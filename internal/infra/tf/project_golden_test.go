package tf

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	// Aliased: this package's own tests already declare a local helper named
	// config (managed_apply_test.go), so the import cannot take that name.
	fugaroconfig "github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
)

// installationFixture is the installation root's golden tfvars (the one
// scripts/gen-golden-plans.sh plans): the local config's profiles, and
// nothing of any repository — the installation root never sees one.
type installationFixture struct {
	RunsBucket     string                       `json:"runs_bucket"`
	Project        string                       `json:"project"`
	ProjectSecrets map[string]string            `json:"project_secrets"`
	ProjectJobs    map[string]projectJobFixture `json:"project_jobs"`
}

// projectJobFixture is one project_jobs entry, as the fixture writes it.
type projectJobFixture struct {
	Profile        string `json:"profile"`
	Provider       string `json:"provider"`
	Account        string `json:"account"`
	Job            string `json:"job"`
	ServiceAccount struct {
		AccountID string `json:"account_id"`
	} `json:"service_account"`
	BucketCondition struct {
		Title      string `json:"title"`
		Expression string `json:"expression"`
	} `json:"bucket_condition"`
	TokenCondition struct {
		Title      string `json:"title"`
		Expression string `json:"expression"`
	} `json:"token_condition"`
	Env map[string]string `json:"env"`
	// SecretEnv is the profile's mounts, by variable name, plus the account's
	// token on the job that serves it: the logical secret name, which
	// project_secrets resolves to the container Terraform deploys.
	SecretEnv map[string]string `json:"secret_env"`
}

func installationGoldenFixture(t *testing.T) installationFixture {
	t.Helper()
	return readInstallationFixture(t, "installation-budget-job.tfvars.json")
}

// accountsGoldenFixture is the account axis's tfvars (the fixture
// TestFirebaseTfvarsGolden writes with projectAccountJobSpec).
func accountsGoldenFixture(t *testing.T) installationFixture {
	t.Helper()
	return readInstallationFixture(t, "installation-accounts.tfvars.json")
}

func readInstallationFixture(t *testing.T, name string) installationFixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var f installationFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	// The installation's tfvars carries no repository, so nothing the plan
	// creates can be a function of how many there are (plan step 6; the
	// repository-independence of the job set itself is
	// TestProjectJobsDoNotFollowTheRepositories in internal/infra).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"repos", "repo", "workflows"} {
		if _, ok := raw[k]; ok {
			t.Errorf("the installation fixture carries %q, which is a repository's", k)
		}
	}
	return f
}

// resource finds the plan's change of that address.
func resource(t *testing.T, p *Plan, addr string) ResourceChange {
	t.Helper()
	for _, rc := range p.ResourceChanges {
		if rc.Address == addr {
			return rc
		}
	}
	t.Fatalf("%s is not in the golden plan", addr)
	return ResourceChange{}
}

