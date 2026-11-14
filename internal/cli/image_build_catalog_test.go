package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/imagebake"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
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
	// A catalog entry's build is billable and leases its cost before it is
	// submitted (T4), so this checkout's installation has the budget database,
	// the caps and the price lookup a submission needs.
	buildBudgetWorld(t, f)
	return fb, gcs, f
}

// stubCatalogProvider makes the copy a catalog build writes read a
// repository-sourced definition out of a fake provider instead of the process
// environment's git credentials: the copy is the project's existing
// credential path, which a test has no account for.
func stubCatalogProvider(t *testing.T, files map[string]string) {
	t.Helper()
	stubCatalogProviderState(t, fake.State{Files: files})
}

// stubCatalogProviderState is stubCatalogProvider for a copy that reads more
// than a file out of the fake: the refs, the commits and the trees an entry
// that names a build context is resolved through.
func stubCatalogProviderState(t *testing.T, state fake.State) {
	t.Helper()
	p := &fake.Provider{Repo: "acme/app", State: state}
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

// catalogBuildStepIDs are the ids of the last request's steps, in order.
func catalogBuildStepIDs(t *testing.T, fb *gcpfake.Build) []string {
	t.Helper()
	steps, _ := fb.Last()["steps"].([]any)
	ids := make([]string, 0, len(steps))
	for _, raw := range steps {
		st, _ := raw.(map[string]any)
		id, _ := st["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// TestImageBuildWithACatalogBuildsTheEntry: with a catalog published,
// `fugaro image build <name>` submits that entry's own build (D1144) — the
// copied Dockerfile is the build's whole context, the definition's sha256 is
// the content tag of the image it produces, and the image lands in the
// installation's shared registry (D1152) as this repository's build account.
// Without a name every entry of this checkout is submitted, a prebuilt image
// among them: it is not built but smoked, which is the entry's own run
// (TestPrebuiltEntrySmoked), and only another repository's entry is passed
// over rather than failing the rest.
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

	// No name with a catalog published is every entry of this checkout: a
	// build per entry that has a Dockerfile, and the smoke of the one a
	// prebuilt image answers (F10/IS11). Only another repository's entry is
	// passed over rather than failing the rest.
	before := buildPosts(fb)
	_, stderr, err = executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if got := buildPosts(fb) - before; got != 3 {
		t.Errorf("the unnamed build submitted %d builds, want one per entry of this checkout", got)
	}
	if !strings.Contains(stderr, "other") {
		t.Errorf("stderr = %q, want other named among the entries passed over", stderr)
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

// catalogBuildContextYAML is one entry that names a build context: what a
// build pins and records for one.
const catalogBuildContextYAML = `image_catalog:
  entries:
    - name: base
      tags: [node]
      source: { kind: dockerfile, repo: acme/app, path: .fugaro/base.Dockerfile, branch: main, context: ci }
`

// TestCatalogBuildSubmitsTheContextIdentity: an entry that names a context is
// submitted with the identity the copy staged beside its Dockerfile — the
// commit both were read at, and the digest of the tree the build runs on — and
// the record's own commit is that one rather than the checkout's (D1395,
// D1400).
func TestCatalogBuildSubmitsTheContextIdentity(t *testing.T) {
	fb, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildContextYAML)
	// The commit the entry's branch resolves to, which is not this checkout's
	// HEAD: the fixture is a checkout of a branch of its own.
	const sha = "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
	tree := map[string]string{"Dockerfile": "FROM example.invalid/base\n", "src/main.go": "package main\n"}
	atCommit := map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched}
	for p, data := range tree {
		atCommit["ci/"+p] = data
	}
	stubCatalogProviderState(t, fake.State{
		Branches: map[string]map[string]string{"main": {".fugaro/base.Dockerfile": catalogBuildFetched}},
		Commits:  map[string]string{"main": sha},
		Trees:    map[string]map[string]string{sha: atCommit},
	})
	f.sm.AllowAccess()
	putSecretID(t, f.sm, mustSlug(gitprov.KindBitbucket, "acme/app"), config.BitbucketSecret, []byte(catalogTokenPlaceholder))

	if _, stderr, err := executeBuild(t, "image", "build", "base", "--base", "b:1"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	// The copy is the definition the resolved commit holds, and the two
	// objects the entry's identity is read back from are beside it.
	if got, _ := catalogCopy(t, gcs, "base"); got != catalogBuildFetched {
		t.Fatalf("the copy is %q, want the definition the commit holds", got)
	}
	b := gcs.Bucket(t, catalogBuildBucket)
	for _, staged := range []struct {
		key  func(string) (string, error)
		want string
	}{
		{config.CatalogContextCommitKey, sha},
		{config.CatalogContextHashKey, contextDigestOf(tree)},
	} {
		k, err := staged.key("base")
		if err != nil {
			t.Fatal(err)
		}
		data, _, err := b.Read(context.Background(), k)
		if err != nil || string(data) != staged.want {
			t.Errorf("%s holds %q (%v), want %q", k, data, err, staged.want)
		}
	}
	// The build's own record: the commit the definition was read at, and the
	// context's identity, all three of them the staged values.
	env := catalogRecordEnv(t, fb)
	for _, want := range []string{"COMMIT=" + sha, "CONTEXT_COMMIT=" + sha, "CONTEXT_SHA256=" + contextDigestOf(tree)} {
		if !slices.Contains(env, want) {
			t.Errorf("the record's env is %v, want it to carry %s", env, want)
		}
	}
	if checkout, _ := headCommit(t); checkout == sha {
		t.Fatal("the fixture's checkout is the commit the entry resolves to, so nothing here tells the two apart")
	}
	// The context is what the request tells the build to run on.
	if subs := catalogBuildSubs(t, fb); subs["_CONTEXT_DIR"] != "/workspace/src/ci" {
		t.Errorf("_CONTEXT_DIR = %v, want the checkout's own context directory", subs["_CONTEXT_DIR"])
	}
	// The checkout step is made at the context's commit, which is what the
	// build reads the context from.
	if st := catalogBuildStep(t, fb, "catalog-context"); st == nil {
		t.Error("the build has no context step")
	} else if env, _ := st["env"].([]any); !slices.Contains(env, "COMMIT="+sha) {
		t.Errorf("the context step's env is %v, want it to check out %s", env, sha)
	}
	// The smoke's own spec is rendered from the commit the build bakes, which
	// for a context entry is the commit its definition was read at: the smoke
	// checks the baked checkout against the commit the spec names, so one
	// rendered from the checkout's HEAD would fail wherever the operator is not
	// standing on the source branch's head. The two commits differ here, so a
	// spec naming the wrong one is not the same spec.
	var smoke image.SelftestSpec
	if err := json.Unmarshal(catalogBuildSelftest(t, fb), &smoke); err != nil {
		t.Fatalf("the smoke's spec is not one: %v", err)
	}
	if smoke.Commit != sha {
		t.Errorf("the smoke checks the checkout against %q, want the commit the build bakes (%s)", smoke.Commit, sha)
	}
	if smoke.Origin != "https://bitbucket.org/acme/app.git" {
		t.Errorf("the smoke's origin is %q, want the entry's repository", smoke.Origin)
	}
}

// catalogBuildSelftest is the smoke's spec as the request carries it: the
// decoded _SELFTEST_B64 the build hands to `fugaro image selftest`.
func catalogBuildSelftest(t *testing.T, fb *gcpfake.Build) []byte {
	t.Helper()
	b64, _ := catalogBuildSubs(t, fb)["_SELFTEST_B64"].(string)
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("_SELFTEST_B64 = %q: %v", b64, err)
	}
	return raw
}

// TestCatalogBuildRefusesACopyAnotherPublisherStaged: the definition, the
// commit and the context digest a request is built from are three objects, and
// another publisher staging this entry's copy between the copy and the
// submission's read-back leaves the bucket holding one revision's value beside
// another's. The build's pin step sees the definition alone — its generation
// and its sha256 — so a request made from such a mix would pass it and then
// build one revision's Dockerfile on another's context, recording the stale
// commit beside the newer digest. Each of the three is refused on its own,
// before anything is submitted, and the sentence says which value moved.
func TestCatalogBuildRefusesACopyAnotherPublisherStaged(t *testing.T) {
	// The commit the entry's branch resolves to and the definition it holds:
	// the values this command's own copy stages. otherCommit and otherHash are
	// another publisher's revision of the same entry.
	const sha = "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
	const otherCommit = "b1a2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4"
	const otherDefinition = "FROM example.invalid/other\nRUN true\n"
	otherHash := strings.Repeat("b", 64)
	atCommit := map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched, "ci/Dockerfile": "FROM example.invalid/base\n"}
	for _, tc := range []struct {
		what  string
		part  string
		key   func(string) (string, error)
		data  []byte
		ctype string
	}{
		{"the definition", "Dockerfile", config.CatalogDockerfileKey, []byte(otherDefinition), catalogDockerfileType},
		{"the commit", "commit", config.CatalogContextCommitKey, []byte(otherCommit), catalogIdentityType},
		{"the context digest", "context digest", config.CatalogContextHashKey, []byte(otherHash), catalogIdentityType},
	} {
		t.Run(tc.what, func(t *testing.T) {
			fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildContextYAML)
			stubCatalogProviderState(t, fake.State{
				Branches: map[string]map[string]string{"main": {".fugaro/base.Dockerfile": catalogBuildFetched}},
				Commits:  map[string]string{"main": sha},
				Trees:    map[string]map[string]string{sha: atCommit},
			})
			f.sm.AllowAccess()
			putSecretID(t, f.sm, mustSlug(gitprov.KindBitbucket, "acme/app"), config.BitbucketSecret, []byte(catalogTokenPlaceholder))

			// The other publisher's value, staged after this command's copy and
			// before its read-back: the window catalogCopyRace stands in for.
			race := catalogCopyRace
			t.Cleanup(func() { catalogCopyRace = race })
			catalogCopyRace = func(_ context.Context, b *blobx.Bucket, e config.Entry) {
				key, err := tc.key(e.Name)
				if err != nil {
					t.Fatal(err)
				}
				if err := b.Put(context.Background(), key, tc.data, tc.ctype); err != nil {
					t.Fatal(err)
				}
			}

			_, _, err := executeBuild(t, "image", "build", "base", "--base", "b:1")
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "base") ||
				!strings.Contains(err.Error(), "another publisher") || !strings.Contains(err.Error(), tc.part) {
				t.Fatalf("exit %d, err %v: a copy whose %s another publisher staged is refused, naming the entry and the value that moved", ExitCode(err), err, tc.what)
			}
			if buildPosts(fb) != 0 {
				t.Errorf("a copy of two revisions reached Cloud Build: %d posts", buildPosts(fb))
			}
		})
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

// TestCatalogBuildCopiesNothingOverCap: the copies are written after the lease
// the build takes (T4), so a build a cap refuses — here the repository's own
// daily cap, with the entry's copy still to be written — submits nothing to
// Cloud Build and leaves the bucket as it found it.
//
// Mutation (run, restore): move the copy of the entry's Dockerfile above
// openBuildLease in runImageBuildCatalog, and this test fails: the copy lands
// in the bucket for a build that was never submitted.
func TestCatalogBuildCopiesNothingOverCap(t *testing.T) {
	fb, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	noTerminal(t)
	f.db.Set(budget.PathCapsRepo(mustSlug("bitbucket", "acme/app")), map[string]any{"dailyMicros": usd1 / 2})

	_, _, err := executeStdin(t, "aurora\n", "image", "build", "tools", "--base", "b:1")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro budget set") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if buildPosts(fb) != 0 {
		t.Errorf("a build over the repository's cap reached Cloud Build: %d posts", buildPosts(fb))
	}
	if !catalogCopyAbsent(t, gcs, "tools") {
		t.Errorf("a refused build copied the entry's Dockerfile")
	}
}

// TestCatalogBuildNoWaitSettleFailureNamesTheBuild: the catalog arm's own
// version of image_test.go's TestImageBuildNoWaitSettleFailureNamesTheBuild.
// With --no-wait nothing waits for the build, so its whole reservation is
// recorded and the sentence naming it is the step's own line — which the
// settle precedes. A settle that fails therefore has to carry that sentence as
// its context (G8): the build is out and billing from the submission, and on
// this arm nothing else in the run says which build it is.
func TestCatalogBuildNoWaitSettleFailureNamesTheBuild(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	noTerminal(t)
	// The settle's own write and only it: the lease a take writes carries no
	// "settled" key, so the build is submitted first and this failure lands
	// after it — the state this test is about.
	f.db.DenyKeys("settled")

	out, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1", "--no-wait")
	if err == nil {
		t.Fatalf("the build's spend could not be recorded and the command exited zero:\n%s%s", out, stderr)
	}
	if buildPosts(fb) != 1 {
		t.Fatalf("submitted %d builds, want the one the failure is about\n%s%s", buildPosts(fb), out, stderr)
	}
	if !strings.Contains(out, "submitted Cloud Build build ") || !strings.Contains(out, "log:") {
		t.Errorf("the failed run never names the build it submitted and is still billing:\n%s", out)
	}
}

// TestCatalogBuildCountsTheWholeReservationWhenTheWatchIsLost: an entry whose
// build is submitted and whose watch then gives up — here the first status read
// is refused, which Wait does not retry — goes on billing without this command,
// so the day counts the whole reservation. The catalog arm settles with the same
// rule as the per-workflow one (image.go, image_build_catalog.go: both settle
// what gcp.TerminalStatus says of the result the wait returned).
//
// Mutation (run, restore): `lease.settle(ctx, gcp.TerminalStatus(done.Status))`
// back to `lease.settle(ctx, true)` in runImageBuildCatalog: this test fails
// with 15600 µ$ on the counters, the first minute of a build still running.
func TestCatalogBuildCountsTheWholeReservationWhenTheWatchIsLost(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	fb.FailGets, fb.FailGetCode = 1, http.StatusForbidden

	_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v: want the lost watch's own error (%s)", ExitCode(err), err, stderr)
	}
	if buildPosts(fb) != 1 {
		t.Fatalf("the entry's build was submitted %d times, want the one the lost watch is about", buildPosts(fb))
	}
	wholeReservationCounted(t, f.db, mustSlug("bitbucket", "acme/app"), "for a catalog build whose watch was lost")
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

// TestCatalogBuildCountsTheWholeReservationWhenItsRecordCannotBeWritten: a
// build the record's own failure leaves running is counted whole.
//
// The gate's record is written after the submission and before the wait, so a
// record that cannot be written ends the command with a build already out and
// billing — and nothing in that process watches this build for however long it
// runs. What the day counts is therefore the whole reservation, exactly as it
// counts a --no-wait build's (buildLease.settle's contract, image_build_lease.go):
// settling it as a build whose wait this command measured records the moment
// between the submission and the failed write at the lease's minute rate and
// gives the rest of the reservation back, which is money a build that is still
// running is spending and headroom under the day's and the project's caps for a
// loop of such builds.
//
// Both invocations are held here, because the flag cannot be what decides it:
// this path returns the command before any wait in either of them.
func TestCatalogBuildCountsTheWholeReservationWhenItsRecordCannotBeWritten(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag []string
	}{
		{name: "a build this command would have waited for"},
		{name: "--no-wait", flag: []string{"--no-wait"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
			w := managedBuildWorld(t, f, managedBuildMarker(1))
			stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
			noAgentSession(t)
			// The gate's own record is the write that fails: by then the
			// submission is out, and this failure is what the person reads.
			w.gcs.DenyWrites(initRunsBucket, managedmode.AuditPrefix)

			args := append([]string{"image", "build", "alpha", "--base", "b:1"}, tc.flag...)
			_, stderr, err := executeBuild(t, args...)
			if err == nil {
				t.Fatalf("a build whose audit record could not be written succeeded:\n%s", stderr)
			}
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "audit record could not be written") {
				t.Fatalf("exit %d, err %v: want the record failure the person reads", ExitCode(err), err)
			}
			if buildPosts(fb) != 1 {
				t.Fatalf("the build was submitted %d times, want the one submission the record failure is about", buildPosts(fb))
			}
			// The whole reservation the lease took, read from the same book the
			// fixture prices every build with rather than spelled out here.
			rate, err := budget.BuildCost(pricedBook(), "E2_HIGHCPU_8", "us-east5")
			if err != nil {
				t.Fatal(err)
			}
			want := rate.Estimate()
			client, err := newBudgetClient(context.Background(), f.db.URL, true)
			if err != nil {
				t.Fatal(err)
			}
			read, err := budget.ReadBuildState(context.Background(), client, mustSlug("bitbucket", "acme/app"), today())
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range []struct {
				what string
				got  budget.Micros
			}{
				{"the repository's day counter", read.State.Repo.Counted},
				{"the project's day counter", read.State.Global.Counted},
				{"the repository's day spend", read.State.Repo.Spent},
			} {
				if c.got != want {
					t.Errorf("%s is %d µ$ for a build that is still running, want the whole %d µ$ reservation: an unwatched build's settle records all of it, and the first minute of it is what the other direction leaves", c.what, c.got, want)
				}
			}
		})
	}
}

