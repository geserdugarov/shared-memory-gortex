// Command store-bench compares the three graph.Store implementations
// (in-memory, bbolt-on-disk, SQLite-on-disk) on equivalent workloads.
//
// Procedure:
//
//  1. Index the target repo once with the in-memory indexer to build a
//     reference graph.Graph. This becomes the "ground truth" data set
//     every backend gets loaded with.
//  2. For each backend: open a fresh store, bulk-load it from the
//     reference graph via AddBatch (timed), measure on-disk size,
//     run a fixed query workload (point lookups + adjacency walks +
//     name searches), measure p50/p95 latencies, sample heap RSS.
//  3. Print a comparison table.
//
// The reference-graph step uses the in-memory store as the source of
// truth so all backends benchmark against identical data. The bench
// measures the Store interface itself, not end-to-end indexing through
// each backend (that comes later, once the indexer is refactored to
// take graph.Store rather than *graph.Graph).
package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_bolt"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/progress"
)

// stageReporter mirrors bench/perf-profile's progress sink so we get
// visibility into where the indexer is spending time on the reference
// build (and also confirms the indexer is doing real work).
type stageReporter struct {
	start time.Time
	last  string
}

func (s *stageReporter) Report(stage string, cur, total int) {
	if stage == s.last && (cur == 0 || (cur != total && cur%5000 != 0)) {
		return
	}
	s.last = stage
	if cur == 0 && total == 0 {
		fmt.Fprintf(os.Stderr, "  [%6.2fs] %s\n", time.Since(s.start).Seconds(), stage)
		return
	}
	fmt.Fprintf(os.Stderr, "  [%6.2fs] %s %d/%d\n", time.Since(s.start).Seconds(), stage, cur, total)
}

type benchResult struct {
	Backend     string
	NodeCount   int
	EdgeCount   int
	LoadMs      float64 // AddBatch(refNodes, refEdges) wall time
	DiskBytes   int64   // on-disk size after load (0 for in-memory)
	QueryP50us  float64 // microseconds for clarity at sub-ms latencies
	QueryP95us  float64
	HeapMB      float64 // process heap after a forced GC
	IndexBuilt  bool    // true when load completed
	Err         string
}

type queryWorkload struct {
	nodeIDs     []string // for GetNode
	outIDs      []string // for GetOutEdges
	inIDs       []string // for GetInEdges
	names       []string // for FindNodesByName
	filePaths   []string // for GetFileNodes
}

func main() {
	root := flag.String("root", "", "repo root to index (required)")
	workers := flag.Int("workers", runtime.NumCPU(), "indexer parallelism for reference graph")
	querySize := flag.Int("queries", 1000, "number of point/adjacency queries per backend")
	skipMemory := flag.Bool("skip-memory", false, "skip the in-memory baseline")
	skipBolt := flag.Bool("skip-bolt", false, "skip the bbolt backend")
	skipSQLite := flag.Bool("skip-sqlite", false, "skip the sqlite backend")
	flag.Parse()
	if *root == "" {
		die("usage: store-bench -root <path>")
	}

	// Build reference graph in memory.
	fmt.Fprintln(os.Stderr, "[step 1] indexing reference graph...")
	t0 := time.Now()
	refGraph, refStats, err := buildReferenceGraph(*root, *workers)
	if err != nil {
		die("reference index: %v", err)
	}
	fmt.Fprintf(os.Stderr, "  reference graph: %d nodes, %d edges, indexed in %.2fs\n",
		refStats.nodeCount, refStats.edgeCount, time.Since(t0).Seconds())

	// Pick a deterministic-ish query workload from the reference graph.
	workload := pickQueries(refGraph, *querySize)
	fmt.Fprintf(os.Stderr, "  workload: %d point lookups, %d adjacency walks, %d name searches, %d file scans\n",
		len(workload.nodeIDs), len(workload.outIDs)+len(workload.inIDs), len(workload.names), len(workload.filePaths))

	// Run each backend.
	var results []benchResult

	if !*skipMemory {
		fmt.Fprintln(os.Stderr, "[step 2a] benching in-memory backend...")
		results = append(results, benchBackend("memory", refGraph, workload, func() (graph.Store, func() int64, error) {
			return graph.New(), func() int64 { return 0 }, nil
		}))
	}

	if !*skipBolt {
		fmt.Fprintln(os.Stderr, "[step 2b] benching bbolt backend...")
		results = append(results, benchBackend("bbolt", refGraph, workload, func() (graph.Store, func() int64, error) {
			dir, err := os.MkdirTemp("", "store-bench-bolt-*")
			if err != nil {
				return nil, nil, err
			}
			path := filepath.Join(dir, "store.db")
			s, err := store_bolt.Open(path)
			if err != nil {
				os.RemoveAll(dir)
				return nil, nil, err
			}
			diskFn := func() int64 {
				_ = s.Close()
				return fileSize(path)
			}
			return s, diskFn, nil
		}))
	}

	if !*skipSQLite {
		fmt.Fprintln(os.Stderr, "[step 2c] benching sqlite backend...")
		results = append(results, benchBackend("sqlite", refGraph, workload, func() (graph.Store, func() int64, error) {
			dir, err := os.MkdirTemp("", "store-bench-sqlite-*")
			if err != nil {
				return nil, nil, err
			}
			path := filepath.Join(dir, "store.sqlite")
			s, err := store_sqlite.Open(path)
			if err != nil {
				os.RemoveAll(dir)
				return nil, nil, err
			}
			diskFn := func() int64 {
				_ = s.Close()
				// SQLite WAL mode keeps a -wal companion file; count both
				// so the reported size matches what an operator would see
				// in their data dir.
				return fileSize(path) + fileSize(path+"-wal") + fileSize(path+"-shm")
			}
			return s, diskFn, nil
		}))
	}

	// Print table.
	printTable(os.Stdout, results)
}

