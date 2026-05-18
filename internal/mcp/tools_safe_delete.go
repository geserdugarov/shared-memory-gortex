package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/zzet/gortex/internal/graph"
)

// registerSafeDeleteSymbolTool wires safe_delete_symbol — atomic
// dead-code removal with a graph-aware safety gate. Before touching
// disk, the tool checks for referencing edges (calls, implements,
// extends, references); a non-zero count rejects the delete unless
// the caller passes force=true.
//
// Default is dry_run=true: returns the planned delete (line range +
// preview of the bytes that would disappear) without writing. The
// agent gets one round-trip to inspect, then flips dry_run=false to
// commit.
func (s *Server) registerSafeDeleteSymbolTool() {
	s.addTool(
		mcp.NewTool("safe_delete_symbol",
			mcp.WithDescription("Atomically delete a symbol from the file system, with a graph-aware safety gate. Computes referencing edges first (calls / implements / extends / references); if any exist, the delete is REJECTED unless force=true. Default dry_run=true returns the preview without writing — flip dry_run=false to commit. The deleted range covers the symbol body plus any leading doc-comment block. The graph is re-indexed on commit so subsequent queries see the new state."),
			mcp.WithString("id", mcp.Description("Symbol ID (e.g. pkg/foo.go::Bar).")),
			mcp.WithBoolean("dry_run", mcp.Description("When true (default), returns the planned delete without writing. Set false to commit.")),
			mcp.WithBoolean("force", mcp.Description("Bypass the referencing-edge check. Use when you've already removed every caller in the same change set. Default false.")),
			mcp.WithString("format", mcp.Description("Output format: json (default), gcx, or toon")),
		),
		s.handleSafeDeleteSymbol,
	)
}

// safeDeleteReference describes a single referencing edge — enough
// information for the caller to navigate to it and remove it before
// retrying the delete.
type safeDeleteReference struct {
	FromID   string `json:"from_id"`
	Kind     string `json:"kind"`
	FromName string `json:"from_name,omitempty"`
	FilePath string `json:"file_path,omitempty"`
}

func (s *Server) handleSafeDeleteSymbol(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError("id is required"), nil
	}
	dryRun := requestBoolDefault(req, "dry_run", true)
	force := req.GetBool("force", false)

	node := s.graph.GetNode(id)
	if node == nil {
		return mcp.NewToolResultError("symbol not found: " + id), nil
	}
	if node.StartLine == 0 || node.EndLine == 0 {
		return mcp.NewToolResultError("symbol has no line range: " + id), nil
	}

	// Safety check: referencing edges. We keep the four edge kinds
	// that signal actual code-level use; structural edges like
	// EdgeDefines / EdgeMemberOf are skipped (they don't represent
	// "someone calls this").
	refs := collectReferencingEdges(s.graph, id)
	if len(refs) > 0 && !force {
		return s.respondJSONOrTOON(ctx, req, map[string]any{
			"status":          "rejected_has_references",
			"symbol":          id,
			"file":            node.FilePath,
			"references":      refs,
			"reference_count": len(refs),
			"dry_run":         dryRun,
			"force":           force,
			"hint":            "remove every referencing edge first, or pass force=true to override",
		})
	}

	absPath, err := s.resolveNodePath(node)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("could not read file: %v", err)), nil
	}
	lines := strings.Split(string(content), "\n")
	if node.StartLine > len(lines) || node.EndLine > len(lines) {
		return mcp.NewToolResultError("symbol line range exceeds file length"), nil
	}

	// Expand range upward to consume any leading doc-comment block.
	// Comments immediately preceding a symbol logically belong to
	// it; leaving them orphaned would create lint noise on the next
	// pass. Blank lines between the doc block and the previous
	// symbol stay where they are — eating them tightens the file
	// in unexpected ways.
	deleteStart := node.StartLine
	for deleteStart > 1 {
		trimmed := strings.TrimSpace(lines[deleteStart-2])
		if isCommentLine(trimmed) {
			deleteStart--
			continue
		}
		break
	}
	deleteEnd := node.EndLine
	// Eat one trailing blank line to keep the resulting file tidy.
	if deleteEnd < len(lines) && strings.TrimSpace(lines[deleteEnd]) == "" {
		deleteEnd++
	}

	deletedChunk := strings.Join(lines[deleteStart-1:deleteEnd], "\n")
	linesDeleted := deleteEnd - deleteStart + 1

	result := map[string]any{
		"symbol":          id,
		"file":            node.FilePath,
		"start_line":      deleteStart,
		"end_line":        deleteEnd,
		"lines_deleted":   linesDeleted,
		"reference_count": len(refs),
		"references":      refs,
		"preview":         deletedChunk,
		"dry_run":         dryRun,
		"force":           force,
	}

	if dryRun {
		result["status"] = "preview"
		return s.respondJSONOrTOON(ctx, req, result)
	}

	// Commit. Reassemble the file without the deleted block, write
	// atomically through the same path edit_symbol uses, then record
	// the modification so session tracking sees it.
	remaining := append([]string{}, lines[:deleteStart-1]...)
	remaining = append(remaining, lines[deleteEnd:]...)
	newContent := strings.Join(remaining, "\n")
	if err := os.WriteFile(absPath, []byte(newContent), 0o644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("could not write file: %v", err)), nil
	}

	sess := s.sessionFor(ctx)
	sess.recordModified(node.FilePath)
	sess.recordSymbol(id)
	if s.symHistory != nil {
		s.symHistory.Record(id, true)
	}

	result["status"] = "deleted"
	return s.respondJSONOrTOON(ctx, req, result)
}

// collectReferencingEdges returns every in-edge to id whose kind
// represents real use (someone calls, implements, extends, or
// references this symbol). Structural edges (defines, member_of)
// are excluded because they don't block a delete.
func collectReferencingEdges(g *graph.Graph, id string) []safeDeleteReference {
	out := make([]safeDeleteReference, 0)
	seen := map[string]bool{}
	for _, e := range g.GetInEdges(id) {
		if !isReferencingEdgeKind(e.Kind) {
			continue
		}
		key := e.From + "|" + string(e.Kind)
		if seen[key] {
			continue
		}
		seen[key] = true
		row := safeDeleteReference{FromID: e.From, Kind: string(e.Kind)}
		if from := g.GetNode(e.From); from != nil {
			row.FromName = from.Name
			row.FilePath = from.FilePath
		}
		out = append(out, row)
	}
	return out
}

// isReferencingEdgeKind reports whether an in-edge of this kind
// counts as "real use" that should block a delete.
func isReferencingEdgeKind(k graph.EdgeKind) bool {
	switch k {
	case graph.EdgeCalls,
		graph.EdgeImplements,
		graph.EdgeExtends,
		graph.EdgeReferences,
		graph.EdgeInstantiates,
		graph.EdgeCrossRepoCalls,
		graph.EdgeCrossRepoImplements,
		graph.EdgeCrossRepoExtends:
		return true
	}
	return false
}

// isCommentLine recognises every block- and line-comment leader the
// extractors emit. Used by the doc-comment expansion above.
func isCommentLine(trimmed string) bool {
	switch {
	case strings.HasPrefix(trimmed, "//"),
		strings.HasPrefix(trimmed, "/*"),
		strings.HasPrefix(trimmed, "*"),
		strings.HasPrefix(trimmed, "#"),
		strings.HasPrefix(trimmed, "///"),
		strings.HasPrefix(trimmed, "--"):
		return true
	}
	return false
}
