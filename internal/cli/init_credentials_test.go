package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// The credentials stage (docs/credentials-service, D146, D147; plan task
// C1.20). It is the owner's step and it is behind --provision-credentials:
// what it turns on is the one Cloud Run service that reads every model
// credential the installation holds, so --yes never covers it, its
// confirmation is the project's name typed at a real terminal, and a run
// without the credsRules marker of C1.3 is refused before anything is
// planned.
//
// The rig is an images rig: a released CLI version, the release's credentials
// image published in the fake source under its own "-pinned" tag (so the
// stage's resolve-and-verify step answers with a digest the destination
// registry serves, as the remote repository does), and the installation's
// budget database carrying the marker.

// credentialsStageRig is the images rig plus the credentials service's own
// pieces: the released image, the budget backend and its marker.
func credentialsStageRig(t *testing.T) *imagesRig {
	t.Helper()
	r := newImagesRig(t, "1.2.3")
	// The released image, and the nightly-immune "-pinned" tag a release
	// publishes beside the plain version tag.
	r.src.publish("dimipaun/fugaro-credentials", "1.2.3")
	r.src.tags["dimipaun/fugaro-credentials:1.2.3-pinned"] = r.src.tags["dimipaun/fugaro-credentials:1.2.3"]
	// The database the stage reads the marker from: a project config with a
	// budget backend is what init --firebase writes.
	r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: "+r.db.URL+" }\n")
	r.db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
	return r
}

// credentialsDigest is the digest the fake source published the credentials
// image under: what the stage's resolve-and-verify step returns.
func (r *imagesRig) credentialsDigest() string {
	return r.src.tags["dimipaun/fugaro-credentials:1.2.3"]
}

// credentialsRef is the reference the stage must write for
// credentials_service.image: the remote repository's path, the version as a
// tag, and the verified digest.
func (r *imagesRig) credentialsRef() string {
	return "us-east5-docker.pkg.dev/" + initProject + "/" + infra.GhcrRegistry +
		"/dimipaun/fugaro-credentials:1.2.3@" + r.credentialsDigest()
}

// A coding agent's session applies nothing, and the credentials service is
// the last stage --yes could ever reach: the run stops at the stage, with the
// agent's own refusal and the typed route, and NOTHING of the stage runs --
// no marker read, no digest resolved, no tfvars written.
//
// This is the RED STEP of the task (plan C1.20).
func TestInitCredentialsStageRefusedInAgent(t *testing.T) {
	r := credentialsStageRig(t)
	// Everything before the credentials stage is done, so the run reaches it:
	// the refusal below can only be this stage's.
	r.setPlanAt(t, "installation")
	t.Setenv("CLAUDECODE", "1")
	out, errOut, err := executeStdin(t, "", "init", "--yes", "--provision-credentials")
	if err == nil {
		t.Fatalf("a coding agent's session applied the credentials service:\n%s%s", out, errOut)
	}
	s := out + errOut
	if !strings.Contains(s, initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the agent was not refused with the agent's own sentence: %v\n%s", err, s)
	}
	// The refusal is THIS stage's: the loop reached it, with every stage before
	// it done, so the run is not stopping at an earlier one whose refusal would
	// read the same. An agent plans a self-confirming engine read-only, and the
	// installation root's plan is empty here, which is what makes it Done.
	if !strings.Contains(s, "[done]") || !strings.Contains(s, "installation") {
		t.Errorf("the installation stage was not done before the credentials stage:\n%s", s)
	}
	if !strings.Contains(s, "[needs you]  credentials") {
		t.Errorf("the run did not reach the credentials stage, so this is another stage's refusal:\n%s", s)
	}
	if r.resolveCalls != 0 {
		t.Errorf("the stage resolved a digest in a coding agent's session (%d calls)", r.resolveCalls)
	}
	if _, ok := r.tfvars(t)["credentials_service"]; ok {
		t.Errorf("the stage wrote credentials_service in a coding agent's session: %v", r.tfvars(t)["credentials_service"])
	}
	if applies := r.ran(t, "apply"); len(applies) != 0 {
		t.Errorf("a coding agent's session applied %v", applies)
	}
}

