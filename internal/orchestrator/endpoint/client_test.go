package endpoint

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The reader of the endpoint a daemon serves (CG13c of
// docs/cli-output-console-graph/implementation-plan-graph.md): the lookup that
// only trusts a file a live endpoint proves, the read, the stream, and the one
// rule the whole design is written against -- the token travels in the header
// and in nothing else.
//
// Every case is hermetic: the endpoint is a real one on loopback and the
// registry file is placed in a temp directory by a Getenv stub, so no case
// reads a developer's own state directory and nothing here reaches a network
// that is not the loopback interface.

// freshToken is a value of the shape the endpoint mints, drawn per test: a
// fixture never carries a literal credential, and nothing here is a secret of
// anybody's (the plan's rule, .claude/rules/ee.md's).
func freshToken(t *testing.T) string {
	t.Helper()
	var b [tokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("drawing a token: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// clientFor is the reader a test uses: the state directory in a temp one, and
// the platform spelled out because that is what places it.
func clientFor(t *testing.T) (Client, func(string) string, string) {
	t.Helper()
	getenv, dir := registryEnv(t)
	return Client{Getenv: getenv, GOOS: "linux"}, getenv, dir
}

// hold is the file a daemon beside a live endpoint would have written.
func hold(t *testing.T, getenv func(string) string, reg Registry) *Holder {
	t.Helper()
	holder, err := Hold(getenv, "linux", reg)
	if err != nil {
		t.Fatalf("writing the registry: %v", err)
	}
	t.Cleanup(func() { _ = holder.Remove() })
	return holder
}

// registryOf is the registry file the endpoint under test itself would write.
func registryOf(id string, srv *Server) Registry {
	return Registry{ID: id, PID: os.Getpid(), Addr: srv.Addr(),
		Token: srv.Token(), Nonce: srv.Nonce(), Started: time.Now().UTC()}
}

// waitFor waits until cond holds, and fails the case at the deadline: a stream
// is read from its own goroutine, so a case waits for what arrived rather than
// sleeping for a guess.
func waitFor(t *testing.T, why string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

// notTheDaemon is a port on loopback that is not the daemon: it answers the
// endpoint's own route with a document this release can read and none of the
// endpoint's identity, and it answers to a token of its own. It is the case the
// handshake exists for -- a registry file a dead daemon left behind, and the
// port that belongs to some other process by the time a reader looks -- and the
// case a reader that trusted the file alone would draw a stranger from.
func notTheDaemon(t *testing.T) (string, string) {
	t.Helper()
	token := freshToken(t)
	document, err := orchestrator.EncodeState(testState("wi-not-the-daemon"))
	if err != nil {
		t.Fatalf("encoding the state a stranger serves: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerTick, tickedAt.Format(time.RFC3339))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(document)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), token
}

// registryForAForeignPort is the file for that port: the token the port answers
// to, and a nonce no live endpoint holds, which is the half of the handshake
// that decides whose file this is.
func registryForAForeignPort(t *testing.T, id, addr, token string) Registry {
	t.Helper()
	return Registry{ID: id, PID: os.Getpid(), Addr: addr, Token: token,
		Nonce: freshToken(t), Started: time.Now().UTC()}
}

// recordingTransport records what left the process: the whole URL of every
// request and the Authorization header it carried. It is how the token's one
// place is held: the header, and never the URL.
type recordingTransport struct {
	base http.RoundTripper

	mu    sync.Mutex
	urls  []string
	auths []string
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.urls = append(r.urls, req.URL.String())
	r.auths = append(r.auths, req.Header.Get(authorizationHeader))
	r.mu.Unlock()
	return r.base.RoundTrip(req)
}

func (r *recordingTransport) requests() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.urls...), append([]string(nil), r.auths...)
}

// redact takes a value out of a sentence a failure message will carry: a test
// that prints the token it found in a URL has written the credential into a CI
// log, which is the leak under test.
func redact(s, secret string) string {
	return strings.ReplaceAll(s, secret, "<token>")
}

// TestClientReadsTheLiveEndpoint: a registry file a live endpoint answers for
// is read, and what comes back is the daemon's own document and the server time
// of its tick. The decoder is the node's own (orchestrator.DecodeState), which
// is D1359's "the endpoint adds no shape": a document the node could not carry
// is one this reader cannot read either.
func TestClientReadsTheLiveEndpoint(t *testing.T) {
	ctx := context.Background()
	c, getenv, _ := clientFor(t)
	_, reg := servedEndpoint(t, ctx, endpointID)
	hold(t, getenv, reg)

	live, ok, err := c.Lookup(ctx, endpointID)
	if err != nil {
		t.Fatalf("looking up a live endpoint: %v", err)
	}
	if !ok {
		t.Fatal("a registry file a live endpoint answers for was not found")
	}
	state, at, err := c.Get(ctx, live)
	if err != nil {
		t.Fatalf("reading the live endpoint: %v", err)
	}
	if state.AgentID != endpointID || len(state.WorkItems) != 1 || state.WorkItems[0].ID != "wi-1" {
		t.Errorf("the read answered %+v, want the daemon's own state", state)
	}
	if !at.Equal(tickedAt) {
		t.Errorf("the read is stamped %s, want the server time of the tick %s", at, tickedAt)
	}
}

// TestClientLookupIsNoEndpointWhenThereIsNone: a machine that never ran a
// daemon has no run directory, and a machine whose daemon was killed has a file
// nothing answers for. Both are "no endpoint here" -- not an error -- because
// the caller reads the node and the cache below this exactly as it did before
// the endpoint existed (T5(a), amended 2026-11-11). The dead file is removed by
// the reader that read it and found it dead (CG13b's rule), and the reader
// plants nothing: the directory is not created to look in it.
func TestClientLookupIsNoEndpointWhenThereIsNone(t *testing.T) {
	ctx := context.Background()
	c, _, dir := clientFor(t)

	if live, ok, err := c.Lookup(ctx, endpointID); err != nil || ok {
		t.Errorf("a machine with no registry answered %+v, live %v, %v", live, ok, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the reader created the run directory %s to look in it", dir)
	}

	// A live process that is not the daemon: the pid is this test's and nothing
	// answers at the address, so the file is a dead one.
	writeRegistryFile(t, dir, registryFor(t, endpointID, freeAddr(t)), 0o600)
	if _, ok, err := c.Lookup(ctx, endpointID); err != nil || ok {
		t.Errorf("a file nothing answers for read as live (%v, %v)", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, endpointID+".json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the reader that found the file dead did not remove it: %v", err)
	}
}

// TestClientLookupRefusesAFileThisReleaseDidNotWrite: a registry file that is
// not 0600 in a directory that is not 0700 is refused rather than read, and so
// is a document that is not the shape this release wrote. The refusal is an
// error, because this reader cannot judge such a file, and the file is left
// alone: it is the next daemon's to replace and never this reader's to delete.
func TestClientLookupRefusesAFileThisReleaseDidNotWrite(t *testing.T) {
	ctx := context.Background()
	c, _, dir := clientFor(t)
	reg := registryFor(t, endpointID, freeAddr(t))
	path := filepath.Join(dir, endpointID+".json")

	writeRegistryFile(t, dir, reg, 0o644)
	if _, ok, err := c.Lookup(ctx, endpointID); err == nil {
		t.Errorf("a registry file at mode 0644 was read (live %v)", ok)
	}
	writeRegistryFile(t, dir, reg, 0o600)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("widening the run directory: %v", err)
	}
	if _, _, err := c.Lookup(ctx, endpointID); err == nil {
		t.Error("a registry file in a directory at mode 0755 was read")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("restoring the run directory: %v", err)
	}

	noNonce := reg
	noNonce.Nonce = ""
	writeRegistryFile(t, dir, noNonce, 0o600)
	if _, _, err := c.Lookup(ctx, endpointID); err == nil {
		t.Error("a registry file with no nonce was read")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("a file this reader cannot judge was removed: %v", err)
	}
}

// TestClientRefusesAnAnswerThatIsNotTheEndpoints: the two halves of "only the
// daemon's answer is a document" -- the handshake, which refuses a file whose
// port belongs to something else, and the identity of the answer itself, which
// is checked on every read and not only at the lookup. The second is what a
// port taken over between the lookup and the read fails: a process that answers
// 200 with a document this release can read is refused for not being the
// process that wrote the file, and nothing of its document reaches the caller.
func TestClientRefusesAnAnswerThatIsNotTheEndpoints(t *testing.T) {
	ctx := context.Background()
	c, _, dir := clientFor(t)
	addr, token := notTheDaemon(t)
	reg := registryForAForeignPort(t, endpointID, addr, token)
	writeRegistryFile(t, dir, reg, 0o600)

	if _, ok, err := c.Lookup(ctx, endpointID); err != nil || ok {
		t.Errorf("a port that is not the daemon read as live (%v, %v)", ok, err)
	}

	// The read's own half, taken through a Live built here so that no handshake
	// stands between the caller and the answer: the file is a live value and the
	// answer is a stranger's, which is exactly the case the check is for.
	state, _, err := c.Get(ctx, Live{reg: reg})
	if err == nil {
		t.Fatalf("a document from a port with none of the endpoint's identity was read: %+v", state)
	}
	if len(state.WorkItems) != 0 {
		t.Errorf("the refused read handed back %+v, want nothing", state)
	}

	// A Live this reader never looked up is refused rather than dialled: there
	// is no address to read and no endpoint that proved it is one.
	if _, _, err := c.Get(ctx, Live{}); err == nil {
		t.Error("a read was made through a Live that was not looked up")
	}
	if err := c.Stream(ctx, Live{}, func(orchestrator.State, time.Time) error { return nil }); err == nil {
		t.Error("a stream was opened through a Live that was not looked up")
	}
}

// TestClientStreamFollowsTheEndpoint: the stream carries the state at connect
// time and then every tick whose state moved, each with the server time of the
// tick it came from. It ends when the caller's context does (the caller's own
// choice, returned as the error it is) and when the callback fails (the
// callback's error, returned unchanged), and it never invents an event: the
// endpoint is silent while the state stands.
func TestClientStreamFollowsTheEndpoint(t *testing.T) {
	ctx := context.Background()
	c, getenv, _ := clientFor(t)
	f := newFixture(t)
	hold(t, getenv, registryOf(endpointID, f.srv))

	live, ok, err := c.Lookup(ctx, endpointID)
	if err != nil || !ok {
		t.Fatalf("looking up the endpoint under test: %v, live %v", err, ok)
	}

	t.Run("the state at connect time and then the ticks that moved", func(t *testing.T) {
		sctx, cancel := context.WithCancel(ctx)
		defer cancel()
		var mu sync.Mutex
		var ticks []time.Time
		done := make(chan error, 1)
		go func() {
			done <- c.Stream(sctx, live, func(state orchestrator.State, at time.Time) error {
				mu.Lock()
				ticks = append(ticks, at)
				mu.Unlock()
				return nil
			})
		}()

		waitFor(t, "the state at connect time", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(ticks) >= 1
		})
		later := tickedAt.Add(5 * time.Minute)
		f.tick(testState("wi-moved"), later)
		waitFor(t, "the tick that moved", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(ticks) >= 2
		})
		mu.Lock()
		got := append([]time.Time(nil), ticks...)
		mu.Unlock()
		if !got[0].Equal(tickedAt) || !got[1].Equal(later) {
			t.Errorf("the stream carried the ticks %v, want %s then %s", got, tickedAt, later)
		}

		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("the stream ended with %v, want the caller's own cancellation", err)
		}
	})

	t.Run("a callback that fails ends it", func(t *testing.T) {
		refused := errors.New("the reader went away")
		err := c.Stream(ctx, live, func(orchestrator.State, time.Time) error { return refused })
		if !errors.Is(err, refused) {
			t.Errorf("the stream ended with %v, want the callback's own error", err)
		}
	})

	t.Run("a daemon that is gone stops the stream", func(t *testing.T) {
		gone, err := Start(ctx, Options{ID: endpointID, Snapshot: func() (orchestrator.State, time.Time, bool) {
			return testState("wi-1"), tickedAt, true
		}})
		if err != nil {
			t.Fatalf("starting the endpoint: %v", err)
		}
		closed, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := gone.Close(closed); err != nil {
			t.Fatalf("closing the endpoint: %v", err)
		}
		if err := c.Stream(ctx, Live{reg: registryOf(endpointID, gone)}, func(orchestrator.State, time.Time) error { return nil }); err == nil {
			t.Error("a stream to an endpoint that is gone ended without an error")
		}
	})
}

// TestTheTokenIsNeverPrintedOrLogged is the rule the whole design is written
// against (F23), held at the end that holds the token: the credential travels
// in the one Authorization header of every request and appears in no URL, in no
// error, in no document and in no rendering of the value a caller holds — the
// last of those is every verb, `%#v` included, because the debug verb is the
// one a person reaches for when something is wrong. The registry file is 0600
// in a directory that is 0700 for exactly this reason,
// and a reader that put the value in a query string would undo it in one line
// -- the URL is what a proxy's access log, a shell's history and this machine's
// own process list carry.
func TestTheTokenIsNeverPrintedOrLogged(t *testing.T) {
	ctx := context.Background()
	getenv, _ := registryEnv(t)
	_, reg := servedEndpoint(t, ctx, endpointID)
	hold(t, getenv, reg)
	token := reg.Token

	rec := &recordingTransport{base: http.DefaultTransport}
	c := Client{Getenv: getenv, GOOS: "linux", HTTP: &http.Client{Transport: rec}}
	live, ok, err := c.Lookup(ctx, endpointID)
	if err != nil || !ok {
		t.Fatalf("looking up a live endpoint: %v, live %v", err, ok)
	}
	state, _, err := c.Get(ctx, live)
	if err != nil {
		t.Fatalf("reading the live endpoint: %s", redact(err.Error(), token))
	}

	urls, auths := rec.requests()
	if len(urls) != 2 {
		t.Fatalf("the two reads made %d requests, want the handshake and the read", len(urls))
	}
	for i, u := range urls {
		if strings.Contains(u, token) {
			t.Errorf("request %d carried the token in its URL: %s", i, redact(u, token))
		}
		if strings.Contains(u, "?") {
			t.Errorf("request %d carried a query string: %s", i, redact(u, token))
		}
		if !strings.HasSuffix(u, routeState) {
			t.Errorf("request %d went to %s, want %s", i, redact(u, token), routeState)
		}
		if auths[i] != bearerScheme+" "+token {
			t.Errorf("request %d carried the credential as %q, want it in the %s header and nowhere else",
				i, redact(auths[i], token), authorizationHeader)
		}
	}

	document, err := orchestrator.EncodeState(state)
	if err != nil {
		t.Fatalf("encoding the state the read answered with: %v", err)
	}
	if strings.Contains(string(document), token) {
		t.Error("the document a read answered with carries the token")
	}
	for _, rendered := range []string{fmt.Sprint(live), fmt.Sprintf("%+v", live), fmt.Sprintf("%s", live), fmt.Sprintf("%#v", live)} {
		if strings.Contains(rendered, token) {
			t.Errorf("the value a caller holds renders the token: %s", redact(rendered, token))
		}
		if !strings.Contains(rendered, reg.Addr) {
			t.Errorf("the value a caller holds renders %q, want the address it answers at", rendered)
		}
	}

	// The failure paths too, both of them: a read of an address nothing answers
	// at, and a read the endpoint refuses. Each fails with the address it read
	// and never with the credential it read through.
	t.Run("an error names the address and not the credential", func(t *testing.T) {
		gone := Live{reg: Registry{ID: endpointID, Addr: freeAddr(t), Token: token, Nonce: freshToken(t)}}
		if _, _, err := c.Get(ctx, gone); err == nil {
			t.Fatal("a read of an address nothing answers at answered")
		} else {
			if strings.Contains(err.Error(), token) {
				t.Errorf("the error repeats the credential: %s", redact(err.Error(), token))
			}
			if !strings.Contains(err.Error(), gone.reg.Addr) {
				t.Errorf("the error is %q, want it to name the address it read", redact(err.Error(), token))
			}
		}

		// A credential the endpoint does not know: the refusal says so and says
		// nothing of either value.
		wrong := Live{reg: Registry{ID: endpointID, Addr: reg.Addr, Token: freshToken(t), Nonce: freshToken(t)}}
		if _, _, err := c.Get(ctx, wrong); err == nil {
			t.Fatal("a read with a credential the endpoint does not know was answered")
		} else {
			if !strings.Contains(err.Error(), "refused the read") {
				t.Errorf("the error is %q, want the endpoint's own refusal", redact(err.Error(), token))
			}
			if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), wrong.reg.Token) {
				t.Errorf("the error repeats a credential: %s", redact(err.Error(), token))
			}
		}
	})
}
