# Project Overview

## What This Project Is

Gortex is a code intelligence engine written primarily in Go. It walks one or
more repositories, parses source files across a large language set, builds an
in-memory knowledge graph, and exposes that graph to AI coding tools and
external integrations.

The core promise is read-efficient code understanding: instead of an agent
opening many files, it can ask graph-backed questions such as "find usages",
"get callers", "give me editing context", "what tests cover this symbol", or
"what contracts cross this service boundary".

## Main Users

- AI coding assistants using MCP tools.
- Developers using the `gortex` CLI directly.
- Editor, dashboard, or CI integrations using the HTTP API.
- Evaluation runs measuring whether graph context improves SWE-bench outcomes.
- Go applications embedding the engine through `pkg/gortex`.

## Major Capabilities

- Multi-language indexing through hand-written extractors and forest-backed
  tree-sitter extractors.
- A sharded, thread-safe in-memory graph for files, symbols, dependencies,
  contracts, dataflow, infrastructure, ownership, test links, and metadata.
- Query APIs for symbols, dependencies, dependents, callers, call chains,
  usages, implementations, overrides, and file summaries.
- MCP tools for graph navigation, editing, planning, diagnostics, analysis,
  contract checks, dataflow, AST search, and multi-repo management.
- HTTP API under `/v1/*` for health, tools, stats, graph dumps, events,
  dashboard data, contracts, communities, and overlays.
- Optional daemon process that shares one graph across multiple agent sessions.
- Multi-repo workspaces with repo prefixes, workspace/project slugs, cross-repo
  resolution, and contract matching.
- Semantic enrichment via Go analysis, SCIP, and LSP-backed providers.
- Search through BM25/Bleve text search plus optional vector search.
- Agent setup for Claude Code, Cursor, Kiro, VS Code, Windsurf, Continue.dev,
  Cline, OpenCode, Antigravity, Codex, Gemini, Zed, Aider, Kilo Code, and
  OpenClaw.
- Evaluation harnesses for recall, embedders, wire format token savings, and
  SWE-bench style experiments.

## Repository Layout

```text
.
|-- cmd/gortex/        CLI entry point and subcommands
|-- pkg/gortex/        Small public Go API for embedding the engine
|-- internal/graph/    Core graph data model and sharded storage
|-- internal/parser/   Parser registry, tree-sitter bridge, extractors
|-- internal/indexer/  Repository walking, parsing, indexing, watching
|-- internal/resolver/ Symbol and cross-repo edge resolution
|-- internal/query/    Higher-level graph traversals and search access
|-- internal/search/   BM25, Bleve, hybrid text/vector search
|-- internal/mcp/      MCP server, tools, resources, prompts, sessions
|-- internal/server/   HTTP /v1 API wrapper around MCP and graph data
|-- internal/daemon/   Shared long-lived process, Unix socket protocol
|-- internal/contracts/ API, DI, env, route, topic, dependency matching
|-- internal/semantic/ Semantic enrichment providers and LSP integration
|-- internal/analysis/ Dead code, hotspots, cycles, communities, guards
|-- internal/agents/   Agent adapter contracts and per-agent integrations
|-- internal/hooks/    Claude Code hook dispatch and graph augmentation
|-- internal/config/   Global config, per-repo config, excludes, guards
|-- internal/persistence/ Snapshot, feedback, combo, frecency storage
|-- docs/              User-facing documentation
|-- bench/             Benchmarks and fixtures
|-- eval/              Python SWE-bench evaluation harness
```

## Key Entry Points

- `cmd/gortex/main.go` calls `execute()`.
- `cmd/gortex/root.go` defines the root Cobra command and persistent flags.
- `cmd/gortex/index.go` runs one-shot indexing.
- `cmd/gortex/mcp.go` starts the MCP server and optional HTTP sidecar.
- `cmd/gortex/server.go` starts the standalone HTTP API.
- `cmd/gortex/daemon.go` starts the shared daemon.
- `cmd/gortex/init.go` wires the current repository into supported agents.
- `pkg/gortex/api.go` exposes a small embeddable engine API.
- `eval/run_eval.py` is the Python SWE-bench runner entry point.

## Primary Runtime Modes

```text
CLI one-shot:
  gortex index .       -> build graph, print stats
  gortex query ...     -> build graph, run direct query

MCP embedded:
  gortex mcp --index . -> MCP over stdio, optional watch/cache/server sidecar

HTTP API:
  gortex server --index . -> /v1/* API, graph/events/dashboard endpoints

Daemon:
  gortex daemon start -> Unix socket server shared by agents and CLI commands

Agent setup:
  gortex install      -> user-level setup
  gortex init         -> repo-level MCP configs, hooks, routing, skills
```

## Important Design Choices

- The graph is in memory and sharded by node ID to reduce write contention.
- Indexing is additive and restart-friendly through snapshots keyed by repo and
  commit hash.
- Multi-repo indexing uses one shared graph plus repo prefixes to avoid node ID
  collisions.
- Symbol resolution and global graph passes are deliberately serialized or
  batched in multi-repo paths to avoid races and repeated O(global) work.
- MCP is the main AI-facing API; HTTP is a wrapper around the same tool surface.
- The daemon owns shared state and session isolation so multiple agents do not
  each need a separate full graph process.
- Semantic enrichment is optional and additive. The AST graph remains usable
  when LSP, SCIP, or external providers are unavailable.

## Local Verification Note

The repository declares Go `1.26.2` in `go.mod`, but the local environment used
for this review did not have `go` on `PATH`. These notes are based on source
inspection and existing project documentation, not on executing the Go test
suite.
