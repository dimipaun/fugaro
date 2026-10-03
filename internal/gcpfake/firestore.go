package gcpfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/firestore"
)

// Firestore is a fake of the Cloud Firestore v1 REST API the history client
// uses, for one project's (default) database: documents GET/PATCH/DELETE
// (updateMask, currentDocument preconditions), runQuery (a field range, order
// by, limit, startAt cursor) and the databases get/create calls with their
// long-running operation. It is as strict as the real service where that is
// known: typed values only (an integerValue is a decimal string), updateMask
// paths parsed with the backtick grammar, a name that must match the URL,
// 404/409/400 shaped as Google answers. Refuse (from Server) makes every call
// fail; DenyNext makes writes answer 403.
//
// Not modelled: security rules, transactions, indexes (a query that would
// need a composite index is refused 400 FAILED_PRECONDITION), nested
// collections, field transforms.
type Firestore struct {
	*Server

	mu       sync.Mutex
	db       *fsDatabase
	docs     map[string]*fsDoc // "coll/id"
	clock    func() time.Time
	last     time.Time
	denyNext int
	auth     []string
	quota    []string
	ops      map[string]bool // operation name -> polled once already
	raceLoc  string          // see RaceCreate
}

type fsDatabase struct{ location, typ, protection string }

type fsDoc struct {
	fields         map[string]any // typed values
	created, saved time.Time
}

// firestoreLocations is the fake's idea of the locations a native database
// accepts. The real list is Google's; this is an assumption (plan A1).
var firestoreLocations = map[string]bool{
	"nam5": true, "nam7": true, "eur3": true, "eur5": true,
	"us-central1": true, "us-east1": true, "us-east4": true, "us-east5": true, "us-west1": true,
	"europe-west1": true, "europe-west3": true, "asia-northeast1": true,
}

// NewFirestore starts a fake that lives until the test ends, with a native
// database in us-east5 (see RemoveDatabase and SetDatabase).
func NewFirestore(t *testing.T) *Firestore {
	t.Helper()
	f := &Firestore{
		db:   &fsDatabase{location: "us-east5", typ: "FIRESTORE_NATIVE", protection: "DELETE_PROTECTION_ENABLED"},
		docs: map[string]*fsDoc{}, clock: time.Now, ops: map[string]bool{},
	}
	f.Server = newServer(t, f.handle)
	return f
}

// SetDatabase gives the project a database in location.
func (f *Firestore) SetDatabase(location string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.db = &fsDatabase{location: location, typ: "FIRESTORE_NATIVE", protection: "DELETE_PROTECTION_ENABLED"}
}

// SetDatabaseType gives the existing database another type (FIRESTORE_NATIVE
// or DATASTORE_MODE).
func (f *Firestore) SetDatabaseType(typ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.db.typ = typ
}

// RaceCreate makes the next database creation lose a race: the database
// appears in location, and the create answers 409 ALREADY_EXISTS.
func (f *Firestore) RaceCreate(location string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raceLoc = location
}

// RemoveDatabase leaves the project with no database: document calls answer
// 404 "database does not exist", as the service does before one is created.
func (f *Firestore) RemoveDatabase() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.db = nil
}

// Database reports the database's location and delete protection ("" when none).
func (f *Firestore) Database() (location, protection string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.db == nil {
		return "", ""
	}
	return f.db.location, f.db.protection
}

// SetClock sets the time documents are stamped with.
func (f *Firestore) SetClock(now func() time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = now
}

// DenyNext makes the next n write requests (PATCH, DELETE, database create)
// answer 403 PERMISSION_DENIED, changing nothing.
func (f *Firestore) DenyNext(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.denyNext = n
}

// Credentials lists the Authorization header of each request in order.
func (f *Firestore) Credentials() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auth...)
}

// QuotaProjects lists the X-Goog-User-Project header of each request.
func (f *Firestore) QuotaProjects() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.quota...)
}

