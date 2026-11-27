package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
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
			Packages: []string{"\"left-pad\" \"1.3.0\" \"\""}, Tools: []string{"node@22.11.0"},
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
		err := checkSharedImageSince(ctx, env, appSlug, spec, sharedSince,
			"this checkout: shared workflow", "how to clone for a shared image (FUGARO_CHECKOUT)", &warn)
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

// TestRunRefusesSharedWorkflowUntilTheImagesShip is the same repository
// driven the way an operator meets it, and it stops before the gate above:
// validation refuses checkout: shared until the release that builds a
// shared image (config/image.go's sharedCheckoutShipped), so the launch has
// no workflow to read the kind from and never starts a job — not even on an
// image built from sharedSince, which is what this test's lock holds. What
// it pins is the refusal a person is owed, and the key it is about: fugaro
// validate reads the same file and prints both, where the launch stops on
// the refused file itself (it leaves no config to read the workflow's kind
// from). The sharedSince gate keeps its own test above, which a launch
// cannot reach until the flag flips.
func TestRunRefusesSharedWorkflowUntilTheImagesShip(t *testing.T) {
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
	lockWith(t, f, key, "ghcr.io/dimipaun/fugaro-base:"+sharedSince, "acme/other", other, "svc")
	out, errOut, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a run of a checkout: shared workflow = %v, want refused", err)
	}
	if want := "workflows.svc.checkout: " + config.SharedCheckoutRefusal; !strings.Contains(err.Error(), want) {
		t.Errorf("the launch's refusal = %v, want it to carry %q, the sentence fugaro validate prints, not whatever later step trips over the refused file", err, want)
	}
	if n := len(f.run.Requests()); n != 0 {
		t.Errorf("the launch made %d call(s) to the Cloud Run API, want none: a shared workflow's fugaro.yaml is refused before anything is started", n)
	}
	out, errOut, err = execute(t, "validate")
	if err == nil {
		t.Fatalf("fugaro validate accepted a checkout: shared workflow:\n%s%s", out, errOut)
	}
	for _, want := range []string{"workflows.svc.checkout", config.SharedCheckoutRefusal} {
		if !strings.Contains(out+errOut+err.Error(), want) {
			t.Errorf("the refusal does not carry %q:\nstdout:\n%s\nstderr:\n%s\n%v", want, out, errOut, err)
		}
	}
}

// TestRunRefusesSharedCheckoutWhenLocalConfigNamesTheRepo is the launch a
// set-up repository makes: its local config entry gives the provider and the
// workflow, so nothing the launch needs comes from the refused fugaro.yaml,
// and without the refusal it started a job the runner would refuse at
// bootstrap. The --pr path reads the same checkout and is held to the same
// sentence.
func TestRunRefusesSharedCheckoutWhenLocalConfigNamesTheRepo(t *testing.T) {
	f := newCloudFixture(t)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\nworkflows:\n" +
		"  web: { checkout: shared, commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)
	want := "workflows.web.checkout: " + config.SharedCheckoutRefusal
	for _, args := range [][]string{
		{"run", "A task"},
		{"run", "--pr", "7", "A follow-up"},
	} {
		_, _, err := execute(t, args...)
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), want) {
			t.Errorf("%v = %v, want refused with %q", args, err, want)
		}
	}
	if n := len(f.run.Requests()); n != 0 {
		t.Errorf("the launches made %d call(s) to the Cloud Run API, want none", n)
	}
}

