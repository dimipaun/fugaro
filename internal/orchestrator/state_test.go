package orchestrator_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestStateRefusesAnUnknownField is why the validator is the shape check the
// design leans on. The rules refuse a token writer outright (O2), so a
// document that exists at all was written by the person's own credential,
// which IAM lets past those rules; what keeps a malformed or unknown-to-this-
// release one from being read is the strict decode.
//
// Mutation: drop DisallowUnknownFields from decodeStrict and both cases below
// decode, carrying a field a later release will mean something by.
func TestStateRefusesAnUnknownField(t *testing.T) {
	raw := readFixture(t)
	s, err := orchestrator.DecodeState(raw)
	if err != nil {
		t.Fatalf("the fixture is not a state this release reads: %v", err)
	}
	if len(s.WorkItems) != 2 || s.WorkItems[0].ID != "wi-1" {
		t.Fatalf("the fixture decoded as %d work items, first %q", len(s.WorkItems), s.WorkItems[0].ID)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, map[string]any)
	}{
		{"at the top of the document", func(_ *testing.T, m map[string]any) {
			m["status"] = "running"
		}},
		{"beside a work item's phase", func(t *testing.T, m map[string]any) {
			// The item's phase is not called status: the run record has a
			// status of its own, the daemon folds one into the other, and a
			// name that meant two things would be read as the wrong one
			// (TestWorkItemFieldsAreNotTheRunRecordsFields).
			workItemAt(t, m, 0)["status"] = "running"
		}},
		{"on an intent", func(t *testing.T, m map[string]any) {
			listAt(t, m, "intents")[0].(map[string]any)["attempt"] = 1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := document(t, raw)
			tc.mutate(t, doc)
			_, err := orchestrator.DecodeState(remarshal(t, doc))
			if !errors.Is(err, orchestrator.ErrInvalid) {
				t.Fatalf("a document with an unknown field decoded: err = %v, want it to be refused as %v", err, orchestrator.ErrInvalid)
			}
			if !strings.Contains(err.Error(), "does not model") {
				t.Fatalf("the refusal does not say the field is unknown: %v", err)
			}
		})
	}
}

