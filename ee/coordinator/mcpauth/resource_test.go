// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// This file drives M7's resource server the way a client reaches it: one POST
// per JSON-RPC request on MCPPath, with a bearer token this installation
// minted. The five OAuth endpoints' own tests are M5's and M6's and are not
// repeated here — what is tested here is the door behind them.

// testIdentity is the identity the flow's tokens act for: the address the test
// Firebase token attests, in the entry form the lists are written in (D923).
// testEmail is M6's own fixture and the `user:` prefix is the rule M5's
// approval applies to it, joined once here.
const testIdentity = "user:" + testEmail

// testSeededToken is the bearer value of a token record a test writes into the
// store directly. It is a marker and never a credential: the store keys the
// record by its hash, so the value itself is written nowhere, and nothing in
// this repository carries a real one.
const testSeededToken = "token-marker-of-a-record-seeded-by-a-test"

// testLists is the installation's member lists as the tests start them: this
// one person, a launcher, and no operator.
func testLists() Members { return Members{Launchers: []string{testIdentity}} }

// rosterFunc is a Roster a test writes as a function, which is what the tests
// that change the lists mid-test and the one whose read fails need.
type rosterFunc func(ctx context.Context) (Members, error)

func (f rosterFunc) Members(ctx context.Context) (Members, error) { return f(ctx) }

// rosterOf is the lists an installation has, as the front door reads them.
func rosterOf(lists Members) Roster {
	return rosterFunc(func(context.Context) (Members, error) { return lists, nil })
}

// movingClock is the server's clock, which a test advances instead of waiting
// for: the one bound on an access token here is time (AccessTokenTTL).
type movingClock struct{ at time.Time }

func (c *movingClock) now() time.Time { return c.at }

// resourceTest is the front door as this file drives it: M5's test server —
// the fake bucket, the verifier, the service — with the installation's lists
// and a clock the test owns.
type resourceTest struct {
	*testServer
	clock *movingClock
}

// newResourceServer builds the front door. The lists default to testLists and
// every option the caller passes is applied after them, so a test that wants
// no roster, a roster that refuses or a roster that fails says so with the same
// WithRoster it would use to name one.
func newResourceServer(t *testing.T, opts ...ServerOption) *resourceTest {
	t.Helper()
	clock := &movingClock{at: testNow}
	ts := newTestServerOver(t, testConfig(), clock.now, nil,
		append([]ServerOption{WithRoster(rosterOf(testLists()))}, opts...)...)
	return &resourceTest{testServer: ts, clock: clock}
}

// serve registers one tool with the handler the test supplies. The name has to
// be one the catalogue holds, which is what makes registration a test's way of
// saying "this tool is served" and nothing more.
func (ts *testServer) serve(t *testing.T, name string, handler Handler) {
	t.Helper()
	err := ts.tools.Register(Tool{
		Name:        name,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler:     handler,
	})
	if err != nil {
		t.Fatalf("registering %q: %v", name, err)
	}
}

// seedAccessToken writes one access token record into the store the server
// reads, with the identity, the limits and the expiry the test names. It is how
// a test reaches a token the flow will not mint — an unknown scope, an identity
// on no list, no expiry at all — which is the shape a later build or a
// half-finished installation would present.
func (ts *testServer) seedAccessToken(t *testing.T, value string, token Token) {
	t.Helper()
	ctx := context.Background()
	fam := Family{
		ID:        "family-" + TokenHash(value)[:16],
		ClientID:  token.ClientID,
		Identity:  token.Identity,
		Limits:    token.Limits,
		CreatedAt: testNow,
	}
	if err := ts.store.Store.PutFamily(ctx, fam, 0); err != nil {
		t.Fatalf("seeding the family: %v", err)
	}
	rec := TokenRecord{Hash: TokenHash(value), FamilyID: fam.ID, Kind: KindAccess, Token: token}
	if err := ts.store.Store.PutToken(ctx, rec); err != nil {
		t.Fatalf("seeding the token record: %v", err)
	}
}

// seededToken is seedAccessToken for the common shape: this installation's
// person, both scopes, alive at the test's own instant.
func (ts *testServer) seededToken(t *testing.T, identity string) string {
	t.Helper()
	ts.seedAccessToken(t, testSeededToken, Token{
		Identity:  identity,
		ClientID:  "client-that-minted-it",
		Limits:    Limits{Scopes: []string{ScopeTasksRead, ScopeTasksWrite}},
		ExpiresAt: testNow.Add(AccessTokenTTL),
	})
	return testSeededToken
}

// rpc posts one JSON-RPC request with this installation's bearer token.
func (ts *testServer) rpc(t *testing.T, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	header := ""
	if token != "" {
		header = "Bearer " + token
	}
	return ts.rpcHeader(t, header, body)
}

// rpcHeader posts one request with the Authorization header written verbatim,
// which is how a test presents one this door has to refuse.
func (ts *testServer) rpcHeader(t *testing.T, header, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, MCPPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	return ts.do(t, req)
}

// runFlowScoped is runFlow with the scopes the client asks for, which is how a
// test gets a token that holds one of the two: the flow narrows what is asked
// for against the project's and refuses what D927 does not know.
func (ts *testServer) runFlowScoped(t *testing.T, redirect, scope string) (clientID string, tokens tokenResponse) {
	t.Helper()
	clientID, _ = ts.register(t, "a reader", redirect)
	q := authorizeQuery(clientID, redirect)
	q.Set("scope", scope)
	binding, number := ts.startFlow(t, q)
	code := codeOf(t, ts.approve(t, binding, number))
	rec, tokens := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {redirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("the code exchange answered %d (%s); want a token pair holding %s", rec.Code, strings.TrimSpace(rec.Body.String()), scope)
	}
	return clientID, tokens
}

