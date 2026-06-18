package languages

import (
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Function-as-value capture.
//
// When a function is passed as a *value* rather than called — registering a
// handler (`router.Get("/x", handler)`), a callback (`list.forEach(process)`),
// an observer (`signal.connect(onChange)`), `&fn` / `Class::method` /
// `method(:sym)` special forms — no direct call edge exists, yet the function
// is genuinely reachable through the registration. A per-language AST walk
// collects each such value-position identifier as a FnValueCandidate; this file
// is the shared capture table and placeholder emitter. The resolver's
// ResolveFnValueCallbacks gate then binds each candidate to a real same-file
// function and drops the unbound ones.
//
// Splitting capture (here) from the gate (resolver) keeps the two halves
// independently testable and lets every language reuse one emitter instead of
// hand-rolling placeholder edges. The per-language value-position walks land on
// top of this skeleton.

// fnValueUnresolvedPrefix is the synthetic-target namespace a captured
// function value occupies until the gate binds it. Kept human-readable
// (the bare name is appended) to match Gortex's navigable-ID convention.
const fnValueUnresolvedPrefix = "unresolved::fnvalue::"

// fnValueCandidateVia marks a placeholder edge as awaiting the callback gate.
// It mirrors the resolver-side constant of the same value; the two packages
// share the string convention, not a symbol, to avoid an import cycle.
const fnValueCandidateVia = "callback_candidate"

// FnValueCandidate is one captured function-as-value reference: the identifier
// used in a value position (Name), the enclosing symbol or registration site it
// was found in (FromID), and its source location. A per-language walk
// accumulates these during extraction and flushes them with
// EmitFnValueCandidates.
type FnValueCandidate struct {
	FromID   string
	Name     string
	FilePath string
	Line     int
}

// EmitFnValueCandidates appends one placeholder reference edge per candidate to
// result. Each edge targets the fn-value namespace and carries the captured
// name in Meta so the resolver gate can bind it; the edge rides
// OriginSpeculative until then. Candidates missing a source site or name are
// skipped.
func EmitFnValueCandidates(result *parser.ExtractionResult, cands []FnValueCandidate) {
	for _, c := range cands {
		if c.FromID == "" || c.Name == "" {
			continue
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From:     c.FromID,
			To:       fnValueUnresolvedPrefix + c.Name,
			Kind:     graph.EdgeReferences,
			FilePath: c.FilePath,
			Line:     c.Line,
			Origin:   graph.OriginSpeculative,
			Meta: map[string]any{
				"via":           fnValueCandidateVia,
				"fn_value_name": c.Name,
			},
		})
	}
}
