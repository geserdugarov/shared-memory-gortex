package languages

import "testing"

func TestPHPStringCallable_ArrayMap(t *testing.T) {
	src := []byte(`<?php
function helper($x) { return $x; }
function run($xs) {
    return array_map('helper', $xs);
}
`)
	res, err := NewPHPExtractor().Extract("a.php", src)
	if err != nil {
		t.Fatal(err)
	}
	cands := fnValueCands(res)
	meta, ok := cands["helper"]
	if !ok {
		t.Fatalf("array_map string callable not captured (got: %v)", keys(cands))
	}
	if meta["skip_gate"] != true {
		t.Errorf("skip_gate = %v, want true", meta["skip_gate"])
	}
	if meta["fn_ref_form"] != "php_string_callable" {
		t.Errorf("fn_ref_form = %v, want php_string_callable", meta["fn_ref_form"])
	}
}

func TestPHPStringCallable_NonHOFIgnored(t *testing.T) {
	src := []byte(`<?php
function helper() {}
function run() {
    some_other_fn('helper');
}
`)
	res, err := NewPHPExtractor().Extract("a.php", src)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fnValueCands(res)["helper"]; ok {
		t.Error("a non-HOF call must not trigger string-callable capture")
	}
}

func TestPHPStringCallable_StaticAndArrayForms(t *testing.T) {
	src := []byte(`<?php
function run($a, $svc) {
    usort($a, 'cmp');
    call_user_func('Foo::bar');
    array_map([$svc, 'handle'], $a);
    array_filter($a, ['Acme', 'isValid']);
}
`)
	res, err := NewPHPExtractor().Extract("a.php", src)
	if err != nil {
		t.Fatal(err)
	}
	cands := fnValueCands(res)
	if _, ok := cands["cmp"]; !ok {
		t.Errorf("usort string callable cmp not captured (got: %v)", keys(cands))
	}
	if m, ok := cands["bar"]; !ok || m["fn_ref_recv_hint"] != "Foo" {
		t.Errorf("Foo::bar static-string callable not captured with recv hint (got: %v)", cands["bar"])
	}
	if _, ok := cands["handle"]; !ok {
		t.Errorf("[$svc, 'handle'] array callable not captured (got: %v)", keys(cands))
	}
	if m, ok := cands["isValid"]; !ok || m["fn_ref_recv_hint"] != "Acme" {
		t.Errorf("['Acme','isValid'] array callable not captured with recv hint (got: %v)", cands["isValid"])
	}
}
