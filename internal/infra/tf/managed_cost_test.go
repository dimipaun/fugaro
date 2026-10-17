package tf

import (
	"slices"
	"strings"
	"testing"
)

// The managed cost list (Task 1.3). One fixture per entry, a golden test that
// pins the list against literals, and the plan below every line, which must
// fire nothing — the last is the rule the whole list exists under: a cost gate
// that refuses ordinary work is a cost gate that gets switched off.
//
// The fixtures write a plan the way terraform does, not the way it reads most
// easily: a block a plan states is a list of one, an attribute the plan does
// not set is absent from after (testdata/golden/installation.plan.json has no
// "scaling" key at all on its Cloud Run job), and one it sets inside a block
// the plan does state is null (that plan's "delay_execution": null beside
// "max_retries": 0). Both shapes are below every line, and a check that read
// either as above one would refuse the plans this project writes every day.

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

// TestManagedCostFiresPerEntry is the list's own review: one fixture per
// entry, each above its line, each asserting the entry fires and that it does
// what the list says — the owner named all four, so all four block, and every
// one of them is recorded whether it blocks or not.
func TestManagedCostFiresPerEntry(t *testing.T) {
	for name, tc := range map[string]struct {
		rc    ResourceChange
		entry string
	}{
		"a Cloud Run job whose minimum instance count is above the line": {
			costJob(costScaling(2)), "cloud-run-min-instances"},
		"a Cloud Run service whose minimum instance count is above the line": {
			costService(costScaling(1)), "cloud-run-min-instances"},
		"a Cloud SQL tier above the shared-core class": {
			costSQL("db-n1-standard-2"), "cloud-sql-tier"},
		"an attached GPU": {
			costVM("n2-standard-8", []any{map[string]any{"type": "nvidia-l4", "count": 1}}), "gpu"},
		"an accelerator-optimised machine type": {
			costVM("a2-highgpu-1g", nil), "gpu"},
		"a Cloud Run node selector naming an accelerator": {
			costJob(map[string]any{"node_selector": []any{map[string]any{"accelerator": "nvidia-l4"}}}), "gpu"},
		"a machine type above the shared-core class": {
			costVM("n2-standard-8", nil), "machine-type"},
	} {
		t.Run(name, func(t *testing.T) {
			p := managedPlan(tc.rc)
			fs := ManagedCosts(p)
			f, ok := costFinding(fs, tc.entry, tc.rc.Address)
			if !ok {
				t.Fatalf("the plan fires %q, want the entry %q", fs, tc.entry)
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
			if out := ManagedCostRefusals(p); !refusedWith(out, tc.rc.Address, tc.entry) {
				t.Errorf("the plan's refusals are %q, want one naming %s and %s", out, tc.rc.Address, tc.entry)
			}
		})
	}
}

// TestManagedCostOverTheThresholdIsBlockedAndRecorded: a plan above a line is
// both refused — the finding's Block is true and the refusal list carries it —
// and recorded, with the entry, the address and the entry's own words, which
// is what a confirmation shows.
func TestManagedCostOverTheThresholdIsBlockedAndRecorded(t *testing.T) {
	job := costJob(costScaling(3))
	p := managedPlan(job)
	fs := ManagedCosts(p)
	if len(fs) != 1 {
		t.Fatalf("the plan fires %q, want exactly one entry", fs)
	}
	f := fs[0]
	if f.Entry != "cloud-run-min-instances" || f.Address != job.Address || !f.Block || f.Detail == "" {
		t.Errorf("the finding is %+v, want the min-instances entry on %s, blocking, with words", f, job.Address)
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
	if i < 0 || line[:i] != job.Address || !strings.HasSuffix(line, ")") {
		t.Errorf("the refusal %q is not the shape the verdict parses: <address> (<why>)", line)
	}
}

// TestManagedCostBelowTheThresholdPlanFiresNothing is Task 1.3's red step, as
// a rule rather than as a hope: the ordinary plan of a managed project — a
// Cloud Run job with min instances 0 and a small VM — is below every line, so
// no entry fires and nothing is refused.
//
// It is the red step because the criterion an earlier revision of the plan
// carried, "a continuously-billing resource of any type the allowlist admits",
// fires on this fixture twice: both resources bill continuously by existing.
// That entry is not in the list for exactly this reason.
func TestManagedCostBelowTheThresholdPlanFiresNothing(t *testing.T) {
	p := managedPlan(costJob(costScaling(0)), costVM("e2-small", []any{}))
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
		"a job with no scaling block at all":       managedPlan(costJob(map[string]any{})),
		"a job whose scaling block is empty":       managedPlan(costJob(map[string]any{"scaling": []any{}})),
		"a job whose scaling block is empty of it": managedPlan(costJob(map[string]any{"scaling": []any{map[string]any{}}})),
		"a job whose minimum instance count is null": managedPlan(
			costJob(map[string]any{"scaling": []any{map[string]any{"min_instance_count": nil}}})),
		"a service with no scaling block at all": managedPlan(costService(map[string]any{})),
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
			withUnknown(costJob(map[string]any{}), map[string]any{
				"template": []any{map[string]any{"template": []any{map[string]any{
					"scaling": []any{map[string]any{"min_instance_count": true}}}}}}}),
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
			rc := costJob(costScaling(3))
			rc.Change.Actions = []string{"no-op"}
			return rc
		}(),
		"a delete": func() ResourceChange {
			rc := costJob(costScaling(3))
			rc.Change.Actions = []string{"delete"}
			rc.Change.After = map[string]any{}
			return rc
		}(),
		"a read": func() ResourceChange {
			rc := costJob(costScaling(3))
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
	raised := updating(costJob(costScaling(3)))
	if fs := ManagedCosts(managedPlan(raised)); len(fs) != 1 {
		t.Errorf("an update that raises the minimum instance count fires %q, want the min-instances entry", fs)
	}
	lowered := updating(costJob(costScaling(0)))
	if fs := ManagedCosts(managedPlan(lowered)); len(fs) != 0 {
		t.Errorf("an update that lowers the minimum instance count fires %q, want no entry", fs)
	}
}
