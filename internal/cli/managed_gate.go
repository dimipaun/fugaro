package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The managed gate's ONE helper: the four lines every command in the set makes
// around its own write (M6, D1015, D1017, D1024, D1025).
//
// Why one helper and not a rule each command writes. The gate is a value in
// internal/managed (D1021) and a command's own rules are its own, but FOUR
// things around them are the same four things in `fugaro update`,
// `config publish`, `project policy publish` and `image refresh`: read the two
// marks per command, ask the ownership question before the plan is judged
// (D1017), re-read the marks immediately before the write (D1024), and write
// the audit record for an admission and for a refusal alike (D1025). Written
// four times they are four chances to get one of them wrong — and the one that
// would be wrong quietly is the order: a plan judged against a database the run
// is about to refuse, or a mark read once and cached so an owner who revoked it
// mid-command keeps the permission. So the helper is written once and the
// commands call it; a command that re-implemented any of the four would be the
// drift this file exists to prevent.
//
// What a command in the set writes, in order:
//
//	gate, err := readManagedGate(ctx, env, facts)         // marks, ownership
//	if err != nil { return err }
//	// the command judges ITS OWN document with ITS OWN rules, and builds a
//	// managed.Converge from the class, the bytes it read and the rules
//	m, err := gate.admit(ctx, class, conv)                // re-read, verdict
//	if err != nil { return err }                          // the gate refused it
//	if !initflow.CanConfirmManaged(class, cond, m) { ... }// the command's own rule
//	// the command's write
//	if err := gate.recorded(ctx); err != nil { return err }// after the write
//
// Why the verdict is handed back as an initflow.Managed rather than as a
// boolean. That value is the rule D55 already has (initflow.CanConfirmManaged),
// and its zero value is "not managed", so a command that never read the gate —
// or that ignored the error and carried on — keeps today's refusal exactly. A
// helper that answered "you may proceed" as an error-or-nil would be a second
// rule beside that one, and the second rule is the one that would widen a
// confirmation instead of narrowing it (D1026).
//
// Why the audit is written in two places rather than one. The record is written
// AFTER the command's write (spec §6), so an admission's record is the caller's
// `recorded` call and nothing else can be recorded as applied; a refusal's is
// written where the refusal happens, because there is no write to wait for. A
// command that applied nothing — one whose own confirmation was kept, so it
// never reached its write — records nothing: the record is the GATE's, and a
// record saying "admitted" about a write that never happened is worse than no
// record at all. The whole-project inventory's fallback (D59) is one reason a
// confirmation is kept, and it travels in the answer (Fallback) rather than
// being decided on here: whether the step is applied is the command's own rule
// over conditions this helper never sees, so a gate that dropped the admission
// on a fallback would lose the record of a person's own confirmed apply.
//
// Why a project that is not managed is not this helper's business (D1025). Both
// marks absent or unreadable is the ordinary installation, and its steps are
// confirmed by a person or refused to an agent exactly as they were before this
// file existed: the helper returns the zero Managed and writes nothing, because
// a log of a person's own decisions at a terminal is a new data path for a
// property this design did not create. The only thing it changes there is that
// the marks were read at all — which is D1015's whole mechanism.
//
// What this helper does NOT do: it does not judge a document (the command's own
// rules do, in internal/infra/tf for a Terraform plan and in the command for the
// rest), it does not decide a class (D1026's rule lives in initflow and in the
// Converge), and it never writes anything a run's own job account could write:
// the record goes into the operator-only prefix, with the credentials the
// command holds.

// managedGateNow is when an audit record says it happened. It is a package
// variable for the reason budgetNow and pairNow are: a record's date directory
// and its id come from the time, so a test that wants to read a day's records
// back has to say which day.
var managedGateNow = time.Now

// managedGateEnv is what the gate reads and writes with: the installation's own
// trusted configuration (the GCP project must come from here and never from a
// repository — managed.IsManaged's own warning) and the clients of the account
// running the command.
type managedGateEnv struct {
	lc  *localcfg.Config
	s   *storage.Service
	crm *crm.Service
	// db is the installation's own budget database, when the command has it
	// open: the ownership question (D1017) is asked with it, before the plan is
	// judged, so no plan is judged against a database the command is about to
	// refuse. A command with no budget database in play leaves it nil and its
	// own path asks the question where it always has; the question is asked
	// here only for a project the marks call managed, so no ordinary
	// installation gains a read it did not have.
	db *rtdb.Client
}

