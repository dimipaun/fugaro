package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The deployed security rules generation is what a published project policy is
// READ under: the rules carry the policy node and the grant a run reads it
// with, and the marker records which generation is deployed. It was deployed by
// `fugaro update --firebase-project` alone, and the policy stage's write holds
// the publisher's gate on the marker (requirePolicyRules), so an installation
// whose rules predate the policy model met the gate and refused — leaving one
// `fugaro update` unfinished and the owner with a second command to run against
// the rule of 2026-10-30. This stage deploys the generation this binary carries
// before the policy stage's own, in the same run and behind the same
// confirmation, using the same plan, apply and sentences as the database step
// of `--firebase-project` (init_firebase.go's deployDatabase).
//
// It never deploys over rules a newer Fugaro wrote: the classification is
// doctor's own (rulesSkewOf), so the doctor's policy-rules-skew line and this
// stage cannot answer the same question differently, and the one state that
// classification calls newer — a marker above this binary's RulesVersion — is
// reported rather than deployed over (infra.DB.Plan refuses it as well, for a
// caller that got here another way). The states in which the deployed rules
// cannot be read or this binary's own cannot be generated are reported too: a
// read that failed is not "the rules are current", and neither is a refusal
// this stage would not repair.
//
// `--firebase-project` keeps its meaning: it is the door that adopts a Firebase
// project and builds the backend in it, and it is where an installation with no
// shared budget gets one. This stage does nothing there (no budget.firebase_project
// recorded, so there are no deployed rules to read) and nothing over the
// deployed rules when they are already this generation.

// forgetPolicyMigration drops the policy stage's cached read, when it has one.
// The stage is built by initStages, so a run that never registered it (an
// engine a test drives on its own) has nothing to drop.
func (e *initEngine) forgetPolicyMigration() {
	if e.policy != nil {
		e.policy.forget()
	}
}

// rulesStage is the stage itself. It is SelfConfirming, like the engines and
// the policy stage: it shows what it would write and takes its own
// confirmation (the run's one confirmation covers it where the Firebase project
// is verified as the project's own, as it covers the database step).
type rulesStage struct{ e *initEngine }

func newRulesStage(e *initEngine) *rulesStage { return &rulesStage{e: e} }

func (*rulesStage) Name() string    { return initflow.Rules }
func (*rulesStage) SelfConfirming() {}

func (s *rulesStage) Left() initflow.Left {
	return promptLeft(initflow.Rules, "type the project's name to deploy the security rules when asked", s.e.r.o.doorName())
}

// rulesNoBudget is why the stage does nothing here, "" when the installation
// has a shared budget backend. It is a skip and not a refusal: a project that
// never adopted a Firebase backend has no deployed rules to be stale, and its
// runs read no policy node either, so `fugaro update` converges there as it
// always did. The two ways to be without one are named apart, because the move
// differs: nothing at all, and the other half of the pair `--firebase-project`
// records together.
func rulesNoBudget(lc *localcfg.Config) string {
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" {
		return "the project config records no shared Firebase budget backend (budget.firebase_project): fugaro update --firebase-project adopts one, and the security rules are deployed with it"
	}
	if lc.Budget.RTDBURL == "" {
		return "the project config records a Firebase project (budget.firebase_project) and no database URL (budget.rtdb_url), so there is no deployed rules generation to read: fugaro update --firebase-project records both"
	}
	return ""
}

// rulesForeignDatabase is why the stage must not read or write the database the
// config names, "" when the pair agrees. The two halves are one fact — the
// database a Firebase project's own — and `infra.DB.Check` is not this check: a
// database that is EMPTY passes it whatever project the URL belongs to, so a
// config whose two halves disagree had this stage write Fugaro's mark and its
// rules into a database that is not the project's (the security review's
// finding). The predicate is the one the history sweeper already refuses a
// mismatched pair with (checkDatabaseHost), so one config cannot be safe to
// sweep and unsafe to deploy into.
func rulesForeignDatabase(lc *localcfg.Config) string {
	if lc.Budget == nil || lc.Budget.RTDBURL == "" || lc.Budget.FirebaseProject == "" {
		return ""
	}
	if checkDatabaseHost(lc.Budget.RTDBURL, lc.Budget.FirebaseProject) == nil {
		return ""
	}
	return fmt.Sprintf("the config's budget.rtdb_url (%s) is not the database of the Firebase project its budget.firebase_project names (%s): a Firebase project's own database is <project>-default-rtdb, so one of the two is wrong and nothing here was read or written; fugaro update --firebase-project <project> records the pair together",
		oneLine(lc.Budget.RTDBURL), oneLine(lc.Budget.FirebaseProject))
}

// rulesUnavailable is why the stage does not run, "" when it may: no shared
// budget backend at all, or one whose database is not the Firebase project's
// own. Both are reported and neither is a refusal that stops the run, because
// `fugaro update`'s other stages are what the owner came for.
func rulesUnavailable(lc *localcfg.Config) string {
	if why := rulesNoBudget(lc); why != "" {
		return why
	}
	return rulesForeignDatabase(lc)
}

