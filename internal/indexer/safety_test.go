package indexer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnsafeIndexRootReason(t *testing.T) {
	// A normal temporary directory is safe to index.
	if r := UnsafeIndexRootReason(t.TempDir()); r != "" {
		t.Errorf("a temp dir was flagged unsafe: %q", r)
	}

	// The POSIX filesystem root is refused.
	if runtime.GOOS != "windows" {
		if r := UnsafeIndexRootReason("/"); r == "" {
			t.Error("filesystem root was not flagged unsafe")
		}
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}

	// The home directory itself is refused.
	if r := UnsafeIndexRootReason(home); r == "" {
		t.Errorf("home directory %q was not flagged unsafe", home)
	}
	// A trailing-slash / unclean form of home is still refused.
	if r := UnsafeIndexRootReason(home + string(filepath.Separator) + "."); r == "" {
		t.Errorf("unclean home path %q was not flagged unsafe", home)
	}
	// A subdirectory of home is safe.
	if r := UnsafeIndexRootReason(filepath.Join(home, "code", "project")); r != "" {
		t.Errorf("a home subdirectory was flagged unsafe: %q", r)
	}
}
