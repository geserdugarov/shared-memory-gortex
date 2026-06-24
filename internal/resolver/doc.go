// Package resolver lands cross-reference, import, and framework-dispatch edges
// the per-file extractors leave on `unresolved::` placeholders.
//
// Caching strategy. The batch resolver deliberately does NOT use a
// fixed-capacity LRU for its hot lookups. Each pass (relative-import binding,
// Rust/Lua/Razor module resolution, framework synthesis, cross-repo joins)
// builds its index maps once from the graph, uses them for that pass, and lets
// them fall out of scope when the pass returns. This per-pass-clear strategy is
// strictly better than an LRU here: it incurs no eviction bookkeeping, can
// never serve a stale cross-pass entry, and is bounded by the actual pass work
// set rather than a guessed capacity. There is therefore no
// resolver-cache-size knob — the maps are not user-tunable because they are not
// retained.
//
// The one resolver-adjacent cache that IS long-lived is the compile-DB
// include-dir set built for C/C++ include resolution: it is keyed by repo root
// and survives across reindexes, so it is bounded by a memory budget
// (GORTEX_RESOLVER_CACHE_MAX_MB; unset = unbounded) via a small LRU in the
// indexer package. That budget exists purely to cap a long-lived per-repo cache
// and has no effect on hot resolution.
package resolver
