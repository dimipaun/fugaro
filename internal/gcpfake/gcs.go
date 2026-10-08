package gcpfake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
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
//
// Only ifGenerationMatch is implemented as a precondition. Any other if*
// parameter (ifGenerationNotMatch, ifMetagenerationMatch,
// ifMetagenerationNotMatch) is refused with a failed test, so a caller that
// starts sending one is noticed instead of passing unchecked.
//
// Buckets can also be created (insert, which gives the new bucket the
// project convenience bindings real GCS gives it), and a bucket's IAM
// policy replaced (setIamPolicy), matched on the etag its getIamPolicy
// served: a stale etag is refused with 412, as GCS does.
//
// Known differences from real GCS, none of which a current caller depends on:
//   - A resumable upload checks its precondition when it is finalized, not
//     when the session starts.
//   - Resumable chunks are appended without checking their Content-Range
//     offsets, so a retried chunk would be stored twice. A status query
//     ("bytes */N" with no body) is treated as the final chunk.
//   - Error bodies carry code, status and message, but no errors[].reason
//     (real GCS answers a failed precondition with reason "conditionNotMet").
//   - Listings honour prefix, delimiter, startOffset, endOffset, maxResults
//     and pageToken, but the page token is simply the next name to list
//     (real GCS's is opaque); matchGlob and every other filter is ignored.
//   - A bucket's get and getIamPolicy answer only for a bucket AddBucket
//     or an insert made; objects can be stored in any bucket without it.
//   - A bucket insert needs the project to have been added (AddProject),
//     and otherwise answers 403.
type GCS struct {
	*Server
	mu      sync.Mutex
	gen     int64
	buckets map[string]map[string]*object
	meta    map[string]*bucketMeta
	uploads map[string]*upload
	nextID  int
	// projects are the project numbers bucket inserts may name, by ID.
	projects map[string]uint64
	// failDeletes makes the next object deletes answer 503.
	failDeletes int
	// listCalls counts object listing requests (one per page).
	listCalls int
}

// bucketMeta is what a bucket's get and getIamPolicy report.
type bucketMeta struct {
	projectNumber uint64
	labels        map[string]string
	policy        []Binding
	// policyGen counts the policy's changes; the etag derives from it.
	policyGen int
	// inserted is the body of the insert that made the bucket, nil for
	// one AddBucket made.
	inserted map[string]any
	// versioning is whether the bucket keeps noncurrent versions.
	versioning bool
}

func (m *bucketMeta) etag() string { return "etag-" + strconv.Itoa(m.policyGen) }

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
	g := &GCS{gen: 1_700_000_000_000_000, buckets: map[string]map[string]*object{}, meta: map[string]*bucketMeta{},
		uploads: map[string]*upload{}, projects: map[string]uint64{}}
	g.Server = newServer(t, g.handle)
	return g
}

// AddBucket makes bucket name exist, in the project numbered
// projectNumber, carrying labels.
func (g *GCS) AddBucket(name string, projectNumber uint64, labels map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.meta[name] = &bucketMeta{projectNumber: projectNumber, labels: maps.Clone(labels), policyGen: 1}
}

// SetVersioning turns bucket name's object versioning on or off, as its
// get reports it.
func (g *GCS) SetVersioning(name string, on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m := g.meta[name]; m != nil {
		m.versioning = on
	}
}

// FailObjectDeletes makes the next n object deletes answer 503, deleting
// nothing.
func (g *GCS) FailObjectDeletes(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failDeletes = n
}

// AddProject lets bucket inserts name project id, whose number new buckets
// then carry.
func (g *GCS) AddProject(id string, number uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.projects[id] = number
}

// ConvenienceBindings are the project convenience bindings GCS gives a new
// bucket of project: owners and editors own it, viewers read it.
func ConvenienceBindings(project string) []Binding {
	return []Binding{
		{Role: "roles/storage.legacyBucketOwner", Members: []string{"projectEditor:" + project, "projectOwner:" + project}},
		{Role: "roles/storage.legacyBucketReader", Members: []string{"projectViewer:" + project}},
		{Role: "roles/storage.legacyObjectOwner", Members: []string{"projectEditor:" + project, "projectOwner:" + project}},
		{Role: "roles/storage.legacyObjectReader", Members: []string{"projectViewer:" + project}},
	}
}

