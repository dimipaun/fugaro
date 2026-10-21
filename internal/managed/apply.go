// The managed apply: the one path that applies a saved plan in a managed
// project, and the three things that replace the coding-agent refusal D55
// removes there (docs/design/managed-projects.md, D55 and D76).
//
// What it is: the agent-authored root is copied into this command's own
// workdir, the backend is set by the command itself (D77's state bucket, never
// one the configuration chose), the root is planned into a saved file, the
// file is gated (Tasks 1.0-1.3) with the addresses the owner's own object
// approves (Task 1.4, D72/D74), and the saved file is applied as the D75
// deployer account (Task 1.5) rather than as the operator.
//
// What it is not, stated rather than implied, because this is the change the
// design's §6 item 4 is about: none of the three replacements is a boundary
// against an agent that goes around the program. The agent shares the
// container and the user with the runner, reads the runner's environment
// through /proc and the job's service account through the metadata server
// (SECURITY.md), so it can apply directly, write the marker with the
// operator's own credentials, or unset its marker and run this command
// itself. What the gate, the delete list and the marker stop is the accident:
// a session that goes through this program applies only what the owner's
// rules admit.
//
// And every path below is behind Request.Enable, whose zero value is closed
// (enable.go). The required budget (Task 4.2) is in the build, and the one
// thing that opens the gate is the
// apply-time budget read (Task 4.4) — internal/cli's managed apply makes it and
// hands the answer in, and this package refuses a gate that read did not open
// as its own first step.
//
// The whole-project inventory (Task 4.1, inventory.go) is consulted here, and
// this is the wiring Task 4.5 is: Request.Inventory carries the caller's
// read of the project against what the installation's own state accounts for,
// and its Fallback is the third fact of the confirmation rule below — where it
// fires, the run keeps its confirmations (D59). What that means in the two
// sessions this command runs in: a person at a terminal is asked, with the
// inventory's own warning printed first, and a coding agent's session is
// refused, because the confirmation D55 removes for a managed project is
// exactly the confirmation the fallback keeps. Nothing here halts and nothing
// here deletes: a plan the gate admitted is still applied, by a session that
// may confirm it.
//
// Request.Inventory is zero-value-closed (check.go/InventoryCheck): a caller
// that read nothing falls back rather than proceeding, so forgetting the read
// lands on the safe side of the rule.
// TestApplyConsultsTheInventory pins the wiring, and the two directions of it:
// a foreign resource keeps the confirmations, and an inventory that could not
// be read keeps them too.
package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The refusals this file's own rules make. They are values as well as
// messages, so a caller can match on them without reading English.
var (
	// ErrNotManaged is the creation fact's answer: a project the marker read
	// did not say is managed is applied by nobody's session, and this command
	// applies nothing for it.
	ErrNotManaged = errors.New("the project is not managed")
	// ErrSessionAddresses is an address the applying command was given on its
	// own command line: refused by name rather than honoured, whatever the
	// owner's object says (D72/D74).
	ErrSessionAddresses = errors.New("the applying command named addresses of its own")
	// ErrRefused is the gate's refusal: the plan is not applied, and the only
	// action a false verdict permits is a confirmation this session cannot
	// give.
	ErrRefused = errors.New("the managed gate refused the plan")
	// ErrPreflight is the root's own configuration: a file this command
	// cannot read as HCL, a block whose whole point is to act outside the plan
	// (a backend, a cloud block, provider_meta, an ephemeral or an action
	// block), or something that would run before the gate does — a data
	// source, a provider other than Google's own, a function that reads the
	// applier's disk, a remote module, an override file or the root's own lock
	// file (preflight.go).
	ErrPreflight = errors.New("the root's configuration is not one a managed apply runs")
	// ErrProviderSetting is a provider setting that reached terraform's
	// environment. tf.Env's allowlist drops every one of them, and this is the
	// assertion that says so at run time rather than only in a test.
	ErrProviderSetting = errors.New("a provider setting is in terraform's environment")
	// ErrPlanChanged is the saved plan file not being the bytes the gate
	// admitted (Verdict's own contract): the gate's verdict is about a plan,
	// not about a file name, so the bytes are hashed at gate time and again
	// before the apply.
	ErrPlanChanged = errors.New("the saved plan changed between the gate and the apply")
	// ErrRequest is a Request this command cannot run at all: no workdir, no
	// terraform, no gate inputs.
	ErrRequest = errors.New("the managed apply has nothing to run")
	// ErrNotConfirmed is D55's confirmation rule refusing the step: the
	// session asking for the apply is not one that may apply a plan of this
	// class — a coding agent's session in a project the read did not say is
	// managed, or a session with neither a terminal nor a marker. It is a
	// refusal and not a fallback: nothing is applied.
	ErrNotConfirmed = errors.New("this session may not confirm the apply")
)

