package tf

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func rc(typ string, after map[string]any, unk map[string]any, acts ...string) ResourceChange {
	return ResourceChange{Address: "module.m." + typ + ".x", Type: typ, Change: Change{Actions: acts, After: after, AfterUnknown: unk}}
}

func grant(typ, member, role string) ResourceChange {
	return rc(typ, map[string]any{"member": member, "role": role}, map[string]any{}, "create")
}

// grantOnAccount is grant with the account the role is granted on: what
// deployerGrant reads for the token creator role, and what a plan that leaves
// it out does not say.
func grantOnAccount(typ, member, role, account string) ResourceChange {
	return rc(typ, map[string]any{"member": member, "role": role, "service_account_id": account}, map[string]any{}, "create")
}

// deployerAccountName is the deployer account of a run's project in the form a
// plan names it (the resource's own name attribute), which is the form the
// module writes as a literal.
func deployerAccountName(project string) string {
	return "projects/" + project + "/serviceAccounts/" + DeployerAccount(project)
}

var testCover = Cover{Projects: []string{"proj-1", "fp-2"}, Listed: []string{"user:me@example.com", "group:ops@example.com", "domain:example.com", "allUsers"},
	Registry: "us-east5-docker.pkg.dev", Region: "us-east5", Buckets: []string{"fugaro-runs-proj-1"}, Billing: []string{"AAAA-BBBB"},
	Ghcr: "fugaro-ghcr", Owner: "dimipaun"}

func bucket(after map[string]any) ResourceChange {
	m := map[string]any{"public_access_prevention": "enforced", "uniform_bucket_level_access": true}
	for k, v := range after {
		m[k] = v
	}
	return rc("google_storage_bucket", m, map[string]any{}, "create")
}

func job(image string, cmd, args []any) ResourceChange {
	c := map[string]any{"image": image}
	if cmd != nil {
		c["command"], c["args"] = cmd, args
	}
	return rc("google_cloud_run_v2_job", map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"containers": []any{c}}}}}}, map[string]any{}, "create")
}

func sched(uri, email string) ResourceChange {
	return rc("google_cloud_scheduler_job", map[string]any{"http_target": []any{map[string]any{"uri": uri, "oauth_token": []any{map[string]any{"service_account_email": email}}}}}, map[string]any{}, "create")
}

func customRole(id, project string, perms []any) ResourceChange {
	a := map[string]any{"role_id": id, "permissions": perms}
	if project != "" {
		a["project"] = project
	}
	return rc("google_project_iam_custom_role", a, map[string]any{}, "create")
}

func updRole(id string, before, after []any) ResourceChange {
	return ResourceChange{Address: "r", Type: "google_project_iam_custom_role", Change: Change{Actions: []string{"update"},
		Before:       map[string]any{"role_id": id, "project": "proj-1", "permissions": before},
		After:        map[string]any{"role_id": id, "project": "proj-1", "permissions": after},
		AfterUnknown: map[string]any{}}}
}

// unkRole is a role whose attribute attr is known only after apply.
func unkRole(attr string) ResourceChange {
	a := map[string]any{"role_id": "fugaroJobRunner", "project": "proj-1", "permissions": []any{"run.jobs.run", "run.jobs.runWithOverrides"}}
	delete(a, attr)
	return rc("google_project_iam_custom_role", a, map[string]any{attr: true}, "create")
}

// jobUnknown is a job in this run's registry whose container attribute attr
// is known only after apply (cmd and args are what is known of the others).
func jobUnknown(cmd, args []any, attr string) ResourceChange {
	c := map[string]any{"image": "us-east5-docker.pkg.dev/proj-1/fugaro-base/x:1"}
	if cmd != nil {
		c["command"], c["args"] = cmd, args
	}
	delete(c, attr)
	unk := map[string]any{attr: true}
	return rc("google_cloud_run_v2_job", map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"containers": []any{c}}}}}},
		map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"containers": []any{unk}}}}}}, "create")
}

const okURI = "https://run.googleapis.com/v2/projects/proj-1/locations/us-east5/jobs/fugarohist:run"

func TestCoverNotCovered(t *testing.T) {
	sa := rc("google_service_account", map[string]any{}, map[string]any{}, "create")
	role := customRole("fugaroJobRunner", "proj-1", []any{"run.jobs.run", "run.jobs.runWithOverrides"})
	for name, tc := range map[string]struct {
		changes []ResourceChange
		covered bool
	}{
		"additive known plan": {[]ResourceChange{sa, role, bucket(nil), job("us-east5-docker.pkg.dev/proj-1/fugaro-base/history:latest", []any{"/usr/local/bin/fugaro"}, []any{"budget", "history", "--sweep"}), sched(okURI, "fugaro-scheduler@proj-1.iam.gserviceaccount.com"),
			rc("google_logging_project_sink", map[string]any{"destination": "logging.googleapis.com/projects/proj-1/locations/global/buckets/fugaro"}, map[string]any{}, "create"),
			grant("google_project_iam_member", "user:me@example.com", "roles/datastore.viewer"),
			grant("google_project_iam_member", "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com", "roles/datastore.user"),
			grant("google_project_iam_member", "serviceAccount:fugaro-b-acme-0123abcd@proj-1.iam.gserviceaccount.com", "roles/logging.logWriter"),
			grant("google_project_iam_member", "group:ops@example.com", "projects/proj-1/roles/fugaroLauncher"),
			grant("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountUser"),
			rc("google_project_service", nil, nil, "no-op")}, true},
		"member computed from a service account created here": {[]ResourceChange{sa,
			rc("google_project_iam_member", map[string]any{"role": "roles/logging.logWriter"}, map[string]any{"member": true}, "create")}, true},
		"role computed from a custom role created here": {[]ResourceChange{role,
			rc("google_project_iam_member", map[string]any{"member": "user:me@example.com"}, map[string]any{"role": true}, "create")}, true},
		"nil plan":                               {nil, false},
		"foreign image":                          {[]ResourceChange{job("evil.example/x/y:1", nil, nil)}, false},
		"image of another project":               {[]ResourceChange{job("us-east5-docker.pkg.dev/other/fugaro-base/x:1", nil, nil)}, false},
		"foreign command":                        {[]ResourceChange{job("us-east5-docker.pkg.dev/proj-1/r/x:1", []any{"sh"}, []any{"-c", "x"})}, false},
		"workflow job, no command":               {[]ResourceChange{job("us-east5-docker.pkg.dev/proj-1/fugaro-acme-0123456789ab/x:1", nil, nil)}, true},
		"scheduler to another host":              {[]ResourceChange{sched("https://evil.example/v2/projects/proj-1/locations/us-east5/jobs/j:run", "fugaro-scheduler@proj-1.iam.gserviceaccount.com")}, false},
		"scheduler to another project":           {[]ResourceChange{sched("https://run.googleapis.com/v2/projects/other/locations/us-east5/jobs/j:run", "fugaro-scheduler@proj-1.iam.gserviceaccount.com")}, false},
		"scheduler as a stranger":                {[]ResourceChange{sched(okURI, "evil@proj-1.iam.gserviceaccount.com")}, false},
		"scheduler as another project's account": {[]ResourceChange{sched(okURI, "fugaro-scheduler@other.iam.gserviceaccount.com")}, false},
		"scheduler uri unknown, job created": {[]ResourceChange{job("us-east5-docker.pkg.dev/proj-1/r/x:1", nil, nil),
			rc("google_cloud_scheduler_job", map[string]any{"http_target": []any{map[string]any{"oauth_token": []any{map[string]any{"service_account_email": "fugaro-scheduler@proj-1.iam.gserviceaccount.com"}}}}},
				map[string]any{"http_target": []any{map[string]any{"uri": true}}}, "create")}, true},
		"scheduler uri unknown, no job": {[]ResourceChange{rc("google_cloud_scheduler_job", map[string]any{"http_target": []any{map[string]any{"oauth_token": []any{map[string]any{"service_account_email": "fugaro-scheduler@proj-1.iam.gserviceaccount.com"}}}}},
			map[string]any{"http_target": []any{map[string]any{"uri": true}}}, "create")}, false},
		"sink to another project":         {[]ResourceChange{rc("google_logging_project_sink", map[string]any{"destination": "logging.googleapis.com/projects/other/locations/global/buckets/x"}, map[string]any{}, "create")}, false},
		"sink to a bucket elsewhere":      {[]ResourceChange{rc("google_logging_project_sink", map[string]any{"destination": "storage.googleapis.com/evil"}, map[string]any{}, "create")}, false},
		"public bucket":                   {[]ResourceChange{bucket(map[string]any{"public_access_prevention": "inherited"})}, false},
		"bucket without uniform access":   {[]ResourceChange{bucket(map[string]any{"uniform_bucket_level_access": false})}, false},
		"bucket attributes missing":       {[]ResourceChange{rc("google_storage_bucket", nil, nil, "create")}, false},
		"resource in another project":     {[]ResourceChange{rc("google_secret_manager_secret", map[string]any{"project": "other"}, map[string]any{}, "create")}, false},
		"grant on another bucket":         {[]ResourceChange{rc("google_storage_bucket_iam_member", map[string]any{"bucket": "evil-bucket", "member": "user:me@example.com", "role": "roles/storage.objectAdmin"}, map[string]any{}, "create")}, false},
		"grant on the runs bucket":        {[]ResourceChange{rc("google_storage_bucket_iam_member", map[string]any{"bucket": "fugaro-runs-proj-1", "member": "user:me@example.com", "role": "roles/storage.objectAdmin"}, map[string]any{}, "create")}, true},
		"grant on a foreign registry":     {[]ResourceChange{rc("google_artifact_registry_repository_iam_member", map[string]any{"repository": "prod-images", "member": "user:me@example.com", "role": "roles/artifactregistry.reader"}, map[string]any{}, "create")}, false},
		"billing budget not shown":        {[]ResourceChange{rc("google_billing_budget", map[string]any{"billing_account": "billingAccounts/ZZZZ-ZZZZ"}, map[string]any{}, "create")}, false},
		"billing budget shown":            {[]ResourceChange{rc("google_billing_budget", map[string]any{"billing_account": "billingAccounts/AAAA-BBBB"}, map[string]any{}, "create")}, true},
		"api key with a wide target":      {[]ResourceChange{rc("google_apikeys_key", map[string]any{"restrictions": []any{map[string]any{"api_targets": []any{map[string]any{"service": "identitytoolkit.googleapis.com"}, map[string]any{"service": "storage.googleapis.com"}}}}}, map[string]any{}, "create")}, false},
		"api key as the module writes it": {[]ResourceChange{rc("google_apikeys_key", map[string]any{"restrictions": []any{map[string]any{"api_targets": []any{map[string]any{"service": "securetoken.googleapis.com"}, map[string]any{"service": "identitytoolkit.googleapis.com"}}}}}, map[string]any{}, "create")}, true},
		"import of another project":       {[]ResourceChange{{Address: "a.google_storage_bucket.x", Type: "google_storage_bucket", Change: Change{Actions: []string{"no-op"}, Importing: &Importing{ID: "other/fugaro-runs"}, After: map[string]any{"public_access_prevention": "enforced", "uniform_bucket_level_access": true}}}}, false},
		"import of this project": {[]ResourceChange{{Address: "a.google_storage_bucket.x", Type: "google_storage_bucket", Change: Change{Actions: []string{"update"}, Importing: &Importing{ID: "proj-1/fugaro-runs-proj-1"},
			After: map[string]any{"public_access_prevention": "enforced", "uniform_bucket_level_access": true}}}}, true},
		"domain member, even listed":            {[]ResourceChange{grant("google_project_iam_member", "domain:example.com", "roles/datastore.viewer")}, false},
		"allUsers, even listed":                 {[]ResourceChange{grant("google_project_iam_member", "allUsers", "roles/datastore.viewer")}, false},
		"a custom role of the firebase project": {[]ResourceChange{grant("google_service_account_iam_member", "user:me@example.com", "projects/fp-2/roles/fugaroTokenMinter")}, true},
		"a custom role by a made-up name":       {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "projects/proj-1/roles/fugaroEverything")}, false},
		"a made-up fugaro account":              {[]ResourceChange{grant("google_project_iam_member", "serviceAccount:fugaro-evil@proj-1.iam.gserviceaccount.com", "roles/logging.logWriter")}, false},
		"computed member from another module's account": {[]ResourceChange{
			{Address: "module.a.google_service_account.s", Type: "google_service_account", Change: Change{Actions: []string{"create"}, After: map[string]any{}, AfterUnknown: map[string]any{}}},
			{Address: "module.b.google_project_iam_member.m", Type: "google_project_iam_member", Change: Change{Actions: []string{"create"}, After: map[string]any{"role": "roles/logging.logWriter"}, AfterUnknown: map[string]any{"member": true}}}}, false},
		"computed member from the same module's account": {[]ResourceChange{
			{Address: "module.a.google_service_account.s", Type: "google_service_account", Change: Change{Actions: []string{"create"}, After: map[string]any{}, AfterUnknown: map[string]any{}}},
			{Address: "module.a.google_project_iam_member.m", Type: "google_project_iam_member", Change: Change{Actions: []string{"create"}, After: map[string]any{"role": "roles/logging.logWriter"}, AfterUnknown: map[string]any{"member": true}}}}, true},
		"computed role from another module's custom role": {[]ResourceChange{
			{Address: "module.a.google_project_iam_custom_role.r", Type: "google_project_iam_custom_role", Change: Change{Actions: []string{"create"}, After: map[string]any{}, AfterUnknown: map[string]any{}}},
			{Address: "module.b.google_project_iam_member.m", Type: "google_project_iam_member", Change: Change{Actions: []string{"create"}, After: map[string]any{"member": "user:me@example.com"}, AfterUnknown: map[string]any{"role": true}}}}, false},
		"delete":                    {[]ResourceChange{rc("google_storage_bucket", nil, nil, "delete")}, false},
		"replace":                   {[]ResourceChange{rc("google_storage_bucket", nil, nil, "delete", "create")}, false},
		"forget":                    {[]ResourceChange{rc("google_storage_bucket", nil, nil, "forget")}, false},
		"no actions":                {[]ResourceChange{rc("google_storage_bucket", nil, nil)}, false},
		"iam_binding create":        {[]ResourceChange{rc("google_project_iam_binding", map[string]any{"role": "roles/storage.objectUser", "members": []any{"user:me@example.com"}}, nil, "create")}, false},
		"iam_policy":                {[]ResourceChange{rc("google_project_iam_policy", nil, nil, "create")}, false},
		"audit config":              {[]ResourceChange{rc("google_project_iam_audit_config", nil, nil, "create")}, false},
		"service account key":       {[]ResourceChange{rc("google_service_account_key", nil, nil, "create")}, false},
		"unknown resource type":     {[]ResourceChange{rc("google_compute_instance", nil, nil, "create")}, false},
		"allUsers":                  {[]ResourceChange{grant("google_storage_bucket_iam_member", "allUsers", "roles/storage.objectViewer")}, false},
		"allAuthenticatedUsers":     {[]ResourceChange{grant("google_storage_bucket_iam_member", "allAuthenticatedUsers", "roles/storage.objectViewer")}, false},
		"domain member":             {[]ResourceChange{grant("google_project_iam_member", "domain:example.com", "roles/datastore.viewer")}, false},
		"member not on the screen":  {[]ResourceChange{grant("google_project_iam_member", "user:eve@example.com", "roles/datastore.viewer")}, false},
		"another project's account": {[]ResourceChange{grant("google_project_iam_member", "serviceAccount:fugaro-x@other.iam.gserviceaccount.com", "roles/logging.logWriter")}, false},
		"a non-fugaro account":      {[]ResourceChange{grant("google_project_iam_member", "serviceAccount:evil@proj-1.iam.gserviceaccount.com", "roles/logging.logWriter")}, false},
		"owner to a listed member":  {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "roles/owner")}, false},
		"editor":                    {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "roles/editor")}, false},
		"securityAdmin":             {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "roles/iam.securityAdmin")}, false},
		// D75's one-time impersonation setup: on a service account, for a
		// person the review listed — the grant that lets the owner assume the
		// deployer account. To a stranger, or on the project, is still refused
		// (the two rows below), and it stays refused on every other type.
		"tokenCreator to a listed member": {[]ResourceChange{grantOnAccount("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountTokenCreator", deployerAccountName("proj-1"))}, true},
		"tokenCreator to a stranger":      {[]ResourceChange{grantOnAccount("google_service_account_iam_member", "user:eve@example.com", "roles/iam.serviceAccountTokenCreator", deployerAccountName("proj-1"))}, false},
		"tokenCreator on the project":     {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "roles/iam.serviceAccountTokenCreator")}, false},
		// The account is the half grantRules cannot see: this role on a
		// person's behalf is covered only on the deployer account of this run
		// (deployerGrant), so the scheduler's account, a stranger's, another
		// project's deployer and an unnamed account are all refused.
		"tokenCreator on the scheduler account":                {[]ResourceChange{grantOnAccount("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountTokenCreator", "projects/proj-1/serviceAccounts/fugaro-scheduler@proj-1.iam.gserviceaccount.com")}, false},
		"tokenCreator on an unrelated account":                 {[]ResourceChange{grantOnAccount("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountTokenCreator", "projects/proj-1/serviceAccounts/eve@proj-1.iam.gserviceaccount.com")}, false},
		"tokenCreator on another project's deployer account":   {[]ResourceChange{grantOnAccount("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountTokenCreator", deployerAccountName("other"))}, false},
		"tokenCreator on an account named without its project": {[]ResourceChange{grantOnAccount("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountTokenCreator", DeployerAccount("proj-1"))}, false},
		"tokenCreator on an email of another project":          {[]ResourceChange{grantOnAccount("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountTokenCreator", "projects/proj-1/serviceAccounts/"+DeployerAccount("other"))}, false},
		"tokenCreator on no account":                           {[]ResourceChange{grant("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountTokenCreator")}, false},
		"tokenCreator on an unknown account":                   {[]ResourceChange{rc("google_service_account_iam_member", map[string]any{"member": "user:me@example.com", "role": "roles/iam.serviceAccountTokenCreator"}, map[string]any{"service_account_id": true}, "create")}, false},
		// The account rule is the token creator role's alone: the actAs grants
		// on the job and build accounts name their accounts however the module
		// does, and are not this check's business.
		"serviceAccountUser on a computed account": {[]ResourceChange{rc("google_service_account_iam_member", map[string]any{"member": "user:me@example.com", "role": "roles/iam.serviceAccountUser"}, map[string]any{"service_account_id": true}, "create")}, true},
		"keyAdmin":                         {[]ResourceChange{grant("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountKeyAdmin")}, false},
		"resourcemanager admin":            {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "roles/resourcemanager.projectIamAdmin")}, false},
		"serviceAccountUser to a stranger": {[]ResourceChange{grant("google_service_account_iam_member", "user:eve@example.com", "roles/iam.serviceAccountUser")}, false},
		"another project's custom role":    {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "projects/other/roles/fugaroLauncher")}, false},
		"a custom role not ours":           {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "projects/proj-1/roles/owner")}, false},
		"unknown member, no account made":  {[]ResourceChange{rc("google_project_iam_member", map[string]any{"role": "roles/logging.logWriter"}, map[string]any{"member": true}, "create")}, false},
		"unknown role, no role made":       {[]ResourceChange{rc("google_project_iam_member", map[string]any{"member": "user:me@example.com"}, map[string]any{"role": true}, "create")}, false},
		"custom role widened":              {[]ResourceChange{updRole("fugaroJobRunner", []any{"run.jobs.run"}, []any{"run.jobs.run", "run.jobs.runWithOverrides", "c.d"})}, false},
		"custom role narrowed":             {[]ResourceChange{updRole("fugaroJobRunner", []any{"run.jobs.run", "run.jobs.runWithOverrides", "c.d"}, []any{"run.jobs.run", "run.jobs.runWithOverrides"})}, false},
		"custom role permissions unknown": {[]ResourceChange{{Address: "r", Type: "google_project_iam_custom_role", Change: Change{Actions: []string{"update"},
			Before: map[string]any{"role_id": "fugaroJobRunner", "project": "proj-1", "permissions": []any{"run.jobs.run"}}, After: map[string]any{"role_id": "fugaroJobRunner", "project": "proj-1"}, AfterUnknown: map[string]any{"permissions": true}}}}, false},
		"custom role retitled":                                {[]ResourceChange{updRole("fugaroJobRunner", []any{"run.jobs.run", "run.jobs.runWithOverrides"}, []any{"run.jobs.run", "run.jobs.runWithOverrides"})}, true},
		"custom role updated to its pin from a drifted state": {[]ResourceChange{updRole("fugaroJobRunner", []any{"run.jobs.run", "run.jobs.runWithOverrides", "x.y"}, []any{"run.jobs.run", "run.jobs.runWithOverrides"})}, false},
		"custom role created as pinned":                       {[]ResourceChange{customRole("fugaroTokenMinter", "fp-2", []any{"iam.serviceAccounts.signJwt"})}, true},
		"custom role created with an extra permission":        {[]ResourceChange{customRole("fugaroJobRunner", "proj-1", []any{"run.jobs.run", "run.jobs.runWithOverrides", "iam.serviceAccounts.getAccessToken"})}, false},
		"custom role created with a missing permission":       {[]ResourceChange{customRole("fugaroJobRunner", "proj-1", []any{"run.jobs.run"})}, false},
		"custom role created with a swapped permission":       {[]ResourceChange{customRole("fugaroJobRunner", "proj-1", []any{"run.jobs.run", "iam.serviceAccounts.signJwt"})}, false},
		"unknown role id with no permissions":                 {[]ResourceChange{customRole("fugaroEverything", "proj-1", nil)}, false},
		"custom role created with no permissions":             {[]ResourceChange{customRole("fugaroJobRunner", "proj-1", nil)}, false},
		"token minter widened to sign blobs":                  {[]ResourceChange{customRole("fugaroTokenMinter", "fp-2", []any{"iam.serviceAccounts.signJwt", "iam.serviceAccounts.signBlob"})}, false},
		"custom role with an unknown id":                      {[]ResourceChange{customRole("fugaroEverything", "proj-1", []any{"run.jobs.run"})}, false},
		"custom role with another project's id":               {[]ResourceChange{customRole("fugaroJobRunner", "other", []any{"run.jobs.run", "run.jobs.runWithOverrides"})}, false},
		"custom role with no project":                         {[]ResourceChange{customRole("fugaroJobRunner", "", []any{"run.jobs.run", "run.jobs.runWithOverrides"})}, false},
		"custom role with an unknown project":                 {[]ResourceChange{unkRole("project")}, false},
		"custom role with a computed id":                      {[]ResourceChange{unkRole("role_id")}, false},
		"custom role with unknown permissions":                {[]ResourceChange{unkRole("permissions")}, false},
		"project unknown on a bucket":                         {[]ResourceChange{rc("google_storage_bucket", map[string]any{"public_access_prevention": "enforced", "uniform_bucket_level_access": true}, map[string]any{"project": true}, "create")}, false},
		"project unknown on a secret":                         {[]ResourceChange{rc("google_secret_manager_secret", map[string]any{}, map[string]any{"project": true}, "create")}, false},
		"command unknown":                                     {[]ResourceChange{jobUnknown(nil, nil, "command")}, false},
		"args unknown":                                        {[]ResourceChange{jobUnknown(nil, nil, "args")}, false},
		"command unknown on the known sweep":                  {[]ResourceChange{jobUnknown([]any{"/usr/local/bin/fugaro"}, []any{"budget", "history", "--sweep"}, "command")}, false},
		"containers unknown":                                  {[]ResourceChange{rc("google_cloud_run_v2_job", map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{}}}}}, map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"containers": true}}}}}, "create")}, false},
		"image unknown":                                       {[]ResourceChange{jobUnknown(nil, nil, "image")}, false},
	} {
		t.Run(name, func(t *testing.T) {
			var p *Plan
			if tc.changes != nil {
				p = &Plan{ResourceChanges: tc.changes}
			}
			if got := testCover.NotCovered(p, tableImports); (len(got) == 0) != tc.covered {
				t.Fatalf("NotCovered = %q, covered want %v", got, tc.covered)
			}
		})
	}
}

