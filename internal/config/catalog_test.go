package config

import (
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// catalogOf decodes a catalog fixture the way the local configuration
// decodes one: strictly, so a misspelt key is the error it is in production
// and not a silently dropped entry (internal/localcfg decodes its whole
// file with KnownFields).
func catalogOf(t *testing.T, text string) Catalog {
	t.Helper()
	var c Catalog
	dec := yaml.NewDecoder(strings.NewReader(text))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return c
}

// TestCatalogEntryRejectsAnUnknownSourceKind pins the catalog's source
// grammar (spec.md §Catalog entry): a source is one of three kinds — a
// Dockerfile at a repo and path, an inline definition, or a prebuilt image —
// and a kind outside them is refused where it is written rather than
// reaching a build that has no step for it.
func TestCatalogEntryRejectsAnUnknownSourceKind(t *testing.T) {
	const msg = "must be dockerfile, inline or image"
	c := catalogOf(t, `
entries:
  - name: backend
    tags: [helm]
    source:
      kind: helm
      path: charts/api
`)
	ps := ValidateCatalog("image_catalog", c)
	if !hasProblem(ps, "image_catalog.entries[0].source.kind", msg, 0) {
		t.Fatalf("problems = %v, want one at image_catalog.entries[0].source.kind: %s", ps, msg)
	}
	// Each of the three kinds is a source, so a catalog of them is valid and
	// the refusal above is about the kind and nothing else.
	c = catalogOf(t, `
entries:
  - name: backend
    tags: [node, postgres]
    source: { kind: dockerfile, repo: acme/api, path: docker/agent.Dockerfile }
  - name: frontend
    tags: [node]
    source: { kind: inline, inline: "FROM node:24\n" }
  - name: legacy
    source: { kind: image, image: "example.invalid/legacy:1" }
`)
	if ps := ValidateCatalog("image_catalog", c); len(ps) > 0 {
		t.Fatalf("unexpected problems: %v", ps)
	}
}

// catalogWithTags is one entry of the given tags, built as a value rather than
// decoded from YAML: a tag under test may hold a bidi override, a zero-width
// space or a fullwidth letter, and a fixture carrying one of those in a YAML
// literal is a line no reader can tell from the tag it imitates.
func catalogWithTags(tags ...string) Catalog {
	return Catalog{Entries: []Entry{{
		Name:   "backend",
		Tags:   tags,
		Source: Source{Kind: SourceImage, Image: "example.invalid/backend:1"},
	}}}
}

// TestCatalogTagsAreAnAllowlist pins the tag grammar (spec.md §Catalog
// entry): 1 to 64 of a-z, 0-9, '.', '_' and '-', starting with a letter or a
// digit. It is an allowlist and not "no whitespace, no control character"
// because a tag is compared for equality by the tag-cover rule: an upper-case
// N, a zero-width space, a fullwidth letter or a bidi override is a tag a
// person reads as node and that no entry's tags: node will ever match, so a
// repository's needs: would be refused for a tag it appears to spell.
func TestCatalogTagsAreAnAllowlist(t *testing.T) {
	const msg = "must be a tag of 1 to 64"
	for _, tc := range []struct{ name, tag string }{
		{"upper case", "Node"},
		{"a zero-width space", "no​de"},
		{"a bidi override", "node‮gnih"},
		{"a fullwidth letter", "ｎode"},
		{"a space", "postgres redis"},
		{"a colon", "node:24"},
		{"a leading dash", "-node"},
		{"an empty tag", ""},
		{"over 64 characters", strings.Repeat("a", maxCatalogTagLen+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := ValidateCatalog("image_catalog", catalogWithTags(tc.tag))
			if !hasProblem(ps, "image_catalog.entries[0].tags[0]", msg, 0) {
				t.Fatalf("problems = %v, want one at image_catalog.entries[0].tags[0]: %s", ps, msg)
			}
		})
	}
	// The allowlist's own shapes are tags, at both ends of the length bound:
	// a tag is what an entry provides and what a repository asks for, so a
	// catalog of them is valid.
	for _, tag := range []string{"a", "node", "postgres", "firebase-emulator", "python3.12", "golang-1.24", strings.Repeat("a", maxCatalogTagLen)} {
		if ps := ValidateCatalog("image_catalog", catalogWithTags(tag)); len(ps) > 0 {
			t.Errorf("tag %q: unexpected problems: %v", tag, ps)
		}
	}
}

// TestCatalogEntryTagsRefuseADuplicate pins the entry-tags half of the
// duplicate rule (validateCatalogTags' needs: half is exercised through a
// workflow): two entries of one tag on one entry would make the tag-cover rule
// compare a set with a repeat in it, and the message names the first position
// so a person can find the pair.
func TestCatalogEntryTagsRefuseADuplicate(t *testing.T) {
	ps := ValidateCatalog("image_catalog", catalogWithTags("node", "postgres", "node"))
	if !hasProblem(ps, "image_catalog.entries[0].tags[2]", "duplicates image_catalog.entries[0].tags[0]", 0) {
		t.Fatalf("problems = %v, want the second node refused at its own index", ps)
	}
}

// catalogWithImage is one entry whose source is the given prebuilt image
// reference, built as a value for the reason catalogWithTags is.
func catalogWithImage(ref string) Catalog {
	return Catalog{Entries: []Entry{{
		Name:   "backend",
		Source: Source{Kind: SourceImage, Image: ref},
	}}}
}

// TestCatalogSourceImageIsAReference pins a prebuilt image's grammar (spec.md
// §Catalog entry): the reference is written into the job spec, a listing and
// the sentences a selection writes, so it is one container image reference,
// within maxCatalogImageRefLen. A newline with an escape sequence, a bidi
// override, a space or a command after the reference is a value no registry
// serves and no person can read back as the image it names; a hundred thousand
// bytes of valid characters is the same value at a size the node, the job spec
// and every reader of the run would carry.
func TestCatalogSourceImageIsAReference(t *testing.T) {
	const refMsg = "must be a container image reference"
	const lenMsg = "must be at most 255 characters"
	for _, tc := range []struct{ name, ref, msg string }{
		{"a newline and an escape sequence", "ghcr.io/acme/agent:1\n\x1b[31m", refMsg},
		{"a bidi override", "ghcr.io/acme/‮gnih:1", refMsg},
		{"a space", "ghcr.io/acme/my image:1", refMsg},
		{"an upper-case host", "GHCR.IO/acme/agent", refMsg},
		{"a command after the reference", "ghcr.io/acme/agent:1; rm -rf /", refMsg},
		{"over the length bound", strings.Repeat("a", maxCatalogImageRefLen+1), lenMsg},
		{"a hundred thousand bytes", strings.Repeat("a", 100000), lenMsg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := ValidateCatalog("image_catalog", catalogWithImage(tc.ref))
			if !hasProblem(ps, "image_catalog.entries[0].source.image", tc.msg, 0) {
				t.Fatalf("problems = %v, want one at image_catalog.entries[0].source.image: %s", ps, tc.msg)
			}
		})
	}
	// The shapes a registry serves are references: a bare name and tag, a
	// registry host, a host with a port, a path of several components, and a
	// digest pin.
	for _, ref := range []string{
		"node:24",
		"ghcr.io/acme/agent:1.2.3",
		"us-central1-docker.pkg.dev/project/repo/agent:2026-11-03",
		"localhost:5000/agent:1",
		"ghcr.io/acme/agent@sha256:" + strings.Repeat("a", 64),
	} {
		if ps := ValidateCatalog("image_catalog", catalogWithImage(ref)); len(ps) > 0 {
			t.Errorf("reference %q: unexpected problems: %v", ref, ps)
		}
	}
}

