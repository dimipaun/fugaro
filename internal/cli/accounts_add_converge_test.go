package cli

import (
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// accounts_add_converge_test.go is the converge half of `fugaro accounts add`
// (accounts_add_converge.go): what the command does after the local config is
// written — the secret, the mark, the three stages it invokes in-process, and
// the read-back that is the command's own last line.

// TestAccountsAddLeavesNothingToRun is A2's red step (spec §2.2 steps 5–8): a
// command that only declares the account leaves the owner three commands — a
// secret to store, a job set to deploy, a node to publish — and the whole
// point of the converge is that it leaves none of them. It fails against an
// implementation that writes the local config and stops: the node carries no
// account, no job mounts the token, and the last line names a run that is not
// ready.
func TestAccountsAddLeavesNothingToRun(t *testing.T) {
	r := newAddRig(t)
	out, _ := r.add(t, "team-a", "--repos", "owner/repo-a")

	// The node every run of the project is admitted by: the account and the
	// repositories its token may be used by, derived from the local config the
	// command just wrote and published by the Policy stage and no other writer.
	p := r.policy(t)

	acc, ok := p.Accounts["team-a"]
	if !ok {
		t.Errorf("the published node names no account team-a: accounts = %v\nthe command printed:\n%s", slices.Sorted(maps.Keys(p.Accounts)), out)
	} else if want := []string{"github/owner/repo-a"}; !slices.Equal(acc.AllowRepos, want) {
		t.Errorf("the published accounts.team-a.allow_repos = %v, want %v", acc.AllowRepos, want)
	}
	// The profile the command added the name to serves it, which is what makes
	// a job exist for it at all.
	if pr := p.Profiles["standard"]; !slices.Contains(pr.ClaudeAccounts, "team-a") {
		t.Errorf("the published profiles.standard.claude_accounts = %v, want team-a", pr.ClaudeAccounts)
	}

	// The job set: one job per (profile, provider, account), and this account's
	// token mounted on that job and no other — the secretAccessor grant is the
	// job's own secret_env, and the negative half is the one that matters.
	jobs := r.projectJobs(t)
	job, ok := jobs[gcp.ProjectJobName("standard", "github", "team-a")]
	if !ok {
		t.Fatalf("the job set has no standard/github/team-a: jobs = %v", slices.Sorted(maps.Keys(jobs)))
	}
	secret := config.ClaudeAccountSecret("team-a")
	if got := job.SecretEnv[config.ReservedSecrets[config.ClaudeOAuthToken]]; got != secret {
		t.Errorf("standard/github/team-a mounts %q as the Claude token, want %q", got, secret)
	}
	for name, j := range jobs {
		if name == job.Job {
			continue
		}
		for _, mounted := range j.SecretEnv {
			if mounted == secret {
				t.Errorf("job %s mounts %s too: one account's token rides on that account's job and no other", name, secret)
			}
		}
	}

	// The last line is the whole point of the command: it names the one thing
	// the owner does next, and it is a run.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "fugaro run") {
		t.Errorf("the command's last line is %q, and it has to name the fugaro run that is now ready", last)
	}
	if strings.Contains(last, "fugaro update") {
		t.Errorf("the command's last line is %q: it names an update, and there is nothing left to update", last)
	}
}

// accountsAddRow is one line of the command's block, by the label it opens
// with: an assertion about a step is an assertion about that step's own line
// and not about a word appearing anywhere in the output.
func accountsAddRow(t *testing.T, out, label string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, label+" ") {
			return line
		}
	}
	t.Fatalf("the command's block has no %s line:\n%s", label, out)
	return ""
}

