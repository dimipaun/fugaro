package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

const (
	newProj    = "fugaro-new-1234"
	billingAcc = "0123AB-4567CD-89EF01"
)

// projectRig is the init rig with the Firebase Management and Cloud
// Billing fakes, and a local config for a GCP project that does not exist
// yet.
type projectRig struct {
	*initRig
	fb   *gcpfake.FirebaseMgmt
	bill *gcpfake.Billing
}

func newProjectRig(t *testing.T) *projectRig {
	t.Helper()
	r := newInitRig(t)
	// A coding agent's session (this one, maybe) refuses typed confirmations.
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	r.crm.PendingPolls = 1
	fb, bill := gcpfake.NewFirebaseMgmt(t), gcpfake.NewBilling(t)
	fb.UseCRM(r.crm)
	bill.UseCRM(r.crm)
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.ReplaceAll(string(b), initProject, newProj)
	cfg = strings.Replace(cfg, "no_auth: true }", "cloud_billing: "+bill.URL+"/, firebase_management: "+fb.URL+"/, no_auth: true }", 1)
	if err := os.WriteFile(r.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	old := infra.Wait
	infra.Wait.Interval = time.Millisecond
	t.Cleanup(func() { infra.Wait = old })
	oldSuffix := infra.Suffix
	infra.Suffix = func() string { return "ab12" }
	t.Cleanup(func() { infra.Suffix = oldSuffix })
	return &projectRig{initRig: r, fb: fb, bill: bill}
}

// existing makes the project exist with Firebase, and billing as given.
func (r *projectRig) existing(billing bool) {
	r.crm.AddProject(newProj, 424242)
	r.gcs.AddProject(newProj, 424242)
	r.fb.AddFirebase(newProj)
	r.bill.SetBilling(newProj, billing)
}

// run is fugaro init with the flags and the stdin; terminal says whether
// stdin is a terminal.
func (r *projectRig) run(t *testing.T, terminal bool, stdin string, args ...string) (string, string, error) {
	t.Helper()
	if terminal {
		fakeTerminal(t)
	}
	return executeStdin(t, stdin, append([]string{"init", "--gcp-project", newProj}, args...)...)
}

func (r *projectRig) nothingDone(t *testing.T, why string) {
	t.Helper()
	if n := len(r.crm.Creates()); n != 0 {
		t.Errorf("%s: %d project(s) created", why, n)
	}
	if n := len(r.fb.Adds()); n != 0 {
		t.Errorf("%s: Firebase added %d time(s)", why, n)
	}
	if n := len(r.bill.Links()); n != 0 {
		t.Errorf("%s: billing linked %d time(s)", why, n)
	}
}

func (r *projectRig) posts(t *testing.T) int {
	t.Helper()
	n := 0
	for _, s := range []*gcpfake.Server{r.crm.Server, r.fb.Server, r.bill.Server} {
		for _, q := range s.Requests() {
			if q.Method != "GET" {
				n++
			}
		}
	}
	return n
}

type projectJSON struct {
	Created string `json:"created_project"`
	Stages  []struct{ Name, State, Detail string }
	Left    []map[string]string `json:"left_for_you"`
}

func parseProjectJSON(t *testing.T, out string) projectJSON {
	t.Helper()
	var j projectJSON
	if err := json.Unmarshal([]byte(out), &j); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	return j
}

func (j projectJSON) state(name string) string {
	for _, s := range j.Stages {
		if s.Name == name {
			return s.State
		}
	}
	return ""
}

// The flag alone creates nothing; neither does a wrong ID; the typed ID
// creates exactly one project and adds Firebase, and billing stays the
// user's.
func TestCreateProjectNeedsFlagAndConfirm(t *testing.T) {
	r := newProjectRig(t)

	// No flag: the project stage is not even registered.
	// (The installation's own checks then find no such project.)
	_, _, err := r.run(t, true, newProj+"\n", "--plan-only")
	if err == nil || !strings.Contains(err.Error(), "reading project") {
		t.Errorf("err = %v", err)
	}
	r.nothingDone(t, "no --create-project")

	// The flag, and a wrong confirmation.
	_, _, err = r.run(t, true, "yes\n", "--create-project")
	if err == nil {
		t.Error("an unconfirmed creation exited 0")
	}
	r.nothingDone(t, "a wrong confirmation")

	// The flag, with --plan-only: shows the plan and does nothing.
	out, _, err := r.run(t, true, newProj+"\n", "--create-project", "--plan-only")
	if err != nil && ExitCode(err) != ExitUserError {
		t.Fatal(err)
	}
	if !strings.Contains(out, "create project "+newProj) {
		t.Errorf("the plan does not say what it will create:\n%s", out)
	}
	r.nothingDone(t, "--plan-only")

	// The right ID.
	_, _, _ = r.run(t, true, newProj+"\n", "--create-project", "--display-name", "Fugaro New")
	cs := r.crm.Creates()
	if len(cs) != 1 || cs[0].ID != newProj || cs[0].DisplayName != "Fugaro New" || cs[0].Parent != "" {
		t.Fatalf("creates = %+v: want exactly one, with no parent", cs)
	}
	if adds := r.fb.Adds(); len(adds) != 1 || adds[0] != newProj {
		t.Errorf("Firebase adds = %v", adds)
	}
	if len(r.bill.Links()) != 0 {
		t.Error("billing was linked without --link-billing")
	}
}

// --yes, --non-interactive, --json, a pipe and a coding agent's session
// never confirm the creation, whatever else is on stdin.
func TestYesNeverCreatesProject(t *testing.T) {
	cases := []struct {
		name     string
		terminal bool
		args     []string
		env      string
	}{
		{"--yes, piped stdin", false, []string{"--yes"}, ""},
		{"--yes --non-interactive", true, []string{"--yes", "--non-interactive"}, ""},
		{"--yes --json at a terminal", true, []string{"--yes", "--json"}, ""},
		{"terminal, no flags but a coding agent", true, nil, "CLAUDECODE"},
		{"piped stdin without --yes", false, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newProjectRig(t)
			if tc.env != "" {
				t.Setenv(tc.env, "1")
			}
			out, _, err := r.run(t, tc.terminal, newProj+"\n"+newProj+"\n", append([]string{"--create-project", "--link-billing", billingAcc}, tc.args...)...)
			r.nothingDone(t, tc.name)
			if err == nil {
				t.Errorf("exit 0 without creating the project:\n%s", out)
			}
			if p := r.posts(t); p != 0 {
				t.Errorf("%d write call(s) were sent", p)
			}
		})
	}

	// --yes at a terminal, with the ID not typed (EOF): still nothing.
	r := newProjectRig(t)
	_, _, err := r.run(t, true, "", "--yes", "--create-project")
	if err == nil {
		t.Error("exit 0")
	}
	r.nothingDone(t, "--yes with nothing typed")
}

