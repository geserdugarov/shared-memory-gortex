package lsp

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestRouter_For_NoSpec returns an error for unknown extensions.
func TestRouter_For_NoSpec(t *testing.T) {
	r := NewRouter(t.TempDir(), zap.NewNop())
	defer r.Close()
	if _, err := r.For("README.md"); err == nil {
		t.Fatal("expected error for unknown ext")
	}
}

// TestRouter_AvailableSpecs filters by exec.LookPath. We can't assume
// any LSP server is on PATH on CI, so just check the call doesn't
// panic and returns a sane shape.
func TestRouter_AvailableSpecs(t *testing.T) {
	r := NewRouter(t.TempDir(), zap.NewNop())
	defer r.Close()
	specs := r.AvailableSpecs()
	for _, s := range specs {
		if s == nil {
			t.Fatal("nil spec returned")
		}
		if s.Name == "" {
			t.Fatal("empty name returned")
		}
	}
}

// TestRouter_Stats_EmptyOnConstruct confirms a fresh router exposes
// no live providers until For() succeeds.
func TestRouter_Stats_EmptyOnConstruct(t *testing.T) {
	r := NewRouter(t.TempDir(), zap.NewNop())
	defer r.Close()
	if got := r.Stats(); len(got) != 0 {
		t.Fatalf("expected empty stats, got %v", got)
	}
}

// TestRouter_SupportedLanguages doesn't depend on PATH binaries —
// AvailableSpecs may be empty on CI; the function should still return
// a sorted, deduplicated slice (possibly empty).
func TestRouter_SupportedLanguages(t *testing.T) {
	r := NewRouter(t.TempDir(), zap.NewNop())
	defer r.Close()
	langs := r.SupportedLanguages()
	for i := 1; i < len(langs); i++ {
		if langs[i-1] >= langs[i] {
			t.Errorf("not sorted: %v", langs)
		}
	}
}

// TestRouter_NoOpReap returns nothing when the router is empty.
func TestRouter_NoOpReap(t *testing.T) {
	r := NewRouter(t.TempDir(), zap.NewNop()).WithIdleTimeout(time.Millisecond)
	defer r.Close()
	if names := r.Reap(); len(names) != 0 {
		t.Fatalf("expected no names, got %v", names)
	}
}

// TestRouter_LanguageIDForPath delegates to the package helper.
func TestRouter_LanguageIDForPath(t *testing.T) {
	r := NewRouter(t.TempDir(), zap.NewNop())
	defer r.Close()
	if got := r.LanguageIDForPath("a.ts"); got != "typescript" {
		t.Fatalf("got %q, want typescript", got)
	}
}
