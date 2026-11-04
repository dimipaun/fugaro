package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/budget"
	inframanaged "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// `fugaro image refresh` under the managed gate (M4, D1011, D1013).
//
// Why this command is in the set at all, and what makes it unlike the others.
// `fugaro update` applies Terraform and the gate there judges a saved plan; a
// refresh applies no Terraform at all and submits Cloud Builds, each of which
// is billable, so there is no plan to judge and the document the gate reads is
// the command's own per-build list (D1022, spec §4.2). That is also why the
// command carries a control no other command in the set has: the COUNT of
// billable builds one invocation may start, read from the project's creation
// fact (M5's max_image_builds, D1013), which is what the agent's session is
// bounded by where a person's own run has always had the per-build question
// and no cap.
//
// What this file does, in the order the helper's own comment lays out:
// `openRefreshManaged` is the ONE read per command (D1015) and the door: it is
// made only where the coding agent's refusal would have been made, so a
// person's own run and a project that is not managed pay nothing for it and
// keep today's behaviour exactly. `admitBuild` is one build's judgement: the
// marker and the kill switch re-read (D1013 — before EACH build, never once
// per command), the plan's M4 rules, and the gate's answer through M6's
// helper. The command's own write (the Cloud Build submission) and the
// helper's `recorded` call follow in refreshBuilds.
//
// What this file does NOT gate, said here rather than left to be discovered.
// Steps 2 and 3 — the base pin and the daily image check job — keep their own
// confirmations and their own coding-agent refusal: they are not builds, they
// are not billable, and the confirmations they take are not this task's files
// (init_images.go and init_review.go). A session that reaches either of them
// with work to do is refused there, which is the pre-M4 behaviour and not a
// widening of it.

// refreshManaged is a managed project's refresh in progress: the one gate
// helper M6 owns, the installation the gate read, and the count of builds this
// invocation has already started against the bound.
type refreshManaged struct {
	gate managedGate
	// lc is the installation the gate read, and it is the SAME value
	// refreshPreflight is handed: a plan judged against one installation and
	// applied to another is the drift internal/infra/managed's own warning
	// about gcpProject is about, so the selection is made once and used twice.
	lc    *localcfg.Config
	lcPath string
	lcOld []byte
	// s, crm and db are the clients the read used, kept because the bound and
	// the kill switch are re-read with them before every build.
	s   *storage.Service
	crm *crm.Service
	db  *rtdb.Client
	// started is how many builds this invocation has submitted, and it is what
	// the bound is counted against: M5's bound is per invocation of this
	// command (ImageBuilds' own comment), so a rerun gets the count again
	// rather than inheriting the previous run's.
	started int64
}

// openRefreshManaged is the door: the gate's read, made once per command, in
// the one session that would otherwise be refused before anything ran.
//
// Why nil is the answer to every way of not being managed. The refusal it
// stands in for is refuseRefreshHere's own — a coding agent's session applies
// nothing — and in a session that is not a managed project's that refusal is
// exactly what the run gets, with the sentence it has always had. A mark that
// cannot be read is not a permission (the direction every read in
// internal/infra/managed takes), a config that cannot be selected is a project
// this command has nothing to read about, and a client that cannot be built is
// an account that cannot act: each of them is "the gate did not admit this",
// and none of them is a reason to answer with a sentence of its own — a
// session that read a different error here would learn which part of the
// installation to break next.
//
// Why nothing is read for a session that is not a coding agent's. A person at
// their own terminal has a confirmation for every step this command takes,
// which the gate could only ever take away; they gain nothing from the marks
// and must not pay for them, so a transient storage or Resource Manager
// failure cannot stop a run that has no reason to make either call (M6's
// review of 2026-11-03).
func openRefreshManaged(ctx context.Context, o refreshOptions, iopts *initOptions) *refreshManaged {
	if agentMarker(os.Getenv) == "" {
		return nil
	}
	lc, path, old, err := loadRepoConfig(ctx, iopts, o.checkoutDir())
	if err != nil || lc == nil {
		return nil
	}
	c, err := newInitClients(ctx, lc)
	if err != nil || c == nil || c.Storage == nil || c.CRM == nil {
		return nil
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil
	}
	gate, err := readManagedGate(ctx, managedGateEnv{lc: lc, s: c.Storage, crm: c.CRM, db: db}, refreshGateFacts())
	if err != nil || !gate.managed {
		return nil
	}
	return &refreshManaged{gate: gate, lc: lc, lcPath: path, lcOld: old, s: c.Storage, crm: c.CRM, db: db}
}

