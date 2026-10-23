// Package shellword quotes a value as one shell word, for the commands fugaro
// prints to be pasted into a terminal.
package shellword

import "strings"

// safeBytes are the bytes a shell word may hold unquoted: the letters and
// digits, and the punctuation no shell of the family this program's commands
// are pasted into reads in a way of its own. Every other byte is one a shell
// does something with — a separator, a redirection, a substitution, a quote, a
// glob, an expansion, or a byte with no printable form — and the one set of
// bytes is what this package's two readers agree on: Quote puts a value
// holding one in single quotes, and a printer that may not quote, because its
// text is compared byte for byte by the command it is pasted into
// (internal/infra/tf's identity, printed for fugaro allow-delete), escapes it
// instead.
const safeBytes = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_./:@%+=,-"

// SafeByte reports whether c may stand unquoted in a shell word this program
// prints: Quote quotes a value holding any byte it answers false for, and an
// identity escapes one instead. It is the set above and the only place the set
// is written.
func SafeByte(c byte) bool { return strings.IndexByte(safeBytes, c) >= 0 }

// safe is s as a shell word that needs no quotes: every byte of it is one
// SafeByte accepts, and it does not begin with '-', which is a safe byte and
// still not safe to begin an argument with, because the command it is an
// argument of reads it as an option.
func safe(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !SafeByte(s[i]) {
			return false
		}
	}
	return true
}

// Quote is s as one shell word: unchanged when it is made of safe characters
// and cannot be read as an option (no leading '-'), else in single quotes. A
// branch or repository name may hold ; $ ( | & ' and more, and the text is
// printed to be copied into a shell.
func Quote(s string) string {
	if safe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
