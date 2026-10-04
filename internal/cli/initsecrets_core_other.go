//go:build !unix

package cli

// noCoreDumps has no equivalent here.
func noCoreDumps() (restore func()) { return func() {} }
