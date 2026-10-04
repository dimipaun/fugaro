package shellword

import "testing"

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
