package mcp

import "github.com/zzet/gortex/internal/config"

// smartContextSections resolves which in-pack enrichment sections a
// smart_context call should attach. Per-call include_* params override the
// project's smart_context config; every section is off by default.
func (s *Server) smartContextSections(args map[string]any, relPath string) config.SmartContextSections {
	cfg := config.SmartContextConfig{}
	if s.configManager != nil {
		cfg = s.configManager.GetRepoConfig(repoPrefixForPath(s, relPath)).MCP.SmartContext
	}
	return cfg.Resolve(
		boolPtrArg(args, "include_call_paths"),
		boolPtrArg(args, "include_flows"),
		boolPtrArg(args, "include_confidence"),
	)
}

// boolPtrArg returns a *bool: the parsed value when the caller passed the key,
// nil when absent — so an unset flag inherits config rather than forcing false.
func boolPtrArg(args map[string]any, key string) *bool {
	if v, set := boolArg(args, key); set {
		return &v
	}
	return nil
}

// attachInPackSections records the opt-in in-pack enrichment sections on the
// assembled pack. It is a no-op while every section is off — keeping the
// default pack untouched — and otherwise marks which sections are active. Later
// passes populate each section's content (anchored call-paths, the flow spine,
// the retrieval-confidence verdict) under this block.
func (s *Server) attachInPackSections(result map[string]any, sections config.SmartContextSections) {
	if !sections.Any() {
		return
	}
	result["in_pack"] = map[string]any{
		"call_paths": sections.CallPaths,
		"flows":      sections.Flows,
		"confidence": sections.Confidence,
	}
}
