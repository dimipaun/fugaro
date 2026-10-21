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
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/basepin"
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
	// proxy is the upstream this registry serves a manifest it does not
	// hold from, by digest: what an Artifact Registry remote (pull-through)
	// repository does for ghcr.io (design §3.1). A GET for a digest the
	// proxy lacks is a 404, the shape a missing upstream version has.
	proxy *imagesRegistry
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
		typ := r.types[d]
		if !ok && r.proxy != nil { // a remote repository pulls from its upstream on demand
			b, typ = r.proxy.mans[d], r.proxy.types[d]
			_, ok = r.proxy.mans[d]
		}
		if !ok {
			http.NotFound(w, q)
			return
		}
		w.Header().Set("Content-Type", typ)
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
	// The destination registry is the remote (pull-through) repository:
	// it serves what its upstream has, looked up by digest, whatever path
	// the read-back asks for.
	r.dst.proxy = r.src
	r.basePin = r.src.tags["dimipaun/fugaro-base:1.2.3"]
	// The nightly-immune "-pinned" tag a release publishes beside the plain
	// version tag, naming the same image.
	r.src.tags["dimipaun/fugaro-base:1.2.3-pinned"] = r.basePin

	oldResolve, oldAR := resolvePinnedBase, newARClient
	resolvePinnedBase = func(ctx context.Context, source, kind, version string) (string, error) {
		r.resolveCalls++
		if r.resolveErr != nil {
			return "", r.resolveErr
		}
		// The fake models the real step's shape: read the "-pinned" tag of
		// the allowed source's own owner, never the plain version tag a
		// nightly rebuild moves.
		_, owner, _ := strings.Cut(source, "/")
		d := r.src.tags[owner+"/fugaro-"+kind+":"+version+"-pinned"]
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
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes")
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
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes", "--json")
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
	if out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes"); err != nil || r.dst.puts != puts {
		t.Fatalf("rerun: %v, %d writes\n%s", err, r.dst.puts-puts, out)
	}
}

func TestBaseImagesRecordedInConfig(t *testing.T) {
	r := newImagesRig(t, "v1.2.3")
	out, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	bi := r.localConfig(t).BaseImages
	if bi["base"] != r.pinnedBase("base", "1.2.3") || len(bi) != 1 {
		t.Errorf("base_images = %v", bi)
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] != "" {
		t.Errorf("fugaro-base was copied: %v", r.dst.tags)
	}
	if r.dst.tags[initProject+"/fugaro-base/history:latest"] != "" {
		t.Error("the history image is copied without --firebase")
	}
	// Idempotent: the entry is already this release's pin, so the rerun
	// resolves nothing and writes nothing.
	resolves := r.resolveCalls
	out, _, err = executeStdin(t, "", "setup", "--yes", "--base", "base")
	if err != nil || r.resolveCalls != resolves || !strings.Contains(out, "already has") {
		t.Fatalf("rerun: %v, %d resolve(s)\n%s", err, r.resolveCalls-resolves, out)
	}
}

// TestInitBasePinsTheRemoteRepositoryAtTheVerifiedDigest is the whole
// design in one assertion: after --base base, the local config names the
// remote-repository path (not a copy in fugaro-base), at the digest the
// signature was verified for, with the release tag still present as the
// version marker; and nothing was pushed anywhere.
func TestInitBasePinsTheRemoteRepositoryAtTheVerifiedDigest(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	out, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := r.pinnedBase("base", "1.2.3")
	if bi := r.localConfig(t).BaseImages; bi["base"] != want || len(bi) != 1 {
		t.Errorf("base_images = %v, want base: %s", bi, want)
	}
	if r.dst.puts != 0 {
		t.Errorf("the base kind must not push anything: %d write(s)", r.dst.puts)
	}
	if r.resolveCalls != 1 || r.arCalls != 1 {
		t.Errorf("the pin was recorded without one resolve and one read-back (%d, %d)", r.resolveCalls, r.arCalls)
	}
	if !strings.Contains(out, "sha256:") || !strings.Contains(out, "nothing is copied") {
		t.Errorf("the confirmation does not show the digest or say nothing is copied:\n%s", out)
	}
}

