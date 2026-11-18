// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Paddle Billing, as the portal reaches it (portal plan P4.1; F15).
//
// The `Paddle` seam (api.go) is what every area is written against; this file is
// the one implementation of it, and it is the only place the merchant's API key
// is used. It is here rather than in the core because the key is the portal's
// deploy secret and the portal is what this half of the repository is: the CLI
// never holds it.
//
// **The key travels in one header and to one host.** Every request is built
// against the base the constructor chose — Paddle's live API or its sandbox,
// named by the deployment's own environment — and the client refuses a redirect,
// which is the reason `probeInstallation` does (workspaces.go): a redirect is an
// answer this service did not ask for, and following one would send the
// merchant's key to whatever host that answer named. Nothing here is given a
// URL: the paths are this file's own constants and only the ids a caller passes
// go into them, each escaped, so no caller can reach a route this file does not
// name.
//
// **What a caller names is an id and a number.** The price is the deployment's
// (Config.PaddlePriceID), the quantity is the checkout's or the billing screen's,
// and the `custom_data` is built here from the organisation and the workspace
// this service read — never from a body (F15). A price, an amount or a
// `custom_data` from a caller has nowhere to arrive: no method of the seam takes
// one.
//
// **Paddle's own refusal is quoted and never swallowed.** A non-2xx answer is
// the merchant saying no, and the code it answers with is what the next reader
// has to see ("transaction_create_invalid_price", "subscription_not_found"), so
// it is carried into the error's own text while the key and the request body
// never are.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// The two hosts Paddle answers on. Which one a deployment reaches is its
	// environment's, and a deployment cannot reach both: staging is wired to the
	// sandbox and production to the live account, each with its own key (P0.2).
	paddleLiveAPI    = "https://api.paddle.com"
	paddleSandboxAPI = "https://sandbox-api.paddle.com"

	// paddleTimeout bounds one call. The checkout is a person waiting with a
	// page open, so a merchant that has not answered in this long is one this
	// service stops waiting for rather than one it holds the request on.
	paddleTimeout = 10 * time.Second
	// paddleMaxAnswerBytes bounds what of an answer is read. Paddle's answers
	// are small JSON documents; a bound here is what keeps a failure page or a
	// runaway body from being buffered by a service a stranger's click reaches.
	paddleMaxAnswerBytes = 64 << 10
)

// PaddleClient is the merchant of record as this deployment reaches it.
type PaddleClient struct {
	// apiKey is the deploy's secret and it is never logged, never put in an
	// error and never returned.
	apiKey []byte
	// base is the account's own host, without a trailing slash.
	base   string
	client *http.Client
}

