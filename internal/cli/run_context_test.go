package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

// contextRepos are the repositories these launches read: two names, so a test
// that reads a list back has an order and a length to check.
var contextRepos = []string{"acme/lib", "acme/other"}

// contextLaunchFixture is a launch fixture of an installation whose
// credentials service serves its repository: the block `fugaro update` writes
// where it deploys one, and the repository's own record — the half its root's
// apply writes (markRepoCredentialsServiceReady, infra.CredentialsServiceServes).
// The service is what reads a context repository for a run, so a launch that
// cannot reach it is refused (contextReposRefusal).
func contextLaunchFixture(t *testing.T) *launchBudgetFixture {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	f.appendConfig(t, "credentials_service:\n  url: "+launchCredsURL+"\n")
	markRepoCredentialsServiceReady(t, f.cloudFixture)
	return f
}

// publishAllowOnly publishes a policy whose allow_repos is exactly allow. It
// is the node this Fugaro writes — the version it reads, the budget mode the
// fixture's database carries — so the only thing a test varies is the list.
func publishAllowOnly(t *testing.T, f *launchBudgetFixture, allow ...string) {
	t.Helper()
	list, err := json.Marshal(allow)
	if err != nil {
		t.Fatal(err)
	}
	publishPolicy(t, f, `{"version":`+jsonInt(projectpolicy.Version)+`,"budget":{"mode":"enforce","per_run_usd":5},"allow_repos":`+string(list)+`}`)
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// contextRun runs the fixture's launch with the context repositories named,
// the way a person would type them.
func contextRun(t *testing.T, f *launchBudgetFixture, repos ...string) (string, string, error) {
	t.Helper()
	args := []string{"--context", repos[0]}
	for _, repo := range repos[1:] {
		args = append(args, "--context", repo)
	}
	return runBudget(t, args...)
}

// TestRunContextReposMustBeAdmitted: every context repository a launch names
// is admitted by the project's published allow_repos, exactly as the run's own
// repository is (C5a, D613): allow_repos is what decides which repositories a
// run may touch, and a run that may read one is a wider grant than one that
// only pushes its own. The refusal is the policy's own, worded once
// (projectpolicy.NotAdmitted), and it prints the command that fixes it.
func TestRunContextReposMustBeAdmitted(t *testing.T) {
	f := contextLaunchFixture(t)
	publishAllowOnly(t, f, "github/acme/app")
	wantRefused(t, f.cloudFixture, "fugaro project repos add github/acme/lib",
		"run", "--repo", "acme/app", "--run-id", budgetRun, "--context", "acme/lib", "Add a feature")

	// The same launch with a list that admits both is not refused, so the
	// refusal above is the allow-list's and nothing else about this launch.
	admits := contextLaunchFixture(t)
	publishAllowOnly(t, admits, "github/acme/app", "github/acme/lib")
	if _, _, err := contextRun(t, admits, "acme/lib"); err != nil {
		t.Fatalf("a context repository the allow-list admits was refused: %v", err)
	}
}

// TestRunContextReposCappedAtFive: a run reads a handful of repositories at
// most (task.MaxContextRepos), and the sixth is refused by the launcher before
// the launch reaches the cloud at all — the flag is judged before openCloud,
// so this costs no call and starts no execution.
func TestRunContextReposCappedAtFive(t *testing.T) {
	f := contextLaunchFixture(t)
	args := []string{"run", "--repo", "acme/app", "--run-id", budgetRun}
	for i := 0; i < task.MaxContextRepos+1; i++ {
		args = append(args, "--context", "acme/lib"+string(rune('a'+i)))
	}
	args = append(args, "Add a feature")
	wantRefused(t, f.cloudFixture, "at most 5", args...)
}

// TestRunContextRefusedOnBitbucket: a context repository is read through the
// credentials service, whose fetch is brokered against the task's own
// repository and its installation (secrets-brokering D496), and that path
// serves GitHub alone. A Bitbucket run is refused rather than launched with a
// repository nothing can fetch for it.
func TestRunContextRefusedOnBitbucket(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	env := memEnv(t, f.cloudFixture)
	slug := mustSlug("bitbucket", proxiedRepo)
	spec := &task.Spec{Version: 1, RunID: budgetRun, Repo: proxiedRepo, Ref: "main", Workflow: "app",
		Task: "Read the graph", ContextRepos: []string{"workspace-a/lib"}}
	err := contextReposRefusal(context.Background(), env, slug, spec)
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is on bitbucket") {
		t.Fatalf("a context repository on a Bitbucket run = %v, want a refusal naming the provider", err)
	}
	// A launch naming no context repository is refused by nothing here, so the
	// refusal above is the list's and not the provider's.
	if err := contextReposRefusal(context.Background(), env, slug,
		&task.Spec{Version: 1, RunID: budgetRun, Repo: proxiedRepo, Ref: "main", Workflow: "app", Task: "x"}); err != nil {
		t.Fatalf("a Bitbucket launch naming no context repository = %v, want no refusal", err)
	}
}

