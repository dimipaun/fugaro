package tf

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The cost-bearing attributes of a managed plan, flagged and blocked (Task 1.3
// of docs/plans/2026-10-16-managed-projects.md).
//
// The spec's step 4 asks the agent to flag "new or upsized resources that bill
// continuously, such as Cloud Run min instances, Cloud SQL tiers, GPUs and
// large VMs", and the reconciliation's Gap finding 27 records what was
// missing: nothing blocked a plan on a cost. This is that check, and it is a
// LIST and never an ESTIMATE (design D62). Fugaro cannot price an arbitrary
// plan — a price is a catalogue, a region, a commitment and a discount, and a
// number this repository invented would be wrong in every installation but the
// one it was written for — and a gate that pretends to price is a gate whose
// refusals nobody can check. What a plan can be held to is what it STATES, so
// an entry below is a property a plan is above or below, compared against a
// line stated once as a constant, and every refusal this file writes is a fact
// a reviewer can check by reading the plan.
//
// An entry is a property, never a resource type. The type in each rule says
// where a plan states the property — a plan states a machine type on
// google_compute_instance because that is the resource the provider defines it
// on — and it is not the criterion: the same property on another type is
// another rule and another decision (D61).
//
// D62's answer is "flag by default, block on a named, owner-written attribute
// allowlist, and block on the budget rather than on an estimate". This list is
// that allowlist: the four properties the owner named in the spec, each one
// blocking, and each one recorded whether it blocks or not — a finding is what
// a confirmation shows, and the block is what happens when nobody confirms.
// The flag half is not decoration: an entry added for a property the owner has
// not named is added with block false, and it is then recorded and shown while
// the plan proceeds. D62's other blocking input is not here: the spend against
// the recorded budget amount is read at apply time by Task 4.4, and the two are
// deliberately separate, so that neither is a proxy for the other — this list
// can refuse a plan that has not run, and Task 4.4 can refuse a run that has
// already spent.
//
// "A continuously-billing resource of any type the allowlist admits" is
// deliberately NOT an entry, and the below-the-threshold test is the reason
// stated as a rule: nearly every resource a managed project creates bills
// continuously by existing — a Cloud Run service with min instances 0, the
// project's own runs bucket, a scheduler — so that criterion fires on
// essentially every plan, and a gate that refuses everything is a gate that
// gets switched off. An entry has to be a property a plan is above or below.
//
// What this list can reach today is worth stating plainly rather than leaving
// to be discovered. Three of the four entries read a type Task 1.1's managed
// allowlist does not admit — a Cloud Run service, a compute instance, a Cloud
// SQL instance — so a plan of one of those is refused for its type before this
// list is asked anything, and those entries are the second line rather than the
// first: they become load-bearing the moment the allowlist admits a service, a
// VM or a SQL instance, which is a decision for the owner rather than for this
// file. An entry is a decision about a property rather than about a type (D61),
// and the four are the spec's own examples, so that is a statement about the
// allowlist and not a defect here.
//
// The one type the two lists share is google_cloud_run_v2_job, and it is the
// type this task's own wording gets wrong: a job states no minimum instance
// count at all (the min-instances entry below says why), while a job's node
// selector is where a GPU is attached to it — so the GPU entry is the one entry
// with reach on a plan the managed cover would admit.

// The entries of the list. A key is what a recorded finding and a refusal name
// the entry by, so it is stable: it is the entry's identity in a verdict, in a
// confirmation, and in this file's tests.
const (
	// managedCostMinInstances is the spec's "Cloud Run min instances": a
	// service whose minimum instance count is above the line keeps instances
	// running — and billing — when nothing calls it.
	managedCostMinInstances = "cloud-run-min-instances"
	// managedCostSQLTier is the spec's "Cloud SQL tiers": a tier above the
	// named class is a database sized above the smallest one, billed by the
	// hour whether or not anything queries it.
	managedCostSQLTier = "cloud-sql-tier"
	// managedCostGPU is the spec's "GPUs": an attached accelerator bills by
	// the hour on top of the machine it is attached to.
	managedCostGPU = "gpu"
	// managedCostMachineType is the spec's "large VMs": a machine type above
	// the named class is a VM sized above the smallest one.
	managedCostMachineType = "machine-type"
)

// The line the one entry whose threshold is a number is above. Zero is the
// provider's own default, and a plan that does not state the attribute is at
// the line rather than above it.
const managedCostMaxMinInstances = 0

// managedCostClass is a class of resource sizes one entry's line names: the
// name a refusal uses, and the values at or below the line. The two are one
// value rather than a name beside a set, so the class is stated once and the
// words a refusal gives a reader cannot drift from the comparison behind them.
//
// The values are an allowlist, and that is the fail-closed direction every
// list in this package takes (Task 1.1's managedTypes reads the same way): a
// value the class has never heard of — a family GCP adds later, a custom type
// (n2-custom-8-16384), a typo, an empty string, a value of another shape — is
// above the line rather than below it.
type managedCostClass struct {
	name  string
	small map[string]bool
}

// managedCostMachineClass is the class the machine-type entry is above: GCP's
// shared-core machine types, the smallest class there is, at a fraction of a
// vCPU.
var managedCostMachineClass = managedCostClass{name: "shared-core", small: map[string]bool{
	"e2-micro":  true,
	"e2-small":  true,
	"e2-medium": true,
	"f1-micro":  true,
	"g1-small":  true,
}}

// managedCostSQLClass is the class the tier entry is above: Cloud SQL's
// shared-core tiers, its smallest. A class of its own, not the machine types':
// the two catalogues share the name and nothing else.
var managedCostSQLClass = managedCostClass{name: "shared-core", small: map[string]bool{
	"db-f1-micro": true,
	"db-g1-small": true,
}}

// managedCostGPUFamilies are the machine-type families that are an attached
// GPU in their own right: a VM of one of these carries accelerators whatever
// else the plan states, so the GPU entry fires on it as well as the
// machine-type entry does. Prefixes, because a family's sizes share one.
var managedCostGPUFamilies = []string{"a2-", "a3-", "g2-"}

// managedCostEntry is one entry of the list: the property, where a plan states
// it, and what happens when a plan is above it.
type managedCostEntry struct {
	// key is the entry's name in a finding and in a refusal, one of the
	// constants above.
	key string
	// block is true when the entry refuses a plan — the plan is not applied
	// and falls back to a confirmation — and false when the entry is only
	// recorded and shown to whoever confirms. Every entry the owner named in
	// the spec blocks; an entry added later for a property the owner has not
	// named is added with block false, which is the flag behaviour D62 asks
	// for by default.
	block bool
	// why is the entry's justification, in the list's own words. A test
	// requires it: an entry added without one is an entry added without a
	// decision (D61).
	why string
	// rules are the places a plan states this entry's property. An entry
	// fires once per address, however many rules it has.
	rules []managedCostRule
}

// managedCostRule is one place a plan states one entry's property: the
// resource type whose schema holds it, and the path to it inside a change's
// after.
type managedCostRule struct {
	typ string
	// what names the property in the words an entry fired by a value the plan
	// knows only after apply is recorded in.
	what string
	// path is the property's place in the plan's after, in the shape
	// after_unknown writes a path — map keys and list indexes — which is the
	// shape unknownAt reads, so the property is named once and both the value
	// and whether the plan knows it are read with it.
	path []any
	// above reads the value the plan states at path and returns the entry's
	// own words for why the plan is above the line, or "" when it is at or
	// below it. It is called only for a value the plan states and knows.
	above func(v any) string
}

// managedCostList is the whole check: four entries, in the order the spec names
// them, each one blocking because each one is a property the owner named.
var managedCostList = []managedCostEntry{
	{
		key:   managedCostMinInstances,
		block: true,
		why: "a Cloud Run service whose minimum instance count is above the line keeps " +
			"instances running when nothing calls it, and an instance that always runs " +
			"bills always — the spec's own example, and the one entry whose line is a " +
			"number a plan states rather than a class it is above. The entry reads the " +
			"service and not the job this task's own wording names: a job runs and exits " +
			"rather than staying up, the provider's schema carries no scaling block on " +
			"one (the job in testdata/golden/installation.plan.json states no scaling key " +
			"at either template level), and a rule reading a key no plan of the type has " +
			"is a rule that can never fire on a stated value",
		rules: []managedCostRule{
			{
				typ:   "google_cloud_run_v2_service",
				what:  "the minimum instance count",
				path:  []any{"template", 0, "scaling", 0, "min_instance_count"},
				above: managedCostAboveInstances,
			},
			{
				// The service's own top-level scaling block, the provider's
				// service-level scaling (min and manual instance counts).
				typ:   "google_cloud_run_v2_service",
				what:  "the service-level minimum instance count",
				path:  []any{"scaling", 0, "min_instance_count"},
				above: managedCostAboveInstances,
			},
			{
				typ:   "google_cloud_run_v2_service",
				what:  "the manual instance count",
				path:  []any{"scaling", 0, "manual_instance_count"},
				above: managedCostAboveInstances,
			},
		},
	},
	{
		key:   managedCostSQLTier,
		block: true,
		why: "a Cloud SQL tier above the shared-core class is a database sized above the " +
			"smallest one Cloud SQL offers, billed by the hour whether or not anything " +
			"queries it — the spec's \"Cloud SQL tiers\", read as the class the plan is " +
			"above rather than as a price",
		rules: []managedCostRule{
			{
				typ:   "google_sql_database_instance",
				what:  "the tier",
				path:  []any{"settings", 0, "tier"},
				above: managedCostAboveSQLTier,
			},
		},
	},
	{
		key:   managedCostGPU,
		block: true,
		why: "an attached GPU is billed on top of the machine it is attached to, whatever " +
			"that machine costs — the spec's \"GPUs\", read wherever a plan states one: a " +
			"guest_accelerator block, a Cloud Run node selector naming an accelerator, or " +
			"a machine type from an accelerator-optimised family. A Cloud Run job's GPU " +
			"is billed for the execution rather than around the clock, and it is an entry " +
			"all the same: an accelerator is the largest line a plan of that size can add, " +
			"and the spec names GPUs without qualifying them",
		rules: []managedCostRule{
			{
				typ:   "google_compute_instance",
				what:  "the accelerators",
				path:  []any{"guest_accelerator"},
				above: managedCostAboveGuestAccelerator,
			},
			{
				typ:   "google_compute_instance",
				what:  "the machine type",
				path:  []any{"machine_type"},
				above: managedCostAboveGPUMachine,
			},
			{
				typ:   "google_cloud_run_v2_job",
				what:  "the accelerators",
				path:  []any{"template", 0, "template", 0, "node_selector", 0, "accelerator"},
				above: managedCostAboveAccelerator,
			},
			{
				typ:   "google_cloud_run_v2_service",
				what:  "the accelerators",
				path:  []any{"template", 0, "node_selector", 0, "accelerator"},
				above: managedCostAboveAccelerator,
			},
		},
	},
	{
		key:   managedCostMachineType,
		block: true,
		why: "a machine type above the shared-core class is a VM sized above the smallest " +
			"one GCP offers, billed by the hour whether or not anything runs on it — the " +
			"spec's \"large VMs\", read as the class the plan is above",
		rules: []managedCostRule{
			{
				typ:   "google_compute_instance",
				what:  "the machine type",
				path:  []any{"machine_type"},
				above: managedCostAboveMachineType,
			},
		},
	},
}

// ManagedCostFinding is one entry of the list fired by one address of a plan:
// the entry's key, the address it was fired on, the entry's own words for what
// the plan states, and whether the entry blocks the plan or only records it.
type ManagedCostFinding struct {
	Entry   string
	Address string
	Detail  string
	Block   bool
}

// String is the finding as one line of the shape both gates of a managed apply
// write a refused change in — "<address> (<why>)", the first " (" being the one
// that separates them — which is the shape internal/managed's verdict parses
// (internal/managed/plan.go, refusals) and the shape a confirmation shows. The
// entry's key leads the why, so a reader of a verdict can find the entry in
// this file's list.
func (f ManagedCostFinding) String() string {
	return f.Address + " (" + f.Entry + ": " + f.Detail + ")"
}

// ManagedCosts is the check: every entry of the list a plan is above, one
// finding per entry and address, in the order the plan's changes and the
// list's entries come in — the order a person reads them in.
//
// It reads a plan and returns; it refuses nothing itself. ManagedCostRefusals
// is the same findings in the shape the gate's verdict takes, and a caller
// that records the findings and applies anyway is a caller that ignored the
// blocks, which is the one thing the block flag is for.
//
// A nil plan fires nothing, and that is not the same statement as "a nil plan
// is below every line": this check reads a plan a caller already has, and the
// caller that has no plan is a caller with nothing to apply (internal/managed's
// Gate reads the plan first, and a plan it cannot read is an error rather than
// a verdict).
func ManagedCosts(p *Plan) []ManagedCostFinding {
	if p == nil {
		return nil
	}
	var out []ManagedCostFinding
	for _, rc := range p.ResourceChanges {
		if rc.Mode != "managed" || !managedCostBills(rc.Change.Actions) {
			continue
		}
		for _, e := range managedCostList {
			if detail, ok := e.fires(rc); ok {
				out = append(out, ManagedCostFinding{
					Entry: e.key, Address: rc.Address, Detail: detail, Block: e.block})
			}
		}
	}
	return out
}

// ManagedCostRefusals is the blocking entries as the verdict's lines: the
// findings whose entry blocks, each in the shape internal/managed's gate parses
// ("<address> (<why>)"), which is what a managed apply hands its verdict and
// what it falls back to a confirmation on. The findings whose entry only flags
// are not here — they are recorded and shown, and the plan proceeds — and a
// caller that wants both reads ManagedCosts.
func ManagedCostRefusals(p *Plan) []string {
	var out []string
	for _, f := range ManagedCosts(p) {
		if f.Block {
			out = append(out, f.String())
		}
	}
	return out
}

// ManagedCostEntries is the list's own keys, in the list's order: the closed
// set of properties a managed plan is held to, which a test pins against a
// literal so an entry added without a decision is a diff rather than a quiet
// widening (D61).
func ManagedCostEntries() []string {
	out := make([]string, 0, len(managedCostList))
	for _, e := range managedCostList {
		out = append(out, e.key)
	}
	return out
}

// fires is one entry's own question about one change: is the plan above this
// entry's line? The rules are tried in order and the first that fires answers,
// so an entry fires once per address however many rules it has — a VM whose
// machine type is an accelerator-optimised one and whose guest_accelerator is
// set is one GPU, not two.
func (e managedCostEntry) fires(rc ResourceChange) (string, bool) {
	for _, r := range e.rules {
		if r.typ != rc.Type {
			continue
		}
		// A value the plan knows only after apply is refused rather than read
		// as below the line: a saved plan keeps only the references an
		// expression uses, never the expression, so a minimum instance count
		// computed from a variable the plan sets to 10 reads as unknown — and
		// unknown is above the line, in the fail-closed direction Task 1.1's
		// cover takes for a computed name.
		if unknownAt(rc.Change.AfterUnknown, r.path...) {
			return managedCostUnknown(r.what), true
		}
		v, ok := managedCostAt(rc.Change.After, r.path...)
		if !ok {
			continue
		}
		if detail := r.above(v); detail != "" {
			return detail, true
		}
	}
	return "", false
}

// managedCostBills reports whether a change adds or upsizes what bills: a
// create, an update, and a replace (delete and create) all leave the resource
// as the plan's after says. A delete, a no-op and a read do not — an entry is
// about a plan that creates or raises something, and a plan that only removes
// the expensive resource is the guard's business rather than this list's.
func managedCostBills(actions []string) bool {
	return slices.Contains(actions, "create") || slices.Contains(actions, "update")
}

// managedCostAt reads the value a plan states at path, through objects and
// blocks: a list is indexed by the ints in the path, as after_unknown writes
// it. found is false when the plan does not state the value — a block that is
// empty, a key that is absent, and a key the plan writes as null, which is how
// terraform's own plan JSON writes an optional attribute nothing sets
// (testdata/golden/installation.plan.json has "delay_execution": null beside
// "max_retries": 0) and what an optional attribute inside a stated block looks
// like.
func managedCostAt(v any, path ...any) (any, bool) {
	for _, k := range path {
		switch k := k.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			v, ok = m[k]
			if !ok || v == nil {
				return nil, false
			}
		case int:
			l, ok := v.([]any)
			if !ok || k < 0 || k >= len(l) {
				return nil, false
			}
			v = l[k]
		}
	}
	return v, true
}

