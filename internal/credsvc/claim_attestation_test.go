package credsvc

// BR2's tests: the attestation a Batch run's claim is served on (R1, R18,
// D3108, D3114). The claim serves a run's identity to a verified job account
// whose claim carries an attestation the link accepts for the job the record
// names, and to nobody else.
//
// BR3's link is not merged, and nothing here is written against it: C1 declares
// the interface (identitywire.VMIdentity), and the fake below is BR2's own
// double for it — the platform as the contract describes it, answering the one
// question the claim asks, false for every VM that is not that job's and an
// error only when its read failed. What each test holds is the ROUTE's side of
// that answer: served or refused, the status and the code, exactly one audit
// line and never a line carrying the attestation, no signature on a refusal,
// and the ticket left unclaimed — a refused claim that marked it would burn the
// ticket the real run is waiting to exchange.
//
// The fake is deliberately not a guess at what BR3 reads: nothing here parses a
// token, and the shape of the link's answer is C1's (a bool and an error). The
// margins the platform has — the project and the region, the bound and its
// clock tolerance — are in the fake only so that each scenario the acceptance
// names is a state a test says out loud; the window's own arithmetic is the
// link's (BR3, Check 830) and the one thing BR2 holds about it is the stamp the
// route measures it from.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// The installation and its platform, as these tests state them: the project
// every job and VM of the fixture is in, and the region it runs in — a zone in
// the metadata server's spelling, "<region>-<letter>".
const (
	attestedProject = testProject
	attestedRegion  = "europe-west1"
	attestedZone    = "europe-west1-b"
)

// The two jobs one launcher could have created, and the attestations of the VMs
// that run them. The attestations are names and not credentials: three
// dot-separated base64url segments is what Attestation.WellFormed reads (C1),
// and no VM ever issued either of these.
const (
	attestedJob      identitywire.JobName = "projects/" + attestedProject + "/locations/" + attestedRegion + "/jobs/fb-20261120-041919-8271"
	otherAttestedJob identitywire.JobName = "projects/" + attestedProject + "/locations/" + attestedRegion + "/jobs/fb-20261120-041919-8272"

	attestedJobID = "job-8271"

	testAttestation  identitywire.Attestation = "a-vm-of-this-job.a-header.and-a-signature"
	otherAttestation identitywire.Attestation = "a-vm-of-another-job.a-header.and-a-signature"

	// testStrangerSA is a service account of no table of this installation: it
	// is not a job, it is not a shared project job's and it is not the review
	// job's, so the claim verifies no caller at all for it.
	testStrangerSA = "stranger@aurora-dev.iam.gserviceaccount.com"
)

// ---------------------------------------------------------------------------
// The fake link (C1's identitywire.VMIdentity, BR3's half)
// ---------------------------------------------------------------------------

// fakeVMState is one VM of the fake platform: the job it is a VM of, and the
// two facts of its token that say which VM it is.
type fakeVMState struct {
	job     identitywire.JobName
	project string
	zone    string
}

// fakeJobState is one Batch job of the fake platform: the id the platform gave
// it, when it was created, whether it has finished, and where it runs.
type fakeJobState struct {
	id       string
	created  time.Time
	finished bool
	project  string
	location string
}

// fakeLinkCall is one question the route asked, recorded as it was asked: what
// the route hands the link is half of what these tests hold.
type fakeLinkCall struct {
	attestation identitywire.Attestation
	job         identitywire.JobName
	jobID       string
	recordedAt  time.Time
}

// fakeVMLink is the link BR2 is tested against, and it answers VMsJob the way
// C1 says a correct link answers: true only for a VM of the job, of a job that
// is live, created within the bound of recordedAt (ClaimClockTolerance
// included) and holding the registered id; false for everything else; and an
// error only when its read failed.
type fakeVMLink struct {
	mu      sync.Mutex
	vms     map[identitywire.Attestation]fakeVMState
	jobs    map[identitywire.JobName]fakeJobState
	readErr error
	calls   []fakeLinkCall
}

var _ identitywire.VMIdentity = (*fakeVMLink)(nil)