// TestRunContextRefusedWithoutService: the address a launch hands a job is
// what makes the credentials service reachable at all, and the in-job mint
// path is deliberately not extended to context repositories (C5a) — a run on
// it holds a token for its own repository and nothing else. So a launch of a
// repository the service does not serve is refused, in the operator's own
// words, rather than recorded a list the run could never read.
func TestRunContextRefusedWithoutService(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	_, _, err := contextRun(t, f, "acme/lib")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a context repository on an installation with no credentials service = %v, want a user refusal", err)
	}
	for _, want := range []string{"credentials service", selfCommand() + " update", "agent.auth: cascade", "drop --context"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if launchedAny(f) {
		t.Fatal("a refused launch started an execution")
	}
	// The same repository with the service installed and its record on is
	// launched, so the refusal above is the service's and not --context's.
	served := contextLaunchFixture(t)
	publishAllowOnly(t, served, "github/acme/*")
	if _, _, err := contextRun(t, served, "acme/lib"); err != nil {
		t.Fatalf("a context repository on a served repository was refused: %v", err)
	}
	if !launchedAny(served) {
		t.Fatal("the launch that was not refused started no execution")
	}
}

// TestTokenContextClaimMatchesTask: the list a launch records on the task and
// the list it mints into the run's identity are the same list (D613). The
// runner reads the task and the credentials service can read nothing but the
// claim, so a pair that disagreed would let a run read a repository its
// identity does not name — or name one it was never admitted for.
func TestTokenContextClaimMatchesTask(t *testing.T) {
	f := contextLaunchFixture(t)
	publishAllowOnly(t, f, "github/acme/*")
	var env map[string]string
	f.run.OnRun = func(c gcpfake.RunCall) { env = c.Env }
	if _, _, err := contextRun(t, f, contextRepos...); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f.cloudFixture, budgetRun)
	if len(spec.ContextRepos) != len(contextRepos) {
		t.Fatalf("the stored task names %v, want %v", spec.ContextRepos, contextRepos)
	}
	for i, want := range contextRepos {
		if spec.ContextRepos[i] != want {
			t.Fatalf("the stored task names %v, want %v", spec.ContextRepos, contextRepos)
		}
	}
	claims := claimsOf(t, f, unsealed(t, markerKey(t, env), []byte(readToken(t, f.cloudFixture, budgetRun)), budgetRun))
	repos, ok := claims[token.ClaimContextRepos].([]any)
	if !ok || len(repos) != len(spec.ContextRepos) {
		t.Fatalf("the identity's %s = %v, want the task's %v", token.ClaimContextRepos, claims[token.ClaimContextRepos], spec.ContextRepos)
	}
	for i, want := range spec.ContextRepos {
		if repos[i] != want {
			t.Fatalf("the identity's %s = %v, want the task's %v", token.ClaimContextRepos, claims[token.ClaimContextRepos], spec.ContextRepos)
		}
	}
	// A launch that names none mints exactly the claim set every earlier
	// release minted: the claim is written only when the run has it.
	plain := contextLaunchFixture(t)
	publishAllowOnly(t, plain, "github/acme/*")
	var plainEnv map[string]string
	plain.run.OnRun = func(c gcpfake.RunCall) { plainEnv = c.Env }
	if _, _, err := runBudget(t); err != nil {
		t.Fatal(err)
	}
	without := claimsOf(t, plain, unsealed(t, markerKey(t, plainEnv), []byte(readToken(t, plain.cloudFixture, budgetRun)), budgetRun))
	if v, ok := without[token.ClaimContextRepos]; ok {
		t.Fatalf("a launch naming no context repository minted %s = %v", token.ClaimContextRepos, v)
	}
}

