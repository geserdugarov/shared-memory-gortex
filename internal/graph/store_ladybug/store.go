package store_ladybug

import (
	"fmt"
	"sync"
	"sync/atomic"

	lbug "github.com/LadybugDB/go-ladybug"

	"github.com/zzet/gortex/internal/graph"
)

// Store is the KuzuDB-backed graph.Store implementation.
type Store struct {
	db   *lbug.Database
	conn *lbug.Connection // setup connection — DDL + extension installs
	pool *connPool        // per-Store fan-out for query traffic

	// writeMu serialises every mutation AND excludes reads for the
	// duration of a write. It is an RWMutex: writes take the exclusive
	// Lock (one writer at a time, no concurrent readers), reads take the
	// shared RLock (any number of concurrent readers, none while a write
	// is in flight).
	//
	// The read-exclusion is load-bearing, not just for logical
	// consistency: ladybug's bulk COPY extends the .lbug file in place,
	// and a read issued on a *different* pooled connection while that
	// COPY is mid-flight lands in a half-written buffer page. The benign
	// outcome is an "IO exception: Cannot read N bytes at position M"
	// (degraded to an empty result on the read path); the malign outcome
	// is a SIGSEGV inside lbug_connection_query as the COPY's own CGo
	// call trips over the concurrently-mutated buffer-pool state. Holding
	// the writer side across every COPY/MERGE/DELETE and the reader side
	// across every query makes the two mutually exclusive, which is the
	// only contract this ladybug revision actually honours under
	// concurrency. Concurrent reads still parallelise via RLock, so the
	// steady-state fan-out the conformance suite exercises is preserved.
	writeMu sync.RWMutex

	// resolveMu is the resolver-coordination mutex returned by
	// ResolveMutex. Held by cross-repo / temporal / external resolver
	// passes to keep their edge mutations from interleaving. Separate
	// from writeMu so the resolver can hold it across multiple writes
	// without blocking unrelated steady-state mutations.
	resolveMu sync.Mutex

	edgeIdentityRevs atomic.Int64

	// writeGen monotonically advances on every successful graph
	// mutation. Cheap, lock-free, and consumed by the algo
	// projection cache to invalidate a stale CALL PROJECT_GRAPH
	// declaration when the underlying graph has changed. Reads
	// must NOT bump it — only paths that hit disk via COPY /
	// MERGE / CREATE / DELETE / SET on Node or Edge.
	writeGen atomic.Uint64

	// Bulk-load fast path. When the indexer brackets its parse loop
	// with BeginBulkLoad/FlushBulk, AddBatch routes incoming rows
	// into these slices instead of round-tripping through Cypher per
	// call. FlushBulk dedupes the buffers and commits via Kuzu's
	// COPY FROM CSV — one INSERT-only statement per table, no MERGE
	// cost, no per-row Cypher parse/plan. See BeginBulkLoad doc.
	// bulkSlot serialises BeginBulkLoad ↔ FlushBulk against the
	// per-Store buffer. Concurrent per-repo Indexers each call
	// BeginBulkLoad on the shared Store at drain time; without this
	// mutex they would race on bulkActive and the second caller
	// would observe bulkActive==true. Holding the slot for the full
	// Begin→Flush window means concurrent drains serialise — the
	// second drain blocks at BeginBulkLoad until the first flush
	// returns the slot.
	bulkSlot   sync.Mutex
	bulkMu     sync.Mutex
	bulkActive bool
	bulkNodes  []*graph.Node
	bulkEdges  []*graph.Edge

	// fts tracks whether the native FTS extension is loaded and
	// whether the symbol FTS index has been built. See fts.go for
	// the SymbolSearcher implementation.
	fts ftsState

	// vec tracks the native VECTOR extension load + the per-dim
	// SymbolVec schema declaration + index-build sentinel. See
	// vector.go for the VectorSearcher implementation.
	vec vectorState

	// algo tracks the native ALGO extension load + the per-call
	// projection-name serialisation mutex. See algo.go for the
	// PageRanker / CommunityDetector / ComponentFinder / KCorer
	// implementations.
	algo algoState

	// fileIDs accelerates per-file lookups (GetFileSubGraph,
	// GetFileNodes …) by sidestepping the Node-table full scan Kuzu
	// would otherwise need. Maintained on every node mutation; see
	// file_index.go.
	fileIDs *fileIDIndex

	// nameIdx is the tier-0 fast path for SearchSymbols: a
	// denormalised lower(name) → []NodeID map maintained alongside
	// every Node write. Identifier-shape queries skip the FTS
	// round-trip when this hits. See name_index.go.
	nameIdx *nameIndex

	// needsRebuild is set at Open when the migration ladder crossed a
	// rung that ALTER could not satisfy (a Meta-payload reshape, a table
	// restructure). The caller surfaces it via NeedsRebuild() and treats
	// the on-disk graph as stale — a full re-index into the fresh schema.
	// Always false on a fresh open and after purely additive migrations.
	// See migrate.go.
	needsRebuild bool
}

// Compile-time assertion: *Store satisfies graph.Store.
var _ graph.Store = (*Store)(nil)

