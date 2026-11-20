package credsvc

// The mint (C2.1, D145): a run's in-runner gateway asks this service for a
// GitHub installation token, because the App's private key is a credential of
// the service's bundle and not of the job. C2.2 makes the runner ask for it,
// C2.3 takes the job's grant and mount away, and this file is the endpoint
// they meet at.
//
// Four things make "the run's own repository" a property of this service
// rather than a check on something the caller sent:
//
//   - The repository is never the caller's to name. A mint call carries the
//     App's ID and nothing else; the repository is the one the SCOPE's table
//     pairs the calling job account with (Scope.Repos, C1) — the deployment's
//     own table, derived from the job accounts it serves — and the pairing
//     between the run identity and that account is the admission ServeHTTP
//     already makes before any route is served (review finding N1: a run
//     identity is honoured only by the job whose repository it belongs to). A
//     run that asked for another repository has no field to ask in, and a job
//     account bound to another repository never reaches this function.
//   - The repository the table answers is held to the coverage this call's
//     forge credentials reach (ForgeReader.Covers, R5, WS17): a workspace's
//     owner declares the repositories its shared App key is spent on, and this
//     fence holds the table's answer to that declaration. An installation
//     covers every repository its table holds, so the fence refuses nothing
//     its own pairing admitted; a workspace whose owner's declaration does not
//     name the repository is refused before the bundle is read, so the key is
//     never spent on it.
//   - The permissions are the class's and this service adds none: a repository
//     caller is issued github.RunTokenPermissions(), the exported spelling of
//     the set internal/gitprov/github's own provider mints with, so the
//     service's tokens and the in-job ones are the same reach — and a review
//     caller, which the same file admits to this route, is issued CSR1's
//     read-only set instead (mintPermissions, review.go; D883, D1065), because
//     the run's set is the write set of the very repository the review is
//     about.
//   - The key that signs is the bundle's and nothing else. A bundle with no
//     App key is a refusal (TestMintFailsClosedWithNoAppKeyInTheBundle) and
//     never a fallback to the copy a job may still mount (D143).
//
// The App ID is the one thing the caller supplies, and it is not a secret: the
// platform already passes it to a job as FUGARO_GITHUB_APP_ID (a number, not
// a credential), and the service's own environment does not hold it — Global
// Constraint 12 keeps this service's Terraform member count at one container
// and one grant, so nothing was added there to carry it. A caller that names
// the wrong App ID makes GitHub refuse the JWT and the mint fails; it cannot
// make the service sign with another App's key, because the key is the
// bundle's.
//
// This is the one answer of this service that carries a credential's VALUE
// out, and that is what the endpoint is for: a run needs the token itself, and
// the alternative D145 rules out is the job holding the App key. The value is
// written to the answer and to nothing else — no log line, no error, no
// header — and every refusal below is a sentence this file wrote rather than
// another package's, because an upstream's error text is not this service's to
// hand to a run.
//
// A mint is audited through C1.13's auditor (audit.go, PurposeMint, no tier)
// like every other act of this service that touches a credential, and the
// auditor's fail-closed rule is the mint's: a token whose line cannot be
// written is handed to nobody.
//
// How the key gets into the bundle is C1.25's half, which the plan's C2.1 file
// list names (internal/cli/creds.go, the same key through `fugaro creds set`).
// That writer is on this branch and takes the key the way this file reads it:
// `fugaro creds set github-app-key` is the ONE entry whose value is not one
// line, so it accepts exactly one PEM block and parses it with the parser
// below (github.ParsePrivateKey, the same function appKey calls) — a key the
// command stores is a key this mint signs with, and the two answers cannot
// drift. An installation whose bundle holds no key gets exactly the named
// refusal below (ErrMintNoAppKey) and never a fallback, which is also what a
// bundle written before this command knew the key's shape holds. What C2.1 owes
// C1.25 is here and is the shared code that writer reuses: CredentialKey
// answers the App key's kind, the schema admits the key, and the mint reads it
// by kind. Nothing else is needed from this side, and a second writer would be
// the thing to avoid.

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// MintPath is the path a run asks for an installation token on. It is not an
// Anthropic path: the request and the answer are this service's own contract,
// and the two Anthropic paths stay the only ones a run's model traffic takes.
const MintPath = "/v1/github/installation-token"

// ContextTokenPath is the path a run asks for read-only installation tokens
// for its context repositories on (C5b, D613). It is a route of its own and not
// a field of MintPath, and that is the point: a mint call carries no repository
// and must go on carrying none (docs/git-providers.md, C2.1), while this call's
// whole subject is the repositories the run asks to read — which are the ones
// its own identity's claim names, and nothing a caller can widen.
const ContextTokenPath = "/v1/github/context-tokens"

// maxMintRequestBytes bounds a mint call's body. The whole request is one
// small field ({"app_id":"1234"}), so the bound is tiny on purpose: an
// installation token is minted from this body, and a body larger than this is
// not a mint call. A context mint's body is the App's ID and at most
// task.MaxContextRepos repository names, which is smaller still, so the same
// bound holds it.
const maxMintRequestBytes = 4 << 10