// TestRunContextRefusedOverTheClaimBound: the identity provider refuses a
// custom token whose claims pass the bound the token package carries
// (token.MaxClaimsBytes), and it refuses it at the exchange — inside the run,
// long after this launcher was told the launch had succeeded. So a launch
// whose context repositories would pass the bound is refused here, before a
// signature is asked for and before an execution exists.
func TestRunContextRefusedOverTheClaimBound(t *testing.T) {
	f := contextLaunchFixture(t)
	publishAllowOnly(t, f, "github/acme/*")
	long := make([]string, 0, task.MaxContextRepos)
	for i := 0; i < task.MaxContextRepos; i++ {
		long = append(long, "acme/"+strings.Repeat("r", 180)+string(rune('a'+i)))
	}
	wantRefused(t, f.cloudFixture, "custom token may carry",
		append([]string{"run", "--repo", "acme/app", "--run-id", budgetRun}, contextArgs(long)...)...)
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Fatalf("%d claim sets were signed for a refused launch", n)
	}
	if tok := readToken(t, f.cloudFixture, budgetRun); tok != "" {
		t.Error("the refused launch left a budget token in the bucket")
	}
}

func contextArgs(repos []string) []string {
	var args []string
	for _, repo := range repos {
		args = append(args, "--context", repo)
	}
	return append(args, "Add a feature")
}

// TestRunContextReposGateTheJobImage: the field is one an older runner
// refuses outright (task.Parse decodes with DisallowUnknownFields), so a
// launch whose job image predates the release that reads it is refused here
// rather than started on a job that dies at bootstrap (contextReposSince). The
// gate is the field's and never the flag's: a launch that names none is
// launched onto any image at all, which is what keeps today's launches
// byte-identical.
func TestRunContextReposGateTheJobImage(t *testing.T) {
	f := contextLaunchFixture(t)
	publishAllowOnly(t, f, "github/acme/*")
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, contextReposSince, -1))
	wantRefused(t, f.cloudFixture, "read-only context repositories",
		"run", "--repo", "acme/app", "--run-id", budgetRun, "--context", "acme/lib", "Add a feature")

	plain := contextLaunchFixture(t)
	writeBuildRecord(t, plain.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, contextReposSince, -1))
	if _, _, err := runBudget(t); err != nil {
		t.Fatalf("a launch naming no context repository was gated on the image: %v", err)
	}
}

// TestRunContextReposNeedARole: the launcher mints the run's identity with the
// context repositories in it, and what reads that identity holds the signature
// rather than a role (the credentials service, plan task C5b), so a repository
// the caller holds no role in must be refused here — a launch that recorded one
// would hand a run a read the person was never given. ActionViewRepo is the
// role that read takes: nothing of a context repository is launched, written or
// pushed, only read, so a viewer of it is enough where the run's own repository
// takes operator.
func TestRunContextReposNeedARole(t *testing.T) {
	f := contextLaunchFixture(t)
	publishAllowOnly(t, f, "github/acme/*")
	accessRunNode(t, f, userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA))
	accessRunAs(t, accessOperator)
	_, _, err := contextRun(t, f, "acme/other")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a context repository the credential holds no role in: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{
		"nothing was launched",
		"reading " + accessRepoB,
		"needs the role viewer",
		"holds no role",
		accessOperator,
		"user grant " + accessOperator + " --role viewer --scope " + accessRepoB,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if launchedAny(f) {
		t.Fatal("a refused launch started an execution")
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("the refused launch stored its task")
	}
	// The role is the read's and not the launch's: the same credential with a
	// viewer's role in the context repository is launched.
	viewer := contextLaunchFixture(t)
	publishAllowOnly(t, viewer, "github/acme/*")
	accessRunNode(t, viewer,
		userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA),
		userStored(t, accessOperator, accesslib.RoleViewer, accessRepoB))
	if _, _, err := contextRun(t, viewer, "acme/other"); err != nil {
		t.Fatalf("a viewer of the context repository was refused: %v", err)
	}
	if !launchedAny(viewer) {
		t.Fatal("the launch that was not refused started no execution")
	}
}

// TestRunContextRefusedWithNoPolicy: a context repository is admitted by the
// project's published allow_repos (plan task C5a), and an installation that has
// published no policy has no list that could admit one — the absent node is
// read as no policy rather than as an empty list, which is what keeps a launch
// that names no context repository possible there (launchPolicyGates' own
// `!found` return). So the launch that names one is refused, before the task is
// stored and before the claim that would name the repository is minted.
func TestRunContextRefusedWithNoPolicy(t *testing.T) {
	f := contextLaunchFixture(t)
	_, _, err := contextRun(t, f, "acme/lib")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a context repository with no policy published: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"no policy is published", "project policy publish", "project repos add", "drop --context"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if launchedAny(f) {
		t.Fatal("a refused launch started an execution")
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("the refused launch stored its task")
	}
	// The same installation launches a run naming no context repository, so the
	// refusal above is the list's and not the absent policy's.
	plain := contextLaunchFixture(t)
	if _, _, err := runBudget(t); err != nil {
		t.Fatalf("a launch naming no context repository on an installation with no policy was refused: %v", err)
	}
	if !launchedAny(plain) {
		t.Fatal("the launch that was not refused started no execution")
	}
}