// rpcAnswer is one answer off the wire: JSON-RPC's own three fields, with the
// error left as a pointer so that a test can tell a result from a refusal.
type rpcAnswer struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcFailure     `json:"error"`
}

type rpcFailure struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func rpcBodyOf(t *testing.T, rec *httptest.ResponseRecorder) rpcAnswer {
	t.Helper()
	var out rpcAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("the answer is not a JSON-RPC document: %v (%s)", err, strings.TrimSpace(rec.Body.String()))
	}
	if out.JSONRPC != "2.0" {
		t.Errorf("the answer says jsonrpc=%q; every answer of this endpoint is 2.0", out.JSONRPC)
	}
	return out
}

// wantResult reads an answer that has to be a result: a 200 and no error. The
// status is 200 for a failure too — JSON-RPC carries its errors in the body —
// so the two are checked together here.
func wantResult(t *testing.T, rec *httptest.ResponseRecorder) rpcAnswer {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("the answer is %d (%s); want 200", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	out := rpcBodyOf(t, rec)
	if out.Error != nil {
		t.Fatalf("the answer is the error %d %q; want a result", out.Error.Code, out.Error.Message)
	}
	if len(out.Result) == 0 {
		t.Fatal("the answer carries no result")
	}
	return out
}

// wantFailure reads an answer that has to be a refusal, with the status and the
// JSON-RPC code the caller names.
func wantFailure(t *testing.T, rec *httptest.ResponseRecorder, wantStatus, wantCode int) rpcAnswer {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("the refusal is %d (%s); want %d", rec.Code, strings.TrimSpace(rec.Body.String()), wantStatus)
	}
	out := rpcBodyOf(t, rec)
	if out.Error == nil {
		t.Fatalf("the answer is a result (%s); want the error %d", out.Result, wantCode)
	}
	if out.Error.Code != wantCode {
		t.Errorf("the refusal carries the code %d (%s); want %d", out.Error.Code, out.Error.Message, wantCode)
	}
	return out
}

// wantToolText reads a tools/call result's own answer: MCP puts a tool's answer
// in a content list of one text member, and its failure beside it rather than
// in a JSON-RPC error.
func wantToolText(t *testing.T, ans rpcAnswer) (text string, isError bool) {
	t.Helper()
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(ans.Result, &result); err != nil {
		t.Fatalf("the tool's result is not MCP's shape: %v (%s)", err, ans.Result)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("the tool's answer is %s; want one text member", ans.Result)
	}
	return result.Content[0].Text, result.IsError
}

// wantBearerRefusal checks the refusal RFC 6750 §3 asks for: the status, the
// error the caller reads in the body, the challenge carrying this resource's
// metadata URL, and an answer that is never cached.
//
// What the challenge carries *beside* the metadata URL is not checked here,
// because the parameter set differs with the refusal — a request that carried
// no credential is given no error information at all — and the test below is
// the one place that pins each shape.
func wantBearerRefusal(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantError string) oauthRefusal {
	t.Helper()
	refusal := refusalOf(t, rec)
	if rec.Code != wantStatus || refusal.Error != wantError {
		t.Fatalf("the refusal is %d with error %q (%s); want %d with %q", rec.Code, refusal.Error, strings.TrimSpace(rec.Body.String()), wantStatus, wantError)
	}
	challenge := rec.Header().Get("WWW-Authenticate")
	if !strings.HasPrefix(challenge, "Bearer ") {
		t.Errorf("the refusal carries WWW-Authenticate %q, which names no Bearer challenge: RFC 9728 puts the metadata document's URL in that header and nowhere else", challenge)
	}
	if want := `resource_metadata="` + testIssuer + ProtectedResourcePath + `"`; !strings.Contains(challenge, want) {
		t.Errorf("the challenge %q does not name the resource metadata document: a client that reads it has no other way to find the flow", challenge)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("the refusal is cacheable (Cache-Control: %q): an answer about a token must not be kept by anything between this door and the client", got)
	}
	return refusal
}

// The two bodies most of the tests below drive the door with.
const (
	toolsListBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	callBody      = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_project_state","arguments":{}}}`
)