// TestSharedSinceHasAReleaseBullet: sharedSince names 0.14.0, which is not
// tagged, so docs/release.md's "Before you tag" list is the only thing that
// holds the number to the release actually cut — the same job every other
// *Since constant's bullet does. It also holds the bullet to the second
// release this feature has: sharedSince is the release whose *runner* clones
// for a shared job, while the image such a job runs is
// config.SharedCheckoutSince's (0.15.0), and a bullet that calls 0.14.0 the
// release the shared images ship in would have an operator verify the wrong
// number. That second number is stale: 0.15.0 shipped without building or
// refreshing a shared image, and no release does — what a repository reads
// names no release at all (config/image.go's SharedCheckoutRefusal), and
// the release that ships these images is the one that moves the constant
// and this bullet together.
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
	if sharedSince == config.SharedCheckoutSince {
		t.Fatalf("sharedSince and config.SharedCheckoutSince are both %q: the runner release and the image release are two facts", sharedSince)
	}
	for _, want := range []string{"the release whose runner clones", config.SharedCheckoutSince} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's sharedSince bullet does not say %q: the bullet has to name both releases this feature has, and the image is %s's", want, config.SharedCheckoutSince)
		}
	}
}

// --- the refresh's shared half (§3.3, §4, §5, §6) -------------------------
//
// These drive sharedRefresh directly, through sharedCloud, because no
// repository can declare `checkout: shared` until the images ship
// (config.SharedCheckoutRefusal): the order, the refusals and the lock writes
// are decided in the engine, and a fake decides the three things outside it —
// the lock, one build, one job.

// sharedFake is the shared refresh's outside world, in memory: the lock, the
// builds a key cost, and the moves this repository's job was given.
type sharedFake struct {
	lock  *imagecheck.Lock
	built []sharedBuild
	moved []string
	// onBuild, when set, replaces the build: the digest it returns is what the
	// lock records. A build of a key the lock already holds is a build that
	// never happens (§3.3), so a test that counts builds detects it here.
	onBuild func(sharedBuild) (sharedBuilt, error)
	// beforeWrite, when set, runs inside the lock's write, before the plan: it
	// is the other operator's machine arriving between this refresh's read and
	// its write (§3.4).
	beforeWrite func(*imagecheck.Lock)
	writes      int
}

func (f *sharedFake) ReadLock(context.Context) (*imagecheck.Lock, error) { return f.lock, nil }

func (f *sharedFake) WriteLock(_ context.Context, plan func(*imagecheck.Lock) (*imagecheck.Lock, error)) error {
	if f.beforeWrite != nil {
		f.beforeWrite(f.lock)
		f.beforeWrite = nil
	}
	// imagecheck.Update hands the plan a lock, never a nil one: absence is an
	// empty object, so the plan has one shape to write.
	if f.lock == nil {
		f.lock = &imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{}}
	}
	next, err := plan(f.lock)
	if err != nil || next == nil {
		return err
	}
	f.lock = next
	f.writes++
	return nil
}

func (f *sharedFake) Build(_ context.Context, b sharedBuild) (sharedBuilt, error) {
	f.built = append(f.built, b)
	if f.onBuild != nil {
		return f.onBuild(b)
	}
	return sharedBuilt{Digest: "sha256:" + strings.Repeat("11", 32), BuildID: "build-1"}, nil
}

func (f *sharedFake) MoveJob(_ context.Context, workflow, image string) error {
	f.moved = append(f.moved, workflow+" "+image)
	return nil
}

func (f *sharedFake) Close() error { return nil }

// sharedFact is one package fact as the key files and the lock write it: three
// quoted literals.
func sharedFact(name, version string) string {
	return "\"" + name + "\" \"" + version + "\" \"sha512-" + name + "\""
}

// sharedSet is a resolved dependency set of one workflow, with the base §2.1
// keys on.
func sharedSet(pkgs ...string) imagecheck.Resolved {
	return imagecheck.Resolved{
		BaseRef: "ghcr.io/acme/fugaro-base:0.15.0", BaseDigest: "sha256:" + strings.Repeat("cd", 32),
		Packages: pkgs, Tools: []string{"node@22.11.0"},
	}
}

