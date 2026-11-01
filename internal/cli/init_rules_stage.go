package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/dimipaun/fugaro/internal/budget"
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
//
// A Firebase project other than the installation's own is the one database this
// stage may not write in on the strength of the config alone: a name in a config
// is a name someone typed, and --firebase-project takes only the installation's
// own GCP project (checkFirebaseProject, the owner's ruling of 2026-10-23), so
// there is no door here that adopts a project the config names. A config naming
// another project whose database does not carry this installation's mark — an
// EMPTY database included, which infra.DB.Check passes — is skipped, with the
// way out: the config's pair corrected, and the installation's own Firebase
// project adopted (unadopted). A typo that agrees with itself would otherwise
// mark and deploy into a database that is not the installation's (security
// review finding F3).

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
//
// Both sentences print the flag's own value (firebaseProjectIDWord) and not the
// bare flag: --firebase-project takes the installation's GCP project id, so a
// remedy that stops at the name of the flag is a command nobody can run.
func rulesNoBudget(lc *localcfg.Config) string {
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" {
		return fmt.Sprintf("the project config records no shared Firebase budget backend (budget.firebase_project): fugaro update --firebase-project %s adopts one, and the security rules are deployed with it", firebaseProjectIDWord(lc.GCPProject))
	}
	if lc.Budget.RTDBURL == "" {
		return fmt.Sprintf("the project config records a Firebase project (budget.firebase_project) and no database URL (budget.rtdb_url), so there is no deployed rules generation to read: fugaro update --firebase-project %s records both", firebaseProjectIDWord(lc.GCPProject))
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
	return fmt.Sprintf("the config's budget.rtdb_url (%s) is not the database of the Firebase project its budget.firebase_project names (%s): a Firebase project's own database is <project>-default-rtdb, so one of the two is wrong and nothing here was read or written; fugaro update --firebase-project %s records the pair together",
		oneLine(lc.Budget.RTDBURL), oneLine(lc.Budget.FirebaseProject), firebaseProjectIDWord(lc.GCPProject))
}

// rulesSeparateFirebaseProject is why the config's budget.firebase_project is a
// project other than the installation's own GCP project, "" when it is (or when
// there is none). It is the predicate internal/cli/sharedcfg.go's reader refuses
// a shared config with, and here it says only which project the database is in:
// that the installation is entitled to write it is the mark's answer (unadopted),
// because a name in a config is a name someone typed.
func rulesSeparateFirebaseProject(lc *localcfg.Config) string {
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" || lc.Budget.FirebaseProject == lc.GCPProject {
		return ""
	}
	return fmt.Sprintf("the config's budget.firebase_project (%s) is not this installation's GCP project (%s)",
		oneLine(lc.Budget.FirebaseProject), oneLine(lc.GCPProject))
}

// budgetDBUnadopted is why the database the config names may not be treated as
// this installation's, "" when it may. It is the ONE ownership question every
// writer into the budget database asks, and it is one function so that they
// cannot drift apart: the rules stage (state), the policy migration of `fugaro
// update` (init_policy_migrate.go's policyMigrationFor) and the policy step of
// `fugaro upgrade` (which reaches the migration and nothing else), plus the
// history job's rollover, which asks it of the environment Terraform gives it
// (history_rollover.go's historyJobConfig).
//
// It is asked in two halves, in this order. The config's own facts come first
// (rulesUnavailable): no Firebase project recorded at all, or a database URL
// that is not the Firebase project's own, are facts no read of the database can
// settle, and the stage that asks them must not open it to find out. Then the
// database's mark (infra.DB.Check), ALWAYS and for every shape — not only for a
// config naming a Firebase project other than the installation's own, which is
// the hole the second review found: a config recording no Firebase project, and
// one recording the installation's own project with a URL pointing at the
// database beside it, both made the rules stage skip while the policy migration
// behind it read the node, took its ETag and rewrote it in place. Every config
// the pair refuses and every database Check refuses is now the same answer for
// every writer.
//
// The order matters in both directions. The pair is asked before the mark
// because a URL that is not the project's is a URL nothing here may contact, and
// the mark is asked before the count of what would be written because a database
// that is not ours is not ours whatever is in it.
//
// infra.DB.Check's three answers are three states, and each is passed on as it
// is: an error (another installation's mark, or data and no mark) is reported
// verbatim — it names the mark it found — and marked is the licence. An EMPTY
// database that carries no mark is the case that made this a finding: it passes
// Check whatever project the URL belongs to, so with a separate project named it
// is a skip and not a licence, and the skip names the way out. With no separate
// project named there is nothing to adopt — the installation's own Firebase
// project's database is its own — and the empty database is passed, which is
// what every first run on a fresh backend is.
//
// declined names what the caller would have written there and program the
// command the way out is run with, so one sentence serves every door. declined
// ends the sentence on every path, the two halves of the config and the
// database's own refusals alike: what was NOT done is the caller's fact and not
// the database's, and the operator reading a skipped stage or a refused job has
// to know nothing was written.
func budgetDBUnadopted(ctx context.Context, lc *localcfg.Config, db *infra.DB, declined, program string) string {
	why, _ := budgetDBUnadoptedErr(ctx, lc, db, declined, program)
	return why
}

