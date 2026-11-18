//go:build terraform

package infra

// The resident orchestrator's own class in the installation's credentials
// service (F19, O2; E16, D2049, and credsvc's AuthConfig.OrchestratorAccounts),
// planned for real.
//
// The module's text test (deploy/terraform/gcp/modules/installation) reads
// which resources exist and what each of them names, and internal/cli's tests
// read what the process makes of the environment it is handed; what neither of
// them can see is the pair of facts this file exists for: that a REAL plan of
// an installation that runs the credentials service and opts into a resident
// orchestrator hands the service the daemon's own address in the class it
// admits, and that the plan's two new rows — the account's invoker bit on the
// service and the operators' token-creator grant on the account — are the
// design's section 4.3 table rather than a widening of it. The fixture is the
// installation the credentials golden plan was made from plus the operator
// roster B5's own plan test adds, so the class is read off a plan whose service
// is the same one the golden holds.

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// orchestratorEnrolledInstallation is the spec these tests plan: the
// installation that runs the credentials service (build_identity_test.go's own
// fixture, and the one testdata/golden/installation-credentials.plan.json is
// the plan of) with the orchestrator block added, which is exactly how an
// operator who has no CLI flag for the opt-in yet would ask for one.
func orchestratorEnrolledInstallation(t *testing.T) (InstallationSpec, CredentialsServiceSpec) {
	t.Helper()
	lc := parseLC(t, m4LocalConfig+m5Additions+credentialsBudgetLC+buildIdentityOperatorsLC)
	spec, err := Installation(lc, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := Credentials(lc, credsVarsImage(), nil)
	if err != nil {
		t.Fatal(err)
	}
	spec.CredentialsService = &svc
	return spec, svc
}

// TestOrchestratorIsTheCredentialsServicesOwnClass is the deliverable read off
// a plan: the service's container carries the daemon's own address, and it is
// the whole of that class.
//
// Three things are asserted together because each of them alone is passable by
// a wrong installation: the entry is a JSON array (the shape
// internal/cli's credentialsServiceOrchestratorAccounts decodes, and a bare
// address here would be a service that never binds a port), the array holds
// exactly one address, and that address is this plan's own orchestrator
// account — composed from the ID the design fixes and the project this same
// plan creates it in.
//
// Mutation (run, restore): render the entry from anything else — an address of
// another project, a second address beside it, the account's own resource
// attribute (which is what makes it unknown at plan time) — and this fails.
func TestOrchestratorIsTheCredentialsServicesOwnClass(t *testing.T) {
	spec, svc := orchestratorEnrolledInstallation(t)
	account := OrchestratorAccountID + "@" + spec.Project + ".iam.gserviceaccount.com"
	_, rows := installationPlanWith(t, spec, map[string]any{"orchestrator": orchestratorBlock(t, spec, svc.FirebaseProject)})

	const serviceAddr = "module.installation.google_cloud_run_v2_service.credentials[0]"
	svcRow, ok := rows[serviceAddr]
	if !ok {
		t.Fatalf("the plan creates no credentials service (%s is not in it): this test is about the class the service admits, and without the service there is no class", serviceAddr)
	}
	env := credentialsServiceEnv(t, svcRow)
	raw, ok := env["FUGARO_ORCHESTRATOR_ACCOUNTS"]
	if !ok {
		t.Fatalf("the credentials service's environment carries no FUGARO_ORCHESTRATOR_ACCOUNTS (%v): the daemon's own class is then empty and the label route and the merge mint refuse the daemon with the sentence a stranger gets", env)
	}
	var got []string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("FUGARO_ORCHESTRATOR_ACCOUNTS is not a JSON array of addresses (%q): the process refuses to start on one it cannot decode", raw)
	}
	if want := []string{account}; !slices.Equal(got, want) {
		t.Errorf("FUGARO_ORCHESTRATOR_ACCOUNTS is %v, want %v: the class is the one account this plan creates for the daemon — a second address is a second principal admitted to the label route and the merge mint, and an empty list over an opted-in orchestrator is a daemon with no identity at all", got, want)
	}
}

