package main

import (
	"strings"
	"testing"
	"time"
)

func TestTracePerfRequiresEnabledFlag(t *testing.T) {
	oldEnabled := perfTraceEnabled
	oldThreshold := perfTraceThreshold
	oldLogf := perfTraceLogf
	defer func() {
		perfTraceEnabled = oldEnabled
		perfTraceThreshold = oldThreshold
		perfTraceLogf = oldLogf
	}()

	var lines []string
	perfTraceEnabled = false
	perfTraceThreshold = 0
	perfTraceLogf = func(format string, args ...any) {
		lines = append(lines, format)
	}

	tracePerf("disabled")()

	if len(lines) != 0 {
		t.Fatalf("tracePerf logged while disabled: %#v", lines)
	}
}

func TestTracePerfLogsWhenEnabledAndSlow(t *testing.T) {
	oldEnabled := perfTraceEnabled
	oldThreshold := perfTraceThreshold
	oldLogf := perfTraceLogf
	defer func() {
		perfTraceEnabled = oldEnabled
		perfTraceThreshold = oldThreshold
		perfTraceLogf = oldLogf
	}()

	var lines []string
	perfTraceEnabled = true
	perfTraceThreshold = 0
	perfTraceLogf = func(format string, args ...any) {
		lines = append(lines, format)
	}

	tracePerf("model.View")()

	if len(lines) != 1 || !strings.Contains(lines[0], "tui perf: %s took %s") {
		t.Fatalf("tracePerf logs = %#v, want perf format", lines)
	}
}

func TestTracePerfSkipsFastOperations(t *testing.T) {
	oldEnabled := perfTraceEnabled
	oldThreshold := perfTraceThreshold
	oldLogf := perfTraceLogf
	defer func() {
		perfTraceEnabled = oldEnabled
		perfTraceThreshold = oldThreshold
		perfTraceLogf = oldLogf
	}()

	var lines []string
	perfTraceEnabled = true
	perfTraceThreshold = time.Hour
	perfTraceLogf = func(format string, args ...any) {
		lines = append(lines, format)
	}

	tracePerf("fast")()

	if len(lines) != 0 {
		t.Fatalf("tracePerf logged fast operation: %#v", lines)
	}
}
