package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
)

// fakeCredential is the value the opener below hands out as the project's
// repository credential. It is built at run time rather than written here,
// so no secret-shaped literal lives in this file, and the tests only ever
// assert that it is absent from what a fetch returns.
func fakeCredential(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte("catalog-fetch:" + t.Name()))
	return "cred-" + hex.EncodeToString(sum[:12])
}

// fetchOpener is a gitprov.Opener for one provider, recording every open and
// handing out a credential the fetch must never write anywhere.
type fetchOpener struct {
	p      gitprov.Provider
	cred   string
	opened []string
}

func (o *fetchOpener) open(_ context.Context, kind, repo string) (gitprov.Provider, []string, error) {
	o.opened = append(o.opened, kind+":"+repo)
	return o.p, []string{o.cred}, nil
}

// quietReader is a provider whose read fails without quoting the path back:
// what a message then prints is the resolver's own, which is what the test
// below is about (a provider's error carries the path it was given, as the
// real adapters' do).
type quietReader struct{ gitprov.Provider }

func (quietReader) Contents(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("the read failed")
}

func dockerfileEntry(name, path, branch string) config.Entry {
	return config.Entry{Name: name, Source: config.Source{
		Kind: config.SourceDockerfile, Repo: "acme/app", Path: path, Branch: branch,
	}}
}

// TestCatalogFetchRejectsAPathOutsideTheRepository: the path a source names
// is read out of a repository through the contents API, so anything that is
// not one clean relative path inside it is refused before a provider is
// even opened (the fetch's own bound on a SEC read path: PIC1 refuses the
// same values at publish time, and this is where one reaches an API).
func TestCatalogFetchRejectsAPathOutsideTheRepository(t *testing.T) {
	ctx := context.Background()
	for _, path := range []string{
		"../docker/agent.Dockerfile",
		"/etc/agent.Dockerfile",
		"docker/../../etc/agent.Dockerfile",
		"./docker/agent.Dockerfile",
		"docker/./agent.Dockerfile",
		"docker//agent.Dockerfile",
		"docker/",
		".",
		"..",
		"docker/agent.Dockerfile\n",
	} {
		o := &fetchOpener{p: &fake.Provider{Repo: "acme/app"}, cred: fakeCredential(t)}
		e := dockerfileEntry("agent", path, "")
		if _, err := Fetch(ctx, o.open, gitprov.KindGitHub, e); err == nil {
			t.Fatalf("the path %q was accepted", path)
		}
		if len(o.opened) != 0 {
			t.Fatalf("the path %q reached the provider as %v", path, o.opened)
		}
	}
}

// TestCatalogFetchRefusesARefTheCatalogWouldRefuse: the branch a source
// names is held to the rule the catalog's own validation applies
// (config.ValidBranchName) and not to a narrower one of this package's, so a
// ref the catalog refuses never reaches a provider and this guard cannot
// drift below what the catalog checked.
func TestCatalogFetchRefusesARefTheCatalogWouldRefuse(t *testing.T) {
	ctx := context.Background()
	for _, branch := range []string{
		"../main",
		"feat/x..y",
		"release.lock",
		"main//x",
		"main/",
		"-b",
		"a b",
	} {
		o := &fetchOpener{p: &fake.Provider{Repo: "acme/app"}, cred: fakeCredential(t)}
		e := dockerfileEntry("agent", "docker/agent.Dockerfile", branch)
		if _, err := Fetch(ctx, o.open, gitprov.KindGitHub, e); err == nil {
			t.Fatalf("the branch %q was accepted", branch)
		}
		if len(o.opened) != 0 {
			t.Fatalf("the branch %q reached the provider", branch)
		}
	}
}

