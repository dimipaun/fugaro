package credsvc

// The merge mint (E16, D2049): the orchestrator DAEMON asks this service for a
// short-lived installation token scoped to one repository and one pull
// request's merge and ready calls, and the service mints it from the App key
// its bundle holds — but only once the gate below has passed.
//
// It exists because the merge credential had no route (F28). A run holds no App
// key (C2.2, D145) and the run mint issues a token a run may push with and
// nothing more, so the daemon that merges had nothing to merge with; and the
// one credential the daemon COULD reach on its own is the person's, which is
// exactly the credential a coding-agent session on the same machine holds. So
// the mint is here, behind a caller class of its own, and it is the gate that
// makes the token worth having rather than the credential's shape alone.
//
// The four things that make this route what it is:
//
//   - The caller is the installation's ORCHESTRATOR DAEMON, and only it
//     (VerifyOrchestrator). It is a class of its own and not a person: a person
//     class's token is the one a coding-agent session on the same machine can
//     mint for itself, so admitting one would hand the merge to the session the
//     gate exists to keep out of it. The class's accounts are service accounts
//     and its audience is this service's own URL alone — never GcloudAudience —
//     which is the second half of the same rule: a plain `gcloud auth
//     print-identity-token` cannot mint for a custom audience, so the token a
//     person's ambient credentials produce is refused here by its audience
//     whatever list it was matched against.
//
//     What this class does NOT do is make the credential unreachable from the
//     machine it runs on. The daemon runs as the operator (D1241), and an
//     operator who may impersonate the daemon's account may do so from any
//     process of theirs, a coding-agent session included. The gate, the rate
//     limit and the audit below are what bound that session: what it can obtain
//     is a token bound to one pull request's head, refused when the base's
//     bypass list names the App, refused past the per-repository rate, and
//     written down every time. Closing the last of it is a credential the
//     machine cannot reach at all, which is the owner's ruling to make and not
//     this task's (D2049's alternatives).
//
//   - The token is bound to ONE HEAD. The call names the head SHA the daemon's
//     gate admitted, and the service reads the pull request's CURRENT head from
//     GitHub and refuses when the two differ (G2): a token minted for a head
//     that has moved is a token for a merge nobody reviewed.
//
//   - The token never bypasses branch protection (G14). The service reads both
//     places GitHub lets an App be named as an actor that may merge past a
//     protected base — the rulesets that cover it and its classic branch
//     protection's own bypass list — and refuses when the App is on either, and
//     it refuses when either read fails in any way but the 404 that means "no
//     such protection": an unreadable bypass list is not an absent one. It asks
//     for no administration write and sends no bypass flag — the permission set
//     it mints is mergeTokenPermissions, which is the narrowest set GitHub's
//     merge endpoint accepts.
//
//   - The mint is rate-limited per repository, and every act of it is audited
//     once (PurposeMergeMint, no tier): the line names the daemon account, the
//     repository's SLUG and the App key, and carries no run and no job, because
//     the caller is neither (callerWithoutJobPurpose, repositoryOnlyPurpose). A
//     mint whose line cannot be written hands the token to nobody (D143).
//
// The repository is resolved through the installation's own inventory and the
// App ID is the caller's, exactly as the read mint resolves and takes them: the
// service signs with the bundle's key whatever App ID a caller names, so a
// wrong one is GitHub refusing the JWT and never this service signing with
// another App's key.

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// MergeMintPath is the path the orchestrator daemon asks for a merge token on.
// It is not an Anthropic path: the request and the answer are this service's
// own contract, and it is served ahead of the shared request parse (ServeHTTP)
// because its caller holds no run identity.
//
// creds.MergeMintPath is the same path spelled by the client the daemon calls,
// and TestMergeMintRouteNamesTheClientPath holds the two to each other, so a
// rename of either cannot quietly move the route.
const MergeMintPath = "/v1/github/merge-token"

