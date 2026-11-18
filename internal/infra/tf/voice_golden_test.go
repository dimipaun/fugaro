package tf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// voiceFixture is the voice case of the installation golden's tfvars
// (internal/infra/testdata/installation-voice.tfvars.json, the one
// scripts/gen-golden-plans.sh plans for
// testdata/golden/installation-voice.plan.json): the whole installation run
// with the voice hop ON beside the credentials service, so the hop's revision,
// its account, its one role binding and the one public binding a phone reaches
// it through are in a real plan.
//
// The fixture is the credentials golden's own document plus the hop's block,
// which is the point: the two services are one image and one run, and what the
// hop is configured with is the credentials service's own two members. So this
// struct reads those two facts out of the same document the module reads them
// from, and a test that compared the hop's environment against literals of its
// own would pass on an installation whose two blocks disagree.
type voiceFixture struct {
	Project       string `json:"project"`
	FugaroProject string `json:"fugaro_project"`
	Region        string `json:"region"`
	Credentials   struct {
		Image           string `json:"image"`
		FirebaseProject string `json:"firebase_project"`
		RTDBURL         string `json:"rtdb_url"`
		URL             string `json:"url"`
	} `json:"credentials_service"`
	Voice struct {
		AccountID string `json:"account_id"`
		Service   string `json:"service"`
		Image     string `json:"image"`
	} `json:"voice_service"`
}

func voiceGoldenFixture(t *testing.T) voiceFixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "installation-voice.tfvars.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f voiceFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestGoldenVoiceHopIsTheOneImageTheTwoOtherServicesRun is the pin the two
// services' blocks share: ONE release, ONE digest, three subcommands. The hop
// runs whatever var.voice_service.image names, and the module's own validation
// refuses a tag there for the credentials service's reason — the account reads
// the whole database instance and every operator may push to the registry —
// but the validation is the module's and this is the deployment's: two blocks
// that named different releases would run two binaries of one installation
// against one published policy node, and the release behind's reader refuses a
// node it cannot decode.
//
// Mutation (run, restore): point var.voice_service.image at another digest in
// the fixture and this fails, which is also what the cover's own case
// (voiceServiceChange) refuses on a hand-edited plan.
func TestGoldenVoiceHopIsTheOneImageTheTwoOtherServicesRun(t *testing.T) {
	f := voiceGoldenFixture(t)
	// The golden is only worth reading if the fixture is the shape it claims:
	// the hop pinned to the same reference as the service beside it, and the
	// two facts the module reads out of that block present.
	if f.Voice.Image != f.Credentials.Image {
		t.Fatalf("the voice fixture pins the hop to %q and the credentials service to %q: this golden exists to hold the two to ONE release, so the fixture must carry the same reference", f.Voice.Image, f.Credentials.Image)
	}
	if f.Credentials.FirebaseProject == "" || f.Credentials.RTDBURL == "" {
		t.Fatalf("the voice fixture names no Firebase project or no database: the hop's two environment values come from the credentials service's block (voice.tf), so a fixture that carries neither could not tell them from empty strings: %v", f.Credentials)
	}

	p := goldenPlan(t, "installation-voice")
	svc := resource(t, p, "module.installation.google_cloud_run_v2_service.voice[0]")
	tpl := objects(at(svc.Change.After, "template"))
	if len(tpl) != 1 {
		t.Fatalf("%s has %d templates, want one", svc.Address, len(tpl))
	}
	cs := objects(tpl[0]["containers"])
	if len(cs) != 1 {
		t.Fatalf("%s has %d containers, want one", svc.Address, len(cs))
	}
	if got := str(cs[0]["image"]); got != f.Voice.Image {
		t.Errorf("the hop's container runs %q, want %q (the fixture's own pin)", got, f.Voice.Image)
	}
	// The pin is a DIGEST and never a tag: an operator may push a tag, and this
	// account reads every node of the installation's database.
	if _, digest, ok := cutDigest(str(cs[0]["image"])); !ok || digest == "" {
		t.Errorf("the hop's image %q is not pinned by digest", str(cs[0]["image"]))
	}
	if got := strList(cs[0]["args"]); !slices.Equal(got, voiceServiceArgs) {
		t.Errorf("the hop's container runs args %v, want %v: the image's default command serves the credentials service, so the args are the one thing that tells the two apart", got, voiceServiceArgs)
	}
	if got := strList(cs[0]["command"]); len(got) != 0 {
		t.Errorf("the hop's container sets command %v, want none: the image's entrypoint is the released binary and `args` replaces its command", got)
	}
}

