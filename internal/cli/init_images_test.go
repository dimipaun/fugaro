package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
	"golang.org/x/oauth2"
)

// imagesRegistry is an in-process registry for the images stage's tests: as
// the source it serves one release image per name (an index with an amd64
// image); as the destination it takes uploads, and calls onTag when a tag
// is written (the AR fake learns of the history image that way). The
// mirror's own tests (internal/mirror) cover the protocol.
type imagesRegistry struct {
	srv    *httptest.Server
	mu     sync.Mutex
	blobs  map[string][]byte
	mans   map[string][]byte
	types  map[string]string
	tags   map[string]string // repo:tag -> digest
	ups    map[string][]byte
	puts   int // manifest and blob writes
	onTag  func(repo, tag string)
	onBlob func()
}

func cdg(b []byte) string { h := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(h[:]) }

func newImagesRegistry(t *testing.T) *imagesRegistry {
	r := &imagesRegistry{blobs: map[string][]byte{}, mans: map[string][]byte{}, types: map[string]string{}, tags: map[string]string{}, ups: map[string][]byte{}}
	r.srv = httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(r.srv.Close)
	return r
}

// publish makes repo:tag an index with one linux/amd64 image.
func (r *imagesRegistry) publish(repo, tag string) {
	cfg := []byte(`{"os":"linux","architecture":"amd64"}`)
	layer := []byte("layer of " + repo)
	r.blobs[cdg(cfg)], r.blobs[cdg(layer)] = cfg, layer
	im, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{"digest": cdg(cfg), "size": len(cfg)}, "layers": []any{map[string]any{"digest": cdg(layer), "size": len(layer)}}})
	r.mans[cdg(im)], r.types[cdg(im)] = im, "application/vnd.oci.image.manifest.v1+json"
	ix, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json",
		"manifests": []any{map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": cdg(im), "size": len(im), "platform": map[string]string{"os": "linux", "architecture": "amd64"}}}})
	r.mans[cdg(ix)], r.types[cdg(ix)] = ix, "application/vnd.oci.image.index.v1+json"
	r.tags[repo+":"+tag] = cdg(ix)
}

func (r *imagesRegistry) handle(w http.ResponseWriter, q *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := strings.TrimPrefix(q.URL.Path, "/v2/")
	switch {
	case strings.Contains(p, "/blobs/uploads/"):
		repo, id, _ := strings.Cut(p, "/blobs/uploads/")
		switch q.Method {
		case "POST":
			r.puts++
			id = fmt.Sprint("u", len(r.ups))
			r.ups[id] = nil
			w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+id)
			w.WriteHeader(202)
		case "PATCH":
			b, _ := io.ReadAll(q.Body)
			r.ups[id] = append(r.ups[id], b...)
			w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+id)
			w.WriteHeader(202)
		case "PUT":
			d := q.URL.Query().Get("digest")
			if cdg(r.ups[id]) != d {
				w.WriteHeader(400)
				return
			}
			r.blobs[d] = r.ups[id]
			if r.onBlob != nil {
				r.onBlob()
			}
			w.WriteHeader(201)
		}
	case strings.Contains(p, "/blobs/"):
		_, d, _ := strings.Cut(p, "/blobs/")
		b, ok := r.blobs[d]
		if !ok {
			http.NotFound(w, q)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		if q.Method == "GET" {
			_, _ = w.Write(b)
		}
	case strings.Contains(p, "/manifests/"):
		repo, ref, _ := strings.Cut(p, "/manifests/")
		if q.Method == "PUT" {
			r.puts++
			b, _ := io.ReadAll(q.Body)
			d := cdg(b)
			r.mans[d], r.types[d], r.tags[repo+":"+ref] = b, q.Header.Get("Content-Type"), d
			if r.onTag != nil {
				r.onTag(repo, ref)
			}
			w.WriteHeader(201)
			return
		}
		d := ref
		if !strings.HasPrefix(ref, "sha256:") {
			d = r.tags[repo+":"+ref]
		}
		b, ok := r.mans[d]
		if !ok {
			http.NotFound(w, q)
			return
		}
		w.Header().Set("Content-Type", r.types[d])
		w.Header().Set("Docker-Content-Digest", d)
		if q.Method == "GET" {
			_, _ = w.Write(b)
		}
	}
}

