package cli

// D2313 (docs/shared-jobs-default/reconciliation.md, plan task M1): a
// repository's `secrets:` is accepted and ignored with a warning, "in a way
// similar to repository-level images" (D1900) — disabled for now, the
// containers and their values left alone, the code kept so the feature can
// return.
//
// The ruling has five surfaces, and one test here is about each: the warning a
// person reads (validate; `fugaro update` prints the same text from M7), the
// repository-scope forms of the secrets commands, which answer the notice
// instead of writing, the credentials service's record, which names no
// repository-level container, the Terraform input, which does not move at
// all, and the secrets stage, which takes no value for such a name at all —
// no line, no prompt, the ruling's own sentence instead — because the one
// store a prompt could write is the repository's own copy, which no shared job
// mounts.
//
// Every other test in this package drives the machinery the ruling keeps with
// the door open, exactly as they did before it (main_test.go, TestMain), so
// what is here is the ruling alone — the shape TestRepoImageRefreshIsDisabled
// has for D1900's images.

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// repoSecretsOn turns D2313's door on for one test: the ruling as the CLI
// ships it, which TestMain opens for every other test in this package.
func repoSecretsOn(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { config.RepoSecretsOff = false })
	config.RepoSecretsOff = true
}

// repoSecretsLC is the installation the two structural halves are judged
// against: one github repository, one profile that mounts the project's own
// reserved key, the project_secrets entry that makes that key project-wide and
// the base image an installation has. It is deliberately the smallest config a
// project job and a repository root can be built from — what a shared job
// mounts is a function of the profiles and the accounts, and reads no
// repository at all (infra.ProjectJobs).
func repoSecretsLC(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Parse([]byte(`version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
base_images: { base: ` + checkBase + ` }
repos:
  acme/app: { provider: github, base_branch: main, workflows: [app], github_app_id: "123456789" }
project_secrets:
  anthropic-api-key: { allow_data_to: [acme/app] }
profiles:
  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, secrets: [anthropic-api-key] }
`))
	if err != nil {
		t.Fatalf("the fixture's local config does not parse: %v", err)
	}
	return lc
}

// TestWorkflowSecretsAreAcceptedAndIgnoredBySharedJobs is the acceptance's
// two words, over one fixture: a workflow whose `secrets:` names a secret of
// its own is a valid file — the ruling disables the feature, it does not make
// the file wrong — and no shared job mounts it.
//
// The second half is the property the ruling rests on and the one that needs
// no change to hold: a project job's mounts come from the profile the run
// selected, and a repository's own container is reachable only by a job that
// repository alone runs (infra.projectJobSecrets, whose refusal of a name that
// is not stored project-wide is TestProjectJobSecretsMustBeProjectWide's). So
// what this test pins is that the repository's declaration reaches the shared
// job set through no path at all — the profile's own mount is there, beside
// it, and the workflow's name is not.
func TestWorkflowSecretsAreAcceptedAndIgnoredBySharedJobs(t *testing.T) {
	repoSecretsOn(t)
	cfg, problems := config.Parse([]byte(workflowSecretsYAML))
	if len(problems) > 0 {
		t.Fatalf("a workflow declaring a secret of its own is accepted (D2313); Parse = %v", problems)
	}
	// The one word validate has about the block is the warning, which is what
	// "accepted" means here: a Problem would make the file undeployable.
	for _, p := range problems {
		if strings.Contains(p.Path, ".secrets") {
			t.Errorf("the declared secret is a problem: %v", p)
		}
	}
	if ws := config.RepoSecretsWarnings(cfg); len(ws) != 1 {
		t.Fatalf("the file's declared secrets earn one warning; got %v", ws)
	}

	jobs, err := infra.ProjectJobs(repoSecretsLC(t), "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatalf("building the shared job set: %v", err)
	}
	job, ok := jobs[gcp.ProjectJobName("standard", "github", "")]
	if !ok {
		t.Fatalf("the fixture deployed no shared job: %v", jobs)
	}
	if got, want := job.SecretEnv[config.ReservedSecrets[config.AnthropicSecret]], config.AnthropicSecret; got != want {
		t.Errorf("the shared job mounts %q for %s, want %s: the profile's own mount is the control", got, config.ReservedSecrets[config.AnthropicSecret], want)
	}
	for env, name := range job.SecretEnv {
		if name == "npm-token" || name == "deploy-token" {
			t.Errorf("the shared job mounts %s → %s: a workflow's own secret is not a shared job's to mount", env, name)
		}
	}
	for env := range job.Env {
		if env == "NPM_TOKEN" || env == "DEPLOY_TOKEN" {
			t.Errorf("the shared job's plain env carries %s, which is a workflow secret's variable", env)
		}
	}
}

