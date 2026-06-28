package mcp

import (
	"context"
	"path/filepath"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func exportGraphTool(t *testing.T, srv *Server, ctx context.Context, args map[string]any) *mcplib.CallToolResult {
	t.Helper()
	req := mcplib.CallToolRequest{}
	req.Params.Name = "export_graph"
	req.Params.Arguments = args
	res, err := srv.handleExportGraph(ctx, req)
	require.NoError(t, err)
	return res
}

// TestExportGraph_ConfinesAgentOutputPaths proves the export write sink
// confines caller-named output_path / output_dir to indexed repository roots
// for an MCP agent session (the prompt-injection surface) while leaving the
// local CLI / control channel — which carries no session cwd — free to write
// anywhere the operator names.
func TestExportGraph_ConfinesAgentOutputPaths(t *testing.T) {
	srv, _, repoRoot := newSingleRepoServer(t)
	// Resolve the repo root the way the guard does (EvalSymlinks), so an
	// in-repo target compares equal on platforms where the temp dir is a
	// symlink (macOS /var -> /private/var).
	realRoot, err := filepath.EvalSymlinks(repoRoot)
	require.NoError(t, err)
	outside := t.TempDir()

	escapeFile := filepath.Join(outside, "escape.cypher")
	escapeDir := filepath.Join(outside, "escape-dir")

	// An MCP agent session always carries the connecting client's cwd.
	agentCtx := WithSessionCWD(context.Background(), realRoot)

	// 1. Agent + absolute output_path outside every repo root → refused, no write.
	res := exportGraphTool(t, srv, agentCtx, map[string]any{"format": "cypher", "output_path": escapeFile})
	require.True(t, res.IsError, "agent write outside every repo root must be refused")
	txt, _ := singleTextContent(res)
	require.Contains(t, txt, "outside every indexed repository")
	require.NoFileExists(t, escapeFile)

	// 2. Agent + absolute output_dir outside every repo root → refused before MkdirAll runs.
	res = exportGraphTool(t, srv, agentCtx, map[string]any{"format": "mermaid", "scope": "architecture", "output_dir": escapeDir})
	require.True(t, res.IsError, "agent out-dir outside every repo root must be refused")
	require.NoDirExists(t, escapeDir)

	// 3. Agent + in-repo output_path → allowed (no false positive: agents may still export within the repo).
	inFile := filepath.Join(realRoot, "graph.cypher")
	res = exportGraphTool(t, srv, agentCtx, map[string]any{"format": "cypher", "output_path": inFile})
	okTxt, _ := singleTextContent(res)
	require.False(t, res.IsError, "in-repo agent export must be allowed: %s", okTxt)
	require.FileExists(t, inFile)

	// 4. Local CLI / control channel (no session cwd) → may write outside the repo.
	cliRes := exportGraphTool(t, srv, context.Background(), map[string]any{"format": "cypher", "output_path": escapeFile})
	cliTxt, _ := singleTextContent(cliRes)
	require.False(t, cliRes.IsError, "CLI export must be unrestricted: %s", cliTxt)
	require.FileExists(t, escapeFile)
}
