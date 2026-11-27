package runner_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// This file is the checkout decision (CF1, D3440): a run works on the checkout
// its image baked for it when that tree is usable, and clones its own repository
// when there is none, when the one there is another repository's, or when it
// cannot be fetched. Nothing here reads FUGARO_CHECKOUT: what decides is the
// tree the image holds.

// symlinksResolved is p with every symlink on the way resolved, which is how
// the run's own stages see a path: t.TempDir() is under /var/folders on macOS,
// itself a symlink to /private/var/folders, so a directory the test named and
// the same directory as the run was handed differ by that link alone. Both
// sides of a path comparison here go through it (review L2).
func symlinksResolved(t *testing.T, p string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// jobHarness is a harness whose run is in a per-repository Cloud Run job, the
// class whose image may hold a baked checkout. The harness's own run is a local
// invocation (Deps.Local), and a local invocation has no image and so no baked
// tree: it opens or clones into the work directory it was given, and removes
// nothing beside it (F24, the separate tests in baked_checkout_test.go).
func jobHarness(t *testing.T, extra map[string]string) *harness {
	t.Helper()
	h := newHarnessFiles(t, "", nil, extra)
	h.deps.Local = false
	h.deps.Project = "aurora"
	h.deps.Execution = cloudRunExecution(gcp.JobName(repoSlug, gcp.WorkflowVariant("app", "")))
	h.deps.Env = append(h.deps.Env, "CLOUD_RUN_EXECUTION=exec-1")
	return h
}

// bakedCheckout lays the checkout a run of the image finds, at the path the
// image bakes it at: a clone of the fixture's remote, which is this run's own
// repository.
func bakedCheckout(t *testing.T, at string, remote string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Dir(at), "clone", "--quiet", remote, at)
	return at
}

// brokenCheckout lays this run's own checkout at `at` and leaves it in the state
// a job that died mid-fetch leaves behind: git still reads the tree — the
// configuration, the refs and the origin all answer — but every fetch through it
// fails, because the directory git unpacks a fetched pack into is a file. A tree
// whose origin is gone fails the fetch at the remote instead, and there is
// nothing left to clone from then, which is a run that ends rather than one that
// falls back.
func brokenCheckout(t *testing.T, at string, remote string) string {
	t.Helper()
	bakedCheckout(t, at, remote)
	objects := filepath.Join(at, ".git", "objects")
	if err := os.RemoveAll(objects); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(objects, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return at
}

// bakedPlaces are the two places the image can hold a checkout of the run's
// repository: the work directory the job started in, and the per-slug directory
// the run would adopt for its own repository (BakedRepoRoot).
var bakedPlaces = []struct {
	name string
	at   func(t *testing.T, h *harness) string
}{
	{"the work directory", func(t *testing.T, h *harness) string { return h.deps.WorkDir }},
	{"the directory the run would adopt", func(t *testing.T, h *harness) string { return bakedDir(t, h, repoSlug) }},
}

// noRemoteJob makes h the job a baked image runs in: the deployer writes
// FUGARO_REMOTE only for a job whose image holds no checkout of this run's
// repository (a clone or shared workflow's, a prebuilt entry's), so a job whose
// image baked one names no remote at all and the bake was the only thing that
// named the repository. What the run clones its own repository from then is the
// URL it computes (cloneURL) — task.json's repository under the kind its own
// slug derives — and the rewrite is what makes that URL reachable without a
// network: git is told to open the fixture's remote in place of github's own
// URL. It returns the URL the run computed, which is where the clone lands from.
func noRemoteJob(t *testing.T, h *harness) string {
	t.Helper()
	url := "https://github.com/acme/app.git"
	h.deps.Remote = ""
	urlRewrite(t, map[string]string{"https://github.com/acme/app.git": h.remote})
	return url
}

// TestForeignBakeRemovedAndCloned (CF1, D3440, F39): the checkout the image
// holds is not this run's repository's — a shared catalog image built for
// another repository, or a warm container an earlier execution left behind — so
// working on it would commit another repository's source under this run's slug,
// with a git credential scoped to this run's repository, and open the pull
// request as this repository's. The run neither works on it nor stops: it logs
// one line, removes the tree before the agent starts, clones its own remote into
// a fresh directory, and the foreign tree is gone by the time a stage runs.
//
// Mutation (run, restore): return the refusal from openBakedCheckout for every
// run rather than only a local one — this test fails with the run in
// infra_error and nothing cloned. Mutation (run, restore): clone over the
// foreign tree without removing it — the agent's first stage finds the other
// repository's file.
// The tree is the per-slug one and not the work directory: a checkout of
// another repository in the work directory of a per-repository job is refused
// before the checkout is opened at all, by the admission's own remote binding
// (admissionCheck), which reads the origin of a checkout already in the work
// directory as the remote the run would clone from. That refusal is a halt and
// not this fallback, since it is about the remote the run was launched with
// rather than about a tree the image baked for it.
//
// The job names no remote (noRemoteJob), because the job this is about is one
// whose image baked a checkout and the deployer writes FUGARO_REMOTE only for a
// job whose image holds none: the tree the run would have cloned from went with
// the fallback, so the URL the clone is made from is the one the run computes
// from its own task (cloneURL, the review finding on this change).
func TestForeignBakeRemovedAndCloned(t *testing.T) {
	h := jobHarness(t, nil)
	var logs bytes.Buffer
	h.deps.Log = runner.NewLogger(&logs)
	computed := noRemoteJob(t, h)
	foreign := bakedDir(t, h, repoSlug)
	otherRepositoryCheckout(t, h, foreign)
	mark := filepath.Join(foreign, "secret.txt")
	writeFileAt(t, mark, "another repository's source\n")
	readableAtStage := false
	first := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if _, err := os.Stat(mark); err == nil {
			readableAtStage = true
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, first, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v: the run did not clone over another repository's checkout", rec, err)
	}
	if readableAtStage {
		t.Errorf("the agent ran with another repository's file still at %s", mark)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Errorf("the other repository's file is still at %s after the run", mark)
	}
	if _, err := os.Stat(foreign); err == nil {
		t.Errorf("the unusable tree at %s is still there after the run", foreign)
	}
	if got, want := symlinksResolved(t, h.agent.calls[0].Dir), symlinksResolved(t, h.deps.WorkDir); got != want {
		t.Errorf("the run worked in %q, want the checkout it cloned into %q", got, want)
	}
	if got := testutil.Git(t, h.deps.WorkDir, "config", "remote.origin.url"); got != computed {
		t.Errorf("the checkout's origin = %q, want the URL the run computed for its own repository %q", got, computed)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "feature.txt")); err != nil {
		t.Errorf("the run did not work in the checkout it cloned: %v", err)
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Errorf("remote branch %s != record head %s", got, rec.HeadSHA)
	}
	if !strings.Contains(logs.String(), "is unusable") {
		t.Errorf("the run logged no line about the unusable checkout it removed:\n%s", logs.String())
	}
}

