package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	roleaccess "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/safetext"
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

It is also how a second machine, yours or a teammate's, gets its local config:
on a machine with no config for the installation, fugaro update --gcp-project
<id> --region <region> takes the project's name from the installation (at a
terminal; a run that cannot ask also passes --name <its name>), writes
the local config from the installation's outputs (its launchers, operators and
budget backend included) and applies nothing; it then names the roles you may
lack and the one command an owner runs to grant them. The next fugaro update
is the ordinary re-converge.

It creates nothing. The flags that do — --create-project, --display-name,
--parent, --managed, --link-billing and --provision-batch — are fugaro setup's,
and update refuses each by name rather than ignoring it, so a re-converge
cannot create a GCP project, link a billing account or provision the Batch
backend by accident. A first installation is fugaro setup.

--forget is update's own: it is the rollback, and it turns log isolation and
registry cleanup off with a guarded apply before it removes every address from
Terraform's state — in a checkout, the repository's addresses with them —
destroying nothing else. An installation that holds the managed deployer's fence
has no rollback: it is refused there, because the state removal would leave the
seven scoped roles and their seven conditioned bindings live in the project and
accounted for by nothing. The fence is the installation's and not a mode of the
project — every run that rewrites the installation's settings carries the key
forward — so taking a project out of managed mode does not remove it and does
not lift the refusal (docs/design/managed-projects.md §10.2, "Rolling back").

Everything else is setup's help word for word, because it is the same
converge: the review screen and the one typed project name in a project Fugaro
owns — and in a managed project (fugaro managed flag) the gate's own read of
the project's marks stands in for that name and for the steps the gate judges,
at your own terminal exactly as in a coding agent's session: every plan is
still printed exactly as it applies and every verdict is still recorded, and
only a step that is money, permanence or a secret still asks for your own word
(D1026). The steps that keep their own typed confirmation — a project's creation
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
func newUpdateCmdWith(o *initOptions) *cobra.Command {
	cmd := newInitCmdWith(o, updateDoor)
	// The retry offer (update_retry.go, T-IS19) is the door's own last line,
	// and this is the only point that is the door's own: cobra runs PostRunE
	// when RunE returned nil and not otherwise, so a refusal, a step left for
	// the person and a cloud failure offer nothing — the run that just
	// installed the release that fixed an issue is the run that says so.
	//
	// The args are passed on because --repo converges the checkout at PATH
	// (init.go's runInitRepo): which store the offer may read follows from
	// which checkout the run converged, and PostRunE is the only place the
	// door still has them.
	cmd.PostRunE = func(cmd *cobra.Command, args []string) error {
		updateRetryHint(cmd, o, args)
		return nil
	}
	return cmd
}

