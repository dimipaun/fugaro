// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// This file is P9f's login half: the flow, the loopback listener, the keychain
// item, and the one property that decides the design — the refresh token is in
// the keychain and in no file.
//
// The front door is a fake of this file's own (fakeFrontDoor): the real one
// needs a device approval with a Firebase ID token, which is a person and a
// phone, and what is under test here is the client. The fake is not a stub of
// the answers, though — it runs the parts of the protocol a client can get
// wrong and checks them: it registers the redirect, it refuses a token request
// whose verifier does not hash to the challenge it was given, and it refuses
// the MCP call of a token it did not mint. A client that skipped PKCE, guessed
// the port or reused a spent refresh token fails here.

// fakeToolCall is one tools/call the fake door served, kept so that a test can
// assert what the CLI put on the wire rather than what it printed.
type fakeToolCall struct {
	Name      string
	Arguments map[string]any
}

// fakeFrontDoor is the installation's front door in process: the four OAuth
// endpoints and the MCP one, over loopback HTTP.
type fakeFrontDoor struct {
	t      *testing.T
	server *httptest.Server

	mu sync.Mutex
	// The registration and the authorization the client asked for.
	registeredRedirect string
	authorizeQuery     url.Values
	code               string
	// The tokens the door has minted. refresh is the one that works now: a
	// rotation replaces it, so a client that presents the old one is refused.
	access  string
	refresh string
	minted  int
	// What the door answers a write with.
	status  string
	reason  string
	current string
	// calls and kinds are what the door served and what it queued.
	calls []fakeToolCall
	kinds map[string]string
	// refuseStatus, when set, is the status every MCP call is answered with
	// instead of being served.
	refuseStatus int
}

func newFakeFrontDoor(t *testing.T) *fakeFrontDoor {
	t.Helper()
	// minted starts at one so that the first token the door mints is not the
	// one a test seeded: a fixture whose successor is its predecessor would
	// pass a rotation test that asserted nothing.
	d := &fakeFrontDoor{
		t:       t,
		refresh: "refresh-1",
		minted:  1,
		kinds:   map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(mcpauth.RegisterPath, d.register)
	mux.HandleFunc(mcpauth.AuthorizePath, d.authorize)
	mux.HandleFunc(mcpauth.TokenPath, d.token)
	mux.HandleFunc(mcpauth.MCPPath, d.mcp)
	d.server = httptest.NewServer(mux)
	t.Cleanup(d.server.Close)
	return d
}

// URL is where the fake door is served, which is what the installation's
// outputs would publish.
func (d *fakeFrontDoor) URL() string { return d.server.URL }

// refreshToken is the refresh token that works now, so that a test seeding a
// credential seeds the one the door will accept.
func (d *fakeFrontDoor) refreshToken() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.refresh
}

// clientID is the registration the door minted.
func (d *fakeFrontDoor) clientID() string { return "cli-client-1" }

// serve sets what the door answers a queued request with, by status.
func (d *fakeFrontDoor) serve(status, reason, current string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status, d.reason, d.current = status, reason, current
}

// toolCalls is what the door was asked to do, in order.
func (d *fakeFrontDoor) toolCalls() []fakeToolCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]fakeToolCall(nil), d.calls...)
}

func (d *fakeFrontDoor) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.RedirectURIs) != 1 {
		d.t.Errorf("the registration named no single redirect URI: %v", err)
	}
	d.mu.Lock()
	if len(req.RedirectURIs) == 1 {
		d.registeredRedirect = req.RedirectURIs[0]
	}
	d.mu.Unlock()
	writeFakeJSON(w, http.StatusCreated, map[string]any{
		"client_id": d.clientID(), "token_endpoint_auth_method": "none",
		"redirect_uris": req.RedirectURIs,
	})
}

