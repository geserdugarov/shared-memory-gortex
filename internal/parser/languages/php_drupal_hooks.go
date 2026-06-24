package languages

import (
	"path"
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Drupal hook detection. A Drupal module implements a hook by defining a
// function named `{module}_{hook_suffix}` (or by a `@Implements hook_X`
// docblock). The hook itself is a contract, not a declared symbol, so an
// implementation has nothing to point at. This pass flags hook
// implementations and wires each to a synthetic `hook_X` node via
// EdgeImplements, so `find_implementations hook_X` lists every module that
// implements it across `.module` / `.inc` files.

// drupalModuleExts are the Drupal PHP file extensions whose function names
// follow the `{module}_{hook}` convention.
var drupalModuleExts = map[string]bool{
	".module": true, ".install": true, ".inc": true,
	".theme": true, ".profile": true, ".engine": true,
}

// drupalImplementsRE extracts the hook name from an `Implements hook_X`
// docblock line (the authoritative signal).
var drupalImplementsRE = regexp.MustCompile(`(?i)Implements\s+(hook_\w+)`)

// drupalKnownHookSuffixes is the hook-name set (without the `hook_` prefix)
// the name-pattern detector recognises when there is no `@Implements`
// docblock. Curated to the common, distinctive Drupal hooks so an ordinary
// `{module}_helper()` function is not mistaken for a hook.
var drupalKnownHookSuffixes = map[string]bool{
	"node_insert": true, "node_update": true, "node_delete": true,
	"node_load": true, "node_view": true, "node_presave": true, "node_access": true,
	"entity_insert": true, "entity_update": true, "entity_delete": true,
	"entity_presave": true, "entity_load": true, "entity_view": true,
	"user_login": true, "user_logout": true, "user_insert": true, "user_delete": true,
	"form_alter": true, "menu": true, "menu_alter": true, "theme": true,
	"permission": true, "cron": true, "init": true, "boot": true,
	"install": true, "uninstall": true, "schema": true, "requirements": true,
	"help": true, "mail": true, "mail_alter": true, "views_data": true,
	"preprocess_page": true, "preprocess_node": true, "library_info_alter": true,
	"token_info": true, "tokens": true, "page_attachments": true,
}

// captureDrupalHooks flags hook-implementation functions and wires them to a
// synthetic hook node. Runs at the tail of Extract.
func captureDrupalHooks(result *parser.ExtractionResult, filePath string) {
	if result == nil {
		return
	}
	ext := strings.ToLower(path.Ext(filePath))
	moduleFile := drupalModuleExts[ext]
	module := ""
	if moduleFile {
		base := path.Base(filePath)
		module = strings.TrimSuffix(base, path.Ext(base))
	}

	hookNodes := map[string]bool{}
	var add []*graph.Node
	for _, n := range result.Nodes {
		if n == nil || n.Kind != graph.KindFunction {
			continue
		}
		hook := drupalHookFor(n, module, moduleFile)
		if hook == "" {
			continue
		}
		if n.Meta == nil {
			n.Meta = map[string]any{}
		}
		n.Meta["drupal_hook"] = hook
		hookID := "drupal::hook::" + hook
		if !hookNodes[hook] {
			hookNodes[hook] = true
			add = append(add, &graph.Node{
				ID: hookID, Kind: graph.KindInterface, Name: hook,
				FilePath: filePath, StartLine: n.StartLine,
				Meta: map[string]any{"drupal_hook_def": true},
			})
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: n.ID, To: hookID, Kind: graph.EdgeImplements,
			FilePath: filePath, Line: n.StartLine,
			Meta: map[string]any{"drupal_hook": hook},
		})
	}
	result.Nodes = append(result.Nodes, add...)
}

// drupalHookFor returns the hook a function implements: from an `@Implements`
// docblock first, then the `{module}_{known_hook}` name pattern.
func drupalHookFor(n *graph.Node, module string, moduleFile bool) string {
	if n.Meta != nil {
		if doc, _ := n.Meta["doc"].(string); doc != "" {
			if m := drupalImplementsRE.FindStringSubmatch(doc); m != nil {
				return strings.ToLower(m[1])
			}
		}
	}
	if moduleFile && module != "" && strings.HasPrefix(n.Name, module+"_") {
		suffix := n.Name[len(module)+1:]
		if drupalKnownHookSuffixes[suffix] {
			return "hook_" + suffix
		}
	}
	return ""
}
