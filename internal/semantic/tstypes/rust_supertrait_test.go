package tstypes

import (
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

// A `trait Sub: Super` declaration makes Sub extend Super; scoped and
// multi-bound supertraits each yield their own extends edge.
func TestRust_SupertraitExtendsSynthesis(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"src/traits.rs": `pub trait Super {
    fn base(&self);
}

pub trait Marker {
    fn mark(&self);
}

pub trait Sub: Super + Marker {
    fn extra(&self);
}
`,
	})
	p := NewProvider(RustSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	sub := nodeByNameKind(t, g, "Sub", graph.KindInterface)
	super_ := nodeByNameKind(t, g, "Super", graph.KindInterface)
	marker := nodeByNameKind(t, g, "Marker", graph.KindInterface)

	e := edgeBetween(g, sub.ID, graph.EdgeExtends, super_.ID)
	if e == nil {
		t.Fatalf("extends edge Sub->Super missing; edges: %v", g.GetOutEdges(sub.ID))
	}
	assertASTProvenance(t, e, "rust-types")

	if edgeBetween(g, sub.ID, graph.EdgeExtends, marker.ID) == nil {
		t.Fatalf("extends edge Sub->Marker missing; edges: %v", g.GetOutEdges(sub.ID))
	}
}
