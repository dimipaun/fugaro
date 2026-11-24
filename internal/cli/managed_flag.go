package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

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

// fugaro managed flag (docs/managed-projects/spec.md D58, the plan's Task
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
//   - It is per project, with the project named. One invocation names one
//     project: the argument, when there is one, is a GCP project ID, there is
//     no bulk form, no wildcard and no "all projects" form, and it must be the
//     selected installation's own gcp_project. The argument became OPTIONAL on
//     2026-11-21 (D2213, which amended D1541's consent by argument): omitted,
//     the project is that installation's own, and what stands where the typed
//     ID stood is that managedFlagStatement names it before either mark is
//     written — so the invocation is still about a project the owner can see
//     it is about, and the two refusals either side of the write (a coding
//     agent's session, a run's process) are unchanged. This command still
//     registers no --yes and no --non-interactive: nothing stands in for the
//     project's identity, and there is nothing to confirm.
//   - It writes through the marker's own discipline. The object is
//     fugaro/managed.json at the runs bucket's root, written by
//     managed.WriteFlag exactly as Task 0.1 writes the creation fact: into a
//     bucket proved to be that project's own by its project number and the
//     installation's label (a bucket name is global, so a mark proves nothing
//     without them), and in the version that carries the budget amount the
//     owner states or takes the default of — the version with no count in it
//     (D1543). A project whose bucket fails that proof is not flagged, and the
//     command says so rather than writing anywhere else.
//   - It writes once, with two exceptions. A project that already records an
//     amount is not touched by a run that states none: the default is what a
//     project recording nothing is marked with, and a figure its owner chose is
//     not a default's to replace (D2215). An EXPLICIT --budget does replace it,
//     through managed.RewriteBudget and under the same generation precondition,
//     because the amount became a figure a default writes and a figure nobody
//     chose must be changeable by the owner who meant another one. The other
//     exception is the one object the apply cannot use and the gate's own
//     warning names this command as the way out of: a version-1 creation fact,
//     which is what every `fugaro setup --create-project` without an amount
//     leaves behind. That fact records no amount; the flag replaces it with the
//     version that records one. Either way the write is conditioned on the
//     generation it read, so a writer that got there first is never clobbered.
//   - It only ADDS guards, and the risk is in its own output. A flagged
//     project gains the deterministic gate, the owner-written delete list
//     (fugaro allow-delete) and the recorded budget — things an ordinary
//     project does not have — so its applies are MORE checked. What it also
//     does is remove the typed confirmation, and the command prints that,
//     before anything is written, as what the owner is giving up. Against a
//     session that goes around the program it is not a boundary: a coding
//     agent holds the operator's credentials, can write this object directly
//     and can unset its own marker (SECURITY.md records the wider Owner/Editor
//     bypass). The refusal below stops an agent that uses the program.
//   - belong is never a target of any task in the plan (Global Constraint 7).
//     This command has no batch form, and flagging belong would be the owner's
//     own deliberate act, selecting its installation and naming it.
//
// What it is not, stated rather than implied: it does not create or change a
// budget alert. The amount it records is the amount the owner states or the
// default it takes (D2212), in the marker, and the apply reads it as the
// evidence that a budget was chosen at all (Task 4.4) — the billing account's
// alert thresholds are Terraform's, created by fugaro setup's installation
// root.