// cutDigest splits an image reference at its @sha256: digest, ok false when it
// carries none.
func cutDigest(image string) (tag, digest string, ok bool) {
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] == '@' {
			return image[:i], image[i+1:], true
		}
	}
	return image, "", false
}

// TestGoldenVoiceHopCarriesItsThreeNamesAndNoOther is C1.14's rule at the hop's
// scale: the process refuses to bind a port unless the names it needs are there
// and well formed (internal/cli's voiceServiceConfig), and the module renders
// exactly those — so a name the Go side computes and the module drops is a
// revision that never serves, and a name the module adds is a value that
// reached the process without the deployment deciding it.
//
// The first two values are the credentials service's own (voice.tf reads
// var.credentials_service), which is what makes this one fact and not two: the
// hop verifies ID tokens minted for the installation's Firebase project and
// reads that project's database, and a second copy of either could be pulled
// apart from the block the credentials service, the runs and the policy read.
// The third is P5.3.5's: the installation's own project, which is where the
// provider-key container lives — never the container's ID, which the process
// reads from its own constant (internal/backend/gcp's VoiceProviderKeysSecret),
// so no secret ID is an environment value of this tree. The service's own URL
// is deliberately NOT among them: the hop's callers are phones whose tokens are
// minted for the Firebase project, so there is no address to read back and no
// second apply.
func TestGoldenVoiceHopCarriesItsThreeNamesAndNoOther(t *testing.T) {
	f := voiceGoldenFixture(t)
	p := goldenPlan(t, "installation-voice")
	svc := resource(t, p, "module.installation.google_cloud_run_v2_service.voice[0]")

	got := map[string]string{}
	tpl := objects(at(svc.Change.After, "template"))
	if len(tpl) != 1 {
		t.Fatalf("%s has %d templates, want one", svc.Address, len(tpl))
	}
	cs := objects(tpl[0]["containers"])
	if len(cs) != 1 {
		t.Fatalf("%s has %d containers, want one", svc.Address, len(cs))
	}
	for _, e := range objects(cs[0]["env"]) {
		if refs := objects(e["value_source"]); len(refs) > 0 {
			t.Errorf("%s takes %s from a source rather than from the module: this service's environment is the module's own three values and nothing else", svc.Address, str(e["name"]))
			continue
		}
		got[str(e["name"])] = str(e["value"])
	}
	want := map[string]string{
		"FUGARO_FIREBASE_PROJECT": f.Credentials.FirebaseProject,
		"FUGARO_RTDB_URL":         f.Credentials.RTDBURL,
		"FUGARO_GCP_PROJECT":      f.Project,
	}
	if len(got) != len(want) {
		t.Errorf("the hop's revision carries %d environment names (%v), want %d (%v): a name the process does not read is a value the deployment did not decide", len(got), got, len(want), voiceServiceEnv)
	}
	for n, v := range want {
		if got[n] != v {
			t.Errorf("the hop's revision sets %s = %q, want %q (the credentials service's own member: the same run writes both blocks)", n, got[n], v)
		}
	}
}

