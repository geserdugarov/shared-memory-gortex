package astquery

import (
	"sort"
	"strings"
	"sync"

	"github.com/zzet/gortex/internal/parser"
)

// Detector is one named structural rule. The Languages map carries
// per-language tree-sitter S-expression queries; the engine compiles
// them once per run and runs the appropriate one for each target's
// language. PostFilter is an optional second-pass filter that
// receives the raw QueryResult plus the file's source bytes — used
// when a detector needs to do something beyond what tree-sitter
// query predicates support (e.g. "this regex matches the text of
// capture X" combined with structural shape).
type Detector struct {
	Name        string
	Description string
	Severity    string

	// Languages is keyed by the language string stored on KindFile
	// nodes ("go", "python", "typescript", …). Each value is a
	// tree-sitter S-expression. A capture named `match` is the
	// row's anchor; absent that, the engine falls back to the
	// longest captured node.
	Languages map[string]string

	// ExcludeTests defaults to true for detectors — a "panic in
	// library" rule firing inside `_test.go` is noise. Detectors
	// that intentionally inspect tests (e.g. a "test name doesn't
	// match prefix" rule) can flip this to false.
	ExcludeTests bool

	// PostFilter is optional. Return true to keep the match.
	PostFilter func(parser.QueryResult, []byte) bool
}

var (
	detectorMu       sync.RWMutex
	detectorRegistry = map[string]*Detector{}
)

// RegisterDetector adds d to the global detector registry. Called
// from package-level init in detectors.go for each bundled rule.
// Tests may register additional detectors via RegisterDetector — the
// API is intentionally exported so a downstream consumer (e.g. a
// project-specific lint set) can layer rules without forking the
// engine.
func RegisterDetector(d *Detector) {
	if d == nil || d.Name == "" {
		return
	}
	d.normalise()
	detectorMu.Lock()
	detectorRegistry[d.Name] = d
	detectorMu.Unlock()
}

func lookupDetector(name string) (*Detector, bool) {
	detectorMu.RLock()
	defer detectorMu.RUnlock()
	d, ok := detectorRegistry[name]
	return d, ok
}

// ListDetectors returns the names of every registered detector,
// sorted alphabetically. Used by the MCP layer to fail fast with a
// helpful error when a caller passes an unknown detector name.
func ListDetectors() []string {
	detectorMu.RLock()
	defer detectorMu.RUnlock()
	names := make([]string, 0, len(detectorRegistry))
	for n := range detectorRegistry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// DescribeDetectors returns rich metadata for every registered
// detector, suitable for surfacing in the MCP tool description so
// agents can pick the right rule without an out-of-band docs lookup.
func DescribeDetectors() []DetectorInfo {
	detectorMu.RLock()
	defer detectorMu.RUnlock()
	out := make([]DetectorInfo, 0, len(detectorRegistry))
	for _, d := range detectorRegistry {
		langs := make([]string, 0, len(d.Languages))
		for l := range d.Languages {
			langs = append(langs, l)
		}
		sort.Strings(langs)
		out = append(out, DetectorInfo{
			Name:        d.Name,
			Description: d.Description,
			Severity:    d.Severity,
			Languages:   langs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// DetectorInfo is the read-only projection used by the MCP layer.
type DetectorInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Severity    string   `json:"severity"`
	Languages   []string `json:"languages"`
}

func (d *Detector) normalise() {
	d.Name = strings.TrimSpace(d.Name)
	if d.Severity == "" {
		d.Severity = "warning"
	}
	// Normalise language keys to the lowercase, hyphen-free form
	// the engine and the graph use.
	if len(d.Languages) > 0 {
		fixed := make(map[string]string, len(d.Languages))
		for k, v := range d.Languages {
			fixed[strings.ToLower(strings.TrimSpace(k))] = v
		}
		d.Languages = fixed
	}
	// (Tests-exclusion default lives in the engine — see
	// buildPlan; detectors don't need to flip a bit on every
	// entry.)
}
