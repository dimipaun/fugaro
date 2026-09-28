package runner_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/cache"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// cacheYAML is the fixture config with one cache entry under HOME.
const cacheYAML = `version: 1
git: { provider: github, base_branch: main }
agent: { auth: api-key, review_rounds: 1 }
workflows:
  app:
    base: web-node
    commands:
      build: sh build.sh
      test: sh test.sh
      reports: ["build/test-results/*.xml"]
    cache:
      - { key: [README.md], paths: ["~/.fugaro-test-cache"] }
    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`

func withBucket(h *harness) *blobx.Bucket {
	b := blobx.Wrap(h.bucket)
	h.deps.Bucket = b
	return b
}

// cacheEntry is the first cache entry of the app workflow in yaml.
func cacheEntry(t *testing.T, yaml string) config.CacheEntry {
	t.Helper()
	cfg, problems := config.Parse([]byte(yaml))
	if len(problems) > 0 {
		t.Fatalf("parsing the fixture config: %v", problems)
	}
	return cfg.Workflows["app"].Cache[0]
}

func fakePR(n int, branch string, draft bool) fake.PRState {
	return fake.PRState{PR: gitprov.PR{Number: n, URL: "https://example.invalid/pr/1", Draft: draft}, Spec: gitprov.PRSpec{Branch: branch}}
}

func TestLockHeldDuringRunThenReleased(t *testing.T) {
	h := newHarness(t, "", nil)
	b := withBucket(h)
	key := lock.Key("acme-app", "fugaro/"+runID)
	held := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if ok, _ := b.Exists(ctx, key); !ok {
			t.Error("the branch lock is not held during implement")
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, held, review("ship", 0))
	if err != nil || rec.Stage != "writeback" || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if ok, _ := b.Exists(context.Background(), key); ok {
		t.Fatal("the lock survives the run")
	}
}

func TestBranchBusyIsInfraError(t *testing.T) {
	h := newHarness(t, "", nil)
	b := withBucket(h)
	key := lock.Key("acme-app", "fugaro/"+runID)
	if _, err := lock.Acquire(context.Background(), b, key, lock.Holder{RunID: "20260101-000000-ffff", ExpiresAt: time.Now().Add(time.Hour)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t) // no agent steps may run
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "branch busy") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if ok, _ := b.Exists(context.Background(), key); !ok {
		t.Fatal("a busy run released someone else's lock")
	}
}

const (
	exec1 = "projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-app/executions/fugaro-acme-app-app-aaaaa"
	exec2 = "projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-app/executions/fugaro-acme-app-app-bbbbb"
	// exec1 as the API may spell it, with the project number.
	exec1ByNumber = "projects/123456789/locations/us-east5/jobs/fugaro-acme-app-app/executions/fugaro-acme-app-app-aaaaa"
)

