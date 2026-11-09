package cli

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/issues"
	"github.com/spf13/cobra"
)

// issuesPendingID is doctor's one line for the issues this machine saved and
// has not filed. It is an item of the default view — the unfiled issues are
// work, and the fix is one command — but a purely informational one: nothing
// about the installation is wrong, so it never fails doctor, --strict
// included.
const issuesPendingID = "issues-pending"

// doctorIssuesCheck is doctor's line for the issues `fugaro troubleshoot` saved
// here and nothing has filed yet: still pending, or never uploaded to the
// project's shared store, so the team cannot see it (spec 2, D1725's one
// doctor line).
//
// It prints nothing at all when there is nothing waiting, which is the
// "doctor is one screen" rule: a machine with no unfiled issue stays at its
// one ok line, and a machine that never ran troubleshoot has no line and no
// count. A machine with no state directory at all is the same case — there is
// nowhere an issue could have been saved — so it is silent rather than a
// finding about a file nobody could have written.
//
// The caller adds the line only when the tree carries the commands its fix
// names: this task lands before them (T-IS5 onwards), and doctor must never
// send the reader to a command that answers "unknown command" — see
// doctorIssuesLineExists.
//
// A store that cannot be read is the one state it reports as unread: the
// number of unfiled issues is then unknown, not zero, and an unknown filed as
// a measurement is the silent pass the classes exist to keep out of the view
// (doctorClassOf).
func doctorIssuesCheck(getenv func(string) string, goos string) *doctorCheck {
	store, err := issues.OpenStore(getenv, goos)
	if err != nil {
		return nil
	}
	all, err := store.List()
	if err != nil {
		return &doctorCheck{ID: issuesPendingID, Severity: "info", unread: true,
			Problem: "the issues this machine saved could not be read (" + err.Error() + "), so how many are unfiled is unknown",
			Fix:     "run " + selfCommand() + " issue list"}
	}
	unfiled := 0
	for _, i := range all {
		if i.Status == issues.StatusPending || !i.Uploaded {
			unfiled++
		}
	}
	if unfiled == 0 {
		return nil
	}
	problem, them := "1 saved issue is not filed or uploaded yet", "it"
	if unfiled > 1 {
		problem, them = fmt.Sprintf("%d saved issues are not filed or uploaded yet", unfiled), "them"
	}
	return &doctorCheck{ID: issuesPendingID, Severity: "info", Problem: problem,
		Fix: "run " + selfCommand() + " issue list to see " + them + ", then " + selfCommand() + " issue submit <number> to file one"}
}

// doctorIssuesLineExists reports whether this build's tree carries the command
// the line's fix names: `issue submit`, which T-IS5 onwards registers. This
// task lands before the issue commands exist, and a line whose only advice is a
// command that exits "unknown command" is worse than no line, so doctor skips
// the check until the tree can run what the fix says. The line appears on its
// own, with no further change here, the moment those commands do.
func doctorIssuesLineExists(cmd *cobra.Command) bool {
	for _, parent := range cmd.Root().Commands() {
		if parent.Name() != "issue" {
			continue
		}
		for _, child := range parent.Commands() {
			if child.Name() == "submit" {
				return true
			}
		}
	}
	return false
}
