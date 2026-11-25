//go:build race

package yaml

// raceEnabled: the race detector slows the parser down several times and
// unevenly; timings measure the detector, not the parser.
const raceEnabled = true