// appIDRE is the shape of a GitHub App ID: 1 to 20 digits, the rule
// internal/cli (init), internal/infra and internal/backend/gcp each spell for
// the App ID an operator types. It is checked here so a body that could not
// name an App is refused by this service rather than by GitHub.
var appIDRE = regexp.MustCompile(`^[0-9]{1,20}$`)

// The mint's own refusals. They are sentinels so a test and a reader can tell
// them apart, and every one of them is a sentence written here: none repeats
// GitHub's own error text, which is another service's and can carry up to 512
// bytes of an answer's body.
var (
	// ErrMintBadRequest is a call whose body is not this contract's: not one
	// JSON object of one field, or an App ID that is not 1 to 20 digits.
	ErrMintBadRequest = errors.New("credsvc: the mint call is not one this service reads")
	// ErrMintNoAppKey is a bundle that holds no GitHub App key. It is the
	// fail-closed case C2.1's plan amendment names: a service that minted
	// without one, or fell back to the job's copy, would be indistinguishable
	// from a broken installation (D143).
	ErrMintNoAppKey = errors.New("credsvc: the credentials bundle holds no GitHub App key")
	// ErrMintBadAppKey is a bundle whose App key is not a private key this
	// code can sign with.
	ErrMintBadAppKey = errors.New("credsvc: the credentials bundle's GitHub App key is not a usable private key")
	// ErrMintRepository is a calling job whose repository is not one this
	// service mints a GitHub token for: an installation whose jobs are
	// Bitbucket's, or a canonical name this code cannot read.
	ErrMintRepository = errors.New("credsvc: the calling job's repository is not one this service mints a GitHub token for")
	// ErrMintGitHub is GitHub refusing the mint, or the call to it failing.
	ErrMintGitHub = errors.New("credsvc: GitHub did not mint an installation token")
	// ErrMintUnaudited is a mint GitHub granted and C1.13's auditor could not
	// record. The token exists at GitHub and reaches nobody: this service will
	// not hand a run a credential it could not write a line for (D143's
	// fail-closed rule), and a token cannot be unminted.
	ErrMintUnaudited = errors.New("credsvc: the mint could not be audited, so no installation token was issued")
	// ErrContextGitHub is GitHub refusing a context mint, or the call to it
	// failing. A run halts on it, exactly as it does on ErrMintGitHub (D143):
	// the job holds no App key to fall back to.
	ErrContextGitHub = errors.New("credsvc: GitHub did not mint an installation token for a context repository")
	// ErrContextUnaudited is a context mint GitHub granted and C1.13's auditor
	// could not record. The tokens exist at GitHub and reach nobody, the rule
	// ErrMintUnaudited keeps for the write mint, and they cannot be unminted.
	ErrContextUnaudited = errors.New("credsvc: the context mint could not be audited, so no installation token was issued")
)

// MintRequest is the body of a mint call: the GitHub App's ID, and nothing
// else. There is no repository field on purpose — the repository is the one the
// scope pairs the calling job account with, and a body that names one is refused
// by the strict decoder rather than ignored (Convention 6).
type MintRequest struct {
	AppID string `json:"app_id"`
}

// MintResponse is the answer: the installation token, when it expires, and the
// repository the token is scoped to.
//
// Repository is the CANONICAL name — "<provider>/<owner>/<name>", the spelling
// projectpolicy.Canonical builds and JobAccount.Repo holds ("github/acme/app")
// — and not the storage slug. The two are different strings and both are
// spoken by something: the slug ("acme-app-1a2b3c4d", task.Slug) is the run's
// own record's key, what creds.Run carries and what C1.13's audit line writes
// in its repository field (Identity.Slug), while the canonical name is the one
// the operator onboarded, the one this service's binding table is keyed by, and
// the one GitHub itself takes in the two path segments the mint is scoped with.
// The answer carries the canonical name because a caller that asked for a token
// for its own repository is being told what the token is FOR, in the spelling
// the provider and the onboarding record use — and because the caller already
// knows its own slug, so repeating it would tell it nothing. A caller that
// needs the slug has it (it is its own identity), and no caller needs to
// recover one from the other: task.Slug is one-way.
//
// Token is the credential's value and the reason this endpoint exists. Nothing
// in this package logs it, and a caller must not either.
type MintResponse struct {
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Repository string    `json:"repository"`
}

// ContextTokenRequest is the body of a context mint call (C5b, D613): the
// GitHub App's ID and the repositories the run asks to read.
//
// The list is the caller's ASK and never its reach. The repositories a run may
// be served are the ones the run identity's fcr claim names — minted by the
// launcher from the task the launch was given — and a repository the claim does
// not name is refused by name and audited, so an agent that calls this service
// with the run's own identity gains nothing beyond its launch. Unlike the write
// mint's body, this one has a repository field, and that is what the two routes
// are apart for: there is no widening here either, because the field is read
// against the claim and never trusted.
//
// The names are the canonical "owner/name" the task and the claim hold them in
// (task.CanonicalRepo's form), which is the key space the published policy is
// read in: what the service compares is then a name and not a spelling a caller
// chose.
type ContextTokenRequest struct {
	AppID        string   `json:"app_id"`
	Repositories []string `json:"repositories"`
}

