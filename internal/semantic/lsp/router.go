package lsp

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Router is a daemon-managed pool of LSP providers keyed by ServerSpec.
// It routes requests to the right provider by file extension, spawns
// providers lazily on first touch, and reaps idle ones to bound the
// number of subprocesses kept alive.
//
// Usage shape:
//
//	r := NewRouter(workspaceRoot, logger).WithIdleTimeout(10*time.Minute)
//	p, err := r.For("path/to/file.rs") // provider for rust-analyzer
//	if err != nil { ... }
//	d, _ := p.LastDiagnostics(absPath)
//
// Lifecycle:
//   - First For() call per spec: ServerSpec.Command must be on PATH
//     or one of AlternativeCommands must resolve. Failure returns the
//     unresolvable spec name in the error.
//   - Subsequent For() calls reuse the cached provider.
//   - Close() shuts every provider down deterministically.
//   - Reap() (best-effort, called from a tick goroutine when
//     WithReaperInterval is set) closes providers idle longer than
//     IdleTimeout.
type Router struct {
	workspaceRoot string
	logger        *zap.Logger

	mu        sync.Mutex
	providers map[string]*routedProvider // spec.Name → cached provider

	// limits — zero means "no limit / no reaping".
	idleTimeout    time.Duration
	reaperInterval time.Duration
	maxAlive       int

	stopReaper chan struct{}

	// availability cache — checking exec.LookPath has measurable
	// overhead on Windows / WSL filesystems, and the answer is
	// stable for the life of the process.
	availMu sync.RWMutex
	avail   map[string]bool // spec.Name → resolved on PATH
}

type routedProvider struct {
	spec     *ServerSpec
	provider *Provider
	lastUsed time.Time
}

// NewRouter constructs an empty Router. workspaceRoot is the directory
// passed to LSP servers as `rootUri`.
func NewRouter(workspaceRoot string, logger *zap.Logger) *Router {
	if logger == nil {
		logger = zap.NewNop()
	}
	abs, _ := filepath.Abs(workspaceRoot)
	return &Router{
		workspaceRoot: abs,
		logger:        logger,
		providers:     make(map[string]*routedProvider),
		avail:         make(map[string]bool),
	}
}

// WithIdleTimeout sets how long a provider can be idle before Reap()
// will shut it down.
func (r *Router) WithIdleTimeout(d time.Duration) *Router {
	r.idleTimeout = d
	return r
}

// WithReaperInterval starts a background reaper that calls Reap() at
// the given cadence. Idempotent — calling twice replaces the previous
// reaper. A zero duration disables reaping.
func (r *Router) WithReaperInterval(d time.Duration) *Router {
	r.mu.Lock()
	if r.stopReaper != nil {
		close(r.stopReaper)
		r.stopReaper = nil
	}
	if d > 0 {
		stop := make(chan struct{})
		r.stopReaper = stop
		go r.reaperLoop(d, stop)
	}
	r.reaperInterval = d
	r.mu.Unlock()
	return r
}

// WithMaxAlive caps the number of concurrent live providers. When
// exceeded, the least-recently-used provider is evicted.
func (r *Router) WithMaxAlive(n int) *Router {
	r.maxAlive = n
	return r
}

// For returns the provider responsible for the given file path. It
// spawns the LSP subprocess on first call. relPath may be either an
// absolute path or relative to the router's workspace root.
func (r *Router) For(relPath string) (*Provider, error) {
	spec := SpecForPath(relPath)
	if spec == nil {
		return nil, fmt.Errorf("no LSP server registered for %s", filepath.Ext(relPath))
	}
	return r.ForSpec(spec)
}

