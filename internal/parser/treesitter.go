package parser

import (
	"context"
	"fmt"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
)

const parseTimeout = 5 * time.Second

// NB: a sync.Pool of *sitter.Parser was attempted for allocation
// savings but the smacker/go-tree-sitter bindings surface cross-call
// state (bogus "operation limit was hit" errors on reused parsers
// even after Reset + SetLanguage). Net: allocate fresh per call
// until upstream exposes a cleaner reset path.

// CapturedNode holds information about a single captured tree-sitter node.
type CapturedNode struct {
	Text      string
	StartLine int // 0-based (tree-sitter native)
	EndLine   int // 0-based
	StartCol  int
	EndCol    int
	Node      *sitter.Node
}

// QueryResult represents a single match from a tree-sitter query.
type QueryResult struct {
	Captures map[string]*CapturedNode
}

// ParseFile parses source bytes with the given language and returns the tree.
// The caller must call tree.Close() when done.
//
// The *sitter.Parser is borrowed from a pool and returned after parse —
// `sitter.Parser.Close()` is only called on pool eviction (implicit via
// sync.Pool's GC sweep), not per file. This cuts allocation pressure
// noticeably on large-repo indexing where ParseFile is hot.
func ParseFile(src []byte, lang *sitter.Language) (*sitter.Tree, error) {
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(lang)

	ctx, cancel := context.WithTimeout(context.Background(), parseTimeout)
	defer cancel()

	tree, err := parser.ParseCtx(ctx, nil, src)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter parse: %w", err)
	}
	return tree, nil
}

// RunQuery executes a tree-sitter S-expression query against a node and
// returns all matches with their captures.
func RunQuery(pattern string, lang *sitter.Language, node *sitter.Node, src []byte) ([]QueryResult, error) {
	q, err := sitter.NewQuery([]byte(pattern), lang)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter query compile: %w", err)
	}
	defer q.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(q, node)

	var results []QueryResult
	for {
		match, ok := cursor.NextMatch()
		if !ok {
			break
		}
		match = cursor.FilterPredicates(match, src)
		if len(match.Captures) == 0 {
			continue
		}

		qr := QueryResult{
			Captures: make(map[string]*CapturedNode, len(match.Captures)),
		}
		for _, c := range match.Captures {
			name := q.CaptureNameForId(c.Index)
			qr.Captures[name] = &CapturedNode{
				Text:      c.Node.Content(src),
				StartLine: int(c.Node.StartPoint().Row),
				EndLine:   int(c.Node.EndPoint().Row),
				StartCol:  int(c.Node.StartPoint().Column),
				EndCol:    int(c.Node.EndPoint().Column),
				Node:      c.Node,
			}
		}
		results = append(results, qr)
	}
	return results, nil
}

// NodeText extracts the text content of a tree-sitter node from source bytes.
func NodeText(node *sitter.Node, src []byte) string {
	return node.Content(src)
}
