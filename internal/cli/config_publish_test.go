package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

func layerFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "project-layer.yaml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func bucketText(t *testing.T, f *cloudFixture, key string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "runs", filepath.FromSlash(key)))
	if err != nil {
		return ""
	}
	return string(data)
}

// Review Focus 4.
func TestPublishRefusedInAgentSession(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	t.Setenv("CLAUDECODE", "1")
	_, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "your own terminal") {
		t.Fatalf("err = %v", err)
	}
	if bucketText(t, f, config.LayerKey) != "" {
		t.Fatal("published from an agent session")
	}
}

func TestPublishWritesAndCopies(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	out, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer))
	if err != nil || !strings.Contains(out, "published the project layer of aurora to gs://fugaro-runs-proj-1234/fugaro/project-layer.yaml") ||
		!strings.Contains(out, "acme/app: copied to "+config.LayerCopyKey(appSlug)) {
		t.Fatalf("out %q, err %v", out, err)
	}
	if bucketText(t, f, config.LayerKey) != testProjectLayer || bucketText(t, f, config.LayerCopyKey(appSlug)) != testProjectLayer {
		t.Fatal("the object or its copy is not the published text")
	}
}

// Review Focus 4, decision L7.
func TestPublishRefusesExecutableChangeWithoutFlag(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	_, errOut, err := execute(t, "config", "publish", layerFile(t, testProjectLayer))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--executable-changes") ||
		!strings.Contains(errOut, "EXECUTABLE CHANGES") || !strings.Contains(errOut, `profile svc: commands.test: "" -> "sh test.sh"`) {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
	if bucketText(t, f, config.LayerKey) != "" {
		t.Fatal("published without the flag")
	}
	if _, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer)); err != nil {
		t.Fatal(err)
	}
	// A change outside the executable keys needs no flag.
	v2 := strings.Replace(testProjectLayer, "auth: api-key", "auth: oauth", 1)
	if _, errOut, err := execute(t, "config", "publish", layerFile(t, v2)); err != nil || strings.Contains(errOut, "EXECUTABLE") {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
}

// Review Focus 4.
func TestPublishRefusesConcurrentPublisher(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	noAgentSession(t)
	race := layerPublishRace
	t.Cleanup(func() { layerPublishRace = race })
	other := strings.Replace(testProjectLayer, "auth: api-key", "auth: vertex", 1)
	layerPublishRace = func(context.Context, *blobx.Bucket) { writeBucketFile(t, f, config.LayerKey, other) }
	v2 := strings.Replace(testProjectLayer, "auth: api-key", "auth: oauth", 1)
	_, _, err := execute(t, "config", "publish", layerFile(t, v2))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "another publisher changed") {
		t.Fatalf("err = %v", err)
	}
	if bucketText(t, f, config.LayerKey) != other {
		t.Fatal("the concurrent publisher's object was overwritten")
	}
}

func TestPublishRefusesAnInvalidLayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	_, _, err := execute(t, "config", "publish", layerFile(t, layerWithDefaults("  budget: { per_run_usd: 1 }\n")))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") || !strings.Contains(err.Error(), "budget.per_run_usd may only be set in: repo") {
		t.Fatalf("err = %v", err)
	}
}

// A launcher's publish is refused with the operator-role text before any
// write (stand-in for bucket-iam plan Task 6's own test on operator_write.go,
// not yet merged; see operatorWriteErr in layer_copy.go).
func TestPublishLayerRefusedAsLauncher(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	fake.DenyWrites("fugaro-runs-proj-1234", "fugaro/")
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	l, ps := config.ParseProjectLayer([]byte(testProjectLayer), config.LayerAnchor{})
	if len(ps) > 0 {
		t.Fatalf("fixture layer is invalid: %v", ps)
	}
	var w bytes.Buffer
	_, err := publishLayer(ctx, &w, b, l, true)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "operator role") {
		t.Fatalf("err = %v", err)
	}
	if _, _, rerr := b.Read(ctx, config.LayerKey); !errors.Is(rerr, blobx.ErrNotExist) {
		t.Fatalf("something was written: %v", rerr)
	}
}

