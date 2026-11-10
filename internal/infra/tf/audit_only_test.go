package tf

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
)

// The tests of D1545's audit-only split (docs/no-prompt-policy/reconciliation.md,
// plan T9): the managed gate stops refusing a plan for its CONTENT and records
// and warns instead, while the integrity refusals of D1549 and the type rule of
// D1551 stay. TestEveryAllowedTypeIsClassifiedDataBearingOrNot is D1551's table
// (cover.go), TestAnUnclassifiedTypeRefusesDelete is the delete rule the guard
// reads (guard.go), and TestNotCoveredSplitTellsIntegrityFromContent is the
// cover's own two kinds (managed_cover.go).

// TestEveryAllowedTypeIsClassifiedDataBearingOrNot holds D1551's classification:
// every entry of allowedTypes is decided — data-bearing or not — so a type
// added to the list later cannot pass unclassified, the two tables neither
// overlap nor name a type outside allowedTypes, and the nine data-bearing names
// are exactly D1551's list.
func TestEveryAllowedTypeIsClassifiedDataBearingOrNot(t *testing.T) {
	for typ := range allowedTypes {
		bearing, classified := DataBearingType(typ)
		if !classified {
			t.Errorf("%s is in allowedTypes and in neither classification table: D1551 requires every type decided, and an undecided type refuses every delete or replace", typ)
			continue
		}
		if want := dataBearingTypes[typ]; bearing != want {
			t.Errorf("DataBearingType(%s) = %v, want %v", typ, bearing, want)
		}
	}
	for typ := range dataBearingTypes {
		if !allowedTypes[typ] {
			t.Errorf("dataBearingTypes names %s, which is not in allowedTypes", typ)
		}
	}
	for typ := range nonDataBearingTypes {
		if !allowedTypes[typ] {
			t.Errorf("nonDataBearingTypes names %s, which is not in allowedTypes", typ)
		}
		if dataBearingTypes[typ] {
			t.Errorf("%s is in both classification tables", typ)
		}
	}
	// D1551's nine, written out: the failure names the type if the table drifts
	// from the ruling.
	wantBearing := []string{
		"google_apikeys_key",
		"google_artifact_registry_repository",
		"google_firebase_database_instance",
		"google_logging_log_view",
		"google_logging_project_bucket_config",
		"google_secret_manager_secret",
		"google_service_account",
		"google_storage_bucket",
		"google_storage_bucket_object",
	}
	if got := slices.Sorted(maps.Keys(dataBearingTypes)); !slices.Equal(got, wantBearing) {
		t.Errorf("the data-bearing types are %v, want D1551's nine:\n%v", got, wantBearing)
	}
	if got, want := len(dataBearingTypes), 9; got != want {
		t.Errorf("data-bearing types = %d, want %d", got, want)
	}
	if got, want := len(nonDataBearingTypes), 22; got != want {
		t.Errorf("non-data-bearing types = %d, want %d", got, want)
	}
	if got, want := len(dataBearingTypes)+len(nonDataBearingTypes), len(allowedTypes); got != want {
		t.Errorf("the two tables hold %d types, want all %d of allowedTypes", got, want)
	}
}

// TestAnUnclassifiedTypeRefusesDelete is D1551's delete rule as the managed
// apply's guard reads it (GuardManaged): a delete of a type in neither
// classification table refuses, a delete of a classified, non-data-bearing type
// is taken with a warning (D1545), and a delete of a data-bearing type still
// refuses (D1551).
func TestAnUnclassifiedTypeRefusesDelete(t *testing.T) {
	deletePlan := func(typ string) *Plan {
		return &Plan{FormatVersion: "1.2", ResourceChanges: []ResourceChange{{
			Address: "module.managed." + typ + ".this", Mode: "managed", Type: typ,
			ProviderName: "registry.terraform.io/hashicorp/google",
			Change:       Change{Actions: []string{"delete"}, Before: map[string]any{"name": "x"}},
		}}}
	}
	// A type neither table names: D1551's "a type added later", which refuses
	// until somebody classifies it. The guard below fails loudly if this name
	// is ever classified, so the fixture cannot silently stop exercising the
	// unclassified case.
	const unclassified = "google_firestore_database"
	if _, classified := DataBearingType(unclassified); classified {
		t.Fatalf("%s is classified now, so it no longer exercises the unclassified case: pick a type still in neither table", unclassified)
	}
	if refused, warned := GuardManaged(deletePlan(unclassified), nil, nil); len(refused) != 1 || len(warned) != 0 {
		t.Errorf("a delete of the unclassified %s gives refused %v, warned %v, want one refusal and no warning", unclassified, refused, warned)
	}
	// A classified, non-data-bearing type: the gate records and warns (D1545).
	refused, warned := GuardManaged(deletePlan("google_project_service"), nil, nil)
	if len(refused) != 0 || len(warned) != 1 {
		t.Errorf("a delete of google_project_service gives refused %v, warned %v, want no refusal and one warning", refused, warned)
	} else if !strings.Contains(warned[0], "google_project_service") {
		t.Errorf("the warning %q does not name the change", warned)
	}
	// A data-bearing type: the delete still refuses (D1551).
	if refused, warned := GuardManaged(deletePlan("google_storage_bucket"), nil, nil); len(refused) != 1 || len(warned) != 0 {
		t.Errorf("a delete of google_storage_bucket gives refused %v, warned %v, want one refusal and no warning", refused, warned)
	}
}

