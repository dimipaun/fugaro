package config

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Catalog is a project's image catalog (spec.md §Catalog entry): a handful of
// named images a person curates by hand, which the project's jobs run. It is
// project state, not repository state — a repository names an entry
// (Workflow.ImageName) or the tags it needs (Workflow.Needs) — and it travels
// in the published policy node under image_catalog (D1140), copied there from
// the local configuration `fugaro update` already holds.
//
// The Dockerfile bytes are not here: they are copied into the project's
// bucket before a build and the entry's source holds the pointer (D1140,
// PIC3). The tags and the json tags are both written because the same value
// is decoded from the local YAML and from the node's JSON.
type Catalog struct {
	// Entries are the catalog's images. Each is named, so the list is where
	// uniqueness is checked and not where it is structural: a name has to be
	// refused with a message naming the two entries that collide
	// (reconciliation §3.1 picks between candidates by name).
	Entries []Entry `yaml:"entries,omitempty" json:"entries,omitempty"`
}

// Entry is one named image of a project's catalog (spec.md §Catalog entry).
type Entry struct {
	// Name is the entry's name, unique in the catalog. It reaches a registry
	// tag, a bucket key and the sentences a selection prints, so its grammar
	// is an allowlist rather than a denylist (catalogNameRE).
	Name string `yaml:"name" json:"name"`
	// Tags are what the image provides — node, postgres, redis,
	// firebase-emulator, docker — and are what a repository's needs: is
	// matched against (reconciliation §3.1: a candidate covers the needed set
	// iff every needed tag is one of its tags). One of the allowlist
	// catalogTagRE states, so the tag a person reads as node is the tag node.
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	// Source is what the image is built from, one of three kinds.
	Source Source `yaml:"source" json:"source"`
	// Description is for a person reading `fugaro image ls`; optional, and
	// nothing reads it to decide anything.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Default marks the entry a job whose repositories name nothing runs
	// (spec.md §Repo configuration item 1, reconciliation §3.1 step 3). One
	// entry, or none.
	Default bool `yaml:"default,omitempty" json:"default,omitempty"`
}

// SourceKind is which of the three sources an entry has (spec.md §Catalog
// entry): a Dockerfile at a repo and path, an inline Dockerfile definition,
// or a reference to a prebuilt image.
type SourceKind string

const (
	// SourceDockerfile is a Dockerfile in a named repository, fetched with
	// the project's existing credentials and copied into the bucket before
	// the build (D1140, F3, PIC3).
	SourceDockerfile SourceKind = "dockerfile"
	// SourceInline is a Dockerfile definition held in project state, written
	// into the bucket before the build (D1140, PIC3).
	SourceInline SourceKind = "inline"
	// SourceImage is a prebuilt image reference: no build, the image is used
	// as it is.
	SourceImage SourceKind = "image"
)

// SourceKinds are the three kinds, in the order a refusal names them.
var SourceKinds = []string{string(SourceDockerfile), string(SourceInline), string(SourceImage)}

// catalogKindList is SourceKinds as a refusal reads it: every value the kind
// key accepts, so a kind added to that list is one the message names too.
func catalogKindList() string {
	last := len(SourceKinds) - 1
	return strings.Join(SourceKinds[:last], ", ") + " or " + SourceKinds[last]
}

// Source is what an entry is built from. The kind decides which of the
// fields may be set, and the rest are refused rather than ignored: two
// definitions in one source would leave which one a build reads unstated.
type Source struct {
	Kind SourceKind `yaml:"kind" json:"kind"`
	// Repo, Path and Branch are a SourceDockerfile's pointer: a repository of
	// the project (owner/name, the shape the local configuration keys its
	// repositories by) and a path inside it, at an optional branch. A
	// build fetches the file without a checkout of its own (spec.md §Where
	// Dockerfiles live).
	Repo   string `yaml:"repo,omitempty" json:"repo,omitempty"`
	Path   string `yaml:"path,omitempty" json:"path,omitempty"`
	Branch string `yaml:"branch,omitempty" json:"branch,omitempty"`
	// Inline is a SourceInline's Dockerfile definition, held in the catalog
	// itself.
	Inline string `yaml:"inline,omitempty" json:"inline,omitempty"`
	// Image is a SourceImage's reference to a prebuilt image. It reaches the
	// job spec, the sentences a selection writes and a listing, so it is one
	// container image reference within catalogImageRE, and no longer than
	// maxCatalogImageRefLen.
	Image string `yaml:"image,omitempty" json:"image,omitempty"`
}

