//go:build terraform

package infra

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// The public mobile service beside the credentials service (mobile plan task
// P5; D2782 as amended by F31, O8). The credentials service's own plan is the
// last word on what an installation that runs it holds, so the three facts this
// file is about are read off a REAL plan of the embedded installation root
// against the fixture the credentials golden is made from
// (internal/infra/testdata/installation-credentials.tfvars.json, the plan
// testdata/golden/installation-credentials.plan.json holds):
//
//   - a SECOND Cloud Run service of the same released pin, its own runtime
//     account and the image's `mobile` mode, reached through ONE public invoker
//     binding — while the private service keeps every one of its own invoker
//     grants and gains no public one;
//   - the signer is a SECOND account, created in the FIREBASE project (Firebase
//     accepts a custom token only from an account of the Firebase project, and
//     an installation can span two), and the only grant naming it is the
//     runtime account's signJwt, whose role is the project's own
//     fugaroTokenMinter;
//   - the mobile runtime account's ONLY project-level binding is the unchanged
//     custom role fugaroCredentialsDatabase (whole instance, rules bypassed;
//     O1) — no other project role, on no other project.
//
// The three are one task's contract because they are one reach: O8's correction
// is that signJwt on the signer is the widest grant in this tree — it signs ANY
// claim set for ANY uid — so what bounds the account is that Firebase accepts a
// custom token only from an account of the Firebase project, together with the
// route allowlist the process mounts (I1) and the tests over it. The module
// states both (mobile_service.tf's header), and these tests hold the plan to the
// grants the task announced rather than to the sentence.
const (
	// mobileAccountID and mobileServiceName are the runtime account and the
	// service the module names (mobile_service.tf): the account the public
	// service runs as, and the service itself. They are literals of the module
	// and not variables, because the credential this service holds is the one
	// that may mint a device token — a name an operator's tfvars could move is a
	// name a review screen cannot read.
	mobileAccountID   = "fugaro-mobile"
	mobileServiceName = "fugaro-mobile"
	// deviceSignerAccountID is the device-only signer account the FIREBASE root
	// creates beside google_service_account.signer
	// (deploy/terraform/gcp/modules/firebase/iam.tf), and deviceSignerRoleID is
	// the role the installation root grants the runtime account ON that account:
	// the Firebase project's own fugaroTokenMinter, whose one permission is
	// iam.serviceAccounts.signJwt.
	deviceSignerAccountID = "fugaro-device-signer"
	deviceSignerRoleID    = "fugaroTokenMinter"
	// credentialsDatabaseRoleID is the service's database role (D146), declared
	// in the Firebase project by credentials.tf and unchanged by this task: the
	// mobile account holds it exactly as the private service's account does.
	credentialsDatabaseRoleID = "fugaroCredentialsDatabase"
)

// credentialsMobileArgs is the argv the module writes for the public service:
// the released image's own credentials subcommand in its mobile mode, which
// mounts the allowlisted mobile routes and none of the existing ones (I1 owns
// what that mode mounts). The image's default command serves the PRIVATE
// service, so an argv that did not name the mode would serve the credentials
// routes from a public URL — the one outcome F31 exists to prevent.
var credentialsMobileArgs = []string{"credentials", "serve", "--mobile"}

// credentialsMobilePlan is the credentials fixture planned for real, with the
// four reads these tests are about: the plan's rows by address, the project and
// the Firebase project the fixture names.
type credentialsMobilePlan struct {
	rows map[string]plannedResource
	// project and firebaseProject are the fixture's own, and the signer's
	// address is derived from the second: that is the whole point of the task.
	project         string
	firebaseProject string
}

// mobileMember is the runtime account as an IAM member, spelled the way the
// provider spells a service account of the installation's project.
func (p credentialsMobilePlan) mobileMember() string {
	return "serviceAccount:" + mobileAccountID + "@" + p.project + ".iam.gserviceaccount.com"
}

