package tf

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	ids := map[string]string{"launcher": "fugaroLauncher", "job_runner": "fugaroJobRunner", "build_submitter": "fugaroBuildSubmitter", "tag_mover": "fugaroTagMover", "cloud_asset_reader": CloudAssetRole, "token_minter": "fugaroTokenMinter", "history": "fugaroHistory", "reaper": "fugaroReaper", "preload_builder": "fugaroPreloadBuilder", "fugaro_deployer": DeployerRole, "fugaro_deployer_scoped": DeployerScopedRole}
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
				// (D75, amended the same day): the scoped deployer role holds
				// actAs, because a managed job or schedule must run as an
				// account of the plan's own. The bound on it is the condition
				// on the single binding of that role (the prefix fence, which
				// admits no account the plan did not create) and
				// TestDeployerRolesHoldOnlyWhatD75Names below — not this list,
				// which is about roles a Fugaro run holds. The other five stay
				// forbidden for both deployer roles.
				if id == DeployerScopedRole && never == "iam.serviceAccounts.actAs" {
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

// deployerRoleGolden is both roles' permission lists, permission for
// permission, as deploy/terraform/gcp/modules/installation/deployer.tf writes
// them. A widening — one permission added to either role — is a diff here and
// in the module pin (TestRolePinsAreTheModules), so it cannot ride along on a
// covered create.
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
	DeployerScopedRole: {
		"apikeys.keys.delete", "apikeys.keys.update",
		"artifactregistry.repositories.delete", "artifactregistry.repositories.setIamPolicy", "artifactregistry.repositories.update",
		"cloudscheduler.jobs.delete", "cloudscheduler.jobs.enable", "cloudscheduler.jobs.pause", "cloudscheduler.jobs.run", "cloudscheduler.jobs.update",
		"iam.serviceAccounts.actAs", "iam.serviceAccounts.delete", "iam.serviceAccounts.setIamPolicy", "iam.serviceAccounts.update",
		"run.jobs.delete", "run.jobs.run", "run.jobs.runWithOverrides", "run.jobs.setIamPolicy", "run.jobs.update",
		"secretmanager.secrets.delete", "secretmanager.secrets.setIamPolicy", "secretmanager.secrets.update",
		"storage.buckets.delete", "storage.buckets.get", "storage.buckets.setIamPolicy", "storage.buckets.update",
		"storage.objects.create", "storage.objects.delete", "storage.objects.get", "storage.objects.list", "storage.objects.update",
	},
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
	for _, p := range deployerRoleGolden[DeployerScopedRole] {
		moved := append(slices.Clone(deployerRoleGolden[DeployerRole]), p)
		if got := unconditionedDeployerViolations(moved); len(got) == 0 {
			t.Errorf("moving %s into %s is not caught", p, DeployerRole)
		}
	}
	if got := unconditionedDeployerViolations([]string{"cloudscheduler.jobs.enable"}); len(got) == 0 {
		t.Error("cloudscheduler.jobs.enable (a Scheduler job's resume) in the unconditioned role is not caught")
	}
}

// The deployer's two roles hold exactly the lists D75 names, and the split
// between them is the decision: every verb that changes or reaches an existing
// resource — update, delete, run, pause, resume (cloudscheduler.jobs.enable),
// setIamPolicy — and actAs
// itself are in the scoped role alone, which is bound once and only under the
// managed-prefix condition. The other role creates, reads and enables, and
// nothing else.
func TestDeployerRolesHoldOnlyWhatD75Names(t *testing.T) {
	for id, want := range deployerRoleGolden {
		got := slices.Clone(RolePermissions[id])
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s pins %v, want %v", id, got, want)
		}
	}
	// A wildcard is never a permission, and neither role may reach project IAM
	// or an IAM role: an account holding either could grant itself object
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
	// The scoped role: exactly the nine storage permissions of D75, and no
	// secretmanager.versions.*, which would make a secret value readable.
	var storage []string
	for _, p := range RolePermissions[DeployerScopedRole] {
		if strings.HasPrefix(p, "storage.") {
			storage = append(storage, p)
		}
		if strings.HasPrefix(p, "secretmanager.versions.") {
			t.Errorf("%s holds %s: a managed plan creates a container and never reads a version", DeployerScopedRole, p)
		}
	}
	want := []string{"storage.buckets.delete", "storage.buckets.get", "storage.buckets.setIamPolicy", "storage.buckets.update",
		"storage.objects.create", "storage.objects.delete", "storage.objects.get", "storage.objects.list", "storage.objects.update"}
	slices.Sort(storage)
	if !slices.Equal(storage, want) {
		t.Errorf("%s holds the storage permissions %v, want the nine D75 names %v", DeployerScopedRole, storage, want)
	}
}