// TestACallReachesTheHandlerWithWhatTheTokenResolvedTo is the whole path in one
// test: a token the flow minted, a tool the test registered, and a handler that
// answers. What it pins is that the handler is handed the caller and not the
// request — the identity, the role, the client and the scopes the token
// resolved to — because that is the argument every later tool reads and the
// only thing it may act on.
func TestACallReachesTheHandlerWithWhatTheTokenResolvedTo(t *testing.T) {
	ts := newResourceServer(t)
	var got Call
	calls := 0
	ts.serve(t, "get_task_status", func(ctx context.Context, call Call) (any, error) {
		calls++
		got = call
		return "run-7 is implementing", nil
	})
	clientID, tokens := ts.runFlow(t, testClientRedirect)

	rec := ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_task_status","arguments":{"run":"run-7","nested":{"a":[1,2]}}}}`)
	ans := wantResult(t, rec)
	if string(ans.ID) != "7" {
		t.Errorf("the answer's id is %s; want the request's own 7 — JSON-RPC answers what it was asked", ans.ID)
	}
	text, isError := wantToolText(t, ans)
	if text != "run-7 is implementing" || isError {
		t.Errorf("the tool answered %q (isError=%v); want its own text and no error", text, isError)
	}
	if calls != 1 {
		t.Errorf("the handler ran %d times; want once", calls)
	}
	if got.Tool != "get_task_status" {
		t.Errorf("the handler was told it is %q; want the name the client called", got.Tool)
	}
	if got.Identity != testIdentity {
		t.Errorf("the handler was told the caller is %q; want the identity the token resolved to, %q", got.Identity, testIdentity)
	}
	if got.ClientID != clientID {
		t.Errorf("the handler was told the client is %q; want the registration the token was issued to, %q", got.ClientID, clientID)
	}
	if !got.Role.Launcher {
		t.Error("the handler was told the caller is no launcher: the role comes from the installation's lists and this person is on the launcher list")
	}
	if !slices.Contains(got.Scopes, ScopeTasksRead) {
		t.Errorf("the handler was told the scopes %v; want the token's own", got.Scopes)
	}
	if string(got.Arguments) != `{"run":"run-7","nested":{"a":[1,2]}}` {
		t.Errorf("the handler was handed %s; want the client's arguments verbatim", got.Arguments)
	}

	ts.serve(t, "list_tasks", func(context.Context, Call) (any, error) {
		return nil, errors.New("the run record for run-9 is not readable")
	})
	ans = wantResult(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"list_tasks","arguments":{}}}`))
	text, isError = wantToolText(t, ans)
	if !isError || !strings.Contains(text, "run-9") {
		t.Errorf("a tool that failed answered %q (isError=%v); want the tool's own refusal inside a successful result: a client has to show what the tool said, and a JSON-RPC error would be the server failing to read the call", text, isError)
	}
}

// TestAToolTheTokenHasNoScopeForIsRefused is D927 at the tool boundary: a token
// that holds tasks:read reaches the read tools and is refused at the one that
// writes, in RFC 6750's shape, with the scope it would need named in the
// challenge. The read call is the control — the refusal below is about the
// tool's scope and not about the token.
func TestAToolTheTokenHasNoScopeForIsRefused(t *testing.T) {
	ts := newResourceServer(t)
	reads, writes := 0, 0
	ts.serve(t, "list_tasks", func(context.Context, Call) (any, error) { reads++; return "two runs", nil })
	ts.serve(t, "record_override", func(context.Context, Call) (any, error) { writes++; return "recorded", nil })
	_, tokens := ts.runFlowScoped(t, testClientRedirect, ScopeTasksRead)

	ans := wantResult(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_tasks","arguments":{}}}`))
	if text, isError := wantToolText(t, ans); text != "two runs" || isError || reads != 1 {
		t.Fatalf("the read tool answered %q (isError=%v, %d calls); want it served: without this half the refusal below would pass for a door that serves nothing", text, isError, reads)
	}

	rec := ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"record_override","arguments":{"reason":"a decision"}}}`)
	wantBearerRefusal(t, rec, http.StatusForbidden, "insufficient_scope")
	if want := `scope="` + ScopeTasksWrite + `"`; !strings.Contains(rec.Header().Get("WWW-Authenticate"), want) {
		t.Errorf("the challenge %q does not name %s: a client that could ask for a wider scope has no way to learn which one", rec.Header().Get("WWW-Authenticate"), want)
	}
	if writes != 0 {
		t.Error("the handler ran for a token that does not hold its scope: the scope is checked before a handler is reached, and never inside one")
	}
}

// TestTheDoorAnswersJSONRPCAndRefusesEverythingElse drives the surface a client
// meets: the three methods this door has beyond tools/call, the notification it
// answers with nothing, and the bodies it will not read at all. The refusals
// are JSON-RPC's own codes, because a client acts on them.
func TestTheDoorAnswersJSONRPCAndRefusesEverythingElse(t *testing.T) {
	ts := newResourceServer(t)
	ts.serve(t, "get_project_state", func(context.Context, Call) (any, error) {
		return map[string]any{"runs": 2}, nil
	})
	_, tokens := ts.runFlow(t, testClientRedirect)

	t.Run("initialize names the revision this door serves", func(t *testing.T) {
		ans := wantResult(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"a client","version":"1"}}}`))
		var result struct {
			ProtocolVersion string                     `json:"protocolVersion"`
			Capabilities    map[string]json.RawMessage `json:"capabilities"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		}
		if err := json.Unmarshal(ans.Result, &result); err != nil {
			t.Fatalf("initialize's result is not the shape MCP asks for: %v (%s)", err, ans.Result)
		}
		if result.ProtocolVersion != mcpProtocolVersion {
			t.Errorf("initialize answered the revision %q; want %q: a server names the revision it serves, and never echoes the one the client asked for", result.ProtocolVersion, mcpProtocolVersion)
		}
		if _, ok := result.Capabilities["tools"]; !ok {
			t.Errorf("initialize advertises %v; the one capability this door has is tools", result.Capabilities)
		}
		if result.ServerInfo.Name != mcpServerName || result.ServerInfo.Version == "" {
			t.Errorf("initialize names the server %+v; want this door's own name and a version", result.ServerInfo)
		}
	})

	t.Run("ping answers after the same token check as everything else", func(t *testing.T) {
		ans := wantResult(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":2,"method":"ping"}`))
		if string(ans.Result) != "{}" {
			t.Errorf("ping answered %s; want an empty result", ans.Result)
		}
	})

	t.Run("tools/list names the tools that are served", func(t *testing.T) {
		ans := wantResult(t, ts.rpc(t, tokens.AccessToken, toolsListBody))
		var result struct {
			Tools []Declaration `json:"tools"`
		}
		if err := json.Unmarshal(ans.Result, &result); err != nil {
			t.Fatalf("tools/list's result is not the shape MCP asks for: %v (%s)", err, ans.Result)
		}
		if len(result.Tools) != 1 || result.Tools[0].Name != "get_project_state" {
			t.Fatalf("tools/list answered %+v; want the one tool this test registered", result.Tools)
		}
		if result.Tools[0].Description == "" || len(result.Tools[0].InputSchema) == 0 {
			t.Errorf("the declaration is %+v; a client decides to call a tool from its description and renders its form from the schema", result.Tools[0])
		}
	})

	t.Run("tools/call answers a tool that returns data", func(t *testing.T) {
		ans := wantResult(t, ts.rpc(t, tokens.AccessToken, callBody))
		if text, isError := wantToolText(t, ans); text != `{"runs":2}` || isError {
			t.Errorf("the tool answered %q (isError=%v); want its own data as JSON text", text, isError)
		}
	})

	t.Run("a notification is answered with no body", func(t *testing.T) {
		rec := ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("a notification was answered %d (%s); want 202 with no body: JSON-RPC answers a request with no id with nothing at all", rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if body := strings.TrimSpace(rec.Body.String()); body != "" {
			t.Errorf("a notification was answered with %q; want no body", body)
		}
	})

	t.Run("a method this door has not got", func(t *testing.T) {
		wantFailure(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`), http.StatusOK, codeMethodMissing)
	})

	t.Run("a name no tool of this front door has", func(t *testing.T) {
		wantFailure(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"roadmap_plan","arguments":{}}}`), http.StatusOK, codeInvalidParams)
	})

	t.Run("a tool the catalogue holds and nothing serves yet", func(t *testing.T) {
		// spawn_task is D936's deferred half: its scope is decided and no
		// handler is registered, so the name is known and not served.
		wantFailure(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"spawn_task","arguments":{"repo":"a-repo"}}}`), http.StatusOK, codeInvalidParams)
	})

	t.Run("arguments that are not an object", func(t *testing.T) {
		wantFailure(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_project_state","arguments":[1,2]}}`), http.StatusOK, codeInvalidParams)
	})

	t.Run("a call that names no tool", func(t *testing.T) {
		wantFailure(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"arguments":{}}}`), http.StatusOK, codeInvalidParams)
	})

	t.Run("a body that is not JSON at all", func(t *testing.T) {
		wantFailure(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":`), http.StatusBadRequest, codeParse)
	})

	t.Run("an empty body", func(t *testing.T) {
		wantFailure(t, ts.rpc(t, tokens.AccessToken, ``), http.StatusBadRequest, codeParse)
	})

	t.Run("a batch", func(t *testing.T) {
		wantFailure(t, ts.rpc(t, tokens.AccessToken, `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`), http.StatusBadRequest, codeInvalid)
	})

	t.Run("an id of another shape", func(t *testing.T) {
		wantFailure(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":{"a":1},"method":"ping"}`), http.StatusBadRequest, codeInvalid)
	})

	t.Run("a request that is not JSON-RPC 2.0", func(t *testing.T) {
		wantFailure(t, ts.rpc(t, tokens.AccessToken, `{"id":1,"method":"ping"}`), http.StatusBadRequest, codeInvalid)
	})

	t.Run("a GET is not a stream this door serves", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, MCPPath, nil)
		req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
		rec := ts.do(t, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("a GET on the MCP endpoint was answered %d (%s); want 405", rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if got := rec.Header().Get("Allow"); got != http.MethodPost {
			t.Errorf("the 405 allows %q; want POST named, so a client learns what this endpoint does take", got)
		}
	})
}

