//go:build !unix

package blobx

import "os"

// OpenSource is os.Open where there is no O_NOFOLLOW: the fstat PutFile makes
// on the returned file is the only guard, and the copy bounded by that size is
// what keeps a source that does not end from running on.
func OpenSource(path string) (*os.File, error) { return os.Open(path) }