func TestDuplicateExecutionWritesNothing(t *testing.T) {
	h := newHarness(t, "", nil)
	withBucket(h)
	first := &runstore.Record{Version: 1, RunID: runID, Repo: "acme/app", Execution: exec1, Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone}
	if err := h.store.WriteRecord(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	h.deps.Execution = exec2
	_, err := h.run(t)
	if !errors.Is(err, runner.ErrDuplicateExecution) {
		t.Fatalf("err = %v", err)
	}
	stored, _ := h.store.ReadRecord(context.Background())
	if stored.Execution != exec1 || stored.Status != runstore.StatusRunning || stored.Stage != "implement" {
		t.Fatalf("the duplicate overwrote result.json: %+v", stored)
	}
	if len(h.provider.State.PRs) != 0 {
		t.Fatal("the duplicate opened a PR")
	}
}

func TestSameExecutionSpelledDifferentlyIsNotADuplicate(t *testing.T) {
	h := newHarness(t, "", nil)
	withBucket(h)
	// A record whose execution came back from the API with the project number.
	prev := &runstore.Record{Version: 1, RunID: runID, Repo: "acme/app", Execution: exec1ByNumber, Status: runstore.StatusRunning, Stage: "bootstrap", Outcome: runstore.OutcomeNone}
	_ = h.store.WriteRecord(context.Background(), prev)
	h.deps.Execution = exec1
	if rec, err := h.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestDuplicateExecutionLosesLock covers the defensive path: the first
// execution's record is gone but its lock is held. The duplicate has
// created its own first record by then; it must not go further, mark the
// run infra_error, or release the other execution's lock.
func TestDuplicateExecutionLosesLock(t *testing.T) {
	h := newHarness(t, "", nil)
	b := withBucket(h)
	key := lock.Key("acme-app", "fugaro/"+runID)
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if _, err := lock.Acquire(context.Background(), b, key, lock.Holder{RunID: runID, Execution: exec1, ExpiresAt: expires}, time.Now()); err != nil {
		t.Fatal(err)
	}
	h.deps.Execution = exec2
	_, err := h.run(t)
	if !errors.Is(err, runner.ErrDuplicateExecution) {
		t.Fatalf("err = %v", err)
	}
	// The duplicate hands the record it created to the lock's holder, so
	// every view follows the owner.
	stored, _ := h.store.ReadRecord(context.Background())
	if stored.Status != runstore.StatusRunning || stored.Execution != exec1 || stored.Stage != "bootstrap" ||
		stored.Deadline == nil || !stored.Deadline.Equal(expires) {
		t.Fatalf("the duplicate's record = %+v, want it running in bootstrap, naming %s until %s", stored, exec1, expires)
	}
	if ok, _ := b.Exists(context.Background(), key); !ok {
		t.Fatal("the duplicate released the first execution's lock")
	}
}

// TestOwnLockIsAdopted covers a retried create (or a restarted
// execution) that finds the lock this very execution already holds,
// spelled with the project number: it is ours, not a busy branch, and it
// is released at the end.
func TestOwnLockIsAdopted(t *testing.T) {
	h := newHarness(t, "", nil)
	b := withBucket(h)
	key := lock.Key("acme-app", "fugaro/"+runID)
	if _, err := lock.Acquire(context.Background(), b, key, lock.Holder{RunID: runID, Execution: exec1ByNumber, ExpiresAt: time.Now().Add(time.Hour)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	h.deps.Execution = exec1
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if ok, _ := b.Exists(context.Background(), key); ok {
		t.Fatal("the adopted lock survives the run")
	}
}

func TestRecordsExecutionAndDeadline(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.Execution = exec1
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Execution != exec1 {
		t.Fatalf("rec.Execution = %q, err = %v", rec.Execution, err)
	}
	// The fixture's finalize_reserve is 30s, stored for cancel's grace floor.
	if d, ok := rec.FinalizeReserve(); !ok || d != 30*time.Second {
		t.Fatalf("finalize reserve = %v, %v", d, ok)
	}
	// The fixture's total is 5m: deadline = started_at + 5m + 3m.
	if rec.Deadline == nil || !rec.Deadline.Equal(rec.StartedAt.Add(8*time.Minute)) {
		t.Fatalf("deadline = %v, started %v", rec.Deadline, rec.StartedAt)
	}
}

func TestCancelledRunSkipsCacheWriteback(t *testing.T) {
	h := newHarness(t, cacheYAML, nil)
	b := withBucket(h)
	home := envValue(h.deps.Env, "HOME")
	cancelAfterFilling := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_ = os.MkdirAll(filepath.Join(home, ".fugaro-test-cache"), 0o755)
		_ = os.WriteFile(filepath.Join(home, ".fugaro-test-cache", "pkg.tgz"), []byte("deps"), 0o644)
		_ = h.store.RequestCancel(context.Background())
		return blockUntilDone(t, ctx, req)
	}
	rec, err := h.run(t, cancelAfterFilling)
	if err != nil || rec.Status != runstore.StatusCancelled {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	it := b.List(&blob.ListOptions{Prefix: "cache/"})
	if obj, err := it.Next(context.Background()); err == nil {
		t.Fatalf("a cancelled run wrote a cache: %s", obj.Key)
	}
	if ok, _ := b.Exists(context.Background(), lock.Key("acme-app", "fugaro/"+runID)); ok {
		t.Fatal("a cancelled run kept its lock")
	}
}

func TestCachesRestoredAndWrittenBack(t *testing.T) {
	h := newHarness(t, cacheYAML, nil)
	b := withBucket(h)
	h.deps.BaseImage = "base@sha256:abc"
	home := envValue(h.deps.Env, "HOME")
	cacheDir := filepath.Join(home, ".fugaro-test-cache")
	fill := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cacheDir, "pkg.tgz"), []byte("deps"), 0o644); err != nil {
			t.Fatal(err)
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, fill, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	it := b.List(nil)
	var archives int
	for obj, err := it.Next(context.Background()); err == nil; obj, err = it.Next(context.Background()) {
		if strings.HasPrefix(obj.Key, "cache/acme-app/app/") && strings.HasSuffix(obj.Key, ".tar.zst") {
			archives++
		}
	}
	if archives != 1 {
		t.Fatalf("%d cache archives written, want 1", archives)
	}

	// A second run on the same bucket restores the cache before implement.
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}
	const run2 = "20260926-231530-bcde"
	spec := &task.Spec{Version: 1, RunID: run2, Repo: "acme/app", Ref: "main", Task: "Again"}
	h.deps.Store = runstore.Open(h.bucket, "acme-app", run2)
	if err := h.deps.Store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	restored := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if data, err := os.ReadFile(filepath.Join(cacheDir, "pkg.tgz")); err != nil || string(data) != "deps" {
			t.Errorf("cache not restored before implement: %q, %v", data, err)
		}
		return implement("again")(t, ctx, req)
	}
	h.agent.calls = nil
	if _, err := h.run(t, restored, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptCacheDoesNotFailTheRun(t *testing.T) {
	h := newHarness(t, cacheYAML, nil)
	b := withBucket(h)
	// Plant garbage under the exact key bootstrap will compute: no
	// FUGARO_BASE_IMAGE keys as "unpinned", and the fixture has no image:
	// settings.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(h.files["README.md"]), 0o644); err != nil {
		t.Fatal(err)
	}
	key, ok, err := cache.KeyOf(context.Background(), root, cacheEntry(t, cacheYAML), "unpinned", runner.ToolchainHash(config.Image{}), nil)
	if err != nil || !ok {
		t.Fatal(err)
	}
	obj := cache.ObjectKey("acme-app", "app", key)
	_ = b.WriteAll(context.Background(), obj, []byte("garbage"), nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// The restore met the garbage (and deleted it as a bad archive)
	// rather than missing a differently computed key.
	if data, err := b.ReadAll(context.Background(), obj); err == nil && string(data) == "garbage" {
		t.Fatal("the planted archive was never read: bootstrap computed a different key")
	}
}

func TestToolchainHashIsStable(t *testing.T) {
	img := config.Image{Node: "24.19.0", Apt: []string{"libvips-dev", "fonts-liberation"}, Setup: []string{"a", "b"}}
	same := config.Image{Node: "24.19.0", Apt: []string{"libvips-dev", "fonts-liberation"}, Setup: []string{"a", "b"}}
	if runner.ToolchainHash(img) != runner.ToolchainHash(same) {
		t.Fatal("equal image settings hash differently")
	}
	if got := runner.ToolchainHash(config.Image{}); got != runner.ToolchainHash(config.Image{}) || got == "" {
		t.Fatalf("empty image settings hash = %q", got)
	}
	if runner.ToolchainHash(config.Image{Apt: []string{}, Setup: []string{}}) != runner.ToolchainHash(config.Image{}) {
		t.Error("empty and absent apt/setup lists hash differently")
	}
	for name, other := range map[string]config.Image{
		"node":        {Node: "24.20.0", Apt: img.Apt, Setup: img.Setup},
		"jdk":         {Node: img.Node, JDK: "21", Apt: img.Apt, Setup: img.Setup},
		"apt":         {Node: img.Node, Apt: []string{"libvips-dev"}, Setup: img.Setup},
		"setup order": {Node: img.Node, Apt: img.Apt, Setup: []string{"b", "a"}},
		"field shift": {Node: img.Node, Apt: img.Apt, Setup: []string{"ab"}},
	} {
		if runner.ToolchainHash(other) == runner.ToolchainHash(img) {
			t.Errorf("%s: a changed toolchain keeps its hash", name)
		}
	}
	if runner.ToolchainHash(config.Image{Apt: []string{"b", "a"}}) != runner.ToolchainHash(config.Image{Apt: []string{"a", "b"}}) {
		t.Error("the order of apt packages changes the hash")
	}
	// A pinned value for a fixed input: the hash must not drift between
	// versions, or every cache would go cold.
	const want = "625ade30036547d627fc6eb96eade1697865c8075fa0897d2d36dd35bda0e1de"
	if got := runner.ToolchainHash(config.Image{Node: "24"}); got != want {
		t.Fatalf("ToolchainHash(node 24) = %s, want %s", got, want)
	}
}

func TestAgentReadiedPRIsReturnedToDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	h.fails(t, "beta")
	readyItself := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		// The agent opens and readies its own PR (gh pr create && gh pr ready).
		h.provider.State.PRs = append(h.provider.State.PRs, fakePR(1, "fugaro/"+runID, false))
		return res, err
	}
	rec, err := h.run(t, readyItself, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if pr := onlyPR(t, h.provider); !pr.Draft {
		t.Fatal("finalize left the agent's ready PR ready on a failing run")
	}
}

// TestUnpinnedBaseWarnsButRuns covers a --local build (a tag) and a
// repository Dockerfile (no FUGARO_BASE_IMAGE): the run warns that its
// caches are keyed on an unpinned base and carries on; a digest does not warn.
func TestUnpinnedBaseWarnsButRuns(t *testing.T) {
	for base, wantWarn := range map[string]bool{"": true, "fugaro/web-node:dev": true, "reg/web-node@sha256:abc": false} {
		t.Run(base, func(t *testing.T) {
			h := newHarness(t, cacheYAML, nil)
			withBucket(h)
			h.deps.BaseImage = base
			var logs bytes.Buffer
			h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
			rec, err := h.run(t, implement("feature"), review("ship", 0))
			if err != nil || rec.Status != runstore.StatusSucceeded {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if got := strings.Count(logs.String(), "not pinned by digest"); got != map[bool]int{true: 1, false: 0}[wantWarn] {
				t.Fatalf("%d unpinned warnings, want warn=%v:\n%s", got, wantWarn, logs.String())
			}
		})
	}
}

// TestLockReleasedAfterPostLockBootstrapFailure: a bootstrap error after
// the lock (here git.provider disagreeing with --provider) still releases it.
func TestLockReleasedAfterPostLockBootstrapFailure(t *testing.T) {
	h := newHarness(t, "", nil)
	b := withBucket(h)
	h.deps.ProviderKind = "bitbucket" // the fixture's git.provider is github
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "git.provider") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if ok, _ := b.Exists(context.Background(), lock.Key("acme-app", "fugaro/"+runID)); ok {
		t.Fatal("a failed bootstrap kept the branch lock")
	}
}

// TestWritebackRekeysFromFinalTree: the agent changed the key file, so the
// dependencies it installed are written under the new key, not the one
// bootstrap restored from.
func TestWritebackRekeysFromFinalTree(t *testing.T) {
	h := newHarness(t, cacheYAML, nil)
	b := withBucket(h)
	h.deps.BaseImage = "base@sha256:abc"
	home := envValue(h.deps.Env, "HOME")
	const newReadme = "a new lockfile\n"
	bump := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_ = os.MkdirAll(filepath.Join(home, ".fugaro-test-cache"), 0o755)
		_ = os.WriteFile(filepath.Join(home, ".fugaro-test-cache", "pkg.tgz"), []byte("deps"), 0o644)
		if err := os.WriteFile(filepath.Join(req.Dir, "README.md"), []byte(newReadme), 0o644); err != nil {
			t.Fatal(err)
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, bump, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	keyFor := func(readme string) string {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
			t.Fatal(err)
		}
		key, _, err := cache.KeyOf(context.Background(), root, cacheEntry(t, cacheYAML), "base@sha256:abc", runner.ToolchainHash(config.Image{}), nil)
		if err != nil {
			t.Fatal(err)
		}
		return cache.ObjectKey("acme-app", "app", key)
	}
	if ok, _ := b.Exists(context.Background(), keyFor(newReadme)); !ok {
		t.Fatal("no archive under the final tree's key")
	}
	if ok, _ := b.Exists(context.Background(), keyFor(h.files["README.md"])); ok {
		t.Fatal("the archive was written under the key bootstrap started from")
	}
}

// cache2YAML declares two caches, so a restore timeout has a later entry.
var cache2YAML = strings.Replace(cacheYAML,
	`      - { key: [README.md], paths: ["~/.fugaro-test-cache"] }`,
	"      - { key: [README.md], paths: [\"~/.fugaro-test-cache\"] }\n      - { key: [README.md], paths: [\"~/.fugaro-test-cache2\"] }", 1)

// TestRestoreTimeoutLoggedOnce: the restore the bound interrupts is
// logged with its object; later entries are only counted as skipped.
func TestRestoreTimeoutLoggedOnce(t *testing.T) {
	if cache2YAML == cacheYAML {
		t.Fatal("cache2YAML did not add an entry")
	}
	runner.SetRestoreBound(t, 50*time.Millisecond)
	runner.SetSlowRestore(t)
	out := runLogged(t, cache2YAML)
	if n := strings.Count(out, "cache restore timed out"); n != 1 || !strings.Contains(out, "object=cache/acme-app/app/") {
		t.Fatalf("%d timeout warnings, want 1 naming the object:\n%s", n, out)
	}
	if !strings.Contains(out, "restore time bound was reached") || !strings.Contains(out, "entries=1") {
		t.Fatalf("the later entry was not counted as skipped:\n%s", out)
	}
}

// TestRestoreBoundPassedBeforeAnEntryStarts: an entry that never started
// before the bound passed was not slow, so it is counted, never named.
func TestRestoreBoundPassedBeforeAnEntryStarts(t *testing.T) {
	runner.SetRestoreBound(t, time.Nanosecond)
	out := runLogged(t, cache2YAML)
	if strings.Contains(out, "cache restore timed out") {
		t.Fatalf("an entry that never started was named as timed out:\n%s", out)
	}
	if !strings.Contains(out, "restore time bound was reached") || !strings.Contains(out, "entries=2") {
		t.Fatalf("the entries were not counted as skipped:\n%s", out)
	}
}

// runLogged runs a ready scenario with cfg and returns its text log.
func runLogged(t *testing.T, cfg string) string {
	t.Helper()
	h := newHarness(t, cfg, nil)
	withBucket(h)
	var logs bytes.Buffer
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	return logs.String()
}
