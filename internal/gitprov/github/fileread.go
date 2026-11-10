package github

// The launcher's reader (A1, D910): one file of one repository at one ref, read
// with a credential the caller supplies and never with one this process holds.
// It exists for the launch that has no checkout of the repository and has to
// know, before anything is started, what the ref's fugaro.yaml selects — so it
// is the one path on which a provider credential is put in the launcher's
// process, and the owner's answer to D902 is that the credential is the
// operator's own read mint at the installation's credentials service
// (ServiceReadTokens), not the App's key.
//
// It is built over what the package already has rather than over a second HTTP
// client: the provider is assembled with ServiceHeldKey and the caller's
// gitprov.TokenSource installed through UseServiceTokens, and the read itself
// is Contents — gitprov.ContentsReader — which already refuses a directory, a
// symlink entry, a submodule, a non-base64 body and anything over
// gitprov.MaxContentsBytes. Nothing here parses a document and nothing here
// decodes anything a second time: the bytes are Contents's own.

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// ErrFileNotFound is the repository holding no file at the path and ref that
// were asked for. It is a fact of its own and not a refusal, because the
// launcher's messages differ for the two: a document that is not there selects
// nothing and leaves the run on the repository's own job, while a read that was
// refused means the job cannot be decided at all (D905, spec §7). A caller
// reads it with errors.Is; every other failure FileAt answers with is
// Contents's own refusal, wording and all.
var ErrFileNotFound = errors.New("github: the repository holds no such file at that ref")

// FileAt reads path of repo at ref, mints through ts, and returns the file's
// bytes unchanged.
//
// repo is one name in the form "owner/name" — the spelling a project's config
// and every command line use — and o carries what is not the repository's own:
// the App ID the read mint is signed as, and, for a test, the API root and the
// HTTP client. A key in o is dropped rather than carried: the provider this
// builds mints through ts and through nothing else, so the App's key cannot
// reach the process that decides a run's job.
//
// No permission is asked for anywhere here, and there is no parameter to ask
// with: what the token reaches is the set the credentials service mints it
// with (A0's PersonReadTokenPermissions).
func FileAt(ctx context.Context, o Options, ts gitprov.TokenSource, repo, ref, path string) ([]byte, error) {
	owner, name, ok := gitprov.SplitRepo(repo)
	if !ok {
		return nil, fmt.Errorf("github: %s is not a repository name of the form owner/name", pluginwire.Printable(repo))
	}
	o.Owner, o.Repo = owner, name
	o.PrivateKey = nil
	o.ServiceHeldKey = true
	p, err := New(o)
	if err != nil {
		return nil, err
	}
	p.UseServiceTokens(ts)
	data, err := p.Contents(ctx, path, ref)
	if err != nil {
		var se *httpjson.StatusError
		if errors.As(err, &se) && se.Status == http.StatusNotFound {
			// A ref the forge does not have answers the contents API the same
			// 404 as a path that ref does not carry, and FileAt cannot tell
			// them apart: both are the document's absence at the ref, which is
			// the one thing its caller decides differently from a refusal.
			where := "the default branch"
			if ref != "" {
				where = pluginwire.Printable(ref)
			}
			return nil, fmt.Errorf("%w: %s at %s", ErrFileNotFound, pluginwire.Printable(path), where)
		}
		return nil, err
	}
	return data, nil
}
