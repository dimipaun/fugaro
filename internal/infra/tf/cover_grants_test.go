package tf

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

func grantRoleString(key string) string {
	if id, ok := strings.CutPrefix(key, customKey); ok {
		return "projects/proj-1/roles/" + id
	}
	return key
}

// withCondFor gives a grant's cell the condition it needs so that
// bucketCondition and jobsCondition (added alongside grantRules' conditioned
// cells) never flip a cell these matrices test only at the table level: for a
// bucket objectUser, the launchers' exact condition for a person and a Fugaro
// prefix condition for an account; for the project's batch.jobsEditor, the
// compute module's job-name condition on proj-1. Every other role or type
// needs none, since neither function checks them.
func withCondFor(after map[string]any, typ, role string, person bool) {
	switch {
	case typ == "google_storage_bucket_iam_member" && role == "roles/storage.objectUser" && person:
		after["condition"] = []any{map[string]any{"title": gcp.LauncherBucketConditionTitle, "expression": gcp.LauncherBucketCondition("fugaro-runs-proj-1"), "description": nil}}
	case typ == "google_storage_bucket_iam_member" && role == "roles/storage.objectUser":
		after["condition"] = []any{map[string]any{"title": "fugaro-x", "expression": gcp.BucketCondition("fugaro-runs-proj-1", gcp.JobBucketPrefixes, "acme-0123456789abcdef"), "description": nil}}
	case typ == "google_project_iam_member" && role == "roles/batch.jobsEditor":
		after["project"] = "proj-1"
		after["condition"] = []any{map[string]any{"title": gcp.LauncherJobsConditionTitle, "expression": gcp.LauncherJobsCondition("proj-1", testCover.Region), "description": gcp.LauncherJobsConditionDescription}}
	}
}

// deployerCondFor is the condition a project grant of one of the deployer's
// roles needs so that deployerGrant never flips a cell this matrix tests
// only at the table level: each conditioned role's own condition, which is
// bound with none for the unconditioned one. Every other role needs none.
func deployerCondFor(typ, role string) []any {
	if typ != "google_project_iam_member" {
		return nil
	}
	id, ok := strings.CutPrefix(role, "projects/proj-1/roles/")
	if !ok || id == DeployerRole {
		return nil
	}
	expr, ok := DeployerConditionExpression("proj-1", id)
	if !ok {
		return nil
	}
	return []any{map[string]any{"title": DeployerConditionTitle, "expression": expr, "description": nil}}
}

// deployerGrantAccountFor is the account a grant of role through typ needs so
// that deployerGrant never flips a cell this matrix tests only at the table
// level: the token creator role is granted on the deployer account and on
// nothing else, and every other grant needs no account named.
func deployerGrantAccountFor(typ, role string) string {
	if typ != "google_service_account_iam_member" || role != DeployerTokenCreatorRole {
		return ""
	}
	return deployerAccountName("proj-1")
}

