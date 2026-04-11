package mcp

import (
	"sync"
	"time"

	"github.com/zzet/gortex/internal/persistence"
)

// feedbackManager provides thread-safe access to agent feedback data
// and handles persistence across server restarts.
type feedbackManager struct {
	mu    sync.Mutex
	store persistence.FeedbackStore
	dir   string // cache directory for feedback file
}

// newFeedbackManager creates a feedback manager, loading any existing
// feedback from disk. Returns a no-op manager if dir is empty.
func newFeedbackManager(cacheDir, repoPath string) *feedbackManager {
	if cacheDir == "" || repoPath == "" {
		return &feedbackManager{}
	}
	dir := persistence.FeedbackDir(cacheDir, repoPath)
	fm := &feedbackManager{dir: dir}

	loaded, err := persistence.LoadFeedback(dir)
	if err == nil && loaded != nil {
		fm.store = *loaded
	}
	return fm
}

// Record appends a feedback entry and flushes to disk.
func (fm *feedbackManager) Record(entry persistence.FeedbackEntry) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	entry.Timestamp = time.Now()
	fm.store.Entries = append(fm.store.Entries, entry)

	if fm.dir == "" {
		return nil
	}
	return persistence.SaveFeedback(fm.dir, &fm.store)
}

// symbolStats holds aggregated feedback counts for a single symbol.
type symbolStats struct {
	UsefulCount    int
	NotNeededCount int
	MissingCount   int
}

// Score returns a value in [-1, 1] representing how useful this symbol has been.
// Positive = consistently useful, negative = consistently not needed.
func (ss symbolStats) Score() float64 {
	total := ss.UsefulCount + ss.NotNeededCount
	if total == 0 {
		return 0
	}
	return float64(ss.UsefulCount-ss.NotNeededCount) / float64(total)
}

// GetSymbolScore returns the feedback score for a single symbol.
// Returns 0 if the symbol has no feedback data.
func (fm *feedbackManager) GetSymbolScore(symbolID string) float64 {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	stats := fm.aggregateSymbol(symbolID)
	return stats.Score()
}

// aggregateSymbol computes stats for one symbol across all entries.
// Caller must hold fm.mu.
func (fm *feedbackManager) aggregateSymbol(symbolID string) symbolStats {
	var ss symbolStats
	for _, e := range fm.store.Entries {
		for _, id := range e.Useful {
			if id == symbolID {
				ss.UsefulCount++
			}
		}
		for _, id := range e.NotNeeded {
			if id == symbolID {
				ss.NotNeededCount++
			}
		}
		for _, id := range e.Missing {
			if id == symbolID {
				ss.MissingCount++
			}
		}
	}
	return ss
}

// AggregatedStats returns summary statistics across all feedback entries.
func (fm *feedbackManager) AggregatedStats(toolSource string, topN int) map[string]any {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	if topN <= 0 {
		topN = 10
	}

	// Collect all symbol IDs and their stats.
	allStats := make(map[string]*symbolStats)
	totalUseful := 0
	totalNotNeeded := 0
	matchingEntries := 0

	for _, e := range fm.store.Entries {
		if toolSource != "" && toolSource != "all" && e.Source != toolSource {
			continue
		}
		matchingEntries++

		for _, id := range e.Useful {
			totalUseful++
			if _, ok := allStats[id]; !ok {
				allStats[id] = &symbolStats{}
			}
			allStats[id].UsefulCount++
		}
		for _, id := range e.NotNeeded {
			totalNotNeeded++
			if _, ok := allStats[id]; !ok {
				allStats[id] = &symbolStats{}
			}
			allStats[id].NotNeededCount++
		}
		for _, id := range e.Missing {
			if _, ok := allStats[id]; !ok {
				allStats[id] = &symbolStats{}
			}
			allStats[id].MissingCount++
		}
	}

	// Build ranked lists.
	type ranked struct {
		ID    string  `json:"id"`
		Score float64 `json:"score"`
		Count int     `json:"count"`
	}

	var mostUseful, mostMissed, mostDemoted []ranked

	for id, ss := range allStats {
		if ss.UsefulCount > 0 {
			mostUseful = append(mostUseful, ranked{ID: id, Score: ss.Score(), Count: ss.UsefulCount})
		}
		if ss.MissingCount > 0 {
			mostMissed = append(mostMissed, ranked{ID: id, Score: ss.Score(), Count: ss.MissingCount})
		}
		if ss.NotNeededCount > 0 {
			mostDemoted = append(mostDemoted, ranked{ID: id, Score: ss.Score(), Count: ss.NotNeededCount})
		}
	}

	// Sort and trim.
	sortDesc := func(s []ranked, byCount bool) []ranked {
		for i := range s {
			for j := i + 1; j < len(s); j++ {
				swap := false
				if byCount {
					swap = s[j].Count > s[i].Count
				} else {
					swap = s[j].Score > s[i].Score
				}
				if swap {
					s[i], s[j] = s[j], s[i]
				}
			}
		}
		if len(s) > topN {
			s = s[:topN]
		}
		return s
	}

	mostUseful = sortDesc(mostUseful, false)
	mostMissed = sortDesc(mostMissed, true)
	mostDemoted = sortDesc(mostDemoted, true)

	accuracy := 0.0
	if totalUseful+totalNotNeeded > 0 {
		accuracy = float64(totalUseful) / float64(totalUseful+totalNotNeeded)
	}

	return map[string]any{
		"total_entries": matchingEntries,
		"accuracy":      accuracy,
		"most_useful":   mostUseful,
		"most_missed":   mostMissed,
		"most_demoted":  mostDemoted,
	}
}

// HasData returns true if there is any feedback recorded.
func (fm *feedbackManager) HasData() bool {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return len(fm.store.Entries) > 0
}

// MissedSymbols returns symbol IDs that have been reported missing
// at least minCount times, sorted by miss frequency descending.
func (fm *feedbackManager) MissedSymbols(minCount int) []string {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	counts := make(map[string]int)
	for _, e := range fm.store.Entries {
		for _, id := range e.Missing {
			counts[id]++
		}
	}

	type mc struct {
		id    string
		count int
	}
	var result []mc
	for id, c := range counts {
		if c >= minCount {
			result = append(result, mc{id, c})
		}
	}

	// Sort by count descending.
	for i := range result {
		for j := i + 1; j < len(result); j++ {
			if result[j].count > result[i].count {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	ids := make([]string, len(result))
	for i, r := range result {
		ids[i] = r.id
	}
	return ids
}
