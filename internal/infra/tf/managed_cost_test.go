package tf

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// The managed cost list (Task 1.3). One fixture per RULE of the list rather
// than per entry — the table below is the single place they live, and
// TestManagedCostRulesAreCovered holds it against the rules, so a rule added
// without a fixture fails rather than passing unseen. A golden test pins the
// list against literals, and the plan below every line must fire nothing: that
// last is the rule the whole list exists under, because a cost gate that
// refuses ordinary work is a cost gate that gets switched off.
//
// The fixtures write a plan the way terraform does, not the way it reads most
// easily: a block a plan states is a list of one, an attribute the plan does
// not set is absent from after, and one it sets inside a block the plan does
// state is null (testdata/golden/installation.plan.json has "delay_execution":
// null beside "max_retries": 0). Both shapes are below every line, and a check
// that read either as above one would refuse the plans this project writes
// every day.
//
// A fixture can still write a shape terraform never emits — a key the
// provider's schema does not have — and no fixture of it can say so. That is
// what TestManagedCostRulesReadWhatTheGoldenPlanStates is for, and it is the
// test a rule reading google_cloud_run_v2_job's scaling block would have
// failed: the job states no such key.

// costJob is a Cloud Run job whose inner template is the blocks the caller
// names.
func costJob(inner map[string]any) ResourceChange {
	return mrc("google_cloud_run_v2_job", map[string]any{
		"template": []any{map[string]any{"template": []any{inner}}}})
}

// costService is the same job as a service: a service's template is the
// revision's, so its blocks are one shallower.
func costService(inner map[string]any) ResourceChange {
	return mrc("google_cloud_run_v2_service", map[string]any{"template": []any{inner}})
}

// costScaling is the scaling block a plan states for min (nil for the
// attribute a plan leaves null inside a block it does state).
func costScaling(min any) map[string]any {
	return map[string]any{"scaling": []any{map[string]any{"min_instance_count": min}}}
}

// costVM is a compute instance with the machine type and guest_accelerator the
// caller states. The managed allowlist refuses the type today (Task 1.1), and
// this list is checked against the plan regardless of it: an entry is a
// property, so the type it is read from is not what decides it.
func costVM(machine string, accelerators []any) ResourceChange {
	return mrc("google_compute_instance", map[string]any{
		"machine_type": machine, "guest_accelerator": accelerators})
}

// costSQL is a Cloud SQL instance with the tier the caller states.
func costSQL(tier string) ResourceChange {
	return mrc("google_sql_database_instance", map[string]any{
		"settings": []any{map[string]any{"tier": tier}}})
}

// costFinding is the finding of entry on address, and whether the plan fired
// it.
func costFinding(fs []ManagedCostFinding, entry, address string) (ManagedCostFinding, bool) {
	for _, f := range fs {
		if f.Entry == entry && f.Address == address {
			return f, true
		}
	}
	return ManagedCostFinding{}, false
}

// costPathKey is a rule's path as costFixtures' key: the steps joined, so a
// rule and its fixture are matched by the property they are about rather than
// by their position in the list.
func costPathKey(path []any) string {
	steps := make([]string, 0, len(path))
	for _, k := range path {
		steps = append(steps, fmt.Sprint(k))
	}
	return strings.Join(steps, ".")
}

// costFixtures is one plan change per rule of the list, keyed by the entry's
// key and the rule's path: a change of the rule's own type that states the
// rule's own property above its line. It is where the list's review is written
// from, and its completeness is checked rather than assumed —
// TestManagedCostRulesAreCovered fails on a rule with no fixture here and on a
// fixture that belongs to no rule, so the two cannot drift apart.
var costFixtures = map[string]ResourceChange{
	// The min-instances entry reads a service and not a job: a job has no
	// scaling block at all, which is what the golden-plan test holds.
	"cloud-run-min-instances|template.0.scaling.0.min_instance_count": costService(costScaling(1)),
	"cloud-sql-tier|settings.0.tier":                                  costSQL("db-n1-standard-2"),
	"gpu|guest_accelerator":                                           costVM("n2-standard-8", []any{map[string]any{"type": "nvidia-l4", "count": 1}}),
	"gpu|machine_type":                                                costVM("a2-highgpu-1g", nil),
	"gpu|template.0.template.0.node_selector.0.accelerator": costJob(map[string]any{
		"node_selector": []any{map[string]any{"accelerator": "nvidia-l4"}}}),
	"gpu|template.0.node_selector.0.accelerator": costService(map[string]any{
		"node_selector": []any{map[string]any{"accelerator": "nvidia-l4"}}}),
	"machine-type|machine_type": costVM("n2-standard-8", nil),
	"cloud-run-min-instances|scaling.0.min_instance_count": mrc("google_cloud_run_v2_service", map[string]any{
		"scaling": []any{map[string]any{"min_instance_count": 5}}}),
	"cloud-run-min-instances|scaling.0.manual_instance_count": mrc("google_cloud_run_v2_service", map[string]any{
		"scaling": []any{map[string]any{"manual_instance_count": 50}}}),
}

