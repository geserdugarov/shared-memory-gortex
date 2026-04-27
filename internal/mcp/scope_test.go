package mcp

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/workspace"
)

// fixtureWorkspace constructs a minimal workspace.Bind for the
// dispatcher tests without touching the filesystem. Members are the
// caller-supplied list in name-only form.
func fixtureWorkspace(members ...string) *workspace.Bind {
	ms := make([]workspace.Member, 0, len(members))
	for _, n := range members {
		ms = append(ms, workspace.Member{Name: n, Path: "/tmp/" + n})
	}
	return &workspace.Bind{Mode: workspace.ModeWorkspace, Root: "/tmp", Members: ms}
}

func fixtureSingle(name string) *workspace.Bind {
	return &workspace.Bind{
		Mode:    workspace.ModeSingleProject,
		Root:    "/tmp/" + name,
		Members: []workspace.Member{{Name: name, Path: "/tmp/" + name}},
	}
}

// --- ScopeRepo --------------------------------------------------------

func TestScopeRepo_RequiresStringInWorkspaceMode(t *testing.T) {
	bind := fixtureWorkspace("alpha", "beta")

	got, errResult := ResolveScopedRepos(ScopeRepo, bind, nil)
	if errResult == nil {
		t.Fatalf("expected protocol error for missing repo, got %+v", got)
	}
	// `scope "repo"` is JSON-escaped to `scope \"repo\"`.
	assertErrorContains(t, errResult, `scope \"repo\"`)
	assertErrorContains(t, errResult, "string")
}

func TestScopeRepo_RejectsListInWorkspaceMode(t *testing.T) {
	bind := fixtureWorkspace("alpha", "beta")

	_, errResult := ResolveScopedRepos(ScopeRepo, bind, []any{"alpha"})
	if errResult == nil {
		t.Fatal("list-typed repo on scope=repo must be a protocol error")
	}
	assertErrorContains(t, errResult, "list")
}

func TestScopeRepo_AcceptsKnownMember(t *testing.T) {
	bind := fixtureWorkspace("alpha", "beta")

	got, errResult := ResolveScopedRepos(ScopeRepo, bind, "alpha")
	if errResult != nil {
		t.Fatalf("unexpected error: %s", errToString(errResult))
	}
	if got.Kind != ScopeRepo {
		t.Fatalf("kind = %v, want repo", got.Kind)
	}
	if !equalSlice(got.Repos, []string{"alpha"}) {
		t.Fatalf("repos = %v, want [alpha]", got.Repos)
	}
}

func TestScopeRepo_RejectsUnknownMember(t *testing.T) {
	bind := fixtureWorkspace("alpha", "beta")

	_, errResult := ResolveScopedRepos(ScopeRepo, bind, "zeta")
	if errResult == nil {
		t.Fatal("unknown repo must be rejected")
	}
	assertErrorContains(t, errResult, "zeta")
	assertErrorContains(t, errResult, "alpha")
	assertErrorContains(t, errResult, "beta")
}

func TestScopeRepo_RejectsEmptyString(t *testing.T) {
	bind := fixtureWorkspace("alpha")

	_, errResult := ResolveScopedRepos(ScopeRepo, bind, "")
	if errResult == nil {
		t.Fatal("empty string repo must be rejected")
	}
}

func TestScopeRepo_SingleProjectModeDefaults(t *testing.T) {
	bind := fixtureSingle("solo")

	got, errResult := ResolveScopedRepos(ScopeRepo, bind, nil)
	if errResult != nil {
		t.Fatalf("single-project should default repo: %s", errToString(errResult))
	}
	if !equalSlice(got.Repos, []string{"solo"}) {
		t.Fatalf("repos = %v, want [solo]", got.Repos)
	}
}

func TestScopeRepo_SingleProjectModeRejectsForeignRepo(t *testing.T) {
	bind := fixtureSingle("solo")

	_, errResult := ResolveScopedRepos(ScopeRepo, bind, "other")
	if errResult == nil {
		t.Fatal("single-project mode must reject explicit non-bound repo")
	}
}

// --- ScopeWorkspace ---------------------------------------------------

func TestScopeWorkspace_RejectsAnyRepo(t *testing.T) {
	bind := fixtureWorkspace("alpha")

	_, errResult := ResolveScopedRepos(ScopeWorkspace, bind, "alpha")
	if errResult == nil {
		t.Fatal("scope=workspace must reject string repo")
	}
	_, errResult = ResolveScopedRepos(ScopeWorkspace, bind, []any{"alpha"})
	if errResult == nil {
		t.Fatal("scope=workspace must reject list repo")
	}
	_, errResult = ResolveScopedRepos(ScopeWorkspace, bind, []any{"*"})
	if errResult == nil {
		t.Fatal("scope=workspace must reject [*] repo")
	}
}

