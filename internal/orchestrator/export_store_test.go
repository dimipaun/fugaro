package orchestrator

// This file is compiled into this package's tests and into nothing else, and
// it is separate from export_test.go because O0 wrote that one: two writers of
// one table is the drift the plan's file list avoids.
//
// It exposes the two bounds a lost race is answered within, so a test asserts
// against the number the code uses: a copy of one written into a test beside
// the constant it is meant to be would go on passing after the constant moved.
const (
	// SaveAttempts bounds the writes one Save makes under a moving ETag.
	SaveAttempts = saveAttempts
	// LeaseAttempts bounds the reads and writes one claim, beat or release
	// makes under a moving ETag.
	LeaseAttempts = leaseAttempts
)
