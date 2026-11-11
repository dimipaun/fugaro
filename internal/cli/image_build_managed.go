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

// `fugaro image build` under the managed gate: the catalog arm's own document
// and its own rules (M4, M6, D1011, D1013, D1022).
//
// What this file is, and what it stopped being. Until T4 a billable build was
// guarded by a confirmation, and in a managed project the gate was what stood in
// for it: the person at their own terminal was asked nothing, and the gate's
// verdict over the printed banner plus the project's own record of the build
// took the typed name's place. The typed name is gone from every arm of this
// command now (D1540, D1542) and what bounds a build is the lease the caller
// takes from the project's budget database before each submission
// (image_build_lease.go, D1543), so the prompt is gone from the gate to stand in
// for as well. What the gate still does here — and it is what it does in both
// sessions it is read for, the owner's terminal and a coding agent's — is the
// managed project's own judgement and record: each catalog entry is judged
// against the row §6 of docs/repo-image-removal/spec.md states for this command,
// the plan a rule refuses is refused rather than judged again, the verdict is
// written as the audit record (D1025), and the session reads the sentence of the
// branch it was admitted on (GateProceeded, AgentProceeded) or, in a coding
// agent's session, the refusal a verdict that did not come has always left there
// (D55). A project that is not managed, a mark that cannot be read and a project
// whose clients or budget database cannot be opened answer nil from
// openBuildManaged, and the build then takes its lease and runs — the same answer
// every session this file does not judge gets.
//
// Which arm the gate has a document for, and which it does not. The gate's rows
// are the design of record's, and for this command the row is the catalog entry:
// "admitted for a catalog entry — the entry exists in the effective catalog, the
// build account is the project's own for that entry's source repository, and the
// count is within D1013's max_image_builds bound" (docs/repo-image-removal/spec.md
// §6, D1206). The count clause went with the bound (T4, D1543). The derived arm —
// `--workflow`, or a checkout with no catalog — is written by no row anywhere, so
// no document of the gate's names it: that arm submits its build under its lease
// and is recorded by nothing here. That is also the whole of what a coding
// agent's session is judged for: the row above is the catalog entry, so that
// session is judged, admitted and recorded for an entry of the catalog and for
// nothing else this command builds.
//
// Why the derived arm is unjudged rather than refused. It is a build, and a build
// is the one thing the policy puts under the budget alone: no confirmation, no
// session refused for being unable to answer, the caps the only spending limit
// (docs/no-prompt-policy/spec.md, rules 2 and 4; D1540, D1543). So the derived arm
// refuses no session — a coding agent's session submits it — and the lease the
// caller takes immediately before the submission (openBuildLease) is the whole of
// what bounds it. D1016's read — a step the gate has no document for is not a step
// a session is admitted to — is `fugaro update`'s: its stages are what the gate's
// verdict stands in for, and §3.1 of docs/managed-agent-converge/spec.md is the
// list of commands it is read over. This command is not one of those seven, and
// the one row it does have is the catalog entry's above (§6 of the removal
// design, D1206) — so D1016 is not what its derived arm would be refused under,
// and reading it here would put a refusal in the one command the policy exempts
// from one.
//
// Why the kill switch is read, and read per build. D1013's bound was what made
// this row admissible rather than a licence, and the kill switch is the part of
// it that stands: it is re-read before EACH build, never once per command — an
// owner who stops the project means the build after that to stop, and a command
// that read it once would keep spending on a project that has just been told to
// stop (refreshManaged.admitBuild, the same call over the same object).
//
// Why the document is the sentence that was printed (What below). D1022's
// digest is what lets an owner check, months later, that the document a verdict
// was reached on is the document the command acted on; for this command the
// plan the person reads is the banner line, so the banner is IN the document
// rather than beside it. A record whose digest covered only the figures the
// rules were stated over would leave the one thing the person was shown out of
// what is provably what ran. One document per build, and the entry it is about
// is named in it: the catalog arm submits one build per entry, and a set of
// records that all carried one digest would not say which build was admitted
// (refreshBuildDoc's own reason for naming the build).
//
// Why the class is YesOrTyped, and why it decides less than it used to. YesOrTyped
// is D12's answer of 2026-10-20 and the class `fugaro image refresh`'s per-build
// document already carried (askBuild), and it is the class a build's admission is
// still made under. What it decides now is what the verdict is spent on — the
// record, and the line the session that was admitted reads (GateProceeded at a
// terminal, AgentProceeded under a marker) — and no longer whether anything is
// asked: with the confirmation gone, a managed project's terminal and a coding
// agent's session are judged by the same document and submit their builds under
// the lease the caller takes immediately after the verdict, and a session the
// verdict did not come for is refused rather than left to the lease alone
// (admitEntry). What keeps the admission from being a licence is the same as
// before minus the prompt: the rules below are re-stated over the submitted build
// rather than assumed from the code that computed it, and the admission — not the
// absence of a question — is what the record carries.

