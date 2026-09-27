package gcpfake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"gocloud.dev/blob/gcsblob"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// GCS is a stateful fake of the GCS JSON API subset that gcsblob and
// cloud.google.com/go/storage send for Fugaro's operations: multipart and
// resumable uploads (with ifGenerationMatch), metadata and media reads,
// conditional deletes, customTime patches and prefix listings.
type GCS struct {
	*Server
	mu      sync.Mutex
	gen     int64
	buckets map[string]map[string]*object
	uploads map[string]*upload
	nextID  int
}

type object struct {
	data       []byte
	gen        int64
	metagen    int64
	ct         string
	customTime time.Time
	updated    time.Time
}

// upload is a resumable upload in progress.
type upload struct {
	bucket, name, ct string
	cond             *int64
	data             []byte
}

// NewGCS starts a GCS fake that lives until the test ends.
func NewGCS(t *testing.T) *GCS {
	t.Helper()
	g := &GCS{gen: 1_700_000_000_000_000, buckets: map[string]map[string]*object{}, uploads: map[string]*upload{}}
	g.Server = newServer(t, g.handle)
	return g
}

// Client returns a storage client pointed at the fake. It reads through the
// JSON API, so every call the client makes reaches the handler below.
func (g *GCS) Client(t *testing.T) *storage.Client {
	t.Helper()
	client, err := storage.NewClient(context.Background(), option.WithEndpoint(g.URL+"/storage/v1/"),
		option.WithoutAuthentication(), option.WithHTTPClient(g.Server.Server.Client()), storage.WithJSONReads())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// Bucket returns a gcsblob bucket named name on this fake.
func (g *GCS) Bucket(t *testing.T, name string) *blobx.Bucket {
	t.Helper()
	b, err := gcsblob.OpenBucket(context.Background(), nil, name, &gcsblob.Options{Client: g.Client(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return &blobx.Bucket{Bucket: b, GCSName: name}
}

// HasCustomTime reports whether the object bucket/key has its customTime set.
func (g *GCS) HasCustomTime(bucket, key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	o := g.buckets[bucket][key]
	return o != nil && !o.customTime.IsZero()
}

const (
	objPrefix    = "/storage/v1/b/"
	uploadPrefix = "/upload/storage/v1/b/"
)

func (g *GCS) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	q := r.URL.Query()
	switch {
	case strings.HasPrefix(r.URL.Path, uploadPrefix):
		bucket, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, uploadPrefix), "/")
		if rest != "o" {
			break
		}
		// Chunks go to the session URL; the client sends them as POST, and
		// the API also accepts PUT.
		switch {
		case q.Get("upload_id") != "" && (r.Method == http.MethodPost || r.Method == http.MethodPut):
			g.putResumable(w, r, body)
			return
		case r.Method == http.MethodPost && q.Get("uploadType") == "multipart":
			g.multipart(w, r, bucket, body)
			return
		case r.Method == http.MethodPost && q.Get("uploadType") == "resumable":
			g.startResumable(w, r, bucket, body)
			return
		}
	case strings.HasPrefix(r.URL.Path, objPrefix):
		bucket, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, objPrefix), "/")
		if rest == "o" && r.Method == http.MethodGet {
			g.list(w, bucket, q.Get("prefix"), q.Get("delimiter"))
			return
		}
		name, ok := strings.CutPrefix(rest, "o/")
		if !ok || name == "" {
			break
		}
		switch r.Method {
		case http.MethodGet:
			g.get(w, bucket, name, q.Get("alt") == "media")
			return
		case http.MethodDelete:
			g.delete(w, r, bucket, name)
			return
		case http.MethodPatch:
			g.patch(w, bucket, name, body)
			return
		}
	}
	g.unhandled(w, r)
}

// ifGenerationMatch parses the precondition, nil when absent.
func ifGenerationMatch(r *http.Request) (*int64, error) {
	s := r.URL.Query().Get("ifGenerationMatch")
	if s == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// precondition reports whether cond holds for the current object o (nil if
// missing). A zero generation means "must not exist".
func precondition(cond *int64, o *object) bool {
	switch {
	case cond == nil:
		return true
	case *cond == 0:
		return o == nil
	default:
		return o != nil && o.gen == *cond
	}
}

func (g *GCS) multipart(w http.ResponseWriter, r *http.Request, bucket string, body []byte) {
	cond, err := ifGenerationMatch(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || params["boundary"] == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart upload without a boundary")
		return
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var meta struct{ Name, ContentType string }
	var media []byte
	for i := 0; ; i++ {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		data, _ := io.ReadAll(p)
		switch i {
		case 0:
			if err := json.Unmarshal(data, &meta); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
				return
			}
		case 1:
			media = data
		}
	}
	if meta.Name == "" {
		meta.Name = r.URL.Query().Get("name")
	}
	g.store(w, bucket, meta.Name, meta.ContentType, cond, media)
}

func (g *GCS) startResumable(w http.ResponseWriter, r *http.Request, bucket string, body []byte) {
	cond, err := ifGenerationMatch(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	var meta struct{ Name, ContentType string }
	if len(body) > 0 {
		if err := json.Unmarshal(body, &meta); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
	}
	if meta.Name == "" {
		meta.Name = r.URL.Query().Get("name")
	}
	g.nextID++
	id := strconv.Itoa(g.nextID)
	g.uploads[id] = &upload{bucket: bucket, name: meta.Name, ct: meta.ContentType, cond: cond}
	w.Header().Set("Location", fmt.Sprintf("%s%s%s/o?uploadType=resumable&upload_id=%s", g.URL, uploadPrefix, bucket, id))
	w.WriteHeader(http.StatusOK)
}

// putResumable takes one chunk (POST or PUT to the session URL). "Content-Range: bytes a-b/*" is an
// intermediate chunk; a known total ends the upload.
func (g *GCS) putResumable(w http.ResponseWriter, r *http.Request, body []byte) {
	id := r.URL.Query().Get("upload_id")
	u := g.uploads[id]
	if u == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "no such upload")
		return
	}
	u.data = append(u.data, body...)
	cr := r.Header.Get("Content-Range")
	if strings.HasSuffix(cr, "/*") {
		if len(u.data) > 0 {
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(u.data)-1))
		}
		// "Resume incomplete" is a 308, which the client asks to receive
		// as a 200 with an override header instead (X-GUploader-No-308).
		if r.Header.Get("X-GUploader-No-308") == "yes" {
			w.Header().Set("X-Http-Status-Code-Override", "308")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusPermanentRedirect)
		return
	}
	delete(g.uploads, id)
	g.store(w, u.bucket, u.name, u.ct, u.cond, u.data)
}