// Fact is the creation fact's read, as infra/managed.IsManaged answered it:
// whether the project is managed, the one line it gave for its answer, and the
// error when the read could not be made at all.
//
// It is passed in rather than read here because the read needs the storage and
// Resource Manager clients, which internal/cli holds and this package does not
// (the same split statebucket.go's StateStore follows). The zero value is "not
// managed", which is the direction every mistake has to fall in.
type Fact struct {
	// Managed is IsManaged's first result: true only with a nil error.
	Managed bool
	// Reason is IsManaged's one-line reason, empty when Managed.
	Reason string
	// Err is IsManaged's error, when the read could not be made.
	Err error
}

// Request is everything a managed apply is told. Every field is set by the
// caller that holds the cloud clients (internal/cli's fugaro managed apply);
// nothing here is read from a flag the applying session could choose.
type Request struct {
	// Enable is the gate of enable.go, made by the caller from the apply-time
	// budget read, and the FIRST thing Apply reads: a zero value — what a
	// caller that read nothing passes — refuses.
	Enable Enable
	// Project is the managed Fugaro project's name, GCPProject its GCP
	// project. The state bucket is derived from GCPProject and from nothing
	// else (managed.ManagedStateBucketName).
	Project, GCPProject string
	// Fact is the marker read's answer.
	Fact Fact
	// Workdir is the workdir terraform runs in: Dir holds terraform's CLI
	// config, Root the copy of the agent-authored root. It is this command's
	// own (infra.ManagedWorkdir), never the checkout.
	Workdir *infra.Workdir
	// Terraform builds the terraform this apply runs, in Workdir.Root, with
	// the environment TerraformEnv built (tf.Env's allowlist, and the
	// assertion over it) and the credential of the deployer account of D75 in
	// it. It is a function and not a *tf.TF because the credential is not
	// known until the account has been assumed, and every terraform this apply
	// runs — init, plan, the show the gate reads, and the apply — has to run
	// as it: the deployer holds this root's state bucket (D77) and the
	// operator's identity may not, and a plan read as one identity and applied
	// as another is not the plan the gate admitted. A nil one is refused
	// (ready).
	//
	// The function it returns alongside is the cleanup of whatever the
	// terraform needs that outlives the call — the deployer's credential file,
	// in production — and Apply runs it once every terraform it built has
	// finished: after the apply, or on any refusal, error or panic in between.
	// A credential file removed before init would leave terraform naming a
	// file that is not there.
	Terraform func(cred Credential) (*tf.TF, func(), error)
	// Cover is the managed instantiation of the scope-and-attribute gate: the
	// managed project's own allowlist (Task 1.1), with the projects, region
	// and buckets of this installation.
	Cover Cover
	// AllowDelete is the owner's own object, read fail-closed by the caller
	// (infra/managed.GuardAllowList): for each address they approve, the
	// identity of the object that was at it when they approved it (Task 6.3).
	// An empty list allows no delete and no replace at all, and so does an
	// entry about another object than the one a plan removes; either is what
	// the gate then refuses.
	AllowDelete []tf.DeleteEntry
	// Session is what the applying command's OWN command line named as
	// addresses it may delete or replace. fugaro managed apply registers no
	// such flag, so the production value is nil, and internal/cli asserts that
	// it is (a command-line flag of this command is refused rather than
	// ignored) — the field exists because the rule is enforced by the one
	// function that runs an apply rather than left to each caller, and
	// anything here is refused by name whatever the owner's object says.
	Session []string
	// Inventory is the whole-project inventory's answer (Task 4.5, design
	// D59): the caller's Cloud Asset read of the project against what the
	// installation's own state accounts for. It is the third fact of the
	// confirmation rule — a fallback keeps this run's confirmations, which in
	// a coding agent's session means the plan is not applied unattended and in
	// a person's means the person is asked (and warned) as they would be
	// without the managed case at all.
	//
	// The zero value FALLS BACK (InventoryCheck's contract): a caller that
	// made no read is a run that checked nothing, and "checked nothing" must
	// not read as "found nothing".
	Inventory InventoryCheck
	// Conditions is what the session asking for the apply says about who is
	// there (initflow.Conditions: the marker its environment carries, a
	// terminal, --yes). It is the third input of D55's confirmation rule, and
	// it is a field rather than a caller's decision because the rule has to be
	// asked where all three of its inputs exist at once: the fact read above,
	// the gate's verdict, and this. Apply asks it (confirm) after the verdict
	// and before the apply, and the zero value is refused — a caller that
	// passes nothing is not a session that may confirm anything.
	Conditions initflow.Conditions
	// Ask is the prompt a person at a terminal answers before the apply: the
	// question names the project, the saved plan's hash and its counts, and
	// only a true answer applies. It is asked when the session D55's rule
	// admits is a person's (no coding agent's marker) — a terminal alone is
	// not a confirmation — and a nil one refuses that session rather than
	// apply unasked. A coding agent's session in a managed project is not
	// asked (D55's answer): the gate is its confirmation.
	Ask func(question string) (bool, error)
	// Imports are the import blocks the root's discovery wrote: an import is
	// admitted only when its address and ID are exactly one of them.
	Imports []tf.ImportKey
	// Access and Impersonator are D75's resolution: whether the deployer
	// account is there, whether this caller may assume it, and the assumption
	// itself. A nil one is refused rather than treated as nothing to check.
	Access       Access
	Impersonator Impersonator
	// Out receives terraform's progress output and this command's own lines.
	// Nil discards them.
	Out io.Writer
}

