package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The workspace's own project's root, as a stage of the converge
// (docs/workspaces/implementation-plan.md W3).
//
// A workspace has a project of its own — the project the workspace is named
// after, whose config carries the owner's half of the membership (D1290,
// D1291) — and that project is an installation like any other: `fugaro setup
// --name <workspace>` created it, the installation, Firebase and rules stages
// converge it, and `fugaro update` publishes the workspace record into its
// database (update_workspace.go, W2). The one thing no other root creates there
// is the workspace's SERVICE: the account the workspace's own bundle runs as
// (W6), which reads the record at workspaces/<name>, keeps the admitted-token
// hashes (D1296) and meters the calls the workspace's credentials pay for
// (W7) — all of it over IAM, because the database's rules deny every client
// (internal/budget/rules/workspace.go).
//
// So this stage converges `deploy/terraform/gcp/roots/workspace`: the
// workspace's own service account, the one custom role that names what it
// reaches and the ONE grant that binds them, in a workdir of its own (infra.WorkspaceWorkdir, state
// fugaro/workspace) so the installation's saved plan is never gated and applied
// as this root's.
//
// It runs only where there IS a workspace, which is the predicate W2's publish
// makes and not a second reading of it: the project config carries the OWNER's
// half (workspace_members / workspace_cap), so the stage is skipped — never an
// error, and never a grant — on a member's project and on a plain installation.
// A member's project must not carry this account: a member holds no grant on
// the workspace's project, and the workspace holds none on the member (spec
// §5), and a stage that created one here would be the widening rule 1 forbids.
//
// The confirmation is this stage's own and it is TYPED: the apply is an IAM
// GRANT, and the cover never admits one — a grant is a loosening, and a
// loosening may not be covered by the run's one --yes, by a plan gate's
// verdict, or by a coding agent's session (spec §6, and the same answer W7's
// one grant on a member takes). A plan that is not exactly the account and its
// one grant is refused before it is applied (tf.WorkspaceNotCovered), which is
// this root's whole cover: the installation's tables deliberately do not admit
// these resources. The role is the database's two DATA permissions and no
// lifecycle permission — the account holds the workspace's record and the
// hashes of its admitted tokens, and it must not be able to destroy the
// database they live in.
//
// The SECOND half of the root is the workspace's credentials service (WS6,
// D2980, F4, F17, R9): the same image an installation's service runs, told what
// it is by its environment, running as the account above, reading the ONE bundle
// container this project holds and invoked by a member's own job accounts and by
// the roster's people — one invoke grant per account and per person, which is
// why the root's variables carry the owner's member table and the workspace's
// own roster's people (R6, D2971). So this stage passes three values no plan and
// no installation output carries: the member table, from this machine's config
// (the owner's own member table W2 publishes, read here for the grants), the
// roster's people, read from the workspace's own roster node (people), and the
// service's own block. The image in that block is the reference this run's resolve answered
// (credentialsResolve) — the same digest-pinned pin an installation's service is
// deployed with, and the same owner the cover holds the plan's service to — and
// the URL in it is the one Cloud Run assigns, read back from this root's own
// output in a second apply inside this stage (fillServiceURL), because the apply
// that creates a service cannot know its own uri.
//
// A run that deploys no service — a coding agent's session, a development build,
// a resolve that failed, a config that names no database — writes the block the
// workdir already carries, VERBATIM, and says why in one line. That is not
// tidiness: the variable's default is null and null plans the DESTRUCTION of the
// running service, its accessor and every member's invoke grant, which the cover
// refuses (a member's grant is the one deletion it admits, F25) — so the plan a
// session reads would be a refusal, and the state it would leave is one nobody
// decided. An installation whose block is already this release's image writes it
// back unchanged and has no revision to move.
type workspaceProjectStage struct {
	e       *initEngine
	t       *tf.TF
	wd      *infra.Workdir
	dir     string // the root's own directory, for the sentences that name where to read a plan
	applied bool
	// svc is var.credentials_service as this run writes it, nil when the root's
	// tfvars carry none — ONE decision, read once (serviceBlock) because prepare
	// runs on every path of the stage and the line a run that deploys nothing
	// prints is one line.
	svc     *workspaceServiceVar
	svcRead bool
	// peopleList is var.people as this run writes it — the workspace roster's
	// human addresses — read once for the same reason, and the zero value is
	// never the answer: people decides the empty list itself and memoizes that
	// (peopleRead).
	peopleList []string
	peopleRead bool
}

func newWorkspaceProjectStage(e *initEngine) *workspaceProjectStage {
	return &workspaceProjectStage{e: e}
}

func (workspaceProjectStage) Name() string { return initflow.WorkspaceProject }

// SelfConfirming is what lets the loop look at this stage in a run that cannot
// confirm anything at all: the stage shows its own plan and takes its own typed
// word, so the loop plans it read-only rather than leaving it for the user
// unseen (initflow's own definition of the marker). An EMPTY plan is the stage
// being done — the account and its one grant are already there — and the run
// goes on to the record stage after it; anything else is a step only the owner
// can take, in the terminal they are not at, which is the loop's own refusal.
//
// Every other engine stage that asks its own confirmation declares it (the
// images, rules, policy, layer and record stages), and on a workspace's own
// project it is what keeps a coding agent's session from stopping here with
// nothing to do: without it the record the run publishes behind this stage
// would never be published by that session.
func (workspaceProjectStage) SelfConfirming() {}

// Left is the step this stage leaves when its own typed word cannot be given,
// and the door decides which command that is (workspaceConfirmLeft).
func (s *workspaceProjectStage) Left() initflow.Left {
	if s.e.r.o.door == applyName {
		return workspaceConfirmLeft()
	}
	return promptLeft(initflow.WorkspaceProject, "type the project's name at the workspace's confirmation", s.e.r.o.doorName())
}

// workspaceConfirmLeft is the left of a run that cannot ask this stage's
// question: the command that can, in the shape the repository gate's own left
// uses (enrolLeft).
//
// The question is a name typed at a prompt and `fugaro apply` runs with no
// terminal of its own by construction — its run is nonInteractive, so the
// stage's askTyped answers "not reachable" rather than waiting, and the run
// ends needing the person. A left naming `fugaro apply` there would send the
// reader back to the command that just said it: the answer would be the
// question again, for ever, which is the no-prompt rule's own failure (a
// refusal names the fix that reaches it, and this is not one). `fugaro update`
// in the owner's own terminal is the door that reaches this stage's plan and
// asks.
func workspaceConfirmLeft() initflow.Left {
	cmd := selfCommand() + " " + updateDoor.name
	return initflow.Left{Stage: initflow.WorkspaceProject, Kind: initflow.LeftCommand,
		Text: cmd, Commands: []string{cmd}}
}

