package bitbucket

// The read mint's client half (BB4): a person asks the credentials service for
// a read-scoped Bitbucket token, and the provider the coordinator opens on that
// repository reads with it.
//
// It is the same shape as the run's service mint (internal/gitprov/github's
// ServiceTokens) with two differences that are the whole of BB4's client side:
//
//   - the caller is a PERSON, so the token it presents is their own Google ID
//     token and there is no run identity header at all. The service verifies
//     that token's email against its own list and mints for the person; there
//     is no second identity to send and none is sent.
//   - the body names ONE REPOSITORY, because a Bitbucket access token is not
//     scoped to a repository (U3). The service holds the name to its own
//     inventory and to the read consumer's workspace, and the answer echoes the
//     repository back, which is what this client checks before it uses the
//     token: a token minted for another repository is a token this read must
//     not make.
//
// The API root is not this file's business and must never become so. The token
// this returns is the installing person's whole workspace's, and the only thing
// standing between it and an API root somebody overrode is that the provider is
// opened on DefaultBaseURL and that nothing here reads an environment value
// (G10, T2.5).

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
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// ServiceReadPath is where the credentials service mints a read-scoped
// Bitbucket token for a person. It is spelled here rather than imported because
// this package cannot import internal/credsvc -- credsvc imports this one, for
// MintAccessToken -- and internal/credsvc's TestNewRoutesAuditConsumerKind holds
// every one of these to the service's own spelling, so the two cannot drift.
const ServiceReadPath = "/v1/bitbucket/read-token"

// maxServiceReadAnswerBytes bounds a read mint's answer. It is one small object
// -- a token, an expiry and the repository's name -- so a longer answer is not
// one this contract wrote.
const maxServiceReadAnswerBytes = 16 << 10

// serviceReadTimeout bounds one read mint call. A mint is a short call: one
// request, one answer, no streaming. It is internal/gitprov/github's own
// serviceMintTimeout, respelled here for the reason this package respells
// several of that one's helpers -- the two are separate packages and neither
// imports the other.
const serviceReadTimeout = 30 * time.Second

// serviceReadRequest is a read mint's body and serviceReadAnswer is the
// service's answer (credsvc.BitbucketReadRequest and BitbucketReadResponse):
// the one repository the token is asked for, and the token, its life and the
// repository it was minted for.
type serviceReadRequest struct {
	Repository string `json:"repository"`
}

type serviceReadAnswer struct {
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Repository string    `json:"repository"`
}

// serviceReadErrorCode is the whole shape of an error code a refusal may carry
// (credsvc's wire contract): a bounded word like "state_unavailable". An answer's
// error field that is anything else is text this contract did not write, so it
// is dropped with the rest of the body.
var serviceReadErrorCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// printableValue reports whether v is one clean HTTP header value: non-empty
// printable ASCII with no space (internal/credsvc's own rule for a presented
// token).
func printableValue(v string) bool {
	for i := 0; i < len(v); i++ {
		if v[i] < 0x21 || v[i] > 0x7e {
			return false
		}
	}
	return v != ""
}

// isLoopback reports whether host is a loopback IP literal. It is
// internal/gitprov/github's own check, respelled here because that package does
// not export it.
func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// The refusals a read mint can end in. Every one is a sentence written here,
// and none carries a token, an identity or the service's own text: the value
// this endpoint exists to hand over reaches the provider and nothing else -- no
// log line, no error, and no other field.
var (
	// ErrServiceRead is the credentials service refusing a read mint, or the
	// call to it failing. A read halts on it: the person's machine holds no
	// Bitbucket credential of its own to fall back to.
	ErrServiceRead = errors.New("bitbucket: the credentials service did not mint a read token")
	// ErrServiceReadIdentity is this caller's inability to present what a read
	// mint is verified by -- the service's URL and a Google ID token for it. It
	// is a fact about how the command was run (no installation is configured
	// for it, or its identity is not available) and never about the service.
	ErrServiceReadIdentity = errors.New("bitbucket: a read mint needs the credentials service's URL and the caller's own identity")
)

