package cli

// The facts of the feed contract's section 10 that only a real process can
// show: what a closed stdout does to the child (section 10.4), what SIGTERM
// does to it (section 10.12), and which stream the degraded retry line goes to
// (section 10.8). All three live in the kernel, in cmd/fugaro/main.go's signal
// handler, or behind the exec boundary, so a docs-presence test -- and an
// in-process one -- cannot see them.
//
// They are in internal/cli because the plan's Task 0.2 puts its tests with the
// CLI, and they spawn the real binary through internal/testutil.BuildFugaro,
// the helper internal/e2e and internal/image already use for this. Task 0.2's
// Files line names only the contract document and
// docs_desktop_console_test.go, so this file is outside the plan's inventory:
// it is here because a reviewer asked for behavioural coverage of the facts a
// presence test cannot reach, and it adds no Go dependency and no product
// behaviour.
//
// It names no path under the console app's own directory, on purpose: Task
// 0.3's boundary test carries a closed allow-list of the Go files permitted to
// mention one -- the core's console contract test, which reads the contract as
// bytes, and the coordinator command's moved half of that same check under
// ee/ -- so a mentioner here would fail it. The contract is referred to by its
// section numbers instead.
//
// None of them touches a live installation: the database is
// internal/gcpfake's Realtime Database fake on loopback, the config is a
// file in the test's own temp directory, and the project is a fixture named
// aurora. The belong installation is never named here.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// buildCache hands testutil.BuildFugaro the go build cache the developer's own
// `go build` uses. TestMain points XDG_CACHE_HOME at a fresh temp directory
// (so no test writes the developer's update-check cache), and Go derives
// GOCACHE from it -- without this, the `go build` inside BuildFugaro compiles
// the whole dependency graph into an empty cache and every run of this
// package pays about fifty seconds for it.
func buildCache(t *testing.T) {
	t.Helper()
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "XDG_CACHE_HOME=") || strings.HasPrefix(kv, "GOCACHE=") {
			continue
		}
		env = append(env, kv)
	}
	cmd := exec.Command("go", "env", "GOCACHE")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return // no answer: build into the temp cache, as before
	}
	if dir := strings.TrimSpace(string(out)); dir != "" {
		t.Setenv("GOCACHE", dir)
	}
}

// spawnWatch starts the real binary on `watch --json --config cfg` in dir,
// writing its stdout to out (a pipe for the tests that read documents) and
// keeping its stderr in a buffer for failure messages. A child process cannot
// see the test process's t.Setenv, so the fixture's config is written to a
// file the child is pointed at.
func spawnWatch(t *testing.T, dir, cfg string, out io.Writer, extra ...string) (*exec.Cmd, *lockedBuf) {
	t.Helper()
	buildCache(t)
	bin := testutil.BuildFugaro(t)
	cfgPath := filepath.Join(dir, "aurora.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"watch", "--json", "--config", cfgPath}, extra...)
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	// The child's own update-check cache, never the developer's.
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+filepath.Join(dir, "cache"))
	errOut := &lockedBuf{}
	cmd.Stdout, cmd.Stderr = out, errOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd, errOut
}

// watchChild spawns the real fugaro binary on `watch --json` against f and
// returns the running command, the read end of its stdout pipe, and its
// stderr. The config is the live one: a budget backend on f, and a runs bucket
// whose project marker names the fixture.
func watchChild(t *testing.T, f *gcpfake.RTDB) (*exec.Cmd, *os.File, *lockedBuf) {
	t.Helper()
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	// The installation names its project in the runs bucket, as its Terraform
	// does: watch's queued-run lookup checks it before trusting the bucket.
	marker := filepath.Join(runs, filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\n" +
		"bucket_url: file://" + runs + "\nuser: someone@example.com\nmax_parallel: 2\n" +
		"budget: { mode: observe, per_run_usd: 5, rtdb_url: " + f.URL + " }\n" +
		"endpoints: { no_auth: true }\n" +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	f.Set("fugaro/project", "aurora")
	f.Set(budget.PathSpendGlobal(today()), map[string]any{"counted": 1000, "spent": 0, "notional": 0})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd, errOut := spawnWatch(t, dir, cfg, w)
	w.Close() // the child holds the only write end
	t.Cleanup(func() { _ = r.Close() })
	return cmd, r, errOut
}

// childDocument reads one line of the child's stdout and decodes it.
func childDocument(t *testing.T, r *os.File) map[string]any {
	t.Helper()
	type read struct {
		line string
		err  error
	}
	got := make(chan read, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		got <- read{line, err}
	}()
	select {
	case res := <-got:
		if res.err != nil {
			t.Fatalf("reading the child's first document: %v", res.err)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(res.line), &doc); err != nil {
			t.Fatalf("the child's first line is not a JSON document: %q (%v)", res.line, err)
		}
		return doc
	case <-time.After(30 * time.Second):
		t.Fatal("the child wrote no document within 30 s")
		return nil
	}
}

// waitChild waits for cmd to end, failing if it is still running d later.
func waitChild(t *testing.T, cmd *exec.Cmd, d time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("the child was still running %s later", d)
	}
}

