package persistence

import (
	"fmt"
	"time"
)

// CLI-verb ledger. The cli_events table records one row per CLI verb that
// ran under a correlation session id. Unlike the opt-in savings ledger it
// carries no consent gate and no daily-aggregate dimension: it is a thin,
// consent-free, per-event log scoped by session_id so a context-budget
// receipt can name the exact verbs (and the safety steps derived from them)
// a single agent session drove through the CLI.
//
// Modeled on savings_events: a single INSERT per event (durable at the call,
// nothing batched), and time- / session-queryable reads. The table is keyed
// on session_id so the receipt can read back exactly one session's verbs.

// CLIEvent is one recorded CLI-verb invocation.
type CLIEvent struct {
	TS        time.Time
	SessionID string
	Verb      string
}

// AddCLIEvent books one CLI-verb invocation as a single INSERT. Durable at
// the call — a SIGKILLed process loses nothing. A zero ts is stamped with
// the current time.
func (s *SidecarStore) AddCLIEvent(ts time.Time, sessionID, verb string) error {
	if s == nil {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if ts.IsZero() {
		ts = time.Now()
	}
	if _, err := s.db.Exec(
		`INSERT INTO cli_events (ts, session_id, verb) VALUES (?,?,?)`,
		ts.UTC().UnixNano(), sessionID, verb,
	); err != nil {
		return fmt.Errorf("persistence: cli event: %w", err)
	}
	return nil
}

// CLIEventsSince returns events with ts >= since, oldest first.
// since=zero returns everything.
func (s *SidecarStore) CLIEventsSince(since time.Time) ([]CLIEvent, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT ts, session_id, verb FROM cli_events WHERE ts >= ? ORDER BY ts, id`,
		unixOrZero(since),
	)
	if err != nil {
		return nil, fmt.Errorf("persistence: cli events since: %w", err)
	}
	defer rows.Close()

	var out []CLIEvent
	for rows.Next() {
		var ev CLIEvent
		var tsN int64
		if err := rows.Scan(&tsN, &ev.SessionID, &ev.Verb); err != nil {
			return nil, err
		}
		ev.TS = time.Unix(0, tsN).UTC()
		out = append(out, ev)
	}
	return out, rows.Err()
}

// CLIEventsBySession returns every event for one correlation session id,
// oldest first.
func (s *SidecarStore) CLIEventsBySession(sessionID string) ([]CLIEvent, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT ts, session_id, verb FROM cli_events WHERE session_id = ? ORDER BY ts, id`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("persistence: cli events by session: %w", err)
	}
	defer rows.Close()

	var out []CLIEvent
	for rows.Next() {
		var ev CLIEvent
		var tsN int64
		if err := rows.Scan(&tsN, &ev.SessionID, &ev.Verb); err != nil {
			return nil, err
		}
		ev.TS = time.Unix(0, tsN).UTC()
		out = append(out, ev)
	}
	return out, rows.Err()
}
