package orchestrator_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The resident launcher (mobile plan task P5.1d) is the launcher half of the
// resident orchestrator: the claim, the mint and the job start, in that order,
// over seams the deployment provides. These tests drive it with seams of their
// own and hold the ORDER as tightly as the calls: every step is recorded in
// slices, so a launcher that minted before it claimed, or started after a mint
// it should have given the claim back from, fails here rather than on a live
// installation.

// residentRun is the run id these tests launch: one shape of the ids
// internal/task mints, and the same one the intent of a re-issued attempt
// carries.
const residentRun = "20261117-050000-abcd"

// residentSeams is the three steps as a test drives them: every call is
// appended to calls, and every answer a test can get wrong is a field.
type residentSeams struct {
	t *testing.T

	calls []string

	taken    bool
	holder   string
	claimErr error

	key      []byte
	tokenErr error

	startID    string
	startErr   error
	releaseErr error

	// What the launcher handed the steps: the claim's stamp and the seal key
	// the start was given.
	at      time.Time
	sealKey []byte
	spec    orchestrator.Spec
}

// newResidentSeams is a set of steps that do everything a launch asks: the
// claim is this launcher's, the token is minted under a key and the execution
// is the run id it was asked for.
func newResidentSeams(t *testing.T) *residentSeams {
	t.Helper()
	return &residentSeams{t: t, taken: true, key: []byte("the-seal-key"), startID: residentRun}
}

func (s *residentSeams) launcher() *orchestrator.ResidentLaunch {
	return &orchestrator.ResidentLaunch{
		Claim:   s.claim,
		Release: s.release,
		Token:   s.token,
		Start:   s.start,
		Now:     func() time.Time { return o6Now() },
	}
}

func (s *residentSeams) claim(_ context.Context, _ string, at time.Time) (bool, string, error) {
	s.calls = append(s.calls, "claim")
	s.at = at
	return s.taken, s.holder, s.claimErr
}

func (s *residentSeams) release(_ context.Context, _ string) error {
	s.calls = append(s.calls, "release")
	return s.releaseErr
}

func (s *residentSeams) token(_ context.Context, spec orchestrator.Spec, _ string) ([]byte, error) {
	s.calls = append(s.calls, "mint")
	s.spec = spec
	return s.key, s.tokenErr
}

func (s *residentSeams) start(_ context.Context, spec orchestrator.Spec, _ string, sealKey []byte) (string, error) {
	s.calls = append(s.calls, "start")
	s.spec = spec
	s.sealKey = sealKey
	return s.startID, s.startErr
}

// TestResidentLaunchTakesTheClaimMintsAndStarts is the order the design lists
// (section 4.2): the claim says this process is the one launching the run, the
// token is what the run spends under, and the execution is handed the key that
// opens it (D15 — an execution handed another key cannot open the token its own
// run id names).
func TestResidentLaunchTakesTheClaimMintsAndStarts(t *testing.T) {
	seams := newResidentSeams(t)
	spec := orchestrator.SpecOf(workItemOf("wi-1"))

	got, err := seams.launcher().Launch(context.Background(), spec, residentRun)
	if err != nil {
		t.Fatalf("a launch whose every step answered was refused: %v", err)
	}
	if got != residentRun {
		t.Errorf("the launcher answered %q for the run %q the state recorded: a second run id is a run the state does not carry", got, residentRun)
	}
	if !slices.Equal(seams.calls, []string{"claim", "mint", "start"}) {
		t.Fatalf("the launcher called %v, want the claim, the mint and the start in that order: the design's own sequence, and each step rests on the one before it", seams.calls)
	}
	if !bytes.Equal(seams.sealKey, seams.key) {
		t.Errorf("the execution was handed a seal key that is not the one the mint drew: the run cannot open its own budget token (D15)")
	}
	if !seams.at.Equal(o6Now()) {
		t.Errorf("the claim was stamped %s, and the launcher's own clock reads %s: the claim's stamp is the launcher's and not a remembered one", seams.at, o6Now())
	}
	if seams.spec != spec {
		t.Errorf("the mint was handed %+v, not the spec of the attempt the node holds (%+v)", seams.spec, spec)
	}
}

