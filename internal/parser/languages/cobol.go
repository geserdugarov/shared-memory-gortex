package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// COBOL extraction is deliberately conservative: we capture PROGRAM-ID,
// DIVISION and SECTION headers, `CALL 'name'` subprogram calls, and
// `COPY name` library imports. Paragraph names (bare labels in column 8)
// are too easy to confuse with data items, so we skip them.
var (
	cobolProgIDRe  = regexp.MustCompile(`(?im)^\s*PROGRAM-ID\.\s*(\w[\w-]*)`)
	cobolDivRe     = regexp.MustCompile(`(?im)^\s*([A-Z][\w-]*)\s+DIVISION\.`)
	cobolSectionRe = regexp.MustCompile(`(?im)^\s*([A-Z][\w-]*)\s+SECTION\.`)
	cobolCallRe    = regexp.MustCompile(`(?im)\bCALL\s+["']([^"']+)["']`)
	cobolCopyRe    = regexp.MustCompile(`(?im)\bCOPY\s+(\w[\w-]*)`)
)

// CobolExtractor extracts COBOL source using regex.
type CobolExtractor struct{}

func NewCobolExtractor() *CobolExtractor { return &CobolExtractor{} }

func (e *CobolExtractor) Language() string { return "cobol" }
func (e *CobolExtractor) Extensions() []string {
	return []string{".cob", ".cbl", ".cpy", ".COB", ".CBL", ".CPY"}
}

func (e *CobolExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "cobol",
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
			Language: "cobol",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range cobolProgIDRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, len(lines))
	}
	for _, m := range cobolDivRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]]) + "-DIVISION"
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, line)
	}
	for _, m := range cobolSectionRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]]) + "-SECTION"
		line := lineAt(src, m[0])
		add(name, graph.KindMethod, line, line)
	}

	for _, m := range cobolCopyRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range cobolCallRe.FindAllSubmatchIndex(src, -1) {
		target := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::" + target,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

var _ parser.Extractor = (*CobolExtractor)(nil)