// A release with no signed "-pinned" tag published yet (#318's release
// side, a later task): fugaro init on a checkout whose workflow has no
// base: (the implicit Fugaro base) must refuse with a clear error naming
// the missing image, write NOTHING to the local config, and pin nothing.
//
// Mutation (run, restore): publish the "-pinned" tag on the fake source
// registry (letting the resolve-and-verify step succeed, as it would once
// the release publishes one), and this test's assertions fail: init
// succeeds, writes base_images.base, and records a pin.
func TestInitRefusesWhenFugaroBaseIsNotPublished(t *testing.T) {
	r := newImagesRig(t, "v1.2.3")
	delete(r.src.tags, "dimipaun/fugaro-base:1.2.3-pinned")
	testutil.Git(t, ".", "init", "--quiet")
	if err := os.WriteFile("fugaro.yaml", []byte("version: 1\nproject: "+initProjectName+"\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: make, test: make test } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "setup", "--yes")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(err.Error(), "fugaro-base") || !strings.Contains(err.Error(), "no signed pinned image") {
		t.Fatalf("err = %v, want it to name the missing image and say the release has no signed pinned one", err)
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
	out, _, err := executeStdin(t, "", "setup", "--yes")
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
	out, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base", "--base-image", "base=ghcr.io/me/fugaro-base:mine")
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

// The copy an earlier CLI made is the project's own to move on: the same
// version in the old shape is stale (B3), and it is re-pinned through the
// remote repository with nothing copied.
func TestMirroredBaseMovesWithTheCLI(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	r.appendConfig(t, "base_images: { base: \"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:1.2.3\" }\n")
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base"); err != nil {
		t.Fatal(err)
	}
	if got, want := r.localConfig(t).BaseImages["base"], r.pinnedBase("base", "1.2.3"); got != want {
		t.Errorf("base_images.base = %s, want %s", got, want)
	}
	if r.dst.puts != 0 {
		t.Errorf("%d writes: a base kind is pinned, never copied", r.dst.puts)
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
	if _, _, err := executeStdin(t, "", "setup", "--yes"); err != nil { // writes the config
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "setup", "--base", "base")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "needs a real terminal: "+initflow.NoTerminalAdvice) {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(out, "pin "+r.pinnedBase("base", "1.2.3")) || r.dst.puts != 0 {
		t.Fatalf("%d writes\n%s", r.dst.puts, out)
	}
}

// All blobs are at the destination but the tag is not (a run that died at
// the manifest): the manifest is pushed, the stage reports the change, and
// the tag exists only afterwards. A base kind copies nothing any more, so
// the copy path this covers is history's.
func TestManifestOnlyCopyIsAChange(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	for d, b := range r.src.blobs {
		r.dst.blobs[d] = b
	}
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if res.state("images") != "changed" || r.dst.tags[initProject+"/fugaro-base/history:latest"] == "" {
		t.Fatalf("images %s, tags %v", res.state("images"), r.dst.tags)
	}
}

// A failed resolve records nothing: base_images never names a digest that
// was not verified.
func TestFailedResolveRecordsNothing(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	delete(r.src.tags, "dimipaun/fugaro-base:1.2.3-pinned")
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base"); err == nil {
		t.Fatal("a missing source succeeded")
	}
	if got := r.localConfig(t).BaseImages["base"]; got != "" {
		t.Errorf("base_images.base = %s", got)
	}
}

// A hand-pushed history:latest (the live installs have one) is not replaced
// by --yes alone; --replace-image does it, visibly, naming both digests.
func TestHistoryLatestNeedsReplaceImage(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	other := cdg([]byte("hand pushed history"))
	r.dst.mans[other], r.dst.types[other] = []byte("x"), "application/vnd.oci.image.manifest.v1+json"
	r.dst.tags[initProject+"/fugaro-base/history:latest"] = other
	_, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes")
	if err == nil || !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), "--replace-image") {
		t.Fatalf("err = %v", err)
	}
	if r.dst.tags[initProject+"/fugaro-base/history:latest"] != other || r.dst.puts != 0 {
		t.Fatal("history:latest was replaced")
	}
	// The database exists by now (the run above made it): only the replacement is typed.
	out, _, err := executeStdin(t, initProjectName+"\n", "setup", "--firebase", fpID, "--yes", "--replace-image", "history")
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
//
// The kind it drives is history: a base kind is not copied anywhere any
// more, so --replace-image names nothing for one (TestReplaceImageRefusesBase
// is the refusal, and the base kind's own "never silently replaced" case is
// gone with the copy).
func TestReplaceImageIsNeverConfirmedByYes(t *testing.T) {
	const history = initProject + "/fugaro-base/history:latest"
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
			// The installation and the Firebase backend are converged first,
			// so a subtest without --yes reaches the images stage instead of
			// stopping at the backend's own typed confirmation.
			if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes"); err != nil {
				t.Fatal(err)
			}
			puts := r.dst.puts                    // the converged run's own copy, not this one's
			canConfirmTyped = initflow.CanConfirm // the real rule
			other := cdg([]byte("hand pushed"))
			r.dst.mans[other], r.dst.types[other] = []byte("x"), "application/vnd.oci.image.manifest.v1+json"
			r.dst.tags[history] = other
			if tc.env != "" {
				t.Setenv(tc.env, "1")
			}
			out, errOut, err := executeStdin(t, tc.stdin, append([]string{"setup", "--firebase", fpID, "--replace-image", "history"}, tc.args...)...)
			out += errOut // --json sends the account to stderr
			if r.dst.tags[history] != other || r.dst.puts != puts {
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
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	canConfirmTyped = initflow.CanConfirm
	fakeTerminal(t)
	other := cdg([]byte("hand pushed"))
	r.dst.mans[other], r.dst.types[other] = []byte("x"), "application/vnd.oci.image.manifest.v1+json"
	r.dst.tags[history] = other
	// the replacement's confirmation and the copy's
	if out, _, err := executeStdin(t, names(2), "setup", "--firebase", fpID, "--replace-image", "history"); err != nil || r.dst.tags[history] == other {
		t.Fatalf("typed at a terminal: %v\n%s", err, out)
	}
}

// A base kind is not copied anywhere any more, so there is nothing to
// replace: --replace-image base is a user error that names the removal and
// the way to keep your own image instead (design §5.1), and it is refused
// before anything is read or written.
func TestReplaceImageRefusesBaseNamesTheRemoval(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	out, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base", "--replace-image", "base")
	if ExitCode(err) != ExitUserError ||
		!strings.Contains(err.Error(), "a base kind is not copied anywhere any more, so there is nothing to replace") ||
		!strings.Contains(err.Error(), "--base-image base=IMAGE") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if r.dst.puts != 0 || len(r.localConfig(t).BaseImages) != 0 {
		t.Fatalf("wrote something: %d write(s), base_images %v", r.dst.puts, r.localConfig(t).BaseImages)
	}
}

// A pin that matches nothing is an error, and the confirmation never lists
// a base kind as unpinned: after the pin is signature-verified and read
// back (design §3.2), --expect-digest is no longer the only way a kind can
// be pinned.
func TestExpectDigestUnusedAndUnpinnedListed(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	good := r.src.tags["dimipaun/fugaro-base:1.2.3"]
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base", "--expect-digest", "history="+good); err == nil || !strings.Contains(err.Error(), "matches no image") {
		t.Fatalf("err = %v", err)
	}
	if r.dst.puts != 0 {
		t.Fatal("copied with an unused pin")
	}
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The copy is the only image that can be unpinned; the base kind was
	// verified and read back, so it is never in this list.
	list := ""
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, "NOT pinned with --expect-digest: "); i >= 0 {
			// The list is the tail of the copy half of the confirmation,
			// which the pin half follows after " — and it ".
			list, _, _ = strings.Cut(l[i+len("NOT pinned with --expect-digest: "):], " — and it")
		}
	}
	if list != "history" {
		t.Errorf("the unpinned list is %q, want just the copy (history):\n%s", list, out)
	}
}

