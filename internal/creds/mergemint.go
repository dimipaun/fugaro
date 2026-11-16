package creds

// The merge mint's client (E16, D2049): the call the installation's orchestrator
// daemon makes when it needs the credential a merge takes, and the one piece of
// this package that speaks HTTP.
//
// It is here rather than in the package that consumes it because the plan puts
// it here: `internal/creds` is the library the daemon is built on — the marks,
// the leases, the slips and the selection — and the daemon holds one client for
// the credentials service. What it is NOT is a second reading of the service's
// contract: the path, the four request fields and the answer are the service's
// own (credsvc/mergemint.go), and the two spellings of the path are held to each
// other by TestMergeMintRouteNamesTheClientPath in the service's package, which
// can read this one and which this one cannot read back.
//
// The daemon's identity is the class the mint is gated on: this client presents
// the daemon's own Google ID token for the service's URL and NOTHING else — no
// run identity, no job — because the route it calls is answered ahead of the
// service's shared parse, which is the shape every caller that holds no run
// identity has. The token comes from IDToken, which is the daemon's own
// credential and not the operator's: a person's ambient token is refused by the
// service's audience check whatever list it was matched against
// (credsvc's VerifyOrchestrator).
//
// Nothing here puts a value in an error. The token the service mints is never
// formatted into one, and the service's own answer is read for its status and
// its error code alone: the sentence beside that code is written for the
// operator's log and not for this caller, so it is dropped rather than relayed.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MergeMintPath is the path the merge token is asked for on, spelled as the
// service spells it (credsvc.MergeMintPath).
const MergeMintPath = "/v1/github/merge-token"

// The client's own refusals. They are sentinels so a daemon can tell a refusal
// it must report from an outage it must retry.
var (
	// ErrMergeMint is the service refusing, or the call to it failing. The
	// service's own error code is carried beside it and its sentence is not.
	ErrMergeMint = errors.New("creds: the credentials service did not mint a merge token")
	// ErrMergeMintIdentity is a client that cannot present the daemon's own
	// identity, or a call this code will not make: a missing URL or App ID, no
	// ID token, a repository that is not one, a pull request number that is not
	// positive, or a head that is not a commit.
	ErrMergeMintIdentity = errors.New("creds: the orchestrator daemon has no identity to ask for a merge token with")
)

// MergeMintRequest is the mint's body: the one repository and pull request the
// token is asked for, the head that pull request must still be at, and the App
// ID the service signs its JWT as.
type MergeMintRequest struct {
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	HeadSHA    string `json:"head_sha"`
	AppID      string `json:"app_id"`
}

// mergeMintAnswer is the mint's answer as this client reads it: the mirror of
// the service's MergeMintResponse.
type mergeMintAnswer struct {
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Repository string    `json:"repository"`
}

// MergeToken is one gated merge token: the credential, when it expires, and the
// repository the service minted it for — the repository the caller named, which
// this client checks its own request against before it hands the token on.
type MergeToken struct {
	Token      string
	ExpiresAt  time.Time
	Repository string
}

// MergeMinter is what the daemon holds and what the provider's ready, merge and
// checks calls are given (E8). It is one method and the plan's own signature, so
// that the provider depends on the mint and not on the way it is reached.
//
// The head it takes is the head the merge must be made at, and the CALLER of
// the merge is what keeps the merge there: the service checks the head at mint
// time (G2) and the token it hands back is a repository-scoped installation
// token that outlives that check, so a merge call that does not name the head
// is a merge call this mint did not gate. The provider's merge call therefore
// passes `sha` equal to the headSHA it minted for, and treats a refusal of that
// head as the merge not happening — a second mint for a moved head is a second
// review, not a retry.
type MergeMinter interface {
	MintMerge(ctx context.Context, repo string, pr int, headSHA string) (MergeToken, error)
}

