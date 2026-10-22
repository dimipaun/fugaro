package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// Check 40 step 40i's bug, end to end through the engine the owner ran: a
// model provider the installation shares project-wide (design
// docs/design/project-secrets.md §5), its key stored in the project
// container, and the repository's own container -- what Terraform's module
// creates -- holding no version at all. The name is covered, so the run is
// ready: nothing under "Still missing", nothing in the result's missing list
// (--json reads the same one), and the first image build is offered at all
// (infra.NeedsBuild), which is the gate the bug also closed.
func TestInitRepoProjectWideProviderKeyIsReady(t *testing.T) {
	r := newProjectKeyRig(t, `["*"]`)
	r.seedRepoSecret(t, "bitbucket-token", []byte("tok"))
	// The repository's own container exists with NO version, as Terraform
	// leaves it; the project copy holds the value.
	r.seedRepoSecret(t, "probe2-api-key", nil)
	r.seedProjectKey(t, "probe2-api-key")
	fb := useFakeBuilder(t)
	e := rigEngine(t, r, &initOptions{})
	out := atTerminal(t, e, strings.Repeat(initProjectName+"\n", 3))
	if err := e.r.repoEngine(t.Context(), ".", "", false); err != nil {
		t.Fatalf("the project-covered key left the run unfinished: %v\n%s", err, out.String())
	}
	if len(fb.specs) != 1 || fb.specs[0].Workflow != "app" {
		t.Fatalf("specs = %+v, want the app workflow's first build: a covered key must not strand it", fb.specs)
	}
	assertNoProjectKeyMissing(t, out.String(), e.r.res.Missing)
}

// The same repository under --yes, with its image already built (a build
// record): no terminal, no typed input, and the run is ready -- exit 0, and
// nothing for the name in the output.
func TestInitRepoProjectWideProviderKeyIsReadyUnderYes(t *testing.T) {
	r := newProjectKeyRig(t, `["*"]`)
	r.seedRepoSecret(t, "bitbucket-token", []byte("tok"))
	r.seedRepoSecret(t, "probe2-api-key", nil)
	r.seedProjectKey(t, "probe2-api-key")
	r.seedBuildRecord(t, "app")
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--yes")
	if err != nil {
		t.Fatalf("the project-covered key left the --yes run unfinished: %v\n%s\n%s", err, out, errOut)
	}
	assertNoProjectKeyMissing(t, out+errOut, nil)
}

// The other direction, unchanged: the repository's own stored copy is what
// resolves, and the run is ready too.
func TestInitRepoRepoCopyStillWinsAndIsReady(t *testing.T) {
	r := newProjectKeyRig(t, `["*"]`)
	r.seedRepoSecret(t, "bitbucket-token", []byte("tok"))
	r.seedRepoSecret(t, "probe2-api-key", []byte("repo-value"))
	r.seedProjectKey(t, "probe2-api-key")
	r.seedBuildRecord(t, "app")
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, out, errOut)
	}
	assertNoProjectKeyMissing(t, out+errOut, nil)
}

// Neither copy is stored: the name is still the real missing case, and the
// command printed beside it stores the project-wide copy -- the bare command,
// since D2 made project scope the default store -- never the repository copy
// that would then win forever over it.
func TestInitRepoProjectWideProviderKeyUnstoredIsMissing(t *testing.T) {
	r := newProjectKeyRig(t, `["*"]`)
	r.seedRepoSecret(t, "bitbucket-token", []byte("tok"))
	r.seedRepoSecret(t, "probe2-api-key", nil)
	e := rigEngine(t, r, &initOptions{})
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.repoEngine(t.Context(), ".", "", false); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := missingLine(e.r.res.Missing, "probe2-api-key"); !strings.Contains(got, "secrets set probe2-api-key") || strings.Contains(got, "--repo") {
		t.Errorf("missing = %q, want the project-wide command, never a repository copy", got)
	}
	if !strings.Contains(out.String(), "Still missing for acme/sandbox:") {
		t.Errorf("no missing block:\n%s", out.String())
	}
}

// A project container whose only version is DISABLED covers nothing: the
// name is missing, with the same project-wide fix.
func TestInitRepoProjectWideProviderKeyDisabledIsMissing(t *testing.T) {
	r := newProjectKeyRig(t, `["*"]`)
	r.seedRepoSecret(t, "bitbucket-token", []byte("tok"))
	r.seedRepoSecret(t, "probe2-api-key", nil)
	r.sm.DisableVersions(r.seedProjectKey(t, "probe2-api-key"))
	e := rigEngine(t, r, &initOptions{})
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.repoEngine(t.Context(), ".", "", false); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := missingLine(e.r.res.Missing, "probe2-api-key"); !strings.Contains(got, "secrets set probe2-api-key") || strings.Contains(got, "--repo") {
		t.Errorf("missing = %q, want the project-wide command", got)
	}
}