// authorize is the page a person's browser loads. The fake answers the code in
// its body rather than rendering a consent form; the browser stub below reads
// it and follows the redirect, which is the part of the flow this file tests.
func (d *fakeFrontDoor) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d.mu.Lock()
	d.authorizeQuery = q
	d.minted++
	d.code = fmt.Sprintf("code-%d", d.minted)
	code := d.code
	d.mu.Unlock()
	writeFakeJSON(w, http.StatusOK, map[string]string{"code": code})
}

func (d *fakeFrontDoor) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	d.mu.Lock()
	challenge, code, redirect := d.authorizeQuery.Get("code_challenge"), d.code, d.registeredRedirect
	d.mu.Unlock()
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		// The verifier is checked against the challenge the door was given,
		// which is the whole of PKCE: a client that sent none, or sent a
		// verifier that does not hash to it, is refused here.
		switch {
		case r.Form.Get("code") != code:
			writeFakeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "the code is not the one this door issued"})
			return
		case r.Form.Get("redirect_uri") != redirect:
			writeFakeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "the redirect URI is not the one this client registered"})
			return
		case r.Form.Get("client_id") != d.clientID():
			writeFakeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client", "error_description": "no such client"})
			return
		case challenge == "" || pkceChallenge(r.Form.Get("code_verifier")) != challenge:
			writeFakeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "the code verifier does not match the challenge"})
			return
		}
	case "refresh_token":
		d.mu.Lock()
		spent := r.Form.Get("refresh_token") != d.refresh
		d.mu.Unlock()
		if spent {
			writeFakeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "this refresh token was already spent"})
			return
		}
	default:
		writeFakeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	d.mu.Lock()
	d.minted++
	d.access = fmt.Sprintf("access-%d", d.minted)
	d.refresh = fmt.Sprintf("refresh-%d", d.minted)
	access, refresh := d.access, d.refresh
	d.mu.Unlock()
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"access_token": access, "token_type": "Bearer", "expires_in": 600,
		"refresh_token": refresh, "scope": mcpauth.ScopeRoadmapWrite,
	})
}

func (d *fakeFrontDoor) mcp(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	refuse, access := d.refuseStatus, d.access
	d.mu.Unlock()
	if refuse != 0 {
		writeFakeJSON(w, refuse, map[string]string{"error": "invalid_token", "message": "this token was revoked"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+access || access == "" {
		writeFakeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token", "message": "no token of this door's"})
		return
	}
	var req struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Method != "tools/call" {
		d.rpcError(w, -32601, "only tools/call is served here")
		return
	}
	if _, ok := mcpauth.ToolScope(req.Params.Name); !ok {
		d.rpcError(w, -32601, "no such tool")
		return
	}
	d.mu.Lock()
	d.calls = append(d.calls, fakeToolCall{Name: req.Params.Name, Arguments: req.Params.Arguments})
	d.mu.Unlock()
	if req.Params.Name == "roadmap_request" {
		id, _ := req.Params.Arguments["id"].(string)
		outcome, ok := d.outcome(id)
		if !ok {
			d.rpcError(w, -32602, mcpauth.ErrRoadmapRequestUnknown.Error())
			return
		}
		d.answer(w, req.ID, outcome)
		return
	}
	kind, ok := fakeRequestKind(req.Params.Name)
	if !ok {
		d.rpcError(w, -32601, "no such tool")
		return
	}
	id := d.queue(kind)
	d.answer(w, req.ID, map[string]any{
		"version": coordinatorOutcomeVersion, "request": id, "kind": kind, "asked": req.Params.Arguments,
	})
}

// fakeRequestKind is the tool name → request kind mapping the door's own
// catalogue carries, spelled here so that a test asserting the kind a command
// asked for is asserting the door's vocabulary and not this file's.
func fakeRequestKind(tool string) (string, bool) {
	switch tool {
	case "roadmap_need":
		return string(mcpauth.RequestNeed), true
	case "roadmap_unneed":
		return string(mcpauth.RequestUnneed), true
	case "roadmap_release_set":
		return string(mcpauth.RequestReleaseSet), true
	default:
		return "", false
	}
}

func (d *fakeFrontDoor) queue(kind string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := fmt.Sprintf("req-%d", len(d.kinds)+1)
	d.kinds[id] = kind
	return id
}

func (d *fakeFrontDoor) outcome(id string) (map[string]any, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	kind, ok := d.kinds[id]
	if !ok {
		return nil, false
	}
	status := d.status
	if status == "" {
		status = mcpauth.RoadmapApplied
	}
	out := map[string]any{"version": coordinatorOutcomeVersion, "request": id, "kind": kind, "status": status}
	if d.reason != "" {
		out["reason"] = d.reason
	}
	if d.current != "" {
		out["current"] = d.current
	}
	return out, true
}

func (d *fakeFrontDoor) answer(w http.ResponseWriter, id int, out map[string]any) {
	text, err := json.Marshal(out)
	if err != nil {
		d.rpcError(w, -32603, "the fake could not render its answer")
		return
	}
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0", "id": id,
		"result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(text)}},
			"isError": false,
		},
	})
}

