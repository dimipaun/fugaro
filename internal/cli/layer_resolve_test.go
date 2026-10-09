package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
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

// Production risk: findLayer must not hang when the bucket client retries
// a flaky or offline network forever; layerBucketTimeout bounds both the
// open and the read, and a timeout is isUnreachable's own
// context.DeadlineExceeded case, so it falls back to the cache exactly as
// a dial failure or a 5xx would.
//
// Mutation (run, restore): drop the `context.WithTimeout` wrap around the
// layerRead call in findLayer (pass ctx straight through), and this test
// hangs until the outer test timeout kills the whole run, instead of
// returning within layerBucketTimeout.
func TestFindLayerTimesOutAndFallsBackToTheCache(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow); err != nil {
		t.Fatal(err)
	}
	oldTimeout := layerBucketTimeout
	layerBucketTimeout = 20 * time.Millisecond
	t.Cleanup(func() { layerBucketTimeout = oldTimeout })
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(ctx context.Context, b *blobx.Bucket) ([]byte, int64, error) {
		<-ctx.Done() // a client stuck retrying a dead connection
		return nil, 0, ctx.Err()
	}
	start := time.Now()
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(time.Hour))
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("findLayer took %s; layerBucketTimeout did not bound the read", d)
	}
	if err != nil || got.Layer == nil || !strings.Contains(got.Note, "unreachable") {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// Without a usable cache, a lenient caller gets "not checked" instead of
// hanging (and a strict one a clear error, TestFindLayerOpenFailureGoesThroughBucketErrFor
// already covers every other open failure's shape the same way a timeout
// takes).
func TestFindLayerTimesOutLenientWithoutACache(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	oldTimeout := layerBucketTimeout
	layerBucketTimeout = 20 * time.Millisecond
	t.Cleanup(func() { layerBucketTimeout = oldTimeout })
	open := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = open })
	layerBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	start := time.Now()
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{Lenient: true}, layerNow)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("findLayer took %s; layerBucketTimeout did not bound the open", d)
	}
	if err != nil || !got.Unknown || got.Layer != nil || !strings.Contains(got.Note, "not checked") {
		t.Fatalf("got %+v, %v", got, err)
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

// (a) An object over the size limit is always an error, in strict and in
// lenient mode: it is present and published, never "no layer", and never
// silently swallowed the way an unreadable bucket is in lenient mode.
func TestFindLayerOversizedObjectIsAlwaysAnError(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, fmt.Errorf("%s: %w (70000 bytes, the cap is %d)", config.LayerKey, blobx.ErrTooLarge, config.LayerMaxBytes)
	}
	for _, lenient := range []bool{false, true} {
		got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{Lenient: lenient}, layerNow)
		if err == nil || got.Unknown || got.Layer != nil || !strings.Contains(err.Error(), "over the") {
			t.Fatalf("lenient=%v: got %+v, %v", lenient, got, err)
		}
	}
}

// (b) ErrNotExist (the layer was retired or never published) drops a stale
// cache entry: a repository must not keep resolving against a layer the
// project withdrew.
func TestFindLayerAbsentDropsStaleCache(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow); err != nil {
		t.Fatal(err)
	}
	if _, ok := localcfg.LoadLayerCache(os.Getenv, "aurora"); !ok {
		t.Fatal("expected a cache entry after the first read")
	}
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) { return nil, 0, blobx.ErrNotExist }
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(time.Minute))
	if err != nil || got.Layer != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, ok := localcfg.LoadLayerCache(os.Getenv, "aurora"); ok {
		t.Fatal("a stale cache entry survived the layer going absent")
	}
}

// (c) A forged or otherwise invalid published object deletes the cache
// entry: the next command must not keep offering a cache of a layer that
// no longer validates.
func TestFindLayerInvalidObjectDropsTheCache(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow); err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, config.LayerKey, layerWithDefaults("  followup: { trusted: ['1'] }\n"))
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(time.Minute)); err == nil {
		t.Fatal("a forged object was accepted")
	}
	if _, ok := localcfg.LoadLayerCache(os.Getenv, "aurora"); ok {
		t.Fatal("the cache survived an invalid published object")
	}
}

