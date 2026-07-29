package ui

import (
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const windowsOEMCodePage = 1

func decodeToolOutput(name, line string) string {
	if isWindowsOEMTool(name) {
		if decoded, err := decodeWindowsCodePage(windowsOEMCodePage, line); err == nil {
			return decoded
		}
	}
	return strings.ToValidUTF8(line, "?")
}

func isWindowsOEMTool(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(filepath.Base(name)), ".exe")
	switch name {
	case "route", "netstat", "ping", "nslookup", "tracert", "pathping":
		return true
	default:
		return false
	}
}

func decodeWindowsCodePage(codePage uint32, line string) (string, error) {
	if line == "" {
		return "", nil
	}
	src := []byte(line)
	n, err := windows.MultiByteToWideChar(codePage, 0, &src[0], int32(len(src)), nil, 0)
	if err != nil {
		return "", err
	}
	dst := make([]uint16, n)
	n, err = windows.MultiByteToWideChar(codePage, 0, &src[0], int32(len(src)), &dst[0], n)
	if err != nil {
		return "", err
	}
	return string(utf16.Decode(dst[:n])), nil
}
