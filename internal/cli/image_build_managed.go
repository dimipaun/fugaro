package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// `fugaro image build` under the managed gate: the rules a managed project's
// builds are judged by, and the record of that judgement (D1013, D1206, M6,
// docs/repo-image-removal/spec.md §6).
//
// What the gate is now that the prompts are gone (T4, D1543). Every arm of this
// command used to ask for the project's name typed at a real terminal, because
// a Cloud Build is billable (confirmBuild); in a managed project the owner's
// ruling of 2026-11-06 took that prompt away and this file was the stand-in for
// it. T4 removes the prompt from the command altogether — a build asks nothing
// in any session — so what the gate is left as is what it was underneath the
// prompt: the row's rules stated over the build that is actually submitted, the
// refusal of a build a rule refuses, and the record of the verdict. A refusal
// is still a refusal with no prompt to fall back to, and the record is the
// audit an owner reads months later (D1022).
//
// Which arm, and why only that one. The managed gate's rows are the design of
// record's, and for this command the row is the catalog entry: "admitted for a
// catalog entry — the entry exists in the effective catalog, the build account
// is the project's own for that entry's source repository" (spec.md §6, D1206).
// That row is stated for the command the removal leaves, which has no derived
// arm; until the removal lands, the catalog arm is the part of today's command
// the row covers. The derived arm — `--workflow`, or a checkout with no catalog
// — is written by no row anywhere, and the design's own boundary is why
// (docs/managed-agent-converge/spec.md §3.1): a path the design gave neither a
// document nor a rule is a path this file does not judge, so the derived arm is
// submitted with no judgement and no record.
//
// Why the door is still the person at the terminal. The gate's scope is the
// session whose prompt it stood in for, and T4 does not widen it: whether the
// record should cover every session is D1545's audit-only question, T9's
// rewrite of this call, and not a side effect of removing the prompt. The
// consequence is named rather than hidden: a build a coding agent's session or
// a pipeline submits on a managed project reaches the cloud unrecorded (T4's PR
// body carries it as a finding).
//
// Why the kill switch is read, and read per build. A billable build the gate
// admits is one an owner can stop, and the project's kill switches are re-read
// before EACH build, never once per command — an owner who stops the project
// means the build after that to stop, and a command that read them once would
// keep spending on a project that has just been told to stop
// (refreshManaged.admitBuild, the same call over the same object).
//
// Why the document is the sentence that was shown (What below). D1022's digest
// is what lets an owner check, months later, that the document a verdict was
// reached on is the document the command acted on; for this command what a
// person meets is the banner line, so the banner is IN the document rather than
// beside it. A record whose digest covered only the figures the rules were
// stated over would leave the one thing that was shown out of what is provably
// what ran. One document per build, and the entry it is about is named in it:
// the catalog arm submits one build per entry, and a set of records that all
// carried one digest would not say which build was admitted (refreshBuildDoc's
// own reason for naming the build).
//
// Why the class is YesOrTyped, and why that is not a widening. A billable build
// is money, and D1026 keeps a Typed step — money or permanent — out of every
// plan gate; a billable build's own step has never been a Typed one (YesOrTyped,
// D12's answer of 2026-10-20), so YesOrTyped is the class D1026's own rule lets
// a document admit. What keeps this from being a licence is not the absence of a
// prompt but the three things that were always here: the door opens only for
// initflow.PersonAtTheTerminal, the rules below are re-stated over the submitted
// build rather than assumed from the code that computed it, and the admission —
// not the absence of a prompt — is what the record carries.

// buildManaged is `fugaro image build`'s own door: the installation the gate
// read, the gate itself, and the read D1013 asks for before every build.
//
// nil is the whole of the difference for every other session. A nil
// *buildManaged answers standsIn false, admitEntry and recorded do nothing, and
// the command submits its build with no judgement and no record — which is the
// answer for an unmanaged project, a mark that cannot be read, an agent's
// session, a pipe and a project whose clients or budget database cannot be
// opened.
type buildManaged struct {
	// lc is the installation the gate read, and the one this command already
	// acts on: the note's project name, the recorded project and the rules'
	// own project all come from it.
	lc *localcfg.Config
	// gate is the gate the door read, and it is the same value every build of
	// this command is judged through: one read of the marks per command
	// (D1015), and the second read D1024 asks for is the gate's own, made
	// inside admit immediately before each build's write.
	gate *managedGate
	// db is the client the read used, kept because the kill switch is re-read
	// with it before every build.
	db *rtdb.Client
}

