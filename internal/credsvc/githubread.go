package credsvc

// The GitHub read mint: a PERSON asks this service for a READ-ONLY GitHub
// installation token, and the service mints it from the App key its bundle
// holds. It is the GitHub half of BB4's read mint, and the answer to the one
// thing a person's GitHub read could not do without it: a person's machine
// holds no App key (C2.2, D145), so `fugaro coordinator show` on a GitHub pull
// request had nothing to mint with and, before this route, asked for the App's
// ID and key out of its own environment.
//
// It is bitbucket.go's route with the credential changed, and the four things
// that make that route what it are this one's too:
//
//   - The caller is a PERSON, and only a person. VerifyPersonCaller is C3.9f's
//     mechanism against the installation's people list, and the route is
//     answered ahead of RequestFromHTTP for the reason the verify route is:
//     a person holds no run identity, and the shared parse refuses a call that
//     has none. A caller that presents one anyway is refused by name. The
//     coordinator job is deliberately NOT admitted here, unlike on the Bitbucket
//     read mint: it holds the App credential itself (coordinatorAppCredential)
//     and mints its own read-only token, so this route would be a second way to
//     the same credential for a caller that has no need of it.
//   - The permissions are the coordinator's read-only set and this service adds
//     none (github.CoordinatorTokenPermissions): contents and pull requests
//     read. It is the same set the project-wide reader mints in-process with,
//     so a person's read and the coordinator's own read are the same reach —
//     and the set is asked for by name rather than derived from the caller,
//     which is what makes "this route never mints a write" a property of one
//     constant and not of a check.
//   - The key that signs is the bundle's and nothing else. A bundle with no App
//     key is a refusal and never a fallback to a key the caller may hold.
//   - The App ID is the caller's, and it is not a secret: the mint route's own
//     answer (github.go). The service's environment does not hold it — Global
//     Constraint 12 keeps this service's Terraform member count where it is —
//     so the person supplies it from the project's own configuration
//     (`repos.<owner/name>.github_app_id`, localcfg.Repo), which is the same
//     fact the platform passes to a job as FUGARO_GITHUB_APP_ID. A caller that
//     names the wrong App ID makes GitHub refuse the JWT and the mint fails; it
//     cannot make the service sign with another App's key, because the key is
//     the bundle's.
//
// The route names ONE repository, and the service resolves it through the
// installation's own inventory: a repository this installation does not hold is
// refused BY NAME, and a canonical name this code cannot read is refused rather
// than guessed at. What it refuses is a token for a repository the installation
// has not onboarded — never a token whose reach is narrower: the token is scoped
// by GitHub to the repository the mint names, and the reach it carries is the
// permission set above.
//
// The mint goes to api.github.com and to nothing else (github.MintInstallationToken
// with no BaseURL override), exactly as the run mint does: a root a caller could
// name would be the App credential's destination chosen by the caller.
//
// Every act of it is audited once through C1.13's auditor (audit.go,
// PurposeGitHubRead, no tier): the line names the person who asked and the
// repository the token was for, and it carries no run and no job, because the
// caller is neither (callerWithoutJobPurpose, repositoryOnlyPurpose). A mint
// whose line cannot be written hands the token to nobody (D143).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/task"
)

// GitHubReadPath is the path a person asks for a read-only GitHub installation
// token on. It is not an Anthropic path: the request and the answer are this
// service's own contract, and it is served ahead of the shared request parse
// (ServeHTTP) because its caller holds no run identity.
//
// github.ServiceReadPath is the same path spelled by the client that calls it,
// and TestGitHubReadRouteNamesTheClientPath holds the two to each other, so a
// rename of either cannot quietly move the route.
const GitHubReadPath = "/v1/github/read-token"

// GitHubReadRequest is a read mint's body: the one repository the token is
// asked for, in the installation's own spelling ("owner/name"), and the App ID
// the mint's JWT is signed as.
//
// It is two fields and it is two on purpose. There is no permission field, no
// repository-list field and no API-root field: the permissions are this
// service's constant (github.CoordinatorTokenPermissions), the token is scoped
// to the one repository named here and to no other, and the endpoint the mint
// goes to is api.github.com and nothing a caller can move. A body that carries
// any of them is refused by the strict decoder rather than read for the fields
// it does know (Convention 6), which is what makes "no caller can widen this
// mint" a property of the route and not a check inside it.
//
// AppID is the mint route's own field for the mint route's own reason: it is
// the one thing the caller supplies to a GitHub App mint and it is not a
// secret. It is validated to the App ID shape here so a body that could not
// name an App is refused by this service rather than by GitHub.
type GitHubReadRequest struct {
	Repository string `json:"repository"`
	AppID      string `json:"app_id"`
}

