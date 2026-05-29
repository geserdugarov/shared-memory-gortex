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

// TestVectorSearcher_RepeatedBulkReplaceIsDeterministic hammers the
// wipe-and-rewrite path (bulk -> BuildVectorIndex -> bulk -> ...) in a
// single store. Pre-fix the 2nd+ BulkUpsertEmbeddings non-deterministically
// failed with "COPY into a non-empty primary-key node table without a hash
// index is not supported": DELETE empties the rows logically but leaves the
// table non-empty for COPY, and whether the PK hash index is materialized at
// COPY time depended on auto-checkpoint timing. The fix drops + recreates the
// table so every COPY targets a fresh empty table. The in-process loop makes
// the formerly-racy failure reliably reproducible.
func TestVectorSearcher_RepeatedBulkReplaceIsDeterministic(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbug-vec-recopy-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	s, err := Open(filepath.Join(dir, "store.lbug"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.NoError(t, s.BulkUpsertEmbeddings([]graph.VectorItem{
		{NodeID: "a", Vec: []float32{1, 0, 0, 0}},
		{NodeID: "b", Vec: []float32{0, 1, 0, 0}},
	}))
	require.NoError(t, s.BuildVectorIndex(4))

	for i := 0; i < 30; i++ {
		require.NoErrorf(t, s.BulkUpsertEmbeddings([]graph.VectorItem{
			{NodeID: "z", Vec: []float32{1, 1, 0, 0}},
		}), "re-bulk iteration %d hit the COPY-into-non-empty rejection", i)
		require.NoErrorf(t, s.BuildVectorIndex(4), "BuildVectorIndex iteration %d", i)
		hits, err := s.SimilarTo([]float32{1, 0, 0, 0}, 10)
		require.NoErrorf(t, err, "SimilarTo iteration %d", i)
		require.Lenf(t, hits, 1, "wipe-and-rewrite must leave exactly 1 row (iteration %d)", i)
		assert.Equal(t, "z", hits[0].NodeID)
	}
}
