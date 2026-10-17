package tf

import (
	"slices"
	"strings"
	"testing"
)

// The managed allowlist (Task 1.1). These tests are the list's own review: one
// fixture per entry the list keeps, one per entry it leaves out, and one per
// naming rule, each named for the entry, so the list and its tests read side
// by side. The golden test at the top of the file pins the list against a
// literal, so a widening is a visible diff rather than a quiet one.

func managedTestCover() ManagedCover {
	return ManagedCover{Cover: Cover{
		Projects: []string{"proj-1"},
		Listed:   []string{"user:owner@example.com", "group:ops@example.com"},
		Registry: "us-east5-docker.pkg.dev",
		Region:   "us-east5",
		Buckets:  []string{"fugaro-runs-proj-1"},
		Billing:  []string{"AAAA-BBBB"},
	}}
}

// mrc is one resource of a managed plan: the address is the type's own, so a
// refusal's address says which fixture it came from.
func mrc(typ string, after map[string]any) ResourceChange {
	if after == nil {
		after = map[string]any{}
	}
	return ResourceChange{Address: "module.managed." + typ + ".this", Type: typ,
		Change: Change{Actions: []string{"create"}, After: after, AfterUnknown: map[string]any{}}}
}

func managedPlan(changes ...ResourceChange) *Plan {
	return &Plan{FormatVersion: "1.2", ResourceChanges: changes}
}

// managedAccount is the email of a managed service account of the managed
// project, the shape a managed plan's own grants point at.
const managedAccount = ManagedPrefix + "run@proj-1.iam.gserviceaccount.com"

// TestManagedAllowedTypesGolden pins both halves of the decision against
// literals: the 14 types a managed plan may contain, and the 12 entries of
// allowedTypes it may not. A widening — or a type dropped without a reason —
// is a diff in this test.
func TestManagedAllowedTypesGolden(t *testing.T) {
	want := []string{
		"google_apikeys_key",
		"google_artifact_registry_repository",
		"google_artifact_registry_repository_iam_member",
		"google_cloud_run_v2_job",
		"google_cloud_run_v2_job_iam_member",
		"google_cloud_scheduler_job",
		"google_project_service",
		"google_secret_manager_secret",
		"google_secret_manager_secret_iam_member",
		"google_service_account",
		"google_service_account_iam_member",
		"google_storage_bucket",
		"google_storage_bucket_iam_member",
		"google_storage_bucket_object",
	}
	if got := ManagedAllowedTypes(); !slices.Equal(got, want) {
		t.Errorf("the managed allowlist is %v, want %v", got, want)
	}
	wantDropped := []string{
		"google_billing_budget",
		"google_firebase_database_instance",
		"google_firebase_project",
		"google_logging_log_view",
		"google_logging_log_view_iam_member",
		"google_logging_project_bucket_config",
		"google_logging_project_exclusion",
		"google_logging_project_sink",
		"google_monitoring_alert_policy",
		"google_monitoring_notification_channel",
		"google_project_iam_custom_role",
		"google_project_iam_member",
	}
	if got := managedDroppedTypes(); !slices.Equal(got, wantDropped) {
		t.Errorf("the types the managed list drops are %v, want %v", got, wantDropped)
	}
}

// TestManagedAllowedTypesIsDecided is the completeness property: every type
// init's own cover allows is either kept by the managed list with a
// justification or left out with a reason, and nothing is on the managed list
// that no module declares. A type added to allowedTypes fails this test until
// it is decided here, so the managed list cannot be widened by silence.
func TestManagedAllowedTypesIsDecided(t *testing.T) {
	kept := map[string]bool{}
	for _, typ := range ManagedAllowedTypes() {
		kept[typ] = true
		if !allowedTypes[typ] {
			t.Errorf("the managed list keeps %s, which no module declares", typ)
		}
		if managedTypeReasons[typ] == "" {
			t.Errorf("%s is kept with no justification", typ)
		}
	}
	for typ, why := range managedDropped {
		if !allowedTypes[typ] {
			t.Errorf("the managed list drops %s, which is not a type the modules declare", typ)
		}
		if why == "" {
			t.Errorf("%s is dropped with no reason", typ)
		}
		if kept[typ] {
			t.Errorf("%s is both kept and dropped", typ)
		}
	}
	for typ := range allowedTypes {
		if !kept[typ] && managedDropped[typ] == "" {
			t.Errorf("%s is neither kept nor dropped: decide it in tf/managed_cover.go", typ)
		}
	}
	if got := ManagedAllowedTypes(); !slices.IsSorted(got) || len(got) != len(managedTypes) {
		t.Errorf("ManagedAllowedTypes is %v: not the list's own keys", got)
	}
}

