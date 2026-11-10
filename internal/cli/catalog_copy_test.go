package cli

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

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
)

// catalogOpen is a gitprov.Opener for one fake provider, so a copy test
// reads through the same path a real one does without a network.
func catalogOpen(p *fake.Provider) gitprov.Opener {
	return func(context.Context, string, string) (gitprov.Provider, []string, error) { return p, nil, nil }
}

// TestCatalogDockerfileIsCopiedUnderBuildsCatalog: a catalog entry's
// Dockerfile is fetched from its repository and copied into the project's
// bucket at builds/catalog/<name>/Dockerfile, the one object a catalog
// build reads (spec.md §Where Dockerfiles live).
func TestCatalogDockerfileIsCopiedUnderBuildsCatalog(t *testing.T) {
	ctx := context.Background()
	const def = "FROM example.invalid/base\nRUN true\n"
	b := gcpfake.NewGCS(t).Bucket(t, "fugaro-runs-proj-1234")
	e := config.Entry{Name: "agent", Source: config.Source{
		Kind: config.SourceDockerfile, Repo: "acme/app", Path: "docker/agent.Dockerfile",
	}}
	open := catalogOpen(&fake.Provider{Repo: "acme/app", State: fake.State{Files: map[string]string{"docker/agent.Dockerfile": def}}})
	if err := writeCatalogDockerfile(ctx, b, open, gitprov.KindGitHub, e); err != nil {
		t.Fatalf("writeCatalogDockerfile: %v", err)
	}
	key, err := config.CatalogDockerfileKey("agent")
	if err != nil || key != "builds/catalog/agent/Dockerfile" {
		t.Fatalf("key = %q, %v; want builds/catalog/agent/Dockerfile", key, err)
	}
	got, _, err := b.Read(ctx, key)
	if err != nil {
		t.Fatalf("reading the copy: %v", err)
	}
	if string(got) != def {
		t.Fatalf("the copy is %q, want %q", got, def)
	}
}

// TestCatalogCopySkipsAnUnchangedDockerfile: the copy is written with
// layer_copy.go's read-compare-write-if-changed rule, so republishing a
// definition that did not change leaves the object (and its generation)
// alone, and one that did change is written.
func TestCatalogCopySkipsAnUnchangedDockerfile(t *testing.T) {
	ctx := context.Background()
	const first = "FROM example.invalid/base\nRUN true\n"
	gcs := gcpfake.NewGCS(t)
	b := gcs.Bucket(t, "fugaro-runs-proj-1234")
	e := config.Entry{Name: "agent", Source: config.Source{
		Kind: config.SourceDockerfile, Repo: "acme/app", Path: "docker/agent.Dockerfile",
	}}
	p := &fake.Provider{Repo: "acme/app", State: fake.State{Files: map[string]string{"docker/agent.Dockerfile": first}}}
	open := catalogOpen(p)
	if err := writeCatalogDockerfile(ctx, b, open, gitprov.KindGitHub, e); err != nil {
		t.Fatalf("writeCatalogDockerfile: %v", err)
	}
	key, err := config.CatalogDockerfileKey("agent")
	if err != nil {
		t.Fatal(err)
	}
	data, gen, err := b.Read(ctx, key)
	if err != nil || string(data) != first {
		t.Fatalf("the copy is %q (%v), want %q", data, err, first)
	}
	if err := writeCatalogDockerfile(ctx, b, open, gitprov.KindGitHub, e); err != nil {
		t.Fatalf("republishing wrote %v", err)
	}
	if _, again, err := b.Read(ctx, key); err != nil || again != gen {
		t.Fatalf("an unchanged definition rewrote the copy: generation %d, want %d (%v)", again, gen, err)
	}
	const second = first + "RUN echo changed\n"
	p.State.Files["docker/agent.Dockerfile"] = second
	if err := writeCatalogDockerfile(ctx, b, open, gitprov.KindGitHub, e); err != nil {
		t.Fatalf("republishing a changed definition: %v", err)
	}
	if data, _, err := b.Read(ctx, key); err != nil || string(data) != second {
		t.Fatalf("the changed definition was not written: %q (%v)", data, err)
	}
}

