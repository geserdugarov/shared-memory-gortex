package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestSwiftExtractor_ObjCProperty(t *testing.T) {
	const swift = `class Widget {
    @objc var title: String = ""
    @objc let count: Int = 0
    var plain: Double = 0
}
`
	res, err := NewSwiftExtractor().Extract("W.swift", []byte(swift))
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]*graph.Node{}
	for _, n := range res.Nodes {
		byName[n.Name] = n
	}
	memberOf := func(id string) bool {
		for _, e := range res.Edges {
			if e.Kind == graph.EdgeMemberOf && e.From == id && e.To == "W.swift::Widget" {
				return true
			}
		}
		return false
	}

	// @objc var: field, mutable, getter + setter selectors, typed, member of Widget.
	title := byName["title"]
	if title == nil || title.Kind != graph.KindField {
		t.Fatalf("@objc var 'title' should be a field node, got %+v", title)
	}
	if title.Meta["objc_selector"] != "title" {
		t.Errorf("title objc_selector = %v, want title", title.Meta["objc_selector"])
	}
	if title.Meta["objc_setter_selector"] != "setTitle:" {
		t.Errorf("title objc_setter_selector = %v, want setTitle:", title.Meta["objc_setter_selector"])
	}
	if title.Meta["mutable"] != true {
		t.Errorf("var title should be mutable: meta=%v", title.Meta)
	}
	if title.Meta["field_type"] != "String" {
		t.Errorf("title field_type = %v, want String", title.Meta["field_type"])
	}
	if !memberOf(title.ID) {
		t.Errorf("title should be member_of Widget; id=%s", title.ID)
	}

	// @objc let: getter only — no setter, not mutable.
	count := byName["count"]
	if count == nil {
		t.Fatal("@objc let 'count' was not extracted")
	}
	if count.Meta["objc_selector"] != "count" {
		t.Errorf("count objc_selector = %v, want count", count.Meta["objc_selector"])
	}
	if _, ok := count.Meta["objc_setter_selector"]; ok {
		t.Errorf("immutable let count should have no setter selector: meta=%v", count.Meta)
	}
	if count.Meta["mutable"] == true {
		t.Errorf("let count should not be mutable")
	}

	// Non-@objc property: extracted, but carries no selector metadata.
	plain := byName["plain"]
	if plain == nil || plain.Kind != graph.KindField {
		t.Fatalf("var 'plain' should be a field node, got %+v", plain)
	}
	if _, ok := plain.Meta["objc_selector"]; ok {
		t.Errorf("non-@objc plain should have no objc_selector: meta=%v", plain.Meta)
	}
}
