package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// catalogBuildInline is the definition the inline entry carries, and
// catalogBuildFetched the one the repository-sourced entry's file holds. They
// differ, so a test can tell the two copies and the two content tags apart.
const (
	catalogBuildInline  = "FROM example.invalid/base\nRUN true\n"
	catalogBuildFetched = "FROM example.invalid/tools\nRUN true\n"
)

// catalogBuildYAML is the catalog a build fixture's project names: an entry
// written in project state, an entry whose Dockerfile is fetched out of the
// repository the build runs from, an entry that is a prebuilt image and so has
// nothing to build, and an entry whose Dockerfile is another repository's.
const catalogBuildYAML = `image_catalog:
  entries:
    - name: tools
      tags: [node]
      source: { kind: inline, inline: "FROM example.invalid/base\nRUN true\n" }
    - name: base
      tags: [node, postgres]
      source: { kind: dockerfile, repo: acme/app, path: .fugaro/base.Dockerfile }
    - name: prebuilt
      tags: [docker]
      source: { kind: image, image: "docker.io/library/node:22" }
    - name: other
      tags: [node]
      source: { kind: dockerfile, repo: acme/other, path: .fugaro/other.Dockerfile }
`

// catalogBuildThreeYAML is three entries that all build, in the order they are
// declared: what a whole-catalog build of a catalog whose second entry fails
// shows about the first and the third.
const catalogBuildThreeYAML = `image_catalog:
  entries:
    - name: alpha
      tags: [node]
      source: { kind: inline, inline: "FROM example.invalid/alpha\nRUN true\n" }
    - name: beta
      tags: [node]
      source: { kind: inline, inline: "FROM example.invalid/beta\nRUN true\n" }
    - name: gamma
      tags: [node]
      source: { kind: inline, inline: "FROM example.invalid/gamma\nRUN true\n" }
`

// catalogBuildWorkflowsYAML is bitbucketYAML with a second workflow, so a
// catalog build has to take its secrets from the repository rather than from
// one workflow: extra declares the secrets the test at hand needs.
func catalogBuildWorkflowsYAML(secrets string) string {
	second := "  extra: { secrets: [" + secrets + "], commands: { build: sh build.sh, test: sh test.sh } }\n"
	return strings.Replace(bitbucketYAML, "workflows:\n", "workflows:\n"+second, 1)
}

// catalogBuildBucket is the runs bucket the catalog fixture's config names.
const catalogBuildBucket = "fugaro-runs-proj-1234"

// catalogBuildCheckout is catalogBuildCheckoutFor for a Bitbucket repository,
// which is what a catalog build test needs unless it is about the credential
// the copy is read with.
func catalogBuildCheckout(t *testing.T, repo, catalog string) (*gcpfake.Build, *gcpfake.GCS, *cloudFixture) {
	t.Helper()
	return catalogBuildCheckoutFor(t, repo, catalog, gitprov.KindBitbucket, "")
}