// TestCatalogCopyReadsTheDockerfileAndTheContextAtOneCommit: the copy resolves
// the entry's branch to one commit and reads both the Dockerfile and the
// context tree at it (D1394, D1395). The fake answers the branch one tree and
// the commit it resolves to another, so a copy that read at the branch — the
// ref read this replaces — stages the branch's bytes and fails here.
func TestCatalogCopyReadsTheDockerfileAndTheContextAtOneCommit(t *testing.T) {
	ctx := context.Background()
	const (
		atCommit = "FROM example.invalid/base\nRUN echo the commit\n"
		atBranch = "FROM example.invalid/base\nRUN echo the branch\n"
		ctxFile  = "the context the commit holds\n"
		ctxOther = "the context the branch holds\n"
	)
	// A full commit, which is what the copy stages and what a build fetches.
	const sha = "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
	b := gcpfake.NewGCS(t).Bucket(t, "fugaro-runs-proj-1234")
	e := config.Entry{Name: "agent", Source: config.Source{
		Kind: config.SourceDockerfile, Repo: "acme/app", Path: "docker/agent.Dockerfile",
		Branch: "main", Context: "ci",
	}}
	open := catalogOpen(&fake.Provider{Repo: "acme/app", State: fake.State{
		Branches: map[string]map[string]string{"main": {
			"docker/agent.Dockerfile": atBranch,
			"ci/Dockerfile":           ctxOther,
		}},
		Commits: map[string]string{"main": sha},
		Trees: map[string]map[string]string{sha: {
			"docker/agent.Dockerfile": atCommit,
			"ci/Dockerfile":           ctxFile,
		}},
	}})
	if err := writeCatalogDockerfile(ctx, b, open, gitprov.KindGitHub, e); err != nil {
		t.Fatalf("writeCatalogDockerfile: %v", err)
	}
	if got := catalogObject(t, b, config.CatalogDockerfileKey, "agent"); got != atCommit {
		t.Fatalf("the copy is %q, want the Dockerfile the resolved commit holds (%q)", got, atCommit)
	}
	if got := catalogObject(t, b, config.CatalogContextCommitKey, "agent"); got != sha {
		t.Fatalf("the staged commit is %q, want %q", got, sha)
	}
	if want := contextDigestOf(map[string]string{"Dockerfile": ctxFile}); catalogObject(t, b, config.CatalogContextHashKey, "agent") != want {
		t.Fatalf("the staged context hash is not the digest of the commit's context tree")
	}
}

// contextCopy is the fake provider and bucket a copy of one context entry is
// exercised against, with the two files the entry's commit holds under "ci".
func contextCopy(t *testing.T) (*fake.Provider, *blobx.Bucket, config.Entry) {
	t.Helper()
	const sha = "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
	const def = "FROM example.invalid/base\nRUN true\n"
	p := &fake.Provider{Repo: "acme/app", State: fake.State{
		Branches: map[string]map[string]string{"main": {"docker/agent.Dockerfile": def}},
		Commits:  map[string]string{"main": sha},
		Trees: map[string]map[string]string{sha: {
			"docker/agent.Dockerfile": def,
			"ci/Dockerfile":           "FROM example.invalid/base\n",
			"ci/src/main.go":          "package main\n",
		}},
	}}
	e := config.Entry{Name: "agent", Source: config.Source{
		Kind: config.SourceDockerfile, Repo: "acme/app", Path: "docker/agent.Dockerfile",
		Branch: "main", Context: "ci",
	}}
	return p, gcpfake.NewGCS(t).Bucket(t, "fugaro-runs-proj-1234"), e
}

// contextTree is the tree the copy above reads, keyed by the path the context
// digest is taken over: relative to the context root.
func contextTree() map[string]string {
	return map[string]string{"Dockerfile": "FROM example.invalid/base\n", "src/main.go": "package main\n"}
}

