package credsvc

// PR10's service half: the route a paused run's launcher cancels its pause
// through (docs/model-credentials-routing, contract-pause "Cancelling", D3197).
//
// What the route is held to is one sentence: the launcher's own act, and
// nobody else's. The service has no admin surface (D143), so it cannot tell a
// project admin from anyone else, and the plan gives an admin no way to cancel
// another person's pause — the caller is verified against the launcher the
// SERVICE ITSELF recorded for that run's pause (R8), which is the principal its
// own slip named and never the run's word about itself.
//
// Every refusal after the caller is verified writes exactly one audit line of
// the pause cancel's own purpose, carrying the run and the repository it is
// about and no credential and no job, and no value of the call's.

import (
	"net/http"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/routing"
)

// otherLauncherSub is a second person on the fixture's roster: a launcher whose
// token verifies, and whose principal is not the pause's own.
const otherLauncherSub = "104329501182774832336"

// cancelBody is the route's own body: the repository and the run whose pause is
// cancelled.
func cancelBody(t *testing.T, slug, run string) string {
	t.Helper()
	return mustBody(t, PauseCancelRequest{Slug: slug, Run: run})
}

// cancelAs sends one cancel call as the caller holding tok.
func (s *pauses) cancelAs(t *testing.T, body, tok string) (*http.Response, []byte) {
	t.Helper()
	return s.slipPost(t, PauseCancelPath, body, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+tok)
	})
}

// cancelOK is the launcher's own cancel, failing the test on a refusal.
func (s *pauses) cancelOK(t *testing.T, slug, run string) {
	t.Helper()
	resp, body := s.cancelAs(t, cancelBody(t, slug, run), s.launcherToken())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the cancel is answered %d, want 200: %s", resp.StatusCode, body)
	}
}

// wantOneCancelLine holds a refusal to the rule every refusal of this service is
// held to (C1.13): exactly one audit line, of the cancel's own purpose, naming
// the caller it was verified for, the run and the repository it is about, and no
// credential and no job.
func wantOneCancelLine(t *testing.T, s *pauses, before int, status, caller, run, repository string) {
	t.Helper()
	lines := parseAuditLines(t, s.audit.String()[before:])
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit line(s), want exactly one", len(lines))
	}
	wantSlipLine(t, lines[0], PurposePauseCancel, status, map[string]any{
		"run": run, "repository": repository, "caller": caller,
		"job": "", "credential": "", "tier": "",
	})
}

