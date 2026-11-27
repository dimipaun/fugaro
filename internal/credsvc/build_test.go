package credsvc

// The build-credentials route's tests (B4.3, R5, D2076, D2077, G20).
//
// What this file is for, in one sentence: a repository's BUILD is a caller of
// this service and is served its OWN repository's credentials, and no field of
// its call can move either the repository or the provider. The tests below are
// the ways that could be false — another repository's row, another provider's
// credential, a run route reached with a build's token, a call that names a
// repository, a secret out of a workflow the record does not hold — and each of
// them is a refusal rather than a narrower answer.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
)

// buildCredsPath is the path this file's calls are made to. It is a literal
// here rather than the constant, so TestBuildCredentialsRoutePathIsTheServices
// Own is what says the two agree instead of one of them having moved with the
// other following it.
const buildCredsPath = "/v1/build/credentials"

// buildCredsBundleDoc is the installation's bundle with BOTH providers'
// credentials in it: the App key the GitHub mint signs with (mintBundleDoc) and
// the Bitbucket token a Bitbucket repository's build is answered (D2076 (a)).
func buildCredsBundleDoc(t *testing.T, token string) []byte {
	t.Helper()
	return append(mintBundleDoc(t), []byte(config.BitbucketSecret+": "+token+"\n")...)
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// buildCredsFixture is the installation under test: buildSvc's service — the
// build-identity class, the image map, the fake GitHub — with this route's own
// three reads stood in for (the record, the containers and the bundle that
// holds both providers' credentials) and the repository build class configured
// beside it.
type buildCredsFixture struct {
	*svc
	gh    *ghFake
	reads *workflowReads
	// rows is the class the route verifies against, by the repository the
	// caller bakes: one GitHub repository, a second GitHub repository and a
	// Bitbucket one, so "served only its own" is a comparison and not a
	// constant.
	rows map[string]RepoBuildAccount
	// bitbucketToken is the bundle's own Bitbucket token, which is the whole of
	// what a Bitbucket repository's build is answered.
	bitbucketToken string
}

// newBuildCredsSvc is the fixture. extra runs after everything else, so a test
// can put one thing wrong with an otherwise correct installation.
func newBuildCredsSvc(t *testing.T, extra ...func(*svc, *Config)) *buildCredsFixture {
	t.Helper()
	reads := newWorkflowReads(t)
	// The Bitbucket repository is recorded too, under a container of its own, so
	// that "a build is served its own repository's record" is a distinguishable
	// answer on both providers and not only on GitHub.
	const bitbucketContainer = "repo-a-implement-deploy"
	reads.dyn.values[bitbucketContainer] = reads.otherValue
	reads.records.recs[bitbucketSlug(t, testBitbucketRepo)] = creds.WorkflowSecrets{
		Version: creds.WorkflowSecretsVersion,
		Workflows: map[string][]creds.WorkflowSecret{
			testWorkflow: {{Env: testWorkflowEnv, ID: bitbucketContainer}},
		},
	}
	token := fakeCred(t, "bitbucket-token")
	reads.dyn.doc = buildCredsBundleDoc(t, token)

	rows := map[string]RepoBuildAccount{}
	for _, spec := range []struct{ provider, repo string }{
		{gitprov.KindGitHub, testRepoPath},
		{gitprov.KindGitHub, testOtherRepoPath},
		{gitprov.KindBitbucket, testBitbucketRepo},
	} {
		got, err := RepoBuildAccountsFor(testFirebase, spec.provider, []RepoJob{{Repo: spec.repo, Workflow: testWorkflow}})
		if err != nil {
			t.Fatalf("the fixture's own build rows for %q: %v", spec.repo, err)
		}
		if len(got) != 1 {
			t.Fatalf("the fixture's own build rows for %q = %d rows, want 1", spec.repo, len(got))
		}
		rows[spec.repo] = got[0]
	}

	f := &buildCredsFixture{reads: reads, rows: rows, bitbucketToken: token}
	f.svc, f.gh = buildSvc(t, func(s *svc, cfg *Config) {
		// The bundle the route reads, over the source that also answers
		// containers (perSecrets): the App key and the Bitbucket token in the
		// document, the workflow secrets' values beside it.
		cfg.Bundles = &BundleLoader{Source: reads.dyn, Now: func() time.Time { return s.now }}
		cfg.WorkflowSecrets = reads.records
		// The code host the repository's own fugaro.yaml is read through (UO7,
		// D3459). It answers "the host could not be reached" until a test says
		// otherwise, so every test here that is not about the file read is served
		// from the record, exactly as it was before this route read a file.
		cfg.RepoFiles = reads.files
		// The project record too: a build's own mounts can name a `fugaro_p_`
		// container, and F13 holds that route to the same fence the by-name
		// route is held to (IS1).
		cfg.ProjectSecrets = reads.projects
		// The class is rebuilt rather than added to, because a class is decided
		// from the verified email and the fixture's own verifier would otherwise
		// refuse every one of these three accounts as an unknown caller — the
		// test would then be reading the verifier's answer and not the route's.
		auth, err := NewAuthenticator(AuthConfig{
			Project:            testProject,
			FirebaseProject:    testFirebase,
			Audience:           testAudience,
			JobAccounts:        []string{testJobSA},
			Keys:               s.keys,
			JobKeys:            s.jobKeys,
			Outcomes:           RTDBOutcomes{DB: s.db},
			SharedJobAccounts:  []SharedJobAccount{sharedRow()},
			ReviewJobAccounts:  []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}},
			VerifierAccounts:   []string{testVerifierSA},
			PersonAccounts:     []string{testPersonAccount},
			CoordinatorAccount: testCoordinatorAccount,
			LauncherAccounts:   []string{testLauncherAccount},
			BuildAccounts:      []string{testBuildSA},
			RepoBuildAccounts: []RepoBuildAccount{
				rows[testRepoPath], rows[testOtherRepoPath], rows[testBitbucketRepo],
			},
			Now: func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's own build class is not one NewAuthenticator accepts: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		for _, m := range extra {
			// A table-driven test's row carries a nil mutation when it has
			// nothing to break, and a nil call here would be a panic rather than
			// a passing case.
			if m != nil {
				m(s, cfg)
			}
		}
	})
	return f
}

// shortName is the repository's name as GitHub's mint spells it: the
// `repositories` list of a token's body carries the NAME and not the canonical
// "owner/name" the service's own tables hold (github.MintInstallationToken
// takes the two apart), so a test that compared the two would be comparing two
// spellings of one repository and not two repositories.
func shortName(t *testing.T, canonical string) string {
	t.Helper()
	_, name, err := mintRepository(canonical)
	if err != nil {
		t.Fatalf("the fixture's own repository %q is not one a mint can read: %v", canonical, err)
	}
	return name
}

// token is the Google ID token one of the fixture's build accounts presents:
// the job token's own shape, naming that account and this service's audience.
func (f *buildCredsFixture) token(row RepoBuildAccount, mutate ...func(map[string]any)) string {
	return f.jobToken(append([]func(map[string]any){
		func(c map[string]any) { c["email"] = row.Email },
	}, mutate...)...)
}

