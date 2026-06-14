package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/graph"
)

// freshnessRiderFor returns a small structured freshness block for a
// file-reading tool whose target file has changed on disk since it was
// indexed — so an agent reading a just-edited file sees, inline, that the
// graph view may lag the working tree. It returns nil (no rider, zero
// extra tokens) for the overwhelmingly common fresh case, for non-file
// tools, and in multi-repo mode (where the legacy single-indexer's
// staleness signal is all-noise — per-repo watchers own freshness there).
//
// The check is O(1): one map lookup + one stat on the single file the
// tool targets, only for the handful of read/source tools.
func (s *Server) freshnessRiderFor(toolName string, req mcp.CallToolRequest) map[string]any {
	if s.indexer == nil || s.multiIndexer != nil {
		return nil
	}
	if os.Getenv("GORTEX_NO_FRESHNESS_RIDER") == "1" {
		return nil
	}
	rel := targetRepoRelFile(toolName, req, s.indexer.RepoPrefix())
	if rel == "" || !s.indexer.IsTrackedStale(rel) {
		return nil
	}
	out := map[string]any{
		"file":  rel,
		"stale": true,
		"hint":  "this file changed on disk since it was last indexed; the graph view may lag the working tree",
	}
	if r, ok := graph.Store(s.graph).(graph.RepoIndexStateReader); ok {
		if st, found, _ := r.GetRepoIndexState(s.indexer.RepoPrefix()); found {
			if st.IndexedSHA != "" {
				out["indexed_sha"] = shortFreshSHA(st.IndexedSHA)
			}
			if st.Dirty {
				out["working_tree_dirty_at_index"] = true
			}
		}
	}
	return out
}

// targetRepoRelFile extracts the repo-relative path of the single file a
// read tool targets, or "" when the tool is not file-scoped. A leading
// repo prefix is stripped so the result matches the indexer's mtime keys;
// a path that does not match is simply reported not-stale (IsTrackedStale
// returns false for an unknown key), so imperfect normalization is safe.
func targetRepoRelFile(toolName string, req mcp.CallToolRequest, prefix string) string {
	var raw string
	switch toolName {
	case "read_file", "get_file_summary", "get_editing_context":
		raw = req.GetString("path", "")
	case "get_symbol_source", "get_symbol":
		id := req.GetString("id", "")
		if i := strings.Index(id, "::"); i >= 0 {
			raw = id[:i]
		}
	default:
		return ""
	}
	if raw == "" {
		return ""
	}
	raw = filepath.ToSlash(raw)
	if prefix != "" {
		raw = strings.TrimPrefix(raw, prefix+"/")
	}
	return raw
}

// decorateResultWithFreshness attaches the freshness rider to a JSON-object
// tool response under the "freshness" key. Non-JSON-object payloads
// (GCX / TOON / arrays) are left untouched — a best-effort hint must never
// reshape a compact wire format the caller opted into.
func decorateResultWithFreshness(res *mcp.CallToolResult, rider map[string]any) *mcp.CallToolResult {
	if len(rider) == 0 {
		return res
	}
	text, ok := singleTextContent(res)
	if !ok || text == "" {
		return res
	}
	var asObj map[string]any
	if json.Unmarshal([]byte(text), &asObj) != nil {
		return res
	}
	if _, exists := asObj["freshness"]; exists {
		return res
	}
	asObj["freshness"] = rider
	body, err := json.Marshal(asObj)
	if err != nil {
		return res
	}
	return rebuildTextResult(res, string(body))
}

// shortFreshSHA trims a git SHA to 12 chars for the rider.
func shortFreshSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
