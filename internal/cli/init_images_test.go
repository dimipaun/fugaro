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
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
	"github.com/dimipaun/fugaro/internal/testutil"
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
	// basePin is the digest the fake resolve-and-verify step returns: the
	// base image the fake source publishes. The destination registry serves
	// that manifest by digest, as the remote repository does, so the
	// read-back through it succeeds.
	basePin string
	// resolveCalls counts the resolve-and-verify calls, and resolveErr is
	// what they fail with when set (the "no signed -pinned image yet" and
	// "the signature did not verify" cases).
	resolveCalls int
	resolveErr   error
	// arCalls counts the read-backs through the remote repository.
	arCalls int
}

// hostRewrite sends every request to the test server, whatever host the
// caller named: the read-back asks for <region>-docker.pkg.dev and must
// reach the fake.
type hostRewrite struct{ to string }

func (h hostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	u.Scheme, u.Host = "http", strings.TrimPrefix(h.to, "http://")
	rq := req.Clone(req.Context())
	rq.URL = &u
	return http.DefaultTransport.RoundTrip(rq)
}

func newImagesRig(t *testing.T, version string) *imagesRig { return newImagesRigFor(t, version, fpID) }

func newImagesRigFor(t *testing.T, version, fp string) *imagesRig {
	t.Helper()
	r := &imagesRig{fbRig: newFBRigFor(t, fp), src: newImagesRegistry(t), dst: newImagesRegistry(t)}
	t.Chdir(t.TempDir()) // outside any checkout: no fugaro.yaml names a base
	for _, n := range []string{"fugaro-history", "fugaro-base"} {
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
	// The remote (pull-through) repository serves what its upstream has,
	// looked up by digest: the read-back finds the base image's manifest
	// whatever path it asks for.
	r.basePin = r.src.tags["dimipaun/fugaro-base:1.2.3"]
	r.dst.mans[r.basePin], r.dst.types[r.basePin] = r.src.mans[r.basePin], r.src.types[r.basePin]
	// The nightly-immune "-pinned" tag a release publishes beside the plain
	// version tag, naming the same image.
	r.src.tags["dimipaun/fugaro-base:1.2.3-pinned"] = r.basePin

	oldResolve, oldAR := resolvePinnedBase, newARClient
	resolvePinnedBase = func(ctx context.Context, source, kind, version string) (string, error) {
		r.resolveCalls++
		if r.resolveErr != nil {
			return "", r.resolveErr
		}
		// The fake models the real step's shape: read the "-pinned" tag,
		// never the plain version tag a nightly rebuild moves.
		d := r.src.tags["dimipaun/fugaro-"+kind+":"+version+"-pinned"]
		if d == "" {
			return "", fmt.Errorf("%s/fugaro-%s:%s-pinned does not exist: this release has no signed pinned image yet", source, kind, version)
		}
		return d, nil
	}
	newARClient = func(context.Context, *mirror.Mirror) (*http.Client, error) {
		r.arCalls++
		return &http.Client{Transport: hostRewrite{r.dst.srv.URL}}, nil
	}
	t.Cleanup(func() { Version, newMirror, resolvePinnedBase, newARClient = oldV, oldNew, oldResolve, oldAR })
	return r
}

// pinnedBase is what the rig's fake pin records for kind at this release:
// the remote repository's path, the default source's owner, the version as
// a tag, and the digest the signature was verified for.
func (r *imagesRig) pinnedBase(kind, ver string) string {
	return "us-east5-docker.pkg.dev/" + initProject + "/" + infra.GhcrRegistry + "/dimipaun/fugaro-" + kind + ":" + ver + "@" + r.basePin
}

// A development build copies nothing and says what to do, and init
// --firebase behaves as it did: the history job waits for its image.
func TestDevBuildHasNoSilentFallback(t *testing.T) {
	r := newImagesRig(t, "dev")
	newMirror = func(context.Context, *localcfg.Config, []string) (*mirror.Mirror, error) {
		t.Fatal("a development build tried to copy an image")
		return nil, nil
	}
	out, _, err := executeStdin(t, "us-east5\n", "init", "--firebase", fpID, "--yes")
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
	out, _, err := executeStdin(t, "us-east5\n", "init", "--firebase", fpID, "--yes", "--json")
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
	if out, _, err := executeStdin(t, "us-east5\n", "init", "--firebase", fpID, "--yes"); err != nil || r.dst.puts != puts {
		t.Fatalf("rerun: %v, %d writes\n%s", err, r.dst.puts-puts, out)
	}
}

func TestBaseImagesRecordedInConfig(t *testing.T) {
	r := newImagesRig(t, "v1.2.3")
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	bi := r.localConfig(t).BaseImages
	want := "us-east5-docker.pkg.dev/" + initProject + "/fugaro-base/"
	if bi["base"] != want+"fugaro-base:1.2.3" || len(bi) != 1 {
		t.Errorf("base_images = %v", bi)
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] == "" {
		t.Errorf("fugaro-base not copied: %v", r.dst.tags)
	}
	if r.dst.tags[initProject+"/fugaro-base/history:latest"] != "" {
		t.Error("the history image is copied without --firebase")
	}
	// Idempotent: nothing is sent again.
	puts := r.dst.puts
	out, _, err = executeStdin(t, "", "init", "--yes", "--base", "base")
	if err != nil || r.dst.puts != puts || !strings.Contains(out, "nothing to copy") {
		t.Fatalf("rerun: %v, %d writes\n%s", err, r.dst.puts-puts, out)
	}
}

func TestBaseKindIsCopiedAsFugaroBase(t *testing.T) {
	r := newImagesRig(t, "v1.2.3")
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := "us-east5-docker.pkg.dev/" + initProject + "/fugaro-base/fugaro-base:1.2.3"
	if bi := r.localConfig(t).BaseImages; bi["base"] != want || len(bi) != 1 {
		t.Errorf("base_images = %v, want base: %s", bi, want)
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] == "" {
		t.Errorf("not copied: %v", r.dst.tags)
	}
}

// Today's installations have no fugaro-base image published yet (the base
// image's own release and publish pipeline is a later task): fugaro init on
// a checkout whose workflow has no base: (the implicit Fugaro base) must
// refuse with a clear error naming the missing image and saying it has no
// published image, write NOTHING to the local config, and copy nothing to
// the project's registry.
//
// Mutation (run, restore): publish "fugaro-base" on the fake source
// registry (letting the copy proceed, as it would once the base image is
// actually published), and this test's assertions fail: init succeeds,
// writes base_images.base, and a manifest is copied to the registry.
func TestInitRefusesWhenFugaroBaseIsNotPublished(t *testing.T) {
	r := newImagesRig(t, "v1.2.3")
	delete(r.src.tags, "dimipaun/fugaro-base:1.2.3")
	testutil.Git(t, ".", "init", "--quiet")
	if err := os.WriteFile("fugaro.yaml", []byte("version: 1\nproject: "+initProjectName+"\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: make, test: make test } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "init", "--yes")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(err.Error(), "fugaro-base") || !strings.Contains(err.Error(), "no published image") {
		t.Fatalf("err = %v, want it to name the missing image and say it has no published image", err)
	}
	if bi := r.localConfig(t).BaseImages; len(bi) != 0 {
		t.Errorf("base_images was written: %v", bi)
	}
	if r.dst.puts != 0 {
		t.Errorf("the registry was written to: %d manifest/blob writes", r.dst.puts)
	}
}

// A checkout whose workflow has no base: names the base kind.
func TestCheckoutWithoutBaseAsksForTheBaseKind(t *testing.T) {
	r := newImagesRig(t, "v1.2.3")
	testutil.Git(t, ".", "init", "--quiet")
	if err := os.WriteFile("fugaro.yaml", []byte("version: 1\nproject: "+initProjectName+"\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: make, test: make test } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "init", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.localConfig(t).BaseImages["base"] == "" {
		t.Errorf("the checkout's base kind was not copied: %v\n%s", r.localConfig(t).BaseImages, out)
	}
}

// A kind the local config points at its own image (a dev tag, a hand-pushed
// image) is left alone, and --base-image wins over --base in the same run.
func TestPointedBaseImageIsNotMirrored(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	r.appendConfig(t, "base_images: { base: \"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc\" }\n")
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "base", "--base-image", "base=ghcr.io/me/fugaro-base:mine")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.dst.puts != 0 || r.built != 0 {
		t.Errorf("something was mirrored for a pointed kind: %d writes\n%s", r.dst.puts, out)
	}
	bi := r.localConfig(t).BaseImages
	if bi["base"] != "ghcr.io/me/fugaro-base:mine" {
		t.Errorf("base_images = %v, want --base-image to win over the existing pointed value", bi)
	}
	if !strings.Contains(out, "base base: --base-image points it") {
		t.Errorf("no %q in\n%s", "base base: --base-image points it", out)
	}
}

// A mirrored release image of an older CLI is the project's own to move on.
func TestMirroredBaseMovesWithTheCLI(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	r.appendConfig(t, "base_images: { base: \"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:1.0.0\" }\n")
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "base"); err != nil {
		t.Fatal(err)
	}
	if got := r.localConfig(t).BaseImages["base"]; !strings.HasSuffix(got, "fugaro-base:1.2.3") {
		t.Errorf("base_images.base = %s", got)
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
	out, _, err := executeStdin(t, "", "init", "--base", "base")
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
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "base", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if res.state("images") != "changed" || r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] == "" {
		t.Fatalf("images %s, tags %v", res.state("images"), r.dst.tags)
	}
	if !strings.HasSuffix(r.localConfig(t).BaseImages["base"], "fugaro-base:1.2.3") {
		t.Errorf("base_images = %v", r.localConfig(t).BaseImages)
	}
}

// A failed copy records nothing: base_images never names a tag that does
// not exist.
func TestFailedCopyRecordsNothing(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	delete(r.src.tags, "dimipaun/fugaro-base:1.2.3")
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "base"); err == nil {
		t.Fatal("a missing source succeeded")
	}
	if got := r.localConfig(t).BaseImages["base"]; got != "" {
		t.Errorf("base_images.go = %s", got)
	}
}

