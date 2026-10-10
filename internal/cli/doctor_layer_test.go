package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
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

// TestDoctorImageConfigNamesEachDifferingSource: when a workflow's base
// comes from its profile but its image settings are overridden directly in
// the repository (or vice versa), imageConfigChecks' message must name both
// sources, never silently attribute the whole thing to just one of them.
//
// Mutation (run, restore): revert imageConfigReason to the single `from :=
// res.SourceOf(...base); if s := res.SourceOf(...image); s !=
// SourceDefault { from = s }` form, and this test fails: the message says
// only "its base comes from repo" (image's source silently overwrote
// base's), never naming profile svc at all.
func TestDoctorImageConfigNamesEachDifferingSource(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored+"git: { provider: github }\nworkflows:\n  default: { profile: svc, image: { node: \"22\" } }\n")
	slug := mustSlug("github", "acme/other")
	data, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/other", Workflow: config.ImplicitWorkflow, ImageConfigHash: strings.Repeat("0", 64)})
	writeBucketFile(t, f, imagecheck.RecordKey(slug, config.ImplicitWorkflow), string(data))
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)

	c := layerCheck(doctorLayerChecks(ctx, lc, dir, rf), "image-config-"+config.ImplicitWorkflow)
	if c == nil || !strings.Contains(c.Problem, "its base comes from profile svc") || !strings.Contains(c.Problem, "its image settings from repo") {
		t.Fatalf("check %+v", c)
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

// TestDoctorImageConfigSkipsDockerfileWorkflows: a workflow with its own
// Dockerfile is never named by imageConfigChecks: its hash covers the
// Dockerfile blob, which needs a real git tree imageConfigChecks doesn't
// have (it always calls imagecheck.ImageConfigHash with a nil Tree).
//
// Mutation (run, restore): change the guard from `if
// cfg.Workflows[name].Dockerfile != "" { continue }` to `if false {
// continue }`, and this test fails — not with a wrong check, but a panic:
// ImageConfigHash calls tree.BlobID(w.Dockerfile) on the nil Tree, which is
// exactly why the guard exists. A panic inside the test function is a
// failure, so the mutation is still caught.
func TestDoctorImageConfigSkipsDockerfileWorkflows(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored+"git: { provider: github }\nworkflows:\n  app: { base: web-node, dockerfile: .fugaro/app.Dockerfile, commands: { build: sh build.sh, test: sh test.sh } }\n")
	testutil.WriteFiles(t, dir, map[string]string{".fugaro/app.Dockerfile": "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\nRUN git clone \"$REPO_URL\" /work/repo\n"})
	slug := mustSlug("github", "acme/other")
	data, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/other", Workflow: "app", ImageConfigHash: strings.Repeat("0", 64)})
	writeBucketFile(t, f, imagecheck.RecordKey(slug, "app"), string(data))
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	rf := resolvedLayerFixture(t, ctx, lc, dir)

	cs := doctorLayerChecks(ctx, lc, dir, rf)
	if c := layerCheck(cs, "image-config-app"); c != nil {
		t.Fatalf("a Dockerfile workflow must never get an image-config check: %+v", c)
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
	selfSufficient := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: github }\nworkflows:\n  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"
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
}

// TestImageConfigReasonSortsMultipleSources: imageConfigReason's "other
// sources" list is sorted, so the message is deterministic even when more
// than one other source applies.
//
// Mutation (run, restore): reverse the sorted slice before joining it
// (`s := slices.Sorted(...); slices.Reverse(s)`), and this test fails: the
// message lists "repo, profile zzz" instead of "profile zzz, repo".
func TestImageConfigReasonSortsMultipleSources(t *testing.T) {
	res := &config.Resolution{Sources: map[string]string{
		"workflows.default.base":       config.SourceDefault,
		"workflows.default.image.node": "repo",
		"workflows.default.image.jdk":  config.SourceProfile("zzz"),
	}}
	got := imageConfigReason(res, "default")
	want := "its base comes from default and its image settings from profile zzz, repo"
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