// A launcher's repository copy is refused the same way.
func TestWriteLayerCopyRefusedAsLauncher(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	fake.DenyWrites("fugaro-runs-proj-1234", "builds/")
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	err := writeLayerCopy(ctx, b, appSlug, []byte(testProjectLayer))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "operator role") {
		t.Fatalf("err = %v", err)
	}
}

// writeLayerCopy's nil-data path has no caller in this PR (fanOutLayer
// always passes l.Raw), but is part of its declared interface (a future
// retiring publish, and Task 14), so it is exercised directly here.
func TestWriteLayerCopyRemovesAnExistingCopy(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	key := config.LayerCopyKey(appSlug)
	fake.Put("fugaro-runs-proj-1234", key, []byte(testProjectLayer))
	if err := writeLayerCopy(ctx, b, appSlug, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Read(ctx, key); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("the copy was not removed: %v", err)
	}
}

// A launcher's removal of a copy is refused the same way, and nothing is
// deleted.
func TestWriteLayerCopyRemoveRefusedAsLauncher(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	key := config.LayerCopyKey(appSlug)
	fake.Put("fugaro-runs-proj-1234", key, []byte(testProjectLayer))
	fake.DenyWrites("fugaro-runs-proj-1234", "builds/")
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	err := writeLayerCopy(ctx, b, appSlug, nil)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "operator role") {
		t.Fatalf("err = %v", err)
	}
	if _, _, rerr := b.Read(ctx, key); rerr != nil {
		t.Fatalf("the copy was removed: %v", rerr)
	}
}

// Review Focus 4: the oversized-object branch of writeLayerCopy's removal
// (the generation is not known, so it deletes through the embedded gocloud
// Bucket, not a blobx writer) must still classify a 403 as needing the
// operator role, not a generic error.
func TestWriteLayerCopyRemoveOfOversizedIsRefusedAsLauncher(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	key := config.LayerCopyKey(appSlug)
	fake.Put("fugaro-runs-proj-1234", key, bytes.Repeat([]byte("x"), config.LayerMaxBytes+1))
	fake.DenyWrites("fugaro-runs-proj-1234", "builds/")
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	err := writeLayerCopy(ctx, b, appSlug, nil)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "operator role") {
		t.Fatalf("err = %v", err)
	}
	if data, _, rerr := b.Read(ctx, key); rerr != nil || len(data) != config.LayerMaxBytes+1 {
		t.Fatalf("the oversized copy was removed: data %d, err %v", len(data), rerr)
	}
}

// Review Focus 4, ruling: an existing oversized copy cannot be read to
// compare (ReadMax's old is nil for it), so writeLayerCopy's "writes only
// when it differs" doc must not claim equality skips the write here: it
// always overwrites.
func TestWriteLayerCopyAlwaysOverwritesAnOversizedExistingCopy(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	key := config.LayerCopyKey(appSlug)
	fake.Put("fugaro-runs-proj-1234", key, bytes.Repeat([]byte("x"), config.LayerMaxBytes+1))
	if err := writeLayerCopy(ctx, b, appSlug, []byte(testProjectLayer)); err != nil {
		t.Fatal(err)
	}
	if data, _, rerr := b.Read(ctx, key); rerr != nil || string(data) != testProjectLayer {
		t.Fatalf("the oversized copy was not overwritten: data %q, err %v", data, rerr)
	}
}

