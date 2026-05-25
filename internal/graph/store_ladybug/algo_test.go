//go:build ladybug

package store_ladybug

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// seedAlgoTestGraph builds the same hub-and-spoke graph the probe
// used. Two SCC triangles + a hub that every node points at — gives
// PageRank, SCC, Louvain, and K-Core a predictable answer to test
// against without needing a big real corpus.
func seedAlgoTestGraph(t *testing.T) *Store {
	t.Helper()
	dir, err := os.MkdirTemp("", "lbug-algo-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	s, err := Open(filepath.Join(dir, "store.lbug"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	for _, n := range []*graph.Node{
		{ID: "a", Kind: graph.KindFunction, Name: "a", FilePath: "x.go"},
		{ID: "b", Kind: graph.KindFunction, Name: "b", FilePath: "x.go"},
		{ID: "c", Kind: graph.KindFunction, Name: "c", FilePath: "x.go"},
		{ID: "d", Kind: graph.KindFunction, Name: "d", FilePath: "y.go"},
		{ID: "e", Kind: graph.KindFunction, Name: "e", FilePath: "y.go"},
		{ID: "f", Kind: graph.KindFunction, Name: "f", FilePath: "y.go"},
		{ID: "hub", Kind: graph.KindFunction, Name: "hub", FilePath: "z.go"},
	} {
		s.AddNode(n)
	}
	for _, e := range []*graph.Edge{
		{From: "a", To: "b", Kind: graph.EdgeCalls, FilePath: "x.go"},
		{From: "b", To: "c", Kind: graph.EdgeCalls, FilePath: "x.go"},
		{From: "c", To: "a", Kind: graph.EdgeCalls, FilePath: "x.go"},
		{From: "d", To: "e", Kind: graph.EdgeCalls, FilePath: "y.go"},
		{From: "e", To: "f", Kind: graph.EdgeCalls, FilePath: "y.go"},
		{From: "f", To: "d", Kind: graph.EdgeCalls, FilePath: "y.go"},
		{From: "c", To: "d", Kind: graph.EdgeCalls, FilePath: "x.go"},
		{From: "a", To: "hub", Kind: graph.EdgeCalls, FilePath: "x.go"},
		{From: "b", To: "hub", Kind: graph.EdgeCalls, FilePath: "x.go"},
		{From: "c", To: "hub", Kind: graph.EdgeCalls, FilePath: "x.go"},
		{From: "d", To: "hub", Kind: graph.EdgeCalls, FilePath: "y.go"},
		{From: "e", To: "hub", Kind: graph.EdgeCalls, FilePath: "y.go"},
		{From: "f", To: "hub", Kind: graph.EdgeCalls, FilePath: "y.go"},
	} {
		s.AddEdge(e)
	}
	return s
}

func TestPageRanker_RanksHubFirst(t *testing.T) {
	s := seedAlgoTestGraph(t)
	hits, err := s.PageRank(graph.PageRankOpts{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(hits), 7)

	// Hub has six incoming edges (every other node calls it) while
	// triangle nodes only have one or two — PageRank must rank hub
	// first by a clear margin.
	assert.Equal(t, "hub", hits[0].NodeID,
		"hub should rank #1; got %v", hits)
	assert.Greater(t, hits[0].Rank, hits[1].Rank*1.5,
		"hub rank should dominate next-highest by at least 1.5x; got hits=%v", hits)
}

func TestPageRanker_RespectsLimit(t *testing.T) {
	s := seedAlgoTestGraph(t)
	hits, err := s.PageRank(graph.PageRankOpts{Limit: 3})
	require.NoError(t, err)
	assert.Len(t, hits, 3, "Limit=3 must cap the result at 3 rows")
}

func TestPageRanker_RespectsNodeKindFilter(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbug-algo-filter-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	s, err := Open(filepath.Join(dir, "store.lbug"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Two kinds. Only KindFunction should appear when we filter for it.
	for _, n := range []*graph.Node{
		{ID: "fn1", Kind: graph.KindFunction, Name: "fn1", FilePath: "x.go"},
		{ID: "fn2", Kind: graph.KindFunction, Name: "fn2", FilePath: "x.go"},
		{ID: "ty1", Kind: graph.KindType, Name: "ty1", FilePath: "x.go"},
	} {
		s.AddNode(n)
	}
	s.AddEdge(&graph.Edge{From: "fn1", To: "fn2", Kind: graph.EdgeCalls, FilePath: "x.go"})
	s.AddEdge(&graph.Edge{From: "fn1", To: "ty1", Kind: graph.EdgeReferences, FilePath: "x.go"})

	hits, err := s.PageRank(graph.PageRankOpts{
		NodeKinds: []graph.NodeKind{graph.KindFunction},
	})
	require.NoError(t, err)
	for _, h := range hits {
		assert.NotEqual(t, "ty1", h.NodeID, "type node should be excluded by NodeKinds filter; got %v", hits)
	}
}

func TestPageRanker_RespectsTuningKnobs(t *testing.T) {
	s := seedAlgoTestGraph(t)
	// A high damping factor with very few iterations should still
	// produce hub-first ordering — this just exercises the named-arg
	// path so a future binder change can't silently break it.
	hits, err := s.PageRank(graph.PageRankOpts{
		DampingFactor: 0.9,
		MaxIterations: 5,
		Tolerance:     1e-4,
		Limit:         3,
	})
	require.NoError(t, err)
	require.Len(t, hits, 3)
	assert.Equal(t, "hub", hits[0].NodeID)
}

// TestPageRanker_ConsecutiveCallsDoNotLeak validates the project →
// run → drop lifecycle: two back-to-back calls must succeed even
// though they reuse the same projection name. A leaked projection
// from call 1 would make call 2's PROJECT_GRAPH error out.
func TestPageRanker_ConsecutiveCallsDoNotLeak(t *testing.T) {
	s := seedAlgoTestGraph(t)
	for i := 0; i < 3; i++ {
		hits, err := s.PageRank(graph.PageRankOpts{Limit: 1})
		require.NoError(t, err, "consecutive PageRank call %d must succeed", i)
		require.Len(t, hits, 1)
		assert.Equal(t, "hub", hits[0].NodeID)
	}
}
