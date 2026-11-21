package credsvc

import (
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// TestIdentityRoutesAnswerNotServed is C1's red step and its whole test: the
// five identity routes are mounted in every service of this release, and a
// service with no issuer configured answers every one of them the contract's
// "not served" (R7). That answer is what a launcher of this release bridges on
// — it signs the run's identity locally, which works exactly as long as the
// people's grant is still there — so it is a refusal a caller is told apart
// from every other one, and the test states the three facts that make it so:
// the status, the code, and that it is not the 404 a path this service does not
// know is answered with.
//
// Before the mount this test fails for every path, with the answer
// TestUnknownPathAnswerIsPinned records: a service of the release behind does
// not know these paths at all, and 404 is what an older service says. The two
// answers must stay told apart, because the bridge keys on three things and the
// release-behind answer is a different one of them.
func TestIdentityRoutesAnswerNotServed(t *testing.T) {
	s := newSlips(t, testLauncherAccount)

	for _, path := range []string{
		identitywire.TicketPath, identitywire.ClaimPath, identitywire.PairingPath,
		identitywire.DevicePath, identitywire.ProbePath,
	} {
		t.Run(path, func(t *testing.T) {
			resp, body := s.postRaw(t, http.MethodPost, path)
			// The call carries no header at all, deliberately: the stub reads
			// nothing of it — no caller's identity is verified, since there is
			// no issuer to verify one against — and its answer is the same for
			// every shape a caller may send.
			if resp.StatusCode == http.StatusNotFound {
				t.Fatalf("POST %s is answered 404: %s\nthis is the answer of a service that does not serve the route, and it is not the contract's: R7 keys the bridge on the two being told apart", path, body)
			}
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("POST %s is answered %d, want %d: a service whose identity half is not configured is up and answers that it serves no identity", path, resp.StatusCode, StatusUnavailable)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != identitywire.RefusalNotServed {
				t.Errorf("the refusal is %q, want %q: it is a code of this contract's own and not the service's existing refuseNotServed (F27)", e.Error, identitywire.RefusalNotServed)
			}
			if !strings.Contains(e.Message, "fugaro update") {
				t.Errorf("the refusal does not name the fix: %s", e.Message)
			}
			// A refusal before any caller is verified writes no audit line
			// (audit.go's rule): there is no verified identity to record, and a
			// line here would be a line naming nobody.
			if got := s.audit.String(); got != "" {
				t.Errorf("the refusal wrote an audit line: %s", got)
			}
		})
	}
}

// TestUnknownPathAnswerIsPinned pins the OTHER end of the bridge: the answer a
// service of the release behind gives these paths (F27, R7). It knows none of
// the five, so servedRoute answers false for it and the front door writes the
// 404 below — the same 404 it writes for any path nobody knows — and that
// status is what internal/cli's slipRouteAbsent already reads as an absent
// route (404 or 405).
//
// The paths are asked with methods the identity routes are not served with, so
// that this test keeps pinning the release-behind answer after C1's mount
// exists: the service of this release serves them by POST alone (the census
// holds that), and a service that never heard of a path answers it the same way
// whatever method it comes with.
func TestUnknownPathAnswerIsPinned(t *testing.T) {
	s := newSlips(t, testLauncherAccount)

	// The answer for a path no service ever served, which is the whole of what
	// a release-behind service says about a path it does not know.
	_, unknown := s.postRaw(t, http.MethodPost, "/v1/not-a-route")
	known := refusal(t, http.StatusNotFound, unknown)
	if known.Error != "not_found" {
		t.Fatalf("a path this service does not serve answers %q, want not_found: it is the code the bridge's second trigger is read by", known.Error)
	}

	for _, path := range []string{
		identitywire.TicketPath, identitywire.ClaimPath, identitywire.PairingPath,
		identitywire.DevicePath, identitywire.ProbePath,
	} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			resp, body := s.postRaw(t, method, path)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s is answered %d, want 404: an identity route is served by POST alone, and every other method is what a service that never heard of the path answers with", method, path, resp.StatusCode)
				continue
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != known.Error || e.Message != known.Message {
				t.Errorf("%s %s answers %q/%q, want the unknown path's own answer %q/%q: the release-behind answer is one answer and not five", method, path, e.Error, e.Message, known.Error, known.Message)
			}
		}
	}
}