// credentialsMobilePlanOf plans the credentials fixture. The fixture is the
// golden's own, decoded into the spec this package writes and planned by the
// same helper every other installation plan test uses: the module, and not a
// second spelling of it in Go, is what the rows below come from.
func credentialsMobilePlanOf(t *testing.T) credentialsMobilePlan {
	t.Helper()
	spec := credentialsMobileSpecOf(t)
	fp := spec.CredentialsService.FirebaseProject
	// enable_mobile_service is the module's switch (mobile_service.tf), and the
	// plan is the plan of an installation that has turned it ON: no Go field and
	// no fixture carries it, because `fugaro update` writes no key for it until
	// the release that lands the mode the service is started in (I1), so the key
	// is written here and nowhere else — exactly as an operator's own hand-edited
	// tfvars would carry it.
	_, rows := installationPlanWith(t, spec, map[string]any{enableMobileServiceVar: true})
	return credentialsMobilePlan{rows: rows, project: spec.Project, firebaseProject: fp}
}

// credentialsMobileSpecOf is the credentials fixture as this package writes it:
// the one installation whose plan reaches the mobile service at all, and the one
// whose Firebase project is not its own — the shape the second signer exists
// for.
func credentialsMobileSpecOf(t *testing.T) InstallationSpec {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "installation-credentials.tfvars.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec InstallationSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.CredentialsService == nil {
		t.Fatalf("the credentials fixture carries no credentials_service block: this plan is the one installation that reaches the mobile service at all")
	}
	fp := spec.CredentialsService.FirebaseProject
	if fp == "" || fp == spec.Project {
		t.Fatalf("the credentials fixture names Firebase project %q beside the installation's own %q: this task's whole shape is a signer in the FIREBASE project, and a same-project fixture could not tell the two apart", fp, spec.Project)
	}
	return spec
}

// afterStr is one planned attribute as a string, "" when the plan left it
// computed: a read of an unknown attribute has to be visible as an empty value
// rather than as a panic.
func afterStr(row plannedResource, attr string) string {
	s, _ := row.After[attr].(string)
	return s
}

// The public service: a second Cloud Run service of the credentials service's
// own released pin, running as its own account, in the image's mobile mode, and
// reached through exactly one public invoker binding — while the private
// service keeps every invoker grant it had and gains none that names allUsers.
//
// The red step is the pair of assertions at the end: before this task the plan
// holds no such service at all, and the private service's own invoker set is
// the three classes iam.tf grants (a launcher's, the coordinator's writer and
// the verifier). "The existing service stays private" is read here as a fact of
// the whole plan — every run.invoker binding to allUsers in it is the mobile
// service's — because a second public member anywhere would be the model
// credentials behind a public URL, which is F31's own finding.
func TestCredentialsMobileIsASecondPublicService(t *testing.T) {
	p := credentialsMobilePlanOf(t)
	const (
		svcAddr  = "module.installation.google_cloud_run_v2_service.mobile[0]"
		acctAddr = "module.installation.google_service_account.mobile[0]"
		pubAddr  = "module.installation.google_cloud_run_v2_service_iam_member.mobile_public[0]"
	)
	acct, ok := p.rows[acctAddr]
	if !ok {
		t.Fatalf("the plan creates no mobile runtime account (%s): the public service would run as the account that reads every model credential in the bundle, or as nothing", acctAddr)
	}
	if got := afterStr(acct, "account_id"); got != mobileAccountID {
		t.Errorf("%s is the account %q, want %q", acctAddr, got, mobileAccountID)
	}
	svc, ok := p.rows[svcAddr]
	if !ok {
		t.Fatalf("the plan creates no mobile service (%s): this task's whole acceptance is a second Cloud Run service of the credentials image with a public invoker, so a phone still has nothing to call (F31)", svcAddr)
	}
	if got := afterStr(svc, "name"); got != mobileServiceName {
		t.Errorf("%s is the service %q, want %q", svcAddr, got, mobileServiceName)
	}
	// The pin is the SAME released image as the private service's: the mobile
	// endpoints are the credentials binary's own code (D2782 as amended), so a
	// service running an image of its own would be code no review read.
	credsSvc, ok := p.rows["module.installation.google_cloud_run_v2_service.credentials[0]"]
	if !ok {
		t.Fatalf("the plan creates no credentials service: the fixture is not the installation this task is about")
	}
	if got, want := mobileContainerImage(t, svc), mobileContainerImage(t, credsSvc); got != want {
		t.Errorf("%s runs the image %q, want the credentials service's own pin %q: the mobile routes are the released binary's, one image and one review", svcAddr, got, want)
	}
	if got := afterStr(acct, "email"); got != mobileAccountID+"@"+p.project+".iam.gserviceaccount.com" {
		t.Errorf("%s is %q, want the account of the installation's own project: it holds a project-level database role and signJwt on a signer of the Firebase project, and both bindings name the address this value carries", acctAddr, got)
	}
	if got := mobileServiceAccount(t, svc); got != mobileAccountID+"@"+p.project+".iam.gserviceaccount.com" {
		t.Errorf("%s runs as %q, want the account this module creates for it (%s)", svcAddr, got, acctAddr)
	}
	if got := mobileServiceArgs(t, svc); !slices.Equal(got, credentialsMobileArgs) {
		t.Errorf("%s runs with args %v, want %v: the image's default command serves the PRIVATE service, so the mode has to be named or the credentials routes are served from a public URL", svcAddr, got, credentialsMobileArgs)
	}
	if got := svc.After["invoker_iam_disabled"]; got != false {
		t.Errorf("%s has invoker_iam_disabled = %v, want false: the public reach has to be the binding below, which a review reads in the plan, and not a flag that opens the service with no resource to read", svcAddr, got)
	}
	pub, ok := p.rows[pubAddr]
	if !ok {
		t.Fatalf("the plan grants nobody the invoker on the mobile service (%s): a phone's call is refused by the front end before the container runs, and the endpoints this task deploys are reachable by nothing", pubAddr)
	}
	if got := afterStr(pub, "member"); got != "allUsers" {
		t.Errorf("%s grants the invoker to %q, want allUsers: this is the ONE public member this task adds, and every other caller class of the installation is a named member", pubAddr, got)
	}
	if got := afterStr(pub, "role"); got != "roles/run.invoker" {
		t.Errorf("%s grants %q, want roles/run.invoker", pubAddr, got)
	}
	if got := afterStr(pub, "name"); got != mobileServiceName {
		t.Errorf("%s grants the invoker on %q, want the mobile service %q", pubAddr, got, mobileServiceName)
	}
	// The private service's invoker, read as the whole plan's fact: the only
	// allUsers binding in it is the mobile service's own.
	var public []string
	for addr, row := range p.rows {
		if row.Type != "google_cloud_run_v2_service_iam_member" {
			continue
		}
		switch afterStr(row, "member") {
		case "allUsers", "allAuthenticatedUsers":
			public = append(public, addr+" ("+afterStr(row, "name")+")")
		}
	}
	slices.Sort(public)
	if len(public) != 1 || !strings.HasPrefix(public[0], pubAddr) {
		t.Errorf("the plan's public invoker bindings are %v, want the mobile service's one (%s): the credentials service's own members are the three classes iam.tf grants — the launchers, the coordinator's writer and the verifier — and a public member there is every model credential of the installation behind a URL anyone may call (F31)", public, pubAddr)
	}
}