// ContextTokenResponse is a context mint's answer: one installation token per
// App installation the call's repositories belong to.
//
// One per installation and not one for the whole list: a GitHub App
// installation belongs to the owner, and a run's context list may name
// repositories under different owners (D613), so a list spanning two owners is
// answered two tokens — and a single token cannot cover both, because GitHub
// scopes a token to the repositories of the installation it was minted for.
//
// Tokens is internal/gitprov/github's own ContextToken and not a second
// spelling of the same fields: the service writes what the mint built, so this
// wire has one type at both ends and nothing to keep in step. It is never empty
// on an answered call — a call naming no repository is refused before the
// answer is built — and every token in it is read-only
// (github.ContextTokenPermissions).
type ContextTokenResponse struct {
	Tokens []github.ContextToken `json:"tokens"`
}

// readMintRequest reads a mint call's body. It is bounded and decoded strictly
// (Convention 6: a field this code does not know is refused, not ignored), and
// no error here repeats what it read: the body is the caller's text, and a
// decoder's own message names the byte it choked on.
func readMintRequest(r *http.Request) (MintRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMintRequestBytes+1))
	if err != nil {
		return MintRequest{}, fmt.Errorf("%w: the body could not be read", ErrMintBadRequest)
	}
	if len(body) > maxMintRequestBytes {
		return MintRequest{}, fmt.Errorf("%w: the body is larger than the %d bytes this service accepts", ErrMintBadRequest, maxMintRequestBytes)
	}
	var req MintRequest
	if err := decodeStrict(body, &req); err != nil {
		return MintRequest{}, fmt.Errorf("%w: the body is not one JSON object of one field", ErrMintBadRequest)
	}
	if !appIDRE.MatchString(req.AppID) {
		return MintRequest{}, fmt.Errorf("%w: the call carries no GitHub App ID (1 to 20 digits)", ErrMintBadRequest)
	}
	return req, nil
}

// mintRepository splits the canonical repository name the calling job account
// is bound to into the owner and the repository GitHub's API takes.
//
// The name is the deployment's own — the table the scope resolves the calling
// job account in (Scope.Repos, built by JobAccountsFor from the repository the
// operator onboarded) and never the caller's — so this is a parse of a name this
// service was configured with — and it still refuses
// anything that is not exactly "<provider>/<owner>/<name>" of provider github,
// because a name this code cannot read is a name it must not guess a
// repository out of: the token it mints is scoped to what these two strings
// say, and GitHub takes them as given.
func mintRepository(canonical string) (owner, repo string, err error) {
	provider, rest, ok := strings.Cut(canonical, "/")
	if !ok || provider != gitprov.KindGitHub {
		return "", "", ErrMintRepository
	}
	owner, repo, ok = strings.Cut(rest, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", ErrMintRepository
	}
	return owner, repo, nil
}

// appKey is the bundle's GitHub App private key, parsed.
//
// It is found by KIND (Bundle.AppKey) and never by name: an account may be
// named "github-app-key" (claude-oauth-token-github-app-key is a legal named
// account) and resolves to that same name, so a name lookup could not tell the
// key from the token — and a token offered to a PEM parser is the mistake the
// kind exists to make unrepresentable. The kind is what the document says the
// entry is, and only KindGitHubApp is the key.
//
// Neither refusal carries the value: a parse error is another package's
// sentence about a credential, and a credential in an error is a credential in
// a log.
func appKey(b *Bundle) (*rsa.PrivateKey, error) {
	e, ok := b.AppKey()
	if !ok {
		return nil, ErrMintNoAppKey
	}
	key, err := github.ParsePrivateKey([]byte(e.Value()))
	if err != nil {
		return nil, ErrMintBadAppKey
	}
	return key, nil
}

