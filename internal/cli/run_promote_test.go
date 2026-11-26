package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// areaPromoteYAML is the two areas a promote launch raises — `payments` at
// critical, `ledger` at pro, over a default of poc — so a promote run can raise
// (ledger to critical), stand equal (payments at critical) or be refused for
// going below (payments at pro). It is the block a repository's file writes
// before the lift, and since S1 (D3237) the launch reads it where it resolves
// now: as the project layer the fixture's file is anchored to (publishFilePins).
const areaPromoteYAML = "rigor:\n  default: poc\n  areas:\n" +
	"    - { name: payments, match: \"internal/payments/**\", mode: critical }\n" +
	"    - { name: ledger, match: \"internal/ledger/**\", mode: pro }\n"

// areaPromoteWorkflows is the workflow a launch of the fixture's checkout
// resolves: one of the two the installation's config admits.
const areaPromoteWorkflows = "workflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// areaPromoteFixture is a launch of acme/app from a checkout whose fugaro.yaml
// writes those areas, on a job image whose runner reads the promote block — the
// release this work ships in, since an older one refuses the stored task at
// bootstrap (TestRunPromoteRefusesAJobImageThatPredatesTheBlock).
func areaPromoteFixture(t *testing.T) *cloudFixture {
	t.Helper()
	return areaPromoteFixtureOn(t, "ghcr.io/dimipaun/fugaro-base:"+promoteSince)
}

// areaPromoteFixtureOn is areaPromoteFixture on a job image of the caller's
// own: the test that judges the promote block's image gate needs a record of a
// base older than promoteSince, which is the same world otherwise.
func areaPromoteFixtureOn(t *testing.T, image string) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", image)
	promoteLayerCheckout(t, publishFilePins(t, f, "version: 1\nproject: aurora\n"+
		"git: { provider: github, base_branch: main }\nagent: { auth: api-key }\n"+
		areaPromoteYAML+areaPromoteWorkflows))
	return f
}

// launchPromoteArea launches one promote run of acme/app under run id id with the
// given flags (the area is one of them, as it is on the command line) and
// returns its stored task.
func launchPromoteArea(t *testing.T, f *cloudFixture, id string, extra ...string) *task.Spec {
	t.Helper()
	args := append([]string{"run", "--repo", "acme/app", "--run-id", id}, extra...)
	if _, _, err := execute(t, args...); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return readSpec(t, f, id)
}

// TestRunPromoteRaisesAnAreaAndRecordsIt: `fugaro run --promote <area> --mode
// <level>` is a run of the area rather than of a task (LV7.1, LD6): the stored
// task carries the promote block and the area's own paths, the level asked for
// reaches the runner, and the task says in the launcher's words what the run
// is for. A target EQUAL to the level the area already resolves to is allowed
// — the pass pays the area's debt down at the level it is at.
func TestRunPromoteRaisesAnAreaAndRecordsIt(t *testing.T) {
	for _, c := range []struct{ name, area, mode string }{
		{"raising the area", "ledger", "critical"},
		{"equal to the area's level", "payments", "critical"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := areaPromoteFixture(t)
			spec := launchPromoteArea(t, f, "20261122-130000-abcd", "--promote", c.area, "--mode", c.mode)
			if spec.Promote == nil {
				t.Fatalf("the stored task carries no promote block: %+v", spec)
			}
			if spec.Promote.Area != c.area {
				t.Errorf("promote area = %q, want %q", spec.Promote.Area, c.area)
			}
			want := []string{"internal/" + c.area + "/**"}
			if len(spec.Promote.Paths) != 1 || spec.Promote.Paths[0] != want[0] {
				t.Errorf("promote paths = %v, want the area's own %v", spec.Promote.Paths, want)
			}
			if len(spec.Paths) != 1 || spec.Paths[0] != want[0] {
				t.Errorf("declared paths = %v, want the area's own %v: the runner resolves the level from them", spec.Paths, want)
			}
			if string(spec.Rigor) != c.mode {
				t.Errorf("rigor = %q, want the level asked for", spec.Rigor)
			}
			if spec.Mode != "" {
				t.Errorf("mode = %q: a promote run is an implement run, not a recipe mode", spec.Mode)
			}
			if !strings.Contains(spec.Task, "Raise the "+c.area+" area to "+c.mode) {
				t.Errorf("task = %q, want the launcher's line about the area", spec.Task)
			}
		})
	}
}

