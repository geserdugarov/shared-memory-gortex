package mcp

import (
	"fmt"
	"sort"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/workspace"
)

// ToolScope is the per-tool scope kind. Wire-format encodes this on
// every tool definition (`scope` field on the GCX1 schema). The
// in-memory registry below is the server-side equivalent: it lets the
// dispatcher validate `repo` per call without re-deriving the scope
// from the tool's name on every request.
//
// Three kinds, exhaustive:
//
//   - ScopeRepo — operates against one project's index. Requires
//     `repo: <string>`. Missing or list = protocol error.
//   - ScopeWorkspace — operates at the workspace level. `repo` MUST
//     NOT appear. Present = protocol error.
//   - ScopeFanOut — spans several indexes. Requires
//     `repo: <list>` (non-empty, including the `["*"]` sentinel).
//     Missing, empty, or non-list = protocol error.
//
// Single-project mode degrades these; see ResolveScopedRepos.
type ToolScope int

const (
	// ScopeRepo — `repo: <string>` required (workspace mode); defaults
	// to the bound project in single-project mode.
	ScopeRepo ToolScope = iota + 1
	// ScopeWorkspace — no `repo` parameter. Operates against the
	// active workspace as a unit.
	ScopeWorkspace
	// ScopeFanOut — `repo: <list>` required; `["*"]` resolves to all
	// auto-discovered, non-excluded members of the active workspace.
	ScopeFanOut
)

// String returns the wire-form scope name (matches gcx-go encoding).
func (s ToolScope) String() string {
	switch s {
	case ScopeRepo:
		return "repo"
	case ScopeWorkspace:
		return "workspace"
	case ScopeFanOut:
		return "fan-out"
	default:
		return "unknown"
	}
}

// FanOutAllSentinel is the wire-format `["*"]` sentinel.
// Server-side, it expands to every auto-discovered,
// non-excluded member of the active workspace and
// nothing else (workspace-isolation invariant).
const FanOutAllSentinel = "*"

// scopeRegistry is the per-Server tool-name → scope map. Populated as
// tools are registered via RegisterToolScope; consulted by
// ResolveScopedRepos before any handler runs. Concurrent-safe so the
// daemon path can register tools after construction without surprises.
type scopeRegistry struct {
	mu     sync.RWMutex
	scopes map[string]ToolScope
}

func newScopeRegistry() *scopeRegistry {
	return &scopeRegistry{scopes: make(map[string]ToolScope)}
}

// set assigns scope to toolName. Last write wins — re-registering a
// tool with a different scope is allowed (used by tests) but logged at
// the call site.
func (r *scopeRegistry) set(toolName string, scope ToolScope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scopes[toolName] = scope
}

// get returns the scope for toolName and whether it was registered.
func (r *scopeRegistry) get(toolName string) (ToolScope, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.scopes[toolName]
	return s, ok
}

// snapshot returns a copy of the registry as a tool-name → scope-name
// map, sorted by name. Used by `list_repos`-shaped diagnostics and by
// tests that want to assert no tool has been left unclassified.
func (r *scopeRegistry) snapshot() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.scopes))
	for name, scope := range r.scopes {
		out[name] = scope.String()
	}
	return out
}

// allTools returns the registered tool names sorted lexically.
func (r *scopeRegistry) allTools() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.scopes))
	for name := range r.scopes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ScopedRepos is the resolved repo set for a single MCP tool call. The
// kind matches the tool's declared scope; Repos lists the concrete
// member names the handler should query.
type ScopedRepos struct {
	Kind ToolScope
	// Repos is the resolved set the handler should target. Always
	// non-empty when the call is valid. For ScopeWorkspace it carries
	// every active workspace member (so handlers can present a
	// degenerate one-member workspace uniformly in single-project
	// mode). For ScopeRepo it contains exactly one name. For
	// ScopeFanOut it contains the explicit subset, with `["*"]`
	// already expanded.
	Repos []string
}