// mint answers one mint call. The caller has been verified and paired with a
// repository before this is reached (ServeHTTP), so what is left is: choose the
// permission set, read the body, ask the scope for the repository this token is
// scoped to and hold it to the coverage, read the App key, mint, and answer.
//
// Every refusal of the mint's own is a state_unavailable refusal — the run
// halts on it (D143) and never falls back to a key of its own — and every one,
// the class's included, is audited through C1.13's auditor (auditMint), whether
// the mint issued or refused, because a mint is one of the acts that file
// exists to record. The class's refusal is the one that is not
// state_unavailable: it is a fact about the run identity, and it is answered —
// and recorded — with the pairing's own vocabulary.
func (s *Server) mint(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity) {
	// The set is chosen first because it is a fact about the identity and not
	// about the request: a class this endpoint does not mint for, and a
	// review-class caller the claim rule refuses, are answered before the body
	// is read and long before the bundle is. The refusal goes through
	// refusePairing — it is the pairing's question about the run identity, asked
	// for this endpoint's second decision (D1065) — so it carries the vocabulary
	// a pairing refusal already carries and no run needs a new case.
	perms, err := mintPermissions(id)
	if err != nil {
		s.refusePairing(ctx, w, id, PurposeMint, config.GitHubAppKeySecret, err)
		return
	}
	req, err := readMintRequest(r)
	if err != nil {
		s.refuseMint(ctx, w, id, err.Error())
		return
	}
	// The repository is the scope's answer and not this handler's to read off
	// the job row: a workspace's table holds the CALLING job account's own
	// repositories and no other member's, so reading the row here would be a
	// second answer to a question the scope has already narrowed. The scope's
	// two fences are asked in that one call and before the bundle is read — the
	// PAIRING, which refuses a slug no table holds, and the COVERAGE, which
	// refuses a repository the workspace owner's declaration does not name — so
	// a repository this caller may not be served costs this service no read of
	// the App key at all.
	canonical, ok := s.admitMintRepository(ctx, w, id)
	if !ok {
		return
	}
	owner, repo, err := mintRepository(canonical)
	if err != nil {
		s.refuseMint(ctx, w, id, err.Error())
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseMint(ctx, w, id, BundleRefusal(err))
		return
	}
	key, err := appKey(b)
	if err != nil {
		s.refuseMint(ctx, w, id, err.Error())
		return
	}
	token, expires, err := github.MintInstallationToken(ctx, github.Options{
		Owner: owner, Repo: repo, AppID: req.AppID, PrivateKey: key,
		HTTP: s.cfg.Client, Now: s.now,
	}, perms)
	if err != nil {
		// The operator gets the class of the failure and, when GitHub
		// answered, its status — and never a byte of GitHub's own text. Two
		// reasons, and either is enough: httpjson's StatusError quotes up to
		// 512 bytes of the answer's body, which is another service's sentence
		// and can echo whatever it chose; and the request that failed carried
		// the App's JWT, so an upstream that echoed the request would be
		// putting a bearer credential in this service's log. The status alone
		// is what an operator acts on (401 is a revoked key, 404 a missing
		// installation), and it is logged beside the name of the credential
		// and never beside its value.
		attrs := []any{
			"purpose", PurposeMint, "credential", config.GitHubAppKeySecret,
			"repository", canonical, "run", id.Run,
		}
		var se *httpjson.StatusError
		if errors.As(err, &se) {
			attrs = append(attrs, "status", se.Status)
		}
		s.log.Warn("credsvc: the GitHub installation token could not be minted", attrs...)
		s.refuseMint(ctx, w, id, ErrMintGitHub.Error())
		return
	}
	// The answer is built, audited and handed over in that order, in
	// answerMint: an answer the service cannot build is a refusal that writes
	// its own line and no issuance line at all, and an issuance whose line
	// cannot be written is a refusal too (C1.13, D143).
	s.answerMint(ctx, w, id, MintResponse{Token: token, ExpiresAt: expires, Repository: canonical})
}

// admitMintRepository answers the canonical repository this call's installation
// token is scoped to, and it is the whole of what the scope decides for a mint:
// the name the calling job account is paired with in the scope's own table, held
// to the coverage this call's forge credentials reach.
//
// ok is false when the call was refused, and that is the one answer a caller has
// to act on by returning: every refusal here is already written, audited and
// answered by the helper that chose it.
//
// **The pairing is read here and no longer passed in.** It is the same read
// ServeHTTP made — the slug a run identity carries resolved through the
// deployment's table (Scope.Repos, review finding N1) — and it is made again
// because the table is the scope's and not the server's: a workspace's holds
// the repositories of the CALLING job account and no other member's, and what
// this endpoint mints must be scoped to one of those. Reading the row the
// dispatcher resolved would be reading the server's table, which is the answer
// this task put behind the scope.
func (s *Server) admitMintRepository(ctx context.Context, w http.ResponseWriter, id Identity) (string, bool) {
	sc := scopeOfCall(ctx, s)
	canonical, paired := sc.Repos()[id.Slug]
	if !paired {
		// Unreachable through ServeHTTP — the gate resolved this very table and
		// refused an unpaired slug before it dispatched the route, with the
		// sentence below — and kept rather than assumed: the alternative to
		// refusing is minting for the zero repository, which is a token for a
		// repository nobody named. The call reaching it is refused exactly as the
		// gate refuses it, with the pairing's own vocabulary, so a reader of the
		// line cannot tell the two apart and does not have to.
		const message = "the run identity is not a run of the calling job's repository"
		s.refuseCall(ctx, w, id, PurposeMint, config.GitHubAppKeySecret, "", StatusUnverified, ErrorUnverifiedRunIdentity, message)
		return "", false
	}
	covered, err := sc.Forge().Covers(ctx, canonical)
	if err != nil {
		// A coverage that could not be read is not a coverage that names nothing
		// (C1's own rule for Covers): the fail-closed direction is to refuse, and
		// the sentence is this file's rather than the reader's, which may carry a
		// node's path or a database's error.
		s.refuseMint(ctx, w, id, mintCoverageUnreadable)
		return "", false
	}
	if !covered {
		s.refuseMint(ctx, w, id, mintNotCovered(canonical))
		return "", false
	}
	return canonical, true
}

