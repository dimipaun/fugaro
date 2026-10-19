package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// appRepoServer fakes the three calls the App-level listing makes: the App's
// installations (as the App's JWT), one installation token per installation (as
// the App's JWT) and that installation's repositories (as the minted token). It
// pages both listings: the Nth entry of each page list is that page's answer,
// and every page but the last carries the Link rel="next" header GitHub sends.
// It records every request so a test can assert which credential went where.
type appRepoServer struct {
	*httptest.Server
	seen []string
	// bodies are the access-token request bodies, in order.
	bodies []string
}

// appRepoFixture is what the fake serves: installations per page, repositories
// per (installation, page), and whether a next page leaves the API host.
type appRepoFixture struct {
	installPages [][]int64
	repoPages    map[int64][][]string
	// installNextHost, when set, is the host a next link for the installations
	// listing points at instead of the fake's own.
	installNextHost string
}

func newAppRepoServer(t *testing.T, fx appRepoFixture) *appRepoServer {
	t.Helper()
	s := &appRepoServer{}
	page := func(r *http.Request) int {
		n, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if n < 1 {
			n = 1
		}
		return n - 1
	}
	next := func(w http.ResponseWriter, r *http.Request, path string, more bool) {
		if !more {
			return
		}
		scheme, host := "http", strings.TrimPrefix(s.URL, "http://")
		if fx.installNextHost != "" && path == "/app/installations" {
			scheme, host = "https", fx.installNextHost
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s://%s%s?per_page=100&page=%d>; rel="next"`,
			scheme, host, path, page(r)+2))
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		s.seen = append(s.seen, r.Method+" "+r.URL.Path+" "+r.URL.RawQuery+" "+auth)
		if !strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("no bearer credential on %s %s", r.Method, r.URL.Path)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/app/installations":
			if strings.Contains(auth, "ghs_") {
				t.Errorf("the installations listing was asked with an installation token, not the App's JWT")
			}
			p := page(r)
			if p >= len(fx.installPages) {
				t.Errorf("installations page %d does not exist", p+1)
				_ = json.NewEncoder(w).Encode([]map[string]any{})
				return
			}
			next(w, r, "/app/installations", p+1 < len(fx.installPages))
			out := []map[string]any{}
			for _, id := range fx.installPages[p] {
				out = append(out, map[string]any{"id": id})
			}
			_ = json.NewEncoder(w).Encode(out)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/app/installations/") && strings.HasSuffix(r.URL.Path, "/access_tokens"):
			if strings.Contains(auth, "ghs_") {
				t.Errorf("the installation token was minted with an installation token, not the App's JWT")
			}
			body, _ := io.ReadAll(r.Body)
			s.bodies = append(s.bodies, string(body))
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/app/installations/"), "/access_tokens")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "ghs_token_for_" + id,
				"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/installation/repositories":
			if !strings.HasPrefix(auth, "Bearer ghs_token_for_") {
				t.Errorf("the repositories listing was asked with %q, not the installation's own token", auth)
			}
			id, err := strconv.ParseInt(strings.TrimPrefix(auth, "Bearer ghs_token_for_"), 10, 64)
			if err != nil {
				t.Errorf("unreadable installation token %q", auth)
			}
			pages := fx.repoPages[id]
			p := page(r)
			if p >= len(pages) {
				t.Errorf("repositories page %d of installation %d does not exist", p+1, id)
				_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 0, "repositories": []any{}})
				return
			}
			next(w, r, "/installation/repositories", p+1 < len(pages))
			out := []map[string]any{}
			for _, full := range pages[p] {
				out = append(out, map[string]any{"full_name": full})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(out), "repositories": out})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func appRepoOpts(t *testing.T, url string) Options {
	return Options{AppID: "12345", PrivateKey: key(t), BaseURL: url}
}

// The listing is every installation's repositories, over every page of both
// listings, deduplicated and sorted: two installations of one App may both
// grant the same repository (a repository-level installation beside an
// owner-level one), and a caller that reads the list as a set must get one
// entry per repository.
//
// Mutation (run, restored): stop following the Link header. The second page's
// repositories disappear from the answer and this test fails.
func TestAppRepositoriesListsEveryInstallation(t *testing.T) {
	s := newAppRepoServer(t, appRepoFixture{
		installPages: [][]int64{{11}, {22}},
		repoPages: map[int64][][]string{
			11: {{"acme/web", "acme/app"}},
			22: {{"acme/app"}, {"other/thing"}},
		},
	})
	got, err := AppRepositories(context.Background(), appRepoOpts(t, s.URL))
	if err != nil {
		t.Fatalf("AppRepositories: %v", err)
	}
	want := []string{"acme/app", "acme/web", "other/thing"}
	if !slices.Equal(got, want) {
		t.Errorf("AppRepositories = %v, want %v", got, want)
	}
	for _, id := range []string{"11", "22"} {
		if !slices.ContainsFunc(s.seen, func(l string) bool {
			return strings.HasPrefix(l, "POST /app/installations/"+id+"/access_tokens")
		}) {
			t.Errorf("no installation token was minted for installation %s: %v", id, s.seen)
		}
	}
}

