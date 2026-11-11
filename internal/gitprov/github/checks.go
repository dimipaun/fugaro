package github

import (
	"context"
	"fmt"
	"net/url"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// checksPage is what one page of the check-runs listing holds. GitHub's own
// ceiling is 100, and asking for it keeps a commit with a normal number of
// checks to one request.
const checksPage = 100

// checksMaxPages bounds the listing: this many pages, 1000 check runs at a
// commit, is far past any real one, and a host that keeps answering with a
// next link stops being followed there. The read then fails, which is the
// direction this call fails in — an unbounded listing would be a hang, and a
// truncated one read as the whole set could read a commit as greener than it
// is (a failure on page 3 is not a commit whose checks all passed).
const checksMaxPages = 10

// Checks implements gitprov.Provider: every check run GitHub reports at commit
// sha, in the order it lists them. The caller decides the state with
// ChecksState; this read reports what the host said and nothing more, and a
// commit with no check runs at all comes back as an empty slice and no error
// (F25: Fugaro's own repository reports nothing on a pull request, and reading
// that as "nothing failed" is the mistake ChecksState exists to prevent).
//
// The read needs no write credential and is made with the provider's own
// installation token (E8): the checks are the host's own report about a
// commit, and reading them changes nothing.
func (p *Provider) Checks(ctx context.Context, number int, sha string) ([]gitprov.CheckRun, error) {
	if number <= 0 {
		return nil, fmt.Errorf("reading the checks of pull request #%d: not a pull request number", number)
	}
	if !isCommit(sha) {
		// A value that is not a commit cannot be read at all, and asking
		// GitHub for one would be a request whose 422 says less than this
		// does. The SHA reaches here from a run record and a ledger, so its
		// shape is checked before it is put in a path rather than after.
		return nil, fmt.Errorf("reading the checks of pull request #%d: %q is not a commit", number, sha)
	}
	var runs []gitprov.CheckRun
	path := p.repoPath("/commits/" + url.PathEscape(sha) + fmt.Sprintf("/check-runs?per_page=%d", checksPage))
	for page := 0; ; page++ {
		if page >= checksMaxPages {
			return nil, fmt.Errorf("reading the checks of pull request #%d at %s: more than %d pages of check runs", number, sha, checksMaxPages)
		}
		var out struct {
			CheckRuns []struct {
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			} `json:"check_runs"`
		}
		next, err := p.api.DoPage(ctx, "GET", path, nil, &out)
		if err != nil {
			return nil, fmt.Errorf("reading the checks of pull request #%d at %s: %w", number, sha, err)
		}
		for _, r := range out.CheckRuns {
			// Conclusion is null while a run has not completed; GitHub's
			// answer decodes it to "", which ChecksState holds as unknown
			// rather than as a pass.
			runs = append(runs, gitprov.CheckRun{Status: r.Status, Conclusion: r.Conclusion})
		}
		if next == "" {
			return runs, nil
		}
		path = next
	}
}

// isCommit reports whether s names a commit the way this API spells one: 7 to
// 40 hex digits. An abbreviated head is a commit GitHub resolves (a
// comparison, a read), so the shorter form is accepted here and the longer
// one — the shape a run record carries — with it.
func isCommit(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}
