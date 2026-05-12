# Architecture

## Architectural Style

Gortex is built as a layered, in-process graph engine. Most features share the
same object graph:

```text
CLI / MCP / HTTP / Daemon / Public API
              |
              v
        Query + Tool Layer
              |
              v
Graph + Search + Contracts + Session State
              ^
              |
Indexer + Parser + Resolver + Enrichment
              ^
              |
          Source Repos
```

The central dependency is `internal/graph.Graph`. Indexers write to it. Query,
analysis, MCP, HTTP, contracts, and daemon sessions read from it.

## Core Data Model

The graph has two primitive records:

- `graph.Node`: file, package, function, method, type, interface, variable,
  field, param, contract, module, table, config key, flag, event, resource,
  image, todo, team, release, license, and more.
- `graph.Edge`: imports, defines, calls, references, implements, overrides,
  tests, reads/writes, throws, dataflow, route handling, ORM models, infra
  dependencies, contract matches, and related relationships.

Each node may carry:

- `RepoPrefix` for multi-repo identity.
- `WorkspaceID` as the hard query and contract boundary.
- `ProjectID` as a soft sub-boundary inside a workspace.
- `Meta` for signatures, docs, origins, confidence, coverage, ownership, and
  language-specific facts.

## Ingestion Pipeline

The indexer path is the write side of the system.

```text
Repository root
    |
    v
Walk files and apply excludes
    |
    v
Detect language with parser.Registry
    |
    v
Extractor emits nodes + edges (+ optional parse tree)
    |
    v
Coverage/domain passes add todos, licenses, ownership, sql, configs, etc.
    |
    v
Apply repo prefix / workspace / project identity
    |
    v
Graph.AddNode / Graph.AddEdge
    |
    +--> Contract extractors collect HTTP, gRPC, topics, DI, env, deps
    |
    v
Resolver lifts unresolved edges to concrete graph node IDs
    |
    v
Optional semantic enrichment confirms/adds/refutes edges
    |
    v
Build search index and commit contract registry
```

Relevant packages:

- `internal/parser`: extractor interface and registry.
- `internal/parser/languages`: hand-written language extractors.
- `internal/parser/forest`: long-tail signature-only extractors.
- `internal/indexer`: repo walking, indexing, incremental reindexing, watchers.
- `internal/resolver`: symbol resolution, cross-repo resolution, inferred
  implementations and overrides.
- `internal/contracts`: contract extraction, registry, matching, validation.
- `internal/semantic`: Go analysis, SCIP, and LSP provider orchestration.
- `internal/search`: BM25, Bleve, vector, hybrid search.

## Query and Tool Layer

`internal/query.Engine` provides graph operations for the rest of the system:

- direct symbol lookup
- file summaries
- dependency and dependent traversal
- call chains and callers
- usages
- implementations and overrides
- search-backed symbol discovery

`internal/mcp.Server` wraps the query engine with AI-friendly tools. It adds
session state, token-savings tracking, frecency, feedback, combo reranking,
diagnostics broadcasting, resources, prompts, editing tools, and scoped
multi-repo behavior.

## Serving Surfaces

```text
                 +----------------+
                 | internal/graph |
                 +----------------+
                         ^
                         |
       +-----------------+-----------------+
       |                                   |
+--------------+                   +---------------+
| query.Engine |                   | indexer.Indexer|
+--------------+                   +---------------+
       |
       v
+----------------+
| internal/mcp   |
| tools/resources|
+----------------+
       |
       +-------------------+--------------------+
       |                   |                    |
       v                   v                    v
gortex mcp stdio     HTTP /v1 tools       daemon dispatcher
```

Main surfaces:

- CLI commands in `cmd/gortex` construct the graph stack directly.
- MCP stdio serves tool calls to agents.
- HTTP server exposes MCP tools and graph/dashboard endpoints under `/v1/*`.
- Daemon accepts Unix socket sessions and dispatches MCP or control requests.
- Public API in `pkg/gortex` embeds a smaller subset of the same engine.

## Daemon Architecture

The daemon is an optional long-lived process. It exists to avoid each agent
window building its own graph.

```text
AI clients / CLI proxies
        |
        v
Unix socket handshake
        |
        v
internal/daemon.Server
        |
        +--> Control RPC: track, untrack, reload, status, shutdown
        |
        +--> MCP frames
                |
                v
        daemon MCP dispatcher
                |
                v
        internal/mcp.Server
                |
                v
        Shared graph, search, contracts, sessions
```

Daemon startup builds the same graph/indexer/query/MCP stack as `gortex mcp`.
It can warm-start from snapshots, open the socket before warmup completes, then
reconcile tracked repos in the background. It also manages:

- periodic graph snapshots
- savings flushes
- reconciliation janitor
- multi-repo watcher
- optional embeddings
- optional semantic/LSP providers
- multi-server routing via `~/.gortex/servers.toml`

## Multi-Repo Model

Multi-repo mode uses `internal/indexer.MultiIndexer`.

```text
Global config repos
      |
      v
For each repo:
  load .gortex.yaml
  resolve repo prefix
  resolve workspace/project slugs
  index into shared graph
      |
      v
Shared graph with prefixed IDs
      |
      +--> cross-repo resolver
      +--> contract edge reconciliation
      +--> global graph passes once
```

Important boundaries:

- Repo prefix prevents node ID collisions.
- Workspace slug is the hard boundary for query scope and contract matching.
- Project slug narrows matching inside a workspace.
- Explicit cross-workspace dependencies are modeled in config.

## Contract Layer

The contract system finds provider/consumer surfaces and lets graph traversals
cross service boundaries.

```text
Source code / manifests / configs
        |
        v
Contract extractors
        |
        v
contracts.Registry
        |
        v
contracts.Match
        |
        v
Graph edges:
  symbol -> contract     provides / consumes
  consumer -> provider   matches
  handler -> route       handles_route
```

Contract types include HTTP, gRPC, GraphQL, topics, WebSocket, env, OpenAPI,
dependency, and DI. Matching is bounded by effective workspace and project.

## Agent Integration

`gortex init` and `gortex install` use `internal/agents` adapters. Each adapter
follows a `Detect -> Plan -> Apply` contract and writes the files needed by a
specific assistant.

```text
gortex init
    |
    +--> create .gortex marker
    +--> optional index for overview and community skills
    +--> generate CLAUDE.md / routing / SKILL.md content
    +--> apply selected agent adapters
    +--> update global config so daemon can track the repo
```

Claude Code hooks enter through `internal/hooks.Run`, which dispatches
PreToolUse, PreCompact, Stop, and SessionStart events. Hook behavior is
best-effort and intentionally degrades silently if the server is unavailable.

## Evaluation Architecture

There are two evaluation areas:

- Go CLI eval commands under `cmd/gortex/eval*.go` for recall, embedders,
  token savings, and SWE-bench passthrough.
- Python SWE-bench harness under `eval/`, where `eval/run_eval.py` launches
  containers, configures a `GortexAgent`, and compares baseline/native modes.

```text
eval/run_eval.py
      |
      +--> load model + mode YAML
      +--> load SWE-bench instances
      +--> start Docker environment
      +--> expose Gortex bridge commands / eval server
      +--> run agent
      +--> collect patch and metrics
```

