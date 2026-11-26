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
	case typ == "google_project_iam_member" && role == dynamicSecretsRole(IntakeSecretsRole):
		after["project"] = "proj-1"
		after["condition"] = dynamicSecretsCondition()
	case typ == "google_project_iam_member" && role == "roles/secretmanager.secretAccessor":
		// The service's prefix accessor (D1931, DS2.2): the same fence, and it
		// is a PROJECT grant, so dynamicSecretsGrant is what checks it.
		after["project"] = "proj-1"
		after["condition"] = dynamicSecretsCondition()
	case typ == "google_storage_bucket_iam_member" && role == "roles/storage.objectViewer" && strings.HasPrefix(str(after["member"]), "serviceAccount:"+IntakeAccountID+"@"):
		// The intake's one bucket read: objectViewer on the derived admin
		// list's single object, under DS2.0's condition — the object, byte for
		// byte, from the builder the check reads (intakeAdminListGrant).
		after["condition"] = []any{map[string]any{"title": gcp.IntakeAdminListConditionTitle, "expression": IntakeAdminListCondition("fugaro-runs-proj-1"), "description": nil}}
	case typ == "google_project_iam_member" && role == dynamicSecretsRole(SecretListerRole):
		// ... and the lister, which must carry NO condition: Check 510 refused
		// secretmanager.secrets.list under the fence, which is why the
		// permission is a role of its own (dynamicSecretsGrant requires the
		// empty condition list).
		after["project"] = "proj-1"
	}
}

// dynamicSecretsRole is the full name of one of the dynamic class's custom
// roles, and dynamicSecretsCondition the class's fence as a plan writes it —
// the one condition the intake's writes and the service's prefix accessor are
// both bound under.
func dynamicSecretsRole(id string) string {
	return "projects/proj-1/roles/" + id
}

