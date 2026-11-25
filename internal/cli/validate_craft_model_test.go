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
    senior-review: {model: claude-opus-4}
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
// the documents the crafts resolve over — the file in hand and the project
// layer file it resolved over, the two a run of this file resolves over — and
// every problem is reported at the key that wrote the model, which is not
// always a key of the file in hand: `fugaro validate` reads one fugaro.yaml and
// may name a line of the layer it resolved over, because a reader sent to a key
// that is not in the file they have is being sent to the wrong file.
//
// The allow-list is the problem this test uses because it is one rule that
// reaches the layers a run resolves over, and the document that wrote the model
// is the project layer's since S1 (D3237): a repository file's own crafts are
// dropped before the merge, so the file in hand is no longer the document a
// craft model is judged at — this test's second half is exactly that.
func TestValidateNamesACraftModelProblemAtItsOwnPath(t *testing.T) {
	// The models this world names with no row in the shipped table carry an
	// owner's price, because the price rule reaches them: every run has a
	// gateway now (AB1b, D3009), so `validate` prices the crafts' models in
	// every mode, and "no price" would otherwise be a problem this test's
	// fixture contributed rather than the allow-list rule it is about.
	craftFixtureWith(t, "budget: { allowed_models: [\"claude-opus-4\"] }\n")
	testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": validateCraftModelYAML})
	out, errOut, err := execute(t, "validate", "--offline", "fugaro.yaml")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("validate accepted craft models no allow-list holds: exit %d\n%s%s", ExitCode(err), out, errOut)
	}
	// The project layer file wrote the first-line review's model, and its key is
	// `defaults.agent.crafts.<craft>.model` -- a document the reader has, but not
	// the one this command was handed.
	if line := problemLine(out, "defaults.agent.crafts.review.model"); !strings.Contains(line, "`claude-sonnet-5-5` is not an allowed model") {
		t.Errorf("validate did not report the layer's model at the key that wrote it:\nwant %q in a line\ngot  %q\nall:\n%s", "`claude-sonnet-5-5` is not an allowed model", line, out)
	}
	// The file in hand wrote craft models of its own, and they are not the
	// run's: `agent.crafts` is a lifted key, so the file that wrote the
	// security review's model off the list is not refused at that key — the
	// line a reader would be sent to changes nothing about this run. The
	// crafts that name no model of their own are not refused at theirs either
	// (F17), for the same reason.
	for _, path := range []string{"agent.crafts.review.model", "agent.crafts.security-review.model", "agent.crafts.senior-review.model", "crafts.senior-review.model"} {
		if line := problemLine(out, path); line != "" {
			t.Errorf("validate judged a craft model at %s, which no run of this file reads: %q\n%s", path, line, out)
		}
	}
}

// TestValidateIgnoresTheCraftsNoRunReads: the crafts are resolved over the
// documents a run of this file resolves over — the file, merged with the
// project layer — and the installation's own config is not one of them
// (internal/runner's craftLayers: a run in a job has no installation config,
// it carries what the owner published). `fugaro validate` reads one for the
// credentials, the pools and the routes a craft's access and its model are
// judged against, and reading its crafts too would refuse a file for a model
// no run of it reads.
//
// The world is the one above, with an allow-list that holds every model the
// run's two documents write and a file whose own pins are on it, so that the
// crafts are the only thing left a refusal could be about: the installation
// config's crafts name two models no run of this file reads, and a validate
// that judged them would report the coder's at a key of a document its reader
// may not even have.
//
// The world carries the security review model's price for the sibling test's
// reason: the price rule reaches the crafts' models in every mode now (AB1b,
// D3009), so a model with no row in the shipped table would be a problem this
// fixture contributed rather than the crafts-of-the-installation rule it is
// about. That price is the fixture's own (craftLocalExtra, AB4, D3020), and it
// is not repeated here: the installation config carries one model_prices block.
func TestValidateIgnoresTheCraftsNoRunReads(t *testing.T) {
	craftFixtureWith(t, "budget: { allowed_models: [\"claude-opus-4-1\", \"claude-sonnet-5-5\"] }\n")
	// The file's own pins are the layer's since S1 (D3237): a run of this file
	// is priced by the layer, and a file whose models the drop left nowhere
	// would be refused for naming none before the crafts are read at all. The
	// layer keeps its own craft — the first-line review's model — and takes the
	// file's pins, which is what a published project answer to this file is.
	writeCraftLayer(t, testutil.MergePins(craftProjectLayer, pinsLayer(validateCraftPinnedYAML)))
	testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": validateCraftPinnedYAML})
	out, errOut, err := execute(t, "validate", "--offline", "fugaro.yaml")
	if ExitCode(err) != ExitOK {
		t.Fatalf("validate refused a file whose crafts are all served: exit %d\n%s%s", ExitCode(err), out, errOut)
	}
	for _, path := range []string{"crafts.coder.model", "crafts.senior-review.model"} {
		if line := problemLine(out, path); line != "" {
			t.Errorf("validate judged the installation config's own craft at %s, which no run of this file reads: %q\n%s", path, line, out)
		}
	}
}

