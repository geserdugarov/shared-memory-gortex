package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/zzet/gortex/internal/query"
)

// registerWalkGraphTool wires walk_graph — a token-budgeted, free-form
// graph traversal. Unlike the fixed-purpose traversal tools
// (get_call_chain, get_dependents) the caller picks the edge kinds and
// the direction, and the walk auto-stops once the estimated encoded
// size of the result reaches a token budget.
func (s *Server) registerWalkGraphTool() {
	edgeKindList := strings.Join(query.KnownEdgeKinds(), ", ")
	s.addTool(
		mcp.NewTool("walk_graph",
			mcp.WithDescription("Token-budgeted free-form graph traversal from a start symbol. Pick the edge kinds and direction; the walk expands breadth-first and stops automatically once the encoded result would exceed the token budget. Use it to explore a neighbourhood when the fixed-purpose tools (get_call_chain, get_dependents) don't match the relationship you want to follow."),
			mcp.WithString("id", mcp.Required(), mcp.Description("Start symbol node ID (e.g. pkg/server.go::HandleRequest).")),
			mcp.WithString("edge_kinds", mcp.Description("Comma-separated edge kinds to follow (default: calls). Valid kinds: "+edgeKindList+".")),
			mcp.WithString("direction", mcp.Description("Traversal direction: out (default — follow outgoing edges), in (incoming), or both (undirected).")),
			mcp.WithNumber("token_budget", mcp.Description("Approximate token ceiling for the encoded result (default 6000). The walk stops adding nodes once the estimate would exceed this.")),
			mcp.WithNumber("max_depth", mcp.Description("Hard cap on BFS depth, applied even when the token budget would allow deeper expansion (default 8).")),
			mcp.WithString("format", mcp.Description("Output format: json (default), gcx (GCX1 compact wire format), or toon.")),
			mcp.WithString("repo", mcp.Description("Filter results to a specific repository prefix")),
			mcp.WithString("project", mcp.Description("Filter results to repositories in a specific project")),
			mcp.WithString("ref", mcp.Description("Filter results to repositories with a specific reference tag")),
		),
		s.handleWalkGraph,
	)
}

// handleWalkGraph runs WalkBudgeted with the request's parameters and
// returns the resulting subgraph. The response carries budget_hit and
// stopped_at_depth so the caller knows whether the walk was truncated.
func (s *Server) handleWalkGraph(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError("id is required"), nil
	}

	edgeKinds, kindErr := query.ParseEdgeKindsCSV(req.GetString("edge_kinds", "calls"))
	if kindErr != nil {
		return mcp.NewToolResultError(kindErr.Error()), nil
	}
	if len(edgeKinds) == 0 {
		// An explicit empty value would otherwise mean "every kind";
		// walk_graph's documented default is calls.
		edgeKinds, _ = query.ParseEdgeKindsCSV("calls")
	}

	direction := strings.ToLower(strings.TrimSpace(req.GetString("direction", "out")))
	switch direction {
	case "", "out", "in", "both":
	default:
		return mcp.NewToolResultError(fmt.Sprintf("direction must be out, in, or both (got %q)", direction)), nil
	}

	tokenBudget := req.GetInt("token_budget", 6000)
	if tokenBudget <= 0 {
		tokenBudget = 6000
	}
	maxDepth := req.GetInt("max_depth", 8)
	if maxDepth <= 0 {
		maxDepth = 8
	}

	eng := s.engineFor(ctx)
	if eng.GetSymbol(id) == nil {
		return mcp.NewToolResultError(fmt.Sprintf("symbol not found: %s", id)), nil
	}

	scopeWS, scopeProj := s.scopeFromRequest(ctx, &req)
	sg := eng.WalkBudgeted(id, query.WalkOptions{
		EdgeKinds:   edgeKinds,
		Direction:   direction,
		TokenBudget: tokenBudget,
		MaxDepth:    maxDepth,
		WorkspaceID: scopeWS,
		ProjectID:   scopeProj,
	})

	allowed, filterErr := s.resolveRepoFilter(ctx, req)
	if filterErr != nil {
		return mcp.NewToolResultError(filterErr.Error()), nil
	}
	budgetHit, stoppedAt := sg.BudgetHit, sg.StoppedAtDepth
	sg = filterSubGraph(sg, allowed)
	// filterSubGraph builds a fresh SubGraph and does not copy the
	// budget fields — restore them so the response keeps them.
	sg.BudgetHit = budgetHit
	sg.StoppedAtDepth = stoppedAt
	enrichSubGraphEdges(sg)
	return s.returnSubGraph(ctx, req, sg)
}
