package github

// The GitHub read mint's client half: a PERSON asks the credentials service for
// a read-only GitHub installation token, and the provider the coordinator opens
// on that repository reads with it.
//
// It is the same shape as the run's service mint (apptoken.go's ServiceTokens)
// with the differences that are the whole of a person's read:
//
//   - the caller is a PERSON, so the token it presents is their own Google ID
//     token and there is no run identity header at all. The service verifies
//     that token's email against its own list of the installation's people and
//     mints for the person; there is no second identity to send and none is
//     sent.
//   - the body names ONE REPOSITORY, because the token is scoped to it: a
//     person reads one repository at a time, and the service resolves the
//     repository through the installation's own inventory and refuses one it
//     does not hold. The answer echoes the repository back, which is what this
//     client checks before it uses the token.
//   - the permissions are the service's own (its github read mint asks GitHub
//     for the coordinator's read-only set and adds none). This client asks for
//     no permission, because there is no field to ask in.
//
// The App ID travels in the body, and it is the one field of this contract that
// is neither the repository nor an identity. It is not a secret -- the platform
// passes the same number to a job as FUGARO_GITHUB_APP_ID -- and it is the one
// thing a caller must supply to a GitHub App mint (apptoken.go's own doc for
// ServiceTokens says the same of the run's mint). The service's environment
// does not hold it, so a person's read carries the project's own recorded value
// (localcfg.Repo.GitHubAppID) and nothing reads an environment variable to find
// it.
//
// The API root is not this file's business and must never become so: the mint
// is signed at the service, and the token this returns is the installation's.
// The only thing standing between it and an API root somebody overrode is that
// this code sends nothing to GitHub at all.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// ServiceReadPath is where the credentials service mints a read-only GitHub
// installation token for a person. It is spelled here rather than imported
// because this package cannot import internal/credsvc -- credsvc imports this
// one -- and internal/credsvc's own route list holds every one of these to the
// service's spelling, so the two cannot drift.
const ServiceReadPath = "/v1/github/read-token"

// serviceReadRequest is a read mint's body and serviceReadAnswer is the
// service's answer: the one repository the token is asked for and the App ID
// the mint is signed as, and the token, its life and the repository it was
// minted for.
type serviceReadRequest struct {
	Repository string `json:"repository"`
	AppID      string `json:"app_id"`
}

type serviceReadAnswer struct {
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Repository string    `json:"repository"`
}

// The read mint's own refusals, in the shape the run's mint already has: one
// for the service refusing or failing, and one for this caller's own inability
// to present what a read mint is verified by.
var (
	// ErrServiceRead is the credentials service refusing a read mint, or the
	// call to it failing. A read halts on it: the person's machine holds no
	// GitHub App key of its own to fall back to.
	ErrServiceRead = errors.New("github: the credentials service did not mint a read token")
	// ErrServiceReadIdentity is this caller's inability to present what a read
	// mint is verified by -- the service's URL, the repository's App ID and a
	// Google ID token for it. It is a fact about how the command was run (no
	// installation is configured for it, or its identity is not available) and
	// never about the service.
	ErrServiceReadIdentity = errors.New("github: a read mint needs the credentials service's URL, the App ID and the caller's own identity")
)