// TestValidateWarnsThatRepoSecretsAreIgnored: the warning reaches a person
// through `fugaro validate` — one line, per repository, on stderr where every
// other warning goes, with the file still valid and the command still exiting
// 0. It names the secrets it is about and the way out, because a person who
// wrote a `secrets:` block has nowhere else to learn that no job mounts it.
func TestValidateWarnsThatRepoSecretsAreIgnored(t *testing.T) {
	repoSecretsOn(t)
	path := repoWithWorkflowSecret(t, "npm-token")
	out, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("a file declaring a repository-level secret is valid (D2313): %v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out, "is valid") {
		t.Fatalf("stdout = %q, want the file reported valid", out)
	}
	if n := strings.Count(errOut, "disabled for now"); n != 1 {
		t.Fatalf("%d warning line(s) about the block, want exactly one:\n%s", n, errOut)
	}
	if !strings.Contains(errOut, "warning: workflows.app.secrets: repository-level secrets (npm-token)") {
		t.Fatalf("the warning is not one of validate's own, at the block's own path:\n%s", errOut)
	}
	for _, want := range []string{"npm-token", "workflows.app.secrets", "project_secrets", "workflow_secrets"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the warning does not say %q:\n%s", want, errOut)
		}
	}
	// What replaces the value is the project's, which is the whole of what the
	// ruling asks a person to do about it.
	if !strings.Contains(errOut, "a shared job mounts only the project's secrets") {
		t.Errorf("the warning does not say what a run mounts instead:\n%s", errOut)
	}
}