// TestOnlyTheLauncherCancelsAPause is PR10's service test: the launcher whose
// run paused may cancel its pause, and a caller whose principal is another's is
// refused — the orchestrator's own token and a second launcher's alike, which is
// what "an admin does not cancel another person's pause" comes to where the
// service has no admin surface at all (D143, D3197).
//
// The mark is the record AND the node: the record is what a resume grant reads
// and is refused on, and the node is the gate the resident orchestrator's tick
// reads.
func TestOnlyTheLauncherCancelsAPause(t *testing.T) {
	const pausedRun = "20261120-090000-abcd"

	// paused is the fixture with one run's pause recorded as the service would
	// have recorded it: the launcher is the fixture's person, and the instant is
	// one a resume would otherwise be granted at.
	paused := func(t *testing.T) *pauses {
		t.Helper()
		s := newPauses(t, nil)
		s.writePause(t, s.slug, pausedRun, creds.Pause{
			Launcher: personLauncher, Pool: "team", Chain: pausedRun,
			ResumeAt: s.now.Add(time.Hour).UnixMilli(), PausedSeconds: 1800,
		})
		return s
	}

	t.Run("the launcher's own cancel marks the pause and its node", func(t *testing.T) {
		s := paused(t)
		s.cancelOK(t, s.slug, pausedRun)

		rec, found := s.record(t, s.slug, pausedRun)
		if !found {
			t.Fatal("the cancel left no record: the mark is what a resume is refused on")
		}
		if !rec.Cancelled {
			t.Error("the record is not marked cancelled, so a resume would still be granted")
		}
		if rec.ResumedBy != "" {
			t.Errorf("the cancel marked the pause resumed by %q", rec.ResumedBy)
		}
		if rec.Launcher != personLauncher || rec.Chain != pausedRun {
			t.Errorf("the cancel rewrote the record: %+v", rec)
		}

		n, ok := s.node(t, s.slug, pausedRun)
		if !ok {
			t.Fatal("the cancel wrote no node: the orchestrator's tick would still resume this run")
		}
		if !n.Cancelled {
			t.Error("the node is not marked cancelled")
		}
		// The node is rebuilt from the record and not patched, so the two cannot
		// disagree about the pause the mark is set on.
		if n.Launcher != rec.Launcher.ID || n.Pool != rec.Pool || n.Chain != rec.Chain ||
			n.ResumeAt != rec.ResumeAt || int64(n.PausedSeconds) != rec.PausedSeconds {
			t.Errorf("the node and the record disagree: node %+v, record %+v", n, rec)
		}

		// A cancel is idempotent: a caller that lost the answer to its own call
		// may ask again, and the mark set twice is the mark.
		s.cancelOK(t, s.slug, pausedRun)
		if again, _ := s.record(t, s.slug, pausedRun); !again.Cancelled || again.ResumedBy != "" {
			t.Errorf("a second cancel moved the record: %+v", again)
		}
	})

	t.Run("another launcher's cancel is refused", func(t *testing.T) {
		s := paused(t)
		before := s.audit.Len()
		resp, body := s.cancelAs(t, cancelBody(t, s.slug, pausedRun),
			s.launcherToken(func(c map[string]any) { c["sub"] = otherLauncherSub }))
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)

		if rec, _ := s.record(t, s.slug, pausedRun); rec.Cancelled {
			t.Error("a caller whose principal is another's cancelled the pause (D3197)")
		}
		if _, ok := s.node(t, s.slug, pausedRun); ok {
			t.Error("a refused cancel wrote a node")
		}
		wantOneCancelLine(t, s, before, ErrorRequestRefused, testLauncherAccount, pausedRun, s.slug)
	})

	t.Run("the orchestrator's own token is refused", func(t *testing.T) {
		// D3197 read as the plan writes it: the service has no admin surface, so
		// the installation's own daemon — the one class that resumes a pause —
		// is refused here with everyone else. Its principal is the account it
		// runs as, and the pause's launcher is a person.
		s := paused(t)
		before := s.audit.Len()
		resp, body := s.cancelAs(t, cancelBody(t, s.slug, pausedRun), s.orchestratorToken())
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)

		if rec, _ := s.record(t, s.slug, pausedRun); rec.Cancelled {
			t.Error("the orchestrator cancelled a person's pause")
		}
		wantOneCancelLine(t, s, before, ErrorRequestRefused, testOrchestratorSA, pausedRun, s.slug)
	})

	t.Run("a run this service recorded no pause of", func(t *testing.T) {
		s := newPauses(t, nil)
		before := s.audit.Len()
		resp, body := s.cancelAs(t, cancelBody(t, s.slug, pausedRun), s.launcherToken())
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)

		if _, ok := s.node(t, s.slug, pausedRun); ok {
			t.Error("a cancel of a run with no pause wrote a node")
		}
		wantOneCancelLine(t, s, before, ErrorRequestRefused, testLauncherAccount, pausedRun, s.slug)
	})

	t.Run("a pause already resumed is refused", func(t *testing.T) {
		// The chain is in flight again as the run that continued this pause, and
		// that run is a new one this pause's cancel cannot reach.
		s := newPauses(t, nil)
		s.writePause(t, s.slug, pausedRun, creds.Pause{
			Launcher: personLauncher, Chain: pausedRun, ResumedBy: "20261120-120000-beef",
		})
		before := s.audit.Len()
		resp, body := s.cancelAs(t, cancelBody(t, s.slug, pausedRun), s.launcherToken())
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)

		if rec, _ := s.record(t, s.slug, pausedRun); rec.Cancelled || rec.ResumedBy == "" {
			t.Errorf("the cancel disturbed a resumed pause: %+v", rec)
		}
		wantOneCancelLine(t, s, before, ErrorRequestRefused, testLauncherAccount, pausedRun, s.slug)
	})

	t.Run("a caller presenting a run identity is refused", func(t *testing.T) {
		// The route is the LAUNCHER class's: a launcher holds no run identity —
		// the run it cancels is one it launched, and it presents its own Google
		// ID token rather than a run's — so a call carrying one is a call of a
		// shape this route does not have.
		s := paused(t)
		before := s.audit.Len()
		resp, body := s.slipPost(t, PauseCancelPath, cancelBody(t, s.slug, pausedRun),
			s.launcherBearer(), func(r *http.Request) {
				r.Header.Set(HeaderRunIdentity, s.run)
			})
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)

		if rec, _ := s.record(t, s.slug, pausedRun); rec.Cancelled {
			t.Error("a call presenting a run identity cancelled the pause")
		}
		wantOneCancelLine(t, s, before, ErrorRequestRefused, testLauncherAccount, "", "")
	})
}

