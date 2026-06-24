package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestLiquidJSONExtractor_SectionLinks(t *testing.T) {
	src := []byte(`{
  "sections": {
    "main": { "type": "main-product" },
    "rel":  { "type": "related-products" },
    "dup":  { "type": "main-product" }
  },
  "order": ["main", "rel", "dup"]
}`)
	res, err := NewLiquidJSONExtractor().Extract("templates/product.json", src)
	require.NoError(t, err)

	// One EdgeImports per distinct section type (the duplicate main-product
	// section dedupes), normalized to the same target a `{% section %}` tag uses.
	importTargets := map[string]int{}
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeImports {
			importTargets[e.To]++
		}
	}
	assert.Equal(t, 1, importTargets["unresolved::import::sections/main-product.liquid"],
		"main-product linked once despite two sections of that type")
	assert.Equal(t, 1, importTargets["unresolved::import::sections/related-products.liquid"])

	// A searchable import node per distinct type, tagged json_section.
	imports := map[string]*graph.Node{}
	for _, n := range res.Nodes {
		if n.Kind == graph.KindImport {
			imports[n.Name] = n
		}
	}
	require.Len(t, imports, 2)
	require.NotNil(t, imports["main-product"])
	assert.Equal(t, "json_section", imports["main-product"].Meta["liquid_tag"])
	assert.Equal(t, "sections/main-product.liquid", imports["main-product"].Meta["target"])
}

func TestLiquidJSONExtractor_NonTemplateJSON(t *testing.T) {
	// A JSON without a parseable `sections` map links nothing (just a file node).
	res, err := NewLiquidJSONExtractor().Extract("templates/x.json", []byte(`{"foo": "bar"}`))
	require.NoError(t, err)
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeImports {
			t.Errorf("a non-section JSON must not emit import edges, got %s", e.To)
		}
	}
}