// TestPrebuiltEntrySmoked: an entry whose source is a prebuilt image is not
// built, and not passed over either — its run is the smoke of the reference
// the entry names against the runtime contract (F10/IS11), submitted as that
// entry's own Cloud Build. Before this, an unnamed build passed such an entry
// over and a named one was refused, so nothing in this tool ever asserted that
// the image a job starts on is one a job can start on: the job was the first
// thing to find out.
//
// What reports the answer afterwards is the record the run writes, which
// fugaro doctor reads back. Nothing of a build is in the submission, and the
// fixture makes that checkable: it holds no secret, stubs no provider and has
// nothing copied, so a run that read a credential, opened a provider or wrote
// a Dockerfile fails here rather than passing unnoticed.
func TestPrebuiltEntrySmoked(t *testing.T) {
	fb, gcs, _ := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	const ref = "docker.io/library/node:22"

	out, stderr, err := executeBuild(t, "image", "build", "prebuilt", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if got := buildPosts(fb); got != 1 {
		t.Fatalf("smoking the entry submitted %d builds, want one", got)
	}
	// The run is the smoke and the record of it: no step of a build is in it.
	if got, want := catalogBuildStepIDs(t, fb), []string{"catalog-smoke", "catalog-record"}; !slices.Equal(got, want) {
		t.Errorf("the smoke's steps are %v, want %v", got, want)
	}
	subs := catalogBuildSubs(t, fb)
	if len(subs) != 3 {
		t.Errorf("the smoke's substitutions are %v, want the entry's reference and the smoke's spec alone", subs)
	}
	if subs["_IMAGE"] != ref {
		t.Errorf("_IMAGE = %v, want the reference the entry names", subs["_IMAGE"])
	}
	if subs["_CATALOG_TAG"] != "" {
		t.Errorf("_CATALOG_TAG = %v, want empty: the run pushes no image and moves no tag", subs["_CATALOG_TAG"])
	}
	// The smoke of a reference somebody else published is the critical tools
	// alone: the checkout, the hardening and the stamped versions the
	// per-workflow spec asks about are about a container this command built.
	b64, _ := subs["_SELFTEST_B64"].(string)
	spec, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("_SELFTEST_B64 = %q: %v", b64, err)
	}
	want, err := json.Marshal(image.SelftestSpec{ToolsOnly: true, Tools: "critical"})
	if err != nil {
		t.Fatal(err)
	}
	if string(spec) != string(want) {
		t.Errorf("the smoke's spec is %s, want the tools tier alone %s", spec, want)
	}
	// Nothing was copied for the entry and nothing was pushed under its
	// reference: a smoke is not a build.
	if !catalogCopyAbsent(t, gcs, "prebuilt") {
		t.Errorf("the prebuilt entry's run wrote a Dockerfile copy")
	}
	if fb.Tag(ref, "latest") != "" {
		t.Errorf("the smoke pushed %s:latest", ref)
	}
	// What the entry answers is the digest its reference resolved to, which is
	// what the record then carries.
	if !strings.Contains(out, "smoked "+ref+"@sha256:") {
		t.Errorf("stdout = %q, want the reference and the digest it smoked", out)
	}

	// --no-wait reports the submitted run without a digest, and says what it
	// is smoking rather than that it built something. The reference it names is
	// the entry's own: a run that pushes nothing leaves no latest behind, so
	// the result names the reference as the entry spells it and never
	// docker.io/library/node:22:latest.
	before := buildPosts(fb)
	if out, stderr, err = executeBuild(t, "image", "build", "prebuilt", "--base", "b:1", "--no-wait"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if got := buildPosts(fb) - before; got != 1 {
		t.Errorf("a --no-wait smoke submitted %d builds", got)
	}
	if !strings.Contains(out, "smoking "+ref+";") || strings.Contains(out, "@sha256:") {
		t.Errorf("stdout = %q, want the submitted run and the reference it smokes", out)
	}
	if strings.Contains(out, ":latest") {
		t.Errorf("stdout = %q, want no tag of this command's on a reference somebody else published", out)
	}

	// A SUCCESS that reports no digest says the digest is unknown, and it names
	// the same reference: this path prints the submission's own result, which
	// is where a tag this command invented would have shown.
	fb.NoResults = true
	out, stderr, err = executeBuild(t, "image", "build", "prebuilt", "--base", "b:1")
	fb.NoResults = false
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if !strings.Contains(out, "smoked "+ref+", digest unknown") || strings.Contains(out, ":latest") {
		t.Errorf("stdout = %q, want the reference it smoked and no digest", out)
	}

	// --no-smoke asks for a build without its smoke, and a prebuilt entry has
	// nothing else: there would be no run at all.
	before = buildPosts(fb)
	_, _, err = executeBuild(t, "image", "build", "prebuilt", "--base", "b:1", "--no-smoke")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--no-smoke") {
		t.Fatalf("exit %d, err %v: --no-smoke on a prebuilt entry is refused", ExitCode(err), err)
	}
	if buildPosts(fb) != before {
		t.Errorf("a run --no-smoke left nothing in reached Cloud Build: %d posts", buildPosts(fb)-before)
	}

	// A smoke that fails is the command's failure: the build stops at the
	// smoke step, so the record step never runs and fugaro doctor reports the
	// entry as one whose smoke was never recorded.
	fb.FailStep = "catalog-smoke"
	_, _, err = executeBuild(t, "image", "build", "prebuilt", "--base", "b:1")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v: a failed smoke is the command's failure", ExitCode(err), err)
	}
}

