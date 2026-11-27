package tf

import "testing"

// The tests of DG0's class table (docs/ground-rules/delete-classes.md, D3430):
// what one planned removal deletes, per change. TestRemovalClassFollowsD3430
// holds the table against D3430's two lists, TestPersonGrantIsIrreplaceable and
// TestServiceAccountGrantIsRecreatable hold the member half of the rule (D3454,
// D3456), and TestUnknownTypeRefused holds the fail-closed direction. The three
// that read a change are the rule DG1a, DG2, DG3, UO2, AG2a and DG4 consume.

// TestRemovalClassFollowsD3430 holds the class table against D3430's two lists:
// what Fugaro made and can make again is ClassRecreatable and what it cannot put
// back is ClassIrreplaceable, an unclassified type is irreplaceable, and
// RefusesRemovalType is the same table's bearing column read by a caller with
// a type alone — D1551's question, which DG0 leaves answering as it did.
func TestRemovalClassFollowsD3430(t *testing.T) {
	for _, tc := range []struct {
		name   string
		typ    string
		before map[string]any
		want   Class
	}{
		// D3430's recreatable list: Cloud Run jobs and services, Scheduler jobs,
		// images and registry repositories, build artifacts, and the service
		// accounts and IAM bindings Fugaro made.
		{"a Cloud Run job", "google_cloud_run_v2_job", nil, ClassRecreatable},
		{"a Cloud Run service", "google_cloud_run_v2_service", nil, ClassRecreatable},
		{"a scheduler job", "google_cloud_scheduler_job", nil, ClassRecreatable},
		{"a registry repository", "google_artifact_registry_repository", nil, ClassRecreatable},
		// The bindings Fugaro wrote: a service account's grant of a role, which
		// the same apply writes again.
		{"a registry binding", "google_artifact_registry_repository_iam_member",
			map[string]any{"member": "serviceAccount:fugaro-build@proj-1.iam.gserviceaccount.com"}, ClassRecreatable},
		{"a Fugaro-made service account", "google_service_account", map[string]any{"account_id": "fugaro-run"}, ClassRecreatable},
		// D3430's irreplaceable list, by the types this tree holds: secrets,
		// buckets, the RTDB, KMS keys and the logs.
		{"a secret", "google_secret_manager_secret", nil, ClassIrreplaceable},
		{"a bucket", "google_storage_bucket", nil, ClassIrreplaceable},
		{"a bucket object", "google_storage_bucket_object", nil, ClassIrreplaceable},
		{"the RTDB", "google_firebase_database_instance", nil, ClassIrreplaceable},
		{"a KMS key", "google_kms_crypto_key", nil, ClassIrreplaceable},
		{"the log bucket", "google_logging_project_bucket_config", nil, ClassIrreplaceable},
		// D3456: the workspace service account is the one account that is not
		// recreatable, because Link is the member owner's act and no admin list
		// exists for update to re-grant from.
		{"the workspace service account", "google_service_account", map[string]any{"account_id": WorkspaceIAMAccountID}, ClassIrreplaceable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RemovalClass(removal(tc.typ, tc.before), nil); got != tc.want {
				t.Errorf("RemovalClass(%s) = %s, want %s: D3430 decides this by type, and D3456 by the account", tc.typ, got, tc.want)
			}
		})
	}

	// Every allowedTypes entry has a row, so a later module's type is a decision
	// and not a default: the fail-closed direction is for a type nobody has
	// decided about, and this is what tells the two apart.
	for typ := range allowedTypes {
		if _, ok := removalClasses[typ]; !ok {
			t.Errorf("%s is in allowedTypes and in no removalClasses row: DG0 requires every type decided, and an undecided type refuses every removal", typ)
		}
	}

	// RefusesRemovalType is D1551's column read by a caller holding a type
	// alone, and DG0 moves none of it: the two types D3430 makes recreatable —
	// a registry repository and a service account — keep refusing here until
	// DG1a takes the gate that asks this onto RemovalClass, which is what makes
	// DG0 inert.
	for _, tc := range []struct {
		typ  string
		want bool
	}{
		{"google_artifact_registry_repository", true},
		{"google_service_account", true},
		{"google_secret_manager_secret", true},
		{"google_storage_bucket", true},
		{"google_cloud_run_v2_job", false},
		{"google_firestore_database", true}, // unclassified: nobody decided it
	} {
		if got := RefusesRemovalType(tc.typ); got != tc.want {
			t.Errorf("RefusesRemovalType(%s) = %v, want %v: it is D1551's column, and an unclassified type is irreplaceable", tc.typ, got, tc.want)
		}
	}
}

