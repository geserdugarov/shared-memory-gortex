package languages

import (
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Tree-sitter query patterns for Go source files.
const (
	qPackage = `(package_clause (package_identifier) @pkg.name)`

	qFunction = `(function_declaration
		name: (identifier) @func.name
		parameters: (parameter_list) @func.params
		result: (_)? @func.result) @func.def`

	qMethod = `(method_declaration
		receiver: (parameter_list) @method.receiver
		name: (field_identifier) @method.name
		parameters: (parameter_list) @method.params
		result: (_)? @method.result) @method.def`

	qStruct = `(type_declaration
		(type_spec
			name: (type_identifier) @type.name
			type: (struct_type) @type.body)) @type.def`

	qInterface = `(type_declaration
		(type_spec
			name: (type_identifier) @iface.name
			type: (interface_type) @iface.body)) @iface.def`

	qTypeOther = `(type_declaration
		(type_spec
			name: (type_identifier) @typedef.name
			type: (_) @typedef.type)) @typedef.def`

	qTypeAlias = `(type_declaration
		(type_alias
			name: (type_identifier) @alias.name
			type: (_) @alias.type)) @alias.def`

	qImportSingle = `(import_declaration
		(import_spec
			name: (package_identifier)? @import.alias
			path: (interpreted_string_literal) @import.path))`

	qImportBlock = `(import_declaration
		(import_spec_list
			(import_spec
				name: (package_identifier)? @import.alias
				path: (interpreted_string_literal) @import.path)))`

	qCallPlain = `(call_expression
		function: (identifier) @call.name) @call.expr`

	qCallSelector = `(call_expression
		function: (selector_expression
			operand: (_) @call.receiver
			field: (field_identifier) @call.method)) @call.expr`

	qVar = `(var_declaration
		(var_spec
			name: (identifier) @var.name
			type: (_)? @var.type)) @var.def`

	qConst = `(const_declaration
		(const_spec
			name: (identifier) @const.name)) @const.def`

	qShortVar = `(short_var_declaration
		left: (expression_list (identifier) @svar.name)
		right: (expression_list (_) @svar.value)) @svar.def`
)

// GoExtractor extracts Go source files into graph nodes and edges.
type GoExtractor struct {
	lang *sitter.Language
}

func NewGoExtractor() *GoExtractor {
	return &GoExtractor{lang: golang.GetLanguage()}
}

func (e *GoExtractor) Language() string     { return "go" }
func (e *GoExtractor) Extensions() []string { return []string{".go"} }

func (e *GoExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	tree, err := parser.ParseFile(src, e.lang)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	root := tree.RootNode()
	result := &parser.ExtractionResult{}

	// File node.
	fileNode := &graph.Node{
		ID:        filePath,
		Kind:      graph.KindFile,
		Name:      filePath,
		FilePath:  filePath,
		StartLine: 1,
		EndLine:   int(root.EndPoint().Row) + 1,
		Language:  "go",
	}
	result.Nodes = append(result.Nodes, fileNode)

	// Package declaration.
	pkgName := e.extractPackage(root, src, filePath, result)

	// Functions.
	e.extractFunctions(root, src, filePath, fileNode.ID, result)

	// Methods.
	e.extractMethods(root, src, filePath, fileNode.ID, result)

	// Types: structs, interfaces, type aliases.
	e.extractTypes(root, src, filePath, fileNode.ID, result)

	// Imports.
	e.extractImports(root, src, filePath, fileNode.ID, pkgName, result)

	// Build type environment for receiver type inference.
	tenv := e.buildTypeEnv(root, src)

	// Call sites (with type env for receiver resolution).
	e.extractCalls(root, src, filePath, result, tenv)

	// Variables and constants.
	e.extractVarsConsts(root, src, filePath, fileNode.ID, result)

	return result, nil
}

func (e *GoExtractor) extractPackage(root *sitter.Node, src []byte, filePath string, result *parser.ExtractionResult) string {
	matches, err := parser.RunQuery(qPackage, e.lang, root, src)
	if err != nil || len(matches) == 0 {
		return ""
	}
	name := matches[0].Captures["pkg.name"].Text
	return name
}

func (e *GoExtractor) extractFunctions(root *sitter.Node, src []byte, filePath, fileID string, result *parser.ExtractionResult) {
	matches, err := parser.RunQuery(qFunction, e.lang, root, src)
	if err != nil {
		return
	}
	for _, m := range matches {
		name := m.Captures["func.name"].Text
		def := m.Captures["func.def"]

		id := filePath + "::" + name
		node := &graph.Node{
			ID:        id,
			Kind:      graph.KindFunction,
			Name:      name,
			FilePath:  filePath,
			StartLine: def.StartLine + 1,
			EndLine:   def.EndLine + 1,
			Language:  "go",
			Meta:      make(map[string]any),
		}
		node.Meta["signature"] = buildFuncSignature(name, m.Captures["func.params"], m.Captures["func.result"])
		if resultCap, ok := m.Captures["func.result"]; ok && resultCap.Text != "" {
			if rt := normalizeGoTypeName(resultCap.Text); rt != "" {
				node.Meta["return_type"] = rt
			}
		}
		result.Nodes = append(result.Nodes, node)
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
	}
}

func (e *GoExtractor) extractMethods(root *sitter.Node, src []byte, filePath, fileID string, result *parser.ExtractionResult) {
	matches, err := parser.RunQuery(qMethod, e.lang, root, src)
	if err != nil {
		return
	}
	for _, m := range matches {
		name := m.Captures["method.name"].Text
		def := m.Captures["method.def"]
		receiverText := m.Captures["method.receiver"].Text
		receiverType := extractReceiverType(receiverText)

		id := filePath + "::" + receiverType + "." + name
		node := &graph.Node{
			ID:        id,
			Kind:      graph.KindMethod,
			Name:      name,
			FilePath:  filePath,
			StartLine: def.StartLine + 1,
			EndLine:   def.EndLine + 1,
			Language:  "go",
			Meta: map[string]any{
				"receiver": receiverType,
			},
		}
		node.Meta["signature"] = buildMethodSignature(receiverText, name, m.Captures["method.params"], m.Captures["method.result"])
		if resultCap, ok := m.Captures["method.result"]; ok && resultCap.Text != "" {
			if rt := normalizeGoTypeName(resultCap.Text); rt != "" {
				node.Meta["return_type"] = rt
			}
		}
		result.Nodes = append(result.Nodes, node)
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})

		// MemberOf edge to receiver type.
		typeID := filePath + "::" + receiverType
		result.Edges = append(result.Edges, &graph.Edge{
			From: id, To: typeID, Kind: graph.EdgeMemberOf, FilePath: filePath, Line: def.StartLine + 1,
		})
	}
}

