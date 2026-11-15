package credsvc

// The workflow-secrets route (B3.2, G18, G19) as a caller sees it.
//
// The route's own file (workflowsecrets.go) states what it serves and why. What
// these tests hold is the two constraints the plan names it for: G18, that no
// credential VALUE reaches a record, a line, an error or an answer but the one
// answer that serves it, and G19, that a workflow secret is served only to the
// per-repository job account of that repository AND THAT WORKFLOW — the
// verified account and never a header — and never to a review, shared, launcher
// or person caller.
//
// The three reads the route makes are stood in for and nowhere else: the record
// (fakeWorkflowRecords), the containers (the fixture's dynReader, which is what
// the deployment's Secret Manager client is behind) and the job accounts (the
// fixture's own rows, one per (repository, workflow)). Nothing in this file is
// a credential: the two values are built at run time by fakeCred (Convention
// 2), and every container name is a NAME the record holds.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/creds"
)

// The two workflows of the fixture's own repository, and the two variables
// their jobs declare. Two workflows of ONE repository is what makes G19's "and
// that workflow" a test rather than a claim: the record holds both, one job is
// the fixture's own and the other is its sibling's, and each is served its own.
const (
	testWorkflow      = "implement"
	testOtherWorkflow = "review-fix"
)

// The variables the two workflows declare, and the containers their values live
// in. A container is a Secret Manager secret ID — a NAME, never a value — which
// is why these are spelled out here and no fixture holds anything
// secret-shaped.
const (
	testWorkflowEnv            = "DEPLOY_TOKEN"
	testOtherWorkflowEnv       = "REVIEW_TOKEN"
	testWorkflowContainer      = "aurora-implement-deploy"
	testOtherWorkflowContainer = "aurora-review-fix-token"
	testOtherRepoContainer     = "borealis-implement-deploy"
)

// fakeWorkflowRecords is the record reader the fixture serves: a map of slug to
// record, so a test can leave a repository unrecorded or make the read fail
// with no database at all.
type fakeWorkflowRecords struct {
	recs map[string]creds.WorkflowSecrets
	err  error
	// reads is every slug this reader was asked for, in order, which is what
	// makes "a refused caller reads no record" a fact and not an inference.
	reads []string
}

func (f *fakeWorkflowRecords) Read(_ context.Context, slug string) (creds.WorkflowSecrets, bool, error) {
	f.reads = append(f.reads, slug)
	if f.err != nil {
		return creds.WorkflowSecrets{}, false, f.err
	}
	rec, ok := f.recs[slug]
	return rec, ok, nil
}

// workflowRecord is the record `fugaro update` writes for one repository (B3.1,
// creds.PutWorkflowSecrets): both workflows, each with its one secret, and no
// value anywhere (G18).
func workflowRecord() creds.WorkflowSecrets {
	return creds.WorkflowSecrets{
		Version: creds.WorkflowSecretsVersion,
		Workflows: map[string][]creds.WorkflowSecret{
			testWorkflow:      {{Env: testWorkflowEnv, ID: testWorkflowContainer}},
			testOtherWorkflow: {{Env: testOtherWorkflowEnv, ID: testOtherWorkflowContainer}},
		},
	}
}

// workflowReads is the three things this route reads, stood in for: the record,
// the container reader and the two values. It is separate from workflowFixture
// because two of the class tests wire it into a fixture of ANOTHER class — a
// shared caller's and a review caller's, whose auth and pairing are their own —
// and the route's own reads are the same object there.
type workflowReads struct {
	records *fakeWorkflowRecords
	dyn     *dynReader
	// value and otherValue are what the containers hold. They are built at run
	// time (fakeCred) so this file holds no secret-shaped literal, and they
	// differ so that "served its own workflow's value" is one comparison and
	// not two.
	value      string
	otherValue string
}

// newWorkflowReads is the record, the containers and the values for one test:
// the fixture's repository and a second one are both recorded, and the second
// repository's `implement` workflow names a container of its own — so "another
// repository's job is served its own record" is a distinguishable answer.
func newWorkflowReads(t *testing.T) *workflowReads {
	t.Helper()
	r := &workflowReads{
		records:    &fakeWorkflowRecords{},
		dyn:        &dynReader{values: map[string]string{}},
		value:      fakeCred(t, "workflow-secret"),
		otherValue: fakeCred(t, "other-workflow-secret"),
	}
	r.dyn.values[testWorkflowContainer] = r.value
	r.dyn.values[testOtherWorkflowContainer] = r.otherValue
	r.dyn.values[testOtherRepoContainer] = r.otherValue
	other := workflowRecord()
	other.Workflows[testWorkflow] = []creds.WorkflowSecret{{Env: testWorkflowEnv, ID: testOtherRepoContainer}}
	r.records.recs = map[string]creds.WorkflowSecrets{
		testSlug(t, testRepoPath):      workflowRecord(),
		testSlug(t, testOtherRepoPath): other,
	}
	return r
}

