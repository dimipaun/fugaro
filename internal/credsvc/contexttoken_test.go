package credsvc

// The context mint's tests (C5b, D613): a run asks this service for a
// read-only installation token over the repositories its own launch recorded,
// and the service answers with one token per App installation — or refuses,
// and says so in a line an operator can read.
//
// The boundary is the run identity's own fcr claim (C5a). A call NAMES
// repositories, which the write mint's body deliberately cannot, and that is
// admissible only because naming one is not being served one: the claim is read
// before the project's policy and long before the App key, so an agent calling
// this service with the job's identity gains nothing beyond its launch.
//
// Everything here is hermetic (Convention 9): the GitHub API is the fake
// loopback server github_test.go already builds (ghTransport refuses every
// other destination), and the App key is generated at run time (Convention 2).
//
// The client's half of this contract is internal/gitprov/github's
// ServiceTokens.ContextTokens, and the two spellings are held to each other
// where every other route's are: TestTheServiceContextContractIsTheMint.

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
)

// contextRepo and contextSecondRepo are the two repositories the fixture's
// context claims name. The first is a repository of the fixture's own project
// and the second is the one bit of the policy a two-installation test has to
// publish: two owners are two App installations and so two tokens.
const (
	contextRepo       = testOtherRepoPath
	contextSecondRepo = "kepler/nowhere"
	// contextSecondRepoPolicy is the canonical name the published policy holds
	// it in: the key space policy admission is read in.
	contextSecondRepoPolicy = "github/" + contextSecondRepo
)

// contextPolicyDoc is the project's published policy with the second owner's
// repository admitted: what `fugaro project repos add kepler/nowhere` leaves
// behind, and what a context claim naming a repository of another owner is held
// to.
func contextPolicyDoc() string {
	return testPolicyDocAllowRepos(
		`["`+testRepo+`", "`+testOtherRepo+`", "`+contextSecondRepoPolicy+`"]`,
		`"default": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]},
			"`+testAccount+`": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]}`)
}

// contextSvc is the fixture for a context mint: a service whose bundle holds
// the App key, whose published policy admits the fixture's repositories and the
// second owner's, and the fake GitHub behind it — with an installation per
// owner, because two owners are two installations.
func contextSvc(t *testing.T, mutate func(*svc, *Config)) (*svc, *ghFake) {
	t.Helper()
	gh := newGHFake(t)
	gh.installations = map[string]int64{"acme": 777, "kepler": 888}
	s := newSvcWith(t, func(sv *svc, cfg *Config) {
		sv.src.docs = [][]byte{mintBundleDoc(t)}
		cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
		sv.policy.shared = decodeTestPolicy(t, contextPolicyDoc())
		if mutate != nil {
			mutate(sv, cfg)
		}
	})
	return s, gh
}

// contextBody is a context call's body, as the runner builds it.
func contextBody(repos ...string) string {
	raw, err := json.Marshal(ContextTokenRequest{AppID: testAppID, Repositories: repos})
	if err != nil {
		panic("credsvc test: building a context call: " + err.Error())
	}
	return string(raw)
}

// contextCall sends one context call as the fixture's run, whose identity names
// claim as its context repositories, and reads the answer.
func (s *svc) contextCall(t *testing.T, claim []string, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	run := s.runToken(func(c map[string]any) {
		repos := make([]any, len(claim))
		for i, r := range claim {
			repos[i] = r
		}
		c["fcr"] = repos
	})
	resp := s.postTokens(t, ContextTokenPath, body, run, s.jobToken(), s.job, mutate...)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, b
}

// contextAnswer reads a context mint's answer strictly (Convention 6: the two
// ends of this contract are this repository's code).
func contextAnswer(t *testing.T, body []byte) ContextTokenResponse {
	t.Helper()
	var ans ContextTokenResponse
	if err := decodeStrict(body, &ans); err != nil {
		t.Fatalf("the context mint's answer is not one this contract can read: %v (%s)", err, body)
	}
	return ans
}