// managedKeptFixture is one resource per type the list keeps: the type's own
// name carries the managed prefix, and the account it runs as, where it has
// one, is a managed account of the managed project. TestManagedCoverAdmits...
// fails when a kept type has none, so adding one to the list is adding a
// fixture beside it.
var managedKeptFixture = map[string]ResourceChange{
	"google_apikeys_key": mrc("google_apikeys_key", map[string]any{"name": ManagedPrefix + "web-key"}),
	"google_artifact_registry_repository": mrc("google_artifact_registry_repository", map[string]any{
		"repository_id": ManagedPrefix + "repo", "format": "DOCKER"}),
	"google_artifact_registry_repository_iam_member": mrc("google_artifact_registry_repository_iam_member", map[string]any{
		"repository": ManagedPrefix + "repo", "member": "serviceAccount:" + managedAccount, "role": "roles/artifactregistry.reader"}),
	"google_cloud_run_v2_job": mrc("google_cloud_run_v2_job", map[string]any{
		"name": ManagedPrefix + "job",
		"template": []any{map[string]any{"template": []any{map[string]any{
			"service_account": managedAccount,
			"containers":      []any{map[string]any{"image": "us-east5-docker.pkg.dev/proj-1/" + ManagedPrefix + "img:1"}},
		}}}},
	}),
	"google_cloud_run_v2_job_iam_member": mrc("google_cloud_run_v2_job_iam_member", map[string]any{
		"name": ManagedPrefix + "job", "member": "serviceAccount:" + managedAccount, "role": "roles/run.invoker"}),
	"google_cloud_scheduler_job": mrc("google_cloud_scheduler_job", map[string]any{
		"name": ManagedPrefix + "sweep",
		"http_target": []any{map[string]any{"uri": "https://run.googleapis.com/v2/projects/proj-1/locations/us-east5/jobs/" + ManagedPrefix + "job:run",
			"oauth_token": []any{map[string]any{"service_account_email": managedAccount}}}}},
	),
	// The service is enabled by name, not created under one: the modules write
	// it as its own type and there is nothing about it to prefix.
	"google_project_service": mrc("google_project_service", map[string]any{"service": "run.googleapis.com"}),
	"google_secret_manager_secret": mrc("google_secret_manager_secret", map[string]any{
		"secret_id": ManagedPrefix + "token"}),
	"google_secret_manager_secret_iam_member": mrc("google_secret_manager_secret_iam_member", map[string]any{
		"secret_id": ManagedPrefix + "token", "member": "serviceAccount:" + managedAccount, "role": "roles/secretmanager.secretAccessor"}),
	"google_service_account": mrc("google_service_account", map[string]any{"account_id": ManagedPrefix + "sa"}),
	"google_service_account_iam_member": mrc("google_service_account_iam_member", map[string]any{
		"service_account_id": "projects/proj-1/serviceAccounts/" + managedAccount,
		"member":             "serviceAccount:" + managedAccount, "role": "roles/iam.serviceAccountUser"}),
	"google_storage_bucket": mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "bucket"}),
	"google_storage_bucket_iam_member": mrc("google_storage_bucket_iam_member", map[string]any{
		"bucket": ManagedPrefix + "bucket", "member": "serviceAccount:" + managedAccount, "role": "roles/storage.objectViewer"}),
	"google_storage_bucket_object": mrc("google_storage_bucket_object", map[string]any{
		"bucket": ManagedPrefix + "bucket", "name": "results.json"}),
}