func (g *GCS) store(w http.ResponseWriter, bucket, name, ct string, cond *int64, data []byte) {
	if name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "upload without an object name")
		return
	}
	objs := g.buckets[bucket]
	if objs == nil {
		objs = map[string]*object{}
		g.buckets[bucket] = objs
	}
	if !precondition(cond, objs[name]) {
		writeError(w, http.StatusPreconditionFailed, "FAILED_PRECONDITION", "At least one of the pre-conditions you specified did not hold.")
		return
	}
	g.gen++
	o := &object{data: append([]byte(nil), data...), gen: g.gen, metagen: 1, ct: ct, updated: time.Now().UTC()}
	objs[name] = o
	writeJSON(w, http.StatusOK, meta(bucket, name, o))
}

func meta(bucket, name string, o *object) map[string]any {
	m := map[string]any{
		"kind":           "storage#object",
		"bucket":         bucket,
		"name":           name,
		"generation":     strconv.FormatInt(o.gen, 10),
		"metageneration": strconv.FormatInt(o.metagen, 10),
		"size":           strconv.Itoa(len(o.data)),
		"contentType":    o.ct,
		"updated":        o.updated.Format(time.RFC3339Nano),
		"timeCreated":    o.updated.Format(time.RFC3339Nano),
	}
	if !o.customTime.IsZero() {
		m["customTime"] = o.customTime.Format(time.RFC3339Nano)
	}
	return m
}

func (g *GCS) notFound(w http.ResponseWriter, bucket, name string) {
	writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("No such object: %s/%s", bucket, name))
}

func (g *GCS) get(w http.ResponseWriter, bucket, name string, media bool) {
	o := g.buckets[bucket][name]
	if o == nil {
		g.notFound(w, bucket, name)
		return
	}
	if !media {
		writeJSON(w, http.StatusOK, meta(bucket, name, o))
		return
	}
	h := w.Header()
	if o.ct != "" {
		h.Set("Content-Type", o.ct)
	}
	h.Set("Content-Length", strconv.Itoa(len(o.data)))
	h.Set("X-Goog-Generation", strconv.FormatInt(o.gen, 10))
	h.Set("X-Goog-Metageneration", strconv.FormatInt(o.metagen, 10))
	h.Set("X-Goog-Stored-Content-Length", strconv.Itoa(len(o.data)))
	h.Set("Last-Modified", o.updated.Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(o.data)
}

func (g *GCS) delete(w http.ResponseWriter, r *http.Request, bucket, name string) {
	cond, err := ifGenerationMatch(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	o := g.buckets[bucket][name]
	if o == nil {
		g.notFound(w, bucket, name)
		return
	}
	if !precondition(cond, o) {
		writeError(w, http.StatusPreconditionFailed, "FAILED_PRECONDITION", "At least one of the pre-conditions you specified did not hold.")
		return
	}
	delete(g.buckets[bucket], name)
	w.WriteHeader(http.StatusNoContent)
}

func (g *GCS) patch(w http.ResponseWriter, bucket, name string, body []byte) {
	o := g.buckets[bucket][name]
	if o == nil {
		g.notFound(w, bucket, name)
		return
	}
	var req struct {
		CustomTime *time.Time `json:"customTime"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if req.CustomTime != nil {
		o.customTime = req.CustomTime.UTC()
	}
	o.metagen++
	o.updated = time.Now().UTC()
	writeJSON(w, http.StatusOK, meta(bucket, name, o))
}

// list answers a single page: items directly under prefix, and the
// delimiter-collapsed prefixes below it.
func (g *GCS) list(w http.ResponseWriter, bucket, prefix, delim string) {
	objs := g.buckets[bucket]
	names := make([]string, 0, len(objs))
	for n := range objs {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	items := []map[string]any{}
	prefixes := []string{}
	seen := map[string]bool{}
	for _, n := range names {
		if delim != "" {
			if i := strings.Index(n[len(prefix):], delim); i >= 0 {
				p := n[:len(prefix)+i+len(delim)]
				if !seen[p] {
					seen[p] = true
					prefixes = append(prefixes, p)
				}
				continue
			}
		}
		items = append(items, meta(bucket, n, objs[n]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": "storage#objects", "items": items, "prefixes": prefixes})
}
