package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/query"
)

// siblingDiffGitRepo creates a git repo with a base commit and a HEAD commit
// that mutates three Go files in two packages, so the changeset has several
// changed files. Returns the repo root and the relative paths of the changed
// files (focus + two siblings).
func siblingDiffGitRepo(t *testing.T) (root, fileA, fileB, fileC string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("config", "diff.mnemonicPrefix", "false")
	run("config", "diff.noprefix", "false")

	fileA = filepath.Join("internal", "alpha", "a.go")
	fileB = filepath.Join("internal", "alpha", "b.go")
	fileC = filepath.Join("internal", "beta", "c.go")
	write := func(rel, src string) {
		abs := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(src), 0o644))
	}
	write(fileA, "package alpha\n\nfunc Alpha() int {\n\treturn 1\n}\n")
	write(fileB, "package alpha\n\nfunc Beta() int {\n\treturn 2\n}\n")
	write(fileC, "package beta\n\nfunc Gamma() int {\n\treturn 3\n}\n")
	run("add", ".")
	run("commit", "-m", "base")
	run("tag", "base-ref")

	// HEAD commit mutates the body of every function so all three files change.
	write(fileA, "package alpha\n\nfunc Alpha() int {\n\tx := 1\n\treturn x\n}\n")
	write(fileB, "package alpha\n\nfunc Beta() int {\n\ty := 2\n\treturn y\n}\n")
	write(fileC, "package beta\n\nfunc Gamma() int {\n\tz := 3\n\treturn z\n}\n")
	run("add", ".")
	run("commit", "-m", "change")
	return dir, fileA, fileB, fileC
}

// indexedSiblingServer indexes the repo and builds a server over it.
func indexedSiblingServer(t *testing.T, dir string) *Server {
	t.Helper()
	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, config.Default().Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)
	srv := NewServer(query.NewEngine(g), g, idx, nil, zap.NewNop(), nil)
	srv.RunAnalysis()
	return srv
}

func callSiblingDiff(t *testing.T, srv *Server, args map[string]any) *mcplib.CallToolResult {
	t.Helper()
	req := mcplib.CallToolRequest{}
	req.Params.Name = "sibling_diff_context"
	req.Params.Arguments = args
	res, err := srv.handleSiblingDiffContext(t.Context(), req)
	require.NoError(t, err)
	return res
}

type siblingDiffOut struct {
	Focus     []string `json:"focus"`
	Total     int      `json:"total"`
	Truncated bool     `json:"truncated"`
	Siblings  []struct {
		File     string  `json:"file"`
		Relation string  `json:"relation"`
		Score    float64 `json:"score"`
		Diff     string  `json:"diff"`
	} `json:"siblings"`
}

func decodeSiblingDiff(t *testing.T, res *mcplib.CallToolResult) siblingDiffOut {
	t.Helper()
	require.False(t, res.IsError, "errored: %v", res)
	var out siblingDiffOut
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out))
	return out
}

// TestSiblingDiffContext_ExcludesFocusReturnsSiblings asserts the focus file is
// excluded and the other changed files come back with their raw diffs.
func TestSiblingDiffContext_ExcludesFocusReturnsSiblings(t *testing.T) {
	dir, fileA, fileB, fileC := siblingDiffGitRepo(t)
	srv := indexedSiblingServer(t, dir)

	out := decodeSiblingDiff(t, callSiblingDiff(t, srv, map[string]any{
		"base":        "base-ref",
		"focus_files": fileA,
	}))

	require.Equal(t, []string{fileA}, out.Focus)
	require.Equal(t, 2, out.Total, "two siblings expected (b, c)")

	got := map[string]string{}
	for _, sib := range out.Siblings {
		got[sib.File] = sib.Diff
		require.NotEqual(t, fileA, sib.File, "focus file must be excluded")
		require.NotEmpty(t, sib.Relation, "every sibling carries a relation tag")
	}
	require.Contains(t, got, fileB)
	require.Contains(t, got, fileC)

	// Each sibling carries the RAW unified diff text for that file only.
	require.Contains(t, got[fileB], "+++ b/"+filepath.ToSlash(fileB))
	require.Contains(t, got[fileB], "@@")
	require.Contains(t, got[fileB], "y := 2")
	require.NotContains(t, got[fileB], "x := 1", "sibling b's diff must not include focus a's hunks")

	require.Contains(t, got[fileC], "+++ b/"+filepath.ToSlash(fileC))
	require.Contains(t, got[fileC], "z := 3")
}

