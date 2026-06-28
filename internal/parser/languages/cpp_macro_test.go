package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// A qualified call hidden in a C++ macro body — `ns::f()` — is recovered
// by sub-parsing the replacement list and taking the rightmost segment of
// the qualified_identifier. The regex scan would have captured `ns`, not
// the actual callee `f`.
func TestCppExtractor_MacroQualifiedCall(t *testing.T) {
	src := []byte(`#define NS_CALL() ns::f()
#define DEEP_CALL() a::b::deep()
`)
	result, err := NewCppExtractor().Extract("m.cpp", src)
	require.NoError(t, err)

	assert.Contains(t, macroCallTargets(result, "m.cpp::NS_CALL"),
		"unresolved::f")
	assert.Contains(t, macroCallTargets(result, "m.cpp::DEEP_CALL"),
		"unresolved::deep")
}

// A C++ member call in a macro body is recovered just like in C.
func TestCppExtractor_MacroMemberCall(t *testing.T) {
	src := []byte("#define INVOKE(o) (o)->execute()\n")
	result, err := NewCppExtractor().Extract("m.cpp", src)
	require.NoError(t, err)
	assert.Contains(t, macroCallTargets(result, "m.cpp::INVOKE"),
		"unresolved::execute")
}

// A plain C++ macro call must still be recovered (no regression), and a
// macro node is still emitted for it.
func TestCppExtractor_MacroPlainCallRegression(t *testing.T) {
	src := []byte("#define TRACE(x) log_event(x)\n")
	result, err := NewCppExtractor().Extract("m.cpp", src)
	require.NoError(t, err)

	macros := nodesOfKind(result.Nodes, graph.KindMacro)
	var found bool
	for _, m := range macros {
		if m.Name == "TRACE" {
			found = true
		}
	}
	require.True(t, found, "the macro node is emitted")
	assert.Contains(t, macroCallTargets(result, "m.cpp::TRACE"),
		"unresolved::log_event")
}
