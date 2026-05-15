package indexer

import (
	"os"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser/tsalias"
)

// disambiguateBareTypesViaImports is the post-pass that handles bare
// type refs UpgradeBareTypeRefs left alone because the lookup
// returned ≥2 same-repo candidates. The classic case is a TS web app
// that defines two `DashboardSnapshot` types — one in
// `web/src/lib/schema.ts` (a `type` alias) and one in
// `web/src/lib/types.ts` (an `interface`). The bare name has two
// graph nodes; only the consumer's own `import` statement decides
// which one was actually referenced.
//
// We re-read the contract's source file, parse its TS / JS imports,
// and pick the candidate whose graph FilePath matches an imported
// module. When exactly one candidate matches, the meta entry is
// rewritten to its fully-qualified ID so the downstream
// attachInlinedShapes pass can fold its field shape into the
// contract's Meta.
//
// Languages other than TS / JS are skipped — Go disambiguates
// bare-name collisions via package qualification (`pkg.Type`) and the
// in-file resolveTypeInFile pass already handles those.
func (mi *MultiIndexer) disambiguateBareTypesViaImports(cr *contracts.Registry, g *graph.Graph) {
	srcCache := map[string][]byte{}
	importCache := map[string]map[string]string{}

	for _, c := range cr.All() {
		if c.Meta == nil {
			continue
		}
		if !isImportResolvableLang(c.FilePath) {
			continue
		}
		patched := false
		items := cr.ByID(c.ID)
		for i := range items {
			if items[i].FilePath != c.FilePath || items[i].Meta == nil {
				continue
			}
			for _, key := range []string{"response_type", "request_type"} {
				name, _ := items[i].Meta[key].(string)
				if name == "" || strings.Contains(name, "::") {
					continue
				}
				resolved := mi.resolveBareTypeViaImports(c.FilePath, name, g, srcCache, importCache)
				if resolved == "" {
					continue
				}
				items[i].Meta[key] = resolved
				patched = true
			}
		}
		if patched {
			cr.ReplaceByID(c.ID, items)
		}
	}
}

// resolveBareTypeViaImports looks up `name` among the bare-type
// candidates in the merged graph and returns the unambiguous match
// reachable via an import statement in `srcFile`. Returns "" when
// the lookup is still ambiguous or no candidate matches an import
// (so the caller leaves the bare name in place).
func (mi *MultiIndexer) resolveBareTypeViaImports(
	srcFile, name string,
	g *graph.Graph,
	srcCache map[string][]byte,
	importCache map[string]map[string]string,
) string {
	candidates := g.FindNodesByName(name)
	if len(candidates) == 0 {
		return ""
	}
	var typed []*graph.Node
	for _, n := range candidates {
		if n.Kind == graph.KindType || n.Kind == graph.KindInterface {
			typed = append(typed, n)
		}
	}
	if len(typed) < 2 {
		// 0 candidates → nothing to do; 1 candidate would already have
		// been caught by UpgradeBareTypeRefs, so we don't try to redo
		// its work here.
		return ""
	}

	imports, ok := importCache[srcFile]
	if !ok {
		src, hit := srcCache[srcFile]
		if !hit {
			data, found := mi.readFileFromAnyRepo(srcFile)
			if !found {
				srcCache[srcFile] = nil
				importCache[srcFile] = nil
				return ""
			}
			src = data
			srcCache[srcFile] = src
		}
		if len(src) == 0 {
			importCache[srcFile] = nil
			return ""
		}
		aliasMap, aliasPrefix := mi.tsAliasMapFor(srcFile)
		imports = parseTSImports(string(src), srcFile, aliasMap, aliasPrefix)
		importCache[srcFile] = imports
	}
	if len(imports) == 0 {
		return ""
	}
	wantFile, ok := imports[name]
	if !ok {
		return ""
	}
	for _, n := range typed {
		if n.FilePath == wantFile {
			return n.ID
		}
	}
	return ""
}

// tsAliasCache caches the per-repo Collection of tsconfig/jsconfig
// alias maps. Loaded lazily on first lookup for a repoPrefix and
// reused across all import resolutions in the same session. A nil
// entry means "scanned, no usable config" — distinct from "not yet
// scanned" (missing key).
var (
	tsAliasCache   = map[string]*tsalias.Collection{}
	tsAliasCacheMu sync.Mutex
)

