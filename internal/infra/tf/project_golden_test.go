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
)

// installationFixture is the installation root's golden tfvars (the one
// scripts/gen-golden-plans.sh plans): the local config's profiles, and
// nothing of any repository — the installation root never sees one.
type installationFixture struct {
	RunsBucket  string                       `json:"runs_bucket"`
	ProjectJobs map[string]projectJobFixture `json:"project_jobs"`
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
}

func installationGoldenFixture(t *testing.T) installationFixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "installation-budget-job.tfvars.json"))
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
