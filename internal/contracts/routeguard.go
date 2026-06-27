package contracts

import "github.com/zzet/gortex/internal/routeguard"

// IsLikelyHTTPRouteLiteral reports whether a string literal passed to a call
// (with the given callee function name) is plausibly an HTTP route/path rather
// than a filesystem path, config filename, or unrelated string.
//
// The classifier itself lives in the leaf package internal/routeguard so the
// language parsers can share it without an import cycle (their test packages
// already import internal/contracts). This wrapper keeps the long-standing
// contracts-package entry point stable.
func IsLikelyHTTPRouteLiteral(literal, calleeName string) bool {
	return routeguard.IsLikelyHTTPRoute(literal, calleeName)
}

// IsStaticAssetPath reports whether a rooted path is rooted at a static-asset
// directory (/static/..., /assets/..., /public/...) — a servable file location
// rather than an HTTP API endpoint. The HTTP consumer pass uses it to reject
// asset fetches that IsLikelyHTTPRouteLiteral alone would accept.
func IsStaticAssetPath(s string) bool {
	return routeguard.IsStaticAssetPath(s)
}
