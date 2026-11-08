package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// release060 (run_layer_test.go, Task 11, merged on main) is a plausible
// build record BaseRef for TestDoctorFlagsAStaleImage: imageConfigChecks
// never looks at BaseRef, so its exact form doesn't matter here.

func layerCheck(cs []doctorCheck, id string) *doctorCheck {
	for i := range cs {
		if cs[i].ID == id {
			return &cs[i]
		}
	}
	return nil
}

// resolvedLayerFixture resolves dir's fugaro.yaml over the fixture's
// published layer exactly as doctor.go does (through fugaroYAMLCheck), for
// a test to pass into doctorLayerChecks without a second bucket round trip.
func resolvedLayerFixture(t *testing.T, ctx context.Context, lc *localcfg.Config, dir string) resolvedFile {
	t.Helper()
	_, fy, rf := fugaroYAMLCheck(ctx, dir, lc)
	if fy == nil || !fy.Valid || rf == nil {
		t.Fatalf("fugaro.yaml at %s did not resolve: %+v", dir, fy)
	}
	return *rf
}

func writeRunRecord(t *testing.T, f *cloudFixture, slug, id string, pl *runstore.ProjectLayerRecord) {
	t.Helper()
	data, err := json.Marshal(runstore.Record{Version: 1, RunID: id, Repo: "acme/other", Status: runstore.StatusSucceeded,
		StartedAt: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC), ProjectLayer: pl})
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, "runs/"+slug+"/"+id+"/result.json", string(data))
}

// Review Focus 1.
func TestDoctorFlagsLayerDrift(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	slug := mustSlug("github", "acme/other")
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)

	cs := doctorLayerChecks(ctx, lc, dir, rf)
	if c := layerCheck(cs, "project-layer"); c == nil || c.Severity != "info" || !strings.Contains(c.Problem, "project layer aurora generation") {
		t.Fatalf("checks %+v", cs)
	}
	if c := layerCheck(cs, "project-layer-copy"); c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "has no copy") {
		t.Fatalf("no copy: %+v", cs)
	}

	writeRunRecord(t, f, slug, "20261008-090000-aaaa", nil)
	if c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "project-layer-drift"); c == nil || !strings.Contains(c.Problem, "ran without the project layer") {
		t.Fatalf("a run without the layer: %+v", c)
	}
	old := strings.Repeat("a", 64)
	writeRunRecord(t, f, slug, "20261008-091000-bbbb", &runstore.ProjectLayerRecord{SHA256: old, Generation: 1, Applied: true})
	if c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "project-layer-drift"); c == nil || !strings.Contains(c.Problem, "used project layer generation 1 (sha256 aaaaaaaaaaaa)") {
		t.Fatalf("a run on an older layer: %+v", c)
	}
	writeRunRecord(t, f, slug, "20261008-092000-cccc", &runstore.ProjectLayerRecord{SHA256: config.LayerSum([]byte(testProjectLayer)), Generation: 2, Applied: true})
	writeBucketFile(t, f, config.LayerCopyKey(slug), testProjectLayer)
	cs = doctorLayerChecks(ctx, lc, dir, rf)
	if layerCheck(cs, "project-layer-drift") != nil || layerCheck(cs, "project-layer-copy") != nil {
		t.Fatalf("current run and copy still flagged: %+v", cs)
	}
}

// TestDoctorLayerDriftShowsInvalidGenerationAsDash: a run record's
// Generation is untrusted input (any run, not just a launch of a publisher's
// own binary, can carry one); a negative value never came from a real
// publisher (validGeneration, ls.go) and must print as "-", never echoed
// as-is.
//
// Mutation (run, restore): replace `gen := "-"; if validGeneration(...) {
// gen = strconv.FormatInt(...) }` with `gen :=
// strconv.FormatInt(pl.Generation, 10)` (the unvalidated form), and this
// test fails: the message shows "generation -7", not "generation -".
func TestDoctorLayerDriftShowsInvalidGenerationAsDash(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	slug := mustSlug("github", "acme/other")
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)

	writeRunRecord(t, f, slug, "20261008-091000-bbbb", &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: -7, Applied: true})
	c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "project-layer-drift")
	if c == nil || !strings.Contains(c.Problem, "used project layer generation - (sha256") {
		t.Fatalf("a negative generation must print as -: %+v", c)
	}
}

