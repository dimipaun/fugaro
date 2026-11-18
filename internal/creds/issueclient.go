package creds

// The issue route's client (B9, D2720): the call the installation's daemon
// makes when it reads one issue back, which is what the brief of a run for a
// backlog item is built from and what says whether the issue still carries the
// text the item was approved with. The SERVICE performs the act; this client
// only asks for it, and no token comes back (D2439, D2718).
//
// It is here for the label client's reasons (labelclient.go): this package is
// the library the daemon is built on, and the route's shapes are here already
// (issueroute.go) — the body this client sends is the service's own
// creds.IssueRequest and the answer it reads is creds.IssueAnswer, which are
// the service's own decode and encode targets too (credsvc/issueroute.go), so a
// field cannot be renamed on one side alone.
//
// The daemon's identity is the class the route is gated on: this client
// presents the daemon's own Google ID token, minted for the service's URL, and
// nothing else — exactly what ServiceLabeler presents for the label route and
// ServiceMergeMint for the merge one. The token comes from IDToken, which is
// the daemon's own credential and not the operator's.
//
// Nothing here puts a value in an error. The service's own answer is read for
// its status and its error code alone, and the sentence beside that code is
// written for the operator's log and not for this caller, so it is dropped
// rather than relayed.
//
// The read is the `get` act alone. The other two acts of the route are the
// ingest's and the mirror's (B4, B11) and are bound there; what a launch needs
// is one issue's own text, and a client that could also list or open one would
// be a capability the launcher has no business holding.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// IssueReader is the issue route's read as the daemon's own library holds it:
// one issue of the repository this client was built for, by number. It is what
// a caller that needs the ticket's text depends on.
type IssueReader interface {
	Issue(ctx context.Context, number int) (Issue, error)
}

// ServiceIssues is the issue route reached over HTTP at the installation's
// credentials service. It is ONE repository's client: the route's request
// carries the repository the act is on, and the daemon builds one of these per
// repository, which is what keeps two repositories' issues two hosts' (the
// same shape ServiceLabeler has, and for the same reason).
//
// Every field but HTTP is required, and a missing one is a refusal
// (ErrIssueIdentity) rather than a call the service would answer with less.
type ServiceIssues struct {
	// URL is the credentials service's base URL, from the installation's own
	// configuration (the service's Cloud Run URL).
	URL string
	// Repository is the "owner/name" every call of this client is made for.
	Repository string
	// AppID is the GitHub App's ID: a number, not a credential, and the one
	// thing a call names beyond the act — the service signs its own token as
	// that App, whose environment does not hold it (D2718). There is no
	// Bitbucket issue route: this one is GitHub's alone.
	AppID string
	// HTTP is the client the calls are made with; nil means this package's own
	// hardened one (serviceCallClient: no redirects, no jar, a bounded
	// timeout).
	HTTP *http.Client
	// IDToken mints the daemon's own Google ID token for URL — the identity
	// the service's orchestrator class is. It is called on every call and never
	// cached here, as the label client's is not.
	IDToken func(ctx context.Context) (string, error)
}

var _ IssueReader = (*ServiceIssues)(nil)

// maxIssueAnswerBytes bounds the answer this client reads. A read answers one
// issue, and a body GitHub holds may be far longer than the 8000 characters
// the open act accepts (that bound is the service's rule for text it WRITES,
// and not a bound on what it reads back), so this is a bound on a runaway
// answer rather than a size estimate — the label route's own reading of its
// listing.
const maxIssueAnswerBytes = 1 << 20

// Issue implements IssueReader: it asks the service for issue number of this
// client's repository, through the route's read act.
//
// What comes back is the issue as the service read it, body included: the
// caller hashes the body, builds the brief from the text and stores neither
// (D2720). The read answers a closed issue too, and this client does not refuse
// one — whether a closed issue's item still launches is the state's question
// (D2720's archive rule is the ingest's, B4) and not this route's.
func (s *ServiceIssues) Issue(ctx context.Context, number int) (Issue, error) {
	if s.URL == "" || s.Repository == "" || s.AppID == "" || s.IDToken == nil {
		return Issue{}, fmt.Errorf("%w: this daemon has no credentials service to ask for an issue read at", ErrIssueIdentity)
	}
	if !repoShape(s.Repository) {
		return Issue{}, fmt.Errorf("%w: the call names no repository (owner/name)", ErrIssueIdentity)
	}
	if number < 1 {
		return Issue{}, fmt.Errorf("%w: the read names no issue number", ErrIssueIdentity)
	}
	// The URL is checked before anything is sent to it: a call presents the
	// daemon's own ID token, which is a bearer credential, and over plain http
	// it would travel in cleartext. https, or http on a loopback address, is
	// the rule the label route's client and the merge mint hold theirs to.
	if err := checkServiceURL(s.URL, ErrIssue); err != nil {
		return Issue{}, err
	}
	id, err := s.IDToken(ctx)
	if err != nil {
		return Issue{}, fmt.Errorf("%w: the daemon's identity token could not be minted: %v", ErrIssueIdentity, err)
	}
	if !printableValue(id) {
		return Issue{}, fmt.Errorf("%w: the daemon's identity token is not one header value", ErrIssueIdentity)
	}
	body, err := json.Marshal(IssueRequest{Repository: s.Repository, Action: IssueActionGet, AppID: s.AppID, Number: number})
	if err != nil {
		return Issue{}, fmt.Errorf("%w: the issue call's request could not be built", ErrIssue)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(s.URL, "/")+IssuePath, bytes.NewReader(body))
	if err != nil {
		return Issue{}, fmt.Errorf("%w: the issue call's request could not be built", ErrIssue)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+id)
	resp, err := serviceCallClient(s.HTTP).Do(httpReq)
	if err != nil {
		return Issue{}, fmt.Errorf("%w: the credentials service could not be reached", ErrIssue)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIssueAnswerBytes+1))
	if err != nil {
		return Issue{}, fmt.Errorf("%w: the service's answer could not be read", ErrIssue)
	}
	if len(data) > maxIssueAnswerBytes {
		return Issue{}, fmt.Errorf("%w: the service's answer is larger than an issue call's", ErrIssue)
	}
	if resp.StatusCode != http.StatusOK {
		return Issue{}, fmt.Errorf("%w: the service answered %d%s", ErrIssue, resp.StatusCode, refusalCode(data))
	}
	var ans IssueAnswer
	if err := json.Unmarshal(data, &ans); err != nil {
		return Issue{}, fmt.Errorf("%w: the service's answer is not an issue call's", ErrIssue)
	}
	if len(ans.Issues) != 1 || ans.Issues[0].Number != number {
		// A read is for one issue and the service answers the one it was asked
		// for: an answer that is empty would be read by the launcher as text no
		// ticket carries, and one that names another issue would brief a run
		// with a ticket the item is not about. The label client refuses another
		// pull request's answer for the same reason.
		return Issue{}, fmt.Errorf("%w: the service's answer is not one issue's", ErrIssue)
	}
	return ans.Issues[0], nil
}
