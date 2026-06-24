package contracts

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestVapor_GroupedRoutePrefixJoin(t *testing.T) {
	src := []byte(`import Vapor

func routes(_ app: Application) throws {
    let api = app.grouped("api")
    api.get("users", use: list)
    let v1 = api.grouped("v1")
    v1.post("posts", use: create)
}

func list(req: Request) throws -> String { "ok" }
func create(req: Request) throws -> String { "ok" }
`)
	nodes := []*graph.Node{
		{ID: "Sources/App/routes.swift::list", Name: "list", Kind: graph.KindFunction, FilePath: "Sources/App/routes.swift", StartLine: 10, EndLine: 10},
		{ID: "Sources/App/routes.swift::create", Name: "create", Kind: graph.KindFunction, FilePath: "Sources/App/routes.swift", StartLine: 11, EndLine: 11},
	}
	reg := NewRegistry()
	ext := &HTTPExtractor{}
	cs := ext.Extract("Sources/App/routes.swift", src, nodes, nil)
	reg.AddAll(cs, "")

	srcFor := func(string) []byte { return src }
	JoinRouterPrefixes(reg, []string{"Sources/App/routes.swift"}, srcFor)

	ids := map[string]bool{}
	for _, c := range reg.All() {
		ids[c.ID] = true
	}
	if !ids["http::GET::/api/users"] {
		got := make([]string, 0, len(ids))
		for id := range ids {
			got = append(got, id)
		}
		t.Errorf("expected grouped route http::GET::/api/users; got %v", got)
	}
	if !ids["http::POST::/api/v1/posts"] {
		t.Errorf("expected nested grouped route http::POST::/api/v1/posts")
	}
}
