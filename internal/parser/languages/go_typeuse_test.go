package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// genericArgRefs returns the set of unresolved:: target type names of
// every EdgeReferences edge carrying ref_context=generic_arg.
func genericArgRefs(fix *extractedFixture) map[string]bool {
	out := map[string]bool{}
	for _, e := range fix.edgesByKind[graph.EdgeReferences] {
		if e.Meta == nil {
			continue
		}
		if rc, _ := e.Meta["ref_context"].(string); rc != "generic_arg" {
			continue
		}
		out[e.To] = true
	}
	return out
}

func TestGoTypeUse_GenericArgs(t *testing.T) {
	src := `package foo

type Foo struct{}
type Bar struct{}
type Baz struct{}
type Qux struct{}
type Option[T any] struct{}
type Result[T any, E any] struct{}

func consume(p Option[Foo]) Result[Bar, Baz] {
	var m map[string]Qux
	_ = m
	ch := make(chan Foo)
	_ = ch
	return Result[Bar, Baz]{}
}
`
	fix := runGoExtract(t, src)
	got := genericArgRefs(fix)

	// Generic args (Foo, Bar, Baz), map value (Qux), channel element (Foo).
	want := []string{
		"unresolved::Foo", "unresolved::Bar",
		"unresolved::Baz", "unresolved::Qux",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing generic_arg reference %q; got %v", w, got)
		}
	}

	// Type parameters and the primitive key type must NOT be emitted as
	// generic_arg references — zero false positives.
	for _, bad := range []string{
		"unresolved::T", "unresolved::E", "unresolved::string",
		"unresolved::any",
	} {
		if got[bad] {
			t.Errorf("unexpected generic_arg reference %q (type param / primitive); got %v", bad, got)
		}
	}

	// Owner attribution: every generic_arg edge inside the function body
	// is attributed to the enclosing function, not the file node.
	for _, e := range fix.edgesByKind[graph.EdgeReferences] {
		if e.Meta == nil {
			continue
		}
		if rc, _ := e.Meta["ref_context"].(string); rc != "generic_arg" {
			continue
		}
		if e.From == "" {
			t.Errorf("generic_arg edge has empty owner: %+v", e)
		}
	}
}

func TestGoTypeUse_NestedComposites(t *testing.T) {
	src := `package foo

type Foo struct{}
type Bar struct{}
type List[T any] struct{}

func f() {
	var nested map[string][]Foo
	_ = nested
	var deep chan List[Bar]
	_ = deep
}
`
	fix := runGoExtract(t, src)
	got := genericArgRefs(fix)

	// map[string][]Foo → Foo (value element through the slice wrapper).
	// chan List[Bar] → List (channel element) and Bar (its generic arg).
	for _, w := range []string{"unresolved::Foo", "unresolved::List", "unresolved::Bar"} {
		if !got[w] {
			t.Errorf("missing nested-composite reference %q; got %v", w, got)
		}
	}
	if got["unresolved::string"] {
		t.Errorf("primitive map key string must not be emitted; got %v", got)
	}
}