// TestManagedCostRulesAreCovered holds the fixtures against the rules, in both
// directions: every rule has a fixture that is a change of the rule's type and
// that states the rule's property above its line, and no fixture is left over.
// A rule whose path is wrong is a rule that can never fire on a stated value —
// the failure this list can least afford, and the one a fixture written from
// the same mistake as the rule cannot catch on its own.
func TestManagedCostRulesAreCovered(t *testing.T) {
	covered := map[string]bool{}
	for _, e := range managedCostList {
		for _, r := range e.rules {
			key := e.key + "|" + costPathKey(r.path)
			covered[key] = true
			rc, ok := costFixtures[key]
			if !ok {
				t.Errorf("rule %s has no fixture: nothing shows it fires", key)
				continue
			}
			if rc.Type != r.typ {
				t.Errorf("the fixture for %s is a %s, want the %s the rule names", key, rc.Type, r.typ)
				continue
			}
			v, ok := managedCostAt(rc.Change.After, r.path...)
			if !ok {
				t.Errorf("the fixture for %s does not state its property at %v", key, r.path)
				continue
			}
			if r.above(v) == "" {
				t.Errorf("the fixture for %s states %v, which the rule reads as below the line", key, v)
			}
		}
	}
	for key := range costFixtures {
		if !covered[key] {
			t.Errorf("the fixture %s belongs to no rule of the list", key)
		}
	}
}

// TestManagedCostEntriesGolden pins the list against a literal: the four
// entries, in the order the spec names them. An entry added, dropped or
// reordered is a diff in this test rather than a quiet widening of what a
// managed apply refuses (D61).
func TestManagedCostEntriesGolden(t *testing.T) {
	want := []string{"cloud-run-min-instances", "cloud-sql-tier", "gpu", "machine-type"}
	if got := ManagedCostEntries(); !slices.Equal(got, want) {
		t.Errorf("the cost list is %q, want %q", got, want)
	}
}

// TestManagedCostEntriesAreDecided: every entry carries its reason, and every
// rule names the type and the property it reads, so a rule cannot be added as
// a type check with no property behind it. This is the check Task 1.1's
// TestManagedAllowedTypesIsDecided makes of its own list.
func TestManagedCostEntriesAreDecided(t *testing.T) {
	if len(managedCostList) == 0 {
		t.Fatal("the cost list is empty")
	}
	for _, e := range managedCostList {
		if e.key == "" {
			t.Errorf("an entry of the cost list has no key")
		}
		if e.why == "" {
			t.Errorf("entry %q has no reason: an entry is a decision (D61)", e.key)
		}
		if len(e.rules) == 0 {
			t.Errorf("entry %q reads no property", e.key)
		}
		for _, r := range e.rules {
			if r.typ == "" || len(r.path) == 0 || r.what == "" || r.above == nil {
				t.Errorf("entry %q has a rule that does not name its property: type %q, what %q", e.key, r.typ, r.what)
			}
		}
	}
}

// TestManagedCostFiresPerEntry is the list's own review, one fixture per rule
// (costFixtures), each above its line, each asserting the entry fires and that
// it does what the list says — the owner named all four, so all four block, and
// every one of them is recorded whether it blocks or not.
func TestManagedCostFiresPerEntry(t *testing.T) {
	for key, rc := range costFixtures {
		entry, _, ok := strings.Cut(key, "|")
		if !ok {
			t.Fatalf("the fixture key %q names no entry", key)
		}
		t.Run(key, func(t *testing.T) {
			p := managedPlan(rc)
			fs := ManagedCosts(p)
			f, ok := costFinding(fs, entry, rc.Address)
			if !ok {
				t.Fatalf("the plan fires %q, want the entry %q", fs, entry)
			}
			if !f.Block {
				t.Errorf("entry %q is recorded but does not block; the owner named it", f.Entry)
			}
			if f.Detail == "" {
				t.Errorf("entry %q was recorded with no words for why", f.Entry)
			}
			// Recorded and blocked are two halves of one finding: the entry is
			// in the verdict for whoever confirms, and in the refusals the
			// gate falls back on.
			if out := ManagedCostRefusals(p); !refusedWith(out, rc.Address, entry) {
				t.Errorf("the plan's refusals are %q, want one naming %s and %s", out, rc.Address, entry)
			}
		})
	}
}

