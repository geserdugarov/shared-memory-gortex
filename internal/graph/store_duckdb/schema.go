package store_duckdb

// schemaSQL is the canonical DDL applied on Open. Statements are
// idempotent (IF NOT EXISTS) so they run cleanly against a fresh DB
// and against an existing one.
//
// Schema choices
//
//   - nodes.id is the primary key. DuckDB doesn't support INSERT OR
//     REPLACE / ON CONFLICT REPLACE in the SQLite shape; we emulate
//     idempotent re-adds via DELETE+INSERT under writeMu in AddNode /
//     AddBatch so the visible semantics match the in-memory store
//     (last-write-wins on every non-id column).
//
//   - edges has a synthetic BIGINT primary key (edge_id, allocated by
//     a Go-side atomic counter -- DuckDB has no AUTOINCREMENT) plus a
//     UNIQUE index over (from_id, to_id, kind, file_path, line) -- the
//     logical edge key the in-memory store uses for dedup. AddEdge
//     pre-deletes any colliding logical row before inserting, so the
//     re-add path is a no-op identity, matching the in-memory "second
//     AddEdge for the same key is a no-op" semantics.
//
//   - meta is a gob-encoded BLOB. nil / empty Meta is stored as NULL.
//
//   - Secondary indexes mirror the in-memory store's hot lookup paths:
//       nodes_by_name      -- FindNodesByName / FindNodesByNameInRepo
//       nodes_by_kind      -- Stats / NodesByKind (group-by-kind)
//       nodes_by_file      -- GetFileNodes, EvictFile
//       nodes_by_repo      -- GetRepoNodes, RepoStats, EvictRepo
//       nodes_by_qual      -- GetNodeByQualName
//       edges_by_from      -- GetOutEdges
//       edges_by_to        -- GetInEdges
const schemaSQL = `
CREATE TABLE IF NOT EXISTS nodes (
    id                 VARCHAR PRIMARY KEY,
    kind               VARCHAR NOT NULL,
    name               VARCHAR NOT NULL,
    qual_name          VARCHAR NOT NULL DEFAULT '',
    file_path          VARCHAR NOT NULL,
    start_line         INTEGER NOT NULL DEFAULT 0,
    end_line           INTEGER NOT NULL DEFAULT 0,
    language           VARCHAR NOT NULL DEFAULT '',
    repo_prefix        VARCHAR NOT NULL DEFAULT '',
    workspace_id       VARCHAR NOT NULL DEFAULT '',
    project_id         VARCHAR NOT NULL DEFAULT '',
    absolute_file_path VARCHAR NOT NULL DEFAULT '',
    meta               BLOB
);

CREATE INDEX IF NOT EXISTS nodes_by_name ON nodes(name);
CREATE INDEX IF NOT EXISTS nodes_by_kind ON nodes(kind);
CREATE INDEX IF NOT EXISTS nodes_by_file ON nodes(file_path);
CREATE INDEX IF NOT EXISTS nodes_by_repo ON nodes(repo_prefix);
CREATE INDEX IF NOT EXISTS nodes_by_qual ON nodes(qual_name);

CREATE TABLE IF NOT EXISTS edges (
    edge_id          BIGINT PRIMARY KEY,
    from_id          VARCHAR NOT NULL,
    to_id            VARCHAR NOT NULL,
    kind             VARCHAR NOT NULL,
    file_path        VARCHAR NOT NULL DEFAULT '',
    line             INTEGER NOT NULL DEFAULT 0,
    confidence       DOUBLE  NOT NULL DEFAULT 1.0,
    confidence_label VARCHAR NOT NULL DEFAULT '',
    origin           VARCHAR NOT NULL DEFAULT '',
    tier             VARCHAR NOT NULL DEFAULT '',
    cross_repo       BOOLEAN NOT NULL DEFAULT FALSE,
    meta             BLOB
);

CREATE INDEX IF NOT EXISTS edges_by_from   ON edges(from_id, kind);
CREATE INDEX IF NOT EXISTS edges_by_to     ON edges(to_id, kind);
CREATE UNIQUE INDEX IF NOT EXISTS edges_unique ON edges(from_id, to_id, kind, file_path, line);
`