// wire is the configuration the route reads, for a fixture this file did not
// build: the shared and review fixtures' own auth and pairing are theirs, and
// what they are missing is exactly this.
func (r *workflowReads) wire(_ *svc, cfg *Config) {
	cfg.WorkflowSecrets = r.records
	cfg.Bundles = &BundleLoader{Source: r.dyn}
}

// workflowFixture is the installation under test for this route: the fixture's
// own service with the route's three reads stood in for, and three job accounts
// — the fixture's own job, its sibling workflow's job of the same repository,
// and a job of a second repository.
type workflowFixture struct {
	*svc
	*workflowReads
}

func newWorkflowSvc(t *testing.T, extra ...func(*svc, *Config)) *workflowFixture {
	t.Helper()
	f := &workflowFixture{workflowReads: newWorkflowReads(t)}
	f.svc = newSvcWith(t, func(s *svc, cfg *Config) {
		f.workflowReads.wire(s, cfg)
		// One account per (repository, workflow), which is what a deployment
		// creates (gcp.ServiceAccountID over the slug and the workflow) and
		// what JobAccountsFor builds from RepoJob rows. The fixture's own job
		// is the `implement` workflow of testRepo, its sibling is the
		// `review-fix` workflow of the SAME repository, and the third is the
		// `implement` workflow of the other repository.
		cfg.JobAccounts = []JobAccount{
			{Email: testJobSA, Slug: s.slug, Repo: testRepo, Workflow: testWorkflow},
			{Email: testSiblingSA, Slug: s.slug, Repo: testRepo, Workflow: testOtherWorkflow},
			{Email: testOtherSA, Slug: testSlug(t, testOtherRepoPath), Repo: testOtherRepo, Workflow: testWorkflow},
		}
		// The verifier's own list is rebuilt with all three, because the class
		// is decided from the verified email: without this a sibling's or
		// another repository's call would be refused as an unknown caller and
		// would never reach the pairing or the route. The launcher class is
		// configured too, so that "a launcher is refused HERE" is the route's
		// own answer and not the verifier's.
		auth, err := NewAuthenticator(AuthConfig{
			Project:           testProject,
			FirebaseProject:   testFirebase,
			Audience:          testAudience,
			JobAccounts:       []string{testJobSA, testSiblingSA, testOtherSA},
			Keys:              s.keys,
			JobKeys:           s.jobKeys,
			Outcomes:          RTDBOutcomes{DB: s.db},
			SharedJobAccounts: []SharedJobAccount{sharedRow()},
			LauncherAccounts:  []string{testLauncherAccount},
			Now:               func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's own verifier configuration is not one NewAuthenticator accepts: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		for _, m := range extra {
			m(s, cfg)
		}
	})
	return f
}

// workflowBody is one workflow-secrets call's body: the variable the caller asks
// for, and nothing else — the repository, the workflow and the run are the
// service's own answers and no field of the call.
func workflowBody(env string) string { return `{"env":"` + env + `"}` }

// fetchWorkflowSecrets sends one workflow-secrets call as the given identities
// and reads the answer whole. It is a function rather than a method so a
// fixture of another class — whose auth and pairing are its own — can make the
// call too.
func fetchWorkflowSecrets(t *testing.T, s *svc, env, runTok, jobTok, job string) (*http.Response, []byte) {
	t.Helper()
	resp := s.postTokens(t, WorkflowSecretsPath, workflowBody(env), runTok, jobTok, job)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, b
}

// fetch is one workflow-secrets call as the fixture's own job.
func (f *workflowFixture) fetch(t *testing.T, env string) (*http.Response, []byte) {
	t.Helper()
	return fetchWorkflowSecrets(t, f.svc, env, f.runToken(), f.jobToken(), f.job)
}

// fetchAs is fetch with the job token given rather than minted, for the calls
// that must present one the service refuses.
func (f *workflowFixture) fetchAs(t *testing.T, env, jobTok string) (*http.Response, []byte) {
	t.Helper()
	return fetchWorkflowSecrets(t, f.svc, env, f.runToken(), jobTok, f.job)
}

// call is one workflow-secrets call whose body is given rather than built, for
// the bodies this contract refuses.
func (f *workflowFixture) call(t *testing.T, body string) (*http.Response, []byte) {
	t.Helper()
	return f.callPath(t, WorkflowSecretsPath, body)
}

// otherJobToken is the Google ID token of the OTHER repository's job account.
func (f *workflowFixture) otherJobToken() string {
	return f.jobToken(func(c map[string]any) { c["email"] = testOtherSA })
}

// siblingJobToken is the Google ID token of the other workflow's job of the
// SAME repository.
func (f *workflowFixture) siblingJobToken() string {
	return f.jobToken(func(c map[string]any) { c["email"] = testSiblingSA })
}

// runTokenOf is the run identity of a run of another repository: the slug
// claims of a run of it, which is what that repository's job account is bound
// to.
func (f *workflowFixture) runTokenOf(slug string) string {
	return f.runToken(
		func(c map[string]any) { c["fs"] = slug },
		func(c map[string]any) { c["sub"] = "r~" + slug + "~" + f.run },
		func(c map[string]any) { c["user_id"] = "r~" + slug + "~" + f.run },
	)
}

// workflowAnswer is one served workflow secret as this contract's own reader
// reads it.
func workflowAnswer(t *testing.T, body []byte) WorkflowSecretsResponse {
	t.Helper()
	var ans WorkflowSecretsResponse
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("the answer is not one this contract can read: %v (%d bytes)", err, len(body))
	}
	return ans
}

