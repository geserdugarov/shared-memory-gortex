package contracts

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestDrupalRoutes_ControllerAndForm(t *testing.T) {
	src := []byte(`node.view:
  path: '/node/{node}'
  defaults:
    _controller: '\Drupal\node\Controller\NodeController::view'
    _title: 'View'
node.add:
  path: '/node/add'
  defaults:
    _form: '\Drupal\node\Form\NodeAddForm'
  methods: [GET, POST]
`)
	ext := &HTTPExtractor{}
	cs := ext.Extract("modules/node/node.routing.yml", src, nil, nil)

	var view, addGet, addPost *Contract
	for i := range cs {
		fw, _ := cs[i].Meta["framework"].(string)
		if fw != "drupal" {
			continue
		}
		switch cs[i].ID {
		case "http::ANY::/node/{p1}":
			view = &cs[i]
		case "http::GET::/node/add":
			addGet = &cs[i]
		case "http::POST::/node/add":
			addPost = &cs[i]
		}
	}
	if view == nil {
		t.Fatalf("no _controller route; got %+v", cs)
	}
	if hc, _ := view.Meta["handler_class"].(string); hc != "NodeController" {
		t.Errorf("controller handler_class = %q (want NodeController)", hc)
	}
	if hi, _ := view.Meta["handler_ident"].(string); hi != "view" {
		t.Errorf("controller handler_ident = %q (want view)", hi)
	}
	if addGet == nil || addPost == nil {
		t.Errorf("_form route with methods [GET, POST] should yield both verbs")
	}
	if addGet != nil {
		if hc, _ := addGet.Meta["handler_class"].(string); hc != "NodeAddForm" {
			t.Errorf("_form handler_class = %q (want NodeAddForm)", hc)
		}
	}
}

func TestDrupalRoutes_SameFileControllerBinding(t *testing.T) {
	// The controller method declared in the same routing file resolves to its
	// SymbolID (receiver-aware), not just a cross-file stamp.
	src := []byte(`my.route:
  path: '/x'
  defaults:
    _controller: '\Drupal\my\Controller\MyController::handle'
`)
	nodes := []*graph.Node{{
		ID: "m.routing.yml::MyController.handle", Kind: graph.KindMethod, Name: "handle",
		FilePath: "m.routing.yml", StartLine: 4, EndLine: 4,
		Meta: map[string]any{"receiver": "MyController"},
	}}
	ext := &HTTPExtractor{}
	cs := ext.Extract("m.routing.yml", src, nodes, nil)
	for _, c := range cs {
		if c.ID == "http::ANY::/x" {
			if c.SymbolID != "m.routing.yml::MyController.handle" {
				t.Errorf("same-file controller SymbolID = %q (want the handle method)", c.SymbolID)
			}
		}
	}
}
