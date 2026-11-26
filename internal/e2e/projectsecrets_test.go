package e2e

// The project-shared workflow secret, end to end and hermetically: the owner's
// allow (IS3), the migration's copy (IS5) and the record `fugaro update` writes
// (B3.1), read back through the REAL route (IS1) by the call the runner sends
// (B3.4, IS2), plus the two states the path exists to tell apart -- a repository
// the owner's list does not admit, and a name the migration has not copied yet.
//
// It is a composition test and that is its whole point: each of those pieces has
// its own tests, and what none of them can say is that the record one command
// writes is the record the route reads, that the container the copy fills is the
// container the route names, and that the call the runner builds is the call the
// route answers. The world is the installation's own as credsvcfake wires it (a
// Realtime Database and a Secret Manager, both fake, with the service's readers
// over them), so nothing here reaches a network or a cloud.
//
// What is NOT here is the runner's own half: `internal/e2e` drives the CLI's
// binary and cannot reach the runner's unexported fetch, so the fetch with a
// `file: true` secret -- the value registered for redaction, written 0400 under
// the temporary directory, and the variable pointed at the path -- is pinned in
// internal/runner's tests (secretfile_test.go, workflowsecrets_test.go), which
// send these same calls against this same route.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/credsvcfake"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// The fixture's names, and the values the copy moves. No literal here is, or
// looks like, a credential (Convention 2).
const (
	projectSecretName  = "deploy-token"
	projectSecretEnv   = "DEPLOY_TOKEN"
	projectSecretValue = "project-copied-workflow-secret-5c17"
	repositorySecretID = "fixture-repo-deploy-token"
)

// askWorkflowSecret makes the call the runner makes: one workflow secrets call
// to the installation's service, presented the way a run presents one -- the
// job's own ID token as the bearer, the run identity, and the job the run
// reports (credsvc's contract, read by RequestFromHTTP before any route). The
// body is the version's own, given whole, because that is what the two versions
// differ in.
func askWorkflowSecret(t *testing.T, inst *credsvcfake.Installation, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, inst.URL+credsvc.WorkflowSecretsPath, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer job-id-token-from-the-test")
	req.Header.Set(gateway.HeaderRunIdentity, "run-id-token-from-the-test")
	req.Header.Set(gateway.HeaderRunJob, workerJobName(inst))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

// workerJobName is the job the fixture repository's workflow runs in, as the
// platform names it (gcp.JobName): the name the contract admits
// (credsvc.ValidJobName) and the name a run of this repository reports.
func workerJobName(inst *credsvcfake.Installation) string {
	return gcp.JobName(inst.Slug, gcp.WorkflowVariant(inst.Workflow, ""))
}

// versionTwo is the runner's version-2 call, byte for byte: the logical name
// and the version that says which shape this is (D3324, D3333).
func versionTwo(name string) string {
	return `{"name":"` + name + `","version":2}`
}

// versionOne is the same call in the vocabulary the flip's record is written
// in, which is what the runner falls back to.
func versionOne(env string) string {
	return `{"env":"` + env + `"}`
}

// TestProjectSecretFetchedEndToEnd is the served path whole: the owner admits a
// repository to a name (IS3), the migration copies the value into the grammar's
// container (IS5), `fugaro update` records the repository's own variable (B3.1),
// and the call the runner sends by NAME is answered with the copied value.
//
// The assertions are the joins between the pieces: the answer names the secret
// the call named and carries the value the copy wrote, and the container it was
// read from is the grammar's own -- secretname.Project of the name, which is
// what the route builds and what the copy filled -- so a route reading another
// container, or a copy writing one, fails here. The service audited the act
// once, which is the other half of the contract a served value keeps (C1.13).
func TestProjectSecretFetchedEndToEnd(t *testing.T) {
	inst := credsvcfake.New(t, credsvcfake.Options{Repo: "acme/webapp", Workflow: "app"})
	// The owner's allow, then the migration's copy: what `fugaro secrets allow`
	// and the release's own migration leave.
	inst.Allow(t, projectSecretName)
	inst.SetShared(t, projectSecretName, projectSecretValue)

	status, body := askWorkflowSecret(t, inst, versionTwo(projectSecretName))
	if status != http.StatusOK {
		t.Fatalf("the service answered %d: %s", status, body)
	}
	var ans credsvc.WorkflowSecretsResponse
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("the answer is not this contract's: %v", err)
	}
	if ans.Name != projectSecretName {
		t.Errorf("the answer names %q, want the secret the call named: an answer is never read as another secret's", ans.Name)
	}
	if ans.Value != projectSecretValue {
		t.Errorf("the answer carries %q, want the value the migration copied into the project's container", ans.Value)
	}
	if ans.Env != "" {
		t.Errorf("a version 2 answer carries the variable %q, which no version 2 call named", ans.Env)
	}
	// The container the value came from is the grammar's own, which is what the
	// copy wrote and what the service's account may read: the two have to agree
	// on one name, and this is that name.
	container, err := secretname.Project(projectSecretName)
	if err != nil {
		t.Fatal(err)
	}
	if got := inst.Secrets.Latest(container); string(got) != projectSecretValue {
		t.Errorf("the container %s holds %q, want the copied value: the route served something else", container, got)
	}
	if lines := inst.AuditLines(t); len(lines) != 1 {
		t.Fatalf("the service audited %d acts, want the one serving: %v", len(lines), lines)
	} else if got := lines[0]["status"]; got != credsvc.StatusOK {
		t.Errorf("the audit line says %v, want %s", got, credsvc.StatusOK)
	}
}

