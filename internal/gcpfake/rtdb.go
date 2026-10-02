package gcpfake

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// RTDB is a fake of the Firebase Realtime Database REST API the budget
// client uses: GET (with X-Firebase-ETag), PUT (with if-match), multi-path
// PATCH (atomic), and Server-Sent Events streams. It implements no security
// rules (the emulator suite proves those); DenyNext and Deny inject the
// 401s a rule would give. Refuse, from Server, makes every call fail like an
// unreachable backend (503); DropStreams cuts the open connections.
//
// Values are JSON; numbers read back as json.Number. A null value is an
// absent node and empty objects are pruned, as in the real database.
type RTDB struct {
	*Server

	mu       sync.Mutex
	root     any
	denyNext int
	deny     []string
	clock    func() time.Time
	creds    []string
	streams  map[*rtdbStream]struct{}
}

type rtdbStream struct {
	path string // normalised, "" for the root
	ch   chan string
	done chan struct{}
	once sync.Once
}

func (s *rtdbStream) close() { s.once.Do(func() { close(s.done) }) }

// NewRTDB starts a Realtime Database fake that lives until the test ends.
func NewRTDB(t *testing.T) *RTDB {
	t.Helper()
	f := &RTDB{streams: map[*rtdbStream]struct{}{}, clock: time.Now}
	f.Server = newServer(t, f.handle)
	t.Cleanup(f.DropStreams)
	return f
}

// Set puts v at path (a nil v deletes it), as a test's seed.
func (f *RTDB) Set(path string, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.root = setAt(f.root, split(path), normalise(v))
}

// Value returns a copy of the node at path (nil when absent).
func (f *RTDB) Value(path string) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clone(getAt(f.root, split(path)))
}

// DenyNext makes the next n write requests (PUT or PATCH; never reads)
// answer 401 Permission denied, changing nothing.
func (f *RTDB) DenyNext(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.denyNext = n
}

// Deny makes any write that touches path, or a node above or below it, answer
// 401 and change nothing (the whole multi-path update is rejected). An empty
// path lifts every Deny.
func (f *RTDB) Deny(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if path == "" {
		f.deny = nil
		return
	}
	f.deny = append(f.deny, strings.Join(split(path), "/"))
}

// SetClock sets the time the fake answers in its Date header.
func (f *RTDB) SetClock(now func() time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = now
}

// Credentials lists, per request in order, the credential it carried:
// "auth:<token>" for the auth query parameter, "bearer:<token>" for an
// Authorization header, "" for none.
func (f *RTDB) Credentials() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.creds...)
}

// Streams is the number of open event streams.
func (f *RTDB) Streams() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.streams)
}

// SendKeepAlive sends a keep-alive event to every stream.
func (f *RTDB) SendKeepAlive() { f.broadcast("keep-alive", "null", false) }

// SendAuthRevoked sends auth_revoked to every stream and closes them, as the
// server does when the credential used to listen expires.
func (f *RTDB) SendAuthRevoked() {
	f.broadcast("auth_revoked", `"credential is no longer valid"`, true)
}

// SendCancel sends cancel (permission lost) to every stream and closes them.
func (f *RTDB) SendCancel() { f.broadcast("cancel", `"permission_denied"`, true) }

// DropStreams closes every stream's connection without a word.
func (f *RTDB) DropStreams() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for s := range f.streams {
		s.close()
	}
}

func (f *RTDB) broadcast(event, data string, closeAfter bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for s := range f.streams {
		s.ch <- sse(event, data)
		if closeAfter {
			s.ch <- ""
		}
	}
}

type sseData struct {
	Path string `json:"path"`
	Data any    `json:"data"`
}

func sse(event, data string) string { return "event: " + event + "\ndata: " + data + "\n\n" }

func (f *RTDB) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	if !strings.HasSuffix(r.URL.Path, ".json") {
		f.unhandled(w, r)
		return
	}
	path := split(strings.TrimSuffix(r.URL.Path, ".json"))
	f.mu.Lock()
	cred := ""
	if a := r.URL.Query().Get("auth"); a != "" {
		cred = "auth:" + a
	} else if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		cred = "bearer:" + strings.TrimPrefix(h, "Bearer ")
	}
	f.creds = append(f.creds, cred)
	w.Header().Set("Date", f.clock().UTC().Format(http.TimeFormat))
	f.mu.Unlock()

	switch r.Method {
	case http.MethodGet:
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			f.serveStream(w, r, path)
			return
		}
		f.mu.Lock()
		v := getAt(f.root, path)
		f.mu.Unlock()
		if r.Header.Get("X-Firebase-ETag") == "true" {
			w.Header().Set("ETag", etagOf(v))
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodPut, http.MethodPatch:
		f.write(w, r, path, body)
	default:
		f.unhandled(w, r)
	}
}