// TestCatalogBuildBakesTheMappedReposCheckout: the bake step (IS8b) under the
// owner's O3 reading (b). A project that maps a repository onto an entry gets
// that repository's checkout baked into the entry's image — the definition the
// bucket holds is the entry's own Dockerfile with one generated stage on the
// end, cloned through the build's own credential — and the smoke the build
// submits reads the checkout where the stage put it, so an image that bakes
// is one the build verified rather than one it stopped looking at.
func TestCatalogBuildBakesTheMappedReposCheckout(t *testing.T) {
	fb, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	// The map is the project's (image_map, D1906) and the switch is on
	// (bake_repos, D1901): `fugaro image map set acme/app tools` and the setup
	// wizard's answer.
	f.appendConfig(t, "image_map:\n  acme/app: tools\nbake_repos: true\n")

	out, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	slug := mustSlug(gitprov.KindBitbucket, "acme/app")
	dir := "/work/baked/" + slug
	got, _ := catalogCopy(t, gcs, "tools")
	if !strings.HasPrefix(got, catalogBuildInline) {
		t.Errorf("the copy is %q, want the entry's own definition first", got)
	}
	if baked, ok := imagebake.BakedDir([]byte(got)); !ok || baked != dir {
		t.Errorf("the copy bakes at %q, %v; want %s: the mapped repository's checkout belongs in this image", baked, ok, dir)
	}
	// What the build pins and tags is the copy it wrote, composed stage and
	// all: the record and the nightly rebuild both read this one object.
	if subs := catalogBuildSubs(t, fb); subs["_CATALOG_TAG"] != sha256Hex([]byte(got)) {
		t.Errorf("_CATALOG_TAG = %v, want the composed definition's sha256 %s", subs["_CATALOG_TAG"], sha256Hex([]byte(got)))
	}
	// And the smoke checks the checkout the image baked: HEAD at the commit the
	// definition was copied from, origin the repository's own URL.
	b64, _ := catalogBuildSubs(t, fb)["_SELFTEST_B64"].(string)
	spec, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("_SELFTEST_B64 = %q: %v", b64, err)
	}
	var smoke image.SelftestSpec
	if err := json.Unmarshal(spec, &smoke); err != nil {
		t.Fatal(err)
	}
	commit, _ := headCommit(t)
	if smoke.RepoDir != dir || smoke.Commit != commit || smoke.Origin != "https://bitbucket.org/acme/app.git" {
		t.Errorf("the smoke's spec is %+v, want the baked checkout at %s on %s", smoke, dir, commit)
	}
	// The operator is told which repositories the image serves and which of
	// them clone at start: "bake your repos" reads like the whole project, and
	// the build's credential reaches this one repository alone.
	if !strings.Contains(stderr, "bakes the checkout of acme/app at "+dir) {
		t.Errorf("stderr = %q, want a line naming the baked checkout", stderr)
	}
	// What the line says the image got, not what a job does with it: the
	// runner opens the image's own work directory, so a build that promised a
	// job would find the source there would be promising something false.
	if !strings.Contains(stderr, "the image carries that repository's source") || strings.Contains(stderr, "jobs find the source") {
		t.Errorf("stderr = %q, want the checkout the image carries and no claim about a job reading it", stderr)
	}
	if !strings.Contains(out, "built ") {
		t.Errorf("stdout = %q, want the built image", out)
	}
}