// catalogWithPath is one entry whose source is a Dockerfile at the given path,
// built as a value for the reason catalogWithTags is.
func catalogWithPath(p string) Catalog {
	return Catalog{Entries: []Entry{{
		Name:   "backend",
		Source: Source{Kind: SourceDockerfile, Repo: "acme/api", Path: p},
	}}}
}

// TestCatalogPathHoldsNoHostileText pins what a dockerfile source's path must
// not carry (F3): the path is fetched with the project's credentials and read
// by a build, and it is written into the messages a person reads, so a control
// character — a newline, a tab, a terminal escape sequence — or a path of ten
// thousand bytes is refused. The refusal names no part of the value: it would
// otherwise print the escape sequence it is refusing.
func TestCatalogPathHoldsNoHostileText(t *testing.T) {
	const ctlMsg = "must hold no control character"
	const lenMsg = "must be at most 200 characters"
	for _, tc := range []struct{ name, path, msg string }{
		{"an escape sequence", "docker/\x1b[31magent.Dockerfile", ctlMsg},
		{"a bidi override", "docker/‮gnih.Dockerfile", ctlMsg},
		{"a newline", "docker/agent\n.Dockerfile", ctlMsg},
		{"a tab", "docker/\tagent.Dockerfile", ctlMsg},
		{"over the length bound", strings.Repeat("a", maxCatalogPathLen+1), lenMsg},
		{"ten thousand bytes", "docker/" + strings.Repeat("a", 10000) + ".Dockerfile", lenMsg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := ValidateCatalog("image_catalog", catalogWithPath(tc.path))
			if !hasProblem(ps, "image_catalog.entries[0].source.path", tc.msg, 0) {
				t.Fatalf("problems = %v, want one at image_catalog.entries[0].source.path: %s", ps, tc.msg)
			}
			for _, p := range ps {
				if strings.Contains(p.Message, "\x1b") {
					t.Fatalf("the refusal echoes the value: %q", p.Message)
				}
			}
		})
	}
	// The paths a repository really writes are inside the bound and hold no
	// control character.
	for _, p := range []string{"Dockerfile", "docker/agent.Dockerfile", ".fugaro/web.Dockerfile", strings.Repeat("a", maxCatalogPathLen)} {
		if ps := ValidateCatalog("image_catalog", catalogWithPath(p)); len(ps) > 0 {
			t.Errorf("path %q: unexpected problems: %v", p, ps)
		}
	}
}