// tableImports are the imports TestCoverNotCovered's discovery wrote: both of
// its import cases are on it, so the other project's import is refused for
// its project alone.
var tableImports = []ImportKey{{"a.google_storage_bucket.x", "proj-1/fugaro-runs-proj-1"}, {"a.google_storage_bucket.x", "other/fugaro-runs"}}

// launcherPerms are fugaroLauncher's pinned permissions, as a plan lists them.
func launcherPerms(extra ...any) []any {
	var out []any
	for _, p := range RolePermissions["fugaroLauncher"] {
		out = append(out, p)
	}
	return append(out, extra...)
}

// importRole is an import of the launcher role at addr with ID id, as the
// modules declare it (actions acts; an update keeps its permissions unless
// after says otherwise).
func importRole(addr, id string, after []any, acts ...string) ResourceChange {
	return ResourceChange{Address: addr, Type: "google_project_iam_custom_role", Change: Change{Actions: acts, Importing: &Importing{ID: id},
		Before:       map[string]any{"role_id": "fugaroLauncher", "project": "proj-1", "permissions": launcherPerms()},
		After:        map[string]any{"role_id": "fugaroLauncher", "project": "proj-1", "permissions": after},
		AfterUnknown: map[string]any{}}}
}

// An import is covered only when its address and ID are exactly a pair the
// root's discovery wrote, and its ID is in one of the run's projects; the
// other checks still apply to it.
func TestImportsAreCoveredOnlyWhenDiscoveryWroteThem(t *testing.T) {
	const addr = "module.installation.google_project_iam_custom_role.launcher"
	key := ImportKey{addr, "projects/proj-1/roles/fugaroLauncher"}
	keyAddr := "module.firebase.google_apikeys_key.web"
	keyID := "projects/proj-1/locations/global/keys/fugaro-web"
	apiKey := func(targets ...string) ResourceChange {
		var ts []any
		for _, s := range targets {
			ts = append(ts, map[string]any{"service": s})
		}
		return ResourceChange{Address: keyAddr, Type: "google_apikeys_key", Change: Change{Actions: []string{"update"}, Importing: &Importing{ID: keyID},
			After: map[string]any{"restrictions": []any{map[string]any{"api_targets": ts}}}, AfterUnknown: map[string]any{}}}
	}
	other := ImportKey{"module.installation.google_service_account.scheduler", "projects/proj-1/serviceAccounts/fugaro-scheduler@proj-1.iam.gserviceaccount.com"}
	sa := ResourceChange{Address: other.Address, Type: "google_service_account", Change: Change{Actions: []string{"no-op"}, Importing: &Importing{ID: other.ID},
		After: map[string]any{"project": "proj-1"}, AfterUnknown: map[string]any{}}}
	for _, tc := range []struct {
		name    string
		changes []ResourceChange
		list    []ImportKey
		covered bool
		why     string // a not-covered reason must contain it
	}{
		{"on the list", []ResourceChange{importRole(addr, key.ID, launcherPerms(), "no-op")}, []ImportKey{key}, true, ""},
		{"import and update on the list", []ResourceChange{importRole(addr, key.ID, launcherPerms(), "update")}, []ImportKey{key}, true, ""},
		{"empty list", []ResourceChange{importRole(addr, key.ID, launcherPerms(), "no-op")}, nil, false,
			addr + " (imports " + key.ID + ", which this run's discovery did not find)"},
		{"same id other address", []ResourceChange{importRole("module.installation.google_project_iam_custom_role.tag_mover", key.ID, launcherPerms(), "no-op")}, []ImportKey{key}, false,
			"module.installation.google_project_iam_custom_role.tag_mover (imports " + key.ID + ", which this run's discovery did not find)"},
		{"same address other id in the project", []ResourceChange{importRole(addr, "projects/proj-1/roles/other", launcherPerms(), "no-op")}, []ImportKey{key}, false,
			"imports projects/proj-1/roles/other, which this run's discovery did not find"},
		{"another project's id, even listed", []ResourceChange{importRole(addr, "projects/evil/roles/fugaroLauncher", launcherPerms(), "no-op")}, []ImportKey{{addr, "projects/evil/roles/fugaroLauncher"}}, false,
			"imports projects/evil/roles/fugaroLauncher, which is not in this run's projects"},
		{"no imports in the plan, list not empty", []ResourceChange{customRole("fugaroLauncher", "proj-1", launcherPerms())}, []ImportKey{key}, true, ""},
		{"two imports, one listed", []ResourceChange{importRole(addr, key.ID, launcherPerms(), "no-op"), sa}, []ImportKey{key}, false,
			other.Address + " (imports " + other.ID + ", which this run's discovery did not find)"},
		{"two imports, both listed", []ResourceChange{importRole(addr, key.ID, launcherPerms(), "no-op"), sa}, []ImportKey{key, other}, true, ""},
		{"import and update widening the role, listed", []ResourceChange{importRole(addr, key.ID, launcherPerms("iam.serviceAccounts.getAccessToken"), "update")}, []ImportKey{key}, false,
			"changes a custom role's permissions"},
		{"import and update of the key as the module writes it, listed", []ResourceChange{apiKey(APITargets()...)}, []ImportKey{{keyAddr, keyID}}, true, ""},
		{"import and update widening the key, listed", []ResourceChange{apiKey("identitytoolkit.googleapis.com", "storage.googleapis.com")}, []ImportKey{{keyAddr, keyID}}, false, keyAddr},
		{"delete with an import, listed", []ResourceChange{importRole(addr, key.ID, launcherPerms(), "delete")}, []ImportKey{key}, false, "actions delete"},
		{"replace with an import, listed", []ResourceChange{importRole(addr, key.ID, launcherPerms(), "delete", "create")}, []ImportKey{key}, false, "actions delete, create"},
		{"a type outside the modules, listed", []ResourceChange{{Address: "module.x.google_compute_instance.vm", Type: "google_compute_instance",
			Change: Change{Actions: []string{"no-op"}, Importing: &Importing{ID: "projects/proj-1/zones/z/instances/vm"}, After: map[string]any{}, AfterUnknown: map[string]any{}}}},
			[]ImportKey{{"module.x.google_compute_instance.vm", "projects/proj-1/zones/z/instances/vm"}}, false, "a resource type outside the installation's modules"},
		{"an import by identity, listed", []ResourceChange{importRole(addr, "", launcherPerms(), "no-op")}, []ImportKey{{addr, ""}}, false, "not in this run's projects"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := testCover.NotCovered(&Plan{ResourceChanges: tc.changes}, tc.list)
			if (len(got) == 0) != tc.covered {
				t.Fatalf("NotCovered = %q, covered want %v", got, tc.covered)
			}
			if tc.covered {
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], tc.why) {
				t.Fatalf("NotCovered = %q, want one reason containing %q", got, tc.why)
			}
		})
	}
}