// (d) and message (3): a corrupt local cache entry fails closed (never
// silently treated as no layer or as the published object) and is
// deleted; the error blames the local cache specifically, not the
// operator, and tells the caller to rerun.
func TestFindLayerCorruptCacheFailsClosed(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	lc := fileEnv(t, f).lc
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", CheckedAt: layerNow,
		YAML: layerWithDefaults("  followup: { trusted: ['1'] }\n"),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{Offline: true}, layerNow.Add(time.Minute))
	switch {
	case err == nil || got.Layer != nil:
		t.Fatalf("got %+v, %v", got, err)
	case !strings.Contains(err.Error(), "local cache"):
		t.Fatalf("does not blame the local cache: %v", err)
	case strings.Contains(err.Error(), "ask an operator"):
		t.Fatalf("blames an operator for a local cache problem: %v", err)
	}
	if _, ok := localcfg.LoadLayerCache(os.Getenv, "aurora"); ok {
		t.Fatal("the corrupt cache entry survived")
	}
}

// (e) The cache is refused, offline, when it belongs to another
// installation: a different GCP project, or a different bucket.
func TestFindLayerOfflineRefusesACacheOfAnotherInstallation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry localcfg.SharedCacheEntry
	}{
		{"a different GCP project", localcfg.SharedCacheEntry{GCPProject: "proj-9999", Bucket: "fugaro-runs-proj-1234", CheckedAt: layerNow, YAML: testProjectLayer}},
		{"a different bucket", localcfg.SharedCacheEntry{GCPProject: "proj-1234", Bucket: "fugaro-runs-other", CheckedAt: layerNow, YAML: testProjectLayer}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			isolateCache(t)
			publishedLayer(t, f, "")
			lc := fileEnv(t, f).lc
			if err := localcfg.SaveLayerCache(os.Getenv, "aurora", tc.entry); err != nil {
				t.Fatal(err)
			}
			got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{Offline: true}, layerNow.Add(time.Minute))
			if err != nil || !got.Unknown || got.Layer != nil || !strings.Contains(got.Note, "no project layer of aurora is cached") {
				t.Fatalf("%s: got %+v, %v", tc.name, got, err)
			}
		})
	}
}

// (f) Lenient with no project config selected forces cache-only and never
// opens the bucket at all (not merely "never reads" it).
func TestFindLayerLenientNoConfigNeverOpensTheBucket(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	opens := 0
	open := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = open })
	layerBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) { opens++; return open(ctx, url) }
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), nil, layerOptions{Lenient: true}, layerNow)
	if err != nil || !got.Unknown || got.Layer != nil || opens != 0 {
		t.Fatalf("got %+v, %v, %d opens", got, err, opens)
	}
}

// (g) Lenient with a config selected and an unreadable bucket (not a
// network-unreachable one, which the cache would cover) leaves the layer
// unknown with a note; it never fails the command the way strict mode
// would.
func TestFindLayerLenientWithConfigUnreadableBucketDoesNotFail(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, &googleapi.Error{Code: 500, Message: "server error"}
	}
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{Lenient: true}, layerNow)
	if err != nil || !got.Unknown || got.Layer != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// findLayer must never open a gs:// bucket this installation's own
// endpoints say is a fake (sharedcfg.go's own bucket reads already refuse
// this the same way): skipGSOnFakeEndpoints is off for every other test in
// this package (main_test.go), since they reach buckets through seams
// while carrying fake endpoints, so this test turns it back on, as the
// tests of the skip itself in sharedcfg_test.go and init_fugaroyaml_test.go
// do, to exercise the guard rather than the seam.
//
// Mutation (run, restore): delete the `if lc != nil &&
// fakeEndpointsOnGS(lc, bucketURL)` block from findLayer, and this test
// fails: layerBucketOpener is never overridden here, so it would call the
// real blobx.Open with a gs:// URL (main_test.go's TestMain would panic
// the whole run, which is the point: this guard is what keeps that panic
// from ever firing for a config like this one).
func TestFindLayerSkipsAFakeEndpointGSBucket(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := *fileEnv(t, f).lc
	lc.Bucket = "" // as a real installation's config would be: BucketURL() falls back to gs://<runs_bucket>
	old := skipGSOnFakeEndpoints
	skipGSOnFakeEndpoints = true
	t.Cleanup(func() { skipGSOnFakeEndpoints = old })

	lenient, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), &lc, layerOptions{Lenient: true}, layerNow)
	if err != nil || !lenient.Unknown || lenient.Layer != nil || !strings.Contains(lenient.Note, "fake") {
		t.Fatalf("lenient: got %+v, %v", lenient, err)
	}
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), &lc, layerOptions{}, layerNow); err == nil || !strings.Contains(err.Error(), "fake") {
		t.Fatalf("strict: %v", err)
	}
}