func TestManagedCoverAdmitsTheKeptTypes(t *testing.T) {
	for _, typ := range ManagedAllowedTypes() {
		t.Run(typ, func(t *testing.T) {
			fixture, ok := managedKeptFixture[typ]
			if !ok {
				t.Fatalf("%s is kept with no fixture: one belongs beside the entry", typ)
			}
			if got := managedTestCover().NotCovered(managedPlan(fixture), nil); len(got) != 0 {
				t.Fatalf("the %s fixture is not covered: %q", typ, got)
			}
		})
	}
}

// The same fixture with one of its names outside the prefix: one per entry,
// because the prefix rule is the entry's own condition in Task 1.1's table.
var managedOutsideFixture = map[string]ResourceChange{
	"google_apikeys_key": mrc("google_apikeys_key", map[string]any{"name": "fugaro-web"}),
	"google_artifact_registry_repository": mrc("google_artifact_registry_repository", map[string]any{
		"repository_id": "fugaro-base"}),
	"google_artifact_registry_repository_iam_member": mrc("google_artifact_registry_repository_iam_member", map[string]any{
		"repository": "fugaro-base", "member": "serviceAccount:" + managedAccount, "role": "roles/artifactregistry.reader"}),
	"google_cloud_run_v2_job": mrc("google_cloud_run_v2_job", map[string]any{
		"name":     "fugarohist",
		"template": []any{map[string]any{"template": []any{map[string]any{"service_account": managedAccount}}}},
	}),
	"google_cloud_run_v2_job_iam_member": mrc("google_cloud_run_v2_job_iam_member", map[string]any{
		"name": "fugarohist", "member": "serviceAccount:" + managedAccount, "role": "roles/run.invoker"}),
	"google_cloud_scheduler_job": mrc("google_cloud_scheduler_job", map[string]any{
		"name": "fugaro-history-sweep",
		"http_target": []any{map[string]any{
			"oauth_token": []any{map[string]any{"service_account_email": managedAccount}}}}},
	),
	"google_secret_manager_secret": mrc("google_secret_manager_secret", map[string]any{
		"secret_id": "fugaro-project"}),
	"google_secret_manager_secret_iam_member": mrc("google_secret_manager_secret_iam_member", map[string]any{
		"secret_id": "fugaro-project", "member": "serviceAccount:" + managedAccount, "role": "roles/secretmanager.viewer"}),
	"google_service_account": mrc("google_service_account", map[string]any{"account_id": "fugaro-history"}),
	"google_service_account_iam_member": mrc("google_service_account_iam_member", map[string]any{
		"service_account_id": "projects/proj-1/serviceAccounts/fugaro-scheduler@proj-1.iam.gserviceaccount.com",
		"member":             "serviceAccount:" + managedAccount, "role": "roles/iam.serviceAccountUser"}),
	"google_storage_bucket": mrc("google_storage_bucket", map[string]any{"name": "fugaro-runs-proj-1"}),
	"google_storage_bucket_iam_member": mrc("google_storage_bucket_iam_member", map[string]any{
		"bucket": "fugaro-other", "member": "serviceAccount:" + managedAccount, "role": "roles/storage.objectViewer"}),
	"google_storage_bucket_object": mrc("google_storage_bucket_object", map[string]any{
		"bucket": "fugaro-other", "name": "results.json"}),
}

func TestManagedCoverRefusesNamesOutsideThePrefix(t *testing.T) {
	for _, typ := range ManagedAllowedTypes() {
		if typ == "google_project_service" {
			continue // the service is enabled by name and carries no prefix
		}
		t.Run(typ, func(t *testing.T) {
			fixture, ok := managedOutsideFixture[typ]
			if !ok {
				t.Fatalf("%s has no outside-the-prefix fixture: one belongs beside the entry", typ)
			}
			out := managedTestCover().NotCovered(managedPlan(fixture), nil)
			if len(out) != 1 || !strings.HasPrefix(out[0], fixture.Address+" (") || !strings.Contains(out[0], ManagedPrefix) {
				t.Fatalf("a %s outside the prefix gives %q, want one refusal naming %s", typ, out, ManagedPrefix)
			}
		})
	}
}

