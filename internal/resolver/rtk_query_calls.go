package resolver

import (
	"github.com/zzet/gortex/internal/graph"
)

// rtkQueryVia is the Meta["via"] tag the JS/TS extractors stamp on a
// generated-hook → endpoint placeholder. RTK Query's createApi declares
// data endpoints and auto-generates a React hook per endpoint
// (`useGetUserQuery`) that has no source node — the extractor synthesizes
// both nodes and this placeholder, which this pass binds.
const rtkQueryVia = "rtk-query"

// ResolveRTKQueryCalls binds the two RTK Query edges: the synthesized
// generated-hook → endpoint placeholder (by endpoint name, gated to the
// same createApi file), and any still-unresolved component → generated-hook
// call (so a component's `useGetUserQuery()` reaches the endpoint's query
// body). The naming convention is RTK-contractual, so these land at the
// typed framework tier (ConfidenceTyped / ProvenanceFramework).
//
// Returns the number of edges landed on a real node.
func ResolveRTKQueryCalls(g graph.Store) int {
	if g == nil {
		return 0
	}
	// endpointByFileName: createApi file → endpoint name → endpoint node.
	endpointByFileName := map[string]map[string]*graph.Node{}
	// hookByName: generated-hook name → nodes.
	hookByName := map[string][]*graph.Node{}
	for _, n := range nodesByKindsOrAll(g, graph.KindFunction) {
		if n == nil || n.Meta == nil {
			continue
		}
		if ep, _ := n.Meta["rtk_endpoint"].(string); ep != "" {
			if endpointByFileName[n.FilePath] == nil {
				endpointByFileName[n.FilePath] = map[string]*graph.Node{}
			}
			endpointByFileName[n.FilePath][ep] = n
		}
		if gen, _ := n.Meta["rtk_generated_hook"].(bool); gen {
			hookByName[n.Name] = append(hookByName[n.Name], n)
		}
	}
	if len(endpointByFileName) == 0 {
		return 0
	}

	resolved := 0
	var reindex []graph.EdgeReindex
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil {
			continue
		}
		// (1) generated-hook → endpoint placeholder.
		if e.Meta != nil {
			if v, _ := e.Meta["via"].(string); v == rtkQueryVia {
				ep, _ := e.Meta["rtk_endpoint"].(string)
				hookFile := ""
				if hn := g.GetNode(e.From); hn != nil {
					hookFile = hn.FilePath
				}
				var target *graph.Node
				if m := endpointByFileName[hookFile]; m != nil {
					target = m[ep]
				}
				rtkRebind(e, target, "unresolved::*."+ep, &reindex, &resolved)
				continue
			}
		}
		// (2) component → generated-hook call the generic resolver left
		// unresolved (e.g. the hook is imported cross-file).
		if !graph.IsUnresolvedTarget(e.To) {
			continue
		}
		cands := hookByName[graph.UnresolvedName(e.To)]
		if len(cands) == 0 {
			continue
		}
		if target := pickStoreAction(g, e, sameBoundaryCandidates(g, e.From, cands)); target != nil {
			rtkRebind(e, target, e.To, &reindex, &resolved)
		}
	}
	if len(reindex) > 0 {
		g.ReindexEdges(reindex)
	}
	return resolved
}

// rtkRebind points e at target (typed tier) or re-orphans it to
// unresolvedTo, recording the reindex and bumping resolved on a hit.
func rtkRebind(e *graph.Edge, target *graph.Node, unresolvedTo string, reindex *[]graph.EdgeReindex, resolved *int) {
	want := unresolvedTo
	if target != nil {
		want = target.ID
	}
	if e.To == want {
		if target != nil {
			*resolved++
		}
		return
	}
	oldTo := e.To
	e.To = want
	if target != nil {
		e.Origin = graph.OriginASTInferred
		e.Confidence = ConfidenceTyped
		e.ConfidenceLabel = graph.ConfidenceLabelFor(graph.EdgeCalls, ConfidenceTyped)
		StampSynthesizedTyped(e, SynthRTKQuery)
		*resolved++
	} else {
		e.Origin = graph.OriginASTInferred
		e.Confidence = 0
		e.ConfidenceLabel = ""
		UnstampSynthesized(e)
	}
	*reindex = append(*reindex, graph.EdgeReindex{Edge: e, OldTo: oldTo})
}
