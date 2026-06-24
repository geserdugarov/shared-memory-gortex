package languages

import (
	"encoding/json"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// LiquidJSONExtractor handles Shopify OS 2.0 JSON templates and section groups
// (templates/*.json, sections/*.json). A modern theme wires its sections via a
// `sections` object keyed by id whose values name a section file by `type` —
// not the legacy `{% section %}` tag — so this is the primary section-linking
// mechanism. It is content-sniff-routed only (detect_content.go); it never
// claims the bare .json extension, so package.json / tsconfig.json still route
// to the generic JSON extractor.
type LiquidJSONExtractor struct{}

// NewLiquidJSONExtractor constructs a LiquidJSONExtractor.
func NewLiquidJSONExtractor() *LiquidJSONExtractor { return &LiquidJSONExtractor{} }

func (e *LiquidJSONExtractor) Language() string { return "liquid_json" }

// Extensions is empty: the extractor is reached only through the Shopify-theme
// content sniff, never by extension, so it cannot shadow the JSON extractor.
func (e *LiquidJSONExtractor) Extensions() []string { return nil }

// shopifyTemplate is the relevant slice of an OS 2.0 JSON template: a `sections`
// map whose values name a section file by `type`.
type shopifyTemplate struct {
	Sections map[string]struct {
		Type string `json:"type"`
	} `json:"sections"`
}

// Extract links a JSON template / section group to each `sections/<type>.liquid`
// it references, deduped per type. It reuses liquidSectionPath so a JSON-linked
// section resolves to the same target node as a `{% section %}`-tag-linked one.
func (e *LiquidJSONExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	result := &parser.ExtractionResult{}
	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: 1 + strings.Count(string(src), "\n"),
		Language: "liquid_json",
	}
	result.Nodes = append(result.Nodes, fileNode)

	var doc shopifyTemplate
	if err := json.Unmarshal(src, &doc); err != nil {
		return result, nil // not a parseable template — just the file node
	}
	seen := map[string]bool{}
	for _, s := range doc.Sections {
		if s.Type == "" {
			continue
		}
		mod := liquidSectionPath(s.Type)
		if seen[mod] {
			continue // dedupe per section type
		}
		seen[mod] = true
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: 1,
		})
		// A searchable import node per distinct section type, consistent with
		// the tag-based section nodes (Gaps 9/10) — stamped json_section and
		// resolving to the same sections/<type>.liquid target.
		nodeID := filePath + "::import::" + mod
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: nodeID, Kind: graph.KindImport, Name: s.Type,
			FilePath: filePath, StartLine: 1, EndLine: 1, Language: "liquid_json",
			Meta: map[string]any{"liquid_tag": "json_section", "target": mod},
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: nodeID, Kind: graph.EdgeDefines, FilePath: filePath, Line: 1,
		})
	}
	return result, nil
}
