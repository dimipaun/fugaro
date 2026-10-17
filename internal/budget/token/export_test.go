package token

import "io"

// SetKeyReader replaces the reader NewKey draws from, for the test's
// duration; restore puts crypto/rand back.
func SetKeyReader(r io.Reader) (restore func()) {
	prev := keyReader
	keyReader = r
	return func() { keyReader = prev }
}