// MergeMintRequest is a merge mint's body: the one repository and the one pull
// request the token is asked for, the head that pull request must still be at,
// and the App ID the mint's JWT is signed as.
//
// Every field is required and there are four on purpose. There is no permission
// field, no repository-list field, no API-root field and no merge-method field:
// the permissions are this service's constant (mergeTokenPermissions), the
// token is scoped to the one repository named here and to no other, and the
// endpoint the mint and the two reads go to is api.github.com and nothing a
// caller can move. A body that carries any of them is refused by the strict
// decoder rather than read for the fields it does know (Convention 6), which is
// what makes "no caller can widen this mint" a property of the route and not a
// check inside it.
type MergeMintRequest struct {
	Repository string `json:"repository"`
	// Number is the pull request's number in that repository.
	Number int `json:"number"`
	// HeadSHA is the commit the pull request must still be at. It is the gate's
	// own fact and the binding the mint exists for (G2), so it is required: a
	// call that names none is a call for a merge of whatever the head happens to
	// be, which is the merge this route refuses to mint for.
	HeadSHA string `json:"head_sha"`
	// AppID is the mint route's own field for the mint route's own reason: it is
	// the one thing the caller supplies to a GitHub App mint and it is not a
	// secret.
	AppID string `json:"app_id"`
}

// MergeMintResponse is the merge mint's answer: the token, when it expires, and
// the repository it was minted for.
//
// Repository is the repository the CALLER named, in the same "owner/name"
// spelling the request carried — the read mint's answer and not the canonical
// name a mint's answer carries, for the read mint's reason: the answer's field
// is what the client checks its own request against before it uses the token.
type MergeMintResponse struct {
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Repository string    `json:"repository"`
}

// The merge mint's own refusals. They are sentinels so a test and a reader can
// tell them apart, and every one is a sentence written here: GitHub's own
// answer never reaches a caller, because the requests that failed carried the
// App's JWT and an installation token.
var (
	// ErrMergeMintBadRequest is a call whose body is not this contract's: not
	// one JSON object of four fields, a repository that is not "owner/name", a
	// pull request number that is not positive, a head that is not a commit, or
	// an App ID that is not 1 to 20 digits.
	ErrMergeMintBadRequest = errors.New("credsvc: the merge mint call is not one this service reads")
	// ErrMergeMintUnknownRepository is a repository this installation's
	// inventory does not hold.
	ErrMergeMintUnknownRepository = errors.New("credsvc: the repository is not one of this installation's")
	// ErrMergeMintNoAppKey is a bundle that holds no GitHub App key, and
	// ErrMergeMintBadAppKey is one whose key this code cannot sign with. Neither
	// is a fallback to a key the caller may hold (D143).
	ErrMergeMintNoAppKey  = errors.New("credsvc: the credentials bundle holds no GitHub App key")
	ErrMergeMintBadAppKey = errors.New("credsvc: the credentials bundle's GitHub App key is not a usable private key")
	// ErrMergeMintHeadMoved is a pull request whose current head is not the head
	// the call named. Nothing is minted: a token for a head that has moved is a
	// token for a merge nobody reviewed (G2).
	ErrMergeMintHeadMoved = errors.New("credsvc: the pull request's head is not the commit the call named")
	// ErrMergeMintBypassActor is a base whose rules the App may bypass, and a
	// base whose bypass list could not be read. Both are the same refusal
	// because they are the same answer to G14: a merge this service cannot show
	// to be subject to branch protection is a merge it does not mint for.
	ErrMergeMintBypassActor = errors.New("credsvc: the base's branch rules do not bind the App, or could not be read")
	// ErrMergeMintRateLimited is a repository that has been minted for as many
	// times as this service mints for one repository in an hour.
	ErrMergeMintRateLimited = errors.New("credsvc: this repository has been minted for as often as this service allows")
	// ErrMergeMintGitHub is GitHub refusing a read the gate makes, the mint, or
	// the call to either failing.
	ErrMergeMintGitHub = errors.New("credsvc: GitHub did not answer the merge mint")
	// ErrMergeMintUnaudited is a token GitHub granted and C1.13's auditor could
	// not record. The token exists at GitHub and reaches nobody (D143).
	ErrMergeMintUnaudited = errors.New("credsvc: the merge mint could not be audited, so no installation token was issued")
)

// mergeMintWindow and mergeMintPerRepo are the mint's rate limit (D2049): this
// many mints for one repository in this long a window. It is a fixed window and
// a fixed number rather than configuration, because the number the owner ruled
// is a bound on a runaway daemon and not a policy an installation tunes; a
// repository that legitimately needs more merges in an hour is a repository
// whose daemon is not the one this bound is for.
const (
	mergeMintWindow    = time.Hour
	mergeMintPerRepo   = 10
	mergeMintMaxAnswer = 1 << 20
)

