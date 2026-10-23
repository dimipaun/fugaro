package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// The three patterns TestTheBillingListIsNarrow reads the tree with: the one
// call site that lists a caller's billing accounts, and the predicate that
// bounds it. They are written on the call and not on the callee's own name so
// that an aliased import is caught too.
var (
	billingListCallRE = regexp.MustCompile(`\.ListWithFallback\(`)
	canTypeReadRE     = regexp.MustCompile(`\br\.canType\(\)`)
	canTypeGuardRE    = regexp.MustCompile(`if !r\.canType\(\) \{`)
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

// run is the converge with the flags and the stdin; terminal says whether
// stdin is a terminal.
func (r *projectRig) run(t *testing.T, terminal bool, stdin string, args ...string) (string, string, error) {
	t.Helper()
	if terminal {
		fakeTerminal(t)
	}
	return executeStdin(t, stdin, append([]string{"setup", "--gcp-project", newProj}, args...)...)
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

// Without --link-billing the stage creates the project, links nothing, lists
// nothing itself — the wizard's billing question (W4.3) is the one place in a
// run that lists the caller's accounts, and this stage never asks for one —
// and leaves the one gcloud line, exiting 1.
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
			t.Errorf("billing call %s %s: the project stage reads only the project's own billing, never the accounts (the wizard's billing question is the one place a run lists them, W4.3)", q.Method, q.Path)
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
		_, _, err := executeStdin(t, id+"\n", "setup", "--create-project", "--gcp-project", id)
		if ExitCode(err) != ExitUserError {
			t.Errorf("%q: exit %d (%v)", id, ExitCode(err), err)
		}
		r.nothingDone(t, id)
	}
	r := newProjectRig(t)
	if _, _, err := executeStdin(t, "", "setup", "--create-project"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--gcp-project") {
		t.Errorf("--create-project without --gcp-project: %v", err)
	}
	if _, _, err := executeStdin(t, "", "setup", "--repo", "--create-project", "--gcp-project", newProj); ExitCode(err) != ExitUserError {
		t.Errorf("--repo with --create-project: %v", err)
	}
	r.nothingDone(t, "flag refusals")
}

