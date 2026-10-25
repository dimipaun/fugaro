// Package github is the GitHub adapter: App JWT authentication and
// repo-scoped installation tokens (design §6.1, §6.2).
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// ParsePrivateKey parses a GitHub App private key in PEM form: PKCS#1
// ("RSA PRIVATE KEY", what GitHub issues) or PKCS#8 ("PRIVATE KEY").
func ParsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("github app private key is not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing github app private key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github app private key is not an RSA key")
	}
	return rk, nil
}

// appJWT returns the RS256 JSON Web Token that authenticates as the App.
// It is backdated a minute against clock drift and lives 9 minutes, under
// GitHub's 10-minute maximum.
func appJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", err
	}
	signed := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("signing github app jwt: %w", err)
	}
	return signed + "." + enc.EncodeToString(sig), nil
}

// appAuth returns an httpjson.Client Auth function that sends a fresh App JWT.
func appAuth(appID string, key *rsa.PrivateKey, now func() time.Time) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		jwt, err := appJWT(appID, key, now())
		if err != nil {
			return "", err
		}
		return "Bearer " + jwt, nil
	}
}

// tokenPermissions is what a run's installation token asks for (design
// §6.1): no more than the run needs, even if the App was granted more.
// RunTokenPermissions is its exported copy, for the caller outside this
// package that mints a run's token itself (the credentials service, C2.1) and
// must ask for exactly these and add none.
var tokenPermissions = map[string]string{
	"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read",
}

// buildTokenPermissions is what an image build's token asks for: it only
// clones the repository.
var buildTokenPermissions = map[string]string{"contents": "read", "metadata": "read"}

// coordinatorTokenPermissions is what the coordinator's token asks for (P2 of
// docs/coordinator/implementation-plan.md, reconciliation Q3 answered (A)):
// the reads the project-wide view is built from, and no value carrying a write
// anywhere. A coordinator that could write would be a coordinator that could
// answer a merge (Q10 C, refused), which is why this set is a set of its own
// rather than a level taken off the run's.
//
// Two permissions, because two are what this read uses: every pull request is
// read one at a time through gitprov.Provider.PullRequest -- GET
// /repos/{owner}/{repo}/pulls/{number} -- which is pull_requests: read, and
// metadata: read, which GitHub requires of any request at all. contents and
// issues were asked for and never used: this reader opens no file and reads no
// issue, and a permission nobody uses is reach a stolen token would have for
// nothing. P5's aggregation job re-reads through this same reader, so it asks
// for no more than these two.
var coordinatorTokenPermissions = map[string]string{
	"pull_requests": "read", "metadata": "read",
}

// BuildTokenPermissions are the permissions of an image build's token, a
// copy the caller may keep.
func BuildTokenPermissions() map[string]string { return maps.Clone(buildTokenPermissions) }

// CoordinatorTokenPermissions are the permissions of the coordinator's
// installation token, a copy the caller may keep: the read-only set the
// coordinator's credential is, minted through MintInstallationToken with it.
// Pull requests are read with pull_requests: read (U2, the owner's check that
// the App holds it), and nothing here asks to write anything — the rule the
// set's own test holds to the mint's body.
func CoordinatorTokenPermissions() map[string]string {
	return maps.Clone(coordinatorTokenPermissions)
}

// RunTokenPermissions are the permissions of a run's installation token, a
// copy the caller may keep. It is the exported spelling of tokenPermissions,
// which is the set the in-process provider mints with: a caller that mints a
// run's token through MintInstallationToken (the credentials service, C2.1)
// asks for this and adds nothing, so the two paths cannot drift into asking
// GitHub for different reaches on the same run.
func RunTokenPermissions() map[string]string { return maps.Clone(tokenPermissions) }

// GitUsername is the HTTPS username git uses with an installation token.
const GitUsername = gitUsername

// buildTokenMinValid is how long a build's token must still be valid when
// minted: the build clones again in its last step, well within this.
const buildTokenMinValid = 30 * time.Minute

