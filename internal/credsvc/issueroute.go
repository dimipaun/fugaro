package credsvc

// The issue route (D2718, B3): the installation's orchestrator daemon asks this
// service to read the open issues of a repository it works on, read one issue
// back by number, and open one of its own — and the SERVICE performs the act
// itself, with a token it mints for itself and never hands over.
//
// It is the label route's pattern and its reason (credsvc/labelroute.go, D2439,
// F5): GitHub has no issue-only permission either — the token this route mints
// can write issues on the whole repository — so the daemon never holds one, and
// the acts themselves are what bound a stolen daemon identity (F18): the three
// acts below and no others, the one repository the installation's inventory
// holds, and the bounds an opened issue's title and body are held to.
//
// What the three acts are:
//
//   - `list` reads every open issue of the repository, pull requests excluded,
//     which is what the daemon's sync ingests from (B4, D2720).
//   - `get` reads one issue back by number, an open or a closed one, which is
//     what a run's brief is built from and what tells a closed issue's item
//     that its issue is gone (B4, B9, D2720).
//   - `open` creates one issue from a title and a body, which is what the
//     mirror writes when an item becomes an issue (D2719), and whose answer's
//     number the item's external id is written from (D2728).
//
// The host is GitHub and only GitHub (D2718: GitHub now, Jira later). There is
// therefore no Bitbucket half here, no second credential and no host to resolve
// between two inventories: the repository is looked up in the GitHub inventory
// alone, and a repository this installation holds only on Bitbucket is refused
// as not one of its GitHub repositories rather than served an empty answer.
// Jira would arrive as its own contract and its own credential (O3), not as a
// second branch of this route.
//
// The credential is this service's own mint, scoped to the one repository and
// to `{issues: write, metadata: read}` — NOT the label route's set, which
// cannot touch issues at all (F39, D2718) — cached until near expiry, never put
// in an answer, a log line or an audit line, and named on every line this route
// writes (D2439). It asks for no contents noun: the run's own token set has one
// and no act of this route needs it.
//
// The acts are rate-limited per repository, with the reads counted apart from
// the writes, at the label route's own numbers (F22), and every call that
// reaches a verified caller writes exactly one audit line, served or refused
// (C1.13).

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// IssuePath is the path the daemon asks for an issue read or an issue creation
// on. It is this service's own contract and not a forge path: the call names a
// repository and an act, and the service is what reaches GitHub.
//
// creds.IssuePath is the same path spelled by the daemon's side, and
// TestIssueRoutePathMatchesClient holds the two to each other, so a rename of
// either cannot quietly move the route.
const IssuePath = "/v1/github/issues"

// The bounds an OPENED issue is held to (D2718): a title of at most 256
// characters and a body of at most 8000, counted in characters and not in bytes
// — an issue's text is a person's and the daemon carries an author's own
// writing into it, so a byte count would refuse a title a reader can read and
// GitHub accepts.
//
// They are constants of THIS service and not fields of the call: the request
// carries text and nothing about its limits, so a caller cannot widen them, and
// the refusal names the field and the number rather than the text it refused.
const (
	issueMaxTitleRunes = 256
	issueMaxBodyRunes  = 8000
)

// The route's rate limits are the label route's numbers (F22): the two acts are
// asked by the same daemon on the same tick, and the numbers were sized for
// exactly that tick — 240 reads an hour is two a tick and 60 writes is one
// every other tick — so a second set with a second rationale would be a second
// bound an operator has to reconcile.
//
// They are this process's memory and nothing else (issueState), and the paragraph
// SECURITY.md states for the label route's rate is the one that covers these:
// per instance, best effort, and begun again at every cold start.
const (
	issueWritesPerRepo = labelWritesPerRepo
	issueReadsPerRepo  = labelReadsPerRepo
)

