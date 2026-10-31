package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro restore (docs/design/managed-projects.md, D66) rebuilds a managed
// project from zero: the project that was lost is gone — the state, the
// bucket, the marker, the delete list and the data with it — and what comes
// back is a NEW GCP project, a from-zero apply of the repository's
// agent-authored root, and a list of what is not coming back.
//
// The three things a restore is, and none of them is a shortcut around the
// managed apply (Task 3.1, D76):
//
//   - It creates the project through the SAME path `fugaro setup
//     --create-project --managed` uses. That is not a convenience: the
//     creating run is also what writes the project's creation fact for the
//     project it created (Task 0.1, and Task 4.2's amount) and what sets up
//     D77's dedicated state bucket and binds the deployer to it (Task 1.5).
//     A restore needs a step of its own for neither, and going through
//     anything else would mean a new project that is not managed.
//   - It applies the repository's agent-authored root by calling
//     managedApply — the same function `fugaro managed apply` runs, with the
//     same preflight allowlist, the same deterministic gate, the same
//     plan-hash re-check, the same credential handling and the same
//     confirmation. The restore is not a second, weaker path to an apply:
//     it is that path, with a creation in front of it.
//   - It reads no prior state, anywhere. The state lived in the project that
//     was lost, so a restore that needed it would fail exactly when it is
//     needed (the managed-projects Gap's finding 31). The cloud reads it
//     makes before it creates anything are the OLD project's own creation
//     fact, which is where the owner's amount is written down (Task 4.2) and
//     whether that project was managed at all, and whether the NEW project ID
//     already exists (a restore always creates a new one).
//
// There is no release gate of its own any more (Task 4.4 removed the
// constant one, managedmode.Shipped, that kept this command inert). What
// keeps it from being a weaker path is the order: the coding agent's refusal
// FIRST, before a single cloud call; then restorePreflight's refusals on the
// merits; then the creation, whose postcondition (verifyCreated) reads the
// NEW project's creation fact through the same budget gate the managed apply
// opens on (managedBudgetGateOf: ReadBudget, and a valid version-2 marker or
// a refusal) before the checkout line moves; and then the apply, which opens
// that gate again, itself, on the new project (managedApply ->
// managedBudgetGate). A restore whose creation wrote no valid budget stops
// before the checkout moves, rather than leaving a half-restored
// installation the apply would then refuse.

// restoreOptions are fugaro restore's own flags, besides the cloud flags.
//
// There is no --yes and no --non-interactive, and none is missing: every step
// of a restore is money or permanent, which is initflow.Typed's definition —
// a project is created on a billing account, a Terraform state is written
// from zero, and a committed file in the checkout is edited. A flag that
// stood in for the typed name would be exactly the second, weaker path this
// command must not be.
type restoreOptions struct {
	cloud                          cloudOptions
	budget                         int64
	budgetCurrency, billingAccount string
	displayName, parent            string
}

