package gcpfake

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func rtdbDo(t *testing.T, method, url, body string, hdr map[string]string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b)), resp.Header
}

func TestRTDBFakeGetPutDelete(t *testing.T) {
	f := NewRTDB(t)
	if code, body, _ := rtdbDo(t, "GET", f.URL+"/a/b.json", "", nil); code != 200 || body != "null" {
		t.Fatalf("absent = %d %q", code, body)
	}
	if code, _, _ := rtdbDo(t, "PUT", f.URL+"/a/b.json", `{"x":1,"y":{"z":"s"}}`, nil); code != 200 {
		t.Fatalf("put = %d", code)
	}
	if _, body, _ := rtdbDo(t, "GET", f.URL+"/a.json", "", nil); body != `{"b":{"x":1,"y":{"z":"s"}}}` {
		t.Fatalf("get a = %s", body)
	}
	if _, body, _ := rtdbDo(t, "GET", f.URL+"/a/b/y/z.json", "", nil); body != `"s"` {
		t.Fatalf("leaf = %s", body)
	}
	// Writing null deletes and prunes the empty parents.
	rtdbDo(t, "PUT", f.URL+"/a/b/y/z.json", `null`, nil)
	rtdbDo(t, "PUT", f.URL+"/a/b/x.json", `null`, nil)
	if _, body, _ := rtdbDo(t, "GET", f.URL+"/.json", "", nil); body != "null" {
		t.Fatalf("root after deletes = %s", body)
	}
	// print=silent answers 204.
	if code, _, _ := rtdbDo(t, "PUT", f.URL+"/k.json?print=silent", `1`, nil); code != 204 {
		t.Fatalf("silent put = %d", code)
	}
	// A key written escaped is stored verbatim (the URL is decoded once).
	rtdbDo(t, "PUT", f.URL+"/runs/a%252Eb.json", `7`, nil)
	if got := f.Value("runs/a%2Eb"); got != json.Number("7") {
		t.Fatalf("stored = %#v", f.Value("runs"))
	}
}

func TestRTDBFakeETag(t *testing.T) {
	f := NewRTDB(t)
	_, _, h := rtdbDo(t, "GET", f.URL+"/c.json", "", map[string]string{"X-Firebase-ETag": "true"})
	if h.Get("ETag") != "null_etag" {
		t.Fatalf("etag of absent = %q", h.Get("ETag"))
	}
	if code, _, _ := rtdbDo(t, "PUT", f.URL+"/c.json", `5`, map[string]string{"If-Match": "null_etag"}); code != 200 {
		t.Fatalf("put on null_etag = %d", code)
	}
	code, body, _ := rtdbDo(t, "PUT", f.URL+"/c.json", `6`, map[string]string{"If-Match": "null_etag"})
	if code != 412 || body != "5" {
		t.Fatalf("stale put = %d %q (a 412 carries the current value)", code, body)
	}
	_, _, h = rtdbDo(t, "GET", f.URL+"/c.json", "", map[string]string{"X-Firebase-ETag": "true"})
	etag := h.Get("ETag")
	if etag == "" || etag == "null_etag" {
		t.Fatalf("etag = %q", etag)
	}
	if code, _, _ := rtdbDo(t, "PUT", f.URL+"/c.json", `6`, map[string]string{"If-Match": etag}); code != 200 {
		t.Fatalf("put with the current etag = %d", code)
	}
	if got := f.Value("c"); got != json.Number("6") {
		t.Fatalf("c = %#v", got)
	}
}

func TestRTDBFakeMultiPathAtomic(t *testing.T) {
	f := NewRTDB(t)
	f.Set("counters/a", 0)
	f.Set("counters/b", 0)
	// One failing path rejects all of them.
	f.Deny("counters/b")
	code, _, _ := rtdbDo(t, "PATCH", f.URL+"/.json", `{"counters/a":1,"counters/b":1,"x/y":2}`, nil)
	if code != 401 {
		t.Fatalf("patch = %d, want 401", code)
	}
	if f.Value("counters/a") != json.Number("0") && f.Value("counters/a") != 0 || f.Value("x") != nil {
		t.Fatalf("a rejected patch changed the tree: %#v", f.Value(""))
	}
	f.Deny("")
	// Concurrent patches each add to both counters; every committed patch
	// moves both, never one.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rtdbDo(t, "PATCH", f.URL+"/.json?print=silent", `{"counters/a":1,"counters/b":1,"log/`+strconv.FormatInt(time.Now().UnixNano(), 10)+`":true}`, nil)
		}()
	}
	wg.Wait()
	a, b := f.Value("counters/a"), f.Value("counters/b")
	if a != b || a != json.Number("1") {
		t.Fatalf("counters = %v, %v", a, b)
	}
}