// TestCatalogFetchUsesTheExistingCredentialAndLogsNone: a dockerfile source
// is read through the provider the opener returns — the project's existing
// credential, at the repository and branch the entry names — and neither the
// bytes nor a failure carries any of that credential.
func TestCatalogFetchUsesTheExistingCredentialAndLogsNone(t *testing.T) {
	ctx := context.Background()
	const def = "FROM example.invalid/base\nRUN true\n"
	cred := fakeCredential(t)
	o := &fetchOpener{
		p: &fake.Provider{Repo: "acme/app", State: fake.State{
			Branches: map[string]map[string]string{"release": {"docker/agent.Dockerfile": def}},
		}},
		cred: cred,
	}
	e := dockerfileEntry("agent", "docker/agent.Dockerfile", "release")

	// The default branch holds nothing: a fetch that ignored the branch (or
	// read another repository) cannot produce the definition below.
	if _, err := Fetch(ctx, o.open, gitprov.KindGitHub, dockerfileEntry("agent", "docker/agent.Dockerfile", "")); err == nil {
		t.Fatal("the file was read from the default branch")
	}
	if want := []string{"github:acme/app"}; !slices.Equal(o.opened, want) {
		t.Fatalf("opened %v, want %v", o.opened, want)
	}
	data, err := Fetch(ctx, o.open, gitprov.KindGitHub, e)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != def {
		t.Fatalf("data = %q, want %q", data, def)
	}
	if want := []string{"github:acme/app", "github:acme/app"}; !slices.Equal(o.opened, want) {
		t.Fatalf("opened %v, want %v", o.opened, want)
	}
	if strings.Contains(string(data), cred) {
		t.Fatal("the credential reached the fetched bytes")
	}

	missing := dockerfileEntry("agent", "docker/absent.Dockerfile", "release")
	_, err = Fetch(ctx, o.open, gitprov.KindGitHub, missing)
	if err == nil {
		t.Fatal("a file the repository does not hold was read")
	}
	if want := []string{"github:acme/app", "github:acme/app", "github:acme/app"}; !slices.Equal(o.opened, want) {
		t.Fatalf("opened %v, want %v", o.opened, want)
	}
	if strings.Contains(err.Error(), cred) {
		t.Fatal("the credential reached the failure")
	}
	if !strings.Contains(err.Error(), "docker/absent.Dockerfile") || !strings.Contains(err.Error(), "acme/app") {
		t.Fatalf("the failure does not name what was read: %v", err)
	}
}

// TestCatalogFetchPrintsThePathEscaped: the path a source names is a value a
// person reads back in a failure, and gitprov.ValidRepoPath deliberately
// allows formatting (bidi) characters in one, so the resolver escapes it on
// its way into the message rather than printing whatever it was given.
func TestCatalogFetchPrintsThePathEscaped(t *testing.T) {
	const bidi = "docker/agent\u202ename.Dockerfile"
	o := &fetchOpener{p: quietReader{}, cred: fakeCredential(t)}
	_, err := Fetch(context.Background(), o.open, gitprov.KindGitHub, dockerfileEntry("agent", bidi, ""))
	if err == nil || !strings.Contains(err.Error(), `\u202e`) {
		t.Fatalf("err = %v, want the path escaped", err)
	}
	if strings.ContainsRune(err.Error(), '\u202e') {
		t.Fatalf("err = %q carries the raw path", err)
	}
}

// TestCatalogFetchIsBounded: a definition larger than the bound is refused
// whatever its source, so nothing but a Dockerfile's worth of bytes reaches
// the bucket a build reads.
func TestCatalogFetchIsBounded(t *testing.T) {
	ctx := context.Background()
	big := strings.Repeat("RUN true\n", MaxDockerfileBytes/9+2)
	if len(big) <= MaxDockerfileBytes {
		t.Fatalf("the fixture is %d bytes, not over the %d-byte bound", len(big), MaxDockerfileBytes)
	}
	o := &fetchOpener{
		p:    &fake.Provider{Repo: "acme/app", State: fake.State{Files: map[string]string{"docker/agent.Dockerfile": big}}},
		cred: fakeCredential(t),
	}
	if _, err := Fetch(ctx, o.open, gitprov.KindGitHub, dockerfileEntry("agent", "docker/agent.Dockerfile", "")); err == nil {
		t.Fatal("an oversized definition was fetched")
	}
	if _, err := Fetch(ctx, o.open, gitprov.KindGitHub, config.Entry{Name: "agent", Source: config.Source{Kind: config.SourceInline, Inline: big}}); err == nil {
		t.Fatal("an oversized inline definition was written from state")
	}
}

// inlineEntry is a catalog entry whose definition is held in the catalog
// itself, the other source a credential-shaped definition reaches a build
// through.
func inlineEntry(def string) config.Entry {
	return config.Entry{Name: "agent", Source: config.Source{Kind: config.SourceInline, Inline: def}}
}

