package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSmartContext_BlastRadiusAlwaysPresent verifies the fused
// blast-radius block is emitted on every call (no entry_point given)
// and carries the caller/test sub-lists.
func TestSmartContext_BlastRadiusAlwaysPresent(t *testing.T) {
	srv, _ := setupCompressTestServer(t)
	m := extractTextResult(t, callTool(t, srv, "smart_context", map[string]any{
		"task": "validate token and parse claims",
	}))
	br, ok := m["blast_radius"].(map[string]any)
	require.True(t, ok, "blast_radius must be present on every smart_context call, keys: %v", keysOf(m))
	_, hasCallers := br["callers_by_file"]
	assert.True(t, hasCallers, "blast_radius must carry callers_by_file")
	_, hasTests := br["covering_tests"]
	assert.True(t, hasTests, "blast_radius must carry covering_tests")
}

// TestSmartContext_BlastRadiusNoTestsWarning verifies that when no
// working-set symbol has a covering test the block carries the warning.
func TestSmartContext_BlastRadiusNoTestsWarning(t *testing.T) {
	srv, _ := setupCompressTestServer(t)
	m := extractTextResult(t, callTool(t, srv, "smart_context", map[string]any{
		"task": "validate token and parse claims",
	}))
	br, _ := m["blast_radius"].(map[string]any)
	require.NotNil(t, br)
	// The single-file fixture ships no tests, so the warning must fire.
	assert.Equal(t, "no covering tests found", br["warning"],
		"a working set with no EdgeTests coverage must warn")
}

// TestSmartContext_BlastRadiusStable verifies the blast-radius block
// keeps the pack root byte-stable within a session.
func TestSmartContext_BlastRadiusStable(t *testing.T) {
	srv, _ := setupCompressTestServer(t)
	args := map[string]any{"task": "validate token and parse claims"}
	a := extractTextResult(t, callTool(t, srv, "smart_context", args))
	b := extractTextResult(t, callTool(t, srv, "smart_context", args))
	require.NotEmpty(t, a["etag"])
	assert.Equal(t, a["etag"], b["etag"],
		"the blast-radius block must not perturb within-session pack-root stability")
}