// -- reference graph build --------------------------------------------------

type refStats struct {
	nodeCount int
	edgeCount int
}

func buildReferenceGraph(root string, workers int) (*graph.Graph, refStats, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, refStats{}, fmt.Errorf("abs: %w", err)
	}
	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Config{}
	cfg.Index.Workers = workers
	idx := indexer.New(g, reg, cfg.Index, zap.NewNop())
	rep := &stageReporter{start: time.Now()}
	ctx := progress.WithReporter(context.Background(), rep)
	res, err := idx.IndexCtx(ctx, absRoot)
	if err != nil {
		return nil, refStats{}, err
	}
	if res != nil && len(res.Errors) > 0 {
		fmt.Fprintf(os.Stderr, "  indexer reported %d errors; first: %v\n", len(res.Errors), res.Errors[0])
	}
	// Cross-check the result against the live graph — they should agree;
	// disagreement is a smoke signal we want to see immediately.
	if g.NodeCount() == 0 && res != nil && res.NodeCount > 0 {
		fmt.Fprintf(os.Stderr, "  WARNING: result reports %d nodes but graph is empty\n", res.NodeCount)
	}
	return g, refStats{nodeCount: g.NodeCount(), edgeCount: g.EdgeCount()}, nil
}

// -- workload sampling ------------------------------------------------------

func pickQueries(g *graph.Graph, n int) queryWorkload {
	nodes := g.AllNodes()
	if len(nodes) == 0 {
		return queryWorkload{}
	}
	// Sort for deterministic pre-shuffle order; then a crypto/rand-seeded
	// pick gives reproducible workloads across runs of the same graph.
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	pickN := func(count int) []*graph.Node {
		if count >= len(nodes) {
			out := make([]*graph.Node, len(nodes))
			copy(out, nodes)
			return out
		}
		out := make([]*graph.Node, 0, count)
		seen := make(map[int]bool, count)
		for len(out) < count {
			var b [4]byte
			_, _ = rand.Read(b[:])
			i := int(binary.BigEndian.Uint32(b[:])) % len(nodes)
			if seen[i] {
				continue
			}
			seen[i] = true
			out = append(out, nodes[i])
		}
		return out
	}

	sampleNodes := pickN(n)
	wl := queryWorkload{
		nodeIDs: make([]string, 0, n),
		outIDs:  make([]string, 0, n/2),
		inIDs:   make([]string, 0, n/2),
		names:   nil,
		filePaths: nil,
	}
	nameSet := map[string]struct{}{}
	fileSet := map[string]struct{}{}
	for i, n := range sampleNodes {
		wl.nodeIDs = append(wl.nodeIDs, n.ID)
		if i%2 == 0 {
			wl.outIDs = append(wl.outIDs, n.ID)
		} else {
			wl.inIDs = append(wl.inIDs, n.ID)
		}
		nameSet[n.Name] = struct{}{}
		if n.FilePath != "" {
			fileSet[n.FilePath] = struct{}{}
		}
	}
	for k := range nameSet {
		wl.names = append(wl.names, k)
	}
	for k := range fileSet {
		wl.filePaths = append(wl.filePaths, k)
	}
	// Cap names and files at the per-backend query budget so they don't
	// dominate latency totals on graphs with many distinct names/files.
	if len(wl.names) > n/4 {
		wl.names = wl.names[:n/4]
	}
	if len(wl.filePaths) > n/4 {
		wl.filePaths = wl.filePaths[:n/4]
	}
	return wl
}

