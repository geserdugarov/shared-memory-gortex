package mcp

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/analysis"
)

// registerReviewTools registers the review-engine tool group. Unlike most
// specialised tool groups, these tools are EAGER — their names live in
// hotEagerTools so they are published in the initial tools/list rather than
// hidden behind tools_search. A reviewing agent reaches for them on the first
// turn of a review task, so paying a discovery round-trip for them would be a
// regression. This group grows: it is the single registration site for the
// whole review surface, so later review tools append their addTool block here.
func (s *Server) registerReviewTools() {
	s.addTool(
		mcp.NewTool("sibling_diff_context",
			mcp.WithDescription("Return the raw unified diff of the OTHER changed files in a changeset — the sibling changes a per-symbol or per-file review view filters out — prebuilt in one call. Enumerates the whole changeset (via the git diff against `base`/`scope`), drops the focus files, and returns each remaining file's raw diff ranked by relatedness to the focus (shared community/process → co-change → directory proximity). Pass `focus_files` (comma-separated changed file paths to exclude) and/or `focus_symbol_id` (a changed symbol whose file is the focus). Use to pull in the cross-file context a narrow review needs without issuing a diff call per file."),
			mcp.WithString("base", mcp.Description("Base git ref (e.g. main). Selects the changeset as `git diff base...HEAD`. Alias for scope=compare + base_ref=base.")),
			mcp.WithString("base_ref", mcp.Description("Base ref for scope=compare (default: main). `base` takes precedence when both are set.")),
			mcp.WithString("scope", mcp.Description("Changeset scope: unstaged (default), staged, all, or compare. Ignored when `base` is set (forces compare).")),
			mcp.WithString("repo", mcp.Description("Repository prefix to resolve the working tree (multi-repo mode).")),
			mcp.WithString("focus_files", mcp.Description("Comma-separated changed file paths that are the focus — excluded from the returned siblings.")),
			mcp.WithString("focus_file", mcp.Description("Single focus file path — excluded from the siblings (alias for focus_files with one entry).")),
			mcp.WithString("focus_symbol_id", mcp.Description("A changed symbol's ID; its file becomes a focus file and is excluded from the siblings.")),
			mcp.WithString("format", mcp.Description("Output format: json (default), gcx (GCX1 compact wire format), or toon")),
			mcp.WithNumber("max_bytes", mcp.Description("Cap the marshaled response at this many bytes. The lowest-ranked siblings are trimmed first; truncation metadata rides on the response. Omit for no cap.")),
			mcp.WithNumber("max_tokens", mcp.Description("Token budget for the response — the lowest-ranked siblings are dropped first to fit.")),
		),
		s.handleSiblingDiffContext,
	)
}

// siblingDiffRow is one related-but-filtered-out changed file: its repo-relative
// path, the relation that ranks it against the focus, the relatedness score, and
// the raw unified diff text of just that file's hunks.
type siblingDiffRow struct {
	File     string  `json:"file"`
	Relation string  `json:"relation"`
	Score    float64 `json:"score"`
	Diff     string  `json:"diff"`
}

