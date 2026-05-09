package lsp

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/semantic"
)

// TestSpecForExtension verifies the per-extension lookup table is
// populated for every claimed extension and that case is normalised.
func TestSpecForExtension(t *testing.T) {
	cases := []struct {
		ext      string
		wantName string
	}{
		{".go", "gopls"},
		{".ts", "typescript-language-server"},
		{".tsx", "typescript-language-server"},
		{".py", "pyright"},
		{".pyi", "pyright"},
		{".rs", "rust-analyzer"},
		{".cpp", "clangd"},
		{".HPP", "clangd"}, // case folding
		{".java", "jdtls"},
		{".kt", "kotlin-language-server"},
		{".cs", "omnisharp"},
		{".rb", "ruby-lsp"},
		{".php", "phpactor"},
		{".lua", "lua-language-server"},
		{".swift", "sourcekit-lsp"},
		{".hs", "haskell-language-server"},
		{".ex", "elixir-ls"},
		{".ml", "ocamllsp"},
		{".zig", "zls"},
	}
	for _, c := range cases {
		t.Run(c.ext, func(t *testing.T) {
			s := SpecForExtension(c.ext)
			if s == nil {
				t.Fatalf("no spec for %q", c.ext)
			}
			if s.Name != c.wantName {
				t.Fatalf("ext %q: got %s, want %s", c.ext, s.Name, c.wantName)
			}
		})
	}
	if SpecForExtension(".unknown_ext") != nil {
		t.Fatal("expected nil for unknown ext")
	}
	if SpecForExtension("") != nil {
		t.Fatal("empty ext should not map")
	}
}

// TestSpecForPath verifies path-based routing extracts the extension
// even from messy paths.
func TestSpecForPath(t *testing.T) {
	if s := SpecForPath("/abs/path/to/main.go"); s == nil || s.Name != "gopls" {
		t.Fatalf("unexpected spec for go path: %v", s)
	}
	if s := SpecForPath("README.md"); s != nil {
		t.Fatalf("expected nil spec for markdown, got %v", s)
	}
	if s := SpecForPath("/x.RS"); s == nil || s.Name != "rust-analyzer" {
		t.Fatalf("uppercase rust path: %v", s)
	}
}

// TestLanguageIDForPath checks per-extension languageId resolution.
func TestLanguageIDForPath(t *testing.T) {
	cases := map[string]string{
		"app.tsx":  "typescriptreact",
		"main.ts":  "typescript",
		"a.jsx":    "javascriptreact",
		"foo.py":   "python",
		"pkg/x.rs": "rust",
		"a.cpp":    "cpp",
		"a.h":      "c", // mapping wins
	}
	for path, want := range cases {
		got := LanguageIDForPath(path)
		if got != want {
			t.Errorf("path %q: got %q, want %q", path, got, want)
		}
	}
	if LanguageIDForPath("README.md") != "" {
		t.Fatal("expected empty languageId for unknown ext")
	}
}

// TestSpecByName ensures every entry in Servers is reachable by name.
func TestSpecByName(t *testing.T) {
	for _, want := range Servers {
		got := SpecByName(want.Name)
		if got == nil {
			t.Errorf("SpecByName(%q) = nil", want.Name)
			continue
		}
		if got.Name != want.Name {
			t.Errorf("SpecByName(%q).Name = %q", want.Name, got.Name)
		}
	}
	if SpecByName("nope") != nil {
		t.Fatal("unknown name should return nil")
	}
}

// TestRegistryContributesDefaultProviders proves that initialisation
// adds an entry per spec to the semantic.DefaultConfig provider list.
func TestRegistryContributesDefaultProviders(t *testing.T) {
	cfg := semantic.DefaultConfig()
	names := make(map[string]bool, len(cfg.Providers))
	for _, p := range cfg.Providers {
		names[p.Name] = true
	}
	for _, s := range Servers {
		if !names[s.Name] {
			t.Errorf("DefaultConfig missing %q", s.Name)
		}
	}
}

// TestExtensionsCoverEverySpec asserts every spec lists at least one
// extension — a spec with no extensions can't be routed to.
func TestExtensionsCoverEverySpec(t *testing.T) {
	for _, s := range Servers {
		if len(s.Extensions) == 0 {
			t.Errorf("spec %q has no extensions", s.Name)
		}
		for _, e := range s.Extensions {
			if !strings.HasPrefix(e, ".") {
				t.Errorf("spec %q ext %q must start with '.'", s.Name, e)
			}
		}
	}
}
