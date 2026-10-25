package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	// The managed-mode package of internal/managed, aliased because this file
	// already imports internal/infra/managed as managed: the state bucket's
	// derived name is its own (managedmode.InstallationsStateBucket), the same
	// function init's check and the inventory's read use.
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro managed flag (docs/design/managed-projects.md D58, the plan's Task
// 6.1) is the owner's decision that an EXISTING project — one a Fugaro run
// did not create — is managed, recorded in that project's own marker object.
// It is the second way a project becomes managed; the first is a run that
// created it (Task 0.1).
//
// It is the only path in Fugaro that LOWERS the guard on a project Fugaro
// did not create, and that is the whole reason for its shape: the marker is
// what turns the typed confirmation off (D55), so an operator who flags the
// wrong project has made that project's applies unprompted. The shape, and
// what each part buys, is the answer's own (D58's row, and the plan's Task
// 6.1 steps):
//
//   - It is owner-run. A coding agent's session is refused before anything is
//     read, in the shape the other cloud-writing paths use (agentMarker, the
//     six markers, and initflow.AgentRefusal's one sentence), and that
//     refusal carries a test: an agent cannot flag a project managed by its
//     own action. The identity that can run it is the operator's own, and it
//     is not D75's deployer account — the account a managed apply impersonates
//     can neither write the marker nor run this command.
//   - It is per project, with the exact project ID. One invocation names one
//     project: the argument is a GCP project ID, there is no bulk form, no
//     wildcard and no "all projects" form, and it must be the selected
//     installation's own gcp_project. The confirmation is the exact project ID
//     typed at a real terminal, and nothing stands in for it: this command
//     registers no --yes and no --non-interactive, so a pipe, a script and a
//     flag cannot give it.
//   - It writes through the marker's own discipline. The object is
//     fugaro/managed.json at the runs bucket's root, written by
//     managed.WriteFlag exactly as Task 0.1 writes the creation fact: into a
//     bucket proved to be that project's own by its project number and the
//     installation's label (a bucket name is global, so a mark proves nothing
//     without them), and in the version that carries the budget amount the
//     owner states. A project whose bucket fails that proof is not flagged,
//     and the command says so rather than writing anywhere else.
//   - It writes once, with one exception. A project that already records an
//     amount is refused — the amount is the owner's decision at the time it
//     was taken, and this command adds a budget rather than editing one — and
//     so is any object this build cannot read as this project's marker. The
//     exception is the one object the apply cannot use and the gate's own
//     refusal names this command as the way out of: a version-1 creation fact,
//     which is what every `fugaro setup --create-project` without --managed
//     leaves behind. That fact records no amount, so the project is refused by
//     every managed apply; the flag replaces it with the version that records
//     the amount, under a write conditioned on the generation it read, so a
//     writer that got there first is never clobbered.
//   - It only ADDS guards, and the risk is in its own output. A flagged
//     project gains the deterministic gate, the owner-written delete list
//     (fugaro allow-delete) and the recorded budget — things an ordinary
//     project does not have — so its applies are MORE checked. What it also
//     does is remove the typed confirmation, and the command prints that,
//     before anything is typed, as what the owner is giving up. Against a
//     session that goes around the program it is not a boundary: a coding
//     agent holds the operator's credentials, can write this object directly
//     and can unset its own marker (SECURITY.md records the wider Owner/Editor
//     bypass). The refusal below stops an agent that uses the program.
//   - belong is never a target of any task in the plan (Global Constraint 7).
//     This command has no batch form, and flagging belong would be the owner's
//     own deliberate act, by its exact project ID, at their own terminal.
//
// What it is not, stated rather than implied: it does not create or change a
// budget alert. The amount it records is the amount the owner states, in the
// marker, and the apply reads it as the evidence that a budget was chosen at
// all (Task 4.4) — the billing account's alert thresholds are Terraform's,
// created by fugaro setup's installation root.

// newManagedFlagCmd is `fugaro managed flag PROJECT_ID`.
func newManagedFlagCmd() *cobra.Command {
	var o cloudOptions
	var amount int64
	var currency string
	cmd := &cobra.Command{
		Use:   "flag PROJECT_ID",
		Short: "Flag an existing GCP project as managed (the project's owner)",
		Long: `Flag an existing GCP project PROJECT_ID as managed, recording the managed
marker in that project's own runs bucket.

This is how a project Fugaro did not create becomes managed: the marker is
what tells a later run that this project's applies may skip the typed
confirmation, and it is normally written by the run that created the project
(fugaro setup --create-project --managed). Flagging is the after-the-fact way,
answered by the owner on 2026-10-16 (design D58).

Run it in your own terminal, as an operator, with your own credentials, and
type the GCP project ID it asks for. A coding agent's session is refused, and
nothing stands in for the typed ID — not --yes, not --non-interactive, not a
pipe.

PROJECT_ID must be the exact GCP project ID of the installation selected by
--project, --config or the checkout: one invocation names one project, and
there is no bulk form, no wildcard and no prefix.

It records two things in gs://<runs bucket>/fugaro/managed.json: that this
project is managed, and the monthly budget amount and currency the owner
states with --budget and --budget-currency. The amount is required, with no
default (D64), because the marker's amount is what the apply-time budget read
opens the gate on: a marked project that records no amount is refused by every
managed apply rather than applied without a budget control. The amount is the
budget ALERT amount the installation's Terraform creates and nothing else —
this command does not create or change a budget, and it makes no billing call.

It writes BOTH marks a managed project carries, in one step: that object and
the label fugaro_managed=true on the GCP project itself (Task 3.0). The read
requires both — the label is what a project can be narrowed by, and a project
without it is not managed whatever its runs bucket holds — so writing the
object alone would leave this project exactly as unmanaged as it was. The
label is written on the project, by the account running this command, which is
why the account needs resourcemanager.projects.update on it; the project's
other labels are kept. A project that already carries that key with another
value is refused rather than overwritten — the label someone else put there is
a statement, not an empty slot — and the object written first is not taken
back, so that refusal says which half is there and which is missing.

What flagging gives up, stated before it writes: the marker is what turns the
typed confirmation off (D55), so from this project's next apply on, the
deterministic gate, the whole-project inventory, the owner-written delete list
(fugaro allow-delete) and the recorded budget are what stand in the
confirmation's place — and a coding agent's session can apply a plan the gate
admitted with no human prompt.

What it does not do: it is not a boundary against a session that goes around
this program. A coding agent's session holds the operator's credentials, can
write this object directly with gcloud storage cp, and can unset its own
marker and run this command; SECURITY.md records the wider Owner/Editor
bypass. The refusal below is a speed bump against an agent that uses the
program, not a wall against one that goes around it.

A project that already records an amount is refused: the amount is the owner's
decision at the time it was taken, and this command adds a budget rather than
editing one. The one object it does replace is a version-1 creation fact —
what fugaro setup --create-project without --managed leaves, and the state the
managed apply refuses for having no amount recorded. What it replaced is said
in the output, and the write is conditioned on the object it read, so nothing
is overwritten blind.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return managedFlag(cmd, o, args[0], amount, currency)
		},
	}
	addCloudFlags(cmd, &o)
	cmd.Flags().Int64Var(&amount, "budget", 0, "the monthly budget alert amount to record, in whole units of --budget-currency (required)")
	cmd.Flags().StringVar(&currency, "budget-currency", "", "the currency --budget is in, three uppercase letters such as USD (required)")
	return cmd
}

// managedFlag is newManagedFlagCmd's body. The order is the answer's: who is
// running, what is being asked for, which project it is, that the bucket is
// that project's own — and only then the typed confirmation and the write.
// Every refusal before the write is a hard return, and nothing is written by
// any of them.
func managedFlag(cmd *cobra.Command, o cloudOptions, id string, amount int64, currency string) error {
	ctx := cmd.Context()
	// A credential leak first, before anything opens the cloud: GODEBUG is
	// read before main runs and cannot be unset in time (refuseHTTP2Debug).
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	// And the refusal the answer rests on, before a single read: a coding
	// agent's session flags nothing, whatever its arguments. It holds the
	// operator's credentials and could run this command itself, so this is a
	// mitigation and not a barrier (AgentAdvice) — but it is the property
	// D58's answer is built on, and it carries a test.
	if m := agentMarker(os.Getenv); m != "" {
		return userErr("nothing was written: fugaro managed flag records the owner's own decision about a GCP project with the operator's credentials, so it is run by a person: %s", initflow.AgentRefusal(m))
	}
	// And a run's own process, whatever its agent's markers: the plan refuses
	// the command from a run credential as well as from an agent's session,
	// and a run's variables are set by the platform rather than by the agent.
	if m := managedFlagRunMarker(os.Getenv); m != "" {
		return userErr("nothing was written: fugaro managed flag is run by a person at their own terminal, and this process is a Fugaro run's (%s is set)", m)
	}
	// One project, named exactly, and by a name a shell cannot expand into
	// several: the argument is a GCP project ID and the wildcard and list
	// forms are not ones. The equality with the selected installation's own
	// gcp_project, below, is the other half — a prefix of the ID passes this
	// check and fails that one.
	if !config.GCPProjectRE.MatchString(id) {
		return userErr("%q is not a GCP project ID: this command flags one project, by its exact ID, with no wildcard, no prefix and no list", safetext.Field(id))
	}
	// The amount and the currency are a pair and are required: there is no
	// default (D64), and a marked project that records no amount is refused by
	// every managed apply (Task 4.4), so a flag without one would lower this
	// project's guard and buy nothing at all. The rule is the marker's own
	// (managed.Budget.Check, which internal/infra/managed's writer and reader
	// both apply), called rather than restated.
	budget := managed.Budget{Amount: amount, Currency: currency}
	if err := budget.Check(); err != nil {
		return userErr("nothing was written: flagging a project records the monthly budget alert amount its owner chose, and the amount and the currency are required together: %v; pass --budget and --budget-currency, as in --budget 50 --budget-currency USD", safetext.Field(err.Error()))
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	for _, w := range lc.Warnings() {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", safetext.Field(w))
	}
	// The project the argument names is the project this installation is in,
	// and nothing else: --project and --config select the Fugaro project, the
	// argument is the GCP project, and the two having to agree is what keeps
	// an invocation from flagging a project other than the one whose config
	// and credentials it is using.
	if id != lc.GCPProject {
		return userErr("nothing was written: the project this command would flag is the one the argument names, and %s is not the installation selected here: Fugaro project %s is installed in %s. Name %s, or select its installation with --project or --config",
			safetext.Field(id), safetext.Field(lc.Name), safetext.Field(lc.GCPProject), safetext.Field(lc.GCPProject))
	}
	// The runs bucket's default name and no other, the rule the managed
	// objects already live by (allow-delete, managed apply): the marker is
	// read from fugaro-runs-<gcp_project>, so a config that names another
	// bucket would write a marker nobody reads.
	bucket := "fugaro-runs-" + lc.GCPProject
	if got := lc.RunsBucketName(); got != bucket {
		return userErr("project %s names the runs bucket %q, and a managed project's marker lives in %s: the managed read locates the project's runs bucket by that name, so a marker written into another bucket would be read by nobody. Check runs_bucket in the project config",
			safetext.Field(lc.Name), safetext.Field(got), safetext.Field(bucket))
	}
	// And the installation's own state bucket, by the same rule and for the
	// same reason: the managed apply's inventory reads the installation's state
	// out of gs://fugaro-tfstate-<gcp_project> by that derived name
	// (managedAccounted, and Inventory.installationCreated on the other half of
	// the comparison), so a project whose state lives in another bucket is one
	// whose state read fails (CauseStateUnread) and whose every apply falls back
	// to the typed confirmation this command exists to take away — the very
	// thing flagging gives up (D55, printed below). This command has no
	// --state-bucket of its own: what a project created with `fugaro setup
	// --state-bucket <other>` recorded is the local config's
	// terraform.state_bucket, which is what infra.Installation resolves for it
	// and what is left to check here. The refusal is the rule fugaro restore
	// already keeps about the same field.
	stateBucket := managedmode.InstallationsStateBucket(lc.GCPProject)
	if got := lc.Terraform.StateBucket; got != "" && got != stateBucket {
		return userErr("nothing was written: project %s records the Terraform state bucket %q, and a managed project's installation state must live in %s: the managed apply reads the installation's state out of that bucket by its derived name — a function of the GCP project, never of a flag or of the project's config — so state kept anywhere else reads as state that could not be read at all, and every managed apply of %s would fall back to the typed confirmation this command exists to take away. A project whose installation state is in another bucket cannot be managed: move the state to %s, or leave this project unmanaged",
			safetext.Field(lc.Name), safetext.Field(got), safetext.Field(stateBucket), safetext.Field(lc.Name), safetext.Field(stateBucket))
	}
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return err
	}
	// The number is what says a bucket name is this project's (a bucket name
	// is global), and it is what the writer proves the bucket with.
	number, err := infra.ProjectNumber(ctx, c, lc.GCPProject)
	if err != nil {
		// The API's own text can carry a body: one sanitised line.
		return remote(fmt.Errorf("reading the number of GCP project %s: %s", safetext.Field(lc.GCPProject), safetext.Field(err.Error())))
	}
	w := cmd.OutOrStdout()
	// What is about to be written, and what it costs, before anything is
	// typed: the answer's own framing is that flagging only adds guards AND
	// that the marker is what turns the confirmation off, so the second half
	// is printed rather than left to be inferred.
	fmt.Fprintf(w, "⚠ CONFIRM (project %s): flagging GCP project %s as managed writes gs://%s/%s\n", safetext.Field(lc.Name), safetext.Field(lc.GCPProject), safetext.Field(bucket), managed.MarkerObject)
	fmt.Fprintf(w, "  and the label %s=%s on the project itself: a managed project carries both, and the read requires both, so the object alone would change nothing about this project's runs\n", managed.ProjectLabelKey, managed.ProjectLabelValue)
	fmt.Fprintf(w, "  it records: this project is managed, with the budget alert amount %d %s\n", amount, safetext.Field(currency))
	fmt.Fprintf(w, "  it gives up: the typed confirmation for this project's applies. From the next apply on, a plan a coding agent's session authored is applied with no human prompt when the deterministic gate admits it (D55), and what stands in the confirmation's place is the gate, the whole-project inventory, the owner's delete list (fugaro allow-delete) and the budget above.\n")
	fmt.Fprintf(w, "  it creates nothing: no resource is created, no billing call is made, and the amount above is recorded rather than spent.\n")
	fmt.Fprintf(w, "  it does not protect against: a coding agent's session, which holds these credentials and can write this object directly (SECURITY.md).\n")
	fmt.Fprintf(w, "  if this project already records whether it is managed: nothing is written unless its marker is the creation fact that records no amount (what `fugaro setup --create-project` without --managed leaves), which this replaces with the one above. A marker that already records an amount is refused, and so is any object this build cannot read as this project's.\n")
	if err := confirmTypedGCPProject(cmd, lc.GCPProject); err != nil {
		return err
	}
	replaced, err := managed.WriteFlag(ctx, c.Storage, bucket, lc.GCPProject, number, budget)
	if err != nil {
		return managedFlagErr(lc, bucket, err)
	}
	// The other half of the same statement, in the same step (Task 3.0): the
	// label on the project itself. Both are what the read requires, so a flag
	// that wrote the marker alone would leave the project exactly as
	// unmanaged as it was, with a marker whose command can no longer be run
	// again (this command replaces only the version-1 fact). It is written
	// here, after the marker, in markCreated's order and for markCreated's
	// reason: the object is the authority and goes first, and the label is
	// the narrowing mark that completes it.
	//
	// The gate is this command's own and not WriteManagedLabel's: the owner
	// typed this project's GCP project ID at a real terminal to get here, and
	// this process is neither a coding agent's session nor a Fugaro run's
	// (both refused above, before anything was read). WriteFlag's marker was
	// gated on the same thing. What the label may write is unchanged: other
	// labels are kept, and a value someone else put there is refused rather
	// than overwritten.
	if err := managed.WriteManagedLabelForFlag(ctx, c.CRM, lc.GCPProject); err != nil {
		// The marker is on disk from here on and the project is NOT managed
		// (readFact requires both marks), so this is not a success, and it is
		// not a state a second `fugaro managed flag` can finish: the marker
		// is now the version that records an amount, and that is the one
		// object this command refuses to replace. The refusal says what is
		// missing, that only the project's owner writes it at their own
		// terminal, and where that one line is — the condition and not the
		// command, the rule Task 3.0's refusal text keeps, because this
		// command's output is the operator's (a coding agent's session is
		// refused above and never reaches it).
		return remote(fmt.Errorf("wrote gs://%s/%s, but not the %s=%s label on GCP project %s (%v): the read requires both marks, so %s is not managed yet and nothing is skipped for it. The label is written on the account that owns the project, in the owner's own terminal, and docs/design/managed-projects.md has that one line; it is not a second run of this command, which replaces only the creation fact that records no amount",
			safetext.Field(bucket), managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue, safetext.Field(lc.GCPProject), safetext.FieldErr(err), safetext.Field(lc.Name)))
	}
	// Read back through the managed read itself, with its project-number
	// check (the plan's step 3): a marker the apply would not believe, or one
	// recording another amount than the one typed, is not a flagged project,
	// and the command does not say it is.
	got, ok, reason, err := managedFlagReadBack(ctx, c.Storage, c.CRM, lc.Name, lc.GCPProject)
	if err != nil || !ok || got != budget {
		why := safetext.Field(reason)
		if err != nil {
			why = safetext.Field(err.Error())
		} else if ok {
			why = fmt.Sprintf("it records %d %s", got.Amount, safetext.Field(got.Currency))
		}
		return remote(fmt.Errorf("wrote gs://%s/%s, but it does not read back as %s's marker with the budget %d %s (%s): check it with `gcloud storage cat gs://%s/%s` as the operator before relying on it",
			safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.Name), amount, safetext.Field(currency), why, safetext.Field(bucket), managed.MarkerObject))
	}
	fmt.Fprintf(w, "read back gs://%s/%s through the managed read, with the project-number check: managed, budget %d %s\n", safetext.Field(bucket), managed.MarkerObject, got.Amount, safetext.Field(got.Currency))
	// What happened is said, and the two are different things: this project
	// had no marker, or it had the version-1 creation fact and now records an
	// amount.
	wrote := fmt.Sprintf("wrote gs://%s/%s: %s is a managed project", safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.Name))
	if replaced {
		wrote = fmt.Sprintf("replaced the creation fact in gs://%s/%s: %s recorded no budget amount and now records %d %s", safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.Name), amount, safetext.Field(currency))
	}
	fmt.Fprintf(w, "%s, and its applies no longer stop for the typed confirmation; the gate, the owner's delete list and the recorded budget %d %s are what replace it\n",
		wrote, amount, safetext.Field(currency))
	// The undo names both writes, because this command made both (Task 3.0):
	// the object in the runs bucket and the label on the project. Either one
	// alone makes the project unmanaged again — the read requires both — and
	// they come off differently: the object has no command of ours, and the
	// label has a gcloud line that no test can run, so it is marked
	// UNVERIFIED as the backfill in docs/design/managed-projects.md is.
	fmt.Fprintf(w, "undo: this command wrote two things, and either one alone makes the project unmanaged again, because the read requires both — delete the object gs://%s/%s yourself as the operator, since no command of ours removes the marker in this release, and take the label %s=%s off the project itself with gcloud projects update %s --remove-labels=%s, which is UNVERIFIED until an operator has run it live (docs/design/managed-projects.md carries the same line)\n",
		safetext.Field(bucket), managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue, safetext.Field(lc.GCPProject), managed.ProjectLabelKey)
	return nil
}

