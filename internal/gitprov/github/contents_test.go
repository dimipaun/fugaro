package github

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
)

// fixtureToken is the installation token the exchanges here mint. It is
// built at run time from the test's name, so no token-shaped literal is
// written into this tree.
func fixtureToken(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte("github-contents:" + t.Name()))
	return "ghs_" + hex.EncodeToString(sum[:8])
}

// mintExchanges are the two exchanges a REST call mints its installation
// token with, in the order the client makes them. A test that makes a call
// puts them in front of its own.
func mintExchanges(t *testing.T) []string {
	t.Helper()
	return []string{
		`{"method": "GET", "path": "/repos/acme/web/installation", "status": 200, "response": {"id": 777}}`,
		fmt.Sprintf(`{"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": %q, "expires_at": "2026-09-27T11:00:00Z"}}`, fixtureToken(t)),
	}
}

// contentsExchange is one contents read: the API's path as it is sent (each
// segment escaped, its query with it), answered with reply and status. The
// bearer token is asserted, so a read that did not carry the installation
// token fails the test.
func contentsExchange(t *testing.T, path string, status int, reply string) string {
	t.Helper()
	return fmt.Sprintf(`{"method": "GET", "path": %q, "auth": "Bearer %s", "status": %d, "response": %s}`,
		path, fixtureToken(t), status, reply)
}

// contentsReply is the contents API's answer about one entry: what it is,
// how its content is encoded, the size it declares and the content itself.
func contentsReply(kind, encoding string, size int, content string) string {
	return fmt.Sprintf(`{"type": %q, "encoding": %q, "size": %d, "content": %q}`, kind, encoding, size, content)
}

// contentsProvider replays exchanges. The fixture file is written into a
// temporary directory, the way the bitbucket package's inline fixtures are,
// so every value in it — the definition, its base64 and its size — is built
// here at run time rather than captured in testdata.
func contentsProvider(t *testing.T, exchanges ...string) *Provider {
	t.Helper()
	file := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(file, []byte("["+strings.Join(exchanges, ",")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httpfixture.Serve(t, file)
	p, err := New(Options{Owner: "acme", Repo: "web", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
		Now: func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// definition is the Dockerfile the reads here answer with: long enough that
// its base64 wraps, as GitHub's does.
func definition() string {
	return "FROM example.invalid/base\n" + strings.Repeat("RUN true\n", 10)
}

// wrappedBase64 is b as GitHub sends it: the base64 wrapped at 60
// characters with newlines between the lines.
func wrappedBase64(b []byte) string {
	s := base64.StdEncoding.EncodeToString(b)
	var out strings.Builder
	for len(s) > 60 {
		out.WriteString(s[:60])
		out.WriteString("\n")
		s = s[60:]
	}
	out.WriteString(s)
	return out.String()
}

// TestContentsReadsTheFileAtItsRef: one file of this repository, read at the
// branch the source names, decoded from the base64 GitHub wraps — and the
// request the adapter makes is the escaped one: the path escaped segment by
// segment, so a segment's "?" is the file's rather than the query's, and the
// ref an encoded parameter.
func TestContentsReadsTheFileAtItsRef(t *testing.T) {
	def := definition()
	body := contentsReply("file", "base64", len(def), wrappedBase64([]byte(def)))
	p := contentsProvider(t, append(mintExchanges(t),
		contentsExchange(t, "/repos/acme/web/contents/docker/agent.Dockerfile?ref=release", 200, body),
		contentsExchange(t, "/repos/acme/web/contents/Dockerfile", 200, body),
	)...)
	got, err := p.Contents(ctx, "docker/agent.Dockerfile", "release")
	if err != nil {
		t.Fatalf("Contents: %v", err)
	}
	if string(got) != def {
		t.Fatalf("Contents = %q, want %q", got, def)
	}
	// No ref: the API's own default branch, and no ref parameter at all.
	if got, err = p.Contents(ctx, "Dockerfile", ""); err != nil || string(got) != def {
		t.Fatalf("Contents = %q, %v", got, err)
	}
	for _, tc := range []struct {
		path string
		q    url.Values
		want string
	}{
		{"Dockerfile", nil, "/repos/acme/web/contents/Dockerfile"},
		{"docker/sub/agent.Dockerfile", url.Values{"ref": {"release"}}, "/repos/acme/web/contents/docker/sub/agent.Dockerfile?ref=release"},
		// "?" and "#" in a file's name stay in the file's name, and the ref
		// is encoded: a "/" in a branch name cannot end the value.
		{"docker/agent?v=1", url.Values{"ref": {"release/x"}}, "/repos/acme/web/contents/docker/agent%3Fv=1?ref=release%2Fx"},
		{"docker/agent#1.Dockerfile", nil, "/repos/acme/web/contents/docker/agent%231.Dockerfile"},
	} {
		if got := p.contentsPath(tc.path, tc.q); got != tc.want {
			t.Errorf("contentsPath(%q, %v) = %q, want %q", tc.path, tc.q, got, tc.want)
		}
	}
}

// TestContentsRefusesWhatItCannotRead: a definition a build may be handed is
// a regular file's bytes, so every other shape the API answers with — a
// directory, a symlink, a submodule, an encoding this reader cannot decode,
// a size outside the bound, content that is not base64 — is refused rather
// than passed on.
func TestContentsRefusesWhatItCannotRead(t *testing.T) {
	const path = "docker/agent.Dockerfile"
	ok := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	for _, tc := range []struct {
		name  string
		reply string
		want  string
	}{
		{"a directory", contentsReply("dir", "", 0, ""), "dir"},
		{"a symlink that is not a file", contentsReply("symlink", "base64", 4, ok([]byte("else"))), "symlink"},
		{"a submodule", contentsReply("submodule", "base64", 4, ok([]byte("else"))), "submodule"},
		// Files GitHub serves over 1 MB, and anything else it does not base64.
		{"an encoding it cannot decode", contentsReply("file", "none", 40, ""), "none"},
		{"a declared size of zero", contentsReply("file", "base64", 0, ""), "0 bytes"},
		{"a declared size over the bound", contentsReply("file", "base64", gitprov.MaxContentsBytes+1, ok([]byte("FROM x\n"))), "more than"},
		{"content that is not base64", contentsReply("file", "base64", 4, "!!!!not base64!!!!"), "decoding"},
		{"content that decodes over the bound", contentsReply("file", "base64", 4, ok(make([]byte, gitprov.MaxContentsBytes+1))), "decoded to"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := contentsProvider(t, append(mintExchanges(t),
				contentsExchange(t, "/repos/acme/web/contents/"+path, 200, tc.reply))...)
			_, err := p.Contents(ctx, path, "")
			if err == nil {
				t.Fatalf("the reply was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("err = %v, want it to name the file", err)
			}
		})
	}
}
