package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/safetext"
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

// TestProjectReposLsHelpPointsAtTheDurableList: the command's own help must
// name the list it prints, and not the other one. "Running now" is
// `project show`'s registry list — the runs in flight — while what `repos ls`
// prints is the durable seen, the runs bucket's runs/<slug>/ prefixes and
// spendDaily, which outlive every run (docs/project-layer.md §7 words the
// contrast: "the durable answer is one command away rather than in that
// output"). A Short naming the other surface sends an operator asking about a
// repository that has run but is not running at this moment to the wrong
// command, and the CLI reference is generated from that string
// (website/content/cli-reference.md).
//
// Mutation (verified, run and restored): put "and the repositories running
// now" back in the Short — this test fails, naming it.
func TestProjectReposLsHelpPointsAtTheDurableList(t *testing.T) {
	short := newProjectReposLsCmd().Short
	if strings.Contains(short, "running now") {
		t.Errorf("`repos ls`'s help names the repositories running now, which is `project show`'s list: %q", short)
	}
	if !strings.Contains(short, "have run here") {
		t.Errorf("`repos ls`'s help does not name the list it prints: %q", short)
	}
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
	publishPolicy(t, f, `{"version":4,"allow_repos":["github/acme/app"]}`)
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

// hostileSeenName is a repository name a run can write into the two durable
// sources `repos ls` reads, carrying every class of character that lets a
// value rewrite the line it is printed in: an ANSI escape sequence, a newline,
// a carriage return, a right-to-left override and a zero-width joiner.
var hostileSeenName = "evil\x1b[31mred\nline\rcarriage‮override​zero"

// longSeenName is the other half of the class: length. A bucket's runs/<slug>/
// prefix is an object key, which no 255-byte name limit applies to, and
// spendDaily's slug is a document field; four times the bounded printable form
// is what a name that long does to the line it is printed in.
var longSeenName = strings.Repeat("z", 4*safetext.FieldMax)

// denseSeenName is the third half, and the one the bound is really about: a
// name every character of which is escaped. pluginwire's escape writes one
// character as six (\uXXXX), so a name of safetext.FieldMax zero-width joiners
// is three thousand runes wide in the escaped form — six times the cap, past
// even seenLineMaxRunes — if the bound is put on what the escaper reads rather
// than on what it prints. Both durable sources accept the name: a bucket
// object key has no length limit, and budget.FromFields checks only that a
// spendDaily slug is valid UTF-8 and not empty.
//
// Written as runes, so the source stays plain text: U+200D is what a run
// writes into a name, not what this file holds.
var denseSeenName = strings.Repeat(string(rune(0x200D)), safetext.FieldMax)

// tabbedSeenName is the whitespace half: a name carrying a literal tab. It is
// a value safetext.Field would change (every unicode space becomes a space, a
// tab included) and that pluginwire.PrintableN deliberately keeps, so it is
// the one shape where the escaped form and Field's disagree — printed through
// PrintableN alone it shifts the rest of the line out of the column, which no
// other run-written value in this program does.
var tabbedSeenName = "acme\tcookie"

// seenLineMaxRunes bounds a line of a listing that names a run-written
// repository: the bounded printable form (safetext.FieldMax) plus this
// program's own longest note wrapped around one (the sources, the refusal and
// the remedy). A test that said only "the name is not printed raw" would pass
// on a value that was cut but left one line wide; a line as long as the name
// whoever wrote it made it is what pushes this program's own words off the
// screen.
const seenLineMaxRunes = safetext.FieldMax + 400

// assertBoundedAndInert fails when a command's output carries any of what a
// run-written repository name could put there — an escape sequence, a carriage
// return, another C0 or C1 control, a bidi override, a zero-width character,
// or one of the invisible characters safetext.Field drops that are not format
// characters (a variation selector, a Hangul filler: a name carrying one is
// printed escaped, never literally) — or a line longer than the bounded form
// allows. It scans the whole output,
// not the one line the value is expected on: the point is that no line of it
// reads as something other than what this program printed.
func assertBoundedAndInert(t *testing.T, what, out string) {
	t.Helper()
	for _, bad := range []struct{ name, s string }{
		{"an ANSI escape sequence", "\x1b"},
		{"a carriage return", "\r"},
		{"a right-to-left override", "‮"},
		{"a zero-width joiner", "​"},
		{"a variation selector", string(rune(0xFE00))},
		{"an invisible Hangul filler", string(rune(0x3164))},
	} {
		if strings.Contains(out, bad.s) {
			t.Errorf("%s printed %s a run-written name carried — the name rewrote the output:\n%q", what, bad.name, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if n := utf8.RuneCountInString(line); n > seenLineMaxRunes {
			t.Errorf("%s printed a %d-rune line, past the %d the bounded form allows: the name was printed unbounded:\n%s", what, n, seenLineMaxRunes, line)
		}
		for _, r := range line {
			// A tab is refused here as much as any other C0 control: these
			// listings print every run-written value through safetext.Field's
			// whitespace rule (printableName), which maps a tab to a space, and
			// a live one would shift the rest of the line out of the column the
			// operator is reading. A tab that survived would be the one
			// run-written value in this program that is not normalised.
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				t.Errorf("%s printed the control character %U: a run-written name rewrote the output:\n%q", what, r, out)
				break
			}
		}
	}
}

// TestProjectReposLsPrintsSeenNamesBoundedAndInert: the seen half of the
// listing is written by runs, not by this program. A runs/<slug>/ prefix is a
// directory name in the runs bucket (phase 3's broadened grant lets a run of a
// profile write those prefixes), and spendDaily's slug is a field of a
// document the rollup writes; nothing on either path checks the shape of the
// name (runstore.ListSlugs does not apply its own slugRE, and budget.FromFields
// checks only that the slug is valid UTF-8 and not empty), and this command
// prints the name in two places — the entry's own cell and, for one the
// allow-list does not admit, inside the "repos add" remedy the note words.
// Printed raw, a name carrying an escape sequence rewrites the operator's
// screen, a newline or carriage return adds a line that reads as one of this
// program's own, and a long name pushes the note off the column. The bounded
// printable form is safetext.Field, the one the rest of the CLI prints
// run-written text through (runview's rows, watch).
//
// Mutations (both verified, run and restored): print r.Name raw at the seen
// entry's own cell — this test fails on the escape sequence, on the carriage
// return, on the override, on the zero-width joiner, on the extra line the
// newline adds and on the line length the over-long name makes; and put the
// bound on what the escaper reads instead of what it prints (printableName
// returning pluginwire.PrintableN(s, safetext.FieldMax) whole-string) — it
// fails on the dense name: six characters per escaped one is a line of three
// thousand runes, past the listing's own bound.
//
// An earlier version of this comment said that the name-carrying branch of
// notAdmittedSentence was unreachable because projectpolicy.ValidPattern gated
// it, so no name a run writes could reach the remedy. That was false and the
// review said so: ValidPattern accepts "*", and a run writes single-segment
// names (the runs-bucket prefix "runs/*/", a spendDaily slug of "*"), so the
// remedy told the operator to run `fugaro project repos add *` — enrol every
// repository. The branch is now gated on enrollableArg, which certifies a
// repository identifier and nothing else, and the fallback carries no name at
// all; TestProjectReposLsNeverAdvertisesAPatternAsTheRemedy is that hole's
// test, with the mutation that reopens it.
func TestProjectReposLsPrintsSeenNamesBoundedAndInert(t *testing.T) {
	f := newSeenFixture(t)
	// An allow-list that admits nothing here: the hostile name is then both
	// printed and carried into the remedy sentence.
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	// Both durable sources, and all four halves of the class: the bucket holds
	// the one carrying the controls and the one carrying the tab (a path
	// component of the file-backed bucket this test reads is at most 255 bytes,
	// where a real bucket's object key is not, so the long and the dense name
	// go in through spendDaily's slug field).
	putRun(t, f.dir, hostileSeenName)
	putRun(t, f.dir, tabbedSeenName)
	f.spent(t, budget.DayDate(budget.Day(f.now)), longSeenName, longSeenName)
	f.spent(t, budget.DayDate(budget.Day(f.now)), denseSeenName, denseSeenName)

	out, _, err := execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	_, seen, ok := strings.Cut(out, "repositories seen here")
	if !ok {
		t.Fatalf("the listing does not print the seen half:\n%s", out)
	}
	// One line per entry (the header is the first line): a name carrying a
	// newline must not add a line that reads as another entry of this
	// program's own.
	entries := 0
	for _, line := range strings.Split(seen, "\n")[1:] {
		if strings.TrimSpace(line) != "" {
			entries++
		}
	}
	if entries != 4 {
		t.Errorf("the seen half holds %d lines after the header, want one per repository (4): a run-written name added one:\n%s", entries, seen)
	}
	bounded := printableName(hostileSeenName)
	if !strings.Contains(seen, bounded) {
		t.Errorf("the seen name is not printed in its bounded form;\nwant a line holding:\n%s\ngot:\n%s", bounded, seen)
	}
	if !strings.Contains(seen, printableName(longSeenName)) {
		t.Errorf("the over-long seen name is not printed in its bounded form: four times the cap came back unbounded")
	}
	// The dense name: every character of it is escaped, so this is the bound
	// the listings' line bound rests on. Printed through PrintableN's own
	// limit it would be six times the cap and this assertion — and
	// assertBoundedAndInert's line bound — would fail.
	if dense := printableName(denseSeenName); !strings.Contains(seen, dense) {
		t.Errorf("the name of format characters is not printed in its bounded form (want %d runes):\n%s", len([]rune(dense)), seen)
	}
	if strings.Contains(seen, denseSeenName) {
		t.Errorf("the name of format characters is printed raw, so it is invisible and unbounded at once:\n%s", seen)
	}
	// The tab is normalised to a space, the way every other run-written value
	// in this program is: a live tab shifts everything after it out of the
	// column the operator is reading. Nothing else this command prints carries
	// one, so the whole half is the unit.
	if strings.ContainsRune(seen, '\t') {
		t.Errorf("a name carrying a tab is printed with it live, so the line it is on is shifted:\n%s", seen)
	}
	if line := listLine(t, seen, "acme cookie"); strings.ContainsRune(line, '\t') {
		t.Errorf("a name carrying a tab is printed with it live: %q", line)
	}
	// The note is printed on the entry's own line, and neither copy of the
	// sentence carries the name: the hostile name is not a repository
	// identifier, so it gets the fallback (notAdmittedSentence), whose only
	// command is the fixed "provider/owner/name" template. What this asserts is
	// that the marked line is there at all.
	if !strings.Contains(seen, "not admitted") || !strings.Contains(seen, "repos add") {
		t.Errorf("the seen entry no longer says the allow-list refuses it:\n%s", seen)
	}
	assertBoundedAndInert(t, "`project repos ls`", out)
}

// TestProjectReposLsPrintsALookalikeDistinctly: bounding a name must not make
// two different names one line. safetext.Field drops what Strip drops, so a
// run writing a name that carries a character Field drops and a run of the
// repository spelled without it are, printed through Field alone, two entries
// the operator reads as one — and they are not the same entry: the allow-list
// refuses one of them and admits the other. A name Field would change is
// printed through printableName, which escapes the character instead of
// dropping it, so the entries read as two.
//
// Both halves of Field's drop set are here, because they are two different
// sets of characters and only one of them was covered before the review. The
// format characters (U+2066 and U+2069 are Cf) are the half pluginwire's
// PrintableN escapes on its own; the variation selector (U+FE00) and the
// Hangul filler (U+3164) are the half it does NOT — they are invisible and
// not Cf — and while printableName escaped by pluginwire's rule alone they
// were emitted literally, so "x<U+FE00>y" printed as "xy" and nothing in the
// suite said so.
//
// Mutations (both verified, run and restored): return safetext.Field(s) from
// printableName in every case — this test fails, every entry printed as "xy";
// and escape by pluginwire's rule alone (escapedNameRune without its Strip
// half) — it fails, the two non-Cf entries printed as "xy".
func TestProjectReposLsPrintsALookalikeDistinctly(t *testing.T) {
	f := newSeenFixture(t)
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	// One name per half of the drop set and one plain name of the same letters.
	// None of them is a repository this machine's config names, so all three are
	// listed as seen and all three are refused — but they are three entries, and
	// the listing has to read as three.
	// Written as runes so the source stays plain text: the characters are what a
	// run writes into a name, not what this file holds.
	cfLookalike := "x" + string(rune(0x2066)) + "y" + string(rune(0x2069))
	nonCfLookalike := "x" + string(rune(0xFE00)) + "y" + string(rune(0x3164))
	const plain = "xy"
	putRun(t, f.dir, cfLookalike)
	putRun(t, f.dir, nonCfLookalike)
	putRun(t, f.dir, plain)

	out, _, err := execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	_, seen, ok := strings.Cut(out, "repositories seen here")
	if !ok {
		t.Fatalf("the listing does not print the seen half:\n%s", out)
	}
	var lines []string
	for _, line := range strings.Split(seen, "\n")[1:] {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("the seen half holds %d entries, want 3:\n%s", len(lines), seen)
	}
	// The name each line is about is what this compares: the notes differ on
	// their own (a name of the slug alphabet is told it is a slug), so the
	// whole line is not the unit — the entry's own cell is. Every pair is
	// compared, not the first two: one collision is as bad as another.
	nameOf := func(line string) string {
		if i := strings.Index(line, "  ("); i >= 0 {
			return strings.TrimSpace(line[:i])
		}
		return strings.TrimSpace(line)
	}
	printed := make([]string, 0, len(lines))
	for _, line := range lines {
		printed = append(printed, nameOf(line))
	}
	// The comparison is on what an operator *sees*, not on the bytes: every
	// character in play is invisible, so two cells that differ only in one of
	// them read as one line however they are stored. safetext.Field is that
	// reading — it drops exactly the characters a terminal shows nothing for.
	seenAs := func(cell string) string { return safetext.Field(cell) }
	for i := range printed {
		for j := i + 1; j < len(printed); j++ {
			if seenAs(printed[i]) == seenAs(printed[j]) {
				t.Errorf("two different run-written names read as one: %q and %q both print as %q, so the operator cannot tell the entry the allow-list refuses from the one it admits", printed[i], printed[j], seenAs(printed[i]))
			}
		}
	}
	// Each lookalike reads as what it is: the character it carries is escaped,
	// never dropped and never emitted invisibly.
	// The want is spelled from the rune: a \uXXXX escape written into this file
	// would be the character itself by the time Go read it, invisible in the
	// source and in every diff of it.
	for _, tc := range []struct {
		what string
		name string
		drop rune
	}{
		{"a format character", cfLookalike, 0x2066},
		{"an invisible character that is not a format character", nonCfLookalike, 0xFE00},
	} {
		want := fmt.Sprintf("\\u%04x", tc.drop)
		got := printableName(tc.name)
		if !strings.Contains(got, want) || !strings.Contains(seen, got) {
			t.Errorf("the name carrying %s is not printed with it escaped (want %q in %q):\n%s", tc.what, want, got, seen)
		}
	}
	// The format-character half is escaped the way the rest of this program
	// escapes one, so a listing and a refusal name the same value alike.
	if !strings.Contains(seen, pluginwire.PrintableN(cfLookalike, safetext.FieldMax)) {
		t.Errorf("the name carrying format characters is not printed through the escaping the rest of the CLI uses, so the two read differently:\n%s", seen)
	}
	if line := listLine(t, seen, plain); strings.Contains(line, `\u`) {
		t.Errorf("a plain name is printed escaped: %s", line)
	}
	assertBoundedAndInert(t, "`project repos ls`", out)
}

// TestProjectReposLsBoundsHowManyNamesItLists: the seen list is as long as
// whoever wrote the names made it — a run's grant lets it write runs/<slug>/
// prefixes (the bucket holds an object key, which no 255-byte component limit
// applies to) — and this listing is the one that tells an operator what to
// enroll. Unbounded, a run that wrote a thousand prefixes would push this
// command's own words off the screen: the header, the sources, and the fact
// that what they are reading is a listing of what has run here. It stops at
// safetext.FieldListMax entries and counts the rest in this program's own
// words (safetext.FieldListNote), so a listing that stopped at twenty does not
// read as a list of twenty.
//
// Mutation (verified, run and restored): drop the FieldListMax break (print
// every entry) — this test fails, 25 entry lines and no count.
func TestProjectReposLsBoundsHowManyNamesItLists(t *testing.T) {
	f := newSeenFixture(t)
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	const total = safetext.FieldListMax + 5
	for i := 0; i < total; i++ {
		putRun(t, f.dir, fmt.Sprintf("ghost-%02d", i))
	}
	out, _, err := execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	_, seen, ok := strings.Cut(out, "repositories seen here")
	if !ok {
		t.Fatalf("the listing does not print the seen half:\n%s", out)
	}
	entries := 0
	for _, line := range strings.Split(seen, "\n")[1:] {
		if strings.TrimSpace(line) != "" {
			entries++
		}
	}
	// The entries, the count of what is left out, and nothing else.
	if entries != safetext.FieldListMax+1 {
		t.Errorf("the seen half holds %d lines after the header, want %d entries and the count of the rest:\n%s", entries, safetext.FieldListMax+1, seen)
	}
	if note := safetext.FieldListNote(total - safetext.FieldListMax); !strings.Contains(seen, note) {
		t.Errorf("the listing does not count the entries it left out (%q):\n%s", note, seen)
	}
}

// TestProjectReposLsFailsClosedOnADocumentNameItCannotRead: the seen half
// leaves out a spendDaily document whose *fields* it cannot read (one day of
// one repository, and the bucket still names the repository), and that is
// deliberately not the same thing as a document whose *name* it cannot read.
// firestore.Client's decode takes a document name of exactly two segments
// after /documents/, so a spendDaily object named with a slug of more than one
// segment — a slug the registry held with a "/" in it — fails the query
// itself, and this command reports that error and prints nothing at all. That
// is the fail-closed half: an operator is told the history could not be read
// rather than handed a listing of the part of it that could. It is also why
// the wildcard a run can reach through this source is a single segment ("*"),
// and not a pattern like "github/*/*".
//
// Mutation (verified, run and restored): skip a document the query refused
// (treat firestore's error as an empty page) — this test fails, the command
// succeeds and prints a listing of what is left.
func TestProjectReposLsFailsClosedOnADocumentNameItCannotRead(t *testing.T) {
	f := newSeenFixture(t)
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	f.spent(t, budget.DayDate(budget.Day(f.now)), "github/x/y", "github/x/y")

	out, _, err := execute(t, "project", "repos", "ls")
	if err == nil {
		t.Fatalf("the listing did not report a spendDaily document it cannot read:\n%s", out)
	}
	if !strings.Contains(err.Error(), "unexpected document name") {
		t.Errorf("the listing failed for another reason than the document's name: %v", err)
	}
	if out != "" {
		t.Errorf("the listing printed before it refused, so it reads as one that finished:\n%s", out)
	}
}

// TestProjectShowRunningNamesBoundedAndInert: `project show`'s running list is
// the run registry's keys, which runs write (a slug, and for a key the
// database does not take as it stands, budget.Key's percent-escapes of it —
// which leave a bidi override, a zero-width character and the length of the
// name alone) and which both forms of the command print: the table line and
// the --json document's running_now and running_not_enrolled. The raw values
// stay the ones admission is asked of; what is printed is the bounded
// printable form, the same value in both forms of the command so a table and a
// document that mirror each other cannot disagree.
//
// Mutations (both verified, run and restored): print r raw in the running
// loop — this test fails on the override and the line length; leave
// doc.Running/ doc.RunningNotEnrolled raw — it fails on the JSON half.
func TestProjectShowRunningNamesBoundedAndInert(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	f.db.Set(budget.PathAgent(hostileSeenName, "20261015-120000-aaaa"), map[string]any{"run_id": "20261015-120000-aaaa"})
	// And the dense name, which is what the --json half's bound is about: the
	// escaped form is six characters per character of the name, so a bound on
	// what the escaper reads would put three thousand runes in the document.
	f.db.Set(budget.PathAgent(denseSeenName, "20261015-120000-bbbb"), map[string]any{"run_id": "20261015-120000-bbbb"})

	out, _, err := execute(t, "project", "show")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "repositories running now") {
		t.Fatalf("project show no longer prints the running list:\n%s", out)
	}
	assertBoundedAndInert(t, "`project show`", out)

	js, _, err := execute(t, "project", "show", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, js)
	}
	var doc struct {
		Running            []string `json:"running_now"`
		RunningNotEnrolled []string `json:"running_not_enrolled"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("--json is not JSON (%v):\n%s", err, js)
	}
	for _, got := range [][]string{doc.Running, doc.RunningNotEnrolled} {
		if len(got) != 2 {
			t.Fatalf("--json = %s, want both run-written repositories in each list", js)
		}
		for _, name := range got {
			assertBoundedJSONName(t, "--json", name)
		}
	}
	if !slices.Equal(doc.Running, doc.RunningNotEnrolled) {
		t.Errorf("--json names the same repositories differently in its two lists: %q and %q", doc.Running, doc.RunningNotEnrolled)
	}
	// The table prints the value the document carries, so the two forms of one
	// command agree.
	for _, name := range doc.Running {
		if !strings.Contains(out, name) {
			t.Errorf("`project show` does not print the repository --json names (%q):\n%s", name, out)
		}
	}
}

// assertBoundedJSONName is assertBoundedAndInert for one value a --json
// document carries: it must be the bounded printable form and nothing a
// run-written name could have put in it.
func assertBoundedJSONName(t *testing.T, what, got string) {
	t.Helper()
	if got == "" {
		t.Errorf("%s carries an empty repository name", what)
	}
	if len([]rune(got)) > printableNameMax || got != strings.Join(strings.Fields(got), " ") {
		t.Errorf("%s carries %q, which is not the bounded printable form", what, got)
	}
	for _, r := range got {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || (r >= 0x202a && r <= 0x202e) || r == 0x200b || r == 0x200d {
			t.Errorf("%s carries %q, in which the run-written name survives: %U", what, got, r)
		}
	}
}

// TestProjectReposLsNeverAdvertisesAPatternAsTheRemedy: the advisory half of a
// seen entry is a command an operator is invited to run, and it may only ever
// name a repository. It was built from any name that was not already an
// allow-list pattern (projectpolicy.ValidPattern) — and `ValidPattern("*")` is
// nil, so a run, whose grant lets it write any runs/<slug>/ prefix (the file's
// own comment says so), could write the prefix `runs/*/` and make this program
// print
//
//   - (seen in the runs bucket; not admitted: … 'fugaro project repos add *')
//
// which is the one command that enrolls every repository there is. spendDaily's
// own slug field reaches the same sentence through the other source. The remedy
// is now built only from a repository identifier (enrollableArg:
// provider/owner/name over the repository alphabet, no wildcard, no whitespace,
// no segment opening with "-"), quoted as one shell word; a name that is not one
// gets no command at all, whatever its shape.
//
// A "*" is the shape to probe with, and the only wildcard these two sources can
// actually deliver: spendDaily's document ID is `<date>_<slug>`, so a slug with
// a "/" in it (github/*/*) is refused by the query before this listing sees it
// (the listing fails rather than printing it), and a bucket prefix is one path
// segment, which cannot hold a "/" either.
//
// Mutations (both verified, run and restored): put the remedy back on
// `projectpolicy.ValidPattern(r) != nil` — this test fails on the `*` entry,
// which prints the command it must not print; drop the wildcard rejection from
// the identifier rule (accept any name of three "/"-segments) — it fails on
// `github/*/*` offered as one.
func TestProjectReposLsNeverAdvertisesAPatternAsTheRemedy(t *testing.T) {
	f := newSeenFixture(t)
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	// The wildcard, written the way a run writes one: a directory named "*"
	// under runs/ (the bucket lists it as the prefix `runs/*/`), and the same
	// name as spendDaily's own slug field.
	putRun(t, f.dir, "*")
	f.spent(t, budget.DayDate(budget.Day(f.now)), "*", "*")

	out, _, err := execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	_, seen, ok := strings.Cut(out, "repositories seen here")
	if !ok {
		t.Fatalf("the listing does not print the seen half:\n%s", out)
	}
	const wildcard = "*"
	line := listLine(t, seen, wildcard)
	if strings.Contains(line, "repos add "+wildcard) {
		t.Errorf("the listing tells the operator to run a command built from the run-written name %q:\n%s", wildcard, line)
	}
	if !strings.Contains(line, "not admitted") || !strings.Contains(line, "not a repository name") {
		t.Errorf("the entry for %q does not say the allow-list refuses it, nor that it is not an enrollable name:\n%s", wildcard, line)
	}
	// The positive half: a name this machine's config resolves to a repository
	// still gets the remedy, and it is the repository's canonical name.
	const other = "bitbucket/acme/other"
	putRun(t, f.dir, slugOf(t, "bitbucket", "acme/other"))
	out, _, err = execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	_, seen, _ = strings.Cut(out, "repositories seen here")
	if line := listLine(t, seen, other); !strings.Contains(line, "'fugaro project repos add "+other+"'") {
		t.Errorf("a repository this machine's config names no longer gets the remedy that enrolls it: %s", line)
	}
}

// TestProjectReposLsNeverAdvertisesACommandItWouldRefuse: the remedy is printed
// to be pasted into a shell, so it has to be a command that WORKS. `repos add`
// validates its argument with projectpolicy.ValidPattern, whose first segment
// must be a git provider kind (config.Providers: github, bitbucket) — while the
// shape of a provider (task.ValidProvider: ^[a-z][a-z0-9-]*$) admits any
// lower-case word. A spendDaily slug of `prod/acme/app`, the name one
// hand-written document's slug field can carry, is provider-shaped, so a gate
// that only reads the shape handed the operator
//
//   - (seen in spendDaily; not admitted: … 'fugaro project repos add prod/acme/app')
//
// and that command exits with `"prod" is not a git provider kind (github,
// bitbucket)`. The name is a repository identifier AND its first segment has to
// be a provider kind this Fugaro knows, or the name gets no command at all.
//
// Mutation (verified, run and restored): put the provider test back on
// task.ValidProvider — this test fails on the hand-written slug, which prints
// the command `repos add` refuses.
func TestProjectReposLsNeverAdvertisesACommandItWouldRefuse(t *testing.T) {
	f := newSeenFixture(t)
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	// Written by hand, the way the comment on spendRepos describes: the ID is
	// the day's own (firestore's validName refuses a "/" in a document ID, so
	// Fugaro's own writer never stores a slug that way) and the slug FIELD is
	// the name this listing reads (budget.FromFields) and prints.
	const notAProvider = "prod/acme/app"
	rec := budget.DayRecord{Repo: notAProvider, Slug: notAProvider, Date: budget.DayDate(budget.Day(f.now)),
		Version: budget.DayRecordVersion, Final: true, SpentMicros: budget.Micros(1_000_000), Runs: 1}
	f.fs.Set(budget.SpendCollection, budget.DayDate(budget.Day(f.now))+"_handwritten", rec.ToFields())

	out, _, err := execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	_, seen, ok := strings.Cut(out, "repositories seen here")
	if !ok {
		t.Fatalf("the listing does not print the seen half:\n%s", out)
	}
	line := listLine(t, seen, notAProvider)
	if strings.Contains(line, "repos add "+notAProvider) {
		t.Errorf("the listing tells the operator to run `fugaro project repos add %s`, which `repos add` refuses (%q is not a git provider kind):\n%s", notAProvider, "prod", line)
	}
	if !strings.Contains(line, "not admitted") || !strings.Contains(line, "not a repository name") {
		t.Errorf("the entry for %q does not say the allow-list refuses it, nor that it is not an enrollable name:\n%s", notAProvider, line)
	}

	// The same defect one bound over: a hand-written slug field of 256 bytes is
	// identifier-shaped and inside safetext.FieldMax, so a gate bounded at
	// FieldMax printed `fugaro project repos add github/acme/<300 bytes>` —
	// which `repos add` refuses with "the pattern is N characters; at most 255
	// are allowed" (projectpolicy.MaxPatternLen, the bound the command applies
	// and the one the gate is now held to). Nothing here is a wildcard or a
	// provider problem: the ONLY thing wrong with this name is its length.
	long := "github/acme/" + strings.Repeat("a", projectpolicy.MaxPatternLen-len("github/acme/")+1)
	rec2 := budget.DayRecord{Repo: long, Slug: long, Date: budget.DayDate(budget.Day(f.now)),
		Version: budget.DayRecordVersion, Final: true, SpentMicros: budget.Micros(1_000_000), Runs: 1}
	f.fs.Set(budget.SpendCollection, budget.DayDate(budget.Day(f.now))+"_handwritten_long", rec2.ToFields())
	out2, _, err := execute(t, "project", "repos", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out2)
	}
	_, seen2, ok2 := strings.Cut(out2, "repositories seen here")
	if !ok2 {
		t.Fatalf("the listing does not print the seen half:\n%s", out2)
	}
	longLine := listLine(t, seen2, long)
	// The NAME, not the word "repos add": the refusal sentence these lines
	// carry ends with a generic 'fugaro project repos add provider/owner/name'
	// whatever the name is, so only the name itself says a command was built
	// from it.
	if strings.Contains(longLine, "repos add "+long) {
		t.Errorf("the listing tells the operator to run a `repos add` command for a %d-byte name, which `repos add` refuses (projectpolicy.MaxPatternLen is %d):\n%s", len(long), projectpolicy.MaxPatternLen, longLine)
	}
	if !strings.Contains(longLine, "not admitted") || !strings.Contains(longLine, "not a repository name") {
		t.Errorf("the entry for the %d-byte name does not say the allow-list refuses it, nor that it is not an enrollable name:\n%s", len(long), longLine)
	}
}