// mintCoverageUnreadable is what a mint is refused with when the coverage this
// call's forge credentials reach could not be read.
const mintCoverageUnreadable = "the repositories this call's forge credentials reach could not be read, so no installation token is issued for any of them"

// mintNotCovered is the refusal of a repository the coverage does not name: the
// workspace owner's declaration is the list of repositories a shared App key is
// spent on, and the fix is the owner's own.
//
// It names the repository, which is a name and never a credential, and it names
// the one command that publishes a different declaration (R19, F28): a member's
// operator reading this line has nothing to change on their own side, and a
// sentence that left the fix out would send them looking for one.
func mintNotCovered(canonical string) string {
	return "the coverage this call's forge credentials reach does not name " + canonical + ": a workspace's shared GitHub credential is spent on the repositories its owner declares, and the owner's `fugaro update` is what publishes them"
}

// answerMint builds the mint's answer, audits the issuance and writes it.
//
// The order is the point of this function being one:
//
//   - the answer is built FIRST, and a body the service cannot build is a
//     refusal answered through refuseMint — which writes the refusal's OWN
//     line — with no issuance line written at all. The issuance's line says a
//     token was issued, so a line written for an answer that carries no token
//     is a mint an operator reads as served, and the one act this service hands
//     a credential out for would be the act its log got wrong. Building the
//     body is pure (json.Marshal over three fields) and touches no credential,
//     so nothing is spent by doing it before the line;
//   - the audit line is written BEFORE the token is handed over, and a line
//     that cannot be written is a refusal (C1.13, D143: an issuance that
//     cannot be audited is not made). A token cannot be unminted at GitHub, so
//     what "not made" means here is that it reaches nobody: no answer carries
//     it, no log line and no error does either, and the run halts on
//     state_unavailable like any other refusal of this endpoint.
//
// Both refusals are answered through refuseMint and not written here, because
// each IS a refusal of this endpoint after admission: the act that could not be
// recorded (or could not be built) is the issuance, and a failure to write one
// line is no reason to leave the line an operator does need — the mint that
// handed out no token — unwritten. (refuseMint may fail its own line as well;
// then the failure is on the service's log twice, which is where an operator
// looks for an audit log that is not recording.)
//
// The branch that builds no body is unreachable through a real mint: the answer
// is three fields, two of them strings, and the third is the expiry GitHub's API
// named as an RFC3339 timestamp, whose year is four digits — which is every year
// encoding/json writes. It is kept rather than dropped because the alternative
// to answering is a panic inside the one act that hands out a credential, and
// the call reaching it is the call that must be refused; it is driven by hand in
// TestAMintWhoseAnswerCannotBeBuiltIsRefusedUnaudited.
func (s *Server) answerMint(ctx context.Context, w http.ResponseWriter, id Identity, ans MintResponse) {
	body, err := json.Marshal(ans)
	if err != nil {
		s.log.Error("credsvc: the mint's answer could not be built", "error", err)
		s.refuseMint(ctx, w, id, mintAnswerRefusal)
		return
	}
	if err := s.auditMint(ctx, id, StatusOK); err != nil {
		s.log.Error("credsvc: the mint's audit line was not written", "error", err)
		s.refuseMint(ctx, w, id, ErrMintUnaudited.Error())
		return
	}
	writeMintAnswer(w, body)
}

// mintAnswerRefusal is what a mint whose own answer cannot be built is refused
// with: this file's own sentence, and never a word of json's error, which names
// the byte it choked on and can quote the value it was handed.
const mintAnswerRefusal = "the service could not build the mint's answer"

// refuseMint audits a refused mint and answers it.
//
// The audit line comes first, and the answer is the same whatever the line
// does: an act this service will not record is not made, and a mint it refuses
// is not made either way, so the run gets the refusal it would have got and the
// operator gets the auditor's own error on the service's log. (The issuance
// path is the one where the two differ, and it refuses on a line it could not
// write — see answerMint.)
//
// It answers through refuseCall, the ONE refusal helper this service has, so a
// refused mint is written and answered by the same code as every other refusal
// after admission: one refusal sink, one audit sink (Server.audit through
// auditCall) and one rule for what a mint's line names (routeAct). The mint had
// a helper of its own before that answered its refusals with s.refuse, which
// left the mint's refusals and the answer path's two shapes of one act that
// could drift apart.
//
// The refusal's message is the caller's, and it is always one of this file's
// own sentences: GitHub's text is never relayed. The status is the one every
// refusal of this endpoint carries — state_unavailable, the word the runner's
// halt message and the operator's line read alike.
func (s *Server) refuseMint(ctx context.Context, w http.ResponseWriter, id Identity, message string) {
	purpose, credential := routeAct(MintPath)
	s.refuseCall(ctx, w, id, purpose, credential, "", StatusUnavailable, ErrorStateUnavailable, message)
}

