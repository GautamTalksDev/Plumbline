// Package safetext strips attacker-controlled terminal sequences from strings
// before they are printed to a TTY.
package safetext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// String returns s with ANSI escapes, bidi overrides, and other control
// characters replaced by visible escapes so they cannot drive a terminal.
func String(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		// ANSI CSI / OSC / ESC sequences
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) {
				switch s[j] {
				case '[': // CSI
					j++
					for j < len(s) {
						c := s[j]
						j++
						if c >= 0x40 && c <= 0x7e {
							break
						}
					}
					b.WriteString("\\x1b[…]")
					i = j
					continue
				case ']': // OSC
					j++
					for j < len(s) {
						if s[j] == 0x07 { // BEL terminator
							j++
							break
						}
						if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
							j += 2
							break
						}
						j++
					}
					b.WriteString("\\x1b]…")
					i = j
					continue
				default:
					b.WriteString("\\x1b")
					i++
					continue
				}
			}
			b.WriteString("\\x1b")
			i++
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteString("\\x")
			b.WriteByte(hex(s[i] >> 4))
			b.WriteByte(hex(s[i] & 0xf))
			i++
			continue
		}
		i += size

		switch {
		case r == '\t' || r == '\n':
			b.WriteRune(r)
		case r == '\r':
			b.WriteString("\\r")
		case isBidiOverride(r):
			b.WriteString(unicodeEscape(r))
		case unicode.IsControl(r):
			b.WriteString(unicodeEscape(r))
		case unicode.Is(unicode.Cf, r) && isDangerousFormat(r):
			b.WriteString(unicodeEscape(r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isBidiOverride(r rune) bool {
	switch r {
	case '\u202A', '\u202B', '\u202C', '\u202D', '\u202E', // LRE RLE PDF LRO RLO
		'\u2066', '\u2067', '\u2068', '\u2069': // LRI RLI FSI PDI
		return true
	}
	return false
}

func isDangerousFormat(r rune) bool {
	// Zero-width / format chars often used in spoofing; show them explicitly.
	switch r {
	case '\u200B', '\u200C', '\u200D', '\u200E', '\u200F', '\uFEFF', '\u00AD':
		return true
	}
	return false
}

func unicodeEscape(r rune) string {
	if r <= 0xffff {
		return "\\u" + hex4(uint16(r))
	}
	return "\\U" + hex8(uint32(r))
}

func hex4(v uint16) string {
	const h = "0123456789abcdef"
	return string([]byte{h[v>>12&0xf], h[v>>8&0xf], h[v>>4&0xf], h[v&0xf]})
}

func hex8(v uint32) string {
	const h = "0123456789abcdef"
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = h[v&0xf]
		v >>= 4
	}
	return string(out)
}

func hex(v byte) byte {
	const h = "0123456789abcdef"
	return h[v]
}