// handleSiblingDiffContext enumerates the changeset, drops the focus files, and
// returns each remaining changed file's raw diff ranked by relatedness to the
// focus. Relatedness is community/process sharing → co-change → directory
// proximity; budget trims the lowest-ranked rows first.
func (s *Server) handleSiblingDiffContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.graph == nil {
		return mcp.NewToolResultError("no graph available — index a repo first"), nil
	}

	scope, baseRef := siblingDiffScope(req)

	repo := strings.TrimSpace(req.GetString("repo", ""))
	roots := s.collectRepoRoots(repo)
	repoRoot := pickRepoRoot(roots, repo)
	if repoRoot == "" {
		if s.indexer != nil {
			if root := s.indexer.RootPath(); root != "" {
				repoRoot = root
			}
		}
	}
	if repoRoot == "" {
		return mcp.NewToolResultError("could not resolve a repository root for the changeset diff"), nil
	}

	// Enumerate the whole changeset.
	diff, err := analysis.MapGitDiff(s.graph, repoRoot, scope, baseRef)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Resolve the focus set: explicit focus_files / focus_file plus the file
	// of any focus_symbol_id.
	focus := s.resolveFocusFiles(req)
	focusList := make([]string, 0, len(focus))
	for f := range focus {
		focusList = append(focusList, f)
	}
	sort.Strings(focusList)

	// Build a deduplicated, focus-excluded sibling set out of the changed files.
	seen := map[string]bool{}
	var siblings []siblingDiffRow
	for _, f := range diff.ChangedFiles {
		f = filepath.Clean(f)
		if f == "" || f == "." || focus[f] || seen[f] {
			continue
		}
		seen[f] = true

		raw, derr := s.rawFileDiff(repoRoot, scope, baseRef, f)
		if derr != nil || strings.TrimSpace(raw) == "" {
			continue
		}
		relation, score := s.siblingRelation(f, focusList)
		siblings = append(siblings, siblingDiffRow{
			File:     f,
			Relation: relation,
			Score:    score,
			Diff:     raw,
		})
	}

	// Rank: highest relatedness first, ties broken by path for determinism.
	sort.SliceStable(siblings, func(i, j int) bool {
		if siblings[i].Score != siblings[j].Score {
			return siblings[i].Score > siblings[j].Score
		}
		return siblings[i].File < siblings[j].File
	})

	payload := siblingDiffPayload(focusList, siblings)

	if s.isGCX(ctx, req) {
		return s.gcxResponseWithBudget(req)(encodeSiblingDiffContext(payload))
	}
	if s.isTOON(ctx, req) {
		return returnTOON(payload)
	}
	return s.respondJSONOrTOON(ctx, req, payload)
}

// siblingDiffScope resolves the (scope, baseRef) pair from the request. `base`
// is a convenience alias that forces compare scope against that ref.
func siblingDiffScope(req mcp.CallToolRequest) (scope, baseRef string) {
	base := strings.TrimSpace(req.GetString("base", ""))
	if base != "" {
		return "compare", base
	}
	scope = req.GetString("scope", "unstaged")
	baseRef = req.GetString("base_ref", "main")
	return scope, baseRef
}

// resolveFocusFiles collects the focus file set from focus_files / focus_file
// (paths) and focus_symbol_id (the symbol's file). Paths are cleaned so they
// join the MapGitDiff ChangedFiles keys.
func (s *Server) resolveFocusFiles(req mcp.CallToolRequest) map[string]bool {
	focus := map[string]bool{}
	add := func(p string) {
		p = filepath.Clean(strings.TrimSpace(p))
		if p != "" && p != "." {
			focus[p] = true
		}
	}
	for _, p := range strings.Split(req.GetString("focus_files", ""), ",") {
		add(p)
	}
	if ff := req.GetString("focus_file", ""); ff != "" {
		add(ff)
	}
	if id := strings.TrimSpace(req.GetString("focus_symbol_id", "")); id != "" {
		if n := s.graph.GetNode(id); n != nil && n.FilePath != "" {
			add(n.FilePath)
		}
	}
	return focus
}

