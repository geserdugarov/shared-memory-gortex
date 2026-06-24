package languages

import "sort"

// lineAt returns the 1-based line number for byte offset pos.
func lineAt(src []byte, pos int) int {
	line := 1
	for i := 0; i < pos && i < len(src); i++ {
		if src[i] == '\n' {
			line++
		}
	}
	return line
}

// lineStartOffsets precomputes the byte offset at which each line begins:
// entry k is the start of the (k+1)-th line (entry 0 is 0). One O(n) scan up
// front lets lineForOffset resolve any offset → line in O(log n), replacing a
// repeated O(offset) prefix newline-count when many offsets are resolved over
// the same source (large MyBatis mappers, multi-element XML).
func lineStartOffsets(src []byte) []int {
	starts := make([]int, 1, 256)
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineForOffset returns the 1-based line number for byte offset off, given the
// line-start table from lineStartOffsets — the insertion point of off among the
// line starts (binary search).
func lineForOffset(starts []int, off int) int {
	return sort.Search(len(starts), func(i int) bool { return starts[i] > off })
}

// findBlockEnd finds the approximate end line of a brace-delimited
// block starting at startLine (1-based). Counts `{` / `}` depth from
// startLine onward and returns the 1-based line where depth first
// drops back to zero — startLine itself when no brace is found.
func findBlockEnd(lines []string, startLine int) int {
	depth := 0
	for i := startLine - 1; i < len(lines); i++ {
		for _, ch := range lines[i] {
			switch ch {
			case '{':
				depth++
			case '}':
				depth--
				if depth <= 0 {
					return i + 1
				}
			}
		}
	}
	return startLine
}
