//go:build !race

package gofasthtml

// raceSlowdown scales the time bounds of the worst-case tests: the race
// detector makes the code several times slower.
const raceSlowdown = 1