// TestDoctorLayerChecksDistrustsARewrittenOrigin: a checkout whose raw
// .git/config disagrees with what git resolves for "origin" (readOrigin's
// own "not trusted by name" case) must not make doctor compute a slug and
// read another repository's layer-drift, copy-staleness or image-config
// state from the bucket.
//
// Mutation (run, restore): change `if !ok || oi.Rewritten` to `if !ok`, and
// this test fails: the project-layer-copy warning (which only a successful
// bucket read under the spoofed slug would add) appears.
func TestDoctorLayerChecksDistrustsARewrittenOrigin(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/other.git")
	// A raw .git/config naming a different host/repo than what git itself
	// resolves for "origin" (no url.<base>.insteadOf rewrites it back):
	// readOrigin reports Rewritten.
	testutil.Git(t, dir, "config", "--local", "--add", "url.https://evil.example/acme/spoofed.git.insteadOf", "https://github.com/acme/other.git")
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)
	oi, ok := readOrigin(ctx, dir)
	if !ok || !oi.Rewritten {
		t.Fatalf("fixture did not produce a rewritten origin: %+v, ok=%v", oi, ok)
	}

	cs := doctorLayerChecks(ctx, lc, dir, rf)
	if layerCheck(cs, "project-layer-copy") != nil || layerCheck(cs, "project-layer-drift") != nil {
		t.Fatalf("a rewritten origin must not reach the bucket: %+v", cs)
	}
	if c := layerCheck(cs, "project-layer"); c == nil || c.Severity != "info" {
		t.Fatalf("the layer-applies line must still show: %+v", cs)
	}
}

func TestDoctorFlagsAStaleImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	slug := mustSlug("github", "acme/other")
	data, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/other", Workflow: config.ImplicitWorkflow, ImageConfigHash: strings.Repeat("0", 64), BaseRef: release060})
	writeBucketFile(t, f, imagecheck.RecordKey(slug, config.ImplicitWorkflow), string(data))
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)
	c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "image-config-"+config.ImplicitWorkflow)
	if c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "other image settings") || !strings.Contains(c.Fix, "fugaro image build --workflow default") {
		t.Fatalf("check %+v", c)
	}
}

// TestDoctorLayerShowsTheCachedNote: when the layer applies from the
// offline cache (the bucket unreachable, a fresh-enough cached copy stands
// in — decision L13), the project-layer line must still show the note, not
// silently drop it: "degrade with notes" (design), never degrade quietly.
//
// Mutation (run, restore): drop the `if rf.Layer.Note != "" { problem += "; "
// + rf.Layer.Note }` lines, and this test fails: the project-layer line no
// longer mentions "using the cached project layer".
func TestDoctorLayerShowsTheCachedNote(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	lc := fileEnv(t, f).lc
	ctx := context.Background()

	// Prime the cache with one real, successful read.
	data, err := readFugaroYAML(filepath.Join(dir, "fugaro.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := findLayer(ctx, os.Getenv, data, lc, layerOptions{Lenient: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Now the bucket cannot be reached at all: findLayer falls back to the
	// cache it just saved (decision L13), with a note.
	old := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = old })
	layerBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}

	rf := resolvedLayerFixture(t, ctx, lc, dir)
	if !rf.Layer.Unknown && rf.Layer.Layer == nil {
		t.Fatalf("expected a cached layer, got none: %+v", rf.Layer)
	}
	c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "project-layer")
	if c == nil || c.Severity != "info" || !strings.Contains(c.Problem, "project layer aurora generation") || !strings.Contains(c.Problem, "using the cached project layer of aurora") {
		t.Fatalf("check %+v", c)
	}
}

// TestDoctorLayerUnknownWhenBucketUnreachable: a repository whose own
// fugaro.yaml needs no project layer at all (it names its own workflow)
// still gets doctor's project-layer line, saying plainly that whether one
// applies is unknown (the bucket could not be read and nothing is cached)
// — never failing doctor over it (severity stays "info"): offline-safe
// degrade, not a refusal, for a command the design says must never fail for
// lack of network.
func TestDoctorLayerUnknownWhenBucketUnreachable(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	selfSufficient := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	dir := layerCheckout(t, f, selfSufficient)
	lc := fileEnv(t, f).lc
	ctx := context.Background()

	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, errors.New("boom: no cache exists yet, so this is not isUnreachable's cache-fallback case")
	}

	rf := resolvedLayerFixture(t, ctx, lc, dir)
	if rf.Cfg == nil {
		t.Fatalf("a self-sufficient fugaro.yaml must still resolve: %+v", rf.Problems)
	}
	cs := doctorLayerChecks(ctx, lc, dir, rf)
	c := layerCheck(cs, "project-layer")
	if c == nil || c.OK || c.Severity != "info" {
		t.Fatalf("an unknown layer must be informational, never a failure: %+v", c)
	}
	if !strings.Contains(c.Problem, "whether a project layer applies is unknown") || !strings.Contains(c.Problem, "could not be read") {
		t.Fatalf("check %+v", c)
	}
	if checksFail(cs, true) {
		t.Fatalf("an info check must never fail doctor, strict or not: %+v", cs)
	}
	// Whether a layer applies is unknown, not measured: it is a counted
	// finding, and the pointer line is where its reader hears of it.
	if got := doctorClassOf(*c); got != doctorClassAudit {
		t.Errorf("an unknown project layer is %q, want %q", got, doctorClassAudit)
	}
}

