package mcp

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func freshReq(args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

func TestTargetRepoRelFile(t *testing.T) {
	require.Equal(t, "internal/x.go",
		targetRepoRelFile("read_file", freshReq(map[string]any{"path": "internal/x.go"}), ""))
	require.Equal(t, "internal/x.go",
		targetRepoRelFile("read_file", freshReq(map[string]any{"path": "gortex/internal/x.go"}), "gortex"))
	require.Equal(t, "a.go",
		targetRepoRelFile("get_symbol_source", freshReq(map[string]any{"id": "a.go::Foo"}), ""))
	// Non-file tools yield no target.
	require.Equal(t, "",
		targetRepoRelFile("search_symbols", freshReq(map[string]any{"query": "x"}), ""))
	// Empty args yield no target.
	require.Equal(t, "",
		targetRepoRelFile("read_file", freshReq(map[string]any{}), ""))
}

func TestDecorateResultWithFreshness(t *testing.T) {
	rider := map[string]any{"file": "a.go", "stale": true}

	// JSON object: rider attached under "freshness", original keys kept.
	got := decorateResultWithFreshness(mcp.NewToolResultText(`{"x":1}`), rider)
	text, ok := singleTextContent(got)
	require.True(t, ok)
	var obj map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &obj))
	require.Equal(t, float64(1), obj["x"])
	require.NotNil(t, obj["freshness"])

	// Non-JSON-object payload (GCX/TOON) is left untouched.
	got2 := decorateResultWithFreshness(mcp.NewToolResultText("GCX1 tool=foo\nrow1"), rider)
	text2, _ := singleTextContent(got2)
	require.Equal(t, "GCX1 tool=foo\nrow1", text2)

	// Empty rider is a no-op.
	got3 := decorateResultWithFreshness(mcp.NewToolResultText(`{"x":1}`), nil)
	text3, _ := singleTextContent(got3)
	require.Equal(t, `{"x":1}`, text3)
}