// catalogBuildCheckoutFor is a checkout of acme/app on provider whose
// fugaro.yaml is repo and whose project names the catalog catalog, with the
// fakes a Cloud Build needs, the installation's shared registry a catalog
// image is pushed to (D1152) and the build secrets repo's workflows declare.
// appID is what the local config records for the repository, which only a
// GitHub one carries (infra.Repo refuses a GitHub repository without one); ""
// records none, which is the state the missing-ID test wants.
//
// Its record bucket is the GCS fake rather than the file:// bucket every other
// cloud fixture uses: what the submission pins is the copy's object
// generation, which only GCS answers (a file bucket's is 0, and no
// generation). bucket_url is left naming another bucket, so the copy is
// written through openRecordBucket, the seam every record-bucket caller has,
// and the project-name check is answered from its cache, as it is in the
// fixture that records to a gs:// bucket (setBucketURL).
func catalogBuildCheckoutFor(t *testing.T, repo, catalog, provider, appID string) (*gcpfake.Build, *gcpfake.GCS, *cloudFixture) {
	t.Helper()
	files := npmFiles()
	files["fugaro.yaml"] = repo
	files[".fugaro/base.Dockerfile"] = catalogBuildFetched
	dir := checkoutWith(t, files)
	const entry = "  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	field := "provider: " + provider
	if appID != "" {
		field += ", github_app_id: \"" + appID + "\""
	}
	repos := strings.Replace(entry, "provider: github", field, 1)
	origin := "https://bitbucket.org/acme/app.git"
	if provider == gitprov.KindGitHub {
		origin = "https://github.com/acme/app.git"
	}
	testutil.Git(t, dir, "remote", "set-url", "origin", origin)
	fb := gcpfake.NewBuild(t)
	sm := gcpfake.NewSecrets(t)
	gcs := gcpfake.NewGCS(t)
	f := newCloudFixture(t, "cloud_build: "+fb.URL+"/", "secret_manager: "+sm.URL+"/")
	f.sm = sm
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), entry, repos, 1)
	updated = strings.Replace(updated, "runs_bucket: unused-bucket", "runs_bucket: "+catalogBuildBucket, 1)
	if updated == string(data) {
		t.Fatal("the fixture config's repos entry or runs_bucket has changed shape")
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	setBucketURL(t, f, "gs://other-bucket")
	prev := openRecordBucket
	openRecordBucket = func(_ context.Context, u string) (*blobx.Bucket, error) {
		if u != "gs://"+catalogBuildBucket {
			t.Errorf("the record bucket was opened as %s, want the runs bucket", u)
		}
		return gcs.Bucket(t, catalogBuildBucket), nil
	}
	t.Cleanup(func() { openRecordBucket = prev })
	f.appendConfig(t, catalog)
	fb.AddRegistry("proj-1234", "us-east5", gcp.SharedRegistryRepoID())
	return fb, gcs, f
}

// stubCatalogProvider makes the copy a catalog build writes read a
// repository-sourced definition out of a fake provider instead of the process
// environment's git credentials: the copy is the project's existing
// credential path, which a test has no account for.
func stubCatalogProvider(t *testing.T, files map[string]string) {
	t.Helper()
	p := &fake.Provider{Repo: "acme/app", State: fake.State{Files: files}}
	was := openCatalogProvider
	openCatalogProvider = func([]string, func(string)) (string, gitprov.Opener, error) { return "", catalogOpen(p), nil }
	t.Cleanup(func() { openCatalogProvider = was })
}

// catalogCopy is the object the runs bucket holds for entry's Dockerfile, as
// the build that reads it finds it.
func catalogCopy(t *testing.T, g *gcpfake.GCS, entry string) (string, int64) {
	t.Helper()
	key, err := config.CatalogDockerfileKey(entry)
	if err != nil {
		t.Fatal(err)
	}
	data, gen, err := g.Bucket(t, catalogBuildBucket).Read(context.Background(), key)
	if err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return string(data), gen
}

// catalogCopyAbsent is whether the runs bucket holds no copy for entry: a
// build that was never confirmed, or never reached, leaves the bucket as it
// found it.
func catalogCopyAbsent(t *testing.T, g *gcpfake.GCS, entry string) bool {
	t.Helper()
	key, err := config.CatalogDockerfileKey(entry)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = g.Bucket(t, catalogBuildBucket).Read(context.Background(), key)
	return errors.Is(err, blobx.ErrNotExist)
}

