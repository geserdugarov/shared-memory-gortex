// A Gortex MCP server may bind in exactly one of two modes:
//
//   - Workspace mode. The current working directory contains a
//     `.gortex/workspace.toml` marker. Members are auto-discovered:
//     every immediate-child directory containing its own `.gortex/`
//     index. The marker may carry an optional `exclude = [...]` list
//     that drops named children. There is no `members = [...]` key —
//     auto-discovery plus exclusions is the entire model.
//
//   - Single-project mode. The current working directory has a
//     `.gortex/` index but no `workspace.toml`. The server binds to
//     that single project and degrades workspace-shaped tools to a
//     one-member set (the bound project).
//
// Anywhere else, Resolve returns ErrNotEntryPoint with a message that
// names both supported entry points. There is no walk-up from a
// subdirectory: this is an explicit removal from the design
//
// The workspace-isolation invariant is enforced here too: members live
// strictly inside the resolved workspace root. There is no cross-
// workspace bridging — the active marker fixes the visible universe of
// repos for this server instance.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// MarkerFile is the relative path of the workspace marker — always
// `.gortex/workspace.toml`.
const MarkerFile = ".gortex/workspace.toml"

// IndexDir is the per-project index directory name. A directory that
// contains `.gortex/` (and not `.gortex/workspace.toml`) is a single-
// project root; an immediate-child directory of a workspace root that
// contains `.gortex/` is a workspace member.
const IndexDir = ".gortex"

// Mode is the bind mode chosen by Resolve.
type Mode int

const (
	// ModeWorkspace — cwd contains the marker file; members are
	// auto-discovered children with their own `.gortex/` index.
	ModeWorkspace Mode = iota + 1
	// ModeSingleProject — cwd contains `.gortex/` directly with no
	// marker; the server binds to that one project.
	ModeSingleProject
)

func (m Mode) String() string {
	switch m {
	case ModeWorkspace:
		return "workspace"
	case ModeSingleProject:
		return "single-project"
	default:
		return "unknown"
	}
}

// ErrNotEntryPoint is returned by Resolve when cwd is neither a
// workspace root nor a project root. The error message names both
// supported entry points — there is no walk-up.
var ErrNotEntryPoint = errors.New("not a Gortex entry point")

// Marker is the parsed `.gortex/workspace.toml`. Only `exclude` is
// recognised today; unknown top-level keys are tolerated and surfaced
// via the `Unknown` map so the caller can log a warning per key (Q2
// resolution).
type Marker struct {
	// Exclude lists immediate-child directory names that auto-discovery
	// must drop from the workspace member set. Names are matched
	// case-sensitively against `filepath.Base` of each candidate.
	Exclude []string

	// Unknown captures any top-level TOML key the parser didn't
	// recognise. Forward-compatibility: future fields can be added
	// without breaking older binaries — older binaries see the keys in
	// Unknown and log a warning instead of failing.
	Unknown map[string]any
}

// Bind is the result of Resolve. Exactly one of the two modes is set.
type Bind struct {
	// Mode tells which entry point was matched.
	Mode Mode

	// Root is the absolute path that Resolve walked. In workspace mode
	// this is the directory that holds `.gortex/workspace.toml`. In
	// single-project mode it is the project root.
	Root string

	// Marker is the parsed marker contents. Empty (zero value) in
	// single-project mode.
	Marker Marker

	// Members is the auto-discovered member set, sorted lexically by
	// name. In workspace mode this is the directory list, after
	// excludes. In single-project mode it is a degenerate one-element
	// list whose single member is the bound project (Member.Name is
	// the basename of Root).
	Members []Member
}

// Member is a single workspace member.
type Member struct {
	// Name is the immediate-child directory name (the value users put
	// in `repo` and `exclude` lists).
	Name string

	// Path is the absolute filesystem path to the member's root —
	// always `filepath.Join(Bind.Root, Name)` in workspace mode, equal
	// to Bind.Root in single-project mode.
	Path string
}

// MemberNames returns the member directory names in lexical order.
// Used by tools like `list_repos` and `["*"]` resolution.
func (b *Bind) MemberNames() []string {
	out := make([]string, 0, len(b.Members))
	for _, m := range b.Members {
		out = append(out, m.Name)
	}
	return out
}

// HasMember reports whether name appears in the resolved member set.
// Membership is authoritative for fan-out validation: an unknown name
// in a `repo: [...]` list is a protocol error (Q1 resolution).
func (b *Bind) HasMember(name string) bool {
	for _, m := range b.Members {
		if m.Name == name {
			return true
		}
	}
	return false
}

