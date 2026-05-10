package astquery

import (
	"strings"

	"github.com/zzet/gortex/internal/parser"
)

// Bundled detectors. Each rule:
//   - Has a stable kebab-case Name (the agent-visible handle).
//   - Sets `match` as the row's anchor capture so engine.pickAnchor
//     lands on the meaningful span rather than the whole subtree.
//   - Defaults to ExcludeTests=true so test fixtures don't drown
//     real findings; the few rules that should also flag tests
//     opt out.
//
// Pattern style: every pattern is wrapped in `((…) @match (#…?))`
// when predicates apply to the rule as a whole. Capture names are
// short, lowercase identifiers documented at the rule.
//
// Adding a detector: write the pattern, register it from init(),
// add a golden test in detectors_test.go. Keep the count tight —
// ten high-signal rules age better than fifty noisy ones.

func init() {
	RegisterDetector(detectorErrorNotWrapped())
	RegisterDetector(detectorSQLStringConcat())
	RegisterDetector(detectorWeakCrypto())
	RegisterDetector(detectorPanicInLibrary())
	RegisterDetector(detectorGoroutineWithoutRecover())
	RegisterDetector(detectorHTTPClientNoTimeout())
	RegisterDetector(detectorHardcodedSecret())
	RegisterDetector(detectorEmptyCatch())
	RegisterDetector(detectorJavaStringEquality())
	RegisterDetector(detectorPythonMutableDefault())
}

// 1. error-not-wrapped (Go) -------------------------------------------------
//
// Matches `if err != nil { return err }` (or any single-arg
// pass-through return) without a `fmt.Errorf(..., %w, err)` wrap.
// Captures @errvar from the condition and @retvar from the return,
// then asserts they're identical so we don't false-positive on
// unrelated err handling.
func detectorErrorNotWrapped() *Detector {
	return &Detector{
		Name:        "error-not-wrapped",
		Description: "Returning a Go error verbatim from `if err != nil` instead of wrapping with `fmt.Errorf(\"…: %w\", err)` — strips the call-site context that makes errors debuggable.",
		Severity:    "warning",
		Languages: map[string]string{
			"go": `
((if_statement
   condition: (binary_expression
     left: (identifier) @errvar
     operator: "!="
     right: (nil))
   consequence: (block
     (statement_list
       (return_statement
         (expression_list
           (identifier) @retvar))))) @match
 (#eq? @errvar @retvar))
`,
		},
	}
}

// 2. sql-string-concat (Go / Python / JS / TS / Ruby) -----------------------
//
// Flags a SQL-shaped call site that builds the query via string
// concatenation. The detector is conservative — it only fires on
// well-known method names (`Query`, `Exec`, `execute`, `query`,
// `find_by_sql`) so a generic `+` over strings doesn't spam the
// audit. Cross-language by definition.
func detectorSQLStringConcat() *Detector {
	return &Detector{
		Name:        "sql-string-concat",
		Description: "SQL-shaped database call whose query argument is built with string concatenation — strong indicator of SQL injection in any language.",
		Severity:    "error",
		Languages: map[string]string{
			"go": `
((call_expression
   function: (selector_expression
     field: (field_identifier) @fn)
   arguments: (argument_list
     (binary_expression operator: "+") @concat)) @match
 (#match? @fn "^(Query|QueryRow|Exec|QueryContext|ExecContext|QueryRowContext|Prepare|PrepareContext|Raw)$"))
`,
			"python": `
((call
   function: (attribute
     attribute: (identifier) @fn)
   arguments: (argument_list
     (binary_operator operator: "+") @concat)) @match
 (#match? @fn "^(execute|executemany|raw|fetch|fetchall|fetchone)$"))
`,
			"javascript": `
((call_expression
   function: (member_expression
     property: (property_identifier) @fn)
   arguments: (arguments
     (binary_expression operator: "+") @concat)) @match
 (#match? @fn "^(query|execute|exec|run|raw)$"))
`,
			"typescript": `
((call_expression
   function: (member_expression
     property: (property_identifier) @fn)
   arguments: (arguments
     (binary_expression operator: "+") @concat)) @match
 (#match? @fn "^(query|execute|exec|run|raw)$"))
`,
			"ruby": `
((call
   method: (identifier) @fn
   arguments: (argument_list
     (binary operator: "+") @concat)) @match
 (#match? @fn "^(execute|exec_query|find_by_sql|where|select_all)$"))
`,
		},
	}
}

