package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Nim has a Python-like indent-delimited body structure but keeps the
// ML-family split between `proc` (effectful), `func` (pure), `method`
// (dispatch), `iterator`, `template`, `macro`, and `converter`. The
// trailing `*` marks exported symbols; we strip it from the name.
var (
	nimProcRe   = regexp.MustCompile(`(?m)^\s*(?:proc|func|method|iterator|template|macro|converter)\s+(\w+)\*?`)
	nimTypeRe   = regexp.MustCompile(`(?m)^\s+(\w+)\*?\s*(?:\*\s*)?=\s*(?:object|ref\s+object|tuple|enum|distinct)`)
	nimImportRe = regexp.MustCompile(`(?m)^\s*(?:import|include|from)\s+([\w./]+)`)
	nimCallRe   = regexp.MustCompile(`\b([a-zA-Z_]\w*)\s*\(`)
)

// NimExtractor extracts Nim source using regex.
type NimExtractor struct{}

func NewNimExtractor() *NimExtractor { return &NimExtractor{} }

func (e *NimExtractor) Language() string     { return "nim" }
func (e *NimExtractor) Extensions() []string { return []string{".nim", ".nims", ".nimble"} }

func (e *NimExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "nim",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isNimKeyword(name) {
			return
		}
		id := filePath + "::" + name
		if seen[id] {
			return
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: kind, Name: name,
			FilePath: filePath, StartLine: start, EndLine: end,
			Language: "nim",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range nimProcRe.FindAllSubmatchIndex(src, -1) {
		name := strings.TrimSuffix(string(src[m[2]:m[3]]), "*")
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findIndentedBlockEnd(lines, line))
	}
	for _, m := range nimTypeRe.FindAllSubmatchIndex(src, -1) {
		name := strings.TrimSuffix(string(src[m[2]:m[3]]), "*")
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findIndentedBlockEnd(lines, line))
	}

	for _, m := range nimImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range nimCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isNimKeyword(name) {
			continue
		}
		line := lineAt(src, m[0])
		callerID := findEnclosingFunc(funcRanges, line)
		if callerID == "" || strings.HasSuffix(callerID, "::"+name) {
			continue
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: callerID, To: "unresolved::" + name,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

func isNimKeyword(s string) bool {
	switch s {
	case "if", "elif", "else", "when", "case", "of", "while", "for", "in",
		"notin", "is", "isnot", "block", "break", "continue", "return", "yield",
		"proc", "func", "method", "iterator", "template", "macro", "converter",
		"type", "const", "let", "var", "object", "tuple", "enum", "distinct",
		"ref", "ptr", "import", "include", "from", "as", "export", "defer",
		"try", "except", "finally", "raise", "discard", "true", "false", "nil":
		return true
	}
	return false
}

var _ parser.Extractor = (*NimExtractor)(nil)