// catalogBuildImage is the image SharedCatalogImage names for entry.
func catalogBuildImage(t *testing.T, entry string) string {
	t.Helper()
	img, err := gcp.SharedCatalogImage("us-east5", "proj-1234", entry)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// catalogBuildSubs is the last request's substitutions.
func catalogBuildSubs(t *testing.T, fb *gcpfake.Build) map[string]any {
	t.Helper()
	subs, _ := fb.Last()["substitutions"].(map[string]any)
	return subs
}

// catalogBuildStep is the request's step with id, nil when it is not there.
func catalogBuildStep(t *testing.T, fb *gcpfake.Build, id string) map[string]any {
	t.Helper()
	steps, _ := fb.Last()["steps"].([]any)
	for _, raw := range steps {
		st, _ := raw.(map[string]any)
		if sid, _ := st["id"].(string); sid == id {
			return st
		}
	}
	return nil
}

// TestImageBuildWithACatalogBuildsTheEntry: with a catalog published,
// `fugaro image build <name>` submits that entry's own build (D1144) — the
// copied Dockerfile is the build's whole context, the definition's sha256 is
// the content tag of the image it produces, and the image lands in the
// installation's shared registry (D1152) as this repository's build account.
// Without a name every entry that has a Dockerfile is built, and one that is a
// prebuilt image is named and passed over rather than failing the others.
func TestImageBuildWithACatalogBuildsTheEntry(t *testing.T) {
	fb, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{
		".fugaro/base.Dockerfile":  catalogBuildFetched,
		".fugaro/other.Dockerfile": catalogBuildFetched,
	})
	// The copy of a repository-sourced entry reads the project's own
	// credential before it opens the provider, so the fixture holds one: the
	// stubbed provider is the only part of the copy a test has no account for.
	f.sm.AllowAccess()
	putSecretID(t, f.sm, mustSlug(gitprov.KindBitbucket, "acme/app"), config.BitbucketSecret, []byte(catalogTokenPlaceholder))

	out, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if buildPosts(fb) != 1 {
		t.Fatalf("building one entry submitted %d builds", buildPosts(fb))
	}
	subs := catalogBuildSubs(t, fb)
	if want := sha256Hex([]byte(catalogBuildInline)); subs["_CATALOG_TAG"] != want {
		t.Errorf("_CATALOG_TAG = %v, want the definition's sha256 %s", subs["_CATALOG_TAG"], want)
	}
	if subs["_IMAGE"] != catalogBuildImage(t, "tools") {
		t.Errorf("_IMAGE = %v, want the entry's image in the installation's shared registry", subs["_IMAGE"])
	}
	if subs["_BASE_BRANCH"] != "main" {
		t.Errorf("_BASE_BRANCH = %v", subs["_BASE_BRANCH"])
	}
	got, gen := catalogCopy(t, gcs, "tools")
	if got != catalogBuildInline {
		t.Errorf("the copy is %q, want the entry's own definition", got)
	}
	if gen <= 0 {
		t.Errorf("the copy's generation is %d, and the submission pins it", gen)
	}
	if !strings.Contains(out, "catalog-tools@sha256:") {
		t.Errorf("stdout = %q, want the built image and its digest", out)
	}
	// The smoke is the runtime-contract spec the caller renders (PIC4): a
	// catalog build renders nothing, so what the request carries is the whole
	// check.
	b64, _ := subs["_SELFTEST_B64"].(string)
	spec, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("_SELFTEST_B64 = %q: %v", b64, err)
	}
	cfg, problems := config.Parse([]byte(bitbucketYAML))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	commit, _ := headCommit(t)
	want, err := image.SpecForCloud(cfg, "app", commit, "https://bitbucket.org/acme/app.git")
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(spec) != string(wantJSON) {
		t.Errorf("the smoke's spec is %s, want %s", spec, wantJSON)
	}
	slug := mustSlug("bitbucket", "acme/app")
	if sa, _ := fb.Last()["serviceAccount"].(string); sa != "projects/proj-1234/serviceAccounts/"+gcp.BuildServiceAccountID(slug)+"@proj-1234.iam.gserviceaccount.com" {
		t.Errorf("serviceAccount = %q, want this repository's build account", sa)
	}
	// A catalog build reads one object and never a checkout: the derived
	// build's clone-and-render steps are not in it, and its own are.
	for _, id := range []string{"source", "render", "gate", "record"} {
		if catalogBuildStep(t, fb, id) != nil {
			t.Errorf("the catalog build has the per-workflow step %s", id)
		}
	}
	for _, id := range []string{"dockerfile", "catalog-pin", "catalog-tag", "catalog-record"} {
		if catalogBuildStep(t, fb, id) == nil {
			t.Errorf("the catalog build has no %s step", id)
		}
	}

	// An entry whose Dockerfile is in a repository is fetched out of it and
	// copied, and the same request shape submits it.
	if out, stderr, err = executeBuild(t, "image", "build", "base", "--base", "b:1"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if subs = catalogBuildSubs(t, fb); subs["_CATALOG_TAG"] != sha256Hex([]byte(catalogBuildFetched)) {
		t.Errorf("the fetched definition's _CATALOG_TAG = %v", subs["_CATALOG_TAG"])
	}
	if got, _ = catalogCopy(t, gcs, "base"); got != catalogBuildFetched {
		t.Errorf("the copy is %q, want the definition the repository holds", got)
	}
	if !strings.Contains(out, "catalog-base@sha256:") {
		t.Errorf("stdout = %q", out)
	}

	// No name with a catalog published is every entry that has a Dockerfile,
	// and the prebuilt one is said to have nothing to build rather than
	// stopping the others.
	before := buildPosts(fb)
	_, stderr, err = executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if got := buildPosts(fb) - before; got != 2 {
		t.Errorf("the unnamed build submitted %d builds, want one per buildable entry", got)
	}
	for _, want := range []string{"prebuilt", "other"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want %s named among the entries passed over", stderr, want)
		}
	}

	// An entry whose Dockerfile is another repository's is refused by name:
	// its build would run as that repository's account, which this checkout's
	// submission is not.
	before = buildPosts(fb)
	_, _, err = executeBuild(t, "image", "build", "other", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "other") || !strings.Contains(err.Error(), "acme/other") {
		t.Fatalf("exit %d, err %v: a name of another repository's entry is refused where to run it", ExitCode(err), err)
	}
	if buildPosts(fb) != before {
		t.Errorf("another repository's entry reached Cloud Build: %d posts", buildPosts(fb)-before)
	}
	// A named prebuilt entry is refused in the same way: it has no Dockerfile
	// to copy and no build to submit.
	_, _, err = executeBuild(t, "image", "build", "prebuilt", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "prebuilt") {
		t.Fatalf("exit %d, err %v: a prebuilt entry is named as having nothing to build", ExitCode(err), err)
	}
	if buildPosts(fb) != before {
		t.Errorf("a prebuilt entry reached Cloud Build: %d posts", buildPosts(fb)-before)
	}

	// --no-wait reports the submitted build and its log instead of waiting
	// for the digest the wait would report.
	before = buildPosts(fb)
	out, stderr, err = executeBuild(t, "image", "build", "tools", "--base", "b:1", "--no-wait")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if got := buildPosts(fb) - before; got != 1 {
		t.Errorf("a --no-wait build submitted %d builds", got)
	}
	if !strings.Contains(out, "submitted Cloud Build build") || !strings.Contains(out, "log:") || strings.Contains(out, "@sha256:") {
		t.Errorf("stdout = %q, want the submitted build and its log and no digest", out)
	}
}

