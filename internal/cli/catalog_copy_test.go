package cli

import (
	"context"
	"strings"
	"testing"

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
