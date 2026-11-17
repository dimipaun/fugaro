// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The Paddle client's own cases (P4.1; F15).
//
// The seam's contract is what the rest of this package is written against
// (api.go's `Paddle`), so what these cases are about is the half no fake can
// show: the bytes this service puts on the wire. Three of them are properties
// and not shapes, and they are the ones worth the file —
//
//   - the merchant's key travels to the host the deployment's environment names
//     and to no other, which is why a redirect is refused: an answer that could
//     move the next request would move the key with it;
//   - the `custom_data` is built here from what the caller passed and never
//     from anything a body could reach (F15's whole finding);
//   - a seat change keeps the price the customer is on, so the one act that
//     changes a quantity can never become the act that changes a price.
//
// The server is `httptest`'s and nothing here reaches Paddle: the account's own
// answers are the half of this repository that is a live check (P9.4), and what
// is pinned here is what this service sends and how it reads an answer back.
// The key below is fixture text and not a credential.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// paddleTestKey is fixture text shaped like the deployment's own value, and it
// exists to be found in one place only: the Authorization header.
const paddleTestKey = "fixture-paddle-key-not-a-credential"

// paddleCall is one request the case's merchant server was asked.
type paddleCall struct {
	method  string
	path    string
	target  string
	auth    string
	accept  string
	content string
	body    string
}

// paddleMerchant is Paddle in a case: what it answers, and every request it was
// asked.
type paddleMerchant struct {
	mu       sync.Mutex
	status   int
	answer   string
	location string
	calls    []paddleCall
	server   *httptest.Server
}

// merchant installs the case's merchant and builds the client against it. The
// base is the server's URL, which is the one field a deployment sets to Paddle's
// own host (NewPaddleClient) and a case sets to a server of its own.
func (m *paddleMerchant) merchant(t *testing.T, environment Environment) *PaddleClient {
	t.Helper()
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the merchant's request: %v", err)
		}
		m.mu.Lock()
		m.calls = append(m.calls, paddleCall{
			method: r.Method,
			path:   r.URL.Path,
			// target is the request as it went on the wire, which is where an
			// escaped segment is still escaped: a decoded path cannot tell an id
			// carrying a slash from a slash of the route's own.
			target:  r.RequestURI,
			auth:    r.Header.Get("Authorization"),
			accept:  r.Header.Get("Accept"),
			content: r.Header.Get("Content-Type"),
			body:    string(raw),
		})
		status, answer, location := m.status, m.answer, m.location
		m.mu.Unlock()
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(m.server.Close)
	held, err := NewPaddleClient([]byte(paddleTestKey), environment)
	if err != nil {
		t.Fatalf("building the Paddle client: %v", err)
	}
	held.base = m.server.URL
	// The transport is the case's, and the policy around it is the client's
	// own: replacing the whole client would replace the redirect rule this file
	// is partly about with the test server's.
	held.client.Transport = m.server.Client().Transport
	return held
}

// saw is every request the case's merchant was asked.
func (m *paddleMerchant) saw() []paddleCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]paddleCall(nil), m.calls...)
}

