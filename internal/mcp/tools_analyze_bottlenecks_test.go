package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// callAnalyzeBottlenecks invokes the analyze handler for kind=bottlenecks
// and returns the decoded rows keyed by symbol id. A high cognitive
// metric is stamped on the fixture functions so every node clears the
// "at least one reason" emit gate and shows up in the result — that lets
// the test read each function's transitive_loop_depth directly, even for
// nodes whose own loop depth is zero.
func callAnalyzeBottlenecks(t *testing.T, srv *Server, extra map[string]any) map[string]map[string]any {
	t.Helper()
	args := map[string]any{"kind": "bottlenecks"}
	for k, v := range extra {
		args[k] = v
	}
	req := mcplib.CallToolRequest{}
	req.Params.Name = "analyze"
	req.Params.Arguments = args
	res, err := srv.handleAnalyze(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError, "analyze bottlenecks must not error: %+v", res.Content)
	text := res.Content[0].(mcplib.TextContent).Text
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &out), "json: %s", text)

	byID := map[string]map[string]any{}
	rows, _ := out["functions"].([]any)
	for _, r := range rows {
		row := r.(map[string]any)
		byID[row["id"].(string)] = row
	}
	return byID
}

// addBottleneckFn drops one function node with the given loop depth into
// the graph. cognitive is stamped so the node always emits a row.
func addBottleneckFn(g graph.Store, id, file string, loopDepth, cognitive int) {
	g.AddNode(&graph.Node{
		ID: id, Kind: graph.KindFunction, Name: id,
		FilePath: file, StartLine: 1, EndLine: 5,
		Meta: map[string]any{
			"loop_depth": float64(loopDepth),
			"cognitive":  float64(cognitive),
		},
	})
}

func addBottleneckCall(g graph.Store, from, to, file string) {
	g.AddEdge(&graph.Edge{
		From: from, To: to,
		Kind: graph.EdgeCalls, FilePath: file, Line: 1,
	})
}

func tldOf(t *testing.T, row map[string]any) int {
	t.Helper()
	require.NotNil(t, row)
	v, ok := row["transitive_loop_depth"].(float64)
	require.True(t, ok, "transitive_loop_depth missing from row %+v", row)
	return int(v)
}

// TestAnalyzeBottlenecks_TransitiveLoopDepthThreadsThroughNonLoopingIntermediate
// is the regression guard for loop-depth propagation across a non-looping
// intermediate function. The call chain is F -> G -> H where F and H each
// contain a loop and G contains none. The transitive loop depth of F must
// account for H's loop reached through G: tld(F) = loop(F) + tld(G) = 1 +
// 1 = 2. Before the fix the propagation was gated on the callee itself
// looping, so G (loop 0) blocked H's depth and F was stuck at 1.
func TestAnalyzeBottlenecks_TransitiveLoopDepthThreadsThroughNonLoopingIntermediate(t *testing.T) {
	srv, _ := setupTestServer(t)
	const file = "chain.go"

	addBottleneckFn(srv.graph, "chain.go::F", file, 1, 20) // loops, calls G
	addBottleneckFn(srv.graph, "chain.go::G", file, 0, 20) // no loop, calls H
	addBottleneckFn(srv.graph, "chain.go::H", file, 1, 20) // loops, leaf

	addBottleneckCall(srv.graph, "chain.go::F", "chain.go::G", file)
	addBottleneckCall(srv.graph, "chain.go::G", "chain.go::H", file)

	byID := callAnalyzeBottlenecks(t, srv, nil)
	require.Contains(t, byID, "chain.go::F")
	require.Contains(t, byID, "chain.go::G")
	require.Contains(t, byID, "chain.go::H")

	assert.Equal(t, 2, tldOf(t, byID["chain.go::F"]),
		"F must thread H's loop up through the non-looping G: tld(F)=loop(F)+tld(G)=1+1=2")
	assert.Equal(t, 1, tldOf(t, byID["chain.go::G"]),
		"G carries H's loop depth even though G itself does not loop: tld(G)=loop(G)+tld(H)=0+1=1")
	assert.Equal(t, 1, tldOf(t, byID["chain.go::H"]),
		"H is a leaf loop: tld(H)=loop(H)=1")
}

// TestAnalyzeBottlenecks_TransitiveLoopDepthCycleStaysFinite proves the
// cycle guard survives the propagation-gate removal. A two-node cycle
// F -> G -> F must not recurse forever; the visited-set break caps each
// node at its own loop depth, so the computed transitive depth is finite
// and bounded.
func TestAnalyzeBottlenecks_TransitiveLoopDepthCycleStaysFinite(t *testing.T) {
	srv, _ := setupTestServer(t)
	const file = "cycle.go"

	addBottleneckFn(srv.graph, "cycle.go::F", file, 1, 20)
	addBottleneckFn(srv.graph, "cycle.go::G", file, 1, 20)

	addBottleneckCall(srv.graph, "cycle.go::F", "cycle.go::G", file)
	addBottleneckCall(srv.graph, "cycle.go::G", "cycle.go::F", file)

	byID := callAnalyzeBottlenecks(t, srv, nil)
	require.Contains(t, byID, "cycle.go::F")
	require.Contains(t, byID, "cycle.go::G")

	for _, id := range []string{"cycle.go::F", "cycle.go::G"} {
		got := tldOf(t, byID[id])
		assert.GreaterOrEqual(t, got, 1, "%s transitive depth must be at least its own loop depth", id)
		assert.LessOrEqual(t, got, 3, "%s transitive depth must stay bounded — the cycle guard must terminate", id)
		assert.Equal(t, true, byID[id]["recursive"], "%s participates in the F<->G cycle and must be flagged recursive", id)
	}
}
