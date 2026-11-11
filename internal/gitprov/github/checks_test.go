package github

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// The checks read (E8, F25). It reports what the host said and nothing more:
// the caller's ChecksState decides green, so the things pinned here are that
// every page is read (a failure left on page 3 must reach the caller), that a
// commit with nothing reported is an empty set rather than an error or a
// pass, and that BOTH of this host's reports are read — a commit whose Action
// passed and whose commit status failed is not a commit that passed.

const checksSHA = "1111111111111111111111111111111111111111"

// statusPath is the combined status of checksSHA, the second endpoint every
// Checks call has to reach.
const statusPath = "/repos/acme/web/commits/" + checksSHA + "/status"

// checksHandler answers both of the reads Checks makes: the check-runs
// listing (through runs) and the combined status (through statuses). A nil
// runs or statuses is an empty array, which is what this host answers for a
// commit nothing reports on.
func checksHandler(runs func(w http.ResponseWriter, r *http.Request) bool, statuses string) func(http.ResponseWriter, *http.Request) bool {
	if statuses == "" {
		// What GitHub answers for a commit whose contexts have posted
		// nothing: a pending state over an empty array.
		statuses = `{"state": "pending", "statuses": []}`
	}
	return func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "GET" && r.URL.Path == statusPath:
			io.WriteString(w, statuses)
			return true
		case r.Method == "GET" && r.URL.Path == "/repos/acme/web/commits/"+checksSHA+"/check-runs":
			if runs != nil && runs(w, r) {
				return true
			}
			// A nil runs is a commit with no check runs, which is the shape a
			// test that is about the other report wants.
			io.WriteString(w, `{"total_count": 0, "check_runs": []}`)
			return true
		default:
			return false
		}
	}
}

