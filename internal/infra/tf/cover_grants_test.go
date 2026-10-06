package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func grantRoleString(key string) string {
	if id, ok := strings.CutPrefix(key, customKey); ok {
		return "projects/proj-1/roles/" + id
	}
	return key
}

// Every (type, role, kind of principal) triple is covered exactly when the
// table says so. This is the mutation check of the table: dropping a row,
// adding a role to a type it does not belong to, or widening who may hold it
// flips a cell of this matrix.
func TestGrantRulesMatrix(t *testing.T) {
	const person = "user:me@example.com"
	const account = "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com"
	all := map[string]bool{}
	for _, row := range grantRules {
		for k := range row {
			all[k] = true
		}
	}
	if len(all) < 20 {
		t.Fatalf("only %d roles in grantRules", len(all))
	}
	for typ, row := range grantRules {
		for key := range all {
			role := grantRoleString(key)
			for kind, member := range map[who]string{toPeople: person, toAccounts: account} {
				want := row[key]&kind != 0
				ch := rc(typ, map[string]any{"member": member, "role": role, "bucket": "fugaro-runs-proj-1"}, map[string]any{}, "create")
				got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
				if (len(got) == 0) != want {
					t.Errorf("%s: %s to %s: NotCovered = %q, covered want %v", typ, role, member, got, want)
				}
			}
			// A member computed from a service account created in the same
			// module is an account.
			sa := rc("google_service_account", map[string]any{}, map[string]any{}, "create")
			ch := rc(typ, map[string]any{"role": role, "bucket": "fugaro-runs-proj-1"}, map[string]any{"member": true}, "create")
			got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{sa, ch}}, nil)
			if want := row[key]&toAccounts != 0; (len(got) == 0) != want {
				t.Errorf("%s: %s to a computed account: NotCovered = %q, covered want %v", typ, role, got, want)
			}
		}
		// Roles no module grants through any type.
		for _, role := range []string{"roles/owner", "roles/editor", "roles/viewer", "roles/iam.securityAdmin", "roles/storage.admin", "roles/run.admin", "roles/iam.serviceAccountTokenCreator",
			"projects/proj-1/roles/fugaroEverything", "projects/other/roles/fugaroLauncher"} {
			for _, member := range []string{person, account} {
				ch := rc(typ, map[string]any{"member": member, "role": role, "bucket": "fugaro-runs-proj-1"}, map[string]any{}, "create")
				if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
					t.Errorf("%s: %s to %s is covered", typ, role, member)
				}
			}
		}
	}
	// A computed role is trusted only from a custom role created in the module,
	// and the member is still checked.
	role := customRole("fugaroJobRunner", "proj-1", []any{"run.jobs.run", "run.jobs.runWithOverrides"})
	ch := rc("google_cloud_run_v2_job_iam_member", map[string]any{"member": "user:eve@example.com"}, map[string]any{"role": true}, "create")
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{role, ch}}, nil); len(got) == 0 {
		t.Error("a computed role to a stranger is covered")
	}
}

// A plan that creates a custom role and binds it through a computed role is
// still checked against the table: the type's row must name a custom role for
// that kind of principal, and the member is checked as for any other grant.
func TestComputedRoleWithCreatedCustomRoleIsTableChecked(t *testing.T) {
	const person = "user:me@example.com"
	const account = "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com"
	const computedRole = "a computed role on a "
	role := customRole("fugaroJobRunner", "proj-1", []any{"run.jobs.run", "run.jobs.runWithOverrides"})
	for _, c := range []struct {
		typ, member string
		covered     bool
		reason      string // for a stop: the text the stop must carry
	}{
		{"google_cloud_run_v2_job_iam_member", person, true, ""},
		{"google_cloud_run_v2_job_iam_member", account, true, ""},
		{"google_project_iam_member", person, true, ""},
		{"google_service_account_iam_member", account, false, computedRole}, // its only custom role is for people
		{"google_storage_bucket_iam_member", person, false, computedRole},   // the row has no custom role
		{"google_secret_manager_secret_iam_member", account, false, computedRole},
		{"google_cloud_run_v2_job_iam_member", "user:eve@example.com", false, "who is not on the review screen"},
	} {
		ch := rc(c.typ, map[string]any{"member": c.member, "bucket": "fugaro-runs-proj-1"}, map[string]any{"role": true}, "create")
		got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{role, ch}}, nil)
		if (len(got) == 0) != c.covered {
			t.Errorf("%s computed role to %s: NotCovered = %q, covered want %v", c.typ, c.member, got, c.covered)
		}
		if !c.covered && c.reason != "" && !strings.Contains(strings.Join(got, "\n"), c.reason) {
			t.Errorf("%s computed role to %s: stopped for another reason: %q, want %q", c.typ, c.member, got, c.reason)
		}
	}
}