// workspaceAccount is the address of the workspace's own service account: the
// identity this stage creates. It is derived and not read back — the same
// string the root's own ${google_service_account.workspace.email} resolves to —
// so the sentence the operator confirms names the identity before anything is
// created.
func workspaceAccount(project string) string {
	return tf.WorkspaceServiceAccountID + "@" + project + ".iam.gserviceaccount.com"
}

// workspaceIAMAccount is the address of the SA fallback's IAM account: the
// SECOND account this stage's root creates (WU3 of the workspace-projects-users
// plan, D2803), which Terraform grants nothing and which a member's owner later
// gives roles/owner on their own project (WU20). It is named wherever the stage
// says what it creates, because on an installation that is already converged —
// the service account and its grant are in the cloud — this account is the ONLY
// change the plan carries: a sentence naming the service account alone asks the
// owner to type their project's name for a change it never described, and the
// account it leaves out is the one that ends up holding authority over every
// linked member.
func workspaceIAMAccount(project string) string {
	return tf.WorkspaceIAMAccountID + "@" + project + ".iam.gserviceaccount.com"
}

// declared is whether this installation is a workspace's own, and where it is
// not, why — the detail the stage answers Skipped with.
//
// The config carries the owner's half of the membership (workspaceDeclared,
// which is workspaceRecordOf's own predicate — W2's publish stage and the
// Identity Platform carve-out are built on the same one).
//
// A config that declares a workspace and lists NO member is this project's
// workspace too, and the stage converges it: the record admits nobody and W2
// publishes nothing (workspaceNoMemberReason — a state the owner's own
// `fugaro workspace cap set` and `member rm` reach), but the project is still
// the one the workspace is named after and its own service account is what
// `fugaro update` builds here. Reading that state as "no workspace" would leave
// the account missing until a member was added, and reading it as an ERROR
// would stop the whole run — a Check that answers an error blocks every stage
// after it, which is the HARD RULE (update_workspace.go's own note beside
// workspaceNoMemberReason).
//
// Everything else workspaceRecordOf refuses IS an error and is returned: a
// member table that is not a project name, a cap that is not money, a record
// that does not validate — the config's own problems, which the run must stop
// on rather than converge a root from a half-read config. The record is read
// and not just the predicate, so that check happens before anything is planned.
//
// A workspace's own project whose Firebase is ANOTHER project's is a SKIP for
// the same reason and not an error (workspaceSplitFirebase): the one grant this
// root makes is the workspace's own service's whole reach over the workspace's
// database, that database is the workspace's own project's, and on a split
// installation the grant would bind on a database the workspace does not use.
// Nothing is planned, no service account is created and no grant is made — but
// the run is not stopped, because the record stage, the layer and the checkout
// behind this one are still the upgrade the owner ran `fugaro update` for, and
// one command is the whole of it (the HARD RULE). An installation in this state
// has been updating without this stage all along — the stage is this release's,
// and W2's publish (v0.21.0, the workspace command is public) carries no such
// check — so failing the run here would take a working `fugaro update` away
// from it and leave a hand-edited config as the way back. The reason the
// operator reads is the skip's own detail, and `fugaro doctor` carries the same
// state as a check of its own (doctorWorkspaceSplitCheck), so the owner meets it
// on doctor's day rather than only in one run's output.
//
// The second return is what the caller answers Skipped with and the third
// whether that skip left this stage's own work undone — the fact that tells a
// plan row from a step a machine still owes (initflow.Status.Undone, read by
// internal/cli's applyAnswered). The split installation's skip is the one that
// leaves it: this IS a workspace's own project, and the account and the grant
// this stage converges are the whole of what its own project is for, so a step
// run for it stays owed. A config that declares no workspace is nothing to do
// instead: there is no account to create and no grant to make here at all.
func (s *workspaceProjectStage) declared() (bool, string, bool, error) {
	if !workspaceDeclared(s.e.lc) {
		return false, workspaceProjectSkip(s.e.lc), false, nil
	}
	if why := workspaceSplitFirebase(s.e.lc); why != "" {
		return false, why, true, nil
	}
	if _, err := workspaceRecordOf(s.e.lc); err != nil {
		var notOurs *workspacePublishNotOurs
		if !errors.As(err, &notOurs) {
			return false, "", false, err
		}
	}
	return true, "", false, nil
}

// workspaceSplitFirebase is why a workspace's own installation cannot converge
// its root: the config records a Firebase project that is not its GCP project
// (budget.firebase_project, the key init_firebase.go writes when the two are
// not the same), so the workspace's database is in one project and the project
// the workspace is named after — the one whose service account, role and single
// grant this root creates — is another.
//
// The workspace's own project holds the workspace's Firebase, its database and
// its service (spec §3 and D1290: the workspace's own Firebase project is
// created for the workspace and holds nothing else; the root's own
// variables.tf says the same of `project`), and that is what makes the root's
// one grant the account's whole reach. On a split installation the grant would
// bind on a database the workspace does not use and reach the workspace's own
// database not at all — the record W2's publish stage writes and the workspace's
// own service reads lives in the FIREBASE project's database — while the plan
// check accepts it, because the stage holds the plan to the same wrong project
// (tf.WorkspaceNotCovered reads the one project it is given).
//
// The workspace's credentials service is the same answer, one step further
// (R12). It reads the database of the project it runs in at start-up and serves
// the record of the workspace it is told it is, so a revision deployed here
// would read a database the workspace does not use and reach the workspace's own
// not at all: no revision is deployed, and the skip's sentence says so in the
// same breath as the account and the grant.
//
// Converged against the Firebase project's name it is not, either: the account
// and the role are the identity of a service that runs in the workspace's own
// project (W6), so a second project here would be a cross-project grant the root
// does not carry, tf.WorkspaceNotCovered refuses by name and this design has no
// shape for — and it would be a second answer to what a workspace's own project
// is. The stage skips with this reason and the rest of the run goes on
// (declared): the remedy is the one every split installation takes, which
// `fugaro doctor` says beside the same command (adoptOwnFirebaseProject).
func workspaceSplitFirebase(lc *localcfg.Config) string {
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" || lc.Budget.FirebaseProject == lc.GCPProject {
		return ""
	}
	return fmt.Sprintf("this installation declares a workspace of its own, and its Firebase is another project's: %s's config records budget.firebase_project %s, so the workspace's database is %s's while the workspace's own project — the project the workspace is named after, whose own service account and one grant this root creates — is %s. A workspace's own project holds the workspace's Firebase, its database and its service (docs/workspaces/spec.md §3), and that is what makes the root's single grant the account's whole reach: applied here it would put the workspace's own service account on a database the workspace does not use and on the workspace's own database not at all. The two cannot be split, so the workspace's own project is left alone here — no service account is created, no grant is made, no credentials service is deployed, and the rest of `fugaro update` runs as usual — and the way out is to make this project its own Firebase project: drop budget.firebase_project and budget.rtdb_url from %s's config, then run `fugaro update --firebase-project %s`, which adopts it", lc.Name, lc.Budget.FirebaseProject, lc.Budget.FirebaseProject, lc.GCPProject, lc.Name, lc.GCPProject)
}