// Resolve performs the two-entry-point handshake from cwd.
//
// Order of checks:
//
//  1. If cwd contains `.gortex/workspace.toml`, parse the marker and
//     auto-discover members. ModeWorkspace.
//  2. Else if cwd contains `.gortex/`, bind to that single project.
//     ModeSingleProject.
//  3. Else return ErrNotEntryPoint with a message that names both
//     supported entry points.
//
// Any IO error parsing the marker (malformed TOML, wrong-typed
// `exclude`) is propagated wrapped — the handshake fails with a clear
// error so the user can fix the marker file rather than getting a
// silent fallback.
func Resolve(cwd string) (*Bind, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("workspace: resolve cwd: %w", err)
	}

	markerPath := filepath.Join(abs, MarkerFile)
	if info, statErr := os.Stat(markerPath); statErr == nil && !info.IsDir() {
		return resolveWorkspace(abs, markerPath)
	}

	indexPath := filepath.Join(abs, IndexDir)
	if info, statErr := os.Stat(indexPath); statErr == nil && info.IsDir() {
		return resolveSingleProject(abs)
	}

	return nil, NotEntryPointError(abs)
}

// NotEntryPointError builds the canonical error returned when cwd is
// neither a workspace root nor a project root. Exposed so callers
// (the `mcp` command, tests) can construct the same message without
// going through Resolve.
func NotEntryPointError(cwd string) error {
	return fmt.Errorf(
		"%w: %s is neither a workspace root (containing %s) nor a project root (containing %s/). "+
			"There is no walk-up — start the MCP server from one of these two locations",
		ErrNotEntryPoint, cwd, MarkerFile, IndexDir,
	)
}

// resolveWorkspace parses the marker, walks immediate children, and
// constructs the Bind for ModeWorkspace.
func resolveWorkspace(root, markerPath string) (*Bind, error) {
	marker, err := parseMarker(markerPath)
	if err != nil {
		return nil, fmt.Errorf("workspace: parse %s: %w", markerPath, err)
	}

	members, err := discoverMembers(root, marker.Exclude)
	if err != nil {
		return nil, fmt.Errorf("workspace: discover members under %s: %w", root, err)
	}

	return &Bind{
		Mode:    ModeWorkspace,
		Root:    root,
		Marker:  marker,
		Members: members,
	}, nil
}

func resolveSingleProject(root string) (*Bind, error) {
	name := filepath.Base(root)
	return &Bind{
		Mode: ModeSingleProject,
		Root: root,
		Members: []Member{
			{Name: name, Path: root},
		},
	}, nil
}

// discoverMembers lists immediate-child directories of root that
// contain their own `.gortex/` index, excluding names listed in
// excludes. Hidden directories (starting with `.`) are skipped — they
// are never workspace members.
func discoverMembers(root string, excludes []string) ([]Member, error) {
	excludeSet := make(map[string]struct{}, len(excludes))
	for _, name := range excludes {
		excludeSet[name] = struct{}{}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	out := make([]Member, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if _, skip := excludeSet[name]; skip {
			continue
		}
		childRoot := filepath.Join(root, name)
		indexPath := filepath.Join(childRoot, IndexDir)
		info, statErr := os.Stat(indexPath)
		if statErr != nil || !info.IsDir() {
			continue
		}
		out = append(out, Member{Name: name, Path: childRoot})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// parseMarker reads and parses `.gortex/workspace.toml`. Recognised
// keys: `exclude` (array<string>). Unknown top-level keys are kept in
// Marker.Unknown so the caller can log a warning per key.
//
// Strict validation:
//
//   - Malformed TOML → return error (handshake fails per condition 16).
//   - `exclude` of the wrong type → return error.
//
// Tolerant validation:
//
//   - Unknown top-level keys → kept in Unknown, no error.
func parseMarker(path string) (Marker, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Marker{}, err
	}

	var generic map[string]any
	if err := toml.Unmarshal(raw, &generic); err != nil {
		return Marker{}, fmt.Errorf("malformed TOML: %w", err)
	}

	m := Marker{Unknown: map[string]any{}}
	for key, value := range generic {
		switch key {
		case "exclude":
			arr, ok := value.([]any)
			if !ok {
				return Marker{}, fmt.Errorf(`"exclude" must be an array of strings, got %T`, value)
			}
			for i, v := range arr {
				s, ok := v.(string)
				if !ok {
					return Marker{}, fmt.Errorf(`"exclude"[%d] must be a string, got %T`, i, v)
				}
				m.Exclude = append(m.Exclude, s)
			}
		default:
			m.Unknown[key] = value
		}
	}

	return m, nil
}

// FormatMarkerWarnings returns a stable, human-readable line per
// unknown key in marker.Unknown, suitable for stderr logging at
// handshake time. Used by callers so the warning text is consistent
// across embedded and daemon paths.
func FormatMarkerWarnings(marker Marker) []string {
	if len(marker.Unknown) == 0 {
		return nil
	}
	keys := make([]string, 0, len(marker.Unknown))
	for k := range marker.Unknown {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf(
			`workspace marker: unknown key %q ignored (forward-compatible; check spelling)`, k))
	}
	return out
}
