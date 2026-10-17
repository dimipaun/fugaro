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
	return &Plan{FormatVersion: "1.2", ResourceChanges: changes, Configuration: &Configuration{
		ProviderConfig: map[string]ProviderConfig{"google": {Name: "google", FullName: "registry.terraform.io/hashicorp/google",
			Expressions: map[string]json.RawMessage{"project": json.RawMessage(`{"constant_value":"proj-1"}`)}}}}}
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
		"a project known only after apply": {managedTestCover(),
			withUnknown(bucket(""), map[string]any{"project": true})},
	} {
		t.Run("admits "+name, func(t *testing.T) {
			if out := tc.c.NotCovered(managedPlan(tc.rc), nil); len(out) != 0 {
				t.Fatalf("gives %q, want it covered", out)
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
