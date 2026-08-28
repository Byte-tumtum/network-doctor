//go:build acceptance && (darwin || windows)

package ui

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// TestNativeDrillDownToolsExecute crosses the process boundary with the three
// host-built-in tools whose behavior is stable without external connectivity.
func TestNativeDrillDownToolsExecute(t *testing.T) {
	target := mustTarget(t, "127.0.0.1")
	tools := toolsFor(target, runtime.GOOS, toolBind{})
	for _, key := range []string{"I", "s", "p"} {
		tool := toolByKey(t, tools, key)
		t.Run(tool.Name, func(t *testing.T) {
			if !tool.Available {
				t.Fatalf("host tool %q is unavailable", tool.Bin)
			}
			args, env, _ := tool.Build(target, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			job, _, err := startTool(ctx, 0, "acceptance-"+key, tool.Bin, args, env, 15*time.Second)
			if err != nil {
				t.Fatalf("start %s %v: %v", tool.Bin, args, err)
			}
			lines, done := drain(t, job.ch)
			if done.Status != JobDone {
				t.Fatalf("%s %v status = %v, output = %q", tool.Bin, args, done.Status, lines)
			}
			if len(lines) == 0 {
				t.Fatalf("%s %v produced no output", tool.Bin, args)
			}
		})
	}
}
