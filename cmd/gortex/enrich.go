package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/blame"
	"github.com/zzet/gortex/internal/cochange"
	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/coverage"
	"github.com/zzet/gortex/internal/daemon"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/progress"
	"github.com/zzet/gortex/internal/releases"
)

var enrichCmd = &cobra.Command{
	Use:   "enrich",
	Short: "Run one-shot enrichments (blame, coverage) against an indexed repo",
	Long: `Enrich indexes a repository in-process and stamps additional metadata
onto graph nodes from external data sources — git blame for authorship,
Go cover profiles for test coverage. Useful for CI pipelines or one-off
snapshots where the daemon isn't running. Equivalent to invoking the
` + "`analyze kind=blame`" + ` / ` + "`analyze kind=coverage`" + ` MCP tools against a fresh
index.`,
}

var (
	enrichBlameSnapshot    string
	enrichCoverageSnapshot string
	enrichReleasesSnapshot string
	enrichReleasesBranch   string
	enrichCochangeSnapshot string

	enrichAllSnapshot string
	enrichAllBlame    bool
	enrichAllReleases bool
	enrichAllCochange bool
	enrichAllProfile  string
)

var enrichBlameCmd = &cobra.Command{
	Use:   "blame [path]",
	Short: "Stamp meta.last_authored on every symbol via git blame",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runEnrichBlame,
}

var enrichCoverageCmd = &cobra.Command{
	Use:   "coverage <profile> [path]",
	Short: "Stamp meta.coverage_pct on every symbol from a Go cover profile",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runEnrichCoverage,
}

var enrichReleasesCmd = &cobra.Command{
	Use:   "releases [path]",
	Short: "Stamp meta.added_in on every file from git tag history",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runEnrichReleases,
}

var enrichCochangeCmd = &cobra.Command{
	Use:   "cochange [path]",
	Short: "Add co_change edges between files that git history shows change together",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runEnrichCochange,
}