// TestInitBaseRefusesWhenTheRemoteRepositoryCannotServeTheDigest: a failed
// read-back writes nothing — no half-migrated config — and copies nothing,
// even when the same run has a history image to copy. The read-back is what
// proves the path the jobs pull by works, so a run that cannot complete it
// must not record a pin.
func TestInitBaseRefusesWhenTheRemoteRepositoryCannotServeTheDigest(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	delete(r.src.mans, r.basePin) // the upstream cannot serve the digest the tag names
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes", "--base", "base")
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "cannot serve") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if bi := r.localConfig(t).BaseImages; len(bi) != 0 {
		t.Errorf("base_images = %v, want nothing recorded", bi)
	}
	if r.dst.puts != 0 {
		t.Errorf("something was copied before the read-back was checked: %d write(s)", r.dst.puts)
	}
}

// The destination tag changing after the plan is refused at the stage too.
// The copy path this covers is history's: a base kind copies nothing.
func TestDestinationMovedAtStageLevel(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	other := cdg([]byte("someone else"))
	r.dst.mans[other], r.dst.types[other] = []byte("x"), "application/vnd.oci.image.manifest.v1+json"
	r.dst.onBlob = func() { r.dst.tags[initProject+"/fugaro-base/history:latest"] = other }
	_, _, err := executeStdin(t, "us-east5\n"+names(2), "setup", "--yes", "--firebase", fpID, "--replace-image", "history")
	if err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("err = %v", err)
	}
	if r.dst.tags[initProject+"/fugaro-base/history:latest"] != other || len(r.localConfig(t).BaseImages) != 0 {
		t.Fatal("overwritten or recorded")
	}
}