func dynamicSecretsCondition() []any {
	return []any{map[string]any{"title": gcp.DynamicSecretNamesConditionTitle, "expression": gcp.DynamicSecretNamesCondition(), "description": nil}}
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

// grantAccountFor is the account a grant of role through typ needs so that the
// rules which narrow a role to ONE target account never flip a cell a matrix
// tests only at the table level: the token creator role is granted on the
// deployer account and on nothing else, the minter role on a signer account and
// on nothing else (orchestratorMinterGrant, mobileMinterGrant, issuerMinterGrant
// — three per-account rules of one role), and every other grant needs no
// account named.
//
// Why one helper for both. The matrices that call it iterate every (type, role,
// kind) triple and read their expectation off the grantRules cell alone; a role
// whose cell is open to a kind but whose grant is then refused for naming no
// account would read as a table that refuses what it carries. The account here
// is the one the MODULES name — the deployer for the token creator, the runs'
// signer for the minter — so a cell the table opens is reached in the shape a
// module writes it. What narrows the role to that account is the per-account
// rule's own test, not this shim.
func grantAccountFor(typ, role string) string {
	if typ != "google_service_account_iam_member" {
		return ""
	}
	switch role {
	case DeployerTokenCreatorRole:
		return deployerAccountName("proj-1")
	case "projects/proj-1/roles/fugaroTokenMinter":
		// The runs' signer in the Firebase project, which is fp-2 in the
		// matrices' cover: the minter role's key is written with the
		// installation's project (grantRoleString builds it that way) while the
		// signer account belongs to the Firebase one, so the two halves of the
		// grant name two projects — which is exactly what issuerMinterGrant and
		// orchestratorMinterGrant read, since both are this run's projects.
		return "projects/fp-2/serviceAccounts/fugaro-token-signer@fp-2.iam.gserviceaccount.com"
	case "roles/iam.serviceAccountOpenIdTokenCreator":
		return "projects/proj-1/serviceAccounts/" + BuildIdentityAccountID + "@proj-1.iam.gserviceaccount.com"
	}
	return ""
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
	// The verifier account is a fifth (CSR14): its one row is the credentials
	// service's invoker, and every other cell refuses it.
	const verifier = "serviceAccount:" + VerifierAccountID + "@proj-1.iam.gserviceaccount.com"
	const coordinator = "serviceAccount:" + CoordinatorAccountID + "@proj-1.iam.gserviceaccount.com"
	// The project's build identity (B5, D2301) is a sixth, and it is in this
	// map for the verifier's reason: its one row is the same service's invoker
	// bit, that row's grant needs no condition for the check to accept it, so
	// every other cell of the table has to refuse it here rather than in a test
	// of its own.
	const buildIdentity = "serviceAccount:" + BuildIdentityAccountID + "@proj-1.iam.gserviceaccount.com"
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
			for kind, member := range map[who]string{toPeople: person, toAccounts: account, toDeployer: "serviceAccount:" + DeployerAccount("proj-1"), toCredentials: credentials, toCoordinator: coordinator, toVerifier: verifier, toBuildIdentity: buildIdentity} {
				want := row[key]&kind != 0
				after := map[string]any{"member": member, "role": role, "bucket": "fugaro-runs-proj-1"}
				if acct := grantAccountFor(typ, role); acct != "" {
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
			if acct := grantAccountFor(typ, role); acct != "" {
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
			// The bits the table may name, enumerated so that a new kind is a
			// deliberate line here and not a row nobody classified (P5.3.3
			// added the hop's account, P5 the public mobile service's; both of
			// those accounts' allUsers bindings are deliberately not a kind —
			// cover.go's toVoice and toMobile say why).
			if w == 0 || w&^(toPeople|toAccounts|toDeployer|toCredentials|toCoordinator|toVerifier|toCatalogCheck|toMCP|toIntake|toReports|toLicence|toOrchestrator|toVoice|toBuildIdentity|toMobile) != 0 {
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
		runsvc  = "google_cloud_run_v2_service_iam_member"
		logview = "google_logging_log_view_iam_member"
	)
	// The project's build identity (B5, D2301), whose cells below are pinned:
	// the invoker bit on the credentials service, the writer and tag-mover
	// pair on the shared registry, the reader on the two registries a build
	// pulls from, the bucket's read of the copied definitions and write of one
	// repository's record, the provider credential a build clones with, the
	// one project-level role its own step logs need (B6), and no role of any
	// other row.
	const buildIdentity = "serviceAccount:" + BuildIdentityAccountID + "@proj-1.iam.gserviceaccount.com"
	// One repository's build account (B4.4, the member
	// TestBuildAccountInvokesOnlyTheService reads out of the repo module): the
	// shape a job account's member has under the same regex, which is why its
	// cell below is an account's and not a kind of its own.
	const buildAccount = "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1.iam.gserviceaccount.com"
	for _, c := range []struct {
		typ, role, member string
		covered           bool
	}{
		// What the modules grant.
		{project, "projects/proj-1/roles/fugaroLauncher", person, true},
		// The viewer's read-only job role (RS4, roles.md §4): a person the
		// review screen lists, and no account of Fugaro's own — the row's
		// kinds are the whole of its condition, and
		// TestViewerRoleIsGrantedToListedPeopleAlone reads every one of them.
		{project, "projects/proj-1/roles/" + ViewerRole, person, true},
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
		// The project's build identity's four cells (B5, D2301; the registry
		// roles added by B6): the invoker bit on the SERVICE type, the class
		// the service answers the build route for, the writer and the tag
		// mover on the ONE shared registry every catalog image lands in, and
		// the reader on the base and ghcr registries a build's own steps pull
		// through — the whole of what the account may hold.
		{runsvc, "roles/run.invoker", buildIdentity, true},
		// The build account's one cell of its own (B4.4): the same invoker
		// bit, on the same service, held by a repository's build account
		// rather than by the project's build identity. It is an account's cell
		// and not a kind's, and this is where that is pinned: the member is
		// fugaro-b-<readable>-<8 hex>, which the table reads as one of
		// Fugaro's accounts (jobAccountID), so a change that took toAccounts
		// off this row would take the reach away from both kinds of account
		// and fail here first.
		{runsvc, "roles/run.invoker", buildAccount, true},
		{repo, "roles/artifactregistry.writer", buildIdentity, true},
		{repo, "projects/proj-1/roles/fugaroTagMover", buildIdentity, true},
		// The reader is not a fifth cell of its own: this type's row carries
		// one reader cell for every principal that pulls an image, and the
		// identity is one of them now that a catalog build's first pull — the
		// credential step's own base image — is made as the account the build
		// runs as.
		{repo, "roles/artifactregistry.reader", buildIdentity, true},
		// The bucket and the secret cells the same review added, which are the
		// three reaches a catalog build's own steps still make as the identity:
		// the copied definitions its pin step reads, the record its record step
		// writes, and the provider credential its clone step resolves. The
		// conditions these carry are the check's own input and
		// TestBuildIdentityBucketGrantsAreTheConditions reads them at the size
		// the plan writes them.
		{bkt, "roles/storage.objectViewer", buildIdentity, true},
		{bkt, "roles/storage.objectUser", buildIdentity, true},
		{secret, "roles/secretmanager.secretAccessor", buildIdentity, true},
		// The one project-level role the identity holds, and the one cell of
		// that row it shares with an account of Fugaro's own beside the
		// credentials service's datastore role: a build that names a
		// user-specified service account is written to Cloud Logging as that
		// account, so a catalog build without it is refused or loses every step
		// log (catalog.go submits with Logging: CLOUD_LOGGING_ONLY). The
		// per-repository build account this identity replaces held the same
		// role at the same level.
		{project, "roles/logging.logWriter", buildIdentity, true},
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
		// The build identity on the types and roles it does not hold: a
		// project-level secretAccessor, which would reach every secret of the
		// installation rather than the one repository's provider credential its
		// own cell grants, and the invoker bit on the type that is not the
		// service it invokes. (The runs bucket's two object roles came off this
		// list with the review that found a catalog build's own pin, record and
		// clone steps unreached; their conditions are what narrow them, and
		// TestBuildIdentityBucketGrantsAreTheConditions reads them.)
		{runjob, "roles/run.invoker", buildIdentity, false},
		{project, "roles/secretmanager.secretAccessor", buildIdentity, false},
		{project, "roles/secretmanager.secretAccessor", account, false},
		{project, "roles/iam.serviceAccountUser", person, false},
		{project, "projects/proj-1/roles/fugaroTokenMinter", person, false},
		{project, "projects/proj-1/roles/fugaroLauncher", account, false},
		{project, "projects/proj-1/roles/" + ViewerRole, account, false},
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
	// The three rows it may hold, and nothing else. A row that gains
	// toCredentials is a role this account may be granted, so it is a decision
	// this test makes deliberate.
	allowed := map[string]map[string]bool{
		"google_secret_manager_secret_iam_member": {"roles/secretmanager.secretAccessor": true},
		// The issuer's row (LM13, D3121): iam.serviceAccounts.signJwt on the
		// runs' signer of this run's Firebase project — the design's one new
		// grant, and the whole of what the service's new identity is for
		// (F7). It is the role of the cell; the ACCOUNT it is on is
		// issuerMinterGrant's, and TestIssuerMinterGrantIsCovered reads it.
		"google_service_account_iam_member": {customKey + "fugaroTokenMinter": true},
		"google_project_iam_member": {
			customKey + CredentialsDatabaseRole: true,
			// The dynamic-delivery pair (D1931, DS2.2): the prefix accessor,
			// which is the class's READ — a project-wide one, under the
			// name-prefix fence — and the lister it shares with the intake.
			// Neither is a wider reach than the bundle's accessor: the fence
			// is what makes it exactly the grammar's containers.
			"roles/secretmanager.secretAccessor": true,
			customKey + SecretListerRole:         true,
			// The service's Vertex role (CS11, D3174): the one call a chain
			// whose models are served on Vertex makes as this account, and it
			// is the account's because a job must not hold it — the release-1
			// roles/aiplatform.user the jobs keep under observe is retired at
			// X8. The role's own permission list is what keeps the reach to
			// the predict and nothing of the platform (RolePermissions).
			customKey + CredentialsVertexRole: true,
		},
	}
	for typ, row := range grantRules {
		for key, w := range row {
			if (w&toCredentials != 0) != allowed[typ][key] {
				t.Errorf("%s %s: toCredentials is %v, want %v — the service's account holds the bundle's accessor, its two custom roles, the dynamic class's reader half and the issuer's own minter row, and no other role of the installation", typ, key, w&toCredentials != 0, allowed[typ][key])
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
			if acct := grantAccountFor(typ, role); acct != "" {
				after["service_account_id"] = acct
			}
			withCondFor(after, typ, role, false)
			if acct := grantAccountFor(typ, role); acct != "" {
				after["service_account_id"] = acct
			}
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

// TestIssuerMinterGrantIsCovered is LM13's row read at the size the plan writes
// it (launcher-does-not-mint plan task LM13; D3121, F7): the credentials
// service's account, the root's custom minter role, and iam.serviceAccounts.
// signJwt on the RUNS' signer account of this run's Firebase project — the one
// row that moves a run's identity off every launcher and onto the service.
//
// Why the row needs a test of its own at all. The cell carries the role by
// KIND, and a kind cannot see which account a grant is on: signJwt on any
// account IS that account (a self-signed JWT is an access token for its
// issuer), so the cell alone would admit the credentials service as the
// devices' signer, as a job account holding the model credentials, or as an
// account of another project. issuerMinterGrant is the rule that closes it
// again, and this is where that rule is read: the covered case is the module's
// own two literals — the member the installation root's output carries and the
// signer the Firebase root creates — and every other account is refused with a
// sentence naming this run's signer.
//
// Mutation (run, restore): drop the toCredentials bit and the first case reads
// as a member of no kind the tables carry; drop the isCredentialsMember case
// from grant() and every refused case below is covered, the devices' signer and
// the job account among them.
func TestIssuerMinterGrantIsCovered(t *testing.T) {
	const member = "serviceAccount:" + CredentialsAccountID + "@proj-1.iam.gserviceaccount.com"
	const runsSigner = "fugaro-token-signer@fp-2.iam.gserviceaccount.com"
	const minterRole = "projects/fp-2/roles/fugaroTokenMinter"

	for _, tc := range []struct {
		what    string
		role    string
		member  string
		acct    string
		unknown map[string]any
		covered bool
		named   string // for a refusal: the sentence the stop must carry
	}{
		// The row the design adds, exactly as modules/firebase/iam.tf writes it:
		// the role of this run's Firebase project and the signer account of that
		// same project, with the member the installation root published.
		{"the minter role on the runs' signer, as iam.tf writes it", minterRole, member,
			"projects/fp-2/serviceAccounts/" + runsSigner, map[string]any{}, true, ""},
		// The account is the whole of the row's content, so a plan that moved it
		// is refused: each of these is a grant the role's cell would have
		// admitted and that nobody priced.
		{"the minter role on the devices' signer", minterRole, member,
			"projects/fp-2/serviceAccounts/" + DeviceSignerAccountID + "@fp-2.iam.gserviceaccount.com", map[string]any{}, false, "makes the credentials service that account"},
		{"the minter role on a job account", minterRole, member,
			"projects/proj-1/serviceAccounts/fugaro-history@proj-1.iam.gserviceaccount.com", map[string]any{}, false, "the job accounts holding the model credentials"},
		{"the minter role on another project's signer", minterRole, member,
			"projects/fp-9/serviceAccounts/fugaro-token-signer@fp-9.iam.gserviceaccount.com", map[string]any{}, false, "the budget signer account of this run"},
		{"the minter role on an account the plan leaves unknown", minterRole, member,
			"", map[string]any{"service_account_id": true}, false, "whose account is not known"},
		// ... and the other half: the role. Another role on the right account is
		// not this row, and the table refuses it before the account is read (the
		// launcher's role is the case, since it is the one the member could hold
		// on the project).
		{"a launcher's role on the signer account", "projects/proj-1/roles/fugaroLauncher", member,
			"projects/fp-2/serviceAccounts/" + runsSigner, map[string]any{}, false, ""},
		// The role is the Firebase project's, and a lookalike in another project
		// is not it: roleKey resolves only this run's projects.
		{"the minter role spelled in a project that is not this run's", "projects/fp-9/roles/fugaroTokenMinter", member,
			"projects/fp-2/serviceAccounts/" + runsSigner, map[string]any{}, false, ""},
		// The member is not a lookalike either: the row is the credentials
		// service's ACCOUNT, and another account holding this role — the history
		// account, say — reads as an account nobody listed and not as this kind.
		{"the minter role on the signer, held by a job account", minterRole,
			"serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com",
			"projects/fp-2/serviceAccounts/" + runsSigner, map[string]any{}, false, ""},
		{"the minter role on the signer, held by an account in another project", minterRole,
			"serviceAccount:" + CredentialsAccountID + "@fp-2.iam.gserviceaccount.com",
			"projects/fp-2/serviceAccounts/" + runsSigner, map[string]any{}, false, ""},
	} {
		after := map[string]any{"member": tc.member, "role": tc.role}
		if tc.acct != "" {
			after["service_account_id"] = tc.acct
		}
		ch := rc("google_service_account_iam_member", after, tc.unknown, "create")
		got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
		if (len(got) == 0) != tc.covered {
			t.Errorf("%s: NotCovered = %q, covered want %v", tc.what, got, tc.covered)
		}
		if !tc.covered && tc.named != "" && !strings.Contains(strings.Join(got, "\n"), tc.named) {
			t.Errorf("%s: stopped for another reason: %q, want the sentence %q", tc.what, got, tc.named)
		}
	}
}

// The coordinator's writer account (coordinator plan P3) is its own kind of
// principal for the credentials account's reason (ledger #903): as one of
// FugaroAccounts it would pass every toAccounts row, so one confirmation would
// cover granting the account that writes under the rules' floor a job runner's
// or the history account's roles. It holds the FP's database admin, the two
// reads its own job makes (P5) — the runs bucket and the GitHub App's key —
// and, since BB5, the credentials service's read mint, the one Cloud Run
// service it may invoke and the way its Bitbucket reads are minted at all.
func TestCoordinatorAccountIsItsOwnPrincipalKind(t *testing.T) {
	const coordinator = "serviceAccount:" + CoordinatorAccountID + "@proj-1.iam.gserviceaccount.com"

	if slices.Contains(FugaroAccounts, CoordinatorAccountID) {
		t.Errorf("the coordinator's writer is one of FugaroAccounts, so every toAccounts row accepts it")
	}
	allowed := map[string]map[string]bool{
		"google_project_iam_member":               {"roles/firebasedatabase.admin": true},
		"google_storage_bucket_iam_member":        {"roles/storage.objectViewer": true},
		"google_secret_manager_secret_iam_member": {"roles/secretmanager.secretAccessor": true},
		"google_cloud_run_v2_service_iam_member":  {"roles/run.invoker": true},
	}
	for typ, row := range grantRules {
		for key, w := range row {
			if (w&toCoordinator != 0) != allowed[typ][key] {
				t.Errorf("%s %s: toCoordinator is %v, want %v — the writer holds the FP's database admin and the two reads its own job makes, and no other role", typ, key, w&toCoordinator != 0, allowed[typ][key])
			}
		}
	}

	roles := map[string][]string{}
	for typ, row := range grantRules {
		for key := range row {
			roles[typ] = append(roles[typ], grantRoleString(key))
		}
	}
	roles["google_project_iam_member"] = append(roles["google_project_iam_member"], "roles/owner", "roles/editor", "projects/proj-1/roles/fugaroEverything")
	for typ, list := range roles {
		for _, role := range slices.Sorted(slices.Values(list)) {
			after := map[string]any{"member": coordinator, "role": role, "bucket": "fugaro-runs-proj-1"}
			withCondFor(after, typ, role, false)
			ch := rc(typ, after, map[string]any{}, "create")
			got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
			want := allowed[typ][mustRoleKey(t, role)]
			if (len(got) == 0) != want {
				t.Errorf("%s: %s to the coordinator's writer: NotCovered = %q, covered want %v", typ, role, got, want)
			}
		}
	}

	// The account is the installation's: a lookalike in another project is
	// nobody Fugaro creates.
	for _, member := range []string{
		"serviceAccount:" + CoordinatorAccountID + "@fp-2.iam.gserviceaccount.com",
	} {
		ch := rc("google_project_iam_member", map[string]any{"member": member, "role": "roles/firebasedatabase.admin", "bucket": "fugaro-runs-proj-1"}, map[string]any{}, "create")
		if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
			t.Errorf("%s: the database admin is covered: the writer is %s@proj-1 and nothing else", member, CoordinatorAccountID)
		}
	}

	// Listed on the review screen by mistake, it is still not a person.
	listed := Cover{Projects: testCover.Projects, Listed: []string{coordinator}, Region: testCover.Region, Buckets: testCover.Buckets}
	for _, tc := range []struct {
		role    string
		covered bool
	}{
		{"roles/firebasedatabase.admin", true},
		{"roles/firebasedatabase.viewer", false},
		{"roles/datastore.user", false},
	} {
		ch := rc("google_project_iam_member", map[string]any{"member": coordinator, "role": tc.role, "bucket": "fugaro-runs-proj-1"}, map[string]any{}, "create")
		if got := listed.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); (len(got) == 0) != tc.covered {
			t.Errorf("listed on the review screen: %s to the coordinator's writer: NotCovered = %q, covered want %v", tc.role, got, tc.covered)
		}
	}
}

// The project's build identity (B5, D2301) is its own kind of principal for
// the credentials account's reason: as one of FugaroAccounts every toAccounts
// row would have accepted it, so one confirmation would have covered handing
// the identity a shared repository's catalog build runs as a job runner's
// role, the history account's datastore.user or a build account's registry
// writer. What a catalog build does need of the registry is a role another
// task adds to this table deliberately, and B6 is that task: the identity's
// whole reach is the cells `allowed` names — roles/run.invoker on the
// credentials service, the class the service answers the build route for, and
// the writer and tag-mover pair on the ONE shared registry every catalog image
// is pushed into.
//
// The table alone does not hold that: TestGrantRulesMatrix computes its
// expectation from the table under test, so it follows a row that gains the
// kind. This is the hand-written half — the cells the account may hold,
// asserted over every (type, role) cell of grantRules and then for real
// through the cover, role by role — and TestGrantRulesPinnedCells pins the
// positive cell and two refusals beside it.
func TestBuildIdentityIsItsOwnPrincipalKind(t *testing.T) {
	const build = "serviceAccount:" + BuildIdentityAccountID + "@proj-1.iam.gserviceaccount.com"

	if slices.Contains(FugaroAccounts, BuildIdentityAccountID) {
		t.Errorf("the project's build identity is one of FugaroAccounts, so every toAccounts row accepts it: one confirmation would then cover granting the identity a catalog build runs as a job runner's role or a build account's registry writer")
	}
	// The cells it may hold, and nothing else: a row that gains
	// toBuildIdentity without being named here is a role this account may be
	// granted, which is a decision the account's own tasks make and not one a
	// widened row makes silently. B6 is the second of those tasks, and it
	// added the registry roles a catalog build needs as the account it runs
	// as: the writer and the tag mover on the ONE shared registry, where every
	// catalog image lands, and the reader on the two registries a build pulls
	// from (fugaro-base and the ghcr remote) — the three roles the
	// per-repository build account held (modules/repo/build.tf's build_base
	// and build_ghcr, and modules/repo/registry.tf's pair).
	allowed := map[string]map[string]bool{
		"google_cloud_run_v2_service_iam_member": {"roles/run.invoker": true},
		"google_artifact_registry_repository_iam_member": {
			"roles/artifactregistry.reader": true,
			"roles/artifactregistry.writer": true,
			customKey + "fugaroTagMover":    true,
		},
		// The one project-level role, and the only place this account reaches
		// outside a resource of its own: Cloud Logging writes a build's step
		// logs as the account the build runs as, so a catalog build — which
		// submits with Logging: CLOUD_LOGGING_ONLY — needs logging.logWriter,
		// and the role is project-wide because Cloud Logging offers the write
		// no narrower. It is what the per-repository build account held too
		// (modules/repo/build.tf's `build` row), moved here with the account.
		"google_project_iam_member": {"roles/logging.logWriter": true},
		// The runs bucket and the provider credentials a catalog build's own
		// steps reach, which the same review found missing after the build
		// moved onto this identity: the definitions its pin step reads
		// (objectViewer), the record its record step writes (objectUser), and
		// the credential its clone step resolves through AvailableSecrets
		// (secretAccessor). The conditions are the catalog check's own input
		// and catalogReadGrant and catalogRecordsGrant are what pin them, one
		// repository's records area at a time (see
		// TestBuildIdentityBucketGrantsAreTheConditions).
		"google_storage_bucket_iam_member": {
			"roles/storage.objectUser":   true,
			"roles/storage.objectViewer": true,
		},
		"google_secret_manager_secret_iam_member": {"roles/secretmanager.secretAccessor": true},
		// The OpenID token of the account itself (buildIdentityOpenIDGrant holds it to
		// that account): Cloud Build serves a build its ID token only to an account
		// that holds iam.serviceAccounts.getOpenIdToken.
		"google_service_account_iam_member": {"roles/iam.serviceAccountOpenIdTokenCreator": true},
	}
	for typ, row := range grantRules {
		for key, w := range row {
			if (w&toBuildIdentity != 0) != allowed[typ][key] {
				t.Errorf("%s %s: toBuildIdentity is %v, want %v — the build identity invokes the credentials service and holds no other role of the installation", typ, key, w&toBuildIdentity != 0, allowed[typ][key])
			}
		}
	}
	for typ, row := range allowed {
		for key := range row {
			if grantRules[typ][key]&toBuildIdentity == 0 {
				t.Errorf("%s %s does not accept the build identity, so the module's own grant of it is refused and init cannot cover the plan that creates the account", typ, key)
			}
		}
	}

	// ... and the cover, role by role: every role of every row, the roles the
	// table names for other kinds of principal included, plus roles no row
	// names at all.
	roles := map[string][]string{}
	for typ, row := range grantRules {
		for key := range row {
			roles[typ] = append(roles[typ], grantRoleString(key))
		}
	}
	roles["google_project_iam_member"] = append(roles["google_project_iam_member"],
		"roles/owner", "roles/editor", "roles/logging.logWriter", "roles/storage.objectAdmin",
		"projects/proj-1/roles/fugaroEverything")
	for typ, list := range roles {
		for _, role := range slices.Sorted(slices.Values(list)) {
			after := map[string]any{"member": build, "role": role, "bucket": "fugaro-runs-proj-1"}
			if acct := grantAccountFor(typ, role); acct != "" {
				after["service_account_id"] = acct
			}
			withCondFor(after, typ, role, false)
			ch := rc(typ, after, map[string]any{}, "create")
			got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
			want := allowed[typ][mustRoleKey(t, role)]
			if (len(got) == 0) != want {
				t.Errorf("%s: %s to the build identity: NotCovered = %q, covered want %v", typ, role, got, want)
			}
		}
	}

	// The account is the installation's: the module creates it in the plan's
	// own project, so the same account ID in the Firebase project is somebody
	// else's and no row accepts it. (A suffixed address in this project is an
	// ordinary job account of the run — the toAccounts reading — which is why
	// the ID is exact in the row above rather than a prefix.)
	ch := rc("google_cloud_run_v2_service_iam_member", map[string]any{"member": "serviceAccount:" + BuildIdentityAccountID + "@fp-2.iam.gserviceaccount.com", "role": "roles/run.invoker"}, map[string]any{}, "create")
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
		t.Errorf("the build identity of another project: roles/run.invoker is covered, and the module creates %s@proj-1 alone", BuildIdentityAccountID)
	}
}

// The secrets intake's own account (D1931, DS2.2) is its own kind of principal
// for the credentials account's reason: as one of FugaroAccounts every
// toAccounts row would have accepted it, and this is the ONE account of an
// installation that may create, add a version to and delete its dynamic secret
// containers — a write reach no job account has, which no `fugaro update`
// confirmation announced on its behalf. Its rows are the class's three, each of
// which the cover narrows further (dynamicSecretsGrant reads the fence,
// intakeAdminListGrant the one object): anything else — a secret's VALUE above
// all, which is roles/secretmanager.secretAccessor on a container, and the
// bundle's own accessor with it — is refused and the update that carries one
// asks its owner. This is DS2.2's red step as a test: the summary's
// TestIntakeAccountHasNoAccessor reads the module's source for the same fact.
func TestIntakeAccountIsItsOwnPrincipalKind(t *testing.T) {
	const intake = "serviceAccount:" + IntakeAccountID + "@proj-1.iam.gserviceaccount.com"

	if slices.Contains(FugaroAccounts, IntakeAccountID) {
		t.Errorf("the intake is one of FugaroAccounts, so every toAccounts row accepts it: one confirmation would then cover granting the account that writes the project's dynamic secret containers a build account's registry role or a job's vertex role")
	}
	// The three rows it may hold, and nothing else.
	allowed := map[string]map[string]bool{
		"google_project_iam_member": {
			customKey + IntakeSecretsRole: true,
			customKey + SecretListerRole:  true,
		},
		"google_storage_bucket_iam_member": {"roles/storage.objectViewer": true},
	}
	for typ, row := range grantRules {
		for key, w := range row {
			if (w&toIntake != 0) != allowed[typ][key] {
				t.Errorf("%s %s: toIntake is %v, want %v — the intake holds the class's write role, the shared lister and one object read, and no other role of the installation", typ, key, w&toIntake != 0, allowed[typ][key])
			}
		}
	}
	for typ, row := range allowed {
		for key := range row {
			if grantRules[typ][key]&toIntake == 0 {
				t.Errorf("%s %s does not accept the intake's account, so the module's own grant of it is refused and no update can cover the plan that turns the intake on", typ, key)
			}
		}
	}

	// ... and the cover, role by role: the class's three grants are covered
	// (each under the shape its own check demands), and every other role of
	// every row — plus roles no row names at all — is refused. The value
	// reader is the one that matters most and it is in the list below.
	roles := map[string][]string{}
	for typ, row := range grantRules {
		for key := range row {
			roles[typ] = append(roles[typ], grantRoleString(key))
		}
	}
	roles["google_project_iam_member"] = append(roles["google_project_iam_member"],
		"roles/owner", "roles/editor", "projects/proj-1/roles/fugaroEverything", "roles/datastore.user")
	for typ, list := range roles {
		for _, role := range slices.Sorted(slices.Values(list)) {
			after := map[string]any{"member": intake, "role": role, "bucket": "fugaro-runs-proj-1"}
			withCondFor(after, typ, role, false)
			ch := rc(typ, after, map[string]any{}, "create")
			got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
			want := allowed[typ][mustRoleKey(t, role)]
			if (len(got) == 0) != want {
				t.Errorf("%s: %s to the intake's account: NotCovered = %q, covered want %v", typ, role, got, want)
			}
		}
	}

	// The one READ of a value the installation has: the bundle's accessor. A
	// container-level accessor to the intake would be a fence it does not need
	// and an exit from the grammar (the intake derives a name from the verified
	// sub and writes it; it never reads one back, R3).
	for _, tc := range []struct {
		typ, role string
	}{
		{"google_secret_manager_secret_iam_member", "roles/secretmanager.secretAccessor"},
		{"google_secret_manager_secret_iam_member", "roles/secretmanager.secretVersionAdder"},
		{"google_project_iam_member", "roles/secretmanager.secretAccessor"},
		{"google_project_iam_member", "roles/secretmanager.viewer"},
		{"google_project_iam_member", "roles/secretmanager.admin"},
	} {
		after := map[string]any{"member": intake, "role": tc.role, "bucket": "fugaro-runs-proj-1", "secret_id": "fugaro_p_x"}
		withCondFor(after, tc.typ, tc.role, false)
		ch := rc(tc.typ, after, map[string]any{}, "create")
		if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
			t.Errorf("%s: %s to the intake is covered: the intake writes the class's containers and reads no value of any of them", tc.typ, tc.role)
		}
	}

	// The account is the installation's: a lookalike in another project is
	// nobody Fugaro creates.
	ch := rc("google_project_iam_member", map[string]any{"member": "serviceAccount:" + IntakeAccountID + "@fp-2.iam.gserviceaccount.com", "role": "projects/proj-1/roles/" + IntakeSecretsRole, "project": "proj-1", "bucket": "fugaro-runs-proj-1", "condition": dynamicSecretsCondition()}, map[string]any{}, "create")
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
		t.Errorf("the write role is covered: the intake is %s@proj-1 and nothing else", IntakeAccountID)
	}

	// Listed on the review screen by mistake, it is still not a person.
	listed := Cover{Projects: testCover.Projects, Listed: []string{intake}, Region: testCover.Region, Buckets: testCover.Buckets}
	for _, tc := range []struct {
		role    string
		covered bool
	}{
		{"projects/proj-1/roles/" + IntakeSecretsRole, false}, // no condition: a row, not the binding
		{"projects/proj-1/roles/" + SecretListerRole, false},  // the same: a person holds no lister of the installation
		{"roles/storage.objectViewer", false},
	} {
		after := map[string]any{"member": intake, "role": tc.role, "bucket": "fugaro-runs-proj-1"}
		ch := rc("google_project_iam_member", after, map[string]any{}, "create")
		if got := listed.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); (len(got) == 0) != tc.covered {
			t.Errorf("listed on the review screen: %s to the intake: NotCovered = %q, covered want %v", tc.role, got, tc.covered)
		}
	}
}

// The intake's one bucket read is objectViewer on ONE object of the runs
// bucket — DS2.0's derived admin list — and intakeAdminListGrant is the whole
// of what narrows it: grantRules' bucket row says only that the intake may hold
// objectViewer there at all, and grant() sends an intake member to this check
// INSTEAD of bucketCondition, so a grant that fails it does not fall back to
// the modules' own shapes. Without the steps below, an unconditioned
// objectViewer on the runs bucket — every run record, prompt and transcript the
// installation keeps — would ride the same one confirmation the admin list's
// read does, and a condition the plan has not decided would read as the
// modules' own.
//
// The rows below say which guard refuses which. Six of them are the narrowing
// check's own — the missing condition, another object, the whole bucket, the
// right expression under another title, a description, and a condition the plan
// has not decided — and the other three are refused before it, each by a guard
// that has to hold anyway: the bucket has to be one of this run's, and the role
// has to be the one grantRules' row names for the intake.
//
// Mutation (verified, run and restored): intakeAdminListGrant returning "" at
// its first line leaves those six covered — an unconditioned objectViewer on
// the whole runs bucket among them — and the test fails on exactly those rows.
func TestIntakeAdminListGrantIsTheDerivedObjectAlone(t *testing.T) {
	const intake = "serviceAccount:" + IntakeAccountID + "@proj-1.iam.gserviceaccount.com"
	const bucket = "fugaro-runs-proj-1"
	cond := func(title, expression string, description any) []any {
		c := map[string]any{"title": title, "expression": expression, "description": description}
		return []any{c}
	}
	for _, tc := range []struct {
		name, role, bucket string
		condition          []any
		covered            bool
		reason             string // for a refusal: the text the stop must carry
	}{
		{
			name: "the derived admin list's one object", role: "roles/storage.objectViewer", bucket: bucket,
			condition: cond(gcp.IntakeAdminListConditionTitle, IntakeAdminListCondition(bucket), nil), covered: true,
		},
		{
			name: "no condition", role: "roles/storage.objectViewer", bucket: bucket,
			reason: "no condition",
		},
		{
			name: "a condition on another object of the same bucket", role: "roles/storage.objectViewer", bucket: bucket,
			condition: cond(gcp.IntakeAdminListConditionTitle, `resource.name == "projects/_/buckets/`+bucket+`/objects/admin/other.json"`, nil),
			reason:    "not the derived admin list's one object",
		},
		{
			name: "a condition on the whole bucket", role: "roles/storage.objectViewer", bucket: bucket,
			condition: cond(gcp.IntakeAdminListConditionTitle, `resource.name.startsWith("projects/_/buckets/`+bucket+`/objects/")`, nil),
			reason:    "not the derived admin list's one object",
		},
		{
			name: "the right expression under another title", role: "roles/storage.objectViewer", bucket: bucket,
			condition: cond("fugaro-intake-admin-list-2", IntakeAdminListCondition(bucket), nil),
			reason:    "not the derived admin list's one object",
		},
		{
			name: "a condition with a description", role: "roles/storage.objectViewer", bucket: bucket,
			condition: cond(gcp.IntakeAdminListConditionTitle, IntakeAdminListCondition(bucket), "the admin list"),
			reason:    "description",
		},
		{
			name: "a bucket that is not this installation's runs bucket", role: "roles/storage.objectViewer", bucket: "fugaro-runs-fp-2",
			condition: cond(gcp.IntakeAdminListConditionTitle, IntakeAdminListCondition("fugaro-runs-fp-2"), nil),
			reason:    "which is not this run's", // an earlier guard, before the intake's own narrowing

		},
		{
			name: "another role on the same object", role: "roles/storage.objectUser", bucket: bucket,
			condition: cond(gcp.IntakeAdminListConditionTitle, IntakeAdminListCondition(bucket), nil),
			reason:    "a kind of principal the installation's modules do not grant it to", // grantRules' bucket row, before the narrowing

		},
		{
			name: "objectAdmin on the same object", role: "roles/storage.objectAdmin", bucket: bucket,
			condition: cond(gcp.IntakeAdminListConditionTitle, IntakeAdminListCondition(bucket), nil),
			reason:    "a kind of principal the installation's modules do not grant it to", // grantRules' bucket row, before the narrowing

		},
		{
			name: "a condition known only after apply", role: "roles/storage.objectViewer", bucket: bucket,
			reason: "is not known", // the block is there, its expression is not

		},
	} {
		after := map[string]any{"member": intake, "role": tc.role, "bucket": tc.bucket}
		if tc.condition != nil {
			after["condition"] = tc.condition
		}
		unk := map[string]any{}
		if tc.name == "a condition known only after apply" {
			after["condition"] = []any{map[string]any{"title": gcp.IntakeAdminListConditionTitle}}
			unk["condition"] = []any{map[string]any{"expression": true}}
		}
		ch := rc("google_storage_bucket_iam_member", after, unk, "create")
		got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
		if (len(got) == 0) != tc.covered {
			t.Errorf("%s: %s to the intake: NotCovered = %q, covered want %v", tc.name, tc.role, got, tc.covered)
			continue
		}
		if !tc.covered && !strings.Contains(strings.Join(got, "\n"), tc.reason) {
			t.Errorf("%s: %s to the intake stopped for another reason: %q, want %q", tc.name, tc.role, got, tc.reason)
		}
	}
}

// A computed role to either of the dynamic class's two accounts is refused,
// whatever type carries it: the intake's write role is this module's own custom
// role, so a plan that bound it as a reference to the role being created would
// otherwise be covered WITH the name-prefix condition unread --
// dynamicSecretsGrant reads the condition off a role it can name, and abstains
// when it cannot -- and that is create, delete and version-add for every
// container of the project riding the ordinary confirmation. The refusal names
// the account, so an operator's read of it says which grant to write by name.
//
// Mutation (verified, run and restored): with the two branches in the
// unknown-role case removed, the PROJECT rows cover a computed role to either
// account -- the hole this test is about, since that is the one type whose row
// names a custom role for these accounts -- and the other three rows stop for
// the table's own reason instead of the account's, so all six rows fail.
func TestDynamicAccountsAreRefusedAComputedRole(t *testing.T) {
	role := customRole(IntakeSecretsRole, "proj-1", []any{"secretmanager.secrets.create", "secretmanager.secrets.delete", "secretmanager.versions.add"})
	for _, tc := range []struct{ name, member string }{
		{"the intake", "serviceAccount:" + IntakeAccountID + "@proj-1.iam.gserviceaccount.com"},
		{"the credentials service", "serviceAccount:" + CredentialsAccountID + "@proj-1.iam.gserviceaccount.com"},
	} {
		for _, typ := range []string{"google_project_iam_member", "google_secret_manager_secret_iam_member", "google_storage_bucket_iam_member", "google_cloud_run_v2_service_iam_member"} {
			after := map[string]any{"member": tc.member, "bucket": "fugaro-runs-proj-1", "secret_id": "fugaro_p_x"}
			ch := rc(typ, after, map[string]any{"role": true}, "create")
			got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{role, ch}}, nil)
			if len(got) == 0 {
				t.Errorf("%s: a computed role to %s is covered: a role the plan cannot name cannot be checked against the name-prefix condition it is bound under (DS2.2)", typ, tc.name)
				continue
			}
			if want := "a computed role to " + tc.name; !strings.Contains(strings.Join(got, "\n"), want) {
				t.Errorf("%s: a computed role to %s stopped for another reason: %q, want %q", typ, tc.name, got, want)
			}
		}
	}
}

// dynamicSecretsGrant is the whole fence of the dynamic class, and it is the
// only one: grantRules' project row says which KIND of principal may hold the
// intake's write role and the service's prefix accessor, and says nothing about
// a condition, so without these rows a plan could grant the service's account a
// project-wide secretmanager.secretAccessor with no condition — every secret of
// the project, the GitHub App key among them — or bind either role under a
// wider condition, and one fugaro update confirmation would carry it.
//
// The rows hold both halves of what the check compares: the condition's
// presence (none is a refusal, and the list role carries none because Check 510
// refused secretmanager.secrets.list under the fence) and its exact spelling
// (the title and the expression byte for byte, so a wider spelling, a single
// prefix, another title, a description and a condition the plan has not decided
// are each their own row). The three shapes the modules write are rows too, so
// a check that refused everything cannot pass this test.
//
// Mutation (verified, run and restored): short-circuiting the accessor before
// its condition checks — its member and project steps left in place — leaves
// that holder's six negative rows covered, none of them reported; and dropping
// the title/expression comparison leaves the wider, single-prefix and
// other-title rows of BOTH holders covered. The description and the unknown
// condition are separate steps after the comparison, so those rows still hold
// under the second mutation.
func TestDynamicSecretsGrantIsTheNameFence(t *testing.T) {
	const intake = "serviceAccount:" + IntakeAccountID + "@proj-1.iam.gserviceaccount.com"
	const service = "serviceAccount:" + CredentialsAccountID + "@proj-1.iam.gserviceaccount.com"
	const accessor = "roles/secretmanager.secretAccessor"
	write, lister := dynamicSecretsRole(IntakeSecretsRole), dynamicSecretsRole(SecretListerRole)
	cond := func(title, expression string, description any) []any {
		return []any{map[string]any{"title": title, "expression": expression, "description": description}}
	}
	fence := dynamicSecretsCondition()
	// What every wrong spelling of the fence is refused for, whichever part of
	// it is wrong: the comparison is byte for byte and names the fence it
	// wanted.
	const notTheFence = "is the fence, byte for byte"

	for _, holder := range []struct{ name, member, role string }{
		{"the intake's write role", intake, write},
		{"the service's prefix accessor", service, accessor},
	} {
		for _, tc := range []struct {
			name             string
			condition        []any
			unknownCondition bool
			covered          bool
			reason           string
		}{
			{name: "under the fence", condition: fence, covered: true},
			{name: "with no condition", reason: "no condition"},
			{name: "under a wider expression", condition: cond(gcp.DynamicSecretNamesConditionTitle, `resource.name.startsWith("projects/")`, nil), reason: notTheFence},
			{name: "under one prefix only", condition: cond(gcp.DynamicSecretNamesConditionTitle, `resource.name.extract("/secrets/{secret}").startsWith("fugaro_u_")`, nil), reason: notTheFence},
			{name: "under another title", condition: cond("fugaro-dynamic-secret-names", gcp.DynamicSecretNamesCondition(), nil), reason: notTheFence},
			{name: "under the fence's two prefixes and a description", condition: cond(gcp.DynamicSecretNamesConditionTitle, gcp.DynamicSecretNamesCondition(), "the delivery grammar"), reason: "description"},
			{name: "under a condition known only after apply", unknownCondition: true, reason: "is not known"},
		} {
			after := map[string]any{"member": holder.member, "role": holder.role, "project": "proj-1"}
			unk := map[string]any{}
			if tc.condition != nil {
				after["condition"] = tc.condition
			}
			if tc.unknownCondition {
				after["condition"] = []any{map[string]any{"title": gcp.DynamicSecretNamesConditionTitle}}
				unk["condition"] = []any{map[string]any{"expression": true}}
			}
			ch := rc("google_project_iam_member", after, unk, "create")
			got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
			if (len(got) == 0) != tc.covered {
				t.Errorf("%s %s to %s: NotCovered = %q, covered want %v", holder.name, tc.name, holder.member, got, tc.covered)
				continue
			}
			if !tc.covered && !strings.Contains(strings.Join(got, "\n"), tc.reason) {
				t.Errorf("%s %s to %s stopped for another reason: %q, want %q", holder.name, tc.name, holder.member, got, tc.reason)
			}
		}
	}

	// The lister is the other half: it carries NO condition — Check 510
	// refused secretmanager.secrets.list under the fence, which is what makes
	// the permission a role of its own — and it is the one row of the three
	// that a condition makes a refusal rather than a cover.
	for _, member := range []string{intake, service} {
		for _, tc := range []struct {
			name      string
			condition []any
			covered   bool
		}{
			{"with no condition", nil, true},
			{"under the fence", fence, false},
		} {
			after := map[string]any{"member": member, "role": lister, "project": "proj-1"}
			if tc.condition != nil {
				after["condition"] = tc.condition
			}
			ch := rc("google_project_iam_member", after, map[string]any{}, "create")
			got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
			if (len(got) == 0) != tc.covered {
				t.Errorf("the lister %s to %s: NotCovered = %q, covered want %v", tc.name, member, got, tc.covered)
				continue
			}
			if !tc.covered && !strings.Contains(strings.Join(got, "\n"), "under a condition") {
				t.Errorf("the lister %s to %s stopped for another reason: %q, want %q", tc.name, member, got, "under a condition")
			}
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

// catalogCheckMember is the member a plan names for the project's catalog check
// job account (D1148 task (ii)).
const catalogCheckMember = "serviceAccount:" + CatalogCheckAccountID + "@proj-1.iam.gserviceaccount.com"

// catalogCheckCatalogRead is the plan the catalog check job's read of the
// copied definitions comes from (modules/installation/catalogcheck.tf): the
// account created in the same module, and an objectViewer grant on the runs
// bucket under gcp.CatalogReadCondition — byte for byte the expression every
// build account's own grant carries (modules/repo/build.tf). computed writes
// the member the way a first plan does, from the account beside it; false
// writes it the way the account's own update plan does, named outright.
func catalogCheckCatalogRead(computed bool, title, expr string) *Plan {
	after := map[string]any{
		"bucket": cbBucket, "role": "roles/storage.objectViewer",
		"condition": []any{cond(title, expr)},
	}
	unk := map[string]any{"condition": []any{map[string]any{}}}
	if computed {
		unk["member"] = true
	} else {
		after["member"] = catalogCheckMember
	}
	return &Plan{ResourceChanges: []ResourceChange{
		{Address: "module.installation.google_service_account.catalog_check", Type: "google_service_account",
			Change: Change{Actions: []string{"create"}, After: map[string]any{"account_id": CatalogCheckAccountID}}},
		{Address: "module.installation.google_storage_bucket_iam_member.catalog_check_definitions", Type: "google_storage_bucket_iam_member",
			Change: Change{Actions: []string{"create"}, After: after, AfterUnknown: unk}},
	}}
}

// TestCatalogCheckAccountIsItsOwnPrincipalKind: the project's catalog check job
// (D1148) is a principal of its own kind, like the credentials service's
// account and the coordinator's writer, and for the same reason. Its grants
// — actAs on each repository's build account, the copied definitions read-only,
// one repository's build records and check state, the build submitter, its own
// log lines, and run.invoker on its own job — are a project-wide reach no
// confirmation of `fugaro setup` or `fugaro update` announced, so a plan carrying any of
// them is one the step asks its owner about. Nothing else may accept it either:
// an account that could ride one of the tables' rows would be one whose next
// grant (a secret accessor, an IAM admin role, a key) is covered silently.
//
// The refusal is one rule and not a table of expressions: checkGrant reads the
// member's kind before it reads the role, so the bucket paths below never get
// as far as the rule that accepts a build account's catalog read. They are
// pinned anyway, because that is the property a change to the tables could
// quietly take away — the same bytes, granted to another account.
//
// Mutation (run, restore): give any OTHER row the toCatalogCheck bit and the
// row loop below fails (the objectUser row is the one the modules write for
// this account, and TestCatalogCheckRecordsGrant narrows it to the condition
// that makes it theirs); drop the isCatalogCheckMember case from checkGrant and
// the message case fails (the account reads as an address nobody listed); give
// the catalog-read cases a member that is the build account's and they pass,
// which is the whole point: the expression is the build accounts'.
func TestCatalogCheckAccountIsItsOwnPrincipalKind(t *testing.T) {
	if slices.Contains(FugaroAccounts, CatalogCheckAccountID) {
		t.Errorf("the project's catalog check job is one of FugaroAccounts, so every toAccounts row accepts it")
	}
	// Exactly one row accepts this account, and it is the modules' own records
	// grant: the objectUser row, whose condition bucketCondition then requires
	// (TestCatalogCheckRecordsGrant), so the bit alone is not enough. The matrix
	// below is what keeps that one exception from growing — a row that gained
	// the bit for a role the modules do not grant this account fails here, and
	// so does a second cell.
	const recordsRow, recordsRole = "google_storage_bucket_iam_member", "roles/storage.objectUser"
	rows := 0
	for typ, row := range grantRules {
		for key, w := range row {
			if w&toCatalogCheck == 0 {
				continue
			}
			rows++
			if typ != recordsRow || key != recordsRole {
				t.Errorf("%s %s: the row accepts the catalog check job's account, whose grants are the five of D1148 and are refused here one by one", typ, key)
			}
		}
	}
	if rows != 1 {
		t.Errorf("%d rows carry toCatalogCheck, want the one the modules write for this account: %s %s", rows, recordsRow, recordsRole)
	}

	roles := map[string][]string{}
	for typ, row := range grantRules {
		for key := range row {
			roles[typ] = append(roles[typ], grantRoleString(key))
		}
	}
	// The roles a plan reaching for this account would actually name: every
	// project role of the tables, and the five that would matter most.
	roles["google_project_iam_member"] = append(roles["google_project_iam_member"],
		"roles/owner", "roles/editor", "roles/iam.serviceAccountAdmin",
		"roles/iam.serviceAccountTokenCreator", "roles/iam.serviceAccountKeyAdmin", "roles/secretmanager.secretAccessor")
	for typ, list := range roles {
		for _, role := range slices.Sorted(slices.Values(list)) {
			after := map[string]any{"member": catalogCheckMember, "role": role, "bucket": cbBucket}
			withCondFor(after, typ, role, false)
			ch := rc(typ, after, map[string]any{}, "create")
			if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
				t.Errorf("%s: %s to the catalog check job is covered: every grant to this account is an owner-run apply", typ, role)
			}
		}
	}

	// The refusal says which kind of principal this is, and not that the
	// address is missing from the review screen: the owner confirming the
	// apply has to read why the tables refuse an account the modules create.
	ch := rc("google_project_iam_member", map[string]any{"member": catalogCheckMember, "role": "roles/logging.logWriter", "bucket": cbBucket}, map[string]any{}, "create")
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) != 1 ||
		!strings.Contains(got[0], "a kind of principal the installation's modules do not grant it to") {
		t.Errorf("a project grant to the catalog check job: NotCovered = %q, want the refusal that names its own kind of principal", got)
	}

	// The bucket paths, which are the ones that bite: the catalog read's
	// expression is byte for byte a build account's, so without a rule for this
	// account the very rule that accepts a repository's build account
	// (catalogReadGrant) would cover a project-wide read of the copied
	// definitions — and a build account's records grant is the same shape. Both
	// spellings of the member are refused: the computed one a first plan
	// writes, and the one an update plan names outright.
	for _, computed := range []bool{false, true} {
		p := catalogCheckCatalogRead(computed, gcp.CatalogReadConditionTitle(CatalogCheckAccountID), gcp.CatalogReadCondition(cbBucket))
		if got := testCover.NotCovered(p, nil); len(got) == 0 {
			t.Errorf("an objectViewer grant on builds/catalog/ to the catalog check job (computed member %v) is covered: the rule that accepts a build account's catalog read must not accept the project job's", computed)
		}
	}

	// The account is the installation's: a lookalike in another project is
	// nobody Fugaro creates.
	for _, member := range []string{
		"serviceAccount:" + CatalogCheckAccountID + "@fp-2.iam.gserviceaccount.com",
	} {
		ch := rc("google_project_iam_member", map[string]any{"member": member, "role": "roles/logging.logWriter", "bucket": cbBucket}, map[string]any{}, "create")
		if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
			t.Errorf("%s: the log writer is covered: the catalog check job is %s@proj-1 and nothing else", member, CatalogCheckAccountID)
		}
	}

	// Listed on the review screen by mistake, it is still not a person: the
	// kind is read before the list, as the deployer's, the credentials
	// service's and the coordinator's are.
	listed := Cover{Projects: testCover.Projects, Listed: []string{catalogCheckMember}, Region: testCover.Region, Buckets: testCover.Buckets}
	for _, role := range []string{"roles/storage.objectViewer", "roles/run.invoker", "roles/logging.logWriter"} {
		ch := rc("google_project_iam_member", map[string]any{"member": catalogCheckMember, "role": role, "bucket": cbBucket}, map[string]any{}, "create")
		if got := listed.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
			t.Errorf("roles/%s to the catalog check job, listed on the review screen, is covered", role)
		}
	}
}