// TestImageConfigReasonNamesTheBasesSource: imageConfigReason names where a
// workflow's base comes from, so a reader of the report knows which layer to
// look in.
//
// It named every distinct source among imageLeafKeys — base, image.apt,
// image.setup and image.skip_build_scripts — before the repository-image
// removal refused the last three by name (D1195) and left the base's source
// the whole answer. This is the sentence that is left, and the one
// TestDoctorFlagsAStaleImage reads in the report's own text.
func TestImageConfigReasonNamesTheBasesSource(t *testing.T) {
	res := &config.Resolution{Sources: map[string]string{
		"workflows.default.base": config.SourceProfile("svc"),
	}}
	got := imageConfigReason(res, "default")
	want := "its base comes from profile svc"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestDoctorLayerChecksFailOnlyUnderStrict: a project-layer-copy warning
// (the repository has no copy of the published layer) must not fail
// ordinary `fugaro doctor`, only `--strict` (doctor's own documented
// contract: a warning fails only under --strict, an info line never does).
//
// Mutation (run, restore): change layerCopyCheck's missing-copy check from
// Severity: "warning" to Severity: "" (an ordinary check), and this test
// fails: doctor --json (no --strict) comes back with OK: false.
func TestDoctorLayerChecksFailOnlyUnderStrict(t *testing.T) {
	r := newDoctorRig(t)
	isolateCache(t)
	// doctorLayerChecks' own bucket reads (layerDrift, layerCopyCheck,
	// imageConfigChecks) open lc.BucketURL() directly, not through
	// layerBucketOpener (that seam only covers findLayer's read of the
	// canonical layer object): bucket_url: must itself be a file:// bucket,
	// the same one newCloudFixture sets up, for every read here to stay
	// offline and hermetic.
	runs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(runs, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "fugaro", "project-layer.yaml"), []byte(testProjectLayer), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgData, err := os.ReadFile(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfgPath, append(cfgData, []byte("bucket_url: file://"+runs+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := gitCheckout(t, filepath.Join(r.dir, "app"), minimalAnchored)
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	t.Chdir(dir)

	o := doctorJSON(t)
	if c, ok := doctorCheckByID(o.Checks, "project-layer-copy"); !ok || c.Severity != "warning" {
		t.Fatalf("precondition: expected an unflagged project-layer-copy warning, got %+v (checks %+v)", c, o.Checks)
	}
	if !o.OK {
		t.Fatalf("a warning must not fail doctor without --strict: %+v", o)
	}
	if o := doctorJSON(t, "--strict"); o.OK {
		t.Fatalf("the same warning must fail doctor --strict: %+v", o)
	}
}

// TestDoctorLayerCopyCheckSilentOnOtherReadErrors: layerCopyCheck must say
// nothing for a read failure that isn't "no such object" — only a genuinely
// missing copy (blobx.ErrNotExist) is reported; this rig produces a copy
// object over config.LayerMaxBytes, so ReadMax's own error is
// blobx.ErrTooLarge, not ErrNotExist.
//
// Mutation (run, restore): delete the `case err != nil: return nil` line,
// and this test fails: the fallthrough to `config.LayerSum(data) !=
// l.SHA256` (data is nil for an oversized read) reports a spurious
// project-layer-copy warning instead of staying silent.
func TestDoctorLayerCopyCheckSilentOnOtherReadErrors(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	slug := mustSlug("github", "acme/other")
	writeBucketFile(t, f, config.LayerCopyKey(slug), strings.Repeat("a", config.LayerMaxBytes+1))
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)

	cs := doctorLayerChecks(ctx, lc, dir, rf)
	if c := layerCheck(cs, "project-layer-copy"); c != nil {
		t.Fatalf("an oversized copy (not a missing one) must say nothing: %+v", c)
	}
}

// TestDoctorHelpMentionsTheLayerChecks: doctor's own --help text names the
// project-layer section this task adds (docs must match behaviour: every
// documented command and flag must exist, and conversely a real, documented
// behaviour should be named in the command's own help).
func TestDoctorHelpMentionsTheLayerChecks(t *testing.T) {
	cmd := newDoctorCmd()
	for _, want := range []string{"project layer", "stale"} {
		if !strings.Contains(cmd.Long, want) {
			t.Fatalf("doctor --help does not mention %q:\n%s", want, cmd.Long)
		}
	}
}

// TestDoctorReportsALocalLayerFileThatIsUnpublished: doctor's own door on the
// one local layer file (D830) — §1's path, the shared word `unpublished` and
// the cause the record tells apart (D854) — and the checks that were already
// there are asserted in the same test, so the addition is shown not to have
// moved them.
func TestDoctorReportsALocalLayerFileThatIsUnpublished(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	noAgentSession(t)
	ctx := context.Background()
	slug := mustSlug("github", "acme/other")

	path := localLayerPath(t)
	if err := os.WriteFile(path, []byte(testProjectLayer), 0o600); err != nil {
		t.Fatal(err)
	}
	// The explicit door records the generation it published at, so the edit
	// below is this machine's own and not an older generation's copy.
	if _, _, err := execute(t, "config", "publish", "--executable-changes"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(testProjectLayer, "sh test.sh", "sh test-all.sh", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRunRecord(t, f, slug, "20261008-090000-aaaa", nil)

	lc := fileEnv(t, f).lc
	rf := resolvedLayerFixture(t, ctx, lc, dir)
	cs := doctorLayerChecks(ctx, lc, dir, rf)
	if c := layerCheck(cs, "local-layer-file"); c == nil || c.Severity != "warning" ||
		!strings.Contains(c.Problem, path) || !strings.Contains(c.Problem, "unpublished") ||
		!strings.Contains(c.Problem, layerLocalEdit) || !strings.Contains(c.Problem, "fugaro update") {
		t.Fatalf("local-layer-file: %+v", cs)
	}
	if c := layerCheck(cs, "project-layer"); c == nil || c.Severity != "info" || !strings.Contains(c.Problem, "project layer aurora generation") {
		t.Fatalf("project-layer moved: %+v", cs)
	}
	if c := layerCheck(cs, "project-layer-drift"); c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "ran without the project layer") {
		t.Fatalf("project-layer-drift moved: %+v", cs)
	}
	if c := layerCheck(cs, "project-layer-copy"); c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "has no copy") {
		t.Fatalf("project-layer-copy moved: %+v", cs)
	}

	// It reads no bucket, so it is reported on the paths that stop before the
	// bucket read too: the one line the owner needs in the state that breaks
	// `fugaro rigor set` must not vanish with the network.
	none := rf
	none.Layer = foundLayer{}
	if c := layerCheck(doctorLayerChecks(ctx, lc, dir, none), "local-layer-file"); c == nil || !strings.Contains(c.Problem, path) {
		t.Fatalf("the check vanished with the published layer: %+v", c)
	}

	// A file at the path that this CLI does not read — a symlink, an oversized
	// one — is a state of its own, and the check reports the reason instead of
	// "does not exist": the remedy that sentence carries (`fugaro update` seeds
	// the file) is one update refuses for exactly this file.
	for _, kind := range unreadableLayerKinds {
		t.Run(kind, func(t *testing.T) {
			path := localLayerPath(t)
			reason := unreadableLayerFile(t, path, kind)
			c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "local-layer-file")
			if c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, path) ||
				!strings.Contains(c.Problem, "not read") || !strings.Contains(c.Problem, reason) {
				t.Fatalf("a local layer file this CLI does not read: %+v", c)
			}
			if strings.Contains(c.Problem, "does not exist") {
				t.Errorf("a file this CLI does not read is reported as absent: %q", c.Problem)
			}
			if c.Fix != "" {
				t.Errorf("the check sends the owner to a command that refuses this file: %q", c.Fix)
			}
		})
	}
}

// doctorCatalogFixture is the project image catalog the doctor image checks
// are judged against: one entry a Fugaro build writes an image for (inline,
// so nothing here needs another repository's checkout), tagged node, and no
// default — a workflow the catalog speaks for is one that pins the entry or
// needs a tag it covers.
const doctorCatalogFixture = `image_catalog:
  entries:
    - name: go
      tags: [node]
      source: { kind: inline, inline: "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\n" }
`

// doctorCatalogCheckout is layerCheckout with the fixture's catalog in the local
// config and repoYAML as the checkout's fugaro.yaml.
func doctorCatalogCheckout(t *testing.T, f *cloudFixture, repoYAML string) string {
	t.Helper()
	f.appendConfig(t, doctorCatalogFixture)
	return layerCheckout(t, f, repoYAML)
}

// writeCatalogRecord writes entry's build record under slug, built from the
// definition def, and returns the sha256 the record carries.
func writeCatalogRecordFrom(t *testing.T, f *cloudFixture, slug, entry, def string) {
	t.Helper()
	sum := sha256.Sum256([]byte(def))
	data, err := json.Marshal(imagecheck.CatalogRecordState{
		Version: imagecheck.CatalogRecordVersion, Entry: entry,
		DefinitionSHA256: hex.EncodeToString(sum[:]), ImageDigest: "sha256:" + strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	key, err := gcp.CatalogRecordKey(slug, entry)
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, key, string(data))
}

// TestDoctorImageConfigSkipsCatalogWorkflows: a workflow whose image the
// project's image catalog decides — one that pins an entry with image_name:,
// or one whose needs: an entry covers — never gets an image-config- check.
// Its job image is the catalog entry's, built by the catalog build and
// recorded per entry (imagecheck.CatalogRecordState); there is no per
// workflow build record of the settings it resolves to, so the comparison
// that check makes is against a build that is not the image its jobs run.
// The owner's rule is what this is: doctor is clean after `fugaro update`,
// and "run fugaro update" for an image the update does not build is a loop.
//
// Mutation (run, restore): drop the catalog guard from imageConfigChecks
// (always compare), and this test fails: image-config-app fires for both
// workflows, each with a stale per-workflow record whose hash no longer
// matches what the workflow resolves to.
func TestDoctorImageConfigSkipsCatalogWorkflows(t *testing.T) {
	for _, tc := range []struct{ name, workflow string }{
		{"a pinned entry", "app: { image_name: go, commands: { build: sh build.sh, test: sh test.sh } }"},
		{"a needs: the catalog covers", "app: { needs: [node], commands: { build: sh build.sh, test: sh test.sh } }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			isolateCache(t)
			publishedLayer(t, f, testProjectLayer)
			dir := doctorCatalogCheckout(t, f, minimalAnchored+"git: { provider: github }\nworkflows:\n  "+tc.workflow+"\n")
			slug := mustSlug("github", "acme/other")
			// A per-workflow record whose hash is not what app resolves to
			// now: were the check still comparing, it would fire.
			data, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/other", Workflow: "app", ImageConfigHash: strings.Repeat("0", 64)})
			writeBucketFile(t, f, imagecheck.RecordKey(slug, "app"), string(data))
			lc := fileEnv(t, f).lc
			ctx := context.Background()
			rf := resolvedLayerFixture(t, ctx, lc, dir)

			cs := doctorLayerChecks(ctx, lc, dir, rf)
			if c := layerCheck(cs, "image-config-app"); c != nil {
				t.Fatalf("a catalog workflow must never get an image-config check: %+v", c)
			}
		})
	}
}

// TestDoctorImageConfigComparesWorkflowsTheCatalogDoesNotCover: the guard is
// the catalog's own predicate (catalog.Applies), not a blank cheque. A
// workflow that names nothing and needs nothing, in a project whose catalog
// marks no entry default, is one the catalog does not speak for: its image
// is a derived build, and its stale record is still the warning it was.
//
// Mutation (run, restore): widen the guard to `true` (skip every workflow),
// and this test fails: no image-config-app check is emitted at all.
func TestDoctorImageConfigComparesWorkflowsTheCatalogDoesNotCover(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := doctorCatalogCheckout(t, f, minimalAnchored+"git: { provider: github }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n")
	slug := mustSlug("github", "acme/other")
	data, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/other", Workflow: "app", ImageConfigHash: strings.Repeat("0", 64)})
	writeBucketFile(t, f, imagecheck.RecordKey(slug, "app"), string(data))
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)

	c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "image-config-app")
	if c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "other image settings") {
		t.Fatalf("a workflow the catalog does not cover is still compared: %+v", c)
	}
}