// Result is what an apply did, for the caller that prints it. Changed is false
// when the plan was empty, in which case nothing was gated and nothing was
// applied.
type Result struct {
	Changed  bool
	PlanPath string
	PlanHash string
	Verdict  Verdict
}

// RefusedError is the gate's refusal, carried out of Apply with the verdict
// that made it: every refusal, each with the gate that made it, the address it
// is about and the rule's own words. Nothing is applied when this is returned.
//
// Advice is the one sentence this refusal ends with, when the mode has one:
// the managed sentence (DeleteAdvice), which names the owner's object and the
// owner-run command that writes it rather than a flag of the applying command.
// It is a field of the error and not of the Verdict because it is what the
// caller says about the mode — a verdict is a judgement about a plan (plan.go)
// and knows nothing about who is applying it — and it is rendered here, where
// the refusal a person reads is rendered, so the sentence has one home.
type RefusedError struct {
	Verdict Verdict
	Advice  string
}

// Error is the refusal as a person reads it: one line per refused change — at
// most safetext.FieldListMax of them, with the rest counted in this program's
// own words — each with the gate that refused it and the address and rule's
// own words, and then the mode's advice when there is one. The count in the
// first line is every refusal, listed or not.
//
// Every part of it that can come from somewhere else is one bounded field
// first (safetext.Field): an address is read out of a plan and out of objects
// other people wrote, and a newline, a C1 control or a bidi override carried
// through it would be this program printing text of someone else's choosing —
// in text the session that reads it acts on — and a value of any length would
// push the rest out of sight. A rule's own words are rendered by refusalWhy and
// not by Field: the two gates that judge a change against the owner's delete
// list are bounded already where the rule built them (refusalWhyMax), and
// Field's own cut would fall inside the identity a removal rule prints for its
// reader to approve, which is the one part of the line that has to reach the
// terminal whole — every other gate's why is one Field, as it was. The advice
// is this program's own and is rendered as it is.
func (e *RefusedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s, so nothing is applied (%d refusal(s))", ErrRefused, len(e.Verdict.Refusals))
	listed := e.Verdict.Refusals
	if len(listed) > safetext.FieldListMax {
		listed = listed[:safetext.FieldListMax]
	}
	for _, r := range listed {
		b.WriteString("\n  ")
		// The gate's name is this program's own, but it is rendered the way
		// the rest is: the shape of the line is the same whatever any of its
		// parts holds.
		b.WriteString(safetext.Field(r.Gate))
		b.WriteString(": ")
		if r.Address != "" {
			b.WriteString(safetext.Field(r.Address))
			b.WriteString(": ")
		}
		b.WriteString(refusalWhy(r.Why))
	}
	if len(e.Verdict.Refusals) == 0 {
		b.WriteString(": the verdict refused and listed nothing, which is a refusal all the same")
	}
	// The listing stops at the cap and says how many it did not print, in this
	// program's own words: how many changes a plan refuses is the plan's to
	// choose, and the count in the line above is the whole list's, so a
	// truncated listing is flagged and never read as a short one.
	if n := len(e.Verdict.Refusals) - len(listed); n > 0 {
		b.WriteString("\n  (")
		b.WriteString(safetext.FieldListNote(n))
		b.WriteString(")")
	}
	if e.Advice != "" {
		b.WriteString("\n")
		b.WriteString(e.Advice)
	}
	return b.String()
}

func (e *RefusedError) Unwrap() error { return ErrRefused }