// TestCatalogCheckRecordsGrant: the one row that accepts the project's catalog
// check job (D1148) accepts ONE grant, and the CONDITION is what makes it the
// modules' own. The ID and the project name the account; the reach is the
// condition, which the module builds from its own input, one entry per
// repository (catalogcheck.tf's catalog_check_records:
// gcp.CatalogRecordsConditionTitle over gcp.CatalogRecordsCondition). That
// check is what keeps the row to the module's own condition: without it the row
// covers a records condition retitled for another repository, one naming
// another run's bucket, and one widened past the catalog segment. The
// per-repository job's prefix, the build account's read and the launchers'
// condition are a different check's subject — the objectUser branch refuses
// this account's name under any condition that is not the module's records
// grant, before it reads a condition at all — and each of those shapes is a
// case below too.
//
// Both spellings of the member are admitted, for their own reasons: the plan
// that first deploys the job computes it from the account the same module
// creates (nothing in that plan names the account, and a named member would be
// an account no resource in it makes), and the plan that adds a repository to
// an installation that already has the job names it, because the account is
// already applied.
//
// Everything else is refused, one case each: no condition, another condition of
// this codebase's own (a repository job's prefix, the build account's read of
// the copied definitions, the launchers'), the records condition retitled for
// another repository, on another bucket, widened past the catalog segment, with
// a description, another kind of principal (a repository's job account, a build
// account, a person on the review screen, a lookalike account in another
// project, a computed member from an account this one is not), and another role
// (objectAdmin under the records condition, objectAdmin with none — the shape
// the modules DO grant objectAdmin in, to people — and objectViewer under the
// records condition).
//
// Mutations (each run, then restored):
//
//   - Drop the expression comparison from catalogRecordsGrant, so any condition
//     under the title's slug passes: the retitled, on-another-bucket and
//     widened cases pass. The job-prefix, catalog-read and launcher cases stay
//     refused, because their titles fail the prefix cut before the comparison
//     is reached — what catches them is the objectUser branch's guard on this
//     account's name. Dropping catalogRecordsGrant whole (the member alone)
//     fails the same three cases and no others, and dropping that guard instead
//     lets the job-prefix, catalog-read and launcher cases pass while every
//     other case here still refuses.
//   - Give roles/storage.objectAdmin the toCatalogCheck bit: both objectAdmin
//     cases pass, on the role table's own check.
//   - Drop the slug check from catalogRecordsGrant, so any title after the
//     prefix counts as a slug: the two injected-title cases pass, because the
//     expression is built from that same title, and every other case here stays
//     refused.
//   - Give roles/storage.objectViewer the bit: the objectViewer case is refused
//     anyway, under the tokens/ sentence rather than this one, so what catches
//     that widening is the row-pin loop in
//     TestCatalogCheckAccountIsItsOwnPrincipalKind — which fails under the two
//     bit-granting mutations and not under the first.
func TestCatalogCheckRecordsGrant(t *testing.T) {
	const slug = "acme-web-0123456789abcdef"
	records := gcp.CatalogRecordsCondition(cbBucket, slug)
	// proj-1 first — the installation's own project, which isCatalogCheckMember
	// reads — with proj-1234 beside it, so the other kinds of principal below
	// are read as accounts of this run rather than as strangers.
	c := Cover{Projects: []string{"proj-1", "proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}, Region: testCover.Region}

	for name, tc := range map[string]struct {
		member  string // "" for the computed member a first plan writes
		sa      string // the account ID the grant's module creates beside it, "" for none
		role    string
		noCond  bool // the grant carries no condition block at all
		title   string
		expr    string
		desc    string
		covered bool
		want    string
	}{
		"the modules' grant, the job's account named outright": {
			member: catalogCheckMember, role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle(slug), expr: records, covered: true},
		"the modules' grant, the member computed from the account the module creates": {
			sa: CatalogCheckAccountID, role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle(slug), expr: records, covered: true},
		"no condition at all": {
			member: catalogCheckMember, role: "roles/storage.objectUser", noCond: true,
			want: "no condition"},
		"a repository job's prefix condition": {
			member: catalogCheckMember, role: "roles/storage.objectUser", title: "fugaro-x",
			expr: gcp.BucketCondition(cbBucket, gcp.JobBucketPrefixes, slug), want: "catalog check job"},
		"the build account's read of the copied definitions": {
			member: catalogCheckMember, role: "roles/storage.objectUser",
			title: gcp.CatalogReadConditionTitle(CatalogCheckAccountID), expr: gcp.CatalogReadCondition(cbBucket),
			want: "catalog check job"},
		"the launchers' runs/ condition": {
			member: catalogCheckMember, role: "roles/storage.objectUser",
			title: gcp.LauncherBucketConditionTitle, expr: gcp.LauncherBucketCondition(cbBucket),
			want: "catalog check job"},
		"the records condition retitled for another repository": {
			member: catalogCheckMember, role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle("some-other-repo-0123456789abcdef"), expr: records,
			want: "catalog check job"},
		"the records condition on another bucket": {
			member: catalogCheckMember, role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle(slug), expr: gcp.CatalogRecordsCondition("fugaro-runs-other", slug),
			want: "catalog check job"},
		"the records condition widened past the catalog segment": {
			member: catalogCheckMember, role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle(slug),
			expr:  gcp.BucketCondition(cbBucket, []string{"builds"}, slug), want: "catalog check job"},
		// The title and the expression are BOTH the plan's words, and the
		// module builds the expression from the slug the title carries, so a
		// slug that is not a slug builds whatever condition the plan asks for
		// and the comparison below admits it. These two are that attack: the
		// first closes the clause's string literal and opens a condition that
		// matches nearly the whole bucket (every repository's builds/, runs/
		// and tokens/ with it), which is what this account's row exists to
		// refuse; the second walks a path out of the catalogue's own prefix.
		"a title whose slug closes the condition's string literal": {
			member: catalogCheckMember, role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle(`") || !resource.name.endsWith("`),
			expr:  gcp.CatalogRecordsCondition(cbBucket, `") || !resource.name.endsWith("`),
			want:  "catalog check job"},
		"a title whose slug walks out of the catalog segment": {
			member: catalogCheckMember, role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle(`x/../../tokens/y`),
			expr:  gcp.CatalogRecordsCondition(cbBucket, `x/../../tokens/y`),
			want:  "catalog check job"},
		"the records condition with a description": {
			member: catalogCheckMember, role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle(slug), expr: records, desc: "x", want: "description"},
		"another kind of principal: a repository's job account": {
			member: cbJobSA, role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle(slug), expr: records, want: "condition"},
		"another kind of principal: a build account": {
			member: cbBuildSA, role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle(slug), expr: records, want: "condition"},
		"another kind of principal: a person on the review screen": {
			member: cbPerson, role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle(slug), expr: records, want: "to a person"},
		"another kind of principal: a lookalike account in another project": {
			member: "serviceAccount:" + CatalogCheckAccountID + "@fp-2.iam.gserviceaccount.com",
			role:   "roles/storage.objectUser", title: gcp.CatalogRecordsConditionTitle(slug), expr: records,
			want: "not on the review screen"},
		"another kind of principal: an account the module creates for something else": {
			sa: "fugaro-history", role: "roles/storage.objectUser",
			title: gcp.CatalogRecordsConditionTitle(slug), expr: records, want: "condition"},
		"another role: objectAdmin under the records condition": {
			member: catalogCheckMember, role: "roles/storage.objectAdmin",
			title: gcp.CatalogRecordsConditionTitle(slug), expr: records, want: "the installation's modules do not grant it to"},
		"another role: objectAdmin with no condition": {
			member: catalogCheckMember, role: "roles/storage.objectAdmin", noCond: true,
			want: "the installation's modules do not grant it to"},
		"another role: objectViewer under the records condition": {
			member: catalogCheckMember, role: "roles/storage.objectViewer",
			title: gcp.CatalogRecordsConditionTitle(slug), expr: records, want: "the installation's modules do not grant it to"},
	} {
		t.Run(name, func(t *testing.T) {
			after := map[string]any{"bucket": cbBucket, "member": tc.member, "role": tc.role}
			unk := map[string]any{}
			if tc.member == "" {
				unk["member"] = true
			}
			conds := []any{}
			if !tc.noCond {
				conds = []any{map[string]any{"title": tc.title, "expression": tc.expr, "description": tc.desc}}
				unk["condition"] = []any{map[string]any{}}
			}
			after["condition"] = conds
			changes := []ResourceChange{{
				Address: `module.installation.google_storage_bucket_iam_member.catalog_check_records["` + slug + `"]`,
				Type:    "google_storage_bucket_iam_member",
				Change:  Change{Actions: []string{"create"}, After: after, AfterUnknown: unk},
			}}
			if tc.sa != "" {
				changes = append([]ResourceChange{{
					Address: "module.installation.google_service_account.catalog_check", Type: "google_service_account",
					Change: Change{Actions: []string{"create"}, After: map[string]any{"account_id": tc.sa}},
				}}, changes...)
			}
			got := c.NotCovered(&Plan{ResourceChanges: changes}, nil)
			if tc.covered {
				if len(got) != 0 {
					t.Errorf("NotCovered = %q, want the modules' own records grant", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatal("NotCovered = nil, want a refusal: the records grant must be this exact condition for this account and no other shape")
			}
			if !strings.Contains(got[0], tc.want) {
				t.Errorf("NotCovered = %q, want it to contain %q", got[0], tc.want)
			}
		})
	}
}

// reportsMember is the project's reports job account as a plan names it: the
// account the installation module's reports resource creates (reports.tf,
// ReportsAccountID).
const reportsMember = "serviceAccount:" + ReportsAccountID + "@proj-1234.iam.gserviceaccount.com"

// TestReportsAccountIsItsOwnPrincipalKind: the project's reports job (slice P,
// ruling E4) is a principal of its own kind, like the credentials service's
// account, the coordinator's writer and the project's catalog check job, and
// for the same reason: as one of FugaroAccounts every toAccounts row would
// have accepted it, and this account's reach is not a job account's — it reads
// every repository's run records and it is the one writer of reports/, a write
// no launcher and no job holds. Nothing else may accept it either: an account
// that could ride one of the tables' rows would be one whose next grant (a
// secret accessor, an IAM admin role, a key) is covered silently.
//
// Exactly two rows carry the bit — the bucket's objectViewer and objectUser —
// and what makes each grant the modules' own is the CONDITION and the bucket,
// which the matrix below does not reach and TestReportsGrantsAreTheConditions
// does: the account, one of its two prefixes of one of this run's buckets,
// under the title built from it.
//
// Mutation (run, restore): give a third row the toReports bit and the row loop
// fails; drop the isReportsMember case from checkGrant and the kind-sentence
// case fails; add ReportsAccountID to FugaroAccounts and the first check fails.
func TestReportsAccountIsItsOwnPrincipalKind(t *testing.T) {
	if slices.Contains(FugaroAccounts, ReportsAccountID) {
		t.Errorf("the project's reports job is one of FugaroAccounts, so every toAccounts row accepts it")
	}
	rows := 0
	for typ, row := range grantRules {
		for key, w := range row {
			if w&toReports == 0 {
				continue
			}
			rows++
			if typ != "google_storage_bucket_iam_member" || (key != "roles/storage.objectViewer" && key != "roles/storage.objectUser") {
				t.Errorf("%s %s: the row accepts the reports job's account, whose grants are its two conditioned bucket bindings and are refused here one by one", typ, key)
			}
		}
	}
	if rows != 2 {
		t.Errorf("%d rows carry toReports, want the two the modules write for this account: the bucket's objectViewer and objectUser", rows)
	}

	roles := map[string][]string{}
	for typ, row := range grantRules {
		for key := range row {
			roles[typ] = append(roles[typ], grantRoleString(key))
		}
	}
	roles["google_project_iam_member"] = append(roles["google_project_iam_member"],
		"roles/owner", "roles/editor", "roles/iam.serviceAccountAdmin",
		"roles/iam.serviceAccountTokenCreator", "roles/iam.serviceAccountKeyAdmin", "roles/storage.objectAdmin")
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}, Region: testCover.Region}
	for typ, list := range roles {
		for _, role := range slices.Sorted(slices.Values(list)) {
			after := map[string]any{"member": reportsMember, "role": role, "bucket": cbBucket}
			withCondFor(after, typ, role, false)
			ch := rc(typ, after, map[string]any{}, "create")
			if got := c.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
				t.Errorf("%s: %s to the reports job is covered: every grant to this account is an owner-run apply", typ, role)
			}
		}
	}

	// The refusal says which kind of principal this is, and not that the
	// address is missing from the review screen.
	ch := rc("google_project_iam_member", map[string]any{"member": reportsMember, "role": "roles/logging.logWriter", "bucket": cbBucket}, map[string]any{}, "create")
	if got := c.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) != 1 ||
		!strings.Contains(got[0], "a kind of principal the installation's modules do not grant it to") {
		t.Errorf("a project grant to the reports job: NotCovered = %q, want the refusal that names its own kind of principal", got)
	}

	// The account is the installation's: a lookalike in another project is
	// nobody Fugaro creates.
	ch = rc("google_project_iam_member", map[string]any{"member": "serviceAccount:" + ReportsAccountID + "@fp-2.iam.gserviceaccount.com", "role": "roles/logging.logWriter", "bucket": cbBucket}, map[string]any{}, "create")
	if got := c.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
		t.Errorf("the log writer is covered for %s@fp-2: the reports job is %s@proj-1234 and nothing else", ReportsAccountID, ReportsAccountID)
	}

	// Listed on the review screen by mistake, it is still not a person: the
	// kind is read before the list, as the deployer's and the catalog check
	// job's are.
	listed := Cover{Projects: c.Projects, Buckets: c.Buckets, Listed: []string{reportsMember}, Region: c.Region}
	for _, role := range []string{"roles/storage.objectViewer", "roles/run.invoker", "roles/logging.logWriter"} {
		ch := rc("google_project_iam_member", map[string]any{"member": reportsMember, "role": role, "bucket": cbBucket}, map[string]any{}, "create")
		if got := listed.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
			t.Errorf("%s to the reports job, listed on the review screen, is covered", role)
		}
	}
}