// rulesStageState is the state one read of the deployed rules puts the stage in,
// and the sentence its result carries: whether this generation has to be
// deployed over them, or why it must not be and what that means.
//
// A marker below this generation (or absent) is stale, and the deploy is the
// upgrade the doctor's own fix names. A marker above it is not this binary's to
// deploy over, whatever the document says — the rules came from a newer Fugaro,
// and this release's template would replace them. The read that failed and the
// generation that could not be produced are the two states in which nothing can
// be said about the deployed document at all; both are reported and neither
// stops the run, because a run of `fugaro update` that refused to go on over a
// credential it could not read would leave the owner no way through.
//
// The state between generation 1 and this one carries doctor's own sentence and
// not a shorter one of the stage's: that deploy adds the mailbox subtree and the
// device binding, and a phone account with no device node reads and writes
// nothing under the rules it lands — a lock-out the operator has to know about
// BEFORE confirming, which is what this detail is (the review screen prints it
// beside the plan, and --yes prints it in the run's own account). It is one text
// per state, doctor's, so the report and the deploy that follows it cannot
// describe the same state differently.
func rulesStageState(skew rulesSkew) (initflow.State, string) {
	marker := projectpolicy.PathRulesMarker()
	switch skew.kind {
	case rulesSkewCurrent:
		return initflow.Done, fmt.Sprintf("the deployed security rules are generation %d, this Fugaro's own", skew.gen)
	case rulesSkewAbsentMarker:
		return initflow.Todo, "the deployed security rules carry no generation marker, so they are not the rules this Fugaro reads a project policy under"
	case rulesSkewPrePolicy:
		return initflow.Todo, fmt.Sprintf("the deployed security rules are generation %d, which predates the project policy: this Fugaro deploys generation %d", skew.gen, projectpolicy.RulesVersion)
	case rulesSkewOlder:
		return initflow.Todo, rulesMarkerOlderText(skew.gen)
	case rulesSkewDocument:
		return initflow.Todo, "the deployed security rules are not the document this Fugaro generates: this Fugaro redeploys the generation a project policy is read under"
	case rulesSkewNewer:
		return initflow.Skipped, rulesMarkerNewerText(marker, skew.gen)
	case rulesSkewBadMarker:
		return initflow.Skipped, rulesMarkerBadText(skew.raw)
	case rulesSkewMarkerUnreadable:
		// Doctor says nothing of this state: its policy-published line is where
		// a database a reader could not reach is reported, and this check has
		// no more to add to it. The stage does, because it is the stage that
		// would have deployed: silence here would read as "the rules are
		// current" in the one run that could have changed them.
		return initflow.Skipped, fmt.Sprintf("the rules generation marker at %s could not be read (%v), so whether the deployed rules are the generation this Fugaro deploys is unknown", marker, skew.err)
	case rulesSkewUnreadableRules:
		return initflow.Skipped, rulesRulesUnreadableText(skew.err, marker)
	case rulesSkewUngeneratable:
		return initflow.Skipped, rulesRulesUngeneratableText(skew.err)
	}
	// Every kind is listed above, as doctorRulesSkew lists them, so that a kind
	// added to rulesSkewKind shows up here as a gap rather than as this report.
	// Go still wants a statement after the switch, and a kind outside the list
	// is the same state: nothing can be said about the deployed document.
	return initflow.Skipped, rulesRulesUngeneratableText(skew.err)
}

// read opens the installation's budget database and classifies the deployed
// rules and their marker, in the one order the classification is made in
// (rulesSkewOf): the marker first, and the document only where the marker
// records this generation.
func (s *rulesStage) read(ctx context.Context) (*infra.DB, rulesSkew, error) {
	_, cl, err := budgetDBFor(ctx, s.e.lc)
	if err != nil {
		return nil, rulesSkew{}, err
	}
	return infra.NewDB(cl, s.e.lc.Name, s.e.lc.GCPProject, ""), rulesSkewOf(ctx, cl), nil
}

// state is one read of the stage: the database to deploy into, the state the
// deployed rules are in, and the sentence the result carries.
//
// A deployable state is checked against the database's own gate first
// (infra.DB.Check, the first thing the database step of `--firebase-project`
// does): nothing may be written into a database that holds data and is not this
// installation's, or that carries another project's mark, and the sentence that
// refuses it on that path is the sentence here. A state that must not be
// deployed over at all is not checked — the question is not asked of a database
// this stage is not going to write to — and a database the gate refuses is
// REPORTED rather than fatal: `fugaro update`'s other stages have nothing to do
// with it, and the run that refused them all would be the one command that must
// work, not working.
//
// The config's own pair is checked before this, never after (rulesUnavailable):
// a database the URL names and a Firebase project that is not its own is a
// config no read of that database can settle, and the stage must not open it to
// find out.
func (s *rulesStage) state(ctx context.Context) (*infra.DB, initflow.State, string, error) {
	db, skew, err := s.read(ctx)
	if err != nil {
		return nil, initflow.Done, "", err
	}
	st, detail := rulesStageState(skew)
	if st != initflow.Todo {
		return db, st, detail, nil
	}
	if _, err := db.Check(ctx); err != nil {
		return db, initflow.Skipped, err.Error(), nil
	}
	return db, st, detail, nil
}