// TestUnlistedRepositoryRefusedEndToEnd is the fence: a repository the owner's
// list does not admit is refused even though the container is there, marked and
// holding the value -- which is what makes the allow-list the boundary and not
// the container's existence.
//
// The code is asserted to be the contract's own, and it is the one the runner
// reads as "not copied yet" and asks again in the other vocabulary: what tells
// an unlisted repository from a name whose copy has not happened is the owner's
// list, and the two are one answer on the wire (D3328). The refusal is audited
// once and carries no value.
func TestUnlistedRepositoryRefusedEndToEnd(t *testing.T) {
	inst := credsvcfake.New(t, credsvcfake.Options{Repo: "acme/webapp", Workflow: "app"})
	// The name is in the record and its container is filled: only the listing is
	// what this repository's call lacks.
	inst.Allow(t, projectSecretName, "acme/someone-else")
	inst.SetShared(t, projectSecretName, projectSecretValue)

	status, body := askWorkflowSecret(t, inst, versionTwo(projectSecretName))
	if status == http.StatusOK {
		t.Fatalf("a repository the record does not admit was served: %s", body)
	}
	e, err := credsvc.ReadWireError(status, body)
	if err != nil {
		t.Fatalf("the refusal is not this contract's error: %v", err)
	}
	if e.Error != credsvc.ErrorWorkflowSecretNotAllowed {
		t.Errorf("the refusal carries %q, want %q: the runner falls back to version 1 on that code alone", e.Error, credsvc.ErrorWorkflowSecretNotAllowed)
	}
	if strings.Contains(string(body), projectSecretValue) {
		t.Error("the refusal carries the value it refused to serve")
	}
	if lines := inst.AuditLines(t); len(lines) != 1 {
		t.Fatalf("the service audited %d acts, want the one refusal: %v", len(lines), lines)
	} else if got := lines[0]["status"]; got != credsvc.ErrorWorkflowSecretNotAllowed {
		t.Errorf("the audit line says %v, want %s", got, credsvc.ErrorWorkflowSecretNotAllowed)
	}
}

// TestUncopiedNameFallsBackToV1EndToEnd is the state the migration is in the
// middle of: the repository's own record is written (B3.1) and the project's
// copy has not happened, so version 2 refuses with exactly the code the runner
// treats as "not copied yet" and version 1 -- the record that repository is on
// today -- serves the value.
//
// The two calls are the runner's own sequence, byte for byte
// (internal/runner's fetchWorkflowSecret): what is pinned is that the first
// answer is the code the fallback is keyed on and nothing else, and that the
// second is served from the container `fugaro update` recorded -- which is what
// makes the fallback a fallback rather than a second failure.
func TestUncopiedNameFallsBackToV1EndToEnd(t *testing.T) {
	inst := credsvcfake.New(t, credsvcfake.Options{Repo: "acme/webapp", Workflow: "app"})
	// B3.1's record, and its container: the repository's own secret, which is
	// what a run of it is served today.
	inst.RecordWorkflowSecret(t, projectSecretEnv, repositorySecretID, projectSecretValue)

	status, body := askWorkflowSecret(t, inst, versionTwo(projectSecretName))
	if status == http.StatusOK {
		t.Fatalf("version 2 served a name the project record does not hold: %s", body)
	}
	e, err := credsvc.ReadWireError(status, body)
	if err != nil {
		t.Fatalf("the refusal is not this contract's error: %v", err)
	}
	if e.Error != credsvc.ErrorWorkflowSecretNotAllowed {
		t.Fatalf("version 2 refused with %q, want %q: that code alone is what the runner asks again on", e.Error, credsvc.ErrorWorkflowSecretNotAllowed)
	}

	status, body = askWorkflowSecret(t, inst, versionOne(projectSecretEnv))
	if status != http.StatusOK {
		t.Fatalf("the fallback answered %d: %s", status, body)
	}
	var ans credsvc.WorkflowSecretsResponse
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("the answer is not this contract's: %v", err)
	}
	if ans.Env != projectSecretEnv || ans.Value != projectSecretValue {
		t.Errorf("the fallback answered env %q, value %q, want %q and the recorded container's value", ans.Env, ans.Value, projectSecretEnv)
	}
	if got := inst.Secrets.Latest(repositorySecretID); string(got) != projectSecretValue {
		t.Errorf("the container %s holds %q, want what the repository's record names", repositorySecretID, got)
	}
}