// The route's own refusals. They are sentinels so a test and a reader can tell
// them apart, and every one is a sentence written here: GitHub's own answer
// never reaches a caller, because the requests that failed carried the App's
// JWT and an installation token.
var (
	// ErrIssueUnknownRepository is a repository this installation's GitHub
	// inventory does not hold.
	ErrIssueUnknownRepository = errors.New("credsvc: the repository is not one of this installation's GitHub repositories")
	// ErrIssueNoAppKey is a bundle that holds no GitHub App key, and
	// ErrIssueBadAppKey is one whose key this code cannot sign with. Neither is
	// a fallback to a key the caller may hold (D143).
	ErrIssueNoAppKey  = errors.New("credsvc: the credentials bundle holds no GitHub App key")
	ErrIssueBadAppKey = errors.New("credsvc: the credentials bundle's GitHub App key is not a usable private key")
	// ErrIssueRateLimited is a repository that has had as many issue reads or
	// issue writes as this service performs for one repository in an hour.
	ErrIssueRateLimited = errors.New("credsvc: this repository's issues have been read or written as often as this service allows")
	// ErrIssueGitHub is GitHub refusing a read, a creation or a mint, or the
	// call to any of them failing.
	ErrIssueGitHub = errors.New("credsvc: GitHub did not answer the issue call")
	// ErrIssueUnaudited is an act GitHub performed and C1.13's auditor could not
	// record. The read acts change nothing and the one write is not idempotent
	// — a second creation is a second issue — so a caller told its creation was
	// not served re-reads the listing to find out whether it landed, which the
	// marker D2728 reads is what makes decidable.
	ErrIssueUnaudited = errors.New("credsvc: the issue call could not be audited")
)

// issueState is this process's memory for the issue route: the installation
// tokens it has minted for its own acts, and the two rate windows. It is the
// label route's shape and the label route's reason for holding no store — what
// the windows bound is a daemon looping on this service, and a node under
// creds/ would be a second writer of a fact that has no reader.
//
// The tokens are its OWN cache and not the label route's, and that is not a
// detail: an installation token is keyed by nothing but the repository and the
// App, so a cache shared with the label route would hand a label act's
// `{pull_requests: write, metadata: read}` token to an issue act — or the
// reverse — and the narrower set would be spent for the wider act's refusal
// rather than for its act (D2718, F39).
type issueState struct {
	tokens labelTokenCache
	writes *labelWindow
	reads  *labelWindow
}

func newIssueState(now func() time.Time) *issueState {
	return &issueState{
		writes: newLabelWindow(now, issueWritesPerRepo),
		reads:  newLabelWindow(now, issueReadsPerRepo),
	}
}

// allow reports whether one act for slug is within the rate, counting the
// creations apart from the two reads.
func (st *issueState) allow(slug string, write bool) bool {
	if write {
		return st.writes.allow(slug)
	}
	return st.reads.allow(slug)
}

// issueTokenSource is what the provider this route performs its acts through
// gets its installation tokens from (gitprov.TokenSource): this service's own
// mint, at the route's own permission set, cached until the token is near its
// expiry.
//
// The provider is built with the bundle's key so that the mint can sign, and
// this source replaces its in-job mint before any call is made, so no act of
// this route is ever performed with the run's wider token set (D2718).
type issueTokenSource struct {
	state *issueState
	s     *Server
	slug  string
	owner string
	repo  string
	appID string
	key   *rsa.PrivateKey
}

var _ gitprov.TokenSource = (*issueTokenSource)(nil)

// Token returns this service's own installation token, valid for at least
// minValid, minting one when the cached token would expire sooner. The whole
// check-then-mint runs under the cache's lock, as the label route's does: a
// mint is an act at GitHub that must not happen once per request.
func (t *issueTokenSource) Token(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	key := t.slug + "\x00" + t.appID
	t.state.tokens.mu.Lock()
	defer t.state.tokens.mu.Unlock()
	if held, ok := t.state.tokens.held[key]; ok && held.expires.Sub(t.s.now()) >= minValid {
		return held.token, held.expires, nil
	}
	token, expires, err := github.MintInstallationToken(ctx, github.Options{
		Owner: t.owner, Repo: t.repo, AppID: t.appID, PrivateKey: t.key,
		HTTP: t.s.cfg.Client, Now: t.s.now,
	}, issueTokenPermissions())
	if err != nil {
		return "", time.Time{}, err
	}
	if t.state.tokens.held == nil {
		t.state.tokens.held = map[string]labelToken{}
	}
	t.state.tokens.held[key] = labelToken{token: token, expires: expires}
	return token, expires, nil
}

