package languages

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestFactoryChainReceiverExprStamped pins that a factory chain whose hop the
// in-extractor walk cannot type (the method lives cross-file) preserves the
// receiver expression under Meta["receiver_expr"] for the resolver pass.
func TestFactoryChainReceiverExprStamped(t *testing.T) {
	src := []byte(`package main

type Builder struct{}

func New() *Builder { return &Builder{} }

func run() {
	New().Frob().Run()
}
`)
	res, err := NewGoExtractor().Extract("main.go", src)
	if err != nil {
		t.Fatal(err)
	}
	var runEdge *graph.Edge
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeCalls && strings.HasSuffix(e.To, ".Run") {
			runEdge = e
		}
	}
	if runEdge == nil {
		t.Fatal("Run() call edge not found")
	}
	if got, _ := runEdge.Meta["receiver_expr"].(string); got != "New().Frob()" {
		t.Errorf("receiver_expr = %q, want New().Frob()", got)
	}
}
