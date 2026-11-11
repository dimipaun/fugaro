package github

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// The checks read (E8, F25). It reports what the host said and nothing more:
// the caller's ChecksState decides green, so the two things pinned here are
// that every page is read (a failure left on page 3 must reach the caller) and
// that a commit with no runs is an empty set rather than an error or a pass.

const checksSHA = "1111111111111111111111111111111111111111"

func TestGithubChecksReadsEveryPage(t *testing.T) {
	var pages int
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
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
	})
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
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/repos/acme/web/commits/"+checksSHA+"/check-runs" {
			io.WriteString(w, `{"total_count": 0, "check_runs": []}`)
			return true
		}
		return false
	})
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
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/repos/acme/web/commits/"+checksSHA+"/check-runs" {
			io.WriteString(w, `{"total_count": 2, "check_runs": [{"status": "completed", "conclusion": "success"}, {"status": "in_progress", "conclusion": null}]}`)
			return true
		}
		return false
	})
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
