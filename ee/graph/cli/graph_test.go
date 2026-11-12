// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

// `fugaro graph` (CG3 of docs/cli-output-console-graph/implementation-plan-graph.md):
// the command the console spawns, and what these tests hold of it.
//
//   - TestGraphRefusesAnInvalidScopeBeforeAnyRead is H1. --source reaches argv
//     from the console's renderer, so every value outside graph.ParseScope's
//     closed set is refused before the project is even selected — no header on
//     stderr, no call to the database, nothing on stdout — and an id nothing
//     here knows is refused, as a fact about the id rather than about a missing
//     reader (CG5), rather than answered with an empty graph. The control at the
//     end keeps the "no read" half from passing vacuously.
//   - TestGraphRefusedOrchestratorReadIsTerminal: the one failure of an
//     orchestrator read that is the command's rather than the scope's — a
//     database that refuses the read, which is exit 1 with no document and
//     never a scope that is down or a cached brief (CG5, H3, T3).
//   - TestGraphReadsTheOrchestratorAndLocalScopes: CG5's scopes through the
//     command — one orchestrator's work items and their `depends_on`, a local
//     scope falling back to this machine's cached brief, and `all` as the union
//     keyed by source (D1356, D1358, §6.22).
//   - TestGraphAllWarnsAboutAnUnreadableCache: a cache file this release cannot
//     read is the warning of a read that still answers for every scope the
//     database holds, and it refuses only the `local:<id>` that asked for that
//     file — never a document-less exit for the console's default scope.
//   - TestGraphAnswersAnOrchestratorWhoseStateIsNotWrittenYet: existence is the
//     question `fugaro orchestrator ls` asks — a key under `orchestrator/` — so
//     an id the database holds with no state node yet is a scope that is down in
//     `all` and named on the command line alike, and never a scope dropped or
//     refused (H3, §6.22).
//   - TestGraphAllIsTheCoordinatorToday: `all` is the union of the scopes that
//     exist, which today is the coordinator alone, and the document names it as
//     itself — `coordinator` is the source half of every key it produced and the
//     value the console sends back (contract §6.22). Its subtest is §6.13's
//     other half: a project the coordinator has never reconciled, drawn from
//     the runs alone and stated as the scope being down.
//   - TestGraphJSONIsOneVersionedDocument: the shape a reader outside this
//     binary consumes, read back strictly.
//   - TestGraphNeedsNoCheckout: the read paints from a directory that is not a
//     checkout, which is where the console's child runs — and its subtest is the
//     one fact it does need from the project config, the provider each
//     repository's slug is keyed by, which a config that leaves it out is
//     refused for by name rather than guessed at.
//   - TestGraphUnreadableStoredViewIsAStateNotARefusal: an answer this build
//     cannot read — a node whose decoding it refuses, or a record whose identity
//     sanitising would rewrite — is the scope's `unreadable` state with a
//     document printed, not a terminal refusal the console would read as "this
//     CLI has no graph".
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
	"runtime"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/ee/graph"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/orchestrator"
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
// It also keeps the directories a command reads and writes out of the
// developer's own home — $XDG_CACHE_HOME (the update-check cache, and the
// project-name and layer caches), $XDG_CONFIG_HOME (checkouts.json, which the
// console's first run reads) and $XDG_STATE_HOME (the run directory a local
// scope looks in for the endpoint a daemon serves; the reader of that file
// removes one no endpoint answers for, so a case here could otherwise take a
// registry file out of the developer's own state directory) — and tells git not
// to find this repository's own fugaro.yaml, so
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
	for _, k := range []string{"XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
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

// writeOrchestratorState leaves one orchestrator's state node in the fixture's
// database: two work items, the second waiting on the first, written through
// the daemon's own encoder so the reader decodes what a daemon writes.
func (f *graphFixture) writeOrchestratorState(t *testing.T, id, first, second string) {
	t.Helper()
	item := func(itemID, title string, deps ...string) orchestrator.WorkItem {
		return orchestrator.WorkItem{
			ID: itemID, Title: title, Repo: "acme/app",
			Recipe: "standard", RigorTier: "medium",
			Brief: "The brief of " + itemID + ".", DependsOn: deps, Phase: orchestrator.PhaseQueued,
		}
	}
	raw, err := orchestrator.EncodeState(orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       id, Role: orchestrator.RoleOrchestrator,
		WorkItems: []orchestrator.WorkItem{item("wi-1", first), item("wi-2", second, "wi-1")},
	})
	if err != nil {
		t.Fatalf("the fixture state is not one this release writes: %v", err)
	}
	path, err := orchestrator.PathState(id)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(path, json.RawMessage(raw))
}

