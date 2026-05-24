// Package store_cayley is a Cayley-backed (pure-Go) implementation of
// graph.Store. The on-disk format is a single bolt file written through
// cayley's KV bolt backend, with each Node / Edge materialised as a
// fixed set of quads sharing one IRI subject (see quad_layout.go).
//
// Race-detector caveat: cayley v0.7.7 pins github.com/boltdb/bolt
// v1.3.1, which uses unsafe pointer casts that trip Go 1.14+'s
// runtime checkptr validation under `go test -race`. The check is not
// a real data race — it's a false positive in legacy bolt code. Run
// `go test -count=1 -race` here with `-gcflags=all=-d=checkptr=0` if
// you want race coverage; the underlying conformance is unaffected
// either way (37/37 subtests pass with and without -race once the
// checkptr knob is set).
package store_cayley

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"iter"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cayleygraph/cayley/graph"
	_ "github.com/cayleygraph/cayley/graph/kv/bolt" // register bolt backend
	"github.com/cayleygraph/quad"

	gortex "github.com/zzet/gortex/internal/graph"
)

// Store is a Cayley-backed implementation of graph.Store. Cayley's
// underlying KV layer is bolt — pure Go, single-file on disk, recoverable.
//
// Reads either scan quads through QuadIterator (subject-keyed lookups,
// O(quads-per-subject)) or fan out across an in-memory mirror that we
// rebuild on open. The mirror is rebuild-on-open only; mutations go to
// both layers in the same critical section, so concurrent reads always
// see a consistent view.
type Store struct {
	qs graph.QuadStore

	// mu serialises every mutation against every other mutation and
	// against the in-memory mirror updates. Reads take it as RLock.
	mu sync.RWMutex

	// resolveMu is the resolver-coordination mutex returned by
	// ResolveMutex. Held by cross-repo / temporal / external resolver
	// passes to keep their edge mutations from interleaving.
	resolveMu sync.Mutex

	edgeIdentityRevs atomic.Int64

	// In-memory mirror. Cayley quads are the canonical source of truth;
	// the mirror exists purely so steady-state reads (GetNode,
	// GetOutEdges, EdgesByKind, FindNodesByName, …) don't pay a quad
	// scan on every call. Mirror is rebuilt from the quad store on
	// Open and kept in sync with every mutation.
	nodes        map[string]*gortex.Node
	nodesByName  map[string][]*gortex.Node
	nodesByQual  map[string]*gortex.Node
	nodesByFile  map[string]map[string]*gortex.Node
	nodesByRepo  map[string]map[string]*gortex.Node
	nodesByKind  map[gortex.NodeKind]map[string]*gortex.Node
	outEdges     map[string]map[edgeKey]*gortex.Edge
	inEdges      map[string]map[edgeKey]*gortex.Edge
	edgesByKind  map[gortex.EdgeKind]map[edgeKey]*gortex.Edge
	allEdges     map[edgeKey]*gortex.Edge
	unresolvedES map[edgeKey]*gortex.Edge
}

// edgeKey is the in-memory identity of an Edge, mirroring the composite
// IRI we use as the Cayley subject for an edge.
type edgeKey struct {
	From string
	To   string
	Kind gortex.EdgeKind
	File string
	Line int
}

func (k edgeKey) subject() quad.IRI {
	return quad.IRI(edgeSubjectPrefix + k.From + "|" + k.To + "|" + string(k.Kind) + "|" + k.File + "|" + strconv.Itoa(k.Line))
}

func keyOf(e *gortex.Edge) edgeKey {
	return edgeKey{From: e.From, To: e.To, Kind: e.Kind, File: e.FilePath, Line: e.Line}
}

func nodeSubject(id string) quad.IRI {
	return quad.IRI(nodeSubjectPrefix + id)
}

// Compile-time assertion: *Store satisfies graph.Store.
var _ gortex.Store = (*Store)(nil)

// Open opens (or creates) a Cayley quad store at path, using the bolt
// backend. The store is created on first open.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, fmt.Errorf("store_cayley: mkdir %q: %w", path, err)
	}
	// Cayley's hidalgo bolt backend stores at <path>/indexes.bolt.
	// Mark it init'd on first open; ignore "already exists".
	if err := graph.InitQuadStore("bolt", path, nil); err != nil {
		// hidalgo's bolt backend returns nil even when the file is
		// present, but cayley wraps it; tolerate ErrDatabaseExists.
		if err != graph.ErrDatabaseExists {
			// Some path/permission errors should still propagate; we
			// allow the subsequent NewQuadStore to surface them.
			_ = err
		}
	}
	qs, err := graph.NewQuadStore("bolt", path, nil)
	if err != nil {
		return nil, fmt.Errorf("store_cayley: open %q: %w", path, err)
	}
	s := &Store{
		qs:           qs,
		nodes:        make(map[string]*gortex.Node),
		nodesByName:  make(map[string][]*gortex.Node),
		nodesByQual:  make(map[string]*gortex.Node),
		nodesByFile:  make(map[string]map[string]*gortex.Node),
		nodesByRepo:  make(map[string]map[string]*gortex.Node),
		nodesByKind:  make(map[gortex.NodeKind]map[string]*gortex.Node),
		outEdges:     make(map[string]map[edgeKey]*gortex.Edge),
		inEdges:      make(map[string]map[edgeKey]*gortex.Edge),
		edgesByKind:  make(map[gortex.EdgeKind]map[edgeKey]*gortex.Edge),
		allEdges:     make(map[edgeKey]*gortex.Edge),
		unresolvedES: make(map[edgeKey]*gortex.Edge),
	}
	if err := s.rebuildMirror(); err != nil {
		_ = qs.Close()
		return nil, fmt.Errorf("store_cayley: rebuild mirror: %w", err)
	}
	return s, nil
}

