package mcp

import "github.com/zzet/gortex/internal/config"

// SetSearchConfig installs the `.gortex.yaml::search` block on the
// server. Called by the server / daemon entrypoint right after
// NewServer, alongside SetArtifacts / SetNamedQueries. The block
// supplies the keyword-soup rewrite mode, equivalence-class
// expansion settings, and the prose-indexing toggle consumed by the
// search handlers. A no-op-friendly zero value keeps every knob at
// its documented default.
func (s *Server) SetSearchConfig(cfg config.SearchConfig) {
	s.searchCfg = cfg
}

// searchConfig returns the installed search config. The zero value is
// valid -- every accessor on config.SearchConfig folds an empty field
// into its default.
func (s *Server) searchConfig() config.SearchConfig {
	return s.searchCfg
}
