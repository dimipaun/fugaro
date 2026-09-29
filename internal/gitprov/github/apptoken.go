// Package github is the GitHub adapter: App JWT authentication and
// repo-scoped installation tokens (design §6.1, §6.2).
package github

import (
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
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

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
var tokenPermissions = map[string]string{
	"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read",
}

// buildTokenPermissions is what an image build's token asks for: it only
// clones the repository.
var buildTokenPermissions = map[string]string{"contents": "read", "metadata": "read"}

// BuildTokenPermissions are the permissions of an image build's token, a
// copy the caller may keep.
func BuildTokenPermissions() map[string]string { return maps.Clone(buildTokenPermissions) }

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
