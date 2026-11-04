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
