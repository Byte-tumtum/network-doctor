//go:build acceptance && (darwin || windows)

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/heymaikol/network-doctor/internal/report"
)

// TestNativeBinaryUsesLoopbackInterface proves the release-shaped binary can
// resolve and inspect a real host interface, then run the host's SSID fallback.
func TestNativeBinaryUsesLoopbackInterface(t *testing.T) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	bin := filepath.Join(t.TempDir(), "netdoc"+suffix)
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build netdoc: %v\n%s", err, out)
	}

	runCtx, cancelRun := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelRun()
	out, err := exec.CommandContext(runCtx, bin,
		"--json", "--check", "iface,ssid", "--iface", "127.0.0.1", "--public-dns=").CombinedOutput()
	if err != nil {
		t.Fatalf("run netdoc: %v\n%s", err, out)
	}
	var got report.Report
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out)
	}
	if len(got.Checks) != 2 {
		t.Fatalf("checks = %+v, want interface and SSID only", got.Checks)
	}
	checks := map[string]report.Check{}
	for _, check := range got.Checks {
		checks[check.ID] = check
	}
	iface := checks["iface"]
	if iface.Status != "PASS" || iface.Source != "127.0.0.1" || iface.Iface == "" {
		t.Errorf("interface check = %+v, want loopback source on a named host interface", iface)
	}
	if ssid := checks["ssid"]; ssid.Status != "N/A" || ssid.Network != "" {
		t.Errorf("SSID check on loopback = %+v, want N/A with no network", ssid)
	}
}