// TestDoctorImageConfigComparesAPinWithNoCatalog: the two reasons a catalog is
// nil are not one state. A project whose catalog is known to be empty is one
// where every workflow runs the derived image — internal/infra's
// catalogJobImage and run.go's catalogDockerAsk both stop at cat == nil — so
// the per-workflow record is the image its jobs start on and is compared like
// any other workflow's. catalog.Applies answers true for a pin however empty
// the catalog is (a pin is the repository's own claim), so the catalog guard
// alone took such a workflow for the catalog's and no line judged its image at
// all. The other nil is a catalog nobody read: there the node that did not
// answer may hold the entry a pin names, one whose tags cover a workflow's
// needs:, or the default a workflow that names nothing runs, so no workflow is
// judged and imageCatalogUnreadCheck is the line that says so. A catalog
// written empty in this machine's own config is the
// first state and not the second, however the node answered: the file's
// catalog wins (localcfg's keep rule, D1140), and ImageCatalogEffective
// answers nil for it on purpose.
//
// Mutations (run, restore): `case cat == nil:` in workflowCatalogEntry, and
// the first and last subtests fail (no image-config-app); `return
// config.Entry{}, false` for the needs: path with catUnknown, and the third
// fails (the unread node's catalog may cover the tag); dropping the
// `catUnknown && catalog.MayApply(w)` arm, and the fourth fails (the unread
// node may mark the default a workflow that names nothing runs);
// `catUnknown := cat == nil && catErr != nil`, and the last fails (a
// written-empty local catalog is read as an unknown).
func TestDoctorImageConfigComparesAPinWithNoCatalog(t *testing.T) {
	const pinned = minimalAnchored + "git: { provider: github }\nworkflows:\n  app: { image_name: go, commands: { build: sh build.sh, test: sh test.sh } }\n"
	const tagged = minimalAnchored + "git: { provider: github }\nworkflows:\n  app: { needs: [node], commands: { build: sh build.sh, test: sh test.sh } }\n"
	const namesNothing = minimalAnchored + "git: { provider: github }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"

	// rig is a checkout of acme/other with a per-workflow record whose hash is
	// not what app resolves to now: compared, it fires. brokenNode is whether
	// the published policy node is one no reader gets past (a marker that is
	// not a generation, projectpolicy.Read's MarkerError).
	rig := func(t *testing.T, f *cloudFixture, repoYAML string, brokenNode bool) (string, context.Context, *localcfg.Config, resolvedFile) {
		t.Helper()
		isolateCache(t)
		publishedLayer(t, f, testProjectLayer)
		var db *gcpfake.RTDB
		if brokenNode {
			db = doctorPublishedCatalog(t, f)
		}
		dir := layerCheckout(t, f, repoYAML)
		slug := mustSlug("github", "acme/other")
		data, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/other", Workflow: "app", ImageConfigHash: strings.Repeat("0", 64)})
		writeBucketFile(t, f, imagecheck.RecordKey(slug, "app"), string(data))
		if db != nil {
			db.Set(projectpolicy.PathRulesMarker(), "not-a-generation")
		}
		lc := fileEnv(t, f).lc
		ctx := context.Background()
		return dir, ctx, lc, resolvedLayerFixture(t, ctx, lc, dir)
	}

	t.Run("the project has no catalog", func(t *testing.T) {
		f := newCloudFixture(t)
		dir, ctx, lc, rf := rig(t, f, pinned, false)
		cs := doctorLayerChecks(ctx, lc, dir, rf)
		if c := layerCheck(cs, "image-catalog"); c != nil {
			t.Fatalf("the node answered and states no catalog: %+v", c)
		}
		if c := layerCheck(cs, "image-catalog-app"); c != nil {
			t.Fatalf("a pin no catalog holds is not an entry to judge: %+v", c)
		}
		c := layerCheck(cs, "image-config-app")
		if c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "other image settings") {
			t.Fatalf("a pinned workflow whose jobs run the derived image is still compared: %+v", c)
		}
	})

	t.Run("the catalog could not be read", func(t *testing.T) {
		f := newCloudFixture(t)
		dir, ctx, lc, rf := rig(t, f, pinned, true)
		cs := doctorLayerChecks(ctx, lc, dir, rf)
		if c := layerCheck(cs, "image-catalog"); c == nil || !c.unread {
			t.Fatalf("an unread node must be doctor's unread state: %+v", c)
		}
		if c := layerCheck(cs, "image-config-app"); c != nil {
			t.Fatalf("the unread node may hold the pinned entry, so the pin is left to the catalog: %+v", c)
		}
		if c := layerCheck(cs, "image-catalog-app"); c != nil {
			t.Fatalf("the unread node's catalog may hold the entry, so no entry is judged: %+v", c)
		}
	})

	t.Run("a needs: the unread node's catalog may cover", func(t *testing.T) {
		f := newCloudFixture(t)
		dir, ctx, lc, rf := rig(t, f, tagged, true)
		cs := doctorLayerChecks(ctx, lc, dir, rf)
		if c := layerCheck(cs, "image-catalog"); c == nil || !c.unread {
			t.Fatalf("an unread node must be doctor's unread state: %+v", c)
		}
		if c := layerCheck(cs, "image-config-app"); c != nil {
			t.Fatalf("the unread node's catalog may cover the tag, so the workflow is left to it: %+v", c)
		}
	})

	// A workflow that names nothing runs the project's default, and whether the
	// node nobody read marks one is the single clause of catalog.Applies that
	// reads the catalog — so the unread state asks MayApply instead, which is
	// that predicate without the clause, and the workflow is left to the
	// catalog. Judged here it would be compared against the derived image,
	// which its jobs run only if the node marks no default at all.
	// Mutation (run, restore): drop the `catUnknown && catalog.MayApply(w)`
	// arm, and this subtest fails with image-config-app.
	t.Run("a workflow that names nothing, and an unread node", func(t *testing.T) {
		f := newCloudFixture(t)
		dir, ctx, lc, rf := rig(t, f, namesNothing, true)
		cs := doctorLayerChecks(ctx, lc, dir, rf)
		if c := layerCheck(cs, "image-catalog"); c == nil || !c.unread {
			t.Fatalf("an unread node must be doctor's unread state: %+v", c)
		}
		if c := layerCheck(cs, "image-config-app"); c != nil {
			t.Fatalf("the unread node may mark the default this workflow runs, so it is left to the catalog: %+v", c)
		}
	})

	t.Run("a catalog written empty in this file is an absence", func(t *testing.T) {
		f := newCloudFixture(t)
		// This machine's own file says the catalog is empty, which is how a
		// published one is taken away on purpose (localcfg's keep rule).
		f.appendConfig(t, "image_catalog: {}\n")
		dir, ctx, lc, rf := rig(t, f, pinned, true)
		cs := doctorLayerChecks(ctx, lc, dir, rf)
		if c := layerCheck(cs, "image-catalog"); c != nil {
			t.Fatalf("a config that states a catalog of its own is not the unread state: %+v", c)
		}
		c := layerCheck(cs, "image-config-app")
		if c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "other image settings") {
			t.Fatalf("the file's empty catalog is what the job spec resolves, so the derived image is compared: %+v", c)
		}
	})
}

