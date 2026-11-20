package credsvc

// WS17's own tests: a run's GitHub mint and its context tokens read the SCOPE
// (R5, D2978, D2979, F35).
//
// The mint's repository is the one the scope's table pairs the calling job
// account with, and it is held to the coverage this call's forge credentials
// reach before the bundle is read — so a repository the workspace owner's own
// declaration does not name is refused, on a workspace that serves another
// member's repository through another job account, and a scope whose coverage
// could not be read refuses too. The context mint's allow-list is the calling
// scope's policy, which for a workspace is the MEMBER's project: the list the
// launcher admitted the run's context repositories against.
//
// The three fences are the allow-list, the member's policy and the
// repository's admission (the plan's constraint table, R5), and each of the
// four tests below is one of them seen from this task's routes:
//
//   - the TABLE, which is the calling job account's own repositories and never
//     another member's (TestAnotherMembersRepositoryIsRefusedAMint);
//   - the workspace OWNER's coverage, which is what the member's own policy can
//     never substitute for (TestARepositoryNamedOnlyByTheMemberIsRefusedAMint);
//   - the member's own policy, which is the context route's second fence and is
//     read from the scope (TestWorkspaceContextTokensAreFenced).
//
// Everything here is hermetic (Convention 9): the GitHub API is the fake
// loopback server github_test.go builds, the App key is generated at run time
// (Convention 2), and the scope is a value of this file's own installed on the
// front door — no test opens a socket to anything but 127.0.0.1.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// errCoverageUnreadable is the failure a workspace's coverage reader answers
// when the node it reads cannot be read: it carries no node's path and no
// database's error, the shape every reader in this package keeps, and the only
// thing the test below asserts about it is the direction the route takes.
var errCoverageUnreadable = errors.New("credsvc test: the coverage could not be read")

// wsScope is a workspace's scope for the mint's tests: the caller's own
// installation-side resolution — which is what verifies the two identities and
// makes the pairing, so that nothing in these tests is served an identity the
// deployment would not have verified — with the three answers a workspace's
// deployment differs in written by the test: the table a slug is resolved
// through, the forge coverage, and the policy a context repository is admitted
// by.
type wsScope struct {
	Scope
	repos  map[string]string
	forge  ForgeReader
	policy PolicyReader
}

// Repos is the CALLING job account's own repositories and no other's: the
// narrowing a workspace's table is (C1's Repos), which is why the test writes
// it per call rather than leaving it to the deployment.
func (sc wsScope) Repos() map[string]string { return sc.repos }

func (sc wsScope) Forge() ForgeReader { return sc.forge }

func (sc wsScope) Policy() PolicyReader { return sc.policy }

// Serves is held to the two routes these tests reach: a workspace serves a
// run's own token mint and its context tokens, and refuses everything else
// (R14, D2988 — the read mints, the merge mint and the label and issue routes
// stay installation-only). The rest of that set is WS2's and is not pinned
// here.
func (sc wsScope) Serves(path string) bool {
	return path == MintPath || path == ContextTokenPath
}

// AcceptsSlip is false, and that is a workspace's answer rather than this
// fixture's convenience (R18): a member's call carries the job's identity and
// the run's token and nothing else of the member's.
func (sc wsScope) AcceptsSlip() bool { return false }

// wsResolver serves every call of one workspace, over the installation's own
// resolver: what it replaces is the three answers above and nothing else, so
// the identity a test presents is verified from the request exactly as the
// deployment verifies it.
type wsResolver struct {
	inner  ScopeResolver
	repos  map[string]string
	forge  ForgeReader
	policy PolicyReader
}

func (r wsResolver) Resolve(ctx context.Context, req *http.Request) (Scope, error) {
	sc, err := r.inner.Resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	return wsScope{Scope: sc, repos: r.repos, forge: r.forge, policy: r.policy}, nil
}

// wsForge is a workspace's forge reader for these tests: the coverage the
// workspace owner's declaration names for the calling job account, written out
// as a set — and Bitbucket nil, which is a workspace's answer for the token
// store this route never reads (C1's rule 10).
type wsForge struct {
	covers map[string]bool
	err    error
}

func (f wsForge) Bitbucket() bbtokens.Selector { return nil }

func (f wsForge) Covers(_ context.Context, canonical string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.covers[canonical], nil
}

// wsCovering is the coverage a workspace owner's declaration leaves behind,
// spelled as the canonical names it names.
func wsCovering(repos ...string) wsForge {
	covers := make(map[string]bool, len(repos))
	for _, repo := range repos {
		covers[repo] = true
	}
	return wsForge{covers: covers}
}

