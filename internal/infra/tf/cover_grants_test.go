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
// only at the table level: binding (2)'s exact condition for the scoped role,
// which is bound with none for the other one. Every other role needs none.
func deployerCondFor(typ, role string) []any {
	if typ != "google_project_iam_member" || role != "projects/proj-1/roles/"+DeployerScopedRole {
		return nil
	}
	return []any{map[string]any{"title": DeployerConditionTitle, "expression": DeployerConditionExpression(), "description": nil}}
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
			for kind, member := range map[who]string{toPeople: person, toAccounts: account, toDeployer: "serviceAccount:" + DeployerAccount("proj-1")} {
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
		// deployer's two roles are not here either: their row is
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
			if w == 0 || w&^(toPeople|toAccounts|toDeployer) != 0 {
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

// The deployer's two roles are held by the deployer account and by nothing
// else, and the deployer account holds those two roles and nothing else: a
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
	scoped := "projects/proj-1/roles/" + DeployerScopedRole
	plain := "projects/proj-1/roles/" + DeployerRole
	exact := []any{map[string]any{"title": DeployerConditionTitle, "expression": DeployerConditionExpression(), "description": nil}}
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

// The deployer is its own kind of principal: only its two roles name it, and
// they name nothing else. A row that let an account or a person hold either
// role, or let the deployer hold any other, would undo the check above.
func TestOnlyTheDeployerRolesNameTheDeployer(t *testing.T) {
	for typ, row := range grantRules {
		for key, w := range row {
			isDeployerRole := typ == "google_project_iam_member" && (key == customKey+DeployerRole || key == customKey+DeployerScopedRole)
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

// The fence of D75 (Task 1.5 step 5), tested the way it is applied: a project
// binding of fugaroDeployerScoped is covered only under binding (2)'s exact
// condition — the managed prefix, per service, byte for byte — and one of
// fugaroDeployer only with no condition at all. A binding with the condition
// removed, with a wider one, with a narrowed one, with a condition known only
// after apply or with more than one condition hands the deployer every update,
// delete, setIamPolicy and actAs of the scoped role project-wide, which is the
// account that could then reach the runs bucket and forge the marker.
func TestDeployerGrantConditions(t *testing.T) {
	const account = "serviceAccount:fugaro-deployer@proj-1.iam.gserviceaccount.com"
	scoped := "projects/proj-1/roles/" + DeployerScopedRole
	plain := "projects/proj-1/roles/" + DeployerRole
	cond := func(title, expr string) []any {
		return []any{map[string]any{"title": title, "expression": expr, "description": nil}}
	}
	exact := cond(DeployerConditionTitle, DeployerConditionExpression())
	first := DeployerConditionClauses()[0].String()
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
		"the scoped role under a widened condition":            {role: scoped, member: account, after: map[string]any{"condition": cond(DeployerConditionTitle, DeployerConditionExpression()+" || true")}, covered: false, why: DeployerConditionTitle},
		"the scoped role under a narrowed condition":           {role: scoped, member: account, after: map[string]any{"condition": cond(DeployerConditionTitle, first)}, covered: false, why: DeployerConditionTitle},
		"the scoped role under another title":                  {role: scoped, member: account, after: map[string]any{"condition": cond("fugaro-something-else", DeployerConditionExpression())}, covered: false, why: DeployerConditionTitle},
		"the scoped role with a description":                   {role: scoped, member: account, after: map[string]any{"condition": []any{map[string]any{"title": DeployerConditionTitle, "expression": DeployerConditionExpression(), "description": "x"}}}, covered: false, why: "description"},
		"the scoped role with two conditions":                  {role: scoped, member: account, after: map[string]any{"condition": []any{exact[0], map[string]any{"title": DeployerConditionTitle, "expression": DeployerConditionExpression(), "description": nil}}}, covered: false, why: "more than one condition"},
		"the scoped role with a condition that is not a block": {role: scoped, member: account, after: map[string]any{"condition": []any{"true", "true"}}, covered: false, why: "not a condition block"},
		"the scoped role with an unknown condition":            {role: scoped, member: account, unknown: map[string]any{"condition": []any{map[string]any{"expression": true}}}, covered: false, why: "not known"},
		"the scoped role to a person":                          {role: scoped, member: "user:me@example.com", after: map[string]any{"condition": exact}, covered: false, why: "kind of principal"},
		"the plain role with no condition":                     {role: plain, member: account, covered: true},
		"the plain role under the scoped condition":            {role: plain, member: account, after: map[string]any{"condition": exact}, covered: false, why: "under a condition"},
		"the plain role under any condition":                   {role: plain, member: account, after: map[string]any{"condition": cond("fugaro-x", "true")}, covered: false, why: "under a condition"},
		"the plain role to a person":                           {role: plain, member: "user:me@example.com", covered: false, why: "kind of principal"},
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
