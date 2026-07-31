//go:build !linux

package diagnostic

import "context"

// AdvertisedNames has no DNS-SD browser to shell out to off Linux, so every IP
// falls through to ReverseName.
func AdvertisedNames(context.Context, []string) map[string]string { return nil }