// The resource set the whole phase rests on (plan task 3.2 step 5): one job
// and one job service account per (profile, provider, account), three bucket
// conditions, and no per-slug condition.
func TestGoldenInstallationHasOneProjectJobPerTriple(t *testing.T) {
	f := installationGoldenFixture(t)
	if len(f.ProjectJobs) == 0 {
		t.Fatalf("the installation golden declares no project job: the fixture must carry the local config's profiles and internal/infra/tf/testdata/golden/installation.plan.json must be regenerated (scripts/gen-golden-plans.sh)")
	}
	p := goldenPlan(t, "installation")

	// One job, one account, one grant per entry of the fixture, and the
	// entries are exactly the (profile, provider, account) triples: a job the
	// fixture does not name would be a second one for a triple, and a missing
	// one would leave a profile with nothing to run on.
	triples := map[string]string{}
	accounts := map[string]string{}
	for name, j := range f.ProjectJobs {
		triple := j.Profile + "/" + j.Provider + "/" + j.Account
		if prev, dup := triples[triple]; dup {
			t.Errorf("the fixture has two jobs for %s: %s and %s", triple, prev, name)
		}
		triples[triple] = name
		if name != j.Job || name != gcp.ProjectJobName(j.Profile, j.Provider, j.Account) {
			t.Errorf("the fixture keys %s, whose job is %s", name, j.Job)
		}
		if prev, dup := accounts[j.ServiceAccount.AccountID]; dup {
			t.Errorf("jobs %s and %s share the service account %s", prev, name, j.ServiceAccount.AccountID)
		}
		accounts[j.ServiceAccount.AccountID] = name

		mod := `module.project["` + name + `"]`
		job := resource(t, p, mod+".google_cloud_run_v2_job.this")
		if got := str(job.Change.After["name"]); got != name {
			t.Errorf("%s creates the job %q", mod, got)
		}
		if job.Change.After["deletion_protection"] != true {
			t.Errorf("%s does not protect the job from deletion", mod)
		}
		sa := resource(t, p, mod+".google_service_account.job")
		if got := str(sa.Change.After["account_id"]); got != j.ServiceAccount.AccountID {
			t.Errorf("%s creates the account %q, want %q", mod, got, j.ServiceAccount.AccountID)
		}
		grant := resource(t, p, mod+".google_storage_bucket_iam_member.job")
		if got := str(grant.Change.After["bucket"]); got != f.RunsBucket {
			t.Errorf("%s grants on %q, want %s", mod, got, f.RunsBucket)
		}
		if got := str(grant.Change.After["role"]); got != "roles/storage.objectUser" {
			t.Errorf("%s grants %q", mod, got)
		}
	}

	// The service-account count is the profiles' and the accounts', not a
	// repository's: every project account in the plan belongs to exactly one
	// job of the fixture, and there are no others.
	//
	// Design §13 names the ceiling this claim is about — Google's default
	// service-account quota, about 100 per project, which today's model exceeds
	// at about 60 repositories and this one does not — and settling it is Check
	// 52 step 5 (U4), which the plan's Task 3.0 owns. The owner ran it on
	// 2026-10-16: 10 accounts in the sandbox project, ServiceAccountsPerProject
	// reported as -1 (no limit, UNVERIFIED) and 30 creates per minute
	// (docs/design/project-centric.md §4.1). This assertion stands on the
	// shape above, not on those numbers, which belong in the checklist.
	projectSAs := map[string]bool{}
	for _, rc := range p.ResourceChanges {
		if rc.Type == "google_service_account" && strings.HasPrefix(rc.Address, "module.project[") {
			projectSAs[str(rc.Change.After["account_id"])] = true
		}
	}
	if len(projectSAs) != len(f.ProjectJobs) {
		t.Errorf("the plan creates %d project accounts for %d jobs: %v", len(projectSAs), len(f.ProjectJobs), slices.Sorted(maps.Keys(projectSAs)))
	}
}

// jobEnvOf is a Cloud Run job resource's container environment by variable
// name, and its secret references by variable name: the two halves the golden
// plan writes (modules/project/job.tf's two dynamic env blocks).
func jobEnvOf(t *testing.T, job ResourceChange) (plain, secret map[string]string) {
	t.Helper()
	plain, secret = map[string]string{}, map[string]string{}
	tpl := objects(job.Change.After["template"])
	if len(tpl) != 1 {
		t.Fatalf("%s has %d templates, want one", job.Address, len(tpl))
	}
	for _, task := range objects(tpl[0]["template"]) {
		for _, c := range objects(task["containers"]) {
			for _, e := range objects(c["env"]) {
				name := str(e["name"])
				if name == "" {
					t.Errorf("%s sets an environment variable with no name: %v", job.Address, e)
					continue
				}
				if refs := objects(e["value_source"]); len(refs) == 1 {
					for _, r := range objects(refs[0]["secret_key_ref"]) {
						secret[name] = str(r["secret"])
						if v := str(e["value"]); v != "" {
							t.Errorf("%s sets %s both plainly (%q) and from a secret", job.Address, name, v)
						}
					}
					continue
				}
				plain[name] = str(e["value"])
			}
		}
	}
	return plain, secret
}

// jobSecretVersion is the version the job's mount of that variable resolves,
// and "" when the job mounts it as anything else.
func jobSecretVersion(job ResourceChange, name string) string {
	tpl := objects(job.Change.After["template"])
	if len(tpl) != 1 {
		return ""
	}
	for _, task := range objects(tpl[0]["template"]) {
		for _, c := range objects(task["containers"]) {
			for _, e := range objects(c["env"]) {
				if str(e["name"]) != name {
					continue
				}
				for _, ref := range objects(e["value_source"]) {
					for _, r := range objects(ref["secret_key_ref"]) {
						return str(r["version"])
					}
				}
			}
		}
	}
	return ""
}