// reportsGrantPlan is one of the reports job's two bucket grants as a plan
// carries it: the account created in the same module (a computed member, which
// is what the first apply's plan writes) or named outright (an update plan),
// and the one condition the case writes.
func reportsGrantPlan(computed bool, role string, cnd map[string]any) *Plan {
	conds := []any{}
	if cnd != nil {
		conds = []any{cnd}
	}
	after := map[string]any{"bucket": cbBucket, "role": role, "condition": conds}
	unk := map[string]any{"condition": []any{map[string]any{}}}
	if computed {
		unk["member"] = true
	} else {
		after["member"] = reportsMember
	}
	return &Plan{ResourceChanges: []ResourceChange{
		{Address: "module.installation.google_service_account.reports", Type: "google_service_account",
			Change: Change{Actions: []string{"create"}, After: map[string]any{"account_id": ReportsAccountID}}},
		{Address: "module.installation.google_storage_bucket_iam_member.reports_read", Type: "google_storage_bucket_iam_member",
			Change: Change{Actions: []string{"create"}, After: after, AfterUnknown: unk}},
	}}
}

// TestReportsGrantsAreTheConditions: the two rows that accept the reports job's
// account accept exactly the two bindings the module writes, and the CONDITION
// is what makes them the modules' own — the account, one prefix of one of this
// run's buckets, under the title built from that account, byte for byte
// gcp.ReportsReadCondition or gcp.ReportsWriteCondition.
//
// The two the plan must REFUSE are the ones a reader reaches for: the write
// role under the read condition (objectUser on runs/, which is the launchers'
// own area) and the read role under the write one, and — the shape the whole
// task exists for — either condition with its two clauses joined by " && "
// instead of " || ", which reads like a grant carrying both halves and matches
// no request at all. Neither prefix may be widened, the whole-bucket objectViewer
// and objectAdmin are refused outright, and the other half of the pair is the
// other grant's: the title says which one the plan meant to write, so a grant
// carrying the right bytes under the other title is refused too.
//
// Mutation (run, restore): drop either toReports bit and TestGrantRulesMatrix's
// row loop and this test's accepted cases fail; accept the writes on runs/ and
// the writes that must be refused pass.
func TestReportsGrantsAreTheConditions(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}, Region: testCover.Region}
	read := cond(gcp.ReportsReadConditionTitle(ReportsAccountID), gcp.ReportsReadCondition(cbBucket))
	write := cond(gcp.ReportsWriteConditionTitle(ReportsAccountID), gcp.ReportsWriteCondition(cbBucket))
	andJoined := cond(gcp.ReportsReadConditionTitle(ReportsAccountID),
		strings.Replace(gcp.ReportsReadCondition(cbBucket), " || ", " && ", 1))
	for _, tc := range []struct {
		name     string
		role     string
		cond     map[string]any
		computed bool
		covered  bool
		want     string
	}{
		{"the job reads runs/", "roles/storage.objectViewer", read, false, true, ""},
		{"the job writes reports/", "roles/storage.objectUser", write, false, true, ""},
		{"a first plan's computed member reads runs/", "roles/storage.objectViewer", read, true, true, ""},
		{"a first plan's computed member writes reports/", "roles/storage.objectUser", write, true, true, ""},
		// The two roles swapped: objectUser on runs/ is the launchers' own
		// write area, and the job holds no part of it.
		{"the job writes runs/", "roles/storage.objectUser", read, false, false, "reports/"},
		// ... and the reader role under the writer's condition, which is the
		// other half of the same mistake: each role is bound to its own prefix
		// and to no other.
		{"the job reads reports/ under the reader's role", "roles/storage.objectViewer", write, false, false, "runs/"},
		// The conjunction, which is the shape this task exists to refuse: both
		// halves written, and no request carries both.
		{"runs/ read under an && join", "roles/storage.objectViewer", andJoined, false, false, "reports job's condition"},
		// The title is what says whose grant it is, so the right bytes under
		// another account's title are not the module's grant.
		{"runs/ read under another account's title", "roles/storage.objectViewer",
			cond(gcp.ReportsReadConditionTitle("fugaro-other"), gcp.ReportsReadCondition(cbBucket)), false, false, "not the project's reports account"},
		// ... and the other bucket: the same bytes on a bucket of another run
		// are not this run's module.
		{"runs/ read of another bucket", "roles/storage.objectViewer",
			cond(gcp.ReportsReadConditionTitle(ReportsAccountID), gcp.ReportsReadCondition("fugaro-runs-other")), false, false, "reports job's condition"},
		{"runs/ read with its listing half dropped", "roles/storage.objectViewer",
			cond(gcp.ReportsReadConditionTitle(ReportsAccountID), `resource.name.startsWith("projects/_/buckets/`+cbBucket+`/objects/runs/")`), false, false, "reports job's condition"},
		{"unconditioned objectViewer", "roles/storage.objectViewer", nil, false, false, "unconditioned"},
		{"unconditioned objectAdmin", "roles/storage.objectAdmin", nil, false, false, "objectAdmin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := c.NotCovered(reportsGrantPlan(tc.computed, tc.role, tc.cond), nil)
			if tc.covered {
				if len(got) != 0 {
					t.Errorf("NotCovered = %q, want the module's own grant", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatal("NotCovered = nil, want a refusal: only the module's two bindings are the modules' own")
			}
			if !strings.Contains(got[0], tc.want) {
				t.Errorf("NotCovered = %q, want it to contain %q", got[0], tc.want)
			}
		})
	}

	// The account alone is not the grant: the same rule for a lookalike in
	// another project, under the right bytes and the right title.
	lookalike := reportsGrantPlan(false, "roles/storage.objectViewer", read)
	lookalike.ResourceChanges[1].Change.After["member"] = "serviceAccount:" + ReportsAccountID + "@fp-2.iam.gserviceaccount.com"
	if got := c.NotCovered(lookalike, nil); len(got) == 0 {
		t.Errorf("runs/ read of %s@fp-2 is covered: the account is the installation's own", ReportsAccountID)
	}

	// A computed member the grant's module does not create is not this
	// account's grant either, whatever its title says.
	orphan := reportsGrantPlan(true, "roles/storage.objectViewer", read)
	orphan.ResourceChanges[0].Change.After["account_id"] = "fugaro-something-else"
	if got := c.NotCovered(orphan, nil); len(got) == 0 {
		t.Error("a computed member with no reports account beside it is covered")
	}
}

