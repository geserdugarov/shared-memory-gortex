package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func rubyRecvConst(edges []*graph.Edge, method string) (string, bool) {
	for _, e := range edges {
		if e.Kind != graph.EdgeCalls || e.To != "unresolved::*."+method {
			continue
		}
		if e.Meta == nil {
			return "", false
		}
		rc, ok := e.Meta["recv_const"].(string)
		return rc, ok
	}
	return "", false
}

func TestRubyRecvConst_StampedForConstantReceiver(t *testing.T) {
	src := `class UsersController < ApplicationController
  def index
    UserService.perform(params)
    @user = User.find(1)
    user.save
  end
end
`
	res, err := NewRubyExtractor().Extract("app/controllers/users_controller.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if rc, ok := rubyRecvConst(res.Edges, "perform"); !ok || rc != "UserService" {
		t.Errorf("UserService.perform recv_const = %q ok=%v (want UserService)", rc, ok)
	}
	if rc, ok := rubyRecvConst(res.Edges, "find"); !ok || rc != "User" {
		t.Errorf("User.find recv_const = %q ok=%v (want User)", rc, ok)
	}
	// A lowercase variable receiver (`user.save`) is not a constant — no stamp.
	if rc, ok := rubyRecvConst(res.Edges, "save"); ok {
		t.Errorf("user.save must not carry recv_const, got %q", rc)
	}
}

func TestRubyRecvConst_ScopedReceiverLeaf(t *testing.T) {
	src := `class X
  def go
    Admin::UserService.run
  end
end
`
	res, err := NewRubyExtractor().Extract("app/x.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if rc, ok := rubyRecvConst(res.Edges, "run"); !ok || rc != "UserService" {
		t.Errorf("Admin::UserService.run recv_const = %q ok=%v (want UserService)", rc, ok)
	}
}