// managedGateFacts is the command's own facts about itself: what a reader of
// the audit record needs that the gate's verdict does not say.
type managedGateFacts struct {
	// Subcommand is the command as a person types it after the program's name
	// ("config publish"), which is what the record's command field names.
	Subcommand string
	// Argv is the session's own arguments as the caller has them (the
	// process's, os.Args[1:]), with every secret-bearing argument ABSENT rather
	// than redacted (spec §6): the gate admits no command that takes a secret,
	// so a command with one to hide has nothing to record here.
	Argv []string
	// Declined is what the command would have written, in the words the
	// ownership refusal ends with ("nothing was published"). It is passed
	// through requireOwnBudgetDB, which is the same question every writer into
	// the budget database already asks.
	Declined string
	// Identity is the account the write ran as, when the command knows better
	// than the credentials do (the deployer account of `managed apply`); an
	// empty one is filled from the credentials in force.
	Identity string
	// AgentMarker is the session's own environment marker, "" for a person at a
	// terminal. It is recorded rather than trusted: it says what the session
	// claimed, and it is never a reason to admit anything.
	AgentMarker string
	// DeleteListGeneration is the version of the owner's delete list that was
	// in force where a removal was judged, and 0 when the command judged none:
	// the record tells the two apart by an empty field and never by a zero.
	DeleteListGeneration int64
	// InventoryFallback is the whole-project inventory's answer (D59) for this
	// run: true keeps every confirmation the managed case would remove. A
	// command that consulted an inventory passes its Fallback() here, and a
	// command that has none passes false.
	InventoryFallback bool
}

// managedGate is the state between the helper's calls: the marks as the last
// read found them, and the document the gate admitted, if it admitted one.
//
// The zero value is a gate nobody read: not managed, so every call below
// answers the way every caller of D55's rule answered before this file existed.
type managedGate struct {
	env   managedGateEnv
	facts managedGateFacts
	// marks is the creation fact the most recent read answered with: the one
	// readManagedGate made, and then the one admit re-read immediately before
	// the write (D1024). It is what the record names, so the fact a run acted
	// under is the one in force at the write and not the one in force when the
	// session started.
	marks managed.MarkFacts
	// managed is whether both marks were there at readManagedGate.
	managed bool
	// revoked is set by the second read when it finds the project is no longer
	// managed: the one way a gate that was open closes before its command is
	// over. It is a field of its own and not managed's negation, because the
	// two say different things to a caller that holds this gate across a run
	// (initGate.underway): a gate that never opened keeps every confirmation
	// the run always had, and a gate the second read revoked has to stop
	// standing in for them — the run's one confirmation, the plugin wiring's
	// y/N and the IAM removals included — from that moment on. Without it a
	// caller that copied managed at the first read would go on admitting steps
	// for a project that is not managed any more, in the gate's sentence and
	// with nothing typed.
	revoked bool
	// judged is the document this gate ADMITTED, nil until it admits one and
	// nil again from the next admit: it is held here so the record written
	// after the command's write is about the document that was judged — the
	// same value, carrying the digest of the same bytes (D1022) — rather than
	// about one the caller passes again. It never outlives the step it was
	// admitted for, which is what makes one admission one record.
	judged *managedmode.Converge
}

// readManagedGate is the first of the helper's calls, made before the command
// judges anything: it reads the two marks per command (D1015 — never an
// environment variable, never a file in the repository, never a cached answer)
// and asks the ownership question before the plan is judged (D1017).
//
// It answers an error in three cases and each of them stops the command: no
// installation to read about, a read that did not answer at all (a project this
// program cannot prove is managed is a project whose confirmations it must not
// skip, so a failure here is never a reason to proceed), and the ownership
// question's own refusal, which is asked exactly as it is today and writes
// nothing — it is asked BEFORE the gate, so there is no judged document for a
// record to carry, and a record with an invented digest would be a lie about a
// plan nobody read.
func readManagedGate(ctx context.Context, env managedGateEnv, facts managedGateFacts) (managedGate, error) {
	g := managedGate{env: env, facts: facts}
	if env.lc == nil {
		return g, userErr("the managed gate cannot be read: this command has no installation's config to read the project from, and gcp_project comes from the installation and never from a repository (D1015)")
	}
	marks, err := managed.ReadMarkFacts(ctx, env.s, env.crm, env.lc.Name, env.lc.GCPProject)
	g.marks = marks
	if err != nil {
		return g, remote(fmt.Errorf("the creation fact of %s could not be read, so this command cannot tell whether the project is managed and does not skip any confirmation: %w",
			safetext.Field(env.lc.Name), safetext.FieldErr(err)))
	}
	if !marks.Managed {
		return g, nil
	}
	// The ownership question comes before the gate is open, not after: the
	// gate answers "you may skip this confirmation", and a database this
	// installation may not write has no business clearing anything — a caller
	// that ignores this error and goes on to admit gets the zero Managed and
	// today's refusal, which is the direction a mistake has to fall in.
	if g.env.db != nil {
		if err := requireOwnBudgetDB(ctx, env.lc, env.db, facts.Declined); err != nil {
			return g, err
		}
	}
	g.managed = true
	return g, nil
}

