package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// statusFixture is the enrolment fixture with a Secret Manager fake wired in
// and a published allow-list: the state `status` is a doctor for.
//
// The allow-list matters to more than the first row: the view builds a row per
// concrete name the list admits, and a fixture whose node was never published
// would draw "admitted by nothing" about a repository the operator has, which
// is not the state any of these tests is about.
func statusFixture(t *testing.T) (*projectFixture, *gcpfake.Secrets) {
	t.Helper()
	f := newProjectFixture(t, "")
	sm := gcpfake.NewSecrets(t)
	setConfig(t, f, func(s string) string {
		s = strings.Replace(s, "no_auth: true", "secret_manager: "+sm.URL+"/, no_auth: true", 1)
		return strings.Replace(s,
			"acme/app: { provider: github, workflows: [web] }",
			`acme/app: { provider: github, workflows: [web], github_app_id: "12345" }`, 1)
	})
	viewCatalogNodeBase(t, f.db, []string{"github/acme/app"}, "")
	return f, sm
}

// seedGitHubAppKey stores the repository's github-app-key container and hands
// its ID back, so a test can take the enabled version away again.
func seedGitHubAppKey(t *testing.T, sm *gcpfake.Secrets) string {
	t.Helper()
	slug := slugOf(t, "github", "acme/app")
	id := gcp.SecretID(slug, "github-app-key")
	sm.Seed(id, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue,
		gcp.LabelRepo:    slug,
		gcp.LabelSecret:  "github-app-key",
	}, []byte("not-a-secret-shaped-literal"))
	return id
}

// statusRowIn finds one row of one repository in the document.
func statusRowIn(t *testing.T, doc reposStatusDoc, canonical, id string) reposStatusRow {
	t.Helper()
	for _, r := range doc.Repos {
		if r.Canonical != canonical {
			continue
		}
		for _, row := range r.Rows {
			if row.ID == id {
				return row
			}
		}
		t.Fatalf("%s has no %s row: %+v", canonical, id, r.Rows)
	}
	t.Fatalf("no row for %s in %+v", canonical, doc.Repos)
	return reposStatusRow{}
}

func statusDocOf(t *testing.T, out string) reposStatusDoc {
	t.Helper()
	var doc reposStatusDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	return doc
}

// TestReposStatusNamesTheEmptyCredentialAndItsFix is the red this task exists
// for.
//
// A repository whose github-app-key container holds no ENABLED version is the
// failure that made the whole command necessary: Terraform creates the
// container and never fills it, the readiness gate plans no job for a workflow
// mounting it, and the enrolment otherwise reads as complete. `status` must say
// so and must name the one command that puts it right — a row that says what is
// wrong and not what to type is the state the operator was already in (F11).
//
// Mutation (verified, run and restored): drop the `!creds.Entries[i].Enabled`
// arm of reposStatusRowCredentialOf and let the empty container fall through to
// the passing row — this test fails on the row's OK and on the exit status.
func TestReposStatusNamesTheEmptyCredentialAndItsFix(t *testing.T) {
	_, sm := statusFixture(t)
	id := seedGitHubAppKey(t, sm)
	// The empty container: the secret is there and every version is DISABLED,
	// which is exactly what `fugaro secrets set` writes over and what the
	// readiness gate reads as "no version".
	sm.DisableVersions(id)

	out, _, err := execute(t, "project", "repos", "status", "github/acme/app", "--json")
	if err == nil {
		t.Fatal("want a nonzero exit: the repository's credential container holds no enabled version")
	}
	doc := statusDocOf(t, out)
	row := statusRowIn(t, doc, "github/acme/app", reposStatusRowCredential)
	if row.OK {
		t.Errorf("the credential row passes on an empty container: %+v", row)
	}
	if row.Severity != "" {
		t.Errorf("the credential row is severity %q, want a failing row: a launch of this repository cannot start", row.Severity)
	}
	want := selfCommand() + " secrets set github-app-key --repo acme/app"
	if !strings.Contains(row.Fix, want) {
		t.Errorf("the credential row's fix is %q, want it to name %q", row.Fix, want)
	}
	if !strings.Contains(row.Problem, "no enabled version") {
		t.Errorf("the credential row's problem is %q, want it to say the container is empty", row.Problem)
	}
}

// TestReposStatusEmptyContainerExitIsNonzero: the exit status of the empty
// container is checksFail's own rule, not a flag — a row that blocks a launch
// fails the command (D2412), and the warning rows around it never do.
func TestReposStatusEmptyContainerExitIsNonzero(t *testing.T) {
	_, sm := statusFixture(t)
	sm.DisableVersions(seedGitHubAppKey(t, sm))
	_, _, err := execute(t, "project", "repos", "status", "github/acme/app")
	if err == nil {
		t.Fatal("want a nonzero exit for a repository whose container holds no enabled version")
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit code = %d, want %d", got, ExitUserError)
	}
}

