package imagecheck

import (
	"strings"
	"testing"
)

// baseDigest is a base image digest of the shape a record holds, and not a
// real one: the key never reads the registry.
var baseDigest = "sha256:" + strings.Repeat("a", 64)

// resolvedWeb is the set a node repository's key files reduce to: two
// packages and one toolchain pin on one base.
func resolvedWeb() Resolved {
	return Resolved{
		BaseRef:    "web",
		BaseDigest: baseDigest,
		Packages:   []string{"left-pad@1.3.0", "react@18.3.1"},
		Tools:      []string{"node@22.11.0"},
	}
}

// TestKeyIdenticalForIdenticalDependencySets: the whole point of the key.
// Two repositories whose resolved sets are the same facts — the same
// packages at the same versions, the same toolchain, the same base — are
// one key, whatever order their files name those facts in and however many
// times a file names one. Order and repetition are not facts (§2.4).
func TestKeyIdenticalForIdenticalDependencySets(t *testing.T) {
	want := Key(resolvedWeb())
	for _, c := range []struct {
		name string
		got  Resolved
	}{
		{"the same facts in another order", Resolved{
			BaseRef:    "web",
			BaseDigest: baseDigest,
			Packages:   []string{"react@18.3.1", "left-pad@1.3.0"},
			Tools:      []string{"node@22.11.0"},
		}},
		{"the same fact named twice", Resolved{
			BaseRef:    "web",
			BaseDigest: baseDigest,
			Packages:   []string{"react@18.3.1", "left-pad@1.3.0", "react@18.3.1"},
			Tools:      []string{"node@22.11.0", "node@22.11.0"},
		}},
		{"the same facts, toolchain first", Resolved{
			Tools:      []string{"node@22.11.0"},
			Packages:   []string{"react@18.3.1", "left-pad@1.3.0"},
			BaseRef:    "web",
			BaseDigest: baseDigest,
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Key(c.got); got != want {
				t.Errorf("Key = %s, want %s (the same set)", got, want)
			}
		})
	}
	if want != Key(resolvedWeb()) {
		t.Error("Key is not stable across calls")
	}
}

// TestKeyChangesWithAnyLockfile: a package added to the set — whichever
// ecosystem's key file it came from, which is not a fact the key sees — is
// a different set and so a different key. A set is never a subset's key.
func TestKeyChangesWithAnyLockfile(t *testing.T) {
	base := resolvedWeb()
	keys := map[string]string{"the set itself": Key(base)}
	for _, p := range []string{"lodash@4.17.21", "golang.org/x/text@0.14.0", "certifi@2024.8.30", "serde@1.0.210"} {
		got := base
		got.Packages = append([]string{p}, base.Packages...)
		keys[p] = Key(got)
	}
	for p, k := range keys {
		if p == "the set itself" {
			continue
		}
		if k == keys["the set itself"] {
			t.Errorf("adding %s left the key at %s", p, k)
		}
	}
}

// TestKeyIgnoresOrderCommentsFormattingRepoNameAndCommands: the canonical
// form is §2.4's text and nothing else. A repository's name, its workflow
// and its build and test commands have no line and no field — the type is
// the proof: Resolved cannot carry one — and a set's facts are sorted and
// deduplicated, so no file's order, comments or formatting reaches the
// bytes the hash is taken over.
func TestKeyIgnoresOrderCommentsFormattingRepoNameAndCommands(t *testing.T) {
	got := canonical(Resolved{
		BaseRef:    "web",
		BaseDigest: baseDigest,
		Packages:   []string{"react@18.3.1", "left-pad@1.3.0", "react@18.3.1"},
		Tools:      []string{"node@22.11.0"},
	})
	want := "key v1\n" +
		"base:web@" + baseDigest + "\n" +
		"pkg:left-pad@1.3.0\n" +
		"pkg:react@18.3.1\n" +
		"tool:node@22.11.0\n"
	if got != want {
		t.Errorf("canonical form =\n%q\nwant\n%q", got, want)
	}
	if Key(resolvedWeb()) != hashOf([]byte(want)) {
		t.Error("Key is not the SHA-256 of the canonical form")
	}
}

// TestKeyChangesWithVersionToolchainOrBase: each of the key's other three
// inputs is an input. A version move is a new set, and so is an added or
// moved toolchain pin and a different base — the base by its ref and by its
// digest, since a rebuilt base under one ref is a different image.
func TestKeyChangesWithVersionToolchainOrBase(t *testing.T) {
	base := resolvedWeb()
	for _, c := range []struct {
		name string
		got  Resolved
	}{
		{"a package version", Resolved{BaseRef: "web", BaseDigest: baseDigest, Packages: []string{"left-pad@1.3.1", "react@18.3.1"}, Tools: []string{"node@22.11.0"}}},
		{"a second toolchain pin", Resolved{BaseRef: "web", BaseDigest: baseDigest, Packages: base.Packages, Tools: []string{"node@22.11.0", "python@3.12.7"}}},
		{"a toolchain version", Resolved{BaseRef: "web", BaseDigest: baseDigest, Packages: base.Packages, Tools: []string{"node@22.12.0"}}},
		{"the base ref", Resolved{BaseRef: "beta", BaseDigest: baseDigest, Packages: base.Packages, Tools: base.Tools}},
		{"the base digest", Resolved{BaseRef: "web", BaseDigest: "sha256:" + strings.Repeat("b", 64), Packages: base.Packages, Tools: base.Tools}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Key(c.got); got == Key(base) {
				t.Errorf("%s left the key at %s", c.name, got)
			}
		})
	}
}
