package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: create the bare project directory with `.gortex/` index.
func makeProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, IndexDir), 0o755); err != nil {
		t.Fatalf("makeProject %s: %v", dir, err)
	}
}

// helper: create a workspace root with `.gortex/workspace.toml`.
func makeWorkspace(t *testing.T, dir, tomlBody string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, IndexDir), 0o755); err != nil {
		t.Fatalf("makeWorkspace %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, MarkerFile), []byte(tomlBody), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}

func TestResolve_WorkspaceMode_AutoDiscovery(t *testing.T) {
	root := t.TempDir()
	// Two children with .gortex/, one without (must not appear in members).
	makeProject(t, filepath.Join(root, "alpha"))
	makeProject(t, filepath.Join(root, "beta"))
	if err := os.MkdirAll(filepath.Join(root, "no-index", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeWorkspace(t, root, "# marker\n")

	bind, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if bind.Mode != ModeWorkspace {
		t.Fatalf("mode = %s, want workspace", bind.Mode)
	}
	got := bind.MemberNames()
	want := []string{"alpha", "beta"}
	if !equalSlice(got, want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
}

func TestResolve_WorkspaceMode_HonorsExclude(t *testing.T) {
	root := t.TempDir()
	makeProject(t, filepath.Join(root, "alpha"))
	makeProject(t, filepath.Join(root, "beta"))
	makeProject(t, filepath.Join(root, "dormant"))
	makeWorkspace(t, root, `exclude = ["dormant"]`+"\n")

	bind, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := bind.MemberNames()
	want := []string{"alpha", "beta"}
	if !equalSlice(got, want) {
		t.Fatalf("members = %v, want %v (excluded should not appear)", got, want)
	}
	if bind.HasMember("dormant") {
		t.Fatal("HasMember should not see excluded child")
	}
}

func TestResolve_WorkspaceMode_TolerateUnknownKeys(t *testing.T) {
	root := t.TempDir()
	makeProject(t, filepath.Join(root, "alpha"))
	makeWorkspace(t, root, `exclude = []`+"\n"+`future_field = "value"`+"\n"+`another_one = 42`+"\n")

	bind, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, ok := bind.Marker.Unknown["future_field"]; !ok {
		t.Fatal("unknown key future_field not preserved")
	}
	if _, ok := bind.Marker.Unknown["another_one"]; !ok {
		t.Fatal("unknown key another_one not preserved")
	}
	warnings := FormatMarkerWarnings(bind.Marker)
	if len(warnings) != 2 {
		t.Fatalf("warnings = %d, want 2: %v", len(warnings), warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "unknown key") {
			t.Errorf("warning %q missing the expected phrase", w)
		}
	}
}

func TestResolve_WorkspaceMode_MalformedTOML(t *testing.T) {
	root := t.TempDir()
	makeProject(t, filepath.Join(root, "alpha"))
	makeWorkspace(t, root, "exclude = [\n# unterminated array\n")

	_, err := Resolve(root)
	if err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
	if !strings.Contains(err.Error(), "malformed TOML") && !strings.Contains(err.Error(), "parse") {
		t.Fatalf("error %v does not mention parse failure", err)
	}
}

func TestResolve_WorkspaceMode_WrongTypedExclude(t *testing.T) {
	root := t.TempDir()
	makeProject(t, filepath.Join(root, "alpha"))
	makeWorkspace(t, root, `exclude = "not-an-array"`+"\n")

	_, err := Resolve(root)
	if err == nil {
		t.Fatal("expected error for wrong-typed exclude, got nil")
	}
	if !strings.Contains(err.Error(), "exclude") {
		t.Fatalf("error %v should mention exclude", err)
	}
}

func TestResolve_WorkspaceMode_ExcludeElementWrongType(t *testing.T) {
	root := t.TempDir()
	makeProject(t, filepath.Join(root, "alpha"))
	makeWorkspace(t, root, `exclude = ["ok", 42]`+"\n")

	_, err := Resolve(root)
	if err == nil {
		t.Fatal("expected error for non-string element in exclude")
	}
}

func TestResolve_SingleProjectMode(t *testing.T) {
	root := t.TempDir()
	makeProject(t, root)

	bind, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if bind.Mode != ModeSingleProject {
		t.Fatalf("mode = %s, want single-project", bind.Mode)
	}
	if len(bind.Members) != 1 {
		t.Fatalf("members = %d, want 1", len(bind.Members))
	}
	if bind.Members[0].Name != filepath.Base(root) {
		t.Fatalf("member name = %q, want %q", bind.Members[0].Name, filepath.Base(root))
	}
}

func TestResolve_NotEntryPoint(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Resolve(root)
	if err == nil {
		t.Fatal("expected ErrNotEntryPoint, got nil")
	}
	if !errors.Is(err, ErrNotEntryPoint) {
		t.Fatalf("expected ErrNotEntryPoint, got %v", err)
	}
	msg := err.Error()
	// The error message must name BOTH supported entry points.
	if !strings.Contains(msg, MarkerFile) {
		t.Errorf("error %q must name workspace marker %q", msg, MarkerFile)
	}
	if !strings.Contains(msg, IndexDir) {
		t.Errorf("error %q must name project index dir %q", msg, IndexDir)
	}
	if !strings.Contains(msg, "no walk-up") && !strings.Contains(msg, "walk-up") {
		t.Errorf("error %q should mention no walk-up", msg)
	}
}

func TestResolve_NoWalkUp(t *testing.T) {
	// Confirm that if cwd is a SUBDIRECTORY of a project root, Resolve
	// fails — there is no walk-up.
	root := t.TempDir()
	makeProject(t, root)
	sub := filepath.Join(root, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Resolve(sub)
	if !errors.Is(err, ErrNotEntryPoint) {
		t.Fatalf("walk-up must NOT succeed; got %v", err)
	}
}

func TestResolve_NoWalkUp_FromWorkspaceChild(t *testing.T) {
	// A subdirectory of a workspace ROOT (not a project) also fails —
	// the marker is not "discovered" upward.
	root := t.TempDir()
	makeWorkspace(t, root, "")
	makeProject(t, filepath.Join(root, "alpha"))
	deeper := filepath.Join(root, "alpha", "subdir")
	if err := os.MkdirAll(deeper, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Resolve(deeper)
	if !errors.Is(err, ErrNotEntryPoint) {
		t.Fatalf("walk-up from workspace child must fail; got %v", err)
	}
}

func TestWorkspaceIsolation_MembersBoundedByMarker(t *testing.T) {
	// Two unrelated workspace roots side-by-side. A bind to A must see
	// only A's children; B is invisible.
	parent := t.TempDir()
	a := filepath.Join(parent, "ws-a")
	b := filepath.Join(parent, "ws-b")
	makeWorkspace(t, a, "")
	makeWorkspace(t, b, "")
	makeProject(t, filepath.Join(a, "child-a1"))
	makeProject(t, filepath.Join(a, "child-a2"))
	makeProject(t, filepath.Join(b, "child-b1"))

	bindA, err := Resolve(a)
	if err != nil {
		t.Fatalf("resolve A: %v", err)
	}
	if !bindA.HasMember("child-a1") || !bindA.HasMember("child-a2") {
		t.Fatalf("A must see its own children; got %v", bindA.MemberNames())
	}
	if bindA.HasMember("child-b1") || bindA.HasMember("ws-b") {
		t.Fatalf("A must NOT see B's children; got %v", bindA.MemberNames())
	}

	bindB, err := Resolve(b)
	if err != nil {
		t.Fatalf("resolve B: %v", err)
	}
	if !bindB.HasMember("child-b1") {
		t.Fatalf("B must see its own children; got %v", bindB.MemberNames())
	}
	if bindB.HasMember("child-a1") {
		t.Fatalf("B must NOT see A's children; got %v", bindB.MemberNames())
	}
}

func TestDiscoverMembers_SkipsHiddenDirs(t *testing.T) {
	root := t.TempDir()
	// `.gortex/` itself contains an index but is hidden — must not
	// appear as a member, even though it has the right shape.
	makeProject(t, filepath.Join(root, ".gortex"))
	makeProject(t, filepath.Join(root, "real"))
	makeWorkspace(t, root, "")

	bind, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if bind.HasMember(".gortex") {
		t.Fatal("hidden dir .gortex should never be a member")
	}
	if !bind.HasMember("real") {
		t.Fatal("real child must appear")
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
