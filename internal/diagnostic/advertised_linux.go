//go:build linux

package diagnostic

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"time"
)

func advertisedNames(ctx context.Context, ips []string) map[string]string {
	if _, err := exec.LookPath("avahi-browse"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "avahi-browse",
		"--all", "--terminate", "--resolve", "--parsable", "--no-db-lookup")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil
	}
	var out bytes.Buffer
	_, _ = io.Copy(&out, io.LimitReader(stdout, 1<<20))
	_, _ = io.Copy(io.Discard, stdout)
	_ = cmd.Wait()
	return parseAvahiNames(out.Bytes(), ips)
}
