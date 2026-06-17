package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestAstroExtractor(t *testing.T) {
	const astro = `---
import Card from '../components/Card.astro'
const title = "Home"
---
<html><body><Card /></body></html>
`
	res, err := NewAstroExtractor().Extract("pages/index.astro", []byte(astro))
	if err != nil {
		t.Fatal(err)
	}

	var comp, title *graph.Node
	for _, n := range res.Nodes {
		if n.Kind == graph.KindType && n.Name == "index" {
			comp = n
		}
		if n.Name == "title" {
			title = n
		}
	}
	if comp == nil {
		t.Fatalf("no component node 'index' among %d nodes", len(res.Nodes))
	}
	if comp.Meta["exported"] != true || comp.Language != "astro" {
		t.Errorf("component meta/lang = %v / %q", comp.Meta, comp.Language)
	}

	// The frontmatter is delegated to TypeScript and rebased into host coords.
	if title == nil {
		t.Fatalf("frontmatter 'title' constant was not extracted")
	}
	if title.Language != "astro" || title.Meta["inline_script"] != true {
		t.Errorf("frontmatter symbol lang=%q meta=%v, want astro + inline_script", title.Language, title.Meta)
	}
	if title.StartLine != 3 {
		t.Errorf("title StartLine = %d, want 3 (host-file coordinates)", title.StartLine)
	}
}