func TestReleaseTagIsNotMovedWithoutReplaceImage(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	other := cdg([]byte("hand pushed"))
	r.dst.mans[other], r.dst.types[other] = []byte("hand pushed"), "application/vnd.oci.image.manifest.v1+json"
	r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] = other
	_, _, err := executeStdin(t, "", "init", "--yes", "--base", "base")
	if err == nil || !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), "--replace-image") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("err = %v", err)
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] != other || r.dst.puts != 0 {
		t.Fatal("the release tag moved")
	}
	// The flag names the kind; the project's name is typed to confirm it.
	if _, _, err := executeStdin(t, initProjectName+"\n", "init", "--yes", "--base", "base", "--replace-image", "base"); err != nil || r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] == other {
		t.Fatalf("with --replace-image go: %v", err)
	}
}

// A hand-pushed history:latest (the live installs have one) is not replaced
// by --yes alone; --replace-image does it, visibly, naming both digests.
func TestHistoryLatestNeedsReplaceImage(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	other := cdg([]byte("hand pushed history"))
	r.dst.mans[other], r.dst.types[other] = []byte("x"), "application/vnd.oci.image.manifest.v1+json"
	r.dst.tags[initProject+"/fugaro-base/history:latest"] = other
	_, _, err := executeStdin(t, "us-east5\n", "init", "--firebase", fpID, "--yes")
	if err == nil || !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), "--replace-image") {
		t.Fatalf("err = %v", err)
	}
	if r.dst.tags[initProject+"/fugaro-base/history:latest"] != other || r.dst.puts != 0 {
		t.Fatal("history:latest was replaced")
	}
	// The database exists by now (the run above made it): only the replacement is typed.
	out, _, err := executeStdin(t, initProjectName+"\n", "init", "--firebase", fpID, "--yes", "--replace-image", "history")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "REPLACES "+other+" with sha256:") || r.dst.tags[initProject+"/fugaro-base/history:latest"] == other {
		t.Fatalf("not replaced visibly:\n%s", out)
	}
}

