package astquery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runDetector is a small test helper that invokes RunOnSource with
// the named detector and returns the result. Tests assert on
// .Total / .Matches[*].Line so a query-level regression (a
// detector that compiles but matches the wrong shape) fails loud.
func runDetector(t *testing.T, name, lang, file, src string) Result {
	t.Helper()
	res, err := RunOnSource(context.Background(), Options{
		Detector: name,
		// Test fixtures use abstract paths like "lib.go" that
		// IsTestPath flags as non-test, so detectors with
		// ExcludeTests=true still fire.
	}, file, lang, []byte(src))
	require.NoError(t, err)
	return res
}

func TestDetector_ErrorNotWrapped_FiresOnPassthrough(t *testing.T) {
	src := `package x

func F() error {
	if err := do(); err != nil {
		return err
	}
	return nil
}
`
	res := runDetector(t, "error-not-wrapped", "go", "lib.go", src)
	require.Equal(t, 1, res.Total, "expected one match for plain pass-through")
	assert.Equal(t, "error-not-wrapped", res.Matches[0].Detector)
	assert.Equal(t, "warning", res.Matches[0].Severity)
}

func TestDetector_ErrorNotWrapped_SkipsWrappedReturns(t *testing.T) {
	src := `package x

import "fmt"

func F() error {
	if err := do(); err != nil {
		return fmt.Errorf("doing: %w", err)
	}
	return nil
}
`
	res := runDetector(t, "error-not-wrapped", "go", "lib.go", src)
	assert.Equal(t, 0, res.Total, "wrapped errors must not match")
}

func TestDetector_SQLStringConcat_Go(t *testing.T) {
	src := `package x

func F(db *sql.DB, name string) {
	rows, _ := db.Query("SELECT * FROM users WHERE name = '" + name + "'")
	_ = rows
}
`
	res := runDetector(t, "sql-string-concat", "go", "lib.go", src)
	require.GreaterOrEqual(t, res.Total, 1)
	assert.Equal(t, "error", res.Matches[0].Severity)
}

func TestDetector_SQLStringConcat_GoParameterised(t *testing.T) {
	src := `package x

func F(db *sql.DB, name string) {
	rows, _ := db.Query("SELECT * FROM users WHERE name = ?", name)
	_ = rows
}
`
	res := runDetector(t, "sql-string-concat", "go", "lib.go", src)
	assert.Equal(t, 0, res.Total, "parameterised query must not match")
}

func TestDetector_WeakCrypto_Go(t *testing.T) {
	src := `package x

import (
	"crypto/md5"
	"crypto/sha256"
)

func F() {
	_ = md5.New()
	_ = sha256.New()
}
`
	res := runDetector(t, "weak-crypto", "go", "lib.go", src)
	require.Equal(t, 1, res.Total, "only md5.New() should match")
}

func TestDetector_WeakCrypto_Python(t *testing.T) {
	src := `import hashlib

def f():
    h1 = hashlib.md5(b"x")
    h2 = hashlib.sha256(b"x")
    return h1, h2
`
	res := runDetector(t, "weak-crypto", "python", "lib.py", src)
	require.Equal(t, 1, res.Total, "only hashlib.md5(...) should match")
}

func TestDetector_PanicInLibrary_Go(t *testing.T) {
	src := `package x

func F() {
	panic("nope")
}
`
	res := runDetector(t, "panic-in-library", "go", "lib.go", src)
	assert.Equal(t, 1, res.Total)
}

func TestDetector_PanicInLibrary_SkipsTestFiles(t *testing.T) {
	// Path ending in _test.go should be classified as a test
	// and skipped by the test-exclusion gate.
	src := `package x

func F() {
	panic("ok in tests")
}
`
	res := runDetector(t, "panic-in-library", "go", "lib_test.go", src)
	assert.Equal(t, 0, res.Total, "panic in test file must not flag")
}

func TestDetector_GoroutineWithoutRecover_Fires(t *testing.T) {
	src := `package x

func F() {
	go func() {
		doSomething()
	}()
}
`
	res := runDetector(t, "goroutine-without-recover", "go", "lib.go", src)
	assert.Equal(t, 1, res.Total)
}

func TestDetector_GoroutineWithoutRecover_SkipsRecoveredBody(t *testing.T) {
	src := `package x

func F() {
	go func() {
		defer func() { _ = recover() }()
		doSomething()
	}()
}
`
	res := runDetector(t, "goroutine-without-recover", "go", "lib.go", src)
	assert.Equal(t, 0, res.Total)
}

