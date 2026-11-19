package voicesvc

// The server's tests: the route, the order of the chain, and the log line.
//
// The log line's test is the one Global Constraint 10 and §4.4 point 6 are
// about: no byte of any body reaches a log file, and this is where that is
// checked rather than promised.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// testHop is the whole service over the fakes: the real verifier, the real
// device reader and the real enforcer, and a log buffer to read what it wrote.
//
// It carries the forwarding half's parts too, because NewServer requires them
// (P5.3.5): the provider transport is the forwarding tests' own hook and a
// status query must never reach it, which is a property these tests assert
// rather than assume.
type testHop struct {
	server   *Server
	key      *testKey
	db       *testDB
	log      *bytes.Buffer
	provider *fakeProvider
}

func newTestHop(t *testing.T) *testHop {
	t.Helper()
	db := newTestDB(t)
	key := newTestKey(t)
	enforcer, err := NewEnforcer(RTDBVoice{DB: db.Client})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	buf := &bytes.Buffer{}
	provider := newFakeProvider()
	srv, err := NewServer(Config{
		Auth:     testAuthenticator(t, key),
		Devices:  RTDBDevices{DB: db.Client},
		Enforcer: enforcer,
		Keys:     &fakeKeys{keys: map[string]string{"deepgram": testKeyValue, "elevenlabs": testKeyValue}},
		Tokens:   &fakeTokens{token: "the-installations-own-access-token"},
		Client:   &http.Client{Transport: provider},
		Log:      slog.New(slog.NewTextHandler(buf, nil)),
		Now:      func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return &testHop{server: srv, key: key, db: db, log: buf, provider: provider}
}

// pair writes the device node, which is what `fugaro pair` does (D165/D166).
func (h *testHop) pair(t *testing.T) {
	t.Helper()
	h.db.Set(budget.DeviceRoot+"/"+testUID, map[string]any{"role": "owner", "email": "owner@example.invalid"})
}

// revoke deletes it, which is what revocation does.
func (h *testHop) revoke() { h.db.Delete(budget.DeviceRoot + "/" + testUID) }

// deviceToken mints a token for the paired device.
func (h *testHop) deviceToken(t *testing.T) string {
	t.Helper()
	return h.key.token(t, h.key.claims())
}

// call makes one request and returns the recorder.
func (h *testHop) call(t *testing.T, method, path, authHeader, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if authHeader != "" {
		r.Header.Set(HeaderAuth, authHeader)
	}
	rec := httptest.NewRecorder()
	h.server.ServeHTTP(rec, r)
	return rec
}

// status asks the project-status query with a device token.
func (h *testHop) status(t *testing.T, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	hdr := ""
	if token != "" {
		hdr = authScheme + token
	}
	return h.call(t, http.MethodPost, StatusPath, hdr, body)
}

// TestNewServerNeedsItsParts: a server missing any one of its parts would answer
// every call wrongly, and the wrong answer here is an admission. Each member is
// left out on its own, so a member that stopped being required is one subtest
// failing rather than a claim in a comment.
func TestNewServerNeedsItsParts(t *testing.T) {
	k := newTestKey(t)
	auth := testAuthenticator(t, k)
	e := testEnforcer(t, []byte(`{"providers":["deepgram"],"direction":{"stt":true}}`))
	d := fakeDevices{live: map[string]bool{testUID: true}}
	keys := &fakeKeys{keys: map[string]string{"deepgram": testKeyValue}}
	tokens := &fakeTokens{token: "the-installations-own-access-token"}
	client := &http.Client{Transport: newFakeProvider()}

	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"with no verifier", Config{Devices: d, Enforcer: e, Keys: keys, Tokens: tokens, Client: client}},
		{"with no device reader", Config{Auth: auth, Enforcer: e, Keys: keys, Tokens: tokens, Client: client}},
		{"with no enforcer", Config{Auth: auth, Devices: d, Keys: keys, Tokens: tokens, Client: client}},
		{"with no provider key source", Config{Auth: auth, Devices: d, Enforcer: e, Tokens: tokens, Client: client}},
		{"with no token source", Config{Auth: auth, Devices: d, Enforcer: e, Keys: keys, Client: client}},
		{"with no HTTP client", Config{Auth: auth, Devices: d, Enforcer: e, Keys: keys, Tokens: tokens}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewServer(tc.cfg); err == nil {
				t.Errorf("a server %s was built: it would answer calls it cannot serve, and the wrong answer on this service is an admission", tc.name)
			}
		})
	}
	if _, err := NewServer(Config{
		Auth: auth, Devices: d, Enforcer: e, Keys: keys, Tokens: tokens, Client: client,
	}); err != nil {
		t.Errorf("a complete server was refused: %v", err)
	}
}