// TestRunContextReposAreStoredCanonical: the list a launch carries is written in
// the one form the allow-list, the task and the claim are all judged in
// (task.CanonicalRepo). A person who types a spelling of a repository — a
// capital, a ".git" suffix — gets the repository's canonical name on the task
// and in the identity, so no reader of either has to canonicalise the value it
// was handed, and two spellings of one repository cannot become two entries.
func TestRunContextReposAreStoredCanonical(t *testing.T) {
	f := contextLaunchFixture(t)
	publishAllowOnly(t, f, "github/acme/*")
	var env map[string]string
	f.run.OnRun = func(c gcpfake.RunCall) { env = c.Env }
	if _, _, err := contextRun(t, f, "Acme/Lib.git"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f.cloudFixture, budgetRun)
	if len(spec.ContextRepos) != 1 || spec.ContextRepos[0] != "acme/lib" {
		t.Fatalf("the stored task names %v, want [acme/lib]", spec.ContextRepos)
	}
	claims := claimsOf(t, f, unsealed(t, markerKey(t, env), []byte(readToken(t, f.cloudFixture, budgetRun)), budgetRun))
	repos, ok := claims[token.ClaimContextRepos].([]any)
	if !ok || len(repos) != 1 || repos[0] != "acme/lib" {
		t.Fatalf("the identity's %s = %v, want [acme/lib]", token.ClaimContextRepos, claims[token.ClaimContextRepos])
	}
}

// TestRunContextNeedsTheAppInstalledIsSaid: the run reads the repositories this
// launch records, with a read-only token the credentials service mints for
// them, and that mint is scoped to what the installation's GitHub App covers —
// a GitHub-side state no gate here reads, so it is a thing a person has to hear
// about rather than one this command leaves them to discover at bootstrap. The
// line is the field's: a launch that names none says nothing.
func TestRunContextNeedsTheAppInstalledIsSaid(t *testing.T) {
	f := contextLaunchFixture(t)
	publishAllowOnly(t, f, "github/acme/*")
	_, errOut, err := contextRun(t, f, "acme/lib")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "GitHub App") || !strings.Contains(errOut, "acme/lib") {
		t.Fatalf("a launch carrying --context said nothing about the App installation the read-only mint needs:\n%s", errOut)
	}
	plain := contextLaunchFixture(t)
	publishAllowOnly(t, plain, "github/acme/*")
	_, quiet, err := runBudget(t)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "GitHub App") {
		t.Fatalf("a launch naming no context repository printed the context line:\n%s", quiet)
	}
}

// contextCascadeSharedPolicy is the shared-job fixture's own policy with the two
// things a launch of an `agent.auth: cascade` repository needs to pass the
// cascade's gate: the switch that turns the cascade on (routing.claude.via at
// O2, which only the operator publishes) and the model allow-list the
// credentials service holds every call of such a run to (C1.11). The profile,
// the allow-list and the job are the fixture's own, so the launch this test
// judges is a shared-job one for the reason it is about.
func contextCascadeSharedPolicy() string {
	return `{"version":` + jsonInt(projectpolicy.Version) + `,"allow_repos":["github/acme/*"],` +
		`"routing":{"claude":{"via":"cascade"}},` +
		`"budget":{"mode":"enforce","per_run_usd":5,"allowed_models":["claude-sonnet-5-5"]},` +
		`"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}}}`
}