// TestManagedCostOverTheThresholdIsBlockedAndRecorded: a plan above a line is
// both refused — the finding's Block is true and the refusal list carries it —
// and recorded, with the entry, the address and the entry's own words, which
// is what a confirmation shows.
func TestManagedCostOverTheThresholdIsBlockedAndRecorded(t *testing.T) {
	svc := costService(costScaling(3))
	p := managedPlan(svc)
	fs := ManagedCosts(p)
	if len(fs) != 1 {
		t.Fatalf("the plan fires %q, want exactly one entry", fs)
	}
	f := fs[0]
	if f.Entry != "cloud-run-min-instances" || f.Address != svc.Address || !f.Block || f.Detail == "" {
		t.Errorf("the finding is %+v, want the min-instances entry on %s, blocking, with words", f, svc.Address)
	}
	out := ManagedCostRefusals(p)
	if len(out) != 1 {
		t.Fatalf("the plan's refusals are %q, want exactly one", out)
	}
	// The shape internal/managed's verdict parses: the address stands before
	// the first " (", and the line ends with ")". A refusal a verdict cannot
	// read is a refusal nobody is shown.
	line := out[0]
	i := strings.Index(line, " (")
	if i < 0 || line[:i] != svc.Address || !strings.HasSuffix(line, ")") {
		t.Errorf("the refusal %q is not the shape the verdict parses: <address> (<why>)", line)
	}
}

// TestManagedCostBelowTheThresholdPlanFiresNothing is Task 1.3's red step, as
// a rule rather than as a hope: the ordinary plan of a managed project — a
// Cloud Run service with min instances 0 and a small VM — is below every line, so
// no entry fires and nothing is refused.
//
// It is the red step because the criterion an earlier revision of the plan
// carried, "a continuously-billing resource of any type the allowlist admits",
// fires on this fixture twice: both resources bill continuously by existing.
// That entry is not in the list for exactly this reason.
func TestManagedCostBelowTheThresholdPlanFiresNothing(t *testing.T) {
	p := managedPlan(costService(costScaling(0)), costVM("e2-small", []any{}))
	if fs := ManagedCosts(p); len(fs) != 0 {
		t.Errorf("an ordinary plan fires %q, want no entry", fs)
	}
	if out := ManagedCostRefusals(p); len(out) != 0 {
		t.Errorf("an ordinary plan is refused by %q, want no refusal", out)
	}
}

// TestManagedCostReadsTheShapesAPlanWrites: the shapes a plan uses for a
// property it does not set are all below the line — the block absent, the
// block empty, the block present with the attribute null, the block present
// with the attribute left out — and the small class is below it on each of the
// four entries. Read the other way, any of these would refuse the plans this
// project writes every day.
func TestManagedCostReadsTheShapesAPlanWrites(t *testing.T) {
	for name, p := range map[string]*Plan{
		"a service with no scaling block at all": managedPlan(costService(map[string]any{})),
		"a service whose scaling block is empty": managedPlan(costService(map[string]any{"scaling": []any{}})),
		"a service whose scaling block is empty of it": managedPlan(
			costService(map[string]any{"scaling": []any{map[string]any{}}})),
		"a service whose minimum instance count is null": managedPlan(
			costService(map[string]any{"scaling": []any{map[string]any{"min_instance_count": nil}}})),
		"a service whose minimum instance count is zero": managedPlan(costService(costScaling(0))),
		// A job states no minimum instance count at all — the shape the golden
		// plan writes — so it is below the line however the entry is read.
		"a job, which states no minimum instance count": managedPlan(costJob(map[string]any{})),
		"a VM with no accelerator and a small type": managedPlan(
			costVM("e2-small", []any{})),
		"a VM whose guest_accelerator is null": managedPlan(
			costVM("e2-small", nil)),
		"a Cloud SQL instance at the smallest tier":    managedPlan(costSQL("db-f1-micro")),
		"a Cloud SQL instance at the other small tier": managedPlan(costSQL("db-g1-small")),
	} {
		t.Run(name, func(t *testing.T) {
			if fs := ManagedCosts(p); len(fs) != 0 {
				t.Errorf("the plan fires %q, want no entry", fs)
			}
		})
	}
}