// credentialsOutputs makes the installation root's `terraform output -json`
// answer carry the credentials service the module publishes once the opt-in
// is in (deploy/terraform/gcp/roots/installation/outputs.tf): the stage
// reads it back, and the repository roots read the same object from the
// installation's outputs.
func credentialsOutputs(t *testing.T, r *imagesRig) {
	t.Helper()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": map[string]any{"name": infra.CredentialsServiceName, "region": "us-east5"},
	})}
	r.save(t)
}

// --yes never covers the stage. It is registered noYes in initflow's order,
// so with --yes and no terminal the loop does not even enter the stage: it
// reports the step left for the owner, naming the flag and the sentence to
// type. At a terminal the stage asks its own confirmation, which --yes cannot
// answer either (nothing typed is a refusal, and nothing is applied).
//
// The count of applies throughout is the installation stage's own: the
// credentials stage never applies here, so a stage that ran would make it two.
func TestInitCredentialsStageNotCoveredByYes(t *testing.T) {
	if initflow.YesCovers(initflow.Credentials) || initflow.RunCovers(initflow.Credentials) {
		t.Fatal("--yes covers the credentials stage: it is the one identity that reads every model credential, and the opt-in is the owner's own step")
	}

	// --yes, no terminal: left for the owner.
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation")
	out, errOut, err := executeStdin(t, "", "init", "--yes", "--provision-credentials")
	s := out + errOut
	if err == nil || !strings.Contains(err.Error(), "left for you") {
		t.Fatalf("err = %v, want the step left for the owner\n%s", err, s)
	}
	if !strings.Contains(s, "left for you (credentials, prompt)") {
		t.Errorf("the step was not reported as left for the owner:\n%s", s)
	}
	if !strings.Contains(s, "type the project's name at the credentials service's confirmation") {
		t.Errorf("the step left for the owner does not say what to type:\n%s", s)
	}
	if !strings.Contains(s, "--provision-credentials") {
		t.Errorf("the route left for the owner does not name the flag:\n%s", s)
	}
	if _, ok := r.tfvars(t)["credentials_service"]; ok {
		t.Errorf("--yes turned the service on anyway: %v", r.tfvars(t)["credentials_service"])
	}

	// --yes at a terminal: the stage asks, and --yes is not an answer.
	r2 := credentialsStageRig(t)
	r2.setPlanAt(t, "installation")
	fakeTerminal(t)
	out, errOut, err = executeStdin(t, "", "init", "--yes", "--provision-credentials")
	s = out + errOut
	if err == nil {
		t.Fatalf("--yes stood in for the typed confirmation:\n%s", s)
	}
	if !strings.Contains(s, "Type "+initProjectName+" to confirm") {
		t.Errorf("the stage did not ask for the project's name at a terminal:\n%s", s)
	}
	if !strings.Contains(s, "nothing was applied") {
		t.Errorf("nothing typed was not a refusal:\n%s", s)
	}
	if _, ok := r2.tfvars(t)["credentials_service"]; ok {
		t.Errorf("nothing typed turned the service on: %v", r2.tfvars(t)["credentials_service"])
	}

	// --plan-only shows the plan and changes nothing: the key the stage wrote
	// to plan is taken back out again, so merely looking at the plan cannot
	// leave the opt-in where the next ordinary init would apply it.
	//
	// The stage's own plan ran — it resolved the image, which nothing else
	// here does — and the tfvars carry no key afterwards: written to plan
	// with, taken back out. The installation root's plan is empty in this run
	// on purpose: a waiting earlier stage would leave this stage "after" it,
	// and the loop plans a stage only once nothing before it waits.
	r3 := credentialsStageRig(t)
	r3.setPlanAt(t, "installation")
	typedThroughYes(t)
	fakeTerminal(t)
	out, errOut, err = executeStdin(t, initProjectName+"\n", "init", "--plan-only", "--provision-credentials")
	s = out + errOut
	if r3.resolveCalls == 0 {
		t.Errorf("the plan-only run never planned the credentials stage:\n%s (err %v)", s, err)
	}
	if !strings.Contains(s, "[done]       credentials") {
		t.Errorf("the credentials stage was not planned to a still-empty plan:\n%s (err %v)", s, err)
	}
	if _, ok := r3.tfvars(t)["credentials_service"]; ok {
		t.Errorf("--plan-only left the opt-in in the tfvars: %v", r3.tfvars(t)["credentials_service"])
	}
	if applies := r3.ran(t, "apply"); len(applies) != 0 {
		t.Errorf("--plan-only applied: %v", applies)
	}
}

