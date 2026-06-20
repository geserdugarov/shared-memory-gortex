package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/persistence"
	"gopkg.in/yaml.v3"
)

// cannedToolProfileCountsJSON mirrors the full-mode tool_profile response shape
// the daemon returns: top-level preset / preset_mode / live_count /
// deferred_count fields (handleToolProfile).
const cannedToolProfileCountsJSON = `{
  "lazy_enabled": true,
  "preset": "core",
  "preset_mode": "defer",
  "total": 175,
  "live_count": 34,
  "deferred_count": 141,
  "live": ["search_symbols", "get_callers"],
  "deferred": ["edit_file", "rename_symbol"]
}`

// newToolsReceiptTestCmd resets receipt flag state and binds a buffer. It also
// redirects Part B's CLI-verb ledger to an isolated temp sidecar and clears the
// session-id seam, so a receipt build in a test never reads the developer's
// real ~/.gortex (and Part B is empty unless the test seeds it).
func newToolsReceiptTestCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	toolsIndex = "."
	toolsReceiptFormat = "yaml"
	toolsReceiptSince = time.Hour

	path := filepath.Join(t.TempDir(), "sidecar.sqlite")
	origPath := receiptSidecarPath
	origSession := receiptSessionID
	receiptSidecarPath = func() string { return path }
	receiptSessionID = func() string { return "" }
	t.Cleanup(func() {
		receiptSidecarPath = origPath
		receiptSessionID = origSession
	})

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{Use: "receipt", RunE: runToolsReceipt}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd, buf
}

// TestToolsReceipt_DaemonReachable_YAML asserts a daemon-reachable receipt
// reports skill_cli transport and the live / deferred counts from tool_profile.
func TestToolsReceipt_DaemonReachable_YAML(t *testing.T) {
	orig := toolsDaemonTool
	t.Cleanup(func() { toolsDaemonTool = orig })

	var gotTool string
	toolsDaemonTool = func(_ string, tool string, _ map[string]any) (json.RawMessage, error) {
		gotTool = tool
		return json.RawMessage(cannedToolProfileCountsJSON), nil
	}

	cmd, buf := newToolsReceiptTestCmd(t)
	require.NoError(t, runToolsReceipt(cmd, nil))
	require.Equal(t, "tool_profile", gotTool)

	var env receiptEnvelope
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &env))
	r := env.GortexContextBudget
	require.Equal(t, "skill_cli", r.Transport)
	require.Equal(t, "core", r.DaemonPreset)
	require.Equal(t, "defer", r.DaemonPresetMode)
	require.Equal(t, 34, r.AdvertisedTools)
	require.Equal(t, 0, r.RegisteredToolSchemas)
	require.Equal(t, 141, r.DeferredCapabilities.Count)
	require.Equal(t, "full_surface_not_mounted", r.Decision)
	// repo is the absolute path of the index.
	require.NotEmpty(t, r.Repo)

	// The top-level key is present in the raw YAML text.
	require.Contains(t, buf.String(), "gortex_context_budget:")
}

// TestToolsReceipt_DaemonReachable_JSON asserts the JSON rendering carries the
// same structure under the gortex_context_budget key.
func TestToolsReceipt_DaemonReachable_JSON(t *testing.T) {
	orig := toolsDaemonTool
	t.Cleanup(func() { toolsDaemonTool = orig })
	toolsDaemonTool = func(string, string, map[string]any) (json.RawMessage, error) {
		return json.RawMessage(cannedToolProfileCountsJSON), nil
	}

	cmd, buf := newToolsReceiptTestCmd(t)
	toolsReceiptFormat = "json"
	require.NoError(t, runToolsReceipt(cmd, nil))

	var env receiptEnvelope
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	r := env.GortexContextBudget
	require.Equal(t, "skill_cli", r.Transport)
	require.Equal(t, "core", r.DaemonPreset)
	require.Equal(t, "defer", r.DaemonPresetMode)
	require.Equal(t, 34, r.AdvertisedTools)
	require.Equal(t, 0, r.RegisteredToolSchemas)
	require.Equal(t, 141, r.DeferredCapabilities.Count)
	require.Equal(t, "full_surface_not_mounted", r.Decision)
}