// TestTheBearerCredentialIsTheOnlyWayIn is the door itself: one Authorization
// header, one Bearer credential, a token this installation minted for this
// resource, and nothing else. Every refusal below answers the same way, so a
// caller that has nothing learns nothing about what the installation holds.
func TestTheBearerCredentialIsTheOnlyWayIn(t *testing.T) {
	ts := newResourceServer(t)
	ts.serve(t, "get_project_state", func(context.Context, Call) (any, error) { return "ok", nil })
	clientID, tokens := ts.runFlow(t, testClientRedirect)

	for _, tc := range []struct {
		what   string
		header string
	}{
		{"no Authorization header", ""},
		{"a scheme this door does not take", "Basic"},
		{"the Bearer scheme with no credential", "Bearer"},
		{"the Bearer scheme with an empty credential", "Bearer    "},
		{"two credentials in one header", "Bearer " + tokens.AccessToken + " " + tokens.AccessToken},
		{"a value this installation never minted", "Bearer " + testSeededToken},
		{"a refresh token presented as a bearer", "Bearer " + tokens.RefreshToken},
	} {
		t.Run(tc.what, func(t *testing.T) {
			rec := ts.rpcHeader(t, tc.header, toolsListBody)
			wantBearerRefusal(t, rec, http.StatusUnauthorized, "invalid_token")
			if body := rec.Body.String(); strings.Contains(body, testSeededToken) || strings.Contains(body, tokens.RefreshToken) {
				t.Errorf("the refusal quotes the credential it refused: %s", body)
			}
		})
	}

	t.Run("a second Authorization header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, MCPPath, strings.NewReader(toolsListBody))
		req.Header.Add("Authorization", "Bearer "+tokens.AccessToken)
		req.Header.Add("Authorization", "Bearer "+tokens.AccessToken)
		wantBearerRefusal(t, ts.do(t, req), http.StatusUnauthorized, "invalid_token")
	})

	t.Run("a family that was revoked", func(t *testing.T) {
		// A revoke is one delete of the family (D930), after which the record
		// does not resolve at all: the refusal is the same one a stranger's
		// value gets, and the caller is told nothing more.
		rec := ts.postForm(t, RevokePath, url.Values{"token": {tokens.AccessToken}, "client_id": {clientID}})
		if rec.Code != http.StatusOK {
			t.Fatalf("the revocation answered %d (%s); want 200", rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		wantBearerRefusal(t, ts.rpc(t, tokens.AccessToken, toolsListBody), http.StatusUnauthorized, "invalid_token")
	})

	t.Run("a token the flow minted", func(t *testing.T) {
		_, fresh := ts.runFlow(t, testClientRedirect)
		wantResult(t, ts.rpc(t, fresh.AccessToken, toolsListBody))
	})
}

// wantChallenge pins the parameters of a refusal's WWW-Authenticate challenge:
// the resource metadata URL, which every refusal carries, and the error and the
// scope only where this refusal has one to name. An empty wantError is the
// assertion that the challenge carries no error information at all, which is
// what RFC 6750 §3.1 asks of a request that presented no credential.
func wantChallenge(t *testing.T, rec *httptest.ResponseRecorder, wantError, wantScope string) {
	t.Helper()
	challenge := rec.Header().Get("WWW-Authenticate")
	if !strings.HasPrefix(challenge, "Bearer ") {
		t.Fatalf("the challenge is %q; want a Bearer challenge", challenge)
	}
	if got, want := resourceMetadataURL(t, challenge), testIssuer+ProtectedResourcePath; got != want {
		t.Errorf("the challenge names %q and this resource's metadata document is at %q", got, want)
	}
	for _, param := range []struct {
		name  string
		value string
	}{
		{"error", wantError},
		{"scope", wantScope},
	} {
		carried := strings.Contains(challenge, param.name+"=")
		if param.value == "" {
			if carried {
				t.Errorf("the challenge %q carries a %s parameter: this refusal has none to name, and RFC 6750 §3.1 withholds the error information from a request that carried no credential at all — the challenge is what a client acts on, and an error code there describes a refusal it did not get", challenge, param.name)
			}
			continue
		}
		if want := param.name + `="` + param.value + `"`; !strings.Contains(challenge, want) {
			t.Errorf("the challenge %q does not carry %s: a client reads which of the two things went wrong out of the challenge and nowhere else, and a caller that could ask for a wider scope has no other way to learn which one", challenge, want)
		}
	}
}

// TestTheChallengeNamesAnErrorOnlyWhereThereIsOne is RFC 6750 §3.1 at this
// door, and it is written for what is *absent* as much as for what is present.
// A caller that presented no Bearer credential is not a caller whose token was
// refused — it was not attempting to authenticate — so the challenge names the
// metadata document and no error at all; the error the body carries is M5's
// shape and stays. Every other refusal names one, because the caller did
// present something and the code is the only thing it can act on.
func TestTheChallengeNamesAnErrorOnlyWhereThereIsOne(t *testing.T) {
	ts := newResourceServer(t)
	ts.serve(t, "record_override", func(context.Context, Call) (any, error) { return "recorded", nil })
	ts.serve(t, "get_project_state", func(context.Context, Call) (any, error) { return "ok", nil })
	_, tokens := ts.runFlowScoped(t, testClientRedirect, ScopeTasksRead)

	t.Run("a request that carried no credential", func(t *testing.T) {
		rec := ts.rpcHeader(t, "", toolsListBody)
		wantBearerRefusal(t, rec, http.StatusUnauthorized, "invalid_token")
		wantChallenge(t, rec, "", "")
	})

	t.Run("a value this installation never minted", func(t *testing.T) {
		rec := ts.rpc(t, testSeededToken, toolsListBody)
		wantBearerRefusal(t, rec, http.StatusUnauthorized, "invalid_token")
		wantChallenge(t, rec, "invalid_token", "")
	})

	t.Run("a token that holds no scope for the tool it called", func(t *testing.T) {
		rec := ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"record_override","arguments":{"reason":"a decision"}}}`)
		wantBearerRefusal(t, rec, http.StatusForbidden, "insufficient_scope")
		wantChallenge(t, rec, "insufficient_scope", ScopeTasksWrite)
	})

	t.Run("an identity on neither list", func(t *testing.T) {
		// The token is this installation's own and holds both scopes, so the
		// refusal is about who it acts for: it names the code and no scope,
		// because no wider scope would help.
		off := newResourceServer(t, WithRoster(rosterOf(Members{})))
		_, stranger := off.runFlow(t, testClientRedirect)
		rec := off.rpc(t, stranger.AccessToken, toolsListBody)
		wantBearerRefusal(t, rec, http.StatusForbidden, "insufficient_scope")
		wantChallenge(t, rec, "insufficient_scope", "")
	})
}

// TestAStoreThatCannotBeReadIsNotAnsweredAsABadToken is the same distinction
// the revoke path and resolveIdentity make, at this door's lookup: a token this
// installation never issued and a store that could not be read are two
// different facts, and answering the second as the first tells a person holding
// a good token that it was never minted — after which they stop asking. The
// control at the end is the same token against a store that answers.
func TestAStoreThatCannotBeReadIsNotAnsweredAsABadToken(t *testing.T) {
	ts := newResourceServer(t)
	_, tokens := ts.runFlow(t, testClientRedirect)
	wantResult(t, ts.rpc(t, tokens.AccessToken, toolsListBody))

	// The flow above ran against the real store, so what is replaced here is a
	// working store and not a broken one everything else also stands on.
	ts.Server.store = unreadableStore{Store: ts.store}

	rec := ts.rpc(t, tokens.AccessToken, toolsListBody)
	wantBearerRefusal(t, rec, http.StatusInternalServerError, "server_error")
	wantChallenge(t, rec, "", "")
	if body := rec.Body.String(); !strings.Contains(body, "could not read this installation's token records") {
		t.Errorf("the refusal says %s; want it naming the read that failed: the caller's token is not what is wrong, and a statement about it would send them to sign in again", strings.TrimSpace(body))
	}
}

// TestTheBodyBoundIsWhatStopsARequestPastIt is this endpoint's half of M5's
// form bound: readRequest's MaxBytesReader is the only per-request memory bound
// a public endpoint with no rate limit has, and a caller past it is refused
// rather than read.
//
// The two bodies below are well-formed JSON-RPC requests that differ in nothing
// but their size — the padding is inside a member the request does not read —
// so the refusal is about the size and not about the body. The control at
// exactly the bound is the same request, answered on its merits: with the
// MaxBytesReader gone it is answered 200 instead of 400, so a test that
// asserted only the status of the large one would pass either way.
func TestTheBodyBoundIsWhatStopsARequestPastIt(t *testing.T) {
	ts := newResourceServer(t)
	_, tokens := ts.runFlow(t, testClientRedirect)

	const padding = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"padding":"%s"}}`
	bodyOf := func(n int) string {
		pad := n - (len(padding) - len("%s"))
		if pad < 0 {
			t.Fatalf("a body of %d bytes cannot hold this request: the two bodies below have to differ in size and in nothing else", n)
		}
		return fmt.Sprintf(padding, strings.Repeat("a", pad))
	}
	if got := len(bodyOf(maxRequestBytes)); got != maxRequestBytes {
		t.Fatalf("the padded request is %d bytes; want exactly %d: the bound is what the two requests below differ by", got, maxRequestBytes)
	}

	rec := ts.rpc(t, tokens.AccessToken, bodyOf(maxRequestBytes+1))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a %d-byte body answered %d (%s); want 400: the endpoint reads a bounded body, and a caller that sends more is refused rather than read", maxRequestBytes+1, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if body := rec.Body.String(); !strings.Contains(body, "not one this endpoint can read") {
		t.Errorf("the refusal for a %d-byte body is %s; want it naming the body it could not read: with the bound gone the same request is read and answered", maxRequestBytes+1, strings.TrimSpace(body))
	}

	wantResult(t, ts.rpc(t, tokens.AccessToken, bodyOf(maxRequestBytes)))
}