// auditCount is the number of audit lines a buffer holds, and it tolerates ZERO
// — which auditLines does not, because it fails on an empty buffer. A refusal
// answered before the identity was verified is a refusal with no line at all
// (C1.13: the line states the identity the service verified), and that is a
// property a test has to be able to assert.
func auditCount(t *testing.T, buf *logBuf) int {
	t.Helper()
	if buf.Len() == 0 {
		return 0
	}
	return len(auditLines(t, buf))
}

// assertRefused is the shape every refusal of this route has: a typed refusal,
// no VALUE read, and the number of audit lines the point of the call allows.
//
// It takes the service and the reads rather than one fixture, because two of the
// refusals are made on a fixture of another class — a shared caller's and a
// review caller's, whose auth and pairing are their own.
//
// The records the call read are given rather than assumed, because they are the
// one thing that differs between the refusals: a caller refused before the
// pairing reads none, one refused by the route's own gate reads none either, and
// one refused for the variable it named has read the record by then — which is
// how far the fail-closed order lets it get. Omitted, no record was read.
func assertRefused(t *testing.T, who string, resp *http.Response, body []byte, s *svc, reads *workflowReads, wantLines int, wantType string, wantRecords ...string) {
	t.Helper()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("%s was served a workflow secret: %s", who, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != wantType {
		t.Errorf("%s was refused %q, want %q", who, e.Error, wantType)
	}
	if !slices.Equal(reads.records.reads, wantRecords) {
		t.Errorf("%s read the records %v, want %v", who, reads.records.reads, wantRecords)
	}
	if len(reads.dyn.reads) != 0 {
		t.Errorf("%s caused a container to be read (%v); a refused caller is served no value", who, reads.dyn.reads)
	}
	if got := auditCount(t, s.audit); got != wantLines {
		t.Errorf("%s wrote %d audit lines, want %d", who, got, wantLines)
	}
	assertNoValue(t, s, reads, body)
}

// assertNoValue is G18 read over everything a call wrote: neither value appears
// in an audit line, in the service's own log, or in the answer — the body given,
// which is checked only where the value may not be in it (pass nil on the path
// that serves it, where the answer is the one place the value belongs).
func assertNoValue(t *testing.T, s *svc, reads *workflowReads, answer []byte) {
	t.Helper()
	where := map[string]string{
		"the audit line": s.audit.String(),
		"the log":        s.logs.String(),
	}
	if answer != nil {
		where["the refusal"] = string(answer)
	}
	for where, text := range where {
		if strings.Contains(text, reads.value) || strings.Contains(text, reads.otherValue) {
			t.Errorf("%s carries a workflow secret's value", where)
		}
	}
}

// ---------------------------------------------------------------------------
// The tests
// ---------------------------------------------------------------------------

// TestWorkflowSecretsServesTheRecordedSecretsOfTheCallersRepository is B3.2's
// red step: before it the route did not exist, so a job asking for one of its
// own repository's workflow secrets was answered the 404 of a path this service
// does not serve.
//
// What the answer must be is the whole of the route in one call: the value the
// RECORD named for the calling job's own workflow — read out of that container
// and no other — written no-store, and recorded as exactly one line naming the
// container and no value (G18).
func TestWorkflowSecretsServesTheRecordedSecretsOfTheCallersRepository(t *testing.T) {
	f := newWorkflowSvc(t)
	resp, body := f.fetch(t, testWorkflowEnv)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the calling job was answered %d, want its own workflow's secret: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: a cached workflow secret is a credential this service did not serve to the next caller", got)
	}
	if got := resp.Header.Get("Content-Type"); got != contentTypeJSON {
		t.Errorf("Content-Type = %q, want %q", got, contentTypeJSON)
	}
	ans := workflowAnswer(t, body)
	if ans.Env != testWorkflowEnv || ans.Value != f.value {
		t.Errorf("the answer is %+v, want the variable the call named and the value its own workflow's container holds", ans)
	}
	// The record's entry decided the read: the container the caller's workflow
	// names, once, and not the sibling workflow's.
	if want := []string{testWorkflowContainer}; !slices.Equal(f.dyn.reads, want) {
		t.Errorf("the service read %v, want exactly %v", f.dyn.reads, want)
	}
	if want := []string{testSlug(t, testRepoPath)}; !slices.Equal(f.records.reads, want) {
		t.Errorf("the service read the records %v, want exactly %v: the caller's own binding", f.records.reads, want)
	}
	line := f.onlyAuditLine(t)
	if got := auditField(line, "purpose"); got != PurposeWorkflowSecret {
		t.Errorf("the line's purpose = %q, want %q", got, PurposeWorkflowSecret)
	}
	if got := auditField(line, "credential"); got != testWorkflowContainer {
		t.Errorf("the line's credential = %q, want the container the value was read from, %q", got, testWorkflowContainer)
	}
	if got := auditField(line, "tier"); got != "" {
		t.Errorf("the line's tier = %q, want none: a workflow secret was selected at no tier", got)
	}
	if got := auditField(line, "status"); got != StatusOK {
		t.Errorf("the line's status = %q, want %q", got, StatusOK)
	}
	// The line is a run's own act and states the run like every other job-class
	// line (C1.13), so an operator reading it can tell whose value it was.
	if got := auditField(line, "run"); got != f.run {
		t.Errorf("the line's run = %q, want %q", got, f.run)
	}
	if got := auditField(line, "repository"); got != f.slug {
		t.Errorf("the line's repository = %q, want %q", got, f.slug)
	}
	if got := auditField(line, "job"); got != f.job {
		t.Errorf("the line's job = %q, want %q", got, f.job)
	}
	assertNoValue(t, f.svc, f.workflowReads, nil)
}

