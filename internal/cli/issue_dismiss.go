package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

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
			"An issue is the project's, so one that has already been uploaded is set\n" +
			"aside in the project's store as well (F13): a teammate's listing shows\n" +
			"it as dismissed, and the decision does not have to be made again on\n" +
			"every machine. A store that cannot be reached is a line on stderr and\n" +
			"the dismissal here stands; a store that answers and refuses — the issue\n" +
			"is filed, or a teammate has decided it — refuses the command.\n\n" +
			"An issue that has already been filed is refused: it is open on GitHub,\n" +
			"and setting it aside here would not close it.\n\n" +
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
	ctx := cmd.Context()
	store, err := openIssueStore()
	if err != nil {
		return err
	}
	ref = issueRefArg(ref)
	i, err := store.Get(ref)
	if err != nil {
		return userErr("nothing was dismissed: %v", err)
	}
	// dismiss marks an issue as not worth submitting (spec 4), so it has nothing
	// to say about one that is already filed: setting `dismissed` over
	// `submitted` would drop the record's own statement that the issue is open on
	// GitHub and print that nothing is filed for it, which is then false. The
	// record is left as it is.
	if i.Status == issues.StatusSubmitted {
		filed := ""
		if u := issueURL(i.URL); u != "" {
			filed = " at " + u
		}
		return userErr("nothing was dismissed: %s was already filed%s, and dismiss marks an issue as not worth submitting", issueRef(i), filed)
	}
	// The project's copy is set aside first and this machine's file second (F13):
	// the two have to agree, and the write that can fail is the one that leaves
	// the other as it was. An issue with no number has never been uploaded, so
	// there is no project copy of it and the file here is the whole decision.
	if i.Number != 0 {
		note, err := dismissShared(ctx, i)
		if err != nil {
			return userErr("nothing was dismissed: %v", err)
		}
		if note != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), note)
		}
	}
	if err := store.SetStatus(ref, issues.StatusDismissed, time.Now()); err != nil {
		return userErr("nothing was dismissed: %v", err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Dismissed %s. Nothing is filed for it.\n", issueRef(i))
	return err
}

// dismissShared sets the issue aside in the project's store, where the store is
// the truth about an issue that has a number (F13): a teammate's listing then
// shows the dismissal, and nobody has to decide the same issue twice.
//
// A store that cannot be reached answers with a line and no error: the issue is
// on this machine, the person is at the terminal, and a network that is down
// must not stop them setting their own record aside — the line says the
// project's copy is still open, and running the command again once the store is
// back settles it. A store that answers and refuses is another matter — the
// issue is filed, or another teammate has already decided it — and that is an
// error, because the decision is then not this machine's to make.
func dismissShared(ctx context.Context, i issues.Issue) (string, error) {
	sh := openIssueShared(ctx, "")
	defer sh.Close()
	if sh.bucket == nil {
		return "note: the project's copy of " + issueRef(i) + " is still open: " + sh.why(), nil
	}
	if err := issues.SetSharedStatus(ctx, sh.bucket, i.Number, issues.StatusDismissed, time.Now()); err != nil {
		return "", errors.New(oneLine(err.Error()))
	}
	return "", nil
}
