package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/buildtoken"
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

// catalogBuildWorkflowsYAML is bitbucketYAML with a second workflow, for the
// tests about a repository that declares several: an entry's build reads no
// workflow's secrets (B9, D2311), and the workflow count changes neither that
// nor which workflow's name the smoke's spec is rendered for. secrets is what
// the second workflow declares.
func catalogBuildWorkflowsYAML(secrets string) string {
	second := "  extra: { secrets: [" + secrets + "], commands: { build: sh build.sh, test: sh test.sh } }\n"
	return strings.Replace(bitbucketYAML, "workflows:\n", "workflows:\n"+second, 1)
}

// catalogBuildBucket is the runs bucket the catalog fixture's config names.
const catalogBuildBucket = "fugaro-runs-proj-1234"

// catalogBuildServiceURL is the installation's credentials service as the
// catalog fixture's config records it: the address an entry's build asks for
// the git credential it clones with (IS8, D3330).
const catalogBuildServiceURL = "https://credentials.example.com"

// catalogBuildCheckout is catalogBuildCheckoutFor for a Bitbucket repository,
// which is what a catalog build test needs unless it is about the credential
// the copy is read with.
func catalogBuildCheckout(t *testing.T, repo, catalog string) (*gcpfake.Build, *gcpfake.GCS, *cloudFixture) {
	t.Helper()
	return catalogBuildCheckoutFor(t, repo, catalog, gitprov.KindBitbucket, "")
}