// --replace-image names the intent only: a run that cannot take a typed
// confirmation (--yes, --non-interactive, --json, a pipe, a coding agent)
// leaves the replacement to the user with the one-line typed route, and moves
// nothing. The real rule is in force here.
func TestReplaceImageIsNeverConfirmedByYes(t *testing.T) {
	for name, tc := range map[string]struct {
		args  []string
		env   string
		stdin string
	}{
		"--yes":                    {[]string{"--yes"}, "", initProjectName + "\n"},
		"--yes --non-interactive":  {[]string{"--yes", "--non-interactive"}, "", ""},
		"--yes --json":             {[]string{"--yes", "--json"}, "", initProjectName + "\n"},
		"a pipe, no flags":         {nil, "", initProjectName + "\n"},
		"a coding agent, with yes": {[]string{"--yes"}, "CLAUDECODE", initProjectName + "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newImagesRig(t, "1.2.3")
			r.stateBucket()
			r.script["plan"] = map[string]any{"exit": 0}
			r.save(t)
			if _, _, err := executeStdin(t, "", "init", "--yes"); err != nil { // the installation is converged
				t.Fatal(err)
			}
			canConfirmTyped = initflow.CanConfirm // the real rule
			other := cdg([]byte("hand pushed"))
			r.dst.mans[other], r.dst.types[other] = []byte("x"), "application/vnd.oci.image.manifest.v1+json"
			r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] = other
			if tc.env != "" {
				t.Setenv(tc.env, "1")
			}
			out, errOut, err := executeStdin(t, tc.stdin, append([]string{"init", "--base", "base", "--replace-image", "base"}, tc.args...)...)
			out += errOut // --json sends the account to stderr
			if r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] != other || r.dst.puts != 0 {
				t.Fatalf("the tag moved: %v\n%s", err, out)
			}
			if err == nil && !strings.Contains(out, "needs-you") && !strings.Contains(out, "needs you") {
				t.Errorf("exit 0 without replacing, and not left for the user:\n%s", out)
			}
			if !strings.Contains(out, "REPLACES "+other) && tc.env == "" {
				t.Errorf("the plan does not say what is replaced:\n%s", out)
			}
		})
	}
	// At a terminal, with the name typed, it is replaced.
	r := newImagesRig(t, "1.2.3")
	r.stateBucket()
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	if _, _, err := executeStdin(t, "", "init", "--yes"); err != nil {
		t.Fatal(err)
	}
	canConfirmTyped = initflow.CanConfirm
	fakeTerminal(t)
	other := cdg([]byte("hand pushed"))
	r.dst.mans[other], r.dst.types[other] = []byte("x"), "application/vnd.oci.image.manifest.v1+json"
	r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] = other
	// the replacement's confirmation and the copy's
	if out, _, err := executeStdin(t, names(2), "init", "--base", "base", "--replace-image", "base"); err != nil || r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] == other {
		t.Fatalf("typed at a terminal: %v\n%s", err, out)
	}
}