// TestContextReposRefusalJudgesTheDecidedJob: a launch whose document selects a
// profile a shared project job holds runs on that job, and a shared project job
// is told the credentials service's address on the same switch a repository's
// own job is (credentialsServiceLaunchEnv, B5.2): the repository's record and
// its document's auth. So --context is refused there while the repository is
// not switched — the case that answers "" for every launch judges the
// repository's own job, and would pass a launch whose run is then handed no
// address at all — and goes once it is.
func TestContextReposRefusalJudgesTheDecidedJob(t *testing.T) {
	// The verdict that passes: a repository the published policy serves no
	// profile for keeps its own job, which is handed the address, so the launch
	// goes — and the address is on the execution, which is the same answer the
	// gate read.
	own := contextLaunchFixture(t)
	publishAllowOnly(t, own, "github/acme/*")
	var launchedEnv map[string]string
	own.run.OnRun = func(c gcpfake.RunCall) { launchedEnv = c.Env }
	if _, _, err := contextRun(t, own, "acme/lib"); err != nil {
		t.Fatalf("--context on a launch that runs on the repository's own job: %v", err)
	}
	if got := launchedEnv[envCredentialsServiceURL]; got != launchCredsURL {
		t.Fatalf("the launch passed the gate and the execution was handed %q, want %s: the gate and the execution disagree about the address", got, launchCredsURL)
	}

	// The same repository and the same switch, on a document that selects a
	// shared project job.
	cascadeShared := func(recorded bool) *projectJobFixture {
		f := newProjectJobFixtureMode(t, "enforce")
		publishPolicy(t, f.launchBudgetFixture, contextCascadeSharedPolicy())
		f.db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
		f.appendConfig(t, "credentials_service:\n  url: "+launchCredsURL+"\n")
		if recorded {
			markRepoCredentialsServiceReady(t, f.cloudFixture)
		}
		projectJobCheckoutYAML(t, projectJobCascadeRepoYAML)
		return f
	}
	shared := cascadeShared(false)
	_, _, err := runBudget(t, "--context", "acme/lib")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("--context on a launch that runs on a shared project job: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"shared project job", shared.job, "no --context"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if launchedAny(shared.launchBudgetFixture) {
		t.Fatal("a refused launch started an execution")
	}
	if storedTask(t, shared.cloudFixture) {
		t.Error("the refused launch stored its task")
	}
	if n := len(shared.iam.SignCalls()); n != 0 {
		t.Errorf("%d claim sets were signed for a refused launch", n)
	}

	// The job the gate judged is the job the launch runs on: the same launch
	// with no context repository starts on it.
	control := cascadeShared(false)
	if _, _, err := runBudget(t); err != nil {
		t.Fatalf("a launch naming no context repository on the same document: %v", err)
	}
	if profile, provider, _ := launchedProjectJob(t, control.cloudFixture); profile != "standard" || provider != "github" {
		t.Fatalf("the launch ran on the %s/%s job, want the standard/github one the gate judged", profile, provider)
	}
	// The same launch once the repository is switched: the shared job is told
	// the address, so the gate passes and the execution carries it.
	switched := cascadeShared(true)
	var sharedEnv map[string]string
	switched.run.OnRun = func(c gcpfake.RunCall) { sharedEnv = c.Env }
	if _, _, err := runBudget(t, "--context", "acme/lib"); err != nil {
		t.Fatalf("--context on a launch on a switched shared project job %s: %v", switched.job, err)
	}
	if got := sharedEnv[envCredentialsServiceURL]; got != launchCredsURL {
		t.Fatalf("the launch passed the gate and the shared job's execution was handed %q, want %s", got, launchCredsURL)
	}
	// And the pair the gate judges with agrees: the switched repository's own
	// job and the shared job it runs on are both handed the address.
	env := memEnv(t, switched.cloudFixture)
	spec := readSpec(t, switched.cloudFixture, budgetRun)
	auth, deployed := env.repoServiceAuth(context.Background(), spec, io.Discard)
	for name, profile := range map[string]string{"the repository's own job": "", "the shared project job": "standard"} {
		if got := credentialsServiceLaunchEnv(env.lc, profile, spec.Repo, auth, env.policyMode(context.Background()), deployed, false); len(got) == 0 {
			t.Fatalf("%s is handed no address (auth %q, deployed %v) on a switched repository", name, auth, deployed)
		}
	}

	// A review launch is the shape this gate judges as a repository's own
	// however the repository's work is placed: its execution runs on the
	// review-only variant, which its own deployment tells the address — never
	// on the shared project job — so --context is not refused there.
	review := &task.Spec{Version: 1, RunID: budgetRun, Repo: spec.Repo, Ref: spec.Ref, Workflow: spec.Workflow,
		Mode: recipe.ModeReview, Task: "Read the graph", ContextRepos: []string{"acme/lib"}}
	if err := contextReposRefusal(context.Background(), env, appSlug, review); err != nil {
		t.Fatalf("--context on a review launch, whose job carries the service's address by its own deployment: %v", err)
	}
}
