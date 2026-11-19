package voicesvc

// The forwarding half's tests, and they are the plan's acceptance list read one
// line at a time:
//
//   - a name with no register row is refused, and so is a name the project's
//     policy does not admit — fail-closed at both ends, in the same direction;
//   - `direction` is enforced per direction, so an audio call is refused where
//     the node admits text only;
//   - the audio is forwarded as it arrives, and nothing is buffered, spooled,
//     retried or written to disk;
//   - no retry against a second provider happens;
//   - the log line is the callLog shape: the identity's own names and no byte
//     of any body;
//   - the key is read from Secret Manager and never appears in an error, a log
//     or an answer.
//
// The provider's transport is a hook in this file and the provider's body a
// reader this file writes, so nothing here opens a socket to a provider, needs
// an account or reads a home directory (Global Constraint 10). The service
// itself is reached over a real loopback socket where the property is a
// property of the socket — a chunk reaching the caller while the provider still
// holds the rest of the answer, a truncated answer not being read as a whole
// one — and in this process where the property is what the handler read.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// These two documents are what the tests read as a project's policy node: one
// engine admitted for the audio direction, which is the state a paired project
// is in, and one admitting every register engine in both directions. The second
// names `whisper` as well, which no register row holds, so the register gate has
// an engine the policy node lists and this service cannot reach.
const (
	forwardPolicySTT = `{"providers":["deepgram"],"direction":{"stt":true}}`
	forwardPolicyAll = `{"providers":["deepgram","google","elevenlabs","whisper"],"direction":{"stt":true,"tts":true}}`
)

// forwardHop is the whole service over the fakes, the real policy reader and a
// provider that is a hook.
type forwardHop struct {
	t        *testing.T
	srv      *Server
	front    *httptest.Server
	key      *testKey
	db       *testDB
	devices  fakeDevices
	keys     *fakeKeys
	tokens   *fakeTokens
	provider *fakeProvider
	log      *logBuffer
}

// newForwardHop builds the hop with a project policy node, which is the
// document written at projectpolicy.PathVoiceRepo(testSlug). An empty document
// is a project with no node at all, which is §4.3's absent case: the shared
// allow-list is then read and, that being absent too, the answer is local.
//
// The policy reader is the REAL one (RTDBVoice over the fake database) and not
// a stand-in, because half of what these tests check is which of §4.3's four
// answers a node produces: an absent node, a force_local one, one nobody can
// decode and one nobody can read are four different refusals.
func newForwardHop(t *testing.T, node string) *forwardHop {
	t.Helper()
	db := newTestDB(t)
	if node != "" {
		db.Set(projectpolicy.PathVoiceRepo(testSlug), json.RawMessage(node))
	}
	enforcer, err := NewEnforcer(RTDBVoice{DB: db.Client})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	key := newTestKey(t)
	devices := fakeDevices{live: map[string]bool{testUID: true}}
	keys := &fakeKeys{keys: map[string]string{
		"deepgram":   testKeyValue,
		"elevenlabs": testKeyValue,
	}}
	tokens := &fakeTokens{token: "the-installations-own-access-token"}
	provider := newFakeProvider()
	log := &logBuffer{}
	srv, err := NewServer(Config{
		Auth:     testAuthenticator(t, key),
		Devices:  devices,
		Enforcer: enforcer,
		Keys:     keys,
		Tokens:   tokens,
		Client:   &http.Client{Transport: provider},
		Log:      slog.New(slog.NewTextHandler(log, nil)),
		Now:      func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	front := httptest.NewServer(srv)
	t.Cleanup(front.Close)
	return &forwardHop{t: t, srv: srv, front: front, key: key, db: db, devices: devices,
		keys: keys, tokens: tokens, provider: provider, log: log}
}

// revoke is what D166's revocation does to the device: the node is gone and the
// next call is refused. The device reader here is the shell's own fake, because
// what a deleted node does to a call is P5.3.3's test; what this file needs is a
// device that is not live.
func (h *forwardHop) revoke() { h.devices.live[testUID] = false }

// token mints a token for the paired device.
func (h *forwardHop) token() string { return h.key.token(h.t, h.key.claims()) }

// noProjectHeader is the sentinel a test passes as the project to send the
// header not at all: the empty string means "the test's own project".
const noProjectHeader = "-"

// forwardCall is one forwarding request a test builds.
type forwardCall struct {
	dir     Direction
	engine  string
	project string
	body    io.Reader
	rawBody string
	ctype   string
	token   string
}

// request builds the call as an HTTP request. It is an http.Request and not an
// httptest one because two of the tests set the body, the Content-Length and the
// headers directly, which is how a caller that declares one length and sends
// another is built.
func (h *forwardHop) request(c forwardCall) *http.Request {
	h.t.Helper()
	prefix := STTPathPrefix
	if c.dir == TTS {
		prefix = TTSPathPrefix
	}
	project := c.project
	if project == "" {
		project = testSlug
	}
	var body io.Reader = c.body
	if body == nil {
		body = strings.NewReader(c.rawBody)
	}
	req, err := http.NewRequest(http.MethodPost, h.front.URL+prefix+c.engine, body)
	if err != nil {
		h.t.Fatalf("building the request: %v", err)
	}
	if project != noProjectHeader {
		req.Header.Set(HeaderProject, project)
	}
	if c.ctype != "" {
		req.Header.Set("Content-Type", c.ctype)
	}
	if c.token != "" {
		req.Header.Set(HeaderAuth, authScheme+c.token)
	}
	return req
}

// do makes one call through the front and returns the answer, which is the
// caller's to close.
func (h *forwardHop) do(c forwardCall) (*http.Response, error) {
	h.t.Helper()
	return h.front.Client().Do(h.request(c))
}

// serve runs one call through the handler in this process, which is where a
// test can see what the handler read: the body is this test's own reader and
// nothing else touches it.
func (h *forwardHop) serve(c forwardCall) *httptest.ResponseRecorder {
	h.t.Helper()
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, h.request(c))
	return rec
}

// serveAsync runs one call on its own goroutine and answers a channel closed
// when the handler returned.
//
// It plays net/http's own part by swallowing the abort a handler ends a
// truncated answer with (http.ErrAbortHandler, forward.go): in production that
// panic is recovered by the connection's own goroutine, and a test that called
// ServeHTTP on its own goroutine without this would take the test binary down
// with it.
func (h *forwardHop) serveAsync(req *http.Request) (<-chan struct{}, *httptest.ResponseRecorder) {
	h.t.Helper()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil && p != http.ErrAbortHandler {
				panic(p)
			}
		}()
		h.srv.ServeHTTP(rec, req)
	}()
	return done, rec
}

