package agents

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// PrintConfig writes a dry-run view of the configuration a single named adapter
// would install — the files it plans to touch, their action, and the keys it
// sets — as indented JSON, without writing anything to disk. It is the
// machine-readable companion to `init doctor`, scoped to one agent. Returns an
// error naming the registered agents when `name` matches none.
func PrintConfig(w io.Writer, reg *Registry, name string, env Env) error {
	var found Adapter
	var available []string
	for _, a := range reg.All() {
		available = append(available, a.Name())
		if a.Name() == name {
			found = a
		}
	}
	if found == nil {
		return fmt.Errorf("unknown agent %q; available: %s", name, strings.Join(available, ", "))
	}

	plan, err := found.Plan(env)
	if err != nil {
		return fmt.Errorf("plan %s: %w", found.Name(), err)
	}
	files := []FileAction{}
	if plan != nil && plan.Files != nil {
		files = plan.Files
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"agent":    found.Name(),
		"docs_url": found.DocsURL(),
		"files":    files,
	})
}
