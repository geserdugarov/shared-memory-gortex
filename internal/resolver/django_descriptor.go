package resolver

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// DjangoDescriptorResolver is a claiming resolver for Django's named
// descriptor dispatch — an attribute reference the static graph cannot
// resolve because it names a runtime descriptor, not a declared method.
// The flagship case is `self._iterable_class(self)` inside a QuerySet:
// `_iterable_class` is a class attribute (default `ModelIterable`), and
// iterating its instance runs `ModelIterable.__iter__`. This resolver
// claims those residual `_iterable_class` references and binds them to the
// iterable class's `__iter__`, keyed by class names present in the graph.
type DjangoDescriptorResolver struct{}

// djangoDescriptorVocab is the set of Django descriptor attribute names this
// resolver claims. Kept tight so the pre-filter only sees its own framework
// vocabulary.
var djangoDescriptorVocab = map[string]bool{
	"_iterable_class": true,
}

// djangoDefaultIterableClass is Django's default QuerySet._iterable_class.
const djangoDefaultIterableClass = "ModelIterable"

func (DjangoDescriptorResolver) Name() string { return SynthDjangoDescriptor }

// Claims reports whether the edge references a Django descriptor name.
func (DjangoDescriptorResolver) Claims(e *graph.Edge) bool {
	if e == nil {
		return false
	}
	return djangoDescriptorVocab[djangoRefName(e.To)]
}

// Resolve rebinds a claimed `_iterable_class` reference to the iterable
// class's `__iter__` method — the class named by the QuerySet's
// django_iterable_class hint, else Django's default ModelIterable.
func (DjangoDescriptorResolver) Resolve(g graph.Store, e *graph.Edge) bool {
	if g == nil || e == nil || djangoRefName(e.To) != "_iterable_class" {
		return false
	}
	iterableClass := djangoIterableClassFor(g, e.From)
	if iterableClass == "" {
		iterableClass = djangoDefaultIterableClass
	}
	target := djangoFindIterMethod(g, iterableClass)
	if target == nil {
		return false
	}
	oldTo := e.To
	e.To = target.ID
	e.Origin = graph.OriginASTInferred
	e.Confidence = 0.7
	e.ConfidenceLabel = graph.ConfidenceLabelFor(graph.EdgeCalls, 0.7)
	StampSynthesized(e, SynthDjangoDescriptor)
	g.ReindexEdges([]graph.EdgeReindex{{Edge: e, OldTo: oldTo}})
	return true
}

// djangoRefName extracts the bare attribute name from an unresolved target
// id, stripping the `unresolved::` prefix and any `*.` method marker.
func djangoRefName(to string) string {
	if !graph.IsUnresolvedTarget(to) {
		return ""
	}
	return strings.TrimPrefix(graph.UnresolvedName(to), "*.")
}

// djangoIterableClassFor returns the iterable-class name hinted on the class
// enclosing the reference's source method, or "".
func djangoIterableClassFor(g graph.Store, fromID string) string {
	n := g.GetNode(fromID)
	if n == nil || n.Meta == nil {
		return ""
	}
	recv, _ := n.Meta["receiver"].(string)
	if recv == "" {
		return ""
	}
	for _, c := range g.FindNodesByName(recv) {
		if c == nil || c.Kind != graph.KindType || c.Meta == nil {
			continue
		}
		if ic, _ := c.Meta["django_iterable_class"].(string); ic != "" {
			return ic
		}
	}
	return ""
}

// djangoFindIterMethod returns the `__iter__` method of the named class.
func djangoFindIterMethod(g graph.Store, className string) *graph.Node {
	for _, n := range g.FindNodesByName("__iter__") {
		if n == nil || n.Kind != graph.KindMethod || n.Meta == nil {
			continue
		}
		if recv, _ := n.Meta["receiver"].(string); recv == className {
			return n
		}
	}
	return nil
}
