package config

import "testing"

func TestSmartContextConfig_ResolveDefaultOff(t *testing.T) {
	// No config, no overrides → every section off (the opt-in default).
	got := SmartContextConfig{}.Resolve(nil, nil, nil)
	if got.Any() {
		t.Errorf("default resolve should be all-off, got %+v", got)
	}
}

func TestSmartContextConfig_ResolveConfigEnable(t *testing.T) {
	// in_pack on + a section enabled → that section on, others off.
	cfg := SmartContextConfig{InPack: true, IncludeCallPaths: true}
	got := cfg.Resolve(nil, nil, nil)
	if !got.CallPaths || got.Flows || got.Confidence {
		t.Errorf("expected only CallPaths on, got %+v", got)
	}

	// in_pack OFF cancels the section default even if the toggle is set.
	off := SmartContextConfig{InPack: false, IncludeCallPaths: true}.Resolve(nil, nil, nil)
	if off.Any() {
		t.Errorf("in_pack off should suppress section defaults, got %+v", off)
	}
}

func TestSmartContextConfig_ResolveOverride(t *testing.T) {
	tr, fa := true, false
	// A per-call override turns a section on even when in_pack is off.
	on := SmartContextConfig{}.Resolve(&tr, nil, nil)
	if !on.CallPaths {
		t.Errorf("override should opt CallPaths in, got %+v", on)
	}
	// And off even when config enables it.
	cfg := SmartContextConfig{InPack: true, IncludeFlows: true}
	got := cfg.Resolve(nil, &fa, nil)
	if got.Flows {
		t.Errorf("override should opt Flows out, got %+v", got)
	}
}