func newFakeVMLink() *fakeVMLink {
	return &fakeVMLink{
		vms:  map[identitywire.Attestation]fakeVMState{},
		jobs: map[identitywire.JobName]fakeJobState{},
	}
}

// runningJob wires the state a correct link reads: one live job, and the VM
// that runs it. It is where every test but the one that varies it starts from.
func (l *fakeVMLink) runningJob(att identitywire.Attestation, job identitywire.JobName, jobID string, created time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.vms[att] = fakeVMState{job: job, project: attestedProject, zone: attestedZone}
	l.jobs[job] = fakeJobState{id: jobID, created: created, project: attestedProject, location: attestedRegion}
}

// setVM replaces one VM's own facts, for the tests that state a VM the right
// one is not.
func (l *fakeVMLink) setVM(att identitywire.Attestation, vm fakeVMState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.vms[att] = vm
}

// fail makes the link's read fail, which is the one answer that is not a
// decision about the caller.
func (l *fakeVMLink) fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.readErr = err
}

// asked is every question the route put to the link, in order.
func (l *fakeVMLink) asked() []fakeLinkCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]fakeLinkCall(nil), l.calls...)
}

func (l *fakeVMLink) VMsJob(_ context.Context, attestation identitywire.Attestation, job identitywire.JobName, jobID string, recordedAt time.Time) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, fakeLinkCall{attestation: attestation, job: job, jobID: jobID, recordedAt: recordedAt})
	if l.readErr != nil {
		return false, l.readErr
	}
	vm, known := l.vms[attestation]
	if !known || vm.job != job {
		return false, nil
	}
	j, live := l.jobs[job]
	if !live || j.finished || j.id != jobID {
		return false, nil
	}
	if vm.project != attestedProject || !strings.HasPrefix(vm.zone, attestedRegion+"-") {
		return false, nil
	}
	if j.project != attestedProject || j.location != attestedRegion {
		return false, nil
	}
	if j.created.Before(recordedAt.Add(-identitywire.ClaimClockTolerance)) ||
		j.created.After(recordedAt.Add(identitywire.ClaimBound+identitywire.ClaimClockTolerance)) {
		return false, nil
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// newAttestedClaimFixture is the claims fixture with a link wired (C1's
// IdentityConfig.VMIdentity): the same service LM2's tests are built on, with
// the one field BR2 reads filled by the fake above.
func newAttestedClaimFixture(t *testing.T) (*claimFixture, *fakeVMLink) {
	t.Helper()
	f := newClaimFixture(t)
	link := newFakeVMLink()
	f.server.cfg.Identity.VMIdentity = link
	return f, link
}

// batch is the record a Batch launch leaves: the fixture's own run, claimed on
// the job the launcher named and the id its registration wrote (R19), with
// everything else the claims fixture writes.
func (f *claimFixture) batch(t *testing.T, job identitywire.JobName, jobID string, mutate ...func(*identitywire.Record)) (string, identitywire.Record) {
	t.Helper()
	return f.issued(t, append([]func(*identitywire.Record){
		func(rec *identitywire.Record) { rec.Job, rec.JobID = job, jobID },
	}, mutate...)...)
}

// attestedClaimBody is a Batch run's claim: the ticket the launcher asked for,
// the nonce the runner drew, and the claiming VM's own identity token beside
// them (R18).
func attestedClaimBody(t *testing.T, ticket string, att identitywire.Attestation, nonce string) string {
	t.Helper()
	return mustBody(t, identitywire.ClaimRequest{Ticket: ticket, Nonce: nonce, Attestation: att})
}

// wantAttestationRefusal is the whole of what a refused Batch claim must be:
// the status and the contract's code the caller is answered, exactly one audit
// line naming the run and the repository it was refused for, no signature, and
// the ticket still unclaimed.
func wantAttestationRefusal(t *testing.T, f *claimFixture, resp *http.Response, body []byte, status int, auditStatus string) {
	t.Helper()
	wantRefusal(t, resp, body, status, identitywire.RefusalIdentityRefused)
	lines := f.identityLines(t)
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
	}
	wantIdentityLine(t, lines[0], f.run, f.slug, auditStatus)
	f.wantNothingSigned(t)
	f.wantUnclaimed(t)
}