// TestManagedCoverRefusesTheDroppedTypes is one fixture per entry the list
// deliberately excludes: each is refused for the type, whatever its
// attributes say, which is what makes the exclusion a decision.
func TestManagedCoverRefusesTheDroppedTypes(t *testing.T) {
	for _, typ := range managedDroppedTypes() {
		t.Run(typ, func(t *testing.T) {
			out := managedTestCover().NotCovered(managedPlan(mrc(typ, nil)), nil)
			if len(out) != 1 || !strings.HasPrefix(out[0], "module.managed."+typ+".this (") {
				t.Fatalf("%s gives %q, want it refused for its type", typ, out)
			}
		})
	}
	if len(managedDroppedTypes()) != len(allowedTypes)-len(ManagedAllowedTypes()) {
		t.Errorf("the managed list keeps %d and drops %d of %d types", len(ManagedAllowedTypes()), len(managedDroppedTypes()), len(allowedTypes))
	}
}

// TestManagedCoverRefusesProjectLevelIAM is rule (a) and rule (d): a managed
// plan binds no project-level IAM and creates no custom role, which is the
// gate's half of the deployer's "no project-level setIamPolicy, no
// iam.roles.*" (D75 (iii)).
func TestManagedCoverRefusesProjectLevelIAM(t *testing.T) {
	for name, fixture := range map[string]ResourceChange{
		"a project-level role grant": mrc("google_project_iam_member", map[string]any{
			"member": "serviceAccount:" + managedAccount, "role": "roles/run.invoker"}),
		"a custom role": mrc("google_project_iam_custom_role", map[string]any{
			"role_id": "fmgdRole", "permissions": []any{"storage.objects.create"}}),
		"an authoritative binding": mrc("google_project_iam_binding", map[string]any{
			"members": []any{"serviceAccount:" + managedAccount}, "role": "roles/run.invoker"}),
		"a service account key": mrc("google_service_account_key", map[string]any{
			"service_account_id": "projects/proj-1/serviceAccounts/" + managedAccount}),
	} {
		t.Run(name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(managedPlan(fixture), nil); len(out) != 1 {
				t.Fatalf("%s gives %q, want one refusal", name, out)
			}
		})
	}
}

// TestManagedCoverRefusesTheRunsBucketByName is rule (b): a bucket grant or
// an object on the installation's own runs bucket is refused by name, not
// merely by the prefix it happens not to carry. The runs bucket here is one
// the prefix rule admits, so the by-name rule is the only thing that can
// refuse it, and the refusal's reason is the by-name rule's own: a fixture
// named fugaro-runs-proj-1 would be refused by the prefix rule as well, and
// would stay red-proof with the by-name rule deleted.
func TestManagedCoverRefusesTheRunsBucketByName(t *testing.T) {
	runs := ManagedPrefix + "runs-proj-1"
	c := managedTestCover()
	c.Buckets = []string{runs}
	for name, fixture := range map[string]ResourceChange{
		"a bucket grant": mrc("google_storage_bucket_iam_member", map[string]any{
			"bucket": runs, "member": "serviceAccount:" + managedAccount, "role": "roles/storage.objectViewer"}),
		"an object": mrc("google_storage_bucket_object", map[string]any{
			"bucket": runs, "name": "fugaro/allow-delete.json"}),
	} {
		t.Run(name, func(t *testing.T) {
			if out := (ManagedCover{Cover: Cover{Projects: c.Projects, Listed: c.Listed}}).NotCovered(managedPlan(fixture), nil); len(out) != 0 {
				t.Fatalf("with no runs bucket named, %s on %s gives %q: the prefix rule refuses it, so this row cannot isolate the by-name rule", name, runs, out)
			}
			out := c.NotCovered(managedPlan(fixture), nil)
			if want := "on bucket " + runs + ", which is not the managed project's"; len(out) != 1 || !strings.Contains(out[0], want) {
				t.Fatalf("%s on the runs bucket gives %q, want one refusal %q", name, out, want)
			}
		})
	}
}

