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

// mrc is one resource of a managed plan, under Google's provider: the address
// is the type's own, so a refusal's address says which fixture it came from.
func mrc(typ string, after map[string]any) ResourceChange {
	if after == nil {
		after = map[string]any{}
	}
	return ResourceChange{Address: "module.managed." + typ + ".this", Mode: "managed", Type: typ,
		ProviderName: "registry.terraform.io/hashicorp/google",
		Change:       Change{Actions: []string{"create"}, After: after, AfterUnknown: map[string]any{}}}
}

// managedPlan is a plan of changes with a configuration that declares Google's
// provider and nothing else: the clean configuration every fixture of the
// resource rules shares (managed_apply_test.go holds the configuration's own).
func managedPlan(changes ...ResourceChange) *Plan {
	return &Plan{FormatVersion: "1.2", ResourceChanges: changes, Configuration: &Configuration{
		ProviderConfig: map[string]ProviderConfig{"google": {Name: "google", FullName: "registry.terraform.io/hashicorp/google"}}}}
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
		"repository_id": ManagedPrefix + "repo", "format": "DOCKER", "mode": "STANDARD_REPOSITORY"}),
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

// managedInModule is rc at module path mod (empty for the root module): the
// computed-value rules trust a value only when the resource it may be
// computed from is created in the same module.
func managedInModule(rc ResourceChange, mod string) ResourceChange {
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

// TestManagedCoverComputedValues pins the rule for a value the plan knows only
// after apply: every name, account and member the cover reads is refused when
// it is computed, whatever else the plan creates beside it. A saved plan keeps
// only the references an expression uses, never the expression, so "computed
// from a resource the plan creates" cannot be proved from it: the attack rows
// below are each a value that is unknown at plan time yet whose result the plan
// chooses — member = "${substr(google_service_account.x.unique_id,0,0)}allUsers"
// reads as unknown, references a created account, and applies as allUsers.
// A refusal falls back to the confirmation path; a plan that names its own
// accounts and resources literally is covered.
func TestManagedCoverComputedValues(t *testing.T) {
	const mod = "module.managed"
	sa := managedInModule(mrc("google_service_account", map[string]any{"account_id": ManagedPrefix + "sa"}), mod)
	bucket := managedInModule(mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "bucket"}), mod)
	secret := managedInModule(mrc("google_secret_manager_secret", map[string]any{"secret_id": ManagedPrefix + "token"}), mod)
	repo := managedInModule(mrc("google_artifact_registry_repository", map[string]any{"repository_id": ManagedPrefix + "repo", "mode": "STANDARD_REPOSITORY"}), mod)
	job := managedInModule(managedKeptFixture["google_cloud_run_v2_job"], mod)
	everything := []ResourceChange{sa, bucket, secret, repo, job}
	// computed is the kept fixture of typ with attr known only after apply,
	// in the module that creates everything a computed value could name.
	computed := func(typ, attr string, set map[string]any) ResourceChange {
		after := map[string]any{}
		for k, v := range managedKeptFixture[typ].Change.After {
			after[k] = v
		}
		delete(after, attr)
		for k, v := range set {
			after[k] = v
		}
		return managedInModule(withUnknown(mrc(typ, after), map[string]any{attr: true}), mod)
	}
	const memberWhy = "a member that is not known at plan time"
	for name, tc := range map[string]struct {
		rc  ResourceChange
		why string
	}{
		// (1) the member: the review's attack, on every grant type.
		"allUsers as storage.admin through a computed member": {computed("google_storage_bucket_iam_member", "member",
			map[string]any{"role": "roles/storage.admin"}), memberWhy},
		"token creator to an outsider through a computed member": {computed("google_service_account_iam_member", "member",
			map[string]any{"role": "roles/iam.serviceAccountTokenCreator"}), memberWhy},
		"a secret grant to a computed member":     {computed("google_secret_manager_secret_iam_member", "member", nil), memberWhy},
		"a repository grant to a computed member": {computed("google_artifact_registry_repository_iam_member", "member", nil), memberWhy},
		"a job grant to a computed member":        {computed("google_cloud_run_v2_job_iam_member", "member", nil), memberWhy},
		// (2) the names: "${substr(google_storage_bucket.b.name,0,0)}fugaro-runs-proj-1"
		// reads as unknown and applies as the runs bucket.
		"the marker through a computed bucket": {computed("google_storage_bucket_object", "bucket",
			map[string]any{"name": "fugaro/managed.json"}), "google_storage_bucket_object whose bucket is not known at plan time"},
		"a grant on a computed bucket": {computed("google_storage_bucket_iam_member", "bucket", nil),
			"google_storage_bucket_iam_member whose bucket is not known at plan time"},
		"a grant on a computed secret": {computed("google_secret_manager_secret_iam_member", "secret_id", nil),
			"google_secret_manager_secret_iam_member whose secret_id is not known at plan time"},
		"a grant on a computed repository": {computed("google_artifact_registry_repository_iam_member", "repository", nil),
			"google_artifact_registry_repository_iam_member whose repository is not known at plan time"},
		"a grant on a computed job": {computed("google_cloud_run_v2_job_iam_member", "name", nil),
			"google_cloud_run_v2_job_iam_member whose name is not known at plan time"},
		"a grant on a computed account": {computed("google_service_account_iam_member", "service_account_id", nil),
			"a grant on an account that is not known at plan time"},
		// The resources' own names were never trusted computed.
		"a computed bucket name": {computed("google_storage_bucket", "name", nil), "google_storage_bucket whose name is not known at plan time"},
		"a computed account id":  {computed("google_service_account", "account_id", nil), "google_service_account whose account_id is not known at plan time"},
		"a computed secret id":   {computed("google_secret_manager_secret", "secret_id", nil), "google_secret_manager_secret whose secret_id is not known at plan time"},
		"a computed job name":    {computed("google_cloud_run_v2_job", "name", nil), "google_cloud_run_v2_job whose name is not known at plan time"},
		"a computed key name":    {computed("google_apikeys_key", "name", nil), "google_apikeys_key whose name is not known at plan time"},
		"a computed repository":  {computed("google_artifact_registry_repository", "repository_id", nil), "google_artifact_registry_repository whose repository_id is not known at plan time"},
		"a computed schedule":    {computed("google_cloud_scheduler_job", "name", nil), "google_cloud_scheduler_job whose name is not known at plan time"},
		"a computed role":        {computed("google_storage_bucket_iam_member", "role", nil), "a role that is not known"},
	} {
		t.Run(name, func(t *testing.T) {
			out := managedTestCover().NotCovered(managedPlan(append(slices.Clone(everything), tc.rc)...), nil)
			if len(out) != 1 || !strings.HasPrefix(out[0], tc.rc.Address+" (") || !strings.Contains(out[0], tc.why) {
				t.Fatalf("gives %q, want one refusal of %s: %q", out, tc.rc.Address, tc.why)
			}
		})
	}
	// The refusal says what to write instead: the account spelled out.
	out := managedTestCover().NotCovered(managedPlan(sa, computed("google_storage_bucket_iam_member", "member", nil)), nil)
	if len(out) != 1 || !strings.Contains(out[0], "serviceAccount:"+ManagedPrefix) {
		t.Errorf("a computed member gives %q, want a refusal naming the literal form", out)
	}
	// A grant to an account the module creates, written with its member, its
	// account and its bucket spelled out, is covered.
	literal := []ResourceChange{sa, bucket,
		managedInModule(managedKeptFixture["google_storage_bucket_iam_member"], mod),
		managedInModule(managedKeptFixture["google_service_account_iam_member"], mod)}
	if out := managedTestCover().NotCovered(managedPlan(literal...), nil); len(out) != 0 {
		t.Errorf("a grant to a created account, spelled out, gives %q, want it covered", out)
	}

	// The job's and the scheduler's accounts: never trusted computed either.
	jobComputedAccount := managedInModule(withUnknown(mrc("google_cloud_run_v2_job", map[string]any{
		"name": ManagedPrefix + "job", "template": []any{map[string]any{"template": []any{map[string]any{}}}}}),
		map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"service_account": true}}}}}), mod)
	schedComputedOAuth := managedInModule(withUnknown(mrc("google_cloud_scheduler_job", map[string]any{
		"name": ManagedPrefix + "sweep", "http_target": []any{map[string]any{"oauth_token": []any{map[string]any{}}}}}),
		map[string]any{"http_target": []any{map[string]any{"oauth_token": []any{map[string]any{"service_account_email": true}}}}}), mod)
	schedComputedOIDC := managedInModule(withUnknown(mrc("google_cloud_scheduler_job", map[string]any{
		"name": ManagedPrefix + "sweep", "http_target": []any{map[string]any{"oidc_token": []any{map[string]any{}}}}}),
		map[string]any{"http_target": []any{map[string]any{"oidc_token": []any{map[string]any{"service_account_email": true}}}}}), mod)
	schedComputedTarget := managedInModule(withUnknown(mrc("google_cloud_scheduler_job", map[string]any{
		"name": ManagedPrefix + "sweep"}), map[string]any{"http_target": true}), mod)
	for name, tc := range map[string]struct {
		rc  ResourceChange
		why string
	}{
		"a job whose account is computed":             {jobComputedAccount, "a job whose service account is not known"},
		"a scheduler whose OAuth account is computed": {schedComputedOAuth, "a scheduler whose service account is not known"},
		"a scheduler whose OIDC account is computed":  {schedComputedOIDC, "a scheduler whose service account is not known"},
		"a scheduler whose whole target is computed":  {schedComputedTarget, "a scheduler whose service account is not known"},
	} {
		t.Run(name, func(t *testing.T) {
			out := managedTestCover().NotCovered(managedPlan(sa, tc.rc), nil)
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

// TestManagedCoverRefusesDeletesAndReplacements: the action set is create,
// update, no-op and read. A delete or a replacement is refused here whatever
// the owner's delete list says (the open question the PR records), and an
// unknown action is refused rather than read as none.
func TestManagedCoverRefusesDeletesAndReplacements(t *testing.T) {
	for name, acts := range map[string][]string{
		"a delete":                         {"delete"},
		"a replacement, delete first":      {"delete", "create"},
		"a replacement, create first":      {"create", "delete"},
		"an action terraform has not used": {"forget"},
		"no actions":                       {},
	} {
		t.Run(name, func(t *testing.T) {
			rc := managedKeptFixture["google_storage_bucket"]
			rc.Change.Actions = acts
			if out := managedTestCover().NotCovered(managedPlan(rc), nil); len(out) != 1 || !strings.HasPrefix(out[0], rc.Address+" (") {
				t.Fatalf("%v gives %q, want one refusal", acts, out)
			}
		})
	}
}

// TestManagedCoverImports: an import is covered only when its ID is in the
// managed project's projects AND this run's discovery found it at that
// address — and a no-op that imports is an import, not a no-op to skip.
func TestManagedCoverImports(t *testing.T) {
	sa := managedKeptFixture["google_service_account"]
	const inProject = "projects/proj-1/serviceAccounts/" + managedAccount
	const elsewhere = "projects/other/serviceAccounts/" + ManagedPrefix + "sa@other.iam.gserviceaccount.com"
	importing := func(id string, acts ...string) ResourceChange {
		rc := sa
		rc.Change.Actions = acts
		rc.Change.Importing = &Importing{ID: id}
		return rc
	}
	for name, tc := range map[string]struct {
		rc      ResourceChange
		imports []ImportKey
		why     string
	}{
		"an import discovery found": {importing(inProject, "no-op"), []ImportKey{{sa.Address, inProject}}, ""},
		"an import of another project's account, even one discovery listed": {importing(elsewhere, "no-op"),
			[]ImportKey{{sa.Address, elsewhere}}, "which is not in the managed project's projects"},
		"a no-op import discovery did not find":     {importing(inProject, "no-op"), nil, "which this run's discovery did not find"},
		"an updating import discovery did not find": {importing(inProject, "update"), nil, "which this run's discovery did not find"},
		"an import discovery found at another address": {importing(inProject, "no-op"),
			[]ImportKey{{"module.other.google_service_account.this", inProject}}, "which this run's discovery did not find"},
	} {
		t.Run(name, func(t *testing.T) {
			out := managedTestCover().NotCovered(managedPlan(tc.rc), tc.imports)
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

// TestManagedCoverListedMembersArePeople: the review's listed members admit a
// person or a group, never an account — a service account on the review
// screen is still not one the managed project owns, and only a managed account
// is granted to as an account.
func TestManagedCoverListedMembersArePeople(t *testing.T) {
	const outsider = "serviceAccount:ci@other.iam.gserviceaccount.com"
	c := managedTestCover()
	c.Listed = append(slices.Clone(c.Listed), outsider)
	rc := mrc("google_storage_bucket_iam_member", map[string]any{
		"bucket": ManagedPrefix + "bucket", "member": outsider, "role": "roles/storage.objectViewer"})
	if out := c.NotCovered(managedPlan(rc), nil); len(out) != 1 || !strings.Contains(out[0], "which is not a user or a group") {
		t.Fatalf("a listed service account gives %q, want one refusal: not a user or a group", out)
	}
}

// TestManagedCoverSchedulerOIDC: a scheduler may call with an OIDC token as
// well as an OAuth one, and the OIDC account is held to the same rule.
func TestManagedCoverSchedulerOIDC(t *testing.T) {
	sched := func(tokens map[string]string) ResourceChange {
		ht := map[string]any{}
		for tok, email := range tokens {
			ht[tok] = []any{map[string]any{"service_account_email": email}}
		}
		return mrc("google_cloud_scheduler_job", map[string]any{"name": ManagedPrefix + "sweep", "http_target": []any{ht}})
	}
	const compute = "123456789-compute@developer.gserviceaccount.com"
	if out := managedTestCover().NotCovered(managedPlan(sched(map[string]string{"oidc_token": managedAccount})), nil); len(out) != 0 {
		t.Errorf("a scheduler calling with an OIDC token as a managed account gives %q, want it covered", out)
	}
	for name, tokens := range map[string]map[string]string{
		"OIDC as the default compute account":             {"oidc_token": compute},
		"OAuth managed, OIDC the default compute account": {"oauth_token": managedAccount, "oidc_token": compute},
	} {
		t.Run(name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(managedPlan(sched(tokens)), nil); len(out) != 1 || !strings.Contains(out[0], compute) {
				t.Fatalf("gives %q, want one refusal naming %s", out, compute)
			}
		})
	}
}

// TestManagedCoverRefusesAVirtualOrRemoteRepository: a repository carrying the
// prefix can still be a virtual one whose upstream is Fugaro's own fugaro-base,
// or a remote one whose upstream password is one of Fugaro's secrets, so the
// prefix alone does not keep it the managed project's. A managed repository is
// a standard one (the provider's default, which the plan shows when unset);
// what else a repository points at is Task 1.2's.
func TestManagedCoverRefusesAVirtualOrRemoteRepository(t *testing.T) {
	repo := func(after map[string]any) ResourceChange {
		after["repository_id"], after["format"] = ManagedPrefix+"repo", "DOCKER"
		return mrc("google_artifact_registry_repository", after)
	}
	for name, rc := range map[string]ResourceChange{
		"virtual":  repo(map[string]any{"mode": "VIRTUAL_REPOSITORY"}),
		"remote":   repo(map[string]any{"mode": "REMOTE_REPOSITORY"}),
		"no mode":  repo(map[string]any{}),
		"computed": withUnknown(repo(map[string]any{}), map[string]any{"mode": true}),
	} {
		t.Run(name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(managedPlan(rc), nil); len(out) != 1 || !refusedWith(out, rc.Address+" (", "STANDARD_REPOSITORY") {
				t.Fatalf("gives %q, want one refusal naming STANDARD_REPOSITORY", out)
			}
		})
	}
	if out := managedTestCover().NotCovered(managedPlan(repo(map[string]any{"mode": "STANDARD_REPOSITORY"})), nil); len(out) != 0 {
		t.Errorf("a standard repository gives %q, want it covered", out)
	}
}

// updating is rc as an in-place update: the same checks hold for an update as
// for a create, because an update's after is what the resource becomes.
func updating(rc ResourceChange) ResourceChange {
	rc.Change.Actions = []string{"update"}
	rc.Change.Before = map[string]any{}
	return rc
}

// TestManagedCoverChecksUpdates: an update is held to every rule a create is —
// one fixture per rule family, each refused, beside an update the list admits.
// Only a no-op or a read skips the checks; a cover that skipped an update too
// would admit each row below.
func TestManagedCoverChecksUpdates(t *testing.T) {
	const runs = "fugaro-runs-proj-1"
	grant := func(bucket, member, role string) ResourceChange {
		return mrc("google_storage_bucket_iam_member", map[string]any{"bucket": bucket, "member": member, "role": role})
	}
	computedMember := mrc("google_storage_bucket_iam_member", map[string]any{"bucket": ManagedPrefix + "bucket", "role": "roles/storage.objectViewer"})
	computedMember.Change.AfterUnknown = map[string]any{"member": true}
	for name, tc := range map[string]struct {
		rc  ResourceChange
		why string
	}{
		"a type outside the allowlist": {mrc("google_compute_instance", map[string]any{"name": ManagedPrefix + "vm"}),
			"a resource type outside the managed project's allowlist"},
		"the runs bucket by its name": {mrc("google_storage_bucket", map[string]any{"name": runs}),
			"outside the managed prefix"},
		"allUsers on the runs bucket": {grant(runs, "allUsers", "roles/storage.objectViewer"),
			"on bucket " + runs + ", which is not the managed project's"},
		"allUsers on a managed bucket": {grant(ManagedPrefix+"bucket", "allUsers", "roles/storage.objectViewer"),
			"grants to allUsers, which is everyone"},
		"a computed member": {computedMember, "a member that is not known at plan time"},
		"a basic role":      {grant(ManagedPrefix+"bucket", "user:owner@example.com", "roles/owner"), "basic role roles/owner"},
		"a retention policy locked by the update": {mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "bucket",
			"retention_policy": []any{map[string]any{"is_locked": true, "retention_period": float64(3600)}}}), "retention policy is locked"},
	} {
		t.Run(name, func(t *testing.T) {
			rc := updating(tc.rc)
			out := managedTestCover().NotCovered(managedPlan(rc), nil)
			if len(out) != 1 || !refusedWith(out, rc.Address+" (", tc.why) {
				t.Fatalf("an update gives %q, want one refusal %q", out, tc.why)
			}
		})
	}
	for _, typ := range ManagedAllowedTypes() {
		t.Run("admitted "+typ, func(t *testing.T) {
			if out := managedTestCover().NotCovered(managedPlan(updating(managedKeptFixture[typ])), nil); len(out) != 0 {
				t.Fatalf("an update of the %s fixture gives %q, want it covered", typ, out)
			}
		})
	}
}

// TestManagedCoverRefusesLocks: a locked retention policy on a bucket, or an
// object retained in mode Locked, can never be undone — the bucket cannot be
// deleted until the retention runs out and a locked bucket can hold a lien on
// the project — so a managed plan locks nothing. The fixtures are the plan
// JSON's own shape: retention_policy and retention are lists of one block,
// and after_unknown mirrors them. An unlocked policy, an Unlocked retention
// and no retention at all are admitted.
func TestManagedCoverRefusesLocks(t *testing.T) {
	bucket := func(policy map[string]any, unknown map[string]any) ResourceChange {
		after := map[string]any{"name": ManagedPrefix + "bucket"}
		if policy != nil {
			after["retention_policy"] = []any{policy}
		}
		return withUnknown(mrc("google_storage_bucket", after), unknown)
	}
	object := func(retention map[string]any, unknown map[string]any) ResourceChange {
		after := map[string]any{"bucket": ManagedPrefix + "bucket", "name": "results.json", "content": "{}"}
		if retention != nil {
			after["retention"] = []any{retention}
		}
		return withUnknown(mrc("google_storage_bucket_object", after), unknown)
	}
	const until = "2066-01-01T00:00:00Z"
	for name, tc := range map[string]struct {
		rc  ResourceChange
		why string
	}{
		"a locked retention policy": {bucket(map[string]any{"is_locked": true, "retention_period": float64(3600)}, map[string]any{}),
			"retention policy is locked"},
		"a computed lock": {bucket(map[string]any{"retention_period": float64(3600)},
			map[string]any{"retention_policy": []any{map[string]any{"is_locked": true}}}), "retention policy lock is not known"},
		"a computed retention policy": {bucket(nil, map[string]any{"retention_policy": true}), "retention policy lock is not known"},
		"a computed policy element": {bucket(map[string]any{}, map[string]any{"retention_policy": []any{true}}),
			"retention policy lock is not known"},
		"an object retained Locked": {object(map[string]any{"mode": "Locked", "retain_until_time": until}, map[string]any{}),
			"retention mode is Locked"},
		"an object retained LOCKED": {object(map[string]any{"mode": "LOCKED", "retain_until_time": until}, map[string]any{}),
			"retention mode is Locked"},
		"an object retained locked": {object(map[string]any{"mode": "locked", "retain_until_time": until}, map[string]any{}),
			"retention mode is Locked"},
		"a computed retention mode": {object(map[string]any{"retain_until_time": until},
			map[string]any{"retention": []any{map[string]any{"mode": true}}}), "retention mode is not known"},
		"a computed retention": {object(nil, map[string]any{"retention": true}), "retention mode is not known"},
	} {
		t.Run(name, func(t *testing.T) {
			out := managedTestCover().NotCovered(managedPlan(tc.rc), nil)
			if len(out) != 1 || !refusedWith(out, tc.rc.Address+" (", tc.why) {
				t.Fatalf("gives %q, want one refusal %q", out, tc.why)
			}
		})
	}
	for name, rc := range map[string]ResourceChange{
		"an unlocked retention policy": bucket(map[string]any{"is_locked": false, "retention_period": float64(3600)}, map[string]any{}),
		"a policy with no lock set":    bucket(map[string]any{"retention_period": float64(3600)}, map[string]any{}),
		"no retention policy":          bucket(nil, map[string]any{}),
		"an object retained Unlocked":  object(map[string]any{"mode": "Unlocked", "retain_until_time": until}, map[string]any{}),
		"an object with no retention":  object(nil, map[string]any{}),
	} {
		t.Run(name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(managedPlan(rc), nil); len(out) != 0 {
				t.Fatalf("gives %q, want it covered", out)
			}
		})
	}
}