func TestExpectDigestPinsTheRelease(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	good := r.src.tags["dimipaun/fugaro-base:1.2.3"]
	bad := cdg([]byte("not the release"))
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base", "--expect-digest", "base="+bad); err == nil || !strings.Contains(err.Error(), "never written against a value that disagrees") {
		t.Fatalf("a wrong pin: %v", err)
	}
	if r.dst.puts != 0 {
		t.Fatal("copied against a wrong pin")
	}
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base", "--expect-digest", "base="+good); err != nil {
		t.Fatalf("the right pin: %v", err)
	}
	for _, v := range []string{"base=sha256:abc", "nope=" + good, "base"} {
		if _, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base", "--expect-digest", v); err == nil {
			t.Errorf("--expect-digest %q accepted", v)
		}
	}
}

// An older CLI never downgrades a newer managed base image, in either
// shape: the copy an earlier CLI made, or a pin.
func TestOlderCLIDoesNotDowngradeBase(t *testing.T) {
	for _, newer := range []string{
		"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:2.0.0",
		"us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-base:2.0.0@sha256:" + strings.Repeat("a", 64),
	} {
		r := newImagesRig(t, "1.2.3")
		// The pin's shape is only recognised with a recorded image_source:
		// the owner it is checked against comes from there (design §3.6),
		// never from the reference.
		r.appendConfig(t, "image_source: ghcr.io/dimipaun\nbase_images: { base: \""+newer+"\" }\n")
		out, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base")
		if err != nil || r.dst.puts != 0 || r.localConfig(t).BaseImages["base"] != newer || !strings.Contains(out, "already has") {
			t.Fatalf("%s: %v, %d writes, base_images %v\n%s", newer, err, r.dst.puts, r.localConfig(t).BaseImages, out)
		}
	}
}