// TestManagedCoverRefusesTheBasicRoles: all three of the project's basic
// roles are refused on whatever resource they are written on, with the
// basic-role reason — roles/viewer included, which a member the review listed
// would otherwise be granted with no refusal at all. The list is a literal
// here and PrimitiveRoles is the single source the cover and the modules'
// own test read, so a role dropped from it is a diff in this test.
func TestManagedCoverRefusesTheBasicRoles(t *testing.T) {
	want := []string{"roles/editor", "roles/owner", "roles/viewer"}
	if got := slices.Sorted(slices.Values(PrimitiveRoles())); !slices.Equal(got, want) {
		t.Fatalf("PrimitiveRoles() = %v, want %v", got, want)
	}
	for _, role := range want {
		t.Run(role, func(t *testing.T) {
			fixture := mrc("google_storage_bucket_iam_member", map[string]any{
				"bucket": ManagedPrefix + "bucket", "member": "user:owner@example.com", "role": role})
			out := managedTestCover().NotCovered(managedPlan(fixture), nil)
			if len(out) != 1 || !strings.Contains(out[0], "basic role "+role) {
				t.Fatalf("a grant of %s gives %q, want one refusal naming it as a basic role", role, out)
			}
		})
	}
}

// inModule is rc at module path mod (empty for the root module): the
// computed-value rules trust a value only when the resource it may be
// computed from is created in the same module.
func inModule(rc ResourceChange, mod string) ResourceChange {
	rc.Address = rc.Type + ".this"
	if mod != "" {
		rc.Address = mod + "." + rc.Address
	}
	return rc
}

// withUnknown is rc with after_unknown set: the values the plan knows only
// after apply, which are absent from after.
func withUnknown(rc ResourceChange, unknown map[string]any) ResourceChange {
	rc.Change.AfterUnknown = unknown
	return rc
}

