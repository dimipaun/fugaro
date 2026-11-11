package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
)

// This file is AK10, folded into MC6: the bundle write refuses a document the
// credentials service that reads it cannot read. The provider key
// (config.ProviderKeySecret, AK6b) is an entry the loader knows and an older
// service does not, and credsvc.ParseBundle refuses the WHOLE document for an
// unknown key rather than the entry, so a bundle written with it by a CLI
// newer than the deployed service halts every caller of that service, coding
// runs included, until `fugaro update` moves the service. `fugaro creds set`
// is the bundle's only writer, so the guard is at that write.

// runServiceSkew is this file's Cloud Run services fake: the two reads the
// guard makes — the service, whose traffic and latestReadyRevision say which
// revision serves the bundle, and that revision, whose container image carries
// the release. It is this file's own rather than an arm of
// gcpfake.RunServices because that fake speaks the policy read alone and fails
// a test on a service get, deliberately (runservices_test.go), and its service
// is a name with no body; the reads are this task's own and so is their fake.
type runServiceSkew struct {
	t  *testing.T
	mu sync.Mutex

	url string

	// The service's own answer. exist false is Cloud Run's 404 — no service
	// deployed. serviceErr answers every read with a refused credential
	// instead, which is the read the guard may not skip.
	exist      bool
	serviceErr bool
	// template is what the NEXT revision would run, which is where a roll-out
	// that never became ready leaves the new image: the guard reads the
	// revision that SERVES, and this field is here to be ignored.
	template string
	latest   string
	traffic  []skewTraffic
	// images is each revision's container image, by revision short name.
	images map[string]string

	paths []string
}

// skewTraffic is one of the service's traffic targets.
type skewTraffic struct {
	kind    string
	percent int
	rev     string
}

func newRunServiceSkew(t *testing.T) *runServiceSkew {
	t.Helper()
	f := &runServiceSkew{t: t, images: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

// deployedService is a service whose traffic all goes to one revision: the
// template names the image a next revision would run, and rev-serving is the
// revision that serves the bundle.
func (f *runServiceSkew) deployedService(template, serving string) {
	f.exist, f.template = true, template
	f.latest = f.revisionName("rev-serving")
	f.traffic = []skewTraffic{{kind: "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST", percent: 100}}
	f.images["rev-serving"] = serving
}

func (f *runServiceSkew) revisionName(short string) string {
	return "projects/proj-1234/locations/us-east5/services/" + infra.CredentialsServiceName + "/revisions/" + short
}

func (f *runServiceSkew) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func (f *runServiceSkew) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.URL.Path)
	// /v2/projects/<p>/locations/<l>/services/<name>[/revisions/<rev>]
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v2/"), "/")
	switch {
	case f.serviceErr:
		skewError(w, http.StatusForbidden, "PERMISSION_DENIED", "caller does not have permission")
	case len(parts) == 6 && parts[5] == infra.CredentialsServiceName:
		if !f.exist {
			skewError(w, http.StatusNotFound, "NOT_FOUND", "Service ["+infra.CredentialsServiceName+"] not found.")
			return
		}
		traffic := make([]any, 0, len(f.traffic))
		for _, tr := range f.traffic {
			traffic = append(traffic, map[string]any{"type": tr.kind, "percent": tr.percent, "revision": tr.rev})
		}
		skewJSON(w, http.StatusOK, map[string]any{
			"name":                "projects/proj-1234/locations/us-east5/services/" + infra.CredentialsServiceName,
			"latestReadyRevision": f.latest,
			"template":            map[string]any{"containers": []any{map[string]any{"image": f.template}}},
			"traffic":             traffic,
		})
	case len(parts) == 8 && parts[5] == infra.CredentialsServiceName && parts[6] == "revisions":
		short := parts[7]
		image, ok := f.images[short]
		if !ok {
			skewError(w, http.StatusNotFound, "NOT_FOUND", "Revision ["+short+"] not found.")
			return
		}
		skewJSON(w, http.StatusOK, map[string]any{
			"name":       f.revisionName(short),
			"containers": []any{map[string]any{"image": image}},
		})
	default:
		f.t.Errorf("the Cloud Run services fake was asked for a path it does not serve: %s", r.URL.Path)
		skewError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unhandled path")
	}
}

func skewJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func skewError(w http.ResponseWriter, code int, status, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"error":{"code":%d,"message":%q,"status":%q}}`, code, msg, status)
}

// credentialsImage is a service image reference as the installation module
// writes it: the pinned `:X.Y.Z@sha256:` form. A rel of "" is the reference
// an operator hand-wrote instead — a digest with no release in it.
func credentialsImage(rel string) string {
	image := "us-east5-docker.pkg.dev/proj-1234/" + infra.GhcrRegistry + "/dimipaun/fugaro-credentials"
	if rel != "" {
		image += ":" + rel
	}
	return image + "@sha256:" + strings.Repeat("ab", 32)
}

// runEndpointRE is the config's one Cloud Run endpoint, which the fixture
// repoints at this file's services fake. The value stops at the flow mapping's
// comma (newCloudFixture writes the endpoints as one), so the entries after it
// keep their own.
var runEndpointRE = regexp.MustCompile(`\brun: [^,}]+`)

// credsSkewFixture is credsFixtureFor with the Cloud Run services fake as the
// config's Cloud Run endpoint, and the installation's own pin written to state
// when rel is not "none". The endpoint is REPLACED rather than added —
// newCloudFixture writes the config's endpoints as one flow mapping, and a
// document with two `run:` keys is not a config — and the bundle commands
// start no job, so nothing else reads it.
func credsSkewFixture(t *testing.T, pin string, skew *runServiceSkew) (*credsFixture, string) {
	t.Helper()
	f := credsFixtureFor(t)
	path := os.Getenv("FUGARO_CONFIG")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := runEndpointRE.ReplaceAll(b, []byte("run: "+skew.url+"/"))
	if bytes.Equal(cfg, b) {
		t.Fatalf("the fixture's config names no Cloud Run endpoint to replace:\n%s", b)
	}
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	// The state directory the installation's own record lives in: a fresh one
	// is "no pin at all", which is the installation that has no workdir here.
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if pin != "none" {
		writeInstallationServiceImage(t, state, "proj-1234", pin)
	}
	return f, state
}

// providerValueDoc is the value `creds set provider-key` reads: the mapping of
// the credential and the two routing facts the entry holds (credsvc's
// providerValue).
func providerValueDoc(t *testing.T) string {
	t.Helper()
	return "key: " + credsValue(t, "provider") + "\nbase_url: https://api.example.com/v1\nauth: bearer\n"
}

// TestCredsSetRefusesAProviderBundleAnOlderServiceCannotRead is the red step:
// the write of a document carrying the provider entry against a deployed
// service older than the constant is refused, nothing is written, and the
// sentence names `fugaro update`.
//
// Its fixture is the service whose TEMPLATE names a newer image while the
// revision that SERVES is the old one — a roll-out that failed to become
// ready, which is the state a template-reading guard calls new. Cloud Run
// keeps serving the previous healthy revision there, so the document's only
// reader is the old one, and the entry written into it halts every caller of
// that service.
func TestCredsSetRefusesAProviderBundleAnOlderServiceCannotRead(t *testing.T) {
	skew := newRunServiceSkew(t)
	f, _ := credsSkewFixture(t, "none", skew)
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
	before := f.value(t)
	skew.deployedService(credentialsImage("0.24.0"), credentialsImage("0.18.0"))

	value := providerValueDoc(t)
	out, _, err := executeStdin(t, value, "creds", "set", config.ProviderKeySecret)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("creds set %s against a 0.18.0 service = %v, want a user error", config.ProviderKeySecret, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "fugaro update") {
		t.Errorf("the refusal names no way out: %v", err)
	}
	// The release it judged is the one that SERVES: the template's own image
	// is the newer one above, and a guard reading it would have written.
	if !strings.Contains(msg, "0.18.0") {
		t.Errorf("the refusal does not name the release of the revision that serves the bundle: %v", err)
	}
	if strings.Contains(msg, "0.24.0") {
		t.Errorf("the refusal judged the template's image rather than the revision that serves: %v", err)
	}
	if strings.Contains(out, value) || strings.Contains(msg, credsValue(t, "provider")) {
		t.Errorf("the value was echoed: stdout %q, err %q", out, msg)
	}
	if n := f.writes(); n != 0 {
		t.Fatalf("%d version(s) written, want none", n)
	}
	if got := f.value(t); got != before {
		t.Errorf("the document changed: %q", got)
	}
	// The service, and the revision that serves it, and nothing else.
	asked := skew.asked()
	if len(asked) != 2 || !strings.HasSuffix(asked[0], "/services/"+infra.CredentialsServiceName) || !strings.Contains(asked[1], "/revisions/") {
		t.Errorf("the guard read %q, want the service and the revision that serves it", asked)
	}
}

// TestCredsSetRefusesWhenNoRecordSaysWhichServiceServes: neither half of the
// guard may be skipped on an unknown, which is where it deliberately parts
// from checkReviewAdmission's warn-and-admit. A Cloud Run read that fails (a
// refused credential, an unavailable API) and a record whose reference carries
// no release (a hand-written digest) are each a refusal that names what could
// not be read, and nothing is written.
func TestCredsSetRefusesWhenNoRecordSaysWhichServiceServes(t *testing.T) {
	value := providerValueDoc(t)
	refuses := func(t *testing.T, skew *runServiceSkew, pin string) error {
		t.Helper()
		f, _ := credsSkewFixture(t, pin, skew)
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		before := f.value(t)
		out, _, err := executeStdin(t, value, "creds", "set", config.ProviderKeySecret)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("creds set %s = %v, want a user error", config.ProviderKeySecret, err)
		}
		if !strings.Contains(err.Error(), "fugaro update") {
			t.Errorf("the refusal names no way out: %v", err)
		}
		if strings.Contains(out, value) {
			t.Errorf("the value was echoed: %q", out)
		}
		if n := f.writes(); n != 0 {
			t.Fatalf("%d version(s) written, want none", n)
		}
		if got := f.value(t); got != before {
			t.Errorf("the document changed: %q", got)
		}
		return err
	}

	t.Run("the service cannot be read", func(t *testing.T) {
		skew := newRunServiceSkew(t)
		skew.serviceErr = true
		if err := refuses(t, skew, "none"); !strings.Contains(err.Error(), "the read of the service that serves this bundle failed") {
			t.Errorf("the refusal does not name the read that failed: %v", err)
		}
	})

	t.Run("the serving revision's reference carries no release", func(t *testing.T) {
		skew := newRunServiceSkew(t)
		skew.deployedService(credentialsImage(providerEntrySince), credentialsImage(""))
		if err := refuses(t, skew, "none"); !strings.Contains(err.Error(), "carries no release") {
			t.Errorf("the refusal does not name the reference it could not read: %v", err)
		}
	})

	t.Run("the installation's own pin carries no release", func(t *testing.T) {
		skew := newRunServiceSkew(t) // no service deployed: the pin decides
		if err := refuses(t, skew, ""); !strings.Contains(err.Error(), "carries no release") {
			t.Errorf("the refusal does not name the record it could not read: %v", err)
		}
	})
}

// TestCredsSetJudgesWhatIsDeployedBeforeWhatIsPinned: the two records name two
// different releases, and what is DEPLOYED decides whenever the service
// exists. The installation's pin is the update's desired state — written as
// the stage plans and applied after — so a declined or failed apply leaves it
// ahead of the service that serves the bundle, which is exactly the state a
// guard reading the pin alone (checkReviewAdmission's reading) writes through.
func TestCredsSetJudgesWhatIsDeployedBeforeWhatIsPinned(t *testing.T) {
	value := providerValueDoc(t)

	t.Run("a pin at this release over an older deployed service is refused", func(t *testing.T) {
		skew := newRunServiceSkew(t)
		f, _ := credsSkewFixture(t, providerEntrySince, skew)
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		skew.deployedService(credentialsImage(providerEntrySince), credentialsImage("0.18.0"))
		if _, _, err := executeStdin(t, value, "creds", "set", config.ProviderKeySecret); err == nil {
			t.Fatal("a pin ahead of the service that serves the bundle was written")
		}
		if n := f.writes(); n != 0 {
			t.Fatalf("%d version(s) written, want none", n)
		}
	})

	t.Run("a deployed service at this release beside a stale pin is written", func(t *testing.T) {
		skew := newRunServiceSkew(t)
		f, _ := credsSkewFixture(t, "0.18.0", skew)
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		skew.deployedService(credentialsImage(providerEntrySince), credentialsImage(providerEntrySince))
		if _, _, err := executeStdin(t, value, "creds", "set", config.ProviderKeySecret); err != nil {
			t.Fatalf("a service at this release beside an older pin: %v", err)
		}
		if n := f.writes(); n != 1 {
			t.Fatalf("%d version(s) written, want one", n)
		}
	})

	t.Run("no deployed service with an old pin is refused", func(t *testing.T) {
		skew := newRunServiceSkew(t)
		f, _ := credsSkewFixture(t, "0.18.0", skew)
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		if _, _, err := executeStdin(t, value, "creds", "set", config.ProviderKeySecret); err == nil {
			t.Fatal("a pin whose apply is still coming was written through")
		}
		if n := f.writes(); n != 0 {
			t.Fatalf("%d version(s) written, want none", n)
		}
	})
}

// TestCredsSetWritesWhenTheServiceIsNewer is the write AK10 itself teaches:
// the provider entry's value is a mapping of the credential and the two
// routing facts, and against a service at or past the constant the write
// succeeds — one version, the other entries kept, and a document the service's
// own loader reads back with the entry in it.
func TestCredsSetWritesWhenTheServiceIsNewer(t *testing.T) {
	skew := newRunServiceSkew(t)
	f, _ := credsSkewFixture(t, "none", skew)
	existing := config.ClaudeOAuthToken + ": " + credsValue(t, "existing") + "\n"
	f.seed(t, []byte(existing))
	skew.deployedService(credentialsImage(providerEntrySince), credentialsImage(providerEntrySince))

	value := providerValueDoc(t)
	if _, _, err := executeStdin(t, value, "creds", "set", config.ProviderKeySecret); err != nil {
		t.Fatalf("creds set %s against a current service: %v", config.ProviderKeySecret, err)
	}
	if n := f.writes(); n != 1 {
		t.Fatalf("%d version(s) written, want one", n)
	}
	doc := f.value(t)
	b, err := credsvc.ParseBundle([]byte(doc))
	if err != nil {
		t.Fatalf("the document this command wrote is one the service's loader refuses: %v\n%s", err, doc)
	}
	entry, ok := b.ProviderKey()
	if !ok {
		t.Fatalf("the document holds no provider entry:\n%s", doc)
	}
	if entry.Key != config.ProviderKeySecret || entry.Kind != credsvc.KindProvider {
		t.Errorf("the entry written is %s/%s, want %s/%s", entry.Key, entry.Kind, config.ProviderKeySecret, credsvc.KindProvider)
	}
	if entry.Value() != credsValue(t, "provider") {
		t.Errorf("the entry's value is not the key this run read")
	}
	// The entry's shape is the one the service reads: the mapping's three
	// fields, and the credential is stored rather than the whole mapping.
	for _, want := range []string{"base_url:", "auth:", "https://api.example.com/v1", "bearer"} {
		if !strings.Contains(doc, want) {
			t.Errorf("the written document does not carry %q:\n%s", want, doc)
		}
	}
	if !strings.Contains(doc, config.ClaudeOAuthToken+": "+credsValue(t, "existing")) {
		t.Errorf("the entry this write was not about changed:\n%s", doc)
	}
}

// TestCredsSetRefusesAProviderValueThatIsNotTheThreeFields: the write AK10
// teaches is the read consumer's one kind over, and it is refused the same way
// — the mapping's own rules here, and the two routing facts at the loader this
// command calls before it stores anything, so the shape is not read twice and
// cannot drift from what the service reads.
//
// Every arm is against a service at this release, so the guard is not what
// refuses the write: what is pinned is the value's shape and nothing else.
func TestCredsSetRefusesAProviderValueThatIsNotTheThreeFields(t *testing.T) {
	refuses := func(t *testing.T, value, want string) {
		t.Helper()
		skew := newRunServiceSkew(t)
		f, _ := credsSkewFixture(t, "none", skew)
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		before := f.value(t)
		skew.deployedService(credentialsImage(providerEntrySince), credentialsImage(providerEntrySince))

		out, _, err := executeStdin(t, value, "creds", "set", config.ProviderKeySecret)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("creds set %s with a value that is not the mapping = %v, want a user error", config.ProviderKeySecret, err)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
		// The credential is a byte of the value in every arm that carries one,
		// and no refusal repeats it.
		if strings.Contains(err.Error(), credsValue(t, "provider")) || strings.Contains(out, credsValue(t, "provider")) {
			t.Errorf("the refusal repeated the value's key: stdout %q, err %v", out, err)
		}
		if n := f.writes(); n != 0 {
			t.Fatalf("%d version(s) written, want none", n)
		}
		if got := f.value(t); got != before {
			t.Errorf("the document changed: %q", got)
		}
	}

	t.Run("a single line is not the mapping", func(t *testing.T) {
		refuses(t, credsValue(t, "provider")+"\n", providerShape)
	})

	t.Run("a field that is not one of the three", func(t *testing.T) {
		refuses(t, providerValueDoc(t)+"token: "+credsValue(t, "stray")+"\n", "a field that is not one of the three")
	})

	t.Run("a field is missing", func(t *testing.T) {
		refuses(t, "key: "+credsValue(t, "provider")+"\nbase_url: https://api.example.com/v1\n", providerShape)
	})

	// The two routing facts are the loader's own reading, reached through the
	// ParseBundle call the write makes: a destination the platform's rule
	// refuses and an auth word that is neither of the two are refused here
	// because they are refused there.
	t.Run("a base URL the platform's rule refuses", func(t *testing.T) {
		refuses(t, "key: "+credsValue(t, "provider")+"\nbase_url: http://api.example.com/v1\nauth: bearer\n", config.ProviderKeySecret)
	})

	t.Run("an auth word that is neither of the two", func(t *testing.T) {
		refuses(t, "key: "+credsValue(t, "provider")+"\nbase_url: https://api.example.com/v1\nauth: basic\n", config.ProviderKeySecret)
	})
}

// TestCredsSetIsUnaffectedByAnInstallationWithNoService: an installation with
// no credentials service AND no pin has no such skew — there is no service to
// refuse the document and none coming — and its bundle is written as today.
// This is "no service", which is an answer, and not "unknown", which is the
// test above.
func TestCredsSetIsUnaffectedByAnInstallationWithNoService(t *testing.T) {
	skew := newRunServiceSkew(t) // no service, and no pin: credsSkewFixture's state dir is fresh
	f, _ := credsSkewFixture(t, "none", skew)
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))

	if _, _, err := executeStdin(t, providerValueDoc(t), "creds", "set", config.ProviderKeySecret); err != nil {
		t.Fatalf("creds set %s with no service and no pin: %v", config.ProviderKeySecret, err)
	}
	if n := f.writes(); n != 1 {
		t.Fatalf("%d version(s) written, want one", n)
	}
	if _, err := credsvc.ParseBundle([]byte(f.value(t))); err != nil {
		t.Fatalf("the document written is one the loader refuses: %v", err)
	}
}

// TestProviderEntrySinceHasAReleaseBullet: providerEntrySince names the
// release whose credentials service reads the provider entry, which is not
// tagged when this constant lands, so docs/release.md's Before-you-tag list is
// what holds the constant to the release actually cut — the job every other
// *Since constant's bullet does. A release that first shipped the entry
// earlier would otherwise keep refusing writes its own service reads.
func TestProviderEntrySinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `providerEntrySince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no providerEntrySince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+providerEntrySince+"\"`") {
		t.Errorf("docs/release.md's providerEntrySince bullet does not carry the constant's release (%q)", providerEntrySince)
	}
	for _, want := range []string{config.ProviderKeySecret, "not tagged yet", "fugaro creds set"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's providerEntrySince bullet does not say %q", want)
		}
	}
}