// BucketPolicy is the current IAM policy of bucket name.
func (g *GCS) BucketPolicy(name string) []Binding {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.meta[name]
	if m == nil {
		g.t.Fatalf("gcpfake: BucketPolicy(%q): no such bucket", name)
	}
	return cloneBindings(m.policy)
}

// BucketPolicyEtag is the etag getIamPolicy serves for bucket name now.
func (g *GCS) BucketPolicyEtag(name string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.meta[name]
	if m == nil {
		g.t.Fatalf("gcpfake: BucketPolicyEtag(%q): no such bucket", name)
	}
	return m.etag()
}

// Inserted is the request body of the insert that created bucket name, or
// nil when no insert did.
func (g *GCS) Inserted(name string) map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m := g.meta[name]; m != nil {
		return maps.Clone(m.inserted)
	}
	return nil
}

// SetBucketPolicy replaces the IAM policy of a bucket AddBucket made.
func (g *GCS) SetBucketPolicy(name string, bindings []Binding) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.meta[name]
	if m == nil {
		g.t.Fatalf("gcpfake: SetBucketPolicy(%q): no such bucket", name)
	}
	m.policy = cloneBindings(bindings)
	m.policyGen++
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

// Put stores data as bucket/name directly, without a request: for tests
// that seed many objects at once.
func (g *GCS) Put(bucket, name string, data []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	objs := g.buckets[bucket]
	if objs == nil {
		objs = map[string]*object{}
		g.buckets[bucket] = objs
	}
	g.gen++
	objs[name] = &object{data: append([]byte(nil), data...), gen: g.gen, metagen: 1, updated: time.Now().UTC()}
}

// ListCalls is how many object listing requests (pages) the fake has
// answered.
func (g *GCS) ListCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.listCalls
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
	for k := range q {
		if strings.HasPrefix(k, "if") && k != "ifGenerationMatch" {
			g.unhandled(w, r) // a precondition the fake would silently ignore
			return
		}
	}
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
	case r.URL.Path == "/storage/v1/b" && r.Method == http.MethodPost:
		g.insertBucket(w, q.Get("project"), body)
		return
	case strings.HasPrefix(r.URL.Path, objPrefix):
		bucket, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, objPrefix), "/")
		if (rest == "" || rest == "iam") && r.Method == http.MethodGet {
			g.bucketGet(w, r, bucket, rest == "iam")
			return
		}
		if rest == "iam" && r.Method == http.MethodPut {
			g.setBucketPolicy(w, bucket, body)
			return
		}
		if rest == "o" && r.Method == http.MethodGet {
			g.list(w, bucket, q)
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

// bucketGet answers a bucket's get, or with iam its getIamPolicy.
func (g *GCS) bucketGet(w http.ResponseWriter, r *http.Request, bucket string, iam bool) {
	m := g.meta[bucket]
	if m == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "The specified bucket does not exist.")
		return
	}
	if iam {
		serveBucketPolicy(w, r.URL.Query(), m)
		return
	}
	out := map[string]any{
		"kind": "storage#bucket", "name": bucket, "id": bucket,
		"projectNumber": strconv.FormatUint(m.projectNumber, 10),
	}
	if len(m.labels) > 0 {
		out["labels"] = maps.Clone(m.labels)
	}
	if m.versioning {
		out["versioning"] = map[string]any{"enabled": true}
	}
	writeJSON(w, http.StatusOK, out)
}

// serveBucketPolicy is servePolicy with the bucket's own etag: it refuses
// a policy holding a condition unless version 3 was asked for.
func serveBucketPolicy(w http.ResponseWriter, q url.Values, m *bucketMeta) {
	version := 1
	for _, b := range m.policy {
		if b.Condition != nil {
			version = 3
		}
	}
	requested, _ := strconv.Atoi(q.Get("optionsRequestedPolicyVersion"))
	if version == 3 && requested < 3 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "the policy has conditions; request policy version 3")
		return
	}
	bindings := cloneBindings(m.policy)
	writeJSON(w, http.StatusOK, map[string]any{"kind": "storage#policy", "version": version, "etag": m.etag(), "bindings": bindings})
}