// GitHubReadResponse is the read mint's answer: the token, when it expires, and
// the repository it was minted for.
//
// Repository is the repository the CALLER named, in the same "owner/name"
// spelling the request carried — not the canonical name a mint's answer carries
// (MintResponse.Repository), for the reason bitbucket.go's answer gives: here
// the caller named the repository, and the answer's field is what the client
// checks its own request against before it uses the token. A caller that asked
// for one repository and was answered about another has been handed a token it
// must not use.
//
// Token is the credential's value and the reason this endpoint exists. Nothing
// in this package logs it, and a caller must not either.
type GitHubReadResponse struct {
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Repository string    `json:"repository"`
}

// The GitHub read mint's own refusals. They are sentinels so a test and a reader
// can tell them apart, and every one is a sentence written here: GitHub's own
// answer never reaches a caller, because the request that failed carried the
// App's JWT.
var (
	// ErrGitHubReadBadRequest is a call whose body is not this contract's: not
	// one JSON object of two fields, a repository that is not "owner/name", or
	// an App ID that is not 1 to 20 digits.
	ErrGitHubReadBadRequest = errors.New("credsvc: the GitHub read mint call is not one this service reads")
	// ErrGitHubReadUnknownRepository is a repository this installation's
	// inventory does not hold. It is answered by name and never guessed at: the
	// list of repositories this route may be asked about is the installation's
	// own.
	ErrGitHubReadUnknownRepository = errors.New("credsvc: the repository is not one of this installation's")
	// ErrGitHubReadNoAppKey is a bundle that holds no GitHub App key. It is the
	// fail-closed case the mint's own plan amendment names (D143): a service
	// that minted without one, or fell back to a key the caller holds, would be
	// indistinguishable from a broken installation.
	ErrGitHubReadNoAppKey = errors.New("credsvc: the credentials bundle holds no GitHub App key")
	// ErrGitHubReadBadAppKey is a bundle whose App key is not a private key this
	// code can sign with.
	ErrGitHubReadBadAppKey = errors.New("credsvc: the credentials bundle's GitHub App key is not a usable private key")
	// ErrGitHubReadGitHub is GitHub refusing the mint, or the call to it
	// failing.
	ErrGitHubReadGitHub = errors.New("credsvc: GitHub did not mint an installation token")
	// ErrGitHubReadUnaudited is a token GitHub granted and C1.13's auditor could
	// not record. The token exists at GitHub and reaches nobody: this service
	// will not hand out a credential it could not write a line for (D143).
	ErrGitHubReadUnaudited = errors.New("credsvc: the GitHub read mint could not be audited, so no installation token was issued")
)

// githubRepository resolves the owner and repository of one repository from the
// installation's inventory: the slug the repository's runs are keyed by
// (task.Slug, the one derivation the launcher, the runner and this service
// share), the canonical name the inventory holds for it, and the two path
// segments GitHub's API takes.
//
// It answers ErrGitHubReadUnknownRepository for a repository no row holds, and
// it refuses a row whose value is not a GitHub canonical name: a table entry
// this code cannot read is one it must not guess a repository out of, because
// the token is then scoped by what these two strings say.
func githubRepository(repos map[string]string, repo string) (slug, owner, name string, err error) {
	slug, err = task.Slug(gitprov.KindGitHub, repo)
	if err != nil {
		return "", "", "", ErrGitHubReadUnknownRepository
	}
	canonical, ok := repos[slug]
	if !ok {
		return "", "", "", ErrGitHubReadUnknownRepository
	}
	owner, name, err = mintRepository(canonical)
	if err != nil {
		return "", "", "", ErrGitHubReadUnknownRepository
	}
	return slug, owner, name, nil
}

