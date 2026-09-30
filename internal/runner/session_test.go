package runner_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// realPath is p with symlinks resolved: Claude Code names a session
// directory after its real working directory.
func realPath(t *testing.T, p string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// sessionPath is where Claude Code keeps session id for a stage run with req.
func sessionPath(t *testing.T, req agent.Request, id string) string {
	t.Helper()
	return filepath.Join(agent.SessionDir(envValue(req.Env, "HOME"), realPath(t, req.Dir)), id+".jsonl")
}

// writeSession writes content as session id's file, as Claude Code would.
func writeSession(t *testing.T, req agent.Request, id, content string) {
	t.Helper()
	p := sessionPath(t, req, id)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

// withSession runs s after writing content to the session the stage
// reports: id, or the one the runner asked for when id is empty.
func withSession(s step, id, content string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		sid := id
		if sid == "" {
			sid = req.SessionID
		}
		writeSession(t, req, sid, content)
		res, err := s(t, ctx, req)
		res.SessionID = sid
		return res, err
	}
}

// bucketObjects returns every object in b, by key.
func bucketObjects(t *testing.T, b *blob.Bucket) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	it := b.List(nil)
	for {
		obj, err := it.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := b.ReadAll(context.Background(), obj.Key)
		if err != nil {
			t.Fatal(err)
		}
		out[obj.Key] = data
	}
}

