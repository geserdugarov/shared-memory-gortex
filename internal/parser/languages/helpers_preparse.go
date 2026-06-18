package languages

// blankConditionalDirectives rewrites C-family conditional-compilation
// directive lines (#if / #elif / #else / #endif) into runs of spaces while
// preserving every byte offset and newline. A conditional directive that
// would otherwise detach an enclosing declaration — the classic case being a
// #if inside an enum member list that breaks the surrounding class's member
// block, silently dropping most of its methods — can no longer corrupt the
// parse, yet the code in every branch survives as plain source and all node
// ranges stay byte-exact.
//
// Only the four conditional directives are blanked. Non-conditional directives
// (#region / #endregion / #pragma / #nullable / #define / #undef / #line /
// #warning / #error) are left untouched. The returned slice always has the
// same length as src.
func blankConditionalDirectives(src []byte) []byte {
	out := make([]byte, len(src))
	copy(out, src)
	for i, n := 0, len(out); i < n; {
		lineStart := i
		j := i
		for j < n && out[j] != '\n' {
			j++
		}
		if isConditionalDirectiveLine(out[lineStart:j]) {
			for k := lineStart; k < j; k++ {
				out[k] = ' '
			}
		}
		if j < n {
			i = j + 1
		} else {
			i = j
		}
	}
	return out
}

// isConditionalDirectiveLine reports whether line (a single source line with no
// trailing newline) is a #if / #elif / #else / #endif preprocessor directive.
// A directive's '#' must be the first non-whitespace byte on the line — which
// is exactly why a '#if' appearing inside a string literal never matches, since
// the first non-whitespace byte there is the opening quote. C# permits
// whitespace between the '#' and the keyword, so that is tolerated too.
func isConditionalDirectiveLine(line []byte) bool {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || line[i] != '#' {
		return false
	}
	i++
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	kwStart := i
	for i < len(line) && isPreprocIdentByte(line[i]) {
		i++
	}
	switch string(line[kwStart:i]) {
	case "if", "elif", "else", "endif":
		return true
	}
	return false
}

// isPreprocIdentByte reports whether b can appear in a preprocessor directive
// keyword (ASCII letters, digits, underscore).
func isPreprocIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}