// DeleteAdvice is the one sentence a managed apply's deletion refusal ends
// with: what to do, and who can do it. It is not a managed `fugaro init`'s
// sentence — init's guard is tf.Guard over its own `--allow-delete` addresses,
// fugaro's own roots, and its refusal ends with internal/cli's
// initManagedDeleteAdvice, which does not point at this list at all: the list
// approves deletes in the project's agent-authored root, which is what a
// managed apply applies, and nothing init plans.
// The owner's own object is named by its whole address, the command that writes
// it is named, and no address of an applying command's own is accepted — so the
// loop a refusal starts is refusal → the owner copies the entry the refusal's
// own line printed, an address AND the identity of the object the plan removes
// (tf.RemoveIdentityText) → fugaro allow-delete → the next plan passes (D74,
// and the entries rule of Task 6.3: what is listed is an entry, so the sentence
// does not promise that an address alone can be approved, which is a command
// that would refuse it).
//
// It names no flag and no agent marker, and that is the rule rather than the
// wording: a refusal is read by the session it refuses — in a managed project
// that session may be a coding agent's — and a flag or a variable named here is
// a recipe for getting past a check, which has no business in text an agent
// reads (internal/initflow's IDEHint, and the plan's Task 3.2). The flag this
// project's apply refuses is therefore described and never spelled, and the
// test that holds it is the negative one over these strings.
//
// gcpProject is the GCP project the object is read from; with no project name
// there is no bucket to name, and the sentence says only what to do and who
// does it.
func DeleteAdvice(gcpProject string) string {
	where := "the owner's own list"
	if gcpProject != "" {
		where = "the owner's own list at gs://fugaro-runs-" + safetext.Field(gcpProject) + "/fugaro/allow-delete.json"
	}
	return "the addresses a managed project may delete or replace are " + where +
		", which the owner writes with their own credentials and the owner-run command fugaro allow-delete, and no address named on the command line of an applying command approves one: what the owner approves is an entry — an address AND the identity of the object the plan removes, which the refusal above printed — so an address alone is not an approval"
}

// Apply is the managed apply: the gate's verdict is the whole of the decision,
// and every refusal below returns before anything is applied.
//
// The order is the plan's own, and each step is before the one that trusts it:
//
//  1. the gate (enable.go) — a gate the apply-time budget read did not open
//     refuses before anything is read;
//  2. the applying command's own command line — an address there is refused;
//  3. the creation fact — an unreadable marker is not an apply;
//  4. the root's own files — read in full BEFORE terraform init, against
//     preflight.go's allowlist of the configuration's shape (no data source,
//     no provider but Google's own, no function that reads the disk, no
//     remote module, no override file, no lock file of its own): what the
//     plan cannot show and what would run before the gate (PR #483's required
//     residual, and Task 3.1's plan-time residual);
//  5. the backend and the lock file, written by this command — D77's bucket
//     and the provider hashes embedded in fugaro, never the configuration's;
//  6. the deployer account of D75 assumed, and everything below inside that
//     assumption: terraform's own environment is built around its credential,
//     the init and the plan run as it, the plan is gated, the session's own
//     leg of D55's confirmation rule is asked (confirm, which is where the
//     managed case and the inventory's fallback are reached), and the saved
//     file is applied. Nothing runs outside the assumption, and a refused or
//     unassumable deployer runs no terraform at all.
//
// The inventory read itself is not made here but handed in (Request.Inventory):
// it needs the Cloud Asset API and the installation's own state bucket, which
// internal/cli reads with the operator's credential — the identity the
// installation grants the read role to — and not with the deployer's, which
// holds neither. Reading it before terraform is also what makes it independent
// of the plan: it is made whatever the plan turns out to hold, and its answer
// is the same whether the plan applies something or nothing.
//
// Where it is SAID is narrower, and deliberately so: the fallback's sentence is
// printed by confirm, and confirm is reached only where the run could go on to
// apply — not on a plan that changes nothing, not on a plan the gate refuses,
// and not on a closed gate. On each of those the run applies nothing, so there
// is no confirmation for the fallback to keep and nothing for the sentence to
// warn about; the read is still made, and its answer still decides whether
// confirm would ask (the apply-time budget gate has the same shape: it is read
// before the plan and its refusal is a refusal, not a warning).
func Apply(ctx context.Context, r Request) (Result, error) {
	if err := r.Enable.Refuse(); err != nil {
		return Result{}, err
	}
	if len(r.Session) > 0 {
		return Result{}, r.refuseSession()
	}
	if err := r.fact(); err != nil {
		return Result{}, err
	}
	if err := r.ready(); err != nil {
		return Result{}, err
	}
	providers, err := r.preflight()
	if err != nil {
		return Result{}, err
	}
	// The backend is this command's, not the configuration's: WriteBackend
	// writes backend.hcl for anyone running terraform here by hand and returns
	// the same settings for init, and WriteBackendBlock writes the one backend
	// block terraform reads. Both are refused by the preflight above if the
	// root carries one of its own, so there is exactly one.
	settings, err := r.Workdir.WriteBackend(r.stateBucket(), infra.StatePrefixManaged)
	if err != nil {
		return Result{}, fmt.Errorf("the state backend of %s: %w", r.what(), safetext.FieldErr(err))
	}
	if err := r.Workdir.WriteBackendBlock(); err != nil {
		return Result{}, fmt.Errorf("the state backend of %s: %w", r.what(), safetext.FieldErr(err))
	}
	if err := r.writeVars(); err != nil {
		return Result{}, fmt.Errorf("the variables file of %s: %w", r.what(), safetext.FieldErr(err))
	}
	// The lock file is this command's too: init runs with -lockfile=readonly,
	// so the lock decides which provider package is accepted, and the root's
	// own was refused by the preflight.
	if err := r.Workdir.WriteManagedLock(providers); err != nil {
		return Result{}, fmt.Errorf("%w: the lock file of %s: %w", ErrPreflight, r.what(), safetext.FieldErr(err))
	}
	// From here on everything runs as the deployer account of D75, and the
	// first thing that runs as it is terraform itself: the plan is read from
	// this root's state bucket (D77) and written back to it, so the identity
	// that plans and the identity that applies have to be the same one, and
	// both are the deployer's or there is no apply.
	var (
		res Result
		run error
	)
	err = Deployer(ctx, r.GCPProject, r.Access, r.Impersonator, func(ctx context.Context, cred Credential) error {
		t, done, err := r.Terraform(cred)
		if done != nil {
			// Whatever terraform needs that outlives the call (the deployer's
			// credential file) lives until every terraform below has finished,
			// and is removed on every way out of here — a return, a refusal
			// or a panic.
			defer done()
		}
		if err != nil {
			return fmt.Errorf("terraform for %s: %w", r.what(), safetext.FieldErr(err))
		}
		if err := t.Init(ctx, settings); err != nil {
			return fmt.Errorf("initializing the root of %s: %w", r.what(), safetext.FieldErr(err))
		}
		planPath := filepath.Join(r.Workdir.Root, infra.PlanFile)
		changed, err := t.Plan(ctx, planPath)
		if err != nil {
			return fmt.Errorf("planning the root of %s: %w", r.what(), safetext.FieldErr(err))
		}
		if !changed {
			// Nothing to change: there is no plan to gate and none to apply,
			// and an empty plan is not a verdict.
			res = Result{Changed: false}
			return nil
		}
		res, run = r.gateAndApply(ctx, t, cred, planPath)
		return run
	})
	if err != nil {
		return res, err
	}
	return res, nil
}