// TestOversizeOrMalformedStateIsRefusedClientSide is the other half of the
// same check: a document that is the right shape but the wrong size, or the
// right size and no shape at all, is refused where it is read rather than
// carried into a daemon that would act on it.
//
// Mutation: move any of these bounds (a cap, a pattern, the schema version)
// and the case that names it stops failing.
func TestOversizeOrMalformedStateIsRefusedClientSide(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, map[string]any)
		want   error
	}{
		{"a schema version this release does not read", func(_ *testing.T, m map[string]any) {
			m["schema_version"] = orchestrator.SchemaVersion + 1
		}, orchestrator.ErrSchemaVersion},
		{"a phase that is not one of the seven", func(t *testing.T, m map[string]any) {
			workItemAt(t, m, 0)["phase"] = "shipped"
		}, orchestrator.ErrInvalid},
		{"a repository that is not owner/name", func(t *testing.T, m map[string]any) {
			workItemAt(t, m, 1)["repo"] = "owner"
		}, orchestrator.ErrInvalid},
		{"a brief over its cap", func(t *testing.T, m map[string]any) {
			workItemAt(t, m, 1)["brief"] = strings.Repeat("a", orchestrator.MaxBriefRunes+1)
		}, orchestrator.ErrTooLarge},
		{"a decision that waits on nobody in particular", func(t *testing.T, m map[string]any) {
			listAt(t, m, "decisions")[0].(map[string]any)["waiting_on"] = "anyone"
		}, orchestrator.ErrInvalid},
		{"counters that count more calls this hour than today", func(_ *testing.T, m map[string]any) {
			m["counters"] = map[string]any{"decisions_hour": 5, "decisions_day": 1}
		}, orchestrator.ErrInvalid},
		{"a fact key that is not a name", func(_ *testing.T, m map[string]any) {
			m["facts"].(map[string]any)["a:key"] = map[string]any{"value": "x", "by": "daemon"}
		}, orchestrator.ErrInvalid},
		{"an intent id that names no attempt", func(t *testing.T, m map[string]any) {
			listAt(t, m, "intents")[0].(map[string]any)["id"] = "wi-1"
		}, orchestrator.ErrInvalid},
		{"a document over the size target with every field inside its cap", func(_ *testing.T, m map[string]any) {
			// Nothing here breaks a cap of its own: sixty-four facts of the
			// longest value a fact may carry are a document of twenty-odd
			// kilobytes, and the target is what says no.
			facts := map[string]any{}
			for i := range orchestrator.MaxFacts {
				facts[factKey(i)] = map[string]any{
					"value": strings.Repeat("a", orchestrator.MaxFactValueRunes),
					"by":    "daemon",
				}
			}
			m["facts"] = facts
		}, orchestrator.ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := document(t, readFixture(t))
			tc.mutate(t, doc)
			_, err := orchestrator.DecodeState(remarshal(t, doc))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestMigrateFromThePreviousSchemaVersion is what makes `fugaro update` the
// whole upgrade (G11): a node written by the release before this one is raised
// to this schema's version as it is read, so a daemon that starts after an
// update reads the state that is already there. A document from a release
// *after* this one is left alone and the decode refuses it, which is the
// direction that must stop rather than guess.
//
// Mutation: drop the Migrate call from DecodeState and the version-0 document
// below is refused as a schema version this release does not read.
func TestMigrateFromThePreviousSchemaVersion(t *testing.T) {
	current, err := orchestrator.DecodeState(readFixture(t))
	if err != nil {
		t.Fatalf("the fixture is not a state this release reads: %v", err)
	}
	doc := document(t, readFixture(t))
	if doc["schema_version"] == 0 {
		t.Fatal("the fixture does not carry a schema version to step back from")
	}
	doc["schema_version"] = 0
	previous := remarshal(t, doc)

	raised, changed := orchestrator.Migrate(mustDecode(t, previous, false))
	if !changed {
		t.Fatal("Migrate left a document from the previous schema version where it was")
	}
	if raised.SchemaVersion != orchestrator.SchemaVersion {
		t.Fatalf("Migrate raised the version to %d, want %d", raised.SchemaVersion, orchestrator.SchemaVersion)
	}
	if raised.Rev != current.Rev || len(raised.WorkItems) != len(current.WorkItems) {
		t.Fatalf("Migrate changed more than the version: rev %d -> %d, %d items -> %d",
			current.Rev, raised.Rev, len(current.WorkItems), len(raised.WorkItems))
	}
	if got, want := orchestrator.SpecDigest(raised.WorkItems[0]), orchestrator.SpecDigest(current.WorkItems[0]); got != want {
		t.Fatalf("the migrated item digests to %s and the one it came from to %s", got, want)
	}

	decoded, err := orchestrator.DecodeState(previous)
	if err != nil {
		t.Fatalf("a document from the previous schema version did not decode: %v", err)
	}
	if decoded.SchemaVersion != orchestrator.SchemaVersion {
		t.Fatalf("the decoded document says version %d, want %d", decoded.SchemaVersion, orchestrator.SchemaVersion)
	}

	future := current
	future.SchemaVersion = orchestrator.SchemaVersion + 1
	if again, changed := orchestrator.Migrate(future); changed || again.SchemaVersion != future.SchemaVersion {
		t.Fatalf("Migrate touched a document from a later release: changed = %v, version = %d", changed, again.SchemaVersion)
	}
	rawFuture, err := json.Marshal(future)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.DecodeState(rawFuture); !errors.Is(err, orchestrator.ErrSchemaVersion) {
		t.Fatalf("a document from a later release decoded: err = %v, want %v", err, orchestrator.ErrSchemaVersion)
	}
}

// TestSpentApprovalAtIsBookkeepingAndSurvivesMigrate pins the one field whose
// loss would be expensive out of proportion to its size. SpentApprovalAt is
// what says the retry an approval covered is spent; it moves no digest, since
// it is not part of the spec, and it must survive the raise from the previous
// schema version, or an update would hand a spent approval back to the daemon
// as an unspent one.
//
// Mutation: put spent_approval_at in the digested set and the approve test's
// digest check fails; drop it in Migrate and the round-trip below fails.
func TestSpentApprovalAtIsBookkeepingAndSurvivesMigrate(t *testing.T) {
	s := mustDecode(t, readFixture(t), true)
	spent := time.Date(2026, 11, 7, 6, 0, 0, 0, time.UTC)
	s.WorkItems[0].Approved = nil
	s.WorkItems[0].SpentApprovalAt = &spent
	s.Intents[0].ApprovalAt = spent

	raw, err := orchestrator.EncodeState(s)
	if err != nil {
		t.Fatalf("the state with a spent approval does not encode: %v", err)
	}
	back, err := orchestrator.DecodeState(raw)
	if err != nil {
		t.Fatalf("the state with a spent approval does not decode: %v", err)
	}
	if back.WorkItems[0].SpentApprovalAt == nil || !back.WorkItems[0].SpentApprovalAt.Equal(spent) {
		t.Fatalf("the spent approval marker came back as %v", back.WorkItems[0].SpentApprovalAt)
	}

	previous := bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":0`), 1)
	raised, err := orchestrator.DecodeState(previous)
	if err != nil {
		t.Fatalf("a document from the previous schema version carrying a spent approval did not decode: %v", err)
	}
	if raised.WorkItems[0].SpentApprovalAt == nil {
		t.Fatal("raising the schema version dropped the spent approval marker")
	}
}

// TestAllowedPhaseTransitions is the phase table, both as a function and
// through Apply: a caller cannot move an item by writing a phase the table
// does not name, because the upsert refuses it rather than storing it.
//
// The retry's own path is here too: failed and blocked go back to queued,
// which is the way a person's new approval re-opens an item.
//
// Mutation: widen any row (say done -> queued) and the transition assertion
// fails; drop the CanMoveTo call from applyUpsert and the Apply case after the
// table does.
func TestAllowedPhaseTransitions(t *testing.T) {
	allowed := map[orchestrator.Phase][]orchestrator.Phase{
		orchestrator.PhaseQueued:    {orchestrator.PhaseRunning, orchestrator.PhaseFailed, orchestrator.PhaseBlocked, orchestrator.PhaseCancelled},
		orchestrator.PhaseRunning:   {orchestrator.PhaseReview, orchestrator.PhaseDone, orchestrator.PhaseFailed, orchestrator.PhaseBlocked, orchestrator.PhaseCancelled},
		orchestrator.PhaseReview:    {orchestrator.PhaseDone, orchestrator.PhaseFailed, orchestrator.PhaseBlocked, orchestrator.PhaseCancelled},
		orchestrator.PhaseDone:      {},
		orchestrator.PhaseFailed:    {orchestrator.PhaseQueued, orchestrator.PhaseCancelled},
		orchestrator.PhaseBlocked:   {orchestrator.PhaseQueued, orchestrator.PhaseCancelled},
		orchestrator.PhaseCancelled: {},
	}
	for _, from := range orchestrator.Phases {
		for _, to := range orchestrator.Phases {
			// A phase may always move to itself: a tick that rewrites what it
			// already recorded changes nothing.
			want := from == to || slices.Contains(allowed[from], to)
			if got := from.CanMoveTo(to); got != want {
				t.Errorf("%s -> %s: CanMoveTo = %v, want %v", from, to, got, want)
			}
		}
	}

	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	s := stateWithAQueuedItem(t, now)
	running, done, queued := orchestrator.PhaseRunning, orchestrator.PhaseDone, orchestrator.PhaseQueued
	runID := "20261107-090000-abcd"

	atRunning := mustApply(t, s, orchestrator.Daemon, "orchestrator-a", now, phaseOp("wi-1", &running, &runID))
	atDone := mustApply(t, atRunning, orchestrator.Daemon, "orchestrator-a", now, phaseOp("wi-1", &done, nil))
	if _, err := applyAs(t, atDone, orchestrator.Daemon, "orchestrator-a", now, phaseOp("wi-1", &queued, nil)); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("a done item went back to the queue: err = %v, want %v", err, orchestrator.ErrInvalid)
	}
	if _, err := applyAs(t, s, orchestrator.Daemon, "orchestrator-a", now, phaseOp("wi-1", &done, nil)); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("a queued item skipped straight to done: err = %v, want %v", err, orchestrator.ErrInvalid)
	}
}

