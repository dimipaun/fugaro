package testutil_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestTheHarnessGivesARunABudgetDatabase: what NewBudget answers is a database
// a run can be pointed at and a launch can be configured with — the job's own
// environment entries (which the run's reader takes), and the config block an
// installation carries. The address is the fake's, so a test that seeds a node
// and the run that opens a session against it are the same database.
func TestTheHarnessGivesARunABudgetDatabase(t *testing.T) {
	b := testutil.NewBudget(t)
	if b.URL() == "" {
		t.Fatal("the harness's database has no address")
	}
	env := map[string]string{}
	for _, kv := range b.Env() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	if env[runner.RTDBURLEnv] != b.URL() || env[runner.FirebaseAPIKeyEnv] != b.APIKey {
		t.Fatalf("the job's environment names %v, want %s and %s", env, b.URL(), b.APIKey)
	}
	// The run's own reader, over exactly those entries: what a launcher parses
	// a job's environment into. A local run admits a database on a loopback
	// address, which is what a fake is.
	be, err := runner.BackendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok }, true)
	if err != nil {
		t.Fatalf("the run's reader refused the environment the harness built: %v", err)
	}
	if !be.On() || be.RTDBURL != b.URL() || be.APIKey != b.APIKey {
		t.Fatalf("the run's reader answered %+v, want a backend on %s", be, b.URL())
	}
	// And the installation's own config: the block, read by the loader the CLI
	// reads a project with. It names the database and nothing about how a run
	// is accounted for, which is what every harness here leans on: the block
	// changes what a mode-unset installation does at no test's merge.
	cfg, err := localcfg.Parse([]byte("version: 2\nname: aurora\ngcp_project: " + budgetProject + "\n" +
		"region: us-east5\nruns_bucket: unused-bucket\nbucket_url: 'file://" + t.TempDir() + "'\n" +
		"endpoints: { no_auth: true }\n" + b.ConfigBlock()))
	if err != nil {
		t.Fatalf("the config block the harness writes does not load: %v", err)
	}
	if cfg.Budget == nil || cfg.Budget.RTDBURL != b.URL() || cfg.Budget.FirebaseAPIKey != b.APIKey {
		t.Fatalf("the config holds %+v, want the database at %s", cfg.Budget, b.URL())
	}
}

// TestTheHarnessCanAskForNone: NoBudget is a harness's way of saying the job,
// the launch and the installation have no database at all, which is the state
// this work refuses a run, a launch and an update for. A null budget answers
// nothing, and a harness that appends its environment blindly adds nothing.
func TestTheHarnessCanAskForNone(t *testing.T) {
	b := testutil.NoBudget()
	if b.URL() != "" || b.Env() != nil || b.ConfigBlock() != "" {
		t.Fatalf("a null budget answered %q, %v, %q", b.URL(), b.Env(), b.ConfigBlock())
	}
	env := map[string]string{}
	for _, kv := range b.Env() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	if len(env) != 0 {
		t.Fatalf("a job built with no budget carries %v", env)
	}
	be, err := runner.BackendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok }, true)
	if err != nil {
		t.Fatalf("the run's reader refused an environment with no backend in it: %v", err)
	}
	if be.On() {
		t.Fatalf("a run built with no budget has a backend: %+v", be)
	}
}

// TestTheFakeProjectAnswersAPermissionTest: the fake project API answers
// projects.testIamPermissions with what the caller holds of what it asked
// about, which is what the probe of a database this installation does not know
// reads (an update that must decide whether the person can make the project's
// IAM changes before it creates one).
func TestTheFakeProjectAnswersAPermissionTest(t *testing.T) {
	const get = "resourcemanager.projects.get"
	const setIam = "resourcemanager.projects.setIamPolicy"
	f := gcpfake.NewCRM(t)
	f.AddProject(budgetProject, 1234)
	f.SetHeldPermissions(budgetProject, get)

	asked := []string{get, setIam}
	if got := testPermissions(t, f, budgetProject, asked); len(got) != 1 || got[0] != get {
		t.Fatalf("the fake answered %v for %v, want just %s", got, asked, get)
	}
	// A caller that holds none of them is answered an empty list, not a
	// refusal: the two mean different things, and an update that read one as
	// the other would create a database for a caller that cannot.
	if got := testPermissions(t, f, budgetProject, []string{setIam}); len(got) != 0 {
		t.Fatalf("the fake answered %v for a permission the caller does not hold, want none", got)
	}
	// A project the caller cannot read is refused, as the fake's other IAM
	// reads are: an empty list must not be how a missing project answers.
	f.SetHeldPermissions("proj-nobody", get)
	if code := testPermissionsStatus(t, f, "proj-nobody", asked); code != http.StatusForbidden {
		t.Fatalf("a project the caller cannot read answered %d, want 403", code)
	}
}

// testPermissions asks the fake for asked on project and returns what it
// answered was held.
func testPermissions(t *testing.T, f *gcpfake.CRM, project string, asked []string) []string {
	t.Helper()
	status, got := testPermissionsCall(t, f, project, asked)
	if status != http.StatusOK {
		t.Fatalf("testIamPermissions on %s answered %d: %v", project, status, got)
	}
	return got
}

// testPermissionsStatus is the same call, for the answers that are not a list.
func testPermissionsStatus(t *testing.T, f *gcpfake.CRM, project string, asked []string) int {
	t.Helper()
	status, _ := testPermissionsCall(t, f, project, asked)
	return status
}

func testPermissionsCall(t *testing.T, f *gcpfake.CRM, project string, asked []string) (int, []string) {
	t.Helper()
	body := `{"permissions":[` + quoted(asked) + `]}`
	resp, err := http.Post(f.URL+"/v1/projects/"+project+":testIamPermissions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	// An error answer carries no permissions list, and its body is not read as
	// one: only a 200 is a list.
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	var out struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("the fake's answer is not JSON: %s", data)
	}
	return resp.StatusCode, out.Permissions
}

// budgetProject is the GCP project these tests' fixtures live in, the one
// internal/cli's own fixtures name.
const budgetProject = "proj-1234"

// quoted is names as a JSON array's contents: the permission IDs these tests
// ask about are plain, so nothing here needs an encoder.
func quoted(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = `"` + n + `"`
	}
	return strings.Join(q, ",")
}
