package mirror

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"
)

const (
	srcHost = "ghcr.io"
	dstHost = "us-east5-docker.pkg.dev"
	srcRepo = "dimipaun/fugaro-go"
	dstRepo = "proj-abcde/fugaro-base/fugaro-go"
	dstTok  = "ya29.USER-OAUTH-TOKEN-SECRET"
	srcTok  = "anon-pull-token"
)

func dg(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

type req struct{ Host, Method, Path, Auth string }

// store is the part of an OCI registry the mirror uses.
type store struct {
	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string][]byte // by digest
	types     map[string]string // digest -> media type
	tags      map[string]string // repo:tag -> digest
	uploads   map[string][]byte
	n         int
}

func newStore() *store {
	return &store{blobs: map[string][]byte{}, manifests: map[string][]byte{}, types: map[string]string{}, tags: map[string]string{}, uploads: map[string][]byte{}}
}

func (s *store) addManifest(raw []byte, mt string) string {
	d := dg(raw)
	s.manifests[d], s.types[d] = raw, mt
	return d
}

// image is a synthetic linux/amd64 image.
type image struct {
	cfg, manifest []byte
	layers        [][]byte
	digest        string
}

func makeImage(arch string, layers ...string) image {
	im := image{}
	cfg, _ := json.Marshal(map[string]string{"os": "linux", "architecture": arch})
	im.cfg = cfg
	type desc struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	}
	m := struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Config        desc   `json:"config"`
		Layers        []desc `json:"layers"`
	}{SchemaVersion: 2, MediaType: mtOCIManifest, Config: desc{"application/vnd.oci.image.config.v1+json", dg(cfg), int64(len(cfg))}}
	for _, l := range layers {
		b := []byte(l)
		im.layers = append(im.layers, b)
		m.Layers = append(m.Layers, desc{"application/vnd.oci.image.layer.v1.tar+gzip", dg(b), int64(len(b))})
	}
	im.manifest, _ = json.Marshal(m)
	im.digest = dg(im.manifest)
	return im
}

func (s *store) put(im image) {
	s.blobs[dg(im.cfg)] = im.cfg
	for _, l := range im.layers {
		s.blobs[dg(l)] = l
	}
	s.addManifest(im.manifest, mtOCIManifest)
}

// publishIndex puts the images under tag as an index with the given
// platforms ("amd64", "arm64"...), the attestation entry a buildx index has.
func (s *store) publishIndex(repo, tag string, imgs map[string]image) (indexDigest string) {
	type ent struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int    `json:"size"`
		Platform  any    `json:"platform"`
	}
	ix := struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Manifests     []ent  `json:"manifests"`
	}{2, mtOCIIndex, nil}
	for arch, im := range imgs {
		s.put(im)
		ix.Manifests = append(ix.Manifests, ent{mtOCIManifest, im.digest, len(im.manifest), map[string]string{"os": "linux", "architecture": arch}})
	}
	ix.Manifests = append(ix.Manifests, ent{mtOCIManifest, dg([]byte("att")), 3, map[string]string{"os": "unknown", "architecture": "unknown"}})
	raw, _ := json.Marshal(ix)
	d := s.addManifest(raw, mtOCIIndex)
	s.tags[repo+":"+tag] = d
	return d
}

// reg is one in-process registry (the source or the destination).
type reg struct {
	t      *testing.T
	st     *store
	srv    *httptest.Server
	mu     sync.Mutex
	log    []req
	bearer string // required Authorization token ("" none); for the source, an anonymous token flow is used when anon
	anon   bool
	// hooks
	tamperBlob     func(digest string, b []byte) []byte
	headerDigest   func(d string) string
	redirectBlobTo string
	lax            bool // finalize an upload without checking its digest
	failPatch      int  // fail the nth PATCH (1-based)
	locationHost   string
	patches        int
	extraHeader    func(w http.ResponseWriter, r *http.Request)
}

func newReg(t *testing.T, st *store) *reg {
	r := &reg{t: t, st: st}
	r.srv = httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *reg) host() string { return strings.TrimPrefix(r.srv.URL, "http://") }

func (r *reg) requests() []req {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]req(nil), r.log...)
}

func (r *reg) count(method, sub string) int {
	n := 0
	for _, q := range r.requests() {
		if q.Method == method && strings.Contains(q.Path, sub) {
			n++
		}
	}
	return n
}

func (r *reg) writes() int {
	n := 0
	for _, q := range r.requests() {
		if q.Method != "GET" && q.Method != "HEAD" {
			n++
		}
	}
	return n
}