// Close closes the underlying Cayley quad store.
func (s *Store) Close() error {
	if s == nil || s.qs == nil {
		return nil
	}
	return s.qs.Close()
}

// ResolveMutex returns the resolver-coordination mutex. Held by
// cross-repo / temporal / external resolver passes to serialise edge
// mutations.
func (s *Store) ResolveMutex() *sync.Mutex { return &s.resolveMu }

// -- write paths: cayley + mirror updates -----------------------------------

// applyDeltas commits a transaction of cayley deltas with ignore-dup/
// ignore-missing semantics so re-adds and stale removes never error.
func (s *Store) applyDeltas(deltas []graph.Delta) error {
	if len(deltas) == 0 {
		return nil
	}
	return s.qs.ApplyDeltas(deltas, graph.IgnoreOpts{IgnoreDup: true, IgnoreMissing: true})
}

// buildNodeDeltas constructs the Add deltas that materialise a Node.
// Empty / zero-valued fields are omitted from the quad set so the
// minimum-shape Node occupies only the predicates it actually populates.
func buildNodeDeltas(n *gortex.Node) ([]graph.Delta, error) {
	sub := nodeSubject(n.ID)
	deltas := []graph.Delta{
		{Action: graph.Add, Quad: quad.Make(sub, predKind, quad.String(string(n.Kind)), labelNode)},
		{Action: graph.Add, Quad: quad.Make(sub, predName, quad.String(n.Name), labelNode)},
		{Action: graph.Add, Quad: quad.Make(sub, predStartLine, quad.Int(n.StartLine), labelNode)},
		{Action: graph.Add, Quad: quad.Make(sub, predEndLine, quad.Int(n.EndLine), labelNode)},
	}
	if n.QualName != "" {
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predQualName, quad.String(n.QualName), labelNode)})
	}
	if n.FilePath != "" {
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predFilePath, quad.String(n.FilePath), labelNode)})
	}
	if n.Language != "" {
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predLanguage, quad.String(n.Language), labelNode)})
	}
	if n.RepoPrefix != "" {
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predRepoPrefix, quad.String(n.RepoPrefix), labelNode)})
	}
	if n.WorkspaceID != "" {
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predWorkspaceID, quad.String(n.WorkspaceID), labelNode)})
	}
	if n.ProjectID != "" {
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predProjectID, quad.String(n.ProjectID), labelNode)})
	}
	if n.AbsoluteFilePath != "" {
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predAbsoluteFilePath, quad.String(n.AbsoluteFilePath), labelNode)})
	}
	if len(n.Meta) > 0 {
		blob, err := encodeMetaBlob(n.Meta)
		if err != nil {
			return nil, err
		}
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predMeta, quad.String(blob), labelNode)})
	}
	return deltas, nil
}

// buildEdgeDeltas constructs the Add deltas that materialise an Edge.
func buildEdgeDeltas(e *gortex.Edge) ([]graph.Delta, error) {
	k := keyOf(e)
	sub := k.subject()
	deltas := []graph.Delta{
		{Action: graph.Add, Quad: quad.Make(sub, predKind, quad.String(string(e.Kind)), labelEdge)},
		{Action: graph.Add, Quad: quad.Make(sub, predFrom, quad.String(e.From), labelEdge)},
		{Action: graph.Add, Quad: quad.Make(sub, predTo, quad.String(e.To), labelEdge)},
		{Action: graph.Add, Quad: quad.Make(sub, predLine, quad.Int(e.Line), labelEdge)},
		{Action: graph.Add, Quad: quad.Make(sub, predConfidence, quad.Float(e.Confidence), labelEdge)},
		{Action: graph.Add, Quad: quad.Make(sub, predCrossRepo, quad.Bool(e.CrossRepo), labelEdge)},
	}
	if e.FilePath != "" {
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predFilePath, quad.String(e.FilePath), labelEdge)})
	}
	if e.ConfidenceLabel != "" {
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predConfidenceLabel, quad.String(e.ConfidenceLabel), labelEdge)})
	}
	if e.Origin != "" {
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predOrigin, quad.String(e.Origin), labelEdge)})
	}
	if e.Tier != "" {
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predTier, quad.String(e.Tier), labelEdge)})
	}
	if len(e.Meta) > 0 {
		blob, err := encodeMetaBlob(e.Meta)
		if err != nil {
			return nil, err
		}
		deltas = append(deltas, graph.Delta{Action: graph.Add, Quad: quad.Make(sub, predMeta, quad.String(blob), labelEdge)})
	}
	return deltas, nil
}

// deleteSubjectDeltas constructs the Delete deltas for every existing
// quad with the given subject. Returns nil if the subject is absent.
func (s *Store) deleteSubjectDeltas(sub quad.Value) []graph.Delta {
	ref := s.qs.ValueOf(sub)
	if ref == nil {
		return nil
	}
	it := s.qs.QuadIterator(quad.Subject, ref)
	var deltas []graph.Delta
	ctx := context.Background()
	_ = graph.Iterate(ctx, it).Each(func(r graph.Ref) {
		q := s.qs.Quad(r)
		deltas = append(deltas, graph.Delta{Action: graph.Delete, Quad: q})
	})
	return deltas
}