// refreshGateFacts is this command's own facts about itself, as M6's helper
// takes them. There is no secret-bearing argument to leave out: every argument
// this command accepts is a repository, a workflow, a kind or an endpoint, and
// a refresh admits no command that takes a credential in the first place.
func refreshGateFacts() managedGateFacts {
	return managedGateFacts{
		Subcommand:  "image refresh",
		Argv:        os.Args[1:],
		Declined:    "nothing was built",
		AgentMarker: agentMarker(os.Getenv),
	}
}

// refreshBuildDoc is the document one build's verdict is about: the plan's
// per-build list, and which entry of it is being judged (D1022's "the per-build
// list for image refresh"). The list is in the plan rather than in this
// struct's order because that is the order the builds run in and the order the
// agent read in the printed plan; the entry is named because one record per
// build that all carried the same digest would not say which build was
// admitted, and the count and the bound are carried because they are what the
// verdict was reached on.
type refreshBuildDoc struct {
	Version int      `json:"version"`
	Repo    string   `json:"repo"`
	Builds  []string `json:"builds"`
	Build   string   `json:"build"`
	Kind    string   `json:"kind"`
	Base    string   `json:"base"`
	Account string   `json:"build_account"`
	Bound   int64    `json:"max_image_builds"`
	Started int64    `json:"builds_started"`
	// KillSwitch is the switch that was in force when this build was judged:
	// empty when both are off, "global" or "repo" when one is on. A build that
	// ran under a switch an operator turned on between builds is exactly the
	// case D1013's re-read exists for, and the record says so.
	KillSwitch string `json:"kill_switch,omitempty"`
}

// refreshBuildDocVersion is the document's own version. It is 1 because a
// record's plan_digest is what an owner checks a document against months
// later, and a document whose shape changed under the same version would make
// that check pass for bytes nobody judged.
const refreshBuildDocVersion = 1

// refreshBuildDocBytes renders one build's document, and it is deterministic:
// a struct's fields and not a map's, and the plan's own order, so the same
// plan hashes to the same digest in every session that reads it.
func refreshBuildDocBytes(t *refreshTarget, build, kind string, planBuilds []string, bound managedmode.ImageBuilds, started int64, killed string) ([]byte, error) {
	doc := refreshBuildDoc{
		Version:    refreshBuildDocVersion,
		Repo:       t.spec.Name,
		Builds:     slices.Clone(planBuilds),
		Build:      build,
		Kind:       kind,
		Base:       t.lc.BaseImage(kind),
		Account:    t.spec.BuildServiceAccountEmail,
		Bound:      bound.Count(),
		Started:    started,
		KillSwitch: killed,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, userErr("the per-build document of %s/%s could not be rendered, and a document the gate cannot read is a build it does not judge: %v", t.spec.Name, build, err)
	}
	return b, nil
}

// admitBuild is one billable build's whole judgement, in the order the plan's
// M4 entry lists the rules.
//
// The two reads come first and are made here rather than once per command
// (D1013): the bound the marker records, and the project's kill switches. Both
// can change while a refresh runs — an owner who lowers the bound or stops the
// project means the build AFTER that to stop — and a command that read either
// of them once would keep spending on a project that has just been told to
// stop. The bound's read is the one thing here that is an error rather than a
// refusal when it answers nothing: a bound nothing recorded is not a bound of
// zero, it is a verdict the gate cannot reach, so no document is judged, no
// record is written, and the command stops (M0's rule for a document that
// could not be read at all).
//
// The class is YesOrTyped because that is the class this command's build
// confirmation already is (askBuild's --yes, the owner's 2026-10-08 decision);
// the gate's own class rule is what keeps a Typed step — money or permanent,
// in initflow's own words — out of an agent's session whatever the verdict
// says.
func (m *refreshManaged) admitBuild(ctx context.Context, p *refreshPlan, t *refreshTarget, build, host string) (initflow.Managed, error) {
	bound, err := m.imageBuildBound(ctx)
	if err != nil {
		return initflow.Managed{}, err
	}
	killed, killRule := m.killSwitch(ctx, t.spec.Slug)
	kind := t.cfg.Workflows[build].BaseKind()
	doc, err := refreshBuildDocBytes(t, build, kind, p.builds, bound, m.started, killed)
	if err != nil {
		return initflow.Managed{}, err
	}
	rules, refused := refreshBuildRules(t, p, build, kind, host, bound, m.started, killRule)
	conv := managedmode.ConvergeAdmitted(doc, initflow.YesOrTyped, rules)
	if len(refused) > 0 {
		conv = managedmode.ConvergeRefused(doc, initflow.YesOrTyped, refused)
	}
	return m.gate.admit(ctx, initflow.YesOrTyped, conv)
}

