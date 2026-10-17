package token_test

import (
	"bytes"
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