// mobileContainerImage is the one container's image, refusing a plan that has
// none: the checks here are about a revision, and a plan without one is not the
// shape they read.
func mobileContainerImage(t *testing.T, svc plannedResource) string {
	t.Helper()
	return tplContainer(t, svc)["image"].(string)
}

// mobileServiceAccount and mobileServiceArgs read the two template-level values
// the service checks above compare: the account the revision runs as and the
// container's argv.
func mobileServiceAccount(t *testing.T, svc plannedResource) string {
	t.Helper()
	tpl, _ := svc.After["template"].([]any)
	m, _ := tpl[0].(map[string]any)
	s, _ := m["service_account"].(string)
	return s
}

func mobileServiceArgs(t *testing.T, svc plannedResource) []string {
	t.Helper()
	var out []string
	for _, a := range tplContainer(t, svc)["args"].([]any) {
		s, _ := a.(string)
		out = append(out, s)
	}
	return out
}

// tplContainer is a Cloud Run service's template[0].containers[0].
func tplContainer(t *testing.T, svc plannedResource) map[string]any {
	t.Helper()
	tpl, _ := svc.After["template"].([]any)
	if len(tpl) != 1 {
		t.Fatalf("%s has %d templates, want one", svc.Address, len(tpl))
	}
	m, _ := tpl[0].(map[string]any)
	cs, _ := m["containers"].([]any)
	if len(cs) != 1 {
		t.Fatalf("%s has %d containers, want one", svc.Address, len(cs))
	}
	c, _ := cs[0].(map[string]any)
	return c
}

