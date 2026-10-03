// Package safetext makes strings from untrusted sources (the job's objects,
// the budget database) safe to print on a terminal: escape sequences are
// dropped, other controls, bidi and zero-width characters become "?".
package safetext

import (
	"strings"
	"unicode/utf8"
)

// OneLine is s made safe for one line of a terminal: ESC, CSI, OSC, DCS,
// SOS, PM and APC sequences (7- and 8-bit) are dropped, and every other C0
// control but tab, DEL, C1 control, bidi control, zero-width character,
// invalid UTF-8 byte and newline becomes "?".
func OneLine(s string) string { return Text(s, false) }

// MultiLine is like OneLine but keeps newlines.
func MultiLine(s string) string { return Text(s, true) }

// Text implements OneLine and MultiLine.
func Text(s string, keepNewlines bool) string { return text(s, keepNewlines, true) }

// Strip is s as typed or pasted input: what OneLine would replace is dropped
// instead of marked, and so are tabs and the Unicode line separators, so the
// result is one run of printable characters.
func Strip(s string) string {
	s = text(s, false, false)
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == 0x2028 || r == 0x2029 {
			return -1
		}
		return r
	}, s)
}

// Clip is s cut to at most n runes (no marker).
func Clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:max(n, 0)])
}

// text implements Text and Strip; repl says whether a rejected character
// leaves a "?" (Text) or nothing (Strip).
func text(s string, keepNewlines, repl bool) string {
	mark := func(b *strings.Builder) {
		if repl {
			b.WriteByte('?')
		}
	}
	clean := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 && c != '\t' && (c != '\n' || !keepNewlines) || c >= 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			mark(&b)
			i += max(size, 1)
		case r == 0x1b: // ESC
			i = skipEscape(s, i+1)
		case r == 0x9b: // 8-bit CSI
			i = skipCSI(s, i+size)
		case r == 0x9d || r == 0x90 || r == 0x98 || r == 0x9e || r == 0x9f: // 8-bit OSC, DCS, SOS, PM, APC
			i = skipString(s, i+size)
		case r == '\t' || (r == '\n' && keepNewlines):
			b.WriteRune(r)
			i += size
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || spoofing(r):
			mark(&b)
			i += size
		default:
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

// spoofing reports whether r is a bidi control or a zero-width character.
// Neither rewrites the screen, but both let text read differently from
// what it is: a PR URL or a reason reordered or split by something the
// operator can't see.
func spoofing(r rune) bool {
	switch {
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069: // embeddings, overrides, isolates
		return true
	case r == 0x200e || r == 0x200f || r == 0x061c: // LRM, RLM, ALM
		return true
	case r >= 0x200b && r <= 0x200d, r == 0x2060, r == 0xfeff: // zero-width space, non-joiner, joiner; word joiner; BOM
		return true
	case r == 0x00ad, r == 0x034f, r == 0x061c, r == 0x115f || r == 0x1160, r == 0x17b4 || r == 0x17b5, r == 0x180e,
		r >= 0x2061 && r <= 0x2064, r == 0x3164, r == 0xffa0: // soft hyphen, grapheme joiner, Hangul fillers, invisible operators
		return true
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef: // variation selectors
		return true
	case r >= 0xe0000 && r <= 0xe007f: // tag characters (invisible ASCII smuggling)
		return true
	}
	return false
}

// skipEscape returns the index just past the escape sequence whose ESC
// ended at i.
func skipEscape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return skipCSI(s, i+1)
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: strings ended by ST or BEL
		return skipString(s, i+1)
	}
	// Other escapes: optional intermediates (0x20-0x2f), then one final byte.
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		i++
	}
	return i
}

// skipCSI returns the index past a CSI sequence's parameters (0x30-0x3f),
// intermediates (0x20-0x2f) and final byte (0x40-0x7e), starting at i.
func skipCSI(s string, i int) int {
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
		i++
	}
	if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
		i++
	}
	return i
}

// skipString returns the index past a control string starting at i: up to
// and including BEL, ESC \ or the 8-bit ST, or the end of s.
func skipString(s string, i int) int {
	for i < len(s) {
		switch {
		case s[i] == 0x07:
			return i + 1
		case s[i] == 0x1b:
			if i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
			return i + 1
		case strings.HasPrefix(s[i:], "\u009c"):
			return i + len("\u009c")
		}
		i++
	}
	return i
}
