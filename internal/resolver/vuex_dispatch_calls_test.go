package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func vuexAction(g *graph.Graph, id, file, namespace, name, kind string) {
	g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: kind + "s." + name, FilePath: file,
		Meta: map[string]any{"vuex_action": name, "vuex_namespace": namespace, "vuex_kind": kind}})
}

func vuexCall(g *graph.Graph, fromID, file, key, kind string) {
	if g.GetNode(fromID) == nil {
		g.AddNode(&graph.Node{ID: fromID, Kind: graph.KindFunction, Name: lastSeg(fromID), FilePath: file})
	}
	name := key
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '/' {
			name = key[i+1:]
			break
		}
	}
	g.AddEdge(&graph.Edge{From: fromID, To: "unresolved::*." + name, Kind: graph.EdgeCalls, FilePath: file,
		Meta: map[string]any{"via": vuexDispatchVia, "vuex_key": key, "vuex_kind": kind}})
}

func synthVuexEdge(g graph.Store, from, to string) *graph.Edge {
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.From != from || e.To != to || e.Meta == nil {
			continue
		}
		if by, _ := e.Meta[MetaSynthesizedBy].(string); by == SynthVuexDispatch {
			return e
		}
	}
	return nil
}

func TestResolveVuexDispatchCalls_NamespaceDisambiguation(t *testing.T) {
	g := graph.New()
	vuexAction(g, "store.ts::actions.login@root", "store.ts", "", "login", "action")
	vuexAction(g, "store.ts::actions.login@user", "store.ts", "user", "login", "action")
	vuexAction(g, "store.ts::mutations.SET_TOKEN@user", "store.ts", "user", "SET_TOKEN", "mutation")
	vuexCall(g, "store.ts::caller", "store.ts", "user/login", "action")
	vuexCall(g, "store.ts::caller", "store.ts", "user/SET_TOKEN", "mutation")
	vuexCall(g, "store.ts::root", "store.ts", "login", "action")

	n := ResolveVuexDispatchCalls(g)
	require.Equal(t, 3, n)

	userLogin := synthVuexEdge(g, "store.ts::caller", "store.ts::actions.login@user")
	require.NotNil(t, userLogin, "user/login binds to the user module action")
	assert.Equal(t, ConfidenceHeuristic, userLogin.Confidence)
	assert.Equal(t, ProvenanceHeuristic, userLogin.Meta[MetaProvenance])

	assert.NotNil(t, synthVuexEdge(g, "store.ts::caller", "store.ts::mutations.SET_TOKEN@user"), "commit binds to the mutation")
	// Root login binds to the root action, never the user one.
	assert.NotNil(t, synthVuexEdge(g, "store.ts::root", "store.ts::actions.login@root"))
	assert.Nil(t, synthVuexEdge(g, "store.ts::root", "store.ts::actions.login@user"))
}

func TestResolveVuexDispatchCalls_KindKeeping(t *testing.T) {
	// A dispatch must not bind to a same-named mutation, and vice versa.
	g := graph.New()
	vuexAction(g, "s.ts::mutations.reset@x", "s.ts", "", "reset", "mutation")
	vuexCall(g, "s.ts::c", "s.ts", "reset", "action") // dispatch('reset'), but only a mutation exists

	assert.Equal(t, 0, ResolveVuexDispatchCalls(g))
	assert.Nil(t, synthVuexEdge(g, "s.ts::c", "s.ts::mutations.reset@x"))
}

func TestResolveVuexDispatchCalls_UnknownKeyStaysPlaceholder(t *testing.T) {
	g := graph.New()
	vuexAction(g, "s.ts::actions.known@x", "s.ts", "", "known", "action")
	vuexCall(g, "s.ts::c", "s.ts", "ghost", "action")

	assert.Equal(t, 0, ResolveVuexDispatchCalls(g))
}