// ServiceReadTokens is where a person's GitHub token comes from when the
// installation's credentials service holds the App key: the service mints it,
// and the machine that reads holds no App key at all.
//
// It is a gitprov.TokenSource, and deliberately shaped like the run's service
// mint: the same cache and the same rule about minValid, because a mint is an
// act the service AUDITS and a token thrown away per call would be a line per
// API call. An installation token lives an hour and the provider asks for a
// build's worth of validity, so the cache is what keeps one mint per read
// rather than one per request.
//
// URL, Repository, AppID and IDToken are required, and a missing one is a
// refusal (ErrServiceReadIdentity) rather than a call the service would answer
// with less.
type ServiceReadTokens struct {
	// URL is the credentials service's base URL, from the installation's
	// published credentials_service block.
	URL string
	// Repository is the repository the token is asked for, in the
	// installation's own spelling ("owner/name"). The service resolves it
	// through its inventory rather than trusting this string, and the answer
	// names it back for this client to check.
	Repository string
	// AppID is the App the mint is signed as, from the project's own recorded
	// value (localcfg.Repo.GitHubAppID). It is not a secret.
	AppID string
	// IDToken mints the caller's own Google ID token for URL, which is what the
	// service verifies a person by. It is called on every mint and never cached
	// here: a Google ID token lives an hour, and a read is one call.
	IDToken func(ctx context.Context) (string, error)
	// HTTP is the client the call is made with; nil means this package's own
	// hardened one (serviceMintClient: no redirects, no jar, no compression, a
	// bounded timeout).
	HTTP *http.Client
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// ServiceReadTokens is the TokenSource a provider installs (gitprov.TokenSource)
// when the installation's service mints a person's read.
var _ gitprov.TokenSource = (*ServiceReadTokens)(nil)

func (s *ServiceReadTokens) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Repo is this client for one repository: the same service, the same identity,
// the same App and a fresh cache, because a token minted for one repository
// must never be handed to a read of another. The service mints for ONE
// repository per call and the answer names it, so the fields this sets are what
// the call carries and what the answer is checked against.
//
// It is a constructor rather than fields a caller sets, so a caller cannot
// leave the repository or the App ID empty and get a mint whose answer could
// not be checked.
func (s *ServiceReadTokens) Repo(repo, appID string) *ServiceReadTokens {
	return &ServiceReadTokens{
		URL:        s.URL,
		Repository: repo,
		AppID:      appID,
		IDToken:    s.IDToken,
		HTTP:       s.HTTP,
		Now:        s.Now,
	}
}

// Token returns a read token valid for at least minValid, minting one at the
// service when the cached token would expire sooner.
//
// The whole check-then-mint runs under the lock, as ServiceTokens does: the API
// calls of one read reach Token at once, and a mint the service AUDITS must not
// happen once per caller.
func (s *ServiceReadTokens) Token(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
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

// mint makes one read mint call and returns its token.
//
// Nothing here can put a value in an error: the identity and the token are
// never formatted into one, and the service's answer is read for the status and
// the error code alone. The message beside that code is the service's text --
// written for an operator's log and not for a command's output, and an answer's
// body is another writer's to fill -- so it is dropped rather than relayed,
// exactly as ServiceTokens drops it.
func (s *ServiceReadTokens) mint(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	if s.URL == "" || s.Repository == "" || s.AppID == "" || s.IDToken == nil {
		return "", time.Time{}, fmt.Errorf("%w: this command has no credentials service to mint at", ErrServiceReadIdentity)
	}
	// The URL is checked before anything is sent to it: a read mint presents
	// the caller's own Google ID token, which is a bearer credential, and over
	// plain http it would travel in cleartext.
	if err := checkServiceURL(s.URL); err != nil {
		return "", time.Time{}, err
	}
	id, err := s.IDToken(ctx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the caller's identity token could not be minted: %v", ErrServiceReadIdentity, err)
	}
	if !printableValue(id) {
		return "", time.Time{}, fmt.Errorf("%w: the caller's identity token is not one header value", ErrServiceReadIdentity)
	}
	body, err := json.Marshal(serviceReadRequest{Repository: s.Repository, AppID: s.AppID})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the read mint's request could not be built", ErrServiceRead)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(s.URL, "/")+ServiceReadPath, bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the read mint's request could not be built", ErrServiceRead)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+id)
	resp, err := serviceMintClient(s.HTTP).Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the credentials service could not be reached", ErrServiceRead)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxServiceAnswerBytes+1))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer could not be read", ErrServiceRead)
	}
	if len(data) > maxServiceAnswerBytes {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer is larger than a read mint's", ErrServiceRead)
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("%w: the service answered %d%s", ErrServiceRead, resp.StatusCode, refusalCode(data))
	}
	var ans serviceReadAnswer
	if err := json.Unmarshal(data, &ans); err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer is not a read mint's", ErrServiceRead)
	}
	if ans.Token == "" {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer carries no token", ErrServiceRead)
	}
	// The answer names the repository it was minted for, and this is what makes
	// that field load-bearing: a token minted for another repository is one this
	// read must not use, and the only way a caller can know it is the one it
	// asked for is that the service said so and this checked it.
	if ans.Repository != s.Repository {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer names another repository than the one this read asked for", ErrServiceRead)
	}
	if ans.ExpiresAt.Sub(s.now()) < minValid {
		return "", time.Time{}, fmt.Errorf("%w: the token the service minted expires too soon to satisfy the requested %s validity", ErrServiceRead, minValid)
	}
	return ans.Token, ans.ExpiresAt, nil
}
