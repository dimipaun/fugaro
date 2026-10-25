package task

import (
	"encoding/json"
	"strings"
	"testing"
)

// specHead is the least a spec needs to parse, with the request's own two
// fields appended by each case below.
const specHead = `{"version":1,"run_id":"20261025-120000-a1b2","repo":"acme/app","ref":"main","task":"x"`

// TestSpecRigorRequest: the run's own rigor request travels in the task
// (fugaro run --mode, --path; docs/rigor-modes implementation-plan T1.6). It
// is named rigor and not mode because Spec.Mode is already the recipe's mode
// (F22's collision), and the vocabulary is closed here for the same reason
// the config's is: a mode name outside it is a launcher bug, not a run's
// opinion, and the runner must not read it as one.
//
// Mutation (drop the Rigor case from Validate): the near misses below parse
// and the test fails on them.
func TestSpecRigorRequest(t *testing.T) {
	for _, mode := range []string{"", "poc", "startup", "pro", "critical", "hotfix"} {
		in := specHead + `,"rigor":"` + mode + `"}`
		spec, err := Parse([]byte(in))
		if err != nil {
			t.Errorf("rigor %q: %v", mode, err)
			continue
		}
		if spec.Rigor != mode {
			t.Errorf("rigor %q parsed as %q", mode, spec.Rigor)
		}
	}
	for _, mode := range []string{"Poc", "PRO", "critical ", " critical", "sizzle", "none", "off"} {
		in := specHead + `,"rigor":"` + mode + `"}`
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), "rigor") {
			t.Errorf("rigor %q: err = %v, want a refusal naming rigor", mode, err)
		}
	}
}

// TestSpecDeclaredPaths: the paths a run declares are the ones the resolver
// matches the area rules against at bootstrap (D22's first moment). They are
// bounded — a list and a path are what a hand-written task.json can make
// arbitrarily long, and every one of them is matched against every rule — and
// an empty one is no declaration rather than a declaration of nothing.
func TestSpecDeclaredPaths(t *testing.T) {
	in := specHead + `,"rigor":"critical","paths":["internal/payments/x.go","docs/**"]}`
	spec, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Paths) != 2 || spec.Paths[0] != "internal/payments/x.go" || spec.Paths[1] != "docs/**" {
		t.Fatalf("paths = %v", spec.Paths)
	}

	blank := specHead + `,"paths":["internal/x.go","   "]}`
	if _, err := Parse([]byte(blank)); err == nil || !strings.Contains(err.Error(), "paths[1]") {
		t.Errorf("a blank declared path: err = %v, want a refusal naming it", err)
	}

	many := make([]string, 0, MaxDeclaredPaths+1)
	for range MaxDeclaredPaths + 1 {
		many = append(many, "internal/x.go")
	}
	over, err := json.Marshal(many)
	if err != nil {
		t.Fatal(err)
	}
	tooMany := specHead + `,"paths":` + string(over) + `}`
	if _, err := Parse([]byte(tooMany)); err == nil || !strings.Contains(err.Error(), "paths") {
		t.Errorf("%d declared paths: err = %v, want a refusal", len(many), err)
	}

	long, err := json.Marshal([]string{strings.Repeat("a", MaxDeclaredPathBytes+1)})
	if err != nil {
		t.Fatal(err)
	}
	tooLong := specHead + `,"paths":` + string(long) + `}`
	if _, err := Parse([]byte(tooLong)); err == nil || !strings.Contains(err.Error(), "paths[0]") {
		t.Errorf("an over-long declared path: err = %v, want a refusal naming it", err)
	}
}

// TestSpecRigorRequestOmittedWhenAbsent: a launch that asks for no mode and
// declares no path writes neither field, so its task.json is byte-identical
// to what the release before this one wrote — which is what lets an older
// runner, whose decoder disallows unknown fields, run it.
func TestSpecRigorRequestOmittedWhenAbsent(t *testing.T) {
	data, err := json.Marshal(&Spec{Version: 1, RunID: "20261025-120000-a1b2", Repo: "acme/app", Ref: "main", Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"rigor"`) || strings.Contains(string(data), `"paths"`) {
		t.Fatalf("an absent request is written: %s", data)
	}
}