// TestAccountsAddPublishHoldsTheAdoptionGate is the plan's A2 pin on the
// publish's gate: the node is written by policyMigrationFor and by nothing
// else, so this command inherits that writer's gate instead of opening a second
// road into the database with a weaker one (the sibling of the settings
// design's TestUpdatePolicyPublishSkipsTheDatabaseTheRulesStageSkips).
//
// The fixture hands the project's database another installation's mark, which
// is the state budgetDBUnadopted refuses for every writer into it. The gate is
// asserted the only way a gate can be: the node is the bytes it was, the mark
// is the mark it held, and not one write reached the database.
func TestAccountsAddPublishHoldsTheAdoptionGate(t *testing.T) {
	r := newAddRig(t)
	r.db.Set(budget.PathMark, birchMark())
	node, mark := r.db.Value(projectpolicy.PathShared()), r.db.Value(budget.PathMark)

	out, _ := r.add(t, "team-a", "--repos", "owner/repo-a")

	line := accountsAddRow(t, out, "publish")
	for _, want := range []string{"skipped:", `the database carries the Fugaro mark of project "birch"`} {
		if !strings.Contains(line, want) {
			t.Errorf("the publish line is %q, and it has to carry %q: the skip's own reason, and never a claim that the node was written", line, want)
		}
	}
	if got := r.db.Value(projectpolicy.PathShared()); !reflect.DeepEqual(got, node) {
		t.Errorf("the node is %v, and it was %v before the command: a database that is not this installation's is not a database this command writes", got, node)
	}
	if got := r.db.Value(budget.PathMark); !reflect.DeepEqual(got, mark) {
		t.Errorf("another installation's mark moved: %v, want %v", got, mark)
	}
	for _, req := range r.db.Requests() {
		if req.Method != http.MethodGet {
			t.Errorf("%s %s was sent to a database that is not this installation's: the publish is skipped, and no other step of this command writes there", req.Method, req.Path)
		}
	}
	// The gate holds the publish and nothing else. The declaration and the token
	// are this command's own work in this project, and the skip is a sentence
	// about the node rather than a command that did nothing.
	if !strings.Contains(out, "project-wide, version 1") {
		t.Errorf("the account's own store did not run:\n%s", out)
	}
	if got := r.addLocal(t).ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-a"}) {
		t.Errorf("the account the command declared is %v, want owner/repo-a", got)
	}
}

// TestAccountsAddStoresProjectWideAndNothingElse is spec §2.2 step 5: the
// value lands as a new version of the account's own project-wide secret and
// under no other secret, and the repository-scoped road to the same value is
// shut — which is why the command stores it itself.
//
// The refused spellings are asserted with an EMPTY stdin: a store that reached
// the read first would refuse the empty value instead, so the refusal naming
// claude_accounts is the proof that the value was never read at all.
func TestAccountsAddStoresProjectWideAndNothingElse(t *testing.T) {
	r := newAddRig(t)
	writes := r.secretWrites(t)

	r.add(t, "team-a", "--repos", "owner/repo-a")

	id := gcp.ProjectSecretID(initProjectName, config.ClaudeAccountSecret("team-a"))
	if got := string(r.sm.Latest(id)); got != addToken() {
		t.Errorf("%s holds %q, want the token the command read: the value the person typed is the value the account's own secret holds", id, got)
	}
	if want := []string{id}; !slices.Equal(writes.created, want) || !slices.Equal(writes.added, want) {
		t.Errorf("the command wrote %v (created) and %v (versions), want only %v: the account's value is stored project-wide and nowhere else", writes.created, writes.added, want)
	}
	// The value is the store's and never the file's: the local config records
	// where a token may be sent, and nothing about the token.
	if strings.Contains(readFileString(t, r.cfg), addToken()) {
		t.Errorf("the local config carries the token's value:\n%s", readFileString(t, r.cfg))
	}

	// The account's own secret at repository scope, for a repository this
	// account's allow_data_to does not name: refused before the value is read,
	// so a repository copy of it cannot be written by hand either. The checkout
	// is another repository's, which is what makes the repository resolvable —
	// the refusal has to be the account rule's and not "no provider here".
	dir := filepath.Join(t.TempDir(), "checkout-b")
	gitCheckout(t, dir, checkoutYAML("github", "api-key", initProjectName, ""))
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/owner/repo-b.git")
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "fugaro.yaml")
	t.Chdir(dir)
	_, _, err := executeStdin(t, "", "secrets", "set", config.ClaudeAccountSecret("team-a"), "--repo", "owner/repo-b")
	if err == nil || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("secrets set %s --repo owner/repo-b: err = %v, want the refusal that names claude_accounts (an empty stdin proves the value was never read)", config.ClaudeAccountSecret("team-a"), err)
	}
	if len(writes.created) != 1 || len(writes.added) != 1 {
		t.Errorf("the refused store wrote anyway: %v (created), %v (versions)", writes.created, writes.added)
	}
}