// TestWorkflowSecretsRefusesAnotherRepositorysWorkflow: another repository's job
// account never reaches this repository's record, whatever identity it
// presents.
//
// The first half is the refusal: that job's own token beside THIS repository's
// run identity is refused by the pairing, which is where the repository a call
// is served for is decided (a job account names one repository and the run
// identity's slug has to be the same one), so neither the record nor a
// container is read at all. The second half is what makes the first a binding
// and not a wall: the same job account, on its OWN repository's run, is served
// its own repository's container and never this one's.
func TestWorkflowSecretsRefusesAnotherRepositorysWorkflow(t *testing.T) {
	f := newWorkflowSvc(t)
	resp, body := f.fetchAs(t, testWorkflowEnv, f.otherJobToken())
	assertRefused(t, "another repository's job beside this repository's run", resp, body, f.svc, f.workflowReads, 1, ErrorUnverifiedRunIdentity)

	otherSlug := testSlug(t, testOtherRepoPath)
	resp, body = fetchWorkflowSecrets(t, f.svc, testWorkflowEnv, f.runTokenOf(otherSlug), f.otherJobToken(), f.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the other repository's job on its own run was answered %d, want its own repository's secret: %s", resp.StatusCode, body)
	}
	if ans := workflowAnswer(t, body); ans.Value != f.otherValue {
		t.Errorf("the answer carries another repository's value, want the caller's OWN repository's container's")
	}
	if want := []string{testOtherRepoContainer}; !slices.Equal(f.dyn.reads, want) {
		t.Errorf("the service read %v, want exactly %v: the caller's OWN repository's container", f.dyn.reads, want)
	}
	if want := []string{otherSlug}; !slices.Equal(f.records.reads, want) {
		t.Errorf("the service read the records %v, want exactly %v", f.records.reads, want)
	}
}

