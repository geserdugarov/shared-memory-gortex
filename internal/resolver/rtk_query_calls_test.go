package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func rtkEndpointNode(g *graph.Graph, id, file, endpoint, kind string) {
	g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: "api." + endpoint, FilePath: file, Meta: map[string]any{"rtk_endpoint": endpoint, "rtk_kind": kind}})
}

func rtkHookNode(g *graph.Graph, id, file, name string) {
	g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: name, FilePath: file, Meta: map[string]any{"rtk_generated_hook": true}})
}

func synthRTKEdge(g graph.Store, from, to string) *graph.Edge {
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.From != from || e.To != to || e.Meta == nil {
			continue
		}
		if by, _ := e.Meta[MetaSynthesizedBy].(string); by == SynthRTKQuery {
			return e
		}
	}
	return nil
}

func TestResolveRTKQueryCalls_HookToEndpointAndComponentToHook(t *testing.T) {
	g := graph.New()
	rtkEndpointNode(g, "api.ts::api.getUser", "api.ts", "getUser", "query")
	rtkHookNode(g, "api.ts::useGetUserQuery", "api.ts", "useGetUserQuery")
	// generated-hook → endpoint placeholder.
	g.AddEdge(&graph.Edge{From: "api.ts::useGetUserQuery", To: "unresolved::*.getUser", Kind: graph.EdgeCalls, FilePath: "api.ts", Meta: map[string]any{"via": rtkQueryVia, "rtk_endpoint": "getUser"}})
	// component → hook, left unresolved by the generic resolver (cross-file).
	g.AddNode(&graph.Node{ID: "page.tsx::Profile", Kind: graph.KindFunction, Name: "Profile", FilePath: "page.tsx"})
	g.AddEdge(&graph.Edge{From: "page.tsx::Profile", To: "unresolved::useGetUserQuery", Kind: graph.EdgeCalls, FilePath: "page.tsx"})

	n := ResolveRTKQueryCalls(g)
	require.Equal(t, 2, n)

	hookToEp := synthRTKEdge(g, "api.ts::useGetUserQuery", "api.ts::api.getUser")
	require.NotNil(t, hookToEp, "hook should reach its endpoint")
	assert.Equal(t, ConfidenceTyped, hookToEp.Confidence)
	assert.Equal(t, ProvenanceFramework, hookToEp.Meta[MetaProvenance])

	compToHook := synthRTKEdge(g, "page.tsx::Profile", "api.ts::useGetUserQuery")
	require.NotNil(t, compToHook, "component call binds to the generated hook")
	assert.Equal(t, ProvenanceFramework, compToHook.Meta[MetaProvenance])
}

func TestResolveRTKQueryCalls_UnknownEndpointStaysPlaceholder(t *testing.T) {
	g := graph.New()
	rtkHookNode(g, "api.ts::useFooQuery", "api.ts", "useFooQuery")
	rtkEndpointNode(g, "api.ts::api.bar", "api.ts", "bar", "query") // different endpoint
	g.AddEdge(&graph.Edge{From: "api.ts::useFooQuery", To: "unresolved::*.foo", Kind: graph.EdgeCalls, FilePath: "api.ts", Meta: map[string]any{"via": rtkQueryVia, "rtk_endpoint": "foo"}})

	assert.Equal(t, 0, ResolveRTKQueryCalls(g))
	assert.Nil(t, synthRTKEdge(g, "api.ts::useFooQuery", "api.ts::api.bar"))
}

func TestResolveRTKQueryCalls_SameCreateApiFileGate(t *testing.T) {
	// An endpoint named getUser in a different file must not bind the hook.
	g := graph.New()
	rtkHookNode(g, "a.ts::useGetUserQuery", "a.ts", "useGetUserQuery")
	rtkEndpointNode(g, "b.ts::api.getUser", "b.ts", "getUser", "query")
	g.AddEdge(&graph.Edge{From: "a.ts::useGetUserQuery", To: "unresolved::*.getUser", Kind: graph.EdgeCalls, FilePath: "a.ts", Meta: map[string]any{"via": rtkQueryVia, "rtk_endpoint": "getUser"}})

	assert.Equal(t, 0, ResolveRTKQueryCalls(g), "cross-file endpoint must not bind")
	assert.Nil(t, synthRTKEdge(g, "a.ts::useGetUserQuery", "b.ts::api.getUser"))
}