// Every (type, role, kind of principal) triple is covered exactly when the
// table says so. This is the mutation check of the table: dropping a row,
// adding a role to a type it does not belong to, or widening who may hold it
// flips a cell of this matrix.
func TestGrantRulesMatrix(t *testing.T) {
	const person = "user:me@example.com"
	const account = "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com"
	// The credentials service's account is a fourth kind of principal, not a
	// second spelling of `account`: only the two rows that name it may hold it,
	// and every other cell of the table refuses it (the ledger finding #903 —
	// while it was one of FugaroAccounts, every account row accepted it).
	const credentials = "serviceAccount:" + CredentialsAccountID + "@proj-1.iam.gserviceaccount.com"
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
			for kind, member := range map[who]string{toPeople: person, toAccounts: account, toDeployer: "serviceAccount:" + DeployerAccount("proj-1"), toCredentials: credentials} {
				want := row[key]&kind != 0
				after := map[string]any{"member": member, "role": role, "bucket": "fugaro-runs-proj-1"}
				if acct := deployerGrantAccountFor(typ, role); acct != "" {
					after["service_account_id"] = acct
				}
				withCondFor(after, typ, role, kind == toPeople)
				if cond := deployerCondFor(typ, role); cond != nil {
					after["condition"] = cond
				}
				ch := rc(typ, after, map[string]any{}, "create")
				got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
				if (len(got) == 0) != want {
					t.Errorf("%s: %s to %s: NotCovered = %q, covered want %v", typ, role, member, got, want)
				}
			}
			// A member computed from a service account created in the same
			// module is an account.
			sa := rc("google_service_account", map[string]any{}, map[string]any{}, "create")
			after := map[string]any{"role": role, "bucket": "fugaro-runs-proj-1"}
			if acct := deployerGrantAccountFor(typ, role); acct != "" {
				after["service_account_id"] = acct
			}
			withCondFor(after, typ, role, false)
			if cond := deployerCondFor(typ, role); cond != nil {
				after["condition"] = cond
			}
			ch := rc(typ, after, map[string]any{"member": true}, "create")
			got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{sa, ch}}, nil)
			if want := row[key]&toAccounts != 0; (len(got) == 0) != want {
				t.Errorf("%s: %s to a computed account: NotCovered = %q, covered want %v", typ, role, got, want)
			}
		}
		// Roles no module grants through any type.
		// tokenCreator is deliberately not among these any more: D75 grants it
		// on a service account for a person (the matrix's own row above). The
		// deployer's eight roles are not here either: their row is
		// google_project_iam_member's alone, so every other type refuses them.
		for _, role := range []string{"roles/owner", "roles/editor", "roles/viewer", "roles/iam.securityAdmin", "roles/storage.admin", "roles/run.admin",
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
			if w == 0 || w&^(toPeople|toAccounts|toDeployer|toCredentials) != 0 {
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
	// The credentials service's account (0.10.1, ledger #903): its own kind of
	// principal, so exactly two rows hold it.
	const credentials = "serviceAccount:" + CredentialsAccountID + "@proj-1.iam.gserviceaccount.com"
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
		// The whole-project inventory's read role, on the operator who runs
		// the managed apply that makes the read: the installation's own custom
		// role, holding cloudasset.assets.searchAllResources alone (Task 4.1a,
		// the owner's ruling of 2026-10-17).
		{project, "projects/proj-1/roles/" + CloudAssetRole, person, true},
		{project, "roles/firebasedatabase.admin", account, true},
		{project, "roles/logging.logWriter", account, true},
		{sacct, "projects/fp-2/roles/fugaroTokenMinter", person, true},
		{sacct, "roles/iam.serviceAccountUser", account, true},
		{bkt, "roles/storage.objectAdmin", person, true},
		{bkt, "roles/storage.objectUser", account, true},
		{bkt, "roles/storage.objectUser", person, true},
		{bkt, "roles/storage.objectViewer", person, true},
		{repo, "projects/proj-1/roles/fugaroTagMover", account, true},
		{secret, "roles/secretmanager.secretVersionAdder", person, true},
		{secret, "roles/secretmanager.secretAccessor", account, true},
		{runjob, "projects/proj-1/roles/fugaroJobRunner", person, true},
		{runjob, "roles/run.invoker", account, true},
		{logview, "roles/logging.viewAccessor", person, true},
		// The credentials service's two rows, and only these two (0.10.1,
		// ledger #903): the bundle's accessor and its own database role. The
		// database role is declared in the Firebase project (credentials.tf,
		// var.credentials_service.firebase_project), so the two-project shape
		// is a cell of its own — the account stays the installation's.
		{secret, "roles/secretmanager.secretAccessor", credentials, true},
		{project, "projects/proj-1/roles/" + CredentialsDatabaseRole, credentials, true},
		{project, "projects/fp-2/roles/" + CredentialsDatabaseRole, credentials, true},
		// ... and every other role it could be handed is refused, the two
		// predefined database roles first: viewer cannot write and admin could
		// delete the installation's database.
		{project, "roles/firebasedatabase.admin", credentials, false},
		{project, "roles/firebasedatabase.viewer", credentials, false},
		{project, "projects/proj-1/roles/" + CredentialsDatabaseRole, account, false},
		{project, "projects/proj-1/roles/" + CredentialsDatabaseRole, person, false},
		{project, "roles/logging.logWriter", credentials, false},
		{project, "roles/datastore.user", credentials, false},
		{project, "projects/proj-1/roles/fugaroHistory", credentials, false},
		{sacct, "roles/iam.serviceAccountUser", credentials, false},
		{bkt, "roles/storage.objectViewer", credentials, false},
		// Roles on the wrong type, or to the wrong kind of principal.
		{bkt, "roles/storage.objectAdmin", account, false},
		{project, "roles/storage.objectAdmin", person, false},
		{project, "roles/secretmanager.secretAccessor", account, false},
		{project, "roles/iam.serviceAccountUser", person, false},
		{project, "projects/proj-1/roles/fugaroTokenMinter", person, false},
		{project, "projects/proj-1/roles/fugaroLauncher", account, false},
		{project, "projects/proj-1/roles/fugaroJobRunner", person, false},
		{project, "roles/datastore.user", person, false},
		{project, "roles/cloudasset.viewer", account, false},
		// The wider predefined Cloud Asset roles, to anyone: the read is
		// SearchAllResources under the installation's own role, and a role
		// carrying cloudasset.assets.listResource is a call this read does not
		// make (Task 4.1a).
		{project, "roles/cloudasset.viewer", person, false},
		{project, "roles/cloudasset.owner", person, false},
		{project, "projects/proj-1/roles/" + CloudAssetRole, account, false},
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
		// These cells test checkGrant alone (the table), not the condition,
		// which TestCoverBucketConditions and TestCoverBatchJobsEditorCondition
		// cover: a conditioned cell carries the condition its kind of principal
		// needs, so neither condition check flips it to a problem the table did not predict.
		after := map[string]any{"member": c.member, "role": c.role, "bucket": "fugaro-runs-proj-1"}
		withCondFor(after, c.typ, c.role, c.member == person)
		ch := rc(c.typ, after, map[string]any{}, "create")
		got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
		if (len(got) == 0) != c.covered {
			t.Errorf("%s: %s to %s: NotCovered = %q, covered want %v", c.typ, c.role, c.member, got, c.covered)
		}
	}
}

