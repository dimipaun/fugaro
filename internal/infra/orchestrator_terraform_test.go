//go:build terraform

package infra

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// The resident orchestrator's identity, at the plan level (mobile plan task
// P5.1b). The module's own text test (deploy/terraform/gcp/modules/installation)
// reads which resources exist and what each of them names; these tests read the
// plan `fugaro update` applies, which is the other half of the same claim:
//
//   - turned OFF — which is what every installation that has not opted in gets,
//     the variable's default — the plan is the one the committed golden
//     installation.plan.json already holds, so the update carrying this task is
//     a no-op for it;
//   - turned ON, the plan grows by section 4.3's table and by nothing else, the
//     account is granted nothing under a role the table does not have (the
//     never-list's eleventh rule, held here as the plan's own fact rather than
//     as a comment), and the run's one confirmation does not refuse the
//     installation for having asked for it. "Nothing else" is the row set the
//     test builds below, and its one addition since the table was written is
//     J6's signing grant on the consumer key — the cost of §4.2's "it signs its
//     replies through J6's KMS signer", priced with the task.
//
// Both plan the same fixture the golden plan is a plan of
// (internal/infra/testdata/installation-budget-job.tfvars.json), so "before
// this task" is a real plan of this root rather than a tree a test edited: the
// opt-in is a key added to those same tfvars, which is exactly how an operator
// who has no CLI flag for it yet would turn the account on.
func goldenInstallationSpec(t *testing.T) InstallationSpec {
	t.Helper()
	const name = "installation-budget-job.tfvars.json"
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var spec InstallationSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	// The fixture is an InstallationVars document, and this test plans it by
	// decoding it back into the spec and encoding that again: the round trip
	// must be byte for byte what the file holds, or the plan below is a plan of
	// a slightly different installation than the golden it is compared with.
	again, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, again) {
		t.Fatalf("%s is not this spec's own tfvars: this test plans the fixture the golden plan was made from, and a field the spec loses in the round trip would make the comparison meaningless", name)
	}
	return spec
}