// Check reads the root's plan, because the plan is the only thing that knows
// whether the account and the grant are there: an empty one is the stage being
// DONE — the workspace's own service is already what the root says it is — and
// only a plan with changes in it is a step somebody has to confirm. Saying "I
// would create the account" without looking is what every run that does not
// reach an apply would answer with, including the converged one: a coding
// agent's session and a `--json` run would stop at this stage for the owner's
// word they do not need, and the record stage behind it would never run.
//
// Empty is the stage being done only where the block the run writes carries its
// audience (audienceMissing): the plan cannot see a block with no url, so the one
// state the check has to answer Todo about on an empty plan is that one, and the
// write it leads to is Apply's own (audienceTodo).
//
// It does NOT hold the plan to this root's cover: the cover belongs to the two
// paths that lead to an apply (Plan and Apply), which is where a plan that is
// not this root's is refused by name. A read that fails is not this stage's
// answer either — the run has not been told there is something to do, and it
// has not been told there is not: it is a Todo, and the read is made again on
// the apply path, whose own failure is the run's.
//
// A config that declares no workspace is a SKIP with the reason, never a
// failure, and it reaches no terraform at all: it is every installation that is
// not a workspace's own, which is most of them. A workspace's own project whose
// Firebase is another project's is the other skip (workspaceSplitFirebase, in
// declared): this stage converges nothing there, and the stages behind it still
// run — but nothing it was run for was done, so a step that asked for this stage
// stays owed (declared's third return, initflow.Status.Undone).
func (s *workspaceProjectStage) Check(ctx context.Context) (initflow.Status, error) {
	ok, why, undone, err := s.declared()
	if err != nil {
		return initflow.Status{}, userErr("%v", err)
	}
	if !ok {
		return initflow.Status{State: initflow.Skipped, Detail: why, Undone: undone}, nil
	}
	plan, _, err := s.readPlan(ctx, false)
	if err == nil && plan == nil {
		// An empty plan is the stage being done only where the block this run
		// writes carries the audience the service is served with: a block with
		// no url is a revision behind the module's unassigned placeholder,
		// which terraform sees nothing to change about, so a Done here would
		// report a service deployed that admits no member and no later update
		// would ever look again (audienceMissing).
		if s.audienceMissing() {
			return initflow.Status{State: initflow.Todo, Detail: s.audienceTodo()}, nil
		}
		project := s.e.lc.GCPProject
		return initflow.Status{State: initflow.Done, Detail: s.converged(project)}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: s.todo(s.people(ctx))}, nil
}

// audienceMissing is whether the block this run writes carries no url: the state
// a run leaves when its second apply never landed — a create that applied and
// then failed before the URL was written back (fillServiceURL), a `--plan-only`
// pass — and the one terraform cannot see. The URL is an environment value the
// module falls back to its own unassigned placeholder for, so a plan over that
// document finds the service converged while the revision serving it admits no
// caller, and every read that decided by the plan alone would report this stage
// done for ever.
//
// It is a question about the block and not about this run: a block this run
// resolves carries the audience a previous run read back, and the carry branch
// hands back the recorded block exactly as it stands, so what this returns is
// the document the root is planned with.
func (s *workspaceProjectStage) audienceMissing() bool {
	return s.svc != nil && s.svc.URL == ""
}

// audienceTodo is what the stage says where the root plans nothing and the block
// it writes carries no audience: the ONE write such a run makes, named as the
// write it is rather than as the accounts a root with changes creates (todo),
// because the sentence the owner reads before typing their name has to be about
// what the run is going to do.
func (s *workspaceProjectStage) audienceTodo() string {
	return fmt.Sprintf("the workspace's own credentials service %s is deployed on the module's unassigned audience, which admits no caller: this run reads the URL Cloud Run assigned it from the root's own output and writes that back as the audience a member's run mints its Google ID token for", s.svc.Service)
}

// converged is what a root that plans nothing and whose block carries the
// audience the service is served with says: the account and its one grant, and
// the workspace's own service where the root's tfvars carry one. The service is
// named only when this run's document holds a block, because a workspace whose
// config names no database has none to deploy and a sentence claiming one would
// tell the operator a process is serving when nothing is — and a block with no
// url never reaches this sentence at all (audienceMissing).
func (s *workspaceProjectStage) converged(project string) string {
	out := fmt.Sprintf("the workspace's own service account %s holds %s on %s", workspaceAccount(project), tf.WorkspaceDatabaseRole(project), project)
	if s.svc != nil {
		out += fmt.Sprintf(", and its own credentials service %s is deployed on this release's image", s.svc.Service)
	}
	return out
}

// todo is what the stage says when it has something to do, in Check and in the
// plan view: the root's two accounts, the one grant and — when this run's own
// document carries them — the workspace's credentials service and the invoke
// grant each of the owner's member job accounts and each of the roster's people
// gets, with the root's own name for what reaches what. people is the list the
// run has already read (people), passed rather than read again so the sentence
// cannot disagree with the document it describes.
func (s *workspaceProjectStage) todo(people []string) string {
	lc := s.e.lc
	out := fmt.Sprintf("the workspace root's two accounts (the workspace's own service account %s and the SA fallback's IAM account %s) and the one grant %s on %s",
		workspaceAccount(lc.GCPProject), workspaceIAMAccount(lc.GCPProject), tf.WorkspaceDatabaseRole(lc.GCPProject), lc.GCPProject)
	if s.svc != nil {
		out += fmt.Sprintf(", its own credentials service %s on this release's image", s.svc.Service)
	}
	if n := len(workspaceMemberAccounts(lc)); n > 0 {
		out += ", and one invoke grant per member job account"
	}
	if n := len(people); n > 0 {
		out += ", and one per person of the workspace's roster"
	}
	return out + " are planned when applied"
}

// workspaceProjectSkip is why this installation has no workspace's own project
// to converge, in the words the operator reads: the owner's half is what makes
// an installation a workspace's, and its absence is a member's project or a
// plain one — the two cases where no account may be created here. The two are
// said differently because the operator's remedy differs: a plain project has
// nothing to read, and a member's file that also carries the owner's half — what
// this machine's own `cap set --workspace` and `member add` write into a
// member's config — is a file whose two halves contradict each other, and what
// they have to delete is the half they did not mean (workspaceDeclared).
func workspaceProjectSkip(lc *localcfg.Config) string {
	if workspaceMember(lc) {
		return fmt.Sprintf("this installation is a member of a workspace and not a workspace's own: %s's project config carries workspace and workspace_pin, and the member's half decides even where workspace_members or workspace_cap is written beside it, so there is no workspace service account to create and no database grant to make here (a member's project holds no grant on the workspace's project, spec §5)", lc.Name)
	}
	return fmt.Sprintf("this installation declares no workspace of its own: %s's project config carries neither workspace_members nor workspace_cap, so there is no workspace service account to create and no database grant to make here (a member's project holds no grant on the workspace's project, spec §5)", lc.Name)
}