func TestYesNeverLinksBilling(t *testing.T) {
	for name, tc := range map[string]struct {
		terminal bool
		stdin    string
		args     []string
	}{
		"--yes, piped":                       {false, billingAcc + "\n", []string{"--yes"}},
		"--yes --non-interactive":            {true, billingAcc + "\n", []string{"--yes", "--non-interactive"}},
		"terminal, the project ID not typed": {true, "x\n", nil},
		"terminal, the wrong account typed":  {true, newProj + "\n", nil},
		"terminal, --yes, account not typed": {true, "", []string{"--yes"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newProjectRig(t)
			r.existing(false)
			_, _, _ = r.run(t, tc.terminal, tc.stdin, append([]string{"--create-project", "--link-billing", billingAcc}, tc.args...)...)
			r.nothingDone(t, name)
		})
	}

	// The creation's confirmation does not stand for billing's.
	r := newProjectRig(t)
	_, _, _ = r.run(t, true, newProj+"\n"+newProj+"\n", "--create-project", "--link-billing", billingAcc)
	if len(r.crm.Creates()) != 1 || len(r.bill.Links()) != 0 {
		t.Errorf("creates %d, links %v: the project's ID must not confirm billing", len(r.crm.Creates()), r.bill.Links())
	}

	// Both confirmations typed: the link is made, to the account named.
	r = newProjectRig(t)
	r.bill.AddAccount(billingAcc, true)
	out, _, _ := r.run(t, true, newProj+"\n"+billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	if l := r.bill.Links(); len(l) != 1 || l[0] != (gcpfake.Link{Project: newProj, Account: billingAcc}) {
		t.Errorf("links = %v\n%s", l, out)
	}
}

func TestProjectAndBillingInConfirmationText(t *testing.T) {
	r := newProjectRig(t)
	t.Setenv("GOOGLE_CLOUD_QUOTA_PROJECT", "my-quota-proj")
	out, _, _ := r.run(t, true, "no\n", "--create-project", "--parent", "organizations/123456", "--display-name", "Fugaro New", "--link-billing", billingAcc)
	for _, want := range []string{newProj, "organizations/123456", "Fugaro New", "my-quota-proj", "permanent", "Billing is linked separately", "CREATES"} {
		if !strings.Contains(out, want) {
			t.Errorf("the creation's confirmation lacks %q:\n%s", want, out)
		}
	}
	r.nothingDone(t, "a declined creation")

	r = newProjectRig(t)
	r.existing(false)
	out, _, _ = r.run(t, true, "no\n", "--create-project", "--link-billing", billingAcc)
	for _, want := range []string{billingAcc, newProj, "LINKS", "charges"} {
		if !strings.Contains(out, want) {
			t.Errorf("billing's confirmation lacks %q:\n%s", want, out)
		}
	}
	// No parent says so; it is never guessed.
	r = newProjectRig(t)
	out, _, _ = r.run(t, true, "no\n", "--create-project")
	if !strings.Contains(out, "no parent") {
		t.Errorf("a create without --parent does not say it has none:\n%s", out)
	}
	r.nothingDone(t, "a declined creation")
}

// A project that exists and the caller can read is adopted: no create, no
// Firebase, no link, whatever the flags.
func TestCreateAdoptsOwnProject(t *testing.T) {
	r := newProjectRig(t)
	r.existing(true)
	r.bill.AddAccount(billingAcc, true)
	out, _, _ := r.run(t, true, newProj+"\n"+billingAcc+"\n", "--create-project", "--link-billing", billingAcc, "--plan-only")
	r.nothingDone(t, "an adopted project (plan)")
	if !strings.Contains(out, "[done]") || !strings.Contains(out, "billing is already linked") {
		t.Errorf("the project stage is not reported done, with billing left as it is:\n%s", out)
	}
	_, _, _ = r.run(t, true, newProj+"\n"+billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	r.nothingDone(t, "an adopted project")
}

// An interrupted run: the project exists without Firebase; the rerun adds
// Firebase behind a typed ID, and never creates a second project.
func TestCreateRerunAddsFirebaseOnly(t *testing.T) {
	r := newProjectRig(t)
	r.crm.AddProject(newProj, 424242)
	r.gcs.AddProject(newProj, 424242)
	r.bill.SetBilling(newProj, true)
	_, _, _ = r.run(t, true, newProj+"\n", "--create-project")
	if len(r.crm.Creates()) != 0 || len(r.fb.Adds()) != 1 {
		t.Errorf("creates %d, adds %v", len(r.crm.Creates()), r.fb.Adds())
	}
}

func TestCreateRefusesForeignExisting(t *testing.T) {
	r := newProjectRig(t)
	r.crm.AddForeign(newProj)
	out, _, err := r.run(t, true, newProj+"\n", "--create-project", "--link-billing", billingAcc)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Errorf("err = %v (exit %d), want a user error", err, ExitCode(err))
	}
	if len(r.fb.Adds()) != 0 || len(r.bill.Links()) != 0 || len(r.crm.Creates()) != 0 {
		t.Errorf("something was done for an ID someone else holds: adds %v links %v", r.fb.Adds(), r.bill.Links())
	}
	if !strings.Contains(out+errString(err), "another project") {
		t.Errorf("the refusal does not say the ID is taken:\n%s\n%v", out, err)
	}

	// A project pending deletion is refused, nothing done.
	r = newProjectRig(t)
	r.existing(true)
	r.crm.SetLifecycle(newProj, "DELETE_REQUESTED")
	out, _, err = r.run(t, true, newProj+"\n", "--create-project")
	if err == nil || !strings.Contains(out+errString(err), "DELETE_REQUESTED") || !strings.Contains(out, "undelete") {
		t.Errorf("a project pending deletion: err %v\n%s", err, out)
	}
	r.nothingDone(t, "a project pending deletion")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestIDCollisionSuggestsAlternative(t *testing.T) {
	r := newProjectRig(t)
	r.crm.AddForeign(newProj)
	out, _, err := r.run(t, true, newProj+"\n", "--create-project")
	if err == nil || !strings.Contains(out, "such as "+newProj+"-ab12") {
		t.Errorf("no suggestion: err %v\n%s", err, out)
	}
	if got := infra.SuggestProjectID("abcdefghij-abcdefghij-abcdefg"); len(got) > 30 || !infra.ValidProjectID(got) {
		t.Errorf("suggestion %q for a long ID is not a valid ID", got)
	}
}

// Without --link-billing the stage creates the project, links nothing,
// lists nothing, and leaves the one gcloud line, exiting 1.
func TestBillingGuidedByDefault(t *testing.T) {
	r := newProjectRig(t)
	out, _, err := r.run(t, true, newProj+"\n", "--create-project")
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d (%v), want 1: billing is the user's to do", ExitCode(err), err)
	}
	if len(r.crm.Creates()) != 1 || len(r.bill.Links()) != 0 {
		t.Fatalf("creates %d links %v", len(r.crm.Creates()), r.bill.Links())
	}
	want := "gcloud billing projects link " + newProj + " --billing-account=BILLING_ACCOUNT_ID"
	if !strings.Contains(out, want) {
		t.Errorf("the guided command is missing:\n%s", out)
	}
	for _, q := range r.bill.Requests() {
		if !strings.HasSuffix(q.Path, "/billingInfo") || q.Method != "GET" {
			t.Errorf("billing call %s %s: only the project's own billing is read, never the accounts", q.Method, q.Path)
		}
	}
	// A rerun with the project there reads, changes nothing, and says the same.
	before := r.posts(t)
	out, _, err = r.run(t, true, "", "--create-project")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, want) || r.posts(t) != before {
		t.Errorf("rerun: exit %d, writes %d -> %d\n%s", ExitCode(err), before, r.posts(t), out)
	}
}

