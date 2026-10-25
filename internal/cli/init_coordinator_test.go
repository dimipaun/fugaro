package cli

import (
	"strings"
	"testing"
)

// The coordinator's aggregation job is the owner's opt-in (coordinator plan
// P5; the security review's MAJOR-1): its account holds the Firebase project's
// database admin and its job holds the GitHub App's key, so nothing creates
// either until the owner runs fugaro update --firebase-project <id>
// --coordinator at a terminal, and the opt-in is then recorded so later runs
// keep it.

// coordinatorAccount is the coordinator's account as the installation's
// outputs name it once the owner has opted in.
const coordinatorAccount = "fugaro-coordinator@proj-1234.iam.gserviceaccount.com"

// TestTheCoordinatorIsOffByDefault: a budget installation's ordinary
// --firebase-project run plans no coordinator in the installation root and
// hands the Firebase root no account to grant the database's admin to, and
// the local config records no opt-in.
//
// Fails when infra.Installation sets the coordinator for every budget backend
// (the code this review found), or infra.Firebase derives its account anyway.
func TestTheCoordinatorIsOffByDefault(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	if v, ok := r.tfvars(t)["coordinator"]; ok {
		t.Errorf("the installation's tfvars carry a coordinator without the opt-in: %v", v)
	}
	if got := r.fbVars(t)["coordinator_account"]; got != "" {
		t.Errorf("the Firebase root grants the database's admin to %v without the opt-in", got)
	}
	if r.localConfig(t).Terraform.Coordinator {
		t.Errorf("the local config records the coordinator's opt-in after a run that did not ask")
	}
}

// TestTheCoordinatorOptInIsRecordedAndKept: --coordinator at a terminal plans
// the coordinator in the installation root, hands its account to the Firebase
// root, and records the opt-in once the installation's outputs carry the
// account — so a later run without the flag keeps it rather than planning
// its removal.
func TestTheCoordinatorOptInIsRecordedAndKept(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"history_service_account": historyAccount, "history_job": nil, "coordinator_service_account": coordinatorAccount})}
	r.save(t)
	fakeTerminal(t)
	out, _, err := executeStdin(t, names(3)+"us-east5\n"+names(1), "setup", "--firebase-project", fpID, "--coordinator")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	c, ok := r.tfvars(t)["coordinator"].(map[string]any)
	if !ok || c["account_id"] != "fugaro-coordinator" {
		t.Fatalf("the opt-in planned no coordinator: %v", r.tfvars(t)["coordinator"])
	}
	if got := r.fbVars(t)["coordinator_account"]; got != coordinatorAccount {
		t.Errorf("the Firebase root's coordinator_account = %v, want %s", got, coordinatorAccount)
	}
	if !r.localConfig(t).Terraform.Coordinator {
		t.Fatalf("the opt-in is not recorded in the local config")
	}

	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.tfvars(t)["coordinator"]; !ok {
		t.Errorf("a later run without the flag dropped the recorded coordinator")
	}
}

// TestTheCoordinatorOptInIsRefusedWhereItCannotBeConfirmed: the opt-in
// follows --provision-credentials — --yes never covers it, it excludes the
// flags that only read or only stop and --repo — and it needs
// --firebase-project, which is the run that grants the account's one role.
func TestTheCoordinatorOptInIsRefusedWhereItCannotBeConfirmed(t *testing.T) {
	for _, o := range []*initOptions{
		{coordinator: true},
		{coordinator: true, firebase: fpID, yes: true},
		{coordinator: true, firebase: fpID, forget: true},
		{coordinator: true, firebase: fpID, printVars: true},
		{coordinator: true, firebase: fpID, configOnly: true},
		{coordinator: true, firebase: fpID, publishConfig: true},
	} {
		if err := o.check(); err == nil || !strings.Contains(err.Error(), "--coordinator") {
			t.Errorf("%+v: err = %v, want it refused", o, err)
		}
	}
	if err := (&initOptions{coordinator: true, repo: true}).checkRepo(); err == nil || !strings.Contains(err.Error(), "--coordinator") {
		t.Errorf("--coordinator with --repo: err = %v, want it refused", err)
	}
	if err := (&initOptions{coordinator: true, firebase: fpID}).check(); err != nil {
		t.Errorf("--firebase-project --coordinator: %v", err)
	}
}

// TestTheCoordinatorOptInRefusesAnUnattendedRun: a coding agent's session and
// a run with no terminal are refused before anything is read or applied.
func TestTheCoordinatorOptInRefusesAnUnattendedRun(t *testing.T) {
	r := newFBRig(t)
	t.Setenv("CLAUDECODE", "1")
	fakeTerminal(t)
	_, _, err := executeStdin(t, "", "update", "--firebase-project", fpID, "--coordinator")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--coordinator") {
		t.Errorf("in an agent's session: exit %d, err %v", ExitCode(err), err)
	}
	t.Setenv("CLAUDECODE", "")
	noTerminal(t)
	_, _, err = executeStdin(t, "", "update", "--firebase-project", fpID, "--coordinator")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "terminal") {
		t.Errorf("without a terminal: exit %d, err %v", ExitCode(err), err)
	}
	if got := r.applies(t); len(got) != 0 {
		t.Errorf("a refused opt-in applied %v", got)
	}
}
