package entrypoints

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestDetect_Alembic(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "alembic/versions/abc.py", Kind: graph.KindFile, FilePath: "alembic/versions/abc.py"},
		{ID: "f::upgrade", Kind: graph.KindFunction, Name: "upgrade"},
		{ID: "f::downgrade", Kind: graph.KindFunction, Name: "downgrade"},
		{ID: "f::revision", Kind: graph.KindVariable, Name: "revision"},
	}
	require.Equal(t, 3, Detect("alembic/versions/abc.py", "python", nodes))
	require.Equal(t, true, nodes[0].Meta[MetaEntryPoint])
	require.Equal(t, "alembic:migration", nodes[0].Meta[MetaEntryKind])
	require.Equal(t, true, nodes[1].Meta[MetaEntryPoint]) // upgrade
	require.Equal(t, true, nodes[2].Meta[MetaEntryPoint]) // downgrade
	require.Nil(t, nodes[3].Meta)                         // revision var not stamped
}

func TestDetect_AlembicRequiresFullSignature(t *testing.T) {
	// upgrade() alone — no downgrade, no revision — is not Alembic.
	nodes := []*graph.Node{
		{ID: "f.py", Kind: graph.KindFile, FilePath: "f.py"},
		{ID: "f::upgrade", Kind: graph.KindFunction, Name: "upgrade"},
	}
	require.Equal(t, 0, Detect("f.py", "python", nodes))
}

func TestDetect_NextJSPagesRouter(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "pages/users.tsx", Kind: graph.KindFile, FilePath: "pages/users.tsx"},
		{ID: "f::getServerSideProps", Kind: graph.KindFunction, Name: "getServerSideProps"},
	}
	require.Equal(t, 2, Detect("pages/users.tsx", "typescript", nodes))
	require.Equal(t, "nextjs:page", nodes[0].Meta[MetaEntryKind])
	require.Equal(t, true, nodes[1].Meta[MetaEntryPoint])
}

func TestDetect_NextJSAppRouter(t *testing.T) {
	page := []*graph.Node{{ID: "src/app/dashboard/page.tsx", Kind: graph.KindFile, FilePath: "src/app/dashboard/page.tsx"}}
	require.Equal(t, 1, Detect("src/app/dashboard/page.tsx", "typescript", page))
	require.Equal(t, "nextjs:page", page[0].Meta[MetaEntryKind])

	route := []*graph.Node{{ID: "app/api/users/route.ts", Kind: graph.KindFile, FilePath: "app/api/users/route.ts"}}
	require.Equal(t, 1, Detect("app/api/users/route.ts", "typescript", route))
	require.Equal(t, "nextjs:route", route[0].Meta[MetaEntryKind])
}

func TestDetect_NextJSGenericAppDirIgnored(t *testing.T) {
	// A non-special file under app/ must NOT be flagged Next.js.
	nodes := []*graph.Node{{ID: "app/helpers.ts", Kind: graph.KindFile, FilePath: "app/helpers.ts"}}
	require.Equal(t, 0, Detect("app/helpers.ts", "typescript", nodes))
}

func TestDetect_ASPNetHost(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "Program.cs", Kind: graph.KindFile, FilePath: "Program.cs"},
		{ID: "p::Main", Kind: graph.KindMethod, Name: "Main"},
		{ID: "p::Helper", Kind: graph.KindMethod, Name: "Helper"},
	}
	require.Equal(t, 2, Detect("src/Program.cs", "csharp", nodes)) // file + Main
	require.Equal(t, "aspnet:host", nodes[0].Meta[MetaEntryKind])
	require.Equal(t, true, nodes[1].Meta[MetaEntryPoint])
	require.Nil(t, nodes[2].Meta) // Helper is not a lifecycle method
}

func TestDetect_NonEntryFileIgnored(t *testing.T) {
	nodes := []*graph.Node{{ID: "src/util.go", Kind: graph.KindFile, FilePath: "src/util.go"}}
	require.Equal(t, 0, Detect("src/util.go", "go", nodes))
}