// The owner's ruling of 2026-10-17 (plan task 3.5 step 4): a shared job on the
// github provider carries the GitHub App's ID in its environment — the ID is
// not a secret, and the runner's github provider refuses to build without it
// (internal/gitprov/providers), so a job that mounts the key and not the ID
// fails every run it starts. The key stays a Secret Manager secret, and it is
// mounted as one: no env value in the plan is key material.
//
// The fixture's own env is what internal/infra writes (ProjectJob.Env), and
// the plan is what Terraform makes of it, so both sides are checked: a value
// the Go code sets but the module drops would pass a fixture-only assertion.
func TestProjectJobCarriesTheGitHubAppID(t *testing.T) {
	f := installationGoldenFixture(t)
	if len(f.ProjectJobs) == 0 {
		t.Fatalf("the installation golden declares no project job: regenerate it as TestGoldenInstallationHasOneProjectJobPerTriple says")
	}
	p := goldenPlan(t, "installation")
	seen := 0
	for name, j := range f.ProjectJobs {
		if j.Provider != "github" {
			continue
		}
		seen++
		id, ok := j.Env["FUGARO_GITHUB_APP_ID"]
		if !ok || !appIDShape.MatchString(id) {
			t.Errorf("the fixture's job %s carries FUGARO_GITHUB_APP_ID=%q, want the App's ID (1 to 20 digits): the runner's github provider refuses to start without it", name, id)
		}
		mod := `module.project["` + name + `"]`
		plain, secret := jobEnvOf(t, resource(t, p, mod+".google_cloud_run_v2_job.this"))
		if got := plain["FUGARO_GITHUB_APP_ID"]; got != id {
			t.Errorf("%s deploys FUGARO_GITHUB_APP_ID=%q, want the fixture's %q", mod, got, id)
		}
		// The key is mounted from the project-wide container the installation
		// module names (fugaro-project-<fugaro project>-github-app-key-<hash>),
		// by reference — the ID is resolved at plan time and is not this
		// test's to spell.
		if got := secret["FUGARO_GITHUB_APP_PRIVATE_KEY"]; !strings.Contains(got, "github-app-key") {
			t.Errorf("%s mounts the App key from %q, want a github-app-key secret container", mod, got)
		}
		// No value anywhere in the job is key material: the key is a secret
		// reference and nothing else (design §12a item 7 — a key in a job's
		// plain environment is a key Cloud Run hands to the console and to
		// every reader of a job description).
		for k, v := range plain {
			if keyMaterial(v) {
				t.Errorf("%s sets %s to what looks like key material: the App's private key is a Secret Manager secret, never an environment value", mod, k)
			}
		}
	}
	if seen == 0 {
		t.Fatalf("the installation golden has no github project job, so this pins nothing: %v", f.ProjectJobs)
	}
}

// appIDShape is the GitHub App ID's shape (internal/infra's appIDRE and
// internal/gitprov's own check): 1 to 20 digits.
var appIDShape = regexp.MustCompile(`^[0-9]{1,20}$`)

// keyMaterial reports whether s looks like a private key rather than an ID, a
// URL or a path: the markers a PEM or a raw base64 body carries.
func keyMaterial(s string) bool {
	return strings.Contains(s, "PRIVATE KEY") || strings.Contains(s, "BEGIN RSA") ||
		strings.Contains(s, "BEGIN EC") || strings.Contains(s, "BEGIN OPENSSH") ||
		strings.Contains(s, "MIIE")
}

