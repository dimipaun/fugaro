package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// This file is BR4's half of the claim: what a Batch run's claim carries, that
// neither the ticket nor the attestation of that claim is printed anywhere, and
// the ways a Batch run ends with no identity at all. LM7's claim path and its
// tests are the Cloud Run half (identity_claim_test.go), and the run below is
// the same run with its job on Batch: a job the launcher provisioned an
// identity for -- the ticket (BR1) -- and one nobody launched, which carries
// neither a ticket nor the marker of the release behind and is therefore not
// this installation's job at all.
//
// The attestation is the whole of the difference. A Batch job runs as an
// account every job the installation launches shares, so its claim carries the
// run's own VM identity token in the metadata server's full format, and the
// service serves the identity to a VM of the job the record names (U2, D3114).
// The tests read it where the service reads it -- in the claim's body -- and
// nowhere else: the value is this VM's credential, and no reason, record or log
// line of the run may carry it (R7).

// vmIdentityFixture is what the metadata stand-in mints for this run's own VM.
// A real answer is a signed JWT in the full format; this is a fixture's own
// value and never a credential, and it is deliberately unlike the job's own ID
// token so that a reader can tell which of the two a claim carried.
const vmIdentityFixture = "fixture-vm-identity-token-in-full-format-not-a-credential"

// onBatch turns a fixture's run into one whose job is on Cloud Batch, as the
// launcher leaves one:
//
//   - the platform's own signals, BATCH_TASK_INDEX and BATCH_TASK_COUNT, which
//     are what makes this process one of Batch's tasks (backend.BatchCodec's
//     On) and what places it by the deployed job below rather than by a Cloud
//     Run name;
//   - FUGARO_BACKEND, which the launcher writes into every job it creates and
//     which is how the run knows its claim must carry an attestation;
//   - FUGARO_DEPLOYED_JOB, the deployed job the run stands in for, without
//     which a Batch run is not placed at all (BR9);
//   - the job's own resource name as the execution, which on Batch is the whole
//     of a run's platform identity -- one job is one run -- derived as the
//     launcher derives it (batch.JobID);
//   - a metadata stand-in answering the read that asks for the full format with
//     this VM's identity and every other read with the job's own ID token,
//     which is what a Batch VM's metadata server serves.
func onBatch(t *testing.T, b *bk) {
	t.Helper()
	onBatchMeta(t, b, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") == "full" {
			_, _ = io.WriteString(w, vmIdentityFixture)
			return
		}
		_, _ = io.WriteString(w, beatIDToken)
	})
}

// onBatchMeta is onBatch with the metadata stand-in's own answer: a test that
// needs the VM identity read to fail -- the attestation arm of claimCallDetail
// -- answers the full-format read with a refusal and the job's own ID token
// read as usual, which is the state of a run on a VM whose metadata server will
// not serve its identity.
func onBatchMeta(t *testing.T, b *bk, meta http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(meta)
	t.Cleanup(srv.Close)
	b.deps.Local = false
	b.deps.Project = "aurora"
	b.deps.Execution = "projects/aurora-fp/locations/europe-west1/jobs/" + batch.JobID(repoSlug, "app", runID)
	b.deps.Env = append(filterEnv(filterEnv(filterEnv(filterEnv(b.deps.Env, backend.BackendEnv),
		backend.DeployedJobEnv), "BATCH_TASK_INDEX"), runner.MetadataHostEnv),
		backend.BackendEnv+"="+backend.CloudBatch,
		backend.DeployedJobEnv+"="+gcp.JobName(repoSlug, gcp.WorkflowVariant("app", "")),
		"BATCH_TASK_INDEX=0", "BATCH_TASK_COUNT=1",
		runner.MetadataHostEnv+"="+strings.TrimPrefix(srv.URL, "http://"))
}