// refuseSession is D72/D74's refusal, in the words infra/managed.GuardAllowList
// uses: it names the addresses and the owner's object, and it names no flag at
// all — a refusal that named one would be read by the session it refuses as a
// thing to pass.
func (r Request) refuseSession() error {
	// The addresses are the applying session's own arguments and are bounded
	// like every other untrusted value before they are interpolated: a refusal
	// that a newline in an argument could turn into a line of its own is a
	// refusal the session it refuses can write the rest of. The listing is
	// capped as its source is — a command line holds as many arguments as the
	// session passed — and the entries it leaves out are counted rather than
	// silently dropped (safetext.FieldListLine).
	named := slices.Clone(r.Session)
	slices.Sort(named)
	line := safetext.FieldListLine(named)
	return fmt.Errorf("%w: refusing the addresses this command's own command line named (%s): in a managed project the addresses a run may delete or replace come from the owner's own list at gs://%s/%s, written with the operator's credentials by fugaro allow-delete, never from the arguments of the command that applies the plan; ask the owner to approve %s",
		ErrSessionAddresses, line, r.runsBucket(), "fugaro/allow-delete.json", line)
}

// fact is the creation fact's own rule: this command applies only for a
// project the read answered managed with no error, and the refusal always
// carries a line a person can read. A read that answered nothing — no reason
// and no managed — is refused rather than read as an answer, and an error is
// never a reason to proceed (Global Constraint 3, fail closed).
func (r Request) fact() error {
	switch {
	case r.Fact.Err != nil:
		return fmt.Errorf("%w: reading the creation fact of %s: %w", ErrNotManaged, r.what(), safetext.FieldErr(r.Fact.Err))
	case r.Fact.Managed:
		return nil
	case safetext.Field(r.Fact.Reason) == "":
		return fmt.Errorf("%w: %s is not managed, and the read gave no reason a person could read, so nothing is applied: a read that answered nothing is not an answer", ErrNotManaged, r.what())
	default:
		return fmt.Errorf("%w: %s (%s), so nothing is applied here: the coding agent's refusal is removed only where the creation fact says the project is managed", ErrNotManaged, r.what(), safetext.Field(r.Fact.Reason))
	}
}

