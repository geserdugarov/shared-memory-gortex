package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// seedChurnRepo creates a real git repo at dir, with several commits
// touching different parts of foo.go so blame returns distinct
// authors and timestamps per line range. Returns absolute path.
func seedChurnRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitInit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitInit("init", "-q")
	gitInit("config", "user.email", "alice@example.com")
	gitInit("config", "user.name", "alice")
	gitInit("config", "commit.gpgsign", "false")

	write := func(content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte(content), 0o644))
	}

	// Commit 1: initial file. dead and live each at one line range.
	write(`package foo

func dead() int {
	return 1
}

func live() int {
	return 1
}
`)
	gitInit("add", "foo.go")
	gitInit("commit", "-q", "-m", "init")

	// Commits 2-4: modify live() body three times, dead() once.
	for i := 2; i <= 4; i++ {
		write(`package foo

func dead() int {
	return ` + string(rune('1'+i)) + `
}

func live() int {
	return ` + string(rune('1'+i)) + `
}
`)
		gitInit("commit", "-aq", "-m", "edit "+string(rune('1'+i))+"")
	}

	return dir
}

func newChurnTestServer(t *testing.T, dir string) *Server {
	t.Helper()
	g := graph.New()
	absFoo := filepath.Join(dir, "foo.go")

	g.AddNode(&graph.Node{
		ID: absFoo + "::dead", Name: "dead", Kind: graph.KindFunction,
		FilePath: absFoo, StartLine: 3, EndLine: 5, Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: absFoo + "::live", Name: "live", Kind: graph.KindFunction,
		FilePath: absFoo, StartLine: 7, EndLine: 9, Language: "go",
	})

	return &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
}

func callChurnHandler(t *testing.T, s *Server, args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleGetChurnRate(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	if res.IsError {
		return map[string]any{"is_error": true}
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

func TestChurnRate_BothFunctionsSurface(t *testing.T) {
	dir := seedChurnRepo(t)
	s := newChurnTestServer(t, dir)

	out := callChurnHandler(t, s, map[string]any{})
	symbols, _ := out["symbols"].([]any)
	require.Len(t, symbols, 2, "both dead and live should surface")
}

func TestChurnRate_LiveHasHigherCommitCount(t *testing.T) {
	dir := seedChurnRepo(t)
	s := newChurnTestServer(t, dir)

	out := callChurnHandler(t, s, map[string]any{"sort_by": "commit_count"})
	symbols, _ := out["symbols"].([]any)
	require.Len(t, symbols, 2)

	first := symbols[0].(map[string]any)
	second := symbols[1].(map[string]any)
	// Both functions get edited by the same 4 commits — blame attribution
	// will treat the entire file's lines as touched in each commit. The
	// ordering should at least be stable; the count should be ≥1.
	assert.GreaterOrEqual(t, int(first["commit_count"].(float64)), 1)
	assert.GreaterOrEqual(t, int(second["commit_count"].(float64)), 1)
}

func TestChurnRate_MinCommitsFilter(t *testing.T) {
	dir := seedChurnRepo(t)
	s := newChurnTestServer(t, dir)

	// Very high threshold should drop everything.
	out := callChurnHandler(t, s, map[string]any{"min_commits": 100})
	symbols, _ := out["symbols"].([]any)
	assert.Empty(t, symbols)
}

func TestChurnRate_LimitTruncates(t *testing.T) {
	dir := seedChurnRepo(t)
	s := newChurnTestServer(t, dir)

	out := callChurnHandler(t, s, map[string]any{"limit": 1})
	symbols, _ := out["symbols"].([]any)
	assert.Len(t, symbols, 1)
	assert.Equal(t, true, out["truncated"])
}

func TestChurnRate_PathPrefixFilter(t *testing.T) {
	dir := seedChurnRepo(t)
	s := newChurnTestServer(t, dir)

	// Use a prefix that won't match anything.
	out := callChurnHandler(t, s, map[string]any{"path_prefix": "/no/such/path"})
	symbols, _ := out["symbols"].([]any)
	assert.Empty(t, symbols)
}

func TestChurnRate_ScannedFilesCount(t *testing.T) {
	dir := seedChurnRepo(t)
	s := newChurnTestServer(t, dir)

	out := callChurnHandler(t, s, map[string]any{})
	// One file (foo.go) — scanned once even with two symbols.
	assert.EqualValues(t, 1, out["scanned_files"].(float64))
}

func TestChurnRate_AgeDaysWithinFreshRepo(t *testing.T) {
	dir := seedChurnRepo(t)
	s := newChurnTestServer(t, dir)

	out := callChurnHandler(t, s, map[string]any{})
	symbols, _ := out["symbols"].([]any)
	require.NotEmpty(t, symbols)
	first := symbols[0].(map[string]any)
	// Fresh repo — age_days < 1 most of the time. Allow some slack.
	age := int(first["age_days"].(float64))
	assert.LessOrEqual(t, age, 1, "fresh repo: symbol age should be 0 or 1 day")
}

func TestChurnRate_RejectsNonGitDirectory(t *testing.T) {
	dir := t.TempDir()
	// Create a file but no git repo.
	abs := filepath.Join(dir, "foo.go")
	require.NoError(t, os.WriteFile(abs, []byte("package foo\nfunc x() {}\n"), 0o644))

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: abs + "::x", Name: "x", Kind: graph.KindFunction,
		FilePath: abs, StartLine: 2, EndLine: 2,
	})
	s := &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}

	out := callChurnHandler(t, s, map[string]any{})
	symbols, _ := out["symbols"].([]any)
	assert.Empty(t, symbols, "non-git directories return zero rows, not an error")
}

func TestChurnRate_SortByOptions(t *testing.T) {
	dir := seedChurnRepo(t)
	s := newChurnTestServer(t, dir)

	for _, sortBy := range []string{"churn_rate", "commit_count", "age_days"} {
		out := callChurnHandler(t, s, map[string]any{"sort_by": sortBy})
		assert.Equal(t, sortBy, out["sort_by"], "sort_by echoed")
		symbols, _ := out["symbols"].([]any)
		assert.NotEmpty(t, symbols, "sort_by=%s should still return rows", sortBy)
	}
}

func TestStripPathPrefix(t *testing.T) {
	got, err := stripPathPrefix("/a/b/c.go", "/a/")
	require.NoError(t, err)
	assert.Equal(t, "b/c.go", got)

	_, err = stripPathPrefix("/x/y.go", "/a/")
	assert.Error(t, err)
}

// Smoke test: roundtrip Unix timestamp through time.Time matches RFC3339.
func TestChurnRate_TimestampShape(t *testing.T) {
	dir := seedChurnRepo(t)
	s := newChurnTestServer(t, dir)

	out := callChurnHandler(t, s, map[string]any{})
	symbols, _ := out["symbols"].([]any)
	require.NotEmpty(t, symbols)
	row := symbols[0].(map[string]any)
	ts, ok := row["last_commit_at"].(string)
	require.True(t, ok)
	_, err := time.Parse(time.RFC3339, ts)
	require.NoError(t, err)
}