// TestRunWithNoTicketStops: a Batch task carries the identity ticket its
// launcher asked the installation's credentials service for, or it is not a
// task this installation launched (BR1, R18). The fixture is that task with
// nothing on it -- no ticket, no marker of the release behind, and no token
// object in the bucket -- and the run stops with the upgrade, `fugaro update`
// and the relaunch as its fix, having run no stage.
//
// The two environments below are one refusal. The first is a task the launcher
// wrote FUGARO_BACKEND into; the second carries only the pair Batch itself
// injects, BATCH_TASK_INDEX and BATCH_TASK_COUNT, which is the platform's own
// signal (backend.DetectPlatform) and what a task nobody launched, or one whose
// environment somebody emptied, may well be without. The refusal is keyed on
// that signal, so both stop -- the review finding on this change: a guard
// reading the launcher's own variable let the second run to completion with no
// session, no cap and no kill switch.
//
// What the refusal keeps the run from is the fail-open direction of the policy
// read: a task nothing was provisioned for is answered "this installation has
// published nothing", which is the right answer for a Cloud Run launch whose
// launcher minted nothing and the wrong one for a Batch task, where a launcher
// that left nothing launched nothing. The run would then open no session at
// all: the stages below would run on the environment ceiling, with no cap to
// reach and no kill switch to answer.
//
// refusedAdmission is what holds the placement, and its credential count is
// what makes it observable: the refusal is met at the policy read
// (budgetClient), before admission and before the provider is opened, so the
// run hands out no git credential and clones nothing. The same refusal met at
// startBudgetBackend would come after the clone -- the fail-open direction
// M2's own rule names, and the second review finding on this change.
//
// The other side of the same refusal is what makes it safe to make: a job
// carrying the marker alone is the launch of the release behind, and
// TestRunnerStillOpensASealedToken is that run, which goes on with it (R7).
//
// Mutations (run, restored): drop the check from budgetClient. The run goes on
// with no identity at all -- the policy read's answer above stands, the
// stray-token check finds no object to refuse -- and this test fails on the
// error it never got: the run ends with the stages it was scripted. Key the
// check on FUGARO_BACKEND again (backend.BackendEnv): the second environment
// goes on, and fails on the same thing. Move the check back to
// startBudgetBackend: both environments still stop, and both fail on the
// credential count refusedAdmission reads.
func TestRunWithNoTicketStops(t *testing.T) {
	for _, tc := range []struct {
		name string
		// strip drops FUGARO_BACKEND from the job's environment, leaving the
		// pair Batch injects as the only thing that says which platform this
		// process is on.
		strip bool
	}{
		{name: "the launcher's own backend variable"},
		{name: "only the platform's own signal", strip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := admissionRun(t, "github", "acme/app", "off", "", "")
			// The launch left nothing at all: no marker in the job (unmarked) and
			// no token object for this run in the bucket.
			b.unmarked()
			if err := token.DeleteObject(context.Background(), b.bucket, repoSlug, runID); err != nil {
				t.Fatal(err)
			}
			onBatch(t, b)
			if tc.strip {
				b.deps.Env = filterEnv(b.deps.Env, backend.BackendEnv)
			}

			rec, err := b.run(t, implement("feature"), review("ship", 0))
			refusedAdmission(t, b, rec, err,
				backend.CloudBatch, identitywire.TicketEnv, "fugaro update", "brew upgrade", "launch the run again")
			if got := len(claimCalls(t, b.fake)); got != 0 {
				t.Errorf("the run claimed its identity %d times with no identity to claim, want none: an identity taken here would carry no session, no cap and no kill switch", got)
			}
		})
	}
}

