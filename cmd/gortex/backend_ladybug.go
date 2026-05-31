package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/daemon"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_ladybug"
)

// ladybugStmtCacheEnabled reports whether the per-connection
// prepared-statement cache is on. ON by default — it stops the per-call
// re-`Prepare` that leaks liblbug's parse/bind AST (the dominant source
// of unbounded daemon growth) and is validated by the full conformance
// suite + a concurrent -race test. GORTEX_LADYBUG_STMT_CACHE=0/false is
// the kill-switch if a long-running workload ever destabilises it. See
// store_ladybug.Options.PreparedStmtCache.
func ladybugStmtCacheEnabled() bool {
	v := os.Getenv("GORTEX_LADYBUG_STMT_CACHE")
	if v == "" {
		return true
	}
	on, err := strconv.ParseBool(v)
	if err != nil {
		return true
	}
	return on
}

// openLadybugBackend opens (or creates) the ladybug store at
// path. Returns a cleanup func that closes the underlying handle
// — important because ladybug's writer locks the directory and
// a subsequent reopen on the same path would fail until the
// previous handle is closed.
func openLadybugBackend(path string, bufferPoolMB uint64) (graph.Store, func(), error) {
	s, err := store_ladybug.OpenWithOptions(path, store_ladybug.Options{
		BufferPoolMB:      bufferPoolMB,
		PreparedStmtCache: ladybugStmtCacheEnabled(),
	})
	if err != nil {
		// liblbug collapses every open failure — including "another
		// process already holds the lock on this store" — into a single
		// generic status with no message (lbug_state is just Success/Error,
		// and lbug_database_init exposes no error string). A second gortex
		// process on the same store is the most common cause, so name it
		// instead of leaving the user the bare, unactionable status code.
		hint := "if another gortex daemon or server is using this store, stop it first (`gortex daemon status` / `gortex daemon stop`)"
		if pid, ok := daemon.RunningPID(); ok {
			hint = fmt.Sprintf("a gortex daemon is already running (pid %d) — stop it with `gortex daemon stop`, or use `gortex daemon restart`", pid)
		}
		return nil, nil, fmt.Errorf("open ladybug store at %q: %w (%s)", path, err, hint)
	}
	return s, func() { _ = s.Close() }, nil
}

// shrinkToResidentBufferPool re-opens the ladybug store at the resident
// (steady-state) buffer-pool cap once warmup/cold-index is done, freeing
// the cold-index page-cache high-water back to the OS. A no-op for any
// non-ladybug backend (the memory store has no buffer pool) and when the
// store is already at the resident cap (ReopenWithBufferPool short-
// circuits). residentMB of 0 means "use DefaultResidentBufferPoolMB".
func shrinkToResidentBufferPool(g graph.Store, residentMB uint64, logger *zap.Logger) {
	lb, ok := g.(*store_ladybug.Store)
	if !ok {
		return
	}
	stats, err := lb.ReopenWithBufferPool(residentMB)
	if err != nil {
		logger.Warn("daemon: resident buffer-pool reopen failed; staying at cold-index size",
			zap.Error(err))
		return
	}
	logger.Info("daemon: shrank buffer pool to resident size after warmup",
		zap.Uint64("buffer_pool_mb", stats.BufferPoolMB),
		zap.Uint64("rss_before_mib", stats.RSSBeforeBytes>>20),
		zap.Uint64("rss_after_mib", stats.RSSAfterBytes>>20),
		zap.Int64("rss_freed_mib", (int64(stats.RSSBeforeBytes)-int64(stats.RSSAfterBytes))>>20))
}

// startBufferPoolBackstop runs a periodic RSS check that reopens the
// ladybug store at its resident cap when RSS exceeds thresholdMB. This
// is the leak backstop: reopening tears the engine's native heap down
// wholesale, reclaiming the query parse/bind ASTs liblbug orphans per
// prepared-statement destroy (the dominant source of unbounded daemon
// growth). It is a no-op for non-ladybug backends, when thresholdMB is
// 0 (disabled), or when interval <= 0.
//
// Each tick is gated on BufferPoolMB()==residentMB so the backstop only
// engages AFTER the post-warmup shrink has run — never mid cold-index,
// where the store still holds the larger index cap and RSS is expected
// to be high. Returns a stop func to wire into the daemon's shutdown.
func startBufferPoolBackstop(g graph.Store, thresholdMB, residentMB uint64, interval time.Duration, logger *zap.Logger) func() {
	lb, ok := g.(*store_ladybug.Store)
	if !ok || thresholdMB == 0 || interval <= 0 {
		return func() {}
	}
	if residentMB == 0 {
		residentMB = store_ladybug.DefaultResidentBufferPoolMB
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				// Skip until the warmup shrink has dropped us to the
				// resident cap — otherwise we'd reopen mid cold-index.
				if lb.BufferPoolMB() != residentMB {
					continue
				}
				reopened, stats, err := lb.ReopenIfRSSAbove(thresholdMB, residentMB)
				if err != nil {
					logger.Warn("daemon: buffer-pool backstop reopen failed", zap.Error(err))
					continue
				}
				if reopened {
					logger.Info("daemon: buffer-pool backstop reopened store to reclaim native memory",
						zap.Uint64("threshold_mib", thresholdMB),
						zap.Uint64("buffer_pool_mb", stats.BufferPoolMB),
						zap.Uint64("rss_before_mib", stats.RSSBeforeBytes>>20),
						zap.Uint64("rss_after_mib", stats.RSSAfterBytes>>20),
						zap.Int64("rss_freed_mib", (int64(stats.RSSBeforeBytes)-int64(stats.RSSAfterBytes))>>20))
				}
			}
		}
	}()
	return func() { close(done) }
}

// The daemon warm-restart path consults this optional capability
// (cmd/gortex/daemon_state.go: storeNeedsRebuild) to force a full re-index
// when a schema migration crossed a rebuild rung. This assertion keeps the
// concrete store and the daemon's optional-interface check from drifting.
var _ interface{ NeedsRebuild() bool } = (*store_ladybug.Store)(nil)