func TestPlanOnlyCopiesNothing(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	r.stateBucket()
	if _, _, err := executeStdin(t, "", "setup", "--firebase", fpID, "--plan-only"); err != nil {
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
		if _, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base", "--image-source", bad); err == nil {
			t.Errorf("--image-source %q accepted", bad)
		}
	}
	// A fork's own release, pinned from its own "-pinned" tag and read back
	// through the remote repository, which proxies all of ghcr.io.
	r.src.publish("acme/fugaro-base", "1.2.3")
	r.src.tags["acme/fugaro-base:1.2.3-pinned"] = r.src.tags["acme/fugaro-base:1.2.3"]
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base", "--image-source", "ghcr.io/acme"); err != nil {
		t.Fatal(err)
	}
	got := r.localConfig(t).BaseImages["base"]
	if want := "us-east5-docker.pkg.dev/" + initProject + "/fugaro-ghcr/acme/fugaro-base:1.2.3@" + r.src.tags["acme/fugaro-base:1.2.3"]; got != want {
		t.Errorf("base_images.base = %q, want %q", got, want)
	}
	if r.localConfig(t).ImageSource != "ghcr.io/acme" {
		t.Errorf("image_source = %q, want the fork's own source", r.localConfig(t).ImageSource)
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
	_, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base", "--replace-image", "history")
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
	if _, _, err := executeStdin(t, "", "setup", "--yes", "--plan-only", "--base", "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sharedObjectPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("--plan-only published: %v", err)
	}
	if publishes != 0 {
		t.Fatalf("--plan-only published %d times", publishes)
	}
	out, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base")
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
	if lc, err := localcfg.Parse(data); err != nil || !strings.Contains(lc.BaseImages["base"], "/fugaro-ghcr/dimipaun/fugaro-base:1.2.3@sha256:") || lc.ImageSource != "ghcr.io/dimipaun" {
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

// The base pin is read through the remote repository, whose upstream is
// https://ghcr.io (design §3.1), so a source on any other host could never
// be served. It is refused where the pin is formed — the flag is not
// refused on its own, because the history image is a direct pull and keeps
// any registry ValidSourcePrefix allows.
func TestRefuseANonGhcrSourceForABaseKind(t *testing.T) {
	// On the flag.
	r := newImagesRig(t, "1.2.3")
	out, _, err := executeStdin(t, "", "setup", "--yes", "--base", "base", "--image-source", "quay.io/acme")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "whose upstream is https://ghcr.io") ||
		!strings.Contains(err.Error(), "--base-image base=IMAGE") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if r.dst.puts != 0 {
		t.Fatal("copied something")
	}
	// And recorded: the same refusal, with no flag anywhere, which is the
	// run a teammate's machine does.
	r = newImagesRig(t, "1.2.3")
	r.appendConfig(t, "image_source: quay.io/acme\n")
	_, _, err = executeStdin(t, "", "setup", "--yes", "--base", "base")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "whose upstream is https://ghcr.io") {
		t.Fatalf("recorded: exit %d, err %v", ExitCode(err), err)
	}
	// The refusal is the base kind's: the history image is a direct pull, so
	// a source on another host is not refused here, it is read (and fails
	// only because the rig's registry has no such image).
	r = newImagesRig(t, "1.2.3")
	_, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes", "--image-source", "quay.io/acme")
	if err != nil && strings.Contains(err.Error(), "whose upstream is https://ghcr.io") {
		t.Fatalf("the history copy refused a foreign source: %v", err)
	}
}