func TestGithubChecksReadsEveryPage(t *testing.T) {
	var pages int
	p := fakeAPI(t, checksHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != "GET" || r.URL.Path != "/repos/acme/web/commits/"+checksSHA+"/check-runs" {
			return false
		}
		pages++
		switch r.URL.Query().Get("page") {
		case "":
			if got := r.URL.Query().Get("per_page"); got != "100" {
				t.Errorf("per_page = %q", got)
			}
			// A next link is followed as a path relative to the API root, so
			// the Authorization header never leaves the host.
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/repos/acme/web/commits/%s/check-runs?per_page=100&page=2>; rel="next"`, r.Host, checksSHA))
			io.WriteString(w, `{"total_count": 3, "check_runs": [{"status": "completed", "conclusion": "success"}, {"status": "completed", "conclusion": "failure"}]}`)
		case "2":
			io.WriteString(w, `{"total_count": 3, "check_runs": [{"status": "completed", "conclusion": "success"}]}`)
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
		}
		return true
	}, ""))
	runs, err := p.Checks(ctx, 12, checksSHA)
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 || len(runs) != 3 {
		t.Fatalf("pages = %d, runs = %+v; want both pages and three runs", pages, runs)
	}
	if got := gitprov.ChecksState(runs); got != gitprov.CIRed {
		t.Errorf("ChecksState = %v, want red: one of the reported runs failed", got)
	}
}

// TestGithubChecksNoRunsIsAnEmptySet is F25's shape: Fugaro's own repository
// reports nothing on a pull request, and that is an empty set with no error —
// never a state the caller could read as green by accident.
func TestGithubChecksNoRunsIsAnEmptySet(t *testing.T) {
	p := fakeAPI(t, checksHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/repos/acme/web/commits/"+checksSHA+"/check-runs" {
			io.WriteString(w, `{"total_count": 0, "check_runs": []}`)
			return true
		}
		return false
	}, ""))
	runs, err := p.Checks(ctx, 12, checksSHA)
	if err != nil || len(runs) != 0 {
		t.Fatalf("Checks = %+v, %v; want an empty set", runs, err)
	}
	if got := gitprov.ChecksState(runs); got != gitprov.CIUnknown {
		t.Fatalf("ChecksState = %v, want unknown", got)
	}
}

// TestGithubChecksPendingRunIsUnknown keeps the pending shape pinned against
// this adapter too: GitHub reports a queued run with a null conclusion, which
// must arrive as a run that has not passed rather than as one that has.
func TestGithubChecksPendingRunIsUnknown(t *testing.T) {
	p := fakeAPI(t, checksHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/repos/acme/web/commits/"+checksSHA+"/check-runs" {
			io.WriteString(w, `{"total_count": 2, "check_runs": [{"status": "completed", "conclusion": "success"}, {"status": "in_progress", "conclusion": null}]}`)
			return true
		}
		return false
	}, ""))
	runs, err := p.Checks(ctx, 12, checksSHA)
	if err != nil || len(runs) != 2 {
		t.Fatalf("Checks = %+v, %v", runs, err)
	}
	if runs[1].Conclusion != "" {
		t.Errorf("a null conclusion arrived as %q", runs[1].Conclusion)
	}
	if got := gitprov.ChecksState(runs); got != gitprov.CIUnknown {
		t.Fatalf("ChecksState = %v, want unknown", got)
	}
}

// TestGithubChecksReadsCommitStatusesToo is the one that keeps a red commit
// from reading green: this host reports CI through check runs AND through
// commit statuses, and a repository whose Codecov, Jenkins or Vercel result is
// a status rather than a check run has its real answer in the second read. A
// passing check run with a failing status is a failed commit.
func TestGithubChecksReadsCommitStatusesToo(t *testing.T) {
	p := fakeAPI(t, checksHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/repos/acme/web/commits/"+checksSHA+"/check-runs" {
			io.WriteString(w, `{"total_count": 1, "check_runs": [{"status": "completed", "conclusion": "success"}]}`)
			return true
		}
		return false
	}, `{"state": "failure", "statuses": [
		{"state": "success", "context": "ci/build"},
		{"state": "failure", "context": "codecov/project"}]}`))
	runs, err := p.Checks(ctx, 12, checksSHA)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("Checks = %+v; want the check run and both commit statuses", runs)
	}
	if got := gitprov.ChecksState(runs); got != gitprov.CIRed {
		t.Fatalf("ChecksState = %v, want red: the commit status failed even though the check run passed", got)
	}
}

// TestGithubChecksCommitStatusStates pins the translation of each state a
// commit status can carry, and the fail-closed end of it: a state this code
// has not seen is a commit that has not passed, never one that has.
func TestGithubChecksCommitStatusStates(t *testing.T) {
	p := fakeAPI(t, checksHandler(nil, `{"state": "success", "statuses": [{"state": "success", "context": "ci/build"}]}`))
	runs, err := p.Checks(ctx, 12, checksSHA)
	if err != nil || len(runs) != 1 {
		t.Fatalf("Checks = %+v, %v", runs, err)
	}
	if got := gitprov.ChecksState(runs); got != gitprov.CIGreen {
		t.Errorf("ChecksState = %v, want green: a status is a report, and this one passed", got)
	}
	for _, c := range []struct {
		state string
		want  gitprov.CIState
	}{
		{"failure", gitprov.CIRed},
		{"error", gitprov.CIRed},
		{"pending", gitprov.CIUnknown},
		{"waiting", gitprov.CIUnknown},
	} {
		run := statusRun(c.state)
		if got := gitprov.ChecksState([]gitprov.CheckRun{run}); got != c.want {
			t.Errorf("a %q status maps to %+v, which ChecksState reads as %v, want %v", c.state, run, got, c.want)
		}
	}
}

// TestGithubChecksRefusesWhenTheStatusReadFails: a check-runs read that
// succeeded is not half an answer. Dropping the second error would hand the
// caller a set holding only the check runs, and a commit whose commit status
// failed would read as one that passed — the mistake the second read is there
// to close.
func TestGithubChecksRefusesWhenTheStatusReadFails(t *testing.T) {
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/acme/web/commits/"+checksSHA+"/check-runs":
			io.WriteString(w, `{"total_count": 1, "check_runs": [{"status": "completed", "conclusion": "success"}]}`)
			return true
		case r.Method == "GET" && r.URL.Path == statusPath:
			http.Error(w, "boom", http.StatusInternalServerError)
			return true
		}
		return false
	})
	if _, err := p.Checks(ctx, 12, checksSHA); err == nil {
		t.Fatal("Checks did not refuse when the commit-statuses read failed")
	}
}

// TestGithubChecksRefusesAValueThatIsNotACommit: the SHA reaches this call from
// a run record and a ledger, so a value that is not a commit is refused before
// it is put in a path. The provider serves no request at all, so a read that
// went out would fail the test.
func TestGithubChecksRefusesAValueThatIsNotACommit(t *testing.T) {
	p := fakeAPI(t, func(http.ResponseWriter, *http.Request) bool { return false })
	for _, sha := range []string{"", "../../etc/passwd", "012345", "not a sha", checksSHA + "1", "-" + checksSHA[:7]} {
		if _, err := p.Checks(ctx, 12, sha); err == nil {
			t.Errorf("Checks(%q) did not refuse", sha)
		}
	}
}

// TestGithubChecksRefusesForWantOfThePermission pins the one 403 this host
// explains. Both endpoints need the App's "Checks: read" and "Commit
// statuses: read", and a run's installation token asks for neither, so an
// installation that has not granted them answers 403 — which must arrive as
// ErrChecksPermission, naming the grant, rather than as a bare HTTP refusal an
// operator has to decode. It stays a failed read: the caller sees an error and
// never a state, so nothing reads a commit as green because the read was
// refused.
func TestGithubChecksRefusesForWantOfThePermission(t *testing.T) {
	for _, c := range []struct {
		name string
		h    func(http.ResponseWriter, *http.Request) bool
	}{
		{"the check-runs read", func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/repos/acme/web/commits/"+checksSHA+"/check-runs" {
				http.Error(w, `{"message": "Resource not accessible by integration"}`, http.StatusForbidden)
				return true
			}
			return false
		}},
		{"the commit-statuses read", func(w http.ResponseWriter, r *http.Request) bool {
			switch r.URL.Path {
			case "/repos/acme/web/commits/" + checksSHA + "/check-runs":
				io.WriteString(w, `{"total_count": 1, "check_runs": [{"status": "completed", "conclusion": "success"}]}`)
				return true
			case statusPath:
				http.Error(w, `{"message": "Resource not accessible by integration"}`, http.StatusForbidden)
				return true
			}
			return false
		}},
	} {
		p := fakeAPI(t, c.h)
		runs, err := p.Checks(ctx, 12, checksSHA)
		if !errors.Is(err, gitprov.ErrChecksPermission) {
			t.Errorf("%s: Checks = %+v, %v; want ErrChecksPermission", c.name, runs, err)
		}
		if runs != nil {
			t.Errorf("%s: a refused read returned %+v; want no runs at all", c.name, runs)
		}
		if !strings.Contains(err.Error(), "Checks: read") || !strings.Contains(err.Error(), "Commit statuses: read") {
			t.Errorf("%s: the refusal %q does not name the grants it needs", c.name, err)
		}
	}
}

// TestGithubChecksReportsOtherFailuresAsTheyCame keeps the 403 mapping from
// swallowing everything else: a 500 is an outage the caller may retry, not a
// permission an operator changes, and it must not read as one.
func TestGithubChecksReportsOtherFailuresAsTheyCame(t *testing.T) {
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		http.Error(w, "boom", http.StatusInternalServerError)
		return true
	})
	_, err := p.Checks(ctx, 12, checksSHA)
	if err == nil || errors.Is(err, gitprov.ErrChecksPermission) {
		t.Fatalf("Checks = %v; want a plain failure, not a permission one", err)
	}
}
