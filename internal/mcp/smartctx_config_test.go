package mcp

import "testing"

func TestSmartContextDefaultOff(t *testing.T) {
	s := &Server{} // no configManager → empty config
	// No include_* args → every in-pack section off.
	got := s.smartContextSections(map[string]any{}, "")
	if got.Any() {
		t.Errorf("smart_context in-pack sections should default off, got %+v", got)
	}

	// An explicit per-call opt-in turns the section on.
	on := s.smartContextSections(map[string]any{"include_call_paths": true}, "")
	if !on.CallPaths {
		t.Errorf("include_call_paths=true should enable CallPaths, got %+v", on)
	}
	if on.Flows || on.Confidence {
		t.Errorf("only CallPaths should be on, got %+v", on)
	}
}

func TestSmartContextDefaultOff_AttachNoop(t *testing.T) {
	s := &Server{}
	result := map[string]any{"relevant_symbols": []string{}}
	// Default-off sections leave the pack untouched.
	s.attachInPackSections(result, s.smartContextSections(map[string]any{}, ""))
	if _, ok := result["in_pack"]; ok {
		t.Errorf("default-off should not add an in_pack block, got %+v", result["in_pack"])
	}
	// An opted-in section adds the in_pack marker.
	s.attachInPackSections(result, s.smartContextSections(map[string]any{"include_flows": true}, ""))
	blk, ok := result["in_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected in_pack block when opted in, got %T", result["in_pack"])
	}
	if blk["flows"] != true || blk["call_paths"] != false {
		t.Errorf("in_pack block = %+v, want flows on / call_paths off", blk)
	}
}