// The condition is the whole fence, so it is tested as one: every clause names
// the resource type it admits (a clause with no type term applies to every type
// the role reaches) and tests the managed prefix, and no clause can match one
// of Fugaro's own resources or the two state buckets.
func TestDeployerConditionFencesOnlyTheManagedPrefix(t *testing.T) {
	cls := DeployerConditionClauses()
	if len(cls) < 5 {
		t.Fatalf("the condition has %d clauses, want one per service the role reaches", len(cls))
	}
	// Fugaro's own names: what the fence must not admit, whatever a plan does.
	forbidden := []string{
		"projects/_/buckets/fugaro-runs-proj-1", "projects/_/buckets/fugaro-runs-proj-1/objects/fugaro/managed.json",
		"projects/_/buckets/fugaro-managed-tfstate-proj-1/objects/fugaro/managed.tfstate",
		"projects/_/buckets/fugaro-tfstate-proj-1/objects/fugaro/installation/default.tfstate",
		"projects/12/locations/us-east5/jobs/fugaro-history", "projects/12/locations/us-east5/jobs/check",
		"projects/12/secrets/fugaro-token-signer", "projects/12/locations/us-east5/repositories/fugaro-base",
		"projects/12/serviceAccounts/fugaro-scheduler@proj-1.iam.gserviceaccount.com",
		"projects/12/serviceAccounts/1234567890-compute@developer.gserviceaccount.com",
		"projects/12/locations/global/keys/fugaro-web",
		// An object carrying the prefix in a bucket that does not: the runs
		// bucket's objects are reached by bucket name, never by object name
		// (a clause testing the prefix anywhere in a storage name admits it).
		"projects/_/buckets/fugaro-runs-x/objects/fmgd-y",
		"projects/_/buckets/fugaro-runs-proj-1/objects/runs/fmgd-y/state.json",
		"projects/_/buckets/fugaro-tfstate-proj-1/objects/fmgd-/default.tfstate",
		"projects/_/buckets/x-fmgd-/objects/a",
	}
	admits := func(c DeployerClause, name string) bool {
		switch c.Op {
		case "startsWith":
			return strings.HasPrefix(name, c.Value)
		default:
			return strings.Contains(name, c.Value)
		}
	}
	for _, c := range cls {
		if c.Type == "" || !strings.Contains(c.Type, "/") {
			t.Errorf("clause %+v carries no resource type: it would apply to every type the role reaches", c)
		}
		if !strings.Contains(c.Value, ManagedPrefix) {
			t.Errorf("clause %+v does not test the managed prefix %q", c, ManagedPrefix)
		}
		if !strings.HasSuffix(c.Value, "/"+ManagedPrefix) {
			t.Errorf("clause %+v does not end at the prefix after a path separator: a name such as x-%s would match it", c, ManagedPrefix)
		}
		// The value is never the prefix alone (DeployerClause's doc comment):
		// a value that admitted any name containing the prefix would admit a
		// fugaro-* object in a bucket whose object name carries it.
		if c.Value == ManagedPrefix || c.Value == "/"+ManagedPrefix {
			t.Errorf("clause %+v tests the prefix alone", c)
		}
		// Cloud Storage names put the bucket at the front, so a storage clause
		// tests the bucket's name with startsWith, on the bucket resource path.
		if strings.HasPrefix(c.Type, "storage.googleapis.com/") && (c.Op != "startsWith" || c.Value != "projects/_/buckets/"+ManagedPrefix) {
			t.Errorf("storage clause %+v is not startsWith(\"projects/_/buckets/%s\")", c, ManagedPrefix)
		}
		switch c.Op {
		case "startsWith":
		case "contains":
		default:
			t.Errorf("clause %+v has an operator the condition does not use", c)
		}
		for _, name := range forbidden {
			if admits(c, name) || strings.Contains(name, c.Value) {
				t.Errorf("clause %+v admits %s", c, name)
			}
		}
	}
	// One expression, both halves of it: the clauses in order, joined, and the
	// module's own text is pinned against it in TestDeployerBindingsAreTheModules.
	expr := DeployerConditionExpression()
	for _, c := range cls {
		if !strings.Contains(expr, c.String()) {
			t.Errorf("the expression does not carry the clause %q", c.String())
		}
	}
	if strings.Count(expr, " || ") != len(cls)-1 {
		t.Errorf("the expression %q is not the clauses joined", expr)
	}
	// The clauses are a copy: a caller cannot widen the fence.
	got := DeployerConditionClauses()
	got[0].Value = "projects/_/buckets/"
	if DeployerConditionClauses()[0].Value == got[0].Value {
		t.Error("DeployerConditionClauses hands out the fence itself, not a copy")
	}
}

