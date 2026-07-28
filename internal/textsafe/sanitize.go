// Package textsafe removes terminal control sequences from untrusted text.
// Anything from the network (banners, tool output) passes through Clean
// before hitting the screen, so a hostile server can't redraw our terminal.
package textsafe

import (
	"regexp"
	"strings"
	"unicode"
)

// escapeRe matches CSI (ESC [ ... final), OSC (ESC ] ... BEL|ST), and any
// other two-byte ESC sequence. CSI/OSC listed first — alternation is
// first-match-wins, and the generic 2-byte form would otherwise eat the
// introducer and leave the payload behind as "clean" text.
var escapeRe = regexp.MustCompile(`\x1b\[[0-9:;<=>?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-_]`)

// Clean strips escape sequences and control runes from external text.
// Tabs become spaces; everything else in the control range (lone ESC, C1,
// even newlines) and invalid UTF-8 gets dropped. Newlines because Clean is
// single-line by contract — streaming callers split first, so a newline
// showing up here is nobody's friend.
//
// Format runes go too. A banner carrying U+202E or an isolate from the
// U+2066 block can flip the reading order of everything after it, so a FAIL
// row renders as PASS and a hostname reads as somebody else's. Cf also
// covers the invisible padding — U+200B, U+00AD — that hides text in a
// report. ZWJ and ZWNJ are Cf as well, so emoji sequences and Arabic or
// Indic shaping lose their joiners here; that is a rendering nit in a
// banner, and worth less than the reordering it buys off.
func Clean(s string) string {
	// Whole sequences first; the rune filter would strip the ESC and
	// leave "[31m" behind looking innocent.
	s = escapeRe.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r == unicode.ReplacementChar: // invalid UTF-8
			return -1
		case unicode.IsControl(r): // C0 + C1, incl. ESC and \n
			return -1
		case unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp):
			// Cf: bidi overrides/isolates/marks, zero-width, soft hyphen.
			// Zl/Zp: U+2028/2029, line breaks by another name.
			return -1
		default:
			return r
		}
	}, s)
}