// buildManaged is `fugaro image build`'s own door: the installation the gate
// read, the gate itself, and the client the kill switch is re-read with before
// every build.
//
// nil is the whole of the difference for every session this file does not
// judge. A nil *buildManaged answers standsIn false, and admitEntry and
// recorded do nothing — which is the answer for an unmanaged project, a mark
// that cannot be read, a pipe and a project whose clients or budget database
// cannot be opened.
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
// session these rows are about.
//
// Why nil is the answer to every way of not being managed. A mark that cannot
// be read is not a permission (the direction every read in internal/infra/managed
// takes), a client that cannot be built is an account that cannot act, and a
// budget database the command cannot open is a project whose ownership question
// it cannot ask (D1017) — each of them is "the gate did not admit this" and
// returns nil, so the build is judged by nothing but the lease the caller takes
// and is never met with an error about the installation.
//
// Why a coding agent's session is read for, and read for here. The gate's
// verdict is what replaces the confirmation in BOTH sessions the confirmation
// used to decide: the owner at their own terminal, whose prompt the verdict
// stands in for, and a coding agent's session, where the verdict is what the
// session proceeds on (D55, managed-agent-converge §3.1). This command's row is
// the catalog entry, so the gate has a document for a catalog build and the
// agent's session is judged by it exactly as `fugaro image refresh` judges its
// own per-build document — and recorded by it, which is the audit the owner has
// instead of the prompts (D1025). A session this file does not judge — a pipe,
// which the rows are not stated for and which has no verdict to act on — pays
// nothing for the read and submits its build under its lease alone.
func openBuildManaged(ctx context.Context, env *cloudEnv, c initflow.Conditions) *buildManaged {
	if !judgedHere(c) {
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

// judgedHere is the session this file's gate is read for and judges, named once
// so the door and standsIn cannot answer two different questions: the owner at
// their own terminal, and a coding agent's session — the two readers
// managed-agent-converge §3.1 states the gate's verdict for.
func judgedHere(c initflow.Conditions) bool {
	return initflow.PersonAtTheTerminal(c) || c.Agent != ""
}

// standsIn is whether this session's builds are judged by the gate: the door's
// read answered "managed" and this is a session the rows are stated for
// (judgedHere).
//
// The predicate is asked again here rather than remembered from the door,
// because the session the record is scoped to and the session the door read for
// are one reading of the same facts, taken where each is decided: a caller
// cannot end up writing one session's record for another session's build.
func (m *buildManaged) standsIn(c initflow.Conditions) bool {
	return m != nil && m.gate != nil && judgedHere(c)
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
// its confirmation prints (What), the plan the command was asked for (Entries),
// the entry one admission is about (Entry), and the facts the gate's rules are
// stated over. The two are one value because they are one plan: what the person
// is shown is what the digest covers, and a caller cannot print one sentence
// and have another judged.
//
// Origin is the checkout's own repository and Repo is the one the build is
// submitted for; the two are equal by construction (both arms refuse a --repo
// that is not the checkout's origin, as canonical paths) and the rule restates
// it at the moment of the write, which is what a rule in this package is for.
//
// KillSwitch is filled in per build rather than once: it is the fact the
// rules re-read before each one, so a document that carried the command's
// first read would say the verdict was reached on a switch that was not in
// force when this build was judged.
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

// buildDocumentVersion is the document's own version. It moved from 1 to 2
// when the build-count bound went (T4, D1543): max_image_builds and
// builds_started were removed from the document, and a record's plan_digest is
// what an owner checks a document against months later, so a document whose
// shape changed under the same version would make that check pass for bytes
// nobody judged.
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
// the document is built and judged, the gate re-reads the marks immediately
// before the write (D1024) and writes the record of a refusal itself, and only
// then is the verdict spent. A gate that refuses the plan is a refusal and never
// a judgement to work around, and a gate that finds the project is not managed
// any more (the second read's own answer, admit's zero Managed) leaves the build
// to its lease exactly as a session this file does not judge. What is left for
// the verdict to decide is the record and the line the session reads, because
// the build's confirmation is gone from every session (T4, D1540).
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
	// The verdict is the record of what was judged, not a permission: what
	// bounds this build is the lease the caller takes immediately after it.
	// What the verdict decides here is what this session reads, and the two
	// sessions are the two the gate is read for (judgedHere).
	//
	// The owner at their own terminal reads the gate's own sentence where the
	// typed name would have been. A coding agent's session reads
	// AgentProceeded's — the sentence this design already carries for exactly
	// this case, a step of a managed project whose plan the gate admitted
	// (managed-agent-converge §3.1) — and is REFUSED where the verdict did not
	// stand in for the confirmation the session cannot give: the second read
	// found the project is not managed any more, the inventory fell back, or
	// the class is not one the gate covers. That refusal is the answer the
	// agent's session got before T4 and it is right where the gate cannot
	// replace the prompt (D55): nothing a session can type reaches past an
	// unadmitted plan, and a session that has no admission to proceed on
	// proceeds on nothing. refreshBuilds' announceBuild gives its own
	// per-build admission the same two answers.
	if initflow.ConfirmsByGate(initflow.YesOrTyped, c, adm) {
		fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", initflow.GateProceeded())
	} else if c.Agent != "" {
		if !initflow.CanConfirmManaged(initflow.YesOrTyped, c, adm) {
			return &initflow.AgentError{Marker: c.Agent}
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", initflow.AgentProceeded(c.Agent))
	}
	return nil
}

// recorded writes the record of the build the gate admitted, and it is called
// after the submission that IS the write: an admission whose build was never
// submitted records nothing, because the record says a build happened. A
// command that submitted nothing (a gate that did not stand in, a build that
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
// that entry's source repository — plus the kill switch and the restatements
// every rule set in this package carries.
//
// A prebuilt entry is judged by the same row read for what its run is — an
// entry of the catalog, submitted as this project's own build account, within
// the bound, under the same switch — and the two rules that are about a build's
// own inputs (the base and the registry it pushes to) are not its own and are
// left out rather than reported as passed (the bound counts a smoke the same
// way: it is a billable Cloud Build either way).
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
	// A prebuilt entry's run is a smoke-only Cloud Build (F10/IS11): it builds
	// from no base and pushes into no registry, so the two rules stated over
	// those are not its own, and reporting them as passed would say the verdict
	// was reached on values this submission does not carry. Everything else
	// below is stated over the same values either way — the entry, the account
	// the run is submitted as, the kill switch and the repository.
	prebuilt := e.Source.Kind == config.SourceImage
	host := ""
	if !prebuilt {
		var err error
		if host, err = infra.RegistryHost(lc); err != nil {
			// A host this installation's own config cannot state is not a build
			// this gate can judge: the rules below are stated over it, and a rule
			// that cannot be evaluated is a refusal rather than a pass.
			return nil, []string{"this installation's own registry host is readable (" + safetext.FieldErr(err).Error() + ")"}
		}
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
	// The switch that stops every build of this project is off. It is the one
	// bound left in this document: what a build costs is bounded by the lease
	// the caller takes immediately after this judgement (T4, D1543), not by a
	// count in the project's marker.
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
	if !prebuilt {
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
