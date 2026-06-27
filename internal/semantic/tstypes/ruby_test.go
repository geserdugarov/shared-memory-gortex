package tstypes

import (
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

const rubySvc = `class Svc
  def run
  end

  def stop
  end
end
`

// Ruby has no annotations and no name-binding imports — constructor
// inference plus repo-unique name resolution carries the cross-file
// case.
func TestRuby_ConstructorInferenceResolvesCrossFile(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"lib/svc.rb": rubySvc,
		"lib/app.rb": `class App
  def main
    s = Svc.new
    s.run
  end
end
`,
	})
	p := NewProvider(RubySpec(), zap.NewNop())
	res, err := p.Enrich(g, dir)
	if err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "main", graph.KindMethod)
	target := nodeByNameKind(t, g, "run", graph.KindMethod)
	e := callEdgeTo(g, caller.ID, target.ID)
	if e == nil {
		t.Fatalf("constructor-inferred call not resolved; edges: %v", g.GetOutEdges(caller.ID))
	}
	assertASTProvenance(t, e, "ruby-types")
	if res.EdgesConfirmed+res.EdgesAdded == 0 {
		t.Errorf("result reported no edge work: %+v", res)
	}
}

// self-qualified calls and `@ivar = Const.new` receivers resolve
// in-class.
func TestRuby_SelfAndIvarReceivers(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"lib/svc.rb": rubySvc,
		"lib/app.rb": `class App
  def initialize
    @worker = Svc.new
  end

  def direct
    self.helper
  end

  def helper
    @worker.run
  end
end
`,
	})
	p := NewProvider(RubySpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	direct := nodeByNameKind(t, g, "direct", graph.KindMethod)
	helper := nodeByNameKind(t, g, "helper", graph.KindMethod)
	if callEdgeTo(g, direct.ID, helper.ID) == nil {
		t.Fatalf("self.helper not resolved; edges: %v", g.GetOutEdges(direct.ID))
	}
	run := nodeByNameKind(t, g, "run", graph.KindMethod)
	if callEdgeTo(g, helper.ID, run.ID) == nil {
		t.Fatalf("@worker.run not resolved through ivar type; edges: %v", g.GetOutEdges(helper.ID))
	}
}

func TestRuby_SuperclassExtendsSynthesis(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"lib/svc.rb": rubySvc,
		"lib/sub.rb": `class Sub < Svc
  def extra
  end
end
`,
	})
	p := NewProvider(RubySpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	sub := nodeByNameKind(t, g, "Sub", graph.KindType)
	svc := nodeByNameKind(t, g, "Svc", graph.KindType)
	e := edgeBetween(g, sub.ID, graph.EdgeExtends, svc.ID)
	if e == nil {
		t.Fatalf("extends edge missing; edges: %v", g.GetOutEdges(sub.ID))
	}
	assertASTProvenance(t, e, "ruby-types")
}

// `include M` mixes a module in — the module indexes as a package
// node, and the engine still grounds the implements edge against it.
func TestRuby_IncludeModuleImplementsSynthesis(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"lib/greeter.rb": `module Greeter
  def greet
  end
end
`,
		"lib/impl.rb": `class Impl
  include Greeter

  def extra
  end
end
`,
	})
	p := NewProvider(RubySpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	impl := nodeByNameKind(t, g, "Impl", graph.KindType)
	mod := nodeByNameKind(t, g, "Greeter", graph.KindPackage)
	e := edgeBetween(g, impl.ID, graph.EdgeImplements, mod.ID)
	if e == nil {
		t.Fatalf("implements edge missing; edges: %v", g.GetOutEdges(impl.ID))
	}
	assertASTProvenance(t, e, "ruby-types")
}