// TestCatalogDefaultIsOneEntryOrNone pins the default marker (spec.md
// §Catalog entry: "The project marks one entry as default"): a catalog marks
// one entry, or none, and two entries marked are refused because the choice
// a repository that names nothing runs would otherwise depend on catalog
// order (reconciliation §3.1 step 3).
func TestCatalogDefaultIsOneEntryOrNone(t *testing.T) {
	entries := func(defaults ...string) string {
		var b strings.Builder
		b.WriteString("entries:\n")
		for i, name := range []string{"backend", "frontend"} {
			b.WriteString("  - name: " + name + "\n")
			b.WriteString("    source: { kind: image, image: \"example.invalid/" + name + ":1\" }\n")
			if i < len(defaults) {
				b.WriteString("    default: " + defaults[i] + "\n")
			}
		}
		return b.String()
	}
	for _, tc := range []struct {
		name string
		text string
		ok   bool
	}{
		{"no entry marked", entries(), true},
		{"one entry marked", entries("true"), true},
		{"two entries marked", entries("true", "true"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := ValidateCatalog("image_catalog", catalogOf(t, tc.text))
			if got := len(ps) == 0; got != tc.ok {
				t.Fatalf("accepted = %v, want %v (problems: %v)", got, tc.ok, ps)
			}
			if !tc.ok && !hasProblem(ps, "image_catalog.entries[1].default", "already the default", 0) {
				t.Errorf("problems = %v, want the second marked entry refused at its own path", ps)
			}
		})
	}
}