// TestWorkflowSecretsRefusesAnotherWorkflowOfTheSameRepository is G19's "and
// that workflow": one repository has one job account per workflow, and each is
// served its own workflow's secrets and never its sibling's.
//
// The sibling job asks for a variable the OTHER workflow declares. The record
// holds that variable, under the other workflow's name — and the answer is
// still a refusal, because the workflow a call is served for is the verified
// account's and the record is read under that name alone. There is no fallback
// to the repository's union of secrets, which is what a route keyed by the
// caller's own claim would serve.
func TestWorkflowSecretsRefusesAnotherWorkflowOfTheSameRepository(t *testing.T) {
	f := newWorkflowSvc(t)
	resp, body := f.fetchAs(t, testWorkflowEnv, f.siblingJobToken())
	assertRefused(t, "the sibling workflow's job asking for the other workflow's variable", resp, body, f.svc, f.workflowReads, 1, ErrorRequestRefused, testSlug(t, testRepoPath))
	if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, workflowSecretNoSuchEnv) {
		t.Errorf("the refusal is %q, want the route's own no-such-variable sentence", e.Message)
	}

	// The same job asking for ITS OWN workflow's variable is served, which is
	// what makes the refusal above about the workflow and not about the job.
	resp, body = f.fetchAs(t, testOtherWorkflowEnv, f.siblingJobToken())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the sibling job was refused its own workflow's secret: HTTP %d %s", resp.StatusCode, body)
	}
	if ans := workflowAnswer(t, body); ans.Env != testOtherWorkflowEnv || ans.Value != f.otherValue {
		t.Errorf("the answer is %+v, want the sibling workflow's own variable and container", ans)
	}
	if want := []string{testOtherWorkflowContainer}; !slices.Equal(f.dyn.reads, want) {
		t.Errorf("the service read %v, want exactly %v", f.dyn.reads, want)
	}
}

// TestWorkflowSecretsIgnoresTheRunJobHeader: the reported job name is the
// caller's own claim (X-Fugaro-Run-Job, spec §2.1) and decides nothing anywhere
// in this service. What it must not do here is move a call from one workflow's
// secrets to another's.
//
// Both halves are the same job account presenting the SIBLING workflow's name in
// the header: the sibling's variable is still refused (the header did not widen
// what the caller is served), and its own workflow's variable is still served
// (the header did not narrow it either). A route that read the header would
// answer the two the other way round.
func TestWorkflowSecretsIgnoresTheRunJobHeader(t *testing.T) {
	f := newWorkflowSvc(t)
	const claimed = "fugaro-aurora-app-review-fix"
	if !ValidJobName(claimed) {
		t.Fatalf("the fixture's own claimed job name %q is not one this service would accept", claimed)
	}
	resp, body := fetchWorkflowSecrets(t, f.svc, testOtherWorkflowEnv, f.runToken(), f.jobToken(), claimed)
	assertRefused(t, "a job claiming the sibling workflow's name", resp, body, f.svc, f.workflowReads, 1, ErrorRequestRefused, testSlug(t, testRepoPath))

	resp, body = fetchWorkflowSecrets(t, f.svc, testWorkflowEnv, f.runToken(), f.jobToken(), claimed)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the calling job was refused its own workflow's secret because of the name it reported: HTTP %d %s", resp.StatusCode, body)
	}
	if ans := workflowAnswer(t, body); ans.Value != f.value {
		t.Errorf("the answer is %+v, want the calling ACCOUNT's own workflow's value", ans)
	}
}

// TestJobAccountsCarryTheirWorkflow: the row a deployment hands the service
// states which workflow's job the account is, and JobAccountsFor fills it from
// the RepoJob it was built from.
//
// It is the fact the whole route rests on (G19): the workflow a call is served
// for is read off the VERIFIED account, so a table that dropped this member
// would leave every row unable to say which workflow it is — and a row that
// carried another workflow's name would serve one job's secrets to another.
func TestJobAccountsCarryTheirWorkflow(t *testing.T) {
	jobs := []RepoJob{
		{Repo: testRepoPath, Workflow: testWorkflow},
		{Repo: testRepoPath, Workflow: testOtherWorkflow},
		{Repo: testOtherRepoPath, Workflow: testWorkflow},
	}
	got, err := JobAccountsFor("fugaro", "aurora-dev", "github", jobs)
	if err != nil {
		t.Fatalf("JobAccountsFor: %v", err)
	}
	if len(got) != len(jobs) {
		t.Fatalf("JobAccountsFor built %d rows, want one per job, %d: %+v", len(got), len(jobs), got)
	}
	for i, ja := range got {
		if ja.Workflow != jobs[i].Workflow {
			t.Errorf("row %d (%s) carries workflow %q, want %q", i, ja.Repo, ja.Workflow, jobs[i].Workflow)
		}
		if want := serviceAccountEmail(gcp.ServiceAccountID(ja.Slug, jobs[i].Workflow), "aurora-dev"); ja.Email != want {
			t.Errorf("row %d's account = %q, want the derivation over the slug and the workflow, %q", i, ja.Email, want)
		}
	}
	// The two rows of ONE repository are one slug, one canonical repository and
	// two accounts: the workflow is the whole of what tells them apart, which is
	// why it cannot be dropped from the row.
	first, second := got[0], got[1]
	if first.Slug != second.Slug || first.Repo != second.Repo {
		t.Errorf("the two rows of one repository disagree about it: %+v and %+v", first, second)
	}
	if first.Email == second.Email {
		t.Errorf("the two workflows of one repository share the account %q, so no call could be told from the other", first.Email)
	}
	if first.Workflow == second.Workflow {
		t.Errorf("the two rows of one repository carry the same workflow %q", first.Workflow)
	}
}