// writeAdminList is D1940's step: the roster's project-scoped admins, derived
// and published as the bucket object the intake reads (internal/access's
// adminlist.go).
//
// Why it is `update`'s and why here: the intake admits a caller to the shared
// `fugaro_p_` prefix by checking them against this list, and it may hold no
// database role — reading the roster directly is the design the decision
// rejected, and the derivation is what keeps the intake's reach an object read
// (D1940, F32). The list is therefore a fact of the installation that has to be
// refreshed by an installation-wide run, and `fugaro update` is the one run
// every installation has: the HARD RULE says the upgrade is this command
// alone, so a roster written by `fugaro project access set` reaches the intake
// here or nowhere. It sits at the end of the converge, beside D1548's raise and
// D1553's self-flag, and for the same reason they do: the roster is read after
// every stage that could have moved it, and what it prints belongs to this
// run's result.
//
// The lag is the decision's, and the step says so rather than hiding it: a
// grant or a revocation is not seen by the intake until the run after it. What
// catches up on its own is nothing the roster says, so the line names the next
// `fugaro update` as the moment a role change lands.
//
// Nothing here may fail the update (the HARD RULE, as in update_budget_mode.go):
// every state that leaves the object as it is — no shared budget backend, a
// database that is not this installation's, a roster that could not be read or
// decoded, a bucket that refused the write — is a warning or silence, and so
// are the two states in which this run is not the act that publishes the list
// at all: a coding agent's session the managed gate does not stand in for, and
// a run that did not confirm its steps (both below). In every one of them the
// object is exactly what the run found. A list that was not written is a list
// the intake decides on as it is, which is fail closed for the caller and never
// a reason to stop an upgrade.
func (e *initEngine) writeAdminList(ctx context.Context) {
	r, o := e.r, e.r.o
	// The door first, as in raiseBudgetMode: this is the upgrade's step, and
	// the hook both doors share is the end of the converge (init_stages.go's
	// outcome). `fugaro setup` creates an installation; the roster of one that
	// exists is not its business, and a project it just created has no admins
	// to derive from.
	if o.doorName() != updateDoor.name || o.planOnly {
		return
	}
	// Then the session, for the rule the three steps beside it keep
	// (pinCredentialsImage, fillCredentialsAudience, selfFlagUnmanaged): this
	// object is authorization data — who the intake admits to the shared
	// prefix — and a coding agent's session is not the act that publishes it.
	// `--yes`'s help says as much: under one of these markers the door "changes
	// nothing outside this machine, --yes included", and a write the loop never
	// put through a confirmation would be exactly the exception that promise
	// does not have. The gate is the one exception (D1016): where the marks say
	// the project is managed, the gate's own read stands in for the
	// confirmations this run would have asked for, so the session IS the run
	// that converges the installation — its stages apply — and the object is
	// part of what it converges. A session the gate refused never reaches here
	// at all: its refusal is the run's outcome.
	//
	// And a run that did not confirm its steps writes nothing either: a step
	// the loop could not take is under left_for_you (a secret's value, a policy
	// publish that would loosen the published node), the run exits 1 over it,
	// and the person it named runs the door again — that run is the one that
	// publishes the list. Both refusals leave the object an earlier update
	// wrote exactly where it is, which is the state this step starts from every
	// time: the intake decides on the list it has, and the next run that
	// finishes replaces it.
	if agentMarker(os.Getenv, r.stdinTerminal()) != "" && !r.gateUnderway() {
		return
	}
	if len(r.res.LeftForYou) > 0 {
		return
	}
	// The roster is a node of the installation's budget database, reached over
	// IAM, and a run that never opened the cloud or a project with no shared
	// backend at all has none to read: the line about that installation is
	// doctor's (policy-published), and `fugaro update --firebase-project` — the
	// command that gives it a backend — is where this step next runs.
	if e.c == nil || e.lc == nil || e.lc.Budget == nil || e.lc.Budget.RTDBURL == "" {
		return
	}
	db, err := newBudgetClient(ctx, e.lc.Budget.RTDBURL, e.lc.Endpoints.NoAuth)
	if err != nil {
		r.warn(fmt.Sprintf("the admin list was not written: the database the roster lives in could not be opened (%s), so the intake keeps the list it has", safetext.FieldErr(err)))
		return
	}
	// The one ownership question every reader and writer of that database asks
	// (budgetDBUnadopted), before anything is read out of it: a database that is
	// not this installation's is not where this installation's roster lives, and
	// a list derived from somebody else's grants would admit the wrong people.
	if why := budgetDBUnadopted(ctx, e.lc, infra.NewDB(db, e.lc.Name, e.lc.GCPProject, ""), "the admin list was not written", selfCommand()+" update"); why != "" {
		r.warn(why)
		return
	}
	node, err := roleaccess.Read(ctx, db)
	if err != nil {
		// The roster is read whole or not at all (roleaccess.Read): a node that is
		// present but unreadable is never an empty roster, and a list derived
		// from a partly understood node would admit people the node does not.
		r.warn(fmt.Sprintf("the admin list was not written: %s, and a list derived from it would be one the roster does not say", safetext.FieldErr(err)))
		return
	}
	// An absent node is a roster with no grants (roleaccess.Read's own answer), and
	// the list that follows from it is the empty one: the object is written on
	// every update, so the intake always reads a document that says who is on
	// it, and "nobody" is a fact rather than a missing object.
	list := roleaccess.Admins(node.Grants)
	data, err := roleaccess.EncodeAdminList(list)
	if err != nil {
		r.warn(fmt.Sprintf("the admin list was not written: the roster's project admins could not be encoded (%s)", safetext.FieldErr(err)))
		return
	}
	// This installation's own runs bucket and no other, the rule every sibling
	// writer of an installation-scoped object keeps (publishSharedWarn's anchor,
	// the managed apply's marker and delete list, restore): the list admits its
	// readers to this project's shared prefix, so it belongs in the bucket this
	// project's intake reads — the one named after the GCP project, never a
	// bucket a local config names instead. A hand edit, or a copy of a
	// teammate's config with the wrong project in it, would otherwise publish
	// this project's admins into another installation's bucket, where that
	// installation's intake reads them.
	//
	// The bucket is read off both spellings a config can name it in, because
	// they can disagree and each one alone would be a way past the check: the
	// URL the write opens (a config's bucket_url is kept as it is, and
	// fugaro image's buildRecordNote exists because a bucket_url naming another
	// bucket is a config a person can hold), and, when that URL is not a GCS
	// one, the name the config declares. A bucket_url off GCS is a path or an
	// endpoint on this machine — a test's or an emulator's bucket, where every
	// other write of such a run lands — and not another installation's.
	target := e.lc.RunsBucketName()
	if u, err := url.Parse(e.lc.BucketURL()); err == nil && u.Scheme == "gs" {
		target = u.Host
	}
	if own := "fugaro-runs-" + e.lc.GCPProject; target != own {
		r.warn(fmt.Sprintf("the admin list was not written: the runs bucket is %q, and an object the intake reads belongs in this installation's own, %s. Check runs_bucket and bucket_url in the project config",
			target, own))
		return
	}
	if fakeEndpointsOnGS(e.lc, e.lc.BucketURL()) {
		// The config's endpoints are fakes (tests, emulators): the gs:// bucket
		// is not the one they stand for, and opening it would reach the real
		// network with the developer's credentials.
		return
	}
	b, err := sharedBucketOpener(ctx, e.lc.BucketURL())
	if err != nil {
		r.warn(fmt.Sprintf("the admin list was not written: the runs bucket %s could not be opened (%s)", safetext.Field(e.lc.BucketURL()), safetext.FieldErr(err)))
		return
	}
	defer b.Close()
	if err := b.Put(ctx, roleaccess.PathAdminList, data, "application/json"); err != nil {
		// operatorWriteErr is the bucket's refusal for the one account that may
		// write outside runs/, tokens/ and issues/ (H8): an update run by a
		// launcher reads the roster but cannot publish the list, and what it can
		// do about that is the command the refusal names.
		r.warn(fmt.Sprintf("the admin list was not written: %s", safetext.FieldErr(operatorWriteErr(e.lc.BucketURL(), roleaccess.PathAdminList, err))))
		return
	}
	fmt.Fprintf(r.w, "admin list: %d project admin(s) in %s, the object the intake reads before it writes to the shared prefix. It lags a role change until the next `fugaro update`: a grant or a revocation reaches the intake in the run after it.\n",
		len(list), roleaccess.PathAdminList)
}