// ServiceMergeMint is the mint reached over HTTP at the installation's
// credentials service. Every field but HTTP and Now is required, and a missing
// one is a refusal (ErrMergeMintIdentity) rather than a call the service would
// answer with less.
type ServiceMergeMint struct {
	// URL is the credentials service's base URL, from the installation's own
	// configuration (the service's Cloud Run URL).
	URL string
	// AppID is the GitHub App's ID: a number, not a credential, and the one
	// thing a mint's body carries. The service's environment does not hold it,
	// so the caller names it — and cannot name anything else: the key that
	// signs, the repository's rules and the permissions are the service's.
	AppID string
	// HTTP is the client the call is made with; nil means this package's own
	// hardened one (mergeMintClient: no redirects, no jar, a bounded timeout).
	HTTP *http.Client
	// IDToken mints the daemon's own Google ID token for URL — the identity the
	// service's orchestrator class is. It is called on every mint and never
	// cached here: the token's life is the platform's business, and a cached one
	// would be a second place a credential lives.
	IDToken func(ctx context.Context) (string, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// ServiceMergeMint is a MergeMinter, which is what E8 consumes.
var _ MergeMinter = (*ServiceMergeMint)(nil)

func (m *ServiceMergeMint) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}

// headShape is the commit shape the call is held to, respelled from the
// service's own rule (credsvc's headRE): a head this client would not send is a
// head the service would refuse, and refusing it here is the same answer one
// round trip earlier.
var headShape = regexp.MustCompile(`^[0-9a-f]{40}$`)

// MintMerge asks the credentials service for a merge token for one pull request
// at one head, and returns it only if the service minted one.
//
// It is the plan's own interface (E16): `MintMerge(ctx, repo, pr, headSHA)`.
// What it does NOT do is decide anything about the merge: the gate — the head
// the pull request is actually at, the base's bypass list, the per-repository
// rate — is the service's, and every one of its refusals arrives here as
// ErrMergeMint with the service's own code beside it. A daemon that finds this
// call refused reports it; it does not merge with another credential, and there
// is no other credential for it to merge with.
//
// The head is checked HERE and at the service, and the merge is the third place
// it has to hold: the token this returns is scoped to the repository and not to
// the head, so the call that merges passes `sha` equal to headSHA (E8's
// provider). Minting and then merging without that `sha` is a merge the gate did
// not decide.
func (m *ServiceMergeMint) MintMerge(ctx context.Context, repo string, pr int, headSHA string) (MergeToken, error) {
	if m.URL == "" || m.AppID == "" || m.IDToken == nil {
		return MergeToken{}, fmt.Errorf("%w: this daemon has no credentials service to mint a merge token at", ErrMergeMintIdentity)
	}
	if repo == "" {
		return MergeToken{}, fmt.Errorf("%w: the call names no repository", ErrMergeMintIdentity)
	}
	if pr < 1 {
		return MergeToken{}, fmt.Errorf("%w: the call names no pull request number", ErrMergeMintIdentity)
	}
	if !headShape.MatchString(headSHA) {
		return MergeToken{}, fmt.Errorf("%w: the call names no head commit (40 hex digits)", ErrMergeMintIdentity)
	}
	// The URL is checked before anything is sent to it: a mint presents the
	// daemon's own ID token, which is a bearer credential, and over plain http
	// it would travel in cleartext. https, or http on a loopback address, is the
	// rule the run's own mint holds its service URL to.
	if err := checkMergeServiceURL(m.URL); err != nil {
		return MergeToken{}, err
	}
	id, err := m.IDToken(ctx)
	if err != nil {
		return MergeToken{}, fmt.Errorf("%w: the daemon's identity token could not be minted: %v", ErrMergeMintIdentity, err)
	}
	if !printableValue(id) {
		return MergeToken{}, fmt.Errorf("%w: the daemon's identity token is not one header value", ErrMergeMintIdentity)
	}
	body, err := json.Marshal(MergeMintRequest{Repository: repo, Number: pr, HeadSHA: headSHA, AppID: m.AppID})
	if err != nil {
		return MergeToken{}, fmt.Errorf("%w: the mint's request could not be built", ErrMergeMint)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(m.URL, "/")+MergeMintPath, bytes.NewReader(body))
	if err != nil {
		return MergeToken{}, fmt.Errorf("%w: the mint's request could not be built", ErrMergeMint)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+id)
	resp, err := mergeMintClient(m.HTTP).Do(req)
	if err != nil {
		return MergeToken{}, fmt.Errorf("%w: the credentials service could not be reached", ErrMergeMint)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMergeAnswerBytes+1))
	if err != nil {
		return MergeToken{}, fmt.Errorf("%w: the service's answer could not be read", ErrMergeMint)
	}
	if len(data) > maxMergeAnswerBytes {
		return MergeToken{}, fmt.Errorf("%w: the service's answer is larger than a mint's", ErrMergeMint)
	}
	if resp.StatusCode != http.StatusOK {
		return MergeToken{}, fmt.Errorf("%w: the service answered %d%s", ErrMergeMint, resp.StatusCode, refusalCode(data))
	}
	var ans mergeMintAnswer
	if err := json.Unmarshal(data, &ans); err != nil {
		return MergeToken{}, fmt.Errorf("%w: the service's answer is not a mint's", ErrMergeMint)
	}
	if ans.Token == "" {
		// An empty token is no token: it would fail the merge as an
		// authentication error, and it must never be used as if it were one.
		return MergeToken{}, fmt.Errorf("%w: the service's answer carries no token", ErrMergeMint)
	}
	if ans.Repository != repo {
		// The answer names the repository the caller asked for (the service's
		// own contract): one that names another is a token this daemon has no
		// business using, and using it would merge the wrong repository.
		return MergeToken{}, fmt.Errorf("%w: the service's answer is for another repository", ErrMergeMint)
	}
	if !ans.ExpiresAt.After(m.now()) {
		// The token was minted and audited at the service and reaches nobody:
		// an expired credential is not one to merge with, and it is never
		// printed, so the caller gets the refusal instead.
		return MergeToken{}, fmt.Errorf("%w: the token the service minted has expired", ErrMergeMint)
	}
	return MergeToken{Token: ans.Token, ExpiresAt: ans.ExpiresAt, Repository: ans.Repository}, nil
}