// TestRepoScopeSecretsCommandsAnswerTheNotice: the repository-scope forms of
// the secrets commands answer a notice, change nothing and exit 0 — the shape
// `repoImagesDisabledNotice` gives a repository's own image build. They are
// answered before the container is looked up and before the value is read, so
// `secrets set` neither stores nor prompts, and `secrets rm` leaves the
// container it already holds where it is: the ruling keeps what is there, and
// a container nothing mounts is what a returning feature will want.
func TestRepoScopeSecretsCommandsAnswerTheNotice(t *testing.T) {
	if !repoSecretsOffShipped {
		t.Error("the CLI ships with repository-level secrets disabled (D2313): the notice is the release's behaviour, not a switch this test sets")
	}
	repoSecretsOn(t)
	secretsRmFugaro(t)
	sm := secretsFixture(t)
	// A container of the repository's own, with the labels the deploy writes:
	// the state `secrets rm` must leave standing.
	label, err := gcp.RepoLabel(appSlug)
	if err != nil {
		t.Fatal(err)
	}
	id := gcp.SecretID(appSlug, "npm-token")
	sm.Seed(id, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue,
		gcp.LabelRepo:    label,
		gcp.LabelSecret:  "npm-token",
	}, []byte(tokenValue))
	appCheckout(t)

	t.Run("secrets set --repo", func(t *testing.T) {
		out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "npm-token", "--repo", "acme/app")
		if err != nil {
			t.Fatalf("secrets set answered %v, want the notice: %s%s", err, out, errOut)
		}
		if !strings.Contains(out, config.RepoSecretsDisabledNotice) {
			t.Errorf("stdout = %q, want %q", out, config.RepoSecretsDisabledNotice)
		}
		if strings.Contains(out+errOut, tokenValue) {
			t.Fatal("the notice printed the value, which the command never read")
		}
		if got := sm.Latest(id); string(got) != tokenValue {
			t.Errorf("the container holds %q: the notice stored over the repository's own copy", got)
		}
	})
	t.Run("secrets set with the checkout's own repository", func(t *testing.T) {
		out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "npm-token")
		if err != nil {
			t.Fatalf("the bare form answered %v, want the notice: %s%s", err, out, errOut)
		}
		if !strings.Contains(out, config.RepoSecretsDisabledNotice) {
			t.Errorf("stdout = %q, want %q", out, config.RepoSecretsDisabledNotice)
		}
	})
	t.Run("secrets rm", func(t *testing.T) {
		out, errOut, err := execute(t, "secrets", "rm", "npm-token")
		if err != nil {
			t.Fatalf("secrets rm answered %v, want the notice: %s%s", err, out, errOut)
		}
		if !strings.Contains(out, config.RepoSecretsDisabledNotice) {
			t.Errorf("stdout = %q, want %q", out, config.RepoSecretsDisabledNotice)
		}
		if got := sm.Deleted(); len(got) != 0 {
			t.Errorf("the command deleted %v: the ruling leaves every container alone", got)
		}
		if got := sm.Latest(id); string(got) != tokenValue {
			t.Errorf("the container holds %q, want it untouched", got)
		}
	})

	// The control: the ruling is about the secrets a repository declares in its
	// own fugaro.yaml, and the delivery of an operator's own credential is not
	// one of those. A provider's key — the local config's, with this repository
	// in its reach — and a name the platform reserves still take the value with
	// the door on, and neither answers the notice. The provider's key goes where
	// every writer of one puts it since KS2 (R2, D1670 reversed): C1's entry for
	// the repository the flag named, in the credentials service's bundle, and no
	// container of its own.
	projectSecretFixture(t, noListProvider)
	bundleID := bundleFixtureFor(t, sm, "aurora", nil)
	t.Run("a provider's key is still stored", func(t *testing.T) {
		out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "openrouter-api-key", "--repo", "acme/app")
		if err != nil {
			t.Fatalf("a configured provider's key at repo scope: %v\n%s%s", err, out, errOut)
		}
		if strings.Contains(out, config.RepoSecretsDisabledNotice) {
			t.Fatalf("a provider's key answered the ruling's notice, which is about a repository's own declared secrets:\n%s", out)
		}
		if entry := bundleEntry(t, sm, bundleID, appSlug, "openrouter"); entry.Value() != tokenValue {
			t.Fatalf("the bundle's entry for acme/app holds %q, want the value the command read", entry.Value())
		}
		if id := gcp.SecretID(appSlug, "openrouter-api-key"); sm.Latest(id) != nil {
			t.Fatalf("%s holds %q: a provider's key writes no container of its own any more", id, sm.Latest(id))
		}
	})
	t.Run("a reserved name is still stored", func(t *testing.T) {
		out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "bitbucket-token", "--repo", "acme/app")
		if err != nil {
			t.Fatalf("a reserved name at repo scope: %v\n%s%s", err, out, errOut)
		}
		if strings.Contains(out, config.RepoSecretsDisabledNotice) {
			t.Fatalf("a reserved name answered the ruling's notice; a repository copy of one is doctor's secret-scope-shadowed to speak about:\n%s", out)
		}
		if got := string(sm.Latest(bitbucketSecret)); got != tokenValue {
			t.Fatalf("stored %q, want the repository's copy of the reserved name", got)
		}
	})
}

