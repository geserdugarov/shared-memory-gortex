# Gortex Project Notes

These notes summarize the project and its architecture from the repository
source as of 2026-05-12.

## Files

- [project-overview.md](project-overview.md) - what Gortex is, who it serves,
  major capabilities, repository layout, and operational notes.
- [architecture.md](architecture.md) - primary runtime layers, package
  responsibilities, data model, and component boundaries.
- [ascii-diagrams.md](ascii-diagrams.md) - simplified ASCII diagrams for the
  main flows: indexing, serving, daemon mode, contracts, agent setup, and eval.

## One-Screen Summary

Gortex is a Go code intelligence engine. It indexes repositories into an
in-memory graph of files, symbols, calls, imports, contracts, infrastructure
resources, dataflow edges, and metadata. That graph is exposed through:

- a Cobra CLI in `cmd/gortex`
- an MCP server in `internal/mcp`
- an HTTP `/v1/*` API in `internal/server`
- an optional long-lived daemon in `internal/daemon`
- a public embedding API in `pkg/gortex`
- a Python SWE-bench evaluation harness in `eval`

Simplified shape:

```text
Repositories
    |
    v
Parser + Indexer + Resolver + Enrichment
    |
    v
In-memory Graph + Search Index + Contract Registry
    |
    +--> CLI queries
    +--> MCP tools/resources/prompts
    +--> HTTP API and dashboard data
    +--> Daemon sessions for AI coding agents
```