// TestProjectShowNeverAdvertisesAPatternAsTheRemedy: `project show`'s running
// list is the run registry's keys, which a run writes, and its not-admitted
// note goes through the same sentence as `repos ls` — so it had the same hole
// (budget.Key does not escape "*", and a registry key of `*` printed
//
//   - (not admitted: … 'fugaro project repos add *')
//
// ). The remedy is built from a repository identifier or not at all, in both
// listings, because they share one sentence.
//
// Mutation (verified, run and restored): put the remedy back on
// `projectpolicy.ValidPattern(r) != nil` — this test fails on the `*` entry.
func TestProjectShowNeverAdvertisesAPatternAsTheRemedy(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	f.db.Set(budget.PathAgent("*", "20261015-120000-aaaa"), map[string]any{"run_id": "20261015-120000-aaaa"})

	out, _, err := execute(t, "project", "show")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	const wildcard = "*"
	line := listLine(t, out, wildcard)
	if strings.Contains(line, "repos add "+wildcard) {
		t.Errorf("project show tells the operator to run a command built from the run-written registry key %q:\n%s", wildcard, line)
	}
	if !strings.Contains(line, "not admitted") || !strings.Contains(line, "not a repository name") {
		t.Errorf("the running entry for %q does not say the allow-list refuses it, nor that it is not an enrollable name:\n%s", wildcard, line)
	}
	js, _, err := execute(t, "project", "show", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, js)
	}
	var doc struct {
		RunningNotEnrolled []string `json:"running_not_enrolled"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("--json is not JSON (%v):\n%s", err, js)
	}
	if len(doc.RunningNotEnrolled) != 1 || doc.RunningNotEnrolled[0] != wildcard {
		t.Errorf("running_not_enrolled = %v, want [%s]: the value itself is the run's, and only the printed advisory is held to repositories", doc.RunningNotEnrolled, wildcard)
	}
}

// TestEnrollableArgHoldsTheRemedyToARepository: the one function that decides
// whether a listing may print a `fugaro project repos add` command for a name,
// and which name it prints. It is the guard behind the two tests above, so it
// is pinned directly as well as through them: a widening of its rule that the
// listings' shapes happen not to reach (a wildcard in a nested name, say, which
// spendDaily's document ID refuses and a bucket prefix cannot hold) still turns
// this test red.
//
// Mutations (each verified, run and restored): accept any name of three or more
// "/"-segments (drop the alphabet check) — red on `github/*/*`, `github/acme/app
// b`, `github/acme/app;id` and the uppercase provider; drop the "-"/"."/".."
// segment rule — red on `-x/y/z`, `github/acme/-app` and `github/acme/..`;
// drop the length bound — red on the name one byte over
// projectpolicy.MaxPatternLen, the bound `repos add` applies;
// put the provider test back on task.ValidProvider, which reads the shape of a
// provider and nothing else — red on `prod/acme/app`, the name `repos add`
// refuses even though it is shaped like a provider.
func TestEnrollableArgHoldsTheRemedyToARepository(t *testing.T) {
	for _, ok := range []string{
		"github/acme/app",
		// A trailing ".git" on a NAME (not on a whole ".git" segment) is a name
		// `repos add` takes: task.CanonicalRepo strips the suffix and stores
		// "github/acme/app". The gate must not refuse it, which is why the rule
		// below is about the last segment and not the suffix.
		"github/acme/app.git",
		"bitbucket/acme/group/app", // a repository path may nest
		"github/Acme/App",          // the repository alphabet admits A-Z; `repos add` lower-cases its argument, so this one works
		// Exactly at the bound `repos add` applies (projectpolicy.MaxPatternLen
		// bytes) is still a name: ValidPattern accepts 255 characters and
		// refuses 256. safetext.FieldMax is 512, and bounding at THAT handed the
		// operator a command the command refuses.
		"github/acme/" + strings.Repeat("a", projectpolicy.MaxPatternLen-len("github/acme/")),
	} {
		got, is := enrollableArg(ok)
		if !is {
			t.Errorf("enrollableArg(%q) refused a repository identifier", ok)
			continue
		}
		if got != ok {
			t.Errorf("enrollableArg(%q) = %q, want the name itself (the alphabet is inside shellword's safe set, so it prints bare)", ok, got)
		}
	}
	for _, bad := range []string{
		"",                    // nothing
		"*",                   // the allow-list's own entry: admits every repository
		"github/*/*",          // a pattern a run wrote
		"acme/app",            // two segments: `repos add` resolves its provider through a config these listings do not have
		"github/acme",         // no repository name
		"GitHub/acme/app",     // not a provider kind (task.ValidProvider is lower-case)
		"prod/acme/app",       // provider-shaped, but not a git provider kind: `repos add` refuses this argument (projectpolicy.ValidPattern)
		"-x/y/z",              // a shell word opening with a dash reads as an option
		"github/acme/-app",    // the same, one segment in
		"github/acme/..",      // a directory, not a repository
		"github/../app",       // the same
		"github//app",         // an empty segment
		"github/acme/app b",   // whitespace
		"github/acme/app\n",   // a control: the name a run writes is not a shell word
		"github/acme/app;id",  // shell metacharacters
		"github/acme/app`id`", // and backticks
		"../acme/app",         // a path out of the tree
		// The last segment is exactly ".git": `repos add` strips a trailing
		// ".git" (after lower-casing) and is left with an empty segment, so it
		// refuses the name — and this gate printed
		// `fugaro project repos add github/acme/.git`, which the command answers
		// with "is not a repository: the repository must look like owner/name".
		"github/acme/.git",
		"github/acme/.GIT", // the strip is applied after ASCIILower, so the case does not save it
		// One byte over the bound `repos add` applies: the advisory line must
		// not be as long as a run chose, and the command in it must be one that
		// command accepts.
		"github/acme/" + strings.Repeat("a", projectpolicy.MaxPatternLen-len("github/acme/")+1),
	} {
		if got, is := enrollableArg(bad); is {
			t.Errorf("enrollableArg(%q) = %q, true; want no command at all", bad, got)
		}
	}
}

