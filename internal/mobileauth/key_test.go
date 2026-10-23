package mobileauth

import (
	"strings"
	"testing"
)

// TestNodeKeyCarriesNoByteAKeyMayNot: the encoding's whole reason is that a
// Realtime Database key may not carry ".", "$", "#", "[", "]" or "/" and may
// not be a control byte (internal/rtdb's validPath states the same set), and an
// address is full of dots.
func TestNodeKeyCarriesNoByteAKeyMayNot(t *testing.T) {
	for _, address := range []string{
		"dimi@example.com",
		"a/b#c$d[e]@example.com",
		"dimi@exämple.com",
		"dimi@example.com\n",
		"dimi paun@example.com",
	} {
		key, err := EncodeNodeKey(address)
		if err != nil {
			t.Fatalf("EncodeNodeKey(%q) = %v", address, err)
		}
		if strings.ContainsAny(key, ".$#[]/") || strings.ContainsFunc(key, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			t.Errorf("EncodeNodeKey(%q) = %q, which carries a byte a Realtime Database key may not", address, key)
		}
	}
}

// TestDecodeNodeKeyRefusesAKeyItDidNotMake: decode is the exact inverse of
// encode, so a key carrying a literal ".", a redundant escape, a broken escape
// or an empty value is refused rather than read as though this package had
// written it. A key that decodes to something is therefore one this package
// made, and a reader never has to guess.
func TestDecodeNodeKeyRefusesAKeyItDidNotMake(t *testing.T) {
	cases := []struct {
		name, key string
	}{
		{"an empty key", ""},
		{"a literal dot", "dimi@example.com"},
		{"a literal slash", "a/b"},
		{"a literal square bracket", "a[b]"},
		{"a redundant escape for a literal byte", "dimi@example%2Ecom%41"},
		{"an escape with one hex digit", "dimi%2"},
		{"a truncated escape", "dimi%"},
		{"an escape with a non-hex byte", "dimi%2Gcom"},
		{"a lower-case hex escape", "dimi%2ecom"},
	}
	for _, c := range cases {
		if back, err := DecodeNodeKey(c.key); err == nil {
			t.Errorf("%s: DecodeNodeKey(%q) = %q, want an error", c.name, c.key, back)
		}
	}
}

// TestNodeKeyRefusesWhatAKeyCannotHold: a value that encodes past the key's 768
// bytes is refused rather than written as a key the client will reject, and an
// empty string is not a key at all.
func TestNodeKeyRefusesWhatAKeyCannotHold(t *testing.T) {
	if _, err := EncodeNodeKey(""); err == nil {
		t.Error("EncodeNodeKey(\"\") = nil error, want a refusal: an empty string is not a key")
	}
	// 256 dots: 768 bytes encoded, exactly the bound; 257 dots is one byte over.
	if key, err := EncodeNodeKey(strings.Repeat(".", 256)); err != nil {
		t.Errorf("EncodeNodeKey(256 dots) = %v, want the 768-byte bound to hold", err)
	} else if len(key) != 768 {
		t.Errorf("EncodeNodeKey(256 dots) is %d bytes, want 768", len(key))
	}
	if _, err := EncodeNodeKey(strings.Repeat(".", 257)); err == nil {
		t.Error("EncodeNodeKey(257 dots) = nil error, want a refusal: it encodes past 768 bytes")
	}
	// The bound is on the key, not on the input: literal bytes cost one byte
	// each, so a 300-byte (or 768-byte) input of them is a legal key, and it is
	// the reason the doc states the encoded bound rather than an input one.
	for _, n := range []int{300, 768} {
		key, err := EncodeNodeKey(strings.Repeat("a", n))
		if err != nil {
			t.Errorf("EncodeNodeKey(%d literal bytes) = %v, want a legal key", n, err)
			continue
		}
		if key != strings.Repeat("a", n) {
			t.Errorf("EncodeNodeKey(%d literal bytes) is %d bytes, want them unchanged", n, len(key))
		}
		back, err := DecodeNodeKey(key)
		if err != nil || back != key {
			t.Errorf("DecodeNodeKey(EncodeNodeKey(%d literal bytes)) = %q, %v; want the input back", n, back, err)
		}
	}
	if _, err := EncodeNodeKey(strings.Repeat("a", 769)); err == nil {
		t.Error("EncodeNodeKey(769 literal bytes) = nil error, want a refusal: 769 bytes is over the key bound")
	}
	if _, err := DecodeNodeKey(strings.Repeat("A", 769)); err == nil {
		t.Error("DecodeNodeKey(769 bytes) = nil error, want a refusal")
	}
}
