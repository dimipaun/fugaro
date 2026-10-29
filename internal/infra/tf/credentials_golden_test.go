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
	} `json:"credentials_service"`
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

// The credentials service's seven environment variables, read off a real plan
// of the installation module against the fixture the plan was made from (C1.14
// and C1.24). The service refuses to serve anything unless every one of them
// is there and well formed (internal/cli's credentialsServiceConfig), so a
// variable the Go side computes and the module drops — or one the module sets
// from a value the fixture does not carry — is a service that never answers a
// job, on an installation whose init reports success.
//
// The Terraform tftest (roots/installation/tests/installation.tftest.hcl)
// pins the same map against its own variable, and both halves are needed: the
// tftest never runs the Go code that decides what to write into the tfvars,
// and this test never exercises a `terraform test` run.
func TestGoldenCredentialsServiceCarriesItsSevenNames(t *testing.T) {
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
	want := map[string]string{
		"FUGARO_PROJECT":                 f.FugaroProject,
		"FUGARO_GCP_PROJECT":             f.Project,
		"FUGARO_REGION":                  f.Region,
		"FUGARO_RTDB_URL":                c.RTDBURL,
		"FUGARO_FIREBASE_PROJECT":        c.FirebaseProject,
		"FUGARO_CREDENTIALS_SERVICE_URL": c.URL,
		"FUGARO_JOB_ACCOUNTS":            string(jobAccounts),
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