// TestResidentLaunchStartsNothingWhileAnotherLauncherHoldsTheClaim is F4's
// mechanism met where it matters: a run another launcher is between is a run
// this pass starts nothing for, it does not mint a token for it, and it gives
// back no claim it does not hold.
func TestResidentLaunchStartsNothingWhileAnotherLauncherHoldsTheClaim(t *testing.T) {
	seams := newResidentSeams(t)
	seams.taken, seams.holder = false, "laptop-4711"

	got, err := seams.launcher().Launch(context.Background(), orchestrator.SpecOf(workItemOf("wi-1")), residentRun)
	if err == nil {
		t.Fatal("a run another launcher holds the claim of was launched: two launchers may not start one run twice")
	}
	if got != "" {
		t.Errorf("the launcher answered %q for a run it did not claim", got)
	}
	if !strings.Contains(err.Error(), seams.holder) {
		t.Errorf("err = %v, want the holder named: an operator reading the line has to know which launcher to wait for", err)
	}
	if !slices.Equal(seams.calls, []string{"claim"}) {
		t.Errorf("the launcher called %v, want the claim alone: nothing is minted or started for a run this pass does not hold", seams.calls)
	}
}

// TestResidentLaunchGivesTheClaimBackWhenTheMintFails: nothing can have started
// before the token exists (PutObject is create-if-absent and the execution is
// started after it), so a mint that failed is a launch nothing made — and a
// claim left behind is a run that waits out ClaimTTL for it.
func TestResidentLaunchGivesTheClaimBackWhenTheMintFails(t *testing.T) {
	seams := newResidentSeams(t)
	seams.tokenErr = errors.New("the signer refused the mint")

	_, err := seams.launcher().Launch(context.Background(), orchestrator.SpecOf(workItemOf("wi-1")), residentRun)
	if err == nil {
		t.Fatal("a launch whose token was not minted was reported as one")
	}
	if !slices.Equal(seams.calls, []string{"claim", "mint", "release"}) {
		t.Fatalf("the launcher called %v, want the claim, the mint and the release: a claim taken for a launch nothing made goes back", seams.calls)
	}
	if !strings.Contains(err.Error(), "nothing was started") {
		t.Errorf("err = %v, want the line to say nothing was started: a refusal an operator cannot read as an outcome sends them looking for a run that does not exist", err)
	}
}

// TestResidentLaunchNamesBothFaultsWhenTheClaimCannotGoBack: the release is the
// second thing that can fail, and the line has to carry both — a claim left
// behind is a run that waits ClaimTTL for a launch nothing made.
func TestResidentLaunchNamesBothFaultsWhenTheClaimCannotGoBack(t *testing.T) {
	seams := newResidentSeams(t)
	seams.tokenErr = errors.New("the signer refused the mint")
	seams.releaseErr = errors.New("the bucket refused the delete")

	_, err := seams.launcher().Launch(context.Background(), orchestrator.SpecOf(workItemOf("wi-1")), residentRun)
	if err == nil {
		t.Fatal("a launch whose token was not minted was reported as one")
	}
	if !strings.Contains(err.Error(), "the signer refused the mint") || !strings.Contains(err.Error(), orchestrator.ClaimTTL.String()) {
		t.Errorf("err = %v, want the mint's own fault and the window the run now waits: a claim nobody gave back is a fact the line has to state", err)
	}
}

// TestResidentLaunchKeepsTheClaimWhenTheStartFails is the one step whose
// failure is genuinely unknown: the request may have been accepted before it
// failed, so the safe reading is that the run is going — and a released claim
// beside the next pass's re-issue of the same run id is exactly the second
// start the claim exists to prevent.
func TestResidentLaunchKeepsTheClaimWhenTheStartFails(t *testing.T) {
	seams := newResidentSeams(t)
	seams.startErr = errors.New("the platform refused the execution")

	_, err := seams.launcher().Launch(context.Background(), orchestrator.SpecOf(workItemOf("wi-1")), residentRun)
	if err == nil {
		t.Fatal("a start the platform refused was reported as a launch")
	}
	if !slices.Equal(seams.calls, []string{"claim", "mint", "start"}) {
		t.Fatalf("the launcher called %v, want no release: a start that failed may have been accepted, and the claim is what keeps the run from being started twice", seams.calls)
	}
	if !strings.Contains(err.Error(), "claim is kept") {
		t.Errorf("err = %v, want the line to say the claim stands: the next reader of that run has to know it will wait", err)
	}
}