// managedCostUnknown is the words an entry fired by a value the plan knows only
// after apply is recorded in. An entry is a property a plan is above or below,
// and a plan that does not state the value is not below the line by default:
// the same fail-closed reading, and the same advice, as Task 1.1's cover gives
// a computed name.
func managedCostUnknown(what string) string {
	return what + " is not known at plan time: write it out literally"
}

// managedCostNumber reads a number a plan states: terraform's plan JSON writes
// every number as a JSON number, which Go decodes as a float64, while a fixture
// writes the int it reads more easily. Both are the same value.
func managedCostNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

// managedCostAboveInstances is the min-instances entry's comparison: the plan
// states a number, and the number is above the line. A value that is present
// but is not a number is refused rather than skipped — the provider requires
// the attribute, so a plan that states something else is a plan this file
// cannot read as below the line.
func managedCostAboveInstances(v any) string {
	n, ok := managedCostNumber(v)
	if !ok {
		return fmt.Sprintf("a minimum instance count of %v, which is not a number", v)
	}
	if n <= float64(managedCostMaxMinInstances) {
		return ""
	}
	return fmt.Sprintf("a minimum instance count of %v, above the line of %d",
		n, managedCostMaxMinInstances)
}

// managedCostAboveSQLTier is the tier entry's comparison: a tier the class does
// not know is above the line, the tier the provider requires included when the
// plan states it empty.
func managedCostAboveSQLTier(v any) string {
	return managedCostClassAbove(managedCostSQLClass, "the tier", str(v))
}

