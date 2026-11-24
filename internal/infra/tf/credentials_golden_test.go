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
)

// moduleSource is the installation module's own file, read as this test's
// third check needs it: whether the module SETS a name is a fact of its text
// and not of any plan, so a name the cover admits has to be found there when
// no golden renders it.
func moduleSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules", "installation", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

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
		// The fields are declared in the order the module's jsonencode writes
		// them (alphabetical), because this test compares the value the plan
		// carries against this struct's own marshalling.
		ReviewJobAccounts []struct {
			Account  string `json:"account"`
			Profile  string `json:"profile"`
			Provider string `json:"provider"`
		} `json:"review_job_accounts"`
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
	// LauncherAccounts is the launcher class the roster derives (D1938, F25,
	// internal/infra/launchers.go), which the module renders into the service's
	// own environment as bare addresses (FUGARO_LAUNCHER_ACCOUNTS, DS2.2). It
	// is the class the grant route and the intake answer to, and it is NOT
	// var.launchers: that one is the same roster as IAM members, for the
	// invoker grants. This fixture names the accounts without naming the
	// members, which is the combination the plan's cover admits — the members
	// would put the project jobs' launcher grants, whose role is the
	// installation's computed custom role, into this plan.
	LauncherAccounts []string `json:"launcher_accounts"`
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
// D1068), the ninth, the read mint's admitted people (BB4), the tenth, the
// read mint's coordinator class (BB5), which arrives with the installation's
// coordinator account, the eleventh, the review-only jobs (C3.9a), which
// arrive with the installation's review variants, the twelfth, the
// launcher class (D1938, F25; DS2.2), which arrives with the roster's own
// people, and the thirteenth, the project's build identity (B5, D2301), which
// arrives with the account the module creates beside the service and is the
// build route's one caller class. So a variable the Go side
// computes and the module drops — or one the module sets from a value the
// fixture does not carry — is a service that never answers a job, or a verify
// route that admits nobody, on an installation whose init reports success.
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
	if len(c.ReviewJobAccounts) == 0 {
		t.Fatalf("the credentials fixture names no review job: this plan is the one that exercises the module's conditional review entry (C3.9a), and a fixture that names none could not tell it from the names the module always sets: %v", f)
	}
	review, err := json.Marshal(c.ReviewJobAccounts)
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
	if len(f.LauncherAccounts) == 0 {
		t.Fatalf("the credentials fixture names no launcher: this plan is the one that exercises the module's conditional twelfth entry — the launcher class the grant route and the intake answer to (D1938, F25) — and a fixture that names none could not tell it from the names the module always sets: %v", f)
	}
	launchers, err := json.Marshal(f.LauncherAccounts)
	if err != nil {
		t.Fatal(err)
	}
	// The coordinator class's one address, spelled the way Google spells the
	// account the module creates from these two inputs — the same derivation
	// the service's own revision runs as (below).
	coordinator := f.Coordinator.AccountID + "@" + f.Project + ".iam.gserviceaccount.com"
	// The build class's one address (B5, D2301), spelled the same way from the
	// account ID build_identity.tf fixes in its own source: this is the whole
	// reason the ID is a literal rather than derived, since the module renders
	// the account's own email into the entry and this test can name it without
	// planning anything.
	builds, err := json.Marshal([]string{BuildIdentityAccountID + "@" + f.Project + ".iam.gserviceaccount.com"})
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
		"FUGARO_REVIEW_JOB_ACCOUNTS":     string(review),
		"FUGARO_VERIFIER_ACCOUNTS":       string(verifiers),
		"FUGARO_PERSON_ACCOUNTS":         string(people),
		"FUGARO_COORDINATOR_ACCOUNT":     coordinator,
		"FUGARO_LAUNCHER_ACCOUNTS":       string(launchers),
		"FUGARO_BUILD_ACCOUNTS":          string(builds),
	}
	// The map's keys are the allow-list the cover holds the service to, and the
	// two are equal rather than nested: a name here that the allow-list does not
	// carry would make this test pass on an environment every run refuses, and a
	// name the allow-list carries that no plan renders is one the cover would
	// admit without the module ever setting it.
	//
	// The cover admits a name before the module renders it
	// (CredentialsServiceEnvAwaitingTheModule), so the plan plus that list is
	// what the allow-list must hold — and each name on that list is checked
	// against the plan first, which is the tripwire that fails once the module
	// starts rendering it and the entry is left behind. That list holds
	// FUGARO_SHARED_JOB_ACCOUNTS now (B5.1, ahead of the module that will render
	// it), so the plan alone no longer spells the allow-list: the plan plus
	// that one name is what the cover holds. FUGARO_LAUNCHER_ACCOUNTS was the
	// entry before it, and left the list in the change that made
	// credentials.tf render the name (DS2.2), as the identity half's three did
	// in LM12's change — which is why the names below and not that list spell
	// them now.
	names := slices.Sorted(maps.Keys(want))
	allowed := slices.Clone(credentialsServiceEnv)
	slices.Sort(allowed)
	pending := CredentialsServiceEnvAwaitingTheModule()
	for _, n := range pending {
		if slices.Contains(names, n) {
			t.Fatalf("the plan carries %s, so the module renders it now: drop it from CredentialsServiceEnvAwaitingTheModule (cover.go), which exists only for the names the module does not render yet", n)
		}
	}
	names = append(names, pending...)
	// ... and the third kind of name the allow-list holds: one the module DOES
	// render, on an installation this golden is not. Two of them are here. The
	// daemon's own class (F19, O2) is gated on var.orchestrator, and every
	// fixture here runs the credentials service with no resident orchestrator —
	// the daemon is the owner's opt-in of its own, and no golden turns it on —
	// so this plan carries no such entry while credentials.tf does set it. The
	// identity half's three (LM12) are the other: credentials.tf renders each
	// only where the credentials_service block carries a value for it, and no
	// fixture here names a signer account, a roster service account or an
	// implicit admin, so the plan carries none of them either. A name is only
	// admitted on this list if the module's own source is checked for it
	// below, which is the same tripwire the awaiting list carries in the other
	// direction: a name here that the module does not set would be a cover
	// admitting an entry nothing writes. The value the daemon's entry carries on
	// an installation that runs the daemon is pinned by internal/infra's
	// TestOrchestratorDaemonIsTheCredentialsServicesOwnCallerClass, which
	// plans that installation with the module itself, and the identity three's
	// values by the installation root's own tftest and by
	// TestCredentialsServiceEnvIsTheDeploymentsOwn.
	module := moduleSource(t, "credentials.tf")
	for _, n := range []string{
		"FUGARO_ORCHESTRATOR_ACCOUNTS",
		"FUGARO_SIGNER_ACCOUNT",
		"FUGARO_ROSTER_SERVICE_ACCOUNTS",
		"FUGARO_IMPLICIT_ADMINS",
	} {
		if slices.Contains(names, n) {
			// A fixture renders it now, so it is an ordinary name above and
			// nothing here may exempt it.
			continue
		}
		if !regexp.MustCompile(`name\s*=\s*"` + n + `"`).MatchString(module) {
			t.Fatalf("the cover admits %s and neither this plan nor credentials.tf renders it: the allow-list would admit an entry nothing writes", n)
		}
		names = append(names, n)
	}
	slices.Sort(names)
	if !slices.Equal(names, allowed) {
		t.Fatalf("this test spells %v (the plan, %v and the names the module renders on an installation this golden is not) as the service's environment, the cover allows %v", names, pending, credentialsServiceEnv)
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
