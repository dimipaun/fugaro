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
			"A number also names a teammate's issue, which this machine has no file\n" +
			"of: the rows `list` prints are the whole team's, so a reference that is\n" +
			"a number and no file here is read from the project's store and set\n" +
			"aside there (F23). A store that cannot be reached is then the refusal\n" +
			"rather than a line, there being nothing here to set aside instead.\n\n" +
			"An issue that has already been filed is refused: it is open on GitHub,\n" +
			"and setting it aside here would not close it. So is one a release has\n" +
			"fixed: the fix is in that release and the record says which, and a bug\n" +
			"that came back is a new upload rather than a dismissal of the fix that\n" +
			"did not hold.\n\n" +
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
	// so a person in their own IDE terminal may dismiss (D1730, F10). The
	// refusal names the command a person runs: a session that only read "not
	// through a coding agent" would not know which command to hand back.
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was dismissed: fugaro issue dismiss is a person's own decision, because a steered diagnosis could otherwise bury a real bug: %s", initflow.AgentRefusal(m))
	}
	ctx := cmd.Context()
	store, err := openIssueStore()
	if err != nil {
		return err
	}
	ref = issueRefArg(ref)
	i, err := store.Get(ref)
	// A file here that carries the number for another project is not the issue
	// the number names (F8b): the number is this project's, and the row a listing
	// prints for it is this project's issue. It is passed over with a line, which
	// is the refusal too when the project's store has no record of the number.
	if err == nil && issueOtherProjects(i, issueProjectName(ctx)) {
		err = errors.New(issueRef(i) + " here is another project's record")
	}
	// dismiss marks an issue as not worth submitting (spec 4), so it has nothing
	// to say about one that is already filed: setting `dismissed` over
	// `submitted` would drop the record's own statement that the issue is open on
	// GitHub and print that nothing is filed for it, which is then false. The
	// record is left as it is.
	if err == nil && i.Status == issues.StatusSubmitted {
		filed := ""
		if u := issueURL(i.URL); u != "" {
			filed = " at " + u
		}
		return userErr("nothing was dismissed: %s was already filed%s, and dismiss marks an issue as not worth submitting", issueRef(i), filed)
	}
	// A resolved issue is the other thing dismiss has nothing to say about
	// (F9, F14): the record's own `resolved_in` says a release fixed the bug,
	// and setting `dismissed` over `resolved` would drop that and print that
	// nothing is filed for an issue that was. A bug that came back is not this
	// command's either — the duplicate check attaches it to the resolved issue
	// as a regression, and the fix that did not hold stays on the record.
	if err == nil && i.Status == issues.StatusResolved {
		fixed := ""
		if i.ResolvedIn != "" {
			fixed = " in " + oneLine(i.ResolvedIn)
		}
		return userErr("nothing was dismissed: %s was fixed%s, and a fixed issue is not buried; if it came back, that is a new upload", issueRef(i), fixed)
	}
	// A number is the project's, so this is the half of the command that reaches
	// the store: a reference the folder here does not hold is the row of a
	// teammate's issue the listing printed (F23), and a numbered issue of this
	// machine's is set aside there as well as here (F13). An issue with no number
	// has never been uploaded, so there is no project copy of it and the file
	// here is the whole decision — and a local ID names one only this machine
	// has, so a reference that is not a number is answered here or not at all,
	// with nothing opened to ask.
	if _, numbered := issueNumber(ref); numbered {
		sh := openIssueShared(ctx, "")
		defer sh.Close()
		if sh.installation != "" {
			store.Installation = sh.installation
		}
		if err != nil {
			return dismissTeammates(cmd, ctx, sh, ref, err)
		}
		// The project's copy is set aside first and this machine's file second:
		// the two have to agree, and the write that can fail is the one that
		// leaves the other as it was.
		if err := dismissShared(cmd, ctx, sh, i); err != nil {
			return userErr("nothing was dismissed: %v", err)
		}
	} else if err != nil {
		return userErr("nothing was dismissed: %v", err)
	}
	if err := store.SetStatus(ref, issues.StatusDismissed, time.Now()); err != nil {
		return userErr("nothing was dismissed: %v", err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Dismissed %s. Nothing is filed for it.\n", issueRef(i))
	return err
}

// dismissShared sets this machine's numbered issue aside in the project's store
// as well, where the store is the truth about an issue that has a number (F13):
// a teammate's listing then shows the dismissal, and nobody has to decide the
// same issue twice.
//
// A store that cannot be reached is a line and no error: the issue is on this
// machine, the person is at the terminal, and a network that is down must not
// stop them setting their own record aside — the line says the project's copy
// was not set aside, and running the command again once the store is back
// settles it. A store that answers and refuses is another matter — the issue is
// filed, or another teammate has already decided it — and that is an error,
// because the decision is then not this machine's to make.
func dismissShared(cmd *cobra.Command, ctx context.Context, sh *issueShared, i issues.Issue) error {
	reached, err := dismissInStore(ctx, sh, i)
	if !reached {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: the project's copy of "+issueRef(i)+" was not set aside: "+err.Error())
		return nil
	}
	return err
}

// dismissTeammates sets aside an issue this machine has no file of: the row of
// a teammate's issue that `list` printed, reached by its number (F23).
//
// The store is the whole of the issue here, so a store that cannot be reached
// is the refusal and not a line — there is nothing local to set aside instead —
// and the lines about what could not be read go out first, so the refusal is
// never the only thing a person has to go on.
func dismissTeammates(cmd *cobra.Command, ctx context.Context, sh *issueShared, ref string, why error) error {
	i, ok := sharedIssueRef(ctx, sh, ref)
	if !ok {
		for _, n := range sh.notes {
			fmt.Fprintln(cmd.ErrOrStderr(), n)
		}
		return userErr("nothing was dismissed: %v", why)
	}
	if _, err := dismissInStore(ctx, sh, i); err != nil {
		return userErr("nothing was dismissed: %v", err)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Dismissed %s. Nothing is filed for it.\n", issueRef(i))
	return err
}

// dismissInStore is the write itself. `reached` is false when the store could
// not be reached at all, which the caller with a file of its own turns into a
// line and the caller without one into the refusal.
func dismissInStore(ctx context.Context, sh *issueShared, i issues.Issue) (reached bool, err error) {
	if sh.bucket == nil {
		return false, errors.New(sh.why())
	}
	if err := issues.SetSharedStatus(ctx, sh.bucket, i.Number, issues.StatusDismissed, time.Now()); err != nil {
		return true, errors.New(oneLine(err.Error()))
	}
	return true, nil
}
