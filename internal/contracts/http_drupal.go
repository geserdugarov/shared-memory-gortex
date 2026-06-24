package contracts

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Drupal routing.yml route extraction. A Drupal module declares its routes in
// `<module>.routing.yml`, mapping a route name to a `path` and a `defaults`
// handler reference (`_controller: \Drupal\node\Controller\NodeController::view`,
// `_form: \Drupal\node\Form\NodeAddForm`, `_entity_*`). This pass parses the
// YAML and emits one route Contract per route, binding the handler same-file
// or stamping handler_ident/handler_class for the module-wide cross-file pass.

// drupalRoute is one route entry in a *.routing.yml file.
type drupalRoute struct {
	Path     string         `yaml:"path"`
	Methods  []string       `yaml:"methods"`
	Defaults map[string]any `yaml:"defaults"`
}

// extractDrupalRoutes parses a *.routing.yml file into route Contracts.
func (h *HTTPExtractor) extractDrupalRoutes(filePath, text string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) []Contract {
	var doc map[string]drupalRoute
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil
	}
	var out []Contract
	for name, r := range doc {
		if r.Path == "" {
			continue
		}
		fqcn, hclass, hident := drupalRouteHandler(r.Defaults)
		normPath, origNames := NormalizeHTTPPathWithParams(r.Path)
		lineNum := drupalRouteLine(lines, name)

		methods := []string{"ANY"}
		if len(r.Methods) > 0 {
			methods = methods[:0]
			for _, m := range r.Methods {
				methods = append(methods, strings.ToUpper(strings.TrimSpace(m)))
			}
		}
		for _, method := range methods {
			c := Contract{
				ID:         fmt.Sprintf("http::%s::%s", method, normPath),
				Type:       ContractHTTP,
				Role:       RoleProvider,
				FilePath:   filePath,
				Line:       lineNum,
				Confidence: 0.85,
				Meta: map[string]any{
					"method":       method,
					"path":         normPath,
					"framework":    "drupal",
					"drupal_route": name,
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
			if hident != "" {
				c.Meta["handler_ident"] = hident
			}
			if hident != "" && hclass != "" {
				if id := findMethodByNameAndReceiver(fileNodes, hident, hclass); id != "" {
					c.SymbolID = id
				}
			}
			EnrichHTTPContractWithTree(&c, lines, fileNodes, lang, tree)
			out = append(out, c)
		}
	}
	return out
}

// drupalRouteHandler reads the handler reference from a route's defaults:
// `_controller` carries `Class::method`; `_form` / `_entity_*` carry a bare
// class FQCN.
func drupalRouteHandler(defaults map[string]any) (fqcn, hclass, hident string) {
	if defaults == nil {
		return "", "", ""
	}
	if c, _ := defaults["_controller"].(string); c != "" {
		cls, method := drupalSplitControllerRef(c)
		return c, drupalSimpleClass(cls), method
	}
	for _, k := range []string{"_form", "_entity_form", "_entity_view", "_entity_list"} {
		if v, _ := defaults[k].(string); v != "" {
			return v, drupalSimpleClass(v), ""
		}
	}
	return "", "", ""
}

// drupalSplitControllerRef splits `Class::method` into its class FQCN and
// method, tolerating a bare class.
func drupalSplitControllerRef(ref string) (cls, method string) {
	if c, m, ok := strings.Cut(ref, "::"); ok {
		return c, m
	}
	return ref, ""
}

// drupalSimpleClass strips the namespace from a PHP FQCN.
func drupalSimpleClass(fqcn string) string {
	fqcn = strings.TrimPrefix(fqcn, "\\")
	if i := strings.LastIndex(fqcn, "\\"); i >= 0 {
		return fqcn[i+1:]
	}
	return fqcn
}

// drupalRouteLine returns the 1-based line of a route's top-level key.
func drupalRouteLine(lines []string, name string) int {
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), name+":") {
			return i + 1
		}
	}
	return 1
}