// TestImageBuildWithoutACatalogKeepsItsMeaning: a project that has published
// no catalog keeps today's per-workflow meaning exactly — `fugaro image build`
// submits the workflow's derived build — and a name given there is refused
// rather than taken for a workflow (D1144).
func TestImageBuildWithoutACatalogKeepsItsMeaning(t *testing.T) {
	fb, _ := cloudBuildCheckout(t, false)

	if _, _, err := executeBuild(t, "image", "build", "--base", "b:1"); err != nil {
		t.Fatal(err)
	}
	subs := catalogBuildSubs(t, fb)
	if subs["_WORKFLOW"] != "app" {
		t.Fatalf("_WORKFLOW = %v: the unnamed build of a project with no catalog is not the workflow's", subs["_WORKFLOW"])
	}
	slug := mustSlug("bitbucket", "acme/app")
	if subs["_IMAGE"] != gcp.ImageName("us-east5-docker.pkg.dev/proj-1234/"+gcp.RegistryRepoID(slug), slug, "app") {
		t.Fatalf("_IMAGE = %v, want the repository's own image", subs["_IMAGE"])
	}
	if _, ok := subs["_CATALOG_TAG"]; ok {
		t.Errorf("a per-workflow build sent a catalog substitution: %v", subs)
	}

	_, _, err := executeBuild(t, "image", "build", "app", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "catalog") || !strings.Contains(err.Error(), "app") {
		t.Fatalf("exit %d, err %v: a name in a project with no catalog is refused", ExitCode(err), err)
	}
	if buildPosts(fb) != 1 {
		t.Errorf("a name no catalog has reached Cloud Build: %d posts", buildPosts(fb))
	}
}

