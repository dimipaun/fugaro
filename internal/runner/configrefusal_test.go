package runner_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestConfigRefusalIsConfigInvalid: a run the runner refuses before any work,
// because the configuration it resolved is not one it can run, ends with the
// class that names that cause (design §3: "the resolved configuration was
// refused before the work could run"). The definition's own two examples open
// the table — the resolved file itself, and a recipe the runner cannot read —
// and the rows after them are the other refusals this task classes: a layer
// that does not parse, a recipe that is in neither place, and a file that
// defines more than one workflow for a task that names none.
//
// The status and the reason do not move: a refusal here has always ended
// infra_error with the runner's own sentence, and the class is the field
// beside them that says which cause it was — the shape endRefused's
// branch_moved already has. The two are asserted together, so a site that
// gained a class by moving the status instead is red.
func TestConfigRefusalIsConfigInvalid(t *testing.T) {
	fixture := func(t *testing.T) string {
		t.Helper()
		return testutil.FixtureFiles(t)["fugaro.yaml"]
	}
	// The fixture with one value changed to one the schema does not define:
	// the problems config.Resolve returns are what the runner refuses.
	const badProvider = "provider: nosuch"
	cfg := fixture(t)
	if !strings.Contains(cfg, "provider: github") {
		t.Fatal("the fixture no longer names a git provider, so this test would refuse nothing")
	}
	badConfig := strings.Replace(cfg, "provider: github", badProvider, 1)
	withRecipe := strings.Replace(cfg, "  review_rounds: 2\n", "  review_rounds: 2\n  recipe: broken\n", 1)
	if withRecipe == cfg {
		t.Fatal("the fixture no longer writes review_rounds, so this test would run the default recipe")
	}
	// The same edit with a name no file and no catalog recipe has: the
	// refusal is resolveRecipe's third, which is reached only when the task
	// embeds no recipe of its own.
	noSuchRecipe := strings.Replace(cfg, "  review_rounds: 2\n", "  review_rounds: 2\n  recipe: nosuch\n", 1)
	// A second workflow beside app: a task that names none has no single
	// workflow to resolve to, which is SelectWorkflow's own refusal.
	twoWorkflows := cfg + `  second:
    commands:
      build: sh build.sh
      test: sh test.sh
      rerun_failed:
        command: sh test.sh
        each: "{id}"
      reports: ["build/test-results/*.xml"]
    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`
	// The layer's own text, as layer_test.go writes it: parsed against the
	// file's project and gcp_project, it names a key only the repository
	// layer may set.
	const badLayer = "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  budget: { per_run_usd: 1 }\n"
	unnamedWorkflow := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature"}

	cases := []struct {
		name  string
		cfg   string
		spec  *task.Spec
		files map[string]string
		wants []string
	}{
		{"the resolved fugaro.yaml is refused", badConfig, nil, nil,
			[]string{"fugaro.yaml is invalid"}},
		// The recipe's own name is in the reason, not merely "is invalid":
		// the refusal of fugaro.yaml carries those two words too, so a reason
		// that only matched them would pass without the recipe being read.
		{"the run's recipe cannot be read", withRecipe, nil,
			map[string]string{".fugaro/recipes/broken.yaml": "version: [\n"},
			[]string{".fugaro/recipes/broken.yaml is invalid"}},
		{"the run's recipe is nowhere", noSuchRecipe, nil, nil,
			[]string{"recipe nosuch is not in", "nor in the catalog"}},
		{"the task's project layer is refused", layeredRepo, layerSpec(badLayer), nil,
			[]string{"the task's project layer", "budget.per_run_usd may only be set in: repo"}},
		{"the task names no workflow and the file defines two", twoWorkflows, unnamedWorkflow, nil,
			[]string{"fugaro.yaml defines 2 workflows", "pick one of app, second"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessFiles(t, tc.cfg, tc.spec, tc.files)
			rec, err := h.run(t)
			if err == nil {
				t.Fatal("a run whose configuration the runner refuses ended without an error")
			}
			if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
				t.Fatalf("record = %+v, want an infra_error with outcome none: the class is the field beside the status, and a refusal that moved the status too would change what every reader of it sees", rec)
			}
			for _, want := range tc.wants {
				if !strings.Contains(rec.Reason, want) {
					t.Errorf("the reason is %q, want one naming %q: the class names the cause and the reason is still the runner's own sentence for it", rec.Reason, want)
				}
			}
			if rec.FailureClass != failure.ConfigInvalid {
				t.Errorf("failure_class = %q, want %q: a run refused for its own configuration is not a run whose machinery failed, and the remedy the report prints for it (change the file) is this class's", rec.FailureClass, failure.ConfigInvalid)
			}
		})
	}
}

// TestFollowUpEndingCarriesItsClassIntoTheStoredReport: a follow-up that stops
// before updating its pull request stores the report a reader of a failed run
// reads, rendered from a copy of the record ended with the class the error
// carries (storeUnposted, followup.go) — the class Run's fallback writes on the
// record beside it (endClassOf). Both follow-up endings that reach it are
// walked: a push that failed (no class on the error, so infra_error) and a pull
// request the forge could not set up (providerRefusal, so git_provider_error).
//
// The class is the field a report counts runs by and nothing rewrites it, so a
// stored ending that named a family other than the record's would be two
// readings of one run's cause, and a site that stopped reading the class off
// its error would file the forge's own failure under infra_error.
func TestFollowUpEndingCarriesItsClassIntoTheStoredReport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		class failure.Class
		wants string
		setup func(t *testing.T, h *fuHarness)
	}{
		{
			name:  "the push failed",
			class: failure.InfraError,
			wants: "pushing fugaro/" + runID,
			setup: func(t *testing.T, h *fuHarness) { refuseAll(t, h.remote, "no thanks") },
		},
		{
			name:  "the pull request could not be set up",
			class: failure.GitProviderError,
			wants: "opening pull request",
			setup: func(t *testing.T, h *fuHarness) {
				h.deps.RetryDelay = time.Millisecond
				h.provider.FailEnsure = 100
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := followUpHarness(t, "", nil)
			h.followUp(t, followID, runID, "Tidy up.")
			tc.setup(t, h)
			rec, err := h.run(t, implement("tidy"), review("ship", 0))
			if err == nil || !strings.Contains(rec.Reason, tc.wants) {
				t.Fatalf("rec = %+v, err = %v, want an error naming %q", rec, err, tc.wants)
			}
			if rec.FailureClass != tc.class {
				t.Errorf("failure_class = %q, want %q: the ending is written with the class the error carries, and the class is what a report prints a remedy for", rec.FailureClass, tc.class)
			}
			stored, rerr := h.store.ReadRecord(context.Background())
			if rerr != nil {
				t.Fatal(rerr)
			}
			if stored.FailureClass != rec.FailureClass || stored.Reason != rec.Reason {
				t.Errorf("the stored record is %q/%q and the run's %q/%q: a stored ending that named a different cause from the record beside it would be two readings of one run", stored.FailureClass, stored.Reason, rec.FailureClass, rec.Reason)
			}
			report, rerr := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"report.md")
			if rerr != nil || !strings.Contains(string(report), "**Outcome:** stopped — ") || !strings.Contains(string(report), tc.wants) {
				t.Fatalf("report.md = %q, %v, want the report of the stopped follow-up that storeUnposted renders", report, rerr)
			}
		})
	}
}
