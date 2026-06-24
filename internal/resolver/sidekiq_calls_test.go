package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func sidekiqWorker(g *graph.Graph, id, file, worker string) {
	g.AddNode(&graph.Node{ID: id, Kind: graph.KindMethod, Name: "perform", FilePath: file, Language: "ruby",
		Meta: map[string]any{"receiver": worker, "sidekiq_worker": worker}})
}

func sidekiqEnqueue(g *graph.Graph, fromID, file, worker string) {
	if g.GetNode(fromID) == nil {
		g.AddNode(&graph.Node{ID: fromID, Kind: graph.KindMethod, Name: lastSeg(fromID), FilePath: file, Language: "ruby"})
	}
	g.AddEdge(&graph.Edge{From: fromID, To: "unresolved::*.perform", Kind: graph.EdgeCalls, FilePath: file,
		Meta: map[string]any{"via": sidekiqVia, "sidekiq_worker": worker}})
}

func synthSidekiqEdge(g graph.Store, from, to string) *graph.Edge {
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.From != from || e.To != to || e.Meta == nil {
			continue
		}
		if by, _ := e.Meta[MetaSynthesizedBy].(string); by == SynthSidekiq {
			return e
		}
	}
	return nil
}

func TestResolveSidekiqCalls_PerformAsyncBindsWorker(t *testing.T) {
	g := graph.New()
	sidekiqWorker(g, "app.rb::EmailJob.perform", "app.rb", "EmailJob")
	sidekiqEnqueue(g, "app.rb::Controller.notify", "app.rb", "EmailJob")

	n := ResolveSidekiqCalls(g)
	require.Equal(t, 1, n)
	e := synthSidekiqEdge(g, "app.rb::Controller.notify", "app.rb::EmailJob.perform")
	require.NotNil(t, e)
	assert.Equal(t, ConfidenceTyped, e.Confidence)
	assert.Equal(t, ProvenanceFramework, e.Meta[MetaProvenance])
}

func TestResolveSidekiqCalls_NamespacedDispatchBindsBySimpleName(t *testing.T) {
	// The worker node is tagged `ReportJob`; the dispatch references it as
	// `Workers::ReportJob`. The simple-name match bridges them.
	g := graph.New()
	sidekiqWorker(g, "app.rb::ReportJob.perform", "app.rb", "ReportJob")
	sidekiqEnqueue(g, "app.rb::C.go", "app.rb", "Workers::ReportJob")

	require.Equal(t, 1, ResolveSidekiqCalls(g))
	assert.NotNil(t, synthSidekiqEdge(g, "app.rb::C.go", "app.rb::ReportJob.perform"))
}

func TestResolveSidekiqCalls_UnknownWorkerStaysPlaceholder(t *testing.T) {
	g := graph.New()
	sidekiqWorker(g, "app.rb::EmailJob.perform", "app.rb", "EmailJob")
	sidekiqEnqueue(g, "app.rb::C.go", "app.rb", "GhostJob")

	assert.Equal(t, 0, ResolveSidekiqCalls(g))
	assert.Nil(t, synthSidekiqEdge(g, "app.rb::C.go", "app.rb::EmailJob.perform"))
}