// openBuildManaged is the door: the gate's read, made once per command, in the
// one session whose typed prompt the ruling takes away.
//
// Why nil is the answer to every way of not being managed. A mark that cannot
// be read is not a permission (the direction every read in
// internal/infra/managed takes), a client that cannot be built is an account
// that cannot act, and a budget database the command cannot open is a project
// whose ownership question it cannot ask (D1017) — each of them is "the gate
// did not judge this" and returns nil, so the build goes through unrecorded
// rather than being met with an error about the installation.
//
// Why nothing is read for a session that is not a person at the terminal. The
// predicate is asked first, before a single read: the door is the one place
// that decides the scope, and a caller that read the marks for a session the
// gate does not judge would be paying for two calls to widen nothing.
func openBuildManaged(ctx context.Context, env *cloudEnv, c initflow.Conditions) *buildManaged {
	if !initflow.PersonAtTheTerminal(c) {
		return nil
	}
	cl, err := newInitClients(ctx, env.lc)
	if err != nil || cl == nil || cl.Storage == nil || cl.CRM == nil {
		return nil
	}
	_, db, err := budgetDBFor(ctx, env.lc)
	if err != nil {
		return nil
	}
	gate, err := readManagedGate(ctx, managedGateEnv{lc: env.lc, s: cl.Storage, crm: cl.CRM, db: db}, buildGateFacts())
	if err != nil || !gate.managed {
		return nil
	}
	return &buildManaged{lc: env.lc, gate: &gate, db: db}
}

// standsIn is whether this session's builds are judged by the gate and recorded
// by it: the door's read answered "managed" and the person is at their own real
// terminal.
//
// The predicate is asked again here rather than remembered from the door,
// because the session the note is scoped to is one reading of the same facts,
// taken where it is decided: a caller cannot end up printing one session's note
// to another session.
func (m *buildManaged) standsIn(c initflow.Conditions) bool {
	return m != nil && m.gate != nil && initflow.PersonAtTheTerminal(c)
}

// buildGateFacts is this command's own facts about itself, as M6's helper takes
// them. There is no secret-bearing argument to leave out: every argument this
// command takes is a repository, a workflow, an entry name, a base, a tag or a
// platform, and a build admits no command that takes a credential.
func buildGateFacts() managedGateFacts {
	return managedGateFacts{
		Subcommand:  "image build",
		Argv:        os.Args[1:],
		Declined:    "nothing was built",
		AgentMarker: agentMarker(os.Getenv, processTerminal()),
	}
}

// buildPlan is one `fugaro image build` command's own document: the sentence
// the command prints (What), the plan the command was asked for (Entries), the
// entry one admission is about (Entry), and the facts the gate's rules are
// stated over. The two are one value because they are one plan: what is shown
// is what the digest covers, and a caller cannot print one sentence and have
// another judged.
//
// Origin is the checkout's own repository and Repo is the one the build is
// submitted for; the two are equal by construction (both arms refuse a --repo
// that is not the checkout's origin, as canonical paths) and the rule restates
// it at the moment of the write, which is what a rule in this package is for.
//
// KillSwitch is filled in per build rather than once: it is the fact D1013
// re-reads before each one, so a document that carried the command's first read
// would say the verdict was reached on a switch that was not in force when this
// build was judged.
type buildPlan struct {
	Version    int      `json:"version"`
	Repo       string   `json:"repo"`
	Origin     string   `json:"origin"`
	Entry      string   `json:"entry"`
	Entries    []string `json:"entries"`
	Kind       string   `json:"base_kind"`
	Base       string   `json:"base"`
	Account    string   `json:"build_account"`
	Registry   string   `json:"registry"`
	Machine    string   `json:"machine_type"`
	KillSwitch string   `json:"kill_switch,omitempty"`
	What       string   `json:"what"`
}

// buildDocumentVersion is the document's own version. It is 2 because the count
// bound (max_image_builds, builds_started) is gone from the document (D1543)
// and a record's plan_digest is what an owner checks a document against months
// later: a document whose shape changed under the same version would make that
// check pass for bytes nobody judged, and a version-1 document still reads as
// what it was.
const buildDocumentVersion = 2

// bytes renders the document, and it is deterministic: a struct's fields in
// their declared order and the caller's own entry order, so the same plan
// hashes to the same digest in every session that reads it.
func (p buildPlan) bytes() ([]byte, error) {
	p.Version = buildDocumentVersion
	b, err := json.Marshal(p)
	if err != nil {
		return nil, userErr("the plan of this build could not be rendered, and a document the gate cannot read is a build it does not judge: %v", err)
	}
	return b, nil
}

