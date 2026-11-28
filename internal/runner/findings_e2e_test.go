package runner_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestFinalizeWritesTheOpenFindingsArtifact (BF1): the artifact is written by
// the run's own finalize, off the round the run ended on. The unit test builds
// a run by hand and calls writeFindings itself, which covers the encoding but
// not the one call site that makes the feature real — delete the call in
// finalize and this is the test that fails.
//
// The run ends on a review round still carrying one finding, so that round is
// the deciding one and the same the record's blocks are read from: what the
// file holds is the reviewer's path for it and the key the run's own triage
// entries carry (findingKey), under the run's own prefix and name.
func TestFinalizeWritesTheOpenFindingsArtifact(t *testing.T) {
	h := newHarness(t, "", nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Address review'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	finding := map[string]any{
		"summary": "unbounded retry loop in the fetch path", "file": "feature.txt",
		"likelihood": 3, "impact": 3, "fix_cost": 2, "class": "correctness",
	}
	rec, err := h.run(t, implement("feature"), reviewScored("changes", finding), fix, reviewScored("changes", finding))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("outcome = %s, want draft: the run ended on a round with an open finding", rec.Outcome)
	}
	data, err := h.store.ReadFile(context.Background(), runner.FindingsName)
	if err != nil {
		t.Fatalf("reading the run's open findings: %v", err)
	}
	var doc struct {
		Version  int    `json:"version"`
		Run      string `json:"run"`
		Findings []struct {
			Severity string `json:"severity"`
			Path     string `json:"path"`
			Key      string `json:"key"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the artifact is not JSON: %v\n%s", err, data)
	}
	if doc.Version != 1 || doc.Run != runID {
		t.Errorf("artifact version/run = %d/%q, want 1/%q", doc.Version, doc.Run, runID)
	}
	if len(doc.Findings) != 1 {
		t.Fatalf("findings = %+v, want the one the run ended on", doc.Findings)
	}
	want := findingKeyOf("feature.txt", "unbounded retry loop in the fetch path")
	if got := doc.Findings[0]; got.Path != "feature.txt" || got.Key != want {
		t.Errorf("finding = %+v, want path feature.txt and key %s", got, want)
	}
}
