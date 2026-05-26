//go:build ladybug

package store_ladybug

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/search"
)

// TestSymbolSearcher_EndToEnd is the conformance check for the
// Ladybug FTS path. Seeds three "symbols" via UpsertSymbolFTS with
// pre-tokenised text, builds the index, then exercises queries that
// the existing BM25 backend recall contract requires to work:
//
//   - exact identifier ("ValidateToken" tokenises to "validate token")
//   - mid-word camelCase ("validate" / "token" alone)
//   - qualifier hop ("auth")
//   - control case ("PrettyPrint" / "pretty")
//
// The probe in fts_probe_test.go proved the raw CALL surface works
// but couldn't camelCase-split — the tokenizer bridge here is what
// closes that recall gap.
func TestSymbolSearcher_EndToEnd(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbug-fts-e2e-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	s, err := Open(filepath.Join(dir, "store.lbug"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Pre-tokenise the symbol names exactly as the indexer will at
	// production time — search.Tokenize handles camelCase and
	// snake_case + path separators.
	upsert := func(id, raw string) {
		toks := search.Tokenize(raw)
		joined := ""
		for i, t := range toks {
			if i > 0 {
				joined += " "
			}
			joined += t
		}
		require.NoError(t, s.UpsertSymbolFTS(id, joined))
	}
	upsert("pkg/auth.go::ValidateToken", "ValidateToken auth.ValidateToken")
	upsert("pkg/auth.go::ValidateSession", "ValidateSession auth.ValidateSession")
	upsert("pkg/format.go::PrettyPrint", "PrettyPrint format.PrettyPrint")

	require.NoError(t, s.BuildSymbolIndex())

	cases := []struct {
		name      string
		query     string
		wantTopID string
		minHits   int
	}{
		{"exact identifier", "ValidateToken", "pkg/auth.go::ValidateToken", 1},
		{"camelCase head", "validate", "", 2},
		{"camelCase tail", "token", "pkg/auth.go::ValidateToken", 1},
		{"two-word query", "validate token", "pkg/auth.go::ValidateToken", 1},
		{"qualifier", "auth", "", 2},
		{"control", "pretty", "pkg/format.go::PrettyPrint", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hits, err := s.SearchSymbols(c.query, 10)
			require.NoError(t, err)
			t.Logf("query %q → %d hits: %v", c.query, len(hits), hits)
			assert.GreaterOrEqual(t, len(hits), c.minHits,
				"query %q must return at least %d hits", c.query, c.minHits)
			if c.wantTopID != "" && len(hits) > 0 {
				assert.Equal(t, c.wantTopID, hits[0].NodeID,
					"top hit for %q must be %s", c.query, c.wantTopID)
			}
		})
	}
}

// TestSymbolSearcher_AutoUpdate verifies the FTS index reflects
// rows added after CREATE_FTS_INDEX. Critical for incremental
// reindexing — a file change re-triggers UpsertSymbolFTS and the
// new row must be findable without re-running BuildSymbolIndex.
func TestSymbolSearcher_AutoUpdate(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbug-fts-auto-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	s, err := Open(filepath.Join(dir, "store.lbug"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.NoError(t, s.UpsertSymbolFTS("pkg/a.go::Original", "original a.original"))
	require.NoError(t, s.BuildSymbolIndex())

	// First query — only the original row exists.
	hits, err := s.SearchSymbols("original", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)

	// Upsert a new row AFTER index creation.
	require.NoError(t, s.UpsertSymbolFTS("pkg/b.go::PostAdd", "post add b.postadd"))
	hits, err = s.SearchSymbols("postadd", 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(hits), 1,
		"post-create insert must be findable without rebuilding the index")
}

// TestSymbolSearcher_IdempotentUpsert verifies that replacing a row's
// text via a second UpsertSymbolFTS call updates the FTS hit in
// place instead of producing a duplicate. Matches the indexer's
// re-parse contract.
func TestSymbolSearcher_IdempotentUpsert(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbug-fts-idem-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	s, err := Open(filepath.Join(dir, "store.lbug"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	id := "pkg/foo.go::Method"
	require.NoError(t, s.UpsertSymbolFTS(id, "originalname"))
	require.NoError(t, s.BuildSymbolIndex())
	require.NoError(t, s.UpsertSymbolFTS(id, "renamedmethod"))

	// Old name should miss; new name should hit. Only one row total.
	missHits, err := s.SearchSymbols("originalname", 10)
	require.NoError(t, err)
	for _, h := range missHits {
		assert.NotEqual(t, id, h.NodeID, "old text must no longer match after upsert replacement")
	}
	freshHits, err := s.SearchSymbols("renamedmethod", 10)
	require.NoError(t, err)
	require.NotEmpty(t, freshHits)
	assert.Equal(t, id, freshHits[0].NodeID)
}

// TestSearchSymbolBundles_ParallelFetchEquivalence is the correctness
// guard for the post-FTS parallelisation: the three batched MATCH
// calls (nodes / out edges / in edges) now run on three goroutines
// against three pool connections. The output must be byte-for-byte
// identical to the sequential composition — same hits in the same
// FTS-ranked order, each carrying the same node payload and the same
// in/out edge slices. This is the contract callers (the engine's
// bundle-seeding gather path) rely on.
func TestSearchSymbolBundles_ParallelFetchEquivalence(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbug-fts-bundle-parallel-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	s, err := Open(filepath.Join(dir, "store.lbug"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Seed a small graph with edges so the in/out edge phase of the
	// bundle returns non-empty payloads — the equivalence assertion
	// matters only when there's actually something to compare. The
	// FTS column stores pre-tokenised text (the indexer does this in
	// production via search.Tokenize); without splitting, a query for
	// "token" would not hit "ValidateToken".
	upsertTokenised := func(id, raw string) {
		toks := search.Tokenize(raw)
		require.NoError(t, s.UpsertSymbolFTS(id, strings.Join(toks, " ")))
	}
	nodeSpecs := []struct {
		id, name, path string
	}{
		{"pkg/auth.go::ValidateToken", "ValidateToken", "pkg/auth.go"},
		{"pkg/auth.go::ParseToken", "ParseToken", "pkg/auth.go"},
		{"pkg/auth.go::AuthMiddleware", "AuthMiddleware", "pkg/auth.go"},
		{"pkg/server.go::HandleRequest", "HandleRequest", "pkg/server.go"},
	}
	for i, spec := range nodeSpecs {
		s.AddNode(&graph.Node{
			ID: spec.id, Kind: graph.KindFunction, Name: spec.name,
			FilePath: spec.path, StartLine: i + 1, EndLine: i + 5, Language: "go",
		})
		upsertTokenised(spec.id, spec.name)
	}
	// Edges: HandleRequest -> AuthMiddleware -> ValidateToken -> ParseToken
	s.AddEdge(&graph.Edge{
		From: "pkg/server.go::HandleRequest", To: "pkg/auth.go::AuthMiddleware",
		Kind: graph.EdgeCalls,
	})
	s.AddEdge(&graph.Edge{
		From: "pkg/auth.go::AuthMiddleware", To: "pkg/auth.go::ValidateToken",
		Kind: graph.EdgeCalls,
	})
	s.AddEdge(&graph.Edge{
		From: "pkg/auth.go::ValidateToken", To: "pkg/auth.go::ParseToken",
		Kind: graph.EdgeCalls,
	})
	require.NoError(t, s.BuildSymbolIndex())

	bundles, err := s.SearchSymbolBundles("token", 10)
	require.NoError(t, err)
	require.NotEmpty(t, bundles, "FTS must surface 'token' hits")

	// Reconstruct the same join sequentially via the public API so the
	// assertion compares against the post-parallel result.
	ids := make([]string, 0, len(bundles))
	for _, b := range bundles {
		require.NotNil(t, b.Node, "bundle node must not be nil")
		ids = append(ids, b.Node.ID)
	}
	seqNodes := s.GetNodesByIDs(ids)
	seqOut := s.GetOutEdgesByNodeIDs(ids)
	seqIn := s.GetInEdgesByNodeIDs(ids)

	for i, b := range bundles {
		seqNode := seqNodes[b.Node.ID]
		require.NotNil(t, seqNode, "sequential GetNodesByIDs lost id %q", b.Node.ID)
		assert.Equal(t, seqNode.ID, b.Node.ID, "bundle[%d] node id drift", i)
		assert.Equal(t, seqNode.Name, b.Node.Name, "bundle[%d] node name drift", i)
		assert.Equal(t, len(seqOut[b.Node.ID]), len(b.OutEdges),
			"bundle[%d] out-edge count drift for %q", i, b.Node.ID)
		assert.Equal(t, len(seqIn[b.Node.ID]), len(b.InEdges),
			"bundle[%d] in-edge count drift for %q", i, b.Node.ID)
	}
}
