// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

// `fugaro orchestrator map` (CG6 of docs/console-fixes/implementation-plan.md):
// the command the console's Coordinator tab spawns, and what these tests hold of
// it.
//
//   - TestMapJSONIsOneVersionedDocument: `--json` prints the document the
//     console reads, read back the way a reader outside this binary does —
//     strictly, and through the same validation a writer applies (D3221) — and
//     its two lists are lists whatever the map holds.
//   - TestMapReadsTheNodesFromTheDatabase: the read behind the document, through
//     the command: one node per orchestrator whose lease is fresh, carrying the
//     principal the driver node names, its open items, its going jobs and the
//     age of its last beat — measured against the database's own clock (D1230).
//     A lease nobody has beaten for longer than its TTL is no node at all.
//   - TestMapTextTableSaysWhatIsAbsent: the human half, where a node's driver and
//     its lease age must read as absent rather than as blank, and a project
//     nobody drives says so instead of printing an empty table (readings 2, 3).
//   - TestMapRefusesAProjectWithNoDatabase: the one refusal before any read, with
//     the sentence that names the operator's command, and exit 1 rather than the
//     exit 2 a failure of the database gets.
//   - TestMapIsASubcommandOfTheCoreFamily: this command is `orchestrator map` and
//     not a command of its own — the family is the core's (CG6) — and it is the
//     name ee/register registers it under (that package's own test pins the
//     registration against the tree the tagged binary builds).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/agentenv"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// TestMain fences this package's tests the way its siblings' do: no credential,
// no metadata server and no network, so a read that tried to reach Google fails
// here rather than hanging; and XDG directories of the test's own, so a command
// that resolved this machine's cache and config would resolve the fixture's and
// never the developer's.
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
	// git stops before it reaches the repository root, so a command that
	// resolves the checkout's project resolves the fixture's and never the
	// project this repository belongs to.
	if wd, err := os.Getwd(); err == nil {
		os.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(filepath.Dir(wd)))
	}
	// A coding agent's session now gets the --json document from every command
	// that has the flag (CO4, D3437), and these tests read the tree as a person
	// does; internal/cli's TestMain clears the same markers, for the same reason.
	for _, k := range agentenv.Markers {
		os.Unsetenv(k)
	}
	os.Exit(m.Run())
}

// mapTestRoot is the tree these tests drive: the released tree with this
// package's command added under the core's `orchestrator` family.
//
// It assembles the tree itself rather than blank-importing ee/register, and the
// reason is that it cannot: ee/register imports this package, so an import of it
// from here is a cycle, and a fixture may not call cli.RegisterSubcommand — only
// ee/register may (E1.5's rule, held by internal/eeboundary).
// ee/register/orchestrator_map_test.go is where the registration itself is
// pinned, against the tree the tagged binary builds.
func mapTestRoot(t *testing.T) *cobra.Command {
	t.Helper()
	root := fcli.NewRootCmd()
	parent, _, err := root.Find([]string{"orchestrator"})
	if err != nil || parent == nil {
		t.Fatalf("the released tree has no orchestrator family: %v", err)
	}
	parent.AddCommand(NewMapCmd())
	return root
}