func TestGrantRulesNameOnlyKnownTypesAndRoles(t *testing.T) {
	for typ, row := range grantRules {
		if !allowedTypes[typ] || !strings.HasSuffix(typ, "_iam_member") {
			t.Errorf("%s is in grantRules but is not an allowed *_iam_member type", typ)
		}
		if len(row) == 0 {
			t.Errorf("%s has no roles", typ)
		}
		for key, w := range row {
			if w == 0 || w&^(toPeople|toAccounts) != 0 {
				t.Errorf("%s %s: bad principal kinds %d", typ, key, w)
			}
			if id, ok := strings.CutPrefix(key, customKey); ok {
				if !slices.Contains(FugaroRoles, id) {
					t.Errorf("%s names the custom role %s, which the modules do not create", typ, id)
				}
			} else if !strings.HasPrefix(key, "roles/") {
				t.Errorf("%s: %q is neither a predefined nor a custom role key", typ, key)
			}
		}
	}
	for typ := range allowedTypes {
		if strings.HasSuffix(typ, "_iam_member") && grantRules[typ] == nil {
			t.Errorf("%s is an allowed type with no grant rules", typ)
		}
	}
}

// The table is the modules': per *_iam_member resource type, the predefined
// roles its resources grant are exactly the ones the table lists for it.
func TestGrantRulesAreTheModules(t *testing.T) {
	root := filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules")
	blockRE := regexp.MustCompile(`(?s)\nresource "([a-z0-9_]+_iam_member)" "[a-z0-9_]+" \{(.*?)\n\}`)
	roleRE := regexp.MustCompile(`"(roles/[A-Za-z0-9.]+)"`)
	used := map[string]map[string]bool{}
	customUse := map[string]bool{}
	n := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tf") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range blockRE.FindAllStringSubmatch("\n"+string(b), -1) {
			n++
			typ, body := m[1], m[2]
			if used[typ] == nil {
				used[typ] = map[string]bool{}
			}
			for _, r := range roleRE.FindAllStringSubmatch(body, -1) {
				used[typ][r[1]] = true
				if _, ok := grantRules[typ][r[1]]; !ok {
					t.Errorf("%s grants %s through %s, which is not in grantRules", path, r[1], typ)
				}
			}
			if strings.Contains(body, "custom_role") || strings.Contains(body, "role_ids") || strings.Contains(body, "job_runner_role") {
				customUse[typ] = true
			}
		}
		return nil
	})
	if err != nil || n < 25 {
		t.Fatalf("read %d iam_member blocks: %v", n, err)
	}
	for typ, row := range grantRules {
		for key := range row {
			if strings.HasPrefix(key, customKey) {
				if !customUse[typ] {
					t.Errorf("%s lists %s but no module grants a custom role through it", typ, key)
				}
			} else if !used[typ][key] {
				t.Errorf("%s lists %s but no module grants it through that type", typ, key)
			}
		}
	}
}

// The build account of a check spec is a Fugaro job account of this run.
func TestCoverCheckSpecBuildAccount(t *testing.T) {
	const reg = "us-east5-docker.pkg.dev/proj-1/"
	spec := func(extra string) string {
		return `{"registry":"` + reg + `r"` + extra + `}`
	}
	for name, tc := range map[string]struct {
		spec    string
		covered bool
	}{
		"as the module writes it":             {spec(`,"build_service_account":"` + okBuild + `"`), true},
		"in the firebase project":             {spec(`,"build_service_account":"fugaro-b-acme-webapp-5b8bba58@fp-2.iam.gserviceaccount.com"`), true},
		"missing":                             {spec(``), false},
		"empty":                               {spec(`,"build_service_account":""`), false},
		"null":                                {spec(`,"build_service_account":null`), false},
		"not a string":                        {spec(`,"build_service_account":7`), false},
		"a stranger in this project":          {spec(`,"build_service_account":"evil@proj-1.iam.gserviceaccount.com"`), false},
		"a fugaro-looking name, no hash":      {spec(`,"build_service_account":"fugaro-b-acme@proj-1.iam.gserviceaccount.com"`), false},
		"another project":                     {spec(`,"build_service_account":"fugaro-b-acme-webapp-5b8bba58@other.iam.gserviceaccount.com"`), false},
		"the scheduler account":               {spec(`,"build_service_account":"fugaro-scheduler@proj-1.iam.gserviceaccount.com"`), false},
		"not a service account email":         {spec(`,"build_service_account":"user:me@example.com"`), false},
		"a lookalike domain":                  {spec(`,"build_service_account":"fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com.evil.example"`), false},
		"case-folded duplicate, last wins":    {spec(`,"build_service_account":"` + okBuild + `","Build_Service_Account":"evil@proj-1.iam.gserviceaccount.com"`), false},
		"duplicate key, the good one is last": {spec(`,"build_service_account":"evil@x.example","build_service_account":"` + okBuild + `"`), true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{jobWithSpec(tc.spec, false)}}, nil); (len(got) == 0) != tc.covered {
				t.Fatalf("NotCovered = %q, covered want %v", got, tc.covered)
			}
		})
	}
}