// ---------------------------------------------------------------------------
// The claim the identity is served to
// ---------------------------------------------------------------------------

// TestTheIdentityIsServedToTheJobsOwnVM is R1's happy path. A record that names
// the job the launcher created, a claim carrying the attestation of a VM of
// that job, and the link's true answer: the run is served its identity.
//
// The launcher's class is the record's and decides nothing here (R20, D3123):
// the claim is the run's own job account's call whatever launched it, and the
// two classes a launch can have are served identically.
//
// The record is written ten minutes before the claim and the job created five
// minutes after the ask, which is what says the link is asked about the
// RECORD's own stamp — the window C1 states is measured from it — and not about
// this service's clock now: a job created five minutes ago sits in a window
// drawn around the record's stamp and outside one drawn around now.
func TestTheIdentityIsServedToTheJobsOwnVM(t *testing.T) {
	for _, launch := range []struct {
		name  string
		class identitywire.CallerClass
	}{
		{"launched by a person", identitywire.ClassPerson},
		{"launched by a roster service account", identitywire.ClassServiceAccount},
	} {
		t.Run(launch.name, func(t *testing.T) {
			f, link := newAttestedClaimFixture(t)
			ticket, rec := f.batch(t, attestedJob, attestedJobID, func(r *identitywire.Record) {
				r.Class = launch.class
				r.At = f.now.Add(-10 * time.Minute)
			})
			link.runningJob(testAttestation, attestedJob, attestedJobID, f.now.Add(-5*time.Minute))

			ans := f.claimOK(t, attestedClaimBody(t, ticket, testAttestation, testNonce))
			if ans.Token == "" {
				t.Error("the claim is served no token: a VM of the job is the one caller this route serves (R1)")
			}
			if calls := f.iam.SignCalls(); len(calls) != 1 {
				t.Errorf("the signer was asked %d times, want once: a served claim is one signature", len(calls))
			}

			asked := link.asked()
			if len(asked) != 1 {
				t.Fatalf("the link was asked %d times, want once", len(asked))
			}
			want := fakeLinkCall{attestation: testAttestation, job: attestedJob, jobID: attestedJobID, recordedAt: rec.At}
			if asked[0] != want {
				t.Errorf("the link was asked %+v, want %+v: the claim's own attestation, the job and the id the launch wrote, and the record's stamp the bound is measured from (C1)", asked[0], want)
			}
			// A served call writes no line at all: audit.go's rule is one line
			// per REFUSAL once the caller is verified, and this is the act the
			// whole route exists for.
			if f.audit.Len() != 0 {
				t.Errorf("a served claim wrote an audit line: %s", f.audit.String())
			}
			if held := f.issuance.record(t, f.slug, f.run); held.Claimed == (identitywire.Claim{}) {
				t.Error("the served claim left the ticket unclaimed, so a second claim could take it (R5)")
			}
		})
	}

	t.Run("a Cloud Run run's claim is served and asks the link nothing", func(t *testing.T) {
		f, link := newAttestedClaimFixture(t)
		ticket, _ := f.issued(t)

		f.claimOK(t, identityClaimBody(t, ticket, testNonce))
		if asked := link.asked(); len(asked) != 0 {
			t.Errorf("the link was asked %+v about a record that names no job: a Cloud Run run's claim is the claim of the release behind, and the account check is the whole of what serves it (R18)", asked)
		}
	})
}

