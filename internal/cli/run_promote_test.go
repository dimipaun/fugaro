package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// areaPromoteYAML is a repository whose two areas are the ones a promote launch
// raises: `payments` at critical, `ledger` at pro, over a repository whose own
// default is poc — so a promote run can raise (ledger to critical), stand
// equal (payments at critical) or be refused for going below (payments at
// pro).
const areaPromoteYAML = "rigor:\n  default: poc\n  areas:\n" +
	"    - { name: payments, match: \"internal/payments/**\", mode: critical }\n" +
	"    - { name: ledger, match: \"internal/ledger/**\", mode: pro }\n"

// areaPromoteFixture is a launch of acme/app from a checkout whose fugaro.yaml
// declares those areas, on a job image whose runner reads the promote block —
// the release this work ships in, since an older one refuses the stored task at
// bootstrap (TestRunPromoteRefusesAJobImageThatPredatesTheBlock).
func areaPromoteFixture(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+promoteSince)
	checkoutWorkflows(t, areaPromoteYAML, "  web: { commands: { build: sh build.sh, test: sh test.sh } }\n")
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
func TestRunPromoteRefusesATargetBelowTheArea(t *testing.T) {
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
	f := newCloudFixture(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, promoteSince, -1)
	writeBuildRecord(t, f, appSlug, "web", older)
	checkoutWorkflows(t, areaPromoteYAML, "  web: { commands: { build: sh build.sh, test: sh test.sh } }\n")
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
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n" +
		"  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n" + areaPromoteYAML})
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