// TestAnExpiredTokenIsRefused is the one bound on an access token that nothing
// but time reaches: the record is in the store and its family is alive, and the
// door refuses it for its age. The second half is the shape a half-written
// record has — no expiry — which is refused rather than read as never expiring.
func TestAnExpiredTokenIsRefused(t *testing.T) {
	ts := newResourceServer(t)
	_, tokens := ts.runFlow(t, testClientRedirect)
	wantResult(t, ts.rpc(t, tokens.AccessToken, toolsListBody))

	ts.clock.at = testNow.Add(time.Duration(tokens.ExpiresIn)*time.Second + time.Second)
	refusal := wantBearerRefusal(t, ts.rpc(t, tokens.AccessToken, toolsListBody), http.StatusUnauthorized, "invalid_token")
	if !strings.Contains(refusal.Description, "expired") {
		t.Errorf("the refusal says %q; a caller holding a real token is told which of the five facts about it refused, and age is actionable where a plain refusal is not", refusal.Description)
	}

	ts.seedAccessToken(t, testSeededToken, Token{
		Identity: testIdentity,
		ClientID: "client-that-minted-it",
		Limits:   Limits{Scopes: []string{ScopeTasksRead}},
	})
	wantBearerRefusal(t, ts.rpc(t, testSeededToken, toolsListBody), http.StatusUnauthorized, "invalid_token")
}