// tsAliasMapFor returns the nearest-ancestor alias map for srcFile
// (and the repo prefix the resolved path should be prefixed with).
// srcFile is a repo-prefixed path; we determine the repo by matching
// it against tracked repos, walk that repo's filesystem root for
// config files (cached), and pick the scope nearest to srcFile.
//
// Returns (nil, "") when the repo can't be located or no usable
// config exists — callers must handle that as "no alias resolution
// available, fall through to bare-name behaviour."
func (mi *MultiIndexer) tsAliasMapFor(srcFile string) (*tsalias.Map, string) {
	if srcFile == "" {
		return nil, ""
	}
	for _, m := range mi.AllMetadata() {
		prefix := m.RepoPrefix
		if prefix == "" || !strings.HasPrefix(srcFile, prefix+"/") {
			continue
		}
		coll := loadTSAliasCollection(prefix, m.RootPath)
		if coll == nil {
			return nil, prefix
		}
		rel := strings.TrimPrefix(srcFile, prefix+"/")
		return coll.FindForFile(path.Dir(rel)), prefix
	}
	return nil, ""
}

func loadTSAliasCollection(prefix, rootPath string) *tsalias.Collection {
	tsAliasCacheMu.Lock()
	defer tsAliasCacheMu.Unlock()
	if c, ok := tsAliasCache[prefix]; ok {
		return c
	}
	c := tsalias.Load(rootPath)
	tsAliasCache[prefix] = c
	return c
}

// readFileFromAnyRepo finds the on-disk bytes for a repo-prefixed
// file path by walking tracked-repo metadata. Mirrors readNodeSource
// but takes the path directly so callers don't need a graph node.
func (mi *MultiIndexer) readFileFromAnyRepo(filePath string) ([]byte, bool) {
	if filePath == "" {
		return nil, false
	}
	for _, m := range mi.AllMetadata() {
		prefix := m.RepoPrefix
		if prefix == "" || !strings.HasPrefix(filePath, prefix+"/") {
			continue
		}
		rel := strings.TrimPrefix(filePath, prefix+"/")
		data, ok := readDiskFile(joinPath(m.RootPath, rel))
		if ok {
			return data, true
		}
	}
	return nil, false
}

// joinPath joins a root and relative path with a single separator,
// avoiding the import of "path/filepath" inside this leaf helper so
// the file's surface-area stays minimal.
func joinPath(root, rel string) string {
	if root == "" {
		return rel
	}
	if strings.HasSuffix(root, "/") {
		return root + rel
	}
	return root + "/" + rel
}

// readDiskFile is a small indirection so tests can swap in an
// in-memory fixture without touching the on-disk reader.
var readDiskFile = func(absPath string) ([]byte, bool) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, false
	}
	return data, true
}

// tsImportRe matches `import { A, B as C } from '...'`,
// `import type { A } from '...'`, `import A from '...'`, and
// `import * as A from '...'`. Capture groups:
//
//	1: named-import body (between `{` and `}`) — empty for default /
//	   namespace imports, in which case group 4 carries the bound
//	   name.
//	2: default / namespace identifier (the bare ident or `* as X`)
//	3: module path
var tsImportRe = regexp.MustCompile(
	`(?m)^\s*import\s+(?:type\s+)?(?:\{([^}]*)\}|([A-Za-z_$][\w$]*|\*\s+as\s+[A-Za-z_$][\w$]*))(?:\s*,\s*\{([^}]*)\})?\s+from\s+['"]([^'"]+)['"]`,
)