// show writes what a plan would write, in the shape the database step of
// `--firebase-project` prints it (init_firebase.go's deployDatabase): what the
// operator reads before confirming is the same account in both doors.
func (s *rulesStage) show(w io.Writer, acts []infra.DBAction) {
	fmt.Fprintf(w, "The security rules of the Firebase project %s:\n", s.e.lc.Budget.FirebaseProject)
	for _, a := range acts {
		fmt.Fprintf(w, "  %s: %s\n", a.Path, a.Text)
	}
}

// Check reports what the deployed rules and their marker are. A read that fails
// is the run's error (the stage cannot tell its state, and the stages after it
// depend on it): a refusal from the database itself, a credential among them,
// is what the operator has to fix.
func (s *rulesStage) Check(ctx context.Context) (initflow.Status, error) {
	if detail := rulesUnavailable(s.e.lc); detail != "" {
		return initflow.Status{State: initflow.Skipped, Detail: detail}, nil
	}
	_, st, detail, err := s.state(ctx)
	if err != nil {
		return initflow.Status{}, err
	}
	return initflow.Status{State: st, Detail: detail}, nil
}

func (s *rulesStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	if detail := rulesUnavailable(s.e.lc); detail != "" {
		return initflow.Plan{NothingToDo: true, Detail: detail}, nil
	}
	db, st, detail, err := s.state(ctx)
	if err != nil {
		return initflow.Plan{}, err
	}
	if st != initflow.Todo {
		return initflow.Plan{NothingToDo: true, Detail: detail}, nil
	}
	acts, _, err := db.Plan(ctx)
	if err != nil {
		return initflow.Plan{}, initErr(err)
	}
	if len(acts) == 0 {
		return initflow.Plan{NothingToDo: true, Detail: detail}, nil
	}
	if env.Interactive || env.Yes {
		s.show(s.e.r.w, acts)
	}
	return initflow.Plan{Detail: fmt.Sprintf("deploys the security rules generation %d, and records it, after your confirmation", projectpolicy.RulesVersion)}, nil
}

// Apply is the deploy, behind the confirmation the database step takes: the
// plan is read again here (it is what is written), shown, and applied only once
// confirmed. Nothing is written when the state is not one this binary may
// deploy over, and nothing is written when the database is already as planned.
func (s *rulesStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if detail := rulesUnavailable(s.e.lc); detail != "" {
		return initflow.Outcome{Detail: detail}, nil
	}
	db, st, detail, err := s.state(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if st != initflow.Todo {
		return initflow.Outcome{Detail: detail}, nil
	}
	acts, warnings, err := db.Plan(ctx)
	if err != nil {
		return initflow.Outcome{}, initErr(err)
	}
	for _, w := range warnings {
		s.e.r.warn(w)
	}
	if len(acts) == 0 {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	s.show(s.e.r.w, acts)
	if err := s.e.r.confirmOrdinary(
		"deploys the security rules listed, as you (project owner, editor or budget admin), and records which generation is deployed: they are what every project policy is read under",
		"the database was not written", s.e.r.firebaseReason()); err != nil {
		return initflow.Outcome{}, err
	}
	if err := db.Apply(ctx); err != nil {
		return initflow.Outcome{}, initErr(err)
	}
	// The policy stage ran its check in the read-only preview pass, before this
	// deploy, and its write is gated on the marker this apply just wrote: what
	// it read there has to go, or the run refuses its own work with a sentence
	// about rules that are no longer deployed.
	s.e.forgetPolicyMigration()
	return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("deployed the security rules (generation %d) and recorded it", projectpolicy.RulesVersion)}, nil
}

// Verify reads the deployed document and the marker back: a rule deploy that
// claims a generation it did not land is one the doctor keeps reporting and
// every later run would deploy again. Its sentence does not say the run
// deployed, because Apply writes nothing when the state moves under it between
// its read and its plan, and the run it is failing wrote nothing to be blamed
// for.
func (s *rulesStage) Verify(ctx context.Context) error {
	if detail := rulesUnavailable(s.e.lc); detail != "" {
		return nil
	}
	_, skew, err := s.read(ctx)
	if err != nil {
		return err
	}
	if st, detail := rulesStageState(skew); st != initflow.Done {
		return fmt.Errorf("the security rules generation %d is not deployed: the read-back says %s", projectpolicy.RulesVersion, detail)
	}
	return nil
}