// catalogBuildCheckoutFor is a checkout of acme/app on provider whose
// fugaro.yaml is repo and whose project names the catalog catalog, with the
// fakes a Cloud Build needs and the installation's shared registry a catalog
// image is pushed to (D1152). Whatever secrets repo's workflows declare are
// part of the checkout and reach no step of the build (B9, D2311).
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
	// The installation's credentials service, which the config of every
	// installation that deployed one records (IS8, D3330): a catalog entry's
	// build clones its source through it, so a config without the address
	// submits no build at all (buildServiceURL's rule, one command along).
	f.appendConfig(t, "credentials_service: { url: "+catalogBuildServiceURL+" }\n")
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
// installation's shared registry (D1152), run as the project's build identity
// (B6).
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
	// The project's build identity and not the repository's build account:
	// the identity is the installation's one account for catalog builds (B6),
	// and it is the value CatalogBuildSpec.check requires.
	if sa, _ := fb.Last()["serviceAccount"].(string); sa != "projects/proj-1234/serviceAccounts/"+gcp.BuildIdentityEmail("proj-1234") {
		t.Errorf("serviceAccount = %q, want the project's build identity %s", sa, gcp.BuildIdentityEmail("proj-1234"))
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
	// The clone step is made at the context's commit, which is what the build
	// reads the context from.
	if st := catalogBuildStep(t, fb, "catalog-clone"); st == nil {
		t.Error("the build has no clone step")
	} else if env, _ := st["env"].([]any); !slices.Contains(env, "COMMIT="+sha) {
		t.Errorf("the clone step's env is %v, want it to check out %s", env, sha)
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

// TestCatalogBuildNeverPutsASecretInALogOrAnError: a secret's value is never in
// a request, an output or an error, whatever the build does with it. Since B9
// the one secret a catalog build names at all is the clone step's credential
// (D2311), so the container a workflow's own `secrets:` entry is stored in is
// not in the request either: a value seeded there has no path into the build,
// and this test holds both halves — the value nowhere, and the container
// nowhere.
func TestCatalogBuildNeverPutsASecretInALogOrAnError(t *testing.T) {
	value := "npm-token-value-" + strings.Repeat("9", 24)
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	// The secret holds a value in the project, as it does in a real one: what
	// the CLI may carry of it is its name, and of a catalog build's none at all.
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
	if strings.Contains(string(body), secret) {
		t.Errorf("the request names the workflow secret's container: an entry's build is handed no secret of the entry's (B9, D2311): %s", body)
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

// TestCatalogBuildIsHandedNoWorkflowSecret: an entry is not a workflow
// (D1144), and since B9 no workflow's secrets reach its build at all — the
// step is handed none (D2311), the one secret the request names is the
// repository's clone credential, and _SECRET_ENVS is empty. So a repository
// that declares several workflows builds an entry the way one that declares a
// single workflow does: there is no list to merge and none to carry.
func TestCatalogBuildIsHandedNoWorkflowSecret(t *testing.T) {
	// extra declares app's own pair again beside one of its own: a build that
	// still merged the lists would carry one of these names in _SECRET_ENVS and
	// fail this test, whatever it did with the duplicate.
	repo := catalogBuildWorkflowsYAML("{ name: npm-token, env: NPM_TOKEN }, { name: registry-token, env: REGISTRY_TOKEN }")
	fb, _, _ := catalogBuildCheckout(t, repo, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})

	if _, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if got := catalogBuildSubs(t, fb)["_SECRET_ENVS"]; got != "" {
		t.Errorf("_SECRET_ENVS = %v, want empty: the build step's loop hands docker a --secret for every name in it, and an entry's build is handed none (B9, D2311)", got)
	}
	// And no secret at all: the request carries no availableSecrets, no step
	// carries a secretEnv, and the credential step asks the installation's
	// service for the git credential it clones with (IS8, D3330).
	if avail := fb.Last()["availableSecrets"]; avail != nil {
		t.Errorf("the request mounts %v, want no availableSecrets at all: an entry's build names no secret of the entry's (B9, D2311) and none of the clone's either (IS8, D3330)", avail)
	}
	for _, st := range fb.Last()["steps"].([]any) {
		step, _ := st.(map[string]any)
		if env := step["secretEnv"]; env != nil {
			t.Errorf("step %v carries secretEnv %v, want none: the credential step is handed the service's address and the secrets come from it", step["id"], env)
		}
	}
	body, err := json.Marshal(fb.Last())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"npm-token", "registry-token"} {
		if want := gcp.SecretID(mustSlug("bitbucket", "acme/app"), name); strings.Contains(string(body), want) {
			t.Errorf("the request names %s, which a workflow declares: a catalog build carries no secret of the entry's (B9, D2311): %s", want, body)
		}
	}
}

// TestCatalogBuildIgnoresTwoSecretsOnOneVariable: two workflows that declare
// different secrets on one variable used to be refused, because the merged
// list would have carried the variable twice. Nothing merges it any more (B9,
// D2311): neither secret reaches the build, so there is nothing to refuse and
// the entry builds.
func TestCatalogBuildIgnoresTwoSecretsOnOneVariable(t *testing.T) {
	repo := catalogBuildWorkflowsYAML("{ name: other-token, env: NPM_TOKEN }")
	fb, _, _ := catalogBuildCheckout(t, repo, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})

	if _, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if buildPosts(fb) != 1 {
		t.Errorf("the entry's build was submitted %d times, want the one a repository declaring two secrets on a variable gets", buildPosts(fb))
	}
	if got := catalogBuildSubs(t, fb)["_SECRET_ENVS"]; got != "" {
		t.Errorf("_SECRET_ENVS = %v, want empty: no workflow secret reaches the build, so no variable is carried twice either", got)
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

// TestCatalogBuildBakesTheMappedReposCheckout: the bake step (IS8b) under
// D2301. A project that maps a repository onto an entry gets that repository's
// checkout baked into the entry's image — the definition the bucket holds is the
// entry's own Dockerfile with one generated stage on the end, cloned through the
// build's own credential — and the smoke the build submits reads the checkout
// where the stage put it, so an image that bakes is one the build verified
// rather than one it stopped looking at.
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

// TestBuildCatalogBakesByProvider: the bake reads the repository-to-image map by
// provider (D3370, R9). A project that names acme/app at both providers holds two
// keys, and a build of the Bitbucket checkout is the Bitbucket one: the GitHub
// key serves no repository this build clones, so it is skipped and neither adds
// a checkout of the other provider nor refuses the bake the map does ask for.
func TestBuildCatalogBakesByProvider(t *testing.T) {
	_, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	f.appendConfig(t, "repo_images:\n  bitbucket/acme/app: tools\n  github/acme/app: tools\nbake_repos: true\n")

	_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	dir := "/work/baked/" + mustSlug(gitprov.KindBitbucket, "acme/app")
	got, _ := catalogCopy(t, gcs, "tools")
	if baked, ok := imagebake.BakedDir([]byte(got)); !ok || baked != dir {
		t.Errorf("the copy bakes at %q, %v; want the Bitbucket checkout at %s: the map's Bitbucket key names this build's repository", baked, ok, dir)
	}
	// One stage: the GitHub key is a provider this build holds no credential for,
	// and the project serves acme/app at Bitbucket alone (R9).
	if n := strings.Count(got, "# fugaro bake: "); n != 1 {
		t.Errorf("the copy carries %d bake stages, want the one the map names at this build's provider:\n%s", n, got)
	}
	if !strings.Contains(stderr, "bakes the checkout of acme/app at "+dir) {
		t.Errorf("stderr = %q, want the one baked checkout the map's own provider names", stderr)
	}
}

// catalogBuildCredentialArg is the git credential the derived template hands a
// build step, as the submitted request spells it: gcp's
// catalogTemplateCredentialArg, whose text that package's own test pins
// (TestCloneStepIsGeneratedAndSeparateFromTheEntryBuild). This is the request's
// bytes and not the constant, because a CLI test reads what was posted.
const catalogBuildCredentialArg = `--secret "id=git-credentials,src=/creds/git-credentials"`

// buildStepArgs is the request's step id's arguments read as one script: an
// argument is quoted text and the generated steps carry heredocs, so what a step
// does is what its arguments say together, not what the marshaled JSON says.
func buildStepArgs(t *testing.T, fb *gcpfake.Build, id string) string {
	t.Helper()
	step := catalogBuildStep(t, fb, id)
	if step == nil {
		t.Fatalf("the last request has no %s step: %v", id, catalogBuildStepIDs(t, fb))
	}
	script := ""
	args, _ := step["args"].([]any)
	for _, raw := range args {
		s, _ := raw.(string)
		script += s + "\n"
	}
	return script
}

// catalogBuildStepCredential is whether the last submitted build's own build
// step is handed the git credential: the argument above and the fugaro-creds
// volume at /creds that holds the file it names. The two go together — the
// argument names a file only that volume puts in the step — so a request that
// carries one and not the other fails here rather than passing as a half.
func catalogBuildStepCredential(t *testing.T, fb *gcpfake.Build) bool {
	t.Helper()
	step := catalogBuildStep(t, fb, "build")
	script := buildStepArgs(t, fb, "build")
	held := strings.Contains(script, catalogBuildCredentialArg)
	vols, _ := step["volumes"].([]any)
	mounts := false
	for _, raw := range vols {
		v, _ := raw.(map[string]any)
		if v["name"] == "fugaro-creds" && v["path"] == "/creds" {
			mounts = true
		}
	}
	if held != mounts {
		t.Errorf("the build step's credential argument is there = %v and its fugaro-creds volume = %v: the argument names a file only that volume holds: %q", held, mounts, script)
	}
	return held
}

// TestCatalogBuildHandsNoCredentialToTheEntrysBuild: whatever the copy bakes,
// the entry's own build step is secret-free — the clone step's build is the one
// that holds the git credential, and the credential reaches no build that runs
// the entry's definition (B8, F19, D2311). What the copy's own bytes decide
// (gcp.CatalogBuildSpec.Baked) is what that clone step's build clones: a
// definition that bakes gets one baked image, whose stages the entry's build
// only copies out, and every other definition gets none.
//
// This is the CLI half of the wiring; the gcp half is
// TestBakedDefinitionClonesInTheCloneStep. It is the one check 390 needed: a
// submission that never read Baked off the copy composed a baked entry's clone
// step with no baked image, and the entry's build failed on the COPY the
// definition's own stages carry.
func TestCatalogBuildHandsNoCredentialToTheEntrysBuild(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	if _, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if catalogBuildStepCredential(t, fb) {
		t.Error("the entry's own build is handed the git credential: every entry's build is secret-free, and the credential reaches the clone step's build alone (B8, F19, D2311)")
	}
	if got := buildStepArgs(t, fb, "catalog-clone"); strings.Contains(got, "--target baked") {
		t.Errorf("the clone step builds a baked image: this definition bakes nothing: %q", got)
	}

	// The project's map puts this build's own repository on the entry with the
	// switch on, so the copy bakes this checkout: the clone step's build is the
	// one that clones it and tags the image the definition copies from.
	f.appendConfig(t, "image_map:\n  acme/app: tools\nbake_repos: true\n")
	if _, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if catalogBuildStepCredential(t, fb) {
		t.Error("the entry's build is handed the git credential: the stages this copy carries copy out of the clone step's image and run no RUN of the entry's own")
	}
	got := buildStepArgs(t, fb, "catalog-clone")
	if !strings.Contains(got, "--target baked") || !strings.Contains(got, "--tag fugaro-baked") {
		t.Errorf("the clone step does not build the baked image the definition's stages copy from: %q", got)
	}
	if !strings.Contains(got, "COVERED='bitbucket.org/acme/app'") {
		t.Errorf("the clone step's covered set does not name this build's own repository: %q", got)
	}
}

// TestCatalogBuildBakesNothingWithoutTheMap: a checkout that the project's map
// does not put on this entry — every installation before this release, and one
// whose operator answered the switch no — is byte for byte what it was: no
// stage is composed, the copy is the entry's own definition, the smoke is the
// spec it was, and no line about the bake is printed. A repository the map
// does put on a quiet entry is baked (that is the default's own case). The
// other two ways a build bakes nothing are the ones the map cannot answer for,
// and TestCatalogBuildRefusesAnUnadmittedRepo and
// TestCatalogBuildRefusesABakeTheMapDoesNotConfirm pin them: an entry the map
// puts a repository the project does not admit on, and a checkout that names
// this entry by its own workflow's reference with no map to say who else runs
// it.
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

// addRepoToProject adds a repository to the project's own `repos` in the config
// the fixture wrote, beside the acme/app row catalogBuildCheckoutFor placed
// there — which is the row this anchors on, spelled as that fixture spelled it
// for the provider and App ID. The G4 fixture names one repository, and the map
// tests need a second one: the map is read against the project's own list, so a
// key the project does not admit and a key it does are two different answers
// (imagebake.Decide).
func addRepoToProject(t *testing.T, name, provider, appID string) {
	t.Helper()
	// appID is what the fixture's own acme/app row carries, which only a GitHub
	// repository has (infra.Repo refuses one without it).
	id := ""
	if appID != "" {
		id = ", github_app_id: \"" + appID + "\""
	}
	appRepo := "  acme/app: { provider: " + provider + id + ", base_branch: main, workflows: [web] }"
	added := "  " + name + ": { provider: " + provider + id + ", workflows: [web] }"
	path := os.Getenv("FUGARO_CONFIG")
	cfg := readFileString(t, path)
	updated := strings.Replace(cfg, appRepo, appRepo+"\n"+added, 1)
	if updated == cfg {
		t.Fatal("the fixture config's repos entry has changed shape")
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCatalogBuildRefusesAnUnadmittedRepo: the map is a config value and a
// published node, and it is the build's own evidence about the repositories the
// entry serves — the build reads one repository's config. So a key the project's
// own list does not answer for is a repository this build holds no URL and no
// provider for, and the bake is refused by name instead of composed from a URL
// the build would have to guess (D2301). The same map with the repository in the
// project's list is the bake D2301 asks for: every repository the map puts on
// the entry, in one image, the build's own last.
func TestCatalogBuildRefusesAnUnadmittedRepo(t *testing.T) {
	const shared = "image_map:\n  acme/app: tools\n  acme/other: tools\nbake_repos: true\n"
	_, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	f.appendConfig(t, shared)

	_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	got, _ := catalogCopy(t, gcs, "tools")
	if got != catalogBuildInline {
		t.Errorf("the copy is %q, want the entry's own definition %q: the build clones no repository the project does not admit", got, catalogBuildInline)
	}
	if !strings.Contains(stderr, "no checkout baked (the map puts acme/other on it, which is not a repository this project admits)") {
		t.Errorf("stderr = %q, want the refusal naming the repository the map puts on the entry and the project does not admit", stderr)
	}

	t.Run("the repository admitted", func(t *testing.T) {
		fb, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
		addRepoToProject(t, "acme/other", gitprov.KindBitbucket, "")
		f.appendConfig(t, shared)

		_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
		if err != nil {
			t.Fatalf("%v (%s)", err, stderr)
		}
		got, _ := catalogCopy(t, gcs, "tools")
		appDir, err := imagebake.Path(mustSlug(gitprov.KindBitbucket, "acme/app"))
		if err != nil {
			t.Fatal(err)
		}
		otherDir, err := imagebake.Path(mustSlug(gitprov.KindBitbucket, "acme/other"))
		if err != nil {
			t.Fatal(err)
		}
		// One stage per repository, and the marker each one opens with is what
		// the clone step clones: the directory the checkout is baked at, the
		// URL it clones from and the branch it starts at (imagebake.copyStage).
		// Both are spelled out here: the generated stage carries no RUN and no
		// build argument, because the entry's own build holds no credential and
		// the clone is the clone step's (B8, F19).
		for _, want := range []string{
			"# fugaro bake: " + appDir + " https://bitbucket.org/acme/app.git main",
			"# fugaro bake: " + otherDir + " https://bitbucket.org/acme/other.git main",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("the copy has no stage with %q:\n%s", want, got)
			}
		}
		// The definition still opens with the entry's own content, and the
		// build's own repository is the last stage — the one the tag, the smoke
		// and the nightly rebuild read the definition by (BakedDir).
		if !strings.HasPrefix(got, catalogBuildInline) {
			t.Errorf("the copy is %q, want the entry's own definition first", got)
		}
		if baked, ok := imagebake.BakedDir([]byte(got)); !ok || baked != appDir {
			t.Errorf("the copy bakes its last checkout at %q, %v; want %s: the build's own repository is the last stage", baked, ok, appDir)
		}
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
		if smoke.RepoDir != appDir || smoke.Commit != commit || smoke.Origin != "https://bitbucket.org/acme/app.git" {
			t.Errorf("the smoke's spec is %+v, want the baked checkout of the build's own repository at %s on %s", smoke, appDir, commit)
		}
		// Both repositories are named with the directory each was baked at: the
		// operator asked for a bake of the entry, and what the image carries is
		// every repository the project's map puts on it (D2301).
		for _, want := range []string{"bakes the checkouts of", "acme/app at " + appDir, "acme/other at " + otherDir} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr = %q, want the line about both baked checkouts to carry %q", stderr, want)
			}
		}
	})
}

// TestCatalogBuildClonesABakedRepoFromTheProjectsCloneURL: a repository's clone
// URL is the project's to choose (`clone_urls`, SD5/D842) and not the provider's
// to imply, so the stage composed for one of the entry's other repositories
// clones from the URL the project gives it — the URL the marker line names,
// which is what the clone step clones. The two blocks the URL is read out of are
// keyed differently — `repos` and `image_map` by "acme/other", `clone_urls` by
// "bitbucket/acme/other" (projectpolicy.Canonical) — and a lookup in the wrong
// key space is silent: CloneURL answers the provider's usual host for a name the
// project gives no URL, so the build would clone from a URL nobody chose and
// nothing would fail.
func TestCatalogBuildClonesABakedRepoFromTheProjectsCloneURL(t *testing.T) {
	const shared = "image_map:\n  acme/app: tools\n  acme/other: tools\nbake_repos: true\n"
	// The project's URL for acme/other, which is this repository on its own
	// provider's host and spelled without the ".git" the usual one carries —
	// the one spelling a clone_urls entry may differ by, since a URL on another
	// host is refused by that block's own validation and by the one credential
	// the build asks for (repourl.CloneURL, imagebake.Decide).
	const urls = "clone_urls:\n  bitbucket/acme/other: https://bitbucket.org/acme/other\n"
	_, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	addRepoToProject(t, "acme/other", gitprov.KindBitbucket, "")
	f.appendConfig(t, shared+urls)

	_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	got, _ := catalogCopy(t, gcs, "tools")
	otherDir, err := imagebake.Path(mustSlug(gitprov.KindBitbucket, "acme/other"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "# fugaro bake: " + otherDir + " https://bitbucket.org/acme/other main"; !strings.Contains(got, want) {
		t.Errorf("the copy has no stage with %q: a baked repository clones from the URL the project gives it (clone_urls)\n%s", want, got)
	}
	if strings.Contains(got, "https://bitbucket.org/acme/other.git") {
		t.Errorf("the copy clones acme/other from the provider's usual URL, not from the project's clone_urls entry:\n%s", got)
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
	stale, err := imagebake.Append([]byte(catalogBuildInline), imagebake.Plan{Baked: []imagebake.Repo{{
		Repo: "acme/app", Slug: slug, URL: "https://bitbucket.org/acme/app.git", Branch: "main",
	}}})
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

// recitingBuildToken is the credentials service's build route as a test sees
// it: it records the request it was handed and answers what the test gave it.
// What ships asks the real service and records nothing, so the entry mode's own
// question — the entry, the build, the repositories and the lifetime it names —
// is held here by a client that can be wrong about it.
type recitingBuildToken struct {
	request buildtoken.Request
	answer  buildtoken.Response
	err     error
}

func (c *recitingBuildToken) Mint(_ context.Context, req buildtoken.Request) (buildtoken.Response, error) {
	c.request = req
	return c.answer, c.err
}

// installBuildToken points the entry mode at c for one test, and puts the
// package's own value back afterwards: what ships is the call itself
// (gitcred_build.go), so a test that wants to hold the build's question still
// replaces it and hands it back.
func installBuildToken(t *testing.T, c buildtoken.Client) {
	t.Helper()
	prev := gitCredBuildToken
	t.Cleanup(func() { gitCredBuildToken = prev })
	gitCredBuildToken = func(*cobra.Command) (buildtoken.Client, error) { return c, nil }
}

// TestCredentialIsReadFromTheNamedEntrysRepos: the bake's credential is asked
// for once, as one request for the entry being built, and what it names is the
// repositories the build composes stages for — the entry, the build, the
// provider, the repositories in the order the plan baked them (deduplicated and
// canonicalised, because a config left a list and the service's key space is the
// canonical name), and the lifetime the step needs. The credential written is
// the answer's own, for the host the answer names and no other. And a build that
// cannot make the request, or is answered something that is not one token for
// the provider's host, writes no file at all: a credential file is what git
// offers to whatever host its line names, so a placeholder or a second answer
// would be a credential the build never had (D2301, F19).
func TestCredentialIsReadFromTheNamedEntrysRepos(t *testing.T) {
	gitCredentialEnv(t)
	out := filepath.Join(t.TempDir(), "git-credentials")
	answer := buildtoken.Response{Token: &buildtoken.Token{
		Value: "ghs_fixture_token", User: "x-access-token", Host: "github.com",
		ExpiresAt: time.Now().Add(time.Hour),
	}}
	// The shape of a Cloud Build id (internal/backend/gcp/build.go's buildIDRE),
	// which is what the service keys the Bitbucket pairing on.
	const buildID = "8f0c1a2e-1234-4abc-9def-0123456789ab"
	// The flags one request is made of, with the cases below overriding whatever
	// they are asking about (a repeated flag's last value is the one it takes).
	entryArgs := func(out string, extra ...string) []string {
		return append(append([]string{"image", "git-credential", "--provider", "github", "--entry", "tools",
			"--build-id", buildID, "--ttl-seconds", "900"}, "--out", out), extra...)
	}
	run := func(extra ...string) (string, string, error) {
		return execute(t, entryArgs(out, extra...)...)
	}

	c := &recitingBuildToken{answer: answer}
	installBuildToken(t, c)
	stdout, stderr, err := run("--repos", "Acme/Web", "--repos", "acme/app", "--repos", "acme/web")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	// One request, and every field of it the build's own: the entry it composes
	// a definition for, the build it runs as, the provider, the plan's
	// repositories, and the lifetime the clone step asked for.
	want := buildtoken.Request{
		Entry: "tools", BuildID: buildID,
		Provider: gitprov.KindGitHub, Repositories: []string{"acme/app", "acme/web"}, TTLSeconds: 900,
	}
	if !reflect.DeepEqual(c.request, want) {
		t.Errorf("the build asked for %+v, want %+v", c.request, want)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("git-credential printed %q / %q: its output is a build log, and the request's fields and a token are not for one", stdout, stderr)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// The answer's user, token and host, and nothing of the request: a step that
	// wrote a request field into the line would be writing something other than
	// the credential it was given.
	if wantLine := "https://x-access-token:ghs_fixture_token@github.com\n"; string(data) != wantLine {
		t.Errorf("the credential file holds %q, want %q", data, wantLine)
	}
	if fi, err := os.Stat(out); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v, want 0600: the file is a credential the build clones with", fi.Mode(), err)
	}

	// A request the build cannot make is refused before anything is asked, and a
	// request it can make is answered by a route that exists: a step that sent
	// what it could and let the service sort it out would be asking the service
	// to be the only thing between a build and a widening the build itself had
	// no business asking for.
	t.Run("a request the build cannot make", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			args func(out string) []string
			want string
		}{
			{
				"the entry mode's flags with no entry",
				func(out string) []string {
					return []string{"image", "git-credential", "--provider", "github", "--build-id", buildID,
						"--ttl-seconds", "900", "--repos", "acme/app", "--out", out}
				},
				"--entry is not set",
			},
			{
				"no build id",
				func(out string) []string {
					return entryArgs(out, "--build-id", "", "--repos", "acme/app")
				},
				"--build-id is required with --entry",
			},
			{
				"no lifetime",
				func(out string) []string { return entryArgs(out, "--ttl-seconds", "0", "--repos", "acme/app") },
				"--ttl-seconds is required with --entry",
			},
			{
				"no repositories",
				func(out string) []string { return entryArgs(out, "--repos", "") },
				"--repos is required with --entry",
			},
			{
				"no out",
				func(out string) []string { return entryArgs(out, "--out", "", "--repos", "acme/app") },
				"--out is required",
			},
			{
				// The one-repository mode's flag: a URL here would be a second,
				// narrower request beside the entry's, and the build has one
				// credential.
				"a clone URL",
				func(out string) []string {
					return entryArgs(out, "--repo-url", "https://github.com/acme/app.git", "--repos", "acme/app")
				},
				"--repo-url is the one-repository mode's",
			},
			{
				// A value a config left in a list: it is refused here and never
				// reaches the request, because the service's key space is the
				// canonical name (task.CanonicalRepo).
				"a name that is not a repository",
				func(out string) []string { return entryArgs(out, "--repos", "acme") },
				"--repos",
			},
			{
				"a name that walks out of the key space",
				func(out string) []string { return entryArgs(out, "--repos", "acme/../../etc/passwd") },
				"--repos",
			},
			{
				"an entry that is not a catalog name",
				func(out string) []string { return entryArgs(out, "--entry", "../tools", "--repos", "acme/app") },
				"catalog entry",
			},
			{
				"a provider that is not one",
				func(out string) []string { return entryArgs(out, "--provider", "gitlab", "--repos", "acme/app") },
				"--provider",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				installBuildToken(t, &recitingBuildToken{answer: answer})
				out := filepath.Join(t.TempDir(), "git-credentials")
				stdout, stderr, err := execute(t, tc.args(out)...)
				if ExitCode(err) != ExitUserError {
					t.Fatalf("exit %d, err %v: the build's own mistake is the build's to fix", ExitCode(err), err)
				}
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("err = %v, want it to say %q", err, tc.want)
				}
				if stdout != "" || stderr != "" {
					t.Errorf("printed %q / %q, want nothing", stdout, stderr)
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Errorf("a credential file was written for a request the build cannot make")
				}
			})
		}

		// A build whose seam holds no route at all: the entry mode refuses
		// rather than cloning with something this process can mint for itself,
		// which would be a credential the project's build identity never
		// granted. What ships installs the call (gitcred_build.go), so this is
		// the guard on a nil seam and not a state of the binary — the test that
		// holds what the shipped client does with the environment is
		// TestCatalogBuildBakesTwoReposThroughTheService.
		t.Run("no route in this binary", func(t *testing.T) {
			prev := gitCredBuildToken
			gitCredBuildToken = nil
			t.Cleanup(func() { gitCredBuildToken = prev })
			out := filepath.Join(t.TempDir(), "git-credentials")
			_, _, err := execute(t, entryArgs(out, "--repos", "acme/app")...)
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no route to the credentials service") {
				t.Errorf("exit %d, err %v: want the build told it cannot ask", ExitCode(err), err)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("a credential file was written with no route to ask")
			}
		})
	})

	// The service's own refusal is passed on as it is and never turned into a
	// credential: the repository it names is the service's answer, and a step that
	// retried or widened would be the reach this contract exists to bound (B1).
	t.Run("the route refuses", func(t *testing.T) {
		installBuildToken(t, &recitingBuildToken{err: errors.New("acme/other is not a repository of the entry tools")})
		out := filepath.Join(t.TempDir(), "git-credentials")
		_, _, err := execute(t, entryArgs(out, "--repos", "acme/app")...)
		if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "acme/other is not a repository of the entry tools") {
			t.Errorf("exit %d, err %v: want the service's refusal passed on", ExitCode(err), err)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("a credential file was written for a request the service refused")
		}
	})

	// An answer that is not exactly one token for the provider's own host is
	// refused and nothing is written. A credential file is read by git for the
	// host in its line, so a token written for a host the answer named would be
	// offered to a server that never minted it; a Bitbucket build is answered a
	// proxy grant and holds no token at all (R6), so there is nothing here to
	// write for one; and the contract says exactly one shape, so an answer that is
	// neither or both is not something a step may guess its way through.
	t.Run("the answer is not one token", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			answer buildtoken.Response
			want   string
		}{
			{
				"another host",
				buildtoken.Response{Token: &buildtoken.Token{Value: "ghs_tok3n", User: "x-access-token", Host: "evil.example"}},
				"clones from github.com",
			},
			{
				"no host",
				buildtoken.Response{Token: &buildtoken.Token{Value: "ghs_tok3n", User: "x-access-token"}},
				"clones from github.com",
			},
			{
				"a provider's other host",
				buildtoken.Response{Token: &buildtoken.Token{Value: "ghs_tok3n", User: "x-access-token", Host: "bitbucket.org"}},
				"clones from github.com",
			},
			{
				"a proxy grant",
				buildtoken.Response{Proxy: &buildtoken.ProxyGrant{URL: "https://creds.example/proxy", Repositories: []string{"acme/app"}}},
				"proxy grant",
			},
			{
				"a token and a grant",
				buildtoken.Response{
					Token: &buildtoken.Token{Value: "ghs_tok3n", User: "x-access-token", Host: "github.com"},
					Proxy: &buildtoken.ProxyGrant{URL: "https://creds.example/proxy"},
				},
				"exactly one",
			},
			{
				"neither",
				buildtoken.Response{},
				"neither a token nor a proxy grant",
			},
			{
				"no user",
				buildtoken.Response{Token: &buildtoken.Token{Value: "ghs_tok3n", Host: "github.com"}},
				"incomplete",
			},
			{
				"no token",
				buildtoken.Response{Token: &buildtoken.Token{User: "x-access-token", Host: "github.com"}},
				"incomplete",
			},
			{
				"a control character in the token",
				buildtoken.Response{Token: &buildtoken.Token{Value: "ghs_tok3n\nx", User: "x-access-token", Host: "github.com"}},
				"control character",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				installBuildToken(t, &recitingBuildToken{answer: tc.answer})
				out := filepath.Join(t.TempDir(), "git-credentials")
				stdout, stderr, err := execute(t, entryArgs(out, "--repos", "acme/app")...)
				if ExitCode(err) != ExitUserError {
					t.Fatalf("exit %d, err %v: an answer the build cannot use is refused", ExitCode(err), err)
				}
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("err = %v, want it to say %q", err, tc.want)
				}
				// What is refused is never quoted back: the answer is where a
				// credential is, and a refusal goes to the build log.
				if err != nil && strings.Contains(err.Error(), "tok3n") {
					t.Errorf("the refusal carries the token: %v", err)
				}
				if stdout != "" || stderr != "" {
					t.Errorf("printed %q / %q, want nothing", stdout, stderr)
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Errorf("a credential file was written for an answer the build cannot use")
				}
			})
		}
	})
}

