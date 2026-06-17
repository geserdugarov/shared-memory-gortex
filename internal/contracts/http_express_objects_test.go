package contracts

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func tsFn(file, name string) *graph.Node {
	return &graph.Node{ID: file + "::" + name, Name: name, Kind: graph.KindFunction, FilePath: file, StartLine: 1, EndLine: 1}
}

func TestFastifyRoutes(t *testing.T) {
	src := []byte(`
fastify.get('/health', getHealth)
fastify.route({
  method: 'POST',
  url: '/users',
  handler: createUser
})
`)
	nodes := []*graph.Node{tsFn("app.ts", "getHealth"), tsFn("app.ts", "createUser")}
	cs := (&HTTPExtractor{}).Extract("app.ts", src, nodes, nil)

	health := flaskFind(cs, "GET", "/health")
	if health == nil {
		t.Fatalf("expected GET /health from fastify.get, got %+v", contractPaths(cs))
	}
	if health.Meta["framework"] != "fastify" {
		t.Errorf("framework = %v, want fastify", health.Meta["framework"])
	}
	if health.SymbolID != "app.ts::getHealth" {
		t.Errorf("health handler = %q", health.SymbolID)
	}

	users := flaskFind(cs, "POST", "/users")
	if users == nil {
		t.Fatalf("expected POST /users from fastify.route object, got %+v", contractPaths(cs))
	}
	if users.Meta["framework"] != "fastify" {
		t.Errorf("framework = %v, want fastify", users.Meta["framework"])
	}
	if users.SymbolID != "app.ts::createUser" {
		t.Errorf("users handler = %q", users.SymbolID)
	}
}

func TestHapiRoutes(t *testing.T) {
	src := []byte(`
server.route({ method: 'GET', path: '/items', handler: listItems })
server.route({ method: ['GET', 'POST'], path: '/multi', handler: multiHandler })
`)
	nodes := []*graph.Node{tsFn("app.ts", "listItems"), tsFn("app.ts", "multiHandler")}
	cs := (&HTTPExtractor{}).Extract("app.ts", src, nodes, nil)

	items := flaskFind(cs, "GET", "/items")
	if items == nil {
		t.Fatalf("expected GET /items from server.route, got %+v", contractPaths(cs))
	}
	if items.Meta["framework"] != "hapi" {
		t.Errorf("framework = %v, want hapi", items.Meta["framework"])
	}
	if items.SymbolID != "app.ts::listItems" {
		t.Errorf("items handler = %q", items.SymbolID)
	}

	// method array expands to one contract per verb.
	if flaskFind(cs, "GET", "/multi") == nil || flaskFind(cs, "POST", "/multi") == nil {
		t.Fatalf("expected GET+POST /multi from method array, got %+v", contractPaths(cs))
	}
}

func TestKoaDeleteAlias(t *testing.T) {
	src := []byte(`
router.del('/users/:id', removeUser)
`)
	nodes := []*graph.Node{tsFn("app.ts", "removeUser")}
	cs := (&HTTPExtractor{}).Extract("app.ts", src, nodes, nil)

	del := flaskFind(cs, "DELETE", "/users/{p1}")
	if del == nil {
		t.Fatalf("expected DELETE /users/{p1} from router.del, got %+v", contractPaths(cs))
	}
	if del.Meta["framework"] != "koa" {
		t.Errorf("framework = %v, want koa", del.Meta["framework"])
	}
	if del.SymbolID != "app.ts::removeUser" {
		t.Errorf("del handler = %q", del.SymbolID)
	}
}

func TestExpressObjectChain(t *testing.T) {
	src := []byte(`
app.route('/users').get(listUsers).post(createUser)
`)
	nodes := []*graph.Node{tsFn("app.ts", "listUsers"), tsFn("app.ts", "createUser")}
	cs := (&HTTPExtractor{}).Extract("app.ts", src, nodes, nil)

	get := flaskFind(cs, "GET", "/users")
	post := flaskFind(cs, "POST", "/users")
	if get == nil || post == nil {
		t.Fatalf("expected GET+POST /users from route chain, got %+v", contractPaths(cs))
	}
	if get.Meta["framework"] != "express" {
		t.Errorf("framework = %v, want express", get.Meta["framework"])
	}
	if get.SymbolID != "app.ts::listUsers" || post.SymbolID != "app.ts::createUser" {
		t.Errorf("chain handlers = %q / %q", get.SymbolID, post.SymbolID)
	}
}
