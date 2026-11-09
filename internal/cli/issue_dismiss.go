package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/issues"
)

// newIssueDismissCmd builds `fugaro issue dismiss`: set one saved issue aside,
// so nothing is filed for it.
func newIssueDismissCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dismiss REF",
		Short: "Mark an issue as not worth filing",
		Long: "Mark one saved issue as not worth filing.\n\n" +
			"REF is the issue's number — `7`, or the `#0007` a listing prints — or\n" +
			"its local ID `local-<run stem>`, the same argument `show` takes. The\n" +
			"status is written in the issue's own file and nothing else changes: the\n" +
			"record stays where it is, so a listing still shows what was set aside\n" +
			"and who did it, and nothing is filed.\n\n" +
			"A coding agent's session may not dismiss an issue: a steered diagnosis\n" +
			"could otherwise bury a real bug, so a person sets one aside (D1730).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runIssueDismiss(cmd, args[0]) },
	}
	return cmd
}

func runIssueDismiss(cmd *cobra.Command, ref string) error {
	// The guard `recipes publish` uses: an agent-set marker refuses whatever the
	// terminal, and the IDE extension's marker alone refuses only without one,
	// so a person in their own IDE terminal may dismiss (D1730, F10).
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was dismissed: %s", initflow.AgentRefusal(m))
	}
	store, err := openIssueStore()
	if err != nil {
		return err
	}
	ref = issueRefArg(ref)
	i, err := store.Get(ref)
	if err != nil {
		return userErr("nothing was dismissed: %v", err)
	}
	if err := store.SetStatus(ref, issues.StatusDismissed); err != nil {
		return userErr("nothing was dismissed: %v", err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Dismissed %s. Nothing is filed for it.\n", issueRef(i))
	return err
}
