package cli

import "github.com/spf13/cobra"

// newLsTombstone keeps the 0.6 name answering for one release with the new
// one (design generic-tool §5, G17). It is deleted in 0.8.0.
func newLsTombstone() *cobra.Command {
	return &cobra.Command{
		Use:                "ls",
		Hidden:             true,
		DisableFlagParsing: true, // any old flag reaches the message, not a flag error
		RunE: func(*cobra.Command, []string) error {
			return userErr("fugaro ls was renamed in 0.7.0: use fugaro runs ls (same flags)")
		},
	}
}