func (d *fakeFrontDoor) rpcError(w http.ResponseWriter, code int, message string) {
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0", "id": 1,
		"error": map[string]any{"code": code, "message": message},
	})
}

// openBrowser is the browser the flow needs and the tests cannot have: it
// loads the authorization URL the CLI printed, reads the code the door put in
// the page, and follows the redirect the door told it to — which is the one
// request that reaches the CLI's loopback listener.
func (d *fakeFrontDoor) openBrowser(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	resp, err := http.Get(raw)
	if err != nil {
		return err
	}
	var page struct {
		Code string `json:"code"`
	}
	err = json.NewDecoder(resp.Body).Decode(&page)
	resp.Body.Close()
	if err != nil {
		return err
	}
	target := u.Query().Get("redirect_uri") + "?code=" + url.QueryEscape(page.Code) + "&state=" + url.QueryEscape(u.Query().Get("state"))
	answer, err := http.Get(target)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, answer.Body)
	return answer.Body.Close()
}

func writeFakeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// pkceChallenge is RFC 7636's S256, which is the only method the door accepts.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// withFrontDoor points the CLI's seams at a fake door and a fake keychain, and
// puts them back when the test ends. The browser is the fake's own, so no test
// here opens a window.
func withFrontDoor(t *testing.T, door *fakeFrontDoor, store keychain.Store) {
	t.Helper()
	prevKeychain, prevURL, prevBrowser := coordinatorKeychain, coordinatorFrontDoorURL, coordinatorOpenBrowser
	t.Cleanup(func() {
		coordinatorKeychain, coordinatorFrontDoorURL, coordinatorOpenBrowser = prevKeychain, prevURL, prevBrowser
	})
	coordinatorKeychain = func() keychain.Store { return store }
	coordinatorFrontDoorURL = func(context.Context, *localcfg.Config) (string, error) { return door.URL(), nil }
	coordinatorOpenBrowser = door.openBrowser
}

