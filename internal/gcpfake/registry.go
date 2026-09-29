package gcpfake

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Registry is a fake of the OCI distribution API's manifest HEAD, which
// the image check reads digests with: HEAD /v2/<name>/manifests/<ref>
// answers Docker-Content-Digest.
//
// With Bearer set, every manifest request must carry that OAuth token, as
// Artifact Registry wants one. With Anonymous set, a request without the
// fake's pull token gets a 401 whose Bearer challenge names the fake's
// token endpoint (GET /token?service=…&scope=repository:<name>:pull), as
// ghcr.io answers for a public image.
type Registry struct {
	*Server

	Bearer    string
	Anonymous bool

	mu        sync.Mutex
	manifests map[string]string // <name>:<ref> → digest
	accepts   []string
	tokens    int
}

// anonymousToken is the pull token the fake's token endpoint hands out.
const anonymousToken = "anonymous-pull-token"

var manifestPathRE = regexp.MustCompile(`^/v2/(.+)/manifests/([^/]+)$`)

// NewRegistry starts a registry fake that lives until the test ends.
func NewRegistry(t *testing.T) *Registry {
	t.Helper()
	f := &Registry{manifests: map[string]string{}}
	f.Server = newServer(t, f.handle)
	return f
}

// SetManifest makes name:ref (a tag) resolve to digest.
func (f *Registry) SetManifest(name, ref, digest string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.manifests[name+":"+ref] = digest
}

// DeleteManifest removes name:ref.
func (f *Registry) DeleteManifest(name, ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.manifests, name+":"+ref)
}

// Accepts are the Accept headers of the manifest requests so far.
func (f *Registry) Accepts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.accepts)
}

// TokenRequests counts the token endpoint's calls.
func (f *Registry) TokenRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokens
}

func (f *Registry) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch m := manifestPathRE.FindStringSubmatch(r.URL.Path); {
	case r.Method == http.MethodGet && r.URL.Path == "/token":
		f.tokens++
		scope := r.URL.Query().Get("scope")
		if !strings.HasPrefix(scope, "repository:") || !strings.HasSuffix(scope, ":pull") {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "want a pull scope")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": anonymousToken})
	case (r.Method == http.MethodHead || r.Method == http.MethodGet) && m != nil:
		f.accepts = append(f.accepts, r.Header.Get("Accept"))
		auth := r.Header.Get("Authorization")
		switch {
		case f.Bearer != "" && auth != "Bearer "+f.Bearer:
			w.WriteHeader(http.StatusUnauthorized)
			return
		case f.Anonymous && auth != "Bearer "+anonymousToken:
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+f.URL+`/token",service="fake-registry",scope="repository:`+m[1]+`:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		digest, ok := f.manifests[m[1]+":"+m[2]]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		w.WriteHeader(http.StatusOK)
	default:
		f.unhandled(w, r)
	}
}