// TestAnAttestationFromAnotherJobsVMIsRefused is the whole point of the
// attestation. Every job one launcher creates runs as the same job service
// account (F4), so the account alone cannot tell two runs apart; the VM's token
// is what does, and a VM of another job's is refused however right the account,
// the ticket and the record are.
func TestAnAttestationFromAnotherJobsVMIsRefused(t *testing.T) {
	t.Run("a VM of another live job", func(t *testing.T) {
		f, link := newAttestedClaimFixture(t)
		ticket, _ := f.batch(t, attestedJob, attestedJobID)
		// The claim carries the other job's VM token, and that job is live and
		// created on time: the ONLY thing wrong is whose VM presented it.
		link.runningJob(otherAttestation, otherAttestedJob, "job-8272", f.now)

		resp, body := f.claimCall(t, attestedClaimBody(t, ticket, otherAttestation, testNonce))
		wantAttestationRefusal(t, f, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)
		// The link is what said no, and the route did ask it.
		if asked := link.asked(); len(asked) != 1 {
			t.Errorf("the link was asked %d times, want once", len(asked))
		}
	})

	t.Run("a token no VM of this installation has", func(t *testing.T) {
		f, link := newAttestedClaimFixture(t)
		ticket, _ := f.batch(t, attestedJob, attestedJobID)
		link.runningJob(testAttestation, attestedJob, attestedJobID, f.now)

		resp, body := f.claimCall(t, attestedClaimBody(t, ticket, otherAttestation, testNonce))
		wantAttestationRefusal(t, f, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)
	})
}

// TestAnAccountOutsideTheJobTableIsRefused: the caller is not this run's
// account. It IS one of the installation's job accounts — the other
// repository's — so the service verifies it and the refusal is audited, and it
// is not the account the launch named, so the claim is refused BEFORE the
// platform is read: a claim that is not this run's costs no read of the job.
func TestAnAccountOutsideTheJobTableIsRefused(t *testing.T) {
	f, link := newAttestedClaimFixture(t)
	ticket, _ := f.batch(t, attestedJob, attestedJobID)
	link.runningJob(testAttestation, attestedJob, attestedJobID, f.now)

	resp, body := f.claimCall(t, attestedClaimBody(t, ticket, testAttestation, testNonce), f.jobBearer(testOtherSA))
	wantAttestationRefusal(t, f, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)
	if asked := link.asked(); len(asked) != 0 {
		t.Errorf("the link was asked %+v about a claim from an account that is not the run's: the VM question is asked only once the record's own checks have passed", asked)
	}
}

// TestAPersonClassAndAnUnlistedServiceAccountAreRefused: the claim's class
// fence, which is C1's table and does not change with this plan (D3123, R20).
// The claim is served to a run's own job service account and to nobody else: a
// person's own Google ID token is not one, and neither is a service account the
// installation holds in no table. Both are refused before a caller is verified,
// so neither writes an audit line (audit.go's rule) and the link is never
// asked.
func TestAPersonClassAndAnUnlistedServiceAccountAreRefused(t *testing.T) {
	for _, caller := range []struct {
		name   string
		mutate func(*claimFixture) func(*http.Request)
	}{
		{"a person's own token", func(f *claimFixture) func(*http.Request) {
			tok := f.launcherToken()
			return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
		}},
		{"a service account in no table", func(f *claimFixture) func(*http.Request) {
			return f.jobBearer(testStrangerSA)
		}},
	} {
		t.Run(caller.name, func(t *testing.T) {
			f, link := newAttestedClaimFixture(t)
			ticket, _ := f.batch(t, attestedJob, attestedJobID)
			link.runningJob(testAttestation, attestedJob, attestedJobID, f.now)

			resp, body := f.claimCall(t, attestedClaimBody(t, ticket, testAttestation, testNonce), caller.mutate(f))
			wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalCallerNotAdmitted)
			if f.audit.Len() != 0 {
				t.Errorf("the refusal wrote an audit line: %s\na caller refused before it was verified has no verified identity for a line to name (audit.go)", f.audit.String())
			}
			f.wantNothingSigned(t)
			f.wantUnclaimed(t)
			if asked := link.asked(); len(asked) != 0 {
				t.Errorf("the link was asked %+v by a caller this route does not admit", asked)
			}
		})
	}
}