// TestCatalogCopyStagesTheContextHash: the object the check compares is the
// digest of the context tree, and it is written with the read-compare-write
// rule the Dockerfile copy has — a second copy of an unchanged tree leaves the
// object and its generation alone, and a tree whose file changed stages a
// different value.
func TestCatalogCopyStagesTheContextHash(t *testing.T) {
	ctx := context.Background()
	p, b, e := contextCopy(t)
	open := catalogOpen(p)
	if err := writeCatalogDockerfile(ctx, b, open, gitprov.KindGitHub, e); err != nil {
		t.Fatalf("writeCatalogDockerfile: %v", err)
	}
	key, err := config.CatalogContextHashKey("agent")
	if err != nil {
		t.Fatal(err)
	}
	data, gen, err := b.Read(ctx, key)
	if err != nil {
		t.Fatalf("reading the staged context hash: %v", err)
	}
	if want := contextDigestOf(contextTree()); string(data) != want {
		t.Fatalf("the staged context hash is %q, want the digest of the tree %q", data, want)
	}
	if err := writeCatalogDockerfile(ctx, b, open, gitprov.KindGitHub, e); err != nil {
		t.Fatalf("republishing an unchanged context: %v", err)
	}
	if _, again, err := b.Read(ctx, key); err != nil || again != gen {
		t.Fatalf("an unchanged context rewrote the hash: generation %d, want %d (%v)", again, gen, err)
	}
	// The commit the Dockerfile and the context were read at is staged beside
	// it, and it is the commit the branch resolved to.
	if got := catalogObject(t, b, config.CatalogContextCommitKey, "agent"); got != p.State.Commits["main"] {
		t.Errorf("the staged commit is %q, want %q", got, p.State.Commits["main"])
	}
	p.State.Trees[p.State.Commits["main"]]["ci/src/main.go"] = "package other\n"
	if err := writeCatalogDockerfile(ctx, b, open, gitprov.KindGitHub, e); err != nil {
		t.Fatalf("republishing a changed context: %v", err)
	}
	if data, _, err := b.Read(ctx, key); err != nil || string(data) == contextDigestOf(contextTree()) {
		t.Fatalf("a context whose file changed staged %q (%v)", data, err)
	}
}

// TestCatalogCopyBoundsTheContext: a context over the bound is refused by the
// entry's name before a build is paid for (D1398), and the copy writes nothing
// at all — not the Dockerfile either, because the definition and the context
// are resolved before either is staged.
func TestCatalogCopyBoundsTheContext(t *testing.T) {
	ctx := context.Background()
	p, b, e := contextCopy(t)
	tree := p.State.Trees[p.State.Commits["main"]]
	for i := 0; i <= catalog.MaxContextFiles; i++ {
		tree[fmt.Sprintf("ci/file-%05d", i)] = "x"
	}
	err := writeCatalogDockerfile(ctx, b, catalogOpen(p), gitprov.KindGitHub, e)
	if !errors.Is(err, gitprov.ErrTreeTooLarge) {
		t.Fatalf("err = %v, want the bound's own error", err)
	}
	if !strings.Contains(err.Error(), "agent") {
		t.Errorf("the refusal %q does not name the entry", err)
	}
	for _, key := range []func(string) (string, error){config.CatalogDockerfileKey, config.CatalogContextCommitKey, config.CatalogContextHashKey} {
		k, err := key("agent")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := b.Read(ctx, k); !errors.Is(err, blobx.ErrNotExist) {
			t.Errorf("a refused context staged %s: %v", k, err)
		}
	}
}

// TestCatalogCopyWithoutAContextIsUnchanged: an entry that names no context
// stages exactly the object it staged before the key existed — no commit and
// no digest — and its build still records the checkout's own HEAD (F23).
func TestCatalogCopyWithoutAContextIsUnchanged(t *testing.T) {
	ctx := context.Background()
	// The copy half, on its own: one object, and neither of the two the
	// context key added.
	const def = "FROM example.invalid/base\nRUN true\n"
	b := gcpfake.NewGCS(t).Bucket(t, "fugaro-runs-proj-1234")
	e := config.Entry{Name: "agent", Source: config.Source{
		Kind: config.SourceDockerfile, Repo: "acme/app", Path: "docker/agent.Dockerfile", Branch: "main",
	}}
	open := catalogOpen(&fake.Provider{Repo: "acme/app", State: fake.State{
		Branches: map[string]map[string]string{"main": {"docker/agent.Dockerfile": def}},
		Commits:  map[string]string{"main": "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"},
	}})
	if err := writeCatalogDockerfile(ctx, b, open, gitprov.KindGitHub, e); err != nil {
		t.Fatalf("writeCatalogDockerfile: %v", err)
	}
	if got := catalogObject(t, b, config.CatalogDockerfileKey, "agent"); got != def {
		t.Fatalf("the copy is %q, want %q", got, def)
	}
	for _, key := range []func(string) (string, error){config.CatalogContextCommitKey, config.CatalogContextHashKey} {
		k, err := key("agent")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := b.Read(ctx, k); !errors.Is(err, blobx.ErrNotExist) {
			t.Errorf("an entry with no context staged %s: %v", k, err)
		}
	}

	// And the build's own record: the entry's commit is still the checkout's
	// HEAD, which is the value a context entry's is not.
	fb, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildYAML)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	f.sm.AllowAccess()
	putSecretID(t, f.sm, mustSlug(gitprov.KindBitbucket, "acme/app"), config.BitbucketSecret, []byte(catalogTokenPlaceholder))
	if _, stderr, err := executeBuild(t, "image", "build", "base", "--base", "b:1"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if got, _ := catalogCopy(t, gcs, "base"); got != catalogBuildFetched {
		t.Errorf("the copy is %q, want the definition the repository holds", got)
	}
	commit, _ := headCommit(t)
	if env := catalogRecordEnv(t, fb); !slices.Contains(env, "COMMIT="+commit) {
		t.Errorf("the record's env is %v, want the checkout's HEAD (%s)", env, commit)
	}
}