// TestToolsReceipt_NoDaemon asserts that when the daemon call fails, the receipt
// degrades to cli_only / no_surface_mounted without a non-zero error exit.
func TestToolsReceipt_NoDaemon(t *testing.T) {
	orig := toolsDaemonTool
	t.Cleanup(func() { toolsDaemonTool = orig })
	toolsDaemonTool = func(string, string, map[string]any) (json.RawMessage, error) {
		// The shape of the no-daemon signal the real path returns.
		return nil, errors.New("no gortex daemon is running — start it with `gortex daemon start --detach`")
	}

	cmd, buf := newToolsReceiptTestCmd(t)
	require.NoError(t, runToolsReceipt(cmd, nil))

	var env receiptEnvelope
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &env))
	r := env.GortexContextBudget
	require.Equal(t, "cli_only", r.Transport)
	require.Equal(t, 0, r.AdvertisedTools)
	require.Equal(t, 0, r.RegisteredToolSchemas)
	require.Equal(t, 0, r.DeferredCapabilities.Count)
	require.Equal(t, "no_surface_mounted", r.Decision)
	// daemon preset / mode are omitted when nothing is mounted.
	require.Empty(t, r.DaemonPreset)
	require.Empty(t, r.DaemonPresetMode)
}

// TestToolsReceipt_CountFallback asserts a profile that omits the *_count
// fields still yields faithful counts from the live / deferred arrays.
func TestToolsReceipt_CountFallback(t *testing.T) {
	orig := toolsDaemonTool
	t.Cleanup(func() { toolsDaemonTool = orig })
	toolsDaemonTool = func(string, string, map[string]any) (json.RawMessage, error) {
		// No live_count / deferred_count — only the arrays.
		return json.RawMessage(`{"preset":"nav","preset_mode":"hide","live":["a","b","c"],"deferred":["d"]}`), nil
	}

	cmd, buf := newToolsReceiptTestCmd(t)
	require.NoError(t, runToolsReceipt(cmd, nil))

	var env receiptEnvelope
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &env))
	r := env.GortexContextBudget
	require.Equal(t, "skill_cli", r.Transport)
	require.Equal(t, 3, r.AdvertisedTools)
	require.Equal(t, 1, r.DeferredCapabilities.Count)
}

// TestToolsReceipt_BadFormat asserts an unknown --format value is a clean error.
func TestToolsReceipt_BadFormat(t *testing.T) {
	orig := toolsDaemonTool
	t.Cleanup(func() { toolsDaemonTool = orig })
	toolsDaemonTool = func(string, string, map[string]any) (json.RawMessage, error) {
		return json.RawMessage(cannedToolProfileCountsJSON), nil
	}

	cmd, _ := newToolsReceiptTestCmd(t)
	toolsReceiptFormat = "toml"
	err := runToolsReceipt(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown --format")
}

// stubDaemonReachable points toolsDaemonTool at the canned tool_profile so a
// Part B test gets a normal Part A without a live daemon.
func stubDaemonReachable(t *testing.T) {
	t.Helper()
	orig := toolsDaemonTool
	t.Cleanup(func() { toolsDaemonTool = orig })
	toolsDaemonTool = func(string, string, map[string]any) (json.RawMessage, error) {
		return json.RawMessage(cannedToolProfileCountsJSON), nil
	}
}

// seedCLIEvents writes verbs for a session id into the receipt's redirected
// sidecar, so Part B has something to read back.
func seedCLIEvents(t *testing.T, sessionID string, verbs ...string) {
	t.Helper()
	sc, err := persistence.OpenSidecar(receiptSidecarPath())
	require.NoError(t, err)
	defer sc.Close()
	base := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	for i, v := range verbs {
		require.NoError(t, sc.AddCLIEvent(base.Add(time.Duration(i)*time.Minute), sessionID, v))
	}
}

// TestToolsReceipt_PartB_BySession: with GORTEX_SESSION_ID set, the receipt
// reports that session's distinct verbs and the safety steps derived from them,
// while Part A is unchanged.
func TestToolsReceipt_PartB_BySession(t *testing.T) {
	stubDaemonReachable(t)
	cmd, buf := newToolsReceiptTestCmd(t)
	// Scope to a session and seed verbs (with a duplicate to prove dedup, and a
	// non-safety verb to prove it contributes no safety step).
	receiptSessionID = func() string { return "agent-sess" }
	seedCLIEvents(t, "agent-sess", "edit.verify", "edit.guards", "edit.verify", "query.stats")

	require.NoError(t, runToolsReceipt(cmd, nil))

	var env receiptEnvelope
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &env))
	r := env.GortexContextBudget

	// Part A unchanged.
	require.Equal(t, "skill_cli", r.Transport)
	require.Equal(t, 34, r.AdvertisedTools)

	// Part B: distinct verbs in first-seen order; safety steps sorted + deduped.
	require.Equal(t, []string{"edit.verify", "edit.guards", "query.stats"}, r.CLIVerbsUsed)
	require.Equal(t, []string{"check_guards", "verify_change"}, r.SafetyStepsRun)
	// With a session id set there is no "set GORTEX_SESSION_ID" note.
	require.Empty(t, r.Note)
}

