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

// TestArtifactFileShape is the artifact entry's JSON as the design fixes it
// (artifact-runs.md §3, §8): the name is the sanitised one the object's key
// leaf is built from, the size and the sha256 are what the upload wrote, the
// content type is recorded and never trusted, and the probe's verdict is an
// object of its own. A record that delivered nothing writes no artifacts key
// at all, so an old reader sees exactly the record it saw before this field
// existed.
//
// Every artifact a runner of this release uploads carries a verdict — the
// probe runs once over the whole set, and a recipe asking for none records
// {ran: false, exit_code: -1} rather than leaving the key out (plan Review
// Focus 6, and the schema's own required ["ran", "exit_code"]). The fixture's
// first entry has no Probe for the one reason a stored record can: it is the
// shape a record written BEFORE the probe step existed has, which is what the
// field's absence means on disk and what this test pins. That is why Probe is
// a pointer: a value type would make every such record unmarshal to the zero
// ProbeResult and re-marshal as {ran: false, exit_code: 0} — a claim about a
// check rather than the absence of one.
func TestArtifactFileShape(t *testing.T) {
	rec := Record{Version: 1, RunID: runID, Repo: "acme/app", Status: StatusSucceeded,
		Stage: "render", Outcome: OutcomeArtifacts,
		Artifacts: []ArtifactFile{
			// No Probe: a pre-probe record's artifact, not a runner's.
			{Name: "sizzle.mp4", Bytes: 4096, SHA256: strings.Repeat("c", 64), ContentType: "video/mp4"},
			{Name: "clips/a.png", Bytes: 12, SHA256: strings.Repeat("d", 64), ContentType: "image/png",
				Probe: &ProbeResult{Ran: true, ExitCode: 0}},
		}}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Artifacts []map[string]any `json:"artifacts"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Artifacts) != 2 {
		t.Fatalf("artifacts = %v", got.Artifacts)
	}
	if got.Artifacts[0]["name"] != "sizzle.mp4" || got.Artifacts[0]["bytes"] != float64(4096) ||
		got.Artifacts[0]["sha256"] != strings.Repeat("c", 64) || got.Artifacts[0]["content_type"] != "video/mp4" {
		t.Fatalf("artifact = %v", got.Artifacts[0])
	}
	// The pre-probe shape round-trips as the absence it is: no verdict on the
	// artifact, no key in the JSON.
	if _, ok := got.Artifacts[0]["probe"]; ok {
		t.Fatalf("a record with no recorded verdict writes one: %v", got.Artifacts[0])
	}
	if p, ok := got.Artifacts[1]["probe"].(map[string]any); !ok || p["ran"] != true || p["exit_code"] != float64(0) {
		t.Fatalf("probe = %v", got.Artifacts[1]["probe"])
	}
	rec.Artifacts = nil
	data, err = json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"artifacts":`) {
		t.Fatalf("a record with no artifacts writes the key: %s", data)
	}
}

// TestRecordReadsAPreChangeResultJSON is Review Focus 8's round trip: a
// result.json written before this change — no artifacts, no render, no
// inputs — unmarshals into Record with every new field empty and nothing
// else changed. It is the compatibility claim in the plan's Global
// Constraints as a test: a stored record stays readable by every version,
// because the new keys are optional and no reader is required to know them.
//
// Mutation (give ArtifactFile a non-omitempty json tag, or add artifacts to
// the schema's required list): the re-marshal below writes a key an old
// record never had, and this test fails.
func TestRecordReadsAPreChangeResultJSON(t *testing.T) {
	// Exactly the shape 0.7.5's runner wrote: the fields it set, and none of
	// the ones added since (render, artifacts, inputs).
	const pre = `{
	  "version": 1,
	  "run_id": "20260926-221530-abcd",
	  "repo": "acme/app",
	  "workflow": "app",
	  "status": "succeeded",
	  "stage": "writeback",
	  "outcome": "ready",
	  "branch": "fugaro/add-a-feature",
	  "head_sha": "abcdef1234567",
	  "cost_usd": 4.12,
	  "started_at": "2026-09-26T22:15:30Z"
	}`
	var rec Record
	if err := json.Unmarshal([]byte(pre), &rec); err != nil {
		t.Fatalf("a pre-change result.json does not unmarshal: %v", err)
	}
	if rec.Artifacts != nil || rec.Render != nil {
		t.Fatalf("a pre-change record gained fields: artifacts %v, render %v", rec.Artifacts, rec.Render)
	}
	if rec.RunID != runID || rec.Repo != "acme/app" || rec.Status != StatusSucceeded ||
		rec.Outcome != OutcomeReady || rec.HeadSHA != "abcdef1234567" || rec.CostUSD != 4.12 {
		t.Fatalf("the record's own fields changed: %+v", rec)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"artifacts":`, `"render":`} {
		if strings.Contains(string(data), key) {
			t.Fatalf("re-marshalling a pre-change record writes %s: %s", key, data)
		}
	}
}
