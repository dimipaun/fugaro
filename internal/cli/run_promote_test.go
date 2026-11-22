package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/task"
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
// declares those areas, on a job image that reads a rigor request.
func areaPromoteFixture(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release0160)
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
