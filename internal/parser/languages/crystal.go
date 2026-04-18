package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Crystal shares most of Ruby's surface syntax plus static types.
// `def` for methods, `class` / `module` / `struct` for types,
// `require "path"` for dependencies. Method names may end in `?`,
// `!`, or `=`.
var (
	crystalDefRe     = regexp.MustCompile(`(?m)^\s*(?:private\s+|protected\s+)?def\s+(?:self\.)?(\w+[?!=]?)`)
	crystalClassRe   = regexp.MustCompile(`(?m)^\s*(?:abstract\s+)?class\s+([\w:]+)`)
	crystalModuleRe  = regexp.MustCompile(`(?m)^\s*module\s+([\w:]+)`)
	crystalStructRe  = regexp.MustCompile(`(?m)^\s*struct\s+([\w:]+)`)
	crystalRequireRe = regexp.MustCompile(`(?m)^\s*require\s+"([^"]+)"`)
	crystalCallRe    = regexp.MustCompile(`\b([a-z_]\w*[?!]?)\s*\(`)
)

// CrystalExtractor extracts Crystal source using regex.
type CrystalExtractor struct{}

func NewCrystalExtractor() *CrystalExtractor { return &CrystalExtractor{} }

func (e *CrystalExtractor) Language() string     { return "crystal" }
func (e *CrystalExtractor) Extensions() []string { return []string{".cr"} }

func (e *CrystalExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "crystal",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isCrystalKeyword(name) {
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
			Language: "crystal",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range crystalModuleRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findKeywordBlockEnd(lines, line, "end"))
	}
	for _, m := range crystalClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findKeywordBlockEnd(lines, line, "end"))
	}
	for _, m := range crystalStructRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findKeywordBlockEnd(lines, line, "end"))
	}
	for _, m := range crystalDefRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindMethod, line, findKeywordBlockEnd(lines, line, "end"))
	}

	for _, m := range crystalRequireRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range crystalCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isCrystalKeyword(name) {
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

func isCrystalKeyword(s string) bool {
	switch s {
	case "if", "elsif", "else", "unless", "while", "until", "for", "case",
		"when", "in", "then", "do", "end", "begin", "rescue", "ensure",
		"raise", "return", "next", "break", "yield", "def", "class", "module",
		"struct", "abstract", "private", "protected", "public", "self",
		"true", "false", "nil", "require", "include", "extend":
		return true
	}
	return false
}

var _ parser.Extractor = (*CrystalExtractor)(nil)
