package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func registryMethod(g *graph.Graph, classID, methodID, file, method string) {
	g.AddNode(&graph.Node{ID: methodID, Kind: graph.KindMethod, Name: method, FilePath: file, Language: "javascript"})
	g.AddEdge(&graph.Edge{From: methodID, To: classID, Kind: graph.EdgeMemberOf, FilePath: file})
}

func registryDispatch(g *graph.Graph, fromID, file, className, method string, line int) {
	if g.GetNode(fromID) == nil {
		g.AddNode(&graph.Node{ID: fromID, Kind: graph.KindMethod, Name: lastSeg(fromID), FilePath: file, Language: "javascript"})
	}
	g.AddEdge(&graph.Edge{
		From: fromID, To: "unresolved::*." + className, Kind: graph.EdgeCalls, FilePath: file, Line: line,
		Meta: map[string]any{"via": objectRegistryVia, "registry_value": className, "registry_method": method},
	})
}

func synthRegistryEdge(g graph.Store, from, to string) *graph.Edge {
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.From != from || e.To != to || e.Meta == nil {
			continue
		}
		if by, _ := e.Meta[MetaSynthesizedBy].(string); by == SynthObjectRegistry {
			return e
		}
	}
	return nil
}

func TestResolveObjectRegistryCalls_DispatchToHandlerMethod(t *testing.T) {
	g := graph.New()
	registryMethod(g, "bus.js::AddCommand", "bus.js::AddCommand.execute", "bus.js", "execute")
	registryMethod(g, "bus.js::RmCommand", "bus.js::RmCommand.execute", "bus.js", "execute")
	registryDispatch(g, "bus.js::Bus.run", "bus.js", "AddCommand", "execute", 9)
	registryDispatch(g, "bus.js::Bus.run", "bus.js", "RmCommand", "execute", 9)

	n := ResolveObjectRegistryCalls(g)
	require.Equal(t, 2, n)

	e := synthRegistryEdge(g, "bus.js::Bus.run", "bus.js::AddCommand.execute")
	require.NotNil(t, e, "dispatcher should reach the handler's execute method")
	assert.Equal(t, ConfidenceHeuristic, e.Confidence)
	assert.Equal(t, ProvenanceHeuristic, e.Meta[MetaProvenance])
	assert.NotNil(t, synthRegistryEdge(g, "bus.js::Bus.run", "bus.js::RmCommand.execute"))
}

func TestResolveObjectRegistryCalls_FallbackToExecuteConvention(t *testing.T) {
	// No method recorded at the dispatch site → resolve via the
	// execute/run/handle convention (here, run).
	g := graph.New()
	registryMethod(g, "h.js::Job", "h.js::Job.run", "h.js", "run")
	registryDispatch(g, "h.js::dispatcher", "h.js", "Job", "", 3)

	require.Equal(t, 1, ResolveObjectRegistryCalls(g))
	assert.NotNil(t, synthRegistryEdge(g, "h.js::dispatcher", "h.js::Job.run"))
}

func TestResolveObjectRegistryCalls_CollisionPrefersSameFile(t *testing.T) {
	g := graph.New()
	registryMethod(g, "a.js::Cmd", "a.js::Cmd.execute", "a.js", "execute")
	registryMethod(g, "b.js::Cmd", "b.js::Cmd.execute", "b.js", "execute")
	registryDispatch(g, "a.js::disp", "a.js", "Cmd", "execute", 2)

	ResolveObjectRegistryCalls(g)
	assert.NotNil(t, synthRegistryEdge(g, "a.js::disp", "a.js::Cmd.execute"), "prefers the same-file handler")
	assert.Nil(t, synthRegistryEdge(g, "a.js::disp", "b.js::Cmd.execute"))
}

func TestResolveObjectRegistryCalls_ClaimsSiteFromSpeculative(t *testing.T) {
	g := graph.New()
	registryMethod(g, "bus.js::AddCommand", "bus.js::AddCommand.execute", "bus.js", "execute")
	registryDispatch(g, "bus.js::Bus.run", "bus.js", "AddCommand", "execute", 9)
	// An unrelated execute method the speculative pass would otherwise reach.
	g.AddNode(&graph.Node{ID: "other.js::Other.execute", Kind: graph.KindMethod, Name: "execute", FilePath: "other.js", Language: "javascript"})
	// The parallel speculative dyn_shape edge at the same dispatch site.
	g.AddEdge(&graph.Edge{
		From: "bus.js::Bus.run", To: "unresolved::execute", Kind: graph.EdgeCalls, FilePath: "bus.js", Line: 9,
		Meta: map[string]any{"dyn_shape": "computed_member", "dyn_key": "execute"},
	})

	ResolveObjectRegistryCalls(g)

	// The dyn_shape edge is now marked claimed...
	var claimed bool
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil {
			continue
		}
		if s, _ := e.Meta["dyn_shape"].(string); s == "" {
			continue
		}
		if c, _ := e.Meta["registry_claimed"].(bool); c {
			claimed = true
		}
	}
	assert.True(t, claimed, "object-registry must claim the dispatch site")

	// ...so the speculative pass mints no hidden edge for it.
	assert.Equal(t, 0, ResolveSpeculativeDispatch(g, true), "claimed site suppresses speculative dispatch")
}