// The deployer's eight roles are held by the deployer account and by nothing
// else, and the deployer account holds those eight roles and nothing else: a
// plan that binds the scoped role — even under its exact condition — to a job
// account, the scheduler or the history account hands that account every
// update, delete, setIamPolicy and actAs on the managed prefix; a plan that
// gives the deployer a role of Fugaro's own (the budget backend, Fugaro's
// secrets, its jobs, actAs on the scheduler) hands an agent-authored apply
// Fugaro's own authority. Grants ON the deployer account are the operators'
// token creation and nothing else: actAs on it for a job account is a job that
// runs as the deployer. Every refusal here was covered before the member was
// checked (Task 1.5 review, PR #505).
func TestDeployerRolesAndAccountAreTheDeployersAlone(t *testing.T) {
	const (
		project   = "google_project_iam_member"
		sacct     = "google_service_account_iam_member"
		bkt       = "google_storage_bucket_iam_member"
		secret    = "google_secret_manager_secret_iam_member"
		runjob    = "google_cloud_run_v2_job_iam_member"
		person    = "user:me@example.com"
		deployer  = "serviceAccount:fugaro-deployer@proj-1.iam.gserviceaccount.com"
		job       = "serviceAccount:fugaro-myrepo-0123abcd@proj-1.iam.gserviceaccount.com"
		scheduler = "serviceAccount:fugaro-scheduler@proj-1.iam.gserviceaccount.com"
		history   = "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com"
	)
	// One service's conditioned role stands for the seven in this matrix, which
	// is about the grant table and not about the conditions: the conditions are
	// tested one by one, per service, in TestDeployerGrantConditions.
	scoped := "projects/proj-1/roles/" + DeployerStorageRole
	plain := "projects/proj-1/roles/" + DeployerRole
	scopedExpr, _ := DeployerConditionExpression("proj-1", DeployerStorageRole)
	exact := []any{map[string]any{"title": DeployerConditionTitle, "expression": scopedExpr, "description": nil}}
	onDeployer := deployerAccountName("proj-1")
	onScheduler := "projects/proj-1/serviceAccounts/fugaro-scheduler@proj-1.iam.gserviceaccount.com"
	type tc struct {
		typ, member, role string
		after             map[string]any
		unknown           map[string]any
		extra             []ResourceChange
		covered           bool
	}
	cases := map[string]tc{
		// The controls: the module's own three grants about the deployer.
		"the deployer holding its scoped role":     {typ: project, member: deployer, role: scoped, after: map[string]any{"condition": exact}, covered: true},
		"the deployer holding its plain role":      {typ: project, member: deployer, role: plain, covered: true},
		"an operator holding token creation on it": {typ: sacct, member: person, role: DeployerTokenCreatorRole, after: map[string]any{"service_account_id": onDeployer}, covered: true},
		// (a) the deployer's roles to anyone else.
		"the scoped role to a job account":       {typ: project, member: job, role: scoped, after: map[string]any{"condition": exact}},
		"the scoped role to the scheduler":       {typ: project, member: scheduler, role: scoped, after: map[string]any{"condition": exact}},
		"the scoped role to the history account": {typ: project, member: history, role: scoped, after: map[string]any{"condition": exact}},
		"the plain role to a job account":        {typ: project, member: job, role: plain},
		"the plain role to the scheduler":        {typ: project, member: scheduler, role: plain},
		"the scoped role to a computed account":  {typ: project, role: scoped, after: map[string]any{"condition": exact}, unknown: map[string]any{"member": true}, extra: []ResourceChange{rc("google_service_account", map[string]any{"account_id": "fugaro-x-0123abcd"}, map[string]any{}, "create")}},
		"the plain role to a computed account":   {typ: project, role: plain, unknown: map[string]any{"member": true}, extra: []ResourceChange{rc("google_service_account", map[string]any{"account_id": "fugaro-x-0123abcd"}, map[string]any{}, "create")}},
		"the plain role to an empty member":      {typ: project, member: "", role: plain},
		"the deployer of another project":        {typ: project, member: "serviceAccount:fugaro-deployer@other.iam.gserviceaccount.com", role: plain},
		// The deployer lives in the installation's project (Projects[0]), never
		// in the verified Firebase project the installation module is not applied to.
		"a deployer in the firebase project":        {typ: project, member: "serviceAccount:fugaro-deployer@fp-2.iam.gserviceaccount.com", role: "projects/fp-2/roles/" + DeployerRole},
		"token creation on a firebase deployer":     {typ: sacct, member: person, role: DeployerTokenCreatorRole, after: map[string]any{"service_account_id": deployerAccountName("fp-2")}},
		"the deployer, another project's role":      {typ: project, member: "serviceAccount:fugaro-deployer@fp-2.iam.gserviceaccount.com", role: plain},
		"the deployer, its role in another project": {typ: project, member: deployer, role: plain, after: map[string]any{"project": "fp-2"}},
		"a lookalike deployer address":              {typ: project, member: "serviceAccount:fugaro-deployer-0123abcd@proj-1.iam.gserviceaccount.com", role: plain},
		// (b) grants ON the deployer account other than the operators' token creation.
		"actAs on the deployer for a job account":  {typ: sacct, member: job, role: "roles/iam.serviceAccountUser", after: map[string]any{"service_account_id": onDeployer}},
		"actAs on the deployer for a person":       {typ: sacct, member: person, role: "roles/iam.serviceAccountUser", after: map[string]any{"service_account_id": onDeployer}},
		"the token minter on the deployer":         {typ: sacct, member: person, role: "projects/proj-1/roles/fugaroTokenMinter", after: map[string]any{"service_account_id": onDeployer}},
		"token creation on the deployer for a job": {typ: sacct, member: job, role: DeployerTokenCreatorRole, after: map[string]any{"service_account_id": onDeployer}},
		"actAs on a computed account beside it":    {typ: sacct, member: person, role: "roles/iam.serviceAccountUser", unknown: map[string]any{"service_account_id": true}, extra: []ResourceChange{rc("google_service_account", map[string]any{"account_id": DeployerAccountID}, map[string]any{}, "create")}},
		// And the installation module's OWN scheduler grant, in the same
		// deployer-creating module: the operators' actAs on the scheduler
		// account, covered because the module names it at plan time
		// (installation/scheduler.tf, the literal form deployer.tf's
		// token-creator grant uses). The row above is the same shape with the
		// account computed, and it is refused — which is what a scheduler.tf
		// that read google_service_account.scheduler.name produced, making
		// the installation apply ask its own confirmation and refuse a grant
		// that is the module's own.
		"the operators' actAs on the scheduler, named literally": {typ: sacct, member: person, role: "roles/iam.serviceAccountUser", after: map[string]any{"service_account_id": onScheduler}, extra: []ResourceChange{rc("google_service_account", map[string]any{"account_id": DeployerAccountID}, map[string]any{}, "create")}, covered: true},
		// (c) other roles to the deployer.
		"firebaseauth.admin to the deployer":         {typ: project, member: deployer, role: "roles/firebaseauth.admin"},
		"firebasedatabase.admin to the deployer":     {typ: project, member: deployer, role: "roles/firebasedatabase.admin"},
		"logWriter to the deployer":                  {typ: project, member: deployer, role: "roles/logging.logWriter"},
		"a computed custom role to the deployer":     {typ: project, member: deployer, unknown: map[string]any{"role": true}, extra: []ResourceChange{customRole("fugaroJobRunner", "proj-1", []any{"run.jobs.run", "run.jobs.runWithOverrides"})}},
		"secretAccessor on a secret to the deployer": {typ: secret, member: deployer, role: "roles/secretmanager.secretAccessor"},
		"the job runner on a job to the deployer":    {typ: runjob, member: deployer, role: "projects/proj-1/roles/fugaroJobRunner"},
		"run.invoker on a job to the deployer":       {typ: runjob, member: deployer, role: "roles/run.invoker"},
		"actAs on the scheduler to the deployer":     {typ: sacct, member: deployer, role: "roles/iam.serviceAccountUser", after: map[string]any{"service_account_id": onScheduler}},
		"object access on the runs bucket":           {typ: bkt, member: deployer, role: "roles/storage.objectViewer", after: map[string]any{"bucket": "fugaro-runs-proj-1"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			after := map[string]any{"role": c.role}
			if c.unknown["member"] != true {
				after["member"] = c.member
			}
			if c.unknown["role"] == true {
				delete(after, "role")
			}
			maps.Copy(after, c.after)
			unk := map[string]any{}
			maps.Copy(unk, c.unknown)
			changes := append(slices.Clone(c.extra), rc(c.typ, after, unk, "create"))
			got := testCover.NotCovered(&Plan{ResourceChanges: changes}, nil)
			if (len(got) == 0) != c.covered {
				t.Fatalf("NotCovered = %q, covered want %v", got, c.covered)
			}
		})
	}
}

