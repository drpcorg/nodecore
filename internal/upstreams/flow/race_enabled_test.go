//go:build race

package flow

// the race detector changes allocation counts
const raceEnabled = true