// insertBucket creates a bucket in project, with the convenience bindings
// GCS gives every new bucket.
func (g *GCS) insertBucket(w http.ResponseWriter, project string, body []byte) {
	num, ok := g.projects[project]
	if !ok {
		writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
		return
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	name, _ := req["name"].(string)
	if name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "a bucket needs a name")
		return
	}
	if g.meta[name] != nil {
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", "Your previous request to create the named bucket succeeded and you already own it.")
		return
	}
	labels := map[string]string{}
	if l, ok := req["labels"].(map[string]any); ok {
		for k, v := range l {
			s, _ := v.(string)
			labels[k] = s
		}
	}
	versioning := false
	if v, ok := req["versioning"].(map[string]any); ok {
		versioning, _ = v["enabled"].(bool)
	}
	g.meta[name] = &bucketMeta{projectNumber: num, labels: labels, policy: ConvenienceBindings(project), policyGen: 1, inserted: req, versioning: versioning}
	out := maps.Clone(req)
	out["kind"], out["id"], out["projectNumber"] = "storage#bucket", name, strconv.FormatUint(num, 10)
	writeJSON(w, http.StatusOK, out)
}

// setBucketPolicy replaces a bucket's policy when the request carries the
// etag of the current one, else answers 412 as GCS does.
func (g *GCS) setBucketPolicy(w http.ResponseWriter, bucket string, body []byte) {
	m := g.meta[bucket]
	if m == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "The specified bucket does not exist.")
		return
	}
	var req struct {
		Bindings []Binding `json:"bindings"`
		Etag     string    `json:"etag"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if req.Etag != m.etag() {
		writeError(w, http.StatusPreconditionFailed, "FAILED_PRECONDITION", "At least one of the pre-conditions you specified did not hold.")
		return
	}
	m.policy = cloneBindings(req.Bindings)
	m.policyGen++
	serveBucketPolicy(w, url.Values{"optionsRequestedPolicyVersion": {"3"}}, m)
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
	if g.failDeletes > 0 {
		g.failDeletes--
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "try again")
		return
	}
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

// list answers one page of a listing: items directly under prefix, and
// the delimiter-collapsed prefixes below it, from startOffset (inclusive)
// to endOffset (exclusive), at most maxResults entries (items and
// prefixes together, as GCS counts them) after pageToken.
func (g *GCS) list(w http.ResponseWriter, bucket string, q url.Values) {
	g.listCalls++
	prefix, delim := q.Get("prefix"), q.Get("delimiter")
	from, end := q.Get("startOffset"), q.Get("endOffset")
	if t := q.Get("pageToken"); t > from {
		from = t
	}
	limit := 1000
	if m, err := strconv.Atoi(q.Get("maxResults")); err == nil && m > 0 && m < limit {
		limit = m
	}
	objs := g.buckets[bucket]
	names := make([]string, 0, len(objs))
	for n := range objs {
		if strings.HasPrefix(n, prefix) && n >= from && (end == "" || n < end) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	items := []map[string]any{}
	prefixes := []string{}
	next := ""
	for i := 0; i < len(names); i++ {
		if len(items)+len(prefixes) == limit {
			next = names[i]
			break
		}
		n := names[i]
		if delim != "" {
			if j := strings.Index(n[len(prefix):], delim); j >= 0 {
				p := n[:len(prefix)+j+len(delim)]
				prefixes = append(prefixes, p)
				for i+1 < len(names) && strings.HasPrefix(names[i+1], p) {
					i++
				}
				continue
			}
		}
		items = append(items, meta(bucket, n, objs[n]))
	}
	resp := map[string]any{"kind": "storage#objects", "items": items, "prefixes": prefixes}
	if next != "" {
		resp["nextPageToken"] = next
	}
	writeJSON(w, http.StatusOK, resp)
}