// lockEntry is one entry of the lock: an image built for set, with the
// repositories and jobs named, as §3.1 writes them.
func lockEntry(t *testing.T, key string, set imagecheck.Resolved, digest string, repos map[string]string) imagecheck.LockedImage {
	t.Helper()
	name, err := gcp.SharedImageName("us-east5-docker.pkg.dev/proj-1234", key)
	if err != nil {
		t.Fatal(err)
	}
	return imagecheck.LockedImage{
		Key: key, Image: name, Digest: digest,
		BaseRef: set.BaseRef, BaseDigest: set.BaseDigest,
		Packages: slices.Clone(set.Packages), Tools: slices.Clone(set.Tools),
		BuiltAt: time.Date(2026, 10, 29, 9, 12, 4, 0, time.UTC), BuildID: "build-1",
		Repositories: slices.Sorted(maps.Keys(repos)), Jobs: repoJobs(repos),
		LastUsedAt: time.Date(2026, 10, 29, 9, 40, 11, 0, time.UTC),
	}
}

// repoJobs copies slug → job into the map the lock holds, under the repository
// names themselves.
func repoJobs(repos map[string]string) map[string]string {
	out := map[string]string{}
	for repo, job := range repos {
		out[repo] = job
	}
	return out
}

// sharedRun is one workflow of one repository on its way onto a shared image,
// with the fake behind it.
func sharedRun(t *testing.T, f *sharedFake, repo, workflow, job string, set imagecheck.Resolved, choice onDivergence) (*sharedRefresh, *bytes.Buffer) {
	t.Helper()
	name, err := gcp.SharedImageName("us-east5-docker.pkg.dev/proj-1234", imagecheck.Key(set))
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	s := &sharedRefresh{
		repo: repo, workflow: workflow, job: job,
		key: imagecheck.Key(set), image: name, resolved: set, choice: choice,
		cloud: f, out: out, detail: io.Discard, warn: func(string) {},
		now: func() time.Time { return time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC) },
	}
	return s, out
}

// TestRefreshBuildsOnceForSharedKey is C8b's red step: two repositories whose
// checkouts resolve the same dependency set. The first builds the image for
// the key; the second finds the key in the lock and adopts it, so one set
// costs one build (§1, §3.3).
func TestRefreshBuildsOnceForSharedKey(t *testing.T) {
	set := sharedSet(sharedFact("react", "18.3.1"), sharedFact("left-pad", "1.3.0"))
	f := &sharedFake{}
	a, _ := sharedRun(t, f, "acme/app", "web", "fugaro-app-web", set, onDivergence{})
	if err := a.run(context.Background()); err != nil {
		t.Fatalf("the first repository's refresh: %v", err)
	}
	if len(f.built) != 1 {
		t.Fatalf("the first refresh built %d images, want 1", len(f.built))
	}
	if f.built[0].Key != a.key {
		t.Errorf("the build was for key %s, want the checkout's own %s", shortKey(f.built[0].Key), shortKey(a.key))
	}
	if got := len(f.lock.Images); got != 1 {
		t.Fatalf("the lock holds %d entries after one build, want 1", got)
	}
	entry := f.lock.Images[a.key]
	if !slices.Equal(entry.Packages, set.Packages) {
		t.Errorf("the lock recorded %v, want the set that was resolved %v", entry.Packages, set.Packages)
	}
	if entry.Jobs["acme/app"] != "fugaro-app-web" {
		t.Errorf("the lock records %q for acme/app's job, want fugaro-app-web", entry.Jobs["acme/app"])
	}
	if len(f.moved) != 1 || !strings.HasSuffix(f.moved[0], "@"+entry.Digest) {
		t.Errorf("the job moved to %v, want one move onto %s", f.moved, digestRef(entry))
	}

	// The second repository, the identical set: the lock already holds the
	// key, so it adopts and nothing is built a second time.
	b, _ := sharedRun(t, f, "acme/tool", "web", "fugaro-tool-web", set, onDivergence{})
	if err := b.run(context.Background()); err != nil {
		t.Fatalf("the second repository's refresh: %v", err)
	}
	if len(f.built) != 1 {
		t.Fatalf("the second refresh built %d images in all, want 1: repositories with one resolved set share one image", len(f.built))
	}
	if got := len(f.moved); got != 2 {
		t.Fatalf("the job moves are %v, want a second one", f.moved)
	}
	if got := f.moved[1]; !strings.HasSuffix(got, "@"+entry.Digest) {
		t.Errorf("the second repository's job moved to %q, want the digest the lock holds (%s)", got, digestRef(entry))
	}
	if f.lock.Images[a.key].Jobs["acme/tool"] != "fugaro-tool-web" {
		t.Errorf("the lock records %v, want acme/tool's job on the key too", f.lock.Images[a.key].Jobs)
	}
}