// Review Focus 4, ruling: the oversized-removal path used an unconditional
// Delete with no generation check, which could delete a valid object a
// concurrent writer just replaced it with. It must instead read the
// generation first and delete under that precondition, leaving (and
// reporting, not silently keeping) a concurrent replacement alone.
func TestWriteLayerCopyRemoveOfOversizedLeavesAConcurrentReplacementAlone(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	key := config.LayerCopyKey(appSlug)
	fake.Put("fugaro-runs-proj-1234", key, bytes.Repeat([]byte("x"), config.LayerMaxBytes+1))
	race := layerCopyRemovalRace
	t.Cleanup(func() { layerCopyRemovalRace = race })
	replacement := []byte(testProjectLayer)
	layerCopyRemovalRace = func(context.Context, *blobx.Bucket, string) {
		fake.Put("fugaro-runs-proj-1234", key, replacement)
	}
	err := writeLayerCopy(ctx, b, appSlug, nil)
	if !errors.Is(err, blobx.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if data, _, rerr := b.Read(ctx, key); rerr != nil || string(data) != string(replacement) {
		t.Fatalf("the concurrent replacement was deleted: data %q, err %v", data, rerr)
	}
}

// Review Focus 4: a transient failure to read the oversized object's
// generation (a network blip, a 5xx/429, a concurrent writer mid-replace)
// is not "this driver has no generation concept" (GCSName == ""): it must
// be reported, not papered over with an unconditional delete that would
// defeat the whole precondition this function exists to provide.
func TestWriteLayerCopyRemoveOfOversizedReportsATransientAttributesError(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	key := config.LayerCopyKey(appSlug)
	fake.Put("fugaro-runs-proj-1234", key, bytes.Repeat([]byte("x"), config.LayerMaxBytes+1))
	fake.FailObjectGets(1)
	err := writeLayerCopy(ctx, b, appSlug, nil)
	if err == nil {
		t.Fatal("a transient Attributes error was not reported")
	}
	if data, _, rerr := b.Read(ctx, key); rerr != nil || len(data) != config.LayerMaxBytes+1 {
		t.Fatalf("the oversized copy was deleted despite its unreadable generation: data %d, err %v", len(data), rerr)
	}
}

func TestPublishWarnsAboutOldImages(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	writeBuildRecord(t, f, appSlug, "web", release050)
	_, errOut, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer))
	if err != nil || !strings.Contains(errOut, "acme/app workflow web runs a job image built from") || !strings.Contains(errOut, "fugaro image refresh") {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
}

// Review Focus 4: a mutation that aliases prev to the new layer (instead
// of parsing the stored old object) would make executableChanges always
// compare a layer to itself, finding zero changes forever after the first
// publish, silently defeating the whole --executable-changes gate. The
// banner must also show both the old and the new value of the changed key.
func TestPublishSecondReplaceStillGatesAnExecutableChange(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	if _, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer)); err != nil {
		t.Fatal(err)
	}
	v2 := strings.Replace(testProjectLayer, "test: sh test.sh", "test: sh test2.sh", 1)
	if v2 == testProjectLayer {
		t.Fatal("the fixture did not change")
	}
	_, errOut, err := execute(t, "config", "publish", layerFile(t, v2))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--executable-changes") ||
		!strings.Contains(errOut, `commands.test: "sh test.sh" -> "sh test2.sh"`) {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
	if bucketText(t, f, config.LayerKey) != testProjectLayer {
		t.Fatal("a replace was published without the flag")
	}
}

// Review Focus 4: the object appears between publishLayer's read (which
// found nothing) and its write (Create, not Replace): the write must
// still be refused, not silently become a replace of someone else's
// first publish.
func TestPublishRefusesAFirstPublishRace(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	race := layerPublishRace
	t.Cleanup(func() { layerPublishRace = race })
	layerPublishRace = func(context.Context, *blobx.Bucket) { writeBucketFile(t, f, config.LayerKey, testProjectLayer) }
	_, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "another publisher changed") {
		t.Fatalf("err = %v", err)
	}
}

