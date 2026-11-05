package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// secretsRmFugaro stubs the binary's own name, so a refusal's sentence can be
// read as the person reads it: the tests' argv[0] is a test binary path.
func secretsRmFugaro(t *testing.T) {
	t.Helper()
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
}

// secretsRmFixture is a project secret that is both reachable project-wide and
// stored: the project_secrets entry in the local config, and the Secret
// Manager container the entry promises. Both halves exist so a test can pin
// that a refusal leaves both of them standing.
func secretsRmFixture(t *testing.T) (*gcpfake.Secrets, string, string) {
	t.Helper()
	sm := secretsFixture(t)
	projectSecretFixture(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [\"acme/app\"] }\n")
	id := gcp.ProjectSecretID("aurora", "bitbucket-token")
	sm.Seed(id, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue,
		gcp.LabelProject: "aurora",
		gcp.LabelSecret:  "bitbucket-token",
	}, []byte(tokenValue))
	return sm, id, os.Getenv("FUGARO_CONFIG")
}

// TestSecretsRmRemovesTheVersionAndTheEntry: `secrets rm NAME` withdraws a
// project secret — the Secret Manager container with every version it holds,
// and the local config's project_secrets.NAME entry that made it
// project-wide — which is the pair D1171 says a secret with no `rm` leaves
// standing forever.
func TestSecretsRmRemovesTheVersionAndTheEntry(t *testing.T) {
	sm, id, path := secretsRmFixture(t)

	out, errOut, err := execute(t, "secrets", "rm", "bitbucket-token", "--yes")
	if err != nil {
		t.Fatalf("secrets rm: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if got := sm.Deleted(); len(got) != 1 || got[0] != id {
		t.Fatalf("deleted %v, want just %s", got, id)
	}
	if got := sm.Latest(id); got != nil {
		t.Fatalf("the container still holds %q", got)
	}
	after := readFileString(t, path)
	if strings.Contains(after, "project_secrets") || strings.Contains(after, "bitbucket-token") {
		t.Fatalf("the config entry is still there:\n%s", after)
	}
	if strings.Contains(out+errOut, tokenValue) {
		t.Fatalf("the value leaked: %q %q", out, errOut)
	}
}

// TestSecretsRmPutsTheEntryBackWhenTheDeleteFails: the removal is one change
// made of two halves, and the half this command writes first is the half it can
// undo. A delete that fails — the refusal a member with secretVersionAdder and
// viewer on the project's secrets gets, and what a dropped connection does —
// must not leave the entry gone while the value is still stored: for a name D2
// shares project-wide, no entry reads as every repository, so the run would
// have widened the value at the next `fugaro update` and nobody would have
// asked for it.
func TestSecretsRmPutsTheEntryBackWhenTheDeleteFails(t *testing.T) {
	secretsRmFugaro(t)
	sm, id, path := secretsRmFixture(t)
	before := readFileString(t, path)
	sm.FailDeleteWith = http.StatusForbidden

	out, errOut, err := execute(t, "secrets", "rm", "bitbucket-token", "--yes")
	if err == nil {
		t.Fatalf("a failed delete was reported as a removal:\nstdout %s\nstderr %s", out, errOut)
	}
	if after := readFileString(t, path); after != before {
		t.Fatalf("the entry was not put back:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if got := sm.Latest(id); got == nil {
		t.Fatal("a delete that failed took the container away")
	}
	for _, want := range []string{"put back", "project_secrets.bitbucket-token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not carry %q:\n%v", want, err)
		}
	}
	if strings.Contains(out+errOut+err.Error(), tokenValue) {
		t.Fatalf("the value leaked: %q %q %v", out, errOut, err)
	}
}