// readGitHubReadRequest reads a read mint's body out of the call the handler
// already read (readCall), which is where the person's own ID token was
// registered with the service's redaction for the length of this call.
//
// The bound is the mint's 4 KiB and not the gateway's: the whole request is two
// short fields, so a body larger than this is not a read mint. The decode is
// strict, as every body of this contract is, which is what makes a request that
// tries to name the permissions, a second repository or an API root a refusal
// rather than a request whose extra field was quietly dropped.
func readGitHubReadRequest(body []byte) (GitHubReadRequest, error) {
	if len(body) > maxMintRequestBytes {
		return GitHubReadRequest{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxMintRequestBytes)
	}
	var req GitHubReadRequest
	if err := decodeStrict(body, &req); err != nil {
		return GitHubReadRequest{}, errors.New("the body is not one JSON object of two fields")
	}
	if _, _, ok := gitprov.SplitRepo(req.Repository); !ok {
		return GitHubReadRequest{}, errors.New("the call names no repository (owner/name)")
	}
	if !appIDRE.MatchString(req.AppID) {
		return GitHubReadRequest{}, errors.New("the call carries no GitHub App ID (1 to 20 digits)")
	}
	return req, nil
}

// githubRead answers one read mint call. It is reached from ServeHTTP's branch
// ahead of RequestFromHTTP, and it does there what bitbucketRead does: it
// verifies its caller, reads its call, resolves what the call names, reads the
// credential it mints from, mints, and records the act.
//
// The order below is why each step is where it is, and it is bitbucket.go's
// order step for step:
//
//   - the caller, first, and on its own token: this is the route's whole
//     admission. A refusal here is typed and has no line — nothing is verified
//     yet, so there is no caller for a line to name.
//   - the run identity the class does not take, before anything else is read: a
//     person who presents one is refused as a shape of call this route does not
//     have.
//   - the call's body, whose read is ALSO the registration of the person's own
//     token with the call-scoped scrub.
//   - the operator's stop flag, read BEFORE the bundle, so a stopped service
//     reads no credential at all. It is the global switch alone, with no slug
//     (F34): this route has no run.
//   - the bundle and the App key, failing closed.
//   - the repository, resolved through the installation's inventory.
//   - the mint, against api.github.com and no other host.
//   - the answer, built, audited and handed over in that order (answerGitHubRead).
func (s *Server) githubRead(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	purpose, credential := routeAct(r.URL.Path)
	tok, err := bearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified, which is
		// the one refusal on this route with nothing to name.
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := s.cfg.Auth.VerifyPersonCaller(ctx, tok)
	if err != nil {
		// The class's own refusal, answered the way the read route answers the
		// person class's: a typed refusal and no line.
		s.refuseVerifier(w, err)
		return
	}
	if id := r.Header.Get(HeaderRunIdentity); id != "" {
		s.refuseGitHubRead(ctx, w, caller, "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it mints for a person, and a caller presenting a run is a call of a shape it does not have")
		return
	}
	if job := r.Header.Get(HeaderRunJob); job != "" {
		s.refuseGitHubRead(ctx, w, caller, "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it mints for a person, and a caller naming a job is a call of a shape it does not have")
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		// A body this service will not hold is a refused REQUEST and not an
		// unavailable service. The read is also what registers the person's own
		// token with the call's scrub, so nothing below this line can echo it.
		s.refuseGitHubRead(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	req, err := readGitHubReadRequest(cl.body)
	if err != nil {
		// The refusal names the field and never what the call sent, and it is
		// answered before the bundle is read: a request this contract refuses
		// reads no credential at all.
		s.refuseGitHubRead(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	on, which, err := s.cfg.Kills.Stop(ctx, "")
	if err != nil {
		// An unreadable switch is not an off switch (budget's own rule), so the
		// call is refused rather than served.
		s.refuseGitHubRead(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		s.refuseGitHubRead(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseGitHubRead(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	key, err := appKey(b)
	if err != nil {
		// The App key's own two refusals, respelled so that a read mint's
		// operator reads the act it was asked for: the sentences are the mint's
		// own and say the same thing, because it is the same key in the same
		// bundle that is missing or malformed.
		if errors.Is(err, ErrMintNoAppKey) {
			err = ErrGitHubReadNoAppKey
		} else {
			err = ErrGitHubReadBadAppKey
		}
		s.refuseGitHubRead(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	slug, owner, name, err := githubRepository(s.cfg.Repos, req.Repository)
	if err != nil {
		// The refusal names the repository, which is the caller's own text and
		// not a credential: an operator reading it has to know which repository
		// was asked about — and only while that text is a repository NAME, so
		// nothing a caller sent that is not one is repeated (readRefusalMessage).
		s.refuseGitHubRead(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused,
			readRefusalMessage(err, req.Repository))
		return
	}
	token, expires, err := github.MintInstallationToken(ctx, github.Options{
		Owner: owner, Repo: name, AppID: req.AppID, PrivateKey: key,
		HTTP: s.cfg.Client, Now: s.now,
	}, github.CoordinatorTokenPermissions())
	if err != nil {
		// The operator gets the class of the failure and, when GitHub answered,
		// its status -- and never a byte of GitHub's own text: the request that
		// failed carried the App's JWT, so an upstream that echoed the request
		// would be putting a bearer credential in this service's log.
		attrs := []any{"purpose", purpose, "credential", credential, "repository", slug}
		var se *httpjson.StatusError
		if errors.As(err, &se) {
			attrs = append(attrs, "status", se.Status)
		}
		s.log.Warn("credsvc: the GitHub read token could not be minted", attrs...)
		s.refuseGitHubRead(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, ErrGitHubReadGitHub.Error())
		return
	}
	s.answerGitHubRead(ctx, w, caller, slug, GitHubReadResponse{Token: token, ExpiresAt: expires, Repository: req.Repository})
}

// answerGitHubRead builds the read mint's answer, audits the issuance and
// writes it. It is answerRead's order and answerRead's reasons: the answer is
// built first so that a body the service cannot build is a refusal with its own
// line and no issuance line at all, and the audit line is written before the
// token is handed over, because an issuance that cannot be audited is not made
// (C1.13, D143) and a GitHub installation token cannot be unminted.
func (s *Server) answerGitHubRead(ctx context.Context, w http.ResponseWriter, caller, repository string, ans GitHubReadResponse) {
	body, err := json.Marshal(ans)
	if err != nil {
		s.log.Error("credsvc: the read mint's answer could not be built", "error", err)
		s.refuseGitHubRead(ctx, w, caller, repository, StatusUnavailable, ErrorStateUnavailable, readAnswerRefusal)
		return
	}
	if err := s.auditGitHubRead(ctx, caller, repository, StatusOK); err != nil {
		s.log.Error("credsvc: the read mint's audit line was not written", "error", err)
		s.refuseGitHubRead(ctx, w, caller, repository, StatusUnavailable, ErrorStateUnavailable, ErrGitHubReadUnaudited.Error())
		return
	}
	writeReadAnswer(w, body)
}

// auditGitHubRead writes one GitHub read mint's line: the person as its caller,
// the repository the token was for, the App key as its credential, and NEITHER
// a run nor a job — the caller is a person, who holds no run identity and is
// not a job of this installation.
//
// It does not go through Identity.Audit because there is no Identity: the read
// path is answered ahead of Verify, which is the only thing that builds one. The
// repository is the installation's SLUG for it and not the "owner/name" the
// caller typed, so the line is keyed the way every other line of this log is.
// purpose and credential come from routeAct, the ONE rule.
func (s *Server) auditGitHubRead(ctx context.Context, caller, repository, status string) error {
	purpose, credential := routeAct(GitHubReadPath)
	return s.audit.Write(ctx, AuditEvent{
		Caller:     caller,
		Repository: repository,
		Credential: credential,
		Purpose:    purpose,
		Status:     status,
	})
}

// refuseGitHubRead answers one read-mint refusal and records it, in the shape
// refuseRead already has: one typed refusal to the caller, one line to the
// operator, and the caller's answer is the same whether or not the line lands
// (D143).
//
// It is reached only once the caller IS verified — the refusals above that point
// are answered by refuse alone, with no line.
func (s *Server) refuseGitHubRead(ctx context.Context, w http.ResponseWriter, caller, repository string, status int, typ, message string) {
	if err := s.auditGitHubRead(ctx, caller, repository, typ); err != nil {
		s.log.Error("credsvc: a refused read mint could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}
