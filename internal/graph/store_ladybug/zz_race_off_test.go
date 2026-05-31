//go:build !race

package store_ladybug

// raceModeEnabled is false in normal (non -race) builds. See the //go:build
// race counterpart for why this exists.
const raceModeEnabled = false
