package token_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// TestNewKeyIsAFreshKeyOfTheSealsSize: the launcher's key is KeySize bytes
// from crypto/rand, one per launch (§7.1, D17), and its one spelling parses
// back to the same bytes. Two draws are never the same key: a key reused for
// a second launch would put one key in front of two executions (D17).
func TestNewKeyIsAFreshKeyOfTheSealsSize(t *testing.T) {
	seen := map[string]bool{}
	for range 64 {
		k, err := token.NewKey()
		if err != nil {
			t.Fatal(err)
		}
		if len(k) != token.KeySize {
			t.Fatalf("NewKey is %d bytes, want %d", len(k), token.KeySize)
		}
		if bytes.Equal(k, make([]byte, token.KeySize)) {
			t.Fatal("NewKey returned the zero key")
		}
		enc := token.EncodeKey(k)
		if seen[enc] {
			t.Fatal("NewKey returned the same key twice")
		}
		seen[enc] = true
		back, err := token.ParseKey(enc)
		if err != nil || !bytes.Equal(back, k) {
			t.Fatalf("ParseKey(EncodeKey(k)) = %v, %v", back, err)
		}
	}
}

// shortReader hands out its bytes one at a time, as a reader is allowed to,
// and counts what it gave; it fails once it runs out.
type shortReader struct {
	src []byte
	n   int
}

func (r *shortReader) Read(p []byte) (int, error) {
	if r.n >= len(r.src) {
		return 0, errors.New("out of entropy")
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.src[r.n]
	r.n++
	return 1, nil
}

// TestNewKeyFillsAllOfItsBytes: every one of the key's KeySize bytes comes
// from the random source, even from a reader that returns a byte per call, so
// no part of the key is left at zero (a 16-byte key in a 32-byte slice would
// be AES-256 in name only); and a source that runs out before KeySize bytes is
// an error, never a key.
//
// Mutation (run, restored): replace io.ReadFull with a single Read in NewKey —
// the first subtest gets one byte and fails.
func TestNewKeyFillsAllOfItsBytes(t *testing.T) {
	t.Run("one byte per read", func(t *testing.T) {
		src := make([]byte, token.KeySize)
		for i := range src {
			src[i] = byte(i + 1)
		}
		r := &shortReader{src: src}
		defer token.SetKeyReader(r)()
		k, err := token.NewKey()
		if err != nil {
			t.Fatal(err)
		}
		if r.n != token.KeySize || !bytes.Equal(k, src) {
			t.Fatalf("NewKey read %d bytes and returned %x, want all %d of %x", r.n, k, token.KeySize, src)
		}
	})
	t.Run("a source that runs out", func(t *testing.T) {
		defer token.SetKeyReader(&shortReader{src: make([]byte, token.KeySize/2)})()
		if k, err := token.NewKey(); err == nil || k != nil {
			t.Fatalf("NewKey = %x, %v, want an error and no key", k, err)
		}
	})
	t.Run("the last half varies", func(t *testing.T) {
		seen := map[string]bool{}
		for range 16 {
			k, err := token.NewKey()
			if err != nil {
				t.Fatal(err)
			}
			seen[string(k[token.KeySize/2:])] = true
		}
		if len(seen) < 16 {
			t.Fatal("the key's last 16 bytes repeat across draws: they are not drawn")
		}
	})
}
