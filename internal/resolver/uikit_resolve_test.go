package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func synthUIKitEdge(g graph.Store, kind graph.EdgeKind, from, to string) *graph.Edge {
	for e := range g.EdgesByKind(kind) {
		if e == nil || e.From != from || e.To != to || e.Meta == nil {
			continue
		}
		if by, _ := e.Meta[MetaSynthesizedBy].(string); by == SynthUIKitResolve {
			return e
		}
	}
	return nil
}

func TestResolveUIKitRefs_CellAndControllerAndDelegate(t *testing.T) {
	g := graph.New()
	const vc = "App/ViewControllers/FeedViewController.swift::FeedViewController"
	g.AddNode(&graph.Node{ID: vc, Kind: graph.KindType, Name: "FeedViewController", FilePath: "App/ViewControllers/FeedViewController.swift", Language: "swift"})

	convNode(g, "App/Cells/PostCell.swift::PostCell", "App/Cells/PostCell.swift", "PostCell")
	convNode(g, "App/ViewControllers/DetailViewController.swift::DetailViewController", "App/ViewControllers/DetailViewController.swift", "DetailViewController")
	// A delegate co-located with the controller (same-dir tier).
	convNode(g, "App/ViewControllers/FeedDelegate.swift::FeedDelegate", "App/ViewControllers/FeedDelegate.swift", "FeedDelegate")

	g.AddEdge(&graph.Edge{From: vc, To: "unresolved::PostCell", Kind: graph.EdgeInstantiates, FilePath: "App/ViewControllers/FeedViewController.swift"})
	g.AddEdge(&graph.Edge{From: vc, To: "unresolved::DetailViewController", Kind: graph.EdgeReferences, FilePath: "App/ViewControllers/FeedViewController.swift"})
	g.AddEdge(&graph.Edge{From: vc, To: "unresolved::FeedDelegate", Kind: graph.EdgeInstantiates, FilePath: "App/ViewControllers/FeedViewController.swift"})

	require.Equal(t, 3, ResolveUIKitRefs(g))
	assert.NotNil(t, synthUIKitEdge(g, graph.EdgeInstantiates, vc, "App/Cells/PostCell.swift::PostCell"),
		"*Cell binds to /Cells/")
	assert.NotNil(t, synthUIKitEdge(g, graph.EdgeReferences, vc, "App/ViewControllers/DetailViewController.swift::DetailViewController"),
		"*ViewController binds to /ViewControllers/")
	assert.NotNil(t, synthUIKitEdge(g, graph.EdgeInstantiates, vc, "App/ViewControllers/FeedDelegate.swift::FeedDelegate"),
		"*Delegate binds via the caller's own directory")
}

func TestResolveUIKitRefs_AmbiguousCellLeftAlone(t *testing.T) {
	g := graph.New()
	const vc = "App/V.swift::V"
	g.AddNode(&graph.Node{ID: vc, Kind: graph.KindType, Name: "V", FilePath: "App/V.swift", Language: "swift"})
	convNode(g, "a/PostCell.swift::PostCell", "a/PostCell.swift", "PostCell")
	convNode(g, "b/PostCell.swift::PostCell", "b/PostCell.swift", "PostCell")
	g.AddEdge(&graph.Edge{From: vc, To: "unresolved::PostCell", Kind: graph.EdgeInstantiates, FilePath: "App/V.swift"})

	require.Equal(t, 0, ResolveUIKitRefs(g))
}

func TestResolveUIKitRefs_NonAppleLeftAlone(t *testing.T) {
	g := graph.New()
	const goFn = "pkg/svc.go::Use"
	g.AddNode(&graph.Node{ID: goFn, Kind: graph.KindFunction, Name: "Use", FilePath: "pkg/svc.go", Language: "go"})
	convNode(g, "App/Cells/PostCell.swift::PostCell", "App/Cells/PostCell.swift", "PostCell")
	g.AddEdge(&graph.Edge{From: goFn, To: "unresolved::PostCell", Kind: graph.EdgeInstantiates, FilePath: "pkg/svc.go"})

	require.Equal(t, 0, ResolveUIKitRefs(g))
}