// TestRefusedClaimStops is the second of the three refusals, and the Batch half
// of TestRunnerHaltsWhenTicketAlreadyClaimed: the service answered the claim
// with the contract's own code for a ticket that was already claimed (R5). The
// run halts -- no session, no cap and no kill switch is worse than stopping, and
// a refusal is an answer, never something to ask again -- with the code and the
// fix in the reason a person reads.
//
// The attestation is pinned beside the refusal because it is read before the
// answer is: a Batch run's refusal is the same refusal, reached through the
// same call, with the VM's identity in the body the service refused.
//
// Mutation (run, restored): retry a refusal as if it were an unanswered call
// (claimRetryable answering true for a refusal code). The service is asked
// again and again until the bound is spent, and this test fails on the number
// of calls: a claim is not a vote.
func TestRefusedClaimStops(t *testing.T) {
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	onBatch(t, b)
	claimed(t, b)
	body, err := json.Marshal(credsvc.WireError{Error: identitywire.RefusalIdentityRefused, Message: "the ticket was already claimed"})
	if err != nil {
		t.Fatal(err)
	}
	answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
		return anthropicfake.Reply{Status: http.StatusConflict, Header: jsonHeader(), Body: string(body)}
	})

	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatalf("a refused claim halts the run, and a halt is an outcome and not an error: %v", err)
	}
	if rec == nil || rec.Status != runstore.StatusHalted || rec.Halt == nil {
		t.Fatalf("a Batch run whose claim was refused did not halt: %+v", rec)
	}
	if rec.Halt.Reason != runstore.HaltBudgetUnavailable {
		t.Errorf("the halt is %+v, want %s", rec.Halt, runstore.HaltBudgetUnavailable)
	}
	for _, want := range []string{"already claimed", identitywire.RefusalIdentityRefused, "fugaro run"} {
		if !strings.Contains(rec.Halt.Detail, want) {
			t.Errorf("the halt's detail does not name %q: %q", want, rec.Halt.Detail)
		}
	}
	calls := claimCalls(t, b.fake)
	if len(calls) != 1 {
		t.Fatalf("the service was asked to claim %d times after refusing one, want one: a refusal is a decision and asking again answers the same thing", len(calls))
	}
	if got := claimOf(t, calls[0]).Attestation; got != vmIdentityFixture {
		t.Errorf("the refused claim carries the attestation %q, want this VM's own identity token in full format", got)
	}
	if len(b.agent.calls) != 0 {
		t.Errorf("%d stages ran on a run whose claim was refused", len(b.agent.calls))
	}
}

// TestUnreachableServiceStops is the third refusal, and the Batch half of
// TestRunnerRetriesThenHaltsWhenIssuerUnreachable: the service answered nothing
// at all, which is asked again with backoff for a bounded time and then halts
// the run (F23, spec §Resilience). The bound is the point of the test -- a run
// that asked once and gave up would stop on a cold start, and one that asked
// forever would never stop -- so the fixture answers nothing and the attempts
// are counted.
//
// Every attempt presents the same ticket and the same nonce (R2), and on Batch
// every attempt carries the attestation: the claim's two own values are drawn
// once and presented unchanged, while what a claim carries of the machine it is
// made from is read per attempt (claimPost).
func TestUnreachableServiceStops(t *testing.T) {
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	onBatch(t, b)
	claimed(t, b)
	answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
		return anthropicfake.Reply{DropBeforeHeaders: true}
	})

	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatalf("a halt is an outcome and not an error: %v", err)
	}
	if rec == nil || rec.Status != runstore.StatusHalted || rec.Halt == nil {
		t.Fatalf("a Batch run whose service answers nothing did not halt: %+v", rec)
	}
	if rec.Halt.Reason != runstore.HaltBudgetUnavailable {
		t.Errorf("the halt is %+v, want %s (D143: a run that cannot reach the credentials service halts like budget_unavailable)", rec.Halt, runstore.HaltBudgetUnavailable)
	}
	for _, want := range []string{"could not be reached", "fugaro run"} {
		if !strings.Contains(rec.Halt.Detail, want) {
			t.Errorf("the halt's detail does not name %q: %q", want, rec.Halt.Detail)
		}
	}
	calls := claimCalls(t, b.fake)
	if len(calls) < 2 {
		t.Fatalf("the service saw %d claim attempts, want the retry: an attempt nothing answered is asked again (F23)", len(calls))
	}
	if len(calls) > runner.SlipClaimAttempts {
		t.Errorf("the run asked %d times, want at most the bound (%d): the retry is bounded (F23)", len(calls), runner.SlipClaimAttempts)
	}
	first := claimOf(t, calls[0])
	if first.Attestation != vmIdentityFixture {
		t.Errorf("the claim carries the attestation %q, want this VM's own identity token in full format", first.Attestation)
	}
	for i, c := range calls {
		req := claimOf(t, c)
		if req.Nonce != first.Nonce {
			t.Errorf("attempt %d presents the nonce %q, want the first attempt's %q: a retry of a claim that may have landed is not a second claim (R2)", i+1, req.Nonce, first.Nonce)
		}
		if req.Ticket != ticketFixture {
			t.Errorf("attempt %d presents the ticket %q, want the one the launcher put on the job", i+1, req.Ticket)
		}
		if req.Attestation != vmIdentityFixture {
			t.Errorf("attempt %d carries the attestation %q, want this VM's identity read again for it", i+1, req.Attestation)
		}
	}
}

