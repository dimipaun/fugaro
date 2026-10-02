package rtdb_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

func idAuth(tok string) rtdb.Auth { return rtdb.Auth{IDToken: func() string { return tok }} }

func newClient(t *testing.T, f *gcpfake.RTDB, opts ...rtdb.Option) *rtdb.Client {
	t.Helper()
	c, err := rtdb.New(f.URL, idAuth("idtok"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

type cap_ struct {
	Daily int64 `json:"dailyMicros"`
}

func TestRTDBGetPutETag(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClient(t, f)
	var got cap_
	etag, found, err := c.GetETag(ctx(t), "config/caps/global", &got)
	if err != nil || found || etag != "null_etag" {
		t.Fatalf("absent: etag=%q found=%v err=%v", etag, found, err)
	}
	if err := c.PutIfMatch(ctx(t), "config/caps/global", etag, cap_{Daily: 60}); err != nil {
		t.Fatal(err)
	}
	// A second writer holding the same etag loses.
	err = c.PutIfMatch(ctx(t), "config/caps/global", etag, cap_{Daily: 99})
	if !errors.Is(err, rtdb.ErrPrecondition) {
		t.Fatalf("stale put = %v, want ErrPrecondition", err)
	}
	etag, found, err = c.GetETag(ctx(t), "config/caps/global", &got)
	if err != nil || !found || got.Daily != 60 || etag == "null_etag" || etag == "" {
		t.Fatalf("read back: %+v %q %v %v", got, etag, found, err)
	}
	if err := c.PutIfMatch(ctx(t), "config/caps/global", etag, cap_{Daily: 70}); err != nil {
		t.Fatal(err)
	}
	if found, err := c.Get(ctx(t), "config/caps/global", &got); err != nil || !found || got.Daily != 70 {
		t.Fatalf("get = %+v %v %v", got, found, err)
	}
	// Absent reads leave the target untouched.
	got = cap_{Daily: 5}
	if found, err := c.Get(ctx(t), "nope", &got); err != nil || found || got.Daily != 5 {
		t.Fatalf("absent get = %+v %v %v", got, found, err)
	}
	// Reading into a raw message.
	var raw json.RawMessage
	if _, err := c.Get(ctx(t), "config/caps/global", &raw); err != nil || string(raw) != `{"dailyMicros":70}` {
		t.Fatalf("raw = %s %v", raw, err)
	}
	// An escaped key is one node, not a path through a dot.
	if err := c.PutIfMatch(ctx(t), "runs/a%2Eb/r1", "null_etag", 7); err != nil {
		t.Fatal(err)
	}
	if v := f.Value("runs/a%2Eb/r1"); v != json.Number("7") {
		t.Fatalf("stored under %#v", f.Value("runs"))
	}
	var n int
	if found, err := c.Get(ctx(t), "runs/a%2Eb/r1", &n); err != nil || !found || n != 7 {
		t.Fatalf("get escaped = %d %v %v", n, found, err)
	}
}

func TestRTDBPatchMultiPathAtomic(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	f.Set("spend/1/global/counted", 100)
	f.Set("runs/x/r/reserved", 0)
	c := newClient(t, f)
	f.Deny("spend/1/global/counted")
	err := c.Patch(ctx(t), "", map[string]any{
		"runs/x/r/reserved":      2,
		"spend/1/global/counted": 102,
	})
	if !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("patch = %v, want ErrPermission", err)
	}
	if f.Value("runs/x/r/reserved") != json.Number("0") && f.Value("runs/x/r/reserved") != nil {
		t.Fatalf("one failing path did not reject all: %#v", f.Value(""))
	}
	f.Deny("")
	err = c.Patch(ctx(t), "", map[string]any{
		"runs/x/r/reserved":      2,
		"spend/1/global/counted": 102,
		"agents/x/r":             nil, // null deletes
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Value("runs/x/r/reserved") != json.Number("2") || f.Value("spend/1/global/counted") != json.Number("102") {
		t.Fatalf("patch not applied: %#v", f.Value(""))
	}
	// A patch below the root.
	if err := c.Patch(ctx(t), "runs/x/r", map[string]any{"spent": 1}); err != nil {
		t.Fatal(err)
	}
	if f.Value("runs/x/r/spent") != json.Number("1") {
		t.Fatalf("rooted patch: %#v", f.Value("runs"))
	}
	// An empty update sends nothing.
	n := len(f.Requests())
	if err := c.Patch(ctx(t), "", nil); err != nil || len(f.Requests()) != n {
		t.Fatalf("empty patch: %v, %d new requests", err, len(f.Requests())-n)
	}
	if err := c.Patch(ctx(t), "", map[string]any{"/abs": 1}); err == nil {
		t.Fatal("a key with a leading slash is a bug and must be refused")
	}
}

func TestRTDBPermissionIsTyped(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClient(t, f)
	f.DenyNext(1)
	err := c.Patch(ctx(t), "", map[string]any{"a": 1})
	var re *rtdb.Error
	if !errors.Is(err, rtdb.ErrPermission) || !errors.As(err, &re) || re.Status != 401 {
		t.Fatalf("401 = %v", err)
	}
	if errors.Is(err, rtdb.ErrUnavailable) || errors.Is(err, rtdb.ErrPrecondition) {
		t.Fatalf("a 401 must be only a permission error: %v", err)
	}
	for _, code := range []int{403} {
		f.Refuse(code, "PERMISSION_DENIED", "", "no")
		if _, err := c.Get(ctx(t), "a", new(int)); !errors.Is(err, rtdb.ErrPermission) {
			t.Errorf("%d = %v", code, err)
		}
	}
	for _, code := range []int{500, 502, 503, 429} {
		f.Refuse(code, "UNAVAILABLE", "", "down")
		if _, err := c.Get(ctx(t), "a", new(int)); !errors.Is(err, rtdb.ErrUnavailable) {
			t.Errorf("%d = %v, want ErrUnavailable", code, err)
		}
	}
	f.Refuse(400, "INVALID_ARGUMENT", "", "bad")
	_, err = c.Get(ctx(t), "a", new(int))
	if err == nil || errors.Is(err, rtdb.ErrUnavailable) || errors.Is(err, rtdb.ErrPermission) {
		t.Errorf("400 = %v: neither permission nor unavailable", err)
	}
	f.Refuse(0, "", "", "")

	// A dead port is unavailable, and a cancelled context is not.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := "http://" + l.Addr().String()
	l.Close()
	dc, _ := rtdb.New(dead, idAuth("tok"))
	if _, err := dc.Get(ctx(t), "a", new(int)); !errors.Is(err, rtdb.ErrUnavailable) {
		t.Errorf("connection refused = %v", err)
	}
	cc, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Get(cc, "a", new(int)); !errors.Is(err, context.Canceled) || errors.Is(err, rtdb.ErrUnavailable) {
		t.Errorf("cancelled = %v", err)
	}
}

func TestErrorsNeverCarryTheToken(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := "http://" + l.Addr().String()
	l.Close()
	const secret = "SECRET-ID-TOKEN-VALUE"
	c, _ := rtdb.New(dead, idAuth(secret))
	for _, call := range []func() error{
		func() error { _, err := c.Get(ctx(t), "a", new(int)); return err },
		func() error { return c.Patch(ctx(t), "", map[string]any{"a": 1}) },
		func() error { return c.PutIfMatch(ctx(t), "a", "e", 1) },
	} {
		if err := call(); err == nil || strings.Contains(err.Error(), secret) {
			t.Errorf("error %v leaks the token or is nil", err)
		}
	}
	f := gcpfake.NewRTDB(t)
	c2, _ := rtdb.New(f.URL, idAuth(secret))
	f.Refuse(503, "UNAVAILABLE", "", "echo "+secret)
	if _, err := c2.Get(ctx(t), "a", new(int)); err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("a server message echoing the token must be scrubbed: %v", err)
	}
}

func TestAuthModes(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	live := "tok1"
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return live }})
	if err != nil {
		t.Fatal(err)
	}
	c.Get(ctx(t), "a", new(int))
	live = "tok2" // a refreshed ID token is used by the next call
	c.Get(ctx(t), "a", new(int))
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "oauthtok", TokenType: "Bearer"})
	oc, err := rtdb.New(f.URL, rtdb.Auth{Source: src})
	if err != nil {
		t.Fatal(err)
	}
	oc.Get(ctx(t), "a", new(int))
	got := f.Credentials()
	if len(got) != 3 || got[0] != "auth:tok1" || got[1] != "auth:tok2" || got[2] != "bearer:oauthtok" {
		t.Fatalf("credentials = %q", got)
	}
	if _, err := rtdb.New(f.URL, rtdb.Auth{}); err == nil {
		t.Error("no auth accepted")
	}
	if _, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "x" }, Source: src}); err == nil {
		t.Error("two auths accepted")
	}
	if _, err := rtdb.New("not a url", idAuth("x")); err == nil {
		t.Error("bad url accepted")
	}
	// A failing token source is an error, not a request without credentials.
	bad := oauth2.ReuseTokenSource(nil, failingSource{})
	bc, _ := rtdb.New(f.URL, rtdb.Auth{Source: bad})
	n := len(f.Requests())
	if _, err := bc.Get(ctx(t), "a", new(int)); err == nil || len(f.Requests()) != n {
		t.Errorf("token failure: %v, %d requests", err, len(f.Requests())-n)
	}
}