// TestPersonGrantIsIrreplaceable is D3430's membership half and D3454's hybrid:
// a grant naming a person is irreplaceable — `update` never deletes a member's
// grant on its own — and admitted only when the roster records that person's
// removal, which is the record DG5 writes when a membership command revokes what
// it can reach.
func TestPersonGrantIsIrreplaceable(t *testing.T) {
	const person = "operator@example.com"
	grantTo := func(typ, member string) ResourceChange {
		return removal(typ, map[string]any{"member": member, "role": "roles/run.developer"})
	}
	// The member types a person's grant appears on: the project role, the
	// service-account actAs and the bucket and registry grants.
	for _, typ := range []string{
		"google_project_iam_member",
		"google_service_account_iam_member",
		"google_storage_bucket_iam_member",
		"google_artifact_registry_repository_iam_member",
	} {
		if got := RemovalClass(grantTo(typ, "user:"+person), nil); got != ClassIrreplaceable {
			t.Errorf("RemovalClass(%s, a person's grant) = %s, want %s: a person's grant is membership (D3430)", typ, got, ClassIrreplaceable)
		}
	}
	// A principal that is neither a service account nor a recorded person is
	// refused too: a group, a domain and a public member are all fail-closed.
	for _, member := range []string{
		"group:ops@example.com", "domain:example.com", "allUsers", "user:", "",
	} {
		if got := RemovalClass(grantTo("google_project_iam_member", member), nil); got != ClassIrreplaceable {
			t.Errorf("RemovalClass of a grant to %q = %s, want %s: only a service account and a recorded person are admitted", member, got, ClassIrreplaceable)
		}
	}
	// The roster's record is what admits the removal, in either spelling of the
	// member: the roster holds the address, and the member is "user:" and the
	// address.
	for _, roster := range [][]string{{person}, {"user:" + person}} {
		if got := RemovalClass(grantTo("google_project_iam_member", "user:"+person), roster); got != ClassRecreatable {
			t.Errorf("RemovalClass of a recorded person's grant with roster %v = %s, want %s: D3454 admits it once the roster records the removal", roster, got, ClassRecreatable)
		}
	}
	// And the record is that person's, not any record at all.
	if got := RemovalClass(grantTo("google_project_iam_member", "user:"+person), []string{"someone-else@example.com"}); got != ClassIrreplaceable {
		t.Errorf("RemovalClass of an unrecorded person's grant = %s, want %s: another person's record admits nothing", got, ClassIrreplaceable)
	}
}

// TestServiceAccountGrantIsRecreatable is the other half of the member rule: a
// binding Fugaro wrote, naming a service account, is one the same apply makes
// again (D3430), so the gate admits its removal with no roster entry and no
// approval.
func TestServiceAccountGrantIsRecreatable(t *testing.T) {
	const account = "serviceAccount:fugaro-run-proj-1@proj-1.iam.gserviceaccount.com"
	for _, typ := range []string{
		"google_project_iam_member",
		"google_service_account_iam_member",
		"google_storage_bucket_iam_member",
		"google_cloud_run_v2_job_iam_member",
		"google_secret_manager_secret_iam_member",
	} {
		rc := removal(typ, map[string]any{"member": account, "role": "roles/run.developer"})
		if got := RemovalClass(rc, nil); got != ClassRecreatable {
			t.Errorf("RemovalClass(%s, a service account's grant) = %s, want %s: a Fugaro-made binding is recreatable (D3430)", typ, got, ClassRecreatable)
		}
	}
	// The roster says nothing about service accounts, and it is not needed: a
	// record of an unrelated person changes nothing.
	rc := removal("google_project_iam_member", map[string]any{"member": account})
	if got := RemovalClass(rc, []string{"operator@example.com"}); got != ClassRecreatable {
		t.Errorf("RemovalClass of a service account's grant with a roster = %s, want %s: a binding is judged by its member, not by the roster", got, ClassRecreatable)
	}
}

// TestUnknownTypeRefused is the fail-closed direction every caller depends on:
// a type nobody has decided about is irreplaceable, whether the caller holds
// the type alone (RefusesRemovalType) or a whole change (RemovalClass). The
// fixture is a type no row names, and the test fails loudly if one is added for
// it, so it cannot silently stop exercising the case.
func TestUnknownTypeRefused(t *testing.T) {
	const unclassified = "google_firestore_database"
	if _, classified := DataBearingType(unclassified); classified {
		t.Fatalf("%s is classified now, so it no longer exercises the unclassified case: pick a type with no removalClasses row", unclassified)
	}
	if !RefusesRemovalType(unclassified) {
		t.Errorf("RefusesRemovalType(%s) = false, want true: a type nobody decided is irreplaceable", unclassified)
	}
	rc := removal(unclassified, map[string]any{"name": "db"})
	if got := RemovalClass(rc, nil); got != ClassIrreplaceable {
		t.Errorf("RemovalClass(%s) = %s, want %s: an unclassified type is irreplaceable, whatever the change carries", unclassified, got, ClassIrreplaceable)
	}
	// Even a change whose shape the member half would otherwise admit: an
	// unknown member type is refused for being unknown, not read for its member.
	unknownGrant := removal("google_firestore_database_iam_member", map[string]any{"member": "serviceAccount:x@p.iam.gserviceaccount.com"})
	if got := RemovalClass(unknownGrant, nil); got != ClassIrreplaceable {
		t.Errorf("RemovalClass(an unclassified grant type) = %s, want %s: the type is decided before the member is read", got, ClassIrreplaceable)
	}
}