// TestTheDeleteRulesOwnReasonsFollowTheType is the guard's half of the split
// the cover takes for the delete rule's own sentences: a list that NAMES the
// removal's address without approving the object the plan removes — an entry
// bound to another object, a before view that identifies nothing — is content
// for a type D1551 takes and integrity for a type it protects. The guard
// refused both whatever the type was, so one removal came back refused by this
// half of the gate (Verdict.OK false, and the apply stopped) while the cover
// wrote the same sentence as one of D1545's warnings: the plan was refused for
// a removal D1545 says is recorded and warned, and TestGateSeesOneRemovalOnce
// is what reads the two halves together.
func TestTheDeleteRulesOwnReasonsFollowTheType(t *testing.T) {
	const taken, bearing, unclassified = "google_cloud_run_v2_job", "google_storage_bucket", "google_firestore_database"
	if RefusesRemovalType(taken) {
		t.Fatalf("%s refuses its own removal now, so it no longer exercises D1545's side of the table", taken)
	}
	if !RefusesRemovalType(bearing) {
		t.Fatalf("%s no longer refuses its own removal, so it no longer exercises D1551's data-bearing side", bearing)
	}
	if _, classified := DataBearingType(unclassified); classified {
		t.Fatalf("%s is classified now: pick a type in neither table for the unclassified case", unclassified)
	}
	// bound is an entry that names the address for an object the plan does not
	// show there: what Approves refuses for the identity it holds.
	bound := func(addr string) []DeleteEntry {
		return []DeleteEntry{{Address: addr, Before: "type=" + taken + " name=fmgd-other"}}
	}
	job := managedKeptFixture[taken].Change.After
	// A view of a type whose identity turns on no per-instance attribute and
	// carries none of the three name-derived ones identifies nothing, which is
	// the other sentence Approves writes for a listed address (identity.go).
	anonymous := map[string]any{"service": "run.googleapis.com"}
	for _, tc := range []struct {
		name, half, want string
		typ              string
		before           map[string]any
		entries          []DeleteEntry
	}{
		{"an entry bound to another object, a type D1551 takes", "warn", "for another object", taken, job, bound("module.m." + taken + ".this")},
		{"a before view that identifies nothing, a type D1551 takes", "warn", "does not identify", "google_project_service", anonymous,
			[]DeleteEntry{{Address: "module.m.google_project_service.this", Before: "type=google_project_service"}}},
		{"an entry bound to another object, a data-bearing type", "refuse", "for another object", bearing, bucketBefore("fmgd-b", "2026-01-02T03:04:05Z"), bound("module.m." + bearing + ".this")},
		{"an entry bound to another object, an unclassified type", "refuse", "for another object", unclassified, job, bound("module.m." + unclassified + ".this")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := managedPlan(removal(tc.typ, tc.before))
			refused, warned := GuardManaged(p, tc.entries, nil)
			half, other := warned, refused
			if tc.half == "refuse" {
				half, other = refused, warned
			}
			if len(half) != 1 || !strings.Contains(half[0], tc.want) {
				t.Errorf("%s: the guard gives %s %q, want one item naming %q", tc.name, tc.half, half, tc.want)
			}
			if len(other) != 0 {
				t.Errorf("%s: the guard gives the other half %q, want nothing: one removal is one answer", tc.name, other)
			}
			// And the reason itself is the rule's own sentence either way: the
			// same text the cover writes for it, which is what a reader parses.
			if _, why := Approves(tc.entries, p.ResourceChanges[0]); why == "" || !strings.Contains(half[0], why) {
				t.Errorf("%s: the item %q does not carry the delete rule's sentence %q", tc.name, half, why)
			}
		})
	}
}

