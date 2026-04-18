package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Ada is a case-insensitive, heavily typed language. We cover the
// primary declaration shapes: procedures, functions, packages
// (specification or body), types, and `with` clauses for imports.
var (
	adaProcRe    = regexp.MustCompile(`(?im)^\s*procedure\s+([\w.]+)`)
	adaFuncRe    = regexp.MustCompile(`(?im)^\s*function\s+([\w.]+)`)
	adaPackageRe = regexp.MustCompile(`(?im)^\s*package\s+(?:body\s+)?([\w.]+)`)
	adaTypeRe    = regexp.MustCompile(`(?im)^\s*(?:sub)?type\s+(\w+)\s+is`)
	adaWithRe    = regexp.MustCompile(`(?im)^\s*with\s+([\w.]+(?:\s*,\s*[\w.]+)*)\s*;`)
	adaSplitRe   = regexp.MustCompile(`[\s,]+`)
)

// AdaExtractor extracts Ada source using regex.
type AdaExtractor struct{}

func NewAdaExtractor() *AdaExtractor { return &AdaExtractor{} }

func (e *AdaExtractor) Language() string { return "ada" }
func (e *AdaExtractor) Extensions() []string {
	return []string{".ada", ".adb", ".ads", ".gpr"}
}

func (e *AdaExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "ada",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" {
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
			Language: "ada",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range adaPackageRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, len(lines))
	}
	for _, m := range adaTypeRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, line)
	}
	for _, m := range adaProcRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "end "+strings.ToLower(name), "end"))
	}
	for _, m := range adaFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "end "+strings.ToLower(name), "end"))
	}

	for _, m := range adaWithRe.FindAllSubmatchIndex(src, -1) {
		clause := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		for _, tok := range adaSplitRe.Split(clause, -1) {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileNode.ID, To: "unresolved::import::" + tok,
				Kind: graph.EdgeImports, FilePath: filePath, Line: line,
			})
		}
	}

	return result, nil
}

var _ parser.Extractor = (*AdaExtractor)(nil)