// TestGoldenVoiceHopKeepsNoInstanceWarmAndAdmitsEveryoneOnPurpose is the two
// halves a reviewer of this feature actually weighs: the price of the hop and
// the reach of it.
//
// The count is zero at BOTH levels, and the golden is where the second one is
// visible: the provider computes the service-level block otherwise, and the
// managed gate prices a Cloud Run service by its floor — so a plan that stated
// only the template's would be refused as unpriceable, and one that stated a
// nonzero floor anywhere is an installation paying around the clock for a
// process that runs when a phone speaks.
//
// The binding is allUsers on the hop's own service, the one resource in this
// tree whose member is nobody: a device presents a Firebase ID token and no
// Google identity, so there is no member an invoker binding could name and the
// process's own VerifyDevice is the gate (voice.tf, SECURITY.md). What this
// holds is that the plan carries it exactly where the module writes it — the
// hop's service, roles/run.invoker, this run's project. Every other shape of
// it is refused by deploy/terraform's
// TestAllUsersIsRefusedOnEveryOtherResource, which names the one path and the
// one block an allUsers member is admitted on; init's cover carries no such
// member at all (cover.go's toVoice, the front door's own state), so the
// update that carries this binding shows it as a finding and applies it
// rather than passing it as covered.
func TestGoldenVoiceHopKeepsNoInstanceWarmAndAdmitsEveryoneOnPurpose(t *testing.T) {
	f := voiceGoldenFixture(t)
	p := goldenPlan(t, "installation-voice")
	svc := resource(t, p, "module.installation.google_cloud_run_v2_service.voice[0]")

	if svc.Change.After["invoker_iam_disabled"] != false {
		t.Errorf("the hop's service states invoker_iam_disabled = %v, want false: with the IAM check off the endpoint would be public through a flag no reviewer reads instead of through the binding below", svc.Change.After["invoker_iam_disabled"])
	}
	for _, b := range []struct {
		what string
		path []string
	}{
		{"the service's", []string{"scaling"}},
		{"the template's", []string{"template", "scaling"}},
	} {
		blocks := objects(at(svc.Change.After, b.path...))
		if len(blocks) != 1 {
			t.Fatalf("%s plan carries %d scaling blocks, want one", b.what, len(blocks))
		}
		// The JSON number a plan writes is a float64: a count of 0 is 0.0, and
		// "is it zero" is the whole question — the gate prices the service by
		// this attribute and the provider computes it when the module omits it.
		if n, ok := blocks[0]["min_instance_count"].(float64); !ok || n != 0 {
			t.Errorf("%s minimum instance count is %v, want 0: the hop is a public endpoint whose work arrives with a phone's call, so an instance kept warm is a bill for an idle process", b.what, blocks[0]["min_instance_count"])
		}
	}

	bind := resource(t, p, "module.installation.google_cloud_run_v2_service_iam_member.voice_public[0]")
	if got := str(bind.Change.After["member"]); got != "allUsers" {
		t.Errorf("the hop's binding admits %q, want allUsers", got)
	}
	if got := str(bind.Change.After["role"]); got != "roles/run.invoker" {
		t.Errorf("the hop's public binding grants %q, want roles/run.invoker: the invoker bit is what lets a caller reach the container, and the container's handler is the gate", got)
	}
	if got, want := str(bind.Change.After["name"]), f.Voice.Service; got != want {
		t.Errorf("the hop's public binding is on the service %q, want %q (the fixture's own name for it)", got, want)
	}
	if got := str(bind.Change.After["project"]); got != f.Project {
		t.Errorf("the hop's public binding is in %q, want %q", got, f.Project)
	}
}

