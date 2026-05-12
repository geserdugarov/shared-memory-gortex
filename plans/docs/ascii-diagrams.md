# ASCII Diagrams

## 1. System Context

```text
                    +----------------------+
                    |  AI coding agents    |
                    |  Claude/Cursor/etc.  |
                    +----------+-----------+
                               |
                               | MCP stdio or daemon socket
                               v
+-------------+       +-------------------+       +------------------+
| Developers  | ----> | gortex CLI        | ----> | Gortex engine    |
+-------------+       +-------------------+       +------------------+
                               |                         |
                               | HTTP /v1                |
                               v                         v
                    +----------------------+   +----------------------+
                    | Web UI / CI / tools  |   | Source repositories |
                    +----------------------+   +----------------------+
```

## 2. Simplified Layer Diagram

```text
Presentation / Integration
  cmd/gortex, internal/mcp, internal/server, internal/daemon, pkg/gortex

Application Logic
  internal/query, internal/analysis, internal/contracts, internal/agents

Indexing and Enrichment
  internal/indexer, internal/parser, internal/resolver, internal/semantic

Storage and Indexes
  internal/graph, internal/search, internal/persistence, internal/config
```

## 3. Indexing Flow

```text
 repo root
    |
    v
+----------------+
| walk files     |
| apply excludes |
+-------+--------+
        |
        v
+----------------+
| detect language|
| parser.Registry|
+-------+--------+
        |
        v
+-----------------------+
| language extractor    |
| nodes, edges, tree    |
+-----------+-----------+
            |
            v
+-----------------------+
| coverage/domain passes|
| todos, ownership, sql |
| configs, infra, etc.  |
+-----------+-----------+
            |
            v
+-----------------------+
| stamp repo/workspace  |
| add nodes and edges   |
+-----------+-----------+
            |
            +----------------------+
            |                      |
            v                      v
+--------------------+    +----------------------+
| resolver           |    | contract extractors  |
| unresolved -> real |    | registry + graph     |
+---------+----------+    +----------+-----------+
          |                          |
          v                          v
+--------------------+    +----------------------+
| semantic enrich    |    | search index         |
| go/types, SCIP,LSP |    | BM25/Bleve/vector    |
+---------+----------+    +----------+-----------+
          |                          |
          +------------+-------------+
                       v
              +----------------+
              | usable graph   |
              +----------------+
```

## 4. Query Serving Flow

```text
 Client request
      |
      v
+--------------------+
| CLI / MCP / HTTP   |
+---------+----------+
          |
          v
+--------------------+
| tool or command    |
| argument parsing   |
+---------+----------+
          |
          v
+--------------------+
| query.Engine       |
| traversals/search  |
+---------+----------+
          |
          +---------------------+
          |                     |
          v                     v
+--------------------+  +--------------------+
| internal/graph     |  | internal/search    |
| nodes + edges      |  | ranked symbols     |
+---------+----------+  +----------+---------+
          |                        |
          +-----------+------------+
                      v
              formatted response
```

## 5. MCP Server Shape

```text
MCP client
   |
   v
JSON-RPC over stdio or daemon proxy
   |
   v
+--------------------------------+
| internal/mcp.Server            |
| - core graph tools             |
| - coding/editing tools         |
| - analysis tools               |
| - LSP diagnostics/actions      |
| - resources and prompts        |
| - session/token/feedback state |
+----------------+---------------+
                 |
                 v
    +------------+-------------+
    | graph + query + indexer  |
    +--------------------------+
```

## 6. HTTP API Shape

```text
HTTP client
   |
   v
GET /v1/health
GET /v1/stats
GET /v1/graph
GET /v1/events
POST /v1/tools/{name}
   |
   v
+-------------------------+
| internal/server.Handler |
+------------+------------+
             |
             +--> direct graph/dashboard endpoints
             |
             +--> MCP tool dispatch
                      |
                      v
               internal/mcp.Server
```

## 7. Daemon Mode

```text
        +----------------+
        | Agent window A |
        +-------+--------+
                |
        +-------v--------+       +----------------+
        | Agent window B | ----> | Unix socket    |
        +-------+--------+       +-------+--------+
                |                        |
        +-------v--------+               v
        | CLI proxy      |       +------------------------+
        +----------------+       | internal/daemon.Server |
                                 +-----------+------------+
                                             |
                     +-----------------------+----------------------+
                     |                                              |
                     v                                              v
          control requests                                  MCP requests
          track/status/reload                               tool calls
                     |                                              |
                     v                                              v
          realController                              internal/mcp.Server
                     |                                              |
                     +----------------------+-----------------------+
                                            v
                                  shared graph state
```

## 8. Multi-Repo Graph

```text
~/.config/gortex/config.yaml
          |
          v
  active repos / project
          |
          v
+-------------------+     +-------------------+     +-------------------+
| repo: frontend    |     | repo: backend     |     | repo: shared-lib  |
| prefix: frontend  |     | prefix: backend   |     | prefix: shared    |
+---------+---------+     +---------+---------+     +---------+---------+
          |                         |                         |
          +-------------+-----------+-------------+-----------+
                        |                         |
                        v                         v
              +-------------------------------------------+
              | shared graph                              |
              | node ids prefixed by repo                 |
              | workspace/project stamped on each node    |
              +----------------------+--------------------+
                                     |
                                     v
                         cross-repo resolver and queries
```

## 9. Contract Matching

```text
provider code                      consumer code
    |                                  |
    v                                  v
http route / grpc / topic        http call / grpc / topic
    |                                  |
    v                                  v
provider Contract                consumer Contract
    |                                  |
    +---------------+------------------+
                    |
                    v
          same workspace + project?
                    |
             yes ---+--- no
              |         |
              v         v
        EdgeMatches   orphan provider/consumer
              |
              v
call graph can cross service boundary
```

## 10. Watch and Incremental Reindex

```text
file edit / branch switch / janitor sweep
             |
             v
+-----------------------+
| indexer.Watcher or    |
| MultiWatcher          |
+-----------+-----------+
            |
            v
+-----------------------+
| evict changed file    |
| parse changed file    |
| patch graph           |
+-----------+-----------+
            |
            v
+-----------------------+
| resolve affected refs |
| rebuild/update search |
| rerun analyses        |
+-----------+-----------+
            |
            v
notifications/events/resources updated
```

## 11. Agent Initialization

```text
gortex init
    |
    +--> ensure .gortex/ marker
    |
    +--> optional index
    |       |
    |       +--> CLAUDE.md overview
    |       +--> community skill routing
    |
    +--> agent registry
    |       |
    |       +--> Detect
    |       +--> Plan
    |       +--> Apply
    |
    +--> writes per-agent MCP configs, hooks, instructions
    |
    +--> updates global config for daemon tracking
```

## 12. Evaluation Harness

```text
model config + mode config
          |
          v
   eval/run_eval.py
          |
          +--> load SWE-bench instances
          +--> start Docker environment
          +--> start or bridge Gortex
          +--> run GortexAgent
          +--> collect patch + costs + tool metrics
          v
       results/
```

