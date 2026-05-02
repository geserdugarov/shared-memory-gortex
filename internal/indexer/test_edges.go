package indexer

import (
	"github.com/zzet/gortex/internal/graph"
)

// markTestSymbolsAndEmitEdges runs after the resolver and before
// community detection. It performs two passes over the graph:
//
//  1. Walk every function/method node and stamp Meta["is_test"] = true
//     when the node lives in a test file (per IsTestFile) or its name
//     matches a per-language test convention (per IsTestSymbol).
//
//  2. Walk every EdgeCalls. For each call whose source is a test
//     function and whose target is non-test, emit a parallel
//     EdgeTests pointing to the same target.
//
// The split lets agents distinguish prod callers from test callers
// (find_usages with exclude_tests) and lets get_test_targets answer
// "which tests cover X?" with a single reverse-edge walk instead of
// the runtime call-graph traversal it does today.
//
// Returns counts for telemetry: number of nodes marked as test,
// number of EdgeTests emitted.
func markTestSymbolsAndEmitEdges(g *graph.Graph) (markedTests int, edgesEmitted int) {
	if g == nil {
		return 0, 0
	}

	// Pass 1: classify file nodes, then function/method nodes.
	testFiles := map[string]bool{} // file node ID → is test file
	for _, n := range g.AllNodes() {
		if n == nil || n.Kind != graph.KindFile {
			continue
		}
		if IsTestFile(n.FilePath) {
			testFiles[n.ID] = true
			if n.Meta == nil {
				n.Meta = map[string]any{}
			}
			n.Meta["is_test_file"] = true
		}
	}

	for _, n := range g.AllNodes() {
		if n == nil {
			continue
		}
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			continue
		}
		isTest := false
		if testFiles[n.FilePath] {
			isTest = true
		} else if IsTestSymbol(n.Name, n.Language) {
			isTest = true
		}
		if !isTest {
			continue
		}
		if n.Meta == nil {
			n.Meta = map[string]any{}
		}
		n.Meta["is_test"] = true
		markedTests++
	}

	// Pass 2: walk EdgeCalls; for each (test, non-test) pair, emit a
	// parallel EdgeTests. We dedupe per (From, To) because a single
	// test can call the same subject multiple times.
	seen := map[string]bool{}
	type pair struct{ from, to string }
	var pending []struct {
		pair pair
		edge *graph.Edge
	}
	for _, e := range g.AllEdges() {
		if e == nil || e.Kind != graph.EdgeCalls {
			continue
		}
		fromNode := g.GetNode(e.From)
		toNode := g.GetNode(e.To)
		if fromNode == nil || toNode == nil {
			continue
		}
		if !isTestNode(fromNode) {
			continue
		}
		if isTestNode(toNode) {
			continue // test → test calls are infrastructure, not subject coverage
		}
		key := e.From + "\x00" + e.To
		if seen[key] {
			continue
		}
		seen[key] = true
		pending = append(pending, struct {
			pair pair
			edge *graph.Edge
		}{pair{e.From, e.To}, e})
	}
	for _, p := range pending {
		newEdge := &graph.Edge{
			From:     p.pair.from,
			To:       p.pair.to,
			Kind:     graph.EdgeTests,
			FilePath: p.edge.FilePath,
			Line:     p.edge.Line,
			Origin:   graph.OriginASTInferred,
		}
		g.AddEdge(newEdge)
		edgesEmitted++
	}
	return markedTests, edgesEmitted
}

func isTestNode(n *graph.Node) bool {
	if n == nil || n.Meta == nil {
		return false
	}
	v, _ := n.Meta["is_test"].(bool)
	return v
}