// TestResidentLaunchRefusesAStepItHasNoSeamFor is the fail-closed guard: a
// deployment that gives this launcher no reach for one of the three steps gets
// a refusal naming the step, and nothing is called at all. It is the shape
// Starter.begin answers with, one file over, and it is what a resident service
// meets before its deployment can carry a launcher at all.
func TestResidentLaunchRefusesAStepItHasNoSeamFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		drop func(*orchestrator.ResidentLaunch)
		want string
	}{
		{name: "no claim", drop: func(r *orchestrator.ResidentLaunch) { r.Claim = nil }, want: "the run's launch claim"},
		{name: "no mint", drop: func(r *orchestrator.ResidentLaunch) { r.Token = nil }, want: "its budget token"},
		{name: "no start", drop: func(r *orchestrator.ResidentLaunch) { r.Start = nil }, want: "the job execution"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seams := newResidentSeams(t)
			l := seams.launcher()
			tc.drop(l)

			got, err := l.Launch(context.Background(), orchestrator.SpecOf(workItemOf("wi-1")), residentRun)
			if err == nil {
				t.Fatalf("a launcher missing %s launched a run", tc.name)
			}
			if got != "" {
				t.Errorf("the launcher answered %q where it has no %s", got, tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want the missing step named as %q: an operator has to know which step their deployment does not carry", err, tc.want)
			}
			if len(seams.calls) != 0 {
				t.Errorf("the launcher called %v beside a step it does not carry: a launch it cannot make is one it must not begin", seams.calls)
			}
		})
	}
}

// TestResidentLaunchCarriesOutANodesOwnReissue is the launcher half end to end
// and the half of the design that says what it is for: the attempt is already
// on the node — a person's patch committed it — and what is left is to start
// it. The starter a resident pass holds carries a launcher and NOTHING else,
// which is the same sentence as "this process writes no node", and the run id
// is the one the intent recorded and never a second one.
func TestResidentLaunchCarriesOutANodesOwnReissue(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	w, it := o6OpenItem("wi-1", residentRun, approvedAt, now.Add(-orchestrator.ClaimTTL))
	s := o6Node(t, now, w)
	s.Intents = []orchestrator.Intent{it}

	requests, others := orchestrator.ResidentRequests(s, orchestrator.RunView{}, o6Caps(), now)
	if len(others) != 0 {
		t.Fatalf("the node's own attempt was read as %+v beside the re-issue: an attempt committed and not started is a launch request", others)
	}
	a := onlyAction(t, requests)
	if a.Kind != orchestrator.ActionReissue || a.RunID != residentRun || a.IntentID != it.ID {
		t.Fatalf("the pass reads %+v, not a re-issue of %q under %q", a, it.ID, residentRun)
	}

	seams := newResidentSeams(t)
	d := orchestrator.Starter{Launcher: seams.launcher()}
	if _, err := d.Start(context.Background(), s, a, now); err != nil {
		t.Fatalf("the node's own re-issue was refused: %v", err)
	}
	if !slices.Equal(seams.calls, []string{"claim", "mint", "start"}) {
		t.Fatalf("the re-issue called %v, want the three steps and no more: a re-issue starts the attempt that was already committed", seams.calls)
	}
	if seams.spec != orchestrator.SpecOf(w) {
		t.Errorf("the launcher was handed %+v, and the node holds %+v: what a launcher starts is the spec the state carries", seams.spec, orchestrator.SpecOf(w))
	}

	// And the identity of a start is the state's record: a launcher that comes
	// back with another run id is a run the state does not carry.
	against := newResidentSeams(t)
	against.startID = "20261117-050000-ffff"
	if _, err := (orchestrator.Starter{Launcher: against.launcher()}).Start(context.Background(), s, a, now); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("a launcher that answered %q for the attempt recorded as %q was accepted: %v", against.startID, residentRun, err)
	}
}

