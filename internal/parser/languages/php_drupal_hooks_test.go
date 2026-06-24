package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func drupalHookOf(nodes []*graph.Node, fn string) string {
	for _, n := range nodes {
		if n.Name == fn && n.Meta != nil {
			s, _ := n.Meta["drupal_hook"].(string)
			return s
		}
	}
	return ""
}

func drupalHookNode(nodes []*graph.Node, hook string) *graph.Node {
	for _, n := range nodes {
		if n.Kind == graph.KindInterface && n.Name == hook && n.Meta != nil && n.Meta["drupal_hook_def"] != nil {
			return n
		}
	}
	return nil
}

func drupalImplements(edges []*graph.Edge, from, to string) bool {
	for _, e := range edges {
		if e.Kind == graph.EdgeImplements && e.From == from && e.To == to {
			return true
		}
	}
	return false
}

func TestDrupalHooks_DocblockAndNamePattern(t *testing.T) {
	src := `<?php
/**
 * Implements hook_user_login().
 */
function custom_login_handler($account) {}

function mymodule_node_insert($node) {}

function mymodule_helper_util() {}
`
	res, err := NewPHPExtractor().Extract("modules/mymodule/mymodule.module", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	// Docblock-detected hook.
	if h := drupalHookOf(res.Nodes, "custom_login_handler"); h != "hook_user_login" {
		t.Errorf("@Implements docblock hook = %q (want hook_user_login)", h)
	}
	// Name-pattern detected hook.
	if h := drupalHookOf(res.Nodes, "mymodule_node_insert"); h != "hook_node_insert" {
		t.Errorf("name-pattern hook = %q (want hook_node_insert)", h)
	}
	// A plain helper is not a hook.
	if h := drupalHookOf(res.Nodes, "mymodule_helper_util"); h != "" {
		t.Errorf("a non-hook helper must not be flagged, got %q", h)
	}

	// Synthetic hook node + EdgeImplements wiring (for find_implementations).
	hookNode := drupalHookNode(res.Nodes, "hook_node_insert")
	if hookNode == nil {
		t.Fatalf("no synthetic hook_node_insert node")
	}
	if !drupalImplements(res.Edges, "modules/mymodule/mymodule.module::mymodule_node_insert", hookNode.ID) {
		t.Errorf("missing EdgeImplements from mymodule_node_insert to the hook node")
	}
}

func TestDrupalHooks_NonModuleFileNamePatternIgnored(t *testing.T) {
	// In a plain .php file (not a Drupal module file), the name pattern does
	// not apply — only the @Implements docblock would.
	src := `<?php
function mymodule_node_insert($node) {}
`
	res, err := NewPHPExtractor().Extract("src/Service.php", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if h := drupalHookOf(res.Nodes, "mymodule_node_insert"); h != "" {
		t.Errorf("name-pattern must not fire in a non-module .php file, got %q", h)
	}
}