// TestRunPromoteRefusesATargetBelowTheArea: a promote run raises, so a target
// below the level the area already resolves to is refused before anything is
// launched, and the refusal names the level the area is at (LV7.1's own word:
// below is refused, equal is allowed).
//
// The second case is the rule's own level over a brace pattern. The level is
// read from what the rule declares as well as from what the resolver answers
// over the area's paths, because the paths handed to the resolver are the rule's
// own glob — a glob is not a path, and a brace pattern matched against its own
// text (`internal/{payments,ledger}/**` against itself) does not match, so the
// resolver alone reads a brace area as the repository's default and lets a
// target below the rule through. Mutation (drop the declared.Rank() comparison):
// the brace row below is launched instead of refused.
func TestRunPromoteRefusesATargetBelowTheArea(t *testing.T) {
	t.Run("a plain glob", func(t *testing.T) {
		areaPromoteFixture(t)
		for _, mode := range []string{"pro", "startup", "poc"} {
			_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261122-130100-abcd", "--promote", "payments", "--mode", mode)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("--promote payments --mode %s: err = %v, want a refusal", mode, err)
			}
			if !strings.Contains(err.Error(), "already resolves to critical") || !strings.Contains(err.Error(), "equal to it is allowed") {
				t.Errorf("--promote payments --mode %s: err = %v, want the area's own level named", mode, err)
			}
		}
	})
	t.Run("a brace pattern", func(t *testing.T) {
		f := newCloudFixture(t)
		writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+promoteSince)
		promoteLayerCheckout(t, publishFilePins(t, f, "version: 1\nproject: aurora\n"+
			"git: { provider: github, base_branch: main }\nagent: { auth: api-key }\n"+
			"rigor:\n  default: poc\n  areas:\n"+
			"    - { name: money, match: \"internal/{payments,ledger}/**\", mode: critical }\n"+
			areaPromoteWorkflows))
		for _, mode := range []string{"pro", "poc"} {
			_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261122-130101-abcd", "--promote", "money", "--mode", mode)
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "already resolves to critical") {
				t.Fatalf("--promote money --mode %s: err = %v, want the rule's own level named", mode, err)
			}
		}
		spec := launchPromoteArea(t, f, "20261122-130102-abcd", "--promote", "money", "--mode", "critical")
		if spec.Promote == nil || spec.Promote.Area != "money" {
			t.Fatalf("the area at its own level is launched, not refused: %+v", spec.Promote)
		}
	})
}

// promoteLayerCheckout makes the working directory a checkout of acme/app whose
// fugaro.yaml is yaml, the file a launch resolves the layer over: checkoutWorkflows
// writes a file with the project and no gcp_project, which is anchored for
// nothing and read beside no layer.
func promoteLayerCheckout(t *testing.T, yaml string) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