func (r *reg) handle(w http.ResponseWriter, q *http.Request) {
	r.mu.Lock()
	r.log = append(r.log, req{q.Host, q.Method, q.URL.Path, q.Header.Get("Authorization")})
	r.mu.Unlock()
	if r.extraHeader != nil {
		r.extraHeader(w, q)
	}
	if q.URL.Path == "/token" {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": srcTok})
		return
	}
	if strings.HasPrefix(q.URL.Path, "/cdn/") {
		r.serveBlob(w, q, strings.TrimPrefix(q.URL.Path, "/cdn/"))
		return
	}
	if r.bearer != "" && q.Header.Get("Authorization") != "Bearer "+r.bearer {
		if r.anon {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="fake"`, r.srv.URL))
		}
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if strings.HasPrefix(q.URL.Path, "/cdn/") {
		d := strings.TrimPrefix(q.URL.Path, "/cdn/")
		r.serveBlob(w, q, d)
		return
	}
	p := strings.TrimPrefix(q.URL.Path, "/v2/")
	switch {
	case strings.Contains(p, "/blobs/uploads/"):
		r.upload(w, q, p)
	case strings.Contains(p, "/blobs/"):
		repo, d, _ := strings.Cut(p, "/blobs/")
		_ = repo
		if r.redirectBlobTo != "" && q.Method == "GET" {
			http.Redirect(w, q, r.redirectBlobTo+"/cdn/"+d+"?sig=SECRET-SIG", http.StatusFound)
			return
		}
		r.serveBlob(w, q, d)
	case strings.Contains(p, "/manifests/"):
		repo, ref, _ := strings.Cut(p, "/manifests/")
		r.manifest(w, q, repo, ref)
	default:
		http.NotFound(w, q)
	}
}

func (r *reg) serveBlob(w http.ResponseWriter, q *http.Request, d string) {
	r.st.mu.Lock()
	b, ok := r.st.blobs[d]
	r.st.mu.Unlock()
	if !ok {
		http.NotFound(w, q)
		return
	}
	if r.tamperBlob != nil {
		b = r.tamperBlob(d, b)
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(b)))
	w.Header().Set("Docker-Content-Digest", d)
	if q.Method == "GET" {
		_, _ = w.Write(b)
	}
}

func (r *reg) manifest(w http.ResponseWriter, q *http.Request, repo, ref string) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	switch q.Method {
	case "PUT":
		body, _ := io.ReadAll(q.Body)
		d := dg(body)
		r.st.manifests[d], r.st.types[d] = body, q.Header.Get("Content-Type")
		r.st.tags[repo+":"+ref] = d
		w.Header().Set("Docker-Content-Digest", d)
		w.WriteHeader(http.StatusCreated)
		return
	}
	d := ref
	if !strings.HasPrefix(ref, "sha256:") {
		var ok bool
		if d, ok = r.st.tags[repo+":"+ref]; !ok {
			http.NotFound(w, q)
			return
		}
	}
	raw, ok := r.st.manifests[d]
	if !ok {
		http.NotFound(w, q)
		return
	}
	hd := d
	if r.headerDigest != nil {
		hd = r.headerDigest(d)
	}
	w.Header().Set("Content-Type", r.st.types[d])
	w.Header().Set("Docker-Content-Digest", hd)
	w.Header().Set("Content-Length", fmt.Sprint(len(raw)))
	if q.Method == "GET" {
		_, _ = w.Write(raw)
	}
}

func (r *reg) upload(w http.ResponseWriter, q *http.Request, p string) {
	repo, id, _ := strings.Cut(p, "/blobs/uploads/")
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	loc := func(id string) string {
		h := ""
		if r.locationHost != "" {
			h = r.locationHost
		}
		return h + "/v2/" + repo + "/blobs/uploads/" + id + "?_state=UPLOAD-STATE-SECRET"
	}
	switch q.Method {
	case "POST":
		r.st.n++
		id := fmt.Sprintf("u%d", r.st.n)
		r.st.uploads[id] = nil
		w.Header().Set("Location", loc(id))
		w.WriteHeader(http.StatusAccepted)
	case "PATCH":
		r.patches++
		if r.failPatch > 0 && r.patches == r.failPatch {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		b, _ := io.ReadAll(q.Body)
		r.st.uploads[id] = append(r.st.uploads[id], b...)
		w.Header().Set("Location", loc(id))
		w.WriteHeader(http.StatusAccepted)
	case "PUT":
		d := q.URL.Query().Get("digest")
		b := r.st.uploads[id]
		if !r.lax && dg(b) != d {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.st.blobs[d] = b
		w.Header().Set("Docker-Content-Digest", d)
		w.WriteHeader(http.StatusCreated)
	}
}

// env is a source and a destination registry and a Mirror wired to them.
type env struct {
	t         *testing.T
	src, dst  *reg
	sst, dst2 *store
	m         *Mirror
	img       image
	srcRef    Ref
	dstRef    Ref
}

func newEnv(t *testing.T, layers ...string) *env {
	t.Helper()
	if len(layers) == 0 {
		layers = []string{"layer-one-bytes", "layer-two-bytes-longer"}
	}
	e := &env{t: t, sst: newStore(), dst2: newStore()}
	e.src, e.dst = newReg(t, e.sst), newReg(t, e.dst2)
	e.src.bearer, e.src.anon = srcTok, true
	e.dst.bearer = dstTok
	e.img = makeImage("amd64", layers...)
	e.sst.publishIndex(srcRepo, "1.2.3", map[string]image{"amd64": e.img, "arm64": makeImage("arm64", "arm-layer")})
	e.m = &Mirror{
		Token: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: dstTok}),
		Endpoint: func(h string) string {
			switch h {
			case srcHost:
				return e.src.srv.URL
			case dstHost:
				return e.dst.srv.URL
			}
			return "http://127.0.0.1:1" // anything else is unreachable
		},
	}
	e.srcRef = Ref{srcHost, srcRepo, "1.2.3"}
	e.dstRef = Ref{dstHost, dstRepo, "1.2.3"}
	return e
}

func hostOf(u string) string { x, _ := url.Parse(u); return x.Host }
