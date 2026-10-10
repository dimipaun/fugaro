package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
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
