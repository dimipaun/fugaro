package runner_test

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The reader of a legacy record classes a record that carries no field from
// the record's own reason (runview.ClassOf, design §5), so the class an
// *ending* writes has to be the class that reason reads as: a record written
// before the field shipped and one written after must name the same cause for
// the same fact (F34's rule, which FC5's agreement walk holds).
//
// TestHaltClassesAgreeWithTheReadersTable and
// TestStageErrorClassesAgreeWithTheReadersTable (package runner) hold that for
// the tables themselves — the halt switch and the classifier's own answer.
// This holds it for the endings that *write* a class, which is the value
// `runs ls --json`, `diagnose` and the console show, and it is a different
// question: a site that computes the right class and then stores another one
// agrees with the reader in neither direction, and the tables' own walk cannot
// see it.
//
// The comparison is against a field-stripped copy of the record the run
// produced — the same record, read the way a reader of a run from before the
// field shipped reads it. That is the walk the design asks for: the class
// being on the record is not the question; the class being the one the reason
// means is.
//
// It sits in this package rather than beside the two walks above because it
// drives whole runs, and the harness is here.
func TestEndingsAgreeWithTheReadersTable(t *testing.T) {
	// The text a provider's refusal arrives in, is_error set and the subtype
	// "success" — the shape Claude Code reports a refused API call in, which
	// is what makes the stage's own result carry a class (stageFailureClass)
	// and the reason a sentence the reader's table knows.
	const refused = "the provider answered with status 403"
	stageRefused := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		return agent.Result{IsError: true, Subtype: "success", Text: refused}, nil
	}
	// Every ending below fails for the same cause through a different mode,
	// and each one writes the record itself: an ordinary run through
	// finalize's failed case, a review run through endReviewFailed, an
	// artifact run through endArtifactFailed.
	cases := []struct {
		name string
		run  func(t *testing.T) (*runstore.Record, error)
	}{
		{"an ordinary run's stage", func(t *testing.T) (*runstore.Record, error) {
			return newHarness(t, "", nil).run(t, stageRefused)
		}},
		{"a review run's stage", func(t *testing.T) (*runstore.Record, error) {
			h := newHarness(t, "", reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
			pushReviewBranch(t, h, "feature", func(dir string) {
				testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
			})
			addPR(h, 7, "feature", "")
			return h.run(t, stageRefused)
		}},
		{"an artifact run's plan stage", func(t *testing.T) (*runstore.Record, error) {
			h := newHarnessFiles(t, renderCfg(t, "", ""), artifactSpec(sizzlePlanRenderYAML), renderScript(sizzleScript))
			return h.run(t, stageRefused)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := tc.run(t)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != runstore.StatusFailed {
				t.Fatalf("the run ended %q, want the failure this case is about: %+v", rec.Status, rec)
			}
			if rec.FailureClass != failure.ProviderAuthRefused {
				t.Errorf("the ending wrote the class %q, want %q: the stage's own result carried it and the site that wrote the reason must keep it, or the field says a cause the record's own sentence does not (`%s`)", rec.FailureClass, failure.ProviderAuthRefused, rec.Reason)
			}
			stripped := *rec
			stripped.FailureClass = ""
			if got := runview.ClassOf(&stripped); got != rec.FailureClass {
				t.Errorf("the ending wrote %q and a reader with no field to read answers %q for the same record: the run that ended today and the run that ended before the field shipped must be counted under one name, whatever their class is written by (reason %q)", rec.FailureClass, got, rec.Reason)
			}
		})
	}
}
