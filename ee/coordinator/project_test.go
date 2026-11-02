// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
)

// These are P2's tests for the project-wide read: the refusal of a provider
// its credential cannot be widened to, the token minted per repository for
// the App's installation, the cache that keeps a second read from minting
// again, and the redaction that keeps a credential out of a failure.
//
// They are the reader's own tests and drive no network: the mint and the
// opener are the fixture's, so what is under test is the coordinator's
// decisions — which repository gets which token, how long a token is kept,
// what a failure may carry — and not GitHub's answers.

// projectFixture is the reader under test: a mint that counts its calls and
// hands each one a token of its own, and an opener that hands back the fake
// host of the repository it was asked for.
type projectFixture struct {
	now     time.Time
	mu      sync.Mutex
	mints   map[string]int
	opened  []string
	sources map[string]gitprov.TokenSource
	hosts   map[string]*fake.Provider
}

func newProjectFixture(repos ...string) *projectFixture {
	f := &projectFixture{
		now:     time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC),
		mints:   map[string]int{},
		sources: map[string]gitprov.TokenSource{},
		hosts:   map[string]*fake.Provider{},
	}
	for _, repo := range repos {
		f.hosts[repo] = &fake.Provider{Repo: repo, SelfID: "1"}
	}
	return f
}

func (f *projectFixture) project() *Project {
	return &Project{
		Mint: func(_ context.Context, repo string) (string, time.Time, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.mints[repo]++
			return fmt.Sprintf("ghs_fixture_%s_%d", strings.ReplaceAll(repo, "/", "_"), f.mints[repo]),
				f.now.Add(time.Hour), nil
		},
		Open: func(ctx context.Context, repo string, src gitprov.TokenSource) (gitprov.Provider, []string, error) {
			// A provider asks for its token on its first API call, so the
			// fixture asks here: a read then mints the way a real one does,
			// and the cache is exercised by the reads rather than beside them.
			if _, _, err := src.Token(ctx, 2*time.Minute); err != nil {
				return nil, nil, err
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.opened = append(f.opened, repo)
			f.sources[repo] = src
			host, ok := f.hosts[repo]
			if !ok {
				return nil, nil, fmt.Errorf("the fixture has no host for %s", repo)
			}
			return host, nil, nil
		},
		Redact: agent.Redact,
		Now:    func() time.Time { return f.now },
	}
}

// minted is how many tokens were minted for repo.
func (f *projectFixture) minted(repo string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mints[repo]
}

// prState is one pull request the fake host holds.
func prState(number int, branch string, state gitprov.PRState) fake.PRState {
	return fake.PRState{
		PR:    gitprov.PR{Number: number, URL: fmt.Sprintf("https://github.com/acme/web/pull/%d", number)},
		Spec:  gitprov.PRSpec{Branch: branch},
		State: state,
	}
}

// A project that holds a Bitbucket repository is refused, and the refusal
// names the reason rather than the repository: the credential is one
// repository's access token and there is nothing to widen it to (reconciliation
// Q12 answered (A), finding #30). Nothing is minted or opened on the way to
// the refusal — the read does not spend a credential to find out it cannot
// serve the project — and a repository with no work in flight is refused the
// same way, so a caller may ask about every repository it knows.
func TestCoordinatorRefusesABitbucketProject(t *testing.T) {
	f := newProjectFixture("acme/legacy")
	p := f.project()
	_, err := p.PullRequests(context.Background(), gitprov.KindBitbucket, "acme/legacy", nil)
	if !errors.Is(err, ErrBitbucket) {
		t.Fatalf("PullRequests on a bitbucket project = %v, want ErrBitbucket", err)
	}
	if !strings.Contains(err.Error(), "cannot be widened to a project") {
		t.Errorf("the refusal does not name the reason: %v", err)
	}
	for _, named := range []string{"acme/legacy", "legacy"} {
		if strings.Contains(err.Error(), named) {
			t.Errorf("the refusal names the repository (%q) rather than the reason: %v", named, err)
		}
	}
	if len(f.opened) != 0 || len(f.mints) != 0 {
		t.Errorf("the refused read opened %v and minted %v, want neither", f.opened, f.mints)
	}
}

// Two repositories under one App mint two tokens: D193's answer is a token per
// repository, minted through the same App and cached under the repository it
// is scoped to, so neither repository is ever read with the other's token.
func TestCoordinatorMintsATokenPerRepository(t *testing.T) {
	f := newProjectFixture("acme/web", "acme/api")
	f.hosts["acme/web"].State.PRs = []fake.PRState{prState(12, "topic/web", gitprov.PROpen)}
	f.hosts["acme/api"].State.PRs = []fake.PRState{prState(7, "topic/api", gitprov.PROpen)}
	p := f.project()
	ctx := context.Background()

	for repo, number := range map[string]int{"acme/web": 12, "acme/api": 7} {
		prs, err := p.PullRequests(ctx, gitprov.KindGitHub, repo, []int{number})
		if err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		if len(prs) != 1 || prs[0].Number != number || prs[0].State != StateOpen {
			t.Errorf("%s: pull requests = %+v, want #%d open", repo, prs, number)
		}
		if got := f.minted(repo); got != 1 {
			t.Errorf("%s: %d mints, want 1", repo, got)
		}
	}

	// Each repository's provider was opened with a source that yields that
	// repository's own token, and the two are not the same token.
	tokens := map[string]string{}
	for _, repo := range []string{"acme/web", "acme/api"} {
		src, ok := f.sources[repo]
		if !ok {
			t.Fatalf("%s was never opened", repo)
		}
		tok, _, err := src.Token(ctx, time.Minute)
		if err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		tokens[repo] = tok
	}
	if tokens["acme/web"] == tokens["acme/api"] {
		t.Errorf("both repositories read with the token %q, want one token each", tokens["acme/web"])
	}
}

// A second read of the same repository reuses the cached token rather than
// minting again, and a token that no longer satisfies the validity asked for
// is not reused: the cache is keyed by the repository and lives for the
// token's own life.
func TestCoordinatorReusesTheCachedToken(t *testing.T) {
	f := newProjectFixture("acme/web")
	f.hosts["acme/web"].State.PRs = []fake.PRState{prState(12, "topic/web", gitprov.PROpen)}
	p := f.project()
	ctx := context.Background()

	read := func() {
		t.Helper()
		if _, err := p.PullRequests(ctx, gitprov.KindGitHub, "acme/web", []int{12}); err != nil {
			t.Fatal(err)
		}
	}
	token := func() string {
		t.Helper()
		tok, _, err := f.sources["acme/web"].Token(ctx, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	read()
	first := token()
	read()
	if second := token(); second != first {
		t.Errorf("the second read minted %q, want the cached %q", second, first)
	}
	if got := f.minted("acme/web"); got != 1 {
		t.Errorf("%d mints over two reads of one repository, want 1: the plan's own red step", got)
	}

	// The token the fixture mints expires an hour out, and the reader asks for
	// a minute: an hour less a minute is no longer a token that satisfies it.
	f.now = f.now.Add(time.Hour - time.Minute)
	read()
	if third := token(); third == first {
		t.Errorf("a token about to expire was reused: %q", third)
	}
	if got := f.minted("acme/web"); got != 2 {
		t.Errorf("%d mints, want 2: a token that no longer satisfies the validity asked for is minted again", got)
	}
}

// secretHost fails every read of a pull request with an error carrying the
// credential, which is what a real host's 401 does.
type secretHost struct {
	*fake.Provider
	secret string
	cause  error
}

func (h secretHost) PullRequest(context.Context, int) (gitprov.PRInfo, error) {
	return gitprov.PRInfo{}, fmt.Errorf("GET /repos/acme/web/pulls/12: 401: bad credentials (token %s): %w", h.secret, h.cause)
}

// Nothing the reader reports names a credential: the credential values the
// opener holds are redacted out of a failure, whether it came from opening the
// provider or from a read through it.
func TestCoordinatorRedactsTheCredentialsAnOpenerHolds(t *testing.T) {
	const secret = "ghs_0123456789abcdefghij"
	// The failure's kind, wrapped by both hosts below: the reader rewrites the
	// text of what it reports, and a caller above decides from the kind whether
	// a failure is the configuration's or the host's — so the kind has to
	// survive the rewrite (redactedError).
	sentinel := errors.New("the host refused the credential")
	ctx := context.Background()
	mint := func(context.Context, string) (string, time.Time, error) {
		return "ghs_fixture_token", time.Now().Add(time.Hour), nil
	}
	for _, tc := range []struct {
		name string
		open ProviderOpener
	}{
		{"opening the provider", func(context.Context, string, gitprov.TokenSource) (gitprov.Provider, []string, error) {
			return nil, []string{secret}, fmt.Errorf("github: the App credential %s was refused: %w", secret, sentinel)
		}},
		{"reading a pull request", func(_ context.Context, repo string, _ gitprov.TokenSource) (gitprov.Provider, []string, error) {
			return secretHost{Provider: &fake.Provider{Repo: repo, SelfID: "1"}, secret: secret, cause: sentinel}, []string{secret}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Project{Mint: mint, Open: tc.open, Redact: agent.Redact}
			_, err := p.PullRequests(ctx, gitprov.KindGitHub, "acme/web", []int{12})
			if err == nil {
				t.Fatal("the read failed and PullRequests succeeded")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the credential reached the failure: %v", err)
			}
			if !strings.Contains(err.Error(), "[REDACTED]") {
				t.Errorf("the failure is %v, want the credential redacted in it", err)
			}
			if !errors.Is(err, sentinel) {
				t.Errorf("the failure is %v, want the kind of what failed still findable in it", err)
			}
		})
	}
}

// tokenHost fails every read of a pull request with an error that quotes the
// installation token its source handed it, which is what a host that echoes
// the Authorization header back does.
type tokenHost struct {
	*fake.Provider
	src gitprov.TokenSource
}

func (h tokenHost) PullRequest(ctx context.Context, n int) (gitprov.PRInfo, error) {
	tok, _, err := h.src.Token(ctx, time.Minute)
	if err != nil {
		return gitprov.PRInfo{}, err
	}
	return gitprov.PRInfo{}, fmt.Errorf("GET /repos/acme/web/pulls/%d: 401: bad credentials (Authorization: token %s)", n, tok)
}

// The coordinator's own minted token is a credential too: the opener does not
// hold it (the source mints it on the provider's first call), so the opener's
// secrets alone would let a host's echo of it through.
func TestCoordinatorRedactsItsMintedToken(t *testing.T) {
	const minted = "ghs_minted0123456789abcdef"
	p := &Project{
		Mint: func(context.Context, string) (string, time.Time, error) {
			return minted, time.Now().Add(time.Hour), nil
		},
		Open: func(_ context.Context, repo string, src gitprov.TokenSource) (gitprov.Provider, []string, error) {
			return tokenHost{Provider: &fake.Provider{Repo: repo, SelfID: "1"}, src: src}, nil, nil
		},
		Redact: agent.Redact,
	}
	_, err := p.PullRequests(context.Background(), gitprov.KindGitHub, "acme/web", []int{12})
	if err == nil {
		t.Fatal("the read failed and PullRequests succeeded")
	}
	if strings.Contains(err.Error(), minted) {
		t.Errorf("the minted token reached the failure: %v", err)
	}
}

// The single read keeps the view's own rule: a failure of the host goes through
// Redact with the values the opener held and the token this reader minted, so a
// host that echoes the Authorization header back in its own error body cannot
// put the installation token in front of whoever reads the failure
// (Project.PullRequest).
//
// The opener hands back no secret at all here, so the only place the value
// exists is this reader's cache — the half a redactor built from the opener's
// values alone would miss, and the reason minted() exists.
func TestCoordinatorRedactsAHostTokenOnTheSingleRead(t *testing.T) {
	minted := hostToken(t)
	p := &Project{
		Mint: func(context.Context, string) (string, time.Time, error) {
			return minted, time.Now().Add(time.Hour), nil
		},
		Open: func(_ context.Context, repo string, src gitprov.TokenSource) (gitprov.Provider, []string, error) {
			return tokenHost{Provider: &fake.Provider{Repo: repo, SelfID: "1"}, src: src}, nil, nil
		},
		Redact: agent.Redact,
	}
	doc, err := p.PullRequest(context.Background(), gitprov.KindGitHub, "acme/web", 12)
	if err == nil {
		t.Fatal("the host failed the read and PullRequest succeeded")
	}
	if strings.Contains(err.Error(), minted) {
		t.Errorf("the minted token reached the failure: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("the failure is %v, want the token redacted in it", err)
	}
	if doc != (PRDocument{}) {
		t.Errorf("a failed read returned a document: %+v", doc)
	}
}

// hostToken is the token-shaped value the redaction tests look for, built at
// run time from a prefix and the test's own name (Convention 2: no
// secret-shaped literal, ever). The value a failure must not carry is one this
// test invented rather than a pattern it guessed at, which is the only claim a
// search for it can honestly make.
func hostToken(t *testing.T) string {
	t.Helper()
	return "ghs_" + strings.ReplaceAll(t.Name(), "/", "_")
}

// A reader with no redactor panics rather than reading: a forgotten one would
// ship the provider's own text, which is where a credential would arrive
// (internal/followup's rule for the same reason).
func TestCoordinatorPanicsWithoutARedactor(t *testing.T) {
	p := &Project{
		Mint: func(context.Context, string) (string, time.Time, error) { return "ghs_fixture_token", time.Now(), nil },
		Open: func(context.Context, string, gitprov.TokenSource) (gitprov.Provider, []string, error) {
			return &fake.Provider{Repo: "acme/web", SelfID: "1"}, nil, nil
		},
	}
	defer func() {
		if recover() == nil {
			t.Error("PullRequests read without a redactor, want a panic")
		}
	}()
	if _, err := p.PullRequests(context.Background(), gitprov.KindGitHub, "acme/web", []int{12}); err == nil {
		t.Fatal("the read succeeded, want the panic above")
	}
}

// The numbers asked for are the pull requests returned, in the order they were
// asked for, and no numbers is no call at all: a repository whose runs name no
// pull request is one GitHub is not asked about.
func TestCoordinatorReadsTheNumbersItIsGiven(t *testing.T) {
	f := newProjectFixture("acme/web")
	f.hosts["acme/web"].State.PRs = []fake.PRState{
		prState(12, "topic/twelve", gitprov.PROpen),
		prState(14, "topic/fourteen", gitprov.PRMerged),
	}
	p := f.project()
	if prs, err := p.PullRequests(context.Background(), gitprov.KindGitHub, "acme/web", nil); err != nil || prs != nil {
		t.Fatalf("PullRequests with no numbers = %v, %v; want no call and no answer", prs, err)
	}
	if len(f.opened) != 0 {
		t.Errorf("a repository with no numbers was opened: %v", f.opened)
	}
	prs, err := p.PullRequests(context.Background(), gitprov.KindGitHub, "acme/web", []int{14, 12})
	if err != nil {
		t.Fatal(err)
	}
	var numbers []int
	for _, pr := range prs {
		numbers = append(numbers, pr.Number)
	}
	if !slices.Equal(numbers, []int{14, 12}) {
		t.Errorf("pull requests = %v, want the numbers asked for, in order", numbers)
	}
	if prs[0].State != StateMerged || prs[0].Branch != "topic/fourteen" {
		t.Errorf("pull request 14 = %+v, want its state and source branch", prs[0])
	}
}