// ForSpec returns the provider for a named spec, spawning it on first
// call.
func (r *Router) ForSpec(spec *ServerSpec) (*Provider, error) {
	if !r.specAvailable(spec) {
		return nil, fmt.Errorf("LSP server %q not available on PATH", spec.Name)
	}
	r.mu.Lock()
	rp, ok := r.providers[spec.Name]
	if ok {
		rp.lastUsed = time.Now()
		r.mu.Unlock()
		return rp.provider, nil
	}
	r.mu.Unlock()

	// Spawn outside the lock — initialize() blocks on stdio I/O.
	p := NewProviderFromSpec(spec, r.logger)
	if err := p.EnsureClient(r.workspaceRoot); err != nil {
		return nil, fmt.Errorf("spawn %s: %w", spec.Name, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Race: another goroutine may have spawned it while we were
	// initializing. Prefer the existing one and shut down our duplicate.
	if existing, ok := r.providers[spec.Name]; ok {
		existing.lastUsed = time.Now()
		go func() { _ = p.Close() }()
		return existing.provider, nil
	}
	r.providers[spec.Name] = &routedProvider{
		spec:     spec,
		provider: p,
		lastUsed: time.Now(),
	}
	r.maybeEvictLRULocked()
	return p, nil
}

// Available reports whether at least one of the spec's commands is on
// PATH. Negative results are cached, but a future PATH change between
// calls is the caller's problem.
func (r *Router) Available(spec *ServerSpec) bool {
	return r.specAvailable(spec)
}

// AvailableSpecs lists every spec resolvable on the current PATH. Use
// at startup to log which servers will spin up later.
func (r *Router) AvailableSpecs() []*ServerSpec {
	out := make([]*ServerSpec, 0)
	for _, s := range AllSpecs() {
		if r.specAvailable(s) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// specAvailable returns true when one of spec.Command +
// spec.AlternativeCommands resolves on PATH.
func (r *Router) specAvailable(spec *ServerSpec) bool {
	if spec == nil {
		return false
	}
	r.availMu.RLock()
	v, cached := r.avail[spec.Name]
	r.availMu.RUnlock()
	if cached {
		return v
	}
	avail := false
	if _, err := exec.LookPath(spec.Command); err == nil {
		avail = true
	} else {
		for _, alt := range spec.AlternativeCommands {
			if _, err := exec.LookPath(alt.Command); err == nil {
				avail = true
				break
			}
		}
	}
	r.availMu.Lock()
	r.avail[spec.Name] = avail
	r.availMu.Unlock()
	return avail
}

// LanguageIDForPath proxies to the package-level helper for callers
// that hold a router but not a Provider.
func (r *Router) LanguageIDForPath(path string) string { return LanguageIDForPath(path) }

// Reap closes any provider idle for longer than IdleTimeout. Returns
// the names of reaped specs.
func (r *Router) Reap() []string {
	if r.idleTimeout <= 0 {
		return nil
	}
	cut := time.Now().Add(-r.idleTimeout)
	r.mu.Lock()
	var victims []*routedProvider
	for name, rp := range r.providers {
		if rp.lastUsed.Before(cut) {
			victims = append(victims, rp)
			delete(r.providers, name)
		}
	}
	r.mu.Unlock()
	names := make([]string, 0, len(victims))
	for _, v := range victims {
		names = append(names, v.spec.Name)
		_ = v.provider.Close()
	}
	if len(names) > 0 {
		r.logger.Info("LSP router reaped idle providers", zap.Strings("names", names))
	}
	return names
}

// maybeEvictLRULocked evicts the least-recently-used provider if
// providers exceed maxAlive. Caller must hold r.mu.
func (r *Router) maybeEvictLRULocked() {
	if r.maxAlive <= 0 || len(r.providers) <= r.maxAlive {
		return
	}
	var oldest *routedProvider
	var oldestName string
	for name, rp := range r.providers {
		if oldest == nil || rp.lastUsed.Before(oldest.lastUsed) {
			oldest = rp
			oldestName = name
		}
	}
	if oldest != nil {
		delete(r.providers, oldestName)
		go func() { _ = oldest.provider.Close() }()
		r.logger.Info("LSP router evicted LRU provider", zap.String("name", oldestName))
	}
}

func (r *Router) reaperLoop(d time.Duration, stop chan struct{}) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			r.Reap()
		case <-stop:
			return
		}
	}
}

// Close shuts down every active provider. Safe to call multiple times.
func (r *Router) Close() error {
	r.mu.Lock()
	if r.stopReaper != nil {
		close(r.stopReaper)
		r.stopReaper = nil
	}
	provs := r.providers
	r.providers = make(map[string]*routedProvider)
	r.mu.Unlock()

	var firstErr error
	for _, rp := range provs {
		if err := rp.provider.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Stats reports the live provider names and their last-used times.
// Intended for debug / status endpoints.
type RouterStat struct {
	Spec     string    `json:"spec"`
	LastUsed time.Time `json:"last_used"`
}

// Stats returns one entry per live provider.
func (r *Router) Stats() []RouterStat {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RouterStat, 0, len(r.providers))
	for name, rp := range r.providers {
		out = append(out, RouterStat{Spec: name, LastUsed: rp.lastUsed})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Spec < out[j].Spec })
	return out
}

// Names returns just the names of live providers (helper for tests).
func (r *Router) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.providers))
	for n := range r.providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SupportedLanguages returns the set of languages the router can serve
// (any spec with at least one alt command on PATH). Used to advertise
// capability to MCP clients on startup.
func (r *Router) SupportedLanguages() []string {
	seen := make(map[string]bool)
	for _, s := range r.AvailableSpecs() {
		for _, l := range s.Languages {
			seen[l] = true
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// MarshalDescription returns a human-readable status for one router,
// used by the daemon's `gortex daemon status` command.
func (r *Router) MarshalDescription() string {
	stats := r.Stats()
	var b strings.Builder
	fmt.Fprintf(&b, "lsp-router workspace=%s alive=%d\n", r.workspaceRoot, len(stats))
	for _, s := range stats {
		fmt.Fprintf(&b, "  %s last_used=%s\n", s.Spec, s.LastUsed.Format(time.RFC3339))
	}
	return b.String()
}
