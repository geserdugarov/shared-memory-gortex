package indexer

import (
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
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
		imports = parseTSImports(string(src), srcFile)
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
// source file and returns name → absolute repo-relative file path.
// `srcFile` is the importing file's own repo-relative path; it
// anchors relative module specifiers like `'./schema'`. Bare module
// specifiers (`'react'`, `'@/lib/api'`) are skipped — they don't
// resolve to a graph file the local repo owns.
func parseTSImports(src, srcFile string) map[string]string {
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
		resolved := resolveTSModulePath(modulePath, srcDir)
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
// repo-relative file path of the imported source, or "" when the
// specifier is bare (third-party / aliased) and we can't statically
// know the target. We don't probe the disk here — the caller will
// match the resolved path against a candidate's FilePath, so we
// just append the canonical `.ts` extension when none is present.
// `.tsx` / `.js` / `.jsx` paths are returned as-is when the user
// wrote them explicitly. Directory imports resolving to `index.*`
// are NOT handled — the resolver returns the bare-stem path; if
// the candidate type lives in `<dir>/index.ts` the upgrade falls
// through and the bare name is left in place (acceptable: the
// dashboard still renders the bare type chip).
func resolveTSModulePath(modulePath, srcDir string) string {
	if modulePath == "" {
		return ""
	}
	if !strings.HasPrefix(modulePath, "./") && !strings.HasPrefix(modulePath, "../") {
		// Bare specifier (`react`, `@/lib/foo`, etc.). Path aliases
		// like `@/...` aren't statically resolved here — they need a
		// tsconfig parse, which we don't do. The caller falls back to
		// leaving the bare type name in place when this returns "".
		return ""
	}
	joined := path.Clean(path.Join(srcDir, modulePath))
	switch path.Ext(joined) {
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
		return joined
	}
	return joined + ".ts"
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