// The account axis's real plan (plan task 2; design §8, D5, D9): a
// profile serving a named Claude account deploys a job of its own, and the one
// token that job can read is its own account's — mounted as a secret reference
// on that job, and granted to that job's service account alone.
//
// Three properties, and this is the only test that reads them off a real plan
// of the real modules rather than a hand-written fixture:
//
//   - the mount: the account job's CLAUDE_CODE_OAUTH_TOKEN resolves to the
//     container the account's own name derives, and the no-account job's to
//     the project's default one (D5: the default account rides the job the
//     profile already had), so a run in either job presents its own account
//     and never the other's;
//   - the grant follows the mount: each job's service account holds
//     secretAccessor on exactly the containers its own secret_env names, and
//     on no other. That is the one-token-one-job-account property — a token
//     readable by two jobs is an account two jobs share — and it is the half
//     the tftest cannot show, because this is the plan of the whole module;
//   - and nothing the installation already runs is replaced. The rollout adds
//     the account's job — its scenario is the new golden
//     `installation-accounts.plan.json` — and six of the nine goldens that
//     already existed moved (relative to 0.8.x; the three firebase ones are
//     untouched), by additions and in-place updates only:
//     `google_storage_bucket_iam_member.job_tokens`, a read-only
//     `roles/storage.objectViewer` binding on the run's sealed budget token,
//     added to every job account (each workflow's job account on its own
//     `tokens/<slug>/` — `repo.plan.json`, `repo-adopt.plan.json`,
//     `repo-scope-move.plan.json` — and each project job account on the whole
//     `tokens/` prefix, in the three existing installation scenarios —
//     `installation.plan.json`, `installation-adopt.plan.json`,
//     `installation-managed.plan.json` — as in the new one); the runs bucket's
//     one-day `/budget-token` cleanup rule now matching `tokens/` beside
//     `runs/`; and each project job's environment gaining this release's
//     variables, `FUGARO_GITHUB_APP_ID` and `FUGARO_SECRET_ENVS` (D9). Every
//     one of those changes is an addition or an update in place: no resource is
//     replaced or deleted, and the goldens are regenerated in the same change,
//     so the generator's diff against them is empty.
func TestGoldenAccountJobMountsOnlyItsOwnToken(t *testing.T) {
	f := accountsGoldenFixture(t)
	if len(f.ProjectSecrets) == 0 {
		t.Fatalf("the accounts fixture carries no project_secrets: regenerate it with go test ./internal/infra -run TestFirebaseTfvarsGolden -update")
	}
	// The fixture is the account axis: the no-account job the default account
	// rides and the named account's own, and nothing else.
	byAccount := map[string]string{}
	for name, j := range f.ProjectJobs {
		if prev, dup := byAccount[j.Account]; dup {
			t.Fatalf("the fixture has two jobs for the account %q: %s and %s", j.Account, prev, name)
		}
		byAccount[j.Account] = name
	}
	defaultJob, accountJob := byAccount[""], byAccount["acme-personal"]
	if len(f.ProjectJobs) != 2 || defaultJob == "" || accountJob == "" {
		t.Fatalf("the accounts fixture's jobs are %v, want the no-account job and the acme-personal one", f.ProjectJobs)
	}

	p := goldenPlan(t, "installation-accounts")
	// The container each account's token is deployed as, named by the one
	// function that derives a secret name from an account: the mount is only
	// as good as the secret behind it, so a container the fixture does not
	// carry is a job that cannot start.
	container := map[string]string{} // account -> the token container its job mounts
	for _, account := range []string{"", "acme-personal"} {
		logical := fugaroconfig.ClaudeAccountSecret(account)
		want, ok := f.ProjectSecrets[logical]
		if !ok {
			t.Fatalf("project_secrets has no %q, so the account %q's token is not deployed: %v", logical, account, f.ProjectSecrets)
		}
		container[account] = want
	}
	for _, c := range []struct{ account, job string }{
		{"", defaultJob},
		{"acme-personal", accountJob},
	} {
		mod := `module.project["` + c.job + `"]`
		j := f.ProjectJobs[c.job]
		logical, want := fugaroconfig.ClaudeAccountSecret(c.account), container[c.account]
		varName := fugaroconfig.ReservedSecrets[fugaroconfig.ClaudeOAuthToken]

		// The mount, from the plan: the variable the runner reads, resolving
		// to that container at latest. The fixture's own secret_env is checked
		// too, because a value the Go code writes and the module drops would
		// pass a plan-only assertion.
		if got := j.SecretEnv[varName]; got != logical {
			t.Errorf("the fixture's job %s mounts %s from %q, want its account %q's own %q",
				c.job, varName, got, c.account, logical)
		}
		job := resource(t, p, mod+".google_cloud_run_v2_job.this")
		plain, secret := jobEnvOf(t, job)
		if got := secret[varName]; got != want {
			t.Errorf("%s mounts %s from %q, want the account %q's own container %q",
				mod, varName, got, c.account, want)
		}
		// At latest, so a rotation of the account's token reaches the job
		// without a plan: a pinned version would keep serving the token the
		// account has replaced.
		if got := jobSecretVersion(job, varName); got != "latest" {
			t.Errorf("%s mounts %s at version %q, want latest", mod, varName, got)
		}
		// And never another account's: the two containers are the two
		// accounts, and a job mounting the wrong one is a run presenting
		// somebody else's account.
		for _, other := range []string{"", "acme-personal"} {
			if other == c.account {
				continue
			}
			if got := secret[varName]; got == container[other] {
				t.Errorf("%s mounts %s from %q, which is the account %q's token", mod, varName, got, other)
			}
		}

		// And what the runner redacts is what the job mounts: the plan's two
		// halves of the same env — the variables resolved from a secret, and
		// the list runner.SecretEnvsVar names — are one list. A job whose
		// token is not in that list publishes it the moment a run prints its
		// environment, whatever that run's agent.auth is: an api-key or Vertex
		// run of a profile that lists the default account holds the token just
		// the same, and SECURITY.md's claim that the redactor masks what
		// Fugaro publishes is what this makes true.
		//
		// Mutation (run, restore): revert this golden to the plan generated
		// before ProjectJobs set the variable, and the deployed job carries no
		// list at all; the check fails on both jobs.
		wantRegistered := strings.Join(slices.Sorted(maps.Keys(secret)), ",")
		if got := plain[runner.SecretEnvsVar]; got != wantRegistered {
			t.Errorf("%s sets %s = %q, want %q: the variables this job mounts secrets as are what the runner registers with its redactor",
				mod, runner.SecretEnvsVar, got, wantRegistered)
		}

		// The grant: the module's secret grants are exactly the containers
		// this job mounts, each on the account's own member.
		wantMember := "serviceAccount:" + j.ServiceAccount.AccountID + "@" + f.Project + ".iam.gserviceaccount.com"
		wantGrants := map[string]bool{}
		for _, l := range j.SecretEnv {
			id, ok := f.ProjectSecrets[l]
			if !ok {
				t.Fatalf("the fixture's job %s mounts %q, which project_secrets does not name: %v", c.job, l, f.ProjectSecrets)
			}
			wantGrants[id] = true
		}
		gotGrants := map[string]string{}
		for _, rc := range p.ResourceChanges {
			if rc.Type != "google_secret_manager_secret_iam_member" || !strings.HasPrefix(rc.Address, mod+".") {
				continue
			}
			if !slices.Equal(rc.Change.Actions, []string{"create"}) {
				t.Errorf("%s: actions %v, want create", rc.Address, rc.Change.Actions)
			}
			if got := str(rc.Change.After["role"]); got != "roles/secretmanager.secretAccessor" {
				t.Errorf("%s grants %q, want read-only secretAccessor", rc.Address, got)
			}
			if got := str(rc.Change.After["member"]); got != wantMember {
				t.Errorf("%s grants to %q, want the job's own account %q", rc.Address, got, wantMember)
			}
			gotGrants[str(rc.Change.After["secret_id"])] = str(rc.Change.After["member"])
		}
		if got, want := slices.Sorted(maps.Keys(gotGrants)), slices.Sorted(maps.Keys(wantGrants)); !slices.Equal(got, want) {
			t.Errorf("%s's secret grants are on %v, want exactly the containers its own mounts name, %v", mod, got, want)
		}
	}

	// One token, one job, one account: the two jobs read different containers,
	// so neither can present the other's account, and each account's container
	// appears in exactly one module's grants.
	if container[""] == container["acme-personal"] {
		t.Errorf("both jobs mount the same token container %q: two accounts, one credential", container[""])
	}
	for _, c := range []struct{ account, job string }{{"", defaultJob}, {"acme-personal", accountJob}} {
		mod := `module.project["` + c.job + `"]`
		for _, rc := range p.ResourceChanges {
			if rc.Type != "google_secret_manager_secret_iam_member" || strings.HasPrefix(rc.Address, mod+".") {
				continue
			}
			if str(rc.Change.After["secret_id"]) == container[c.account] {
				t.Errorf("%s grants a member on the account %q's token %q: one token belongs to one job", rc.Address, c.account, container[c.account])
			}
		}
	}
}

