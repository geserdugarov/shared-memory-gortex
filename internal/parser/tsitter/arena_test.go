package tsitter

import "testing"

// TestNodeArenaStablePointers guards the arena's load-bearing invariant:
// a pointer returned by alloc stays valid after later allocations grow the
// arena onto a new backing chunk. If alloc ever appended into a single
// reallocating slice, earlier &slice[i] pointers would dangle and node data
// would silently corrupt mid-walk.
func TestNodeArenaStablePointers(t *testing.T) {
	a := newNodeArena()
	const n = arenaMaxChunk*2 + 17 // cross several chunk boundaries incl. the max-size cap
	ptrs := make([]*Node, n)
	for i := range ptrs {
		p := a.alloc()
		if p == nil {
			t.Fatalf("alloc %d returned nil", i)
		}
		p.valid = true
		ptrs[i] = p
	}
	seen := make(map[*Node]bool, n)
	for i, p := range ptrs {
		if seen[p] {
			t.Fatalf("alloc %d aliased an earlier node pointer", i)
		}
		seen[p] = true
		if !p.valid {
			t.Fatalf("node %d was clobbered by a later chunk allocation", i)
		}
	}
}

// TestNodeArenaGeometricGrowth confirms the first chunk is small (low waste
// for tiny files) and chunks grow up to the cap (few objects for deep files).
func TestNodeArenaGeometricGrowth(t *testing.T) {
	a := newNodeArena()
	a.alloc()
	if got := len(a.cur); got != arenaFirstChunk {
		t.Fatalf("first chunk size = %d, want %d", got, arenaFirstChunk)
	}
	// Drain well past the cap; the current chunk must never exceed the max.
	for i := 0; i < arenaMaxChunk*3; i++ {
		a.alloc()
		if len(a.cur) > arenaMaxChunk {
			t.Fatalf("chunk grew to %d, exceeds cap %d", len(a.cur), arenaMaxChunk)
		}
	}
}
