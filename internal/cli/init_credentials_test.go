package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
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
func credentialsStageRig(t *testing.T) *imagesRig { return credentialsStageRigAt(t, "1.2.3") }

// credentialsStageRigAt is credentialsStageRig for a CLI that is another
// release: the rig whose credentials image is published under version, which
// is what an upgrade BETWEEN two releases is tested with.
func credentialsStageRigAt(t *testing.T, version string) *imagesRig {
	t.Helper()
	r := newImagesRig(t, version)
	// The released image, and the nightly-immune "-pinned" tag a release
	// publishes beside the plain version tag.
	r.src.publish("dimipaun/fugaro-credentials", version)
	r.src.tags["dimipaun/fugaro-credentials:"+version+"-pinned"] = r.src.tags["dimipaun/fugaro-credentials:"+version]
	// The database the stage reads the marker from: a project config with a
	// budget backend is what update --firebase-project writes. Its firebase_project is
	// the one the credentials service's own environment carries
	// (FUGARO_FIREBASE_PROJECT) and its rtdb_url the one FUGARO_RTDB_URL
	// carries: infra.Credentials takes both from here, because the service
	// reads the installation's own database and has no default.
	r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+fpID+", rtdb_url: "+r.db.URL+" }\n")
	r.db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
	return r
}

// credentialsDigest is the digest the fake source published the credentials
// image under: what the stage's resolve-and-verify step returns.
func (r *imagesRig) credentialsDigest() string { return r.credentialsDigestAt("1.2.3") }

// credentialsDigestAt is credentialsDigest for another release.
func (r *imagesRig) credentialsDigestAt(version string) string {
	return r.src.tags["dimipaun/fugaro-credentials:"+version]
}

// credentialsRef is the reference the stage must write for
// credentials_service.image: the remote repository's path, the version as a
// tag, and the verified digest.
func (r *imagesRig) credentialsRef() string { return r.credentialsRefAt("1.2.3") }

// credentialsRefAt is credentialsRef for another release of the same source.
func (r *imagesRig) credentialsRefAt(version string) string {
	return "us-east5-docker.pkg.dev/" + initProject + "/" + infra.GhcrRegistry +
		"/dimipaun/fugaro-credentials:" + version + "@" + r.credentialsDigestAt(version)
}

// oldCredentialsRef is the reference an installation that opted in under an
// EARLIER release holds: that release's version in the tag and a digest the
// rig never published — an installation's own record, which is what makes an
// update have to resolve the running release's image rather than read one.
func oldCredentialsRef(version string) string {
	return "us-east5-docker.pkg.dev/" + initProject + "/" + infra.GhcrRegistry +
		"/dimipaun/fugaro-credentials:" + version + "@sha256:" + strings.Repeat("ab", 32)
}

// seedCredentialsService writes the installation root's tfvars as a run of an
// earlier release left them: an installation that has opted in, on that
// release's credentials image, with the entry's other facts exactly what the
// credentials stage writes (infra.Credentials).
func seedCredentialsService(t *testing.T, r *imagesRig, image string) {
	t.Helper()
	doc := map[string]any{"credentials_service": map[string]any{
		"account_id":       infra.CredentialsAccountID,
		"service":          infra.CredentialsServiceName,
		"image":            image,
		"bundle_secret":    "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358",
		"firebase_project": fpID,
		"rtdb_url":         r.db.URL,
		"job_accounts": []any{map[string]any{
			"provider": "bitbucket", "repo": "acme/sandbox", "workflow": "web"}},
		"url": credentialsServiceURL,
	}}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.root(), infra.VarsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// credentialsServiceEntry is the credentials_service entry the installation
// root's tfvars carry after a run.
func credentialsServiceEntry(t *testing.T, r *imagesRig) map[string]any {
	t.Helper()
	svc, _ := r.tfvars(t)["credentials_service"].(map[string]any)
	if svc == nil {
		t.Fatalf("the installation's tfvars carry no credentials_service: %v", r.tfvars(t))
	}
	return svc
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
	out, errOut, err := executeStdin(t, "", "setup", "--yes", "--provision-credentials")
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
//
// The URL is part of it, and it is what the stage's SECOND apply is for
// (fillAudience): the apply that creates the service cannot know it — Cloud
// Run assigns it, and Terraform refuses a resource that reads its own uri —
// so the revision carries the module's unassigned placeholder until this
// value comes back and the stage applies it.
func credentialsOutputs(t *testing.T, r *imagesRig) {
	t.Helper()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": map[string]any{"name": infra.CredentialsServiceName, "region": "us-east5", "url": credentialsServiceURL},
	})}
	r.save(t)
}

