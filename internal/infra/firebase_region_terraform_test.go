//go:build terraform

package infra

import (
	"maps"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// The budget's Realtime Database instance: a NEW one is created in the
// location the installation's region maps to, and one that EXISTS keeps the
// location it was created in (LP2, D3089, D3091, F8, R4). The module says both
// — `region = var.database_region` under `lifecycle { ignore_changes =
// [region] }` — and the two-run mock pair in
// deploy/terraform/gcp/roots/firebase/tests/database_region.tftest.hcl is one
// half of the same fact.
//
// These are the other half, and they are planned against a PRIOR STATE with
// the REAL provider (rootPlanAgainst), because that is the only place a plan
// like the owner's is computed: the state says "this instance exists, in this
// location", the mapping says another, and what a release would do to the
// installation is read back out of the plan. The mock provider cannot say it —
// it plans no replacement with or without ignore_changes, measured — so an
// assertion about a replacement there proves nothing. What is asserted here is
// the PLANNED region, which is the state's own when the field is ignored and
// the config's when it is not.

// firebaseProviderOverride is providerOverride with the Firebase root's second
// provider: it is the only root that uses google-beta, and a provider block
// its configuration does not require is an error.
const firebaseProviderOverride = providerOverride + `
provider "google-beta" {
  access_token                = "no-credentials-the-plan-makes-no-api-call"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_provider_registry      = true
}
`

// firebasePlanAgainst plans the Firebase root for spec against the prior state
// state builds, which is the plan `fugaro update` writes against the
// installation it finds. A nil state is a plan of a Firebase project that
// holds nothing.
func firebasePlanAgainst(t *testing.T, spec FirebaseSpec, state func(vars map[string]any) map[string]any) (*tf.Plan, map[string]plannedResource) {
	t.Helper()
	vars, err := FirebaseVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	return rootPlanAgainst(t, "firebase", vars, nil, state, firebaseProviderOverride)
}

// firebaseDBSpec is the Firebase root's spec for an installation in region,
// with the one project-wide account shape the module requires, and with its
// Firestore database there: that is the answer `fugaro update` reads before it
// applies this root on an installation that has completed a setup, and what the
// run index's composite indexes are planned against (RI2). The specs of this
// package are the plan of such an installation; a test about the other answer
// sets FirebaseSpec.FirestoreDatabaseExists to false itself.
func firebaseDBSpec(t *testing.T, region string, fp string) FirebaseSpec {
	t.Helper()
	spec, err := Firebase(InstallationSpec{
		Project: "proj-1234", FugaroProject: "aurora", Region: region, RunsBucket: "fugaro-runs-proj-1234",
	}, FirebaseInputs{FP: fp, HistoryAccount: "fugaro-history@proj-1234.iam.gserviceaccount.com", FirestoreDatabaseExists: true})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// instanceAddress is the instance's address in the Firebase root.
const instanceAddress = "module.firebase.google_firebase_database_instance.this"

// databaseState is the terraform state an earlier apply of the Firebase root
// would have written for its one database instance, in the location it was
// created in: the attributes a real state carries, including the computed ones
// (a plan that finds them missing renders them as "known after apply", which
// is a change, and the point here is a plan with no change to the database).
// The URL is the one Google answers for each location.
func databaseState(t *testing.T, vars map[string]any, location string) map[string]any {
	t.Helper()
	project, _ := vars["project"].(string)
	id := firebaseDBInstanceID(project)
	name := "projects/" + project + "/locations/" + location + "/instances/" + id
	url := "https://" + id + "." + location + ".firebasedatabase.app"
	if location == "us-central1" {
		url = "https://" + id + ".firebaseio.com"
	}
	return map[string]any{
		"version": 4,
		"serial":  1,
		"lineage": "00000000-0000-0000-0000-000000000000",
		"outputs": map[string]any{},
		"resources": []any{map[string]any{
			"mode":     "managed",
			"type":     "google_firebase_database_instance",
			"name":     "this",
			"provider": `provider["registry.terraform.io/hashicorp/google-beta"]`,
			"module":   "module.firebase",
			"instances": []any{map[string]any{
				"schema_version": 0,
				"attributes": map[string]any{
					"project":         project,
					"region":          location,
					"instance_id":     id,
					"type":            firebaseDBType,
					"desired_state":   "ACTIVE",
					"deletion_policy": "DELETE",
					"database_url":    url,
					"name":            name,
					"id":              name,
					"state":           "ACTIVE",
					"timeouts":        nil,
				},
				"sensitive_attributes": []any{},
			}},
		}},
	}
}

// plannedInstance is the instance's row in a plan, and the location the plan
// leaves it in.
func plannedInstance(t *testing.T, rows map[string]plannedResource) (plannedResource, string) {
	t.Helper()
	rc, ok := rows[instanceAddress]
	if !ok {
		t.Fatalf("the plan names no database instance: %v", maps.Keys(rows))
	}
	region, _ := rc.After["region"].(string)
	return rc, region
}

// TestAnExistingInstanceKeepsItsOwnRegion (LP2, F8, R4, D3091): the instance
// an installation already has keeps the location it was created in. The
// installation here is European — its mapping says europe-west1 — and its
// instance is in us-central1, which is every installation created before this
// work and every one that adopts a Firebase project whose default instance
// Firebase itself made. A plan that wrote the mapping's answer over it would
// move a database that cannot move.
func TestAnExistingInstanceKeepsItsOwnRegion(t *testing.T) {
	spec := firebaseDBSpec(t, "europe-west1", "aurora-fp")
	if spec.DatabaseRegion != "europe-west1" {
		t.Fatalf("the fixture's mapping says %q; want europe-west1 for a European installation", spec.DatabaseRegion)
	}
	_, rows := firebasePlanAgainst(t, spec, func(vars map[string]any) map[string]any {
		return databaseState(t, vars, "us-central1")
	})
	rc, region := plannedInstance(t, rows)
	if region != "us-central1" {
		t.Errorf("the plan puts the instance in %q; want us-central1, the location it was created in, whatever the mapping now says", region)
	}
	if strings.Join(rc.Actions, ",") != "no-op" {
		t.Errorf("the plan's actions for the instance are %v; want no-op: the database holds the budget counters and the run registry", rc.Actions)
	}
}

// TestAManagedInstanceOfAnEUInstallationPlansNoChange (LP2): an installation
// in a European region whose instance is in us-central1 plans NO CHANGE at
// all. It is a case of its own beside the one above because it is the whole
// plan and not the one row: the mapping's answer (europe-west1) is what the
// module would write for a new instance, and a release that let it through
// would replace a database Terraform already manages — which the guard refuses
// and which would delete the ledger and the registry.
func TestAManagedInstanceOfAnEUInstallationPlansNoChange(t *testing.T) {
	spec := firebaseDBSpec(t, "europe-west1", "aurora-fp")
	_, rows := firebasePlanAgainst(t, spec, func(vars map[string]any) map[string]any {
		return databaseState(t, vars, "us-central1")
	})
	rc, region := plannedInstance(t, rows)
	if region != "us-central1" || strings.Join(rc.Actions, ",") != "no-op" {
		t.Errorf("the instance is planned %v in %q; want no change in us-central1", rc.Actions, region)
	}
	for _, r := range rows {
		if strings.Join(r.Actions, ",") == "delete" || strings.Contains(strings.Join(r.Actions, ","), "delete") {
			t.Errorf("%s is planned %v: no resource of this root may be deleted by an installation's ordinary plan", r.Address, r.Actions)
		}
	}
}

// TestANewInstanceIsCreatedWhereTheMappingSays (LP2, D3089) is the positive
// half at the same level: a Firebase project that holds nothing gets the
// instance in the location the installation's region maps to, and for a
// European installation that is europe-west1 rather than the us-central1 of
// every installation before this release.
func TestANewInstanceIsCreatedWhereTheMappingSays(t *testing.T) {
	spec := firebaseDBSpec(t, "europe-west1", "aurora-fp")
	_, rows := firebasePlanAgainst(t, spec, nil)
	rc, region := plannedInstance(t, rows)
	if region != "europe-west1" || !strings.Contains(strings.Join(rc.Actions, ","), "create") {
		t.Errorf("a Firebase project that holds nothing plans the instance %v in %q; want a create in europe-west1", rc.Actions, region)
	}
}