// licenceMember is the renewal job's account as a plan names it.
const licenceMember = "serviceAccount:" + LicenceAccountID + "@proj-1234.iam.gserviceaccount.com"

// licenceGrantPlan is one of the renewal job's two bucket grants as a plan
// carries it: the account created in the same module (a computed member, which
// is what the first apply's plan writes) or named outright (an update plan),
// and the one condition the case writes.
func licenceGrantPlan(computed bool, role string, cnd map[string]any) *Plan {
	conds := []any{}
	if cnd != nil {
		conds = []any{cnd}
	}
	after := map[string]any{"bucket": cbBucket, "role": role, "condition": conds}
	unk := map[string]any{"condition": []any{map[string]any{}}}
	if computed {
		unk["member"] = true
	} else {
		after["member"] = licenceMember
	}
	return &Plan{ResourceChanges: []ResourceChange{
		{Address: "module.installation.google_service_account.licence", Type: "google_service_account",
			Change: Change{Actions: []string{"create"}, After: map[string]any{"account_id": LicenceAccountID}}},
		{Address: "module.installation.google_storage_bucket_iam_member.licence_read", Type: "google_storage_bucket_iam_member",
			Change: Change{Actions: []string{"create"}, After: after, AfterUnknown: unk}},
	}}
}

// TestLicenceGrantsAreTheConditions: the two rows that accept the renewal
// job's account accept exactly the two bindings the module writes, and the
// CONDITION is what makes them the modules' own — one prefix of one of this
// run's buckets under the title built from that account, byte for byte
// gcp.LicenceReadCondition or gcp.LicenceWriteCondition.
//
// The shape the account may NOT hold is the one the reports job's rule already
// refuses and the comment beside the licence bits claims for this account too:
// an UNCONDITIONED bucket-wide grant. Every other account's unconditioned
// objectViewer is a job reading the whole bucket, which is the modules' choice
// for them; this account holds the two conditioned grants and nothing else, so
// a whole-bucket read would reach the run records, the prompts, the
// transcripts, the sealed tokens, the cache, the locks and the issue store,
// none of which the job has any business reading (plan K4.2, D524).
//
// Mutation (verified, run and restored): deleting licenceGrant's
// member-mismatch return covers the orphan plan below — the plan that creates
// some other service account, so the member reads as an account and the row
// admits it — and leaves the rest of the test green, which is what makes that
// case the pin on the branch rather than on the condition rows.
func TestLicenceGrantsAreTheConditions(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}, Region: testCover.Region}
	read := cond(gcp.LicenceReadConditionTitle(LicenceAccountID), gcp.LicenceReadCondition(cbBucket))
	write := cond(gcp.LicenceWriteConditionTitle(LicenceAccountID), gcp.LicenceWriteCondition(cbBucket))
	for _, tc := range []struct {
		name     string
		role     string
		cond     map[string]any
		computed bool
		covered  bool
		want     string
	}{
		{"the job reads its three objects", "roles/storage.objectViewer", read, false, true, ""},
		{"the job writes the licence object", "roles/storage.objectUser", write, false, true, ""},
		{"a first plan's computed member reads its three objects", "roles/storage.objectViewer", read, true, true, ""},
		// The read role under the write condition's title, and the read
		// condition carrying the write's bytes: the title says which of the
		// two grants the plan meant to write, so neither is read as the other.
		{"the job reads under the write condition's title", "roles/storage.objectViewer",
			cond(gcp.LicenceWriteConditionTitle(LicenceAccountID), gcp.LicenceReadCondition(cbBucket)), false, false, "not its three-object read of this run's bucket"},
		{"the job reads under the read title with the write's bytes", "roles/storage.objectViewer",
			cond(gcp.LicenceReadConditionTitle(LicenceAccountID), gcp.LicenceWriteCondition(cbBucket)), false, false, "not the licence condition of this run's bucket"},
		// The two roles swapped: each is bound to its own condition and to no
		// other, so the write role under the read condition is refused rather
		// than read as either grant.
		{"the job writes under the read condition", "roles/storage.objectUser", read, false, false, "is not the licence object's own"},
		// ... and the whole bucket, which is the grant no module writes: the
		// account's reach is the two conditioned rows above and nothing else.
		{"unconditioned objectViewer", "roles/storage.objectViewer", nil, false, false, "unconditioned, to the installation's licence renewal job"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := c.NotCovered(licenceGrantPlan(tc.computed, tc.role, tc.cond), nil)
			if tc.covered {
				if len(got) != 0 {
					t.Errorf("NotCovered = %q, want the module's own grant", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatal("NotCovered = nil, want a refusal: only the module's two bindings are the module's own")
			}
			if !strings.Contains(got[0], tc.want) {
				t.Errorf("NotCovered = %q, want it to contain %q", got[0], tc.want)
			}
		})
	}

	// The account alone is not the grant: the same rule for a lookalike in
	// another project, under the right bytes and the right title.
	lookalike := licenceGrantPlan(false, "roles/storage.objectViewer", read)
	lookalike.ResourceChanges[1].Change.After["member"] = "serviceAccount:" + LicenceAccountID + "@fp-2.iam.gserviceaccount.com"
	if got := c.NotCovered(lookalike, nil); len(got) == 0 {
		t.Errorf("the three-object read of %s@fp-2 is covered: the account is the installation's own", LicenceAccountID)
	}

	// A computed member under a licence title is the account's grant only when
	// the plan creates that account in the grant's own module: the title is
	// built from the account, so a plan that computes the member from ANOTHER
	// service account of the module — the orphan below, whose account_id is not
	// the licence one — would otherwise be covered on the strength of a name it
	// only claims, and the cover would report a binding no reader can hold to
	// the account.
	orphan := licenceGrantPlan(true, "roles/storage.objectViewer", read)
	orphan.ResourceChanges[0].Change.After["account_id"] = "fugaro-something-else"
	if got := c.NotCovered(orphan, nil); len(got) == 0 {
		t.Errorf("a computed member with no %s account beside it is covered: the licence condition is only this account's grant", LicenceAccountID)
	}
}

// TestALicenceConditionTitleNamesItsOwnAccount: the second half of the rule
// licenceGrant applies to the renewal job's two bucket bindings (plan K4.2),
// and the half no other test of this package reads. The account's ID is a fixed
// literal — the module's variable validation pins it, LicenceAccountID — so
// nothing about a licence grant is derived from its member, and the TITLE's
// suffix is what says whose condition the plan claims to be writing.
//
// Without this check a grant titled for another account would be read as this
// account's own and covered: the member is the renewal job's, the role is one
// of its two, and the expression is the builder's own bytes for this run's
// bucket, so every other step of licenceGrant passes. The consequence is the
// one the read grant's three names exist to bound — the account would hold a
// condition of its own naming under the ordinary update confirmation, and the
// cover would report the grant that was written rather than the one the plan's
// title announces.
//
// The first row is the control: the module's own title, on the module's own
// bytes, is still the module's grant, so the two foreign-title rows cannot pass
// by a check that refuses every licence-shaped title.
//
// Mutation (verified, run and restored): deleting licenceGrant's
// `id != LicenceAccountID` return covers both foreign-title rows — the first
// two fail with "NotCovered = nil" — and leaves the control green, which is
// what makes those two rows the pin on the check rather than on the bytes
// around it.
func TestALicenceConditionTitleNamesItsOwnAccount(t *testing.T) {
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}, Region: testCover.Region}
	const foreign = LicenceAccountID + "-old"
	for _, tc := range []struct {
		name    string
		role    string
		cond    map[string]any
		covered bool
	}{
		{"the read title names this account", "roles/storage.objectViewer",
			cond(gcp.LicenceReadConditionTitle(LicenceAccountID), gcp.LicenceReadCondition(cbBucket)), true},
		{"the read title names another account", "roles/storage.objectViewer",
			cond(gcp.LicenceReadConditionTitle(foreign), gcp.LicenceReadCondition(cbBucket)), false},
		{"the write title names another account", "roles/storage.objectUser",
			cond(gcp.LicenceWriteConditionTitle(foreign), gcp.LicenceWriteCondition(cbBucket)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := c.NotCovered(licenceGrantPlan(false, tc.role, tc.cond), nil)
			if tc.covered {
				if len(got) != 0 {
					t.Errorf("NotCovered = %q, want the module's own grant", got)
				}
				return
			}
			join := strings.Join(got, "\n")
			if len(got) == 0 {
				t.Fatal("NotCovered = nil, want a refusal: the title's account is the only thing that says whose condition this is")
			}
			if want := "which is not the installation's licence account"; !strings.Contains(join, want) {
				t.Errorf("NotCovered = %q, want it to contain %q", got, want)
			}
		})
	}
}