// connPoolSize is the per-Store connection-pool fan-out.
// MultiIndexer runs one parse goroutine per repo; with 4 active
// repos and per-repo shadow drains, 8 gives ample headroom for
// concurrent reads + drains without queue contention. ladybug's
// C engine handles its own internal threadpool per query, so
// over-sizing the pool here mostly burns memory without buying
// extra parallelism.
const connPoolSize = 8

// DefaultBufferPoolMB is the buffer-pool cap applied when the caller
// passes Options{} (zero value). Ladybug's own default is 80% of
// system RAM, which on a 16 GiB laptop reserves ~12.8 GiB before a
// single row is inserted; clamping to a fixed 4 GiB keeps the
// daemon's resident set predictable across machine sizes.
const DefaultBufferPoolMB = 4096

// Options configures the embedded Ladybug instance. The zero value
// applies DefaultBufferPoolMB; callers override fields as needed.
type Options struct {
	// BufferPoolMB caps the engine's page cache in MiB. Zero falls
	// back to DefaultBufferPoolMB.
	BufferPoolMB uint64
}

// Open is the zero-config entry point. Equivalent to
// OpenWithOptions(path, Options{}).
func Open(path string) (*Store, error) {
	return OpenWithOptions(path, Options{})
}

// OpenWithOptions opens (or creates) a Ladybug database at path and
// applies the schema. The path is a directory Ladybug owns end-to-end;
// an empty directory is initialised on first open and reused on every
// subsequent open.
//
// Opens one "setup" connection for DDL + extension installs, then
// a pool of additional connections for parallel query traffic.
// MultiIndexer's per-repo goroutines each borrow their own pool
// connection so concurrent reads + drains don't serialise on a
// single Connection handle (the Go binding races in cgo without
// a per-connection serialisation point).
func OpenWithOptions(path string, opts Options) (*Store, error) {
	cfg := lbug.DefaultSystemConfig()
	bufMB := opts.BufferPoolMB
	if bufMB == 0 {
		bufMB = DefaultBufferPoolMB
	}
	cfg.BufferPoolSize = bufMB * 1024 * 1024
	db, err := lbug.OpenDatabase(path, cfg)
	if err != nil {
		return nil, fmt.Errorf("store_ladybug: open %q: %w", path, err)
	}
	conn, err := lbug.OpenConnection(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("store_ladybug: open connection: %w", err)
	}
	for _, stmt := range schemaDDL {
		res, err := conn.Query(stmt)
		if err != nil {
			conn.Close()
			db.Close()
			return nil, fmt.Errorf("store_ladybug: schema %q: %w", firstLine(stmt), err)
		}
		res.Close()
	}
	// Bring the on-disk schema up to currentSchemaVersion before any
	// query traffic. Runs on the raw setup conn (no pool yet, no
	// writeMu) — see migrate.go. needsRebuild is true only if a ladder
	// step required a full re-index (ALTER could not express it).
	needsRebuild, err := applyLadybugMigrations(conn)
	if err != nil {
		conn.Close()
		db.Close()
		return nil, fmt.Errorf("store_ladybug: migrate schema: %w", err)
	}
	pool, err := newConnPool(db, connPoolSize)
	if err != nil {
		conn.Close()
		db.Close()
		return nil, fmt.Errorf("store_ladybug: init conn pool: %w", err)
	}
	st := &Store{db: db, conn: conn, pool: pool, needsRebuild: needsRebuild, fileIDs: newFileIDIndex(), nameIdx: newNameIndex()}
	// Populate the file→id accelerator from any data already on disk
	// (daemon restart, ladybug snapshot reload). A fresh DB returns 0
	// rows and this is a cheap no-op; an existing DB pays one
	// sequential Node scan in exchange for sub-millisecond file
	// lookups for the rest of the process lifetime.
	if err := st.populateFileIDIndexLocked(); err != nil {
		conn.Close()
		db.Close()
		return nil, fmt.Errorf("store_ladybug: populate file-id index: %w", err)
	}
	return st, nil
}

// populateFileIDIndexLocked seeds the fileIDs accelerator from the
// on-disk Node table. Runs once at Open. Streaming the (id, file_path)
// projection keeps the working set small — we don't materialise the
// full node rows for this.
func (s *Store) populateFileIDIndexLocked() error {
	if s.fileIDs == nil {
		s.fileIDs = newFileIDIndex()
	}
	const q = `MATCH (n:Node) WHERE n.file_path <> '' RETURN n.id, n.file_path`
	rows := s.querySelect(q, nil)
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		id, _ := r[0].(string)
		fp, _ := r[1].(string)
		s.fileIDs.add(fp, id)
	}
	return nil
}

// Close closes the underlying connection and database. Drops any
// cached PROJECT_GRAPH declaration first so the engine's catalog
// isn't left holding a dangling projection across the teardown —
// the algo extension's catalog state would otherwise be
// rehydrated on the next Open.
func (s *Store) Close() error {
	s.dropCachedProjection()
	if s.pool != nil {
		s.pool.close()
	}
	if s.conn != nil {
		s.conn.Close()
	}
	if s.db != nil {
		s.db.Close()
	}
	return nil
}

// ResolveMutex returns the resolver-coordination mutex.
func (s *Store) ResolveMutex() *sync.Mutex { return &s.resolveMu }