// TestAccountsAddReplacesTheTokenOnRerun is spec §2.2 step 1: re-running for a
// name this project already defines is a rotation. The new value is a second
// version of the ONE secret that name owns, the job that mounts it is the same
// one job, and the lists the first run wrote are not written twice — a second
// entry in claude_accounts.<name>.allow_data_to or in a profile's
// claude_accounts is a config the next publish refuses.
func TestAccountsAddReplacesTheTokenOnRerun(t *testing.T) {
	r := newAddRig(t)
	writes := r.secretWrites(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")

	rotated := strings.Repeat("m", 48)
	out, _, err := executeStdin(t, rotated+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--yes")
	if err != nil {
		t.Fatalf("the second run: %v\n%s", err, out)
	}

	id := gcp.ProjectSecretID(initProjectName, config.ClaudeAccountSecret("team-a"))
	if got := string(r.sm.Latest(id)); got != rotated {
		t.Errorf("%s holds %q after the rotation, want the token the second run read", id, got)
	}
	if want := []string{id, id}; !slices.Equal(writes.added, want) {
		t.Errorf("the versions were added to %v, want %v: a rotation is a new version of the account's own secret", writes.added, want)
	}
	if want := []string{id}; !slices.Equal(writes.created, want) {
		t.Errorf("the runs created %v, want %v: the second run is a version of the secret that is there, never a second secret", writes.created, want)
	}
	if !strings.Contains(accountsAddRow(t, out, "account"), "project-wide, version 2") {
		t.Errorf("the account line reads %q, and the rotation is version 2 of that secret", accountsAddRow(t, out, "account"))
	}

	// One job, and it mounts the account's secret and no other's.
	jobs := r.projectJobs(t)
	job, ok := jobs[gcp.ProjectJobName("standard", "github", "team-a")]
	if !ok {
		t.Fatalf("the job set has no standard/github/team-a after the rotation: jobs = %v", slices.Sorted(maps.Keys(jobs)))
	}
	secret := config.ClaudeAccountSecret("team-a")
	if got := job.SecretEnv[config.ReservedSecrets[config.ClaudeOAuthToken]]; got != secret {
		t.Errorf("standard/github/team-a mounts %q as the Claude token, want %q", got, secret)
	}
	mounting := 0
	for _, j := range jobs {
		for _, mounted := range j.SecretEnv {
			if mounted == secret {
				mounting++
			}
		}
	}
	if mounting != 1 {
		t.Errorf("%d jobs mount %s, want exactly one: one account, one job", mounting, secret)
	}

	lc := r.addLocal(t)
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-a"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v after the rotation, want one entry: re-declaring an account adds to the list it has", got)
	}
	if got := lc.Profiles["standard"].ClaudeAccounts; !slices.Equal(got, []string{"team-a"}) {
		t.Errorf("profiles.standard.claude_accounts = %v after the rotation, want one entry", got)
	}
	// A rotation of a name this project already declares changes no key, so the
	// file is not rewritten at all: the second run reads the config, stores the
	// value and converges. (The confirmation's own rotation sentence, which a
	// widening re-run prints, is A1's — accounts_add_test.go.)
	if !strings.Contains(out, ": no change") {
		t.Errorf("the second run rewrote the config although the declaration in it is the one that is there:\n%s", out)
	}
}