// audioCall is the common case: an audio call to deepgram, with a paired device
// and a usable token.
func (h *forwardHop) audioCall(body string) forwardCall {
	return forwardCall{dir: STT, engine: "deepgram", rawBody: body, ctype: "audio/webm", token: h.token()}
}

// textCall is the text direction's common case: elevenlabs, whose register row
// serves it.
func (h *forwardHop) textCall(body string) forwardCall {
	return forwardCall{dir: TTS, engine: "elevenlabs", rawBody: body, token: h.token()}
}

// mustDo makes a call and stops the test when it could not be made.
func mustDo(t *testing.T, h *forwardHop, c forwardCall) *http.Response {
	t.Helper()
	resp, err := h.do(c)
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	return resp
}

// refusal reads a refusal off a response and decodes it.
func refusal(t *testing.T, resp *http.Response) (int, WireError) {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the refusal: %v", err)
	}
	var e WireError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("the refusal is not a wire error (%s): %v", body, err)
	}
	return resp.StatusCode, e
}

// recRefusal reads a refusal off an in-process recorder.
func recRefusal(t *testing.T, rec *httptest.ResponseRecorder) WireError {
	t.Helper()
	var e WireError
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("the refusal is not a wire error (%s): %v", rec.Body.String(), err)
	}
	return e
}

// refusalReason asserts a refusal's reason, and that its status is the one the
// shell maps that reason to (server.go's statusFor, P5.3.3).
//
// The reason is the strong assertion: it is the wire's vocabulary and the thing
// the app branches on (§4.2, §4.5). The status is asserted against the shell's
// one mapping rather than against a number spelled here, because the property
// this file holds the forwarding handler to is that it reuses that mapping and
// does not write a second one. What the mapping itself makes of a policy
// refusal — a 503, for the three reasons above — is a question this task reports
// rather than decides (see the pull request).
func refusalReason(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	code, e := refusal(t, resp)
	if e.Error != want {
		t.Fatalf("the refusal is %q, want %q (%d %s)", e.Error, want, code, e.Message)
	}
	if wantStatus := statusFor(want); code != wantStatus {
		t.Fatalf("the refusal is a %d and the shell maps %s to a %d: the forwarding handler reuses statusFor and does not spell a second mapping of its own", code, want, wantStatus)
	}
}

// TestForwardRefusesAnEngineThisHopDoesNotHold is the register gate: a name with
// no row, and a name whose row serves the other direction, are refused — by the
// SAME reason, so which engines this hop holds is not something a caller can
// enumerate by asking for directions one at a time.
func TestForwardRefusesAnEngineThisHopDoesNotHold(t *testing.T) {
	h := newForwardHop(t, forwardPolicyAll)

	for _, tc := range []struct {
		name    string
		dir     Direction
		engine  string
		wantWhy string
		because string
	}{
		{"a provider the register does not hold", STT, "whisper", ReasonUnknownEngine,
			"the policy node lists it and the register does not, and the register is what says which engines exist"},
		{"a provider nobody could read the terms of", STT, "openai", ReasonUnknownEngine,
			"§7 read no terms for it on the read date, so a row would claim what nobody read"},
		{"a direction the row does not serve", TTS, "deepgram", ReasonUnknownEngine,
			"it is an audio engine here, and the same reason as an unknown name keeps the two indistinguishable"},
		{"a direction the row does not serve, the other way", STT, "google", ReasonUnknownEngine,
			"Google's streaming audio path is the v2 gRPC API and this hop has no HTTP audio call for it"},
		{"a name that is a traversal", STT, "deepgram/../../x", ReasonBadRequest,
			"the tail is bounded by the register's own name pattern before anything looks it up"},
		{"a name with a slash", STT, "a/b", ReasonBadRequest, "a name is never a path"},
		{"an empty name", STT, "", ReasonBadRequest, "the route is <engine> and the engine is a name"},
		{"a name in capitals", STT, "Deepgram", ReasonBadRequest,
			"an engine name is lowercase by the policy's own rule, so a name that differs in case is not one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := h.audioCall("audio")
			c.dir, c.engine = tc.dir, tc.engine
			if tc.dir == TTS {
				c.ctype = ""
			}
			code, e := refusal(t, mustDo(t, h, c))
			if e.Error != tc.wantWhy {
				t.Fatalf("the refusal is %q, want %q (%d %s): %s", e.Error, tc.wantWhy, code, e.Message, tc.because)
			}
			if n := h.provider.calls(); n != 0 {
				t.Fatalf("the register gate let %d provider calls through: the gate decides the request and not the call", n)
			}
			if n := h.keys.callCount(); n != 0 {
				t.Fatalf("a refused call read the provider key %d times: the key is read only once the call is going to be made", n)
			}
		})
	}
}

// TestForwardRefusesAnEngineThePolicyDoesNotAdmit is the other end of the
// fail-closed pair: the register holds the engine and the project's node does
// not admit it, and the refusal is the policy's own.
func TestForwardRefusesAnEngineThePolicyDoesNotAdmit(t *testing.T) {
	// The node admits elevenlabs for text and nothing for audio. Both engines
	// are in the register, so what decides is the node.
	h := newForwardHop(t, `{"providers":["elevenlabs"],"direction":{"tts":true}}`)

	t.Run("the text call the node admits", func(t *testing.T) {
		resp := mustDo(t, h, h.textCall("the text"))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("the admitted call answered %d: %s", resp.StatusCode, body)
		}
		if n := h.provider.calls(); n != 1 {
			t.Fatalf("the admitted call made %d provider calls, want 1", n)
		}
	})

	t.Run("the audio call the node does not admit", func(t *testing.T) {
		refusalReason(t, mustDo(t, h, h.audioCall("audio")), ReasonNotAdmitted)
		if n := h.provider.calls(); n != 1 {
			t.Fatalf("the refused call reached a provider: %d calls in total, want only the admitted one", n)
		}
	})

	t.Run("an engine the node does not list", func(t *testing.T) {
		// The register holds google for the text direction and the node lists
		// elevenlabs alone: the refusal is the node's own and not the register
		// gate's, which is the distinction the app's indicator reads.
		refusalReason(t, mustDo(t, h, forwardCall{dir: TTS, engine: "google", rawBody: "the text", token: h.token()}), ReasonNotAdmitted)
	})

	t.Run("a project the installation forbids remote for", func(t *testing.T) {
		h2 := newForwardHop(t, `{"providers":["deepgram"],"direction":{"stt":true},"force_local":true}`)
		refusalReason(t, mustDo(t, h2, h2.audioCall("audio")), ReasonForceLocal)
		if n := h2.provider.calls(); n != 0 {
			t.Fatalf("a force_local project reached a provider %d times: force_local wins over every other member of the node", n)
		}
	})

	t.Run("a node nothing could read", func(t *testing.T) {
		// An unknown member: the node exists, the hop's reader refuses to decode
		// it, and §4.3's rule is that this is the local answer and not the
		// shared list's.
		h3 := newForwardHop(t, `{"providers":["deepgram"],"direction":{"stt":true},"carried_forward":true}`)
		refusalReason(t, mustDo(t, h3, h3.audioCall("audio")), ReasonPolicyUnreadable)
	})

	t.Run("a node the database would not give up", func(t *testing.T) {
		h4 := newForwardHop(t, forwardPolicySTT)
		h4.db.DenyRead(projectpolicy.PathVoiceRepo(testSlug))
		refusalReason(t, mustDo(t, h4, h4.audioCall("audio")), ReasonStateUnavailable)
		if n := h4.provider.calls(); n != 0 {
			t.Fatalf("a call whose policy could not be read reached a provider %d times", n)
		}
	})

	t.Run("a project nothing admits", func(t *testing.T) {
		h5 := newForwardHop(t, "")
		refusalReason(t, mustDo(t, h5, h5.audioCall("audio")), ReasonNoAdmission)
	})
}