const (
	// maxCatalogTagLen bounds a tag, and maxCatalogTags how many one entry or
	// one needs: may list. A tag is compared for equality by the tag-cover
	// rule and printed in the sentences a selection and a refusal write, so
	// neither list is unbounded.
	maxCatalogTagLen = 64
	maxCatalogTags   = 32
	// maxCatalogDescriptionLen bounds an entry's description, which a listing
	// prints to a terminal.
	maxCatalogDescriptionLen = 200
	// maxCatalogImageRefLen bounds a source image reference. A reference is
	// written into the job spec and the sentences a selection writes, and 255
	// is the length a registry accepts for a repository name, so no reference
	// a registry would serve is refused by it.
	maxCatalogImageRefLen = 255
	// maxCatalogInlineBytes bounds an inline Dockerfile definition: it
	// travels in the published policy node, which every launcher and every run
	// reads, so a definition that is not a Dockerfile's size must be refused
	// where it is written rather than published.
	maxCatalogInlineBytes = 64 << 10
)

// catalogNameRE is a catalog entry's name. It is ProjectNameRE's shape — 1 to
// 40 of a-z, 0-9 and '-', starting and ending with a letter or digit — and it
// is deliberately the same rule, because a name reaches a tag of an image in
// the shared registry and a key of the project's bucket as well as the
// messages a person reads.
var catalogNameRE = ProjectNameRE

