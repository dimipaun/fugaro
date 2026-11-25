package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// What the standalone --repo door prints, on the terminal and in the log.
//
// The owner ran `fugaro update --repo` in a sandbox checkout on 2026-11-24 and
// read two terraform init banners, every "Refreshing state" line, Terraform's
// whole plan with its resource blocks and attribute paths, and a refusal that
// ended by telling them — the owner of that project, at their own terminal —
// to hand the refusal to the project's owner. None of it said what the update
// was doing. These tests are the owner's answer, and they are one property
// each: Terraform's own output goes to the log and only the log, the step's
// one line carries the counts and where the rest is, and the refusal a person
// reads is short, bounded and addressed to them.

// terraformNoise is what terraform prints on a run of its own: the init
// banner, a refresh line, and the plan in Terraform's words rather than
// Fugaro's. The four strings are the ones the owner named, and the door's
// terminal may carry none of them.
const terraformNoise = "Initializing the backend...\n" +
	"Refreshing state... [id=projects/proj-1234/locations/us-east5/jobs/fugaro-acme-sandbox-app]\n" +
	"# module.repo.google_service_account.job will be created\n" +
	"resource \"google_service_account\" \"job\" {\n"

// terraformNoiseMarks are those strings, one by one, for the assertions below.
var terraformNoiseMarks = []string{"Initializing", "Refreshing state", "will be created", `resource "google_`}

// readOut is everything a run of the door put in front of the person who ran
// it: what it printed, and the refusal or failure it returned — which is the
// command's own text and is printed under `fugaro:` by PrintFailure, the one
// line the harness holds no writer for.
func readOut(out, errOut string, err error) string {
	if err == nil {
		return out + errOut
	}
	return out + errOut + err.Error()
}

// repoOutputRig is the door's rig at a terminal: the checkout of acme/sandbox,
// ready to plan, with no retirement and no cloud to speak of. The plan each
// test scripts at the repository root is what the run plans.
func repoOutputRig(t *testing.T) *initRig {
	t.Helper()
	r := newInitRig(t)
	r.repoReady(t)
	t.Chdir(repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "oauth", "aurora", "")))
	fakeTerminal(t)
	return r
}

// repoDeletePlan scripts a plan at the repository root holding one delete per
// address, which the guard refuses one for one: the count a test wants to read
// back off the terminal is the number it passes here.
func repoDeletePlan(t *testing.T, r *initRig, addrs ...string) {
	t.Helper()
	var changes []planChange
	for _, a := range addrs {
		changes = append(changes, retireChange(a, "google_storage_bucket_iam_member", "delete"))
	}
	r.setPlanAt(t, "repo", changes...)
	r.script["plan"] = map[string]any{"exit": 2} // the plan changes something
	r.save(t)
}

// Terraform's own output is the log's, and the terminal's account of the plan
// is one line carrying the counts and where the whole of it is kept (D1352,
// D1354). The four strings the owner read are the assertion: nothing of
// Terraform's reaches the screen, and every one of them is in the log the step
// line names.
func TestUpdateRepoKeepsTerraformsOutputInTheLog(t *testing.T) {
	r := repoOutputRig(t)
	before := logNames(t, "update")
	repoDeletePlan(t, r, retireSvcAddr)
	r.script["init"] = map[string]any{"stdout": terraformNoise}
	r.script["plan"] = map[string]any{"exit": 2, "stdout": terraformNoise}
	r.save(t)

	out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build", "--allow-delete", retireSvcAddr)
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := readOut(out, errOut, err)
	if !strings.Contains(text, "✔ repository acme/sandbox 1 to delete (log: ") {
		t.Errorf("the terminal does not carry the plan's one line with its counts and the log to read:\n%s", text)
	}
	for _, mark := range terraformNoiseMarks {
		if strings.Contains(text, mark) {
			t.Errorf("the terminal carries terraform's own %q:\n%s", mark, text)
		}
	}
	log := logsAdded(t, before, "update")
	if log == "" {
		t.Fatal("the run kept no log, so the whole of what it printed is the screen it was supposed not to fill")
	}
	for _, mark := range terraformNoiseMarks {
		if !strings.Contains(log, mark) {
			t.Errorf("the log does not carry terraform's own %q:\n%s", mark, log)
		}
	}
}

// The guard refusal a person at the terminal reads: what the plan does and how
// much of it, five of the refused addresses and a pointer at the log for the
// rest, and the one command that goes on. It says nothing about a session and
// sends nobody to the project's owner — the reader is the owner (the owner's
// feedback of 2026-11-24).
func TestUpdateRepoGuardRefusalIsShortAtTheTerminal(t *testing.T) {
	newRetireRig(t, retireOpts{protection: false})
	before := logNames(t, "update")
	out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s; want the guard's refusal", ExitCode(err), err, out, errOut)
	}
	text := readOut(out, errOut, err)
	for _, want := range []string{
		"this run would delete or replace 1 resource",
		"what goes: ",
		"refused (1):\n  " + retireSvcAddr,
		tf.AllowDeleteAdvice,
		"Next command:\n  " + retireNextCommand,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, text)
		}
	}
	for _, never := range []string{"this session", "the person who owns the project", "Review these first", "hint: "} {
		if strings.Contains(text, never) {
			t.Errorf("the person's own refusal carries %q:\n%s", never, text)
		}
	}
	// Every refused address is still the log's: the block shortens the list it
	// prints and never the record of what refused it.
	if !strings.Contains(logsAdded(t, before, "update"), retireSvcAddr) {
		t.Errorf("the log does not hold the refused address:\n%s", text)
	}
}