// TestForwardEnforcesTheDirectionPerDirection is the plan's own sentence: an
// audio call is refused where the node admits text only, and the text call the
// same node admits goes through. The two switches are two different privacy
// propositions (§3), and each is read on its own.
func TestForwardEnforcesTheDirectionPerDirection(t *testing.T) {
	// Both engines are in the register and both are listed by the node, so the
	// only thing that can decide is the direction switch.
	h := newForwardHop(t, `{"providers":["deepgram","elevenlabs"],"direction":{"tts":true}}`)

	t.Run("the audio call the node does not admit", func(t *testing.T) {
		refusalReason(t, mustDo(t, h, h.audioCall("audio")), ReasonNotAdmitted)
	})

	t.Run("the text call the same node admits", func(t *testing.T) {
		resp := mustDo(t, h, h.textCall("the text"))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("the admitted call answered %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("the mirror image", func(t *testing.T) {
		// The other node: the audio direction on and the text direction off. If
		// the two switches were read as one, exactly one of the two nodes would
		// behave differently and this test would fail on it.
		h2 := newForwardHop(t, `{"providers":["deepgram","elevenlabs"],"direction":{"stt":true,"tts":false}}`)
		refusalReason(t, mustDo(t, h2, h2.textCall("the text")), ReasonNotAdmitted)
		resp := mustDo(t, h2, h2.audioCall("audio"))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("the admitted audio call answered %d: %s", resp.StatusCode, body)
		}
	})
}

// TestForwardRefusesTheCallerItDoesNotKnow walks the chain's first steps: the
// token and the device node come before everything else, including before the
// register gate — a request that names an engine this hop does not hold is still
// a request from somebody, and who that is decides first.
func TestForwardRefusesTheCallerItDoesNotKnow(t *testing.T) {
	h := newForwardHop(t, forwardPolicySTT)

	for _, tc := range []struct {
		name     string
		mutate   func(c *forwardCall)
		wantCode int
		wantWhy  string
		because  string
	}{
		{"no token", func(c *forwardCall) { c.token = "" }, http.StatusUnauthorized, ReasonTokenUnverified, ""},
		{"no token, and an engine this hop does not hold", func(c *forwardCall) { c.token, c.engine = "", "whisper" },
			http.StatusUnauthorized, ReasonTokenUnverified,
			"the token is checked before the register, so a caller that presents none cannot probe which engines this service holds"},
		{"a token of another service", func(c *forwardCall) { c.token = "not.a.token" }, http.StatusUnauthorized, ReasonTokenUnverified, ""},
		{"a run identity", func(c *forwardCall) { c.token = h.key.token(t, uidClaims(h.key, testRunUID)) },
			http.StatusForbidden, ReasonNotADevice, "a run is not a device, and this service is not one a run may reach"},
		{"a project that is not a slug", func(c *forwardCall) { c.project = "not-a-slug" }, http.StatusBadRequest, ReasonBadRequest,
			"the slug is what the policy node is written under, so a request that names none is not a request about a project"},
		{"no project header at all", func(c *forwardCall) { c.project = noProjectHeader }, http.StatusBadRequest, ReasonBadRequest, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := h.audioCall("audio")
			tc.mutate(&c)
			code, e := refusal(t, mustDo(t, h, c))
			if code != tc.wantCode || e.Error != tc.wantWhy {
				t.Fatalf("the refusal is %d %s, want %d %s: %s", code, e.Error, tc.wantCode, tc.wantWhy, tc.because)
			}
			if n := h.provider.calls(); n != 0 {
				t.Fatalf("a refused call reached a provider %d times", n)
			}
			if n := h.keys.callCount(); n != 0 {
				t.Fatalf("a refused call read the provider key %d times", n)
			}
		})
	}

	t.Run("a device that was revoked", func(t *testing.T) {
		h2 := newForwardHop(t, forwardPolicySTT)
		h2.revoke()
		code, e := refusal(t, mustDo(t, h2, h2.audioCall("audio")))
		if code != http.StatusForbidden || e.Error != ReasonDeviceUnknown {
			t.Fatalf("the refusal is %d %s, want 403 %s: a revoked device is refused whatever it asked (D166)", code, e.Error, ReasonDeviceUnknown)
		}
		if n := h2.provider.calls(); n != 0 {
			t.Fatalf("a revoked device reached a provider %d times", n)
		}
	})

	t.Run("a GET of a forwarding route", func(t *testing.T) {
		req := h.request(h.audioCall("audio"))
		req.Method = http.MethodGet
		resp, err := h.front.Client().Do(req)
		if err != nil {
			t.Fatalf("the call failed: %v", err)
		}
		code, e := refusal(t, resp)
		if code != http.StatusMethodNotAllowed || e.Error != ReasonBadRequest {
			t.Fatalf("the refusal is %d %s, want 405 %s", code, e.Error, ReasonBadRequest)
		}
	})

	t.Run("a path this service does not serve", func(t *testing.T) {
		// A provider's own path: §8's rule is that this service resolves a NAME
		// and never an address, so a caller cannot make it call one.
		req, err := http.NewRequest(http.MethodPost, h.front.URL+"/v1/audio/transcriptions", nil)
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}
		resp, err := h.front.Client().Do(req)
		if err != nil {
			t.Fatalf("the call failed: %v", err)
		}
		code, e := refusal(t, resp)
		if code != http.StatusNotFound || e.Error != ReasonNotFound {
			t.Fatalf("the refusal is %d %s, want 404 %s", code, e.Error, ReasonNotFound)
		}
	})
}