// TestContextTokenNamesOnlyTheTasksRepos is C5b's own boundary, read forwards:
// a run whose launch recorded context repositories is served a read-only token
// over exactly those, and the answer names them the way the task and the claim
// spell them.
//
// Three facts, and each of them is a thing the endpoint could get wrong:
//
//   - the token GitHub was asked for is scoped to the claim's repositories, by
//     NAME WITHIN THE OWNER, which is the form the API's endpoint takes — and
//     it carries the read-only set, because the request body the fake records
//     is that set exactly;
//   - the answer names the canonical "owner/name" the caller asked in, so a
//     caller matching tokens to its own list reads the same spelling back;
//   - a list under TWO owners is two tokens, one per installation, because a
//     GitHub App installation belongs to the owner and no one token covers two
//     (the fixture's per-owner installation ids are what makes the second
//     lookup's answer distinguishable).
func TestContextTokenNamesOnlyTheTasksRepos(t *testing.T) {
	t.Run("one installation", func(t *testing.T) {
		s, gh := contextSvc(t, nil)
		resp, body := s.contextCall(t, []string{contextRepo}, contextBody(contextRepo))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
		}
		ans := contextAnswer(t, body)
		if len(ans.Tokens) != 1 {
			t.Fatalf("the answer carries %d tokens, want one for one installation: %+v", len(ans.Tokens), ans)
		}
		if ans.Tokens[0].Token != gh.token || !ans.Tokens[0].ExpiresAt.Equal(ghExpiry) {
			t.Errorf("the token is %+v, want the fake's token expiring %s", ans.Tokens[0], ghExpiry)
		}
		if want := []string{contextRepo}; !slices.Equal(ans.Tokens[0].Repositories, want) {
			t.Errorf("the token covers %v, want %v", ans.Tokens[0].Repositories, want)
		}
		// What GitHub was asked: the installation of the repository's OWNER,
		// and a token scoped to the repository BY NAME and to the read-only
		// set. The name within the owner is the API's own form and not the
		// canonical one — a service that sent "acme/borealis" would have been
		// refused by GitHub, and a service that sent the write set would have
		// handed a reading run a writing token.
		seen := gh.requests()
		if len(seen) != 2 {
			t.Fatalf("the fake GitHub saw %d requests, want the lookup and the mint: %v", len(seen), seen)
		}
		if want := "/repos/" + contextRepo + "/installation"; seen[0].path != want {
			t.Errorf("the lookup is %s, want %s", seen[0].path, want)
		}
		if want := "/app/installations/777/access_tokens"; seen[1].path != want {
			t.Errorf("the mint is %s, want %s", seen[1].path, want)
		}
		var minted struct {
			Repositories []string          `json:"repositories"`
			Permissions  map[string]string `json:"permissions"`
		}
		if err := decodeStrict([]byte(seen[1].body), &minted); err != nil {
			t.Fatalf("the mint's body is not one this contract can read: %v (%s)", err, seen[1].body)
		}
		if want := []string{"borealis"}; !slices.Equal(minted.Repositories, want) {
			t.Errorf("the mint is scoped to %v, want %v", minted.Repositories, want)
		}
		if want := map[string]string{"contents": "read", "metadata": "read"}; !maps.Equal(minted.Permissions, want) {
			t.Errorf("the mint asks for %v, want the read-only set %v", minted.Permissions, want)
		}
	})

	t.Run("two owners are two installations", func(t *testing.T) {
		s, gh := contextSvc(t, nil)
		resp, body := s.contextCall(t, []string{contextRepo, contextSecondRepo},
			contextBody(contextRepo, contextSecondRepo))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
		}
		ans := contextAnswer(t, body)
		if len(ans.Tokens) != 2 {
			t.Fatalf("the answer carries %d tokens, want one per installation (2): %+v", len(ans.Tokens), ans)
		}
		for i, want := range [][]string{{contextRepo}, {contextSecondRepo}} {
			if !slices.Equal(ans.Tokens[i].Repositories, want) {
				t.Errorf("token %d covers %v, want %v", i, ans.Tokens[i].Repositories, want)
			}
		}
		// Two lookups and two mints, one per owner, each at its own
		// installation: the fake's strict accounting is what says so, and its
		// per-owner ids are what makes the second installation's mint
		// distinguishable from the first's.
		seen := gh.requests()
		if len(seen) != 4 {
			t.Fatalf("the fake GitHub saw %d requests, want two lookups and two mints: %v", len(seen), seen)
		}
		for i, want := range []string{"777", "888"} {
			if got := seen[1+2*i].path; got != "/app/installations/"+want+"/access_tokens" {
				t.Errorf("mint %d is at %s, want the installation %s", i, got, want)
			}
		}
	})

	// The claim is the boundary and the call is only its subset: a call that
	// asks for ONE of the run's two context repositories is served that one,
	// and the second is not minted for. A service that answered the whole
	// claim whatever the call asked for would hand a run read reach it did not
	// ask for, which is the same complaint as handing it one it was not
	// granted.
	t.Run("the call's own subset", func(t *testing.T) {
		s, gh := contextSvc(t, nil)
		resp, body := s.contextCall(t, []string{contextRepo, contextSecondRepo}, contextBody(contextSecondRepo))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
		}
		ans := contextAnswer(t, body)
		if len(ans.Tokens) != 1 || !slices.Equal(ans.Tokens[0].Repositories, []string{contextSecondRepo}) {
			t.Errorf("the answer is %+v, want one token over %s alone", ans, contextSecondRepo)
		}
		if n := gh.count(); n != 2 {
			t.Errorf("the fake GitHub saw %d requests, want the one installation's lookup and mint", n)
		}
	})
}

