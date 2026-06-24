package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func sidekiqWorkerNode(nodes []*graph.Node, id string) *graph.Node {
	for _, n := range nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func sidekiqPlaceholder(edges []*graph.Edge, worker string) *graph.Edge {
	for _, e := range edges {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "sidekiq-dispatch" {
			continue
		}
		if w, _ := e.Meta["sidekiq_worker"].(string); w == worker {
			return e
		}
	}
	return nil
}

func TestSidekiq_TagsWorkerAndDispatches(t *testing.T) {
	src := `class EmailJob
  include Sidekiq::Job
  def perform(id)
  end
end

module Workers
  class ReportJob
    include Sidekiq::Worker
    def perform(id)
    end
  end
end

class Controller
  def notify(user)
    EmailJob.perform_async(user.id)
    Workers::ReportJob.perform_in(60, user.id)
  end
end
`
	res, err := NewRubyExtractor().Extract("app.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	email := sidekiqWorkerNode(res.Nodes, "app.rb::EmailJob.perform")
	if email == nil || email.Meta["sidekiq_worker"] != "EmailJob" {
		t.Fatalf("EmailJob#perform not tagged: %+v", email)
	}
	report := sidekiqWorkerNode(res.Nodes, "app.rb::ReportJob.perform")
	if report == nil || report.Meta["sidekiq_worker"] != "ReportJob" {
		t.Fatalf("Sidekiq::Worker ReportJob#perform not tagged: %+v", report)
	}

	ph := sidekiqPlaceholder(res.Edges, "EmailJob")
	if ph == nil {
		t.Fatalf("no placeholder for EmailJob.perform_async")
	}
	if ph.From != "app.rb::Controller.notify" {
		t.Errorf("placeholder From = %q", ph.From)
	}
	if sidekiqPlaceholder(res.Edges, "Workers::ReportJob") == nil {
		t.Errorf("no placeholder for Workers::ReportJob.perform_in")
	}
}

func TestSidekiq_NonWorkerClassNotTagged(t *testing.T) {
	// A class without an include Sidekiq mixin is not a worker.
	src := `class PlainService
  def perform(id)
  end
end
`
	res, err := NewRubyExtractor().Extract("svc.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	n := sidekiqWorkerNode(res.Nodes, "svc.rb::PlainService.perform")
	if n != nil && n.Meta["sidekiq_worker"] != nil {
		t.Errorf("a non-Sidekiq class must not be tagged a worker")
	}
}
