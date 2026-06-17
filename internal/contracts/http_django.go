package contracts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

var (
	// djangoRouteRE matches a urlpatterns entry — path / re_path / url — and
	// captures the call name, the route literal (regex or path converter) and
	// the remainder of the line (the view + name= kwargs).
	djangoRouteRE = regexp.MustCompile(`\b(path|re_path|url)\(\s*r?["']([^"']*)["']\s*,\s*(.+)`)
	// djangoRouteCallRE is the cheap prefilter for the dedicated pass.
	djangoRouteCallRE = regexp.MustCompile(`\b(?:path|re_path|url)\s*\(`)
	// djangoIncludeRE matches an include('app.urls') sub-URLconf mount.
	djangoIncludeRE = regexp.MustCompile(`include\s*\(\s*["']([^"']+)["']`)
	// djangoAsViewRE matches a class-based view handler, View.as_view().
	djangoAsViewRE = regexp.MustCompile(`([A-Za-z_][\w.]*)\.as_view\b`)
	// djangoLeadIdentRE captures the leading dotted identifier of a handler.
	djangoLeadIdentRE = regexp.MustCompile(`^([A-Za-z_][\w.]*)`)
	// djangoPathConverterRE rewrites a path() converter, <int:year> / <year>,
	// to the {year} placeholder NormalizeHTTPPathWithParams understands.
	djangoPathConverterRE = regexp.MustCompile(`<(?:\w+:)?(\w+)>`)
	// djangoNamedGroupRE rewrites a re_path() named group, (?P<year>[0-9]{4}),
	// to {year}.
	djangoNamedGroupRE = regexp.MustCompile(`\(\?P<(\w+)>[^)]*\)`)
)

// extractDjangoRoutes detects Django urlpatterns route shapes —
// path / re_path / url — resolving the view handler (function, or a
// class-based View.as_view()) and recording include('app.urls') sub-URLconf
// mounts. It runs as a node-aware pass because the handler is a symbol declared
// elsewhere in the file, which the per-line provider table cannot resolve.
func (h *HTTPExtractor) extractDjangoRoutes(filePath, text string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) []Contract {
	var out []Contract
	for i, line := range lines {
		m := djangoRouteRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		callName, rawRoute, rest := m[1], m[2], m[3]
		lineNum := i + 1

		// path('blog/', include('blog.urls')) mounts a sub-URLconf.
		if inc := djangoIncludeRE.FindStringSubmatch(rest); inc != nil {
			out = append(out, h.buildDjangoMount(filePath, callName, rawRoute, inc[1], lineNum, lines, fileNodes, lang, tree))
			continue
		}

		symbolID := resolveDjangoHandler(rest, fileNodes)
		out = append(out, h.buildDjangoContract(filePath, callName, rawRoute, symbolID, lineNum, lines, fileNodes, lang, tree))
	}
	return out
}

// resolveDjangoHandler resolves the view a route dispatches to: a class-based
// View.as_view() resolves to the class node, otherwise the leading identifier
// resolves as a function/method handler.
func resolveDjangoHandler(expr string, fileNodes []*graph.Node) string {
	expr = strings.TrimSpace(expr)
	if m := djangoAsViewRE.FindStringSubmatch(expr); m != nil {
		name := m[1]
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		if t := findTypeNodeByName(fileNodes, name); t != nil {
			return t.ID
		}
		return ""
	}
	if m := djangoLeadIdentRE.FindStringSubmatch(expr); m != nil {
		return resolveHandlerIdent(fileNodes, m[1])
	}
	return ""
}

// normalizeDjangoRoute rewrites a Django route literal to the canonical HTTP
// path the contract ID hashes on: path() converters and re_path() named groups
// both collapse onto positional placeholders, and the route is rooted at "/".
func normalizeDjangoRoute(callName, raw string) string {
	s := strings.TrimSpace(raw)
	if callName == "re_path" || callName == "url" {
		s = strings.TrimPrefix(s, "^")
		s = strings.TrimSuffix(s, "$")
		s = djangoNamedGroupRE.ReplaceAllString(s, "{$1}")
	} else {
		s = djangoPathConverterRE.ReplaceAllString(s, "{$1}")
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

// buildDjangoContract assembles a provider contract for one Django route. The
// method is ANY: Django dispatches every verb to the same view, which decides
// the methods it serves.
func (h *HTTPExtractor) buildDjangoContract(filePath, callName, rawRoute, symbolID string, lineNum int, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) Contract {
	normPath, origNames := NormalizeHTTPPathWithParams(normalizeDjangoRoute(callName, rawRoute))
	meta := map[string]any{
		"method":      "ANY",
		"path":        normPath,
		"framework":   "django",
		"route_shape": callName,
	}
	if len(origNames) > 0 {
		meta["path_param_names"] = origNames
	}
	c := Contract{
		ID:         fmt.Sprintf("http::ANY::%s", normPath),
		Type:       ContractHTTP,
		Role:       RoleProvider,
		SymbolID:   symbolID,
		FilePath:   filePath,
		Line:       lineNum,
		Meta:       meta,
		Confidence: 0.85,
	}
	EnrichHTTPContractWithTree(&c, lines, fileNodes, lang, tree)
	return c
}

// buildDjangoMount records a path('prefix/', include('app.urls')) sub-URLconf
// mount — the prefix-join seed the route-prefix pass consumes.
func (h *HTTPExtractor) buildDjangoMount(filePath, callName, rawRoute, includeModule string, lineNum int, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) Contract {
	normPath, _ := NormalizeHTTPPathWithParams(normalizeDjangoRoute(callName, rawRoute))
	return Contract{
		ID:   fmt.Sprintf("http::MOUNT::%s", normPath),
		Type: ContractHTTP,
		Role: RoleProvider,
		Meta: map[string]any{
			"method":         "MOUNT",
			"path":           normPath,
			"framework":      "django",
			"route_shape":    callName,
			"django_include": includeModule,
			"mount":          true,
		},
		FilePath:   filePath,
		Line:       lineNum,
		Confidence: 0.85,
	}
}