func (e *GoExtractor) extractTypes(root *sitter.Node, src []byte, filePath, fileID string, result *parser.ExtractionResult) {
	// Track which names we've already added to avoid duplicates between
	// struct/interface queries and the general type alias query.
	seen := make(map[string]bool)

	// Structs.
	matches, _ := parser.RunQuery(qStruct, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["type.name"].Text
		def := m.Captures["type.def"]
		seen[name] = true
		id := filePath + "::" + name
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindType, Name: name,
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "go",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Interfaces.
	matches, _ = parser.RunQuery(qInterface, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["iface.name"].Text
		def := m.Captures["iface.def"]
		seen[name] = true
		id := filePath + "::" + name

		// Extract method specs from the interface body by walking child nodes.
		var methods []string
		if body := m.Captures["iface.body"]; body != nil && body.Node != nil {
			methods = extractInterfaceMethods(body.Node, src)
		}

		node := &graph.Node{
			ID: id, Kind: graph.KindInterface, Name: name,
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "go",
			Meta:     map[string]any{"methods": methods},
		}
		result.Nodes = append(result.Nodes, node)
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Other type declarations (named types that aren't struct/interface).
	matches, _ = parser.RunQuery(qTypeOther, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["typedef.name"].Text
		if seen[name] {
			continue
		}
		seen[name] = true
		def := m.Captures["typedef.def"]
		id := filePath + "::" + name
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindType, Name: name,
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "go",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// True type aliases (type X = Y).
	matches, _ = parser.RunQuery(qTypeAlias, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["alias.name"].Text
		if seen[name] {
			continue
		}
		def := m.Captures["alias.def"]
		id := filePath + "::" + name
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindType, Name: name,
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "go",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
	}
}

func (e *GoExtractor) extractImports(root *sitter.Node, src []byte, filePath, fileID, _ string, result *parser.ExtractionResult) {
	for _, q := range []string{qImportSingle, qImportBlock} {
		matches, err := parser.RunQuery(q, e.lang, root, src)
		if err != nil {
			continue
		}
		for _, m := range matches {
			pathCap := m.Captures["import.path"]
			importPath := strings.Trim(pathCap.Text, `"`)
			result.Edges = append(result.Edges, &graph.Edge{
				From:     fileID,
				To:       "unresolved::import::" + importPath,
				Kind:     graph.EdgeImports,
				FilePath: filePath,
				Line:     pathCap.StartLine + 1,
			})
		}
	}
}

func (e *GoExtractor) extractCalls(root *sitter.Node, src []byte, filePath string, result *parser.ExtractionResult, tenv typeEnv) {
	funcRanges := buildFuncRanges(result)

	// Plain function calls: foo()
	matches, _ := parser.RunQuery(qCallPlain, e.lang, root, src)
	for _, m := range matches {
		callName := m.Captures["call.name"].Text
		expr := m.Captures["call.expr"]
		callerID := findEnclosingFunc(funcRanges, expr.StartLine+1)
		if callerID == "" {
			continue
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From:     callerID,
			To:       "unresolved::" + callName,
			Kind:     graph.EdgeCalls,
			FilePath: filePath,
			Line:     expr.StartLine + 1,
		})
	}

	// Selector calls: x.Method()
	matches, _ = parser.RunQuery(qCallSelector, e.lang, root, src)
	for _, m := range matches {
		methodName := m.Captures["call.method"].Text
		receiverText := m.Captures["call.receiver"].Text
		expr := m.Captures["call.expr"]
		callerID := findEnclosingFunc(funcRanges, expr.StartLine+1)
		if callerID == "" {
			continue
		}

		edge := &graph.Edge{
			From:     callerID,
			To:       "unresolved::*." + methodName,
			Kind:     graph.EdgeCalls,
			FilePath: filePath,
			Line:     expr.StartLine + 1,
		}

		// Attach receiver type hint from type env (Tier 0+1) or chain resolution (Tier 2).
		if recvType, ok := tenv[receiverText]; ok {
			edge.Meta = map[string]any{"receiver_type": recvType}
		} else if strings.Contains(receiverText, ".") || strings.Contains(receiverText, "(") {
			if chainType := resolveChainType(receiverText, tenv, result); chainType != "" {
				edge.Meta = map[string]any{"receiver_type": chainType}
			}
		}

		result.Edges = append(result.Edges, edge)
	}
}

func (e *GoExtractor) extractVarsConsts(root *sitter.Node, src []byte, filePath, fileID string, result *parser.ExtractionResult) {
	for _, q := range []string{qVar, qConst} {
		matches, err := parser.RunQuery(q, e.lang, root, src)
		if err != nil {
			continue
		}
		for _, m := range matches {
			var name string
			var def *parser.CapturedNode
			if c, ok := m.Captures["var.name"]; ok {
				name = c.Text
				def = m.Captures["var.def"]
			} else if c, ok := m.Captures["const.name"]; ok {
				name = c.Text
				def = m.Captures["const.def"]
			}
			if name == "" || name == "_" {
				continue
			}
			id := filePath + "::" + name
			result.Nodes = append(result.Nodes, &graph.Node{
				ID: id, Kind: graph.KindVariable, Name: name,
				FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
				Language: "go",
			})
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
			})
		}
	}
}

// --- Helpers ---

type funcRange struct {
	id        string
	startLine int // 1-based
	endLine   int // 1-based
}

func buildFuncRanges(result *parser.ExtractionResult) []funcRange {
	var ranges []funcRange
	for _, n := range result.Nodes {
		if n.Kind == graph.KindFunction || n.Kind == graph.KindMethod {
			ranges = append(ranges, funcRange{
				id: n.ID, startLine: n.StartLine, endLine: n.EndLine,
			})
		}
	}
	return ranges
}

func findEnclosingFunc(ranges []funcRange, line int) string {
	for _, r := range ranges {
		if line >= r.startLine && line <= r.endLine {
			return r.id
		}
	}
	return ""
}

// extractReceiverType extracts the type name from a Go receiver parameter list.
// "(s *Server)" -> "Server", "(s Server)" -> "Server"
func extractReceiverType(receiver string) string {
	receiver = strings.Trim(receiver, "()")
	parts := strings.Fields(receiver)
	if len(parts) == 0 {
		return ""
	}
	typePart := parts[len(parts)-1]
	typePart = strings.TrimPrefix(typePart, "*")
	return typePart
}

func buildFuncSignature(name string, params, result *parser.CapturedNode) string {
	sig := fmt.Sprintf("func %s%s", name, captureText(params))
	if result != nil && result.Text != "" {
		sig += " " + result.Text
	}
	return sig
}

func buildMethodSignature(receiver, name string, params, result *parser.CapturedNode) string {
	sig := fmt.Sprintf("func (%s) %s%s", receiver, name, captureText(params))
	if result != nil && result.Text != "" {
		sig += " " + result.Text
	}
	return sig
}

// extractInterfaceMethods walks the children of an interface_type node
// and returns the names of all method_spec entries.
func extractInterfaceMethods(ifaceNode *sitter.Node, src []byte) []string {
	var methods []string
	for i := 0; i < int(ifaceNode.NamedChildCount()); i++ {
		child := ifaceNode.NamedChild(i)
		if child.Type() == "method_elem" || child.Type() == "method_spec" {
			// The first named child of a method_spec is the field_identifier (name).
			for j := 0; j < int(child.NamedChildCount()); j++ {
				nameNode := child.NamedChild(j)
				if nameNode.Type() == "field_identifier" {
					methods = append(methods, nameNode.Content(src))
					break
				}
			}
		}
	}
	return methods
}

func captureText(c *parser.CapturedNode) string {
	if c == nil {
		return "()"
	}
	return c.Text
}

// --- Type environment for receiver type inference ---

// typeEnv maps variable name → inferred type name within a file.
type typeEnv map[string]string

// buildTypeEnv scans variable declarations and short variable declarations
// to infer types (Tier 0: explicit annotations, Tier 1: composite literals
// and Go constructor convention).
func (e *GoExtractor) buildTypeEnv(root *sitter.Node, src []byte) typeEnv {
	tenv := make(typeEnv)

	// Tier 0: explicit var declarations — var x Type
	matches, _ := parser.RunQuery(qVar, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["var.name"].Text
		if typeCap, ok := m.Captures["var.type"]; ok && typeCap.Text != "" {
			typeName := normalizeGoTypeName(typeCap.Text)
			if typeName != "" {
				tenv[name] = typeName
			}
		}
	}

	// Tier 0 + Tier 1: short variable declarations — x := expr
	matches, _ = parser.RunQuery(qShortVar, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["svar.name"].Text
		valueCap := m.Captures["svar.value"]
		if valueCap == nil || valueCap.Node == nil {
			continue
		}
		if inferred := inferTypeFromGoExpr(valueCap.Node, src); inferred != "" {
			tenv[name] = inferred
		}
	}

	return tenv
}

// normalizeGoTypeName strips pointer prefix and package qualifier.
// "*User" → "User", "pkg.User" → "User", "*pkg.User" → "User"
func normalizeGoTypeName(t string) string {
	t = strings.TrimPrefix(t, "*")
	if idx := strings.LastIndex(t, "."); idx >= 0 {
		t = t[idx+1:]
	}
	if t == "" || t[0] < 'A' || t[0] > 'Z' {
		return "" // skip built-in types like int, string, etc.
	}
	return t
}

// inferTypeFromGoExpr inspects a tree-sitter expression node to infer
// the type of a short variable declaration's RHS.
func inferTypeFromGoExpr(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "composite_literal":
		// User{} or User{field: val}
		// First named child is the type identifier.
		if node.NamedChildCount() > 0 {
			typeNode := node.NamedChild(0)
			return normalizeGoTypeName(typeNode.Content(src))
		}

	case "unary_expression":
		// &User{} — operand is composite_literal
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			if child.Type() == "composite_literal" {
				return inferTypeFromGoExpr(child, src)
			}
		}

	case "call_expression":
		// NewUser() → "User" (Go constructor convention)
		if node.NamedChildCount() > 0 {
			funcNode := node.NamedChild(0)
			if funcNode.Type() == "identifier" {
				funcName := funcNode.Content(src)
				if strings.HasPrefix(funcName, "New") && len(funcName) > 3 {
					candidate := funcName[3:]
					if len(candidate) > 0 && candidate[0] >= 'A' && candidate[0] <= 'Z' {
						return candidate
					}
				}
			}
		}
	}

	return ""
}

