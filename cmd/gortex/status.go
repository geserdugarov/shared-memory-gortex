package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

var statusIndex string

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show index status: node/edge counts, languages, and file breakdown",
	RunE:  runStatus,
}

func init() {
	statusCmd.Flags().StringVar(&statusIndex, "index", ".", "repository path to index")
	rootCmd.AddCommand(statusCmd)
}

func runStatus(_ *cobra.Command, _ []string) error {
	logger := newLogger()
	defer logger.Sync()

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)

	idx := indexer.New(g, reg, cfg.Index, logger)
	result, err := idx.Index(statusIndex)
	if err != nil {
		return fmt.Errorf("indexing failed: %w", err)
	}

	stats := g.Stats()

	fmt.Fprintf(os.Stdout, "Repository:  %s\n", statusIndex)
	fmt.Fprintf(os.Stdout, "Files:       %d\n", result.FileCount)
	fmt.Fprintf(os.Stdout, "Nodes:       %d\n", stats.TotalNodes)
	fmt.Fprintf(os.Stdout, "Edges:       %d\n", stats.TotalEdges)
	fmt.Fprintf(os.Stdout, "Duration:    %dms\n\n", result.DurationMs)

	if len(stats.ByLanguage) > 0 {
		fmt.Fprintln(os.Stdout, "Languages:")
		for lang, count := range stats.ByLanguage {
			fmt.Fprintf(os.Stdout, "  %-14s %d nodes\n", lang, count)
		}
		fmt.Fprintln(os.Stdout)
	}

	if len(stats.ByKind) > 0 {
		fmt.Fprintln(os.Stdout, "By kind:")
		for kind, count := range stats.ByKind {
			fmt.Fprintf(os.Stdout, "  %-14s %d\n", kind, count)
		}
	}

	return nil
}