// auditMint writes one mint's line through C1.13's auditor (audit.go), which
// is the service's one writer of audit lines: the line is written by auditCall,
// the same helper the answer path writes through, and the purpose and the
// credential it carries come from routeAct — the ONE rule, so the mint's own
// refusals, the refusals ServeHTTP answers before the mint's branch (the
// binding and the stop switches) and this line cannot name the act or the
// credential two ways. The event is completed from the identity the service
// verified (Identity.Audit — the run, the repository slug, the job the run
// reported and the account that called, and none of them from anything a caller
// typed), the credential is the App key's NAME, the purpose is PurposeMint, and
// there is NO tier.
//
// The absent tier is the shape C1.13 defines for a mint and not an omission: a
// tier names the model credential a call spent (A, B or C), the App key is not
// a model credential of the cascade and is spent on no call, and the auditor
// REFUSES a tier on a mint rather than recording one this service never
// assigned. So a mint is recorded without inventing a tier for the key.
//
// The status is C1.13's own vocabulary, not one of this file's words: StatusOK
// for a token that was issued, and the refusal's wire error type for one that
// was not. Every refusal this endpoint writes is state_unavailable, which is
// the error type the wire contract and this file already share — so the status
// is a word the runner's halt message and the operator's line read alike.
//
// The writer is the service's one auditor (Server.audit) and not a second one
// taken from the configuration: there is one audit log, the service builds the
// writer from the handler it was configured with (NewServer's NewProbeAuditor),
// and that writer is armed with the bundle loader's own redaction — so a mint's
// lines are redacted by the same code and the same values as every other line
// this service writes, and no caller of this package can hand the mint a writer
// the arming was forgotten on.
//
// Nothing in the event can carry a value: the App key's value and the minted
// token have no field to be written into, the credential field is the name,
// and the auditor's own redaction (armed by the service, WithAuditScrub) runs
// over every field before any rule looks at one.
//
// A returned error means the act was not recorded and the caller must not go
// on with it — the mint's answer is a refusal in that case, and a refusal's
// answer is the refusal it already had.
func (s *Server) auditMint(ctx context.Context, id Identity, status string) error {
	purpose, credential := routeAct(MintPath)
	return s.auditCall(ctx, id, purpose, credential, "", status)
}

// writeMintAnswer writes the mint's answer, from the body answerMint already
// built and audited. It is the one answer of this service that carries a
// credential's value, so it is no-store: an intermediary that cached an
// installation token would be handing the next caller a credential this service
// did not mint for it.
//
// No header of this answer carries the value, and none of the forward path's
// headers is set: X-Fugaro-Tier and X-Fugaro-Credential describe a credential
// that answered an Anthropic call, and a mint has neither (the App key is not
// a model credential and no cascade chose it).
func writeMintAnswer(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// readContextTokenRequest reads a context mint call's body. It is bounded and
// decoded strictly (Convention 6), and it holds every name to the canonical
// form the claim and the published policy are read in, so what the handler
// compares is a name and not a spelling the caller chose.
//
// Its refusals are VALUES only in the shape they break and never in what they
// carried: the body is the caller's text, and a name that is not owner/name is
// refused without being repeated.
func readContextTokenRequest(r *http.Request) (ContextTokenRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMintRequestBytes+1))
	if err != nil {
		return ContextTokenRequest{}, errors.New("the body could not be read")
	}
	if len(body) > maxMintRequestBytes {
		return ContextTokenRequest{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxMintRequestBytes)
	}
	var req ContextTokenRequest
	if err := decodeStrict(body, &req); err != nil {
		return ContextTokenRequest{}, errors.New("the body is not one JSON object of this contract's fields")
	}
	if !appIDRE.MatchString(req.AppID) {
		return ContextTokenRequest{}, errors.New("the call carries no GitHub App ID (1 to 20 digits), and a context token is signed as the App")
	}
	if len(req.Repositories) == 0 {
		return ContextTokenRequest{}, errors.New("the call names no repository: a context token covers exactly the repositories the call lists, and a call naming none asks for nothing")
	}
	if len(req.Repositories) > task.MaxContextRepos {
		return ContextTokenRequest{}, fmt.Errorf("the call names more than %d repositories, and a run's context list is bounded there (task.MaxContextRepos)", task.MaxContextRepos)
	}
	seen := make(map[string]bool, len(req.Repositories))
	for _, repo := range req.Repositories {
		canonical, err := task.CanonicalRepo(repo)
		if err != nil {
			return ContextTokenRequest{}, errors.New("the call names a repository that is not owner/name")
		}
		if canonical != repo {
			return ContextTokenRequest{}, errors.New("the call names a repository that is not in its canonical form: a repository is lower-cased owner/name, as acme/lib")
		}
		if seen[repo] {
			return ContextTokenRequest{}, errors.New("the call names the same repository twice")
		}
		seen[repo] = true
	}
	return req, nil
}

