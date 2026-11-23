package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

const upsertDoc = `{"base_rev":7,"ops":[{"op":"work_item_upsert","work_item":{
"id":"chk640-probe-20261122","title":"Check 640 probe",
"repo":"DimiPaun/Fugaro","recipe":"default","rigor_tier":"trivial","spec":"Forge-Labels",
"brief":"Fix one true typo.\n\n  in a doc  ","depends_on":[]}}]}`

func digestOf(t *testing.T, doc string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(strings.NewReader(doc), &out)
	return strings.TrimSpace(out.String()), err
}

// TestDigestEqualsWhatTheStateHolds: the printed digest is the one of the item
// a state at the patch's revision holds after the patch lands, including the
// folding Apply does (repository and spec case, the brief's whitespace).
func TestDigestEqualsWhatTheStateHolds(t *testing.T) {
	got, err := digestOf(t, upsertDoc)
	if err != nil {
		t.Fatal(err)
	}
	p, err := orchestrator.DecodePatch([]byte(upsertDoc))
	if err != nil {
		t.Fatal(err)
	}
	base := orchestrator.State{SchemaVersion: orchestrator.SchemaVersion, Rev: 7, AgentID: "fugaro", Role: orchestrator.RoleOrchestrator,
		Facts: map[string]orchestrator.Fact{}}
	next, err := orchestrator.Apply(base, p, orchestrator.Actor{Class: orchestrator.Session, Name: "s", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	w, ok := next.Item("chk640-probe-20261122")
	if !ok {
		t.Fatal("no item")
	}
	if want := string(orchestrator.SpecDigest(w)); got != want {
		t.Fatalf("printed %s, the state holds %s", got, want)
	}
	if w.Repo != "dimipaun/fugaro" || w.Spec != "forge-labels" {
		t.Errorf("the item is not folded as expected: %+v", w)
	}

	// The approve op built from that digest lands for a person.
	ap := orchestrator.Patch{BaseRev: next.Rev, Ops: []orchestrator.Op{{Op: orchestrator.OpWorkItemApprove,
		Approve: &orchestrator.ApproveOp{ID: w.ID, SpecDigest: orchestrator.Digest(got)}}}}
	approved, err := orchestrator.Apply(next, ap, orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: time.Now()})
	if err != nil {
		t.Fatalf("the approval with the printed digest was refused: %v", err)
	}
	if i, _ := approved.Item(w.ID); i.Approved == nil {
		t.Error("the item is not approved")
	}
}

func TestDigestIgnoresTheRevisionAndChangesWithTheSpec(t *testing.T) {
	a, err := digestOf(t, upsertDoc)
	if err != nil {
		t.Fatal(err)
	}
	b, err := digestOf(t, strings.Replace(upsertDoc, `"base_rev":7`, `"base_rev":900`, 1))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("the revision changed the digest: %s vs %s", a, b)
	}
	c, err := digestOf(t, strings.Replace(upsertDoc, "one true typo", "two true typos", 1))
	if err != nil {
		t.Fatal(err)
	}
	if a == c {
		t.Error("an edited brief kept its digest")
	}
}

func TestRefusals(t *testing.T) {
	for name, doc := range map[string]string{
		"not json":     `nope`,
		"no ops":       `{"base_rev":1,"ops":[]}`,
		"other op":     `{"base_rev":1,"ops":[{"op":"goal_set","goal":{"goal":"x"}}]}`,
		"incomplete":   `{"base_rev":1,"ops":[{"op":"work_item_upsert","work_item":{"id":"a-b","title":"t"}}]}`,
		"two upserts":  strings.Replace(upsertDoc, `}}]}`, `}},`+`{"op":"work_item_upsert","work_item":{"id":"x-y","title":"t"}}]}`, 1),
		"daemon field": strings.Replace(upsertDoc, `"depends_on":[]`, `"phase":"queued"`, 1),
	} {
		if out, err := digestOf(t, doc); err == nil {
			t.Errorf("%s: printed %q", name, out)
		}
	}
	var v any
	if json.Unmarshal([]byte(upsertDoc), &v) != nil {
		t.Fatal("the fixture is not JSON")
	}
}
