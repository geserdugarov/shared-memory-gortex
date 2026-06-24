package resolver

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// UIKit directory-convention name-resolution. Like the SwiftUI pass, this is a
// fallback for the references sourcekit-lsp leaves unresolved: a `*ViewController`
// binds to /ViewControllers/ or /Controllers/, a `*Cell` to /Cells/,
// /TableViewCells/ or /CollectionViewCells/, and a `*Delegate` / `*DataSource`
// to a /Delegates/ or /DataSources/ directory (or, via the same-dir tier, the
// owning controller's own directory). Swift and Objective-C references are
// considered.

var (
	uikitVCDirs       = []string{"/ViewControllers/", "/Controllers/", "/ViewController/", "/Controller/"}
	uikitCellDirs     = []string{"/Cells/", "/TableViewCells/", "/CollectionViewCells/", "/Cell/"}
	uikitDelegateDirs = []string{"/Delegates/", "/DataSources/", "/Delegate/", "/DataSource/"}
)

// ResolveUIKitRefs binds residual unresolved UIKit references to their
// directory-located definitions. Returns the count bound.
func ResolveUIKitRefs(g graph.Store) int {
	if g == nil {
		return 0
	}
	resolved := 0
	var reindex []graph.EdgeReindex
	for _, kind := range []graph.EdgeKind{graph.EdgeInstantiates, graph.EdgeReferences, graph.EdgeTypedAs, graph.EdgeCalls} {
		for e := range g.EdgesByKind(kind) {
			if e == nil || !graph.IsUnresolvedTarget(e.To) {
				continue
			}
			name := graph.UnresolvedName(e.To)
			if name == "" || strings.ContainsRune(name, '.') {
				continue
			}
			dirs, ok := uikitDirsFor(name)
			if !ok {
				continue
			}
			fromFile := ""
			if n := g.GetNode(e.From); n != nil {
				fromFile = n.FilePath
			}
			if !isAppleSourceFile(fromFile) {
				continue
			}
			targetID, conf := ResolveByConvention(g, name, "", dirs, fromFile)
			if targetID == "" {
				continue
			}
			oldTo := e.To
			e.To = targetID
			e.Origin = graph.OriginASTInferred
			e.Confidence = conf
			e.ConfidenceLabel = graph.ConfidenceLabelFor(e.Kind, conf)
			StampSynthesized(e, SynthUIKitResolve)
			reindex = append(reindex, graph.EdgeReindex{Edge: e, OldTo: oldTo})
			resolved++
		}
	}
	if len(reindex) > 0 {
		g.ReindexEdges(reindex)
	}
	return resolved
}

// uikitDirsFor classifies a UIKit reference name into its convention dirs.
func uikitDirsFor(name string) ([]string, bool) {
	switch {
	case strings.HasSuffix(name, "ViewController"):
		return uikitVCDirs, true
	case strings.HasSuffix(name, "Cell"):
		return uikitCellDirs, true
	case strings.HasSuffix(name, "Delegate"), strings.HasSuffix(name, "DataSource"):
		return uikitDelegateDirs, true
	}
	return nil, false
}

// isAppleSourceFile reports whether a path is a Swift or Objective-C source
// file — the only files the UIKit pass binds references from.
func isAppleSourceFile(p string) bool {
	switch {
	case strings.HasSuffix(p, ".swift"), strings.HasSuffix(p, ".m"),
		strings.HasSuffix(p, ".mm"), strings.HasSuffix(p, ".h"):
		return true
	}
	return false
}
