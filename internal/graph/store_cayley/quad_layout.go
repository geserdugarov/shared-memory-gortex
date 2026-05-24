// Package store_cayley provides a Cayley-backed implementation of
// graph.Store. Cayley is a pure-Go quad store with multiple query
// languages and pluggable on-disk backends; this implementation uses
// the bolt-backed KV backend (github.com/cayleygraph/cayley/graph/kv/bolt)
// to keep the binary CGO-free on this code path.
//
// Quad layout
// -----------
//
// Cayley stores graphs as quads (subject, predicate, object, label).
// We map our property graph as follows.
//
// Node subject is an IRI: "node:<id>". Each Node is materialised as a
// fixed set of quads — one per non-zero field — sharing that subject:
//
//	(node:<id>, kind,             "<kind>",             label="node")
//	(node:<id>, name,             "<name>",             label="node")
//	(node:<id>, qualName,         "<qualName>",         label="node")
//	(node:<id>, filePath,         "<filePath>",         label="node")
//	(node:<id>, startLine,        Int(<startLine>),     label="node")
//	(node:<id>, endLine,          Int(<endLine>),       label="node")
//	(node:<id>, language,         "<language>",         label="node")
//	(node:<id>, repoPrefix,       "<repoPrefix>",       label="node")
//	(node:<id>, workspaceID,      "<workspaceID>",      label="node")
//	(node:<id>, projectID,        "<projectID>",        label="node")
//	(node:<id>, absoluteFilePath, "<absoluteFilePath>", label="node")
//	(node:<id>, meta,             gob-blob,             label="node")
//
// Edge subject is a composite IRI carrying the full identity tuple so
// that (From, To, Kind, FilePath, Line) deduplicates naturally — re-adding
// the same edge updates the same quads:
//
//	"edge:<from>|<to>|<kind>|<file>|<line>"
//
// Each Edge is materialised as a fixed set of quads sharing that subject:
//
//	(edge:..., kind,              "<kind>",            label="edge")
//	(edge:..., from,              "node:<from>",       label="edge")
//	(edge:..., to,                "node:<to>",         label="edge")
//	(edge:..., filePath,          "<filePath>",        label="edge")
//	(edge:..., line,              Int(<line>),         label="edge")
//	(edge:..., confidence,        Float(<confidence>), label="edge")
//	(edge:..., confidenceLabel,   "<confidenceLabel>", label="edge")
//	(edge:..., origin,            "<origin>",          label="edge")
//	(edge:..., tier,              "<tier>",            label="edge")
//	(edge:..., crossRepo,         Bool,                label="edge")
//	(edge:..., meta,              gob-blob,            label="edge")
//
// Label discriminates node-subject quads from edge-subject quads in a
// single mixed scan; we use the IRIs "kind:node" and "kind:edge".
//
// Encoding notes
// --------------
//
//   - String predicates and object values use quad.String for unicode
//     safety. Composite IDs in the subject position use quad.IRI.
//   - Numeric fields (StartLine, EndLine, Line) use quad.Int so the
//     KV backend keeps the typed value intact across round-trip.
//   - Confidence uses quad.Float; CrossRepo uses quad.Bool.
//   - Meta map[string]any is gob-encoded to bytes and stored as a
//     quad.String of the base64-decoded payload — quad.String is
//     bytes-safe in this version of cayley.
//   - Empty / zero values are omitted to keep the typical node/edge
//     small. Decoding fills the corresponding Go-struct field with its
//     zero value when the predicate is absent.
package store_cayley

import "github.com/cayleygraph/quad"

// Subject IRI prefixes.
const (
	nodeSubjectPrefix = "node:"
	edgeSubjectPrefix = "edge:"
)

// Discriminator label IRIs that ride on every quad we materialise.
// Cayley label is the fourth quad position; we use it as a kind tag so
// QuadIterator(Label, labelNode|labelEdge) can scan one subtree.
var (
	labelNode = quad.IRI("kind:node")
	labelEdge = quad.IRI("kind:edge")
)

// Predicate IRIs. Defined once so cayley's interning table records each
// predicate exactly once across the whole store.
var (
	predKind             = quad.IRI("kind")
	predName             = quad.IRI("name")
	predQualName         = quad.IRI("qualName")
	predFilePath         = quad.IRI("filePath")
	predStartLine        = quad.IRI("startLine")
	predEndLine          = quad.IRI("endLine")
	predLanguage         = quad.IRI("language")
	predRepoPrefix       = quad.IRI("repoPrefix")
	predWorkspaceID      = quad.IRI("workspaceID")
	predProjectID        = quad.IRI("projectID")
	predAbsoluteFilePath = quad.IRI("absoluteFilePath")
	predMeta             = quad.IRI("meta")

	predFrom            = quad.IRI("from")
	predTo              = quad.IRI("to")
	predLine            = quad.IRI("line")
	predConfidence      = quad.IRI("confidence")
	predConfidenceLabel = quad.IRI("confidenceLabel")
	predOrigin          = quad.IRI("origin")
	predTier            = quad.IRI("tier")
	predCrossRepo       = quad.IRI("crossRepo")
)