// TestARecreatedJobIsRefusedByItsId is F30 and D3108, which is what the
// registration's id is for. A Cloud Batch job's name is reusable — the platform
// may hand the same name to a second job — so a record's job name is not enough
// to hold a VM to: the id the launcher registered is, and a job re-created
// under that name carries another one, so a VM of the NEW job is refused by the
// record of the old launch.
//
// The id the link is asked about is the RECORD's and never one the route
// invents: that is the whole comparison.
func TestARecreatedJobIsRefusedByItsId(t *testing.T) {
	f, link := newAttestedClaimFixture(t)
	ticket, _ := f.batch(t, attestedJob, attestedJobID)
	// The same name, a second job: another id, and the VM the claim presents
	// is a VM of that second job.
	link.runningJob(testAttestation, attestedJob, "job-recreated-8271", f.now)

	resp, body := f.claimCall(t, attestedClaimBody(t, ticket, testAttestation, testNonce))
	wantAttestationRefusal(t, f, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)

	asked := link.asked()
	if len(asked) != 1 {
		t.Fatalf("the link was asked %d times, want once", len(asked))
	}
	if asked[0].jobID != attestedJobID {
		t.Errorf("the link was asked about the job id %q, want the one the registration wrote (%q): a job re-created under the same name is refused by the id and by nothing else (D3108)", asked[0].jobID, attestedJobID)
	}
}

// TestAStaleRecordIsRefused: R1's bound, which is what keeps a record from
// being claimable for as long as its ticket lives. The launcher's two calls —
// the ask and the create — are one API call apart, so a job created long before
// or long after the record was written is no job this record's ticket was for,
// and the link answers false.
//
// The record's own stamp is the reference and this service's clock is not: the
// window C1 states is measured from the write, with a stated tolerance either
// side because the two clocks are not one.
func TestAStaleRecordIsRefused(t *testing.T) {
	for _, stale := range []struct {
		name    string
		created func(f *claimFixture) time.Time
	}{
		{"a job created before the launch's bound", func(f *claimFixture) time.Time {
			return f.now.Add(-identitywire.ClaimClockTolerance - time.Minute)
		}},
		{"a job created long after the record was written", func(f *claimFixture) time.Time {
			return f.now.Add(identitywire.ClaimBound + identitywire.ClaimClockTolerance + time.Minute)
		}},
	} {
		t.Run(stale.name, func(t *testing.T) {
			f, link := newAttestedClaimFixture(t)
			ticket, _ := f.batch(t, attestedJob, attestedJobID)
			link.runningJob(testAttestation, attestedJob, attestedJobID, stale.created(f))

			resp, body := f.claimCall(t, attestedClaimBody(t, ticket, testAttestation, testNonce))
			wantAttestationRefusal(t, f, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)
		})
	}
}

// TestAVMOutsideTheProjectIsRefused: a VM of the installation's project only.
// The credentials service serves one installation's runs, so a token naming
// another project — a VM someone created elsewhere, or one of the same name in
// another project — is refused, and so is a VM outside the installation's
// region.
func TestAVMOutsideTheProjectIsRefused(t *testing.T) {
	for _, vm := range []struct {
		name  string
		state fakeVMState
	}{
		{"a VM of another project", fakeVMState{job: attestedJob, project: "another-project", zone: attestedZone}},
		{"a VM in another region", fakeVMState{job: attestedJob, project: attestedProject, zone: "us-central1-a"}},
	} {
		t.Run(vm.name, func(t *testing.T) {
			f, link := newAttestedClaimFixture(t)
			ticket, _ := f.batch(t, attestedJob, attestedJobID)
			link.runningJob(testAttestation, attestedJob, attestedJobID, f.now)
			link.setVM(testAttestation, vm.state)

			resp, body := f.claimCall(t, attestedClaimBody(t, ticket, testAttestation, testNonce))
			wantAttestationRefusal(t, f, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)
		})
	}
}

