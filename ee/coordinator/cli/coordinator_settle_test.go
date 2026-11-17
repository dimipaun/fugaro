// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// These are the settle command's own tests: the environment the decision is
// made in — the set read out of the run that names it, the members' records out
// of the runs bucket, and the settle-only token the writes are made with
// (D641). What the decision itself does with a member is ee/coordinator's
// (changeset_test.go); what is judged here is that the command reaches it with
// the right set, the right records and the right credential, and that a set it
// cannot read is the caller's own refusal.

const (
	settleSetID   = "20261116-140000-a1b2"
	settleOtherID = "20261116-140001-c3d4"

	settleAppHead   = "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
	settleOtherHead = "9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b1c0d"

	// settleFixtureToken is what the fake GitHub answers a mint with: a value
	// of the shape an installation token has and not a credential, so nothing
	// here is a secret-shaped literal.
	settleFixtureToken = "ghs_fixture_settle_token"
)

// TestCoordinatorSettleRefusesASetItCannotRead: the set is read from the run
// that names it — the id is its first member's run (D642) — so a --change-set
// that names no run of the repository, or a run that is in no change set, is
// the caller's own refusal (exit 1) and never an empty report: a settle that
// answered "nothing to do" for a set it never found would say the set is
// settled.
func TestCoordinatorSettleRefusesASetItCannotRead(t *testing.T) {
	f := newCloudFixture(t)
	_, _, err := execute(t, "coordinator", "settle")
	if err == nil || !strings.Contains(err.Error(), "--change-set") {
		t.Fatalf("settle with no --change-set = %v, want a refusal naming it", err)
	}
	if got := fcli.ExitCode(err); got != fcli.ExitUserError {
		t.Errorf("no --change-set: exit %d, want %d", got, fcli.ExitUserError)
	}
	if _, _, err := execute(t, "coordinator", "settle", "--change-set", "yesterday"); err == nil ||
		!strings.Contains(err.Error(), "20260926-221530-a1b2") {
		t.Errorf("a --change-set that is not a run id = %v, want the shape it must have", err)
	}

	// No run of that id at all: the set's id is the id of its FIRST member's
	// run, so the refusal says which repository the run is looked for under.
	// This fixture's project lists one repository, so --repo defaults to it.
	_, _, err = execute(t, "coordinator", "settle", "--change-set", settleSetID)
	if err == nil || !strings.Contains(err.Error(), task.ShortRun(settleSetID)) || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("a set naming no run = %v, want a refusal naming the run and --repo", err)
	}
	if got := fcli.ExitCode(err); got != fcli.ExitUserError {
		t.Errorf("no such run: exit %d, want %d", got, fcli.ExitUserError)
	}

	// A run that is there and is in no change set: an ordinary launch, which is
	// nothing to settle and not a set with no members.
	seedSettleTask(t, f, "acme/app", appSlug, settleSetID, nil)
	_, _, err = execute(t, "coordinator", "settle", "--change-set", settleSetID, "--repo", "acme/app")
	if err == nil || !strings.Contains(err.Error(), "is in no change set") {
		t.Fatalf("a run in no change set = %v, want a refusal saying so", err)
	}
	if got := fcli.ExitCode(err); got != fcli.ExitUserError {
		t.Errorf("a run in no change set: exit %d, want %d", got, fcli.ExitUserError)
	}
}

