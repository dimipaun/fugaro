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
//     installation for having asked for it.
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
// the rows the opt-in adds are the account, the launcher's project role, the
// runs bucket's conditioned write and its unconditioned read, the token-minter
// role on the signer, and the job runner's role on each of the installation's
// own jobs — and the plan does not move in any other way.
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
	plan, on := installationPlanWith(t, spec, map[string]any{"orchestrator": map[string]any{
		"account_id":       OrchestratorAccountID,
		"firebase_project": fp,
		"token_signer":     signer,
	}})

	const (
		accountAddr = "module.installation.google_service_account.orchestrator[0]"
		launcherRow = "module.installation.google_project_iam_member.orchestrator_launcher[0]"
		writeRow    = "module.installation.google_storage_bucket_iam_member.orchestrator_runs_launcher[0]"
		readRow     = "module.installation.google_storage_bucket_iam_member.orchestrator_runs_reader[0]"
		minterRow   = "module.installation.google_service_account_iam_member.orchestrator_token_minter[0]"
	)
	want := []string{accountAddr, launcherRow, writeRow, readRow, minterRow}
	for key := range spec.ProjectJobs {
		want = append(want, `google_cloud_run_v2_job_iam_member.orchestrator_runner["`+key+`"]`)
	}
	sort.Strings(want)

	// The plan's own rows: the account and its four bindings, one binding per
	// project job, and nothing beside them. A row missing is a launch or a mint
	// the resident orchestrator cannot make; a row added is a grant with no row
	// in the design's table.
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
	} {
		if got := str(on[c.addr].After[c.key]); got != c.want {
			t.Errorf("%s: %s is %q, want %q — the root writes the name out rather than reading it off the resource it names, so the two are one name only if they are equal here", c.addr, c.key, got, c.want)
		}
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
			t.Errorf("%s: the binding's condition is (%q, %q), want (%q, %q): the write is fenced by the launchers' own condition and the read is fenced by nothing, because listing the bucket needs storage.objects.list and an object-name condition cannot grant it", c.addr, title, expr, c.cond, c.expr)
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

	// The never-list's eleventh rule, as the plan holds it: every row naming the
	// account is one of the table's. A grant spelled with the member and left
	// out of the list above — the Firebase root's database read among them —
	// would be a reach section 4.3 has not priced, on the one account whose
	// envelope is enumerated.
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

	// And the run's one confirmation: `fugaro update` may not turn an
	// installation's own opt-in into a question the owner has to answer item by
	// item. Other rows of this plan may legitimately be refused (the store's own
	// test says so), so what is asserted is that none of them is this account's.
	for _, item := range theRunsConfirmation(t, spec).NotCovered(plan, nil) {
		if strings.Contains(item, OrchestratorAccountID) || strings.Contains(item, "orchestrator") {
			t.Errorf("the run's one confirmation refuses the resident orchestrator: %s\nsection 4.3's rows are what the run's own review screen admits, and `fugaro update` creating them must not ask its own question about them", item)
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
