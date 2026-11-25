package credsvc

// BR17's tests: the registration (batchclaim_register.go), the launcher's
// second call, and the three fences the one write on a run's record is made
// under (R19, D3108).
//
// What each test is for, in one line: the launcher that asked for a run's
// ticket registers the job it created, and the record carries the id and
// nothing else new; a second identity cannot register another's job; the id is
// written once and a second registration is refused; a job the record does not
// name is refused; a retry is a new run id with a record of its own; only the
// launcher class is admitted, and a roster service account is one (D3123, R20);
// the route is mounted at the door and answers the contract's "not served"
// where the installation is not wired; and every refusal of a verified caller
// writes exactly one audit line.
//
// The handler is called the way the mount (I1) will call it, with the caller
// the mount has already verified, and the store under it is the real one over
// internal/gcpfake's Realtime Database (the fixture's, identity_ticket_test.go):
// what is under test is the write and the fences around it, and a store that
// wrote unconditionally would make every fence here pass on a service that
// hands two launchers one record.
//
// Everything is hermetic (Convention 9), and no value here stands for a secret:
// the job's name and the id the platform gave it are the platform's own values
// (R1, R19), and the ticket a case needs is one this service drew.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

const (
	// testJobName is a Batch job's full resource name, in the one shape the
	// platform gives one, and testJobID the id a create of it returned: opaque
	// to this service and compared only with what the link answers (C1's
	// JobRequest.JobID).
	testJobName = identitywire.JobName("projects/aurora-dev/locations/europe-west1/jobs/fb-20261020-090000-ab12")
	testJobID   = "job-8271"
	// testOtherJobName and testRetryJobName are other jobs of the same
	// installation: the first is a job this run's ask did not name, the second
	// the job of a retry launched under a new run id.
	testOtherJobName = identitywire.JobName("projects/aurora-dev/locations/europe-west1/jobs/fb-20261020-090000-ffff")
	testRetryJobName = identitywire.JobName("projects/aurora-dev/locations/europe-west1/jobs/fb-20261020-090001-cd34")
	// testRetryRun is the run id of the retry (R6: a retry takes a new run id),
	// and testRetryJobID the id of the job that retry created.
	testRetryRun   = "20261020-090001-cd34"
	testRetryJobID = "job-9034"
)

// register calls the registration handler with one body, the way the mount (I1)
// will call it, and rawRegister with a body the test wrote itself (a body that
// is not this contract, which is the only way to send one).
func (f *tickets) register(t *testing.T, caller IdentityCaller, req identitywire.JobRequest) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("building the registration: %v", err)
	}
	return f.rawRegister(t, caller, string(body))
}

func (f *tickets) rawRegister(t *testing.T, caller IdentityCaller, body string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, identitywire.JobPath, strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.server.identityJobRegister(context.Background(), rec, caller, r)
	return rec, rec.Body.Bytes()
}

// registered reads one answer as the registration's, which is the empty object
// on purpose: a launcher told its record was written has what it needs.
func (f *tickets) registered(t *testing.T, rec *httptest.ResponseRecorder, body []byte) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("the registration is answered %d, want 200: %s", rec.Code, body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("the answer is cacheable with %q, want no-store", got)
	}
	var ans identitywire.JobResponse
	if err := identitywire.DecodeStrict(body, &ans); err != nil {
		t.Fatalf("the answer is not the empty object this contract answers with: %v (%s)", err, body)
	}
}

// jobID reads one run's record and answers the id it carries, which is the
// whole of what the registration writes.
func (f *tickets) jobID(t *testing.T, slug, run string) string {
	t.Helper()
	return f.record(t, slug, run).JobID
}

// refusedAnswer checks one refusal's answer — the status and the contract's
// code — and auditsCount how many lines the fixture holds. The two are what a
// test that makes MORE than one refusal counts instead of f.refused's "exactly
// one": a refusal owes the line it ADDED, and the fixture's first line is not
// the call's.
func (f *tickets) refusedAnswer(t *testing.T, rec *httptest.ResponseRecorder, body []byte, wantStatus int, wantCode string) WireError {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("the registration is answered %d, want %d: %s", rec.Code, wantStatus, body)
	}
	e := refusal(t, rec.Code, body)
	if e.Error != wantCode {
		t.Errorf("the refusal is %q, want the contract's %q: %s", e.Error, wantCode, e.Message)
	}
	if got := e.Message; !strings.HasPrefix(got, requestRefused) && wantStatus == StatusRequestRefused {
		t.Errorf("the refusal reads %q, want the service's own prefix %q first", got, requestRefused)
	}
	return e
}