// TestNoWorkflowSecretsRecordIsWrittenForAnIgnoredSecret: the credentials
// service is told of no repository-level container, so it can serve no value
// no job mounts — the fail-closed direction the record's own step takes (G19).
//
// The name that resolves project-wide is the control beside it: the record is
// not "no rows for a workflow that declares secrets", it is the mounts the
// job really has, and a project-wide copy this repository may read is one of
// them. With the door off the pre-ruling answer is what the same spec gives,
// which is the machinery the ruling keeps.
func TestNoWorkflowSecretsRecordIsWrittenForAnIgnoredSecret(t *testing.T) {
	cfg, problems := config.Parse([]byte(workflowSecretsYAML))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	slug := mustSlug("github", "acme/app")
	rs := infra.RepoSpec{
		Secrets: map[string]string{
			"npm-token":    gcp.SecretID(slug, "npm-token"),
			"deploy-token": gcp.SecretID(slug, "deploy-token"),
		},
		ProjectSecrets: map[string]string{"deploy-token": gcp.ProjectSecretID("aurora", "deploy-token")},
	}
	repoSecretsOn(t)
	want := []creds.WorkflowSecret{{Env: "DEPLOY_TOKEN", ID: gcp.ProjectSecretID("aurora", "deploy-token")}}
	if rec := workflowSecretsRecord(rs, cfg); !slices.Equal(rec.Workflows["app"], want) {
		t.Errorf("the record holds %v, want %v: a repository-level secret is not a mount the service may be told about", rec.Workflows["app"], want)
	}
	// The door off is the answer before the ruling, unchanged: this is what
	// keeps the machinery the ruling keeps exercised rather than rotted.
	config.RepoSecretsOff = false
	want = []creds.WorkflowSecret{
		{Env: "NPM_TOKEN", ID: gcp.SecretID(slug, "npm-token")},
		{Env: "DEPLOY_TOKEN", ID: gcp.ProjectSecretID("aurora", "deploy-token")},
	}
	if rec := workflowSecretsRecord(rs, cfg); !slices.Equal(rec.Workflows["app"], want) {
		t.Errorf("with the door off the record holds %v, want %v", rec.Workflows["app"], want)
	}
}