// (h) A runs bucket that is not the default name gets no layer, with a
// note, rather than reading another installation's bucket.
func TestFindLayerNonDefaultBucketNameGetsNoLayer(t *testing.T) {
	f := newCloudFixture(t) // runs_bucket: unused-bucket, not the default name
	isolateCache(t)
	lc := fileEnv(t, f).lc
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow)
	if err != nil || got.Layer != nil || !strings.Contains(got.Note, "needs the default runs bucket name") {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// Messages (1) and (2): the offline note says "--offline:" only when the
// user actually passed --offline, never when a lenient command forced it
// because no project config is selected.
func TestFindLayerOfflineNoteOnlySaysOfflineWhenPassed(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow); err != nil {
		t.Fatal(err)
	}
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{Offline: true}, layerNow.Add(time.Hour))
	if err != nil || !strings.HasPrefix(got.Note, "--offline:") {
		t.Fatalf("explicit --offline: got %+v, %v", got, err)
	}
	got, err = findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), nil, layerOptions{Lenient: true}, layerNow.Add(time.Hour))
	if err != nil || strings.Contains(got.Note, "--offline") {
		t.Fatalf("forced by lenient with no config selected: got %+v, %v", got, err)
	}
}

// Message (2): the "no project layer is cached" note gives its own true
// reason in each of its three cases, never the same sentence for all.
func TestFindLayerOfflineNoteNamesItsTrueReason(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	lc := fileEnv(t, f).lc

	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), nil, layerOptions{Lenient: true}, layerNow)
	if err != nil || !strings.Contains(got.Note, "no project config is selected and no project layer of aurora is cached") {
		t.Fatalf("no config selected, nothing cached: got %+v, %v", got, err)
	}

	got, err = findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{Offline: true}, layerNow)
	if err != nil || strings.Contains(got.Note, "no project config is selected") || !strings.Contains(got.Note, "no project layer of aurora is cached") {
		t.Fatalf("--offline with a config selected: got %+v, %v", got, err)
	}

	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", CheckedAt: layerNow, YAML: testProjectLayer,
	}); err != nil {
		t.Fatal(err)
	}
	got, err = findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{Offline: true}, layerNow.Add(8*24*time.Hour))
	if err != nil || strings.Contains(got.Note, "no project layer of aurora is cached") || !strings.Contains(got.Note, "days old, over the 7-day offline limit") {
		t.Fatalf("a stale cache: got %+v, %v", got, err)
	}
}

// Message (4): a bucket open failure that is not an unreachable network
// (here, access denied) carries the project layer's context and the
// bucket, and reads as "no access" through bucketErrFor, not a generic
// remote failure.
func TestFindLayerOpenFailureGoesThroughBucketErrFor(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	open := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = open })
	layerBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		return nil, &googleapi.Error{Code: 403, Message: "forbidden"}
	}
	_, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow)
	if err == nil || !strings.Contains(err.Error(), "no access") || !strings.Contains(err.Error(), "the project layer of aurora") {
		t.Fatalf("err = %v", err)
	}
}

// Message (5): a selected project config whose own name differs from the
// repository's project: never has its bucket substituted (findLayer, line
// ~120), but that alone does not guarantee isolation if some other caller
// of config.Resolve (the runner, against a different ref) is handed a
// mismatched repository and layer; config.Resolve's own anchor check
// refuses that pairing regardless.
func TestResolveRefusesALayerOfAnotherProject(t *testing.T) {
	layer, ps := config.ParseProjectLayer([]byte(testProjectLayer), config.LayerAnchor{Project: "aurora", GCPProject: "proj-1234"})
	if len(ps) > 0 {
		t.Fatalf("test layer: %v", ps)
	}
	_, _, ps = config.Resolve([]byte("version: 1\nproject: other\ngcp_project: proj-1234\n"), layer)
	if len(ps) == 0 || !strings.Contains(ps[0].Message, "aurora") {
		t.Fatalf("Resolve did not refuse a mismatched project: %v", ps)
	}
}

