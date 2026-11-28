package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"gocloud.dev/blob/fileblob"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/selfupgrade"
	"github.com/dimipaun/fugaro/internal/task"
)

// The two releases these tests compare: the job image's own binary runs
// stampOld, and the task a newer launcher wrote is stamped stampGood.
const (
	stampGood = "0.5.0"
	stampOld  = "0.4.0"
)

// selfUpgradeRunID is the run every test here works on.
const selfUpgradeRunID = "20260926-221530-abcd"

// upgradedPayload is what the fake release's fugaro holds. The tests read it
// back from the installed file, so it is bytes rather than something
// runnable: the re-exec itself is what execSelf records.
var upgradedPayload = []byte("#!/bin/sh\necho fugaro\n")

// fakeRelease is a fake GitHub release: checksums.txt and the archive at the
// names internal/selfupgrade fetches, with a request count and a knob that
// makes every request fail.
type fakeRelease struct {
	*httptest.Server

	mu       sync.Mutex
	requests int
	status   int
}

func newFakeRelease(t *testing.T, version string, archive []byte) *fakeRelease {
	t.Helper()
	r := &fakeRelease{}
	sum := sha256.Sum256(archive)
	name := upgradeArchiveName(version)
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests++
		status := r.status
		r.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		dir := "/v" + version + "/"
		switch req.URL.Path {
		case dir + "checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		case dir + name:
			w.Write(archive)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(r.Server.Close)
	t.Cleanup(selfupgrade.PointAtForTest(r.Server.URL + "/"))
	return r
}

// upgradeArchiveName is the archive goreleaser writes for a job image, as
// internal/selfupgrade builds the name.
func upgradeArchiveName(version string) string {
	return "fugaro_" + version + "_linux_" + runtime.GOARCH + ".tar.gz"
}

func (r *fakeRelease) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests
}

func (r *fakeRelease) refuse(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = code
}