// TestAccountsAddClearsTheBrokenMark is spec §2.2 step 6: a 401 or a usage
// limit left a hold on the account, and the token has just been replaced, so
// the command clears it — the same remedy `fugaro accounts clear` is, and the
// one `fugaro doctor` prescribes for exactly this mark.
//
// The negative half is the other states' rule (C1.12): a backed-off mark is a
// probe's to clear, so it is left alone with the sentence that says so and the
// command that does mean it. A clear that rounded that direction would route
// real work to a credential that has not answered since it was held.
func TestAccountsAddClearsTheBrokenMark(t *testing.T) {
	broken := newAddRig(t)
	broken.db.Set(credsMarkPath("team-a"), map[string]any{"state": "broken", "since": "2026-10-01T00:00:00Z"})
	out, _ := broken.add(t, "team-a", "--repos", "owner/repo-a")
	if got := broken.markState(t, "team-a"); got != string(creds.StateAvailable) {
		t.Errorf("the mark is %q after the command, want %q: the account's token was just replaced, and this is the remedy for the hold it was under", got, creds.StateAvailable)
	}
	if row := accountsAddRow(t, out, "mark"); !strings.Contains(row, "cleared (was broken)") {
		t.Errorf("the mark line reads %q, and the state it cleared is the database's own fact (internal/creds read the mark back)", row)
	}

	backed := newAddRig(t)
	backed.db.Set(credsMarkPath("team-a"), map[string]any{"state": "backed_off"})
	out, _ = backed.add(t, "team-a", "--repos", "owner/repo-a")
	if got := backed.markState(t, "team-a"); got != string(creds.StateBackedOff) {
		t.Errorf("the bare-backed-off mark is %q after the command, want it left alone: a backed-off credential waits for a probe", got)
	}
	if row := accountsAddRow(t, out, "mark"); !strings.Contains(row, "left as it was: backed off") {
		t.Errorf("the mark line reads %q, want the sentence that says the hold stands", row)
	}
	if !strings.Contains(out, "fugaro accounts clear team-a") {
		t.Errorf("the note never names the command that does mean it:\n%s", out)
	}
}

// TestAccountsAddNamesTheRepositoriesItCouldNotConverge is D865's corner: the
// command runs in ONE checkout, so a --repos repository whose own checkout is
// elsewhere has its installation half done (the job exists and serves it) and
// its repository root not converged. The owner is told, by name, rather than
// left to discover it — and the checkout the command runs in is not named,
// because the Repository stage converged it.
func TestAccountsAddNamesTheRepositoriesItCouldNotConverge(t *testing.T) {
	r := newAddRig(t)
	dir := filepath.Join(t.TempDir(), "checkout")
	gitCheckout(t, dir, checkoutYAML("github", "api-key", initProjectName, ""))
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/owner/repo-a.git")
	t.Chdir(dir)

	out, _ := r.add(t, "team-a", "--repos", "owner/repo-a,owner/repo-b")

	if !strings.Contains(out, "owner/repo-b has no checkout here, so its own repository root was not converged") {
		t.Errorf("the command does not name the repository it could not converge:\n%s", out)
	}
	if !strings.Contains(out, "fugaro update") {
		t.Errorf("the note does not say what converges that repository:\n%s", out)
	}
	if strings.Contains(out, "owner/repo-a has no checkout here") {
		t.Errorf("the command names owner/repo-a, which is the checkout it runs in and whose repository root the Repository stage converged:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "fugaro run") {
		t.Errorf("the command's last line is %q, and a repository that needs an update in another checkout does not stop the run that is ready here", last)
	}
}

// TestAccountsAddReportNamesTheListTheAccountEndsUpWith: a rotation re-declares
// an account that already exists, and its allow_data_to is the list it had plus
// the repositories this run adds — the declare never replaces the list. The
// block's repos line is that list, read back from the config the command just
// wrote and not from the --repos it was given: printing the flag would print a
// shorter list than the file now holds, which is the one thing a line that
// names claude_accounts.<name>.allow_data_to may not do.
func TestAccountsAddReportNamesTheListTheAccountEndsUpWith(t *testing.T) {
	// The account is already there, reaching one repository, and the profile
	// serves it: the shape every rotation starts from.
	r := newAddRigWith(t, addRigLocal+"claude_accounts:\n  team-a:\n    allow_data_to:\n      - owner/repo-a\n")
	out, _ := r.add(t, "team-a", "--repos", "owner/repo-b")

	lc := r.addLocal(t)
	want := []string{"owner/repo-a", "owner/repo-b"}
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, want) {
		t.Fatalf("claude_accounts.team-a.allow_data_to = %v, want %v: a rotation merges the list", got, want)
	}
	line := accountsAddRow(t, out, "repos")
	if !strings.Contains(line, strings.Join(want, ", ")) {
		t.Errorf("the repos line is %q, want the list the account ends up with (%s)", line, strings.Join(want, ", "))
	}
	// And the account the node carries reaches both: the line the command reads
	// back is the same list the publish derived.
	if acc, ok := r.policy(t).Accounts["team-a"]; !ok {
		t.Errorf("the published node names no account team-a")
	} else if want := []string{"github/owner/repo-a", "github/owner/repo-b"}; !slices.Equal(acc.AllowRepos, want) {
		t.Errorf("the published accounts.team-a.allow_repos = %v, want %v", acc.AllowRepos, want)
	}
}