// TestResidentRequestsAreTheNodesOwnLaunchRequests is the split a resident pass
// acts on: the launch requests the node's own record already carries are the
// re-issue's, everything else a plan derives is a write's — and the ceilings a
// pass cannot read do not bound a re-issue, because it starts the attempt that
// was already committed and counted. The budget switch is the exception, and it
// is not a ceiling: it stops the re-issues too, which is the one thing a pass
// has to read rather than assume.
func TestResidentRequestsAreTheNodesOwnLaunchRequests(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	w, it := o6OpenItem("wi-1", residentRun, approvedAt, now.Add(-time.Minute))
	s := o6Node(t, now, w)
	s.Intents = []orchestrator.Intent{it}

	// Inside the claim window the attempt is the platform's: a launch beside it
	// may still be starting, so there is nothing for this pass to read.
	if requests, others := orchestrator.ResidentRequests(s, orchestrator.RunView{}, o6Caps(), now); len(requests)+len(others) != 0 {
		t.Fatalf("the pass read %+v / %+v inside the attempt's claim window", requests, others)
	}

	// Past the window the request is the re-issue, and the ceilings a pass
	// cannot read as anything but zero do not bound it: what recovery re-issues
	// was committed and counted already. The SWITCH is not one of those zeros —
	// the field's zero value reads as OFF, which is the permissive direction and
	// the reason Caps' doc states the rule the other way round ("a switch it
	// could not read is on") — so every call below writes it: a pass hands in
	// what it read, and a switch it could not read is handed in as true.
	later := now.Add(orchestrator.ClaimTTL)
	requests, others := orchestrator.ResidentRequests(s, orchestrator.RunView{}, orchestrator.Caps{KillSwitch: false}, later)
	a := onlyAction(t, requests)
	if a.Kind != orchestrator.ActionReissue || a.RunID != residentRun || a.IntentID != it.ID {
		t.Fatalf("the pass read %+v under the zero ceilings, not the re-issue of %q", a, it.ID)
	}
	if len(others) != 0 {
		t.Fatalf("the pass left %+v beside the node's own re-issue", others)
	}

	// And the switch DOES bound it, which is the one rung no ceiling reaches:
	// planNode's first line turns the whole node off, recovery included, so what
	// a pass carries out under a switch that is on is nothing at all — and that
	// is the value a pass whose read FAILED hands in, which is why a `fugaro
	// budget kill` stops the re-issues and not only the first launches.
	if requests, others := orchestrator.ResidentRequests(s, orchestrator.RunView{}, orchestrator.Caps{KillSwitch: true}, later); len(requests)+len(others) != 0 {
		t.Fatalf("the pass planned %+v / %+v under a switch that is on, and the switch stops every request a pass carries out", requests, others)
	}

	// A run that started is in flight however long it takes, and no window
	// makes it a second launch.
	if requests, others := orchestrator.ResidentRequests(s, orchestrator.RunView{Runs: []orchestrator.Run{{RunID: residentRun, Started: true}}}, o6Caps(), later); len(requests)+len(others) != 0 {
		t.Fatalf("the pass planned %+v / %+v for a run that had started", requests, others)
	}

	// An item a person approved and nothing has started yet is a WRITE: the
	// first launch is the attempt's own recording, and the ceilings a pass
	// cannot read — every one of them zero, with the switch read as off — plan
	// none of it.
	fresh := o6Node(t, now, o6Item("wi-2", orchestrator.PhaseQueued, approvedAt))
	requests, others = orchestrator.ResidentRequests(fresh, orchestrator.RunView{}, o6Caps(), now)
	if a := onlyAction(t, others); a.Kind != orchestrator.ActionLaunch {
		t.Fatalf("the plan left %+v for an approved item, not the first launch", a)
	}
	if len(requests) != 0 {
		t.Fatalf("the pass read %+v as a launch request of the node, and nothing committed that attempt", requests)
	}
	if requests, others := orchestrator.ResidentRequests(fresh, orchestrator.RunView{}, orchestrator.Caps{KillSwitch: false}, now); len(requests)+len(others) != 0 {
		t.Fatalf("the zero ceilings planned %+v / %+v, and a zero cap is no room at all", requests, others)
	}

	// An attempt whose spec a person edited after the intent was committed is
	// an abandon, which is a write as well: it closes the intent rather than
	// starting anything.
	edited := o6Edit(t, s, "wi-1", "A different brief.", now)
	requests, others = orchestrator.ResidentRequests(edited, orchestrator.RunView{}, o6Caps(), later)
	if a := onlyAction(t, others); a.Kind != orchestrator.ActionAbandon {
		t.Fatalf("the plan left %+v for an edited attempt, not the abandon", a)
	}
	if len(requests) != 0 {
		t.Fatalf("the pass read %+v as a launch request, and nothing may start under the abandoned intent", requests)
	}
}
