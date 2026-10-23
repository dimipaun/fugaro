package shellword

import (
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"main":         "main",
		"acme/app":     "acme/app",
		"-x":           `'-x'`,
		"a b":          `'a b'`,
		"it's":         `'it'\''s'`,
		"a;b":          `'a;b'`,
		"$(id)":        `'$(id)'`,
		"user:a@b.com": "user:a@b.com",
		"":             "''",
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSafeByteIsTheBytesAShellReads: the set is the one Quote quotes by and the
// one internal/infra/tf's identity escapes by, and it is written once — so it
// is pinned here against a literal list of the bytes a shell reads in a way of
// its own, in the two directions: every byte of that list is unsafe, and every
// byte SafeByte calls safe is one Quote leaves in a word (which the loop below
// asks of Quote itself, so the two cannot drift apart).
//
// The list is the shell's own syntax rather than a spelling of this package's
// set: the separators and the pipe, the redirections and the ampersand, the
// substitution and the quote characters, the glob characters and the braces,
// the comment, the history expansion, the escape itself, the tilde, and every
// byte with no printable form — a space and a tab among them.
func TestSafeByteIsTheBytesAShellReads(t *testing.T) {
	const reads = " \t\n;|&<>(){}[]*?$`'\"\\#!~" + "\x00\x1b\x7f\x80\xff"
	for i := 0; i < len(reads); i++ {
		if SafeByte(reads[i]) {
			t.Errorf("SafeByte(%q) = true, want false: a shell reads it in a way of its own", reads[i])
		}
		if word := "a" + string(reads[i]) + "b"; Quote(word) == word {
			t.Errorf("Quote(%q) = %q, want it quoted", word, word)
		}
	}
	// And the other direction, over the whole byte range: a word of bytes
	// SafeByte accepts is printed bare, so the set is exactly what Quote
	// leaves alone (a leading '-' excepted, which is not a byte's property).
	for b := 0; b < 256; b++ {
		c := string(rune(b))
		if strings.Contains(reads, c) {
			continue
		}
		if got := Quote("a" + c + "b"); (got == "a"+c+"b") != SafeByte(byte(b)) {
			t.Errorf("Quote(\"a%sb\") = %q and SafeByte(%#x) = %v: the two disagree about the byte", c, got, b, SafeByte(byte(b)))
		}
	}
}