// seedCredential is the state a person is in after a login: the keychain holds
// the credential a previous flow wrote. The tests that are not about the flow
// start here.
func seedCredential(t *testing.T, store *keychain.Fake, door *fakeFrontDoor, project string) {
	t.Helper()
	item, err := keychain.ParseItem(project+coordinatorCredentialSuffix, project)
	if err != nil {
		t.Fatal(err)
	}
	err = coordinatorStoreCredential(context.Background(), store, item, frontDoorCredential{
		Issuer: door.URL(), ClientID: door.clientID(), RefreshToken: door.refreshToken(),
		Scopes: []string{mcpauth.ScopeRoadmapWrite},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestLoginUsesPKCEAndALoopbackRedirect is the flow end to end: the client
// registers the redirect its own listener is bound to, sends an S256 challenge
// to the authorization endpoint, and spends the code with the verifier that
// hashes to it. The fake door refuses the exchange unless all of that is true,
// so a client that skipped PKCE cannot pass this test by printing the right
// sentence.
func TestLoginUsesPKCEAndALoopbackRedirect(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)

	out, _, err := execute(t, "coordinator", "login", "--scope", "roadmap:write", "--json")
	if err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	door.mu.Lock()
	redirect, q := door.registeredRedirect, door.authorizeQuery
	door.mu.Unlock()

	// The listener binds 127.0.0.1 literally and never `localhost`: the name is
	// resolved by whatever the machine's resolver says, and the address is what
	// the door's own rule is written in.
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("the registered redirect %q is not a URL: %v", redirect, err)
	}
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		t.Errorf("the client registered %q; want a loopback redirect on the literal 127.0.0.1", redirect)
	}
	if u.Path != coordinatorCallbackPath {
		t.Errorf("the client registered %q; want the path %q", redirect, coordinatorCallbackPath)
	}
	if u.Port() == "" {
		t.Errorf("the client registered %q, which names no port", redirect)
	}
	switch {
	case q.Get("response_type") != "code":
		t.Errorf("response_type is %q; want code", q.Get("response_type"))
	case q.Get("code_challenge_method") != "S256":
		t.Errorf("code_challenge_method is %q; want S256 (the door refuses an absent method rather than reading RFC 7636's plain default)", q.Get("code_challenge_method"))
	case len(q.Get("code_challenge")) != 43:
		t.Errorf("code_challenge is %q, which is not the 43 characters of an unpadded base64url SHA-256", q.Get("code_challenge"))
	case q.Get("state") == "":
		t.Error("the authorization request carried no state")
	case q.Get("scope") != mcpauth.ScopeRoadmapWrite:
		t.Errorf("scope is %q; want %q", q.Get("scope"), mcpauth.ScopeRoadmapWrite)
	}

	// What the login left behind: the credential in the keychain, and nothing
	// of it on standard output.
	var doc coordinatorLoginDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("login --json printed %q, which is not the login document: %v", out, err)
	}
	if doc.Issuer != door.URL() || doc.Project != "aurora" {
		t.Errorf("login --json printed %+v; want the door's URL and the project's name", doc)
	}
	item, err := keychain.ParseItem("aurora"+coordinatorCredentialSuffix, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Keys[item.Name()]; !ok {
		t.Fatalf("the keychain holds %v; want the item %q", store.Keys, item.Name())
	}
	cred, err := coordinatorReadCredential(context.Background(), store, item)
	if err != nil {
		t.Fatalf("reading back what login stored: %v", err)
	}
	if cred.RefreshToken != door.refreshToken() {
		t.Error("the credential does not hold the refresh token the door minted")
	}
	if strings.Contains(out, cred.RefreshToken) || strings.Contains(out, "code-1") {
		t.Errorf("login printed something of the flow it must not:\n%s", out)
	}
}

// TestTheRefreshTokenNeverTouchesAFile is the property the whole design turns
// on: the token is in the keychain and in no file anywhere the process could
// have written one. Every directory a process on this machine would reach for
// is redirected under one temporary root, and the root is walked afterwards.
//
// The mutation this catches is the obvious one — writing the refresh token
// into the local config beside the other project settings — which is exactly
// what a reader would have to do to find it here.
func TestTheRefreshTokenNeverTouchesAFile(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)

	root := t.TempDir()
	for _, name := range []string{"home", "config", "cache", "state", "data", "tmp"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("TMPDIR", filepath.Join(root, "tmp"))

	out, errOut, err := execute(t, "coordinator", "login", "--scope", "roadmap:write")
	if err != nil {
		t.Fatalf("login: %v\n%s", err, errOut)
	}
	token := door.refreshToken()
	if token == "" {
		t.Fatal("the door minted no refresh token, so this test would pass on nothing")
	}
	if where, found := fileHolding(root, token); found {
		t.Errorf("the refresh token was written to %s", where)
	}
	// The project's own config is where the mutation puts it, and it is inside
	// the tree the walk above covers — this is the assertion that says so.
	if where, found := fileHolding(filepath.Dir(os.Getenv("FUGARO_CONFIG")), token); found {
		t.Errorf("the refresh token is in the project's config directory at %s", where)
	}
	if strings.Contains(out+errOut, token) {
		t.Error("the refresh token was printed")
	}
}