// TestEnrollableArgAgreesWithReposAddOnEveryName: the property the table above
// claims — every name it accepts is a name `repos add` accepts — pinned by
// running BOTH for each name, so the gate cannot accept a name the command
// refuses without this failing. The table alone did not hold it: it carried the shapes
// the author thought of, and "github/acme/.git" was not one of them, so the
// gate printed `fugaro project repos add github/acme/.git` and the command
// answered "is not a repository: the repository must look like owner/name" —
// the same defect as the provider-kind and the length bounds, found the same
// way (by running the pair instead of reading the validator).
//
// Mutations (each verified, run and restored): drop the ".git" last-segment
// rule — red on both ".git" names; widen it to a suffix test on the whole name
// — red on "github/acme/app.git", which `repos add` accepts and this then
// refuses; widen it from the last segment to every segment — red on
// "github/.git/app"; move it to the FIRST segment — red on both ".git" names,
// the shape it exists for.
func TestEnrollableArgAgreesWithReposAddOnEveryName(t *testing.T) {
	// The implication runs ONE way and that is the property: a name this gate
	// accepts is a name `repos add` accepts. The converse is deliberately false
	// — `repos add github/*/*` would enrol every repository, and refusing to
	// print it is the point of the gate — so the names it refuses are not
	// checked against the command here.
	for _, name := range []string{
		"github/acme/app",
		"bitbucket/acme/group/app",
		"github/Acme/App",
		"github/acme/app.git", // the command strips the suffix and stores the name
		"github/.git/app",     // a ".git" SEGMENT in the middle is not the shape below
		"github/acme/git",
		"github/acme/" + strings.Repeat("a", projectpolicy.MaxPatternLen-len("github/acme/")),
	} {
		got, ok := enrollableArg(name)
		if !ok {
			t.Errorf("enrollableArg(%q) refused a repository identifier, so this test checks nothing about it", name)
			continue
		}
		newProjectFixture(t, "")
		if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := execute(t, "project", "repos", "add", name); err != nil {
			t.Errorf("enrollableArg(%q) = %q, and a listing prints `fugaro project repos add %s`, but `repos add` refuses it: %v", name, got, name, err)
		}
	}

	// And the shape the table above was missing, with both halves: the gate
	// refuses it AND the command refuses it, which is what makes the gate's
	// refusal the right answer rather than a lost remedy.
	for _, name := range []string{"github/acme/.git", "github/acme/.GIT"} {
		if got, ok := enrollableArg(name); ok {
			t.Errorf("enrollableArg(%q) = %q; want no command at all: `repos add` strips a trailing \".git\" and is left with an empty segment, so it refuses this name", name, got)
		}
		newProjectFixture(t, "")
		if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := execute(t, "project", "repos", "add", name); err == nil {
			t.Errorf("`repos add` accepted %q, so refusing it in the listing loses a remedy that works", name)
		}
	}
}