// TestDockerfileSecretArgRefused: a definition that names a credential in an
// ARG or an ENV is refused wherever it came from — an inline entry and a file
// fetched out of a repository both reach the same bucket key and the same
// build — and the refusal names the secret mount a build uses instead. The
// name is enough on its own: an ARG whose default is empty still carries the
// value a --build-arg passes through the layer.
//
// A definition that names no credential is accepted, so the rule pins the
// shapes it is about rather than every definition.
func TestDockerfileSecretArgRefused(t *testing.T) {
	ctx := context.Background()
	for _, def := range []string{
		"FROM example.invalid/base\nARG NPM_TOKEN\nRUN true\n",
		"FROM example.invalid/base\nARG GIT_TOKEN=value\n",
		"FROM example.invalid/base\nENV GITHUB_TOKEN=value\n",
		"FROM example.invalid/base\nENV AWS_SECRET_ACCESS_KEY=value\n",
		"FROM example.invalid/base\nARG CI_JOB_TOKEN=\"value\"\n",
		"FROM example.invalid/base\nenv npm_config_//registry.npmjs.org/:_authToken=value\n",
		"FROM example.invalid/base\nARG FUGARO_BASE\nARG \\\n    DEPLOY_PASSWORD\n",
		// The continuation joins as the parser joins it: NPM_\ + TOKEN is the
		// one name NPM_TOKEN a build sets, and a join that inserted a space
		// would read the harmless NPM. A comment inside a continuation is
		// dropped with it and the halves join across it, the same way.
		"FROM example.invalid/base\nARG NPM_\\\nTOKEN\n",
		"FROM example.invalid/base\nENV GIT_\\\nTOKEN=value\n",
		"FROM example.invalid/base\nARG NPM\\\n# a comment a build drops\nTOKEN\n",
		"FROM example.invalid/base\nARG NPM_\\\n\nTOKEN\n",
		// A lone continuation character continues the line it is alone on the
		// same way.
		"FROM example.invalid/base\nARG NPM_\\\n\\\nTOKEN\n",
		// The parser's `# escape=` directive is honoured: a backtick continues
		// a line, and a backslash at the end of one is text.
		"# escape=`\nFROM example.invalid/base\nARG NPM_TOKEN`\n=x\n",
		// A comment ends at its own line whatever it ends with, so a backslash
		// in one continues nothing: the ARG below is read on its own.
		"FROM example.invalid/base\n# the trailing backslash is text \\\nARG NPM_TOKEN\n",
		// ONBUILD defers the ARG to a child image, which is the image that
		// then carries the value.
		"FROM example.invalid/base\nONBUILD ARG NPM_TOKEN\n",
	} {
		_, err := Fetch(ctx, nil, gitprov.KindGitHub, inlineEntry(def))
		if err == nil {
			t.Errorf("the inline definition %q was accepted", def)
		} else if !strings.Contains(err.Error(), "--mount=type=secret") {
			t.Errorf("the inline refusal does not name the secret mount: %v", err)
		}
		o := &fetchOpener{
			p:    &fake.Provider{Repo: "acme/app", State: fake.State{Files: map[string]string{"docker/agent.Dockerfile": def}}},
			cred: fakeCredential(t),
		}
		_, err = Fetch(ctx, o.open, gitprov.KindGitHub, dockerfileEntry("agent", "docker/agent.Dockerfile", ""))
		if err == nil {
			t.Errorf("the fetched definition %q was accepted", def)
		} else if !strings.Contains(err.Error(), "--mount=type=secret") {
			t.Errorf("the fetched refusal does not name the secret mount: %v", err)
		}
	}
	for _, def := range []string{
		"FROM example.invalid/base\nARG NODE_VERSION=22\nRUN true\n",
		"FROM example.invalid/base\nENV PATH=/usr/local/bin:$PATH\n",
		"FROM example.invalid/base\nENV TFLINT_PLUGIN_DIR=/home/fugaro/.tflint.d/plugins\n",
		"FROM example.invalid/base\nARG REPO_URL\nARG BASE_BRANCH\nRUN git clone \"$REPO_URL\" /work/repo\n",
		"FROM example.invalid/base\nENV AUTH_MODE=none\n",
		// A continuation line keeps its own leading whitespace, which is the
		// parser's reading and not a tidier one: ARG NPM_\ over an indented
		// TOKEN is the build argument NPM_ with the default TOKEN, and no
		// build argument named after a credential, so it is not refused.
		"FROM example.invalid/base\nARG NPM_\\\n    TOKEN\n",
		"FROM example.invalid/base\nENV NPM_\\\n    TOKEN=value\n",
		// With the parser's escape character set to the backtick, a trailing
		// backslash is a character in the name and not a join.
		"# escape=`\nFROM example.invalid/base\nARG NPM_\\\nTOKEN\n",
	} {
		if _, err := Fetch(ctx, nil, gitprov.KindGitHub, inlineEntry(def)); err != nil {
			t.Errorf("the definition %q was refused: %v", def, err)
		}
	}
}

