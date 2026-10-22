package credsvc

// The mint (C2.1, D145): a run's in-runner gateway asks this service for a
// GitHub installation token, because the App's private key is a credential of
// the service's bundle and not of the job. C2.2 makes the runner ask for it,
// C2.3 takes the job's grant and mount away, and this file is the endpoint
// they meet at.
//
// Three things make "the run's own repository" a property of this service
// rather than a check on something the caller sent:
//
//   - The repository is never the caller's to name. A mint call carries the
//     App's ID and nothing else; the repository is the one the calling job
//     account is bound to (JobAccount.Repo), and the pairing between the run
//     identity and that account is the admission ServeHTTP already makes
//     before any route is served (review finding N1: a run identity is
//     honoured only by the job whose repository it belongs to). A run that
//     asked for another repository has no field to ask in, and a job account
//     bound to another repository never reaches this function.
//   - The permissions are the run's own set and this service adds none:
//     github.RunTokenPermissions is the exported spelling of the set
//     internal/gitprov/github's own provider mints with, so the service's
//     tokens and the in-job ones are the same reach.
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
// How the key gets into the bundle is C1.25's half, which the plan's C2.1 file
// list names (internal/cli/creds.go, the same key through `fugaro creds set`).
// That file is NOT on this branch — no `creds` command exists here yet — so on
// this branch the App key has no writer, and an installation whose bundle holds
// none gets exactly the named refusal below (ErrMintNoAppKey) and never a
// fallback. What C2.1 owes C1.25 is here and is the shared code that writer
// reuses: CredentialKey answers the App key's kind, the schema admits the key,
// and the mint reads it by kind. Nothing else is needed from this side, and a
// second writer would be the thing to avoid.

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
)

// MintPath is the path a run asks for an installation token on. It is not an
// Anthropic path: the request and the answer are this service's own contract,
// and the two Anthropic paths stay the only ones a run's model traffic takes.
const MintPath = "/v1/github/installation-token"

// maxMintRequestBytes bounds a mint call's body. The whole request is one
// small field ({"app_id":"1234"}), so the bound is tiny on purpose: an
// installation token is minted from this body, and a body larger than this is
// not a mint call.
const maxMintRequestBytes = 4 << 10

// appIDRE is the shape of a GitHub App ID: 1 to 20 digits, the rule
// internal/cli (init), internal/infra and internal/backend/gcp each spell for
// the App ID an operator types. It is checked here so a body that could not
// name an App is refused by this service rather than by GitHub.
var appIDRE = regexp.MustCompile(`^[0-9]{1,20}$`)

// mintPurpose is the audit line's purpose for a mint (C1.13's shape: run,
// repository, credential name, purpose, status).
const mintPurpose = "github-installation-token"

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
)

// MintRequest is the body of a mint call: the GitHub App's ID, and nothing
// else. There is no repository field on purpose — the repository is the one
// the calling job account is bound to, and a body that names one is refused by
// the strict decoder rather than ignored (Convention 6).
type MintRequest struct {
	AppID string `json:"app_id"`
}

// MintResponse is the answer: the installation token, when it expires, and the
// repository it is scoped to (the canonical name, which is the one the run's
// own record and the audit line speak).
//
// Token is the credential's value and the reason this endpoint exists. Nothing
// in this package logs it, and a caller must not either.
type MintResponse struct {
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Repository string    `json:"repository"`
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
// The name is the deployment's own (JobAccount.Repo, built by JobAccountsFor
// from the repository the operator onboarded) and never the caller's, so this
// is a parse of a name this service was configured with — and it still refuses
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

// mint answers one mint call. The caller has been verified and bound to a
// repository before this is reached (ServeHTTP), so what is left is: read the
// body, read the App key, resolve the repository, mint, and answer.
//
// Every refusal is a state_unavailable refusal — the run halts on it (D143) and
// never falls back to a key of its own — and the audit line is written whether
// the mint issued or refused (C1.13: one line per issuance and per probe).
func (s *Server) mint(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity, ja JobAccount) {
	req, err := readMintRequest(r)
	if err != nil {
		s.auditMint(id, ja, "refused")
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	owner, repo, err := mintRepository(ja.Repo)
	if err != nil {
		s.auditMint(id, ja, "refused")
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.auditMint(id, ja, "refused")
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	key, err := appKey(b)
	if err != nil {
		s.auditMint(id, ja, "refused")
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	token, expires, err := github.MintInstallationToken(ctx, github.Options{
		Owner: owner, Repo: repo, AppID: req.AppID, PrivateKey: key,
		HTTP: s.cfg.Client, Now: s.now,
	}, github.RunTokenPermissions())
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
			"purpose", mintPurpose, "credential", config.GitHubAppKeySecret,
			"repository", ja.Repo, "run", id.Run,
		}
		var se *httpjson.StatusError
		if errors.As(err, &se) {
			attrs = append(attrs, "status", se.Status)
		}
		s.log.Warn("credsvc: the GitHub installation token could not be minted", attrs...)
		s.auditMint(id, ja, "failed")
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, ErrMintGitHub.Error())
		return
	}
	s.auditMint(id, ja, "issued")
	writeMintAnswer(w, MintResponse{Token: token, ExpiresAt: expires, Repository: ja.Repo})
}

// auditMint writes the audit line for one mint: run, repository, credential
// name, purpose, status. It is C1.13's shape as that task's plan writes it,
// and it carries no tier: the App key is not spent on a call, so it has none.
//
// C1.13 is the task that owns the service's audit line, and it writes it in
// internal/credsvc/audit.go, which is NOT landed on this branch — so this is a
// local spelling of that shape rather than a call into its writer, and it is
// the ONE reconciliation C2.1 owes when C1.13 lands: fold this line into
// C1.13's writer, keeping the fields below and keeping the tier absent (a tier
// names the credential a model call spent, and a mint spends none). The shape
// is pinned by TestMintAudited so the fold is a move and not a rewrite.
//
// The line names the App key and never its value, and the token that was
// minted is in neither.
func (s *Server) auditMint(id Identity, ja JobAccount, status string) {
	s.log.Info("credsvc: github installation token",
		"purpose", mintPurpose,
		"credential", config.GitHubAppKeySecret,
		"repository", ja.Repo,
		"run", id.Run,
		"job", id.Job,
		"status", status,
	)
}

// writeMintAnswer writes the mint's answer. It is the one answer of this
// service that carries a credential's value, so it is no-store: an
// intermediary that cached an installation token would be handing the next
// caller a credential this service did not mint for it.
//
// No header of this answer carries the value, and none of the forward path's
// headers is set: X-Fugaro-Tier and X-Fugaro-Credential describe a credential
// that answered an Anthropic call, and a mint has neither (the App key is not
// a model credential and no cascade chose it).
func writeMintAnswer(w http.ResponseWriter, ans MintResponse) {
	body, err := json.Marshal(ans)
	if err != nil {
		WriteWireError(w, StatusUnavailable, WireError{
			Error:   ErrorStateUnavailable,
			Message: "the service could not build the mint's answer",
		})
		return
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