// wipCommit adds a commit to the checkout the test is running in and returns
// it: a commit the fixture's remote does not have, which is the state a build
// of an entry pinned to the checkout's HEAD is refused in.
func wipCommit(t *testing.T) string {
	t.Helper()
	testutil.WriteFiles(t, ".", map[string]string{"wip.txt": "wip\n"})
	testutil.Git(t, ".", "add", "wip.txt")
	testutil.Git(t, ".", "commit", "--quiet", "-m", "wip")
	return testutil.Git(t, ".", "rev-parse", "HEAD")
}

// stubRemoteHasCommit answers the remote half of the unpushed-commit check
// with found, for a test whose fixture's origin is a URL no git in the test
// can read.
func stubRemoteHasCommit(t *testing.T, found bool) {
	t.Helper()
	was := remoteHasCommit
	remoteHasCommit = func(context.Context, string, string, string) (bool, error) { return found, nil }
	t.Cleanup(func() { remoteHasCommit = was })
}

// stubRemoteUnreadable answers the remote half of the unpushed-commit check
// with the error of a remote git cannot reach, which is inconclusive rather
// than an answer about the commit.
func stubRemoteUnreadable(t *testing.T) {
	t.Helper()
	was := remoteHasCommit
	remoteHasCommit = func(context.Context, string, string, string) (bool, error) {
		return false, errors.New("could not read from remote repository")
	}
	t.Cleanup(func() { remoteHasCommit = was })
}

