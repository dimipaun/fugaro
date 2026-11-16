package cli

import (
	"context"

	"github.com/spf13/cobra"
)

// runAfterWrite is a no-op until N3 implements the in-process stage
// (docs/contracts/config-ownership.md, D2602). The stub lives in its own file
// so that the real hook is a deletion and not an edit: N3's init in
// write_publish.go is initialized after this one — the toolchain presents a
// package's files in name order — so the real function is what every command
// calls from the moment N3 lands, and I1 deletes this file, whose absence is
// what TestNoStubSurvives reads.
func init() {
	runAfterWrite = func(context.Context, *cobra.Command, afterWriteChange) error { return nil }
}