func (f *RTDB) write(w http.ResponseWriter, r *http.Request, path []string, body []byte) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var in any
	if err := dec.Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid data; couldn't parse JSON object, array, or value."})
		return
	}
	in = normalise(in)

	f.mu.Lock()
	defer f.mu.Unlock()

	// The changes, as absolute location → value.
	type change struct {
		path []string
		v    any
	}
	var changes []change
	if r.Method == http.MethodPut {
		changes = []change{{path, in}}
	} else {
		m, ok := in.(map[string]any)
		if !ok {
			// PATCH null is a no-op for an absent node.
			if in != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid data; must be an object."})
				return
			}
		}
		for k, v := range m {
			changes = append(changes, change{append(append([]string{}, path...), split(k)...), normalise(v)})
		}
	}

	if f.denyNext > 0 {
		f.denyNext--
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Permission denied"})
		return
	}
	for _, c := range changes {
		for _, d := range f.deny {
			if touches(strings.Join(c.path, "/"), d) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Permission denied"})
				return
			}
		}
	}
	if m := r.Header.Get("If-Match"); m != "" && r.Method == http.MethodPut {
		cur := getAt(f.root, path)
		if m != etagOf(cur) {
			w.Header().Set("ETag", etagOf(cur))
			writeJSON(w, http.StatusPreconditionFailed, cur)
			return
		}
	}

	before := f.root
	next := clone(f.root)
	for _, c := range changes {
		next = setAt(next, c.path, c.v)
	}
	f.root = next

	// Tell the streams.
	for s := range f.streams {
		sp := split(s.path)
		if r.Method == http.MethodPatch && hasPrefix(path, sp) && !hasSlash(in) {
			s.ch <- sse("patch", mustJSON(sseData{rel(path, sp), in}))
			continue
		}
		for _, c := range changes {
			switch {
			case hasPrefix(c.path, sp):
				s.ch <- sse("put", mustJSON(sseData{rel(c.path, sp), c.v}))
			case hasPrefix(sp, c.path):
				if mustJSON(getAt(before, sp)) != mustJSON(getAt(f.root, sp)) {
					s.ch <- sse("put", mustJSON(sseData{"/", getAt(f.root, sp)}))
				}
			}
		}
	}

	if r.URL.Query().Get("print") == "silent" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, getAt(f.root, path))
}

func (f *RTDB) serveStream(w http.ResponseWriter, r *http.Request, path []string) {
	fl, ok := w.(http.Flusher)
	if !ok {
		f.unhandled(w, r)
		return
	}
	s := &rtdbStream{path: strings.Join(path, "/"), ch: make(chan string, 64), done: make(chan struct{})}
	f.mu.Lock()
	f.streams[s] = struct{}{}
	init := getAt(f.root, path)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.streams, s)
		f.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, sse("put", mustJSON(sseData{"/", init})))
	fl.Flush()
	for {
		select {
		case m := <-s.ch:
			if m == "" {
				return
			}
			fmt.Fprint(w, m)
			fl.Flush()
		case <-s.done:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// Tree helpers. Nodes are map[string]any, json.Number, string, bool or nil.

func split(p string) []string {
	var out []string
	for _, s := range strings.Split(strings.Trim(p, "/"), "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func hasPrefix(p, prefix []string) bool {
	if len(prefix) > len(p) {
		return false
	}
	for i := range prefix {
		if p[i] != prefix[i] {
			return false
		}
	}
	return true
}

func touches(path, denied string) bool {
	p, d := split(path), split(denied)
	return hasPrefix(p, d) || hasPrefix(d, p)
}

func rel(p, base []string) string { return "/" + strings.Join(p[len(base):], "/") }

func hasSlash(v any) bool {
	m, _ := v.(map[string]any)
	for k := range m {
		if strings.Contains(k, "/") {
			return true
		}
	}
	return false
}

func getAt(n any, p []string) any {
	for _, k := range p {
		m, ok := n.(map[string]any)
		if !ok {
			return nil
		}
		n = m[k]
	}
	return n
}

// setAt returns n with the node at p replaced by v (nil deletes), pruning
// objects left empty.
func setAt(n any, p []string, v any) any {
	if len(p) == 0 {
		return v
	}
	m, _ := n.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	child := setAt(m[p[0]], p[1:], v)
	if child == nil {
		delete(m, p[0])
	} else {
		m[p[0]] = child
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// normalise converts test-supplied Go values (ints, structs) to the JSON
// model and prunes nulls and empty objects.
func normalise(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case map[string]any:
		out := map[string]any{}
		for k, c := range x {
			if c = normalise(c); c != nil {
				out[k] = c
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case json.Number, string, bool:
		return x
	default:
		b, err := json.Marshal(x)
		if err != nil {
			panic(err)
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var out any
		if err := dec.Decode(&out); err != nil {
			panic(err)
		}
		if _, same := out.(map[string]any); same {
			return normalise(out)
		}
		return out
	}
}

func clone(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, c := range x {
			out[k] = clone(c)
		}
		return out
	}
	return v
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func etagOf(v any) string {
	if v == nil {
		return "null_etag"
	}
	sum := sha1.Sum([]byte(mustJSON(v)))
	return base64.StdEncoding.EncodeToString(sum[:])
}