// newRestoreCmd is `fugaro restore NEW_PROJECT_ID ROOT` (D68: a top-level
// verb of its own, beside fugaro setup --create-project --managed).
func newRestoreCmd() *cobra.Command {
	var o restoreOptions
	cmd := &cobra.Command{
		Use:   "restore NEW_PROJECT_ID ROOT",
		Short: "Rebuild a managed project from zero, into a new GCP project ID, and print what is not restored",
		Long: `Rebuild a managed project from zero into a NEW GCP project ID.

A managed project is one fugaro created, with a marker and an owner-written
delete list. When it is lost — deleted, or closed — its ID stays reserved for
about thirty days, so this is a restore into a new project:

  - NEW_PROJECT_ID is the GCP project the installation is rebuilt in. It is
    created through the same path fugaro setup --create-project --managed uses,
    which is also what re-marks the project as managed and re-creates the
    deployer account with the rest of the installation, so neither is a
    separate step here. The repository's gcp_project: line is moved to it, so
    teammates find the shared config again.
  - ROOT is the repository's agent-authored Terraform root, in the checkout
    whose fugaro.yaml names the project. It is applied from zero through the
    SAME path fugaro managed apply runs — the managed allowlist, the
    deterministic gate over the saved plan, the owner's delete list, and the
    deployer account of D75 — into the dedicated state bucket the new project
    was created with. No prior state is read: the state lived in the project
    that was lost.

The OLD project's own creation fact is read whenever the old project can
still be read: a project that is not managed is refused, whatever flags are
given, and a managed one gives the monthly budget amount unless --budget and
--budget-currency are given. When the old project cannot be read (deleted,
closed, not yours to read) the restore proceeds only on --budget and
--budget-currency. The billing account is always yours to give: the creation
fact is not a credential and records none.

NEW_PROJECT_ID must not exist: an existing project is refused (Fugaro only starts fresh).
If the creation fails or does not end in a project this run created and
marked, the local project config file is put back as it was and the restore
can be run again; what the creation did in the cloud (the project, its
billing link, its state bucket, the installation's resources, any secrets)
is not undone, and the message says so.

Every step is money or permanent, so the project's name is typed at a real
terminal, and there is no --yes, no --non-interactive and no address argument.
The command is refused in a coding agent's session.

It ends by printing what was NOT restored, as a list: the runs that were in
flight and their counters, the spend history and the kill-switch state, the
Firestore and RTDB application data, the Firestore database's location (which
is permanent and can never be changed), every secret value, the plan files
that were never stored, and the owner-written delete list — fugaro/allow-delete.json
is not carried across and nothing here re-creates it, so until the owner writes
it again with fugaro allow-delete every delete and replace in the new project
is refused (D74). The project layer and the project's recipes are not held by
the repository and are not re-published: the command names them and the one
command that publishes each. The deployer account is the one thing that is
re-created rather than lost, and the output says so.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestore(cmd, o, args[0], args[1])
		},
	}
	addCloudFlags(cmd, &o.cloud)
	f := cmd.Flags()
	f.Int64Var(&o.budget, "budget", 0, "the new project's monthly budget amount, in whole units of --budget-currency (default: the amount the OLD project's creation fact records)")
	f.StringVar(&o.budgetCurrency, "budget-currency", "", "the budget's currency, such as USD (travels with --budget)")
	f.StringVar(&o.billingAccount, "billing-account", "", "the billing account the new project is linked to (required: the creation fact records the amount and no account)")
	f.StringVar(&o.displayName, "display-name", "", "with the creation: the new project's display name (default: its ID)")
	f.StringVar(&o.parent, "parent", "", "with the creation: organizations/<number> or folders/<number> to create the project under (default: no parent; an organization is never guessed)")
	return cmd
}

// restoreRun is one run of fugaro restore. The fields are filled in in the
// order the sequence uses them, so each step reads only what an earlier step
// has already decided.
type restoreRun struct {
	cmd  *cobra.Command
	o    restoreOptions
	sel  localcfg.Selection
	lc   *localcfg.Config // the installation as it is: the project that was lost
	path string           // the project config's file, which this run moves
	old  []byte           // its bytes, for the diff, the backup and putBack
	// backup is where writeConfig saved old, named in putBack's message.
	backup string
	next   *localcfg.Config // the config after the move
	// left is what the config's move could not place in the new project, in
	// the operator's words. It is printed in the run's account.
	left []string

	newID, rootArg string
	root, checkout string
	budget         *managed.Budget
}

// runRestore is newRestoreCmd's body. The order is the whole of the design:
// the refusals first, then the inputs (restorePreflight), then the one
// confirmation, then the move, the creation and its verification, the
// checkout and the apply — and every refusal before the creation is a hard
// return.
func runRestore(cmd *cobra.Command, o restoreOptions, newID, rootArg string) error {
	// A credential leak first, before anything opens the cloud (refuseHTTP2Debug).
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	// And then the coding agent's refusal, FIRST of everything that reads the
	// cloud: before the project is selected and before a single cloud call.
	// It is the rule every applying step gives, and of the creation of a
	// project on somebody's billing account it is most true. There is no
	// release gate after it any more: the budget gate is the new project's
	// own, read after the creation (verifyCreated) and again by the apply.
	if m := agentMarker(os.Getenv); m != "" {
		return userErr("nothing was restored: %s", initflow.AgentRefusal(m))
	}
	return restoreSteps(cmd, o, newID, rootArg)
}

// restoreManagedApply is the apply a restore ends in: managedApply, the
// function `fugaro managed apply` runs, and nothing else (a test pins the
// production value). It is a variable only so that a test can record that
// the sequence below reaches it, exactly once and with the new project's
// root.
var restoreManagedApply = managedApply

// restoreInitCmd builds the creating path's command: newSetupCmdWith, the
// constructor `fugaro setup` itself uses (the door that creates — a restore
// makes a project, so the update door, which refuses every creating flag, is
// not the one it runs). A variable only so a test can make the creating path
// panic and see the config put back.
var restoreInitCmd = newSetupCmdWith

// restorePreflight is every check a restore makes before it changes anything:
// no file is written, no project created, nothing applied, and the one cloud
// write it could cause is none — it only reads. It is what refuses a restore
// on its merits.
//
// In order: the coding agent's refusal and the real terminal every typed step
// needs; the selection and the project config's file; the fresh ID; the root
// and its checkout, and the checkout's gcp_project: line, which must be one
// the restore can move; the old project's managed status and the budget
// (budgetOf), which must pass managed.Budget.Check whether it came from the
// flags or from the old fact; the new ID, which must not exist; and the
// config's move.
func restorePreflight(cmd *cobra.Command, o restoreOptions, newID, rootArg string) (*restoreRun, *infra.Clients, error) {
	ctx := cmd.Context()
	// The coding agent's refusal again, at the head of the sequence itself:
	// runRestore checks it before the gate, and a caller of this function
	// that skipped runRestore must not skip it too.
	if m := agentMarker(os.Getenv); m != "" {
		return nil, nil, userErr("nothing was restored: %s", initflow.AgentRefusal(m))
	}
	if !stdinIsTerminal(cmd.InOrStdin()) {
		return nil, nil, userErr("nothing was restored: %s", initflow.NeedsTerminal("a restore's typed confirmations"))
	}
	sel, lc, err := selectProject(ctx, o.cloud)
	if err != nil {
		return nil, nil, err
	}
	for _, w := range lc.Warnings() {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", w)
	}
	rr := &restoreRun{cmd: cmd, o: o, sel: sel, lc: lc, newID: newID, rootArg: rootArg}
	if err := restoreNewProjectID(lc, newID); err != nil {
		return nil, nil, err
	}
	if rr.path, err = restoreConfigPath(sel, lc); err != nil {
		return nil, nil, err
	}
	if rr.old, err = os.ReadFile(rr.path); err != nil {
		return nil, nil, userErr("reading the project config %s: %v", rr.path, err)
	}
	if rr.root, rr.checkout, err = restoreRoot(ctx, lc, rootArg); err != nil {
		return nil, nil, err
	}
	// The checkout's line, read now: a line the restore would refuse to move
	// (a third project, a nested key) is a refusal before the creation, not
	// a project created for an installation whose checkout then cannot follow.
	yaml, err := readFugaroYAML(filepath.Join(rr.checkout, "fugaro.yaml"))
	if err != nil {
		return nil, nil, userErr("the checkout's fugaro.yaml: %v", err)
	}
	if _, _, err := restoreGCPProjectLine(yaml, lc.GCPProject, newID); err != nil {
		return nil, nil, err
	}
	// The amount, and the account: the two things a creation needs and that
	// the old project's fact does not both answer — and the old project's
	// managed status, which budgetOf refuses on.
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return nil, nil, err
	}
	if rr.budget, err = rr.budgetOf(ctx, c); err != nil {
		return nil, nil, err
	}
	// The same shape rule the creation and the fact's read apply (Task 4.2),
	// to the flags' value and the fact's alike, before either is printed.
	if err := rr.budget.Check(); err != nil {
		return nil, nil, userErr("nothing was restored: %v", err)
	}
	// The new project must not exist yet (and the creating path is told to
	// refuse one that does by the time it reads it: strictCreate).
	if err := rr.refuseExisting(ctx); err != nil {
		return nil, nil, err
	}
	// What the config's move will be, before the confirmation rather than
	// after it: restoreConfig's refusals are refusals a typed name does not
	// help with.
	if err := rr.planConfig(); err != nil {
		return nil, nil, err
	}
	return rr, c, nil
}

// restoreSteps is the sequence behind runRestore's refusals: the preflight,
// the one confirmation, the creation and its verification, the checkout and
// the apply.
//
// The local config moves before the creation (the creating path installs into
// it, and init refuses a --gcp-project the selected config does not name), so
// until the creation is verified a failure — an error or a panic — puts the
// config FILE back to its original bytes (the deferred putBack) and the
// checkout's fugaro.yaml has not been touched. What the creating path did in
// the cloud is not undone (putBack's message lists it).
func restoreSteps(cmd *cobra.Command, o restoreOptions, newID, rootArg string) (err error) {
	ctx := cmd.Context()
	rr, c, err := restorePreflight(cmd, o, newID, rootArg)
	if err != nil {
		return err
	}
	// The one confirmation, before anything is changed: the local config, the
	// checkout's file, the project and the apply.
	if err := rr.confirm(cmd); err != nil {
		return err
	}
	if err := rr.writeConfig(); err != nil {
		return err
	}
	moved := true
	defer func() {
		if !moved {
			return
		}
		if p := recover(); p != nil {
			_ = rr.putBack(fmt.Errorf("panic: %v", p))
			panic(p)
		}
		if err != nil {
			err = rr.putBack(err)
		}
	}()
	var made initCreation
	if err := rr.create(ctx, &made); err != nil {
		return err
	}
	if err := rr.verifyCreated(ctx, c, made); err != nil {
		var notOpen *createdNotOpenError
		var unmarked *createdUnmarkedError
		switch {
		case errors.As(err, &notOpen):
			// The creation is this run's and complete; only the budget gate
			// refused. The config stays moved (no putBack), the checkout does not.
			//
			// Both ways out are offered and each is scoped to the state it
			// applies to, because the states are not one: what this command's
			// own --managed creation writes is a version-2 fact that records
			// the amount the restore was given, and the one object the owner's
			// flag replaces is a version-1 creation fact, which records none
			// (internal/infra/managed, WriteFlag's own rule). The inherited
			// message handed the flag to both (round-2 review, nit 3), which
			// offers a command that would refuse for the state this command
			// actually leaves.
			moved = false
			return restoreWrap(err, "%s exists: this run created it and wrote its creation fact, so it is a complete managed project except for the apply, and the project config now names it (its previous content is in the backup %s); the checkout's gcp_project: line did not move. If the marker read failed for a passing reason, change that line to gcp_project: %s and run fugaro managed apply %s to finish. Otherwise the fact in the runs bucket is what refuses the apply, and what can replace it depends on what it records: a creation fact that records none is what `fugaro managed flag %s --budget N --budget-currency CCC`, run at the owner's own terminal, replaces with the version that records the amount (Task 6.1), while the fact this run's --managed creation writes already records one, and no command of ours rewrites one that does — so for it, shut %s down (gcloud projects delete %s), put the backup back over the project config, and run fugaro restore again with another NEW_PROJECT_ID",
				rr.newID, rr.backup, rr.newID, rr.rootArg, rr.newID, rr.newID, rr.newID)
		case errors.As(err, &unmarked):
			// The creation is this run's and half marked: the fact is in the
			// runs bucket and the project has no label, so nothing reads it
			// as managed yet. The config stays moved (no putBack) for the
			// reason the budget-gate branch above does: the project exists
			// because this run made it, and it is one owner-run write away
			// from being a complete managed project, so the message says
			// what is missing and where the line that writes it is — the
			// refusal names the condition and not the command, because a
			// refusal is what a coding agent's session reads (Task 3.0 §5).
			moved = false
			return restoreWrap(err, "%s exists: this run created it and wrote its creation fact, so the project config keeps naming it (its previous content is in the backup %s) and the checkout's gcp_project: line did not move. What is missing is the project's %s=%s label, which only the owner writes, in their own terminal: docs/design/managed-projects.md has that one line. Run it, then change the checkout's line to gcp_project: %s and run fugaro managed apply %s to finish. If the project is unwanted instead, shut %s down (gcloud projects delete %s), put the backup back over the project config, and run fugaro restore again with another NEW_PROJECT_ID",
				rr.newID, rr.backup, managed.ProjectLabelKey, managed.ProjectLabelValue, rr.newID, rr.rootArg, rr.newID, rr.newID)
		default:
			return err
		}
	}
	// The project exists, this run created it and wrote its fact: from here
	// the config names a project that is the installation's, and it stays.
	moved = false
	if err := rr.anchor(cmd); err != nil {
		return restoreWrap(err, "%s was created by this run and is managed, and the project config names it; the checkout's gcp_project: line did not move, so change it to gcp_project: %s yourself, then run fugaro managed apply %s", rr.newID, rr.newID, rr.rootArg)
	}
	// The apply: the same function `fugaro managed apply` runs, so the
	// preflight allowlist, the gate, the plan-hash re-check, the credential
	// and the confirmation are the same ones and not a second, weaker path.
	if err := restoreManagedApply(cmd, restoreApplyCloud(o.cloud), rr.root); err != nil {
		return restoreWrap(err, "%s was created and is managed, and the project config names it; the apply did not finish, so run fugaro managed apply %s to finish it (nothing else is left to do but D66's list below, which a finished restore prints)", rr.newID, rr.rootArg)
	}
	return rr.report()
}

// restoreWrap adds what state a failed restore left to err, keeping err's
// exit code.
func restoreWrap(err error, format string, args ...any) error {
	return &ExitError{Code: ExitCode(err), Err: fmt.Errorf("%w. %s", err, fmt.Sprintf(format, args...))}
}

// refuseExisting is the creation's first precondition: NEW_PROJECT_ID is not
// a project the caller can read. The read fails closed: an answer that is not
// "absent" is a refusal. It is not the guard on its own — a project can
// appear, or become readable, between this read and the creation — so the
// creating path also refuses one that exists when it reads it (strictCreate),
// a create of a taken ID fails (409), and verifyCreated requires that the
// creating run itself created the project and wrote its fact.
func (rr *restoreRun) refuseExisting(ctx context.Context) error {
	ep := rr.lc.Endpoints
	pc, err := infra.NewProjectClients(ctx, infra.ProjectEndpoints{ResourceManager: ep.ResourceManager, Firebase: ep.FirebaseManagement, Billing: ep.CloudBilling}, ep.NoAuth, nil)
	if err != nil {
		return remote(err)
	}
	info, err := infra.ReadProject(ctx, pc, rr.newID)
	if info.Exists {
		return userErr("nothing was restored: GCP project %s already exists (%s), and a restore always creates a new project, which is what makes it managed. Choose a NEW_PROJECT_ID nobody holds", rr.newID, oneLine(info.State))
	}
	if err != nil {
		return userErr("nothing was restored: whether GCP project %s already exists could not be read (%s), and a restore only creates its project, so an unanswered read is a refusal", rr.newID, safetext.Field(err.Error()))
	}
	return nil
}

// verifyCreated is the creation's postcondition, checked before anything
// else moves, and it is bound to THIS run rather than to whatever is in the
// cloud: the creating run's own account (made, filled by init's markCreated)
// must say it created NEW_PROJECT_ID, did not find it already there, and
// wrote its mark — the creation fact, whose write managed.WriteMarker makes
// create-only (ifGenerationMatch=0), so a fact somebody planted first makes
// that write fail and is never this run's, and the project's label (Task
// 3.0), which is the second half and the one a credential can lack. Only
// then is the fact read back through the fail-closed read every managed
// command uses.
//
// The two halves are refused apart, and the caller acts on which one is
// missing: no fact at all is a project this run created and cannot prove it
// is the managed one, so the local config goes back and the operator is told
// to retry or shut the project down; the fact without the label is the same
// project one owner-run write away from being complete, so the config keeps
// naming it and the refusal says how to finish (createdUnmarkedError).
func (rr *restoreRun) verifyCreated(ctx context.Context, c *infra.Clients, made initCreation) error {
	switch {
	case made.Existed:
		return userErr("the creating path found %s already there, so this run did not create it (a restore always creates a new project)", rr.newID)
	case made.Created != rr.newID:
		return userErr("the creating path did not create %s (it created %q)", rr.newID, made.Created)
	case !made.Marked && made.Fact:
		// Half a mark: the creation fact is written and the label is not,
		// which the read answers "not managed" for (managed.readFact requires
		// both). The project IS this run's — the create-only fact write is
		// what proves that — so it is not putBack's case: its advice (shut
		// the project down, retry with another ID) would orphan a project an
		// owner-run label away from being complete. This run tells the
		// operator what the state is and how it is finished instead, and
		// leaves the config naming the project.
		return &createdUnmarkedError{err: userErr("the creating path created %s and wrote its creation fact, but not its %s=%s label, and the read requires both, so %s is not managed yet", rr.newID, managed.ProjectLabelKey, managed.ProjectLabelValue, rr.newID)}
	case !made.Marked:
		return userErr("the creating path created %s but did not write its creation fact, so it is not managed", rr.newID)
	}
	var s *storage.Service
	var r *crm.Service
	if c != nil {
		s, r = c.Storage, c.CRM
	}
	// The read is the managed apply's own budget gate on the NEW project
	// (ReadBudget, through managedBudgetGateOf), not IsManaged alone: a
	// creation that wrote no valid version-2 marker — a version 1, a figure
	// that fails Budget.Check, one naming another project, an unreadable
	// object — is a project the apply at the end would refuse, so it is
	// refused here, while the config can still be put back and before the
	// checkout's gcp_project: line moves.
	if _, _, err := managedBudgetGateOf(ctx, s, r, rr.lc.Name, rr.newID); err != nil {
		return &createdNotOpenError{err: userErr("the creation of %s ended and its creation fact does not open the managed apply: %v", rr.newID, err)}
	}
	return nil
}

// createdUnmarkedError is verifyCreated's other refusal that comes after the
// creation was proved to be this run's: the run wrote the creation fact and
// could not write the project's fugaro_managed label, so the project exists,
// is Fugaro's, and is not managed until an owner writes the label.
//
// It is a type of its own rather than a plain refusal for the same reason
// createdNotOpenError is: the caller's own branch has to treat it differently
// from every failure that comes before — the local project config stays
// pointed at the new project (no putBack) and the checkout's gcp_project:
// line does not move, because the project is real and one line from being
// complete. Folding it into createdNotOpenError would make the caller's
// message claim the budget gate refused, which is not what happened.
type createdUnmarkedError struct{ err error }

func (e *createdUnmarkedError) Error() string { return e.err.Error() }
func (e *createdUnmarkedError) Unwrap() error { return e.err }

// createdNotOpenError is verifyCreated's one refusal that comes AFTER this
// run is proved to have created NEW_PROJECT_ID and written its fact: the
// project exists and is the installation's, and only the managed apply's
// budget gate refused it. restoreSteps keeps the config moved for it (the
// project is real, and putting the config back would hide it) and says how
// to finish by hand, or when another NEW_PROJECT_ID is right.
type createdNotOpenError struct{ err error }

func (e *createdNotOpenError) Error() string { return e.err.Error() }
func (e *createdNotOpenError) Unwrap() error { return e.err }

// putBack is the rollback of the local config's move, for a run that failed
// after the move and before the new project was verified: the config FILE
// gets its original bytes back so `fugaro restore` can be run again, and the
// error says what state the run left — including what it does not undo.
func (rr *restoreRun) putBack(cause error) error {
	left := fmt.Sprintf("the checkout's fugaro.yaml was not touched, and the backup %s stays. Nothing the creating path did in the cloud is undone: if it got as far as creating %s, that project stays, with whatever it reached of its billing link, its Terraform state bucket, the installation's resources and any Secret Manager values it stored. A restore always creates a new project, so retry with another NEW_PROJECT_ID, and shut %s down (gcloud projects delete %s) if it is yours and unwanted", rr.backup, rr.newID, rr.newID, rr.newID)
	if werr := writeFileAtomic(rr.path, rr.old); werr != nil {
		return restoreWrap(cause, "nothing was restored, and the project config %s could NOT be put back (%v): it names %s, so copy its backup %s over it by hand before retrying; %s", rr.path, werr, rr.newID, rr.backup, left)
	}
	return restoreWrap(cause, "nothing was restored: the project config %s is back as it was (it names %s again, so the restore can be retried); %s", rr.path, rr.lc.GCPProject, left)
}

// restoreRoot is ROOT as the restore reads it: the absolute root the managed
// apply will run, and the checkout it is in — which is where the gcp_project:
// line lives.
//
// managedRoot is the check, and it is the apply's own: a root outside the
// checkout of the project it belongs to is a root nobody reviewed. The
// restore adds nothing to it and subtracts nothing from it.
func restoreRoot(ctx context.Context, lc *localcfg.Config, rootArg string) (root, checkout string, err error) {
	abs, err := managedRoot(ctx, lc, rootArg)
	if err != nil {
		return "", "", err
	}
	co, err := checkoutProject(ctx, abs)
	if err != nil {
		return "", "", err
	}
	if co == nil {
		return "", "", userErr("the root %s is in no checkout with a fugaro.yaml", rootArg)
	}
	return abs, co.Root, nil
}

// restoreNewProjectID is the fresh-ID rule (D66). A deleted project's ID
// stays reserved for about thirty days, so a restore always creates a new
// one: passing the ID the installation already lives in is the mistake this
// catches, and it is caught before anything is created.
func restoreNewProjectID(lc *localcfg.Config, id string) error {
	if !config.GCPProjectRE.MatchString(id) {
		return userErr("%q is not a GCP project ID (6 to 30 characters: lowercase letters, digits and hyphens, starting with a letter and ending with a letter or a digit)", id)
	}
	if lc != nil && id == lc.GCPProject {
		return userErr("%s is the ID of the installation that was lost: a deleted project's ID stays reserved about thirty days, so a restore always creates a new one and updates the repository's gcp_project: line to it", id)
	}
	return nil
}

// restoreApplyCloud is the cloud flags the final apply is given.
//
// --gcp-project is dropped, and only it: by the time the apply runs, the
// project config has moved to the new project, and a --gcp-project that got
// this far named the OLD one (any other value would have been refused by the
// first selection). Handing it on would make the apply's own selection refuse
// at the last step — after the project exists and the config has moved. The
// rest of the flags (--config, --project, --region) name the same project
// before and after the move and are passed through unchanged.
func restoreApplyCloud(o cloudOptions) cloudOptions {
	o.gcpProject = ""
	return o
}

// restoreConfigPath is the project config this run moves, and it must be a
// file. A selection with no path is the config published to the runs bucket:
// it is read from a bucket and nothing may write it, so an installation whose
// only config is the shared one is not a restore's to move — that is a first
// run for this machine, which is fugaro setup's job and not a weaker second
// one of this command's.
func restoreConfigPath(sel localcfg.Selection, lc *localcfg.Config) (string, error) {
	if sel.Path == "" {
		return "", userErr("%s's project config was read from the shared config in gs://fugaro-runs-%s (selected by %s), and a bucket object cannot be moved: run fugaro setup --name %s in the new project to write this machine's own project config, then restore into it", lc.Name, lc.GCPProject, sel.From, lc.Name)
	}
	return sel.Path, nil
}

// budgetOf is the monthly amount the new project is created with (D64, Task
// 4.2), and the billing account it is linked to — and it is where a restore
// of a project that is not managed is refused.
//
// The rule (stated in the PR and in docs/design/managed-projects.md's D66
// row), whatever flags are given:
//
//   - The OLD project's creation fact is read whenever the old project can be
//     read: the project answers Resource Manager, is ACTIVE, and its runs
//     bucket answers (restoreOldReadable). Then a definite "not managed" is a
//     refusal — the flags do not change it, because a restore is a rebuild of
//     a MANAGED project and not a way to make any installation into one — and
//     a managed one gives the amount, unless --budget and --budget-currency
//     are given, which then win.
//   - When the old project cannot be read — deleted, closed, or no longer
//     the caller's to read: the case a restore is for — there is no fact to
//     read, and the run proceeds only on --budget and --budget-currency, with
//     the typed confirmations as designed. Without them it is a refusal
//     naming the flags: an absent amount is not an unlimited one.
//   - A read that did not answer (a 5xx, a cancelled context) is a refusal
//     either way: it is neither "gone" nor "managed".
//
// ReadBudget is the same read IsManaged is (one object, one proof), so the
// amount can never come from an object the marker read would refuse.
//
// The billing account has no source to read. The creation fact is not a
// credential and records no account, so --billing-account is required on
// every restore. That is a fact about the marker's shape as it is on this
// branch, and it is stated in the PR: a Task 4.2 that records the account in
// the fact is the change that would let this step read it instead.
func (rr *restoreRun) budgetOf(ctx context.Context, c *infra.Clients) (*managed.Budget, error) {
	o := rr.o
	old := rr.lc.GCPProject
	if o.billingAccount == "" {
		return nil, userErr("a restore creates a project and links it to billing, and --billing-account is required: the creation fact of %s records the monthly amount (Task 4.2) and records no account, so this one is always yours to give, together with --budget and --budget-currency if you do not want the amount read from the fact", old)
	}
	var flags *managed.Budget
	switch {
	case o.budget != 0 && o.budgetCurrency != "":
		if o.budget < 0 {
			return nil, userErr("--budget %d: a monthly amount is above 0 (D64 requires one at creation, with no default)", o.budget)
		}
		flags = &managed.Budget{Amount: o.budget, Currency: o.budgetCurrency}
	case o.budget != 0 || o.budgetCurrency != "":
		return nil, userErr("--budget %d with --budget-currency %q: a monthly amount and its currency are one pair, and the creation was required to choose both. Give both, or neither to read them from %s's creation fact", o.budget, o.budgetCurrency, old)
	}
	if c == nil || c.Storage == nil || c.CRM == nil {
		return nil, userErr("nothing was restored: no client to read %s's creation fact with, and a restore reads it whenever it can, to refuse a project that is not managed", old)
	}
	readable, err := restoreOldReadable(ctx, c, old)
	if err != nil {
		return nil, userErr("nothing was restored: whether %s can still be read did not get an answer (%s), and a restore reads the old project's creation fact whenever it can", old, safetext.Field(err.Error()))
	}
	if !readable {
		if flags == nil {
			return nil, userErr("nothing was restored: %s cannot be read (deleted, closed, or not yours to read), so its creation fact cannot be either. Pass --budget, --budget-currency and --billing-account together: there is no amount to fall back on, and a restore never chooses one for you (D64)", old)
		}
		if rr.cmd != nil {
			fmt.Fprintf(rr.cmd.ErrOrStderr(), "fugaro: %s cannot be read, so its creation fact is not read and the amount is the one given: %d %s\n", old, flags.Amount, oneLine(flags.Currency))
		}
		return flags, nil
	}
	// The old fact is read through ReadBudgetAnswer, the read the managed
	// apply's gate makes, once: a valid version-2 fact is managed and gives
	// the amount (the flags still win over it, and a difference is said in
	// one line). A version-2 fact about this project whose figure fails
	// Budget.Check is refused as an invalid recorded budget, whatever the
	// flags say: it is not a fact this build wrote. Only when it answers
	// with no amount at all is IsManaged asked, to tell a managed version-1
	// fact (the flags are then required) from a project that is not managed
	// (refused whatever the flags say).
	answer, err := managed.ReadBudgetAnswer(ctx, c.Storage, c.CRM, rr.lc.Name, old)
	if err != nil {
		return nil, userErr("nothing was restored: the creation fact of %s could not be read, and a restore refuses a project that is not managed: %s", old, safetext.Field(err.Error()))
	}
	b, reason := answer.Budget, answer.Reason
	if answer.Invalid {
		why := "the recorded figure was refused"
		if cerr := b.Check(); cerr != nil {
			why = cerr.Error()
		}
		return nil, userErr("nothing was restored: the budget recorded in %s's creation fact is invalid: %s. A restore rebuilds a managed project from a fact this build wrote, and no flag stands in for one it did not", old, safetext.Field(why))
	}
	if answer.OK {
		if flags != nil {
			if *flags != b && rr.cmd != nil {
				fmt.Fprintf(rr.cmd.ErrOrStderr(), "fugaro: %s's creation fact records %d %s; the new project is created with the flags' %d %s\n",
					old, b.Amount, safetext.Field(b.Currency), flags.Amount, safetext.Field(flags.Currency))
			}
			return flags, nil
		}
		return &b, nil
	}
	isManaged, managedReason, err := managed.IsManaged(ctx, c.Storage, c.CRM, rr.lc.Name, old)
	if err != nil {
		return nil, userErr("nothing was restored: the creation fact of %s could not be read, and a restore refuses a project that is not managed: %s", old, safetext.Field(err.Error()))
	}
	if !isManaged {
		return nil, userErr("nothing was restored: %s can be read and is not managed (%s). A restore rebuilds a managed project, and no flag turns an installation that is not managed into one", old, safetext.Field(managedReason))
	}
	if flags != nil {
		return flags, nil
	}
	return nil, userErr("nothing was restored: %s's creation fact records no monthly amount (%s). Pass --budget, --budget-currency and --billing-account together: there is no amount to fall back on, and a restore never chooses one for you (D64)", old, safetext.Field(reason))
}

// restoreOldReadable is whether the OLD project can still be read at all:
// Resource Manager answers it with a number and as ACTIVE, and its default
// runs bucket answers. A 403 or a 404 from either, a project that is being
// deleted, or one with no number is "cannot be read" (false, nil) — the state
// a lost project is in. Any other failure is an error: no answer is not an
// answer, and budgetOf refuses on it.
func restoreOldReadable(ctx context.Context, c *infra.Clients, gcpProject string) (bool, error) {
	gone := func(err error) bool {
		var ge *googleapi.Error
		return errors.As(err, &ge) && (ge.Code == http.StatusForbidden || ge.Code == http.StatusNotFound)
	}
	p, err := c.CRM.Projects.Get(gcpProject).Context(ctx).Do()
	switch {
	case gone(err):
		return false, nil
	case err != nil:
		return false, err
	case p.ProjectNumber == 0 || (p.LifecycleState != "" && p.LifecycleState != "ACTIVE"):
		return false, nil
	}
	_, err = c.Storage.Buckets.Get("fugaro-runs-" + gcpProject).Context(ctx).Do()
	switch {
	case gone(err):
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}

// restoreConfig moves a local config from the project that was lost to the
// project this run creates, and returns it beside what it could not place in
// the new one (in the operator's words).
//
// Why the restore does this itself rather than passing --gcp-project to the
// creating path: localcfg.Config.Override refuses a --gcp-project that is not
// the selected config's, and rightly so — a project config pointed at another
// GCP project is a mistake, not an instruction. So the config moves first,
// and the creating path then finds the config it is told to install into.
//
// What moves, and why each is not a guess:
//
//   - gcp_project and runs_bucket: named after the project, so both follow it.
//   - bucket_url, registry_host, log_view, build.service_account,
//     terraform.state_bucket and the legacy registry are cleared rather than
//     derived here. The creating apply writes every one of them from the new
//     installation's own outputs (init's writeConfig), and a value naming the
//     old project would be planned before it does. registry is the legacy
//     shared image registry (localcfg.Config.Registry), which the per-
//     repository jobs' own spec still turns into every workflow's LegacyImage
//     (spec.go's `if lc.Registry != ""`): kept across a restore it would name a
//     registry inside the project that was lost, which is the class this
//     function's own list clears — the images a job starting from it would look
//     for are not there, and the create plans a job that cannot start.
//   - The budget backend's four project-naming fields (rtdb_url,
//     firebase_project, firebase_api_key, token_signer) go the same way, and
//     the operator's own settings in that block — the mode, the caps, the
//     token ceiling, the grace — stay. Where the new backend lives is the
//     operator's decision (fugaro update --firebase-project), not the restore's, and it
//     is named in what is returned.
//   - Every base_images entry whose reference names the old project's
//     registry goes, and the kinds it dropped are named in what is returned.
//     The reference is <region>-docker.pkg.dev/<old project>/…, so it is not
//     cleared by the creating apply: init reads base_images as the operator's
//     own choice (basePlanItem's isManaged), and a reference that is not a pin
//     this installation wrote is used as it is with a note — so the
//     installation would keep building FROM a registry in a project that no
//     longer exists, and nothing else would move it. Emptying the entry is
//     what makes the creating run pin this release's base image again (the
//     empty case is basePlanItem's pin), and an entry pointing anywhere else —
//     an image of the operator's own — is kept untouched: it names no project
//     of this installation's, and clearing it would replace a deliberate
//     choice with this release's base.
//
// What is refused rather than moved, because the restore cannot know the
// answer: a runs_bucket that is not the installation's default name, and a
// terraform.state_bucket that is a bucket of the operator's own. Both are
// buckets that exist somewhere specific, and a restore that silently pointed
// them at the new project would be guessing about where the operator's data
// is.
//
// lc is the run's selection — the config the flags selected and addressed the
// cloud with — and onDisk is the same config as its own file writes it, which
// is where every value that is not this move's to change comes from: the
// region above all, which --region overrides for the invocation and must not
// become the installation's on disk (planConfig).
func restoreConfig(lc, onDisk *localcfg.Config, newID string) (*localcfg.Config, []string, error) {
	if got, want := lc.RunsBucketName(), "fugaro-runs-"+lc.GCPProject; got != "" && got != want {
		return nil, nil, userErr("the project config names runs_bucket %s, and an installation whose runs bucket is not the default %s for %s cannot be moved by a restore: the marker, the delete list and the shared config are all read from the default name, so point the config at %s or move runs_bucket yourself", got, want, lc.GCPProject, want)
	}
	if s := lc.Terraform.StateBucket; s != "" && s != "fugaro-tfstate-"+lc.GCPProject {
		return nil, nil, userErr("the project config names terraform.state_bucket %s, which is a bucket of its own rather than %s's default: a restore cannot know where that bucket's successor should be. Remove terraform.state_bucket from the config (fugaro setup derives fugaro-tfstate-%s) or move it yourself", s, lc.GCPProject, newID)
	}
	var strandedBases []string
	next := *onDisk
	next.GCPProject = newID
	next.RunsBucket = "fugaro-runs-" + newID
	next.Bucket = ""
	next.Registry = ""
	next.RegistryHost = ""
	next.LogView = ""
	next.Build = onDisk.Build
	next.Build.ServiceAccount = ""
	next.Terraform = onDisk.Terraform
	next.Terraform.StateBucket = ""
	next.BaseImages, strandedBases = restoreBaseImages(onDisk)

	var left []string
	if len(strandedBases) > 0 {
		left = append(left, fmt.Sprintf("the base images of %s's registry (kinds: %s) did not come across: each reference named the registry of the project that was lost, and the creating run pins this release's base images again (fugaro update --base, or --base-image KIND=IMAGE to point a kind at an image of your own)", onDisk.GCPProject, strings.Join(strandedBases, ", ")))
	}
	if b := onDisk.Budget; b != nil {
		nb := *b
		if nb.RTDBURL != "" || nb.FirebaseProject != "" || nb.FirebaseAPIKey != "" || nb.TokenSigner != "" {
			nb.RTDBURL, nb.FirebaseProject, nb.FirebaseAPIKey, nb.TokenSigner = "", "", "", ""
			left = append(left, fmt.Sprintf("the budget backend of %s — its Realtime Database, its Firestore database, its restricted web API key and its token signer — did not come across: where the new one lives is your decision, so build it again with fugaro update --firebase-project %s (the mode, the caps and the token ceiling in the config are kept as they were), or leave it off if you want none", onDisk.GCPProject, firebaseProjectIDWord(next.GCPProject)))
		}
		next.Budget = &nb
	}
	return &next, left, nil
}

// restoreBaseImages is the base_images block as the new project may have it,
// beside the kinds it dropped (sorted, so the sentence the restore prints names
// them in one order). It drops exactly the entries whose reference is under the
// OLD installation's registry host — <region>-docker.pkg.dev/<old project>,
// which is where every ref this tool writes lives, both the pin through the
// remote repository and a copy in the installation's own fugaro-base — and
// keeps every other entry as it is.
//
// The host comes from infra.RegistryHost, the one derivation of it: the
// config's own registry_host when it records one, else <region>-docker.pkg.dev/
// <gcp_project>. It is read before the caller clears registry_host, and it is
// read through the config the operator's file holds, so a config whose
// registry_host names another project is refused by every other reader anyway
// (localcfg's checkRegistryHostProject, infra.registryHost) and nothing here
// has to decide what that would mean. A config that cannot name a host at all
// has no entry this installation's own registry served, and drops nothing.
func restoreBaseImages(lc *localcfg.Config) (kept map[string]string, dropped []string) {
	if len(lc.BaseImages) == 0 {
		return lc.BaseImages, nil
	}
	host, err := infra.RegistryHost(lc)
	if err != nil || host == "" {
		return maps.Clone(lc.BaseImages), nil
	}
	kept = make(map[string]string, len(lc.BaseImages))
	for kind, ref := range lc.BaseImages {
		if strings.HasPrefix(ref, host+"/") {
			dropped = append(dropped, kind)
			continue
		}
		kept[kind] = ref
	}
	if len(kept) == 0 {
		// No entry left is the same config as none written: the creating run
		// pins this release's base images for the kinds it needs.
		kept = nil
	}
	slices.Sort(dropped)
	return kept, dropped
}

// readConfirmLine reads one line from in, one byte at a time.
//
// It is byte-wise and not a bufio.Reader, and that is the whole point. The
// creating path below is handed the SAME reader this run's confirmations read
// from, so that it sees the user's terminal — stdinIsTerminal is a type
// assertion on an *os.File — and a bufio.Reader would break that twice over:
// the child would be handed the *bufio.Reader itself and every typed step of
// the creation would be left for the user ("not finished: 1 step(s) left for
// you") after this run had already moved the operator's project config; and
// the first read would buffer every line after it, so the child would find an
// empty input even if it did see a terminal.
func readConfirmLine(in io.Reader) (string, error) {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := in.Read(b)
		if n > 0 {
			if b[0] == '\n' {
				return string(line), nil
			}
			line = append(line, b[0])
			continue
		}
		if err != nil {
			return string(line), err
		}
	}
}

// whatToDo is the question's subject: what this run is about to make
// permanent, in one line. It names the amount, because it is the figure the
// operator is committing to on a billing account.
func (rr *restoreRun) whatToDo() string {
	return fmt.Sprintf("restore %s into the new GCP project %s, with a monthly budget of %d %s, apply the agent-authored root %s from zero, and move this machine's project config and the repository's gcp_project: line to it",
		rr.lc.Name, rr.newID, rr.budget.Amount, oneLine(rr.budget.Currency), rr.rootArg)
}

// whyPermanent is why this step is one initflow.Typed and not one --yes
// covers, and it names no flag: there is none to pass.
func (rr *restoreRun) whyPermanent() string {
	return "a restore creates a project on your billing account, writes a Terraform state from zero, and edits a committed file in the checkout: every step of it is money or permanent, so the project's name is typed here at a real terminal"
}

// confirm is the one typed confirmation of the run (initflow.Typed's shape,
// the same one confirmTypedStrict gives): a real terminal, and the project's
// name typed back. A pipe or a redirect confirms nothing, a wrong name
// changes nothing, and no flag stands in for either.
func (rr *restoreRun) confirm(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "⚠ CONFIRM (project %s): %s\n", rr.lc.Name, rr.whatToDo())
	if !stdinIsTerminal(cmd.InOrStdin()) {
		return userErr("%s: %s", initflow.NeedsTerminal("the typed confirmation of a restore"), rr.whyPermanent())
	}
	fmt.Fprintf(out, "Type %s to confirm: ", rr.lc.Name)
	line, err := readConfirmLine(cmd.InOrStdin())
	if err != nil && !errors.Is(err, io.EOF) {
		return userErr("reading the confirmation: %v", err)
	}
	if strings.TrimSpace(line) != rr.lc.Name {
		return userErr("not confirmed (the project's name was not typed); nothing was changed")
	}
	return nil
}

// planConfig is what the config's move will be, decided before the run's
// typed confirmation: restoreConfig's refusals are refusals a typed name does
// not help with, and the operator reads them before typing rather than after.
func (rr *restoreRun) planConfig() error {
	// The config as its own file writes it, beside the selection's copy: the
	// selection has this invocation's --gcp-project and --region applied to it
	// (selectProject → announce → localcfg.Config.Override), and the moved
	// config is written from that copy — so a --region given to address the old
	// project would become the installation's own region on disk, and the new
	// project's resource region with it (createArgs passes the moved config to
	// the creating path, which derives the region from it), without the
	// operator having asked for either. The flag's help says what it is: "GCP
	// region (overrides the project config's)". --gcp-project needs no such
	// treatment: Override refuses one that is not the config's own.
	onDisk, err := localcfg.Parse(rr.old)
	if err != nil {
		return userErr("the project config %s: %v", rr.path, err)
	}
	next, left, err := restoreConfig(rr.lc, onDisk, rr.newID)
	if err != nil {
		return err
	}
	rr.next, rr.left = next, left
	return nil
}

// writeConfig is the local config's move: the diff, a backup of the old file,
// and the write through init's own helpers. It runs behind the run's typed
// confirmation, so it asks nothing of its own, and it takes the value
// planConfig decided.
//
// There is no "already up to date" branch, because there is no such run: the
// config names the OLD project (newID == lc.GCPProject is refused before
// this) and the move writes gcp_project: <newID>, so the two are never equal.
func (rr *restoreRun) writeConfig() error {
	data, err := rr.next.Marshal()
	if err != nil {
		return userErr("marshalling the moved project config: %v", err)
	}
	if _, err := localcfg.Parse(data); err != nil {
		return userErr("the moved project config: %v", err)
	}
	w := rr.cmd.OutOrStdout()
	fmt.Fprintf(w, "The project config %s changes:\n%s", rr.path, lineDiff(string(rr.old), string(data)))
	if err := os.MkdirAll(filepath.Dir(rr.path), 0o700); err != nil {
		return userErr("%v", err)
	}
	if rr.old != nil {
		backup, err := backupFile(rr.path, rr.old, time.Now())
		if err != nil {
			return userErr("backing up the project config: %v", err)
		}
		rr.backup = backup
		fmt.Fprintf(w, "backed up the old project config to %s\n", backup)
	}
	if err := writeFileAtomic(rr.path, data); err != nil {
		return userErr("writing the project config: %v", err)
	}
	fmt.Fprintf(w, "wrote %s\n", rr.path)
	for _, l := range rr.left {
		fmt.Fprintf(w, "note: %s\n", l)
	}
	return nil
}

// createArgs is the creating path's flags, and nothing else: `fugaro setup
// --create-project --managed` with this run's inputs. It is a function of its
// own so that what the restore delegates to is one thing a reader (and a
// test) can see whole.
//
// --config is the moved project config's own file, and it is not optional:
// without it the creating run selects a project from scratch, and an operator
// who had to name the project (or a machine with several project configs)
// would give the child either no selection or another project's config — one
// whose gcp_project then contradicts --gcp-project. Naming the file this run
// just wrote is the one selection that cannot be another project.
func (rr *restoreRun) createArgs() []string {
	args := []string{
		"--create-project",
		"--config", rr.path,
		"--gcp-project", rr.newID,
		"--link-billing", rr.o.billingAccount,
		"--managed",
		"--budget", strconv.FormatInt(rr.budget.Amount, 10),
		"--budget-currency", rr.budget.Currency,
		"--billing-account", rr.o.billingAccount,
	}
	if rr.o.displayName != "" {
		args = append(args, "--display-name", rr.o.displayName)
	}
	if rr.o.parent != "" {
		args = append(args, "--parent", rr.o.parent)
	}
	return args
}

// create is the creating path: literally `fugaro setup --create-project
// --managed`, built by the same constructor the command tree uses and run
// with this run's stdin, stdout and stderr.
//
// Delegating is the point and not a shortcut. The creating run is what writes
// the project's creation fact for the project it created and what sets up
// D77's state bucket and binds the deployer to it (markCreated), and what
// removes every confirmation would be a second implementation of the creation
// that skipped one of them. So the restore runs the creation, with the
// creating run's own confirmations — the project's ID typed, the billing
// account typed, and the installation's own review — exactly as a person
// running it by hand would.
//
// The stdin is the command's own, unchanged: the child has to see the user's
// terminal (stdinIsTerminal is a type assertion on an *os.File), and this
// run's own confirmations read it a byte at a time so that they take one line
// and leave the rest — see readConfirmLine.
func (rr *restoreRun) create(ctx context.Context, made *initCreation) error {
	cmd := rr.cmd
	args := rr.createArgs()
	fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: creating the project through the same path fugaro setup --create-project --managed uses: fugaro setup %s\n", strings.Join(args, " "))
	// strictCreate: the child refuses a project that is already there, even
	// one an earlier run of fugaro setup created; made: the child's own account of what it did, which
	// verifyCreated checks.
	child := restoreInitCmd(&initOptions{strictCreate: true, created: made})
	// The child is run as a root of its own, so cobra would print its error
	// (raw, past ErrorText's sanitising) and its whole usage before returning
	// it — and main prints the error again. Both are silenced: the error comes
	// back to this command and is printed once, sanitised, like any other.
	child.SilenceErrors, child.SilenceUsage = true, true
	child.SetArgs(args)
	child.SetIn(cmd.InOrStdin())
	child.SetOut(cmd.OutOrStdout())
	child.SetErr(cmd.ErrOrStderr())
	return child.ExecuteContext(ctx)
}

// anchor moves the checkout's gcp_project: line to the new project — the one
// field of the repository the restore changes, and the one every teammate's
// CLI and every CI job reads the shared config from.
//
// The edit keeps init's own discipline: the diff, then a yes at a terminal
// (anything else, and a pipe, leaves the file alone and prints the line to
// add), and a read-back that has to mean what the file meant plus the one
// value. There is no --yes here: a restore has none.
func (rr *restoreRun) anchor(cmd *cobra.Command) error {
	path := filepath.Join(rr.checkout, "fugaro.yaml")
	old, err := readFugaroYAML(path)
	if err != nil {
		return userErr("the checkout's %s: %v", path, err)
	}
	out, changed, err := restoreGCPProjectLine(old, rr.lc.GCPProject, rr.newID)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if !changed {
		fmt.Fprintf(w, "%s already names gcp_project: %s\n", path, rr.newID)
		return nil
	}
	fmt.Fprintf(w, "%s\n%s", path, lineDiff(string(old), string(out)))
	addLine := fmt.Sprintf("to let teammates use this installation without setup, change the gcp_project line of %s yourself: gcp_project: %s\n", path, rr.newID)
	if !stdinIsTerminal(cmd.InOrStdin()) {
		fmt.Fprint(w, addLine)
		return nil
	}
	fmt.Fprintf(w, "Write this to %s? [y/N]: ", path)
	line, _ := readConfirmLine(cmd.InOrStdin()) // an ended input is a no
	if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
		fmt.Fprint(w, addLine)
		return nil
	}
	if err := writeFileAtomic(path, out); err != nil {
		return userErr("writing %s: %v", path, err)
	}
	fmt.Fprintf(w, "Updated %s with the new gcp_project. Review it with git diff and commit it like any change: every teammate's CLI and every CI job or pin that runs fugaro against the repository reads the shared config from this line.\n", path)
	return nil
}

// restoreGCPProjectLine is the gcp_project: line's move from oldID to newID,
// leaving every other byte of the file as it is.
//
// The three cases, and none of them is a guess:
//
//   - The file already names newID: nothing to do, and nothing written.
//   - The file has no line at all (an installation onboarded before the
//     field, or one that never ran update --anchor): init's own editor adds it
//     (setGCPProjectLine), so the line lands where init puts it.
//   - The file names oldID, which is the case a restore is for.
//
// Anything else — a line naming a third project, a gcp_project key nested
// where a top-level one belongs, or an edit that does not read back as
// intended — is a refusal naming the line to write by hand, never a rewrite.
// A reviewed value in a committed file is not the restore's to change on its
// own word.
func restoreGCPProjectLine(data []byte, oldID, newID string) ([]byte, bool, error) {
	if !config.GCPProjectRE.MatchString(newID) {
		return nil, false, userErr("%q is not a GCP project ID", newID)
	}
	// byHand is the refusal of every shape this editor will not handle, in
	// setGCPProjectLine's own words: the file is not edited and the line to
	// write is in the message.
	byHand := func(format string, args ...any) error {
		return userErr("%s; fugaro.yaml is not edited, change that line yourself: gcp_project: %s", fmt.Sprintf(format, args...), newID)
	}
	cur, err := config.GCPProjectOf(data)
	if err != nil {
		return nil, false, byHand("fugaro.yaml cannot be read for gcp_project (%v)", err)
	}
	switch {
	case cur == newID:
		return data, false, nil
	case cur == "":
		// No line at all (an installation onboarded before the field): init's
		// own editor adds it, where init puts it.
		out, changed, err := setGCPProjectLine(data, newID)
		if err != nil {
			return nil, false, userErr("%v", err)
		}
		return out, changed, nil
	case cur != oldID:
		// cur is the checkout's own text, which a run can write: one line.
		return nil, false, byHand("fugaro.yaml names gcp_project: %s, and this restore moves %s to %s (a reviewed value in a committed file is never rewritten)", oneLine(cur), oldID, newID)
	}

	// The line the restore is for: read, so it is the top-level one the
	// parser read, and replaced with the same framing it had.
	lines := bytes.SplitAfter(data, []byte("\n"))
	at := -1
	for i, l := range lines {
		if gcpProjectKeyRE.Match(bytes.TrimRight(l, "\r\n")) {
			at = i
		}
	}
	if at < 0 {
		return nil, false, byHand("fugaro.yaml's gcp_project: is not a one-line top-level key")
	}
	line := bytes.TrimRight(lines[at], "\r\n")
	eol := lines[at][len(line):]
	rewritten, err := rewriteGCPProjectValue(line, newID)
	if err != nil {
		return nil, false, byHand("%v", err)
	}
	lines[at] = append(rewritten, eol...)
	out := bytes.Join(lines, nil)

	// Read it back, as setGCPProjectLine does: the file must mean what it
	// did, plus the one value.
	_, before := config.Parse(data)
	_, after := config.Parse(out)
	oldProject, _ := config.ProjectOf(data)
	newProject, perr := config.ProjectOf(out)
	got, gerr := config.GCPProjectOf(out)
	if !slices.Equal(before, after) || perr != nil || newProject != oldProject || gerr != nil || got != newID {
		return nil, false, byHand("the edited file would not read back as intended")
	}
	return out, true, nil
}

// rewriteGCPProjectValue replaces the VALUE of a gcp_project: line and nothing
// else — the key's own spelling, the spacing after the colon, the quoting the
// value carried and any trailing comment are the file's, because the restore
// edits one value of a committed file and every other byte of it is the
// operator's. It is the difference between "the line now names the new
// project" and "the line is now what this program would have written": the
// second drops a comment that said why the value was what it was, and the
// read-back check cannot see it, because a comment and a pair of quotes are
// not part of the parsed value.
//
// The value is read with the quoting YAML gives it — a double-quoted scalar
// (with the backslash escapes it may hold), a single-quoted one, or a bare
// scalar that ends at the first blank or "#" — and written back in the same
// quoting. A project ID needs no escaping, so the two forms cannot differ in
// what they mean; what is kept is the file's.
func rewriteGCPProjectValue(line []byte, id string) ([]byte, error) {
	colon := bytes.IndexByte(line, ':')
	if colon < 0 {
		return nil, errors.New("the gcp_project: line has no colon")
	}
	// head is the key and the colon with the spacing after it, as written.
	end := colon + 1
	for end < len(line) && (line[end] == ' ' || line[end] == '\t') {
		end++
	}
	head, rest := line[:end], line[end:]
	tail, quote, err := valueTailAndQuote(rest)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(line)+len(id))
	out = append(out, head...)
	out = append(out, quote...)
	out = append(out, id...)
	out = append(out, quote...)
	out = append(out, tail...)
	return out, nil
}