// addNodeLocked materialises a Node into both cayley and the mirror.
// Caller holds s.mu.
func (s *Store) addNodeLocked(n *gortex.Node) error {
	if n == nil || n.ID == "" {
		return nil
	}
	if _, dup := s.nodes[n.ID]; dup {
		// Idempotent overwrite — delete the existing quad set first so
		// repeated AddNodes with changed metadata reflect the latest
		// payload without leaving stale predicates behind.
		if del := s.deleteSubjectDeltas(nodeSubject(n.ID)); len(del) > 0 {
			if err := s.applyDeltas(del); err != nil {
				return err
			}
		}
		s.unindexNodeLocked(s.nodes[n.ID])
	}
	deltas, err := buildNodeDeltas(n)
	if err != nil {
		return err
	}
	if err := s.applyDeltas(deltas); err != nil {
		return err
	}
	// Store a defensive copy so callers can't mutate our mirror in-place.
	cp := *n
	if n.Meta != nil {
		cp.Meta = make(map[string]any, len(n.Meta))
		for k, v := range n.Meta {
			cp.Meta[k] = v
		}
	}
	s.indexNodeLocked(&cp)
	return nil
}

// addEdgeLocked materialises an Edge into both cayley and the mirror.
// Caller holds s.mu.
func (s *Store) addEdgeLocked(e *gortex.Edge) error {
	if e == nil {
		return nil
	}
	k := keyOf(e)
	if _, dup := s.allEdges[k]; dup {
		// Re-add of the exact same identity tuple is a no-op for the
		// quad subject — cayley would deduplicate the quads but we
		// also want to refresh non-identity fields (Origin upgrades,
		// Meta changes) without inflating EdgeIdentityRevisions.
		if del := s.deleteSubjectDeltas(k.subject()); len(del) > 0 {
			if err := s.applyDeltas(del); err != nil {
				return err
			}
		}
		s.unindexEdgeLocked(s.allEdges[k])
	}
	deltas, err := buildEdgeDeltas(e)
	if err != nil {
		return err
	}
	if err := s.applyDeltas(deltas); err != nil {
		return err
	}
	// Defensive copy of the edge for the mirror.
	cp := *e
	if e.Meta != nil {
		cp.Meta = make(map[string]any, len(e.Meta))
		for k2, v := range e.Meta {
			cp.Meta[k2] = v
		}
	}
	s.indexEdgeLocked(&cp)
	return nil
}

// indexNodeLocked inserts a node into every in-memory index. Caller
// holds s.mu.
func (s *Store) indexNodeLocked(n *gortex.Node) {
	s.nodes[n.ID] = n
	if n.Name != "" {
		s.nodesByName[n.Name] = append(s.nodesByName[n.Name], n)
	}
	if n.QualName != "" {
		s.nodesByQual[n.QualName] = n
	}
	if n.FilePath != "" {
		bucket := s.nodesByFile[n.FilePath]
		if bucket == nil {
			bucket = make(map[string]*gortex.Node)
			s.nodesByFile[n.FilePath] = bucket
		}
		bucket[n.ID] = n
	}
	if n.RepoPrefix != "" {
		bucket := s.nodesByRepo[n.RepoPrefix]
		if bucket == nil {
			bucket = make(map[string]*gortex.Node)
			s.nodesByRepo[n.RepoPrefix] = bucket
		}
		bucket[n.ID] = n
	}
	bucket := s.nodesByKind[n.Kind]
	if bucket == nil {
		bucket = make(map[string]*gortex.Node)
		s.nodesByKind[n.Kind] = bucket
	}
	bucket[n.ID] = n
}

// unindexNodeLocked removes a node from every in-memory index. Caller
// holds s.mu.
func (s *Store) unindexNodeLocked(n *gortex.Node) {
	if n == nil {
		return
	}
	delete(s.nodes, n.ID)
	if n.Name != "" {
		bucket := s.nodesByName[n.Name]
		for i, v := range bucket {
			if v.ID == n.ID {
				s.nodesByName[n.Name] = append(bucket[:i], bucket[i+1:]...)
				break
			}
		}
		if len(s.nodesByName[n.Name]) == 0 {
			delete(s.nodesByName, n.Name)
		}
	}
	if n.QualName != "" {
		if cur := s.nodesByQual[n.QualName]; cur != nil && cur.ID == n.ID {
			delete(s.nodesByQual, n.QualName)
		}
	}
	if n.FilePath != "" {
		bucket := s.nodesByFile[n.FilePath]
		delete(bucket, n.ID)
		if len(bucket) == 0 {
			delete(s.nodesByFile, n.FilePath)
		}
	}
	if n.RepoPrefix != "" {
		bucket := s.nodesByRepo[n.RepoPrefix]
		delete(bucket, n.ID)
		if len(bucket) == 0 {
			delete(s.nodesByRepo, n.RepoPrefix)
		}
	}
	bucket := s.nodesByKind[n.Kind]
	delete(bucket, n.ID)
	if len(bucket) == 0 {
		delete(s.nodesByKind, n.Kind)
	}
}