// TestIdentityOffTheListsIsRefused is D933 at the door: a token this
// installation minted for a person who is on neither list reaches no tool, and
// a fault that stops the lists being read is not reported as that refusal.
func TestIdentityOffTheListsIsRefused(t *testing.T) {
	for _, tc := range []struct {
		what       string
		opts       []ServerOption
		wantStatus int
		wantError  string
	}{
		{
			"an identity on neither list",
			[]ServerOption{WithRoster(rosterOf(Members{Launchers: []string{"user:somebody-else@example.com"}}))},
			http.StatusForbidden, "insufficient_scope",
		},
		{
			"a server built with no roster admits nobody",
			[]ServerOption{WithRoster(nil)},
			http.StatusForbidden, "insufficient_scope",
		},
		{
			"a roster this front door cannot read",
			[]ServerOption{WithRoster(rosterFunc(func(context.Context) (Members, error) {
				return Members{}, errors.New("the installation's published state is not readable")
			}))},
			http.StatusInternalServerError, "server_error",
		},
		{
			"a person on the operator list",
			[]ServerOption{WithRoster(rosterOf(Members{Operators: []string{testIdentity}}))},
			http.StatusOK, "",
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			ts := newResourceServer(t, tc.opts...)
			ts.serve(t, "get_project_state", func(context.Context, Call) (any, error) { return "ok", nil })
			_, tokens := ts.runFlow(t, testClientRedirect)
			rec := ts.rpc(t, tokens.AccessToken, toolsListBody)
			if tc.wantStatus == http.StatusOK {
				wantResult(t, rec)
				return
			}
			wantBearerRefusal(t, rec, tc.wantStatus, tc.wantError)
			if body := rec.Body.String(); strings.Contains(body, testEmail) {
				t.Errorf("the refusal names the address it refused: %s — whether an address is on a list is a fact about the installation, and an answer that told a caller it would tell a stranger which addresses it holds", body)
			}
		})
	}
}