// ServiceReadTokens is where a person's Bitbucket token comes from when the
// installation's credentials service holds the read consumer (BB4, F21): the
// service mints it, and the machine that reads holds no consumer at all.
//
// It is a gitprov.TokenSource, and deliberately shaped like the run's service
// mint: the same cache and the same rule about minValid, because a mint is an
// act the service AUDITS and a token thrown away per call would be a line per
// API call. A Bitbucket access token lives one to two hours (U1) and the
// provider asks for two minutes' validity, so the cache is what keeps one mint
// per read rather than one per request.
//
// URL, Repository and IDToken are required, and a missing one is a refusal
// (ErrServiceReadIdentity) rather than a call the service would answer with
// less.
type ServiceReadTokens struct {
	// URL is the credentials service's base URL, and it comes from the
	// installation's published credentials_service block and from nothing else.
	// There is deliberately no environment variable behind it (the owner's
	// ruling of 2026-11-03): the URL decides where the caller's own Google ID
	// token is presented, so a value a person's shell carries may not supply one
	// their project never published.
	URL string
	// Repository is the repository the token is asked for, in the spelling the
	// installation uses ("workspace/name"). It is the one field of the request,
	// and the service resolves its workspace from its own inventory rather than
	// from this string.
	Repository string
	// IDToken mints the caller's own Google ID token for URL, which is what the
	// service verifies a person by. It is called on every mint and never cached
	// here: a Google ID token lives an hour, and a read is one call.
	IDToken func(ctx context.Context) (string, error)
	// HTTP is the client the call is made with; nil means this package's own
	// hardened one (serviceReadClient: no redirects, no jar, no compression, a
	// bounded timeout).
	HTTP *http.Client
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// ServiceReadTokens is the TokenSource a provider installs (gitprov.TokenSource)
// when the installation's service holds the read consumer, and it is a
// gitprov.TokenHolder so that the reader above can redact the token out of a
// host's echo of it: the token never passes through the reader's hands, so the
// holder is the only thing that knows it.
var (
	_ gitprov.TokenSource = (*ServiceReadTokens)(nil)
	_ gitprov.TokenHolder = (*ServiceReadTokens)(nil)
)

// HeldToken is the token this source last minted, or "" before the first mint.
// It is read under the same lock Token mints under, because the provider's
// calls and the reader's failure path are different goroutines.
func (s *ServiceReadTokens) HeldToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

func (s *ServiceReadTokens) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Repo is this client for one repository: the same service, the same identity,
// the same client, and a fresh cache, because a token minted for one
// repository must never be handed to a read of another (G11). The service
// mints for ONE repository per call and the answer names it, so the field this
// sets is what the call carries and what the answer is checked against.
//
// It is a constructor rather than a field a caller sets, so a caller cannot
// leave the repository empty and get a mint whose answer could not be checked.
func (s *ServiceReadTokens) Repo(repo string) *ServiceReadTokens {
	return &ServiceReadTokens{
		URL:        s.URL,
		Repository: repo,
		IDToken:    s.IDToken,
		HTTP:       s.HTTP,
		Now:        s.Now,
	}
}

// Token returns a read token valid for at least minValid, minting one at the
// service when the cached token would expire sooner.
//
// The whole check-then-mint runs under the lock, as github's ServiceTokens does:
// the API calls of one read reach Token at once, and a mint the service AUDITS
// must not happen once per caller.
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
// exactly as github's ServiceTokens drops it.
func (s *ServiceReadTokens) mint(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	if s.URL == "" || s.Repository == "" || s.IDToken == nil {
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
	if id == "" || !printableValue(id) {
		return "", time.Time{}, fmt.Errorf("%w: the caller's identity token is not one header value", ErrServiceReadIdentity)
	}
	body, err := json.Marshal(serviceReadRequest{Repository: s.Repository})
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
	resp, err := serviceReadClient(s.HTTP).Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the credentials service could not be reached", ErrServiceRead)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxServiceReadAnswerBytes+1))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer could not be read", ErrServiceRead)
	}
	if len(data) > maxServiceReadAnswerBytes {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer is larger than a read mint's", ErrServiceRead)
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("%w: the service answered %d%s", ErrServiceRead, resp.StatusCode, serviceReadRefusalCode(data))
	}
	var ans serviceReadAnswer
	if err := json.Unmarshal(data, &ans); err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer is not a read mint's", ErrServiceRead)
	}
	if ans.Token == "" {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer carries no token", ErrServiceRead)
	}
	// The answer names the repository it was minted for, and this is what makes
	// that field load-bearing: a token minted for another repository reaches the
	// whole workspace anyway (U3), so the only way a caller can know it is the
	// one it asked for is that the service said so and this checked it.
	if ans.Repository != s.Repository {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer names another repository than the one this read asked for", ErrServiceRead)
	}
	if ans.ExpiresAt.Sub(s.now()) < minValid {
		return "", time.Time{}, fmt.Errorf("%w: the token the service minted expires too soon to satisfy the requested %s validity", ErrServiceRead, minValid)
	}
	return ans.Token, ans.ExpiresAt, nil
}

// checkServiceURL refuses a credentials service URL this code will not send a
// read mint to: https, or http on a loopback address. It is github's
// checkServiceURL rule for the run's mint, respelled here because that package
// does not export it and this one cannot import it.
//
// The refusal carries the URL's shape and never the URL.
func checkServiceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: the credentials service URL must be a scheme and a host only", ErrServiceRead)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && (u.Hostname() == "localhost" || isLoopback(u.Hostname())) {
		return nil
	}
	return fmt.Errorf("%w: the credentials service URL must be https, or http on a loopback address", ErrServiceRead)
}

// serviceReadClient is the client a read mint is made with: it never follows a
// redirect, never keeps a cookie jar, and -- when this code builds the transport
// rather than being handed one -- never asks for compression on its own and
// never waits on an answer forever.
//
// The redirect half is the one that matters. The request carries the caller's
// own Google ID token in Authorization, and a followed 307 or 308 would replay
// it to whatever host the Location named. A client handed in is copied, not used
// as it is, so its redirect policy and jar are this function's whatever the
// caller set.
func serviceReadClient(c *http.Client) *http.Client {
	var out http.Client
	if c != nil {
		out = *c
	} else {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DisableCompression = true
		out.Transport = t
		out.Timeout = serviceReadTimeout
	}
	out.Jar = nil
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// serviceReadRefusalCode is the wire error code of a refused answer, as
// ", refused: code", or "" when the answer carries none this contract could have
// written. Only the code is read: the message beside it is the service's text
// and is never relayed into a command's output.
func serviceReadRefusalCode(data []byte) string {
	var refusal struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &refusal) != nil || !serviceReadErrorCode.MatchString(refusal.Error) {
		return ""
	}
	return ", refused: " + refusal.Error
}