// execute runs the map command and captures its output.
func execute(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := mapTestRoot(t)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(fcli.NormalizeArgs(args))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// mapDocument runs the command with --json and reads its document back the way
// a reader outside this binary does: strictly, so a field this build does not
// know is an error rather than a key quietly dropped (DecodeMapDoc).
func mapDocument(t *testing.T, args ...string) orchestrator.MapDoc {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"orchestrator", "map", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	doc, err := orchestrator.DecodeMapDoc([]byte(out))
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return doc
}

// mapFixture is the fixture these tests drive: a local config pointing at a fake
// database and a file:// runs bucket, and the project marker every cloud command
// checks.
type mapFixture struct {
	dir string
	db  *gcpfake.RTDB
	run *gcpfake.Run
	// beat is the clock the lease below is measured against: the database
	// answers in its Date header with this, so a node's age is a figure the test
	// states rather than a race with the machine it runs on.
	beat time.Time
}

// mapTTL is how long the fixture's beat is old: inside the lease's TTL, so the
// orchestrator it belongs to is driven, and a whole minute of slop either side
// of it is what a case rewinds to make it stale.
var mapTTL = 10 * time.Second

func newMapFixture(t *testing.T) *mapFixture {
	t.Helper()
	f := &mapFixture{dir: t.TempDir(), db: gcpfake.NewRTDB(t), run: gcpfake.NewRun(t)}
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	runs := filepath.Join(f.dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	xdg := filepath.Join(f.dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(f.dir, "cache"))
	t.Setenv("FUGARO_PROJECT", "")
	t.Setenv("FUGARO_CONFIG", "")
	projects := filepath.Join(xdg, "fugaro", "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	logging := gcpfake.NewLogging(t)
	// The project is aurora and its orchestrator id is the project's own name, so
	// the node this read finds is the one its daemon writes.
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\nbucket_url: file://" + runs +
		"\nuser: someone@example.com\nmax_parallel: 2\n" +
		"endpoints: { run: " + f.run.URL + "/, logging: " + logging.URL + "/, no_auth: true }\n" +
		"budget: { mode: observe, per_run_usd: 5, rtdb_url: " + f.db.URL + " }\n" +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	path := filepath.Join(projects, "aurora.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	marker, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(runs, filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, marker, 0o644); err != nil {
		t.Fatal(err)
	}
	// The database answers in its own clock, which is where every freshness
	// question is measured (D1230): the beat below is this many seconds old.
	f.beat = time.Now().UTC()
	f.setAge(mapTTL)
	return f
}

// setAge makes the fixture's database answer with a clock age past its beat: the
// lease of a node written at f.beat is that old.
func (f *mapFixture) setAge(age time.Duration) {
	at := f.beat.Add(age)
	f.db.SetClock(func() time.Time { return at })
}

// writeConfig rewrites the project's config, for the one case that needs a
// project with no database.
func (f *mapFixture) writeConfig(t *testing.T, cfg string) {
	t.Helper()
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", "aurora.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// noDatabase is the fixture's project with budget.rtdb_url dropped: an
// installation whose coordinator has no database to report to.
func (f *mapFixture) noDatabase(t *testing.T) {
	t.Helper()
	f.writeConfig(t, "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\nbucket_url: file://"+
		filepath.Join(f.dir, "runs")+"\nuser: someone@example.com\nmax_parallel: 2\n"+
		"endpoints: { run: "+f.run.URL+"/, no_auth: true }\n")
}

// state leaves one orchestrator's state node in the fixture's database, written
// through the release's own encoder so the reader decodes what a daemon writes:
// one item being worked, one queued, one done, and the run the first committed.
func (f *mapFixture) state(t *testing.T, id, runID string) {
	t.Helper()
	item := func(itemID string, phase orchestrator.Phase, wRun string) orchestrator.WorkItem {
		w := orchestrator.WorkItem{
			ID: itemID, Title: "the work of " + itemID, Repo: "acme/app",
			Recipe: "standard", RigorTier: "medium",
			Brief: "The brief of " + itemID + ".", Phase: phase, RunID: wRun,
		}
		if wRun != "" {
			w.Attempts = 1
		}
		return w
	}
	running := item("wi-1", orchestrator.PhaseRunning, runID)
	raw, err := orchestrator.EncodeState(orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       id, Role: orchestrator.RoleOrchestrator,
		Goal:      "keep the project's pull requests moving without a person watching",
		WorkItems: []orchestrator.WorkItem{running, item("wi-2", orchestrator.PhaseQueued, ""), item("wi-3", orchestrator.PhaseDone, "")},
		Intents: []orchestrator.Intent{{
			ID: orchestrator.IntentID(running.ID, 1), RunID: runID,
			SpecDigest: orchestrator.SpecDigest(running),
			ApprovalAt: f.beat.Add(-time.Hour), At: f.beat.Add(-time.Hour),
		}},
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

// lease leaves one orchestrator's lease node in the fixture's database, beaten
// at the fixture's own moment.
func (f *mapFixture) lease(t *testing.T, id, holder string) {
	t.Helper()
	path, err := orchestrator.PathLease(id)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(path, map[string]any{"holder": holder, "at": f.beat.Format(time.RFC3339)})
}

// driver leaves one orchestrator's driver node in the fixture's database.
func (f *mapFixture) driver(t *testing.T, id, principal string) {
	t.Helper()
	path, err := orchestrator.PathDriver(id)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(path, map[string]any{"principal": principal, "at": f.beat.Format(time.RFC3339)})
}

func TestMapJSONIsOneVersionedDocument(t *testing.T) {
	f := newMapFixture(t)
	f.state(t, "aurora", "20261122-083000-aaaa")
	f.lease(t, "aurora", "20261122-083000-aaaa")
	f.driver(t, "aurora", "ops@acme.test")

	out, errOut, err := execute(t, "orchestrator", "map", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	// The document the console reads is read back here by the same door a reader
	// outside this binary uses — strict, and validated — so a field this build
	// writes that this build would not read is the failure.
	doc, err := orchestrator.DecodeMapDoc([]byte(out))
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if doc.Version != orchestrator.MapDocVersion || doc.Project != "aurora" {
		t.Errorf("the document is %q at version %d, want aurora at %d", doc.Project, doc.Version, orchestrator.MapDocVersion)
	}
	// One object and not a stream: a reader parses stdout as a whole (the
	// per-change stream is not this command's shape).
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		t.Fatalf("the document is not one object: %s", out)
	}
	if len(doc.Nodes) != 1 || len(doc.Edges) != 0 {
		t.Fatalf("the document is %+v, want one node and no edge: one orchestrator is driven and it is alone", doc)
	}
	// The health says the sweep is off over the absent heartbeat rather than
	// leaving the field to be read as "nothing overlaps" (F23, reading 6).
	if doc.Health.Sweep || doc.Health.Note != orchestrator.MapSweepOffNote {
		t.Errorf("the health is %+v, want the sweep off with its own sentence", doc.Health)
	}

	// The shape a reader ranges over: both lists are lists even when one of them
	// has nothing in it, which is what a nil slice would break (reading 4).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"nodes", "edges"} {
		if string(raw[key]) == "null" {
			t.Errorf("the document's %s is null, want a list: %s", key, out)
		}
	}
}

func TestMapReadsTheNodesFromTheDatabase(t *testing.T) {
	f := newMapFixture(t)
	const runID = "20261122-083000-aaaa"
	f.state(t, "aurora", runID)
	f.lease(t, "aurora", runID)
	f.driver(t, "aurora", "ops@acme.test")

	doc := mapDocument(t)
	if len(doc.Nodes) != 1 {
		t.Fatalf("the map drew %+v, want the one orchestrator whose lease is fresh", doc.Nodes)
	}
	n := doc.Nodes[0]
	if n.ID != "aurora" || n.Name != "" {
		t.Errorf("the node is %q named %q, want the project's own orchestrator", n.ID, n.Name)
	}
	if n.Driver != "ops@acme.test" {
		t.Errorf("the driver is %q, want the principal the driver node names", n.Driver)
	}
	// The item being worked and the queued one are the work left; the done one is
	// not, and one intent has no outcome yet.
	if n.ActiveTasks != 2 || n.RunningJobs != 1 {
		t.Errorf("the node carries %d active tasks and %d running jobs, want 2 and 1", n.ActiveTasks, n.RunningJobs)
	}
	// The age is the database's own clock's, and the two stamps are whole seconds
	// — a lease's beat and the server's Date header alike — so the figure is the
	// fixture's own with a second of rounding either side.
	if n.LeaseAgeSeconds == nil {
		t.Fatal("lease_age_seconds is null, want the age of a beat that carried a stamp")
	}
	if age := *n.LeaseAgeSeconds; age < mapTTL.Seconds()-1 || age > mapTTL.Seconds()+1 {
		t.Errorf("lease_age_seconds = %v, want the %v the database's clock says the beat is old by", age, mapTTL.Seconds())
	}

	// The same read once the beat is past the lease's TTL: the orchestrator is
	// not being driven any more, so it is no node at all — and every document
	// under the id is still there.
	f.setAge(orchestrator.LeaseTTL + time.Minute)
	if doc := mapDocument(t); len(doc.Nodes) != 0 {
		t.Errorf("the map drew %+v, want no node: nobody has beaten the lease for longer than its TTL", doc.Nodes)
	}
}

func TestMapTextTableSaysWhatIsAbsent(t *testing.T) {
	f := newMapFixture(t)
	const runID = "20261122-083000-aaaa"
	f.state(t, "aurora", runID)
	f.lease(t, "aurora", runID)

	// No driver node: the cell says so in a word rather than by being empty, and
	// the sweep line carries the sentence that explains the missing overlap half.
	out, errOut, err := execute(t, "orchestrator", "map")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	for _, want := range []string{"aurora: 1 orchestrators being driven", "aurora", "2 tasks, 1 jobs", "lease 10s", "sweep: off — " + orchestrator.MapSweepOffNote} {
		if !strings.Contains(out, want) {
			t.Errorf("the table is\n%s\nwant it to carry %q", out, want)
		}
	}
	if strings.Contains(out, "ops@acme.test") {
		t.Errorf("the table printed a driver no node holds:\n%s", out)
	}
	// The driver cell is the one place a person tells "the driver node is not
	// readable" from "nobody drives this": the node is on the map either way, and
	// its cell says the first in a word rather than by being blank.
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "aurora") {
			line = l
		}
	}
	if fields := strings.Fields(line); len(fields) == 0 || fields[1] != "-" {
		t.Errorf("the node's line is %q, want its second cell to be the absent-driver mark", line)
	}

	// A project nobody drives prints the fact and not an empty table.
	leasePath, err := orchestrator.PathLease("aurora")
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(leasePath, nil)
	out, _, err = execute(t, "orchestrator", "map")
	if err != nil {
		t.Fatalf("the read of a project nobody drives: %v", err)
	}
	if !strings.Contains(out, "aurora: no orchestrator is being driven now") {
		t.Errorf("the table is\n%s\nwant the sentence that says nobody drives the project", out)
	}
}

func TestMapRefusesAProjectWithNoDatabase(t *testing.T) {
	f := newMapFixture(t)
	f.noDatabase(t)
	out, _, err := execute(t, "orchestrator", "map", "--json")
	if err == nil {
		t.Fatal("the map was read for a project with no database")
	}
	// The refusal names the entry to set and suffix the command to run, and it is
	// this CLI's own refusal (exit 1) rather than the database's failure (exit 2):
	// there is no database to fail.
	if !strings.Contains(err.Error(), "budget.rtdb_url") || !strings.Contains(err.Error(), "fugaro update") {
		t.Errorf("the refusal is %q, want the sentence naming budget.rtdb_url and the command that sets it", err)
	}
	if code := fcli.ExitCode(err); code != fcli.ExitUserError {
		t.Errorf("the refusal exits %d, want %d: nothing was read", code, fcli.ExitUserError)
	}
	if out != "" {
		t.Errorf("the refused read wrote %q to stdout, want nothing", out)
	}
}

func TestMapIsASubcommandOfTheCoreFamily(t *testing.T) {
	// The family is the core's and this command only joins it: the released tree
	// answers `orchestrator` with its own command, and the map is one of its
	// children rather than a name of its own at the root.
	root := fcli.NewRootCmd()
	if _, _, err := root.Find([]string{"map"}); err == nil {
		t.Error("the released tree answers `fugaro map`: the map is a member of the orchestrator family and not a command of its own")
	}
	parent, _, err := root.Find([]string{"orchestrator"})
	if err != nil || parent == nil {
		t.Fatalf("the released tree has no orchestrator family: %v", err)
	}
	for _, c := range parent.Commands() {
		if c.Name() == "map" {
			t.Error("the released tree answers `fugaro orchestrator map` with no ee code: the command is ee/register's to add")
		}
	}

	cmd := NewMapCmd()
	if cmd.Name() != "map" {
		t.Errorf("the command is named %q, want map: it is registered under the name it answers to", cmd.Name())
	}
	if cmd.Flags().Lookup("json") == nil {
		t.Error("the command has no --json flag: it is the shape the console's argv row spells")
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Error("the command accepted an argument: the map is the whole project's and takes none")
	}
}