// fileHolding answers the first file under root that holds want.
func fileHolding(root, want string) (string, bool) {
	var found string
	_ = filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() || found != "" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(data), want) {
			found = path
		}
		return nil
	})
	return found, found != ""
}

// TestLogoutDeletesTheStoredCredential is the other half of the item's life:
// what login wrote, logout removes, and an item that is not there is not an
// error — the state a person asks for is the state they get.
func TestLogoutDeletesTheStoredCredential(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	seedCredential(t, store, door, "aurora")

	item, err := keychain.ParseItem("aurora"+coordinatorCredentialSuffix, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Keys[item.Name()]; !ok {
		t.Fatalf("the fixture seeded %v; want the item %q", store.Keys, item.Name())
	}
	out, _, err := execute(t, "coordinator", "logout", "--json")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, ok := store.Keys[item.Name()]; ok {
		t.Errorf("logout left %q in the keychain", item.Name())
	}
	var doc coordinatorLogoutDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil || !doc.Removed {
		t.Errorf("logout --json printed %q; want the removal document", out)
	}
	if _, _, err := execute(t, "coordinator", "logout"); err != nil {
		t.Errorf("a second logout is not an error, and was answered %v", err)
	}
}

// TestLoginRefusesWithoutAFrontDoor is the installation whose switch is off
// (D934): its outputs publish no mcp_server, and the command says so in one
// sentence rather than calling a URL it guessed.
func TestLoginRefusesWithoutAFrontDoor(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	prev := coordinatorFrontDoorURL
	t.Cleanup(func() { coordinatorFrontDoorURL = prev })
	coordinatorFrontDoorURL = func(context.Context, *localcfg.Config) (string, error) { return "", errFrontDoorOff }

	_, _, err := execute(t, "coordinator", "login", "--scope", "roadmap:write")
	if err == nil {
		t.Fatal("login succeeded on an installation whose front door is off")
	}
	if !strings.Contains(err.Error(), "front door is off") {
		t.Errorf("the refusal is %q; want it to say the front door is off", err)
	}
	if len(store.Keys) != 0 {
		t.Errorf("a refused login stored %v", store.Keys)
	}
}

// TestLoginRefusesAnUnknownScopeLocally keeps a typo from costing a browser
// round trip: the vocabulary is the front door's own constant, so the two
// cannot drift apart.
func TestLoginRefusesAnUnknownScopeLocally(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	withFrontDoor(t, door, &keychain.Fake{})

	_, _, err := execute(t, "coordinator", "login", "--scope", "roadmap:write-please")
	if err == nil {
		t.Fatal("login accepted a scope this front door does not mint")
	}
	if !strings.Contains(err.Error(), mcpauth.ScopeRoadmapWrite) {
		t.Errorf("the refusal is %q; want it to name the vocabulary", err)
	}
	if door.authorizeQuery != nil {
		t.Error("the client reached the authorization endpoint for a scope it could have refused itself")
	}
}

// TestFrontDoorURLIsReadFromTheInstallationsOutputs pins the one place the URL
// comes from: the installation's own outputs, and the off state as the null
// the module writes there.
func TestFrontDoorURLIsReadFromTheInstallationsOutputs(t *testing.T) {
	newCloudFixture(t)
	prev := coordinatorInstallationOutputs
	t.Cleanup(func() { coordinatorInstallationOutputs = prev })

	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		outputs string
		want    string
		wantErr error
	}{
		{"a front door", `{"mcp_server":{"name":"coordinator","region":"us-east5","url":"https://coordinator-abc-uc.a.run.app"}}`, "https://coordinator-abc-uc.a.run.app", nil},
		{"a trailing slash", `{"mcp_server":{"url":"https://coordinator-abc-uc.a.run.app/"}}`, "https://coordinator-abc-uc.a.run.app", nil},
		{"the switch off", `{"mcp_server":null}`, "", errFrontDoorOff},
		{"an output this build does not read", `{"mcp_server":{"url":""}}`, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coordinatorInstallationOutputs = func(context.Context, *localcfg.Config) (map[string]json.RawMessage, error) {
				var raw map[string]json.RawMessage
				if err := json.Unmarshal([]byte(tc.outputs), &raw); err != nil {
					t.Fatal(err)
				}
				return raw, nil
			}
			got, err := readCoordinatorFrontDoorURL(context.Background(), lc)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("read answered %v; want %v", err, tc.wantErr)
				}
			case tc.want == "":
				if err == nil {
					t.Fatalf("read answered %q; want a refusal", got)
				}
			default:
				if err != nil {
					t.Fatalf("read: %v", err)
				}
				if got != tc.want {
					t.Errorf("read answered %q; want %q", got, tc.want)
				}
			}
		})
	}
}