// TestForwardRefusesBeforeItReadsTheBody: a caller that will be refused for its
// identity, its engine or its project has not had a byte of its payload read. It
// matters most in the audio direction, where the payload is the caller's voice.
//
// The call is run in this process so the count is exact: the body is this test's
// own reader and only the handler can read it.
func TestForwardRefusesBeforeItReadsTheBody(t *testing.T) {
	h := newForwardHop(t, forwardPolicySTT)

	for _, tc := range []struct {
		name     string
		mutate   func(c *forwardCall)
		wantCode int
		wantWhy  string
	}{
		{"no token", func(c *forwardCall) { c.token = "" }, http.StatusUnauthorized, ReasonTokenUnverified},
		{"an engine this hop does not hold", func(c *forwardCall) { c.engine = "whisper" }, http.StatusBadRequest, ReasonUnknownEngine},
		{"a project that is not a slug", func(c *forwardCall) { c.project = "not-a-slug" }, http.StatusBadRequest, ReasonBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &countingReader{r: strings.NewReader(strings.Repeat("a", 4096))}
			c := forwardCall{dir: STT, engine: "deepgram", body: body, ctype: "audio/webm", token: h.token()}
			tc.mutate(&c)
			rec := h.serve(c)
			if rec.Code != tc.wantCode || recRefusal(t, rec).Error != tc.wantWhy {
				t.Fatalf("the refusal is %d %s, want %d %s", rec.Code, rec.Body.String(), tc.wantCode, tc.wantWhy)
			}
			if n := body.reads(); n != 0 {
				t.Fatalf("the refusal read the caller's payload (%d reads): the checks that decide an identity come before the payload is touched, and in this direction the payload is the caller's voice", n)
			}
			if n := h.provider.calls(); n != 0 {
				t.Fatalf("the refusal reached a provider %d times", n)
			}
		})
	}
}

// TestForwardForwardsTheAudioAsItArrives: the caller's body goes on to the
// provider as the provider reads it, and nothing of this service holds it.
//
// The caller's reader will not produce its second half until the provider's
// transport has read the first, so a hop that collected the body into memory
// before making the call — or spooled it anywhere — never gets the second half,
// and this test fails with that name rather than passing quietly.
func TestForwardForwardsTheAudioAsItArrives(t *testing.T) {
	h := newForwardHop(t, forwardPolicySTT)
	body := &gatedBody{
		first:   []byte("the first half of the recording"),
		second:  []byte(" and the second half"),
		release: h.provider.firstBodyRead(),
	}
	resp := mustDo(t, h, forwardCall{dir: STT, engine: "deepgram", body: body, ctype: "audio/webm", token: h.token()})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		answer, _ := io.ReadAll(resp.Body)
		t.Fatalf("the call answered %d: %s", resp.StatusCode, answer)
	}
	want := string(body.first) + string(body.second)
	if sent := string(h.provider.lastBody()); sent != want {
		t.Fatalf("the provider received %q, want the caller's own bytes (%q) and nothing else", sent, want)
	}
	// And it arrived in more than one piece: the second half existed only
	// because the provider's transport had already read the first, so a hop that
	// had collected the body could not have produced it at all.
	if n := h.provider.bodyReads(); n < 2 {
		t.Fatalf("the caller's body reached the provider in %d reads, want at least 2: the audio is forwarded as it arrives and not in one collected piece", n)
	}
}

// TestForwardSendsExactlyOneRequestAndNeverRetries: §4.5's row. A provider that
// fails is not tried again, and no second provider is tried at all — one call,
// one engine, once. A retry against another provider is the same speech to a
// second third party.
func TestForwardSendsExactlyOneRequestAndNeverRetries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer providerAnswer
	}{
		{"the provider refuses the credential", providerAnswer{status: http.StatusUnauthorized, body: "an echo of the credential: " + testKeyValue}},
		{"the provider is out of quota", providerAnswer{status: http.StatusTooManyRequests, body: "slow down"}},
		{"the provider is broken", providerAnswer{status: http.StatusInternalServerError, body: "boom"}},
		{"the provider cannot be reached", providerAnswer{err: errors.New("dial tcp: the provider is unreachable")}},
		{"the provider's answer fails mid-stream", providerAnswer{status: http.StatusOK, chunks: []string{"half a sentence"}, bodyErr: errors.New("the connection reset")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newForwardHop(t, forwardPolicySTT)
			h.provider.setAnswer(tc.answer)
			resp, err := h.do(h.audioCall("audio"))
			if err == nil {
				resp.Body.Close()
			}
			if n := h.provider.calls(); n != 1 {
				t.Fatalf("the call reached a provider %d times, want exactly 1: nothing here retries, and nothing falls over to a second provider", n)
			}
			if n := h.keys.callCount(); n != 1 {
				t.Fatalf("the key was read %d times for one call, want exactly 1", n)
			}
		})
	}
}

// TestForwardNeverRelaysAProvidersFailure: a provider's failure body is never
// read and never relayed. It may echo the key this service presented, and the
// app's answer to a failed call is the local engine either way (§4.5).
func TestForwardNeverRelaysAProvidersFailure(t *testing.T) {
	const marker = "MARKER-the-provider-echoed-the-request"
	h := newForwardHop(t, forwardPolicySTT)
	h.provider.setAnswer(providerAnswer{
		status: http.StatusBadRequest,
		body:   marker + " and the credential " + testKeyValue,
	})
	code, e := refusal(t, mustDo(t, h, h.audioCall("audio")))
	if code != http.StatusBadGateway || e.Error != ReasonProviderFailed {
		t.Fatalf("the refusal is %d %s, want 502 %s", code, e.Error, ReasonProviderFailed)
	}
	if strings.Contains(e.Message, marker) || strings.Contains(e.Message, testKeyValue) {
		t.Fatalf("the refusal carries the provider's body: %s", e.Message)
	}
	if n := h.provider.failureBodyReads(); n != 0 {
		t.Fatalf("the provider's failure body was read %d times: it may echo the credential, so it is never read at all", n)
	}
	logged := h.log.String()
	if strings.Contains(logged, marker) || strings.Contains(logged, testKeyValue) {
		t.Fatalf("the log carries the provider's failure body:\n%s", logged)
	}
	// The log DOES carry what an operator reads a failed provider call from: the
	// status the provider answered with, the engine, and the reason the app
	// branches on.
	for _, want := range []string{"provider_status=400", "provider=deepgram", "reason=" + ReasonProviderFailed} {
		if !strings.Contains(logged, want) {
			t.Fatalf("the log line is missing %q:\n%s", want, logged)
		}
	}
}

