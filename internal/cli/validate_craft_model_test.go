package cli

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// validateCraftModelYAML is the craft fixture's own checkout file with the
// workflow every valid file carries: the offline crafts read resolves
// agent.crafts without validating the file, and `fugaro validate` does not, so
// this test's world needs the file a repository would really hold.
const validateCraftModelYAML = `version: 1
project: aurora
gcp_project: aurora-gcp
git: { provider: github }
agent:
  auth: api-key
  crafts:
    review: {access: anthropic-key}
    security-review: {model: claude-opus-4-1}
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`

// The tests here pin MC14's two surfaces on the model half of the crafts
// (D1665): `fugaro validate` names every problem the run's own checks would
// refuse the crafts' models for, each at the path of the key in the document
// that wrote the model, and a launch refuses the same configurations before it
// writes a task, so a launch that cannot be served costs no execution.
//
// They are one test file because the two surfaces share one function
// (craftModelProblems) and the whole point of the task is that they cannot
// disagree: a rule written twice is a rule that drifts.

// TestValidateNamesACraftModelProblemAtItsOwnPath: the model half is judged on
// the three documents the crafts resolve over — the checkout's own file, the
// installation's config and the project layer file — and every problem is
// reported at the key that wrote the model, which is not always a key of the
// file in hand: `fugaro validate` reads one fugaro.yaml and may name a line of
// two other documents, because a reader sent to a key that is not in the file
// they have is being sent to the wrong file.
//
// The allow-list is the problem this test uses because it is one rule that
// reaches every layer: the installation config's coder model and the project
// layer's first-line review model are both left out of it while the senior
// review's is on it, and the checkout's own security review model is left out
// too, so the three paths come out of one run.
func TestValidateNamesACraftModelProblemAtItsOwnPath(t *testing.T) {
	craftFixtureWith(t, "budget: { allowed_models: [\"claude-opus-4\"] }\n")
	testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": validateCraftModelYAML})
	out, errOut, err := execute(t, "validate", "--offline", "fugaro.yaml")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("validate accepted craft models no allow-list holds: exit %d\n%s%s", ExitCode(err), out, errOut)
	}
	for _, want := range []struct{ path, message string }{
		// The installation's own config wrote the coder's model (craftLocalExtra),
		// and that document's key is `crafts.<craft>.model`.
		{"crafts.coder.model", "`deepseek/deepseek-v4.1-flash` is not an allowed model"},
		// The project layer file wrote the first-line review's, and its key is
		// `defaults.agent.crafts.<craft>.model` -- a document the reader has, but
		// not the one this command was handed.
		{"defaults.agent.crafts.review.model", "`claude-sonnet-5-5` is not an allowed model"},
		// The file in hand wrote the security review's model, at the key its own
		// document spells.
		{"agent.crafts.security-review.model", "`claude-opus-4-1` is not an allowed model"},
	} {
		if line := problemLine(out, want.path); !strings.Contains(line, want.message) {
			t.Errorf("validate did not report %s at the key that wrote it:\nwant %q in a line\ngot  %q\nall:\n%s", want.path, want.message, line, out)
		}
	}
	// The senior review's model is on the list, and the crafts that name no
	// model of their own — the coder's follower and the security review — are
	// not refused for a model they did not write (F17): a problem at their keys
	// would send a reader to a line that changes nothing.
	if line := problemLine(out, "crafts.senior-review.model"); line != "" {
		t.Errorf("the senior review's model is on the allow-list and was reported anyway: %q", line)
	}
}

// launchCraftAllowListYAML is the file the allow-list launch commits as the
// ref's fugaro.yaml: configSkewHead's repository, ref and workflow, with a craft
// whose model the file's own allow-list leaves out. The first-line review is off
// so that the allow-list is the only rule this launch can be refused by.
const launchCraftAllowListYAML = `version: 1
project: aurora
git: { provider: github, base_branch: develop }
budget: { allowed_models: [claude-sonnet-5-5] }
agent:
  auth: api-key
  model: claude-sonnet-5-5
  first_line_review: off
  crafts:
    coder: { model: claude-opus-4-1 }
workflows:
  svc: { commands: { build: sh build.sh, test: sh test.sh } }
`