// 3. weak-crypto (Go / Python) ---------------------------------------------
//
// Flags hashing or symmetric-cipher constructors known to be
// cryptographically weak: MD5, SHA-1, DES, RC4. Both for password
// hashing and for HMAC keys these are deprecated; the only
// legitimate use is checksumming non-security-relevant data.
func detectorWeakCrypto() *Detector {
	return &Detector{
		Name:        "weak-crypto",
		Description: "Use of MD5 / SHA-1 / DES / RC4 — cryptographically broken for any security-sensitive purpose. Use SHA-256+, AES-GCM, or ChaCha20-Poly1305 instead.",
		Severity:    "error",
		Languages: map[string]string{
			"go": `
((call_expression
   function: (selector_expression
     operand: (identifier) @pkg
     field: (field_identifier) @fn)) @match
 (#match? @pkg "^(md5|sha1|des|rc4)$")
 (#match? @fn "^(New|Sum|Sum256|NewCipher|NewTripleDESCipher)$"))
`,
			"python": `
((call
   function: (attribute
     object: (identifier) @lib
     attribute: (identifier) @fn)) @match
 (#eq? @lib "hashlib")
 (#match? @fn "^(md5|sha1|new)$"))
`,
		},
	}
}

// 4. panic-in-library (Go) -------------------------------------------------
//
// A direct `panic(...)` call. Excludes `_test.go` automatically; in
// tests panic is the right primitive. In library / production code
// panic should be reserved for "unreachable" invariants — return an
// error instead.
func detectorPanicInLibrary() *Detector {
	return &Detector{
		Name:        "panic-in-library",
		Description: "`panic(...)` call in non-test Go source. Library code should propagate errors; reserve panic for genuinely unreachable invariants.",
		Severity:    "warning",
		Languages: map[string]string{
			"go": `
((call_expression
   function: (identifier) @fn) @match
 (#eq? @fn "panic"))
`,
		},
		ExcludeTests: true,
	}
}

// 5. goroutine-without-recover (Go) ----------------------------------------
//
// A `go func() { … }()` whose body never calls `recover()`. A panic
// inside the goroutine's body crashes the process; the canonical
// fix is `defer func() { _ = recover() }()` at the top of the
// goroutine. Pure-AST predicates can't express "absence" of a node,
// so the post-filter reads the body text and looks for a recover
// call.
func detectorGoroutineWithoutRecover() *Detector {
	return &Detector{
		Name:        "goroutine-without-recover",
		Description: "`go func() { … }()` whose body never calls `recover()` — a panic anywhere in that goroutine crashes the whole process.",
		Severity:    "warning",
		Languages: map[string]string{
			"go": `
(go_statement
  (call_expression
    function: (func_literal
      body: (block) @body))) @match
`,
		},
		PostFilter: func(qr parser.QueryResult, _ []byte) bool {
			body, ok := qr.Captures["body"]
			if !ok {
				return false
			}
			// Conservative containment check — false negatives
			// (recover() inside a string literal would suppress
			// the warning) are acceptable here; false positives
			// would erode trust.
			return !strings.Contains(body.Text, "recover()")
		},
	}
}

// 6. http-client-no-timeout (Go) -------------------------------------------
//
// `&http.Client{}` or `http.Client{}` literal that doesn't set
// `Timeout`. The default zero-value timeout means an upstream that
// never responds will wedge the goroutine forever — a classic
// production-grade outage trigger.
func detectorHTTPClientNoTimeout() *Detector {
	return &Detector{
		Name:        "http-client-no-timeout",
		Description: "`http.Client{}` literal without a `Timeout` field — defaults to no timeout, which lets a slow upstream wedge the goroutine indefinitely.",
		Severity:    "warning",
		Languages: map[string]string{
			"go": `
((composite_literal
   type: (qualified_type
     package: (package_identifier) @pkg
     name: (type_identifier) @typ)
   body: (literal_value) @body) @match
 (#eq? @pkg "http")
 (#eq? @typ "Client"))
`,
		},
		PostFilter: func(qr parser.QueryResult, _ []byte) bool {
			body, ok := qr.Captures["body"]
			if !ok {
				return false
			}
			return !strings.Contains(body.Text, "Timeout")
		},
	}
}

