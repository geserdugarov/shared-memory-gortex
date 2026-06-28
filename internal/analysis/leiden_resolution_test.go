package analysis

import (
	"fmt"
	"sort"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// buildTieredGraph builds a graph with a clear two-level hierarchy so
// the Leiden resolution knob (γ) has something to expose:
//
//   - nm modules, each holding cpm tight cliques of cs nodes;
//   - every intra-clique pair is a strong EdgeCalls edge;
//   - clique hubs inside a module are stitched together with `within`
//     EdgeReferences edges (so a module is cohesive but looser than a
//     clique);
//   - modules form a ring joined by `inter` EdgeCalls edges (the
//     weakest scale).
//
// Sweeping γ walks the hierarchy: low γ merges modules into a few
// blobs, the default γ = 1.0 lands on the per-module scale, and high γ
// fragments modules down to their individual cliques.
func buildTieredGraph(nm, cpm, cs, within, inter int) *graph.Graph {
	g := graph.New()
	add := func(id string) {
		g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction, FilePath: id + ".go"})
	}
	edge := func(from, to string, k graph.EdgeKind) {
		g.AddEdge(&graph.Edge{From: from, To: to, Kind: k})
	}
	node := func(m, c, i int) string { return fmt.Sprintf("m%d_c%d_n%d", m, c, i) }

	for m := 0; m < nm; m++ {
		for c := 0; c < cpm; c++ {
			for i := 0; i < cs; i++ {
				add(node(m, c, i))
			}
			for i := 0; i < cs; i++ {
				for j := i + 1; j < cs; j++ {
					edge(node(m, c, i), node(m, c, j), graph.EdgeCalls)
				}
			}
		}
		// within-module bridges across distinct clique-hub pairs.
		placed := 0
		for c := 0; c < cpm && placed < within; c++ {
			for d := c + 1; d < cpm && placed < within; d++ {
				edge(node(m, c, 0), node(m, d, 0), graph.EdgeReferences)
				placed++
			}
		}
	}
	// inter-module ring; `inter` distinct node pairs per adjacency.
	for m := 0; m < nm; m++ {
		next := (m + 1) % nm
		for k := 0; k < inter; k++ {
			edge(node(m, 0, k%cs), node(next, 0, k%cs), graph.EdgeCalls)
		}
	}
	return g
}

// partitionStats summarises a CommunityResult: number of communities,
// the largest community size, and the mean community size.
func partitionStats(cr *CommunityResult) (count, maxSize int, avgSize float64) {
	count = len(cr.Communities)
	total := 0
	for _, c := range cr.Communities {
		if c.Size > maxSize {
			maxSize = c.Size
		}
		total += c.Size
	}
	if count > 0 {
		avgSize = float64(total) / float64(count)
	}
	return
}

// TestLeidenResolutionGradient is the acceptance test for the γ knob.
// γ = 2.0 must yield MORE and (on average) smaller communities than the
// default; γ = 0.5 must yield FEWER and (on average) larger ones.
func TestLeidenResolutionGradient(t *testing.T) {
	g := buildTieredGraph(4, 3, 4, 3, 2)

	def := DetectCommunitiesLeiden(g)
	hi := DetectCommunitiesLeidenWith(g, LeidenOptions{Resolution: 2.0})
	lo := DetectCommunitiesLeidenWith(g, LeidenOptions{Resolution: 0.5})

	defN, defMax, defAvg := partitionStats(def)
	hiN, hiMax, hiAvg := partitionStats(hi)
	loN, loMax, loAvg := partitionStats(lo)

	t.Logf("gamma=0.5 -> %d communities, maxSize=%d, avgSize=%.2f", loN, loMax, loAvg)
	t.Logf("gamma=1.0 -> %d communities, maxSize=%d, avgSize=%.2f", defN, defMax, defAvg)
	t.Logf("gamma=2.0 -> %d communities, maxSize=%d, avgSize=%.2f", hiN, hiMax, hiAvg)

	// Higher resolution -> more, smaller communities.
	if hiN <= defN {
		t.Errorf("gamma=2.0 should produce MORE communities than default: got %d vs %d", hiN, defN)
	}
	if hiAvg >= defAvg {
		t.Errorf("gamma=2.0 should produce smaller communities than default: avg %.2f vs %.2f", hiAvg, defAvg)
	}
	// Lower resolution -> fewer, larger communities.
	if loN >= defN {
		t.Errorf("gamma=0.5 should produce FEWER communities than default: got %d vs %d", loN, defN)
	}
	if loAvg <= defAvg {
		t.Errorf("gamma=0.5 should produce larger communities than default: avg %.2f vs %.2f", loAvg, defAvg)
	}
}