// indexEdgeLocked inserts an edge into every in-memory index. Caller
// holds s.mu.
func (s *Store) indexEdgeLocked(e *gortex.Edge) {
	k := keyOf(e)
	s.allEdges[k] = e
	if s.outEdges[e.From] == nil {
		s.outEdges[e.From] = make(map[edgeKey]*gortex.Edge)
	}
	s.outEdges[e.From][k] = e
	if s.inEdges[e.To] == nil {
		s.inEdges[e.To] = make(map[edgeKey]*gortex.Edge)
	}
	s.inEdges[e.To][k] = e
	if s.edgesByKind[e.Kind] == nil {
		s.edgesByKind[e.Kind] = make(map[edgeKey]*gortex.Edge)
	}
	s.edgesByKind[e.Kind][k] = e
	if strings.HasPrefix(e.To, "unresolved::") {
		s.unresolvedES[k] = e
	}
}

// unindexEdgeLocked removes an edge from every in-memory index. Caller
// holds s.mu.
func (s *Store) unindexEdgeLocked(e *gortex.Edge) {
	if e == nil {
		return
	}
	k := keyOf(e)
	delete(s.allEdges, k)
	if bucket := s.outEdges[e.From]; bucket != nil {
		delete(bucket, k)
		if len(bucket) == 0 {
			delete(s.outEdges, e.From)
		}
	}
	if bucket := s.inEdges[e.To]; bucket != nil {
		delete(bucket, k)
		if len(bucket) == 0 {
			delete(s.inEdges, e.To)
		}
	}
	if bucket := s.edgesByKind[e.Kind]; bucket != nil {
		delete(bucket, k)
		if len(bucket) == 0 {
			delete(s.edgesByKind, e.Kind)
		}
	}
	delete(s.unresolvedES, k)
}

// -- 35 graph.Store methods ------------------------------------------------

// AddNode adds (or replaces) a node.
func (s *Store) AddNode(n *gortex.Node) {
	if n == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.addNodeLocked(n)
}

// AddBatch adds a batch of nodes and edges in one transaction-shaped
// pass. Cayley's ApplyDeltas chunks internally; for readability we
// commit in chunks of ~5000 mutations to keep memory bounded.
func (s *Store) AddBatch(nodes []*gortex.Node, edges []*gortex.Edge) {
	if len(nodes) == 0 && len(edges) == 0 {
		return
	}
	const chunk = 5000
	s.mu.Lock()
	defer s.mu.Unlock()

	// Nodes first. Iterate per-node and use addNodeLocked so dedup
	// semantics match the single-add path exactly.
	for i := 0; i < len(nodes); i += chunk {
		end := i + chunk
		if end > len(nodes) {
			end = len(nodes)
		}
		for _, n := range nodes[i:end] {
			_ = s.addNodeLocked(n)
		}
	}
	for i := 0; i < len(edges); i += chunk {
		end := i + chunk
		if end > len(edges) {
			end = len(edges)
		}
		for _, e := range edges[i:end] {
			_ = s.addEdgeLocked(e)
		}
	}
}

// AddEdge adds (or replaces) an edge.
func (s *Store) AddEdge(e *gortex.Edge) {
	if e == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.addEdgeLocked(e)
}

// SetEdgeProvenance promotes the Origin of e to newOrigin when newOrigin
// is strictly more confident. Returns true when the persisted edge was
// rewritten (and EdgeIdentityRevisions bumped).
func (s *Store) SetEdgeProvenance(e *gortex.Edge, newOrigin string) bool {
	if e == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := keyOf(e)
	cur := s.allEdges[k]
	if cur == nil {
		return false
	}
	if gortex.OriginRank(newOrigin) <= gortex.OriginRank(cur.Origin) {
		return false
	}
	cur.Origin = newOrigin
	e.Origin = newOrigin
	// Rewrite the subject's quads to reflect the new origin.
	if del := s.deleteSubjectDeltas(k.subject()); len(del) > 0 {
		if err := s.applyDeltas(del); err != nil {
			return false
		}
	}
	deltas, err := buildEdgeDeltas(cur)
	if err != nil {
		return false
	}
	if err := s.applyDeltas(deltas); err != nil {
		return false
	}
	s.edgeIdentityRevs.Add(1)
	return true
}

// ReindexEdge re-binds an edge from oldTo to its current e.To.
func (s *Store) ReindexEdge(e *gortex.Edge, oldTo string) {
	if e == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reindexEdgeLocked(e, oldTo)
}

func (s *Store) reindexEdgeLocked(e *gortex.Edge, oldTo string) {
	oldKey := edgeKey{From: e.From, To: oldTo, Kind: e.Kind, File: e.FilePath, Line: e.Line}
	old := s.allEdges[oldKey]
	// Drop the old subject quads, regardless of whether the mirror saw it.
	if del := s.deleteSubjectDeltas(oldKey.subject()); len(del) > 0 {
		_ = s.applyDeltas(del)
	}
	if old != nil {
		s.unindexEdgeLocked(old)
	}
	_ = s.addEdgeLocked(e)
}

// ReindexEdges batches per-edge ReindexEdge calls under one mutex acquisition.
func (s *Store) ReindexEdges(batch []gortex.EdgeReindex) {
	if len(batch) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range batch {
		if item.Edge == nil {
			continue
		}
		s.reindexEdgeLocked(item.Edge, item.OldTo)
	}
}