// issueTokenPermissions is the reach of the token this route mints for its own
// acts, and it is D2718's whole answer: `issues: write` is what GitHub's issue
// endpoints require — the creation is the act that needs the write, and GitHub
// has no read-only issue permission beside it — and `metadata: read` is the
// noun every installation token carries.
//
// It is deliberately NOT the label route's set: `pull_requests: write` cannot
// touch an issue at all, and a token that carried both would be one credential
// serving two routes' acts (F39). There is no contents noun, which the run's
// own set carries and no act here needs. The set is a constant of this service
// and not a field of the call, which is what makes a caller unable to widen it.
func issueTokenPermissions() map[string]string {
	return map[string]string{"issues": "write", "metadata": "read"}
}

// readIssueCall reads an issue call's body out of the call the handler already
// read (readCall), which is where the daemon's own ID token was registered with
// the service's redaction for the length of this call.
//
// The decode is strict, as every body of this contract is, which is what makes
// "the caller cannot name the permissions, the token or the repository list" a
// property of the route rather than a check inside it (Convention 6). Each act
// then takes the fields it has and no others, so a body carrying a field its
// act does not take is refused rather than read for the fields it does — a
// listing that carried a title, a read that carried a body, a creation that
// carried a number.
//
// The App ID's presence is required here and not left to a host check, because
// this route has one host: every act of it is a GitHub one, so a call that
// names no App ID is a call this route cannot sign its own token for (the label
// route's shape, where the host decides, is the other way round).
func readIssueCall(body []byte) (creds.IssueRequest, error) {
	if len(body) > maxMintRequestBytes {
		return creds.IssueRequest{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxMintRequestBytes)
	}
	var req creds.IssueRequest
	if err := decodeStrict(body, &req); err != nil {
		return creds.IssueRequest{}, errors.New("the body is not one JSON object of this route's fields")
	}
	if _, _, ok := gitprov.SplitRepo(req.Repository); !ok {
		return creds.IssueRequest{}, errors.New("the call names no repository (owner/name)")
	}
	if req.AppID == "" {
		return creds.IssueRequest{}, errors.New("the call names no GitHub App ID, which every act of this route takes")
	}
	if !appIDRE.MatchString(req.AppID) {
		return creds.IssueRequest{}, errors.New("the call's App ID is not a GitHub App ID (1 to 20 digits)")
	}
	switch req.Action {
	case creds.IssueActionList:
		if req.Number != 0 || req.Title != "" || req.Body != "" {
			return creds.IssueRequest{}, errors.New("a listing names no issue number, no title and no body")
		}
		return req, nil
	case creds.IssueActionGet:
		if req.Number < 1 {
			return creds.IssueRequest{}, errors.New("the call names no issue number")
		}
		if req.Title != "" || req.Body != "" {
			return creds.IssueRequest{}, errors.New("an issue read names no title and no body")
		}
		return req, nil
	case creds.IssueActionOpen:
		if req.Number != 0 {
			return creds.IssueRequest{}, errors.New("a creation names no issue number: GitHub answers the one it made")
		}
		if err := checkOpenedIssue(req.Title, req.Body); err != nil {
			return creds.IssueRequest{}, err
		}
		return req, nil
	default:
		return creds.IssueRequest{}, errors.New("the call names no act this route performs: it performs " + creds.IssueActionList + ", " + creds.IssueActionGet + " and " + creds.IssueActionOpen)
	}
}

