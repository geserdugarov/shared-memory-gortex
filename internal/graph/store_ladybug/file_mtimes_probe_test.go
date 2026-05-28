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

// TestFileMtimes_SharedRelativePathsAcrossRepos is the regression guard
// for the cross-repo collision that re-indexed (and crashed) repos on
// every warm restart. PRIMARY KEY(file_id) is global, but relative paths
// are not unique across repos — every tree-sitter grammar repo ships
// `src/parser.c`, `grammar.js`, `binding.gyp`. With the bare relative
// path as file_id, the second repo's MERGE overwrote the first's
// repo_prefix, so LoadFileMtimes returned zero rows for every repo but
// the last writer; the daemon then full-COPY-re-indexed those repos
// against an already-populated store, SIGSEGVing on the duplicate keys.
// The fix prefixes file_id with the repo prefix; this test proves two
// repos sharing identical relative paths each round-trip their own
// mtimes.
func TestFileMtimes_SharedRelativePathsAcrossRepos(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbug-mtime-collide-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "store.lbug")

	shared := []string{"src/parser.c", "grammar.js", "binding.gyp"}

	{
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		dart := map[string]int64{}
		swift := map[string]int64{}
		for i, p := range shared {
			dart[p] = int64(1779000000 + i)
			swift[p] = int64(1779009000 + i)
		}
		if err := s.BulkSetFileMtimes("tree-sitter-dart", dart); err != nil {
			t.Fatalf("set dart: %v", err)
		}
		if err := s.BulkSetFileMtimes("tree-sitter-swift", swift); err != nil {
			t.Fatalf("set swift: %v", err)
		}
		_ = s.Close()
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	gotDart := s.LoadFileMtimes("tree-sitter-dart")
	if len(gotDart) != len(shared) {
		t.Fatalf("dart loaded %d entries, want %d (cross-repo collision regressed): %v",
			len(gotDart), len(shared), gotDart)
	}
	if gotDart["src/parser.c"] != 1779000000 {
		t.Errorf("dart src/parser.c = %d, want 1779000000 (got swift's value? = collision)", gotDart["src/parser.c"])
	}

	gotSwift := s.LoadFileMtimes("tree-sitter-swift")
	if len(gotSwift) != len(shared) {
		t.Fatalf("swift loaded %d entries, want %d: %v", len(gotSwift), len(shared), gotSwift)
	}
	if gotSwift["src/parser.c"] != 1779009000 {
		t.Errorf("swift src/parser.c = %d, want 1779009000", gotSwift["src/parser.c"])
	}
}