// mergeRulesetPage is the page size the gate asks GitHub's rulesets list for:
// GitHub's own maximum, so that the list fits in one page for every repository
// that has one at all, and a page that comes back full is refused rather than
// read as the whole list (mergeGate).
const mergeRulesetPage = 100

// headRE is the commit shape a call's head_sha is held to: the 40 hex digits of
// a git object name. It is a shape and not a parse, and the SHA is compared to
// GitHub's answer as the string it is — never normalised, because a SHA that
// was rewritten on the way in is a SHA the gate did not admit.
var headRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// mergeTokenPermissions is the reach of the token this route hands out, and it
// is the whole of "the token never merges past branch protection" on this
// service's side: contents write and pull requests write are what GitHub's
// merge endpoint and its ready-for-review call require, metadata read is the
// noun every installation token carries, and there is no administration noun
// and no bypass of any kind. The set is a constant of this service and not a
// field of the call, which is what makes a caller unable to widen it.
//
// It is defined here rather than in internal/gitprov/github because it is the
// credential of THIS route and the reason the route exists: the run's own mint
// asks for the run set and a person's read asks for the read set, and a fourth
// set that no caller chooses is a fact about this endpoint.
func mergeTokenPermissions() map[string]string {
	return map[string]string{
		"contents":      "write",
		"pull_requests": "write",
		"metadata":      "read",
	}
}

// mergeGateReadPermissions is the reach of the token the GATE reads with. It is
// a read set and nothing else — the pull request the call names and the
// rulesets that cover its base — and its token never leaves this process: what
// a caller is handed is mergeTokenPermissions' mint and never this one.
// administration read is on it because a repository's rulesets are read under
// that noun and no narrower one reaches them.
func mergeGateReadPermissions() map[string]string {
	return map[string]string{
		"contents":       "read",
		"pull_requests":  "read",
		"metadata":       "read",
		"administration": "read",
	}
}

// mergeMintRate is the per-repository rate limit, held in this process's own
// memory. It is deliberately not in the database: what it bounds is a daemon
// looping on this service, a restart of the service is a clean slate for a
// bound of an hour, and a node under creds/ would be a second writer of a fact
// that has no reader.
type mergeMintRate struct {
	mu   sync.Mutex
	now  func() time.Time
	seen map[string][]time.Time
}

func newMergeMintRate(now func() time.Time) *mergeMintRate {
	return &mergeMintRate{now: now, seen: map[string][]time.Time{}}
}

// allow records one mint for slug and reports whether it was within the rate.
// The window is fixed and it is read from the service's own clock, which is the
// clock every other decision of this service is made with.
func (r *mergeMintRate) allow(slug string) bool {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.seen[slug][:0]
	for _, at := range r.seen[slug] {
		if now.Sub(at) < mergeMintWindow {
			kept = append(kept, at)
		}
	}
	if len(kept) >= mergeMintPerRepo {
		// The window is kept as it was: a refused call is not a mint and does
		// not extend the window it was refused by.
		r.seen[slug] = kept
		return false
	}
	r.seen[slug] = append(kept, now)
	return true
}

// mergeBinding is what the gate read: the pull request's current head, and the
// base branch whose rules bind the merge.
type mergeBinding struct {
	head string
	base string
}

// readMergeMintRequest reads a merge mint's body out of the call the handler
// already read (readCall), which is where the daemon's own ID token was
// registered with the service's redaction for the length of this call.
func readMergeMintRequest(body []byte) (MergeMintRequest, error) {
	if len(body) > maxMintRequestBytes {
		return MergeMintRequest{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxMintRequestBytes)
	}
	var req MergeMintRequest
	if err := decodeStrict(body, &req); err != nil {
		return MergeMintRequest{}, errors.New("the body is not one JSON object of four fields")
	}
	if _, _, ok := gitprov.SplitRepo(req.Repository); !ok {
		return MergeMintRequest{}, errors.New("the call names no repository (owner/name)")
	}
	if req.Number < 1 {
		return MergeMintRequest{}, errors.New("the call names no pull request number")
	}
	if !headRE.MatchString(req.HeadSHA) {
		return MergeMintRequest{}, errors.New("the call names no head commit (40 hex digits)")
	}
	if !appIDRE.MatchString(req.AppID) {
		return MergeMintRequest{}, errors.New("the call carries no GitHub App ID (1 to 20 digits)")
	}
	return req, nil
}

