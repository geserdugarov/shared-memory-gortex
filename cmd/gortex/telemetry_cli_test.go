package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/persistence"
	"github.com/zzet/gortex/internal/telemetry"
)

func envOn(k string) string {
	if k == telemetry.EnvTelemetry {
		return "on"
	}
	return ""
}

func envOff(string) string { return "" }

func TestCLIRecorderDim(t *testing.T) {
	root := &cobra.Command{Use: "gortex"}
	review := &cobra.Command{Use: "review"}
	daemon := &cobra.Command{Use: "daemon"}
	start := &cobra.Command{Use: "start"}
	root.AddCommand(review)
	root.AddCommand(daemon)
	daemon.AddCommand(start)

	if got := cliCommandDim(review); got != "review" {
		t.Errorf("top-level dim = %q, want review", got)
	}
	if got := cliCommandDim(start); got != "daemon.start" {
		t.Errorf("nested dim = %q, want daemon.start", got)
	}
	if got := cliCommandDim(root); got != "" {
		t.Errorf("bare-root dim = %q, want empty", got)
	}
}

func TestTelemetryCLIRecordsCommand(t *testing.T) {
	store := telemetry.NewStore(t.TempDir())
	root := &cobra.Command{Use: "gortex"}
	sub := &cobra.Command{Use: "review"}
	root.AddCommand(sub)

	recordCLIUsage(sub, store, envOn)

	roll, err := store.Load(telemetry.DayKey(time.Now()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if roll.Counts["cli_command:review"] != 1 {
		t.Errorf("cli_command:review = %d, want 1 (counts=%v)", roll.Counts["cli_command:review"], roll.Counts)
	}
}

func TestTelemetryCLIDisabledRecordsNothing(t *testing.T) {
	store := telemetry.NewStore(t.TempDir())
	root := &cobra.Command{Use: "gortex"}
	sub := &cobra.Command{Use: "index"}
	root.AddCommand(sub)

	recordCLIUsage(sub, store, envOff) // consent default off

	if days, _ := store.Days(); len(days) != 0 {
		t.Errorf("disabled CLI telemetry wrote days %v", days)
	}
}

func TestTelemetryCLIBareRootRecordsNothing(t *testing.T) {
	store := telemetry.NewStore(t.TempDir())
	root := &cobra.Command{Use: "gortex"}

	recordCLIUsage(root, store, envOn) // enabled, but no subcommand ran

	if days, _ := store.Days(); len(days) != 0 {
		t.Errorf("bare-root invocation recorded a command: %v", days)
	}
}

// redirectCLIEventSidecar points the consent-free CLI-verb ledger at a temp
// path and returns it, so a write test never touches the real ~/.gortex.
func redirectCLIEventSidecar(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sidecar.sqlite")
	orig := cliEventSidecarPath
	cliEventSidecarPath = func() string { return path }
	t.Cleanup(func() { cliEventSidecarPath = orig })
	return path
}

// envWithSession returns a getenv that reports a fixed GORTEX_SESSION_ID and
// nothing else.
func envWithSession(id string) func(string) string {
	return func(k string) string {
		if k == "GORTEX_SESSION_ID" {
			return id
		}
		return ""
	}
}

// TestRecordCLIEventNoSessionIsNoOp: with GORTEX_SESSION_ID unset, the
// consent-free recorder returns early — it must NOT even create the sidecar
// file, so a bare interactive run pays zero cost.
func TestRecordCLIEventNoSessionIsNoOp(t *testing.T) {
	path := redirectCLIEventSidecar(t)
	root := &cobra.Command{Use: "gortex"}
	sub := &cobra.Command{Use: "review"}
	root.AddCommand(sub)

	recordCLIEvent(sub, envOff) // no GORTEX_SESSION_ID

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("recordCLIEvent created the sidecar with no session id (stat err=%v)", err)
	}
}

// TestRecordCLIEventWithSessionWritesRow: with a session id set the verb is
// recorded under it, readable back by session.
func TestRecordCLIEventWithSessionWritesRow(t *testing.T) {
	path := redirectCLIEventSidecar(t)
	root := &cobra.Command{Use: "gortex"}
	edit := &cobra.Command{Use: "edit"}
	verify := &cobra.Command{Use: "verify"}
	root.AddCommand(edit)
	edit.AddCommand(verify)

	recordCLIEvent(verify, envWithSession("sess-w"))

	sc, err := persistence.OpenSidecar(path)
	if err != nil {
		t.Fatalf("OpenSidecar: %v", err)
	}
	defer sc.Close()
	got, err := sc.CLIEventsBySession("sess-w")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Verb != "edit.verify" {
		t.Errorf("recorded events = %+v, want one edit.verify", got)
	}
}

// TestRecordCLIEventSkipsTelemetrySubcommand: even with a session id, the
// self-referential telemetry subcommand is not recorded.
func TestRecordCLIEventSkipsTelemetrySubcommand(t *testing.T) {
	path := redirectCLIEventSidecar(t)
	root := &cobra.Command{Use: "gortex"}
	tel := &cobra.Command{Use: "telemetry"}
	root.AddCommand(tel)

	recordCLIEvent(tel, envWithSession("sess-t"))

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		// The early return for the telemetry verb happens before OpenSidecar,
		// so no file should be created.
		t.Errorf("recordCLIEvent recorded the telemetry subcommand (stat err=%v)", err)
	}
}