// TestCatalogBuildBakesNothingWithoutTheMap: a checkout that the project's map
// does not put on this entry — every installation before this release, and one
// whose operator answered the switch no — is byte for byte what it was: no
// stage is composed, the copy is the entry's own definition, the smoke is the
// spec it was, and no line about the bake is printed. A repository the map
// does put on a quiet entry is baked (that is the default's own case). The
// other two ways a build bakes nothing are the ones the map cannot answer for,
// and TestCatalogBuildBakesNothingIntoASharedEntry and
// TestCatalogBuildRefusesABakeTheMapDoesNotConfirm pin them: an entry other
// repositories run, and a checkout that names this entry by its own workflow's
// reference with no map to say who else runs it.
//
// This is the half of the HARD RULE that keeps `fugaro update` a one-command
// upgrade: no path under it writes a definition at all (O1), so nothing about
// the bake arrives until a build an operator runs composes it.
func TestCatalogBuildBakesNothingWithoutTheMap(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, note string }{
		// The previous release's installation, and one whose operator never
		// mapped anything: the build says nothing at all about a bake.
		"a project with no map": {},
		// A switch written false is no ask for a bake, so there is nothing to
		// explain.
		"a project with bake off": {
			yaml: "image_map:\n  acme/app: tools\nbake_repos: false\n",
		},
		// This entry is not the image the repository runs: the build of it has
		// nothing to report, and saying why for every entry of a project would
		// read as a failure to the operator who asked for no bake.
		"a project mapping elsewhere": {yaml: "image_map:\n  acme/app: base\n"},
	} {
		t.Run(name, func(t *testing.T) {
			fb, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
			if tc.yaml != "" {
				f.appendConfig(t, tc.yaml)
			}
			_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
			if err != nil {
				t.Fatalf("%v (%s)", err, stderr)
			}
			got, _ := catalogCopy(t, gcs, "tools")
			if got != catalogBuildInline {
				t.Errorf("the copy is %q, want the entry's own definition %q: this project asked for no bake", got, catalogBuildInline)
			}
			if subs := catalogBuildSubs(t, fb); subs["_CATALOG_TAG"] != sha256Hex([]byte(catalogBuildInline)) {
				t.Errorf("_CATALOG_TAG = %v, want the untouched definition's sha256", subs["_CATALOG_TAG"])
			}
			switch {
			case tc.note == "" && strings.Contains(stderr, "no checkout baked"):
				t.Errorf("stderr = %q, want no bake line for a project that asks for none", stderr)
			case tc.note != "" && !strings.Contains(stderr, tc.note):
				t.Errorf("stderr = %q, want the reason the bake did not happen (%s)", stderr, tc.note)
			}
		})
	}
}

