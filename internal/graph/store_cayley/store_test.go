package store_cayley_test

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_cayley"
	"github.com/zzet/gortex/internal/graph/storetest"
)

// TestCayleyStoreConformance runs the cross-backend conformance suite
// against the cayley-backed store. Each subtest gets its own temp dir
// so state cannot leak between runs.
func TestCayleyStoreConformance(t *testing.T) {
	storetest.RunConformance(t, func(t *testing.T) graph.Store {
		dir := t.TempDir()
		s, err := store_cayley.Open(filepath.Join(dir, "cayley"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
