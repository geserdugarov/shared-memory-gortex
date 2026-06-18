package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestFlutterSetState_EmitsSetStateCallEdge(t *testing.T) {
	src := `class _CounterState extends State<Counter> {
  void increment() {
    setState(() {});
  }
  Widget build(BuildContext context) {
    return Container();
  }
}
`
	res, err := NewDartExtractor().Extract("counter.dart", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var hasSetStateCall, hasBuildMember bool
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeCalls && e.From == "counter.dart::_CounterState.increment" && len(e.To) >= 9 && e.To[len(e.To)-9:] == ".setState" {
			hasSetStateCall = true
		}
		if e.Kind == graph.EdgeMemberOf && e.From == "counter.dart::_CounterState.build" && e.To == "counter.dart::_CounterState" {
			hasBuildMember = true
		}
	}
	if !hasSetStateCall {
		t.Errorf("expected a setState call edge from increment")
	}
	if !hasBuildMember {
		t.Errorf("expected build member_of _CounterState")
	}
}
