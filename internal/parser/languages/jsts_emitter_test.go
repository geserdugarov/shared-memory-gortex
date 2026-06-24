package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestJSEmitter_NamedHandlerAttributedToHandler(t *testing.T) {
	// A bare emitter (no `events` import) with a named handler: the listen
	// edge attributes to the handler node, not the .on call's enclosing
	// function, so the resolver's synthesized call lands on onReady.
	src := `function onReady() {}

function boot(emitter) {
  emitter.on('ready', onReady);
  emitter.emit('ready');
}
`
	fix := runJSExtractFixture(t, "app.js", src)

	events := fix.nodesByKind[graph.KindEvent]
	if len(events) != 1 || events[0].ID != "event::emitter::emitter::ready" {
		t.Fatalf("want 1 emitter topic event::emitter::emitter::ready, got %+v", events)
	}
	if tr, _ := events[0].Meta["transport"].(string); tr != "emitter" {
		t.Errorf("topic transport = %q (want emitter)", tr)
	}

	listens := fix.edgesByKind[graph.EdgeListensOn]
	if len(listens) != 1 {
		t.Fatalf("want 1 EdgeListensOn, got %d", len(listens))
	}
	if listens[0].From != "app.js::onReady" {
		t.Errorf("listen From = %q (want app.js::onReady — the handler, not the enclosing fn)", listens[0].From)
	}

	emits := fix.edgesByKind[graph.EdgeEmits]
	if len(emits) != 1 {
		t.Fatalf("want 1 EdgeEmits, got %d", len(emits))
	}
	if emits[0].From != "app.js::boot" {
		t.Errorf("emit From = %q (want app.js::boot — the enclosing fn)", emits[0].From)
	}
}

func TestJSEmitter_AnonymousHandlerFallsBackToEnclosingFn(t *testing.T) {
	// An inline arrow handler has no named node, so the listen edge falls
	// back to the enclosing function — and the topic node still appears.
	src := `function boot(emitter) {
  emitter.on('tick', () => { doWork(); });
}
`
	fix := runJSExtractFixture(t, "a.js", src)
	if got := len(fix.nodesByKind[graph.KindEvent]); got != 1 {
		t.Fatalf("anonymous-handler emitter should still produce 1 topic node, got %d", got)
	}
	listens := fix.edgesByKind[graph.EdgeListensOn]
	if len(listens) != 1 {
		t.Fatalf("want 1 EdgeListensOn, got %d", len(listens))
	}
	if listens[0].From != "a.js::boot" {
		t.Errorf("anon handler listen From = %q (want enclosing fn a.js::boot)", listens[0].From)
	}
}

func TestJSEmitter_ImportGatedPathStillWins(t *testing.T) {
	// With the `events` import the import-gated pub/sub path classifies the
	// call, so the emitter-literal fallback must NOT also fire (one node).
	src := `const EventEmitter = require('events');

function boot(emitter) {
  emitter.on('ready', onReady);
  emitter.emit('ready');
}
`
	fix := runJSExtractFixture(t, "g.js", src)
	events := fix.nodesByKind[graph.KindEvent]
	if len(events) != 1 {
		t.Fatalf("want exactly 1 topic node (no fallback double-emit), got %d: %+v", len(events), events)
	}
	if events[0].ID != "event::pubsub::eventemitter::ready" {
		t.Errorf("topic id = %q (want the import-gated pubsub node)", events[0].ID)
	}
}

func TestJSEmitter_TemplateLiteralEventSkipped(t *testing.T) {
	// A computed / template-literal event name has no stable literal, so
	// no emitter topic node is produced.
	src := "function boot(emitter) {\n  emitter.on(`evt-${id}`, cb);\n}\n"
	fix := runJSExtractFixture(t, "t.js", src)
	if got := len(fix.nodesByKind[graph.KindEvent]); got != 0 {
		t.Errorf("template-literal event must produce no topic node, got %d", got)
	}
}

func TestTSEmitter_LiteralFallback(t *testing.T) {
	src := `function boot(bus: any) {
  bus.on('open', onOpen);
  bus.emit('open');
}

function onOpen() {}
`
	fix := runTSExtractFixture(t, "ws.ts", src)
	events := fix.nodesByKind[graph.KindEvent]
	if len(events) != 1 || events[0].ID != "event::emitter::bus::open" {
		t.Fatalf("want event::emitter::bus::open, got %+v", events)
	}
	listens := fix.edgesByKind[graph.EdgeListensOn]
	if len(listens) != 1 || listens[0].From != "ws.ts::onOpen" {
		t.Errorf("listen From = %v (want ws.ts::onOpen)", listens)
	}
}
