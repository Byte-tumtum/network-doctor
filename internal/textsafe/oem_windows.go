package textsafe

import (
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// CP_OEMCP: whatever console code page this machine actually runs, not a
// guess at 437.
const oemCodePage = 1

// DecodeOEM converts console output from the OEM code page to UTF-8. Console
// tools emit code-page bytes, not UTF-8, so non-ASCII interface and network
// names arrive mangled without this. Falls back to byte-wise replacement when
// the conversion fails, which keeps garbage visible instead of silent.
func DecodeOEM(s string) string {
	if decoded, err := decodeCodePage(oemCodePage, s); err == nil {
		return decoded
	}
	return strings.ToValidUTF8(s, "?")
}

func decodeCodePage(codePage uint32, s string) (string, error) {
	if s == "" {
		return "", nil
	}
	src := []byte(s)
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