// The listing folds case the way the rest of Fugaro does, and not the way
// strings.ToLower does. Every name it returns is compared against — and
// offered to an operator as — a repository in task's canonical form
// (task.ASCIILower, task.CanonicalRepo), which folds A–Z and nothing else, so
// Unicode folding here would spell a repository as one nobody has: a Kelvin
// sign K (U+212A) lower-cases to an ASCII "k" and an "İ" (U+0130) to "i̇", so
// the name would come back as a repository the canonical form refuses to
// produce at all, and the sync that offers it would offer a name that cannot
// be enrolled. The look-alike is left as written, where the fold that decides
// what a repository is refuses it.
//
// Mutation (verified, run and restored): task.ASCIILower's body replaced by
// strings.ToLower — this test fails on both names, the Kelvin sign folded to
// "acme/keyboard" and the dotted capital I to "acme/i".
func TestAppRepositoriesFoldsASCIIOnly(t *testing.T) {
	// Spelled as runes, so the source stays plain text: both are names GitHub
	// can return, written the way a person would paste them.
	kelvin := "acme/" + string(rune(0x212A)) + "eyboard" // KELVIN SIGN, not "K"
	dotted := "acme/" + string(rune(0x0130))             // I WITH DOT ABOVE, not "I"
	s := newAppRepoServer(t, appRepoFixture{
		installPages: [][]int64{{11}},
		repoPages:    map[int64][][]string{11: {{"ACME/Fixture", kelvin, dotted}}},
	})
	got, err := AppRepositories(context.Background(), appRepoOpts(t, s.URL))
	if err != nil {
		t.Fatalf("AppRepositories: %v", err)
	}
	want := []string{dotted, kelvin, "acme/fixture"}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("AppRepositories = %q, want %q: the names are folded the way the canonical form does, or a repository is returned spelled as one nobody has", got, want)
	}
	// And the fold is not the one that merges them: the look-alikes are still
	// look-alikes, not the ASCII names they resemble.
	for _, name := range got {
		if name == "acme/keyboard" || name == "acme/i" {
			t.Errorf("AppRepositories = %q, in which a Unicode case fold turned a look-alike into a different repository's name", got)
		}
	}
}

// The token that reads the repository list asks for the one permission that
// call needs. Asking for none at all grants the installation's whole permission
// set, which would hand this one command a credential that can push code to
// every repository the App is installed on.
//
// Mutation (run, restored): send no body, or the run token's own permissions.
// This test fails and the fake mints a token with more than metadata: read.
func TestAppRepositoriesMintsALeastPrivilegeToken(t *testing.T) {
	s := newAppRepoServer(t, appRepoFixture{installPages: [][]int64{{11}}, repoPages: map[int64][][]string{11: {{"acme/app"}}}})
	if _, err := AppRepositories(context.Background(), appRepoOpts(t, s.URL)); err != nil {
		t.Fatalf("AppRepositories: %v", err)
	}
	if len(s.bodies) != 1 {
		t.Fatalf("access-token bodies = %v, want one", s.bodies)
	}
	var body struct {
		Permissions map[string]string `json:"permissions"`
		Repos       []string          `json:"repositories"`
	}
	if err := json.Unmarshal([]byte(s.bodies[0]), &body); err != nil {
		t.Fatalf("the access-token body is not JSON (%v): %s", err, s.bodies[0])
	}
	if len(body.Permissions) != 1 || body.Permissions["metadata"] != "read" {
		t.Errorf("the installation token asks for %v, want metadata: read alone", body.Permissions)
	}
	if len(body.Repos) != 0 {
		t.Errorf("the installation token is scoped to %v; the listing names every repository the App is installed on", body.Repos)
	}
}

// A next link that leaves the API host is refused rather than followed: the
// credential a followed cross-host link would send away is the App's JWT
// (httpjson.PagePath's rule, asserted for this caller too).
func TestAppRepositoriesRefusesANextPageOffTheHost(t *testing.T) {
	s := newAppRepoServer(t, appRepoFixture{
		installPages:    [][]int64{{11}, {22}},
		repoPages:       map[int64][][]string{11: {{"acme/app"}}},
		installNextHost: "evil.example",
	})
	_, err := AppRepositories(context.Background(), appRepoOpts(t, s.URL))
	if err == nil {
		t.Fatal("a next page on another host was followed")
	}
	if !strings.Contains(err.Error(), "left the API host") {
		t.Errorf("the refusal does not say the page left the host: %v", err)
	}
	if slices.ContainsFunc(s.seen, func(l string) bool { return strings.Contains(l, "evil.example") }) {
		t.Errorf("a request went to the foreign host: %v", s.seen)
	}
}

// GitHub refusing the App's own JWT — the private key is not that App's — is
// the failure an operator has to be told about by name.
func TestAppRepositoriesRefusesABadJWT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	t.Cleanup(srv.Close)
	_, err := AppRepositories(context.Background(), appRepoOpts(t, srv.URL))
	var bad *BadCredentialsError
	if !errors.As(err, &bad) {
		t.Fatalf("err = %v, want a BadCredentialsError", err)
	}
}

func TestAppRepositoriesNeedsAnAppIDAndKey(t *testing.T) {
	if _, err := AppRepositories(context.Background(), Options{BaseURL: "http://127.0.0.1:1"}); err == nil {
		t.Fatal("AppRepositories ran with no App ID and no key")
	}
	if _, err := AppRepositories(context.Background(), Options{AppID: "1", BaseURL: "http://127.0.0.1:1"}); err == nil {
		t.Fatal("AppRepositories ran with no key")
	}
}