// releaseArchive builds the tar.gz a release asset is: one regular fugaro.
func releaseArchive(t *testing.T, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: "fugaro", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// cloudTask is a cloud job's run: its task written into a fresh bucket, and
// this binary's release set to have. write stores the task, so a test can
// write one carrying a field this binary does not know. It returns the
// --bucket URL and the run's store.
func cloudTask(t *testing.T, have string, write func(ctx context.Context, store *runstore.Store) error) (string, *runstore.Store) {
	t.Helper()
	setCloudRunEnv(t, "aaaaa")
	// The marker must be absent, not empty: an empty one would be the first
	// match for the value a re-exec appends.
	for _, k := range []string{selfupgrade.EnvDir, selfupgrade.EnvSum} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	old := Version
	Version = have
	t.Cleanup(func() { Version = old })
	dir := t.TempDir()
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	store := runstore.Open(b, appSlug, selfUpgradeRunID)
	if err := write(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	return "file://" + dir, store
}

// stampedSpec is the task a newer launcher wrote: an ordinary one, carrying
// the launcher's release.
func stampedSpec() *task.Spec {
	return &task.Spec{Version: 1, RunID: selfUpgradeRunID, Repo: "acme/app", Workflow: fixtureWorkflow,
		Ref: "main", Task: "x", CLIVersion: stampGood}
}

// captureReexec installs the execSelf hook and restores it when the test
// ends. It reports whether a re-exec was attempted, and where to.
func captureReexec(t *testing.T) (*bool, *string, *[]string) {
	t.Helper()
	called, exe, env := new(bool), new(string), new([]string)
	old := execSelf
	execSelf = func(exe0 string, _ []string, env0 []string) error {
		*called, *exe, *env = true, exe0, env0
		// A successful exec never returns, so the run goes on from here: the
		// test reads what the hook captured, and the run then ends at its
		// checkout, as a cloud run with no remote does.
		return nil
	}
	t.Cleanup(func() { execSelf = old })
	return called, exe, env
}

// cloudExecArgs are the flags a cloud exec needs, with a work directory and a
// state directory of their own and no remote, so a run that gets past the
// upgrade ends at its checkout rather than reaching the network.
func cloudExecArgs(t *testing.T, bucket string) []string {
	t.Helper()
	return []string{"exec", "--bucket", bucket, "--run", appSlug + "/" + selfUpgradeRunID,
		"--workdir", filepath.Join(t.TempDir(), "work"), "--state-dir", filepath.Join(t.TempDir(), "state")}
}

// TestExecUpgradesToStamp: a task stamped by a newer launcher makes exec
// fetch exactly that release, install it and re-exec it (spec §6, D3441).
// What it re-execs is the fetched binary, and the process that runs it is
// told where the install is and what its checksum is, which is what every
// stage of the run is checked against (F33).
func TestExecUpgradesToStamp(t *testing.T) {
	r := newFakeRelease(t, stampGood, releaseArchive(t, upgradedPayload))
	bucket, _ := cloudTask(t, stampOld, func(ctx context.Context, store *runstore.Store) error {
		return store.WriteTask(ctx, stampedSpec())
	})
	called, exe, env := captureReexec(t)

	_, _, err := execute(t, cloudExecArgs(t, bucket)...)
	if !*called {
		t.Fatalf("no re-exec: err = %v", err)
	}
	if !strings.Contains(*exe, "fugaro-"+stampGood) {
		t.Fatalf("re-exec'd %q, want the installed release %s", *exe, stampGood)
	}
	body, rerr := os.ReadFile(*exe)
	if rerr != nil || !bytes.Equal(body, upgradedPayload) {
		t.Fatalf("the re-exec'd binary is %q, %v; want the fetched one", body, rerr)
	}
	sum := sha256.Sum256(upgradedPayload)
	gotDir, _ := envHas(*env, selfupgrade.EnvDir)
	gotSum, _ := envHas(*env, selfupgrade.EnvSum)
	if gotDir != filepath.Dir(*exe) || gotSum != hex.EncodeToString(sum[:]) {
		t.Fatalf("the re-exec'd process carries %q/%q, want the install and its checksum", gotDir, gotSum)
	}
	if got := r.count(); got != 2 {
		t.Fatalf("%d requests: want checksums.txt and the archive, and nothing else", got)
	}
	if !strings.Contains(err.Error(), "holds no checkout and this run has no remote") {
		t.Fatalf("err = %v: the run did not go on past the upgrade", err)
	}
}

// TestExecUpgradesDespiteUnknownField: the stamp is read with a lenient
// decode, so a task a newer launcher wrote — one carrying a field this
// binary does not know — still upgrades it (F41). The strict parse is the
// upgraded runner's, and happens after.
func TestExecUpgradesDespiteUnknownField(t *testing.T) {
	r := newFakeRelease(t, stampGood, releaseArchive(t, upgradedPayload))
	data, err := stampedSpec().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["a_field_a_later_release_added"] = true
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, perr := task.Parse(raw); perr == nil {
		t.Fatal("this binary parsed the task strictly, so the test proves nothing")
	}
	bucket, _ := cloudTask(t, stampOld, func(ctx context.Context, store *runstore.Store) error {
		return store.PutFile(ctx, "task.json", raw, "application/json")
	})
	called, _, _ := captureReexec(t)

	_, _, err = execute(t, cloudExecArgs(t, bucket)...)
	if !*called {
		t.Fatalf("a task with an unknown field did not upgrade the runner: err = %v", err)
	}
	if got := r.count(); got != 2 {
		t.Fatalf("%d requests, want 2", got)
	}
}

// TestExecUpgradesOnce: the process an upgrade re-execs carries the marker,
// and a marker stops a second upgrade (D3442).
func TestExecUpgradesOnce(t *testing.T) {
	r := newFakeRelease(t, stampGood, releaseArchive(t, upgradedPayload))
	bucket, _ := cloudTask(t, stampOld, func(ctx context.Context, store *runstore.Store) error {
		return store.WriteTask(ctx, stampedSpec())
	})
	t.Setenv(selfupgrade.EnvDir, filepath.Join(t.TempDir(), "installed"))
	called, _, _ := captureReexec(t)

	_, _, err := execute(t, cloudExecArgs(t, bucket)...)
	if *called {
		t.Fatal("a process that already upgraded re-exec'd again")
	}
	if got := r.count(); got != 0 {
		t.Fatalf("%d requests for a process that already upgraded", got)
	}
	if !strings.Contains(err.Error(), "holds no checkout and this run has no remote") {
		t.Fatalf("err = %v: the run did not go on past the upgrade check", err)
	}
}

// TestExecFailsOnUnfetchableStamp: a stamp that cannot be fetched ends the
// run in one line naming the version, and the run's own record carries that
// line, so the run is visible rather than lost (D3443).
func TestExecFailsOnUnfetchableStamp(t *testing.T) {
	r := newFakeRelease(t, stampGood, releaseArchive(t, upgradedPayload))
	r.refuse(http.StatusInternalServerError)
	bucket, store := cloudTask(t, stampOld, func(ctx context.Context, store *runstore.Store) error {
		return store.WriteTask(ctx, stampedSpec())
	})
	called, _, _ := captureReexec(t)

	_, _, err := execute(t, cloudExecArgs(t, bucket)...)
	if *called {
		t.Fatal("a release that could not be fetched was re-exec'd")
	}
	if ExitCode(err) != ExitRemoteError || err == nil {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	msg := err.Error()
	if strings.Contains(msg, "\n") || len(msg) > 140 || !strings.Contains(msg, stampGood) {
		t.Fatalf("the failure is not one line naming the version: %q", msg)
	}
	rec, rerr := store.ReadRecord(context.Background())
	if rerr != nil {
		t.Fatal(rerr)
	}
	if rec.Status != runstore.StatusInfraError || rec.Reason != msg {
		t.Fatalf("record = %+v: the run was not recorded with the upgrade's own line", rec)
	}
}

// TestLocalExecNeverUpgrades: `--task-file` is a person's own binary on a
// person's own machine, and a stamp above it upgrades nothing there.
func TestLocalExecNeverUpgrades(t *testing.T) {
	r := newFakeRelease(t, stampGood, releaseArchive(t, upgradedPayload))
	old := Version
	Version = stampOld
	t.Cleanup(func() { Version = old })
	specJSON, err := stampedSpec().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	taskFile := filepath.Join(t.TempDir(), "task.json")
	if err := os.WriteFile(taskFile, specJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	called, _, _ := captureReexec(t)

	_, _, err = execute(t, "exec", "--bucket", "file://"+t.TempDir(), "--task-file", taskFile, "--provider", "fake",
		"--provider-state", filepath.Join(t.TempDir(), "provider.json"),
		"--workdir", filepath.Join(t.TempDir(), "work"), "--state-dir", filepath.Join(t.TempDir(), "state"))
	if *called {
		t.Fatal("a local --task-file invocation upgraded itself")
	}
	if got := r.count(); got != 0 {
		t.Fatalf("%d requests from a local invocation", got)
	}
	if err == nil {
		t.Fatal("the local run went on with no checkout to work on")
	}
}

// TestRunnerRefusesATamperedInstall: the runner re-checks the install an
// upgrade left before every stage, and a tool planted beside the binary ends
// the run in one line (F33): the job runs as one non-root user, which could
// write its own claude beside the binary and have a later stage run that
// instead of the release the run was launched with.
func TestRunnerRefusesATamperedInstall(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "fugaro")
	if err := os.WriteFile(exe, upgradedPayload, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(upgradedPayload)
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pins := func(cfg string) string {
		return strings.Replace(pinnedConfig(t, cfg), "coder: deepseek/deepseek-v4-flash", "coder: claude-sonnet-5-5", 1)
	}
	_, _, store := localExecCfg(t, pins, map[string]string{
		selfupgrade.EnvDir:   dir,
		selfupgrade.EnvSum:   hex.EncodeToString(sum[:]),
		"FIXTURE_FAILS_FILE": filepath.Join(t.TempDir(), "fails"),
	}, "--claude", filepath.Join(t.TempDir(), "no-such-claude"))
	rec, rerr := store.ReadRecord(context.Background())
	if rerr != nil {
		t.Fatal(rerr)
	}
	// The run ends there rather than going on to a stage: the first stage
	// that would run is the one that finds the planted tool.
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "the upgraded fugaro binary changed") {
		t.Fatalf("record = %+v: the run did not end on the install it re-exec'd", rec)
	}
}
