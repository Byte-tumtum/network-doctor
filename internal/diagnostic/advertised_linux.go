//go:build linux

package diagnostic

import (
	"context"
	"io"
	"os/exec"
	"time"
)

const maxAvahiOutput = 1 << 20

type cappedAvahiOutput []byte

func (out *cappedAvahiOutput) Write(p []byte) (int, error) {
	*out = append(*out, p[:min(len(p), maxAvahiOutput-len(*out))]...)
	return len(p), nil
}

// AdvertisedNames returns the device names advertised through the platform's
// DNS-SD browser, keyed by IP. They win over reverse DNS because they are
// usually the user-facing label configured on the device, so callers should
// ask here first and only fall back to ReverseName for what's left, keeping a
// row's first name its final one.
func AdvertisedNames(ctx context.Context, ips []string) map[string]string {
	if _, err := exec.LookPath("avahi-browse"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "avahi-browse",
		"--all", "--terminate", "--resolve", "--parsable", "--no-db-lookup")
	cmd.WaitDelay = time.Second // don't hang on Wait if a child holds the pipe
	var out cappedAvahiOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil
	}
	return parseAvahiNames(out, ips)
}
