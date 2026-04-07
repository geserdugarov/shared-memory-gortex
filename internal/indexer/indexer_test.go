package indexer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

func setupTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// main.go
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import "fmt"

func main() {
	fmt.Println("hello")
	helper()
}

func helper() {}
`)

	// pkg/util.go
	pkgDir := filepath.Join(dir, "pkg")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	writeFile(t, filepath.Join(pkgDir, "util.go"), `package pkg

type Config struct {
	Port int
}

func NewConfig() *Config {
	return &Config{Port: 8080}
}
`)

	// vendor/ should be excluded.
	vendorDir := filepath.Join(dir, "vendor")
	require.NoError(t, os.MkdirAll(vendorDir, 0o755))
	writeFile(t, filepath.Join(vendorDir, "lib.go"), `package vendor

func Ignored() {}
`)

	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func newTestIndexer(g *graph.Graph) *Indexer {
	reg := parser.NewRegistry()
	reg.Register(languages.NewGoExtractor())
	cfg := config.Default().Index
	cfg.Workers = 2
	return New(g, reg, cfg, zap.NewNop())
}

func TestIndex_SingleGoFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Hello() {}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	result, err := idx.Index(dir)
	require.NoError(t, err)

	assert.Equal(t, 1, result.FileCount)
	assert.Greater(t, result.NodeCount, 0)
	assert.Greater(t, result.EdgeCount, 0)
}

func TestIndex_MultipleFiles(t *testing.T) {
	dir := setupTestDir(t)

	g := graph.New()
	idx := newTestIndexer(g)
	result, err := idx.Index(dir)
	require.NoError(t, err)

	assert.Equal(t, 2, result.FileCount) // main.go + pkg/util.go (vendor excluded)
	assert.Greater(t, result.NodeCount, 4)
}

func TestIndex_ExcludePatterns(t *testing.T) {
	dir := setupTestDir(t)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	// Vendor file should not be indexed.
	nodes := g.FindNodesByName("Ignored")
	assert.Empty(t, nodes)
}

func TestIndex_UnsupportedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "readme.txt"), "hello world")

	g := graph.New()
	idx := newTestIndexer(g)
	result, err := idx.Index(dir)
	require.NoError(t, err)

	assert.Equal(t, 0, result.FileCount)
}

func TestIndexFile_SingleFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	writeFile(t, filePath, `package main

func Original() {}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	origNodes := g.FindNodesByName("Original")
	require.Len(t, origNodes, 1)

	// Modify and re-index single file.
	writeFile(t, filePath, `package main

func Replaced() {}
`)
	require.NoError(t, idx.IndexFile(filePath))

	assert.Empty(t, g.FindNodesByName("Original"))
	assert.Len(t, g.FindNodesByName("Replaced"), 1)
}

func TestMtimeTracking(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	writeFile(t, goFile, `package main

func Hello() {}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	result, err := idx.Index(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, result.FileCount)

	// FileMtimes should be populated with the indexed file.
	mtimes := idx.FileMtimes()
	assert.NotEmpty(t, mtimes, "fileMtimes should be populated after Index()")
	assert.Contains(t, mtimes, "main.go", "fileMtimes should contain the indexed file")
	assert.Greater(t, mtimes["main.go"], int64(0), "mtime should be a positive unix nano value")
}

func TestMtimeIsStale_FreshFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Hello() {}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	// A just-indexed file should not be stale.
	assert.False(t, idx.IsStale("main.go"), "file should not be stale immediately after indexing")
}

func TestMtimeIsStale_ModifiedFile(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	writeFile(t, goFile, `package main

func Hello() {}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	// Modify the file after indexing — ensure mtime actually changes.
	// Some filesystems have coarse mtime resolution, so we sleep briefly.
	time.Sleep(50 * time.Millisecond)
	writeFile(t, goFile, `package main

func HelloModified() {}
`)

	assert.True(t, idx.IsStale("main.go"), "file should be stale after modification")
}

func TestMtimeIsStale_UnknownFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Hello() {}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	// A file not in the index should be treated as stale.
	assert.True(t, idx.IsStale("unknown.go"), "unknown file should be treated as stale")
}

func TestMtimeUpdatedAfterIndexFile(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	writeFile(t, goFile, `package main

func Original() {}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	originalMtime := idx.FileMtimes()["main.go"]

	// Modify and re-index the single file.
	time.Sleep(50 * time.Millisecond)
	writeFile(t, goFile, `package main

func Replaced() {}
`)
	require.NoError(t, idx.IndexFile(goFile))

	updatedMtime := idx.FileMtimes()["main.go"]
	assert.Greater(t, updatedMtime, originalMtime, "mtime should be updated after IndexFile")
	assert.False(t, idx.IsStale("main.go"), "file should not be stale after re-indexing")
}

func TestEvictFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Foo() {}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)
	require.NotEmpty(t, g.FindNodesByName("Foo"))

	n, e := idx.EvictFile(filepath.Join(dir, "main.go"))
	assert.Greater(t, n, 0)
	assert.Greater(t, e, 0)
	assert.Empty(t, g.FindNodesByName("Foo"))
}