// TestCatalogBuildNeverPutsASecretInALogOrAnError: a build secret reaches a
// catalog build as the Secret Manager name the build mounts and nothing else
// (D1150) — never its value, in no request, no output and no error, whatever
// the build does with it.
func TestCatalogBuildNeverPutsASecretInALogOrAnError(t *testing.T) {
	value := "npm-token-value-" + strings.Repeat("9", 24)
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	// The secret holds a value in the project, as it does in a real one: what
	// the CLI may carry of it is its name.
	secret := gcp.SecretID(mustSlug("bitbucket", "acme/app"), "npm-token")
	f.sm.Seed(secret, nil, []byte(value))

	out, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	body, err := json.Marshal(fb.Last())
	if err != nil {
		t.Fatal(err)
	}
	for what, text := range map[string]string{"stdout": out, "stderr": stderr, "the request": string(body)} {
		if strings.Contains(text, value) {
			t.Errorf("%s carries the secret's value", what)
		}
	}
	if !strings.Contains(string(body), secret) {
		t.Errorf("the request does not name the secret it mounts: %s", body)
	}
	// The value is a file secret the build step mounts, never an env of any
	// step and never a build argument the image's history would hold.
	steps, _ := fb.Last()["steps"].([]any)
	for _, raw := range steps {
		st, _ := raw.(map[string]any)
		id, _ := st["id"].(string)
		for _, key := range []string{"env", "args", "secretEnv"} {
			vals, _ := st[key].([]any)
			for _, v := range vals {
				s, _ := v.(string)
				if strings.Contains(s, value) {
					t.Errorf("step %s has the secret's value in its %s", id, key)
				}
			}
		}
	}

	// A build that fails says nothing of it either: the account's own log and
	// the status are all the error carries.
	fb.Outcome = "FAILURE"
	_, stderr, err = executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err == nil || strings.Contains(err.Error(), value) {
		t.Fatalf("err = %v, want the build's failure and no secret in it", err)
	}
	if strings.Contains(stderr, value) {
		t.Errorf("the failure's stderr carries the secret's value")
	}

	// A name no entry has is refused, and the refusal names no value either.
	fb.Outcome = ""
	before := buildPosts(fb)
	_, _, err = executeBuild(t, "image", "build", "missing", "--base", "b:1")
	if err == nil || strings.Contains(err.Error(), value) {
		t.Fatalf("err = %v", err)
	}
	if buildPosts(fb) != before {
		t.Errorf("an entry the catalog has not reached Cloud Build: %d posts", buildPosts(fb)-before)
	}
}

// TestCatalogBuildCopiesNothingWithoutConfirmation: the copies are written
// after the typed confirmation, so a run that cannot give it — a pipe here,
// with a name that only a catalog has — submits nothing and leaves the bucket
// as it found it.
func TestCatalogBuildCopiesNothingWithoutConfirmation(t *testing.T) {
	fb, gcs, _ := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	noTerminal(t)

	_, _, err := executeStdin(t, "aurora\n", "image", "build", "tools", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "tools") || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if buildPosts(fb) != 0 {
		t.Errorf("an unconfirmable build reached Cloud Build: %d posts", buildPosts(fb))
	}
	if !catalogCopyAbsent(t, gcs, "tools") {
		t.Errorf("an unconfirmable build copied the entry's Dockerfile")
	}
}