// TestCatalogBuildRefusesACommitOnlyInTheCheckout: a build clones the commit
// it is pinned to, so a checkout whose HEAD the remote does not have is
// refused where that is known — before the entry's definition is copied, a
// lease taken or a build submitted — rather than billed and failed two minutes
// later on the clone step's fetch (the owner's build of 2026-11-23).
func TestCatalogBuildRefusesACommitOnlyInTheCheckout(t *testing.T) {
	fb, gcs, _ := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	stubRemoteHasCommit(t, false)
	commit := wipCommit(t)

	_, _, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v: a commit only this checkout has is refused", ExitCode(err), err)
	}
	for _, want := range []string{commit, "exists only in this checkout", "push the branch", "git switch main && git pull --ff-only"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %v, want it to say %q", err, want)
		}
	}
	if n := buildPosts(fb); n != 0 {
		t.Errorf("the refusal submitted %d Cloud Build builds, want none", n)
	}
	if !catalogCopyAbsent(t, gcs, "tools") {
		t.Error("the refusal wrote the entry's definition into the runs bucket")
	}
}

// TestCatalogBuildProceedsWhenTheRemoteHasTheCommit: the check asks the remote
// and not this machine's remote-tracking refs alone, so a commit the checkout
// holds no ref of — fetched into it by hand, a pull request's head, one pushed
// from another checkout — is submitted exactly as it was before this check
// existed.
func TestCatalogBuildProceedsWhenTheRemoteHasTheCommit(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	f.sm.AllowAccess()
	putSecretID(t, f.sm, mustSlug(gitprov.KindBitbucket, "acme/app"), config.BitbucketSecret, []byte(catalogTokenPlaceholder))
	stubRemoteHasCommit(t, true)
	wipCommit(t)

	if _, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if n := buildPosts(fb); n != 1 {
		t.Errorf("a commit the remote has submitted %d builds, want one", n)
	}
}