// wsTable is the table a workspace's scope answers for a caller of one
// repository: the calling job account's own repository, keyed the way every
// table is — the run identity's slug to the canonical name.
func wsTable(t *testing.T, canonical string) map[string]string {
	t.Helper()
	return map[string]string{testSlug(t, strings.TrimPrefix(canonical, "github/")): canonical}
}

// wsMintSvc is a mint fixture served as a workspace's: mintSvc with a scope of
// this file's on the front door, so the caller is the fixture's own run of the
// repository the table names.
func wsMintSvc(t *testing.T, repos map[string]string, forge ForgeReader, policy PolicyReader, mutate func(*svc, *Config)) (*svc, *ghFake) {
	t.Helper()
	s, gh := mintSvc(t, mutate)
	s.server.resolver = wsResolver{inner: installationResolver{s: s.server}, repos: repos, forge: forge, policy: policy}
	return s, gh
}

// wsPolicy is a workspace scope's policy reader: the member's published policy,
// which is the only list a context repository of that member's run is admitted
// by.
func wsPolicy(t *testing.T, doc string) *fakePolicy {
	t.Helper()
	return &fakePolicy{shared: decodeTestPolicy(t, doc), sharedFound: true}
}

// twoMemberAuth is the fixture's authenticator with a SECOND member's job
// service account admitted, which the fixture's own is not (newFixtureWith's
// account list is one repository's job): a workspace's two members are two jobs
// of two projects, and the account a call is verified as is what its scope is
// resolved for.
func twoMemberAuth(t *testing.T, s *svc) *Authenticator {
	t.Helper()
	auth, err := NewAuthenticator(AuthConfig{
		Project:           testProject,
		FirebaseProject:   testFirebase,
		Audience:          testAudience,
		JobAccounts:       []string{testJobSA, testOtherSA},
		Keys:              s.keys,
		JobKeys:           s.jobKeys,
		Outcomes:          RTDBOutcomes{DB: s.db},
		SharedJobAccounts: []SharedJobAccount{sharedRow()},
		Now:               func() time.Time { return s.now },
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return auth
}

// TestWorkspaceMintServesOnlyACoveredRepository is WS17's red step: a run of a
// workspace mints for the repository its own job account is paired with when
// the workspace owner's coverage names it, and for nothing else.
//
// The coverage is the third fence and the one this task adds: before it, the
// mint resolved the repository out of the deployment's table and minted, so a
// workspace whose owner declared no coverage at all — or declared one that does
// not name this repository — was served an installation token from the shared
// App key. A scope whose coverage could not be READ refuses as well: a node
// that could not be read is not a node that names nothing (C1's rule for
// Covers), and a mint is the one act a fail-open answer would spend a
// credential on.
func TestWorkspaceMintServesOnlyACoveredRepository(t *testing.T) {
	t.Run("a covered repository is served", func(t *testing.T) {
		s, gh := wsMintSvc(t, wsTable(t, testRepo), wsCovering(testRepo), wsPolicy(t, testPolicyDoc()), nil)
		resp, body := s.mintCall(t, mintBody(testAppID))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
		}
		ans := mintAnswer(t, body)
		if ans.Repository != testRepo {
			t.Errorf("the answer's repository is %q, want the one the scope's table pairs the calling job account with (%q)", ans.Repository, testRepo)
		}
		lookup, _ := gh.both(t)
		if want := "/repos/" + testRepoPath + "/installation"; lookup.path != want {
			t.Errorf("the installation lookup was %s, want %s", lookup.path, want)
		}
	})

	t.Run("a repository outside the coverage is refused", func(t *testing.T) {
		// The table pairs the calling job account with testRepo — the same
		// table the covered half serves — and the owner's coverage names
		// ANOTHER repository, which is what a workspace whose declaration does
		// not reach this member's repository looks like.
		s, gh := wsMintSvc(t, wsTable(t, testRepo), wsCovering(testOtherRepo), wsPolicy(t, testPolicyDoc()), nil)
		resp, body := s.mintCall(t, mintBody(testAppID))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		got := refusal(t, resp.StatusCode, body)
		if got.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q: the run halts on it (D143)", got.Error, ErrorStateUnavailable)
		}
		if !strings.Contains(got.Message, testRepo) {
			t.Errorf("the refusal is %q, want the repository it is about (%s)", got.Message, testRepo)
		}
		if !strings.Contains(got.Message, "fugaro update") {
			t.Errorf("the refusal is %q, want the fix named: the repositories a shared credential reaches are the workspace owner's declaration, and its `fugaro update` is what publishes them (R19, F28)", got.Message)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests, want none: the coverage is read before the App key", n)
		}
		// One audit line, written by the refusal itself (C1.13): the act the
		// operator reads is a mint that issued nothing.
		lines := mintAuditLines(t, s)
		if len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want the one every post-verification refusal writes: %v", len(lines), lines)
		}
		if got := lines[0]["status"]; got != ErrorStateUnavailable {
			t.Errorf("the refusal's line is recorded as %v, want %q", got, ErrorStateUnavailable)
		}
	})

	t.Run("a coverage that could not be read is refused", func(t *testing.T) {
		forge := wsForge{err: errCoverageUnreadable}
		s, gh := wsMintSvc(t, wsTable(t, testRepo), forge, wsPolicy(t, testPolicyDoc()), nil)
		resp, body := s.mintCall(t, mintBody(testAppID))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		if got := refusal(t, resp.StatusCode, body); got.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests, want none: a coverage that could not be read is not a coverage that names nothing", n)
		}
	})
}

