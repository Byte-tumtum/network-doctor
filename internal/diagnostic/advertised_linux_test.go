//go:build linux

package diagnostic

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fakeAvahiBrowse(t *testing.T, exitCode int, holdStdout bool) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "avahi-browse")
	script := "#!/bin/sh\n"
	if holdStdout {
		pidFile := filepath.Join(dir, "child.pid")
		t.Setenv("AVAHI_PID_FILE", pidFile)
		script += `/bin/sleep 30 &
printf '%s\n' "$!" > "$AVAHI_PID_FILE"
`
		t.Cleanup(func() {
			// #nosec G304 -- pidFile is this test's temporary fixture path.
			pidText, err := os.ReadFile(pidFile)
			if err != nil {
				return
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(pidText)))
			if err != nil || pid <= 0 {
				return
			}
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Kill()
			}
		})
	}
	script += `
printf '%s\n' '=;eth0;IPv4;Kitchen Speaker;_googlecast._tcp;local;speaker.local;192.0.2.10;8009;"fn=Kitchen\032Speaker"'
exit ` + strconv.Itoa(exitCode) + "\n"
	// #nosec G306 -- this test-owned shell fixture must be executable via PATH.
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestAdvertisedNames(t *testing.T) {
	fakeAvahiBrowse(t, 0, false)
	want := map[string]string{"192.0.2.10": "Kitchen Speaker"}
	if got := AdvertisedNames(context.Background(), []string{"192.0.2.10"}); !maps.Equal(got, want) {
		t.Fatalf("AdvertisedNames = %v, want %v", got, want)
	}
}

func TestAdvertisedNamesRejectsFailedBrowse(t *testing.T) {
	fakeAvahiBrowse(t, 1, false)
	if got := AdvertisedNames(context.Background(), []string{"192.0.2.10"}); got != nil {
		t.Fatalf("AdvertisedNames = %v after failed browse, want nil", got)
	}
}

func TestAdvertisedNamesBoundsInheritedStdout(t *testing.T) {
	fakeAvahiBrowse(t, 0, true)
	done := make(chan map[string]string, 1)
	go func() {
		done <- AdvertisedNames(context.Background(), []string{"192.0.2.10"})
	}()
	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("AdvertisedNames = %v with stdout held open, want nil", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AdvertisedNames remained blocked after WaitDelay")
	}
}
