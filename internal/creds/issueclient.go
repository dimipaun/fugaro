package creds

// The issue client (B4, D2718): the calls the installation's orchestrator
// daemon makes when it reads the open issues of a repository, opens one of its
// own, and — B9, D2720 — reads one issue back by number, which is what the
// brief of a run for a backlog item is built from and what says whether the
// issue still carries the text the item was approved with. The SERVICE performs
// the act with a token it mints and never hands over; this client only asks for
// it (D2439's pattern, B3's route).
//
// It is here for the label client's reasons (labelclient.go): this package is
// the library the daemon is built on, the route's shapes are here already
// (issueroute.go), and the two spellings of the path are held to each other by
// the service's own TestIssueRoutePathMatchesClient. The daemon presents its
// own Google ID token and nothing else — no run identity, no job — which is the
// class the route is gated on.
//
// The Issues interface below is this package's own rather than
// internal/issuesync's, because that package sits ABOVE this one: what the
// daemon's wiring adapts this client to is B4's seam, and the adapter is where
// the two shapes meet. IssueReader is the read the launcher's brief holds (B9):
// the two are separate interfaces because a caller that briefs a run needs one
// issue's own text and has no business listing or opening one, and the daemon
// wires both to the one client below.
//
// Nothing here puts a value in an error. The service's own answer is read for
// its status and its error code alone, and the sentence beside that code is
// written for the operator's log and not for this caller, so it is dropped
// rather than relayed.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Issues is the issue capability as the daemon's own library holds it: the open
// issues of the client's repository, and one issue opened for it. It is the
// half of B4's seam (issuesync.Adapter) that speaks to a service rather than to
// a host, and it is an interface so that the daemon's wiring depends on the
// capability and not on the way an issue is reached.
type Issues interface {
	// ListOpen returns every open issue of the repository, in the host's own
	// order, with each issue's body: the sync hashes a body it has not
	// ingested yet and never stores one.
	ListOpen(ctx context.Context) ([]Issue, error)
	// Open creates one issue from a title and a body and answers it as the host
	// created it. Its number is what the item's external id is written from.
	Open(ctx context.Context, title, body string) (Issue, error)
}

// IssueReader is the issue route's read as the daemon's own library holds it:
// one issue of the repository this client was built for, by number. It is what
// a caller that needs the ticket's text depends on (B9, D2720), and it is
// separate from Issues because briefing a run is not a capability that lists or
// opens anything.
type IssueReader interface {
	Issue(ctx context.Context, number int) (Issue, error)
}

// maxIssueAnswerBytes bounds the answer this client reads. It is far larger
// than the label client's because a listing answers with every open issue of
// the repository and EVERY issue's body, which GitHub allows to be 64 KiB:
// like the label route's listing, the size is a repository's and not a
// constant, and this is a bound on a runaway answer rather than a size
// estimate. The read of one issue is held to the same bound for the same
// reason: a body GitHub holds may be far longer than the 8000 characters the
// open act accepts (that bound is the service's rule for text it WRITES, and
// not a bound on what it reads back).
const maxIssueAnswerBytes = 16 << 20

// ServiceIssues is the issue route reached over HTTP at the installation's
// credentials service. It is ONE repository's client, as ServiceLabeler is: the
// route's request carries the repository the client was built with, and the
// daemon holds one per repository.
//
// Every field is required, and a missing one is a refusal (ErrIssueIdentity)
// rather than a call the service would answer with less.
type ServiceIssues struct {
	// URL is the credentials service's base URL, from the installation's own
	// configuration (the service's Cloud Run URL).
	URL string
	// Repository is the "owner/name" every call of this client is made for.
	Repository string
	// AppID is the GitHub App's ID: a number, not a credential, and the one
	// thing a call names beyond the act. The service's environment does not
	// hold it, so the caller names it — and cannot name anything else: the key
	// that signs, the permissions the token takes and the issue the act reads
	// or writes are the service's. It is also the value the sync holds an
	// issue's own App to before a mirror marker counts (D2728), so a daemon
	// that named another App would both sign as it and read its issues as the
	// installation's own.
	AppID string
	// HTTP is the client the calls are made with; nil means this package's own
	// hardened one (serviceCallClient: no redirects, no jar, a bounded
	// timeout).
	HTTP *http.Client
	// IDToken mints the daemon's own Google ID token for URL — the identity
	// the service's orchestrator class is. It is called on every call and never
	// cached here.
	IDToken func(ctx context.Context) (string, error)
}