// TestCoordinatorSettleSettlesASetFromTheRunsBucket drives the whole command
// with nothing replaced but the two ends it must not reach: the runs bucket is
// the fixture's own (file://), the member records are seeded the way a member's
// run leaves them, and the host is a fake GitHub — its API root, which is where
// the settle mints (D641), and the providers the pull requests are written
// through. So what is judged is the wiring the command is: the set read out of
// the first member's task, every member's task, record and changeset.json read
// out of its own prefix, ONE token minted for the set's members and scoped to
// them with pull requests write and nothing else, and every pull request left
// ready.
func TestCoordinatorSettleSettlesASetFromTheRunsBucket(t *testing.T) {
	f := newCloudFixture(t)
	enrollSettleRepo(t, "acme/other")
	set := &task.ChangeSet{ID: settleSetID, Members: []task.ChangeSetMember{
		{Repo: "acme/app", RunID: settleSetID},
		{Repo: "acme/other", RunID: settleOtherID},
	}}
	seedSettleTask(t, f, "acme/app", appSlug, settleSetID, set)
	seedSettleMember(t, f, appSlug, "acme/app", settleSetID, settleAppHead, set)
	otherSlug := mustSlug(gitprov.KindGitHub, "acme/other")
	seedSettleTask(t, f, "acme/other", otherSlug, settleOtherID, set)
	seedSettleMember(t, f, otherSlug, "acme/other", settleOtherID, settleOtherHead, set)

	mint := newSettleMintServer(t, "app", "other")
	setCoordinatorGitHubRoot(t, mint.srv.URL)
	t.Setenv(providers.EnvGitHubAppID, appIDFixture)
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	hosts := map[string]*settleHostProvider{
		"acme/app":   settleHost("acme/app", settleAppHead),
		"acme/other": settleHost("acme/other", settleOtherHead),
	}
	setSettleHosts(t, hosts)

	out, _, err := execute(t, "coordinator", "settle", "--change-set", settleSetID, "--repo", "acme/app")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if !strings.Contains(out, "settled") || strings.Contains(out, "not settled") {
		t.Errorf("the report = %q, want the set settled", out)
	}
	for repo, host := range hosts {
		prs := host.Snapshot().PRs
		if len(prs) != 1 {
			t.Fatalf("%s holds %d pull requests, want 1", repo, len(prs))
		}
		if prs[0].Draft {
			t.Errorf("%s's pull request is still a draft", repo)
		}
		if !strings.Contains(out, repo) {
			t.Errorf("the report does not name %s: %q", repo, out)
		}
		// The provider each pull request was written through was opened with
		// the settle's own token and not with a credential of the App's: the
		// source installed on it answers the token the mint just handed out.
		src := host.source()
		if src == nil {
			t.Fatalf("%s was written to through a provider the settle installed no token on", repo)
		}
		token, _, err := src.Token(context.Background(), 0)
		if err != nil {
			t.Fatalf("%s's token source: %v", repo, err)
		}
		if token != settleFixtureToken {
			t.Errorf("%s was written with %q, want the settle token the mint answered", repo, token)
		}
	}

	// The credential is the settle's own: one token for the set's members, in
	// the one owner they are in, and the permission set is D641's — pull
	// requests write and nothing else (the mint itself is held to that in
	// internal/gitprov/github's TestSettleTokenIsPullRequestsWriteOnly; this is
	// the command asking for it).
	got := mint.bodies()
	if len(got) != 1 {
		t.Fatalf("the settle minted %d tokens, want 1 for the set's one owner: %+v", len(got), got)
	}
	if repos := got[0]["repositories"]; !equalStrings(repos, []string{"app", "other"}) {
		t.Errorf("the token is scoped to %v, want the set's members [app other]", repos)
	}
	perms, _ := got[0]["permissions"].(map[string]any)
	for name, value := range perms {
		if value != "read" && name != "pull_requests" {
			t.Errorf("the settle token asks for %s: %v, and its reach is pull requests write alone (D641)", name, value)
		}
	}
	if perms["pull_requests"] != "write" {
		t.Errorf("the settle token's pull requests permission = %v, want write", perms["pull_requests"])
	}
}