// newManagedFlagCmd is `fugaro managed flag [PROJECT_ID]`.
func newManagedFlagCmd() *cobra.Command {
	var o cloudOptions
	var amount int64
	var currency string
	cmd := &cobra.Command{
		Use:   "flag [PROJECT_ID]",
		Short: "Flag an existing GCP project as managed (the project's owner)",
		Long: `Flag an existing GCP project as managed, recording the managed marker in
that project's own runs bucket.

This is how a project Fugaro did not create becomes managed: the marker is
what tells a later run that this project's applies may skip the typed
confirmation, and it is normally written by the run that created the project
(fugaro setup --create-project). Flagging is the after-the-fact way, answered
by the owner on 2026-10-16 (design D58).

Run it as an operator, with your own credentials. A coding agent's session is
refused, and so is a Fugaro run's process. The project is the installation
selected by --project, --config or the checkout. PROJECT_ID may be given, and
must then be that installation's own exact GCP project ID; the statement
printed before the write names the project either way, so the invocation is
about a named project and not about whatever the checkout happens to hold
(design D1541, amended on 2026-11-21 by D2213, when the argument became
optional: what stands where the typed ID stood is the naming, and still nothing
is typed at a prompt). One invocation names one project: there is no bulk form,
no wildcard and no prefix.

It records two things in gs://<runs bucket>/fugaro/managed.json: that this
project is managed, and the monthly budget amount and currency the owner means
with --budget and --budget-currency. Both have defaults — 100 USD — so the
command with no flags records a figure rather than asking for one (design
D2212 and D2211). The amount is the budget ALERT amount the installation's
Terraform creates and nothing else — this command does not create or change a
budget, and it makes no billing call. The alert itself is the installation's
own Terraform and belongs to the update step of that installation, not to this
object — fugaro update --budget N --budget-currency CCC --billing-account ID
sets the amount this command records. Nothing else is a question: the bound
on
billable image builds this object used to record (D1013) is gone (D1543), so
there is no count to ask the owner for and none to write, exactly as fugaro
setup asks for none.

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

An amount a project already records is its owner's figure, and a default never
replaces it: run with no --budget over such a marker, the command refuses and
names the flag that changes it. An explicit --budget rewrites the recorded
amount (design D2215), keeping the recorded currency unless --budget-currency
is given too, and prints the old figure and the new one. The one object it
replaces unasked is a version-1 creation fact — what fugaro setup
--create-project leaves, and the state the managed apply warns for, having no
amount recorded. What it replaced is said in the output, and every write is
conditioned on the object it read, so nothing is overwritten blind.`,
		// The argument became optional (D2213), and an invocation with none is
		// about the selected installation's own project — the one the
		// statement names before either mark is written. One invocation still
		// names one project: no form of this command names two.
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			return managedFlag(cmd, o, id, amount, currency)
		},
	}
	addCloudFlags(cmd, &o)
	cmd.Flags().Int64Var(&amount, "budget", managed.DefaultBudgetAmount, "the monthly budget alert amount to record, in whole units of --budget-currency (default 100; pass it to record another figure, or to replace an amount the marker already records)")
	cmd.Flags().StringVar(&currency, "budget-currency", managed.DefaultBudgetCurrency, "the currency --budget is in, three uppercase letters such as USD (default USD, or the currency the marker already records when an amount is being replaced)")
	return cmd
}

// managedFlagStatement is what flagging a project writes, what it records and
// what it gives up, printed BEFORE the write and never after it: the answer's
// own framing (D58) is that flagging only adds guards AND that the marker is
// what turns the typed confirmation off, so the second half is stated rather
// than left to be inferred. It is a statement and not a confirmation — what
// confirms the write is the project the invocation named, the argument it was
// given (D1541) or the installation's own named here (D2213) — so it is printed
// as what the write does.
//
// It is one function because two commands make this same write, both of them
// the owner's own and both through the same two writers: `fugaro managed flag`
// and `fugaro update`'s self-flag (update_selfflag.go, D1553, which makes it
// when the run is given the budget once). A statement of what a write costs,
// kept true in two places, goes stale in one of them — and this one names the
// amount, the bound and the guard that is given up.
//
// Its last line is each caller's own, because what happens when the project
// already records whether it is managed is not the same for the two: this
// command creates the object, replaces the creation fact that records no
// amount, and rewrites a recorded figure only when its owner named one
// (D2215), while the self-flag can also finish a fact that already records the
// amount it was given.
func managedFlagStatement(w io.Writer, gcpProject, bucket string, amount int64, currency string) {
	fmt.Fprintf(w, "flagging GCP project %s as managed writes gs://%s/%s\n", safetext.Field(gcpProject), safetext.Field(bucket), managed.MarkerObject)
	fmt.Fprintf(w, "  and the label %s=%s on the project itself: a managed project carries both, and the read requires both, so the object alone would change nothing about this project's runs\n", managed.ProjectLabelKey, managed.ProjectLabelValue)
	fmt.Fprintf(w, "  it records: this project is managed, with the budget alert amount %d %s, and no bound on the billable image builds a run may start (D1543): each build reserves its estimated cost against the project's budget before Cloud Build is asked for it, and a build the caps have no room for is refused where the project's budget mode enforces them\n", amount, safetext.Field(currency))
	fmt.Fprintf(w, "  it gives up: the typed confirmation for this project's applies. From the next apply on, a plan a coding agent's session authored is applied with no human prompt when the deterministic gate admits it (D55), and what stands in the confirmation's place is the gate, the whole-project inventory, the owner's delete list (fugaro allow-delete) and the budget above.\n")
	fmt.Fprintf(w, "  it creates nothing: no resource is created, no billing call is made, and the amount above is recorded rather than spent.\n")
	fmt.Fprintf(w, "  it does not protect against: a coding agent's session, which holds these credentials and can write this object directly (SECURITY.md).\n")
}