// A pin that matches nothing is an error, and the confirmation says which
// images are not pinned.
func TestExpectDigestUnusedAndUnpinnedListed(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	good := r.src.tags["dimipaun/fugaro-base:1.2.3"]
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "base", "--expect-digest", "history="+good); err == nil || !strings.Contains(err.Error(), "matches no image") {
		t.Fatalf("err = %v", err)
	}
	if r.dst.puts != 0 {
		t.Fatal("copied with an unused pin")
	}
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "base")
	if err != nil || !strings.Contains(out, "NOT pinned with --expect-digest: base") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestLaterKindTagMovedRecordsNothing: one init run still copies more than
// one image (here, history and the base kind, since --firebase pulls in
// history too), so the all-or-nothing rule survives the cut even with one
// base kind: if the base's own release tag moves away after the first
// image (history) is already written, nothing is recorded at all, not a
// partial base_images.
func TestLaterKindTagMovedRecordsNothing(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	oldHook := r.dst.onTag
	moved := false
	r.dst.onTag = func(repo, tag string) {
		if !moved && repo == initProject+"/fugaro-base/history" && tag == "latest" {
			moved = true
			r.src.tags["dimipaun/fugaro-base:1.2.3"] = cdg([]byte("moved"))
			r.src.mans[cdg([]byte("moved"))] = []byte("moved")
			r.src.types[cdg([]byte("moved"))] = "application/vnd.oci.image.index.v1+json"
		}
		if oldHook != nil {
			oldHook(repo, tag)
		}
	}
	_, _, err := executeStdin(t, "us-east5\n", "init", "--firebase", fpID, "--yes", "--base", "base")
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
	r.dst.onBlob = func() { r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] = other }
	_, _, err := executeStdin(t, initProjectName+"\n", "init", "--yes", "--base", "base", "--replace-image", "base")
	if err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("err = %v", err)
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] != other || len(r.localConfig(t).BaseImages) != 0 {
		t.Fatal("overwritten or recorded")
	}
}

func TestExpectDigestPinsTheRelease(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	good := r.src.tags["dimipaun/fugaro-base:1.2.3"]
	bad := cdg([]byte("not the release"))
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "base", "--expect-digest", "base="+bad); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("a wrong pin: %v", err)
	}
	if r.dst.puts != 0 {
		t.Fatal("copied against a wrong pin")
	}
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "base", "--expect-digest", "base="+good); err != nil {
		t.Fatalf("the right pin: %v", err)
	}
	for _, v := range []string{"base=sha256:abc", "nope=" + good, "base"} {
		if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "base", "--expect-digest", v); err == nil {
			t.Errorf("--expect-digest %q accepted", v)
		}
	}
}