// parseTSImports walks the import lines of a TypeScript / JavaScript
// source file and returns name → absolute repo-prefixed file path.
// `srcFile` is the importing file's own repo-prefixed path; it
// anchors relative module specifiers like `'./schema'`. Bare module
// specifiers (`'react'`) are skipped — they don't resolve to a graph
// file the local repo owns. tsconfig-style path aliases (`@/lib/api`,
// `$utils/format`) ARE resolved when an alias map is provided.
func parseTSImports(src, srcFile string, aliasMap *tsalias.Map, repoPrefix string) map[string]string {
	matches := tsImportRe.FindAllStringSubmatch(src, -1)
	if len(matches) == 0 {
		return nil
	}
	out := map[string]string{}
	srcDir := path.Dir(srcFile)
	for _, m := range matches {
		named := m[1]
		defaultOrStar := m[2]
		extraNamed := m[3]
		modulePath := m[4]
		resolved := resolveTSModulePath(modulePath, srcDir, aliasMap, repoPrefix)
		if resolved == "" {
			continue
		}
		for _, name := range splitTSImportClause(named) {
			out[name] = resolved
		}
		for _, name := range splitTSImportClause(extraNamed) {
			out[name] = resolved
		}
		if defaultOrStar != "" {
			ident := defaultOrStar
			if strings.HasPrefix(ident, "*") {
				if i := strings.LastIndex(ident, " "); i >= 0 {
					ident = strings.TrimSpace(ident[i+1:])
				}
			}
			if ident != "" {
				out[ident] = resolved
			}
		}
	}
	return out
}

// splitTSImportClause unpacks a brace-delimited import list like
// `Foo, Bar as Baz, type Qux` into the local-binding names a caller
// would reference (`Foo`, `Baz`, `Qux`). The `type` keyword and
// `as <alias>` rebinds are normalised; commas inside the body are
// the only separator we care about.
func splitTSImportClause(body string) []string {
	if body == "" {
		return nil
	}
	parts := strings.Split(body, ",")
	out := make([]string, 0, len(parts))
	for _, raw := range parts {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		entry = strings.TrimPrefix(entry, "type ")
		entry = strings.TrimSpace(entry)
		if i := strings.Index(entry, " as "); i >= 0 {
			entry = strings.TrimSpace(entry[i+4:])
		}
		if entry == "" {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// resolveTSModulePath turns a TS/JS module specifier into the
// repo-prefixed file path of the imported source, or "" when the
// specifier is unresolvable (third-party module with no matching
// alias). Resolution order:
//
//  1. Relative path (`./foo`, `../bar/baz`) — anchored at srcDir.
//  2. tsconfig path alias (`@/lib/foo`, `$utils/format`) — looked up
//     against aliasMap; the resolved repo-relative target is prefixed
//     with repoPrefix so it lines up with graph FilePaths.
//
// We don't probe the disk; the caller matches the resolved path
// against a candidate's FilePath, so we just append the canonical
// `.ts` extension when none is present. `.tsx` / `.js` / `.jsx`
// paths are returned as-is when the user wrote them explicitly.
// Directory imports resolving to `index.*` are NOT handled — the
// resolver returns the bare-stem path; if the candidate type lives
// in `<dir>/index.ts` the upgrade falls through and the bare name
// is left in place (acceptable: the dashboard still renders the
// bare type chip).
func resolveTSModulePath(modulePath, srcDir string, aliasMap *tsalias.Map, repoPrefix string) string {
	if modulePath == "" {
		return ""
	}
	if strings.HasPrefix(modulePath, "./") || strings.HasPrefix(modulePath, "../") {
		joined := path.Clean(path.Join(srcDir, modulePath))
		switch path.Ext(joined) {
		case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
			return joined
		}
		return joined + ".ts"
	}
	// Bare specifier — try alias resolution. tsalias.Resolve returns a
	// repo-relative path with the extension stripped; we re-prefix it
	// with repoPrefix so it lines up with graph FilePaths, then add
	// the canonical `.ts` so the caller can match against an indexed
	// file. Returning "" still leaves the bare type name in place.
	if aliasMap == nil {
		return ""
	}
	repoRel := tsalias.Resolve(aliasMap, modulePath)
	if repoRel == "" {
		return ""
	}
	full := repoRel
	if repoPrefix != "" {
		full = repoPrefix + "/" + repoRel
	}
	switch path.Ext(full) {
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
		return full
	}
	return full + ".ts"
}

// isImportResolvableLang reports whether the contract source file
// uses an import system this resolver can parse. TypeScript and
// JavaScript files use ES-module imports we understand; Go uses
// package qualification which the in-file pass already handles
// (and would have produced an unambiguous resolution at extraction
// time).
func isImportResolvableLang(filePath string) bool {
	switch path.Ext(filePath) {
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
		return true
	}
	return false
}