// TestOrchestratorIsInvokerOnTheCredentialsService: Cloud Run decides admission
// per service, so the class above is unreachable without the invoker bit — the
// front end refuses the daemon before the container reads the class, and the
// operator meets a 403 rather than the sentence this task exists to produce.
//
// The grant is read whole — the service, the role, the member, the project and
// the absence of a condition — for build_identity_test.go's reason: "invoker
// on the credentials service" is the whole of what this row grants, and a
// member that is another address or a condition fencing it to a resource the
// next plan does not carry would each be a different fact with the same name.
func TestOrchestratorIsInvokerOnTheCredentialsService(t *testing.T) {
	spec, svc := orchestratorEnrolledInstallation(t)
	account := OrchestratorAccountID + "@" + spec.Project + ".iam.gserviceaccount.com"
	_, rows := installationPlanWith(t, spec, map[string]any{"orchestrator": orchestratorBlock(t, spec, svc.FirebaseProject)})

	const addr = "module.installation.google_cloud_run_v2_service_iam_member.orchestrator_credentials_invoker[0]"
	g, ok := rows[addr]
	if !ok {
		t.Fatalf("the plan grants no invoker on the credentials service: %s is not in it, so the daemon's class can never be reached — Cloud Run's front end refuses it whatever FUGARO_ORCHESTRATOR_ACCOUNTS says", addr)
	}
	if g.Type != "google_cloud_run_v2_service_iam_member" {
		t.Fatalf("%s is a %s, want the service-level IAM member", addr, g.Type)
	}
	if got := str(g.After["role"]); got != "roles/run.invoker" {
		t.Errorf("%s grants %q, want roles/run.invoker: this row is the invoker bit on this one service and nothing else", addr, got)
	}
	if got := str(g.After["member"]); got != "serviceAccount:"+account {
		t.Errorf("%s grants to %q, want serviceAccount:%s — the address of the account this same plan creates, which is the address the class above names", addr, got, account)
	}
	if got := str(g.After["name"]); got != CredentialsServiceName {
		t.Errorf("%s grants the invoker role on service %q, want %q: the daemon must invoke the credentials service and no other", addr, got, CredentialsServiceName)
	}
	if got := str(g.After["project"]); got != spec.Project {
		t.Errorf("%s grants in project %q, want %q", addr, got, spec.Project)
	}
	if conds, _ := g.After["condition"].([]any); len(conds) > 0 {
		t.Errorf("%s carries a condition: the service's own ID-token check is what decides who is served (Q6), and the grant stays unconditional as every invoker grant of this service is", addr)
	}
}

// TestOperatorsMayImpersonateTheOrchestratorAccount: the daemon runs on the
// operator's machine (D1241) and the class it is admitted as is a SERVICE
// ACCOUNT whose token is minted for the service's own URL as its audience — a
// mint gcloud refuses for a user account outright ("Invalid account type for
// --audiences"). So the one way the installation can admit its own daemon is
// the operators' impersonation of that account, and this row is the whole of
// it: one grant per operator, on the account, under the ID the design fixes.
//
// The account is read off the row's own service_account_id as a literal path
// and not off the account resource, which is TestInstallationService
// AccountGrantsNameTheirAccount's rule and the cover's: a computed account is
// one nobody can check (cover.deployerTokenCreator).
func TestOperatorsMayImpersonateTheOrchestratorAccount(t *testing.T) {
	spec, svc := orchestratorEnrolledInstallation(t)
	account := OrchestratorAccountID + "@" + spec.Project + ".iam.gserviceaccount.com"
	_, rows := installationPlanWith(t, spec, map[string]any{"orchestrator": orchestratorBlock(t, spec, svc.FirebaseProject)})
	if len(spec.Operators) == 0 {
		t.Fatalf("the fixture names no operator: this test is about the impersonation the operators hold, and a fixture that names none could not tell the grant from a grant nobody holds")
	}

	for _, op := range spec.Operators {
		addr := "module.installation.google_service_account_iam_member.orchestrator_token_creator[" + strconv.Quote(op) + "]"
		g, ok := rows[addr]
		if !ok {
			t.Errorf("the plan grants no impersonation on the orchestrator's account to %s (%s is not in it): the operator's own credential cannot be the daemon's class — the mint the service demands names a service account and the service's own URL as the audience, which gcloud refuses for a user account — so without this row the daemon has no identity to present at all", op, addr)
			continue
		}
		if got := str(g.After["role"]); got != tf.DeployerTokenCreatorRole {
			t.Errorf("%s grants %q, want %s: the mint is a service account impersonation and this is the role that makes one possible", addr, got, tf.DeployerTokenCreatorRole)
		}
		if got, want := str(g.After["service_account_id"]), "projects/"+spec.Project+"/serviceAccounts/"+account; got != want {
			t.Errorf("%s impersonates %q, want %q — written out from the project rather than read off the account resource, whose name attribute reaches the plan unknown (cover's token-creator rule refuses a computed account)", addr, got, want)
		}
		if got := str(g.After["member"]); got != op {
			t.Errorf("%s grants to %q, want %q", addr, got, op)
		}
	}
	// And the account is nobody else's: one row per operator, and no row for a
	// principal the installation did not list.
	for addr := range rows {
		if !strings.HasPrefix(addr, "module.installation.google_service_account_iam_member.orchestrator_token_creator[") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(addr, "module.installation.google_service_account_iam_member.orchestrator_token_creator["), "]")
		// The key is the member as the for_each set spells it, which is quoted
		// because a member carries a colon Terraform will not take bare.
		op, err := strconv.Unquote(key)
		if err != nil {
			t.Errorf("%s: the row's key is %q, which is not a quoted IAM member: the grant's principal cannot be read out of it", addr, key)
			continue
		}
		if !slices.Contains(spec.Operators, op) {
			t.Errorf("%s impersonates the orchestrator's account and %s is not one of the installation's operators (%v): the grant is the operators' own, and a principal beside them holds the daemon's identity without the installation having listed them", addr, op, spec.Operators)
		}
	}
}