// TestWorkflowSecretsRefusesSharedReviewLauncherAndPersonCallers: this route
// serves a per-repository job of a workflow and nobody else (G19).
//
// A shared project job and a review job are the two callers the service admits
// that are NOT a workflow's job: their accounts name no repository and no
// workflow, and the pairing synthesises their rows — so the route's own class
// gate is what refuses them, and it is reached with the pairing already
// satisfied (each fixture's caller is admitted on the model path, which is why
// the refusal below is this route's and not the pairing's).
//
// A launcher and a person hold no run identity at all, so they are refused
// before any route is reached: the launcher here IS one this service admits as a
// launcher (it may ask for a slip), and it is still refused this route, which is
// the whole point.
func TestWorkflowSecretsRefusesSharedReviewLauncherAndPersonCallers(t *testing.T) {
	sharedReads := newWorkflowReads(t)
	shared := newSharedCodingSvc(t, sharedReads.wire)
	resp, body := fetchWorkflowSecrets(t, shared, testWorkflowEnv, shared.sharedRunToken(), shared.sharedJobToken(), shared.job)
	assertRefused(t, "a shared project job", resp, body, shared, sharedReads, 1, ErrorUnverifiedRunIdentity)

	reviewReads := newWorkflowReads(t)
	review := newReviewSvcSpelled(t, testReviewSA, testReviewSA, reviewReads.wire)
	resp, body = fetchWorkflowSecrets(t, review, testWorkflowEnv, review.reviewRunToken(), review.reviewJobToken(), review.job)
	assertRefused(t, "a review job", resp, body, review, reviewReads, 1, ErrorUnverifiedRunIdentity)

	// The two below are refused before the identity is verified, so they write
	// NO line: a line states the identity the service verified, and there is
	// none.
	f := newWorkflowSvc(t)
	resp, body = f.callPerson(t, WorkflowSecretsPath, workflowBody(testWorkflowEnv), f.launcherToken())
	assertRefused(t, "a launcher", resp, body, f.svc, f.workflowReads, 0, ErrorUnverifiedRunIdentity)

	resp, body = f.callPerson(t, WorkflowSecretsPath, workflowBody(testWorkflowEnv), f.personToken(testPersonAccount))
	assertRefused(t, "a person", resp, body, f.svc, f.workflowReads, 0, ErrorUnverifiedRunIdentity)
}

// TestWorkflowSecretsRefusesAnEndedRun: the liveness check every route is held
// to applies here too, and it is the same check — the run's outcome node, read
// per call. A run that has ended is refused before the route is reached, so it
// reads no record and no container and writes no line.
func TestWorkflowSecretsRefusesAnEndedRun(t *testing.T) {
	f := newWorkflowSvc(t)
	f.db.set(budget.PathOutcome(budget.Day(f.now), f.slug, f.run))
	resp, body := f.fetch(t, testWorkflowEnv)
	assertRefused(t, "an ended run", resp, body, f.svc, f.workflowReads, 0, ErrorUnverifiedRunIdentity)
}

