package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// managed_require.go is D1541's fence in one place: what a command that
// changes a managed project says to a project that is not managed, and the one
// command that turns one into a managed project.
//
// The spec is "Fugaro only supports managed projects", and under the no-prompt
// policy a command may not fall back to asking: a mutating command on a project
// that is not managed refuses and prints the one command that flags it managed,
// with the GCP project ID given as an ARGUMENT (D1541, answered 2026-11-07:
// consent by argument, not by typing at a prompt). The argument is the whole of
// it. This file is the single writer of that sentence, so the command a refusal
// names — and the way it is named — cannot drift between the commands that
// refuse this way.
//
// Which commands those are is declared in root.go's managedMutatingCommands, by
// cobra's own command path, and applied to the tree as the annotation below:
// the command's own opening clause (what it declines, and what it is) comes
// from that table, so a command that refuses this way says what it is doing in
// one place rather than in the middle of a sentence written here.
const managedRequiresAnnotation = "fugaro.io/requires-managed-project"

// managedFlagCommand is the one command that makes an existing project managed,
// spelled with the project it would flag. The budget flags carry placeholders
// and not a number: the amount is the owner's own decision, and the command
// requires it with no default (D64), so a sentence that printed a figure would
// be choosing one for them.
func managedFlagCommand(gcpProject string) string {
	return fmt.Sprintf("%s managed flag %s --budget N --budget-currency CCC", selfCommand(), safetext.Field(gcpProject))
}

// managedRequired is the refusal of a command that may not change a project
// that is not managed: the project, the managed read's own reason (so the
// refusal says which half is missing and not only that something is), what the
// command declines to do, and the one command that flags the project managed.
//
// cmd is the command refusing, and its annotation is the opening clause. A
// command that refuses this way without being declared in
// managedMutatingCommands is a sentence with a hole in it rather than a wrong
// answer, so the refusal still holds and says what it is.
func managedRequired(cmd *cobra.Command, gcpProject, reason string) error {
	what := ""
	if cmd != nil {
		what = cmd.Annotations[managedRequiresAnnotation]
	}
	if what == "" {
		what = "nothing was written: this command changes a managed project"
	}
	return userErr("%s: %s is not managed (%s), and no flag of this command turns an installation that is not managed into one: the command that flags an existing project managed is `%s`, run by its owner at their own terminal — it takes the GCP project ID as its argument, so the argument is the declaration itself and nothing is typed at a prompt (D1541)",
		what, safetext.Field(gcpProject), safetext.Field(reason), managedFlagCommand(gcpProject))
}
