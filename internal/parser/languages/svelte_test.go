package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestSvelteExtractor(t *testing.T) {
	const svelte = `<script lang="ts">
  import { onMount } from 'svelte'
  let count = 0
  function increment() { count += 1 }
</script>

<button on:click={increment}>{count}</button>
`
	res, err := NewSvelteExtractor().Extract("Counter.svelte", []byte(svelte))
	if err != nil {
		t.Fatal(err)
	}

	var comp, incr *graph.Node
	for _, n := range res.Nodes {
		if n.Kind == graph.KindType && n.Name == "Counter" {
			comp = n
		}
		if n.Name == "increment" {
			incr = n
		}
	}
	if comp == nil {
		t.Fatalf("no component node 'Counter' among %d nodes", len(res.Nodes))
	}
	if comp.Meta["exported"] != true || comp.Language != "svelte" {
		t.Errorf("component meta/lang = %v / %q", comp.Meta, comp.Language)
	}
	if incr == nil {
		t.Fatalf("delegated function 'increment' was not extracted from <script>")
	}
	if incr.Language != "svelte" || incr.Meta["inline_script"] != true {
		t.Errorf("delegated symbol lang=%q meta=%v, want svelte + inline_script", incr.Language, incr.Meta)
	}
	if incr.StartLine != 4 {
		t.Errorf("increment StartLine = %d, want 4 (host-file coordinates)", incr.StartLine)
	}
}