// The module writes the fence, so the module's text and Go's copy of it must
// be the same bytes: the clauses one by one, in order, under the same title,
// and one binding per role — the scoped one conditioned, the other one not.
// This is what makes the plan review's byte comparison (deployerGrant)
// check the condition the module actually applies.
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
	joinRE := regexp.MustCompile(`(?s)deployer_condition_expression\s*=\s*join\(" \|\| ", \[(.*?)\]\)`)
	jm := joinRE.FindStringSubmatch(src)
	if jm == nil {
		t.Fatal("deployer.tf has no join(\" || \", [...]) expression")
	}
	var moduleClauses []string
	// HCL escapes the expression's own quotes, so each clause is unquoted
	// before it is compared: the comparison is of the condition's bytes, not
	// of the file's escaping.
	for _, c := range regexp.MustCompile(`"((?:[^"\\]|\\.)*resource\.type(?:[^"\\]|\\.)*)"`).FindAllStringSubmatch(jm[1], -1) {
		clause, err := strconv.Unquote(`"` + c[1] + `"`)
		if err != nil {
			t.Fatalf("unquoting the clause %q: %v", c[1], err)
		}
		moduleClauses = append(moduleClauses, clause)
	}
	var goClauses []string
	for _, c := range DeployerConditionClauses() {
		goClauses = append(goClauses, c.String())
	}
	if !slices.Equal(moduleClauses, goClauses) {
		t.Errorf("deployer.tf's clauses are\n%v\nGo's are\n%v", moduleClauses, goClauses)
	}
	// Exactly one binding of each role in the whole module tree, and the
	// scoped role's is the conditioned one: a second binding of the scoped
	// role — under another condition or under none — would widen the account
	// as much as deleting the condition.
	for _, tc := range []struct {
		role      string
		condition bool
	}{
		{DeployerRole, false},
		{DeployerScopedRole, true},
	} {
		// The closing quote is counted too: fugaroDeployer is a prefix of
		// fugaroDeployerScoped, and the two bindings must be counted apart.
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
			if strings.Contains(blk[1], "condition") != tc.condition {
				t.Errorf("the %s binding's condition is %v, want %v", tc.role, !tc.condition, tc.condition)
			}
			if !strings.Contains(blk[1], "google_service_account.fugaro_deployer.member") {
				t.Errorf("the %s binding is not on the deployer account: %s", tc.role, blk[1])
			}
			if tc.condition && !strings.Contains(blk[1], "local.deployer_condition_expression") {
				t.Errorf("the %s binding's condition is not the module's expression", tc.role)
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

// A plan that creates one of the deployer's roles may not also carry a project
// grant of a computed role: the computed role could be the scoped one, whose
// binding carries the fence, and a plan that does not say which role it is
// cannot be reviewed as if it were the other one.
func TestDeployerRoleCreationRefusesComputedProjectRoles(t *testing.T) {
	var perms []any
	for _, p := range deployerRoleGolden[DeployerScopedRole] {
		perms = append(perms, p)
	}
	deployerRole := customRole(DeployerScopedRole, "proj-1", perms)
	for name, tc := range map[string]struct {
		changes []ResourceChange
		covered bool
	}{
		"a computed project role beside the scoped role": {[]ResourceChange{deployerRole,
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
// that creates only fugaroDeployer, or only fugaroDeployerScoped, with a
// computed project role beside it is refused — either role being created is a
// plan in which the scoped role may be the computed one (the two are created
// together by the module, and a plan that carried one could still bind the
// other, already existing, under a role nobody can name).
func TestDeployerRoleCreationRefusesComputedProjectRolesForEitherRole(t *testing.T) {
	for _, id := range []string{DeployerRole, DeployerScopedRole} {
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