func TestDetector_HTTPClientNoTimeout_Fires(t *testing.T) {
	src := `package x

import "net/http"

func F() *http.Client {
	return &http.Client{}
}
`
	res := runDetector(t, "http-client-no-timeout", "go", "lib.go", src)
	assert.Equal(t, 1, res.Total)
}

func TestDetector_HTTPClientNoTimeout_SkipsExplicitTimeout(t *testing.T) {
	src := `package x

import (
	"net/http"
	"time"
)

func F() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}
`
	res := runDetector(t, "http-client-no-timeout", "go", "lib.go", src)
	assert.Equal(t, 0, res.Total)
}

func TestDetector_HardcodedSecret_Go(t *testing.T) {
	// Camel-case `apiKey` is the canonical Go name for the
	// credential — the regex must be case-insensitive to catch it.
	// Values avoid markers ("example", "todo", "your-", …) so the
	// placeholder-rejection post-filter doesn't drop them.
	src := `package x

func F() {
	password := "hunter2hunter2hunter"
	apiKey := "AKIA0123456789ABCDEF"
	emptyDefault := ""
	_ = password
	_ = apiKey
	_ = emptyDefault
}
`
	res := runDetector(t, "hardcoded-secret", "go", "lib.go", src)
	require.Equal(t, 2, res.Total, "expect both password (snake) and apiKey (camel)")
}

func TestDetector_HardcodedSecret_Python(t *testing.T) {
	src := `password = "hunter2hunter2hunter"
api_key = "AKIA0123456789ABCDEF"
empty_default = ""
placeholder = "TODO_set_me"
`
	res := runDetector(t, "hardcoded-secret", "python", "lib.py", src)
	require.Equal(t, 2, res.Total)
}

func TestDetector_EmptyCatch_JavaScript(t *testing.T) {
	src := `function f() {
  try {
    risky();
  } catch (e) {
  }
  try {
    risky();
  } catch (e) {
    log(e);
  }
}
`
	res := runDetector(t, "empty-catch", "javascript", "lib.js", src)
	assert.Equal(t, 1, res.Total)
}

func TestDetector_EmptyCatch_Python(t *testing.T) {
	src := `def f():
    try:
        risky()
    except Exception:
        pass
    try:
        risky()
    except Exception as e:
        log(e)
`
	res := runDetector(t, "empty-catch", "python", "lib.py", src)
	assert.Equal(t, 1, res.Total)
}

func TestDetector_JavaStringEquality_Fires(t *testing.T) {
	src := `class C {
    boolean f(String s) {
        return s == "foo";
    }
    boolean g(String s) {
        return "bar" == s;
    }
    boolean h(String s) {
        return s.equals("safe");
    }
}
`
	res := runDetector(t, "java-string-equality", "java", "C.java", src)
	require.Equal(t, 2, res.Total, "two `==` comparisons must match; .equals() must not")
}

func TestDetector_PythonMutableDefault_Fires(t *testing.T) {
	src := `def f(items=[]):
    items.append(1)
    return items

def g(opts={}):
    return opts

def h(x=None):
    return x
`
	res := runDetector(t, "python-mutable-default-arg", "python", "lib.py", src)
	require.Equal(t, 2, res.Total, "list and dict defaults match; None must not")
}

func TestListDetectors_TenBundled(t *testing.T) {
	names := ListDetectors()
	require.GreaterOrEqual(t, len(names), 10, "expected at least 10 bundled detectors")
	want := map[string]bool{
		"error-not-wrapped":           false,
		"sql-string-concat":           false,
		"weak-crypto":                 false,
		"panic-in-library":            false,
		"goroutine-without-recover":   false,
		"http-client-no-timeout":      false,
		"hardcoded-secret":            false,
		"empty-catch":                 false,
		"java-string-equality":        false,
		"python-mutable-default-arg":  false,
	}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, present := range want {
		assert.True(t, present, "detector %q should be registered", n)
	}
}

func TestRawPattern_GoCallExpression(t *testing.T) {
	// Sanity test for the raw-pattern path: find every panic()
	// call without going through the detector registry.
	src := `package x
func F() { panic("x") }
func G() { _ = "panic"; do() }
`
	res, err := RunOnSource(context.Background(), Options{
		Pattern: `((call_expression function: (identifier) @fn) @match (#eq? @fn "panic"))`,
	}, "lib.go", "go", []byte(src))
	require.NoError(t, err)
	require.Equal(t, 1, res.Total, "only the real panic() call should match; the string literal must not")
}

func TestRawPattern_RejectsBadPattern(t *testing.T) {
	_, err := RunOnSource(context.Background(), Options{
		Pattern: `(this_node_does_not_exist) @match`,
	}, "lib.go", "go", []byte(`package x`))
	require.Error(t, err)
}