// The whole-project inventory's read role is the installation's own custom
// role, holding exactly the one permission SearchAllResources needs and no
// other (Task 4.1a, the owner's ruling of 2026-10-17). The wider predefined
// role Task 4.1 first granted is gone from the table: no Cloud Asset role but
// this one is covered, on any type, and this one is held by people alone. A pin
// that gained cloudasset.assets.listResource, or a row that readmitted a
// predefined Cloud Asset role, would hand every operator a read the inventory
// never makes — ListAssets is not called anywhere.
func TestCloudAssetRoleIsTheSearchReadOnly(t *testing.T) {
	want := []string{"cloudasset.assets.searchAllResources"}
	if got := RolePermissions[CloudAssetRole]; !slices.Equal(got, want) {
		t.Errorf("RolePermissions[%s] = %v, want exactly %v", CloudAssetRole, got, want)
	}
	if !slices.Contains(FugaroRoles, CloudAssetRole) {
		t.Errorf("FugaroRoles does not name %s, so a grant of the role the module creates is refused", CloudAssetRole)
	}
	if w := grantRules["google_project_iam_member"][customKey+CloudAssetRole]; w != toPeople {
		t.Errorf("google_project_iam_member %s is held by %d, want people alone", CloudAssetRole, w)
	}
	for typ, row := range grantRules {
		for key, w := range row {
			if !strings.Contains(key, "cloudasset") {
				continue
			}
			if key != customKey+CloudAssetRole {
				t.Errorf("%s %s is covered: the inventory's read is the installation's own %s, and no Cloud Asset role but it", typ, key, CloudAssetRole)
			}
			if w&toAccounts != 0 || w&toDeployer != 0 {
				t.Errorf("%s %s may be held by an account or the deployer, want people alone", typ, key)
			}
		}
	}
	for _, id := range FugaroRoles {
		for _, p := range RolePermissions[id] {
			if strings.HasPrefix(p, "cloudasset.") && p != "cloudasset.assets.searchAllResources" {
				t.Errorf("%s holds %s: the installation's only Cloud Asset permission is the search read", id, p)
			}
		}
	}
}