// TestLicenceAccountIsRefusedAComputedRole: checkGrant's computed-role branch
// for the renewal job's account (plan K4.2), which the dynamic class's own test
// does not reach (it names the intake and the credentials service). The job's
// Secret Manager role is this module's own custom role (fugaroLicenceRenewal),
// written as a literal in licence.tf, and licenceSecretGrant's whole check is
// the container that literal is bound on: a role the plan cannot name, bound on
// whatever container the plan likes, is a Secret Manager reach nobody can hold
// to that one container, and it would ride the ordinary update confirmation.
// The refusal names the account, so an operator's read of it says which grant
// to write by name.
//
// Every type is refused, not just the secret binding the reason is about: the
// branch is reached from the member's kind, before the type's row is looked up,
// so a computed role on a bucket or a project binding — where the account has
// no role at all — stops with the account's own sentence rather than with the
// table's.
//
// Mutation (verified, run and restored): deleting the `kind == toLicence`
// return in checkGrant's unknown-role case makes all four rows stop for the
// table's own reason instead of the account's — "a role that is not known and
// no custom role is created with it" — and each fails on the wanted sentence,
// which is what makes this test the pin on that branch rather than on the
// refusals around it.
func TestLicenceAccountIsRefusedAComputedRole(t *testing.T) {
	// The installation's project is proj-1234, as licenceMember is named
	// (isLicenceMember reads the member and Projects[0], in that order).
	c := Cover{Projects: []string{"proj-1234"}, Buckets: []string{cbBucket}, Listed: []string{cbPerson}, Region: testCover.Region}
	for _, typ := range []string{"google_project_iam_member", "google_secret_manager_secret_iam_member", "google_storage_bucket_iam_member", "google_cloud_run_v2_service_iam_member"} {
		after := map[string]any{"member": licenceMember, "bucket": cbBucket, "secret_id": "fugaro-project-proj-1234-licence-renewal-0e382f732fd21bbd"}
		ch := rc(typ, after, map[string]any{"role": true}, "create")
		got := c.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
		if len(got) == 0 {
			t.Errorf("%s: a computed role to the installation's licence renewal job is covered: its own custom role is written as a literal and the container it is bound on is what the cover checks (plan K4.2)", typ)
			continue
		}
		if want := "a computed role to the installation's licence renewal job"; !strings.Contains(strings.Join(got, "\n"), want) {
			t.Errorf("%s: a computed role to the renewal job stopped for another reason: %q, want %q", typ, got, want)
		}
	}
}

// TestMCPAccountIsItsOwnPrincipalKind: the front door's own account (D920,
// D935) is a principal of its own kind, like the credentials service's, the
// coordinator's, the verifier's and the project's catalog check job, and for
// the same reason: as one of FugaroAccounts every toAccounts row would have
// accepted it, and this account serves a public endpoint — a plan that rode
// one of those rows would be one grant away from making spec.md §8's "there is
// no credential to reach" false.
//
// Its ONE grant is the store's: the account, roles/storage.objectAdmin,
// `fugaro-mcpauth-<the installation's project>`, no condition. The bit alone is
// not enough — mcpauthStoreGrant narrows the row to that grant, the way
// catalogRecordsGrant narrows the catalog check job's objectUser one — so the
// runs bucket's objectAdmin (which the operators hold and this account does
// not), the store's role to another member and the store's role under a
// condition are each refused.
//
// The covered case is the point of the change: `fugaro update --mcp-server` is
// one command, and the store's grant is one of the two things it creates, so a
// plan review that refused it would ask the owner for a second confirmation on
// the release that turns the front door on (M10's finding on this task's first
// round).
//
// Mutation (run, restore): give any OTHER row the toMCP bit and the row loop
// fails; drop the isMCPMember case from checkGrant and the covered case fails
// (the account reads as an address nobody listed); drop mcpauthStoreGrant from
// grant and the runs bucket's objectAdmin to this account is covered — the bit
// alone, with no bucket to narrow it — which is what makes the check the
// narrowing rather than the row.
func TestMCPAccountIsItsOwnPrincipalKind(t *testing.T) {
	const store = MCPServerStorePrefix + "proj-1"
	const member = "serviceAccount:" + MCPServerAccountID + "@proj-1.iam.gserviceaccount.com"
	const rowType, rowRole = "google_storage_bucket_iam_member", "roles/storage.objectAdmin"
	if slices.Contains(FugaroAccounts, MCPServerAccountID) {
		t.Errorf("the front door's account is one of FugaroAccounts, so every toAccounts row accepts it")
	}
	// The account's two grants and no third (O16b added the state group's read
	// to D935's store): the table is where a grant of another role to this
	// account would have to be written down, and this is the count that makes
	// writing one a decision. Each of the two is narrowed further by its own
	// check — the store's by the bucket (mcpauthStoreGrant, below), the state
	// group's by its role and project (mcpauthStateReadGrant,
	// TestCoverAdmitsTheStateReadGrantOnPurpose).
	wantRows := map[string]string{
		rowType:                     rowRole,
		"google_project_iam_member": customKey + MCPStateReadRole,
	}
	got := map[string]string{}
	for typ, row := range grantRules {
		for key, w := range row {
			if w&toMCP == 0 {
				continue
			}
			if _, twice := got[typ]; twice {
				t.Errorf("%s carries toMCP on more than one role: the front door's account holds the store's objectAdmin and the database's read, and a third role is a widening this table has not decided", typ)
			}
			got[typ] = key
		}
	}
	if !maps.Equal(got, wantRows) {
		t.Errorf("the rows carrying toMCP are %v, want %v: the front door's account holds D935's store and the orchestrator's node, read-only, and nothing else (D920, D935, O16b)", got, wantRows)
	}

	// The grant as the module writes it, on the store's own bucket.
	storeGrant := rc(rowType, map[string]any{"member": member, "role": rowRole, "bucket": store}, map[string]any{}, "create")
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{storeGrant}}, nil); len(got) != 0 {
		t.Errorf("D935's store grant to the front door's account: NotCovered = %q, want it covered: the store and its one grant are what `fugaro update --mcp-server` creates, and a plan review that refused them would ask for a second confirmation on the release that turns the front door on", got)
	}

	// Everything else this account could be handed, one case each. The
	// condition is the shape the modules write elsewhere (a bucket grant's one
	// condition block), so the case is about the store's grant carrying none
	// and not about the expression.
	cond := []any{map[string]any{"title": gcp.LauncherBucketConditionTitle, "expression": gcp.LauncherBucketCondition(store), "description": nil}}
	for _, tc := range []struct {
		what  string
		after map[string]any
	}{
		{"objectViewer on the store", map[string]any{"member": member, "role": "roles/storage.objectViewer", "bucket": store}},
		{"objectAdmin on the store to the operators' own bucket", map[string]any{"member": member, "role": rowRole, "bucket": "fugaro-runs-proj-1"}},
		{"objectAdmin on the store to a bucket that is nobody's", map[string]any{"member": member, "role": rowRole, "bucket": "someone-elses-bucket"}},
		{"objectAdmin on the store to a person on the review screen", map[string]any{"member": "user:me@example.com", "role": rowRole, "bucket": store}},
		{"objectAdmin on the store to a job account", map[string]any{"member": "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com", "role": rowRole, "bucket": store}},
		{"objectAdmin on the store to a lookalike in another project", map[string]any{"member": "serviceAccount:" + MCPServerAccountID + "@fp-2.iam.gserviceaccount.com", "role": rowRole, "bucket": store}},
		{"objectAdmin on the store under a condition", map[string]any{"member": member, "role": rowRole, "bucket": store, "condition": cond}},
		{"objectAdmin on the store with a computed role", map[string]any{"member": member, "bucket": store}},
	} {
		ch := rc(rowType, tc.after, map[string]any{}, "create")
		if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
			t.Errorf("%s is covered: the front door's account holds one grant, roles/storage.objectAdmin on the store D935 gives it, and nothing else", tc.what)
		}
	}

	// Listed on the review screen by mistake, it is still not a person: the
	// kind is read before the list, as the deployer's, the credentials
	// service's and the catalog check job's are, so a listed address cannot
	// hand this account a role of the operators' own. (The store's own grant
	// stays covered when the address is listed, and that is the row being the
	// account's and not the list: what a person's row would add to this account
	// — the runs bucket's objectAdmin — is refused here.)
	listed := Cover{Projects: testCover.Projects, Region: testCover.Region, Buckets: testCover.Buckets, Listed: []string{member}}
	ch := rc(rowType, map[string]any{"member": member, "role": rowRole, "bucket": "fugaro-runs-proj-1"}, map[string]any{}, "create")
	if got := listed.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
		t.Errorf("objectAdmin on the runs bucket to the front door's account, listed on the review screen, is covered: the account is not a person, and the runs bucket's objects are the operators'")
	}

	// And the account is the installation's: the same ID in another project is
	// nobody this installation creates, on its own project's store or not.
	for _, b := range []string{store, "fugaro-mcpauth-fp-2"} {
		ch := rc(rowType, map[string]any{"member": "serviceAccount:" + MCPServerAccountID + "@fp-2.iam.gserviceaccount.com", "role": rowRole, "bucket": b}, map[string]any{}, "create")
		if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
			t.Errorf("objectAdmin on %s to %s@fp-2 is covered: the front door is %s@proj-1 and nothing else", b, MCPServerAccountID, MCPServerAccountID)
		}
	}
}

// TestCoverAdmitsTheStateReadGrantOnPurpose is O16b's half of the plan
// review: the front door's read of the orchestrator's node is covered, and
// nothing but that one grant is.
//
// It has to be covered, because the run's one confirmation is what applies the
// release that turns the front door on: a plan review that refused the grant
// would ask the owner for a second confirmation over a step that is meant to
// be one. And it has to be the ONLY thing covered for this account, because
// Realtime Database IAM has no path-level scope: a project binding of this
// role is a read of the WHOLE database of that project — every run's state,
// the policy, the kill switches and the credentials' own nodes — so the grant
// is the reach D2020 (a) answered for, and the role's permission list
// (MCPStateReadPermissions) is where the read/write direction is decided.
//
// The mutations this fails on: the state-read row dropped from grantRules (the
// grant would be refused and the release would ask twice), the row widened to
// another kind of principal, the role's pin given the write, the role moved to
// another project than the grant's, and a condition on the binding.
func TestCoverAdmitsTheStateReadGrantOnPurpose(t *testing.T) {
	const member = "serviceAccount:" + MCPServerAccountID + "@proj-1.iam.gserviceaccount.com"
	const role = "projects/proj-1/roles/" + MCPStateReadRole
	const rowType = "google_project_iam_member"

	// The pin, the module's permission list and the list this file exports are
	// one list: the role the module creates is the role the review reads, and
	// neither may gain a second permission.
	if want := MCPStateReadPermissions; !slices.Equal(RolePermissions[MCPStateReadRole], want) {
		t.Errorf("RolePermissions[%s] = %v, want exactly %v: a second permission here is a second permission over the whole database", MCPStateReadRole, RolePermissions[MCPStateReadRole], want)
	}
	if !slices.Contains(FugaroRoles, MCPStateReadRole) {
		t.Errorf("FugaroRoles does not name %s, so a grant of this role is refused outright", MCPStateReadRole)
	}
	if slices.Contains(MCPStateReadPermissions, "firebasedatabase.instances.update") {
		t.Errorf("MCPStateReadPermissions holds the write: the state group's writes stay with `fugaro orchestrator patch` and the person's own credential (D1223, D1241), and apply_patch is served by nothing (D2021)")
	}

	// The grant as the module writes it: the front door's own account, the
	// role on the project it was declared in, no condition.
	grant := rc(rowType, map[string]any{"member": member, "role": role, "project": "proj-1"}, map[string]any{}, "create")
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{grant}}, nil); len(got) != 0 {
		t.Errorf("O16b's state-read grant to the front door's account: NotCovered = %q, want it covered: the grant is what `fugaro update --mcp-server` creates for the state group, and a plan review that refused it would ask the owner for a second confirmation", got)
	}

	// Everything else that could arrive in its place, one case each: the read
	// on another project's role, the write role (the credentials service's own,
	// which is the two data permissions), another member, another role of the
	// account's, and a condition the module never writes (RTDB cannot express a
	// path prefix, so the condition would fence nothing).
	cond := []any{map[string]any{"title": gcp.LauncherBucketConditionTitle, "expression": gcp.LauncherBucketCondition("fugaro-runs-proj-1"), "description": nil}}
	for _, tc := range []struct {
		what  string
		after map[string]any
	}{
		{"the read declared in a project the grant is not in", map[string]any{"member": member, "role": "projects/fp-2/roles/" + MCPStateReadRole, "project": "proj-1"}},
		{"the read on a project this run does not know", map[string]any{"member": member, "role": "projects/fp-3/roles/" + MCPStateReadRole, "project": "fp-3"}},
		{"the credentials service's database role, which holds the write too", map[string]any{"member": member, "role": "projects/proj-1/roles/" + CredentialsDatabaseRole, "project": "proj-1"}},
		{"the store's role on the project", map[string]any{"member": member, "role": "roles/storage.objectAdmin", "project": "proj-1"}},
		{"the read to a person on the review screen", map[string]any{"member": "user:me@example.com", "role": role, "project": "proj-1"}},
		{"the read to a job account", map[string]any{"member": "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com", "role": role, "project": "proj-1"}},
		{"the read to a lookalike in another project", map[string]any{"member": "serviceAccount:" + MCPServerAccountID + "@fp-2.iam.gserviceaccount.com", "role": role, "project": "proj-1"}},
		{"the read under a condition", map[string]any{"member": member, "role": role, "project": "proj-1", "condition": cond}},
		{"the read with a computed role", map[string]any{"member": member, "project": "proj-1"}},
	} {
		ch := rc(rowType, tc.after, map[string]any{}, "create")
		if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
			t.Errorf("%s is covered: the front door's account reads the orchestrator's node through one custom role on one project, and every other grant is a widening no `fugaro update` confirmation announced", tc.what)
		}
	}
}

