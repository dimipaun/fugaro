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
// is the root's own resource (roots/installation/main.tf) because the jobs are.
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
// file declares them, and the test fails on any other: a resource added to this
// file is a grant the design's table does not have a row for, and a resource
// removed from it is a row the account can no longer reach.
var orchestratorResources = []struct{ typ, name string }{
	{"google_service_account", "orchestrator"},
	{"google_project_iam_member", "orchestrator_launcher"},
	{"google_storage_bucket_iam_member", "orchestrator_runs_launcher"},
	{"google_storage_bucket_iam_member", "orchestrator_runs_reader"},
	{"google_service_account_iam_member", "orchestrator_token_minter"},
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
// table: the account exists, and the four bindings beside it are the launcher's
// role, the bucket's write under the launchers' own condition, the bucket's
// read with no condition, and the token-minter role on the signer account —
// each of them naming exactly what section 4.3 names and nothing else.
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
	// account out of. The four rows below each carry the expression in `need`
	// for that reason, and the computed form in `absent`.
	for _, c := range []struct {
		what   string
		block  string
		need   []string
		absent []string
	}{
		{
			what:  "the launcher's project role: rows 1 and 3 of the table, the metadata permissions",
			block: tfBlock(src, "google_project_iam_member", "orchestrator_launcher"),
			need: []string{
				`role    = "projects/${var.project}/roles/fugaroLauncher"`,
				`project = var.project`,
				`depends_on = [google_project_iam_custom_role.launcher]`,
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
// TestOrchestratorTurnedOnChangesNothingElse is the plan-level half.
func TestOrchestratorIsNeverALauncher(t *testing.T) {
	src := tfSource(t, orchestratorTF)
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
	// of this table would look for. outputs.tf is the one other file that names
	// the account, and it names its EMAIL to publish the identity rather than
	// binding anything with it.
	for name, text := range tfFiles(t) {
		if name == orchestratorTF {
			continue
		}
		if strings.Contains(text, orchestratorMemberExpr) {
			t.Errorf("%s names the orchestrator's address as a member: section 4.3's table is orchestrator.tf's alone, and a grant written in a second file is one the table's own test cannot see", name)
		}
		if strings.Contains(text, "google_service_account.orchestrator") && name != "outputs.tf" {
			t.Errorf("%s names the orchestrator's account resource: the table is orchestrator.tf's, and outputs.tf is the one other file that may name it — to publish the identity rather than to bind anything with it", name)
		}
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
// TestOrchestratorTurnedOffPlansExactlyAsBefore.
func TestOrchestratorLeavesAPlanUntouchedWhenItIsOff(t *testing.T) {
	src := tfSource(t, orchestratorTF)
	if !strings.Contains(src, "orchestrator_on = var.orchestrator != null") {
		t.Errorf("%s: the gate is not the variable's own null check: every resource here is counted on one local, and a second way to turn the file on would leave a resource behind (credentials.tf's shape)", orchestratorTF)
	}
	if n := strings.Count(src, `resource "`); n != len(orchestratorResources) {
		t.Fatalf("%s declares %d resources and %d are expected: the count below cannot be checked against a list that is not the file's", orchestratorTF, n, len(orchestratorResources))
	}
	for _, r := range orchestratorResources {
		block := tfBlock(src, r.typ, r.name)
		if !strings.Contains(block, "count = local.orchestrator_on ? 1 : 0") {
			t.Errorf("%s: %s.%s is not counted on local.orchestrator_on, so an installation that has not opted in acquires it:\n%s", orchestratorTF, r.typ, r.name, block)
		}
	}

	vars := tfSource(t, "variables.tf")
	block := tfVariable(t, vars, "orchestrator")
	if !regexp.MustCompile(`(?m)^\s*default = null\s*$`).MatchString(block) {
		t.Errorf("variables.tf: var.orchestrator does not default to null: the switch is OFF for every installation that has not opted in, and one that did not ask must plan exactly what it planned before this task (design section 4.3; the brief's no-op rule for `fugaro update`):\n%s", block)
	}
}
