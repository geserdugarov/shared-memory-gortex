package contracts

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestDjango_RouteShapes(t *testing.T) {
	src := []byte(`from django.urls import path, re_path, include
from . import views
from .views import ArticleDetail

urlpatterns = [
    path('articles/', views.article_list),
    path('articles/<int:year>/', views.year_archive, name='year-archive'),
    re_path(r'^authors/(?P<author_id>[0-9]+)/$', views.author),
    path('detail/<int:pk>/', ArticleDetail.as_view()),
    path('blog/', include('blog.urls')),
]
`)
	nodes := []*graph.Node{
		flaskNode("app.py::article_list", "article_list", graph.KindFunction, 6),
		flaskNode("app.py::year_archive", "year_archive", graph.KindFunction, 7),
		flaskNode("app.py::author", "author", graph.KindFunction, 8),
		flaskNode("app.py::ArticleDetail", "ArticleDetail", graph.KindType, 9),
	}
	cs := (&HTTPExtractor{}).Extract("app.py", src, nodes, nil)

	// path() with a function handler.
	list := flaskFind(cs, "ANY", "/articles")
	if list == nil {
		t.Fatalf("expected ANY /articles, got %+v", contractPaths(cs))
	}
	if list.Meta["framework"] != "django" {
		t.Errorf("framework = %v, want django", list.Meta["framework"])
	}
	if list.SymbolID != "app.py::article_list" {
		t.Errorf("article_list SymbolID = %q", list.SymbolID)
	}

	// path() converter <int:year> normalises to a positional param.
	yr := flaskFind(cs, "ANY", "/articles/{p1}")
	if yr == nil {
		t.Fatalf("expected ANY /articles/{p1}, got %+v", contractPaths(cs))
	}
	if yr.SymbolID != "app.py::year_archive" {
		t.Errorf("year_archive SymbolID = %q", yr.SymbolID)
	}
	if names, _ := yr.Meta["path_param_names"].([]string); len(names) != 1 || names[0] != "year" {
		t.Errorf("path_param_names = %v, want [year]", yr.Meta["path_param_names"])
	}

	// re_path() named group normalises like a path converter.
	author := flaskFind(cs, "ANY", "/authors/{p1}")
	if author == nil {
		t.Fatalf("expected ANY /authors/{p1} from re_path, got %+v", contractPaths(cs))
	}
	if author.Meta["route_shape"] != "re_path" {
		t.Errorf("route_shape = %v, want re_path", author.Meta["route_shape"])
	}
	if author.SymbolID != "app.py::author" {
		t.Errorf("author SymbolID = %q", author.SymbolID)
	}

	// Class-based view .as_view() resolves to the view class node.
	detail := flaskFind(cs, "ANY", "/detail/{p1}")
	if detail == nil {
		t.Fatalf("expected ANY /detail/{p1}, got %+v", contractPaths(cs))
	}
	if detail.SymbolID != "app.py::ArticleDetail" {
		t.Errorf("as_view SymbolID = %q, want the view class node", detail.SymbolID)
	}

	// include() records a sub-URLconf mount.
	mount := flaskFind(cs, "MOUNT", "/blog")
	if mount == nil {
		t.Fatalf("expected MOUNT /blog from include(), got %+v", contractPaths(cs))
	}
	if mount.Meta["django_include"] != "blog.urls" {
		t.Errorf("django_include = %v, want blog.urls", mount.Meta["django_include"])
	}
	if mount.Meta["mount"] != true {
		t.Errorf("mount flag = %v, want true", mount.Meta["mount"])
	}
}