// auditCount is the number of audit lines the fixture holds, every one of them
// checked for the shape a line has. It reads the buffer directly rather than
// through auditLines alone, which refuses an empty one: the count is what a
// test asks BEFORE a refusal, and zero is the answer it wants there.
func (f *tickets) auditCount(t *testing.T) int {
	t.Helper()
	if f.audit.String() == "" {
		return 0
	}
	lines := auditLines(t, f.audit)
	for _, line := range lines {
		checkAuditShape(t, line)
	}
	return len(lines)
}

// batchAsk is the fixture's ask naming a job, and issue asks for its ticket and
// checks that the record was written with the name (R19's first write, LM1's).
func (f *tickets) batchAsk(t *testing.T, caller IdentityCaller, job identitywire.JobName) identitywire.TicketRequest {
	t.Helper()
	ask := f.goodAsk()
	ask.Job = job
	rec, body := f.ask(t, caller, ask)
	f.issued(t, rec, body)
	return ask
}

// TestTheLauncherThatAskedForTheTicketRegistersTheJob is BR17's headline: a
// verified launcher's second call writes the platform's id for the job its
// ticket ask named on that run's record, and the record the claim will be
// served from is otherwise exactly the one the ask wrote (R19).
func TestTheLauncherThatAskedForTheTicketRegistersTheJob(t *testing.T) {
	f := newTickets(t)
	caller := f.person(testLauncherAccount)
	ask := f.batchAsk(t, caller, testJobName)

	asked := f.record(t, ask.Slug, ask.Run)
	if asked.Job != testJobName {
		t.Fatalf("the record names the job %q, want the ask's own %q: R19 writes the job with the ticket", asked.Job, testJobName)
	}
	if asked.JobID != "" {
		t.Fatalf("the record carries the id %q before the registration: the id is the registration's write and nothing else's (R19)", asked.JobID)
	}

	rec, body := f.register(t, caller, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName, JobID: testJobID})
	f.registered(t, rec, body)

	got := f.record(t, ask.Slug, ask.Run)
	if got.JobID != testJobID {
		t.Errorf("the record carries the id %q, want the one the registration wrote (%q)", got.JobID, testJobID)
	}
	// The id and nothing else: the caller, the class, the claims, the account
	// the claim must come from, the ticket's digest and the record's own stamp
	// are the ask's, so a registration cannot rewrite the record the claim is
	// served from.
	if got.Job != asked.Job || got.Caller != asked.Caller || got.Class != asked.Class ||
		got.ExpectedAccount != asked.ExpectedAccount || got.TicketHash != asked.TicketHash ||
		got.Attempt != asked.Attempt || !got.At.Equal(asked.At) {
		t.Errorf("the registration rewrote the record:\ngot  %+v\nwant the ask's own with the id on it (%+v)", got, asked)
	}
	if !reflect.DeepEqual(got.Claims, asked.Claims) {
		t.Errorf("the registration rewrote the claims: %+v, want the ask's own %+v", got.Claims, asked.Claims)
	}
	if got.Claimed.Account != "" {
		t.Errorf("the registration marked the record claimed by %q: the claim is the run's own call (R2)", got.Claimed.Account)
	}

	// The registration issues no credential, so it is recorded on the service's
	// own line and not as an audit line — the ticket route's rule read the other
	// way — and that line is where an operator reads which job was registered
	// for which run.
	f.noLines(t)
	line := f.logs.String()
	for _, want := range []string{ask.Run, ask.Slug, string(testJobName), testJobID, testLauncherAccount} {
		if !strings.Contains(line, want) {
			t.Errorf("the service's line does not name %q: %s", want, line)
		}
	}
}