// TestCoverAdmitsTheVoiceHopsStateReadGrantOnPurpose is the hop's half of the
// same plan review (P5.3.3): the account voice.tf creates holds the one grant
// voiceStateReadGrant narrows to, and nothing else is covered for it.
//
// It is the front door's test one account over, and it exists for the reason
// the account does. The two accounts hold the same role — Realtime Database IAM
// has the one read there is, so a second role could only differ by holding the
// write state_read.tf exists to withhold — but this account runs the ONE
// service of the tree anyone on the internet may call (voice.tf's allUsers
// binding), so the instance-wide read the role carries is the reach a widening
// edit would hand the public internet, and the run's one `fugaro update`
// confirmation is what applies it. The member is read before the guard runs
// (grant's isVoiceMember, on the installation's project), so what is left here
// is the role, the project it was declared in and the absence of a condition.
//
// The mutations this fails on: voiceStateReadGrant neutered to `return ""` (the
// role declared in another project than the grant's, and the condition, are
// then covered), its call site in grant's chain removed (the same two), and the
// toVoice bit moved off the state-read row (the good grant would be refused and
// the release would ask twice).
func TestCoverAdmitsTheVoiceHopsStateReadGrantOnPurpose(t *testing.T) {
	const member = "serviceAccount:" + VoiceAccountID + "@proj-1.iam.gserviceaccount.com"
	const role = "projects/proj-1/roles/" + MCPStateReadRole
	const rowType = "google_project_iam_member"

	// The account is not one of FugaroAccounts, and that is what makes the
	// guard the whole of its reach: as one of them every toAccounts row would
	// accept it, and this is the account behind the one service anyone on the
	// internet may call.
	if slices.Contains(FugaroAccounts, VoiceAccountID) {
		t.Errorf("%s is in FugaroAccounts: the hop's account holds one grant, and a toAccounts row would accept every role a job's row carries", VoiceAccountID)
	}

	// The grant as voice.tf writes it: the hop's own account, the shared read
	// role on the project it was declared in, no condition. (The module
	// declares the role in the FIREBASE project — the one the database belongs
	// to — which is one of this run's projects and not necessarily the
	// installation's; the guard reads the grant's project and its role against
	// each other, so one project here is the shape.)
	grant := rc(rowType, map[string]any{"member": member, "role": role, "project": "proj-1"}, map[string]any{}, "create")
	if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{grant}}, nil); len(got) != 0 {
		t.Errorf("the hop's state-read grant: NotCovered = %q, want it covered: the grant is what `fugaro update` applies when the installation has turned the hop on, and a plan review that refused it would ask the owner for a second confirmation", got)
	}

	// Everything else that could arrive in its place, one case each: the read
	// declared in a project the grant is not in, the read on a project this run
	// does not know, the credentials service's role (which holds the write),
	// another role of the account's, another member, and a condition the module
	// never writes (RTDB cannot express a path prefix, so the condition would
	// fence nothing).
	cond := []any{map[string]any{"title": gcp.LauncherBucketConditionTitle, "expression": gcp.LauncherBucketCondition("fugaro-runs-proj-1"), "description": nil}}
	for _, tc := range []struct {
		what  string
		after map[string]any
	}{
		{"the read declared in a project the grant is not in", map[string]any{"member": member, "role": "projects/fp-2/roles/" + MCPStateReadRole, "project": "proj-1"}},
		{"the read on a project this run does not know", map[string]any{"member": member, "role": "projects/fp-3/roles/" + MCPStateReadRole, "project": "fp-3"}},
		{"the credentials service's database role, which holds the write too", map[string]any{"member": member, "role": "projects/proj-1/roles/" + CredentialsDatabaseRole, "project": "proj-1"}},
		{"the store's role on the project", map[string]any{"member": member, "role": "roles/storage.objectAdmin", "project": "proj-1"}},
		{"the read to a person on the review screen", map[string]any{"member": "user:me@example.com", "role": role, "project": "proj-1"}},
		{"the read to a job account", map[string]any{"member": "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com", "role": role, "project": "proj-1"}},
		{"the read to a lookalike in another project", map[string]any{"member": "serviceAccount:" + VoiceAccountID + "@fp-2.iam.gserviceaccount.com", "role": role, "project": "proj-1"}},
		{"the read under a condition", map[string]any{"member": member, "role": role, "project": "proj-1", "condition": cond}},
		{"the read with a computed role", map[string]any{"member": member, "project": "proj-1"}},
	} {
		ch := rc(rowType, tc.after, map[string]any{}, "create")
		if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
			t.Errorf("%s is covered: the hop's account reads the whole database instance through one custom role on one project, and every other grant is a reach the one service anyone on the internet may call would hold", tc.what)
		}
	}
}

// The front door's names in cover.go are their writers' own: this package
// cannot import internal/infra or ee/coordinator/mcpauth (both lead back to
// it), so the account ID is written twice and the store's prefix three times —
// the module that creates the bucket, the store's own Go constructor and the
// plan review. A rename on any writer's side would leave an account or a
// bucket the review no longer recognises: the store's grant would be refused on
// every installation, or — the direction that matters — a row's bit would sit
// on a name nobody creates.
func TestCoverMCPNamesAreTheWritersNames(t *testing.T) {
	for _, tc := range []struct {
		path, where string
		want        *regexp.Regexp
	}{
		{filepath.Join("..", "coordinator_service.go"), "internal/infra/coordinator_service.go",
			regexp.MustCompile(`(?m)^const MCPServerAccountID = "` + MCPServerAccountID + `"$`)},
		{filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules", "installation", "mcpauth_store.tf"),
			"deploy/terraform/gcp/modules/installation/mcpauth_store.tf",
			regexp.MustCompile(`(?m)^\s*mcpauth_store_bucket = "` + regexp.QuoteMeta(MCPServerStorePrefix) + `\$\{var\.project\}"$`)},
		{filepath.Join("..", "..", "..", "ee", "coordinator", "mcpauth", "store_bucket.go"),
			"ee/coordinator/mcpauth/store_bucket.go",
			regexp.MustCompile(`(?m)^func StoreBucketName\(gcpProject string\) string \{ return "` + regexp.QuoteMeta(MCPServerStorePrefix) + `" \+ gcpProject \}$`)},
	} {
		b, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if !tc.want.Match(b) {
			t.Errorf("%s declares no name tf/cover.go holds the front door's account and its store by: update tf/cover.go's literal with it", tc.where)
		}
	}
}

// The row the viewer's project role is granted through — RS4's "cover test
// that the new row requires its condition". The row's one condition is the
// KIND of principal: a member the run's review screen lists, which is a person,
// a group or a service account someone named, and nothing Fugaro creates for
// itself. That is what keeps it from being the launcher's row, where the same
// table takes the accounts too (toPeople | toAccounts): an installation's own
// job, build, history, coordinator, verifier, catalog-check or front-door
// account holds the grants its work needs, and the read of every run is not one
// of them.
//
// Fails when the row is widened to the accounts or to one kind of Fugaro
// account alone (a condition function), which is how a plan that handed an
// agent's account the read of every run and its logs would pass unseen.
func TestViewerRoleIsGrantedToListedPeopleAlone(t *testing.T) {
	const (
		project = "google_project_iam_member"
		person  = "user:me@example.com"
		group   = "group:ops@example.com"
	)
	role := "projects/proj-1/roles/" + ViewerRole
	for _, c := range []struct {
		what   string
		member string
		ok     bool
	}{
		{"a person the review screen lists", person, true},
		{"a group the review screen lists", group, true},
		{"a person the review screen does not list", "user:nobody@example.com", false},
		{"a group the review screen does not list", "group:nobody@example.com", false},
		{"the history account", "serviceAccount:fugaro-history@proj-1.iam.gserviceaccount.com", false},
		{"a repository's job account", "serviceAccount:fugaro-acme-0123456789abcdef@proj-1.iam.gserviceaccount.com", false},
		{"the deployer account", "serviceAccount:" + DeployerAccount("proj-1"), false},
		{"the credentials service's account", "serviceAccount:" + CredentialsAccountID + "@proj-1.iam.gserviceaccount.com", false},
		{"the coordinator's writer account", "serviceAccount:" + CoordinatorAccountID + "@proj-1.iam.gserviceaccount.com", false},
		{"the verifier account", "serviceAccount:" + VerifierAccountID + "@proj-1.iam.gserviceaccount.com", false},
		{"the catalog check job's account", "serviceAccount:" + CatalogCheckAccountID + "@proj-1.iam.gserviceaccount.com", false},
		{"the front door's account", "serviceAccount:" + MCPServerAccountID + "@proj-1.iam.gserviceaccount.com", false},
		{"a service account outside Fugaro", "serviceAccount:builder@other.iam.gserviceaccount.com", false},
	} {
		got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{grant(project, c.member, role)}}, nil)
		if (len(got) == 0) != c.ok {
			t.Errorf("%s (%s) granted the viewer's role: NotCovered = %q, covered want %v", c.what, c.member, got, c.ok)
		}
	}
}

// The viewer's read-only job role is the launcher's list minus
// run.executions.cancel and nothing else (RS4, roles.md §4): every read a
// launcher makes a viewer makes, and the one thing a viewer cannot do is stop
// another person's run.
//
// Fails when the role narrows anything but the cancel, and — reading the
// launcher's list too — when the launcher's role loses the cancel, which would
// leave the viewer and the launcher holding one role and the difference the
// viewer's role exists for gone with nothing failing.
func TestViewerRoleHasNoCancelPermission(t *testing.T) {
	launcher, ok := RolePermissions["fugaroLauncher"]
	if !ok {
		t.Fatal("RolePermissions holds no fugaroLauncher entry")
	}
	if !slices.Contains(launcher, "run.executions.cancel") {
		t.Fatal("the launcher's role no longer holds run.executions.cancel: the viewer's role is defined as that list minus it")
	}
	var want []string
	for _, p := range launcher {
		if p != "run.executions.cancel" {
			want = append(want, p)
		}
	}
	viewer, ok := RolePermissions[ViewerRole]
	if !ok {
		t.Fatalf("RolePermissions holds no %s entry", ViewerRole)
	}
	if slices.Contains(viewer, "run.executions.cancel") {
		t.Error("the viewer's role holds run.executions.cancel: a viewer could cancel another person's run")
	}
	if !slices.Equal(slices.Sorted(slices.Values(viewer)), slices.Sorted(slices.Values(want))) {
		t.Errorf("the viewer's role holds %v, want the launcher's list minus run.executions.cancel: %v", viewer, want)
	}
	if !slices.Contains(FugaroRoles, ViewerRole) {
		t.Errorf("FugaroRoles does not name %s, so a plan that creates it is not covered", ViewerRole)
	}
}

// TestOrchestratorAccountIsItsOwnPrincipalKind: the resident orchestrator's own
// account (mobile plan task P5.1b; the mobile orchestrator design's §4.3) is a
// principal of its own kind, like the credentials service's, the
// coordinator's, the front door's and the intake's, and for their reason plus
// one of its own: as one of FugaroAccounts every toAccounts row would have
// accepted it, and this is the account that holds iam.serviceAccounts.signJwt
// on the budget signer — "may mint is exactly may launch", and signJwt signs
// any claim set for any uid, so it can be the phone and the consumer. A plan
// that rode a toAccounts row would be one job account's reach away from that.
//
// Its table is the design's and nothing else, and the table is the test: five
// cells across four resource types — the launcher's project role, the job
// runner's role on the installation's jobs, the token-minter role on the signer
// ACCOUNT (another project's, which the design grants on purpose), and the runs
// bucket's two grants the launchers hold (a conditional write and an
// unconditional read). A cell added is a grant the design's never-list did not
// price; a cell removed is a row of its table the account cannot reach.
//
// The narrowing is what the rows cannot say, so each covered case is written
// the way the module writes it: the write is under the LAUNCHERS' condition on
// this run's bucket and no other, and the read is unconditional because
// listing an object needs a permission no object-name condition can grant.
// Everything else is refused, and the three the design's eleventh never-rule
// names by hand — roles/firebasedatabase.viewer, roles/datastore.viewer and
// roles/serviceusage.serviceUsageConsumer, which is what membership of
// var.launchers would have bought — are three of the cases below.
//
// Mutation (run, restore): give any other row the toOrchestrator bit and the
// table loop fails; drop the isOrchestratorMember case from checkGrant and the
// covered cases fail (the account reads as an address nobody listed, and the
// review screen's answer is a rule that fires); drop the objectUser case from
// bucketCondition and the write is refused; widen the member predicate to any
// project and the lookalike case is covered.
func TestOrchestratorAccountIsItsOwnPrincipalKind(t *testing.T) {
	const bucketName = "fugaro-runs-proj-1"
	const member = "serviceAccount:" + OrchestratorAccountID + "@proj-1.iam.gserviceaccount.com"
	const signer = "fugaro-token-signer@fp-2.iam.gserviceaccount.com"
	if slices.Contains(FugaroAccounts, OrchestratorAccountID) {
		t.Errorf("the resident orchestrator's account is one of FugaroAccounts, so every toAccounts row accepts it — including the rows carrying a job's project reach, on an account that may mint a token as any uid (design §4.3, E4)")
	}

	want := []string{
		"google_project_iam_member|" + customKey + "fugaroLauncher",
		"google_cloud_run_v2_job_iam_member|" + customKey + "fugaroJobRunner",
		"google_service_account_iam_member|" + customKey + "fugaroTokenMinter",
		"google_storage_bucket_iam_member|roles/storage.objectUser",
		"google_storage_bucket_iam_member|roles/storage.objectViewer",
		"google_secret_manager_secret_iam_member|roles/secretmanager.secretAccessor",
		// ... and the one row that is not section 4.3's, added by the task that
		// gave the daemon an identity to the credentials service (F19, O2 of
		// docs/forge-labels): the account is the service's one caller class
		// (credsvc.OrchestratorAccounts, D2049), and Cloud Run's front end
		// refuses a caller without roles/run.invoker before the container runs.
		// Q13's answer of 2026-10-19 kept the cell out on the premise that the
		// orchestrator never calls the service at all, which the owner
		// reversed on 2026-11-17. It is the installation's OWN service and the
		// class is what answers the call.
		"google_cloud_run_v2_service_iam_member|roles/run.invoker",
		// ... nor this one, which the signing-key task added (mobile-identity
		// pairing plan task J6; O17): the consumer IS this account, so the one
		// grant of the installation's consumer key is its. The cell names the
		// principal kind and the role, and mailboxSignerGrant is what closes it
		// to this installation's one key — the row alone would admit a grant of
		// this role on any key of the project.
		"google_kms_crypto_key_iam_member|" + customKey + MailboxSignerRole,
	}
	var got []string
	for typ, row := range grantRules {
		for key, w := range row {
			if w&toOrchestrator != 0 {
				got = append(got, typ+"|"+key)
			}
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the cells carrying toOrchestrator are %v, want exactly %v: the account holds the design's §4.3 table — the launcher's role, the job runner's, the token minter's, the runs bucket's two grants, and P5.1e's per-secret accessor on the pairing key's one container — and a cell beside them is a grant the never-list has not priced (design §6 rule 11)", got, want)
	}

	launcherCond := []any{map[string]any{"title": gcp.LauncherBucketConditionTitle, "expression": gcp.LauncherBucketCondition(bucketName), "description": nil}}
	jobCond := []any{map[string]any{"title": "fugaro-x", "expression": gcp.BucketCondition(bucketName, gcp.JobBucketPrefixes, "acme-0123456789abcdef"), "description": nil}}
	for _, tc := range []struct {
		what    string
		typ     string
		after   map[string]any
		unknown map[string]any
		covered bool
	}{
		{"the launcher's project role, as the module writes it", "google_project_iam_member",
			map[string]any{"member": member, "role": "projects/proj-1/roles/fugaroLauncher", "project": "proj-1"}, map[string]any{}, true},
		{"the job runner's role on an installation job, as the root writes it", "google_cloud_run_v2_job_iam_member",
			map[string]any{"member": member, "role": "projects/proj-1/roles/fugaroJobRunner"}, map[string]any{}, true},
		// A role the plan leaves UNKNOWN is refused where no custom role is
		// created with it in the same module: nobody reading the plan can name
		// the role, and the root spells it out as a literal for exactly this
		// reason — internal/infra's plan test pins that the literal equals the
		// role_id of the role the same plan creates.
		{"the job runner's role left unknown by the plan", "google_cloud_run_v2_job_iam_member",
			map[string]any{"member": member}, map[string]any{"role": true}, false},
		{"the token minter's role on the signer account, in the Firebase project", "google_service_account_iam_member",
			map[string]any{"member": member, "role": "projects/fp-2/roles/fugaroTokenMinter", "service_account_id": "projects/fp-2/serviceAccounts/" + signer}, map[string]any{}, true},
		// The row is the signer ACCOUNT and not the role alone: signJwt on any
		// other account is that account (a self-signed JWT is an access token
		// for its issuer), so a plan that moved the grant — to a job account
		// whose secrets are the model credentials, or to any other account of
		// the run — is refused rather than covered by the role's cell
		// (orchestratorMinterGrant).
		{"the token minter's role on a job account instead of the signer", "google_service_account_iam_member",
			map[string]any{"member": member, "role": "projects/fp-2/roles/fugaroTokenMinter", "service_account_id": "projects/proj-1/serviceAccounts/fugaro-history@proj-1.iam.gserviceaccount.com"}, map[string]any{}, false},
		{"the token minter's role on the reaper account", "google_service_account_iam_member",
			map[string]any{"member": member, "role": "projects/proj-1/roles/fugaroTokenMinter", "service_account_id": "projects/proj-1/serviceAccounts/fugaro-reaper@proj-1.iam.gserviceaccount.com"}, map[string]any{}, false},
		{"the token minter's role on an account the plan leaves unknown", "google_service_account_iam_member",
			map[string]any{"member": member, "role": "projects/fp-2/roles/fugaroTokenMinter"}, map[string]any{"service_account_id": true}, false},
		{"the token minter's role on a signer lookalike in a project that is not this run's", "google_service_account_iam_member",
			map[string]any{"member": member, "role": "projects/fp-2/roles/fugaroTokenMinter", "service_account_id": "projects/fp-9/serviceAccounts/fugaro-token-signer@fp-9.iam.gserviceaccount.com"}, map[string]any{}, false},
		{"the runs bucket's conditional write, under the launchers' own condition", "google_storage_bucket_iam_member",
			map[string]any{"member": member, "role": "roles/storage.objectUser", "bucket": bucketName, "condition": launcherCond}, map[string]any{}, true},
		{"the runs bucket's read, unconditioned", "google_storage_bucket_iam_member",
			map[string]any{"member": member, "role": "roles/storage.objectViewer", "bucket": bucketName}, map[string]any{}, true},
		// P5.1e: the pairing key's cloud copy, and this is the row the design
		// states as the exception the symmetric tag forces (§6 rule 5). It is
		// the per-secret row and never the project-wide one — the case below
		// is the same role, moved to the project, and it stays refused.
		{"the per-secret accessor on the pairing key's container", "google_secret_manager_secret_iam_member",
			map[string]any{"member": member, "role": "roles/secretmanager.secretAccessor", "secret_id": "fugaro-orch-pairing"}, map[string]any{}, true},
		{"the project-wide accessor, which is the credentials service's alone", "google_project_iam_member",
			map[string]any{"member": member, "role": "roles/secretmanager.secretAccessor", "project": "proj-1"}, map[string]any{}, false},

		// The design's eleventh never-rule, named by hand: these three are what
		// being a member of var.launchers would have granted, and the whole
		// point of the account's own kind is that no row carries them.
		{"the rules-bypassing read every mailbox in the installation", "google_project_iam_member",
			map[string]any{"member": member, "role": "roles/firebasedatabase.viewer", "project": "proj-1"}, map[string]any{}, false},
		{"the spend history's read", "google_project_iam_member",
			map[string]any{"member": member, "role": "roles/datastore.viewer", "project": "proj-1"}, map[string]any{}, false},
		{"the consumer bit that comes with the list", "google_project_iam_member",
			map[string]any{"member": member, "role": "roles/serviceusage.serviceUsageConsumer", "project": "proj-1"}, map[string]any{}, false},

		// The credentials service's invoker, which Q13's answer (2026-10-19)
		// took OUT of the table and the owner put back on 2026-11-17 (F19, O2):
		// the daemon's own token is the class the service's label route and its
		// gated merge mint are served to (D2049), and a token the front end
		// refuses before the container runs reaches no class at all. The run's
		// own job token (D134) is untouched by it: the run's class is the job
		// accounts', which this row has always admitted.
		{"the credentials service's invoker", "google_cloud_run_v2_service_iam_member",
			map[string]any{"member": member, "role": "roles/run.invoker", "project": "proj-1"}, map[string]any{}, true},

		// The write, moved: another bucket, another condition, or none.
		{"the write with no condition at all", "google_storage_bucket_iam_member",
			map[string]any{"member": member, "role": "roles/storage.objectUser", "bucket": bucketName}, map[string]any{}, false},
		{"the write under a job's own prefix condition", "google_storage_bucket_iam_member",
			map[string]any{"member": member, "role": "roles/storage.objectUser", "bucket": bucketName, "condition": jobCond}, map[string]any{}, false},
		// The title on its own is not the condition: a grant carrying the
		// launchers' title with an expression over the whole bucket —
		// objects/, and so the bucket root, where the managed marker
		// fugaro/managed.json is written (design §6 rule 2) — reads as the
		// launchers' row to anything that compares titles, and is not it.
		{"the write under the launchers' title with a whole-bucket expression", "google_storage_bucket_iam_member",
			map[string]any{"member": member, "role": "roles/storage.objectUser", "bucket": bucketName,
				"condition": []any{map[string]any{"title": gcp.LauncherBucketConditionTitle, "expression": "resource.name.startsWith(\"projects/_/buckets/" + bucketName + "/objects/\")", "description": nil}}}, map[string]any{}, false},
		{"the write under the launchers' condition on another bucket", "google_storage_bucket_iam_member",
			map[string]any{"member": member, "role": "roles/storage.objectUser", "bucket": "someone-elses-bucket", "condition": launcherCond}, map[string]any{}, false},
		{"the read under a condition", "google_storage_bucket_iam_member",
			map[string]any{"member": member, "role": "roles/storage.objectViewer", "bucket": bucketName, "condition": launcherCond}, map[string]any{}, false},

		// A type with no custom role for this kind, and the role of another
		// project's installation: the account is proj-1's and nobody else's.
		{"a custom role on a type the table carries none for", "google_secret_manager_secret_iam_member",
			map[string]any{"member": member, "role": "projects/proj-1/roles/fugaroLauncher"}, map[string]any{}, false},
		{"the launcher's role to a lookalike in another project", "google_project_iam_member",
			map[string]any{"member": "serviceAccount:" + OrchestratorAccountID + "@fp-2.iam.gserviceaccount.com", "role": "projects/proj-1/roles/fugaroLauncher", "project": "proj-1"}, map[string]any{}, false},
	} {
		ch := rc(tc.typ, tc.after, tc.unknown, "create")
		got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
		if (len(got) == 0) != tc.covered {
			t.Errorf("%s: NotCovered = %q, covered want %v", tc.what, got, tc.covered)
		}
	}

	// A role the plan leaves UNKNOWN, beside a custom role it creates in this
	// same module: refused here for the intake's and the credentials service's
	// reason one kind over, and this is the case that needs the refusal — the
	// table's project row names a custom role for this kind (fugaroLauncher), so
	// without the branch a plan that created ANY of the installation's custom
	// roles beside the grant would cover a project role nobody can name, which
	// is a project-wide reach (the intake's write role is create, delete and
	// version-add for every container of the project, and its name-prefix
	// condition is a thing this walk has no role to read). The account's rows
	// are literals for this reason.
	created := customRole(IntakeSecretsRole, "proj-1", []any{"secretmanager.secrets.create", "secretmanager.secrets.delete", "secretmanager.versions.add"})
	computed := rc("google_project_iam_member", map[string]any{"member": member, "project": "proj-1"}, map[string]any{"role": true}, "create")
	computedGot := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{created, computed}}, nil)
	if len(computedGot) == 0 || !strings.Contains(strings.Join(computedGot, "\n"), "a computed role to the resident orchestrator's account") {
		t.Errorf("a project role left unknown, beside the intake's own custom role created in the same plan: NotCovered = %q, want the computed role refused by name — a role the plan cannot name cannot be held to the design's section 4.3 table", computedGot)
	}

	// Listed on the review screen by mistake, it is still not a person: the
	// never-rule is about the list's GRANTS, so an operator who put the
	// account's address on var.launchers would get the account every role the
	// list is bound with, which is exactly what the module must not do.
	listed := Cover{Projects: testCover.Projects, Listed: []string{member}, Region: testCover.Region, Buckets: testCover.Buckets}
	for _, tc := range []struct {
		role    string
		covered bool
	}{
		{"projects/proj-1/roles/fugaroLauncher", true},
		{"roles/firebasedatabase.viewer", false},
		{"roles/datastore.viewer", false},
		{"roles/serviceusage.serviceUsageConsumer", false},
	} {
		ch := rc("google_project_iam_member", map[string]any{"member": member, "role": tc.role, "project": "proj-1"}, map[string]any{}, "create")
		if got := listed.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); (len(got) == 0) != tc.covered {
			t.Errorf("listed on the review screen: %s to the resident orchestrator: NotCovered = %q, covered want %v", tc.role, got, tc.covered)
		}
	}
}

