package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// fnValueCandidateEdge mirrors what the per-language capture emits: a
// placeholder reference into the fn-value namespace, carrying the captured name
// in Meta for the gate to bind.
func fnValueCandidateEdge(from, name, file string, line int) *graph.Edge {
	return &graph.Edge{
		From:     from,
		To:       fnValueUnresolvedPrefix + name,
		Kind:     graph.EdgeReferences,
		FilePath: file,
		Line:     line,
		Origin:   graph.OriginSpeculative,
		Meta: map[string]any{
			"via":           fnValueCandidateVia,
			"fn_value_name": name,
		},
	}
}

const fnValueUnresolvedPrefix = "unresolved::fnvalue::"

func boundCallbackEdge(g graph.Store, from, to string) *graph.Edge {
	for _, e := range g.GetOutEdges(from) {
		if e.To != to || e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v == fnValueRegistrationVia {
			return e
		}
	}
	return nil
}

// TestCallbackGateRejectsUnboundIdentifiers is the A3 named test: the gate binds
// a captured value-position identifier that names a same-file function and
// drops one that resolves to nothing, and the bound edge rides a filterable
// provenance tier rather than a flat heuristic flag.
func TestCallbackGateRejectsUnboundIdentifiers(t *testing.T) {
	g := graph.New()
	// A real same-file function the registration can bind to.
	g.AddNode(&graph.Node{
		ID: "router.go::handler", Kind: graph.KindFunction, Name: "handler",
		FilePath: "router.go", StartLine: 10, Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "router.go::register", Kind: graph.KindFunction, Name: "register",
		FilePath: "router.go", StartLine: 3, Language: "go",
	})
	// One bindable candidate (handler exists) and one unbound (ghost is a
	// local / undefined name — never a function node in this file).
	g.AddEdge(fnValueCandidateEdge("router.go::register", "handler", "router.go", 4))
	g.AddEdge(fnValueCandidateEdge("router.go::register", "ghost", "router.go", 5))
	// A builtin-shaped candidate must also be skipped before any lookup.
	g.AddEdge(fnValueCandidateEdge("router.go::register", "nil", "router.go", 6))

	landed := ResolveFnValueCallbacks(g)
	assert.Equal(t, 1, landed, "only the bound candidate should land")

	bound := boundCallbackEdge(g, "router.go::register", "router.go::handler")
	require.NotNil(t, bound, "the bound handler should produce a callback-registration edge")
	assert.Equal(t, graph.EdgeReferences, bound.Kind)
	assert.Equal(t, graph.OriginASTInferred, bound.Origin, "callback edge must ride a filterable tier")
	assert.Equal(t, SynthFnValueCallback, bound.Meta[MetaSynthesizedBy])
	assert.Equal(t, "handler", bound.Meta["fn_value_name"])

	// The unbound and builtin candidates must not have produced any real edge.
	for _, e := range g.GetOutEdges("router.go::register") {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v == fnValueRegistrationVia {
			assert.Equal(t, "router.go::handler", e.To, "no registration edge should bind ghost/nil")
		}
	}
}

// TestCallbackGateIdempotent confirms a second pass lands nothing new — the
// synthesizer is a safe full-recompute.
func TestCallbackGateIdempotent(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "h.go::onClick", Kind: graph.KindFunction, Name: "onClick",
		FilePath: "h.go", StartLine: 8, Language: "go",
	})
	g.AddEdge(fnValueCandidateEdge("h.go::wire", "onClick", "h.go", 2))

	first := ResolveFnValueCallbacks(g)
	second := ResolveFnValueCallbacks(g)
	assert.Equal(t, 1, first)
	assert.Equal(t, 1, second, "the bound edge re-derives identically; AddEdge dedupes")
}