// The matrix above follows the table, so it cannot tell a table that was
// edited. These cells are written out by hand from the modules and from what
// must never be so: a change to the table that flips one fails here.
func TestGrantRulesPinnedCells(t *testing.T) {
	const person = "user:me@example.com"
	const account = "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com"
	const (
		project = "google_project_iam_member"
		sacct   = "google_service_account_iam_member"
		bkt     = "google_storage_bucket_iam_member"
		repo    = "google_artifact_registry_repository_iam_member"
		secret  = "google_secret_manager_secret_iam_member"
		runjob  = "google_cloud_run_v2_job_iam_member"
		logview = "google_logging_log_view_iam_member"
	)
	for _, c := range []struct {
		typ, role, member string
		covered           bool
	}{
		// What the modules grant.
		{project, "projects/proj-1/roles/fugaroLauncher", person, true},
		{project, "roles/datastore.viewer", person, true},
		{project, "roles/firebasedatabase.admin", account, true},
		{project, "roles/logging.logWriter", account, true},
		{sacct, "projects/fp-2/roles/fugaroTokenMinter", person, true},
		{sacct, "roles/iam.serviceAccountUser", account, true},
		{bkt, "roles/storage.objectAdmin", person, true},
		{bkt, "roles/storage.objectUser", account, true},
		{repo, "projects/proj-1/roles/fugaroTagMover", account, true},
		{secret, "roles/secretmanager.secretVersionAdder", person, true},
		{secret, "roles/secretmanager.secretAccessor", account, true},
		{runjob, "projects/proj-1/roles/fugaroJobRunner", person, true},
		{runjob, "roles/run.invoker", account, true},
		{logview, "roles/logging.viewAccessor", person, true},
		// Roles on the wrong type, or to the wrong kind of principal.
		{bkt, "roles/storage.objectAdmin", account, false},
		{bkt, "roles/storage.objectUser", person, false},
		{project, "roles/storage.objectAdmin", person, false},
		{project, "roles/secretmanager.secretAccessor", account, false},
		{project, "roles/iam.serviceAccountUser", person, false},
		{project, "projects/proj-1/roles/fugaroTokenMinter", person, false},
		{project, "projects/proj-1/roles/fugaroLauncher", account, false},
		{project, "projects/proj-1/roles/fugaroJobRunner", person, false},
		{project, "roles/datastore.user", person, false},
		{project, "roles/firebaseauth.admin", person, false},
		{project, "roles/aiplatform.user", person, false},
		{sacct, "projects/proj-1/roles/fugaroTokenMinter", account, false},
		{sacct, "roles/storage.objectViewer", person, false},
		{repo, "projects/proj-1/roles/fugaroTagMover", person, false},
		{secret, "roles/secretmanager.secretAccessor", person, false},
		{secret, "roles/secretmanager.viewer", account, false},
		{runjob, "roles/run.invoker", person, false},
		{runjob, "roles/iam.serviceAccountUser", person, false},
		{logview, "roles/logging.viewAccessor", account, false},
		{logview, "roles/logging.logWriter", account, false},
	} {
		ch := rc(c.typ, map[string]any{"member": c.member, "role": c.role, "bucket": "fugaro-runs-proj-1"}, map[string]any{}, "create")
		got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
		if (len(got) == 0) != c.covered {
			t.Errorf("%s: %s to %s: NotCovered = %q, covered want %v", c.typ, c.role, c.member, got, c.covered)
		}
	}
}