// TestCatalogBuildBakesNothingIntoASharedEntry: the entry serves another
// repository too, so its jobs can read whatever the image carries (F18). O3's
// reading (b) bakes the repositories that share the image's own repository
// grant, which for a shared image is none of the others — so nothing is
// composed into it, and the build names the repositories that made it shared
// rather than baking and warning. bake_repos: true does not change it: the
// switch asks for a bake, and it is not the answer to O3's boundary.
func TestCatalogBuildBakesNothingIntoASharedEntry(t *testing.T) {
	shared := "image_map:\n  acme/app: tools\n  acme/other: tools\n"
	for name, switchLine := range map[string]string{
		"the project answered nothing":   "",
		"the project asked for the bake": "bake_repos: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
			f.appendConfig(t, shared+switchLine)

			_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
			if err != nil {
				t.Fatalf("%v (%s)", err, stderr)
			}
			got, _ := catalogCopy(t, gcs, "tools")
			if got != catalogBuildInline {
				t.Errorf("the copy is %q, want the entry's own definition %q: nothing is baked into an image another repository runs", got, catalogBuildInline)
			}
			if switchLine == "" {
				if strings.Contains(stderr, "no checkout baked") {
					t.Errorf("stderr = %q, want no note: the project never asked for a bake", stderr)
				}
				return
			}
			if !strings.Contains(stderr, "no checkout baked (shared with acme/other)") {
				t.Errorf("stderr = %q, want the repository that would have read the checkout named", stderr)
			}
		})
	}
	t.Run("bake_repos false is silent", func(t *testing.T) {
		_, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
		f.appendConfig(t, shared+"bake_repos: false\n")

		_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
		if err != nil {
			t.Fatalf("%v (%s)", err, stderr)
		}
		got, _ := catalogCopy(t, gcs, "tools")
		if got != catalogBuildInline {
			t.Errorf("the copy is %q, want the entry's own definition %q: the operator turned baking off", got, catalogBuildInline)
		}
		if strings.Contains(stderr, "no checkout baked") {
			t.Errorf("stderr = %q, want no note: a build whose switch is off asked for no bake", stderr)
		}
	})
}

