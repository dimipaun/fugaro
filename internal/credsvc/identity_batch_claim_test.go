package credsvc

import (
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// TestAJobRecordClaimedWithoutAnAttestationIsRefused is C1's rule and its red
// step (D3114, R18): a record that names a Batch job is served only to a VM of
// that job, so a claim on it that carries no attestation — or carries something
// that is no identity token — is refused before anything of the installation is
// read.
//
// What the rule is NOT is the whole of the check: which VM the attestation
// names and which job that VM belongs to are asked of the link
// (IdentityConfig.VMIdentity), and this test holds the account check alone to
// being not enough. Where the refusal is SERVED — the claim route's status, its
// code and its one audit line — is BR2's, and its own test is what holds that.
func TestAJobRecordClaimedWithoutAnAttestationIsRefused(t *testing.T) {
	rec := identitywire.Record{
		Job:   identitywire.JobName("projects/acme-sandbox/locations/europe-west1/jobs/fb-20261120-041919-8271"),
		JobID: "job-8271",
	}
	for _, tc := range []struct {
		name string
		att  identitywire.Attestation
	}{
		{"the field is absent", ""},
		{"the value is no token at all", "not-a-token"},
		{"the value is a token's first two segments", "h.p"},
		{"the value is one segment too many", "h.p.s.x"},
		{"the value carries a character base64url does not", "h+p+s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckClaimAttestation(rec, tc.att)
			if err == nil {
				t.Fatalf("a claim on a record naming the job %s was accepted with the attestation %q: the account proves the job and never the execution (F4), and a claim without the VM's own token would be served on the account check alone", rec.Job, tc.att)
			}
			if !errors.Is(err, ErrClaimAttestation) {
				t.Errorf("the refusal is %v, want ErrClaimAttestation: the route tells this refusal apart from a read that failed", err)
			}
			if tc.att != "" && strings.Contains(err.Error(), string(tc.att)) {
				t.Errorf("the refusal repeats what the claim sent: %v\nthe attestation is a credential of the claiming VM and reaches no error, no log line and no audit line (R7)", err)
			}
		})
	}

	// A token-shaped value passes this rule, and the test says what that means:
	// it is a spelling check, and a value no VM ever issued is not this rule's
	// to refuse — the link's answer is (R1).
	if err := CheckClaimAttestation(rec, "h.p.s"); err != nil {
		t.Errorf("CheckClaimAttestation refused a token-shaped attestation: %v", err)
	}
}

// TestARecordWithoutAJobIsACloudRunClaim is the other half of C1's rule (R18,
// D3114): a record that names no job is a Cloud Run run's, its claim carries no
// attestation, and the account check is the whole of what serves it — which is
// what leaves a Cloud Run launch exactly as it is.
//
// An attestation beside a record that names no job decides nothing and is not
// refused here: D3114's danger is the OMISSION — a claim that leaves the field
// out and falls back to the account check on a record that names a job — and a
// value with no job to bind it to is worth nothing either way. The rule is
// therefore one-directional, and this test pins that it is.
func TestARecordWithoutAJobIsACloudRunClaim(t *testing.T) {
	cloud := identitywire.Record{Caller: "dimi@example.com"}

	if cloud.NamesAJob() {
		t.Error("a record that names no job reports one: a Cloud Run run's record must read as the release behind's")
	}
	if cloud.JobID != "" {
		t.Errorf("a record that names no job carries the job id %q: R19 writes the id on a job, and a Cloud Run run has none", cloud.JobID)
	}
	if err := CheckClaimAttestation(cloud, ""); err != nil {
		t.Errorf("a Cloud Run run's claim is refused: %v\nits claim is the claim of the release behind, and the account check is the whole of what serves it (R18)", err)
	}
	if err := CheckClaimAttestation(cloud, "h.p.s"); err != nil {
		t.Errorf("an attestation beside a record that names no job is refused: %v\nthe rule is one-directional: what the claim carries with no job to bind it to decides nothing (D3114)", err)
	}

	// A Batch launch whose job is not registered yet is still a Batch launch:
	// the rule is the record's job and not its id, and the missing id is the
	// claim route's own refusal (R19, R1).
	unregistered := identitywire.Record{
		Job: identitywire.JobName("projects/acme-sandbox/locations/europe-west1/jobs/fb-20261120-041919-8271"),
	}
	if !unregistered.NamesAJob() {
		t.Error("a record naming a job whose id is not written yet reports no job: the id is written by the registration and the job is named by the ask (R19)")
	}
	if err := CheckClaimAttestation(unregistered, ""); !errors.Is(err, ErrClaimAttestation) {
		t.Errorf("a claim on a record naming an unregistered job with no attestation answered %v, want ErrClaimAttestation: the job's id and the attestation are two different checks, and neither stands in for the other", err)
	}
}

// TestTheTicketWritesTheAskJobsNameOnTheRecord is R19's first write, which this
// contract carries on the ticket route (LM1's, merged): the job the launcher
// named in its ask is written WITH the ticket, because the record is the only
// place the claim can learn whether an attestation is owed (D3114) — and an ask
// that names a job of another shape is refused before a record is written at
// all, so a record can never carry a name the platform could not have given.
func TestTheTicketWritesTheAskJobsNameOnTheRecord(t *testing.T) {
	f := newTickets(t)
	const job = identitywire.JobName("projects/fugaro-dev/locations/europe-west1/jobs/fb-20261120-041919-8271")

	// A Cloud Run launch: the ask names no job, and the record carries none
	// either — it is the record of the release behind.
	cloud := f.goodAsk()
	rec, body := f.ask(t, f.person(testLauncherAccount), cloud)
	f.issued(t, rec, body)
	if got := f.record(t, cloud.Slug, cloud.Run); got.NamesAJob() {
		t.Errorf("a Cloud Run ask's record names the job %q: a record that names no job is what keeps the claim of the release behind working (R18)", got.Job)
	}

	// A Batch launch: the name travels from the ask to the record unchanged.
	batch := f.goodAsk()
	batch.Job = job
	rec, body = f.ask(t, f.person(testLauncherAccount), batch)
	f.issued(t, rec, body)
	got := f.record(t, batch.Slug, batch.Run)
	if got.Job != job {
		t.Errorf("the record names the job %q, want the one the ask named (%q): R19 writes the job with the ticket, and it is the field the claim is held to", got.Job, job)
	}
	if got.JobID != "" {
		t.Errorf("the record carries the job id %q: the id is the registration's write and nothing else's (R19)", got.JobID)
	}

	// A name the platform could never have given is refused, and nothing is
	// written: the launcher is told before it creates anything to name.
	bad := f.goodAsk()
	bad.Job = "not-a-job"
	rec, body = f.ask(t, f.person(testLauncherAccount), bad)
	e := f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
	if !strings.Contains(e.Message, "full resource name") {
		t.Errorf("the refusal does not say what the job must be: %s", e.Message)
	}
}
