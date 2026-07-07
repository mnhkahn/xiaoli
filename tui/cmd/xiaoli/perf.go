package main

import (
	"time"

	"github.com/mnhkahn/gogogo/logger"
)

var (
	perfTraceEnabled   bool
	perfTraceThreshold = 20 * time.Millisecond
	perfTraceLogf      = logger.Debugf
)

func tracePerf(label string) func() {
	if !perfTraceEnabled {
		return func() {}
	}
	start := time.Now()
	return func() {
		if elapsed := time.Since(start); elapsed >= perfTraceThreshold {
			perfTraceLogf("tui perf: %s took %s", label, elapsed)
		}
	}
}