// writeOrchestratorLease leaves one orchestrator's lease node in the fixture's
// database and no state node with it: the shape a daemon that is beating its
// lease and whose cycles have all failed leaves behind — `orchestrator run`
// writes the lease first and saves a state at the end of a successful cycle — so
// the id is one `fugaro orchestrator ls` lists with nothing to draw for it.
func (f *graphFixture) writeOrchestratorLease(t *testing.T, id, holder string, at time.Time) {
	t.Helper()
	path, err := orchestrator.PathLease(id)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(path, map[string]any{"holder": holder, "at": at.UTC().Format(time.RFC3339)})
}

// writeCachedBrief leaves one cached brief where the daemon writes it, placed
// through the writer's own path builder so the test cannot disagree with the
// reader about the file's name or its directory (D1243).
func (f *graphFixture) writeCachedBrief(t *testing.T, id string, at time.Time) {
	t.Helper()
	path, err := orchestrator.BriefCachePath(os.Getenv, runtime.GOOS, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(orchestrator.BriefCache{
		Version: orchestrator.BriefCacheVersion, Brief: "the cached brief of " + id + ".", WrittenAt: at.UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeUnreadableBrief leaves a file where the daemon writes a cached brief,
// holding a document this release does not read — a brief another release's
// daemon wrote, which is what a machine that has upgraded the CLI around a
// daemon it did not restart has. It answers the path it wrote, so a test can
// name the file in the sentence the read gives about it.
func (f *graphFixture) writeUnreadableBrief(t *testing.T, id string) string {
	t.Helper()
	path, err := orchestrator.BriefCachePath(os.Getenv, runtime.GOOS, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(orchestrator.BriefCache{
		Version: orchestrator.BriefCacheVersion + 1, Brief: "a brief another release wrote.", WrittenAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
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

	// The other refusal is an id nothing here knows: the closed set holds the
	// value — CG5 registers both readers — and the scope is refused because it
	// does not exist, naming itself and where the ids that do are listed, rather
	// than answered with an empty document a screen could not tell from a scope
	// with no work in it (§6.22).
	for _, absent := range []string{"orchestrator:o1", "local:o1"} {
		t.Run(absent, func(t *testing.T) {
			if _, err := graph.ParseScope(absent); err != nil {
				t.Fatalf("graph.ParseScope(%q) refused a scope the closed set holds: %v", absent, err)
			}
			before := len(f.db.Requests())
			out, errOut, err := execute(t, "graph", "--source", absent, "--json")
			if err == nil {
				t.Fatalf("graph --source %q was answered rather than refused: nothing here holds an orchestrator o1", absent)
			}
			for _, want := range []string{absent, "fugaro orchestrator ls"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not name %s: an id that is not there is told from one that is", err, want)
				}
			}
			if out != "" {
				t.Errorf("the refused scope wrote %q to stdout, want no document at all", out)
			}
			// The project is selected before the scope is read — the refusal is
			// about the id that is not there, not about a reader this build lacks
			// — so the header is on stderr and the database was asked.
			if errOut == "" {
				t.Error("the refused scope wrote nothing to stderr: the scope was refused before the project was selected")
			}
			if got := len(f.db.Requests()); got == before {
				t.Errorf("graph --source %q read nothing, so the refusal is not about the id: this build has no reader for it", absent)
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

// writeRoadmap leaves the project's roadmap record in the fixture's database,
// written the way the record's own writer leaves it: one node per document under
// the four collections, keyed by the record's own document ids.
func (f *graphFixture) writeRoadmap(t *testing.T, plans []coordinator.Plan, releases []coordinator.Release, assigns []coordinator.Assignment, edges []coordinator.Edge) {
	t.Helper()
	node := map[string]any{"plans": map[string]any{}, "releases": map[string]any{}, "assign": map[string]any{}, "edges": map[string]any{}}
	for _, p := range plans {
		node["plans"].(map[string]any)[coordinator.PlanID(p.Plan)] = p
	}
	for _, r := range releases {
		node["releases"].(map[string]any)[coordinator.ReleaseID(r.Stream, r.ID)] = r
	}
	for _, a := range assigns {
		node["assign"].(map[string]any)[coordinator.AssignID(a.Task)] = a
	}
	for _, e := range edges {
		node["edges"].(map[string]any)[coordinator.EdgeID(e.Task, e.Needs)] = e
	}
	f.db.Set(coordinator.PathRoadmap(), node)
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

// TestGraphReadsTheOrchestratorAndLocalScopes: CG5's scopes, through the
// command. The red step this test replaces is the refusal every build before it
// gave — "no orchestrator is installed in this build" — and what is held here
// is the three things that refusal was standing in for: `orchestrator:<id>`
// reads that id's state node and draws its work items and their `depends_on`,
// `local:<id>` reads the same node and falls back to this machine's cached brief
// when there is none, and `all` is the union of the scopes that exist, keyed by
// source so two orchestrators can each carry a `wi-1` (D1356, §6.22).
func TestGraphReadsTheOrchestratorAndLocalScopes(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	f.writeOrchestratorState(t, "o1", "land the state migration", "move the sweep onto the daemon")
	written := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	f.writeCachedBrief(t, "o2", written)

	// One orchestrator: its scope, its work items, and the one edge the
	// coordinator's view has none of (F17, F18).
	one := graphDocument(t, "graph", "--source", "orchestrator:o1", "--json")
	if len(one.Sources) != 1 || one.Sources[0].Source != "orchestrator:o1" || one.Sources[0].State != graph.SourceUp {
		t.Fatalf("the orchestrator scope is %+v, want orchestrator:o1 up", one.Sources)
	}
	if one.Sources[0].AsOf != nil {
		t.Errorf("the scope carries as_of %v, want null: the state node has no time of its own", one.Sources[0].AsOf)
	}
	if len(one.Nodes) != 2 {
		t.Fatalf("the scope drew %d nodes, want the node's two work items: %+v", len(one.Nodes), one.Nodes)
	}
	if got := one.Nodes[0]; got.Kind != graph.NodeTask || got.Key != graph.NodeKey("orchestrator:o1", "wi-1") || got.State != "queued" {
		t.Errorf("the first work item is %+v, want a queued task keyed by its source", got)
	}
	want := graph.Edge{From: graph.NodeKey("orchestrator:o1", "wi-2"), To: graph.NodeKey("orchestrator:o1", "wi-1"), Kind: graph.DependsOnEdge}
	if len(one.Edges) != 1 || one.Edges[0] != want {
		t.Errorf("the scope's edges are %+v, want the one dependency %+v", one.Edges, want)
	}

	// `all`: every scope that exists — the stored view, the orchestrator the
	// database holds, and the local one this machine holds a cached brief for
	// (CG4's union, D1358) — each node keyed by the source it came from.
	all := graphDocument(t, "graph", "--json")
	if len(all.Sources) != 3 || all.Sources[0].Source != "coordinator" ||
		all.Sources[1].Source != "orchestrator:o1" || all.Sources[2].Source != "local:o2" {
		t.Fatalf("`all` covers %+v, want the coordinator, orchestrator:o1 and local:o2", all.Sources)
	}
	// Under `all` a local id with no node is a scope that is down, not a
	// refusal: the id is one this installation has, and only a scope named on
	// the command line is refused for not existing (H3).
	if all.Sources[2].State != graph.SourceDown {
		t.Errorf("`all` lists local:o2 as %+v, want it down with the cached brief's stamp", all.Sources[2])
	}
	if len(all.Nodes) != 3 {
		t.Fatalf("`all` drew %d nodes, want the stored record's one and the two work items: %+v", len(all.Nodes), all.Nodes)
	}
	seen := map[string]bool{}
	for _, n := range all.Nodes {
		if seen[n.Key] {
			t.Errorf("`all` carries the key %q twice", n.Key)
		}
		seen[n.Key] = true
		if n.Key != graph.NodeKey(n.Source, n.ID) {
			t.Errorf("the node %+v is not keyed by the source it came from", n)
		}
	}
	if len(all.Edges) != 1 {
		t.Errorf("`all`'s edges are %+v, want the orchestrator's one dependency", all.Edges)
	}

	// A local scope with a live node reads the node: the cache is only what it
	// has when the node is not there (D1358).
	local := graphDocument(t, "graph", "--source", "local:o1", "--json")
	if local.Sources[0].State != graph.SourceUp || len(local.Nodes) != 2 {
		t.Errorf("a local scope whose node is there is %+v with %d nodes, want it up with the node's items", local.Sources[0], len(local.Nodes))
	}

	// A local scope with no node falls back to the cached brief: down, with the
	// cache's own stamp as the age and no nodes, because a brief is not a graph
	// (T5(a), D1243).
	fallback := graphDocument(t, "graph", "--source", "local:o2", "--json")
	if len(fallback.Sources) != 1 || fallback.Sources[0].State != graph.SourceDown {
		t.Fatalf("the local scope is %+v, want it down", fallback.Sources)
	}
	if got := fallback.Sources[0].AsOf; got == nil || !got.Equal(written) {
		t.Errorf("the fallback's as_of is %v, want the cached brief's own stamp %s", got, written)
	}
	if !strings.Contains(fallback.Sources[0].Reason, written.Format(time.RFC3339)) {
		t.Errorf("the reason is %q, want it to name when the cached brief was written", fallback.Sources[0].Reason)
	}
	if len(fallback.Nodes) != 0 || len(fallback.Edges) != 0 {
		t.Errorf("the fallback drew %d nodes and %d edges, want none", len(fallback.Nodes), len(fallback.Edges))
	}

	// A local scope with neither is the refusal, not a fourth kind of state: a
	// cached brief for another id is not this id's.
	if _, _, err := execute(t, "graph", "--source", "local:o3", "--json"); err == nil {
		t.Error("a local scope with no node and no cached brief was answered rather than refused")
	} else if !strings.Contains(err.Error(), "fugaro orchestrator ls") {
		t.Errorf("the refusal is %q, want it to say where the ids this installation has are listed", err)
	}

	// And the cache is the local scope's fallback alone: the same id asked for
	// as an orchestrator scope is the project's node read through the caller's
	// credential, so a brief this machine happens to hold beside it does not
	// answer that scope (D1358).
	if _, _, err := execute(t, "graph", "--source", "orchestrator:o2", "--json"); err == nil {
		t.Error("an orchestrator scope was answered from this machine's cached brief, want the refusal: only a local scope falls back to the cache")
	} else if !strings.Contains(err.Error(), "fugaro orchestrator ls") {
		t.Errorf("the refusal is %q, want it to say where the ids this installation has are listed", err)
	}
}

// TestGraphAnswersAnOrchestratorWhoseStateIsNotWrittenYet: `all` enumerates ids
// the way `fugaro orchestrator ls` does — the keys under `orchestrator/`, with no
// values — and `orchestrator run` writes the lease node before the cycle that
// saves a state, so an installation can list an id whose state node is not there
// at all. That id is an orchestrator this installation has, and `all` is one
// scope per orchestrator the database holds (CG4's union, §6.22), so its scope is
// down: dropping it silently would draw a graph that disagrees with the listing
// the console got its ids from, and refusing would take the whole document away
// from a machine with one half-started daemon (H3, T3).
//
// The same id named by hand is asked the same question — does the database hold
// it — and answers with the same scope, while an id no key is under is still the
// refusal. The `local:` kind is the third answer and is its own: it names a daemon
// on this machine, a cached brief is what says this machine runs one, and a scope
// that asked for that daemon and has neither a brief nor a node is refused over
// those and not over the database's listing (§6.22).
func TestGraphAnswersAnOrchestratorWhoseStateIsNotWrittenYet(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	f.writeOrchestratorLease(t, "o1", "run-1", time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC))

	all := graphDocument(t, "graph", "--json")
	if len(all.Sources) != 2 || all.Sources[0].Source != "coordinator" || all.Sources[1].Source != "orchestrator:o1" {
		t.Fatalf("`all` covers %+v, want the coordinator and the orchestrator the database lists", all.Sources)
	}
	if got := all.Sources[1]; got.State != graph.SourceDown || got.AsOf != nil {
		t.Errorf("the orchestrator's scope is %+v, want it down and claiming no age: nothing has written a state", got)
	}
	if !strings.Contains(all.Sources[1].Reason, "state node is not written yet") {
		t.Errorf("the reason is %q, want it to say the state node is not written", all.Sources[1].Reason)
	}
	if len(all.Nodes) != 1 {
		t.Errorf("`all` drew %d nodes, want the stored record's one: an id with no state node has nothing to draw", len(all.Nodes))
	}
	if len(all.Warnings) != 0 {
		t.Errorf("the document's warnings are %q, want none: nothing was left out of it", all.Warnings)
	}

	// Named by hand: the database is asked whether it holds the id at all, which
	// is the read `ls` makes, and it does.
	one := graphDocument(t, "graph", "--source", "orchestrator:o1", "--json")
	if len(one.Sources) != 1 || one.Sources[0].State != graph.SourceDown {
		t.Fatalf("the named scope is %+v, want orchestrator:o1 down", one.Sources)
	}

	// The control, and the half that keeps the two apart: an id no key is under
	// is refused, not answered with a third kind of down, because a screen has to
	// tell "nothing here" from "not asked" (§6.22).
	if _, _, err := execute(t, "graph", "--source", "orchestrator:o2", "--json"); err == nil {
		t.Error("an id the database does not hold was answered with a scope, want the refusal")
	} else if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("the refusal is %v, want the user error the console reads", err)
	}

	// And the local kind of the same id: refused, because a brief on this machine
	// is what makes an orchestrator local, and there is none — the database's
	// listing does not stand in for it (D1358).
	if _, _, err := execute(t, "graph", "--source", "local:o1", "--json"); err == nil {
		t.Error("a local scope with no cached brief was answered, want the refusal")
	} else if !strings.Contains(err.Error(), "cached brief") {
		t.Errorf("the refusal is %q, want it to say this machine holds no cached brief for the id", err)
	}

	// The state node is what the scope is about: written, the same id reads up.
	f.writeOrchestratorState(t, "o1", "land the state migration", "move the sweep onto the daemon")
	live := graphDocument(t, "graph", "--source", "orchestrator:o1", "--json")
	if len(live.Sources) != 1 || live.Sources[0].State != graph.SourceUp || len(live.Nodes) != 2 {
		t.Errorf("the scope is %+v with %d nodes, want it up with the node's two work items", live.Sources, len(live.Nodes))
	}
}

// TestGraphAllWarnsAboutAnUnreadableCache: the ids this machine runs are one of
// the two ways `all` names the scopes that exist, and a cache file this release
// cannot read — a daemon of another release wrote it, which is what a machine
// whose CLI was upgraded around a daemon that was not has — takes that way away
// without taking the read away with it. `all` is the console's default read, so
// the scopes the database answers for are still in the document, the ids only
// the cache knew about are left out, and the sentence saying so is the
// document's warning (H3, T3).
//
// A scope that *asks* for that file is the other thing, and it is still refused:
// `local:<id>` is a request for the brief at that id, and a caller that asked
// for one scope and got a document without it could not tell that from an
// orchestrator this installation does not have (§6.22, cachedBriefs).
func TestGraphAllWarnsAboutAnUnreadableCache(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	f.writeOrchestratorState(t, "o1", "land the state migration", "move the sweep onto the daemon")
	path := f.writeUnreadableBrief(t, "o9")

	doc := graphDocument(t, "graph", "--json")
	if len(doc.Sources) != 2 || doc.Sources[0].Source != "coordinator" || doc.Sources[1].Source != "orchestrator:o1" {
		t.Fatalf("`all` covers %+v, want the coordinator and the orchestrator the database holds", doc.Sources)
	}
	if len(doc.Nodes) != 3 {
		t.Errorf("`all` drew %d nodes, want the stored record's one and the node's two work items", len(doc.Nodes))
	}
	for _, src := range doc.Sources {
		if strings.HasPrefix(src.Source, "local:") {
			t.Errorf("`all` lists %q, want no local scope: the cache one would have come from cannot be read", src.Source)
		}
	}
	if len(doc.Warnings) != 1 {
		t.Fatalf("the document's warnings are %q, want the one sentence about the cache", doc.Warnings)
	}
	if !strings.Contains(doc.Warnings[0], filepath.Base(path)) || !strings.Contains(doc.Warnings[0], "left out") {
		t.Errorf("the warning is %q, want it to name %s and say what was left out for it", doc.Warnings[0], filepath.Base(path))
	}

	// The table is the same read with the warnings on stderr, which is where a
	// person reading it is told what the read could not do.
	out, errOut, err := execute(t, "graph")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	if !strings.Contains(out, "coordinator") {
		t.Errorf("the table is\n%s\nwant the coordinator scope", out)
	}
	if !strings.Contains(errOut, filepath.Base(path)) {
		t.Errorf("stderr is %q, want the warning naming %s", errOut, filepath.Base(path))
	}

	// The one scope that asked for that file: refused, with no document at all.
	out, _, err = execute(t, "graph", "--source", "local:o9", "--json")
	if err == nil {
		t.Fatalf("graph --source local:o9 answered for a cached brief this release cannot read")
	}
	if got := fcli.ExitCode(err); got != fcli.ExitUserError {
		t.Errorf("the refusal exits %d, want %d: it is a file this machine cannot read, not a database that failed", got, fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), filepath.Base(path)) {
		t.Errorf("the refusal is %q, want it to name the file it is about", err)
	}
	if out != "" {
		t.Errorf("the refused scope wrote %q to stdout, want no document at all", out)
	}
}

// TestGraphRefusedOrchestratorReadIsTerminal: a read the database *refuses* is
// the command's and not the scope's, and the orchestrator scopes are the ones
// that can get it wrong. A read that failed is a scope that is down (H3); a read
// this CLI may not make at all is exit 1 with **no document**, which is what a
// console reads as "this CLI has no graph" rather than as a graph that is down
// (T3, §6.22, `graphSources`' classification).
//
// Both kinds are held, because the local one is where the fallback is: a cached
// brief is what a local scope draws with when the node cannot be read, and it is
// never what it draws with when the read was refused — there is no failed read
// here to answer, and putting a brief beside a refusal would turn "you may not
// read this" into a scope that is merely down.
func TestGraphRefusedOrchestratorReadIsTerminal(t *testing.T) {
	written := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	state, err := orchestrator.PathState("o1")
	if err != nil {
		t.Fatal(err)
	}

	for _, src := range []string{"orchestrator:o1", "local:o1"} {
		t.Run(src, func(t *testing.T) {
			f := newGraphFixture(t)
			f.writeStoredView(t, storedView())
			// A cached brief the local kind could fall back to, and must not:
			// the brief is the answer to a read that failed, and this is a read
			// that was refused (D1358, H3).
			f.writeCachedBrief(t, "o1", written)
			f.db.DenyRead(state)

			before := len(f.db.Requests())
			out, _, err := execute(t, "graph", "--source", src, "--json")
			if err == nil {
				t.Fatalf("graph --source %s answered with a document for a node this CLI may not read", src)
			}
			if got := fcli.ExitCode(err); got != fcli.ExitUserError {
				t.Errorf("the refusal exits %d, want %d: a read the database refuses is the caller's, not a remote failure", got, fcli.ExitUserError)
			}
			for _, want := range []string{"refused the read", "Viewer role"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal is %q, want it to carry %q: the sentence says what fixes it", err, want)
				}
			}
			if out != "" {
				t.Errorf("the refused scope wrote %q to stdout, want no document at all: a refusal is not a scope that is down", out)
			}
			if got := len(f.db.Requests()); got == before {
				t.Errorf("graph --source %s read nothing: the refusal is not about this node", src)
			}
		})
	}
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
//
// Its subtest is the one fact the read does take from the project config: the
// provider each repository's slug is keyed by. A config that leaves it out has
// no provider to find here, so the read refuses it by name rather than guessing
// one, and "needs no checkout" is a promise about the config onboarding writes
// writes (every repository's provider in it) and not about this one.
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

	t.Run("a repository whose repos entry leaves the provider out", func(t *testing.T) {
		f := newGraphFixture(t)
		path := f.writeConfig(t, "  acme/app: { base_branch: main, workflows: [web] }\n")
		out, _, err := execute(t, "graph", "--config", path, "--json")
		if err == nil {
			t.Fatalf("a repository whose config names no provider was graphed: %s", out)
		}
		for _, want := range []string{"cannot tell the git provider of acme/app", "repos entry"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal is %q, want it to say %q: the provider is the one fact the read needs and cannot guess", err, want)
			}
		}
		if out != "" {
			t.Errorf("the refused read printed %q, want no document at all", out)
		}
	})
}

// TestGraphUnreadableStoredViewIsAStateNotARefusal: an answer the database gave
// and this build cannot read is the contract's `unreadable` — a state of the
// scope, with the document printed and the process succeeding (§6.22, H3) — and
// never a refusal of the whole read, which is what the console reads as "this
// CLI has no graph" (T3). Both shapes of that class are here: a stored node
// whose decoding this build refuses, and a record whose identity sanitising
// would rewrite. What they are told apart from is the other failure of the same
// read: a database that could not be reached, or a repository whose config
// names no provider, stays a terminal error with no document.
func TestGraphUnreadableStoredViewIsAStateNotARefusal(t *testing.T) {
	f := newGraphFixture(t)
	// What a sweep of a later build leaves behind: a view carrying a field this
	// build does not know, which the read refuses rather than dropping, because
	// a document read with a field missing is not the document that was written.
	f.db.Set(coordinator.PathView(appSlug), map[string]any{
		"repo": "acme/app", "records": []any{}, "collisions": []any{}, "a_field_from_a_later_sweep": true,
	})
	out, errOut, err := execute(t, "graph", "--json")
	if err != nil {
		t.Fatalf("a stored node this build cannot read refused the whole read: %v (stderr: %s)", err, errOut)
	}
	doc, err := graph.ParseDocument([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sources) != 1 || doc.Sources[0].State != graph.SourceUnreadable {
		t.Fatalf("the scope of a read that answered with something unreadable is %+v, want one source in the state %q", doc.Sources, graph.SourceUnreadable)
	}
	if got := doc.Sources[0].Source; got != "coordinator" {
		t.Errorf("the unreadable scope names itself %q, want coordinator: the source half of every key it would have produced", got)
	}
	if doc.Sources[0].Reason == "" {
		t.Error("the unreadable scope carries no reason: a screen has nothing to show beside the state")
	}
	if doc.Sources[0].AsOf != nil {
		t.Errorf("as_of is %v, want null: nothing here says when the data was last good", doc.Sources[0].AsOf)
	}
	if len(doc.Nodes) != 0 {
		t.Errorf("the unreadable scope drew %d nodes, want none: what it answered with is not something this build can read", len(doc.Nodes))
	}

	// The text output says the same thing to a person, who has no document to
	// read the state out of.
	text, errOut, err := execute(t, "graph")
	if err != nil {
		t.Fatalf("the table refused the read the document answered: %v (stderr: %s)", err, errOut)
	}
	if !strings.Contains(text, "coordinator: "+graph.SourceUnreadable) {
		t.Errorf("the table does not say the scope is %s:\n%s", graph.SourceUnreadable, text)
	}

	// The other shape of the same class: the node decodes, and its identity is
	// one this build cannot carry. The run a run-only record is keyed by holds a
	// bidi override, which safetext.Field would drop — so a key rebuilt from the
	// sanitised id could be a key another record already has, which the source
	// refuses rather than printing (D1356, Sanitise).
	t.Run("a record whose identity this build refuses", func(t *testing.T) {
		f := newGraphFixture(t)
		f.writeStoredView(t, coordinator.View{
			Repo: "acme/app",
			Records: []coordinator.Record{{
				Runs: []coordinator.Run{{
					ID: "20261013-100000-a\u202eaaa", Status: "running", Branch: "release/v0.11.0",
					DeclaredBy: "someone@example.com", Files: []string{},
				}},
				Branch: "release/v0.11.0", Release: "0.11.0", InFlight: true,
				Files: []string{}, Services: []string{},
			}},
			Collisions: []coordinator.Collision{},
		})
		out, errOut, err := execute(t, "graph", "--json")
		if err != nil {
			t.Fatalf("a record whose identity this build refuses refused the whole read: %v (stderr: %s)", err, errOut)
		}
		doc, err := graph.ParseDocument([]byte(out))
		if err != nil {
			t.Fatal(err)
		}
		if len(doc.Sources) != 1 || doc.Sources[0].State != graph.SourceUnreadable {
			t.Fatalf("the scope of an unkeyable record is %+v, want one source in the state %q", doc.Sources, graph.SourceUnreadable)
		}
		if doc.Sources[0].Reason == "" {
			t.Error("the unreadable scope carries no reason: a screen has nothing to show beside the state")
		}
		if len(doc.Nodes) != 0 {
			t.Errorf("the scope drew %d nodes, want none: a key this build cannot build is not one it can print", len(doc.Nodes))
		}
	})
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

// TestGraphDrawsTheRoadmapRecord is GV4.1 through the command: the project's
// roadmap record reaches the document the console draws — its release boxes, its
// tasks with their qualified ids and their plan's own version, and its
// cross-plan edges, which a document drawn from the stored view alone has none of
// (D2112, F17).
//
// The record's own read is the scope's own: no flag asks for it (the record is
// project-level, D2111), and a scope named on the command line is the same
// document, because the record is drawn wherever the coordinator's scope is.
func TestGraphDrawsTheRoadmapRecord(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	target := "1.6.0"
	cli := coordinator.Release{Schema: coordinator.RoadmapSchema, ID: "v1.5.0", Stream: "cli",
		Name: "CLI 1.5", State: coordinator.ReleaseOpen, Order: 2}
	f.writeRoadmap(t,
		[]coordinator.Plan{
			{Schema: coordinator.RoadmapSchema, Plan: "plans/cli", Stream: "cli", Tasks: []coordinator.Task{
				{ID: "place", Title: "the box a task is placed in", Version: &target},
				{ID: "free", Title: "a task nobody has placed", Version: &target},
			}},
			{Schema: coordinator.RoadmapSchema, Plan: "plans/style", Stream: "style", Tasks: []coordinator.Task{
				{ID: "paint", Title: "a task in the other stream"},
			}},
		},
		[]coordinator.Release{cli},
		[]coordinator.Assignment{{Schema: coordinator.RoadmapSchema, Task: "plans/cli/place",
			Release: coordinator.ReleaseID(cli.Stream, cli.ID)}},
		[]coordinator.Edge{{Schema: coordinator.RoadmapSchema, Task: "plans/style/paint",
			Needs: "plans/cli/place", Reason: "the palette"}},
	)

	doc := graphDocument(t, "graph", "--json")
	if len(doc.Releases) != 1 {
		t.Fatalf("the document carries %d release boxes, want the record's one: %+v", len(doc.Releases), doc.Releases)
	}
	box := doc.Releases[0]
	if box.Key != coordinator.ReleaseID("cli", "v1.5.0") || box.Stream != "cli" || box.Name != "CLI 1.5" ||
		box.State != coordinator.ReleaseOpen || box.Order != 2 {
		t.Fatalf("the box is %+v, want the record's own release", box)
	}

	byID := map[string]graph.Node{}
	for _, n := range doc.Nodes {
		byID[n.ID] = n
	}
	placed, ok := byID["plans/cli/place"]
	if !ok {
		t.Fatalf("the record's tasks are not in the document: %+v", doc.Nodes)
	}
	if placed.Kind != graph.NodeTask || placed.Plan != "plans/cli" || placed.Stream != "cli" || placed.Release != box.Key {
		t.Errorf("the placed task is %+v, want a task node in its box", placed)
	}
	if placed.Key != graph.NodeKey("coordinator", "plans/cli/place") {
		t.Errorf("the task's key is %q, want the qualified id under the coordinator's source", placed.Key)
	}
	free := byID["plans/cli/free"]
	if free.Release != "" || free.Stream != "cli" || free.Version != "1.6.0" {
		t.Errorf("an unplaced task is %+v, want its plan's lane and its plan's version (D2114)", free)
	}

	if len(doc.Edges) != 1 {
		t.Fatalf("the document carries %d edges, want the record's cross-plan one: %+v", len(doc.Edges), doc.Edges)
	}
	want := graph.Edge{
		From: graph.NodeKey("coordinator", "plans/style/paint"),
		To:   graph.NodeKey("coordinator", "plans/cli/place"),
		Kind: graph.DependsOnEdge,
	}
	if doc.Edges[0] != want {
		t.Errorf("the edge is %+v, want %+v", doc.Edges[0], want)
	}

	// The stored view is still drawn beside the record: the record is the
	// coordinator's own half and not a replacement for the sweep's.
	if _, ok := byID["acme/app#12"]; !ok {
		t.Errorf("the stored view's record is gone from the document: %+v", doc.Nodes)
	}
	if len(doc.Warnings) != 0 {
		t.Errorf("a record this build reads whole warns: %v", doc.Warnings)
	}
}

// TestGraphTextNamesTheRecordsBoxes: the table's group headings are the
// release's own words. A task of the roadmap record names its release by a
// **key**, and `coordinator.ReleaseID` folds two strings into a hash, so a
// heading read off the node alone is 64 hex characters where the release's name
// belongs — and the box's state, which is the only state a record task has
// (GV4.1, §6.22), would not be printed at all.
//
// Hand mutation this fails on: heading a group with n.Release, which puts the
// hash in the table and drops `CLI 1.5 (open)` from it.
func TestGraphTextNamesTheRecordsBoxes(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	target := "1.6.0"
	cli := coordinator.Release{Schema: coordinator.RoadmapSchema, ID: "v1.5.0", Stream: "cli",
		Name: "CLI 1.5", State: coordinator.ReleaseOpen, Order: 2}
	key := coordinator.ReleaseID(cli.Stream, cli.ID)
	f.writeRoadmap(t,
		[]coordinator.Plan{{Schema: coordinator.RoadmapSchema, Plan: "plans/cli", Stream: "cli",
			Tasks: []coordinator.Task{{ID: "place", Title: "a task of the record", Version: &target}}}},
		[]coordinator.Release{cli},
		[]coordinator.Assignment{{Schema: coordinator.RoadmapSchema, Task: "plans/cli/place", Release: key}},
		nil,
	)

	out, errOut, err := execute(t, "graph")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	if !strings.Contains(out, "CLI 1.5 (open)") {
		t.Errorf("the table does not head the record's group with the box's name and state:\n%s", out)
	}
	if strings.Contains(out, key) {
		t.Errorf("the table printed the box's key, which is a hash and not a name:\n%s", out)
	}
	if !strings.Contains(out, "plans/cli/place") {
		t.Errorf("the record's task is not in the table:\n%s", out)
	}
	// The stored view's own groups keep the release string its repository wrote:
	// only a task of the record names a key.
	if !strings.Contains(out, "0.11.0") {
		t.Errorf("the stored view's own group is gone from the table:\n%s", out)
	}
}

// TestGraphSaysWhatItCannotReadOfTheRecord: a document of another schema is
// skipped and counted rather than read as empty — "the record holds nothing" and
// "the record holds something this build cannot read" must not look alike
// (§6.22) — and the read is not refused: the rest of the record, and the stored
// view beside it, are still drawn.
func TestGraphSaysWhatItCannotReadOfTheRecord(t *testing.T) {
	f := newGraphFixture(t)
	f.writeStoredView(t, storedView())
	f.writeRoadmap(t,
		[]coordinator.Plan{
			{Schema: coordinator.RoadmapSchema, Plan: "plans/cli", Stream: "cli",
				Tasks: []coordinator.Task{{ID: "place", Title: "a task this build reads"}}},
		},
		[]coordinator.Release{{Schema: coordinator.RoadmapSchema + 1, ID: "v9.0.0", Stream: "cli",
			Name: "a release of a later shape", State: coordinator.ReleaseOpen, Order: 1}},
		nil, nil,
	)

	doc := graphDocument(t, "graph", "--json")
	if len(doc.Releases) != 0 {
		t.Errorf("a document of a later schema was drawn: %+v", doc.Releases)
	}
	if _, ok := nodeByID(doc, "plans/cli/place"); !ok {
		t.Errorf("the record's readable half is not drawn: %+v", doc.Nodes)
	}
	if len(doc.Warnings) != 1 || !strings.Contains(doc.Warnings[0], "cannot read 1 of the roadmap record's documents") {
		t.Errorf("warnings = %v, want the one sentence about the record", doc.Warnings)
	}
	if len(doc.Sources) != 1 || doc.Sources[0].State != graph.SourceUp {
		t.Errorf("sources = %+v, want the scope up: a record this build partly cannot read is not a scope that is down", doc.Sources)
	}
}

// nodeByID is one node of a document by its id, for the tests that read a
// document's nodes by the thing they are.
func nodeByID(doc graph.Document, id string) (graph.Node, bool) {
	for _, n := range doc.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return graph.Node{}, false
}