var (
	// catalogNameMessage is the refusal of a name outside catalogNameRE.
	catalogNameMessage = "must be a catalog entry name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit"
	// catalogTagRE is the shape a tag must have: an allowlist of 1 to
	// maxCatalogTagLen lower-case letters, digits, '.', '_' and '-', the first
	// a letter or a digit. It is an allowlist and not "no whitespace, no
	// control character" because a tag is compared for equality by the
	// tag-cover rule and printed in the sentences a selection writes: under
	// the denylist, Node and node, no​de and node, ｎode and node and
	// node‮gnih and node all name different tags that a person reads as
	// the same one, so a repository's needs: could fail to match an entry
	// whose tag it appears to spell.
	catalogTagRE = regexp.MustCompile(fmt.Sprintf("^[a-z0-9][a-z0-9._-]{0,%d}$", maxCatalogTagLen-1))
	// catalogTagMessage is the refusal of a tag outside catalogTagRE. It does
	// not repeat the tag: a refusal that echoed one would put whatever it
	// holds, an escape sequence among it, into a log (validateKeyFiles' rule).
	catalogTagMessage = fmt.Sprintf("must be a tag of 1 to %d of a-z, 0-9, '.', '_' and '-', starting with a letter or a digit", maxCatalogTagLen)
	// catalogImageRE is a container image reference: an optional registry host
	// with an optional port, a repository path of lower-case components, an
	// optional tag and an optional sha256 digest. It is an allowlist for the
	// same reason the tag grammar is: a reference is written into the job spec
	// and the sentences a selection writes, and a value carrying a newline, an
	// escape sequence or a bidi override is one a person cannot read back as
	// the reference it is.
	catalogImageRE = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*(?::[A-Za-z0-9_][A-Za-z0-9._-]{0,127})?(?:@sha256:[a-f0-9]{64})?$`)
	// catalogImageMessage is the refusal of a reference outside catalogImageRE.
	// It names a shape rather than repeating the value, for the reason above.
	catalogImageMessage = "must be a container image reference such as ghcr.io/owner/image:1.2"
)

// ValidateCatalog checks a project's image catalog (spec.md §Catalog entry).
// p is the catalog's own path, such as "image_catalog": the rules are stated
// once here because two files carry a catalog — the local configuration and
// the published policy node — and a rule that lived in one of them would
// leave the other free to accept what the first refuses.
func ValidateCatalog(p string, c Catalog) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	names := make(map[string]int, len(c.Entries))
	marked := -1
	for i, e := range c.Entries {
		ep := fmt.Sprintf("%s.entries[%d]", p, i)
		switch {
		case !catalogNameRE.MatchString(e.Name):
			add(ep+".name", "%s", catalogNameMessage)
		default:
			if j, dup := names[e.Name]; dup {
				add(ep+".name", "duplicates entries[%d]", j)
			} else {
				names[e.Name] = i
			}
		}
		validateCatalogTags(ep+".tags", e.Tags, add)
		validateCatalogSource(ep+".source", e.Source, add)
		// A listing prints a description on one line, so it holds no tab and
		// no newline either: badKeyRune is that rule, and it is the same one
		// every value a person reads a line at a time is held to.
		if strings.ContainsFunc(e.Description, badKeyRune) {
			add(ep+".description", "must hold no control character")
		} else if len(e.Description) > maxCatalogDescriptionLen {
			add(ep+".description", "must be at most %d characters", maxCatalogDescriptionLen)
		}
		if !e.Default {
			continue
		}
		// The default is the entry a repository that names nothing runs, so
		// two marked entries would make that choice depend on catalog order
		// (reconciliation §3.1 step 3: the tag counts are never compared for
		// an empty needed set).
		if marked >= 0 {
			add(ep+".default", "another entry is already the default (entries[%d]); a catalog marks one entry as its default, or none", marked)
			continue
		}
		marked = i
	}
	return ps
}

// validateCatalogTags checks one list of tags — an entry's tags: or a
// repository's needs: — whose entries are path[i]. The rule is one statement
// for both lists because the two are compared to each other: a needs: tag
// that no tags: entry could hold would be a set no candidate ever covers.
func validateCatalogTags(path string, tags []string, add func(path, format string, args ...any)) {
	if len(tags) > maxCatalogTags {
		add(path, "must have at most %d entries", maxCatalogTags)
	}
	seen := make(map[string]int, len(tags))
	for i, tag := range tags {
		tp := fmt.Sprintf("%s[%d]", path, i)
		if !catalogTagRE.MatchString(tag) {
			add(tp, "%s", catalogTagMessage)
			continue
		}
		if j, dup := seen[tag]; dup {
			add(tp, "duplicates %s[%d]", path, j)
			continue
		}
		seen[tag] = i
	}
}

// validateCatalogSource checks an entry's source against its kind. The kind's
// own fields are required and the others are refused, so one source states
// one definition; the empty ones mean the same as unset everywhere else in
// this package.
func validateCatalogSource(path string, s Source, add func(path, format string, args ...any)) {
	switch s.Kind {
	case SourceDockerfile:
		switch {
		case s.Repo == "":
			add(path+".repo", "is required for a dockerfile source")
		case !repoSlugRE.MatchString(s.Repo) || hasDotSegment(s.Repo):
			add(path+".repo", "must be the owner/name of a repository of this project")
		}
		if s.Path == "" {
			add(path+".path", "is required for a dockerfile source")
		} else if msg := catalogPathMessage(s.Path); msg != "" {
			add(path+".path", "%s", msg)
		}
		if s.Branch != "" && !validBranchName(s.Branch) {
			add(path+".branch", "must be a git branch name")
		}
		addCatalogWrongKind(path, s, SourceDockerfile, add)
	case SourceInline:
		if s.Inline == "" {
			add(path+".inline", "is required for an inline source")
		} else if len(s.Inline) > maxCatalogInlineBytes {
			add(path+".inline", "must be at most %d bytes", maxCatalogInlineBytes)
		}
		addCatalogWrongKind(path, s, SourceInline, add)
	case SourceImage:
		switch {
		case s.Image == "":
			add(path+".image", "is required for an image source")
		case utf8.RuneCountInString(s.Image) > maxCatalogImageRefLen:
			add(path+".image", "must be at most %d characters", maxCatalogImageRefLen)
		case !catalogImageRE.MatchString(s.Image):
			add(path+".image", "%s", catalogImageMessage)
		}
		addCatalogWrongKind(path, s, SourceImage, add)
	default:
		add(path+".kind", "must be %s", catalogKindList())
	}
}

// addCatalogWrongKind refuses the fields that belong to another kind: repo,
// path and branch to a dockerfile source, inline to an inline one and image
// to an image one. The message names the kind that owns the field and the
// kind this source is, in that order, rather than phrasing the second as an
// alternative ("not a inline one"): two of the three kind names begin with a
// vowel, so no single indefinite article reads correctly in front of all
// three, and "the" reads for every one of them.
func addCatalogWrongKind(path string, s Source, kind SourceKind, add func(path, format string, args ...any)) {
	for _, f := range []struct {
		key   string
		set   bool
		owner SourceKind
	}{
		{"repo", s.Repo != "", SourceDockerfile},
		{"path", s.Path != "", SourceDockerfile},
		{"branch", s.Branch != "", SourceDockerfile},
		{"inline", s.Inline != "", SourceInline},
		{"image", s.Image != "", SourceImage},
	} {
		if f.owner == kind || !f.set {
			continue
		}
		add(path+"."+f.key, "belongs to the %s source; this source is %s", f.owner, kind)
	}
}

// catalogPathMessage names what is wrong with a dockerfile source's path, ""
// when nothing is. It is stricter than "inside the repository" alone: the
// path is fetched from a repository and read by a build, so it is one clean
// relative path, as it is (image.key_files' rules, stated for this key
// because the messages a repository reads must name this one).
func catalogPathMessage(f string) string {
	switch {
	case strings.HasPrefix(f, "/"):
		return "must be a relative path inside the repository"
	case slices.Contains(strings.Split(f, "/"), ".."):
		return "must not hold a .. segment: the path is read inside the repository it names"
	case f == ".":
		return "must name a Dockerfile, not the repository root"
	case path.Clean(f) != f:
		return "must be a clean path: no trailing slash, no // and no . segment"
	}
	return ""
}