func TestScopeWorkspace_NoRepoReturnsAllMembers(t *testing.T) {
	bind := fixtureWorkspace("alpha", "beta", "gamma")

	got, errResult := ResolveScopedRepos(ScopeWorkspace, bind, nil)
	if errResult != nil {
		t.Fatalf("unexpected: %s", errToString(errResult))
	}
	if !equalSlice(got.Repos, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("repos = %v, want all members", got.Repos)
	}
}

func TestScopeWorkspace_SingleProjectIsDegenerateOneMember(t *testing.T) {
	bind := fixtureSingle("solo")

	got, errResult := ResolveScopedRepos(ScopeWorkspace, bind, nil)
	if errResult != nil {
		t.Fatalf("unexpected: %s", errToString(errResult))
	}
	if !equalSlice(got.Repos, []string{"solo"}) {
		t.Fatalf("single-project workspace must have exactly the bound project; got %v", got.Repos)
	}
}

// --- ScopeFanOut ------------------------------------------------------

func TestScopeFanOut_RequiresListInWorkspaceMode(t *testing.T) {
	bind := fixtureWorkspace("alpha")

	_, errResult := ResolveScopedRepos(ScopeFanOut, bind, nil)
	if errResult == nil {
		t.Fatal("missing repo on scope=fan-out must be a protocol error")
	}
	// `["*"]` becomes `[\"*\"]` after JSON-escape inside the
	// TextContent payload.
	assertErrorContains(t, errResult, `[\"*\"]`)
}

func TestScopeFanOut_RejectsString(t *testing.T) {
	bind := fixtureWorkspace("alpha")

	_, errResult := ResolveScopedRepos(ScopeFanOut, bind, "alpha")
	if errResult == nil {
		t.Fatal("scope=fan-out must reject single-string repo")
	}
}

func TestScopeFanOut_EmptyListRejected(t *testing.T) {
	bind := fixtureWorkspace("alpha")

	_, errResult := ResolveScopedRepos(ScopeFanOut, bind, []any{})
	if errResult == nil {
		t.Fatal("empty list repo must be a protocol error")
	}
	assertErrorContains(t, errResult, "non-empty")
}

func TestScopeFanOut_StarSentinelExpandsToAllMembers(t *testing.T) {
	bind := fixtureWorkspace("alpha", "beta", "gamma")

	got, errResult := ResolveScopedRepos(ScopeFanOut, bind, []any{"*"})
	if errResult != nil {
		t.Fatalf("unexpected: %s", errToString(errResult))
	}
	if !equalSlice(got.Repos, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("[*] should resolve to all members; got %v", got.Repos)
	}
}

func TestScopeFanOut_NamedSubset(t *testing.T) {
	bind := fixtureWorkspace("alpha", "beta", "gamma")

	got, errResult := ResolveScopedRepos(ScopeFanOut, bind, []any{"alpha", "gamma"})
	if errResult != nil {
		t.Fatalf("unexpected: %s", errToString(errResult))
	}
	if !equalSlice(got.Repos, []string{"alpha", "gamma"}) {
		t.Fatalf("repos = %v, want [alpha gamma]", got.Repos)
	}
}

func TestScopeFanOut_UnknownNameInListIsProtocolError(t *testing.T) {
	bind := fixtureWorkspace("alpha", "beta")

	_, errResult := ResolveScopedRepos(ScopeFanOut, bind, []any{"alpha", "zeta"})
	if errResult == nil {
		t.Fatal("unknown name in fan-out list must be a protocol error (Q1)")
	}
	assertErrorContains(t, errResult, "zeta")
}

func TestScopeFanOut_StarSentinelMustBeAlone(t *testing.T) {
	bind := fixtureWorkspace("alpha", "beta")

	_, errResult := ResolveScopedRepos(ScopeFanOut, bind, []any{"*", "alpha"})
	if errResult == nil {
		t.Fatal("[*] cannot mix with named entries")
	}
	assertErrorContains(t, errResult, "mix")
}

func TestScopeFanOut_NamedSubsetDeduplicates(t *testing.T) {
	bind := fixtureWorkspace("alpha", "beta")

	got, errResult := ResolveScopedRepos(ScopeFanOut, bind, []any{"alpha", "beta", "alpha"})
	if errResult != nil {
		t.Fatalf("unexpected: %s", errToString(errResult))
	}
	if !equalSlice(got.Repos, []string{"alpha", "beta"}) {
		t.Fatalf("dedup expected; got %v", got.Repos)
	}
}

func TestScopeFanOut_SingleProjectDefaultsToBoundProject(t *testing.T) {
	bind := fixtureSingle("solo")

	got, errResult := ResolveScopedRepos(ScopeFanOut, bind, nil)
	if errResult != nil {
		t.Fatalf("single-project must default fan-out repo: %s", errToString(errResult))
	}
	if !equalSlice(got.Repos, []string{"solo"}) {
		t.Fatalf("repos = %v, want [solo]", got.Repos)
	}
}

