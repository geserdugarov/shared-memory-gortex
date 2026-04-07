package analysis

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/zzet/gortex/internal/graph"
)

// DeadCodeEntry represents a symbol with zero incoming references that is not excluded.
type DeadCodeEntry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	FilePath string `json:"file_path"`
	Line     int    `json:"start_line"`
}

// HotspotEntry represents a symbol with disproportionately high complexity metrics.
type HotspotEntry struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Kind               string  `json:"kind"`
	FilePath           string  `json:"file_path"`
	Line               int     `json:"start_line"`
	FanIn              int     `json:"fan_in"`
	FanOut             int     `json:"fan_out"`
	CommunityCrossings int     `json:"community_crossings"`
	ComplexityScore    float64 `json:"complexity_score"`
}

// FindDeadCode returns all symbols with zero incoming calls or references,
// excluding entry points, test functions, exported symbols, and user-excluded patterns.
func FindDeadCode(g *graph.Graph, processes *ProcessResult, excludePatterns []string) []DeadCodeEntry {
	nodes := g.AllNodes()

	// Build set of entry point node IDs from processes
	entryPoints := make(map[string]bool)
	if processes != nil {
		for _, proc := range processes.Processes {
			entryPoints[proc.EntryPoint] = true
			// Also consider all nodes that participate in any process
			for _, step := range proc.Steps {
				entryPoints[step] = true
			}
		}
	}

	var result []DeadCodeEntry
	for _, n := range nodes {
		// Skip structural node kinds
		if n.Kind == graph.KindFile || n.Kind == graph.KindImport || n.Kind == graph.KindPackage {
			continue
		}

		// Count incoming calls and references
		inEdges := g.GetInEdges(n.ID)
		incomingCount := 0
		for _, e := range inEdges {
			if e.Kind == graph.EdgeCalls || e.Kind == graph.EdgeReferences {
				incomingCount++
			}
		}

		if incomingCount > 0 {
			continue
		}

		// Check exclusions
		if entryPoints[n.ID] {
			continue
		}
		if isTestFilePath(n.FilePath) {
			continue
		}
		if isExportedSymbol(n.Name, n.Language) {
			continue
		}
		if matchesExcludePattern(n.FilePath, n.ID, excludePatterns) {
			continue
		}

		result = append(result, DeadCodeEntry{
			ID:       n.ID,
			Name:     n.Name,
			Kind:     string(n.Kind),
			FilePath: n.FilePath,
			Line:     n.StartLine,
		})
	}

	// Sort by file path then line for deterministic output
	sort.Slice(result, func(i, j int) bool {
		if result[i].FilePath != result[j].FilePath {
			return result[i].FilePath < result[j].FilePath
		}
		return result[i].Line < result[j].Line
	})

	return result
}

// FindHotspots returns symbols whose ComplexityScore exceeds the given threshold.
// ComplexityScore = (fan_in * 2) + (fan_out * 1.5) + (community_crossings * 3), normalized to 0-100.
// If threshold <= 0, the default threshold is mean + 2*stddev.
func FindHotspots(g *graph.Graph, communities *CommunityResult, threshold float64) []HotspotEntry {
	nodes := g.AllNodes()
	edges := g.AllEdges()

	// Build lookup maps for community membership
	nodeToComm := make(map[string]string)
	if communities != nil {
		nodeToComm = communities.NodeToComm
	}

	// Build edge maps for fan-in and fan-out computation
	// fan_in: incoming calls + references
	// fan_out: outgoing calls
	fanIn := make(map[string]int)
	fanOut := make(map[string]int)

	for _, e := range edges {
		if e.Kind == graph.EdgeCalls || e.Kind == graph.EdgeReferences {
			fanIn[e.To]++
		}
		if e.Kind == graph.EdgeCalls {
			fanOut[e.From]++
		}
	}

	// Compute community crossings per node: outgoing edges to nodes in different communities
	crossings := make(map[string]int)
	for _, e := range edges {
		if e.Kind == graph.EdgeCalls || e.Kind == graph.EdgeReferences {
			fromComm := nodeToComm[e.From]
			toComm := nodeToComm[e.To]
			if fromComm != "" && toComm != "" && fromComm != toComm {
				crossings[e.From]++
			}
		}
	}

	// Compute raw scores for function/method nodes only
	type rawEntry struct {
		node     *graph.Node
		fanIn    int
		fanOut   int
		crossing int
		rawScore float64
	}

	var entries []rawEntry
	for _, n := range nodes {
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			continue
		}

		fi := fanIn[n.ID]
		fo := fanOut[n.ID]
		cc := crossings[n.ID]
		raw := float64(fi)*2.0 + float64(fo)*1.5 + float64(cc)*3.0

		entries = append(entries, rawEntry{
			node:     n,
			fanIn:    fi,
			fanOut:   fo,
			crossing: cc,
			rawScore: raw,
		})
	}

	if len(entries) == 0 {
		return nil
	}

	// Find max raw score for normalization
	maxRaw := 0.0
	for _, e := range entries {
		if e.rawScore > maxRaw {
			maxRaw = e.rawScore
		}
	}

	// Normalize to 0-100
	normalized := make([]float64, len(entries))
	for i, e := range entries {
		if maxRaw > 0 {
			normalized[i] = (e.rawScore / maxRaw) * 100.0
		}
	}

	// Compute default threshold if not specified: mean + 2*stddev
	if threshold <= 0 {
		var sum float64
		for _, s := range normalized {
			sum += s
		}
		mean := sum / float64(len(normalized))

		var variance float64
		for _, s := range normalized {
			diff := s - mean
			variance += diff * diff
		}
		variance /= float64(len(normalized))
		stddev := math.Sqrt(variance)

		threshold = mean + 2.0*stddev
	}

	// Filter and build result
	var result []HotspotEntry
	for i, e := range entries {
		score := math.Round(normalized[i]*100) / 100 // round to 2 decimal places
		if score < threshold {
			continue
		}

		result = append(result, HotspotEntry{
			ID:                 e.node.ID,
			Name:               e.node.Name,
			Kind:               string(e.node.Kind),
			FilePath:           e.node.FilePath,
			Line:               e.node.StartLine,
			FanIn:              e.fanIn,
			FanOut:             e.fanOut,
			CommunityCrossings: e.crossing,
			ComplexityScore:    score,
		})
	}

	// Sort by ComplexityScore descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].ComplexityScore > result[j].ComplexityScore
	})

	return result
}

// isTestFilePath checks if a file path indicates a test file.
func isTestFilePath(path string) bool {
	base := filepath.Base(path)
	return strings.Contains(base, "_test.") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") ||
		strings.HasPrefix(base, "test_") ||
		strings.Contains(path, "__tests__/")
}

// isExportedSymbol checks if a symbol name is exported (public API).
func isExportedSymbol(name, lang string) bool {
	if lang == "go" {
		if len(name) == 0 {
			return false
		}
		return unicode.IsUpper(rune(name[0]))
	}
	// For other languages, assume exported if not starting with underscore
	return len(name) > 0 && !strings.HasPrefix(name, "_")
}

// matchesExcludePattern checks if a node matches any user-configured exclusion pattern.
// Patterns are matched against both the file path and the node ID.
func matchesExcludePattern(filePath, nodeID string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		// Try glob match against file path
		if matched, _ := filepath.Match(pattern, filePath); matched {
			return true
		}
		// Try prefix match against file path
		if strings.HasPrefix(filePath, pattern) {
			return true
		}
		// Try prefix match against node ID
		if strings.HasPrefix(nodeID, pattern) {
			return true
		}
	}
	return false
}