// TestManagedCoverComputedValues pins the rules for a value the plan knows
// only after apply, in both directions. A computed name or member is admitted
// only when the resource it may be computed from is created in the same
// module (that resource's own name is checked where it is declared), and is
// refused otherwise — the fail-open half, where a plan points at something it
// did not create. An account a job or a scheduler runs as is never trusted
// computed: refused with its own reason.
func TestManagedCoverComputedValues(t *testing.T) {
	const mod = "module.managed"
	sa := inModule(mrc("google_service_account", map[string]any{"account_id": ManagedPrefix + "sa"}), mod)
	bucket := inModule(mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "bucket"}), mod)
	saElsewhere := inModule(sa, "module.other")
	bucketElsewhere := inModule(bucket, "module.other")
	grantOnComputedBucket := inModule(withUnknown(mrc("google_storage_bucket_iam_member", map[string]any{
		"member": "serviceAccount:" + managedAccount, "role": "roles/storage.objectViewer"}), map[string]any{"bucket": true}), mod)
	grantToComputedMember := inModule(withUnknown(mrc("google_storage_bucket_iam_member", map[string]any{
		"bucket": ManagedPrefix + "bucket", "role": "roles/storage.objectViewer"}), map[string]any{"member": true}), mod)
	grantOnComputedAccount := inModule(withUnknown(mrc("google_service_account_iam_member", map[string]any{
		"member": "serviceAccount:" + managedAccount, "role": "roles/iam.serviceAccountUser"}), map[string]any{"service_account_id": true}), mod)
	jobComputedAccount := inModule(withUnknown(mrc("google_cloud_run_v2_job", map[string]any{
		"name": ManagedPrefix + "job", "template": []any{map[string]any{"template": []any{map[string]any{}}}}}),
		map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"service_account": true}}}}}), mod)
	schedComputedOAuth := inModule(withUnknown(mrc("google_cloud_scheduler_job", map[string]any{
		"name": ManagedPrefix + "sweep", "http_target": []any{map[string]any{"oauth_token": []any{map[string]any{}}}}}),
		map[string]any{"http_target": []any{map[string]any{"oauth_token": []any{map[string]any{"service_account_email": true}}}}}), mod)
	schedComputedOIDC := inModule(withUnknown(mrc("google_cloud_scheduler_job", map[string]any{
		"name": ManagedPrefix + "sweep", "http_target": []any{map[string]any{"oidc_token": []any{map[string]any{}}}}}),
		map[string]any{"http_target": []any{map[string]any{"oidc_token": []any{map[string]any{"service_account_email": true}}}}}), mod)
	schedComputedTarget := inModule(withUnknown(mrc("google_cloud_scheduler_job", map[string]any{
		"name": ManagedPrefix + "sweep"}), map[string]any{"http_target": true}), mod)

	for name, tc := range map[string]struct {
		plan []ResourceChange
		why  string // "" when the plan is covered
	}{
		// managedName's created-trusted path.
		"a grant on a bucket the module creates": {[]ResourceChange{bucket, grantOnComputedBucket}, ""},
		"a grant on a computed bucket, none created": {[]ResourceChange{grantOnComputedBucket},
			"google_storage_bucket_iam_member whose bucket is not known"},
		"a grant on a computed bucket created in another module": {[]ResourceChange{bucketElsewhere, grantOnComputedBucket},
			"google_storage_bucket_iam_member whose bucket is not known"},
		// managedGrant's created-trusted path.
		"a grant to an account the module creates": {[]ResourceChange{sa, grantToComputedMember}, ""},
		"a grant to a computed member, no account created": {[]ResourceChange{grantToComputedMember},
			"a member that is not known and no service account is created with it"},
		"a grant to a computed member, the account created in another module": {[]ResourceChange{saElsewhere, grantToComputedMember},
			"a member that is not known and no service account is created with it"},
		// The service-account grant's guard.
		"a grant on an account the module creates": {[]ResourceChange{sa, grantOnComputedAccount}, ""},
		"a grant on a computed account, none created": {[]ResourceChange{grantOnComputedAccount},
			"a grant on an account that is not known and none is created with it"},
		"a grant on a computed account created in another module": {[]ResourceChange{saElsewhere, grantOnComputedAccount},
			"a grant on an account that is not known and none is created with it"},
		// The job's and the scheduler's guards: never trusted, even with an
		// account created beside them.
		"a job whose account is computed":             {[]ResourceChange{sa, jobComputedAccount}, "a job whose service account is not known"},
		"a scheduler whose OAuth account is computed": {[]ResourceChange{sa, schedComputedOAuth}, "a scheduler whose service account is not known"},
		"a scheduler whose OIDC account is computed":  {[]ResourceChange{sa, schedComputedOIDC}, "a scheduler whose service account is not known"},
		"a scheduler whose whole target is computed":  {[]ResourceChange{sa, schedComputedTarget}, "a scheduler whose service account is not known"},
	} {
		t.Run(name, func(t *testing.T) {
			out := managedTestCover().NotCovered(managedPlan(tc.plan...), nil)
			if tc.why == "" {
				if len(out) != 0 {
					t.Fatalf("gives %q, want it covered", out)
				}
				return
			}
			if len(out) != 1 || !strings.Contains(out[0], tc.why) {
				t.Fatalf("gives %q, want one refusal %q", out, tc.why)
			}
		})
	}
}

// TestManagedCoverRefusesTheStateBucketByThePrefix is D77's fixture: the
// dedicated state bucket, fugaro-managed-tfstate-<gcp-project>, must not match
// the managed prefix — a condition admitting it would hand the deployer the
// state of the very root it applies.
func TestManagedCoverRefusesTheStateBucketByThePrefix(t *testing.T) {
	for name, fixture := range map[string]ResourceChange{
		"a bucket": mrc("google_storage_bucket", map[string]any{"name": "fugaro-managed-tfstate-proj-1"}),
		"a bucket grant": mrc("google_storage_bucket_iam_member", map[string]any{
			"bucket": "fugaro-managed-tfstate-proj-1", "member": "serviceAccount:" + managedAccount, "role": "roles/storage.objectAdmin"}),
		"an object": mrc("google_storage_bucket_object", map[string]any{
			"bucket": "fugaro-managed-tfstate-proj-1", "name": "fugaro/managed/default.tfstate"}),
	} {
		t.Run(name, func(t *testing.T) {
			out := managedTestCover().NotCovered(managedPlan(fixture), nil)
			if len(out) != 1 || !strings.Contains(out[0], ManagedPrefix) {
				t.Fatalf("the state bucket through %s gives %q, want one refusal naming %s", name, out, ManagedPrefix)
			}
		})
	}
}