// TestCatalogBuildProceedsWhenTheRemoteCannotBeRead: only a definite answer
// refuses. A remote git cannot read — no network, a host that does not answer,
// a credential that is not there — says nothing about the commit, and a build
// refused over it would be one that would have worked, so today's behaviour
// stands and the build is submitted.
func TestCatalogBuildProceedsWhenTheRemoteCannotBeRead(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	f.sm.AllowAccess()
	putSecretID(t, f.sm, mustSlug(gitprov.KindBitbucket, "acme/app"), config.BitbucketSecret, []byte(catalogTokenPlaceholder))
	stubRemoteUnreadable(t)
	wipCommit(t)

	if _, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if n := buildPosts(fb); n != 1 {
		t.Errorf("a remote that could not be read submitted %d builds, want one", n)
	}
}

// TestCatalogBuildIsNotRefusedOverACommitItDoesNotClone: only a build pinned
// to the checkout's HEAD is refused when the remote does not have it. A
// prebuilt entry's run smokes the reference its own entry names and clones
// nothing, and a context entry's build is pinned to the commit its copy
// resolved (D1395), so a checkout standing on a wip commit still submits both.
func TestCatalogBuildIsNotRefusedOverACommitItDoesNotClone(t *testing.T) {
	t.Run("a prebuilt entry is smoked", func(t *testing.T) {
		fb, _, _ := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
		stubRemoteHasCommit(t, false)
		wipCommit(t)
		if _, stderr, err := executeBuild(t, "image", "build", "prebuilt", "--base", "b:1"); err != nil {
			t.Fatalf("%v (%s)", err, stderr)
		}
		if n := buildPosts(fb); n != 1 {
			t.Errorf("smoking a prebuilt entry submitted %d builds, want one", n)
		}
	})
	t.Run("a context entry is built at its own commit", func(t *testing.T) {
		fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildContextYAML)
		stubRemoteHasCommit(t, false)
		wipCommit(t)
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
		if n := buildPosts(fb); n != 1 {
			t.Errorf("building a context entry submitted %d builds, want one", n)
		}
	})
}

