package store_ladybug

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// Compile-time assertions: *Store satisfies the FileMtime persistence
// capability. Lifting per-file mtimes off the daemon's gob+gzip
// snapshot and into the FileMtime node table is what lets the warm-
// restart path read incremental-reindex state through ladybug instead
// of through a sidecar file.
var (
	_ graph.FileMtimeWriter = (*Store)(nil)
	_ graph.FileMtimeReader = (*Store)(nil)
)

// BulkSetFileMtimes upserts the per-file modification times under one
// repo prefix. Mirrors the in-memory Indexer's fileMtimes map but
// makes the data durable in ladybug so the next daemon restart can
// reconstruct it without replaying a gob snapshot.
//
// Empty input is a no-op. Empty repoPrefix is allowed (the in-memory
// indexer keys mtimes the same way for single-repo daemons).
func (s *Store) BulkSetFileMtimes(repoPrefix string, mtimes map[string]int64) error {
	if len(mtimes) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// UNWIND + MERGE: one Cypher Execute per chunk amortises the parse
	// + plan over the whole batch. 5k is the same chunk size the rest
	// of the indexer's batched writes use; the relevant constant lives
	// next to the AddBatch path.
	rows := make([]map[string]any, 0, len(mtimes))
	for id, mt := range mtimes {
		if id == "" {
			continue
		}
		// The incoming map is keyed by RELATIVE path (the indexer keys
		// fileMtimes by relKey). PRIMARY KEY(file_id) on the FileMtime
		// table is global, but relative paths are NOT unique across
		// repos: every tree-sitter grammar repo carries `src/parser.c`,
		// `grammar.js`, `binding.gyp`, etc. Storing the bare relative
		// path as file_id let those rows collide cross-repo — the
		// last-writing repo's MERGE overwrote the row's repo_prefix, so
		// every other repo sharing that path silently lost its mtimes
		// and re-indexed (full COPY) on every warm restart. Prefix the
		// id with the repo prefix to make it globally unique, matching
		// the `repoPrefix + "/" + relPath` convention node file_paths
		// already use. LoadFileMtimes strips the prefix back off.
		fileID := id
		if repoPrefix != "" {
			fileID = repoPrefix + "/" + id
		}
		rows = append(rows, map[string]any{
			"file_id":     fileID,
			"repo_prefix": repoPrefix,
			"mtime_ns":    mt,
		})
	}
	for i := 0; i < len(rows); i += kuzuBatchChunkSize {
		end := i + kuzuBatchChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		const q = `
UNWIND $rows AS row
MERGE (m:FileMtime {file_id: row.file_id})
SET m.repo_prefix = row.repo_prefix,
    m.mtime_ns    = row.mtime_ns`
		s.runWriteLocked(q, map[string]any{"rows": rows[i:end]})
	}
	return nil
}

// LoadFileMtimes returns the per-file mtimes for one repo prefix as a
// fresh map. Empty repo prefix returns every recorded mtime — the
// daemon doesn't currently call it that way, but the unsuffixed shape
// keeps the function useful for ad-hoc probes.
//
// The query goes through the read path's degraded-on-error wrapper
// (querySelect → querySelectInner), so a transient IO exception
// returns an empty map rather than killing the daemon. Worst case the
// warmup falls back to TrackRepoCtx for that repo, which is exactly
// what the snapshot-less path used to do.
func (s *Store) LoadFileMtimes(repoPrefix string) map[string]int64 {
	var (
		q    string
		args map[string]any
	)
	if repoPrefix == "" {
		q = `MATCH (m:FileMtime) RETURN m.file_id, m.mtime_ns`
		args = nil
	} else {
		q = `MATCH (m:FileMtime) WHERE m.repo_prefix = $repo RETURN m.file_id, m.mtime_ns`
		args = map[string]any{"repo": repoPrefix}
	}
	rows := s.querySelect(q, args)
	if len(rows) == 0 {
		return nil
	}
	// Strip the repo prefix BulkSetFileMtimes prepends so the returned
	// keys are relative paths again — that's what the indexer's
	// fileMtimes map / IsStale comparison expect. Tolerate rows written
	// by the pre-fix code (bare relative file_id): when the prefix isn't
	// present we use the id verbatim, so a store mid-migration loads
	// both shapes without re-indexing the repos that were never
	// collision victims.
	strip := ""
	if repoPrefix != "" {
		strip = repoPrefix + "/"
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		id, _ := r[0].(string)
		if id == "" {
			continue
		}
		if strip != "" {
			id = strings.TrimPrefix(id, strip)
		}
		out[id] = asInt64(r[1])
	}
	return out
}