// A refusal's text is the service's, verbatim, with the likely cause — and,
// since the owner's ruling of 2026-10-23, under the one helpful line that says
// what to ask the organization for (refusedCreate, refusedLink). The line
// leads, so a reader who stops at the first line still knows what to do; the
// service's own words are still there under it, which is what this test has
// always pinned.
func TestOrgPolicyErrorVerbatim(t *testing.T) {
	r := newProjectRig(t)
	stubSelfCommand(t)
	const msg = "Constraint constraints/gcp.restrictProjectCreation violated for the organization"
	r.crm.FailCreates(403, "PERMISSION_DENIED", msg)
	_, _, err := r.run(t, true, newProj+"\n", "--create-project")
	if err == nil || !strings.Contains(err.Error(), msg) || !strings.Contains(err.Error(), "organization policy") {
		t.Errorf("err = %v: want the service's text and the likely cause", err)
	}
	if !strings.HasPrefix(err.Error(), "Fugaro could not create the project "+newProj+":") {
		t.Errorf("err = %v: want the helpful line first", err)
	}
	if len(r.fb.Adds()) != 0 {
		t.Error("Firebase was added after a refused creation")
	}

	// Billing refusals too: the text is the service's, with the likely cause.
	r = newProjectRig(t)
	stubSelfCommand(t)
	r.existing(false)
	r.bill.FailLinks(403, "PERMISSION_DENIED", "The caller does not have permission to link")
	_, _, err = r.run(t, true, billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	if err == nil || !strings.Contains(err.Error(), "The caller does not have permission to link") || !strings.Contains(err.Error(), "billing.resourceAssociations.create") {
		t.Errorf("err = %v", err)
	}
	if !strings.HasPrefix(err.Error(), "Fugaro could not link billing account "+billingAcc+" to project "+newProj+":") {
		t.Errorf("err = %v: want the helpful line first", err)
	}
}

// stubSelfCommand makes selfCommand answer "fugaro", so a test can assert the
// whole of a message that names the binary and the door (refusedLink).
func stubSelfCommand(t *testing.T) {
	t.Helper()
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
}

// stageLine is what the loop keeps of a failure for the stage's own result:
// the first line of it (initflow's oneLine, loop.go, which cuts at the first
// newline), so a test can assert what a reader of the result sees without
// reaching into the loop for it.
func stageLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// The owner's ruling of 2026-10-23: a person who is not allowed to create the
// project gets one plain line naming what to ask the organization for, the
// service's own text under it, and the run stops with exit 1 — not a raw API
// error. A bare permission refusal and an organization-policy constraint are
// the same line: from where the reader stands they are the same problem, and
// the fix (a role, or someone else creating the project) is the same one.
func TestRefusedProjectCreationIsOneHelpfulLine(t *testing.T) {
	const want = "Fugaro could not create the project " + newProj + ": your Google account is not allowed to create projects in your organization. " +
		"Ask your organization's admin (IT) for the Project Creator role on the organization or a folder, or ask them to create the project and make you its Owner. " +
		"See docs/gcp-setup.md#not-an-owner."
	// The three shapes the same refusal takes: a 403, a 400 whose own text says
	// what it is (the only thing that can tell a refusal from the other 400s
	// these APIs answer with -- infra's refusalWords), and a constraint. Each
	// has to reach the line; the 400 is the one the word list is for.
	for name, c := range map[string]struct {
		code   int
		status string
		msg    string
	}{
		"a bare permission refusal":  {403, "PERMISSION_DENIED", "The caller does not have permission"},
		"an organization policy":     {403, "PERMISSION_DENIED", "Constraint constraints/gcp.resourceLocations violated for the organization"},
		"the role named in the text": {403, "PERMISSION_DENIED", "Caller is not a project creator on the organization"},
		"a 400 that names a permission": {400, "FAILED_PRECONDITION",
			"The caller does not have permission to create projects"},
		"a 400 that names a constraint": {400, "FAILED_PRECONDITION",
			"Constraint constraints/gcp.resourceLocations violated for the organization"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newProjectRig(t)
			stubSelfCommand(t)
			r.crm.FailCreates(c.code, c.status, c.msg)
			_, _, err := r.run(t, true, newProj+"\n", "--create-project")
			if err == nil {
				t.Fatal("a refused creation exited 0")
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d (%v), want 1: a refusal is the reader's to act on, not a remote failure", ExitCode(err), err)
			}
			if !strings.HasPrefix(err.Error(), want+"\n") {
				t.Errorf("err = %q,\nwant the helpful line %q, then the service's text under it", err.Error(), want)
			}
			if strings.Count(err.Error(), "\n") != 1 || !strings.Contains(err.Error(), c.msg) {
				t.Errorf("err = %q: want the service's own text as the one line under the helpful one", err.Error())
			}
			// The stage's result carries the helpful line alone: the loop keeps
			// the first line of a failure for the stage (initflow's oneLine,
			// loop.go), so what a reader of the result sees is the line, never
			// the API's words and never a stack trace.
			if got := stageLine(err.Error()); got != want {
				t.Errorf("the stage's one line = %q, want %q", got, want)
			}
			r.nothingDone(t, "a refused creation")
		})
	}
}

