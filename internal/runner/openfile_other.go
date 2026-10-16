//go:build !unix

package runner

import "os"

// oNoFollow is nothing where there is no O_NOFOLLOW; O_EXCL is what refuses a
// path something is already at.
const oNoFollow = 0

// openNoFollow is os.Open where there is no O_NOFOLLOW: the fstat the caller
// makes on the returned file is the only guard, and the bounded copy is what
// keeps a source that does not end from running on.
func openNoFollow(path string) (*os.File, error) { return os.Open(path) }