// The signer: an account of the FIREBASE project, and the runtime account's
// signJwt the only grant that names it.
//
// The red step is the first assertion: before this task the Firebase root holds
// one signer (the runs' budget signer), and the installation root's mobile
// account does not exist at all. The second half is the asymmetric one O8
// records: signJwt on an account IS that account — the self-signed JWT it signs
// is an access token for the issuer — and IAM cannot limit WHICH claims are
// signed, so the only thing that keeps the reach to device tokens is the
// process's own route allowlist (I1) and the tests over it. What the plan can
// hold, and what this test holds, is that the account is the device-only signer
// and nothing else of this run's.
func TestCredentialsMobileSignerIsInTheFirebaseProject(t *testing.T) {
	p := credentialsMobilePlanOf(t)
	fb := firebaseGoldenRows(t, "firebase-mobile")
	const signerAddr = "module.firebase.google_service_account.device_signer[0]"
	signer, ok := fb[signerAddr]
	if !ok {
		t.Fatalf("the Firebase root creates no %s: Firebase accepts a custom token only from a service account of the Firebase project, so a signer created in the installation's own project could not sign one (O8, F31)", signerAddr)
	}
	if got := afterStr(signer, "account_id"); got != deviceSignerAccountID {
		t.Errorf("%s is the account %q, want %q", signerAddr, got, deviceSignerAccountID)
	}
	if got := afterStr(signer, "project"); got != p.firebaseProject {
		t.Errorf("%s is in project %q, want the Firebase project %q: the account has to be one of THAT project's for Firebase to accept its tokens, which is why it is not created beside the installation's own accounts", signerAddr, got, p.firebaseProject)
	}
	// ... and it holds no role of its own: what the mobile service may do with
	// it is the signJwt grant below, and an account with roles of its own would
	// be reach beyond the one this task announced.
	//
	// The grant is read off the FIREBASE root's golden and not off the
	// installation plan, and that is the fix of a review finding rather than a
	// preference: `fugaro update` applies the installation root BEFORE the
	// Firebase stage (internal/cli/init_stages.go), so a grant written in the
	// installation root would name an account the Firebase root had not created
	// yet on the first update carrying it — a 404 in the middle of an apply. It
	// is written where the signer is created (modules/firebase/iam.tf), and the
	// Firebase golden whose tfvars hand the runtime account over
	// (firebase-mobile, scripts/gen-golden-plans.sh) is the plan that carries
	// it.
	const grantAddr = "module.firebase.google_service_account_iam_member.mobile_token_minter[0]"
	grant, ok := fb[grantAddr]
	if !ok {
		t.Fatalf("the Firebase plan grants no signJwt to the mobile account (%s): the service cannot mint the custom token a pairing hands a phone, and the endpoints it serves answer refusals", grantAddr)
	}
	wantPath := "projects/" + p.firebaseProject + "/serviceAccounts/" + deviceSignerAccountID + "@" + p.firebaseProject + ".iam.gserviceaccount.com"
	if got := afterStr(grant, "service_account_id"); got != wantPath {
		t.Errorf("%s is on the account %q, want the device-only signer %q: signJwt on any other account of this run makes the public service that account — the budget signer's own reach included, which mints the tokens of every run", grantAddr, got, wantPath)
	}
	if got, want := afterStr(grant, "role"), "projects/"+p.firebaseProject+"/roles/"+deviceSignerRoleID; got != want {
		t.Errorf("%s grants the role %q, want %q: the permission is iam.serviceAccounts.signJwt and nothing else, and the role is a literal of the Firebase project's own custom role", grantAddr, got, want)
	}
	if got := afterStr(grant, "member"); got != p.mobileMember() {
		t.Errorf("%s is granted to %q, want the mobile runtime account %q — the account the INSTALLATION's plan creates and hands to this root as var.mobile_account, which is why the same address is read off two plans of two roots here", grantAddr, got, p.mobileMember())
	}
	// The Firebase root grants the new account nothing, which is the other half
	// of "device-only": every row of the Firebase root that names it is the
	// account resource itself and the ONE grant above, which is on the account
	// rather than held by it.
	for addr, row := range fb {
		if strings.Contains(row.json(), deviceSignerAccountID) && addr != signerAddr && addr != grantAddr {
			t.Errorf("the Firebase root's %s names %s: the device-only signer is granted no role of its own — what the mobile service may do with it is the one signJwt grant above, and anything else here is reach nobody announced", addr, deviceSignerAccountID)
		}
	}
}