// contextAdmission is the claim-and-policy answer for one context mint call:
// nil when every repository asked for may be read, and the refusal to answer
// with otherwise.
//
// It carries the refusal's own status and type because the two halves are
// refused differently, the split the build route's narrowing draws (buildRepos):
// a repository the claim or the policy does not name is the CALLER's request
// refused — an agent reaching past its launch, or a launch whose repository has
// since left the project — while a policy this service could not read is the
// service unable to answer, which is no caller's fault and is answered
// state_unavailable.
type contextAdmission struct {
	status  int
	typ     string
	message string
}

func (a *contextAdmission) Error() string { return a.message }

// admitContextRepos holds one call's repositories to the two lists that decide
// them, and answers the first one that refuses.
//
// The CLAIM is read first, and it is the boundary: the repositories a run may
// read are the ones the launcher signed into its identity (C5a), so a call
// naming any other repository is refused by name before the policy is consulted
// at all — an agent calling this service with the job's identity gains nothing
// beyond its launch, and an operator reading the line reads a request that
// asked for something it was never granted.
//
// The POLICY is read second and re-read on every call, because the claim is the
// launch's answer and the policy is the project's: the launcher admits a
// context repository the way it admits the run's own (the project's
// allow_repos), and `fugaro project repos rm` narrows that list afterwards. A
// repository the claim names and the policy no longer admits is refused here
// rather than served a token the project has withdrawn — the same re-read the
// run's own mint gets from the pairing (pairShared).
//
// A policy that is not published is not read as an empty allow-list: it refuses
// the call, which is the fail-closed direction the launcher takes for the same
// repository (a launch naming a context repository needs a published policy).
//
// The policy is read from the SCOPE and not from the service's configuration
// (WS17): an installation's scope answers its own store, which is where this
// route has always read it, and a workspace's answers the MEMBER's — the project
// whose repositories the run was launched to read, which is the project whose
// policy admitted them at the launch (C1's rule 2: the readers are the caller's,
// and the workspace's own node is not the one a context repository is admitted
// by).
func (s *Server) admitContextRepos(ctx context.Context, sc Scope, id Identity, repos []string) error {
	claimed := make(map[string]bool, len(id.ContextRepos))
	for _, repo := range id.ContextRepos {
		claimed[repo] = true
	}
	for _, repo := range repos {
		if !claimed[repo] {
			return &contextAdmission{status: StatusRequestRefused, typ: ErrorRequestRefused,
				message: requestRefused + "the repository " + repo + " is not one this run's context repositories name, and a context token covers the repositories the launch recorded and no others"}
		}
	}
	shared, found, err := sc.Policy().Shared(ctx)
	if err != nil || !found {
		// A node that is not there is not a read that failed (labelRead answers a
		// nil err as nil), so the two share one sentence: there is no allow-list
		// to admit a context repository, and a run is never served a repository
		// the project's own list does not name.
		return &contextAdmission{status: StatusUnavailable, typ: ErrorStateUnavailable,
			message: "the project's policy could not be read, so no allow_repos admits a context repository"}
	}
	for _, repo := range repos {
		canonical, err := projectpolicy.Canonical(gitprov.KindGitHub, repo)
		if err != nil {
			// Unreachable through this route — readContextTokenRequest held the
			// name to the same rule — and kept rather than assumed: the
			// alternative to refusing is comparing a name this code could not
			// read against an allow-list, which would admit nothing and say so
			// as a different refusal.
			return &contextAdmission{status: StatusUnavailable, typ: ErrorStateUnavailable,
				message: "the call names a context repository this service cannot read, and no allow_repos can admit one it cannot read"}
		}
		if !projectpolicy.AdmitRepo(shared.AllowRepos, canonical) {
			// NotAdmitted's own sentence, which is the one the runner's
			// admission and the launcher's advisory twin already answer with:
			// one refusal for the same repository, worded once, so an operator
			// reading the service's line and the launcher's report reads the
			// same repository and the same fix.
			return &contextAdmission{status: StatusRequestRefused, typ: ErrorRequestRefused,
				message: requestRefused + projectpolicy.NotAdmitted(shared.AllowRepos, canonical)}
		}
	}
	return nil
}

