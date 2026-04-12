package audit

import (
	"regexp"
	"strings"
)

// tokenKind classifies a backticked token as path, symbol, or other (ignored).
type tokenKind int

const (
	tokenOther tokenKind = iota
	tokenSymbol
	tokenPath
)

// extractBackticked returns all `backticked` spans on a line, without the
// enclosing backticks. Triple-backticks (code fences) are skipped.
func extractBackticked(line string) []string {
	// Skip fenced code block markers.
	trim := strings.TrimSpace(line)
	if strings.HasPrefix(trim, "```") {
		return nil
	}

	var out []string
	for {
		start := strings.Index(line, "`")
		if start < 0 {
			return out
		}
		// Don't match triple backticks mid-line.
		if strings.HasPrefix(line[start:], "```") {
			end := strings.Index(line[start+3:], "```")
			if end < 0 {
				return out
			}
			line = line[start+3+end+3:]
			continue
		}
		rest := line[start+1:]
		end := strings.Index(rest, "`")
		if end < 0 {
			return out
		}
		tok := strings.TrimSpace(rest[:end])
		if tok != "" {
			out = append(out, tok)
		}
		line = rest[end+1:]
	}
}

// Path-shaped: contains a path separator OR starts with a dot and has a dot
// further on (e.g. `.gortex.yaml`). URLs are excluded.
var (
	pathExts = map[string]bool{
		".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
		".py": true, ".rs": true, ".java": true, ".kt": true, ".rb": true,
		".cs": true, ".cpp": true, ".c": true, ".h": true, ".hpp": true,
		".md": true, ".yaml": true, ".yml": true, ".json": true, ".toml": true,
		".sh": true, ".ps1": true, ".lua": true, ".dart": true, ".swift": true,
		".html": true, ".css": true, ".sql": true, ".proto": true, ".xml": true,
	}

	// Identifier-shaped: CamelCase / PascalCase / snake_case / dotted / `()` suffix.
	// Require either a capital letter somewhere OR an explicit `()` suffix so we
	// don't flag every plain english word.
	identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:[.:][A-Za-z_][A-Za-z0-9_]*)*(?:\(\))?$`)

	// Common shell/tool names and markdown-ish terms that get backticked but
	// are not symbol references.
	skipTokens = map[string]bool{
		"true": true, "false": true, "nil": true, "null": true, "None": true,
		"TODO": true, "FIXME": true, "NOTE": true, "XXX": true,
		"grep": true, "ls": true, "cd": true, "rm": true, "mv": true, "cp": true,
		"cat": true, "echo": true, "sed": true, "awk": true, "find": true,
		"git": true, "go": true, "npm": true, "yarn": true, "make": true,
		"docker": true, "curl": true, "wget": true, "ssh": true,
	}
)

func classifyToken(tok string) tokenKind {
	if tok == "" || len(tok) > 200 {
		return tokenOther
	}
	// Strip common markdown decorations that sometimes end up inside backticks.
	tok = strings.TrimSpace(tok)

	// URL? Ignore.
	if strings.Contains(tok, "://") {
		return tokenOther
	}

	if isPathLike(tok) {
		return tokenPath
	}

	// Strip trailing `()` for ident classification.
	bare := strings.TrimSuffix(tok, "()")
	if skipTokens[bare] {
		return tokenOther
	}
	// Whitespace or quoted content -> not a symbol.
	if strings.ContainsAny(bare, " \t\"'") {
		return tokenOther
	}
	if !identRe.MatchString(tok) {
		return tokenOther
	}
	// Avoid flagging plain lowercase english words. Require at least one of:
	// capital letter, underscore, dot, or `()` suffix.
	hasSignal := strings.HasSuffix(tok, "()") ||
		strings.ContainsAny(bare, "._") ||
		hasUppercase(bare)
	if !hasSignal {
		return tokenOther
	}
	// Must be at least 3 chars to reduce false positives on 1-2 letter vars.
	if len(bare) < 3 {
		return tokenOther
	}
	return tokenSymbol
}

// isPathLike detects tokens that look like filesystem paths rather than symbols.
func isPathLike(tok string) bool {
	if strings.ContainsAny(tok, "/\\") {
		// Exclude package-style dotted identifiers that happen to contain no
		// slashes (already handled).
		return true
	}
	// Files with a known extension (e.g. `.gortex.yaml`, `README.md`).
	if idx := strings.LastIndex(tok, "."); idx > 0 {
		ext := strings.ToLower(tok[idx:])
		if pathExts[ext] {
			return true
		}
	}
	return false
}

func hasUppercase(s string) bool {
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}