// TestDoctorCatalogImageNamesTheBuildCommand: a workflow that runs a catalog
// entry is judged on the entry instead, and the one command that fixes either
// state is the entry's own build — never `fugaro update`, which builds no
// catalog image and is the loop the owner already ran. The built and current
// entry says nothing.
func TestDoctorCatalogImageNamesTheBuildCommand(t *testing.T) {
	const def = "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\n"
	const yaml = minimalAnchored + "git: { provider: github }\nworkflows:\n  app: { image_name: go, commands: { build: sh build.sh, test: sh test.sh } }\n"

	build := func(t *testing.T) (*cloudFixture, string, string, context.Context, *localcfg.Config, resolvedFile) {
		t.Helper()
		f := newCloudFixture(t)
		isolateCache(t)
		publishedLayer(t, f, testProjectLayer)
		dir := doctorCatalogCheckout(t, f, yaml)
		lc := fileEnv(t, f).lc
		ctx := context.Background()
		return f, dir, mustSlug("github", "acme/other"), ctx, lc, resolvedLayerFixture(t, ctx, lc, dir)
	}

	t.Run("nothing built the entry", func(t *testing.T) {
		_, dir, _, ctx, lc, rf := build(t)
		c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "image-catalog-app")
		if c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "go") || !strings.Contains(c.Problem, "no build has built") {
			t.Fatalf("check %+v", c)
		}
		// The daily image check cannot submit the first build of an entry:
		// a rebuild is pinned to the commit the last build recorded, and an
		// entry no build has built has none (image_check_catalog.go says so
		// in the entry's own decision, NeedsBuild). The fix is therefore the
		// check's own sentence for that state — a person's build from the
		// checkout that build has to be submitted from — and naming the daily
		// check here was a run that does nothing. This fixture's entry is
		// inline, so no repository owns it and the checkout is this one (the
		// dockerfile case, which names the entry's own repository, is
		// TestDoctorImageCatalogFromThePublishedNode).
		if c.Fix != "fugaro image build go from this checkout builds it once, and the daily image check rebuilds it from then on" {
			t.Errorf("fix = %q, want the entry's own build", c.Fix)
		}
	})

	t.Run("the entry's definition moved", func(t *testing.T) {
		f, dir, slug, ctx, lc, rf := build(t)
		key, err := config.CatalogDockerfileKey("go")
		if err != nil {
			t.Fatal(err)
		}
		writeBucketFile(t, f, key, def)
		writeCatalogRecordFrom(t, f, slug, "go", "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\nRUN echo other\n")
		c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "image-catalog-app")
		if c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "definition") {
			t.Fatalf("check %+v", c)
		}
		if c.Fix != "the daily image check rebuilds it; to do it now: fugaro image build go" {
			t.Errorf("fix = %q, want the entry's own build", c.Fix)
		}
	})

	t.Run("built from the definition in the bucket now", func(t *testing.T) {
		f, dir, slug, ctx, lc, rf := build(t)
		key, err := config.CatalogDockerfileKey("go")
		if err != nil {
			t.Fatal(err)
		}
		writeBucketFile(t, f, key, def)
		writeCatalogRecordFrom(t, f, slug, "go", def)
		if c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "image-catalog-app"); c != nil {
			t.Fatalf("a built, current entry must say nothing: %+v", c)
		}
	})

	// A record that is there and cannot be read is not an entry no build has
	// built: doctor's best effort says nothing about what it could not read,
	// and a record in that state is the daily check's own to fix or delete —
	// told to rebuild, the owner would build an entry that already has one.
	// Mutation (run, restore): treat every read error as an absent record, and
	// both subtests fail with the "no build has built" warning.
	t.Run("a record that does not parse says nothing", func(t *testing.T) {
		f, dir, slug, ctx, lc, rf := build(t)
		key, err := gcp.CatalogRecordKey(slug, "go")
		if err != nil {
			t.Fatal(err)
		}
		writeBucketFile(t, f, key, "{ this is not the record's shape")
		if c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "image-catalog-app"); c != nil {
			t.Fatalf("a corrupt record must not be read as no record: %+v", c)
		}
	})

	t.Run("a record that cannot be read says nothing", func(t *testing.T) {
		f, dir, slug, ctx, lc, rf := build(t)
		key, err := gcp.CatalogRecordKey(slug, "go")
		if err != nil {
			t.Fatal(err)
		}
		// Over the read cap, so the read fails with something that is not
		// ErrNotExist (a mode 000 file would depend on the uid the test runs
		// as: root reads it anyway).
		writeBucketFile(t, f, key, strings.Repeat("x", blobx.MaxReadBytes+1))
		if c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "image-catalog-app"); c != nil {
			t.Fatalf("a record that could not be read must not be read as no record: %+v", c)
		}
	})
}