// MintInstallationToken mints one installation token for o's repository,
// scoped to it alone and to perms, and valid for at least half an hour
// (GitHub gives it about an hour). It returns the token and its expiry.
func MintInstallationToken(ctx context.Context, o Options, perms map[string]string) (string, time.Time, error) {
	if o.Owner == "" || o.Repo == "" {
		return "", time.Time{}, errors.New("github: owner and repository are required")
	}
	if o.AppID == "" || o.PrivateKey == nil {
		return "", time.Time{}, errors.New("github: an App ID and private key are required")
	}
	if len(perms) == 0 {
		return "", time.Time{}, errors.New("github: an installation token needs its permissions")
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	app := &httpjson.Client{BaseURL: strings.TrimSuffix(o.BaseURL, "/"), HTTP: o.HTTP, Auth: appAuth(o.AppID, o.PrivateKey, o.Now), Header: http.Header{
		"Accept":               {"application/vnd.github+json"},
		"X-Github-Api-Version": {"2022-11-28"},
		"User-Agent":           {"fugaro"},
	}}
	src := &tokenSource{app: app, owner: o.Owner, repo: o.Repo, now: o.Now, permissions: maps.Clone(perms)}
	return src.Token(ctx, buildTokenMinValid)
}

// tokenSource mints installation tokens scoped to one repository and to
// permissions (nil means a run's, tokenPermissions), and caches the
// current one until it gets close to expiring.
type tokenSource struct {
	app          *httpjson.Client // authenticated as the App (JWT)
	owner, repo  string
	permissions  map[string]string
	now          func() time.Time
	mu           sync.Mutex
	installation int64
	token        string
	expires      time.Time
}

// Token returns an installation token valid for at least minValid, minting
// a new one when the cached token would expire sooner. A new token lives
// about an hour, so a minValid beyond that mints on every call.
func (s *tokenSource) Token(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && s.expires.Sub(s.now()) >= minValid {
		return s.token, s.expires, nil
	}
	if s.installation == 0 {
		var inst struct {
			ID int64 `json:"id"`
		}
		path := "/repos/" + url.PathEscape(s.owner) + "/" + url.PathEscape(s.repo) + "/installation"
		if err := s.app.Do(ctx, "GET", path, nil, &inst); err != nil {
			return "", time.Time{}, fmt.Errorf("finding the github app installation for %s/%s: %w", s.owner, s.repo, err)
		}
		if inst.ID == 0 {
			return "", time.Time{}, fmt.Errorf("github returned no installation id for %s/%s", s.owner, s.repo)
		}
		s.installation = inst.ID
	}
	var tok struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	perms := s.permissions
	if perms == nil {
		perms = tokenPermissions
	}
	body := map[string]any{"repositories": []string{s.repo}, "permissions": perms}
	if err := s.app.Do(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", s.installation), body, &tok); err != nil {
		return "", time.Time{}, fmt.Errorf("minting an installation token for %s/%s: %w", s.owner, s.repo, err)
	}
	if tok.Token == "" {
		return "", time.Time{}, fmt.Errorf("github returned an empty installation token for %s/%s", s.owner, s.repo)
	}
	// The GitAuth contract (gitprov.go) promises a token valid for at least
	// minValid; a token that GitHub minted with too little life left (or a
	// missing/zero expiry) cannot honor that, so it is an error rather than
	// a token the caller would treat as good.
	if tok.ExpiresAt.Sub(s.now()) < minValid {
		return "", time.Time{}, fmt.Errorf(
			"github minted an installation token for %s/%s that expires too soon to satisfy the requested %s validity",
			s.owner, s.repo, minValid)
	}
	s.token, s.expires = tok.Token, tok.ExpiresAt
	return s.token, s.expires, nil
}

// The contract of the credentials service's mint (C2.1, D145): the path, the
// two headers that carry the identities a mint is verified by, and the fields
// of the request and the answer. They are spelled here rather than imported
// because this package cannot import internal/credsvc -- credsvc imports this
// one, for MintInstallationToken and RunTokenPermissions -- and internal/credsvc's
// TestTheServiceTokenContractIsTheMints holds every one of them to the
// service's own spelling, so the two cannot drift apart.
const (
	// ServiceTokenPath is where the service mints a run's installation token.
	ServiceTokenPath = "/v1/github/installation-token"
	// ServiceHeaderRunIdentity carries the run's own Firebase ID token: the
	// identity the service verifies the run by (credsvc.HeaderRunIdentity).
	ServiceHeaderRunIdentity = "X-Fugaro-Run-Identity"
	// ServiceHeaderRunJob names the Cloud Run job the run reports it is in
	// (credsvc.HeaderRunJob). The service holds it to the job-name rule and
	// decides nothing from it: which job called is decided from the verified
	// service-account email.
	ServiceHeaderRunJob = "X-Fugaro-Run-Job"
)

// maxServiceAnswerBytes bounds a mint's answer. It is one small object (a
// token, an expiry and the repository's name), so a longer answer is not one
// this contract wrote, and the refusal below is what an operator sees rather
// than a body of unknown size.
const maxServiceAnswerBytes = 16 << 10

// serviceMintTimeout bounds one mint call. A mint is a short call -- one
// request, one answer, no streaming -- so a service that neither answers nor
// fails is cut off here rather than holding the run until the platform's own
// job timeout. The in-job path has the same shape (the API clients' own
// timeouts) and this one has no stage deadline above it: the run's first mint
// is the clone's, which bootstrap asks for before any budget session exists.
const serviceMintTimeout = 30 * time.Second

// serviceMintRequest is a mint call's body and serviceMintAnswer is the
// service's answer (credsvc.MintRequest and MintResponse): the App's ID, and
// nothing else, because the repository is the one the calling job account is
// bound to and a body that named one is refused there.
type serviceMintRequest struct {
	AppID string `json:"app_id"`
}

type serviceMintAnswer struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// serviceWireError is the error code an answer carries when the service
// refused (credsvc's wire contract): a bounded word like "state_unavailable".
// The code is read to say WHICH refusal it was; the message beside it is
// another writer's text and is never relayed -- see mint.
var serviceErrorCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// The refusals a service mint can end in. Every one of them is a sentence
// written here, and none of them carries a token, an identity or the
// service's own text: the value this endpoint exists to hand over reaches the
// caller and nothing else -- no log line, no error, and no other field.
var (
	// ErrServiceTokens is the credentials service refusing a mint, or the
	// call to it failing. A run halts on it (D143): the job holds no App key
	// of its own to fall back to.
	ErrServiceTokens = errors.New("github: the credentials service did not mint an installation token")
	// ErrServiceTokensIdentity is this run's inability to present the two
	// identities a mint is verified by. It is a fact about how the run was
	// launched -- a run with no budget identity is not one the service can
	// serve -- and never about the service.
	ErrServiceTokensIdentity = errors.New("github: the identities the credentials service verifies a mint with are not available")
)

// ServiceTokens is where a run's installation tokens come from when the
// installation's credentials service holds the App key (C2.2, D145): the
// service mints them, and the job holds no App key at all.
//
// It is the service's mint (C2.1) as a TokenSource, and it is deliberately
// shaped like the in-job tokenSource: the same cache, the same rule about
// minValid, and the same refusal of a token that could not satisfy the
// caller. What differs is where the token comes from and who signs for it --
// and that a mint is an act the service AUDITS (C1.13), so a token that is
// handed out and thrown away per call would be a line per API call. The cache
// is what keeps one mint per hour per run rather than one per call.
//
// Every field but HTTP, Job and Now is required, and a missing one is a
// refusal (ErrServiceTokensIdentity) rather than a call the service would
// answer with less: a mint without the run's own identity is not one the
// service verifies, and a call under the job's identity alone is not a call
// this code makes (C1.10).
type ServiceTokens struct {
	// URL is the credentials service's base URL, from the job's environment
	// (the installation's own Cloud Run service, C1.15/D147).
	URL string
	// AppID is the GitHub App's ID: a number, not a credential, and the one
	// thing a mint's body carries. The service's own environment does not
	// hold it (Global Constraint 12: one bundle secret and one grant), and
	// the job's does (FUGARO_GITHUB_APP_ID), so the caller names it -- and
	// cannot name anything else: the key that signs, the repositories and
	// the permissions are the service's.
	AppID string
	// HTTP is the client the call is made with; nil means this package's own
	// hardened one (serviceMintClient: no redirects, no jar, no compression,
	// a bounded timeout). A client passed in is copied with the same
	// redirect and jar rules; its transport and timeout stay the caller's.
	HTTP *http.Client
	// IDToken mints the job service account's Google ID token for URL, which
	// is what Cloud Run IAM admits for run.invoker (D135). It is called on
	// every mint and never cached here: see metadataIDToken in internal/runner
	// for why the job does not cache one.
	IDToken func(ctx context.Context) (string, error)
	// RunIDToken is the run's own Firebase ID token, the identity the budget
	// backend admitted it with (C1.10). It is read per call -- the session's
	// token changes under it -- and an empty value is a refusal: the service
	// verifies both identities on every call, and there is no second way to
	// be admitted.
	RunIDToken func() string
	// Job is the Cloud Run job name the run reports it is in. Empty is
	// allowed here and refused by the service: this is a claim the service
	// holds to its own rule and decides nothing from.
	Job string
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// ServiceTokens is the TokenSource a provider installs (gitprov.TokenSource)
// when the installation's service holds the App key.
var _ gitprov.TokenSource = (*ServiceTokens)(nil)

func (s *ServiceTokens) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Token returns an installation token valid for at least minValid, minting
// one at the service when the cached token would expire sooner -- the same
// contract, and the same rule, as the in-job tokenSource.
//
// The whole check-then-mint runs under the lock, as tokenSource's does: many
// goroutines (the API calls of a stage) reach Token at once, and a mint the
// service AUDITS must not happen once per caller.
func (s *ServiceTokens) Token(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && s.expires.Sub(s.now()) >= minValid {
		return s.token, s.expires, nil
	}
	tok, exp, err := s.mint(ctx, minValid)
	if err != nil {
		return "", time.Time{}, err
	}
	s.token, s.expires = tok, exp
	return s.token, s.expires, nil
}

// mint makes one mint call and returns its token.
//
// Nothing here can put a value in an error: the identities and the token are
// never formatted into one, and the service's own answer is read for the
// status and the error code alone. The message beside that code is the
// service's text -- a sentence this project wrote, but written for the
// operator's log and not for a run, and an answer's body is another writer's
// to fill -- so it is dropped rather than relayed, exactly as the forward
// path's refusals are (credsvc/github.go's mint logs GitHub's status and
// never its text, for the same reason in the other direction).
func (s *ServiceTokens) mint(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	if s.URL == "" || s.AppID == "" || s.IDToken == nil || s.RunIDToken == nil {
		return "", time.Time{}, fmt.Errorf("%w: this run has no credentials service to mint at", ErrServiceTokensIdentity)
	}
	// The URL is checked before anything is sent to it. A mint presents the
	// run's own Firebase ID token, which is a bearer credential for the whole
	// run: over plain http it would travel in cleartext, and a URL whose
	// scheme nobody chose is not one to hand it to. https, or http on a
	// loopback address, is the rule the model path already holds its own URL
	// to (internal/gateway's checkBaseURL, internal/runner's serviceUpstream).
	if err := checkServiceURL(s.URL); err != nil {
		return "", time.Time{}, err
	}
	run := s.RunIDToken()
	if run == "" {
		return "", time.Time{}, fmt.Errorf("%w: the run's own identity token is not available, and a mint under the job's identity alone is not one the service verifies", ErrServiceTokensIdentity)
	}
	id, err := s.IDToken(ctx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the job's identity token could not be minted: %v", ErrServiceTokensIdentity, err)
	}
	if id == "" {
		return "", time.Time{}, fmt.Errorf("%w: the metadata server answered with no identity token", ErrServiceTokensIdentity)
	}
	body, err := json.Marshal(serviceMintRequest{AppID: s.AppID})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the mint's request could not be built", ErrServiceTokens)
	}
	// One clean header value each: a token with a newline in it is refused
	// here rather than sent, the rule internal/runner keeps for the same two
	// values on the forward path.
	for _, v := range []string{run, id} {
		if !printableValue(v) {
			return "", time.Time{}, fmt.Errorf("%w: an identity token is not one header value", ErrServiceTokensIdentity)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(s.URL, "/")+ServiceTokenPath, bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the mint's request could not be built", ErrServiceTokens)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+id)
	req.Header.Set(ServiceHeaderRunIdentity, run)
	req.Header.Set(ServiceHeaderRunJob, s.Job)
	resp, err := serviceMintClient(s.HTTP).Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the credentials service could not be reached", ErrServiceTokens)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxServiceAnswerBytes+1))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer could not be read", ErrServiceTokens)
	}
	if len(data) > maxServiceAnswerBytes {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer is larger than a mint's", ErrServiceTokens)
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("%w: the service answered %d%s", ErrServiceTokens, resp.StatusCode, refusalCode(data))
	}
	var ans serviceMintAnswer
	if err := json.Unmarshal(data, &ans); err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer is not a mint's", ErrServiceTokens)
	}
	if ans.Token == "" {
		// An empty token is no token: it would fail every git call as an
		// authentication error, deep in a stage, and it must never be
		// cached as if it were one.
		return "", time.Time{}, fmt.Errorf("%w: the service's answer carries no token", ErrServiceTokens)
	}
	if ans.ExpiresAt.Sub(s.now()) < minValid {
		// The GitAuth contract promises a token valid for at least minValid,
		// and the token has already been minted and audited at the service:
		// it reaches nobody, and it is never printed, so the caller gets the
		// refusal instead of a credential it could not honor.
		return "", time.Time{}, fmt.Errorf("%w: the token the service minted expires too soon to satisfy the requested %s validity", ErrServiceTokens, minValid)
	}
	return ans.Token, ans.ExpiresAt, nil
}