func TestRTDBFakeDenyNextCountsWritesOnly(t *testing.T) {
	f := NewRTDB(t)
	f.DenyNext(2)
	if code, _, _ := rtdbDo(t, "GET", f.URL+"/a.json", "", nil); code != 200 {
		t.Fatalf("read while denying = %d", code)
	}
	for i := 0; i < 2; i++ {
		if code, body, _ := rtdbDo(t, "PUT", f.URL+"/a.json", `1`, nil); code != 401 || !strings.Contains(body, "Permission denied") {
			t.Fatalf("write %d = %d %q", i, code, body)
		}
	}
	if code, _, _ := rtdbDo(t, "PUT", f.URL+"/a.json", `1`, nil); code != 200 {
		t.Fatalf("third write = %d", code)
	}
}

func TestRTDBFakeDateHeaderFollowsClock(t *testing.T) {
	f := NewRTDB(t)
	at := time.Date(2026, 10, 2, 23, 59, 59, 0, time.UTC)
	f.SetClock(func() time.Time { return at })
	_, _, h := rtdbDo(t, "GET", f.URL+"/a.json", "", nil)
	if h.Get("Date") != "Fri, 02 Oct 2026 23:59:59 GMT" {
		t.Fatalf("Date = %q", h.Get("Date"))
	}
}

func TestRTDBFakeRecordsCredentials(t *testing.T) {
	f := NewRTDB(t)
	rtdbDo(t, "GET", f.URL+"/a.json?auth=idtok", "", nil)
	rtdbDo(t, "GET", f.URL+"/a.json", "", map[string]string{"Authorization": "Bearer oauthtok"})
	rtdbDo(t, "GET", f.URL+"/a.json", "", nil)
	got := f.Credentials()
	if len(got) != 3 || got[0] != "auth:idtok" || got[1] != "bearer:oauthtok" || got[2] != "" {
		t.Fatalf("credentials = %q", got)
	}
}

type sseReader struct {
	t  *testing.T
	sc *bufio.Scanner
}

func (r *sseReader) next() (event, data string, ok bool) {
	for r.sc.Scan() {
		line := r.sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "" && event != "":
			return event, data, true
		}
	}
	return "", "", false
}

func openSSE(t *testing.T, url string) *sseReader {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	return &sseReader{t: t, sc: bufio.NewScanner(resp.Body)}
}

func waitStreams(t *testing.T, f *RTDB, n int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if f.Streams() == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("streams = %d, want %d", f.Streams(), n)
}

func TestRTDBFakeStream(t *testing.T) {
	f := NewRTDB(t)
	f.Set("config/kill/global", map[string]any{"on": false})
	r := openSSE(t, f.URL+"/config/kill.json")
	if ev, data, _ := r.next(); ev != "put" || data != `{"path":"/","data":{"global":{"on":false}}}` {
		t.Fatalf("initial = %s %s", ev, data)
	}
	waitStreams(t, f, 1)

	// A put below the stream's path.
	rtdbDo(t, "PUT", f.URL+"/config/kill/global/on.json", `true`, nil)
	if ev, data, _ := r.next(); ev != "put" || data != `{"path":"/global/on","data":true}` {
		t.Fatalf("put = %s %s", ev, data)
	}
	// A PATCH on the streamed path is a patch event.
	rtdbDo(t, "PATCH", f.URL+"/config/kill.json", `{"repos":{"x":{"on":true}}}`, nil)
	if ev, data, _ := r.next(); ev != "patch" || data != `{"path":"/","data":{"repos":{"x":{"on":true}}}}` {
		t.Fatalf("patch = %s %s", ev, data)
	}
	// A write above it that changes it is a put of the whole value.
	rtdbDo(t, "PUT", f.URL+"/config.json", `{"kill":{"global":{"on":false}}}`, nil)
	if ev, data, _ := r.next(); ev != "put" || data != `{"path":"/","data":{"global":{"on":false}}}` {
		t.Fatalf("ancestor put = %s %s", ev, data)
	}
	// A write elsewhere is not sent.
	rtdbDo(t, "PUT", f.URL+"/other.json", `1`, nil)
	f.SendKeepAlive()
	if ev, _, _ := r.next(); ev != "keep-alive" {
		t.Fatalf("after an unrelated write: %s", ev)
	}
	f.SendAuthRevoked()
	if ev, data, _ := r.next(); ev != "auth_revoked" || data != `"credential is no longer valid"` {
		t.Fatalf("auth_revoked = %s %s", ev, data)
	}
	if _, _, ok := r.next(); ok {
		t.Fatal("the stream stays open after auth_revoked")
	}
	waitStreams(t, f, 0)

	r = openSSE(t, f.URL+"/config/kill.json")
	r.next()
	f.SendCancel()
	if ev, data, _ := r.next(); ev != "cancel" || data != `"permission_denied"` {
		t.Fatalf("cancel = %s %s", ev, data)
	}
	waitStreams(t, f, 0)

	r = openSSE(t, f.URL+"/config/kill.json")
	r.next()
	waitStreams(t, f, 1)
	f.DropStreams()
	if _, _, ok := r.next(); ok {
		t.Fatal("DropStreams left the stream open")
	}
}

