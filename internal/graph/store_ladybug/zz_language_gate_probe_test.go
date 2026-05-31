package store_ladybug

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestHasLanguageAndNodesByKindLang validates the language-scoped store
// methods the resolver's language-gate relies on: HasLanguage must be an
// exact per-language presence check, and NodesByKindLang must return only
// nodes matching BOTH kind and language. A wrong result here would make a
// language-gated pass skip a graph it should process.
func TestHasLanguageAndNodesByKindLang(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "x.kuzu"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	s.AddNode(&graph.Node{ID: "a.go::T", Name: "T", Kind: graph.KindType, FilePath: "a.go", Language: "go"})
	s.AddNode(&graph.Node{ID: "b.ts::I", Name: "I", Kind: graph.KindType, FilePath: "b.ts", Language: "typescript"})

	for lang, want := range map[string]bool{"go": true, "typescript": true, "python": false, "": false} {
		if got := s.HasLanguage(lang); got != want {
			t.Errorf("HasLanguage(%q) = %v, want %v", lang, got, want)
		}
	}

	collect := func(lang string) []string {
		var ids []string
		for n := range s.NodesByKindLang(graph.KindType, lang) {
			ids = append(ids, n.ID)
		}
		return ids
	}
	if got := collect("go"); len(got) != 1 || got[0] != "a.go::T" {
		t.Errorf("NodesByKindLang(type, go) = %v, want [a.go::T]", got)
	}
	if got := collect("typescript"); len(got) != 1 || got[0] != "b.ts::I" {
		t.Errorf("NodesByKindLang(type, typescript) = %v, want [b.ts::I]", got)
	}
	if got := collect("python"); len(got) != 0 {
		t.Errorf("NodesByKindLang(type, python) = %v, want []", got)
	}
}
