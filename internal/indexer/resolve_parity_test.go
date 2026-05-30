package indexer_test

// Resolver differential: the ladybug backend must be NO WORSE than the
// in-memory backend at resolving call edges through the multi-repo
// prefixed-stub form.
//
// The bug this guards: in multi-repo mode copyBulkLocked rewrites
// unresolved stubs to `<repoPrefix>::unresolved::<name>` (so per-repo
// stubs don't collide on the COPY primary key). The Go worker-pool
// resolver drains store.EdgesWithUnresolvedTarget(); if that scan only
// matches the bare `unresolved::` form it silently skips every
// multi-repo stub, the callee never gets a Calls/References edge, and
// every such function is reported dead by analyze kind=dead_code.
//
// We exercise the REAL surfaces — the Go tree-sitter extractor, the
// real copyBulkLocked prefixing (triggered by RepoPrefix-stamped
// nodes), and the real resolver.ResolveAll — but replay the extraction
// directly so a single COPY into an empty table reproduces the prefixed
// form without tripping the separate multi-repo COPY-into-non-empty
// limitation. (The full multi-repo indexer pipeline against a live
// ladybug store is validated separately by the live cold-load.)
//
// The invariant is intentionally directional — NOT strict parity.
// In-memory is the lax backend and is not the source of truth; ladybug
// may legitimately be stricter/better. So the assertion is:
//
//	{functions ladybug reports dead} ⊆ {functions memory reports dead}
//
// BulkOff forces the Go-pool-only path (GORTEX_BACKEND_RESOLVER=0) so
// resolution depends solely on EdgesWithUnresolvedTarget + the Go
// resolver — the cleanest exercise of the prefixed-stub scan. BulkOn is
// the production config (Cypher ResolveAllBulk + Go pool).

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_ladybug"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/resolver"
)

const parityRepoPrefix = "repo-a"

// parityFixtureFiles exercises every call-site shape the case
// enumeration found in the dead_code false-positive set: each callee is
// package-private and referenced exactly once, so a dropped call edge
// makes it look dead. All of them MUST resolve.
var parityFixtureFiles = map[string]string{
	"app.go": `package main

import "fmt"

func runIt(mode string) {
	body := renderJSON(mode) // assign_single :=
	fmt.Println(body)
	switch mode {
	case "a":
		x := computeIt(mode) // assign_single := inside switch/case
		fmt.Println(x)
	case "b":
		g, h, err := openThing(mode) // assign_multi := inside switch/case
		fmt.Println(g, h, err)
	}
	fmt.Println(humanize(len(mode))) // nested arg
	emitBanner(mode)                 // bare statement call
	if e := checkErr(mode); e != nil { // if-init
		fmt.Println(e)
	}
}

func renderJSON(m string) string           { return m }
func computeIt(m string) int               { return len(m) }
func openThing(m string) (int, int, error) { return 0, 0, nil }
func humanize(n int) string                { return fmt.Sprint(n) }
func emitBanner(m string)                  {}
func checkErr(m string) error              { return nil }
`,
	"caller.go": `package main

func driver() {
	runIt("a") // cross-file statement call
}
`,
}

// callees referenced exactly once that must never be reported dead.
// driver is the fixture root (calls runIt, itself uncalled) — genuinely
// dead in both backends by design, so it is intentionally excluded.
var parityCallees = []string{
	"runIt", "renderJSON", "computeIt", "openThing",
	"humanize", "emitBanner", "checkErr",
}

// extractFixture runs the real Go extractor over every fixture file and
// returns the merged nodes/edges with RepoPrefix stamped on every node
// — the shape a per-repo Indexer hands the store in multi-repo mode.
func extractFixture(t *testing.T) (nodes []*graph.Node, edges []*graph.Edge) {
	t.Helper()
	ext := languages.NewGoExtractor()
	// Deterministic file order so the two backends see identical input.
	paths := make([]string, 0, len(parityFixtureFiles))
	for p := range parityFixtureFiles {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		res, err := ext.Extract(p, []byte(parityFixtureFiles[p]))
		require.NoErrorf(t, err, "extract %s", p)
		for _, n := range res.Nodes {
			if n != nil {
				n.RepoPrefix = parityRepoPrefix
			}
		}
		nodes = append(nodes, res.Nodes...)
		edges = append(edges, res.Edges...)
	}
	return nodes, edges
}