// The allowlists are the modules': a module that declares a resource type or
// grants a predefined role that is not listed fails here, so init's one
// confirmation never covers something nobody decided on.
func TestAllowedTypesCoverTheModules(tt *testing.T) {
	t := tt
	root := filepath.Join("..", "..", "..", "deploy", "terraform")
	resRE := regexp.MustCompile(`(?m)^resource "([a-z0-9_]+)"`)
	roleRE := regexp.MustCompile(`"(roles/[A-Za-z0-9.]+)"`)
	n := 0
	usedTypes, usedRoles := map[string]bool{}, map[string]bool{}
	all := map[string]string{} // path -> content
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tf") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n++
		all[path] = string(b)
		for _, m := range resRE.FindAllStringSubmatch(string(b), -1) {
			usedTypes[m[1]] = true
			if !allowedTypes[m[1]] {
				t.Errorf("%s declares %s, which init's one confirmation does not allow: decide it in tf/cover.go", path, m[1])
			}
		}
		for _, m := range roleRE.FindAllStringSubmatch(string(b), -1) {
			usedRoles[m[1]] = true
			if !allowedRoles[m[1]] {
				t.Errorf("%s grants %s, which is not in tf/cover.go's roles", path, m[1])
			}
		}
		return nil
	})
	if err != nil || n < 20 {
		t.Fatalf("read %d module files: %v", n, err)
	}
	// The reverse: nothing is allowed that no module uses.
	for t := range allowedTypes {
		if !usedTypes[t] {
			tt.Errorf("type %s is allowed but no module declares it", t)
		}
	}
	for r := range allowedRoles {
		if !usedRoles[r] {
			tt.Errorf("role %s is allowed but no module grants it", r)
		}
	}
	// The attribute values the checks expect are the modules' literals.
	pins := map[string][]string{
		"modules/installation/history.tf": {`"/usr/local/bin/fugaro"`, `["budget", "history", "--sweep"]`, `https://run.googleapis.com/v2/projects/${var.project}/locations/`, `service_account_email = google_service_account.scheduler.email`},
		"modules/repo/check.tf":           {`command = ["fugaro"]`, `["image", "check", "--job"]`, `https://run.googleapis.com/v2/projects/${var.project}/locations/`},
		"modules/installation/logging.tf": {`destination            = "logging.googleapis.com/${local.log_bucket_path}"`, `projects/${var.project}/`},
		"modules/installation/bucket.tf":  {`uniform_bucket_level_access = true`, `public_access_prevention    = "enforced"`},
		"modules/firebase/firebase.tf":    {`identitytoolkit.googleapis.com`, `securetoken.googleapis.com`},
		"modules/installation/budget.tf":  {`billing_account = var.budget.billing_account`},
	}
	for file, wants := range pins {
		got := all[filepath.Join(root, "gcp", file)]
		for _, w := range wants {
			if !strings.Contains(got, w) {
				tt.Errorf("%s no longer contains %s: update the attribute checks in tf/cover.go", file, w)
			}
		}
	}
	// tokenCreator is not on this list any more: D75 grants it, on the
	// deployer account, to the operators (installation/deployer.tf), which is
	// what makes a managed apply possible. It is allowed on a service account
	// for a person and nowhere else — TestCoverNotCovered's three tokenCreator
	// rows and TestGrantRulesMatrix's matrix are what hold that.
	for _, never := range []string{"roles/owner", "roles/editor", "roles/iam.securityAdmin", "roles/iam.serviceAccountKeyAdmin"} {
		if allowedRoles[never] {
			t.Errorf("%s is allowed", never)
		}
	}
	for _, never := range []string{"google_project_iam_binding", "google_project_iam_policy", "google_service_account_key", "google_project_iam_audit_config"} {
		if allowedTypes[never] {
			t.Errorf("%s is allowed", never)
		}
	}
}

// A job's FUGARO_CHECK_SPEC tells the check job where to build and what to
// build from: its registry and every base image must be this run's.
func jobWithSpec(spec string, unknownValue bool) ResourceChange {
	env := []any{map[string]any{"name": "FUGARO_PROJECT", "value": "aurora"}, map[string]any{"name": "FUGARO_CHECK_SPEC", "value": spec}}
	c := map[string]any{"image": "us-east5-docker.pkg.dev/proj-1/fugaro-base/x:1", "command": []any{"fugaro"}, "args": []any{"image", "check", "--job"}, "env": env}
	unk := map[string]any{}
	if unknownValue {
		delete(env[1].(map[string]any), "value")
		unk["env"] = []any{map[string]any{}, map[string]any{"value": true}}
	}
	return rc("google_cloud_run_v2_job", map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"containers": []any{c}}}}}},
		map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"containers": []any{unk}}}}}}, "create")
}

const okBuild = "fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com"

func TestCoverCheckSpec(t *testing.T) {
	const reg = "us-east5-docker.pkg.dev/proj-1/"
	spec := func(registry string, bases ...string) string {
		b := map[string]string{}
		for i, r := range bases {
			b[string(rune('a'+i))] = r
		}
		m := map[string]any{"repo": "acme/webapp", "registry": registry, "build_service_account": okBuild, "base_images": b}
		out, _ := json.Marshal(m)
		return string(out)
	}
	for name, tc := range map[string]struct {
		job     ResourceChange
		covered bool
	}{
		"as the module writes it":                         {jobWithSpec(spec(reg+"fugaro-acme-webapp-e9eafd3c6e8c", reg+"fugaro-base/web:1", reg+"fugaro-base/api:2"), false), true},
		"no base images":                                  {jobWithSpec(spec(reg+"fugaro-acme-webapp-e9eafd3c6e8c"), false), true},
		"foreign registry":                                {jobWithSpec(spec("evil.example/x/y", reg+"fugaro-base/web:1"), false), false},
		"another project's registry":                      {jobWithSpec(spec("us-east5-docker.pkg.dev/other/r"), false), false},
		"another region's registry":                       {jobWithSpec(spec("europe-docker.pkg.dev/proj-1/r"), false), false},
		"no registry":                                     {jobWithSpec(spec(""), false), false},
		"registry is the bare host":                       {jobWithSpec(spec("us-east5-docker.pkg.dev"), false), false},
		"registry prefix by lookalike":                    {jobWithSpec(spec("us-east5-docker.pkg.dev/proj-12/r"), false), false},
		"registry by host lookalike":                      {jobWithSpec(spec("us-east5-docker.pkg.dev.evil.example/proj-1/r"), false), false},
		"registry with dot-dot":                           {jobWithSpec(spec(reg+"../other/r"), false), false},
		"one foreign base image":                          {jobWithSpec(spec(reg+"r", reg+"fugaro-base/web:1", "evil.example/web:1"), false), false},
		"base image of another project":                   {jobWithSpec(spec(reg+"r", "us-east5-docker.pkg.dev/other/fugaro-base/web:1"), false), false},
		"base image with a space":                         {jobWithSpec(spec(reg+"r", reg+"fugaro-base/web:1 --evil"), false), false},
		"base image with a newline":                       {jobWithSpec(spec(reg+"r", reg+"fugaro-base/web:1\nRUN x"), false), false},
		"base image with a quote":                         {jobWithSpec(spec(reg+"r", reg+"fugaro-base/web:1'"), false), false},
		"base image with a dollar":                        {jobWithSpec(spec(reg+"r", reg+"fugaro-base/$X"), false), false},
		"base image with a semicolon":                     {jobWithSpec(spec(reg+"r", reg+"fugaro-base/web;x"), false), false},
		"registry with a space":                           {jobWithSpec(spec(reg+"r x"), false), false},
		"base image by digest":                            {jobWithSpec(spec(reg+"r", reg+"fugaro-base/web@sha256:0123abcd"), false), true},
		"empty base image":                                {jobWithSpec(spec(reg+"r", ""), false), false},
		"base image with dot-dot":                         {jobWithSpec(spec(reg+"r", reg+"../other/web:1"), false), false},
		"not json":                                        {jobWithSpec("registry="+reg, false), false},
		"json that is not an object":                      {jobWithSpec(`["`+reg+`"]`, false), false},
		"registry not a string":                           {jobWithSpec(`{"build_service_account":"fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com","registry":7}`, false), false},
		"base_images not an object":                       {jobWithSpec(`{"build_service_account":"fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com","registry":"`+reg+`r","base_images":["x"]}`, false), false},
		"base image not a string":                         {jobWithSpec(`{"build_service_account":"fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com","registry":"`+reg+`r","base_images":{"a":5}}`, false), false},
		"case-folded duplicate key, as the job parses it": {jobWithSpec(`{"build_service_account":"fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com","registry":"`+reg+`r","Registry":"evil.example/x"}`, false), false},
		"duplicate key, last wins":                        {jobWithSpec(`{"build_service_account":"fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com","registry":"evil.example/x","registry":"`+reg+`r"}`, false), true},
		"null base images":                                {jobWithSpec(`{"build_service_account":"fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com","registry":"`+reg+`r","base_images":null}`, false), true},
		"spec unknown":                                    {jobWithSpec("", true), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{tc.job}}, nil); (len(got) == 0) != tc.covered {
				t.Fatalf("NotCovered = %q, covered want %v", got, tc.covered)
			}
		})
	}
}

// Two FUGARO_CHECK_SPEC entries are both read, and a spec on any job counts.
func TestCoverCheckSpecEveryEntry(t *testing.T) {
	j := jobWithSpec(`{"build_service_account":"fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com","registry":"us-east5-docker.pkg.dev/proj-1/r"}`, false)
	cs := j.Change.After["template"].([]any)[0].(map[string]any)["template"].([]any)[0].(map[string]any)["containers"].([]any)[0].(map[string]any)
	cs["env"] = append(cs["env"].([]any), map[string]any{"name": "FUGARO_CHECK_SPEC", "value": `{"build_service_account":"fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com","registry":"evil.example/x"}`})
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{j}}, nil); len(got) == 0 {
		t.Fatal("a second, foreign FUGARO_CHECK_SPEC is covered")
	}
}

// The permissions pinned in cover.go are the modules': a module that adds
// or drops a permission of a custom role fails here until the pin is updated
// by decision, so a widened role can never ride along on a covered create.
func TestRolePinsAreTheModules(t *testing.T) {
	root := filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules")
	blockRE := regexp.MustCompile(`(?s)resource "google_project_iam_custom_role" "([a-z_]+)" \{(.*?)\n\}`)
	permsRE := regexp.MustCompile(`(?s)permissions\s*=\s*\[(.*?)\]`)
	strRE := regexp.MustCompile(`"([^"]+)"`)
	// resource name -> the role ID the modules give it (names.role_ids and the
	// literals in history.tf and the Firebase module's names.minter_role_id).
	ids := map[string]string{"launcher": "fugaroLauncher", "job_runner": "fugaroJobRunner", "build_submitter": "fugaroBuildSubmitter", "tag_mover": "fugaroTagMover", "cloud_asset_reader": CloudAssetRole, "token_minter": "fugaroTokenMinter", "history": "fugaroHistory", "reaper": "fugaroReaper", "preload_builder": "fugaroPreloadBuilder", "fugaro_deployer": DeployerRole}
	for role, label := range deployerRoleLabel {
		ids[label] = role
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tf") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range blockRE.FindAllStringSubmatch(string(b), -1) {
			id, ok := ids[m[1]]
			if !ok {
				t.Errorf("%s declares the custom role %s, which tf/cover.go does not pin", path, m[1])
				continue
			}
			seen[id] = true
			// Comments hold quotes too: drop them before reading the list.
			var lines []string
			for _, l := range strings.Split(m[2], "\n") {
				if i := strings.Index(l, "#"); i >= 0 {
					l = l[:i]
				}
				lines = append(lines, l)
			}
			pm := permsRE.FindStringSubmatch(strings.Join(lines, "\n"))
			if pm == nil {
				t.Errorf("%s: no permissions list in %s", path, m[1])
				continue
			}
			var got []string
			for _, s := range strRE.FindAllStringSubmatch(pm[1], -1) {
				got = append(got, s[1])
			}
			want := slices.Clone(RolePermissions[id])
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("%s (%s) grants %v, but tf/cover.go pins %v: widening a role is a decision, update RolePermissions", path, id, got, want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range FugaroRoles {
		if !seen[id] {
			t.Errorf("role %s is in FugaroRoles but no module declares it", id)
		}
		if len(RolePermissions[id]) == 0 {
			t.Errorf("role %s has no pinned permissions", id)
		}
	}
	if len(RolePermissions) != len(FugaroRoles) {
		t.Errorf("RolePermissions has %d roles, FugaroRoles %d", len(RolePermissions), len(FugaroRoles))
	}
	for _, never := range []string{"iam.serviceAccounts.getAccessToken", "iam.serviceAccounts.signBlob", "iam.serviceAccountKeys.create", "iam.serviceAccounts.actAs", "resourcemanager.projects.setIamPolicy", "iam.roles.update"} {
		for id, ps := range RolePermissions {
			if slices.Contains(ps, never) {
				// One exception, and it is the owner's answer of 2026-10-16
				// (D75, amended the same day): the conditioned deployer role
				// for service accounts holds actAs, because a managed job or
				// schedule must run as an account of the plan's own. The bound
				// on it is the condition on the single binding of that role
				// (the prefix fence, which admits no account the plan did not
				// create) and TestDeployerRolesHoldOnlyWhatD75Names below — not
				// this list, which is about roles a Fugaro run holds. The other
				// five stay forbidden for every deployer role.
				if id == DeployerAccountRole && never == "iam.serviceAccounts.actAs" {
					continue
				}
				t.Errorf("%s pins %s", id, never)
			}
		}
	}
}

// Unknown flags are honoured even when a value is shown beside them: the
// plan's after_unknown is the authority on what apply will decide.
func TestCoverUnknownFlagsWin(t *testing.T) {
	flag := func(j ResourceChange, path ...any) ResourceChange {
		// set after_unknown at path (maps and one-element lists) to true
		var set func(v any, path []any) any
		set = func(v any, path []any) any {
			if len(path) == 0 {
				return true
			}
			switch k := path[0].(type) {
			case string:
				m, _ := v.(map[string]any)
				if m == nil {
					m = map[string]any{}
				}
				m[k] = set(m[k], path[1:])
				return m
			default:
				l, _ := v.([]any)
				i := k.(int)
				for len(l) <= i {
					l = append(l, nil)
				}
				l[i] = set(l[i], path[1:])
				return l
			}
		}
		j.Change.AfterUnknown = set(j.Change.AfterUnknown, path)
		return j
	}
	ok := jobWithSpec(`{"build_service_account":"fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com","registry":"us-east5-docker.pkg.dev/proj-1/r"}`, false)
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ok}}, nil); len(got) != 0 {
		t.Fatalf("baseline not covered: %q", got)
	}
	for name, path := range map[string][]any{
		"containers": {"template", 0, "template", 0, "containers"},
		"env":        {"template", 0, "template", 0, "containers", 0, "env"},
		"env value":  {"template", 0, "template", 0, "containers", 0, "env", 1, "value"},
		"env name":   {"template", 0, "template", 0, "containers", 0, "env", 1, "name"},
		"command":    {"template", 0, "template", 0, "containers", 0, "command"},
		// a whole ancestor block unknown hides everything under it
		"template block":       {"template"},
		"inner template block": {"template", 0, "template"},
		"container element":    {"template", 0, "template", 0, "containers", 0},
		"container image":      {"template", 0, "template", 0, "containers", 0, "image"},
		"container args":       {"template", 0, "template", 0, "containers", 0, "args"},
	} {
		t.Run(name, func(t *testing.T) {
			j := flag(jobWithSpec(`{"build_service_account":"fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com","registry":"us-east5-docker.pkg.dev/proj-1/r"}`, false), path...)
			if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{j}}, nil); len(got) == 0 {
				t.Fatal("covered")
			}
		})
	}
	// An environment only known after apply could hold any spec.
	nospec := jobUnknown([]any{"fugaro"}, []any{"image", "check", "--job"}, "env")
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{nospec}}, nil); len(got) == 0 {
		t.Fatal("a job with an unknown environment is covered")
	}
}

