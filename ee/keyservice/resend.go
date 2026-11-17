// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The mail path the portal's invitations go out on, over Resend's HTTP API.
//
// **The mailer is a transport and not a renderer.** It is handed a Message the
// service built (mail.go, invites.go) and it writes that message's own fields
// into the provider's request: it composes nothing, it rewrites no link, and
// what a person receives is what the service decided to say. That is what keeps
// §7.2's rule — no text a user typed reaches a header — a property of the
// message's construction rather than of a client that might re-split it.
//
// **The key is a credential and is treated as one** (the `ee/` credentials
// floor): it is read once from the deployment's config (service.go's
// ResendKey) into this struct, it is written into one Authorization header and
// nowhere else — not the URL's query, not the body, not a log line — and no
// failure path prints it. A request that fails is reported with its status and
// the provider's own answer, never with the request the header was on. What is
// in the URL is the provider's own endpoint, so a log line along the way holds
// the mail's fate and not the credential that authorized it (D2182's rule read
// at the transport: a key that reaches a log is a key that has to be rotated).
//
// **A failure is the caller's error and never a silent success.** An invitation
// is mailed because somebody has to receive the link that leads to it, so a
// transport that could not deliver is a request that did not happen: the
// caller (createInvitation, SeedOperations) turns it into a failure rather than
// answering as though a person had been invited. The invitation record stays
// where it is — it is withdrawable, and the seed runs again — because deleting
// it would throw away the only trace of an attempt a person may have received
// half of.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ResendAPIURL is where Resend takes a message. It is a constant rather than a
// config field, as the intake's other endpoints are: the deployment chooses
// whether it has a provider and which key it holds, and never which host its
// mail is handed to — a configurable endpoint is a field a mistake can point at
// a stranger, and the mail is a link that leads into the portal.
const ResendAPIURL = "https://api.resend.com/emails"

// maxMailAnswerBytes bounds the provider's own answer before it is read. It is
// read only to say what went wrong, and an answer past the bound is a provider
// this service will not read unbounded.
const maxMailAnswerBytes = 4 << 10

// sendTimeout is how long one message may take. It is per request and not per
// caller, so a provider that has stopped answering fails the invitation it is
// sending rather than the request that asked for it.
const sendTimeout = 15 * time.Second

// HTTPDoer is the one thing this file needs of an HTTP client: do the request
// it is given. It is an interface so a test can hand in a client that answers a
// transport failure without a network (invites_test.go), and http.Client
// satisfies it as it is.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// ResendMailer is the portal's mail over Resend. Its fields are this file's and
// never a caller's: the key is the deployment's (Config.ResendKey), the sender
// is the deployment's own verified sender address, and endpoint and doer exist
// so that a test can point one at a server it runs itself.
type ResendMailer struct {
	key      []byte
	from     string
	doer     HTTPDoer
	endpoint string
}

// NewResendMailer builds the mailer a deployment mails through. What it refuses
// is what the deployment got wrong, and it refuses it here rather than at the
// first invitation — a key that is not there and a sender that is not there are
// both a mail path that can never deliver, and a person waiting for an
// invitation that will never arrive is the failure this turns into a startup
// error instead.
//
// A nil doer is the default client and not a failure: an unset HTTP client is
// not a mistake, it is the ordinary case, and every request this mailer makes
// is to a constant host.
func NewResendMailer(key []byte, from string, doer HTTPDoer) (*ResendMailer, error) {
	if len(bytes.TrimSpace(key)) == 0 {
		return nil, fmt.Errorf("keyservice: a Resend mail path needs the provider's API key, and every request without one is refused by the provider")
	}
	sender := strings.TrimSpace(from)
	if sender == "" {
		return nil, fmt.Errorf("keyservice: a Resend mail path needs the sender address the deployment's domain is verified for")
	}
	if doer == nil {
		doer = http.DefaultClient
	}
	return &ResendMailer{key: key, from: sender, doer: doer, endpoint: ResendAPIURL}, nil
}

// sendBody is the provider's own request shape: one sender, one recipient, the
// subject and the text. The recipient is an array because that is Resend's
// shape, and it is always the one address the Message names — a mail path that
// could send to a list is a mail path that could send to somebody the service
// did not decide about.
type sendBody struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

// Send hands one message to the provider. A refused or unreachable provider is
// an error, and the error says what the provider answered rather than what was
// asked: the request carries the credential and the answer does not.
func (m *ResendMailer) Send(ctx context.Context, msg Message) error {
	body, err := json.Marshal(sendBody{
		From:    m.from,
		To:      []string{msg.To},
		Subject: msg.Subject,
		Text:    msg.Body,
	})
	if err != nil {
		return fmt.Errorf("keyservice: building the mail provider's request: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("keyservice: building the mail provider's request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The one place the key is written. The header is the only field of the
	// request that holds it, so the request itself is never logged on a failure.
	req.Header.Set("Authorization", "Bearer "+string(m.key))
	answer, err := m.doer.Do(req)
	if err != nil {
		// The transport's error names the URL and never the header: what a
		// deployment reads here is that the provider could not be reached.
		return fmt.Errorf("keyservice: the mail provider could not be reached: %w", err)
	}
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode < 200 || answer.StatusCode > 299 {
		detail, _ := io.ReadAll(io.LimitReader(answer.Body, maxMailAnswerBytes))
		return fmt.Errorf("keyservice: the mail provider refused the message with %s: %s", answer.Status, strings.TrimSpace(string(detail)))
	}
	return nil
}