// ready is what an apply needs before it can read anything: a workdir of this
// command's own and a terraform running in it.
func (r Request) ready() error {
	switch {
	case r.Workdir == nil || r.Workdir.Dir == "" || r.Workdir.Root == "":
		return fmt.Errorf("%w: no workdir, so there is no root to plan and nothing to apply", ErrRequest)
	case r.Terraform == nil:
		return fmt.Errorf("%w: nothing builds the terraform of %s, and an apply with no terraform to run as the deployer runs nothing", ErrRequest, r.what())
	case r.Cover == nil:
		return fmt.Errorf("%w: no cover, and a managed plan is admitted by the managed cover of %s or by nothing", ErrRequest, r.what())
	case r.Access == nil || r.Impersonator == nil:
		return fmt.Errorf("%w: nothing resolves the deployer account of %s, and an apply that cannot run as it is refused rather than run as the operator", ErrRequest, r.what())
	}
	return nil
}

// stateBucket is D77's bucket: a function of the GCP project alone, so no
// argument, flag or configuration can point the managed state at another
// bucket (statebucket.go's rule).
func (r Request) stateBucket() string { return ManagedStateBucketName(r.GCPProject) }

// writeVars makes sure the root has a terraform.tfvars.json, because the plan
// this apply runs passes -var-file=terraform.tfvars.json (tf.Plan's own
// sequence) and terraform fails when the file is not there. A root that wrote
// its own variables file keeps it byte for byte — it is the agent's
// configuration, and what its values do is in the plan the gate reads — and one
// that wrote none gets the empty object, which is what an agent-authored root
// with no variables needs.
func (r Request) writeVars() error {
	path := filepath.Join(r.Workdir.Root, infra.VarsFile)
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return os.WriteFile(path, []byte("{}\n"), 0o600)
}

// runsBucket is the name the marker and the delete list live under, the one
// infra/managed reads (its runsBucketName): the default, never the config's
// runs_bucket, because a managed object in another bucket is read by nobody.
// The project in it is bounded (safetext.Field) because this name is printed:
// it is built from what the marker read answered, and that is a value out of
// an object someone else could have written.
func (r Request) runsBucket() string { return "fugaro-runs-" + safetext.Field(r.GCPProject) }

// what names the project in this file's refusals. Both halves of the name are
// untrusted at this boundary — whatever the caller read the project and its
// GCP project from, and in a managed project that includes the marker the
// cloud answered with — so each is bounded before it is interpolated.
func (r Request) what() string {
	switch {
	case r.Project != "" && r.GCPProject != "":
		return safetext.Field(r.Project) + " (" + safetext.Field(r.GCPProject) + ")"
	case r.Project != "":
		return safetext.Field(r.Project)
	case r.GCPProject != "":
		return safetext.Field(r.GCPProject)
	}
	return "this project"
}

// spec is the gate's own input: the managed cover, the owner's addresses, the
// discovery's imports and the terraform the saved plan is read with — the one
// the apply already built around the deployer's credential, so the show the
// gate reads and the apply it admits are the same identity's.
func (r Request) spec(t *tf.TF) Spec {
	return Spec{Project: r.Project, GCPProject: r.GCPProject, Cover: r.Cover,
		AllowDelete: r.AllowDelete, Imports: r.Imports, TF: t}
}

// gateAndApply is the second half of Apply, inside the deployer's assumption:
// the hash, the gate, the re-check and the apply, in that order.
func (r Request) gateAndApply(ctx context.Context, t *tf.TF, cred Credential, planPath string) (Result, error) {
	before, err := planHash(planPath)
	if err != nil {
		return Result{}, fmt.Errorf("%w: the saved plan of %s: %w", ErrRequest, r.what(), safetext.FieldErr(err))
	}
	v, err := Gate(ctx, planPath, r.spec(t))
	if err != nil {
		// A plan the gate cannot read is an error and never a verdict, and the
		// zero Verdict it returns is not OK either.
		return Result{}, fmt.Errorf("gating the plan of %s: %w", r.what(), safetext.FieldErr(err))
	}
	res := Result{Changed: true, PlanPath: planPath, PlanHash: before, Verdict: v}
	if !v.OK {
		// The refusal is a hard return: there is no "apply anyway" here, and
		// the only action a false verdict permits is a confirmation, which in
		// the session a managed apply runs in means the step does not proceed.
		// Its sentence is the mode's own (advice): the refusal a person reads
		// says what to do about it, and in a managed project that is the
		// owner's object and not a flag of this command.
		return res, &RefusedError{Verdict: v, Advice: r.advice(v)}
	}
	// The session's own leg of D55's rule, asked here because this is the one
	// moment all three of its inputs exist: the fact was read before terraform
	// ran, the verdict that admits the plan has just been read, and Conditions
	// is what the session says about who is there.
	if err := r.confirm(v, before); err != nil {
		return res, err
	}
	// The verdict is about a plan and not about a file name (Verdict's own
	// contract), so the bytes are hashed again here, after the gate and
	// immediately before the apply.
	now, err := planHash(planPath)
	if err != nil {
		return res, fmt.Errorf("%w: the saved plan of %s: %w", ErrPlanChanged, r.what(), safetext.FieldErr(err))
	}
	if now != before {
		return res, fmt.Errorf("%w: the file the gate admitted is not the file that would be applied (%s then, %s now), so nothing is applied: gate and apply must be the same bytes",
			ErrPlanChanged, before, now)
	}
	if r.Out != nil {
		fmt.Fprintf(r.Out, "applying the saved plan of %s as %s\n", r.what(), safetext.Field(cred.Account()))
	}
	if err := t.Apply(ctx, planPath); err != nil {
		return res, safetext.FieldErr(err)
	}
	return res, nil
}