// --- Tier 2: Chain resolution ---

// resolveChainType tries to infer the type of a chained expression like
// "svc.GetUser()" by looking up the root variable in the type env, then
// following method return types through already-extracted nodes.
func resolveChainType(expr string, tenv typeEnv, result *parser.ExtractionResult) string {
	// Strip balanced parentheses and their contents.
	// "svc.GetUser(arg1, arg2).Save()" → "svc.GetUser.Save"
	cleaned := stripCallArgs(expr)

	parts := strings.Split(cleaned, ".")
	if len(parts) < 2 {
		return ""
	}

	// Look up the root variable.
	currentType, ok := tenv[parts[0]]
	if !ok {
		return ""
	}

	// Walk the chain: for each segment, find a method on currentType and read return_type.
	for i := 1; i < len(parts); i++ {
		methodName := parts[i]
		returnType := findMethodReturnType(currentType, methodName, result)
		if returnType == "" {
			return "" // chain breaks
		}
		currentType = returnType
	}

	return currentType
}

// stripCallArgs removes balanced parenthesized argument lists from an expression.
// "svc.GetUser(arg1).Save()" → "svc.GetUser.Save"
func stripCallArgs(expr string) string {
	var b strings.Builder
	depth := 0
	for _, ch := range expr {
		switch ch {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(ch)
			}
		}
	}
	return b.String()
}

// findMethodReturnType searches extracted nodes for a method with the given
// receiver type and name, returning its Meta["return_type"] if found.
func findMethodReturnType(receiverType, methodName string, result *parser.ExtractionResult) string {
	for _, n := range result.Nodes {
		if n.Kind != graph.KindMethod && n.Kind != graph.KindFunction {
			continue
		}
		if n.Name != methodName {
			continue
		}
		if recv, ok := n.Meta["receiver"].(string); ok && recv == receiverType {
			if rt, ok := n.Meta["return_type"].(string); ok {
				return rt
			}
		}
	}
	return ""
}