// APITargets hands out a fresh slice: a caller that changes it cannot
// change the rule.
func TestAPITargetsIsAFreshSlice(t *testing.T) {
	a := APITargets()
	a[0] = "storage.googleapis.com"
	if got := APITargets(); got[0] != "identitytoolkit.googleapis.com" || len(got) != 2 {
		t.Fatalf("APITargets() = %v after a caller changed its copy", got)
	}
}

// The remote (pull-through) repository proxies ALL of ghcr.io, so
// "<registry>/<project>/" stops confining anything the moment it exists: a
// job CONTAINER's image under the remote repository's ID must match the
// base pin's closed shape (design registry-remote-repository.md §3.6) — the
// RECORDED owner, a fugaro-<kind> image, a version tag and a required
// digest — and anything else under that ID is refused.
//
// This is the container's own inline test, which is NOT checkSpecs's
// inRegistry: it is the one that guards what a run's job pulls, and the
// hole this change exists to close. The owner is a parameter, so an empty
// one refuses every remote path rather than trusting whatever owner the
// reference names.
// Mutation (run, restore): in Cover.attributes' container loop, call
// inRegistry instead of baseImageOK — every "refused" row but the empty
// owner's becomes covered.
func TestCoverRefusesAJobImageFromAProxyPathThatIsNotThePin(t *testing.T) {
	const reg = "us-east5-docker.pkg.dev/proj-1/"
	pin := func(s string) string { return reg + "fugaro-ghcr/dimipaun/fugaro-" + s }
	d := strings.Repeat("a", 64)
	noOwner := testCover
	noOwner.Owner = ""
	for name, tc := range map[string]struct {
		cover   Cover
		image   string
		covered bool
	}{
		"this installation's pin":           {testCover, pin("base:1.2.3@sha256:") + d, true},
		"a foreign owner through the proxy": {testCover, reg + "fugaro-ghcr/evil/fugaro-base:9.9.9@sha256:" + d, false},
		"the pin without a digest":          {testCover, pin("base:1.2.3"), false},
		"the pin without a version tag":     {testCover, pin("base@sha256:") + d, false},
		"the pin with an empty version tag": {testCover, pin("base:@sha256:") + d, false},
		// The tag is the version marker every reader of a pin works from,
		// so a tag that is not a release version is not a pin this tool
		// wrote (managedVersion and baseIsCurrent would read "" or a dev
		// sha out of it).
		"the pin with a latest tag":         {testCover, pin("base:latest@sha256:") + d, false},
		"the pin with a dev tag":            {testCover, pin("base:dev-abc1234@sha256:") + d, false},
		"the pin with a two-part version":   {testCover, pin("base:1.2@sha256:") + d, false},
		"not a fugaro image":                {testCover, reg + "fugaro-ghcr/dimipaun/nginx:1.2.3@sha256:" + d, false},
		"not a sha256 digest":               {testCover, pin("base:1.2.3@sha256:") + strings.Repeat("a", 63), false},
		"a bare path under the proxy":       {testCover, reg + "fugaro-ghcr/anything", false},
		"the remote repository itself":      {testCover, reg + "fugaro-ghcr/", false},
		"an empty owner refuses the pin":    {noOwner, pin("base:1.2.3@sha256:") + d, false},
		"an empty owner, the base registry": {noOwner, reg + "fugaro-base/x:1", true},
		"the base registry is as it was":    {testCover, reg + "fugaro-base/x:1", true},
		"the run's own repository":          {testCover, reg + "fugaro-acme-webapp/app:1", true},
		"dot-dot under the proxy":           {testCover, reg + "fugaro-ghcr/../evil/fugaro-base:1@sha256:" + d, false},
	} {
		t.Run(name, func(t *testing.T) {
			p := &Plan{ResourceChanges: []ResourceChange{job(tc.image, []any{"fugaro"}, []any{"image", "check", "--job"})}}
			if got := tc.cover.NotCovered(p, nil); (len(got) == 0) != tc.covered {
				t.Fatalf("NotCovered = %q, covered want %v", got, tc.covered)
			}
		})
	}
}

// TestCoverRefusesACheckSpecOutsideThePin is the OTHER path: checkSpecs's
// inRegistry, reached through FUGARO_CHECK_SPEC. The spec's own registry
// and each of its base images must be refused in exactly the same cases as
// the job image above — two tests, not one, because a change that fixes
// only this one leaves the container accepting anything under
// <registry>/<project>/, and vice versa.
// Mutation (run, restore): make inRegistry return true after the prefix
// test alone and every "refused" row is covered.
func TestCoverRefusesACheckSpecOutsideThePin(t *testing.T) {
	const reg = "us-east5-docker.pkg.dev/proj-1/"
	d := strings.Repeat("a", 64)
	ok := reg + "fugaro-acme-webapp-e9eafd3c6e8c"
	pin := reg + "fugaro-ghcr/dimipaun/fugaro-base:1.2.3@sha256:" + d
	foreign := reg + "fugaro-ghcr/evil/fugaro-base:9.9.9@sha256:" + d
	noDigest := reg + "fugaro-ghcr/dimipaun/fugaro-base:1.2.3"
	noOwner := testCover
	noOwner.Owner = ""
	for name, tc := range map[string]struct {
		cover    Cover
		registry string
		bases    []string
		covered  bool
	}{
		"the pin, as the migrated module writes it": {testCover, ok, []string{pin}, true},
		"the pre-migration copy":                    {testCover, ok, []string{reg + "fugaro-base/web:1"}, true},
		"a foreign owner":                           {testCover, ok, []string{foreign}, false},
		"the pin without a digest":                  {testCover, ok, []string{noDigest}, false},
		"an empty owner refuses the pin":            {noOwner, ok, []string{pin}, false},
		"the spec's own registry under the proxy":   {testCover, reg + "fugaro-ghcr/dimipaun/fugaro-base:1.2.3", nil, false},
		"a foreign registry, as before":             {testCover, "evil.example/x/y", nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			b := map[string]string{}
			for i, r := range tc.bases {
				b[string(rune('a'+i))] = r
			}
			m := map[string]any{"repo": "acme/webapp", "registry": tc.registry, "build_service_account": okBuild, "base_images": b}
			v, _ := json.Marshal(m)
			p := &Plan{ResourceChanges: []ResourceChange{jobWithSpec(string(v), false)}}
			if got := tc.cover.NotCovered(p, nil); (len(got) == 0) != tc.covered {
				t.Fatalf("NotCovered = %q, covered want %v", got, tc.covered)
			}
		})
	}
}

// --- The deployer account of D75 (Task 1.5) -------------------------------

// deployerRoleGolden is every deployer role's permission list, permission for
// permission, as deploy/terraform/gcp/modules/installation/deployer.tf writes
// them. A widening — one permission added to any of the eight roles — is a diff
// here and in the module pin (TestRolePinsAreTheModules), so it cannot ride
// along on a covered create.
var deployerRoleGolden = map[string][]string{
	DeployerRole: {
		"apikeys.keys.create", "apikeys.keys.get", "apikeys.keys.list",
		"artifactregistry.repositories.create", "artifactregistry.repositories.get", "artifactregistry.repositories.getIamPolicy", "artifactregistry.repositories.list",
		"cloudscheduler.jobs.create", "cloudscheduler.jobs.get", "cloudscheduler.jobs.list",
		"iam.serviceAccounts.create", "iam.serviceAccounts.get", "iam.serviceAccounts.getIamPolicy", "iam.serviceAccounts.list",
		"run.jobs.create", "run.jobs.get", "run.jobs.getIamPolicy", "run.jobs.list",
		"secretmanager.secrets.create", "secretmanager.secrets.get", "secretmanager.secrets.getIamPolicy", "secretmanager.secrets.list",
		"serviceusage.services.enable", "serviceusage.services.get", "serviceusage.services.list",
		"storage.buckets.create",
	},
	DeployerStorageRole: {
		"storage.buckets.delete", "storage.buckets.get", "storage.buckets.setIamPolicy", "storage.buckets.update",
		"storage.objects.create", "storage.objects.delete", "storage.objects.get", "storage.objects.list", "storage.objects.update",
	},
	DeployerSecretRole: {
		"secretmanager.secrets.delete", "secretmanager.secrets.setIamPolicy", "secretmanager.secrets.update",
	},
	DeployerAccountRole: {
		"iam.serviceAccounts.actAs", "iam.serviceAccounts.delete", "iam.serviceAccounts.setIamPolicy", "iam.serviceAccounts.update",
	},
	DeployerAPIKeyRole: {
		"apikeys.keys.delete", "apikeys.keys.update",
	},
	DeployerRepositoryRole: {
		"artifactregistry.repositories.delete", "artifactregistry.repositories.setIamPolicy", "artifactregistry.repositories.update",
	},
	DeployerRunJobRole: {
		"run.jobs.delete", "run.jobs.run", "run.jobs.runWithOverrides", "run.jobs.setIamPolicy", "run.jobs.update",
	},
	DeployerSchedulerRole: {
		"cloudscheduler.jobs.delete", "cloudscheduler.jobs.enable", "cloudscheduler.jobs.pause", "cloudscheduler.jobs.run", "cloudscheduler.jobs.update",
	},
}

// deployerRoleLabel is each conditioned role's Terraform resource label in
// deployer.tf. The module's names and the roles are one list: the module-pin
// test reads the condition local deployer_condition_<suffix> off the role ID,
// and this says which resource carries that role's permissions and binding.
var deployerRoleLabel = map[string]string{
	DeployerStorageRole:    "fugaro_deployer_storage",
	DeployerSecretRole:     "fugaro_deployer_secret",
	DeployerAccountRole:    "fugaro_deployer_account",
	DeployerAPIKeyRole:     "fugaro_deployer_apikey",
	DeployerRepositoryRole: "fugaro_deployer_repository",
	DeployerRunJobRole:     "fugaro_deployer_run_job",
	DeployerSchedulerRole:  "fugaro_deployer_scheduler_job",
}

// permVerb is a permission's last segment: the verb of service.resource.verb.
func permVerb(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[i+1:]
	}
	return p
}

// changesExisting is whether a permission changes or reaches an existing
// resource, which the unconditioned deployer role may never hold. GCP names a
// Cloud Scheduler job's resume cloudscheduler.jobs.enable (no
// cloudscheduler.jobs.resume exists), so enable is a changing verb too, with
// one exception: serviceusage.services.enable, which turns an API on and is
// the unconditioned role's on purpose.
func changesExisting(p string) bool {
	switch permVerb(p) {
	case "update", "delete", "run", "runWithOverrides", "pause", "setIamPolicy", "actAs":
		return true
	case "enable":
		return p != "serviceusage.services.enable"
	}
	return false
}

// unconditionedDeployerViolations is why perms, as fugaroDeployer's list,
// breaks D75's split: a verb that belongs to the scoped role, or a storage
// permission other than the one creation.
func unconditionedDeployerViolations(perms []string) []string {
	var out []string
	for _, p := range perms {
		if changesExisting(p) {
			out = append(out, fmt.Sprintf("%s holds %s: that verb belongs to the scoped role, under its condition", DeployerRole, p))
		}
		if strings.HasPrefix(p, "storage.") && p != "storage.buckets.create" {
			out = append(out, fmt.Sprintf("%s holds %s: the only storage permission it may hold is storage.buckets.create", DeployerRole, p))
		}
	}
	return out
}

// The unconditioned role's check must catch every scoped verb moved into it,
// Cloud Scheduler's resume (cloudscheduler.jobs.enable) included, and must not
// trip on serviceusage.services.enable, which that role holds by design.
func TestUnconditionedDeployerCheckCatchesEveryScopedVerb(t *testing.T) {
	if got := unconditionedDeployerViolations(deployerRoleGolden[DeployerRole]); len(got) != 0 {
		t.Fatalf("the real unconditioned role is refused: %v", got)
	}
	for _, id := range DeployerServiceRoles() {
		for _, p := range deployerRoleGolden[id] {
			moved := append(slices.Clone(deployerRoleGolden[DeployerRole]), p)
			if got := unconditionedDeployerViolations(moved); len(got) == 0 {
				t.Errorf("moving %s out of %s into %s is not caught", p, id, DeployerRole)
			}
		}
	}
	if got := unconditionedDeployerViolations([]string{"cloudscheduler.jobs.enable"}); len(got) == 0 {
		t.Error("cloudscheduler.jobs.enable (a Scheduler job's resume) in the unconditioned role is not caught")
	}
}

// The deployer's eight roles hold exactly the lists D75 names, and the split is
// the decision: every verb that changes or reaches an existing resource —
// update, delete, run, pause, resume (cloudscheduler.jobs.enable), setIamPolicy
// — and actAs itself are in the conditioned roles alone, each bound once and
// only under its own managed-prefix condition. The other role creates, reads
// and enables, and nothing else.
func TestDeployerRolesHoldOnlyWhatD75Names(t *testing.T) {
	for id, want := range deployerRoleGolden {
		got := slices.Clone(RolePermissions[id])
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s pins %v, want %v", id, got, want)
		}
	}
	// A wildcard is never a permission, and no deployer role may reach project
	// IAM or an IAM role: an account holding either could grant itself object
	// access to the runs bucket and forge the marker.
	for id, ps := range deployerRoleGolden {
		for _, p := range ps {
			switch {
			case strings.Contains(p, "*"):
				t.Errorf("%s holds %s, which is a wildcard", id, p)
			case strings.HasPrefix(p, "resourcemanager.projects."):
				t.Errorf("%s holds %s: no project-level IAM permission at all", id, p)
			case strings.HasPrefix(p, "iam.roles."):
				t.Errorf("%s holds %s: an account that can edit its own role can widen itself", id, p)
			case strings.Contains(p, "getAccessToken") || strings.Contains(p, "signBlob") || strings.Contains(p, "signJwt") || strings.Contains(p, "serviceAccountKeys"):
				t.Errorf("%s holds %s: nothing in the deployer mints or signs anything", id, p)
			}
		}
	}
	// The unconditioned role: no update, delete, run, pause, resume
	// (cloudscheduler.jobs.enable), setIamPolicy or actAs, and among storage
	// permissions exactly the one creation.
	for _, why := range unconditionedDeployerViolations(RolePermissions[DeployerRole]) {
		t.Error(why)
	}
	// The conditioned roles together: exactly the nine storage permissions of
	// D75, and no secretmanager.versions.*, which would make a secret value
	// readable.
	var storage []string
	for _, id := range DeployerServiceRoles() {
		for _, p := range RolePermissions[id] {
			if strings.HasPrefix(p, "storage.") {
				storage = append(storage, p)
			}
			if strings.HasPrefix(p, "secretmanager.versions.") {
				t.Errorf("%s holds %s: a managed plan creates a container and never reads a version", id, p)
			}
		}
	}
	want := []string{"storage.buckets.delete", "storage.buckets.get", "storage.buckets.setIamPolicy", "storage.buckets.update",
		"storage.objects.create", "storage.objects.delete", "storage.objects.get", "storage.objects.list", "storage.objects.update"}
	slices.Sort(storage)
	if !slices.Equal(storage, want) {
		t.Errorf("the conditioned roles hold the storage permissions %v, want the nine D75 names %v", storage, want)
	}
}