// valueTailAndQuote reads the YAML scalar a gcp_project: line drops after the
// colon — a double-quoted one (with the backslash escapes it may hold), a
// single-quoted one, or a bare one that ends at the first blank or "#" — and
// returns what follows it on the line (the spacing before a trailing comment,
// and the comment) together with the quote character the scalar opened with,
// "" for a bare one. Its two errors are shapes the read above has in practice
// already refused — a line with no value at all parses as no value, and an
// unclosed quote does not parse — and they are kept so that the editor refuses
// a line it cannot read rather than rebuilding it: the rule here is that every
// shape this function does not understand is the operator's, not this
// program's to write.
func valueTailAndQuote(rest []byte) (tail []byte, quote string, err error) {
	if len(rest) == 0 {
		return nil, "", errors.New("the gcp_project: line has no value")
	}
	if q := rest[0]; q == '"' || q == '\'' {
		for i := 1; i < len(rest); i++ {
			if q == '"' && rest[i] == '\\' {
				// The escape and what it escapes are inside the scalar whatever
				// the next byte is, so a quoted \" does not close it.
				i++
				continue
			}
			if rest[i] == q {
				return rest[i+1:], string(rune(q)), nil
			}
		}
		return nil, "", errors.New("the gcp_project: value's quote is never closed")
	}
	i := 0
	for i < len(rest) && rest[i] != ' ' && rest[i] != '\t' && rest[i] != '#' {
		i++
	}
	return rest[i:], "", nil
}