// TestTheAttestationIsNeverPrinted: the attestation is this VM's own
// credential, read from the metadata server at the claim. It reaches the
// service -- the test reads it out of the claim's body, which is the one wire it
// belongs on -- and nothing else the run writes: not a reason it publishes, not
// its record, not a log line, and not the error a caller prints (R7, BR4's
// acceptance).
//
// The service refuses the claim here, which is what gives the run a sentence to
// publish and a record to read back, and the attestation was in the body it
// refused: the leak this checks for is one the run could have made.
//
// The read registers the attestation with the run's redactor (batchAttestation,
// addSecret), so a reason is redacted of it rather than dropped: the same
// sentence comes back a sentence. Mutation (run, restored): drop the addSecret
// from batchAttestation -- the redaction below answers the value whole, and
// every line of this test that reads a run's own output fails with it.
func TestTheAttestationIsNeverPrinted(t *testing.T) {
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	onBatch(t, b)
	claimed(t, b)
	body, err := json.Marshal(credsvc.WireError{Error: identitywire.RefusalIdentityRefused, Message: "the ticket was already claimed"})
	if err != nil {
		t.Fatal(err)
	}
	answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
		return anthropicfake.Reply{Status: http.StatusConflict, Header: jsonHeader(), Body: string(body)}
	})
	// The run itself, so the redactor it publishes through can be asked
	// directly: a leak check that only looked for the value in the run's output
	// would pass on a redactor that dropped a whole line instead.
	var handle *runner.RunHandle
	runner.OnRun(t, func(h *runner.RunHandle) { handle = h })

	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatalf("a halt is an outcome and not an error: %v", err)
	}
	calls := claimCalls(t, b.fake)
	if len(calls) != 1 {
		t.Fatalf("the service saw %d claims, want the run's one", len(calls))
	}
	claim := claimOf(t, calls[0])
	if claim.Attestation != vmIdentityFixture {
		t.Fatalf("the claim carries the attestation %q, want the VM identity the metadata server served it: the claim is where this value belongs", claim.Attestation)
	}

	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(vmIdentityFixture)) {
		t.Error("the run's record carries the attestation, which is this VM's own credential")
	}
	if rec.Halt != nil && strings.Contains(rec.Halt.Detail, vmIdentityFixture) {
		t.Errorf("the halt's detail carries the attestation: %q", rec.Halt.Detail)
	}
	if strings.Contains(b.logs.String(), vmIdentityFixture) {
		t.Errorf("a log line carries the attestation:\n%s", b.logs.String())
	}
	if handle == nil {
		t.Fatal("the fixture never saw the run")
	}
	// The run's own redactor, over a line that carries the attestation and the
	// ticket the claim presents beside it: both are gone and the sentence the
	// run publishes survives.
	line := "the claim carried " + vmIdentityFixture + " and the ticket " + ticketFixture + " and was refused"
	printed := handle.Redact(line)
	for _, secret := range []string{vmIdentityFixture, ticketFixture} {
		if strings.Contains(printed, secret) {
			t.Errorf("a reason the run publishes carries %q: %q", secret, printed)
		}
	}
	if !strings.Contains(printed, "[REDACTED]") {
		t.Errorf("the values were dropped whole rather than redacted: %q", printed)
	}
	if !strings.Contains(printed, "and was refused") {
		t.Errorf("the sentence the run published lost its own words: %q", printed)
	}
	// The registration happens before the claim, not at it: the ticket is on
	// the run's own secret list from the first moment of the run, so a reason
	// published before the claim -- an admission halt, a config refusal -- is
	// redacted of it too (addIdentityTicket).
	if got := handle.Secrets(); !slices.Contains(got, ticketFixture) {
		t.Errorf("the run's redactor does not know the launch's ticket: %v", got)
	}
}