// twoProfilesLocal is the fixture of the profile the flag resolved: two
// profiles (standard, which the project requires, and zeta), each writing its
// own allow_repos and each able to serve team-a. The alphabetically first of
// them already lists the account, which is the state a project is
// in when a second profile takes the account on — and the only state in which
// reading the profile back off the file can name a profile this run was not
// about.
const twoProfilesLocal = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/owner/repo-a
    claude_accounts:
      - team-a
  zeta:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/owner/repo-a
claude_accounts:
  team-a:
    allow_data_to:
      - owner/repo-a
repos:
  owner/repo-a: { provider: github, github_app_id: "12345", workflows: [web] }
`

// TestAccountsAddReportNamesTheProfileTheRunResolved is F5: --profile is what
// decides whose claude_accounts gains the name and whose job the confirmation
// names, and every line of the block has to be about that profile. Reading the
// served profile back off the file instead answers a different question — "which
// profile lists this account" — and it has no single answer once two profiles
// list it: the block then names the alphabetically first of them, a profile this
// run's confirmation never mentioned and whose job this run did not deploy.
func TestAccountsAddReportNamesTheProfileTheRunResolved(t *testing.T) {
	r := newAddRigWith(t, twoProfilesLocal)
	out, _ := r.add(t, "team-a", "--repos", "owner/repo-a", "--profile", "zeta")

	// Both profiles carry the account after the edit, which is what makes the
	// answer undecidable from the file alone.
	lc := r.addLocal(t)
	for _, p := range []string{"standard", "zeta"} {
		if !slices.Contains(lc.Profiles[p].ClaudeAccounts, "team-a") {
			t.Fatalf("profiles.%s.claude_accounts = %v, want team-a listed", p, lc.Profiles[p].ClaudeAccounts)
		}
	}

	if line := accountsAddRow(t, out, "profile"); !strings.Contains(line, "zeta") || strings.Contains(line, "standard") {
		t.Errorf("the profile line is %q, want the --profile this run was about (zeta)", line)
	}
	// The deploy line is the same profile's, for the same reason: the job this
	// run wrote is zeta's, and standard's job was not deployed by this run.
	job := gcp.ProjectJobName("zeta", "github", "team-a")
	if line := accountsAddRow(t, out, "deploy"); !strings.Contains(line, job) {
		t.Errorf("the deploy line is %q, want the job of the profile this run was about (%s)", line, job)
	}
	if other := gcp.ProjectJobName("standard", "github", "team-a"); strings.Contains(out, other) {
		t.Errorf("the block names %s, and this run's --profile was zeta:\n%s", other, out)
	}
}

// TestAccountsAddFailsWhenAStageIsLeftForTheOwner is F1: a stage that ends
// NeedsYou — here the Repository stage of a checkout this project does not list,
// so onboarding it is a person's job — is not a converged installation. The loop
// records it in Left and blocks the Policy stage behind it, so the node never
// carries the account, and a command that read only res.Failed reported the run
// as ready: exit 0, "Nothing else to run", and a run admitted by a node that
// names no account. The one-line user error is the same one `fugaro update` ends
// on (init_inputs.go), because the last line of this command is only true when
// the run it names is ready.
//
// The Repository stage is the one that stops: the checkout's working tree holds
// a fugaro.yaml that is not the one on its default branch, which the engine
// reads and this stage will not settle for the owner. The Policy stage is then
// blocked behind it, and both the NeedsYou reason and the Blocked stages are in
// the Left and the stage list the run reports.
func TestAccountsAddFailsWhenAStageIsLeftForTheOwner(t *testing.T) {
	r := newAddRigWith(t, addRigLocal)
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(checkoutYAML("github", "oauth", "aurora", "")+"# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--yes")
	if err == nil {
		t.Fatalf("the command exited 0 with a stage left for the owner:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "git switch -- main") {
		t.Errorf("the refusal does not carry the stage's own reason: %v", err)
	}
	if strings.Contains(out, "Nothing else to run") {
		t.Errorf("the failed command still ended on its ready line:\n%s", out)
	}
	// The publish never ran, and that is the fact the ready line would have
	// contradicted: the node every run of the project is admitted by carries no
	// account.
	if names := r.policy(t).AccountNames(); len(names) != 0 {
		t.Errorf("the published node names %v, and the Policy stage never ran", names)
	}
}

// TestAccountsAddDeployLineOnlyClaimsADeploymentForChanged is F2: the deploy row
// names the job this run wrote, so it says `+ <job>` for the one state that
// applied — Changed — and for no other. Every other state is the stage's own
// answer, in its own words: a job named over needs-you or blocked claims a
// deployment that did not happen, and the last line of the command then invites
// the owner to start a run on a job that is not there.
func TestAccountsAddDeployLineOnlyClaimsADeploymentForChanged(t *testing.T) {
	lc := &localcfg.Config{Profiles: map[string]config.JobProfile{"standard": {Provider: "github"}}}
	job := gcp.ProjectJobName("standard", "github", "team-a")
	line := func(state initflow.State, detail string) string {
		return accountsAddDeployLine(lc, "standard", "team-a", initflow.StageResult{Name: initflow.Installation, State: state, Detail: detail})
	}

	got := line(initflow.Changed, "deployed the job set")
	if !strings.HasPrefix(got, "+ ") || !strings.Contains(got, job) {
		t.Errorf("changed: %q, want the + <job> line naming %s", got, job)
	}
	for _, tc := range []struct {
		state  initflow.State
		detail string
	}{
		{initflow.NeedsYou, "type the project's name at each apply"},
		{initflow.Blocked, "waits for Repository"},
		{initflow.BillingPending, "the billing account is not active yet"},
		{initflow.Done, "the job set is current"},
		{initflow.Skipped, "no budget backend: the stage does not apply"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			got := line(tc.state, tc.detail)
			if contains := strings.Contains(got, job); contains {
				t.Errorf("%s: %q names the job, and the stage did not deploy it", tc.state, got)
			}
			if !strings.HasPrefix(got, string(tc.state)+": ") {
				t.Errorf("%s: %q, want the stage's own state and reason", tc.state, got)
			}
			if !strings.Contains(got, tc.detail) {
				t.Errorf("%s: %q does not carry the stage's reason %q", tc.state, got, tc.detail)
			}
		})
	}
}