// TestThePauseCancelBodyIsStrict is the route's body: the two names it carries,
// held to the shapes the paths built from them are safe against, and nothing
// else — a body naming a principal is a request of a shape this route does not
// have, which is the whole of "an admin does not cancel another person's pause".
func TestThePauseCancelBodyIsStrict(t *testing.T) {
	const pausedRun = "20261120-090000-abcd"
	for _, tc := range []struct {
		name string
		body string
	}{
		{"no repository", `{"run":"` + pausedRun + `"}`},
		{"a repository of two segments", `{"slug":"a/b","run":"` + pausedRun + `"}`},
		{"no run", `{"slug":"repo"}`},
		{"a run id of another grammar", `{"slug":"repo","run":"not a run"}`},
		{"a principal the body would name", `{"slug":"repo","run":"` + pausedRun + `","principal":"someone"}`},
		{"not one object", `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newPauses(t, nil)
			before := s.audit.Len()
			resp, body := s.cancelAs(t, tc.body, s.launcherToken())
			wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)
			// The body is refused before the run and the repository are known, so
			// the line carries neither.
			wantOneCancelLine(t, s, before, ErrorRequestRefused, testLauncherAccount, "", "")
		})
	}
}

// TestACancelNobodyIsVerifiedForIsUnverified: a call with no bearer at all is
// refused before any caller was verified, which is the one refusal on this route
// with no line to write.
func TestACancelNobodyIsVerifiedForIsUnverified(t *testing.T) {
	s := newPauses(t, nil)
	before := s.audit.Len()
	resp, body := s.slipPost(t, PauseCancelPath, cancelBody(t, s.slug, "20261120-090000-abcd"))
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedJobIdentity)
	if got := s.audit.String()[before:]; got != "" {
		t.Errorf("a call refused before its caller was verified wrote %q, want no line", got)
	}
}

// TestACancelOfAStoppedRepositoryIsRefused is the designation route's own rule:
// an operator who stopped the service stopped what it decides, and ending a
// pause is a decision.
func TestACancelOfAStoppedRepositoryIsRefused(t *testing.T) {
	const pausedRun = "20261120-090000-abcd"
	s := newPauses(t, nil)
	s.writePause(t, s.slug, pausedRun, creds.Pause{
		Launcher: routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub}, Chain: pausedRun,
	})
	s.kills.repos[s.slug] = true
	before := s.audit.Len()
	resp, body := s.cancelAs(t, cancelBody(t, s.slug, pausedRun), s.launcherToken())
	wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable)
	if rec, _ := s.record(t, s.slug, pausedRun); rec.Cancelled {
		t.Error("a stopped service cancelled a pause")
	}
	wantOneCancelLine(t, s, before, ErrorStateUnavailable, testLauncherAccount, pausedRun, s.slug)
}