// TestTheBuildReadsTheMountsTheDeploymentHas: the derived-image build a
// repository is offered reads the containers the spec it is handed mounts, each
// by the precedence the plan's merge takes, and nothing for a name that spec
// does not mount (D2313).
//
// The mounts, and neither the file's list nor the ruling's name test, because
// the three part company in both directions. A repository-level secret whose
// own container holds no version has no mount — Readiness drops it, and the
// container it was declared with is left versionless for good — so a build
// naming it asks Cloud Build for a version no store may ever put there, which
// Cloud Build refuses: that is the build `fugaro update` newly offers such a
// repository, failing after the apply, before the deploy plan and every later
// stage, on every attempt. A repository-level secret whose own container DOES
// hold a version keeps its mount and its job reads the value — Readiness's
// version check comes before the ruling's branch, which
// internal/infra's TestReadinessKeepsTheMountOfARepositorySecretThatHasAVersion
// holds it to — so a build that dropped it by the name's kind alone would build
// the image without a credential the job is using.
//
// The name the project covers is the third case, and the container beside it:
// the copy its job mounts is the project's, whatever the file wrote and
// whatever the repository's own container is. This is the layer nothing
// reached before — the infra test stops at NeedsBuild, and no fake models an
// available secret — so each container is asserted by name in the request.
//
// The spec here is Readiness's answer by construction, not by running it: the
// fixture builds the pre-Readiness one and applies each decision Readiness
// makes — the drop of the versionless name (the two maps its dropMount edits)
// and the ProjectSecrets entry its resolveProject writes — so every case is a
// state a deployment is really in.
func TestTheBuildReadsTheMountsTheDeploymentHas(t *testing.T) {
	cfg, problems := config.Parse([]byte(workflowSecretsYAML))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	repoSecretsOn(t)
	spec, err := infra.Repo(infra.Inputs{LC: repoSecretsLC(t), Repo: "acme/app", Cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	repoID, projectID := gcp.SecretID(spec.Slug, "npm-token"), gcp.ProjectSecretID("aurora", "deploy-token")
	if got := spec.Secrets["npm-token"]; got != repoID {
		t.Fatalf("the spec's own container for npm-token is %q, want %q: the fixture is not the state this case is about", got, repoID)
	}
	if _, mounted := spec.Workflows["app"].SecretEnv["NPM_TOKEN"]; !mounted {
		t.Fatalf("the fixture does not build the file's own secret into the job (%v): there would be no mount to read", spec.Workflows["app"].SecretEnv)
	}
	// Readiness's own decision for the name the project covers (resolveProject):
	// this repository may mount the project's copy, so that copy is the one its
	// job mounts, whatever the file wrote under `secrets:`.
	spec.ProjectSecrets = map[string]string{"deploy-token": projectID}

	request := func(t *testing.T) string {
		t.Helper()
		bs, err := cloudBuildSpec(spec, cfg, "app", checkBase, "E2_HIGHCPU_8", "gs://fugaro-runs-proj-1234", appliedGitSecretID(spec))
		if err != nil {
			t.Fatal(err)
		}
		req, err := gcp.BuildRequest("proj-1234", bs)
		if err != nil {
			t.Fatalf("the build of a repository whose declared secret the ruling ignores is not submittable: %v", err)
		}
		var versions []string
		for _, s := range req.AvailableSecrets.SecretManager {
			versions = append(versions, s.Env+"="+s.VersionName)
		}
		return strings.Join(versions, " ") + " | _SECRET_ENVS=" + req.Substitutions["_SECRET_ENVS"]
	}
	// The provider credential is always there and is what the fixture's
	// provider names it: a github build reads the App key, where bitbucket's
	// reads the token (cloudbuild.yaml's own variable, rewritten by
	// BuildRequest).
	gitVersion := "GITHUB_APP_KEY=projects/proj-1234/secrets/" + appliedGitSecretID(spec) + "/versions/latest"
	both := gitVersion +
		" NPM_TOKEN=projects/proj-1234/secrets/" + repoID + "/versions/latest" +
		" DEPLOY_TOKEN=projects/proj-1234/secrets/" + projectID + "/versions/latest" +
		" | _SECRET_ENVS=NPM_TOKEN DEPLOY_TOKEN"

	// Both mounts stand: the repository-level secret the deployment keeps
	// (Readiness's version check passed it), by the container its job reads,
	// and the project's copy for the name the project covers.
	if got := request(t); got != both {
		t.Errorf("the build reads\n%s\nwant\n%s", got, both)
	}

	// The mount the ruling takes away, in the two maps Readiness's dropMount
	// edits (the job's mounts, and the containers the repository root holds):
	// the build names no container for the name then, and the versionless one
	// appears nowhere in the request.
	ws := spec.Workflows["app"]
	delete(ws.SecretEnv, "NPM_TOKEN")
	delete(ws.SecretIDs, "npm-token")
	spec.Workflows["app"] = ws
	got := request(t)
	want := gitVersion +
		" DEPLOY_TOKEN=projects/proj-1234/secrets/" + projectID + "/versions/latest" +
		" | _SECRET_ENVS=DEPLOY_TOKEN"
	if got != want {
		t.Errorf("the build reads\n%s\nwant\n%s\nthe repository's own container for npm-token has no version and no store may give it one", got, want)
	}
	if strings.Contains(got, repoID) || strings.Contains(got, "NPM_TOKEN") {
		t.Errorf("the request names the unmounted secret at all:\n%s", got)
	}

	// The door decides the mounts, not this: a build reads the same deployment
	// with the ruling on and off, so nothing here is a second copy of it.
	config.RepoSecretsOff = false
	if again := request(t); again != got {
		t.Errorf("with the door off the build reads\n%s\nwant\n%s", again, got)
	}
}

// TestSecretsStageDoesNotAskForARepositoryLevelSecret: a repository-level
// secret is one no shared job mounts (D2313), so the stage must not take one.
// Every store a line or a prompt could name for it answers the ruling's notice
// — `secrets set NAME` decides its scope from this same local config and finds
// no project-wide meaning, and `--repo` is the notice — so a line for one
// would be a step that stores nothing, while a prompt for one would write the
// repository's own copy through this stage's own Secret Manager write — the
// store the ruling closed at the command, reached here by another route — so
// find() would then count that copy as covering the name, a later run would
// never ask again, and the last "stored" would be over a value no shared job
// reads.
// What the stage does instead is say the ruling's fact, in the sentence
// `validate` warns a file's block with.
func TestSecretsStageDoesNotAskForARepositoryLevelSecret(t *testing.T) {
	repoSecretsOn(t)
	r := newSecretsRig(t, bitbucketOrigin, strings.NewReader(""))
	r.workflowSecretConfig(t, "app-signing-key")
	r.stage.resolve(context.Background())
	if r.stage.projectScope("app-signing-key") {
		t.Fatal("the fixture is not the state this case is about: the secret has a project-wide meaning")
	}
	if slices.Contains(r.stage.tg.wanted, "app-signing-key") {
		t.Fatalf("the stage still wants the repository-level secret: %v", r.stage.tg.wanted)
	}
	res, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, l := range res.Left {
		left = append(left, l.Commands...)
	}
	if joined := strings.Join(left, "\n"); strings.Contains(joined, "app-signing-key") {
		t.Fatalf("the stage left a line for a secret no job mounts:\n%s", joined)
	}
	if n := r.store.setCalls(); n != 0 {
		t.Fatalf("%d value(s) stored: the stage took the secret anyway", n)
	}
	detail := ""
	for _, st := range res.Stages {
		if st.Name == initflow.Secrets {
			detail = st.Detail
		}
	}
	for _, want := range []string{
		"app-signing-key",
		"disabled for now and ignored",
		"a shared job mounts only the project's secrets",
		"project_secrets",
		"workflow_secrets",
		"D2313",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the stage's line does not say %q:\n%s", want, detail)
		}
	}
}

// TestRepoSecretsStayInTheRepoRootInputs: the ruling changes no Terraform
// input. `var.repo.secrets` still carries the container a repository declares,
// so a repository root an installation still applies keeps its containers and
// its mounts, and nothing plans to destroy a `prevent_destroy` container —
// which is what makes the feature able to return (D2313, and D2312 as amended
// is why nothing here removes the input either).
func TestRepoSecretsStayInTheRepoRootInputs(t *testing.T) {
	repoSecretsOn(t)
	cfg, problems := config.Parse([]byte(workflowSecretsYAML))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	lc := repoSecretsLC(t)
	spec, err := infra.Repo(infra.Inputs{LC: lc, Repo: "acme/app", Cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	id := gcp.SecretID(spec.Slug, "npm-token")
	if got := spec.Secrets["npm-token"]; got != id {
		t.Fatalf("spec.secrets[npm-token] = %q, want %q: the repository's own container is still what the root creates", got, id)
	}
	// The input itself, read the way Terraform reads it: the same bytes the
	// plan is made from, and the door is not what decides them.
	for _, off := range []bool{true, false} {
		config.RepoSecretsOff = off
		vars, err := infra.RepoVars(spec)
		if err != nil {
			t.Fatalf("RepoVars (door off = %t): %v", off, err)
		}
		if !strings.Contains(string(vars), `"npm-token"`) {
			t.Errorf("the repo root's vars do not carry npm-token with the door off = %t:\n%s", off, vars)
		}
	}
	config.RepoSecretsOff = true
}
