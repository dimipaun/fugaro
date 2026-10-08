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
	rules    []byte
	rulePuts int
	holds    []*rtdbHold
}

type rtdbHold struct {
	path    string
	stamped chan struct{}
	release chan struct{}
}

type rtdbStream struct {
	path string // normalised, "" for the root
	ch   chan string
	done chan struct{}
	once sync.Once
}

func (s *rtdbStream) close() { s.once.Do(func() { close(s.done) }) }

// push queues a message without blocking; a consumer that has fallen a full
// buffer behind is cut off (its connection closes and the client reconnects),
// so a stalled stream never blocks the fake's writes.
func (s *rtdbStream) push(m string) bool {
	select {
	case s.ch <- m:
		return true
	default:
		s.close()
		return false
	}
}

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

// HoldNext makes the next request to path stamp its Date header and then
// wait, unanswered, until release is called (or the request is cancelled):
// a response delayed in flight, so a test can deliver it after the answers
// to requests sent later. stamped is closed once the Date is fixed.
func (f *RTDB) HoldNext(path string) (stamped <-chan struct{}, release func()) {
	h := &rtdbHold{path: strings.Join(split(path), "/"), stamped: make(chan struct{}), release: make(chan struct{})}
	f.mu.Lock()
	f.holds = append(f.holds, h)
	f.mu.Unlock()
	var once sync.Once
	return h.stamped, func() { once.Do(func() { close(h.release) }) }
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

// send pushes to a stream (f.mu held), dropping it from the set if it is cut off.
func (f *RTDB) send(s *rtdbStream, m string) {
	if !s.push(m) {
		delete(f.streams, s)
	}
}

func (f *RTDB) broadcast(event, data string, closeAfter bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for s := range f.streams {
		ok := s.push(sse(event, data))
		if ok && closeAfter {
			ok = s.push("")
		}
		if !ok {
			delete(f.streams, s)
		}
	}
}

type sseData struct {
	Path string `json:"path"`
	Data any    `json:"data"`
}

func sse(event, data string) string { return "event: " + event + "\ndata: " + data + "\n\n" }

// Rules is the security rules last deployed with PUT /.settings/rules (nil
// when none), and RulePuts how many times they were deployed.
func (f *RTDB) Rules() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.rules...)
}

// RulePuts is the number of rules deployments.
func (f *RTDB) RulePuts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rulePuts
}

func (f *RTDB) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	if !strings.HasSuffix(r.URL.Path, ".json") {
		f.unhandled(w, r)
		return
	}
	path := split(strings.TrimSuffix(r.URL.Path, ".json"))
	if len(path) == 2 && path[0] == ".settings" && path[1] == "rules" {
		f.settingsRules(w, r, body)
		return
	}
	f.mu.Lock()
	cred := ""
	if a := r.URL.Query().Get("auth"); a != "" {
		cred = "auth:" + a
	} else if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		cred = "bearer:" + strings.TrimPrefix(h, "Bearer ")
	}
	f.creds = append(f.creds, cred)
	w.Header().Set("Date", f.clock().UTC().Format(http.TimeFormat))
	var hold *rtdbHold
	for i, h := range f.holds {
		if h.path == strings.Join(path, "/") {
			hold = h
			f.holds = append(f.holds[:i], f.holds[i+1:]...)
			break
		}
	}
	f.mu.Unlock()
	if hold != nil {
		close(hold.stamped)
		select {
		case <-hold.release:
		case <-r.Context().Done():
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			f.serveStream(w, r, path)
			return
		}
		f.mu.Lock()
		v := getAt(f.root, path)
		f.mu.Unlock()
		if r.URL.Query().Get("shallow") == "true" {
			if m, ok := v.(map[string]any); ok {
				shallow := map[string]any{}
				for k := range m {
					shallow[k] = true
				}
				v = shallow
			}
		}
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
	// A PATCH keeps its nulls: each key is a path and null deletes it, as in
	// the real database; the value is normalised per path below.
	var patch map[string]any
	if r.Method == http.MethodPatch {
		patch, _ = in.(map[string]any)
	}
	in = normalise(in)
	for _, seg := range path {
		if !validKey(seg) {
			badKey(w, seg)
			return
		}
	}
	check := in
	if patch != nil {
		check = patch
	}
	if k, bad := firstBadKey(check, r.Method == http.MethodPatch); bad {
		badKey(w, k)
		return
	}

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
		m := patch
		if m == nil {
			// PATCH null is a no-op for an absent node.
			if _, isObj := in.(map[string]any); !isObj && in != nil {
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
	// The real database refuses a conditional request that also asks for
	// print=silent or shallow (found live on 2026-10-02: PUT with If-Match and
	// print=silent answers 400).
	if r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" {
		q := r.URL.Query()
		if q.Get("print") == "silent" || q.Get("shallow") != "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Mixing 'shallow', querying parameters or 'print=silent' and if-match or if-none-match requests is not supported"})
			return
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
			f.send(s, sse("patch", mustJSON(sseData{rel(path, sp), in})))
			continue
		}
		for _, c := range changes {
			switch {
			case hasPrefix(c.path, sp):
				f.send(s, sse("put", mustJSON(sseData{rel(c.path, sp), c.v})))
			case hasPrefix(sp, c.path):
				if mustJSON(getAt(before, sp)) != mustJSON(getAt(f.root, sp)) {
					f.send(s, sse("put", mustJSON(sseData{"/", getAt(f.root, sp)})))
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
	rc := http.NewResponseController(w)
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
			_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
			fmt.Fprint(w, m)
			fl.Flush()
		case <-s.done:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// validKey is RTDB's key rule: non-empty, at most 768 bytes, none of . $ # [ ]
// / and no ASCII control characters.
func validKey(k string) bool {
	if k == "" || len(k) > 768 {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if c < 0x20 || c == 0x7f || strings.IndexByte(".$#[]/", c) >= 0 {
			return false
		}
	}
	return true
}

// firstBadKey finds a key of a written value that real RTDB rejects. At the
// top level of a PATCH the keys are paths: each of their segments is checked.
func firstBadKey(v any, patchTop bool) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", false
	}
	for k, c := range m {
		if patchTop {
			for _, sg := range strings.Split(k, "/") {
				if !validKey(sg) {
					return k, true
				}
			}
		} else if !validKey(k) {
			return k, true
		}
		if bk, bad := firstBadKey(c, false); bad {
			return bk, true
		}
	}
	return "", false
}

func badKey(w http.ResponseWriter, k string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("Invalid data; key %q is empty, too long or contains . $ # [ ] / or a control character", k)})
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

// settingsRules serves /.settings/rules: GET the deployed document, PUT to
// deploy one (any JSON; the fake does not interpret rules).
func (f *RTDB) settingsRules(w http.ResponseWriter, r *http.Request, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		if f.rules == nil {
			writeJSON(w, http.StatusOK, map[string]any{"rules": map[string]any{".read": false, ".write": false}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(f.rules)
	case http.MethodPut:
		if !json.Valid(body) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid rules"})
			return
		}
		if f.denyNext > 0 {
			f.denyNext--
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Permission denied"})
			return
		}
		f.rules = append([]byte(nil), body...)
		f.rulePuts++
		if r.URL.Query().Get("print") == "silent" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, json.RawMessage(body))
	default:
		f.unhandled(w, r)
	}
}
