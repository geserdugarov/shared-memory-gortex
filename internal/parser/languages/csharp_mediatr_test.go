package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func mediatrHandlerNode(nodes []*graph.Node, id string) *graph.Node {
	for _, n := range nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func mediatrPlaceholder(edges []*graph.Edge, reqType, kind string) *graph.Edge {
	for _, e := range edges {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "mediatr-dispatch" {
			continue
		}
		rt, _ := e.Meta["mediatr_request_type"].(string)
		k, _ := e.Meta["mediatr_kind"].(string)
		if rt == reqType && k == kind {
			return e
		}
	}
	return nil
}

func TestMediatR_TagsHandlersAndDispatches(t *testing.T) {
	src := `using MediatR;
public class CreateOrderHandler : IRequestHandler<CreateOrder, int> {
  public Task<int> Handle(CreateOrder r, CancellationToken ct) { return Task.FromResult(0); }
}
public class EmailHandler : INotificationHandler<OrderPlaced> {
  public Task Handle(OrderPlaced n, CancellationToken ct) { return Task.CompletedTask; }
}
public class Controller {
  private IMediator _mediator;
  public async Task Place() {
    await _mediator.Send(new CreateOrder(1));
    await _mediator.Publish(new OrderPlaced(1));
  }
}
`
	res, _, err := NewCSharpExtractor().extractCSharp("App.cs", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	reqH := mediatrHandlerNode(res.Nodes, "App.cs::CreateOrderHandler.Handle")
	if reqH == nil || reqH.Meta["mediatr_request_type"] != "CreateOrder" || reqH.Meta["mediatr_kind"] != "request" {
		t.Fatalf("IRequestHandler not tagged correctly: %+v", reqH)
	}
	notifH := mediatrHandlerNode(res.Nodes, "App.cs::EmailHandler.Handle")
	if notifH == nil || notifH.Meta["mediatr_kind"] != "notification" {
		t.Fatalf("INotificationHandler not tagged correctly: %+v", notifH)
	}

	send := mediatrPlaceholder(res.Edges, "CreateOrder", "request")
	if send == nil {
		t.Fatalf("no Send placeholder")
	}
	if send.From != "App.cs::Controller.Place" {
		t.Errorf("Send placeholder From = %q", send.From)
	}
	if mediatrPlaceholder(res.Edges, "OrderPlaced", "notification") == nil {
		t.Errorf("no Publish placeholder")
	}
}

func TestMediatR_NonHandlerSendIgnored(t *testing.T) {
	// A `.Send` on a non-MediatR object with no registered handler must not
	// tag any handler; the placeholder (if any) simply never resolves.
	src := `public class Wire {
  private Socket _sock;
  public void Go() { _sock.Send(buffer); }
}
`
	res, _, err := NewCSharpExtractor().extractCSharp("Wire.cs", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// `_sock.Send(buffer)` has no `new X()` arg → no placeholder.
	if mediatrPlaceholder(res.Edges, "buffer", "request") != nil {
		t.Errorf("a Send with a non-constructor arg must not produce a placeholder")
	}
}