// TestSecretsRmLeavesAChangedFileAloneWhenTheDeleteFails: the entry is put back
// only when the file is still the bytes this run wrote. A file that moved in
// between — another terminal, another tool, a person who saw the entry go and
// wrote the policy they wanted — is theirs, and overwriting it with the bytes
// this run read would discard that edit silently. The error says the entry was
// not put back, and why.
func TestSecretsRmLeavesAChangedFileAloneWhenTheDeleteFails(t *testing.T) {
	secretsRmFugaro(t)
	sm, id, path := secretsRmFixture(t)
	before := readFileString(t, path)
	sm.FailDeleteWith = http.StatusForbidden
	moved := strings.Replace(before, `["acme/app"]`, `["acme/app", "acme/web"]`, 1)
	if moved == before {
		t.Fatalf("the fixture is not the shape this test moves:\n%s", before)
	}
	sm.OnDelete = func(string) {
		// The fake calls this without its own lock, so writing here is the
		// other writer's edit landing between the run's write and its undo.
		if err := os.WriteFile(path, []byte(moved), 0o600); err != nil {
			t.Errorf("moving the file under the run: %v", err)
		}
	}

	_, _, err := execute(t, "secrets", "rm", "bitbucket-token", "--yes")
	if err == nil {
		t.Fatal("a failed delete was reported as a removal")
	}
	if after := readFileString(t, path); after != moved {
		t.Fatalf("the undo overwrote what someone else wrote:\nwant:\n%s\ngot:\n%s", moved, after)
	}
	if got := sm.Latest(id); got == nil {
		t.Fatal("a delete that failed took the container away")
	}
	for _, want := range []string{"not put back", path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not carry %q:\n%v", want, err)
		}
	}
}

// TestSecretsRmRefusesAnAgentSession: the whole family is refused in a coding
// agent's session, --yes included and whatever the managed gate would say
// (spec §5, D1171). The refusal is the FIRST thing this command does — before
// the config is read and before the container is looked up — because the
// deletion is not the settings write path's and its fence would arrive after
// the value was already gone. Nothing is removed, and the container and the
// entry both survive.
//
// The second state is the one where this refusal is the only guard: with an
// entry to remove, the settings write path refuses the write as well, and with
// nothing to change it returns at its "no change" line, before its own fence —
// so a session would reach the deletion if this command did not refuse first.
func TestSecretsRmRefusesAnAgentSession(t *testing.T) {
	secretsRmFugaro(t)
	t.Setenv(accountsAddAgent, "1") // a coding agent's session

	cases := []struct {
		what  string
		entry string
	}{
		{"with an entry to remove", "project_secrets:\n  bitbucket-token: { allow_data_to: [\"acme/app\"] }\n"},
		{"with no entry and the container present", ""},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			sm := secretsFixture(t)
			if tc.entry != "" {
				projectSecretFixture(t, tc.entry)
			}
			id := gcp.ProjectSecretID("aurora", "bitbucket-token")
			sm.Seed(id, map[string]string{
				gcp.LabelManaged: gcp.ManagedValue,
				gcp.LabelProject: "aurora",
				gcp.LabelSecret:  "bitbucket-token",
			}, []byte(tokenValue))
			path := os.Getenv("FUGARO_CONFIG")
			before := readFileString(t, path)

			out, errOut, err := execute(t, "secrets", "rm", "bitbucket-token", "--yes")
			if err == nil {
				t.Fatalf("a coding agent's session removed a secret:\nstdout %s\nstderr %s", out, errOut)
			}
			for _, want := range []string{
				accountsAddAgent,      // the marker that made it a session
				"fugaro secrets rm",   // the command the person runs instead
				"bitbucket-token",     // what it refused to remove
				"nothing was removed", // that the refusal is total
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not carry %q:\n%v", want, err)
				}
			}
			if got := sm.Deleted(); len(got) != 0 {
				t.Fatalf("the session deleted %v", got)
			}
			if got := sm.Latest(id); got == nil {
				t.Fatal("the session took the container away")
			}
			if after := readFileString(t, path); after != before {
				t.Fatalf("the session wrote the config:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if strings.Contains(out+errOut, tokenValue) {
				t.Fatalf("the value leaked: %q %q", out, errOut)
			}
		})
	}
}