// A base kind is never copied, and the two halves of the confirmation say
// different things about trust, so they are asserted apart: the base half
// names the verified signature and says nothing is copied, the history half
// keeps the copy sentence and its "not a signature" caveat (design §3.2).
func TestImagesStageConfirmationSaysTheBaseIsNotCopied(t *testing.T) {
	newImagesRig(t, "1.2.3")
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"pins 1 base image(s) to this release's image at the digest its cosign signature was verified for",
		"nothing is copied, and nothing is pushed into " + mirror.BaseRepository,
		"copies 1 image(s)",
		"not a signature",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the confirmation does not say %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{
		// The base kind is pinned, never listed among the copies.
		"copies 2 image(s)",
		// And it is not an unpinned, trust-the-tag image.
		"NOT pinned with --expect-digest: base",
	} {
		if strings.Contains(out, bad) {
			t.Errorf("the confirmation still says %q:\n%s", bad, out)
		}
	}
	// With no history image to copy, there is no copy sentence at all: a
	// base-only run claims no copy anywhere.
	newImagesRig(t, "1.2.3")
	out, _, err = executeStdin(t, "us-east5\n", "setup", "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "pins 1 base image(s) to this release's image") {
		t.Errorf("the base-only confirmation does not name the pin:\n%s", out)
	}
	for _, bad := range []string{"copies 0 image(s)", "not a signature"} {
		if strings.Contains(out, bad) {
			t.Errorf("a base-only run still says %q:\n%s", bad, out)
		}
	}
}

// The confirmation names the verifier that actually ran: which binary
// checked the signature is part of what the pin rests on, and the path is
// resolved once (basepin.CosignPath). With cosign absent the clause is
// simply not there — read() has already refused, so a confirmation is
// never reached without a verifier, and the confirmation must not claim
// one it did not use.
func TestImagesConfirmationNamesTheVerifierThatRan(t *testing.T) {
	old := basepin.CosignBin
	t.Cleanup(func() { basepin.CosignBin = old })
	bin := filepath.Join(t.TempDir(), "cosign")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	basepin.CosignBin = bin
	newImagesRig(t, "1.2.3")
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "(verified by "+bin+")") {
		t.Errorf("the confirmation does not name the verifier %s:\n%s", bin, out)
	}

	basepin.CosignBin = filepath.Join(t.TempDir(), "not-here")
	newImagesRig(t, "1.2.3")
	out, _, _ = executeStdin(t, "us-east5\n", "setup", "--yes", "--base", "base")
	if strings.Contains(out, "verified by") {
		t.Errorf("the confirmation claims a verifier with none on PATH:\n%s", out)
	}
}

// The stage's own outcome line is past tense, and each of its two clauses
// appears only when the run did that thing: a base kind is pinned and never
// copied, and it never reaches the Copy loop, so a base-only run (and a
// rerun whose history tag is already the release image) must report the pin
// alone rather than leading with a copy that did not happen.
func TestImagesOutcomeSaysOnlyWhatItDid(t *testing.T) {
	const (
		copied = "images copied and verified by digest"
		pinned = "base images pinned by digest through the remote repository"
	)
	// Base and history together: both clauses.
	newImagesRig(t, "1.2.3")
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if want := copied + "; " + pinned; !strings.Contains(out, want) {
		t.Errorf("a base+history run does not report %q:\n%s", want, out)
	}
	// Base alone: the pin clause, and no copy claim anywhere.
	newImagesRig(t, "1.2.3")
	out, _, err = executeStdin(t, "us-east5\n", "setup", "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, pinned) {
		t.Errorf("a base-only run does not report %q:\n%s", pinned, out)
	}
	if strings.Contains(out, copied) {
		t.Errorf("a base-only run still claims a copy:\n%s", out)
	}
}

