package parser

import "github.com/zzet/gortex/internal/graph"

// Extractor extracts graph nodes and edges from a single source file.
type Extractor interface {
	Language() string
	Extensions() []string
	Extract(filePath string, src []byte) (*ExtractionResult, error)
}

// PreParser is an optional Extractor capability: a source-rewriting hook run
// before tree-sitter parsing. It lets a language neutralise constructs that
// confuse the grammar (e.g. C-family conditional-compilation directives that
// detach an enclosing declaration) without discarding any code.
//
// Implementations MUST preserve byte offsets and line counts exactly — the
// returned slice has the same length as the input and every newline stays in
// place — so all extracted node ranges, line numbers, and downstream
// resolution remain byte-accurate. Returning nil means "no rewrite".
//
// The hook is a first-class, language-agnostic interface rather than a
// per-language private step: any extractor opts in by implementing it, and the
// same offset-preserving rewrite machinery is then reusable across languages.
type PreParser interface {
	PreParse(src []byte) []byte
}

// ApplyPreParse runs e's PreParse hook when e implements PreParser and the hook
// returns a non-nil rewrite; otherwise it returns src unchanged. The identity
// default means extractors opt in by implementing PreParser, with no behaviour
// change for those that don't.
func ApplyPreParse(e Extractor, src []byte) []byte {
	if pp, ok := e.(PreParser); ok {
		if rewritten := pp.PreParse(src); rewritten != nil {
			return rewritten
		}
	}
	return src
}

// ExtractionResult holds the nodes and edges extracted from a single
// file, plus an optional handle to the parse tree the extractor used.
//
// When Tree is non-nil the indexer is responsible for releasing it
// after every per-file consumer (contract extractors, body-fact
// resolvers) has run. Languages whose extractor doesn't have a
// downstream consumer for the tree leave Tree as nil and close their
// own trees internally — the contract pipeline degrades to its regex
// fallback for those languages.
type ExtractionResult struct {
	Nodes []*graph.Node
	Edges []*graph.Edge
	Tree  *ParseTree
	// ConstValues carries the literal value of each KindConstant node
	// whose RHS is a string / numeric literal, for the indexer to persist
	// in the queryable constant_values sidecar (kept out of the gob Meta
	// blob). Keyed by the const node id. Empty for languages / files with
	// no literal constants.
	ConstValues []ConstValue
}

// ConstValue is one constant's persisted literal value: the const node id,
// its file (for file-scoped eviction), and the literal text.
type ConstValue struct {
	NodeID   string
	FilePath string
	Value    string
}
