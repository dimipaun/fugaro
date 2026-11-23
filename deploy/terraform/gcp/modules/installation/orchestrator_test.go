// The resident orchestrator's identity, held at the module's own level (mobile
// plan task P5.1b): section 4.3's table row for row, and the never-list's
// eleventh rule — the account is never joined to var.launchers.
//
// The tests read orchestrator.tf as text, the way state_read_test.go's and
// coordinator_service_test.go's do, and for the same reason: the subject is
// which resources exist and what each of them names, and a plan assertion only
// ever sees the inputs a test hands it. internal/infra's own terraform-tagged
// test is the other half — it plans the root and reads the resulting rows — so
// between them a grant that is added, removed or retargeted fails somewhere
// with the row it broke.
//
// The table below is the mobile orchestrator design's section 4.3 — the document
// internal/cli/docs_mobile_orchestrator_test.go pins, whose path this file does
// not spell (the rule over that folder lets only its docs_* pins name it) — and
// it is the whole of what the account may hold: the launcher's project role
// (its metadata permissions, rows 1 and 3 of the table in one custom role), the
// runs bucket's conditional write and its unconditioned read, the token-minter
// role on the budget signer, and the launch of the installation's jobs, which
// is the root's own resource (roots/installation/main.tf) because the jobs are,
// and — P5.1e — the pairing key's cloud copy: one container per installation and
// the accessor row that lets this account read it, which is section 6 rule 5's
// own exception rather than a row of the launcher's set.
package installation

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// orchestratorTF is the file that carries the account and its bindings.
const orchestratorTF = "orchestrator.tf"

// orchestratorMemberExpr is the account's address as every binding in the file
// writes it out. The header comment says why it is written rather than read off
// the account resource: a computed member reaches the plan unknown, and the
// cover reads a principal's kind off the plan's member string.
const orchestratorMemberExpr = `"serviceAccount:${var.orchestrator.account_id}@${var.project}.iam.gserviceaccount.com"`

// orchestratorResources is every resource that file declares, in the order the
// file declares them, and the test fails on any other: the account and its
// bindings are section 4.3's table, where a resource added is a grant the design
// does not have a row for and one removed is a row the account can no longer
// reach; and the service, its invoker grant and its tick are the design's
// section 4.1 (mobile plan task P5.1c), where a resource added beside them is a
// second way in that section 4.3's envelope was never priced against.
var orchestratorResources = []struct{ typ, name string }{
	{"google_service_account", "orchestrator"},
	{"google_secret_manager_secret", "pairing_key"},
	{"google_secret_manager_secret_iam_member", "orchestrator_pairing_key"},
	{"google_project_iam_member", "orchestrator_launcher"},
	{"google_storage_bucket_iam_member", "orchestrator_runs_launcher"},
	{"google_storage_bucket_iam_member", "orchestrator_runs_reader"},
	{"google_service_account_iam_member", "orchestrator_token_minter"},
	{"google_cloud_run_v2_service", "orchestrator"},
	{"google_cloud_run_v2_service_iam_member", "orchestrator_invoker"},
	// The account's way into the credentials service (F19, O2 of
	// docs/forge-labels): the label route and the gated merge mint are served
	// to this account's class alone, and the front end refuses a caller
	// without roles/run.invoker before the container runs.
	{"google_cloud_run_v2_service_iam_member", "orchestrator_credentials_invoker"},
	{"google_cloud_scheduler_job", "orchestrator_tick"},
	// The operators' token creation on the account itself, which the daemon on
	// an operator's machine needs to mint its ID token (the one use of
	// var.operators this file admits).
	{"google_service_account_iam_member", "fugaro_orchestrator_token_creator"},
}

