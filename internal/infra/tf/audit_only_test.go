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