// TestTheFrontDoorAddressIsNeverAPlainRemoteHost is the one downgrade this
// client can refuse before it has a token to lose: both the access token and
// the refresh token travel to this URL.
func TestTheFrontDoorAddressIsNeverAPlainRemoteHost(t *testing.T) {
	for _, tc := range []struct {
		url  string
		ok   bool
		what string
	}{
		{"https://coordinator-abc-uc.a.run.app", true, "the installation's own Cloud Run URL"},
		{"http://127.0.0.1:8080", true, "a door on this machine"},
		{"http://localhost:8080", true, "a door on this machine, named"},
		{"http://[::1]:8080", true, "a door on this machine, over v6"},
		{"http://coordinator.example.com", false, "a plain-http host that is not this one"},
		{"http://127.0.0.1.evil.example.com", false, "a name that merely starts with a loopback address"},
		{"ftp://coordinator.example.com", false, "a scheme that carries no credential"},
	} {
		err := coordinatorFrontDoorAddress(tc.url)
		if tc.ok && err != nil {
			t.Errorf("%s: %v", tc.what, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s (%s) was accepted", tc.what, tc.url)
		}
	}
}

// TestALoginThatWasNeverAnsweredStoresNothing is the bound on the wait: a
// person who never finishes leaves no half a credential behind.
func TestALoginThatWasNeverAnsweredStoresNothing(t *testing.T) {
	newCloudFixture(t)
	door := newFakeFrontDoor(t)
	store := &keychain.Fake{}
	withFrontDoor(t, door, store)
	// A browser that loads the page and never follows the redirect is the
	// ordinary way a sign-in is abandoned.
	coordinatorOpenBrowser = func(string) error { return nil }
	prevWait := coordinatorLoginWait
	t.Cleanup(func() { coordinatorLoginWait = prevWait })
	coordinatorLoginWait = 20 * time.Millisecond

	_, _, err := execute(t, "coordinator", "login", "--scope", "roadmap:write")
	if err == nil {
		t.Fatal("login succeeded with no answer to the redirect")
	}
	if !strings.Contains(err.Error(), "loopback listener") {
		t.Errorf("the refusal is %q; want it to name the loopback listener", err)
	}
	if len(store.Keys) != 0 {
		t.Errorf("an abandoned login stored %v", store.Keys)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("exit code %d; want %d", fcli.ExitCode(err), fcli.ExitUserError)
	}
}