// TestWorkItemFieldsAreNotTheRunRecordsFields pins the naming rule the phase's
// name comes from. The daemon folds a run record into a work item, a person
// reads both, and the console draws figures from both: a field shared between
// them has to mean the same thing in both, or a figure is read out of the
// wrong document with no error anywhere.
//
// It is reflection rather than a copied list, so a field added to either
// struct lands in the comparison without anyone remembering it.
//
// Mutation: rename the item's phase to status and the shared set grows by one.
func TestWorkItemFieldsAreNotTheRunRecordsFields(t *testing.T) {
	item := jsonNames(reflect.TypeOf(orchestrator.WorkItem{}))
	record := jsonNames(reflect.TypeOf(runstore.Record{}))

	// The three shared names mean the same thing on both sides: the repository
	// the work is in, the recipe it is carried out with, and the run doing it.
	// Nothing else may.
	if shared := intersect(item, record); !slices.Equal(shared, []string{"recipe", "repo", "run_id"}) {
		t.Fatalf("a work item and a run record share %v, want [recipe repo run_id]", shared)
	}
	if got := intersect(jsonNames(reflect.TypeOf(orchestrator.State{})), record); len(got) != 0 {
		t.Fatalf("the state document and the run record share %v, and neither name means the same in both", got)
	}
	if slices.Contains(item, "status") {
		t.Fatal("a work item carries a status: the run record's status is a different thing and the item's phase has its own name")
	}
}

