//go:build !linux

package yaml

import "time"

// cpuTimer measures wall time where the thread's CPU time is not at hand.
func cpuTimer() func() time.Duration {
	t0 := time.Now()
	return func() time.Duration { return time.Since(t0) }
}