// o.Data (Cloud Build, the check job: a layer already read and checked)
// never touches the bucket, succeeds on a valid layer and fails, naming
// Where, on an invalid one.
func TestFindLayerFromData(t *testing.T) {
	opens := 0
	open := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = open })
	layerBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) { opens++; return open(ctx, url) }

	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), nil, layerOptions{Data: []byte(testProjectLayer), Where: "the pinned copy"}, layerNow)
	if err != nil || got.Layer == nil || got.Where != "the pinned copy" || opens != 0 {
		t.Fatalf("got %+v, %v, %d opens", got, err, opens)
	}

	_, err = findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), nil,
		layerOptions{Data: []byte(layerWithDefaults("  followup: { trusted: ['1'] }\n")), Where: "the pinned copy"}, layerNow)
	if err == nil || !strings.Contains(err.Error(), "the pinned copy is invalid") {
		t.Fatalf("err = %v", err)
	}
	if opens != 0 {
		t.Fatalf("%d opens", opens)
	}
}

// o.File (--project-layer FILE) never touches the bucket, succeeds on a
// valid file, fails naming the path on an invalid one, and surfaces
// readLayerFile's own errors (here, a missing file) as a user error.
func TestFindLayerFromFile(t *testing.T) {
	opens := 0
	open := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = open })
	layerBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) { opens++; return open(ctx, url) }

	dir := t.TempDir()
	path := filepath.Join(dir, "layer.yaml")
	if err := os.WriteFile(path, []byte(testProjectLayer), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), nil, layerOptions{File: path}, layerNow)
	if err != nil || got.Layer == nil || got.Where != path {
		t.Fatalf("got %+v, %v", got, err)
	}

	if err := os.WriteFile(path, []byte(layerWithDefaults("  followup: { trusted: ['1'] }\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), nil, layerOptions{File: path}, layerNow)
	if err == nil || !strings.Contains(err.Error(), path+" is invalid") {
		t.Fatalf("invalid file: err = %v", err)
	}

	missing := filepath.Join(dir, "missing.yaml")
	_, err = findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), nil, layerOptions{File: missing}, layerNow)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("missing file: err = %v", err)
	}
	if opens != 0 {
		t.Fatalf("%d opens", opens)
	}
}

// o.NoBucket (the daily check job, whose account cannot read fugaro/)
// without Data means none applies, and the bucket is never opened.
func TestFindLayerNoBucketIsNone(t *testing.T) {
	opens := 0
	open := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = open })
	layerBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) { opens++; return open(ctx, url) }
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), nil, layerOptions{NoBucket: true}, layerNow)
	if err != nil || got.Layer != nil || got.Unknown || opens != 0 {
		t.Fatalf("got %+v, %v, %d opens", got, err, opens)
	}
}

// readLayerFile's own error paths: a missing file, a non-regular file
// (here, a directory) and one over the size limit; a regular file within
// the limit is read whole.
func TestReadLayerFile(t *testing.T) {
	dir := t.TempDir()

	if _, err := readLayerFile(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("a missing file was read")
	}

	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := readLayerFile(sub); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("a directory was read: %v", err)
	}

	big := filepath.Join(dir, "big.yaml")
	if err := os.WriteFile(big, make([]byte, config.LayerMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLayerFile(big); err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("an oversized file was read: %v", err)
	}

	ok := filepath.Join(dir, "ok.yaml")
	if err := os.WriteFile(ok, []byte(testProjectLayer), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readLayerFile(ok)
	if err != nil || string(data) != testProjectLayer {
		t.Fatalf("got %q, %v", data, err)
	}
}

// parseCheckoutFugaroYAML converts a findLayer/resolveFugaroYAML error
// (here, an invalid published object, which is always an error, even in
// the lenient mode parseCheckoutFugaroYAML always uses) into a single
// Problem naming "project layer", with a nil Cfg.
func TestParseCheckoutFugaroYAMLConvertsAnErrorToAProblem(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithDefaults("  followup: { trusted: ['1'] }\n"))
	cfg, ps := parseCheckoutFugaroYAML(context.Background(), []byte(minimalAnchored), fileEnv(t, f).lc)
	if cfg != nil {
		t.Fatalf("cfg = %+v, want nil", cfg)
	}
	if len(ps) != 1 || ps[0].Path != "project layer" || !strings.Contains(ps[0].Message, "is invalid") {
		t.Fatalf("ps = %+v", ps)
	}
}