// credentialsServiceURL is the URL the module's output carries in these
// tests: the shape Cloud Run assigns (the service's name, the project
// number, the region), which is the audience its callers' ID tokens are
// minted for.
const credentialsServiceURL = "https://fugaro-credentials-123456789012.us-east5.run.app"

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
		t.Fatal("--yes covers the credentials stage: it is the identity the repository jobs' model credentials move to, and the opt-in is the owner's own step")
	}

	// --yes, no terminal: left for the owner.
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation")
	out, errOut, err := executeStdin(t, "", "setup", "--yes", "--provision-credentials")
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
	out, errOut, err = executeStdin(t, "", "setup", "--yes", "--provision-credentials")
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
	out, errOut, err = executeStdin(t, initProjectName+"\n", "setup", "--plan-only", "--provision-credentials")
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
			out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials")
			s := out + errOut
			if err == nil {
				t.Fatalf("the stage ran without the credentials rules marker:\n%s", s)
			}
			for _, want := range []string{rules.PathCredsRulesMarker(), "fugaro update --firebase-project", credsServiceSince} {
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
	if out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials"); err != nil {
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
	out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials")
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
	if applies := r.ran(t, "apply"); len(applies) != 3 {
		// The installation stage's own apply; this stage's over the same root
		// with credentials_service now in the tfvars; and the third, which
		// puts the service's own URL on the revision. The third is
		// fillAudience's and it is unavoidable: Cloud Run assigns the URL, so
		// the apply that CREATES the service cannot know the audience its
		// callers' ID tokens are minted for.
		t.Errorf("applies = %v, want the installation root three times (the installation stage's, this stage's, and the audience's)", applies)
	}
	if svc["url"] != credentialsServiceURL {
		t.Errorf("credentials_service.url = %v, want %s (the URL the installation's outputs carry): the audience is the service's own URL, never a value this stage composes — a revision whose audience is not its own URL admits no caller",
			svc["url"], credentialsServiceURL)
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
	out, errOut, err = executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials")
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
		"fugaro update --provision-credentials",
		"fugaro update --firebase-project",
		"fugaro update --repo .",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the note lacks %q:\n%s", want, out)
		}
	}
	// The service is the identity the repository jobs' model credentials move
	// to, so the note must not read as something an upgrade does on its own.
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
	if got := jobConfigCopyCredentialsNote(cfg); !strings.Contains(got, "fugaro update --provision-credentials") {
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

// A database that cannot ANSWER the marker read is refused exactly as one that
// answers "no marker" is (C1.20's review finding F2; mutation M07). "The read
// failed" is not "the rules carry the credentials children", and this read is
// the whole gate of the stage that turns on the one identity which reads every
// model credential the installation holds — so an unanswered read has to stop
// the run, and the refusal has to say which read it was so the owner can act.
//
// Value-free by construction: the refusal carries the node's PATH and the
// transport's own message, and no value from the database at all.
//
// Mutation (verified, run and restored): make the read's error non-fatal in
// requireCredsRules (drop the return and carry on as if the marker had been
// found): this test fails — the stage plans, resolves a digest and writes the
// tfvars.
func TestInitCredentialsStageRefusedWhenTheMarkerCannotBeRead(t *testing.T) {
	r := credentialsStageRig(t)
	// Only the marker's own read fails: RefusePath matches on the request
	// path, so every other read of the run (the installation's, the Firebase
	// root's) is untouched and the refusal below can only be this read's.
	r.db.RefusePath(rules.PathCredsRulesMarker(), 500, "INTERNAL", "", "the database answered nothing")
	r.setPlanAt(t, "installation")
	credentialsOutputs(t, r)
	typedThroughYes(t)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials")
	s := out + errOut
	if err == nil {
		t.Fatalf("the stage carried on when the credentials rules marker could not be read:\n%s", s)
	}
	if !strings.Contains(err.Error(), rules.PathCredsRulesMarker()) {
		t.Errorf("the refusal does not name the node that could not be read: %v\n%s", err, s)
	}
	if r.resolveCalls != 0 {
		t.Errorf("a digest was resolved before the rules gate answered (%d calls)", r.resolveCalls)
	}
	if _, ok := r.tfvars(t)["credentials_service"]; ok {
		t.Errorf("the tfvars were written when the rules gate could not be read: %v", r.tfvars(t)["credentials_service"])
	}
	if applies := r.ran(t, "apply"); len(applies) != 1 {
		// The one apply is the installation stage's own: a stage that ran
		// would make it two.
		t.Errorf("the stage applied when the rules gate could not be read: %v", applies)
	}
}

// The apply can finish and the installation's outputs still carry no
// credentials_service (C1.20's review finding M10). The whole of this stage's
// change is one key of a JSON document, and Terraform only WARNS about a
// tfvars key no variable declares: without this check a misspelled key would
// leave the plan empty, the apply with nothing to change, and the stage
// reporting a service that does not exist — while the repository roots, which
// read exactly this output, drop every job's model-secret accessor for a
// service nothing can call.
//
// The key STAYS: the apply ran, so the installation's state may already hold
// the service, and a tfvars without the key is a plan of its destruction.
//
// Mutation (verified, run and restored): delete the `outs.CredentialsService
// == nil` refusal in Verify: this test fails (the run reports success).
func TestInitCredentialsStageRefusedWhenTheOutputsCarryNoService(t *testing.T) {
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation")
	// A null value, which is what the installation's output answers for a
	// service the module did not create.
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"credentials_service": nil})}
	r.save(t)
	typedThroughYes(t)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials")
	s := out + errOut
	if err == nil {
		t.Fatalf("the stage reported a service the installation's outputs do not carry:\n%s", s)
	}
	if !strings.Contains(err.Error(), "carry no credentials service") {
		t.Errorf("the refusal does not say what the outputs were missing: %v\n%s", err, s)
	}
	if applies := r.ran(t, "apply"); len(applies) != 2 {
		t.Errorf("applies = %v, want the installation root twice", applies)
	}
	if _, ok := r.tfvars(t)["credentials_service"]; !ok {
		t.Error("a finished apply left the tfvars without the opt-in: the next init would plan the service's destruction, which the bundle container's prevent_destroy refuses")
	}
}

