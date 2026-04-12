package mcp

import (
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
)

// TestOrigin_BackfilledOnEveryEdge asserts that every edge returned by
// edge-surfacing tools carries an Origin tier, even when the underlying edge
// was produced before the A7 work (empty Origin). This is the contract that
// makes `min_tier` filtering meaningful for clients.
func TestOrigin_BackfilledOnEveryEdge(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "find_usages", map[string]any{"id": "main.go::helper"})
	require.False(t, result.IsError)

	var sg query.SubGraph
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcplib.TextContent).Text), &sg))
	require.Greater(t, len(sg.Edges), 0)

	validOrigins := []string{
		graph.OriginLSPResolved,
		graph.OriginLSPDispatch,
		graph.OriginASTResolved,
		graph.OriginASTInferred,
		graph.OriginTextMatched,
	}
	for _, e := range sg.Edges {
		assert.NotEmpty(t, e.Origin, "edge %s→%s must carry Origin", e.From, e.To)
		assert.Contains(t, validOrigins, e.Origin, "edge origin must be one of the documented tiers")
	}
}

// TestMinTier_FilterDropsLowTierEdges verifies that passing min_tier filters
// the edge set. The fixture's extracted edges are AST-grade, so a
// min_tier=lsp_resolved filter should drop them all.
func TestMinTier_FilterDropsLowTierEdges(t *testing.T) {
	srv, _ := setupTestServer(t)

	unfiltered := callTool(t, srv, "find_usages", map[string]any{"id": "main.go::helper"})
	require.False(t, unfiltered.IsError)
	var sgFull query.SubGraph
	require.NoError(t, json.Unmarshal([]byte(unfiltered.Content[0].(mcplib.TextContent).Text), &sgFull))
	require.Greater(t, len(sgFull.Edges), 0)

	filtered := callTool(t, srv, "find_usages", map[string]any{
		"id":       "main.go::helper",
		"min_tier": graph.OriginLSPResolved,
	})
	require.False(t, filtered.IsError)
	var sgFiltered query.SubGraph
	require.NoError(t, json.Unmarshal([]byte(filtered.Content[0].(mcplib.TextContent).Text), &sgFiltered))

	// Every surviving edge must meet the tier; since the fixture has no LSP
	// enrichment, we expect the filtered set to be empty.
	for _, e := range sgFiltered.Edges {
		assert.True(t, graph.MeetsMinTier(e.Origin, graph.OriginLSPResolved),
			"edge %s→%s origin=%s leaked through min_tier=lsp_resolved filter",
			e.From, e.To, e.Origin)
	}
	assert.LessOrEqual(t, len(sgFiltered.Edges), len(sgFull.Edges),
		"filtered edge count must not exceed unfiltered")
}

// TestMinTier_EmptyTierIsNoOp confirms that passing min_tier="" (or omitting
// it) yields the full edge set — nothing filtered.
func TestMinTier_EmptyTierIsNoOp(t *testing.T) {
	srv, _ := setupTestServer(t)

	withEmpty := callTool(t, srv, "get_callers", map[string]any{
		"id":       "main.go::helper",
		"min_tier": "",
	})
	require.False(t, withEmpty.IsError)
	var sgEmpty query.SubGraph
	require.NoError(t, json.Unmarshal([]byte(withEmpty.Content[0].(mcplib.TextContent).Text), &sgEmpty))

	omitted := callTool(t, srv, "get_callers", map[string]any{"id": "main.go::helper"})
	require.False(t, omitted.IsError)
	var sgOmitted query.SubGraph
	require.NoError(t, json.Unmarshal([]byte(omitted.Content[0].(mcplib.TextContent).Text), &sgOmitted))

	assert.Equal(t, len(sgOmitted.Edges), len(sgEmpty.Edges),
		"empty min_tier must match omitting the param")
}