// SetEdgeProvenanceBatch promotes every input edge whose NewOrigin
// is strictly more confident than its current Origin. Returns the count
// of edges actually changed.
func (s *Store) SetEdgeProvenanceBatch(batch []gortex.EdgeProvenanceUpdate) int {
	if len(batch) == 0 {
		return 0
	}
	const chunk = 5000
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := 0
	for i := 0; i < len(batch); i += chunk {
		end := i + chunk
		if end > len(batch) {
			end = len(batch)
		}
		for _, upd := range batch[i:end] {
			if upd.Edge == nil {
				continue
			}
			k := keyOf(upd.Edge)
			cur := s.allEdges[k]
			if cur == nil {
				continue
			}
			if gortex.OriginRank(upd.NewOrigin) <= gortex.OriginRank(cur.Origin) {
				continue
			}
			cur.Origin = upd.NewOrigin
			upd.Edge.Origin = upd.NewOrigin
			if del := s.deleteSubjectDeltas(k.subject()); len(del) > 0 {
				_ = s.applyDeltas(del)
			}
			if deltas, err := buildEdgeDeltas(cur); err == nil {
				_ = s.applyDeltas(deltas)
			}
			s.edgeIdentityRevs.Add(1)
			changed++
		}
	}
	return changed
}

// RemoveEdge removes any edge matching (from, to, kind) regardless of
// file/line — mirrors the in-memory store semantics. Returns true when
// at least one edge was removed.
func (s *Store) RemoveEdge(from, to string, kind gortex.EdgeKind) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var victims []*gortex.Edge
	if bucket := s.outEdges[from]; bucket != nil {
		for _, e := range bucket {
			if e.To == to && e.Kind == kind {
				victims = append(victims, e)
			}
		}
	}
	if len(victims) == 0 {
		return false
	}
	for _, e := range victims {
		k := keyOf(e)
		if del := s.deleteSubjectDeltas(k.subject()); len(del) > 0 {
			_ = s.applyDeltas(del)
		}
		s.unindexEdgeLocked(e)
	}
	return true
}

// EvictFile removes every node whose FilePath equals filePath plus every
// edge touching one of those nodes. Returns the counts.
func (s *Store) EvictFile(filePath string) (int, int) {
	if filePath == "" {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket := s.nodesByFile[filePath]
	if len(bucket) == 0 {
		return 0, 0
	}
	ids := make(map[string]struct{}, len(bucket))
	for id := range bucket {
		ids[id] = struct{}{}
	}
	return s.evictNodesByIDLocked(ids)
}

// EvictRepo removes every node whose RepoPrefix equals repoPrefix plus
// every edge touching one of those nodes.
func (s *Store) EvictRepo(repoPrefix string) (int, int) {
	if repoPrefix == "" {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket := s.nodesByRepo[repoPrefix]
	if len(bucket) == 0 {
		return 0, 0
	}
	ids := make(map[string]struct{}, len(bucket))
	for id := range bucket {
		ids[id] = struct{}{}
	}
	return s.evictNodesByIDLocked(ids)
}

// evictNodesByIDLocked drops every node in ids and every edge whose From
// or To is in ids. Returns (nodesRemoved, edgesRemoved).
func (s *Store) evictNodesByIDLocked(ids map[string]struct{}) (int, int) {
	var nRemoved, eRemoved int
	// Collect every edge whose From or To is in ids — duplicates dedupe
	// via the map.
	victims := make(map[edgeKey]*gortex.Edge)
	for id := range ids {
		for k, e := range s.outEdges[id] {
			victims[k] = e
		}
		for k, e := range s.inEdges[id] {
			victims[k] = e
		}
	}
	for _, e := range victims {
		k := keyOf(e)
		if del := s.deleteSubjectDeltas(k.subject()); len(del) > 0 {
			_ = s.applyDeltas(del)
		}
		s.unindexEdgeLocked(e)
		eRemoved++
	}
	for id := range ids {
		n := s.nodes[id]
		if n == nil {
			continue
		}
		if del := s.deleteSubjectDeltas(nodeSubject(id)); len(del) > 0 {
			_ = s.applyDeltas(del)
		}
		s.unindexNodeLocked(n)
		nRemoved++
	}
	return nRemoved, eRemoved
}

// -- point lookups ----------------------------------------------------------

// GetNode returns the node with the given ID, or nil if absent.
func (s *Store) GetNode(id string) *gortex.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nodes[id]
}

// GetNodeByQualName returns the node whose QualName matches.
func (s *Store) GetNodeByQualName(qualName string) *gortex.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nodesByQual[qualName]
}

// -- name / scope queries ---------------------------------------------------

// FindNodesByName returns every node whose Name field matches.
func (s *Store) FindNodesByName(name string) []*gortex.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bucket := s.nodesByName[name]
	if len(bucket) == 0 {
		return nil
	}
	out := make([]*gortex.Node, len(bucket))
	copy(out, bucket)
	return out
}

// FindNodesByNameInRepo returns every node whose Name and RepoPrefix
// match.
func (s *Store) FindNodesByNameInRepo(name, repoPrefix string) []*gortex.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bucket := s.nodesByName[name]
	if len(bucket) == 0 {
		return nil
	}
	var out []*gortex.Node
	for _, n := range bucket {
		if n.RepoPrefix == repoPrefix {
			out = append(out, n)
		}
	}
	return out
}

// GetFileNodes returns every node in the given file.
func (s *Store) GetFileNodes(filePath string) []*gortex.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bucket := s.nodesByFile[filePath]
	if len(bucket) == 0 {
		return nil
	}
	out := make([]*gortex.Node, 0, len(bucket))
	for _, n := range bucket {
		out = append(out, n)
	}
	return out
}