// managedFlag is newManagedFlagCmd's body. The order is the answer's: who is
// running, what is being asked for, which project it is, that the bucket is
// that project's own — and only then the statement of what the write does and
// the write itself. Every refusal before the write is a hard return, and
// nothing is written by any of them.
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
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was written: fugaro managed flag records the owner's own decision about a GCP project with the operator's credentials, so it is run by a person: %s", initflow.AgentRefusal(m))
	}
	// And a run's own process, whatever its agent's markers: the plan refuses
	// the command from a run credential as well as from an agent's session,
	// and a run's variables are set by the platform rather than by the agent.
	if m := managedFlagRunMarker(os.Getenv); m != "" {
		return userErr("nothing was written: fugaro managed flag is run by a person at their own terminal, and this process is a Fugaro run's (%s is set)", m)
	}
	// One project, named exactly, and by a name a shell cannot expand into
	// several: the argument, when there is one, is a GCP project ID and the
	// wildcard and list forms are not ones. The equality with the selected
	// installation's own gcp_project, below, is the other half — a prefix of
	// the ID passes this check and fails that one. An invocation with no
	// argument takes the installation's own project (D2213): a name the config
	// already holds, so there is nothing here for this check to judge.
	if id != "" && !config.GCPProjectRE.MatchString(id) {
		return userErr("%q is not a GCP project ID: this command flags one project, by its exact ID, with no wildcard, no prefix and no list", safetext.Field(id))
	}
	// The amount and the currency both have defaults (D2212, D2211): a flag
	// with neither records 100 USD. A mark that records no amount is the state
	// the apply gate warns for and sends its owner to this very command to fix
	// (D1545, F14): the amount is the evidence that a budget was chosen at
	// all, not a lock, and a flag that recorded none would leave that warning
	// standing. What is still refused, before any cloud read, is a pair the
	// marker itself would not write — and the rule is the marker's own
	// (managed.Budget.Check, which internal/infra/managed's writer and reader
	// both apply), called rather than restated. A currency the marker already
	// records replaces the default further down (R6), and the pair it makes is
	// one this same rule accepted as the marker's own.
	budget := managed.Budget{Amount: amount, Currency: currency}
	if err := budget.Check(); err != nil {
		return userErr("nothing was written: flagging a project records the monthly budget alert amount its owner chose (design D2212), and %v; the amount and the currency default to %d %s, and --budget and --budget-currency are what record another figure, as in --budget 50 --budget-currency USD",
			safetext.Field(err.Error()), managed.DefaultBudgetAmount, safetext.Field(managed.DefaultBudgetCurrency))
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	for _, w := range lc.Warnings() {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", safetext.Field(w))
	}
	// D2213: with no argument the project is the selected installation's own.
	// What stood where the typed ID stood is the statement below, which names
	// the project before either mark is written — so an invocation with no
	// argument is still about a named project, and still not one the owner
	// cannot see. The installation's own gcp_project is what no argument
	// means: it is resolved here and not defaulted at the flag's declaration,
	// so a config that moved under the invocation moves this with it.
	if id == "" {
		id = lc.GCPProject
	}
	// The project the argument names is the project this installation is in,
	// and nothing else: --project and --config select the Fugaro project, the
	// argument is the GCP project, and the two having to agree is what keeps
	// an invocation from flagging a project other than the one whose config
	// and credentials it is using. Only a given argument can disagree — the
	// branch above resolved the other case to this very field — so this is the
	// refusal for a typed ID that names another install, and the message says
	// which one it is.
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
	// This run's account and its log (D1352), opened where the run's own work
	// starts and not at the command's door: who is running, whether the
	// argument is a project ID at all, whether it is this installation's, and
	// whether the marker and the state are read from a bucket this project can
	// be managed through are all decided above from the command line and this
	// installation's own config (reporterOpener), so a refused flag files no
	// log, not even an empty one, which would be one of the fifty this name
	// keeps (CL3's pruneLogs).
	rep, sink := reporterFor(cmd, "managed-flag", nil)
	if sink != nil {
		defer func() { _ = sink.Close() }()
	}
	defer rep.Summary()
	// The number is what says a bucket name is this project's (a bucket name
	// is global), and it is what the writer proves the bucket with.
	number, err := infra.ProjectNumber(ctx, c, lc.GCPProject)
	if err != nil {
		// The API's own text can carry a body: one sanitised line.
		return remote(fmt.Errorf("reading the number of GCP project %s: %s", safetext.Field(lc.GCPProject), safetext.Field(err.Error())))
	}
	// What the project records now, read before the statement and before
	// anything is written (D2215, R6). It is the object half alone — no label
	// requirement — because the two answers it has to give are both about the
	// object: whether the project already records an amount, which decides
	// whether a default may be written, and which currency a rewrite keeps.
	// ReadFlagFact is that read, the same one `fugaro update`'s self-flag
	// takes, so the two owners of this write judge the marker the same way.
	fact, err := managed.ReadFlagFact(ctx, c.Storage, bucket, lc.GCPProject, number)
	if err != nil {
		// Nothing is written yet and nothing is claimed: a fact that could not
		// be read is not "records nothing", so this is a hard return and the
		// writer is never reached.
		return managedFlagFactErr(lc, bucket, err)
	}
	if fact.Ours {
		// This project already records an amount. D2215: the default is not a
		// value to write over a figure the owner chose, and an explicit
		// --budget is what replaces it. The trigger is --budget and not
		// --budget-currency, because the flag the answer names is the amount —
		// a currency alone would rewrite the amount with the default.
		if !cmd.Flags().Changed("budget") {
			return userErr("nothing was written for %s: gs://%s/%s is already there and records the budget %d %s, and a default never replaces a figure the project's owner chose (design D2215). Run again with --budget to replace it, as in --budget 400, and with --budget-currency only if the currency changes too",
				safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, fact.Budget.Amount, safetext.Field(fact.Budget.Currency))
		}
		// R6: the currency a rewrite writes is the recorded one unless the
		// owner named another. Putting the default over a recorded EUR would
		// be the one thing this default must never do.
		if !cmd.Flags().Changed("budget-currency") {
			currency = fact.Budget.Currency
		}
		budget = managed.Budget{Amount: amount, Currency: currency}
	}
	w := cmd.OutOrStdout()
	managedFlagStatement(w, lc.GCPProject, bucket, budget.Amount, budget.Currency)
	// The statement's last line is this command's own, and it is the one the
	// self-flag answers differently: this command replaces one object unasked
	// and refuses every other state.
	fmt.Fprintf(w, "  if this project already records whether it is managed: an object that records no amount (what `fugaro setup --create-project` without a budget leaves) is replaced with the one above. One that already records an amount is not touched by a default — a figure its owner chose is not a default's to replace (D2215) — and the amount above is written over it only when --budget was named on the command line. Any object this build cannot read as this project's marker is refused.\n")
	// The write, and the read that settles it, are one step: the marker, the
	// label and the read-back are one thing an operator either has or has not
	// (Task 3.0). It is opened after the statement above, so nothing in flight
	// is drawn over the lines the owner has just read (G1). What confirms the
	// write is the project the invocation names — the argument matched against
	// the selected installation, or the installation itself named in the
	// statement above (D1541, D2213) — and not a second typed answer at a
	// prompt.
	step := rep.Step("flag")
	fail := func(err error) error {
		step.Failed(err, nil)
		return err
	}
	// One write, two writers, and which one runs is decided by what is there:
	// RewriteBudget is the only way a recorded amount changes (D2215), and
	// WriteFlag is the creation of the object and the replacement of the
	// creation fact that records no amount. Collapsing the two into one call
	// would either edit a figure the owner chose or refuse the one state the
	// apply gate sends owners here for.
	var replaced bool
	var oldBudget managed.Budget
	if fact.Ours {
		oldBudget, err = managed.RewriteBudget(ctx, c.Storage, bucket, lc.GCPProject, number, budget)
		if err != nil {
			return fail(managedFlagRewriteErr(lc, bucket, err))
		}
	} else {
		replaced, err = managed.WriteFlag(ctx, c.Storage, bucket, lc.GCPProject, number, budget)
		if err != nil {
			return fail(managedFlagErr(lc, bucket, err))
		}
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
	// named this project's GCP project ID as this command's argument to get
	// here, and this process is neither a coding agent's session nor a Fugaro
	// run's (both refused above, before anything was read). WriteFlag's marker
	// was gated on the same thing. What the label may write is unchanged: other
	// labels are kept, and a value someone else put there is refused rather
	// than overwritten.
	if err := managed.WriteManagedLabelForFlag(ctx, c.CRM, lc.GCPProject); err != nil {
		// The object is on disk from here on and the project is NOT managed
		// (readFact requires both marks), so this is not a success. What the
		// failing write is decides what a second run can do about it: the
		// object half is settled — the figures this invocation states are the
		// ones the object records, so the rewrite writes nothing — and the
		// label is the half left, which a second run retries and which fails
		// again only where the credential still may not update the project's
		// labels. The refusal says what is missing, which write it is and the
		// permission that write needs, and where the owner's own one line is —
		// the condition and not the command, the rule Task 3.0's refusal text
		// keeps, because this command's output is the operator's (a coding
		// agent's session is refused above and never reaches it).
		return fail(remote(fmt.Errorf("wrote gs://%s/%s, but not the %s=%s label on GCP project %s (%v): the read requires both marks, so %s is not managed yet and nothing is skipped for it. The label is the half that needs resourcemanager.projects.update on the project, so it is written by the account that owns it — the owner's own terminal, whose one line docs/managed-projects/spec.md has. Running this command again with the same --budget retries exactly that write: the object already records the figures this invocation states, so the rewrite writes nothing and the label is the half left to write",
			safetext.Field(bucket), managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue, safetext.Field(lc.GCPProject), safetext.FieldErr(err), safetext.Field(lc.Name))))
	}
	// Read back through the managed read itself, with its project-number
	// check (the plan's step 3): a marker the apply would not believe, or one
	// recording another amount than the flags gave, is not a flagged project,
	// and the command does not say it is.
	got, ok, reason, err := managedFlagReadBack(ctx, c.Storage, c.CRM, lc.Name, lc.GCPProject)
	if err != nil || !ok || got != budget {
		why := safetext.Field(reason)
		if err != nil {
			why = safetext.Field(err.Error())
		} else if ok {
			why = fmt.Sprintf("it records %d %s", got.Amount, safetext.Field(got.Currency))
		}
		return fail(remote(fmt.Errorf("wrote gs://%s/%s, but it does not read back as %s's marker with the budget %d %s (%s): check it with `gcloud storage cat gs://%s/%s` as the operator before relying on it",
			safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.Name), budget.Amount, safetext.Field(budget.Currency), why, safetext.Field(bucket), managed.MarkerObject)))
	}
	// The read-back is what the step ends on, so the line a person reads is
	// the one the command is willing to claim.
	step.Done(fmt.Sprintf("read back gs://%s/%s through the managed read, with the project-number check: managed, budget %d %s",
		safetext.Field(bucket), managed.MarkerObject, got.Amount, safetext.Field(got.Currency)))
	// What happened is said, and the three are different things: this project
	// had no marker, it had the version-1 creation fact and now records an
	// amount, or it already recorded one and this invocation is about that
	// figure — replaced when the owner named another, left exactly as it was
	// when they named none.
	wrote := fmt.Sprintf("wrote gs://%s/%s: %s is a managed project", safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.Name))
	switch {
	case replaced:
		wrote = fmt.Sprintf("replaced the creation fact in gs://%s/%s: %s recorded no budget amount and now records %d %s", safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.Name), budget.Amount, safetext.Field(budget.Currency))
	case fact.Ours && oldBudget != budget:
		wrote = fmt.Sprintf("rewrote the budget in gs://%s/%s: %s recorded %d %s and now records %d %s",
			safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.Name), oldBudget.Amount, safetext.Field(oldBudget.Currency), budget.Amount, safetext.Field(budget.Currency))
	case fact.Ours:
		wrote = fmt.Sprintf("left gs://%s/%s as it was: %s already records the budget %d %s, and nothing was rewritten",
			safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.Name), budget.Amount, safetext.Field(budget.Currency))
	}
	// What was written, and the undo, go through the run's own writer: they are
	// the account the step above ends on, so they are kept in the log beside
	// the step's line (D1352). They are printed after the step ended — the
	// step's line is what the read-back says, and these two say what it wrote
	// and how to undo it — so nothing is in flight over them.
	out := sayThrough(rep, w)
	fmt.Fprintf(out, "%s, and its applies no longer stop for the typed confirmation; the gate, the owner's delete list and the recorded budget %d %s are what replace it\n",
		wrote, budget.Amount, safetext.Field(budget.Currency))
	if fact.Ours {
		// The alert is the installation's own Terraform, and what sets it is
		// the update that applies the installation — not this object. The
		// sentence names that invocation with this figure, because a person who
		// changed the amount has to be told what sets it; it says nothing about
		// what an update reads for itself, which is the update's business. It is
		// printed on this path alone, so a creation is not told about a change
		// it did not make.
		fmt.Fprintf(out, "the budget ALERT is not changed here: it is the installation's own Terraform, and the `fugaro update` that applies the installation is what sets it — `fugaro update --budget %d --budget-currency %s --billing-account ID` applies the figure above\n",
			budget.Amount, safetext.Field(budget.Currency))
	}
	// The undo names both marks, because this command writes both (Task 3.0):
	// the object in the runs bucket and the label on the project. Either one
	// alone makes the project unmanaged again — the read requires both — and
	// they come off together now: `fugaro managed unflag` is the owner-run
	// command that takes both off (D1020), the one this line used to say did
	// not exist. The gcloud line takes the label off on
	// its own, and no test can run it, so it stays marked UNVERIFIED as the
	// backfill in docs/managed-projects/spec.md is.
	fmt.Fprintf(out, "undo: the two marks a managed project carries are the object gs://%s/%s and the %s=%s label on the project itself, and either one alone makes the project unmanaged again, because the read requires both — take both off with `fugaro managed unflag %s` in your own terminal, which removes the object and the label; the label alone comes off with `gcloud projects update %s --remove-labels=%s`, which is UNVERIFIED until an operator has run it live (docs/managed-projects/spec.md carries the same line)\n",
		safetext.Field(bucket), managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue, safetext.Field(lc.GCPProject), safetext.Field(lc.GCPProject), managed.ProjectLabelKey)
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