// contextTokens answers one context mint call (C5b, D613). The caller has been
// verified and paired before this is reached (ServeHTTP), so what is left is:
// read the body, hold the repositories to the run's own claim and to the
// calling scope's policy, read the App key, mint one token per installation, and
// answer.
//
// The order is the boundary's and not an accident. The claim and the policy are
// read before the bundle, so a call that may not be served reads no credential
// at all; the policy is read before the key because it is what decides whether
// there is a call to serve; and GitHub is asked last, so the two refusals a
// caller causes itself are answered without this service having signed anything
// at the App.
//
// What this route adds to the run's reach is one read-only token per installed
// owner (github.ContextTokenPermissions), for repositories the launch recorded
// — and nothing else. No Terraform and no grant come with it: the service still
// holds its one bundle grant and mints from the App key it already holds
// (TestCredentialsServiceGrantsAreTheOneBundle is unchanged).
func (s *Server) contextTokens(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity) {
	req, err := readContextTokenRequest(r)
	if err != nil {
		s.refuseContextTokens(ctx, w, id, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if err := s.admitContextRepos(ctx, scopeOfCall(ctx, s), id, req.Repositories); err != nil {
		var refused *contextAdmission
		if errors.As(err, &refused) {
			s.refuseContextTokens(ctx, w, id, refused.status, refused.typ, refused.message)
			return
		}
		// Unreachable through this route — admitContextRepos answers its
		// refusals as contextAdmission and nothing else — and kept rather than
		// assumed, the shape the build route's own narrowing keeps: answering
		// is what a call that got here needs, and a panic inside the one act
		// that hands out a credential is the one thing not to do.
		s.refuseContextTokens(ctx, w, id, StatusUnavailable, ErrorStateUnavailable,
			"the call's context repositories could not be read")
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseContextTokens(ctx, w, id, StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	key, err := appKey(b)
	if err != nil {
		s.refuseContextTokens(ctx, w, id, StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	tokens, err := github.MintContextTokens(ctx, github.Options{
		AppID: req.AppID, PrivateKey: key, HTTP: s.cfg.Client, Now: s.now,
	}, req.Repositories, github.ContextTokenPermissions())
	if err != nil {
		// The write mint's rule for the same failure: the operator gets the
		// class of it and, when GitHub answered, its status — and never a byte
		// of GitHub's own text, which can quote the body of a request that
		// carried the App's JWT.
		attrs := []any{
			"purpose", PurposeContextToken, "credential", config.GitHubAppKeySecret,
			"repository", id.Slug, "run", id.Run,
		}
		var se *httpjson.StatusError
		if errors.As(err, &se) {
			attrs = append(attrs, "status", se.Status)
		}
		s.log.Warn("credsvc: an installation token for a context repository could not be minted", attrs...)
		s.refuseContextTokens(ctx, w, id, StatusUnavailable, ErrorStateUnavailable, ErrContextGitHub.Error())
		return
	}
	s.answerContextTokens(ctx, w, id, tokens)
}

// answerContextTokens builds the context mint's answer, audits the issuance and
// writes it, in that order and for the reasons answerMint keeps: an answer the
// service cannot build writes the refusal's own line and no issuance line, and
// an issuance whose line cannot be written is a refusal, because a token cannot
// be unminted and what "not made" means is that it reaches nobody.
//
// The answer is written by writeMintAnswer, which is the same writer the write
// mint uses: both answers carry a credential's value, both are no-store, and
// neither carries a header of the forward path. It also means there is one
// place in this service where a token is put on the wire.
func (s *Server) answerContextTokens(ctx context.Context, w http.ResponseWriter, id Identity, tokens []github.ContextToken) {
	body, err := json.Marshal(ContextTokenResponse{Tokens: tokens})
	if err != nil {
		s.log.Error("credsvc: the context mint's answer could not be built", "error", err)
		s.refuseContextTokens(ctx, w, id, StatusUnavailable, ErrorStateUnavailable, mintAnswerRefusal)
		return
	}
	if err := s.auditContextTokens(ctx, id, StatusOK); err != nil {
		s.log.Error("credsvc: the context mint's audit line was not written", "error", err)
		s.refuseContextTokens(ctx, w, id, StatusUnavailable, ErrorStateUnavailable, ErrContextUnaudited.Error())
		return
	}
	writeMintAnswer(w, body)
}

// refuseContextTokens audits a refused context mint and answers it, refuseMint's
// shape for this route: one refusal sink, one audit sink, and the purpose and
// credential read from routeAct — the ONE rule — so this route's refusals and
// its issuance line cannot name the act or the credential two ways.
//
// The status and the type are the caller's, because this route has two classes
// of refusal: the request's own (a repository the claim or the policy does not
// name) and the service's (a bundle, a key, or GitHub). Both are audited before
// the answer is written, the rule every refusal of this service keeps (C1.13).
func (s *Server) refuseContextTokens(ctx context.Context, w http.ResponseWriter, id Identity, status int, typ, message string) {
	purpose, credential := routeAct(ContextTokenPath)
	s.refuseCall(ctx, w, id, purpose, credential, "", status, typ, message)
}

// auditContextTokens writes one context mint's line through C1.13's auditor, the
// write mint's shape (auditMint): the event is completed from the identity the
// service verified — the run, its repository slug, the job it reported and the
// account that called — the credential is the App key's NAME, the purpose is
// this route's own, and there is no tier, because the App key is not a model
// credential and was spent on no call. A tier here would be a tier the service
// never assigned, and the auditor refuses one rather than recording it.
//
// What the line does NOT carry is which repositories were served: a line's
// repository field is the run's own (Identity.Audit), and the context list is a
// property of the call rather than of the run. The refusal's own sentence names
// the repository a call was refused for, which is where an operator reads it —
// and the line says, by its purpose, that a run read something beyond its own
// repository at that instant.
func (s *Server) auditContextTokens(ctx context.Context, id Identity, status string) error {
	purpose, credential := routeAct(ContextTokenPath)
	return s.auditCall(ctx, id, purpose, credential, "", status)
}
