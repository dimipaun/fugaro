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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The two things a 404 from the contents API can be. They are separate answers
// because they are separate rows of the design's failure table (spec.md §7): a
// document the ref does not carry selects nothing — no profile, no clone
// workflow — and leaves the run on the repository's own job, while a ref the
// forge cannot resolve is a refusal naming the ref (spec.md §3: a deleted or
// never-pushed branch is not a reason to read another one, and D905 refuses
// rather than falling back). A caller that read the second as the first would
// place a run by a document it never read; both keep Contents's own error
// wrapped, so the status, the URL and GitHub's message stay reachable to
// whoever reads the failure.
var (
	// ErrFileNotFound is the repository holding no file at the path asked for,
	// at a ref it does have (or at its default branch when the caller named
	// none). A caller reads it with errors.Is.
	ErrFileNotFound = errors.New("github: the repository holds no such file")
	// ErrRefNotFound is the repository having no such ref at all. It is never
	// the answer to a read that named no ref: the API's own default branch is
	// one the repository has.
	ErrRefNotFound = errors.New("github: the repository has no such ref")
)

// refNotFoundPrefix is how GitHub words the 404 that is about the ref and not
// the path: "No commit found for the ref <ref>", and the SHA spelling of the
// same miss for a ref given as one. Its answer about a path the ref does not
// carry is a bare "Not Found", so this is the wording that positively selects
// the ref row, and a message this code does not recognize stays the file row.
//
// It is GitHub's prose and can change under this package. A rewording puts a
// missing ref back in the absent-file row, where this plan had it before the
// distinction existed, and never the other way round: the row a message has to
// positively select is the absent-file one, which is what a read that reached
// the API at all is far likelier to be.
const refNotFoundPrefix = "No commit found"

// refNotFound reports whether body, a 404's own, is GitHub's answer about the
// ref rather than the path.
func refNotFound(body string) bool {
	var answer struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(body), &answer) != nil {
		return false
	}
	return strings.HasPrefix(answer.Message, refNotFoundPrefix)
}

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
			at := pluginwire.Printable(path)
			where := "the default branch"
			if ref != "" {
				where = pluginwire.Printable(ref)
			}
			if refNotFound(se.Body) {
				return nil, fmt.Errorf("%w: %s: %w", ErrRefNotFound, where, err)
			}
			return nil, fmt.Errorf("%w: %s at %s: %w", ErrFileNotFound, at, where, err)
		}
		// Every other failure — a refused read, 403 included, which is what a
		// live call with a mint that carries no contents gets — a 5xx, a
		// non-file entry and a body over the bound all stay Contents's own
		// refusal, wording and all.
		return nil, err
	}
	return data, nil
}