// A part-way apply failure must NOT drop the opted-in key (C1.20's review).
//
// Terraform can create part of the service — the account, the container, the
// grant — and then stop. Taking the key back out afterwards would make the
// next ordinary init plan exactly that part's destruction, a plan the bundle
// container's prevent_destroy refuses: the installation would be left holding
// a half-built service whose removal no run can plan and no run can finish.
// Only a run that never reached the apply puts the key back (putBack).
//
// Mutation (verified, run and restored): return to the old rule — restore on
// every path that did not set s.applied: this test fails, the tfvars losing
// credentials_service while the service may already exist in state.
func TestInitCredentialsStageKeepsTheKeyWhenTheApplyFailsPartWay(t *testing.T) {
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation")
	credentialsOutputs(t, r)
	// The installation stage's apply succeeds; this stage's fails. The fake
	// answers a key's calls in the order its list names them, so the two
	// applies of the run get different answers.
	r.script["apply"] = []any{
		map[string]any{"exit": 0},
		map[string]any{"exit": 1, "stderr": "Error: the apply was interrupted\n"},
	}
	r.save(t)
	typedThroughYes(t)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials")
	s := out + errOut
	if err == nil {
		t.Fatalf("the stage reported success when its apply failed:\n%s", s)
	}
	if applies := r.ran(t, "apply"); len(applies) != 2 {
		t.Fatalf("applies = %v, want the installation stage's and this stage's (the second failing)", applies)
	}
	if _, ok := r.tfvars(t)["credentials_service"]; !ok {
		t.Errorf("a part-way apply dropped the opt-in from the tfvars: the next init would plan the service's destruction, which the bundle container's prevent_destroy refuses, so a half-built service would be stuck:\n%s", s)
	}
}

// A restore that FAILS is an error of the run, not a warning (C1.20's
// review). restore is the only thing between a declined run and a leftover
// key, and a leftover key IS the opt-in — the next ordinary init carries it
// forward (infra.CarriedCredentialsService) and applies the service with none
// of this stage's typed confirmation. A warning is a run that reports success
// while leaving exactly that behind.
//
// The failure is driven where it really happens: at the confirmation, which
// the stage reaches after prepare wrote the key and before it would and
// applied nothing, the tfvars file is replaced by a directory — a write to it
// fails for root too, unlike a mode bit.
//
// Mutation (verified, run and restored): make the deferred restore print its
// error and return (fmt.Fprintf(r.w, "warning: %v")) instead of returning it:
// this test fails — the run's own error is "nothing was applied" and the
// restore failure is only a line of output.
func TestInitCredentialsStageFailsWhenItCannotPutTheKeyBack(t *testing.T) {
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation")
	fakeTerminal(t)
	path := filepath.Join(r.root(), infra.VarsFile)
	done := false
	old := canConfirmTyped
	canConfirmTyped = func(_ initflow.ConfirmClass, c initflow.Conditions) bool {
		// The opt-in is in the workdir's tfvars by now and nothing has
		// applied: this is the moment before the stage takes it back out.
		if !done {
			if b, err := os.ReadFile(path); err == nil && bytes.Contains(b, []byte("credentials_service")) {
				done = true
				if err := os.Remove(path); err != nil {
					t.Errorf("removing the tfvars to drive the failed put-back: %v", err)
				} else if err := os.Mkdir(path, 0o700); err != nil {
					t.Errorf("making a directory of the tfvars: %v", err)
				}
			}
		}
		return c.Agent == ""
	}
	t.Cleanup(func() { canConfirmTyped = old })
	out, errOut, err := executeStdin(t, "", "setup", "--yes", "--provision-credentials")
	s := out + errOut
	if !done {
		t.Fatalf("the stage never wrote the opt-in into the workdir's tfvars, so no put-back was driven:\n%s", s)
	}
	if err == nil {
		t.Fatalf("a run that could not take the opt-in back out reported success:\n%s", s)
	}
	if !strings.Contains(err.Error(), "before the next fugaro update") {
		t.Errorf("the run's own error is not the failed put-back: %v\n%s", err, s)
	}
	if applies := r.ran(t, "apply"); len(applies) != 1 {
		t.Errorf("applies = %v, want the installation stage's alone: a stage that declined must not apply", applies)
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
		t.Errorf("fugaro update --repo --provision-credentials: err = %v, want a refusal naming the flag", err)
	}
}