// TestServerHasOneRoute: the status route is matched on the exact path, and a
// provider's own path is not one this service serves at all — §8's rule is that
// the hop resolves a NAME and never an address a caller named, so a caller
// cannot make it call one. The refusal says so rather than reading the request
// as something it understands.
//
// The two forwarding routes are P5.3.5's and are tested in forward_test.go; what
// is pinned here is that they are not reachable by accident from this one.
func TestServerHasOneRoute(t *testing.T) {
	h := newTestHop(t)
	h.pair(t)
	tok := h.deviceToken(t)

	for _, tc := range []struct {
		name, method, path string
		want               int
		wantReason         string
	}{
		{"another path", http.MethodPost, "/v1/audio/transcriptions", http.StatusNotFound, ReasonNotFound},
		{"the path with a slash", http.MethodPost, StatusPath + "/", http.StatusNotFound, ReasonNotFound},
		{"the root", http.MethodGet, "/", http.StatusNotFound, ReasonNotFound},
		{"a GET of the status path", http.MethodGet, StatusPath, http.StatusMethodNotAllowed, ReasonBadRequest},
		{"a PUT of the status path", http.MethodPut, StatusPath, http.StatusMethodNotAllowed, ReasonBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.call(t, tc.method, tc.path, authScheme+tok, `{"project":"`+testSlug+`"}`)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantReason) {
				t.Fatalf("the refusal reads %s, want %q", rec.Body.String(), tc.wantReason)
			}
		})
	}
}