// TestForwardKeepsTheKeyOutOfTheAnswerAndTheLog: §4.4 point 4, from the wire
// side. A provider that echoes the credential it was handed — in the body, in a
// header of its own — must not be able to hand it to the phone through this
// service, and it must not reach a log line either.
func TestForwardKeepsTheKeyOutOfTheAnswerAndTheLog(t *testing.T) {
	h := newForwardHop(t, forwardPolicySTT)
	h.provider.setAnswer(providerAnswer{
		status: http.StatusOK,
		body:   "the answer begins " + testKeyValue + " and ends",
		headers: http.Header{
			"Content-Type":     {"audio/mpeg"},
			"X-Echoed-Key":     {testKeyValue},
			"X-Request-Id":     {"a-request-id-that-identifies-the-account"},
			"X-Account-Region": {"a-region"},
		},
	})
	resp := mustDo(t, h, h.audioCall("audio"))
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	if strings.Contains(string(body), testKeyValue) {
		t.Fatalf("the credential reached the caller in the answer: %s", body)
	}
	if !strings.Contains(string(body), redacted) {
		t.Fatalf("the answer is %q, want the credential replaced with %s", body, redacted)
	}
	for _, hdr := range []string{"X-Echoed-Key", "X-Request-Id", "X-Account-Region"} {
		if got := resp.Header.Get(hdr); got != "" {
			t.Fatalf("the header %s reached the caller: %q", hdr, got)
		}
	}
	if got := resp.Header.Get("Content-Type"); got != "audio/mpeg" {
		t.Fatalf("the media type is %q, want the provider's own audio/mpeg: it is what says whether the bytes the phone is about to read are MP3, OGG or a JSON error", got)
	}
	// The credential did reach the PROVIDER, in exactly one header of a request
	// that carries exactly two: the payload's media type and the credential, and
	// nothing of the caller's.
	if got := h.provider.lastAuth().Get(HeaderAuth); got != "Token "+testKeyValue {
		t.Fatalf("the provider was presented %q, want the credential in one header", got)
	}
	if n := len(h.provider.lastAuth()); n != 2 {
		t.Fatalf("the call carried %d headers (%v), want exactly 2", n, h.provider.lastAuth())
	}
	if n := h.keys.callCount(); n != 1 {
		t.Fatalf("the key was read %d times for one call, want once per call and never per chunk", n)
	}
	if strings.Contains(h.log.String(), testKeyValue) {
		t.Fatalf("the log line carries the key:\n%s", h.log.String())
	}
}

// TestForwardStreamsTheAnswerBack: the answer reaches the caller while the
// provider still holds the rest of it. It is the text direction — a reply spoken
// as it comes — and the audio direction's answer has the same property.
func TestForwardStreamsTheAnswerBack(t *testing.T) {
	h := newForwardHop(t, `{"providers":["elevenlabs"],"direction":{"tts":true}}`)
	first := "the first sentence of the reply"
	rest := " and the rest of it"
	h.provider.setAnswer(providerAnswer{
		status:  http.StatusOK,
		chunks:  []string{first, rest},
		gate:    make(chan struct{}),
		headers: http.Header{"Content-Type": {"audio/mpeg"}},
	})
	resp := mustDo(t, h, h.textCall("the text"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("the call answered %d: %s", resp.StatusCode, body)
	}
	got := make([]byte, len(first))
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(resp.Body, got)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the first chunk could not be read: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the first chunk did not reach the caller while the provider still held the rest of the answer: the hop is buffering the reply instead of streaming it, so a phone hears a turn only once the provider has finished writing it")
	}
	if string(got) != first {
		t.Fatalf("the first chunk is %q, want %q", got, first)
	}
	h.provider.release()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("the rest of the answer could not be read: %v", err)
	}
	if string(body) != rest {
		t.Fatalf("the rest of the answer is %q, want %q", body, rest)
	}
}

// TestForwardAbortsATruncatedAnswer: half a sentence played as the whole reply is
// worse than a failed call, which the app answers by speaking locally. So a
// provider's stream that fails mid-answer aborts the response — the caller reads
// a truncated transfer and not a short answer — and the log says which end
// stopped the copy.
func TestForwardAbortsATruncatedAnswer(t *testing.T) {
	h := newForwardHop(t, forwardPolicySTT)
	h.provider.setAnswer(providerAnswer{
		status:  http.StatusOK,
		chunks:  []string{"half a sentence of audio"},
		bodyErr: errors.New("the connection reset"),
	})
	resp := mustDo(t, h, h.audioCall("audio"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the response's head is %d, want 200: the truncation happens after it", resp.StatusCode)
	}
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("the caller read a truncated answer as a whole one: the response must be aborted, in net/http's own way for a proxy that could not copy a body")
	}
	logged := h.log.String()
	if !strings.Contains(logged, "cut="+cutProvider) {
		t.Fatalf("the log line does not say the provider's end stopped the copy:\n%s", logged)
	}
	if strings.Contains(logged, "half a sentence") {
		t.Fatalf("the log line carries the answer's bytes:\n%s", logged)
	}
	// A cut is a fact about the call and not a refusal: the caller was answered
	// 200 and the log says so, because some of the answer was already on its way
	// when the copy stopped.
	if !strings.Contains(logged, "status=200") {
		t.Fatalf("the log line reports the call as something other than the 200 it answered with:\n%s", logged)
	}
}

