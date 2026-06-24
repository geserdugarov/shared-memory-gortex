package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func djangoClassHint(nodes []*graph.Node, class string) string {
	for _, n := range nodes {
		if n.Kind == graph.KindType && n.Name == class && n.Meta != nil {
			s, _ := n.Meta["django_iterable_class"].(string)
			return s
		}
	}
	return ""
}

func TestDjango_IterableClassAssignmentHint(t *testing.T) {
	src := `class ModelIterable:
    def __iter__(self):
        yield 1

class QuerySet:
    def __init__(self):
        self._iterable_class = ModelIterable
    def iterator(self):
        for x in self._iterable_class(self):
            yield x
`
	res, err := NewPythonExtractor().Extract("models.py", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if h := djangoClassHint(res.Nodes, "QuerySet"); h != "ModelIterable" {
		t.Errorf("QuerySet django_iterable_class hint = %q (want ModelIterable)", h)
	}
}

func TestDjango_ClassLevelIterableClass(t *testing.T) {
	// The class-level form `_iterable_class = X` is also recognised.
	src := `class CustomIterable:
    def __iter__(self):
        yield 1

class CustomQS:
    _iterable_class = CustomIterable
`
	res, err := NewPythonExtractor().Extract("qs.py", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if h := djangoClassHint(res.Nodes, "CustomQS"); h != "CustomIterable" {
		t.Errorf("CustomQS hint = %q (want CustomIterable)", h)
	}
}

func TestDjango_UnrelatedAssignmentNoHint(t *testing.T) {
	src := `class Plain:
    def __init__(self):
        self._other = 1
`
	res, err := NewPythonExtractor().Extract("p.py", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if h := djangoClassHint(res.Nodes, "Plain"); h != "" {
		t.Errorf("Plain must have no iterable-class hint, got %q", h)
	}
}
