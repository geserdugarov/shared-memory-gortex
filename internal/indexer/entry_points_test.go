package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestIndex_AlembicEntryPoint indexes a real Alembic migration and
// verifies its upgrade() is flagged as an entry point and therefore
// not reported as dead code.
func TestIndex_AlembicEntryPoint(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "alembic", "versions"), 0o755))
	writeFile(t, filepath.Join(dir, "alembic", "versions", "001_init.py"), `revision = "001"
down_revision = None


def upgrade():
    pass


def downgrade():
    pass
`)
	g := graph.New()
	reg := parser.NewRegistry()
	reg.Register(languages.NewPythonExtractor())
	idx := New(g, reg, config.Default().Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)

	var upgrade *graph.Node
	for _, n := range g.AllNodes() {
		if n.Name == "upgrade" && n.Kind == graph.KindFunction {
			upgrade = n
		}
	}
	require.NotNil(t, upgrade, "upgrade() must be indexed")
	require.Equal(t, true, upgrade.Meta["entry_point"])
	require.Equal(t, "alembic:migration", upgrade.Meta["entry_point_kind"])

	// The migration's upgrade() has no in-app caller — without the
	// entry-point tag it would be a dead-code false positive.
	for _, d := range analysis.FindDeadCode(g, nil, nil) {
		require.NotEqual(t, "upgrade", d.Name, "Alembic upgrade() must not be flagged dead")
		require.NotEqual(t, "downgrade", d.Name, "Alembic downgrade() must not be flagged dead")
	}
}