// The deployer is its own kind of principal: only its eight roles name it, and
// they name nothing else. A row that let an account or a person hold either
// role, or let the deployer hold any other, would undo the check above.
func TestOnlyTheDeployerRolesNameTheDeployer(t *testing.T) {
	for typ, row := range grantRules {
		for key, w := range row {
			isDeployerRole := typ == "google_project_iam_member" && (key == customKey+DeployerRole || slices.Contains(DeployerServiceRoles(), strings.TrimPrefix(key, customKey)))
			if isDeployerRole && w != toDeployer {
				t.Errorf("%s %s is held by %d, want the deployer alone", typ, key, w)
			}
			if !isDeployerRole && w&toDeployer != 0 {
				t.Errorf("%s %s may be held by the deployer", typ, key)
			}
		}
	}
	if slices.Contains(FugaroAccounts, DeployerAccountID) {
		t.Error("the deployer is one of FugaroAccounts, so every account row accepts it")
	}
}

// The credentials service's account is its own kind of principal (0.10.1,
// ledger #903), and this is the test the finding asked for: a role granted to
// it OUTSIDE its allowed set fails the cover.
//
// The finding, exactly. While fugaro-credentials was one of FugaroAccounts, the
// toAccounts rows accepted it: a plan — written by hand, by an agent, or by a
// module edited in a hurry — granting the service account roles/datastore.user,
// roles/batch.jobsEditor, a job runner's run.jobs.run or a launcher's role was
// covered by init's ONE typed confirmation, because the account's NAME was on a
// list that means "a Fugaro service account of this run" and never "an account
// that may hold this role". The service reads one secret and one database and
// must hold nothing else: a role it does not need is reach at the budget
// backend, at Fugaro's own secrets or at the authority to run jobs, held by the
// one identity that holds every model credential.
//
// So both halves are asserted: fugaro-credentials is NOT in FugaroAccounts (the
// list, which is where the finding started), and the table plus the cover refuse
// it every role but its two — the table row by row, and the cover for real, on
// the roles the modules grant elsewhere, on the roles the table does not name at
// all, and on the one predefined role that would be the easy mistake
// (roles/firebasedatabase.admin, which could delete the installation's
// database).
func TestCredentialsAccountIsItsOwnPrincipalKind(t *testing.T) {
	const credentials = "serviceAccount:" + CredentialsAccountID + "@proj-1.iam.gserviceaccount.com"

	if slices.Contains(FugaroAccounts, CredentialsAccountID) {
		t.Errorf("the credentials service's account is one of FugaroAccounts, so every toAccounts row accepts it: one confirmation then covers a grant of the history account's datastore.user, a job runner's or a launcher's role to the identity that holds every model credential (ledger #903)")
	}
	// The two rows it may hold, and nothing else. A row that gains
	// toCredentials is a role this account may be granted, so it is a decision
	// this test makes deliberate.
	allowed := map[string]map[string]bool{
		"google_secret_manager_secret_iam_member": {"roles/secretmanager.secretAccessor": true},
		"google_project_iam_member":               {customKey + CredentialsDatabaseRole: true},
	}
	for typ, row := range grantRules {
		for key, w := range row {
			if (w&toCredentials != 0) != allowed[typ][key] {
				t.Errorf("%s %s: toCredentials is %v, want %v — the service's account holds the bundle's accessor and its database role, and no other role of the installation", typ, key, w&toCredentials != 0, allowed[typ][key])
			}
		}
	}
	for typ, row := range allowed {
		for key := range row {
			if grantRules[typ][key]&toCredentials == 0 {
				t.Errorf("%s %s does not accept the credentials service's account, so the module's own grant of it is refused and init cannot cover the plan that turns the service on", typ, key)
			}
		}
	}

	// ... and the cover, role by role. Every role of every row, the roles the
	// table names for other kinds of principal included, plus roles no row
	// names at all.
	roles := map[string][]string{}
	for typ, row := range grantRules {
		for key := range row {
			roles[typ] = append(roles[typ], grantRoleString(key))
		}
	}
	roles["google_project_iam_member"] = append(roles["google_project_iam_member"],
		"roles/owner", "roles/editor", "roles/firebasedatabase.admin", "roles/firebasedatabase.viewer",
		"projects/proj-1/roles/fugaroEverything", "projects/fp-2/roles/"+CredentialsDatabaseRole)
	for typ, list := range roles {
		for _, role := range slices.Sorted(slices.Values(list)) {
			after := map[string]any{"member": credentials, "role": role, "bucket": "fugaro-runs-proj-1"}
			withCondFor(after, typ, role, false)
			ch := rc(typ, after, map[string]any{}, "create")
			got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
			want := allowed[typ][mustRoleKey(t, role)]
			if (len(got) == 0) != want {
				t.Errorf("%s: %s to the credentials service's account: NotCovered = %q, covered want %v", typ, role, got, want)
			}
		}
	}

	// A member computed from a service account the module creates is one of
	// Fugaro's accounts, and it is NOT this one: the database role to a computed
	// account is refused, the way the deployer's own bindings are (a plan that
	// hands this role to an account nobody can name is not the module's). The
	// module's own grant names its member, which is a literal plan-time string:
	// google_service_account.credentials[0].member is
	// serviceAccount:<account_id>@<project>.iam.gserviceaccount.com as soon as
	// the account's ID and project are known.
	sa := rc("google_service_account", map[string]any{}, map[string]any{}, "create")
	ch := rc("google_project_iam_member", map[string]any{"role": "projects/proj-1/roles/" + CredentialsDatabaseRole, "bucket": "fugaro-runs-proj-1"}, map[string]any{"member": true}, "create")
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{sa, ch}}, nil); len(got) == 0 {
		t.Errorf("the database role to a computed account is covered: only the credentials service's own account may hold it")
	}

	// The account in ANOTHER project is not it either: the module creates it in
	// the installation's project, and the role's project does not move the
	// account (a lookalike address in the Firebase project is refused).
	for _, member := range []string{
		"serviceAccount:" + CredentialsAccountID + "@fp-2.iam.gserviceaccount.com",
		"serviceAccount:" + CredentialsAccountID + "-0123abcd@proj-1.iam.gserviceaccount.com",
	} {
		after := map[string]any{"member": member, "role": "projects/fp-2/roles/" + CredentialsDatabaseRole, "bucket": "fugaro-runs-proj-1"}
		ch := rc("google_project_iam_member", after, map[string]any{}, "create")
		if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
			t.Errorf("%s: the database role is covered: the service's account is %s@proj-1 and nothing else", member, CredentialsAccountID)
		}
	}

	// The account is not a person either: a review screen that listed its
	// address (a project admin who added it by mistake) does not make any role
	// grantable to it.
	listed := Cover{Projects: testCover.Projects, Listed: []string{credentials}, Region: testCover.Region, Buckets: testCover.Buckets}
	for _, tc := range []struct {
		role    string
		covered bool
	}{
		{"projects/proj-1/roles/" + CredentialsDatabaseRole, true},
		{"roles/logging.logWriter", false},
		{"roles/datastore.user", false},
	} {
		after := map[string]any{"member": credentials, "role": tc.role, "bucket": "fugaro-runs-proj-1"}
		ch := rc("google_project_iam_member", after, map[string]any{}, "create")
		if got := listed.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); (len(got) == 0) != tc.covered {
			t.Errorf("listed on the review screen: %s to the credentials account: NotCovered = %q, covered want %v", tc.role, got, tc.covered)
		}
	}
}

