package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/infra"
)

const enableCommand = "gcloud services enable cloudresourcemanager.googleapis.com --project proj-1234"

// noEnableWait makes the retries after an enable immediate.
func noEnableWait(t *testing.T, n int) {
	t.Helper()
	old := resourceManagerRetries
	resourceManagerRetries = make([]time.Duration, n)
	t.Cleanup(func() { resourceManagerRetries = old })
}

// With Cloud Resource Manager disabled, --yes enables it through Service
// Usage (a change even under --plan-only), waits for the enable to
// propagate, and carries on.
func TestInitEnablesResourceManager(t *testing.T) {
	noEnableWait(t, 5)
	r := newInitRig(t)
	r.stateBucket()
	r.su.Disable(infra.ServiceResourceManager, r.crm.Server)
	r.su.Propagation = 2
	out, errOut, err := executeStdin(t, "", "init", "--plan-only", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if got := r.su.Enables(); !slices.Equal(got, []string{infra.ServiceResourceManager}) {
		t.Fatalf("enables = %q", got)
	}
	if !strings.Contains(out, "⚠ CONFIRM (project proj-1234): enables the Cloud Resource Manager API (cloudresourcemanager.googleapis.com)") ||
		!strings.Contains(out, "enabled cloudresourcemanager.googleapis.com in proj-1234") {
		t.Errorf("no confirmation or change line:\n%s", out)
	}
	if n := strings.Count(errOut, "waiting for the Cloud Resource Manager API"); n != 2 {
		t.Errorf("%d progress lines on stderr, want 2:\n%s", n, errOut)
	}
	if len(r.ran(t, "plan")) != 1 || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("calls = %q", r.calls(t))
	}

	// --json lists it among the changes made.
	r = newInitRig(t)
	r.stateBucket()
	r.su.Disable(infra.ServiceResourceManager, r.crm.Server)
	out, _, err = executeStdin(t, "", "init", "--plan-only", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Enabled []string `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || !slices.Equal(res.Enabled, []string{infra.ServiceResourceManager}) {
		t.Fatalf("result %s: %+v, %v", out, res, err)
	}
}

// An enabled Resource Manager asks nothing and enables nothing.
func TestInitResourceManagerEnabledAsksNothing(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	out, _, err := executeStdin(t, "", "init", "--plan-only", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.su.Requests()) != 0 || strings.Contains(out, "Cloud Resource Manager") {
		t.Fatalf("service usage calls %v, output:\n%s", r.su.Requests(), out)
	}
}

// Without a terminal and without --yes, nothing is enabled: exit 1 with
// the command to run by hand. At a terminal, a wrong answer is the same.
func TestInitResourceManagerDisabledNeedsConfirmation(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.su.Disable(infra.ServiceResourceManager, r.crm.Server)
	_, _, err := executeStdin(t, "", "init", "--plan-only")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), enableCommand) || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.su.Enables()) != 0 || len(r.calls(t)) > 1 { // terraform version only
		t.Fatalf("enables %q, terraform calls %q", r.su.Enables(), r.calls(t))
	}

	fakeTerminal(t)
	_, _, err = executeStdin(t, "yes\n", "init", "--plan-only")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), enableCommand) || len(r.su.Enables()) != 0 {
		t.Fatalf("declined at a terminal: exit %d, err %v, enables %q", ExitCode(err), err, r.su.Enables())
	}
	noEnableWait(t, 1)
	if _, _, err := executeStdin(t, initProject+"\n", "init", "--plan-only"); err != nil || len(r.su.Enables()) != 1 {
		t.Fatalf("typed project ID: %v, enables %q", err, r.su.Enables())
	}
}

// A refused enable (no permission to enable services) is a remote error.
func TestInitResourceManagerEnableRefused(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.su.Disable(infra.ServiceResourceManager, r.crm.Server)
	r.su.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "AUTH_PERMISSION_DENIED", "Permission denied to enable service [cloudresourcemanager.googleapis.com]")
	_, _, err := executeStdin(t, "", "init", "--plan-only", "--yes")
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "Permission denied to enable service") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "plan")) != 0 {
		t.Fatalf("calls = %q", r.calls(t))
	}
}

// An enable that never propagates within the retries is a remote error
// that says to rerun.
func TestInitResourceManagerNeverPropagates(t *testing.T) {
	noEnableWait(t, 3)
	r := newInitRig(t)
	r.stateBucket()
	r.su.Disable(infra.ServiceResourceManager, r.crm.Server)
	r.su.Propagation = 100
	_, _, err := executeStdin(t, "", "init", "--plan-only", "--yes")
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "rerun fugaro init") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// A disabled API other than Resource Manager holds nothing, so init plans
// the create of what it holds: Cloud Logging's log bucket here.
func TestInitDisabledLoggingPlansCreate(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.logs.AddBucket(initProject, "global", infra.LogBucket, "ACTIVE", infra.LogBucketDescription)
	r.su.Disable("logging.googleapis.com", r.logs.Server)
	if _, errOut, err := executeStdin(t, "", "init", "--plan-only"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if len(r.su.Enables()) != 0 {
		t.Errorf("enables = %q; only Terraform enables Cloud Logging", r.su.Enables())
	}
	b, err := os.ReadFile(filepath.Join(r.root(), infra.ImportsFile))
	if err != nil || strings.Contains(string(b), "google_logging") {
		t.Fatalf("imports (%v):\n%s", err, b)
	}
}
