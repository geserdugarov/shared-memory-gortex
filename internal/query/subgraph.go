package query

import (
	"fmt"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// SubGraph is a JSON-serializable result from a graph query.
type SubGraph struct {
	Nodes      []*graph.Node `json:"nodes"`
	Edges      []*graph.Edge `json:"edges"`
	TotalNodes int           `json:"total_nodes"`
	TotalEdges int           `json:"total_edges"`
	Truncated  bool          `json:"truncated"`
}

// QueryOptions controls traversal depth, result limits, and detail level.
type QueryOptions struct {
	Depth  int    `json:"depth"`
	Limit  int    `json:"limit"`
	Detail string `json:"detail"` // "brief" or "full"
}

// ToDot returns a Graphviz DOT representation of the subgraph.
func (sg *SubGraph) ToDot() string {
	var b strings.Builder
	b.WriteString("digraph gortex {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [fontname=\"monospace\" fontsize=10];\n")
	b.WriteString("  edge [fontname=\"monospace\" fontsize=8];\n\n")

	kindColors := map[graph.NodeKind]string{
		graph.KindFile:      "#607D8B",
		graph.KindPackage:   "#bb9af7",
		graph.KindFunction:  "#7aa2f7",
		graph.KindMethod:    "#7dcfff",
		graph.KindType:      "#9ece6a",
		graph.KindInterface: "#73daca",
		graph.KindVariable:  "#ff9e64",
		graph.KindImport:    "#795548",
	}

	kindShapes := map[graph.NodeKind]string{
		graph.KindFile:      "folder",
		graph.KindFunction:  "ellipse",
		graph.KindMethod:    "ellipse",
		graph.KindType:      "box",
		graph.KindInterface: "box",
		graph.KindVariable:  "triangle",
		graph.KindImport:    "note",
		graph.KindPackage:   "diamond",
	}

	for _, n := range sg.Nodes {
		color := kindColors[n.Kind]
		if color == "" {
			color = "#565f89"
		}
		shape := kindShapes[n.Kind]
		if shape == "" {
			shape = "ellipse"
		}
		label := fmt.Sprintf("%s\\n%s", n.Name, n.Kind)
		fmt.Fprintf(&b, "  %q [label=%q shape=%s style=filled fillcolor=%q fontcolor=white];\n",
			n.ID, label, shape, color)
	}

	b.WriteString("\n")

	edgeColors := map[graph.EdgeKind]string{
		graph.EdgeCalls:        "#7aa2f7",
		graph.EdgeImports:      "#565f89",
		graph.EdgeDefines:      "#414868",
		graph.EdgeImplements:   "#9ece6a",
		graph.EdgeExtends:      "#bb9af7",
		graph.EdgeReferences:   "#3b4261",
		graph.EdgeMemberOf:     "#3b4261",
		graph.EdgeInstantiates: "#e0af68",
	}

	for _, e := range sg.Edges {
		color := edgeColors[e.Kind]
		if color == "" {
			color = "#3b4261"
		}
		fmt.Fprintf(&b, "  %q -> %q [label=%q color=%q];\n",
			e.From, e.To, e.Kind, color)
	}

	b.WriteString("}\n")
	return b.String()
}

// DefaultOptions returns options with sensible defaults.
func DefaultOptions() QueryOptions {
	return QueryOptions{
		Depth:  3,
		Limit:  50,
		Detail: "brief",
	}
}
