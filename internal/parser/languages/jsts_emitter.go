package languages

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
)

// Emitter-literal fallback channel (F15, extends jsts_pubsub.go).
//
// The import-gated pub/sub path (detectJSPubsubCall) only recognises a
// bare `recv.on('event', …)` / `recv.emit('event')` when the file imports
// a recognised event library (`events` / `eventemitter3` / socket.io).
// A custom emitter base class, an inline `new EventEmitter()`, or an
// anonymous-handler `.on()` in a file with no such import produces no
// topic node, so the emit↔listen pair is lost.
//
// This file backstops that path: when detectJSPubsubCall declines a call,
// the JS/TS extractors try detectJSEmitterLiteralCall, which materialises
// an emitter-literal KindEvent topic node keyed by the receiver scope and
// the event-name string literal. Those nodes flow through the existing
// ResolveEventChannelCalls resolver (which now recognises the
// event::emitter:: prefix and applies a tighter per-literal fan-out cap),
// so no new resolver is needed.
//
// jsEmitterTransport must match resolver.emitterLiteralTransport — the two
// packages agree on the "emitter" label by value (the languages package
// does not import resolver).
const jsEmitterTransport = "emitter"

// emitterSubscribeMethods is the EventEmitter subscribe family. These are
// the weak EventEmitter entries of pubsubMethods, recognised here without
// the import gate so a custom emitter still pairs.
var emitterSubscribeMethods = map[string]struct{}{
	"on":                  {},
	"once":                {},
	"addListener":         {},
	"prependListener":     {},
	"prependOnceListener": {},
}

// emitterPublishMethod is the EventEmitter publish call.
const emitterPublishMethod = "emit"

// jsEmitterEvent is one resolved emitter-literal call site the
// import-gated pub/sub path did not classify.
type jsEmitterEvent struct {
	publish bool   // emit -> true; on/once/addListener -> false
	recv    string // receiver text — the topic-node scope
	topic   string // the event-name string literal
	method  string // the matched call method name
	line    int    // 1-based line of the call expression
	handler string // named handler identifier on a subscribe site ("" = inline/anonymous)
}

// detectJSEmitterLiteralCall recognises a bare EventEmitter call
// (recv.on('event', handler) / recv.emit('event')) the import-gated
// pub/sub path (detectJSPubsubCall) did not classify. Callers invoke it
// only when detectJSPubsubCall returned ok=false, so the two paths never
// double-emit. ok is false for a non-emitter method, an empty receiver,
// or a missing event-name literal.
func detectJSEmitterLiteralCall(callExpr *sitter.Node, method, receiver string, src []byte, line int) (jsEmitterEvent, bool) {
	if callExpr == nil {
		return jsEmitterEvent{}, false
	}
	recv := emitterReceiverScope(receiver)
	if recv == "" {
		return jsEmitterEvent{}, false
	}
	_, isSub := emitterSubscribeMethods[method]
	isPub := method == emitterPublishMethod
	if !isSub && !isPub {
		return jsEmitterEvent{}, false
	}
	topic := emitterEventLiteral(callExpr, src)
	if topic == "" {
		return jsEmitterEvent{}, false
	}
	ev := jsEmitterEvent{publish: isPub, recv: recv, topic: topic, method: method, line: line}
	if isSub {
		ev.handler = emitterHandlerName(callExpr, src)
	}
	return ev, true
}

// emitterReceiverScope normalises a member-call receiver text into a
// single clean node-ID segment. An empty receiver (a bare function call,
// not a member call) yields "" so it is skipped — there is no scope to
// correlate the literal against.
func emitterReceiverScope(receiver string) string {
	r := strings.TrimSpace(receiver)
	if r == "" {
		return ""
	}
	r = strings.Join(strings.Fields(r), " ")
	// The :: delimiter is reserved for the node-ID structure.
	r = strings.ReplaceAll(r, "::", ".")
	return r
}

// emitterEventLiteral returns the event-name string literal — the first
// positional argument when it is a plain string. A non-string first
// argument (template literal, computed name) yields "" because there is
// no stable literal to key the topic node on.
func emitterEventLiteral(callExpr *sitter.Node, src []byte) string {
	args := callExpr.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return ""
	}
	first := args.NamedChild(0)
	if first == nil || first.Type() != "string" {
		return ""
	}
	return jsStringLiteralContent(first, src)
}

// emitterHandlerName returns the handler identifier of a subscribe call's
// second positional argument when it is a bare identifier
// (`emitter.on('e', onReady)`). An inline function / arrow / member
// expression yields "" — there is no named node to attribute the listen
// edge to, so the caller falls back to the enclosing function.
func emitterHandlerName(callExpr *sitter.Node, src []byte) string {
	args := callExpr.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() < 2 {
		return ""
	}
	h := args.NamedChild(1)
	if h == nil || h.Type() != "identifier" {
		return ""
	}
	return strings.TrimSpace(h.Content(src))
}

// emitterEventNodeID is the canonical ID for an emitter-literal topic
// node. It is scoped by the receiver binding so recv.emit('e') and
// recv.on('e') across different files pair on the same node, while a
// different receiver's 'e' stays a distinct node — the analog of the
// tight per-literal cap, receiver-scoped rather than file-scoped so
// cross-file pairing still holds.
func emitterEventNodeID(recv, topic string) string {
	return "event::emitter::" + recv + "::" + topic
}

// emitJSEmitterLiteralEvents materialises one KindEvent topic node per
// distinct (recv, topic) emitter-literal pair, an EdgeEmits from each emit
// site's enclosing function, and an EdgeListensOn whose From is the named
// handler (so the resolver's synthesized call edge lands on the handler,
// not the .on call's enclosing function), falling back to the enclosing
// function for an inline/anonymous handler. callerLookup maps a 1-based
// line to its enclosing function ID; sites with no enclosing function are
// skipped.
func emitJSEmitterLiteralEvents(events []jsEmitterEvent, callerLookup func(line int) string, filePath, language string, result *parser.ExtractionResult) {
	if len(events) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(events))
	for _, e := range events {
		var from string
		kind := graph.EdgeEmits
		if e.publish {
			from = callerLookup(e.line)
		} else {
			kind = graph.EdgeListensOn
			if e.handler != "" {
				from = filePath + "::" + e.handler
			} else {
				from = callerLookup(e.line)
			}
		}
		if from == "" {
			continue
		}
		nodeID := emitterEventNodeID(e.recv, e.topic)
		if _, ok := seen[nodeID]; !ok {
			seen[nodeID] = struct{}{}
			result.Nodes = append(result.Nodes, &graph.Node{
				ID:       nodeID,
				Kind:     graph.KindEvent,
				Name:     e.topic,
				FilePath: filePath, // first sighting; not authoritative
				Language: language,
				Meta: map[string]any{
					"event_kind": "emitter",
					"transport":  jsEmitterTransport,
					"name":       e.topic,
					"receiver":   e.recv,
				},
			})
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From:     from,
			To:       nodeID,
			Kind:     kind,
			FilePath: filePath,
			Line:     e.line,
			Origin:   graph.OriginASTInferred,
			Meta: map[string]any{
				"method":    e.method,
				"transport": jsEmitterTransport,
			},
		})
	}
}