// TestRefreshForksOnChange: a repository on an image that does not hold the
// set its checkout now resolves, with the choice fork (§5): a new image for
// the new key, this repository's job on it, and the entry it left naming no
// job — which is what §9's clean-up reads.
func TestRefreshForksOnChange(t *testing.T) {
	old := sharedSet(sharedFact("react", "18.2.0"))
	next := sharedSet(sharedFact("react", "18.3.1"))
	oldKey := imagecheck.Key(old)
	f := &sharedFake{lock: &imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{
		oldKey: lockEntry(t, oldKey, old, "sha256:"+strings.Repeat("ab", 32), map[string]string{"acme/app": "fugaro-app-web", "acme/tool": "fugaro-tool-web"}),
	}}}
	s, out := sharedRun(t, f, "acme/app", "web", "fugaro-app-web", next, onDivergence{fork: true})
	if err := s.run(context.Background()); err != nil {
		t.Fatalf("the fork: %v", err)
	}
	if len(f.built) != 1 || f.built[0].Key != imagecheck.Key(next) {
		t.Fatalf("the fork built %v, want one build of the new key", f.built)
	}
	if len(out.String()) != 0 {
		t.Errorf("the fork printed %q, want nothing: the choice was named, not asked for", out.String())
	}
	newKey := imagecheck.Key(next)
	entry, ok := f.lock.Images[newKey]
	if !ok {
		t.Fatalf("the lock holds %v, want the new key %s", slices.Sorted(maps.Keys(f.lock.Images)), shortKey(newKey))
	}
	if entry.Jobs["acme/app"] != "fugaro-app-web" {
		t.Errorf("the new entry records %v, want acme/app's job", entry.Jobs)
	}
	left := f.lock.Images[oldKey]
	if _, named := left.Jobs["acme/app"]; named {
		t.Errorf("the entry it left still names acme/app's job: %v", left.Jobs)
	}
	if !slices.Contains(left.Repositories, "acme/tool") {
		t.Errorf("the entry it left holds %v, want the sibling repository untouched", left.Repositories)
	}
	if _, named := left.Jobs["acme/tool"]; !named {
		t.Errorf("the entry it left dropped the sibling's job: %v", left.Jobs)
	}
	if got := f.moved[0]; !strings.HasSuffix(got, "@"+entry.Digest) {
		t.Errorf("the job moved to %q, want the new entry's digest %s", got, digestRef(entry))
	}
}

// TestRefreshNeverOverwritesALockedDigest: an image already in the lock is
// never rebuilt and its digest is never written over, whichever machine got
// there first (§3.3, §3.4).
func TestRefreshNeverOverwritesALockedDigest(t *testing.T) {
	set := sharedSet(sharedFact("react", "18.3.1"))
	key := imagecheck.Key(set)
	stored := "sha256:" + strings.Repeat("ab", 32)

	f := &sharedFake{lock: &imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{
		key: lockEntry(t, key, set, stored, map[string]string{"acme/tool": "fugaro-tool-web"}),
	}}}
	s, _ := sharedRun(t, f, "acme/app", "web", "fugaro-app-web", set, onDivergence{})
	if err := s.run(context.Background()); err != nil {
		t.Fatalf("the adopting refresh: %v", err)
	}
	if len(f.built) != 0 {
		t.Fatalf("a key the lock already holds was built %d times, want none", len(f.built))
	}
	if got := f.lock.Images[key].Digest; got != stored {
		t.Fatalf("the digest became %q, want the locked %q", got, stored)
	}
	if got := f.moved[0]; !strings.HasSuffix(got, "@"+stored) {
		t.Errorf("the job moved to %q, want the locked digest", got)
	}

	// The other machine's entry, arriving between this refresh's read and its
	// write: its digest is the one that stays, and this build's is not written
	// over it.
	theirs := "sha256:" + strings.Repeat("ef", 32)
	raced := &sharedFake{}
	raced.beforeWrite = func(l *imagecheck.Lock) {
		raced.lock = &imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{
			key: lockEntry(t, key, set, theirs, map[string]string{"acme/tool": "fugaro-tool-web"}),
		}}
	}
	r, _ := sharedRun(t, raced, "acme/app", "web", "fugaro-app-web", set, onDivergence{})
	if err := r.run(context.Background()); err != nil {
		t.Fatalf("the raced refresh: %v", err)
	}
	if got := raced.lock.Images[key].Digest; got != theirs {
		t.Errorf("the raced write left digest %q, want the other machine's %q", got, theirs)
	}
	if got := raced.moved[0]; !strings.HasSuffix(got, "@"+theirs) {
		t.Errorf("the raced job moved to %q, want the other machine's digest", got)
	}
}