// slugClause is a clause of the per-repository condition
// (gcp.BucketCondition): the repository's own prefix under one of the shared
// areas.
var slugClause = regexp.MustCompile(`/objects/(runs|cache|locks|builds)/[^/"]+/"\)`)

// The IAM assertion the phase turns on (plan task 3.2 step 2 and its red
// step): a project job's bucket grant is the widened condition — the three
// prefixes, byte for byte, and no slug anywhere — and no condition in the
// installation plan names a single repository.
func TestProjectJobBucketGrantsAreTheWidenedCondition(t *testing.T) {
	f := installationGoldenFixture(t)
	if len(f.ProjectJobs) == 0 {
		t.Fatalf("the installation golden declares no project job: regenerate it as TestGoldenInstallationHasOneProjectJobPerTriple says")
	}
	p := goldenPlan(t, "installation")
	want := gcp.ProjectBucketCondition(f.RunsBucket, gcp.JobBucketPrefixes)
	if clauses := strings.Split(want, " || "); len(clauses) != len(gcp.JobBucketPrefixes) {
		t.Fatalf("the widened condition has %d clauses, want %d", len(clauses), len(gcp.JobBucketPrefixes))
	}
	for name, j := range f.ProjectJobs {
		if j.BucketCondition.Expression != want {
			t.Errorf("the fixture's job %s carries %q, want the widened %q", name, j.BucketCondition.Expression, want)
		}
		mod := `module.project["` + name + `"]`
		conds := objects(resource(t, p, mod+".google_storage_bucket_iam_member.job").Change.After["condition"])
		if len(conds) != 1 {
			t.Fatalf("%s has %d conditions, want one", mod, len(conds))
		}
		got := str(conds[0]["expression"])
		if got != want {
			t.Errorf("%s grants %q,\nwant the widened condition %q", mod, got, want)
		}
		if tt := str(conds[0]["title"]); tt != gcp.BucketConditionTitle(j.ServiceAccount.AccountID) {
			t.Errorf("%s titles its condition %q, want %q", mod, tt, gcp.BucketConditionTitle(j.ServiceAccount.AccountID))
		}
		// No clause may name a repository: the grant is the whole prefix.
		for _, clause := range strings.Split(got, " || ") {
			if slugClause.MatchString(clause) {
				t.Errorf("%s grants %q, which names a single repository", mod, clause)
			}
		}
	}
	// And nothing else in the installation plan is conditioned on a slug: the
	// per-repository path is the repository root's, not this one's.
	for _, rc := range p.ResourceChanges {
		if rc.Type != "google_storage_bucket_iam_member" {
			continue
		}
		for _, cond := range objects(rc.Change.After["condition"]) {
			if expr := str(cond["expression"]); slugClause.MatchString(expr) {
				t.Errorf("%s carries the per-slug condition %q", rc.Address, expr)
			}
		}
	}
	// The review screen accepts what the modules write, and only that. Three
	// cases, because the widened condition is the one grant the operator's
	// single confirmation covers without a per-clause comparison:
	//
	//   - the project module's own grant, whose member the module computes as
	//     its account's member, is covered;
	//   - the same condition for an account the plan does not create is not;
	//   - and the same condition written into a *repository's* workflow module
	//     is not, in either form of the member: that module creates its own job
	//     account (modules/workflow/sa.tf), and a per-repository job account is
	//     fugaro-<readable>-<8 hex> just as a project job's is, so the module
	//     path is what tells the two apart. Accepting it would let one
	//     confirmation widen a per-repository job's grant to the whole bucket.
	saID := gcp.ProjectServiceAccountID("standard", "github", "")
	mod := `module.project["` + gcp.ProjectJobName("standard", "github", "") + `"]`
	created := inModule(mod, "google_service_account", map[string]any{"account_id": saID}, map[string]any{}, "create")
	grant := func(mod, id string) ResourceChange {
		return inModule(mod, "google_storage_bucket_iam_member", map[string]any{
			"bucket": f.RunsBucket, "role": "roles/storage.objectUser",
			"condition": []any{map[string]any{"title": gcp.BucketConditionTitle(id), "expression": want, "description": nil}},
		}, map[string]any{"member": true}, "create")
	}
	if got := goldenCover().NotCovered(&Plan{ResourceChanges: []ResourceChange{created, grant(mod, saID)}}, nil); len(got) != 0 {
		t.Errorf("a project job's widened grant is not covered: %q", got)
	}
	stranger := inModule(mod, "google_service_account", map[string]any{"account_id": "fugaro-p-other-github-0123456789ab"}, map[string]any{}, "create")
	if got := goldenCover().NotCovered(&Plan{ResourceChanges: []ResourceChange{stranger, grant(mod, saID)}}, nil); len(got) == 0 {
		t.Error("a widened grant whose title names no account the plan creates is covered")
	}

	// The named-member form in the project module (an update plan, where the
	// account already exists): covered only for the account that module
	// itself holds, which an update plan lists as a no-op. Another Fugaro job
	// account of the run's project — a second profile's, or a repository's —
	// is not the module's own, so the widened grant is not covered for it.
	namedIn := func(mod, id string) ResourceChange {
		return inModule(mod, "google_storage_bucket_iam_member", map[string]any{
			"bucket": f.RunsBucket, "role": "roles/storage.objectUser", "member": "serviceAccount:" + id + "@" + goldenCover().Projects[0] + ".iam.gserviceaccount.com",
			"condition": []any{map[string]any{"title": gcp.BucketConditionTitle(id), "expression": want, "description": nil}},
		}, map[string]any{}, "update")
	}
	held := inModule(mod, "google_service_account", map[string]any{"account_id": saID}, map[string]any{}, "no-op")
	if got := goldenCover().NotCovered(&Plan{ResourceChanges: []ResourceChange{held, namedIn(mod, saID)}}, nil); len(got) != 0 {
		t.Errorf("a project job's widened grant naming the module's own account is not covered: %q", got)
	}
	otherSA := gcp.ProjectServiceAccountID("heavy", "github", "")
	if got := goldenCover().NotCovered(&Plan{ResourceChanges: []ResourceChange{held, namedIn(mod, otherSA)}}, nil); !refusedTheGrant(got, mod+".google_storage_bucket_iam_member.x") {
		t.Errorf("a widened grant in %s naming another module's account %s is covered\nrefused: %q", mod, otherSA, got)
	}

	// A per-repository job's own module: the account is created there, so
	// without the module path the computed-member form would be covered.
	repoMod := `module.repo.module.workflow["api"]`
	repoSA := gcp.ServiceAccountID("bitbucket-acme-app", "api")
	repoCreated := inModule(repoMod, "google_service_account", map[string]any{"account_id": repoSA}, map[string]any{}, "create")
	if got := goldenCover().NotCovered(&Plan{ResourceChanges: []ResourceChange{repoCreated, grant(repoMod, repoSA)}}, nil); !refusedTheGrant(got, repoMod+".google_storage_bucket_iam_member.x") {
		t.Errorf("a per-repository job account's grant, widened to the whole prefix, is covered: %s is a workflow module's grant, not the project module's\nrefused: %q", repoMod, got)
	}
	// The named-member form: an update plan, where the account already exists.
	named := inModule(repoMod, "google_storage_bucket_iam_member", map[string]any{
		"bucket": f.RunsBucket, "role": "roles/storage.objectUser", "member": "serviceAccount:" + repoSA + "@" + goldenCover().Projects[0] + ".iam.gserviceaccount.com",
		"condition": []any{map[string]any{"title": gcp.BucketConditionTitle(repoSA), "expression": want, "description": nil}},
	}, map[string]any{}, "create")
	if got := goldenCover().NotCovered(&Plan{ResourceChanges: []ResourceChange{named}}, nil); !refusedTheGrant(got, repoMod+".google_storage_bucket_iam_member.x") {
		t.Errorf("a per-repository job account's widened grant is covered when the grant names its member\nrefused: %q", got)
	}
}