// mustRoleKey is grantRules' key for a role as a module writes it, for the
// table lookups above. It is the cover's own roleKey, so the test cannot
// disagree with the code about which row a role lands on.
func mustRoleKey(t *testing.T, role string) string {
	t.Helper()
	key, ok := testCover.roleKey(role)
	if !ok {
		return role
	}
	return key
}

// The fence of D75 (Task 1.5 step 5), tested the way it is applied: a project
// binding of one of the deployer's conditioned roles is covered only under that
// role's own condition — the managed prefix for its own service, byte for byte
// — and one of fugaroDeployer only with no condition at all. A binding carrying
// ANOTHER service's condition is refused too: it is the wrong fence, and the
// review cannot tell a mistake from a widening. A binding with the condition
// removed, with a wider one, with a narrowed one, with a condition known only
// after apply or with more than one condition hands the deployer every update,
// delete, setIamPolicy and actAs of the scoped role project-wide, which is the
// account that could then reach the runs bucket and forge the marker.
//
// The condition known only after apply is a row for EACH of the two roles, and
// the plain role is not the scoped one's case under another name. What catches
// a condition the plan has not decided is the check's own `unknownAt` clauses,
// which read `after_unknown` by path — condition[0].title, .expression,
// .description — so they catch it whether the whole condition is computed or
// only its expression is. The row the plain role needs is the WHOLLY computed
// one: a condition the plan does not know at all is in `after` as nothing, so
// the "under a condition" branch has no condition to see and the binding reads
// as the modules' plain one — `fugaroDeployer` decided during apply, on the
// account the fence exists for, with no refusal at all. A plan whose expression
// alone is computed still carries its block in `after` (with the expression
// null), so the plain branch refuses that one as "under a condition" with the
// check gone and without it: that row is held to the refusal naming the unknown
// condition, not to the binding being accepted.
//
// Mutation (verified, run and restored): removing the check's `unknownAt`
// clauses fails the row whose expression alone is computed; removing the whole
// check fails the wholly computed row with no refusal at all, and the scoped
// role's unknown-condition row above with a refusal that is not the one it
// names — so that row is evidence about the message and not about acceptance.
func TestDeployerGrantConditions(t *testing.T) {
	const account = "serviceAccount:fugaro-deployer@proj-1.iam.gserviceaccount.com"
	scoped := "projects/proj-1/roles/" + DeployerStorageRole
	plain := "projects/proj-1/roles/" + DeployerRole
	cond := func(title, expr string) []any {
		return []any{map[string]any{"title": title, "expression": expr, "description": nil}}
	}
	scopedExpr, _ := DeployerConditionExpression("proj-1", DeployerStorageRole)
	otherExpr, _ := DeployerConditionExpression("proj-1", DeployerRunJobRole)
	exact := cond(DeployerConditionTitle, scopedExpr)
	cls, _ := DeployerConditionClauses("proj-1", DeployerStorageRole)
	first := cls[0].String()
	for name, tc := range map[string]struct {
		role    string
		member  string
		after   map[string]any
		unknown map[string]any
		covered bool
		why     string
	}{
		"the scoped role under its condition":                  {role: scoped, member: account, after: map[string]any{"condition": exact}, covered: true},
		"the scoped role with no condition":                    {role: scoped, member: account, covered: false, why: DeployerConditionTitle},
		"the scoped role under a widened condition":            {role: scoped, member: account, after: map[string]any{"condition": cond(DeployerConditionTitle, scopedExpr+" || true")}, covered: false, why: DeployerConditionTitle},
		"the scoped role under another service's condition":    {role: scoped, member: account, after: map[string]any{"condition": cond(DeployerConditionTitle, otherExpr)}, covered: false, why: DeployerConditionTitle},
		"the scoped role under a narrowed condition":           {role: scoped, member: account, after: map[string]any{"condition": cond(DeployerConditionTitle, first)}, covered: false, why: DeployerConditionTitle},
		"the scoped role under another title":                  {role: scoped, member: account, after: map[string]any{"condition": cond("fugaro-something-else", scopedExpr)}, covered: false, why: DeployerConditionTitle},
		"the scoped role with a description":                   {role: scoped, member: account, after: map[string]any{"condition": []any{map[string]any{"title": DeployerConditionTitle, "expression": scopedExpr, "description": "x"}}}, covered: false, why: "description"},
		"the scoped role with two conditions":                  {role: scoped, member: account, after: map[string]any{"condition": []any{exact[0], map[string]any{"title": DeployerConditionTitle, "expression": scopedExpr, "description": nil}}}, covered: false, why: "more than one condition"},
		"the scoped role with a condition that is not a block": {role: scoped, member: account, after: map[string]any{"condition": []any{"true", "true"}}, covered: false, why: "not a condition block"},
		"the scoped role with an unknown condition":            {role: scoped, member: account, after: map[string]any{"condition": []any{map[string]any{"title": DeployerConditionTitle, "expression": nil, "description": nil}}}, unknown: map[string]any{"condition": []any{map[string]any{"expression": true}}}, covered: false, why: "not known"},
		"the scoped role to a person":                          {role: scoped, member: "user:me@example.com", after: map[string]any{"condition": exact}, covered: false, why: "kind of principal"},
		"the plain role with no condition":                     {role: plain, member: account, covered: true},
		"the plain role under the scoped condition":            {role: plain, member: account, after: map[string]any{"condition": exact}, covered: false, why: "under a condition"},
		"the plain role under any condition":                   {role: plain, member: account, after: map[string]any{"condition": cond("fugaro-x", "true")}, covered: false, why: "under a condition"},
		// The plain role's condition being UNKNOWN is its own row, and it is the
		// one the two rows above cannot stand in for. The wholly computed
		// condition is the plan shape that leaves `after` with no condition at
		// all — the "under a condition" branch then sees nothing and the binding
		// reads as the modules' plain one, which leaves `fugaroDeployer` decided
		// during apply, on the account the fence exists for — and the second row
		// is the other half of the same `unknownAt` read: a block that is in
		// `after` with its expression known only after apply.
		"the plain role with a wholly computed condition":        {role: plain, member: account, unknown: map[string]any{"condition": true}, covered: false, why: "not known"},
		"the plain role with a condition known only after apply": {role: plain, member: account, after: map[string]any{"condition": []any{map[string]any{"title": DeployerConditionTitle, "expression": nil, "description": nil}}}, unknown: map[string]any{"condition": []any{map[string]any{"expression": true}}}, covered: false, why: "not known"},
		"the plain role to a person":                             {role: plain, member: "user:me@example.com", covered: false, why: "kind of principal"},
	} {
		t.Run(name, func(t *testing.T) {
			after := map[string]any{"member": tc.member, "role": tc.role}
			maps.Copy(after, tc.after)
			unk := map[string]any{}
			maps.Copy(unk, tc.unknown)
			ch := rc("google_project_iam_member", after, unk, "create")
			got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
			if (len(got) == 0) != tc.covered {
				t.Fatalf("NotCovered = %q, covered want %v", got, tc.covered)
			}
			if !tc.covered && tc.why != "" && !strings.Contains(strings.Join(got, "\n"), tc.why) {
				t.Errorf("refused for another reason: %q, want it to name %q", got, tc.why)
			}
		})
	}
}