// imagesRig wires a release-version CLI to a fake source and destination.
type imagesRig struct {
	*fbRig
	src, dst *imagesRegistry
	built    int
}

func newImagesRig(t *testing.T, version string) *imagesRig {
	t.Helper()
	r := &imagesRig{fbRig: newFBRig(t), src: newImagesRegistry(t), dst: newImagesRegistry(t)}
	t.Chdir(t.TempDir()) // outside any checkout: no fugaro.yaml names a base
	for _, n := range []string{"fugaro-history", "fugaro-go", "fugaro-web-node"} {
		r.src.publish("dimipaun/"+n, "1.2.3")
	}
	r.dst.onTag = func(repo, tag string) {
		if repo == initProject+"/fugaro-base/history" {
			r.historyImage() // the AR API now sees the tag the job's image check reads
		}
	}
	oldV, oldNew := Version, newMirror
	Version = version
	newMirror = func(ctx context.Context, lc *localcfg.Config, allow []string) (*mirror.Mirror, error) {
		r.built++
		return &mirror.Mirror{
			Token: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "user-token"}), Allow: allow,
			Endpoint: func(h string) string {
				if h == "ghcr.io" {
					return r.src.srv.URL
				}
				return r.dst.srv.URL
			},
		}, nil
	}
	t.Cleanup(func() { Version, newMirror = oldV, oldNew })
	return r
}

// A development build copies nothing and says what to do, and init
// --firebase behaves as it did: the history job waits for its image.
func TestDevBuildHasNoSilentFallback(t *testing.T) {
	r := newImagesRig(t, "dev")
	newMirror = func(context.Context, *localcfg.Config, []string) (*mirror.Mirror, error) {
		t.Fatal("a development build tried to copy an image")
		return nil, nil
	}
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"development build", "images/build-base.sh", "--base-image KIND=<tag>", "sh images/build-base.sh history " + historyImagePath} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if r.dst.puts != 0 || r.built != 0 {
		t.Fatal("something was copied")
	}
	if h, _ := r.tfvars(t)["history"].(map[string]any); h["deploy_job"] != false {
		t.Errorf("the history job was deployed without its image: %v", h)
	}
}

func TestHistoryJobAppliedAfterMirror(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	for stage, want := range map[string]string{"installation": "skipped", "firebase": "changed", "images": "changed", "installation-2": "changed"} {
		if got := res.state(stage); got != want {
			t.Errorf("%s is %s, want %s (%+v)", stage, got, want, res.Stages)
		}
	}
	if got := strings.Join(r.applies(t), ","); got != "installation,firebase,installation" {
		t.Errorf("applies %s", got)
	}
	if h, _ := r.tfvars(t)["history"].(map[string]any); h["deploy_job"] != true || h["image"] != historyImagePath {
		t.Errorf("the history job was not deployed with the mirrored image: %v", h)
	}
	if r.dst.tags[initProject+"/fugaro-base/history:latest"] == "" {
		t.Errorf("history:latest is not at the destination: %v", r.dst.tags)
	}
	// Mirrored before the third apply, not after: the order of the stages.
	order := []string{}
	for _, s := range res.Stages {
		order = append(order, s.Name)
	}
	if got := strings.Join(order, ","); !strings.Contains(got, "images,installation-2") {
		t.Errorf("stage order %s", got)
	}
	// A rerun copies nothing.
	puts := r.dst.puts
	if out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil || r.dst.puts != puts {
		t.Fatalf("rerun: %v, %d writes\n%s", err, r.dst.puts-puts, out)
	}
}

