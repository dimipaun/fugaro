package infra

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	ghKeyOnce sync.Once
	ghKey     *rsa.PrivateKey
)

// githubTestKey is one 2048-bit key for the whole file: generating one per
// test dominates the package's runtime.
func githubTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	ghKeyOnce.Do(func() {
		var err error
		if ghKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
	})
	return ghKey
}

// githubAppServer answers the two calls InstalledRepos makes and returns the
// names as GitHub spells them (its own capitalisation).
func githubAppServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/app/installations":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7}})
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/7/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_x", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
		case r.Method == http.MethodGet && r.URL.Path == "/installation/repositories":
			out := []map[string]any{}
			for _, n := range names {
				out = append(out, map[string]any{"full_name": n})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(out), "repositories": out})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The answer is the canonical form the allow-list and the runner both use
// ("<provider>/<owner>/<name>", lower-cased), not GitHub's own spelling: a
// name typed into `project repos add` from this list has to be one the
// publisher accepts, and an allow-list entry that matched only GitHub's
// capitalisation would admit nothing.
func TestInstalledReposAreCanonical(t *testing.T) {
	srv := githubAppServer(t, "Acme/Web", "acme/app")
	got, err := InstalledRepos(context.Background(), GitHubApp{AppID: "1", PrivateKey: githubTestKey(t), BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("InstalledRepos: %v", err)
	}
	want := []string{"github/acme/app", "github/acme/web"}
	if !slices.Equal(got, want) {
		t.Errorf("InstalledRepos = %v, want %v", got, want)
	}
}

// A name GitHub returns that is not a repository name at all is refused rather
// than carried into the answer. The answer's only reader offers these names as
// allow-list entries, and an entry the publisher would refuse is a repository
// the operator would believe enrolled.
//
// Mutation (run, restored): drop the Canonical error check. This test fails and
// the junk name reaches the caller.
func TestInstalledReposRefuseANameThatIsNotARepository(t *testing.T) {
	for _, bad := range []string{"acme", "acme/we b", "acme/../etc", "/etc/passwd"} {
		t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
			srv := githubAppServer(t, "acme/app", bad)
			_, err := InstalledRepos(context.Background(), GitHubApp{AppID: "1", PrivateKey: githubTestKey(t), BaseURL: srv.URL})
			if err == nil {
				t.Fatalf("InstalledRepos accepted %q", bad)
			}
			if !strings.Contains(err.Error(), "not a repository name") {
				t.Errorf("the refusal does not say which name was refused: %v", err)
			}
		})
	}
}

func TestInstalledReposNeedsTheAppCredentials(t *testing.T) {
	if _, err := InstalledRepos(context.Background(), GitHubApp{}); err == nil {
		t.Fatal("InstalledRepos ran with no App ID and no key")
	}
}
