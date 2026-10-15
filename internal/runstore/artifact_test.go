package runstore

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOutcomeArtifactsValue pins the outcome's own value: it is written into
// result.json and switched on by the report, cancel and watch, so the string
// is a contract, not an implementation detail.
func TestOutcomeArtifactsValue(t *testing.T) {
	if got := string(OutcomeArtifacts); got != "artifacts" {
		t.Fatalf("OutcomeArtifacts = %q, want artifacts", got)
	}
}

// TestRenderRecordShape is the render record's JSON as the design fixes it
// (artifact-runs.md §3, D8): command and command_sha256 are separate fields
// (the pair diverges whenever the redactor fired, which is the point), tree
// is always present, timed_out is written only when true, and a record
// without a render writes no render key at all.
func TestRenderRecordShape(t *testing.T) {
	rec := Record{Version: 1, RunID: runID, Repo: "acme/app", Status: StatusSucceeded,
		Stage: "render", Outcome: OutcomeArtifacts,
		Render: &RenderRecord{
			Command:       "sh render.sh",
			CommandSHA256: strings.Repeat("a", 64),
			Tree:          "working",
			ExitCode:      0,
			DurationS:     12.5,
			StartedAt:     "2026-10-15T10:00:00Z",
		}}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Render map[string]any `json:"render"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Render["command"] != "sh render.sh" || got.Render["tree"] != "working" || got.Render["command_sha256"] != strings.Repeat("a", 64) {
		t.Fatalf("render = %v", got.Render)
	}
	if _, ok := got.Render["timed_out"]; ok {
		t.Fatalf("timed_out is written for a render that did not time out: %v", got.Render)
	}
	rec.Render = nil
	data, err = json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"render":{`) {
		t.Fatalf("a record with no render writes one: %s", data)
	}
}
