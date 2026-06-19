package languages

import (
	"strings"
	"testing"
)

func TestExtractDocAbove_SlashSlash_Go(t *testing.T) {
	src := []byte(`package x

// Foo does something useful.
// Second line of explanation.
func Foo() {}
`)
	// "func Foo()" is on line 5 → row 4 (0-based).
	got := ExtractDocAbove(src, 4, DocLangSlashSlash)
	want := "Foo does something useful. Second line of explanation."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractDocAbove_StopsAtBlankLine(t *testing.T) {
	src := []byte(`package x

// Old unrelated comment.

// Foo is the real doc.
func Foo() {}
`)
	got := ExtractDocAbove(src, 5, DocLangSlashSlash)
	if got != "Foo is the real doc." {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_NoComment(t *testing.T) {
	src := []byte(`package x

func Foo() {}
`)
	if got := ExtractDocAbove(src, 2, DocLangSlashSlash); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestExtractDocAbove_TopOfFile(t *testing.T) {
	src := []byte(`func Foo() {}
`)
	if got := ExtractDocAbove(src, 0, DocLangSlashSlash); got != "" {
		t.Fatalf("expected empty for row 0, got %q", got)
	}
}

func TestExtractDocAbove_BlockStar_JSDoc(t *testing.T) {
	src := []byte(`import x from "y";

/**
 * Computes the answer.
 * Has more detail here.
 */
function compute() {}
`)
	got := ExtractDocAbove(src, 6, DocLangBlockStar)
	if !strings.Contains(got, "Computes the answer.") {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_BlockStar_StopsAtJSDocTag(t *testing.T) {
	src := []byte(`/**
 * Compute X.
 *
 * @param y the y
 * @returns the result
 */
function compute() {}
`)
	got := ExtractDocAbove(src, 6, DocLangBlockStar)
	if got != "Compute X." {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_BlockStar_FallsBackToLineComments(t *testing.T) {
	src := []byte(`// JS-style line doc.
function f() {}
`)
	got := ExtractDocAbove(src, 1, DocLangBlockStar)
	if got != "JS-style line doc." {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_Hash_Ruby(t *testing.T) {
	src := []byte(`module X
  # The greeter.
  # Welcomes the user.
  def greet
  end
end
`)
	// "def greet" is line 4 → row 3.
	got := ExtractDocAbove(src, 3, DocLangHash)
	if got != "The greeter. Welcomes the user." {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_CSharpXML_StripsTags(t *testing.T) {
	src := []byte(`using System;

/// <summary>
/// Returns the answer.
/// </summary>
public int Compute() => 42;
`)
	got := ExtractDocAbove(src, 5, DocLangCSharpXML)
	if !strings.Contains(got, "Returns the answer.") {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_Truncates(t *testing.T) {
	long := strings.Repeat("a", docMaxLen+50)
	src := []byte("// " + long + "\nfunc F() {}\n")
	got := ExtractDocAbove(src, 1, DocLangSlashSlash)
	if len(got) > docMaxLen+5 {
		t.Fatalf("doc not truncated: %d chars", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", got[len(got)-3:])
	}
}

func TestExtractPyDocstring_TripleQuoted(t *testing.T) {
	body := `
    """The docstring.

    Multi-line detail.
    """
    return 42
`
	got := ExtractPyDocstring(body)
	if !strings.HasPrefix(got, "The docstring.") {
		t.Fatalf("got %q", got)
	}
}

func TestExtractPyDocstring_None(t *testing.T) {
	body := `
    return 42
`
	if got := ExtractPyDocstring(body); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestVisibilityByCase(t *testing.T) {
	if v := VisibilityByCase("Foo"); v != VisibilityPublic {
		t.Fatalf("got %q", v)
	}
	if v := VisibilityByCase("foo"); v != VisibilityPackage {
		t.Fatalf("got %q", v)
	}
	if v := VisibilityByCase(""); v != "" {
		t.Fatalf("got %q", v)
	}
}

func TestVisibilityByUnderscore(t *testing.T) {
	if v := VisibilityByUnderscore("_foo"); v != VisibilityPrivate {
		t.Fatalf("got %q", v)
	}
	if v := VisibilityByUnderscore("foo"); v != VisibilityPublic {
		t.Fatalf("got %q", v)
	}
}

func TestVisibilityFromModifiers(t *testing.T) {
	cases := []struct {
		mods []string
		def  string
		want string
	}{
		{[]string{"public"}, VisibilityPackage, VisibilityPublic},
		{[]string{"private"}, VisibilityPackage, VisibilityPrivate},
		{[]string{"protected"}, VisibilityPackage, VisibilityProtected},
		{[]string{"internal"}, VisibilityPackage, VisibilityInternal},
		{[]string{"pub"}, VisibilityPrivate, VisibilityPublic},
		{[]string{"pub(crate)"}, VisibilityPrivate, VisibilityInternal},
		{[]string{"open"}, VisibilityPackage, VisibilityPublic},
		{[]string{"fileprivate"}, VisibilityInternal, VisibilityPrivate},
		{[]string{"static", "final"}, VisibilityPackage, VisibilityPackage},
		{nil, VisibilityPublic, VisibilityPublic},
	}
	for _, c := range cases {
		if got := VisibilityFromModifiers(c.mods, c.def); got != c.want {
			t.Fatalf("mods=%v default=%q got=%q want=%q", c.mods, c.def, got, c.want)
		}
	}
}

// TestDocCommentWrapperClimb covers F16's wrapper-climb: the doc-comment scan
// skips a decorator / export wrapper line that sits between the doc and the
// declaration, and the new dash-comment family (Lua) is stripped.
func TestDocWrapperClimb(t *testing.T) {
	t.Run("climbs_past_decorator", func(t *testing.T) {
		// row0=// doc, row1=@Component, row2=class Foo (startRow0=2)
		src := []byte("// the widget component\n@Component\nclass Foo {}\n")
		if got := ExtractDocAbove(src, 2, DocLangSlashSlash); got != "the widget component" {
			t.Errorf("decorator climb = %q, want %q", got, "the widget component")
		}
	})

	t.Run("climbs_past_export_keyword", func(t *testing.T) {
		src := []byte("// the public constant\nexport\nconst X = 1\n")
		if got := ExtractDocAbove(src, 2, DocLangSlashSlash); got != "the public constant" {
			t.Errorf("export climb = %q, want %q", got, "the public constant")
		}
	})

	t.Run("python_decorator_climb", func(t *testing.T) {
		src := []byte("# the route handler\n@app.route(\"/\")\ndef handler():\n")
		if got := ExtractDocAbove(src, 2, DocLangHash); got != "the route handler" {
			t.Errorf("python decorator climb = %q, want %q", got, "the route handler")
		}
	})

	t.Run("lua_dash_comment", func(t *testing.T) {
		src := []byte("-- the lua function\nfunction f() end\n")
		if got := ExtractDocAbove(src, 1, DocLangDashDash); got != "the lua function" {
			t.Errorf("lua -- = %q, want %q", got, "the lua function")
		}
	})

	t.Run("lua_triple_dash_doc", func(t *testing.T) {
		src := []byte("--- the documented function\nfunction g() end\n")
		if got := ExtractDocAbove(src, 1, DocLangDashDash); got != "the documented function" {
			t.Errorf("lua --- = %q, want %q", got, "the documented function")
		}
	})

	t.Run("no_false_climb_on_real_code", func(t *testing.T) {
		// A non-wrapper code line between the comment and the decl terminates.
		src := []byte("// unrelated comment\nsomeRealCode()\nfunc Bar() {}\n")
		if got := ExtractDocAbove(src, 2, DocLangSlashSlash); got != "" {
			t.Errorf("must not climb past real code; got %q", got)
		}
	})
}