// TestWorkflowSecretsFailedReadFailsClosed: every step of the route that cannot
// answer is a typed refusal with no fallback, and there is no path from any of
// them to a value the record did not name.
//
// Each case is one way the route's own reads come up empty, and each is asserted
// whole: the typed refusal, the record and the container read no further than
// the step before, exactly one audit line, and no value anywhere. A service that
// answered the bundle's value, an empty value or nothing at all would be serving
// a credential this repository's own record does not name (D143, G19).
func TestWorkflowSecretsFailedReadFailsClosed(t *testing.T) {
	noWorkflow := creds.WorkflowSecrets{
		Version:   creds.WorkflowSecretsVersion,
		Workflows: map[string][]creds.WorkflowSecret{testOtherWorkflow: {{Env: testOtherWorkflowEnv, ID: testOtherWorkflowContainer}}},
	}
	cases := []struct {
		name       string
		extra      []func(*svc, *Config)
		after      func(*workflowFixture)
		env        string
		wantStatus int
		wantType   string
		// wantCredential is what the audit line may name: the container only
		// once the record has named it, which is why the last three cases carry
		// it and every case above them does not.
		wantCredential string
	}{
		{
			name:       "no record reader at all",
			extra:      []func(*svc, *Config){func(_ *svc, cfg *Config) { cfg.WorkflowSecrets = nil }},
			env:        testWorkflowEnv,
			wantStatus: StatusUnavailable,
			wantType:   ErrorStateUnavailable,
		},
		{
			name:       "no record for the repository",
			after:      func(f *workflowFixture) { delete(f.records.recs, testSlug(t, testRepoPath)) },
			env:        testWorkflowEnv,
			wantStatus: StatusUnavailable,
			wantType:   ErrorStateUnavailable,
		},
		{
			name:       "the record could not be read",
			after:      func(f *workflowFixture) { f.records.err = errors.New("the record could not be read") },
			env:        testWorkflowEnv,
			wantStatus: StatusUnavailable,
			wantType:   ErrorStateUnavailable,
		},
		{
			name: "the record holds nothing for the calling job's workflow",
			after: func(f *workflowFixture) {
				f.records.recs[testSlug(t, testRepoPath)] = noWorkflow
			},
			env:        testWorkflowEnv,
			wantStatus: StatusUnavailable,
			wantType:   ErrorStateUnavailable,
		},
		{
			name: "the record's container is not a Secret Manager secret ID",
			after: func(f *workflowFixture) {
				rec := workflowRecord()
				rec.Workflows[testWorkflow] = []creds.WorkflowSecret{{Env: testWorkflowEnv, ID: "aurora/../elsewhere"}}
				f.records.recs[testSlug(t, testRepoPath)] = rec
			},
			env:        testWorkflowEnv,
			wantStatus: StatusUnavailable,
			wantType:   ErrorStateUnavailable,
		},
		{
			name:           "the service has no per-secret reader",
			extra:          []func(*svc, *Config){func(_ *svc, cfg *Config) { cfg.Bundles = &BundleLoader{Source: docOnly{doc: testBundleDoc(t)}} }},
			env:            testWorkflowEnv,
			wantStatus:     StatusUnavailable,
			wantType:       ErrorStateUnavailable,
			wantCredential: testWorkflowContainer,
		},
		{
			name:           "the container could not be read",
			after:          func(f *workflowFixture) { f.dyn.err = errors.New("the container could not be read") },
			env:            testWorkflowEnv,
			wantStatus:     StatusUnavailable,
			wantType:       ErrorStateUnavailable,
			wantCredential: testWorkflowContainer,
		},
		{
			name:           "the container is not there",
			after:          func(f *workflowFixture) { delete(f.dyn.values, testWorkflowContainer) },
			env:            testWorkflowEnv,
			wantStatus:     StatusUnavailable,
			wantType:       ErrorStateUnavailable,
			wantCredential: testWorkflowContainer,
		},
		{
			name:       "the call names a variable the record does not hold",
			env:        "NOT_A_RECORDED_VARIABLE",
			wantStatus: StatusRequestRefused,
			wantType:   ErrorRequestRefused,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkflowSvc(t, tc.extra...)
			if tc.after != nil {
				tc.after(f)
			}
			resp, body := f.fetch(t, tc.env)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("the refusal is HTTP %d, want %d: %s", resp.StatusCode, tc.wantStatus, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != tc.wantType {
				t.Errorf("the refusal is %q, want %q", e.Error, tc.wantType)
			}
			// One line, and it names the container only where the record had
			// already named it — never a value, and never a container the
			// record did not answer with.
			line := f.onlyAuditLine(t)
			if got := auditField(line, "purpose"); got != PurposeWorkflowSecret {
				t.Errorf("the line's purpose = %q, want %q", got, PurposeWorkflowSecret)
			}
			if got := auditField(line, "credential"); got != tc.wantCredential {
				t.Errorf("the line's credential = %q, want %q", got, tc.wantCredential)
			}
			assertNoValue(t, f.svc, f.workflowReads, body)
		})
	}
}

// TestWorkflowSecretsNoValueInAuditOrError: the value travels into the answer
// and into nothing this service writes (G18). It is asserted on the path that
// serves it and on the path that refuses it AFTER the container was read — an
// audit line that could not be written, which is the one refusal of this route
// that is taken with the value already in hand.
func TestWorkflowSecretsNoValueInAuditOrError(t *testing.T) {
	f := newWorkflowSvc(t)
	resp, body := f.fetch(t, testWorkflowEnv)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the calling job was answered %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), f.value) {
		t.Fatalf("the answer does not carry the value, so this test proves nothing about where it went: %s", body)
	}
	assertNoValue(t, f.svc, f.workflowReads, nil)

	// A service whose audit line cannot be written serves nothing (D143: an act
	// this service cannot record is not made), and the refusal it writes instead
	// names the container and never the value. The sink below refuses every line,
	// so this is the real failure and not a fixture's flag.
	broken := newWorkflowSvc(t, func(_ *svc, cfg *Config) {
		cfg.AuditLog = slog.NewJSONHandler(errWriter{}, nil)
	})
	resp, body = broken.fetch(t, testWorkflowEnv)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a service that could not record the act answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	assertNoValue(t, broken.svc, broken.workflowReads, body)
}

