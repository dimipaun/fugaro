package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func rc(typ string, after map[string]any, unk map[string]any, acts ...string) ResourceChange {
	return ResourceChange{Address: "module.m." + typ + ".x", Type: typ, Change: Change{Actions: acts, After: after, AfterUnknown: unk}}
}

func grant(typ, member, role string) ResourceChange {
	return rc(typ, map[string]any{"member": member, "role": role}, map[string]any{}, "create")
}

var testCover = Cover{Projects: []string{"proj-1", "fp-2"}, Listed: []string{"user:me@example.com", "group:ops@example.com", "domain:example.com", "allUsers"},
	Registry: "us-east5-docker.pkg.dev", Region: "us-east5", Buckets: []string{"fugaro-runs-proj-1"}, Billing: []string{"AAAA-BBBB"}}

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

const okURI = "https://run.googleapis.com/v2/projects/proj-1/locations/us-east5/jobs/fugarohist:run"

func TestCoverNotCovered(t *testing.T) {
	sa := rc("google_service_account", map[string]any{}, map[string]any{}, "create")
	role := rc("google_project_iam_custom_role", map[string]any{"permissions": []any{"a.b"}}, map[string]any{}, "create")
	for name, tc := range map[string]struct {
		changes []ResourceChange
		covered bool
	}{
		"additive known plan": {[]ResourceChange{sa, role, bucket(nil), job("us-east5-docker.pkg.dev/proj-1/fugaro-base/history:latest", []any{"/usr/local/bin/fugaro"}, []any{"budget", "history", "--sweep"}), sched(okURI, "fugaro-scheduler@proj-1.iam.gserviceaccount.com"),
			rc("google_logging_project_sink", map[string]any{"destination": "logging.googleapis.com/projects/proj-1/locations/global/buckets/fugaro"}, map[string]any{}, "create"),
			grant("google_project_iam_member", "user:me@example.com", "roles/storage.objectUser"),
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
		"grant on another bucket":         {[]ResourceChange{rc("google_storage_bucket_iam_member", map[string]any{"bucket": "evil-bucket", "member": "user:me@example.com", "role": "roles/storage.objectUser"}, map[string]any{}, "create")}, false},
		"grant on the runs bucket":        {[]ResourceChange{rc("google_storage_bucket_iam_member", map[string]any{"bucket": "fugaro-runs-proj-1", "member": "user:me@example.com", "role": "roles/storage.objectUser"}, map[string]any{}, "create")}, true},
		"grant on a foreign registry":     {[]ResourceChange{rc("google_artifact_registry_repository_iam_member", map[string]any{"repository": "prod-images", "member": "user:me@example.com", "role": "roles/artifactregistry.reader"}, map[string]any{}, "create")}, false},
		"billing budget not shown":        {[]ResourceChange{rc("google_billing_budget", map[string]any{"billing_account": "billingAccounts/ZZZZ-ZZZZ"}, map[string]any{}, "create")}, false},
		"billing budget shown":            {[]ResourceChange{rc("google_billing_budget", map[string]any{"billing_account": "billingAccounts/AAAA-BBBB"}, map[string]any{}, "create")}, true},
		"api key with a wide target":      {[]ResourceChange{rc("google_apikeys_key", map[string]any{"restrictions": []any{map[string]any{"api_targets": []any{map[string]any{"service": "identitytoolkit.googleapis.com"}, map[string]any{"service": "storage.googleapis.com"}}}}}, map[string]any{}, "create")}, false},
		"api key as the module writes it": {[]ResourceChange{rc("google_apikeys_key", map[string]any{"restrictions": []any{map[string]any{"api_targets": []any{map[string]any{"service": "securetoken.googleapis.com"}, map[string]any{"service": "identitytoolkit.googleapis.com"}}}}}, map[string]any{}, "create")}, true},
		"import of another project":       {[]ResourceChange{{Address: "a.google_storage_bucket.x", Type: "google_storage_bucket", Change: Change{Actions: []string{"no-op"}, Importing: &Importing{ID: "other/fugaro-runs"}, After: map[string]any{"public_access_prevention": "enforced", "uniform_bucket_level_access": true}}}}, false},
		"import of this project": {[]ResourceChange{{Address: "a.google_storage_bucket.x", Type: "google_storage_bucket", Change: Change{Actions: []string{"update"}, Importing: &Importing{ID: "proj-1/fugaro-runs-proj-1"},
			After: map[string]any{"public_access_prevention": "enforced", "uniform_bucket_level_access": true}}}}, true},
		"domain member, even listed":            {[]ResourceChange{grant("google_project_iam_member", "domain:example.com", "roles/storage.objectUser")}, false},
		"allUsers, even listed":                 {[]ResourceChange{grant("google_project_iam_member", "allUsers", "roles/storage.objectUser")}, false},
		"a custom role of the firebase project": {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "projects/fp-2/roles/fugaroTokenMinter")}, true},
		"a custom role by a made-up name":       {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "projects/proj-1/roles/fugaroEverything")}, false},
		"a made-up fugaro account":              {[]ResourceChange{grant("google_project_iam_member", "serviceAccount:fugaro-evil@proj-1.iam.gserviceaccount.com", "roles/storage.objectUser")}, false},
		"computed member from another module's account": {[]ResourceChange{
			{Address: "module.a.google_service_account.s", Type: "google_service_account", Change: Change{Actions: []string{"create"}, After: map[string]any{}, AfterUnknown: map[string]any{}}},
			{Address: "module.b.google_project_iam_member.m", Type: "google_project_iam_member", Change: Change{Actions: []string{"create"}, After: map[string]any{"role": "roles/logging.logWriter"}, AfterUnknown: map[string]any{"member": true}}}}, false},
		"computed member from the same module's account": {[]ResourceChange{
			{Address: "module.a.google_service_account.s", Type: "google_service_account", Change: Change{Actions: []string{"create"}, After: map[string]any{}, AfterUnknown: map[string]any{}}},
			{Address: "module.a.google_project_iam_member.m", Type: "google_project_iam_member", Change: Change{Actions: []string{"create"}, After: map[string]any{"role": "roles/logging.logWriter"}, AfterUnknown: map[string]any{"member": true}}}}, true},
		"computed role from another module's custom role": {[]ResourceChange{
			{Address: "module.a.google_project_iam_custom_role.r", Type: "google_project_iam_custom_role", Change: Change{Actions: []string{"create"}, After: map[string]any{}, AfterUnknown: map[string]any{}}},
			{Address: "module.b.google_project_iam_member.m", Type: "google_project_iam_member", Change: Change{Actions: []string{"create"}, After: map[string]any{"member": "user:me@example.com"}, AfterUnknown: map[string]any{"role": true}}}}, false},
		"delete":                           {[]ResourceChange{rc("google_storage_bucket", nil, nil, "delete")}, false},
		"replace":                          {[]ResourceChange{rc("google_storage_bucket", nil, nil, "delete", "create")}, false},
		"forget":                           {[]ResourceChange{rc("google_storage_bucket", nil, nil, "forget")}, false},
		"no actions":                       {[]ResourceChange{rc("google_storage_bucket", nil, nil)}, false},
		"iam_binding create":               {[]ResourceChange{rc("google_project_iam_binding", map[string]any{"role": "roles/storage.objectUser", "members": []any{"user:me@example.com"}}, nil, "create")}, false},
		"iam_policy":                       {[]ResourceChange{rc("google_project_iam_policy", nil, nil, "create")}, false},
		"audit config":                     {[]ResourceChange{rc("google_project_iam_audit_config", nil, nil, "create")}, false},
		"service account key":              {[]ResourceChange{rc("google_service_account_key", nil, nil, "create")}, false},
		"unknown resource type":            {[]ResourceChange{rc("google_compute_instance", nil, nil, "create")}, false},
		"allUsers":                         {[]ResourceChange{grant("google_storage_bucket_iam_member", "allUsers", "roles/storage.objectViewer")}, false},
		"allAuthenticatedUsers":            {[]ResourceChange{grant("google_storage_bucket_iam_member", "allAuthenticatedUsers", "roles/storage.objectViewer")}, false},
		"domain member":                    {[]ResourceChange{grant("google_project_iam_member", "domain:example.com", "roles/storage.objectViewer")}, false},
		"member not on the screen":         {[]ResourceChange{grant("google_project_iam_member", "user:eve@example.com", "roles/storage.objectUser")}, false},
		"another project's account":        {[]ResourceChange{grant("google_project_iam_member", "serviceAccount:fugaro-x@other.iam.gserviceaccount.com", "roles/storage.objectUser")}, false},
		"a non-fugaro account":             {[]ResourceChange{grant("google_project_iam_member", "serviceAccount:evil@proj-1.iam.gserviceaccount.com", "roles/storage.objectUser")}, false},
		"owner to a listed member":         {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "roles/owner")}, false},
		"editor":                           {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "roles/editor")}, false},
		"securityAdmin":                    {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "roles/iam.securityAdmin")}, false},
		"tokenCreator":                     {[]ResourceChange{grant("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountTokenCreator")}, false},
		"keyAdmin":                         {[]ResourceChange{grant("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountKeyAdmin")}, false},
		"resourcemanager admin":            {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "roles/resourcemanager.projectIamAdmin")}, false},
		"serviceAccountUser to a stranger": {[]ResourceChange{grant("google_service_account_iam_member", "user:eve@example.com", "roles/iam.serviceAccountUser")}, false},
		"another project's custom role":    {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "projects/other/roles/fugaroLauncher")}, false},
		"a custom role not ours":           {[]ResourceChange{grant("google_project_iam_member", "user:me@example.com", "projects/proj-1/roles/owner")}, false},
		"unknown member, no account made":  {[]ResourceChange{rc("google_project_iam_member", map[string]any{"role": "roles/logging.logWriter"}, map[string]any{"member": true}, "create")}, false},
		"unknown role, no role made":       {[]ResourceChange{rc("google_project_iam_member", map[string]any{"member": "user:me@example.com"}, map[string]any{"role": true}, "create")}, false},
		"custom role widened": {[]ResourceChange{{Address: "r", Type: "google_project_iam_custom_role", Change: Change{Actions: []string{"update"},
			Before: map[string]any{"permissions": []any{"a.b"}}, After: map[string]any{"permissions": []any{"a.b", "c.d"}}, AfterUnknown: map[string]any{}}}}, false},
		"custom role narrowed": {[]ResourceChange{{Address: "r", Type: "google_project_iam_custom_role", Change: Change{Actions: []string{"update"},
			Before: map[string]any{"permissions": []any{"a.b", "c.d"}}, After: map[string]any{"permissions": []any{"a.b"}}, AfterUnknown: map[string]any{}}}}, false},
		"custom role permissions unknown": {[]ResourceChange{{Address: "r", Type: "google_project_iam_custom_role", Change: Change{Actions: []string{"update"},
			Before: map[string]any{"permissions": []any{"a.b"}}, After: map[string]any{}, AfterUnknown: map[string]any{"permissions": true}}}}, false},
		"custom role retitled": {[]ResourceChange{{Address: "r", Type: "google_project_iam_custom_role", Change: Change{Actions: []string{"update"},
			Before: map[string]any{"permissions": []any{"a.b"}}, After: map[string]any{"permissions": []any{"a.b"}}, AfterUnknown: map[string]any{}}}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			var p *Plan
			if tc.changes != nil {
				p = &Plan{ResourceChanges: tc.changes}
			}
			if got := testCover.NotCovered(p); (len(got) == 0) != tc.covered {
				t.Fatalf("NotCovered = %q, covered want %v", got, tc.covered)
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
	for _, never := range []string{"roles/owner", "roles/editor", "roles/iam.securityAdmin", "roles/iam.serviceAccountTokenCreator", "roles/iam.serviceAccountKeyAdmin"} {
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
