//go:build !linux && !darwin

package diagnostic

import "context"

// AdvertisedNames has no DNS-SD browser to shell out to here, so every IP falls
// through to ReverseName. Windows is the platform this costs: its DNS client
// speaks mDNS but exposes no browser, and the WinRT one behind
// Windows.Networking.ServiceDiscovery.Dnssd needs COM — which needs cgo, which
// release builds don't have.
func AdvertisedNames(context.Context, []string) map[string]string { return nil }
