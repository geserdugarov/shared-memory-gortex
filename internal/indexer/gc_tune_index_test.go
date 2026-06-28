package indexer_test

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// indexSampleRepo indexes a fixed Go source tree with a freshly-built graph,
// registry, and indexer, returning the node/edge counts. Each call is fully
// isolated so two calls under different GC-tuning settings are comparable.
func indexSampleRepo(t *testing.T, root string) (nodes, edges int) {
	t.Helper()
	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, config.Default().Index, zap.NewNop())
	res, err := idx.Index(root)
	if err != nil {
		t.Fatalf("index %s: %v", root, err)
	}
	return res.NodeCount, res.EdgeCount
}

// TestColdIndexCountsIdenticalWithAndWithoutGCTuning verifies the GC-tuning
// knobs are timing-only: a cold index produces the same node and edge counts
// whether tuning is enabled (default) or disabled via GORTEX_INDEX_GC_TUNE=0.
func TestColdIndexCountsIdenticalWithAndWithoutGCTuning(t *testing.T) {
	root := t.TempDir()
	src := `package sample

// Widget is a small carrier type.
type Widget struct {
	Name  string
	Count int
}

// Hello greets using the widget's name.
func (w Widget) Hello() string {
	if w.Count > 0 {
		return w.Name
	}
	return "anon"
}

// Greet is a free function exercising a method call edge.
func Greet(w Widget) string {
	return w.Hello()
}

func use() string {
	w := Widget{Name: "gx", Count: 2}
	return Greet(w)
}
`
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Tuning enabled (default — env unset).
	os.Unsetenv("GORTEX_INDEX_GC_TUNE")
	tunedNodes, tunedEdges := indexSampleRepo(t, root)

	// Tuning disabled.
	t.Setenv("GORTEX_INDEX_GC_TUNE", "0")
	untunedNodes, untunedEdges := indexSampleRepo(t, root)

	if tunedNodes == 0 || tunedEdges == 0 {
		t.Fatalf("expected a non-empty index, got nodes=%d edges=%d", tunedNodes, tunedEdges)
	}
	if tunedNodes != untunedNodes {
		t.Errorf("node count differs: tuned=%d untuned=%d", tunedNodes, untunedNodes)
	}
	if tunedEdges != untunedEdges {
		t.Errorf("edge count differs: tuned=%d untuned=%d", tunedEdges, untunedEdges)
	}
}