// Review Focus 4: the old object is untrusted and never validated before
// this publish runs; it must never be echoed (it may hold a credential a
// hand edit put there), and never diffed against.
func TestPublishWithAnInvalidOldObjectSkipsTheDiffAndStillGates(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	writeBucketFile(t, f, config.LayerKey, "version: 1\nproject: aurora\ngcp_project: proj-1234\nbogus: sk-ant-"+strings.Repeat("a", 20)+"\n")
	_, errOut, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer))
	if err != nil {
		t.Fatalf("err = %v, stderr %s", err, errOut)
	}
	if !strings.Contains(errOut, "the published layer is invalid") || !strings.Contains(errOut, "every executable key counts as changed") {
		t.Fatalf("stderr %q", errOut)
	}
	if strings.Contains(errOut, "sk-ant-") {
		t.Fatalf("a credential from the unparsed old object was echoed: %s", errOut)
	}
}

// Review Focus 4: lineDiff's longest-common-subsequence table is
// O(len(a's lines) * len(b's lines)); two large objects of short lines
// must not build it.
func TestPublishSkipsTheDiffWhenTooLargeToCompareSafely(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	old := strings.Repeat("# old padding\n", 1500) + testProjectLayer
	publishedLayer(t, f, old)
	noAgentSession(t)
	next := strings.Repeat("# new padding\n", 1500) + testProjectLayer
	_, errOut, err := execute(t, "config", "publish", layerFile(t, next))
	if err != nil {
		t.Fatalf("err = %v, stderr %s", err, errOut)
	}
	if !strings.Contains(errOut, "diff is skipped") {
		t.Fatalf("stderr %q", errOut)
	}
	if strings.Contains(errOut, "old padding") || strings.Contains(errOut, "new padding") {
		t.Fatalf("a full diff was built despite the size: %s", errOut)
	}
}

func TestLayerDiffTextIsNonEmptyWhenContentDiffers(t *testing.T) {
	got := layerDiffText("a: 1\n", "a: 2\n")
	if strings.TrimSpace(got) == "" {
		t.Fatal("empty diff for differing text")
	}
}

func TestLayerDiffTextSkipsWhenTooLarge(t *testing.T) {
	a := strings.Repeat("a\n", 1500)
	b := strings.Repeat("b\n", 1500)
	got := layerDiffText(a, b)
	if !strings.Contains(got, "diff is skipped") {
		t.Fatalf("got %q", got)
	}
}

// envOnRepos is a minimal cloudEnv over a hand-built local config with the
// given repositories, for fanOutLayer/warnOldImages tests that need more
// than the cloud fixture's single repository.
func envOnRepos(b *blobx.Bucket, repos map[string]localcfg.Repo) *cloudEnv {
	return &cloudEnv{lc: &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Repos: repos}, bucket: b}
}

// Review Focus 4: a repository copy failure (here: an invalid provider,
// so task.Slug itself refuses it) is counted, the other repository's
// copy still runs, and a later rerun (the provider fixed) is idempotent:
// it completes only the one that failed.
func TestFanOutCountsAFailedCopyAndRerunIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	repos := map[string]localcfg.Repo{
		"acme/app":   {Provider: "github", Workflows: []string{"web"}},
		"acme/other": {Provider: "GitHub", Workflows: []string{"svc"}}, // not a provider kind (task.Slug, inside env.repoSlug, is case-sensitive)
	}
	env := envOnRepos(b, repos)
	l := parseLayerOrFatal(t, testProjectLayer)
	gen, err := b.Create(ctx, config.LayerKey, l.Raw, "application/yaml")
	if err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	if failed := fanOutLayer(ctx, &w, env, l, gen); failed != 1 {
		t.Fatalf("failed = %d, want 1\n%s", failed, w.String())
	}
	if !strings.Contains(w.String(), "acme/app: copied to "+config.LayerCopyKey(appSlug)) {
		t.Fatalf("the other repository was not copied: %s", w.String())
	}
	if !strings.Contains(w.String(), "acme/other: skipped: provider of acme/other is not known here") {
		t.Fatalf("the failure was not reported: %s", w.String())
	}
	repos["acme/other"] = localcfg.Repo{Provider: "github", Workflows: []string{"svc"}}
	w.Reset()
	if failed := fanOutLayer(ctx, &w, env, l, gen); failed != 0 {
		t.Fatalf("failed = %d, want 0 on rerun\n%s", failed, w.String())
	}
	otherSlug := mustSlug("github", "acme/other")
	if data, _, rerr := b.Read(ctx, config.LayerCopyKey(otherSlug)); rerr != nil || string(data) != string(l.Raw) {
		t.Fatalf("the repository's copy was not completed on rerun: data %q, err %v", data, rerr)
	}
}