// TestTheBoundAllowsTheStatedClockTolerance: the window's own edges, which are
// the two minutes C1 states and not a second more. A job created at either edge
// is served — the record's stamp is this service's clock and the creation time
// is the platform's, and a claim refused for a few seconds of skew would be a
// run nobody could debug — and one second outside is refused.
//
// The arithmetic is the link's (BR3, Check 830) and it lives in the fake here;
// what BR2 holds is the input it is given, which is why every served row also
// asserts that the stamp the route handed over is the RECORD's own.
func TestTheBoundAllowsTheStatedClockTolerance(t *testing.T) {
	for _, edge := range []struct {
		name   string
		offset time.Duration
		served bool
	}{
		{"at the near edge of the tolerance", -identitywire.ClaimClockTolerance, true},
		{"a second inside the near edge", -identitywire.ClaimClockTolerance + time.Second, true},
		{"a second outside the near edge", -identitywire.ClaimClockTolerance - time.Second, false},
		{"at the far edge of the bound", identitywire.ClaimBound + identitywire.ClaimClockTolerance, true},
		{"a second inside the far edge", identitywire.ClaimBound + identitywire.ClaimClockTolerance - time.Second, true},
		{"a second outside the far edge", identitywire.ClaimBound + identitywire.ClaimClockTolerance + time.Second, false},
	} {
		t.Run(edge.name, func(t *testing.T) {
			f, link := newAttestedClaimFixture(t)
			ticket, rec := f.batch(t, attestedJob, attestedJobID)
			link.runningJob(testAttestation, attestedJob, attestedJobID, rec.At.Add(edge.offset))

			resp, body := f.claimCall(t, attestedClaimBody(t, ticket, testAttestation, testNonce))
			if edge.served {
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("a job created %s from the record's own stamp is refused: %d %s\nthe window C1 states is the record's stamp, its bound and the tolerance either side of it", edge.offset, resp.StatusCode, body)
				}
				asked := link.asked()
				if len(asked) != 1 || !asked[0].recordedAt.Equal(rec.At) {
					t.Errorf("the link was asked %+v, want the record's own stamp (%s): the window is measured from the launch's write and not from this service's clock now", asked, rec.At)
				}
				return
			}
			wantAttestationRefusal(t, f, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)
		})
	}
}

