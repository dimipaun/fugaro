package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// `fugaro image build` under the managed gate: the typed prompt's replacement
// for a person at their own terminal in a managed project (NP3, the owner's
// ruling of 2026-11-06, which #1845 and image_refresh_steps.go's askBuild
// already applied elsewhere).
//
// What was wrong. A Cloud Build is billable, so every arm of this command asked
// for the project's name typed at a real terminal (confirmBuild). In a managed
// project that is the one thing the ruling says does not happen: the person at
// a real terminal is asked nothing, and what stands in for the typed name is
// the gate's verdict over the plan that was printed plus the project's own
// record of the build. The prompt the owner met by running `fugaro image build
// go` on the managed project was exactly this one, and #1845 had already given
// the same treatment to the policy-migration prompt; what was missing was the
// audit of the rest of the set (the PR body classifies every one of them).
//
// The shape is #1845's (init_policy_migrate.go's policyMigrationConfirm), which
// is what the ruling asks for and what the commands in the set already share:
// a person at a real terminal on a managed project reads the plan and a
// sentence saying why nothing is typed, and the step's own record is written
// after it. Here the record is not a courtesy: the build is billable and
// nothing types, so the record is the only account of what was submitted and
// against which document.
//
// Why the document is the sentence that was printed (What below). D1022's
// digest is what lets an owner check, months later, that the document a verdict
// was reached on is the document the command acted on; for this command the
// plan the person would have read is the banner line, so the banner is IN the
// document rather than beside it. A record whose digest covered only the
// figures the rules were stated over would leave the one thing the person was
// shown out of what is provably what ran.
//
// Why the class is YesOrTyped, and why that is not a widening. A billable build
// is money, and D1026 keeps a Typed step — money or permanent — out of every
// plan gate; but this command's confirmation is not a Typed one in the first
// place: `fugaro image refresh`'s own per-build confirmation is YesOrTyped
// (D12's answer of 2026-10-20, askBuild), `--yes` has always covered a
// converge's first build (askCovered), and YesOrTyped is the class D1026's own
// rule lets a document admit. The three things that keep this from being a
// licence: the door opens only for initflow.PersonAtTheTerminal (so a coding
// agent's session and a pipeline keep today's refusal, unchanged and reached
// before any of this file runs), the rules below are re-stated over the
// submitted build rather than assumed from the code that computed it, and the
// admission — not the absence of a prompt — is what the record carries.
//
// What this file does NOT do, said here rather than left to be found. It does
// not read the project's bound on billable builds (M5's max_image_builds,
// D1013) and it does not read the kill switch. Both belong to `fugaro image
// refresh`, whose one invocation submits a build per workflow and is bounded
// per invocation for that reason; here one invocation submits one build (the
// derived arm) or one per entry the command was asked for (the catalog arm,
// every one of them named by the operator or by the catalog the project
// published), and the accounting of the whole command is the ONE document and
// the ONE record below. A bound of one per invocation — the default M5 records
// for a marker with no figure — would refuse the owner's own catalog build at
// its second entry, which is not a tightening of a confirmation but the
// refusal of a command the person asked for.
//
// It also does not touch the run's own `--yes`: this command has no such flag
// (its conditions are the terminal, --json and the marker and nothing else), so
// the note below is reached only where the typed prompt would have been.

// buildManaged is `fugaro image build`'s own door: the installation's config,
// and the gate when the session is the one the ruling is about.
type buildManaged struct {
	// lc is the installation the gate read, and the one this command already
	// acts on: the note's project name, the recorded project and the rules'
	// own project all come from it.
	lc *localcfg.Config
	// gate is nil for every session but a person at a real terminal in a
	// managed project, and nil is what keeps every other session's confirmation
	// exactly what it has always been (confirmBuild's typed name).
	gate *managedGate
}

