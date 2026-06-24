package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zzet/gortex/internal/graph"
)

func luaRequire(g graph.Store, from, target string, roblox string) *graph.Edge {
	e := &graph.Edge{From: from, To: "unresolved::import::" + target, Kind: graph.EdgeImports}
	if roblox != "" {
		e.Meta = map[string]any{"roblox_path": roblox}
	}
	g.AddEdge(e)
	return e
}

// TestResolveLuaRequires_StringModule pins the classic dotted require:
// require("mod.a.b.c") binds to the repo-relative mod/a/b/c.lua file.
func TestResolveLuaRequires_StringModule(t *testing.T) {
	g := graph.New()
	seedFile(g, "main.lua", "lua")
	seedFile(g, "mod/a/b/c.lua", "lua")
	e := luaRequire(g, "main.lua", "mod.a.b.c", "")

	r := New(g)
	r.resolveLuaRequires()

	assert.Equal(t, "mod/a/b/c.lua", e.To)
	assert.Equal(t, graph.OriginASTResolved, e.Origin)
}

// TestResolveLuaRequires_InitModule pins that a require for a package directory
// binds to that directory's init.lua.
func TestResolveLuaRequires_InitModule(t *testing.T) {
	g := graph.New()
	seedFile(g, "main.lua", "lua")
	seedFile(g, "mod/a/b/init.lua", "lua")
	e := luaRequire(g, "main.lua", "mod.a.b", "")

	r := New(g)
	r.resolveLuaRequires()

	assert.Equal(t, "mod/a/b/init.lua", e.To, "dir module resolves to init.lua")
}

// TestResolveLuaRequires_LuauExtension pins resolution onto a .luau file.
func TestResolveLuaRequires_LuauExtension(t *testing.T) {
	g := graph.New()
	seedFile(g, "main.luau", "luau")
	seedFile(g, "shared/util.luau", "luau")
	e := luaRequire(g, "main.luau", "shared.util", "")

	r := New(g)
	r.resolveLuaRequires()

	assert.Equal(t, "shared/util.luau", e.To)
}

// TestResolveLuaRequires_PackageRootSuffix pins the package-root net: a dotted
// require resolves to a uniquely-matching file under a package root.
func TestResolveLuaRequires_PackageRootSuffix(t *testing.T) {
	g := graph.New()
	seedFile(g, "main.lua", "lua")
	seedFile(g, "src/a/b/c.lua", "lua") // under a package root, not at a/b/c.lua
	e := luaRequire(g, "main.lua", "a.b.c", "")

	r := New(g)
	r.resolveLuaRequires()

	assert.Equal(t, "src/a/b/c.lua", e.To, "dotted require resolves under a package root")
}

// TestResolveLuaRequires_RobloxUnique pins a Roblox instance-path require
// binding by its unique leaf module file.
func TestResolveLuaRequires_RobloxUnique(t *testing.T) {
	g := graph.New()
	seedFile(g, "main.luau", "luau")
	seedFile(g, "src/components/Foo.luau", "luau")
	e := luaRequire(g, "main.luau", "Foo", "script.Parent.Foo")

	r := New(g)
	r.resolveLuaRequires()

	assert.Equal(t, "src/components/Foo.luau", e.To)
}

// TestResolveLuaRequires_RobloxAmbiguousStaysExternal pins that an ambiguous
// Roblox module name does not bind.
func TestResolveLuaRequires_RobloxAmbiguousStaysExternal(t *testing.T) {
	g := graph.New()
	seedFile(g, "main.luau", "luau")
	seedFile(g, "a/Foo.luau", "luau")
	seedFile(g, "b/Foo.luau", "luau")
	e := luaRequire(g, "main.luau", "Foo", "script.Parent.Foo")

	r := New(g)
	r.resolveLuaRequires()

	assert.Equal(t, "unresolved::import::Foo", e.To, "ambiguous Roblox module stays external")
}

// TestResolveLuaRequires_UnindexedStaysExternal pins that a require with no
// indexed target is left external.
func TestResolveLuaRequires_UnindexedStaysExternal(t *testing.T) {
	g := graph.New()
	seedFile(g, "main.lua", "lua")
	e := luaRequire(g, "main.lua", "nonexistent.module", "")

	r := New(g)
	r.resolveLuaRequires()

	assert.Equal(t, "unresolved::import::nonexistent.module", e.To)
}
