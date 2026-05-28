//go:build ladybug

package store_ladybug

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileMtimes_PersistAcrossOpens locks in the warm-restart
// contract: BulkSetFileMtimes writes to the FileMtime table, the
// store closes, the store reopens, and LoadFileMtimes returns the
// same data. Pre-fix, the daemon's warmup re-walked every repo on
// each restart — find_usages stayed correct but the daemon paid 10
// minutes of warmup it could have skipped. This probe is the
// regression guard.
func TestFileMtimes_PersistAcrossOpens(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbug-mtime-probe-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "store.lbug")

	// Phase 1: open, write, close.
	{
		s, err := Open(path)
		if err != nil {
			t.Fatalf("phase1 open: %v", err)
		}
		mtimes := map[string]int64{
			"internal/mcp/server.go":  1779000000,
			"internal/mcp/handler.go": 1779000001,
			"internal/graph/graph.go": 1779000002,
		}
		if err := s.BulkSetFileMtimes("gortex", mtimes); err != nil {
			t.Fatalf("phase1 BulkSetFileMtimes: %v", err)
		}
		mtimesB := map[string]int64{
			"api/billing.go": 1779000010,
		}
		if err := s.BulkSetFileMtimes("gortex-cloud", mtimesB); err != nil {
			t.Fatalf("phase1 BulkSetFileMtimes B: %v", err)
		}
		_ = s.Close()
	}

	// Phase 2: reopen, read, compare.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("phase2 open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	gotA := s.LoadFileMtimes("gortex")
	if len(gotA) != 3 {
		t.Errorf("phase2 LoadFileMtimes(gortex) = %d entries, want 3: %v", len(gotA), gotA)
	}
	if gotA["internal/mcp/server.go"] != 1779000000 {
		t.Errorf("phase2 server.go mtime = %d, want 1779000000", gotA["internal/mcp/server.go"])
	}

	gotB := s.LoadFileMtimes("gortex-cloud")
	if len(gotB) != 1 {
		t.Errorf("phase2 LoadFileMtimes(gortex-cloud) = %d entries, want 1: %v", len(gotB), gotB)
	}
	if gotB["api/billing.go"] != 1779000010 {
		t.Errorf("phase2 billing.go mtime = %d, want 1779000010", gotB["api/billing.go"])
	}

	// Empty prefix returns all.
	all := s.LoadFileMtimes("")
	if len(all) != 4 {
		t.Errorf("phase2 LoadFileMtimes('') = %d entries, want 4", len(all))
	}
}