// TestServiceRefusesContextRepoNotInClaim: the claim is the boundary, and a
// call naming a repository it does not hold is refused BY NAME — before the
// policy is read and long before GitHub is reached, which the fake's zero
// request count is what proves.
//
// The repository refused here is one the project DOES admit (it is the
// fixture's own repository, in the project's allow_repos and in the calling
// job's list): what refuses it is not the policy but the launch. That is the
// whole point of the route — an agent calling this service with the run's own
// job identity reaches its launch's repositories and no others — and it is why
// the refusal is the CALLER's request (400) rather than the service's state.
func TestServiceRefusesContextRepoNotInClaim(t *testing.T) {
	s, gh := contextSvc(t, nil)
	resp, body := s.contextCall(t, []string{contextRepo}, contextBody(testRepoPath))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorRequestRefused {
		t.Errorf("the refusal's error is %q, want %q", e.Error, ErrorRequestRefused)
	}
	if !strings.Contains(e.Message, testRepoPath) {
		t.Errorf("the refusal is %q, want the repository it refused named", e.Message)
	}
	if !strings.HasPrefix(e.Message, requestRefused) {
		t.Errorf("the refusal is %q, want the contract's own shape", e.Message)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("the fake GitHub saw %d requests for a repository the run never named, want none", n)
	}
	// The sentence is the run's and the audit line is the operator's: the line
	// carries the contract's fields and NOT the message, so what an operator
	// reads there is the act and the refusal's class (TestServiceAuditsContextRefusal).
	if strings.Contains(s.audit.String(), testRepoPath) {
		t.Errorf("the audit line carries the refusal's sentence: %s", s.audit.String())
	}
}

// TestServiceRefusesContextRepoOutsidePolicy: a repository the claim names and
// the project no longer admits is refused too, and refused as the caller's
// request.
//
// The claim is the launch's answer and the policy is the project's, and the two
// can disagree in exactly this direction: the launcher admitted the context
// repository against the allow-list the project published when the run started,
// and `fugaro project repos rm` takes it out afterwards. A run that had it and
// lost it is not served a token the project has withdrawn — the same re-read
// the run's own repository gets from the pairing — and the refusal is the
// caller's because the repository the call named is not one this project reads.
//
// The refusal is the policy's OWN sentence (projectpolicy.NotAdmitted), which is
// the one the runner's admission and the launcher's report already carry: one
// repository refused for the same reason reads the same way wherever an
// operator meets it.
func TestServiceRefusesContextRepoOutsidePolicy(t *testing.T) {
	s, gh := contextSvc(t, func(s *svc, _ *Config) {
		// The project takes the second owner's repository back out of its
		// allow-list, and leaves the claim alone.
		s.policy.shared = decodeTestPolicy(t, testPolicyDoc())
	})
	resp, body := s.contextCall(t, []string{contextSecondRepo}, contextBody(contextSecondRepo))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorRequestRefused {
		t.Errorf("the refusal's error is %q, want %q", e.Error, ErrorRequestRefused)
	}
	if !strings.Contains(e.Message, contextSecondRepoPolicy) {
		t.Errorf("the refusal is %q, want the policy's own sentence naming %s", e.Message, contextSecondRepoPolicy)
	}
	if !strings.Contains(e.Message, "allow_repos") {
		t.Errorf("the refusal is %q, want the allow-list it was read against", e.Message)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("the fake GitHub saw %d requests for a repository the policy does not admit, want none", n)
	}
}