// TestLaunchRefusesACraftModelOutsideTheAllowList: the launch is a refusal of
// the same configuration the run would halt on — a craft's model no allow-list
// holds — and it happens before the task is written and before any job is
// asked for, because a launch that cannot be served costs nothing while the
// alternative is a job paid for a run whose first act is to refuse its task.
//
// It is the verdict `fugaro validate` prints (from the same function,
// craftModelProblems), and the message says which file wrote the model: the
// allow-list is the file at the ref's own, so that is the document the reader
// has to open, not the launcher's working tree.
func TestLaunchRefusesACraftModelOutsideTheAllowList(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	configSkewCheckout(t, f, launchCraftAllowListYAML)
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("the launch: err = %v (exit %d), want the crafts refused", err, ExitCode(err))
	}
	for _, want := range []string{
		"the crafts this run would resolve cannot be served",
		"agent.crafts.coder.model",
		"`claude-opus-4-1` is not an allowed model",
		"the fugaro.yaml at the ref",
		"the launch is refused before any execution exists",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a refused launch", n)
	}
	if storedTask(t, f) {
		t.Error("the refused launch stored its task")
	}
}

// launchCraftSameModelYAML is the file the guard test below commits: the
// first-line review and the senior review each take their model from their own
// craft, and the two crafts name one ID — while the roles the levels would fall
// back to (agent.model for the coder, agent.models.reviewer for the senior
// review) name two, so a launcher that did not resolve the crafts would see a
// pair of levels that differ and let it through.
const launchCraftSameModelYAML = `version: 1
project: aurora
git: { provider: github, base_branch: develop }
agent:
  auth: api-key
  model: claude-sonnet-5-5
  models: { reviewer: claude-opus-4-1 }
  first_line_review: on
  crafts:
    review: { model: claude-opus-4-1 }
    senior-review: { model: claude-opus-4-1 }
workflows:
  svc: { commands: { build: sh build.sh, test: sh test.sh } }
`

// TestLaunchRefusesTheSameModelForL1AndL2ThroughCrafts: D721's guard is about
// the models the two levels will really run (D1664), and rung 3 of the model
// half's contract makes a craft a level's model wherever the level names none
// (MC13) — so two crafts that resolve to one ID are refused exactly as two
// levels that name it, and the refusal names the keys that wrote them.
//
// The launch is where it has to be refused: the run's bootstrap makes the same
// resolution and would refuse the task it was given, after the job was paid
// for. The refusal names the crafts rather than l1/l2.model, which is the line
// the reader has to change.
func TestLaunchRefusesTheSameModelForL1AndL2ThroughCrafts(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	configSkewCheckout(t, f, launchCraftSameModelYAML)
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("the launch: err = %v (exit %d), want the levels refused", err, ExitCode(err))
	}
	for _, want := range []string{
		"agent.crafts.senior-review.model and agent.crafts.review.model are both claude-opus-4-1",
		"review level 2 may not run the same model as level 1 (D721)",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a refused launch", n)
	}
	if storedTask(t, f) {
		t.Error("the refused launch stored its task")
	}
}

// launchCraftOffLevelYAML is the file the two launches below commit: two crafts
// name a model the allow-list leaves out — the first-line review's and the
// security review's — and the allow-list is the only rule in it, so what each
// launch does with those two crafts is the whole question. first_line_review is
// off here and turned on for the second launch; level 3 is off in both, which
// is what it is outside a task that asks for the strongest review.
const launchCraftOffLevelYAML = `version: 1
project: aurora
git: { provider: github, base_branch: develop }
budget: { allowed_models: [claude-sonnet-5-5] }
agent:
  auth: api-key
  model: claude-sonnet-5-5
  first_line_review: %s
  crafts:
    review: { model: claude-opus-4-1 }
    security-review: { model: claude-opus-4-1 }
workflows:
  svc: { commands: { build: sh build.sh, test: sh test.sh } }
`