// doctorPublishedCatalog puts the project's image catalog in the published
// policy node and leaves the checkout's own config silent about it: the shape
// a managed project has, where the catalog is the owner's and a repository's
// file states none. The entry is a dockerfile one naming acme/app, so the
// checkout its build has to be submitted from has a name of its own. It
// returns the database, so a test can break the node the catalog is read from.
func doctorPublishedCatalog(t *testing.T, f *cloudFixture) *gcpfake.RTDB {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	db.Set(projectpolicy.PathShared(), &projectpolicy.Policy{
		Version:    projectpolicy.Version,
		Budget:     projectpolicy.Budget{Mode: "off"},
		AllowRepos: []string{"github/acme/other"},
		ImageCatalog: &config.Catalog{Entries: []config.Entry{{
			Name: "go", Tags: []string{"node"},
			Source: config.Source{Kind: config.SourceDockerfile, Repo: "acme/app", Path: "docker/agent.Dockerfile"},
		}}},
	})
	f.appendConfig(t, "budget: { mode: off, rtdb_url: "+db.URL+" }\n")
	return db
}

// TestDoctorImageCatalogFromThePublishedNode: the catalog a workflow's job
// image comes from is the one the job spec resolves — this checkout's config
// when it states one, the published node's otherwise (effectiveCatalog, the
// resolution internal/infra's spec, run.go's launcher and `fugaro image
// catalog` all make). Doctor read the local config alone, so on a machine
// whose file states no catalog (the normal case: the catalog is the owner's)
// it read "no catalog" for a project that has one, took the pinned workflow
// for a bare pin, skipped the per-workflow comparison and judged no entry
// either: a workflow whose job runs a stale catalog image was reported by no
// line at all.
//
// Mutation (run, restore): read the local config again in doctorLayerChecks
// (env.lc.ImageCatalogEffective(nil) for effectiveCatalog) and the first
// subtest fails, finding neither image-catalog-app nor image-config-app for a
// workflow whose job is deployed from the node's entry.
func TestDoctorImageCatalogFromThePublishedNode(t *testing.T) {
	const yaml = minimalAnchored + "git: { provider: github }\nworkflows:\n  app: { image_name: go, commands: { build: sh build.sh, test: sh test.sh } }\n"

	t.Run("the node's entry is judged, and its build names its checkout", func(t *testing.T) {
		f := newCloudFixture(t)
		isolateCache(t)
		publishedLayer(t, f, testProjectLayer)
		doctorPublishedCatalog(t, f)
		dir := layerCheckout(t, f, yaml)
		slug := mustSlug("github", "acme/other")
		// A stale per-workflow record, the false positive the catalog guard
		// exists for: compared, it would fire.
		data, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/other", Workflow: "app", ImageConfigHash: strings.Repeat("0", 64)})
		writeBucketFile(t, f, imagecheck.RecordKey(slug, "app"), string(data))
		lc := fileEnv(t, f).lc
		ctx := context.Background()
		rf := resolvedLayerFixture(t, ctx, lc, dir)

		cs := doctorLayerChecks(ctx, lc, dir, rf)
		if c := layerCheck(cs, "image-config-app"); c != nil {
			t.Fatalf("a workflow the published catalog speaks for must not get an image-config check: %+v", c)
		}
		if c := layerCheck(cs, "image-catalog"); c != nil {
			t.Fatalf("the catalog was read: %+v", c)
		}
		c := layerCheck(cs, "image-catalog-app")
		if c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "no build has built") {
			t.Fatalf("check %+v", c)
		}
		// The daily image check cannot submit this entry's first build (its
		// own line says so: a rebuild is pinned to the commit the last build
		// recorded, and there is none), so the fix is the build the entry's
		// own repository's checkout submits, and it is named.
		if c.Fix != "fugaro image build go from acme/app's checkout builds it once, and the daily image check rebuilds it from then on" {
			t.Errorf("fix = %q, want the entry's own build named with its checkout", c.Fix)
		}
	})

	t.Run("a node that could not be read is an unread line", func(t *testing.T) {
		f := newCloudFixture(t)
		isolateCache(t)
		publishedLayer(t, f, testProjectLayer)
		db := doctorPublishedCatalog(t, f)
		// The node no reader gets past: a marker that is not a generation
		// (projectpolicy.Read's MarkerError), which is the state a node
		// doctor cannot read the catalog out of.
		db.Set(projectpolicy.PathRulesMarker(), "not-a-generation")
		dir := layerCheckout(t, f, yaml)
		lc := fileEnv(t, f).lc
		ctx := context.Background()
		rf := resolvedLayerFixture(t, ctx, lc, dir)

		cs := classifyDoctorChecks(doctorLayerChecks(ctx, lc, dir, rf))
		c := layerCheck(cs, "image-catalog")
		if c == nil || c.Severity != "info" || !c.unread || c.Fix == "" {
			t.Fatalf("an unread node must be doctor's unread state, a line of its own: %+v", c)
		}
		if !strings.Contains(c.Problem, "could not be read") || !strings.Contains(c.Problem, "states no catalog of its own") {
			t.Errorf("the unread line does not say what was not read: %q", c.Problem)
		}
		if c.Class != doctorClassAudit {
			t.Errorf("an unread catalog is classed %q, want %q: an unknown is a counted finding, never a measurement", c.Class, doctorClassAudit)
		}
	})

	t.Run("a config stating its own catalog is not that state", func(t *testing.T) {
		f := newCloudFixture(t)
		isolateCache(t)
		publishedLayer(t, f, testProjectLayer)
		db := doctorPublishedCatalog(t, f)
		db.Set(projectpolicy.PathRulesMarker(), "not-a-generation")
		dir := doctorCatalogCheckout(t, f, yaml)
		lc := fileEnv(t, f).lc
		ctx := context.Background()
		rf := resolvedLayerFixture(t, ctx, lc, dir)

		// The local catalog wins (localcfg's keep rule, D1140): the node's
		// copy is not what the job spec resolves, so its unread state cannot
		// change a line here.
		if c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "image-catalog"); c != nil {
			t.Fatalf("a config that states its own catalog judges against it: %+v", c)
		}
	})
}