// TestServiceAuditsContextRefusal: every refusal of this route writes exactly
// one line (C1.13, and the credentials-service rule that no path writes zero
// lines or two), and the line is the one an operator greps for the act.
//
// The purpose is the route's own (PurposeContextToken) and it is the field that
// tells a run's reach beyond its repository from the run's own mint — both are
// signed with the one App key, so the credential name alone could not. The
// credential is the App key's NAME and the tier is present and EMPTY, because
// the App key is not a model credential and was spent on no call. The status is
// the refusal's own wire error type, so an operator's line and the run's halt
// message read the same word for the same refusal.
//
// What the line is NOT is a record of which repositories the call asked for:
// its repository field is the run's own (Identity.Audit), and a context list is
// a property of the call and not of the run. The refusal's sentence names the
// repository a call was refused for, and that sentence is the run's and reaches
// no line.
func TestServiceAuditsContextRefusal(t *testing.T) {
	s, gh := contextSvc(t, nil)
	resp, body := s.contextCall(t, []string{contextRepo}, contextBody(testRepoPath))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	lines := mintAuditLines(t, s)
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %v", len(lines), lines)
	}
	checkAuditShape(t, lines[0])
	for field, want := range map[string]any{
		"purpose":    PurposeContextToken,
		"credential": config.GitHubAppKeySecret,
		"status":     ErrorRequestRefused,
		"run":        s.run,
		"repository": s.slug,
		"job":        s.job,
		"caller":     testJobSA,
		"tier":       "",
	} {
		if got := lines[0][field]; got != want {
			t.Errorf("the refusal's audit line %s = %v, want %q", field, got, want)
		}
	}
	for _, secret := range []string{gh.token, pemCore(t), testRepoPath} {
		if strings.Contains(s.audit.String(), secret) {
			t.Errorf("the refusal's audit line carries %q", secret)
		}
	}

	// An issuance is audited with the same purpose and the contract's own
	// StatusOK, and it is audited as one act: a run that reads three
	// repositories under two owners is one context mint and not two lines.
	s.audit.b.Reset()
	resp, body = s.contextCall(t, []string{contextRepo, contextSecondRepo},
		contextBody(contextRepo, contextSecondRepo))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	lines = mintAuditLines(t, s)
	if len(lines) != 1 {
		t.Fatalf("the issuance wrote %d audit lines, want one per act: %v", len(lines), lines)
	}
	checkAuditShape(t, lines[0])
	for field, want := range map[string]any{
		"purpose":    PurposeContextToken,
		"credential": config.GitHubAppKeySecret,
		"status":     StatusOK,
		"run":        s.run,
		"repository": s.slug,
		"tier":       "",
	} {
		if got := lines[0][field]; got != want {
			t.Errorf("the issuance's audit line %s = %v, want %q", field, got, want)
		}
	}
	if strings.Contains(s.audit.String(), gh.token) {
		t.Error("the issuance's audit line carries the token it issued")
	}
}