func TestBillingGuidedJSON(t *testing.T) {
	r := newProjectRig(t)
	r.existing(false)
	out, _, err := r.run(t, false, "", "--create-project", "--json", "--non-interactive")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d (%v)", ExitCode(err), err)
	}
	j := parseProjectJSON(t, out)
	if j.state("project") != "needs-you" || len(j.Left) != 1 || !strings.HasPrefix(j.Left[0]["text"], "gcloud billing projects link "+newProj) {
		t.Errorf("%+v", j)
	}
	r.nothingDone(t, "a guided billing")
}

// Linking billing needs both flags; --link-billing alone is refused before
// any call, as is a malformed ID, account, parent or name.
func TestProjectFlagsRefusedBeforeAnyCall(t *testing.T) {
	for name, args := range map[string][]string{
		"--link-billing alone":   {"--link-billing", billingAcc},
		"--parent alone":         {"--parent", "organizations/1"},
		"a lowercase account":    {"--create-project", "--link-billing", strings.ToLower(billingAcc)},
		"a bad parent":           {"--create-project", "--parent", "projects/1"},
		"an account's full name": {"--create-project", "--link-billing", "billingAccounts/" + billingAcc},
		"a bad display name":     {"--create-project", "--display-name", "x"},
		"--forget":               {"--create-project", "--forget"},
		"--config-only":          {"--create-project", "--config-only"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newProjectRig(t)
			_, _, err := r.run(t, true, newProj+"\n"+billingAcc+"\n", args...)
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d (%v)", ExitCode(err), err)
			}
			r.nothingDone(t, name)
			if n := len(r.crm.Requests()) + len(r.fb.Requests()) + len(r.bill.Requests()); n != 0 {
				t.Errorf("%d call(s) sent before the refusal", n)
			}
		})
	}
	// The project's ID: validated, and never taken from anywhere but the flag.
	for _, id := range []string{"abc", "Upper-case-id", "1starts-digit", "ends-with-hyphen-", "has_underscore", "this-id-is-way-too-long-for-gcp-1", "my-google-project", "has-ssl-in-it"} {
		r := newProjectRig(t)
		_, _, err := executeStdin(t, id+"\n", "init", "--create-project", "--gcp-project", id)
		if ExitCode(err) != ExitUserError {
			t.Errorf("%q: exit %d (%v)", id, ExitCode(err), err)
		}
		r.nothingDone(t, id)
	}
	r := newProjectRig(t)
	if _, _, err := executeStdin(t, "", "init", "--create-project"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--gcp-project") {
		t.Errorf("--create-project without --gcp-project: %v", err)
	}
	if _, _, err := executeStdin(t, "", "init", "--repo", "--create-project", "--gcp-project", newProj); ExitCode(err) != ExitUserError {
		t.Errorf("--repo with --create-project: %v", err)
	}
	r.nothingDone(t, "flag refusals")
}