func TestBaseImagesRecordedInConfig(t *testing.T) {
	r := newImagesRig(t, "v1.2.3")
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "go,web-node")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	bi := r.localConfig(t).BaseImages
	want := "us-east5-docker.pkg.dev/" + initProject + "/fugaro-base/"
	if bi["go"] != want+"fugaro-go:1.2.3" || bi["web-node"] != want+"fugaro-web-node:1.2.3" || len(bi) != 2 {
		t.Errorf("base_images = %v", bi)
	}
	for _, k := range []string{"fugaro-go", "fugaro-web-node"} {
		if r.dst.tags[initProject+"/fugaro-base/"+k+":1.2.3"] == "" {
			t.Errorf("%s not copied: %v", k, r.dst.tags)
		}
	}
	if r.dst.tags[initProject+"/fugaro-base/history:latest"] != "" {
		t.Error("the history image is copied without --firebase")
	}
	// Idempotent: nothing is sent again.
	puts := r.dst.puts
	out, _, err = executeStdin(t, "", "init", "--yes", "--base", "go")
	if err != nil || r.dst.puts != puts || !strings.Contains(out, "nothing to copy") {
		t.Fatalf("rerun: %v, %d writes\n%s", err, r.dst.puts-puts, out)
	}
}

// A kind the local config points at its own image (a dev tag, a hand-pushed
// image) is left alone, and --base-image wins over --base in the same run.
func TestPointedBaseImageIsNotMirrored(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	r.appendConfig(t, "base_images: { go: \"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-go:dev-abc\" }\n")
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "go,web-node", "--base-image", "web-node=ghcr.io/me/fugaro-web-node:mine")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.dst.puts != 0 || r.built != 0 {
		t.Errorf("something was mirrored for a pointed kind: %d writes\n%s", r.dst.puts, out)
	}
	bi := r.localConfig(t).BaseImages
	if !strings.HasSuffix(bi["go"], "fugaro-go:dev-abc") || bi["web-node"] != "ghcr.io/me/fugaro-web-node:mine" {
		t.Errorf("base_images = %v", bi)
	}
	for _, want := range []string{"base go: the local config's base image", "base web-node: --base-image points it"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}

// A mirrored release image of an older CLI is the project's own to move on.
func TestMirroredBaseMovesWithTheCLI(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	r.appendConfig(t, "base_images: { go: \"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-go:1.0.0\" }\n")
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "go"); err != nil {
		t.Fatal(err)
	}
	if got := r.localConfig(t).BaseImages["go"]; !strings.HasSuffix(got, "fugaro-go:1.2.3") {
		t.Errorf("base_images.go = %s", got)
	}
}

// The stage takes its own confirmation: with no --yes and no terminal it
// shows what it would copy and refuses, having copied nothing (the
// installation has no changes here, so it is the images stage that asks).
func TestImagesStageNeedsItsOwnConfirmation(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	r.stateBucket()
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	if _, _, err := executeStdin(t, "", "init", "--yes"); err != nil { // writes the config
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "init", "--base", "go")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "needs a real terminal: "+initflow.NoTerminalAdvice) {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(out, "adds sha256:") || r.dst.puts != 0 {
		t.Fatalf("%d writes\n%s", r.dst.puts, out)
	}
}

// All blobs are at the destination but the tag is not (a run that died at
// the manifest): the manifest is pushed, the stage reports the change, and
// the config records the tag only once it exists.
func TestManifestOnlyCopyIsAChange(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	for d, b := range r.src.blobs {
		r.dst.blobs[d] = b
	}
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "go", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if res.state("images") != "changed" || r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] == "" {
		t.Fatalf("images %s, tags %v", res.state("images"), r.dst.tags)
	}
	if !strings.HasSuffix(r.localConfig(t).BaseImages["go"], "fugaro-go:1.2.3") {
		t.Errorf("base_images = %v", r.localConfig(t).BaseImages)
	}
}

