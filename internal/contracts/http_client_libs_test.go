package contracts

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// extractClientConsumer is a small helper: extract contracts from a non-Go
// source file and return the consumer contracts only.
func clientConsumers(cs []Contract) []Contract {
	var out []Contract
	for _, c := range cs {
		if c.Type == ContractHTTP && c.Role == RoleConsumer {
			out = append(out, c)
		}
	}
	return out
}

// TestClientLib_Python_Requests_CrossRepo_PairsFlaskProvider is the headline
// acceptance: a Python `requests.get("/api/users")` consumer in one repo pairs
// with a Flask provider for the same route in ANOTHER repo behind a shared
// workspace — a cross-repo link with CrossRepo:true.
func TestClientLib_Python_Requests_CrossRepo_PairsFlaskProvider(t *testing.T) {
	// Provider repo: a Flask route.
	provSrc := []byte(`from flask import Flask

app = Flask(__name__)

@app.get('/api/users')
def list_users():
    return []
`)
	provNodes := makeNodes("app.py", []struct {
		name       string
		start, end int
	}{{"list_users", 6, 7}})
	provCs := (&HTTPExtractor{}).Extract("app.py", provSrc, provNodes, nil)
	prov := findContract(t, provCs, "http::GET::/api/users", RoleProvider)

	// Consumer repo: a `requests` client call to the same route.
	consSrc := []byte(`import requests

def fetch_users():
    return requests.get("/api/users")
`)
	consNodes := makeNodes("client.py", []struct {
		name       string
		start, end int
	}{{"fetch_users", 3, 4}})
	consCs := (&HTTPExtractor{}).Extract("client.py", consSrc, consNodes, nil)
	cons := findContract(t, consCs, "http::GET::/api/users", RoleConsumer)
	assertMetaString(t, cons, "method", "GET")
	assertMetaString(t, cons, "path", "/api/users")

	// Stitch them into one registry across two repos in a shared workspace.
	reg := NewRegistry()
	prov.RepoPrefix = "svc-api"
	prov.WorkspaceID = "acme"
	prov.ProjectID = "users"
	cons.RepoPrefix = "webapp"
	cons.WorkspaceID = "acme"
	cons.ProjectID = "users"
	reg.Add(prov)
	reg.Add(cons)

	result := Match(reg)
	if len(result.Matched) != 1 {
		t.Fatalf("expected 1 cross-repo match, got %d (orphanP=%d orphanC=%d)",
			len(result.Matched), len(result.OrphanProviders), len(result.OrphanConsumers))
	}
	m := result.Matched[0]
	if !m.CrossRepo {
		t.Error("expected CrossRepo:true (provider svc-api, consumer webapp)")
	}
	if m.Provider.RepoPrefix != "svc-api" || m.Consumer.RepoPrefix != "webapp" {
		t.Errorf("wrong sides: provider=%s consumer=%s", m.Provider.RepoPrefix, m.Consumer.RepoPrefix)
	}
	if m.ContractID != "http::GET::/api/users" {
		t.Errorf("wrong contract ID: %s", m.ContractID)
	}
}

// TestClientLib_Precision_NoImport_NoConsumer proves the import-resolution gate:
// a local variable named `requests` in a file that never imports the library
// must NOT mint a consumer contract.
func TestClientLib_Precision_NoImport_NoConsumer(t *testing.T) {
	src := []byte(`def fetch():
    requests = make_client()
    return requests.get("/api/users")
`)
	nodes := makeNodes("client.py", []struct {
		name       string
		start, end int
	}{{"fetch", 1, 3}})
	cs := (&HTTPExtractor{}).Extract("client.py", src, nodes, nil)
	if got := clientConsumers(cs); len(got) != 0 {
		t.Fatalf("local `requests` var without `import requests` must not be a consumer, got %d: %v", len(got), routeSummaries(cs))
	}
}

// TestClientLib_Precision_Rust_SurfNotRegistered proves an unregistered crate
// (surf) is ignored even when its call looks like a registered one, and a bare
// `client.get` with no HTTP-client import is also ignored.
func TestClientLib_Precision_Rust_SurfNotRegistered(t *testing.T) {
	src := []byte(`use surf;

async fn fetch(client: &Surf) {
    surf::get("/api/users").await;
    client.get("/api/orders").await;
}
`)
	nodes := makeNodes("client.rs", []struct {
		name       string
		start, end int
	}{{"fetch", 3, 6}})
	cs := (&HTTPExtractor{}).Extract("client.rs", src, nodes, nil)
	if got := clientConsumers(cs); len(got) != 0 {
		t.Fatalf("surf (unregistered) must not mint consumers, got %d: %v", len(got), routeSummaries(cs))
	}
}

// TestClientLib_Python_ConstURL_ResolvesViaStore proves the URL argument is run
// through ResolveEndpointArg: a const-referenced path resolves graph-wide.
func TestClientLib_Python_ConstURL_ResolvesViaStore(t *testing.T) {
	src := []byte(`import requests

URL = "/api/users"

def fetch_users():
    return requests.get(URL)
`)
	nodes := makeNodes("client.py", []struct {
		name       string
		start, end int
	}{{"fetch_users", 5, 6}})

	store := constStore([3]string{"repo/client.py::URL", "URL", "repo/client.py"})
	store.vals["repo/client.py::URL"] = "/api/users"

	cs := (&HTTPExtractor{}).ExtractWithStore("repo/client.py", src, nodes, nil, nil, store, "repo")
	c := findContract(t, cs, "http::GET::/api/users", RoleConsumer)
	assertMetaString(t, c, "path", "/api/users")
}