// TestManagedPrefixDoesNotMatchFugarosOwnNames holds the constant itself: the
// prefix must not admit the runs bucket, the two state buckets, the fixed
// accounts, a per-job account, the installation's repositories or the web key.
func TestManagedPrefixDoesNotMatchFugarosOwnNames(t *testing.T) {
	for _, name := range []string{
		"fugaro-runs-proj-1",
		"fugaro-managed-tfstate-proj-1",
		"fugaro-tfstate-proj-1",
		"fugaro-scheduler", "fugaro-history", "fugaro-token-signer",
		"fugaro-b-acme-webapp-5b8bba58", "fugaro-web", "fugaro-base", "fugaro-ghcr",
		"proj-1-compute", "123456789-compute",
	} {
		if strings.HasPrefix(name, ManagedPrefix) {
			t.Errorf("the managed prefix %q admits Fugaro's own name %q", ManagedPrefix, name)
		}
	}
	if ManagedPrefix == "" || strings.HasPrefix("fugaro-managed-tfstate-proj-1", ManagedPrefix) {
		t.Errorf("the managed prefix %q is not usable", ManagedPrefix)
	}
}

// TestManagedCoverRefusesTheDefaultComputeAccount is rule (c)'s named case:
// the project's default compute account holds Editor by default, so a plan
// that runs a job as it, or grants IAM on it, is refused — while the same plan
// on a managed account is admitted.
func TestManagedCoverRefusesTheDefaultComputeAccount(t *testing.T) {
	const compute = "123456789-compute@developer.gserviceaccount.com"
	managed := ManagedPrefix + "run@proj-1.iam.gserviceaccount.com"
	for name, tc := range map[string]struct {
		typ, attr string
		account   string
	}{
		"a job running as it":                {"google_cloud_run_v2_job", "service_account", compute},
		"a job running as a managed account": {"google_cloud_run_v2_job", "service_account", managed},
		"a scheduler calling as it":          {"google_cloud_scheduler_job", "service_account_email", compute},
		"a scheduler calling as managed":     {"google_cloud_scheduler_job", "service_account_email", managed},
	} {
		t.Run(name, func(t *testing.T) {
			var fixture ResourceChange
			if tc.typ == "google_cloud_run_v2_job" {
				fixture = mrc(tc.typ, map[string]any{
					"name":     ManagedPrefix + "job",
					"template": []any{map[string]any{"template": []any{map[string]any{tc.attr: tc.account}}}},
				})
			} else {
				fixture = mrc(tc.typ, map[string]any{
					"name": ManagedPrefix + "sweep",
					"http_target": []any{map[string]any{
						"oauth_token": []any{map[string]any{tc.attr: tc.account}}}},
				})
			}
			out := managedTestCover().NotCovered(managedPlan(fixture), nil)
			wantRefused := tc.account == compute
			if wantRefused && (len(out) != 1 || !strings.Contains(out[0], compute)) {
				t.Fatalf("running as the default compute account gives %q, want one refusal naming it", out)
			}
			if !wantRefused && len(out) != 0 {
				t.Fatalf("running as a managed account gives %q, want it covered", out)
			}
		})
	}
}