// TestToolsReceipt_PartB_NoSession: with no session id, Part B falls back to the
// --since window and carries the note. Events outside the window are excluded.
func TestToolsReceipt_PartB_NoSession(t *testing.T) {
	stubDaemonReachable(t)
	cmd, buf := newToolsReceiptTestCmd(t)
	// No session id (helper already cleared it). Seed under any id — the Since
	// read ignores the session dimension. Pin the clock so the seeded events
	// (base 2026-06-01 09:00) fall inside the window.
	origNow := receiptNow
	t.Cleanup(func() { receiptNow = origNow })
	receiptNow = func() time.Time { return time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC) }
	toolsReceiptSince = time.Hour
	seedCLIEvents(t, "whoever", "edit.tests", "edit.contract")

	require.NoError(t, runToolsReceipt(cmd, nil))

	var env receiptEnvelope
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &env))
	r := env.GortexContextBudget
	require.Equal(t, []string{"edit.tests", "edit.contract"}, r.CLIVerbsUsed)
	require.Equal(t, []string{"change_contract", "get_test_targets"}, r.SafetyStepsRun)
	require.Contains(t, r.Note, "GORTEX_SESSION_ID")
}

// TestToolsReceipt_PartB_EmptyWhenNoEvents: with nothing recorded the two
// fields are present as empty lists (never nil/omitted), and Part A still
// renders. The redirected sidecar is created on read but holds no rows.
func TestToolsReceipt_PartB_EmptyWhenNoEvents(t *testing.T) {
	stubDaemonReachable(t)
	cmd, buf := newToolsReceiptTestCmd(t)

	require.NoError(t, runToolsReceipt(cmd, nil))

	var env receiptEnvelope
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &env))
	r := env.GortexContextBudget
	require.NotNil(t, r.CLIVerbsUsed)
	require.NotNil(t, r.SafetyStepsRun)
	require.Empty(t, r.CLIVerbsUsed)
	require.Empty(t, r.SafetyStepsRun)
	// The raw YAML carries both keys explicitly.
	require.Contains(t, buf.String(), "cli_verbs_used:")
	require.Contains(t, buf.String(), "safety_steps_run:")
}

// TestSafetyStepsForVerbs unit-tests the verb→safety-tool map: it dedups
// (two verbs that map to feedback yield one), preserves the documented
// mappings, and a non-safety verb contributes nothing.
func TestSafetyStepsForVerbs(t *testing.T) {
	require.Equal(t,
		[]string{"change_contract", "check_guards", "get_test_targets", "verify_change"},
		safetyStepsForVerbs([]string{"edit.verify", "edit.guards", "edit.tests", "edit.contract"}),
	)
	// feedback.record + feedback.query both map to the same tool — dedup.
	require.Equal(t, []string{"feedback"}, safetyStepsForVerbs([]string{"feedback.record", "feedback.query"}))
	// A non-safety verb maps to nothing.
	require.Empty(t, safetyStepsForVerbs([]string{"query.stats", "daemon.start"}))
	// Empty input is empty output.
	require.Empty(t, safetyStepsForVerbs(nil))
}
