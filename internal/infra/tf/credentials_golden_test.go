package tf

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// credentialsFixture is the credentials case of the installation golden's
// tfvars (internal/infra/testdata/installation-credentials.tfvars.json, the
// one scripts/gen-golden-plans.sh plans for
// testdata/golden/installation-credentials.plan.json): the whole installation
// run with the credentials service ON, so the service's revision, its
// account, its bundle and its seven environment variables are in a real plan.
type credentialsFixture struct {
	FugaroProject string `json:"fugaro_project"`
	Project       string `json:"project"`
	Region        string `json:"region"`
	Credentials   struct {
		AccountID       string `json:"account_id"`
		Service         string `json:"service"`
		Image           string `json:"image"`
		BundleSecret    string `json:"bundle_secret"`
		FirebaseProject string `json:"firebase_project"`
		RTDBURL         string `json:"rtdb_url"`
		URL             string `json:"url"`
		JobAccounts     []struct {
			Provider string `json:"provider"`
			Repo     string `json:"repo"`
			Workflow string `json:"workflow"`
		} `json:"job_accounts"`
		VerifierAccounts []string `json:"verifier_accounts"`
	} `json:"credentials_service"`
	// Coordinator is the coordinator's opt-in (coordinator plan P5), which this
	// fixture names so that the service's tenth environment entry — the read
	// mint's second class (BB5), the account the sweep calls with — is on a real
	// plan and its value read back: only the account's ID and the project are
	// needed for that, so the job itself stays off (deploy_job false, which is
	// the state every installation's first apply plans).
	Coordinator struct {
		AccountID string `json:"account_id"`
	} `json:"coordinator"`
	// Operators are the installation's, not the service's: BB4's read mint
	// admits their user members, and the module renders them into the service's
	// own environment as bare addresses (FUGARO_PERSON_ACCOUNTS).
	Operators []string `json:"operators"`
}

// credentialsReadPeople is the admitted people as the module derives them from
// the fixture's operators: the user members with their IAM spelling stripped,
// which is the address a Google ID token's email claim carries. This is the
// fixture's side of the derivation the module does in one local, so a module
// that rendered something else — a whole IAM member, or another spelling's
// address — fails here rather than admitting nobody at runtime.
func credentialsReadPeople(operators []string) []string {
	out := []string{}
	for _, m := range operators {
		if after, ok := strings.CutPrefix(m, "user:"); ok {
			out = append(out, after)
		}
	}
	return out
}

func credentialsGoldenFixture(t *testing.T) credentialsFixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "installation-credentials.tfvars.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f credentialsFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// credentialsServiceEnvOf is the credentials service's container environment
// as the plan writes it: variable name to literal value, plus the name of any
// entry that takes its value from a source instead (credentials.tf sets none,
// and the cover refuses one).
func credentialsServiceEnvOf(t *testing.T, rc ResourceChange) (plain map[string]string, fromSource []string) {
	t.Helper()
	plain = map[string]string{}
	tpl := objects(rc.Change.After["template"])
	if len(tpl) != 1 {
		t.Fatalf("%s has %d templates, want one", rc.Address, len(tpl))
	}
	cs := objects(tpl[0]["containers"])
	if len(cs) != 1 {
		t.Fatalf("%s has %d containers, want one", rc.Address, len(cs))
	}
	for _, e := range objects(cs[0]["env"]) {
		name := str(e["name"])
		if name == "" {
			t.Errorf("%s sets an environment variable with no name: %v", rc.Address, e)
			continue
		}
		if refs := objects(e["value_source"]); len(refs) > 0 {
			fromSource = append(fromSource, name)
			continue
		}
		plain[name] = str(e["value"])
	}
	slices.Sort(fromSource)
	return plain, fromSource
}