// ResolveScopedRepos validates a request against the tool's declared
// scope and the active workspace bind. Returns the concrete repo list
// to query, or a structured protocol error suitable for the caller to
// surface verbatim.
//
// The bind argument is the active server bind. nil means "no bind has
// been established yet"; callers should pass through the request
// (legacy single-repo mode) — this preserves byte-for-byte
// backwards-compat for repos that have never been touched by the new
// handshake.
//
// The repo argument is the union value the wire-format slice decoded
// from the request: nil means "absent", a string means "single-repo
// shape", a slice means "fan-out shape". Callers wrap their
// `req.GetArguments()["repo"]` once and forward to us.
func ResolveScopedRepos(scope ToolScope, bind *workspace.Bind, repo any) (*ScopedRepos, *mcp.CallToolResult) {
	switch scope {
	case ScopeRepo:
		return resolveRepoScope(bind, repo)
	case ScopeWorkspace:
		return resolveWorkspaceScope(bind, repo)
	case ScopeFanOut:
		return resolveFanOutScope(bind, repo)
	default:
		return nil, NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   fmt.Sprintf("unknown tool scope %v", scope),
		})
	}
}

// resolveRepoScope: scope=repo. Wire-format shape: `repo: <string>`.
//
//   - Workspace mode: `repo` REQUIRED. Missing or list = protocol
//     error. Unknown name = protocol error.
//   - Single-project mode: `repo` defaults to the bound project when
//     omitted; explicit `repo` must match the bound project (otherwise
//     reject — mode degradation.
func resolveRepoScope(bind *workspace.Bind, repo any) (*ScopedRepos, *mcp.CallToolResult) {
	switch v := repo.(type) {
	case nil:
		// Default to the bound project in single-project mode.
		if bind != nil && bind.Mode == workspace.ModeSingleProject {
			return &ScopedRepos{Kind: ScopeRepo, Repos: []string{bind.Members[0].Name}}, nil
		}
		// Workspace mode (or no bind): absent `repo` is a protocol error.
		return nil, NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   `scope "repo" requires a string "repo" parameter; in workspace mode, name the project explicitly`,
			Data:      map[string]any{"scope": "repo", "expected": "string"},
		})
	case string:
		if v == "" {
			return nil, NewStructuredErrorResult(StructuredError{
				ErrorCode: ErrCodeInvalidArgument,
				Message:   `"repo" must not be the empty string`,
				Data:      map[string]any{"scope": "repo"},
			})
		}
		if bind != nil && !bind.HasMember(v) {
			return nil, unknownRepoError(bind, v)
		}
		return &ScopedRepos{Kind: ScopeRepo, Repos: []string{v}}, nil
	case []any, []string:
		return nil, NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   `scope "repo" requires a single string for "repo"; got a list — use a fan-out tool if you mean to query multiple projects`,
			Data:      map[string]any{"scope": "repo", "got": "list"},
		})
	default:
		return nil, NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   fmt.Sprintf(`scope "repo" requires a string "repo"; got %T`, repo),
			Data:      map[string]any{"scope": "repo"},
		})
	}
}

// resolveWorkspaceScope: scope=workspace. Wire-format shape: `repo`
// MUST NOT appear. Single-project mode degrades to a one-member
// degenerate workspace.
func resolveWorkspaceScope(bind *workspace.Bind, repo any) (*ScopedRepos, *mcp.CallToolResult) {
	if repo != nil {
		return nil, NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   `scope "workspace" tools must not pass "repo"; the tool operates on the active workspace as a unit`,
			Data:      map[string]any{"scope": "workspace"},
		})
	}
	if bind == nil {
		// No bind established — return an empty list; degenerates
		// gracefully in legacy single-repo mode where there's no
		// workspace concept.
		return &ScopedRepos{Kind: ScopeWorkspace}, nil
	}
	return &ScopedRepos{Kind: ScopeWorkspace, Repos: bind.MemberNames()}, nil
}

