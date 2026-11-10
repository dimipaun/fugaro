package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
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
// real adapters' do). It resolves a ref, because a dockerfile read resolves
// one before it reads anything: the failure under test is the read's.
type quietReader struct{ gitprov.Provider }

func (quietReader) Contents(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("the read failed")
}

func (quietReader) Commit(context.Context, string) (string, error) {
	return strings.Repeat("a", 40), nil
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

// contextEntry is a dockerfile entry that names a build context, the source
// Resolve's context read is about.
func contextEntry(context string) config.Entry {
	e := dockerfileEntry("agent", "docker/agent.Dockerfile", "main")
	e.Source.Context = context
	return e
}

// agentDockerfile is the definition the entries here are read from, and
// contextDir the directory their context names.
const (
	agentDockerfile = "FROM example.invalid/base\n"
	contextDir      = "ci"
)

// contextFiles is the context tree a commit holds, keyed by the path the read
// answers with: relative to the context root, which is what the digest is
// taken over.
func contextFiles() map[string]string {
	return map[string]string{"Dockerfile": agentDockerfile, "src/main.go": "package main\n"}
}

// contextProvider is a fake provider whose branch "main" resolves to one
// commit, and whose tree at that commit holds the definition the entry names
// and the files of its context. The branch itself holds a different
// definition, so a read at the branch rather than at the commit is not the
// same read.
func contextProvider(files map[string]string) *fake.Provider {
	const sha = "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
	tree := map[string]string{"docker/agent.Dockerfile": agentDockerfile}
	for p, data := range files {
		tree[contextDir+"/"+p] = data
	}
	return &fake.Provider{Repo: "acme/app", State: fake.State{
		Branches: map[string]map[string]string{"main": {"docker/agent.Dockerfile": "the branch's own\n"}},
		Commits:  map[string]string{"main": sha},
		Trees:    map[string]map[string]string{sha: tree},
	}}
}

// TestCatalogResolveReadsTheContextAtTheResolvedCommit: the context is read
// at the commit the branch resolved to — the same commit the Dockerfile was
// read at — so the digest it stages is the digest of that tree and not of
// whatever the branch holds now.
func TestCatalogResolveReadsTheContextAtTheResolvedCommit(t *testing.T) {
	ctx := context.Background()
	files := contextFiles()
	p := contextProvider(files)
	o := &fetchOpener{p: p, cred: fakeCredential(t)}
	src, err := Resolve(ctx, o.open, gitprov.KindGitHub, contextEntry("ci"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src.Commit != p.State.Commits["main"] {
		t.Errorf("the resolved commit is %q, want the commit the branch resolved to", src.Commit)
	}
	if string(src.Dockerfile) != agentDockerfile {
		t.Errorf("the Dockerfile is %q, want the one the commit holds (%q)", src.Dockerfile, agentDockerfile)
	}
	if want := contextDigestOf(files); src.ContextHash != want {
		t.Errorf("the context hash is %q, want the digest of the commit's tree %q", src.ContextHash, want)
	}
	// An entry that names no context resolves no hash at all, which is the
	// shape every entry had before the key existed.
	if src, err = Resolve(ctx, o.open, gitprov.KindGitHub, dockerfileEntry("agent", "docker/agent.Dockerfile", "main")); err != nil || src.ContextHash != "" {
		t.Errorf("a context-less entry resolved %q, %v", src.ContextHash, err)
	}
}

// contextDigestOf is the digest of a tree written out the way the context's
// identity is defined — a sha256 over the paths and each file's own digest, in
// path order — so this test states the definition rather than asking the
// resolver for its own answer.
func contextDigestOf(files map[string]string) string {
	h := sha256.New()
	for _, p := range slices.Sorted(maps.Keys(files)) {
		sum := sha256.Sum256([]byte(files[p]))
		fmt.Fprintf(h, "%s\x00%x\n", p, sum)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// reverseTreeReader is a provider that answers a context tree in the order its
// host happened to list it — here, the reverse of the fake's own — so a digest
// that trusted the read's order would differ from the same tree read the other
// way round.
type reverseTreeReader struct{ *fake.Provider }

func (r reverseTreeReader) Tree(ctx context.Context, path, ref string, limit gitprov.TreeLimit) ([]gitprov.TreeFile, error) {
	files, err := r.Provider.Tree(ctx, path, ref, limit)
	slices.Reverse(files)
	return files, err
}

// TestCatalogResolveDigestsAContextWhateverOrderItIsRead: the digest is a
// function of the tree and not of the order a host listed it in, so two reads
// of one context agree — which is what keeps the nightly check from rebuilding
// an entry nothing changed.
func TestCatalogResolveDigestsAContextWhateverOrderItIsRead(t *testing.T) {
	ctx := context.Background()
	files := contextFiles()
	one, err := Resolve(ctx, (&fetchOpener{p: contextProvider(files), cred: fakeCredential(t)}).open, gitprov.KindGitHub, contextEntry("ci"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	two, err := Resolve(ctx, (&fetchOpener{p: reverseTreeReader{contextProvider(files)}, cred: fakeCredential(t)}).open, gitprov.KindGitHub, contextEntry("ci"))
	if err != nil {
		t.Fatalf("Resolve of a tree read in another order: %v", err)
	}
	if one.ContextHash != two.ContextHash {
		t.Errorf("the two reads digest to %q and %q, want one value", one.ContextHash, two.ContextHash)
	}
	if len(one.ContextHash) != 64 {
		t.Errorf("the context hash is %q, want a sha256", one.ContextHash)
	}
	// A file that changed is a different tree.
	changed := contextFiles()
	changed["src/main.go"] = "package other\n"
	three, err := Resolve(ctx, (&fetchOpener{p: contextProvider(changed), cred: fakeCredential(t)}).open, gitprov.KindGitHub, contextEntry("ci"))
	if err != nil {
		t.Fatalf("Resolve of a changed tree: %v", err)
	}
	if three.ContextHash == one.ContextHash {
		t.Error("a context whose file changed digests to the same value")
	}
}

// limitRecorder is a provider that reads a file and resolves a ref, and whose
// tree read refuses with the bound its caller gave it — so a test can read
// back the ceiling the resolver applies, which is D1398's own number.
type limitRecorder struct{ gitprov.Provider }

func (limitRecorder) Contents(context.Context, string, string) ([]byte, error) {
	return []byte("FROM example.invalid/base\n"), nil
}

func (limitRecorder) Commit(context.Context, string) (string, error) {
	return strings.Repeat("a", 40), nil
}

func (limitRecorder) Tree(_ context.Context, _, _ string, limit gitprov.TreeLimit) ([]gitprov.TreeFile, error) {
	recorded = limit
	return nil, fmt.Errorf("the tree is over it: %w", gitprov.ErrTreeTooLarge)
}

// recorded is the limit the last limitRecorder read was given. It is a
// package-level value because a provider is handed back through an interface
// the test cannot reach into, and the test that reads it is one test.
var recorded gitprov.TreeLimit

// TestCatalogResolveBoundsTheContext: the context is bounded at copy time
// (D1398) with this package's own ceiling, and a context over it is refused by
// the entry's name before a build is paid for.
func TestCatalogResolveBoundsTheContext(t *testing.T) {
	recorded = gitprov.TreeLimit{}
	o := &fetchOpener{p: limitRecorder{}, cred: fakeCredential(t)}
	_, err := Resolve(context.Background(), o.open, gitprov.KindGitHub, contextEntry("ci"))
	if !errors.Is(err, gitprov.ErrTreeTooLarge) {
		t.Fatalf("err = %v, want the bound's own error", err)
	}
	if !strings.Contains(err.Error(), "agent") {
		t.Errorf("the refusal %q does not name the entry", err)
	}
	if want := (gitprov.TreeLimit{MaxFiles: MaxContextFiles, MaxBytes: MaxContextBytes}); recorded != want {
		t.Errorf("the read was bounded by %+v, want %+v", recorded, want)
	}
}

// TestCatalogResolveRefusesAnEmptyContext: a context that holds no files is
// refused — a tree read cannot tell an empty directory from one that is not
// there, and an entry whose context is either is an entry whose context is a
// mistake.
func TestCatalogResolveRefusesAnEmptyContext(t *testing.T) {
	o := &fetchOpener{p: contextProvider(map[string]string{}), cred: fakeCredential(t)}
	_, err := Resolve(context.Background(), o.open, gitprov.KindGitHub, contextEntry("ci"))
	if err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("err = %v, want a refusal naming the entry", err)
	}
}

// noTreeReader reads a file and resolves a ref but cannot read a directory of
// its repository: what a context entry on such a provider is refused with.
type noTreeReader struct{ gitprov.Provider }

func (noTreeReader) Contents(context.Context, string, string) ([]byte, error) {
	return []byte("FROM example.invalid/base\n"), nil
}

func (noTreeReader) Commit(context.Context, string) (string, error) {
	return strings.Repeat("a", 40), nil
}

// TestCatalogResolveRefusesAProviderWithoutATreeReader: a provider that reads
// a file but no directory is refused by name rather than read as an empty
// context, which is the same rule Fetch has for a provider with no file read.
func TestCatalogResolveRefusesAProviderWithoutATreeReader(t *testing.T) {
	o := &fetchOpener{p: noTreeReader{}, cred: fakeCredential(t)}
	_, err := Resolve(context.Background(), o.open, gitprov.KindGitHub, contextEntry("ci"))
	if err == nil || !strings.Contains(err.Error(), "agent") || !strings.Contains(err.Error(), "context") {
		t.Fatalf("err = %v, want a refusal naming the entry and its context", err)
	}
}

// noCommitReader reads a file and a directory but cannot resolve a ref: the
// shape a host without a commit read has, which the resolver refuses rather
// than reading at the branch it could not pin.
type noCommitReader struct{ gitprov.Provider }

func (noCommitReader) Contents(context.Context, string, string) ([]byte, error) {
	return []byte(agentDockerfile), nil
}

func (noCommitReader) Tree(context.Context, string, string, gitprov.TreeLimit) ([]gitprov.TreeFile, error) {
	return []gitprov.TreeFile{{Path: "Dockerfile", Data: []byte(agentDockerfile)}}, nil
}

// TestCatalogResolveRefusesAProviderWithoutACommitReader: a provider that
// reads a file and a directory but cannot say which commit either was read at
// is refused by name — the alternative is a copy that stages the branch's
// bytes under a commit it never resolved, which is the failure D1394 exists to
// prevent.
func TestCatalogResolveRefusesAProviderWithoutACommitReader(t *testing.T) {
	o := &fetchOpener{p: noCommitReader{}, cred: fakeCredential(t)}
	_, err := Resolve(context.Background(), o.open, gitprov.KindGitHub, contextEntry("ci"))
	if err == nil || !strings.Contains(err.Error(), "agent") || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("err = %v, want a refusal naming the entry and the commit it cannot resolve", err)
	}
}

// shortCommitReader answers a ref with an abbreviated hash, which is what a
// host that resolved a ref loosely would give: a value that is not a commit
// and that a build's `git fetch`, its revision label and its record would each
// read as something else.
type shortCommitReader struct{ gitprov.Provider }

func (shortCommitReader) Contents(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("the definition was read at a ref that is not a commit")
}

func (shortCommitReader) Tree(context.Context, string, string, gitprov.TreeLimit) ([]gitprov.TreeFile, error) {
	return nil, errors.New("the context was read at a ref that is not a commit")
}

func (shortCommitReader) Commit(context.Context, string) (string, error) {
	return "3f2b1c0", nil
}

// TestCatalogResolveRefusesARefThatIsNotAFullCommit: the resolved value is
// held to a full commit before anything is read with it, so a provider that
// answered an abbreviated hash is refused by name rather than carried into a
// build, a label and a record that each want a commit. The reads below fail
// loudly, which is what says neither was made.
func TestCatalogResolveRefusesARefThatIsNotAFullCommit(t *testing.T) {
	o := &fetchOpener{p: shortCommitReader{}, cred: fakeCredential(t)}
	_, err := Resolve(context.Background(), o.open, gitprov.KindGitHub, contextEntry("ci"))
	if err == nil || !strings.Contains(err.Error(), "agent") || !strings.Contains(err.Error(), "not a full commit") {
		t.Fatalf("err = %v, want a refusal naming the entry and the value it cannot use", err)
	}
}

// TestCatalogResolveReadsAContextWrittenWithItsTrailingSlash: a context is a
// directory, and the catalog's grammar takes one written the way the key's own
// example spells it — "ci/" (D1391) — so the read resolves the same tree and
// stages the same identity as the same context written without the slash.
func TestCatalogResolveReadsAContextWrittenWithItsTrailingSlash(t *testing.T) {
	ctx := context.Background()
	files := contextFiles()
	plain, err := Resolve(ctx, (&fetchOpener{p: contextProvider(files), cred: fakeCredential(t)}).open, gitprov.KindGitHub, contextEntry("ci"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	slashed, err := Resolve(ctx, (&fetchOpener{p: contextProvider(files), cred: fakeCredential(t)}).open, gitprov.KindGitHub, contextEntry("ci/"))
	if err != nil {
		t.Fatalf("Resolve of a context written with its trailing slash: %v", err)
	}
	if slashed.ContextHash != plain.ContextHash || slashed.Commit != plain.Commit {
		t.Errorf("the context \"ci/\" resolved %q at %q, want the same as \"ci\": %q at %q",
			slashed.ContextHash, slashed.Commit, plain.ContextHash, plain.Commit)
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
		// The parser's `# escape=` directive is honoured, and its key is the
		// parser's own: case-insensitive, so an upper-case one sets the escape
		// character too.
		"# escape=`\nFROM example.invalid/base\nARG NPM_TOKEN`\n=x\n",
		"# ESCAPE=`\nFROM example.invalid/base\nARG NPM_`\nTOKEN=1\n",
		// The separator between an instruction and its name is the parser's
		// own whitespace and not only a space or a tab.
		"FROM example.invalid/base\nENV\vNPM_TOKEN=1\n",
		// A byte-order mark is stripped from the first line, where it would
		// otherwise be part of the instruction's name.
		"\ufeffARG NPM_TOKEN=1\nFROM example.invalid/base\n",
		// A comment ends at its own line whatever it ends with, so a backslash
		// in one continues nothing: the ARG below is read on its own.
		"FROM example.invalid/base\n# the trailing backslash is text \\\nARG NPM_TOKEN\n",
		// ONBUILD defers the ARG to a child image, which is the image that
		// then carries the value.
		"FROM example.invalid/base\nONBUILD ARG NPM_TOKEN\n",
		// Every whitespace-separated word of an ARG line is its own build
		// argument, so a credential-shaped name written second is declared by a
		// build all the same, with the value a --build-arg of that name carries.
		"FROM example.invalid/base\nARG FOO TOKEN\n",
		"FROM example.invalid/base\nARG NODE_VERSION NPM_TOKEN=value\n",
		"FROM example.invalid/base\nARG FOO TOKEN=\"value\"\n",
		// The continuation line's own leading whitespace is what separates the
		// two: a build reads ARG NPM_\ over an indented TOKEN as the two build
		// arguments NPM_ and TOKEN, and TOKEN is a name a credential is carried
		// in. Reading it as one harmless name is the guard missing a name a
		// build sets, so it is pinned here rather than left to the reader.
		"FROM example.invalid/base\nARG NPM_\\\n    TOKEN\n",
		"FROM example.invalid/base\nARG NPM_\\\n    TOKEN=value\n",
		// The parser stops looking for directives at the first line that is not
		// one, so a blank line, a plain comment and a comment naming something
		// else all end the block: the `# escape=` under each of them is a
		// comment to a build, the backslash is the escape character again, and
		// the two halves of the name join into the one ARG NPM_TOKEN.
		"# note\n# escape=`\nFROM example.invalid/base\nARG NPM_\\\nTOKEN\n",
		"\n# escape=`\nFROM example.invalid/base\nARG NPM_\\\nTOKEN\n",
		"# foo=bar\n# escape=`\nFROM example.invalid/base\nARG NPM_\\\nTOKEN\n",
		// A `# syntax=` directive is one the parser knows, so the block goes on
		// past it and the `# escape=` after it is followed.
		"# syntax=docker/dockerfile:1\n# escape=`\nFROM example.invalid/base\nARG NPM_`\nTOKEN=1\n",
		// `check` is a directive the parser knows too, from the dockerfile:1.8
		// frontend on — the pinned dockerfile:1.10 build has it, and
		// internal/config's knownDirectives carries the same three names — so a
		// `# check=` line does not end the block either: the `# escape=` under
		// it stands, the backtick joins the name, and a build sets NPM_TOKEN.
		"# syntax=docker/dockerfile:1.10\n# check=skip=all\n# escape=`\nFROM example.invalid/base\nARG NPM_`\nTOKEN=1\n",
		// The parser takes a leading run of `--`-prefixed words off the front
		// of a line's arguments as builder flags, and a bare `--` ends the run
		// (splitCommand's extractBuilderFlags), so the name after one is the
		// name the instruction sets: read without that cut, an ENV's name is
		// the `--` and the credential name under it is never looked at.
		"FROM example.invalid/base\nENV -- NPM_TOKEN=1\n",
		"FROM example.invalid/base\nONBUILD ENV -- NPM_TOKEN=1\n",
		// The arguments a build parses are the ones splitCommand returns, and
		// that is the remainder trimmed (strings.TrimSpace(args)): left as the
		// `--` left it, the space in front of the name is what the legacy form
		// of an ENV reads as the name, and the variable a build sets —
		// `ENV -- NPM_TOKEN value` sets NPM_TOKEN to the value `value` — is
		// never looked at.
		"FROM example.invalid/base\nENV -- NPM_TOKEN value\n",
		"FROM example.invalid/base\nENV --  NPM_TOKEN value\n",
		"FROM example.invalid/base\nONBUILD ENV -- NPM_TOKEN value\n",
		// The words of a line are cut by runes, as the parser cuts them
		// (parseWords decodes with utf8.DecodeRuneInString): the whitespace a
		// build separates words with includes the multi-byte runes, and a reader
		// that tests one byte at a time sees their bytes as ordinary characters.
		// Read that way the line below is the one word A=1　NPM_TOKEN=1 and
		// the pair a build reads second is never looked at.
		"FROM example.invalid/base\nENV A=1　NPM_TOKEN=1\n",
		"FROM example.invalid/base\nENV A=1 NPM_TOKEN=1\n",
		"FROM example.invalid/base\nARG A　NPM_TOKEN\n",
		"FROM example.invalid/base\nARG -- NPM_TOKEN=1\n",
		"FROM example.invalid/base\nARG --build-arg NPM_TOKEN=1\n",
		"FROM example.invalid/base\nARG --x=1 FOO TOKEN\n",
		// An ENV line's pairs are the parser's own words, so an escaped quote
		// stays inside the value it is written in and the next pair is a pair.
		// Read by hand, up to the next quote byte, the value's end is guessed:
		// the rest of the line is swallowed with it, and the name a build sets
		// is either never looked at or read out of the middle of a value.
		"FROM example.invalid/base\nENV A=\"\\\" B=\" NPM_TOKEN=1\n",
		"FROM example.invalid/base\nENV A=\"a\\\"b\" NPM_TOKEN=1\n",
		// A credential-shaped name is refused unless the line gives it a path
		// (pathValue): a name that holds a credential and does not point at one
		// still carries what --build-arg or the environment puts in the layer.
		"FROM example.invalid/base\nENV GOOGLE_APPLICATION_CREDENTIALS={\"type\":\"service_account\"}\n",
		"FROM example.invalid/base\nENV GOOGLE_APPLICATION_CREDENTIALS=$HOME/.config/gcloud/adc.json\n",
		"FROM example.invalid/base\nENV AWS_SHARED_CREDENTIALS_FILE=credentials.ini\n",
		"FROM example.invalid/base\nARG API_KEY_PATH\n",
		// Every trailing carriage return is the parser's to trim
		// (trimNewline is bytes.TrimRight(src, "\r\n")), so a doubled one does
		// not hide the continuation: a build reads ARG NPM_TOKEN here.
		"FROM example.invalid/base\r\r\nARG NPM_\\\r\r\nTOKEN\r\r\n",
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
		// An ENV keeps the parser's own reading: the legacy form sets the one
		// variable its first word names, so ENV NPM_\ over an indented TOKEN is
		// the variable NPM_ with the value TOKEN=value. The same shape on an
		// ARG line is refused (above), because an ARG declares every word of it.
		"FROM example.invalid/base\nENV NPM_\\\n    TOKEN=value\n",
		// With the parser's escape character set to the backtick, a trailing
		// backslash is a character in the name and not a join.
		"# escape=`\nFROM example.invalid/base\nARG NPM_\\\nTOKEN\n",
		// And with the directive stopped by a plain comment before it, the
		// backslash is the escape character a build uses: ARG NPM` is the one
		// build argument named NPM`, and the TOKEN line under it is a word no
		// ARG line sets, so neither is a name a credential is carried in.
		"# note\n# escape=`\nFROM example.invalid/base\nARG NPM`\nTOKEN\n",
		// Several words on an ARG line are several names, and none of these is
		// credential-shaped.
		"FROM example.invalid/base\nARG NODE_VERSION GOPATH=/go\n",
		// A quoted default is one word with blanks in it (the parser's own
		// reading, parseWords), so the words inside the quotes are part of a
		// value and not names: a split on whitespace would refuse this
		// definition over a build argument named credentials that it does not
		// declare. The same on an ENV line.
		"FROM example.invalid/base\nARG NOTES=\"no credentials here\"\n",
		"FROM example.invalid/base\nENV NOTES=\"not a password field\"\n",
		// The multi-byte whitespace is whitespace inside a quoted stretch to no
		// one: the quotes are what make it one word, and the words inside them
		// are a value and not names.
		"FROM example.invalid/base\nARG NOTES=\"no credentials here\"\n",
		// A pointer is not a credential: a name that is credential-shaped but is
		// given a path carries no secret into the layer, and the platform's own
		// names are pointers. The first of these is the definition
		// docs/gcp-live-checklist.md's Check 291 builds — an entry that cannot be
		// renamed and has to be accepted for that check to run at all.
		"FROM example.invalid/base\nENV NEXUS_BASEURL=https://example.invalid/repository/maven-public/\nENV GOOGLE_APPLICATION_CREDENTIALS=/dev/null\n",
		"FROM example.invalid/base\nENV GOOGLE_APPLICATION_CREDENTIALS /dev/null\n",
		"FROM example.invalid/base\nENV AWS_SHARED_CREDENTIALS_FILE=/root/.aws/credentials\n",
		"FROM example.invalid/base\nENV API_KEY_PATH=/run/secrets/api_key\n",
		"FROM example.invalid/base\nARG SSH_PRIVATE_KEY_PATH=/home/fugaro/.ssh/id_ed25519\n",
		"FROM example.invalid/base\nENV X=1 CREDENTIALS_FILE=./creds.json\n",
		"FROM example.invalid/base\nONBUILD ENV GOOGLE_APPLICATION_CREDENTIALS=/dev/null\n",
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
		// And the raw bytes are read as well, because a build is not the only
		// reader: the definition is copied into the project's bucket whole,
		// comments and all. A comment inside a continuation is dropped with the
		// continuation it belongs to, so this URL is in no logical line — and the
		// token is in the project all the same.
		"FROM example.invalid/base\nRUN echo a \\\n# https://user:" + token + "@example.invalid/x\n b\n",
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