// The confirmation AND the flag's help tell the owner what this release's
// opt-in actually does (C1.20's review findings F1 and, in the second round,
// the scope of the promise). They used to say a job that mounts a model
// credential today would "ask this service instead" — which is not what
// happens here at all:
//
//   - what the opt-in removes is every REPOSITORY job's model credential, in
//     EVERY agent.auth mode (internal/infra/spec.go's dropModelMounts, which
//     the repository roots run for oauth and api-key as much as for cascade),
//   - the runner half is in, and it is the cascade's and the GitHub mint's:
//     the launch puts the service's address on a per-repository job
//     (internal/cli/run.go's credentialsServiceLaunchEnv), so a cascade run
//     takes its credential from the service and a GitHub run mints there —
//     while an oauth or api-key repository job has nothing to replace the
//     mount that went, and the bundle has to hold the App key before a GitHub
//     run can mint at all, and
//   - the SHARED project jobs are outside it: credentials_service reaches the
//     installation and workflow modules only, so modules/project keeps
//     mounting its provider keys and CLAUDE_CODE_OAUTH_TOKEN with their
//     secretAccessor grants (C3.9 is the task that moves them). Both texts
//     said "every model credential the installation holds", which is the
//     whole installation and is not what this step does.
//
// An owner typing their project's name at this prompt is deciding something
// money-and-permanent with that in front of them, so both texts are pinned
// here — the words that must be there, the scope that must be named, and the
// claims that must not come back.
//
// Mutation (verified, run and restored): restore either old sentence ("stops
// mounting one and asks this service instead" in the confirmation, "stops
// mounting each job's model credential and asks the service instead" in the
// flag's help), drop the shared-jobs sentence from either text, or put back
// the dormancy sentence this release retires ("the part that ... is not in
// this release yet"): this test fails.
func TestCredentialsStageTextsSayWhatTheOptInDoes(t *testing.T) {
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation")
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "setup", "--yes", "--provision-credentials")
	s := out + errOut
	if err == nil {
		t.Fatalf("--yes stood in for the typed confirmation:\n%s", s)
	}
	if !strings.Contains(s, "⚠ CONFIRM") {
		t.Fatalf("the stage never showed its confirmation, so this test read the wrong text:\n%s", s)
	}
	for _, want := range []string{
		"the REPOSITORY jobs' model credentials move to",
		"drops its model credential",
		"in every agent.auth mode",
		"oauth and api-key as much as cascade",
		"does NOT reach the shared project jobs",
		"CLAUDE_CODE_OAUTH_TOKEN",
		// What this release wires, which is the cascade's half and the
		// GitHub mint's — and what it leaves unwired, which is every other
		// agent.auth mode's own credential (the mount drop is auth-blind
		// while only the cascade's calls reach the service).
		"And it is wired",
		"a cascade run takes its model credential from the service",
		"its installation token minted there",
		"fugaro creds set github-app-key",
		"no model credential of its own and nothing yet to replace it",
		"agent.auth: cascade",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the confirmation does not say %q:\n%s", want, s)
		}
	}
	for _, unwanted := range []string{
		"asks this service instead",
		"every model credential the installation holds",
		// The two claims this release retires: the runner's half is here,
		// and "no re-applied repository job has a model credential at all"
		// is false of a cascade job now that the launch hands the job the
		// service's address.
		"not in this release yet",
		"no re-applied repository job has a model credential at all",
	} {
		if strings.Contains(s, unwanted) {
			t.Errorf("the confirmation still says %q:\n%s", unwanted, s)
		}
	}

	// The flag's help, which is what an owner reads BEFORE they run it.
	cmd := NewRootCmd()
	initCmd, _, ferr := cmd.Find([]string{"setup"})
	if ferr != nil {
		t.Fatal(ferr)
	}
	f := initCmd.Flags().Lookup("provision-credentials")
	if f == nil {
		t.Fatal("fugaro setup has no --provision-credentials flag")
	}
	for _, want := range []string{
		"the REPOSITORY jobs' model credentials move to",
		"drops its model credential",
		"in every agent.auth mode",
		"oauth and api-key as much as cascade",
		"does NOT reach the shared project jobs",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"And it is wired",
		"a cascade run takes its model credential from the service",
		"its installation token minted there",
		"fugaro creds set github-app-key",
		"has no model credential to use and nothing yet to replace it",
		"agent.auth: cascade",
	} {
		if !strings.Contains(f.Usage, want) {
			t.Errorf("the flag's help does not say %q:\n%s", want, f.Usage)
		}
	}
	for _, unwanted := range []string{
		"asks the service instead",
		"not in this release yet",
		"no re-applied repository job has a model credential to use",
	} {
		if strings.Contains(f.Usage, unwanted) {
			t.Errorf("the flag's help still says %q:\n%s", unwanted, f.Usage)
		}
	}
}