// TestEachRefusalWritesOneAuditLine: the rule this service's whole refusal path
// is built on (audit.go, R7), swept over every way a Batch claim can be
// refused. Each refusal answered after the caller was verified writes exactly
// one line — the run and the repository it was refused for and the status, and
// nothing else — and the call refused before it writes none: there is no
// verified identity for a line to name.
//
// The audit's status is its own closed vocabulary (identityAuditStatus) and not
// the code the caller is answered: what a 400 is recorded as and what a 403 is
// are two different classes of thing, and the sweep holds both.
func TestEachRefusalWritesOneAuditLine(t *testing.T) {
	registered := func(f *claimFixture, link *fakeVMLink) string {
		ticket, _ := f.batch(t, attestedJob, attestedJobID)
		link.runningJob(testAttestation, attestedJob, attestedJobID, f.now)
		return ticket
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f *claimFixture, link *fakeVMLink) (string, func(*http.Request))
		// status and code are what the caller is answered.
		status int
		code   string
		// audit is the status the ONE line of a verified refusal carries, and
		// "" is a call that writes no line at all.
		audit string
	}{
		{
			name: "a record that names a job and a claim with no attestation",
			setup: func(t *testing.T, f *claimFixture, link *fakeVMLink) (string, func(*http.Request)) {
				return attestedClaimBody(t, registered(f, link), "", testNonce), nil
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
		{
			name: "a claim carrying something that is no identity token",
			setup: func(t *testing.T, f *claimFixture, link *fakeVMLink) (string, func(*http.Request)) {
				return attestedClaimBody(t, registered(f, link), "not-an-identity-token", testNonce), nil
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
		{
			name: "a job the launcher never registered",
			setup: func(t *testing.T, f *claimFixture, link *fakeVMLink) (string, func(*http.Request)) {
				ticket, _ := f.batch(t, attestedJob, "")
				link.runningJob(testAttestation, attestedJob, attestedJobID, f.now)
				return attestedClaimBody(t, ticket, testAttestation, testNonce), nil
			},
			status: StatusUnverified, code: identitywire.RefusalIdentityRefused, audit: ErrorUnverifiedRunIdentity,
		},
		{
			name: "an installation whose link is not wired",
			setup: func(t *testing.T, f *claimFixture, link *fakeVMLink) (string, func(*http.Request)) {
				body := attestedClaimBody(t, registered(f, link), testAttestation, testNonce)
				f.server.cfg.Identity.VMIdentity = nil
				return body, nil
			},
			status: StatusUnavailable, code: identitywire.RefusalNotServed, audit: ErrorStateUnavailable,
		},
		{
			name: "a link whose read failed",
			setup: func(t *testing.T, f *claimFixture, link *fakeVMLink) (string, func(*http.Request)) {
				body := attestedClaimBody(t, registered(f, link), testAttestation, testNonce)
				link.fail(errors.New("the platform could not be read"))
				return body, nil
			},
			status: StatusUnavailable, code: ErrorStateUnavailable, audit: ErrorStateUnavailable,
		},
		{
			name: "a VM that is not the job's",
			setup: func(t *testing.T, f *claimFixture, link *fakeVMLink) (string, func(*http.Request)) {
				body := attestedClaimBody(t, registered(f, link), testAttestation, testNonce)
				link.setVM(testAttestation, fakeVMState{job: otherAttestedJob, project: attestedProject, zone: attestedZone})
				return body, nil
			},
			status: StatusUnverified, code: identitywire.RefusalIdentityRefused, audit: ErrorUnverifiedRunIdentity,
		},
		{
			name: "a caller this route does not admit",
			setup: func(t *testing.T, f *claimFixture, link *fakeVMLink) (string, func(*http.Request)) {
				body := attestedClaimBody(t, registered(f, link), testAttestation, testNonce)
				return body, f.jobBearer(testStrangerSA)
			},
			status: StatusUnverified, code: identitywire.RefusalCallerNotAdmitted, audit: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, link := newAttestedClaimFixture(t)
			body, mutate := tc.setup(t, f, link)

			var resp *http.Response
			var answer []byte
			if mutate == nil {
				resp, answer = f.claimCall(t, body)
			} else {
				resp, answer = f.claimCall(t, body, mutate)
			}
			wantRefusal(t, resp, answer, tc.status, tc.code)
			if tc.audit == "" {
				if f.audit.Len() != 0 {
					t.Fatalf("the call wrote an audit line, want none: a caller refused before it was verified has no identity for a line to name (audit.go)")
				}
				return
			}
			lines := f.identityLines(t)
			if len(lines) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
			}
			wantIdentityLine(t, lines[0], f.run, f.slug, tc.audit)
			f.wantNothingSigned(t)
			f.wantUnclaimed(t)
		})
	}
}

// TestNoAuditLineCarriesTheTicketOrTheAttestation: R7's rule for the two
// credentials a Batch claim carries. The attestation is the VM's identity token
// and the ticket is the secret the launcher asked for; neither reaches an audit
// line, a log line or an answer, and the service keeps no copy of either.
func TestNoAuditLineCarriesTheTicketOrTheAttestation(t *testing.T) {
	f, link := newAttestedClaimFixture(t)
	ticket, _ := f.batch(t, attestedJob, attestedJobID)
	link.runningJob(testAttestation, attestedJob, attestedJobID, f.now)

	// Every refusal this file is about, over one fixture: no attestation, a
	// value that is no token, and a VM that is not the job's.
	for _, body := range []string{
		attestedClaimBody(t, ticket, "", testNonce),
		attestedClaimBody(t, ticket, "not-an-identity-token", testNonce),
		attestedClaimBody(t, ticket, otherAttestation, testNonce),
	} {
		f.claimCall(t, body)
	}

	for _, buf := range []struct {
		name string
		text string
	}{
		{"the service's log", f.logs.String()},
		{"the audit", f.audit.String()},
	} {
		for _, secret := range []struct{ name, value string }{
			{"the attestation", string(testAttestation)},
			{"the other VM's attestation", string(otherAttestation)},
			{"the ticket", ticket},
			{"the nonce", testNonce},
		} {
			if strings.Contains(buf.text, secret.value) {
				t.Errorf("%s carries %s: the attestation is a credential of the VM and the ticket is the launcher's, and R7 confines both to the flows that need them", buf.name, secret.name)
			}
		}
	}

	// Not vacuous: the refusals did reach the audit, and their lines do name
	// the run they were refused for.
	if f.audit.Len() == 0 {
		t.Fatal("no refusal wrote a line at all, so this test is looking for values in a buffer nothing wrote")
	}
	if !strings.Contains(f.audit.String(), f.run) {
		t.Error("the audit lines do not name the run they refused, so this test would pass on a service that wrote nothing")
	}
}
