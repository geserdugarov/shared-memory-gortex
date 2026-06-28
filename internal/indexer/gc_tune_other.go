//go:build !linux && !darwin

package indexer

// hostPhysicalMemory has no portable reader on this platform, so it returns 0.
// The budget logic then skips the soft memory limit (the GC percent knob still
// applies). Linux and darwin carry real implementations.
func hostPhysicalMemory() uint64 { return 0 }