// TestForwardEndsTheProviderCallWhenTheCallerGoesAway: a caller that stopped
// listening must not leave this service reading a stream nobody will hear — this
// service's own cost, and audio nobody asked for. The caller's context is the
// only thing that ends the provider's call: this service sets no deadline of its
// own, because the provider's own end of the body is what says a call is over.
func TestForwardEndsTheProviderCallWhenTheCallerGoesAway(t *testing.T) {
	h := newForwardHop(t, forwardPolicySTT)
	h.provider.setAnswer(providerAnswer{
		status: http.StatusOK,
		chunks: []string{"the beginning of an answer nobody will hear", "and the rest"},
		gate:   make(chan struct{}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, _ := h.serveAsync(h.request(h.audioCall("audio")).WithContext(ctx))
	// The provider's call has to exist first: a context cancelled before the hop
	// reached the provider would prove that the hop never called one, which is a
	// different and weaker claim than this test's.
	waitForCalls(t, h.provider, 1)
	cancel()
	for _, tc := range []struct {
		name string
		got  <-chan struct{}
		why  string
	}{
		{"the provider's own call", h.provider.contextDone(),
			"a caller that stopped listening must not leave this service reading a stream nobody will hear"},
		{"the handler", done, "the handler was still copying the provider's answer after the caller went away"},
	} {
		select {
		case <-tc.got:
		case <-time.After(30 * time.Second):
			t.Fatalf("%s was still going after the caller went away: %s", tc.name, tc.why)
		}
	}
}

// TestForwardRefusesABodyOverItsBound: the audio direction's bound is enforced
// while the body is forwarded, because the caller's Content-Length is only a
// claim. Both routes to it are checked: the declared one, which needs no read at
// all, and the body that declares nothing and sends more than the bound.
func TestForwardRefusesABodyOverItsBound(t *testing.T) {
	h := newForwardHop(t, forwardPolicySTT)

	t.Run("a declared length over the bound", func(t *testing.T) {
		req := h.request(h.audioCall("audio"))
		req.ContentLength = maxAudioBytes + 1
		rec := httptest.NewRecorder()
		h.srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || recRefusal(t, rec).Error != ReasonBadRequest {
			t.Fatalf("the refusal is %d %s, want 400", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "bound") {
			t.Fatalf("the refusal does not say what was over the bound: %s", rec.Body.String())
		}
		if n := h.provider.calls(); n != 0 {
			t.Fatalf("an oversized declared body reached a provider %d times", n)
		}
	})

	t.Run("a body that declares nothing and sends more", func(t *testing.T) {
		// A caller's claim is unknown here, so the only way this service can
		// find out is by counting what it forwards — and it refuses mid-body,
		// which is what makes the bound a bound rather than an honour system.
		c := h.audioCall("")
		c.body = io.LimitReader(neverEnding{}, maxAudioBytes+64)
		rec := h.serve(c)
		if rec.Code != http.StatusBadRequest || recRefusal(t, rec).Error != ReasonBadRequest {
			t.Fatalf("the refusal is %d %s, want 400: the bound is enforced while the body is forwarded", rec.Code, rec.Body.String())
		}
	})

	t.Run("a body of exactly the bound is forwarded", func(t *testing.T) {
		// The other side of the same rule: the bound is the largest body this
		// service forwards and not the first one it refuses.
		c := h.audioCall("")
		c.body = io.LimitReader(neverEnding{}, maxAudioBytes)
		rec := h.serve(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("a body of exactly the bound was answered %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a text body over the bound", func(t *testing.T) {
		h2 := newForwardHop(t, `{"providers":["elevenlabs"],"direction":{"tts":true}}`)
		rec := h2.serve(forwardCall{dir: TTS, engine: "elevenlabs", rawBody: strings.Repeat("a", maxTextBytes+1), token: h2.token()})
		if rec.Code != http.StatusBadRequest || recRefusal(t, rec).Error != ReasonBadRequest {
			t.Fatalf("the refusal is %d %s, want 400", rec.Code, rec.Body.String())
		}
	})
}

// TestForwardRefusesAPayloadOfTheWrongKind: the audio direction's body must be
// audio of a container the caller names, and the text direction's must be text.
// Each refusal is the request's own and no provider is reached.
func TestForwardRefusesAPayloadOfTheWrongKind(t *testing.T) {
	h := newForwardHop(t, forwardPolicyAll)

	for _, tc := range []struct {
		name    string
		c       forwardCall
		because string
	}{
		{"audio with no media type", forwardCall{dir: STT, engine: "deepgram", rawBody: "audio"},
			"a provider reads the bytes by the type, so a call without one is not a call"},
		{"audio with a text media type", forwardCall{dir: STT, engine: "deepgram", rawBody: "audio", ctype: "text/plain"}, ""},
		{"audio with a JSON media type", forwardCall{dir: STT, engine: "deepgram", rawBody: "audio", ctype: "application/json"}, ""},
		{"text that is empty", forwardCall{dir: TTS, engine: "elevenlabs", rawBody: ""},
			"a body of no characters would be a provider call with nothing to speak"},
		{"text that is not UTF-8", forwardCall{dir: TTS, engine: "elevenlabs", rawBody: "\xff\xfe the text"},
			"the text goes into a JSON document a provider parses, and this service does not hand the caller's bytes on with a guess about what they were"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.c.token = h.token()
			rec := h.serve(tc.c)
			if rec.Code != http.StatusBadRequest || recRefusal(t, rec).Error != ReasonBadRequest {
				t.Fatalf("the refusal is %d %s, want 400: %s", rec.Code, rec.Body.String(), tc.because)
			}
			if n := h.provider.calls(); n != 0 {
				t.Fatalf("a payload of the wrong kind reached a provider %d times", n)
			}
		})
	}
}

// TestForwardRefusesAMediaTypeItCannotFold: the media type is rebuilt from the
// parsed type before it goes into an outbound header, so a caller cannot write a
// second header or another parameter through it. The header is set on the
// request directly, because net/http's own client refuses to send one of these
// at all — which is a second line of defence and not this one.
func TestForwardRefusesAMediaTypeItCannotFold(t *testing.T) {
	h := newForwardHop(t, forwardPolicySTT)
	for _, ctype := range []string{"audio/webm\r\nX-Evil: 1", "audio/webm\nX-Evil: 1", "audio/webm\x00"} {
		req := h.request(h.audioCall("audio"))
		req.Header.Set("Content-Type", ctype)
		rec := httptest.NewRecorder()
		h.srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || recRefusal(t, rec).Error != ReasonBadRequest {
			t.Errorf("the media type %q was answered %d %s, want 400", ctype, rec.Code, rec.Body.String())
		}
	}
	if n := h.provider.calls(); n != 0 {
		t.Fatalf("a media type this service could not fold reached a provider %d times", n)
	}
}

// TestForwardReadsTheKeyOnlyOnceItIsGoingToCall: the credential is resolved
// after the policy has admitted the call, so a refused call never touches the
// key at all — and a credential that cannot be read is this service's state and
// not a refusal of the caller.
func TestForwardReadsTheKeyOnlyOnceItIsGoingToCall(t *testing.T) {
	t.Run("a call the policy refuses", func(t *testing.T) {
		h := newForwardHop(t, `{"providers":["elevenlabs"],"direction":{"tts":true},"force_local":true}`)
		refusalReason(t, mustDo(t, h, h.textCall("the text")), ReasonForceLocal)
		if n := h.keys.callCount(); n != 0 {
			t.Fatalf("a refused call read the provider key %d times", n)
		}
	})

	t.Run("a key that cannot be read", func(t *testing.T) {
		h := newForwardHop(t, forwardPolicySTT)
		h.keys.err = errors.New("the provider-key container could not be read")
		refusalReason(t, mustDo(t, h, h.audioCall("audio")), ReasonStateUnavailable)
		if n := h.provider.calls(); n != 0 {
			t.Fatalf("a call with no credential reached a provider %d times: a provider handed an empty credential answers 401, which reads as the wrong failure", n)
		}
	})

	t.Run("a key the document does not name", func(t *testing.T) {
		h := newForwardHop(t, forwardPolicySTT)
		delete(h.keys.keys, "deepgram")
		refusalReason(t, mustDo(t, h, h.audioCall("audio")), ReasonStateUnavailable)
		if n := h.provider.calls(); n != 0 {
			t.Fatalf("a call with no credential reached a provider %d times", n)
		}
	})

	t.Run("the engine reached as the installation itself", func(t *testing.T) {
		// Google's API is one an installation can already reach with its own
		// identity, so this engine presents this service's own token and no
		// provider key of it exists at all.
		h := newForwardHop(t, `{"providers":["google"],"direction":{"tts":true}}`)
		resp := mustDo(t, h, forwardCall{dir: TTS, engine: "google", rawBody: "the text", token: h.token()})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("the call answered %d: %s", resp.StatusCode, body)
		}
		if n := h.keys.callCount(); n != 0 {
			t.Fatalf("the engine reached as the installation's own identity read a provider key %d times", n)
		}
		if got := h.provider.lastAuth().Get(HeaderAuth); got != authScheme+h.tokens.token {
			t.Fatalf("the provider was presented %q, want this service's own token", got)
		}
	})

	t.Run("no token to mint", func(t *testing.T) {
		h := newForwardHop(t, `{"providers":["google"],"direction":{"tts":true}}`)
		h.tokens.err = errors.New("the metadata server is unreachable")
		refusalReason(t, mustDo(t, h, forwardCall{dir: TTS, engine: "google", rawBody: "the text", token: h.token()}), ReasonStateUnavailable)
		if n := h.provider.calls(); n != 0 {
			t.Fatalf("a call with no token of this service's own reached a provider %d times", n)
		}
	})
}

// TestForwardLogsTheCallLogShape is §4.4 point 6: one line per call, carrying
// the act, the names this service already knows and the outcome, and no byte of
// any body. The markers are put where content would be if content were logged.
func TestForwardLogsTheCallLogShape(t *testing.T) {
	const textMarker = "MARKER-the-reply-text"
	const audioMarker = "MARKER-the-recording"
	h := newForwardHop(t, forwardPolicyAll)
	tok := h.token()
	h.provider.setAnswer(providerAnswer{status: http.StatusOK, body: "the answer"})

	for _, c := range []forwardCall{
		{dir: STT, engine: "deepgram", rawBody: audioMarker, ctype: "audio/webm", token: tok},
		{dir: TTS, engine: "elevenlabs", rawBody: textMarker, token: tok},
	} {
		resp := mustDo(t, h, c)
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Fatalf("reading the answer: %v", err)
		}
		resp.Body.Close()
	}
	logged := h.log.String()
	// slog's text handler quotes a value that needs it, so the assertions are
	// made against the line with its quotes removed.
	flat := strings.ReplaceAll(logged, `"`, "")
	for _, want := range []string{
		"act=voice forward",
		"device=" + testUID,
		"project=" + testSlug,
		"provider=deepgram",
		"provider=elevenlabs",
		"direction=stt",
		"direction=tts",
		"provider_status=200",
		"bytes=",
		"status=200",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the log line is missing %q:\n%s", want, logged)
		}
	}
	for _, marker := range []string{audioMarker, textMarker, tok, testKeyValue, "the answer"} {
		if strings.Contains(flat, marker) {
			t.Errorf("a log line carries %q:\n%s", marker, logged)
		}
	}
	// One line per call and no more, and the device is on both of them.
	if n := strings.Count(logged, "voice call"); n != 2 {
		t.Errorf("the log holds %d lines for two calls:\n%s", n, logged)
	}
	if n := strings.Count(flat, "device="+testUID); n != 2 {
		t.Errorf("the log carries the device on %d of the two lines:\n%s", n, logged)
	}
}

// TestTheForwardingHalfWritesNothingToDisk is the half of the plan's sentence
// that no other test in this file can see: "nothing is buffered, spooled,
// retried or written to disk".
//
// What is checked is the package's own sources and not a run, because a
// temporary file is a property of the code and not of a call: the forwarding
// half may not reach for a file at all, and the markers are the ways it could.
// `mime/multipart` is among them for the reason it is easy to miss — a multipart
// writer spills a large part to a temporary file of its own — and `os` and
// `path/filepath` are the imports any of the others would need first.
func TestTheForwardingHalfWritesNothingToDisk(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading this package's directory: %v", err)
	}
	markers := []string{
		"os.Create", "os.OpenFile", "os.WriteFile", "os.MkdirTemp", "os.CreateTemp",
		"TempFile(", "TempDir(", `"os"`, `"path/filepath"`, `"io/ioutil"`, `"mime/multipart"`,
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		checked++
		for _, marker := range markers {
			if strings.Contains(string(src), marker) {
				t.Errorf("%s carries %q: the forwarding half holds a live stream and writes nothing — no temporary file, no spool and no buffer", name, marker)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no source file was scanned, so this proves nothing")
	}
}

// logBuffer is the log's destination: a bytes.Buffer with a lock, because the
// service writes to it while a test reads it (a streaming call's line is written
// after the caller has read the answer).
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// fakeKeys is a ProviderKeys a test decides by hand.
type fakeKeys struct {
	mu    sync.Mutex
	keys  map[string]string
	err   error
	calls []string
}

func (f *fakeKeys) Key(ctx context.Context, provider string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, provider)
	if f.err != nil {
		return "", f.err
	}
	key, ok := f.keys[provider]
	if !ok {
		return "", fmt.Errorf("%w: the document names no key for %s", ErrKeyUnavailable, provider)
	}
	return key, nil
}

func (f *fakeKeys) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeTokens is a TokenSource a test decides by hand.
type fakeTokens struct {
	token string
	err   error
}

func (f *fakeTokens) Token(ctx context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.token, nil
}

// providerAnswer is what the fake provider answers with.
type providerAnswer struct {
	// status is the HTTP status, and 0 is 200.
	status int
	// body is the whole answer, sent as one chunk. It is what the cases that are
	// not about chunking use.
	body string
	// chunks is the answer sent one chunk per read, and gate holds every chunk
	// after the first until it is released.
	chunks []string
	gate   chan struct{}
	// bodyErr makes the body fail after its chunks, as a provider whose stream
	// broke mid-answer does.
	bodyErr error
	// headers are the provider's answer headers.
	headers http.Header
	// err makes the call fail before it is answered, as a transport error does.
	err error
}

// fakeProvider is the provider's transport: an http.RoundTripper that records
// what it was sent and answers what the test told it to. Nothing in this file
// opens a socket to a provider.
type fakeProvider struct {
	mu     sync.Mutex
	answer providerAnswer
	// first is closed by the first read of a request body, which is what a test
	// uses to let a caller's own reader produce its second half.
	first    chan struct{}
	firstOne sync.Once
	requests []*http.Request
	bodies   [][]byte
	reads    int // reads of a request body, in total
	failRead int // reads of a FAILURE answer's body
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{first: make(chan struct{})}
}

func (p *fakeProvider) setAnswer(a providerAnswer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answer = a
}

// release lets the provider's answer go on past its first chunk.
func (p *fakeProvider) release() {
	p.mu.Lock()
	gate := p.answer.gate
	p.mu.Unlock()
	if gate != nil {
		close(gate)
	}
}

// firstBodyRead is the channel closed by the first read of a request body.
func (p *fakeProvider) firstBodyRead() <-chan struct{} { return p.first }

func (p *fakeProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *fakeProvider) lastBody() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.bodies) == 0 {
		return nil
	}
	return p.bodies[len(p.bodies)-1]
}

func (p *fakeProvider) bodyReads() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reads
}