// TestAnotherIdentityCannotRegisterTheJob: the route's classes are one half of
// the fence and the record's caller is the other (R19, D3108). A second address
// on the installation's own launcher roster — one that may ask for tickets of
// its own — cannot write the id on another identity's record, and its refusal
// leaves the record exactly as the ask wrote it.
func TestAnotherIdentityCannotRegisterTheJob(t *testing.T) {
	f := newTickets(t)
	owner := f.person(testLauncherAccount)
	ask := f.batchAsk(t, owner, testJobName)

	other := f.person(testOtherLauncher)
	rec, body := f.register(t, other, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName, JobID: testJobID})
	f.refused(t, rec, body, testOtherLauncher, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
	if got := f.jobID(t, ask.Slug, ask.Run); got != "" {
		t.Errorf("another identity's registration wrote the id %q: only the identity that asked for the ticket may write it (R19, D3108)", got)
	}

	// The record is still the owner's to register, which is what makes the
	// refusal a fence around the caller and not a damaged record.
	rec, body = f.register(t, owner, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName, JobID: testJobID})
	f.registered(t, rec, body)
	if got := f.jobID(t, ask.Slug, ask.Run); got != testJobID {
		t.Errorf("the owner's own registration wrote %q, want %q", got, testJobID)
	}
}

// TestTheJobIdIsWrittenOnce: the id is the tie the VM's attestation is checked
// against, so it is written once and never overwritten (R19, D3108) — a job
// deleted and created again under the same name is another job with another id,
// and a record that could be re-registered would be a record a stale VM could
// claim.
//
// The same id a second time is refused too, and that is deliberate: a launcher's
// retry is a NEW run id (TestARetryIsANewRunId, R6) and not a second write on
// the record it already wrote, so a registration that landed twice is a launcher
// that has lost track of its own run.
//
// Both registrations are made by the same caller in sequence. The fence is not
// a check this route makes from a value it read earlier: it is made inside the
// store's one read-then-write on the node (RegisterJob, identity_record.go's
// update), which is what makes it hold against a second registration racing
// this one — and the store's own atomicity is LM1's test.
func TestTheJobIdIsWrittenOnce(t *testing.T) {
	f := newTickets(t)
	caller := f.person(testLauncherAccount)
	ask := f.batchAsk(t, caller, testJobName)
	rec, body := f.register(t, caller, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName, JobID: testJobID})
	f.registered(t, rec, body)

	for _, tc := range []struct {
		name string
		id   string
	}{
		{"another id for the same job", "job-9999"},
		{"the id it already carries", testJobID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := f.auditCount(t)
			rec, body := f.register(t, caller, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName, JobID: tc.id})
			f.refusedAnswer(t, rec, body, StatusRequestRefused, identitywire.RefusalIdentityRefused)
			if got := f.auditCount(t); got != lines+1 {
				t.Errorf("the second registration wrote %d audit lines, want exactly one: a refusal is one line, and a call that writes none is a refusal nobody can see", got-lines)
			}
			if got := f.jobID(t, ask.Slug, ask.Run); got != testJobID {
				t.Errorf("the second registration left the id %q, want the first one (%q): the id is written once", got, testJobID)
			}
		})
	}
}

// TestAnIdThatIsNotTheJobsIsRefused: the registration names the job the ticket
// ask named, and a job that is not it is refused rather than written (R19,
// D3108) — an id on another job would be an id on a record whose VM it does not
// belong to, and the claim is held to the record's job (D3114).
//
// A name the platform could not have given is refused by its shape, which is
// what tells a launcher that sent a truncated or re-spelled name what a job's
// name is, rather than answering it as a job that is not this run's.
func TestAnIdThatIsNotTheJobsIsRefused(t *testing.T) {
	f := newTickets(t)
	caller := f.person(testLauncherAccount)
	ask := f.batchAsk(t, caller, testJobName)

	rec, body := f.register(t, caller, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testOtherJobName, JobID: testJobID})
	e := f.refusedAnswer(t, rec, body, StatusRequestRefused, identitywire.RefusalIdentityRefused)
	if !strings.Contains(e.Message, "not the job this run's ticket ask named") {
		t.Errorf("the refusal does not say the job is not this run's: %s", e.Message)
	}

	rec, body = f.register(t, caller, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: "not-a-job", JobID: testJobID})
	e = f.refusedAnswer(t, rec, body, StatusRequestRefused, identitywire.RefusalIdentityRefused)
	if !strings.Contains(e.Message, "full resource name") {
		t.Errorf("the refusal of a name the platform could not have given does not say what a job's name is: %s", e.Message)
	}

	// A registration that carries no id is refused before the store is asked:
	// the id is the whole of what this call writes.
	rec, body = f.register(t, caller, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName})
	f.refusedAnswer(t, rec, body, StatusRequestRefused, identitywire.RefusalIdentityRefused)

	// Three refusals of a verified caller, three lines: no more, no fewer.
	if got := f.auditCount(t); got != 3 {
		t.Errorf("the three refusals wrote %d audit lines, want exactly three", got)
	}
	if got := f.record(t, ask.Slug, ask.Run); got.JobID != "" || got.Job != testJobName {
		t.Errorf("a refused registration changed the record: id %q, job %q", got.JobID, got.Job)
	}
}