// TestTheTicketIsRegisteredBeforeBootstrap is the ticket's half of
// TestModelCredentialsAreRegisteredBeforeBootstrap: the runner registers the
// ticket its job's environment carries at the top of Run (addIdentityTicket),
// before bootstrap reads anything, rather than at the claim that presents it.
// A run whose job carries a ticket is a Batch job of this release, and a run
// that did not know its ticket until the claim would publish every reason it
// wrote before that -- an admission refusal, a config refusal, a git failure --
// with the ticket in it (R7, D3115).
//
// The mutation this fails on is moving the call to the claim (dropping it here
// entirely, which is where the claim's own registration stands): the ticket is
// still redacted from everything the claim's own path writes, and the run reads
// the same either way -- which is exactly why the observation is the run's
// secret list at the moment Run hands it over.
func TestTheTicketIsRegisteredBeforeBootstrap(t *testing.T) {
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	onBatch(t, b)
	answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
		return claimAnswer(t, b.mintToken(repoSlug, runID, time.Now()))
	})
	claimed(t, b)
	var atStart []string
	runner.OnRun(t, func(h *runner.RunHandle) { atStart = h.Secrets() })

	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec == nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("the run = %+v, err = %v", rec, err)
	}
	if !slices.Contains(atStart, ticketFixture) {
		t.Fatalf("the run started with a ticket in its environment that the redactor did not know: %v", atStart)
	}
}

