package main

import (
	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/hooks"
)

var hookPort int

var hookCmd = &cobra.Command{
	Use:    "hook",
	Short:  "Claude Code hook handler (called by PreToolUse hooks)",
	Hidden: true, // Not for direct user invocation.
	Run: func(_ *cobra.Command, _ []string) {
		hooks.RunPreToolUse(hookPort)
	},
}

func init() {
	hookCmd.Flags().IntVar(&hookPort, "port", 8765, "Gortex web server port")
	rootCmd.AddCommand(hookCmd)
}