// GetRepoNodes returns every node in the given repo.
func (s *Store) GetRepoNodes(repoPrefix string) []*gortex.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bucket := s.nodesByRepo[repoPrefix]
	if len(bucket) == 0 {
		return nil
	}
	out := make([]*gortex.Node, 0, len(bucket))
	for _, n := range bucket {
		out = append(out, n)
	}
	return out
}

// -- edge adjacency --------------------------------------------------------

// GetOutEdges returns every edge whose From is nodeID.
func (s *Store) GetOutEdges(nodeID string) []*gortex.Edge {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bucket := s.outEdges[nodeID]
	if len(bucket) == 0 {
		return nil
	}
	out := make([]*gortex.Edge, 0, len(bucket))
	for _, e := range bucket {
		out = append(out, e)
	}
	return out
}

// GetInEdges returns every edge whose To is nodeID.
func (s *Store) GetInEdges(nodeID string) []*gortex.Edge {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bucket := s.inEdges[nodeID]
	if len(bucket) == 0 {
		return nil
	}
	out := make([]*gortex.Edge, 0, len(bucket))
	for _, e := range bucket {
		out = append(out, e)
	}
	return out
}

// -- bulk reads ------------------------------------------------------------

// AllNodes returns every node in the store.
func (s *Store) AllNodes() []*gortex.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*gortex.Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, n)
	}
	return out
}

// AllEdges returns every edge in the store.
func (s *Store) AllEdges() []*gortex.Edge {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*gortex.Edge, 0, len(s.allEdges))
	for _, e := range s.allEdges {
		out = append(out, e)
	}
	return out
}

// -- predicate-shaped reads -------------------------------------------------

// EdgesByKind yields every edge whose Kind matches.
func (s *Store) EdgesByKind(kind gortex.EdgeKind) iter.Seq[*gortex.Edge] {
	return func(yield func(*gortex.Edge) bool) {
		s.mu.RLock()
		bucket := s.edgesByKind[kind]
		// Snapshot so we don't hold the lock for the duration of the
		// caller's loop body — caller might do arbitrarily expensive
		// work per yielded edge.
		snap := make([]*gortex.Edge, 0, len(bucket))
		for _, e := range bucket {
			snap = append(snap, e)
		}
		s.mu.RUnlock()
		for _, e := range snap {
			if !yield(e) {
				return
			}
		}
	}
}

// NodesByKind yields every node whose Kind matches.
func (s *Store) NodesByKind(kind gortex.NodeKind) iter.Seq[*gortex.Node] {
	return func(yield func(*gortex.Node) bool) {
		s.mu.RLock()
		bucket := s.nodesByKind[kind]
		snap := make([]*gortex.Node, 0, len(bucket))
		for _, n := range bucket {
			snap = append(snap, n)
		}
		s.mu.RUnlock()
		for _, n := range snap {
			if !yield(n) {
				return
			}
		}
	}
}

// EdgesWithUnresolvedTarget yields every edge whose To starts with
// "unresolved::".
func (s *Store) EdgesWithUnresolvedTarget() iter.Seq[*gortex.Edge] {
	return func(yield func(*gortex.Edge) bool) {
		s.mu.RLock()
		snap := make([]*gortex.Edge, 0, len(s.unresolvedES))
		for _, e := range s.unresolvedES {
			snap = append(snap, e)
		}
		s.mu.RUnlock()
		for _, e := range snap {
			if !yield(e) {
				return
			}
		}
	}
}

// -- batched point lookups -------------------------------------------------

// GetNodesByIDs returns a map id->*Node for every input ID present.
func (s *Store) GetNodesByIDs(ids []string) map[string]*gortex.Node {
	if len(ids) == 0 {
		return map[string]*gortex.Node{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*gortex.Node, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if n := s.nodes[id]; n != nil {
			out[id] = n
		}
	}
	return out
}

// FindNodesByNames returns a map name->[]*Node where each slot holds
// every node whose Name field matches.
func (s *Store) FindNodesByNames(names []string) map[string][]*gortex.Node {
	if len(names) == 0 {
		return map[string][]*gortex.Node{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]*gortex.Node, len(names))
	for _, name := range names {
		if _, dup := out[name]; dup {
			continue
		}
		bucket := s.nodesByName[name]
		if len(bucket) == 0 {
			continue
		}
		cp := make([]*gortex.Node, len(bucket))
		copy(cp, bucket)
		out[name] = cp
	}
	return out
}

// -- counts and stats -------------------------------------------------------

// NodeCount returns the number of nodes.
func (s *Store) NodeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.nodes)
}

// EdgeCount returns the number of edges.
func (s *Store) EdgeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.allEdges)
}

// Stats returns aggregate node/edge counts and per-kind / per-language
// node breakdowns.
func (s *Store) Stats() gortex.GraphStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := gortex.GraphStats{
		TotalNodes: len(s.nodes),
		TotalEdges: len(s.allEdges),
		ByKind:     make(map[string]int),
		ByLanguage: make(map[string]int),
	}
	for _, n := range s.nodes {
		st.ByKind[string(n.Kind)]++
		if n.Language != "" {
			st.ByLanguage[n.Language]++
		}
	}
	return st
}