func TestRuby_AmbiguousReceiverStaysUntouched(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"lib/svc.rb": rubySvc,
		"lib/alt.rb": `class Alt
  def run
  end
end
`,
		"lib/app.rb": `class App
  def main
    s = Svc.new
    s = Alt.new
    s.run
  end
end
`,
	})
	p := NewProvider(RubySpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "main", graph.KindMethod)
	assertUntouched(t, g, caller.ID, "run", "ruby-types")
}

// A method provided by an included module resolves to that module's
// declaration: `include` models as an implements edge to the module's
// package node, the module's instance-methods are members of it, and
// the engine climbs the include edge to find the mixed-in method.
func TestRuby_IncludeModuleMethodResolvesThroughMixin(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"lib/greeter.rb": `module Greeter
  def greet
  end
end
`,
		"lib/impl.rb": `class Impl
  include Greeter

  def extra
  end
end
`,
		"lib/app.rb": `class App
  def main
    c = Impl.new
    c.greet
  end
end
`,
	})
	p := NewProvider(RubySpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "main", graph.KindMethod)
	target := nodeByNameKind(t, g, "greet", graph.KindMethod)
	e := callEdgeTo(g, caller.ID, target.ID)
	if e == nil {
		t.Fatalf("mixin method greet not resolved through include; edges: %v", g.GetOutEdges(caller.ID))
	}
	assertASTProvenance(t, e, "ruby-types")
}

// When the same method name is contributed by two distinct mixins, both
// of which the engine can genuinely resolve, it cannot disambiguate and
// leaves the call unresolved rather than picking one arbitrarily.
func TestRuby_AmbiguousMixinMethodStaysUntouched(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"lib/m1.rb": `module M1
  def shared
  end
end
`,
		"lib/m2.rb": `module M2
  def shared
  end
end
`,
		"lib/impl.rb": `class Impl
  include M1
  include M2
end
`,
		"lib/app.rb": `class App
  def main
    c = Impl.new
    c.shared
  end
end
`,
	})
	p := NewProvider(RubySpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	// Both M1#shared and M2#shared are resolvable members, so the call
	// is genuinely ambiguous and must be skipped.
	m1 := nodeByNameKind(t, g, "M1", graph.KindPackage)
	if mem := memberMethod(g, m1.ID, "shared"); mem == nil {
		t.Fatalf("precondition: M1#shared not a member of M1")
	}
	m2 := nodeByNameKind(t, g, "M2", graph.KindPackage)
	if mem := memberMethod(g, m2.ID, "shared"); mem == nil {
		t.Fatalf("precondition: M2#shared not a member of M2")
	}
	caller := nodeByNameKind(t, g, "main", graph.KindMethod)
	assertUntouched(t, g, caller.ID, "shared", "ruby-types")
}

// A spec that leaves InheritEdgeKinds empty climbs only the extends
// chain — the legacy default every non-Ruby language relies on.
func TestLangSpec_InheritEdgeKindsDefault(t *testing.T) {
	var s LangSpec
	got := s.inheritEdgeKinds()
	if len(got) != 1 || got[0] != graph.EdgeExtends {
		t.Fatalf("default inheritEdgeKinds = %v, want [%v]", got, graph.EdgeExtends)
	}
	s.InheritEdgeKinds = []graph.EdgeKind{graph.EdgeExtends, graph.EdgeImplements}
	got = s.inheritEdgeKinds()
	if len(got) != 2 || got[0] != graph.EdgeExtends || got[1] != graph.EdgeImplements {
		t.Fatalf("explicit inheritEdgeKinds = %v, want [%v %v]", got, graph.EdgeExtends, graph.EdgeImplements)
	}
}

// memberMethod returns the method named name that is a member (via
// EdgeMemberOf) of ownerID, or nil.
func memberMethod(g *graph.Graph, ownerID, name string) *graph.Node {
	for _, e := range g.GetInEdges(ownerID) {
		if e.Kind != graph.EdgeMemberOf {
			continue
		}
		n := g.GetNode(e.From)
		if n != nil && n.Kind == graph.KindMethod && n.Name == name {
			return n
		}
	}
	return nil
}