// tfBlock returns the body of the resource block for typ and name, or "" when
// the file declares none.
func tfBlock(src, typ, name string) string {
	header := "resource \"" + typ + "\" \"" + name + "\""
	i := strings.Index(src, header)
	if i < 0 {
		return ""
	}
	rest := src[i+len(header):]
	if j := strings.Index(rest, "\nresource \""); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// TestOrchestratorModuleGrantsTheDesignsTable is the module's half of the
// table: the account exists, and the bindings beside it are the pairing key's
// container and its one reader (P5.1e — the exception section 6 rule 5 states,
// because the MAC is symmetric), the launcher's role, the bucket's write under
// the launchers' own condition, the bucket's read with no condition, and the
// token-minter role on the signer account — each of them naming exactly what
// section 4.3 names and nothing else.
//
// It fails on a resource added to the file (a grant with no row in the table),
// on one removed, on a role or a bucket retargeted, on a write that lost the
// launchers' condition or gained another, and on a read that gained one — which
// last pair is the one that matters most, because the read is the whole bucket
// by design (listing needs storage.objects.list, which no object-name condition
// can grant) and the write is three prefixes and never more.
func TestOrchestratorModuleGrantsTheDesignsTable(t *testing.T) {
	src := tfSource(t, orchestratorTF)

	var got []string
	for _, m := range regexp.MustCompile(`(?m)^resource "([^"]+)" "([^"]+)"`).FindAllStringSubmatch(src, -1) {
		got = append(got, m[1]+"."+m[2])
	}
	var want []string
	for _, r := range orchestratorResources {
		want = append(want, r.typ+"."+r.name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("%s declares %v, want exactly %v: the account holds section 4.3's table and a resource beside it is a grant the design has not priced", orchestratorTF, got, want)
	}

	account := tfBlock(src, "google_service_account", "orchestrator")
	if account == "" {
		t.Fatalf("%s declares no google_service_account.orchestrator: the install's resident orchestrator has no identity, and P5.1c's service has nothing to run as", orchestratorTF)
	}
	if !regexp.MustCompile(`(?m)^\s*account_id\s*=\s*var\.orchestrator\.account_id\s*$`).MatchString(account) {
		t.Errorf("%s: the account's ID is not var.orchestrator.account_id, which is the account `fugaro doctor` looks for and the one internal/infra/tf's cover reads a member of that project as:\n%s", orchestratorTF, account)
	}

	// Every binding names the account with the written-out expression, and none
	// of them reads it off the account resource: google_service_account
	// .orchestrator[0].member is computed, so a grant using it would reach the
	// plan as an unknown member — which internal/infra/tf's cover reads as an
	// ordinary account, the one kind the never-list's eleventh rule keeps this
	// account out of. Every row below carries the expression in `need` for that
	// reason, and the computed form in `absent`.
	for _, c := range []struct {
		what   string
		block  string
		need   []string
		absent []string
	}{
		{
			what:  "the pairing key's cloud copy: one container per installation, and never a logical secret of the project's set",
			block: tfBlock(src, "google_secret_manager_secret", "pairing_key"),
			need: []string{
				`secret_id = "fugaro-orch-pairing"`,
				`fugaro         = "managed"`,
				`fugaro_project = var.fugaro_project`,
				"prevent_destroy = true",
			},
			// fugaro_secret is the label `fugaro update`'s discovery reads to
			// decide which logical secret of the project's secret set a
			// container holds; this container is one version per pairing with
			// two writers and neither of them Terraform (credentials.tf's own
			// reason), so carrying it would plan the same secret ID twice.
			absent: []string{"fugaro_secret"},
		},
		{
			what:  "the pairing key's reader: the account above, on that one container",
			block: tfBlock(src, "google_secret_manager_secret_iam_member", "orchestrator_pairing_key"),
			need: []string{
				`role      = "roles/secretmanager.secretAccessor"`,
				`member    = ` + orchestratorMemberExpr,
			},
			// The project-wide accessor is refused by the cover's own rule for
			// every principal but the credentials service's account
			// (dynamicSecretsGrant), and a role beside the accessor is a reach
			// section 4.3's table has no row for.
			absent: []string{"orchestrator[0].member", "roles/secretmanager.secretVersionAdder", "roles/secretmanager.viewer", "roles/secretmanager.secrets.setIamPolicy"},
		},
		{
			what:  "the launcher's project role: rows 1 and 3 of the table, the metadata permissions",
			block: tfBlock(src, "google_project_iam_member", "orchestrator_launcher"),
			need: []string{
				`role    = "projects/${var.project}/roles/fugaroLauncher"`,
				`project = var.project`,
				`depends_on = [google_service_account.orchestrator, google_project_iam_custom_role.launcher]`,
				`member  = ` + orchestratorMemberExpr,
			},
			absent: []string{"orchestrator[0].member"},
		},
		{
			what:  "the runs bucket's write: objectUser under the launchers' own condition",
			block: tfBlock(src, "google_storage_bucket_iam_member", "orchestrator_runs_launcher"),
			need: []string{
				`bucket = google_storage_bucket.runs.name`,
				`role   = "roles/storage.objectUser"`,
				`member = ` + orchestratorMemberExpr,
				`title      = var.launcher_bucket_condition.title`,
				`expression = var.launcher_bucket_condition.expression`,
			},
			absent: []string{"orchestrator[0].member"},
		},
		{
			what:  "the runs bucket's read: objectViewer, unconditioned (D683)",
			block: tfBlock(src, "google_storage_bucket_iam_member", "orchestrator_runs_reader"),
			need: []string{
				`bucket = google_storage_bucket.runs.name`,
				`role   = "roles/storage.objectViewer"`,
				`member = ` + orchestratorMemberExpr,
			},
			absent: []string{"condition", "objectAdmin", "orchestrator[0].member"},
		},
		{
			what:  "the token-minter role on the budget signer, in the Firebase project",
			block: tfBlock(src, "google_service_account_iam_member", "orchestrator_token_minter"),
			need: []string{
				`service_account_id = "projects/${var.orchestrator.firebase_project}/serviceAccounts/${var.orchestrator.signer_account_id}@${var.orchestrator.firebase_project}.iam.gserviceaccount.com"`,
				`role               = "projects/${var.orchestrator.firebase_project}/roles/fugaroTokenMinter"`,
				`member             = ` + orchestratorMemberExpr,
			},
			// roles/iam.serviceAccountTokenCreator's holder mints ACCESS TOKENS
			// as the signer, which is a reach section 4.3's row is not: the row
			// is iam.serviceAccounts.signJwt and nothing beside it.
			absent: []string{"orchestrator[0].member", "serviceAccountTokenCreator", "roles/owner", "roles/editor"},
		},
	} {
		if c.block == "" {
			t.Errorf("%s: %s is missing from %s entirely", orchestratorTF, c.what, orchestratorTF)
			continue
		}
		for _, n := range c.need {
			if !strings.Contains(c.block, n) {
				t.Errorf("%s: %s does not name %q:\n%s", orchestratorTF, c.what, n, c.block)
			}
		}
		for _, a := range c.absent {
			if strings.Contains(c.block, a) {
				t.Errorf("%s: %s names %q, which section 4.3's row for it does not:\n%s", orchestratorTF, c.what, a, c.block)
			}
		}
	}

	// The write is the launchers' condition and never a copy of it: the
	// precondition is what holds the expression to the bytes
	// gcp.LauncherBucketCondition builds, as iam.tf's own does, so a plan that
	// widened the write area fails at apply rather than being applied.
	write := tfBlock(src, "google_storage_bucket_iam_member", "orchestrator_runs_launcher")
	if !strings.Contains(write, "precondition") || !strings.Contains(write, "fugaro-launchers-runs-tokens-issues") {
		t.Errorf("%s: the bucket write carries no precondition pinning the condition to runs/, tokens/ and issues/ of the runs bucket: the condition arrives as a variable, and a widened one would be applied as the launchers' own (D1727):\n%s", orchestratorTF, write)
	}
}

// TestOrchestratorIsNeverALauncher is the never-list's eleventh rule, and it is
// the one that costs the most to get wrong: the account is granted the
// launcher's GRANTS one binding at a time and is never a member of the
// launchers' LIST. Joining the list would give it three project-level bindings
// the FIREBASE root grants that list — roles/firebasedatabase.viewer (an IAM
// read that bypasses the Realtime Database rules and reaches every mailbox of
// the installation), roles/datastore.viewer and
// roles/serviceusage.serviceUsageConsumer — on top of the signJwt reach it
// already has (section 4.5 E4).
//
// This is a text test and it can only be one: var.launchers is what the FIREBASE
// root reads, and this module's plan never sees that root's bindings. What it
// fails on is the shape the mistake takes here — the account's address reaching
// var.launchers, var.operators or var.viewers from inside this module, or the
// wide roles being named in this file at all. internal/infra's
// TestOrchestratorGrantsAreTheDesignsTableRows is the plan-level half: it reads
// every row of the plan that names the account and fails on one beside the
// table's.
func TestOrchestratorIsNeverALauncher(t *testing.T) {
	src := tfSource(t, orchestratorTF)
	// The operators' token creation on the account is the one admitted use of
	// var.operators: it grants operators the right to act as the account and
	// never puts the account on a people list.
	const operatorsFor = "for_each = local.orchestrator_on ? toset(var.operators) : toset([])"
	if strings.Count(src, operatorsFor) != 1 {
		t.Errorf("%s does not carry the operators' token-creator for_each exactly once", orchestratorTF)
	}
	src = strings.Replace(src, operatorsFor, "", 1)
	for _, never := range []string{
		"var.launchers", "var.operators", "var.viewers", "var.launcher_accounts",
		"roles/firebasedatabase.viewer", "roles/datastore.viewer", "roles/serviceusage.serviceUsageConsumer",
		"roles/owner", "roles/editor",
	} {
		if strings.Contains(src, never) {
			t.Errorf("%s names %s: the account is granted section 4.3's rows one binding at a time and is NEVER joined to the launchers' list, which the Firebase root binds a rules-bypassing read of every mailbox with (design section 4.3, section 6 rule 11)", orchestratorTF, never)
		}
	}

	// The account's address is bound by this file and nowhere else in the
	// module: the table is one file, and a second file's grant is one no reader
	// of this table would look for. Two other files name the account, and both
	// name its EMAIL as a VALUE rather than binding anything with it:
	// outputs.tf publishes the identity, and credentials.tf renders it into the
	// credentials service's own environment (FUGARO_ORCHESTRATOR_ACCOUNTS,
	// F19/O2) — which is a value the service verifies a caller's claim
	// against, not a grant made with the address. The member expression above
	// is refused EVERYWHERE but this file, so a grant written in a second file
	// still fails, whichever spelling it reaches for.
	for name, text := range tfFiles(t) {
		if name == orchestratorTF {
			continue
		}
		if strings.Contains(text, orchestratorMemberExpr) {
			t.Errorf("%s names the orchestrator's address as a member: section 4.3's table is orchestrator.tf's alone, and a grant written in a second file is one the table's own test cannot see", name)
		}
		if !strings.Contains(text, "google_service_account.orchestrator") {
			continue
		}
		if name != "outputs.tf" && name != "credentials.tf" {
			t.Errorf("%s names the orchestrator's account resource: the table is orchestrator.tf's, and outputs.tf (publishing the identity) and credentials.tf (rendering it into the service's environment) are the two other files that may name it as a value rather than to bind anything with it", name)
		}
		for _, m := range regexp.MustCompile(`google_service_account\.orchestrator\[[^]]*\]\.([A-Za-z_]+)`).FindAllStringSubmatch(text, -1) {
			if m[1] != "email" {
				t.Errorf("%s reads the orchestrator account's .%s: the account's email is the identity, and every other attribute — .member above all — is what a BINDING is built from, which belongs in %s alone", name, m[1], orchestratorTF)
			}
		}
		if strings.Contains(text, "google_service_account.orchestrator[") && !strings.Contains(text, "google_service_account.orchestrator[0].email") {
			t.Errorf("%s names the orchestrator's account resource and not its email: the one thing these two files may read off it is the address the service matches a caller's claim against", name)
		}
	}
}

// TestOrchestratorServiceIsTheDesignsShape is the module's half of section 4.1
// (mobile plan task P5.1c): one Cloud Run service, scale-to-zero, request-based
// billing, woken by a Cloud Scheduler tick. It reads the two resources the way
// this file's tests read the table, and it is the half the plan-level test
// cannot be: internal/infra's terraform-tagged test reads the rows a plan
// produces, and a plan cannot say whether a value came from a variable or from a
// literal somebody typed into terraform.tfvars.json.
func TestOrchestratorServiceIsTheDesignsShape(t *testing.T) {
	src := tfSource(t, orchestratorTF)

	svc := tfBlock(src, "google_cloud_run_v2_service", "orchestrator")
	if svc == "" {
		t.Fatalf("%s declares no google_cloud_run_v2_service.orchestrator: the resident orchestrator has no service and the tick wakes nothing (design section 4.1)", orchestratorTF)
	}
	for _, need := range []string{
		// Scale-to-zero, twice: the service's own scaling block is what a
		// read of the live revision reports as minScale and check 660 is what
		// reads it, and the template's copy is what the revision is created
		// with. min-instances = 1 is the shape the design priced and did not
		// take.
		"min_instance_count = 0",
		`scaling_mode       = "AUTOMATIC"`,
		// The fence: an IAM-required service. INGRESS_TRAFFIC_ALL with IAM on
		// is what "no public ingress" means here (intake.tf's rule); the
		// invoker is the scheduler account alone, in the grant below.
		`ingress = "INGRESS_TRAFFIC_ALL"`,
		"invoker_iam_disabled = false",
		`name     = var.orchestrator.service`,
		`location = var.region`,
		`image = var.orchestrator.image`,
		`args  = ["orchestrator", "serve"]`,
		`service_account = google_service_account.orchestrator[0].email`,
		`type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"`,
		"ignore_changes = [client, client_version]",
		// The six names the process reads, and the six values: the three the
		// NODE is (P5.1c — the installation's own name, which is the node's
		// id; its Firebase project; and the database it reads the node out
		// of) and the three the LAUNCH is (P5.1d — the GCP project the
		// installation's jobs are in, the region they were deployed to, and
		// the runs bucket's own URL, which is the three facts a run's own
		// container carries: internal/cli reads them under these same names
		// inside a run, and internal/infra's BucketEnv, GCPProjectEnv and
		// RegionEnv are the spellings). None of them is a secret and none is
		// a secret ID (section 6 rules 4 and 5), and the two lists are held to
		// internal/cli's own reads in both directions by
		// internal/cli's TestOrchestratorServiceEnvIsTheDeploymentsOwn and by
		// internal/infra/tf's orchestratorServiceEnv allow-list.
		`name  = "FUGARO_PROJECT"`, `value = var.fugaro_project`,
		`name  = "FUGARO_FIREBASE_PROJECT"`, `value = var.orchestrator.firebase_project`,
		`name  = "FUGARO_RTDB_URL"`, `value = var.orchestrator.rtdb_url`,
		`name  = "FUGARO_GCP_PROJECT"`, `value = var.project`,
		`name  = "FUGARO_REGION"`, `value = var.region`,
		`name  = "FUGARO_BUCKET"`, `value = "gs://${var.runs_bucket}"`,
	} {
		if !strings.Contains(svc, need) {
			t.Errorf("%s: the orchestrator's service does not carry %q:\n%s", orchestratorTF, need, svc)
		}
	}
	// The account is the plan's own and not a literal, so a plan reads it back
	// as the resource this module creates (internal/infra/tf's
	// orchestratorAccount).
	if !regexp.MustCompile(`(?m)^\s*count = local\.orchestrator_on \? 1 : 0\s*$`).MatchString(svc) {
		t.Errorf("%s: the service is not counted on local.orchestrator_on, so an installation that has not opted in acquires it:\n%s", orchestratorTF, svc)
	}
	for _, never := range []string{"value_source", "secret", "min_instance_count = 1"} {
		if strings.Contains(svc, never) {
			t.Errorf("%s: the orchestrator's service names %q, which section 4.1 and section 6 rules 4 and 5 do not allow here:\n%s", orchestratorTF, never, svc)
		}
	}

	// The invoker: the installation's own scheduler account, and nobody else.
	// Cloud Run's invoker is per service and there is nothing narrower (F36),
	// so this grant IS the caller class: no person, and no other account of
	// the installation, may wake the service.
	inv := tfBlock(src, "google_cloud_run_v2_service_iam_member", "orchestrator_invoker")
	for _, need := range []string{
		`role     = "roles/run.invoker"`,
		`name     = var.orchestrator.service`,
		`member   = google_service_account.scheduler.member`,
	} {
		if !strings.Contains(inv, need) {
			t.Errorf("%s: the service's invoker grant does not carry %q:\n%s", orchestratorTF, need, inv)
		}
	}
	// The member is the scheduler account's own attribute, which is the shape
	// the module's four other ticks give it (history.tf, coordinator.tf,
	// reports.tf, catalogcheck.tf): the grant names the account this module
	// makes, so the address the tick mints its token for and the one the front
	// end admits are one name. internal/infra/tf's cover reads a principal's
	// KIND off the plan member, and it reads an unknown one as an account of
	// this run exactly when the module creates a service account with it
	// (checkGrant's createsSA arm) — which a literal written from the variable
	// here would bypass, spelling an address the module already holds as a
	// resource.
	if strings.Contains(inv, `member   = "serviceAccount:`) {
		t.Errorf("%s: the invoker grant writes the member out as a literal rather than naming the scheduler account this module creates:\n%s", orchestratorTF, inv)
	}
	// And the orchestrator's own address is nowhere in it: this grant is to the
	// scheduler, so it is not a row of section 4.3's table.
	if strings.Contains(inv, "var.orchestrator.account_id") {
		t.Errorf("%s: the invoker grant names the orchestrator's own account: it is a grant TO the installation's scheduler, and the account's table is the five rows above:\n%s", orchestratorTF, inv)
	}

	// The tick: every minute (Cloud Scheduler's floor, and the cadence Q4's
	// answer of 2026-10-19 fixed), UTC, no retry, POST to the service's own URL
	// on the process's only route, with an OIDC ID token whose audience is that
	// same URL.
	tick := tfBlock(src, "google_cloud_scheduler_job", "orchestrator_tick")
	if tick == "" {
		t.Fatalf("%s declares no google_cloud_scheduler_job.orchestrator_tick: nothing wakes the service and the phone's worst-case wait is forever", orchestratorTF)
	}
	for _, need := range []string{
		`region    = var.orchestrator.scheduler_region`,
		`name      = var.orchestrator.scheduler_job`,
		`schedule  = "* * * * *"`,
		`time_zone = "Etc/UTC"`,
		"retry_count = 0",
		`http_method = "POST"`,
		`uri         = "${local.orchestrator_url}${local.orchestrator_tick_path}"`,
		`service_account_email = google_service_account.scheduler.email`,
		`audience              = local.orchestrator_url`,
	} {
		if !strings.Contains(tick, need) {
			t.Errorf("%s: the tick does not carry %q:\n%s", orchestratorTF, need, tick)
		}
	}
	if !strings.Contains(src, `orchestrator_url = local.orchestrator_on ? "https://${var.orchestrator.service}-${data.google_project.this[0].number}.${var.region}.run.app" : ""`) {
		t.Errorf("%s: the tick's URL is not the service's deterministic <service>-<project number>.<region>.run.app: the `.uri` attribute is the a.run.app form, which internal/infra/tf's cover cannot tie to this project and refuses", orchestratorTF)
	}
	// The token is an ID token and not an access token: Cloud Run's front end
	// admits a run.app caller by the ID token whose audience is the service,
	// and every other tick in this module carries an oauth_token because what
	// it calls is the run API.
	for _, never := range []string{"oauth_token", "scope"} {
		if strings.Contains(tick, never) {
			t.Errorf("%s: the tick carries %q: it calls a run.app URL, where Cloud Run requires an OIDC ID token — an OAuth access token is refused before the container runs:\n%s", orchestratorTF, never, tick)
		}
	}
	// The route `/tick` is the process's only one, and it is the local the URI
	// is composed from: internal/infra/tf's cover holds the tick to that same
	// path, and the two spellings are one string because this file pins it.
	if !strings.Contains(src, `orchestrator_tick_path = "/tick"`) {
		t.Errorf("%s: the tick's route is not the local `/tick` — internal/cli/orchestrator_serve.go's only route, and the path internal/infra/tf's cover admits a tick on", orchestratorTF)
	}
}

// TestOrchestratorLeavesAPlanUntouchedWhenItIsOff: the switch, in the file that
// carries the account. null creates nothing — every resource here is counted on
// the one local, the local is the variable's own null check, and the variable
// defaults to null — so the plan of an installation that has not opted in is
// the plan it had before this file existed, which is what makes the update that
// carries it a no-op for every installation that never asked for a resident
// orchestrator (docs/design/update-in-flight.md's row for the installation
// step). The plan-level half is internal/infra's
// TestOrchestratorTurnedOffPlansAsTheGoldenDoes.
func TestOrchestratorLeavesAPlanUntouchedWhenItIsOff(t *testing.T) {
	src := tfSource(t, orchestratorTF)
	if !strings.Contains(src, "orchestrator_on = var.orchestrator != null") {
		t.Errorf("%s: the gate is not the variable's own null check: every resource here is counted on one local, and a second way to turn the file on would leave a resource behind (credentials.tf's shape)", orchestratorTF)
	}
	if n := strings.Count(src, `resource "`); n != len(orchestratorResources) {
		t.Fatalf("%s declares %d resources and %d are expected: the count below cannot be checked against a list that is not the file's", orchestratorTF, n, len(orchestratorResources))
	}
	// The two resources that need a second switch, and the whole of why for each.
	// The credentials-service invoker grant binds something on a resource
	// credentials.tf creates, so an installation whose credentials service is
	// off has no service to bind anything on; it is the same shape every grant of
	// that service in iam.tf and build_identity.tf has. The token-minter row is
	// on people_minter as well (LM12 of docs/launcher-does-not-mint), the switch
	// the Firebase root gates the people's row with, which the plan that moves
	// the grant to the credentials service turns off — see
	// TestOrchestratorMinterIsBehindTheSwitch below. Each local holding its pair
	// still turns the file off whole when the orchestrator is off, so the no-op
	// this test is about is unchanged.
	twoSwitches := map[string]string{
		"google_cloud_run_v2_service_iam_member.orchestrator_credentials_invoker": "count = local.orchestrator_credentials_on ? 1 : 0",
		"google_service_account_iam_member.orchestrator_token_minter":             "count = local.orchestrator_on && var.people_minter ? 1 : 0",
		// The operators' grant is a for_each over the operators, off with the
		// orchestrator by an empty set.
		"google_service_account_iam_member.fugaro_orchestrator_token_creator": "for_each = local.orchestrator_on ? toset(var.operators) : toset([])",
	}
	for _, r := range orchestratorResources {
		block := tfBlock(src, r.typ, r.name)
		want := "count = local.orchestrator_on ? 1 : 0"
		if c, ok := twoSwitches[r.typ+"."+r.name]; ok {
			want = c
		}
		if !strings.Contains(block, want) {
			t.Errorf("%s: %s.%s does not carry %q, so an installation that has not opted in acquires it:\n%s", orchestratorTF, r.typ, r.name, want, block)
		}
	}

	vars := tfSource(t, "variables.tf")
	block := tfVariable(t, vars, "orchestrator")
	if !regexp.MustCompile(`(?m)^\s*default = null\s*$`).MatchString(block) {
		t.Errorf("variables.tf: var.orchestrator does not default to null: the switch is OFF for every installation that has not opted in, and one that did not ask must plan exactly what it planned before this task (design section 4.3; the brief's no-op rule for `fugaro update`):\n%s", block)
	}
}

// TestOrchestratorMinterIsBehindTheSwitch is the red step of LM12 in
// docs/launcher-does-not-mint, and it is about the ONE row in this file that is
// a minter: the resident orchestrator's fugaroTokenMinter grant on the runs'
// signer. That row is what the launcher-does-not-mint plan moves to the
// credentials service — after the move the service mints a run's identity and
// nobody else does — so it is planned behind people_minter, the switch the
// FIREBASE root gates the people's row with (LM11), under that one name in both
// roots so that the update which makes the move applies both roots with it off
// and both rows leave in the one plan (LM14, R8).
//
// What it fails on: the row counted on var.orchestrator alone (the state this
// task's red step starts from — the grant would survive the move and every
// launcher could still mint a run's identity as the signer, which is the whole
// of what D3121 ends), a switch that defaults to false (the update carrying
// this change would plan the row away for every installation that has not
// moved, halting its minting), and a second row in this file on the switch or
// on the minter role — the plan grants a second minter row to the credentials
// service's account and to nothing else.
func TestOrchestratorMinterIsBehindTheSwitch(t *testing.T) {
	src := tfSource(t, orchestratorTF)

	row := tfBlock(src, "google_service_account_iam_member", "orchestrator_token_minter")
	if row == "" {
		t.Fatalf("%s declares no google_service_account_iam_member.orchestrator_token_minter: this test is about the resident orchestrator's one minter row, and the file it reads must be the file that carries it", orchestratorTF)
	}
	want := "count = local.orchestrator_on && var.people_minter ? 1 : 0"
	if !strings.Contains(row, want) {
		t.Errorf("%s: the token-minter row does not carry %q, so it is planned whatever the installation's grant move says (LM12, D3121): the row is the reach the credentials service takes over, and an installation that has moved must plan it away while keeping the issuer's own row (R8):\n%s", orchestratorTF, want, row)
	}

	// One row and one row only: the switch is not a second minter row's, and the
	// minter role is named once in this file. The comments are already stripped,
	// so these count the file's own expressions.
	if n := strings.Count(src, "var.people_minter"); n != 1 {
		t.Errorf("%s names var.people_minter %d times, want exactly 1: the switch gates the orchestrator's one minter row in this module and nothing else — the people's row it shares the name with is the FIREBASE root's (LM11), and a second row here would be a grant neither LM11 nor LM12 lists", orchestratorTF, n)
	}
	if n := strings.Count(src, "fugaroTokenMinter"); n != 1 {
		t.Errorf("%s names the fugaroTokenMinter role %d times, want exactly 1: the credentials service's account is the only principal a second minter row is meant for and it is not this module's (LM12), so a second row here is a grant the plan does not have", orchestratorTF, n)
	}

	vars := tfSource(t, "variables.tf")
	block := tfVariable(t, vars, "people_minter")
	if !regexp.MustCompile(`(?m)^\s*type\s*=\s*bool\s*$`).MatchString(block) {
		t.Errorf("variables.tf: var.people_minter is not a bool, and the Firebase root reads the same switch under this name:\n%s", block)
	}
	if !regexp.MustCompile(`(?m)^\s*default\s*=\s*true\s*$`).MatchString(block) {
		t.Errorf("variables.tf: var.people_minter does not default to true: ON by default is the whole of what makes the update carrying this change a plan with nothing new in it, and OFF would take the row away from every installation whose grant has not moved (LM12, R8):\n%s", block)
	}
	if !regexp.MustCompile(`(?m)^\s*nullable\s*=\s*false\s*$`).MatchString(block) {
		t.Errorf("variables.tf: var.people_minter is nullable, so a null would reach the count expression and fail it at plan time rather than meaning one of the switch's two states:\n%s", block)
	}
}