// goldenAddresses is the resource_changes addresses of the committed golden
// plan of the installation root, in the shape these tests compare with: it was
// made before this task, so a row it does not carry is a row nothing may add to
// an installation that has not opted in.
func goldenAddresses(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("tf", "testdata", "golden", name+".plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ResourceChanges []struct {
			Address string `json:"address"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(doc.ResourceChanges))
	for _, rc := range doc.ResourceChanges {
		out = append(out, rc.Address)
	}
	sort.Strings(out)
	return out
}

// addresses is a plan's own addresses, sorted the way goldenAddresses' are.
func addresses(plan map[string]plannedResource) []string {
	out := make([]string, 0, len(plan))
	for a := range plan {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// orchestratorBlock is the installation root's var.orchestrator as an operator's
// own tfvars would carry it: the account and the two names of its token-minter
// row (P5.1b), and the service, its released pin, the database its process reads
// this installation's node out of, and the tick's two names (P5.1c). Every test
// that plans an installation with a resident orchestrator uses this one block,
// so "the opt-in" is one shape and a test cannot pass by opting into something
// narrower than another test reads.
//
// The account, the service and the tick are read off the constants the WRITER
// composes the block with (OrchestratorAccountID, OrchestratorServiceName,
// OrchestratorSchedulerJob), so these plans are also what holds the writer's
// names to the module's: a writer that spelled another account or another tick
// would plan a second service beside the one the fixture creates.
//
// The image is this run's fixture registry and the recorded owner, in the
// released pin's closed shape — `us-east5-docker.pkg.dev/proj-1234/
// fugaro-ghcr/dimipaun/fugaro-<kind>:<version>@sha256:<64 hex>` — which is what
// internal/infra/tf's cover holds a service's image to (credentialsImageOK).
// An image the cover refuses would make every plan below ask its own
// confirmation, and the point of these tests is the rows the opt-in adds.
// orchestratorImageOwner is the GitHub owner the fixture installation's base
// images come from, which is the owner a pin through the pull-through
// repository must name (internal/cli's baseOwner, cover.go's pinOK — the same
// "dimipaun" the release pins and the golden tfvars spell).
const orchestratorImageOwner = "dimipaun"

func orchestratorBlock(t *testing.T, spec InstallationSpec, fp string) map[string]any {
	t.Helper()
	return map[string]any{
		"account_id":        OrchestratorAccountID,
		"firebase_project":  fp,
		"signer_account_id": SignerAccountID,
		"service":           OrchestratorServiceName,
		"image":             "us-east5-docker.pkg.dev/" + spec.Project + "/fugaro-ghcr/" + orchestratorImageOwner + "/fugaro-base:1.2.3@sha256:" + strings.Repeat("ab", 32),
		"rtdb_url":          "https://" + fp + "-default-rtdb.firebaseio.com",
		"scheduler_job":     OrchestratorSchedulerJob,
		"scheduler_region":  spec.Region,
	}
}

// TestOrchestratorTurnedOffPlansAsTheGoldenDoes is the update's own promise:
// the installation root's plan for an installation that passes no orchestrator
// block is the plan the committed golden holds, address for address. The golden
// was made before this task existed, so a row appearing in the plan here is a
// resource an installation that never asked for a resident orchestrator
// acquires — which is exactly what the switch being off is supposed to make
// impossible (docs/design/update-in-flight.md's row for the installation step,
// and the module's own switch test).
func TestOrchestratorTurnedOffPlansAsTheGoldenDoes(t *testing.T) {
	spec := goldenInstallationSpec(t)
	_, rows := installationPlan(t, spec)

	golden := goldenAddresses(t, "installation")
	if got := addresses(rows); !slices.Equal(got, golden) {
		t.Errorf("the plan of an installation with no orchestrator block is not the golden plan:\n only in the plan: %v\n only in the golden: %v", missing(got, golden), missing(golden, got))
	}
	for _, rc := range rows {
		if strings.Contains(rc.Address, "orchestrator") || strings.Contains(rc.json(), OrchestratorAccountID) {
			t.Errorf("%s is in the plan of an installation that never opted in: var.orchestrator is null by default, and an installation that did not ask for a resident orchestrator must plan exactly what it planned before the account existed", rc.Address)
		}
	}
}

// TestOrchestratorGrantsAreTheDesignsTableRows is section 4.3 held as the plan:
// the rows the opt-in adds are the account, the pairing key's container and its
// one reader (P5.1e — the copy section 6 rule 5 states as the exception a
// symmetric tag forces), the launcher's project role, the runs bucket's
// conditioned write and its unconditioned read, the token-minter role on the
// signer, the database's one-permission read (P5.1c's finding A: without it the
// tick answers 503 with the store's own "401: Unauthorized request." and no pull
// request is ever labelled), and the job runner's role on each of the
// installation's own jobs. The plan does not move in any other way.
//
// Two of its claims are the ones a comment cannot make. The first is that the
// member and the role the root spells out are the ones the same plan creates:
// the root writes `serviceAccount:<account_id>@<project>.iam.gserviceaccount.com`
// and `projects/<project>/roles/<role_id>` as literals, because a reference to
// the created resource or role reaches the plan as UNKNOWN, and an unknown
// member is a principal internal/infra/tf's cover cannot tell from an ordinary
// job account — the kind the design's eleventh never-rule exists to keep this
// one out of. The second is the never-rule itself at plan level: the account
// appears in the plan's rows and nowhere else, so no binding anywhere grants it
// anything beside the table.
func TestOrchestratorGrantsAreTheDesignsTableRows(t *testing.T) {
	spec := goldenInstallationSpec(t)
	if spec.History == nil || spec.History.FirebaseProject == "" || len(spec.ProjectJobs) == 0 {
		t.Fatalf("the golden fixture has changed shape: %d project jobs, history %+v — this test reads the Firebase project and the job set out of it", len(spec.ProjectJobs), spec.History)
	}
	fp := spec.History.FirebaseProject
	signer := SignerAccountID + "@" + fp + ".iam.gserviceaccount.com"
	member := "serviceAccount:" + OrchestratorAccountID + "@" + spec.Project + ".iam.gserviceaccount.com"

	_, off := installationPlan(t, spec)
	plan, on := installationPlanWith(t, spec, map[string]any{"orchestrator": orchestratorBlock(t, spec, fp)})

	const (
		accountAddr = "module.installation.google_service_account.orchestrator[0]"
		secretAddr  = "module.installation.google_secret_manager_secret.pairing_key[0]"
		accessAddr  = "module.installation.google_secret_manager_secret_iam_member.orchestrator_pairing_key[0]"
		launcherRow = "module.installation.google_project_iam_member.orchestrator_launcher[0]"
		writeRow    = "module.installation.google_storage_bucket_iam_member.orchestrator_runs_launcher[0]"
		readRow     = "module.installation.google_storage_bucket_iam_member.orchestrator_runs_reader[0]"
		minterRow   = "module.installation.google_service_account_iam_member.orchestrator_token_minter[0]"
		// The account's read of this installation's database (P5.1c's finding
		// A). It is a row of the opt-in and of section 4.3's table both: the
		// tick reads orchestrator/<id>/{state,lease} as this account, and a
		// service-account bearer bypasses the Realtime Database rules, so the
		// read is an IAM permission and its absence is the 503 every tick
		// answered. The role is state_read.tf's one-permission custom role —
		// the tree's ONE role for that node, which the front door and the hop
		// hold too — and it is declared in the Firebase project, where the
		// database is, not in the installation's.
		stateReadRow  = "module.installation.google_project_iam_member.orchestrator_state_read[0]"
		stateReadRole = "module.installation.google_project_iam_custom_role.state_read[0]"
		// The service and its tick (mobile plan task P5.1c; the design's
		// section 4.1). They are rows of the opt-in and not of section 4.3's
		// table: they hold no grant for the account, and the two grants among
		// them are made TO the installation's scheduler account — the one
		// principal outside this module that may reach the service.
		serviceAddr = "module.installation.google_cloud_run_v2_service.orchestrator[0]"
		invokerAddr = "module.installation.google_cloud_run_v2_service_iam_member.orchestrator_invoker[0]"
		tickAddr    = "module.installation.google_cloud_scheduler_job.orchestrator_tick[0]"
		// The consumer's signing grant (mobile-identity-pairing plan task J6;
		// O17), which is the one row of the opt-in that is not a row of section
		// 4.3's original table: the account signs its mailbox replies and
		// heartbeats with the installation's consumer key instead of a MAC, so
		// the row is what §4.2's "it signs its replies through J6's KMS signer"
		// costs. It is here rather than beside the key because section 4.3's
		// table is orchestrator.tf's alone, and the key, its ring and the role
		// it grants are mailbox_key.tf's
		// (internal/infra/mailbox_key_test.go holds that row's key, role and
		// member; this test holds that the account gains no OTHER row for it).
		signAddr = "module.installation.google_kms_crypto_key_iam_member.orchestrator_mailbox_signer[0]"
	)
	want := []string{accountAddr, secretAddr, accessAddr, launcherRow, writeRow, readRow, minterRow, stateReadRow, serviceAddr, invokerAddr, tickAddr, signAddr}
	for key := range spec.ProjectJobs {
		want = append(want, `google_cloud_run_v2_job_iam_member.orchestrator_runner["`+key+`"]`)
	}
	sort.Strings(want)

	// The plan's own rows: the account, the pairing key's container and its one
	// reader, one binding per project job, and nothing beside them. A row
	// missing is a launch, a mint or a copy of the pairing key the resident
	// orchestrator cannot make; a row added is a grant with no row in the
	// design's table.
	if extra := missing(addresses(on), append(addresses(off), want...)); len(extra) > 0 {
		t.Errorf("the opt-in adds %v, which section 4.3's table does not have: the table is the whole of what this account may hold", extra)
	}
	if gone := missing(addresses(on), want); len(gone) > 0 {
		t.Errorf("the opt-in plans no %v: those are rows of section 4.3's table, and each of them is a launch, a `fugaro ls`, a mailbox write or a mint the account cannot make without it", gone)
	}
	for addr, before := range off {
		after, ok := on[addr]
		if !ok {
			t.Errorf("%s is in the plan of an installation that has not opted in and not in the one that has: the switch adds the resident orchestrator and changes nothing else", addr)
			continue
		}
		if before.json() != after.json() {
			t.Errorf("%s changed when the orchestrator was turned on:\n before: %s\n after:  %s", addr, before.json(), after.json())
		}
	}

	account := on[accountAddr]
	if account.After["account_id"] != OrchestratorAccountID {
		t.Errorf("%s: account_id is %v, want %s — the ID the installation module's variable declares, the cover reads and `fugaro doctor` looks for", accountAddr, account.After["account_id"], OrchestratorAccountID)
	}
	// The literals the root spells out name the resources this same plan
	// creates: the account's address and the custom roles' IDs. Written from the
	// tfvars instead of read off them because those attributes are computed, and
	// this is what keeps the two spellings one name.
	for _, c := range []struct{ addr, key, want string }{
		{accountAddr, "member", "serviceAccount:" + str(account.After["account_id"]) + "@" + spec.Project + ".iam.gserviceaccount.com"},
		{launcherRow, "member", member},
		{launcherRow, "role", "projects/" + spec.Project + "/roles/" + customRoleID(t, on, "launcher")},
		{minterRow, "role", "projects/" + fp + "/roles/fugaroTokenMinter"},
		{accessAddr, "member", member},
		{accessAddr, "role", "roles/secretmanager.secretAccessor"},
		{accessAddr, "secret_id", OrchestratorPairingSecretID},
		{secretAddr, "secret_id", OrchestratorPairingSecretID},
	} {
		if got := str(on[c.addr].After[c.key]); got != c.want {
			t.Errorf("%s: %s is %q, want %q — the root writes the name out rather than reading it off the resource it names, so the two are one name only if they are equal here", c.addr, c.key, got, c.want)
		}
	}
	// The container's ownership marks, and the label it must NOT carry: a
	// fugaro_secret label would make `fugaro update`'s discovery read this
	// container as one of the project's logical secrets and plan the same
	// secret ID twice (credentials.tf's own reason).
	labels, _ := on[secretAddr].After["labels"].(map[string]any)
	if str(labels["fugaro"]) != "managed" || str(labels["fugaro_project"]) != spec.FugaroProject {
		t.Errorf("%s: labels are %v, want fugaro=managed and fugaro_project=%s — the two marks that make the container recognizably this installation's", secretAddr, labels, spec.FugaroProject)
	}
	if _, ok := labels["fugaro_secret"]; ok {
		t.Errorf("%s carries fugaro_secret: this container is one version per pairing with two writers and neither of them Terraform, so discovery must not read it as one of the project's logical secrets", secretAddr)
	}
	for key := range spec.ProjectJobs {
		addr := `google_cloud_run_v2_job_iam_member.orchestrator_runner["` + key + `"]`
		rc, ok := on[addr]
		if !ok {
			continue
		}
		if got, want := str(rc.After["role"]), "projects/"+spec.Project+"/roles/"+customRoleID(t, on, "job_runner"); got != want {
			t.Errorf("%s: role is %q, want %q — the job's launchers hold that same custom role, so the two grants of it name one role", addr, got, want)
		}
		if got := str(rc.After["member"]); got != member {
			t.Errorf("%s: member is %q, want %q", addr, got, member)
		}
	}

	for _, c := range []struct {
		addr string
		role string
		cond string
		expr string
	}{
		{launcherRow, "projects/" + spec.Project + "/roles/" + customRoleID(t, on, "launcher"), "", ""},
		{writeRow, "roles/storage.objectUser", gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(spec.RunsBucket)},
		{readRow, "roles/storage.objectViewer", "", ""},
		{minterRow, "projects/" + fp + "/roles/fugaroTokenMinter", "", ""},
		{stateReadRow, "projects/" + fp + "/roles/fugaroStateRead", "", ""},
		{accessAddr, "roles/secretmanager.secretAccessor", "", ""},
	} {
		rc, ok := on[c.addr]
		if !ok {
			continue
		}
		if got := str(rc.After["role"]); got != c.role {
			t.Errorf("%s: role is %q, want %q", c.addr, got, c.role)
		}
		if got := str(rc.After["member"]); got != member {
			t.Errorf("%s: member is %q, want %q", c.addr, got, member)
		}
		title, expr := conditionStrings(rc)
		if title != c.cond || expr != c.expr {
			t.Errorf("%s: the binding's condition is (%q, %q), want (%q, %q): the write is fenced by the launchers' own condition and the other rows are fenced by nothing — the read because listing the bucket needs storage.objects.list and an object-name condition cannot grant it, and the two assignments because the resources they name are already the narrowest the row can express", c.addr, title, expr, c.cond, c.expr)
		}
	}
	if got := str(on[writeRow].After["bucket"]); got != spec.RunsBucket {
		t.Errorf("%s: bucket is %q, want %q", writeRow, got, spec.RunsBucket)
	}
	if got := str(on[readRow].After["bucket"]); got != spec.RunsBucket {
		t.Errorf("%s: bucket is %q, want %q", readRow, got, spec.RunsBucket)
	}
	if got, want := str(on[minterRow].After["service_account_id"]),
		"projects/"+fp+"/serviceAccounts/"+signer; got != want {
		t.Errorf("%s: service_account_id is %q, want %q — the grant is on the signer ACCOUNT rather than on the Firebase project, which is what keeps it iam.serviceAccounts.signJwt and not a token-creator role", minterRow, got, want)
	}
	// The database read's project and role, tied to the role the same plan
	// creates: the binding names `projects/<firebase project>/roles/<role_id>`
	// as a literal, so the two spellings are one grant only if the project is
	// the database's own and the id is the created role's. A binding in the
	// installation's project would grant a role that project does not hold.
	if got, want := str(on[stateReadRow].After["project"]), fp; got != want {
		t.Errorf("%s: project is %q, want %q — the role is declared in the database's project, and a binding in any other project names a role that project does not hold", stateReadRow, got, want)
	}
	if got, want := str(on[stateReadRole].After["role_id"]), "fugaroStateRead"; got != want {
		t.Errorf("%s: role_id is %q, want %q — the literal the binding names is this role's own id, and a role created under another id is a binding that names a role nobody creates", stateReadRole, got, want)
	}

	// The invoker grant's member is the installation's own scheduler account,
	// read off the account resource the module creates rather than written out as
	// a literal. That member is the whole of who Cloud Run's front end admits to
	// this service, and the tick's ID token is minted as the same account, so the
	// two spellings have to be one identity: a member that is not it is a
	// principal that can wake the resident orchestrator and make it read this
	// installation's node (section 4.1).
	if got, want := str(on[invokerAddr].After["member"]),
		"serviceAccount:"+SchedulerServiceAccountID+"@"+spec.Project+".iam.gserviceaccount.com"; got != want {
		t.Errorf("%s: member is %q, want %q — the module's four other ticks and this one hand the grant the scheduler account's own attribute, and the ID token the tick presents is minted as that account, so any other member is either a caller Cloud Run should not admit or a tick that cannot wake the service it is pointed at", invokerAddr, got, want)
	}

	// The never-list's eleventh rule, as the plan holds it: every row naming the
	// account is one of the table's. A grant spelled with the member and left
	// out of the list above would be a reach section 4.3 has not priced, on the
	// one account whose envelope is enumerated.
	var naming []string
	for addr, rc := range on {
		if strings.Contains(rc.json(), OrchestratorAccountID) {
			naming = append(naming, addr)
		}
	}
	sort.Strings(naming)
	if wantNaming := append([]string{}, want...); !slices.Equal(naming, wantNaming) {
		t.Errorf("the rows naming %s are %v, want exactly the table's %v: a grant beside them is a reach the design's section 4.3 does not have, and the account is never joined to var.launchers (section 6 rule 11)", OrchestratorAccountID, naming, wantNaming)
	}
	for _, rc := range on {
		for _, never := range []string{"roles/firebasedatabase.viewer", "roles/datastore.viewer", "roles/serviceusage.serviceUsageConsumer", "roles/owner", "roles/editor"} {
			if strings.Contains(rc.json(), never) && strings.Contains(rc.json(), OrchestratorAccountID) {
				t.Errorf("%s grants %s to %s: those are the roles membership of var.launchers would hand the account, and the launcher list's database read bypasses the Realtime Database rules and reaches every mailbox of the installation (section 6 rule 11)", rc.Address, never, OrchestratorAccountID)
			}
		}
	}

	// The tick is written from the service's deterministic URL, and the cover
	// holds it by the service's name (tf.orchestratorServiceName), so the name
	// the module renders is the one the installation's block carries. A value
	// the plan cannot read yet is the project number's own read at apply.
	if ht, _ := on[tickAddr].After["http_target"].([]any); len(ht) == 1 {
		m, _ := ht[0].(map[string]any)
		if uri, ok := m["uri"].(string); ok {
			prefix := "https://" + OrchestratorServiceName + "-"
			if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, ".run.app/tick") || strings.Contains(uri, ".a.run.app") {
				t.Errorf("%s: uri is %q, want %s<project number>.<region>.run.app/tick — the form the managed gate's cover reads, not Cloud Run's own .uri", tickAddr, uri, prefix)
			}
		}
	}

	// And the run's one confirmation: `fugaro update` may not turn an
	// installation's own opt-in into a question the owner has to answer item by
	// item. Other rows of this plan may legitimately be refused (the store's own
	// test says so), so what is asserted is that none of them is this account's.
	//
	// The cover is the run's own with the recorded owner written in: the run
	// reads that owner out of the local config's image_source, and the fixture
	// the shared helper parses predates the field, so its cover belongs to an
	// installation whose base images have no owner — and an empty owner refuses
	// every reference through the pull-through repository (cover.go's pinOK),
	// which would refuse this plan's service for a reason the installation
	// above does not have. The fixture's own M5 additions and the golden tfvars
	// name the same owner the block above does.
	cover := theRunsConfirmation(t, spec)
	cover.Owner = orchestratorImageOwner
	for _, item := range cover.NotCovered(plan, nil) {
		if strings.Contains(item, OrchestratorAccountID) || strings.Contains(item, "orchestrator") {
			t.Errorf("the run's one confirmation refuses the resident orchestrator: %s\nsection 4.3's rows are what the run's own review screen admits, and `fugaro update` creating them must not ask its own question about them", item)
		}
	}
}

// The daemon's own way into the credentials service, at the plan level (F19
// and O2 of docs/forge-labels; E16, D2049). Two facts, and both are about a
// plan rather than about the module's text: the installation grants the
// daemon's account the invoker bit on the credentials service — without which
// Cloud Run's front end refuses its call before the class check runs — and the
// same account is the whole of FUGARO_ORCHESTRATOR_ACCOUNTS, the class the
// service admits a label write and the gated merge mint by.
//
// The fixture is the local config build_identity_test.go plans its own
// installations from — the service on, with the image a release pins and the
// job table the config's repository derives — with the orchestrator block
// added by hand exactly as an operator's own tfvars would carry it: this
// release has no writer for the key, and the plan is of the module either way.
//
// The second half is the fail-closed direction: on the same installation with
// no orchestrator block the entry is absent and no grant is made, so a service
// deployed by a release before this one — or by this one on an installation
// that never opted in — starts with a class that admits nobody and answers
// every caller of the two routes with the class's own refusal rather than
// admitting a caller nobody named.
func TestOrchestratorDaemonIsTheCredentialsServicesOwnCallerClass(t *testing.T) {
	lc := credentialsLC(t)
	spec, err := Installation(lc, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := Credentials(lc, credsVarsImage(), nil)
	if err != nil {
		t.Fatal(err)
	}
	spec.CredentialsService = &svc
	const (
		invokerAddr = "module.installation.google_cloud_run_v2_service_iam_member.orchestrator_credentials_invoker[0]"
		serviceAddr = "module.installation.google_cloud_run_v2_service.credentials[0]"
		envName     = "FUGARO_ORCHESTRATOR_ACCOUNTS"
	)
	account := OrchestratorAccountID + "@" + spec.Project + ".iam.gserviceaccount.com"
	member := "serviceAccount:" + account

	_, off := installationPlan(t, spec)
	svcOff, ok := off[serviceAddr]
	if !ok {
		t.Fatalf("the plan of the credentials fixture creates no credentials service: every fact below hangs off it")
	}
	envOff := credentialsServiceEnv(t, svcOff)
	if got, ok := envOff[envName]; ok {
		t.Errorf("%s carries %s = %q on an installation that runs no resident orchestrator: the class has to be absent rather than empty, so a service this release deploys on an installation with no daemon admits nobody", serviceAddr, envName, got)
	}
	if _, ok := off[invokerAddr]; ok {
		t.Errorf("%s is in the plan of an installation that never opted in: the daemon does not exist there, and a grant to an account the same plan does not create is a principal nobody can be", invokerAddr)
	}

	_, on := installationPlanWith(t, spec, map[string]any{"orchestrator": orchestratorBlock(t, spec, credentialsFP)})

	row, ok := on[invokerAddr]
	if !ok {
		t.Fatalf("turning the resident orchestrator on plans no %s: the daemon's token is minted and never served, because Cloud Run's front end refuses a caller without roles/run.invoker before the container decides the caller's class", invokerAddr)
	}
	for _, c := range []struct{ key, want string }{
		{"role", "roles/run.invoker"},
		{"member", member},
		{"name", CredentialsServiceName},
		{"project", spec.Project},
		{"location", spec.Region},
	} {
		if got := str(row.After[c.key]); got != c.want {
			t.Errorf("%s: %s is %q, want %q — the grant is the invoker bit on this installation's own credentials service for the account this same plan creates, and any other member, role or service is a different reach with the same name", invokerAddr, c.key, got, c.want)
		}
	}
	if title, expr := conditionStrings(row); title != "" || expr != "" {
		t.Errorf("%s carries the condition (%q, %q): the service's own class check is what decides who is served, and the grant stays unconditional as every other invoker grant of this service is", invokerAddr, title, expr)
	}
	if _, ok := on["module.installation.google_service_account.orchestrator[0]"]; !ok {
		t.Fatalf("the plan grants %s the invoker bit and creates no such account: the member above would name an identity nothing makes", account)
	}

	svcOn, ok := on[serviceAddr]
	if !ok {
		t.Fatalf("the plan with the opt-in creates no credentials service: %s is in it and the service it names is not", invokerAddr)
	}
	env := credentialsServiceEnv(t, svcOn)
	raw, ok := env[envName]
	if !ok {
		t.Fatalf("the credentials service's environment carries no %s (%v): the class admits nobody, and the daemon's label write and merge mint are refused with every other caller after the front end has already admitted it", envName, env)
	}
	var got []string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("%s is not a JSON array of addresses (%q): internal/cli's credentialsServiceOrchestrators refuses to build a service from one it cannot decode", envName, raw)
	}
	if want := []string{account}; !slices.Equal(got, want) {
		t.Errorf("%s is %v, want exactly %v: the class is the daemon's own address and nothing else — a person or a run account in it is a caller the label route and the merge mint must refuse", envName, got, want)
	}
	// The entry is the ONLY thing the opt-in changes about this service: the
	// class lists beside it are the installation's own record, and an update
	// that moved one of them would silently widen or narrow another route.
	for name, value := range envOff {
		if name == envName {
			continue
		}
		if env[name] != value {
			t.Errorf("turning the resident orchestrator on changed %s: %q became %q — the daemon's own class is the one entry it adds, and the other routes' classes are the installation's record", name, value, env[name])
		}
	}
	for name := range env {
		if name == envName {
			continue
		}
		if _, ok := envOff[name]; !ok {
			t.Errorf("turning the resident orchestrator on added %s to the credentials service: the daemon's own class is the one entry it adds", name)
		}
	}
}

// customRoleID is the role_id of the custom role the plan creates under the
// installation module, which is what a grant naming that role as a literal has
// to name.
func customRoleID(t *testing.T, plan map[string]plannedResource, name string) string {
	t.Helper()
	addr := "module.installation.google_project_iam_custom_role." + name
	rc, ok := plan[addr]
	if !ok {
		t.Fatalf("the plan creates no %s: the roles the orchestrator's bindings name are this installation's own custom roles", addr)
	}
	id := str(rc.After["role_id"])
	if id == "" {
		t.Fatalf("%s: the plan leaves the role's id unknown, so the literal the orchestrator's grant names cannot be tied to it", addr)
	}
	return id
}

// conditionStrings is a binding's condition as the two strings the module
// writes, and "" when the binding carries none.
func conditionStrings(rc plannedResource) (title, expr string) {
	list, _ := rc.After["condition"].([]any)
	for _, c := range list {
		m, _ := c.(map[string]any)
		title, expr = str(m["title"]), str(m["expression"])
	}
	return title, expr
}

// missing is the entries of want the plan does not have, in the order want
// names them.
func missing(got, want []string) []string {
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	var out []string
	for _, w := range want {
		if !seen[w] {
			out = append(out, w)
		}
	}
	return out
}

// str is a planned attribute as a string, with the empty string for one the
// plan leaves unknown or absent.
func str(v any) string {
	s, _ := v.(string)
	return s
}

// TestOrchestratorOperatorsMayActAsTheDaemonsAccount is the grant that lets the
// daemon run on an operator's machine: with the block on, each operator holds
// roles/iam.serviceAccountTokenCreator on fugaro-orch (and on that account
// alone), and with it off nobody does. It is the plan-level half of
// iam.serviceAccountTokenCreator's one new site (orchestrator.tf).
func TestOrchestratorOperatorsMayActAsTheDaemonsAccount(t *testing.T) {
	lc := credentialsLC(t)
	spec, err := Installation(lc, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec.Operators = []string{"user:ops@example.com"}
	const prefix = "module.installation.google_service_account_iam_member.fugaro_orchestrator_token_creator"

	_, off := installationPlan(t, spec)
	for addr := range off {
		if strings.HasPrefix(addr, prefix) {
			t.Errorf("%s is in the plan of an installation with no orchestrator block", addr)
		}
	}

	_, on := installationPlanWith(t, spec, map[string]any{"orchestrator": orchestratorBlock(t, spec, credentialsFP)})
	row, ok := on[prefix+`["user:ops@example.com"]`]
	if !ok {
		t.Fatalf("the plan with the orchestrator on grants the operator no token creation on the daemon's account")
	}
	account := "projects/" + spec.Project + "/serviceAccounts/" + OrchestratorAccountID + "@" + spec.Project + ".iam.gserviceaccount.com"
	for key, want := range map[string]string{"role": "roles/iam.serviceAccountTokenCreator", "member": "user:ops@example.com", "service_account_id": account} {
		if got := str(row.After[key]); got != want {
			t.Errorf("%s is %q, want %q", key, got, want)
		}
	}
}