// validateCraftPinnedYAML is validateCraftModelYAML with the pins a file needs
// once an allow-list bounds the run: agent.model on the list, and the
// background slot, which has no fallback of its own, which is what leaves the
// crafts as the one thing a refusal can be about.
const validateCraftPinnedYAML = `version: 1
project: aurora
gcp_project: aurora-gcp
git: { provider: github }
agent:
  auth: api-key
  model: claude-sonnet-5-5
  models: { background: claude-sonnet-5-5 }
  crafts:
    review: {access: anthropic-key}
    security-review: {model: claude-opus-4-1}
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`

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

// launchCraftAutoYAML is the file the launch below commits: the coder craft
// moves the coder onto a model a route claims, and first_line_review is auto —
// so whether level 1 runs is read off the coder's model
// (config.Agent.FirstLineOn), which the craft decides, and the first-line
// review's own model is off the allow-list.
//
// The file's own slots say the other answer: they name one model, no route
// claims it, so a resolution that reads them leaves level 1 off. That is the
// divergence the launch must not have (MC12's write comes before the levels are
// asked whether they run).
const launchCraftAutoYAML = `version: 1
project: aurora
git: { provider: github, base_branch: develop }
budget: { allowed_models: [claude-opus-4-1, deepseek/deepseek-v4.1-flash] }
agent:
  auth: api-key
  model: claude-opus-4-1
  first_line_review: auto
  crafts:
    coder: { model: deepseek/deepseek-v4.1-flash }
    review: { model: claude-sonnet-5-5 }
workflows:
  svc: { commands: { build: sh build.sh, test: sh test.sh } }
`

// launchCraftAutoRoute is the owner's route the launch below needs: it claims
// the model the coder craft names, which is what turns level 1 on for the run
// and leaves it off for a resolution reading the file's own slots.
const launchCraftAutoRoute = "providers:\n" +
	"  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n" +
	"    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n" +
	"model_prices:\n  \"deepseek/deepseek-v4.1-flash\": { input_per_m: 1, output_per_m: 2 }\n"

// TestLaunchTakesTheCraftsTheRunsBootstrapResolves: the crafts a launch judges
// are the ones the run's own bootstrap reads, and the level 1 switch is the
// one answer a craft can move — the coder craft's model is what
// agent.first_line_review: auto reads, so the run turns level 1 on for a file
// whose own slots leave it off (MC12's write happens before the levels are
// resolved, internal/runner/craftmodel.go).
//
// A launch that resolved the file's own slots reads level 1 as off, reads the
// first-line review's craft nowhere and starts a job whose first act is to
// refuse the task — the exact cost this gate exists to save (MC14, D1665).
func TestLaunchTakesTheCraftsTheRunsBootstrapResolves(t *testing.T) {
	noAgentSession(t)
	f := newCloudFixture(t)
	f.appendConfig(t, launchCraftAutoRoute)
	configSkewCheckout(t, f, launchCraftAutoYAML)
	publishedLayer(t, f, layerPins(launchCraftAutoYAML))
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releasePolicy)
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("the launch served a craft the run reads and refuses: err = %v (exit %d)", err, ExitCode(err))
	}
	for _, want := range []string{
		"the crafts this run would resolve cannot be served",
		"agent.crafts.review.model",
		"`claude-sonnet-5-5` is not an allowed model",
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

// cropLayerNoReviewCraft is craftProjectLayer without the first-line review's
// model and with the coder's: the layer writes the one model two crafts run —
// the coder's own, and the review's, which follows it (F16).
const cropLayerNoReviewCraft = `version: 1
project: aurora
gcp_project: aurora-gcp
defaults:
  git: { provider: github }
  agent:
    auth: api-key
    crafts:
      coder: {model: deepseek/deepseek-v4.1-flash}
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
	line := "defaults.agent.crafts.coder.model: `deepseek/deepseek-v4.1-flash` is not an allowed model"
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