// TestNotCoveredSplitTellsIntegrityFromContent is D1545's split at the cover:
// the type table and the grant table are content (recorded and warned), while
// the managed configuration rule, the mode rule and the provider rule are
// integrity (still refused). Each item is unchanged — NotCovered returns the
// same ones — and the two halves hold them, so a reader parses either the way
// it parses a refusal.
func TestNotCoveredSplitTellsIntegrityFromContent(t *testing.T) {
	// A provisioner is the managed configuration rule (D1549 by name).
	provisioner := managedPlan(mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "bucket"}))
	provisioner.Configuration.RootModule.Resources = []ConfigResource{{Address: "google_storage_bucket.this",
		Mode: "managed", Type: "google_storage_bucket", Provisioners: []json.RawMessage{json.RawMessage(`{"type":"local-exec"}`)}}}
	// A data source and a foreign provider are the change's own mode and
	// provider rules.
	dataMode := managedPlan(mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "bucket"}))
	dataMode.ResourceChanges[0].Mode = "data"
	foreign := managedPlan(mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "bucket"}))
	foreign.ResourceChanges[0].ProviderName = "registry.terraform.io/hashicorp/aws"
	// The rest of the resource chain, one fixture each: every rule of it below
	// the type table is D1549's, so each of these is integrity whatever the
	// change's actions are, and a mutation that made one content — a warning the
	// plan then applies — has to fail a test (the table below is what fails it).
	authoritativeIAM := managedPlan(mrc("google_storage_bucket_iam_binding",
		map[string]any{"bucket": ManagedPrefix + "bucket"}))
	serviceKey := managedPlan(mrc("google_service_account_key", nil))
	billing := managedPlan(mrc("google_billing_budget", map[string]any{"billing_account": "billingAccounts/CCCC-DDDD"}))
	// managedResource's name rule: a kept type whose own name is not the
	// managed project's.
	outsidePrefix := managedPlan(mrc("google_cloud_run_v2_job", map[string]any{"name": "fugaro-coordinator"}))
	// managedProject: a well-named resource that lands in another project.
	otherProject := managedPlan(mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "bucket", "project": "other"}))
	// The import rules, which read the plan's importing block rather than its
	// views: an ID outside this run's projects.
	imported := mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "bucket"})
	imported.Change.Importing = &Importing{ID: "other-project/fugaro-bucket"}
	foreignImport := managedPlan(imported)

	for _, tc := range []struct {
		name, half, want string
		plan             *Plan
	}{
		{"a provisioner", "integrity", "a provisioner", provisioner},
		{"a data source", "integrity", "a data source", dataMode},
		{"a foreign provider", "integrity", "registry.terraform.io/hashicorp/aws", foreign},
		{"an authoritative IAM resource", "integrity", "an authoritative IAM resource", authoritativeIAM},
		{"a service account key", "integrity", "a service account key", serviceKey},
		{"a billing account the review did not show", "integrity", "billing account CCCC-DDDD", billing},
		{"a name outside the managed prefix", "integrity", `"fugaro-coordinator", outside the managed prefix`, outsidePrefix},
		{"a resource in another project", "integrity", "in project other, which is not this run's", otherProject},
		{"an import outside this run's projects", "integrity", "which is not in the managed project's projects", foreignImport},
		{"a dropped type", "content", "allowlist leaves out", managedPlan(mrc("google_logging_project_sink", nil))},
		{"a grant the table does not make", "content", "basic role", managedPlan(mrc("google_storage_bucket_iam_member",
			map[string]any{"bucket": ManagedPrefix + "bucket", "member": "serviceAccount:" + managedAccount, "role": "roles/owner"}))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contains := func(items []string) bool {
				return slices.ContainsFunc(items, func(s string) bool { return strings.Contains(s, tc.want) })
			}
			c := managedTestCover()
			integrity, content := c.NotCoveredSplit(tc.plan, nil)
			half, other := integrity, content
			if tc.half == "content" {
				half, other = content, integrity
			}
			if !contains(half) {
				t.Errorf("%s: %s holds %q, want an item naming %q", tc.name, tc.half, half, tc.want)
			}
			if contains(other) {
				t.Errorf("%s: the %q item is in the wrong half (%v)", tc.name, tc.want, other)
			}
			// The halves partition NotCovered's own items — the same walk,
			// only told apart.
			if got, want := len(c.NotCovered(tc.plan, nil)), len(integrity)+len(content); got != want {
				t.Errorf("%s: NotCovered holds %d items, the split holds %d", tc.name, got, want)
			}
		})
	}
}

