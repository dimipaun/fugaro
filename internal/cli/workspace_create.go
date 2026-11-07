package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
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
type workspaceProjectStage struct {
	e       *initEngine
	t       *tf.TF
	dir     string // the root's own directory, for the sentences that name where to read a plan
	applied bool
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

func (s *workspaceProjectStage) Left() initflow.Left {
	return promptLeft(initflow.WorkspaceProject, "type the project's name at the workspace's confirmation", s.e.r.o.doorName())
}

// workspaceAccount is the address of the workspace's own service account: the
// identity this stage creates. It is derived and not read back — the same
// string the root's own ${google_service_account.workspace.email} resolves to —
// so the sentence the operator confirms names the identity before anything is
// created.
func workspaceAccount(project string) string {
	return tf.WorkspaceServiceAccountID + "@" + project + ".iam.gserviceaccount.com"
}

// declared is whether this installation is a workspace's own: the config
// carries the owner's half of the membership (workspaceDeclared, which is
// workspaceRecordOf's own predicate — W2's publish stage and the Identity
// Platform carve-out are built on the same one).
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
func (s *workspaceProjectStage) declared() (bool, error) {
	if !workspaceDeclared(s.e.lc) {
		return false, nil
	}
	if _, err := workspaceRecordOf(s.e.lc); err != nil {
		var notOurs *workspacePublishNotOurs
		if !errors.As(err, &notOurs) {
			return false, err
		}
	}
	return true, nil
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
// It does NOT hold the plan to this root's cover: the cover belongs to the two
// paths that lead to an apply (Plan and Apply), which is where a plan that is
// not this root's is refused by name. A read that fails is not this stage's
// answer either — the run has not been told there is something to do, and it
// has not been told there is not: it is a Todo, and the read is made again on
// the apply path, whose own failure is the run's.
//
// A config that declares no workspace is a SKIP with the reason, never a
// failure, and it reaches no terraform at all: it is every installation that is
// not a workspace's own, which is most of them.
func (s *workspaceProjectStage) Check(ctx context.Context) (initflow.Status, error) {
	ok, err := s.declared()
	if err != nil {
		return initflow.Status{}, userErr("%v", err)
	}
	if !ok {
		return initflow.Status{State: initflow.Skipped, Detail: workspaceProjectSkip(s.e.lc)}, nil
	}
	plan, _, err := s.readPlan(ctx, false)
	if err == nil && plan == nil {
		project := s.e.lc.GCPProject
		return initflow.Status{State: initflow.Done, Detail: fmt.Sprintf("the workspace's own service account %s holds %s on %s", workspaceAccount(project), tf.WorkspaceDatabaseRole(project), project)}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: workspaceProjectTodo(s.e.lc)}, nil
}

// workspaceProjectTodo is what the stage says when it has something to do, in
// Check and in the plan view: the account and the grant, with the root's own
// name for what reaches what.
func workspaceProjectTodo(lc *localcfg.Config) string {
	return fmt.Sprintf("the workspace's own service account %s and its one grant %s on %s are planned when applied",
		workspaceAccount(lc.GCPProject), tf.WorkspaceDatabaseRole(lc.GCPProject), lc.GCPProject)
}

// workspaceProjectSkip is why this installation has no workspace's own project
// to converge, in the words the operator reads: the owner's half is what makes
// an installation a workspace's, and its absence is a member's project or a
// plain one — the two cases where no account may be created here.
func workspaceProjectSkip(lc *localcfg.Config) string {
	return fmt.Sprintf("this installation declares no workspace of its own: %s's project config carries neither workspace_members nor workspace_cap, so there is no workspace service account to create and no database grant to make here (a member's project holds no grant on the workspace's project, spec §5)", lc.Name)
}

// Plan shows the real plan and guards it, and changes nothing.
func (s *workspaceProjectStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	ok, err := s.declared()
	if err != nil {
		return initflow.Plan{}, userErr("%v", err)
	}
	if !ok {
		return initflow.Plan{NothingToDo: true, Detail: workspaceProjectSkip(s.e.lc)}, nil
	}
	plan, counts, err := s.readPlan(ctx, true)
	if err != nil {
		return initflow.Plan{}, err
	}
	if plan != nil {
		fmt.Fprint(s.e.r.w, tf.Summary(plan))
	}
	return s.e.planned(counts), nil
}

// Apply is the write, and a coding agent's session never reaches it: the typed
// confirmation cannot be given there (stepConfirmation), and what it would
// confirm is an access grant.
func (s *workspaceProjectStage) Apply(ctx context.Context, _ initflow.Env) (initflow.Outcome, error) {
	ok, err := s.declared()
	if err != nil {
		return initflow.Outcome{}, userErr("%v", err)
	}
	if !ok {
		return initflow.Outcome{Detail: workspaceProjectSkip(s.e.lc)}, nil
	}
	plan, counts, err := s.readPlan(ctx, true)
	if err != nil {
		return initflow.Outcome{}, err
	}
	project := s.e.lc.GCPProject
	account := workspaceAccount(project)
	if plan == nil {
		fmt.Fprintf(s.e.r.w, "No changes: the workspace's own service account %s holds %s on %s.\n", account, tf.WorkspaceDatabaseRole(project), project)
		return initflow.Outcome{Detail: "the workspace's own service account and its one grant are already there"}, nil
	}
	fmt.Fprint(s.e.r.w, tf.Summary(plan))
	confirmed, reachable, err := s.e.r.askTyped(fmt.Sprintf("creates the workspace's own service account %s in GCP project %s and grants it %s on that project's database (%s): the account is the one identity that reaches the workspace's database, because the database's rules deny every client — and it is a GRANT, so --yes never confirms it; %s)", account, project, tf.WorkspaceDatabaseRole(project), counts.String(), initflow.AgentAdvice))
	if err != nil {
		return initflow.Outcome{}, err
	}
	if !reachable {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	if !confirmed {
		return initflow.Outcome{}, s.e.r.notConfirmed("nothing was applied")
	}
	if err := s.t.Apply(ctx, infra.PlanFile); err != nil {
		return initflow.Outcome{}, remote(err)
	}
	s.applied = true
	s.e.r.res.Applied = true
	fmt.Fprintf(s.e.r.w, "created the workspace's own service account %s and its one grant (%s on %s)\n", account, tf.WorkspaceDatabaseRole(project), project)
	return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("applied: %s (the workspace's own service account %s)", counts.String(), account)}, nil
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
		if err := s.guard(plan); err != nil {
			return nil, nil, err
		}
	}
	counts := infra.CountPlan(plan)
	return plan, &counts, nil
}

// guard is this root's cover: the plan is the workspace's own role, its
// service account and that account's one grant, and nothing else.
// tf.WorkspaceNotCovered answers what of
// the plan is not, so the message can name it rather than say "refused".
func (s *workspaceProjectStage) guard(plan *tf.Plan) error {
	if why := tf.WorkspaceNotCovered(plan, s.e.lc.GCPProject, tf.WorkspaceServiceAccountID); len(why) > 0 {
		return userErr("the workspace root's plan is not the workspace's own role, its service account and that account's one grant on the workspace's database (%s); nothing was applied, and a plan that carries anything else is not this root's to apply: read it with terraform in %s", strings.Join(why, "; "), s.dir)
	}
	return nil
}

// prepare writes the workspace workdir's tfvars and backend and inits it.
//
// The three values the root takes are this machine's own config and the pin in
// internal/infra/tf: the GCP project the workspace is named after, the
// workspace's name (which is the Fugaro project name, and the last segment of
// the path the record lives at), and the account id the cover holds the plan
// to. Nothing is derived from the installation's outputs, so the stage needs no
// spec of its own — only the state bucket, which is the installation's, read
// from the same default chain every other root's backend uses.
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
	spec, err := installOptions(e.r.o, e.lc)
	if err != nil {
		return err
	}
	vars, err := workspaceVarsFor(e.lc)
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
	s.t, s.dir = t, wd.Root
	return nil
}

// workspaceVars is the workspace root's terraform.tfvars.json: exactly the
// three values variables.tf declares. A key that names no variable is refused
// by terraform itself ("Value for undeclared variable"), so a half-write here
// fails loudly rather than converging a root with a zero value in it.
type workspaceVars struct {
	Project   string `json:"project"`
	Workspace string `json:"workspace"`
	Names     struct {
		WorkspaceAccountID string `json:"workspace_account_id"`
	} `json:"names"`
}

func workspaceVarsFor(lc *localcfg.Config) ([]byte, error) {
	var v workspaceVars
	v.Project, v.Workspace = lc.GCPProject, lc.Name
	v.Names.WorkspaceAccountID = tf.WorkspaceServiceAccountID
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