// TestReposAddTakesTheLengthEnrollableArgBoundsAt: the boundary above, run
// against the command itself and not against the validator the comment cites.
// enrollableArg refuses at projectpolicy.MaxPatternLen because that is what
// `repos add` accepts — and if the command's own bound ever moved, the gate
// would go on printing a remedy the command refuses, which is the whole defect.
// So the pair is pinned end to end: exactly MaxPatternLen bytes is enrolled,
// one byte more is refused.
func TestReposAddTakesTheLengthEnrollableArgBoundsAt(t *testing.T) {
	newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	at := "github/acme/" + strings.Repeat("a", projectpolicy.MaxPatternLen-len("github/acme/"))
	if _, _, err := execute(t, "project", "repos", "add", at); err != nil {
		t.Fatalf("`repos add` refused a %d-byte name, the length enrollableArg prints a command for: %v", len(at), err)
	}
	if _, _, err := execute(t, "project", "repos", "add", at+"a"); err == nil {
		t.Fatalf("`repos add` accepted a %d-byte name, one byte over the bound enrollableArg refuses at: the gate and the command have drifted apart", len(at)+1)
	}
}

// TestProjectAllowListDoorsAreRefusedInAnAgentSession: SD8's allow-list half.
// The three commands that write `allow_repos` — and, through it, which
// repository a run of the project may be — are the operator's, the same way
// `project policy publish` is: refused before the read, so nothing is written
// and no node is even looked at, and, for `sync`, without the App's private key
// ever being read. The `--github-app-key` arm is the one that has to hold with
// the flag as well as without it: the flag is what allows that read, and it is
// not a way past this refusal (TestProjectSyncRefusesTheKeyReadInACodingAgentSession
// is the flag's own test, which pins the sentence the flag carries).
func TestProjectAllowListDoorsAreRefusedInAnAgentSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		sync bool
	}{
		{"repos add", []string{"project", "repos", "add", "github/acme/new"}, false},
		{"repos rm", []string{"project", "repos", "rm", "github/acme/app", "--yes"}, false},
		{"sync", []string{"project", "sync", "--github-app-id", "12345"}, true},
		{"sync --github-app-key", []string{"project", "sync", "--github-app-id", "12345", "--github-app-key"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjectFixture(t, "")
			f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
			var g *syncRig
			if tc.sync {
				g = newSyncRig(t, "acme/new")
			}
			t.Setenv("CLAUDECODE", "1")
			before := nodeOf(t, f.db)

			out, errOut, err := executeStdin(t, "", tc.args...)
			if err == nil {
				t.Fatalf("a coding agent's session edited the allow-list:\n%s%s", out, errOut)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			for _, want := range []string{"nothing was written", "coding agent", "allow-list"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if after := nodeOf(t, f.db); after != before {
				t.Errorf("the database changed in a coding agent's session:\nbefore %s\nafter  %s", before, after)
			}
			if g != nil && len(g.reads) != 0 {
				t.Errorf("the App's private key was read in a coding agent's session: %v", g.reads)
			}
		})
	}
}