// Review Focus 4, ruling: a repository's provider resolves exactly as a
// launch's own env.repoSlug does (the local config, else the checkout
// whose origin is that repository), never guessed from the layer's
// defaults.git.provider: Repo.Provider's own doc (internal/localcfg/
// localcfg.go) says empty means "the checkout's fugaro.yaml decides",
// not "the project layer decides". A repository with a known provider
// still gets its copy.
func TestFanOutNeverGuessesAProviderFromTheLayerDefault(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	env := envOnRepos(b, map[string]localcfg.Repo{
		"acme/app":   {Provider: "github", Workflows: []string{"web"}},
		"acme/other": {Workflows: []string{"svc"}}, // no provider of its own
	})
	// defaults.git.provider is bitbucket: a different provider than
	// acme/app's, so a wrongly-guessed slug for acme/other is easy to
	// tell apart from a correct one.
	l := parseLayerOrFatal(t, strings.Replace(testProjectLayer, "provider: github", "provider: bitbucket", 1))
	gen, err := b.Create(ctx, config.LayerKey, l.Raw, "application/yaml")
	if err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	failed := fanOutLayer(ctx, &w, env, l, gen)
	if failed != 1 {
		t.Fatalf("failed = %d, want 1: %s", failed, w.String())
	}
	if !strings.Contains(w.String(), "acme/app: copied to "+config.LayerCopyKey(appSlug)) {
		t.Fatalf("the repository with a known provider was not copied: %s", w.String())
	}
	if !strings.Contains(w.String(), "provider of acme/other is not known here") || !strings.Contains(w.String(), "its copy was not written") {
		t.Fatalf("no note that the provider could not be determined: %s", w.String())
	}
	guessedSlug := mustSlug("bitbucket", "acme/other")
	if _, _, rerr := b.Read(ctx, config.LayerCopyKey(guessedSlug)); !errors.Is(rerr, blobx.ErrNotExist) {
		t.Fatalf("a copy was written under a slug guessed from the layer's default provider: %v", rerr)
	}
}

// Review Focus 4, ruling: warnOldImages must resolve a repository's
// provider the same way, never guessing from the layer's default; a
// build record that only the guessed (wrong) slug would find must stay
// unseen.
func TestWarnOldImagesNeverGuessesAProviderFromTheLayerDefault(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	data, err := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/other", Workflow: "svc", BaseRef: release050, FugaroVersion: "0.5.0"})
	if err != nil {
		t.Fatal(err)
	}
	guessedSlug := mustSlug("bitbucket", "acme/other")
	fake.Put("fugaro-runs-proj-1234", imagecheck.RecordKey(guessedSlug, "svc"), data)
	env := envOnRepos(b, map[string]localcfg.Repo{"acme/other": {Workflows: []string{"svc"}}})
	var w bytes.Buffer
	warnOldImages(ctx, &w, env)
	if w.String() != "" {
		t.Fatalf("warned using a provider guessed from the layer's default: %s", w.String())
	}
}

