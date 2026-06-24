package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func synthVaporEdge(g graph.Store, kind graph.EdgeKind, from, to string) *graph.Edge {
	for e := range g.EdgesByKind(kind) {
		if e == nil || e.From != from || e.To != to || e.Meta == nil {
			continue
		}
		if by, _ := e.Meta[MetaSynthesizedBy].(string); by == SynthVaporResolve {
			return e
		}
	}
	return nil
}

func TestResolveVaporRefs_ControllerAndMiddleware(t *testing.T) {
	g := graph.New()
	const route = "Sources/App/routes.swift::routes"
	g.AddNode(&graph.Node{ID: route, Kind: graph.KindFunction, Name: "routes", FilePath: "Sources/App/routes.swift", Language: "swift"})

	convNode(g, "Sources/App/Controllers/UserController.swift::UserController", "Sources/App/Controllers/UserController.swift", "UserController")
	convNode(g, "Sources/App/Middleware/AuthMiddleware.swift::AuthMiddleware", "Sources/App/Middleware/AuthMiddleware.swift", "AuthMiddleware")

	g.AddEdge(&graph.Edge{From: route, To: "unresolved::UserController", Kind: graph.EdgeInstantiates, FilePath: "Sources/App/routes.swift"})
	g.AddEdge(&graph.Edge{From: route, To: "unresolved::AuthMiddleware", Kind: graph.EdgeInstantiates, FilePath: "Sources/App/routes.swift"})

	require.Equal(t, 2, ResolveVaporRefs(g))
	assert.NotNil(t, synthVaporEdge(g, graph.EdgeInstantiates, route, "Sources/App/Controllers/UserController.swift::UserController"),
		"*Controller binds to /Controllers/")
	assert.NotNil(t, synthVaporEdge(g, graph.EdgeInstantiates, route, "Sources/App/Middleware/AuthMiddleware.swift::AuthMiddleware"),
		"*Middleware binds to /Middleware/")
}

func TestResolveVaporRefs_ViewControllerLeftToUIKit(t *testing.T) {
	g := graph.New()
	const route = "Sources/App/routes.swift::routes"
	g.AddNode(&graph.Node{ID: route, Kind: graph.KindFunction, Name: "routes", FilePath: "Sources/App/routes.swift", Language: "swift"})
	convNode(g, "Sources/App/Controllers/HomeViewController.swift::HomeViewController", "Sources/App/Controllers/HomeViewController.swift", "HomeViewController")
	g.AddEdge(&graph.Edge{From: route, To: "unresolved::HomeViewController", Kind: graph.EdgeInstantiates, FilePath: "Sources/App/routes.swift"})

	// A *ViewController is the UIKit pass's shape — Vapor must not claim it.
	require.Equal(t, 0, ResolveVaporRefs(g))
}

func TestResolveVaporRefs_AmbiguousLeftAlone(t *testing.T) {
	g := graph.New()
	const route = "Sources/App/r.swift::r"
	g.AddNode(&graph.Node{ID: route, Kind: graph.KindFunction, Name: "r", FilePath: "Sources/App/r.swift", Language: "swift"})
	convNode(g, "a/UserController.swift::UserController", "a/UserController.swift", "UserController")
	convNode(g, "b/UserController.swift::UserController", "b/UserController.swift", "UserController")
	g.AddEdge(&graph.Edge{From: route, To: "unresolved::UserController", Kind: graph.EdgeInstantiates, FilePath: "Sources/App/r.swift"})

	require.Equal(t, 0, ResolveVaporRefs(g))
}