// The preview line under the review screen lists what is unpinned, and a
// base kind is never one of them: it is pinned to a digest whose signature
// was verified, whether or not --expect-digest repeated the value.
func TestImagesPreviewDoesNotListBaseAsUnpinned(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{baseKinds: []string{"base"}, firebase: fpID})
	e.spec.History = &infra.HistorySpec{}
	line := newImagesStage(e).sourceLine(context.Background())
	if !strings.Contains(line, "signature-verified") || !strings.Contains(line, "base") {
		t.Fatalf("the preview does not say the base image is signature-verified: %q", line)
	}
	if _, rest, ok := strings.Cut(line, "NOT pinned with --expect-digest: "); ok && strings.Contains(rest, "base") {
		t.Errorf("the unpinned list names the base kind: %q", rest)
	}
	// With a history copy in the run too, the unpinned list is the copy's
	// alone.
	if !strings.Contains(line, "NOT pinned with --expect-digest: history") {
		t.Errorf("the preview does not list the history copy as unpinned: %q", line)
	}

	// A base-only run copies nothing, so the preview must not claim a copy
	// either — the same rule applyDetail states for the outcome line
	// (TestImagesOutcomeSaysOnlyWhatItDid). Without this case the clause was
	// reached with an empty unpinned list and printed "every image copied is
	// pinned with --expect-digest" over a run that copies nothing.
	//
	// Mutation (run, restore): drop the `len(copied) > 0` guard in
	// sourceLine and this fails, with "--expect-digest" in a base-only line.
	e = rigEngine(t, r, &initOptions{baseKinds: []string{"base"}})
	e.spec.History = nil
	line = newImagesStage(e).sourceLine(context.Background())
	if !strings.Contains(line, "signature-verified") || !strings.Contains(line, "base") {
		t.Fatalf("the base-only preview does not say the base image is signature-verified: %q", line)
	}
	if strings.Contains(line, "copied") || strings.Contains(line, "--expect-digest") {
		t.Errorf("a base-only run's preview claims a copy: %q", line)
	}
}

// No user-facing text offers a command that no longer works for a base
// kind: --replace-image refuses one (nothing is copied, so there is nothing
// to replace), and --expect-digest is a cross-check of the verified digest,
// never the way a base kind is trusted.
func TestNoMessageOffersARemovedBaseCommand(t *testing.T) {
	var all []string
	for _, args := range [][]string{{"setup", "--help"}, {"image", "refresh", "--help"}, {"upgrade", "--help"}} {
		out, _, _ := executeStdin(t, "", args...)
		all = append(all, out)
	}
	newImagesRig(t, "1.2.3")
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	all = append(all, out)
	for _, text := range all {
		for _, bad := range []string{"--replace-image base", "--expect-digest base", "history or base"} {
			if strings.Contains(text, bad) {
				t.Errorf("a message still offers %q:\n%s", bad, text)
			}
		}
	}
	// The refusal for the flag itself names the working alternative.
	newImagesRig(t, "1.2.3")
	out, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase", fpID, "--yes", "--replace-image", "base")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "--base-image base=IMAGE") || !strings.Contains(out, "there is nothing to replace") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
}

// TestNewARClientRefusesARedirectOffTheRegistryHost: the read-back through
// the remote repository runs on the operator's own Application Default
// Credentials (their reader grant), and those credentials stay on the
// registry's host. Artifact Registry answers a request for another
// location with a redirect, so the check is what keeps a registry-supplied
// URL from carrying the bearer token somewhere else.
//
// Mutation (run, restore): make CheckRedirect return nil — the client
// follows the redirect, the request succeeds, and this test fails on the
// first assertion.
func TestNewARClientRefusesARedirectOffTheRegistryHost(t *testing.T) {
	var elsewhere []string
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere = append(elsewhere, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	var atRegistry []string
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atRegistry = append(atRegistry, r.Header.Get("Authorization"))
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer reg.Close()
	c, err := newARClient(context.WithValue(context.Background(), oauth2.HTTPClient, reg.Client()), &mirror.Mirror{Token: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "user-token"})})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, reg.URL+"/v2/proj/fugaro-ghcr/manifests/1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("the redirect to another host was followed: %d", resp.StatusCode)
	}
	// Both servers are https, so the scheme check cannot be what refuses
	// this: the HOST check is, and its own sentence is the proof (a test that
	// only looked for the other host's URL passed with the host check gone,
	// because the scheme check names it too).
	if !strings.Contains(err.Error(), "registry's own host") {
		t.Errorf("err = %v, want the host check's refusal (the credential stays on the registry's own host)", err)
	}
	if len(atRegistry) != 1 || atRegistry[0] != "Bearer user-token" {
		t.Fatalf("the registry saw %v, want the credential (the test is vacuous otherwise)", atRegistry)
	}
	if len(elsewhere) != 0 {
		t.Errorf("the other host received %d request(s), the first with %q", len(elsewhere), elsewhere[0])
	}
}