type failingSource struct{}

func (failingSource) Token() (*oauth2.Token, error) { return nil, errors.New("adc expired") }

func TestRTDBServerTimeFromDate(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClient(t, f)
	if _, ok := c.ServerNow(); ok {
		t.Fatal("ServerNow before any response")
	}
	at := time.Date(2026, 10, 2, 23, 59, 58, 0, time.UTC)
	f.SetClock(func() time.Time { return at })
	if _, err := c.Get(ctx(t), "a", new(int)); err != nil {
		t.Fatal(err)
	}
	got, ok := c.ServerNow()
	if !ok || got.Before(at) || got.After(at.Add(2*time.Second)) || got.Location() != time.UTC {
		t.Fatalf("ServerNow = %v %v, want about %v", got, ok, at)
	}
	// It advances with the local clock between responses.
	time.Sleep(50 * time.Millisecond)
	later, _ := c.ServerNow()
	if !later.After(got) {
		t.Fatalf("ServerNow did not advance: %v then %v", got, later)
	}
	// The clock offset follows the newest answer: a server one day ahead.
	f.SetClock(func() time.Time { return at.Add(24 * time.Hour) })
	c.Get(ctx(t), "a", new(int))
	if got, _ := c.ServerNow(); got.Before(at.Add(24 * time.Hour)) {
		t.Fatalf("ServerNow = %v, did not follow the Date header", got)
	}
}

