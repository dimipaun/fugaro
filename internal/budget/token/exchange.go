package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Config is how a run talks to Firebase Auth.
type Config struct {
	// APIKey is the Firebase web API key (restricted to Identity Toolkit and
	// Secure Token; not secret, but redacted like one).
	APIKey string
	// IdentityURL and SecureTokenURL are the base URLs; empty means Google's.
	IdentityURL, SecureTokenURL string
	// HTTP is the client; redirects are never followed.
	HTTP *http.Client
	// Now is the clock (default time.Now).
	Now func() time.Time
	// Register receives every secret value (the custom token, the API key,
	// each ID token and refresh token) before it is used for the first time
	// and again whenever a new one appears. The runner's redactor is behind
	// it. Nothing is logged without it, but a nil Register is an error: a
	// secret that nobody redacts must not be created.
	Register func(secret string)
	// OnResult, when set, is told the outcome of every exchange and refresh
	// attempt (nil on success), for the run's D14 grace clock.
	OnResult func(op string, err error)
	// RefreshBefore is how long before expiry Keep refreshes (default 5 m,
	// at most half the token's lifetime).
	RefreshBefore time.Duration
	// RetryEvery is Keep's wait after a failed refresh (default 10 s).
	RetryEvery time.Duration
	// MinRefreshInterval is the least Keep waits between refreshes (default
	// 30 s), so a tiny expires_in cannot make it hammer Secure Token.
	MinRefreshInterval time.Duration
}

