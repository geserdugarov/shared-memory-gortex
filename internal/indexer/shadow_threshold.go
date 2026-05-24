package indexer

import (
	"os"
	"strconv"
)

// defaultShadowMaxFileCount caps the file count above which IndexCtx
// refuses to swap idx.graph for an in-memory shadow during cold start.
// Picked empirically from the in-memory store's prior profiling: at
// ~35k C files (drivers/) the in-memory store peaked at 8.6GB RSS; at
// 60k+ the peak is well past 16GB. The shadow path doubles that
// footprint (in-memory + persisted disk copy at the FlushBulk step),
// so the safe ceiling for a 32GB dev machine sits around 50k source
// files. Above that we fall through to the per-call disk path —
// slower per IndexCtx but bounded RAM.
const defaultShadowMaxFileCount = 50000

// shadowMaxFileCount returns the active file-count ceiling for the
// IndexCtx in-memory shadow swap. GORTEX_SHADOW_MAX_FILES overrides
// the default; setting it to 0 disables the shadow entirely (always
// run against the disk store directly), setting it to a high value
// (e.g. 10_000_000) effectively disables the guard. Non-numeric or
// negative values fall back to the default.
func shadowMaxFileCount() int {
	if v := os.Getenv("GORTEX_SHADOW_MAX_FILES"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n >= 0 {
			return n
		}
	}
	return defaultShadowMaxFileCount
}