// managedFlagReadBack is the read the written marker is checked with; tests
// replace it.
var managedFlagReadBack = managed.ReadBudget

// managedFlagRunMarkers are the variables a Fugaro run's process carries: the
// run's own name (set on both backends), Cloud Run's job and execution, and
// Batch's task index.
var managedFlagRunMarkers = []string{"FUGARO_RUN", "CLOUD_RUN_JOB", "CLOUD_RUN_EXECUTION", "BATCH_TASK_INDEX"}

// managedFlagRunMarker is the first run marker getenv shows set, or "".
func managedFlagRunMarker(getenv func(string) string) string {
	for _, k := range managedFlagRunMarkers {
		if getenv(k) != "" {
			return k
		}
	}
	return ""
}

// confirmTypedGCPProject is the confirmation this command takes and the only
// one it takes: the exact GCP project ID typed at a real terminal. It is not
// confirmTypedStrict, which asks for the Fugaro project's name, because what
// this command decides is about a GCP project and the string that shows the
// owner has the right one in front of them is that project's own ID (D58's
// answer). No flag stands in for it: this command registers no --yes and no
// --non-interactive, so a refusal here names none.
func confirmTypedGCPProject(cmd *cobra.Command, gcpProject string) error {
	if !stdinIsTerminal(cmd.InOrStdin()) {
		return userErr("%s: it is confirmed by typing the GCP project ID and by nothing else — not a flag, not a pipe", initflow.NeedsTerminal("flagging a project managed"))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Type %s to confirm: ", safetext.Field(gcpProject))
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return userErr("reading the confirmation: %v", safetext.FieldErr(err))
	}
	// The exact ID, after the trim the other typed confirmations do: another
	// project's ID, a prefix of this one and an empty line are all "not
	// confirmed".
	if strings.TrimSpace(line) != gcpProject {
		return userErr("not confirmed (the GCP project ID was not typed exactly); nothing was written")
	}
	return nil
}