// d75ScopedPermissions is D75's scoped list, spelled out here independently of
// the seven roles: the redesign partitions this list, it does not rewrite it.
var d75ScopedPermissions = []string{
	"apikeys.keys.delete", "apikeys.keys.update",
	"artifactregistry.repositories.delete", "artifactregistry.repositories.setIamPolicy", "artifactregistry.repositories.update",
	"cloudscheduler.jobs.delete", "cloudscheduler.jobs.enable", "cloudscheduler.jobs.pause", "cloudscheduler.jobs.run", "cloudscheduler.jobs.update",
	"iam.serviceAccounts.actAs", "iam.serviceAccounts.delete", "iam.serviceAccounts.setIamPolicy", "iam.serviceAccounts.update",
	"run.jobs.delete", "run.jobs.run", "run.jobs.runWithOverrides", "run.jobs.setIamPolicy", "run.jobs.update",
	"secretmanager.secrets.delete", "secretmanager.secrets.setIamPolicy", "secretmanager.secrets.update",
	"storage.buckets.delete", "storage.buckets.get", "storage.buckets.setIamPolicy", "storage.buckets.update",
	"storage.objects.create", "storage.objects.delete", "storage.objects.get", "storage.objects.list", "storage.objects.update",
}

// deployerServiceAPI is each conditioned role's own API: every permission of
// the role starts with it, and no other role's does. This is what keeps a
// condition that is too wide for one service from reaching another service's
// permissions — the roles are the boundary the conditions cannot cross.
var deployerServiceAPI = map[string]string{
	DeployerStorageRole:    "storage.",
	DeployerSecretRole:     "secretmanager.",
	DeployerAccountRole:    "iam.",
	DeployerAPIKeyRole:     "apikeys.",
	DeployerRepositoryRole: "artifactregistry.",
	DeployerRunJobRole:     "run.",
	DeployerSchedulerRole:  "cloudscheduler.",
}

// The seven conditioned roles partition D75's 31 scoped permissions: each in
// exactly one of them, none added, none dropped, none also in the unconditioned
// role, and every role holding one service's API and no other's. This is what
// makes the redesign a change of SHAPE and not a change of what the deployer
// may do — and it is the test that fails when a permission is moved between
// services to make one condition easier to write.
func TestDeployerScopedRolesAreD75Partitioned(t *testing.T) {
	if len(deployerServiceAPI) != len(DeployerServiceRoles()) {
		t.Fatalf("deployerServiceAPI names %d roles, the deployer has %d", len(deployerServiceAPI), len(DeployerServiceRoles()))
	}
	owner := map[string]string{}
	for _, id := range DeployerServiceRoles() {
		api, ok := deployerServiceAPI[id]
		if !ok {
			t.Errorf("%s has no API this test knows", id)
			continue
		}
		for _, p := range deployerRoleGolden[id] {
			if !strings.HasPrefix(p, api) {
				t.Errorf("%s holds %s, which is not one of its own API's permissions (%s)", id, p, api)
			}
			if prev, dup := owner[p]; dup {
				t.Errorf("%s and %s both hold %s: a permission in two roles is a permission under two conditions, and only one of them was reasoned about", prev, id, p)
				continue
			}
			owner[p] = id
		}
	}
	for _, p := range d75ScopedPermissions {
		if _, ok := owner[p]; !ok {
			t.Errorf("D75 gives the conditioned half %s, and no role holds it", p)
		}
	}
	for p, id := range owner {
		if !slices.Contains(d75ScopedPermissions, p) {
			t.Errorf("%s holds %s, which is not in D75's scoped list", id, p)
		}
		if slices.Contains(RolePermissions[DeployerRole], p) {
			t.Errorf("%s holds %s and so does %s: a permission the fence is meant to limit cannot also be granted with no condition", DeployerRole, p, id)
		}
	}
}

// TestDeployerPermissionCountsAreTheDocumentedNumbers pins the two numbers the
// design's R3 states to the lists the tree holds, because R3's claim is that the
// redesign neither added, removed nor moved a permission between the two halves:
// a count that drifted from the lists leaves that claim unreadable, and an
// earlier revision of the sentence stated the unconditioned half as 25 where
// every tree that carries it holds 26 — this map, the module's fugaroDeployer
// role and the golden plan.
func TestDeployerPermissionCountsAreTheDocumentedNumbers(t *testing.T) {
	unconditioned := len(RolePermissions[DeployerRole])
	conditioned := 0
	for _, role := range DeployerServiceRoles() {
		conditioned += len(RolePermissions[role])
	}
	if unconditioned != len(deployerRoleGolden[DeployerRole]) || conditioned != len(d75ScopedPermissions) {
		t.Fatalf("the unconditioned role holds %d permissions here and %d in the golden plan; the seven conditioned roles hold %d here and D75's scoped list holds %d: R3 is stated against one list and this test reads the other",
			unconditioned, len(deployerRoleGolden[DeployerRole]), conditioned, len(d75ScopedPermissions))
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "design", "managed-deployer-fence.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(b)), " ")
	want := fmt.Sprintf("The %d + %d permissions are D75's", unconditioned, conditioned)
	if !strings.Contains(doc, want) {
		t.Errorf("docs/design/managed-deployer-fence.md's R3 does not state %q: the tree holds %d permissions in %s and %d across the seven conditioned roles, and R3's claim is that no permission was added, removed or moved between the halves", want, unconditioned, DeployerRole, conditioned)
	}
}

// deployerOperators is the number of logical operators IAM counts in a
// condition expression: every && and every || is one. IAM allows 12, and the
// 0.8.6 shape had 17 — TestDeployerOperatorCountMatchesTheRecordedDefect pins
// this count to that recorded number, so a change of counting shows up as a
// failure here rather than silently making the limit easier to pass.
func deployerOperators(expr string) int {
	return strings.Count(expr, " && ") + strings.Count(expr, " || ")
}

// deployerOldCondition is the 0.8.6 expression, clause for clause: the one
// deployer.tf's header records as having 17 logical operators, a
// resource.name.contains() and five types resource.name does not support. It is
// here to be counted, not to be used.
var deployerOldCondition = strings.Join([]string{
	`(resource.type == "storage.googleapis.com/Bucket" && resource.name.startsWith("projects/_/buckets/fmgd-"))`,
	`(resource.type == "storage.googleapis.com/Object" && resource.name.startsWith("projects/_/buckets/fmgd-"))`,
	`(resource.type == "secretmanager.googleapis.com/Secret" && resource.name.contains("/secrets/fmgd-"))`,
	`(resource.type == "secretmanager.googleapis.com/SecretVersion" && resource.name.contains("/secrets/fmgd-"))`,
	`(resource.type == "artifactregistry.googleapis.com/Repository" && resource.name.contains("/repositories/fmgd-"))`,
	`(resource.type == "run.googleapis.com/Job" && resource.name.contains("/jobs/fmgd-"))`,
	`(resource.type == "cloudscheduler.googleapis.com/Job" && resource.name.contains("/jobs/fmgd-"))`,
	`(resource.type == "iam.googleapis.com/ServiceAccount" && resource.name.contains("/serviceAccounts/fmgd-"))`,
	`(resource.type == "apikeys.googleapis.com/Key" && resource.name.contains("/keys/fmgd-"))`,
}, " || ")

func TestDeployerOperatorCountMatchesTheRecordedDefect(t *testing.T) {
	if n := deployerOperators(deployerOldCondition); n != 17 {
		t.Fatalf("the 0.8.6 condition counts as %d logical operators, and deployer.tf's header records 17: this count is what the split is measured against", n)
	}
}

// TestDeployerOperatorCountsAreTheDocumentedNumbers pins what each of the seven
// redesigned conditions counts as, under the same convention as the 17 above
// (&& and || alike): one operator for each single-clause condition — the && its
// clause joins the type term to the name test with — and three for Cloud
// Storage, whose two clauses carry two && and the || between them.
//
// This is the number Check 68's C1 step expects and the number
// docs/design/managed-projects.md states, and they are the same number or the
// live check measures a different expression from the one the tree writes. A
// reader who counts the operator only when it joins two clauses reads these
// conditions as 0 and 1, which is not the count IAM refuses on and not the count
// deployerOperators makes.
func TestDeployerOperatorCountsAreTheDocumentedNumbers(t *testing.T) {
	want := map[string]int{DeployerStorageRole: 3}
	for _, role := range DeployerServiceRoles() {
		if _, ok := want[role]; !ok {
			want[role] = 1
		}
	}
	svcs := deployerServices(testDeployerProject)
	if len(svcs) != len(want) {
		t.Fatalf("the deployer has %d conditioned roles, and this test pins %d counts", len(svcs), len(want))
	}
	multi := 0
	for _, s := range svcs {
		n, ok := want[s.Role]
		if !ok {
			t.Errorf("%s: no operator count is pinned for this role", s.Role)
			continue
		}
		if got := deployerOperators(s.Expression()); got != n {
			t.Errorf("%s: %d logical operators in %q, want %d", s.Role, got, s.Expression(), n)
		}
		if len(s.Clauses) > 1 {
			multi++
			if s.Role != DeployerStorageRole || n != 3 {
				t.Errorf("%s: the only condition with more than one clause is Cloud Storage's, and this test pins %d operators for %s", s.Role, n, s.Role)
			}
		}
	}
	if multi != 1 {
		t.Errorf("%d conditions carry more than one clause, and this test expects Cloud Storage's alone: the pinned counts are for one two-clause condition and six single-clause ones", multi)
	}
}

// deployerNameFunctions are the resource.name functions IAM supports.
// resource.name.contains() is not one of them, and neither is any other call.
var deployerNameFunctions = []string{"startsWith", "endsWith", "extract"}

// deployerExtractAnchors is the literal text each extract template must anchor
// at, per service: the collection segment immediately before the resource's own
// name. Nothing else may be in the template — the resource's own segment is
// what the prefix is tested on, and a template that reached further into the
// name would hand the wrong text to that test.
var deployerExtractAnchors = map[string]string{
	DeployerSecretRole:     "/secrets/",
	DeployerAPIKeyRole:     "/keys/",
	DeployerRepositoryRole: "/repositories/",
	DeployerRunJobRole:     "/jobs/",
	DeployerSchedulerRole:  "/jobs/",
}

// The extract templates are the form the design's C6 records and nothing else.
//
// The design reads the IAM Conditions resource-attribute reference as giving
// resource.name.extract a template that carries ONE placeholder, written
// {name}, with literal text around it, and returning the text that placeholder
// matched — and it records that reading as UNVERIFIED, because no source in this
// tree quotes the reference's text for it (docs/design/managed-deployer-fence.md,
// "What this document does not claim"). The first cut of this fence used a
// multi-placeholder template — extract("projects/{project}/secrets/{secret}") —
// which is not that form, and the failure it invites is the 0.8.6 outage again:
// setIamPolicy refuses the expression, and the owner-run apply that creates the
// whole installation stops. It is also the form whose result the tree can model
// honestly (extractSegment), rather than an undocumented "last segment"
// behaviour.
//
// Structural, and deliberately so: it reads the templates the fence is built
// from, in Go and (through TestDeployerBindingsAreTheModules) in the module, so
// a template that grows a second placeholder is a test failure here rather than
// an apply that never completes.
func TestDeployerExtractTemplatesAreTheRecordedForm(t *testing.T) {
	// The recorded shape: literal text, one {placeholder}, literal text.
	form := regexp.MustCompile(`^([^{}]*)\{([A-Za-z_][A-Za-z0-9_]*)\}([^{}]*)$`)
	svcs := deployerServices(testDeployerProject)
	seen := map[string]bool{}
	for _, s := range svcs {
		for _, c := range s.Clauses {
			if c.Op != "extract" {
				continue
			}
			seen[s.Role] = true
			anchor, want := deployerExtractAnchors[s.Role]
			if !want {
				t.Errorf("%s: uses extract, and this test names no anchor for it: an extract template has to be the collection segment the resource's own name follows", s.Role)
				continue
			}
			if n := strings.Count(c.Value, "{"); n != 1 {
				t.Errorf("%s: extract template %q carries %d placeholders, and the form C6 records carries exactly one", s.Role, c.Value, n)
			}
			if n := strings.Count(c.Value, "}"); n != 1 {
				t.Errorf("%s: extract template %q carries %d closing braces, and the form C6 records carries exactly one placeholder", s.Role, c.Value, n)
			}
			m := form.FindStringSubmatch(c.Value)
			if m == nil {
				t.Errorf("%s: extract template %q is not literal text around one {placeholder}", s.Role, c.Value)
				continue
			}
			if m[1] != anchor || m[3] != "" {
				t.Errorf("%s: extract template %q is not %q followed by one placeholder and nothing else: the extracted text is the resource's own name, and a template that reached further into the name (or stopped short of it) would test the prefix against the wrong text", s.Role, c.Value, anchor+"{name}")
			}
		}
	}
	for role, anchor := range deployerExtractAnchors {
		if !seen[role] {
			t.Errorf("%s: no clause uses extract, and this test expects the %q template", role, anchor+"{name}")
		}
	}
	// The template the first cut wrote is the one that must not come back, and
	// the helper that models the recorded reading says so: it extracts
	// nothing from a multi-placeholder template, so such a fence admits nothing
	// even if IAM were to accept it.
	if seg := extractSegment("projects/{project}/secrets/{secret}", "projects/12/secrets/fmgd-x"); seg != "" {
		t.Errorf("extractSegment(multi-placeholder template) = %q, want \"\": a template that is not the form C6 records matches nothing", seg)
	}
}

// TestDeployerExtractRuleIsRecordedAsUnverified holds the extract reading to the
// label it can support.
//
// The round-5 review found the design's C6 and C7 reclassified from UNVERIFIED
// to settled on a citation chain that reaches no source: deployer.tf's header
// stated the rule without naming a document, Task 1.5 cites the reference for
// the services' *name shapes* and never for the grammar, and extract( appears
// nowhere else in the tree. `extractSegment` — the helper below — is this
// repository's reimplementation, not evidence for it. The reading is also
// unfalsifiable by any later run: every conditioned type's real names carry
// their own clause's anchor, so no live step can construct a non-match (Check
// 68's non-match probe was dropped on the strength of the settled label). An
// uncited reading that nothing can measure is exactly what UNVERIFIED is for, so
// this test reads the three files that carry the claim and refuses the settled
// wording in all of them.
func TestDeployerExtractRuleIsRecordedAsUnverified(t *testing.T) {
	const designPath = "docs/design/managed-deployer-fence.md"
	docBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "design", "managed-deployer-fence.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(docBytes)
	row := map[string]string{}
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| C6 |") || strings.HasPrefix(line, "| C7 |") {
			row[strings.TrimSpace(line[2:4])] = line
		}
	}
	for _, fact := range []string{"C6", "C7"} {
		line, ok := row[fact]
		if !ok {
			t.Fatalf("%s's §3 table has no %s row: the label this test pins lives there, and a row that disappears takes the label with it", designPath, fact)
		}
		// The status cell, not the row: a row can mention UNVERIFIED in its
		// prose while its label says something else, which is the state the
		// round-5 review found these two rows in.
		cells := strings.Split(line, "|")
		if len(cells) < 5 {
			t.Fatalf("%s's %s row is not a four-cell table row: %q", designPath, fact, line)
		}
		status := strings.TrimSpace(cells[4])
		if !strings.HasPrefix(status, "**UNVERIFIED") {
			t.Errorf("%s's %s row carries the status %q: the grammar and the first-occurrence rule are this repository's reading of the reference — no source in the tree quotes it and extractSegment is our own model — so the row records a reading rather than a citation, and its label has to say so", designPath, fact, status)
		}
	}
	if !strings.Contains(doc, "no source in this tree quotes") {
		t.Errorf("%s does not record where the extract grammar's citation stands: the review that reclassified C6 and C7 could name no source, and a reader has to be able to tell a citation from a reading", designPath)
	}
	if strings.Contains(doc, "C7 is not one of them") {
		t.Errorf("%s's opening note says C7 has left the UNVERIFIED rows: no source in this tree quotes the reference's text for the grammar, and no live step can falsify the reading, so the label stays on", designPath)
	}
	// Every file that states the rule: each may state the reading, and none may
	// promote it to a citation. The phrases are the ones the settled label was
	// written in. docs/design/managed-projects.md is here because the round-5
	// revert left its 0.9.6 note saying C7 was "settled by the documented
	// semantics" — a security-relevant reading the design of record holds
	// UNVERIFIED — and a scan that stopped at the module and this package could
	// not see it.
	for _, path := range []string{
		filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules", "installation", "deployer.tf"),
		filepath.Join("..", "..", "..", "docs", "design", "managed-projects.md"),
		filepath.Join("..", "..", "..", "SECURITY.md"),
		filepath.Join("..", "..", "..", "website", "content", "security.md"),
		"cover.go",
	} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(b)), " ")
		for _, phrase := range []string{
			"documented semantics",
			"documented form",
			"the reference documents",
			"is settled that way",
		} {
			if strings.Contains(text, phrase) {
				t.Errorf("%s states the extract rule as %q: the reading is uncited — no source in this tree quotes the reference's text — and the design's C6/C7 rows carry UNVERIFIED for that reason", path, phrase)
			}
		}
	}
	// And the module states the label where the fence's rationale is read, so an
	// owner-run apply is not made on the strength of a citation that does not
	// exist.
	modBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules", "installation", "deployer.tf"))
	if err != nil {
		t.Fatal(err)
	}
	mod := strings.Join(strings.Fields(string(modBytes)), " ")
	if !strings.Contains(mod, "UNVERIFIED") {
		t.Error("deployer.tf's header states the extract rule without recording that the design holds it UNVERIFIED: this file is what the owner reads before the apply that creates the fence, and the rule it states is a reading, not a citation")
	}
}