// deadFunctions loads the extracted fixture into store, runs the full
// resolve, and returns the set of function names with NO incoming usage
// edge (Calls/References/MemberOf/Instantiates) — the exact predicate
// analyze kind=dead_code applies to KindFunction. loadBulk selects the
// ladybug COPY/prefix path (true) vs a plain in-memory AddBatch (false).
func deadFunctions(t *testing.T, store graph.Store, nodes []*graph.Node, edges []*graph.Edge, loadBulk bool) map[string]bool {
	t.Helper()
	if loadBulk {
		// Drive the real bulk path so copyBulkLocked applies the
		// `<repoPrefix>::unresolved::` rewrite + auto-stubs the targets.
		type bulkLoader interface {
			BeginBulkLoad()
			FlushBulk() error
		}
		bl, ok := store.(bulkLoader)
		require.True(t, ok, "ladybug store must implement BeginBulkLoad/FlushBulk")
		bl.BeginBulkLoad()
		store.AddBatch(nodes, edges)
		require.NoError(t, bl.FlushBulk())
	} else {
		store.AddBatch(nodes, edges)
	}

	resolver.New(store).ResolveAll()

	counting := map[graph.EdgeKind]bool{
		graph.EdgeCalls:        true,
		graph.EdgeReferences:   true,
		graph.EdgeMemberOf:     true,
		graph.EdgeInstantiates: true,
	}
	dead := map[string]bool{}
	for n := range store.NodesByKind(graph.KindFunction) {
		if n == nil || n.Name == "main" {
			continue
		}
		alive := false
		for _, e := range store.GetInEdges(n.ID) {
			if e != nil && counting[e.Kind] {
				alive = true
				break
			}
		}
		if !alive {
			dead[n.Name] = true
		}
	}
	return dead
}

func assertLadybugNotWorseThanMemory(t *testing.T) {
	t.Helper()
	nodes, edges := extractFixture(t)

	memDead := deadFunctions(t, graph.New(), nodes, edges, false)

	// Fresh node/edge copies for the second load: AddBatch/copyBulkLocked
	// mutate edge.To in place (the prefix rewrite), so reuse would taint
	// the second backend with the first's rewritten ids.
	nodes2, edges2 := extractFixture(t)
	lbug, err := store_ladybug.Open(filepath.Join(t.TempDir(), "rp.kuzu"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = lbug.Close() })
	lbugDead := deadFunctions(t, lbug, nodes2, edges2, true)

	// Sanity: the in-memory baseline must resolve every callee. If not,
	// the fixture or parser regressed and the differential is moot.
	for _, name := range parityCallees {
		assert.Falsef(t, memDead[name],
			"in-memory backend reports %q dead — fixture/parser regression, not a backend bug", name)
	}

	// Invariant: ladybug must be no worse than memory.
	var worse []string
	for name := range lbugDead {
		if !memDead[name] {
			worse = append(worse, name)
		}
	}
	sort.Strings(worse)
	assert.Emptyf(t, worse,
		"ladybug reports these functions dead but in-memory resolves them (ladybug worse than memory): %v", worse)
}

// Go-pool-only path: resolution depends entirely on
// EdgesWithUnresolvedTarget + the Go resolver — RED before the
// EdgesWithUnresolvedTarget prefixed-stub fix, GREEN after.
func TestResolveParity_LadybugNotWorseThanMemory_BulkOff(t *testing.T) {
	t.Setenv("GORTEX_BACKEND_RESOLVER", "0")
	assertLadybugNotWorseThanMemory(t)
}

// Production config: Cypher ResolveAllBulk drains most stubs, the Go
// pool mops up the residue.
func TestResolveParity_LadybugNotWorseThanMemory_BulkOn(t *testing.T) {
	t.Setenv("GORTEX_BACKEND_RESOLVER", "1")
	assertLadybugNotWorseThanMemory(t)
}