// THE ROOT CAUSE of C1.20's second review round (finding F3): an ordinary
// `fugaro update` after the opt-in must keep the credentials_service key.
//
// The tfvars are rewritten from the spec on every run, and the spec never
// carries the key — so the key is carried forward from the workdir's PREVIOUS
// tfvars (infra.CarriedCredentialsService, init.go's prepare). The order is
// what broke it: PrepareWorkdir rebuilds the workdir's tree (os.RemoveAll of
// dir/gcp) BEFORE the installation stage calls prepare, so a carry that reads
// wd.Root at that point reads a file that no longer exists, answers "nothing
// to carry", and drops the key. The next apply then plans the service, its
// account and its grant away — a plan the bundle container's prevent_destroy
// refuses, so every ordinary init after the opt-in FAILS instead of applying.
//
// This runs the real thing end to end: the real opt-in, then the real
// ordinary run over the same workdir.
//
// Mutation (verified, run and restored): read the carried key from wd.Root
// again in prepare (the order this test exists to pin): the second run drops
// credentials_service and this test fails.
func TestOrdinaryInitAfterTheOptInKeepsTheCredentialsService(t *testing.T) {
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation")
	credentialsOutputs(t, r)
	typedThroughYes(t)
	fakeTerminal(t)
	if out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials"); err != nil {
		t.Fatalf("the opt-in run failed: %v\n%s%s", err, out, errOut)
	}
	want, err := json.Marshal(r.tfvars(t)["credentials_service"])
	if err != nil || string(want) == "null" {
		t.Fatalf("the opt-in run wrote no credentials_service (%s, %v)", want, err)
	}

	// The ordinary run: no --provision-credentials, so the credentials stage
	// is not even registered, and nothing but the carry can keep the key.
	out, errOut, err := executeStdin(t, "", "setup", "--yes")
	if err != nil {
		t.Fatalf("the ordinary run after the opt-in failed: %v\n%s%s", err, out, errOut)
	}
	got, err := json.Marshal(r.tfvars(t)["credentials_service"])
	if err != nil || string(got) == "null" {
		t.Fatalf("an ordinary fugaro update dropped the opt-in: the next plan is the service's destruction, which the bundle container's prevent_destroy refuses\n%s%s", out, errOut)
	}
	if string(got) != string(want) {
		t.Errorf("credentials_service = %s, want it carried forward exactly as %s", got, want)
	}
}

// The opt-in records the service's URL in the project config, and that is what
// makes it reach a run at all: `fugaro run` puts FUGARO_CREDENTIALS_SERVICE_URL
// on a job from the selected config's credentials_service block (run.go's
// credentialsServiceLaunchEnv), and the block is published with the rest of the
// installation's facts (localcfg.Shared/MergeShared) so a teammate's machine
// gets it too. The value is the installation's own record and never something
// this stage composes: it is what the installation root's outputs carry, which
// is the service resource's own uri.
//
// It is written in the opt-in run ITSELF and not left to the next ordinary
// init: the installation stage's writeConfig ran before the service existed, so
// waiting would leave a one-run window in which the service is on, the
// repository jobs have lost their model credentials, and every launch still
// goes out without the variable — the run the owner launches right after this
// one, which is the whole point of the stage.
//
// Mutation (verified, run and restored): drop the recordURL call from Apply's
// apply branch: this test fails, the config carrying no block.
func TestInitCredentialsStageRecordsTheURLInTheProjectConfig(t *testing.T) {
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation")
	// The outputs as the run really meets them: the installation stage reads
	// them BEFORE the apply that creates the service, so its read carries no
	// credentials service (the module's output answers null while the tfvars
	// have no key), and every read from the credentials stage on carries the
	// URL. Scripting the list is what makes this test about recordURL at all —
	// with one static answer the installation stage would write the block and
	// the stage's own write would be invisible. The first two steps are the
	// reads before the service exists (the installation stage's own output
	// read and the writeConfig that follows it); the third is this stage's.
	noService := map[string]any{"stdout": outputsJSONWith(t, nil)}
	withService := map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": map[string]any{"name": infra.CredentialsServiceName, "region": "us-east5", "url": credentialsServiceURL},
	})}
	r.script["output"] = []any{noService, noService, withService}
	r.save(t)
	typedThroughYes(t)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	data, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil {
		t.Fatalf("the project config the opt-in wrote does not parse: %v\n%s", err, data)
	}
	if lc.CredentialsService == nil || lc.CredentialsService.URL != credentialsServiceURL {
		t.Fatalf("credentials_service = %+v, want the URL the installation's outputs carry (%s): without it every launch of this project goes out with no address on the job while the installation has the service on\n%s", lc.CredentialsService, credentialsServiceURL, data)
	}
	if !strings.Contains(out, credentialsServiceURL) {
		t.Errorf("the stage did not say it recorded the URL:\n%s", out)
	}
	// The block is published: a machine that has no local config reads it from
	// the runs bucket (localcfg.Select), which is the ledger item of PR #903.
	pub, err := lc.Shared().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pub), "credentials_service:") || !strings.Contains(string(pub), credentialsServiceURL) {
		t.Errorf("the published shared config carries no credentials service:\n%s", pub)
	}
}

