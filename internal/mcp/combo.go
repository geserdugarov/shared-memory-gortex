package mcp

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zzet/gortex/internal/persistence"
)

// comboManager is the in-memory wrapper for ComboStore. It records
// (query → symbol) associations whenever an agent consumes a symbol that
// was returned by a recent search, and exposes a boost score used by the
// search-result reranker. The reranker applies a multiplier once a match
// has been seen at least comboMinHits times, matching FFF's min-3 gate.
type comboManager struct {
	mu    sync.Mutex
	store persistence.ComboStore
	dir   string
	mode  AgentMode

	// Overridable for tests. Seconds since the unix epoch, not a Time, so
	// comparisons stay fast and the struct stays gob-friendly.
	now func() int64
}

const (
	// Min hits before a (query, symbol) pair starts receiving a boost.
	// Below this we treat the association as noise.
	comboMinHits = 3
	// Boost per hit, capped at comboMaxBoost. BM25 scores in practice
	// land in the 1–5 range; a multiplier of ~1.3x per extra hit means a
	// well-established combo can dominate a cold result of similar BM25
	// score without overwhelming a much stronger BM25 hit.
	comboBoostPerHit = 0.3
	comboMaxBoost    = 3.0

	// Max age in seconds for a combo match before it's evicted on access.
	// AI mode: 7 days — agents churn through queries quickly, stale combos
	// become noise. Human mode: 30 days — sessions span weeks and a "I
	// always mean this file when I search for X" association is genuine.
	comboMaxAgeAISec    = int64(7 * 86400)
	comboMaxAgeHumanSec = int64(30 * 86400)
)

func newComboManager(cacheDir, repoPath string, mode AgentMode) *comboManager {
	nowFn := func() int64 { return time.Now().Unix() }
	if cacheDir == "" || repoPath == "" {
		return &comboManager{mode: mode, now: nowFn}
	}
	dir := persistence.ComboDir(cacheDir, repoPath)
	cm := &comboManager{dir: dir, mode: mode, now: nowFn}
	if loaded, err := persistence.LoadCombo(dir); err == nil && loaded != nil {
		cm.store = *loaded
	}
	return cm
}

func (cm *comboManager) maxAgeSec() int64 {
	if cm.mode == ModeHuman {
		return comboMaxAgeHumanSec
	}
	return comboMaxAgeAISec
}

// reapStaleLocked drops matches older than the mode's max age. Called
// lazily on Record and BoostMap so we never keep a background goroutine
// just for GC.
func (cm *comboManager) reapStaleLocked() {
	cutoff := cm.now() - cm.maxAgeSec()
	for qi := range cm.store.Queries {
		q := &cm.store.Queries[qi]
		fresh := q.Matches[:0]
		for _, m := range q.Matches {
			if m.LastUsed >= cutoff {
				fresh = append(fresh, m)
			}
		}
		q.Matches = fresh
	}
}

// normalizeQuery collapses whitespace and lowercases. Matches FFF's
// blake3(project + "::" + query) pattern at a coarser grain — we don't
// need exact byte fidelity, just "treat variant spacings as the same".
func normalizeQuery(q string) string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return ""
	}
	fields := strings.Fields(q)
	return strings.Join(fields, " ")
}

// Record tallies one (query → symbol) hit. If the query is unseen, a new
// entry is created; if the symbol already has an entry for the query, its
// HitCount bumps by one and LastUsed refreshes. Always flushed to disk.
func (cm *comboManager) Record(rawQuery, symbolID string) {
	if cm == nil {
		return
	}
	q := normalizeQuery(rawQuery)
	if q == "" || symbolID == "" {
		return
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.reapStaleLocked()
	now := cm.now()
	idx := cm.findQueryLocked(q)
	if idx < 0 {
		cm.store.Queries = append(cm.store.Queries, persistence.ComboQuery{
			Query:   q,
			Matches: []persistence.ComboMatch{{SymbolID: symbolID, HitCount: 1, LastUsed: now}},
		})
	} else {
		cq := &cm.store.Queries[idx]
		mIdx := -1
		for i := range cq.Matches {
			if cq.Matches[i].SymbolID == symbolID {
				mIdx = i
				break
			}
		}
		if mIdx < 0 {
			cq.Matches = append(cq.Matches, persistence.ComboMatch{
				SymbolID: symbolID, HitCount: 1, LastUsed: now,
			})
		} else {
			cq.Matches[mIdx].HitCount++
			cq.Matches[mIdx].LastUsed = now
		}
		// Keep matches ordered by hit count descending so hot symbols float
		// to the top — cheap because the list is tiny (capped below).
		sort.Slice(cq.Matches, func(i, j int) bool {
			return cq.Matches[i].HitCount > cq.Matches[j].HitCount
		})
		if cap := persistence.MaxComboEntries(); len(cq.Matches) > cap {
			cq.Matches = cq.Matches[:cap]
		}
		// Move the recently-touched query to the tail so SaveCombo's MRU
		// trim preserves it.
		cm.moveToEndLocked(idx)
	}

	if cm.dir == "" {
		return
	}
	_ = persistence.SaveCombo(cm.dir, &cm.store)
}

// BoostMap returns a per-symbol multiplier derived from combo history for
// the given query. Returns nil when the query is empty or has no matches
// above the minimum-hits threshold; callers treat nil as "no reweight".
func (cm *comboManager) BoostMap(rawQuery string) map[string]float64 {
	if cm == nil {
		return nil
	}
	q := normalizeQuery(rawQuery)
	if q == "" {
		return nil
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.reapStaleLocked()
	idx := cm.findQueryLocked(q)
	if idx < 0 {
		return nil
	}
	out := make(map[string]float64, len(cm.store.Queries[idx].Matches))
	for _, m := range cm.store.Queries[idx].Matches {
		if int(m.HitCount) < comboMinHits {
			continue
		}
		extra := float64(int(m.HitCount)-comboMinHits+1) * comboBoostPerHit
		boost := 1.0 + extra
		if boost > comboMaxBoost {
			boost = comboMaxBoost
		}
		out[m.SymbolID] = boost
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// findQueryLocked returns the index of q in store.Queries, or -1.
// Caller must hold cm.mu.
func (cm *comboManager) findQueryLocked(q string) int {
	for i := range cm.store.Queries {
		if cm.store.Queries[i].Query == q {
			return i
		}
	}
	return -1
}

// moveToEndLocked rotates the slice so element at idx lives at the end,
// preserving relative order of the rest. Used to make the MRU trim cheap.
// Caller must hold cm.mu.
func (cm *comboManager) moveToEndLocked(idx int) {
	if idx < 0 || idx >= len(cm.store.Queries)-1 {
		return
	}
	q := cm.store.Queries[idx]
	copy(cm.store.Queries[idx:], cm.store.Queries[idx+1:])
	cm.store.Queries[len(cm.store.Queries)-1] = q
}

// HasData reports whether any queries have been recorded. Used by the
// feedback tool to decide whether to surface combo stats.
func (cm *comboManager) HasData() bool {
	if cm == nil {
		return false
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return len(cm.store.Queries) > 0
}
