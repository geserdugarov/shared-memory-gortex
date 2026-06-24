package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewCppIncludeDirCache_EnvBudget(t *testing.T) {
	t.Setenv("GORTEX_RESOLVER_CACHE_MAX_MB", "7")
	assert.Equal(t, int64(7)<<20, newCppIncludeDirCache().maxBytes)

	t.Setenv("GORTEX_RESOLVER_CACHE_MAX_MB", "")
	assert.Equal(t, int64(0), newCppIncludeDirCache().maxBytes, "unset = unbounded")

	t.Setenv("GORTEX_RESOLVER_CACHE_MAX_MB", "0")
	assert.Equal(t, int64(0), newCppIncludeDirCache().maxBytes, "0 = unbounded")
}

// TestCppIncludeDirCache_Eviction pins that a tiny memory budget evicts the
// least-recently-used per-repo include-dir set.
func TestCppIncludeDirCache_Eviction(t *testing.T) {
	c := newCppIncludeDirCache()
	c.maxBytes = 200 // tiny budget; each entry below is ~89 bytes

	mk := func(f string) map[string]cppTU {
		return map[string]cppTU{f: {file: f, includeDirs: []string{"inc"}}}
	}
	c.put("repoA", mk("a.c"))
	c.put("repoB", mk("b.c"))
	c.put("repoC", mk("c.c"))

	_, okA := c.get("repoA")
	assert.False(t, okA, "least-recently-used repoA evicted under the budget")
	_, okB := c.get("repoB")
	assert.True(t, okB, "repoB retained")
	_, okC := c.get("repoC")
	assert.True(t, okC, "most-recently-used repoC retained")
}

// TestCppIncludeDirCache_GetPromotes pins that a get refreshes recency so the
// promoted entry survives a later eviction.
func TestCppIncludeDirCache_GetPromotes(t *testing.T) {
	c := newCppIncludeDirCache()
	c.maxBytes = 200

	mk := func(f string) map[string]cppTU {
		return map[string]cppTU{f: {file: f, includeDirs: []string{"inc"}}}
	}
	c.put("repoA", mk("a.c"))
	c.put("repoB", mk("b.c"))
	c.get("repoA")            // promote A ahead of B
	c.put("repoC", mk("c.c")) // evicts the now-LRU repoB

	_, okA := c.get("repoA")
	assert.True(t, okA, "promoted repoA survives")
	_, okB := c.get("repoB")
	assert.False(t, okB, "repoB evicted as least-recently-used")
}

// TestCppIncludeDirCache_UnboundedKeepsAll pins that the default (no budget)
// retains every entry.
func TestCppIncludeDirCache_UnboundedKeepsAll(t *testing.T) {
	c := newCppIncludeDirCache() // maxBytes 0
	for _, r := range []string{"r1", "r2", "r3"} {
		c.put(r, map[string]cppTU{r: {file: r}})
	}
	for _, r := range []string{"r1", "r2", "r3"} {
		_, ok := c.get(r)
		assert.True(t, ok, "unbounded cache retains %s", r)
	}
}