// TestServerRefusesTheCallerItDoesNotKnow walks the chain in order, and each
// step's refusal is the one the app branches on.
func TestServerRefusesTheCallerItDoesNotKnow(t *testing.T) {
	h := newTestHop(t)
	body := `{"project":"` + testSlug + `"}`

	t.Run("no token at all", func(t *testing.T) {
		rec := h.status(t, "", body)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), ReasonTokenUnverified) {
			t.Fatalf("the refusal is %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a token of another service", func(t *testing.T) {
		rec := h.status(t, "not.a.token", body)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), ReasonTokenUnverified) {
			t.Fatalf("the refusal is %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a run identity", func(t *testing.T) {
		h.pair(t)
		rec := h.status(t, h.key.token(t, uidClaims(h.key, testRunUID)), body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), ReasonNotADevice) {
			t.Fatalf("the refusal is %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a device that was never paired", func(t *testing.T) {
		h.revoke()
		rec := h.status(t, h.deviceToken(t), body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), ReasonDeviceUnknown) {
			t.Fatalf("the refusal is %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a body this contract cannot read", func(t *testing.T) {
		h.pair(t)
		rec := h.status(t, h.deviceToken(t), `{"project":"not-a-slug"}`)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), ReasonBadRequest) {
			t.Fatalf("the refusal is %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a path this contract does not serve", func(t *testing.T) {
		rec := h.call(t, http.MethodPost, "/nope", authScheme+h.deviceToken(t), body)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), ReasonNotFound) {
			t.Fatalf("the refusal is %d %s", rec.Code, rec.Body.String())
		}
	})
}

// TestServerAnswersTheProjectStatusQuery is the query working end to end: a
// paired device, a published policy, and the answer.
func TestServerAnswersTheProjectStatusQuery(t *testing.T) {
	h := newTestHop(t)
	h.pair(t)
	h.db.Set(projectpolicy.PathVoiceRepo(testSlug), map[string]any{
		"providers": []any{"deepgram"},
		"direction": map[string]any{"stt": true},
	})

	rec := h.status(t, h.deviceToken(t), `{"project":"`+testSlug+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var ans StatusAnswer
	mustUnmarshal(t, rec.Body.Bytes(), &ans)
	if !ans.Remote || len(ans.Providers) != 1 || ans.Providers[0] != "deepgram" || !ans.Direction.STT || ans.Direction.TTS {
		t.Fatalf("the answer reads %+v", ans)
	}
	if ans.Project != testSlug {
		t.Fatalf("Project = %q, want %q", ans.Project, testSlug)
	}
	// The query carries no audio and no text, so it reaches no provider: it is
	// the only route of this service that never may.
	if n := h.provider.calls(); n != 0 {
		t.Fatalf("the project-status query reached a provider %d times", n)
	}
}

// TestServerAnswersALocalProjectWithTwoHundred: "this project is served
// locally" is the query's ANSWER and not a failure of the request (§4.4 point
// 7) — which is what lets the app show the indicator rather than an error.
func TestServerAnswersALocalProjectWithTwoHundred(t *testing.T) {
	h := newTestHop(t)
	h.pair(t)
	h.db.Set(projectpolicy.PathVoiceRepo(testSlug), map[string]any{"force_local": true})

	rec := h.status(t, h.deviceToken(t), `{"project":"`+testSlug+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var ans StatusAnswer
	mustUnmarshal(t, rec.Body.Bytes(), &ans)
	if ans.Remote || ans.Reason != ReasonForceLocal {
		t.Fatalf("the answer reads %+v", ans)
	}
}

// TestServerRefusesAProjectNothingAdmits: the same node, one call later, is a
// refusal rather than an answer — the query reports and the call refuses, and
// both read the same node.
func TestServerRefusesAProjectNothingAdmits(t *testing.T) {
	h := newTestHop(t)
	h.pair(t)
	h.db.Set(projectpolicy.PathVoiceRepo(testSlug), map[string]any{})

	// The query answers "nothing admitted".
	rec := h.status(t, h.deviceToken(t), `{"project":"`+testSlug+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var ans StatusAnswer
	mustUnmarshal(t, rec.Body.Bytes(), &ans)
	if ans.Remote || ans.Reason != ReasonNoAdmission {
		t.Fatalf("the answer reads %+v", ans)
	}

	// And the enforcer refuses the call itself.
	e, err := NewEnforcer(RTDBVoice{DB: h.db.Client})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	if err := e.Decide(t.Context(), testSlug, "deepgram", STT); ReasonOf(err) != ReasonNoAdmission {
		t.Fatalf("Decide = %v (%q), want %q", err, ReasonOf(err), ReasonNoAdmission)
	}
}

// TestServerAnswersStateUnavailableWhenItCannotRead: state this service could
// not reach is a 5xx and not a 4xx, because it is not the phone's fault — and
// it is the one answer the app must not read as "your device is revoked".
func TestServerAnswersStateUnavailableWhenItCannotRead(t *testing.T) {
	h := newTestHop(t)
	h.pair(t)
	h.db.DenyRead(budget.DeviceRoot)

	rec := h.status(t, h.deviceToken(t), `{"project":"`+testSlug+`"}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), ReasonStateUnavailable) {
		t.Fatalf("the refusal is %d %s", rec.Code, rec.Body.String())
	}
}

// TestNoLogLineCarriesAByteOfABody is §4.4 point 6 and Global Constraint 10:
// the log line holds the act, the identity's own names and the outcome, and no
// byte of any body.
//
// The markers are put where a body byte would be if one were logged — an
// unknown member of the request, and the token itself — and the assertion is
// that neither reaches the buffer. The token is the one that matters most: a
// token in a log file is a credential in a log file.
func TestNoLogLineCarriesAByteOfABody(t *testing.T) {
	h := newTestHop(t)
	h.pair(t)
	const bodyMarker = "MARKER-a-transcript-in-the-body"
	const headerMarker = "MARKER-a-credential"

	// A refused request: the body carries a marker as an unknown member.
	rec := h.status(t, headerMarker, `{"project":"`+testSlug+`","`+bodyMarker+`":"`+bodyMarker+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (%s)", rec.Code, rec.Body.String())
	}

	// An admitted request: the token is real and long, and the body is a slug.
	tok := h.deviceToken(t)
	h.status(t, tok, `{"project":"`+testSlug+`"}`)
	// A refused request with a real token: the body is unreadable.
	h.status(t, tok, `{"project":"`+bodyMarker+`"}`)

	logged := h.log.String()
	for _, marker := range []string{bodyMarker, headerMarker, tok, tok[:32]} {
		if strings.Contains(logged, marker) {
			t.Fatalf("a log line carries %q:\n%s", marker, logged)
		}
	}
	// And the line that WAS written is the one that should have been: the act,
	// the device and the outcome.
	if !strings.Contains(logged, "voice call") || !strings.Contains(logged, testUID) {
		t.Fatalf("the log line is missing what it should carry:\n%s", logged)
	}
	if !strings.Contains(logged, "status=401") && !strings.Contains(logged, "status=401 ") {
		t.Fatalf("the log line does not report the refusal:\n%s", logged)
	}
}

// TestTheRefusalOnTheWireDoesNotEchoTheCallerEither: the log is one half and
// the response is the other. A caller's unknown member name must not come back
// as a sentence this service wrote.
func TestTheRefusalOnTheWireDoesNotEchoTheCallerEither(t *testing.T) {
	h := newTestHop(t)
	h.pair(t)
	const marker = "MARKER-the-caller-wrote-this"

	rec := h.status(t, h.deviceToken(t), `{"project":"`+testSlug+`","`+marker+`":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), marker) {
		t.Fatalf("the refusal echoes the caller's member name: %s", rec.Body.String())
	}
}

// mustUnmarshal decodes a response body a test reads.
func mustUnmarshal(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decoding %s: %v", body, err)
	}
}