// A name the allow-list does not name this repository for gets no project
// coverage at all, whatever its project container holds: it is still missing,
// and its fix is the repository's own copy. bitbucket-token is the reserved
// name every Bitbucket repository mounts, so the exclusion is what the run
// reads (a provider the allow-list excludes is not mounted at all).
func TestInitRepoProjectSecretUnlistedRepoIsMissing(t *testing.T) {
	r := newProjectKeyRig(t, `["*"]`)
	r.appendConfig(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [acme/other] }\n")
	r.seedRepoSecret(t, "bitbucket-token", nil)
	r.seedRepoSecret(t, "probe2-api-key", nil)
	r.seedProjectKey(t, "probe2-api-key")
	r.sm.Seed(gcp.ProjectSecretID(initProjectName, "bitbucket-token"),
		map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: initProjectName, gcp.LabelSecret: "bitbucket-token"}, []byte("v"))
	e := rigEngine(t, r, &initOptions{})
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.repoEngine(t.Context(), ".", "", false); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got := missingLine(e.r.res.Missing, "bitbucket-token")
	if !strings.Contains(got, "secrets set bitbucket-token --repo acme/sandbox") {
		t.Errorf("missing = %q, want the --repo command: the project copy is not this repository's to use", got)
	}
}

// assertNoProjectKeyMissing is the covered case's whole assertion: the name
// is absent from everything the run prints and everything it carries into
// the result (--json reads the same list), and no missing block exists.
func assertNoProjectKeyMissing(t *testing.T, out string, missing []string) {
	t.Helper()
	for _, m := range missing {
		if strings.Contains(m, "probe2-api-key") {
			t.Errorf("the project-covered key is still listed missing: %q", m)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		// The step's own shape, "name: <the repository container's ID>". The
		// config diff's "-      secret: probe2-api-key" lines are the
		// provider's own config key, not a missing list.
		if strings.Contains(l, "probe2-api-key:") || strings.Contains(l, "secrets set probe2-api-key") {
			t.Errorf("the run still says what is missing: %q", l)
		}
	}
}

// missingLine is the result line naming logical, or "".
func missingLine(missing []string, logical string) string {
	for _, m := range missing {
		if strings.Contains(m, logical) {
			return m
		}
	}
	return ""
}

// probeKeyYAML is a checkout whose only pin is served by the project-shared
// provider probe2, so a run on it never calls Anthropic (the #371 rule,
// config.AnthropicKeyNeeded under an enforcing ceiling).
const probeKeyYAML = "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\n" +
	"agent: { auth: api-key, models: { coder: probe2/probe-1, reviewer: probe2/probe-1, background: probe2/probe-1 } }\n" +
	"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// newProjectKeyRig is the rig of Check 40 step 40i's shape: an installation
// of aurora, a checkout of acme/sandbox pinning only provider probe2, and
// probe2's allow_data_to given by the caller. The project layer is served
// from a local bucket, so no test reaches a real gs:// URL.
func newProjectKeyRig(t *testing.T, allow string) *initRig {
	t.Helper()
	r := newInitRig(t)
	r.installationState(t)
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 2 }\n"+
		"providers:\n  probe2:\n    kind: anthropic-compat\n    base_url: https://probe.example\n"+
		"    auth: bearer\n    secret: probe2-api-key\n    models: [\"probe2/*\"]\n    allow_data_to: "+allow+"\n"+
		"base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.6.0}\n")
	r.script["show"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x"}]}}}`}
	r.script["output"] = map[string]any{"stdout": outputsJSON(t)}
	r.save(t)
	isolateCache(t)
	layerDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(layerDir, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "fugaro", "project-layer.yaml"), []byte(testProjectLayer), 0o644); err != nil {
		t.Fatal(err)
	}
	openLayer := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = openLayer })
	layerBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, "file://"+layerDir) }
	openRecords := openRecordBucket
	t.Cleanup(func() { openRecordBucket = openRecords })
	openRecordBucket = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "file://"+t.TempDir())
	}
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", probeKeyYAML)
	t.Chdir(dir)
	return r
}

// seedRepoSecret stores the repository's own container of logical, correctly
// labelled, with one enabled version when value is non-nil and with none
// when it is nil (what Terraform's module leaves).
func (r *initRig) seedRepoSecret(t *testing.T, logical string, value []byte) string {
	t.Helper()
	slug := mustSlug("bitbucket", "acme/sandbox")
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	id := gcp.SecretID(slug, logical)
	r.sm.Seed(id, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: logical}, value)
	return id
}

// seedProjectKey stores the project-wide container of logical, correctly
// labelled for the installation, with one enabled version, and returns its ID.
func (r *initRig) seedProjectKey(t *testing.T, logical string) string {
	t.Helper()
	id := gcp.ProjectSecretID(initProjectName, logical)
	r.sm.Seed(id, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: initProjectName, gcp.LabelSecret: logical}, []byte("project-"+logical))
	return id
}

// seedBuildRecord gives the checkout's workflow a build record in the
// installation's runs bucket, so the run has no billable build to offer.
func (r *initRig) seedBuildRecord(t *testing.T, workflow string) {
	t.Helper()
	slug := mustSlug("bitbucket", "acme/sandbox")
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
	w, err := r.gcs.Bucket(t, initRunsBucket).NewWriter(context.Background(), imagecheck.RecordKey(slug, workflow), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`{"digest":"sha256:` + strings.Repeat("a", 64) + `"}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}