// TestARepositoryNamedOnlyByTheMemberIsRefusedAMint is the constraint the plan
// pins on WS17 (WS16's TestARepositoryNamedOnlyByTheMemberIsRefused, read for
// the mint): a forge entry's coverage is the workspace OWNER's list and never
// the member's.
//
// The scope below is the shape of a member who has admitted the repository
// their own side — the member's administrator added it, and the published
// policy's allow-list names it — while the workspace owner's declaration for
// that member does not. The mint's answer is the owner's, because the App key
// being spent is the owner's: a member's own admission can never widen the
// coverage a shared credential is spent on (R5: the two fences are asked beside
// each other, and a call has to pass both).
func TestARepositoryNamedOnlyByTheMemberIsRefusedAMint(t *testing.T) {
	// The member's own policy admits BOTH repositories — the one the run is
	// paired with and the one the owner's coverage names — so a mint that read
	// this list would have something to admit the call with.
	s, gh := wsMintSvc(t, wsTable(t, testRepo), wsCovering(testOtherRepo), wsPolicy(t, testPolicyDoc()), nil)
	if !projectpolicy.AdmitRepo(s.policy.shared.AllowRepos, testRepo) {
		t.Fatal("the fixture's own policy does not admit the repository under test, so this test could not tell which fence refused the call")
	}
	resp, body := s.mintCall(t, mintBody(testAppID))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: a repository only the member names must not be minted for: %s", resp.StatusCode, StatusUnavailable, body)
	}
	got := refusal(t, resp.StatusCode, body)
	if !strings.Contains(got.Message, testRepo) || !strings.Contains(got.Message, "fugaro update") {
		t.Errorf("the refusal is %q, want the repository and the workspace owner's `fugaro update`", got.Message)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("GitHub saw %d requests, want none", n)
	}
}

// TestAnotherMembersRepositoryIsRefusedAMint is the plan's other constraint row
// read for the mint (WS16's TestAnotherMembersRepositoryIsRefused): one member's
// job never reaches a repository covered for another member.
//
// A workspace serves several members, and each one's scope holds the
// repositories its own job account is paired with and the coverage the owner
// declared for THAT account. The first half is a call of this member's whose
// coverage names the other member's repository and not its own: the token is
// refused, and the other member's repository is never named in an answer. The
// second half is the other member's OWN call on the same workspace: it is
// served, which is what makes the first half a statement about whose coverage
// was read rather than about a repository the workspace cannot serve at all.
func TestAnotherMembersRepositoryIsRefusedAMint(t *testing.T) {
	t.Run("a member's run is not minted for another member's repository", func(t *testing.T) {
		s, gh := wsMintSvc(t, wsTable(t, testRepo), wsCovering(testOtherRepo), wsPolicy(t, testPolicyDoc()), nil)
		resp, body := s.mintCall(t, mintBody(testAppID))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		if strings.Contains(string(body), testOtherRepo) {
			t.Errorf("the answer names another member's repository: %s", body)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests, want none: another member's repository is not one this caller's coverage reaches", n)
		}
	})

	t.Run("the other member's own call is served", func(t *testing.T) {
		// The same workspace, and the call its OTHER member makes: the job
		// account of that member's job, the run identity of that member's
		// repository, and the scope a workspace's resolver builds for it — the
		// table and the coverage that member's own declaration leaves behind.
		otherSlug := testSlug(t, testOtherRepoPath)
		s, gh := mintSvc(t, func(sv *svc, cfg *Config) { cfg.Auth = twoMemberAuth(t, sv) })
		s.server.resolver = wsResolver{
			inner:  installationResolver{s: s.server},
			repos:  map[string]string{otherSlug: testOtherRepo},
			forge:  wsCovering(testOtherRepo),
			policy: wsPolicy(t, testPolicyDoc()),
		}
		other := s.runToken(func(c map[string]any) {
			c["fs"] = otherSlug
			c["sub"] = "r~" + otherSlug + "~" + s.run
			c["user_id"] = c["sub"]
		})
		otherJob := s.jobToken(func(c map[string]any) { c["email"] = testOtherSA })
		resp, body := s.mintTokens(t, mintBody(testAppID), other, otherJob, s.job)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the answer is %d, want 200: the other member's own call is the one this workspace serves: %s", resp.StatusCode, body)
		}
		if ans := mintAnswer(t, body); ans.Repository != testOtherRepo {
			t.Errorf("the answer's repository is %q, want the other member's own %q", ans.Repository, testOtherRepo)
		}
		lookup, _ := gh.both(t)
		if want := "/repos/" + testOtherRepoPath + "/installation"; lookup.path != want {
			t.Errorf("the installation lookup was %s, want %s", lookup.path, want)
		}
	})
}