// An installation that has NOT opted in records no block, and that is the
// off position of every reader: `fugaro run` puts no variable on the job, so
// the launch is byte for byte what it was before the wiring existed. An
// ordinary init of such an installation must therefore not invent one — there
// is no URL for it to write, and a block with an empty url is a config the
// loader refuses (localcfg's validCredentialsServiceURL).
func TestAnOrdinaryInitOfAnInstallationWithoutTheServiceRecordsNoBlock(t *testing.T) {
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation")
	// The outputs of an installation that never opted in: the field is absent,
	// which is what the module's output answers (outputs.tf).
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, nil)}
	r.save(t)
	typedThroughYes(t)
	if out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes"); err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	data, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "credentials_service") {
		t.Errorf("an ordinary init of an installation without the service wrote a credentials_service block:\n%s", data)
	}
}

// --print-vars prints the installation as it STANDS, so it carries the
// opted-in key too (C1.20's review). Printing a document that drops the
// opt-in is a reading of the installation the owner cannot trust: it is the
// same document a real run writes (init.go's prepare does the same carry),
// and --print-vars is how an owner looks at the variables without a cloud
// call.
//
// Mutation (verified, run and restored): drop the carry from the --print-vars
// branch: this test fails, the key missing from the printed document.
func TestPrintVarsCarriesTheOptedInKey(t *testing.T) {
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation")
	credentialsOutputs(t, r)
	typedThroughYes(t)
	fakeTerminal(t)
	if out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials"); err != nil {
		t.Fatalf("the opt-in run failed: %v\n%s%s", err, out, errOut)
	}
	out, errOut, err := executeStdin(t, "", "setup", "--print-vars")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--print-vars did not print one JSON object: %v\n%s", err, out)
	}
	svc, _ := doc["credentials_service"].(map[string]any)
	if svc == nil {
		t.Fatalf("--print-vars dropped the opt-in the workdir carries:\n%s", out)
	}
	if svc["image"] != r.credentialsRef() {
		t.Errorf("printed credentials_service.image = %v, want %q", svc["image"], r.credentialsRef())
	}
}

// credentialsBatchRig is the credentials rig plus what the compute stage's
// provisioning needs to reach its own apply: the installation's pin, the
// run-class account a repository root created, the rebuild job, and the
// compute root's own outputs.
func credentialsBatchRig(t *testing.T) *imagesRig {
	t.Helper()
	r := credentialsStageRig(t)
	r.appendConfig(t, "base_images:\n  "+config.BaseKind+": "+computePin+"\n")
	r.iam.AddServiceAccount(initProject, "fugaro-acme-web@proj-1234.iam.gserviceaccount.com", "Fugaro job acme/sandbox web")
	// The rebuild job the compute stage starts once its apply is done, in the
	// SCHEDULER region and not the installation's: Cloud Scheduler does not
	// serve us-east5, so the module creates it in us-east4 and the stage starts
	// it there (the compute rig registers it the same way, and the module's
	// scheduler_region says why).
	r.sched.SetJob(initProject, "us-east4", infra.PreloadSchedulerJob,
		"https://cloudbuild.googleapis.com/v1/projects/proj-1234/builds", "fugaro-preload@proj-1234.iam.gserviceaccount.com")
	r.script["output@compute"] = map[string]any{"stdout": computeOutputsJSON(t)}
	r.save(t)
	return r
}

// A run that names BOTH owner-run applies keeps the opt-in (C1.20's third
// review round, finding 1). The compute stage begins by reading the
// installation's outputs, and that read runs init.go's prepare on the
// installation's OWN workdir: the tfvars are rewritten from the spec — which
// never carries credentials_service — so the key the credentials stage wrote
// a few stages earlier is gone unless the carry reads the document the workdir
// holds NOW and not only the snapshot PrepareWorkdir took before the run
// started.
//
// What losing it costs, and why this is not a cosmetic loss: the next
// fugaro update carries nothing forward, plans the service, its account, its
// grant and its bundle secret away, and the bundle container's
// prevent_destroy refuses that plan — at the INSTALLATION stage, before any
// later stage runs, so the --provision-credentials run that would put the key
// back cannot reach its own apply either. The plan is made before the compute
// stage's typed confirmation, so even an owner who declines the Batch
// provisioning is left in it.
//
// The two typed confirmations are answered in the order the stages run:
// credentials first (it is registered before the repository stage), Batch
// second.
//
// Mutation (verified, run and restored): carry only from wd.PrevVars in
// prepare (drop the current-document carry): this test fails, the
// installation's tfvars holding no credentials_service after the run.
func TestCredentialsAndBatchInOneRunKeepTheOptIn(t *testing.T) {
	r := credentialsBatchRig(t)
	r.setPlanAt(t, "installation")
	credentialsOutputs(t, r)
	typedThroughYes(t)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, initProjectName+"\n"+initProjectName+"\n", "setup", "--yes", "--provision-credentials", "--provision-batch")
	s := out + errOut
	if err != nil {
		t.Fatalf("the run naming both applies failed: %v\n%s", err, s)
	}
	if applies := r.ran(t, "apply"); len(applies) < 2 {
		t.Fatalf("applies = %v, want the installation's and the compute root's at least: the run did not reach the Batch provisioning\n%s", applies, s)
	}
	if _, ok := r.tfvars(t)["credentials_service"]; !ok {
		t.Errorf("the run's later compute stage rewrote the installation's tfvars and dropped the opt-in: the next fugaro update plans the service's destruction, which the bundle container's prevent_destroy refuses at the installation stage, before the credentials stage can put the key back:\n%s", s)
	}
}

