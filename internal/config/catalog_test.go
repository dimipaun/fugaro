package config

import (
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