// admit is the helper's second and third calls in one, made after the command
// has judged its own document and immediately before the command's own write:
// it re-reads the marks (D1024 — the gap a revocation can fall into is the
// plan-to-apply interval and not the whole command), and it writes the audit
// record when the gate refused the plan.
//
// The value it returns is what the step's confirmation is decided under, field
// for field: whether the project is managed, whether the gate admitted THIS
// document, and whether the whole-project inventory fell back. The class is
// passed beside the verdict because D1026's rule is not this file's to restate:
// a class the gate does not cover keeps the confirmation it has today whatever
// the plan looked like, and initflow.CanConfirmManaged is where that is decided.
//
// A refused plan is this call's own error, and it is the gate's own sentence
// (managed.NotConverged, with the rules that fired) rather than the coding
// agent's: the session reads why the document was refused. It is returned for
// every class the gate covers and for no other, and the refusal is recorded
// before it is returned.
func (g *managedGate) admit(ctx context.Context, class initflow.ConfirmClass, conv managedmode.Converge) (initflow.Managed, error) {
	// The document a previous call admitted is spent by the time this one is
	// made: the step it was admitted for has either recorded it or is being
	// asked again for a new plan. Only the branch at the end of this call that
	// admits one sets this again, so no path out of here can leave a document
	// behind that this call did not admit — a recorded() reached afterwards
	// would otherwise file a record of a plan the step did not apply, and a
	// caller's own write would be accounted for by another step's admission.
	g.judged = nil
	if !g.managed {
		return initflow.Managed{}, nil
	}
	fresh, err := managed.ReadMarkFacts(ctx, g.env.s, g.env.crm, g.env.lc.Name, g.env.lc.GCPProject)
	if err != nil {
		return initflow.Managed{}, remote(fmt.Errorf("the creation fact of %s could not be read a second time, immediately before the write this step was admitted for, and a mark that cannot be read is not a permission: %w",
			safetext.Field(g.env.lc.Name), safetext.FieldErr(err)))
	}
	if !fresh.Managed {
		// The project stopped being managed between the two reads: the step
		// loses its admission and keeps its confirmation, which is what a
		// project that is not managed has. Nothing is recorded — the project is
		// not managed at this moment, and D1025 is about managed projects only
		// — and the window D1024 narrows is exactly this one.
		//
		// The gate's own answer goes back with it: the second read is the last
		// word on whether this project is managed, so a later call on this gate
		// finds the same un-managed project the second read did and not the
		// managed one the first read saw.
		//
		// revoked records that this is what happened, for the caller that holds
		// the gate across a run and decides prompts of its own from it: the
		// answer that was true at the first read is not the answer any more.
		g.managed = false
		g.revoked = true
		return initflow.Managed{}, nil
	}
	g.marks = fresh
	m := initflow.Managed{Managed: true, Admitted: conv.Refuse() == nil, Fallback: g.facts.InventoryFallback}
	if !m.Covers(class) {
		return m, nil
	}
	if err := conv.Refuse(); err != nil {
		// The refusal and the record of it are one answer. The session reads
		// why nothing was applied, and both sentences stay in the chain (a %w
		// each): the gate's own refusal is what errors.Is finds, so a caller
		// can tell a step the plan refused from any other failure, and the
		// write's own cause is what classifies the exit — an operator's missing
		// grant is theirs to fix, an outage is not.
		if werr := g.record(ctx, conv); werr != nil {
			return m, fmt.Errorf("%w: %w", err, werr)
		}
		return m, err
	}
	g.judged = &conv
	return m, nil
}

// recorded is the helper's last call, made AFTER the command's own write: it
// writes the record of the admission, or nothing at all when this gate admitted
// no document.
//
// Nothing at all is the answer for every other case, and each of them is
// deliberate: a project that is not managed writes nothing (D1025), a command
// that applied nothing has no application to record, and a step the gate did
// not judge has no digest and no verdict to write — the record is the gate's
// and not a log of the command's doings.
//
// A write that failed is the command's own failure (D1014): an apply that could
// not leave a trail is an apply nobody can account for afterwards, so this call
// returns the error rather than printing a warning beside a success.
func (g *managedGate) recorded(ctx context.Context) error {
	conv := g.judged
	if conv == nil {
		return nil
	}
	// The admission is spent here, before the write is attempted: one gate
	// admits one step, and one step leaves one record however many times a
	// caller asks. A command that deferred this call and made it again at the
	// end would otherwise file a second object for one applied command — and
	// a write that failed would be retried by a second call rather than
	// failing the command, which is the opposite of what D1014 says.
	g.judged = nil
	return g.record(ctx, *conv)
}