// refreshBuildRules is the plan's M4 entry as rules over this one build: each
// rule that was applied and passed goes into rules, and each that refused goes
// into refused, so a refusal reads as the rule that refused and an admission as
// the rules it was reached on (Converge's own two answers).
func refreshBuildRules(t *refreshTarget, p *refreshPlan, build, kind, host string, bound managedmode.ImageBuilds, started int64, killRule string) (rules, refused []string) {
	base, account := t.lc.BaseImage(kind), t.spec.BuildServiceAccountEmail
	// The plan is what the agent read and what a person would have read: a
	// build the skew rules did not put in it is a build nothing printed, so it
	// is refused here rather than submitted off the back of a build record
	// that changed after the plan was printed.
	if !slices.Contains(p.builds, build) {
		refused = append(refused, "the build is one the printed plan's per-build list names")
	} else {
		rules = append(rules, "the build is one the printed plan's per-build list names")
	}
	// A base that is not one of this installation's own release images is a
	// base the refresh copies nothing to and does not build from: preflight
	// refuses a custom base earlier (customBaseRefusal), and this is the same
	// rule at the moment of the write.
	if !isManaged(base, host, kind, baseOwner(t.lc)) {
		refused = append(refused, "the base is one of this installation's own release images ("+safetext.Field(base)+" is not)")
	} else {
		rules = append(rules, "the base is one of this installation's own release images ("+safetext.Field(base)+")")
	}
	if strings.TrimSpace(account) == "" {
		refused = append(refused, "the build runs as this project's own build account")
	} else {
		rules = append(rules, "the build runs as this project's own build account ("+safetext.Field(account)+")")
	}
	if killRule != "" {
		refused = append(refused, killRule)
	} else {
		rules = append(rules, "the project's kill switch is off")
	}
	if started < bound.Count() {
		rules = append(rules, fmt.Sprintf("the build is within the bound the project's marker records (%d per invocation, %d already submitted)", bound.Count(), started))
	} else {
		refused = append(refused, fmt.Sprintf("the build is past the bound the project's marker records (%d per invocation, and %d builds have already been submitted by this one), so nothing further is built here: the owner runs %s in their own terminal window, where a person's own run has the per-build question and no bound, or records a larger max_image_builds for the project (D1013)",
			bound.Count(), started, safetext.Field(selfCommand()+" image refresh")))
	}
	return rules, refused
}

// imageBuildBound is the bound in force right now, read from the project's
// creation fact through M5's read and put to M5's decision: the figure the
// marker records, the default of one for the versions that record none, and a
// refusal for everything else (an unknown version, a figure outside the
// marker's own range, a project that stopped being managed). It is the same
// pair of calls managed_apply.go's budget gate makes, over the same object and
// the same project-number proof.
func (m *refreshManaged) imageBuildBound(ctx context.Context) (managedmode.ImageBuilds, error) {
	a, err := inframanaged.ReadImageBuilds(ctx, m.s, m.crm, m.lc.Name, m.lc.GCPProject)
	read := managedmode.ImageBuildsRead{Project: m.lc.Name, Builds: a.Builds, OK: a.OK, Default: a.Default, Invalid: a.Invalid, Err: err}
	if !a.OK && !a.Invalid && err == nil {
		// The read that cannot tell a managed creation which recorded no bound
		// from a project that is not managed at all: the default of one is
		// only for the first, so the second question is asked (and only then),
		// exactly as the budget gate asks it.
		isManaged, reason, _ := inframanaged.IsManaged(ctx, m.s, m.crm, m.lc.Name, m.lc.GCPProject)
		read.Managed, read.ManagedReason = isManaged, reason
	}
	bound, err := managedmode.ImageBuildsBound(read)
	if err != nil {
		return managedmode.ImageBuilds{}, userErr("the bound on this project's billable image builds was not read, so no build of %s is started: %v", m.lc.Name, err)
	}
	return bound, nil
}

// killSwitch is the project's two kill switches as they stand right now, and
// the rule a build under them reads: "" when both are off, and the refusal's
// own sentence when one is on — or when the read answered nothing at all,
// which D1013 counts as ON for the reason budget's own decode gives: the
// reason an operator wrote a switch is lost when it cannot be read, and the
// intent to stop is not.
func (m *refreshManaged) killSwitch(ctx context.Context, slug string) (which, rule string) {
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
