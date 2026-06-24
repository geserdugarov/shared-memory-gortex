package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func vuexTaggedNode(nodes []*graph.Node, namespace, action, kind string) *graph.Node {
	for _, n := range nodes {
		if n.Meta == nil {
			continue
		}
		a, _ := n.Meta["vuex_action"].(string)
		ns, _ := n.Meta["vuex_namespace"].(string)
		k, _ := n.Meta["vuex_kind"].(string)
		if a == action && ns == namespace && k == kind {
			return n
		}
	}
	return nil
}

func vuexPlaceholder(edges []*graph.Edge, key string) *graph.Edge {
	for _, e := range edges {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "vuex-dispatch" {
			continue
		}
		if k, _ := e.Meta["vuex_key"].(string); k == key {
			return e
		}
	}
	return nil
}

func TestVuex_TagsAndDispatchPlaceholders(t *testing.T) {
	src := `import Vuex from 'vuex';
const store = new Vuex.Store({
  actions: { rootAction() {} },
  modules: {
    user: {
      namespaced: true,
      actions: { login(ctx) {} },
      mutations: { SET_TOKEN(state) {} },
    },
  },
});
function caller() {
  this.$store.dispatch('user/login');
  this.$store.commit('user/SET_TOKEN');
}
`
	res, err := NewTypeScriptExtractor().Extract("store.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	if vuexTaggedNode(res.Nodes, "user", "login", "action") == nil {
		t.Errorf("user/login action not tagged")
	}
	if vuexTaggedNode(res.Nodes, "user", "SET_TOKEN", "mutation") == nil {
		t.Errorf("user SET_TOKEN mutation not tagged")
	}
	if vuexTaggedNode(res.Nodes, "", "rootAction", "action") == nil {
		t.Errorf("root action not tagged with empty namespace")
	}

	login := vuexPlaceholder(res.Edges, "user/login")
	if login == nil {
		t.Fatalf("no placeholder for dispatch('user/login')")
	}
	if login.From != "store.ts::caller" {
		t.Errorf("placeholder From = %q (want store.ts::caller)", login.From)
	}
	if k, _ := login.Meta["vuex_kind"].(string); k != "action" {
		t.Errorf("vuex_kind = %q (want action)", k)
	}
	if vuexPlaceholder(res.Edges, "user/SET_TOKEN") == nil {
		t.Errorf("no placeholder for commit('user/SET_TOKEN')")
	}
}

func TestVuex_NonNamespacedModuleIsRoot(t *testing.T) {
	// A module without `namespaced: true` contributes its actions to the
	// root namespace.
	src := `const store = new Vuex.Store({
  modules: {
    cart: {
      actions: { add() {} },
    },
  },
});
`
	res, err := NewJavaScriptExtractor().Extract("s.js", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if vuexTaggedNode(res.Nodes, "", "add", "action") == nil {
		t.Errorf("non-namespaced module action should be in the root namespace")
	}
	if vuexTaggedNode(res.Nodes, "cart", "add", "action") != nil {
		t.Errorf("non-namespaced module action must not be namespaced")
	}
}

func TestVuex_NonVuexCreateStoreIgnored(t *testing.T) {
	// A Redux-style createStore(reducer) has no mutations/modules key and
	// must not be treated as a Vuex store.
	src := `const store = createStore(rootReducer, preloadedState);
`
	res, err := NewJavaScriptExtractor().Extract("redux.js", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range res.Nodes {
		if n.Meta != nil && n.Meta["vuex_action"] != nil {
			t.Errorf("Redux createStore must not produce Vuex tags")
		}
	}
}