// TestMobileAccountIsItsOwnPrincipalKind is the public mobile service's account
// (mobile plan task P5; D2782 as amended by F31, O8) held to the same shape the
// resident orchestrator's is: a kind of its own, two pinned cells, and every
// other role refused.
//
// The two cells are P5's acceptance as a property of the table, and the second
// of them is the reason the test exists at all: iam.serviceAccounts.signJwt is
// the widest grant of this tree, so the cell is opened to this kind by its ROLE
// alone — the row cannot see which account a grant is on — and mobileMinterGrant
// is what closes it again to the devices' signer. Every case below the two
// covered ones is a grant the module does not make and that a row alone would
// have admitted.
func TestMobileAccountIsItsOwnPrincipalKind(t *testing.T) {
	const member = "serviceAccount:" + MobileAccountID + "@proj-1.iam.gserviceaccount.com"
	const deviceSigner = DeviceSignerAccountID + "@fp-2.iam.gserviceaccount.com"
	const runsSigner = "fugaro-token-signer@fp-2.iam.gserviceaccount.com"
	if slices.Contains(FugaroAccounts, MobileAccountID) {
		t.Errorf("the public mobile service's account is one of FugaroAccounts, so every toAccounts row accepts it — on the one service anyone on the internet may call, holding signJwt on a signer account (P5, O8)")
	}
	if MobileAccountID == VoiceAccountID || MobileAccountID == DeviceSignerAccountID {
		t.Errorf("the mobile service's account and the signer it mints through are %q and %q: the runtime identity and the key it uses must be different accounts, or the reach that is meant to be one service's is the other's too", MobileAccountID, DeviceSignerAccountID)
	}

	want := []string{
		"google_project_iam_member|" + customKey + CredentialsDatabaseRole,
		"google_service_account_iam_member|" + customKey + "fugaroTokenMinter",
	}
	var got []string
	for typ, row := range grantRules {
		for key, w := range row {
			if w&toMobile != 0 {
				got = append(got, typ+"|"+key)
			}
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the cells carrying toMobile are %v, want exactly %v: the account's whole reach is the unchanged database role O1 approved and the signJwt on the devices' signer, and a cell beside them is a grant nobody priced (P5)", got, want)
	}

	for _, tc := range []struct {
		what    string
		typ     string
		after   map[string]any
		unknown map[string]any
		covered bool
	}{
		{"the database role on the Firebase project, as credentials.tf writes it", "google_project_iam_member",
			map[string]any{"member": member, "role": "projects/fp-2/roles/" + CredentialsDatabaseRole, "project": "fp-2"}, map[string]any{}, true},
		{"the signJwt on the devices' signer, as mobile_service.tf writes it", "google_service_account_iam_member",
			map[string]any{"member": member, "role": "projects/fp-2/roles/fugaroTokenMinter", "service_account_id": "projects/fp-2/serviceAccounts/" + deviceSigner}, map[string]any{}, true},
		// The grant the whole function is for: the RUNS' signer. It is the same
		// role on an account of the same project, and a plan that named it would
		// make the public mobile service the identity that mints every run's and
		// every launcher's token.
		{"the signJwt on the runs' own signer instead of the devices'", "google_service_account_iam_member",
			map[string]any{"member": member, "role": "projects/fp-2/roles/fugaroTokenMinter", "service_account_id": "projects/fp-2/serviceAccounts/" + runsSigner}, map[string]any{}, false},
		{"the signJwt on a job account", "google_service_account_iam_member",
			map[string]any{"member": member, "role": "projects/fp-2/roles/fugaroTokenMinter", "service_account_id": "projects/proj-1/serviceAccounts/fugaro-history@proj-1.iam.gserviceaccount.com"}, map[string]any{}, false},
		{"the signJwt on a device signer lookalike in a project that is not this run's", "google_service_account_iam_member",
			map[string]any{"member": member, "role": "projects/fp-2/roles/fugaroTokenMinter", "service_account_id": "projects/fp-9/serviceAccounts/" + DeviceSignerAccountID + "@fp-9.iam.gserviceaccount.com"}, map[string]any{}, false},
		{"the signJwt on an account the plan leaves unknown", "google_service_account_iam_member",
			map[string]any{"member": member, "role": "projects/fp-2/roles/fugaroTokenMinter"}, map[string]any{"service_account_id": true}, false},
		// The roles being a member of var.launchers would have granted, named by
		// hand: no toMobile cell carries them, which is what the row's kind is
		// for.
		{"the rules-bypassing read of every node of the instance", "google_project_iam_member",
			map[string]any{"member": member, "role": "roles/firebasedatabase.viewer", "project": "fp-2"}, map[string]any{}, false},
		{"the launcher's project role", "google_project_iam_member",
			map[string]any{"member": member, "role": "projects/proj-1/roles/fugaroLauncher", "project": "proj-1"}, map[string]any{}, false},
		{"the project-wide secret accessor the bundle's reader holds", "google_project_iam_member",
			map[string]any{"member": member, "role": "roles/secretmanager.secretAccessor", "project": "proj-1"}, map[string]any{}, false},
		// It is the INVOKER binding of its own service that is public, and the
		// member there is allUsers rather than this account: the bit admits
		// people, accounts and the classes that call a service, and this kind is
		// none of them.
		{"the invoker bit on the credentials service", "google_cloud_run_v2_service_iam_member",
			map[string]any{"member": member, "role": "roles/run.invoker", "project": "proj-1"}, map[string]any{}, false},
		{"the database role to a lookalike in another project", "google_project_iam_member",
			map[string]any{"member": "serviceAccount:" + MobileAccountID + "@fp-2.iam.gserviceaccount.com", "role": "projects/fp-2/roles/" + CredentialsDatabaseRole, "project": "fp-2"}, map[string]any{}, false},
	} {
		ch := rc(tc.typ, tc.after, tc.unknown, "create")
		gotReasons := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil)
		if (len(gotReasons) == 0) != tc.covered {
			t.Errorf("%s: NotCovered = %q, covered want %v", tc.what, gotReasons, tc.covered)
		}
	}

	// Listed on the review screen by mistake, it is still not a person: an
	// operator who put this account's address on var.launchers would give the
	// public service every grant the list carries, and the account's own kind
	// is precisely that no row but its two accepts it.
	listed := Cover{Projects: testCover.Projects, Listed: []string{member}, Region: testCover.Region, Buckets: testCover.Buckets}
	for _, role := range []string{"projects/proj-1/roles/fugaroLauncher", "roles/firebasedatabase.viewer", "roles/run.invoker"} {
		ch := rc("google_project_iam_member", map[string]any{"member": member, "role": role, "project": "proj-1"}, map[string]any{}, "create")
		if got := listed.NotCovered(&Plan{ResourceChanges: []ResourceChange{ch}}, nil); len(got) == 0 {
			t.Errorf("listed on the review screen: %s to the public mobile service's account: NotCovered = %q, want it refused — the account is never a member of var.launchers", role, got)
		}
	}
}

// A build's ID token comes from Cloud Build's metadata server only to an
// account holding iam.serviceAccounts.getOpenIdToken, so the build identity
// holds roles/iam.serviceAccountOpenIdTokenCreator ON ITSELF. The row opens by
// member and role; buildIdentityOpenIDGrant closes it to the account.
func TestBuildIdentityOpenIDTokenIsOnItselfAlone(t *testing.T) {
	const (
		role     = "roles/iam.serviceAccountOpenIdTokenCreator"
		member   = "serviceAccount:" + BuildIdentityAccountID + "@proj-1.iam.gserviceaccount.com"
		self     = "projects/proj-1/serviceAccounts/" + BuildIdentityAccountID + "@proj-1.iam.gserviceaccount.com"
		runner   = "projects/proj-1/serviceAccounts/fugaro-job-abc@proj-1.iam.gserviceaccount.com"
		creds    = "projects/proj-1/serviceAccounts/fugaro-credentials@proj-1.iam.gserviceaccount.com"
		otherPrj = "projects/fp-2/serviceAccounts/" + BuildIdentityAccountID + "@fp-2.iam.gserviceaccount.com"
	)
	for name, tc := range map[string]struct {
		change  ResourceChange
		covered bool
	}{
		"on itself":                               {grantOnAccount("google_service_account_iam_member", member, role, self), true},
		"on a job account":                        {grantOnAccount("google_service_account_iam_member", member, role, runner), false},
		"on the credentials service's account":    {grantOnAccount("google_service_account_iam_member", member, role, creds), false},
		"on another project's own":                {grantOnAccount("google_service_account_iam_member", member, role, otherPrj), false},
		"on no account named":                     {grant("google_service_account_iam_member", member, role), false},
		"a job account holds it on itself":        {grantOnAccount("google_service_account_iam_member", "serviceAccount:fugaro-job-abc@proj-1.iam.gserviceaccount.com", role, runner), false},
		"a person holds it on the build identity": {grantOnAccount("google_service_account_iam_member", "user:me@example.com", role, self), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := testCover.NotCovered(&Plan{ResourceChanges: []ResourceChange{tc.change}}, nil); (len(got) == 0) != tc.covered {
				t.Fatalf("NotCovered = %q, covered want %v", got, tc.covered)
			}
		})
	}
}