// TestBrokenBakeClonesFresh (CF1, D3440, F26): the tree the image holds is this
// run's repository's and cannot be brought to the state the run works from, so
// the run clones its own rather than ending bootstrap. What is broken is the
// tree and not the remote the run clones from: the tree's origin is this run's
// own, live, and the clone that replaces the tree is made from the URL the run
// computes for its own repository, on a job that names no remote at all
// (noRemoteJob) because a job whose image bakes a checkout is launched with
// none.
//
// Mutation (run, restore): return the fetch error instead of falling back — this
// test fails with the run in infra_error.
func TestBrokenBakeClonesFresh(t *testing.T) {
	for _, tc := range bakedPlaces {
		t.Run(tc.name, func(t *testing.T) {
			h := jobHarness(t, nil)
			var logs bytes.Buffer
			h.deps.Log = runner.NewLogger(&logs)
			computed := noRemoteJob(t, h)
			brokenCheckout(t, tc.at(t, h), h.remote)
			rec, err := h.run(t, implement("feature"), review("ship", 0))
			if err != nil || rec.Status != runstore.StatusSucceeded {
				t.Fatalf("rec = %+v, err = %v: the run did not clone over a tree it could not fetch", rec, err)
			}
			if got := testutil.Git(t, h.deps.WorkDir, "config", "remote.origin.url"); got != computed {
				t.Errorf("the checkout's origin = %q, want the URL the run computed for its own repository %q", got, computed)
			}
			if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "feature.txt")); err != nil {
				t.Errorf("the run did not work in the checkout it cloned: %v", err)
			}
			if got, want := symlinksResolved(t, h.agent.calls[0].Dir), symlinksResolved(t, h.deps.WorkDir); got != want {
				t.Errorf("the run worked in %q, want the checkout it cloned into %q", got, want)
			}
			if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
				t.Errorf("remote branch %s != record head %s", got, rec.HeadSHA)
			}
			if !strings.Contains(logs.String(), "is unusable") {
				t.Errorf("the run logged no line about the unusable checkout it removed:\n%s", logs.String())
			}
		})
	}
}