// resolveFanOutScope: scope=fan-out. Wire-format shape:
// `repo: <non-empty list>`. `["*"]` expands to every workspace member.
//
//   - Workspace mode: `repo` REQUIRED and must be a non-empty list.
//     Unknown member name = protocol error (Q1 resolution).
//   - Single-project mode: `repo` defaults to `[bound project]` when
//     omitted; `["*"]` resolves to the same one-member set.
func resolveFanOutScope(bind *workspace.Bind, repo any) (*ScopedRepos, *mcp.CallToolResult) {
	switch v := repo.(type) {
	case nil:
		if bind != nil && bind.Mode == workspace.ModeSingleProject {
			return &ScopedRepos{Kind: ScopeFanOut, Repos: []string{bind.Members[0].Name}}, nil
		}
		return nil, NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   `scope "fan-out" requires a non-empty list "repo"; for breadth use ["*"], for a subset name them explicitly`,
			Data:      map[string]any{"scope": "fan-out", "expected": "list"},
		})
	case string:
		return nil, NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   `scope "fan-out" requires a list "repo"; got a single string`,
			Data:      map[string]any{"scope": "fan-out", "got": "string"},
		})
	case []any:
		names := make([]string, 0, len(v))
		for i, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, NewStructuredErrorResult(StructuredError{
					ErrorCode: ErrCodeInvalidArgument,
					Message:   fmt.Sprintf(`"repo"[%d] must be a string, got %T`, i, e),
				})
			}
			names = append(names, s)
		}
		return resolveFanOutNames(bind, names)
	case []string:
		return resolveFanOutNames(bind, v)
	default:
		return nil, NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   fmt.Sprintf(`scope "fan-out" requires a list "repo"; got %T`, repo),
		})
	}
}

// resolveFanOutNames is the inner stage of fan-out resolution. Splits
// the `["*"]` sentinel from the named-subset path; rejects empty
// lists; rejects unknown names.
func resolveFanOutNames(bind *workspace.Bind, names []string) (*ScopedRepos, *mcp.CallToolResult) {
	if len(names) == 0 {
		return nil, NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   `"repo" must be a non-empty list; pass ["*"] for the full workspace`,
		})
	}

	// `["*"]` sentinel: must be the only element; mixed `["*", "x"]`
	// is rejected to keep the wire shape unambiguous.
	for _, n := range names {
		if n == FanOutAllSentinel && len(names) != 1 {
			return nil, NewStructuredErrorResult(StructuredError{
				ErrorCode: ErrCodeInvalidArgument,
				Message:   `"repo" cannot mix the ["*"] sentinel with named entries`,
			})
		}
	}
	if len(names) == 1 && names[0] == FanOutAllSentinel {
		if bind == nil {
			return nil, NewStructuredErrorResult(StructuredError{
				ErrorCode: ErrCodeInvalidArgument,
				Message:   `["*"] requires an active workspace bind`,
			})
		}
		members := bind.MemberNames()
		if len(members) == 0 {
			return nil, NewStructuredErrorResult(StructuredError{
				ErrorCode: ErrCodeInvalidArgument,
				Message:   `["*"] resolved to an empty workspace; nothing to query`,
			})
		}
		return &ScopedRepos{Kind: ScopeFanOut, Repos: members}, nil
	}

	// Named subset: every entry must be a known workspace member.
	if bind != nil {
		for _, n := range names {
			if !bind.HasMember(n) {
				return nil, unknownRepoError(bind, n)
			}
		}
	}
	// Deduplicate while preserving first-seen order so the handler's
	// fan-out is stable for caching / logging.
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return &ScopedRepos{Kind: ScopeFanOut, Repos: out}, nil
}

// unknownRepoError builds the canonical "name not in workspace" error
// for the dispatcher. Reused by ScopeRepo and ScopeFanOut so the wire
// shape is identical regardless of which scope rejected the call.
func unknownRepoError(bind *workspace.Bind, name string) *mcp.CallToolResult {
	available := []string{}
	if bind != nil {
		available = bind.MemberNames()
	}
	return NewStructuredErrorResult(StructuredError{
		ErrorCode: ErrCodeRepoNotTracked,
		Message: fmt.Sprintf(
			`"repo" %q is not a member of the active workspace; valid members: %v`, name, available),
		Retriable: false,
		Data: map[string]any{
			"requested": name,
			"available": available,
		},
	})
}
