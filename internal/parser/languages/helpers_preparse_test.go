package languages

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/parser"
)

// TestPreParse_BlankConditionalDirectivesPreservesOffsets is the A1 named
// test: the conditional-compilation blanker must keep code in every branch,
// neutralise only the #if/#elif/#else/#endif directive lines, and leave the
// byte layout (length + newline positions) exactly intact so node ranges stay
// accurate.
func TestPreParse_BlankConditionalDirectivesPreservesOffsets(t *testing.T) {
	src := []byte("class C {\n" +
		"#if DEBUG\n" +
		"    void A() {}\n" +
		"  #elif RELEASE\n" +
		"    void B() {}\n" +
		"#else\n" +
		"    void Cc() {}\n" +
		"#endif\n" +
		"    void D() {}\n" +
		"}\n")

	out := blankConditionalDirectives(src)

	if len(out) != len(src) {
		t.Fatalf("length changed: got %d want %d", len(out), len(src))
	}
	// Every newline must stay at the exact same offset.
	for i := range src {
		if src[i] == '\n' && out[i] != '\n' {
			t.Fatalf("newline at offset %d was clobbered", i)
		}
		if src[i] != '\n' && out[i] == '\n' {
			t.Fatalf("newline appeared at offset %d", i)
		}
	}

	got := string(out)
	want := "class C {\n" +
		"         \n" + // #if DEBUG -> spaces (9 cols)
		"    void A() {}\n" +
		"               \n" + // "  #elif RELEASE" -> spaces (15 cols)
		"    void B() {}\n" +
		"     \n" + // #else -> spaces (5 cols)
		"    void Cc() {}\n" +
		"      \n" + // #endif -> spaces (6 cols)
		"    void D() {}\n" +
		"}\n"
	if got != want {
		t.Fatalf("blanked output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestPreParse_LeavesNonConditionalDirectives ensures the blanker only touches
// the four conditional directives — #region/#pragma/#nullable/#define and a
// '#if' that is not line-leading (e.g. inside a string) stay verbatim.
func TestPreParse_LeavesNonConditionalDirectives(t *testing.T) {
	src := []byte("#region Models\n" +
		"#pragma warning disable\n" +
		"#nullable enable\n" +
		"#define FOO\n" +
		"var s = \"#if not a directive\";\n" +
		"#if FOO\n" +
		"#endif\n")
	out := string(blankConditionalDirectives(src))
	for _, keep := range []string{
		"#region Models",
		"#pragma warning disable",
		"#nullable enable",
		"#define FOO",
		"\"#if not a directive\"",
	} {
		if !strings.Contains(out, keep) {
			t.Fatalf("non-conditional content %q was clobbered:\n%s", keep, out)
		}
	}
	if strings.Contains(out, "#if FOO") || strings.Contains(out, "#endif") {
		t.Fatalf("line-leading conditional directives were not blanked:\n%s", out)
	}
}

// TestPreParse_ApplyIdentityDefault verifies ApplyPreParse is the identity for
// an extractor that does not implement parser.PreParser, so wiring the hook at
// a parse entry is a no-op until the language opts in.
func TestPreParse_ApplyIdentityDefault(t *testing.T) {
	src := []byte("package p\nfunc F() {}\n")
	out := parser.ApplyPreParse(NewGoExtractor(), src)
	if string(out) != string(src) {
		t.Fatalf("ApplyPreParse mutated source for a non-PreParser extractor:\n got: %q\nwant: %q", out, src)
	}
}