// TestPaddleClientSellsWhatTheServiceNames: one transaction, asked for with the
// price and the quantity the caller named and the organisation and workspace in
// its `custom_data`. The custom data is what an event is mapped back through
// (F15), so it is built from the arguments and from nothing else, and the key
// travels in the one header and nowhere else in the request.
func TestPaddleClientSellsWhatTheServiceNames(t *testing.T) {
	m := &paddleMerchant{status: http.StatusCreated, answer: `{"data":{"id":"txn_01","customer_id":"ctm_01"}}`}
	client := m.merchant(t, Production)

	answer, err := client.CreateTransaction(context.Background(), Checkout{
		PriceID:      "pri_team_monthly",
		Organisation: "org-1",
		Workspace:    "ws-1",
		Seats:        3,
	})
	if err != nil {
		t.Fatalf("creating a transaction: %v", err)
	}
	if answer.ID != "txn_01" || answer.CustomerID != "ctm_01" {
		t.Errorf("the merchant's answer reads as %+v, want the transaction and customer it named", answer)
	}

	calls := m.saw()
	if len(calls) != 1 {
		t.Fatalf("the merchant was asked %d time(s), want one", len(calls))
	}
	asked := calls[0]
	switch {
	case asked.method != http.MethodPost || asked.path != "/transactions":
		t.Errorf("the request is %s %s, want POST /transactions", asked.method, asked.path)
	case asked.auth != "Bearer "+paddleTestKey:
		t.Errorf("the request carries the Authorization header %q, want the deployment's key as a bearer token", asked.auth)
	case asked.content != "application/json":
		t.Errorf("the request's Content-Type is %q, want JSON", asked.content)
	}
	var body struct {
		Items []struct {
			PriceID  string `json:"price_id"`
			Quantity int    `json:"quantity"`
		} `json:"items"`
		CustomData map[string]string `json:"custom_data"`
	}
	if err := json.Unmarshal([]byte(asked.body), &body); err != nil {
		t.Fatalf("reading the request %q: %v", asked.body, err)
	}
	switch {
	case len(body.Items) != 1 || body.Items[0].PriceID != "pri_team_monthly" || body.Items[0].Quantity != 3:
		t.Errorf("the request sells %+v, want one item of the price and quantity the caller named", body.Items)
	case body.CustomData["org_id"] != "org-1" || body.CustomData["workspace_id"] != "ws-1":
		t.Errorf("the request's custom_data is %v, want the organisation and workspace the caller named", body.CustomData)
	case strings.Contains(asked.body, paddleTestKey):
		t.Error("the request's body carries the deployment's key, which belongs in one header only")
	}
}

// TestPaddleClientNeverFollowsARedirect: an answer that names another host is
// refused rather than followed. The key is a bearable-in-one-place secret: a
// redirect is not the merchant's answer, and following one would send it, on the
// next request, to whatever host that answer named.
func TestPaddleClientNeverFollowsARedirect(t *testing.T) {
	elsewhere := &paddleMerchant{status: http.StatusOK, answer: `{"data":{"id":"txn_elsewhere"}}`}
	elsewhere.merchant(t, Production)
	t.Cleanup(elsewhere.server.Close)

	redirects := &paddleMerchant{status: http.StatusTemporaryRedirect, location: elsewhere.server.URL + "/transactions"}
	client := redirects.merchant(t, Production)

	_, err := client.CreateTransaction(context.Background(), Checkout{
		PriceID: "pri_team_monthly", Organisation: "org-1", Workspace: "ws-1", Seats: 1,
	})
	if err == nil {
		t.Fatal("a redirected transaction answered no error")
	}
	if seen := elsewhere.saw(); len(seen) != 0 {
		t.Errorf("the other host was asked %d time(s), want none: the key follows the merchant's own host and no answer's", len(seen))
	}
}

// TestPaddleClientQuotesTheMerchantsRefusal: a refusal is the merchant's own
// code and detail, carried into the error so whoever reads the API's report can
// see what Paddle said — and never a body that is not Paddle's shape, which is
// answered as exactly that. The key is in no error text at all: an error is
// logged.
func TestPaddleClientQuotesTheMerchantsRefusal(t *testing.T) {
	refusing := &paddleMerchant{status: http.StatusUnprocessableEntity, answer: `{"error":{"type":"request_error","code":"transaction_create_invalid_price","detail":"The price is not one of this account's."}}`}
	client := refusing.merchant(t, Production)

	_, err := client.CreateTransaction(context.Background(), Checkout{
		PriceID: "pri_gone", Organisation: "org-1", Workspace: "ws-1", Seats: 1,
	})
	if err == nil {
		t.Fatal("a refused transaction answered no error")
	}
	if !strings.Contains(err.Error(), "transaction_create_invalid_price") {
		t.Errorf("the error is %q, want the merchant's own code in it", err)
	}
	if strings.Contains(err.Error(), paddleTestKey) {
		t.Errorf("the error carries the deployment's key: %q", err)
	}

	// A failure page is not Paddle's shape, and it is not quoted: what a log
	// holds is then this service's own words about an unreadable answer.
	opaque := &paddleMerchant{status: http.StatusBadGateway, answer: `<html>upstream is unwell</html>`}
	client = opaque.merchant(t, Production)
	_, err = client.CreateTransaction(context.Background(), Checkout{
		PriceID: "pri_team_monthly", Organisation: "org-1", Workspace: "ws-1", Seats: 1,
	})
	if err == nil {
		t.Fatal("an unreadable failure answered no error")
	}
	if strings.Contains(err.Error(), "upstream is unwell") {
		t.Errorf("the error quotes a body that is not the merchant's shape: %q", err)
	}
}

