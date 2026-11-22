package schemas_test

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestAnOldRecordWithModeOffStillValidates is AB11's schemas half (R7, F14):
// the word that was the budget mode is still one a stored record may carry.
// `policy.effective.mode` is in a record of version 1 that old runs already
// wrote, and a run's policy resolved the old word whenever a repository's
// `fugaro.yaml`, its branch, or the project's published policy set it (F14).
// The version is not bumped and nothing rewrites a stored record, so the enum
// keeps the word and the description says what it is: what a record written
// before this release carries, and not a value any writer produces now.
func TestAnOldRecordWithModeOffStillValidates(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	rec := runstore.Record{
		Version: 1, RunID: "20260601-100000-abcd", Status: runstore.StatusSucceeded,
		Stage: "writeback", Outcome: runstore.OutcomeReady, StartedAt: at,
		Policy: &runstore.PolicyRecord{Effective: runstore.PolicyEffective{Mode: "off"}},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the schema refuses a record a run wrote before this release: %v\n%s", err, data)
	}

	// The description is where a reader who finds the word learns why it is
	// still here and that nothing writes it: without it the enum reads as a
	// mode a run may be launched with, which is exactly what it is not.
	raw, err := os.ReadFile("result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties struct {
			Policy struct {
				Properties struct {
					Effective struct {
						Properties struct {
							Mode struct {
								Enum        []string `json:"enum"`
								Description string   `json:"description"`
							} `json:"mode"`
						} `json:"properties"`
					} `json:"effective"`
				} `json:"properties"`
			} `json:"policy"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	mode := doc.Properties.Policy.Properties.Effective.Properties.Mode
	if !slices.Contains(mode.Enum, "off") {
		t.Errorf("policy.effective.mode: the enum is %v, and the record of a run that ran before this release carries off", mode.Enum)
	}
	if !strings.Contains(mode.Description, "nothing writes it") {
		t.Errorf("policy.effective.mode: the description does not say that nothing writes the word any more: %q", mode.Description)
	}
}