// Plan shows the real plan and guards it, and changes nothing.
//
// A root that plans nothing is a NothingToDo plan only where the block it writes
// carries its audience: where it carries none the run has the ONE write to make
// (audienceTodo), and a plan view that answered "No changes" there would be this
// stage's half of the "converged" the check may not say — a coding agent's
// session reads NothingToDo as the stage being done and would leave the
// workspace unreachable with nothing on the screen (loop's agentStage).
func (s *workspaceProjectStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	ok, why, _, err := s.declared()
	if err != nil {
		return initflow.Plan{}, userErr("%v", err)
	}
	if !ok {
		return initflow.Plan{NothingToDo: true, Detail: why}, nil
	}
	plan, counts, err := s.readPlan(ctx, true)
	if err != nil {
		return initflow.Plan{}, err
	}
	if plan != nil {
		fmt.Fprint(s.e.r.w, tf.Summary(plan))
	}
	if plan == nil && s.audienceMissing() {
		return initflow.Plan{Detail: s.audienceTodo()}, nil
	}
	return s.e.planned(counts), nil
}

// Apply is the write, and a coding agent's session never reaches it: the typed
// confirmation cannot be given there (stepConfirmation), and what it would
// confirm is an access grant.
//
// A root that plans nothing is not "nothing to do" here: it is the one state
// whose write the plan cannot show (audienceMissing), and the run that finds it
// is the run that heals it. Where there is a plan the screen carries its summary
// and the word the owner types is the one above; where there is not, the state
// itself is what the screen carries and the word is the audience sentence
// (audienceTodo).
//
// Every state that is not a workspace's own answers its own reason and applies
// nothing, which for the split installation is the same answer Check gives: a
// run whose Check skipped is never planned or applied, so the branch is the
// answer of a stage nothing should have asked — and if something ever does ask,
// no grant is made on the wrong project.
func (s *workspaceProjectStage) Apply(ctx context.Context, _ initflow.Env) (initflow.Outcome, error) {
	ok, why, _, err := s.declared()
	if err != nil {
		return initflow.Outcome{}, userErr("%v", err)
	}
	if !ok {
		return initflow.Outcome{Detail: why}, nil
	}
	plan, counts, err := s.readPlan(ctx, true)
	if err != nil {
		return initflow.Outcome{}, err
	}
	project := s.e.lc.GCPProject
	account := workspaceAccount(project)
	iamAccount := workspaceIAMAccount(project)
	// The service clause of the two sentences below, and only where this run's
	// own document carries a block: a workspace whose config names no database
	// deploys none, and a sentence naming a service that was not deployed is the
	// one thing a confirmation may not do.
	//
	// It names every grant the root binds on that service, because the sentence
	// is what the owner types their name against: the people of the workspace's
	// roster are read from the same call the plan's own document was written from
	// (people, memoized by the guard above), so the confirmation cannot name
	// grants the plan does not make.
	service := ""
	if s.svc != nil {
		service = fmt.Sprintf(", and the workspace's own credentials service %s running as that account on this release's verified image, with one invoke grant on it per member job account", s.svc.Service)
		if len(s.people(ctx)) > 0 {
			service += " and one per person of the workspace's roster"
		}
	}
	if plan == nil && !s.audienceMissing() {
		fmt.Fprintf(s.e.r.w, "No changes: the workspace's own service account %s holds %s on %s.\n", account, tf.WorkspaceDatabaseRole(project), project)
		return initflow.Outcome{Detail: "the workspace's own service account, its one grant and its own credentials service are already there"}, nil
	}
	// The word the owner gives is the state's: a root with changes creates what
	// the plan says, and a root that plans nothing while its block carries no
	// audience makes the ONE write the plan cannot show (audienceTodo) — a
	// sentence about accounts nobody is creating would be a sentence about a run
	// that is not this one.
	what := fmt.Sprintf("creates the workspace's own service account %s in GCP project %s and grants it %s on that project's database (%s), and creates the SA fallback's IAM account %s, which this root grants nothing: the service account is the one identity that reaches the workspace's database, because the database's rules deny every client — and it is a GRANT, so no flag confirms it%s; %s)", account, project, tf.WorkspaceDatabaseRole(project), counts.String(), iamAccount, service, initflow.AgentAdvice)
	if plan != nil {
		fmt.Fprint(s.e.r.w, tf.Summary(plan))
	} else {
		// A plan that is already empty has no summary to show, and the state it is
		// empty in is the one thing the screen has to carry.
		fmt.Fprintf(s.e.r.w, "%s.\n", s.audienceTodo())
		what = fmt.Sprintf("assigns the workspace's own credentials service %s the URL Cloud Run assigned it as the audience a member's run mints its Google ID token for: the revision serving now carries the module's unassigned audience, which admits no caller, and this root has nothing else to change; %s)", s.svc.Service, initflow.AgentAdvice)
	}
	confirmed, reachable, err := s.e.r.askTyped(what)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if !reachable {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	if !confirmed {
		return initflow.Outcome{}, s.e.r.notConfirmed("nothing was applied")
	}
	if plan != nil {
		if err := s.t.Apply(ctx, infra.PlanFile); err != nil {
			return initflow.Outcome{}, remote(err)
		}
		fmt.Fprintf(s.e.r.w, "created the workspace root's two accounts and its one grant: the workspace's own service account %s, the SA fallback's IAM account %s, and %s on %s%s\n", account, iamAccount, tf.WorkspaceDatabaseRole(project), project, createdService(s.svc))
	}
	s.applied = true
	s.e.r.res.Applied = true
	// The second apply is on the STATE this run leaves and not on this run's own
	// resolve: a block written with no url — a create the apply never made, a
	// `--plan-only` pass — is a revision whose audience is the module's
	// unassigned placeholder, which admits no member, and the update that finds
	// the block is the run that heals it rather than the release after it
	// (fillServiceURL, the same rule as fillCredentialsAudience one root over).
	// A block that already carries the root's own audience writes nothing.
	if s.svc != nil {
		if err := s.fillServiceURL(ctx); err != nil {
			return initflow.Outcome{}, err
		}
	}
	detail := fmt.Sprintf("applied: %s (the workspace's own service account %s and the SA fallback's IAM account %s)", counts.String(), account, iamAccount)
	if plan == nil {
		detail = fmt.Sprintf("assigned the workspace's own credentials service %s the URL Cloud Run gave it as the audience a member's run mints its Google ID token for", s.svc.Service)
	}
	return initflow.Outcome{Changed: true, Detail: detail}, nil
}

// createdService is the clause the success line adds when the apply wrote the
// workspace's own service — empty when it wrote none, which is every state of a
// workspace whose config names no database.
func createdService(svc *workspaceServiceVar) string {
	if svc == nil {
		return ""
	}
	return fmt.Sprintf(", and its own credentials service %s", svc.Service)
}

// Verify re-reads what the apply claims: a fresh plan of the same root over the
// cloud the apply just touched, which must find nothing left to do. It runs
// only for a run that applied, so an installation that was already converged
// pays no second plan for it.
func (s *workspaceProjectStage) Verify(ctx context.Context) error {
	if !s.applied {
		return nil
	}
	changed, err := s.t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return remote(err)
	}
	if changed {
		return userErr("the workspace root still has changes after it was applied: read the plan with terraform in %s and apply it there, or run %s again", s.dir, selfCommand()+" "+s.e.r.o.doorName())
	}
	return nil
}