// siblingRelation classifies and scores how a candidate sibling file relates to
// the focus set. The strongest applicable relation wins:
//
//	community → the sibling shares a graph community with a focus symbol
//	process   → the sibling shares a process with a focus symbol
//	cochange  → the sibling historically changes alongside a focus file
//	directory → the sibling lives in (or near) a focus file's directory
//	none      → unrelated by any signal
//
// Score is a coarse band per relation (plus a co-change magnitude bump) so the
// ranking is deterministic and the bands stay separable.
func (s *Server) siblingRelation(file string, focusFiles []string) (string, float64) {
	if len(focusFiles) == 0 {
		return "none", 0
	}

	// community / process sharing, evaluated symbol-wise across both sides.
	communities := s.getCommunities()
	processes := s.getProcesses()
	focusCommunities := map[string]bool{}
	focusProcesses := map[string]bool{}
	for _, ff := range focusFiles {
		for _, n := range s.graph.GetFileNodes(ff) {
			if communities != nil {
				if c, ok := communities.NodeToComm[n.ID]; ok && c != "" {
					focusCommunities[c] = true
				}
			}
			if processes != nil {
				for _, p := range processes.NodeToProcs[n.ID] {
					if p != "" {
						focusProcesses[p] = true
					}
				}
			}
		}
	}
	if communities != nil && len(focusCommunities) > 0 {
		for _, n := range s.graph.GetFileNodes(file) {
			if c, ok := communities.NodeToComm[n.ID]; ok && focusCommunities[c] {
				return "community", 100
			}
		}
	}
	if processes != nil && len(focusProcesses) > 0 {
		for _, n := range s.graph.GetFileNodes(file) {
			for _, p := range processes.NodeToProcs[n.ID] {
				if focusProcesses[p] {
					return "process", 80
				}
			}
		}
	}

	// co-change: historical commit overlap between the sibling and a focus file.
	bestCo := 0.0
	for _, ff := range focusFiles {
		if sc, ok := s.coChangeScores(ff)[file]; ok && sc > bestCo {
			bestCo = sc
		}
	}
	if bestCo > 0 {
		// Keep co-change strictly below process so the bands don't collide.
		return "cochange", 40 + clampScore(bestCo, 39)
	}

	// directory proximity: same directory (or an ancestor of one) as a focus
	// file. The deeper the shared prefix the higher the score.
	bestDir := 0.0
	for _, ff := range focusFiles {
		if d := dirProximity(file, ff); d > bestDir {
			bestDir = d
		}
	}
	if bestDir > 0 {
		return "directory", bestDir
	}

	return "none", 0
}

// dirProximity scores directory closeness in (0,20]. Same directory scores
// highest; a shared parent directory scores by the number of shared leading
// path segments. Returns 0 when the files share no directory prefix.
func dirProximity(a, b string) float64 {
	da := strings.Split(filepath.ToSlash(filepath.Dir(a)), "/")
	db := strings.Split(filepath.ToSlash(filepath.Dir(b)), "/")
	shared := 0
	for shared < len(da) && shared < len(db) && da[shared] == db[shared] {
		if da[shared] == "." || da[shared] == "" {
			break
		}
		shared++
	}
	if shared == 0 {
		return 0
	}
	if len(da) == len(db) && shared == len(da) {
		// Exact same directory.
		return 20
	}
	score := float64(shared)
	if score > 19 {
		score = 19
	}
	return score
}

// clampScore caps v into [0, max].
func clampScore(v, max float64) float64 {
	if v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

// rawFileDiff returns the raw unified diff text (context-bearing) for a single
// changed file within the changeset. It runs the same git-diff selection as
// MapGitDiff narrowed to one pathspec, so the per-file diff joins the enumerated
// changeset exactly.
func (s *Server) rawFileDiff(repoRoot, scope, baseRef, file string) (string, error) {
	args := siblingDiffArgs(scope, baseRef)
	args = append(args, "--", file)
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		// An empty diff for a path is not an error (e.g. mode-only change).
		if len(out) == 0 {
			return "", nil
		}
		return "", fmt.Errorf("git diff for %s failed: %w", file, err)
	}
	return string(out), nil
}

// siblingDiffArgs mirrors the analysis diff-arg selection but emits a context
// window (unified=3) so the raw sibling diff carries readable surrounding lines.
func siblingDiffArgs(scope, baseRef string) []string {
	switch scope {
	case "staged":
		return []string{"diff", "--cached", "--unified=3"}
	case "all":
		return []string{"diff", "HEAD", "--unified=3"}
	case "compare":
		if baseRef == "" {
			baseRef = "main"
		}
		return []string{"diff", baseRef + "...HEAD", "--unified=3"}
	default: // unstaged
		return []string{"diff", "--unified=3"}
	}
}

// siblingDiffPayload projects the ranked siblings onto the wire shape.
// truncated is always false here — the byte/token budget applied downstream
// stamps its own truncation flag when it trims rows.
func siblingDiffPayload(focus []string, siblings []siblingDiffRow) map[string]any {
	if focus == nil {
		focus = []string{}
	}
	rows := make([]map[string]any, 0, len(siblings))
	for _, sib := range siblings {
		rows = append(rows, map[string]any{
			"file":     sib.File,
			"relation": sib.Relation,
			"score":    sib.Score,
			"diff":     sib.Diff,
		})
	}
	return map[string]any{
		"focus":     focus,
		"siblings":  rows,
		"total":     len(rows),
		"truncated": false,
	}
}