// advice is the sentence a refusal ends with, when the mode has one: a
// refused plan whose guard refused a delete or replace a new entry in the
// owner's list would admit — one naming the address AND the identity of the
// object the plan removes (Verdict.Listable, whose rule is tf.ListableEntries')
// — ends with DeleteAdvice, since that is the one thing its reader can act on.
// Nothing else gets it: a refusal from the cover or the cost list alone says
// what the plan is, not who may approve it, and a guard refusal made only of
// what no entry clears — an object moved here from another address, an action
// the guard does not know, a change with no actions, a removal whose before
// view identifies nothing — would send the owner to record an entry and the
// next plan would be refused the same way. No advice rather than the wrong one.
func (r Request) advice(v Verdict) string {
	if len(v.Listable) == 0 {
		return ""
	}
	for _, ref := range v.Refusals {
		if ref.Gate == guardGate {
			return DeleteAdvice(r.GCPProject)
		}
	}
	return ""
}

// confirm is D55's rule, and this is the caller that reaches its managed case:
// initflow.CanConfirmManaged is the one predicate, its zero Managed is today's
// rule word for word, and the value asked with here is this apply's own three
// facts — the creation fact the marker read answered, the verdict that
// admitted the plan, and whether the whole-project inventory fell back (D59,
// Task 4.5).
//
// The step's class is YesOrTyped because that is the class this apply is: the
// gate judged a saved plan, which is what a YesOrTyped step is, and money or
// permanent — a project, billing, a secret — is not something this command
// does at all. The zero Conditions is refused, which is the direction a caller
// that forgot falls in: a session with neither a marker nor a terminal has no
// confirmation to give, and the answer to that is no apply rather than an
// apply nobody confirmed.
//
// A session the rule admits is then one of two. A coding agent's, in a managed
// project with an admitted plan the inventory did not fall back on, is D55's
// answer: it proceeds, and says why in initflow's own sentence
// (AgentProceeded). A person's — a terminal and no marker — is ASKED
// (Request.Ask), with a question that names the project, the saved plan's hash
// and what it changes: having a terminal is what makes a confirmation
// possible, not a confirmation, and an apply in a project with no agent must
// not run on a person's terminal without the person saying so.
//
// The refusal names what refused in the words initflow already produces: the
// reason is decided in one place, and a caller that printed its own version of
// it would be the second copy of the rule this whole shape exists to avoid.
// Where the inventory's fallback is what kept the confirmation, the
// inventory's own sentence is appended to that refusal rather than replacing
// it — both are true, and the reader needs both.
func (r Request) confirm(v Verdict, hash string) error {
	// D59's warning comes first, before anything is asked or refused, and it
	// is printed in every session: a person about to answer the question and
	// the session about to be refused both read why this run is not the
	// managed case. It names the foreign resources (the inventory's own
	// sentence, bounded) and it halts nothing.
	fallback := r.Inventory.Fallback()
	if fallback && r.Out != nil {
		fmt.Fprintln(r.Out, safetext.MultiLine(r.Inventory.Why()))
	}
	if !initflow.CanConfirmManaged(initflow.YesOrTyped, r.Conditions, initflow.Managed{Managed: r.Fact.Managed, Admitted: v.OK, Fallback: fallback}) {
		if marker := r.Conditions.Agent; marker != "" {
			// The reason is initflow's own sentence, which is where D55's
			// rule is decided; the fallback's own sentence is appended rather
			// than substituted, so a reader gets both the rule and what this
			// project did to it.
			if fallback {
				return fmt.Errorf("%w: %s (%s)", ErrNotConfirmed, initflow.AgentRefusal(marker), safetext.MultiLine(r.Inventory.Why()))
			}
			return fmt.Errorf("%w: %s", ErrNotConfirmed, initflow.AgentRefusal(marker))
		}
		return fmt.Errorf("%w: %s", ErrNotConfirmed, initflow.NeedsTerminal("applying the saved plan of "+r.what()))
	}
	if marker := r.Conditions.Agent; marker != "" {
		if r.Out != nil {
			fmt.Fprintln(r.Out, initflow.AgentProceeded(marker))
		}
		return nil
	}
	if r.Ask == nil {
		return fmt.Errorf("%w: nothing asks the person at this terminal, and a managed apply is not made on a terminal nobody answered", ErrNotConfirmed)
	}
	ok, err := r.Ask(r.question(v, hash))
	switch {
	case err != nil:
		return fmt.Errorf("%w: asking whether to apply the saved plan of %s: %w", ErrNotConfirmed, r.what(), safetext.FieldErr(err))
	case !ok:
		return fmt.Errorf("%w: the saved plan of %s was not confirmed, so nothing is applied", ErrNotConfirmed, r.what())
	}
	return nil
}

