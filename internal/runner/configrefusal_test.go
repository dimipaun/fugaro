package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestConfigRefusalIsConfigInvalid: a run the runner refuses before any work,
// because the configuration it resolved is not one it can run, ends with the
// class that names that cause (design §3: "the resolved configuration was
// refused before the work could run"). The two rows are the definition's own
// two examples: the resolved file itself, and a recipe the runner cannot read.
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

	cases := []struct {
		name   string
		cfg    string
		files  map[string]string
		reason string
	}{
		{"the resolved fugaro.yaml is refused", badConfig, nil, "fugaro.yaml is invalid"},
		// The recipe's own name is in the reason, not merely "is invalid":
		// the refusal of fugaro.yaml carries those two words too, so a reason
		// that only matched them would pass without the recipe being read.
		{"the run's recipe cannot be read", withRecipe,
			map[string]string{".fugaro/recipes/broken.yaml": "version: [\n"},
			".fugaro/recipes/broken.yaml is invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessFiles(t, tc.cfg, nil, tc.files)
			rec, err := h.run(t)
			if err == nil {
				t.Fatal("a run whose configuration the runner refuses ended without an error")
			}
			if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
				t.Fatalf("record = %+v, want an infra_error with outcome none: the class is the field beside the status, and a refusal that moved the status too would change what every reader of it sees", rec)
			}
			if !strings.Contains(rec.Reason, tc.reason) {
				t.Errorf("the reason is %q, want one naming %q: the class names the cause and the reason is still the runner's own sentence for it", rec.Reason, tc.reason)
			}
			if rec.FailureClass != failure.ConfigInvalid {
				t.Errorf("failure_class = %q, want %q: a run refused for its own configuration is not a run whose machinery failed, and the remedy the report prints for it (change the file) is this class's", rec.FailureClass, failure.ConfigInvalid)
			}
		})
	}
}