// A run that cannot show the installation's rules carry the credentials
// generation is refused BEFORE anything is planned: the tfvars keep no
// credentials_service, no digest is resolved, and nothing is applied. The
// refusal names the marker and the one command that writes it, the same
// remedy a cascade launch names (C1.3, C1.19).
func TestInitCredentialsStageRefusedWithoutCredsMarker(t *testing.T) {
	// Absent, and present but older than this binary's generation. A database
	// that cannot answer at all is the read's own error, covered elsewhere.
	for name, gen := range map[string]any{
		"no marker at all":    nil,
		"an older generation": rules.CredsRulesVersion - 1,
	} {
		t.Run(name, func(t *testing.T) {
			r := credentialsStageRig(t)
			r.db.Set(rules.PathCredsRulesMarker(), gen) // nil is an absent node
			r.setPlanAt(t, "installation")
			credentialsOutputs(t, r)
			typedThroughYes(t)
			fakeTerminal(t)
			out, errOut, err := executeStdin(t, initProjectName+"\n", "init", "--yes", "--provision-credentials")
			s := out + errOut
			if err == nil {
				t.Fatalf("the stage ran without the credentials rules marker:\n%s", s)
			}
			for _, want := range []string{rules.PathCredsRulesMarker(), "fugaro init --firebase", credsServiceSince} {
				if !strings.Contains(s, want) {
					t.Errorf("the refusal lacks %q:\n%s", want, s)
				}
			}
			if r.resolveCalls != 0 {
				t.Errorf("a digest was resolved before the rules gate (%d calls)", r.resolveCalls)
			}
			if _, ok := r.tfvars(t)["credentials_service"]; ok {
				t.Errorf("the tfvars were written before the rules gate: %v", r.tfvars(t)["credentials_service"])
			}
			if applies := r.ran(t, "apply"); len(applies) != 1 {
				// The one apply is the installation stage's own; the
				// credentials stage applied nothing (a stage that ran would
				// make it two).
				t.Errorf("the stage applied without the marker: %v", applies)
			}
		})
	}
	// A marker at or above this binary's generation is admitted: the gate is
	// "older than the children", never "not exactly this release".
	r := credentialsStageRig(t)
	r.db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion+1)
	r.setPlanAt(t, "installation")
	credentialsOutputs(t, r)
	typedThroughYes(t)
	fakeTerminal(t)
	if out, errOut, err := executeStdin(t, initProjectName+"\n", "init", "--yes", "--provision-credentials"); err != nil {
		t.Fatalf("a newer marker was refused: %v\n%s%s", err, out, errOut)
	}
	if r.resolveCalls == 0 {
		t.Error("a marker at this generation's level or above still refused the run")
	}
}

