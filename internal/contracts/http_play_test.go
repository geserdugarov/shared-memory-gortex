package contracts

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func playContracts(cs []Contract) map[string]Contract {
	out := map[string]Contract{}
	for _, c := range cs {
		if fw, _ := c.Meta["framework"].(string); fw == "play" {
			out[c.ID] = c
		}
	}
	return out
}

func TestPlayRoutes_VerbLinesAndHandlerStamps(t *testing.T) {
	src := []byte(`# Application routes
# ~~~~

GET     /                     controllers.HomeController.index()
GET     /users                controllers.UserController.list()
POST    /users                controllers.UserController.create()
GET     /users/:id            controllers.UserController.show(id: Long)
->      /api                  api.Routes
+nocsrf
GET     /assets/*file         controllers.Assets.at(path="/public", file)
`)
	ext := &HTTPExtractor{}
	cs := ext.Extract("conf/routes", src, nil, nil)
	by := playContracts(cs)

	list, ok := by["http::GET::/users"]
	if !ok {
		t.Fatalf("no GET /users contract; got %d play routes", len(by))
	}
	if hc, _ := list.Meta["handler_class"].(string); hc != "UserController" {
		t.Errorf("handler_class = %q (want UserController)", hc)
	}
	if hi, _ := list.Meta["handler_ident"].(string); hi != "list" {
		t.Errorf("handler_ident = %q (want list)", hi)
	}
	if fq, _ := list.Meta["handler_fqcn"].(string); fq != "controllers.UserController" {
		t.Errorf("handler_fqcn = %q (want controllers.UserController)", fq)
	}
	if _, ok := by["http::POST::/users"]; !ok {
		t.Errorf("no POST /users contract")
	}
	if _, ok := by["http::GET::/"]; !ok {
		t.Errorf("no GET / contract")
	}
	// The args-with-space handler (`at(path="/public", file)`) still resolves
	// its method despite the embedded space.
	var sawAssets bool
	for _, c := range by {
		if hi, _ := c.Meta["handler_ident"].(string); hi == "at" {
			sawAssets = true
			if hc, _ := c.Meta["handler_class"].(string); hc != "Assets" {
				t.Errorf("assets handler_class = %q (want Assets)", hc)
			}
		}
	}
	if !sawAssets {
		t.Errorf("the /assets handler line should produce a route")
	}
	// A `:id` path param is captured as a route, with a show handler.
	var sawShow bool
	for _, c := range by {
		if hi, _ := c.Meta["handler_ident"].(string); hi == "show" {
			sawShow = true
		}
	}
	if !sawShow {
		t.Errorf("the /users/:id show route should be extracted")
	}
}

func TestPlayRoutes_CommentsAndIncludesSkipped(t *testing.T) {
	src := []byte(`# A comment
->      /admin                admin.Routes
+nocsrf
GET     /ping                 controllers.HealthController.ping()
`)
	ext := &HTTPExtractor{}
	by := playContracts(ext.Extract("conf/routes", src, nil, nil))

	if len(by) != 1 {
		t.Fatalf("expected exactly 1 route (comments/includes/modifiers skipped), got %d", len(by))
	}
	if _, ok := by["http::GET::/ping"]; !ok {
		t.Errorf("expected the GET /ping route")
	}
}

func TestPlayRoutes_SameFileControllerBinding(t *testing.T) {
	// When the controller method is present in the same node set (receiver-
	// aware), the route's SymbolID binds to it directly.
	src := []byte(`GET  /x  controllers.MyController.handle()
`)
	nodes := []*graph.Node{{
		ID: "conf/routes::MyController.handle", Kind: graph.KindMethod, Name: "handle",
		FilePath: "conf/routes", StartLine: 1, EndLine: 1,
		Meta: map[string]any{"receiver": "MyController"},
	}}
	ext := &HTTPExtractor{}
	cs := ext.Extract("conf/routes", src, nodes, nil)
	for _, c := range cs {
		if c.ID == "http::GET::/x" {
			if c.SymbolID != "conf/routes::MyController.handle" {
				t.Errorf("same-file controller SymbolID = %q (want the handle method)", c.SymbolID)
			}
		}
	}
}
