package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func flutterSetStateEdgeBetween(g graph.Store, from, to string) *graph.Edge {
	for _, e := range g.GetOutEdges(from) {
		if e.To == to && e.Kind == graph.EdgeCalls && e.Meta != nil {
			if v, _ := e.Meta["via"].(string); v == flutterSetStateVia {
				return e
			}
		}
	}
	return nil
}

// flutterState wires a State class with the given methods and setState callers.
func flutterState(g graph.Store, file, class string, methods []string, setStateCallers map[string]bool) {
	g.AddNode(&graph.Node{ID: file + "::" + class, Kind: graph.KindType, Name: class, FilePath: file})
	for i, m := range methods {
		id := file + "::" + class + "." + m
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindMethod, Name: m, FilePath: file, StartLine: 5 + i})
		g.AddEdge(&graph.Edge{From: id, To: file + "::" + class, Kind: graph.EdgeMemberOf})
		if setStateCallers[m] {
			g.AddEdge(&graph.Edge{From: id, To: "unresolved::*.setState", Kind: graph.EdgeCalls, FilePath: file, Line: 6 + i})
		}
	}
}

func TestResolveFlutterSetState_LinksSetterToBuild(t *testing.T) {
	g := graph.New()
	flutterState(g, "counter.dart", "_CounterState",
		[]string{"increment", "build", "noop"},
		map[string]bool{"increment": true})

	n := ResolveFlutterSetStateCalls(g)
	assert.Equal(t, 1, n)

	e := flutterSetStateEdgeBetween(g, "counter.dart::_CounterState.increment", "counter.dart::_CounterState.build")
	require.NotNil(t, e, "increment (calls setState) should reach build")
	assert.Equal(t, "counter.dart::_CounterState", e.Meta["state_class"])
	assert.Equal(t, SynthFlutterSetState, e.Meta[MetaSynthesizedBy])

	assert.Nil(t, flutterSetStateEdgeBetween(g, "counter.dart::_CounterState.noop", "counter.dart::_CounterState.build"))
}

func TestResolveFlutterSetState_NoBuildNoEdge(t *testing.T) {
	g := graph.New()
	flutterState(g, "svc.dart", "Svc", []string{"update"}, map[string]bool{"update": true})
	assert.Equal(t, 0, ResolveFlutterSetStateCalls(g))
}

func TestResolveFlutterSetState_Idempotent(t *testing.T) {
	g := graph.New()
	flutterState(g, "counter.dart", "_CounterState", []string{"increment", "build"}, map[string]bool{"increment": true})
	first := ResolveFlutterSetStateCalls(g)
	second := ResolveFlutterSetStateCalls(g)
	assert.Equal(t, first, second)

	count := 0
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e != nil && e.Meta != nil {
			if v, _ := e.Meta["via"].(string); v == flutterSetStateVia {
				count++
			}
		}
	}
	assert.Equal(t, 1, count)
}