// TestPaddleClientSetsTheSubscriptionsWholeQuantity: a seat change reads the
// subscription first and writes the quantity back with the price it is on. That
// is two calls and not one because Paddle replaces a subscription's whole item
// list on an update: this service knows the quantity a customer asked for and
// not the price they bought under, and a price it guessed would be a price this
// act changed. The quantity is the whole one, so a lost answer can be sent
// again.
func TestPaddleClientSetsTheSubscriptionsWholeQuantity(t *testing.T) {
	m := &paddleMerchant{status: http.StatusOK, answer: `{"data":{"id":"sub_01","items":[{"price_id":"pri_team_monthly","quantity":3}]}}`}
	client := m.merchant(t, Staging)

	if err := client.SetSeats(context.Background(), "sub_01", 7); err != nil {
		t.Fatalf("setting the seats: %v", err)
	}
	calls := m.saw()
	if len(calls) != 2 {
		t.Fatalf("the merchant was asked %d time(s), want a read and an update", len(calls))
	}
	if calls[0].method != http.MethodGet || calls[0].path != "/subscriptions/sub_01" {
		t.Errorf("the first call is %s %s, want the subscription read back", calls[0].method, calls[0].path)
	}
	asked := calls[1]
	if asked.method != http.MethodPatch || asked.path != "/subscriptions/sub_01" {
		t.Fatalf("the second call is %s %s, want the subscription updated", asked.method, asked.path)
	}
	var body struct {
		Items []struct {
			PriceID  string `json:"price_id"`
			Quantity int    `json:"quantity"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(asked.body), &body); err != nil {
		t.Fatalf("reading the update %q: %v", asked.body, err)
	}
	switch {
	case len(body.Items) != 1 || body.Items[0].Quantity != 7:
		t.Errorf("the update asks for %+v, want one item of seven seats", body.Items)
	case body.Items[0].PriceID != "pri_team_monthly":
		t.Errorf("the update names the price %q, want the subscription's own", body.Items[0].PriceID)
	}

	// A subscription carrying something other than the one price this portal
	// sells is not this service's to change, and it is refused before anything
	// is written.
	two := &paddleMerchant{status: http.StatusOK, answer: `{"data":{"items":[{"price_id":"pri_a"},{"price_id":"pri_b"}]}}`}
	client = two.merchant(t, Staging)
	if err := client.SetSeats(context.Background(), "sub_02", 7); err == nil {
		t.Fatal("a two-item subscription was changed")
	} else if len(two.saw()) != 1 {
		t.Errorf("the merchant was asked %d time(s) for a subscription this service does not sell, want the read alone", len(two.saw()))
	}
}

// TestPaddleClientReadsTheCustomerPortalSession: the session is minted for one
// customer and the answer is the URL a person is sent to, which is the portal's
// link out (spec §Customer experience)'s only source. An id with a slash in it
// is one path segment rather than two: the id is escaped, so a customer id from
// a record can never widen the route this call reaches.
func TestPaddleClientReadsTheCustomerPortalSession(t *testing.T) {
	m := &paddleMerchant{status: http.StatusOK, answer: `{"data":{"urls":{"general":{"overview":"https://paddle.example/portal/session-1"}}}}`}
	client := m.merchant(t, Production)

	session, err := client.PortalSession(context.Background(), "ctm_01")
	if err != nil {
		t.Fatalf("minting a customer-portal session: %v", err)
	}
	if session != "https://paddle.example/portal/session-1" {
		t.Errorf("the session is %q, want the URL the merchant named", session)
	}
	calls := m.saw()
	if len(calls) != 1 || calls[0].path != "/customers/ctm_01/portal-sessions" {
		t.Fatalf("the merchant was asked %+v, want one session for the customer", calls)
	}

	// An id with a slash in it cannot widen the path: the segment is escaped, so
	// the call reaches the customer's own route and no other.
	escaped := &paddleMerchant{status: http.StatusOK, answer: `{"data":{"urls":{"general":{"overview":"https://paddle.example/portal/session-2"}}}}`}
	client = escaped.merchant(t, Production)
	if _, err := client.PortalSession(context.Background(), "ctm_01/../subscriptions"); err != nil {
		t.Fatalf("minting a session for an odd id: %v", err)
	}
	if seen := escaped.saw(); len(seen) != 1 || seen[0].target != "/customers/ctm_01%2F..%2Fsubscriptions/portal-sessions" {
		t.Errorf("the merchant was asked %+v, want the id escaped into one path segment", seen)
	}
}

// TestPaddleClientCancelsImmediately: the cancellation is the one an account's
// deletion makes (P2.4), and it takes effect at once — a subscription that ran
// to the end of its period would keep a customer who asked to be gone.
func TestPaddleClientCancelsImmediately(t *testing.T) {
	m := &paddleMerchant{status: http.StatusOK, answer: `{"data":{"id":"sub_01","status":"canceled"}}`}
	client := m.merchant(t, Production)

	if err := client.Cancel(context.Background(), "sub_01"); err != nil {
		t.Fatalf("cancelling: %v", err)
	}
	calls := m.saw()
	if len(calls) != 1 || calls[0].method != http.MethodPost || calls[0].path != "/subscriptions/sub_01/cancel" {
		t.Fatalf("the merchant was asked %+v, want one cancellation", calls)
	}
	if !strings.Contains(calls[0].body, `"effective_from":"immediately"`) {
		t.Errorf("the cancellation asks for %q, want it effective immediately", calls[0].body)
	}
}

// TestPaddleClientChoosesTheAccountItsEnvironmentNames: a staging deployment
// sells in Paddle's sandbox and a production one in its live account, each with
// its own key (P0.2). Neither can reach the other's API, and a client with no
// key or an unknown environment is refused where it is built rather than at the
// first customer's checkout.
func TestPaddleClientChoosesTheAccountItsEnvironmentNames(t *testing.T) {
	live, err := NewPaddleClient([]byte(paddleTestKey), Production)
	if err != nil {
		t.Fatalf("building the production client: %v", err)
	}
	sandbox, err := NewPaddleClient([]byte(paddleTestKey), Staging)
	if err != nil {
		t.Fatalf("building the staging client: %v", err)
	}
	switch {
	case live.base != paddleLiveAPI:
		t.Errorf("the production client reaches %q, want Paddle's live API %q", live.base, paddleLiveAPI)
	case sandbox.base != paddleSandboxAPI:
		t.Errorf("the staging client reaches %q, want Paddle's sandbox %q", sandbox.base, paddleSandboxAPI)
	}

	if _, err := NewPaddleClient(nil, Production); err == nil {
		t.Error("a client with no key was built")
	}
	if _, err := NewPaddleClient([]byte("  "), Production); err == nil {
		t.Error("a client with a blank key was built")
	}
	if _, err := NewPaddleClient([]byte(paddleTestKey), Environment("sandbox")); err == nil {
		t.Error("a client for an environment the deployment does not have was built")
	}
}