// TestARetryIsANewRunId: a launch that is retried is a new run with a record of
// its own (R6, D3108), and the two records do not touch: the first keeps its job
// and its id, the second carries its own, and the first cannot be re-registered
// with the retry's job — which is what makes "a retry is a new run id" a fact
// of the store and not advice.
func TestARetryIsANewRunId(t *testing.T) {
	f := newTickets(t)
	caller := f.person(testLauncherAccount)
	first := f.batchAsk(t, caller, testJobName)
	rec, body := f.register(t, caller, identitywire.JobRequest{Slug: first.Slug, Run: first.Run, Job: testJobName, JobID: testJobID})
	f.registered(t, rec, body)

	retry := f.goodAsk()
	retry.Run = testRetryRun
	retry.Job = testRetryJobName
	rec, body = f.ask(t, caller, retry)
	f.issued(t, rec, body)
	rec, body = f.register(t, caller, identitywire.JobRequest{Slug: retry.Slug, Run: retry.Run, Job: testRetryJobName, JobID: testRetryJobID})
	f.registered(t, rec, body)

	if got := f.jobID(t, first.Slug, first.Run); got != testJobID {
		t.Errorf("the first run's record carries the id %q, want its own (%q)", got, testJobID)
	}
	if got := f.jobID(t, retry.Slug, retry.Run); got != testRetryJobID {
		t.Errorf("the retry's record carries the id %q, want its own (%q)", got, testRetryJobID)
	}
	if got := f.record(t, first.Slug, first.Run).Job; got != testJobName {
		t.Errorf("the first run's record names the job %q, want its own (%q): the retry's ask is the retry's record and no other", got, testJobName)
	}

	// The retry's id is not a second write on the first run's record, whichever
	// fence answers first: that record names another job and carries an id.
	rec, body = f.register(t, caller, identitywire.JobRequest{Slug: first.Slug, Run: first.Run, Job: testRetryJobName, JobID: testRetryJobID})
	f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
	if got := f.jobID(t, first.Slug, first.Run); got != testJobID {
		t.Errorf("the retry's id landed on the first run's record: %q", got)
	}
}

// TestOnlyTheLauncherClassRegisters: the registration is the ticket ask's second
// half, so the classes that may be issued a run are the classes that may name
// the job it launched (D3123, R20) — and a roster service account IS a launcher,
// which the class table says and the route has to honour.
//
// A run's own job account is the class that must not: it is the class the claim
// is served to, and a job that could register its own record could name the job
// its identity is bound to.
func TestOnlyTheLauncherClassRegisters(t *testing.T) {
	// The table's own answer first, read off the contract, so a change to either
	// side of this fails here.
	route, found := identitywire.RouteFor(identitywire.JobPath)
	if !found {
		t.Fatalf("RouteFor(%q) answers no route", identitywire.JobPath)
	}
	for _, class := range []identitywire.CallerClass{identitywire.ClassPerson, identitywire.ClassServiceAccount, identitywire.ClassOrchestrator} {
		if !route.AdmitsClass(class) {
			t.Errorf("the registration does not admit the class %q, and it is the ticket ask's own table (D3123, R20)", class)
		}
	}
	if route.AdmitsClass(identitywire.ClassJobAccount) {
		t.Error("the registration admits a run's own job account: the claim is that class's call, and it may not name the job its identity is bound to")
	}

	// A job account at the registration, on its own run's record and with the
	// job the ask named.
	f := newTickets(t)
	ask := f.batchAsk(t, f.person(testLauncherAccount), testJobName)
	job := IdentityCaller{Address: testJobSA, Class: identitywire.ClassJobAccount}
	rec, body := f.register(t, job, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName, JobID: testJobID})
	f.refused(t, rec, body, testJobSA, StatusUnverified, identitywire.RefusalCallerNotAdmitted, ReasonNotAdmitted)
	if got := f.jobID(t, ask.Slug, ask.Run); got != "" {
		t.Errorf("a run's own job account registered the id %q", got)
	}

	// A roster service account: it asks for the run's ticket and registers the
	// job it created, and the record's caller is its own address (D3123, R20).
	// The address is identity_callers_test.go's own roster service account, so
	// the two halves of D3123 — the class a caller is verified as and the route
	// that admits it — are held to one name.
	sa := IdentityCaller{Address: testRosterSA, Class: identitywire.ClassServiceAccount}
	g := newTickets(t, func(s *svc, cfg *Config) {
		cfg.Identity.ServiceAccounts = []string{testRosterSA}
	})
	saAsk := g.batchAsk(t, sa, testJobName)
	rec, body = g.register(t, sa, identitywire.JobRequest{Slug: saAsk.Slug, Run: saAsk.Run, Job: testJobName, JobID: testJobID})
	g.registered(t, rec, body)
	if got := g.jobID(t, saAsk.Slug, saAsk.Run); got != testJobID {
		t.Errorf("a roster service account's registration wrote %q, want %q", got, testJobID)
	}
	if got := g.record(t, saAsk.Slug, saAsk.Run).Caller; got != testRosterSA {
		t.Errorf("the record names the caller %q, want the roster service account %q", got, testRosterSA)
	}
}

