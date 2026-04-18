package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PostTaskInput is the JSON structure Claude Code sends to Stop hooks.
// stop_hook_active is true when the hook is already being rerun (another
// Stop hook asked the agent to continue) — we must skip in that case to
// avoid recursion.
type PostTaskInput struct {
	HookEventName   string `json:"hook_event_name"`
	SessionID       string `json:"session_id"`
	TranscriptPath  string `json:"transcript_path"`
	CWD             string `json:"cwd"`
	StopHookActive  bool   `json:"stop_hook_active"`
}

// runPostTask handles a Stop hook invocation with the raw stdin bytes.
// It runs diagnostics on any changed symbols (dead-code, test targets,
// guards, contract violations) and injects the findings as
// additionalContext so the agent self-corrects before handing off.
//
// Degrades silently when: the bridge is unreachable, there are no
// changes, or stop_hook_active is true.
func runPostTask(data []byte, port int) {
	var input PostTaskInput
	if err := json.Unmarshal(data, &input); err != nil {
		return
	}
	if input.HookEventName != "Stop" {
		return
	}
	// Prevent recursion — if we're already rerunning a Stop hook, don't fire again.
	if input.StopHookActive {
		return
	}

	briefing := buildPostTaskBriefing(port)
	if briefing == "" {
		return
	}

	output := HookOutput{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName:     "Stop",
			AdditionalContext: briefing,
		},
	}
	out, err := json.Marshal(output)
	if err != nil {
		return
	}
	fmt.Print(string(out))
}

// buildPostTaskBriefing runs diagnostics on the current working tree and
// returns a compact markdown summary. Returns empty string when there's
// nothing to report or the bridge is unreachable.
func buildPostTaskBriefing(port int) string {
	raw := callServerTool(port, "detect_changes", map[string]any{
		"scope": "unstaged",
	})
	if raw == "" {
		return ""
	}

	var changes struct {
		ChangedFiles   []string `json:"changed_files"`
		ChangedSymbols []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"changed_symbols"`
		Risk    string `json:"risk"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(raw), &changes); err != nil {
		return ""
	}

	if len(changes.ChangedSymbols) == 0 {
		// No indexed symbols touched — skip silently. The agent doesn't need
		// a post-task briefing when nothing meaningful changed in the graph.
		return ""
	}

	ids := make([]string, len(changes.ChangedSymbols))
	for i, cs := range changes.ChangedSymbols {
		ids[i] = cs.ID
	}
	idsCSV := strings.Join(ids, ",")

	var sb strings.Builder
	sb.WriteString("## Gortex Post-Task Diagnostics\n\n")
	fmt.Fprintf(&sb, "**Changed:** %d symbols across %d files — risk `%s`.\n\n",
		len(changes.ChangedSymbols), len(changes.ChangedFiles), changes.Risk)

	// Test targets — what to run.
	if tests := renderTestTargets(port, idsCSV); tests != "" {
		sb.WriteString("### Tests to Run\n\n")
		sb.WriteString(tests)
		sb.WriteString("\n")
	}

	// Guard rule violations.
	if guards := renderGuardViolations(port, idsCSV); guards != "" {
		sb.WriteString("### Guard Violations\n\n")
		sb.WriteString(guards)
		sb.WriteString("\n")
	}

	// Dead code — specifically whether any of the changed symbols are now orphaned.
	if dead := renderDeadCodeHits(port, ids); dead != "" {
		sb.WriteString("### Potential Dead Code (among changed symbols)\n\n")
		sb.WriteString(dead)
		sb.WriteString("\n")
	}

	// Contract mismatches.
	if contracts := renderContractMismatches(port); contracts != "" {
		sb.WriteString("### API Contract Issues\n\n")
		sb.WriteString(contracts)
		sb.WriteString("\n")
	}

	sb.WriteString("_Run the tests above and review any flagged items before handoff._\n")
	return sb.String()
}

// renderTestTargets asks the bridge for test files that exercise the changed symbols.
func renderTestTargets(port int, idsCSV string) string {
	raw := callServerTool(port, "get_test_targets", map[string]any{
		"ids":     idsCSV,
		"compact": true,
	})
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "no test targets found" {
		return ""
	}
	return cappedLines(raw, 15)
}

// renderGuardViolations asks the bridge for .gortex.yaml guard rule violations.
func renderGuardViolations(port int, idsCSV string) string {
	raw := callServerTool(port, "check_guards", map[string]any{
		"ids":     idsCSV,
		"compact": true,
	})
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "no guard rule violations") {
		return ""
	}
	return cappedLines(raw, 10)
}

// renderDeadCodeHits filters analyze:dead_code results to the intersection
// with the currently-changed symbols (i.e. "did this task leave anything
// orphaned"). Emits nothing when the intersection is empty.
func renderDeadCodeHits(port int, ids []string) string {
	raw := callServerTool(port, "analyze", map[string]any{
		"kind":    "dead_code",
		"compact": true,
	})
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	var hits []string
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		// Each compact line starts with the symbol descriptor including the
		// file path — we substring-match any of the changed IDs.
		for id := range idSet {
			if strings.Contains(line, id) {
				hits = append(hits, line)
				break
			}
		}
	}
	if len(hits) == 0 {
		return ""
	}
	if len(hits) > 8 {
		hits = hits[:8]
	}
	return strings.Join(hits, "\n") + "\n"
}

// renderContractMismatches runs the contracts check and returns a short list
// of orphan providers/consumers. Empty when all contracts are matched.
func renderContractMismatches(port int) string {
	raw := callServerTool(port, "contracts", map[string]any{
		"action":  "check",
		"compact": true,
	})
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// The compact summary prefixes "no contract issues" when clean.
	if strings.HasPrefix(strings.ToLower(raw), "no contract") {
		return ""
	}
	return cappedLines(raw, 10)
}