// TestRefreshUpdatesOnlyTheChangedRepositoryJob: an adopt moves this
// repository's job and no other (§6), and the entry's other repositories keep
// the image and the job they had.
func TestRefreshUpdatesOnlyTheChangedRepositoryJob(t *testing.T) {
	set := sharedSet(sharedFact("react", "18.3.1"))
	key := imagecheck.Key(set)
	f := &sharedFake{lock: &imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{
		key: lockEntry(t, key, set, "sha256:"+strings.Repeat("ab", 32), map[string]string{"acme/tool": "fugaro-tool-web", "acme/site": "fugaro-site-web"}),
	}}}
	s, _ := sharedRun(t, f, "acme/app", "web", "fugaro-app-web", set, onDivergence{})
	if err := s.run(context.Background()); err != nil {
		t.Fatalf("the refresh: %v", err)
	}
	if len(f.moved) != 1 || !strings.HasPrefix(f.moved[0], "web ") {
		t.Fatalf("the moves are %v, want exactly this repository's workflow", f.moved)
	}
	jobs := f.lock.Images[key].Jobs
	if jobs["acme/tool"] != "fugaro-tool-web" || jobs["acme/site"] != "fugaro-site-web" {
		t.Errorf("the sibling jobs are %v, want tool and site where they were", jobs)
	}
	if jobs["acme/app"] != "fugaro-app-web" {
		t.Errorf("this repository's job is %v, want it on the entry", jobs)
	}
}

// TestDivergenceProposesAndWaits: a repository on an image that does not hold
// its set, named no choice: the proposal is printed with the facts the
// decision needs and nothing is built, written or moved (§5).
func TestDivergenceProposesAndWaits(t *testing.T) {
	old := sharedSet(sharedFact("react", "18.2.0"), sharedFact("left-pad", "1.3.0"))
	next := sharedSet(sharedFact("react", "18.3.1"))
	oldKey := imagecheck.Key(old)
	// acme/tool is on an image that holds acme/app's new set with one extra
	// package, so the proposal has a candidate to print.
	toolSet := sharedSet(sharedFact("react", "18.3.1"), sharedFact("lodash", "4.17.21"))
	toolKey := imagecheck.Key(toolSet)
	f := &sharedFake{lock: &imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{
		oldKey:  lockEntry(t, oldKey, old, "sha256:"+strings.Repeat("ab", 32), map[string]string{"acme/app": "fugaro-app-web"}),
		toolKey: lockEntry(t, toolKey, toolSet, "sha256:"+strings.Repeat("ef", 32), map[string]string{"acme/tool": "fugaro-tool-web"}),
	}}}
	s, out := sharedRun(t, f, "acme/app", "web", "fugaro-app-web", next, onDivergence{})
	err := s.run(context.Background())
	if err == nil {
		t.Fatal("the divergence built something, want a refusal until a choice is named")
	}
	if got := ExitCode(err); got != 1 {
		t.Errorf("the unanswered proposal exits %d, want 1", got)
	}
	if len(f.built) != 0 || f.writes != 0 || len(f.moved) != 0 {
		t.Errorf("the unanswered proposal built %d, wrote %d and moved %d, want nothing", len(f.built), f.writes, len(f.moved))
	}
	text := out.String()
	for _, want := range []string{
		"web: its image does not hold the set this checkout resolves",
		"key " + shortKey(oldKey),
		"key " + shortKey(s.key),
		"0 added, 1 removed, 1 at another version",
		"acme/tool (key " + shortKey(toolKey) + ", +1 packages)",
		"--on-divergence=fork",
		"--on-divergence=adopt=<repository>",
		"--on-divergence=pin",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the proposal does not say %q:\n%s", want, text)
		}
	}
}

