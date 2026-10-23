package cli

import (
	"github.com/spf13/cobra"
)

// update.go is `fugaro update`: the second door of the converge — the
// re-converge of an installation (or a checkout's repository) that already
// exists, which is what a CLI upgrade needs (task W1.2 of
// docs/setup-wizard/implementation-plan.md; its stage set, its remaining
// refusals and its non-interactive contract are W5.1's).
//
// It is the same engine as setup, and the same stages, because a re-converge
// IS a converge: what differs is what the two doors may create. update may
// create nothing, so the flags that create are hidden here and refused by
// name (see foreignFlag) — a script that re-converges after an upgrade must
// not be able to create a project, a billing link or a Batch backend by
// passing the flag that did it on the first run.

// updateLong is `fugaro update --help`. W1.2 writes it (the retired
// command's Long is split between the two doors; this half is new text,
// because the old one described a command that created); W5.1 grows it with
// update's own stage set and refusals.
const updateLong = `update re-converges an installation that already exists against this release:
the same Terraform roots, the same stages, the same confirmations and the same
exit codes as fugaro setup, for a project that is set up already (or, with
--repo, for the repository of the checkout at PATH). Run it after upgrading the
CLI: it applies what the release changed, and one with nothing to do says
"No changes" and exits 0.

It creates nothing. The flags that do — --create-project, --display-name,
--parent, --managed, --link-billing and --provision-batch — are fugaro setup's,
and update refuses each by name rather than ignoring it, so a re-converge
cannot create a GCP project, link a billing account or provision the Batch
backend by accident. A first installation is fugaro setup.

--forget is update's own: it is the rollback, and it turns log isolation and
registry cleanup off with a guarded apply before it removes every address from
Terraform's state — in a checkout, the repository's addresses with them —
destroying nothing else. A managed project has no rollback: it is refused there,
because the installation's own resources include the managed deployer's fence,
which the state removal would leave live in the project and accounted for by
nothing (docs/gcp-setup.md, "Rolling back").

Everything else is setup's help word for word, because it is the same
converge: the review screen and the one typed project name in a project Fugaro
owns; the steps that keep their own typed confirmation — a project's creation
and the billing link are setup's, and here they cannot arise at all, and a
secret is a value, typed in a hidden prompt, which --yes cannot supply — against
the steps --yes covers, which by D12's answer of 2026-10-20 is every ordinary
step this door owns: the Firestore database's permanent location, a billable
first image build, replacing a registry tag and onboarding an unlisted
repository on a provider's own host with an origin git config that does not
rewrite it (the build, the tag and the repository are covered in setup too); the
secrets stage's hidden prompts and the one line of fugaro secrets set commands
it leaves instead; the plugin wiring; --anchor, which writes the checkout's
gcp_project line and nothing else; --plan-only, --print-vars, --config-only and
--publish-config; and --json, which prints the same result object. Exit codes:
0 done (or only planned), 1 a refusal or a step left for you, 2 a cloud
failure.`

// updateDoor is the second door: the re-converge, and the door that creates
// nothing.
var updateDoor = convergeDoor{
	name:  "update",
	use:   "update [--repo [PATH]]",
	short: "Re-converge the installation, or a checkout's repository, after a CLI upgrade",
	long:  updateLong,
	// The creating flags, in the order the flags are registered: each is
	// fugaro setup's, and each is refused by name with the door that takes it.
	// --provision-batch is here because it provisions the Batch backend's
	// installation-level root — an apply that creates resources, and a first
	// run's business, like the project and the billing link.
	foreign: []foreignFlag{
		{name: "create-project", owner: "fugaro setup", hint: "creating a GCP project is a first run's"},
		{name: "display-name", owner: "fugaro setup", hint: "it names a project --create-project makes"},
		{name: "parent", owner: "fugaro setup", hint: "it places a project --create-project makes"},
		{name: "managed", owner: "fugaro setup", hint: "it marks the project --create-project makes as managed"},
		{name: "link-billing", owner: "fugaro setup", hint: "linking a billing account is a first run's, and it may incur charges"},
		{name: "provision-batch", owner: "fugaro setup", hint: "provisioning the Batch backend's root is a first run's"},
	},
}

// newUpdateCmd is `fugaro update`.
func newUpdateCmd() *cobra.Command { return newUpdateCmdWith(&initOptions{}) }

// newUpdateCmdWith is newUpdateCmd over options the caller holds, the shape
// newSetupCmdWith has for the same reason: the tests drive a door over an
// options value they can read back.
func newUpdateCmdWith(o *initOptions) *cobra.Command { return newInitCmdWith(o, updateDoor) }
