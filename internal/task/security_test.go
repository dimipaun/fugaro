package task

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestTaskSecurityFieldRaisesTheLevel is the field itself: a task that says
// `security: true` — the request `fugaro run --security` writes — carries it
// through the decode and the round trip, and the run's own resolution is what
// turns it into level 3 (internal/runner's securityForcedLevels, and the
// runner test that holds the level). The red step of SC10 is here, where the
// field lands: on the tree before it, the strict decoder below refuses the key
// outright.
//
// The decode is the runner's own (Parse decodes with DisallowUnknownFields),
// so what this proves is what a run reads out of the bucket rather than what
// this package would like to write.
func TestTaskSecurityFieldRaisesTheLevel(t *testing.T) {
	const doc = `{"version":1,"run_id":"20261112-120000-abcd","repo":"owner/repo-a","ref":"main","task":"do it","security":true}`
	got, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse refused a task carrying security: true: %v", err)
	}
	if !got.Security {
		t.Fatal("security: true decoded to false, so the run would resolve the levels it resolves without it")
	}
	data, err := got.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatalf("parse of %s: %v", data, err)
	}
	if !back.Security {
		t.Fatalf("the round trip lost the request: %s", data)
	}
}

// TestSecurityFieldIsTheFloorAndNotAnExemption: the field only turns on, so a
// false is the same statement as an absent one — the floor's answer (D1517).
// Both spellings decode to the same spec, and neither writes the key: a task
// that says `security: false` is byte-identical to one that says nothing, which
// is the whole of "there is no way to ask for less" on this side of the wire.
func TestSecurityFieldIsTheFloorAndNotAnExemption(t *testing.T) {
	const head = `{"version":1,"run_id":"20261112-120000-abcd","repo":"owner/repo-a","ref":"main","task":"do it"`
	absent, err := Parse([]byte(head + `}`))
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Parse([]byte(head + `,"security":false}`))
	if err != nil {
		t.Fatalf("Parse refused security: false: %v", err)
	}
	if explicit.Security {
		t.Fatal("security: false decoded to true")
	}
	absentData, err := absent.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	explicitData, err := explicit.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(absentData, explicitData) {
		t.Errorf("a task that says security: false is not the task that says nothing:\n%s\n%s", absentData, explicitData)
	}
	if strings.Contains(string(explicitData), "security") {
		t.Errorf("a task that asks for nothing writes the key anyway: %s", explicitData)
	}
}

// TestSecurityFieldNeedsARunnerThatKnowsIt is what the launch's skew gate
// exists for (securityRequestSince): a runner built before the field refuses
// the whole spec rather than dropping the request, because Parse decodes with
// DisallowUnknownFields. The old-shaped decoder below is this package's Spec
// without the field, copied field for field as TestOldRunnerRefusesOAuthTokenField
// copies one, so it proves what such a binary actually does.
func TestSecurityFieldNeedsARunnerThatKnowsIt(t *testing.T) {
	spec := Spec{Version: 1, RunID: "20261112-120000-abcd", Repo: "owner/repo-a", Ref: "main", Task: "do it", Security: true}
	data, err := spec.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	type oldSpec struct {
		Version  int    `json:"version"`
		RunID    string `json:"run_id"`
		Repo     string `json:"repo"`
		Ref      string `json:"ref"`
		Workflow string `json:"workflow,omitempty"`
		Task     string `json:"task,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var old oldSpec
	if err := dec.Decode(&old); err == nil {
		t.Fatal("an old-shaped decoder accepted the security request silently: a runner that does not know the field would run the task with the request dropped, rather than refusing it at bootstrap")
	}
}