// Review Focus 4: during a fan-out the main object IS already published,
// so a copy's 403 must not say "nothing was published".
func TestFanOutForbiddenMessageDoesNotClaimNothingWasPublished(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	fake.DenyWrites("fugaro-runs-proj-1234", "builds/")
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	env := envOnRepos(b, map[string]localcfg.Repo{"acme/app": {Provider: "github", Workflows: []string{"web"}}})
	l := parseLayerOrFatal(t, testProjectLayer)
	gen, err := b.Create(ctx, config.LayerKey, l.Raw, "application/yaml")
	if err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	if failed := fanOutLayer(ctx, &w, env, l, gen); failed != 1 {
		t.Fatalf("failed = %d: %s", failed, w.String())
	}
	if strings.Contains(w.String(), "nothing was published") {
		t.Fatalf("a copy failure falsely claimed nothing was published: %s", w.String())
	}
	if !strings.Contains(w.String(), "operator role") {
		t.Fatalf("stderr lacks the operator-role text: %s", w.String())
	}
}

// Review Focus 4: copies are written with an unconditional Put. If
// publisher A writes v1, then publisher B publishes v2 before A's own
// (delayed) fan-out runs, A's fan-out must notice config.LayerKey is no
// longer the generation it wrote and stop, rather than put v1's bytes
// over B's v2.
func TestFanOutStopsWhenAnotherPublisherReplacedTheLayer(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	env := envOnRepos(b, map[string]localcfg.Repo{
		"acme/app":   {Provider: "github", Workflows: []string{"web"}},
		"acme/other": {Provider: "github", Workflows: []string{"svc"}},
	})
	l := parseLayerOrFatal(t, testProjectLayer)
	genA, err := b.Create(ctx, config.LayerKey, l.Raw, "application/yaml")
	if err != nil {
		t.Fatal(err)
	}
	v2 := strings.Replace(testProjectLayer, "auth: api-key", "auth: oauth", 1)
	l2 := parseLayerOrFatal(t, v2)
	if _, err := b.ReplaceIfType(ctx, config.LayerKey, l2.Raw, "application/yaml", genA, l.Raw); err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	failed := fanOutLayer(ctx, &w, env, l, genA) // A's own, now-stale generation
	if failed == 0 {
		t.Fatalf("A's late fan-out did not stop: %s", w.String())
	}
	if !strings.Contains(w.String(), "stopped") || !strings.Contains(w.String(), "another publisher changed") {
		t.Fatalf("no note that another publisher changed the layer: %s", w.String())
	}
	if _, _, rerr := b.Read(ctx, config.LayerCopyKey(appSlug)); !errors.Is(rerr, blobx.ErrNotExist) {
		t.Fatalf("A's stale bytes were copied over B's publish: %v", rerr)
	}
}

// Review Focus 4: a genuine generation race ("another publisher changed
// it") and a plain read failure (a transient error, a permissions
// problem, the object briefly over LayerMaxBytes) are not the same thing;
// conflating them sends an operator chasing a nonexistent concurrent
// publish instead of the real connectivity/permissions issue.
func TestFanOutStopsWithoutClaimingARaceOnAReadError(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	env := envOnRepos(b, map[string]localcfg.Repo{"acme/app": {Provider: "github", Workflows: []string{"web"}}})
	l := parseLayerOrFatal(t, testProjectLayer)
	gen, err := b.Create(ctx, config.LayerKey, l.Raw, "application/yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Nothing else published; the object just happens to be unreadable
	// right now (here: oversized), not replaced by another publisher.
	fake.Put("fugaro-runs-proj-1234", config.LayerKey, bytes.Repeat([]byte("x"), config.LayerMaxBytes+1))
	var w bytes.Buffer
	failed := fanOutLayer(ctx, &w, env, l, gen)
	if failed != 1 {
		t.Fatalf("failed = %d, want 1: %s", failed, w.String())
	}
	if strings.Contains(w.String(), "another publisher changed") {
		t.Fatalf("a read error was misreported as a concurrent publish: %s", w.String())
	}
	if !strings.Contains(w.String(), "could not confirm") {
		t.Fatalf("stderr lacks the real-error note: %s", w.String())
	}
	if _, _, rerr := b.Read(ctx, config.LayerCopyKey(appSlug)); !errors.Is(rerr, blobx.ErrNotExist) {
		t.Fatalf("a copy was written despite the unconfirmed layer: %v", rerr)
	}
}
