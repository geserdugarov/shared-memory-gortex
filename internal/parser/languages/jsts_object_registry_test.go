package languages

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func objectRegistryPlaceholders(fix *extractedFixture) map[string]string {
	out := map[string]string{} // registry_value -> registry_method
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if v, _ := e.Meta["via"].(string); v != "object-registry" {
			continue
		}
		rv, _ := e.Meta["registry_value"].(string)
		rm, _ := e.Meta["registry_method"].(string)
		out[rv] = rm
	}
	return out
}

func TestObjectRegistry_DispatchPlaceholders(t *testing.T) {
	src := `class AddCommand { execute() {} }
class RmCommand { execute() {} }
class Bus {
  constructor() {
    this.commands = { [Cmd.ADD]: AddCommand, [Cmd.RM]: RmCommand };
  }
  run(cmd) {
    new this.commands[cmd]().execute();
  }
}
`
	fix := runJSExtractFixture(t, "bus.js", src)

	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if v, _ := e.Meta["via"].(string); v == "object-registry" && e.From != "bus.js::Bus.run" {
			t.Errorf("dispatcher From = %q (want bus.js::Bus.run)", e.From)
		}
	}
	got := objectRegistryPlaceholders(fix)
	if got["AddCommand"] != "execute" || got["RmCommand"] != "execute" {
		t.Fatalf("registry placeholders = %v (want AddCommand/RmCommand → execute)", got)
	}
}

func TestObjectRegistry_MinifiedSkipped(t *testing.T) {
	// A single very long line reads as a minified bundle — no registry.
	src := strings.Repeat("x", 2500) + ";const commands={a:AddCommand};function d(k){new commands[k]().execute();}"
	fix := runJSExtractFixture(t, "min.js", src)
	if got := objectRegistryPlaceholders(fix); len(got) != 0 {
		t.Errorf("minified bundle must produce no registry edges, got %v", got)
	}
}

func TestObjectRegistry_PlainObjectNotRegistry(t *testing.T) {
	// An object with no computed-member dispatch site is not a registry.
	src := `const config = { add: AddCommand };
function f() { return config.add; }
`
	fix := runJSExtractFixture(t, "c.js", src)
	if got := objectRegistryPlaceholders(fix); len(got) != 0 {
		t.Errorf("object with no dispatch site must produce no registry edges, got %v", got)
	}
}

func TestTSObjectRegistry_VarBinding(t *testing.T) {
	src := `class AddCmd { execute() {} }
const registry = { add: AddCmd };
function dispatch(k: string) { new registry[k]().execute(); }
`
	fix := runTSExtractFixture(t, "r.ts", src)
	got := objectRegistryPlaceholders(fix)
	if got["AddCmd"] != "execute" {
		t.Errorf("registry placeholders = %v (want AddCmd → execute)", got)
	}
}
