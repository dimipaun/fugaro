package providers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
)

var ctx = context.Background()

func keyPEM(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

func TestBitbucketFromEnv(t *testing.T) {
	p, secrets, err := FromEnv([]string{EnvBitbucketToken + "=bb-token-1234"}, nil, nil)(ctx, gitprov.KindBitbucket, "acme/web")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*bitbucket.Provider); !ok || !slices.Equal(secrets, []string{"bb-token-1234"}) {
		t.Fatalf("provider %T, secrets %q", p, secrets)
	}
	a, err := p.GitAuth(ctx, 0)
	if err != nil || a.Token != "bb-token-1234" {
		t.Fatalf("auth = %+v, %v", a, err)
	}
}

func TestGitHubFromEnvKeyOrFile(t *testing.T) {
	pemData := keyPEM(t)
	file := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(file, []byte(pemData), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][]string{
		"inline": {EnvGitHubAppID + "=1234", EnvGitHubAppKey + "=" + pemData},
		"file":   {EnvGitHubAppID + "=1234", EnvGitHubAppKeyFile + "=" + file},
	} {
		p, secrets, err := FromEnv(env, nil, nil)(ctx, gitprov.KindGitHub, "acme/web")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := p.(*github.Provider); !ok || !slices.Equal(secrets, []string{pemData}) {
			t.Fatalf("%s: provider %T, %d secrets", name, p, len(secrets))
		}
	}
}

// AppKey is the App's key as the opener reads it, for the caller that mints a
// token of its own (the coordinator's read-only one): the environment's own
// value, the file it names, and a refusal naming both when neither is there.
func TestAppKeyFromEnv(t *testing.T) {
	pemData := keyPEM(t)
	file := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(file, []byte(pemData), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][]string{
		"inline": {EnvGitHubAppKey + "=" + pemData},
		"file":   {EnvGitHubAppKeyFile + "=" + file},
	} {
		got, err := AppKey(env)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != pemData {
			t.Errorf("%s: AppKey = %d bytes, want the key the environment names", name, len(got))
		}
	}
	if _, err := AppKey([]string{EnvGitHubAppKeyFile + "=" + filepath.Join(t.TempDir(), "missing.pem")}); err == nil ||
		!strings.Contains(err.Error(), EnvGitHubAppKeyFile) {
		t.Errorf("an unreadable key file: err = %v, want it to name %s", err, EnvGitHubAppKeyFile)
	}
	if _, err := AppKey(nil); err == nil || !strings.Contains(err.Error(), EnvGitHubAppKey) || !strings.Contains(err.Error(), EnvGitHubAppKeyFile) {
		t.Errorf("no key at all: err = %v, want it to name both variables", err)
	}
}

