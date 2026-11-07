// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

// `fugaro graph` (CG3 of docs/cli-output-console-graph/implementation-plan-graph.md):
// the command the console spawns, and the two boundaries it is:
//
//   - TestGraphRefusesAnInvalidScopeBeforeAnyRead is H1. --source reaches argv
//     from the console's renderer, so every value outside graph.ParseScope's
//     closed set is refused before the project is even selected — no header on
//     stderr, no call to the database, nothing on stdout — and a scope this
//     build has no reader for is refused with the honest sentence rather than
//     answered with an empty graph. The control at the end keeps the "no read"
//     half from passing vacuously.
//   - TestGraphAllIsTheCoordinatorToday: `all` is the union of the scopes that
//     exist, which today is the coordinator alone, and the document names it as
//     itself — `coordinator` is the source half of every key it produced and the
//     value the console sends back (contract §6.22). The subtests are the two
//     halves of §6.13's read: a stored view, and a project the coordinator has
//     never reconciled (the runs alone, `down`).
//   - TestGraphJSONIsOneVersionedDocument: the shape a reader outside this
//     binary consumes, read back strictly.
//   - TestGraphNeedsNoCheckout: the read paints from a directory that is not a
//     checkout, which is what the plan scope's card is not.
//   - TestGraphEmptyProjectIsAnEmptyDocument: a project that lists no repository
//     is an empty document and not a refusal, because the refusal would be read
//     as "this CLI has no graph".
//   - TestGraphTextTableNamesTheScopeAndGroupsByRelease: the human half, which
//     groups by release because the coordinator's view has no edges (F17).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/ee/graph"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestMain closes the ways a test could reach the developer's own machine or
// Google for real: no application default credentials (a file that doesn't
// exist), no metadata server, and every https request sent to a proxy nothing
// listens on (Go sends loopback traffic, which is all the fakes use, around any
// proxy). It is internal/cli's TestMain, which this package's tests need for
// the same reason: they drive the same command tree, and without it a read
// would go to the ambient credentials and a real runs bucket.
//
// It also keeps the two directories a command writes out of the developer's own
// home — $XDG_CACHE_HOME (the update-check cache, and the project-name and
// layer caches) and $XDG_CONFIG_HOME (checkouts.json, which the console's first
// run reads) — and tells git not to find this repository's own fugaro.yaml, so
// a command that resolves the checkout's project resolves the fixture's and
// never the project this repository belongs to.
//
// It is in this file rather than in a main_test.go of its own, where the
// sibling ee/ packages keep theirs, because this package has one test file and
// CG3's file budget is spent (the plan's file list).
func TestMain(m *testing.M) {
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/fugaro-tests-have-no-credentials.json")
	os.Setenv("GCE_METADATA_HOST", "127.0.0.1:1")
	os.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	os.Setenv("https_proxy", "http://127.0.0.1:1")
	os.Unsetenv("STORAGE_EMULATOR_HOST")
	for _, k := range []string{"XDG_CACHE_HOME", "XDG_CONFIG_HOME"} {
		if dir, err := os.MkdirTemp("", "fugaro-test-ee-"); err == nil {
			os.Setenv(k, dir)
		}
	}
	// git stops before it reaches the repository root, the way internal/cli's
	// TestMain stops it: Dir(Dir(wd)) is above this package's own directory and
	// below the root that holds .git.
	if wd, err := os.Getwd(); err == nil {
		os.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(filepath.Dir(wd)))
	}
	os.Exit(m.Run())
}

// appSlug is the storage slug of the fixture's repository, GitHub's acme/app.
var appSlug = mustSlug("github", "acme/app")

func mustSlug(provider, repo string) string {
	s, err := task.Slug(provider, repo)
	if err != nil {
		panic(err)
	}
	return s
}

// graphTestRoot is the tree these tests drive: the released tree with this
// package's command in place of the stub a build without it shows for the name.
//
// It assembles the tree itself rather than blank-importing ee/register, and the
// reason is that it cannot: ee/register imports this package, so an import of
// it from here is a cycle ("import cycle not allowed in test"), and a fixture
// may not call cli.RegisterCommand — only ee/register may (E1.5's rule, held by
// internal/eeboundary). ee/register/graph_test.go is where the registration
// itself is pinned, against the tree the tagged binary builds.
func graphTestRoot() *cobra.Command {
	root := fcli.NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "graph" {
			root.RemoveCommand(c)
			break
		}
	}
	root.AddCommand(NewGraphCmd())
	return root
}

