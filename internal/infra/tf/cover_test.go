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

var testCover = Cover{Projects: []string{"proj-1", "fp-2"}, Listed: []string{"user:me@example.com", "group:ops@example.com"}}

func TestCoverNotCovered(t *testing.T) {
	sa := rc("google_service_account", map[string]any{}, map[string]any{}, "create")
	role := rc("google_project_iam_custom_role", map[string]any{"permissions": []any{"a.b"}}, map[string]any{}, "create")
	for name, tc := range map[string]struct {
		changes []ResourceChange
		covered bool
	}{
		"additive known plan": {[]ResourceChange{sa, role, rc("google_storage_bucket", nil, nil, "create"), rc("google_cloud_run_v2_job", nil, nil, "update"),
			grant("google_project_iam_member", "user:me@example.com", "roles/storage.objectUser"),
			grant("google_project_iam_member", "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com", "roles/datastore.user"),
			grant("google_project_iam_member", "group:ops@example.com", "projects/proj-1/roles/fugaroLauncher"),
			grant("google_service_account_iam_member", "user:me@example.com", "roles/iam.serviceAccountUser"),
			rc("google_project_service", nil, nil, "no-op")}, true},
		"member computed from a service account created here": {[]ResourceChange{sa,
			rc("google_project_iam_member", map[string]any{"role": "roles/logging.logWriter"}, map[string]any{"member": true}, "create")}, true},
		"role computed from a custom role created here": {[]ResourceChange{role,
			rc("google_project_iam_member", map[string]any{"member": "user:me@example.com"}, map[string]any{"role": true}, "create")}, true},
		"nil plan":                         {nil, false},
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
func TestAllowedTypesCoverTheModules(t *testing.T) {
	root := filepath.Join("..", "..", "..", "deploy", "terraform")
	resRE := regexp.MustCompile(`(?m)^resource "([a-z0-9_]+)"`)
	roleRE := regexp.MustCompile(`"(roles/[A-Za-z0-9.]+)"`)
	n := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tf") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n++
		for _, m := range resRE.FindAllStringSubmatch(string(b), -1) {
			if !allowedTypes[m[1]] {
				t.Errorf("%s declares %s, which init's one confirmation does not allow: decide it in tf/cover.go", path, m[1])
			}
		}
		for _, m := range roleRE.FindAllStringSubmatch(string(b), -1) {
			if !allowedRoles[m[1]] {
				t.Errorf("%s grants %s, which is not in tf/cover.go's roles", path, m[1])
			}
		}
		return nil
	})
	if err != nil || n < 20 {
		t.Fatalf("read %d module files: %v", n, err)
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