// TestAReplaceIsJudgedOnWhatItCreates is the hole D1545's own arm opens if the
// removal half is the whole of what a replace is told: the delete of a
// classified, non-data-bearing type is content — recorded and warned — and the
// resource the replace CREATES has to be judged exactly as a create is, or a
// replace would be a way past every rule D1549 keeps refusing. Every case below
// is a plan whose create half the same cover refuses (TestCoverNotCovered
// carries each as a create) and that a walk stopping at the removal only warns
// about, which is what a managed project would then apply.
func TestAReplaceIsJudgedOnWhatItCreates(t *testing.T) {
	// replaced turns a change a test built as a create into the replace of the
	// same resource: the object it removes is in before, the one it creates in
	// after.
	replaced := func(rc ResourceChange) ResourceChange {
		rc.Change.Actions = []string{"delete", "create"}
		rc.Change.Before = map[string]any{"name": "fugaro-something"}
		return rc
	}
	for _, tc := range []struct {
		name, want string
		change     ResourceChange
	}{
		{"a scheduler pointed at another host", "attacker.example",
			replaced(sched("https://attacker.example/v2/projects/proj-1/locations/us-east5/jobs/j:run", "fugaro-scheduler@proj-1.iam.gserviceaccount.com"))},
		{"a custom role that grants the world", "changes a custom role's permissions",
			replaced(customRole("fugaroJobRunner", "proj-1", []any{"storage.objects.get"}))},
		{"an image from somewhere else", "evil.example",
			replaced(job("evil.example/x/y:1", nil, nil))},
		{"a sink moved to another project", "not this run's",
			replaced(rc("google_logging_project_sink", map[string]any{"project": "other",
				"destination": "logging.googleapis.com/projects/proj-1/locations/global/buckets/fugaro"}, map[string]any{}, "create"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Fail loudly if the fixture stops being the arm this is about: a
			// type D1551 protects refuses the removal itself, and the walk would
			// then never reach the checks below for a reason of its own.
			if !removalContent(tc.change) {
				t.Fatalf("%s is not a replace of a classified, non-data-bearing type, so it no longer exercises this arm", tc.change.Type)
			}
			p := &Plan{FormatVersion: "1.2", ResourceChanges: []ResourceChange{tc.change}}
			integrity, content := testCover.NotCoveredSplit(p, nil)
			if len(integrity) != 1 || !strings.Contains(integrity[0], tc.want) {
				t.Errorf("the replace gives integrity %q, want one refusal naming %q: what a replace creates is judged as a create is", integrity, tc.want)
			}
			// And the removal half is still D1545's: the warning is what the
			// session reads and the record holds for the delete the same change
			// carries.
			if len(content) != 1 || !strings.Contains(content[0], "actions delete, create") {
				t.Errorf("the replace gives content %q, want the removal kept as one warning", content)
			}
			if got, want := len(testCover.NotCovered(p, nil)), len(integrity)+len(content); got != want {
				t.Errorf("NotCovered holds %d items, the split holds %d: the halves are the whole answer", got, want)
			}

			// The same resource DELETED and not replaced is judged on what it
			// REMOVES: a delete creates nothing, so terraform writes the object
			// in before and null in after, and the walk below reads that view
			// (removed) rather than an empty after. The case's own bad view is
			// moved into before, which is what makes the two halves of one
			// resource answer the same way — a removal warned about and the
			// resource it takes away refused for the same reason a create of it
			// would be.
			gone := tc.change
			gone.Change.Actions = []string{"delete"}
			gone.Change.Before = tc.change.Change.After
			gone.Change.After = nil
			gone.Change.AfterUnknown = nil
			integrity, content = testCover.NotCoveredSplit(&Plan{FormatVersion: "1.2", ResourceChanges: []ResourceChange{gone}}, nil)
			if len(integrity) != 1 || !strings.Contains(integrity[0], tc.want) {
				t.Errorf("the delete gives integrity %q, want one refusal of the removed view naming %q: a removal is not a way past the rules below it", integrity, tc.want)
			}
			if len(content) != 1 || !strings.Contains(content[0], "actions delete") {
				t.Errorf("the delete gives content %q, want one warning for the removal", content)
			}
		})
	}
}

// TestAManagedReplaceIsJudgedOnWhatItCreates is the same hole in the managed
// walk, on the three arms the owner's delete list answers: an address the list
// does not name, an action_reason the rule does not know and an entry that
// approves another object are each content for a type D1551 takes (D1545),
// and each used to end the change there — so a replace the list warned about
// was never read for the resource it creates, which is the way past every rule
// notCoveredResource keeps. Every case below is one of those arms whose
// created half is in another project, so the walk below the arm has something
// to refuse: the removal stays one warning and the refusal is the create's. A
// pure delete of the same object is judged on the object it REMOVES instead,
// so the removal stays one warning while the resource it takes away is read —
// which refuses it when it is not this run's.
func TestAManagedReplaceIsJudgedOnWhatItCreates(t *testing.T) {
	// The fixture is a kept resource of a type D1551 does NOT protect, so its
	// removal is one of D1545's warnings rather than a refusal of its own —
	// which is what lets these cases reach the arms below instead of stopping
	// at the classification. replaced removes it from where the fixture has it
	// and creates it in somebody else's project: the resource rule the created
	// half must reach is managedProject.
	job := managedKeptFixture["google_cloud_run_v2_job"]
	if RefusesRemovalType(job.Type) {
		t.Fatalf("%s refuses its own removal now, so this fixture no longer exercises the arm: pick a type D1551 leaves to D1545", job.Type)
	}
	replaced := func() ResourceChange {
		rc := job
		rc.Change.Actions = []string{"delete", "create"}
		rc.Change.Before = map[string]any{"name": ManagedPrefix + "job"}
		rc.Change.After = maps.Clone(job.Change.After)
		rc.Change.After["project"] = "somebody-elses-project"
		return rc
	}
	for _, tc := range []struct {
		name string
		// list is the owner's delete list for this arm and reason the
		// action_reason the plan carries, and says is the arm's own wording in
		// the warning — the guard that the case reached THIS arm and not
		// another.
		list   func(rc ResourceChange) ManagedCover
		reason string
		says   string
	}{
		{"an address the list does not name", func(ResourceChange) ManagedCover { return managedTestCover() },
			"replace_because_cannot_update", "not on the owner's delete list"},
		{"an action_reason the rule does not know", func(rc ResourceChange) ManagedCover {
			c := managedTestCover()
			c.AllowDelete = approving(rc)
			return c
		}, "something_the_rule_never_heard_of", "does not know"},
		{"an entry that approves another object", func(rc ResourceChange) ManagedCover {
			c := managedTestCover()
			c.AllowDelete = []DeleteEntry{{Address: rc.Address, Before: "another-object"}}
			return c
		}, "replace_because_cannot_update", "for another object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := replaced()
			rc.ActionReason = tc.reason
			c := tc.list(rc)
			integrity, content := c.NotCoveredSplit(managedPlan(rc), nil)
			if len(integrity) != 1 || !strings.Contains(integrity[0], "not this run's") {
				t.Errorf("%s gives integrity %q, want one refusal of the resource the replace creates", tc.name, integrity)
			}
			if len(content) != 1 || !strings.Contains(content[0], tc.says) {
				t.Errorf("%s gives content %q, want one warning for %s", tc.name, content, tc.says)
			}
			if got, want := len(c.NotCovered(managedPlan(rc), nil)), len(integrity)+len(content); got != want {
				t.Errorf("%s: NotCovered holds %d items, the split holds %d", tc.name, got, want)
			}
		})
	}

	// And a pure delete is judged the same way — the walk does not end at the
	// removal's warning, it reads the object being removed (removed). This
	// fixture's own object, removed, is this run's: the warning is the whole
	// answer, as it always was. The same object removed from somebody else's
	// project is refused for the resource rule, which is the hole a delete that
	// ended at its warning left open — an unlisted destroy of a kept type was
	// waved through whatever it took away, a resource in another project, a name
	// outside the managed prefix, an object whose type says nothing about
	// removal.
	ours := job
	ours.Change.Actions = []string{"delete"}
	ours.Change.Before = maps.Clone(job.Change.After)
	ours.Change.After = nil
	ours.Change.AfterUnknown = nil
	integrity, content := managedTestCover().NotCoveredSplit(managedPlan(ours), nil)
	if len(integrity) != 0 {
		t.Errorf("a delete of this run's own object gives integrity %q, want nothing: the removal's own warning is the whole answer", integrity)
	}
	if len(content) != 1 || !strings.Contains(content[0], "not on the owner's delete list") {
		t.Errorf("a delete gives content %q, want the one warning", content)
	}

	elsewhere := ours
	elsewhere.Change.Before = maps.Clone(ours.Change.Before)
	elsewhere.Change.Before["project"] = "somebody-elses-project"
	integrity, content = managedTestCover().NotCoveredSplit(managedPlan(elsewhere), nil)
	if len(integrity) != 1 || !strings.Contains(integrity[0], "not this run's") {
		t.Errorf("a delete of somebody else's object gives integrity %q, want one refusal of the removed resource", integrity)
	}
	if len(content) != 1 || !strings.Contains(content[0], "not on the owner's delete list") {
		t.Errorf("that delete gives content %q, want the one warning for the removal", content)
	}

	// The same arm on the other half of managedResource's rule, and the shape
	// the finding was written with: an unlisted destroy of a kept type whose
	// name is not the managed project's is refused for the name, where before
	// the walk ended at the removal's warning.
	outside := managedOutsideFixture[job.Type]
	outside.Change.Actions = []string{"delete"}
	outside.Change.Before = maps.Clone(outside.Change.After)
	outside.Change.After = nil
	outside.Change.AfterUnknown = nil
	integrity, content = managedTestCover().NotCoveredSplit(managedPlan(outside), nil)
	if len(integrity) != 1 || !strings.Contains(integrity[0], ManagedPrefix) {
		t.Errorf("a delete of a name outside the prefix gives integrity %q, want one refusal naming %s", integrity, ManagedPrefix)
	}
	if len(content) != 1 || !strings.Contains(content[0], "not on the owner's delete list") {
		t.Errorf("that delete gives content %q, want the one warning for the removal", content)
	}
}