// TestRunPromoteRaisesAProjectAreaOfThisRepository: an area of the project
// layer is promoted one member at a time — the run is in one member repository
// and raises that repository's part of the rule (§3b item 12a, the launch's
// package doc). The member's own paths are the run's scope, and they are what
// the launcher stores, so the areas a nested organizational rule declares are
// launchable from the repositories they name rather than only from a rule the
// repository's own fugaro.yaml happens to carry.
//
// The area's level stays the operator's where the rule is the layer's (item
// 12b raises a repo-owned area's level in the pull request): this launch reads
// the layer's rule and refuses a target below it, and asks for no edit to it.
func TestRunPromoteRaisesAProjectAreaOfThisRepository(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+promoteSince)
	// The checkout is anchored (project and gcp_project), which is what makes
	// the project layer apply to it at all: a file without gcp_project is read
	// beside no layer (anchorOf), and this test is about the layer's areas.
	promoteLayerCheckout(t, "version: 1\nproject: aurora\ngcp_project: proj-1234\n"+
		"git: { provider: github, base_branch: main }\nagent: { auth: api-key }\n"+
		"workflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n")
	publishedLayer(t, f, layerWithRigorMembers("  rigor:\n    areas:\n"+
		"      - name: db\n        members:\n          - {repo: acme/app, paths: [\"internal/db/**\"]}\n        level: pro\n"+
		"      - name: mobile\n        members:\n          - {repo: acme/other, paths: [\"app/**\"]}\n        level: pro\n"))

	spec := launchPromoteArea(t, f, "20261122-130300-abcd", "--promote", "db", "--mode", "critical")
	if spec.Promote == nil || spec.Promote.Area != "db" {
		t.Fatalf("the layer's area of this repository is not launched: %+v", spec)
	}
	want := []string{"internal/db/**"}
	if len(spec.Promote.Paths) != 1 || spec.Promote.Paths[0] != want[0] {
		t.Errorf("promote paths = %v, want this repository's member paths %v", spec.Promote.Paths, want)
	}
	if len(spec.Paths) != 1 || spec.Paths[0] != want[0] {
		t.Errorf("declared paths = %v, want the member's own %v", spec.Paths, want)
	}
	// Below the layer's rule is refused for a project area too: the member's
	// paths resolve to the rule's own level.
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261122-130301-abcd", "--promote", "db", "--mode", "poc")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "already resolves to pro") {
		t.Fatalf("err = %v, want the layer's own level named", err)
	}
	// An area that names only another repository is not this repository's to
	// raise: there is no code of it here.
	_, _, err = execute(t, "run", "--repo", "acme/app", "--run-id", "20261122-130302-abcd", "--promote", "mobile", "--mode", "critical")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "declares no area by that name") {
		t.Fatalf("err = %v, want an area of another repository refused", err)
	}
}

// TestRunPromoteRefusesAnAreaTheRepositoryDoesNotDeclare: the area is the
// repository's own rule, so a name it does not declare is refused with the
// names it does — a run of somebody else's area would close items of an area
// it never resolved.
func TestRunPromoteRefusesAnAreaTheRepositoryDoesNotDeclare(t *testing.T) {
	areaPromoteFixture(t)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261122-130200-abcd", "--promote", "shipping", "--mode", "critical")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "declares no area by that name") {
		t.Fatalf("err = %v, want the name refused", err)
	}
	for _, name := range []string{"payments", "ledger"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal does not list %q:\n%v", name, err)
		}
	}
}

// TestRunPromoteRefusesHotfixAndAModeOfItsOwn: hotfix is off the rigor axis —
// a level a run is asked for and never one an area holds — so it is never a
// promote target, and a promote run takes the flags of a launch of its own and
// no others: the area's paths are the declared paths, and the brief is the
// runner's.
func TestRunPromoteRefusesHotfixAndAModeOfItsOwn(t *testing.T) {
	areaPromoteFixture(t)
	for _, c := range []struct {
		name, want string
		args       []string
	}{
		{"hotfix", "off the rigor axis", []string{"--promote", "payments", "--mode", "hotfix"}},
		{"no mode", "takes --mode", []string{"--promote", "payments"}},
		{"no area", "as its one argument", []string{"--promote", "--mode", "critical"}},
		{"two areas", "at most 1 arg", []string{"--promote", "payments", "ledger", "--mode", "critical"}},
		{"a path of its own", "takes no --path", []string{"--promote", "payments", "--mode", "critical", "--path", "internal/x/y.go"}},
		{"a task of its own", "takes no --task-file", []string{"--promote", "payments", "--mode", "critical", "--task-file", "t.md"}},
		{"a best-of-N batch", "takes no --attempts", []string{"--promote", "payments", "--mode", "critical", "--attempts", "2"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := execute(t, append([]string{"run", "--repo", "acme/app"}, c.args...)...)
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v: err = %v, want a refusal carrying %q", c.args, err, c.want)
			}
		})
	}
	// The mode's own vocabulary is checked first and by the flag's own rule:
	// --promote does not open a second spelling of the levels.
	_, _, err := execute(t, "run", "--repo", "acme/app", "--promote", "payments", "--mode", "sizzle")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--mode") {
		t.Fatalf("err = %v, want the mode refused by --mode's own rule", err)
	}
}