func TestScopeFanOut_ExcludedNotResolvedByStar(t *testing.T) {
	// The bind member set is post-exclude (workspace.Resolve filters
	// excludes at handshake time). [*] therefore reflects exclusion
	// directly. Spec condition 11 + 15.
	bind := fixtureWorkspace("alpha", "beta") // dormant excluded at marker time

	got, _ := ResolveScopedRepos(ScopeFanOut, bind, []any{"*"})
	for _, name := range got.Repos {
		if name == "dormant" {
			t.Fatal("excluded member must not appear in [*] expansion")
		}
	}

	// Explicit naming of an excluded member is treated as unknown
	// (condition 15 final sentence).
	_, errResult := ResolveScopedRepos(ScopeFanOut, bind, []any{"dormant"})
	if errResult == nil {
		t.Fatal("explicit excluded name must be a protocol error")
	}
}

// --- Workspace-isolation invariant -----------------------------------

func TestWorkspaceIsolation_StarBoundedByActiveMarker(t *testing.T) {
	// Two unrelated workspaces. Resolving [*] against bind A must NOT
	// yield any of B's members. There is no bridging — the only repos
	// visible to a server instance are those carried in its bind.
	bindA := fixtureWorkspace("a-1", "a-2")
	bindB := fixtureWorkspace("b-1", "b-2")

	gotA, _ := ResolveScopedRepos(ScopeFanOut, bindA, []any{"*"})
	for _, name := range gotA.Repos {
		if strings.HasPrefix(name, "b-") {
			t.Fatalf("A's [*] leaked B's member %q; isolation broken", name)
		}
	}
	gotB, _ := ResolveScopedRepos(ScopeFanOut, bindB, []any{"*"})
	for _, name := range gotB.Repos {
		if strings.HasPrefix(name, "a-") {
			t.Fatalf("B's [*] leaked A's member %q; isolation broken", name)
		}
	}
}

func TestWorkspaceIsolation_NoSyntaxToReachOtherWorkspace(t *testing.T) {
	// Try to name a member that exists in another workspace but not
	// the active one. Must be rejected — there is no escape hatch.
	bindA := fixtureWorkspace("only-a")

	_, errResult := ResolveScopedRepos(ScopeFanOut, bindA, []any{"only-b"})
	if errResult == nil {
		t.Fatal("must reject foreign-workspace member name")
	}
	_, errResult = ResolveScopedRepos(ScopeRepo, bindA, "only-b")
	if errResult == nil {
		t.Fatal("must reject foreign-workspace member name (scope=repo)")
	}
}

// --- Registry --------------------------------------------------------

// TestEveryToolHasScope guards against future tool additions that
// forget to declare a scope.
//
// The test compares the scope-init map to the map of tools the
// dispatcher knows about. It does NOT assert against the in-memory
// MCP tool registry from mark3labs/mcp-go (that's exercised at
// runtime by the integration test); it asserts only on
// defaultToolScopes — when feature-owner adds a tool, they must add
// an entry here too.
func TestEveryDefaultToolHasScopeKind(t *testing.T) {
	if len(defaultToolScopes) == 0 {
		t.Fatal("defaultToolScopes empty — scope migration incomplete")
	}
	for name, scope := range defaultToolScopes {
		switch scope {
		case ScopeRepo, ScopeWorkspace, ScopeFanOut:
			// ok
		default:
			t.Errorf("tool %q has unrecognised scope %v", name, scope)
		}
	}
}

func TestScopeRegistry_RoundTrip(t *testing.T) {
	r := newScopeRegistry()
	r.set("find_usages", ScopeRepo)
	r.set("list_repos", ScopeWorkspace)
	r.set("audit_agent_config", ScopeFanOut)

	got, ok := r.get("find_usages")
	if !ok || got != ScopeRepo {
		t.Fatalf("get(find_usages) = (%v, %v), want (repo, true)", got, ok)
	}
	if names := r.allTools(); !equalSlice(names, []string{
		"audit_agent_config", "find_usages", "list_repos",
	}) {
		t.Fatalf("allTools sort wrong: %v", names)
	}
	snap := r.snapshot()
	if snap["find_usages"] != "repo" {
		t.Fatalf("snapshot(find_usages) = %q, want repo", snap["find_usages"])
	}
	if snap["list_repos"] != "workspace" {
		t.Fatalf("snapshot(list_repos) = %q, want workspace", snap["list_repos"])
	}
	if snap["audit_agent_config"] != "fan-out" {
		t.Fatalf("snapshot(audit_agent_config) = %q, want fan-out", snap["audit_agent_config"])
	}
}

// --- helpers ---------------------------------------------------------

// errResultBody concatenates the Text fields of every TextContent in
// res. NewStructuredErrorResult writes a single JSON-encoded
// TextContent so callers' substring assertions work directly.
func errResultBody(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func errToString(res *mcp.CallToolResult) string {
	if res == nil {
		return "<nil>"
	}
	return errResultBody(res)
}

func assertErrorContains(t *testing.T, res *mcp.CallToolResult, substr string) {
	t.Helper()
	body := errResultBody(res)
	if !strings.Contains(body, substr) {
		t.Fatalf("error body %q does not contain %q", body, substr)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