// readPlan prepares the root and reads its plan: nil when there is nothing to
// do, and the plan and its counts when there is. guard is whether the plan is
// held to this root's cover — the two callers that lead to an apply hold it,
// and Check reads the same plan without the cover (its own comment says why).
func (s *workspaceProjectStage) readPlan(ctx context.Context, guard bool) (*tf.Plan, *infra.PlanCounts, error) {
	if err := s.prepare(ctx); err != nil {
		return nil, nil, err
	}
	return s.planRoot(ctx, guard)
}

// planRoot reads the plan of the root this stage has already prepared, which is
// what the second apply of the same stage needs: fillServiceURL rewrites the
// tfvars with the URL it read back and plans THAT document, in the same workdir
// and with no second init (fillCredentialsAudience's own shape, one root over).
func (s *workspaceProjectStage) planRoot(ctx context.Context, guard bool) (*tf.Plan, *infra.PlanCounts, error) {
	changed, err := s.t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return nil, nil, remote(err)
	}
	if !changed {
		counts := infra.PlanCounts{}
		return nil, &counts, nil
	}
	plan, err := s.t.Show(ctx, infra.PlanFile)
	if err != nil {
		return nil, nil, remote(err)
	}
	if guard {
		if err := s.guard(ctx, plan); err != nil {
			return nil, nil, err
		}
	}
	counts := infra.CountPlan(plan)
	return plan, &counts, nil
}

// guard is this root's cover: the plan is the workspace's own role, its two
// accounts (its service account and the SA fallback's IAM account), that
// service account's one grant on the workspace's database, the
// workspace's own credentials service, its ONE bundle accessor, its members'
// invoke grants and the invoke grant of each of the roster's people (R6, D2971),
// and nothing else. tf.WorkspaceNotCovered answers what of the plan is not, so
// the message can name it rather than say "refused".
//
// Its hand-written arguments are the half of the plan a reader outside it has
// to supply, because none of them is a value any plan carries: the workspace's
// own bundle container is DERIVED from the workspace's name
// (credsvc.WorkspaceBundleSecretID — the same container this project's own
// installation already holds every model credential in), the members' job
// accounts are the owner's member table, which is this machine's config and not
// terraform's, the roster's people are the workspace's own roster node, read
// here (people) and not a value any plan holds, and the image identity the
// service is held to is the recorded owner's released pin (workspaceImageCover).
// A rule that read any of them out of the plan would accept whatever the plan
// named.
func (s *workspaceProjectStage) guard(ctx context.Context, plan *tf.Plan) error {
	cover := workspaceImageCover(s.e.lc)
	project := s.e.lc.GCPProject
	why := cover.WorkspaceNotCovered(plan, project, tf.WorkspaceServiceAccountID,
		credsvc.WorkspaceBundleSecretID(s.e.lc.Name), workspaceMemberAccounts(s.e.lc), s.people(ctx))
	if len(why) > 0 {
		return userErr("the workspace root's plan is not the workspace's own role, its two accounts (the workspace's own service account %s and the SA fallback's IAM account %s) and the one grant that binds the service account to %s's database, its own credentials service with the ONE bundle accessor, the members' invoke grants and the roster's people's (%s); nothing was applied, and a plan that carries anything else is not this root's to apply: read it with terraform in %s",
			workspaceAccount(project), workspaceIAMAccount(project), project, strings.Join(why, "; "), s.dir)
	}
	return nil
}

// workspaceImageCover is the image identity the workspace root's cover holds its
// service to: the registry, the remote (pull-through) repository and the owner
// this machine's config records — tf.Cover's own three fields, built the way
// init's review and managedCover build them, so the workspace's service is held
// to the same pin shape an installation's credentials service is
// (tf.Cover.credentialsImageOK). The owner is the half of that pin no plan
// holds and no tfvars may choose: an operator holds
// roles/artifactregistry.writer on this project's own repositories, so a
// reference whose owner was read out of the reference itself would admit the
// code of whoever pushed it, run as the account that reads every model
// credential the workspace holds.
func workspaceImageCover(lc *localcfg.Config) tf.Cover {
	c := tf.Cover{Ghcr: infra.GhcrRegistry, Owner: baseOwner(lc)}
	if host, err := infra.RegistryHost(lc); err == nil {
		c.Registry, _, _ = strings.Cut(host, "/")
	}
	return c
}

// workspaceMemberAccounts is the member table's job accounts, in one flat list:
// the principals the workspace root's cover admits an invoke grant to (F17), and
// the same set the root's own `members` variable makes a grant per. An entry
// that names no account contributes nothing, and a config that declares no
// workspace members at all — every installation that is not a workspace's own —
// answers an empty list, which is a plan that may hold no invoke grant.
func workspaceMemberAccounts(lc *localcfg.Config) []string {
	if lc.WorkspaceMembers == nil {
		return nil
	}
	var out []string
	for _, name := range slices.Sorted(maps.Keys(*lc.WorkspaceMembers)) {
		for _, a := range (*lc.WorkspaceMembers)[name].JobAccounts {
			out = append(out, a.Account)
		}
	}
	return out
}