// TestDeployerDesignNamesTheModelFields holds §6's description of the Go model
// to the model itself.
//
// The sentence read "models this as a `DeployerService` per row — role, title,
// clauses", and there is no title in the struct: the condition title is one
// shared constant, `DeployerConditionTitle`, for every binding — as the
// paragraph directly above the sentence says, because an operator reading the
// project's IAM policy should see one fence and not seven names. A design that
// names a field the struct does not have sends a reader looking for a
// per-service title the module deliberately does not write. The field list is
// read off the type rather than written out here, so a field added to the
// struct and not to the sentence fails here too.
//
// Mutation (verified, run and restored): put "title" back into the sentence —
// this fails naming both lists.
func TestDeployerDesignNamesTheModelFields(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "design", "managed-deployer-fence.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(b)), " ")
	const lead = "models this as a `DeployerService` per row — "
	i := strings.Index(doc, lead)
	if i < 0 {
		t.Fatalf("the design does not describe the model as %q: this test reads the field list from there", strings.TrimSpace(lead))
	}
	rest := doc[i+len(lead):]
	j := strings.Index(rest, " —")
	if j < 0 {
		t.Fatalf("the design's description of DeployerService does not close its field list: %q", rest)
	}
	listed := strings.Split(rest[:j], ", ")
	var fields []string
	typ := reflect.TypeOf(DeployerService{})
	for k := 0; k < typ.NumField(); k++ {
		fields = append(fields, strings.ToLower(typ.Field(k).Name))
	}
	if !slices.Equal(listed, fields) {
		t.Errorf("the design lists the DeployerService fields as %v, and the struct's are %v: the condition title is the one shared constant DeployerConditionTitle and not a field, so the sentence has to describe the type that exists", listed, fields)
	}
	if !strings.Contains(doc, "DeployerConditionTitle") {
		t.Error("the design does not name DeployerConditionTitle: with no title field in the struct, the shared constant is the only place the title is written, and a reader has to be told which it is")
	}
}

// TestDeployerDesignSaysWhatTheCoverEnforces: §6 of the design used to say the
// cover gate refuses "a second binding of any of the seven". It does not —
// deployerGrant reads each binding's role, member, project and condition on its
// own, so a second google_project_iam_member of the same role under the
// byte-identical condition is accepted (harmless: IAM members are a set). The
// single-binding property is the module tree's, and TestDeployerBindingsAreTheModules
// is what holds it. A design sentence that credits the gate with a property the
// gate does not have is the kind of claim a later change reasons from, so this
// reads the two sentences that carry it.
//
// Mutation (verified, run and restored): put the old clause back into the
// design — this fails naming it.
func TestDeployerDesignSaysWhatTheCoverEnforces(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "design", "managed-deployer-fence.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(b)), " ")
	if strings.Contains(doc, "a second binding of any of the seven are all refused") {
		t.Error("the design says the cover gate refuses a second binding of any of the seven: deployerGrant reads each binding independently, so a second binding of the same role under the byte-identical condition is accepted, and the single-binding property is TestDeployerBindingsAreTheModules' (the module tree's), not the plan review's")
	}
	if !strings.Contains(doc, "TestDeployerBindingsAreTheModules") {
		t.Error("the design does not name TestDeployerBindingsAreTheModules as what holds the one-binding-per-role property: the cover gate does not hold it, so a reader told otherwise has nowhere to look for the rule")
	}
	if !strings.Contains(doc, "harmless because IAM members are a set") {
		t.Error("the design does not say why a second identical binding being accepted is harmless: without it the sentence reads as an unclosed hole rather than as IAM's own set semantics")
	}
}

// The module's own header states the reading the rest of the tree states.
//
// deployer.tf is the file the plan is made from, so its comment on the extract
// form is the fence's stated rationale: it is what the owner reads to decide
// whether the fence is the form the design records — a form the design marks
// UNVERIFIED, because the repository holds the grammar by its own reading rather
// than by a citation. It said the opposite of that reading once — "extract tests
// the resource's own LAST segment" — twelve
// lines above the paragraph arguing the rule, and a header that
// states two readings states none.
//
// What this pins is the reading rather than the wording: the header names the
// FIRST occurrence of the template's literal prefix, and it names the "last
// segment" reading only to deny it.
func TestDeployerHeaderStatesTheExtractionRule(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules", "installation", "deployer.tf"))
	if err != nil {
		t.Fatal(err)
	}
	// The header's comments with their markers and wrapping removed: a sentence
	// that wraps across comment lines still reads as one sentence, and the
	// header is prose rather than configuration.
	var prose []string
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "#") {
			continue
		}
		prose = append(prose, strings.TrimSpace(strings.TrimPrefix(s, "#")))
	}
	header := strings.Join(strings.Fields(strings.Join(prose, " ")), " ")
	if !strings.Contains(header, "the FIRST occurrence of the template's literal prefix") {
		t.Error("deployer.tf's header does not state the extraction rule the design's C6 records (the text after the FIRST occurrence of the template's literal prefix): the module is where the fence's rationale is read, and the reading defines the extraction by occurrence")
	}
	// The reading the first cut took, which the design records no rule for: the
	// header may name it only to deny it, so the denial is removed before the
	// rest is searched.
	rest := strings.ReplaceAll(header, `not a "last segment" reading`, "")
	if m := regexp.MustCompile(`(?i)last segment`).FindString(rest); m != "" {
		t.Errorf("deployer.tf's header states the %q reading, and the design records no such rule: the header has to state one reading, and it is the one the design's C7 and this package's extractSegment model", m)
	}
	// And the reason the five services take the extract form, which is the
	// header's other half: without it the header reads as a preference rather
	// than as the plan-time fact it is.
	if !strings.Contains(header, "other five services need it because") {
		t.Error("deployer.tf's header does not give the reason the five services take the extract form")
	}
}

// extractSegment is what resource.name.extract(template) returns for name under
// the reading the design's C6 records: the template carries one {placeholder}
// with literal text around it, and the call returns the text the placeholder
// matched — which that reading defines by OCCURRENCE: with a prefix-only
// template, the characters after the FIRST occurrence of the prefix, and with a
// suffix too, the characters between that first occurrence of the prefix and the
// first SUBSEQUENT occurrence of the suffix. **C6 marks that reading UNVERIFIED**
// — no source in this tree quotes the reference's text for the grammar, so this
// helper is the repository's reading written down, not evidence for it. This is
// the whole of the rule as it bears on this fence: the first occurrence of each
// literal, and not the last or the only one, and a suffix that ends the extracted
// text rather than the name.
//
// A name that does not carry the template's literal prefix — or, when the
// template has a suffix, one that does not carry it after that prefix — matches
// nothing, and the result is the empty string, which the condition's
// .startsWith("fmgd-") reads as false, so a non-match denies under the same
// reading (C7, likewise UNVERIFIED and equally unmeasurable live: the empty
// string does not start with the prefix).
//
// This is the model the rest of these tests read the fence through. It is
// deliberately not "the resource's last segment": that reading is what the
// first cut assumed, and it is not the reading C6 records. What makes the
// run-to-the-end result the resource's own name is the type term beside it —
// a resource of the clause's own type has its own name as the last segment, and
// a longer name (a version under a secret, an execution under a job) is another
// type, which no clause of that service names.
//
// A template that is not the recorded form — no placeholder, an unterminated
// one, or more than one — returns "" here: the reading gives no result for it at
// all, so this helper refuses to guess at a template it does not describe. (Such
// a template is refused outright by
// TestDeployerExtractTemplatesAreTheRecordedForm; this is the second line of
// that defence, not the first.)
func extractSegment(template, name string) string {
	if strings.Count(template, "{") != 1 || strings.Count(template, "}") != 1 {
		return ""
	}
	open := strings.Index(template, "{")
	closing := strings.Index(template, "}")
	if open < 0 || closing < open {
		return ""
	}
	pre, post := template[:open], template[closing+1:]
	rest := name
	if pre != "" {
		i := strings.Index(name, pre)
		if i < 0 {
			return ""
		}
		rest = name[i+len(pre):]
	}
	if post != "" {
		i := strings.Index(rest, post)
		if i < 0 {
			return ""
		}
		rest = rest[:i]
	}
	return rest
}

// The helper above is the tree's model of the reading the design's C6 records,
// so it is tested as a model: the three shapes a template can take, the
// non-matches that have to deny, and the near misses that must not be admitted by
// a prefix test on the wrong text. The name says "the recorded reading" rather
// than "the documented semantics" on purpose: C6 carries UNVERIFIED, and a test
// name that asserts a citation the repository cannot produce is the same defect
// the design was fixed for.
func TestExtractSegmentModelsTheRecordedReading(t *testing.T) {
	cases := []struct {
		name, template, resource, want string
	}{
		{"the secret's own name", "/secrets/{secret}", "projects/12/secrets/fmgd-x", "fmgd-x"},
		{"a name without the anchor", "/secrets/{secret}", "projects/12/locations/us-east5/jobs/fmgd-x", ""},
		// A version's name carries the anchor too, so extract returns the rest
		// of the name — and it is the type term beside the clause, not the
		// name test, that keeps a version out: no clause names
		// secretmanager.googleapis.com/SecretVersion.
		{"a version under the secret", "/secrets/{secret}", "projects/12/secrets/fmgd-x/versions/1", "fmgd-x/versions/1"},
		{"another secret under the anchor", "/secrets/{secret}", "projects/12/secrets/other/versions/fmgd-x", "other/versions/fmgd-x"},
		{"the anchor in the container", "/secrets/{secret}", "projects/fmgd-/secrets/other", "other"},
		{"a literal suffix", "/jobs/{job}/executions", "projects/12/locations/l/jobs/fmgd-x/executions", "fmgd-x"},
		{"the suffix missing", "/jobs/{job}/executions", "projects/12/locations/l/jobs/fmgd-x", ""},
		// The suffix does not have to END the name: the reading C6 records says the
		// text runs from the prefix's first occurrence to the FIRST SUBSEQUENT
		// occurrence of the suffix, so what follows that occurrence is outside
		// the extracted text.
		{"text after the suffix", "/jobs/{job}/executions", "projects/12/locations/l/jobs/fmgd-x/executions/e1", "fmgd-x"},
		{"the suffix twice", "/jobs/{job}/executions", "projects/12/locations/l/jobs/fmgd-x/executions/executions", "fmgd-x"},
		{"a whole-name template", "{name}", "projects/_/buckets/fmgd-x", "projects/_/buckets/fmgd-x"},
		{"two placeholders", "projects/{project}/secrets/{secret}", "projects/12/secrets/fmgd-x", ""},
		{"no placeholder at all", "/secrets/fmgd-", "projects/12/secrets/fmgd-x", ""},
		{"an unterminated placeholder", "/secrets/{secret", "projects/12/secrets/fmgd-x", ""},
		// The anchor twice: the reading C6 records names the occurrence — the FIRST one
		// — so the extracted text is what follows the first /jobs/ and runs to
		// the end of the name. It does not start with the prefix, so the clause
		// denies; what the fence reads here is C6's rule and not a
		// guess about an ambiguous name.
		{"the anchor twice", "/jobs/{job}", "projects/12/jobs/x/jobs/fmgd-x", "x/jobs/fmgd-x"},
		// And the shape the same rule admits, which is worth naming rather than
		// leaving to be discovered: with the prefix after the FIRST anchor the
		// extracted text begins with it. No resource of these five services can
		// be named that way — a secret id, an API key's uid, a repository id and
		// a job id carry no path separator — so the fence never sees one, and
		// the model reads C6's rule instead of the stricter "the anchor
		// exactly once" rule it used to apply.
		{"the anchor twice, prefix after the first", "/jobs/{job}", "projects/12/jobs/fmgd-a/jobs/x", "fmgd-a/jobs/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractSegment(tc.template, tc.resource); got != tc.want {
				t.Errorf("extractSegment(%q, %q) = %q, want %q", tc.template, tc.resource, got, tc.want)
			}
		})
	}
	// And what the fence does with it: the extracted text is tested with the
	// managed prefix, so the only names that pass are the ones whose own
	// segment begins with it.
	prefix := ManagedPrefix
	for _, tc := range []struct {
		template, resource string
		admits             bool
	}{
		{"/secrets/{secret}", "projects/12/secrets/fmgd-x", true},
		{"/secrets/{secret}", "projects/12/secrets/fugaro-token-signer", false},
		{"/secrets/{secret}", "projects/12/secrets/x-fmgd-y", false},
		{"/jobs/{job}", "projects/12/locations/us-east5/jobs/xfmgd-y", false},
		{"/jobs/{job}", "projects/12/locations/us-east5/jobs/fmgd-x", true},
	} {
		seg := extractSegment(tc.template, tc.resource)
		if got := strings.HasPrefix(seg, prefix); got != tc.admits {
			t.Errorf("extract(%q) on %s = %q: admits=%v, want %v", tc.template, tc.resource, seg, got, tc.admits)
		}
	}
}