// TestCatalogNameGrammarIsUniqueAndBounded pins a name's two rules: its
// grammar and length (one project curates these by hand, and a name reaches
// the job spec, a registry tag and a bucket key), and its uniqueness — the
// tag-cover algorithm picks between entries by name, so two entries of one
// name would make the choice ambiguous.
func TestCatalogNameGrammarIsUniqueAndBounded(t *testing.T) {
	const msg = "must be a catalog entry name"
	for _, tc := range []struct{ name, entry string }{
		{"upper case", "    name: Backend\n"},
		{"an underscore", "    name: back_end\n"},
		{"a leading dash", "    name: -backend\n"},
		{"over 40 characters", "    name: " + strings.Repeat("a", 41) + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := catalogOf(t, "entries:\n  - source: { kind: image, image: \"example.invalid/x:1\" }\n"+tc.entry)
			if ps := ValidateCatalog("image_catalog", c); !hasProblem(ps, "image_catalog.entries[0].name", msg, 0) {
				t.Fatalf("problems = %v, want one at image_catalog.entries[0].name: %s", ps, msg)
			}
		})
	}
	c := catalogOf(t, `
entries:
  - name: backend
    source: { kind: image, image: "example.invalid/backend:1" }
  - name: backend
    source: { kind: image, image: "example.invalid/other:1" }
`)
	if ps := ValidateCatalog("image_catalog", c); !hasProblem(ps, "image_catalog.entries[1].name", "duplicates entries[0]", 0) {
		t.Fatalf("problems = %v, want the second backend refused as a duplicate", ps)
	}
	// The bounds are the name's: the longest one the grammar allows is 40
	// characters, so a catalog may hold it.
	c = catalogOf(t, "entries:\n  - name: "+strings.Repeat("a", 40)+"\n    source: { kind: image, image: \"example.invalid/x:1\" }\n")
	if ps := ValidateCatalog("image_catalog", c); len(ps) > 0 {
		t.Fatalf("unexpected problems: %v", ps)
	}
}

// TestCatalogSourceNeedsItsOwnKindsFields pins the other two halves of the
// source grammar (spec.md §Catalog entry): a source states one definition, so
// a kind's own field is required and the fields of the other two kinds are
// refused rather than ignored — an inline entry with no definition and an
// image entry with no reference are both sources a build could not read.
func TestCatalogSourceNeedsItsOwnKindsFields(t *testing.T) {
	for _, tc := range []struct{ name, source, path, msg string }{
		{"inline with no definition", "{ kind: inline }",
			"image_catalog.entries[0].source.inline", "is required for an inline source"},
		{"image with no reference", "{ kind: image }",
			"image_catalog.entries[0].source.image", "is required for an image source"},
		{"inline carrying a dockerfile pointer", `{ kind: inline, inline: "FROM node:24\n", repo: acme/api }`,
			"image_catalog.entries[0].source.repo", "belongs to the dockerfile source; this source is inline"},
		{"image carrying a dockerfile pointer", `{ kind: image, image: "example.invalid/x:1", path: docker/x.Dockerfile }`,
			"image_catalog.entries[0].source.path", "belongs to the dockerfile source; this source is image"},
		{"dockerfile carrying an inline definition", `{ kind: dockerfile, repo: acme/api, path: docker/x.Dockerfile, inline: "FROM node:24\n" }`,
			"image_catalog.entries[0].source.inline", "belongs to the inline source; this source is dockerfile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := catalogOf(t, "entries:\n  - name: backend\n    source: "+tc.source+"\n")
			if ps := ValidateCatalog("image_catalog", c); !hasProblem(ps, tc.path, tc.msg, 0) {
				t.Fatalf("problems = %v, want one at %s: %s", ps, tc.path, tc.msg)
			}
		})
	}
}

// TestCatalogDefaultEntryIsTheMarkedOne pins CatalogDefaultEntry against the
// marker ValidateCatalog checks: the entry a repository that names nothing
// runs (spec.md §Repo configuration item 1, reconciliation §3.1 step 3), and
// no entry at all for a catalog that marks none.
func TestCatalogDefaultEntryIsTheMarkedOne(t *testing.T) {
	c := catalogOf(t, `
entries:
  - name: backend
    tags: [node]
    source: { kind: image, image: "example.invalid/backend:1" }
  - name: frontend
    default: true
    source: { kind: image, image: "example.invalid/frontend:1" }
`)
	e, ok := CatalogDefaultEntry(c)
	if !ok || e.Name != "frontend" {
		t.Fatalf("CatalogDefaultEntry = %+v, %v; want the marked frontend entry", e, ok)
	}
	c = catalogOf(t, `
entries:
  - name: backend
    source: { kind: image, image: "example.invalid/backend:1" }
`)
	if e, ok := CatalogDefaultEntry(c); ok || e.Name != "" {
		t.Fatalf("CatalogDefaultEntry = %+v, %v; want no entry for a catalog that marks none", e, ok)
	}
	if e, ok := CatalogDefaultEntry(Catalog{}); ok || e.Name != "" {
		t.Fatalf("CatalogDefaultEntry = %+v, %v; want no entry for an empty catalog", e, ok)
	}
}

