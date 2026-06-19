package agents

import (
	"bytes"
	"encoding/json"
	"testing"
)

// planStubAdapter is a stub adapter whose Plan returns a known file set so the
// PrintConfig output can be asserted.
type planStubAdapter struct{ name string }

func (p planStubAdapter) Name() string    { return p.name }
func (p planStubAdapter) DocsURL() string { return "https://example.test/" + p.name }
func (p planStubAdapter) Detect(Env) (bool, error) {
	return true, nil
}
func (p planStubAdapter) Plan(Env) (*Plan, error) {
	return &Plan{Files: []FileAction{
		{Path: ".cursor/mcp.json", Action: ActionMerge, Keys: []string{"mcpServers.gortex"}},
	}}, nil
}
func (p planStubAdapter) Apply(Env, ApplyOpts) (*Result, error) {
	return &Result{Name: p.name}, nil
}

// TestPrintConfig proves the --print-config core: a known agent's planned files
// are rendered as JSON, and an unknown agent errors with the available list.
func TestPrintConfig(t *testing.T) {
	reg := NewRegistry()
	reg.Register(planStubAdapter{name: "cursor"})

	var buf bytes.Buffer
	if err := PrintConfig(&buf, reg, "cursor", Env{}); err != nil {
		t.Fatalf("PrintConfig(cursor): %v", err)
	}
	var out struct {
		Agent   string       `json:"agent"`
		DocsURL string       `json:"docs_url"`
		Files   []FileAction `json:"files"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if out.Agent != "cursor" {
		t.Errorf("agent = %q, want cursor", out.Agent)
	}
	if len(out.Files) != 1 || out.Files[0].Path != ".cursor/mcp.json" {
		t.Errorf("files = %+v, want the planned mcp.json", out.Files)
	}

	// An unknown agent errors and names the registered agents.
	err := PrintConfig(&bytes.Buffer{}, reg, "nonexistent", Env{})
	if err == nil {
		t.Fatal("an unknown agent must error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("unknown agent")) ||
		!bytes.Contains([]byte(err.Error()), []byte("cursor")) {
		t.Errorf("error should name the unknown agent and the available list; got %v", err)
	}
}