// TestSchemasMatchTheStructs keeps the two documents the wire format is
// described by honest. Nothing generates them from the structs, and a console
// or a doc reader that trusts the schema would be wrong in a way nothing else
// catches: a field that is written and not declared, a field declared but
// never written, a required list that does not match what the encoder omits.
//
// The patch schema's op branches are checked against the op vocabulary too,
// because one shape cannot carry two payloads and the schema is the only place
// that says so to a reader who is not the Go compiler.
//
// Mutation: add a field to WorkItem and not to the schema, or mark the item's
// attempts optional in the schema, and this fails.
func TestSchemasMatchTheStructs(t *testing.T) {
	for _, tc := range []struct {
		file  string
		root  reflect.Type
		defs  map[string]reflect.Type
		extra []string
	}{
		{
			file: "orchestrator-state.schema.json",
			root: reflect.TypeOf(orchestrator.State{}),
			defs: map[string]reflect.Type{
				"work_item": reflect.TypeOf(orchestrator.WorkItem{}),
				"approval":  reflect.TypeOf(orchestrator.Approval{}),
				"decision":  reflect.TypeOf(orchestrator.Decision{}),
				"fact":      reflect.TypeOf(orchestrator.Fact{}),
				"archived":  reflect.TypeOf(orchestrator.Archived{}),
				"intent":    reflect.TypeOf(orchestrator.Intent{}),
				"counters":  reflect.TypeOf(orchestrator.Counters{}),
			},
		},
		{
			file:  "orchestrator-patch.schema.json",
			root:  reflect.TypeOf(orchestrator.Patch{}),
			extra: []string{"op"},
			defs: map[string]reflect.Type{
				"work_item_op": reflect.TypeOf(orchestrator.WorkItemOp{}),
				"intent_op":    reflect.TypeOf(orchestrator.IntentOp{}),
				"approve_op":   reflect.TypeOf(orchestrator.ApproveOp{}),
				"archive_op":   reflect.TypeOf(orchestrator.ArchiveOp{}),
				"decision_op":  reflect.TypeOf(orchestrator.DecisionOp{}),
				"resolve_op":   reflect.TypeOf(orchestrator.ResolveOp{}),
				"fact_op":      reflect.TypeOf(orchestrator.FactOp{}),
				"goal_op":      reflect.TypeOf(orchestrator.GoalOp{}),
			},
		},
	} {
		t.Run(tc.file, func(t *testing.T) {
			doc := schemaDocument(t, "../../schemas/"+tc.file)
			compileSchema(t, "../../schemas/"+tc.file)
			checkSchemaObject(t, tc.file, doc, tc.root)
			defs := doc["$defs"].(map[string]any)
			if len(defs) != len(tc.defs)+len(tc.extra) {
				t.Fatalf("the schema has %d definitions, and this release names %d structs and %v among its shapes", len(defs), len(tc.defs), tc.extra)
			}
			for name, typ := range tc.defs {
				def, ok := defs[name].(map[string]any)
				if !ok {
					t.Fatalf("the schema has no definitions.%s", name)
				}
				checkSchemaObject(t, tc.file+" $defs."+name, def, typ)
			}
			for _, name := range tc.extra {
				if _, ok := defs[name]; !ok {
					t.Fatalf("%s has no definitions.%s", tc.file, name)
				}
			}
			if op, ok := defs["op"].(map[string]any); ok {
				checkSchemaOps(t, tc.file, op)
			}
		})
	}
}

// TestSpecDigestCoversEveryFieldButTheBookkeepingList is what makes an
// approval mean one spec and not another. Every field that describes the work
// moves the digest when it changes, the six bookkeeping fields move it not at
// all, and the two sets together are every field of the struct — so a field
// added to a work item is digested by default and only leaves the digest on
// purpose. That is what an edit to a brief, a repository or a dependency does:
// it makes the approval off (TestEditAfterApprovalRevokes).
//
// Mutation: drop a field from the digesting loop (say rigor_tier) and the
// digest stops moving for it; add one to bookkeepingFields by mistake and the
// same assertion fails from the other side.
func TestSpecDigestCoversEveryFieldButTheBookkeepingList(t *testing.T) {
	want := []string{"approved", "attempts", "note", "phase", "run_id", "spent_approval_at"}
	got := orchestrator.BookkeepingFields()
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("the bookkeeping list is %v, want %v", got, want)
	}

	base := orchestrator.WorkItem{
		ID:        "wi-1",
		Title:     "land the state migration",
		Repo:      "owner/repo",
		Recipe:    "standard",
		RigorTier: "medium",
		Brief:     "Move the sweep onto the daemon.",
		DependsOn: []string{"wi-0"},
		Phase:     orchestrator.PhaseQueued,
	}
	digest := orchestrator.SpecDigest(base)
	typ := reflect.TypeOf(base)
	for i := range typ.NumField() {
		f := typ.Field(i)
		name := jsonName(f)
		moved := orchestrator.SpecDigest(changedField(t, base, f))
		if slices.Contains(want, name) {
			if moved != digest {
				t.Errorf("changing %s moved the spec digest, and it is bookkeeping", name)
			}
			continue
		}
		if moved == digest {
			t.Errorf("changing %s left the spec digest alone, and an approval covers it", name)
		}
	}

	// The list has to name fields the struct has: a name on it that no field
	// carries would silently digest nothing at all.
	fields := jsonNames(typ)
	for _, name := range want {
		if !slices.Contains(fields, name) {
			t.Fatalf("%q is on the bookkeeping list and is not a field of a work item", name)
		}
	}
}