// TestDockerfileUserinfoURLRefused: a definition that carries a URL with
// userinfo is refused, inline and fetched alike, and the refusal names the
// secret mount — the shape the project's own builds use (D1150, the
// git-credentials mount) rather than a token written into the definition.
//
// The token is built at run time and asserted absent from the refusal: a
// message that named the URL would leak into the log exactly the credential
// this refusal keeps out of the definition. A URL without userinfo, and the
// scp-like clone form that can hold no password, are accepted.
func TestDockerfileUserinfoURLRefused(t *testing.T) {
	ctx := context.Background()
	token := fakeCredential(t)
	for _, def := range []string{
		"FROM example.invalid/base\nRUN git clone https://user:" + token + "@example.invalid/acme/app.git /work/repo\n",
		"FROM example.invalid/base\nRUN git clone https://" + token + "@example.invalid/acme/app.git /work/repo\n",
		"FROM example.invalid/base\nRUN git clone ssh://git:" + token + "@example.invalid/acme/app.git /work/repo\n",
		"FROM example.invalid/base\nENV DATABASE_URL=postgres://user:" + token + "@db.internal/app\n",
		// The URL is read joined, so a credential split across a continuation
		// is the credential a build carries into the layer.
		"FROM example.invalid/base\nRUN git clone https://user:" + token + "\\\n@example.invalid/acme/app.git /work/repo\n",
	} {
		_, err := Fetch(ctx, nil, gitprov.KindGitHub, inlineEntry(def))
		if err == nil {
			t.Errorf("the inline definition %q was accepted", def)
		} else {
			if !strings.Contains(err.Error(), "--mount=type=secret") {
				t.Errorf("the inline refusal does not name the secret mount: %v", err)
			}
			if strings.Contains(err.Error(), token) {
				t.Errorf("the refusal carries the credential: %v", err)
			}
		}
		o := &fetchOpener{
			p:    &fake.Provider{Repo: "acme/app", State: fake.State{Files: map[string]string{"docker/agent.Dockerfile": def}}},
			cred: fakeCredential(t),
		}
		_, err = Fetch(ctx, o.open, gitprov.KindGitHub, dockerfileEntry("agent", "docker/agent.Dockerfile", ""))
		if err == nil {
			t.Errorf("the fetched definition %q was accepted", def)
		} else if strings.Contains(err.Error(), token) {
			t.Errorf("the fetched refusal carries the credential: %v", err)
		}
	}
	for _, def := range []string{
		"FROM example.invalid/base\nRUN git clone \"$REPO_URL\" /work/repo\n",
		"FROM example.invalid/base\nRUN git clone git@example.invalid:acme/app.git /work/repo\n",
		"FROM example.invalid/base\nRUN curl -fsSL https://example.invalid/tool.tar.gz | tar xz\n",
		"FROM example.invalid/base\n# see https://example.invalid/issues/1 for person@example.invalid\n",
	} {
		if _, err := Fetch(ctx, nil, gitprov.KindGitHub, inlineEntry(def)); err != nil {
			t.Errorf("the definition %q was refused: %v", def, err)
		}
	}
}

// noReader is a provider of a kind that cannot read a file out of its
// repository: an opener hands one back for a kind this package's list does
// not admit, and the fetch must refuse the source rather than call a method
// the provider has not got.
type noReader struct{ gitprov.Provider }

// TestCatalogReadableNamesTheKindsThatReadAFile: the list `fugaro image add`
// and `fugaro validate` refuse on, offline and before anything is built, of
// which fetchDockerfile's type assertion is the runtime half.
func TestCatalogReadableNamesTheKindsThatReadAFile(t *testing.T) {
	for _, kind := range []string{gitprov.KindGitHub, gitprov.KindBitbucket} {
		if !Readable(kind) {
			t.Errorf("Readable(%q) = false, want the adapter that reads a file", kind)
		}
	}
	// A kind is the spelling the configs carry, and these are not ones.
	for _, kind := range []string{"", "gitlab", "GitHub", "Bitbucket"} {
		if Readable(kind) {
			t.Errorf("Readable(%q) = true, want a kind no reader answers", kind)
		}
	}
}

// TestCatalogFetchRefusesAProviderWithoutAReader: the refusal is the one
// sentence, so an operator who meets the limit at `fugaro image add` meets
// the one the build itself would have given.
func TestCatalogFetchRefusesAProviderWithoutAReader(t *testing.T) {
	o := &fetchOpener{p: noReader{}, cred: fakeCredential(t)}
	e := dockerfileEntry("agent", "docker/agent.Dockerfile", "main")
	_, err := Fetch(context.Background(), o.open, "gitlab", e)
	if err == nil {
		t.Fatal("a provider with no reader was asked for a file")
	}
	if want := UnreadableRefusal(e, "gitlab").Error(); err.Error() != want {
		t.Errorf("the refusal = %q, want %q", err, want)
	}
}
