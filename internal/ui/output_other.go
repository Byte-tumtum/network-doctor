//go:build !windows

package ui

import "strings"

func decodeToolOutput(_ string, line string) string {
	return strings.ToValidUTF8(line, "?")
}