// TestTheRegistrationRouteAnswersNotServedUntilItIsWired: the route is mounted
// at the service's door and answers the contract's "not served" where the
// identity configuration is not wired (C1, R7, I1).
//
// It is asked three ways, because "mounted" and "served" are two facts and the
// release needs both: the DOOR answers the contract's code rather than the
// unknown-path 404, and the 404's own sentence names the path (so an operator
// reading what this service is reads the registration); and the handler's own
// floor answers the same code for a service with no issuer and for one whose
// store cannot register a job.
func TestTheRegistrationRouteAnswersNotServedUntilItIsWired(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	resp, body := s.postRaw(t, http.MethodPost, identitywire.JobPath)
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("POST %s is answered the unknown-path refusal: the path is a route of this service (BR17)\n%s", identitywire.JobPath, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != identitywire.RefusalNotServed {
		t.Errorf("the door answers %q, want the contract's %q: an installation with no issuer answers 'not served' (R7, I1)", e.Error, identitywire.RefusalNotServed)
	}
	_, notFound := s.postRaw(t, http.MethodPost, "/v1/not-a-route")
	if e := refusal(t, http.StatusNotFound, notFound); !strings.Contains(e.Message, identitywire.JobPath) {
		t.Errorf("the 404's sentence does not name %s, and it is what an operator reads to find out what this service is: %s", identitywire.JobPath, e.Message)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*svc, *Config)
	}{
		{"a service with no issuer", func(s *svc, cfg *Config) { cfg.Identity.Signer = "" }},
		{"a store that cannot register a job", func(s *svc, cfg *Config) { cfg.Identity.Store = StubIdentityStore() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTickets(t, tc.mutate)
			rec, body := f.register(t, f.person(testLauncherAccount), identitywire.JobRequest{
				Slug: f.slug, Run: f.run, Job: testJobName, JobID: testJobID,
			})
			f.refused(t, rec, body, testLauncherAccount, StatusUnavailable, identitywire.RefusalNotServed, ErrorStateUnavailable)
		})
	}
}

