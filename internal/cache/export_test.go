package cache

import (
	"context"
	"io"
)

// Extract is extract without a deadline, for the archive tests; Restore,
// the only production caller, passes its own context.
func Extract(r io.Reader, roots []string, maxBytes int64) error {
	return extract(context.Background(), r, roots, maxBytes)
}