func recv(t *testing.T, ch <-chan rtdb.Event) rtdb.Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("stream closed")
		}
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("no event")
	}
	return rtdb.Event{}
}

func waitStreams(t *testing.T, f *gcpfake.RTDB, n int) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if f.Streams() == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("streams = %d, want %d", f.Streams(), n)
}

func TestRTDBStreamEvents(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	f.Set("config/kill/global", map[string]any{"on": false})
	var tokens atomic.Int32
	c, _ := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "tok" + string(rune('0'+tokens.Add(1))) }},
		rtdb.WithStreamBackoff(5*time.Millisecond, 20*time.Millisecond))
	sc, cancel := context.WithCancel(ctx(t))
	defer cancel()
	ch := c.Stream(sc, "config/kill")

	ev := recv(t, ch)
	if ev.Type != "put" || ev.Path != "/" || string(ev.Data) != `{"global":{"on":false}}` {
		t.Fatalf("initial = %+v", ev)
	}
	waitStreams(t, f, 1)

	cl := newClient(t, f)
	if err := cl.Patch(ctx(t), "config/kill", map[string]any{"repos": map[string]any{"x": map[string]any{"on": true}}}); err != nil {
		t.Fatal(err)
	}
	if ev := recv(t, ch); ev.Type != "patch" || ev.Path != "/" || string(ev.Data) != `{"repos":{"x":{"on":true}}}` {
		t.Fatalf("patch = %+v", ev)
	}
	if err := cl.Patch(ctx(t), "", map[string]any{"config/kill/global/on": true}); err != nil {
		t.Fatal(err)
	}
	if ev := recv(t, ch); ev.Type != "put" || ev.Path != "/global/on" || string(ev.Data) != `true` {
		t.Fatalf("put = %+v", ev)
	}
	f.SendKeepAlive()
	if ev := recv(t, ch); ev.Type != "keep-alive" {
		t.Fatalf("keep-alive = %+v", ev)
	}

	// auth_revoked: delivered, then the client reconnects with a fresh token.
	f.SendAuthRevoked()
	if ev := recv(t, ch); ev.Type != "auth_revoked" {
		t.Fatalf("auth_revoked = %+v", ev)
	}
	if ev := recv(t, ch); ev.Type != "put" || ev.Path != "/" {
		t.Fatalf("after reconnect the stream starts again with a put: %+v", ev)
	}
	creds := f.Credentials()
	last := creds[len(creds)-1]
	if first := creds[0]; first == last || !strings.HasPrefix(last, "auth:tok") {
		t.Fatalf("reconnect reused the old token: %q", creds)
	}
	cancel()
	waitStreams(t, f, 0)
	for range ch { // closes after the context ends
	}
}