// people is var.people as this run writes it: the workspace roster's human
// addresses (R6, D2971), read from the roster node in the workspace's own
// database — `workspaceRosters/<name>` — so `fugaro update` renders the people
// the roster names and the root binds one invoke grant on the workspace's
// service to each. It is the half of the root's document no plan and no
// installation output carries, so the cover's own argument (guard) and the
// tfvars are read from the same call, memoized so a plan and its sentence cannot
// disagree with the document they describe — where that call is made with the
// workdir set, which is every call that decides anything. A call made before the
// workdir exists decides nothing and is not kept (below).
//
// A roster that cannot be read — no database named, no credentials, a read that
// failed, a document this reader refuses — is not the run's failure: the people
// the workdir's own tfvars already hold go back VERBATIM, and one line says why.
// That is fail-closed and it is the security property: an empty list would plan
// the deletion of every person's invoke grant, which is a loosening of the
// workspace's own service this run never decided, and the cover would then be
// admitting a deletion the roster did not ask for. An ABSENT roster (a workspace
// whose first write has not happened) is a workspace with no people and writes
// the empty list the variable's own default is.
//
// It is the same posture serviceBlock takes of the block a run that deploys
// nothing carries, one document over.
func (s *workspaceProjectStage) people(ctx context.Context) []string {
	if s.peopleRead {
		return s.peopleList
	}
	e, lc := s.e, s.e.lc
	// s.wd is nil on a path where prepare failed before the workdir was set —
	// Check answers Todo with a sentence even where its own read never reached a
	// plan — and that call has no recorded list to carry and no document in
	// front of it. Its answer is therefore NOT memoized: the call that decides
	// what the tfvars carry is prepare's own vars, made with the workdir set, and
	// an empty list kept from before it would write `people: []` over a roster
	// this run never read — the plan that deletes every person's invoke grant,
	// which is the one loosening this function exists to prevent.
	if s.wd == nil {
		return []string{}
	}
	s.peopleRead = true
	recorded := recordedWorkspacePeople(s.wd.PrevVars)
	carry := func(why string) []string {
		s.peopleList = recorded
		fmt.Fprintf(e.r.w, "the workspace's roster is not read: %s\n", why)
		return s.peopleList
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return carry(err.Error())
	}
	store, err := workspace.NewRosterStore(db, lc.Name)
	if err != nil {
		return carry(err.Error())
	}
	node, found, err := store.Read(ctx)
	if err != nil {
		return carry(err.Error())
	}
	if !found {
		s.peopleList = []string{}
		return s.peopleList
	}
	roster, err := workspace.DecodeRoster(node.Raw)
	if err != nil {
		return carry(err.Error())
	}
	s.peopleList = rosterHumans(roster)
	return s.peopleList
}