// The runtime account's project-level reach: the unchanged custom role
// fugaroCredentialsDatabase, on the Firebase project the database lives in
// (O1). That is the whole of it — no second project role, on no other project,
// and no other kind of grant that is not the signJwt above.
func TestCredentialsMobileAccountHoldsTheDatabaseRoleOnly(t *testing.T) {
	p := credentialsMobilePlanOf(t)
	var grants []string
	for addr, row := range p.rows {
		if !strings.HasSuffix(row.Type, "_iam_member") {
			continue
		}
		if afterStr(row, "member") == p.mobileMember() {
			grants = append(grants, addr)
		}
	}
	slices.Sort(grants)
	const dbAddr = "module.installation.google_project_iam_member.mobile_database[0]"
	// The installation plan's WHOLE reach for this account, and it is one row:
	// the database binding below. The signJwt on the device-only signer is in
	// the Firebase root's plan (TestCredentialsMobileSignerIsInTheFirebaseProject
	// above), because that root is where the signer is created and `fugaro
	// update` applies this one first — a row here would name an account the
	// Firebase stage had not created yet. testdata/golden/firebase-mobile.plan.json
	// is the other half, and the two plans' one shared string is the member.
	want := []string{dbAddr}
	if !slices.Equal(grants, want) {
		t.Fatalf("the installation plan grants the mobile account %v, want exactly %v: its reach is the database role O1 announced and, in the Firebase root's own plan, the signJwt on the device-only signer — and a further grant here, a project role, a bucket, a secret or an invoker on either service, is reach nobody priced for the one endpoint anyone on the internet may call", grants, want)
	}
	db, ok := p.rows[dbAddr]
	if !ok {
		t.Fatalf("the plan grants the mobile account no %s: the service writes the device nodes under the rules' floor and reads the kill switches, so without this binding it answers a refusal to every pairing (O1)", dbAddr)
	}
	if got, wantRole := afterStr(db, "role"), "projects/"+p.firebaseProject+"/roles/"+credentialsDatabaseRoleID; got != wantRole {
		t.Errorf("%s grants the role %q, want %q: the database role is declared in the Firebase project and the binding spells it literally, so the plan says which role it is", dbAddr, got, wantRole)
	}
	if got := afterStr(db, "project"); got != p.firebaseProject {
		t.Errorf("%s is in project %q, want the Firebase project %q, where the database and the role both live", dbAddr, got, p.firebaseProject)
	}
	// The private service's binding is not what was read: this account's role is
	// a SECOND binding of the same role, and both members are the two services'
	// own accounts. The count is the fact that tells "a second service, holding
	// what the first holds" apart from "the first service, granted twice".
	var members []string
	for addr, row := range p.rows {
		if row.Type != "google_project_iam_member" || afterStr(row, "role") != "projects/"+p.firebaseProject+"/roles/"+credentialsDatabaseRoleID {
			continue
		}
		members = append(members, addr+" = "+afterStr(row, "member"))
	}
	slices.Sort(members)
	wantMembers := []string{
		"module.installation.google_project_iam_member.credentials_database[0] = serviceAccount:fugaro-credentials@" + p.project + ".iam.gserviceaccount.com",
		dbAddr + " = " + p.mobileMember(),
	}
	slices.Sort(wantMembers)
	if !slices.Equal(members, wantMembers) {
		t.Errorf("the plan's %s bindings are %v, want the two services' own accounts (%v): the role is unchanged and the mobile account is a second holder of it, never a second role", credentialsDatabaseRoleID, members, wantMembers)
	}
}

// firebaseGoldenRows is a committed Firebase golden plan's rows by address —
// a real plan of the Firebase root, whose project is aurora-fp in the fixture
// the credentials fixture names too (internal/infra/testdata/firebase.tfvars.json).
// It is read as a plan rather than as the module's text because "the signer is
// in the Firebase project" is a fact of the plan the Firebase root applies.
//
// name is a golden of its own and not always "firebase": the signer and its one
// grant exist only when the installation hands the runtime account over as
// mobile_account, so firebase-mobile is the plan that carries them — made by
// scripts/gen-golden-plans.sh from the same fixture plus that one key, and the
// plan a previous-version installation's Firebase project converges to holds
// neither (TestMobileServiceIsOffUntilTheSwitchIsOn below).
func firebaseGoldenRows(t *testing.T, name string) map[string]plannedResource {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("tf", "testdata", "golden", name+".plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The document is decoded the way every other plan read in this package is
	// (tf.Plan, whose UnmarshalJSON keeps the top-level keys beside the fields):
	// the attributes live under change.after, so a struct with an `after` tag of
	// its own would read every row as empty and every assertion below as a pass
	// on nothing.
	var plan tf.Plan
	if err := json.Unmarshal(b, &plan); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]plannedResource, len(plan.ResourceChanges))
	for _, rc := range plan.ResourceChanges {
		out[rc.Address] = plannedResource{
			Address: rc.Address, Type: rc.Type, Actions: rc.Change.Actions,
			After: rc.Change.After, Importing: rc.Change.Importing,
		}
	}
	return out
}

