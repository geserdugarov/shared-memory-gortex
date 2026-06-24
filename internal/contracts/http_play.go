package contracts

import (
	"fmt"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Play Framework conf/routes extraction. A Play app declares its HTTP routes
// in an extensionless `conf/routes` file (and per-module `*.routes` includes),
// one route per line:
//
//	GET     /users            controllers.UserController.list()
//	POST    /users/:id        controllers.UserController.update(id: Long)
//
// This pass parses each verb line into a route Contract and stamps the
// controller class + method so the module-wide cross-file pass binds it to the
// Scala/Java handler. `#` comments, `->` sub-router includes, and `+modifier`
// lines emit no routes (matching Play's own routes grammar).

// playRouteVerbs is the set of HTTP verbs a Play route line may start with.
var playRouteVerbs = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true,
	"PATCH": true, "HEAD": true, "OPTIONS": true,
}

// extractPlayRoutes parses a Play conf/routes file into route Contracts.
func (h *HTTPExtractor) extractPlayRoutes(filePath, text string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) []Contract {
	var out []Contract
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		// Comments, sub-router includes, and route modifiers are not routes.
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "->") || strings.HasPrefix(line, "+") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		verb := strings.ToUpper(fields[0])
		if !playRouteVerbs[verb] {
			continue
		}
		path := fields[1]
		if !strings.HasPrefix(path, "/") {
			continue
		}
		fqcn, hclass, hident := playHandlerRef(fields[2])
		if hident == "" {
			continue
		}
		normPath, origNames := NormalizeHTTPPathWithParams(path)
		c := Contract{
			ID:         fmt.Sprintf("http::%s::%s", verb, normPath),
			Type:       ContractHTTP,
			Role:       RoleProvider,
			FilePath:   filePath,
			Line:       i + 1,
			Confidence: 0.85,
			Meta: map[string]any{
				"method":    verb,
				"path":      normPath,
				"framework": "play",
			},
		}
		if len(origNames) > 0 {
			c.Meta["path_param_names"] = origNames
		}
		if fqcn != "" {
			c.Meta["handler_fqcn"] = fqcn
		}
		if hclass != "" {
			c.Meta["handler_class"] = hclass
		}
		c.Meta["handler_ident"] = hident
		if id := findMethodByNameAndReceiver(fileNodes, hident, hclass); id != "" {
			c.SymbolID = id
		}
		EnrichHTTPContractWithTree(&c, lines, fileNodes, lang, tree)
		out = append(out, c)
	}
	return out
}

// playHandlerRef parses a Play route handler reference
// `controllers.UserController.list(id: Long)` into its qualified class
// (`controllers.UserController`), simple class (`UserController`), and method
// (`list`). A leading `@` (dependency-injected routing) is tolerated. Returns
// empty strings when the reference has no `Class.method` shape.
func playHandlerRef(ref string) (fqcn, hclass, hident string) {
	ref = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ref), "@"))
	if i := strings.IndexByte(ref, '('); i >= 0 {
		ref = ref[:i]
	}
	ref = strings.TrimSpace(ref)
	i := strings.LastIndex(ref, ".")
	if i < 0 {
		return "", "", ""
	}
	method := ref[i+1:]
	cls := ref[:i]
	if method == "" || cls == "" {
		return "", "", ""
	}
	simple := cls
	if j := strings.LastIndex(cls, "."); j >= 0 {
		simple = cls[j+1:]
	}
	return cls, simple, method
}