func (c *Config) defaults() {
	if c.IdentityURL == "" {
		c.IdentityURL = "https://identitytoolkit.googleapis.com"
	}
	if c.SecureTokenURL == "" {
		c.SecureTokenURL = "https://securetoken.googleapis.com"
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.RefreshBefore == 0 {
		c.RefreshBefore = 5 * time.Minute
	}
	if c.RetryEvery == 0 {
		c.RetryEvery = 10 * time.Second
	}
	if c.MinRefreshInterval == 0 {
		c.MinRefreshInterval = 30 * time.Second
	}
	c.HTTP = newHTTPClient(c.HTTP)
}

// Session is a run's live Firebase identity. It is safe for concurrent use.
// Its String and GoString never show a token.
type Session struct {
	cfg  Config
	uid  string
	want map[string]any // payload of the custom token: uid and claims to hold

	refreshMu sync.Mutex // one refresh at a time
	mu        sync.Mutex
	idToken   string
	refresh   string
	expiry    time.Time
	lifetime  time.Duration
}

// Exchange trades the custom token for an ID token and a refresh token
// (accounts:signInWithCustomToken). It makes one attempt; an unreachable
// backend is ErrUnavailable, a token minted more than an hour ago is
// ErrTokenExpired (no request is made), a token or key Google refuses is
// ErrTokenInvalid. The answer must carry the custom token's uid and claims
// or it is ErrTokenMismatch.
func Exchange(ctx context.Context, cfg Config, custom string) (*Session, error) {
	cfg.defaults()
	if cfg.Register == nil {
		return nil, errors.New("budget token: no redactor registered for the Firebase tokens")
	}
	if cfg.APIKey == "" {
		return nil, errors.New("budget token: no Firebase API key (FUGARO_FIREBASE_API_KEY)")
	}
	// Redaction first: before any code path can print or send them.
	cfg.Register(custom)
	cfg.Register(cfg.APIKey)
	if err := checkURL("the Identity Toolkit URL", cfg.IdentityURL); err != nil {
		return nil, err
	}
	if err := checkURL("the Secure Token URL", cfg.SecureTokenURL); err != nil {
		return nil, err
	}
	want, err := parseJWT(custom)
	if err != nil {
		return nil, report(cfg, "exchange", err)
	}
	if exp, ok := claimInt(want, "exp"); !ok || exp <= cfg.Now().Unix() {
		return nil, report(cfg, "exchange", fmt.Errorf("%w: minted more than an hour ago; launch the run again", ErrTokenExpired))
	}
	body, _ := json.Marshal(map[string]any{"token": custom, "returnSecureToken": true})
	status, resp, err := httpDo(ctx, cfg.HTTP, http.MethodPost, keyURL(cfg.IdentityURL, "/v1/accounts:signInWithCustomToken", cfg.APIKey), "application/json", body, nil)
	if err != nil {
		return nil, report(cfg, "exchange", err)
	}
	if status/100 != 2 {
		sentinel := ErrTokenInvalid
		if expiredAnswer(resp) {
			sentinel = ErrTokenExpired
		}
		return nil, report(cfg, "exchange", statusError("signInWithCustomToken", status, resp, sentinel))
	}
	var out struct {
		IDToken      string `json:"idToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    string `json:"expiresIn"`
	}
	if json.Unmarshal(resp, &out) != nil || out.IDToken == "" || out.RefreshToken == "" {
		return nil, report(cfg, "exchange", errors.New("budget token: signInWithCustomToken answered without tokens"))
	}
	cfg.Register(out.IDToken)
	cfg.Register(out.RefreshToken)
	s := &Session{cfg: cfg, uid: claimString(want, "uid"), want: want}
	if err := s.adopt(out.IDToken, out.RefreshToken, out.ExpiresIn); err != nil {
		return nil, report(cfg, "exchange", err)
	}
	report(cfg, "exchange", nil)
	return s, nil
}

// report tells OnResult the outcome and returns err. A cancelled or timed-out
// caller context is not a backend failure and is not reported.
func report(cfg Config, op string, err error) error {
	if cfg.OnResult != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		cfg.OnResult(op, err)
	}
	return err
}

// adopt validates an answer and installs it. Nothing is installed on error.
func (s *Session) adopt(idToken, refreshToken, expiresIn string) error {
	secs, err := strconv.Atoi(expiresIn)
	if err != nil || secs < 1 || secs > 24*3600 {
		return errors.New("budget token: the ID token has no usable lifetime")
	}
	got, err := parseJWT(idToken)
	if err != nil {
		return errors.New("budget token: the ID token is not a JWT")
	}
	if !sameIdentity(got, s.want) {
		return ErrTokenMismatch
	}
	life := time.Duration(secs) * time.Second
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idToken, s.refresh, s.lifetime = idToken, refreshToken, life
	s.expiry = s.cfg.Now().Add(life)
	return nil
}

// Token is the current ID token, for rtdb.Auth.IDToken. After a failed
// refresh it is the last good one (an expired token makes the database answer
// 401, which the caller classifies); it never blocks on the network.
func (s *Session) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idToken
}

// Expiry is when the current ID token stops being accepted.
func (s *Session) Expiry() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expiry
}

// UID is the run's Firebase uid.
func (s *Session) UID() string { return s.uid }

// String and GoString keep tokens out of %v and %#v.
func (s *Session) String() string   { return "token.Session{uid: " + s.uid + ", tokens redacted}" }
func (s *Session) GoString() string { return s.String() }

// Refresh trades the refresh token for a new ID token (Secure Token). The
// uid and claims must be unchanged or the answer is refused (ErrTokenMismatch)
// and the old tokens kept. A permanent refusal (user deleted or disabled, the
// refresh token invalid) is ErrRevoked.
func (s *Session) Refresh(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.mu.Lock()
	rt := s.refresh
	s.mu.Unlock()
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}
	status, resp, err := httpDo(ctx, s.cfg.HTTP, http.MethodPost, keyURL(s.cfg.SecureTokenURL, "/v1/token", s.cfg.APIKey),
		"application/x-www-form-urlencoded", []byte(form.Encode()), nil)
	if err != nil {
		return report(s.cfg, "refresh", err)
	}
	if status/100 != 2 {
		return report(s.cfg, "refresh", statusError("token refresh", status, resp, ErrRevoked))
	}
	var out struct {
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    string `json:"expires_in"`
	}
	if json.Unmarshal(resp, &out) != nil || out.IDToken == "" || out.RefreshToken == "" {
		return report(s.cfg, "refresh", errors.New("budget token: the token refresh answered without tokens"))
	}
	s.cfg.Register(out.IDToken)
	s.cfg.Register(out.RefreshToken)
	return report(s.cfg, "refresh", s.adopt(out.IDToken, out.RefreshToken, out.ExpiresIn))
}

// Keep refreshes the ID token before it expires until ctx ends or the
// identity is revoked (then it returns the error). A failed refresh is
// retried every RetryEvery; the outcome of each is reported to OnResult.
func (s *Session) Keep(ctx context.Context) error {
	for {
		s.mu.Lock()
		before := s.cfg.RefreshBefore
		if half := s.lifetime / 2; before > half {
			before = half
		}
		wait := max(s.expiry.Add(-before).Sub(s.cfg.Now()), s.cfg.MinRefreshInterval)
		s.mu.Unlock()
		for {
			if err := sleep(ctx, max(wait, 0)); err != nil {
				return err
			}
			err := s.Refresh(ctx)
			if err == nil {
				break
			}
			if errors.Is(err, ErrRevoked) || errors.Is(err, ErrTokenMismatch) {
				return err
			}
			wait = max(s.cfg.RetryEvery, 0)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
