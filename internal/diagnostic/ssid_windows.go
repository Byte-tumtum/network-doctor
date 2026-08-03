//go:build windows

package diagnostic

import (
	"context"
	"os/exec"
	"time"

	"github.com/heymaikol/network-doctor/internal/textsafe"
)

// ssid returns iface's Wi-Fi network name via the built-in netsh tool, or ""
// when iface isn't a WLAN interface or netsh fails (display-only garnish).
func ssid(ctx context.Context, iface string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "netsh", "wlan", "show", "interfaces").Output()
	if err != nil {
		return ""
	}
	// netsh writes OEM code page bytes; decode before parsing so non-ASCII
	// interface names still match their block.
	return textsafe.Clean(parseNetshSSID(textsafe.DecodeOEM(string(out)), iface))
}