// TestCatalogDockerfileSourceNeedsRepoAndPath pins the dockerfile source's
// grammar (F3, D1140): the pointer is a repository, a path inside it and an
// optional branch, so a build can fetch the file without a checkout of its
// own — both halves are required, and the path stays inside the repository
// it names.
func TestCatalogDockerfileSourceNeedsRepoAndPath(t *testing.T) {
	const msg = "is required for a dockerfile source"
	for _, tc := range []struct{ name, source, path string }{
		{"no repo", "{ kind: dockerfile, path: docker/agent.Dockerfile }", "image_catalog.entries[0].source.repo"},
		{"no path", "{ kind: dockerfile, repo: acme/api }", "image_catalog.entries[0].source.path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := catalogOf(t, "entries:\n  - name: backend\n    source: "+tc.source+"\n")
			if ps := ValidateCatalog("image_catalog", c); !hasProblem(ps, tc.path, msg, 0) {
				t.Fatalf("problems = %v, want one at %s: %s", ps, tc.path, msg)
			}
		})
	}
	c := catalogOf(t, `
entries:
  - name: backend
    source:
      kind: dockerfile
      repo: acme/api
      path: docker/agent.Dockerfile
      branch: release/1.x
`)
	if ps := ValidateCatalog("image_catalog", c); len(ps) > 0 {
		t.Fatalf("unexpected problems: %v", ps)
	}
}

// catalogContextYAML is one dockerfile entry whose source carries the given
// context, spelled as a document rather than as a value: the red step this
// test carries is the strict decode's, and only a document reaches it. The
// value is quoted, so a context holding a control character is written where a
// reader can see what it is.
func catalogContextYAML(context string) string {
	return "entries:\n  - name: backend\n    source:\n      kind: dockerfile\n      repo: acme/api\n      path: docker/agent.Dockerfile\n      context: " + strconv.Quote(context) + "\n"
}

// TestCatalogContextIsADirectoryPath pins the context grammar (D1391, D1392):
// the key names a directory of the source's own repository, so it is held to
// the path's rules, with the two exceptions naming a directory makes — the
// repository root is a legitimate context and a file has no root to name, and a
// directory is written with its own trailing slash and a file is not. The keys
// therefore disagree about those two values and about nothing else, and both
// sides of both disagreements are pinned here: the shared rules below are the
// path's own, cleanliness included.
func TestCatalogContextIsADirectoryPath(t *testing.T) {
	// The issue's own example value, and a directory of the repository spelled
	// the way a directory is written.
	for _, context := range []string{".", "ci/", "docker/context"} {
		if ps := ValidateCatalog("image_catalog", catalogOf(t, catalogContextYAML(context))); len(ps) > 0 {
			t.Errorf("context %q: unexpected problems: %v", context, ps)
		}
	}
	// The sibling key still refuses the two values a directory is allowed: a
	// context that reused its message would refuse the issue's own example
	// value, and one that dropped the path's cleanliness with it would take the
	// trailing slash as a licence for `a//b`.
	for _, f := range []string{".", "ci/"} {
		if msg := catalogPathMessage(f); msg == "" {
			t.Fatalf("catalogPathMessage accepts %q, so the two keys no longer disagree about it", f)
		}
	}
	const ctlMsg = "must hold no control character"
	const lenMsg = "must be at most 200 characters"
	const absMsg = "must be a relative path inside the repository"
	const dotdotMsg = "must not hold a .. segment: the context is read inside the repository it names"
	const cleanMsg = "must be a clean path: no // and no . segment"
	for _, tc := range []struct{ name, context, msg string }{
		{"a .. segment", "ci/../../etc", dotdotMsg},
		{"an absolute path", "/ci", absMsg},
		{"a control character", "ci\nDockerfile", ctlMsg},
		{"an escape sequence", "docker/\x1b[31mcontext", ctlMsg},
		{"over the length bound", strings.Repeat("a", maxCatalogPathLen+1), lenMsg},
		// The path's clean rule, held here too: the trailing slash a directory
		// is spelled with is the only part of it this key may drop.
		{"a . segment", "./ci", cleanMsg},
		{"an empty segment", "ci//maven", cleanMsg},
		{"two trailing slashes", "ci//", cleanMsg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := ValidateCatalog("image_catalog", catalogOf(t, catalogContextYAML(tc.context)))
			if !hasProblem(ps, "image_catalog.entries[0].source.context", tc.msg, 0) {
				t.Fatalf("problems = %v, want one at image_catalog.entries[0].source.context: %s", ps, tc.msg)
			}
			for _, p := range ps {
				if strings.Contains(p.Message, "\x1b") {
					t.Fatalf("the refusal echoes the value: %q", p.Message)
				}
			}
		})
	}
}

