// Package catalog resolves a project's image catalog (docs/project-image-
// catalog/spec.md) into what a build reads: the Dockerfile bytes of an
// entry's source, fetched from a repository through the project's existing
// git credentials or taken from the entry's own inline definition, and the
// selection among entries (PIC5).
package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// MaxDockerfileBytes bounds a resolved Dockerfile, whatever its source:
// gitprov.MaxContentsBytes, the ceiling a contents read already has, applied
// to an inline definition too. What is fetched is copied into the project's
// bucket and handed to a build, so a definition larger than this is refused
// where it is resolved rather than carried.
const MaxDockerfileBytes = gitprov.MaxContentsBytes

// Fetch resolves e's source into the Dockerfile bytes a catalog build reads
// (D1140, PIC3): a repo reference is read out of the named repository, at
// the named branch, through the provider the opener returns — the project's
// existing credentials, and no other access (F9) — and an inline definition
// is taken from the entry itself, with no repository involved.
//
// open and kind are the project's git provider opener and kind
// (gitprov.KindGitHub or KindBitbucket). A kind whose provider cannot read a
// file (gitprov.ContentsReader, which the Bitbucket adapter does not
// implement) is refused: an inline entry needs neither.
//
// The credential the opener returns is dropped here: nothing in this
// function writes it, returns it or puts it in a failure — what a caller
// prints is the entry's repository and path, and the file's own bytes. Those
// two are printed through pluginwire.Printable, the rule every value that
// reaches a log follows: the catalog's own validation is not this package's
// to rely on, and a name or a repository that carried an escape sequence
// would carry it into whatever reads the message.
func Fetch(ctx context.Context, open gitprov.Opener, kind string, e config.Entry) ([]byte, error) {
	switch e.Source.Kind {
	case config.SourceInline:
		return bounded(e, []byte(e.Source.Inline))
	case config.SourceDockerfile:
		return fetchDockerfile(ctx, open, kind, e)
	case config.SourceImage:
		return nil, fmt.Errorf("the catalog entry %s has no Dockerfile: its source is a prebuilt image, so there is nothing to build or copy", nameOf(e))
	default:
		return nil, fmt.Errorf("the catalog entry %s has no source kind this build knows (%s)", nameOf(e), strings.Join(config.SourceKinds, ", "))
	}
}

// fetchDockerfile reads a dockerfile source's file out of its repository.
//
// The repository, the path and the ref are checked before the provider is
// opened, not only where the catalog is validated: this is where all three
// reach an API path and a build, and a value that got past a validation is
// still not one to hand a provider (a path outside the repository, an
// owner/name that is not one, a ref the API would read as something else).
// The ref is held to config.ValidBranchName, the rule the catalog's own
// validation applies, so this guard tightens or loosens with that one
// instead of being a second, weaker rule that reads like a safety net.
func fetchDockerfile(ctx context.Context, open gitprov.Opener, kind string, e config.Entry) ([]byte, error) {
	s := e.Source
	if !validRepo(s.Repo) {
		return nil, fmt.Errorf("the catalog entry %s: the source's repository is not the owner/name of a repository of this project", nameOf(e))
	}
	if !gitprov.ValidRepoPath(s.Path) {
		return nil, fmt.Errorf("the catalog entry %s: the source's path must be a relative path inside %s, with no .. segment", nameOf(e), pluginwire.Printable(s.Repo))
	}
	if s.Branch != "" && !config.ValidBranchName(s.Branch) {
		return nil, fmt.Errorf("the catalog entry %s: the source's branch must be a git branch name", nameOf(e))
	}
	if open == nil {
		return nil, fmt.Errorf("the catalog entry %s: no git provider was given to read %s with", nameOf(e), pluginwire.Printable(s.Repo))
	}
	p, _, err := open(ctx, kind, s.Repo)
	if err != nil {
		return nil, fmt.Errorf("the catalog entry %s: opening the provider of %s: %w", nameOf(e), pluginwire.Printable(s.Repo), err)
	}
	r, ok := p.(gitprov.ContentsReader)
	if !ok {
		return nil, fmt.Errorf("the catalog entry %s: the %s provider cannot read a file out of %s, so its Dockerfile cannot be fetched; use an inline definition or a repository on a provider that can", nameOf(e), pluginwire.Printable(kind), pluginwire.Printable(s.Repo))
	}
	data, err := r.Contents(ctx, s.Path, s.Branch)
	if err != nil {
		// The path is printed through pluginwire.Printable like every other
		// value that reaches a message here: gitprov.ValidRepoPath lets a
		// formatting (bidi) character through, so the path a person reads
		// back is escaped rather than taken at its word.
		return nil, fmt.Errorf("the catalog entry %s: reading %s from %s: %w", nameOf(e), pluginwire.Printable(s.Path), pluginwire.Printable(s.Repo), err)
	}
	return bounded(e, data)
}

// bounded refuses a definition larger than MaxDockerfileBytes. It is applied
// to a fetched file as well as to an inline one, because a provider's own
// ceiling is not this package's to rely on: what is bounded here is what
// this package hands on.
func bounded(e config.Entry, data []byte) ([]byte, error) {
	if len(data) > MaxDockerfileBytes {
		return nil, fmt.Errorf("the catalog entry %s: its Dockerfile is %d bytes, more than the %d a definition may be", nameOf(e), len(data), MaxDockerfileBytes)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("the catalog entry %s: its Dockerfile is empty", nameOf(e))
	}
	return data, nil
}

// nameOf is e's name for a message this package writes, made safe to print
// (pluginwire.Printable): the name is validated where a catalog is
// published, and this package is not that place, so it prints the name the
// way every value that reaches a log is printed rather than trusting it.
func nameOf(e config.Entry) string { return pluginwire.Printable(e.Name) }

// validRepo reports whether repo is the owner/name of a repository: two
// non-empty segments that name a repository rather than a path above the
// host's root. gitprov.SplitRepo cuts the two apart and nothing more, and
// an owner of ".." or "." reaches an API path that means something else.
func validRepo(repo string) bool {
	owner, name, ok := gitprov.SplitRepo(repo)
	if !ok {
		return false
	}
	for _, seg := range []string{owner, name} {
		if seg == "." || seg == ".." || strings.ContainsAny(seg, `\`) {
			return false
		}
	}
	return true
}
