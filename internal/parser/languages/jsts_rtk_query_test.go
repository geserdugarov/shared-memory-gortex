package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func rtkPlaceholderCount(fix *extractedFixture) int {
	n := 0
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if v, _ := e.Meta["via"].(string); v == "rtk-query" {
			n++
		}
	}
	return n
}

func TestRTKQuery_EndpointAndHookNodes(t *testing.T) {
	src := "import { createApi, fetchBaseQuery } from '@reduxjs/toolkit/query/react';\n" +
		"export const api = createApi({\n" +
		"  baseQuery: fetchBaseQuery({ baseUrl: '/' }),\n" +
		"  endpoints: (builder) => ({\n" +
		"    getUser: builder.query({ query: (id) => `u/${id}` }),\n" +
		"    updateUser: builder.mutation({ query: (u) => ({ url: 'u', method: 'POST' }) }),\n" +
		"  }),\n" +
		"});\n"
	fix := runTSExtractFixture(t, "api.ts", src)

	ep := fix.nodesByID["api.ts::api.getUser"]
	if ep == nil || ep.Meta["rtk_kind"] != "query" {
		t.Fatalf("getUser endpoint node missing/wrong: %+v", ep)
	}
	mut := fix.nodesByID["api.ts::api.updateUser"]
	if mut == nil || mut.Meta["rtk_kind"] != "mutation" {
		t.Fatalf("updateUser endpoint node missing/wrong: %+v", mut)
	}
	if h := fix.nodesByID["api.ts::useGetUserQuery"]; h == nil || h.Meta["rtk_generated_hook"] != true {
		t.Errorf("useGetUserQuery generated-hook node missing")
	}
	if h := fix.nodesByID["api.ts::useUpdateUserMutation"]; h == nil || h.Meta["rtk_generated_hook"] != true {
		t.Errorf("useUpdateUserMutation generated-hook node missing")
	}
	if got := rtkPlaceholderCount(fix); got != 2 {
		t.Errorf("want 2 hook→endpoint placeholders, got %d", got)
	}
}

func TestRTKQuery_HandWrittenHookSuppressesSynthetic(t *testing.T) {
	src := "import { createApi } from '@reduxjs/toolkit/query/react';\n" +
		"function useGetUserQuery() { return null; }\n" +
		"export const api = createApi({\n" +
		"  endpoints: (builder) => ({\n" +
		"    getUser: builder.query({ query: () => 'u' }),\n" +
		"  }),\n" +
		"});\n"
	fix := runTSExtractFixture(t, "api.ts", src)

	if fix.nodesByID["api.ts::api.getUser"] == nil {
		t.Fatalf("endpoint node should still be minted")
	}
	for _, n := range fix.nodesByID {
		if n.Name == "useGetUserQuery" && n.Meta["rtk_generated_hook"] == true {
			t.Errorf("hand-written useGetUserQuery must suppress the synthetic hook node")
		}
	}
	if got := rtkPlaceholderCount(fix); got != 0 {
		t.Errorf("no rtk-query placeholder when the hook is hand-written, got %d", got)
	}
}

func TestRTKQuery_JSReturnBlockShape(t *testing.T) {
	// JS extractor + builder arrow with an explicit return block.
	src := "const api = createApi({\n" +
		"  endpoints: (build) => {\n" +
		"    return { listItems: build.query({ query: () => 'items' }) };\n" +
		"  },\n" +
		"});\n"
	fix := runJSExtractFixture(t, "api.js", src)
	if fix.nodesByID["api.js::api.listItems"] == nil {
		t.Errorf("endpoint from a return-block builder not detected")
	}
	if fix.nodesByID["api.js::useListItemsQuery"] == nil {
		t.Errorf("generated hook for return-block builder not detected")
	}
}