// TestGoldenVoiceHopReadsTheWholeDatabaseThroughOneRole is the grant, stated
// as the grant it is (P5.3.3's reviewer paragraph, SECURITY.md).
//
// Realtime Database IAM has no path-level scope: firebasedatabase.instances.get
// is a read of ANY node of the instance, which is why the hop's account holds
// the same one-permission role the front door already holds (fugaroStateRead,
// state_read.tf) rather than a second role that could only differ by holding
// the write that file exists to withhold. What narrows the process to the
// device node and the policy nodes is internal/voicesvc's own paths and not the
// grant, so this test holds the grant and nothing pretends otherwise: the plan
// must carry the one role, with the one permission RolePermissions pins, bound
// to the hop's own account and to no one else's.
func TestGoldenVoiceHopReadsTheWholeDatabaseThroughOneRole(t *testing.T) {
	f := voiceGoldenFixture(t)
	p := goldenPlan(t, "installation-voice")
	grant := resource(t, p, "module.installation.google_project_iam_member.voice_state_read[0]")

	account := f.Voice.AccountID + "@" + f.Project + ".iam.gserviceaccount.com"
	if got := str(grant.Change.After["member"]); got != "serviceAccount:"+account {
		t.Errorf("the database read is bound to %q, want %q: the hop's own account and no other", got, "serviceAccount:"+account)
	}
	// The role is declared in the FIREBASE project — the one the database
	// belongs to (D6) — and not in var.project, which is what state_read.tf's
	// own local names: the binding's project is the Firebase project of the
	// installation's two blocks, and the account is a member of the
	// installation's own project. The two differ in this fixture (proj-1234's
	// database is aurora-fp's), which is why the test reads both.
	if got, want := str(grant.Change.After["role"]), "projects/"+f.Credentials.FirebaseProject+"/roles/"+MCPStateReadRole; got != want {
		t.Errorf("the database read grants %q, want %q", got, want)
	}
	if got := str(grant.Change.After["project"]); got != f.Credentials.FirebaseProject {
		t.Errorf("the database read is bound in %q, want %q: the database belongs to the Firebase project (D6), which is where the role is declared", got, f.Credentials.FirebaseProject)
	}
	// The role is the ONE permission there is, and the plan's own account is
	// created by the same apply — the test that the account exists at all in
	// this plan, which is what makes the binding's member resolvable rather
	// than a name nothing created.
	sa := resource(t, p, "module.installation.google_service_account.voice[0]")
	if got := str(sa.Change.After["account_id"]); got != f.Voice.AccountID {
		t.Errorf("the plan creates the account %q, want %q", got, f.Voice.AccountID)
	}
	if got := RolePermissions[MCPStateReadRole]; !slices.Equal(got, MCPStateReadPermissions) {
		t.Errorf("RolePermissions[%s] = %v, want %v: the hop's whole reach is this role, and a permission added to it is reach added to a public endpoint", MCPStateReadRole, got, MCPStateReadPermissions)
	}
}