// TestOnlyExactUserEntriesAreRead is D923's reading rule, row by row: the
// identity a token acts for has to be one person written as `user:<email>`, and
// a list entry admits that person only when it is that entry exactly. Every
// other kind of entry — group, domain, service account — and every near miss of
// the form is read past, so a list carrying one beside its people is still a
// list of people and an entry of another kind admits nobody.
func TestOnlyExactUserEntriesAreRead(t *testing.T) {
	for _, tc := range []struct {
		what     string
		lists    Members
		identity string
		admitted bool
	}{
		{"the entry the command writes", Members{Launchers: []string{testIdentity}}, testIdentity, true},
		{"an operator", Members{Operators: []string{testIdentity}}, testIdentity, true},
		{"a domain entry admits no person", Members{Launchers: []string{"domain:example.com"}}, testIdentity, false},
		{"an entry this door does not read beside the person's own", Members{Launchers: []string{"domain:example.com", testIdentity}}, testIdentity, true},
		{"an identity that is a domain entry", Members{Launchers: []string{"domain:example.com"}}, "domain:example.com", false},
		{"an identity that is a group entry", Members{Launchers: []string{"group:the-team@example.com"}}, "group:the-team@example.com", false},
		{"an identity that is a service account", Members{Launchers: []string{"serviceAccount:robot@example.iam.gserviceaccount.com"}}, "serviceAccount:robot@example.iam.gserviceaccount.com", false},
		{"allUsers", Members{Launchers: []string{"allUsers"}}, "allUsers", false},
		{"an identity with no user: prefix", Members{Launchers: []string{testIdentity}}, testEmail, false},
		{"an identity that is not an address", Members{Launchers: []string{"user:person-1"}}, "user:person-1", false},
		{"an identity with a trailing space", Members{Launchers: []string{testIdentity}}, testIdentity + " ", false},
		{"an empty identity", Members{Launchers: []string{testIdentity}}, "", false},
		{"a list entry with a leading space", Members{Launchers: []string{" " + testIdentity}}, testIdentity, false},
		{"a list entry with a trailing space", Members{Launchers: []string{testIdentity + " "}}, testIdentity, false},
		{"a wildcard entry", Members{Launchers: []string{"user:*@example.com"}}, testIdentity, false},
		{"a list entry whose address is written with a capital", Members{Launchers: []string{"user:Person-1@example.com"}}, testIdentity, false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			ts := newResourceServer(t, WithRoster(rosterOf(tc.lists)))
			ts.serve(t, "get_project_state", func(context.Context, Call) (any, error) { return "ok", nil })
			ts.seededToken(t, tc.identity)
			rec := ts.rpc(t, testSeededToken, toolsListBody)
			if tc.admitted {
				wantResult(t, rec)
				return
			}
			wantBearerRefusal(t, rec, http.StatusForbidden, "insufficient_scope")
		})
	}
}