// notRestored is D66's list: what the new project does NOT have, as a list
// rather than as a caveat, because an operator who is not told will look for
// it and find nothing. The one entry that is about something being
// re-created rather than lost is said as such (D75's deployer account), so
// nobody has to discover which of the two the new project holds.
//
// The two GCS objects the repository does not hold are the Gap's finding 32
// (Open item 5): the re-publication path for exactly the project layer and
// the project's recipes is named and not designed, so this command does not
// re-publish them and does not pretend to. It names them, the object each
// lives at, and the one command that publishes each — and the old project,
// because while a deleted project's ID is reserved its bucket is where they
// still are.
func restoreNotRestored(old *localcfg.Config, newID string) []string {
	bucket := "fugaro-runs-" + old.GCPProject
	return []string{
		fmt.Sprintf("the runs that were in flight, and their RTDB counters: they ended with %s, and nothing here starts them again", old.GCPProject),
		fmt.Sprintf("the spend history and the kill switch state: they lived in %s's Firestore database and RTDB, and a restore carries neither across", old.GCPProject),
		fmt.Sprintf("the Firestore and RTDB application data of %s: the usage, the counters and every document the project held are gone, and this restore does not copy them", old.GCPProject),
		"the Firestore database's location: the new project's is chosen once, by fugaro update --firebase-project <gcp-project-id>, and it can never be changed afterwards (a database of another location is never adopted)",
		"every secret value: the secrets are in the old project's Secret Manager and are never carried across. Set them again in the new project with fugaro secrets set, and treat the old values as disclosed if the old project was not deleted for you alone",
		fmt.Sprintf("the plan files that were never stored: nothing reads a plan out of %s's state, and nothing here reads prior state either — the root is applied from zero", old.GCPProject),
		fmt.Sprintf("%s: the owner's delete list is NOT carried across, and nothing this restore runs re-creates it. Write it again with fugaro allow-delete, and until you do, every delete and every replacement in %s is refused (D74)", managed.AllowDeleteObject, newID),
		fmt.Sprintf("the project layer, gs://%s/%s: the repository does not hold it and this restore does not re-publish it — there is no path for that yet (the Gap's finding 32). Publish it again yourself, from a copy, with fugaro config publish", bucket, config.LayerKey),
		fmt.Sprintf("the project's recipes, gs://%s/%s<name>.yaml: the repository does not hold them and this restore does not re-publish them either. Publish each one again with fugaro recipes publish", bucket, recipe.ObjectPrefix),
		fmt.Sprintf("and the one thing that is RE-CREATED rather than lost: %s, with the deployer's custom roles and their bindings on gs://%s (the fmgd- fenced ones only where the installation opts in, which nothing does in this release). The creating path builds it with the rest of the installation (D75), so a managed apply works in %s without a step of its own", managedmode.DeployerAccount(newID), managedmode.ManagedStateBucketName(newID), newID),
	}
}

// report ends the run with D66's list. It is the last thing the command does,
// after the apply, so a reader takes away what the new project has and what it
// does not in one place.
func (rr *restoreRun) report() error {
	w := rr.cmd.OutOrStdout()
	fmt.Fprintf(w, "\n%s is restored into %s. NOT restored:\n", rr.lc.Name, rr.newID)
	for _, l := range restoreNotRestored(rr.lc, rr.newID) {
		fmt.Fprintf(w, "  - %s\n", l)
	}
	for _, l := range rr.left {
		fmt.Fprintf(w, "  - %s\n", l)
	}
	return nil
}
