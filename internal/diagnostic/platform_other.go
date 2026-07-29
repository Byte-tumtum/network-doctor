//go:build !linux && !darwin && !windows

package diagnostic

import "context"

// Unsupported GOOSes compile with the cosmetic SSID field empty; they are
// out of scope because we cannot test them.

func ssid(context.Context, string) string { return "" }