// A failed copy records nothing: base_images never names a tag that does
// not exist.
func TestFailedCopyRecordsNothing(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	delete(r.src.tags, "dimipaun/fugaro-go:1.2.3")
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "go"); err == nil {
		t.Fatal("a missing source succeeded")
	}
	if got := r.localConfig(t).BaseImages["go"]; got != "" {
		t.Errorf("base_images.go = %s", got)
	}
}

func TestReleaseTagIsNotMovedWithoutReplaceImage(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	other := cdg([]byte("hand pushed"))
	r.dst.mans[other], r.dst.types[other] = []byte("hand pushed"), "application/vnd.oci.image.manifest.v1+json"
	r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] = other
	_, _, err := executeStdin(t, "", "init", "--yes", "--base", "go")
	if err == nil || !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), "--replace-image") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("err = %v", err)
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] != other || r.dst.puts != 0 {
		t.Fatal("the release tag moved")
	}
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "go", "--replace-image"); err != nil || r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] == other {
		t.Fatalf("with --replace-image: %v", err)
	}
}

// A hand-pushed history:latest (the live installs have one) is not replaced
// by --yes alone; --replace-image does it, visibly, naming both digests.
func TestHistoryLatestNeedsReplaceImage(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	other := cdg([]byte("hand pushed history"))
	r.dst.mans[other], r.dst.types[other] = []byte("x"), "application/vnd.oci.image.manifest.v1+json"
	r.dst.tags[initProject+"/fugaro-base/history:latest"] = other
	_, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
	if err == nil || !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), "--replace-image") {
		t.Fatalf("err = %v", err)
	}
	if r.dst.tags[initProject+"/fugaro-base/history:latest"] != other || r.dst.puts != 0 {
		t.Fatal("history:latest was replaced")
	}
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--replace-image")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "REPLACES "+other+" with sha256:") || r.dst.tags[initProject+"/fugaro-base/history:latest"] == other {
		t.Fatalf("not replaced visibly:\n%s", out)
	}
}

// --replace-image is not a confirmation: without --yes the stage still asks.
func TestReplaceImageStillAsks(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	r.stateBucket()
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	if _, _, err := executeStdin(t, "", "init", "--yes"); err != nil {
		t.Fatal(err)
	}
	other := cdg([]byte("hand pushed"))
	r.dst.mans[other], r.dst.types[other] = []byte("x"), "application/vnd.oci.image.manifest.v1+json"
	r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] = other
	out, _, err := executeStdin(t, "", "init", "--base", "go", "--replace-image")
	if err == nil || !strings.Contains(err.Error(), "needs a real terminal: "+initflow.NoTerminalAdvice) || r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] != other || r.dst.puts != 0 {
		t.Fatalf("err %v, %d writes\n%s", err, r.dst.puts, out)
	}
	if !strings.Contains(out, "REPLACES "+other) {
		t.Errorf("the plan does not say what is replaced:\n%s", out)
	}
}

// A pin that matches nothing is an error, and the confirmation says which
// images are not pinned.
func TestExpectDigestUnusedAndUnpinnedListed(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	good := r.src.tags["dimipaun/fugaro-go:1.2.3"]
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "go", "--expect-digest", "web-node="+good); err == nil || !strings.Contains(err.Error(), "matches no image") {
		t.Fatalf("err = %v", err)
	}
	if r.dst.puts != 0 {
		t.Fatal("copied with an unused pin")
	}
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "go,web-node", "--expect-digest", "go="+good)
	if err != nil || !strings.Contains(out, "NOT pinned with --expect-digest: web-node") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// One kind is copied and a later one fails in Copy (its release tag moved