func TestSaveSessionUploads(t *testing.T) {
	h := newHarness(t, "", nil)
	content := `{"prompt":"Add a feature"}` + "\n"
	rec, err := h.run(t, withSession(implement("feature"), "", content), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	id := h.agent.calls[0].SessionID
	if rec.PushedHead == "" || rec.PushedHead != rec.HeadSHA {
		t.Fatalf("pushed_head = %q, head_sha = %q", rec.PushedHead, rec.HeadSHA)
	}
	m, data, err := h.store.ReadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := runstore.SessionMeta{Version: 1, ID: id, HeadSHA: rec.PushedHead, WorkDir: h.deps.WorkDir, Bytes: int64(len(content))}
	if *m != want || string(data) != content {
		t.Fatalf("session = %+v, %q; want %+v", m, data, want)
	}
	objs := bucketObjects(t, h.bucket)
	for _, name := range []string{"session/" + id + ".jsonl", "session/session.json"} {
		if _, ok := objs[h.store.Prefix()+name]; !ok {
			t.Errorf("%s was not stored", name)
		}
	}
}

func TestSaveSessionUsesResultID(t *testing.T) {
	h := newHarness(t, "", nil)
	forked := agent.NewSessionID()
	if _, err := h.run(t, withSession(implement("feature"), forked, "forked\n"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	m, data, err := h.store.ReadSession(context.Background())
	if err != nil || m.ID != forked || string(data) != "forked\n" {
		t.Fatalf("session = %+v, %q, %v; want %s", m, data, err, forked)
	}
}

func TestSaveSessionRedacts(t *testing.T) {
	h := newHarness(t, "", nil)
	content := `{"tool_result":"ANTHROPIC_API_KEY=test-key"}` + "\n" + `{"prompt":"ok"}` + "\n"
	if _, err := h.run(t, withSession(implement("feature"), "", content), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	_, data, err := h.store.ReadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("test-key")) || !bytes.Contains(data, []byte("[REDACTED]")) || !bytes.Contains(data, []byte(`{"prompt":"ok"}`)) {
		t.Fatalf("session not redacted: %q", data)
	}
}

func TestSaveSessionRefusesSymlink(t *testing.T) {
	h := newHarness(t, "", nil)
	canary := filepath.Join(t.TempDir(), "canary")
	if err := os.WriteFile(canary, []byte("canary-4a61d0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	swap := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		p := sessionPath(t, req, req.SessionID)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(canary, p); err != nil {
			t.Fatal(err)
		}
		res.SessionID = req.SessionID
		return res, err
	}
	rec, err := h.run(t, swap, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v; a refused session must not fail the run", rec, err)
	}
	for key, data := range bucketObjects(t, h.bucket) {
		if bytes.Contains(data, []byte("canary-4a61d0")) {
			t.Fatalf("%s holds the symlink target's contents", key)
		}
	}
	if _, _, err := h.store.ReadSession(context.Background()); !errors.Is(err, runstore.ErrNotFound) {
		t.Fatalf("ReadSession err = %v, want nothing saved", err)
	}
}

func TestSaveSessionCap(t *testing.T) {
	h := newHarness(t, "", nil)
	huge := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		writeSession(t, req, req.SessionID, "")
		// Sparse, so the test writes almost nothing to disk.
		if err := os.Truncate(sessionPath(t, req, req.SessionID), runstore.MaxSessionBytes+1); err != nil {
			t.Fatal(err)
		}
		res.SessionID = req.SessionID
		return res, err
	}
	rec, err := h.run(t, huge, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if _, _, err := h.store.ReadSession(context.Background()); !errors.Is(err, runstore.ErrNotFound) {
		t.Fatalf("ReadSession err = %v, want nothing saved", err)
	}
}

func TestSaveSessionAtCap(t *testing.T) {
	h := newHarness(t, "", nil)
	full := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		writeSession(t, req, req.SessionID, "")
		if err := os.Truncate(sessionPath(t, req, req.SessionID), runstore.MaxSessionBytes); err != nil {
			t.Fatal(err)
		}
		res.SessionID = req.SessionID
		return res, err
	}
	if _, err := h.run(t, full, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if m, data, err := h.store.ReadSession(context.Background()); err != nil || len(data) != runstore.MaxSessionBytes || m.Bytes != runstore.MaxSessionBytes {
		t.Fatalf("session at the cap: %+v, %d bytes, %v", m, len(data), err)
	}
}

func TestSaveSessionOnCancel(t *testing.T) {
	h := newHarness(t, "", nil)
	cancelThenBlock := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		writeSession(t, req, req.SessionID, "cancelled\n")
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		res, err := blockUntilDone(t, ctx, req)
		res.SessionID = req.SessionID
		return res, err
	}
	rec, err := h.run(t, cancelThenBlock)
	if err != nil || rec.Status != runstore.StatusCancelled {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	m, data, err := h.store.ReadSession(context.Background())
	if err != nil || m.ID != h.agent.calls[0].SessionID || string(data) != "cancelled\n" {
		t.Fatalf("session = %+v, %q, %v", m, data, err)
	}
}

func TestSaveSessionOnFinalizeError(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.RetryDelay = time.Millisecond
	h.provider.FailEnsure = 3
	rec, err := h.run(t, withSession(implement("feature"), "", "finalize\n"), review("ship", 0))
	if err == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v; want a finalize failure", rec, err)
	}
	m, data, err := h.store.ReadSession(context.Background())
	if err != nil || string(data) != "finalize\n" || m.HeadSHA != rec.PushedHead || rec.PushedHead == "" {
		t.Fatalf("session = %+v, %q, %v; pushed_head %q", m, data, err, rec.PushedHead)
	}
}

func TestSaveSessionSkippedWithoutID(t *testing.T) {
	h := newHarness(t, "", nil)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.store.ReadSession(context.Background()); !errors.Is(err, runstore.ErrNotFound) {
		t.Fatalf("ReadSession err = %v, want nothing saved", err)
	}
}

func TestFixResumesLatestSessionID(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "review_rounds: 2", "review_rounds: 3", 1)
	h := newHarness(t, cfg, nil)
	first, second := agent.NewSessionID(), agent.NewSessionID()
	fix := func(want, next string) step {
		return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			if !req.Resume || req.SessionID != want {
				t.Errorf("fix resumed %q (resume %v), want %q", req.SessionID, req.Resume, want)
			}
			writeSession(t, req, next, "fix of "+want+"\n")
			shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
			verifyTest(t, ctx, req)
			return agent.Result{SessionID: next}, nil
		}
	}
	rec, err := h.run(t, withSession(implement("feature"), first, "implement\n"),
		review("changes", 1), fix(first, second), review("changes", 1), fix(second, second), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if n := len(h.agent.calls); n != 6 {
		t.Fatalf("%d agent calls, want 6", n)
	}
	m, _, err := h.store.ReadSession(context.Background())
	if err != nil || m.ID != second {
		t.Fatalf("saved session = %+v, %v; want %s", m, err, second)
	}
}

// commentHook runs hook before each comment the fake provider posts.
type commentHook struct {
	*fake.Provider
	hook func()
}

func (c *commentHook) Comment(ctx context.Context, pr gitprov.PR, body string) error {
	c.hook()
	return c.Provider.Comment(ctx, pr, body)
}

func TestPushedHeadOnFirstRun(t *testing.T) {
	h := newHarness(t, "", nil)
	var seen []string
	h.deps.OpenProvider = gitprov.Static(&commentHook{Provider: h.provider, hook: func() {
		stored, err := h.store.ReadRecord(context.Background())
		if err != nil {
			t.Error(err)
			return
		}
		seen = append(seen, stored.PushedHead)
	}})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	remoteTip := testutil.Git(t, h.remote, "rev-parse", "refs/heads/"+rec.Branch)
	if rec.PushedHead != remoteTip || len(seen) != 1 || seen[0] != remoteTip {
		t.Fatalf("pushed_head = %q, stored before the report = %q, remote tip %s", rec.PushedHead, seen, remoteTip)
	}
}

// restoreRig is a checkout, a HOME and a previous run's store to restore from.
type restoreRig struct {
	deps   runner.Deps
	prev   *runstore.Store
	home   string
	head   string
	parent string
}

func newRestoreRig(t *testing.T) *restoreRig {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, testutil.FixtureFiles(t))
	tmp := t.TempDir()
	work, home := filepath.Join(tmp, "work"), filepath.Join(tmp, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitops.OpenOrClone(context.Background(), work, remote, gitops.IdentityEnv()); err != nil {
		t.Fatal(err)
	}
	parent := testutil.Git(t, work, "rev-parse", "HEAD")
	testutil.Git(t, work, "commit", "-q", "--allow-empty", "-m", "second")
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { bucket.Close() })
	return &restoreRig{
		deps: runner.Deps{WorkDir: work, Env: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}},
		prev: runstore.Open(bucket, "acme-app", "20260925-101010-abcd"),
		home: home, head: testutil.Git(t, work, "rev-parse", "HEAD"), parent: parent,
	}
}