// nodeToCommSignature renders NodeToComm as a stable, comparable string
// so two partitions can be checked for exact equality.
func nodeToCommSignature(cr *CommunityResult) string {
	ids := make([]string, 0, len(cr.NodeToComm))
	for id := range cr.NodeToComm {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b []byte
	for _, id := range ids {
		b = append(b, id...)
		b = append(b, '=')
		b = append(b, cr.NodeToComm[id]...)
		b = append(b, ';')
	}
	return string(b)
}

// asymResolutionGraph builds three differently-sized, asymmetrically
// bridged clusters so the modularity optimum is unique — the full
// Leiden path breaks exact gain ties by map-iteration order, so a
// byte-identical assertion can only be made on a graph with no
// symmetric ties (where the partition is the same on every run).
func asymResolutionGraph() *graph.Graph {
	g := graph.New()
	add := func(id string) {
		g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction, FilePath: id + ".go"})
	}
	e := func(from, to string, k graph.EdgeKind) { g.AddEdge(&graph.Edge{From: from, To: to, Kind: k}) }
	clusters := [][]string{
		{"a1", "a2", "a3", "a4", "a5"},
		{"b1", "b2", "b3", "b4"},
		{"c1", "c2", "c3"},
	}
	for _, ids := range clusters {
		for _, id := range ids {
			add(id)
		}
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				e(ids[i], ids[j], graph.EdgeCalls)
			}
		}
	}
	e("a1", "b1", graph.EdgeReferences)
	e("b2", "c1", graph.EdgeImports)
	e("a3", "c2", graph.EdgeImports)
	return g
}

// TestLeidenResolutionDefaultByteIdentical proves the γ knob is a true
// no-op at its default: the historical entry point, an explicit
// γ = 1.0, the zero-value options (normalised to 1.0), and
// defaultLeidenOptions() must all produce byte-identical partitions —
// multiplying the null-model penalty by exactly 1.0 is the IEEE-754
// identity, so the default path is unchanged from the pre-resolution
// implementation. Both fixtures have a unique modularity optimum, so
// the full Leiden path is deterministic on them.
func TestLeidenResolutionDefaultByteIdentical(t *testing.T) {
	graphs := map[string]*graph.Graph{
		"toy":  buildTestGraph(),
		"asym": asymResolutionGraph(),
	}
	for name, g := range graphs {
		t.Run(name, func(t *testing.T) {
			base := nodeToCommSignature(DetectCommunitiesLeiden(g))
			explicit := nodeToCommSignature(DetectCommunitiesLeidenWith(g, LeidenOptions{Resolution: 1.0}))
			zero := nodeToCommSignature(DetectCommunitiesLeidenWith(g, LeidenOptions{}))
			defOpts := nodeToCommSignature(DetectCommunitiesLeidenWith(g, defaultLeidenOptions()))

			if base != explicit {
				t.Errorf("default differs from explicit gamma=1.0:\n base=%s\n  1.0=%s", base, explicit)
			}
			if base != zero {
				t.Errorf("default differs from zero-value options (should normalise to 1.0):\n base=%s\n zero=%s", base, zero)
			}
			if base != defOpts {
				t.Errorf("default differs from defaultLeidenOptions():\n base=%s\n  def=%s", base, defOpts)
			}
		})
	}
}
