package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// F# is an indent-sensitive ML-family language. The shapes that matter
// for extraction are `let`-bindings (both values and functions; they
// share syntax), `module` declarations, `type` declarations, `member`
// method bindings inside types, and `open` imports.
var (
	fsharpLetRe    = regexp.MustCompile(`(?m)^[ \t]*(?:let|and)\s+(?:rec\s+|mutable\s+|inline\s+|private\s+|internal\s+|public\s+)*(\w+)`)
	fsharpModuleRe = regexp.MustCompile(`(?m)^[ \t]*module(?:\s+rec)?\s+(?:private\s+|internal\s+|public\s+)?([\w.]+)`)
	fsharpTypeRe   = regexp.MustCompile(`(?m)^[ \t]*(?:type|and)\s+(?:private\s+|internal\s+|public\s+)?(\w+)\s*[=<(]`)
	fsharpMemberRe = regexp.MustCompile(`(?m)^[ \t]*(?:static\s+)?member\s+(?:this\.|_\.|[\w.]+\.)?(\w+)`)
	fsharpOpenRe   = regexp.MustCompile(`(?m)^[ \t]*open\s+([\w.]+)`)
)

// FSharpExtractor extracts F# source using regex.
type FSharpExtractor struct{}

func NewFSharpExtractor() *FSharpExtractor { return &FSharpExtractor{} }

func (e *FSharpExtractor) Language() string     { return "fsharp" }
func (e *FSharpExtractor) Extensions() []string { return []string{".fs", ".fsi", ".fsx"} }

func (e *FSharpExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "fsharp",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isFSharpKeyword(name) {
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
			Language: "fsharp",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range fsharpModuleRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findIndentedBlockEnd(lines, line))
	}
	for _, m := range fsharpTypeRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findIndentedBlockEnd(lines, line))
	}
	for _, m := range fsharpLetRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findIndentedBlockEnd(lines, line))
	}
	for _, m := range fsharpMemberRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindMethod, line, findIndentedBlockEnd(lines, line))
	}

	for _, m := range fsharpOpenRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

func isFSharpKeyword(s string) bool {
	switch s {
	case "let", "rec", "and", "in", "do", "done", "if", "then", "else", "elif",
		"match", "with", "when", "for", "while", "to", "downto", "yield",
		"return", "fun", "function", "module", "namespace", "open", "type",
		"new", "null", "true", "false", "use", "begin", "end", "member",
		"static", "override", "mutable", "inline", "abstract", "default",
		"interface", "struct", "class", "of", "as":
		return true
	}
	return false
}

var _ parser.Extractor = (*FSharpExtractor)(nil)