// TestCatalogBuildBakesNothingIntoTheDefaultEntry: the entry the project's
// catalog marks as its default is the image every repository of the project
// that names no entry of its own runs, and a job resolves that through
// catalog.Choose without reading the map at all (internal/infra's
// catalogJobImage) — so a map that puts this checkout alone on the default
// entry does not make that image this repository's. A checkout composed into it
// is source every other repository's jobs can read, which is F18's exposure and
// not O3 (b)'s shared-image case: the map says nothing about those repositories
// at all. bake_repos: true does not change it, for the same reason it does not
// in a shared entry.
func TestCatalogBuildBakesNothingIntoTheDefaultEntry(t *testing.T) {
	def := strings.Replace(catalogBuildYAML, "    - name: tools\n", "    - name: tools\n      default: true\n", 1)
	if def == catalogBuildYAML {
		t.Fatal("the fixture catalog has changed shape")
	}
	_, gcs, f := catalogBuildCheckout(t, bitbucketYAML, def)
	// A second repository of the project, which the map does not name: its jobs
	// resolve by their own config, and the default entry is what one that names
	// nothing runs. The repositories are rewritten rather than appended to,
	// because the fixture's own config carries blocks after them.
	const appRepo = "  acme/app: { provider: bitbucket, base_branch: main, workflows: [web] }"
	path := os.Getenv("FUGARO_CONFIG")
	cfg := readFileString(t, path)
	two := strings.Replace(cfg, appRepo, appRepo+"\n  acme/other: { provider: bitbucket, workflows: [web] }", 1)
	if two == cfg {
		t.Fatal("the fixture config's repos entry has changed shape")
	}
	if err := os.WriteFile(path, []byte(two), 0o600); err != nil {
		t.Fatal(err)
	}
	f.appendConfig(t, "image_map:\n  acme/app: tools\nbake_repos: true\n")

	_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	got, _ := catalogCopy(t, gcs, "tools")
	if got != catalogBuildInline {
		t.Errorf("the copy is %q, want the entry's own definition %q: a checkout in the project's default image is source every repository's jobs can read", got, catalogBuildInline)
	}
	if !strings.Contains(stderr, "no checkout baked (default entry, shared by every repo)") {
		t.Errorf("stderr = %q, want the entry's being the project's default named as why nothing was baked", stderr)
	}
}

