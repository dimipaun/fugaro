package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// IdentityPlatformURL is Identity Toolkit's API root.
const IdentityPlatformURL = "https://identitytoolkit.googleapis.com"

// IdentityPlatform ensures Identity Platform is initialized in the Firebase
// project, over REST with the person's credentials. Terraform can't: its
// google_identity_platform_config does not adopt a project where Identity
// Platform is already initialized (400 INVALID_PROJECT_ID, found live).
type IdentityPlatform struct {
	// Endpoint is the API root (IdentityPlatformURL when empty).
	Endpoint string
	Project  string
	// HTTP carries the credentials.
	HTTP *http.Client
}

// IdentityPlatformStep is the line init lists among the writes it asks to
// confirm.
const IdentityPlatformStep = "ensure Identity Platform is initialized (no sign-in providers)"

type idpError struct {
	method, path string
	code         int
	body         string
}

func (e *idpError) Error() string {
	return fmt.Sprintf("Identity Toolkit %s %s: HTTP %d: %s", e.method, e.path, e.code, e.body)
}

func (p *IdentityPlatform) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	if p.Project == "" || strings.ContainsAny(p.Project, "/?#% ") {
		return nil, fmt.Errorf("%q is not a Firebase project id", p.Project)
	}
	root := p.Endpoint
	if root == "" {
		root = IdentityPlatformURL
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(root, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-user-project", p.Project)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Identity Toolkit %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("Identity Toolkit %s %s: %w", method, path, err)
	}
	if resp.StatusCode/100 != 2 {
		msg := string(b)
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return nil, &idpError{method, path, resp.StatusCode, msg}
	}
	return b, nil
}

// Config reads the project's Identity Platform configuration: exists is false
// when it is not initialized (CONFIGURATION_NOT_FOUND). The raw JSON is
// returned for PublicSignUp.
func (p *IdentityPlatform) Config(ctx context.Context) (raw []byte, exists bool, err error) {
	b, err := p.do(ctx, http.MethodGet, "/admin/v2/projects/"+url.PathEscape(p.Project)+"/config", nil)
	var ie *idpError
	switch {
	case errors.As(err, &ie) && (ie.code == http.StatusBadRequest || ie.code == http.StatusNotFound) && strings.Contains(ie.body, "CONFIGURATION_NOT_FOUND"):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return b, true, nil
}

// Initialize initializes Identity Platform with no sign-in provider. A
// project where it already is (a race, or a hand-made initialization) counts
// as done.
func (p *IdentityPlatform) Initialize(ctx context.Context) error {
	_, err := p.do(ctx, http.MethodPost, "/v2/projects/"+url.PathEscape(p.Project)+"/identityPlatform:initializeAuth", []byte("{}"))
	var ie *idpError
	if errors.As(err, &ie) && ie.code == http.StatusBadRequest && strings.Contains(ie.body, "already been enabled") {
		return nil
	}
	return err
}

// PublicSignUp lists the public sign-up paths a configuration enables. The web API
// key is public, so only custom tokens signed by our signer may work.
func PublicSignUp(raw []byte) ([]string, error) {
	var c struct {
		SignIn struct {
			Email       struct{ Enabled bool } `json:"email"`
			Anonymous   struct{ Enabled bool } `json:"anonymous"`
			PhoneNumber struct{ Enabled bool } `json:"phoneNumber"`
		} `json:"signIn"`
		Default []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"defaultSupportedIdpConfigs"`
		OAuth []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"oauthIdpConfigs"`
		Client struct {
			Permissions struct {
				DisabledUserSignup bool `json:"disabledUserSignup"`
			} `json:"permissions"`
		} `json:"client"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("Identity Platform configuration: %w", err)
	}
	var out []string
	if c.SignIn.Email.Enabled {
		out = append(out, "signIn.email.enabled")
	}
	if c.SignIn.Anonymous.Enabled {
		out = append(out, "signIn.anonymous.enabled")
	}
	if c.SignIn.PhoneNumber.Enabled {
		out = append(out, "signIn.phoneNumber.enabled")
	}
	for _, l := range []struct {
		field string
		cfgs  []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		}
	}{{"defaultSupportedIdpConfigs", c.Default}, {"oauthIdpConfigs", c.OAuth}} {
		for _, i := range l.cfgs {
			if i.Enabled {
				out = append(out, l.field+" "+i.Name+" enabled")
			}
		}
	}
	if len(out) > 0 && !c.Client.Permissions.DisabledUserSignup {
		out = append(out, "client.permissions.disabledUserSignup is false")
	}
	return out, nil
}