// TestRunPromoteRefusesAJobImageThatPredatesTheBlock is LV7.1's skew gate at
// its call site: the promote block is a field of the task format, and a runner
// older than promoteSince refuses the whole spec at bootstrap — after the
// launch was paid for — so the launch judges the job image before anything is
// written.
//
// The image refused is one release below promoteSince and it is deliberately
// an image that passes the rigor-request gate (promoteSince is newer than
// rigorRequestSince): the refusal has to be the promote block's, or a person
// reads a message about the mode they passed beside the flag instead of the
// flag itself. Mutation (drop the Promote gate from the launch's built-image
// branch, or gate it on rigorRequestSince): this test's row stops being
// refused. The clone branch's half of the same gate is
// TestRunPromoteIsGatedOnACloneWorkflow's, and neither test covers the other's
// branch: this one judges a build record, and that one has no record to judge.
func TestRunPromoteRefusesAJobImageThatPredatesTheBlock(t *testing.T) {
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, promoteSince, -1)
	areaPromoteFixtureOn(t, older)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261122-130500-abcd", "--promote", "ledger", "--mode", "critical")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the job image refused", err)
	}
	for _, want := range []string{"fugaro " + promoteSince + " or later", older, "--promote", "ledger", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
}

// TestRunPromoteIsGatedOnACloneWorkflow: a checkout: clone workflow has no
// build record to judge, so the promote block's gate is judged against
// base_images.base itself — the other half of the check, and the half a
// mutation can drop without the test above noticing.
//
// Mutation (drop the Promote block from finishLaunch's clone branch): the
// launch goes through on an image whose runner refuses the whole task at
// bootstrap, which is the failure the gate exists to prevent.
//
// The image is release0160 and not the older one the rigor test uses: the
// area this launch resolves comes from the checkout's own `rigor:` block, and
// a base older than rigorSince is refused by that block's gate before the
// promote block is ever judged. 0.16.0 passes the config gates and the level-3
// request, and is older than promoteSince — so the refusal below is the promote
// block's and nothing else's.
func TestRunPromoteIsGatedOnACloneWorkflow(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "base_images: {base: "+release0160+"}\n")
	testutil.IsolateGit(t)
	other := mustSlug("github", "acme/other")
	f.run.AddJob(gcp.JobName(other, "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": publishFilePins(t, f,
		"version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n"+
			"  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n"+areaPromoteYAML)})
	t.Chdir(dir)
	_, _, err := execute(t, "run", "--run-id", "20261122-130600-abcd", "--promote", "ledger", "--mode", "critical")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the job image refused", err)
	}
	for _, want := range []string{"--promote ledger", "the promote block", "fugaro " + promoteSince + " or later", release0160 + ", release 0.16.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunPromoteRefusesAReviewRecipe: a review-mode recipe builds its spec in
// reviewSpec, which has no promote field at all — so `--promote` beside one
// would validate, launch, and raise nothing. Refused where the recipe's mode is
// known, the way --review-run and --owner-ruled are.
func TestRunPromoteRefusesAReviewRecipe(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	writeBuildRecord(t, f, appSlug, "web", release0160)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261122-130700-abcd",
		"--promote", "payments", "--mode", "critical", "--recipe", "review-only", "--pr", "7")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--promote") || !strings.Contains(err.Error(), "review-only") {
		t.Fatalf("err = %v, want the review recipe to refuse the flag", err)
	}
}

// TestPromoteSinceHasAReleaseBullet: promoteSince names the release that ships
// task.Spec.Promote, and that release is not tagged yet, so docs/release.md's
// Before-you-tag list is where a release verifies it against the tag actually
// cut.
func TestPromoteSinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `promoteSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no promoteSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+promoteSince+"\"`") {
		t.Errorf("docs/release.md's promoteSince bullet does not carry the constant's release (%q)", promoteSince)
	}
	for _, want := range []string{
		"`task.Spec.Promote`", "not tagged yet", "checkImageSince", "checkCloneImageSince",
		"promoteGateSubject", "rigorRequestSince",
	} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's promoteSince bullet does not say %q", want)
		}
	}
}

// TestRunPromoteIsRefusedByRetry: a retry launches the stored task as it is,
// and the promote block is part of that task — so the flag is refused with it
// rather than applied to a run that already says what it is.
func TestRunPromoteIsRefusedByRetry(t *testing.T) {
	areaPromoteFixture(t)
	_, _, err := execute(t, "run", "--retry", "20261122-130400-abcd", "--promote", "payments")
	if err == nil || !strings.Contains(err.Error(), "takes no --promote") {
		t.Fatalf("err = %v, want --retry to refuse the flag", err)
	}
}