// TestDataBearingTypeStaysIntegrityWhenItsRemovalIsListed is D1551 at the
// cover's type table: a delete of a dropped data-bearing type refuses whatever
// reason is weighed, so even a LISTED and approved removal — one that passes the
// delete rule's own sites — comes back in NotCoveredSplit's integrity half for
// the type-table reason, while the same type CREATED is content (D1545). Without
// removalRefuses the type table would warn and the removal would apply, which is
// the case D1551 exists to keep refused.
func TestDataBearingTypeStaysIntegrityWhenItsRemovalIsListed(t *testing.T) {
	// One of D1551's nine that the managed list also drops, so its create is
	// refused for the type table. Fail loudly if either half of that stops
	// holding: the fixture would no longer reach the arm it is about.
	const typ = "google_firebase_database_instance"
	if bearing, classified := DataBearingType(typ); !bearing || !classified {
		t.Fatalf("%s is not a classified data-bearing type, so this fixture no longer exercises D1551", typ)
	}
	if _, dropped := managedDropped[typ]; !dropped {
		t.Fatalf("%s is not dropped from the managed list, so its create would not carry the type-table reason", typ)
	}

	t.Run("a create is content", func(t *testing.T) {
		integrity, content := managedTestCover().NotCoveredSplit(managedPlan(mrc(typ, map[string]any{"name": ManagedPrefix + "db"})), nil)
		if len(integrity) != 0 || len(content) != 1 || !strings.Contains(content[0], "allowlist leaves out") {
			t.Errorf("a create gives integrity %q, content %q, want one content item naming the type table", integrity, content)
		}
	})

	t.Run("a listed delete is integrity", func(t *testing.T) {
		c := managedTestCover()
		p := managedPlan(removal(typ, map[string]any{"id": ManagedPrefix + "db", "name": ManagedPrefix + "db", "project": "proj-1"}))
		c.AllowDelete = []DeleteEntry{ApprovalFor(p.ResourceChanges[0])}
		// The removal must be approved, so the fixture reaches the type table
		// on the removed view rather than stopping at the delete rule's own
		// site (which refuses it independently): that is the case the type
		// table's content flag is about.
		if ok, _ := Approves(c.AllowDelete, p.ResourceChanges[0]); !ok {
			t.Fatal("the removed object is not approved, so this fixture does not reach the type table")
		}
		integrity, content := c.NotCoveredSplit(p, nil)
		if len(content) != 0 {
			t.Fatalf("a listed delete gives content %q, want the type-table reason kept in integrity", content)
		}
		if len(integrity) != 1 || !strings.Contains(integrity[0], "allowlist leaves out") {
			t.Errorf("a listed delete gives integrity %q, want one item naming the type table", integrity)
		}
	})
}
