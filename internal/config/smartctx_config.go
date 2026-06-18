package config

// SmartContextConfig governs the optional in-pack enrichment sections
// smart_context can attach to its assembled pack — anchored call-paths, the
// flow spine, and the retrieval-confidence verdict. Every section is OFF by
// default: smart_context stays a minimal working-set assembler unless a project
// opts in here or a caller opts in per call. This keeps the default pack small
// and predictable, in line with Gortex's capability-only philosophy.
type SmartContextConfig struct {
	// InPack is the master switch for in-pack enrichment. With it off (the
	// default) the per-section toggles below have no effect — only an explicit
	// per-call override can still turn a section on.
	InPack bool `mapstructure:"in_pack" yaml:"in_pack,omitempty"`
	// IncludeCallPaths attaches anchored call-path chains to the pack.
	IncludeCallPaths bool `mapstructure:"include_call_paths" yaml:"include_call_paths,omitempty"`
	// IncludeFlows attaches the flow spine and dynamic-boundary announce.
	IncludeFlows bool `mapstructure:"include_flows" yaml:"include_flows,omitempty"`
	// IncludeConfidence attaches the retrieval-confidence verdict.
	IncludeConfidence bool `mapstructure:"include_confidence" yaml:"include_confidence,omitempty"`
}

// SmartContextSections is the resolved set of in-pack enrichment sections for
// one smart_context call.
type SmartContextSections struct {
	CallPaths  bool
	Flows      bool
	Confidence bool
}

// Any reports whether at least one in-pack section is enabled.
func (s SmartContextSections) Any() bool {
	return s.CallPaths || s.Flows || s.Confidence
}

// Resolve merges the project config with optional per-call overrides. A nil
// override inherits the config (whose section default is gated by InPack); a
// non-nil override wins outright, so a caller can opt a section in even when
// in_pack is off in config, or opt out when it is on. With no config and no
// overrides every section is off — the opt-in default.
func (c SmartContextConfig) Resolve(callPaths, flows, confidence *bool) SmartContextSections {
	gate := func(override *bool, enabled bool) bool {
		if override != nil {
			return *override
		}
		return c.InPack && enabled
	}
	return SmartContextSections{
		CallPaths:  gate(callPaths, c.IncludeCallPaths),
		Flows:      gate(flows, c.IncludeFlows),
		Confidence: gate(confidence, c.IncludeConfidence),
	}
}
