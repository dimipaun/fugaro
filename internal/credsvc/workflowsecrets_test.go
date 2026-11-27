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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
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

// The project-shared side of the fixture (IS1): the logical name a version-2
// call asks for, and the container the grammar builds from it. They are the
// same name and container a `fugaro secrets shared set` writes, and the record
// below is the one the owner's `secrets allow` leaves.
const (
	testSharedName      = "deploy-token"
	testSharedContainer = "fugaro_p_deploy-token"
	// testOtherName is a logical name the fixture's project record does not admit
	// the fixture's repository to (D3459): a file that declares it is refused by
	// the allow-list, which is what tells a read of the repository's default
	// branch from a read of any other ref.
	testOtherName = "other-token"
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

// fakeProjectRecords is the project record (IS3's codec) the fixture serves: one
// document for the installation, and a read counter rather than a list of names,
// because the record is one node and not one per repository.
type fakeProjectRecords struct {
	rec   creds.ProjectSecrets
	found bool
	err   error
	// reads is how many times the record was asked for: a refusal made before
	// the record is reached reads none, and one made by the allow-list reads
	// exactly one.
	reads int
}

func (f *fakeProjectRecords) Read(context.Context) (creds.ProjectSecrets, bool, error) {
	f.reads++
	if f.err != nil {
		return creds.ProjectSecrets{}, false, f.err
	}
	return f.rec, f.found, nil
}

// projectRecord is the record `fugaro secrets allow` leaves for the fixture's
// one name: the fixture's own repository admitted, by the spelling the local
// block's `allow_data_to` uses (owner/name, and never the provider-qualified
// canonical form the verified job account carries).
func projectRecord(repos ...string) creds.ProjectSecrets {
	return creds.ProjectSecrets{
		Version: creds.ProjectSecretsVersion,
		Secrets: map[string]creds.ProjectSecretRule{testSharedName: {Repos: repos}},
	}
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

// fakeRepoFiles is the code host, stood in for (UO7, D3459): what a repository's
// own fugaro.yaml holds at its default branch, and what a read at any OTHER ref
// answers with. It is a fake of the SEAM and not of GitHub: what this file's
// tests hold is which repository, which file and which ref the service asks for,
// and that a file which cannot be read is a refusal rather than the record.
type fakeRepoFiles struct {
	// yaml is the default branch's fugaro.yaml, by the canonical repository name
	// the read names. A repository not in the map is one whose file is not
	// there.
	yaml map[string][]byte
	// refYAML is what a read at a ref other than the default branch answers: a
	// test that let the service name the run's own branch would be served this
	// file instead of the repository's.
	refYAML []byte
	// unreachable makes every read answer the code host not answering, which is
	// the one answer the record is a fallback for (D3459) — and the state every
	// test that is not about the file read is in.
	unreachable bool
	// err is any other failure a read answers with: a file that is not there, a
	// host that refused the credential.
	err error
	// reads is every read this host was asked for, in order, which is what makes
	// "the repository is the run's and the ref is the default branch" a fact
	// rather than an inference.
	reads []RepoFileQuery
}

func (f *fakeRepoFiles) Read(_ context.Context, q RepoFileQuery) ([]byte, error) {
	f.reads = append(f.reads, q)
	if f.unreachable {
		return nil, fmt.Errorf("%w: this test's code host is down", ErrRepoFileUnreachable)
	}
	if f.err != nil {
		return nil, f.err
	}
	if q.Ref != "" && f.refYAML != nil {
		return f.refYAML, nil
	}
	data, ok := f.yaml[q.Repo]
	if !ok {
		return nil, errors.New("the repository holds no " + q.Path)
	}
	return data, nil
}

// repoYAML is a repository's own fugaro.yaml as these tests write it: one
// workflow and the secrets it declares, in the spelling config.RepoOf reads. The
// file carries the logical NAME and the variable a job mounts it under, and
// never a value (G18).
func repoYAML(workflow, env, name string) []byte {
	return []byte("workflows:\n  " + workflow + ":\n    secrets:\n      - name: " + name + "\n        env: " + env + "\n")
}

// workflowReads is the three things this route reads, stood in for: the record,
// the container reader and the two values. It is separate from workflowFixture
// because two of the class tests wire it into a fixture of ANOTHER class — a
// shared caller's and a review caller's, whose auth and pairing are their own —
// and the route's own reads are the same object there.
type workflowReads struct {
	records *fakeWorkflowRecords
	dyn     *dynReader
	// files is the code host the repository's own fugaro.yaml is read through
	// (UO7, D3459), unreachable until a test says otherwise: the record below is
	// then the fallback every test that is not about the file read is served
	// from, exactly as it was before this route read a file at all.
	files *fakeRepoFiles
	// projects is the project record a version-2 call is admitted by (IS1), and
	// sharedValue is what the project's own container holds. The two are on this
	// type rather than on a fixture of their own because the version-1 and
	// version-2 halves of the route read through the same three readers.
	projects    *fakeProjectRecords
	sharedValue string
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
//
// The project side is seeded the way `fugaro secrets shared set` and `secrets
// allow` leave an installation: one name, its value in the grammar's own
// container, that container MARKED (F18, G2), and the fixture's repository
// admitted to the name.
func newWorkflowReads(t *testing.T) *workflowReads {
	t.Helper()
	r := &workflowReads{
		records:     &fakeWorkflowRecords{},
		dyn:         &dynReader{values: map[string]string{}, marked: map[string]bool{}},
		files:       &fakeRepoFiles{unreachable: true},
		projects:    &fakeProjectRecords{rec: projectRecord(testRepoPath), found: true},
		value:       fakeCred(t, "workflow-secret"),
		otherValue:  fakeCred(t, "other-workflow-secret"),
		sharedValue: fakeCred(t, "shared-workflow-secret"),
	}
	r.dyn.values[testWorkflowContainer] = r.value
	r.dyn.values[testOtherWorkflowContainer] = r.otherValue
	r.dyn.values[testOtherRepoContainer] = r.otherValue
	r.dyn.values[testSharedContainer] = r.sharedValue
	r.dyn.marked[testSharedContainer] = true
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
	cfg.ProjectSecrets = r.projects
	cfg.RepoFiles = r.files
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

// workflowBodyV2 is the other version's body (D3324): the logical secret's name
// and the version that says which shape this is, and no variable at all.
func workflowBodyV2(name string) string { return `{"name":"` + name + `","version":2}` }

// workflowBodyApp is version 1's body with the App ID a GitHub repository's call
// carries (UO7, D3459). The App ID is the one thing a caller supplies to a GitHub
// App mint and it is not a secret (githubread.go), and it is what the service
// signs the read of the repository's own fugaro.yaml as: without it a GitHub
// repository's file cannot be read at all, which is a refused request and never
// the record.
func workflowBodyApp(env, appID string) string {
	return `{"env":"` + env + `","app_id":"` + appID + `"}`
}

// workflowBodyV2App is the same for version 2's shape.
func workflowBodyV2App(name, appID string) string {
	return `{"name":"` + name + `","version":2,"app_id":"` + appID + `"}`
}

// openTheDoor opens the installation's repository-secrets door for one test and
// puts it back (G9, F24): `config.RepoSecretsOff` false is D2313 reversed, the
// state a repository's own copies are in force in, and it is the state version 2
// is REFUSED in — a project copy would shadow a repository's own value. The
// served path is the door SHUT, which is the state every installation is in
// until the flip, so a test of that path calls nothing here.
func openTheDoor(t *testing.T) {
	t.Helper()
	prev := config.RepoSecretsOff
	config.RepoSecretsOff = false
	t.Cleanup(func() { config.RepoSecretsOff = prev })
}

// fetchWorkflowSecrets sends one workflow-secrets call as the given identities
// and reads the answer whole. It is a function rather than a method so a
// fixture of another class — whose auth and pairing are its own — can make the
// call too.
func fetchWorkflowSecrets(t *testing.T, s *svc, env, runTok, jobTok, job string) (*http.Response, []byte) {
	t.Helper()
	return fetchWorkflowSecretsBody(t, s, workflowBody(env), runTok, jobTok, job)
}

// fetchWorkflowSecretsBody is the same call with the body given rather than
// built, which is what the version-2 tests need.
func fetchWorkflowSecretsBody(t *testing.T, s *svc, body, runTok, jobTok, job string) (*http.Response, []byte) {
	t.Helper()
	resp := s.postTokens(t, WorkflowSecretsPath, body, runTok, jobTok, job)
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

// fetchV2 is one version-2 call as the fixture's own job: it names a logical
// secret and no variable (D3324).
func (f *workflowFixture) fetchV2(t *testing.T, name string) (*http.Response, []byte) {
	t.Helper()
	return fetchWorkflowSecretsBody(t, f.svc, workflowBodyV2(name), f.runToken(), f.jobToken(), f.job)
}

// fetchApp is one version-1 call as the fixture's own job, carrying the App ID a
// GitHub repository's read is signed as (UO7, D3459).
func (f *workflowFixture) fetchApp(t *testing.T, env, appID string) (*http.Response, []byte) {
	t.Helper()
	return fetchWorkflowSecretsBody(t, f.svc, workflowBodyApp(env, appID), f.runToken(), f.jobToken(), f.job)
}

// fetchV2App is the same for version 2's shape.
func (f *workflowFixture) fetchV2App(t *testing.T, name, appID string) (*http.Response, []byte) {
	t.Helper()
	return fetchWorkflowSecretsBody(t, f.svc, workflowBodyV2App(name, appID), f.runToken(), f.jobToken(), f.job)
}

// served is one served call: the answer's value and the variable or name it
// echoed, which is what a test that is about WHERE a value came from has to read
// and nothing else.
func served(t *testing.T, who string, resp *http.Response, body []byte) WorkflowSecretsResponse {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s was refused: %d %s", who, resp.StatusCode, body)
	}
	return workflowAnswer(t, body)
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
		for _, value := range []string{reads.value, reads.otherValue, reads.sharedValue} {
			if strings.Contains(text, value) {
				t.Errorf("%s carries a workflow secret's value", where)
			}
		}
	}
}

// assertRefusedV2 is assertRefused for a version-2 call: the typed refusal, the
// status it is answered with, no container and no marker read at all — a refused
// caller is served no value and is not told what any container holds — and the
// audit lines the point of the call allows.
func assertRefusedV2(t *testing.T, who string, resp *http.Response, body []byte, s *svc, reads *workflowReads, wantStatus int, wantLines int, wantType string) {
	t.Helper()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("%s was served a workflow secret: %s", who, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != wantType {
		t.Errorf("%s was refused %q, want %q", who, e.Error, wantType)
	}
	if resp.StatusCode != wantStatus {
		t.Errorf("%s was answered HTTP %d, want %d", who, resp.StatusCode, wantStatus)
	}
	if len(reads.dyn.reads) != 0 {
		t.Errorf("%s caused a container's VALUE to be read (%v); a refused caller is served none", who, reads.dyn.reads)
	}
	if len(reads.dyn.markerReads) != 0 {
		t.Errorf("%s caused a container's marker to be read (%v); the refusal was made before the container", who, reads.dyn.markerReads)
	}
	if got := auditCount(t, s.audit); got != wantLines {
		t.Errorf("%s wrote %d audit lines, want %d", who, got, wantLines)
	}
	assertNoValue(t, s, reads, body)
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

// TestWorkflowSecretsRefusesABodyOfAnotherVersion: this route serves version 1
// and nothing else (wire.go's Validate), and the strict decoder is no longer
// enough on its own.
//
// `name` and `version` are fields this contract carries now, so a body that
// names a secret, or that declares version 2, DECODES where it used to be
// refused as an unknown field — and the route would then answer it as a
// version-1 call, looking the variable up in a record that never held one. Both
// are bodies version 1 never wrote, and the shape rule refuses them before any
// record is read: exactly the answer the route gave when the fields did not
// exist yet.
func TestWorkflowSecretsRefusesABodyOfAnotherVersion(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a version 2 body", `{"env":"DEPLOY_TOKEN","version":2}`},
		{"a body carrying both shapes", `{"env":"DEPLOY_TOKEN","name":"deploy-token"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkflowSvc(t)
			resp, body := f.call(t, tc.body)
			// No record read and no container touched: assertRefused holds both,
			// which is what makes this the shape rule and not a lookup.
			assertRefused(t, "a body of another version", resp, body, f.svc, f.workflowReads, 1, ErrorRequestRefused)
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

// ---------------------------------------------------------------------------
// Version 2 (IS1, D3324, D3328, D3333)
// ---------------------------------------------------------------------------

// TestWorkflowSecretsV2ServesAListedRepository is IS1's red step. Before the
// version-2 branch, a call naming a logical secret reached the version-1 route,
// which looked the EMPTY variable it carries up in the repository's record and
// refused it (request_refused) — the answer this test saw first.
//
// What the answer must be is version 2 whole: the value out of the project's own
// `fugaro_p_<name>` container, admitted by the owner's record and by the
// container's marker, written no-store, and recorded as exactly one line naming
// that container and no value (G18).
func TestWorkflowSecretsV2ServesAListedRepository(t *testing.T) {
	f := newWorkflowSvc(t)
	resp, body := f.fetchV2(t, testSharedName)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a listed repository was answered %d, want the project secret: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: a cached workflow secret is a credential this service did not serve to the next caller", got)
	}
	ans := workflowAnswer(t, body)
	if ans.Name != testSharedName || ans.Value != f.sharedValue {
		t.Errorf("the answer is %+v, want the name the call named and the project container's value", ans)
	}
	if ans.Env != "" {
		t.Errorf("a version-2 answer carries the variable %q, and it carries none: the call named no variable", ans.Env)
	}
	// The container the NAME built, read once, and the marker read once for it.
	if want := []string{testSharedContainer}; !slices.Equal(f.dyn.reads, want) {
		t.Errorf("the service read the containers %v, want exactly %v", f.dyn.reads, want)
	}
	if want := []string{testSharedContainer}; !slices.Equal(f.dyn.markerReads, want) {
		t.Errorf("the service read the markers of %v, want exactly %v: the container the name builds is the one the marker is asked about", f.dyn.markerReads, want)
	}
	// The record is the fence, and it is read once: a second read would be a
	// second decision, and the two could disagree.
	if f.projects.reads != 1 {
		t.Errorf("the project record was read %d times, want once", f.projects.reads)
	}
	line := f.onlyAuditLine(t)
	if got := auditField(line, "purpose"); got != PurposeWorkflowSecret {
		t.Errorf("the line's purpose = %q, want %q", got, PurposeWorkflowSecret)
	}
	if got := auditField(line, "credential"); got != testSharedContainer {
		t.Errorf("the line's credential = %q, want the container the value was read from, %q", got, testSharedContainer)
	}
	if got := auditField(line, "status"); got != StatusOK {
		t.Errorf("the line's status = %q, want %q", got, StatusOK)
	}
	if got := auditField(line, "run"); got != f.run {
		t.Errorf("the line's run = %q, want %q", got, f.run)
	}
	if got := auditField(line, "repository"); got != f.slug {
		t.Errorf("the line's repository = %q, want %q", got, f.slug)
	}
	assertNoValue(t, f.svc, f.workflowReads, nil)
}

// TestWorkflowSecretsV2RefusesAnUnlistedRepository is D3328's fence: the record
// holds the name and does not admit this repository to it, so the call is
// refused with the code the runner reads to print the fix — and the refusal is
// made BEFORE any container is touched, so a repository that may not fetch a
// secret learns nothing about it.
//
// The repository is checked in the spelling the record's patterns use: the
// verified job account carries the canonical provider/owner/name, and the
// owner/name the local block's `allow_data_to` writes is what must match. The
// second case is the same record read by a job of the OTHER repository, which is
// what makes "the caller's own repository decides" a fact rather than a claim.
func TestWorkflowSecretsV2RefusesAnUnlistedRepository(t *testing.T) {
	t.Run("the record lists another repository", func(t *testing.T) {
		f := newWorkflowSvc(t)
		f.projects.rec = projectRecord(testOtherRepoPath)
		resp, body := f.fetchV2(t, testSharedName)
		assertRefusedV2(t, "a repository the record does not admit", resp, body, f.svc, f.workflowReads, StatusUnverified, 1, ErrorWorkflowSecretNotAllowed)
		if f.projects.reads != 1 {
			t.Errorf("the project record was read %d times, want once: the allow-list is what refused", f.projects.reads)
		}
		if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, "fugaro secrets allow") {
			t.Errorf("the refusal does not name the fix a person runs: %q", e.Message)
		}
	})
	t.Run("the record names no such secret", func(t *testing.T) {
		f := newWorkflowSvc(t)
		resp, body := f.fetchV2(t, "not-a-listed-name")
		assertRefusedV2(t, "a name the record does not hold", resp, body, f.svc, f.workflowReads, StatusUnverified, 1, ErrorWorkflowSecretNotAllowed)
	})
	t.Run("there is no record at all", func(t *testing.T) {
		f := newWorkflowSvc(t)
		f.projects.found = false
		resp, body := f.fetchV2(t, testSharedName)
		// An absent record admits nobody: it is never read as "every name is
		// allowed" (G19).
		assertRefusedV2(t, "an installation with no project record", resp, body, f.svc, f.workflowReads, StatusUnverified, 1, ErrorWorkflowSecretNotAllowed)
	})
	t.Run("the record could not be read", func(t *testing.T) {
		f := newWorkflowSvc(t)
		f.projects.err = errors.New("the record could not be read")
		resp, body := f.fetchV2(t, testSharedName)
		assertRefusedV2(t, "an unreadable project record", resp, body, f.svc, f.workflowReads, StatusUnavailable, 1, ErrorStateUnavailable)
	})
	t.Run("the service has no record reader", func(t *testing.T) {
		f := newWorkflowSvc(t, func(_ *svc, cfg *Config) { cfg.ProjectSecrets = nil })
		resp, body := f.fetchV2(t, testSharedName)
		assertRefusedV2(t, "a service with no project record reader", resp, body, f.svc, f.workflowReads, StatusUnavailable, 1, ErrorStateUnavailable)
	})
	t.Run("the caller's own repository is the one checked", func(t *testing.T) {
		f := newWorkflowSvc(t)
		f.projects.rec = projectRecord(testRepoPath)
		// The other repository's job account presents ITS own run identity, so
		// the pairing binds it: the record admits the fixture's repository and
		// not this one, and the call is refused on that alone.
		resp, body := fetchWorkflowSecretsBody(t, f.svc, workflowBodyV2(testSharedName), f.runTokenOf(testSlug(t, testOtherRepoPath)), f.otherJobToken(), f.job)
		assertRefusedV2(t, "another repository's job", resp, body, f.svc, f.workflowReads, StatusUnverified, 1, ErrorWorkflowSecretNotAllowed)
	})
}

// TestWorkflowSecretsV2RefusesAnUnmarkedContainer is G2's half of the fence
// (F18): the record admits the repository and the container is there, but it
// carries no workflow-secret marker, so it is the project's own credential of
// that name rather than a workflow secret's and this route does not serve it.
//
// The container's VALUE is never read, which is the point of the order: the
// marker is asked for first, so a name that resolves to a model credential's
// container is refused without that credential ever entering this process.
func TestWorkflowSecretsV2RefusesAnUnmarkedContainer(t *testing.T) {
	f := newWorkflowSvc(t)
	delete(f.dyn.marked, testSharedContainer)
	resp, body := f.fetchV2(t, testSharedName)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("an unmarked container answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorWorkflowSecretUnmarked {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorWorkflowSecretUnmarked)
	}
	if len(f.dyn.reads) != 0 {
		t.Errorf("the service read the value of %v; the marker is asked for FIRST, so a container that is not a workflow secret's is never read", f.dyn.reads)
	}
	if want := []string{testSharedContainer}; !slices.Equal(f.dyn.markerReads, want) {
		t.Errorf("the service asked about the markers of %v, want exactly %v", f.dyn.markerReads, want)
	}
	if got := auditCount(t, f.audit); got != 1 {
		t.Errorf("the refusal wrote %d audit lines, want 1", got)
	}
	assertNoValue(t, f.svc, f.workflowReads, body)
}

// TestWorkflowSecretsV2RefusesAnUnreadableMarker: a marker read that FAILED is
// not "the container is unmarked". The two have two fixes — one is the
// installation's state and the other is the container's — and a service that
// answered the second for the first would send an operator to rewrite a
// container that is already right. Both are refusals, and neither reads a value.
func TestWorkflowSecretsV2RefusesAnUnreadableMarker(t *testing.T) {
	f := newWorkflowSvc(t)
	f.dyn.markerErr = errors.New("the container's labels could not be listed")
	resp, body := f.fetchV2(t, testSharedName)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("an unreadable marker answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q: a marker that could not be read is the installation's state, not the container's shape", e.Error, ErrorStateUnavailable)
	}
	if len(f.dyn.reads) != 0 {
		t.Errorf("the service read the value of %v; nothing is served from a container it could not classify", f.dyn.reads)
	}
	assertNoValue(t, f.svc, f.workflowReads, body)
}

// TestWorkflowSecretsV2RefusesEachOtherClass is G5 and F21: D3333 makes the
// repository the unit of access, so every caller class that is not a
// per-repository coding job is refused on version 2 exactly as it is on
// version 1 — a review job, a shared project job, a build identity, the
// installation's coordinator, a launcher and a person.
//
// Each is refused before the fence is reached, which is why none of them reads
// the project record, a container or a marker: the class is the route's first
// gate and nothing below it runs.
func TestWorkflowSecretsV2RefusesEachOtherClass(t *testing.T) {
	// The launcher and the person are refused before the identity is verified,
	// so they write NO line: a line states the identity the service verified.
	t.Run("a launcher", func(t *testing.T) {
		f := newWorkflowSvc(t)
		resp, body := f.callPerson(t, WorkflowSecretsPath, workflowBodyV2(testSharedName), f.launcherToken())
		assertRefusedV2(t, "a launcher", resp, body, f.svc, f.workflowReads, StatusUnverified, 0, ErrorUnverifiedRunIdentity)
	})
	t.Run("a person", func(t *testing.T) {
		f := newWorkflowSvc(t)
		resp, body := f.callPerson(t, WorkflowSecretsPath, workflowBodyV2(testSharedName), f.personToken(testPersonAccount))
		assertRefusedV2(t, "a person", resp, body, f.svc, f.workflowReads, StatusUnverified, 0, ErrorUnverifiedRunIdentity)
	})
	t.Run("a shared project job", func(t *testing.T) {
		reads := newWorkflowReads(t)
		shared := newSharedCodingSvc(t, reads.wire)
		resp, body := fetchWorkflowSecretsBody(t, shared, workflowBodyV2(testSharedName), shared.sharedRunToken(), shared.sharedJobToken(), shared.job)
		assertRefusedV2(t, "a shared project job", resp, body, shared, reads, StatusUnverified, 1, ErrorUnverifiedRunIdentity)
	})
	t.Run("a review job", func(t *testing.T) {
		reads := newWorkflowReads(t)
		review := newReviewSvcSpelled(t, testReviewSA, testReviewSA, reads.wire)
		resp, body := fetchWorkflowSecretsBody(t, review, workflowBodyV2(testSharedName), review.reviewRunToken(), review.reviewJobToken(), review.job)
		assertRefusedV2(t, "a review job", resp, body, review, reads, StatusUnverified, 1, ErrorUnverifiedRunIdentity)
	})
	t.Run("a build identity", func(t *testing.T) {
		b := newBuildCredsSvc(t)
		mine := b.rows[testRepoPath]
		resp, body := fetchWorkflowSecretsBody(t, b.svc, workflowBodyV2(testSharedName), b.runToken(), b.token(mine), b.job)
		assertRefusedV2(t, "a repository's build", resp, body, b.svc, b.reads, StatusUnverified, 0, ErrorUnverifiedJobIdentity)
	})
	t.Run("the coordinator", func(t *testing.T) {
		s, _ := coordinatorSvc(t, testCoordinatorAccount)
		reads := newWorkflowReads(t)
		resp, body := fetchWorkflowSecretsBody(t, s, workflowBodyV2(testSharedName), s.runToken(), s.personToken(testCoordinatorAccount), s.job)
		assertRefusedV2(t, "the coordinator", resp, body, s, reads, StatusUnverified, 0, ErrorUnverifiedJobIdentity)
	})
}

// TestWorkflowSecretsV2RefusedWhileRepoSecretsOn is G9 and F24: version 2 is
// refused while `config.RepoSecretsOff` is false, because a repository's own
// copies are still in force and a project copy would shadow them. The refusal is
// the installation's state and not the caller's call — unavailable, and no
// record, container or marker is read for it.
//
// The door this test opens is the one every installation has SHUT until the
// flip: the whole version-2 suite beside this one runs on the default (door
// shut, version 2 served), so inverting the gate fails there and fails here.
func TestWorkflowSecretsV2RefusedWhileRepoSecretsOn(t *testing.T) {
	f := newWorkflowSvc(t)
	openTheDoor(t)
	if config.RepoSecretsOff {
		t.Fatalf("config.RepoSecretsOff is true after opening the door: this test cannot see the state it is about")
	}
	resp, body := f.fetchV2(t, testSharedName)
	assertRefusedV2(t, "version 2 while repository secrets are in force", resp, body, f.svc, f.workflowReads, StatusUnavailable, 1, ErrorWorkflowSecretsOff)
	if f.projects.reads != 0 {
		t.Errorf("the project record was read %d times before the flip; the door is the first thing version 2 reads", f.projects.reads)
	}
}

// TestWorkflowSecretsV2RefusesAPlatformCredentialName is the other half of
// secretname.Project (R7): a name the grammar refuses is a refused REQUEST and
// not a lookup, so no container is built from it and no record is read. The
// model credential names are deliberately NOT refused (F29) — they are what R6
// rotates through the intake — so the name here is a platform credential kind's,
// which the grammar refuses so that a project secret can never shadow it.
func TestWorkflowSecretsV2RefusesAPlatformCredentialName(t *testing.T) {
	f := newWorkflowSvc(t)
	resp, body := f.fetchV2(t, config.BitbucketSecret)
	assertRefusedV2(t, "a platform credential's name", resp, body, f.svc, f.workflowReads, StatusRequestRefused, 1, ErrorRequestRefused)
	if f.projects.reads != 0 {
		t.Errorf("the project record was read %d times for a name the grammar refuses", f.projects.reads)
	}
}

// TestWorkflowSecretsAuditNamesOneContainer is G18's shape read over the
// version-2 route: every call of it writes exactly one line, the line names the
// ONE container the value came from (or was to come from), and no line and no
// refusal carries a value.
//
// The refusals are the four the fence can answer with, and the served call is
// the one that hands a value over — the case where a second line, or a line
// naming something else, would matter most.
func TestWorkflowSecretsAuditNamesOneContainer(t *testing.T) {
	calls := []struct {
		name string
		call func(t *testing.T, f *workflowFixture)
	}{
		{"the served call", func(t *testing.T, f *workflowFixture) {
			resp, body := f.fetchV2(t, testSharedName)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the call was refused: %s", body)
			}
		}},
		{"a repository the record does not admit", func(t *testing.T, f *workflowFixture) {
			f.projects.rec = projectRecord(testOtherRepoPath)
			_, _ = f.fetchV2(t, testSharedName)
		}},
		{"a container with no marker", func(t *testing.T, f *workflowFixture) {
			delete(f.dyn.marked, testSharedContainer)
			_, _ = f.fetchV2(t, testSharedName)
		}},
		{"a record that could not be read", func(t *testing.T, f *workflowFixture) {
			f.projects.err = errors.New("the record could not be read")
			_, _ = f.fetchV2(t, testSharedName)
		}},
		{"a name the grammar refuses", func(t *testing.T, f *workflowFixture) {
			_, _ = f.fetchV2(t, config.BitbucketSecret)
		}},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkflowSvc(t)
			tc.call(t, f)
			line := f.onlyAuditLine(t)
			if got := auditField(line, "purpose"); got != PurposeWorkflowSecret {
				t.Errorf("the line's purpose = %q, want %q: every act of this route is this route's", got, PurposeWorkflowSecret)
			}
			// The line names ONE container and nothing else of the record: a
			// line that named two would be an act this service did not perform,
			// and one that named the name rather than the container would not
			// say where a value came from.
			if got := auditField(line, "credential"); got != "" && got != testSharedContainer {
				t.Errorf("the line's credential = %q, want the container %q or none", got, testSharedContainer)
			}
			if got := auditField(line, "tier"); got != "" {
				t.Errorf("the line's tier = %q, want none: a workflow secret was selected at no tier", got)
			}
			assertNoValue(t, f.svc, f.workflowReads, nil)
		})
	}
}

// ---------------------------------------------------------------------------
// The repository's own fugaro.yaml (UO7, D3459)
// ---------------------------------------------------------------------------

// TestWorkflowSecretsFromDefaultBranch is UO7's red step: before it the route
// read only the record `fugaro update` wrote, so the names a call was served were
// that record's — and D3459 makes the repository's own fugaro.yaml at its default
// branch the authority, with the record a fallback for a code host that cannot be
// reached and nothing else.
//
// The fixture's record and the file name DIFFERENT containers for the same
// variable, so an answer is never ambiguous about which of the two it came from:
// the record names the repository's own copy and the file names the project's
// shared one, and what a call must be served is the file's.
func TestWorkflowSecretsFromDefaultBranch(t *testing.T) {
	f := newWorkflowSvc(t)
	f.files.unreachable = false
	f.files.yaml = map[string][]byte{testRepo: repoYAML(testWorkflow, testWorkflowEnv, testSharedName)}

	resp, body := f.fetchApp(t, testWorkflowEnv, testAppID)
	ans := served(t, "a job of a repository whose own file declares the variable", resp, body)
	if ans.Env != testWorkflowEnv {
		t.Errorf("the answer echoed %q, want the variable the call named", ans.Env)
	}
	if ans.Value != f.sharedValue {
		t.Errorf("the call was served the record's value; the repository's own file is the authority (D3459)")
	}
	if len(f.records.reads) != 0 {
		t.Errorf("the record was read (%v); a call whose file can be read is not served from the record", f.records.reads)
	}
	if len(f.files.reads) != 1 {
		t.Fatalf("the code host was read %d times, want one", len(f.files.reads))
	}
	q := f.files.reads[0]
	if q.Provider != gitprov.KindGitHub || q.Repo != testRepo || q.Path != fugaroYAMLPath || q.Ref != "" {
		t.Errorf("the read named %+v, want the calling repository's own %s at its default branch", q, fugaroYAMLPath)
	}
	if q.AppID != testAppID {
		t.Errorf("the read named the App ID %q, want the call's own %q: a GitHub read is signed as the App", q.AppID, testAppID)
	}
	if !slices.Equal(f.dyn.reads, []string{testSharedContainer}) {
		t.Errorf("the containers read were %v, want the file's own %q", f.dyn.reads, testSharedContainer)
	}
	if !slices.Equal(f.dyn.markerReads, []string{testSharedContainer}) {
		t.Errorf("the markers read were %v, want the file's own container: a name the file declares is fenced like any other (D3333)", f.dyn.markerReads)
	}
	if line := f.onlyAuditLine(t); auditField(line, "credential") != testSharedContainer {
		t.Errorf("the audit line names %q, want the container the value was read from", auditField(line, "credential"))
	}
}

// TestWorkflowSecretsIgnoreRunBranch is D3459's other half: the file is read at
// the repository's DEFAULT branch and never at the branch the run itself pushes
// (Identity.Branch, the fb claim).
//
// The fake answers a read at any other ref with a file that declares the same
// variable under a name the owner's record does not admit, so a service that let
// the run's branch reach the read would refuse this call instead of serving it —
// which is what makes this a test of the ref rather than of a comment.
func TestWorkflowSecretsIgnoreRunBranch(t *testing.T) {
	f := newWorkflowSvc(t)
	f.files.unreachable = false
	f.files.yaml = map[string][]byte{testRepo: repoYAML(testWorkflow, testWorkflowEnv, testSharedName)}
	f.files.refYAML = repoYAML(testWorkflow, testWorkflowEnv, testOtherName)

	// The run identity carries the branch this run may push, which is a branch
	// of the repository and not its default one (G6).
	tok := f.runToken(func(c map[string]any) { c["fb"] = "fugaro/" + f.run })
	resp, body := fetchWorkflowSecretsBody(t, f.svc, workflowBodyApp(testWorkflowEnv, testAppID), tok, f.jobToken(), f.job)
	ans := served(t, "a run whose identity names the branch it pushes", resp, body)
	if ans.Value != f.sharedValue {
		t.Errorf("the call was served the value of another ref; a workflow secret is read at the repository's default branch")
	}
	if len(f.files.reads) != 1 {
		t.Fatalf("the code host was read %d times, want one", len(f.files.reads))
	}
	if ref := f.files.reads[0].Ref; ref != "" {
		t.Errorf("the read named the ref %q; the empty ref IS the default branch, and the run's own branch is never read", ref)
	}
}

// TestWorkflowSecretsRepoFromRunToken is D3459's first clause: the repository the
// file is read for is the signed run token's, paired with the verified job
// account, and never text a call carries — the contract has no field for one.
//
// The call is made as the OTHER repository's job presenting the OTHER
// repository's run identity, and the file it must be read for is that one.
func TestWorkflowSecretsRepoFromRunToken(t *testing.T) {
	f := newWorkflowSvc(t)
	f.files.unreachable = false
	f.files.yaml = map[string][]byte{
		testRepo:      repoYAML(testWorkflow, testWorkflowEnv, testSharedName),
		testOtherRepo: repoYAML(testWorkflow, testWorkflowEnv, testSharedName),
	}
	// The project record admits the OTHER repository to the name and not the
	// fixture's own, so a service that read the file for, or fenced the name
	// against, anything but the calling run token's repository would refuse
	// this call instead of serving it.
	f.projects.rec = projectRecord(testOtherRepoPath)
	resp, body := fetchWorkflowSecretsBody(t, f.svc, workflowBodyApp(testWorkflowEnv, testAppID),
		f.runTokenOf(testSlug(t, testOtherRepoPath)), f.otherJobToken(), f.job)
	ans := served(t, "the other repository's job", resp, body)
	if ans.Value != f.sharedValue {
		t.Errorf("the call was served %q, want the file's own container", ans.Value)
	}
	if len(f.files.reads) != 1 {
		t.Fatalf("the code host was read %d times, want one", len(f.files.reads))
	}
	if got := f.files.reads[0].Repo; got != testOtherRepo {
		t.Errorf("the read named the repository %q, want the run token's own %q", got, testOtherRepo)
	}
	if len(f.records.reads) != 0 {
		t.Errorf("the record was read (%v); the repository's own file is the authority", f.records.reads)
	}
}

// TestWorkflowSecretsBoundedByAllowList is D3459's third clause: a name the
// repository's own file declares is still bounded by the project's `secrets
// allow` record and by the marker, exactly as a version-2 call is (F13, D3328,
// D3333).
//
// The file is the repository's own text and it is still not a grant: a file that
// declares a name the owner's record does not admit this repository to is refused
// with the command that fixes it, and a container with no marker is refused
// rather than read.
func TestWorkflowSecretsBoundedByAllowList(t *testing.T) {
	t.Run("a name the record admits another repository to", func(t *testing.T) {
		f := newWorkflowSvc(t)
		f.files.unreachable = false
		f.files.yaml = map[string][]byte{testRepo: repoYAML(testWorkflow, testWorkflowEnv, testSharedName)}
		f.projects.rec = projectRecord(testOtherRepoPath)
		resp, body := f.fetchApp(t, testWorkflowEnv, testAppID)
		assertRefused(t, "a file that declares a name the record does not admit this repository to",
			resp, body, f.svc, f.workflowReads, 1, ErrorWorkflowSecretNotAllowed)
		if len(f.files.reads) != 1 {
			t.Errorf("the code host was read %d times, want one", len(f.files.reads))
		}
	})
	t.Run("a container that carries no marker", func(t *testing.T) {
		f := newWorkflowSvc(t)
		f.files.unreachable = false
		f.files.yaml = map[string][]byte{testRepo: repoYAML(testWorkflow, testWorkflowEnv, testSharedName)}
		f.dyn.marked[testSharedContainer] = false
		resp, body := f.fetchApp(t, testWorkflowEnv, testAppID)
		assertRefused(t, "a file that declares a name whose container carries no marker",
			resp, body, f.svc, f.workflowReads, 1, ErrorWorkflowSecretUnmarked)
	})
	t.Run("a version-2 call names a secret the file does not declare", func(t *testing.T) {
		f := newWorkflowSvc(t)
		f.files.unreachable = false
		f.files.yaml = map[string][]byte{testRepo: repoYAML(testWorkflow, testWorkflowEnv, testOtherName)}
		resp, body := f.fetchV2App(t, testSharedName, testAppID)
		// The refusal is the one answer the runner falls back to version 1 on
		// (workflowsecrets.go's header), so a name the file does not declare is
		// refused with it rather than served.
		assertRefusedV2(t, "a version-2 call naming a secret the repository's own file does not declare",
			resp, body, f.svc, f.workflowReads, http.StatusForbidden, 1, ErrorWorkflowSecretNotAllowed)
	})
}

// TestRecordFallbackOnlyWhenUnreachable is D3459's fourth clause, in both
// directions: the record is read when the code host cannot be reached, and it is
// NOT read when the host answered — a file this release cannot read and a file
// that does not declare the variable are both refusals, because a record read
// beside a file that changed would serve a name the repository's own file no
// longer writes.
func TestRecordFallbackOnlyWhenUnreachable(t *testing.T) {
	t.Run("the host does not answer", func(t *testing.T) {
		f := newWorkflowSvc(t)
		resp, body := f.fetch(t, testWorkflowEnv)
		ans := served(t, "a job whose code host cannot be reached", resp, body)
		if ans.Value != f.value {
			t.Errorf("the call was served %q, want the record's own value: the record is the fallback", ans.Value)
		}
		if len(f.files.reads) != 1 {
			t.Errorf("the code host was read %d times, want one: the fallback is reached from a read that failed", len(f.files.reads))
		}
		if !slices.Equal(f.records.reads, []string{f.slug}) {
			t.Errorf("the records read were %v, want the calling repository's own", f.records.reads)
		}
	})
	t.Run("the host answers with a file this release cannot read", func(t *testing.T) {
		f := newWorkflowSvc(t)
		f.files.unreachable = false
		f.files.yaml = map[string][]byte{testRepo: []byte("workflows:\n  " + testWorkflow + ":\n    secrets: [")}
		resp, body := f.fetchApp(t, testWorkflowEnv, testAppID)
		assertRefused(t, "a job whose repository's own file cannot be read",
			resp, body, f.svc, f.workflowReads, 1, ErrorStateUnavailable)
		if len(f.records.reads) != 0 {
			t.Errorf("the record was read (%v) after a file that could not be read: a parse error is never the fallback (D3459)", f.records.reads)
		}
	})
	t.Run("the host answers with a file that does not declare the variable", func(t *testing.T) {
		f := newWorkflowSvc(t)
		f.files.unreachable = false
		f.files.yaml = map[string][]byte{testRepo: repoYAML(testWorkflow, testOtherWorkflowEnv, testSharedName)}
		resp, body := f.fetchApp(t, testWorkflowEnv, testAppID)
		assertRefused(t, "a job whose repository's own file declares no such variable",
			resp, body, f.svc, f.workflowReads, 1, ErrorRequestRefused)
		if len(f.records.reads) != 0 {
			t.Errorf("the record was read (%v) beside a file that answered: the record is the fallback for an unreachable host alone", f.records.reads)
		}
	})
}
