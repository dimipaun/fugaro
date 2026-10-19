package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// seenFixture is a project with both of the durable sources of "which
// repositories have run here": the runs bucket (a directory with
// runs/<slug>/ prefixes) and Firestore's spendDaily. The clock is fixed so a
// spendDaily document's date is a day this test can compute.
type seenFixture struct {
	*projectFixture
	fs  *gcpfake.Firestore
	now time.Time
}

func newSeenFixture(t *testing.T) *seenFixture {
	t.Helper()
	f := &seenFixture{projectFixture: newProjectFixture(t, ""), fs: gcpfake.NewFirestore(t),
		now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	// The Firestore fake, and the repository the config names but nothing has
	// ever run (the entry a listing must not turn into a "seen" one).
	setConfig(t, f.projectFixture, func(s string) string {
		s = strings.Replace(s, "endpoints: { no_auth: true }", "endpoints: { firestore: "+f.fs.URL+", no_auth: true }", 1)
		s = strings.Replace(s, "rtdb_url: "+f.db.URL, "rtdb_url: "+f.db.URL+", firebase_project: fugaro-fp-1", 1)
		return s + "  acme/idle: { provider: github, workflows: [web] }\n"
	})
	f.fs.Set(budget.MetaCollection, budget.MarkDocument, map[string]any{"project": "aurora", "version": int64(1)})
	old := budgetNow
	budgetNow = func() time.Time { return f.now }
	t.Cleanup(func() { budgetNow = old })
	return f
}

// slugOf is the storage slug a repository's runs are stored under.
func slugOf(t *testing.T, provider, repo string) string {
	t.Helper()
	s, err := task.Slug(provider, repo)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// putRun makes the runs bucket hold a run of slug: runs/<slug>/<run-id>/task.json,
// which is what makes runs/<slug>/ a prefix the listing sees. dir is the
// project's runs directory (the fixture's bucket_url is file://dir/runs).
func putRun(t *testing.T, dir, slug string) {
	t.Helper()
	p := filepath.Join(dir, "runs", "runs", slug, "20261010-101010-abcd", "task.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// spent writes one spendDaily document, as the rollover job does for a day
// that ran out.
func (f *seenFixture) spent(t *testing.T, date, slug, repo string) {
	t.Helper()
	r := budget.DayRecord{Repo: repo, Slug: slug, Date: date, Version: budget.DayRecordVersion, Final: true,
		SpentMicros: budget.Micros(1_000_000), Runs: 1}
	f.fs.Set(budget.SpendCollection, r.DocID(), r.ToFields())
}

// allowReposOf is the published allow-list, as the node holds it.
func allowReposOf(t *testing.T, db *gcpfake.RTDB) []string {
	t.Helper()
	v := db.Value(projectpolicy.PathShared())
	if v == nil {
		t.Fatalf("no policy node at %s", projectpolicy.PathShared())
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p, found, err := projectpolicy.Decode(b)
	if err != nil || !found {
		t.Fatalf("the node does not decode (found=%v): %v", found, err)
	}
	return p.AllowRepos
}

// nodeOf is the database's whole tree, as bytes, for the "nothing was written"
// assertions: a listing and a sync that adds nothing must leave every node —
// including one a registration record would be — exactly as it was.
func nodeOf(t *testing.T, db *gcpfake.RTDB) string {
	t.Helper()
	b, err := json.Marshal(db.Value(""))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// listLine is the one line of a listing that names name: the line whose first
// field is name, so an assertion about a repository's note cannot be satisfied
// by another repository's line.
func listLine(t *testing.T, out, name string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name+" ") || strings.TrimSpace(line) == name {
			return line
		}
	}
	t.Fatalf("no line for %q in:\n%s", name, out)
	return ""
}

// `project repos ls` prints the allow-list and, against it, the repositories
// the installation has seen run — from the two durable sources the design
// names (§4.3): the runs bucket's runs/<slug>/ prefixes and Firestore's
// spendDaily. The seen set is not the enrollment: an entry no source named is
// not listed, a repository this machine's config names but that has never run
// is not listed either, and the listing writes nothing at all — there is no
// registration record (the design's "no registration record": no principal on
// this path may write one, which is why the answer is derived instead).
//
// Mutation (verified, run and restored): list the config's repos: map as the
// seen set (allowSources' own seed) instead of reading the two sources — this
// test fails on acme/ghost and on acme/idle.
func TestProjectReposListsEnrolledAndSeen(t *testing.T) {
	f := newSeenFixture(t)
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	today := budget.DayDate(budget.Day(f.now))
	appSlug := slugOf(t, "github", "acme/app")
	putRun(t, f.dir, appSlug)                              // both sources
	putRun(t, f.dir, slugOf(t, "bitbucket", "acme/other")) // the bucket alone, and not admitted
	f.spent(t, today, appSlug, "acme/app")
	f.spent(t, today, "ghost-0123", "acme/ghost") // spendDaily alone, a slug no config names

	before := nodeOf(t, f.db)
	out, _, err := execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if nodeOf(t, f.db) != before {
		t.Error("the listing wrote to the database: the seen set is derived, never recorded")
	}
	// The enrolled half is the policy's own list, and it comes first.
	enrolled, seen, ok := strings.Cut(out, "repositories seen here")
	if !ok {
		t.Fatalf("the listing does not separate the allow-list from what it has seen:\n%s", out)
	}
	if !strings.Contains(enrolled, "github/acme/app") {
		t.Errorf("the allow-list is not printed:\n%s", out)
	}
	for _, want := range []string{"bitbucket/acme/other", "github/acme/app", "ghost-0123"} {
		if !strings.Contains(seen, want) {
			t.Errorf("%q is missing from the repositories seen here:\n%s", want, out)
		}
	}
	if strings.Contains(seen, "acme/idle") {
		t.Errorf("a repository nothing has run is listed as seen:\n%s", out)
	}
	// A repository the allow-list admits carries no mark; one it does not
	// carries the remedy, and a name that is a slug says why it cannot be
	// passed to that remedy as it stands.
	if line := listLine(t, seen, "github/acme/app"); strings.Contains(line, "not admitted") {
		t.Errorf("an admitted repository is marked as refused: %s", line)
	}
	if line := listLine(t, seen, "bitbucket/acme/other"); !strings.Contains(line, "fugaro project repos add bitbucket/acme/other") {
		t.Errorf("an unenrolled repository does not say how to enroll it: %s", line)
	}
	if line := listLine(t, seen, "ghost-0123"); !strings.Contains(line, "slug") {
		t.Errorf("a name that is a slug does not say so: %s", line)
	}
	// The sources are named, so an operator can tell a repository that ran
	// today from one only the bucket still remembers.
	if line := listLine(t, seen, "github/acme/app"); !strings.Contains(line, "runs bucket") || !strings.Contains(line, "spendDaily") {
		t.Errorf("an entry from both sources does not name them: %s", line)
	}
	if line := listLine(t, seen, "ghost-0123"); !strings.Contains(line, "spendDaily") {
		t.Errorf("an entry only spendDaily holds does not say so: %s", line)
	}
}

// syncRig is a GitHub App that answers the two listings sync makes, and the
// secret read behind them.
type syncRig struct {
	srv   *httptest.Server
	names []string
	// reads are the secret IDs the App's key was read from, in order.
	reads []string
}

func newSyncRig(t *testing.T, names ...string) *syncRig {
	t.Helper()
	g := &syncRig{names: names}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/app/installations":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7}})
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/7/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_x", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
		case r.Method == http.MethodGet && r.URL.Path == "/installation/repositories":
			out := []map[string]any{}
			for _, n := range g.names {
				out = append(out, map[string]any{"full_name": n})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(out), "repositories": out})
		default:
			t.Errorf("unexpected GitHub request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(g.srv.Close)
	oldURL, oldClient, oldRead := githubAPIURL, githubAPIClient, readAppKey
	githubAPIURL, githubAPIClient = g.srv.URL, g.srv.Client()
	readAppKey = func(_ context.Context, _ *localcfg.Config, secretID string) ([]byte, error) {
		g.reads = append(g.reads, secretID)
		return append([]byte(nil), appKey(t)...), nil
	}
	t.Cleanup(func() { githubAPIURL, githubAPIClient, readAppKey = oldURL, oldClient, oldRead })
	return g
}

// `project sync` offers the App's installed repositories that the allow-list
// does not admit, in one confirmed batch, and writes them through the same
// ETag-guarded path `repos add` uses.
//
// What it must never do is remove: an entry the App is not installed on at all
// (bitbucket/acme/other) is still on the list after the sync, which is the
// property that makes sync safe to run in a project that enrolls repositories
// from more than one provider.
//
// Mutation (verified, run and restored): write the installed set as the new
// allow-list instead of appending the missing ones — this test fails on the
// bitbucket entry, which a sync would have un-enrolled.
func TestProjectSyncAddsOnlyTheMissingOnesAndConfirmsOnce(t *testing.T) {
	typed(t)
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"bitbucket/acme/other", "github/acme/app"},
	})
	g := newSyncRig(t, "acme/app", "Acme/New", "other/thing")
	out, _, err := executeStdin(t, "aurora\n", "project", "sync", "--github-app-id", "12345", "--github-app-key")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// One batch, one confirmation, and it comes after the list it confirms.
	if n := strings.Count(out, "Type aurora to confirm"); n != 1 {
		t.Errorf("the batch is confirmed %d times, want once:\n%s", n, out)
	}
	offer, confirm, ok := strings.Cut(out, "Type aurora to confirm")
	if !ok {
		t.Fatalf("the sync wrote without confirming:\n%s", out)
	}
	for _, want := range []string{"github/acme/new", "github/other/thing"} {
		if !strings.Contains(offer, want) {
			t.Errorf("the offer does not name %q:\n%s", want, out)
		}
	}
	if strings.Contains(offer, "github/acme/app") {
		t.Errorf("the offer names a repository the allow-list already admits:\n%s", out)
	}
	_ = confirm
	// Canonical and sorted, with the entries that were there left alone.
	want := []string{"bitbucket/acme/other", "github/acme/app", "github/acme/new", "github/other/thing"}
	if got := allowReposOf(t, f.db); !slices.Equal(got, want) {
		t.Errorf("allow_repos = %v, want %v", got, want)
	}
	// The App's key is the project's own copy, read once.
	if wantID := gcp.ProjectSecretID("aurora", "github-app-key"); len(g.reads) != 1 || g.reads[0] != wantID {
		t.Errorf("the App's key was read from %v, want [%s]", g.reads, wantID)
	}
	// A second sync has nothing to add and writes nothing at all.
	before := nodeOf(t, f.db)
	out2, _, err := executeStdin(t, "aurora\n", "project", "sync", "--github-app-id", "12345", "--github-app-key")
	if err != nil {
		t.Fatalf("%v\n%s", err, out2)
	}
	if !strings.Contains(out2, "nothing to add") {
		t.Errorf("a second sync does not say it added nothing:\n%s", out2)
	}
	if nodeOf(t, f.db) != before {
		t.Error("a sync with nothing to add wrote to the policy node")
	}
}

// A repository the allow-list already admits is not offered, even though the
// list does not name it: `github/acme/*` admits `github/acme/app`, whose runs
// the gate lets through, so adding the concrete entry would write an entry
// that changes nothing and read as an enrollment the operator still had to
// make (the design's ordering: never a state in which a job serves a
// repository the list does not allow).
//
// Mutation (verified, run and restored): compare against the allow-list's
// entries (slices.Contains) instead of AdmitRepo — this test fails, the
// pattern's repository offered as missing.
func TestProjectSyncOffersNothingTheListAlreadyAdmits(t *testing.T) {
	typed(t)
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/*"},
	})
	newSyncRig(t, "acme/app")
	before := nodeOf(t, f.db)
	out, _, err := executeStdin(t, "aurora\n", "project", "sync", "--github-app-id", "12345", "--github-app-key")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing to add") {
		t.Errorf("sync offered a repository the pattern already admits:\n%s", out)
	}
	if nodeOf(t, f.db) != before {
		t.Error("sync wrote an entry the allow-list already admitted")
	}
}

// The batch is a write to the project's policy node, and a confirmation that
// was not given writes nothing: the operator types the project's name, and
// anything else leaves the node byte-identical.
//
// Mutation (verified, run and restored): ignore confirmTyped's error — this
// test fails, the node holding the batch after a refused confirmation.
func TestProjectSyncWritesNothingWhenTheConfirmationIsRefused(t *testing.T) {
	typed(t)
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	newSyncRig(t, "acme/new")
	before := nodeOf(t, f.db)
	_, _, err := executeStdin(t, "nope\n", "project", "sync", "--github-app-id", "12345", "--github-app-key")
	if err == nil {
		t.Fatal("sync wrote the batch without the confirmation")
	}
	if nodeOf(t, f.db) != before {
		t.Error("a refused confirmation still changed the policy node")
	}
}

// Reading the App's private key is the one secret this command needs, so it is
// behind an explicit flag and refused in a coding agent's session — the same
// two rules `--check-github-app` has (a flag read that a plain command name
// cannot trigger, and a refusal before anything is read). Without the flag
// nothing is read and nothing is written.
//
// Mutation (verified, run and restored): read the key without the flag (drop
// the flag check) — this test fails, the key being read by an ordinary
// `project sync`.
func TestProjectSyncRefusesTheKeyReadWithoutTheFlag(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	g := newSyncRig(t, "acme/new")
	before := nodeOf(t, f.db)
	_, _, err := execute(t, "project", "sync", "--github-app-id", "12345")
	if err == nil || !strings.Contains(err.Error(), "--github-app-key") {
		t.Fatalf("err = %v, want a refusal naming the flag that allows the read", err)
	}
	if len(g.reads) != 0 {
		t.Errorf("the App's key was read without the flag: %v", g.reads)
	}
	if nodeOf(t, f.db) != before {
		t.Error("a refused sync changed the policy node")
	}
}

// The refusal in a coding agent's session: the key must never reach an agent's
// process, so the flag is refused before the read, and the refusal names the
// flag rather than leaving the operator to guess which one to add.
//
// Mutation (verified, run and restored): drop the agentEnv refusal — this test
// fails, the key read inside a session that sets CLAUDECODE.
func TestProjectSyncRefusesTheKeyReadInACodingAgentSession(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	g := newSyncRig(t, "acme/new")
	t.Setenv("CLAUDECODE", "1")
	before := nodeOf(t, f.db)
	_, _, err := execute(t, "project", "sync", "--github-app-id", "12345", "--github-app-key")
	if err == nil || !strings.Contains(err.Error(), "--github-app-key") || !strings.Contains(err.Error(), "coding agent") {
		t.Fatalf("err = %v, want the refusal of the flag in a coding agent's session", err)
	}
	if len(g.reads) != 0 {
		t.Errorf("the App's key was read in a coding agent's session: %v", g.reads)
	}
	if nodeOf(t, f.db) != before {
		t.Error("a refused sync changed the policy node")
	}
}

// Nothing to ask GitHub with, nothing to do: an App ID is a number the
// operator has (the config may already hold it), and a sync with neither says
// which flag carries it rather than asking GitHub as an anonymous App.
func TestProjectSyncNeedsAnAppID(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	g := newSyncRig(t, "acme/new")
	_, _, err := execute(t, "project", "sync", "--github-app-key")
	if err == nil || !strings.Contains(err.Error(), "--github-app-id") {
		t.Fatalf("err = %v, want a refusal naming --github-app-id", err)
	}
	if len(g.reads) != 0 {
		t.Errorf("the App's key was read with no App ID to list: %v", g.reads)
	}
}

// Removing an entry is what un-enrolls a repository, and the next run of that
// repository is the proof: the launcher's own read of the node refuses it
// before any execution exists (plan tasks 1.5 and 3.3), with the message the
// runner prints. The same binary performs both halves here — the removal
// through the CAS path, then the launch — so a removal that only edited the
// node's text (or a gate that read another node) shows up as a launch that
// went ahead.
//
// Mutation (verified, run and restored): make runProjectReposEdit write the
// list without removing the entry (keep `allow` as it was) — this test fails,
// the launch creating an execution for a repository that is still admitted.
func TestProjectRemoveRepoStopsAdmittingTheRepo(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, `{"version":3,"allow_repos":["github/acme/app"]}`)
	if allow := allowReposOf(t, f.db); !projectpolicy.AdmitRepo(allow, "github/acme/app") {
		t.Fatalf("allow_repos = %v: the fixture does not start with the repository admitted", allow)
	}
	out, _, err := execute(t, "project", "repos", "rm", "github/acme/app", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if allow := allowReposOf(t, f.db); len(allow) != 0 {
		t.Fatalf("allow_repos = %v after the removal, want none", allow)
	}
	_, _, err = runBudget(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want the launch refused", err, ExitCode(err))
	}
	for _, want := range []string{"github/acme/app", "allow_repos", "before any execution exists"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a removed repository", n)
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("the refused launch stored a task")
	}
}

// The seen half reads the project's own bucket and its own Firestore database,
// and both reads are checked to be this project's before anything is printed:
// the bucket's marker (checkCloudName, the check every cloud command makes) and
// the installation mark in Firestore. A config pointed at another project's
// bucket or Firebase project is refused rather than answered with that
// project's repositories, which is the one way this command could print
// another installation's data.
//
// Mutation (verified, run and restored): drop checkCloudName from
// runsBucketSlugs — the first case fails, listing another project's bucket.
func TestProjectReposRefusesAnotherProjectsSources(t *testing.T) {
	t.Run("bucket", func(t *testing.T) {
		f := newSeenFixture(t)
		f.db.Set(projectpolicy.PathShared(), map[string]any{
			"version":     projectpolicy.Version,
			"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
			"allow_repos": []string{"github/acme/app"},
		})
		writeRunsMarker(t, filepath.Join(f.dir, "runs"), "someone-else", "proj-9999")
		out, _, err := execute(t, "project", "repos", "ls")
		if err == nil {
			t.Fatalf("a bucket of another project was listed:\n%s", out)
		}
		if !strings.Contains(err.Error(), "someone-else") {
			t.Errorf("the refusal does not name the project the bucket belongs to: %v", err)
		}
	})
	t.Run("firestore", func(t *testing.T) {
		f := newSeenFixture(t)
		f.db.Set(projectpolicy.PathShared(), map[string]any{
			"version":     projectpolicy.Version,
			"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
			"allow_repos": []string{"github/acme/app"},
		})
		f.fs.Set(budget.MetaCollection, budget.MarkDocument, map[string]any{"project": "someone-else", "version": int64(1)})
		out, _, err := execute(t, "project", "repos", "ls")
		if err == nil {
			t.Fatalf("a Firestore database of another project was read:\n%s", out)
		}
		if !strings.Contains(err.Error(), "someone-else") {
			t.Errorf("the refusal does not name the project the database belongs to: %v", err)
		}
	})
}

// A project with no Firebase budget backend still lists: the allow-list is the
// policy's, and the note says which source is missing rather than leaving an
// empty seen list to read as "nothing has run here".
func TestProjectReposListsWithoutASpendHistory(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	putRun(t, f.dir, slugOf(t, "github", "acme/app"))
	out, _, err := execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "no Firebase budget backend") {
		t.Errorf("the listing does not say that the spend history was not read:\n%s", out)
	}
	if !strings.Contains(out, "runs bucket") || !strings.Contains(out, "github/acme/app") {
		t.Errorf("the listing dropped the bucket's answer with the missing source:\n%s", out)
	}
}

// A seen entry that is a slug and *is* admitted still says what it is: only
// `*` can admit a name that is not a provider/owner/name at all, and an
// operator reading the list has to know that the entry they see is the
// storage key of a repository whose name nothing here records — otherwise the
// allow-list's every-repository entry reads as if it had been checked against
// named repositories.
//
// Mutation (verified, run and restored): drop the slug half of seenNote (the
// `case r.Provider == ""` clause) — this test fails, an admitted slug printed
// as if it were a canonical name.
func TestProjectReposSeenSlugSaysWhatItIs(t *testing.T) {
	f := newSeenFixture(t)
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"*"},
	})
	putRun(t, f.dir, "ghost-0123")
	out, _, err := execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	_, seen, _ := strings.Cut(out, "repositories seen here")
	line := listLine(t, seen, "ghost-0123")
	if !strings.Contains(line, "slug") {
		t.Errorf("an admitted slug is printed as if it were a name: %s", line)
	}
	if strings.Contains(line, "not admitted") {
		t.Errorf("an admitted repository is marked as refused: %s", line)
	}
}