func (p *fakeProvider) failureBodyReads() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failRead
}

// lastAuth is the last call's request headers, which is where a test reads what
// credential was presented and what else travelled with it.
func (p *fakeProvider) lastAuth() http.Header {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return http.Header{}
	}
	return p.requests[len(p.requests)-1].Header
}

// contextDone is closed when the last call's context is cancelled. It answers a
// closed channel when no call was made, so a test that asks too early fails on
// its own assertions rather than blocking here.
func (p *fakeProvider) contextDone() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return p.requests[len(p.requests)-1].Context().Done()
}

func (p *fakeProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	p.requests = append(p.requests, r)
	a := p.answer
	p.mu.Unlock()

	// The body is read the way a transport writes it, and every read is counted:
	// a hop that collected the whole body first shows up here as a reader that
	// would still be waiting for the caller.
	body := &countingReader{r: r.Body, onRead: func() {
		p.mu.Lock()
		p.reads++
		p.mu.Unlock()
		p.firstOne.Do(func() { close(p.first) })
	}}
	var collected bytes.Buffer
	if _, err := io.Copy(&collected, body); err != nil {
		// What a transport does with a request body that failed mid-write: the
		// call fails and there is no answer.
		return nil, err
	}
	p.mu.Lock()
	p.bodies = append(p.bodies, collected.Bytes())
	p.mu.Unlock()

	if a.err != nil {
		return nil, a.err
	}
	status := a.status
	if status == 0 {
		status = http.StatusOK
	}
	headers := a.headers
	if headers == nil {
		headers = http.Header{}
	}
	if headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", "audio/mpeg")
	}
	failing := status/100 != 2
	var src io.Reader
	switch {
	case len(a.chunks) > 0:
		src = &gatedChunks{parts: a.chunks, gate: a.gate, done: r.Context().Done(), err: a.bodyErr}
	case a.bodyErr != nil:
		src = &gatedChunks{parts: []string{a.body}, done: r.Context().Done(), err: a.bodyErr}
	default:
		src = strings.NewReader(a.body)
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body: &countingBody{r: src, onRead: func() {
			if !failing {
				return
			}
			p.mu.Lock()
			p.failRead++
			p.mu.Unlock()
		}},
		Request: r,
	}, nil
}

