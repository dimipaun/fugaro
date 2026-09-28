package cli

import (
	"strings"
	"unicode/utf8"
)

// Terminal-safe text. Much of what the views print
// comes from objects the job's service account, and so the agent, can
// write (result.json, launch.json, transcripts) or from Cloud Logging. Printed
// raw, an escape sequence in such a string could rewrite the operator's
// screen (CSI), plant a hyperlink (OSC 8) or set the clipboard (OSC 52).
// Every human-format field of such origin goes through oneLine or
// multiLine; JSON output is escaped by encoding/json instead.

// oneLine is s made safe for one line of a terminal: ESC, CSI, OSC, DCS,
// SOS, PM and APC sequences (7- and 8-bit) are dropped, and every other C0
// control but tab, DEL, C1 control, bidi control, zero-width character
// and invalid UTF-8 byte becomes "?". A newline becomes "?" too, so a
// field can't forge a line of its own.
func oneLine(s string) string { return safeText(s, false) }

// ErrorText is err's message made safe for the terminal, for the command's
// top-level error line. An error can quote a stored or remote value, so
// its controls go the way a view's do; its newlines stay, because some
// errors list several problems.
func ErrorText(err error) string { return multiLine(err.Error()) }

// multiLine is like oneLine, but keeps newlines, for blocks printed as
// several lines (the agent's message).
func multiLine(s string) string { return safeText(s, true) }

func safeText(s string, keepNewlines bool) string {
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
			b.WriteByte('?')
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
			b.WriteByte('?')
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
