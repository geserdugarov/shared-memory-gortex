package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the ui_component framework marker (and the mirrored
// type_flavor="component") onto already-detected SFC / SwiftUI / Fabric
// component nodes, alongside the pre-existing component markers.

func TestUIComponent_Svelte(t *testing.T) {
	src := `<script lang="ts">
  let count = 0
</script>
<button>{count}</button>
`
	res, err := NewSvelteExtractor().Extract("Counter.svelte", []byte(src))
	require.NoError(t, err)
	comp := nodeByName(res.Nodes, "Counter")
	require.NotNil(t, comp)
	assert.Equal(t, "svelte", comp.Meta["ui_component"])
	assert.Equal(t, "component", comp.Meta["type_flavor"])
	assert.Equal(t, true, comp.Meta["component"]) // dual-write
}

func TestUIComponent_Vue(t *testing.T) {
	src := `<script setup lang="ts">
const count = 0
</script>
<template><button>{{ count }}</button></template>
`
	res, err := NewVueExtractor().Extract("components/Counter.vue", []byte(src))
	require.NoError(t, err)
	comp := nodeByName(res.Nodes, "Counter")
	require.NotNil(t, comp)
	assert.Equal(t, "vue", comp.Meta["ui_component"])
	assert.Equal(t, "component", comp.Meta["type_flavor"])
	assert.Equal(t, true, comp.Meta["component"]) // dual-write
}

func TestUIComponent_Astro(t *testing.T) {
	src := `---
const title = "Home"
---
<html><body>{title}</body></html>
`
	res, err := NewAstroExtractor().Extract("pages/index.astro", []byte(src))
	require.NoError(t, err)
	comp := nodeByName(res.Nodes, "index")
	require.NotNil(t, comp)
	assert.Equal(t, "astro", comp.Meta["ui_component"])
	assert.Equal(t, "component", comp.Meta["type_flavor"])
	assert.Equal(t, true, comp.Meta["component"]) // dual-write
}

func TestUIComponent_Razor(t *testing.T) {
	src := `@page "/counter"
<h1>Counter</h1>
@code {
    private int count = 0;
}
`
	res, err := NewRazorExtractor().Extract("Counter.razor", []byte(src))
	require.NoError(t, err)
	comp := nodeByName(res.Nodes, "Counter")
	require.NotNil(t, comp)
	assert.Equal(t, "razor", comp.Meta["ui_component"])
	assert.Equal(t, "component", comp.Meta["type_flavor"])
	assert.Equal(t, true, comp.Meta["component"]) // dual-write
}

func TestUIComponent_SwiftUI(t *testing.T) {
	src := []byte(`import SwiftUI

struct ContentView: View {
    var body: some View { Text("hi") }
}
`)
	res, err := NewSwiftExtractor().Extract("App/ContentView.swift", src)
	require.NoError(t, err)
	comp := nodeByName(res.Nodes, "ContentView")
	require.NotNil(t, comp)
	assert.Equal(t, "swiftui", comp.Meta["ui_component"])
	assert.Equal(t, "component", comp.Meta["swiftui_role"]) // dual-write
}

func TestUIComponent_FabricTS(t *testing.T) {
	src := `import codegenNativeComponent from 'react-native/Libraries/Utilities/codegenNativeComponent';
export default codegenNativeComponent<NativeProps>('RCTColorView');
`
	res, err := NewTypeScriptExtractor().Extract("ColorViewNativeComponent.ts", []byte(src))
	require.NoError(t, err)
	comp := fabricNode(res.Nodes)
	require.NotNil(t, comp)
	assert.Equal(t, "react", comp.Meta["ui_component"])
	assert.Equal(t, "component", comp.Meta["type_flavor"])
	assert.Equal(t, "RCTColorView", comp.Meta["fabric_component"]) // dual-write
}

func TestUIComponent_FabricObjC(t *testing.T) {
	src := `@implementation RCTColorViewManager

RCT_EXPORT_MODULE()

RCT_EXPORT_VIEW_PROPERTY(color, NSString)

@end
`
	res, err := NewObjCExtractor().Extract("RCTColorViewManager.m", []byte(src))
	require.NoError(t, err)
	comp := fabricNode(res.Nodes)
	require.NotNil(t, comp)
	assert.Equal(t, "react", comp.Meta["ui_component"])
	assert.Equal(t, "component", comp.Meta["type_flavor"])
}
