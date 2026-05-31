//go:build race

package store_ladybug

// raceModeEnabled reports whether the binary was built with the race
// detector (-race). Stdlib exposes no such flag, so it is derived from the
// `race` build tag the toolchain sets under -race. Used to skip deliberately
// huge scale tests whose allocations exhaust the race detector's shadow
// memory ("too many address space collisions for -race mode").
const raceModeEnabled = true