// mergeMint answers one merge mint call. It is reached from ServeHTTP's branch
// ahead of RequestFromHTTP, and it does there what githubRead does: it verifies
// its caller, reads its call, resolves what the call names, reads the
// credential it mints from, and only then mints — with the gate between the
// resolution and the mint.
//
// The order below is why each step is where it is:
//
//   - the caller, first, and on its own token: this is the route's whole
//     admission, and a refusal here is typed and has no line, because nothing
//     is verified yet and there is no caller for a line to name.
//   - the run identity the class does not take, before anything else is read.
//   - the call's body, whose read is ALSO the registration of the daemon's own
//     token with the call-scoped scrub.
//   - the operator's stop flag, read BEFORE any credential, so a stopped
//     service reads none.
//   - the repository, resolved through the installation's inventory.
//   - the rate limit, checked before the bundle so that a repository that has
//     been minted for as often as this service allows reads no credential at
//     all.
//   - the bundle and the App key, failing closed.
//   - the gate: the pull request's current head, and the rulesets that cover
//     its base. Both are read with a token of the gate's own, which never
//     leaves this process.
//   - the mint, against api.github.com and no other host.
//   - the answer, built, audited and handed over in that order
//     (answerMergeMint).
func (s *Server) mergeMint(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	purpose, credential := routeAct(r.URL.Path)
	tok, err := bearerFromHTTP(r)
	if err != nil {
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := s.cfg.Auth.VerifyOrchestrator(ctx, tok)
	if err != nil {
		// The class's own refusal, answered the way the read route answers the
		// person class's: a typed refusal and no line.
		s.refuseVerifier(w, err)
		return
	}
	if id := r.Header.Get(HeaderRunIdentity); id != "" {
		s.refuseMergeMint(ctx, w, caller, "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it mints for the orchestrator daemon, and a caller presenting a run is a call of a shape it does not have")
		return
	}
	if job := r.Header.Get(HeaderRunJob); job != "" {
		s.refuseMergeMint(ctx, w, caller, "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it mints for the orchestrator daemon, and a caller naming a job is a call of a shape it does not have")
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		s.refuseMergeMint(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	req, err := readMergeMintRequest(cl.body)
	if err != nil {
		s.refuseMergeMint(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	on, which, err := s.cfg.Kills.Stop(ctx, "")
	if err != nil {
		s.refuseMergeMint(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		s.refuseMergeMint(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return
	}
	slug, owner, name, err := githubRepository(s.cfg.Repos, req.Repository)
	if err != nil {
		s.refuseMergeMint(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused,
			readRefusalMessage(err, req.Repository))
		return
	}
	if !s.mergeMints.allow(slug) {
		s.refuseMergeMint(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable,
			ErrMergeMintRateLimited.Error()+": this service mints for one repository "+fmt.Sprint(mergeMintPerRepo)+" times an hour, and this repository has been minted for as often as that")
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseMergeMint(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	key, err := appKey(b)
	if err != nil {
		if errors.Is(err, ErrMintNoAppKey) {
			err = ErrMergeMintNoAppKey
		} else {
			err = ErrMergeMintBadAppKey
		}
		s.refuseMergeMint(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	_, err = s.mergeGate(ctx, mergeGateCall{
		owner: owner, name: name, appID: req.AppID, key: key, number: req.Number, head: req.HeadSHA,
	})
	if err != nil {
		// The operator gets the class of the failure and, when GitHub answered,
		// its status -- and never a byte of GitHub's own text: the requests that
		// failed carried the App's JWT and an installation token.
		attrs := []any{"purpose", purpose, "credential", credential, "repository", slug}
		var se *httpjson.StatusError
		if errors.As(err, &se) {
			attrs = append(attrs, "status", se.Status)
		}
		s.log.Warn("credsvc: the merge mint's gate refused or could not be read", attrs...)
		switch {
		case errors.Is(err, ErrMergeMintHeadMoved):
			s.refuseMergeMint(ctx, w, caller, slug, StatusRequestRefused, ErrorRequestRefused, ErrMergeMintHeadMoved.Error())
		case errors.Is(err, ErrMergeMintBypassActor):
			s.refuseMergeMint(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, ErrMergeMintBypassActor.Error())
		default:
			s.refuseMergeMint(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, ErrMergeMintGitHub.Error())
		}
		return
	}
	token, expires, err := github.MintInstallationToken(ctx, github.Options{
		Owner: owner, Repo: name, AppID: req.AppID, PrivateKey: key,
		HTTP: s.cfg.Client, Now: s.now,
	}, mergeTokenPermissions())
	if err != nil {
		attrs := []any{"purpose", purpose, "credential", credential, "repository", slug}
		var se *httpjson.StatusError
		if errors.As(err, &se) {
			attrs = append(attrs, "status", se.Status)
		}
		s.log.Warn("credsvc: the merge token could not be minted", attrs...)
		s.refuseMergeMint(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, ErrMergeMintGitHub.Error())
		return
	}
	s.answerMergeMint(ctx, w, caller, slug, MergeMintResponse{Token: token, ExpiresAt: expires, Repository: req.Repository})
}

// mergeGateCall is what the gate reads with: the repository, the App the mint
// signs as, the key it signs with, the pull request the call named and the head
// the call is bound to. It is a struct rather than six arguments because three
// of them are strings of the same shape and an argument swap between them would
// read another repository's rules.
type mergeGateCall struct {
	owner, name string
	appID       string
	key         *rsa.PrivateKey
	number      int
	head        string
}

// mergeGate reads the facts the mint is bound to and refuses when any of them
// does not hold.
//
// Every read is made with an installation token of the gate's own
// (mergeGateReadPermissions), minted from the same App key the answer is minted
// from: the pull request's head and base, the rulesets that cover that base,
// and the base's classic branch protection. The token never leaves this
// process, so the reach the gate needs is not a reach a caller is ever handed.
//
// The rules it holds are G2's and G14's, and all of them fail closed:
//
//   - the head GitHub reports must be the head the call named, character for
//     character. A pull request that moved since the daemon admitted it is
//     refused, and so is one whose head cannot be read.
//   - no ruleset that covers the base may name the App among its bypass
//     actors, and the rulesets must be readable — all of them, which is why a
//     page that comes back full is refused rather than read as the whole list.
//     An empty list is a repository with no rulesets, which is a merge nothing
//     bypasses.
//   - the base's classic branch protection may not name the App among its own
//     bypass actors either, and that read must answer — with the object or with
//     the 404 that says this branch carries no classic protection, which is the
//     ordinary answer for a repository protected by rulesets alone.
//
// What it answers is the binding it read, which the caller of this function
// does not act on: the mint is bound by the refusal and not by a value carried
// forward, and the return is here so a test can read what the gate saw.
func (s *Server) mergeGate(ctx context.Context, call mergeGateCall) (mergeBinding, error) {
	readToken, _, err := github.MintInstallationToken(ctx, github.Options{
		Owner: call.owner, Repo: call.name, AppID: call.appID, PrivateKey: call.key,
		HTTP: s.cfg.Client, Now: s.now,
	}, mergeGateReadPermissions())
	if err != nil {
		return mergeBinding{}, fmt.Errorf("%w: the gate's read token could not be minted: %w", ErrMergeMintGitHub, err)
	}
	var pr struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	path := "/repos/" + call.owner + "/" + call.name + "/pulls/" + strconv.Itoa(call.number)
	if err := s.githubGet(ctx, readToken, path, &pr); err != nil {
		// An unreadable pull request is not a pull request at the head the call
		// named, so it is the binding's refusal and not the upstream's: the
		// direction that refuses is the direction that fails closed.
		return mergeBinding{}, fmt.Errorf("%w: the pull request could not be read: %w", ErrMergeMintHeadMoved, err)
	}
	if pr.Head.SHA != call.head || pr.Base.Ref == "" {
		return mergeBinding{}, fmt.Errorf("%w: the pull request is at %s and the call named %s", ErrMergeMintHeadMoved, shortSHA(pr.Head.SHA), shortSHA(call.head))
	}
	var rulesets []githubRuleset
	rulesPath := "/repos/" + call.owner + "/" + call.name + "/rulesets?per_page=" + strconv.Itoa(mergeRulesetPage)
	more, err := s.githubGetPage(ctx, readToken, rulesPath, &rulesets)
	if err != nil {
		return mergeBinding{}, fmt.Errorf("%w: the base's rulesets could not be read: %w", ErrMergeMintBypassActor, err)
	}
	if more || len(rulesets) >= mergeRulesetPage {
		// The read is one page and the gate needs the whole list, because the
		// ruleset that names the App may be the one past it (G14). A page GitHub
		// says has a next one is refused, and so is a page that comes back full
		// whether or not it says so — a server that ignored the page size would
		// otherwise be read as having answered with everything.
		return mergeBinding{}, fmt.Errorf("%w: the base's rulesets are more than one page of %d", ErrMergeMintBypassActor, mergeRulesetPage)
	}
	if rulesetsNameTheApp(rulesets, pr.Base.Ref, call.appID) {
		return mergeBinding{}, fmt.Errorf("%w: a ruleset covering %s names the App among its bypass actors", ErrMergeMintBypassActor, pr.Base.Ref)
	}
	var protection githubBranchProtection
	protectionPath := "/repos/" + call.owner + "/" + call.name + "/branches/" + url.PathEscape(pr.Base.Ref) + "/protection"
	if err := s.githubGet(ctx, readToken, protectionPath, &protection); err != nil {
		// A branch with no classic protection answers 404, which is the ordinary
		// answer for a repository protected by rulesets alone and is not a
		// refusal. Every other unreadable answer is (G14): a bypass list this
		// gate could not read is not one that names nobody.
		var status *httpjson.StatusError
		if !errors.As(err, &status) || status.Status != http.StatusNotFound {
			return mergeBinding{}, fmt.Errorf("%w: the base's branch protection could not be read: %w", ErrMergeMintBypassActor, err)
		}
	}
	if protection.namesTheApp(call.appID) {
		return mergeBinding{}, fmt.Errorf("%w: the branch protection of %s names the App among its bypass actors", ErrMergeMintBypassActor, pr.Base.Ref)
	}
	return mergeBinding{head: pr.Head.SHA, base: pr.Base.Ref}, nil
}

// githubRuleset is the part of a repository ruleset this gate reads: whether it
// is in force, which refs it covers, and who may bypass it.
//
// It is deliberately this service's own struct and not a type of
// internal/gitprov: what the gate needs is three fields of one answer, and a
// shared type would make the gate's rule a fact about the provider's model
// rather than about this refusal.
type githubRuleset struct {
	Target      string `json:"target"`
	Enforcement string `json:"enforcement"`
	Conditions  struct {
		RefName struct {
			Include []string `json:"include"`
			Exclude []string `json:"exclude"`
		} `json:"ref_name"`
	} `json:"conditions"`
	BypassActors []struct {
		ActorID   int    `json:"actor_id"`
		ActorType string `json:"actor_type"`
	} `json:"bypass_actors"`
}

// rulesetsNameTheApp reports whether any ruleset in force covers branch and
// names the App among its bypass actors. The App is an `Integration` actor and
// its id is the App ID the call named, which is the only spelling GitHub uses
// for it on a bypass list.
//
// A ruleset whose conditions this code cannot read covers everything: an
// unreadable condition is not an absent one, and the direction that refuses is
// the direction that fails closed.
func rulesetsNameTheApp(rulesets []githubRuleset, branch, appID string) bool {
	app, err := strconv.Atoi(appID)
	if err != nil {
		// The App ID is held to the digits-only shape before this is reached, so
		// an unreadable one is a caller's App ID this service would otherwise
		// have signed a JWT as — and a bypass list it cannot compare against is
		// a bypass list it must refuse on.
		return true
	}
	for _, rs := range rulesets {
		if rs.Target != "branch" || rs.Enforcement == "disabled" || !rulesetCoversRef(rs, branch) {
			continue
		}
		for _, actor := range rs.BypassActors {
			if actor.ActorType == "Integration" && actor.ActorID == app {
				return true
			}
		}
	}
	return false
}

// githubBranchProtection is the part of a classic branch protection object this
// gate reads: its own bypass list, which is the second place GitHub lets an App
// be named as an actor that may merge past a protected branch, beside the
// rulesets. Both lists exist because the two features are separate, and a
// repository can carry either one alone.
type githubBranchProtection struct {
	RequiredPullRequestReviews *struct {
		BypassPullRequestAllowances *struct {
			Apps []struct {
				ID int `json:"id"`
			} `json:"apps"`
		} `json:"bypass_pull_request_allowances"`
	} `json:"required_pull_request_reviews"`
}

// namesTheApp reports whether the classic branch protection's bypass list names
// the App. Every absent part of the object — no pull-request-review requirement,
// no allowances, no Apps — is a list that names nobody, because the read that
// produced it answered, and an answer that carries no actor is an answer about a
// branch the App may not bypass. The App ID that will not parse is the same
// refusal rulesetsNameTheApp makes of it.
func (p githubBranchProtection) namesTheApp(appID string) bool {
	app, err := strconv.Atoi(appID)
	if err != nil {
		return true
	}
	if p.RequiredPullRequestReviews == nil || p.RequiredPullRequestReviews.BypassPullRequestAllowances == nil {
		return false
	}
	for _, a := range p.RequiredPullRequestReviews.BypassPullRequestAllowances.Apps {
		if a.ID == app {
			return true
		}
	}
	return false
}

// rulesetCoversRef reports whether a ruleset's conditions reach one branch. It
// is the reading of GitHub's own condition, and it is conservative in the
// direction that refuses: no include list at all covers every ref, an include
// this code cannot decide covers, and an exclude it cannot decide leaves the
// ruleset covering the ref — because a ruleset read as not covering is a
// ruleset skipped, and a skipped ruleset's bypass list is never seen.
func rulesetCoversRef(rs githubRuleset, branch string) bool {
	ref := "refs/heads/" + branch
	for _, exclude := range rs.Conditions.RefName.Exclude {
		switch matchRefPattern(exclude, ref) {
		case refPatternCovers:
			return false
		case refPatternUndecided:
			return true
		}
	}
	if len(rs.Conditions.RefName.Include) == 0 {
		return true
	}
	for _, include := range rs.Conditions.RefName.Include {
		switch matchRefPattern(include, ref) {
		case refPatternCovers, refPatternUndecided:
			return true
		}
	}
	return false
}

// refPatternMatch is what one of GitHub's ref patterns decides about one ref:
// that it covers it, that it does not, or that this code cannot tell. The third
// is why the answer is three states and not a bool — a pattern this code reads
// as "does not cover" while GitHub reads it as covering is a ruleset skipped and
// the App's place on its bypass list never seen, which is the one direction this
// gate must not fail in.
type refPatternMatch int

const (
	refPatternDoesNotCover refPatternMatch = iota
	refPatternCovers
	refPatternUndecided
)

// matchRefPattern decides one of GitHub's ref patterns against one ref.
//
// ~ALL is every ref and ~DEFAULT_BRANCH is the default branch — which this code
// cannot tell from the base it was handed, so it covers, which is the direction
// that refuses. Everything else is GitHub's fnmatch: * and ? match within one
// path segment, ** crosses segments, and the rest is literal. A pattern this
// code does not translate is answered as undecided and never as "does not
// cover", which is what the caller reads it as.
func matchRefPattern(pattern, ref string) refPatternMatch {
	switch pattern {
	case ref, "~ALL", "~DEFAULT_BRANCH":
		return refPatternCovers
	}
	re, err := refPatternRegexp(pattern)
	if err != nil {
		return refPatternUndecided
	}
	if re.MatchString(ref) {
		return refPatternCovers
	}
	return refPatternDoesNotCover
}

// refPatternRegexp translates one of GitHub's ref patterns into the expression
// that matches the same refs, or answers an error for a pattern this code does
// not translate.
//
// The error is not a rejection of the pattern: GitHub's own pattern language is
// wider than this translation (a bracket expression, an escape, a pattern that
// is not a ref at all), and what this function cannot translate it hands back as
// undecided rather than as a ref the pattern misses.
func refPatternRegexp(pattern string) (*regexp.Regexp, error) {
	if !strings.HasPrefix(pattern, "refs/") {
		return nil, errors.New("a ref pattern this code does not translate")
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); {
		switch c := pattern[i]; c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i += 2
				continue
			}
			b.WriteString("[^/]*")
			i++
		case '?':
			b.WriteString("[^/]")
			i++
		case '[', '\\':
			return nil, errors.New("a ref pattern this code does not translate")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// githubGet reads one GitHub REST answer into out with an installation token.
//
// The destination is github.DefaultBaseURL and nothing a caller can name, which
// is the same rule the mint itself is held to: a root a caller could name would
// be the App credential's destination chosen by the caller. GitHub's own text
// is never returned in the error — the request carried a bearer credential, so
// an upstream that echoed the request would be putting one in this service's
// log — and only its status is, in the shape internal/gitprov/httpjson already
// reports one.
func (s *Server) githubGet(ctx context.Context, token, path string, out any) error {
	_, err := s.githubGetPage(ctx, token, path, out)
	return err
}

// githubGetPage is githubGet with the one thing a list needs: whether the answer
// is a page of a longer one. GitHub says so with a Link header naming the next
// page, and a list this gate cannot finish reading is a list it must not read as
// complete (G14), so the caller refuses on a true rather than following it.
func (s *Server) githubGetPage(ctx context.Context, token, path string, out any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, github.DefaultBaseURL+path, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, mergeMintMaxAnswer))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode/100 != 2 {
		return false, &httpjson.StatusError{Status: resp.StatusCode}
	}
	more := linkHasNext(resp.Header.Get("Link"))
	body, err := io.ReadAll(io.LimitReader(resp.Body, mergeMintMaxAnswer))
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return false, errors.New("GitHub's answer is not the object this service reads")
	}
	return more, nil
}

// linkHasNext reports whether GitHub's Link header names a next page. The header
// is a comma-separated list of `<url>; rel="name"` pairs and the gate needs the
// one name; anything it cannot read is answered as a next page, because the
// direction that refuses is the direction that fails closed.
func linkHasNext(header string) bool {
	for _, link := range strings.Split(header, ",") {
		if _, params, ok := strings.Cut(link, ";"); ok && strings.Contains(params, `rel="next"`) {
			return true
		}
	}
	return false
}

// shortSHA is the first eight characters of a commit: what a refusal names, so
// that an operator reading it can tell two heads apart without a line carrying
// a whole object name.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// answerMergeMint builds the merge mint's answer, audits the issuance and
// writes it. It is answerGitHubRead's order and answerGitHubRead's reasons: the
// answer is built first so that a body the service cannot build is a refusal
// with its own line and no issuance line at all, and the audit line is written
// before the token is handed over, because an issuance that cannot be audited
// is not made (C1.13, D143) and a GitHub installation token cannot be unminted.
func (s *Server) answerMergeMint(ctx context.Context, w http.ResponseWriter, caller, repository string, ans MergeMintResponse) {
	body, err := json.Marshal(ans)
	if err != nil {
		s.log.Error("credsvc: the merge mint's answer could not be built", "error", err)
		s.refuseMergeMint(ctx, w, caller, repository, StatusUnavailable, ErrorStateUnavailable, readAnswerRefusal)
		return
	}
	if err := s.auditMergeMint(ctx, caller, repository, StatusOK); err != nil {
		s.log.Error("credsvc: the merge mint's audit line was not written", "error", err)
		s.refuseMergeMint(ctx, w, caller, repository, StatusUnavailable, ErrorStateUnavailable, ErrMergeMintUnaudited.Error())
		return
	}
	writeReadAnswer(w, body)
}

// auditMergeMint writes one merge mint's line: the daemon as its caller, the
// repository the token was for, the App key as its credential, and NEITHER a
// run nor a job — the caller is the installation's orchestrator daemon, which
// holds no run identity and is not a job of this installation.
//
// It does not go through Identity.Audit because there is no Identity: the route
// is answered ahead of Verify, which is the only thing that builds one. The
// repository is the installation's SLUG for it and not the "owner/name" the
// caller typed, so the line is keyed the way every other line of this log is.
// purpose and credential come from routeAct, the ONE rule.
func (s *Server) auditMergeMint(ctx context.Context, caller, repository, status string) error {
	purpose, credential := routeAct(MergeMintPath)
	return s.audit.Write(ctx, AuditEvent{
		Caller:     caller,
		Repository: repository,
		Credential: credential,
		Purpose:    purpose,
		Status:     status,
	})
}

// refuseMergeMint answers one merge-mint refusal and records it, in the shape
// refuseGitHubRead already has: one typed refusal to the caller, one line to
// the operator, and the caller's answer is the same whether or not the line
// lands (D143).
//
// It is reached only once the caller IS verified — the refusals above that
// point are answered by refuse alone, with no line.
func (s *Server) refuseMergeMint(ctx context.Context, w http.ResponseWriter, caller, repository string, status int, typ, message string) {
	if err := s.auditMergeMint(ctx, caller, repository, typ); err != nil {
		s.log.Error("credsvc: a refused merge mint could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}