// question is what a person is asked before the apply: the project, the saved
// plan's hash (the bytes the gate admitted and the apply re-checks) and what it
// changes, so the answer is about this plan and no other.
func (r Request) question(v Verdict, hash string) string {
	c := v.Changes
	return fmt.Sprintf("apply the saved plan of %s (sha256 %s) as the deployer account: %d to create, %d to update, %d to replace, %d to delete, %d to import",
		r.what(), hash, c.Create, c.Update, c.Replace, c.Delete, c.Import)
}

// planHash is the saved plan's own bytes, hex-encoded: what the gate admitted
// and what the apply must find again.
func planHash(planPath string) (string, error) {
	f, err := os.Open(planPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// TerraformEnv is the environment a managed apply runs terraform with: tf.Env's
// allowlist, and the assertion over it that no provider setting of the
// applier's shell is in the environment either.
//
// Why the assertion exists as well as the allowlist (PR #483's required
// residual (c)): the applier's shell is the operator's, and a GOOGLE_* variable
// in it would decide who applies, who is billed or where the requests go —
// exactly the settings the managed cover refuses in a provider block
// (managedProviderAllowed). tf.Env drops everything not on its allowlist, so
// this cannot fire today; it is here so that the day the allowlist grows one,
// the apply refuses rather than runs with it. The names are the cover's own
// list: the credential variables, the billing project, the project/region/zone
// defaults a provider would otherwise fall back to, the impersonation, and
// every *_CUSTOM_ENDPOINT (which would send the applier's bearer token to
// another host).
//
// What the environment carries instead is the deployer's credential of D75,
// and it arrives through parent: the caller replaces the one variable that
// names a credential file (GOOGLE_APPLICATION_CREDENTIALS, on tf.Env's
// allowlist) with a file of its own — the ADC library's
// "impersonated_service_account" form, whose source_credentials are the
// operator's own and whose impersonation URL names the deployer account —
// BEFORE it calls this, so the assertion above runs over exactly what terraform
// will run with. The alternative channel, GOOGLE_OAUTH_ACCESS_TOKEN, is refused
// by name below: a minted access token in the environment is a credential this
// command cannot refresh, and the file is the shape gcloud's own
// --impersonate-service-account writes.
func TerraformEnv(parent []string, workdir, cache string) ([]string, error) {
	env, err := tf.Env(parent, workdir, cache)
	if err != nil {
		return nil, err
	}
	if k := providerSettingIn(env); k != "" {
		// The name is a variable name out of the environment this command
		// inherited — the one thing in the sentence that is not this
		// program's, and a name that may hold anything but "=" and NUL — so it
		// is one bounded field (safetext.Field) before it is interpolated.
		return nil, fmt.Errorf("%w: %s is set in terraform's environment, and the credential terraform runs with is this command's own (the deployer account's): unset it", ErrProviderSetting, safetext.Field(k))
	}
	return env, nil
}

// providerSettings are the variables TerraformEnv refuses by name, beside
// every *_CUSTOM_ENDPOINT.
var providerSettings = []string{
	"GOOGLE_CREDENTIALS", "GOOGLE_OAUTH_ACCESS_TOKEN", "GOOGLE_BILLING_PROJECT",
	"GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "GOOGLE_SERVICE_ACCOUNT", "GOOGLE_SERVICE_ACCOUNT_IMPERSONATION_URL",
	"GOOGLE_PROJECT", "GOOGLE_REGION", "GOOGLE_ZONE",
}

// providerSettingIn is the first provider setting env carries, "" when it
// carries none.
func providerSettingIn(env []string) string {
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasSuffix(k, "_CUSTOM_ENDPOINT") || slices.Contains(providerSettings, k) {
			return k
		}
	}
	return ""
}