// TestEachRegistrationRefusalWritesOneAuditLine: every refusal of a verified
// caller writes exactly one audit line, and it carries the verified caller and
// nothing of the call (R5, audit.go). It is one test over every branch rather
// than a rule each branch remembers, because a branch added without the line is
// a refusal nobody can see.
//
// The wiring itself is two of the cases — a service with no issuer and one
// whose store cannot register — because a caller HAS been verified by the time
// either is reached, and the line is owed for them too (the door's own answer,
// made before any caller is verified, writes none: identity_stub.go's rule).
func TestEachRegistrationRefusalWritesOneAuditLine(t *testing.T) {
	const missing = `"a_field_no_type_here_has":1`

	type refusalCase struct {
		name  string
		build func(t *testing.T) (f *tickets, caller IdentityCaller, body string)
		// status and code are the caller's answer, audit the status the line
		// carries (identity_caller.go's mapping, which is not one vocabulary).
		status int
		code   string
		audit  string
	}
	// wired is the fixture a case is about: the service configured the way a
	// deployment wires it, with the record a case needs already written.
	wired := func(t *testing.T, job identitywire.JobName) (*tickets, IdentityCaller, identitywire.TicketRequest) {
		t.Helper()
		f := newTickets(t)
		caller := f.person(testLauncherAccount)
		ask := f.batchAsk(t, caller, job)
		return f, caller, ask
	}
	body := func(t *testing.T, req identitywire.JobRequest) string {
		t.Helper()
		b, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("building the registration: %v", err)
		}
		return string(b)
	}

	cases := []refusalCase{
		{
			name: "the service holds no issuer",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f := newTickets(t, func(s *svc, cfg *Config) { cfg.Identity.Signer = "" })
				caller := f.person(testLauncherAccount)
				return f, caller, body(t, identitywire.JobRequest{Slug: f.slug, Run: f.run, Job: testJobName, JobID: testJobID})
			},
			status: StatusUnavailable, code: identitywire.RefusalNotServed, audit: ErrorStateUnavailable,
		},
		{
			name: "the service holds no store that can register a job",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f := newTickets(t, func(s *svc, cfg *Config) { cfg.Identity.Store = StubIdentityStore() })
				caller := f.person(testLauncherAccount)
				return f, caller, body(t, identitywire.JobRequest{Slug: f.slug, Run: f.run, Job: testJobName, JobID: testJobID})
			},
			status: StatusUnavailable, code: identitywire.RefusalNotServed, audit: ErrorStateUnavailable,
		},
		{
			name: "the caller is a run's own job account",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f, _, ask := wired(t, testJobName)
				caller := IdentityCaller{Address: testJobSA, Class: identitywire.ClassJobAccount}
				return f, caller, body(t, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName, JobID: testJobID})
			},
			status: StatusUnverified, code: identitywire.RefusalCallerNotAdmitted, audit: ReasonNotAdmitted,
		},
		{
			name: "the body is not one JSON object of this route's fields",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f := newTickets(t)
				caller := f.person(testLauncherAccount)
				return f, caller, `{"slug":"` + f.slug + `"` + missing + `}`
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
		{
			name: "the body is not JSON at all",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f := newTickets(t)
				return f, f.person(testLauncherAccount), "not json"
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
		{
			name: "the run is not spelled as the record's own two fields",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f := newTickets(t)
				caller := f.person(testLauncherAccount)
				return f, caller, body(t, identitywire.JobRequest{Slug: f.slug, Run: f.run + "/x", Job: testJobName, JobID: testJobID})
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
		{
			name: "the job is not a name the platform could have given",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f, caller, ask := wired(t, testJobName)
				return f, caller, body(t, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: "not-a-job", JobID: testJobID})
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
		{
			name: "the registration carries no id",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f, caller, ask := wired(t, testJobName)
				return f, caller, body(t, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName})
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
		{
			name: "the run has no record",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f := newTickets(t)
				caller := f.person(testLauncherAccount)
				return f, caller, body(t, identitywire.JobRequest{Slug: f.slug, Run: f.run, Job: testJobName, JobID: testJobID})
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
		{
			name: "the record is another caller's",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f, _, ask := wired(t, testJobName)
				caller := f.person(testOtherLauncher)
				return f, caller, body(t, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName, JobID: testJobID})
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
		{
			name: "the job is not the one the ask named",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f, caller, ask := wired(t, testJobName)
				return f, caller, body(t, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testOtherJobName, JobID: testJobID})
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
		{
			name: "the run's job is already registered",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f, caller, ask := wired(t, testJobName)
				rec, ans := f.register(t, caller, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName, JobID: testJobID})
				f.registered(t, rec, ans)
				return f, caller, body(t, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName, JobID: "job-9999"})
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
		{
			name: "the run's ticket was already claimed",
			build: func(t *testing.T) (*tickets, IdentityCaller, string) {
				f, caller, ask := wired(t, testJobName)
				if _, err := f.store.Claim(context.Background(), ask.Slug, ask.Run, identitywire.Claim{
					Account: testJobSA, At: f.now, NonceHash: identitywire.Digest("the-nonce-the-run-drew"),
				}); err != nil {
					t.Fatalf("claiming the record: %v", err)
				}
				return f, caller, body(t, identitywire.JobRequest{Slug: ask.Slug, Run: ask.Run, Job: testJobName, JobID: testJobID})
			},
			status: StatusRequestRefused, code: identitywire.RefusalIdentityRefused, audit: ErrorRequestRefused,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, caller, raw := tc.build(t)
			rec, ans := f.rawRegister(t, caller, raw)
			f.refused(t, rec, ans, caller.Address, tc.status, tc.code, tc.audit)
		})
	}
}