// TestRetentionKeepsStateUnderTheCap is the size target's other end: a node
// that has grown past it is brought back by ageing out history, in the order
// that costs least, and the work itself survives. What must not happen is the
// reverse — a live item dropped, or an approval taken away, to make room for
// a pile of facts.
//
// Mutation: stop calling Retain from the daemon's write path and the node
// stays over the target; make shrink drop a live item and the survival
// assertions fail.
func TestRetentionKeepsStateUnderTheCap(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	s := bloatedState(t, now)
	if !orchestrator.OverCap(s) {
		t.Fatalf("the state this test built is %d bytes and not over the target, so retention has nothing to do", stateBytes(t, s))
	}

	kept := orchestrator.Retain(s, now)
	if orchestrator.OverCap(kept) {
		t.Fatalf("retention left the node at %d bytes, over the target of %d", stateBytes(t, kept), orchestrator.MaxStateBytes)
	}
	if _, err := orchestrator.EncodeState(kept); err != nil {
		t.Fatalf("the state retention produced is not one this release reads back: %v", err)
	}
	if kept.Goal != s.Goal {
		t.Fatal("retention dropped the goal")
	}
	for _, id := range []string{"wi-live", "wi-approved"} {
		if _, ok := kept.Item(id); !ok {
			t.Fatalf("retention dropped %s, which is work rather than history", id)
		}
	}
	if kept.Decisions[0].Resolved() {
		t.Fatal("retention dropped the open decision, which is what the node is for")
	}
	if len(kept.Facts) >= len(s.Facts) {
		t.Fatalf("retention kept all %d facts of a node that was over the target", len(kept.Facts))
	}
}

// TestRetentionNeverDropsAnIntentInsideTheCapWindow is the first of the three
// never-evict rules, and the reason the caps can be trusted: they are derived
// from the committed intents, so an intent that is inside the window a cap
// counts over must not be evicted to make room — a session that inflates its
// own facts could otherwise raise the allowance a cycle has left.
//
// The node is left over MaxIntents on purpose when everything in the window is
// all there is: fail closed, with the daemon not spawning, rather than a count
// moving.
//
// Mutation: drop the RetentionWindow rule from protectedIntent and the
// in-window intents below are evicted, which is the cap resetting itself.
func TestRetentionNeverDropsAnIntentInsideTheCapWindow(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	s := stateWithAQueuedItem(t, now)
	s.Intents = nil
	s.WorkItems = nil
	var inWindow, old []string
	for i := range orchestrator.MaxIntents + 6 {
		it := intentFor(t, "wi-gone", i+1, now.Add(-time.Hour), now.Add(-time.Hour))
		s.Intents = append(s.Intents, it)
		inWindow = append(inWindow, it.ID)
	}
	for i := range 20 {
		it := intentFor(t, "wi-long-gone", i+1, now.Add(-48*time.Hour), now.Add(-48*time.Hour))
		s.Intents = append(s.Intents, it)
		old = append(old, it.ID)
	}

	kept := orchestrator.Retain(s, now)
	ids := intentIDs(kept)
	for _, id := range inWindow {
		if !slices.Contains(ids, id) {
			t.Fatalf("retention evicted %s, an intent younger than the %s a cap counts over", id, orchestrator.RetentionWindow)
		}
	}
	for _, id := range old {
		if slices.Contains(ids, id) {
			t.Fatalf("retention kept %s, which is past the window and protects nothing", id)
		}
	}
}