// The credentials service's environment variables, read off a real plan of the
// installation module against the fixture the plan was made from (C1.14 and
// C1.24): the seven the process REQUIRES — it refuses to bind a port unless
// every one of them is there and well formed (internal/cli's
// credentialsServiceConfig) — and the eighth, the verifier list, which this
// fixture names and which the process reads as an optional value (C3.9g1,
// D1068), the ninth, the read mint's admitted people (BB4), and the tenth, the
// read mint's coordinator class (BB5), which arrives with the installation's
// coordinator account. So a variable the Go side computes and the module drops
// — or one the module sets from a value the fixture does not carry — is a
// service that never answers a job, or a verify route that admits nobody, on an
// installation whose init reports success.
//
// The Terraform tftest (roots/installation/tests/installation.tftest.hcl)
// pins the same map against its own variable, and both halves are needed: the
// tftest never runs the Go code that decides what to write into the tfvars,
// and this test never exercises a `terraform test` run.
func TestGoldenCredentialsServiceCarriesItsNames(t *testing.T) {
	f := credentialsGoldenFixture(t)
	c := f.Credentials
	if len(c.JobAccounts) == 0 {
		t.Fatalf("the credentials fixture names no job account: the service's Config refuses an empty table, so this plan could not serve a single job: %v", f)
	}
	p := goldenPlan(t, "installation-credentials")
	rc := resource(t, p, "module.installation.google_cloud_run_v2_service.credentials[0]")

	jobAccounts, err := json.Marshal(c.JobAccounts)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.VerifierAccounts) == 0 {
		t.Fatalf("the credentials fixture names no verifier: this plan is the one that exercises the module's conditional eighth entry, and a fixture that names none could not tell it from the seven the module always sets: %v", f)
	}
	verifiers, err := json.Marshal(c.VerifierAccounts)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Operators) == 0 {
		t.Fatalf("the credentials fixture names no operator: this plan is the one that exercises the module's conditional read-mint entry, and a fixture that names none could not tell it from the seven the module always sets: %v", f)
	}
	people, err := json.Marshal(credentialsReadPeople(f.Operators))
	if err != nil {
		t.Fatal(err)
	}
	if f.Coordinator.AccountID == "" {
		t.Fatalf("the credentials fixture names no coordinator: this plan is the one that exercises the module's conditional tenth entry, and a fixture that names none could not tell it from the seven the module always sets: %v", f)
	}
	// The coordinator class's one address, spelled the way Google spells the
	// account the module creates from these two inputs — the same derivation
	// the service's own revision runs as (below).
	coordinator := f.Coordinator.AccountID + "@" + f.Project + ".iam.gserviceaccount.com"
	want := map[string]string{
		"FUGARO_PROJECT":                 f.FugaroProject,
		"FUGARO_GCP_PROJECT":             f.Project,
		"FUGARO_REGION":                  f.Region,
		"FUGARO_RTDB_URL":                c.RTDBURL,
		"FUGARO_FIREBASE_PROJECT":        c.FirebaseProject,
		"FUGARO_CREDENTIALS_SERVICE_URL": c.URL,
		"FUGARO_JOB_ACCOUNTS":            string(jobAccounts),
		"FUGARO_VERIFIER_ACCOUNTS":       string(verifiers),
		"FUGARO_PERSON_ACCOUNTS":         string(people),
		"FUGARO_COORDINATOR_ACCOUNT":     coordinator,
	}
	// The map's keys are the allow-list the cover holds the service to: a name
	// here that the allow-list does not carry would make this test pass on an
	// environment every run refuses.
	names := slices.Sorted(maps.Keys(want))
	allowed := slices.Clone(credentialsServiceEnv)
	slices.Sort(allowed)
	if !slices.Equal(names, allowed) {
		t.Fatalf("this test spells %v as the service's environment, the cover allows %v", names, credentialsServiceEnv)
	}

	got, fromSource := credentialsServiceEnvOf(t, rc)
	if len(fromSource) != 0 {
		t.Errorf("%s takes %v from a source rather than the module: the service's environment is the module's own names and nothing else", rc.Address, fromSource)
	}
	if !maps.Equal(got, want) {
		t.Errorf("%s deploys the environment\n%v\nwant\n%v", rc.Address, got, want)
	}
	// The job table is the one fact the service derives accounts from: a
	// right-hand side that decodes to something else is a service that binds
	// the wrong accounts (credsvc.JobAccountsFor), which no shape check sees.
	var triples []struct {
		Provider string `json:"provider"`
		Repo     string `json:"repo"`
		Workflow string `json:"workflow"`
	}
	if err := json.Unmarshal([]byte(got["FUGARO_JOB_ACCOUNTS"]), &triples); err != nil {
		t.Fatalf("FUGARO_JOB_ACCOUNTS is not a JSON array of triples: %v", err)
	}
	if len(triples) != len(c.JobAccounts) {
		t.Fatalf("FUGARO_JOB_ACCOUNTS has %d entries, want the fixture's %d", len(triples), len(c.JobAccounts))
	}
	for i, tr := range triples {
		w := c.JobAccounts[i]
		if tr.Provider != w.Provider || tr.Repo != w.Repo || tr.Workflow != w.Workflow {
			t.Errorf("FUGARO_JOB_ACCOUNTS[%d] = %+v, want %s/%s/%s", i, tr, w.Provider, w.Repo, w.Workflow)
		}
		if tr.Provider == "" || tr.Repo == "" || tr.Workflow == "" {
			t.Errorf("FUGARO_JOB_ACCOUNTS[%d] leaves a field empty: the service refuses a triple it cannot derive an account from", i)
		}
	}
	// The service itself: the name, the account and the image the fixture
	// names, so a plan that carries the seven values on something else is not
	// what this test says it is.
	if got := str(rc.Change.After["name"]); got != c.Service {
		t.Errorf("the plan creates the service %q, want the fixture's %q", got, c.Service)
	}
	sa := resource(t, p, "module.installation.google_service_account.credentials[0]")
	if got := str(sa.Change.After["account_id"]); got != c.AccountID {
		t.Errorf("the plan creates the account %q, want the fixture's %q", got, c.AccountID)
	}
	if got := str(rc.Change.After["template"].([]any)[0].(map[string]any)["service_account"]); got != c.AccountID+"@"+f.Project+".iam.gserviceaccount.com" {
		t.Errorf("the revision runs as %q, want the account this module creates", got)
	}
	// And the tenth entry is the email of the coordinator account THIS plan
	// creates: the class the read mint admits is the identity the sweep runs as,
	// and the same one iam.tf grants roles/run.invoker to on this service, so a
	// module that rendered another address — or the account of another project —
	// is a service that admits nobody the job can present, or somebody else.
	coordSA := resource(t, p, "module.installation.google_service_account.coordinator[0]")
	if got := str(coordSA.Change.After["email"]); got != coordinator {
		t.Errorf("the plan creates the coordinator account %q, want %q", got, coordinator)
	}
	// And the environment names no secret: the whole point of the four facts is
	// that the service derives the bundle's container itself and reads it as
	// its own account, so no entry may be a secret reference or a value that
	// looks like key material.
	for name, v := range got {
		if strings.Contains(v, "creds-bundle") || keyMaterial(v) {
			t.Errorf("%s is set to what looks like a secret: the bundle is read through Secret Manager by the service's own account, never carried in the environment", name)
		}
	}
}