func TestFromEnvErrors(t *testing.T) {
	for _, tc := range []struct {
		name, kind, repo string
		env              []string
		want             string
	}{
		{"no bitbucket token", gitprov.KindBitbucket, "acme/web", nil, EnvBitbucketToken},
		{"short bitbucket token", gitprov.KindBitbucket, "acme/web", []string{EnvBitbucketToken + "=abc"}, "shorter"},
		{"no app id", gitprov.KindGitHub, "acme/web", []string{EnvGitHubAppKey + "=x"}, EnvGitHubAppID},
		{"no app key", gitprov.KindGitHub, "acme/web", []string{EnvGitHubAppID + "=1"}, EnvGitHubAppKeyFile},
		{"bad app key", gitprov.KindGitHub, "acme/web", []string{EnvGitHubAppID + "=1", EnvGitHubAppKey + "=junk"}, "not PEM"},
		{"bad repo", gitprov.KindBitbucket, "web", []string{EnvBitbucketToken + "=bb-token-1234"}, "owner/name"},
		{"unknown kind", "gitlab", "acme/web", nil, "unknown git provider"},
	} {
		if _, _, err := FromEnv(tc.env, nil, nil)(ctx, tc.kind, tc.repo); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
}

// TestBitbucketLabelsWarningReachesWarn checks that FromEnv hands its warn
// func to the Bitbucket adapter, so the labels warning is not dropped.
func TestBitbucketLabelsWarningReachesWarn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"values":[]}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1,"title":"t","draft":false,"links":{"html":{"href":"https://bitbucket.org/acme/web/pull-requests/1"}}}`))
	}))
	defer srv.Close()
	var warnings []string
	env := []string{EnvBitbucketToken + "=bb-token-1234", EnvBitbucketAPIURL + "=" + srv.URL}
	p, _, err := FromEnv(env, nil, func(msg string) { warnings = append(warnings, msg) })(ctx, gitprov.KindBitbucket, "acme/web")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x", Base: "main", Title: "t", Labels: []string{"fugaro"}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "labels") {
		t.Fatalf("warnings = %q, want one about labels", warnings)
	}
}

// fakeSource is a stand-in TokenSource: the runner installs the real one
// (github.ServiceTokens) after FromEnv, and this test only needs to see that
// the provider asks whatever was installed.
type fakeSource struct{ token string }

func (f fakeSource) Token(context.Context, time.Duration) (string, time.Time, error) {
	return f.token, time.Now().Add(time.Hour), nil
}

// TestGitHubKeylessWhenServiceOn: with the installation's credentials service
// on, the App's private key is the service's, so FromEnv opens the github
// provider without reading one at all -- not the key the job's environment
// holds, and not a file that would have to be opened to read it (C2.2, D145).
//
// The provider is refused every token until the runner installs the service
// (github.Provider.UseServiceTokens): there is no in-job path to mint one, so
// a run that did not install the service stops instead of falling back to a
// key it was not given.
func TestGitHubKeylessWhenServiceOn(t *testing.T) {
	env := []string{
		EnvGitHubAppID + "=1234",
		EnvCredentialsServiceURL + "=https://creds.example",
		// Both spellings of the key the platform would inject when the
		// service is OFF, planted here with values that could not be read
		// into a provider: a junk PEM, and a path that is not a file. A
		// provider that read either would fail here rather than open.
		EnvGitHubAppKey + "=not-a-pem-key",
		EnvGitHubAppKeyFile + "=/nonexistent/app-key.pem",
	}
	p, secrets, err := FromEnv(env, nil, nil)(ctx, gitprov.KindGitHub, "acme/web")
	if err != nil {
		t.Fatalf("opening a keyless github provider: %v", err)
	}
	if _, ok := p.(*github.Provider); !ok {
		t.Fatalf("provider %T, want *github.Provider", p)
	}
	if len(secrets) != 0 {
		t.Errorf("the provider reported %d static secrets, want none: the key is not read here", len(secrets))
	}
	if _, err := p.GitAuth(ctx, 0); !errors.Is(err, github.ErrServiceTokensIdentity) {
		t.Errorf("a keyless provider with no service installed gave %v, want a refusal", err)
	}
	// With the service installed, it is the service that answers -- and the
	// App ID the job's environment carries is what the mint names.
	src := fakeSource{token: "ghs_fixture_installed"}
	setter, ok := p.(gitprov.ServiceTokenSetter)
	if !ok {
		t.Fatalf("the keyless provider is not a gitprov.ServiceTokenSetter")
	}
	setter.UseServiceTokens(src)
	a, err := p.GitAuth(ctx, 0)
	if err != nil {
		t.Fatalf("GitAuth with the service installed: %v", err)
	}
	if a.Token != src.token || a.Env["GH_TOKEN"] != src.token {
		t.Errorf("auth = %+v, want the installed source's token", a)
	}
}

// TestGitHubReadsTheKeyWhenServiceOff: with the service off the App's key is
// the job's own, exactly as before: no service URL means the in-job mint, and
// the key is the static secret the caller must redact.
func TestGitHubReadsTheKeyWhenServiceOff(t *testing.T) {
	pemData := keyPEM(t)
	p, secrets, err := FromEnv([]string{EnvGitHubAppID + "=1234", EnvGitHubAppKey + "=" + pemData}, nil, nil)(ctx, gitprov.KindGitHub, "acme/web")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(secrets, []string{pemData}) {
		t.Errorf("secrets = %d values, want the App key", len(secrets))
	}
	if _, ok := p.(gitprov.ServiceTokenSetter); !ok {
		t.Fatalf("provider %T is not a settable one", p)
	}
}