// TestSecretsRmDryRunNamesBothHalves: the dry run's document describes the
// config half, because the settings write contract has no member for a Secret
// Manager container — so the half it cannot carry is said on stderr, and
// neither half is touched.
func TestSecretsRmDryRunNamesBothHalves(t *testing.T) {
	secretsRmFugaro(t)
	sm, id, path := secretsRmFixture(t)
	before := readFileString(t, path)

	out, errOut, err := execute(t, "secrets", "rm", "bitbucket-token", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("secrets rm --dry-run: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	var doc struct {
		Version   int    `json:"version"`
		Key       string `json:"key"`
		Layer     string `json:"layer"`
		Changed   bool   `json:"changed"`
		Widening  bool   `json:"widening"`
		Old       string `json:"old"`
		New       string `json:"new"`
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the document does not read back: %v\n%s", err, out)
	}
	if doc.Version != settingsWriteDocVersion {
		t.Errorf("version = %d, want %d: the document stays at the version the console pins", doc.Version, settingsWriteDocVersion)
	}
	if doc.Key != "project_secrets.bitbucket-token" || doc.Layer != settingsLayerLocal {
		t.Errorf("key/layer = %q/%q", doc.Key, doc.Layer)
	}
	if doc.Operation != "remove-secret" {
		t.Errorf("operation = %q, want remove-secret", doc.Operation)
	}
	if !doc.Changed || doc.Widening {
		t.Errorf("changed/widening = %t/%t, want true/false: a removal asks for nothing", doc.Changed, doc.Widening)
	}
	if doc.Old != "allow_data_to=acme/app" || doc.New != "" {
		t.Errorf("old/new = %q/%q, want the entry's own members and nothing", doc.Old, doc.New)
	}
	if !strings.Contains(errOut, id) || !strings.Contains(errOut, "would be deleted") {
		t.Errorf("stderr does not name the container the same run would delete: %q", errOut)
	}
	if got := sm.Deleted(); len(got) != 0 {
		t.Fatalf("a dry run deleted %v", got)
	}
	if after := readFileString(t, path); after != before {
		t.Fatalf("a dry run wrote the config:\n%s", after)
	}
}

// TestSecretsRmRefusesDryRunWithYes: --dry-run and --yes answer different
// questions — a dry run says what the removal would do and writes nothing,
// --yes answers a write — and the family refuses the pair rather than reading
// --yes as a confirmation the person gave for a run that removes nothing.
func TestSecretsRmRefusesDryRunWithYes(t *testing.T) {
	secretsRmFugaro(t)
	sm, id, path := secretsRmFixture(t)
	before := readFileString(t, path)

	_, _, err := execute(t, "secrets", "rm", "bitbucket-token", "--dry-run", "--yes")
	if err == nil || !strings.Contains(err.Error(), "--dry-run and --yes") {
		t.Fatalf("err = %v, want the dry-run/--yes refusal", err)
	}
	if got := sm.Deleted(); len(got) != 0 {
		t.Fatalf("deleted %v", got)
	}
	if got := sm.Latest(id); got == nil {
		t.Fatal("the container was taken away")
	}
	if after := readFileString(t, path); after != before {
		t.Fatalf("the refusal wrote the config:\n%s", after)
	}
}

// TestSecretsRmRemovesTheWorkflowBlockEntry: a name the local config holds in
// workflow_secrets is withdrawn whole — the Secret Manager container, which is
// the same derived ID a project secret has (F2), and the workflow_secrets.NAME
// entry — and both places this command names the entry say which block it lives
// in. Removing one half only is the state D1171 gives this command the job of
// preventing, and the block a name belongs to is the only thing that changes
// about it.
func TestSecretsRmRemovesTheWorkflowBlockEntry(t *testing.T) {
	secretsRmFugaro(t)
	sm := secretsFixture(t)
	projectSecretFixture(t, "workflow_secrets:\n  deploy-token: { allow_data_to: [\"acme/app\"] }\n")
	id := gcp.ProjectSecretID("aurora", "deploy-token")
	sm.Seed(id, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue,
		gcp.LabelProject: "aurora",
		gcp.LabelSecret:  "deploy-token",
	}, []byte(tokenValue))
	path := os.Getenv("FUGARO_CONFIG")
	before := readFileString(t, path)

	// The dry run's document names the key the run would remove, so a person
	// reading a plan for this removal reads the block the entry lives in.
	out, errOut, err := execute(t, "secrets", "rm", "deploy-token", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("secrets rm --dry-run: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	var doc struct {
		Key     string `json:"key"`
		Old     string `json:"old"`
		Changed bool   `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the document does not read back: %v\n%s", err, out)
	}
	if doc.Key != "workflow_secrets.deploy-token" {
		t.Errorf("the dry run's key = %q, want the block the entry lives in", doc.Key)
	}
	if !doc.Changed || doc.Old != "allow_data_to=acme/app" {
		t.Errorf("changed/old = %t/%q, want the entry the run would remove", doc.Changed, doc.Old)
	}
	for _, want := range []string{"workflow_secrets.deploy-token", "deploy-token"} {
		if !strings.Contains(errOut, want) && !strings.Contains(out, want) {
			t.Errorf("the dry run does not name %q: %q %q", want, out, errOut)
		}
	}
	if got := sm.Deleted(); len(got) != 0 {
		t.Fatalf("a dry run deleted %v", got)
	}
	if after := readFileString(t, path); after != before {
		t.Fatalf("a dry run wrote the config:\n%s", after)
	}

	out, errOut, err = execute(t, "secrets", "rm", "deploy-token", "--yes")
	if err != nil {
		t.Fatalf("secrets rm: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if got := sm.Deleted(); len(got) != 1 || got[0] != id {
		t.Fatalf("deleted %v, want just %s", got, id)
	}
	if got := sm.Latest(id); got != nil {
		t.Fatalf("the container still holds %q", got)
	}
	after := readFileString(t, path)
	if strings.Contains(after, "workflow_secrets") || strings.Contains(after, "deploy-token") {
		t.Fatalf("the config entry is still there:\n%s", after)
	}
	if strings.Contains(out+errOut, tokenValue) {
		t.Fatalf("the value leaked: %q %q", out, errOut)
	}
}

// TestSecretsRmReportsNothingToRemove: a name this project never stored and
// whose entry the config does not hold is a state to report, not an error to
// hand back — and nothing is deleted, because a delete of a derived ID that
// carries no Fugaro label would take a container this program never made.
func TestSecretsRmReportsNothingToRemove(t *testing.T) {
	secretsRmFugaro(t)
	sm := secretsFixture(t)
	projectSecretFixture(t, "project_secrets: {}\n")
	foreign := gcp.ProjectSecretID("aurora", "bitbucket-token")
	// A container with that ID but not this program's labels: the lookup must
	// not take it, and its presence must not turn the run into a success.
	sm.Seed(foreign, map[string]string{"owner": "someone-else"}, []byte(tokenValue))

	_, _, err := execute(t, "secrets", "rm", "bitbucket-token", "--yes")
	if err == nil || !strings.Contains(err.Error(), "nothing to remove") {
		t.Fatalf("err = %v, want the nothing-to-remove sentence", err)
	}
	if got := sm.Deleted(); len(got) != 0 {
		t.Fatalf("deleted %v, want nothing: the container is not this program's", got)
	}
	if got := sm.Latest(foreign); got == nil {
		t.Fatal("the container this program never made was taken away")
	}
}

// TestSecretsRmRefusesAValueInTheNamesPlace: a value typed where the NAME
// belongs must not be echoed, and the refusal names the shape the name has to
// have rather than repeating what was typed.
func TestSecretsRmRefusesAValueInTheNamesPlace(t *testing.T) {
	secretsRmFugaro(t)
	secretsFixture(t)
	projectSecretFixture(t, "project_secrets: {}\n")
	const typed = "not-a-secret-name-because-of-the-!-and-the-length"

	_, _, err := execute(t, "secrets", "rm", typed, "--yes")
	if err == nil {
		t.Fatal("secrets rm accepted a name that is not one")
	}
	if strings.Contains(err.Error(), typed) {
		t.Fatalf("the refusal echoed what was typed: %v", err)
	}
	if !strings.Contains(err.Error(), "the secret NAME must be one of") {
		t.Fatalf("err = %v, want the name-shape refusal", err)
	}
}

// TestSecretsRmRefusesANameAnotherBlockOwns: a provider's key, a named Claude
// account's token and a workflow secret this config does not list share the
// derived ID and the labels with a project secret, so only the reserved names
// may be removed here.
func TestSecretsRmRefusesANameAnotherBlockOwns(t *testing.T) {
	secretsRmFugaro(t)
	sm := secretsFixture(t)
	for _, name := range []string{"openrouter-api-key", "claude-oauth-token-work", "deploy-token"} {
		id := gcp.ProjectSecretID("aurora", name)
		sm.Seed(id, map[string]string{
			gcp.LabelManaged: gcp.ManagedValue,
			gcp.LabelProject: "aurora",
			gcp.LabelSecret:  name,
		}, []byte(tokenValue))
		_, _, err := execute(t, "secrets", "rm", name, "--yes")
		if err == nil {
			t.Fatalf("secrets rm %s deleted a container another block owns", name)
		}
		if !strings.Contains(err.Error(), "nothing was removed") {
			t.Errorf("secrets rm %s: the refusal does not say nothing was removed: %v", name, err)
		}
		if sm.Latest(id) == nil {
			t.Fatalf("the container %s was taken away", name)
		}
	}
	if got := sm.Deleted(); len(got) != 0 {
		t.Fatalf("deleted %v", got)
	}
}