// admitEntry is one catalog entry's whole judgement, made immediately before
// the build it is about.
//
// The order is M6's (managed_gate.go's own comment): the kill switch is read,
// the document is built and judged, and the gate re-reads the marks immediately
// before the write (D1024) and writes the record of a refusal itself. A gate
// that refuses the plan is a refusal and never a prompt — a plan a rule refused
// is not one to hand back — and a gate that finds the project is not managed
// any more (the second read's own answer, admit's zero Managed) judges nothing
// and records nothing.
func (m *buildManaged) admitEntry(ctx context.Context, cmd *cobra.Command, c initflow.Conditions, cat *config.Catalog, e config.Entry, slug string, p buildPlan) error {
	if !m.standsIn(c) {
		return nil
	}
	killed, killRule := m.killSwitch(ctx, slug)
	p.Entry, p.KillSwitch = e.Name, killed
	doc, err := p.bytes()
	if err != nil {
		return err
	}
	rules, refused := buildRules(m.lc, cat, e, p, killRule)
	conv := managedmode.ConvergeAdmitted(doc, initflow.YesOrTyped, rules)
	if len(refused) > 0 {
		conv = managedmode.ConvergeRefused(doc, initflow.YesOrTyped, refused)
	}
	adm, err := m.gate.admit(ctx, initflow.YesOrTyped, conv)
	if err != nil {
		return err
	}
	// Nothing is asked here, and there is nothing left to ask: the note says
	// where the verdict was recorded, which is what a person at the terminal
	// meets in the place the command's own confirmation used to be (T4 removed
	// that confirmation and left the record).
	if initflow.ConfirmsByGate(initflow.YesOrTyped, c, adm) {
		w := cmd.ErrOrStderr()
		fmt.Fprintf(w, "⚠ CONFIRM (project %s, GCP project %s): %s\n", m.lc.Name, m.lc.GCPProject, p.What)
		fmt.Fprintf(w, "  %s\n", initflow.GateProceeded())
	}
	return nil
}

// recorded writes the record of the build the gate admitted, and it is called
// after the submission that IS the write: an admission whose build was never
// submitted records nothing, because the record says a build happened. A
// command that submitted nothing (a gate that did not judge it, a build that
// failed before the submission) has nothing to record.
//
// One record per build: the catalog arm judges and records each entry
// separately, so a command that submits three builds leaves three records and
// no entry is built under another entry's verdict.
func (m *buildManaged) recorded(ctx context.Context) error {
	if m == nil || m.gate == nil {
		return nil
	}
	return m.gate.recorded(ctx)
}