// sentRequest is one request a client would actually put on the wire.
type sentRequest struct{ url, auth string }

// recordingTransport records what the client under test sends before
// handing the request to the transport that really dials. It is how the
// test below sees a hop that never happens: the http target of the
// redirect has no server to answer it, so only what the client tried to
// send can say whether the credential would have left.
type recordingTransport struct {
	base http.RoundTripper
	sent []sentRequest
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.sent = append(t.sent, sentRequest{url: req.URL.String(), auth: req.Header.Get("Authorization")})
	return t.base.RoundTrip(req)
}

// TestNewARClientRefusesAnHTTPSDowngradeOnTheSameHost: the host check alone
// is not enough. Artifact Registry answers a request with a redirect, and a
// redirect that stays on the registry's host but drops https would send the
// Bearer token in cleartext — the same credential, over a connection anyone
// on the path can read (and rewrite the answer of). So every redirect
// target must be https, and the client must not even dial the http hop.
//
// The test server is TLS, and the redirect points back at its own
// host:port with scheme http, so only the scheme separates the two: the
// host check cannot be what refuses this. The recording transport is what
// makes "nothing reached the http hop" observable, and the first hop's own
// recorded Authorization header keeps the test from being vacuous.
//
// Mutation (run, restore): drop the scheme check from CheckRedirect — the
// client sends the plain-http request (the recorded list grows) and the
// refusal this test wants is gone.
func TestNewARClientRefusesAnHTTPSDowngradeOnTheSameHost(t *testing.T) {
	var atRegistry []string
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atRegistry = append(atRegistry, r.Header.Get("Authorization"))
		http.Redirect(w, r, "http://"+r.Host+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer reg.Close()

	c, err := newARClient(context.Background(), &mirror.Mirror{Token: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "user-token"})})
	if err != nil {
		t.Fatal(err)
	}
	// Only the two things the test environment forces are replaced: the
	// transport trusts the test server's certificate, and the recording
	// wrapper sits under the token source so it sees the header. The client
	// and its CheckRedirect are newARClient's own.
	rec := &recordingTransport{base: reg.Client().Transport}
	c.Transport = &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "user-token"}), Base: rec}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, reg.URL+"/v2/proj/fugaro-ghcr/manifests/1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("the https-to-http redirect was followed: %d", resp.StatusCode)
	}
	if !strings.Contains(err.Error(), "http://"+req.URL.Host+req.URL.Path) || !strings.Contains(err.Error(), "cleartext") {
		t.Errorf("err = %v, want a refusal naming the http target it would not follow", err)
	}
	if len(atRegistry) != 1 || atRegistry[0] != "Bearer user-token" {
		t.Fatalf("the registry saw %v, want the credential (the test is vacuous otherwise)", atRegistry)
	}
	if len(rec.sent) != 1 {
		t.Fatalf("the client sent %d request(s) %v, want only the https one: the http hop must never be dialed", len(rec.sent), rec.sent)
	}
	if rec.sent[0].auth != "Bearer user-token" {
		t.Fatalf("the https hop carried %q, want the credential (the test is vacuous otherwise)", rec.sent[0].auth)
	}
}
