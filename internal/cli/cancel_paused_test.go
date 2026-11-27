package cli

// PR10's CLI half: what `fugaro cancel` does to a run that ended paused
// (docs/model-credentials-routing, contract-pause "Cancelling", D3195, D3197).
//
// A paused run is not a finished one. Nothing about it has ended: its chain is
// in flight to every guard, and the resident orchestrator resumes it at an
// instant the credentials service computed. So there is no execution to cancel
// and no marker to write — the cancel marker stops a retry and a runner, and
// neither is what holds this run — and what ends the flight is the launcher's
// own cancellation of the pause, asked of the service's route, which verifies
// the caller against the pause's own record. The address the token travels to
// is read out of the selected project's config and never named by a flag, for
// the reason `accounts pool designate` reads its own the same way.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// pauseCancelSeen is one cancel call the command made, as the fake below
// recorded it.
type pauseCancelSeen struct {
	URL string
	Req credsvc.PauseCancelRequest
}

// fakePauseCancels replaces the one seam the pause's cancel goes through. No
// test mints a real identity token or reaches a service.
func fakePauseCancels(t *testing.T) *[]pauseCancelSeen {
	t.Helper()
	seen := &[]pauseCancelSeen{}
	prev := cancelPauseDo
	cancelPauseDo = func(_ context.Context, url string, req credsvc.PauseCancelRequest) error {
		*seen = append(*seen, pauseCancelSeen{URL: url, Req: req})
		return nil
	}
	t.Cleanup(func() { cancelPauseDo = prev })
	return seen
}

// pausedRunRecord lands the record a run that suspended itself leaves: the word
// R15 added to the status vocabulary, which is the whole of what this command
// reads a pause out of.
func pausedRunRecord(t *testing.T, f *cloudFixture, id, exec string) {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	err = runstore.Open(b, appSlug, id).WriteRecord(context.Background(), &runstore.Record{
		Version: 1, RunID: id, Execution: exec, Status: runstore.StatusPaused, Outcome: runstore.OutcomeNone})
	if err != nil {
		t.Fatal(err)
	}
}

// TestCancelOfAPausedRun is PR10's red step: `fugaro cancel` of a run whose
// record ended paused asks the installation's credentials service to mark its
// paused node cancelled, so the chain is never resumed — and it touches neither
// the run's own execution nor the cancel marker, which stop a run that is going
// and a retry, and neither is what holds this one.
func TestCancelOfAPausedRun(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "credentials_service: { url: "+intakeTestCredentialsURL+" }\n")
	const id = "20261120-090000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	// The run suspended itself: its container stopped, and its record is the one
	// it left behind (R12).
	f.run.SetState(exec, backend.StateSucceeded)
	pausedRunRecord(t, f, id, exec)
	seen := fakePauseCancels(t)

	out, _, err := execute(t, "cancel", "--json", id)
	if err != nil {
		t.Fatalf("cancel of a paused run: %v\n%s", err, out)
	}
	var res cancelResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("cancel --json is not a cancel result: %v\n%s", err, out)
	}
	if len(*seen) != 1 {
		t.Fatalf("the command made %d cancel calls, want one: a paused run's chain is ended by the service's own mark", len(*seen))
	}
	got := (*seen)[0]
	if got.URL != intakeTestCredentialsURL {
		t.Errorf("the cancel asked %s, want the installation's own credentials service (%s): the address is read from the project's config and never named by a flag", got.URL, intakeTestCredentialsURL)
	}
	if want := (credsvc.PauseCancelRequest{Slug: appSlug, Run: id}); got.Req != want {
		t.Errorf("the cancel sent %#v, want %#v: the pause is the service's own record of one run of one repository", got.Req, want)
	}
	if res.Status != cancelPauseCancelled {
		t.Errorf("cancel reports %q, want %q: nothing of the run has ended and nothing of it was cancelled through the backend", res.Status, cancelPauseCancelled)
	}
	if s := f.run.State(exec); s != backend.StateSucceeded {
		t.Errorf("the execution is %s: cancel touched the execution of a run that had already stopped", s)
	}
	if cancelled(t, f, id) {
		t.Error("cancel wrote the cancel marker on a paused run: the marker stops a retry and a runner, and what holds this run is the pause")
	}

	// A human reads one line saying what happened, and the line names the pause
	// rather than a run that had already finished.
	human, _, err := execute(t, "cancel", id)
	if err != nil {
		t.Fatalf("cancel of a paused run: %v\n%s", err, human)
	}
	if !strings.Contains(human, "pause") {
		t.Errorf("the line does not say the pause was cancelled:\n%s", human)
	}
}