// TestTheOrchestratorRowsAreTheDesignsTable is the same fact from the review
// screen's side: `fugaro update` creating these rows may not turn an
// installation's own opt-in into a question the owner answers item by item,
// and a row the cover refuses is a row the design's section 4.3 has not
// priced.
//
// It is asserted on a plan that carries BOTH halves of this task — the
// credentials service and the orchestrator — which the file's other plan test
// (orchestrator_terraform_test.go) cannot be: its fixture runs no service, so
// this task's two rows are zero-count there and the never-list's loop has
// nothing to hold.
func TestTheOrchestratorRowsAreTheDesignsTable(t *testing.T) {
	spec, svc := orchestratorEnrolledInstallation(t)
	plan, rows := installationPlanWith(t, spec, map[string]any{"orchestrator": orchestratorBlock(t, spec, svc.FirebaseProject)})

	// The two rows this task adds name the account, and they are the only ones
	// beyond the design's own table (orchestrator.tf's rows) that do: the
	// invoker bit on the service whose class the account is admitted as, and
	// the operators' impersonation of it.
	for _, addr := range []string{
		"module.installation.google_cloud_run_v2_service_iam_member.orchestrator_credentials_invoker[0]",
		"module.installation.google_service_account_iam_member.orchestrator_token_creator[" + strconv.Quote(spec.Operators[0]) + "]",
	} {
		if _, ok := rows[addr]; !ok {
			t.Errorf("%s is not in the plan: the run's own confirmation cannot refuse a row that is not there, and this test would assert nothing about it", addr)
		}
	}

	// The cover's own half, with the recorded owner written in: the run reads
	// that owner out of the local config's image_source, and the fixture the
	// shared helper parses predates the field (orchestrator_terraform_test.go
	// states the same, and an empty owner refuses every reference through the
	// pull-through repository).
	//
	// The installation's Firebase project is added here because this fixture
	// runs no history job and no coordinator, which are the two the shared
	// helper reads it from: the run's own cover carries it as well
	// (internal/cli's initEngine.cover appends the Firebase project the
	// installation was updated against), and without it the token-minter row's
	// role — declared in that project, which is not the installation's own —
	// reads as a role this run does not know, which is the refusal this test
	// would then report as the design's rather than the cover's.
	cover := theRunsConfirmation(t, spec)
	cover.Owner = orchestratorImageOwner
	if svc.FirebaseProject != "" && !slices.Contains(cover.Projects, svc.FirebaseProject) {
		cover.Projects = append(cover.Projects, svc.FirebaseProject)
	}
	for _, item := range cover.NotCovered(plan, nil) {
		if strings.Contains(item, OrchestratorAccountID) || strings.Contains(item, "orchestrator") {
			t.Errorf("the run's one confirmation refuses the resident orchestrator: %s\nsection 4.3's rows are what the run's own review screen admits, and `fugaro update` creating them must not ask its own question about them", item)
		}
	}
}