func TestRTDBFakeUnhandledMethodFails(t *testing.T) {
	f := NewRTDB(t)
	var failed string
	f.failf = func(format string, args ...any) { failed = format }
	if code, _, _ := rtdbDo(t, "POST", f.URL+"/a.json", `1`, nil); code != 400 {
		t.Fatalf("POST = %d", code)
	}
	if failed == "" {
		t.Fatal("POST did not fail the test")
	}
}

func TestRTDBFakeRejectsKeysRealRTDBRejects(t *testing.T) {
	f := NewRTDB(t)
	long := strings.Repeat("k", 769)
	for _, tc := range []struct{ name, method, path, body string }{
		{"dot in a value key", "PUT", "/a.json", `{"gemini-2.5":1}`},
		{"dollar", "PUT", "/a.json", `{"a$b":1}`},
		{"hash", "PUT", "/a.json", `{"a#b":1}`},
		{"brackets", "PUT", "/a.json", `{"a[0]":1}`},
		{"slash in a value key", "PUT", "/a.json", `{"a/b":1}`},
		{"control char", "PUT", "/a.json", `{"a\u0001b":1}`},
		{"empty key", "PUT", "/a.json", `{"":1}`},
		{"over-long key", "PUT", "/a.json", `{"` + long + `":1}`},
		{"nested bad key", "PUT", "/a.json", `{"x":{"y":{"a.b":1}}}`},
		{"bad key in a patch value", "PATCH", "/.json", `{"runs/x":{"a.b":1}}`},
		{"dot in a patch path segment", "PATCH", "/.json", `{"runs/a.b/r":1}`},
		{"empty segment in a patch path", "PATCH", "/.json", `{"runs//r":1}`},
		{"bad URL path segment", "PUT", "/runs/a.b.json", `1`},
		{"dollar in a patch path", "PATCH", "/.json", `{"runs/$x":1}`},
	} {
		code, body, _ := rtdbDo(t, tc.method, f.URL+tc.path, tc.body, nil)
		if code != 400 {
			t.Errorf("%s: %d %s, want 400", tc.name, code, body)
		}
	}
	if f.Value("") != nil {
		t.Fatalf("a rejected write changed the tree: %#v", f.Value(""))
	}
	// An escaped key is fine, and so is a key of exactly 768 bytes.
	if code, _, _ := rtdbDo(t, "PUT", f.URL+"/a.json", `{"gemini-2%2E5":1,"`+strings.Repeat("k", 768)+`":2}`, nil); code != 200 {
		t.Fatalf("valid keys = %d", code)
	}
}

func TestRTDBFakeSlowStreamConsumerDoesNotBlockWrites(t *testing.T) {
	f := NewRTDB(t)
	// A stream whose client never reads.
	req, _ := http.NewRequest("GET", f.URL+"/big.json", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	waitStreams(t, f, 1)
	blob := strings.Repeat("x", 256<<10)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 400; i++ {
			rtdbDo(t, "PUT", f.URL+"/big/v.json?print=silent", `"`+blob+`"`, nil)
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("writes blocked behind a stalled stream consumer")
	}
	waitStreams(t, f, 0) // the slow consumer was cut off
	// Other calls still work.
	if code, _, _ := rtdbDo(t, "GET", f.URL+"/x.json", "", nil); code != 200 {
		t.Fatalf("get = %d", code)
	}
}

// A PATCH with a null deletes that path, atomically with its other keys.
func TestRTDBFakePatchNullDeletes(t *testing.T) {
	f := NewRTDB(t)
	f.Set("agents/s/r", map[string]any{"repo": "acme/app", "stage": "implement"})
	f.Set("keep/x", 1)
	if code, _, _ := rtdbDo(t, "PATCH", f.URL+"/.json", `{"agents/s/r":null,"keep/y":2}`, nil); code != 200 {
		t.Fatalf("patch = %d", code)
	}
	if f.Value("agents") != nil {
		t.Fatalf("the entry survived a null: %v", f.Value("agents"))
	}
	if f.Value("keep/y") == nil || f.Value("keep/x") == nil {
		t.Fatal("the other keys of the update were lost")
	}
	if code, _, _ := rtdbDo(t, "PATCH", f.URL+"/.json", `{"agents/a.b/r":null}`, nil); code != 400 {
		t.Fatalf("a bad key in a null update = %d, want 400", code)
	}
}