// TestWorkspaceContextTokensAreFenced: the context mint reads its allow-list
// out of the CALLING SCOPE's policy, which for a workspace is the member's own
// project — the list the launcher admitted the run's context repositories
// against — and its claim fence is unchanged and still asked first.
//
// The two policy halves are the two directions of one fact, and each is
// invisible to the other: a repository the member's list names and the
// workspace's own does not is SERVED (a route still reading the deployment's
// policy refuses it), and one the member's list no longer names is REFUSED (a
// route still reading the deployment's policy serves it). The third half is the
// boundary the route has always had: a repository outside the run's own fcr
// claim is refused before any policy is consulted.
func TestWorkspaceContextTokensAreFenced(t *testing.T) {
	// The member's project admits both of the fixture's repositories; the
	// workspace's own policy node admits only one of them, so the two readers
	// can be told apart by asking for the other.
	memberPolicy := testPolicyDoc()
	narrowDoc := testPolicyDocAllowRepos(
		`["`+testRepo+`"]`,
		`"default": {"allow_repos": ["`+testRepo+`"]}`)
	t.Run("the member's own list admits a context repository", func(t *testing.T) {
		s, _ := contextSvc(t, func(sv *svc, _ *Config) {
			sv.policy.shared = decodeTestPolicy(t, narrowDoc)
		})
		s.server.resolver = wsResolver{
			inner:  installationResolver{s: s.server},
			repos:  wsTable(t, testRepo),
			forge:  wsCovering(testRepo, contextRepo),
			policy: wsPolicy(t, memberPolicy),
		}
		resp, body := s.contextCall(t, []string{contextRepo}, contextBody(contextRepo))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the answer is %d, want 200: the member's list names this repository: %s", resp.StatusCode, body)
		}
		if ans := contextAnswer(t, body); len(ans.Tokens) == 0 {
			t.Error("the answer carries no token for a repository the member's own list admits")
		}
	})

	t.Run("a repository the member's list does not name is refused", func(t *testing.T) {
		s, gh := contextSvc(t, nil)
		if !projectpolicy.AdmitRepo(s.policy.shared.AllowRepos, testOtherRepo) {
			t.Fatal("the fixture's own policy does not admit the repository under test, so this test could not tell which reader refused the call")
		}
		s.server.resolver = wsResolver{
			inner:  installationResolver{s: s.server},
			repos:  wsTable(t, testRepo),
			forge:  wsCovering(testRepo, contextRepo),
			policy: wsPolicy(t, narrowDoc),
		}
		resp, body := s.contextCall(t, []string{contextRepo}, contextBody(contextRepo))
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
		}
		got := refusal(t, resp.StatusCode, body)
		if !strings.Contains(got.Message, contextRepo) {
			t.Errorf("the refusal is %q, want the repository the member's list does not name", got.Message)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests, want none: the policy is read before the App key", n)
		}
	})

	t.Run("the claim is still the boundary", func(t *testing.T) {
		s, _ := contextSvc(t, nil)
		s.server.resolver = wsResolver{
			inner:  installationResolver{s: s.server},
			repos:  wsTable(t, testRepo),
			forge:  wsCovering(testRepo, contextRepo),
			policy: wsPolicy(t, memberPolicy),
		}
		// The claim names the run's OWN repository, and the call asks for
		// another one the member's policy does admit: naming a repository is
		// not being served one (a launch records the run's context
		// repositories, C5a).
		resp, body := s.contextCall(t, []string{testRepoPath}, contextBody(contextRepo))
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
		}
		if got := refusal(t, resp.StatusCode, body); !strings.Contains(got.Message, contextRepo) {
			t.Errorf("the refusal is %q, want the repository the claim does not name", got.Message)
		}
	})
}