// Every one of the seven conditions is inside what IAM accepts, structurally:
// at most 12 logical operators, only supported resource.name functions, no
// contains() anywhere, a resource.type term in every clause, and exactly the
// eight types D75's scoped permissions reach — each fenced by one service and
// no other.
// The module's own text is pinned to these clauses by
// TestDeployerBindingsAreTheModules, so a loosening here is a loosening there.
func TestDeployerConditionsAreWithinWhatIAMAccepts(t *testing.T) {
	svcs := deployerServices(testDeployerProject)
	if len(svcs) != 7 {
		t.Fatalf("the deployer has %d conditioned roles, want one per service", len(svcs))
	}
	// The eight types D75's scoped permissions reach: no more, no fewer, and
	// each in exactly one service's condition. Secret Manager's VERSION type is
	// not one of them: every secretmanager permission in the deployer is on a
	// secret, and secretmanager.versions.* is nowhere in it (the role pin
	// refuses it), so a version type would be a clause no permission could use.
	d75Types := []string{
		"storage.googleapis.com/Bucket", "storage.googleapis.com/Object",
		"secretmanager.googleapis.com/Secret",
		"artifactregistry.googleapis.com/Repository",
		"run.googleapis.com/Job", "cloudscheduler.googleapis.com/Job",
		"iam.googleapis.com/ServiceAccount", "apikeys.googleapis.com/Key",
	}
	seen := map[string]string{}
	fnRE := regexp.MustCompile(`resource\.name\.([a-zA-Z]+)\(`)
	for _, s := range svcs {
		if len(s.Clauses) == 0 {
			t.Errorf("%s has no clause, so its condition admits nothing or is not written", s.Role)
			continue
		}
		expr := s.Expression()
		if n := deployerOperators(expr); n > 12 {
			t.Errorf("%s: %d logical operators in %q, and IAM allows 12", s.Role, n, expr)
		}
		if strings.Contains(expr, "contains(") {
			t.Errorf("%s: the condition calls contains(), which is not a resource.name function IAM supports: %q", s.Role, expr)
		}
		for _, m := range fnRE.FindAllStringSubmatch(expr, -1) {
			if !slices.Contains(deployerNameFunctions, m[1]) {
				t.Errorf("%s: the condition calls resource.name.%s(), which is not one of %v: %q", s.Role, m[1], deployerNameFunctions, expr)
			}
		}
		for _, c := range s.Clauses {
			if c.Type == "" || !strings.Contains(c.Type, "/") {
				t.Errorf("%s: clause %+v carries no resource.type, so it applies to every type the role reaches", s.Role, c)
			}
			if !slices.Contains(d75Types, c.Type) {
				t.Errorf("%s: clause %+v names a type D75's scoped permissions do not reach", s.Role, c)
			}
			if prev, dup := seen[c.Type]; dup {
				t.Errorf("%s and %s both fence %s: one type, one condition, or a clause can reach another service's resources", prev, s.Role, c.Type)
			}
			seen[c.Type] = s.Role
			switch c.Op {
			case "startsWith":
				if c.Starts != "" {
					t.Errorf("%s: startsWith clause %+v carries an extract-only field", s.Role, c)
				}
			case "extract":
				if c.Value == "" || c.Starts == "" {
					t.Errorf("%s: extract clause %+v has no template or nothing to test the extracted segment with", s.Role, c)
				}
			default:
				t.Errorf("%s: clause %+v uses the operator %q, and the split fence uses startsWith or extract", s.Role, c, c.Op)
			}
		}
	}
	for _, typ := range d75Types {
		if _, ok := seen[typ]; !ok {
			t.Errorf("no condition fences %s, and D75's scoped permissions reach it", typ)
		}
	}
}