// The image the stage writes is the released one pinned BY DIGEST: the value
// in credentials_service.image is the reference the signature was verified
// for and the digest was read back through the remote repository at, never
// the tag alone. The module's own validation refuses a tag at plan time
// (C1.18), so a stage that wrote one would be refused by its own plan — but
// the reason is not tidiness: the service runs as the account that reads
// every credential in the bundle, while every operator of the project holds
// roles/artifactregistry.writer on the registry the image comes from.
//
// The tag-only form is refused before anything is applied, which the
// development build case below shows: there is no released image to resolve,
// so there is no reference to write at all.
func TestInitCredentialsStageFillsTheDigest(t *testing.T) {
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation")
	credentialsOutputs(t, r)
	typedThroughYes(t)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, initProjectName+"\n", "init", "--yes", "--provision-credentials")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	svc, _ := r.tfvars(t)["credentials_service"].(map[string]any)
	if svc == nil {
		t.Fatalf("the apply wrote no credentials_service:\n%s", out)
	}
	image, _ := svc["image"].(string)
	if image != r.credentialsRef() {
		t.Errorf("credentials_service.image = %q, want %q (the verified digest read back through the remote repository)", image, r.credentialsRef())
	}
	if !strings.HasSuffix(image, "@"+r.credentialsDigest()) {
		t.Errorf("the image is not pinned by the verified digest: %q", image)
	}
	if svc["account_id"] != infra.CredentialsAccountID || svc["service"] != infra.CredentialsServiceName {
		t.Errorf("credentials_service = %v, want the account and service names", svc)
	}
	if applies := r.ran(t, "apply"); len(applies) != 2 {
		// The installation stage's own apply, and this stage's over the same
		// root with credentials_service now in the tfvars.
		t.Errorf("applies = %v, want the installation root twice (the installation stage's and this one's)", applies)
	}
	if r.arCalls == 0 {
		t.Error("the digest was written without being read back through the remote repository")
	}
	if !strings.Contains(out, infra.CredentialsServiceName) || !strings.Contains(out, image) {
		t.Errorf("the stage did not report what it turned on:\n%s", out)
	}

	// A development build has no released image: refused before anything is
	// written or applied, and the refusal says why.
	dev := newImagesRig(t, "dev")
	dev.setPlanAt(t, "installation")
	dev.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: "+dev.db.URL+" }\n")
	dev.db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
	typedThroughYes(t)
	fakeTerminal(t)
	out, errOut, err = executeStdin(t, initProjectName+"\n", "init", "--yes", "--provision-credentials")
	if err == nil {
		t.Fatalf("a development build turned the credentials service on:\n%s%s", out, errOut)
	}
	if !strings.Contains(out+errOut, "development build") {
		t.Errorf("the refusal does not say there is no released image:\n%s", out+errOut)
	}
	if _, ok := dev.tfvars(t)["credentials_service"]; ok {
		t.Error("a development build wrote credentials_service")
	}
	if applies := dev.ran(t, "apply"); len(applies) != 1 {
		// Again the installation stage's own: a development build's run adds
		// no apply of its own.
		t.Errorf("a development build applied: %v", applies)
	}
}

// credsRefreshYAML is acme/app's fugaro.yaml with the one value the drift
// note's credentials clause is about: agent.auth: cascade, the mode whose
// credential comes from the installation's credentials service at call time
// (C1.1 offers it, C1.19 refuses a launch without the service).
const credsRefreshYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: cascade }\nworkflows:\n" +
	"  app: { commands: { build: sh build.sh, test: sh test.sh } }\n" +
	"  api: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// upgradeRigWithCascade is upgradeRigWithGitHubAppID's rig for a checkout of
// that file: jobConfigCopyNote calls infra.Workflow and openCloudFor
// directly, unstubbed, so it needs a real repository entry and a runs bucket
// with the installation's project marker.
func upgradeRigWithCascade(t *testing.T) *anchorModeRig {
	t.Helper()
	fakeTerraform(t)
	upgradeEnv(t, "0.5.1")
	r := newAnchorModeRig(t, credsRefreshYAML, false)
	r.appendConfig(t, "  acme/app: { provider: github, workflows: [app], github_app_id: \"12345\" }\n")
	runs := t.TempDir()
	r.appendConfig(t, "bucket_url: file://"+runs+"\n")
	marker, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runs, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "fugaro", "project.json"), marker, 0o644); err != nil {
		t.Fatal(err)
	}
	return r
}