// The same refusal can arrive asynchronously: the create is accepted and the
// operation completes with an error (an organization policy, say). It is the
// same case and gets the same line; a quota that comes back this way is not,
// and keeps its own message and exit code.
func TestRefusedCreationArrivingOnTheOperation(t *testing.T) {
	const want = "Fugaro could not create the project " + newProj + ": your Google account is not allowed to create projects in your organization. " +
		"Ask your organization's admin (IT) for the Project Creator role on the organization or a folder, or ask them to create the project and make you its Owner. " +
		"See docs/gcp-setup.md#not-an-owner."

	// PERMISSION_DENIED and FAILED_PRECONDITION are the two codes a refusal
	// arrives under (infra's refusedOperation); the second is how an
	// organization policy answers, and its message is what says so.
	for name, c := range map[string]struct {
		code int64
		msg  string
	}{
		"permission denied": {7, "The caller does not have permission"},
		"a constraint":      {9, "Constraint constraints/gcp.resourceLocations violated for the organization"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newProjectRig(t)
			stubSelfCommand(t)
			r.crm.FailOperation(c.code, c.msg)
			_, _, err := r.run(t, true, newProj+"\n", "--create-project")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("exit %d (%v), want 1", ExitCode(err), err)
			}
			if !strings.HasPrefix(err.Error(), want+"\n") {
				t.Errorf("err = %q,\nwant the helpful line %q first", err.Error(), want)
			}
			if !strings.Contains(err.Error(), c.msg) {
				t.Errorf("err = %q: the service's own text is gone", err.Error())
			}
			if got := stageLine(err.Error()); got != want {
				t.Errorf("the stage's one line = %q, want %q", got, want)
			}
			// The project the create recorded does not exist: the operation
			// ended in an error, and the real API leaves nothing behind.
			if r.crm.Has(newProj) {
				t.Error("the project exists after an operation that ended in an error")
			}
			r.nothingDone(t, "a refused creation")
		})
	}

	t.Run("a quota", func(t *testing.T) {
		r := newProjectRig(t)
		r.crm.FailOperation(8, "Project quota exceeded")
		_, _, err := r.run(t, true, newProj+"\n", "--create-project")
		if err == nil || ExitCode(err) != ExitRemoteError {
			t.Fatalf("exit %d (%v), want 2: a quota keeps the exit code it had", ExitCode(err), err)
		}
		if strings.Contains(err.Error(), "Fugaro could not") {
			t.Errorf("err = %q: a quota is not a missing role", err.Error())
		}
		if r.crm.Has(newProj) {
			t.Error("the project exists after an operation that ended in an error")
		}
	})
}