// 7. hardcoded-secret (Go / Python / JS / TS / Ruby) ------------------------
//
// Any assignment whose left-hand identifier name looks like a
// credential (password / secret / api_key / apiKey / token) and
// whose right-hand side is a string literal of meaningful length.
// The post-filter rejects placeholder strings (length < 12, or
// purely punctuation) so the detector doesn't spam every
// `password = ""` empty-default.
func detectorHardcodedSecret() *Detector {
	// (?i) makes the regex case-insensitive so apiKey, ApiKey,
	// APIKey, and api_key all match.
	const cred = "(?i)^(password|passwd|secret|api_?key|token|aws_?secret(_?key)?|access_?key|private_?key)$"
	return &Detector{
		Name:        "hardcoded-secret",
		Description: "Variable named like a credential (`password`, `secret`, `api_key`, `token`, …) assigned a non-trivial string literal. Move to env vars or a secret manager.",
		Severity:    "error",
		Languages: map[string]string{
			"go": `
((short_var_declaration
   left: (expression_list (identifier) @name)
   right: (expression_list (interpreted_string_literal) @val)) @match
 (#match? @name "` + cred + `"))
`,
			"python": `
((assignment
   left: (identifier) @name
   right: (string) @val) @match
 (#match? @name "` + cred + `"))
`,
			"javascript": `
((variable_declarator
   name: (identifier) @name
   value: (string) @val) @match
 (#match? @name "` + cred + `"))
`,
			"typescript": `
((variable_declarator
   name: (identifier) @name
   value: (string) @val) @match
 (#match? @name "` + cred + `"))
`,
			"ruby": `
((assignment
   left: (identifier) @name
   right: (string) @val) @match
 (#match? @name "` + cred + `"))
`,
		},
		PostFilter: func(qr parser.QueryResult, _ []byte) bool {
			val, ok := qr.Captures["val"]
			if !ok {
				return false
			}
			text := strings.Trim(val.Text, "\"'`")
			if len(text) < 12 {
				return false
			}
			// Reject obvious placeholders.
			lower := strings.ToLower(text)
			for _, marker := range []string{"todo", "fixme", "changeme", "placeholder", "example", "your-", "xxx"} {
				if strings.Contains(lower, marker) {
					return false
				}
			}
			return true
		},
	}
}

// 8. empty-catch (Java / JavaScript / TypeScript / Python) -----------------
//
// A try/except|catch whose body is empty (or only `pass` in
// Python). Silently swallowing an exception is a near-universal
// bug pattern — we want at least a log call or a comment that
// explains why.
func detectorEmptyCatch() *Detector {
	return &Detector{
		Name:        "empty-catch",
		Description: "Catch / except clause with an empty body — silently swallowing exceptions hides production bugs and breaks observability.",
		Severity:    "warning",
		Languages: map[string]string{
			"java": `
((catch_clause body: (block) @body) @match)
`,
			"javascript": `
((catch_clause body: (statement_block) @body) @match)
`,
			"typescript": `
((catch_clause body: (statement_block) @body) @match)
`,
			"python": `
((except_clause (block) @body) @match)
`,
		},
		PostFilter: func(qr parser.QueryResult, _ []byte) bool {
			body, ok := qr.Captures["body"]
			if !ok {
				return false
			}
			text := strings.TrimSpace(body.Text)
			text = strings.TrimPrefix(text, "{")
			text = strings.TrimSuffix(text, "}")
			text = strings.TrimSpace(text)
			// Strip trivial bodies — empty, pass, ellipsis,
			// comment-only.
			lines := strings.Split(text, "\n")
			meaningful := 0
			for _, ln := range lines {
				s := strings.TrimSpace(ln)
				if s == "" || s == "pass" || s == "..." {
					continue
				}
				if strings.HasPrefix(s, "//") || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "*") {
					continue
				}
				meaningful++
			}
			return meaningful == 0
		},
	}
}

// 9. java-string-equality (Java) -------------------------------------------
//
// `s == "foo"` or `"foo" == s` — Java string comparison via `==`
// compares object identity, not content. The bug is famous and
// still common in code that came from C# / Python / JS.
func detectorJavaStringEquality() *Detector {
	return &Detector{
		Name:        "java-string-equality",
		Description: "Java string comparison via `==` (compares object identity, not content). Use `.equals()` or `Objects.equals()`.",
		Severity:    "warning",
		Languages: map[string]string{
			"java": `
[
  ((binary_expression
     left: (identifier)
     operator: "=="
     right: (string_literal)) @match)
  ((binary_expression
     left: (string_literal)
     operator: "=="
     right: (identifier)) @match)
]
`,
		},
	}
}

// 10. python-mutable-default-arg (Python) -----------------------------------
//
// `def foo(x=[])` — the list is created once at def time and
// shared across every call that omits x. One of the most-cited
// Python pitfalls; the safe form is `def foo(x=None): if x is
// None: x = []`.
func detectorPythonMutableDefault() *Detector {
	return &Detector{
		Name:        "python-mutable-default-arg",
		Description: "Python function default value is a mutable container (list / dict / set). The container is created once at def time and shared across every call — almost certainly a bug.",
		Severity:    "warning",
		Languages: map[string]string{
			"python": `
((default_parameter
   value: [(list) (dictionary) (set)]) @match)
`,
		},
	}
}