// TestGoldenVoiceHopHoldsTheProviderKeysAndNoOtherSecret is P5.3.5's grant read
// as the plan writes it, and the one test in this file that is about a
// credential rather than an address.
//
// The forwarding half gives this process the one secret it holds, and the plan
// is where the two halves of that statement are visible at once: ONE container,
// whose ID is the literal internal/backend/gcp's VoiceProviderKeysSecret carries
// (secrets.tf repeats it because Terraform cannot compute a ProjectSecretID),
// and ONE accessor on it, the hop's own account and no other member anywhere.
// A second binding of that account to any other container of this plan is the
// failure this exists for — the hop is public (the binding below), so a grant
// that reached, say, the credentials bundle would hand the installation's model
// keys to anyone who can call a service on the internet.
//
// And the container is EMPTY here, which is the other half of the design: a
// plan creates no version of it, so no provider key ever reaches Terraform, its
// state or this plan; the value is added by the person who holds it
// (secrets.tf's `gcloud secrets versions add` line), and until then the hop
// refuses a call whose key it cannot read rather than making one with an empty
// credential.
func TestGoldenVoiceHopHoldsTheProviderKeysAndNoOtherSecret(t *testing.T) {
	f := voiceGoldenFixture(t)
	p := goldenPlan(t, "installation-voice")
	sec := resource(t, p, "module.installation.google_secret_manager_secret.voice_provider_keys[0]")
	if got := str(sec.Change.After["secret_id"]); got != gcp.VoiceProviderKeysSecret {
		t.Errorf("the hop's provider-key container is %q, want %q: the process reads it by its own constant (internal/backend/gcp) beside the project its environment names, so the two spellings are one fact", got, gcp.VoiceProviderKeysSecret)
	}
	// The marks fugaro's discovery reads, and the one that must be ABSENT:
	// fugaro_secret is what `fugaro update` and `fugaro secrets ls
	// --project-wide` read to decide which logical secret of the project's set
	// a container holds, and this container is not one of those — with it,
	// discovery would union this very container into repo.project_secrets
	// beside this resource and the apply would plan the same ID twice.
	if got := str(at(sec.Change.After, "labels", "fugaro")); got != "managed" {
		t.Errorf("the hop's provider-key container is labelled fugaro=%q, want managed", got)
	}
	if got := str(at(sec.Change.After, "labels", "fugaro_project")); got != f.FugaroProject {
		t.Errorf("the hop's provider-key container is labelled fugaro_project=%q, want %q", got, f.FugaroProject)
	}
	if _, ok := at(sec.Change.After, "labels", "fugaro_secret").(string); ok {
		t.Errorf("the hop's provider-key container carries the fugaro_secret label: that mark makes discovery read it as one of the project's logical secrets, and the apply would then plan the same container twice (secrets.tf)")
	}

	account := "serviceAccount:" + f.Voice.AccountID + "@" + f.Project + ".iam.gserviceaccount.com"
	grant := resource(t, p, "module.installation.google_secret_manager_secret_iam_member.voice_provider_keys[0]")
	if got := str(grant.Change.After["secret_id"]); got != gcp.VoiceProviderKeysSecret {
		t.Errorf("the provider-key accessor is bound on %q, want %q", got, gcp.VoiceProviderKeysSecret)
	}
	if got := str(grant.Change.After["role"]); got != "roles/secretmanager.secretAccessor" {
		t.Errorf("the provider-key accessor grants %q, want roles/secretmanager.secretAccessor: the value is the whole of what the forwarding half may read", got)
	}
	if got := str(grant.Change.After["member"]); got != account {
		t.Errorf("the provider-key accessor is bound to %q, want %q: the service's own account and no other principal of this installation", got, account)
	}
	if conds := objects(grant.Change.After["condition"]); len(conds) != 0 {
		t.Errorf("the provider-key accessor carries a condition (%v), want none: Secret Manager scopes a binding per secret, so the container is the fence and a condition would fence nothing (secrets.tf)", conds)
	}

	// The whole of the account's Secret Manager reach, read off every binding
	// the plan writes rather than off the one address this test happened to
	// name: a second container bound to the hop in this plan is a grant nobody
	// wrote down.
	for _, rc := range p.ResourceChanges {
		if rc.Type != "google_secret_manager_secret_iam_member" {
			continue
		}
		if str(rc.Change.After["member"]) != account {
			continue
		}
		if got := str(rc.Change.After["secret_id"]); got != gcp.VoiceProviderKeysSecret {
			t.Errorf("%s binds the hop's account on the secret %q: those keys are its one credential, and every other container of this plan belongs to another part of the installation", rc.Address, got)
		}
	}
}

// TestTheVoiceHopIsInTheVoiceGoldenOnly is the opt-in's own guard, the shape
// TestTheCoordinatorIsInTheOptInGoldensOnly has for the front door: the hop is
// OFF by default, and a plan that carries its service without the variable is
// a public endpoint nobody asked for. Every other golden is a real plan of the
// same root, so this reads them.
func TestTheVoiceHopIsInTheVoiceGoldenOnly(t *testing.T) {
	for _, name := range []string{"installation", "installation-accounts", "installation-managed", "installation-credentials", "installation-review",
		"installation-adopt", "installation-coordinator", "installation-viewers"} {
		for _, rc := range goldenPlan(t, name).ResourceChanges {
			if strings.Contains(rc.Address, "voice") {
				t.Errorf("%s plans %s: the voice hop is OFF until var.voice_service is written, and no golden's tfvars pass it but the voice golden's", name, rc.Address)
			}
		}
	}
	// ... and the golden it must be in, so that a rename that took the block out
	// of the voice fixture cannot make this test pass by reading nothing.
	if !slices.ContainsFunc(goldenPlan(t, "installation-voice").ResourceChanges, func(rc ResourceChange) bool {
		return rc.Address == "module.installation.google_cloud_run_v2_service.voice[0]"
	}) {
		t.Fatal("the voice golden plans no hop, so the loop above read the wrong plans")
	}
}