// after the plan): nothing is recorded, and the stage fails with ErrTagMoved.
func TestLaterKindTagMovedRecordsNothing(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	// go is copied first; its tag write moves web-node's source tag.
	r.dst.onTag = func(repo, tag string) {
		if strings.HasSuffix(repo, "/fugaro-go") {
			r.src.tags["dimipaun/fugaro-web-node:1.2.3"] = cdg([]byte("moved"))
			r.src.mans[cdg([]byte("moved"))] = []byte("moved")
			r.src.types[cdg([]byte("moved"))] = "application/vnd.oci.image.index.v1+json"
		}
	}
	_, _, err := executeStdin(t, "", "init", "--yes", "--base", "go,web-node")
	if err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("err = %v", err)
	}
	if bi := r.localConfig(t).BaseImages; len(bi) != 0 {
		t.Errorf("base_images = %v", bi)
	}
}

// The destination tag changing after the plan is refused at the stage too.
func TestDestinationMovedAtStageLevel(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	other := cdg([]byte("someone else"))
	r.dst.mans[other], r.dst.types[other] = []byte("x"), "application/vnd.oci.image.manifest.v1+json"
	r.dst.onBlob = func() { r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] = other }
	_, _, err := executeStdin(t, "", "init", "--yes", "--base", "go", "--replace-image")
	if err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("err = %v", err)
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] != other || len(r.localConfig(t).BaseImages) != 0 {
		t.Fatal("overwritten or recorded")
	}
}

func TestExpectDigestPinsTheRelease(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	good := r.src.tags["dimipaun/fugaro-go:1.2.3"]
	bad := cdg([]byte("not the release"))
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "go", "--expect-digest", "go="+bad); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("a wrong pin: %v", err)
	}
	if r.dst.puts != 0 {
		t.Fatal("copied against a wrong pin")
	}
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "go", "--expect-digest", "go="+good); err != nil {
		t.Fatalf("the right pin: %v", err)
	}
	for _, v := range []string{"go=sha256:abc", "nope=" + good, "go"} {
		if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "go", "--expect-digest", v); err == nil {
			t.Errorf("--expect-digest %q accepted", v)
		}
	}
}

// An older CLI never downgrades a newer mirrored base image.
func TestOlderCLIDoesNotDowngradeBase(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	newer := "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-go:2.0.0"
	r.appendConfig(t, "base_images: { go: \""+newer+"\" }\n")
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "go")
	if err != nil || r.dst.puts != 0 || r.localConfig(t).BaseImages["go"] != newer || !strings.Contains(out, "newer than this CLI's") {
		t.Fatalf("%v, %d writes, base_images %v\n%s", err, r.dst.puts, r.localConfig(t).BaseImages, out)
	}
}

func TestPlanOnlyCopiesNothing(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	r.stateBucket()
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--plan-only"); err != nil {
		t.Fatal(err)
	}
	if r.dst.puts != 0 || len(r.applies(t)) != 0 {
		t.Fatalf("--plan-only wrote: %d registry writes, applies %v", r.dst.puts, r.applies(t))
	}
}

// The source is a registry/owner prefix; any host and path is accepted for a
// fork by design (it is the fork's explicit choice), but only in that shape,
// and the default is never used once one is named.
func TestImageSourceIsExplicit(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	for _, bad := range []string{"https://ghcr.io/acme", "ghcr.io", "ghcr.io/Acme", "acme"} {
		if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "go", "--image-source", bad); err == nil {
			t.Errorf("--image-source %q accepted", bad)
		}
	}
	r.src.publish("acme/fugaro-go", "1.2.3")
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "go", "--image-source", "ghcr.io/acme"); err != nil {
		t.Fatal(err)
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] == "" {
		t.Error("the fork's image was not copied")
	}
}

// The default mirror is built from the person's own credentials, and never
// against the fakes' no_auth (a fake run cannot reach a real registry).
func TestDefaultMirrorRefusesFakeAuth(t *testing.T) {
	lc := &localcfg.Config{}
	lc.Endpoints.NoAuth = true
	if _, err := newMirror(context.Background(), lc, nil); err == nil || !strings.Contains(err.Error(), "no_auth") {
		t.Fatalf("err = %v", err)
	}
}