// TestCoordinatorSettleReportsASetThatHasNotShipped: a set one of whose members
// has not shipped is left exactly as it was and reported, exit 0 — the report
// is the answer and the coordinator job runs this on every tick — and no
// credential is minted for it, which is the half of the decision that matters
// for a job that settles every set it knows.
func TestCoordinatorSettleReportsASetThatHasNotShipped(t *testing.T) {
	f := newCloudFixture(t)
	enrollSettleRepo(t, "acme/other")
	set := &task.ChangeSet{ID: settleSetID, Members: []task.ChangeSetMember{
		{Repo: "acme/app", RunID: settleSetID},
		{Repo: "acme/other", RunID: settleOtherID},
	}}
	seedSettleTask(t, f, "acme/app", appSlug, settleSetID, set)
	seedSettleMember(t, f, appSlug, "acme/app", settleSetID, settleAppHead, set)
	otherSlug := mustSlug(gitprov.KindGitHub, "acme/other")
	seedSettleTask(t, f, "acme/other", otherSlug, settleOtherID, set)
	// The member that has not shipped: a record with no verdict, which is what
	// a run that ended without one writes over an earlier attempt's `ship`.
	member := settleMemberRecord("acme/other", settleOtherID, settleOtherHead, set)
	member.Verdict = ""
	seedSettleRecord(t, f, otherSlug, settleOtherID, settleOtherHead)
	seedSettleChangeSetMember(t, f, otherSlug, member)

	mint := newSettleMintServer(t, "app", "other")
	setCoordinatorGitHubRoot(t, mint.srv.URL)
	t.Setenv(providers.EnvGitHubAppID, appIDFixture)
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	hosts := map[string]*settleHostProvider{"acme/app": settleHost("acme/app", settleAppHead)}
	setSettleHosts(t, hosts)

	out, _, err := execute(t, "coordinator", "settle", "--change-set", settleSetID, "--repo", "acme/app")
	if err != nil {
		t.Fatalf("settle of a set that has not shipped = %v, want a report and exit 0", err)
	}
	if !strings.Contains(out, "not settled") || !strings.Contains(out, "has not shipped") {
		t.Errorf("the report = %q, want the set held back and the member named", out)
	}
	if prs := hosts["acme/app"].Snapshot().PRs; len(prs) != 1 || !prs[0].Draft {
		t.Errorf("the member that HAS shipped was written to while the set was held back: %+v", prs)
	}
	if n := len(mint.bodies()); n != 0 {
		t.Errorf("a set held back minted %d tokens, want none: no credential is asked for until every member has shipped", n)
	}
	if len(hosts["acme/app"].Snapshot().Calls) != 0 {
		t.Errorf("a set held back opened and wrote through a provider: %+v", hosts["acme/app"].Snapshot().Calls)
	}
}

// settleHost is one member repository's fake host: the draft pull request its
// run opened, at head.
//
// The fake is wrapped, and the wrapper is the point: the settle hands the
// provider it opens the token source it minted (installCoordinatorSource), and
// the file-backed fake is deliberately not a gitprov.ServiceTokenSetter — the
// core's own test of that refusal depends on it (TestCoordinatorCredentialIs-
// InstalledOnTheProvider). What the wrapper holds is the source the settle
// installed, so a test can assert which credential the writes would have gone
// out with.
type settleHostProvider struct {
	*fake.Provider
	mu   sync.Mutex
	from gitprov.TokenSource
}

func (p *settleHostProvider) UseServiceTokens(src gitprov.TokenSource) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.from = src
}

// source is the token source the settle installed on this host, nil when it
// opened none.
func (p *settleHostProvider) source() gitprov.TokenSource {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.from
}

func settleHost(repo, head string) *settleHostProvider {
	return &settleHostProvider{Provider: &fake.Provider{Repo: repo, SelfID: "1", State: fake.State{PRs: []fake.PRState{{
		PR:    gitprov.PR{Number: 1, URL: "https://github.com/" + repo + "/pull/1", Draft: true},
		Spec:  gitprov.PRSpec{Branch: "topic"},
		Title: "a change",
		Head:  head,
	}}}}}
}

// setSettleHosts points the command's GitHub opener at one fake host per
// member repository. The opener is a seam because the real one would reach the
// network; the mint and the token it is opened with are not, so what a test
// replaces is the host and never the credential.
func setSettleHosts(t *testing.T, hosts map[string]*settleHostProvider) {
	t.Helper()
	prev := coordinatorSettleProvider
	coordinatorSettleProvider = func(o github.Options) (gitprov.Provider, error) {
		host, ok := hosts[o.Owner+"/"+o.Repo]
		if !ok {
			return nil, os.ErrNotExist
		}
		return host, nil
	}
	t.Cleanup(func() { coordinatorSettleProvider = prev })
}