// budgetDBUnadoptedErr is budgetDBUnadopted with the database's own error kept
// beside the sentence. There is one implementation and not two — the string form
// above is this function with the error dropped — and the error is kept because
// a CLI command has to turn the answer into an exit code: Check refusing (a mark
// that is not ours, data and no mark) is the user's to fix and exits 1, while
// Check failing to reach the database is an outage and exits 2, and a rendered
// sentence cannot carry that difference.
func budgetDBUnadoptedErr(ctx context.Context, lc *localcfg.Config, db *infra.DB, declined, program string) (string, error) {
	if detail := rulesUnavailable(lc); detail != "" {
		return detail, nil
	}
	marked, err := db.Check(ctx)
	if err != nil {
		return fmt.Sprintf("%v: %s", oneLine(err.Error()), declined), err
	}
	if marked {
		return "", nil
	}
	detail := rulesSeparateFirebaseProject(lc)
	if detail == "" {
		return "", nil
	}
	return fmt.Sprintf("%s: nothing proves the database at %s is this installation's — an empty database passes every check that asks it, and the mark (%s) is the one proof there is — so it is not this installation's database, and %s. Drop the config's budget.firebase_project (with the budget.rtdb_url beside it, which is that project's database) and run `%s --firebase-project %s` once to adopt the installation's own Firebase project: the mark that door writes is what lets plain %s converge there from then on",
		detail, oneLine(lc.Budget.RTDBURL), budget.PathMark, declined, program, firebaseProjectIDWord(lc.GCPProject), program), nil
}

// unadopted is budgetDBUnadopted for this stage: it is the stage that neither
// marks the database nor deploys the rules into it when the answer is "".
func (s *rulesStage) unadopted(ctx context.Context, db *infra.DB) string {
	return budgetDBUnadopted(ctx, s.e.lc, db, "this stage neither marks nor writes there", s.e.r.o.doorProgram())
}

// rulesUnavailable is the config's own half of the ownership predicate
// (budgetDBUnadopted): why the stage does not run, "" when it may. No shared
// budget backend at all, or one whose database is not the Firebase project's
// own. Both are config facts, and both are reported rather than refused, because
// `fugaro update`'s other stages are what the owner came for. The other half —
// a database the installation may not write — needs the database opened and is
// asked there, by the same predicate the writers ask it with.
//
// The stage's four entry points ask this half first, before the database is
// opened: a URL that is not the Firebase project's own is one nothing here may
// contact, so the refusal has to be reached without a client ever being built
// (rulesStageState's classification would otherwise read it).
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

// rulesRead opens the installation's budget database and classifies the
// deployed rules and their marker, in the one order the classification is made
// in (rulesSkewOf): the marker first, and the document only where the marker
// records this generation.
func rulesRead(ctx context.Context, lc *localcfg.Config) (*infra.DB, rulesSkew, error) {
	_, cl, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, rulesSkew{}, err
	}
	return infra.NewDB(cl, lc.Name, lc.GCPProject, ""), rulesSkewOf(ctx, cl), nil
}

// rulesState is one read of the deployed rules for every caller that acts on
// it: the database to deploy into, the state the deployed rules are in, and the
// sentence the result carries. The rules stage of `fugaro update` and the rules
// step of `fugaro upgrade` both go through it, so one installation cannot
// converge through one front door into a state the other refuses, and declined
// and program are budgetDBUnadopted's, so each door's skip names what THAT door
// did not do and the command its own operator can run.
//
// The ownership predicate is asked first, before the classification, and it is
// asked of EVERY state, not only of a deployable one (budgetDBUnadopted):
// whether the database is this installation's to write is a question about the
// database, and the state of the document in it cannot be an answer to it. A
// database the predicate refuses is REPORTED rather than fatal — `fugaro
// update`'s other stages have nothing to do with it, and the run that refused
// them all would be the one command that must work, not working.
//
// It is the same question, asked by the same function, that the policy
// migration behind this stage asks (init_policy_migrate.go): a database this
// caller skips cannot come out writable for the stage after it.
func rulesState(ctx context.Context, lc *localcfg.Config, declined, program string) (*infra.DB, initflow.State, string, error) {
	db, skew, err := rulesRead(ctx, lc)
	if err != nil {
		return nil, initflow.Done, "", err
	}
	// Before the classification, because it is about the database and not about
	// the deployed document: what a database that is not this installation's
	// carries says nothing about what this caller may write there.
	if why := budgetDBUnadopted(ctx, lc, db, declined, program); why != "" {
		return db, initflow.Skipped, why, nil
	}
	st, detail := rulesStageState(skew)
	return db, st, detail, nil
}

// rulesShow writes what a plan would write, in the shape the database step of
// `--firebase-project` prints it (init_firebase.go's deployDatabase): what the
// operator reads before confirming is the same account in both doors and in
// both commands.
func rulesShow(w io.Writer, project string, acts []infra.DBAction) {
	fmt.Fprintf(w, "The security rules of the Firebase project %s:\n", project)
	for _, a := range acts {
		fmt.Fprintf(w, "  %s: %s\n", a.Path, a.Text)
	}
}

func (s *rulesStage) read(ctx context.Context) (*infra.DB, rulesSkew, error) {
	return rulesRead(ctx, s.e.lc)
}

func (s *rulesStage) state(ctx context.Context) (*infra.DB, initflow.State, string, error) {
	return rulesState(ctx, s.e.lc, "this stage neither marks nor writes there", s.e.r.o.doorProgram())
}

func (s *rulesStage) show(w io.Writer, acts []infra.DBAction) {
	rulesShow(w, s.e.lc.Budget.FirebaseProject, acts)
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
	db, skew, err := s.read(ctx)
	if err != nil {
		return err
	}
	// A database the stage did not deploy into has nothing to read back: Apply
	// wrote nothing there, which is what the skip said. The state is re-derived
	// rather than remembered for the reason rulesUnavailable's arm above is: a
	// verify that failed over a config changed under a completed deploy would
	// blame the run for it.
	if s.unadopted(ctx, db) != "" {
		return nil
	}
	if st, detail := rulesStageState(skew); st != initflow.Done {
		return fmt.Errorf("the security rules generation %d is not deployed: the read-back says %s", projectpolicy.RulesVersion, detail)
	}
	return nil
}
