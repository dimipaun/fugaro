package cli

import (
	"context"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// repoArg is the one resolver of a --repo value, for every command that takes
// one. A value that already reads as owner/name (task.CanonicalRepo's rule --
// one or more segments, no "." or "..", no leading "/" or "~") passes through
// unchanged, and it wins over anything on disk: `--repo acme/app` names the
// repository acme/app wherever it is typed, even in a directory that happens
// to be a checkout at ./acme/app. Anything else is a path -- ".", "..", a
// relative or an absolute one -- and resolves to the owner/name of the origin
// remote of the checkout that holds it, through repoFromOrigin, the one
// parser of a github or bitbucket remote URL this program has.
//
// A path that resolves to nothing usable is refused: not a directory there,
// not inside a git checkout, no origin remote, an origin that names no
// repository, or a checkout whose repository this project's config does not
// list. Every refusal that names the value prints it through safetext.Field,
// never raw: it is whatever was typed, and a path is text like any other. The
// one that does not name it is the value with nothing at it, which is refused
// with the owner/name sentence alone -- a value there may have been pasted
// into the wrong flag, and an error never echoes what it was given.
//
// The empty value is returned as it is: the bare --repo means "the repository
// this checkout belongs to", and each command resolves that default its own
// way (originRepo). A path never becomes a default and a default never
// becomes a path.
func repoArg(ctx context.Context, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if _, err := task.CanonicalRepo(value); err == nil {
		return value, nil
	}
	dir := value
	fi, err := os.Stat(dir)
	if err != nil {
		// Nothing is at the value and it does not read as owner/name, so it is
		// refused without being printed: whatever was typed there may have been
		// meant for another flag, and a refusal never repeats it (this is
		// task.CanonicalRepo's sentence, and TestSecretsSetNeverEchoes pins it).
		return "", userErr("--repo: the repository must look like owner/name, or be a path inside a git checkout")
	}
	if !fi.IsDir() {
		return "", userErr("--repo %s is a file, and a path must be a directory inside a git checkout whose origin names the repository", safetext.Field(dir))
	}
	out, err := gitCmd(ctx, dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", userErr("--repo %s is not inside a git checkout, and a path names a checkout: its origin remote is what says which repository it is", safetext.Field(dir))
	}
	root := strings.TrimSpace(string(out))
	origin, err := gitCmd(ctx, root, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", userErr("--repo %s: the checkout at %s has no origin remote, so it names no repository (run from its checkout, or pass --repo owner/name)", safetext.Field(dir), safetext.Field(root))
	}
	raw := strings.TrimSpace(string(origin))
	repo, ok := repoFromOrigin(raw)
	if !ok {
		return "", userErr("--repo %s: the checkout's origin %s does not name owner/name (a github.com or bitbucket.org remote, as https, ssh or scp-like)", safetext.Field(dir), gcp.RedactURL(image.HTTPSOrigin(raw)))
	}
	if lc := selectedProjectConfigReadOnly(ctx); lc != nil {
		if _, known := lc.RepoFor(repo); !known {
			return "", userErr("--repo %s is a checkout of %s, which is not one of project %s's repositories in the local config: a path may only name a checkout of this project (fugaro update --repo in that checkout onboards it)", safetext.Field(dir), safetext.Field(repo), safetext.Field(lc.Name))
		}
	}
	return repo, nil
}