// The conditions are the whole fence, so they are tested as one, service by
// service: every clause names the resource type it admits and tests the managed
// prefix at the resource's own name, and no clause — under either form — can
// match one of Fugaro's own resources or the two state buckets.
//
// A clause is read as IAM reads it, (resource.type == T && name test), so every
// name below is offered with the type Google reports for it: the type term is
// half the condition and the half that keeps a LONGER name — a version under a
// secret, an execution under a job — out of reach. Under the extract reading
// C6 and C7 record, the parent's template matches such a name too and extracts a text
// that begins with the prefix (extractSegment returns "fmgd-x/versions/1" for
// the secret's own template), so what excludes it is the type the resource
// itself reports, which no clause names, together with the permissions such a
// resource would need (secretmanager.versions.* is not in the deployer at all;
// the role pin refuses it).
func TestDeployerConditionFencesOnlyTheManagedPrefix(t *testing.T) {
	svcs := deployerServices(testDeployerProject)
	type resource struct{ typ, name string }
	// Fugaro's own names: what the fence must not admit, whatever a plan does.
	forbidden := []resource{
		{"storage.googleapis.com/Bucket", "projects/_/buckets/fugaro-runs-proj-1"},
		{"storage.googleapis.com/Object", "projects/_/buckets/fugaro-runs-proj-1/objects/fugaro/managed.json"},
		{"storage.googleapis.com/Object", "projects/_/buckets/fugaro-managed-tfstate-proj-1/objects/fugaro/managed.tfstate"},
		{"storage.googleapis.com/Object", "projects/_/buckets/fugaro-tfstate-proj-1/objects/fugaro/installation/default.tfstate"},
		{"run.googleapis.com/Job", "projects/12/locations/us-east5/jobs/fugaro-history"},
		{"run.googleapis.com/Job", "projects/12/locations/us-east5/jobs/check"},
		{"cloudscheduler.googleapis.com/Job", "projects/12/locations/us-east4/jobs/fugaro-history-rollover"},
		{"secretmanager.googleapis.com/Secret", "projects/12/secrets/fugaro-token-signer"},
		{"artifactregistry.googleapis.com/Repository", "projects/12/locations/us-east5/repositories/fugaro-base"},
		{"iam.googleapis.com/ServiceAccount", "projects/" + testDeployerProject + "/serviceAccounts/fugaro-scheduler@" + testDeployerProject + ".iam.gserviceaccount.com"},
		{"iam.googleapis.com/ServiceAccount", "projects/" + testDeployerProject + "/serviceAccounts/1234567890-compute@developer.gserviceaccount.com"},
		{"apikeys.googleapis.com/Key", "projects/12/locations/global/keys/fugaro-web"},
		// An object carrying the prefix in a bucket that does not: the runs
		// bucket's objects are reached by bucket name, never by object name
		// (a clause testing the prefix anywhere in a storage name admits it).
		{"storage.googleapis.com/Object", "projects/_/buckets/fugaro-runs-x/objects/fmgd-y"},
		{"storage.googleapis.com/Object", "projects/_/buckets/fugaro-runs-proj-1/objects/runs/fmgd-y/state.json"},
		{"storage.googleapis.com/Object", "projects/_/buckets/fugaro-tfstate-proj-1/objects/fmgd-/default.tfstate"},
		{"storage.googleapis.com/Bucket", "projects/_/buckets/x-fmgd-"},
		// A name whose segment merely CONTAINS the prefix, in the services that
		// test a segment: the prefix is what the segment begins with.
		{"secretmanager.googleapis.com/Secret", "projects/12/secrets/x-fmgd-y"},
		{"run.googleapis.com/Job", "projects/12/locations/us-east5/jobs/x-fmgd-y"},
		{"artifactregistry.googleapis.com/Repository", "projects/12/locations/us-east5/repositories/fugaro-fmgd-y"},
		{"iam.googleapis.com/ServiceAccount", "projects/" + testDeployerProject + "/serviceAccounts/x-fmgd-y@" + testDeployerProject + ".iam.gserviceaccount.com"},
		{"apikeys.googleapis.com/Key", "projects/12/locations/global/keys/fugaro-fmgd-y"},
		// A name of one of the five extract services' own types that does not
		// carry its clause's anchor at all — the bare prefix, with no
		// collection path in front of it. No real resource of these services is
		// named that way; the rows are here because this is the direction the
		// reading C6/C7 records has to get right and the direction no live
		// probe can construct (C7): a template that does not match extracts the
		// empty string, and the empty string does not start with the prefix, so
		// the condition denies — it fails closed rather than open. A fence that
		// had stopped testing the anchor would admit these five.
		{"secretmanager.googleapis.com/Secret", ManagedPrefix + "x"},
		{"apikeys.googleapis.com/Key", ManagedPrefix + "x"},
		{"artifactregistry.googleapis.com/Repository", ManagedPrefix + "x"},
		{"run.googleapis.com/Job", ManagedPrefix + "x"},
		{"cloudscheduler.googleapis.com/Job", ManagedPrefix + "x"},
		// The longer names, each under the type its own resource reports: a
		// version under a managed secret, an execution under a managed job, a
		// package under a managed repository. None of those types is one of the
		// eight a clause names, so no condition admits them — and a fence that
		// leaned on the name test alone would admit the first two, which is
		// exactly what this block pins.
		{"secretmanager.googleapis.com/SecretVersion", "projects/12/secrets/fmgd-x/versions/1"},
		{"run.googleapis.com/Execution", "projects/12/locations/us-east5/jobs/fmgd-x/executions/e1"},
		{"artifactregistry.googleapis.com/Package", "projects/12/locations/us-east5/repositories/fmgd-x/packages/p"},
	}
	// admits is the clause as the condition reads it: the name test the clause
	// names, on the name the clause's own shape produces. The type term is
	// applied by the callers, which know which resource they are offering.
	admits := func(c DeployerClause, name string) bool {
		switch c.Op {
		case "startsWith":
			return strings.HasPrefix(name, c.Value)
		case "extract":
			seg := extractSegment(c.Value, name)
			return seg != "" && strings.HasPrefix(seg, c.Starts)
		}
		return true
	}
	for _, s := range svcs {
		for _, c := range s.Clauses {
			// The prefix is tested at the resource's own name, and never as a
			// bare substring: a value that admitted any name containing
			// "fmgd-" would admit a fugaro-* resource in a container whose name
			// carries it. The two forms test it in the two places the name has
			// it — after a path separator for startsWith, at the start of the
			// extracted segment for extract — and "x-fmgd-y" matches neither.
			switch c.Op {
			case "startsWith":
				if !strings.HasSuffix(c.Value, "/"+ManagedPrefix) {
					t.Errorf("%s: clause %+v does not end at %q after a path separator, so a name such as x-%s would match it", s.Role, c, ManagedPrefix, ManagedPrefix)
				}
			case "extract":
				if c.Starts != ManagedPrefix {
					t.Errorf("%s: extract clause %+v tests the segment with %q, want exactly the reserved prefix %q at the start of the segment", s.Role, c, c.Starts, ManagedPrefix)
				}
			}
			// Cloud Storage names put the bucket at the front, so a storage
			// clause tests the bucket's name with startsWith, on the bucket
			// resource path — the form Check 32 proved.
			if strings.HasPrefix(c.Type, "storage.googleapis.com/") && (c.Op != "startsWith" || c.Value != "projects/_/buckets/"+ManagedPrefix) {
				t.Errorf("%s: storage clause %+v is not startsWith(\"projects/_/buckets/%s\")", s.Role, c, ManagedPrefix)
			}
			// The account clause names the project, because var.project is a
			// plan-time literal and a clause that omitted it would admit an
			// account in any project the binding were ever moved to.
			if c.Type == "iam.googleapis.com/ServiceAccount" && (c.Op != "startsWith" || c.Value != "projects/"+testDeployerProject+"/serviceAccounts/"+ManagedPrefix) {
				t.Errorf("%s: the service-account clause %+v is not startsWith(\"projects/%s/serviceAccounts/%s\")", s.Role, c, testDeployerProject, ManagedPrefix)
			}
			for _, f := range forbidden {
				// The type term is what the clause would be evaluated
				// against, so a name of another type is not this clause's
				// business: the condition it is half of denies it on the type
				// term alone.
				if c.Type != f.typ {
					continue
				}
				if admits(c, f.name) {
					t.Errorf("%s: clause %+v admits %s (%s)", s.Role, c, f.name, f.typ)
				}
			}
		}
	}
	// The longer names are out of reach on the type term alone, so the types
	// they report are pinned as types no clause names: a fence that grew a
	// clause for SecretVersion or Execution would admit a version or an
	// execution of a managed resource, and this is where that shows up.
	named := map[string]string{}
	for _, s := range svcs {
		for _, c := range s.Clauses {
			named[c.Type] = s.Role
		}
	}
	for _, typ := range []string{
		"secretmanager.googleapis.com/SecretVersion",
		"run.googleapis.com/Execution",
		"artifactregistry.googleapis.com/Package",
	} {
		if role, ok := named[typ]; ok {
			t.Errorf("%s names %s, and the type term is what keeps a longer name out: a clause for it would admit a resource the deployer's scoped roles are not fenced for", role, typ)
		}
	}
	// And what each condition must admit: its own service's prefixed resource,
	// in that service's own name shape, so a fence that admits nothing cannot
	// pass this test by refusing everything. The project is the NUMBER for the
	// services whose names carry one, and the location is not the
	// installation's — a managed plan chooses it.
	admitted := map[string][]resource{
		DeployerStorageRole: {
			{"storage.googleapis.com/Bucket", "projects/_/buckets/fmgd-x"},
			{"storage.googleapis.com/Object", "projects/_/buckets/fmgd-x/objects/o"},
		},
		// The container alone: a version's name reports another type
		// (secretmanager.googleapis.com/SecretVersion), which no clause names,
		// so no clause admits a version — and no permission of this role acts
		// on one (secretmanager.versions.* is nowhere in the deployer, and the
		// role pin refuses it).
		DeployerSecretRole:     {{"secretmanager.googleapis.com/Secret", "projects/12/secrets/fmgd-x"}},
		DeployerAccountRole:    {{"iam.googleapis.com/ServiceAccount", "projects/" + testDeployerProject + "/serviceAccounts/fmgd-x@" + testDeployerProject + ".iam.gserviceaccount.com"}},
		DeployerAPIKeyRole:     {{"apikeys.googleapis.com/Key", "projects/12/locations/global/keys/fmgd-x"}},
		DeployerRepositoryRole: {{"artifactregistry.googleapis.com/Repository", "projects/12/locations/europe-west1/repositories/fmgd-x"}},
		DeployerRunJobRole:     {{"run.googleapis.com/Job", "projects/12/locations/europe-west1/jobs/fmgd-x"}},
		DeployerSchedulerRole:  {{"cloudscheduler.googleapis.com/Job", "projects/12/locations/us-east4/jobs/fmgd-x"}},
	}
	for _, s := range svcs {
		names, ok := admitted[s.Role]
		if !ok {
			t.Errorf("%s: this test names no resource the condition must admit", s.Role)
			continue
		}
		for _, r := range names {
			ok := false
			for _, c := range s.Clauses {
				if c.Type == r.typ && admits(c, r.name) {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%s: no clause admits %s (%s), which is the service's own prefixed resource", s.Role, r.name, r.typ)
			}
		}
	}
	// Each condition is its own clauses in order, joined, and nothing else: a
	// widened expression is not something the plan review could tell from a
	// narrowed one, so the string is pinned whole.
	for _, s := range svcs {
		expr := s.Expression()
		for _, c := range s.Clauses {
			if !strings.Contains(expr, c.String()) {
				t.Errorf("%s: the expression does not carry the clause %q", s.Role, c.String())
			}
		}
		if strings.Count(expr, " || ") != len(s.Clauses)-1 {
			t.Errorf("%s: the expression %q is not the clauses joined", s.Role, expr)
		}
	}
	// The clauses are a copy: a caller cannot widen the fence.
	got, ok := DeployerConditionClauses(testDeployerProject, DeployerStorageRole)
	if !ok {
		t.Fatal("DeployerConditionClauses does not know the storage role")
	}
	got[0].Value = "projects/_/buckets/"
	again, _ := DeployerConditionClauses(testDeployerProject, DeployerStorageRole)
	if again[0].Value == got[0].Value {
		t.Error("DeployerConditionClauses hands out the fence itself, not a copy")
	}
	// A role that is not one of the seven has no condition, and the expression
	// of one that is comes back with it: this is what deployerGrant reads.
	if _, ok := DeployerConditionExpression(testDeployerProject, DeployerRole); ok {
		t.Errorf("%s has a condition, and it is bound with none (D75 (1))", DeployerRole)
	}
	for _, s := range svcs {
		expr, ok := DeployerConditionExpression(testDeployerProject, s.Role)
		if !ok || expr != s.Expression() {
			t.Errorf("%s: DeployerConditionExpression = %q, %v, want its own expression", s.Role, expr, ok)
		}
	}
}

// testDeployerProject is the project the module-pin test builds the fence for.
// One clause names a project — the IAM service account's — and the module
// writes it as ${var.project} where a plan carries the value.
const testDeployerProject = "proj-1"

// deployerConditionLocal is the module local that holds one role's condition:
// deployer_condition_<lowercased role suffix>, which is how the module's names
// and the roles stay in step — a service added on one side and not the other is
// a missing local rather than a silently uncovered role.
func deployerConditionLocal(role string) string {
	return "deployer_condition_" + strings.ToLower(strings.TrimPrefix(role, "fugaroDeployer"))
}

// The module writes the fence, so the module's text and Go's copy of it must
// be the same bytes: one local per service, its clauses one by one, in order,
// under the same title, and one binding per role — the seven conditioned ones
// under their own condition, the unconditioned one under none. This is what
// makes the plan review's byte comparison (deployerGrant) check the condition
// the module actually applies.
func TestDeployerBindingsAreTheModules(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules", "installation", "deployer.tf"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	titleRE := regexp.MustCompile(`deployer_condition_title\s*=\s*"([^"]+)"`)
	m := titleRE.FindStringSubmatch(src)
	if m == nil || m[1] != DeployerConditionTitle {
		t.Errorf("the module's condition title is %v, Go's is %q", m, DeployerConditionTitle)
	}
	locals := map[string][]string{}
	joinRE := regexp.MustCompile(`(?s)deployer_condition_([a-z0-9_]+)\s*=\s*join\(" \|\| ", \[(.*?)\]\)`)
	clauseRE := regexp.MustCompile(`"((?:[^"\\]|\\.)*resource\.type(?:[^"\\]|\\.)*)"`)
	for _, jm := range joinRE.FindAllStringSubmatch(src, -1) {
		// HCL escapes the expression's own quotes, so each clause is unquoted
		// before it is compared: the comparison is of the condition's bytes,
		// not of the file's escaping.
		var clauses []string
		for _, c := range clauseRE.FindAllStringSubmatch(jm[2], -1) {
			clause, err := strconv.Unquote(`"` + c[1] + `"`)
			if err != nil {
				t.Fatalf("unquoting the clause %q: %v", c[1], err)
			}
			// The one clause that names a project is written as ${var.project}
			// and Go builds it with the project the cover knows.
			clauses = append(clauses, strings.ReplaceAll(clause, "${var.project}", testDeployerProject))
		}
		if len(clauses) == 0 {
			t.Errorf("deployer.tf's local deployer_condition_%s is not a join of clause literals", jm[1])
		}
		// jm[1] is the group, the suffix alone: the local's name is the prefix
		// and the suffix, which is what the module writes and what the bindings
		// below name.
		if _, dup := locals["deployer_condition_"+jm[1]]; dup {
			t.Errorf("deployer.tf declares deployer_condition_%s twice", jm[1])
		}
		locals["deployer_condition_"+jm[1]] = clauses
	}
	svcs := deployerServices(testDeployerProject)
	for _, s := range svcs {
		name := deployerConditionLocal(s.Role)
		got, ok := locals[name]
		if !ok {
			t.Errorf("deployer.tf has no local.%s for %s", name, s.Role)
			continue
		}
		delete(locals, name)
		want := make([]string, 0, len(s.Clauses))
		for _, c := range s.Clauses {
			want = append(want, c.String())
		}
		if !slices.Equal(got, want) {
			t.Errorf("deployer.tf's %s fence is\n%v\nGo's %s is\n%v", name, got, s.Role, want)
		}
	}
	for name := range locals {
		t.Errorf("deployer.tf declares deployer_condition_%s, which is no service's fence", name)
	}
	// Exactly one binding of each of the eight roles in the whole module tree,
	// the seven conditioned ones under their own condition and the other under
	// none: a second binding of a conditioned role — under another condition or
	// under none — would widen the account as much as deleting the condition,
	// and one carrying another service's condition would fence the wrong
	// resource type.
	type binding struct {
		role      string
		condition string // the local the binding's condition must be, "" for none
	}
	bindings := []binding{{DeployerRole, ""}}
	for _, s := range svcs {
		bindings = append(bindings, binding{s.Role, "local." + deployerConditionLocal(s.Role)})
	}
	for _, tc := range bindings {
		// The closing quote is counted too: fugaroDeployer is a prefix of
		// fugaroDeployerStorage, and the bindings must be counted apart.
		literal := "projects/${var.project}/roles/" + tc.role + `"`
		if n := countInModules(t, literal); n != 1 {
			t.Errorf("the role %s is named %d times in the modules, want exactly its one binding", tc.role, n)
		}
		blockRE := regexp.MustCompile(`(?s)resource "google_project_iam_member" "[a-z0-9_]+" \{(.*?)\n\}`)
		found := false
		for _, blk := range blockRE.FindAllStringSubmatch(src, -1) {
			if !strings.Contains(blk[1], literal) {
				continue
			}
			found = true
			if strings.Contains(blk[1], "condition") != (tc.condition != "") {
				t.Errorf("the %s binding's condition is %v, want %q", tc.role, !(tc.condition != ""), tc.condition)
			}
			if !strings.Contains(blk[1], "google_service_account.fugaro_deployer.member") {
				t.Errorf("the %s binding is not on the deployer account: %s", tc.role, blk[1])
			}
			if tc.condition != "" && !strings.Contains(blk[1], tc.condition) {
				t.Errorf("the %s binding's condition is not %s", tc.role, tc.condition)
			}
			if tc.condition != "" && !strings.Contains(blk[1], "title      = local.deployer_condition_title") {
				t.Errorf("the %s binding's condition is not under the one fence title", tc.role)
			}
		}
		if !found {
			t.Errorf("no binding of %s in deployer.tf", tc.role)
		}
	}
	// The one-time impersonation setup: one grant of the token creator role,
	// on the deployer account, for var.operators and nobody else.
	blockRE := regexp.MustCompile(`(?s)resource "google_service_account_iam_member" "([a-z0-9_]+)" \{(.*?)\n\}`)
	grants := 0
	for _, blk := range blockRE.FindAllStringSubmatch(src, -1) {
		if !strings.Contains(blk[2], "serviceAccountTokenCreator") {
			continue
		}
		grants++
		// The account is a literal built from var.project and the fixed ID,
		// not the account resource's own name attribute: that attribute is
		// unknown at plan time, and deployerGrant covers this role only when
		// the plan names the account it is on. The literal is what the review
		// reads, so it is pinned here to the form deployerAccountName builds.
		want := `service_account_id = "projects/${var.project}/serviceAccounts/` + DeployerAccountID + `@${var.project}.iam.gserviceaccount.com"`
		for _, want := range []string{want, `member             = each.value`, `for_each = toset(var.operators)`} {
			if !strings.Contains(blk[2], want) {
				t.Errorf("%s is not the operators' grant on the deployer account: %s", blk[1], want)
			}
		}
		if strings.Contains(blk[2], "google_service_account.fugaro_deployer.name") {
			t.Errorf("%s names the account through its resource attribute, which is unknown at plan time: the grant has to say which account it is on", blk[1])
		}
	}
	if grants != 1 {
		t.Errorf("the modules grant the token creator role %d times, want one (on the deployer account)", grants)
	}
	// The account itself: the fixed ID, in the installation module.
	if !strings.Contains(src, `account_id   = "`+DeployerAccountID+`"`) {
		t.Errorf("deployer.tf does not create the account %q", DeployerAccountID)
	}
}

// countInModules counts how many times a string appears in the Terraform
// module tree.
func countInModules(t *testing.T, s string) int {
	t.Helper()
	root := filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules")
	n := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tf") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n += strings.Count(string(b), s)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A plan that creates one of the deployer's conditioned roles may not also
// carry a project grant of a computed role: the computed role could be one of
// them, whose binding carries that service's fence, and a plan that does not
// say which role it is cannot be reviewed as if it were the other one.
func TestDeployerRoleCreationRefusesComputedProjectRoles(t *testing.T) {
	var perms []any
	for _, p := range deployerRoleGolden[DeployerStorageRole] {
		perms = append(perms, p)
	}
	deployerRole := customRole(DeployerStorageRole, "proj-1", perms)
	for name, tc := range map[string]struct {
		changes []ResourceChange
		covered bool
	}{
		"a computed project role beside a conditioned role": {[]ResourceChange{deployerRole,
			rc("google_project_iam_member", map[string]any{"member": "user:me@example.com"}, map[string]any{"role": true}, "create")}, false},
		// The control: the same computed project role beside a custom role
		// that is not one of the deployer's, which is the shape the modules
		// have always had (cover.go's table-checked computed-role branch).
		"a computed project role beside another custom role": {[]ResourceChange{
			customRole("fugaroJobRunner", "proj-1", []any{"run.jobs.run", "run.jobs.runWithOverrides"}),
			rc("google_project_iam_member", map[string]any{"member": "user:me@example.com"}, map[string]any{"role": true}, "create")}, true},
		// The refusal is a PROJECT grant's, and only a project grant's: the
		// fence it is about is binding (2)'s condition on a project-level
		// binding, so a computed role at a narrower scope is not it. This is
		// the shape the installation module plans on every installation that
		// deploys the history job — history.tf's
		// google_cloud_run_v2_job_iam_member, whose role is the custom role
		// created beside it — and refusing it would move that job out of the
		// run's confirmation and into one of its own.
		"a computed job role beside the scoped role": {[]ResourceChange{deployerRole,
			customRole("fugaroJobRunner", "proj-1", []any{"run.jobs.run", "run.jobs.runWithOverrides"}),
			rc("google_cloud_run_v2_job_iam_member", map[string]any{"member": "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com"}, map[string]any{"role": true}, "create")}, true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := testCover.NotCovered(&Plan{ResourceChanges: tc.changes}, nil); (len(got) == 0) != tc.covered {
				t.Fatalf("NotCovered = %q, covered want %v", got, tc.covered)
			}
		})
	}
}

// The refusal above holds for each of the deployer's roles on its own: a plan
// that creates only fugaroDeployer, or only one of the seven conditioned roles,
// with a computed project role beside it is refused — either role being created
// is a plan in which a conditioned role may be the computed one (the eight are
// created together by the module, and a plan that carried one could still bind
// another, already existing, under a role nobody can name).
func TestDeployerRoleCreationRefusesComputedProjectRolesForEitherRole(t *testing.T) {
	roles := append([]string{DeployerRole}, DeployerServiceRoles()...)
	for _, id := range roles {
		t.Run(id, func(t *testing.T) {
			var perms []any
			for _, p := range RolePermissions[id] {
				perms = append(perms, p)
			}
			changes := []ResourceChange{customRole(id, "proj-1", perms),
				rc("google_project_iam_member", map[string]any{"member": "user:me@example.com"}, map[string]any{"role": true}, "create")}
			got := testCover.NotCovered(&Plan{ResourceChanges: changes}, nil)
			if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "could be it") {
				t.Fatalf("NotCovered = %q, want the computed project role refused", got)
			}
		})
	}
}

// TestDeployerDesignDirectionalReferencesPointTheRightWay: §0 cited Check 52's
// U3 as "(F5's table above)", and F5 is defined below it, inside §1 — the
// reference sent a reader into §0's own prose, which holds no table. It is the
// pointer to the evidence behind the sentence that even the Secret Manager half
// of the fence is "expected rather than proved", which is the same claim Check
// 68's C7 row rests on, so a design of record that points the wrong way costs a
// reader the evidence at exactly the moment they go looking for it. The
// reference is read for direction against where the labelled fact or section is
// defined, not against a line counted here, so an edit that moves either side
// fails this test rather than sliding past it. What it checks is the references
// that carry a label — "F5's table below", "§9 below"; a bare "open point 3
// above", which names no label this test can resolve, is not read here.
//
// Mutation (verified, run and restored): "F5's table below" back to "above", and
// "§9 below" in §0 to "§9 above" — each fails here naming the reference.
func TestDeployerDesignDirectionalReferencesPointTheRightWay(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "design", "managed-deployer-fence.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	line := func(at int) int { return 1 + strings.Count(doc[:at], "\n") }

	// Where each labelled fact is defined, and where each section starts.
	facts := map[string]int{}
	for _, m := range regexp.MustCompile(`(?m)^\*\*(F\d+) —`).FindAllStringSubmatchIndex(doc, -1) {
		facts[doc[m[2]:m[3]]] = m[0]
	}
	if len(facts) == 0 {
		t.Fatal("the design defines no F-labelled facts: §0's references are to them, so this test reads the definitions from here")
	}
	sections := map[string]int{}
	for _, m := range regexp.MustCompile(`(?m)^## (\d+)\.`).FindAllStringSubmatchIndex(doc, -1) {
		sections[doc[m[2]:m[3]]] = m[0]
	}
	if len(sections) == 0 {
		t.Fatal("the design has no numbered sections: §0's references are to them, so this test reads the headings from here")
	}

	re := regexp.MustCompile(`(\bF\d+|§\d+)[^.\n]{0,60}?\b(above|below)\b`)
	refs := re.FindAllStringSubmatchIndex(doc, -1)
	if len(refs) == 0 {
		t.Fatal("the design makes no directional reference (\"F5's table below\", \"§9 below\"): this test exists to hold the ones it makes to the tree of the document they point into")
	}
	for _, m := range refs {
		label, dir := doc[m[2]:m[3]], doc[m[4]:m[5]]
		var def int
		var what string
		if strings.HasPrefix(label, "§") {
			def, what = sections[strings.TrimPrefix(label, "§")], "section "+label
		} else {
			def, what = facts[label], label
		}
		if what == "" || def == 0 {
			t.Errorf("line %d of the design refers to %s, which the document does not define: a reference to a fact or section that is not here cannot be read", line(m[0]), label)
			continue
		}
		defLine := line(def)
		want := "below"
		if def < m[0] {
			want = "above"
		}
		if dir != want {
			t.Errorf("line %d of the design says \"%s\", and %s is defined at line %d: the reference points a reader the wrong way, into material that does not hold it", line(m[0]), doc[m[0]:m[1]], what, defLine)
		}
	}
}
