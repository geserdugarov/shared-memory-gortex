package persistence

import (
	"testing"
	"time"
)

// TestCLIEvents_RoundTrip books a few CLI-verb events under two sessions
// and proves both read paths: time-windowed (Since) and per-session
// (BySession). Events survive a close + reopen — durable at the call.
func TestCLIEvents_RoundTrip(t *testing.T) {
	sc, path := openTestSidecar(t)

	t0 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	events := []CLIEvent{
		{TS: t0, SessionID: "sess-1", Verb: "edit.verify"},
		{TS: t0.Add(time.Minute), SessionID: "sess-1", Verb: "edit.guards"},
		{TS: t0.Add(2 * time.Minute), SessionID: "sess-2", Verb: "query.stats"},
	}
	for _, ev := range events {
		if err := sc.AddCLIEvent(ev.TS, ev.SessionID, ev.Verb); err != nil {
			t.Fatalf("AddCLIEvent: %v", err)
		}
	}
	if err := sc.Close(); err != nil {
		t.Fatal(err)
	}

	sc2, err := OpenSidecar(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sc2.Close()

	// Since(zero) returns everything, oldest first.
	all, err := sc2.CLIEventsSince(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("CLIEventsSince(zero) = %d events, want 3", len(all))
	}
	if all[0].Verb != "edit.verify" || all[2].Verb != "query.stats" {
		t.Errorf("order = %q…%q, want edit.verify…query.stats", all[0].Verb, all[2].Verb)
	}
	if !all[0].TS.Equal(t0) || all[0].SessionID != "sess-1" {
		t.Errorf("first event = %+v, want ts=%v session=sess-1", all[0], t0)
	}

	// Since a cutoff after the first two events: only sess-2 remains.
	recent, err := sc2.CLIEventsSince(t0.Add(90 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Verb != "query.stats" {
		t.Errorf("CLIEventsSince(cutoff) = %+v, want only query.stats", recent)
	}

	// BySession returns just that session's verbs, oldest first.
	s1, err := sc2.CLIEventsBySession("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(s1) != 2 || s1[0].Verb != "edit.verify" || s1[1].Verb != "edit.guards" {
		t.Errorf("CLIEventsBySession(sess-1) = %+v, want [edit.verify edit.guards]", s1)
	}
	s2, err := sc2.CLIEventsBySession("sess-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(s2) != 1 || s2[0].Verb != "query.stats" {
		t.Errorf("CLIEventsBySession(sess-2) = %+v, want [query.stats]", s2)
	}

	// An unseen session is a clean empty, never an error.
	none, err := sc2.CLIEventsBySession("nope")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("CLIEventsBySession(unknown) = %+v, want empty", none)
	}
}

// TestCLIEvents_ZeroTSStamped: a zero ts is stamped with the current time
// rather than persisted as the unix epoch, so a "since recent" read still
// finds the row.
func TestCLIEvents_ZeroTSStamped(t *testing.T) {
	sc, _ := openTestSidecar(t)
	defer sc.Close()

	before := time.Now().Add(-time.Second)
	if err := sc.AddCLIEvent(time.Time{}, "sess-z", "edit.tests"); err != nil {
		t.Fatal(err)
	}
	got, err := sc.CLIEventsBySession("sess-z")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	if got[0].TS.Before(before) {
		t.Errorf("zero ts was not stamped to now: %v", got[0].TS)
	}
}