// TestRetentionKeepsTheIntentsOfAStandingApproval is the other two rules: the
// intents an item's standing approval could still spend, and the retry the
// spent marker names. An item paused by a cap or by the kill switch for longer
// than the window must not come back with its count reset and spend the same
// retry twice, so neither of those intents ages out with the window.
//
// Mutation: drop either clause from protectedIntent and the intent it protects
// is evicted below, which is a spent approval being handed back unspent.
func TestRetentionKeepsTheIntentsOfAStandingApproval(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	standing := now.Add(-72 * time.Hour)
	spent := now.Add(-96 * time.Hour)

	s := stateWithAQueuedItem(t, now)
	s.WorkItems = []orchestrator.WorkItem{
		approvedItem(t, "wi-approved", now, standing),
		spentItem(t, "wi-spent", now, spent),
	}
	s.Intents = []orchestrator.Intent{
		intentFor(t, "wi-approved", 1, standing, now.Add(-72*time.Hour)),
		intentFor(t, "wi-spent", 1, spent, now.Add(-96*time.Hour)),
	}
	protected := []string{s.Intents[0].ID, s.Intents[1].ID}
	for i := range orchestrator.MaxIntents {
		s.Intents = append(s.Intents, intentFor(t, "wi-long-gone", i+1, now.Add(-48*time.Hour), now.Add(-48*time.Hour)))
	}

	kept := orchestrator.Retain(s, now)
	ids := intentIDs(kept)
	for _, id := range protected {
		if !slices.Contains(ids, id) {
			t.Fatalf("retention evicted %s, an intent its item's approval still names", id)
		}
	}
	if len(kept.Intents) > orchestrator.MaxIntents {
		t.Fatalf("retention kept %d intents, and %d of the others could be dropped", len(kept.Intents), orchestrator.MaxIntents)
	}
}

// stateWithAQueuedItem is the smallest state the validator accepts: one queued
// work item and nothing else. Tests that need more start here and change the
// one thing they are about.
func stateWithAQueuedItem(t *testing.T, now time.Time) orchestrator.State {
	t.Helper()
	return orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           7,
		AgentID:       "orchestrator-a",
		Role:          orchestrator.RoleOrchestrator,
		Goal:          "keep the repository's pull requests moving without a person watching",
		WorkItems: []orchestrator.WorkItem{{
			ID:        "wi-1",
			Title:     "land the state migration",
			Repo:      "owner/repo",
			Recipe:    "standard",
			RigorTier: "medium",
			Brief:     "Move the sweep to the daemon.",
			Phase:     orchestrator.PhaseQueued,
		}},
		Decisions: []orchestrator.Decision{},
		Facts:     map[string]orchestrator.Fact{},
		Archived:  []orchestrator.Archived{},
		Intents:   []orchestrator.Intent{},
	}
}

// approvedItem is an item a person has approved and nothing has launched yet.
func approvedItem(t *testing.T, id string, now, approvedAt time.Time) orchestrator.WorkItem {
	t.Helper()
	w := workItemOf(id)
	w.Phase = orchestrator.PhaseFailed
	w.Approved = &orchestrator.Approval{
		By:         "owner",
		At:         approvedAt.UTC(),
		SpecDigest: orchestrator.SpecDigest(w),
		Attempts:   orchestrator.ApprovalAttempts,
	}
	return w
}

// spentItem is an item whose approval is spent: the retry it covered already
// ran, and the marker is what says so.
func spentItem(t *testing.T, id string, now, spentAt time.Time) orchestrator.WorkItem {
	t.Helper()
	w := workItemOf(id)
	w.Phase = orchestrator.PhaseRunning
	w.RunID = "20261107-090000-abcd"
	w.Attempts = 2
	at := spentAt.UTC()
	w.SpentApprovalAt = &at
	return w
}

func workItemOf(id string) orchestrator.WorkItem {
	return orchestrator.WorkItem{
		ID:        id,
		Title:     "work that outlives its approval",
		Repo:      "owner/repo",
		Recipe:    "standard",
		RigorTier: "medium",
		Brief:     "The brief of " + id + ".",
		Phase:     orchestrator.PhaseQueued,
	}
}

// intentFor is a committed intent: the id names the item and the attempt, and
// the approval it spends is the At it carries.
func intentFor(t *testing.T, item string, attempt int, approvalAt, at time.Time) orchestrator.Intent {
	t.Helper()
	w := workItemOf(item)
	return orchestrator.Intent{
		ID:         orchestrator.IntentID(item, attempt),
		RunID:      "20261107-090000-abcd",
		SpecDigest: orchestrator.SpecDigest(w),
		ApprovalAt: approvalAt.UTC(),
		At:         at.UTC(),
	}
}