// TestSiblingDiffContext_FocusSymbolID resolves the focus file from a changed
// symbol's ID and excludes that file.
func TestSiblingDiffContext_FocusSymbolID(t *testing.T) {
	dir, fileA, fileB, fileC := siblingDiffGitRepo(t)
	srv := indexedSiblingServer(t, dir)

	// Alpha lives in fileA — find its node ID from the graph.
	var alphaID string
	for _, n := range srv.graph.GetFileNodes(fileA) {
		if n.Name == "Alpha" {
			alphaID = n.ID
		}
	}
	require.NotEmpty(t, alphaID, "Alpha symbol must be indexed")

	out := decodeSiblingDiff(t, callSiblingDiff(t, srv, map[string]any{
		"base":            "base-ref",
		"focus_symbol_id": alphaID,
	}))
	require.Equal(t, []string{fileA}, out.Focus)
	require.Equal(t, 2, out.Total)
	for _, sib := range out.Siblings {
		require.NotEqual(t, fileA, sib.File)
	}
	_ = fileB
	_ = fileC
}

// TestSiblingDiffContext_Relation asserts same-package siblings outrank a
// cross-package sibling (directory proximity), so the ranking is deterministic.
func TestSiblingDiffContext_Relation(t *testing.T) {
	dir, fileA, fileB, fileC := siblingDiffGitRepo(t)
	srv := indexedSiblingServer(t, dir)

	out := decodeSiblingDiff(t, callSiblingDiff(t, srv, map[string]any{
		"base":        "base-ref",
		"focus_files": fileA,
	}))
	require.Equal(t, 2, out.Total)

	score := map[string]float64{}
	for _, sib := range out.Siblings {
		score[sib.File] = sib.Score
	}
	// b.go shares the alpha directory with the focus a.go; c.go lives in beta.
	require.Greater(t, score[fileB], score[fileC],
		"same-directory sibling must outrank the cross-directory sibling")
	// Ranking is highest-score-first.
	require.Equal(t, fileB, out.Siblings[0].File)
}

// TestSiblingDiffContext_EmptyChangeset returns total:0 with no siblings.
func TestSiblingDiffContext_EmptyChangeset(t *testing.T) {
	dir, fileA, _, _ := siblingDiffGitRepo(t)
	srv := indexedSiblingServer(t, dir)

	// Compare HEAD against itself — no changes.
	out := decodeSiblingDiff(t, callSiblingDiff(t, srv, map[string]any{
		"scope":       "compare",
		"base_ref":    "HEAD",
		"focus_files": fileA,
	}))
	require.Equal(t, 0, out.Total)
	require.Empty(t, out.Siblings)
}

// TestSiblingDiffContext_GCXAndTOONAndBudget covers the wire-format + budget
// contract.
func TestSiblingDiffContext_GCXAndTOONAndBudget(t *testing.T) {
	dir, fileA, _, _ := siblingDiffGitRepo(t)
	srv := indexedSiblingServer(t, dir)

	base := map[string]any{"base": "base-ref", "focus_files": fileA}

	// GCX round-trip: section headers must appear.
	gcxArgs := map[string]any{}
	for k, v := range base {
		gcxArgs[k] = v
	}
	gcxArgs["format"] = "gcx"
	gcx := callSiblingDiff(t, srv, gcxArgs)
	require.False(t, gcx.IsError)
	gtext := gcx.Content[0].(mcplib.TextContent).Text
	require.Contains(t, gtext, "sibling_diff_context.summary")
	require.Contains(t, gtext, "sibling_diff_context.siblings")

	// max_bytes budget is honoured (response stays bounded vs the full diff).
	budgetArgs := map[string]any{}
	for k, v := range base {
		budgetArgs[k] = v
	}
	budgetArgs["format"] = "gcx"
	budgetArgs["max_bytes"] = float64(140)
	budgeted := callSiblingDiff(t, srv, budgetArgs)
	require.False(t, budgeted.IsError)
	require.LessOrEqual(t, len(budgeted.Content[0].(mcplib.TextContent).Text), 600)

	// TOON round-trip: still carries the total key.
	toonArgs := map[string]any{}
	for k, v := range base {
		toonArgs[k] = v
	}
	toonArgs["format"] = "toon"
	toon := callSiblingDiff(t, srv, toonArgs)
	require.False(t, toon.IsError)
	require.Contains(t, toon.Content[0].(mcplib.TextContent).Text, "total")
}

// TestSiblingDiffContext_RegisteredEagerly asserts the review-engine tool is in
// the eager (hot) set — published in tools/list at session start — unlike the
// deferred PR tools, so a reviewing agent does not pay a discovery round-trip.
func TestSiblingDiffContext_RegisteredEagerly(t *testing.T) {
	require.True(t, hotEagerTools["sibling_diff_context"],
		"sibling_diff_context must be eagerly registered (hot), not deferred")

	// And it is actually live in tools/list even with the lazy split enabled.
	t.Setenv("GORTEX_LAZY_TOOLS", "1")
	srv, _ := setupTestServer(t)
	live := srv.mcpServer.ListTools()
	require.Contains(t, live, "sibling_diff_context",
		"eager review tool must appear in tools/list without tools_search expansion")
	require.False(t, srv.lazy.IsDeferred("sibling_diff_context"),
		"sibling_diff_context must not be deferred")
}
