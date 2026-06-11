package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReadFamilyToolsRecordSavings pins the savings recording surface on a
// single-repo server (the issue-67 shape: one tracked repo, unprefixed
// nodes). Every read-family tool — read_file, get_file_summary,
// get_editing_context — and the original get_symbol_source must book an
// observation; before the lone-repo resolution fix none of them could,
// because the record sites sit behind resolveNodePath/resolveFilePath.
func TestReadFamilyToolsRecordSavings(t *testing.T) {
	srv, _, _ := newSingleRepoServer(t)
	ctx := context.Background()

	calls := func() int64 {
		return srv.tokenStats.snapshot()["calls_counted"].(int64)
	}
	require.Equal(t, int64(0), calls())

	res := callToolByName(t, srv, ctx, "read_file", map[string]any{"path": "main.go"})
	require.False(t, res.IsError, "read_file must succeed on a bare-relative path in single-repo mode")
	require.Equal(t, int64(1), calls(), "read_file must record a savings observation")

	res = callToolByName(t, srv, ctx, "get_file_summary", map[string]any{"path": "main.go"})
	require.False(t, res.IsError)
	require.Equal(t, int64(2), calls(), "get_file_summary must record a savings observation")

	res = callToolByName(t, srv, ctx, "get_editing_context", map[string]any{"path": "main.go"})
	require.False(t, res.IsError)
	require.Equal(t, int64(3), calls(), "get_editing_context must record a savings observation")

	res = callToolByName(t, srv, ctx, "get_symbol_source", map[string]any{"id": "main.go::Hello"})
	require.False(t, res.IsError, "get_symbol_source must resolve unprefixed single-repo nodes")
	require.Equal(t, int64(4), calls(), "get_symbol_source must record a savings observation")

	snap := srv.tokenStats.snapshot()
	require.Greater(t, snap["tokens_returned"].(int64), int64(0))
}
