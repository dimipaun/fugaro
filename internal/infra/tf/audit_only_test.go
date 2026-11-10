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

	for _, tc := range []struct {
		name, half, want string
		plan             *Plan
	}{
		{"a provisioner", "integrity", "a provisioner", provisioner},
		{"a data source", "integrity", "a data source", dataMode},
		{"a foreign provider", "integrity", "registry.terraform.io/hashicorp/aws", foreign},
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

			// The same resource DELETED and not replaced is still only the
			// warning: a plain delete has no after view for the rules below to
			// read, so the walk stops at D1545's record-and-warn exactly as it
			// did before this arm carried on for a replace.
			gone := tc.change
			gone.Change.Actions = []string{"delete"}
			gone.Change.After = nil
			integrity, content = testCover.NotCoveredSplit(&Plan{FormatVersion: "1.2", ResourceChanges: []ResourceChange{gone}}, nil)
			if len(integrity) != 0 {
				t.Errorf("the delete gives integrity %q, want nothing: a delete creates no resource for those rules to read", integrity)
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
// pure delete of the same object is still only the warning, which is the arm
// the walk is allowed to end at.
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

	// And a pure delete of the same object is the arm the walk DOES end at:
	// nothing is created, so there is nothing below to read and the removal's
	// own warning is the whole answer. The unlisted arm is the one that always
	// warns, so the case is written on it.
	gone := replaced()
	gone.Change.Actions = []string{"delete"}
	gone.Change.After = nil
	integrity, content := managedTestCover().NotCoveredSplit(managedPlan(gone), nil)
	if len(integrity) != 0 {
		t.Errorf("a delete gives integrity %q, want nothing: only the removal is read", integrity)
	}
	if len(content) != 1 || !strings.Contains(content[0], "not on the owner's delete list") {
		t.Errorf("a delete gives content %q, want the one warning", content)
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
