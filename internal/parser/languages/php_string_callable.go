package languages

import (
	"strings"

	"github.com/zzet/gortex/internal/parser"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
)

// phpCallableHOFs is the curated set of PHP higher-order functions whose string
// / array arguments are callables. A string literal naming a function inside
// one of these calls is a function-as-value reference the gate resolves
// repo-wide (unique-or-drop), bypassing the same-file scope.
var phpCallableHOFs = map[string]bool{
	"array_map":                  true,
	"array_filter":               true,
	"array_walk":                 true,
	"array_walk_recursive":       true,
	"array_reduce":               true,
	"usort":                      true,
	"uasort":                     true,
	"uksort":                     true,
	"call_user_func":             true,
	"call_user_func_array":       true,
	"preg_replace_callback":      true,
	"register_shutdown_function": true,
	"set_error_handler":          true,
	"spl_autoload_register":      true,
}

// capturePHPStringCallables records each string / array callable passed to a
// curated higher-order function (`array_map('strtoupper', $xs)`,
// `usort($a, 'cmp')`, `['Foo', 'bar']`, `[$svc, 'handle']`) as a gate-skipping
// function-as-value candidate, so the callee is reachable through the
// registration even though no direct call edge exists.
func (e *PHPExtractor) capturePHPStringCallables(result *parser.ExtractionResult, root *sitter.Node, filePath string, src []byte) {
	if root == nil || result == nil {
		return
	}
	funcRanges := buildFuncRanges(result)
	if len(funcRanges) == 0 {
		return
	}
	var cands []FnValueCandidate
	seen := map[string]bool{}
	walkNodes(root, func(n *sitter.Node) {
		if n.Type() != "function_call_expression" {
			return
		}
		fn := n.ChildByFieldName("function")
		if fn == nil {
			return
		}
		callee := strings.TrimSpace(fn.Content(src))
		if i := strings.LastIndex(callee, "\\"); i >= 0 {
			callee = callee[i+1:]
		}
		if !phpCallableHOFs[strings.ToLower(callee)] {
			return
		}
		args := n.ChildByFieldName("arguments")
		if args == nil {
			return
		}
		line := int(n.StartPoint().Row) + 1
		fromID := findEnclosingFunc(funcRanges, line)
		if fromID == "" {
			return
		}
		for i := 0; i < int(args.NamedChildCount()); i++ {
			a := args.NamedChild(i)
			if a == nil {
				continue
			}
			name, recvHint, ok := e.phpCallableArg(a, src)
			if !ok {
				continue
			}
			key := fromID + "\x00php\x00" + name + "\x00" + recvHint
			if seen[key] {
				continue
			}
			seen[key] = true
			cands = append(cands, FnValueCandidate{
				FromID: fromID, Name: name, FilePath: filePath, Line: line,
				Form: "php_string_callable", RecvHint: recvHint, Lang: "php", SkipGate: true,
			})
		}
	})
	EmitFnValueCandidates(result, cands)
}

// phpCallableArg resolves a callable argument node to its (function name,
// receiver hint): a `'fn'` / `'ns\fn'` string, a `'Class::method'` static
// string, or a `[$obj, 'm']` / `['Class', 'm']` array callable. recvHint is the
// class for a method callable, "" for a free function.
func (e *PHPExtractor) phpCallableArg(a *sitter.Node, src []byte) (name, recvHint string, ok bool) {
	// Unwrap an `argument` wrapper to its value node.
	node := a
	if a.Type() == "argument" && a.NamedChildCount() > 0 {
		node = a.NamedChild(0)
	}
	switch node.Type() {
	case "string", "encapsed_string":
		return phpCallableFromString(e.extractStringContent(node, src))
	case "array_creation_expression":
		var strs []string
		walkNodes(node, func(c *sitter.Node) {
			if c.Type() == "string" || c.Type() == "encapsed_string" {
				if s := strings.TrimSpace(e.extractStringContent(c, src)); s != "" {
					strs = append(strs, s)
				}
			}
		})
		switch len(strs) {
		case 1:
			if isPhpIdent(strs[0]) {
				return strs[0], "", true
			}
		case 2:
			if isPhpIdent(phpTrailingSegment(strs[0])) && isPhpIdent(strs[1]) {
				return strs[1], phpTrailingSegment(strs[0]), true
			}
		}
	}
	return "", "", false
}

// phpCallableFromString parses a string callable into (function/method name,
// receiver hint). Returns ok=false for any string that is not a callable-name
// shape (a regex pattern, a format string, …).
func phpCallableFromString(s string) (name, recvHint string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	if i := strings.Index(s, "::"); i >= 0 {
		cls := phpTrailingSegment(strings.TrimPrefix(s[:i], "\\"))
		member := s[i+2:]
		if isPhpIdent(cls) && isPhpIdent(member) {
			return member, cls, true
		}
		return "", "", false
	}
	bare := phpTrailingSegment(strings.TrimPrefix(s, "\\"))
	if isPhpIdent(bare) {
		return bare, "", true
	}
	return "", "", false
}

// phpTrailingSegment returns the last `\`-separated segment of a namespaced name.
func phpTrailingSegment(s string) string {
	if i := strings.LastIndex(s, "\\"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// isPhpIdent reports whether s is a bare PHP identifier (a valid function /
// method / class name), excluding regex patterns and other arbitrary strings.
func isPhpIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			continue
		}
		if i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}