// put stores a previous session with m's fields over the defaults.
func (g *restoreRig) put(t *testing.T, m runstore.SessionMeta, data string) runstore.SessionMeta {
	t.Helper()
	if m.Version == 0 {
		m.Version = 1
	}
	if m.ID == "" {
		m.ID = agent.NewSessionID()
	}
	if m.WorkDir == "" {
		m.WorkDir = g.deps.WorkDir
	}
	if m.HeadSHA == "" {
		m.HeadSHA = g.head
	}
	if err := g.prev.PutSession(context.Background(), m, []byte(data)); err != nil {
		t.Fatal(err)
	}
	return m
}

func (g *restoreRig) restore(t *testing.T) runner.Restored {
	t.Helper()
	return runner.RestoreSession(t, context.Background(), g.deps, g.prev, g.head)
}

func (g *restoreRig) sessionFile(t *testing.T, id string) string {
	return filepath.Join(agent.SessionDir(g.home, realPath(t, g.deps.WorkDir)), id+".jsonl")
}

// wantFresh checks res started fresh with a note containing note, and
// wrote no session file.
func (g *restoreRig) wantFresh(t *testing.T, res runner.Restored, note string) {
	t.Helper()
	if res.Resumed || !strings.Contains(res.Note, note) {
		t.Fatalf("restored = %+v, want fresh with a note containing %q", res, note)
	}
	if _, err := os.Stat(filepath.Join(g.home, ".claude")); err == nil {
		if entries, _ := os.ReadDir(agent.SessionDir(g.home, realPath(t, g.deps.WorkDir))); len(entries) > 0 {
			t.Fatalf("a session file was written for a fresh start: %v", entries)
		}
	}
}