// ServiceIssues is an Issues, which is what the daemon's wiring consumes, and
// an IssueReader, which is what the launcher briefs a run from.
var _ Issues = (*ServiceIssues)(nil)
var _ IssueReader = (*ServiceIssues)(nil)

// ListOpen implements Issues.
func (s *ServiceIssues) ListOpen(ctx context.Context) ([]Issue, error) {
	ans, err := s.call(ctx, IssueRequest{Action: IssueActionList})
	if err != nil {
		return nil, err
	}
	for _, issue := range ans.Issues {
		if issue.Number <= 0 {
			// The number is half the external id an item is keyed on (D2728),
			// and an id of "#0" is one no later read reaches and one the fence
			// would refuse: an answer carrying such an entry is refused whole,
			// rather than read as a listing with one issue the sync cannot
			// ingest.
			return nil, fmt.Errorf("%w: the service's answer names no issue number", ErrIssue)
		}
	}
	return ans.Issues, nil
}

// Open implements Issues: the title and the body are the item's own text, and
// the bounds they are held to are the service's (256 and 8000 characters,
// D2718) — a caller cannot widen them, and a call the service refuses is a
// refusal this client reports.
func (s *ServiceIssues) Open(ctx context.Context, title, body string) (Issue, error) {
	ans, err := s.call(ctx, IssueRequest{Action: IssueActionOpen, Title: title, Body: body})
	if err != nil {
		return Issue{}, err
	}
	if len(ans.Issues) != 1 {
		// The creation answers the issue it made: an answer that is empty would
		// be read by the sync as an issue with no number, and one that carries
		// several is not one creation's.
		return Issue{}, fmt.Errorf("%w: the service's answer is not one issue's", ErrIssue)
	}
	issue := ans.Issues[0]
	if issue.Number <= 0 {
		// The number is what the item's external id is built from (D2728), and
		// an id of "#0" is one every later read would miss.
		return Issue{}, fmt.Errorf("%w: the service's answer names no issue number", ErrIssue)
	}
	return issue, nil
}

// Issue implements IssueReader: it asks the service for issue number of this
// client's repository, through the route's read act.
//
// What comes back is the issue as the service read it, body included: the
// caller hashes the body, builds the brief from the text and stores neither
// (D2720). The read answers a closed issue too, and this client does not refuse
// one — whether a closed issue's item still launches is the state's question
// (D2720's archive rule is the ingest's, B4) and not this route's.
//
// The number is refused here rather than by the service: a read that names no
// issue is one no answer could be read from, and the refusal is this client's
// own (ErrIssueIdentity), as the acts' own bounds are.
func (s *ServiceIssues) Issue(ctx context.Context, number int) (Issue, error) {
	if number < 1 {
		return Issue{}, fmt.Errorf("%w: the read names no issue number", ErrIssueIdentity)
	}
	ans, err := s.call(ctx, IssueRequest{Action: IssueActionGet, Number: number})
	if err != nil {
		return Issue{}, err
	}
	if len(ans.Issues) != 1 || ans.Issues[0].Number != number {
		// A read is for one issue and the service answers the one it was asked
		// for: an answer that is empty would be read by the launcher as text no
		// ticket carries, and one that names another issue would brief a run
		// with a ticket the item is not about.
		return Issue{}, fmt.Errorf("%w: the service's answer is not one issue's", ErrIssue)
	}
	return ans.Issues[0], nil
}