// managedCostAboveMachineType is the machine-type entry's comparison, read the
// same way and for the same reason as the tier's.
func managedCostAboveMachineType(v any) string {
	return managedCostClassAbove(managedCostMachineClass, "the machine type", str(v))
}

// managedCostClassAbove is the comparison the two class entries share: a value
// the class names is at or below the line, and anything else is above it. The
// class's own values are the words the refusal gives a reader, so whoever is
// shown it can see what the plan was held to.
func managedCostClassAbove(class managedCostClass, what, value string) string {
	if class.small[value] {
		return ""
	}
	return fmt.Sprintf("%s %q, above the %s class (%s)", what, value, class.name,
		strings.Join(slices.Sorted(maps.Keys(class.small)), ", "))
}

// managedCostAboveGuestAccelerator is the GPU entry's first comparison: a
// compute instance's guest_accelerator is a block, and a block with an entry is
// an attached GPU.
func managedCostAboveGuestAccelerator(v any) string {
	if len(objects(v)) == 0 {
		return ""
	}
	return "an attached GPU: guest_accelerator is set"
}

// managedCostAboveGPUMachine is the GPU entry's second comparison: a machine
// type from an accelerator-optimised family is a GPU whatever the plan states
// elsewhere. A plan that states both this and a guest_accelerator is one GPU:
// the entry fires once.
func managedCostAboveGPUMachine(v any) string {
	machine := str(v)
	for _, family := range managedCostGPUFamilies {
		if strings.HasPrefix(machine, family) {
			return fmt.Sprintf("an attached GPU: the machine type %q is an accelerator-optimised type", machine)
		}
	}
	return ""
}

// managedCostAboveAccelerator is the GPU entry's third comparison: a Cloud Run
// node selector names the accelerator it wants, and an empty one is the
// provider's own way of saying that none is attached (the attribute is
// optional inside the block).
func managedCostAboveAccelerator(v any) string {
	accelerator, isString := v.(string)
	if !isString {
		return fmt.Sprintf("a node selector accelerator of %v, which is not a string", v)
	}
	if accelerator == "" {
		return ""
	}
	return fmt.Sprintf("an attached GPU: the node selector names the accelerator %q", accelerator)
}