// checkOpenedIssue holds an issue this route would CREATE to D2718's two
// bounds, and it is the whole of what a caller may put into an issue: the text
// is the daemon's own item (a mirrored item's title and body) and an issue is
// created at the repository's front door, where every person who watches it
// reads it.
//
// The title is required — GitHub refuses an issue with none, and a route that
// sent one would spend a creation to learn what this check already knows — and
// the body is not: an item with nothing to say is a legitimate issue, so an
// empty body is carried through as an empty body.
//
// The counts are CHARACTERS (runes) and not bytes, and the length is measured
// before the text is anywhere near GitHub: a byte count refuses a title a
// person can read, and a count made from the answer would be a creation this
// service already spent.
func checkOpenedIssue(title, body string) error {
	switch {
	case title == "":
		return errors.New("the call names no title, which GitHub requires of an issue")
	case utf8.RuneCountInString(title) > issueMaxTitleRunes:
		return fmt.Errorf("the call's title is longer than the %d characters this route opens an issue with", issueMaxTitleRunes)
	case utf8.RuneCountInString(body) > issueMaxBodyRunes:
		return fmt.Errorf("the call's body is longer than the %d characters this route opens an issue with", issueMaxBodyRunes)
	}
	return nil
}

// issueRoute answers one issue call. It is reached from ServeHTTP's branch
// ahead of RequestFromHTTP, and for the label route's reason (D2439): the
// daemon is not a run of this installation and holds no run identity, so the
// shared parse would refuse the call — for the missing identity headers —
// before the class ever read the token.
//
// The order below is the label route's order and its reasons:
//
//   - the caller, first, and on its own token. This is the route's admission
//     and nothing else is read before it: a body cannot widen it, and a refusal
//     here is typed and has no line, because nothing is verified yet and there
//     is no caller for a line to name.
//   - the run identity the class does not take, refused by name before anything
//     else is read.
//   - the call, whose read is ALSO the registration of the daemon's own token
//     with the call-scoped scrub, and whose every field is checked — the title
//     and the body's bounds included — before anything is resolved or reached.
//   - the operator's stop flag, read BEFORE any credential.
//   - the repository, resolved through the installation's GitHub inventory.
//   - the rate limit, checked before the bundle so that a repository whose
//     issues have been read as often as this service allows reads no credential
//     at all.
//   - the bundle and its App key, failing closed.
//   - the provider, built with the bundle's key so the mint can sign, and with
//     this service's own mint installed over its in-job one.
//   - the act, through B11's calls.
//   - the answer, built, audited and handed over in that order (answerIssue).
func (s *Server) issueRoute(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	purpose, credential := routeAct(IssuePath)
	tok, err := bearerFromHTTP(r)
	if err != nil {
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := s.cfg.Auth.VerifyOrchestrator(ctx, tok)
	if err != nil {
		s.refuseVerifier(w, err)
		return
	}
	if id := r.Header.Get(HeaderRunIdentity); id != "" {
		s.refuseIssue(ctx, w, caller, "", credential, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it performs issue calls for the orchestrator daemon, and a caller presenting a run is a call of a shape it does not have")
		return
	}
	if job := r.Header.Get(HeaderRunJob); job != "" {
		s.refuseIssue(ctx, w, caller, "", credential, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it performs issue calls for the orchestrator daemon, and a caller naming a job is a call of a shape it does not have")
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		s.refuseIssue(ctx, w, caller, "", credential, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	req, err := readIssueCall(cl.body)
	if err != nil {
		s.refuseIssue(ctx, w, caller, "", credential, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	on, which, err := s.cfg.Kills.Stop(ctx, "")
	if err != nil {
		s.refuseIssue(ctx, w, caller, "", credential, StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		s.refuseIssue(ctx, w, caller, "", credential, StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return
	}
	slug, owner, name, err := githubRepository(s.cfg.Repos, req.Repository)
	if err != nil {
		// The GitHub inventory alone, and not the two the label route resolves
		// between (labelHostOf): this route has no act a Bitbucket repository
		// could answer, so a name only that host holds is a name this route does
		// not serve rather than a host it resolves to and then refuses (D2718).
		s.refuseIssue(ctx, w, caller, "", credential, StatusRequestRefused, ErrorRequestRefused,
			readRefusalMessage(ErrIssueUnknownRepository, req.Repository))
		return
	}
	write := req.Action == creds.IssueActionOpen
	if !s.issues.allow(slug, write) {
		s.refuseIssue(ctx, w, caller, slug, credential, StatusUnavailable, ErrorStateUnavailable, issueRateRefusal(write))
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseIssue(ctx, w, caller, slug, credential, StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	key, err := appKey(b)
	if err != nil {
		if errors.Is(err, ErrMintNoAppKey) {
			err = ErrIssueNoAppKey
		} else {
			err = ErrIssueBadAppKey
		}
		s.refuseIssue(ctx, w, caller, slug, credential, StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	provider, err := github.New(github.Options{
		Owner: owner, Repo: name, AppID: req.AppID, PrivateKey: key,
		HTTP: s.cfg.Client, Now: s.now,
	})
	if err != nil {
		s.log.Warn("credsvc: the issue route's provider could not be built", "purpose", purpose, "credential", credential, "repository", slug)
		s.refuseIssue(ctx, w, caller, slug, credential, StatusUnavailable, ErrorStateUnavailable, ErrIssueGitHub.Error())
		return
	}
	provider.UseServiceTokens(&issueTokenSource{
		state: s.issues, s: s, slug: slug, owner: owner, repo: name, appID: req.AppID, key: key,
	})
	ans, err := s.issuePerform(ctx, provider, req)
	if err != nil {
		s.issueUpstreamRefusal(ctx, w, caller, slug, purpose, credential, "the issue call was not performed", err)
		return
	}
	s.answerIssue(ctx, w, caller, slug, credential, ans)
}

// issueUpstreamRefusal answers a refusal whose cause is this service's or
// GitHub's and not the call's, logging the class of the failure and, when
// GitHub answered, its status — and never a byte of GitHub's own text, because
// the requests that failed carried the App's JWT and an installation token.
func (s *Server) issueUpstreamRefusal(ctx context.Context, w http.ResponseWriter, caller, repository, purpose, credential, what string, err error) {
	attrs := []any{"purpose", purpose, "credential", credential, "repository", repository}
	var se *httpjson.StatusError
	if errors.As(err, &se) {
		attrs = append(attrs, "status", se.Status)
	}
	s.log.Warn("credsvc: "+what, attrs...)
	s.refuseIssue(ctx, w, caller, repository, credential, StatusUnavailable, ErrorStateUnavailable, ErrIssueGitHub.Error())
}

// issuePerform performs one call's act through B11's calls and builds the
// answer.
//
// The listing is answered in GitHub's own order and not sorted: unlike the
// label route, which reads a map and owes the daemon a stable order, this is a
// list the host already ordered, and re-sorting it by number would be a second
// answer's order presented as the host's.
//
// An issue GitHub answers with no number is refused by the adapter itself
// (github.OpenIssue): the external id the sync writes is built from it, and an
// id of "#0" is one every later read would miss (D2728).
func (s *Server) issuePerform(ctx context.Context, p *github.Provider, req creds.IssueRequest) (creds.IssueAnswer, error) {
	switch req.Action {
	case creds.IssueActionList:
		issues, err := p.ListOpenIssues(ctx)
		if err != nil {
			return creds.IssueAnswer{}, fmt.Errorf("%w: the open issues could not be read: %w", ErrIssueGitHub, err)
		}
		ans := creds.IssueAnswer{Issues: make([]creds.Issue, 0, len(issues))}
		for _, issue := range issues {
			ans.Issues = append(ans.Issues, issueRead(issue))
		}
		return ans, nil
	case creds.IssueActionGet:
		issue, err := p.ReadIssue(ctx, req.Number)
		if err != nil {
			return creds.IssueAnswer{}, fmt.Errorf("%w: the issue could not be read: %w", ErrIssueGitHub, err)
		}
		return creds.IssueAnswer{Issues: []creds.Issue{issueRead(issue)}}, nil
	case creds.IssueActionOpen:
		issue, err := p.OpenIssue(ctx, req.Title, req.Body)
		if err != nil {
			return creds.IssueAnswer{}, fmt.Errorf("%w: the issue could not be created: %w", ErrIssueGitHub, err)
		}
		return creds.IssueAnswer{Issues: []creds.Issue{issueRead(issue)}}, nil
	default:
		// readIssueCall refuses an act this route does not perform, so this is
		// unreachable — and it is here because the alternative to an error is an
		// empty answer, which a caller reads as an act that was performed.
		return creds.IssueAnswer{}, fmt.Errorf("%w: the call names no act this route performs", ErrIssueGitHub)
	}
}

// issueRead is one issue as the adapter read it, in the wire's own shape. The
// adapter's own type is where the host's answer stops and this contract begins,
// and the two are kept one reading by this function being the only translation
// between them: the route performs B11's calls and answers what they answered.
func issueRead(i github.Issue) creds.Issue {
	return creds.Issue{
		Number: i.Number, Title: i.Title, Body: i.Body, URL: i.URL, State: i.State,
		AuthorLogin: i.AuthorLogin, AuthorID: i.AuthorID, AppSlug: i.AppSlug,
	}
}

// issueRateRefusal is the sentence a rate-limited call is refused with. It
// states the number, because an operator reading it has to know which limit was
// reached.
func issueRateRefusal(write bool) string {
	if write {
		return fmt.Sprintf("%s: this service performs %d issue creations an hour for one repository, and this repository has had as many", ErrIssueRateLimited.Error(), issueWritesPerRepo)
	}
	return fmt.Sprintf("%s: this service performs %d issue reads an hour for one repository, and this repository has had as many", ErrIssueRateLimited.Error(), issueReadsPerRepo)
}

// answerIssue builds the issue route's answer, audits the act and writes it. It
// is answerLabel's order and answerLabel's reasons: the answer is built first
// so that a body the service cannot build is a refusal with its own line and no
// act line at all, and the audit line is written before the caller is answered,
// because an act that cannot be audited is not one this service reports as done
// (C1.13, D143).
//
// The two reads are safe to retry after a refusal whose line could not be
// written; the creation is not — a second one is a second issue — and the
// marker D2728 reads is what lets the daemon find out whether the first
// landed.
func (s *Server) answerIssue(ctx context.Context, w http.ResponseWriter, caller, repository, credential string, ans creds.IssueAnswer) {
	body, err := json.Marshal(ans)
	if err != nil {
		s.log.Error("credsvc: the issue route's answer could not be built", "error", err)
		s.refuseIssue(ctx, w, caller, repository, credential, StatusUnavailable, ErrorStateUnavailable, readAnswerRefusal)
		return
	}
	if err := s.auditIssue(ctx, caller, repository, credential, StatusOK); err != nil {
		s.log.Error("credsvc: the issue route's audit line was not written", "error", err)
		s.refuseIssue(ctx, w, caller, repository, credential, StatusUnavailable, ErrorStateUnavailable, ErrIssueUnaudited.Error())
		return
	}
	writeReadAnswer(w, body)
}

// refuseIssue answers one issue refusal and records it, in refuseLabel's shape:
// one typed refusal to the caller, one line to the operator, and the caller's
// answer is the same whether or not the line lands (D143).
//
// It is reached only once the caller IS verified — the refusals above that
// point are answered by refuseVerifier alone, with no line, because an
// unverified caller has no name for one.
func (s *Server) refuseIssue(ctx context.Context, w http.ResponseWriter, caller, repository, credential string, status int, typ, message string) {
	if err := s.auditIssue(ctx, caller, repository, credential, typ); err != nil {
		s.log.Error("credsvc: a refused issue call could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// auditIssue writes one issue route's line: the daemon as its caller, the
// repository the act was for, the credential the act was performed with, and
// NEITHER a run nor a job — the caller is the installation's orchestrator
// daemon, which holds no run identity and is not a job of this installation. A
// refusal taken before the call was read has no repository to name.
//
// The credential is the App key for every line of this route, and unlike the
// label route it never becomes the other host's: this route has one host, so
// routeAct's answer for it is the answer for its every act, served or refused.
func (s *Server) auditIssue(ctx context.Context, caller, repository, credential, status string) error {
	purpose, _ := routeAct(IssuePath)
	return s.audit.Write(ctx, AuditEvent{
		Caller:     caller,
		Repository: repository,
		Credential: credential,
		Purpose:    purpose,
		Status:     status,
	})
}