// TestUsableBakeFetches (CF1): the image holds a checkout of this run's own
// repository, so that is the tree the run works on: it fetches it up to the
// remote's own tip, checks the run's branch out there, and never clones. The
// remote moves on after the image was built, which is what only a fetch brings
// in. The second row carries FUGARO_CHECKOUT=clone, the marker a launcher writes
// for a job whose image holds no checkout: the run ignores it (D3440), because
// what decides is the tree the image holds and never a value written into the
// job's environment.
//
// Mutation (run, restore): have cloneJob (clones) read FUGARO_CHECKOUT again —
// the second row fails: the run clones into the work directory and works there.
func TestUsableBakeFetches(t *testing.T) {
	for _, tc := range []struct{ name, marker string }{
		{"no marker in the job's environment", ""},
		{"a marker that says clone", runner.CheckoutEnv + "=clone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := jobHarness(t, nil)
			var logs bytes.Buffer
			h.deps.Log = runner.NewLogger(&logs)
			if tc.marker != "" {
				h.deps.Env = append(h.deps.Env, tc.marker)
			}
			baked := bakedCheckout(t, bakedDir(t, h, repoSlug), h.remote)
			movedOn(t, h.remote)
			rec, err := h.run(t, implement("feature"), review("ship", 0))
			if err != nil || rec.Status != runstore.StatusSucceeded {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if got, want := symlinksResolved(t, h.agent.calls[0].Dir), symlinksResolved(t, baked); got != want {
				t.Errorf("the run worked in %q, want the checkout the image holds %q", got, want)
			}
			if _, err := os.Stat(filepath.Join(baked, "moved-on.txt")); err != nil {
				t.Errorf("the run did not fetch the checkout the image holds: %v", err)
			}
			if _, err := os.Stat(filepath.Join(h.deps.WorkDir, ".git")); err == nil {
				t.Error("the run cloned into the work directory instead of fetching the checkout the image holds")
			}
			if strings.Contains(logs.String(), "is unusable") {
				t.Errorf("the run treated a usable checkout as unusable:\n%s", logs.String())
			}
			if got := testutil.Git(t, baked, "rev-parse", "--abbrev-ref", "HEAD"); got != "fugaro/"+runID {
				t.Errorf("the image's checkout is on %q, want the run's own branch", got)
			}
			if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
				t.Errorf("remote branch %s != record head %s", got, rec.HeadSHA)
			}
		})
	}
}

// movedOn commits a file on the remote's main branch after the fixture was
// built: the commit a baked checkout does not hold until it fetches.
func movedOn(t *testing.T, remote string) {
	t.Helper()
	seed := filepath.Join(t.TempDir(), "moved-on")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	writeFileAt(t, filepath.Join(seed, "moved-on.txt"), "the remote moved on\n")
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "the remote moves on")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
}

// TestNoBakeClones (CF1, D3440, F35): an image that holds no checkout of this
// run's repository — a shared dependency layer (F35), a prebuilt catalog entry,
// a per-repository job whose entry bakes it nothing — leaves the run with
// nothing to work on, so it clones its own repository and installs the tools the
// checkout declares. The three rows pin that the marker is not what decides: the
// environment says nothing, says clone, or says shared, and the run clones
// either way.
//
// Mutation (run, restore): have cloneJob (clones) read FUGARO_CHECKOUT again —
// the first row fails: no marker, no blobless clone and no `mise install`.
func TestNoBakeClones(t *testing.T) {
	for _, tc := range []struct{ name, marker string }{
		{"no marker at all", ""},
		{"a marker that says clone", runner.CheckoutEnv + "=clone"},
		{"a marker that says shared", runner.CheckoutEnv + "=shared"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := jobHarness(t, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
			log := h.fakeMise(t, 0)
			if tc.marker != "" {
				h.deps.Env = append(h.deps.Env, tc.marker)
			}
			rec, err := h.run(t, implement("feature"), review("ship", 0))
			if err != nil || rec.Status != runstore.StatusSucceeded {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if got := testutil.Git(t, h.deps.WorkDir, "config", "remote.origin.url"); got != h.remote {
				t.Errorf("the checkout's origin = %q, want the job's own remote %q", got, h.remote)
			}
			if v := testutil.Git(t, h.deps.WorkDir, "config", "remote.origin.partialclonefilter"); v != "blob:none" {
				t.Errorf("the checkout's filter = %q, want blob:none", v)
			}
			got := miseCalls(t, log)
			if len(got) == 0 || got[0].Args[0] != "install" {
				t.Errorf("mise calls = %+v, want `mise install` before the first stage", got)
			}
		})
	}
}

// TestPushBindingOnFreshClone (CF1, F39): the binding checkBakedCheckoutOrigin
// holds is not only a baked tree's — it runs on whichever checkout the run uses,
// the clone included, so a checkout that would send the run's branch to another
// repository stops the run even when the run made that checkout itself. A fresh
// clone carries nothing a repository could write into it, so the rewrite this
// test plants is the environment's: a git configuration file (GIT_CONFIG_GLOBAL)
// with a url.<other>.pushInsteadOf rule matching the job's own remote. The run
// refuses before it pushes anything, and nothing lands in the repository the
// rewrite names.
//
// Mutation (run, restore): check the binding before the clone only — this test
// fails: the run opens the fresh clone, pushes the branch through the rewrite
// and succeeds.
func TestPushBindingOnFreshClone(t *testing.T) {
	h := jobHarness(t, nil)
	elsewhere := "file://" + testutil.NewRemote(t, map[string]string{"README.md": "other\n"})
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	rule := "[url \"" + elsewhere + "\"]\n\tpushInsteadOf = " + h.remote + "\n"
	if err := os.WriteFile(cfg, []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err == nil || rec == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v: the run pushed from a checkout bound for another repository", rec, err)
	}
	for _, want := range []string{"acme/app", "not this run's repository", "pushes to file://"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason %q lacks %q", rec.Reason, want)
		}
	}
	if strings.Contains(rec.Reason, "\n") {
		t.Errorf("the reason is not one line: %q", rec.Reason)
	}
	if got := testutil.Git(t, h.remote, "for-each-ref", "refs/heads/fugaro/"); got != "" {
		t.Errorf("the run pushed %q to the repository it is for", got)
	}
}