func TestRTDBStreamReconnects(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	f.Set("config/kill/global", map[string]any{"on": false})
	c := newClient(t, f, rtdb.WithStreamBackoff(5*time.Millisecond, 20*time.Millisecond))
	sc, cancel := context.WithCancel(ctx(t))
	defer cancel()
	ch := c.Stream(sc, "config/kill")
	recv(t, ch)
	waitStreams(t, f, 1)

	// The connection drops: an error event, then the stream re-opens and
	// starts again with the current value (so a change made while it was
	// down is not lost).
	f.Set("config/kill/global/on", true)
	f.DropStreams()
	ev := recv(t, ch)
	if ev.Type != "error" || ev.Err == nil || !errors.Is(ev.Err, rtdb.ErrUnavailable) {
		t.Fatalf("drop = %+v", ev)
	}
	ev = recv(t, ch)
	if ev.Type != "put" || ev.Path != "/" || string(ev.Data) != `{"global":{"on":true}}` {
		t.Fatalf("after reconnect = %+v", ev)
	}

	// While the backend refuses, errors keep coming (the caller's grace
	// clock reads them); when it recovers the stream resumes.
	f.Refuse(503, "UNAVAILABLE", "", "down")
	f.DropStreams()
	for i := 0; i < 3; i++ {
		if ev := recv(t, ch); ev.Type != "error" || !errors.Is(ev.Err, rtdb.ErrUnavailable) {
			t.Fatalf("while down: %+v", ev)
		}
	}
	f.Refuse(0, "", "", "")
	for {
		ev := recv(t, ch)
		if ev.Type == "put" {
			break
		}
		if ev.Type != "error" {
			t.Fatalf("recovering: %+v", ev)
		}
	}
}

func TestRTDBStreamPermissionErrorsAreTyped(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClient(t, f, rtdb.WithStreamBackoff(5*time.Millisecond, 20*time.Millisecond))
	f.Refuse(401, "UNAUTHENTICATED", "", "expired")
	ch := c.Stream(ctx(t), "config/kill")
	if ev := recv(t, ch); ev.Type != "error" || !errors.Is(ev.Err, rtdb.ErrPermission) {
		t.Fatalf("401 on connect = %+v", ev)
	}
}

func TestRTDBStreamCancelEndsIt(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClient(t, f, rtdb.WithStreamBackoff(5*time.Millisecond, 20*time.Millisecond))
	ch := c.Stream(ctx(t), "config/kill")
	recv(t, ch)
	waitStreams(t, f, 1)
	f.SendCancel()
	if ev := recv(t, ch); ev.Type != "cancel" {
		t.Fatalf("cancel = %+v", ev)
	}
	// The server withdrew the listen: no reconnect, the channel closes.
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("event after cancel: %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel still open after cancel")
	}
	if f.Streams() != 0 {
		t.Fatal("the client re-opened a cancelled stream")
	}
}

func TestRTDBStreamIdleTimeoutReconnects(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClient(t, f,
		rtdb.WithStreamBackoff(5*time.Millisecond, 20*time.Millisecond),
		rtdb.WithStreamIdleTimeout(150*time.Millisecond))
	ch := c.Stream(ctx(t), "config/kill")
	recv(t, ch) // initial put
	ev := recv(t, ch)
	if ev.Type != "error" || !errors.Is(ev.Err, rtdb.ErrUnavailable) {
		t.Fatalf("silent stream = %+v", ev)
	}
	// Keep-alives keep it open.
	if ev := recv(t, ch); ev.Type != "put" {
		t.Fatalf("reconnected stream = %+v", ev)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(40 * time.Millisecond):
				f.SendKeepAlive()
			}
		}
	}()
	for i := 0; i < 8; i++ {
		if ev := recv(t, ch); ev.Type != "keep-alive" {
			t.Fatalf("a stream with keep-alives dropped: %+v", ev)
		}
	}
}

