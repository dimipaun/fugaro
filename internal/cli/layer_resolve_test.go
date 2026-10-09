package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
)

var layerNow = time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)

// Review Focus 1: no fresh window, so a publish is seen at once.
func TestFindLayerReadsTheBucketEveryTime(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	first, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow)
	if err != nil || first.Layer == nil || first.Layer.SHA256 != config.LayerSum([]byte(testProjectLayer)) {
		t.Fatalf("first = %+v, %v", first, err)
	}
	v2 := strings.Replace(testProjectLayer, "auth: api-key", "auth: oauth", 1)
	writeBucketFile(t, f, config.LayerKey, v2)
	second, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(time.Minute))
	if err != nil || second.Layer.SHA256 != config.LayerSum([]byte(v2)) || second.Note != "" {
		t.Fatalf("second = %+v, %v", second, err)
	}
}

func TestFindLayerCacheOnlyWhenUnreachable(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow); err != nil {
		t.Fatal(err)
	}
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(3*24*time.Hour))
	if err != nil || got.Layer == nil || !strings.Contains(got.Note, "using the cached project layer of aurora, 3 days old") {
		t.Fatalf("unreachable: %+v, %v", got, err)
	}
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(8*24*time.Hour)); err == nil {
		t.Fatal("an 8-day-old cache stood in")
	}
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, &googleapi.Error{Code: 403, Message: "forbidden"}
	}
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "no access") {
		t.Fatalf("a 403 fell back to the cache: %v", err)
	}
}

// TestFindLayerCacheOnlyWhenBucketCannotBeOpened is
// TestFindLayerCacheOnlyWhenUnreachable's unreachable case, but for a
// bucket that fails to open at all (no route, refused connection): the
// cache stands in exactly as when it opens but the read fails (decision
// L13 does not distinguish the two).
func TestFindLayerCacheOnlyWhenBucketCannotBeOpened(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow); err != nil {
		t.Fatal(err)
	}
	open := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = open })
	layerBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(3*24*time.Hour))
	if err != nil || got.Layer == nil || !strings.Contains(got.Note, "using the cached project layer of aurora, 3 days old") {
		t.Fatalf("unreachable to open: %+v, %v", got, err)
	}
}

// Review Focus 4.
func TestFindLayerRefusesAnInvalidObject(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithDefaults("  followup: { trusted: ['1'] }\n"))
	_, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), fileEnv(t, f).lc, layerOptions{}, layerNow)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro/project-layer.yaml is invalid") ||
		!strings.Contains(err.Error(), "followup.trusted may only be set in: repo") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindLayerAbsentIsNone(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), fileEnv(t, f).lc, layerOptions{}, layerNow)
	if err != nil || got.Layer != nil || got.Unknown {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// Review Focus 5: no gcp_project:, no layer, no bucket read.
func TestUnanchoredCheckoutIgnoresTheLayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	reads := 0
	layerRead = func(ctx context.Context, b *blobx.Bucket) ([]byte, int64, error) { reads++; return read(ctx, b) }
	got, err := findLayer(context.Background(), os.Getenv, []byte("version: 1\nproject: aurora\n"), fileEnv(t, f).lc, layerOptions{}, layerNow)
	if err != nil || got.Layer != nil || reads != 0 {
		t.Fatalf("got %+v, %v, %d reads", got, err, reads)
	}
}

func TestResolveFugaroYAMLMinimal(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	rf, err := resolveFugaroYAML(context.Background(), []byte(minimalAnchored), fileEnv(t, f).lc, layerOptions{})
	if err != nil || len(rf.Problems) > 0 {
		t.Fatalf("%v %v", err, rf.Problems)
	}
	if rf.Cfg.Workflows[config.ImplicitWorkflow].Commands.Test != "sh test.sh" || rf.Res.SourceOf("workflows.default.commands.test") != "profile svc" {
		t.Fatalf("resolved %+v", rf.Cfg.Workflows)
	}
}

func TestResolveFugaroYAMLOfflineWithoutCache(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	rf, err := resolveFugaroYAML(context.Background(), []byte(minimalAnchored), fileEnv(t, f).lc, layerOptions{Offline: true})
	if err != nil || !rf.Layer.Unknown || len(rf.Problems) == 0 || !strings.Contains(rf.Problems[len(rf.Problems)-1].Message, "--project-layer") {
		t.Fatalf("%v %+v", err, rf)
	}
}