// Apply's empty-plan branch keeps the key (C1.20's third review round, finding
// 2). "No changes" is not "nothing happened": the plan that found nothing to
// do found it WITH the key in the tfvars, so the installation's state already
// holds exactly the service the key describes. Putting the key back there
// leaves a document that plans that service's destruction — a plan the bundle
// container's prevent_destroy refuses — and this stage reports itself done
// while doing it, the same trap the apply path is guarded against.
//
// Mutation (verified, run and restored): drop the keepKey assignment from the
// empty-plan branch: this test fails, the tfvars losing credentials_service
// while the stage reports "No changes".
func TestInitCredentialsStageKeepsTheKeyWhenThePlanFindsNothing(t *testing.T) {
	r := credentialsStageRig(t)
	credentialsOutputs(t, r)
	// The installation root's plan finds nothing to do, which is this branch:
	// terraform's detailed exit code 0, not 2. Three plans run in that root
	// and the fake consumes a scripted list in order: the installation
	// stage's (nothing to do), this stage's (nothing to do — the empty-plan
	// branch), and the audience's (fillAudience, which DOES have work: the
	// tfvars this fixture starts from carry no URL, so this run writes the
	// output's URL and applies it).
	r.script["plan@installation"] = []any{
		map[string]any{"exit": 0}, map[string]any{"exit": 0}, map[string]any{"exit": 2},
	}
	r.save(t)
	typedThroughYes(t)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials")
	s := out + errOut
	if err != nil {
		t.Fatalf("a run whose plan found nothing to do failed: %v\n%s", err, s)
	}
	if !strings.Contains(s, "No changes: the installation's credentials service matches the plan.") {
		t.Fatalf("the credentials stage did not reach its empty-plan branch, so this test read the wrong path:\n%s", s)
	}
	if _, ok := r.tfvars(t)["credentials_service"]; !ok {
		t.Errorf("the stage reported \"No changes\" and took the opt-in out of the tfvars: the installation's state holds the service, so the next plan is that service's destruction, which the bundle container's prevent_destroy refuses:\n%s", s)
	}
	if svc, _ := r.tfvars(t)["credentials_service"].(map[string]any); svc["url"] != credentialsServiceURL {
		t.Errorf("the empty-plan run left the audience unset (%v): a service whose plan finds nothing to do is the one that already exists, and the placeholder the module writes without a URL admits no caller", svc["url"])
	}
}

// ...and the one way out of that branch which does NOT keep the key: a Verify
// that refuses says the installation's outputs carry no service, and that
// output is a function of the tfvars key (modules/installation/outputs.tf), so
// the root does not read it and the state holds nothing of the service. The
// key goes back — a key this run could not deliver must not sit in the tfvars
// for a later fugaro to apply with none of this stage's typed confirmation.
//
// Mutation (verified, run and restored): set keepKey before the Verify in the
// empty-plan branch (keep the key whatever the outputs say): this test fails,
// the tfvars keeping credentials_service.
func TestInitCredentialsStageTakesTheKeyBackWhenTheNoChangeRunVerifiesNothing(t *testing.T) {
	r := credentialsStageRig(t)
	// The plan finds nothing to do, and the outputs carry no credentials
	// service: the key is in the tfvars and the installation does not read it.
	r.script["plan@installation"] = map[string]any{"exit": 0}
	r.save(t)
	typedThroughYes(t)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes", "--provision-credentials")
	s := out + errOut
	if err == nil {
		t.Fatalf("a run whose outputs carry no credentials service reported success:\n%s", s)
	}
	if !strings.Contains(err.Error(), "carry no credentials service") {
		t.Errorf("the run's error is not Verify's refusal: %v\n%s", err, s)
	}
	if _, ok := r.tfvars(t)["credentials_service"]; ok {
		t.Errorf("a run that applied nothing and enabled nothing left the opt-in in the tfvars: the next ordinary init carries it forward and applies the service with none of this stage's typed confirmation:\n%s", s)
	}
}