// countingBody counts the reads of an answer body, so a test can assert that a
// provider's FAILURE body was never read at all.
type countingBody struct {
	r      io.Reader
	onRead func()
}

func (b *countingBody) Read(p []byte) (int, error) {
	b.onRead()
	return b.r.Read(p)
}

func (b *countingBody) Close() error { return nil }

// countingReader counts the reads of a body. onRead fires AFTER the read, so a
// test can be sure a reader's own read completed before it acts on it.
type countingReader struct {
	r      io.Reader
	onRead func()
	mu     sync.Mutex
	count  int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.mu.Lock()
	c.count++
	c.mu.Unlock()
	if c.onRead != nil {
		c.onRead()
	}
	return n, err
}

func (c *countingReader) Close() error { return nil }

func (c *countingReader) reads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// gatedChunks is a provider's answer: one part per read, every part after the
// first waiting for the gate, and every read ending when the call's context is
// cancelled — which is what a real body does when the caller it is being
// streamed to goes away.
type gatedChunks struct {
	parts []string
	gate  chan struct{}
	done  <-chan struct{}
	err   error
	i     int
}

func (g *gatedChunks) Read(p []byte) (int, error) {
	if g.i > 0 && g.gate != nil {
		select {
		case <-g.gate:
		case <-g.done:
			return 0, errors.New("the call's context was cancelled")
		case <-time.After(30 * time.Second):
			return 0, errors.New("the test never released the provider's next chunk")
		}
	}
	if g.i >= len(g.parts) {
		if g.err != nil {
			err := g.err
			g.err = nil
			return 0, err
		}
		return 0, io.EOF
	}
	n := copy(p, g.parts[g.i])
	g.i++
	return n, nil
}

// gatedBody is a caller's recording whose second half is not produced until the
// provider's transport has read the first, so "forwarded as it arrives" is
// observed rather than assumed.
type gatedBody struct {
	first, second []byte
	release       <-chan struct{}
	n             int
}

func (g *gatedBody) Read(p []byte) (int, error) {
	switch g.n {
	case 0:
		g.n++
		return copy(p, g.first), nil
	case 1:
		select {
		case <-g.release:
		case <-time.After(30 * time.Second):
			return 0, errors.New("the provider never reached the first half of the recording, so the second half was never produced: the caller's body is being held instead of forwarded")
		}
		g.n++
		return copy(p, g.second), nil
	default:
		return 0, io.EOF
	}
}

// neverEnding is a body of unlimited length, for the case where the caller's own
// claim about its length is the thing that is absent.
type neverEnding struct{}

func (neverEnding) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

// waitForCalls blocks until the provider has been called want times, and fails
// the test rather than blocking forever on a hop that never called it.
func waitForCalls(t *testing.T, p *fakeProvider, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if p.calls() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the provider was called %d times, want %d", p.calls(), want)
}
