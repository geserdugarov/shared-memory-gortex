package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestCobolExtractor_Program(t *testing.T) {
	src := []byte(`       IDENTIFICATION DIVISION.
       PROGRAM-ID. HELLO-WORLD.
       DATA DIVISION.
       WORKING-STORAGE SECTION.
       COPY COMMONLIB.
       PROCEDURE DIVISION.
       MAIN SECTION.
           CALL 'GREET' USING NAME.
           STOP RUN.
`)
	e := NewCobolExtractor()
	require.Equal(t, "cobol", e.Language())

	res, err := e.Extract("HELLO.cob", src)
	require.NoError(t, err)

	var gotProg, gotDiv, gotSection bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "HELLO-WORLD":
			gotProg = true
		case "PROCEDURE-DIVISION":
			gotDiv = true
		case "MAIN-SECTION", "WORKING-STORAGE-SECTION":
			gotSection = true
		}
	}
	var gotCall, gotCopy bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeCalls && ed.To == "unresolved::GREET" {
			gotCall = true
		}
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::COMMONLIB" {
			gotCopy = true
		}
	}
	assert.True(t, gotProg)
	assert.True(t, gotDiv)
	assert.True(t, gotSection)
	assert.True(t, gotCall)
	assert.True(t, gotCopy)
}

func TestCobolExtractor_EmptyInput(t *testing.T) {
	res, err := NewCobolExtractor().Extract("e.cbl", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
