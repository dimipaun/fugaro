package runner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// otherRemote is a repository that is not the fixture's (acme/app): the origin
// a baked checkout of another repository carries, and the repository a run that
// opened it would work on and push to.
const otherRemote = "https://github.com/evil/other.git"

// bakedDir makes the directory a shared image lays a repository it baked out
// in, named by the repository's storage slug, beside the work directory
// (runner.go's bakedRepoRoot).
func bakedDir(t *testing.T, h *harness, slug string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(h.deps.WorkDir), "baked", slug)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestBakedCheckoutOriginMismatchRefuses (IS8): the job is for acme/app, and
// the checkout already in the image's work directory is another repository's —
// a shared image built for someone else, or a warm container an earlier job
// left behind. The run must not work on it: the diff would be repo B's source
// committed under repo A's slug and pushed with this run's git credential. It
// stops at bootstrap, before any git credential is requested and before a stage
// runs.
//
// Mutation (run, restore): drop the checkBakedCheckoutOrigin call for the work
// directory's own checkout — the run opens another repository's checkout and
// fails further down (no fugaro.yaml, then a fetch of a remote that does not
// resolve), with a git credential already requested.
func TestBakedCheckoutOriginMismatchRefuses(t *testing.T) {
	h := newHarness(t, "", nil)
	auths := 0
	h.provider.Auth = func(time.Duration) gitprov.GitAuth {
		auths++
		return gitprov.GitAuth{}
	}
	if err := os.MkdirAll(h.deps.WorkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, h.deps.WorkDir, "init", "--quiet", "-b", "main", ".")
	testutil.Git(t, h.deps.WorkDir, "remote", "add", "origin", otherRemote)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err == nil || rec == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v: the run did not refuse another repository's checkout", rec, err)
	}
	for _, want := range []string{otherRemote, "evil/other", "acme/app", "not this run's repository"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason %q lacks %q", rec.Reason, want)
		}
	}
	if strings.Contains(rec.Reason, "\n") {
		t.Errorf("the reason is not one line: %q", rec.Reason)
	}
	if auths != 0 {
		t.Errorf("%d git credential requests were made before the refusal", auths)
	}
	if len(h.agent.calls) != 0 {
		t.Errorf("%d stages ran after the refusal", len(h.agent.calls))
	}
}

// TestBakedCheckoutPerSlugPath (IS8): the image is shared by the project's
// repositories and holds each baked one at its own per-slug directory (D1900:
// a per-repository job runs the project's catalog image, which bakes several),
// this run's repository is one of them, and the work directory holds no
// checkout of its own. The run works in the directory the image baked for its
// own repository: the stages run there, the run's branch is checked out there,
// and the push that ends the run comes from it.
func TestBakedCheckoutPerSlugPath(t *testing.T) {
	h := newHarness(t, "", nil)
	baked := bakedDir(t, h, repoSlug)
	testutil.Git(t, filepath.Dir(baked), "clone", "--quiet", h.remote, baked)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	want, err := filepath.EvalSymlinks(baked) // $PWD is resolved (review L2)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.agent.calls[0].Dir; got != want {
		t.Errorf("the implement stage ran in %q, want the image's baked checkout %q", got, want)
	}
	if got := testutil.Git(t, baked, "rev-parse", "--abbrev-ref", "HEAD"); got != "fugaro/"+runID {
		t.Errorf("the baked checkout is on %q, want the run's own branch", got)
	}
	if _, err := os.Stat(filepath.Join(baked, "feature.txt")); err != nil {
		t.Errorf("the baked checkout has no feature.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, ".git")); err == nil {
		t.Error("the run cloned into the work directory as well as using the baked checkout")
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Errorf("remote branch %s != record head %s", got, rec.HeadSHA)
	}
}

// TestBakedMissingFallsBackToClone (IS8): the image's per-slug layout holds
// another repository's checkout and none for this run's repository, so nothing
// is baked for this run to work on: it clones its own remote into the work
// directory, and the other repository's checkout is left exactly as it was.
func TestBakedMissingFallsBackToClone(t *testing.T) {
	h := newHarness(t, "", nil)
	other := bakedDir(t, h, mustSlug("github", "evil/other"))
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", h.remote, other)
	// The other repository's own origin, so a run that opened this checkout
	// would be refused rather than quietly succeed in the wrong directory.
	testutil.Git(t, other, "remote", "set-url", "origin", otherRemote)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := testutil.Git(t, h.deps.WorkDir, "config", "remote.origin.url"); got != h.remote {
		t.Errorf("the checkout's origin = %q, want the job's own remote %q", got, h.remote)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "feature.txt")); err != nil {
		t.Errorf("the run did not work in the checkout it cloned: %v", err)
	}
	if got := testutil.Git(t, other, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("the other repository's baked checkout is on %q, want it left as it was (main)", got)
	}
	if got := testutil.Git(t, other, "branch", "--list", "fugaro/"+runID); got != "" {
		t.Errorf("the other repository's baked checkout holds the run's branch: %q", got)
	}
}