// TestCatalogBuildBakesNothingWhileARepositoryIsUnmapped: the other half of the
// same fact, and the one the default rule does not cover. A repository of the
// project the map names no entry for resolves its image by its own config —
// a reference, or the tags it needs — and a build reads neither, so it cannot
// say whether that repository runs this entry. The map is the evidence the bake
// rests on, and it is whole evidence only once it names every repository the
// project has.
func TestCatalogBuildBakesNothingWhileARepositoryIsUnmapped(t *testing.T) {
	_, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	const appRepo = "  acme/app: { provider: bitbucket, base_branch: main, workflows: [web] }"
	path := os.Getenv("FUGARO_CONFIG")
	cfg := readFileString(t, path)
	two := strings.Replace(cfg, appRepo, appRepo+"\n  acme/other: { provider: bitbucket, workflows: [web] }", 1)
	if two == cfg {
		t.Fatal("the fixture config's repos entry has changed shape")
	}
	if err := os.WriteFile(path, []byte(two), 0o600); err != nil {
		t.Fatal(err)
	}
	f.appendConfig(t, "image_map:\n  acme/app: tools\nbake_repos: true\n")

	_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	got, _ := catalogCopy(t, gcs, "tools")
	if got != catalogBuildInline {
		t.Errorf("the copy is %q, want the entry's own definition %q: a build cannot say whether the repositories the map leaves out run this entry", got, catalogBuildInline)
	}
	if !strings.Contains(stderr, "no checkout baked (unmapped repos: acme/other). Map them: fugaro image map set <repo> <entry>") {
		t.Errorf("stderr = %q, want the repository the map leaves out named as why nothing was baked", stderr)
	}
}

// TestCatalogBuildRefusesABakeTheMapDoesNotConfirm: a checkout whose own
// workflow names the entry being built (T1 (b), `image_name:`, D1155) does run
// that entry's image, but the build cannot turn that into a bake on its own:
// its own repository's reference says nothing about the project's other
// repositories, and the map — the only evidence a build holds — does not say
// who else runs the entry. Another repository may pin the same entry, and its
// jobs would read the checkout, so nothing is composed and the line names the
// map entry that makes the readers knowable.
func TestCatalogBuildRefusesABakeTheMapDoesNotConfirm(t *testing.T) {
	repo := strings.Replace(bitbucketYAML, "app: {", "app: { image_name: tools,", 1)
	if repo == bitbucketYAML {
		t.Fatal("the fixture workflow has changed shape")
	}
	_, gcs, f := catalogBuildCheckoutFor(t, repo, catalogBuildYAML, gitprov.KindBitbucket, "")
	f.appendConfig(t, "bake_repos: true\n")

	_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	got, _ := catalogCopy(t, gcs, "tools")
	if got != catalogBuildInline {
		t.Errorf("the copy is %q, want the entry's own definition %q: the map does not put this repository on the entry, so the readers cannot be named", got, catalogBuildInline)
	}
	if !strings.Contains(stderr, "no checkout baked (the image map doesn't list acme/app). To bake: fugaro image map set acme/app tools") {
		t.Errorf("stderr = %q, want the refusal and the command that makes the readers knowable", stderr)
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "no checkout baked") && len(line) >= 200 {
			t.Errorf("the note is %d characters, want under 200: %q", len(line), line)
		}
	}
	// The entry the checkout does not run is nobody's to bake: building it
	// composes nothing (TestCatalogBuildBakesNothingWithoutTheMap).
}

// TestCatalogBuildBakesTheEntryAWorkflowsNeedsChoose: a workflow that declares
// needs: and no image_name: runs the entry the tag-cover rule covers its tags
// with — catalog.Choose, the rule internal/infra's catalogJobImage resolves
// the deployed job's image with — and that entry is a catalog reference like a
// pin (F4, T1 (b)). So the bake follows the rule and not the map: where the map
// puts the repository on another entry, neither entry is composed into — the
// mapped one is an image its own jobs never start, and the chosen one is an
// entry the map does not confirm, whose other readers the build cannot name.
// Where the map agrees with the rule, the bake lands in the entry the jobs run.
func TestCatalogBuildBakesTheEntryAWorkflowsNeedsChoose(t *testing.T) {
	repo := strings.Replace(bitbucketYAML, "app: {", "app: { needs: [postgres],", 1)
	if repo == bitbucketYAML {
		t.Fatal("the fixture workflow has changed shape")
	}
	newFixture := func(t *testing.T, imageMap string) (*gcpfake.Build, *gcpfake.GCS) {
		t.Helper()
		fb, gcs, f := catalogBuildCheckoutFor(t, repo, catalogBuildYAML, gitprov.KindBitbucket, "")
		stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
		f.sm.AllowAccess()
		f.appendConfig(t, imageMap)
		return fb, gcs
	}
	t.Run("the map names another entry", func(t *testing.T) {
		_, gcs := newFixture(t, "image_map:\n  acme/app: tools\n")

		_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
		if err != nil {
			t.Fatalf("%v (%s)", err, stderr)
		}
		got, _ := catalogCopy(t, gcs, "tools")
		if got != catalogBuildInline {
			t.Errorf("the copy is %q, want the entry's own definition %q: this repository's jobs run base, not tools", got, catalogBuildInline)
		}
		if strings.Contains(stderr, "bakes the checkout") {
			t.Errorf("stderr = %q, want no bake line for an entry this repository does not run", stderr)
		}
		// The entry the rule chose is the one its jobs start, but the map says
		// nothing about who else runs it, so that build composes nothing either.
		_, stderr, err = executeBuild(t, "image", "build", "base", "--base", "b:1")
		if err != nil {
			t.Fatalf("%v (%s)", err, stderr)
		}
		got, _ = catalogCopy(t, gcs, "base")
		if baked, ok := imagebake.BakedDir([]byte(got)); ok {
			t.Errorf("the copy of base bakes at %q: nothing is baked into an entry the map does not put this repository on", baked)
		}
	})
	t.Run("the map names the entry the rule chooses", func(t *testing.T) {
		fb, gcs := newFixture(t, "image_map:\n  acme/app: base\n")

		_, stderr, err := executeBuild(t, "image", "build", "base", "--base", "b:1")
		if err != nil {
			t.Fatalf("%v (%s)", err, stderr)
		}
		dir := "/work/baked/" + mustSlug(gitprov.KindBitbucket, "acme/app")
		got, _ := catalogCopy(t, gcs, "base")
		if baked, ok := imagebake.BakedDir([]byte(got)); !ok || baked != dir {
			t.Errorf("the copy of base bakes at %q, %v; want %s: needs: [postgres] picks that entry and the map puts this repository alone on it", baked, ok, dir)
		}
		if subs := catalogBuildSubs(t, fb); subs["_CATALOG_TAG"] != sha256Hex([]byte(got)) {
			t.Errorf("_CATALOG_TAG = %v, want the composed definition's sha256", subs["_CATALOG_TAG"])
		}
	})
}