// TestWatchChildExitsZeroOnSIGTERM pins section 10.12: cmd/fugaro/main.go's
// signalContext cancels the command's context on SIGINT, SIGTERM and SIGHUP,
// watchStream returns nil on that cancellation, and main only calls os.Exit
// on a non-nil error -- so the child exits 0, by an exit code and not by a
// signal. That is what the console's shutdown is built on: close the pipe,
// send SIGTERM, expect 0.
//
// Mutation (run, restore): make signalContext drop syscall.SIGTERM from the
// signal.NotifyContext list, and the child is killed by the signal instead
// (WaitStatus.Signaled(), no exit code), which is what this test fails on.
func TestWatchChildExitsZeroOnSIGTERM(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	cmd, r, errOut := watchChild(t, f)
	defer r.Close()
	doc := childDocument(t, r)
	conn, _ := doc["connection"].(map[string]any)
	if conn == nil || conn["state"] != "live" {
		t.Fatalf("the child's first document is not a live feed: %v", doc)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitChild(t, cmd, 20*time.Second)
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || ws.Signaled() {
		t.Fatalf("SIGTERM ended the child as %v: it died by a signal instead of exiting", cmd.ProcessState)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("SIGTERM: exit %d, want 0 (stderr %q)", code, errOut.String())
	}
}

// TestWatchChildDiesBySIGPIPEWhenItsReaderGoesAway pins section 10.4: a write
// to a stdout whose reader is gone raises SIGPIPE, and Go's runtime does not
// ignore it for descriptors 1 and 2, so the child dies by that signal -- the
// parent sees a signal, never the shell's 141. It also pins the sentence the
// console's shutdown is *not* allowed to rely on: the feed is idle before the
// change below, and an idle feed writes nothing, so closing the pipe alone
// would have left the child running.
//
// Mutation (run, restore): add signal.Ignore(syscall.SIGPIPE) to main, and
// the child survives the write (the test times out waiting for it to end).
func TestWatchChildDiesBySIGPIPEWhenItsReaderGoesAway(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	cmd, r, errOut := watchChild(t, f)
	childDocument(t, r) // the feed is live and has written
	r.Close()           // the console goes away: the pipe has no reader

	// The feed is idle, so nothing would be written; a change makes it write.
	// (gcpfake's Set seeds the tree without broadcasting, so the change goes
	// through a client, as a real writer's would.)
	db, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.Patch(ctx, "", map[string]any{budget.PathSpendGlobal(today()) + "/counted": 2000}); err != nil {
		t.Fatalf("writing the change that forces the child to write: %v", err)
	}
	waitChild(t, cmd, 20*time.Second)
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGPIPE {
		t.Fatalf("the child ended as %v (exit %d), want death by SIGPIPE (stderr %q)",
			cmd.ProcessState, cmd.ProcessState.ExitCode(), errOut.String())
	}
}

// TestWatchChildWritesTheDegradedRetryLineToStdout pins the reader rule of
// section 10.8 against the real process: in the degraded mode the CLI writes
// `run records unavailable (retrying): …` to **stdout**, on the same stream
// as the documents, so a console that decodes every line as JSON breaks. The
// line is provoked by making a run unreadable: the bucket opens (the project
// marker is there) and the run lists, but its task.json is over the record
// cap, so the read fails with something other than "not found" -- which fails
// the listing rather than marking one row unreadable, and starts the retry.
//
// Mutation (run, restore): change runWatchDegraded's fmt.Fprintf target from
// `out` to `errw`, and this test fails: the line is then on stderr, where a
// reader of stdout never sees it.
func TestWatchChildWritesTheDegradedRetryLineToStdout(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	marker := filepath.Join(runs, filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// One run of acme/app, inside the degraded window, whose task.json cannot
	// be read: it is over the record cap, so the read fails with something
	// other than "not found" -- which fails the listing rather than marking
	// one row unreadable, and starts the retry. The bucket keys runs by slug,
	// not by repo name, and a run ID carries four hex digits.
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	id := time.Now().UTC().Add(-time.Hour).Format("20060102-150405") + "-0f3a"
	record := filepath.Join(runs, "runs", filepath.FromSlash(slug), id, "task.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(runstore.MaxRecordBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// No budget block at all: this is the degraded path.
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\n" +
		"bucket_url: file://" + runs + "\nuser: someone@example.com\nmax_parallel: 2\n" +
		"endpoints: { no_auth: true }\n" +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"

	out := &lockedBuf{}
	cmd, errOut := spawnWatch(t, dir, cfg, out, "--interval", "200ms")

	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(out.String(), "run records unavailable (retrying):") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "run records unavailable (retrying):") {
		t.Fatalf("the degraded retry line never reached stdout: out %q stderr %q (state %v)",
			out.String(), errOut.String(), cmd.ProcessState)
	}
	// The point of the rule: the line is not a document, and it is on the
	// document stream, so a reader that decodes every line as JSON fails.
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.Contains(line, "run records unavailable (retrying):") {
			continue
		}
		var doc map[string]any
		if json.Unmarshal([]byte(line), &doc) == nil {
			t.Fatalf("the retry line decoded as a JSON document: %q", line)
		}
	}
}