// The other half of the project job's pair (plan task 3.2 step 2;
// docs/design/budget-token-channel.md §7.2): the sealed budget token lives
// under tokens/, which the widened objectUser grant never covers, so the
// shared job account carries a second, read-only grant on the whole of
// tokens/. Without it a project job's runs that carry a budget token stop at
// bootstrap, reading their own sealed token as a missing object.
func TestProjectJobTokenGrantsAreTheWidenedTokensRead(t *testing.T) {
	f := installationGoldenFixture(t)
	if len(f.ProjectJobs) == 0 {
		t.Fatalf("the installation golden declares no project job: regenerate it as TestGoldenInstallationHasOneProjectJobPerTriple says")
	}
	p := goldenPlan(t, "installation")
	want := gcp.ProjectTokenBucketCondition(f.RunsBucket)
	for name, j := range f.ProjectJobs {
		// The fixture is what fugaro update writes (internal/infra.ProjectJobs):
		// the same widening the module's grant carries, from the Go side.
		if j.TokenCondition.Expression != want {
			t.Errorf("the fixture's job %s carries %q, want the widened %q", name, j.TokenCondition.Expression, want)
		}
		if w := gcp.ProjectTokenBucketConditionTitle(j.ServiceAccount.AccountID); j.TokenCondition.Title != w {
			t.Errorf("the fixture's job %s titles its tokens condition %q, want %q", name, j.TokenCondition.Title, w)
		}
		mod := `module.project["` + name + `"]`
		rc := resource(t, p, mod+".google_storage_bucket_iam_member.job_tokens")
		if got := str(rc.Change.After["bucket"]); got != f.RunsBucket {
			t.Errorf("%s grants on %q, want %s", mod, got, f.RunsBucket)
		}
		// Read-only, and that is what keeps the widening bounded: objectViewer
		// carries no create, no delete and no update, so the account can read a
		// queued run's sealed token and can neither replace nor remove it.
		// objectUser here would be a write on the one prefix the seal moved out
		// of runs/ so that a job could not write it.
		if got := str(rc.Change.After["role"]); got != "roles/storage.objectViewer" {
			t.Errorf("%s grants %q on tokens/, want roles/storage.objectViewer", mod, got)
		}
		conds := objects(rc.Change.After["condition"])
		if len(conds) != 1 {
			t.Fatalf("%s has %d conditions on its tokens grant, want one", mod, len(conds))
		}
		if got := str(conds[0]["expression"]); got != want {
			t.Errorf("%s grants %q,\nwant the widened tokens condition %q", mod, got, want)
		}
		if tt := str(conds[0]["title"]); tt != gcp.ProjectTokenBucketConditionTitle(j.ServiceAccount.AccountID) {
			t.Errorf("%s titles its tokens condition %q, want %q", mod, tt, gcp.ProjectTokenBucketConditionTitle(j.ServiceAccount.AccountID))
		}
		if d := str(conds[0]["description"]); d != "" {
			t.Errorf("%s describes its tokens condition %q, which the module never sets", mod, d)
		}
		// The two grants on one account do not overlap: what the account may
		// write (runs/, cache/, locks/) and what it may read (tokens/) are
		// disjoint, which is the whole point of moving the token out of runs/.
		obj := objects(resource(t, p, mod+".google_storage_bucket_iam_member.job").Change.After["condition"])
		if len(obj) != 1 {
			t.Fatalf("%s has %d conditions on its objectUser grant, want one", mod, len(obj))
		}
		if expr := str(obj[0]["expression"]); strings.Contains(expr, "/"+gcp.TokenBucketPrefix+"/") {
			t.Errorf("%s's objectUser grant reaches tokens/: %q", mod, expr)
		}
	}
	// The review screen accepts the pair, and only the pair: the tokens grant
	// is covered for the module's own account, refused as a write, refused
	// when another account's title names it, and refused when a
	// per-repository workflow module writes the same expression — that
	// module's account serves one repository and must name its own prefix.
	saID := gcp.ProjectServiceAccountID("standard", "github", "")
	mod := `module.project["` + gcp.ProjectJobName("standard", "github", "") + `"]`
	created := inModule(mod, "google_service_account", map[string]any{"account_id": saID}, map[string]any{}, "create")
	tokenGrant := func(mod, id, role string) ResourceChange {
		return inModule(mod, "google_storage_bucket_iam_member", map[string]any{
			"bucket": f.RunsBucket, "role": role,
			"condition": []any{map[string]any{"title": gcp.ProjectTokenBucketConditionTitle(id), "expression": want, "description": nil}},
		}, map[string]any{"member": true}, "create")
	}
	if got := goldenCover().NotCovered(&Plan{ResourceChanges: []ResourceChange{created, tokenGrant(mod, saID, "roles/storage.objectViewer")}}, nil); len(got) != 0 {
		t.Errorf("a project job's tokens grant is not covered: %q", got)
	}
	if got := goldenCover().NotCovered(&Plan{ResourceChanges: []ResourceChange{created, tokenGrant(mod, saID, "roles/storage.objectUser")}}, nil); len(got) == 0 {
		t.Error("a project job's tokens grant is covered as objectUser: a write on the sealed token objects")
	}
	// A title naming no account this plan creates: the grant would be for an
	// account nothing here says exists.
	stranger := inModule(mod, "google_service_account", map[string]any{"account_id": "fugaro-p-other-github-0123456789ab"}, map[string]any{}, "create")
	if got := goldenCover().NotCovered(&Plan{ResourceChanges: []ResourceChange{stranger, tokenGrant(mod, saID, "roles/storage.objectViewer")}}, nil); len(got) == 0 {
		t.Error("a project job's tokens grant whose title names no account the plan creates is covered")
	}
	repoMod := `module.repo.module.workflow["api"]`
	repoSA := gcp.ServiceAccountID("bitbucket-acme-app", "api")
	repoCreated := inModule(repoMod, "google_service_account", map[string]any{"account_id": repoSA}, map[string]any{}, "create")
	if got := goldenCover().NotCovered(&Plan{ResourceChanges: []ResourceChange{repoCreated, tokenGrant(repoMod, repoSA, "roles/storage.objectViewer")}}, nil); len(got) == 0 {
		t.Errorf("a per-repository workflow module's whole-tokens/ read is covered: %s serves one repository, and its token grant must name that repository's prefix", repoMod)
	}
}

// refusedTheGrant reports whether the grant at addr is what the review screen
// refused, and for the widened condition: a synthetic plan can be refused for
// other reasons, and an assertion that only counts entries would pass for the
// wrong one.
func refusedTheGrant(got []string, addr string) bool {
	for _, why := range got {
		if strings.HasPrefix(why, addr+" ") && strings.Contains(why, "not a Fugaro prefix condition") {
			return true
		}
	}
	return false
}

// inModule is rc (cover_test.go) with an explicit address, so a case can put a
// resource in the module the guard has to tell apart.
func inModule(mod, typ string, after, unk map[string]any, acts ...string) ResourceChange {
	return ResourceChange{Address: mod + "." + typ + ".x", Type: typ, Change: Change{Actions: acts, After: after, AfterUnknown: unk}}
}
