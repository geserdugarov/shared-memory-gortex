package store_ladybug

import (
	"fmt"
	"sync"

	lbug "github.com/LadybugDB/go-ladybug"
)

// connPool holds a fixed-size pool of *lbug.Connection bound to
// the same *lbug.Database. The Go binding's `(c *Connection).Query`
// is single-threaded — two goroutines calling Query on the SAME
// Connection race in the cgo layer and SIGSEGV (we saw this with
// the per-repo IndexCtx shadow-swap NodeCount checks under
// MultiIndexer). Giving each goroutine its own Connection
// eliminates the race AND removes the writeMu serialisation
// bottleneck that was making small repos wait 100+ seconds for
// the big repo's bulk drain.
//
// Pool semantics:
//   - get() blocks until a Connection is available (no allocation
//     of new connections beyond the initial size; bounded
//     concurrency by design — ladybug spawns its own internal
//     query workers per connection).
//   - put() returns the Connection to the pool. Always defer put
//     after get.
//   - Each Connection lazy-loads any extensions (FTS / VECTOR /
//     ALGO) that have been registered with the pool. The pool
//     replays the extension list on every checkout against
//     connections that haven't been seen yet for that extension.
type connPool struct {
	db        *lbug.Database
	available chan *lbug.Connection
	closeOnce sync.Once

	extMu      sync.RWMutex
	extensions []string                       // ordered list of extension names
	loadedExt  map[*lbug.Connection]map[string]bool
}

// newConnPool opens `size` connections on db and returns the
// pool. Caller closes via close(). On failure the partially
// created connections are torn down.
func newConnPool(db *lbug.Database, size int) (*connPool, error) {
	if size <= 0 {
		size = 1
	}
	pool := &connPool{
		db:        db,
		available: make(chan *lbug.Connection, size),
		loadedExt: make(map[*lbug.Connection]map[string]bool),
	}
	for i := 0; i < size; i++ {
		conn, err := lbug.OpenConnection(db)
		if err != nil {
			pool.close()
			return nil, fmt.Errorf("connpool: open connection %d/%d: %w", i+1, size, err)
		}
		pool.available <- conn
	}
	return pool, nil
}

// get blocks until a connection is available, applies any
// pending extension loads to it, and returns it. Caller MUST
// defer put.
func (p *connPool) get() *lbug.Connection {
	conn := <-p.available
	p.ensureExtensionsLocked(conn)
	return conn
}

// put returns a connection to the pool. Calling put on a nil
// connection or after close is a no-op.
func (p *connPool) put(conn *lbug.Connection) {
	if conn == nil || p.available == nil {
		return
	}
	defer func() {
		// Re-injecting into a closed channel panics — recover so a
		// late put after close doesn't crash the daemon.
		_ = recover()
	}()
	p.available <- conn
}

// discard removes a connection from circulation instead of returning
// it to the pool, then opens a fresh replacement so the pool stays at
// its configured size. Call this — never put — for any connection
// whose last operation ERRORED.
//
// Rationale: a liblbug connection that errored mid-statement (most
// notably a COPY that hit a duplicated-primary-key Runtime/Copy
// exception during warmup) can be left with poisoned internal
// transaction / pthread-mutex state. Recycling it via put() means the
// next goroutine to check it out and call Prepare dies with
// "prepare: mutex lock failed: Invalid argument" — a panic on a
// completely unrelated goroutine (e.g. the resolver's reconcile
// ReindexEdges pass). Same hazard class as a parse cancelled
// mid-balancing poisoning a tree-sitter parser: a broken handle must
// be closed and replaced, never pooled.
func (p *connPool) discard(conn *lbug.Connection) {
	if conn == nil {
		return
	}
	// Drop any extension-load bookkeeping keyed on the dead handle so
	// the loadedExt map doesn't leak entries for closed connections.
	p.extMu.Lock()
	delete(p.loadedExt, conn)
	p.extMu.Unlock()
	conn.Close()
	if p.available == nil || p.db == nil {
		return
	}
	// Open a replacement so the pool doesn't shrink by one on every
	// error. If reopening fails the pool runs one connection lighter,
	// which is still strictly better than handing out a dead handle.
	fresh, err := lbug.OpenConnection(p.db)
	if err != nil {
		return
	}
	p.put(fresh)
}

// ensureExtensionsLocked loads any registered extensions onto
// the given connection that haven't been loaded there yet.
// Idempotent per (conn, ext) pair.
func (p *connPool) ensureExtensionsLocked(conn *lbug.Connection) {
	p.extMu.RLock()
	exts := append([]string(nil), p.extensions...)
	p.extMu.RUnlock()
	if len(exts) == 0 {
		return
	}
	p.extMu.Lock()
	defer p.extMu.Unlock()
	loaded, ok := p.loadedExt[conn]
	if !ok {
		loaded = make(map[string]bool, len(exts))
		p.loadedExt[conn] = loaded
	}
	for _, ext := range exts {
		if loaded[ext] {
			continue
		}
		// LOAD EXTENSION can soft-fail; the next operation on the
		// connection will surface a real error. Ignore the return
		// here — extensions that aren't available will fail at
		// query time with a clearer message.
		res, err := conn.Query("LOAD EXTENSION " + ext)
		if err == nil && res != nil {
			res.Close()
		}
		loaded[ext] = true
	}
}

// close releases every connection in the pool. Safe to call
// multiple times.
func (p *connPool) close() {
	p.closeOnce.Do(func() {
		close(p.available)
		for conn := range p.available {
			if conn != nil {
				conn.Close()
			}
		}
		p.available = nil
		p.extMu.Lock()
		p.loadedExt = nil
		p.extMu.Unlock()
	})
}