// costChange is the plan's first change of one type, and whether it has one.
func costChange(p *Plan, typ string) (*ResourceChange, bool) {
	for i := range p.ResourceChanges {
		if p.ResourceChanges[i].Type == typ {
			return &p.ResourceChanges[i], true
		}
	}
	return nil, false
}

// costMissingKey walks a rule's path through a plan value the way managedCostAt
// does and reports the first key the object the path reads from does not state.
// A block the plan leaves empty ends the walk: a plan that states none of a
// block states none of what is inside it either, so nothing further can be
// read from that plan.
func costMissingKey(v any, path []any) (string, bool) {
	for _, k := range path {
		switch k := k.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return k, true
			}
			nv, ok := m[k]
			if !ok {
				return k, true
			}
			v = nv
		case int:
			l, ok := v.([]any)
			if !ok || k >= len(l) {
				return "", false
			}
			v = l[k]
		}
	}
	return "", false
}

// TestManagedCostRulesReadWhatTheGoldenPlanStates: a rule's path is the nesting
// terraform writes, and a path this file got wrong is a rule that can never
// fire on a stated value — a silent hole rather than a false refusal, which is
// the failure a cost gate can least afford. It is checked by reading a real
// plan rather than by asserting: terraform's plan JSON writes a block a plan
// states as a list of one, and a block the resource has but the plan sets
// nothing in as an empty list (the golden installation plan's job states
// node_selector, volumes and vpc_access as []), so a key the object a rule
// reads from does not carry at all is not an attribute of that resource. Every
// string key a rule's path steps through must be stated by the fixture.
//
// This is the test that says why the min-instances entry reads a service: the
// golden plan's Cloud Run job states no scaling key at either template level,
// so a rule reading template.0.template.0.scaling.0.min_instance_count could
// never fire on a plan of that type — and the fixtures could not have said so,
// because a fixture written from the same mistake writes the same key.
//
// The fixture holds one of the four types the list reads —
// google_cloud_run_v2_job — and this repository holds no plan of the other
// three, so their paths are read from the provider's schema and this test
// cannot reach them. The limit is recorded rather than hidden.
func TestManagedCostRulesReadWhatTheGoldenPlanStates(t *testing.T) {
	p := readPlanFixture(t, "golden/installation.plan.json")
	checked := 0
	for _, e := range managedCostList {
		for _, r := range e.rules {
			rc, ok := costChange(p, r.typ)
			if !ok {
				continue
			}
			checked++
			if key, missing := costMissingKey(rc.Change.After, r.path); missing {
				t.Errorf("entry %q reads %v of %s, and the plan states no %q where the path reads it",
					e.key, r.path, rc.Address, key)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no rule of the list reads a type the golden plan has, so nothing here was checked")
	}
}

// TestManagedCostComputedValueIsNotBelowTheLine: a plan that knows the
// property only after apply is not below the line by default. A saved plan
// keeps only the references an expression uses, never the expression, so a
// minimum instance count computed from a variable the plan sets to 10 and a
// machine type computed from one it sets to n2-standard-8 both read as
// unknown — and unknown is refused, in the fail-closed direction Task 1.1's
// cover takes for a computed name. A plan that states the value literally is
// read; a plan that hides it is not.
func TestManagedCostComputedValueIsNotBelowTheLine(t *testing.T) {
	for name, tc := range map[string]struct {
		rc    ResourceChange
		entry string
	}{
		"a computed minimum instance count": {
			withUnknown(costService(map[string]any{}), map[string]any{
				"template": []any{map[string]any{
					"scaling": []any{map[string]any{"min_instance_count": true}}}}}),
			"cloud-run-min-instances"},
		"a computed machine type": {
			withUnknown(costVM("", nil), map[string]any{"machine_type": true}),
			"machine-type"},
	} {
		t.Run(name, func(t *testing.T) {
			fs := ManagedCosts(managedPlan(tc.rc))
			f, ok := costFinding(fs, tc.entry, tc.rc.Address)
			if !ok {
				t.Fatalf("the plan fires %q, want the entry %q", fs, tc.entry)
			}
			if !f.Block {
				t.Errorf("entry %q is recorded but does not block", f.Entry)
			}
			if !strings.Contains(f.Detail, "not known at plan time") {
				t.Errorf("entry %q was recorded as %q, want the words a computed value is refused in", f.Entry, f.Detail)
			}
		})
	}
}

// TestManagedCostReadsOnlyWhatBills: an entry is about a plan that adds or
// upsizes a resource. A delete takes the resource away, a no-op changes
// nothing, a read is not a resource at all — none of them leaves the plan
// above a line, and a check that read the before of a delete as a value would
// refuse the plan that removes the expensive thing.
func TestManagedCostReadsOnlyWhatBills(t *testing.T) {
	for name, rc := range map[string]ResourceChange{
		"a no-op": func() ResourceChange {
			rc := costService(costScaling(3))
			rc.Change.Actions = []string{"no-op"}
			return rc
		}(),
		"a delete": func() ResourceChange {
			rc := costService(costScaling(3))
			rc.Change.Actions = []string{"delete"}
			rc.Change.After = map[string]any{}
			return rc
		}(),
		"a read": func() ResourceChange {
			rc := costService(costScaling(3))
			rc.Change.Actions = []string{"read"}
			rc.Mode = "data"
			return rc
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if fs := ManagedCosts(managedPlan(rc)); len(fs) != 0 {
				t.Errorf("the change fires %q, want no entry", fs)
			}
		})
	}
}

// TestManagedCostChecksAnUpdate: an update's after is what the resource
// becomes, so an update that raises the minimum instance count is above the
// line and an update that lowers it is below — the same rule Task 1.1's cover
// holds an update to, for the same reason.
func TestManagedCostChecksAnUpdate(t *testing.T) {
	raised := updating(costService(costScaling(3)))
	if fs := ManagedCosts(managedPlan(raised)); len(fs) != 1 {
		t.Errorf("an update that raises the minimum instance count fires %q, want the min-instances entry", fs)
	}
	lowered := updating(costService(costScaling(0)))
	if fs := ManagedCosts(managedPlan(lowered)); len(fs) != 0 {
		t.Errorf("an update that lowers the minimum instance count fires %q, want no entry", fs)
	}
}

// TestManagedCostFlagOnlyEntryIsFlaggedButNotRefused holds the one behaviour
// ManagedCostRefusals adds to ManagedCosts: an entry with block false is
// recorded and shown (D62: "flag by default; an entry added for a property the
// owner has not named is added with block false") and is not in the refusals.
// Every entry the owner named blocks, so the list has none of its own; this one
// is added for the test and removed with it.
func TestManagedCostFlagOnlyEntryIsFlaggedButNotRefused(t *testing.T) {
	saved := managedCostList
	t.Cleanup(func() { managedCostList = saved })
	managedCostList = append(slices.Clone(saved), managedCostEntry{
		key:   "test-flag-only",
		block: false,
		why:   "a property the owner has not named",
		rules: []managedCostRule{{
			typ: "test_flag_only_thing", what: "the size", path: []any{"size"},
			above: func(any) string { return "the size is large" },
		}},
	})
	rc := mrc("test_flag_only_thing", map[string]any{"size": "large"})
	p := managedPlan(rc)

	f, ok := costFinding(ManagedCosts(p), "test-flag-only", rc.Address)
	if !ok {
		t.Fatalf("the flag-only entry is not recorded: %q", ManagedCosts(p))
	}
	if f.Block || f.Detail == "" {
		t.Errorf("the finding is %+v, want it recorded with words and not blocking", f)
	}
	if out := ManagedCostRefusals(p); len(out) != 0 {
		t.Errorf("a flag-only entry is refused: %q", out)
	}

	// Alongside a blocking entry, only the blocking one is refused.
	vm := costVM("n2-standard-64", nil)
	out := ManagedCostRefusals(managedPlan(rc, vm))
	if len(out) != 1 || !refusedWith(out, vm.Address, managedCostMachineType) {
		t.Errorf("the refusals are %q, want only the VM's", out)
	}
}

// costAttributes is the documented set of cost-bearing attributes, by resource
// type, that the managed cover admits or could admit: where the provider states
// a machine type, a tier, an accelerator or a scaling count. The repository
// holds no provider schema, so this is the documented set, written out; a type
// of the managed cover that is in neither this table nor costNone fails the
// test below, so admitting a type is a decision about its cost attributes.
var costAttributes = map[string][][]any{
	"google_cloud_run_v2_job": {{"template", 0, "template", 0, "node_selector", 0, "accelerator"}},
	"google_cloud_run_v2_service": {
		{"template", 0, "scaling", 0, "min_instance_count"},
		{"scaling", 0, "min_instance_count"},
		{"scaling", 0, "manual_instance_count"},
		{"template", 0, "node_selector", 0, "accelerator"}},
	"google_compute_instance":      {{"machine_type"}, {"guest_accelerator"}},
	"google_sql_database_instance": {{"settings", 0, "tier"}},
}

// costNone are the types of the managed cover decided to carry no machine type,
// tier, accelerator or scaling count.
var costNone = []string{
	"google_apikeys_key", "google_artifact_registry_repository",
	"google_artifact_registry_repository_iam_member", "google_cloud_run_v2_job_iam_member",
	"google_cloud_scheduler_job", "google_project_service", "google_secret_manager_secret",
	"google_secret_manager_secret_iam_member", "google_service_account",
	"google_service_account_iam_member", "google_storage_bucket",
	"google_storage_bucket_iam_member", "google_storage_bucket_object",
}

// TestManagedCostEveryCostAttributeHasARule fails when a resource type the
// managed cover admits is neither decided to carry no cost attribute nor has
// every documented cost attribute under a rule, and when a documented attribute
// of any type has no rule.
func TestManagedCostEveryCostAttributeHasARule(t *testing.T) {
	ruled := map[string]bool{}
	for _, e := range managedCostList {
		for _, r := range e.rules {
			ruled[r.typ+"|"+costPathKey(r.path)] = true
		}
	}
	for typ, paths := range costAttributes {
		for _, path := range paths {
			if !ruled[typ+"|"+costPathKey(path)] {
				t.Errorf("%s states %s, which no cost rule reads", typ, costPathKey(path))
			}
		}
	}
	for _, typ := range ManagedAllowedTypes() {
		_, has := costAttributes[typ]
		if has == slices.Contains(costNone, typ) {
			t.Errorf("the managed cover admits %s, which is %s: decide its cost attributes", typ,
				map[bool]string{true: "in both costAttributes and costNone", false: "in neither costAttributes nor costNone"}[has])
		}
	}
}

// TestManagedCostNonConformingTypesAreRefused: a blocked attribute stated in a
// type the check cannot read is refused, for every blocked attribute alike.
func TestManagedCostNonConformingTypesAreRefused(t *testing.T) {
	for name, rc := range map[string]ResourceChange{
		"a non-number minimum instance count": costService(costScaling("5")),
		"a non-number manual instance count":  mrc("google_cloud_run_v2_service", map[string]any{"scaling": []any{map[string]any{"manual_instance_count": "50"}}}),
		"a non-string accelerator (job)":      costJob(map[string]any{"node_selector": []any{map[string]any{"accelerator": 7}}}),
		"a non-string accelerator (service)":  costService(map[string]any{"node_selector": []any{map[string]any{"accelerator": 7}}}),
	} {
		t.Run(name, func(t *testing.T) {
			if fs := ManagedCosts(managedPlan(rc)); len(fs) != 1 || !fs[0].Block {
				t.Errorf("fires %q, want one blocking entry", fs)
			}
		})
	}
}

// TestManagedCostSmallClassesGolden pins the classes' contents: bigger types
// are not small.
func TestManagedCostSmallClassesGolden(t *testing.T) {
	keys := func(c managedCostClass) []string { return slices.Sorted(maps.Keys(c.small)) }
	if got, want := keys(managedCostMachineClass), []string{"e2-medium", "e2-micro", "e2-small", "f1-micro", "g1-small"}; !slices.Equal(got, want) {
		t.Errorf("machine class %v, want %v", got, want)
	}
	if got, want := keys(managedCostSQLClass), []string{"db-f1-micro", "db-g1-small"}; !slices.Equal(got, want) {
		t.Errorf("sql class %v, want %v", got, want)
	}
	if managedCostMachineClass.small["n2-standard-96"] || managedCostSQLClass.small["db-custom-96-614400"] {
		t.Error("a large class is small")
	}
	if managedCostAboveMachineType("n2-standard-96") == "" || managedCostAboveSQLTier("db-custom-96-614400") == "" {
		t.Error("a large class is not above the line")
	}
}

// TestManagedCostAReplaceBills: a replace creates, in either order.
func TestManagedCostAReplaceBills(t *testing.T) {
	for _, actions := range [][]string{{"delete", "create"}, {"create", "delete"}} {
		rc := costService(costScaling(3))
		rc.Change.Actions = actions
		if fs := ManagedCosts(managedPlan(rc)); len(fs) != 1 {
			t.Errorf("a replace %v fires %q, want the min-instances entry", actions, fs)
		}
	}
}