func TestRTDBStreamStopsOnContext(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClient(t, f)
	sc, cancel := context.WithCancel(ctx(t))
	ch := c.Stream(sc, "config/kill")
	recv(t, ch)
	cancel()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				waitStreams(t, f, 0)
				return
			}
		case <-deadline:
			t.Fatal("channel not closed after cancel")
		}
	}
}

func TestTokenSourceFailuresAreClassified(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"revoked refresh token", &oauth2.RetrieveError{Response: &http.Response{StatusCode: 400}, Body: []byte(`{"error":"invalid_grant"}`)}, rtdb.ErrPermission},
		{"unauthorized", &oauth2.RetrieveError{Response: &http.Response{StatusCode: 401}}, rtdb.ErrPermission},
		{"token endpoint down", &oauth2.RetrieveError{Response: &http.Response{StatusCode: 503}}, rtdb.ErrUnavailable},
		{"network failure", errors.New("dial tcp: i/o timeout"), rtdb.ErrUnavailable},
	} {
		c, _ := rtdb.New(f.URL, rtdb.Auth{Source: errSource{tc.err}}, rtdb.WithStreamBackoff(5*time.Millisecond, 10*time.Millisecond))
		if _, err := c.Get(ctx(t), "a", new(int)); !errors.Is(err, tc.want) {
			t.Errorf("%s: Get = %v, want %v", tc.name, err, tc.want)
		}
		ev := recv(t, c.Stream(ctx(t), "a"))
		if ev.Type != "error" || !errors.Is(ev.Err, tc.want) {
			t.Errorf("%s: stream event = %+v, want %v", tc.name, ev, tc.want)
		}
	}
	if n := len(f.Requests()); n != 0 {
		t.Errorf("%d requests went out without a credential", n)
	}
}

type errSource struct{ err error }

func (e errSource) Token() (*oauth2.Token, error) { return nil, e.err }

func TestPutAndPatchValidateArguments(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClient(t, f)
	if err := c.PutIfMatch(ctx(t), "a", "", 1); err == nil {
		t.Error("an empty etag means no precondition and must be refused")
	}
	for _, bad := range []string{"a//b", "a/../b", "a/./b", "a.b/c", "a/$x", "a/b#", "a/[0]", "a/\x01", "/lead", "trail/"} {
		if err := c.PutIfMatch(ctx(t), bad, "null_etag", 1); err == nil && bad != "/lead" && bad != "trail/" {
			t.Errorf("PutIfMatch path %q accepted", bad)
		}
		if err := c.Patch(ctx(t), "", map[string]any{bad: 1}); err == nil {
			t.Errorf("Patch key %q accepted", bad)
		}
		if err := c.Patch(ctx(t), bad, map[string]any{"ok": 1}); err == nil && bad != "/lead" && bad != "trail/" {
			t.Errorf("Patch root %q accepted", bad)
		}
		if _, err := c.Get(ctx(t), bad, new(int)); err == nil && bad != "/lead" && bad != "trail/" {
			t.Errorf("Get path %q accepted", bad)
		}
	}
	if n := len(f.Requests()); n != 6 { // only the tolerated "/lead" and "trail/" paths of Put, Patch root and Get
		t.Errorf("%d calls reached the server, want the 6 tolerated ones", n)
	}
	// Escaped keys and the root are fine.
	if err := c.Patch(ctx(t), "", map[string]any{"runs/a%2Eb/r1/x": 1}); err != nil {
		t.Error(err)
	}
}

// A server that accepts the credential and revokes it at once, over and over,
// must show up as errors so the caller's grace clock starts.
func TestRTDBStreamRevokeLoopEmitsError(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c := newClient(t, f, rtdb.WithStreamBackoff(5*time.Millisecond, 20*time.Millisecond))
	ch := c.Stream(ctx(t), "config/kill")
	var sawErr bool
	for i := 0; i < 40 && !sawErr; i++ {
		ev := recv(t, ch)
		switch ev.Type {
		case "put":
			waitStreams(t, f, 1)
			f.SendAuthRevoked()
		case "error":
			if !errors.Is(ev.Err, rtdb.ErrPermission) {
				t.Fatalf("error = %v", ev.Err)
			}
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("repeated immediate auth_revoked never produced an error event")
	}
}
