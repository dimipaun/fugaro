package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
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

// The vocabulary the two reports share with ChecksState. A commit status
// spells its states differently from a check run, and this adapter translates
// rather than passes them through, so the green rule stays in one place.
const (
	checkCompleted = "completed"
	checkSuccess   = "success"
	checkFailure   = "failure"
)

// Checks implements gitprov.Provider: everything GitHub reports about commit
// sha, its check runs first and its commit statuses after. The caller decides
// the state with ChecksState; this read reports what the host said and nothing
// more, and a commit nothing reports on comes back as an empty slice and no
// error (F25: Fugaro's own repository reports nothing on a pull request, and
// reading that as "nothing failed" is the mistake ChecksState exists to
// prevent).
//
// Both reports are read because this host has two and either can fail a
// commit (see commitStatuses): a commit whose Action passed and whose Codecov
// status failed is not a commit that passed.
//
// The read needs no write credential and is made with the provider's own
// installation token (E8): the checks are the host's own report about a
// commit, and reading them changes nothing.
//
// It does need a READ permission a run's installation token does not ask for.
// GitHub requires the App's "Checks: read" for the check runs and "Commit
// statuses: read" for the statuses, and tokenPermissions names neither, so on
// an installation that has not granted both this call gets 403 — which
// checksErr reports as ErrChecksPermission rather than as a bare refusal. The
// grant is the owner's (Check 231 of docs/gcp-live-checklist.md, B2 of
// docs/forge-authorship-images/implementation-plan.md, which widens
// tokenPermissions and RequiredPermissions together); until it lands, this
// call fails closed and the ladder's gate waits for a person.
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
	runs, err := p.checkRuns(ctx, number, sha)
	if err != nil {
		return nil, checksErr(err)
	}
	statuses, err := p.commitStatuses(ctx, number, sha)
	if err != nil {
		// Half a set is not a smaller set: a caller that took the check runs
		// and dropped this error would read a commit whose commit statuses
		// were never read as one whose checks all passed.
		return nil, checksErr(err)
	}
	return append(runs, statuses...), nil
}

// checksErr is the refusal a checks read reports, with the one failure this
// host explains: a 403 is GitHub refusing the CREDENTIAL rather than the read,
// because both of these endpoints need the App's "Checks: read" and "Commit
// statuses: read" and a run's installation token asks for neither
// (tokenPermissions). The read is already wrapped with the pull request and
// the commit by the caller, so this only adds the sentinel and the grant it
// names; every other failure is reported as it came.
func checksErr(err error) error {
	var se *httpjson.StatusError
	if errors.As(err, &se) && se.Status == http.StatusForbidden {
		return fmt.Errorf("%w: %w; the App must be granted Checks: read and Commit statuses: read, and each installation must accept them",
			gitprov.ErrChecksPermission, err)
	}
	return err
}

// checkRuns reads every check run GitHub reports at commit sha, following the
// listing's own pages.
func (p *Provider) checkRuns(ctx context.Context, number int, sha string) ([]gitprov.CheckRun, error) {
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

// commitStatuses reads the combined status at commit sha. GitHub reports CI
// twice: check runs, which Actions and apps post, and commit statuses, which
// everything else that reads a repository does — Codecov, Jenkins, Vercel, a
// CI that was set up before Actions existed. They are separate endpoints, and
// the branch-protection screen, which is the thing a person reads to answer
// "is this commit green", shows both. A read of the check runs alone would
// report a commit whose Action passed and whose Codecov status failed as
// CIGreen, and the ladder's gate would merge red CI, so both are read.
//
// The combined status is the latest status of each context rather than every
// status ever posted, so one request is the whole set and there is nothing to
// follow. A repository that uses neither report answers an empty array.
func (p *Provider) commitStatuses(ctx context.Context, number int, sha string) ([]gitprov.CheckRun, error) {
	var out struct {
		Statuses []struct {
			State string `json:"state"`
		} `json:"statuses"`
	}
	path := p.repoPath("/commits/" + url.PathEscape(sha) + "/status")
	if err := p.api.Do(ctx, "GET", path, nil, &out); err != nil {
		return nil, fmt.Errorf("reading the commit statuses of pull request #%d at %s: %w", number, sha, err)
	}
	runs := make([]gitprov.CheckRun, 0, len(out.Statuses))
	for _, s := range out.Statuses {
		runs = append(runs, statusRun(s.State))
	}
	return runs, nil
}

// statusRun maps one commit status onto the CheckRun shape ChecksState reads.
// A status carries one word where a check run carries a status and a
// conclusion, so each state is translated rather than passed through. A state
// this code has not seen lands on a run that has not completed, which
// ChecksState holds as CIUnknown: a host that invents a state has not told
// this code that the commit passed.
func statusRun(state string) gitprov.CheckRun {
	switch state {
	case checkSuccess:
		return gitprov.CheckRun{Status: checkCompleted, Conclusion: checkSuccess}
	case checkFailure, "error":
		return gitprov.CheckRun{Status: checkCompleted, Conclusion: checkFailure}
	default:
		return gitprov.CheckRun{Status: state}
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