// bloatedState is a node that has grown past the size target the way one does:
// a couple of live items with long briefs, and a long tail of history a
// session and the daemon have written.
func bloatedState(t *testing.T, now time.Time) orchestrator.State {
	t.Helper()
	long := strings.Repeat("filler ", 250)
	s := stateWithAQueuedItem(t, now)
	s.WorkItems = []orchestrator.WorkItem{
		{ID: "wi-live", Title: "work in flight", Repo: "owner/repo", Recipe: "standard", RigorTier: "medium",
			Brief: long, Note: strings.Repeat("n", orchestrator.MaxNoteRunes), Phase: orchestrator.PhaseQueued},
		approvedItem(t, "wi-approved", now, now.Add(-30*time.Minute)),
	}
	s.WorkItems[1].Brief = long
	for i := range 12 {
		w := workItemOf(intentItemID(i))
		w.Phase = orchestrator.PhaseDone
		w.Brief = long
		s.WorkItems = append(s.WorkItems, w)
	}
	s.Decisions = []orchestrator.Decision{{
		ID:        "ask-open",
		Question:  "does the sweep keep its own script?",
		WaitingOn: orchestrator.OnPerson,
		At:        now.Add(-time.Hour),
	}}
	for i := range 20 {
		answered := now.Add(-time.Duration(i+1) * time.Hour)
		s.Decisions = append(s.Decisions, orchestrator.Decision{
			ID:         decisionID(i),
			Question:   strings.Repeat("q", orchestrator.MaxQuestionRunes),
			WaitingOn:  orchestrator.OnPerson,
			At:         now.Add(-time.Duration(i+2) * time.Hour),
			Answer:     strings.Repeat("a", orchestrator.MaxAnswerRunes),
			By:         orchestrator.Person,
			ResolvedAt: &answered,
		})
	}
	s.Facts = map[string]orchestrator.Fact{}
	for i := range orchestrator.MaxFacts {
		s.Facts[factKey(i)] = orchestrator.Fact{Value: strings.Repeat("v", orchestrator.MaxFactValueRunes), By: orchestrator.Daemon}
	}
	for i := range 40 {
		s.Archived = append(s.Archived, orchestrator.Archived{ID: intentItemID(i), Outcome: "done", At: now.Add(-time.Duration(i) * time.Hour)})
	}
	for i := range 40 {
		s.Intents = append(s.Intents, intentFor(t, intentItemID(i), 1, now.Add(-49*time.Hour), now.Add(-48*time.Hour)))
	}
	return s
}

func intentItemID(i int) string { return "wi-old-" + twoDigits(i) }
func decisionID(i int) string   { return "ask-" + twoDigits(i) }
func factKey(i int) string      { return "fact." + twoDigits(i) }
func twoDigits(i int) string    { return string(rune('a'+i/10)) + string(rune('a'+i%10)) }

func intentIDs(s orchestrator.State) []string {
	ids := make([]string, 0, len(s.Intents))
	for _, it := range s.Intents {
		ids = append(ids, it.ID)
	}
	return ids
}

func stateBytes(t *testing.T, s orchestrator.State) int {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return len(raw)
}

// readFixture is the state document under testdata, which is the one the
// reader, the migration and the schemas are all checked against.
func readFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/state-a.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// mustDecode decodes the fixture, and validates it only when asked: a test
// that is about what a document with one thing wrong does with it needs the
// state before the validator sees it.
func mustDecode(t *testing.T, raw []byte, validate bool) orchestrator.State {
	t.Helper()
	if validate {
		s, err := orchestrator.DecodeState(raw)
		if err != nil {
			t.Fatalf("the document does not decode: %v", err)
		}
		return s
	}
	var s orchestrator.State
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("the document does not parse: %v", err)
	}
	return s
}

func document(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("the document does not parse: %v", err)
	}
	return m
}

func remarshal(t *testing.T, m map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func workItemAt(t *testing.T, m map[string]any, i int) map[string]any {
	t.Helper()
	return listAt(t, m, "work_items")[i].(map[string]any)
}

func listAt(t *testing.T, m map[string]any, name string) []any {
	t.Helper()
	list, ok := m[name].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("the document carries no %s", name)
	}
	return list
}

// jsonName is a field's name in a document.
func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return name
}