// TestReposStatusUnknownRepoIsItsOwnLine: a repository the caller named that
// nothing enrolls is a row of its own, with the command that admits it, and a
// nonzero exit.
//
// It must not be an empty screen — an operator who mistypes a name has to be
// told the repository is not there — and it must not be the nine rows drawn
// against a repository this project does not have, which would report a
// repository the person never enrolled as misconfigured (F25, D2411).
//
// Mutation (verified, run and restored): let an unenrolled named repository
// fall through to the nine rows — this test fails: the enrolment row is gone
// and the answer claims a repository that is not enrolled has a missing
// credential.
func TestReposStatusUnknownRepoIsItsOwnLine(t *testing.T) {
	statusFixture(t)
	out, _, err := execute(t, "project", "repos", "status", "github/acme/nope", "--json")
	if err == nil {
		t.Fatal("want a nonzero exit: the named repository is not enrolled")
	}
	doc := statusDocOf(t, out)
	if len(doc.Repos) != 1 || doc.Repos[0].Canonical != "github/acme/nope" {
		t.Fatalf("the answer is %+v, want one line for the repository the caller named", doc.Repos)
	}
	r := doc.Repos[0]
	if r.Enrolled {
		t.Errorf("%s is reported as enrolled: %+v", r.Canonical, r)
	}
	if len(r.Rows) != 1 || r.Rows[0].ID != reposStatusRowEnrolment {
		t.Fatalf("the rows are %+v, want the enrolment row alone", r.Rows)
	}
	if !strings.Contains(r.Rows[0].Fix, selfCommand()+" project repos add github/acme/nope") {
		t.Errorf("the enrolment row's fix is %q, want the command that enrolls it", r.Rows[0].Fix)
	}
}

// TestReposStatusIsReadOnly: the command is a read on every path, the shape
// TestDoctorReadOnly holds doctor to.
//
// Nothing here may write: not a secret's value (D2415 — the row names a
// container and never what it holds), not a container, not a version, not the
// published policy and not a run record. The fakes refuse a value read by
// themselves, so the assertion below is the second half of it: no non-read call
// reached any fake at all.
func TestReposStatusIsReadOnly(t *testing.T) {
	f, sm := statusFixture(t)
	seedGitHubAppKey(t, sm)
	before := f.db.Value(projectpolicy.PathShared())
	rules := f.db.RulePuts()

	_, _, _ = execute(t, "project", "repos", "status", "github/acme/app")
	_, _, _ = execute(t, "project", "repos", "status")

	if got := sm.Accessed(); got != nil {
		t.Errorf("status read a secret's value: %v", got)
	}
	if got := sm.Deleted(); got != nil {
		t.Errorf("status deleted a container: %v", got)
	}
	if got := sm.Destroyed(); got != nil {
		t.Errorf("status destroyed a version: %v", got)
	}
	if got := sm.Disabled(); got != nil {
		t.Errorf("status disabled a version: %v", got)
	}
	if got := f.db.RulePuts(); got != rules {
		t.Errorf("status wrote the database rules: %d puts, want %d", got, rules)
	}
	after := f.db.Value(projectpolicy.PathShared())
	if !sameJSON(t, before, after) {
		t.Errorf("status changed the published policy node:\n before %v\n after  %v", before, after)
	}
}

// sameJSON compares two decoded database nodes by their JSON spelling.
func sameJSON(t *testing.T, a, b any) bool {
	t.Helper()
	ab, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(ab) == string(bb)
}

// TestReposStatusJSONCarriesNoValue: `--json` is read outside this binary, so
// the no-value rule (D2415) has to hold member by member and not only on the
// screen.
//
// Mutation (verified, run and restored): make reposViewCredentialsOf carry the
// container's value in the credential entry's ID — the gcpfake refuses the
// read by itself before this assertion, and the JSON this test walks would hold
// it.
func TestReposStatusJSONCarriesNoValue(t *testing.T) {
	const stored = "not-a-secret-shaped-literal"
	_, sm := statusFixture(t)
	id := seedGitHubAppKey(t, sm)
	sm.AddVersion(id, []byte(stored))

	out, _, _ := execute(t, "project", "repos", "status", "github/acme/app", "--json")
	if strings.Contains(out, stored) {
		t.Fatalf("the document carries the stored value: %s", out)
	}
	doc := statusDocOf(t, out)
	if doc.Version != reposStatusDocVersion {
		t.Errorf("version = %d, want %d", doc.Version, reposStatusDocVersion)
	}
	if doc.Project != "aurora" {
		t.Errorf("project = %q, want the installation the read was aimed at", doc.Project)
	}
	if len(doc.Repos) == 0 || len(doc.Repos[0].Rows) != 9 {
		t.Fatalf("the document is %+v, want the nine rows of one repository", doc.Repos)
	}
	c := statusRowIn(t, doc, "github/acme/app", reposStatusRowCredential)
	if !c.OK || !strings.Contains(c.Problem, "github-app-key") {
		t.Errorf("the credential row is %+v, want it to name the container and not its contents", c)
	}
}