// managedFlagErr is what the owner can do about a flag that did not happen:
// the project already records a marker, the bucket is not that project's own,
// credentials the bucket's root refuses, or the cloud's own answer.
func managedFlagErr(lc *localcfg.Config, bucket string, err error) error {
	switch {
	case errors.Is(err, managed.ErrAlreadyMarked):
		return userErr("nothing was written for %s: gs://%s/%s is already there and is not the creation fact this command adds a budget to, so %s already records whether it is managed — a run that created it wrote the fact, or an owner flagged it before. This command replaces only a version-1 creation fact, which records no amount; a marker that records one is the owner's decision, not a value to edit. To read what is there, use `gcloud storage cat gs://%s/%s` as the operator",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject)
	case errors.Is(err, managed.ErrRefused):
		return userErr("nothing was written for %s: the marker goes into the project's own runs bucket, at gs://%s/%s, and that bucket is not %s's: check gcp_project and runs_bucket in the project config, or run fugaro project check: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.GCPProject), safetext.Field(err.Error()))
	case isAccessDenied(err):
		return userErr("nothing was written for %s: gs://%s/%s is at the runs bucket's root, where only an operator's own credential may write (a launcher writes only runs/ and tokens/, and no run account may write the bucket's root at all): run this as an operator, or ask one to flag the project: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(err.Error()))
	default:
		// A wrapped provider error is untrusted text too: one bounded field,
		// with its chain kept so the exit code is still the remote one.
		return remote(fmt.Errorf("writing %s in gs://%s for %s: %w", managed.MarkerObject, safetext.Field(bucket), safetext.Field(lc.Name), safetext.FieldErr(err)))
	}
}