// Set stores a document as a test's seed (Go values, see firestore.Encode).
func (f *Firestore) Set(coll, id string, fields map[string]any) {
	enc, err := firestore.EncodeFields(fields)
	if err != nil {
		f.t.Fatalf("gcpfake: seeding %s/%s: %v", coll, id, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	d := f.docs[coll+"/"+id]
	if d == nil {
		d = &fsDoc{created: now}
		f.docs[coll+"/"+id] = d
	}
	d.fields, d.saved = enc, now
}

// Value returns a document's decoded fields (nil, false when absent).
func (f *Firestore) Value(coll, id string) (map[string]any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.docs[coll+"/"+id]
	if d == nil {
		return nil, false
	}
	m, err := firestore.DecodeFields(d.fields)
	if err != nil {
		f.t.Fatalf("gcpfake: %v", err)
	}
	return m, true
}

// IDs lists the document IDs of a collection, sorted.
func (f *Firestore) IDs(coll string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for k := range f.docs {
		if c, id, _ := strings.Cut(k, "/"); c == coll {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// now is strictly increasing, with microsecond resolution, so that updateTime
// preconditions can tell two writes apart. Callers hold mu.
func (f *Firestore) now() time.Time {
	t := f.clock().UTC().Truncate(time.Microsecond)
	if !t.After(f.last) {
		t = f.last.Add(time.Microsecond)
	}
	f.last = t
	return t
}

func fsTime(t time.Time) string { return t.Format("2006-01-02T15:04:05.000000Z") }

var fsPath = regexp.MustCompile(`^/v1/projects/([^/]+)/databases/(\(default\))(/.*)?$`)

func (f *Firestore) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	f.mu.Lock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.quota = append(f.quota, r.Header.Get("X-Goog-User-Project"))
	write := r.Method == http.MethodPatch || r.Method == http.MethodDelete || (r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, ":runQuery") && !strings.HasSuffix(r.URL.Path, ":listCollectionIds"))
	if write && f.denyNext > 0 {
		f.denyNext--
		f.mu.Unlock()
		writeError(w, 403, "PERMISSION_DENIED", "Missing or insufficient permissions.")
		return
	}
	f.mu.Unlock()

	// Go escapes the parentheses of (default); the service accepts both forms.
	path := strings.NewReplacer("%28", "(", "%29", ")").Replace(r.URL.EscapedPath())
	if m := regexp.MustCompile(`^/v1/projects/([^/]+)/databases$`).FindStringSubmatch(path); m != nil && r.Method == http.MethodPost {
		f.createDatabase(w, r, m[1], body)
		return
	}
	m := fsPath.FindStringSubmatch(path)
	if m == nil {
		f.unhandled(w, r)
		return
	}
	project, rest := m[1], m[3]
	switch {
	case rest == "" && r.Method == http.MethodGet:
		f.getDatabase(w, project)
	case strings.HasPrefix(rest, "/operations/") && r.Method == http.MethodGet:
		f.getOperation(w, project, rest)
	case rest == "/documents:listCollectionIds" && r.Method == http.MethodPost:
		f.listCollectionIDs(w, project, body)
	case rest == "/documents:runQuery" && r.Method == http.MethodPost:
		f.runQuery(w, project, body)
	case strings.HasPrefix(rest, "/documents/"):
		f.document(w, r, project, strings.TrimPrefix(rest, "/documents/"), body)
	default:
		f.unhandled(w, r)
	}
}

func noDatabase(w http.ResponseWriter, project string) {
	writeError(w, 404, "NOT_FOUND", "The database (default) does not exist for project "+project+". Please visit https://console.cloud.google.com/datastore/setup?project="+project+" to add a Cloud Datastore or Cloud Firestore database.")
}

func (f *Firestore) getDatabase(w http.ResponseWriter, project string) {
	f.mu.Lock()
	db := f.db
	f.mu.Unlock()
	if db == nil {
		writeError(w, 404, "NOT_FOUND", "Database projects/"+project+"/databases/(default) does not exist.")
		return
	}
	writeJSON(w, 200, dbJSON(project, db))
}

func dbJSON(project string, db *fsDatabase) map[string]any {
	return map[string]any{"name": "projects/" + project + "/databases/(default)", "locationId": db.location,
		"type": db.typ, "concurrencyMode": "PESSIMISTIC", "deleteProtectionState": db.protection}
}

func (f *Firestore) createDatabase(w http.ResponseWriter, r *http.Request, project string, body []byte) {
	if r.URL.Query().Get("databaseId") != "(default)" {
		writeError(w, 400, "INVALID_ARGUMENT", "databaseId must be (default) here")
		return
	}
	var req struct {
		LocationID, Type, DeleteProtectionState string
	}
	if json.Unmarshal(body, &req) != nil || req.Type != "FIRESTORE_NATIVE" {
		writeError(w, 400, "INVALID_ARGUMENT", "type must be FIRESTORE_NATIVE")
		return
	}
	if !firestoreLocations[req.LocationID] {
		writeError(w, 400, "INVALID_ARGUMENT", "Invalid location_id: "+req.LocationID)
		return
	}
	if req.DeleteProtectionState != "" && req.DeleteProtectionState != "DELETE_PROTECTION_ENABLED" && req.DeleteProtectionState != "DELETE_PROTECTION_DISABLED" {
		writeError(w, 400, "INVALID_ARGUMENT", "bad deleteProtectionState")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.raceLoc != "" && f.db == nil {
		f.db = &fsDatabase{location: f.raceLoc, typ: "FIRESTORE_NATIVE", protection: "DELETE_PROTECTION_DISABLED"}
		f.raceLoc = ""
	}
	if f.db != nil {
		writeError(w, 409, "ALREADY_EXISTS", "Database already exists. Please use a different database ID.")
		return
	}
	if req.DeleteProtectionState == "" {
		req.DeleteProtectionState = "DELETE_PROTECTION_DISABLED"
	}
	f.db = &fsDatabase{location: req.LocationID, typ: req.Type, protection: req.DeleteProtectionState}
	name := "projects/" + project + "/databases/(default)/operations/create1"
	f.ops[name] = false
	writeJSON(w, 200, map[string]any{"name": name, "done": false,
		"metadata": map[string]any{"@type": "type.googleapis.com/google.firestore.admin.v1.CreateDatabaseMetadata"}})
}

// getOperation: an operation is not done on its creation answer and is done
// from the first poll on (the real one takes seconds to a minute).
func (f *Firestore) getOperation(w http.ResponseWriter, project, rest string) {
	name := "projects/" + project + "/databases/(default)" + rest
	f.mu.Lock()
	_, known := f.ops[name]
	f.ops[name] = true
	db := f.db
	f.mu.Unlock()
	if !known || db == nil {
		writeError(w, 404, "NOT_FOUND", "operation not found")
		return
	}
	writeJSON(w, 200, map[string]any{"name": name, "done": true, "response": dbJSON(project, db)})
}

func (f *Firestore) document(w http.ResponseWriter, r *http.Request, project, rel string, body []byte) {
	segs := strings.Split(rel, "/")
	if len(segs) != 2 {
		writeError(w, 400, "INVALID_ARGUMENT", "the fake serves top-level collection documents only")
		return
	}
	coll, err1 := url.PathUnescape(segs[0])
	id, err2 := url.PathUnescape(segs[1])
	if err1 != nil || err2 != nil || coll == "" || id == "" || id == "." || id == ".." ||
		(strings.HasPrefix(id, "__") && strings.HasSuffix(id, "__") && len(id) > 4) || len(id) > 1500 {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid document path")
		return
	}
	full := "projects/" + project + "/databases/(default)/documents/" + coll + "/" + id
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.db == nil {
		noDatabase(w, project)
		return
	}
	key := coll + "/" + id
	d := f.docs[key]
	q := r.URL.Query()
	switch r.Method {
	case http.MethodGet:
		if d == nil {
			writeError(w, 404, "NOT_FOUND", `Document "`+full+`" not found.`)
			return
		}
		writeJSON(w, 200, docJSON(full, d))
	case http.MethodDelete:
		if !f.precondition(w, q, d, full) {
			return
		}
		delete(f.docs, key)
		writeJSON(w, 200, map[string]any{})
	case http.MethodPatch:
		var req struct {
			Name   string         `json:"name"`
			Fields map[string]any `json:"fields"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, 400, "INVALID_ARGUMENT", "invalid JSON payload")
			return
		}
		if req.Name != "" && req.Name != full {
			writeError(w, 400, "INVALID_ARGUMENT", "document name does not match the request path")
			return
		}
		for k, v := range req.Fields {
			if k == "" {
				writeError(w, 400, "INVALID_ARGUMENT", "empty field name")
				return
			}
			if err := checkValue(v, 0); err != nil {
				writeError(w, 400, "INVALID_ARGUMENT", "Invalid value for field "+k+": "+err.Error())
				return
			}
		}
		masks, hasMask := q["updateMask.fieldPaths"]
		var paths [][]string
		for _, m := range masks {
			p, err := parseFieldPath(m)
			if err != nil {
				writeError(w, 400, "INVALID_ARGUMENT", "Invalid field path in updateMask: "+err.Error())
				return
			}
			paths = append(paths, p)
		}
		if !f.precondition(w, q, d, full) {
			return
		}
		now := f.now()
		nd := &fsDoc{created: now, saved: now, fields: map[string]any{}}
		if d != nil {
			nd.created = d.created
		}
		if !hasMask {
			nd.fields = req.Fields // no mask: the document is replaced whole
		} else {
			if d != nil {
				nd.fields = deepCopy(d.fields).(map[string]any)
			}
			for _, p := range paths {
				if v, ok := lookup(req.Fields, p); ok {
					setPath(nd.fields, p, v)
				} else {
					delPath(nd.fields, p)
				}
			}
		}
		f.docs[key] = nd
		writeJSON(w, 200, docJSON(full, nd))
	default:
		writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

// precondition applies currentDocument.exists / updateTime; false means it
// answered. Real answers: exists=true on a missing document is 404 NOT_FOUND,
// exists=false on an existing one is 409 ALREADY_EXISTS, and a stale
// updateTime is 400 FAILED_PRECONDITION.
func (f *Firestore) precondition(w http.ResponseWriter, q url.Values, d *fsDoc, full string) bool {
	if e := q.Get("currentDocument.exists"); e != "" {
		switch {
		case e != "true" && e != "false":
			writeError(w, 400, "INVALID_ARGUMENT", "bad currentDocument.exists")
			return false
		case e == "true" && d == nil:
			writeError(w, 404, "NOT_FOUND", `No document to update: `+full)
			return false
		case e == "false" && d != nil:
			writeError(w, 409, "ALREADY_EXISTS", "Document already exists: "+full)
			return false
		}
	}
	if ut := q.Get("currentDocument.updateTime"); ut != "" {
		t, err := time.Parse(time.RFC3339Nano, ut)
		switch {
		case err != nil:
			writeError(w, 400, "INVALID_ARGUMENT", "bad currentDocument.updateTime")
			return false
		case d == nil:
			writeError(w, 404, "NOT_FOUND", `No document to update: `+full)
			return false
		case !t.Equal(d.saved):
			writeError(w, 400, "FAILED_PRECONDITION", "the stored version of the document does not match the precondition")
			return false
		}
	}
	return true
}

func docJSON(name string, d *fsDoc) map[string]any {
	return map[string]any{"name": name, "fields": d.fields, "createTime": fsTime(d.created), "updateTime": fsTime(d.saved)}
}

var typedKinds = map[string]bool{"nullValue": true, "stringValue": true, "booleanValue": true, "integerValue": true,
	"doubleValue": true, "timestampValue": true, "mapValue": true, "arrayValue": true}

// checkValue enforces the wire shape of a typed Value.
func checkValue(v any, depth int) error {
	if depth > 20 {
		return fmt.Errorf("nesting too deep")
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return fmt.Errorf("a value must have exactly one typed member")
	}
	for k, raw := range m {
		if !typedKinds[k] {
			return fmt.Errorf("unknown value kind %q", k)
		}
		switch k {
		case "nullValue":
			if raw != nil {
				return fmt.Errorf("nullValue must be null")
			}
		case "stringValue":
			if _, ok := raw.(string); !ok {
				return fmt.Errorf("stringValue must be a string")
			}
		case "booleanValue":
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("booleanValue must be a bool")
			}
		case "integerValue":
			s, ok := raw.(string)
			if !ok {
				return fmt.Errorf("integerValue must be a decimal string")
			}
			if _, err := strconv.ParseInt(s, 10, 64); err != nil {
				return fmt.Errorf("integerValue %q is not an int64", s)
			}
		case "doubleValue":
			if _, ok := raw.(float64); !ok {
				return fmt.Errorf("doubleValue must be a number")
			}
		case "timestampValue":
			s, ok := raw.(string)
			if !ok {
				return fmt.Errorf("timestampValue must be a string")
			}
			if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				return fmt.Errorf("timestampValue %q is not RFC 3339", s)
			}
		case "mapValue":
			mm, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("mapValue must be an object")
			}
			fs, _ := mm["fields"].(map[string]any)
			for fk, fv := range fs {
				if fk == "" {
					return fmt.Errorf("empty map key")
				}
				if err := checkValue(fv, depth+1); err != nil {
					return err
				}
			}
		case "arrayValue":
			am, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("arrayValue must be an object")
			}
			vs, _ := am["values"].([]any)
			for _, e := range vs {
				if err := checkValue(e, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// parseFieldPath parses an updateMask path: segments joined by dots, each
// either a simple identifier ([a-zA-Z_][a-zA-Z_0-9]*) or backticked with \`
// and \\ escapes. Anything else (a dash, a bare space, an empty segment, an
// unbalanced backtick) is invalid.
func parseFieldPath(s string) ([]string, error) {
	if s == "" {
		return nil, fmt.Errorf("empty path")
	}
	var segs []string
	i := 0
	for {
		if i >= len(s) {
			return nil, fmt.Errorf("%q: empty segment", s)
		}
		if s[i] == '`' {
			i++
			var b strings.Builder
			for {
				if i >= len(s) {
					return nil, fmt.Errorf("%q: unterminated backtick", s)
				}
				c := s[i]
				if c == '\\' && i+1 < len(s) {
					b.WriteByte(s[i+1])
					i += 2
					continue
				}
				if c == '`' {
					i++
					break
				}
				b.WriteByte(c)
				i++
			}
			if b.Len() == 0 {
				return nil, fmt.Errorf("%q: empty quoted segment", s)
			}
			segs = append(segs, b.String())
		} else {
			j := i
			for j < len(s) && s[j] != '.' {
				j++
			}
			seg := s[i:j]
			if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z_0-9]*$`).MatchString(seg) {
				return nil, fmt.Errorf("%q: segment %q must be a simple name or backtick-quoted", s, seg)
			}
			segs = append(segs, seg)
			i = j
		}
		if i == len(s) {
			return segs, nil
		}
		if s[i] != '.' {
			return nil, fmt.Errorf("%q: unexpected %q after a quoted segment", s, s[i])
		}
		i++
	}
}

func deepCopy(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func lookup(fields map[string]any, p []string) (any, bool) {
	cur := fields
	for i, k := range p {
		v, ok := cur[k]
		if !ok {
			return nil, false
		}
		if i == len(p)-1 {
			return v, true
		}
		mv, ok := v.(map[string]any)["mapValue"].(map[string]any)
		if !ok {
			return nil, false
		}
		cur, _ = mv["fields"].(map[string]any)
	}
	return nil, false
}

func setPath(fields map[string]any, p []string, v any) {
	cur := fields
	for _, k := range p[:len(p)-1] {
		mv, ok := cur[k].(map[string]any)["mapValue"].(map[string]any)
		if !ok {
			mv = map[string]any{"fields": map[string]any{}}
			cur[k] = map[string]any{"mapValue": mv}
		}
		fs, _ := mv["fields"].(map[string]any)
		if fs == nil {
			fs = map[string]any{}
			mv["fields"] = fs
		}
		cur = fs
	}
	cur[p[len(p)-1]] = deepCopy(v)
}

func delPath(fields map[string]any, p []string) {
	cur := fields
	for _, k := range p[:len(p)-1] {
		mv, ok := cur[k].(map[string]any)["mapValue"].(map[string]any)
		if !ok {
			return
		}
		cur, _ = mv["fields"].(map[string]any)
	}
	delete(cur, p[len(p)-1])
}

// ---- runQuery ----

type fsQuery struct {
	From []struct {
		CollectionID string `json:"collectionId"`
	} `json:"from"`
	Where   *fsFilter `json:"where"`
	OrderBy []struct {
		Field struct {
			FieldPath string `json:"fieldPath"`
		} `json:"field"`
		Direction string `json:"direction"`
	} `json:"orderBy"`
	Limit   int `json:"limit"`
	StartAt *struct {
		Values []any `json:"values"`
		Before bool  `json:"before"`
	} `json:"startAt"`
}

type fsFilter struct {
	Field *struct {
		Field struct {
			FieldPath string `json:"fieldPath"`
		} `json:"field"`
		Op    string `json:"op"`
		Value any    `json:"value"`
	} `json:"fieldFilter"`
	Composite *struct {
		Op      string     `json:"op"`
		Filters []fsFilter `json:"filters"`
	} `json:"compositeFilter"`
}

func (f *Firestore) runQuery(w http.ResponseWriter, project string, body []byte) {
	var req struct {
		StructuredQuery fsQuery `json:"structuredQuery"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid JSON payload")
		return
	}
	q := req.StructuredQuery
	if len(q.From) != 1 || q.From[0].CollectionID == "" {
		writeError(w, 400, "INVALID_ARGUMENT", "exactly one from collection is required")
		return
	}
	coll := q.From[0].CollectionID
	var leaves []*fsFilter
	if q.Where != nil {
		switch {
		case q.Where.Field != nil:
			leaves = []*fsFilter{q.Where}
		case q.Where.Composite != nil && q.Where.Composite.Op == "AND":
			for i := range q.Where.Composite.Filters {
				if q.Where.Composite.Filters[i].Field == nil {
					writeError(w, 400, "INVALID_ARGUMENT", "only field filters under AND are faked")
					return
				}
				leaves = append(leaves, &q.Where.Composite.Filters[i])
			}
		default:
			writeError(w, 400, "INVALID_ARGUMENT", "unsupported filter")
			return
		}
	}
	ineqField := ""
	fields := map[string]bool{}
	for _, l := range leaves {
		fp, err := parseFieldPath(l.Field.Field.FieldPath)
		if err != nil || len(fp) != 1 {
			writeError(w, 400, "INVALID_ARGUMENT", "the fake filters on top-level fields only")
			return
		}
		if err := checkValue(l.Field.Value, 0); err != nil {
			writeError(w, 400, "INVALID_ARGUMENT", err.Error())
			return
		}
		fields[fp[0]] = true
		switch l.Field.Op {
		case "EQUAL":
		case "LESS_THAN", "LESS_THAN_OR_EQUAL", "GREATER_THAN", "GREATER_THAN_OR_EQUAL":
			ineqField = fp[0]
		default:
			writeError(w, 400, "INVALID_ARGUMENT", "unsupported op "+l.Field.Op)
			return
		}
	}
	if ineqField != "" && len(fields) > 1 {
		writeError(w, 400, "FAILED_PRECONDITION", "The query requires an index.")
		return
	}
	// Order: the inequality field must come first; __name__ is the implicit
	// last tie-break.
	var order []string
	for _, o := range q.OrderBy {
		if o.Direction != "" && o.Direction != "ASCENDING" {
			writeError(w, 400, "INVALID_ARGUMENT", "the fake orders ascending only")
			return
		}
		fp := o.Field.FieldPath
		if fp != "__name__" {
			p, err := parseFieldPath(fp)
			if err != nil || len(p) != 1 {
				writeError(w, 400, "INVALID_ARGUMENT", "bad orderBy field")
				return
			}
			fp = p[0]
		}
		order = append(order, fp)
	}
	if ineqField != "" && len(order) > 0 && order[0] != ineqField {
		writeError(w, 400, "INVALID_ARGUMENT", "The first sort property must be the same as the property to which the inequality filter is applied.")
		return
	}
	if len(order) == 0 || order[len(order)-1] != "__name__" {
		order = append(order, "__name__")
	}
	if q.StartAt != nil && len(q.StartAt.Values) != len(order) {
		writeError(w, 400, "INVALID_ARGUMENT", "the cursor must have one value per orderBy field (including __name__)")
		return
	}
	if q.Limit < 0 {
		writeError(w, 400, "INVALID_ARGUMENT", "negative limit")
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.db == nil {
		noDatabase(w, project)
		return
	}
	prefix := "projects/" + project + "/databases/(default)/documents/" + coll + "/"
	type row struct {
		name string
		d    *fsDoc
	}
	var rows []row
	for k, d := range f.docs {
		c, id, _ := strings.Cut(k, "/")
		if c != coll {
			continue
		}
		ok := true
		for _, l := range leaves {
			if !matches(d.fields, l.Field.Field.FieldPath, l.Field.Op, l.Field.Value) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		// Ordering by a field excludes documents without it.
		for _, o := range order {
			if o != "__name__" {
				if _, has := d.fields[o]; !has {
					ok = false
				}
			}
		}
		if ok {
			rows = append(rows, row{prefix + id, d})
		}
	}
	cmpRow := func(a row, o string, bv any) int {
		if o == "__name__" {
			s, _ := bv.(map[string]any)["referenceValue"].(string)
			return strings.Compare(a.name, s)
		}
		c, _ := compareValues(a.d.fields[o], bv)
		return c
	}
	sort.Slice(rows, func(i, j int) bool {
		for _, o := range order {
			var bv any
			if o == "__name__" {
				bv = map[string]any{"referenceValue": rows[j].name}
			} else {
				bv = rows[j].d.fields[o]
			}
			if c := cmpRow(rows[i], o, bv); c != 0 {
				return c < 0
			}
		}
		return false
	})
	var out []any
	for _, r := range rows {
		if q.StartAt != nil {
			c := 0
			for i, o := range order {
				if c = cmpRow(r, o, q.StartAt.Values[i]); c != 0 {
					break
				}
			}
			if c < 0 || (c == 0 && !q.StartAt.Before) {
				continue
			}
		}
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
		out = append(out, map[string]any{"document": docJSON(r.name, r.d), "readTime": fsTime(f.now())})
	}
	if len(out) == 0 {
		out = []any{map[string]any{"readTime": fsTime(f.now())}}
	}
	writeJSON(w, 200, out)
}

func matches(fields map[string]any, path, op string, want any) bool {
	p, _ := parseFieldPath(path)
	got, ok := fields[p[0]]
	if !ok {
		return false
	}
	c, comparable := compareValues(got, want)
	if !comparable {
		return false
	}
	switch op {
	case "EQUAL":
		return c == 0
	case "LESS_THAN":
		return c < 0
	case "LESS_THAN_OR_EQUAL":
		return c <= 0
	case "GREATER_THAN":
		return c > 0
	default:
		return c >= 0
	}
}

// compareValues orders two typed values of the same kind (numbers compare
// across integer and double); ok is false for different kinds.
func compareValues(a, b any) (int, bool) {
	da, err1 := firestore.Decode(a)
	db, err2 := firestore.Decode(b)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	num := func(v any) (float64, bool) {
		switch x := v.(type) {
		case int64:
			return float64(x), true
		case float64:
			return x, true
		}
		return 0, false
	}
	switch x := da.(type) {
	case string:
		if y, ok := db.(string); ok {
			return strings.Compare(x, y), true
		}
	case time.Time:
		if y, ok := db.(time.Time); ok {
			return x.Compare(y), true
		}
	case bool:
		if y, ok := db.(bool); ok {
			switch {
			case x == y:
				return 0, true
			case !x:
				return -1, true
			}
			return 1, true
		}
	default:
		if xf, ok := num(da); ok {
			if yf, ok := num(db); ok {
				switch {
				case xf < yf:
					return -1, true
				case xf > yf:
					return 1, true
				}
				return 0, true
			}
		}
	}
	return 0, false
}

// listCollectionIDs answers the root's collection IDs (sorted, paged by
// pageSize/pageToken, the token being the last ID returned).
func (f *Firestore) listCollectionIDs(w http.ResponseWriter, project string, body []byte) {
	var req struct {
		PageSize  int    `json:"pageSize"`
		PageToken string `json:"pageToken"`
	}
	if len(body) > 0 && json.Unmarshal(body, &req) != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "bad request body")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.db == nil {
		noDatabase(w, project)
		return
	}
	seen := map[string]bool{}
	var ids []string
	for k := range f.docs {
		if c, _, _ := strings.Cut(k, "/"); !seen[c] {
			seen[c] = true
			ids = append(ids, c)
		}
	}
	sort.Strings(ids)
	var out []string
	for _, id := range ids {
		if req.PageToken == "" || id > req.PageToken {
			out = append(out, id)
		}
	}
	resp := map[string]any{}
	if req.PageSize > 0 && len(out) > req.PageSize {
		out = out[:req.PageSize]
		resp["nextPageToken"] = out[len(out)-1]
	}
	if len(out) > 0 {
		resp["collectionIds"] = out
	}
	writeJSON(w, 200, resp)
}