// jsonNames is every name a struct writes.
func jsonNames(typ reflect.Type) []string {
	names := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		if name := jsonName(typ.Field(i)); name != "" && name != "-" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// jsonOptional are the names the encoder leaves out when they are empty.
func jsonOptional(typ reflect.Type) []string {
	names := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, rest, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if slices.Contains(strings.Split(rest, ","), "omitempty") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func intersect(a, b []string) []string {
	out := []string{}
	for _, name := range a {
		if slices.Contains(b, name) {
			out = append(out, name)
		}
	}
	return out
}

// changedField is the work item with one field set to something else, which is
// what the digest test changes it by.
func changedField(t *testing.T, w orchestrator.WorkItem, f reflect.StructField) orchestrator.WorkItem {
	t.Helper()
	v := reflect.ValueOf(&w).Elem().FieldByName(f.Name)
	switch v.Kind() {
	case reflect.String:
		v.SetString(v.String() + " (changed)")
	case reflect.Int:
		v.SetInt(v.Int() + 1)
	case reflect.Slice:
		v.Set(reflect.ValueOf([]string{"wi-changed"}))
	case reflect.Pointer:
		v.Set(reflect.New(f.Type.Elem()))
		if p, ok := v.Interface().(*orchestrator.Approval); ok {
			p.By = "someone"
			p.At = time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
			p.SpecDigest = orchestrator.SpecDigest(w)
			p.Attempts = orchestrator.ApprovalAttempts
		}
	default:
		t.Fatalf("the digest test does not know how to change a %s field", f.Type)
	}
	return w
}

// schemaDocument reads a schema file as a document.
func schemaDocument(t *testing.T, file string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return document(t, raw)
}

// compileSchema compiles a schema under its own $id, which is what catches a
// $ref that names nothing: the file is what a consumer outside this repository
// compiles, and it has to be one.
func compileSchema(t *testing.T, file string) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("%s is not a JSON Schema document: %v", file, err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatalf("%s carries no $id", file)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatalf("%s does not compile: %v", file, err)
	}
	return sch
}

// checkSchemaObject is one object's half of TestSchemasMatchTheStructs: the
// properties are the struct's fields, the required list is exactly what the
// encoder never omits, and the object is closed.
func checkSchemaObject(t *testing.T, where string, obj map[string]any, typ reflect.Type) {
	t.Helper()
	props := make([]string, 0)
	raw, ok := obj["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s declares no properties", where)
	}
	for name := range raw {
		props = append(props, name)
	}
	sort.Strings(props)
	if fields := jsonNames(typ); !slices.Equal(props, fields) {
		t.Fatalf("%s declares %v and %s writes %v", where, props, typ, fields)
	}

	required := make([]string, 0)
	list, ok := obj["required"].([]any)
	if !ok {
		t.Fatalf("%s declares no required list", where)
	}
	for _, name := range list {
		required = append(required, name.(string))
	}
	sort.Strings(required)
	always := without(jsonNames(typ), jsonOptional(typ))
	if !slices.Equal(required, always) {
		t.Fatalf("%s requires %v and the encoder always writes %v", where, required, always)
	}

	if closed, ok := obj["additionalProperties"].(bool); !ok || closed {
		t.Fatalf("%s is not closed: a document this release would refuse must not be described as one it reads", where)
	}
}

// checkSchemaOps is the patch schema's op vocabulary: one branch per op kind,
// each requiring the payload that kind takes — the schema's copy of the map
// Apply reads a patch by.
func checkSchemaOps(t *testing.T, where string, op map[string]any) {
	t.Helper()
	branches, ok := op["oneOf"].([]any)
	if !ok {
		t.Fatalf("%s definitions.op is not a choice of op kinds", where)
	}
	if len(branches) != len(orchestrator.OpKinds) {
		t.Fatalf("%s declares %d op branches and this release has %d ops", where, len(branches), len(orchestrator.OpKinds))
	}
	seen := make([]string, 0, len(branches))
	for _, b := range branches {
		branch, ok := b.(map[string]any)
		if !ok {
			t.Fatalf("%s has an op branch that is not an object", where)
		}
		props, ok := branch["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s has an op branch with no properties", where)
		}
		const_, ok := props["op"].(map[string]any)
		if !ok {
			t.Fatalf("%s has an op branch that does not name its kind", where)
		}
		kind, _ := const_["const"].(string)
		payload := orchestrator.OpPayload(orchestrator.OpKind(kind))
		if payload == "" {
			t.Fatalf("%s declares the op %q, which this release does not have", where, kind)
		}
		if _, ok := props[payload]; !ok {
			t.Fatalf("%s declares %q carrying no %s", where, kind, payload)
		}
		required := branch["required"].([]any)
		if len(required) != 2 || required[0] != "op" || required[1] != payload {
			t.Fatalf("%s declares %q requiring %v, want [op %s]", where, kind, required, payload)
		}
		seen = append(seen, kind)
	}
	sort.Strings(seen)
	kinds := make([]string, 0, len(orchestrator.OpKinds))
	for _, k := range orchestrator.OpKinds {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	if !slices.Equal(seen, kinds) {
		t.Fatalf("%s declares the ops %v and this release has %v", where, seen, kinds)
	}
}

func without(names, drop []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if !slices.Contains(drop, name) {
			out = append(out, name)
		}
	}
	return out
}
