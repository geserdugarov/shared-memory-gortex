package contracts

import "testing"

// consumerPaths returns the set of HTTP consumer route paths the extractor
// minted for src, keyed by path.
func consumerPaths(t *testing.T, file string, src string) map[string]bool {
	t.Helper()
	nodes := makeNodes(file, []struct {
		name       string
		start, end int
	}{
		{"caller", 1, 20},
	})
	ext := &HTTPExtractor{}
	out := ext.Extract(file, []byte(src), nodes, nil)
	got := map[string]bool{}
	for _, c := range out {
		if c.Type == ContractHTTP && c.Role == RoleConsumer {
			if p, _ := c.Meta["path"].(string); p != "" {
				got[p] = true
			}
		}
	}
	return got
}

// TestConsumerGuard_StaticAssetRejected pins the consumer-pass acceptance: a
// static-asset fetch is not an API consumer, while a real API fetch still is.
func TestConsumerGuard_StaticAssetRejected(t *testing.T) {
	got := consumerPaths(t, "app.js", `
async function load() {
  await fetch('/static/app.js');
  await fetch('/api/x');
}
`)
	if got["/static/app.js"] {
		t.Errorf("static asset /static/app.js minted a consumer contract")
	}
	if !got["/api/x"] {
		t.Errorf("expected /api/x to still mint a consumer contract; got %v", got)
	}
}

// TestConsumerGuard_FilesystemConfigRejected covers the filesystem/config
// false-positive: a rooted config-file literal is not an HTTP consumer.
func TestConsumerGuard_FilesystemConfigRejected(t *testing.T) {
	got := consumerPaths(t, "app.js", `
async function load() {
  await fetch('/etc/app.conf');
  await fetch('/var/log/app.log');
  await fetch('/api/users');
}
`)
	if got["/etc/app.conf"] {
		t.Errorf("config path /etc/app.conf minted a consumer contract")
	}
	if got["/var/log/app.log"] {
		t.Errorf("log path /var/log/app.log minted a consumer contract")
	}
	if !got["/api/users"] {
		t.Errorf("expected /api/users to still mint a consumer contract; got %v", got)
	}
}

// TestConsumerGuard_RelativeAndURLPreserved guards against over-filtering:
// non-rooted (relative/template) and absolute-URL consumers keep their
// existing behaviour and are not dropped by the rooted-path gate.
func TestConsumerGuard_AbsoluteURLPreserved(t *testing.T) {
	got := consumerPaths(t, "client.py", `
import requests
def call():
    requests.get("http://service/api/users")
`)
	// The literal does not start with "/" (it starts with "http://"), so the
	// rooted-path gate never touches it — the consumer must survive.
	if len(got) == 0 {
		t.Errorf("absolute-URL consumer should still mint a contract; got none")
	}
}
