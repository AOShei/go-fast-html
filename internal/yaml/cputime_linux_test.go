//go:build linux

package yaml

import (
	"runtime"
	"syscall"
	"time"
)

// rusageThread is RUSAGE_THREAD of getrusage(2).
const rusageThread = 1

// cpuTimer measures the CPU time of the calling goroutine's thread, which
// a shared machine's load does not inflate as it does wall time. The
// caller must keep collection off while timing.
func cpuTimer() func() time.Duration {
	runtime.LockOSThread()
	start, ok := threadCPU()
	t0 := time.Now()
	return func() time.Duration {
		defer runtime.UnlockOSThread()
		end, ok2 := threadCPU()
		if !ok || !ok2 {
			return time.Since(t0)
		}
		return end - start
	}
}

func threadCPU() (time.Duration, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(rusageThread, &ru); err != nil {
		return 0, false
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), true
}