var enrichAllCmd = &cobra.Command{
	Use:   "all [path]",
	Short: "Index once and run multiple enrichments in a single pass",
	Long: `Combined enrichment that indexes the target path once, then runs
the requested enrichments against the same in-memory graph. Avoids
the ~3x indexing cost of running blame, coverage, and releases as
three separate subcommand invocations.

By default runs blame and releases (both git-only, no extra data
needed). Pass --coverage <profile> to also run coverage enrichment.
Each enrichment is independently optional via --no-blame /
--no-releases flags should you want a subset.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runEnrichAll,
}

func init() {
	enrichBlameCmd.Flags().StringVar(&enrichBlameSnapshot, "snapshot", "",
		"write the enriched graph as a gob.gz snapshot to this path")
	enrichCoverageCmd.Flags().StringVar(&enrichCoverageSnapshot, "snapshot", "",
		"write the enriched graph as a gob.gz snapshot to this path")
	enrichReleasesCmd.Flags().StringVar(&enrichReleasesSnapshot, "snapshot", "",
		"write the enriched graph as a gob.gz snapshot to this path")
	enrichReleasesCmd.Flags().StringVar(&enrichReleasesBranch, "branch", "",
		"restrict to tags reachable from this branch (default: resolve origin/main/master). Empty means every tag in the repo")
	enrichCochangeCmd.Flags().StringVar(&enrichCochangeSnapshot, "snapshot", "",
		"write the enriched graph as a gob.gz snapshot to this path")
	enrichAllCmd.Flags().StringVar(&enrichAllSnapshot, "snapshot", "",
		"write the enriched graph as a gob.gz snapshot to this path")
	enrichAllCmd.Flags().BoolVar(&enrichAllBlame, "blame", true,
		"run blame enrichment (default: on)")
	enrichAllCmd.Flags().BoolVar(&enrichAllReleases, "releases", true,
		"run releases enrichment (default: on)")
	enrichAllCmd.Flags().BoolVar(&enrichAllCochange, "cochange", true,
		"run co-change enrichment (default: on)")
	enrichAllCmd.Flags().StringVar(&enrichAllProfile, "coverage", "",
		"path to a Go cover.out profile — coverage enrichment is skipped when empty")
	enrichCmd.AddCommand(enrichBlameCmd)
	enrichCmd.AddCommand(enrichCoverageCmd)
	enrichCmd.AddCommand(enrichReleasesCmd)
	enrichCmd.AddCommand(enrichCochangeCmd)
	enrichCmd.AddCommand(enrichAllCmd)
	rootCmd.AddCommand(enrichCmd)
}

func runEnrichAll(cmd *cobra.Command, args []string) error {
	logger := newLogger()
	defer func() { _ = logger.Sync() }()

	path := "."
	if len(args) >= 1 {
		path = args[0]
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, cfg.Index, loggerForSpinner(cmd, logger))

	if err := indexWithSpinner(cmd, idx, path); err != nil {
		return err
	}

	result := map[string]any{
		"root": idx.RootPath(),
	}

	if enrichAllBlame {
		sp := newCLISpinner(cmd, "Stamping blame")
		count, err := blame.EnrichGraph(g, idx.RootPath())
		if err != nil {
			sp.Fail(err)
			return fmt.Errorf("blame: %w", err)
		}
		sp.Set("", fmt.Sprintf("%d nodes stamped", count))
		sp.Done()
		result["blame_enriched"] = count
	}
	if enrichAllReleases {
		sp := newCLISpinner(cmd, "Stamping releases")
		count, err := releases.EnrichGraph(g, idx.RootPath())
		if err != nil {
			sp.Fail(err)
			return fmt.Errorf("releases: %w", err)
		}
		sp.Set("", fmt.Sprintf("%d files stamped", count))
		sp.Done()
		result["releases_enriched"] = count
	}
	if enrichAllCochange {
		sp := newCLISpinner(cmd, "Mining co-change")
		count, err := cochange.EnrichGraph(g, idx.RootPath(), "")
		if err != nil {
			sp.Fail(err)
			return fmt.Errorf("cochange: %w", err)
		}
		sp.Set("", fmt.Sprintf("%d edges added", count))
		sp.Done()
		result["cochange_edges"] = count
	}
	if enrichAllProfile != "" {
		sp := newCLISpinner(cmd, "Stamping coverage")
		sp.Set("", enrichAllProfile)
		segments, err := coverage.ParseFile(enrichAllProfile)
		if err != nil {
			sp.Fail(err)
			return fmt.Errorf("read profile: %w", err)
		}
		modulePath := coverage.ReadModulePath(idx.RootPath())
		count := coverage.EnrichGraph(g, segments, modulePath)
		sp.Set("", fmt.Sprintf("%d symbols · %d segments", count, len(segments)))
		sp.Done()
		result["coverage_enriched"] = count
		result["coverage_segments"] = len(segments)
	}

	if enrichAllSnapshot != "" {
		if err := saveSnapshotTo(g, nil, nil, snapshotVector{}, "gortex-enrich-all", enrichAllSnapshot, logger); err != nil {
			return fmt.Errorf("write snapshot %s: %w", enrichAllSnapshot, err)
		}
		result["snapshot"] = enrichAllSnapshot
	}
	return printEnrichResult(result)
}

func runEnrichReleases(cmd *cobra.Command, args []string) error {
	logger := newLogger()
	defer func() { _ = logger.Sync() }()

	path := "."
	if len(args) >= 1 {
		path = args[0]
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("abs path %q: %w", path, err)
	}

	// Daemon path: forward to the running daemon so the enrichment
	// runs against its in-process (and possibly disk-backed)
	// graph. Mirrors the churn CLI's behaviour.
	if daemon.IsRunning() {
		return forwardEnrichReleasesToDaemon(cmd, abs)
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, cfg.Index, loggerForSpinner(cmd, logger))

	if err := indexWithSpinner(cmd, idx, path); err != nil {
		return err
	}

	branch := enrichReleasesBranch
	if branch == "" {
		branch = gitDefaultBranch(idx.RootPath())
	}

	sp := newCLISpinner(cmd, "Stamping releases")
	if branch != "" {
		sp.Set("", branch)
	}
	count, err := releases.EnrichGraphForBranch(g, idx.RootPath(), "", branch)
	if err != nil {
		sp.Fail(err)
		return fmt.Errorf("releases: %w", err)
	}
	sp.Set("", fmt.Sprintf("%d files stamped", count))
	sp.Done()

	result := map[string]any{
		"enriched": count,
		"branch":   branch,
		"root":     idx.RootPath(),
		"mode":     "standalone",
	}
	if enrichReleasesSnapshot != "" {
		if err := saveSnapshotTo(g, nil, nil, snapshotVector{}, "gortex-enrich-releases", enrichReleasesSnapshot, logger); err != nil {
			return fmt.Errorf("write snapshot %s: %w", enrichReleasesSnapshot, err)
		}
		result["snapshot"] = enrichReleasesSnapshot
	}
	return printEnrichResult(result)
}

// forwardEnrichReleasesToDaemon sends a ControlEnrichReleases RPC
// and renders the response. Same shape as forwardEnrichChurnToDaemon.
func forwardEnrichReleasesToDaemon(cmd *cobra.Command, absPath string) error {
	c, err := daemon.Dial(daemon.Handshake{Mode: daemon.ModeControl, ClientName: "cli-enrich-releases"})
	if err != nil {
		if errors.Is(err, daemon.ErrDaemonUnavailable) {
			return fmt.Errorf("daemon socket detected but dial failed; restart the daemon or run with no daemon (it falls back to in-memory)")
		}
		return fmt.Errorf("dial daemon: %w", err)
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Control(daemon.ControlEnrichReleases, daemon.EnrichReleasesParams{
		Path:   absPath,
		Branch: enrichReleasesBranch,
	})
	if err != nil {
		return fmt.Errorf("control enrich_releases: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("daemon rejected enrich_releases [%s]: %s", resp.ErrorCode, resp.ErrorMsg)
	}
	var out daemon.EnrichReleasesResult
	if len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, &out); err != nil {
			return fmt.Errorf("parse daemon response: %w", err)
		}
	}
	sp := newCLISpinner(cmd, "Enriched via daemon")
	sp.Set("", fmt.Sprintf("%d files · %s", out.Files, out.Branch))
	sp.Done()
	payload := map[string]any{
		"enriched":    out.Files,
		"branch":      out.Branch,
		"duration_ms": out.DurationMS,
		"mode":        "daemon",
	}
	if absPath != "" {
		payload["path"] = absPath
	}
	return printEnrichResult(payload)
}

func runEnrichCochange(cmd *cobra.Command, args []string) error {
	logger := newLogger()
	defer func() { _ = logger.Sync() }()

	path := "."
	if len(args) >= 1 {
		path = args[0]
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, cfg.Index, loggerForSpinner(cmd, logger))

	if err := indexWithSpinner(cmd, idx, path); err != nil {
		return err
	}

	sp := newCLISpinner(cmd, "Mining co-change")
	count, err := cochange.EnrichGraph(g, idx.RootPath(), "")
	if err != nil {
		sp.Fail(err)
		return fmt.Errorf("cochange: %w", err)
	}
	sp.Set("", fmt.Sprintf("%d edges added", count))
	sp.Done()

	result := map[string]any{
		"enriched": count,
		"root":     idx.RootPath(),
	}
	if enrichCochangeSnapshot != "" {
		if err := saveSnapshotTo(g, nil, nil, snapshotVector{}, "gortex-enrich-cochange", enrichCochangeSnapshot, logger); err != nil {
			return fmt.Errorf("write snapshot %s: %w", enrichCochangeSnapshot, err)
		}
		result["snapshot"] = enrichCochangeSnapshot
	}
	return printEnrichResult(result)
}

func runEnrichBlame(cmd *cobra.Command, args []string) error {
	logger := newLogger()
	defer func() { _ = logger.Sync() }()

	path := "."
	if len(args) >= 1 {
		path = args[0]
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, cfg.Index, loggerForSpinner(cmd, logger))

	if err := indexWithSpinner(cmd, idx, path); err != nil {
		return err
	}

	sp := newCLISpinner(cmd, "Stamping blame")
	count, err := blame.EnrichGraph(g, idx.RootPath())
	if err != nil {
		sp.Fail(err)
		return fmt.Errorf("blame: %w", err)
	}
	sp.Set("", fmt.Sprintf("%d nodes stamped", count))
	sp.Done()

	result := map[string]any{
		"enriched": count,
		"root":     idx.RootPath(),
	}
	if enrichBlameSnapshot != "" {
		if err := saveSnapshotTo(g, nil, nil, snapshotVector{}, "gortex-enrich-blame", enrichBlameSnapshot, logger); err != nil {
			return fmt.Errorf("write snapshot %s: %w", enrichBlameSnapshot, err)
		}
		result["snapshot"] = enrichBlameSnapshot
	}
	return printEnrichResult(result)
}

func runEnrichCoverage(cmd *cobra.Command, args []string) error {
	logger := newLogger()
	defer func() { _ = logger.Sync() }()

	profilePath := args[0]
	path := "."
	if len(args) >= 2 {
		path = args[1]
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, cfg.Index, loggerForSpinner(cmd, logger))

	if err := indexWithSpinner(cmd, idx, path); err != nil {
		return err
	}

	sp := newCLISpinner(cmd, "Stamping coverage")
	sp.Set("", profilePath)
	segments, err := coverage.ParseFile(profilePath)
	if err != nil {
		sp.Fail(err)
		return fmt.Errorf("read profile: %w", err)
	}
	modulePath := coverage.ReadModulePath(idx.RootPath())
	count := coverage.EnrichGraph(g, segments, modulePath)
	sp.Set("", fmt.Sprintf("%d symbols · %d segments", count, len(segments)))
	sp.Done()

	result := map[string]any{
		"enriched":    count,
		"segments":    len(segments),
		"profile":     profilePath,
		"module_path": modulePath,
		"root":        idx.RootPath(),
	}
	if enrichCoverageSnapshot != "" {
		if err := saveSnapshotTo(g, nil, nil, snapshotVector{}, "gortex-enrich-coverage", enrichCoverageSnapshot, logger); err != nil {
			return fmt.Errorf("write snapshot %s: %w", enrichCoverageSnapshot, err)
		}
		result["snapshot"] = enrichCoverageSnapshot
	}
	return printEnrichResult(result)
}

// printEnrichResult emits the enrichment summary as JSON when stdout
// is captured by a script and as a one-line human-readable text
// when invoked interactively. On a terminal we keep stdout quiet — the
// spinner already showed the per-pass count — and just caption the root /
// snapshot path. On a pipe / redirect we still emit JSON for scripts.
func printEnrichResult(payload map[string]any) error {
	if progress.IsTTY(os.Stdout) {
		if v, ok := payload["root"]; ok {
			_, _ = fmt.Fprintln(os.Stdout, "  "+progress.Caption("root: "+fmt.Sprint(v)))
		}
		if v, ok := payload["snapshot"]; ok {
			_, _ = fmt.Fprintln(os.Stdout, "  "+progress.Caption("snapshot: "+fmt.Sprint(v)))
		}
		if v, ok := payload["profile"]; ok {
			_, _ = fmt.Fprintln(os.Stdout, "  "+progress.Caption("profile: "+fmt.Sprint(v)))
		}
		return nil
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