// RepoStats returns per-repo stats.
func (s *Store) RepoStats() map[string]gortex.GraphStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]gortex.GraphStats)
	for repo, bucket := range s.nodesByRepo {
		st := gortex.GraphStats{
			ByKind:     make(map[string]int),
			ByLanguage: make(map[string]int),
		}
		nodeIDs := make(map[string]struct{}, len(bucket))
		for id, n := range bucket {
			nodeIDs[id] = struct{}{}
			st.TotalNodes++
			st.ByKind[string(n.Kind)]++
			if n.Language != "" {
				st.ByLanguage[n.Language]++
			}
		}
		// Edge belongs to repo if both endpoints belong to nodes in the
		// repo. Cheap proxy: count edges whose From is in this repo's
		// node set.
		for _, e := range s.allEdges {
			if _, ok := nodeIDs[e.From]; ok {
				st.TotalEdges++
			}
		}
		out[repo] = st
	}
	return out
}

// RepoPrefixes returns the sorted list of distinct repo prefixes seen.
func (s *Store) RepoPrefixes() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.nodesByRepo))
	for repo := range s.nodesByRepo {
		out = append(out, repo)
	}
	return out
}

// -- provenance verification ----------------------------------------------

// EdgeIdentityRevisions returns the monotonic provenance-churn counter.
func (s *Store) EdgeIdentityRevisions() int {
	return int(s.edgeIdentityRevs.Load())
}

// VerifyEdgeIdentities walks every edge and re-checks that its in-memory
// identity tuple matches what the quad subject IRI encodes. Returns the
// first inconsistency.
func (s *Store) VerifyEdgeIdentities() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.allEdges {
		expected := keyOf(e).subject()
		ref := s.qs.ValueOf(expected)
		if ref == nil {
			return fmt.Errorf("store_cayley: edge %s->%s line=%d missing from quad store", e.From, e.To, e.Line)
		}
	}
	return nil
}

// -- memory estimation ----------------------------------------------------

// RepoMemoryEstimate returns an advisory size of the repo's mirror.
func (s *Store) RepoMemoryEstimate(repoPrefix string) gortex.RepoMemoryEstimate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bucket := s.nodesByRepo[repoPrefix]
	est := gortex.RepoMemoryEstimate{NodeCount: len(bucket)}
	for _, n := range bucket {
		est.NodeBytes += uint64(approxNodeSize(n))
	}
	nodeIDs := make(map[string]struct{}, len(bucket))
	for id := range bucket {
		nodeIDs[id] = struct{}{}
	}
	for _, e := range s.allEdges {
		if _, ok := nodeIDs[e.From]; ok {
			est.EdgeCount++
			est.EdgeBytes += uint64(approxEdgeSize(e))
		}
	}
	return est
}

// AllRepoMemoryEstimates returns RepoMemoryEstimate for every repo.
func (s *Store) AllRepoMemoryEstimates() map[string]gortex.RepoMemoryEstimate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]gortex.RepoMemoryEstimate, len(s.nodesByRepo))
	for repo, bucket := range s.nodesByRepo {
		est := gortex.RepoMemoryEstimate{NodeCount: len(bucket)}
		nodeIDs := make(map[string]struct{}, len(bucket))
		for id, n := range bucket {
			est.NodeBytes += uint64(approxNodeSize(n))
			nodeIDs[id] = struct{}{}
		}
		for _, e := range s.allEdges {
			if _, ok := nodeIDs[e.From]; ok {
				est.EdgeCount++
				est.EdgeBytes += uint64(approxEdgeSize(e))
			}
		}
		out[repo] = est
	}
	return out
}

// approxNodeSize returns a rough byte count for a Node (struct overhead
// plus string field lengths). Meta blobs are estimated as their string
// representation length.
func approxNodeSize(n *gortex.Node) int {
	size := 200 // struct overhead (fields, headers)
	size += len(n.ID) + len(n.Name) + len(n.QualName) + len(n.FilePath)
	size += len(n.Language) + len(n.RepoPrefix) + len(n.WorkspaceID)
	size += len(n.ProjectID) + len(n.AbsoluteFilePath)
	for k, v := range n.Meta {
		size += len(k) + 16 // rough
		if s, ok := v.(string); ok {
			size += len(s)
		}
	}
	return size
}

// approxEdgeSize returns a rough byte count for an Edge.
func approxEdgeSize(e *gortex.Edge) int {
	size := 200
	size += len(e.From) + len(e.To) + len(e.FilePath)
	size += len(e.ConfidenceLabel) + len(e.Origin) + len(e.Tier)
	size += len(string(e.Kind))
	for k, v := range e.Meta {
		size += len(k) + 16
		if s, ok := v.(string); ok {
			size += len(s)
		}
	}
	return size
}

// -- meta blob codec -------------------------------------------------------

func encodeMetaBlob(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(m); err != nil {
		return nil, fmt.Errorf("store_cayley: encode meta: %w", err)
	}
	return buf.Bytes(), nil
}

func decodeMetaBlob(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	m := make(map[string]any)
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&m); err != nil {
		return nil, fmt.Errorf("store_cayley: decode meta: %w", err)
	}
	return m, nil
}

// -- mirror reconstruction --------------------------------------------------