func TestOrgPolicyErrorVerbatim(t *testing.T) {
	r := newProjectRig(t)
	const msg = "Constraint constraints/gcp.restrictProjectCreation violated for the organization"
	r.crm.FailCreates(403, "PERMISSION_DENIED", msg)
	_, _, err := r.run(t, true, newProj+"\n", "--create-project")
	if err == nil || !strings.Contains(err.Error(), msg) || !strings.Contains(err.Error(), "organization policy") {
		t.Errorf("err = %v: want the service's text and the likely cause", err)
	}
	if len(r.fb.Adds()) != 0 {
		t.Error("Firebase was added after a refused creation")
	}

	// Billing refusals too: the text is the service's, with the likely cause.
	r = newProjectRig(t)
	r.existing(false)
	r.bill.FailLinks(403, "PERMISSION_DENIED", "The caller does not have permission to link")
	_, _, err = r.run(t, true, billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	if err == nil || !strings.Contains(err.Error(), "The caller does not have permission to link") || !strings.Contains(err.Error(), "billing.resourceAssociations.create") {
		t.Errorf("err = %v", err)
	}
}

// The project is read by its ID alone: the gcloud default project (and the
// environment's) is never used, in a call or as the ID.
func TestNeverUsesGcloudDefaultProject(t *testing.T) {
	r := newProjectRig(t)
	const dflt = "their-default-9999"
	if _, _, err := executeStdin(t, dflt+"\n", "init", "--create-project"); ExitCode(err) != ExitUserError {
		t.Errorf("without --gcp-project: exit %d (%v)", ExitCode(err), err)
	}
	// The environment's default (gcloud's, Terraform's, Google's) does not
	// stand in for the flag, and does not redirect a call: the environment
	// check refuses it before any stage runs.
	for _, k := range []string{"CLOUDSDK_CORE_PROJECT", "GOOGLE_CLOUD_PROJECT", "GOOGLE_PROJECT"} {
		t.Setenv(k, dflt)
		_, _, _ = r.run(t, true, newProj+"\n", "--create-project")
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	r.nothingDone(t, "a default project in the environment")
	_, _, _ = r.run(t, true, newProj+"\n"+billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	for _, s := range []*gcpfake.Server{r.crm.Server, r.fb.Server, r.bill.Server} {
		for _, q := range s.Requests() {
			if strings.Contains(q.Path+q.Query+string(q.Body), dflt) {
				t.Errorf("a call names the default project: %s %s", q.Method, q.Path)
			}
		}
	}
	if cs := r.crm.Creates(); len(cs) != 1 || cs[0].ID != newProj {
		t.Errorf("creates = %+v", cs)
	}
}

// A run that applies the stage twice creates one project.
func TestOneProjectPerRun(t *testing.T) {
	r := newProjectRig(t)
	_, _, _ = r.run(t, true, newProj+"\n"+newProj+"\n", "--create-project")
	_, _, _ = r.run(t, true, newProj+"\n"+newProj+"\n", "--create-project")
	if n := len(r.crm.Creates()); n != 1 {
		t.Errorf("%d creates over two runs: the second adopts", n)
	}
	// The stage itself refuses a second Apply.
	e := &projectStage{started: true}
	if _, err := e.Apply(t.Context(), initflowEnv()); err == nil || !strings.Contains(err.Error(), "at most one project") {
		t.Errorf("a second Apply: %v", err)
	}
}

func initflowEnv() initflow.Env { return initflow.Env{Interactive: true} }

// Billing is never replaced: an unreadable state or a link that is only
// disabled is guided, never written; a project this run created is linked.
func TestRelinkNeverHappens(t *testing.T) {
	r := newProjectRig(t)
	r.existing(false)
	r.bill.HideBilling(newProj)
	out, _, err := r.run(t, true, billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	r.nothingDone(t, "unreadable billing")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "cannot be read") || !strings.Contains(out, "gcloud billing projects link") {
		t.Errorf("exit %d\n%s", ExitCode(err), out)
	}

	r = newProjectRig(t)
	r.existing(false)
	r.bill.SetSuspended(newProj, "AAAAAA-BBBBBB-CCCCCC")
	out, _, err = r.run(t, true, billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	r.nothingDone(t, "a suspended link")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "never replaces an existing link") {
		t.Errorf("exit %d\n%s", ExitCode(err), out)
	}

	r = newProjectRig(t)
	r.bill.AddAccount(billingAcc, true)
	_, _, _ = r.run(t, true, newProj+"\n"+billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	if len(r.bill.Links()) != 1 {
		t.Errorf("a project created by this run was not linked: %v", r.bill.Links())
	}
}

// A foreign, readable project is adopted only with every change said to be
// to an EXISTING project, each behind its own typed confirmation.
func TestExistingProjectBannersSayExisting(t *testing.T) {
	r := newProjectRig(t)
	r.crm.AddProject(newProj, 424242)
	r.gcs.AddProject(newProj, 424242)
	r.bill.SetBilling(newProj, false)
	out, _, _ := r.run(t, true, "", "--create-project", "--link-billing", billingAcc, "--plan-only")
	if !strings.Contains(out, "adopting an EXISTING project "+newProj) {
		t.Errorf("the plan does not say the project exists:\n%s", out)
	}
	r.nothingDone(t, "the plan")

	out, _, _ = r.run(t, true, newProj+"\n"+billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	if n := strings.Count(out, "⚠ CONFIRM: adopting an EXISTING project"); n != 2 {
		t.Errorf("%d confirmations say existing, want 2 (Firebase, billing):\n%s", n, out)
	}
	if len(r.crm.Creates()) != 0 || len(r.fb.Adds()) != 1 || len(r.bill.Links()) != 1 {
		t.Errorf("adds %v links %v", r.fb.Adds(), r.bill.Links())
	}

	// The ID alone is not enough for billing; and nothing typed does nothing.
	r = newProjectRig(t)
	r.crm.AddProject(newProj, 424242)
	r.gcs.AddProject(newProj, 424242)
	r.bill.SetBilling(newProj, false)
	_, _, _ = r.run(t, true, "", "--create-project", "--link-billing", billingAcc)
	r.nothingDone(t, "no typed confirmation")
}

func TestCreateWithOtherFirebaseRefused(t *testing.T) {
	r := newProjectRig(t)
	_, _, err := r.run(t, true, newProj+"\n", "--create-project", "--firebase", "other-fp-1234")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "other-fp-1234") {
		t.Errorf("err = %v", err)
	}
	if n := len(r.crm.Requests()); n != 0 {
		t.Errorf("%d calls", n)
	}
}

// A create that times out says what to wait for, and a taken ID's text
// first asks whether an earlier run created it.
func TestCreatedButNotReadableSaysWait(t *testing.T) {
	r := newProjectRig(t)
	r.crm.PropagationReads = 1000
	out, _, err := r.run(t, true, newProj+"\n", "--create-project")
	if err == nil || !strings.Contains(out, "operation operations/cp.") || !strings.Contains(out, "wait a few minutes and rerun with the same ID") {
		t.Errorf("err %v\n%s", err, out)
	}
	if strings.Contains(out, "such as") {
		t.Errorf("a new ID is suggested for a project this run created:\n%s", out)
	}

	r = newProjectRig(t)
	r.crm.AddForeign(newProj)
	out, _, _ = r.run(t, true, newProj+"\n", "--create-project")
	if !strings.Contains(out, "created "+newProj+" a moment ago") {
		t.Errorf("taken ID text:\n%s", out)
	}
}

func TestCredentialNoteInBanner(t *testing.T) {
	write := func(t *testing.T, body string) {
		t.Helper()
		f := t.TempDir() + "/key.json"
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", f)
	}
	banner := func(t *testing.T) string {
		out, _, _ := newProjectRig(t).run(t, true, "no\n", "--create-project")
		return out
	}
	r := newProjectRig(t)
	_ = r
	t.Run("service account", func(t *testing.T) {
		write(t, `{"type":"service_account","private_key":"SECRETKEY","quota_project_id":"qp-1"}`)
		out := banner(t)
		if !strings.Contains(out, "SERVICE ACCOUNT") || strings.Contains(out, "SECRETKEY") || strings.Contains(out, "key.json") || !strings.Contains(out, "quota project qp-1") {
			t.Errorf("%s", out)
		}
	})
	t.Run("user", func(t *testing.T) {
		write(t, `{"type":"authorized_user","quota_project_id":"qp-2"}`)
		if out := banner(t); !strings.Contains(out, "as you (your own user credentials") || !strings.Contains(out, "quota project qp-2") {
			t.Errorf("%s", out)
		}
	})
	t.Run("no quota project", func(t *testing.T) {
		write(t, `{"type":"authorized_user"}`)
		if out := banner(t); !strings.Contains(out, "set-quota-project") {
			t.Errorf("%s", out)
		}
	})
	t.Run("environment's quota is sanitized", func(t *testing.T) {
		t.Setenv("GOOGLE_CLOUD_QUOTA_PROJECT", "bad\x1b[31m;proj")
		out := banner(t)
		if strings.Contains(out, "\x1b") || !strings.Contains(out, "quota project bad??31m?proj") {
			t.Errorf("%q", out)
		}
	})
}

// The stage's ID is the flag's and nothing else: an engine whose config
// holds another project (a default from anywhere) still reads and creates
// only the flag's.
func TestStageIDIsTheFlagAlone(t *testing.T) {
	r := newProjectRig(t)
	e := &initEngine{r: &initRun{o: &initOptions{createProject: true, cloud: cloudOptions{gcpProject: newProj}}},
		lc: &localcfg.Config{GCPProject: "their-default-9999"}}
	e.lc.Endpoints = localcfg.Endpoints{ResourceManager: r.crm.URL + "/", FirebaseManagement: r.fb.URL + "/", CloudBilling: r.bill.URL + "/", NoAuth: true}
	s := newProjectStage(e)
	if _, err := s.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, sv := range []*gcpfake.Server{r.crm.Server, r.fb.Server, r.bill.Server} {
		for _, q := range sv.Requests() {
			if strings.Contains(q.Path, "their-default") {
				t.Errorf("a call names the other project: %s", q.Path)
			}
		}
	}
	if s.id != newProj {
		t.Errorf("id %q", s.id)
	}
}
