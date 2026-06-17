package contracts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

var (
	// objectRouteCallRE matches an object-config route registration —
	// fastify.route({...}) / server.route({...}) — up to the opening brace.
	objectRouteCallRE = regexp.MustCompile(`\b\w+\.route\(\s*\{`)
	// objRouteMethodRE captures the method field of a route object: a single
	// "GET" string or a ['GET','POST'] array.
	objRouteMethodRE = regexp.MustCompile("method\\s*:\\s*(\\[[^\\]]*\\]|[\"'`][^\"'`]*[\"'`])")
	// objRouteURLRE captures Fastify's url field; objRoutePathRE captures
	// Hapi's path field.
	objRouteURLRE  = regexp.MustCompile("url\\s*:\\s*[\"'`]([^\"'`]+)")
	objRoutePathRE = regexp.MustCompile("path\\s*:\\s*[\"'`]([^\"'`]+)")
	// objRouteHandlerRE captures a `handler: fnName` reference.
	objRouteHandlerRE = regexp.MustCompile(`handler\s*:\s*([A-Za-z_][\w.]*)`)
	// expressRouteChainRE matches app.route('/path').get(h).post(h) — the
	// chained method form. Group 1 is the path, group 2 the chain tail.
	expressRouteChainRE = regexp.MustCompile("\\b\\w+\\.route\\(\\s*[\"'`]([^\"'`]+)[\"'`]\\s*\\)((?:\\s*\\.\\w+\\([^)]*\\))+)")
	// chainVerbRE pulls each .get(handler) off the chain tail.
	chainVerbRE = regexp.MustCompile(`\.(get|post|put|delete|patch|head|options|all)\(\s*([A-Za-z_][\w.]*)?`)
	// jsQuotedTokenRE pulls each quoted verb out of a method array.
	jsQuotedTokenRE = regexp.MustCompile("[\"'`]([^\"'`]+)[\"'`]")
)

// extractObjectRouteProviders detects the object-config and method-chain route
// shapes the per-line provider table cannot: Fastify / Hapi
// `route({ method, url|path, handler })` and Express
// `route('/path').get(h).post(h)`. Each expands to one provider contract per
// HTTP method with the handler resolved.
func (h *HTTPExtractor) extractObjectRouteProviders(filePath, text string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) []Contract {
	var out []Contract

	// Fastify / Hapi: X.route({ method: ..., url|path: ..., handler: ... }).
	for _, loc := range objectRouteCallRE.FindAllStringIndex(text, -1) {
		braceStart := loc[1] - 1
		obj, ok := balancedBraces(text, braceStart)
		if !ok {
			continue
		}
		methods := jsObjectMethods(obj)
		if len(methods) == 0 {
			continue
		}
		routePath, framework := "", ""
		if m := objRouteURLRE.FindStringSubmatch(obj); m != nil {
			routePath, framework = m[1], "fastify"
		} else if m := objRoutePathRE.FindStringSubmatch(obj); m != nil {
			routePath, framework = m[1], "hapi"
		}
		if routePath == "" {
			continue
		}
		handlerID := ""
		if m := objRouteHandlerRE.FindStringSubmatch(obj); m != nil {
			handlerID = resolveHandlerIdent(fileNodes, m[1])
		}
		lineNum := lineOfOffset(text, loc[0])
		for _, method := range methods {
			out = append(out, h.buildObjectRouteContract(filePath, method, routePath, handlerID, framework, lineNum, lines, fileNodes, lang, tree))
		}
	}

	// Express: app.route('/path').get(h).post(h).
	for _, m := range expressRouteChainRE.FindAllStringSubmatchIndex(text, -1) {
		routePath := text[m[2]:m[3]]
		chain := text[m[4]:m[5]]
		lineNum := lineOfOffset(text, m[0])
		for _, vm := range chainVerbRE.FindAllStringSubmatch(chain, -1) {
			method := strings.ToUpper(vm[1])
			handlerID := resolveHandlerIdent(fileNodes, vm[2])
			out = append(out, h.buildObjectRouteContract(filePath, method, routePath, handlerID, "express", lineNum, lines, fileNodes, lang, tree))
		}
	}

	return out
}

// buildObjectRouteContract assembles a provider contract for an object-config
// or chained route.
func (h *HTTPExtractor) buildObjectRouteContract(filePath, method, path, symbolID, framework string, lineNum int, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) Contract {
	normPath, origNames := NormalizeHTTPPathWithParams(path)
	meta := map[string]any{
		"method":    method,
		"path":      normPath,
		"framework": framework,
	}
	if len(origNames) > 0 {
		meta["path_param_names"] = origNames
	}
	c := Contract{
		ID:         fmt.Sprintf("http::%s::%s", method, normPath),
		Type:       ContractHTTP,
		Role:       RoleProvider,
		SymbolID:   symbolID,
		FilePath:   filePath,
		Line:       lineNum,
		Meta:       meta,
		Confidence: 0.9,
	}
	EnrichHTTPContractWithTree(&c, lines, fileNodes, lang, tree)
	return c
}

// jsObjectMethods returns the upper-cased HTTP methods named in a route
// object's method field — one for a string, several for an array.
func jsObjectMethods(obj string) []string {
	m := objRouteMethodRE.FindStringSubmatch(obj)
	if m == nil {
		return nil
	}
	var out []string
	for _, tm := range jsQuotedTokenRE.FindAllStringSubmatch(m[1], -1) {
		if v := strings.ToUpper(strings.TrimSpace(tm[1])); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// balancedBraces returns the substring from the brace at start through its
// matching close brace, or ("", false) when unbalanced.
func balancedBraces(text string, start int) (string, bool) {
	if start < 0 || start >= len(text) || text[start] != '{' {
		return "", false
	}
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1], true
			}
		}
	}
	return "", false
}

// lineOfOffset returns the 1-based line number of a byte offset in text.
func lineOfOffset(text string, off int) int {
	if off > len(text) {
		off = len(text)
	}
	return 1 + strings.Count(text[:off], "\n")
}