// An older CLI never downgrades a newer mirrored base image.
func TestOlderCLIDoesNotDowngradeBase(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	newer := "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:2.0.0"
	r.appendConfig(t, "base_images: { base: \""+newer+"\" }\n")
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "base")
	if err != nil || r.dst.puts != 0 || r.localConfig(t).BaseImages["base"] != newer || !strings.Contains(out, "newer than this CLI's") {
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
		if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "base", "--image-source", bad); err == nil {
			t.Errorf("--image-source %q accepted", bad)
		}
	}
	r.src.publish("acme/fugaro-base", "1.2.3")
	if _, _, err := executeStdin(t, "", "init", "--yes", "--base", "base", "--image-source", "ghcr.io/acme"); err != nil {
		t.Fatal(err)
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] == "" {
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

// --replace-image for a kind this run does not copy is an error, like an
// unused --expect-digest: an intent that is not used would read as done.
func TestReplaceImageUnusedKindIsAnError(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	_, _, err := executeStdin(t, "", "init", "--yes", "--base", "base", "--replace-image", "history")
	if err == nil || !strings.Contains(err.Error(), "--replace-image history matches no image this run copies") || ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err = %v", ExitCode(err), err)
	}
	if r.dst.puts != 0 {
		t.Fatal("copied with an unused --replace-image")
	}
}

// init --base republishes the shared config after recording the base
// images, like the other init paths, and not under --plan-only.
func TestInitBaseRepublishesSharedConfig(t *testing.T) {
	newImagesRig(t, "v1.2.3")
	dir := sharedRuns(t)
	open := sharedBucketOpener
	publishes := 0
	sharedBucketOpener = func(ctx context.Context, u string) (*blobx.Bucket, error) { publishes++; return open(ctx, u) }
	if _, _, err := executeStdin(t, "", "init", "--yes", "--plan-only", "--base", "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sharedObjectPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("--plan-only published: %v", err)
	}
	if publishes != 0 {
		t.Fatalf("--plan-only published %d times", publishes)
	}
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// One publish from the installation stage's config write (before the
	// images exist) and exactly one from the images stage's record of them:
	// the base-image write adds one republish, not a second.
	if publishes != 2 {
		t.Errorf("init --base published %d times, want 2 (installation stage, then images stage)", publishes)
	}
	data, err := os.ReadFile(sharedObjectPath(dir))
	if err != nil {
		t.Fatalf("not published: %v\n%s", err, out)
	}
	if lc, err := localcfg.Parse(data); err != nil || !strings.HasSuffix(lc.BaseImages["base"], "fugaro-base:1.2.3") {
		t.Errorf("published %+v, %v:\n%s", lc, err, data)
	}
}

// fugaro image refresh copies only its workflows' kinds, even in a checkout
// whose fugaro.yaml names others.
func TestImagesStageOnlyKinds(t *testing.T) {
	t.Chdir(repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", "")))
	e, _ := stageEngine(t, "", &initOptions{baseKinds: []string{"java-services"}})
	s := newImagesStage(e)
	if got := strings.Join(s.kinds(t.Context()), ","); got != "base,java-services" {
		t.Fatalf("kinds %s, want --base's and the checkout's", got)
	}
	s.only = []string{"base"}
	if got := strings.Join(s.kinds(t.Context()), ","); got != "base" {
		t.Fatalf("only: kinds %s", got)
	}
}

// only is nil (default kinds) versus empty (no base kinds), and is
// de-duplicated and limited to known kinds.
func TestImagesStageOnlyEmptyAndDuplicates(t *testing.T) {
	t.Chdir(repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", "")))
	e, _ := stageEngine(t, "", &initOptions{baseKinds: []string{"java-services"}})
	s := newImagesStage(e)
	s.only = []string{}
	if got := s.kinds(t.Context()); len(got) != 0 {
		t.Fatalf("empty only: kinds %v, want none", got)
	}
	s.only = []string{"base", "base", "basex"}
	if got := strings.Join(s.kinds(t.Context()), ","); got != "base" {
		t.Fatalf("duplicates and unknown: kinds %s, want base", got)
	}
}