// TestDivergenceNonInteractiveRefusesWithoutFlag: the refusal an answered-by-
// nothing divergence gets. This CLI asks nothing, so there is no terminal to
// read from and no prompt to answer: the flag is the whole of the interface,
// and the refusal names it.
func TestDivergenceNonInteractiveRefusesWithoutFlag(t *testing.T) {
	old := sharedSet(sharedFact("react", "18.2.0"))
	next := sharedSet(sharedFact("react", "18.3.1"))
	oldKey := imagecheck.Key(old)
	f := &sharedFake{lock: &imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{
		oldKey: lockEntry(t, oldKey, old, "sha256:"+strings.Repeat("ab", 32), map[string]string{"acme/app": "fugaro-app-web"}),
	}}}
	s, _ := sharedRun(t, f, "acme/app", "web", "fugaro-app-web", next, onDivergence{})
	err := s.run(context.Background())
	if err == nil {
		t.Fatal("the divergence proceeded with no choice named")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--on-divergence") || !strings.Contains(msg, "acme/app") || !strings.Contains(msg, "web") {
		t.Errorf("the refusal is %q, want it to name the flag, the repository and the workflow", msg)
	}
	if strings.Contains(msg, "?") {
		t.Errorf("the refusal asks a question: %q", msg)
	}
	if len(f.built) != 0 || f.writes != 0 || len(f.moved) != 0 {
		t.Errorf("the refused divergence built %d, wrote %d and moved %d, want nothing", len(f.built), f.writes, len(f.moved))
	}
}