// maxMergeAnswerBytes bounds the answer this client reads, respelled from the
// run's own mint (github's maxServiceAnswerBytes): the whole answer is a token,
// a time and a repository name.
const maxMergeAnswerBytes = 8 << 10

// checkMergeServiceURL refuses a service URL this code will not send the
// daemon's identity to: https, or http on a loopback address. It is
// internal/gitprov/github's checkServiceURL for the run's mint, respelled here
// because that package does not export it and this one cannot import the
// service's (credsvc imports this package).
//
// The refusal carries the URL's shape and never the URL.
func checkMergeServiceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: the credentials service URL must be a scheme and a host only", ErrMergeMint)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("%w: the credentials service URL must be https, or http on a loopback address", ErrMergeMint)
}

// mergeMintClient is the client a mint is made with: it never follows a
// redirect, never keeps a cookie jar, and — when this code builds the transport
// rather than being handed one — never waits on an answer forever. A redirect
// is the one thing a credential-bearing call must not follow, because the
// destination would be chosen by the answer rather than by the configuration.
func mergeMintClient(c *http.Client) *http.Client {
	if c == nil {
		c = &http.Client{}
	}
	out := *c
	out.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	out.Jar = nil
	if out.Timeout == 0 {
		out.Timeout = 30 * time.Second
	}
	return &out
}

// printableValue reports whether s is one clean header value: non-empty
// printable ASCII with no space, which is the rule the run's own mint applies
// (internal/gitprov/github/apptoken.go) and internal/credsvc's own for a
// presented token. It is deliberately the same rule and not a looser one: a
// check that claims parity without having it drifts further from the thing it
// is copied from with every edit, and the value it guards is a credential.
func printableValue(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// refusalCode renders the service's own error code for a refusal's error, and
// nothing else of the body: the code is the service's closed vocabulary and the
// sentence beside it is written for an operator's log.
func refusalCode(data []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &e); err != nil || e.Error == "" {
		return ""
	}
	return ": " + e.Error
}