// NewPaddleClient builds the client one deployment sells through. The
// environment is the deployment's own (Config.Environment) and it is what picks
// the account: a staging deployment sells in Paddle's sandbox and a production
// one in its live account, so a test-mode key can never be sent to the live API
// and a live key is never accepted by the sandbox.
func NewPaddleClient(apiKey []byte, environment Environment) (*PaddleClient, error) {
	if strings.TrimSpace(string(apiKey)) == "" {
		return nil, errors.New("the Paddle client has no API key, so it can reach no account: a checkout without one would be refused by the merchant for every customer")
	}
	var base string
	switch environment {
	case Production:
		base = paddleLiveAPI
	case Staging:
		base = paddleSandboxAPI
	default:
		return nil, fmt.Errorf("the Paddle client does not know the environment %q, so it does not know which account it sells in", environment)
	}
	return &PaddleClient{
		apiKey: apiKey,
		base:   base,
		client: &http.Client{
			Timeout: paddleTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// CreateTransaction opens one transaction for one purchase: the price and the
// quantity the caller named, and the organisation and workspace in the
// transaction's own `custom_data`, which is what the webhook maps an event back
// through (F15). The answer is the id the overlay is opened with and the
// customer Paddle put the transaction under.
func (p *PaddleClient) CreateTransaction(ctx context.Context, checkout Checkout) (Transaction, error) {
	type item struct {
		PriceID  string `json:"price_id"`
		Quantity int    `json:"quantity"`
	}
	type transaction struct {
		Items []item `json:"items"`
		// CustomData is what this service decides and no page does: the ids an
		// event is admitted by (F15). Both are this service's own records, read
		// before the call, so a value a body could set never arrives here.
		CustomData map[string]string `json:"custom_data"`
	}
	asked := transaction{
		Items: []item{{PriceID: checkout.PriceID, Quantity: checkout.Seats}},
		CustomData: map[string]string{
			"org_id":       checkout.Organisation,
			"workspace_id": checkout.Workspace,
		},
	}
	var answer struct {
		Data struct {
			ID         string `json:"id"`
			CustomerID string `json:"customer_id"`
		} `json:"data"`
	}
	if err := p.call(ctx, http.MethodPost, "/transactions", asked, &answer); err != nil {
		return Transaction{}, err
	}
	if strings.TrimSpace(answer.Data.ID) == "" {
		return Transaction{}, errors.New("the merchant answered a transaction with no id, so the overlay would have nothing to open")
	}
	return Transaction{ID: answer.Data.ID, CustomerID: answer.Data.CustomerID}, nil
}

// PortalSession mints one customer-portal session and answers the URL a person
// is sent to. Paddle's own portal is where invoices, payment methods and
// cancellation live (spec §Customer experience), so this is a link out and not a
// screen of this portal's.
func (p *PaddleClient) PortalSession(ctx context.Context, customerID string) (string, error) {
	var answer struct {
		Data struct {
			URLs struct {
				General struct {
					Overview string `json:"overview"`
				} `json:"general"`
			} `json:"urls"`
		} `json:"data"`
	}
	path := "/customers/" + url.PathEscape(customerID) + "/portal-sessions"
	if err := p.call(ctx, http.MethodPost, path, struct{}{}, &answer); err != nil {
		return "", err
	}
	session := answer.Data.URLs.General.Overview
	if strings.TrimSpace(session) == "" {
		return "", errors.New("the merchant answered a customer-portal session with no URL")
	}
	return session, nil
}

// SetSeats sets the whole quantity one subscription buys. It is the whole and
// not an increment (the wire's own shape), so a request whose answer was lost
// can be sent again without buying the difference twice.
//
// The subscription is read first because Paddle replaces a subscription's whole
// item list on an update and this service knows the quantity but not the price
// the customer bought under: the price is read off the subscription this service
// is changing and written back unchanged, so a seat change can never become a
// price change. Proration is Paddle's own default for an update, which is what a
// customer expects: the difference is charged at the moment they change it.
func (p *PaddleClient) SetSeats(ctx context.Context, subscriptionID string, seats int) error {
	var held struct {
		Data struct {
			Items []struct {
				PriceID string `json:"price_id"`
			} `json:"items"`
		} `json:"data"`
	}
	path := "/subscriptions/" + url.PathEscape(subscriptionID)
	if err := p.call(ctx, http.MethodGet, path, nil, &held); err != nil {
		return err
	}
	if len(held.Data.Items) != 1 || strings.TrimSpace(held.Data.Items[0].PriceID) == "" {
		return fmt.Errorf("the subscription %s carries %d item(s) and this portal sells one price on one subscription, so its seats are not this service's to set", subscriptionID, len(held.Data.Items))
	}
	type sold struct {
		PriceID  string `json:"price_id"`
		Quantity int    `json:"quantity"`
	}
	type update struct {
		Items []sold `json:"items"`
	}
	asked := update{Items: []sold{{PriceID: held.Data.Items[0].PriceID, Quantity: seats}}}
	return p.call(ctx, http.MethodPatch, path, asked, nil)
}

// Cancel ends one subscription. It is immediate, because the only caller is an
// account's deletion (P2.4): a subscription that ran to the end of a period
// would keep a customer who asked to be gone, and the licence it pays for is
// already gone with their data.
func (p *PaddleClient) Cancel(ctx context.Context, subscriptionID string) error {
	asked := struct {
		EffectiveFrom string `json:"effective_from"`
	}{EffectiveFrom: "immediately"}
	path := "/subscriptions/" + url.PathEscape(subscriptionID) + "/cancel"
	return p.call(ctx, http.MethodPost, path, asked, nil)
}

// call is the one request this client makes: one JSON body out, one JSON answer
// in. `body` is nil for a read, and `into` is nil for a call whose answer is not
// read (Paddle's own answer to a cancellation is the subscription).
func (p *PaddleClient) call(ctx context.Context, method, path string, body, into any) error {
	ctx, cancel := context.WithTimeout(ctx, paddleTimeout)
	defer cancel()
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("the request this service built is not JSON: %w", err)
		}
		payload = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, p.base+path, payload)
	if err != nil {
		return fmt.Errorf("the merchant's request could not be built: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+string(p.apiKey))
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	answer, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("the merchant of record could not be reached: %w", err)
	}
	defer func() { _ = answer.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(answer.Body, paddleMaxAnswerBytes))
	if err != nil {
		return fmt.Errorf("the merchant's answer could not be read: %w", err)
	}
	if answer.StatusCode/100 != 2 {
		return fmt.Errorf("the merchant of record refused %s %s with %d: %s", method, path, answer.StatusCode, paddleFault(raw))
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("the merchant's answer is not the shape this service reads: %w", err)
	}
	return nil
}

// paddleFault is Paddle's own error code and detail, for an error's text. A body
// that is not Paddle's error shape — a proxy's page, a truncation — is answered
// as exactly that and not quoted, so what a failure says to whoever reads the
// API's report is the merchant's own complaint and never a body this service did
// not write, nor the key it sent with.
func paddleFault(raw []byte) string {
	var answer struct {
		Error struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return "no error shape"
	}
	switch {
	case answer.Error.Code == "" && answer.Error.Detail == "":
		return "no error shape"
	case answer.Error.Detail == "":
		return answer.Error.Code
	case answer.Error.Code == "":
		return answer.Error.Detail
	default:
		return answer.Error.Code + ": " + answer.Error.Detail
	}
}