// errWriter is a log sink that refuses everything written to it, so that an
// audit line the service cannot write is a real failure and not a flag.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("the audit log is not writable") }

// TestWorkflowSecretsOneAuditLinePerRefusal: once the caller's identity is
// verified, every refusal writes exactly one line and no path writes zero or
// two (C1.13). The refusals below are the route's own and the pairing's, in the
// order a call meets them; each is asserted on a fixture of its own, so that
// "one line" is one request's.
func TestWorkflowSecretsOneAuditLinePerRefusal(t *testing.T) {
	calls := []struct {
		name string
		call func(t *testing.T, f *workflowFixture) (*http.Response, []byte)
	}{
		{"a variable that is not an environment variable's name", func(t *testing.T, f *workflowFixture) (*http.Response, []byte) {
			return f.call(t, `{"env":"lowercase"}`)
		}},
		{"a body this contract does not read", func(t *testing.T, f *workflowFixture) (*http.Response, []byte) {
			return f.call(t, `{"env":"DEPLOY_TOKEN","extra":1}`)
		}},
		{"a body that is not JSON at all", func(t *testing.T, f *workflowFixture) (*http.Response, []byte) {
			return f.call(t, `not a body`)
		}},
		{"a variable the record does not hold", func(t *testing.T, f *workflowFixture) (*http.Response, []byte) {
			return f.fetch(t, "NOT_A_RECORDED_VARIABLE")
		}},
		{"a workflow the record holds nothing for", func(t *testing.T, f *workflowFixture) (*http.Response, []byte) {
			f.records.recs[testSlug(t, testRepoPath)] = creds.WorkflowSecrets{
				Version:   creds.WorkflowSecretsVersion,
				Workflows: map[string][]creds.WorkflowSecret{},
			}
			return f.fetch(t, testWorkflowEnv)
		}},
		{"a repository the record does not name", func(t *testing.T, f *workflowFixture) (*http.Response, []byte) {
			delete(f.records.recs, testSlug(t, testRepoPath))
			return f.fetch(t, testWorkflowEnv)
		}},
		{"a record that could not be read", func(t *testing.T, f *workflowFixture) (*http.Response, []byte) {
			f.records.err = errors.New("the record could not be read")
			return f.fetch(t, testWorkflowEnv)
		}},
		{"a container that could not be read", func(t *testing.T, f *workflowFixture) (*http.Response, []byte) {
			f.dyn.err = errors.New("the container could not be read")
			return f.fetch(t, testWorkflowEnv)
		}},
		{"a job of another repository", func(t *testing.T, f *workflowFixture) (*http.Response, []byte) {
			return f.fetchAs(t, testWorkflowEnv, f.otherJobToken())
		}},
		{"the operator's stop switch", func(t *testing.T, f *workflowFixture) (*http.Response, []byte) {
			f.kills.repos[f.slug] = true
			return f.fetch(t, testWorkflowEnv)
		}},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkflowSvc(t)
			resp, body := tc.call(t, f)
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("the call was served: %s", body)
			}
			line := f.onlyAuditLine(t)
			if got := auditField(line, "purpose"); got != PurposeWorkflowSecret {
				t.Errorf("the line's purpose = %q, want %q: every refusal of this route is this route's act", got, PurposeWorkflowSecret)
			}
			if got := auditField(line, "status"); got == StatusOK {
				t.Errorf("the line's status = %q, want the refusal's own type", got)
			}
			assertNoValue(t, f.svc, f.workflowReads, body)
		})
	}
}

// TestWorkflowSecretsNoReaderRefusesEvenForARecordedRepository: a service with
// no record reader is a deployment whose database client is not wired, and the
// route refuses rather than serving nothing — the fail-closed answer and not an
// empty one (G19).
//
// The real reader's nil-database branch is pinned here too: RTDBWorkflowSecrets
// with no database answers as a FAILED read and not as an absent record, so an
// operator sees one incident where a run sees one refusal.
func TestWorkflowSecretsNoReaderRefusesEvenForARecordedRepository(t *testing.T) {
	f := newWorkflowSvc(t, func(_ *svc, cfg *Config) { cfg.WorkflowSecrets = nil })
	resp, body := f.fetch(t, testWorkflowEnv)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a service with no reader answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if len(f.dyn.reads) != 0 {
		t.Errorf("a service with no record read %v; there is no container it could have named", f.dyn.reads)
	}

	if _, found, err := (RTDBWorkflowSecrets{}).Read(context.Background(), testSlug(t, testRepoPath)); err == nil || found {
		t.Errorf("RTDBWorkflowSecrets with no database answered found=%t err=%v, want a failed read", found, err)
	}
}
