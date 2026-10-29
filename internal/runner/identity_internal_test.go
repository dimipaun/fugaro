package runner

// The identity a run's commits carry: the provider's own bot account when it
// has one (a GitHub App), and gitops.Identity for every other provider and
// for every run whose read of that account fails. The commit itself is
// gitops' half of this (gitops.TestCommitsCarryTheRunsIdentity); what is
// tested here is which identity a run ends up with, and that a run never
// stops over one.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
)

// botAccountProvider is a provider whose bot account is whatever the test
// says: the rest of gitprov.Provider is never reached.
type botAccountProvider struct {
	gitprov.Provider
	account gitprov.BotAccount
	err     error
}

func (p botAccountProvider) BotAccount(context.Context) (gitprov.BotAccount, error) {
	return p.account, p.err
}

// TestCommitIdentityIsTheProvidersBotAccount: a github run's commits carry
// the App's bot account -- read from the provider at open, before anything
// commits -- so GitHub links them to that account rather than showing an
// author no account owns.
//
// The checkout is opened here with the default identity, which is the order
// of the provider fugaro.yaml names: it is opened after the checkout exists,
// so the identity has to reach the environment that checkout's git calls
// already carry.
//
// Mutation (leave r.identity nil, or set it only on the checkout path): this
// test fails, on the identity or on the environment.
func TestCommitIdentityIsTheProvidersBotAccount(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	svc := newGitServiceFake(t, now)
	r := gitServiceRun(t, svc, gitMetadataFake(t, gitServiceIDToken), gitRunIdentity(t), func(r *run) {
		r.repo = &gitops.Repo{Dir: t.TempDir(), Env: gitops.IdentityEnv()}
	})
	r.d.Now = func() time.Time { return now }

	if err := r.openProvider(context.Background(), gitprov.KindGitHub, "the --provider flag"); err != nil {
		t.Fatalf("opening the github provider: %v", err)
	}
	want := gitops.BotIdentity(gitServiceBotID, gitServiceBotLogin)
	if got := gitops.IdentityFor(r.identity); !maps.Equal(got, want) {
		t.Fatalf("the run's identity is %v, want the App's bot account %v", got, want)
	}
	for _, kv := range gitops.IdentityEnvFor(want) {
		if !slices.Contains(r.repo.Env, kv) {
			t.Errorf("the open checkout's environment does not carry %q", kv)
		}
	}
}

// TestCommitIdentityKeepsTheDefaultWhenTheBotCannotBeRead: a read that fails
// costs the run its bot identity and nothing else -- the commits keep
// gitops.Identity, and one line in the log says so, with the reason. A run
// must not stop, and must not guess, over an identity.
//
// Mutation (fail the run, or commit under an identity the read did not give):
// this test fails on the identity or on the log.
func TestCommitIdentityKeepsTheDefaultWhenTheBotCannotBeRead(t *testing.T) {
	var logs bytes.Buffer
	r := svcRun(t, "api-key", func(r *run) {
		r.d.Log = slog.New(slog.NewTextHandler(&logs, nil))
	})
	r.provider = botAccountProvider{err: errors.New("reading the App's bot account (GET /user): GET /user: HTTP 403")}
	r.providerKind = gitprov.KindGitHub

	r.resolveCommitIdentity(context.Background())

	if r.identity != nil {
		t.Errorf("the run took an identity from a failed read: %v", r.identity)
	}
	if got := gitops.IdentityFor(r.identity); !maps.Equal(got, gitops.Identity) {
		t.Errorf("the run's commits would carry %v, want the default identity", got)
	}
	got := logs.String()
	if n := strings.Count(got, "level=WARN"); n != 1 {
		t.Errorf("the failed read wrote %d log lines, want exactly 1:\n%s", n, got)
	}
	if !strings.Contains(got, "403") {
		t.Errorf("the log line does not say why the read failed:\n%s", got)
	}
}

// TestCommitIdentityUntouchedForAProviderWithoutABotAccount: a provider that
// names no account of its own is not asked for one and the run keeps
// gitops.Identity -- the file-backed provider every other test of this
// package runs on, and Bitbucket's, whose commits are the run's own. The
// digest run's own test pins the same thing from the other end: the report it
// commits carries "Fugaro <fugaro@users.noreply.invalid>".
//
// Mutation (read an account from every provider): this test fails, on the
// fake provider's identity or on the log.
func TestCommitIdentityUntouchedForAProviderWithoutABotAccount(t *testing.T) {
	var logs bytes.Buffer
	r := svcRun(t, "api-key", func(r *run) {
		r.d.Log = slog.New(slog.NewTextHandler(&logs, nil))
	})
	r.provider, r.providerKind = &fake.Provider{}, gitprov.KindBitbucket

	r.resolveCommitIdentity(context.Background())

	if r.identity != nil {
		t.Errorf("a provider with no bot account gave the run an identity: %v", r.identity)
	}
	if logs.Len() != 0 {
		t.Errorf("a provider with no bot account was logged about:\n%s", logs.String())
	}
}