// TestCommitOnRemoteAnswersFromTheRefsAndThenTheRemote: the check reads the
// checkout's own remote-tracking refs first — the shape a commit pushed from
// this checkout is in — and asks the remote itself only where none of them
// contains the commit, which is where a commit pushed from another checkout,
// or fetched into this one without a ref, is found.
func TestCommitOnRemoteAnswersFromTheRefsAndThenTheRemote(t *testing.T) {
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"a.txt": "a\n"})
	dir := filepath.Join(t.TempDir(), "app")
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", remote, dir)
	ctx := context.Background()
	head := testutil.Git(t, dir, "rev-parse", "HEAD")
	if found, known := commitOnRemote(ctx, dir, head); !found || !known {
		t.Errorf("the clone's own HEAD (%s) is read as found=%v known=%v, want both true", head, found, known)
	}
	// A commit pushed from elsewhere: the remote publishes it as a branch tip
	// and this checkout holds no remote-tracking ref of that branch.
	testutil.WriteFiles(t, dir, map[string]string{"b.txt": "b\n"})
	testutil.Git(t, dir, "add", "b.txt")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "elsewhere")
	pushed := testutil.Git(t, dir, "rev-parse", "HEAD")
	testutil.Git(t, dir, "push", "--quiet", "origin", "HEAD:refs/heads/elsewhere")
	testutil.Git(t, dir, "update-ref", "-d", "refs/remotes/origin/elsewhere")
	if found, known := commitOnRemote(ctx, dir, pushed); !found || !known {
		t.Errorf("a commit the remote publishes (%s) is read as found=%v known=%v, want both true", pushed, found, known)
	}
	// A commit only this checkout has: nothing local contains it and the remote
	// publishes no ref pointing at it.
	testutil.WriteFiles(t, dir, map[string]string{"c.txt": "c\n"})
	testutil.Git(t, dir, "add", "c.txt")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "wip")
	wip := testutil.Git(t, dir, "rev-parse", "HEAD")
	if found, known := commitOnRemote(ctx, dir, wip); found || !known {
		t.Errorf("the wip commit %s is read as found=%v known=%v, want found=false known=true", wip, found, known)
	}
}