// TestMobileServiceIsOffUntilTheSwitchIsOn is the update's own promise, and the
// convergence test the two switches owe the release that carries them: an
// installation that has not opted in — which is every installation `fugaro
// update` reaches, because the binary writes no key for either switch — plans
// NOTHING new, in both roots, on the first update after this change. The mobile
// service, its account, its two bindings, the device-only signer and its signJwt
// are all opt-in facts; a row of any of them in a default plan is a resource an
// installation that never asked for a public endpoint acquires, and the public
// endpoint is the one F31 exists to prevent.
//
// The four assertions read the two facts from both ends. The tfvars the binary
// writes carry neither key — the installation root's own tfvars for
// enable_mobile_service, and the Firebase root's for mobile_account — so the
// switches are reachable only by an operator's hand-edited file this release,
// and the release that lands the mode (I1) is the one that gives them a writer.
// The two default PLANS then carry no mobile row at all: the installation plan
// of the very fixture the mobile golden is made from, and the committed Firebase
// golden whose tfvars have no mobile_account.
func TestMobileServiceIsOffUntilTheSwitchIsOn(t *testing.T) {
	spec := credentialsMobileSpecOf(t)
	vars, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(vars), enableMobileServiceVar) {
		t.Errorf("the installation tfvars the binary writes carry %s: the module's switch is off by default for a reason — a released credentials image runs no `mobile` mode yet, so a service started with it never becomes ready and the apply that created it fails (mobile_service.tf's header, and internal/cli's TestMobileServiceIsOffUntilThisBinaryRunsIt reads the argv against the command tree)", enableMobileServiceVar)
	}
	fs, err := Firebase(spec, FirebaseInputs{FP: spec.CredentialsService.FirebaseProject, Admins: []string{"user:owner@example.com"}, BudgetAdmins: []string{"user:extra@example.com"}, HistoryAccount: "fugaro-history@" + spec.Project + ".iam.gserviceaccount.com"})
	if err != nil {
		t.Fatal(err)
	}
	fbVars, err := FirebaseVars(fs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(fbVars), "mobile_account") {
		t.Errorf("the Firebase tfvars the binary writes carry mobile_account: the device signer and the one signJwt on it exist exactly when an installation hands its runtime account over, and `fugaro update` hands nothing over until the release that turns the switch on in the same change (modules/firebase/iam.tf)")
	}

	// The installation plan of the fixture the mobile golden is made from, with
	// the switch NOT written: no row of the opt-in is in it, and nothing in it
	// names the runtime account.
	_, rows := installationPlan(t, spec)
	for _, rc := range rows {
		if strings.Contains(rc.Address, ".mobile") || strings.Contains(rc.json(), mobileAccountID) {
			t.Errorf("the plan of an installation that wrote no enable_mobile_service holds %s: the mobile service, its account, its public binding and its database binding are four resources an installation acquires only by asking for them, and the first update after this change must plan exactly what it planned before", rc.Address)
		}
	}

	// ... and the Firebase root's own default golden, whose tfvars have no
	// mobile_account: no device signer, and no row naming the runtime account.
	for addr, row := range firebaseGoldenRows(t, "firebase") {
		if strings.Contains(addr, "device_signer") || strings.Contains(row.json(), mobileAccountID) {
			t.Errorf("the Firebase golden of an installation that handed no account over holds %s: the device-only signer and the signJwt on it are created exactly when var.mobile_account is set, so a previous-version installation's Firebase project plans neither", addr)
		}
	}
}

// enableMobileServiceVar is the module's and the root's switch, spelled once:
// the tfvars key, the variable name and the local's name are one word, and this
// test is about the key the binary writes.
const enableMobileServiceVar = "enable_mobile_service"