// TestLaunchServesACraftWhoseLevelIsOff: a launch judges the crafts the run
// reads, not every craft the layers resolve (launchCraftsRead). A craft's model
// reaches a slot only where the level it belongs to runs and wrote no model of
// its own — MC12's write, and F14's rule that a craft decides a level's model
// and never whether the level runs — so a level the run's own layers left off
// leaves its craft's model unread, uncharged and unrefused.
//
// The two launches are one file apart, and the difference is the point: level
// 1's switch is the file's, so the same document launches for the task that
// runs no first-line review and is refused for the one that does. A launch that
// refused both would refuse a configuration the run serves, and would send the
// reader to a key nothing in the refused run reads.
func TestLaunchServesACraftWhoseLevelIsOff(t *testing.T) {
	t.Run("the levels are off, so the crafts are not read", func(t *testing.T) {
		noAgentSession(t)
		f := newCloudFixture(t)
		configSkewCheckout(t, f, fmt.Sprintf(launchCraftOffLevelYAML, "off"))
		writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releasePolicy)
		if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
			t.Fatalf("the launch refused a craft whose level does not run: %v", err)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Errorf("%d executions were created, want one: the launch was served", n)
		}
	})
	t.Run("the level runs, so the craft is judged", func(t *testing.T) {
		noAgentSession(t)
		f := newCloudFixture(t)
		configSkewCheckout(t, f, fmt.Sprintf(launchCraftOffLevelYAML, "on"))
		writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releasePolicy)
		_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("the launch: err = %v (exit %d), want the crafts refused", err, ExitCode(err))
		}
		for _, want := range []string{
			"the crafts this run would resolve cannot be served",
			"agent.crafts.review.model",
			"`claude-opus-4-1` is not an allowed model",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
		}
		// The security review's model is unread in this launch too — level 3 is
		// off in both files — so the refusal names the craft the run reads and
		// not the one it does not.
		if strings.Contains(err.Error(), "agent.crafts.security-review.model") {
			t.Errorf("the refusal names a craft level 3 does not run: %v", err)
		}
		if storedTask(t, f) {
			t.Error("the refused launch stored its task")
		}
	})
}

// cropLayerNoReviewCraft is craftProjectLayer without the first-line review's
// model, so that craft follows the coder's (F16).
const cropLayerNoReviewCraft = `version: 1
project: aurora
gcp_project: aurora-gcp
defaults:
  git: { provider: github }
  agent:
    auth: api-key
`

// TestValidateNamesAnInheritedCraftModelProblemOnce: the first-line review
// follows the coder, so one model the allow-list leaves out is a problem about
// two crafts — and both are written at the key that wrote the model, which is
// the one line a reader has to change. The reader is told once: a line repeated
// is the repetition dedupeLines exists for, and the two surfaces that ask for
// this verdict (validate and a launch) print from the same function, so neither
// of them can repeat it either.
func TestValidateNamesAnInheritedCraftModelProblemOnce(t *testing.T) {
	craftFixtureWith(t, "budget: { allowed_models: [\"claude-opus-4\"] }\n")
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "aurora-gcp", Bucket: "fugaro-runs-aurora-gcp", CheckedAt: time.Now(),
		YAML: cropLayerNoReviewCraft,
	}); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": validateCraftModelYAML})
	out, _, err := execute(t, "validate", "--offline", "fugaro.yaml")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("validate accepted a coder model no allow-list holds: exit %d\n%s", ExitCode(err), out)
	}
	line := "crafts.coder.model: `deepseek/deepseek-v4.1-flash` is not an allowed model"
	if n := strings.Count(out, line); n != 1 {
		t.Errorf("the coder's model is refused %d time(s), want once: the review craft runs it too (F16), and one line is what a reader has to change\n%s", n, out)
	}
}

// problemLine is the reported problem at path, "" when none was: the text form
// of a problem is `<path>: <message>`, one per line.
func problemLine(out, path string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, path+":") {
			return line
		}
	}
	return ""
}