// call makes one issue call and answers what the service answered: the
// repository, the App ID and the daemon's own ID token are this client's and
// are added here, and the rest of the request is the act's.
//
// The order of the checks is the label client's and its reasons: what this code
// will not send is refused before the URL is looked at, the URL before the
// identity is minted, and the token before anything is built.
func (s *ServiceIssues) call(ctx context.Context, req IssueRequest) (IssueAnswer, error) {
	if s.URL == "" || s.Repository == "" || s.AppID == "" || s.IDToken == nil {
		return IssueAnswer{}, fmt.Errorf("%w: this daemon has no credentials service to ask for an issue call at", ErrIssueIdentity)
	}
	if !repoShape(s.Repository) {
		return IssueAnswer{}, fmt.Errorf("%w: the call names no repository (owner/name)", ErrIssueIdentity)
	}
	req.Repository, req.AppID = s.Repository, s.AppID
	if req.Action != IssueActionList && req.Action != IssueActionGet && req.Action != IssueActionOpen {
		return IssueAnswer{}, fmt.Errorf("%w: the call names no act this client makes: it reads the open issues (%s), reads one (%s) and opens one (%s)",
			ErrIssueIdentity, IssueActionList, IssueActionGet, IssueActionOpen)
	}
	if err := checkServiceURL(s.URL, ErrIssue); err != nil {
		return IssueAnswer{}, err
	}
	id, err := s.IDToken(ctx)
	if err != nil {
		return IssueAnswer{}, fmt.Errorf("%w: the daemon's identity token could not be minted: %v", ErrIssueIdentity, err)
	}
	if !printableValue(id) {
		return IssueAnswer{}, fmt.Errorf("%w: the daemon's identity token is not one header value", ErrIssueIdentity)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return IssueAnswer{}, fmt.Errorf("%w: the issue call's request could not be built", ErrIssue)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(s.URL, "/")+IssuePath, bytes.NewReader(body))
	if err != nil {
		return IssueAnswer{}, fmt.Errorf("%w: the issue call's request could not be built", ErrIssue)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+id)
	resp, err := serviceCallClient(s.HTTP).Do(httpReq)
	if err != nil {
		return IssueAnswer{}, fmt.Errorf("%w: the credentials service could not be reached", ErrIssue)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIssueAnswerBytes+1))
	if err != nil {
		return IssueAnswer{}, fmt.Errorf("%w: the service's answer could not be read", ErrIssue)
	}
	if len(data) > maxIssueAnswerBytes {
		return IssueAnswer{}, fmt.Errorf("%w: the service's answer is larger than an issue call's", ErrIssue)
	}
	if resp.StatusCode != http.StatusOK {
		// A read of an issue the repository does not hold is the one refusal here
		// that asking again cannot clear, and the caller decides something
		// different on it: the launcher blocks the item it was reading the ticket
		// for rather than retrying the launch (B9, D2720). The status is the whole
		// of the evidence — the route's own error code is written for an operator's
		// log and a service that answered "unavailable" beside a 404 would be
		// telling two things at once — and the `get` act is the one it is answered
		// for: the listing and the creation name no one issue. Every other status,
		// and every answer to the other two acts, stays the route's own refusal.
		if req.Action == IssueActionGet && resp.StatusCode == http.StatusNotFound {
			return IssueAnswer{}, fmt.Errorf("%w: the service answered %d%s", ErrIssueGone, resp.StatusCode, refusalCode(data))
		}
		return IssueAnswer{}, fmt.Errorf("%w: the service answered %d%s", ErrIssue, resp.StatusCode, refusalCode(data))
	}
	var ans IssueAnswer
	if err := json.Unmarshal(data, &ans); err != nil {
		return IssueAnswer{}, fmt.Errorf("%w: the service's answer is not an issue call's", ErrIssue)
	}
	return ans, nil
}