// The upgrade's own step (0.15.0). An installation that has opted in runs the
// credentials image of the release that pinned it and no other, and until this
// task NOTHING moved it: the credentials stage is registered only under
// --provision-credentials, and an ordinary run rewrites the tfvars from a spec
// that never carries the key and carries the PREVIOUS document's entry forward
// verbatim (infra.CarriedCredentialsService, init.go's prepare) — image and
// all. So an installation that opted in under an earlier release stayed on that
// release's image for ever, and every later release's behavior that the image
// decides (0.15.0's: the process serves HTTP/2 cleartext, so the module names
// the container's port h2c and Check 190's large push stops being cut at the
// HTTP/1 body limit) never reached it. CLAUDE.md's hard rule is that the
// upgrade carries its own migration and `fugaro update` is the whole of it.
//
// Mutation (verified, run and restored): return from pinCredentialsImage before
// it resolves (the carry this test exists to replace): the tfvars keep 1.2.2's
// image and this test fails.
func TestUpdateRepinsTheCredentialsImageOnAnOrdinaryUpdate(t *testing.T) {
	r := credentialsStageRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	seedCredentialsService(t, r, oldCredentialsRef("1.2.2"))
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an ordinary update of an opted-in installation failed: %v\n%s", err, s)
	}
	svc := credentialsServiceEntry(t, r)
	if got := svc["image"]; got != r.credentialsRef() {
		t.Errorf("credentials_service.image = %v, want %s: an ordinary update left the installation on the image of the release it opted in under, so the release's own process never reaches it\n%s", got, r.credentialsRef(), s)
	}
	// The digest is the resolve-and-verify step's own and not a value this run
	// composed: the release's "-pinned" tag, its signature, and the read-back
	// through the remote repository all happened, once for the run.
	if r.resolveCalls != 1 || r.arCalls != 1 {
		t.Errorf("resolve calls %d, read-backs %d, want one of each: the image is pinned only through the step that verifies the signature of the digest it names", r.resolveCalls, r.arCalls)
	}
	// Everything else the entry holds is what the earlier run wrote: the
	// re-pin is a change of the ONE value, not a rebuild of the service's
	// facts.
	for k, want := range map[string]any{
		"account_id": infra.CredentialsAccountID, "service": infra.CredentialsServiceName,
		"firebase_project": fpID, "url": credentialsServiceURL,
		"bundle_secret": "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358",
	} {
		if svc[k] != want {
			t.Errorf("credentials_service.%s = %v, want %v: the re-pin rewrote a fact of the service it does not own", k, svc[k], want)
		}
	}
	if apps := r.ran(t, "apply"); len(apps) != 1 {
		t.Errorf("applies = %v, want the installation root's alone", apps)
	}

	// An installation that has NOT opted in is untouched: nothing is resolved,
	// no credentials_service is written, and the release's image is not pinned
	// onto an installation whose owner never asked for the service.
	plain := credentialsStageRig(t)
	plain.setPlanAt(t, "installation", change("module.installation.google_storage_bucket.runs", "update"))
	if out, errOut, err := executeStdin(t, "", "update", "--yes"); err != nil {
		t.Fatalf("an ordinary update of an installation without the service failed: %v\n%s%s", err, out, errOut)
	}
	if plain.resolveCalls != 0 || plain.arCalls != 0 {
		t.Errorf("an installation without the credentials service resolved %d image(s) and read %d back: the opt-in is what the re-pin is for, and an installation that never opted in is byte for byte the run it was", plain.resolveCalls, plain.arCalls)
	}
	if _, ok := plain.tfvars(t)["credentials_service"]; ok {
		t.Errorf("an ordinary update wrote credentials_service into an installation that never opted in: %v", plain.tfvars(t)["credentials_service"])
	}
}

// The upgrade between the two releases this one follows: ONE `fugaro update`
// takes an installation that opted in under the previous release to this
// release's credentials image — the one whose process serves HTTP/2 cleartext,
// which is the release the module's own local counts as the first h2c-serving
// one and the tftest fixtures name as the pair (the run that asserts the port
// is named h2c, and the run before it that asserts no port at all).
//
// This is the migration CLAUDE.md's hard rule is about, and it is one run: the
// owner upgrades the CLI and runs the one documented command. The entry's other
// facts converge too — a re-pin that rebuilt them from the flag-less spec would
// drop the audience the service's callers mint their ID tokens for, which
// admits nobody.
func TestInstallationAtThePreviousVersionConvergesInOneUpdateCredentials(t *testing.T) {
	r := credentialsStageRigAt(t, "0.15.0")
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	seedCredentialsService(t, r, oldCredentialsRef("0.14.2"))
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("one update from the previous release failed: %v\n%s", err, s)
	}
	svc := credentialsServiceEntry(t, r)
	if got, want := svc["image"], r.credentialsRefAt("0.15.0"); got != want {
		t.Errorf("credentials_service.image = %v, want %s: one update from the previous release must reach this release's verified image, or the installation keeps serving the previous release's process and the h2c port the module names for this one is never what it runs\n%s", got, want, s)
	}
	if svc["url"] != credentialsServiceURL || svc["rtdb_url"] != r.db.URL {
		t.Errorf("the converged entry lost a fact the previous release wrote (%v): the re-pin changes the image and nothing else", svc)
	}
	if r.resolveCalls != 1 {
		t.Errorf("resolve calls %d, want 1: one update is one resolve, and it is the release's own signed image the run pins", r.resolveCalls)
	}
}
