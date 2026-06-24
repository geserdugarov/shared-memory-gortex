package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func hocComponent(nodes []*graph.Node, name string) *graph.Node {
	for _, n := range nodes {
		if n.Name == name && n.Meta != nil {
			if c, _ := n.Meta["component"].(bool); c {
				return n
			}
		}
	}
	return nil
}

func hocRenders(edges []*graph.Edge, from, child string) bool {
	for _, e := range edges {
		if e.Kind == graph.EdgeRendersChild && e.From == from && e.To == "unresolved::"+child {
			return true
		}
	}
	return false
}

func TestReactHOC_MemoForwardRefStyled(t *testing.T) {
	src := []byte("import { memo, forwardRef } from 'react'\n" +
		"import styled from 'styled-components'\n" +
		"const Button = memo(() => <Spinner/>)\n" +
		"const Card = forwardRef((p, ref) => <Inner/>)\n" +
		"const Box = styled.div`color:red`\n" +
		"const helper = memo(() => <Z/>)\n")
	res, err := NewTypeScriptExtractor().Extract("App.tsx", src)
	if err != nil {
		t.Fatal(err)
	}

	if n := hocComponent(res.Nodes, "Button"); n == nil {
		t.Errorf("Button should be flagged a component")
	} else if k, _ := n.Meta["component_kind"].(string); k != "memo" {
		t.Errorf("Button component_kind = %q (want memo)", k)
	}
	if !hocRenders(res.Edges, "App.tsx::Button", "Spinner") {
		t.Errorf("memo render JSX should attribute to the outer Button, not the anon arrow")
	}
	if n := hocComponent(res.Nodes, "Card"); n == nil || n.Meta["component_kind"] != "forwardRef" {
		t.Errorf("Card should be a forwardRef component")
	}
	if !hocRenders(res.Edges, "App.tsx::Card", "Inner") {
		t.Errorf("forwardRef render JSX should attribute to Card")
	}
	if n := hocComponent(res.Nodes, "Box"); n == nil || n.Meta["component_kind"] != "styled" {
		t.Errorf("Box should be a styled component")
	}
	// A lowercase const is not a component.
	if hocComponent(res.Nodes, "helper") != nil {
		t.Errorf("a lowercase const must not be classified as a component")
	}
}

func TestReactHOC_JSXExtractor(t *testing.T) {
	res, err := NewJavaScriptExtractor().Extract("App.jsx", []byte("const Btn = memo(() => <Spinner/>)\n"))
	if err != nil {
		t.Fatal(err)
	}
	if hocComponent(res.Nodes, "Btn") == nil {
		t.Errorf("the JS extractor should classify a memo HOC const as a component")
	}
	if !hocRenders(res.Edges, "App.jsx::Btn", "Spinner") {
		t.Errorf("JS memo render JSX should attribute to Btn")
	}
}

func TestRTKGeneratedHookCodegenTool(t *testing.T) {
	src := []byte("import { createApi, fetchBaseQuery } from '@reduxjs/toolkit/query/react';\n" +
		"export const api = createApi({\n" +
		"  baseQuery: fetchBaseQuery({ baseUrl: '/' }),\n" +
		"  endpoints: (builder) => ({\n" +
		"    getUser: builder.query({ query: (id) => `u/${id}` }),\n" +
		"  }),\n" +
		"});\n")
	res, err := NewTypeScriptExtractor().Extract("api.ts", src)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range res.Nodes {
		if n.Name == "useGetUserQuery" {
			if ct, _ := n.Meta["codegen_tool"].(string); ct != "rtk-query" {
				t.Errorf("useGetUserQuery codegen_tool = %q (want rtk-query)", ct)
			}
			if g, _ := n.Meta["generated"].(bool); !g {
				t.Errorf("useGetUserQuery should carry generated=true")
			}
			return
		}
	}
	t.Fatalf("useGetUserQuery generated-hook node not found")
}