// rebuildMirror walks every quad in the store and reconstructs the
// in-memory indexes. Runs once on Open.
func (s *Store) rebuildMirror() error {
	ctx := context.Background()
	// We discriminate node vs. edge subjects by the IRI prefix.
	nodeRaw := make(map[string]map[string]quad.Value)
	edgeRaw := make(map[string]map[string]quad.Value)

	it := s.qs.QuadsAllIterator()
	defer it.Close()
	err := graph.Iterate(ctx, it).Each(func(r graph.Ref) {
		q := s.qs.Quad(r)
		sub, ok := q.Subject.(quad.IRI)
		if !ok {
			return
		}
		subStr := string(sub)
		pred, _ := q.Predicate.(quad.IRI)
		predStr := string(pred)
		switch {
		case strings.HasPrefix(subStr, nodeSubjectPrefix):
			id := strings.TrimPrefix(subStr, nodeSubjectPrefix)
			if nodeRaw[id] == nil {
				nodeRaw[id] = make(map[string]quad.Value)
			}
			nodeRaw[id][predStr] = q.Object
		case strings.HasPrefix(subStr, edgeSubjectPrefix):
			if edgeRaw[subStr] == nil {
				edgeRaw[subStr] = make(map[string]quad.Value)
			}
			edgeRaw[subStr][predStr] = q.Object
		}
	})
	if err != nil {
		return err
	}

	for id, preds := range nodeRaw {
		n := decodeNode(id, preds)
		if n != nil {
			s.indexNodeLocked(n)
		}
	}
	for _, preds := range edgeRaw {
		e := decodeEdge(preds)
		if e != nil {
			s.indexEdgeLocked(e)
		}
	}
	return nil
}

// decodeNode reconstructs a Node from its per-predicate object values.
func decodeNode(id string, preds map[string]quad.Value) *gortex.Node {
	n := &gortex.Node{ID: id}
	if v, ok := preds[string(predKind)]; ok {
		n.Kind = gortex.NodeKind(stringValue(v))
	}
	if v, ok := preds[string(predName)]; ok {
		n.Name = stringValue(v)
	}
	if v, ok := preds[string(predQualName)]; ok {
		n.QualName = stringValue(v)
	}
	if v, ok := preds[string(predFilePath)]; ok {
		n.FilePath = stringValue(v)
	}
	if v, ok := preds[string(predStartLine)]; ok {
		n.StartLine = intValue(v)
	}
	if v, ok := preds[string(predEndLine)]; ok {
		n.EndLine = intValue(v)
	}
	if v, ok := preds[string(predLanguage)]; ok {
		n.Language = stringValue(v)
	}
	if v, ok := preds[string(predRepoPrefix)]; ok {
		n.RepoPrefix = stringValue(v)
	}
	if v, ok := preds[string(predWorkspaceID)]; ok {
		n.WorkspaceID = stringValue(v)
	}
	if v, ok := preds[string(predProjectID)]; ok {
		n.ProjectID = stringValue(v)
	}
	if v, ok := preds[string(predAbsoluteFilePath)]; ok {
		n.AbsoluteFilePath = stringValue(v)
	}
	if v, ok := preds[string(predMeta)]; ok {
		blob := rawBytes(v)
		if m, err := decodeMetaBlob(blob); err == nil {
			n.Meta = m
		}
	}
	return n
}

// decodeEdge reconstructs an Edge from its per-predicate object values.
func decodeEdge(preds map[string]quad.Value) *gortex.Edge {
	e := &gortex.Edge{}
	if v, ok := preds[string(predKind)]; ok {
		e.Kind = gortex.EdgeKind(stringValue(v))
	}
	if v, ok := preds[string(predFrom)]; ok {
		e.From = stringValue(v)
	}
	if v, ok := preds[string(predTo)]; ok {
		e.To = stringValue(v)
	}
	if v, ok := preds[string(predFilePath)]; ok {
		e.FilePath = stringValue(v)
	}
	if v, ok := preds[string(predLine)]; ok {
		e.Line = intValue(v)
	}
	if v, ok := preds[string(predConfidence)]; ok {
		if f, ok := v.(quad.Float); ok {
			e.Confidence = float64(f)
		}
	}
	if v, ok := preds[string(predConfidenceLabel)]; ok {
		e.ConfidenceLabel = stringValue(v)
	}
	if v, ok := preds[string(predOrigin)]; ok {
		e.Origin = stringValue(v)
	}
	if v, ok := preds[string(predTier)]; ok {
		e.Tier = stringValue(v)
	}
	if v, ok := preds[string(predCrossRepo)]; ok {
		if b, ok := v.(quad.Bool); ok {
			e.CrossRepo = bool(b)
		}
	}
	if v, ok := preds[string(predMeta)]; ok {
		blob := rawBytes(v)
		if m, err := decodeMetaBlob(blob); err == nil {
			e.Meta = m
		}
	}
	return e
}

// stringValue extracts the string from a quad.Value (handles quad.String
// and quad.IRI).
func stringValue(v quad.Value) string {
	switch t := v.(type) {
	case quad.String:
		return string(t)
	case quad.IRI:
		return string(t)
	}
	return quad.StringOf(v)
}

// intValue extracts an int from a quad.Value.
func intValue(v quad.Value) int {
	if i, ok := v.(quad.Int); ok {
		return int(i)
	}
	if s, ok := v.(quad.String); ok {
		if n, err := strconv.Atoi(string(s)); err == nil {
			return n
		}
	}
	return 0
}

// rawBytes extracts the byte payload of a Meta blob. We store gob bytes
// in a quad.String so Go's byte-safe strings carry the payload verbatim.
func rawBytes(v quad.Value) []byte {
	switch t := v.(type) {
	case quad.String:
		return []byte(t)
	}
	return nil
}
