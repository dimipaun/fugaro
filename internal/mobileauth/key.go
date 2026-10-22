package mobileauth

import (
	"fmt"
	"strings"
)

// maxNodeKey is the longest Realtime Database key: the same 768 the tree's own
// path check enforces (internal/rtdb's validPath), so a key this package makes
// is one the client that writes it will accept.
const maxNodeKey = 768

// EncodeNodeKey returns s as a Realtime Database key: every byte outside the
// small literal set below is written as %XX (upper-case hex), and the literal
// bytes are written as they are.
//
// The encoding exists because a key may not carry ".", "$", "#", "[", "]" or
// "/" and may not be a control byte (internal/rtdb's validPath states the same
// set), while an address is full of dots — so an allowlist map keyed by an
// address, or by a domain, needs a spelling that is legal as a key and can be
// read back. Its two properties are the point:
//
//   - It is injective: two different addresses never share a key, because every
//     byte that could be ambiguous ("%" above all, the escape's own byte) is
//     escaped, and DecodeNodeKey is exact.
//   - It round-trips: DecodeNodeKey(EncodeNodeKey(s)) == s for every s it
//     accepts, so a key read back from the node is the address the entry names
//     and not a value somebody has to guess at.
//
// What it accepts, stated: any non-empty string whose ENCODING is at most 768
// bytes — the same bound the tree's path check applies, because the encoded key
// is what the database holds. That is at most 768 bytes of input when every byte
// is written literally, and 256 bytes when every byte is escaped (three bytes
// each); an address or a domain is far shorter than either (NormalizeEmail's own
// bound is 254). The bound is on the key rather than on the input on purpose: a
// 300-byte input of literal bytes is a legal 300-byte key, and refusing it would
// refuse a key the database accepts.
//
// It does not validate what s is: the canonical form is what a writer encodes
// (NormalizeEmail for an address, NormalizeDomain for a domain), and it is the
// canonical form that makes one person one key. An empty string is refused
// because an empty key is not a key.
func EncodeNodeKey(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("mobileauth: an empty string is not a node key")
	}
	key := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		if keyLiteral(b) {
			key = append(key, b)
			continue
		}
		key = append(key, '%', hexDigit(b>>4), hexDigit(b&0xf))
	}
	if len(key) > maxNodeKey {
		return "", fmt.Errorf("mobileauth: %q encodes to %d bytes, over the %d a Realtime Database key holds", s, len(key), maxNodeKey)
	}
	return string(key), nil
}

// DecodeNodeKey returns the string key was made from, and refuses every key
// EncodeNodeKey would not have produced: an empty key, a key over 768 bytes, a
// "%" not followed by two hex digits, a literal byte outside the encoding's
// literal set (so a key carrying "." or "/" — one hand-written in the console —
// is refused rather than read as though this package had made it), and an
// escape for a byte the encoding writes literally ("%41" for "A"). It is
// therefore the exact inverse: every key it accepts has one encoding and one
// address.
//
// Callers do not have to trust its answer either: the address it returns is an
// address only after NormalizeEmail has accepted it, and a caller that reads an
// entry back should pass it through the same door as one it was handed.
func DecodeNodeKey(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("mobileauth: an empty string is not a node key")
	}
	if len(key) > maxNodeKey {
		return "", fmt.Errorf("mobileauth: the key is %d bytes long, over the %d a Realtime Database key holds", len(key), maxNodeKey)
	}
	out := make([]byte, 0, len(key))
	for i := 0; i < len(key); i++ {
		b := key[i]
		if b != '%' {
			if keyLiteral(b) {
				out = append(out, b)
				continue
			}
			return "", fmt.Errorf("mobileauth: the key %q carries %q at offset %d, which this encoding writes as an escape", key, b, i)
		}
		if i+2 >= len(key) {
			return "", fmt.Errorf("mobileauth: the key %q ends with a %q escape that is not two hex digits", key, "%")
		}
		hi, ok := hexValue(key[i+1])
		if !ok {
			return "", fmt.Errorf("mobileauth: the key %q has %q in an escape at offset %d", key, key[i+1], i+1)
		}
		lo, ok := hexValue(key[i+2])
		if !ok {
			return "", fmt.Errorf("mobileauth: the key %q has %q in an escape at offset %d", key, key[i+2], i+2)
		}
		b = hi<<4 | lo
		if keyLiteral(b) {
			return "", fmt.Errorf("mobileauth: the key %q escapes %q at offset %d, which this encoding writes literally", key, b, i)
		}
		out = append(out, b)
		i += 2
	}
	return string(out), nil
}

// keyLiteral reports whether b is written literally in an encoded key. The set
// is deliberately small — letters, digits and "@-_!" — so that every byte the
// database forbids, every byte a shell or a URL would have to quote, and the
// escape's own "%" are all escaped. What is left is readable: the shape of an
// address survives encoding ("dimi@example.com" becomes
// "dimi%2Eexample%2Ecom").
func keyLiteral(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return strings.IndexByte("@-_!", b) >= 0
}

// hexDigit is the upper-case hex digit for a nibble.
func hexDigit(n byte) byte { return "0123456789ABCDEF"[n&0xf] }

// hexValue is the value of an upper-case hex digit.
func hexValue(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}