// TestTheListsComeFromTheInstallationsOwnState is finding M-f, in its two
// halves. The first is the seam: the lists are read on every request, so a
// person an operator took off the installation stops working at the next call
// and starts again when they are put back — no deployment in between, and no
// restart. The second is a property of the source, because the seam is only
// worth having if nothing else reads the lists: no file of this package may
// name a machine's own local config, which is the copy one operator has and
// which would admit whoever that machine last wrote.
func TestTheListsComeFromTheInstallationsOwnState(t *testing.T) {
	lists := testLists()
	ts := newResourceServer(t, WithRoster(rosterFunc(func(context.Context) (Members, error) { return lists, nil })))
	ts.serve(t, "get_project_state", func(context.Context, Call) (any, error) { return "ok", nil })
	_, tokens := ts.runFlow(t, testClientRedirect)
	wantResult(t, ts.rpc(t, tokens.AccessToken, toolsListBody))

	lists.Launchers = nil
	wantBearerRefusal(t, ts.rpc(t, tokens.AccessToken, toolsListBody), http.StatusForbidden, "insufficient_scope")

	lists.Launchers = []string{testIdentity}
	wantResult(t, ts.rpc(t, tokens.AccessToken, toolsListBody))

	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, forbidden := range []string{"internal/localcfg", "UserHomeDir"} {
			if strings.Contains(string(data), forbidden) {
				t.Errorf("%s names %q: the lists this door reads are the installation's published state, and one machine's local config lists only the members that machine knows about", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading this package's own source: %v", err)
	}
}

// TestAnUnauthenticatedRefusalIsNotWrittenToTheDatabase is D920's other half:
// the refusals are logged and never recorded in the database. A caller with no
// token at all can reach this endpoint as often as it likes, and the
// installation must not grow by one object for it — the audit record is written
// for calls that happened, and an unauthenticated caller is not an event the
// store is asked to remember.
func TestAnUnauthenticatedRefusalIsNotWrittenToTheDatabase(t *testing.T) {
	// Nobody is on the lists: the flow still mints a token — the lists decide
	// who may call a tool and not who may sign in — and the door refuses it,
	// which is the third shape below.
	ts := newResourceServer(t, WithRoster(rosterOf(Members{})))
	ts.serve(t, "get_project_state", func(context.Context, Call) (any, error) { return "ok", nil })
	_, tokens := ts.runFlow(t, testClientRedirect)

	objects := len(ts.store.objects(t))
	writes := len(ts.counting.keys)
	if objects == 0 {
		t.Fatal("the flow wrote nothing at all: this test would pass over a door that writes nothing ever")
	}

	for _, tc := range []struct {
		what   string
		header string
		body   string
	}{
		{"a request with no credential", "", toolsListBody},
		{"a call with no credential", "", callBody},
		{"a value this installation never minted", "Bearer " + testSeededToken, toolsListBody},
		{"a body nothing can read, with no credential", "", `{"jsonrpc":`},
		{"a token this installation minted for somebody off its lists", "Bearer " + tokens.AccessToken, callBody},
	} {
		t.Run(tc.what, func(t *testing.T) {
			rec := ts.rpcHeader(t, tc.header, tc.body)
			if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
				t.Fatalf("the request was answered %d (%s); want a refusal", rec.Code, strings.TrimSpace(rec.Body.String()))
			}
			if got := len(ts.counting.keys); got != writes {
				t.Errorf("the refusal wrote through the store: %d objects are named now and %d were before, %v", got, writes, ts.counting.keys)
			}
			if got := len(ts.store.objects(t)); got != objects {
				t.Errorf("the refusal left %d objects in the bucket where %d were: an unauthenticated caller must not be able to grow this installation", got, objects)
			}
		})
	}
}

// TestARefusalIsLoggedAndCarriesNoToken is the other half of M-e ("an
// unauthenticated refusal is logged and never written to the database"): the
// database half is above, and this is the log. A public door has to leave the
// operator of the installation something to see, and what it must never leave
// is a value — the line is written from the refusal and not from the request,
// so the token a caller presented and the pair a flow minted appear in no field
// of it (spec.md §9's "not in a log line").
func TestARefusalIsLoggedAndCarriesNoToken(t *testing.T) {
	for _, name := range config.ModelSecretEnvs {
		t.Setenv(name, testCredentialPlaceholder)
	}

	var log bytes.Buffer
	// Nobody is on the lists, which is what makes the flow's own token
	// refusable: the flow mints it — the lists decide who may call a tool and
	// not who may sign in — and the door turns it away by identity.
	ts := newResourceServer(t,
		WithRoster(rosterOf(Members{})),
		WithLogger(slog.New(slog.NewTextHandler(&log, nil))))
	_, tokens := ts.runFlow(t, testClientRedirect)

	refusals := []struct {
		what   string
		header string
		status int
	}{
		{"a request with no credential", "", http.StatusUnauthorized},
		{"a value this installation never minted", "Bearer " + testSeededToken, http.StatusUnauthorized},
		{"a token minted for somebody off the lists", "Bearer " + tokens.AccessToken, http.StatusForbidden},
	}
	for _, tc := range refusals {
		if rec := ts.rpcHeader(t, tc.header, toolsListBody); rec.Code != tc.status {
			t.Fatalf("%s was answered %d (%s); want %d", tc.what, rec.Code, strings.TrimSpace(rec.Body.String()), tc.status)
		}
	}

	var lines []string
	for _, line := range strings.Split(log.String(), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != len(refusals) {
		t.Fatalf("the door wrote %d log lines for %d refusals:\n%s", len(lines), len(refusals), log.String())
	}
	for _, line := range lines {
		if !strings.Contains(line, "level=WARN") || !strings.Contains(line, "the MCP endpoint refused a request") {
			t.Errorf("a refusal was logged as %q; want a WARN line saying the MCP endpoint refused a request", line)
		}
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "identity="+testIdentity) {
		t.Errorf("the refusal of a token acting for %s does not name it: %q", testIdentity, last)
	}
	for _, value := range []string{tokens.AccessToken, tokens.RefreshToken, testSeededToken, testCredentialPlaceholder} {
		if value != "" && strings.Contains(log.String(), value) {
			t.Errorf("a refusal's log line carries %q: a refusal is logged as the outcome it is, never as the value that was presented", value)
		}
	}
}

// TestAToolAnswerCarriesNoCredential is spec.md §9 held as a property of the
// door rather than of each tool: a handler is handed the caller and never the
// request, so no answer it can write carries the model credentials the
// installation holds, and none carries the token it was called with. The
// handler below answers with the whole of what it was given, which is the most
// a tool could leak from the call itself.
func TestAToolAnswerCarriesNoCredential(t *testing.T) {
	if len(config.ModelSecretEnvs) == 0 {
		t.Fatal("no model credential is named: this test would pass over an installation that holds none")
	}
	forbidden := []string{"FUGARO_PROVIDER_KEY_OPENROUTER"}
	forbidden = append(forbidden, config.ModelSecretEnvs...)
	for _, name := range forbidden {
		t.Setenv(name, testCredentialPlaceholder)
	}

	ts := newResourceServer(t)
	for _, spec := range toolCatalogue {
		ts.serve(t, spec.name, func(ctx context.Context, call Call) (any, error) { return call, nil })
	}
	_, tokens := ts.runFlow(t, testClientRedirect)
	forbidden = append(forbidden, testCredentialPlaceholder, tokens.AccessToken, tokens.RefreshToken)

	for _, spec := range toolCatalogue {
		t.Run(spec.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":{"repo":"a-repo"}}}`, spec.name)
			text, isError := wantToolText(t, wantResult(t, ts.rpc(t, tokens.AccessToken, body)))
			if isError {
				t.Fatalf("the tool refused: %s", text)
			}
			for _, forbidden := range forbidden {
				if strings.Contains(text, forbidden) {
					t.Errorf("the answer of %s carries %q: a tool answers data and no answer carries a credential, so that nothing an agent reads can be the value another tool would spend (spec.md §9)", spec.name, forbidden)
				}
			}
		})
	}

	t.Run("a refusal names no credential either", func(t *testing.T) {
		ans := wantFailure(t, ts.rpc(t, tokens.AccessToken, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"roadmap_plan","arguments":{}}}`), http.StatusOK, codeInvalidParams)
		for _, forbidden := range forbidden {
			if strings.Contains(ans.Error.Message, forbidden) {
				t.Errorf("the refusal carries %q: %s", forbidden, ans.Error.Message)
			}
		}
	})
}

// testCredentialPlaceholder is what the test puts in the environment where a
// model credential would be. It is a marker and not a credential: it is shaped
// like nothing a provider issues, and the test's own point is that this value
// reaches no answer.
const testCredentialPlaceholder = "placeholder-value-not-a-credential"