// managedFlagErr is what the owner can do about a flag that did not happen:
// the project already records a marker, the bucket is not that project's own,
// credentials the bucket's root refuses, or the cloud's own answer.
func managedFlagErr(lc *localcfg.Config, bucket string, err error) error {
	switch {
	case errors.Is(err, managed.ErrAlreadyMarked):
		return userErr("nothing was written for %s: gs://%s/%s is already there and is not the creation fact this command adds a budget to, so %s already records whether it is managed — a run that created it wrote the fact, or an owner flagged it before. This command replaces only a version-1 creation fact, which records no amount, and an amount a marker already records is replaced by running this command with --budget naming the new figure, which rewrites the fact rather than writing over it (D2215) — what is here is neither, so nothing was written over it. To read what is there, use `gcloud storage cat gs://%s/%s` as the operator",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject)
	case errors.Is(err, managed.ErrRefused):
		return userErr("nothing was written for %s: the marker goes into the project's own runs bucket, at gs://%s/%s, and that bucket is not %s's: check gcp_project and runs_bucket in the project config, or run fugaro project check: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.GCPProject), safetext.Field(err.Error()))
	case isAccessDenied(err):
		return userErr("nothing was written for %s: gs://%s/%s is at the runs bucket's root, where only an operator's own credential may write (a launcher writes only runs/, tokens/ and issues/, and no run account may write the bucket's root at all): run this as an operator, or ask one to flag the project: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(err.Error()))
	default:
		// A wrapped provider error is untrusted text too: one bounded field,
		// with its chain kept so the exit code is still the remote one.
		return remote(fmt.Errorf("writing %s in gs://%s for %s: %w", managed.MarkerObject, safetext.Field(bucket), safetext.Field(lc.Name), safetext.FieldErr(err)))
	}
}

// managedFlagFactErr is what a failed READ of the marker leaves, and it is a
// separate function because its sentences have to be about a read: the write
// below has not happened yet, so "nothing was written" is true for a different
// reason, and a reader told the marker "goes into" a bucket would look for a
// write that was never attempted. Same two states, same recoveries — a bucket
// that is not this project's own, and a credential the object refuses.
func managedFlagFactErr(lc *localcfg.Config, bucket string, err error) error {
	switch {
	case errors.Is(err, managed.ErrRefused):
		return userErr("nothing was written for %s: whether this project already records a budget is read from its own runs bucket, at gs://%s/%s, and that bucket is not %s's: check gcp_project and runs_bucket in the project config, or run fugaro project check: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.GCPProject), safetext.Field(err.Error()))
	case isAccessDenied(err):
		return userErr("nothing was written for %s: gs://%s/%s is at the runs bucket's root, and reading it needs the operator's own credential: run this as an operator, or ask one to flag the project: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(err.Error()))
	default:
		return remote(fmt.Errorf("reading %s in gs://%s for %s: %w", managed.MarkerObject, safetext.Field(bucket), safetext.Field(lc.Name), safetext.FieldErr(err)))
	}
}

// managedFlagRewriteErr is what the owner can do about a rewrite that did not
// happen: the object moved under this invocation, or it is one this build may
// not write a fact over. The other failures the write has are the marker's own
// and are reported as they come.
func managedFlagRewriteErr(lc *localcfg.Config, bucket string, err error) error {
	switch {
	case errors.Is(err, managed.ErrAlreadyMarked):
		return userErr("nothing was rewritten for %s: gs://%s/%s was written by someone else between this command's read and its write, so the amount recorded there is that writer's and the figure this run was given is not what the marker holds — read it with `gcloud storage cat gs://%s/%s`, and run this command again if it is not the figure the owner means",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(bucket), managed.MarkerObject)
	case errors.Is(err, managed.ErrRefused):
		return userErr("nothing was rewritten for %s: the amount is replaced in the project's own marker at gs://%s/%s, and that object is not one this build may write over: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(err.Error()))
	default:
		return remote(fmt.Errorf("rewriting the budget recorded in %s in gs://%s for %s: %w", managed.MarkerObject, safetext.Field(bucket), safetext.Field(lc.Name), safetext.FieldErr(err)))
	}
}