// TestManagedCoverRefusesAnUnsetJobAccount is the other half of the same rule:
// a job with no service account runs as the default compute account, so it is
// refused rather than read as "nothing to check".
func TestManagedCoverRefusesAnUnsetJobAccount(t *testing.T) {
	for name, fixture := range map[string]ResourceChange{
		"a job with no account": mrc("google_cloud_run_v2_job", map[string]any{
			"name":     ManagedPrefix + "job",
			"template": []any{map[string]any{"template": []any{map[string]any{"containers": []any{}}}}},
		}),
		"a scheduler with no account": mrc("google_cloud_scheduler_job", map[string]any{
			"name":        ManagedPrefix + "sweep",
			"http_target": []any{map[string]any{}},
		}),
	} {
		t.Run(name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(managedPlan(fixture), nil); len(out) != 1 {
				t.Fatalf("%s gives %q, want one refusal", name, out)
			}
		})
	}
}

// TestManagedCoverRefusesWiderPrincipals: a managed plan grants a role to the
// members the review listed or to a managed account of the managed project —
// never to everyone, a whole domain, another project's account, a computed
// role, or someone the review did not show.
func TestManagedCoverRefusesWiderPrincipals(t *testing.T) {
	grant := func(member, role string) ResourceChange {
		return mrc("google_storage_bucket_iam_member", map[string]any{
			"bucket": ManagedPrefix + "bucket", "member": member, "role": role})
	}
	for name, fixture := range map[string]ResourceChange{
		"everyone":                    grant("allUsers", "roles/storage.objectViewer"),
		"everyone signed in":          grant("allAuthenticatedUsers", "roles/storage.objectViewer"),
		"a whole domain":              grant("domain:example.com", "roles/storage.objectViewer"),
		"another project's account":   grant("serviceAccount:"+ManagedPrefix+"sa@other.iam.gserviceaccount.com", "roles/storage.objectViewer"),
		"the installations scheduler": grant("serviceAccount:fugaro-scheduler@proj-1.iam.gserviceaccount.com", "roles/storage.objectViewer"),
		"a person not on the screen":  grant("user:stranger@example.com", "roles/storage.objectViewer"),
		"no member":                   grant("", "roles/storage.objectViewer"),
		"the project's owner role":    grant("user:owner@example.com", "roles/owner"),
		"the project's editor role":   grant("user:owner@example.com", "roles/editor"),
		"the project's viewer role":   grant("user:owner@example.com", "roles/viewer"),
	} {
		t.Run(name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(managedPlan(fixture), nil); len(out) != 1 {
				t.Fatalf("%s gives %q, want one refusal", name, out)
			}
		})
	}
	for name, member := range map[string]string{
		"a person the review listed":    "user:owner@example.com",
		"a group the review listed":     "group:ops@example.com",
		"a managed account":             "serviceAccount:" + managedAccount,
		"a managed account of the plan": "serviceAccount:" + ManagedPrefix + "other@proj-1.iam.gserviceaccount.com",
	} {
		t.Run(name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(managedPlan(grant(member, "roles/storage.objectViewer")), nil); len(out) != 0 {
				t.Fatalf("%s gives %q, want it covered", name, out)
			}
		})
	}
}

// TestManagedCoverRefusesAComputedRole: a managed plan creates no custom role
// (rule (d)), so a role it does not spell out could be one the deployer holds
// no iam.roles.* to set.
func TestManagedCoverRefusesAComputedRole(t *testing.T) {
	fixture := mrc("google_storage_bucket_iam_member", map[string]any{"bucket": ManagedPrefix + "bucket", "member": "user:owner@example.com"})
	fixture.Change.AfterUnknown = map[string]any{"role": true}
	if out := managedTestCover().NotCovered(managedPlan(fixture), nil); len(out) != 1 {
		t.Fatalf("a computed role gives %q, want one refusal", out)
	}
}

// TestManagedCoverIsACover: the managed cover satisfies the interface the
// managed apply's gate takes (internal/managed/plan.go), and a nil plan is one
// refusal rather than none.
func TestManagedCoverIsACover(t *testing.T) {
	var _ interface {
		NotCovered(*Plan, []ImportKey) []string
	} = ManagedCover{}
	var _ interface {
		NotCovered(*Plan, []ImportKey) []string
	} = managedTestCover()
	if got := managedTestCover().NotCovered(nil, nil); len(got) != 1 || got[0] != "no plan" {
		t.Fatalf("a nil plan gives %q, want one refusal", got)
	}
}