// record writes one record of one verdict, with the operator's own credentials
// into the runs bucket's owner-only prefix. Both the refusal (from admit) and
// the admission (from recorded) come through here, so a record cannot say
// something about a refusal that it does not say about an admission.
func (g *managedGate) record(ctx context.Context, conv managedmode.Converge) error {
	// The identity first, and as the command's own refusal rather than as a
	// record the writer would refuse: "who ran this" is the first question an
	// owner asks of a record, so a command whose account cannot be named has
	// nothing to write and says so in the words that name the fix instead of
	// failing with a shape error about a field the operator never chose.
	id := g.identity(ctx)
	if id == "" {
		// Neither a claim about the apply nor a claim about the gate: this
		// path is reached for a refusal (nothing was applied) and for an
		// admission (the step was), and the sentence that is true of both is
		// the one that says what is missing and what to do about it.
		return userErr("the account %s is running as could not be established from the credentials in force, and the audit record of a managed project's step has to name it, so this command leaves no trail and fails (D1014): run `gcloud auth application-default login` as the account that owns the project and run it again", safetext.Field(selfCommand()+" "+g.facts.Subcommand))
	}
	facts := managedmode.AuditFacts{
		Command:              selfCommand() + " " + g.facts.Subcommand,
		Argv:                 g.facts.Argv,
		Project:              g.env.lc.Name,
		GCPProject:           g.env.lc.GCPProject,
		Identity:             id,
		AgentMarker:          g.facts.AgentMarker,
		MarkerVersion:        g.marks.Version,
		MarkerGeneration:     g.marks.Generation,
		DeleteListGeneration: g.facts.DeleteListGeneration,
		At:                   managedGateNow(),
	}
	_, err := managedmode.WriteAudit(ctx, g.env.s, g.env.lc.RunsBucketName(), managedmode.AuditRecordFor(facts, conv))
	if err == nil {
		return nil
	}
	if isAccessDenied(err) {
		return userErr("the step's audit record could not be written to gs://%s, and a managed apply that leaves no trail fails the command (D1014): the operator's own account needs storage.objects.create under %s there",
			safetext.Field(g.env.lc.RunsBucketName()), safetext.Field(managedmode.AuditPrefix))
	}
	return remote(fmt.Errorf("writing the audit record of %s for %s: %w", safetext.Field(g.facts.Subcommand), safetext.Field(g.env.lc.Name), safetext.FieldErr(err)))
}

// identity is the account a record names: the caller's own when it named one
// (the deployer account of `managed apply`), and otherwise the credentials in
// force, as the account they belong to. It is the first thing an owner asks of
// a record — "who ran this in my project" — so a record whose identity could
// not be established at all is refused by the writer rather than written with
// an empty field, and the command fails with it.
func (g *managedGate) identity(ctx context.Context) string {
	if g.facts.Identity != "" {
		return g.facts.Identity
	}
	return auditIdentity(ctx, g.env.lc)
}

// auditIdentity is the account the credentials in force belong to: the service
// account named by the credentials' own file, or the person the userinfo
// endpoint answers for. It is "" when neither can be established, which the
// writer refuses.
//
// Why the service account is read out of the file and the person is asked: a
// gcloud user credential carries no email, and the endpoint that answers with
// one is the call the first run already makes to name a new project's launcher
// (authenticatedUser). The service account's is the deployer account of a
// managed apply, and reading it out of the file it is configured with avoids a
// network call for the one case where the answer is already on disk.
func auditIdentity(ctx context.Context, lc *localcfg.Config) string {
	if kind, _ := adcInfo(); kind == "service_account" {
		if email := adcServiceAccountEmail(); email != "" {
			return "serviceAccount:" + email
		}
	}
	email, err := authenticatedUser(ctx, lc)
	if err != nil || email == "" {
		return ""
	}
	return "user:" + email
}

// adcServiceAccountEmail is the client_email of the credentials' own file, and
// "" when they are not a service account's, when there is no file to read or
// when what is there is not an email address. The file is the one
// GOOGLE_APPLICATION_CREDENTIALS names or gcloud's own, read bounded and never
// followed anywhere else; it is never printed, and only this one field of it is
// used.
func adcServiceAccountEmail() string {
	path, err := adcFile()
	if err != nil {
		return ""
	}
	b, err := readBounded(path)
	if err != nil {
		return ""
	}
	var f struct {
		Type  string `json:"type"`
		Email string `json:"client_email"`
	}
	if json.Unmarshal(b, &f) != nil || f.Type != "service_account" {
		return ""
	}
	email := strings.ToLower(strings.TrimSpace(f.Email))
	if email == "" || !strings.Contains(email, "@") || strings.ContainsAny(email, " \t\r\n,;:\"'") {
		return ""
	}
	return email
}