// TestUpgradeNoteNamesTheStage (plan C1.20): a version upgrade is where the
// operator of a cascade repository learns that two OWNER-RUN steps stand
// between the file they just edited and a run that works — the note names
// both, in the order they have to run, and never suggests that upgrading
// does any of it.
//
// The clause is orthogonal to the drift the rest of the note is about, so it
// is owed even when every deployed job's env copy matches (the case here),
// and that is what makes it its own reason to speak: without it the whole
// note would stay silent for exactly the operators who need it.
//
// Mutation (verified, run and restored): make jobConfigCopyCredentialsNote
// return "": this test fails, finding no clause at all.
func TestUpgradeNoteNamesTheStage(t *testing.T) {
	r := upgradeRigWithCascade(t)
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil)
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"note: the deployed job's environment matches the local project config",
		"agent.auth: cascade",
		"must mount no model credential at all",
		"fugaro init --provision-credentials",
		"fugaro init --firebase",
		"fugaro init --repo .",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the note lacks %q:\n%s", want, out)
		}
	}
	// The service is the one identity that reads every model credential, so
	// the note must not read as something an upgrade does on its own.
	if strings.Contains(out, "agent.auth: oauth") {
		t.Errorf("the note reports the wrong auth mode:\n%s", out)
	}
}

// The clause is silent for every other auth: a repository whose jobs mount
// their own credential has nothing to be told about the service, and a note
// that named it anyway would be noise the operator learns to skip.
func TestUpgradeNoteCredentialsClauseIsSilentWithoutCascade(t *testing.T) {
	for _, auth := range []string{"", "oauth", "api-key"} {
		cfg := &config.Config{}
		cfg.Agent.Auth = auth
		if got := jobConfigCopyCredentialsNote(cfg); got != "" {
			t.Errorf("the clause speaks for agent.auth %q: %q", auth, got)
		}
	}
	cfg := &config.Config{}
	cfg.Agent.Auth = config.AuthCascade
	if got := jobConfigCopyCredentialsNote(cfg); !strings.Contains(got, "fugaro init --provision-credentials") {
		t.Errorf("the clause does not name the stage for %s: %q", config.AuthCascade, got)
	}
}

// The flag is refused where it could only mislead, exactly as --provision-batch
// is: with --repo (another root's run) and with every flag that only reads or
// only stops, so a run that named --provision-credentials always means the
// apply it names. The check is asked directly: a --repo run resolves the
// checkout before it validates anything.
func TestProvisionCredentialsFlagIsRefusedWhereItCannotApply(t *testing.T) {
	for _, o := range []*initOptions{{provisionCredentials: true, repo: true}, {provisionCredentials: true, forget: true},
		{provisionCredentials: true, printVars: true}, {provisionCredentials: true, configOnly: true},
		{provisionCredentials: true, publishConfig: true}, {provisionCredentials: true, anchor: true}} {
		err := o.check()
		if err == nil || !strings.Contains(err.Error(), "--provision-credentials") {
			t.Errorf("%+v: err = %v, want it refused", o, err)
		}
	}
}

// The two owner-run applies do NOT exclude one another, and the flag's help
// says so: they are applies of different roots (the installation's credentials
// service, the Batch backend's own state), each with its own typed
// confirmation, so naming both means both — a refusal would be a rule with no
// reason behind it, and a help text that claimed one would be a rule the code
// does not have.
func TestProvisionCredentialsAndBatchAreIndependentApplies(t *testing.T) {
	o := &initOptions{provisionBatch: true, provisionCredentials: true}
	if err := o.check(); err != nil {
		t.Fatalf("--provision-batch --provision-credentials was refused: %v", err)
	}
	// ...and a read-only flag beside them is still refused, naming both
	// applies rather than only one of them.
	both := &initOptions{provisionBatch: true, provisionCredentials: true, printVars: true}
	err := both.check()
	if err == nil || !strings.Contains(err.Error(), "--provision-batch and --provision-credentials") {
		t.Errorf("err = %v, want the refusal to name both applies", err)
	}
	// The --repo path refuses it as the single flag does (checkRepo).
	ro := &initOptions{provisionCredentials: true, repo: true}
	if err := ro.checkRepo(); err == nil || !strings.Contains(err.Error(), "--provision-credentials") {
		t.Errorf("fugaro init --repo --provision-credentials: err = %v, want a refusal naming the flag", err)
	}
}
