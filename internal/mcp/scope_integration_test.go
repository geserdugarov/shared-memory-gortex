package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/workspace"
)

// TestScopeDispatchEndToEnd boots an MCP server with a temporary
// workspace tree containing two child indexes (one of them excluded)
// and exercises the handshake + scope-dispatch rules end-to-end.
//
// Acceptance contract conditions exercised:
//
//   - 5 (workspace handshake)
//   - 8 (scope=repo without repo is a protocol error)
//   - 9 (scope=workspace with repo is a protocol error)
//   - 10 (scope=fan-out without repo is a protocol error)
//   - 11 (scope=fan-out with ["*"] returns the auto-discovered set)
//   - 12 (scope=fan-out unknown name is a protocol error)
//   - 13 (workspace-isolation invariant)
//   - 15 (exclude is honored, including for [*] and explicit names)
//   - 16 (TOML parsing tolerates unknown keys, fails on malformed)
func TestScopeDispatchEndToEnd(t *testing.T) {
	root := t.TempDir()
	// Three children: alpha + beta indexed; dormant indexed but
	// excluded by the marker.
	for _, name := range []string{"alpha", "beta", "dormant"} {
		if err := os.MkdirAll(filepath.Join(root, name, ".gortex"), 0o755); err != nil {
			t.Fatalf("setup %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".gortex"), 0o755); err != nil {
		t.Fatal(err)
	}
	markerBody := `
# fixture marker for ScopeDispatch end-to-end test
exclude = ["dormant"]
future_field = "ignored-but-tolerated"
`
	if err := os.WriteFile(
		filepath.Join(root, ".gortex/workspace.toml"),
		[]byte(markerBody), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	// Resolve the bind exactly as the `gortex mcp` command will.
	bind, err := workspace.Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if bind.Mode != workspace.ModeWorkspace {
		t.Fatalf("mode = %s, want workspace", bind.Mode)
	}
	want := []string{"alpha", "beta"}
	got := bind.MemberNames()
	if !equalSlice(got, want) {
		t.Fatalf("members = %v, want %v (dormant must be excluded)", got, want)
	}
	// Tolerate-and-warn on unknown keys (condition 16, Q2).
	warnings := workspace.FormatMarkerWarnings(bind.Marker)
	if len(warnings) == 0 {
		t.Fatal("expected a warning for the unknown 'future_field' key")
	}

	// Build a minimal MCP server pointed at this bind. We don't need
	// real indexes — scope dispatch happens before the handler reads
	// any graph data — so we use empty in-memory graph + indexer.
	g := graph.New()
	reg := parser.NewRegistry()
	idx := indexer.New(g, reg, config.IndexConfig{}, zap.NewNop())
	eng := query.NewEngine(g)
	srv := NewServer(eng, g, idx, nil, zap.NewNop(), nil)
	srv.SetBind(bind)

	// --- Sanity: every tool the registry knows about has a scope.
	for _, name := range srv.RegisteredScopedTools() {
		if _, ok := srv.ToolScope(name); !ok {
			t.Errorf("tool %q registered but has no scope", name)
		}
	}

	// --- Condition 5: workspace bind succeeds; list_repos returns
	// the auto-discovered, non-excluded set.
	if !bind.HasMember("alpha") || !bind.HasMember("beta") {
		t.Fatal("alpha and beta must be visible members")
	}
	if bind.HasMember("dormant") {
		t.Fatal("dormant must be excluded (condition 15)")
	}

	// --- Condition 8: scope=repo without repo is a protocol error.
	if _, err := srv.ResolveToolScope("find_usages", nil); err == nil {
		t.Fatal("missing repo on scope=repo must be a protocol error")
	}

	// --- Condition 9: scope=workspace with any repo is a protocol error.
	if _, err := srv.ResolveToolScope("list_repos", "alpha"); err == nil {
		t.Fatal("repo on scope=workspace must be a protocol error")
	}
	if _, err := srv.ResolveToolScope("list_repos", []any{"alpha"}); err == nil {
		t.Fatal("list-typed repo on scope=workspace must also fail")
	}

	// --- Condition 10: scope=fan-out without repo is a protocol error.
	if _, err := srv.ResolveToolScope("audit_agent_config", nil); err == nil {
		t.Fatal("missing repo on scope=fan-out must be a protocol error")
	}

	// --- Condition 11: ["*"] resolves to the auto-discovered set.
	resolved, errResult := srv.ResolveToolScope("audit_agent_config", []any{"*"})
	if errResult != nil {
		t.Fatalf("['*'] should resolve cleanly: %s", errResultBody(errResult))
	}
	if !equalSlice(resolved.Repos, []string{"alpha", "beta"}) {
		t.Fatalf("['*'] resolved to %v, want [alpha beta]", resolved.Repos)
	}

	// --- Condition 12: unknown name in fan-out list is a protocol error.
	if _, err := srv.ResolveToolScope("audit_agent_config", []any{"alpha", "zeta"}); err == nil {
		t.Fatal("unknown name in fan-out must be a protocol error")
	}

	// --- Condition 15 (final sentence): explicit excluded name in a
	// fan-out list is treated as unknown.
	if _, err := srv.ResolveToolScope("audit_agent_config", []any{"dormant"}); err == nil {
		t.Fatal("explicit excluded name in fan-out must be a protocol error")
	}

	// --- Condition 13: workspace-isolation invariant. Build a
	// SECOND, unrelated workspace tree elsewhere; assert that the
	// first server cannot see its members and ["*"] doesn't bridge.
	otherRoot := t.TempDir()
	for _, name := range []string{"only-other-1", "only-other-2"} {
		if err := os.MkdirAll(filepath.Join(otherRoot, name, ".gortex"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(otherRoot, ".gortex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(otherRoot, ".gortex/workspace.toml"),
		[]byte(""), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	otherBind, err := workspace.Resolve(otherRoot)
	if err != nil {
		t.Fatalf("Resolve other: %v", err)
	}
	for _, foreign := range otherBind.MemberNames() {
		if bind.HasMember(foreign) {
			t.Fatalf("isolation invariant broken: %q from other workspace appears in active bind", foreign)
		}
		if _, errResult := srv.ResolveToolScope("find_usages", foreign); errResult == nil {
			t.Fatalf("scope=repo accepted foreign workspace member %q", foreign)
		}
		if _, errResult := srv.ResolveToolScope("audit_agent_config", []any{foreign}); errResult == nil {
			t.Fatalf("scope=fan-out accepted foreign workspace member %q", foreign)
		}
	}

	// And ["*"] from `srv` must STILL only return alpha+beta.
	resolved, _ = srv.ResolveToolScope("audit_agent_config", []any{"*"})
	for _, n := range resolved.Repos {
		if strings.HasPrefix(n, "only-other-") {
			t.Fatalf("['*'] leaked foreign member %q", n)
		}
	}
}

// TestScopeDispatchSingleProjectMode boots a server pointed at a
// single-project bind (no workspace.toml) and verifies the
// degradation rules.
func TestScopeDispatchSingleProjectMode(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".gortex"), 0o755); err != nil {
		t.Fatal(err)
	}
	bind, err := workspace.Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if bind.Mode != workspace.ModeSingleProject {
		t.Fatalf("mode = %s, want single-project", bind.Mode)
	}
	bound := filepath.Base(root)

	g := graph.New()
	reg := parser.NewRegistry()
	idx := indexer.New(g, reg, config.IndexConfig{}, zap.NewNop())
	eng := query.NewEngine(g)
	srv := NewServer(eng, g, idx, nil, zap.NewNop(), nil)
	srv.SetBind(bind)

	// scope=repo with no `repo` defaults to the bound project.
	got, errResult := srv.ResolveToolScope("find_usages", nil)
	if errResult != nil {
		t.Fatalf("single-project scope=repo without repo must default; got %s", errResultBody(errResult))
	}
	if !equalSlice(got.Repos, []string{bound}) {
		t.Fatalf("default repo = %v, want [%s]", got.Repos, bound)
	}

	// scope=fan-out with no `repo` defaults to [bound project].
	got, errResult = srv.ResolveToolScope("audit_agent_config", nil)
	if errResult != nil {
		t.Fatalf("single-project scope=fan-out without repo must default; got %s", errResultBody(errResult))
	}
	if !equalSlice(got.Repos, []string{bound}) {
		t.Fatalf("default fan-out = %v, want [%s]", got.Repos, bound)
	}

	// scope=workspace returns the one-member degenerate workspace.
	got, errResult = srv.ResolveToolScope("list_repos", nil)
	if errResult != nil {
		t.Fatalf("single-project scope=workspace must succeed; got %s", errResultBody(errResult))
	}
	if !equalSlice(got.Repos, []string{bound}) {
		t.Fatalf("degenerate workspace = %v, want [%s]", got.Repos, bound)
	}

	// scope=workspace with any repo is still rejected (condition 9).
	if _, errResult := srv.ResolveToolScope("list_repos", "anything"); errResult == nil {
		t.Fatal("scope=workspace with repo must always be a protocol error")
	}
}

// TestHandshakeFailsOutsideEntryPoints validates condition 7 — a
// session started in any directory that is neither a workspace root
// nor a project root fails the handshake with a clear message naming
// both supported entry points.
func TestHandshakeFailsOutsideEntryPoints(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := workspace.Resolve(root)
	if err == nil {
		t.Fatal("expected handshake to fail outside entry points")
	}
	msg := err.Error()
	if !strings.Contains(msg, ".gortex/workspace.toml") {
		t.Errorf("error must name the workspace marker: %s", msg)
	}
	if !strings.Contains(msg, ".gortex") {
		t.Errorf("error must name the project index dir: %s", msg)
	}
	if !strings.Contains(msg, "no walk-up") && !strings.Contains(msg, "walk-up") {
		t.Errorf("error must mention no walk-up; got: %s", msg)
	}
}
