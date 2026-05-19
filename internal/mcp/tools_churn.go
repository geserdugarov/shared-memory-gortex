package mcp

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/zzet/gortex/internal/blame"
	"github.com/zzet/gortex/internal/graph"
)

// registerChurnRateTool wires get_churn_rate — a standalone MCP tool
// that exposes per-symbol git-commit density. The metric is already
// implicit in `analyze hotspots` (composite); this tool surfaces the
// raw number so refactor planning, code review, and bus-factor work
// can read it directly.
//
// Computation: walk the scoped subgraph for function/method nodes,
// group by file_path, run `git blame -p` once per unique file, count
// distinct commits whose blame range intersects the symbol's line
// range. Bounded by file count, not symbol count.
func (s *Server) registerChurnRateTool() {
	s.addTool(
		mcp.NewTool("get_churn_rate",
			mcp.WithDescription("Per-symbol git-commit density. For each function/method in scope, runs `git blame -p` once per unique file and counts distinct commits intersecting the symbol's line range. Returns {symbol_id, name, file, churn_rate (commits per active day), commit_count, age_days, last_author, last_commit_at}. Sort and filter by churn_rate or commit_count to find unstable abstractions, hidden coupling, and bus-factor risks. Pairs with `analyze hotspots` — that returns the composite; this returns the raw signal."),
			mcp.WithString("path_prefix", mcp.Description("Scope analysis to nodes under this file-path prefix.")),
			mcp.WithNumber("min_commits", mcp.Description("Only return symbols with at least this many commits (default: 1).")),
			mcp.WithString("kinds", mcp.Description("Comma-separated kinds (default: function,method). Pass 'all' for every symbol.")),
			mcp.WithNumber("limit", mcp.Description("Cap the result set (default: 100).")),
			mcp.WithString("sort_by", mcp.Description("Sort key: churn_rate (default), commit_count, age_days.")),
			mcp.WithString("format", mcp.Description("Output format: json (default), gcx, or toon")),
		),
		s.handleGetChurnRate,
	)
}

type churnRow struct {
	ID           string  `json:"symbol_id"`
	Name         string  `json:"name"`
	File         string  `json:"file"`
	StartLine    int     `json:"start_line"`
	EndLine      int     `json:"end_line"`
	CommitCount  int     `json:"commit_count"`
	AgeDays      int     `json:"age_days"`
	ChurnRate    float64 `json:"churn_rate"`
	LastAuthor   string  `json:"last_author,omitempty"`
	LastCommitAt string  `json:"last_commit_at,omitempty"`
}

func (s *Server) handleGetChurnRate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pathPrefix := strings.TrimSpace(req.GetString("path_prefix", ""))
	minCommits := max(req.GetInt("min_commits", 1), 0)
	limit := max(req.GetInt("limit", 100), 1)
	sortBy := strings.TrimSpace(req.GetString("sort_by", "churn_rate"))

	allowed := map[graph.NodeKind]struct{}{
		graph.KindFunction: {},
		graph.KindMethod:   {},
	}
	if k := strings.TrimSpace(req.GetString("kinds", "")); k != "" && k != "all" {
		allowed = parseAnalyzeKindsFilter(k)
	} else if k == "all" {
		allowed = nil
	}

	// Resolve the repo root once so blame.Run can be called with a
	// fixed cwd. In multi-repo mode each file lives under one of the
	// MultiIndexer repos; we resolve per-file with resolveFilePath.
	scoped := s.scopedNodes(ctx)
	byFile := map[string][]*graph.Node{}
	for _, n := range scoped {
		if allowed != nil {
			if _, ok := allowed[n.Kind]; !ok {
				continue
			}
		}
		if pathPrefix != "" && !strings.HasPrefix(n.FilePath, pathPrefix) {
			continue
		}
		if n.StartLine == 0 {
			continue
		}
		byFile[n.FilePath] = append(byFile[n.FilePath], n)
	}

	rows := make([]churnRow, 0, len(scoped))
	scannedFiles := 0
	for filePath, nodes := range byFile {
		abs, _, err := s.resolveFilePath(filePath)
		if err != nil {
			continue
		}
		workTree := repoRootContaining(abs)
		if workTree == "" {
			continue
		}
		// Convert absolute path back to a path relative to the git
		// work tree — git blame takes tree-relative paths.
		gitRel := abs
		if rel, err := stripPathPrefix(abs, workTree+"/"); err == nil {
			gitRel = rel
		}
		lines, err := blame.Run(workTree, gitRel)
		if err != nil || len(lines) == 0 {
			continue
		}
		scannedFiles++

		for _, n := range nodes {
			endLine := n.EndLine
			if endLine == 0 {
				endLine = n.StartLine
			}
			commits := map[string]bool{}
			oldest, newest := time.Time{}, time.Time{}
			latestEmail := ""
			for line := n.StartLine; line <= endLine; line++ {
				a, ok := lines[line]
				if !ok {
					continue
				}
				if !commits[a.Commit] {
					commits[a.Commit] = true
				}
				if oldest.IsZero() || a.Timestamp.Before(oldest) {
					oldest = a.Timestamp
				}
				if newest.IsZero() || a.Timestamp.After(newest) {
					newest = a.Timestamp
					latestEmail = a.Email
				}
			}
			if len(commits) == 0 || len(commits) < minCommits {
				continue
			}
			ageDays := 0
			if !oldest.IsZero() {
				ageDays = int(time.Since(oldest).Hours() / 24)
			}
			// Churn rate: commits per active day. A symbol active for
			// 1 day with 3 commits gets churn_rate=3.0; one active for
			// 100 days with the same 3 commits gets 0.03. The minimum
			// denominator of 1 day stops a fresh symbol from looking
			// infinitely churny.
			activeDays := ageDays
			if activeDays < 1 {
				activeDays = 1
			}
			row := churnRow{
				ID: n.ID, Name: n.Name, File: n.FilePath,
				StartLine: n.StartLine, EndLine: endLine,
				CommitCount: len(commits),
				AgeDays:     ageDays,
				ChurnRate:   roundScore(float64(len(commits)) / float64(activeDays)),
				LastAuthor:  latestEmail,
			}
			if !newest.IsZero() {
				row.LastCommitAt = newest.UTC().Format(time.RFC3339)
			}
			rows = append(rows, row)
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		switch sortBy {
		case "commit_count":
			if rows[i].CommitCount != rows[j].CommitCount {
				return rows[i].CommitCount > rows[j].CommitCount
			}
		case "age_days":
			if rows[i].AgeDays != rows[j].AgeDays {
				return rows[i].AgeDays > rows[j].AgeDays
			}
		default: // churn_rate
			if rows[i].ChurnRate != rows[j].ChurnRate {
				return rows[i].ChurnRate > rows[j].ChurnRate
			}
		}
		return rows[i].ID < rows[j].ID
	})
	truncated := false
	if len(rows) > limit {
		rows = rows[:limit]
		truncated = true
	}

	return s.respondJSONOrTOON(ctx, req, map[string]any{
		"symbols":        rows,
		"total":          len(rows),
		"truncated":      truncated,
		"scanned_files":  scannedFiles,
		"sort_by":        sortBy,
		"min_commits":    minCommits,
	})
}

// stripPathPrefix returns path with prefix stripped iff path begins
// with prefix. Used to convert absolute paths back to git-tree-relative.
func stripPathPrefix(path, prefix string) (string, error) {
	if strings.HasPrefix(path, prefix) {
		return path[len(prefix):], nil
	}
	if path == strings.TrimSuffix(prefix, "/") {
		return "", nil
	}
	return path, errPathUnresolved
}
