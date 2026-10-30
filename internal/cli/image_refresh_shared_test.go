package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// This file is the shared-image half of this run's CLI tests: the launch
// gate a shared job's image needs (recipes_skew.go's sharedSince), whose
// judge is the lock — the one record of which image a repository's job
// points at (shared-images.md §3.1, §6).

// lockWith writes a lock holding one entry: the image of repository repo,
// built from baseRef, on the job slug's workflow names. It is the state a
// refresh leaves behind (§3.3), written by hand so the launch gate can be
// judged without a refresh.
func lockWith(t *testing.T, f *cloudFixture, key, baseRef, repo, slug, workflow string) {
	t.Helper()
	lock := imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{
		key: {
			Key: key, Image: "us-east5-docker.pkg.dev/proj-1234/fugaro-shared-0123456789ab/img-" + key[:12],
			Digest: "sha256:" + strings.Repeat("ab", 32), BaseRef: baseRef, BaseDigest: "sha256:" + strings.Repeat("cd", 32),
			Packages: []string{"left-pad@1.3.0"}, Tools: []string{"node@22.11.0"},
			BuiltAt: time.Date(2026, 10, 29, 9, 12, 4, 0, time.UTC), BuildID: "build-1",
			Repositories: []string{repo}, Jobs: map[string]string{repo: gcp.JobName(slug, workflow)},
			LastUsedAt: time.Date(2026, 10, 29, 9, 40, 11, 0, time.UTC),
		},
	}}
	data, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, imagecheck.LockKey, string(data))
}

// A checkout: shared workflow's job runs a shared dependency-layer image
// (shared-images.md §6, §12): a source-less image whose runner must be one
// that clones for shared, which is sharedSince. The judge is the lock entry
// the repository's job is on — a launch cannot compute the key (§2.5: the
// key is the operator's machine's fact), but the lock says which image each
// repository is on, so the launcher reads that. An image older than
// sharedSince is refused with the remedy (move the installation's base and
// refresh the repository); a base with no release tag only warns, as every
// other *Since gate does; a repository the lock holds no entry for — one
// that has not migrated yet (§10) — is not judged at all.
func TestLaunchRefusesSharedJobOnOldRunner(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	ctx := context.Background()
	spec := &task.Spec{Version: 1, RunID: "20261030-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	const key = "9f2c1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7"

	check := func() (string, error) {
		var warn bytes.Buffer
		err := checkSharedImageSince(ctx, env, appSlug, spec, sharedSince, &warn)
		return warn.String(), err
	}
	if warn, err := check(); err != nil || warn != "" {
		t.Fatalf("no lock at all (a repository that has not migrated): %v %q", err, warn)
	}
	lockWith(t, f, key, "ghcr.io/dimipaun/fugaro-base:0.13.0", "acme/app", appSlug, "web")
	warn, err := check()
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a shared image built from release 0.13.0: err = %v, want refused", err)
	}
	for _, want := range []string{
		"fugaro " + sharedSince + " or later",
		"acme/app workflow web",
		"ghcr.io/dimipaun/fugaro-base:0.13.0",
		"release 0.13.0",
		"fugaro update --base base",
		"fugaro image refresh --repo acme/app --workflow web",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if warn != "" {
		t.Errorf("a refusal also warns: %q", warn)
	}
	// sharedSince itself, and a release past it, pass in silence.
	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + sharedSince, "ghcr.io/dimipaun/fugaro-base:0.15.0"} {
		lockWith(t, f, key, ref, "acme/app", appSlug, "web")
		if warn, err := check(); err != nil || warn != "" {
			t.Fatalf("%s: %v %q", ref, err, warn)
		}
	}
	// A dev or hand-pushed base: nothing can tell what its runner knows, so
	// it is warned about and let through, like every other *Since gate here.
	lockWith(t, f, key, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", "acme/app", appSlug, "web")
	warn, err = check()
	if err != nil {
		t.Fatalf("a dev base: %v, want warned about, not refused", err)
	}
	for _, want := range []string{"dev-1a2b3c", "not a release base image"} {
		if !strings.Contains(warn, want) {
			t.Errorf("warning %q lacks %q", warn, want)
		}
	}
	// Another repository's image, and another workflow's job, are not this
	// launch's: the gate judges the job this workflow would run, and no other.
	lockWith(t, f, key, "ghcr.io/dimipaun/fugaro-base:0.13.0", "acme/other", appSlug, "web")
	if warn, err := check(); err != nil || warn != "" {
		t.Fatalf("another repository's image: %v %q", err, warn)
	}
	lockWith(t, f, key, "ghcr.io/dimipaun/fugaro-base:0.13.0", "acme/app", appSlug, "api")
	if warn, err := check(); err != nil || warn != "" {
		t.Fatalf("another workflow's job: %v %q", err, warn)
	}
	// A lock this fugaro cannot read is not a licence to launch: the lock is
	// decoded strictly precisely so that a reader which does not know it stops
	// (§3.2), and this gate says so rather than judge nothing.
	writeBucketFile(t, f, imagecheck.LockKey, `{"version":1,"images":{},"unknown":true}`)
	if _, err := check(); err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "the image lock") {
		t.Fatalf("an unreadable lock: err = %v, want refused naming the lock", err)
	}
}

// TestRunSharedWorkflowRefusedBelowSharedSince is the same gate driven the
// way an operator meets it: a real `fugaro run` of a checkout: shared
// workflow, from its checkout, refused before anything is written. It is
// what holds internal/cli/run.go's call to the gate and the checkout kind it
// reads — drop either the `shared` assignment or the `if shared { … }` block
// and the launch below goes through, because nothing else refuses a shared
// workflow on this image.
func TestRunSharedWorkflowRefusedBelowSharedSince(t *testing.T) {
	f := newCloudFixture(t)
	testutil.IsolateGit(t)
	other := mustSlug("github", "acme/other")
	f.run.AddJob(gcp.JobName(other, "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n" +
		"  svc: { checkout: shared, commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)
	const key = "9f2c1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7"
	lockWith(t, f, key, "ghcr.io/dimipaun/fugaro-base:0.13.0", "acme/other", other, "svc")
	_, _, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "how to clone for a shared image (FUGARO_CHECKOUT) (fugaro "+sharedSince+" or later)") ||
		!strings.Contains(err.Error(), "acme/other workflow svc was built from base image ghcr.io/dimipaun/fugaro-base:0.13.0, release 0.13.0") ||
		!strings.Contains(err.Error(), "fugaro update --base base") ||
		!strings.Contains(err.Error(), "fugaro image refresh --repo acme/other --workflow svc") {
		t.Fatalf("err = %v", err)
	}
	// The same launch on an image built from sharedSince: nothing to refuse,
	// which is what says the refusal above is this gate's and not a launch
	// that would have failed anyway.
	lockWith(t, f, key, "ghcr.io/dimipaun/fugaro-base:"+sharedSince, "acme/other", other, "svc")
	if _, _, err := execute(t, "run", "--run-id", "20261030-100000-abce", "A task"); err != nil {
		t.Fatalf("a shared image at %s: %v", sharedSince, err)
	}
}

// TestSharedSinceHasAReleaseBullet: sharedSince names 0.14.0, which is not
// tagged, so docs/release.md's "Before you tag" list is the only thing that
// holds the number to the release actually cut — the same job every other
// *Since constant's bullet does.
func TestSharedSinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `sharedSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no sharedSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+sharedSince+"\"`") {
		t.Errorf("docs/release.md's sharedSince bullet does not carry the constant's release (%q)", sharedSince)
	}
}