// call sends one build-credentials call as the given account and reads the
// answer whole. It is the shape this route has and no other route of this
// service does: a token in Authorization, and nothing else — a build holds no
// run identity, so there is none to present.
func (f *buildCredsFixture) call(t *testing.T, row RepoBuildAccount, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return f.callPerson(t, buildCredsPath, body, f.token(row), mutate...)
}

// buildCredsBody is one call's body: the workflow whose secrets the build
// mounts, the variables it asks for, and the App ID a GitHub token is signed
// as. Nothing here names a repository, a slug or a provider, because the
// contract has no field for one (G20).
func buildCredsBody(workflow string, secrets []string, appID string) string {
	raw, err := json.Marshal(map[string]any{"workflow": workflow, "secrets": secrets, "app_id": appID})
	if err != nil {
		panic("credsvc test: encoding a build-credentials body: " + err.Error())
	}
	return string(raw)
}

// buildCredsAnswer is this route's answer as a TEST reads it, and deliberately
// its own struct rather than the service's: what the contract carries is the
// test's business, and a field renamed in the service alone would leave this
// file reading a body with no field of that name rather than agreeing with a
// rename.
type buildCredsAnswer struct {
	Git struct {
		Provider  string    `json:"provider"`
		Username  string    `json:"username"`
		Token     string    `json:"token"`
		Host      string    `json:"host"`
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"git"`
	Secrets []struct {
		Env   string `json:"env"`
		Value string `json:"value"`
	} `json:"secrets"`
}

// readBuildCredsAnswer reads a 200 answer, failing the test when the body is
// not one.
func readBuildCredsAnswer(t *testing.T, raw []byte) buildCredsAnswer {
	t.Helper()
	var ans buildCredsAnswer
	if err := json.Unmarshal(raw, &ans); err != nil {
		t.Fatalf("the answer is not this contract's: %v (%s)", err, raw)
	}
	return ans
}

// ---------------------------------------------------------------------------
// The tests
// ---------------------------------------------------------------------------

// TestBuildCallerServedOnlyItsOwnRepository is this task's red step. Before the
// route existed, POST /v1/build/credentials was a 404 and a repository's build
// had nowhere to ask. What it pins is R5 and G20: the row the caller's verified
// email lands on IS the repository, so two builds of two repositories of one
// installation are answered two different repositories' credentials, and the
// token that reaches GitHub names the caller's own repository and no other.
func TestBuildCallerServedOnlyItsOwnRepository(t *testing.T) {
	f := newBuildCredsSvc(t)
	mine, other := f.rows[testRepoPath], f.rows[testOtherRepoPath]
	if mine.Email == other.Email {
		t.Fatalf("the fixture's two build accounts are the same address (%s), so this test could not tell them apart", mine.Email)
	}

	body := buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID)

	resp, raw := f.call(t, mine, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a repository's own build was refused: %d %s", resp.StatusCode, raw)
	}
	ans := readBuildCredsAnswer(t, raw)
	if ans.Git.Provider != gitprov.KindGitHub || ans.Git.Token != f.gh.token {
		t.Errorf("the answer's git credential = %+v, want a GitHub token of the fixture's mint", ans.Git)
	}
	if len(ans.Secrets) != 1 || ans.Secrets[0].Value != f.reads.value {
		t.Errorf("the answer's secrets = %+v, want its own repository's one value", ans.Secrets)
	}
	// The repository the token was minted for is the CALLER's, read out of the
	// two requests a mint makes: the installation lookup names it and the mint's
	// body names it again.
	lookup, mint := f.gh.both(t)
	if want := "/repos/" + testRepoPath + "/installation"; lookup.path != want {
		t.Errorf("the installation lookup = %q, want %q", lookup.path, want)
	}
	if want := `"repositories":["` + shortName(t, testRepo) + `"]`; !strings.Contains(mint.body, want) {
		t.Errorf("the mint's body = %s, and it does not name the caller's own repository (%s)", mint.body, want)
	}
	if strings.Contains(mint.body, shortName(t, testOtherRepo)) {
		t.Errorf("the mint's body = %s, and it names a repository the caller does not bake", mint.body)
	}

	// The second repository's build, on the same installation, is answered ITS
	// repository — and the fake sees a second pair of calls naming the other
	// one.
	fresh, raw := f.call(t, other, body)
	if fresh.StatusCode != http.StatusOK {
		t.Fatalf("the second repository's build was refused: %d %s", fresh.StatusCode, raw)
	}
	seen := f.gh.requests()
	if len(seen) != 4 {
		t.Fatalf("the fake GitHub saw %d requests, want two mints: %v", len(seen), seen)
	}
	if want := "/repos/" + testOtherRepoPath + "/installation"; seen[2].path != want {
		t.Errorf("the second lookup = %q, want %q", seen[2].path, want)
	}
	if want := `"repositories":["` + shortName(t, testOtherRepo) + `"]`; !strings.Contains(seen[3].body, want) {
		t.Errorf("the second mint's body = %s, and it does not name that caller's own repository (%s)", seen[3].body, want)
	}
}

// TestBuildCallerNoFieldOfTheCallNamesARepository is the other half of G20, and
// it is a test of the CONTRACT rather than of a check: a call cannot ask for
// another repository's credentials because there is no field to ask in. A body
// carrying one is refused by the strict decoder (Convention 6) before the
// bundle, the record or GitHub is read at all.
func TestBuildCallerNoFieldOfTheCallNamesARepository(t *testing.T) {
	f := newBuildCredsSvc(t)
	mine := f.rows[testRepoPath]
	for _, field := range []string{"repository", "repo", "slug", "provider", "owner", "host"} {
		t.Run(field, func(t *testing.T) {
			body := `{"workflow":"` + testWorkflow + `","app_id":"` + testAppID + `","` + field + `":"` + testOtherRepoPath + `"}`
			resp, raw := f.call(t, mine, body)
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("a call naming %s was answered %d %s, want a refused request", field, resp.StatusCode, raw)
			}
			if e := refusal(t, resp.StatusCode, raw); e.Error != ErrorRequestRefused {
				t.Errorf("the refusal = %q, want %q", e.Error, ErrorRequestRefused)
			}
			if strings.Contains(string(raw), testOtherRepo) {
				t.Errorf("the refusal echoes the repository the call named: %s", raw)
			}
			if n := f.gh.count(); n != 0 {
				t.Errorf("a refused call reached GitHub %d times", n)
			}
			if len(f.reads.records.reads) != 0 {
				t.Errorf("a refused call read the record for %v", f.reads.records.reads)
			}
		})
	}
}

// TestBuildCallerGetsAReadOnlyGitHubTokenNeverTheKey is D2076's GitHub half and
// G18's: the build is answered a token this service minted, and the App key
// that signs it appears in no answer, no audit line and no log line of the
// service's own.
func TestBuildCallerGetsAReadOnlyGitHubTokenNeverTheKey(t *testing.T) {
	f := newBuildCredsSvc(t)
	mine := f.rows[testRepoPath]
	resp, raw := f.call(t, mine, buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the build was refused: %d %s", resp.StatusCode, raw)
	}
	ans := readBuildCredsAnswer(t, raw)
	if ans.Git.Token == "" {
		t.Fatal("the answer carries no token")
	}
	if ans.Git.Token != f.gh.token {
		t.Errorf("the token is not the one GitHub minted for this build")
	}

	// The App key is what signed it, and it is nowhere the build can read. The
	// check is a distinctive line of the PEM's own body, because a JSON body or
	// a JSON log line escapes the newlines and the whole PEM never appears in
	// either.
	core := pemCore(t)
	for what, text := range map[string]string{
		"the answer":      string(raw),
		"the audit lines": f.audit.String(),
		"the service log": f.logs.String(),
	} {
		if strings.Contains(text, core) {
			t.Errorf("the App key appears in %s", what)
		}
	}
	// The mint's own request carried a JWT signed with that key, and the fake
	// records the Authorization header of every call: what reached GitHub is a
	// JWT and not the key.
	_, mint := f.gh.both(t)
	if strings.Contains(mint.auth, core) {
		t.Errorf("the mint sent the App key itself to GitHub: %s", mint.auth)
	}
	if !strings.HasPrefix(mint.auth, "Bearer ") {
		t.Errorf("the mint's Authorization = %q, want a bearer JWT", mint.auth)
	}
}

// TestBuildCallerTokenIsContentsReadOnly is the SCOPE half of D2076, and it is
// checked on the request GitHub actually received rather than on the constant:
// what a build's token may do is decided by the permissions the mint's body
// asks for, and contents: read with metadata: read is a clone and nothing else.
//
// It also holds the set to the person read set the read route mints
// (githubread.go), which is the one a stolen build token would otherwise have:
// the build's is strictly narrower, and the difference is pull requests.
func TestBuildCallerTokenIsContentsReadOnly(t *testing.T) {
	f := newBuildCredsSvc(t)
	mine := f.rows[testRepoPath]
	resp, raw := f.call(t, mine, buildCredsBody(testWorkflow, nil, testAppID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the build was refused: %d %s", resp.StatusCode, raw)
	}
	_, mint := f.gh.both(t)

	var body struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(mint.body), &body); err != nil {
		t.Fatalf("the mint's body is not the JSON this test reads: %v (%s)", err, mint.body)
	}
	want := map[string]string{"contents": "read", "metadata": "read"}
	if len(body.Permissions) != len(want) {
		t.Fatalf("the mint asked for %v, want exactly %v", body.Permissions, want)
	}
	for k, v := range want {
		if body.Permissions[k] != v {
			t.Errorf("the mint asked for %s=%q, want %q", k, body.Permissions[k], v)
		}
	}
	// The token is scoped to the one repository, in the same body: a
	// permission set that were right and a repository list that were not would
	// be the same failure this route exists to prevent.
	if want := shortName(t, testRepo); len(body.Repositories) != 1 || body.Repositories[0] != want {
		t.Errorf("the mint's repositories = %v, want [%s]", body.Repositories, want)
	}
	// Nothing in the set writes, and the check is the spelling and not a list:
	// a permission added later with a write value fails here.
	for k, v := range body.Permissions {
		if v != "read" {
			t.Errorf("the mint asked for %s=%q, and a build's token reads and never writes", k, v)
		}
	}
	// And it is strictly narrower than the set the read route mints for a
	// person, which is the one an over-broad build token would be.
	person := github.PersonReadTokenPermissions()
	for k := range person {
		if _, ok := body.Permissions[k]; !ok && k != "pull_requests" {
			t.Errorf("the person read set holds %s and the build's does not, and only pull_requests is expected to differ", k)
		}
	}
	if _, ok := body.Permissions["pull_requests"]; ok {
		t.Error("the build's token asks for pull_requests, which a clone does not need and the person's set already covers")
	}
}

// TestBuildCallerRefusedOnRunRoutes is the class half of G20: a build's account
// is a caller of the build-credentials route alone, and its token reaches no
// run route. The routes are given bodies of their own shape, so a refusal that
// only said "the body is not one this contract reads" would be a test of the
// reader and not of the class.
func TestBuildCallerRefusedOnRunRoutes(t *testing.T) {
	f := newBuildCredsSvc(t)
	mine := f.rows[testRepoPath]
	digest, signature := testDigest(t, "build-creds"), strings.Repeat("0", 64)
	routes := []struct {
		name string
		path string
		body string
	}{
		{"messages", MessagesPath, callBody("a-repositorys-build")},
		{"count tokens", CountTokensPath, callBody("a-repositorys-build")},
		{"the mint", MintPath, mintBody(testAppID)},
		{"the sign route", SignPath, signBody(digest)},
		{"the verify route", VerifyPath, verifyBody(testReviewAccount, f.run, f.slug, digest, signature)},
		{"the Bitbucket read mint", BitbucketReadPath, `{"repository":"` + testBitbucketRepo + `"}`},
		{"the GitHub read mint", GitHubReadPath, githubReadBody(testRepoPath, testAppID)},
		{"the workflow secrets", WorkflowSecretsPath, workflowBody(testWorkflowEnv)},
	}
	for _, tc := range routes {
		t.Run(tc.name, func(t *testing.T) {
			fresh := newBuildCredsSvc(t)
			// A run identity and a job name ARE presented: without them the call
			// would be refused for the header it is missing, and the claim here
			// is that the ACCOUNT is refused.
			resp := fresh.postTokens(t, tc.path, tc.body, fresh.runToken(), fresh.token(mine), fresh.job)
			defer resp.Body.Close()
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading the answer: %v", err)
			}
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("a repository's build was admitted on %s: %s", tc.name, raw)
			}
			e := refusal(t, resp.StatusCode, raw)
			if e.Error != ErrorUnverifiedJobIdentity {
				t.Errorf("the refusal on %s = %q, want %q", tc.name, e.Error, ErrorUnverifiedJobIdentity)
			}
			// The class is refused before anything of this route is read, so no
			// credential was touched on the way.
			if n := fresh.gh.count(); n != 0 {
				t.Errorf("a refused call on %s reached GitHub %d times", tc.name, n)
			}
		})
	}
}

// TestRunCallerRefusedOnTheBuildRoute is the same rule read the other way: a
// job's token — a run's own caller — is not a build account, so the build route
// refuses it. It is a refusal with NO LINE: the caller was never verified, and
// a line would have to name a caller the service does not have.
func TestRunCallerRefusedOnTheBuildRoute(t *testing.T) {
	f := newBuildCredsSvc(t)
	resp, raw := f.callPerson(t, buildCredsPath, buildCredsBody(testWorkflow, nil, testAppID), f.jobToken())
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("a job's token was answered %d on the build route, want a refusal: %s", resp.StatusCode, raw)
	}
	if e := refusal(t, resp.StatusCode, raw); e.Error != ErrorUnverifiedJobIdentity {
		t.Errorf("the refusal = %q, want %q", e.Error, ErrorUnverifiedJobIdentity)
	}
	if n := len(f.auditLines(t)); n != 0 {
		t.Errorf("an unverified caller left %d audit lines, want none: %v", n, f.auditLines(t))
	}
	if n := f.gh.count(); n != 0 {
		t.Errorf("a refused call reached GitHub %d times", n)
	}
	if len(f.reads.records.reads) != 0 {
		t.Errorf("a refused call read the record for %v", f.reads.records.reads)
	}

	// A token of a class this installation DOES have — the project's build
	// identity, which is a caller of the build-token route — is refused here
	// too, and that is the distinction the two build classes exist for.
	other := newBuildCredsSvc(t)
	resp2, raw2 := other.callPerson(t, buildCredsPath, buildCredsBody(testWorkflow, nil, testAppID), other.buildToken())
	if resp2.StatusCode == http.StatusOK {
		t.Fatalf("the project's build identity was admitted on the repository build route: %s", raw2)
	}
	if e := refusal(t, resp2.StatusCode, raw2); e.Error != ErrorUnverifiedJobIdentity {
		t.Errorf("the project build identity's refusal = %q, want %q", e.Error, ErrorUnverifiedJobIdentity)
	}
}

// TestBuildAccountsAreTheNamesTheRepoRootDeploys is B4.3's own derivation
// checked against the deployment's: the rows this service verifies against are
// the accounts the repository root creates (gcp.BuildServiceAccountID over
// task.Slug), one per repository and not one per workflow, and a build account
// is not a job account — the two are different derivations of different
// accounts, and a service that read one as the other would serve a build a
// run's credentials.
func TestBuildAccountsAreTheNamesTheRepoRootDeploys(t *testing.T) {
	// Two workflows of one repository, which is what a deployment holds and
	// what makes "one row per repository" a claim: the build account is per
	// repository and carries no workflow.
	jobs := []RepoJob{
		{Repo: testRepoPath, Workflow: testWorkflow},
		{Repo: testRepoPath, Workflow: testOtherWorkflow},
		{Repo: testOtherRepoPath, Workflow: testWorkflow},
	}
	got, err := RepoBuildAccountsFor(testFirebase, gitprov.KindGitHub, jobs)
	if err != nil {
		t.Fatalf("RepoBuildAccountsFor: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("the rows = %d, want one per repository and not one per job: %+v", len(got), got)
	}
	want := []RepoBuildAccount{
		{
			Email:    gcp.BuildServiceAccountID(testSlug(t, testRepoPath)) + "@" + testFirebase + ".iam.gserviceaccount.com",
			Slug:     testSlug(t, testRepoPath),
			Repo:     testRepo,
			Provider: gitprov.KindGitHub,
		},
		{
			Email:    gcp.BuildServiceAccountID(testSlug(t, testOtherRepoPath)) + "@" + testFirebase + ".iam.gserviceaccount.com",
			Slug:     testSlug(t, testOtherRepoPath),
			Repo:     testOtherRepo,
			Provider: gitprov.KindGitHub,
		},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// A build account is NOT the job account of the same repository: the two
	// derivations differ, and that is what keeps a build out of every run route.
	jobRows, err := JobAccountsFor("fugaro", testFirebase, gitprov.KindGitHub, jobs)
	if err != nil {
		t.Fatalf("JobAccountsFor: %v", err)
	}
	for _, j := range jobRows {
		for _, b := range got {
			if j.Email == b.Email {
				t.Errorf("the build account of %s is the job account %s, and the two classes would then be one", b.Slug, j.Email)
			}
		}
	}

	// A Bitbucket repository's row is its own derivation and carries the
	// provider, which is what decides which credential the route issues from.
	bb, err := RepoBuildAccountsFor(testFirebase, gitprov.KindBitbucket, []RepoJob{{Repo: testBitbucketRepo, Workflow: testWorkflow}})
	if err != nil {
		t.Fatalf("RepoBuildAccountsFor (bitbucket): %v", err)
	}
	if len(bb) != 1 || bb[0].Provider != gitprov.KindBitbucket || bb[0].Slug != bitbucketSlug(t, testBitbucketRepo) {
		t.Errorf("the Bitbucket row = %+v, want one row bound to %s at %s", bb, bitbucketSlug(t, testBitbucketRepo), gitprov.KindBitbucket)
	}

	// And the class refuses the configurations that would make the binding
	// ambiguous: the same address bound to two repositories, an address that is
	// also a job account's, and a provider this service does not mint at.
	t.Run("the same account on two repositories", func(t *testing.T) {
		dup := []RepoBuildAccount{got[0], {Email: got[0].Email, Slug: got[1].Slug, Repo: got[1].Repo, Provider: got[1].Provider}}
		if _, err := NewAuthenticator(AuthConfig{
			Project: testProject, FirebaseProject: testFirebase, Audience: testAudience,
			RepoBuildAccounts: dup, Now: testNow,
		}); err == nil {
			t.Error("NewAuthenticator accepted one build account bound to two repositories")
		}
	})
	t.Run("a build account that is also a job account", func(t *testing.T) {
		if _, err := NewAuthenticator(AuthConfig{
			Project: testProject, FirebaseProject: testFirebase, Audience: testAudience,
			JobAccounts: []string{got[0].Email}, RepoBuildAccounts: []RepoBuildAccount{got[0]}, Now: testNow,
		}); err == nil {
			t.Error("NewAuthenticator accepted one account in two classes")
		}
	})
	t.Run("a provider this service does not mint at", func(t *testing.T) {
		bad := got[0]
		bad.Provider = "gitlab"
		if _, err := NewAuthenticator(AuthConfig{
			Project: testProject, FirebaseProject: testFirebase, Audience: testAudience,
			RepoBuildAccounts: []RepoBuildAccount{bad}, Now: testNow,
		}); err == nil {
			t.Error("NewAuthenticator accepted a provider this service does not mint at")
		}
	})
	t.Run("an account bound to no repository", func(t *testing.T) {
		bad := got[0]
		bad.Slug, bad.Repo = "", ""
		if _, err := NewAuthenticator(AuthConfig{
			Project: testProject, FirebaseProject: testFirebase, Audience: testAudience,
			RepoBuildAccounts: []RepoBuildAccount{bad}, Now: testNow,
		}); err == nil {
			t.Error("NewAuthenticator accepted a build account bound to no repository")
		}
	})
}

// TestBuildCallerOneAuditLinePerRefusal is C1.13's rule on this route: once the
// caller is verified, EVERY act writes exactly one line — a refusal and an
// answer alike — and a caller that was never verified writes none, because
// there is no caller for a line to name.
//
// The line is keyed the way every other line of this log is: the calling
// account as its caller, the CALLER's repository as its repository, the
// provider's git credential as its credential, and no run and no job — a build
// holds neither.
func TestBuildCallerOneAuditLinePerRefusal(t *testing.T) {
	mine := func(t *testing.T) (*buildCredsFixture, RepoBuildAccount) {
		t.Helper()
		f := newBuildCredsSvc(t)
		return f, f.rows[testRepoPath]
	}

	t.Run("an answered call", func(t *testing.T) {
		f, row := mine(t)
		resp, raw := f.call(t, row, buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the build was refused: %d %s", resp.StatusCode, raw)
		}
		line := f.onlyAuditLine(t)
		if line["caller"] != row.Email || line["repository"] != row.Slug {
			t.Errorf("the line names caller=%v repository=%v, want %s/%s", line["caller"], line["repository"], row.Email, row.Slug)
		}
		if line["purpose"] != PurposeBuild || line["credential"] != config.GitHubAppKeySecret || line["status"] != StatusOK {
			t.Errorf("the line = %v, want purpose=%s credential=%s status=%s", line, PurposeBuild, config.GitHubAppKeySecret, StatusOK)
		}
		if line["run"] != "" || line["job"] != "" {
			t.Errorf("the line names a run or a job (%v), and a build holds neither", line)
		}
	})

	refusals := []struct {
		name string
		// body is the call's text; a mutation of the fixture is optional.
		body   func(t *testing.T, row RepoBuildAccount) string
		mutate func(s *svc, cfg *Config)
		header func(r *http.Request)
		status int
		typ    string
	}{
		{
			name:   "a run identity",
			body:   func(_ *testing.T, _ RepoBuildAccount) string { return buildCredsBody(testWorkflow, nil, testAppID) },
			header: func(r *http.Request) { r.Header.Set(HeaderRunIdentity, "a-run-identity") },
			status: StatusUnverified, typ: ErrorUnverifiedRunIdentity,
		},
		{
			name:   "a job name",
			body:   func(_ *testing.T, _ RepoBuildAccount) string { return buildCredsBody(testWorkflow, nil, testAppID) },
			header: func(r *http.Request) { r.Header.Set(HeaderRunJob, "a-job") },
			status: StatusUnverified, typ: ErrorUnverifiedRunIdentity,
		},
		{
			name:   "a body this contract does not read",
			body:   func(_ *testing.T, _ RepoBuildAccount) string { return `{"workflow":` },
			status: StatusRequestRefused, typ: ErrorRequestRefused,
		},
		{
			name: "no App ID on a GitHub repository",
			body: func(_ *testing.T, _ RepoBuildAccount) string {
				return buildCredsBody(testWorkflow, nil, "")
			},
			status: StatusRequestRefused, typ: ErrorRequestRefused,
		},
		{
			name: "a variable the record does not hold",
			body: func(_ *testing.T, _ RepoBuildAccount) string {
				return buildCredsBody(testWorkflow, []string{testOtherWorkflowEnv}, testAppID)
			},
			status: StatusRequestRefused, typ: ErrorRequestRefused,
		},
		{
			name: "a workflow the record does not hold",
			body: func(_ *testing.T, _ RepoBuildAccount) string {
				return buildCredsBody("nightly", []string{testWorkflowEnv}, testAppID)
			},
			status: StatusUnavailable, typ: ErrorStateUnavailable,
		},
		{
			name:   "the operator's switch",
			body:   func(_ *testing.T, _ RepoBuildAccount) string { return buildCredsBody(testWorkflow, nil, testAppID) },
			mutate: func(s *svc, _ *Config) { s.kills.global = true },
			status: StatusUnavailable, typ: ErrorStateUnavailable,
		},
		{
			name: "a bundle that cannot be read",
			body: func(_ *testing.T, _ RepoBuildAccount) string { return buildCredsBody(testWorkflow, nil, testAppID) },
			mutate: func(_ *svc, cfg *Config) {
				cfg.Bundles = &BundleLoader{Source: &scriptedSource{errs: []error{ErrBundleAbsent}}}
			},
			status: StatusUnavailable, typ: ErrorStateUnavailable,
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			f := newBuildCredsSvc(t, tc.mutate)
			row := f.rows[testRepoPath]
			var mutate []func(*http.Request)
			if tc.header != nil {
				mutate = append(mutate, tc.header)
			}
			resp, raw := f.call(t, row, tc.body(t, row), mutate...)
			if resp.StatusCode != tc.status {
				t.Fatalf("the call was answered %d %s, want %d", resp.StatusCode, raw, tc.status)
			}
			if e := refusal(t, resp.StatusCode, raw); e.Error != tc.typ {
				t.Errorf("the refusal = %q, want %q", e.Error, tc.typ)
			}
			line := f.onlyAuditLine(t)
			if line["caller"] != row.Email || line["repository"] != row.Slug {
				t.Errorf("the line names caller=%v repository=%v, want %s/%s", line["caller"], line["repository"], row.Email, row.Slug)
			}
			if line["purpose"] != PurposeBuild || line["credential"] != config.GitHubAppKeySecret || line["status"] != tc.typ {
				t.Errorf("the line = %v, want purpose=%s credential=%s status=%s", line, PurposeBuild, config.GitHubAppKeySecret, tc.typ)
			}
			// No value of a credential anywhere the caller or the operator can
			// read it (G18): neither the App key nor a secret's value.
			core := pemCore(t)
			for _, text := range []string{string(raw), f.audit.String(), f.logs.String()} {
				if strings.Contains(text, core) || strings.Contains(text, f.reads.value) {
					t.Errorf("a refusal carries a credential value: %s", text)
				}
			}
		})
	}

	t.Run("a caller that was never verified", func(t *testing.T) {
		f, _ := mine(t)
		resp, raw := f.callPerson(t, buildCredsPath, buildCredsBody(testWorkflow, nil, testAppID), f.jobToken())
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("a job's token was admitted: %s", raw)
		}
		if n := len(f.auditLines(t)); n != 0 {
			t.Errorf("an unverified caller left %d audit lines, want none: %v", n, f.auditLines(t))
		}
	})

	t.Run("a token this service cannot verify", func(t *testing.T) {
		f, _ := mine(t)
		resp, raw := f.callPerson(t, buildCredsPath, buildCredsBody(testWorkflow, nil, testAppID), "not-a-token")
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("an unreadable token was admitted: %s", raw)
		}
		if n := len(f.auditLines(t)); n != 0 {
			t.Errorf("an unverified caller left %d audit lines, want none: %v", n, f.auditLines(t))
		}
	})
}

// TestBuildCallerServedItsOwnRepositorysWorkflowSecrets is D2077 (a): the
// secrets half of the answer comes out of B3.1's record for the CALLER's own
// repository, through B3.2's reader, and the workflow the call names is a
// SELECTOR within that record and never a widening.
func TestBuildCallerServedItsOwnRepositorysWorkflowSecrets(t *testing.T) {
	f := newBuildCredsSvc(t)
	mine := f.rows[testRepoPath]

	resp, raw := f.call(t, mine, buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the build was refused: %d %s", resp.StatusCode, raw)
	}
	ans := readBuildCredsAnswer(t, raw)
	if len(ans.Secrets) != 1 || ans.Secrets[0].Env != testWorkflowEnv || ans.Secrets[0].Value != f.reads.value {
		t.Errorf("the answer's secrets = %+v, want %s served its own repository's value", ans.Secrets, testWorkflowEnv)
	}
	if got := f.reads.records.reads; len(got) != 1 || got[0] != mine.Slug {
		t.Errorf("the record was read for %v, want the caller's own slug %s", got, mine.Slug)
	}

	// The workflow the call names decides WHICH variables are served: the same
	// repository's other workflow holds another one, and asking for it under
	// the first workflow's name is a refusal and not a lookup elsewhere.
	resp2, raw2 := f.call(t, mine, buildCredsBody(testWorkflow, []string{testOtherWorkflowEnv}, testAppID))
	if resp2.StatusCode != StatusRequestRefused {
		t.Fatalf("a variable the workflow is not recorded under was answered %d %s", resp2.StatusCode, raw2)
	}
	if strings.Contains(string(raw2), f.reads.otherValue) {
		t.Errorf("the refusal carries a value: %s", raw2)
	}
	resp3, raw3 := f.call(t, mine, buildCredsBody(testOtherWorkflow, []string{testOtherWorkflowEnv}, testAppID))
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("the repository's own other workflow was refused: %d %s", resp3.StatusCode, raw3)
	}
	if ans := readBuildCredsAnswer(t, raw3); len(ans.Secrets) != 1 || ans.Secrets[0].Value != f.reads.otherValue {
		t.Errorf("the sibling workflow's secrets = %+v, want its own value", ans.Secrets)
	}

	// Two variables at once are served in the ORDER they were asked for, so a
	// client can map the answer onto its own request.
	f2 := newBuildCredsSvc(t)
	f2.reads.records.recs[mine.Slug] = creds.WorkflowSecrets{
		Version: creds.WorkflowSecretsVersion,
		Workflows: map[string][]creds.WorkflowSecret{
			testWorkflow: {{Env: testWorkflowEnv, ID: testWorkflowContainer}, {Env: testOtherWorkflowEnv, ID: testOtherWorkflowContainer}},
		},
	}
	resp4, raw4 := f2.call(t, mine, buildCredsBody(testWorkflow, []string{testOtherWorkflowEnv, testWorkflowEnv}, testAppID))
	if resp4.StatusCode != http.StatusOK {
		t.Fatalf("two variables were refused: %d %s", resp4.StatusCode, raw4)
	}
	ans4 := readBuildCredsAnswer(t, raw4)
	if len(ans4.Secrets) != 2 || ans4.Secrets[0].Env != testOtherWorkflowEnv || ans4.Secrets[1].Env != testWorkflowEnv {
		t.Errorf("the answer's secrets = %+v, want the two variables in the order the call asked for them", ans4.Secrets)
	}
	if ans4.Secrets[0].Value != f2.reads.otherValue || ans4.Secrets[1].Value != f2.reads.value {
		t.Errorf("the answer's secrets = %+v, want each variable's own value", ans4.Secrets)
	}
}

// TestBuildCallerServedTheBundlesBitbucketToken is D2076 (a): a Bitbucket
// repository's build is answered the BUNDLE's own token, whose reach is
// unchanged — this route is not the place that narrows a Bitbucket credential —
// with the service's own username and host, so no field of the call can point
// the build at another destination.
func TestBuildCallerServedTheBundlesBitbucketToken(t *testing.T) {
	f := newBuildCredsSvc(t)
	row := f.rows[testBitbucketRepo]
	if row.Provider != gitprov.KindBitbucket {
		t.Fatalf("the fixture's Bitbucket row is %+v", row)
	}

	// A Bitbucket call names no App ID, and the workflow secrets come from that
	// repository's own record.
	body := buildCredsBody(testWorkflow, []string{testWorkflowEnv}, "")
	resp, raw := f.call(t, row, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a Bitbucket repository's build was refused: %d %s", resp.StatusCode, raw)
	}
	ans := readBuildCredsAnswer(t, raw)
	if ans.Git.Provider != gitprov.KindBitbucket {
		t.Errorf("the git credential's provider = %q, want %q", ans.Git.Provider, gitprov.KindBitbucket)
	}
	if ans.Git.Token != f.bitbucketToken {
		t.Errorf("the token is not the bundle's own Bitbucket token")
	}
	if ans.Git.Username != bitbucket.GitUsername {
		t.Errorf("the username = %q, want the service's own %q", ans.Git.Username, bitbucket.GitUsername)
	}
	if ans.Git.Host != "bitbucket.org" {
		t.Errorf("the host = %q, want bitbucket.org", ans.Git.Host)
	}
	if !ans.Git.ExpiresAt.IsZero() {
		t.Errorf("the answer names an expiry (%s), and the bundle's token is not this service's to date", ans.Git.ExpiresAt)
	}
	if len(ans.Secrets) != 1 || ans.Secrets[0].Value != f.reads.otherValue {
		t.Errorf("the answer's secrets = %+v, want that repository's own record's value", ans.Secrets)
	}
	if n := f.gh.count(); n != 0 {
		t.Errorf("a Bitbucket build reached GitHub %d times, and it is answered the bundle's token", n)
	}
	// The line names the BITBUCKET credential and not the App key: which
	// credential the act issued from is the row's provider and never a guess.
	line := f.onlyAuditLine(t)
	if line["credential"] != config.BitbucketSecret || line["purpose"] != PurposeBuild || line["repository"] != row.Slug {
		t.Errorf("the line = %v, want credential=%s purpose=%s repository=%s", line, config.BitbucketSecret, PurposeBuild, row.Slug)
	}

	// An App ID on a Bitbucket repository is a call of a shape this row does not
	// have: the caller can fix it, so it is a refused request and not a
	// configuration fault.
	fresh := newBuildCredsSvc(t)
	resp2, raw2 := fresh.call(t, fresh.rows[testBitbucketRepo], buildCredsBody(testWorkflow, nil, testAppID))
	if resp2.StatusCode != StatusRequestRefused {
		t.Fatalf("a Bitbucket build carrying an App ID was answered %d %s", resp2.StatusCode, raw2)
	}
	if e := refusal(t, resp2.StatusCode, raw2); e.Error != ErrorRequestRefused {
		t.Errorf("the refusal = %q, want %q", e.Error, ErrorRequestRefused)
	}
}

// TestBuildCallerRefusedWhenNothingCanBeServed is the fail-closed half of D143
// and G19 on this route: a bundle with no App key, a bundle with no Bitbucket
// token, a record that is absent, a record this release does not know the
// version of, a container that is not there and a container reader that failed
// are each a TYPED REFUSAL, and none of them is an empty answer or a fallback
// to a credential the caller holds.
func TestBuildCallerRefusedWhenNothingCanBeServed(t *testing.T) {
	t.Run("a bundle with no App key", func(t *testing.T) {
		f := newBuildCredsSvc(t)
		f.reads.dyn.doc = append(testBundleDoc(t), []byte(config.BitbucketSecret+": "+fakeCred(t, "bitbucket-token")+"\n")...)
		resp, raw := f.call(t, f.rows[testRepoPath], buildCredsBody(testWorkflow, nil, testAppID))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a bundle with no App key was answered %d %s", resp.StatusCode, raw)
		}
		if e := refusal(t, resp.StatusCode, raw); e.Error != ErrorStateUnavailable {
			t.Errorf("the refusal = %q, want %q", e.Error, ErrorStateUnavailable)
		}
		if n := f.gh.count(); n != 0 {
			t.Errorf("a call with no key reached GitHub %d times", n)
		}
		f.onlyAuditLine(t)
	})

	t.Run("a bundle with no Bitbucket token", func(t *testing.T) {
		f := newBuildCredsSvc(t)
		f.reads.dyn.doc = mintBundleDoc(t)
		resp, raw := f.call(t, f.rows[testBitbucketRepo], buildCredsBody(testWorkflow, nil, ""))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a bundle with no Bitbucket token was answered %d %s", resp.StatusCode, raw)
		}
		if e := refusal(t, resp.StatusCode, raw); e.Error != ErrorStateUnavailable {
			t.Errorf("the refusal = %q, want %q", e.Error, ErrorStateUnavailable)
		}
		f.onlyAuditLine(t)
	})

	t.Run("a record that is absent", func(t *testing.T) {
		f := newBuildCredsSvc(t)
		delete(f.reads.records.recs, f.rows[testRepoPath].Slug)
		resp, raw := f.call(t, f.rows[testRepoPath], buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("an unrecorded repository was answered %d %s", resp.StatusCode, raw)
		}
		f.onlyAuditLine(t)
	})

	// A record of a version this release does not know is refused by the READER
	// (creds.ReadWorkflowSecrets, the one thing that reads the node), so this
	// route meets it as a failed read and refuses it the same way: the two are
	// one state to a build — nothing was read — and neither is a fallback to an
	// empty answer (G19).
	t.Run("a record of a version this release does not know", func(t *testing.T) {
		f := newBuildCredsSvc(t)
		f.reads.records.err = fmt.Errorf("creds: reading %s: %w (version %d)",
			creds.PathWorkflowSecrets(f.rows[testRepoPath].Slug), creds.ErrWorkflowSecretsVersion, creds.WorkflowSecretsVersion+1)
		resp, raw := f.call(t, f.rows[testRepoPath], buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a record of an unknown version was answered %d %s, want %d", resp.StatusCode, raw, StatusUnavailable)
		}
		if strings.Contains(string(raw), creds.ErrWorkflowSecretsVersion.Error()) {
			t.Errorf("the refusal repeats the reader's own sentence: %s", raw)
		}
		if strings.Contains(string(raw), f.reads.value) {
			t.Errorf("the refusal carries a value: %s", raw)
		}
		f.onlyAuditLine(t)
	})

	t.Run("a container that is not there", func(t *testing.T) {
		f := newBuildCredsSvc(t)
		delete(f.reads.dyn.values, testWorkflowContainer)
		resp, raw := f.call(t, f.rows[testRepoPath], buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a missing container was answered %d %s", resp.StatusCode, raw)
		}
		if e := refusal(t, resp.StatusCode, raw); e.Error != ErrorStateUnavailable {
			t.Errorf("the refusal = %q, want %q", e.Error, ErrorStateUnavailable)
		}
		f.onlyAuditLine(t)
	})

	t.Run("a container reader that failed", func(t *testing.T) {
		f := newBuildCredsSvc(t)
		f.reads.dyn.err = errBuildCredsFixtureRead
		resp, raw := f.call(t, f.rows[testRepoPath], buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a failed read was answered %d %s", resp.StatusCode, raw)
		}
		f.onlyAuditLine(t)
	})

	t.Run("no reader for the record at all", func(t *testing.T) {
		f := newBuildCredsSvc(t, func(_ *svc, cfg *Config) { cfg.WorkflowSecrets = nil })
		resp, raw := f.call(t, f.rows[testRepoPath], buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a service with no record reader was answered %d %s", resp.StatusCode, raw)
		}
		f.onlyAuditLine(t)
	})

	// A call that asks for NO secret is answered without the record being read
	// at all: the git credential is the whole of what it asked for, and a
	// service that read a record it was not asked about would be reading a
	// credential for nothing.
	t.Run("a call that asks for no secret", func(t *testing.T) {
		f := newBuildCredsSvc(t, func(_ *svc, cfg *Config) { cfg.WorkflowSecrets = nil })
		resp, raw := f.call(t, f.rows[testRepoPath], buildCredsBody(testWorkflow, nil, testAppID))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("a call asking for no secret was refused: %d %s", resp.StatusCode, raw)
		}
		if len(f.reads.records.reads) != 0 {
			t.Errorf("the record was read for %v, and the call asked for no secret", f.reads.records.reads)
		}
	})
}

// TestBuildCredentialsRoutePathIsTheServicesOwn holds the route's own constant
// to the path this file's calls are made to, and the method to the one the
// route has: the calls above are made to a literal, so this is what says the
// literal and the service agree rather than one of them having moved.
func TestBuildCredentialsRoutePathIsTheServicesOwn(t *testing.T) {
	if BuildCredentialsPath != buildCredsPath {
		t.Errorf("BuildCredentialsPath = %q, and this file's calls are made to %q", BuildCredentialsPath, buildCredsPath)
	}
	if !servedRoute(&http.Request{Method: http.MethodPost, URL: &url.URL{Path: buildCredsPath}}) {
		t.Errorf("the service does not serve POST %s", buildCredsPath)
	}
	if servedRoute(&http.Request{Method: http.MethodGet, URL: &url.URL{Path: buildCredsPath}}) {
		t.Errorf("the service serves GET %s, and the build-credentials route is a POST alone", buildCredsPath)
	}
	// The two build routes are two paths: a build's own credentials are asked
	// for at one and the project's build identity's token at the other, and a
	// service that served one at both would be answering a class it did not
	// verify.
	if BuildCredentialsPath == BuildTokenPath {
		t.Errorf("the two build routes share the path %q", BuildCredentialsPath)
	}
}

// errBuildCredsFixtureRead is what the fixture's container reader fails with,
// so that "the reader failed" is a state a test can set with no cloud and no
// transport.
var errBuildCredsFixtureRead = errors.New("the fixture's container reader failed")

// TestBuildCredentialsV2 is F13 (D3328, D3333, G2): a build's own mounts can
// name a `fugaro_p_<name>` container, and the route that reads it is held to the
// SAME fence the version-2 workflow-secrets route is held to — the owner's
// record must admit the caller's own repository to the name, and the container
// must carry the workflow-secret marker — so the two routes that can reach a
// project container cannot disagree about who may.
//
// The gate is NOT the one G9 names: `config.RepoSecretsOff` is a property of
// version 2 and is deliberately not read here, because this route's container is
// the one the repository's own record named and the operator's `update` controls.
// The first case is served with the door SHUT, which is what pins that.
func TestBuildCredentialsV2(t *testing.T) {
	if !config.RepoSecretsOff {
		t.Fatalf("config.RepoSecretsOff is false in this package, so this test cannot see the door it is about")
	}
	// The caller's own record puts its variable in the PROJECT container — the
	// shape a repository whose workflow mounts a shared secret has.
	projectRow := func(f *buildCredsFixture) {
		mine := f.rows[testRepoPath]
		f.reads.records.recs[mine.Slug] = creds.WorkflowSecrets{
			Version: creds.WorkflowSecretsVersion,
			Workflows: map[string][]creds.WorkflowSecret{
				testWorkflow: {{Env: testWorkflowEnv, ID: testSharedContainer}},
			},
		}
	}

	t.Run("a project container the record admits and the intake marked", func(t *testing.T) {
		f := newBuildCredsSvc(t)
		projectRow(f)
		mine := f.rows[testRepoPath]
		resp, raw := f.call(t, mine, buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("a build was refused its own repository's project secret: %d %s", resp.StatusCode, raw)
		}
		ans := readBuildCredsAnswer(t, raw)
		if len(ans.Secrets) != 1 || ans.Secrets[0].Env != testWorkflowEnv || ans.Secrets[0].Value != f.reads.sharedValue {
			t.Errorf("the answer's secrets = %+v, want the project container's value", ans.Secrets)
		}
		if f.reads.projects.reads != 1 {
			t.Errorf("the project record was read %d times, want once: the allow-list is the fence", f.reads.projects.reads)
		}
		if want := []string{testSharedContainer}; !slices.Equal(f.reads.dyn.markerReads, want) {
			t.Errorf("the markers read = %v, want %v", f.reads.dyn.markerReads, want)
		}
		f.onlyAuditLine(t)
		if strings.Contains(f.audit.String(), f.reads.sharedValue) {
			t.Errorf("an audit line carries the project secret's value")
		}
	})

	t.Run("a repository the record does not admit", func(t *testing.T) {
		f := newBuildCredsSvc(t)
		projectRow(f)
		f.reads.projects.rec = projectRecord(testOtherRepoPath)
		mine := f.rows[testRepoPath]
		resp, raw := f.call(t, mine, buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("a build of an unlisted repository was answered %d %s", resp.StatusCode, raw)
		}
		if e := refusal(t, resp.StatusCode, raw); e.Error != ErrorWorkflowSecretNotAllowed {
			t.Errorf("the refusal = %q, want %q", e.Error, ErrorWorkflowSecretNotAllowed)
		}
		if len(f.reads.dyn.reads) != 0 {
			t.Errorf("a refused build read the container's value (%v)", f.reads.dyn.reads)
		}
		if len(f.reads.dyn.markerReads) != 0 {
			t.Errorf("a refused build read the container's marker (%v)", f.reads.dyn.markerReads)
		}
		f.onlyAuditLine(t)
	})

	t.Run("a container with no marker", func(t *testing.T) {
		f := newBuildCredsSvc(t)
		projectRow(f)
		delete(f.reads.dyn.marked, testSharedContainer)
		mine := f.rows[testRepoPath]
		resp, raw := f.call(t, mine, buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a build was answered %d for an unmarked container %s", resp.StatusCode, raw)
		}
		if e := refusal(t, resp.StatusCode, raw); e.Error != ErrorWorkflowSecretUnmarked {
			t.Errorf("the refusal = %q, want %q", e.Error, ErrorWorkflowSecretUnmarked)
		}
		if len(f.reads.dyn.reads) != 0 {
			t.Errorf("the value of an unmarked container was read (%v); the marker is asked for first", f.reads.dyn.reads)
		}
		f.onlyAuditLine(t)
	})

	t.Run("a marker that could not be read", func(t *testing.T) {
		f := newBuildCredsSvc(t)
		projectRow(f)
		f.reads.dyn.markerErr = errors.New("the container's labels could not be listed")
		mine := f.rows[testRepoPath]
		resp, raw := f.call(t, mine, buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a build was answered %d for a marker that could not be read %s", resp.StatusCode, raw)
		}
		if e := refusal(t, resp.StatusCode, raw); e.Error != ErrorStateUnavailable {
			t.Errorf("the refusal = %q, want %q: an unreadable marker is the installation's state, not the container's shape", e.Error, ErrorStateUnavailable)
		}
		if len(f.reads.dyn.reads) != 0 {
			t.Errorf("the value of an unclassified container was read (%v)", f.reads.dyn.reads)
		}
		f.onlyAuditLine(t)
	})

	t.Run("a container outside the grammar is served as today", func(t *testing.T) {
		f := newBuildCredsSvc(t)
		mine := f.rows[testRepoPath]
		// The fixture's own record names `aurora-implement-deploy`, which is not
		// in the grammar: it is the project copy `update` derives under D2313, or
		// a repository's own copy, and neither is served through the fence.
		resp, raw := f.call(t, mine, buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("a build was refused its own record's container: %d %s", resp.StatusCode, raw)
		}
		if ans := readBuildCredsAnswer(t, raw); len(ans.Secrets) != 1 || ans.Secrets[0].Value != f.reads.value {
			t.Errorf("the answer's secrets = %+v, want the record's own container's value", ans.Secrets)
		}
		if f.reads.projects.reads != 0 {
			t.Errorf("the project record was read %d times for a container outside the grammar", f.reads.projects.reads)
		}
		if len(f.reads.dyn.markerReads) != 0 {
			t.Errorf("a marker was read (%v) for a container outside the grammar", f.reads.dyn.markerReads)
		}
	})
}

// TestBuildSecretsFromDefaultBranch is UO7's build half (D3459, D2076 (b)): a
// build's secrets are read out of the CALLER's repository's own fugaro.yaml at
// its default branch — the same seam the workflow-secrets route reads one
// through — and the record is the fallback for a code host that cannot be
// reached, exactly as it is there.
//
// The fixture's record and the file name different containers for the same
// variable, so the answer says which of the two it came from: the record names
// the repository's own copy and the file names the project's shared one.
func TestBuildSecretsFromDefaultBranch(t *testing.T) {
	f := newBuildCredsSvc(t)
	f.reads.files.unreachable = false
	f.reads.files.yaml = map[string][]byte{testRepo: repoYAML(testWorkflow, testWorkflowEnv, testSharedName)}

	resp, raw := f.call(t, f.rows[testRepoPath], buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the build was refused: %d %s", resp.StatusCode, raw)
	}
	if ans := readBuildCredsAnswer(t, raw); len(ans.Secrets) != 1 || ans.Secrets[0].Value != f.reads.sharedValue {
		t.Errorf("the answer's secrets = %+v, want the value the repository's own file names", ans.Secrets)
	}
	if len(f.reads.records.reads) != 0 {
		t.Errorf("the record was read (%v); the repository's own file is the authority (D3459)", f.reads.records.reads)
	}
	if len(f.reads.files.reads) != 1 {
		t.Fatalf("the code host was read %d times, want one", len(f.reads.files.reads))
	}
	if q := f.reads.files.reads[0]; q.Repo != testRepo || q.Ref != "" {
		t.Errorf("the read named %+v, want the caller's own repository at its default branch", q)
	}

	// The other direction: a host that cannot be reached is what the record is
	// the fallback for, and nothing else is.
	f2 := newBuildCredsSvc(t)
	resp2, raw2 := f2.call(t, f2.rows[testRepoPath], buildCredsBody(testWorkflow, []string{testWorkflowEnv}, testAppID))
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("a build whose code host cannot be reached was refused: %d %s", resp2.StatusCode, raw2)
	}
	if ans := readBuildCredsAnswer(t, raw2); len(ans.Secrets) != 1 || ans.Secrets[0].Value != f2.reads.value {
		t.Errorf("the answer's secrets = %+v, want the record's own value: the record is the fallback", ans.Secrets)
	}
	if got := f2.reads.records.reads; len(got) != 1 || got[0] != f2.rows[testRepoPath].Slug {
		t.Errorf("the record was read for %v, want the caller's own slug", got)
	}
}