// deployerGrant checks the member itself, and that check is not reachable
// through NotCovered: the grant table holds every one of the eight roles to
// toDeployer (TestOnlyTheDeployerRolesNameTheDeployer pins the table), so
// checkGrant turns a deployer role held by anyone else away before
// deployerGrant runs. It is there because deployerGrant is the one function
// that reads a deployer binding, and a widening of the table — the kind of
// change the table's own tests would have to notice for this one to stay
// correct — would otherwise open the deployer path with nothing in this
// function to object.
//
// So it is pinned here, directly: the test calls deployerGrant with a binding
// that is the modules' in every other respect, and the member is what it must
// refuse for. Delete the check and this test fails with the empty reason the
// function then returns, which is what keeps a later cleanup from deleting it
// as dead code.
func TestDeployerGrantRefusesAMemberThatIsNotTheDeployer(t *testing.T) {
	scoped := "projects/proj-1/roles/" + DeployerStorageRole
	plain := "projects/proj-1/roles/" + DeployerRole
	expr, ok := DeployerConditionExpression("proj-1", DeployerStorageRole)
	if !ok {
		t.Fatalf("no condition for %s", DeployerStorageRole)
	}
	cond := []any{map[string]any{"title": DeployerConditionTitle, "expression": expr, "description": nil}}
	// want is the reason the member check produces, which names the account the
	// binding should have been on.
	want := "which is not serviceAccount:" + DeployerAccount("proj-1") + ": the deployer's roles are held by the deployer account and by nothing else (D75)"
	for name, tc := range map[string]struct {
		member string
		role   string
	}{
		"another Fugaro account":     {"serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com", scoped},
		"a person":                   {"user:me@example.com", scoped},
		"a lookalike address":        {"serviceAccount:fugaro-deployer-0123abcd@proj-1.iam.gserviceaccount.com", plain},
		"another project's deployer": {"serviceAccount:fugaro-deployer@fp-2.iam.gserviceaccount.com", plain},
	} {
		t.Run(name, func(t *testing.T) {
			// The binding is the modules' in every other respect — the
			// conditioned role under its own condition, the plain one under
			// none — so the member is the only thing left to refuse.
			after := map[string]any{"member": tc.member, "role": tc.role, "project": "proj-1"}
			if tc.role == scoped {
				after["condition"] = cond
			}
			ch := rc("google_project_iam_member", after, map[string]any{}, "create")
			got := testCover.deployerGrant("google_project_iam_member", ch.Change, false)
			if !strings.Contains(got, want) {
				t.Errorf("deployerGrant = %q, want it to name the member: %q", got, want)
			}
		})
	}
	// The control: the same binding on the deployer account is the modules', so
	// the rows above are refused for the member and for nothing else.
	after := map[string]any{"member": "serviceAccount:" + DeployerAccount("proj-1"), "role": scoped, "project": "proj-1", "condition": cond}
	ch := rc("google_project_iam_member", after, map[string]any{}, "create")
	if got := testCover.deployerGrant("google_project_iam_member", ch.Change, false); got != "" {
		t.Errorf("deployerGrant on the deployer account = %q, want the modules' binding", got)
	}
}