// TestCatalogContextBelongsToADockerfileSource: a context is read inside the
// repository the Dockerfile is in (D1393), so it is a dockerfile source's field
// and a source of another kind carrying one is refused rather than built
// without it.
func TestCatalogContextBelongsToADockerfileSource(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"inline", "      kind: inline\n      inline: \"FROM node:24\\n\"\n      context: ci\n"},
		{"image", "      kind: image\n      image: example.invalid/backend:1\n      context: ci\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := catalogOf(t, "entries:\n  - name: backend\n    source:\n"+tc.source)
			msg := "belongs to the dockerfile source; this source is " + tc.name
			if ps := ValidateCatalog("image_catalog", c); !hasProblem(ps, "image_catalog.entries[0].source.context", msg, 0) {
				t.Fatalf("problems = %v, want one at image_catalog.entries[0].source.context: %s", ps, msg)
			}
		})
	}
	if ps := ValidateCatalog("image_catalog", catalogOf(t, catalogContextYAML("ci"))); len(ps) > 0 {
		t.Errorf("a dockerfile source carrying a context: unexpected problems: %v", ps)
	}
}

// TestCatalogContextKeyRefusesANameOutsideTheGrammar: the two keys a context
// entry stages its objects under are built from an entry's name, so they refuse
// a name the Dockerfile's own key refuses — a name outside the grammar would
// name an object outside builds/catalog/<entry>/, which is the prefix the build
// and check accounts' grants are conditioned on.
func TestCatalogContextKeyRefusesANameOutsideTheGrammar(t *testing.T) {
	for _, name := range []string{"", "agent/../../etc", "../agent", "Agent", "agent.", strings.Repeat("a", 41)} {
		if _, err := CatalogDockerfileKey(name); err == nil {
			t.Fatalf("CatalogDockerfileKey(%q) was accepted, so the fixture is not a name outside the grammar", name)
		}
		for _, key := range []struct {
			what string
			f    func(string) (string, error)
		}{
			{"CatalogContextCommitKey", CatalogContextCommitKey},
			{"CatalogContextHashKey", CatalogContextHashKey},
		} {
			if _, err := key.f(name); err == nil {
				t.Errorf("%s(%q) was accepted, and CatalogDockerfileKey refuses it", key.what, name)
			}
		}
	}
	for _, tc := range []struct {
		what string
		f    func(string) (string, error)
		want string
	}{
		{"CatalogContextCommitKey", CatalogContextCommitKey, "builds/catalog/agent/source-commit"},
		{"CatalogContextHashKey", CatalogContextHashKey, "builds/catalog/agent/context-sha256"},
	} {
		got, err := tc.f("agent")
		if err != nil {
			t.Fatalf("%s(agent): %v", tc.what, err)
		}
		if got != tc.want {
			t.Errorf("%s(agent) = %q, want %q", tc.what, got, tc.want)
		}
	}
}