// TestCatalogBuildMountsEveryWorkflowsSecrets: an entry is not a workflow
// (D1144), so its build mounts the secrets the repository declares — every
// workflow's, each one mounted once — and a repository that declares several
// workflows builds an entry the way one that declares a single workflow does.
func TestCatalogBuildMountsEveryWorkflowsSecrets(t *testing.T) {
	// extra declares app's own pair again beside one of its own, so a build
	// that mounted the list per workflow would either mount NPM_TOKEN twice
	// (which the request refuses) or drop a secret.
	repo := catalogBuildWorkflowsYAML("{ name: npm-token, env: NPM_TOKEN }, { name: registry-token, env: REGISTRY_TOKEN }")
	fb, _, _ := catalogBuildCheckout(t, repo, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})

	if _, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if got := catalogBuildSubs(t, fb)["_SECRET_ENVS"]; got != "NPM_TOKEN REGISTRY_TOKEN" {
		t.Errorf("_SECRET_ENVS = %v, want each declared variable once", got)
	}
	avail, _ := fb.Last()["availableSecrets"].(map[string]any)
	mounted, _ := avail["secretManager"].([]any)
	counts := map[string]int{}
	for _, raw := range mounted {
		m, _ := raw.(map[string]any)
		env, _ := m["env"].(string)
		counts[env]++
	}
	if counts["NPM_TOKEN"] != 1 || counts["REGISTRY_TOKEN"] != 1 {
		t.Errorf("availableSecrets mounts %v, want each declared secret once", counts)
	}
	body, err := json.Marshal(fb.Last())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"npm-token", "registry-token"} {
		if want := gcp.SecretID(mustSlug("bitbucket", "acme/app"), name); !strings.Contains(string(body), want) {
			t.Errorf("the request does not name %s, which a workflow declares: %s", want, body)
		}
	}
}

// TestCatalogBuildRefusesTwoSecretsOnOneVariable: two workflows that mount
// different secrets on one variable are refused before anything is copied or
// submitted: a build mounts the repository's secrets, and one variable cannot
// carry two of them.
func TestCatalogBuildRefusesTwoSecretsOnOneVariable(t *testing.T) {
	repo := catalogBuildWorkflowsYAML("{ name: other-token, env: NPM_TOKEN }")
	fb, gcs, _ := catalogBuildCheckout(t, repo, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})

	_, _, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "NPM_TOKEN") || !strings.Contains(err.Error(), "other-token") {
		t.Fatalf("exit %d, err %v: two secrets on one variable are refused", ExitCode(err), err)
	}
	if buildPosts(fb) != 0 {
		t.Errorf("a refused build reached Cloud Build: %d posts", buildPosts(fb))
	}
	if !catalogCopyAbsent(t, gcs, "tools") {
		t.Errorf("a refused build copied the entry's Dockerfile")
	}
}

// TestCatalogBuildStopsAtTheEntryThatFails: a whole-catalog build submits its
// entries in the order the catalog declares them and stops at the first that
// fails. What was built before it stands — each entry is a build of its own,
// promoted to its own image — and the entries after it are never copied or
// submitted.
func TestCatalogBuildStopsAtTheEntryThatFails(t *testing.T) {
	fb, gcs, _ := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	// The fake runs a build's steps under its own lock, so the hook is the
	// only place a test can fail one build and not the next.
	n := 0
	fb.SetStep("build", func(string) error {
		n++
		if n == 2 {
			return errors.New("this entry's build fails")
		}
		return nil
	})

	out, stderr, err := executeBuild(t, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v (%s)", ExitCode(err), err, stderr)
	}
	if buildPosts(fb) != 2 {
		t.Fatalf("the failed build submitted %d builds, want the first two and no more", buildPosts(fb))
	}
	if !strings.Contains(err.Error(), "b0002") {
		t.Errorf("err = %v, want the failing build named", err)
	}
	alpha := catalogBuildImage(t, "alpha")
	if !strings.Contains(out, "built "+alpha+"@sha256:") {
		t.Errorf("stdout = %q, want the first entry reported as built before the failure", out)
	}
	if fb.Tag(alpha, "latest") == "" || fb.Tag(alpha, "candidate-b0001") != "" {
		t.Errorf("the first entry's build did not finish on its own: latest %q, candidate %q", fb.Tag(alpha, "latest"), fb.Tag(alpha, "candidate-b0001"))
	}
	catalogCopy(t, gcs, "alpha")
	if !catalogCopyAbsent(t, gcs, "gamma") {
		t.Errorf("an entry after the failing one was copied: the build went on past the failure")
	}
}