// -- per-backend run --------------------------------------------------------

func benchBackend(
	name string,
	ref *graph.Graph,
	wl queryWorkload,
	factory func() (graph.Store, func() int64, error),
) benchResult {
	r := benchResult{Backend: name}

	s, diskFn, err := factory()
	if err != nil {
		r.Err = "factory: " + err.Error()
		return r
	}

	refNodes := ref.AllNodes()
	refEdges := ref.AllEdges()

	// Load: time the bulk insert. Mirrors how a daemon would restore
	// a snapshot or initial-populate a fresh store on startup.
	t0 := time.Now()
	s.AddBatch(refNodes, refEdges)
	r.LoadMs = msSince(t0)
	r.NodeCount = s.NodeCount()
	r.EdgeCount = s.EdgeCount()
	r.IndexBuilt = true

	// Query latencies. Mixed workload: point lookups, adjacency walks,
	// name searches, file-node scans. One total slice per backend; the
	// global p50/p95 covers the mix.
	latencies := make([]time.Duration, 0,
		len(wl.nodeIDs)+len(wl.outIDs)+len(wl.inIDs)+len(wl.names)+len(wl.filePaths))

	for _, id := range wl.nodeIDs {
		t := time.Now()
		_ = s.GetNode(id)
		latencies = append(latencies, time.Since(t))
	}
	for _, id := range wl.outIDs {
		t := time.Now()
		_ = s.GetOutEdges(id)
		latencies = append(latencies, time.Since(t))
	}
	for _, id := range wl.inIDs {
		t := time.Now()
		_ = s.GetInEdges(id)
		latencies = append(latencies, time.Since(t))
	}
	for _, n := range wl.names {
		t := time.Now()
		_ = s.FindNodesByName(n)
		latencies = append(latencies, time.Since(t))
	}
	for _, fp := range wl.filePaths {
		t := time.Now()
		_ = s.GetFileNodes(fp)
		latencies = append(latencies, time.Since(t))
	}
	r.QueryP50us = pctUs(latencies, 50)
	r.QueryP95us = pctUs(latencies, 95)

	// Sample heap. Force GC first so the figure reflects retained state
	// rather than allocation churn from the query loop.
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	r.HeapMB = float64(m.HeapInuse) / 1e6

	// Disk size — diskFn closes the store and returns size in bytes.
	// In-memory backend returns 0.
	r.DiskBytes = diskFn()

	return r
}

// -- output -----------------------------------------------------------------

func printTable(w *os.File, rows []benchResult) {
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "# Store backend comparison")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "| backend | nodes | edges | load | disk size | heap | query p50 | query p95 |")
	fmt.Fprintln(w, "|---------|------:|------:|-----:|----------:|-----:|----------:|----------:|")
	for _, r := range rows {
		if r.Err != "" {
			fmt.Fprintf(w, "| %s | — | — | — | — | — | — | %s |\n", r.Backend, r.Err)
			continue
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			r.Backend,
			fmtInt(r.NodeCount),
			fmtInt(r.EdgeCount),
			fmtMs(r.LoadMs),
			fmtBytes(r.DiskBytes),
			fmtMB(r.HeapMB),
			fmtUs(r.QueryP50us),
			fmtUs(r.QueryP95us),
		)
	}
	fmt.Fprintln(w, "")
}

// -- small helpers ----------------------------------------------------------

func msSince(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000.0 }

func pctMs(samples []time.Duration, pct int) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := (len(sorted) * pct) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return float64(sorted[idx].Microseconds()) / 1000.0
}

func pctUs(samples []time.Duration, pct int) float64 {
	return pctMs(samples, pct) * 1000.0
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

func fmtInt(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func fmtMs(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.2fs", ms/1000)
	}
	return fmt.Sprintf("%.1fms", ms)
}

func fmtUs(us float64) string {
	if us >= 1000 {
		return fmt.Sprintf("%.2fms", us/1000)
	}
	return fmt.Sprintf("%.1fµs", us)
}

func fmtMB(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.2fGB", mb/1024)
	}
	return fmt.Sprintf("%.0fMB", mb)
}

func fmtBytes(b int64) string {
	const (
		KB = 1 << 10
		MB = 1 << 20
		GB = 1 << 30
	)
	switch {
	case b == 0:
		return "—"
	case b >= GB:
		return fmt.Sprintf("%.2fGB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1fMB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1fKB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

func die(format string, args ...any) {
	fmt.Fprintln(os.Stderr, fmt.Sprintf(format, args...))
	os.Exit(1)
}