// TestWriteTokenStillOneRepository: the write mint is unchanged by C5b, and
// that is a claim about the endpoint rather than about a diff — so it is held
// where a caller would meet the difference.
//
// A mint's body carries the App ID and NOTHING ELSE, which is what makes "the
// repository is never the caller's to name" a property of the endpoint instead
// of a check on a field: a body that names a repository is refused, unknown
// field and all, before GitHub is reached. And what a mint answers is the
// calling JOB's repository, resolved by the pairing, whatever the run's context
// claim holds — the run in this test reads another repository, and its mint
// still covers its own.
func TestWriteTokenStillOneRepository(t *testing.T) {
	s, gh := contextSvc(t, nil)
	run := s.runToken(func(c map[string]any) { c["fcr"] = []any{contextRepo} })
	resp, body := s.mintTokens(t, `{"app_id":"`+testAppID+`","repositories":["`+testRepoPath+`"]}`,
		run, s.jobToken(), s.job)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a mint body naming a repository is answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("the fake GitHub saw %d requests for a body the mint refuses, want none", n)
	}
	if strings.Contains(s.audit.String(), testRepoPath) {
		t.Errorf("the refused mint's audit line carries the body's repository: %s", s.audit.String())
	}

	// The mint itself, by the run whose context claim names another
	// repository: the token is the job's repository's, and it is exactly one.
	s.audit.b.Reset()
	resp, body = s.mintTokens(t, mintBody(testAppID), run, s.jobToken(), s.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the mint is answered %d, want 200: %s", resp.StatusCode, body)
	}
	if ans := mintAnswer(t, body); ans.Repository != testRepo {
		t.Errorf("the mint covers %q, want the calling job's own %q", ans.Repository, testRepo)
	}
	seen := gh.requests()
	if len(seen) != 2 {
		t.Fatalf("the fake GitHub saw %d requests for one mint, want the lookup and the mint: %v", len(seen), seen)
	}
	if want := "/repos/" + testRepoPath + "/installation"; seen[0].path != want {
		t.Errorf("the mint's lookup is %s, want %s", seen[0].path, want)
	}
}

// TestTheServiceContextContractIsTheMint holds the two ends of the context
// route to each other, the way TestTheServiceTokenContractIsTheMints holds the
// write mint's: the path the client posts to, the identity headers it presents,
// the request's spelling and the answer's fields.
//
// The request is built from the SERVICE's own type and marshalled, and the
// answer read from a REAL call — a struct built in the test would carry
// whatever the test put in it, so an assertion over it could never fail.
func TestTheServiceContextContractIsTheMint(t *testing.T) {
	if github.ServiceContextTokenPath != ContextTokenPath {
		t.Errorf("the run asks for a context token at %q, this service serves %q", github.ServiceContextTokenPath, ContextTokenPath)
	}
	if ContextTokenPath == MintPath {
		t.Error("the context mint is the write mint's path: the write mint's body carries no repository and must go on carrying none")
	}
	body, err := json.Marshal(ContextTokenRequest{AppID: "1234", Repositories: []string{contextRepo}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), `{"app_id":"1234","repositories":["`+contextRepo+`"]}`; got != want {
		t.Errorf("a context mint's body is %s, want %s -- and the run sends exactly this", got, want)
	}
	s, _ := contextSvc(t, nil)
	resp, raw := s.contextCall(t, []string{contextRepo}, contextBody(contextRepo))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a context mint was answered %d: %s", resp.StatusCode, raw)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("a context mint's answer is not one JSON object: %v", err)
	}
	tokens, ok := fields["tokens"]
	if !ok {
		t.Fatalf("a context mint's answer carries no \"tokens\", so a run reading one reads nothing: %s", raw)
	}
	var first []map[string]json.RawMessage
	if err := json.Unmarshal(tokens, &first); err != nil || len(first) == 0 {
		t.Fatalf("a context mint's answer's tokens are not a non-empty list: %v (%s)", err, raw)
	}
	for _, name := range []string{"token", "expires_at", "repositories"} {
		if first[0][name] == nil {
			t.Errorf("a context token carries no %q, so a run reading one reads nothing: %s", name, raw)
		}
	}
	// The answer is an object of those fields and no credential of its own: the
	// App key that signed the mint is nowhere in the bytes the service wrote.
	if strings.Contains(string(raw), pemCore(t)) {
		t.Error("a context mint's answer carries the App key")
	}
}