func TestRestoreResumes(t *testing.T) {
	g := newRestoreRig(t)
	m := g.put(t, runstore.SessionMeta{}, "line one\n")
	res := g.restore(t)
	if !res.Resumed || res.ID != m.ID || len(res.Moved) != 0 {
		t.Fatalf("restored = %+v", res)
	}
	p := g.sessionFile(t, m.ID)
	data, err := os.ReadFile(p)
	if err != nil || string(data) != "line one\n" {
		t.Fatalf("session file = %q, %v", data, err)
	}
	fi, _ := os.Stat(p)
	di, _ := os.Stat(filepath.Dir(p))
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Fatalf("modes: file %v, dir %v", fi.Mode().Perm(), di.Mode().Perm())
	}
}

func TestRestoreNotesMovedBranch(t *testing.T) {
	g := newRestoreRig(t)
	m := g.put(t, runstore.SessionMeta{HeadSHA: g.parent}, "x\n")
	res := g.restore(t)
	if !res.Resumed || res.ID != m.ID || len(res.Moved) != 1 || !strings.Contains(res.Moved[0], "second") || res.Note == "" {
		t.Fatalf("restored = %+v, want resumed with the one commit since", res)
	}
}

func TestRestoreRefusesRewrittenBranch(t *testing.T) {
	g := newRestoreRig(t)
	// A commit off the branch: known, but not an ancestor of the head.
	testutil.Git(t, g.deps.WorkDir, "checkout", "-q", "-b", "side", g.parent)
	testutil.Git(t, g.deps.WorkDir, "commit", "-q", "--allow-empty", "-m", "side")
	side := testutil.Git(t, g.deps.WorkDir, "rev-parse", "HEAD")
	for _, sha := range []string{side, strings.Repeat("b", 40)} {
		g.put(t, runstore.SessionMeta{HeadSHA: sha}, "x\n")
		g.wantFresh(t, g.restore(t), "the branch was rewritten since the previous session")
	}
}

func TestRestoreRefusesOtherWorkdir(t *testing.T) {
	g := newRestoreRig(t)
	g.put(t, runstore.SessionMeta{WorkDir: "/work/other"}, "x\n")
	g.wantFresh(t, g.restore(t), "another working directory")
}

func TestRestoreBadID(t *testing.T) {
	g := newRestoreRig(t)
	err := g.prev.PutFile(context.Background(), "session/session.json",
		[]byte(`{"version":1,"id":"../../x","head_sha":"`+g.head+`","workdir":"`+g.deps.WorkDir+`"}`), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	g.wantFresh(t, g.restore(t), "session ID")
}

func TestRestoreBadHeadSHA(t *testing.T) {
	g := newRestoreRig(t)
	calls := runner.RecordSessionGit(t)
	for _, sha := range []string{"--output=" + filepath.Join(t.TempDir(), "x"), g.head[:39]} {
		g.put(t, runstore.SessionMeta{HeadSHA: sha}, "x\n")
		g.wantFresh(t, g.restore(t), "not a commit ID")
		for _, c := range calls() {
			if slices.ContainsFunc(c, func(a string) bool { return strings.Contains(a, sha) }) {
				t.Fatalf("git was called with the bad head: %q", c)
			}
		}
	}
}

func TestRestoreNoSession(t *testing.T) {
	g := newRestoreRig(t)
	g.wantFresh(t, g.restore(t), "the previous run saved no session")
}

func TestRestoreMissingFile(t *testing.T) {
	g := newRestoreRig(t)
	err := g.prev.PutFile(context.Background(), "session/session.json",
		[]byte(`{"version":1,"id":"`+agent.NewSessionID()+`","head_sha":"`+g.head+`","workdir":"`+g.deps.WorkDir+`"}`), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	g.wantFresh(t, g.restore(t), "session file")
}

func TestRestoreRefusesExistingFile(t *testing.T) {
	g := newRestoreRig(t)
	m := g.put(t, runstore.SessionMeta{}, "saved\n")
	p := g.sessionFile(t, m.ID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("already here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := g.restore(t)
	if res.Resumed || !strings.Contains(res.Note, "already") {
		t.Fatalf("restored = %+v", res)
	}
	if data, _ := os.ReadFile(p); string(data) != "already here\n" {
		t.Fatalf("existing file changed: %q", data)
	}
}