// checkServiceURL refuses a credentials service URL this code will not send a
// mint to: https, or http on a loopback address (a test's fixture server, or
// a service run on the run's own machine). It is internal/gateway's
// checkBaseURL rule for the model path (gateway.go) and internal/runner's for
// the forward path, respelled here because neither package exports it and
// this one cannot import them (credsvc imports this package, and the runner
// imports credsvc).
//
// The refusal carries the URL's shape and never the URL: the job's
// environment wrote it, and this package's refusals name no value.
func checkServiceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: the credentials service URL must be a scheme and a host only", ErrServiceTokens)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("%w: the credentials service URL must be https, or http on a loopback address", ErrServiceTokens)
}

// serviceMintClient is the client a mint is made with: it never follows a
// redirect, never keeps a cookie jar, and -- when this code builds the
// transport rather than being handed one -- never asks for compression on its
// own and never waits on an answer forever.
//
// The redirect half is the one that matters most here. A mint's request
// carries the run's own Firebase ID token in X-Fugaro-Run-Identity and the
// job's Google ID token in Authorization; Go's client strips Authorization
// across hosts but not a custom header, so a followed 307 or 308 would hand
// the run's identity to whatever host the Location named. Every other
// credential-carrying client in this repository refuses redirects for exactly
// that reason (internal/gateway's upstreamClient, internal/credsvc's own copy
// of it, pinned by forward_test.go). The URL's scheme is checked too
// (checkServiceURL): a followed http:// hop, or an http:// service URL, would
// send the same value in cleartext.
//
// A client handed in is copied, not used as it is, so its redirect policy and
// jar are this function's whatever the caller set -- the same as
// internal/gateway's upstreamClient. Its transport and its timeout stay the
// caller's.
func serviceMintClient(c *http.Client) *http.Client {
	var out http.Client
	if c != nil {
		out = *c
	} else {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DisableCompression = true
		out.Transport = t
		out.Timeout = serviceMintTimeout
	}
	out.Jar = nil
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// refusalCode is the wire error code of a refused answer, as ", refused: code",
// or "" when the answer carries none this contract could have written. Only
// the code is read: the message beside it is the service's text and is never
// relayed into a run's log or error.
func refusalCode(data []byte) string {
	var refusal struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &refusal) != nil || !serviceErrorCode.MatchString(refusal.Error) {
		return ""
	}
	return ", refused: " + refusal.Error
}

// printableValue reports whether v is one clean HTTP header value: non-empty
// printable ASCII with no space (internal/credsvc's own rule for a presented
// token, auth.go's maxTokenBytes and wire.go's printableHeaderValue).
func printableValue(v string) bool {
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x21 || v[i] > 0x7e {
			return false
		}
	}
	return true
}