// TestClientLib_Ruby_Faraday_ImportGated covers a Ruby Faraday consumer (gated
// on `require 'faraday'`) and the negative — a same-shaped call with no require.
func TestClientLib_Ruby_Faraday_ImportGated(t *testing.T) {
	src := []byte(`require 'faraday'

conn = Faraday.new
conn.get('/api/users')
Faraday.get('/api/orders')
`)
	nodes := []*graph.Node{}
	cs := (&HTTPExtractor{}).Extract("client.rb", src, nodes, nil)
	if !hasConsumer(cs, "GET", "/api/users") {
		t.Errorf("expected Faraday conn.get consumer, got %v", routeSummaries(cs))
	}
	if !hasConsumer(cs, "GET", "/api/orders") {
		t.Errorf("expected Faraday.get consumer, got %v", routeSummaries(cs))
	}

	// Negative: no require → no consumer.
	noReq := []byte(`conn = something
conn.get('/api/users')
`)
	if got := clientConsumers((&HTTPExtractor{}).Extract("client.rb", noReq, nil, nil)); len(got) != 0 {
		t.Errorf("ruby without require must not mint a consumer, got %d", len(got))
	}
}

// TestClientLib_PHP_Guzzle_ImportGated covers a PHP Guzzle consumer (gated on
// `use GuzzleHttp\Client` plus a `new Client()` construction).
func TestClientLib_PHP_Guzzle_ImportGated(t *testing.T) {
	src := []byte(`<?php
use GuzzleHttp\Client;

$client = new Client();
$client->get('/api/users');
$client->request('POST', '/api/orders');
`)
	cs := (&HTTPExtractor{}).Extract("client.php", src, nil, nil)
	if !hasConsumer(cs, "GET", "/api/users") {
		t.Errorf("expected Guzzle ->get consumer, got %v", routeSummaries(cs))
	}
	if !hasConsumer(cs, "POST", "/api/orders") {
		t.Errorf("expected Guzzle ->request('POST',…) consumer, got %v", routeSummaries(cs))
	}

	// Negative: no `use GuzzleHttp` import → no consumer.
	noUse := []byte(`<?php
$client = new Client();
$client->get('/api/users');
`)
	if got := clientConsumers((&HTTPExtractor{}).Extract("client.php", noUse, nil, nil)); len(got) != 0 {
		t.Errorf("php without GuzzleHttp use must not mint a consumer, got %d", len(got))
	}
}

// TestClientLib_Java_RestTemplate_ImportGated covers a Java Spring RestTemplate
// consumer (gated on the RestTemplate import + a typed local variable).
func TestClientLib_Java_RestTemplate_ImportGated(t *testing.T) {
	src := []byte(`import org.springframework.web.client.RestTemplate;

class UserClient {
    String fetch() {
        RestTemplate rt = new RestTemplate();
        return rt.getForObject("/api/users", String.class);
    }
}
`)
	cs := (&HTTPExtractor{}).Extract("UserClient.java", src, nil, nil)
	if !hasConsumer(cs, "GET", "/api/users") {
		t.Errorf("expected RestTemplate getForObject consumer, got %v", routeSummaries(cs))
	}

	// Negative: no RestTemplate import → no consumer.
	noImport := []byte(`class UserClient {
    String fetch(Helper rt) {
        return rt.getForObject("/api/users", String.class);
    }
}
`)
	if got := clientConsumers((&HTTPExtractor{}).Extract("UserClient.java", noImport, nil, nil)); len(got) != 0 {
		t.Errorf("java without RestTemplate import must not mint a consumer, got %d", len(got))
	}
}

// TestClientLib_Kotlin_RestTemplate_ImportGated covers a Kotlin Spring
// RestTemplate consumer (gated on the import + a constructed local val).
func TestClientLib_Kotlin_RestTemplate_ImportGated(t *testing.T) {
	src := []byte(`import org.springframework.web.client.RestTemplate

fun fetch(): String {
    val rt = RestTemplate()
    return rt.getForObject("/api/users", String::class.java)
}
`)
	cs := (&HTTPExtractor{}).Extract("Client.kt", src, nil, nil)
	if !hasConsumer(cs, "GET", "/api/users") {
		t.Errorf("expected Kotlin RestTemplate getForObject consumer, got %v", routeSummaries(cs))
	}

	noImport := []byte(`fun fetch(rt: Helper): String {
    return rt.getForObject("/api/users", String::class.java)
}
`)
	if got := clientConsumers((&HTTPExtractor{}).Extract("Client.kt", noImport, nil, nil)); len(got) != 0 {
		t.Errorf("kotlin without RestTemplate import must not mint a consumer, got %d", len(got))
	}
}

// TestClientLib_Scala_Sttp_ImportGated covers an sttp consumer (gated on the
// `import sttp.client3._` import; the URL is an sttp `uri"…"` interpolation).
func TestClientLib_Scala_Sttp_ImportGated(t *testing.T) {
	src := []byte(`import sttp.client3._

val request = basicRequest.get(uri"/api/users")
`)
	cs := (&HTTPExtractor{}).Extract("Client.scala", src, nil, nil)
	if !hasConsumer(cs, "GET", "/api/users") {
		t.Errorf("expected sttp basicRequest.get consumer, got %v", routeSummaries(cs))
	}

	noImport := []byte(`val request = basicRequest.get(uri"/api/users")
`)
	if got := clientConsumers((&HTTPExtractor{}).Extract("Client.scala", noImport, nil, nil)); len(got) != 0 {
		t.Errorf("scala without sttp import must not mint a consumer, got %d", len(got))
	}
}

// hasConsumer reports whether cs contains a consumer contract with the given
// method and path.
func hasConsumer(cs []Contract, method, path string) bool {
	for _, c := range cs {
		if c.Type == ContractHTTP && c.Role == RoleConsumer &&
			c.Meta["method"] == method && c.Meta["path"] == path {
			return true
		}
	}
	return false
}