// TestCatalogBuildSaysWhatItTookOffACopy: a copy can carry a bake no build of
// this project composes again — the map moved the repository onto another
// entry, or the switch went off — and the build that rewrites that copy
// replaces the composed definition with the entry's own bytes. The stage is the
// one part of a copy that is not the entry's own definition, so its loss is
// nothing the build's own plan shows: noteBakePlan is quiet about an entry this
// checkout does not run (Plan.Notable), and the definition the build then
// composes carries no marker for the nightly check to refuse. The jobs of the
// entry stop finding a checkout in their image from this build on, which is the
// F18-safe direction and not a refusal — but it is a change in what the image
// holds, and the one line the operator gets about it is this one.
func TestCatalogBuildSaysWhatItTookOffACopy(t *testing.T) {
	fb, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	// The map puts this checkout on base, not on the entry being built: this
	// build composes nothing into tools.
	f.appendConfig(t, "image_map:\n  acme/app: base\nbake_repos: true\n")
	slug := mustSlug(gitprov.KindBitbucket, "acme/app")
	stale, err := imagebake.Append([]byte(catalogBuildInline), imagebake.Plan{Baked: &imagebake.Repo{
		Repo: "acme/app", Slug: slug, URL: "https://bitbucket.org/acme/app.git", Branch: "main",
	}})
	if err != nil {
		t.Fatal(err)
	}
	key, err := config.CatalogDockerfileKey("tools")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gcs.Bucket(t, catalogBuildBucket).Create(context.Background(), key, stale, "text/plain"); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	got, _ := catalogCopy(t, gcs, "tools")
	if got != catalogBuildInline {
		t.Errorf("the copy is %q, want the entry's own definition %q: this build composes no bake into this entry", got, catalogBuildInline)
	}
	if subs := catalogBuildSubs(t, fb); subs["_CATALOG_TAG"] != sha256Hex([]byte(catalogBuildInline)) {
		t.Errorf("_CATALOG_TAG = %v, want the definition the build composed", subs["_CATALOG_TAG"])
	}
	// The line names the checkout by the repository this machine's config
	// answers for the marker's slug, which is the only name a person can act on.
	if want := "no longer carries the checkout of acme/app at /work/baked/" + slug; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to say the copy %s", stderr, want)
	}
}

// TestCatalogBuildSaysNothingAboutABakeItKeeps: the note above is owed only for
// a bake this build takes off. A copy that bakes the same checkout this build
// composes again — the ordinary rebuild, after a definition changed — is
// written whole and drops nothing, and the build's own bake line is the one
// thing it has to say.
func TestCatalogBuildSaysNothingAboutABakeItKeeps(t *testing.T) {
	repo := strings.Replace(bitbucketYAML, "  app: {", "  app: { image_name: tools,", 1)
	if repo == bitbucketYAML {
		t.Fatal("the fixture workflow has changed shape")
	}
	_, gcs, f := catalogBuildCheckoutFor(t, repo, catalogBuildYAML, gitprov.KindBitbucket, "")
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	f.sm.AllowAccess()
	f.appendConfig(t, "image_map:\n  acme/app: tools\nbake_repos: true\n")

	_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	slug := mustSlug(gitprov.KindBitbucket, "acme/app")
	if !strings.Contains(stderr, "bakes the checkout of acme/app at /work/baked/"+slug) {
		t.Fatalf("stderr = %q, want the build's own bake line: this fixture is the rebuild the note must not fire on", stderr)
	}
	// The second build reads the copy the first one wrote, bake and all, and
	// writes the same bytes back: nothing was dropped, so nothing is said.
	_, stderr, err = executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if strings.Contains(stderr, "no longer carries") {
		t.Errorf("stderr = %q, want no dropped-bake line for a copy that keeps its bake", stderr)
	}
	if got, _ := catalogCopy(t, gcs, "tools"); func() bool { _, ok := imagebake.BakedSlug([]byte(got)); return !ok }() {
		t.Errorf("the copy is %q, want it to keep the bake it had", got)
	}
}