// openBuildManaged is the door: the gate's read, made once per command, in the
// one session whose typed prompt the ruling takes away.
//
// Why nil is the answer to every way of not being managed. The prompt this
// stands in for is confirmBuild's, and in a session that is not a managed
// project's it is exactly what the run gets. A mark that cannot be read is not
// a permission (the direction every read in internal/infra/managed takes), a
// client that cannot be built is an account that cannot act, and a budget
// database the command cannot open is a project whose ownership question it
// cannot ask (D1017) — each of them is "the gate did not admit this" and
// returns the zero value, so the person is asked the question they have always
// been asked rather than met with an error about the installation.
//
// Why nothing is read for a session that is not a person at the terminal. This
// command refuses --json, a pipe and a coding agent's session before the cloud
// is opened at all (buildNotConfirmable), so those sessions never reach here;
// the predicate is asked anyway, because the door is the one place that decides
// it and a caller that read the marks for a session the gate cannot answer for
// would be paying for two calls to widen nothing.
func openBuildManaged(ctx context.Context, env *cloudEnv, c initflow.Conditions) *buildManaged {
	b := &buildManaged{lc: env.lc}
	if !initflow.PersonAtTheTerminal(c) {
		return b
	}
	cl, err := newInitClients(ctx, env.lc)
	if err != nil || cl == nil || cl.Storage == nil || cl.CRM == nil {
		return b
	}
	_, db, err := budgetDBFor(ctx, env.lc)
	if err != nil {
		return b
	}
	gate, err := readManagedGate(ctx, managedGateEnv{lc: env.lc, s: cl.Storage, crm: cl.CRM, db: db}, buildGateFacts())
	if err != nil || !gate.managed {
		return b
	}
	b.gate = &gate
	return b
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
// its confirmation prints (What), and the facts the gate's rules are stated
// over. The two are one value because they are one plan: what the person is
// shown is what the digest covers, and a caller cannot print one sentence and
// have another judged.
//
// Origin is the checkout's own repository and Repo is the one the build is
// submitted for; the two are equal by construction (both arms refuse a --repo
// that is not the checkout's origin) and the rule restates it at the moment of
// the write, which is what a rule in this package is for.
type buildPlan struct {
	Version  int      `json:"version"`
	Repo     string   `json:"repo"`
	Origin   string   `json:"origin"`
	Workflow string   `json:"workflow,omitempty"`
	Entries  []string `json:"entries,omitempty"`
	Kind     string   `json:"base_kind"`
	Base     string   `json:"base"`
	Account  string   `json:"build_account"`
	Registry string   `json:"registry"`
	Machine  string   `json:"machine_type"`
	What     string   `json:"what"`
}

// buildDocumentVersion is the document's own version. It is 1 because a
// record's plan_digest is what an owner checks a document against months later,
// and a document whose shape changed under the same version would make that
// check pass for bytes nobody judged.
const buildDocumentVersion = 1

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

// confirm is one Cloud Build's whole confirmation: the typed name where the
// gate does not stand in for it, and the printed plan plus the gate's verdict
// where it does.
//
// The order is M6's (managed_gate.go's own comment): the document is built and
// judged, the gate re-reads the marks immediately before the write (D1024) and
// writes the record of a refusal itself, and only then is the confirmation
// decided. A gate that refuses the plan is a refusal and never a prompt — a
// plan a rule refused is not one a person's typed name should confirm — and a
// gate that finds the project is not managed any more (the second read's own
// answer, admit's zero Managed) leaves the typed question exactly where it was.
//
// The conditions are asked again here rather than remembered from the door: the
// session the note is scoped to and the session the prompt is scoped to are one
// reading of the same facts, taken where each is decided, so a caller cannot
// end up in one branch with the other's conditions.
func (b *buildManaged) confirm(ctx context.Context, cmd *cobra.Command, c initflow.Conditions, p buildPlan) error {
	if b.gate == nil {
		return confirmBuild(cmd, b.lc.Name, b.lc.GCPProject, p.What)
	}
	doc, err := p.bytes()
	if err != nil {
		return err
	}
	rules, refused := buildRules(b.lc, p)
	conv := managedmode.ConvergeAdmitted(doc, initflow.YesOrTyped, rules)
	if len(refused) > 0 {
		conv = managedmode.ConvergeRefused(doc, initflow.YesOrTyped, refused)
	}
	m, err := b.gate.admit(ctx, initflow.YesOrTyped, conv)
	if err != nil {
		return err
	}
	if !initflow.ConfirmsByGate(initflow.YesOrTyped, c, m) {
		return confirmBuild(cmd, b.lc.Name, b.lc.GCPProject, p.What)
	}
	w := cmd.ErrOrStderr()
	fmt.Fprintf(w, "⚠ CONFIRM (project %s, GCP project %s): %s\n", b.lc.Name, b.lc.GCPProject, p.What)
	fmt.Fprintf(w, "  %s\n", initflow.GateProceeded())
	return nil
}

// recorded writes the record of the build the gate admitted, and it is the
// command's own last call, after the submission that IS the write: an admission
// whose build was never submitted records nothing, because the record says a
// build happened. A command that submitted nothing (a gate that did not stand
// in, a build that failed before the submission) has nothing to record.
//
// One record per command, however many builds it submits: the verdict is spent
// by the first call (managedGate.recorded), and the catalog arm's several
// entries are one plan, one admission and one record.
func (b *buildManaged) recorded(ctx context.Context) error {
	if b.gate == nil {
		return nil
	}
	return b.gate.recorded(ctx)
}

// buildRules is the plan's own rules as rules over this one build: each rule
// that was applied and passed goes into rules, and each that refused goes into
// refused, so a refusal reads as the rule that refused and an admission as the
// rules it was reached on (Converge's own two answers).
//
// Every rule here restates something the command has already enforced by the
// time it submits — the checkout's own repository, a base that is not another
// project's pull-through reference, this project's own build account, this
// installation's own registry. That is deliberate rather than redundant: the
// rules are what the record says the verdict was reached on, so they are
// written over the values that are actually submitted (infra.Repo's spec) and
// never over the inputs that produced them, and the gate is the last place a
// drift between the two would be seen.
func buildRules(lc *localcfg.Config, p buildPlan) (rules, refused []string) {
	host, err := infra.RegistryHost(lc)
	if err != nil {
		// A host this installation's own config cannot state is not a build
		// this gate can judge: the two rules below are stated over it, and a
		// rule that cannot be evaluated is a refusal rather than a pass.
		return nil, []string{"this installation's own registry host is readable (" + safetext.FieldErr(err).Error() + ")"}
	}
	if p.Repo != p.Origin || p.Repo == "" {
		refused = append(refused, "the build is this checkout's own repository ("+safetext.Field(p.Repo)+" is not "+safetext.Field(p.Origin)+")")
	} else {
		rules = append(rules, "the build is this checkout's own repository ("+safetext.Field(p.Repo)+")")
	}
	if foreignProxyRef(p.Base, host, p.Kind, baseOwner(lc)) {
		refused = append(refused, "the base image is not another project's pull-through reference ("+safetext.Field(p.Base)+" is)")
	} else {
		rules = append(rules, "the base image is not another project's pull-through reference ("+safetext.Field(p.Base)+")")
	}
	if !strings.HasSuffix(p.Account, "@"+lc.GCPProject+".iam.gserviceaccount.com") {
		refused = append(refused, "the build runs as this project's own build account ("+safetext.Field(p.Account)+" is not "+safetext.Field(lc.GCPProject)+"'s)")
	} else {
		rules = append(rules, "the build runs as this project's own build account ("+safetext.Field(p.Account)+")")
	}
	if !strings.HasPrefix(p.Registry, host+"/") {
		refused = append(refused, "the build pushes into this installation's own registry ("+safetext.Field(p.Registry)+" is not under "+safetext.Field(host)+"/)")
	} else {
		rules = append(rules, "the build pushes into this installation's own registry ("+safetext.Field(p.Registry)+")")
	}
	return rules, refused
}
