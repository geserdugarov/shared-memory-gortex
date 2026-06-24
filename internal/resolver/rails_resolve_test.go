package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func railsType(g *graph.Graph, id, file, name string, kind graph.NodeKind) {
	g.AddNode(&graph.Node{ID: id, Kind: kind, Name: name, FilePath: file, Language: "ruby"})
}

func railsMethod(g *graph.Graph, classID, methodID, file, name string) {
	g.AddNode(&graph.Node{ID: methodID, Kind: graph.KindMethod, Name: name, FilePath: file, Language: "ruby"})
	g.AddEdge(&graph.Edge{From: methodID, To: classID, Kind: graph.EdgeMemberOf})
}

func railsRecvCall(g *graph.Graph, from, file, method, recv string) {
	g.AddEdge(&graph.Edge{
		From: from, To: "unresolved::*." + method, Kind: graph.EdgeCalls, FilePath: file,
		Meta: map[string]any{"recv_const": recv},
	})
}

func synthRailsEdge(g graph.Store, from, to string) *graph.Edge {
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.From != from || e.To != to || e.Meta == nil {
			continue
		}
		if by, _ := e.Meta[MetaSynthesizedBy].(string); by == SynthRailsResolve {
			return e
		}
	}
	return nil
}

func TestResolveRailsRefs_ServiceModelHelper(t *testing.T) {
	g := graph.New()
	const ctrl = "app/controllers/users_controller.rb::UsersController.index"
	railsType(g, "app/controllers/users_controller.rb::UsersController", "app/controllers/users_controller.rb", "UsersController", graph.KindType)
	g.AddNode(&graph.Node{ID: ctrl, Kind: graph.KindMethod, Name: "index", FilePath: "app/controllers/users_controller.rb"})

	// Service class + its perform method.
	railsType(g, "app/services/user_service.rb::UserService", "app/services/user_service.rb", "UserService", graph.KindType)
	railsMethod(g, "app/services/user_service.rb::UserService", "app/services/user_service.rb::UserService.perform", "app/services/user_service.rb", "perform")
	// ActiveRecord model (EdgeModelsTable marks it).
	railsType(g, "app/models/user.rb::User", "app/models/user.rb", "User", graph.KindType)
	g.AddEdge(&graph.Edge{From: "app/models/user.rb::User", To: "table::users", Kind: graph.EdgeModelsTable})
	// Helper module (KindPackage) + a method on it.
	railsType(g, "app/helpers/application_helper.rb::ApplicationHelper", "app/helpers/application_helper.rb", "ApplicationHelper", graph.KindPackage)
	railsMethod(g, "app/helpers/application_helper.rb::ApplicationHelper", "app/helpers/application_helper.rb::ApplicationHelper.fmt", "app/helpers/application_helper.rb", "fmt")

	railsRecvCall(g, ctrl, "app/controllers/users_controller.rb", "perform", "UserService")
	railsRecvCall(g, ctrl, "app/controllers/users_controller.rb", "find", "User")
	railsRecvCall(g, ctrl, "app/controllers/users_controller.rb", "fmt", "ApplicationHelper")

	require.Equal(t, 3, ResolveRailsRefs(g))

	// Service: binds to the perform method on the dir-located class.
	assert.NotNil(t, synthRailsEdge(g, ctrl, "app/services/user_service.rb::UserService.perform"),
		"UserService.perform binds to /app/services/")
	// Model: `find` is inherited, so it binds to the model class itself.
	assert.NotNil(t, synthRailsEdge(g, ctrl, "app/models/user.rb::User"),
		"User.find binds to the /app/models/ class")
	// Helper module method.
	assert.NotNil(t, synthRailsEdge(g, ctrl, "app/helpers/application_helper.rb::ApplicationHelper.fmt"),
		"ApplicationHelper.fmt binds to /app/helpers/")
}

func TestResolveRailsRefs_AmbiguousServiceLeftAlone(t *testing.T) {
	g := graph.New()
	const ctrl = "app/controllers/x_controller.rb::XController.act"
	g.AddNode(&graph.Node{ID: ctrl, Kind: graph.KindMethod, Name: "act", FilePath: "app/controllers/x_controller.rb"})
	// Two UserService classes in two service dirs → ambiguous.
	railsType(g, "app/services/a/user_service.rb::UserService", "app/services/a/user_service.rb", "UserService", graph.KindType)
	railsType(g, "app/services/b/user_service.rb::UserService", "app/services/b/user_service.rb", "UserService", graph.KindType)
	railsRecvCall(g, ctrl, "app/controllers/x_controller.rb", "perform", "UserService")

	require.Equal(t, 0, ResolveRailsRefs(g))
}

func TestResolveRailsRefs_NonModelBareConstSkipped(t *testing.T) {
	g := graph.New()
	const ctrl = "app/controllers/x_controller.rb::XController.act"
	g.AddNode(&graph.Node{ID: ctrl, Kind: graph.KindMethod, Name: "act", FilePath: "app/controllers/x_controller.rb"})
	// A plain PascalCase constant resolvable by name but NOT an ActiveRecord
	// model (no EdgeModelsTable) — must be left unresolved.
	railsType(g, "lib/widget.rb::Widget", "lib/widget.rb", "Widget", graph.KindType)
	railsRecvCall(g, ctrl, "app/controllers/x_controller.rb", "build", "Widget")

	require.Equal(t, 0, ResolveRailsRefs(g))
	assert.Nil(t, synthRailsEdge(g, ctrl, "lib/widget.rb::Widget"))
}
