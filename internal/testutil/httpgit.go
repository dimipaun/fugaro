package testutil

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// HTTPRemote is a git remote served over HTTP by `git http-backend`, behind
// basic authentication.
type HTTPRemote struct {
	URL  string // clone URL, such as http://127.0.0.1:1234/remote.git
	Bare string // path of the bare repository behind it
}

// NewHTTPRemote seeds a bare repository with files (as NewRemote does) and
// serves it over HTTP. A request is let through only when allow accepts its
// basic-auth username and password; every other request gets a 401, which
// is how git learns to ask its credential helper.
func NewHTTPRemote(t *testing.T, files map[string]string, allow func(user, pass string) bool) *HTTPRemote {
	t.Helper()
	bare := NewRemote(t, files)
	Git(t, bare, "config", "http.receivepack", "true")
	backend := &cgi.Handler{
		Path: filepath.Join(Git(t, bare, "--exec-path"), "git-http-backend"),
		Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Dir(bare), "GIT_HTTP_EXPORT_ALL=1"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		user, pass, ok := req.BasicAuth()
		if !ok || !allow(user, pass) {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	return &HTTPRemote{URL: srv.URL + "/" + filepath.Base(bare), Bare: bare}
}

// Token returns an allow function accepting exactly user and one of tokens.
func Token(user string, tokens ...string) func(string, string) bool {
	return func(u, p string) bool {
		if u != user {
			return false
		}
		for _, tok := range tokens {
			if p == tok {
				return true
			}
		}
		return false
	}
}

// Prefix returns an allow function accepting user with any password that
// starts with prefix, for tests whose tokens change during the run.
func Prefix(user, prefix string) func(string, string) bool {
	return func(u, p string) bool { return u == user && strings.HasPrefix(p, prefix) }
}