// Six refused addresses, five lines: the sixth is a count and a pointer
// (refusalShown), and the address itself is in the log the pointer names.
func TestUpdateRepoRefusalListsFiveAddressesAndPointsAtTheLog(t *testing.T) {
	r := repoOutputRig(t)
	before := logNames(t, "update")
	var addrs []string
	for i := range refusalShown + 1 {
		addrs = append(addrs, fmt.Sprintf(`module.repo.google_storage_bucket_iam_member.state["user:g%d@example.com"]`, i))
	}
	repoDeletePlan(t, r, addrs...)

	out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s; want the guard's refusal", ExitCode(err), err, out, errOut)
	}
	text := readOut(out, errOut, err)
	if !strings.Contains(text, fmt.Sprintf("this run would delete or replace %d resources", len(addrs))) {
		t.Errorf("the refusal does not carry the count %d:\n%s", len(addrs), text)
	}
	if !strings.Contains(text, fmt.Sprintf("refused (%d):", len(addrs))) {
		t.Errorf("the refusal does not carry the refused list's own count:\n%s", text)
	}
	for _, a := range addrs[:refusalShown] {
		if !strings.Contains(text, a) {
			t.Errorf("the refusal leaves out %q:\n%s", a, text)
		}
	}
	if strings.Contains(text, addrs[refusalShown]) {
		t.Errorf("the terminal carries the %dth address, past the five a person reads:\n%s", refusalShown+1, text)
	}
	if !strings.Contains(text, fmt.Sprintf("+1 more, see %s", logDir(t))) {
		t.Errorf("the refusal does not send the reader to the log for the rest:\n%s", text)
	}
	log := logsAdded(t, before, "update")
	if !strings.Contains(log, addrs[refusalShown]) {
		t.Errorf("the log does not hold the address the terminal left out:\n%s", log)
	}
}

// A coding agent's session is refused in the words written for it — the
// address, the owner as the one who decides, and no flag to pass — and the
// person at the terminal, on the same plan, reads the short block instead. One
// project, one plan, two callers: what changes is the reader (D72/D74,
// initManagedDeleteAdvice).
func TestUpdateRepoGuardRefusalSplitsTheAgentFromThePerson(t *testing.T) {
	for _, tc := range []struct {
		name   string
		agent  bool
		want   string
		absent string
	}{
		{name: "the person at the terminal", want: "this run would delete or replace 1 resource",
			absent: initManagedDeleteAdvice},
		{name: "a coding agent's session", agent: true, want: initManagedDeleteAdvice,
			absent: "this run would delete or replace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := repoOutputRig(t)
			// The creation fact: the project's own label and the object in its
			// runs bucket are both halves of "this project is managed" (Task
			// 0.2), and the agent's sentence is the managed refusal's alone.
			r.managedRuns(t, initProjectName, initProject)
			repoDeletePlan(t, r, retireSvcAddr)
			if tc.agent {
				t.Setenv("CLAUDECODE", "1")
			}
			out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
			if ExitCode(err) != ExitUserError {
				t.Fatalf("exit %d, err %v\n%s%s; want the guard's refusal", ExitCode(err), err, out, errOut)
			}
			text := readOut(out, errOut, err)
			if !strings.Contains(text, tc.want) {
				t.Errorf("the refusal does not say %q:\n%s", tc.want, text)
			}
			if strings.Contains(text, tc.absent) {
				t.Errorf("the refusal carries %q, which belongs to the other reader:\n%s", tc.absent, text)
			}
			if !strings.Contains(text, retireSvcAddr) {
				t.Errorf("the refusal does not name the refused address:\n%s", text)
			}
		})
	}
}

// --json is unchanged by all of it: the document's error is Guard's own text,
// every refused address in it and no pointer at a log (report_exempt, the
// contract the console and every script read).
func TestUpdateRepoRefusalJSONKeepsGuardsOwnText(t *testing.T) {
	r := repoOutputRig(t)
	addrs := []string{retireSvcAddr, retireRegistryAddr}
	repoDeletePlan(t, r, addrs...)
	out, _, err := executeStdin(t, "", "update", "--repo", "--no-build", "--json")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s; want the guard's refusal", ExitCode(err), err, out)
	}
	var doc struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not the one --json document: %v\n%s", err, out)
	}
	for _, a := range addrs {
		if !strings.Contains(doc.Error, a) {
			t.Errorf("the document's error leaves out %q:\n%s", a, doc.Error)
		}
	}
	if !strings.Contains(doc.Error, tf.AllowDeleteAdvice) {
		t.Errorf("the document's error is not Guard's own refusal:\n%s", doc.Error)
	}
	for _, never := range []string{"more, see", "this run would delete or replace"} {
		if strings.Contains(doc.Error, never) {
			t.Errorf("the document's error carries the terminal's block (%q):\n%s", never, doc.Error)
		}
	}
	if !strings.HasPrefix(doc.Error, "the plan would delete or replace 2 resource(s)") {
		t.Errorf("the document's error is not Guard's own sentence:\n%s", doc.Error)
	}
}