// rosterHumans is the roster's human addresses, deduped and in the sorted order
// the root's document carries them: the people a workspace's own service admits
// (R6), and never a machine — a `sa` entry of the roster is a member's job
// account, which the root's `members` variable makes its own invoke grant for,
// and a person is admitted by address and by nothing else.
//
// The list is never nil, so the document carries `[]` and not `null`: the
// variable's default is `[]` and its value is walked, so a null here would make
// the root's own validation fail on a null list rather than on anything real.
func rosterHumans(r *workspace.Roster) []string {
	out := []string{}
	for _, p := range r.People {
		if p.Kind != workspace.KindHuman {
			continue
		}
		out = append(out, p.Address)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// recordedWorkspacePeople is var.people as the workdir's own tfvars already
// carry it: Workdir.PrevVars snapshotted before this run rebuilt the tree,
// decoded for the one field, and the empty list for a document that carries none
// — a workspace whose first run predates the variable, or one whose tfvars were
// never written. It is recordedWorkspaceService's shape, one field over: a
// document this run cannot read is a document with no people to carry, and the
// people the roster names are what the next run writes anyway.
//
// The answer is the empty LIST and never nil, on every path, because nil is
// `"people": null` in the document this run writes and the root's own variable
// walks the list: a null is refused at plan time with "iteration over null
// value" rather than carrying nothing, which is the one thing carrying is for.
func recordedWorkspacePeople(vars []byte) []string {
	if len(vars) == 0 {
		return []string{}
	}
	var v workspaceVars
	if err := json.Unmarshal(vars, &v); err != nil {
		return []string{}
	}
	if v.People == nil {
		return []string{}
	}
	return v.People
}

// prepare writes the workspace workdir's tfvars and backend and inits it.
//
// The values the root takes are this machine's own config, the workspace's own
// roster and the pin in internal/infra/tf: the GCP project the workspace is
// named after, the workspace's name (which is the Fugaro project name, and the
// last segment of the path the record lives at), the account id the cover holds
// the plan to, the owner's member table — one invoke grant per job account, F17
// — the roster's people — one invoke grant on the workspace's service per human
// entry, R6, D2971 (people) — and the workspace's own credentials service
// (serviceBlock). Nothing is derived from the installation's outputs, so the
// stage needs no spec of its own — only the state bucket, which is the
// installation's, read from the same default chain every other root's backend
// uses. The one value read back from the CLOUD is the service's URL, and it is
// read after the apply that creates it (fillServiceURL), not here.
//
// The workdir is a workdir of its own (WorkspaceWorkdir): the installation's
// tfvars, imports and saved plan must never be written by an apply of another
// root, and a plan saved by one root must never be gated and applied as
// another's (infra.ManagedWorkdir's rule, which every root of its own keeps).
func (s *workspaceProjectStage) prepare(ctx context.Context) error {
	e := s.e
	if err := e.setup(ctx); err != nil {
		return err
	}
	// installSpec and not installOptions: this stage runs in the run's loop
	// (init_stages.go, W3), so an update that reaches it on a config that can
	// no longer compose the default profile's job would stop here instead of
	// converging. What this stage reads of the spec is its state bucket alone,
	// which the carry never changes.
	spec, err := installSpec(e.r.o, e.lc, e.r.warn)
	if err != nil {
		return err
	}
	dir, err := infra.WorkspaceWorkdir(os.Getenv, e.lc.GCPProject)
	if err != nil {
		return userErr("%v", err)
	}
	wd, t, err := e.r.terraform(e.bin, dir, "workspace")
	if err != nil {
		return err
	}
	// The document is written from PrepareWorkdir's snapshot of what the workdir
	// held before this run rebuilt it: the block a previous run deployed is in
	// there, and it is what a run that deploys nothing carries forward
	// (serviceBlock).
	s.wd, s.t, s.dir = wd, t, wd.Root
	vars, err := s.vars(ctx)
	if err != nil {
		return err
	}
	if err := wd.WriteVars(vars); err != nil {
		return userErr("writing the workspace tfvars: %v", err)
	}
	backend, err := wd.WriteBackend(spec.StateBucket, infra.StatePrefixWorkspace)
	if err != nil {
		return userErr("%v", err)
	}
	if err := t.Init(ctx, backend); err != nil {
		return remote(err)
	}
	return nil
}

// vars is the workspace root's terraform.tfvars.json: exactly the values
// variables.tf declares. A key that names no variable is refused by terraform
// itself ("Value for undeclared variable"), so a half-write here fails loudly
// rather than converging a root with a zero value in it.
type workspaceVars struct {
	Project   string `json:"project"`
	Workspace string `json:"workspace"`
	Names     struct {
		WorkspaceAccountID string `json:"workspace_account_id"`
	} `json:"names"`
	Members map[string]workspaceMemberVar `json:"members"`
	// People is var.people: the workspace roster's human addresses, one invoke
	// grant on the workspace's own service per entry (R6, D2971). It is never
	// null — the root's own validation walks the list — and the empty list is a
	// workspace whose roster names no person.
	People []string `json:"people"`
	// Service is var.credentials_service, null when this run writes no service:
	// the variable's default, and the state of a workspace whose config names no
	// database to serve from. A run that deploys no service but whose workdir
	// already carries a block writes THAT block back (serviceBlock), never null —
	// null is the plan that destroys the running service.
	Service *workspaceServiceVar `json:"credentials_service"`
}

// workspaceMemberVar is one row of var.members: the member's job service
// accounts, which are the principals the root binds ONE invoke grant per (F17).
// The member's repositories are not here — the service resolves a caller by the
// account and reads its repositories from the published node (R13), and the
// root's own variable says the same of its `job_service_accounts` alone.
type workspaceMemberVar struct {
	JobServiceAccounts []string `json:"job_service_accounts"`
}

// workspaceServiceVar is var.credentials_service, field for field: what the
// workspace's own credentials service is called, the image it runs, the region
// it deploys to, the ONE bundle container of the workspace's own project, the
// Firebase project and database it reads at start-up, and the URL Cloud Run
// assigned it. The image is the only field this program derives a value for
// that a plan also carries, and the root's own validation holds it to this
// installation's registry, remote repository and release (variables.tf).
//
// URL is the audience a member's ID token is minted for. It is empty on the
// apply that CREATES the service — Cloud Run assigns it, and the root's own
// comment says why the module cannot read its own uri — and the revision then
// carries the module's unassigned placeholder, which admits nobody until
// fillServiceURL writes the URL back in the second apply.
type workspaceServiceVar struct {
	Service         string `json:"service"`
	Image           string `json:"image"`
	Region          string `json:"region"`
	BundleSecret    string `json:"bundle_secret"`
	FirebaseProject string `json:"firebase_project"`
	RTDBURL         string `json:"rtdb_url"`
	URL             string `json:"url,omitempty"`
}

// vars renders this run's document. It is called twice per applying run — by
// prepare, and again by fillServiceURL once the service's URL is known — so it
// renders from the fields the stage holds and never from a cached document.
func (s *workspaceProjectStage) vars(ctx context.Context) ([]byte, error) {
	v := workspaceVars{
		Project:   s.e.lc.GCPProject,
		Workspace: s.e.lc.Name,
		Members:   workspaceMemberTable(s.e.lc),
		People:    s.people(ctx),
		Service:   s.serviceBlock(ctx),
	}
	v.Names.WorkspaceAccountID = tf.WorkspaceServiceAccountID
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, userErr("writing the workspace tfvars: %v", err)
	}
	return append(b, '\n'), nil
}

// workspaceMemberTable is the owner's member table as var.members takes it: one
// entry per member the config declares, keyed by the member's Fugaro project
// name, each carrying the job service accounts the root binds an invoke grant
// to. It is the SAME walk as workspaceMemberAccounts — the cover's own
// argument, which admits exactly the grants this table makes — read one level
// up, and an entry that names no account still gets its row: the root's
// validation admits an empty list, and a member whose config is half written is
// the operator's to finish, not this stage's to drop from the table the service
// resolves callers by.
//
// The map is never nil, so the document carries `{}` and not `null`: the
// variable's default is `{}` and its value is walked, so a null here would make
// the root's own validation fail on values(null) rather than on anything real.
func workspaceMemberTable(lc *localcfg.Config) map[string]workspaceMemberVar {
	out := map[string]workspaceMemberVar{}
	if lc.WorkspaceMembers == nil {
		return out
	}
	for _, name := range slices.Sorted(maps.Keys(*lc.WorkspaceMembers)) {
		accounts := make([]string, 0, len((*lc.WorkspaceMembers)[name].JobAccounts))
		for _, a := range (*lc.WorkspaceMembers)[name].JobAccounts {
			accounts = append(accounts, a.Account)
		}
		out[name] = workspaceMemberVar{JobServiceAccounts: accounts}
	}
	return out
}

// serviceBlock is var.credentials_service as this run writes it: the ONE
// reading of that decision, memoized because prepare runs on every path of the
// stage (Check, Plan, Apply) and the line a run that deploys nothing prints
// belongs in the transcript once.
//
// It carries the block the workdir already holds forward in every state but the
// two that write one, and that is the security property rather than bookkeeping:
// the variable's default is null, null plans the destruction of the running
// service with its accessor and every member's invoke grant, and the root's
// cover admits exactly one deletion (a member's grant, F25) — so a run that
// deploys nothing would hand the operator a plan that is refused, or, where
// nothing gated it, take a working workspace's service away.
//
// The four states:
//
//   - a coding agent's session and a development build deploy nothing (the two
//     constants the credentials-service stage uses, one fact about the run), and
//     the block is carried with the reason in one line;
//   - a config that names no Firebase project and database cannot carry the
//     service at all — the service builds its database client before it serves
//     anything — so the block is carried and the reason is the line
//     (workspaceServiceFacts);
//   - a block whose image is already this release's (or a NEWER release's, on a
//     second machine of one installation upgraded later) is written back
//     unchanged: this run has no revision to move, and re-pinning DOWN would put
//     the previous release's process back and make two machines of one
//     installation flip the service on every update (credentialsImageStale);
//   - everything else deploys: this release's image, resolved and verified
//     through the installation's remote repository exactly as an installation's
//     service pins it (credentialsResolve — the run's one resolve, shared with
//     the policy stage, and the same shape the cover holds the plan to), with the
//     audience a previous run read back carried onto the new revision.
//
// A resolve that fails is the third state's line and not the run's failure: the
// upgrade converges everything else exactly as it did before this task, nothing
// is left stale, and the next run tries again (the credentials-service deploy's
// own posture, one root over).
func (s *workspaceProjectStage) serviceBlock(ctx context.Context) *workspaceServiceVar {
	if s.svcRead {
		return s.svc
	}
	s.svcRead = true
	e, lc := s.e, s.e.lc
	recorded := recordedWorkspaceService(s.wd.PrevVars)
	// carry is the answer of every state that deploys nothing: the block the
	// workdir holds goes back exactly as it stands, and why is one line.
	carry := func(why string) *workspaceServiceVar {
		s.svc = recorded
		fmt.Fprintf(e.r.w, "the workspace's credentials service is not deployed: %s\n", why)
		return s.svc
	}
	switch {
	case agentMarker(os.Getenv, e.r.stdinTerminal()) != "":
		return carry(credentialsSessionHeld)
	case releaseVersion() == "":
		return carry(credentialsDevBuildHeld)
	}
	if why := workspaceServiceFacts(lc); why != "" {
		return carry(why)
	}
	if recorded != nil && !credentialsImageStale(releaseVersion(), recorded.Image) {
		s.svc = recorded
		return s.svc
	}
	pinned, err := e.credentialsResolve(ctx)
	if err != nil {
		return carry(err.Error())
	}
	svc := &workspaceServiceVar{
		Service:         creds.WorkspaceServiceName,
		Image:           pinned,
		Region:          lc.Region,
		BundleSecret:    credsvc.WorkspaceBundleSecretID(lc.Name),
		FirebaseProject: lc.Budget.FirebaseProject,
		RTDBURL:         lc.Budget.RTDBURL,
	}
	// The bundle container is the workspace's own project's, derived from the
	// workspace's name — the SAME derivation the cover holds the plan's accessor
	// to (credsvc.WorkspaceBundleSecretID, which is a workspace's own
	// installation's container too: §2 of spec.md), so the container the root
	// grants a read on and the container the service reads are one string
	// written twice from one function.
	if recorded != nil {
		svc.URL = recorded.URL
	}
	s.svc = svc
	return svc
}

// recordedWorkspaceService is the credentials_service block the workspace
// root's tfvars carry, or nil when they carry none: the document
// Workdir.PrevVars snapshotted before this run rebuilt the tree, decoded
// tolerantly, because the document is this program's own file and the root's
// variables are the fence for what reaches terraform. A document that does not
// parse reads as none, and that is the safe direction: this run then WRITES a
// block (a deploy, or one of the two carried states) rather than leaving the
// key out, and a key that is left out is the destruction this file's serviceBlock
// exists to prevent.
func recordedWorkspaceService(vars []byte) *workspaceServiceVar {
	if len(vars) == 0 {
		return nil
	}
	var doc struct {
		Service *workspaceServiceVar `json:"credentials_service"`
	}
	if err := json.Unmarshal(vars, &doc); err != nil {
		return nil
	}
	return doc.Service
}

// workspaceServiceFacts is why an installation cannot carry the workspace's own
// credentials service at all: the two Firebase facts the process reads before it
// serves anything. It is the workspace's own question and not
// infra.CredentialsInstallationFacts, which asks the same two facts AND for a
// repository job the installation may admit: the workspace's service admits a
// MEMBER's job account — through the invoke grant this root makes and the
// published node it reads (R13) — so a workspace's own project, which is a
// project whose repositories are its members' and which may hold none of its
// own, is a project that carries the service with no job table at all. Requiring
// one here would refuse the service on exactly the installations this task is
// for.
func workspaceServiceFacts(lc *localcfg.Config) string {
	b := lc.Budget
	if b == nil || b.FirebaseProject == "" || b.RTDBURL == "" {
		return fmt.Sprintf("project %s's config names no Firebase project and database (budget.firebase_project and budget.rtdb_url), and the workspace's own credentials service builds its database client before it serves anything: run 'fugaro update --firebase-project <gcp-project-id>' in it first, which writes both from the Firebase root's outputs", lc.Name)
	}
	return ""
}

// fillServiceURL is the SECOND apply of this stage: the URL Cloud Run assigned
// the service, read back from the root's own output and written into
// credentials_service.url — the second half of the apply that created the service
// above, and, for a root whose state already holds the service, the whole of the
// write (Apply's empty-plan branch).
//
// It answers from the state itself: an output that is already the block's own url
// is the write already made, and one that differs — or a block that carries none
// at all, which is the revision a create apply left behind with the module's
// unassigned placeholder — is this apply. That is the same rule one root over
// (fillCredentialsAudience), and the reason the gate is not "did this run
// deploy the service": the block a run carries forward is a revision already
// serving members, and the run that finds it with no audience is the run that
// has to assign one, or the service admits nobody until the next release.
//
// Why it takes two applies (the root's own comment beside the placeholder, and
// the installation's fillCredentialsAudience for the same shape one root over):
// the URL is the audience a member's run mints its Google ID token for, Cloud
// Run assigns it, and a module cannot read its own service's uri — Terraform
// refuses a self-referential block — so the revision the create apply makes
// carries the module's unassigned placeholder, which admits nobody. Fail-closed
// either way: a revision that guessed its own audience would accept a token
// minted for another service.
//
// The apply is the same root under the same cover, gated a second time
// (planRoot's guard) because a plan nobody read is not one this stage may
// apply, and it takes no second confirmation: the owner's typed word is the
// stage's, and a second prompt for the write that makes the service they just
// confirmed usable is the confirmation step the no-prompt policy forbids.
func (s *workspaceProjectStage) fillServiceURL(ctx context.Context) error {
	raw, err := s.t.Output(ctx)
	if err != nil {
		return remote(err)
	}
	url, err := workspaceServiceURLOf(raw)
	if err != nil {
		return err
	}
	if s.svc == nil || url == s.svc.URL {
		return nil
	}
	s.svc.URL = url
	vars, err := s.vars(ctx)
	if err != nil {
		return err
	}
	if err := s.wd.WriteVars(vars); err != nil {
		return userErr("writing the workspace tfvars: %v", err)
	}
	plan, _, err := s.planRoot(ctx, true)
	if err != nil {
		return err
	}
	if plan == nil {
		return userErr("the workspace root plans nothing with credentials_service.url set to the service's own URL (%s): the root does not carry that value onto the revision, so no revision would ever carry the audience a member's run mints its ID token for", url)
	}
	if err := s.t.Apply(ctx, infra.PlanFile); err != nil {
		return remote(err)
	}
	fmt.Fprintf(s.e.r.w, "the workspace credentials service's audience is its own URL: %s\n", url)
	return nil
}

// workspaceServiceURLOf is the workspace root's workspace_service_url output:
// the service's own uri, and empty when the plan holds no service (the root's
// own output reads local.credentials_service_on).
func workspaceServiceURLOf(raw map[string]json.RawMessage) (string, error) {
	v, ok := raw["workspace_service_url"]
	if !ok {
		return "", userErr("the workspace root's outputs carry no workspace_service_url: the root publishes the service's own uri, and an installation whose outputs are missing it is one whose state this release does not read")
	}
	var url string
	if err := json.Unmarshal(v, &url); err != nil {
		return "", userErr("the workspace root's workspace_service_url output is not a URL: %v", err)
	}
	if url == "" {
		return "", userErr("the workspace root's outputs carry the workspace's credentials service with no URL: the root's output reads the service's own uri, so an empty one means the state holds no service this run's apply created, and a revision whose audience is not its own URL admits no caller")
	}
	return url, nil
}