// catalogRecordEnv is the record step's env: the values the build's own record
// of what it built is written from.
func catalogRecordEnv(t *testing.T, fb *gcpfake.Build) []string {
	t.Helper()
	step := catalogBuildStep(t, fb, "catalog-record")
	if step == nil {
		t.Fatal("the build has no record step")
	}
	raw, _ := step["env"].([]any)
	env := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		env = append(env, s)
	}
	return env
}

// catalogObject is the string a catalog key holds for entry, or a failure:
// every test here reads back what the copy staged rather than what it resolved.
func catalogObject(t *testing.T, b *blobx.Bucket, key func(string) (string, error), entry string) string {
	t.Helper()
	k, err := key(entry)
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := b.Read(context.Background(), k)
	if err != nil {
		t.Fatalf("reading %s: %v", k, err)
	}
	return string(data)
}

// contextDigestOf is the digest of a context tree written out the way the plan
// defines it — a sha256 over the tree's paths and blobs, in path order — so a
// test states the definition instead of asking the code for its own answer.
func contextDigestOf(files map[string]string) string {
	h := sha256.New()
	for _, p := range slices.Sorted(maps.Keys(files)) {
		sum := sha256.Sum256([]byte(files[p]))
		fmt.Fprintf(h, "%s\x00%x\n", p, sum)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestCatalogInlineDefinitionIsWrittenFromState: an inline entry needs no
// repository at all — its bytes are written from project state — and
// nothing opens a provider for it.
func TestCatalogInlineDefinitionIsWrittenFromState(t *testing.T) {
	ctx := context.Background()
	const def = "FROM example.invalid/base\nRUN true\n"
	b := gcpfake.NewGCS(t).Bucket(t, "fugaro-runs-proj-1234")
	e := config.Entry{Name: "inline-agent", Source: config.Source{Kind: config.SourceInline, Inline: def}}
	open := func(context.Context, string, string) (gitprov.Provider, []string, error) {
		t.Fatal("an inline definition opened a provider")
		return nil, nil, nil
	}
	if err := writeCatalogDockerfile(ctx, b, open, gitprov.KindGitHub, e); err != nil {
		t.Fatalf("writeCatalogDockerfile: %v", err)
	}
	key, err := config.CatalogDockerfileKey("inline-agent")
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := b.Read(ctx, key)
	if err != nil || string(got) != def {
		t.Fatalf("the copy is %q (%v), want %q", got, err, def)
	}
	// An entry with no Dockerfile to build (a prebuilt image, PIC5's kind)
	// is refused rather than copied.
	if err := writeCatalogDockerfile(ctx, b, open, gitprov.KindGitHub, config.Entry{
		Name: "prebuilt", Source: config.Source{Kind: config.SourceImage, Image: "ghcr.io/acme/app:1.2"},
	}); err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("a prebuilt image entry was copied: %v", err)
	}
	if _, _, err := b.Read(ctx, "builds/catalog/prebuilt/Dockerfile"); err == nil {
		t.Fatalf("an image entry wrote a Dockerfile: %v", err)
	}
}
