package mcp

import (
	"strings"
	"testing"
)

func TestHandleReadFile_Redact(t *testing.T) {
	s := &Server{} // nil configManager → redaction enabled by default

	const secret = "token: ghp_0123456789abcdefghijklmnopqrstuvwxyz\n"

	// A config-leaf yaml: the secret value is withheld, the key survives.
	out, did := s.maybeRedactConfigLeaf("yaml", "config/app.yaml", false, secret)
	if !did {
		t.Fatalf("expected redaction of a config-leaf secret, got none: %q", out)
	}
	if strings.Contains(out, "ghp_0123456789abcdefghijklmnopqrstuvwxyz") {
		t.Errorf("secret value survived redaction: %q", out)
	}
	if !strings.Contains(out, "token:") {
		t.Errorf("benign key framing was dropped: %q", out)
	}

	// allow_secrets bypasses redaction entirely.
	if raw, did := s.maybeRedactConfigLeaf("yaml", "config/app.yaml", true, secret); did || raw != secret {
		t.Errorf("allow_secrets should serve verbatim: did=%v out=%q", did, raw)
	}

	// A non-config-leaf file (source code) is never redacted.
	const code = "apiKey := \"ghp_0123456789abcdefghijklmnopqrstuvwxyz\"\n"
	if got, did := s.maybeRedactConfigLeaf("go", "main.go", false, code); did || got != code {
		t.Errorf("source code should not be redacted: did=%v", did)
	}
}