// settleMemberRecord is one member's changeset.json as its run leaves it: the
// set it belongs to, its own run and repository, its pull request and the
// verdict it ended on.
func settleMemberRecord(repo, runID, head string, set *task.ChangeSet) *runstore.ChangeSetMember {
	return &runstore.ChangeSetMember{Version: 1, SetID: set.ID, Repo: repo, RunID: runID,
		PRURL: "https://github.com/" + repo + "/pull/1", Verdict: runstore.VerdictShip,
		At: time.Date(2026, 11, 16, 14, 30, 0, 0, time.UTC)}
}

// seedSettleTask writes one member run's task, carrying set (nil for a run in
// no change set).
func seedSettleTask(t *testing.T, f *cloudFixture, repo, slug, runID string, set *task.ChangeSet) {
	t.Helper()
	store := runstore.Open(settleBucket(t, f), slug, runID)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: "main", Workflow: "web",
		Task: "a change", ChangeSet: set}
	if err := store.CreateTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
}

// seedSettleMember writes one member run's result.json and its changeset.json:
// the commit its tests ran on with a passing clean-tree test record, the pull
// request it opened, and the verdict it ended on.
func seedSettleMember(t *testing.T, f *cloudFixture, slug, repo, runID, head string, set *task.ChangeSet) {
	t.Helper()
	seedSettleRecord(t, f, slug, runID, head)
	seedSettleChangeSetMember(t, f, slug, settleMemberRecord(repo, runID, head, set))
}

func seedSettleRecord(t *testing.T, f *cloudFixture, slug, runID, head string) {
	t.Helper()
	store := runstore.Open(settleBucket(t, f), slug, runID)
	rec := &runstore.Record{Version: 1, RunID: runID, Repo: "acme/app", HeadSHA: head,
		PR:     &runstore.PRRef{Number: 1, URL: "https://github.com/acme/app/pull/1"},
		Verify: []verify.Record{{N: 1, Kind: verify.KindTest, HeadSHA: head, CleanTree: true, Passed: true}}}
	if err := store.WriteRecord(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

func seedSettleChangeSetMember(t *testing.T, f *cloudFixture, slug string, m *runstore.ChangeSetMember) {
	t.Helper()
	store := runstore.Open(settleBucket(t, f), slug, m.RunID)
	if err := store.WriteChangeSetMember(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}

// settleBucket is the fixture's runs bucket, opened once per call and closed by
// the test.
func settleBucket(t *testing.T, f *cloudFixture) *blob.Bucket {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

// enrollSettleRepo writes a second repository into the fixture's own repos
// block: a change set spans repositories, and a member the project's own
// configuration cannot place is one the command cannot read.
func enrollSettleRepo(t *testing.T, repo string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const key = "repos:\n"
	at := strings.Index(string(data), key)
	if at < 0 {
		t.Fatal("the fixture's config has no repos block to enroll a repository in")
	}
	at += len(key)
	entry := "  " + repo + ": { provider: github, base_branch: main, workflows: [web], github_app_id: \"" + appIDFixture + "\" }\n"
	if err := os.WriteFile(path, []byte(string(data)[:at]+entry+string(data)[at:]), 0o600); err != nil {
		t.Fatal(err)
	}
}

// settleMintServer is the fake GitHub the settle mints at: the installation
// lookup and the access-token endpoint, answering one token, and the revoke the
// command ends with. It records every access-token request body, which is what
// says which repositories and which permissions the command asked for.
type settleMintServer struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []map[string]any
}

func newSettleMintServer(t *testing.T, repos ...string) *settleMintServer {
	t.Helper()
	m := &settleMintServer{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/installation"):
			_, _ = w.Write([]byte(`{"id": 777}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/access_tokens"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("the settle minted with a body that is not JSON: %v", err)
			}
			m.mu.Lock()
			m.got = append(m.got, body)
			m.mu.Unlock()
			_, _ = w.Write([]byte(`{"token": "` + settleFixtureToken + `", "expires_at": "` +
				time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// bodies is every access-token request body the settle made, in order.
func (m *settleMintServer) bodies() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]any(nil), m.got...)
}

// equalStrings reports whether v is exactly want: the repositories the mint
// names, as the request carries them.
func equalStrings(v any, want []string) bool {
	list, ok := v.([]any)
	if !ok || len(list) != len(want) {
		return false
	}
	for i, s := range want {
		if list[i] != s {
			return false
		}
	}
	return true
}