// TestDivergenceFlagNamesTheChoice: the three choices, spelled as §5 spells
// them, decide what happens where the proposal would have stopped.
func TestDivergenceFlagNamesTheChoice(t *testing.T) {
	for _, c := range []struct {
		flag string
		want onDivergence
	}{
		{"fork", onDivergence{fork: true}},
		{"pin", onDivergence{pin: true}},
		{"adopt=acme/tool", onDivergence{adopt: "acme/tool"}},
		// The repository is canonicalised the way task.CanonicalRepo writes it,
		// which is the spelling the lock's own jobs map is keyed by.
		{"adopt=Acme/Tool", onDivergence{adopt: "acme/tool"}},
		{"", onDivergence{}},
	} {
		got, err := parseOnDivergence(c.flag)
		if err != nil {
			t.Errorf("--on-divergence=%s: %v", c.flag, err)
			continue
		}
		if got != c.want {
			t.Errorf("--on-divergence=%s parsed to %+v, want %+v", c.flag, got, c.want)
		}
	}
	for _, bad := range []string{"adopt=", "adopt", "keep", "Fork", "adopt=not a repo", "fork,pin"} {
		if _, err := parseOnDivergence(bad); err == nil {
			t.Errorf("--on-divergence=%s was accepted, want a refusal naming the three choices", bad)
		} else if got := ExitCode(err); got != 1 {
			t.Errorf("--on-divergence=%s exits %d, want 1", bad, got)
		}
	}

	// adopt names a repository whose image holds the whole set: this
	// repository moves onto it and nothing is built.
	set := sharedSet(sharedFact("react", "18.3.1"))
	held := sharedSet(sharedFact("react", "18.3.1"), sharedFact("lodash", "4.17.21"))
	heldKey := imagecheck.Key(held)
	old := sharedSet(sharedFact("react", "18.2.0"))
	oldKey := imagecheck.Key(old)
	f := &sharedFake{lock: &imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{
		oldKey:  lockEntry(t, oldKey, old, "sha256:"+strings.Repeat("ab", 32), map[string]string{"acme/app": "fugaro-app-web"}),
		heldKey: lockEntry(t, heldKey, held, "sha256:"+strings.Repeat("ef", 32), map[string]string{"acme/tool": "fugaro-tool-web"}),
	}}}
	s, _ := sharedRun(t, f, "acme/app", "web", "fugaro-app-web", set, onDivergence{adopt: "acme/tool"})
	if err := s.run(context.Background()); err != nil {
		t.Fatalf("the adopt: %v", err)
	}
	if len(f.built) != 0 {
		t.Fatalf("the adopt built %d images, want none", len(f.built))
	}
	if got := f.moved[0]; !strings.HasSuffix(got, "@"+f.lock.Images[heldKey].Digest) {
		t.Errorf("the job moved to %q, want acme/tool's digest", got)
	}
	if _, named := f.lock.Images[oldKey].Jobs["acme/app"]; named {
		t.Errorf("the entry it left still names this repository's job: %v", f.lock.Images[oldKey].Jobs)
	}

	// A repository whose image does not hold the set is refused by name, and
	// the refusal says what the choice may name (§5's exit code 1).
	bad := &sharedFake{lock: &imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{
		oldKey: lockEntry(t, oldKey, old, "sha256:"+strings.Repeat("ab", 32), map[string]string{"acme/app": "fugaro-app-web"}),
	}}}
	b, _ := sharedRun(t, bad, "acme/app", "web", "fugaro-app-web", set, onDivergence{adopt: "acme/gone"})
	err := b.run(context.Background())
	if err == nil {
		t.Fatal("adopting a repository the lock does not hold was accepted")
	}
	if got := ExitCode(err); got != 1 {
		t.Errorf("the refusal exits %d, want 1", got)
	}
	if len(bad.built) != 0 || len(bad.moved) != 0 {
		t.Errorf("the refused adopt built %d and moved %d, want nothing", len(bad.built), len(bad.moved))
	}
}

// TestStayPinnedWarns: pin keeps the image the repository is on, digest
// unchanged, moves nothing and builds nothing, and warns (§5).
func TestStayPinnedWarns(t *testing.T) {
	old := sharedSet(sharedFact("react", "18.2.0"))
	next := sharedSet(sharedFact("react", "18.3.1"))
	oldKey := imagecheck.Key(old)
	stored := "sha256:" + strings.Repeat("ab", 32)
	f := &sharedFake{lock: &imagecheck.Lock{Version: imagecheck.LockVersion, Images: map[string]imagecheck.LockedImage{
		oldKey: lockEntry(t, oldKey, old, stored, map[string]string{"acme/app": "fugaro-app-web"}),
	}}}
	s, _ := sharedRun(t, f, "acme/app", "web", "fugaro-app-web", next, onDivergence{pin: true})
	var warned []string
	s.warn = func(msg string) { warned = append(warned, msg) }
	if err := s.run(context.Background()); err != nil {
		t.Fatalf("the pin: %v", err)
	}
	if len(warned) != 1 {
		t.Fatalf("the pin warned %d times, want one", len(warned))
	}
	if !strings.Contains(warned[0], "web") || !strings.Contains(warned[0], shortKey(oldKey)) {
		t.Errorf("the warning is %q, want it to name the workflow and the key it stays on", warned[0])
	}
	if len(f.built) != 0 || f.writes != 0 || len(f.moved) != 0 {
		t.Errorf("the pin built %d, wrote %d and moved %d, want nothing", len(f.built), f.writes, len(f.moved))
	}
	if got := f.lock.Images[oldKey].Digest; got != stored {
		t.Errorf("the pinned entry's digest became %q, want %q", got, stored)
	}
	if got := f.lock.Images[oldKey].Jobs["acme/app"]; got != "fugaro-app-web" {
		t.Errorf("the pinned job is %q, want the job it had", got)
	}
}
