package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zzet/gortex/internal/graph"
)

func convNode(g *graph.Graph, id, file, name string) {
	g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: name, FilePath: file, Language: "javascript"})
}

func TestResolveByConvention_ExactDirTier(t *testing.T) {
	// A candidate under the preferred directory wins at 0.9.
	g := graph.New()
	convNode(g, "src/middleware/auth.js::auth", "src/middleware/auth.js", "auth")
	convNode(g, "src/util/auth.js::auth", "src/util/auth.js", "auth")

	id, conf := ResolveByConvention(g, "authMiddleware", "Middleware", []string{"/middleware/"}, "src/routes/users.js")
	assert.Equal(t, "src/middleware/auth.js::auth", id, "suffix-stripped name resolves to the /middleware/ definition")
	assert.Equal(t, 0.9, conf)
}

func TestResolveByConvention_SameDirTier(t *testing.T) {
	// No preferred-dir match; a candidate in the caller's own directory wins
	// at 0.85.
	g := graph.New()
	convNode(g, "src/routes/helpers.js::format", "src/routes/helpers.js", "format")
	convNode(g, "src/lib/format.js::format", "src/lib/format.js", "format")

	id, conf := ResolveByConvention(g, "format", "", []string{"/middleware/"}, "src/routes/users.js")
	assert.Equal(t, "src/routes/helpers.js::format", id)
	assert.Equal(t, 0.85, conf)
}

func TestResolveByConvention_SoleCandidateTier(t *testing.T) {
	// One candidate anywhere → 0.7.
	g := graph.New()
	convNode(g, "src/services/UserService.js::list", "src/services/UserService.js", "list")

	id, conf := ResolveByConvention(g, "list", "", []string{"/controllers/"}, "src/routes/users.js")
	assert.Equal(t, "src/services/UserService.js::list", id)
	assert.Equal(t, 0.7, conf)
}

func TestResolveByConvention_AmbiguousReturnsEmpty(t *testing.T) {
	// Two candidates, neither in a preferred dir nor the caller's dir → empty.
	g := graph.New()
	convNode(g, "a/auth.js::auth", "a/auth.js", "auth")
	convNode(g, "b/auth.js::auth", "b/auth.js", "auth")

	id, conf := ResolveByConvention(g, "auth", "", []string{"/middleware/"}, "c/routes.js")
	assert.Equal(t, "", id, "ambiguous candidates resolve to nothing")
	assert.Equal(t, 0.0, conf)
}

func TestResolveByConvention_PreferredDirTiebreakBySameDir(t *testing.T) {
	// Two candidates both under /middleware/ → broken by caller's own dir.
	g := graph.New()
	convNode(g, "a/middleware/auth.js::auth", "a/middleware/auth.js", "auth")
	convNode(g, "b/middleware/auth.js::auth", "b/middleware/auth.js", "auth")

	id, conf := ResolveByConvention(g, "auth", "", []string{"/middleware/"}, "a/middleware/index.js")
	assert.Equal(t, "a/middleware/auth.js::auth", id)
	assert.Equal(t, 0.9, conf)
	// Caller in neither dir → ambiguous.
	id2, _ := ResolveByConvention(g, "auth", "", []string{"/middleware/"}, "z/routes.js")
	assert.Equal(t, "", id2)
}

func TestResolveByConvention_NoCandidate(t *testing.T) {
	g := graph.New()
	id, conf := ResolveByConvention(g, "missing", "", nil, "x.js")
	assert.Equal(t, "", id)
	assert.Equal(t, 0.0, conf)
}
