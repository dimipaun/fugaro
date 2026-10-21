package cli

import (
	"github.com/spf13/cobra"
)

// retired.go is what `fugaro init` is now (task W1.2 of
// docs/setup-wizard/implementation-plan.md; decision D2 of the
// reconciliation's §5, answered by the owner on 2026-10-20 as recommended).
//
// The command became two, because it always was two things: `fugaro setup`
// stands an installation up, or adopts one, and asks the wizard's questions;
// `fugaro update` re-converges what is already there after a CLI upgrade. An
// alias is the answer that was not taken: it would have to pick one of those
// meanings for the user, and the user typing the old name is exactly the user
// who cannot yet tell them apart. So the tombstone's whole job is to say both
// names and exit non-zero. It applies nothing — it holds no flags and
// constructs no client — and it is hidden, because it is not a command this
// CLI offers any more.

// retirementMessage is the one sentence the retired `fugaro init` says, whatever
// came after it on the command line. It is a constant because the tombstone
// and its test both quote it, and because the plan's census (W0.2) exempts
// the files that name the command only to retire it.
//
// It is retirementMessage and not retirementText because the census's own
// exemption class for these files is spelled `retirementText`
// (docs_live_init_census_test.go, W0.2) and the two share this package: the
// census keeps the name the plan gave it, and this one says what it is.
const retirementMessage = "`fugaro init` is now `fugaro setup` (first run) and `fugaro update` (after a CLI upgrade)"

// newRetiredInitCmd is the hidden `init`: one message, two names, exit 1.
//
// DisableFlagParsing is the load-bearing line. The old command had ~44 flags
// and a checkout path, and every one of those command lines is one a user may
// still type — `fugaro init --yes --repo .` above all. With parsing on, the
// first thing such a run would read is cobra's "unknown flag: --yes", which
// says nothing about where the command went; with it off, every shape of the
// old line reaches RunE and reads the same sentence. The exit code is a user
// error's (1), the same as any other refusal, so a script that ran the old
// name fails loudly rather than continuing with nothing converged.
func newRetiredInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "init",
		Short:              "Retired: fugaro setup on a first run, fugaro update after a CLI upgrade",
		Long:               retirementMessage + ".\n\nIt applies nothing: it names the two commands that replaced it and exits 1.",
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return userErr("%s", retirementMessage)
		},
	}
}
