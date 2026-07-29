//go:build !linux

package diagnostic

import "context"

func advertisedNames(context.Context, []string) map[string]string { return nil }