// TestTheBatchClaimPathIsPinned is the mutation-pinning test BR4's review left
// open: the claim path of a Batch run is one sequence -- this VM's attestation
// is read and sent, the launcher's ticket is presented, the service's answer is
// stored as the run's identity, and a refusal or an unreadable VM identity
// stops the run -- and each step is pinned here, so a change that quietly
// breaks one is a failing test and not a run that spends with no identity.
//
// Each subtest names the mutation it fails on, run and restored:
//
//   - the attestation is not sent (batchAttestation returns ""): the claim's
//     body carries no attestation, and the first subtest fails on it;
//   - the ticket is read from the wrong place (the identitywire.TicketEnv
//     lookup in BackendFromEnv renamed): the job carries a ticket the runner
//     does not read, so the run stops at the no-ticket refusal and the first
//     subtest fails on the run it never got;
//   - a refused claim is treated as success (the ans.status != StatusOK arm of
//     claimBudgetIdentity dropped): the second subtest's run goes on with no
//     identity, and fails on the halt and the sentence it never wrote;
//   - the claim's result is not stored on the run (budgetClient's c.custom =
//     custom dropped): the session opens on no token, and the first subtest
//     fails on the bootstrap refusal the run ends with;
//   - the VM identity cannot be read (the errNoAttestation arm of
//     claimCallDetail disabled): the third subtest fails on the sentence it
//     gets -- the unreachable service's, not this VM's -- and that arm, the
//     finding this test was deferred for, is pinned by it.
func TestTheBatchClaimPathIsPinned(t *testing.T) {
	t.Run("the attestation is sent and the run lives on the claim", func(t *testing.T) {
		b := newCascade(t, cascadeConfig(t), &cascadeService{})
		onBatch(t, b)
		claimed(t, b)
		// The ticket the run presents is the one its job's environment
		// carries: the Backend is built the way a job's is (BackendFromEnv),
		// so a runner that looked for it anywhere else -- another variable,
		// the sealed object's marker -- has none to present.
		be, err := runner.BackendFromEnv(envOf(map[string]string{
			identitywire.TicketEnv:   ticketFixture,
			runner.RTDBURLEnv:        "http://127.0.0.1:9000",
			runner.FirebaseAPIKeyEnv: "AIzaSyFakeWebApiKeyForTests000000000",
		}), true)
		if err != nil {
			t.Fatalf("a job environment carrying a ticket did not parse: %v", err)
		}
		b.deps.Backend.Ticket = be.Ticket
		answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
			return claimAnswer(t, b.mintToken(repoSlug, runID, time.Now()))
		})

		rec, err := b.run(t, implement("feature"), review("ship", 0))
		if err != nil || rec == nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("a Batch run that claims its identity must finish on it: rec = %+v, err = %v", rec, err)
		}
		calls := claimCalls(t, b.fake)
		if len(calls) != 1 {
			t.Fatalf("the service was asked to claim %d times, want the run's one", len(calls))
		}
		claim := claimOf(t, calls[0])
		if claim.Attestation != vmIdentityFixture {
			t.Errorf("the claim's attestation is %q, want this VM's identity token in full format: a Batch claim that sends none is not this run's", claim.Attestation)
		}
		if claim.Ticket != ticketFixture {
			t.Errorf("the claim presents the ticket %q, want the one the job's environment carries", claim.Ticket)
		}
	})

	t.Run("a refused claim stops the run", func(t *testing.T) {
		b := newCascade(t, cascadeConfig(t), &cascadeService{})
		onBatch(t, b)
		claimed(t, b)
		body, err := json.Marshal(credsvc.WireError{Error: identitywire.RefusalIdentityRefused, Message: "the ticket was already claimed"})
		if err != nil {
			t.Fatal(err)
		}
		answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
			return anthropicfake.Reply{Status: http.StatusConflict, Header: jsonHeader(), Body: string(body)}
		})

		rec, err := b.run(t, implement("feature"), review("ship", 0))
		if err != nil {
			t.Fatalf("a halt is an outcome and not an error: %v", err)
		}
		if rec == nil || rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetUnavailable {
			t.Fatalf("a refused claim must halt the run, not be read as an identity: %+v", rec)
		}
		if !strings.Contains(rec.Halt.Detail, "already claimed") {
			t.Errorf("the halt's detail does not say the claim was refused: %q", rec.Halt.Detail)
		}
		if len(b.agent.calls) != 0 {
			t.Errorf("%d stages ran on a run whose claim was refused", len(b.agent.calls))
		}
	})

	t.Run("a VM identity that cannot be read stops the run", func(t *testing.T) {
		b := newCascade(t, cascadeConfig(t), &cascadeService{})
		onBatchMeta(t, b, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("format") == "full" {
				http.Error(w, "no identity for you", http.StatusForbidden)
				return
			}
			_, _ = io.WriteString(w, beatIDToken)
		})
		claimed(t, b)
		answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
			return claimAnswer(t, b.mintToken(repoSlug, runID, time.Now()))
		})

		rec, err := b.run(t, implement("feature"), review("ship", 0))
		if err != nil {
			t.Fatalf("a halt is an outcome and not an error: %v", err)
		}
		if rec == nil || rec.Status != runstore.StatusHalted || rec.Halt == nil {
			t.Fatalf("a run whose VM identity cannot be read must halt: %+v", rec)
		}
		for _, want := range []string{"identity of the VM this run is on could not be read", "fugaro run"} {
			if !strings.Contains(rec.Halt.Detail, want) {
				t.Errorf("the halt's detail does not name %q: %q", want, rec.Halt.Detail)
			}
		}
		if strings.Contains(rec.Halt.Detail, "could not be reached") {
			t.Errorf("a VM identity that could not be read got the unreachable service's sentence: %q", rec.Halt.Detail)
		}
		if got := len(claimCalls(t, b.fake)); got != 0 {
			t.Errorf("the service saw %d claim calls, want none: the claim is never sent without this VM's identity", got)
		}
	})
}

// claimOf reads one claim call's body as the route's own request. A body that
// is not the request is the fixture's own damage rather than the run's, so it
// fails the test it is read in.
func claimOf(t *testing.T, c claimCall) identitywire.ClaimRequest {
	t.Helper()
	var req identitywire.ClaimRequest
	if err := json.Unmarshal(c.body, &req); err != nil {
		t.Fatalf("a claim's body is not the route's request: %v", err)
	}
	return req
}