// buildRules is the row's own rules over this one build, in the order
// docs/repo-image-removal/spec.md §6 states them for this command: the entry
// exists in the effective catalog, the build account is the project's own for
// that entry's source repository — plus the read the row stands on (the kill
// switch) and the restatements every rule set in this package carries. D1543
// removed the count bound (max_image_builds) from the row: what bounds a build
// now is the lease its cost is taken under, which is not this gate's business
// and is judged before the submission in image_cost.go.
//
// Every rule here restates something the command has already enforced by the
// time it submits — an entry of the catalog it read, the checkout's own
// repository, a base that is not another project's pull-through reference, this
// project's own build account, this installation's own registry. That is
// deliberate rather than redundant: the rules are what the record says the
// verdict was reached on, so they are written over the values that are actually
// submitted (infra.Repo's spec) and never over the inputs that produced them,
// and the gate is the last place a drift between the two would be seen.
func buildRules(lc *localcfg.Config, cat *config.Catalog, e config.Entry, p buildPlan, killRule string) (rules, refused []string) {
	host, err := infra.RegistryHost(lc)
	if err != nil {
		// A host this installation's own config cannot state is not a build
		// this gate can judge: the rules below are stated over it, and a rule
		// that cannot be evaluated is a refusal rather than a pass.
		return nil, []string{"this installation's own registry host is readable (" + safetext.FieldErr(err).Error() + ")"}
	}
	// The entry exists in the effective catalog. The name is re-looked-up
	// rather than taken from the entry that was found: the two are the same
	// value by construction, and a rule stated over the lookup is what says so.
	if _, ok := catalogEntryNamed(cat, e.Name); !ok {
		refused = append(refused, "the build is an entry of this project's effective catalog ("+safetext.Field(e.Name)+" is not)")
	} else {
		rules = append(rules, "the build is an entry of this project's effective catalog ("+safetext.Field(e.Name)+")")
	}
	// The build account is this project's own for that entry's source
	// repository, and the two halves are one rule each because either can be
	// wrong on its own: an account belonging to another project, or an entry
	// whose Dockerfile lives in another repository — which is exactly what the
	// command's own selection passes over (catalogBuildEntries), and which
	// would otherwise be built as THIS repository's account. An inline entry
	// names no source repository at all: its definition is a copy in this
	// project's own bucket, so the account's rule is the whole of D1206's
	// clause for it.
	switch {
	case e.Source.Repo == "":
		rules = append(rules, "the entry is written in this project's own state ("+safetext.Field(e.Name)+" names no source repository)")
	case !sameRepo(e.Source.Repo, p.Repo):
		refused = append(refused, "the entry's source repository is this checkout's own ("+safetext.Field(e.Source.Repo)+" is "+safetext.Field(e.Name)+"'s source, not "+safetext.Field(p.Repo)+")")
	default:
		rules = append(rules, "the entry's source repository is this checkout's own ("+safetext.Field(e.Source.Repo)+")")
	}
	if !strings.HasSuffix(p.Account, "@"+lc.GCPProject+".iam.gserviceaccount.com") {
		refused = append(refused, "the build runs as this project's own build account ("+safetext.Field(p.Account)+" is not "+safetext.Field(lc.GCPProject)+"'s)")
	} else {
		rules = append(rules, "the build runs as this project's own build account ("+safetext.Field(p.Account)+")")
	}
	// The switch that stops a build is off. It is honour in the fail-closed
	// direction: an unreadable switch is one this program honours (killSwitch).
	if killRule != "" {
		refused = append(refused, killRule)
	} else {
		rules = append(rules, "the project's kill switch is off")
	}
	// The repository rule is the command's own guard restated, and it is
	// stated over canonical forms for the same reason that guard is
	// (task.CanonicalRepo): --repo ACME/App is the checkout's origin, and a
	// rule stated over the raw strings would refuse a build the command
	// accepted a moment earlier.
	cr, errR := task.CanonicalRepo(p.Repo)
	co, errO := task.CanonicalRepo(p.Origin)
	if errR != nil || errO != nil || cr != co {
		refused = append(refused, "the build is this checkout's own repository ("+safetext.Field(p.Repo)+" is not "+safetext.Field(p.Origin)+")")
	} else {
		rules = append(rules, "the build is this checkout's own repository ("+safetext.Field(p.Repo)+")")
	}
	if foreignProxyRef(p.Base, host, p.Kind, baseOwner(lc)) {
		refused = append(refused, "the base image is not another project's pull-through reference ("+safetext.Field(p.Base)+" is)")
	} else {
		rules = append(rules, "the base image is not another project's pull-through reference ("+safetext.Field(p.Base)+")")
	}
	if !strings.HasPrefix(p.Registry, host+"/") {
		refused = append(refused, "the build pushes into this installation's own registry ("+safetext.Field(p.Registry)+" is not under "+safetext.Field(host)+"/)")
	} else {
		rules = append(rules, "the build pushes into this installation's own registry ("+safetext.Field(p.Registry)+")")
	}
	return rules, refused
}

// killSwitch is the project's two kill switches as they stand right now, and
// the rule a build under them reads: "" when both are off, and the refusal's
// own sentence when one is on — or when the read answered nothing at all,
// which D1013 counts as ON for the reason budget's own decode gives: the
// reason an operator wrote a switch is lost when it cannot be read, and the
// intent to stop is not.
func (m *buildManaged) killSwitch(ctx context.Context, slug string) (which, rule string) {
	kills, err := budget.ReadKills(ctx, m.db, slug)
	if err != nil {
		return "unreadable", "the project's kill switch could not be read (" + safetext.FieldErr(err).Error() + "), and an unreadable switch is one this program honours rather than one it ignores (D1013): the owner checks it with " + safetext.Field(selfCommand()+" budget show")
	}
	on, which := kills.On()
	if !on {
		return "", ""
	}
	undo := selfCommand() + " budget resume"
	if which == "global" {
		undo += " --all"
	}
	return which, "the " + which + " kill switch is on, so no billable build is started: the owner turns it off with " + safetext.Field(undo)
}