// The billing half: a refused link names the account, both roles the link can
// be missing, the one gcloud line that links it, and stops with exit 1. Both
// roles are named because the 403 names neither: a reader who holds the
// billing one -- adopting an existing project they are not an Owner of -- is
// exactly the reader a billing-only line would send to the wrong person, and
// the stage keeps only this first line. The account and the project are both
// in the line to run, so both are in the message.
func TestRefusedBillingLinkIsOneHelpfulLine(t *testing.T) {
	want := "Fugaro could not link billing account " + billingAcc + " to project " + newProj + ": your account is missing a role the link needs. " +
		"Ask whoever administers " + billingAcc + " for the Billing Account User role on it, and whoever owns " + newProj + " for the Owner or Project Billing Manager role on it; " +
		"either of them can link it for you instead, with the one line `gcloud billing projects link " + newProj + " --billing-account=" + billingAcc + "`. " +
		"Then run `fugaro setup` again. If you hold both roles already, the billing account may be closed, or at its limit of linked projects."
	r := newProjectRig(t)
	stubSelfCommand(t)
	r.existing(false)
	r.bill.FailLinks(403, "PERMISSION_DENIED", "The caller does not have permission")
	_, _, err := r.run(t, true, billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	if err == nil {
		t.Fatal("a refused link exited 0")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d (%v), want 1", ExitCode(err), err)
	}
	if !strings.HasPrefix(err.Error(), want+"\n") {
		t.Errorf("err = %q,\nwant the helpful line %q first", err.Error(), want)
	}
	if !strings.Contains(err.Error(), "The caller does not have permission") {
		t.Errorf("err = %q: the service's own text is gone", err.Error())
	}
	if got := stageLine(err.Error()); got != want {
		t.Errorf("the stage's one line = %q, want %q", got, want)
	}
	if n := r.bill.Attempts(); n != 1 {
		t.Errorf("%d write(s) attempted, want 1 (the link that was refused)", n)
	}
	r.nothingDone(t, "a refused link")
}

// The two cases the ruling leaves alone. A quota and a failure nothing
// recognises keep the message they always had: no helpful line is invented for
// them, their text is not swallowed, and their exit code does not change. A
// closed billing account is the third: it is a 400, not a missing role, and a
// helpful line about Billing Account User would be false of it.
func TestProjectQuotaAndUnknownFailuresKeepTheirOwnMessages(t *testing.T) {
	t.Run("a quota", func(t *testing.T) {
		r := newProjectRig(t)
		r.crm.FailCreates(429, "RESOURCE_EXHAUSTED", "Project quota exceeded")
		_, _, err := r.run(t, true, newProj+"\n", "--create-project")
		if err == nil || ExitCode(err) != ExitRemoteError {
			t.Fatalf("exit %d (%v), want 2: a quota keeps the exit code it had", ExitCode(err), err)
		}
		if !strings.Contains(err.Error(), "Project quota exceeded") || strings.Contains(err.Error(), "Fugaro could not") {
			t.Errorf("err = %q: a quota keeps its own message", err.Error())
		}
	})

	t.Run("an unrecognised failure", func(t *testing.T) {
		r := newProjectRig(t)
		r.crm.FailCreates(500, "INTERNAL", "the backend went away")
		_, _, err := r.run(t, true, newProj+"\n", "--create-project")
		if err == nil || ExitCode(err) != ExitRemoteError {
			t.Fatalf("exit %d (%v), want 2", ExitCode(err), err)
		}
		if !strings.HasPrefix(err.Error(), "creating project "+newProj+": ") || !strings.Contains(err.Error(), "the backend went away") {
			t.Errorf("err = %q: an unrecognised failure is unchanged", err.Error())
		}
		if strings.Contains(err.Error(), "Fugaro could not") || strings.Contains(err.Error(), "\n") {
			t.Errorf("err = %q: nothing was prepended to it", err.Error())
		}
	})

	t.Run("a closed billing account", func(t *testing.T) {
		r := newProjectRig(t)
		r.existing(false)
		r.bill.AddAccount(billingAcc, false)
		_, _, err := r.run(t, true, billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("err = %v: want the service's own text about the closed account", err)
		}
		if strings.Contains(err.Error(), "Fugaro could not link billing account") {
			t.Errorf("err = %q: a closed account is not a missing role, and must not be told it is", err.Error())
		}
	})
}

// The project is read by its ID alone: the gcloud default project (and the
// environment's) is never used, in a call or as the ID.
func TestNeverUsesGcloudDefaultProject(t *testing.T) {
	r := newProjectRig(t)
	const dflt = "their-default-9999"
	if _, _, err := executeStdin(t, dflt+"\n", "setup", "--create-project"); ExitCode(err) != ExitUserError {
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
	if n := r.bill.Attempts(); n != 0 {
		t.Errorf("unreadable billing: %d write(s) attempted", n)
	}
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "cannot be read") || !strings.Contains(out, "gcloud billing projects link") {
		t.Errorf("exit %d\n%s", ExitCode(err), out)
	}

	r = newProjectRig(t)
	r.existing(false)
	r.bill.SetSuspended(newProj, "AAAAAA-BBBBBB-CCCCCC")
	out, _, err = r.run(t, true, billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	r.nothingDone(t, "a suspended link")
	if n := r.bill.Attempts(); n != 0 {
		t.Errorf("a suspended link: %d write(s) attempted", n)
	}
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

// A project this run created is still linked when its billing cannot be
// read right after creation: the write is attempted (here refused by the
// fake's 403, which is the error shown).
func TestCreatedHereUnreadableBillingStillAttempted(t *testing.T) {
	r := newProjectRig(t)
	r.bill.HideBilling(newProj)
	_, _, err := r.run(t, true, newProj+"\n"+billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	if err == nil || r.bill.Attempts() != 1 {
		t.Errorf("err %v, attempts %d: the link should be attempted once", err, r.bill.Attempts())
	}
}

// onRead runs fn the first time the reader is read: the moment the person
// is at the prompt.
type onRead struct {
	r  *strings.Reader
	fn func()
}

func (o *onRead) Read(p []byte) (int, error) {
	if o.fn != nil {
		o.fn()
		o.fn = nil
	}
	return o.r.Read(p)
}

// Billing becomes linked between the decision and the write (while the
// confirmation is typed): the write is not sent.
func TestBillingLinkedWhileConfirmingIsNotReplaced(t *testing.T) {
	r := newProjectRig(t)
	r.existing(false)
	fakeTerminal(t)
	cmd := NewRootCmd()
	var out, errOut strings.Builder
	cmd.SetIn(&onRead{r: strings.NewReader(billingAcc + "\n"), fn: func() { r.bill.SetBilling(newProj, true) }})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"setup", "--gcp-project", newProj, "--create-project", "--link-billing", billingAcc})
	err := cmd.Execute()
	if n := r.bill.Attempts(); n != 0 {
		t.Errorf("%d write(s) attempted\n%s", n, out.String())
	}
	if ExitCode(err) != ExitUserError || !strings.Contains(out.String(), "never replaces a link") {
		t.Errorf("exit %d (%v)\n%s", ExitCode(err), err, out.String())
	}
}

// The project stage's "nothing further was done" refusal tells the reader which
// door to rerun, and the two doors are not interchangeable: `fugaro setup`
// creates, `fugaro update` re-converges. The reader who typed the wrong name is
// exactly the one who cannot tell them apart, so a run reached through update
// must not be sent back to setup by the line that says nothing was done (task
// W2.1).
func TestTheDeclinedProjectPromptNamesTheDoorTheReaderIsAt(t *testing.T) {
	for _, door := range []string{"setup", "update"} {
		s := &projectStage{e: &initEngine{r: &initRun{o: &initOptions{door: door}}}}
		var ny *initflow.NeedsYouError
		if err := s.declined("the project's ID"); !asNeedsYou(err, &ny) {
			t.Fatalf("%s: %v", door, err)
		}
		if !strings.Contains(ny.Left.Text, "rerun "+selfCommand()+" "+door+" in your own terminal window") {
			t.Errorf("%s: the refusal does not name the door the reader is at: %q", door, ny.Left.Text)
		}
	}
}

// TestFirebaseProjectPairingRuleIsUnchanged: a project this run creates is its
// own Firebase project, and --firebase-project is the one way to say it is
// another one — which is refused, naming both ids, before anything is created
// (D7, F4). It is the case that was TestCreateWithOtherFirebaseRefused, ported
// onto the new name rather than kept beside it: the rule is the one --firebase
// had, and the port is the point, because a rename that quietly dropped the
// pairing rule would leave the creation building a budget backend in a project
// it does not create.
func TestFirebaseProjectPairingRuleIsUnchanged(t *testing.T) {
	r := newProjectRig(t)
	_, _, err := r.run(t, true, newProj+"\n", "--create-project", "--firebase-project", "other-fp-1234")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "other-fp-1234") {
		t.Errorf("err = %v", err)
	}
	if n := len(r.crm.Requests()); n != 0 {
		t.Errorf("%d calls", n)
	}
	// The other half of the same rule, on the flags alone: naming the project
	// this run creates is what the default says, and is not refused.
	o := &initOptions{door: setupDoor.name, createProject: true, cloud: cloudOptions{gcpProject: newProj}, firebase: newProj}
	if err := o.check(); err != nil {
		t.Errorf("--create-project --firebase-project %s (the project it creates) was refused: %v", newProj, err)
	}
}

// TestFirebaseProjectIsRefusedWithoutCreateProject pins where the pairing
// refusal lives, from both sides of it: it is checkCreateProject's, so it fires
// for a run that creates the project and is not reached by a run that creates
// nothing — where a Firebase project other than the installation's own is the
// ordinary two-project layout (init_firebase.go's head comment) and must stay
// accepted. A rule hoisted out of the creating branch would keep the first half
// green and break the second, which is what the pair is here for.
func TestFirebaseProjectIsRefusedWithoutCreateProject(t *testing.T) {
	// A creation whose Firebase project is another one: refused, naming the
	// flag and the project.
	with := &initOptions{door: setupDoor.name, createProject: true, cloud: cloudOptions{gcpProject: newProj}, firebase: fpID}
	err := with.check()
	if err == nil || !strings.Contains(err.Error(), fpID) || !strings.Contains(err.Error(), "--firebase-project") {
		t.Errorf("--create-project with --firebase-project %s: err = %v, want a refusal naming the flag and the project", fpID, err)
	}
	// The same flags with no creation: the pair of projects is the adopt path.
	without := &initOptions{door: setupDoor.name, cloud: cloudOptions{gcpProject: newProj}, firebase: fpID}
	if err := without.check(); err != nil {
		t.Errorf("--firebase-project %s with no --create-project: %v, want nil (the two-project layout)", fpID, err)
	}
}

// TestTheBillingListIsNarrow pins the two halves of the rule W4.5 leaves
// behind (docs/setup-wizard/implementation-plan.md, task W4.5). The project
// stage's rule had two clauses: init never lists the caller's billing
// accounts, and it never prints the account a project is already linked to.
// W4.3 made the first false for exactly one call site — the wizard's billing
// question, at a real terminal — so what this test pins is how narrow that
// reversal is: the account list is asked for from one place in the whole tree,
// in a file that reads canType() only to refuse. And the second clause is
// untouched, in the stage and in the question both: a run over a project that
// IS linked to an account never prints that account.
func TestTheBillingListIsNarrow(t *testing.T) {
	// The reversal is one call site in the tree, and it is the question's.
	var callers []string
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || (name != "." && name != ".." && strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if billingListCallRE.Match(b) {
			callers = append(callers, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.ToSlash(filepath.Join(root, "internal", "cli", "billing_prompt.go")); len(callers) != 1 || callers[0] != want {
		t.Errorf("the caller's billing accounts are listed from %v, want only %s: a second call site is a wider reversal than the owner answered (D6)", callers, want)
	}
	// That file reads canType() only to refuse, so the one list call cannot be
	// reached on a path that skipped it (Global Constraint 8).
	b, err := os.ReadFile("billing_prompt.go")
	if err != nil {
		t.Fatal(err)
	}
	reads, guards := len(canTypeReadRE.FindAll(b, -1)), len(canTypeGuardRE.FindAll(b, -1))
	if reads == 0 || reads != guards {
		t.Errorf("billing_prompt.go reads canType() %d time(s), %d of them as a refusal: the account list may be asked for only where canType() says yes", reads, guards)
	}
	// And the call is inside askBilling, after the asksBilling() guard that
	// reads it: the two halves of the condition, in one function, in order.
	body := funcBody(t, string(b), "func (r *initRun) askBilling(")
	guard, call := strings.Index(body, "if !r.asksBilling() {"), billingListCallRE.FindStringIndex(body)
	if guard < 0 || call == nil || call[0] < guard {
		t.Errorf("askBilling does not reach the account list through its asksBilling() guard (the guard at %d, the call at %v): the question's condition is askedBilling, and the list may be asked for only past it", guard, call)
	}

	// The half of the old rule that survives. The project is linked to an
	// account that is not enabled, which is the one state the stage reports
	// and never replaces — and the run is at a terminal, with a source that
	// would answer, so nothing but the rule keeps the account out of the
	// output.
	r := newProjectRig(t)
	linked := pickerAccountID(t, "linked")
	offered := pickerAccountID(t, "offered")
	r.crm.AddProject(newProj, 424242)
	r.gcs.AddProject(newProj, 424242)
	r.fb.AddFirebase(newProj)
	r.bill.SetSuspended(newProj, linked)
	f := gcpfake.NewBilling(t)
	f.AddAccountNamed(offered, "an account this run could link", true)
	pickerSources(t, f) // after newProjectRig, which stubs the sources out
	out, _, err := r.run(t, true, newProj+"\n"+linked+"\n", "--create-project", "--link-billing", linked)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d (%v), want 1: the link a project already has is the user's to change\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(out, "never replaces an existing link") {
		t.Errorf("the run does not report the link it found, so this is not the path that knows about it:\n%s", out)
	}
	for _, leak := range []string{linked, offered, "billingAccounts/"} {
		if strings.Contains(out, leak) {
			t.Errorf("the account a project is already linked to reached the output (%q):\n%s", leak, out)
		}
	}
	if n, m := billingListCalls(f), billingListCalls(r.bill); n != 0 || m != 0 {
		t.Errorf("the accounts were listed: %d call(s) from the question's source, %d from the stage's", n, m)
	}
	if len(r.bill.Links()) != 0 || r.bill.Attempts() != 0 {
		t.Errorf("the link the project already had was written: %d link(s), %d attempt(s)", len(r.bill.Links()), r.bill.Attempts())
	}
}
