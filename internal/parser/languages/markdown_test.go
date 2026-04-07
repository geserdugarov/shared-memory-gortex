package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestMarkdownExtractor_Headings(t *testing.T) {
	src := []byte(`# Getting Started

## Installation

Some text here.

## Usage

More text.

### Advanced
`)
	e := NewMarkdownExtractor()
	result, err := e.Extract("README.md", src)
	require.NoError(t, err)

	vars := nodesOfKind(result.Nodes, graph.KindVariable)
	assert.GreaterOrEqual(t, len(vars), 3)

	names := make([]string, len(vars))
	for i, v := range vars {
		names[i] = v.Name
	}
	assert.Contains(t, names, "Getting Started")
	assert.Contains(t, names, "Installation")
	assert.Contains(t, names, "Usage")
}

func TestMarkdownExtractor_Links(t *testing.T) {
	src := []byte(`# Docs

See [CONTRIBUTING](CONTRIBUTING.md) for guidelines.

Check the [config](docs/config.md) and [API](docs/api.md).

External link [Google](https://google.com) should be skipped.
`)
	e := NewMarkdownExtractor()
	result, err := e.Extract("README.md", src)
	require.NoError(t, err)

	imports := edgesOfKind(result.Edges, graph.EdgeImports)
	assert.Len(t, imports, 3) // CONTRIBUTING.md, docs/config.md, docs/api.md

	targets := make([]string, len(imports))
	for i, e := range imports {
		targets[i] = e.To
	}
	assert.Contains(t, targets, "unresolved::import::CONTRIBUTING.md")
	assert.Contains(t, targets, "unresolved::import::docs/config.md")
	assert.Contains(t, targets, "unresolved::import::docs/api.md")
}

func TestMarkdownExtractor_CodeBlocks(t *testing.T) {
	src := []byte("# Example\n\n```bash\ngortex serve\n```\n\n```go\nfunc main() {}\n```\n")
	e := NewMarkdownExtractor()
	result, err := e.Extract("README.md", src)
	require.NoError(t, err)

	vars := nodesOfKind(result.Nodes, graph.KindVariable)
	// Should have heading + 2 code blocks
	codeBlocks := 0
	for _, v := range vars {
		if _, ok := v.Meta["code_language"]; ok {
			codeBlocks++
		}
	}
	assert.Equal(t, 2, codeBlocks)
}

func TestMarkdownExtractor_FileNode(t *testing.T) {
	src := []byte("# Title\n")
	e := NewMarkdownExtractor()
	result, err := e.Extract("doc.md", src)
	require.NoError(t, err)

	files := nodesOfKind(result.Nodes, graph.KindFile)
	require.Len(t, files, 1)
	assert.Equal(t, "doc.md", files[0].Name)
}

func TestMarkdownExtractor_Extensions(t *testing.T) {
	e := NewMarkdownExtractor()
	assert.Equal(t, "markdown", e.Language())
	assert.Equal(t, []string{".md", ".mdx"}, e.Extensions())
}
