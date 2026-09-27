package providers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
