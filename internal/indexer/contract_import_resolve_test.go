package indexer

import (
	"reflect"
	"testing"
)

// TestParseTSImports_ResolvesNamedImports covers the common shape:
// `import type { Foo, Bar as Baz } from './schema'`. Each named
// binding maps to the resolved file path; the alias rebind uses
// the local-binding name (`Baz`), not the source name (`Bar`).
func TestParseTSImports_ResolvesNamedImports(t *testing.T) {
	src := `
import type { Foo, Bar as Baz } from './schema'
import { Quux } from '../shared/types'
`
	got := parseTSImports(src, "web/src/lib/api.ts")
	want := map[string]string{
		"Foo":  "web/src/lib/schema.ts",
		"Baz":  "web/src/lib/schema.ts",
		"Quux": "web/src/shared/types.ts",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

// TestParseTSImports_SkipsBareSpecifiers ensures third-party and
// path-aliased imports (`react`, `@/lib/foo`) don't leak into the
// resolution map — they don't correspond to a graph file in the
// local repo and would mislead the disambiguator if they did.
func TestParseTSImports_SkipsBareSpecifiers(t *testing.T) {
	src := `
import React from 'react'
import { useState } from 'react'
import { foo } from '@/lib/foo'
`
	got := parseTSImports(src, "web/src/lib/api.ts")
	if len(got) != 0 {
		t.Errorf("expected empty map for bare specifiers, got %+v", got)
	}
}

// TestParseTSImports_DefaultAndNamespace checks `import Foo from`
// (default) and `import * as NS from` bindings.
func TestParseTSImports_DefaultAndNamespace(t *testing.T) {
	src := `
import Default from './a'
import * as NS from './b'
`
	got := parseTSImports(src, "x/y.ts")
	if got["Default"] != "x/a.ts" {
		t.Errorf("default import: got %q, want %q", got["Default"], "x/a.ts")
	}
	if got["NS"] != "x/b.ts" {
		t.Errorf("namespace import: got %q, want %q", got["NS"], "x/b.ts")
	}
}

// TestSplitTSImportClause_AliasAndType normalises the `type` keyword
// and `as <local>` rebind in named-import bodies.
func TestSplitTSImportClause_AliasAndType(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Foo, Bar", []string{"Foo", "Bar"}},
		{"type Foo, Bar as Baz", []string{"Foo", "Baz"}},
		{"  Foo  ,  type Bar  ", []string{"Foo", "Bar"}},
		{"", nil},
	}
	for _, c := range cases {
		got := splitTSImportClause(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitTSImportClause(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestResolveTSModulePath_AddsTSExtension confirms that a relative
// specifier without an extension picks up `.ts` so a candidate
// living in `<dir>/file.ts` is matchable. Specifiers with an
// explicit extension are left alone.
func TestResolveTSModulePath_AddsTSExtension(t *testing.T) {
	cases := []struct {
		mod, dir, want string
	}{
		{"./schema", "web/src/lib", "web/src/lib/schema.ts"},
		{"./schema.ts", "web/src/lib", "web/src/lib/schema.ts"},
		{"./schema.tsx", "web/src/lib", "web/src/lib/schema.tsx"},
		{"../api", "web/src/lib/inner", "web/src/lib/api.ts"},
		{"react", "web/src/lib", ""},
		{"@/lib/foo", "web/src/lib", ""},
	}
	for _, c := range cases {
		got := resolveTSModulePath(c.mod, c.dir)
		if got != c.want {
			t.Errorf("resolveTSModulePath(%q, %q) = %q, want %q", c.mod, c.dir, got, c.want)
		}
	}
}

// TestIsImportResolvableLang only flags TS / JS family extensions —
// Go / Python / etc. are skipped because their import semantics
// differ enough that this resolver would mis-attribute candidates.
func TestIsImportResolvableLang(t *testing.T) {
	pos := []string{"a.ts", "b.tsx", "c.js", "d.jsx", "e.mts", "f.cjs"}
	neg := []string{"a.go", "b.py", "c.rb", "d.java", "e", "f.kt"}
	for _, p := range pos {
		if !isImportResolvableLang(p) {
			t.Errorf("%q: want true, got false", p)
		}
	}
	for _, p := range neg {
		if isImportResolvableLang(p) {
			t.Errorf("%q: want false, got true", p)
		}
	}
}