// execute runs the graph's command tree with args and captures its output.
func execute(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := graphTestRoot()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(fcli.NormalizeArgs(args))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// graphDocument runs the command and reads its document back the way a reader
// outside this binary does: strictly, so a field this build does not know is an
// error rather than a key quietly dropped (graph.ParseDocument).
func graphDocument(t *testing.T, args ...string) graph.Document {
	t.Helper()
	out, errOut, err := execute(t, args...)
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	doc, err := graph.ParseDocument([]byte(out))
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return doc
}

// graphFixture is the fixture these tests drive: a local config pointing at a
// fake database and a file:// runs bucket, the project marker every cloud
// command checks, and the database the coordinator's view is stored in.
type graphFixture struct {
	dir    string
	bucket string // file:// URL
	db     *gcpfake.RTDB
	run    *gcpfake.Run
}

func newGraphFixture(t *testing.T) *graphFixture {
	t.Helper()
	f := &graphFixture{dir: t.TempDir(), db: gcpfake.NewRTDB(t), run: gcpfake.NewRun(t)}
	// Executions the fake starts live where the config's backend looks.
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	runs := filepath.Join(f.dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	f.bucket = "file://" + runs
	path := f.writeConfig(t, "  acme/app: { provider: github, base_branch: main, workflows: [web] }\n")
	t.Setenv("FUGARO_CONFIG", path)
	f.run.AddJob(gcp.JobName(appSlug, "web"), "4", "8Gi")
	// The installation names its project in the runs bucket, as its Terraform
	// does; every cloud command checks it.
	data, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(f.dir, "runs", filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// writeConfig writes a project config holding the given repos block and answers
// its path: the fixture's own is the one every test gets, and a test that needs
// a project listing no repository writes a second one and passes it with
// --config.
func (f *graphFixture) writeConfig(t *testing.T, repos string) string {
	t.Helper()
	xdg := filepath.Join(f.dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// The project-name check caches its answer here, never in the developer's
	// own cache.
	t.Setenv("XDG_CACHE_HOME", filepath.Join(f.dir, "cache"))
	t.Setenv("FUGARO_PROJECT", "")
	t.Setenv("FUGARO_CONFIG", "")
	projects := filepath.Join(xdg, "fugaro", "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	logging := gcpfake.NewLogging(t)
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\nbucket_url: " + f.bucket +
		"\nuser: someone@example.com\nmax_parallel: 2\n" +
		"endpoints: { run: " + f.run.URL + "/, logging: " + logging.URL + "/, no_auth: true }\n" +
		"budget: { mode: observe, per_run_usd: 5, rtdb_url: " + f.db.URL + " }\n" +
		"repos:\n" + repos
	path := filepath.Join(projects, "aurora.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// store is the coordinator's store over the fixture's database, opened the way
// the command's own read opens one: the same client and the same address, with
// no credentials in a test.
func (f *graphFixture) store(t *testing.T) *coordinator.Store {
	t.Helper()
	db, err := rtdb.New(f.db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator.NewStore(db)
}

// writeStoredView leaves v as the repository's stored view: what a sweep that
// reconciled the repository left behind.
func (f *graphFixture) writeStoredView(t *testing.T, v coordinator.View) {
	t.Helper()
	if err := f.store(t).Write(context.Background(), coordinator.PathView(appSlug), v); err != nil {
		t.Fatal(err)
	}
}

// seedRun leaves one run of the fixture's repository in the fixture's bucket: a
// run still in flight that has opened no pull request, which is what the runs
// half of the view holds, and which is dated against the listing's own clock so
// the default window holds it whenever the test runs.
func (f *graphFixture) seedRun(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	id := fcli.LsNow().UTC().Add(-time.Hour).Format("20060102-150405") + "-aaaa"
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	store := runstore.Open(b, appSlug, id)
	if err := store.CreateTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, LaunchedBy: "someone@example.com"}); err != nil {
		t.Fatal(err)
	}
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusRunning, Branch: "fugaro/" + id, BaseBranch: "main"}
	if err := store.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	return id
}

// storedView is what a sweep left for the fixture's repository: one record in
// flight, on a pull request the runs name, carrying the host's own state and
// the release its branch declares.
func storedView() coordinator.View {
	return coordinator.View{
		Repo: "acme/app",
		Records: []coordinator.Record{{
			PR: &coordinator.PullRequest{
				Number: 12, URL: "https://github.com/acme/app/pull/12",
				Title: "the stored title", Branch: "release/v0.11.0", State: coordinator.StateOpen,
			},
			Runs: []coordinator.Run{{
				ID: "20261013-100000-aaaa", PR: 12, Status: "succeeded",
				Branch: "release/v0.11.0", DeclaredBy: "someone@example.com", Files: []string{},
			}},
			Branch: "release/v0.11.0", Release: "0.11.0", InFlight: true,
			Files: []string{}, Services: []string{},
		}},
		Collisions: []coordinator.Collision{},
	}
}

// TestGraphRefusesAnInvalidScopeBeforeAnyRead is H1, and the reason it is the
// command's first statement: the value comes from the console's renderer, so it
// is a string nobody here controls. Every value outside the closed set is
// refused with graph.ParseScope's own sentence — never with the refusal a scope
// this build lacks gets, which is what a loosened pattern would produce — and
// nothing has been read by then: no project header on stderr, no call to the
// database, nothing on stdout.
func TestGraphRefusesAnInvalidScopeBeforeAnyRead(t *testing.T) {
	f := newGraphFixture(t)
	// A stored view, so a read that happened would have something to answer
	// with: the fixture is a working graph, and only the scope is wrong.
	f.writeStoredView(t, storedView())

	// The closed set's complement: an empty value, an unknown kind, an id on a
	// kind that carries none, an empty, too long, upper-case, punctuated,
	// path-shaped or spaced id, a bare flag, and a leading hyphen.
	for _, bad := range []string{
		"", "all:1", "coordinator:", "coordinator:1", "orchestrators", "Coordinator",
		"orchestrator:", "orchestrator:-o1", "orchestrator:O1", "orchestrator:o 1",
		"orchestrator:o1/x", "orchestrator:o1/../../etc", "orchestrator:o.1",
		"orchestrator:" + strings.Repeat("o", 41), "orchestrator:o1:x",
		"local:", "local:/tmp", "--json", "-x",
	} {
		t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
			// The pattern is checked first, on its own: a value this list calls
			// invalid and graph.ParseScope accepts is a defect in the pattern
			// (H1's mutation), and it would otherwise be hidden by the honest
			// refusal below.
			_, want := graph.ParseScope(bad)
			if want == nil {
				t.Fatalf("graph.ParseScope(%q) accepted a value outside the closed set: the pattern has been loosened", bad)
			}
			before := len(f.db.Requests())
			out, errOut, err := execute(t, "graph", "--source", bad, "--json")
			if err == nil {
				t.Fatalf("graph --source %q was accepted", bad)
			}
			if err.Error() != want.Error() {
				t.Errorf("the refusal is\n  %q\nwant the scope pattern's own\n  %q", err, want)
			}
			if out != "" {
				t.Errorf("the refused scope wrote %q to stdout, want no document at all", out)
			}
			// The header every cloud command writes to stderr first: nothing
			// was selected, so the value reached no path, no flag and no read.
			if errOut != "" {
				t.Errorf("the refused scope wrote %q to stderr: the project was selected before the scope was parsed", errOut)
			}
			if got := len(f.db.Requests()); got != before {
				t.Errorf("the refused scope made %d database calls, want none", got-before)
			}
		})
	}

	// The scopes this build has no reader for are the other refusal: an honest
	// sentence naming the scope, and still nothing read (contract §6.22).
	for _, absent := range []string{"orchestrator:o1", "local:o1"} {
		t.Run(absent, func(t *testing.T) {
			if _, err := graph.ParseScope(absent); err != nil {
				t.Fatalf("graph.ParseScope(%q) refused a scope the closed set holds: %v", absent, err)
			}
			before := len(f.db.Requests())
			out, errOut, err := execute(t, "graph", "--source", absent, "--json")
			if err == nil {
				t.Fatalf("graph --source %q was answered rather than refused", absent)
			}
			if !strings.Contains(err.Error(), "no orchestrator is installed in this build") {
				t.Errorf("the refusal is %q, want it to say no orchestrator is installed in this build", err)
			}
			if out != "" {
				t.Errorf("the refused scope wrote %q to stdout, want no document at all", out)
			}
			if errOut != "" {
				t.Errorf("the refused scope wrote %q to stderr: the project was selected before the scope was refused", errOut)
			}
			if got := len(f.db.Requests()); got != before {
				t.Errorf("the refused scope made %d database calls, want none", got-before)
			}
		})
	}

	// The control: the scopes that do exist read. Without it the "no read" half
	// above would pass just as well on a command that never reads at all.
	for _, ok := range []string{"all", "coordinator"} {
		before := len(f.db.Requests())
		out, errOut, err := execute(t, "graph", "--source", ok, "--json")
		if err != nil {
			t.Fatalf("graph --source %s: %v (stderr: %s)", ok, err, errOut)
		}
		if out == "" {
			t.Errorf("graph --source %s printed no document", ok)
		}
		if got := len(f.db.Requests()); got == before {
			t.Errorf("graph --source %s read nothing: the fixture is not a working read", ok)
		}
	}
}

// TestGraphAllIsTheCoordinatorToday: `all` is the union of the scopes that
// exist, and today exactly one does. The document says so in the only way a
// reader can act on — it lists the coordinator under its own name, which is the
// source half of every key it produced and the value the console sends back as
// --source (contract §6.22) — so `all`, no --source at all, and `coordinator`
// are one document but for when it was assembled.
func TestGraphAllIsTheCoordinatorToday(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())

	all := graphDocument(t, "graph", "--source", "all", "--json")
	def := graphDocument(t, "graph", "--json")
	one := graphDocument(t, "graph", "--source", "coordinator", "--json")

	if len(all.Sources) != 1 {
		t.Fatalf("`all` covers %d scopes, want the one that exists: %+v", len(all.Sources), all.Sources)
	}
	if got := all.Sources[0].Source; got != "coordinator" {
		t.Errorf("`all` lists its scope as %q, want coordinator: the source half of every key it produced, and the value the console sends back", got)
	}
	if got := all.Sources[0].State; got != graph.SourceUp {
		t.Errorf("the state of a scope holding a stored view is %q, want %q", got, graph.SourceUp)
	}
	if len(all.Nodes) != 1 {
		t.Fatalf("`all` drew %d nodes, want the stored record's one: %+v", len(all.Nodes), all.Nodes)
	}
	if got := all.Nodes[0].Key; got != graph.NodeKey("coordinator", "acme/app#12") {
		t.Errorf("the node's key is %q, want %q", got, graph.NodeKey("coordinator", "acme/app#12"))
	}
	for _, doc := range []graph.Document{def, one} {
		doc.GeneratedAt, all.GeneratedAt = time.Time{}, time.Time{}
		if !reflect.DeepEqual(doc, all) {
			t.Errorf("a document `all` is not equal to:\n%+v\nwant\n%+v", doc, all)
		}
	}

	t.Run("a project the coordinator has never reconciled", func(t *testing.T) {
		f := newGraphFixture(t)
		id := f.seedRun(t)
		doc := graphDocument(t, "graph", "--json")
		if len(doc.Sources) != 1 || doc.Sources[0].State != graph.SourceDown {
			t.Fatalf("a project with nothing stored is %+v, want the scope down: the read succeeded and answered with the runs half", doc.Sources)
		}
		if doc.Sources[0].AsOf != nil {
			t.Errorf("a down scope carries as_of %v, want null", doc.Sources[0].AsOf)
		}
		if len(doc.Nodes) != 1 {
			t.Fatalf("the runs half drew %d nodes, want the one run: %+v", len(doc.Nodes), doc.Nodes)
		}
		n := doc.Nodes[0]
		if n.Kind != graph.NodeRun || n.ID != "acme/app#"+id || n.State != "running" {
			t.Errorf("the runs half's node is %+v, want the in-flight run %s", n, id)
		}
	})
}

// TestGraphJSONIsOneVersionedDocument: the document is a wire format a reader
// outside this binary consumes, so its shape is pinned here — the version, the
// two values that say "not a stream", the nulls that mean "the source does not
// say", and the lists that are arrays and never null.
func TestGraphJSONIsOneVersionedDocument(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	before := time.Now()
	out, errOut, err := execute(t, "graph", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("stdout is not one JSON document: %s", out)
	}
	doc, err := graph.ParseDocument([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	// The literal, not graph.DocumentVersion: the point is that the number a
	// reader outside this binary sees did not move.
	if doc.Version != 1 {
		t.Errorf("version = %d, want 1 (graph.DocumentVersion is %d)", doc.Version, graph.DocumentVersion)
	}
	// A one-shot read is not a stream: the two values that say so, without a
	// flag a reader has to be told about.
	if doc.StreamID != "" || doc.Seq != 0 {
		t.Errorf("a one-shot read carries stream_id %q and seq %d, want \"\" and 0", doc.StreamID, doc.Seq)
	}
	if doc.GeneratedAt.Before(before.Add(-time.Minute)) || doc.GeneratedAt.After(time.Now().Add(time.Minute)) {
		t.Errorf("generated_at is %v, want the moment this read was assembled", doc.GeneratedAt)
	}
	if len(doc.Sources) != 1 {
		t.Fatalf("the document covers %d scopes, want one: %+v", len(doc.Sources), doc.Sources)
	}
	s := doc.Sources[0]
	// The stored view carries no time of its own, so this read has none to
	// give: null is "no such time", and never "now".
	if s.AsOf != nil {
		t.Errorf("as_of is %v, want null: the stored view carries no time of its own", s.AsOf)
	}
	if s.Reason != "" {
		t.Errorf("an up scope carries the reason %q, want \"\"", s.Reason)
	}
	want := graph.Node{
		Key: "coordinator:acme/app#12", Source: "coordinator", Kind: graph.NodePullRequest,
		ID: "acme/app#12", Title: "the stored title", Repo: "acme/app",
		State: "open", Release: "0.11.0",
	}
	if len(doc.Nodes) != 1 || !reflect.DeepEqual(doc.Nodes[0], want) {
		t.Errorf("the nodes are\n%+v\nwant\n%+v", doc.Nodes, []graph.Node{want})
	}
	// The coordinator's view has no edges (F17), and an empty list is written
	// as an array: a reader tells "nothing to draw" from a field it did not get.
	for _, field := range []string{`"edges": []`, `"warnings": []`, `"as_of": null`, `"updated": null`, `"stream_id": ""`, `"seq": 0`} {
		if !strings.Contains(out, field) {
			t.Errorf("the document does not write %s:\n%s", field, out)
		}
	}
}

// TestGraphNeedsNoCheckout: the scope that exists paints from a directory that
// is not a checkout, with the repository named by the project config and by
// nothing else. An implementation that took the repository from the working
// directory — plan status's rule, and the one this command exists to not have —
// has no origin remote here to take it from, and fails.
func TestGraphNeedsNoCheckout(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())

	dir := t.TempDir()
	t.Chdir(dir)
	for _, marker := range []string{".git", "fugaro.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			t.Fatalf("the test's working directory holds a %s: it is a checkout, and this test would prove nothing", marker)
		}
	}
	doc := graphDocument(t, "graph", "--json")
	if len(doc.Nodes) != 1 {
		t.Fatalf("the read from a directory that is not a checkout drew %d nodes, want one: %+v", len(doc.Nodes), doc.Nodes)
	}
	if got := doc.Nodes[0].Repo; got != "acme/app" {
		t.Errorf("the node's repository is %q, want the one the project config names", got)
	}
}

// TestGraphEmptyProjectIsAnEmptyDocument: a project that lists no repository is
// an empty graph and not a refusal. The refusal the coordinator's own selection
// makes there names a flag to pass ("name one with --repo"), which this command
// does not have — and a console reads a refused read as "this CLI has no
// graph", which would be a lie about a project that simply has no repository
// yet.
func TestGraphEmptyProjectIsAnEmptyDocument(t *testing.T) {
	f := newGraphFixture(t)
	empty := f.writeConfig(t, "")
	out, errOut, err := execute(t, "graph", "--config", empty, "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	doc, err := graph.ParseDocument([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Nodes) != 0 {
		t.Errorf("a project with no repository drew %d nodes, want none", len(doc.Nodes))
	}
	if len(doc.Sources) != 1 || doc.Sources[0].State != graph.SourceUp {
		t.Errorf("the scope of a project with no repository is %+v, want the coordinator up with nothing to draw", doc.Sources)
	}
}

// TestGraphTextTableNamesTheScopeAndGroupsByRelease: the human half. It groups
// by release because the coordinator's view holds no relation between two
// records (F17), and a node that names no release is grouped under a heading
// that says so rather than under a blank line.
func TestGraphTextTableNamesTheScopeAndGroupsByRelease(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	out, errOut, err := execute(t, "graph")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	for _, want := range []string{"coordinator: up", "0.11.0", "pull_request", "open", "acme/app#12", "the stored title"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table does not hold %q:\n%s", want, out)
		}
	}

	f = newGraphFixture(t)
	f.seedRun(t)
	out, errOut, err = execute(t, "graph")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	for _, want := range []string{"coordinator: down", "no release", "run", "running"} {
		if !strings.Contains(out, want) {
			t.Errorf("the runs half's table does not hold %q:\n%s", want, out)
		}
	}
}
