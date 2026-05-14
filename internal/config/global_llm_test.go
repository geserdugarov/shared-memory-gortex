package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/llm"
)

func TestLoadGlobal_LLMSectionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`active_project: ""
repos: []
llm:
    model: /opt/models/qwen.gguf
    template: chatml
    ctx: 4096
    max_steps: 12
    gpu_layers: 999
`), 0o644))

	gc, err := LoadGlobal(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, gc)
	assert.Equal(t, "/opt/models/qwen.gguf", gc.LLM.Model)
	assert.Equal(t, "chatml", gc.LLM.Template)
	assert.Equal(t, 4096, gc.LLM.Ctx)
	assert.Equal(t, 12, gc.LLM.MaxSteps)
	assert.Equal(t, 999, gc.LLM.GPULayers)
}

func TestGlobalConfig_MergeLLMInto_FillsZeroFields(t *testing.T) {
	gc := &GlobalConfig{LLM: llm.Config{
		Model:     "/global/qwen.gguf",
		Template:  "chatml",
		Ctx:       4096,
		MaxSteps:  16,
		GPULayers: 999,
	}}

	got := gc.MergeLLMInto(llm.Config{})
	assert.Equal(t, "/global/qwen.gguf", got.Model)
	assert.Equal(t, "chatml", got.Template)
	assert.Equal(t, 4096, got.Ctx)
	assert.Equal(t, 16, got.MaxSteps)
	assert.Equal(t, 999, got.GPULayers)
}

func TestGlobalConfig_MergeLLMInto_LocalWinsPerField(t *testing.T) {
	gc := &GlobalConfig{LLM: llm.Config{
		Model:    "/global/qwen.gguf",
		Template: "chatml",
		Ctx:      4096,
		MaxSteps: 16,
	}}

	got := gc.MergeLLMInto(llm.Config{
		Model: "/repo/override.gguf", // local wins
		Ctx:   8192,                  // local wins
	})
	assert.Equal(t, "/repo/override.gguf", got.Model)
	assert.Equal(t, 8192, got.Ctx)
	// Unset locals fall through to global.
	assert.Equal(t, "chatml", got.Template)
	assert.Equal(t, 16, got.MaxSteps)
}

func TestGlobalConfig_MergeLLMInto_NilReceiver(t *testing.T) {
	var gc *GlobalConfig // nil
	local := llm.Config{Model: "/repo/x.gguf"}
	got := gc.MergeLLMInto(local)
	assert.Equal(t, "/repo/x.gguf", got.Model)
}

func TestGlobalConfig_MergeLLMInto_ExpandsHomeInModelPath(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	gc := &GlobalConfig{LLM: llm.Config{Model: "~/models/qwen.gguf"}}
	got := gc.MergeLLMInto(llm.Config{})
	assert.Equal(t, filepath.Join(home, "models/qwen.gguf"), got.Model)

	// Local override also gets expanded.
	got = gc.MergeLLMInto(llm.Config{Model: "~/repo-override.gguf"})
	assert.Equal(t, filepath.Join(home, "repo-override.gguf"), got.Model)
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"/abs/path", "/abs/path"},
		{"relative/path", "relative/path"},
		{"~", home},
		{"~/models/foo.gguf", filepath.Join(home, "models/foo.gguf")},
		{"~weird", "~weird"}, // only `~/` form is expanded
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, expandHome(tc.in), "in=%q", tc.in)
	}
}
