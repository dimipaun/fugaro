// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// This file is P2 of docs/coordinator/implementation-plan.md: the
// project-wide read. P1 computed one repository's view from the pull requests
// its runs name; P2 reads those pull requests through a credential of the
// coordinator's own — a read-only installation token per repository, minted
// once and cached for the token's life (D193's answer of 2026-10-21) — and
// refuses the provider whose credential cannot be widened to a project.

// Reader is the project-wide read, behind one interface: one repository's
// pull requests, by the numbers the caller names.
//
// The command is written against this shape rather than against the type
// below, because D193 is a decision about how far a credential reaches and
// not about what a read is: a project-wide token would change the
// implementation and not this signature, and so would a reader that listed a
// repository's pull requests instead of being told their numbers (which
// gitprov has no call for today, reconciliation finding #10).
type Reader interface {
	PullRequests(ctx context.Context, kind, repo string, numbers []int) ([]PullRequest, error)
	// PullRequest reads one pull request whole, for the caller that needs the
	// description the view drops (PRDocument, D816). It is a second method and
	// not a wider PullRequests, because the two answer different questions:
	// the view is a join over many pull requests and keeps none of their
	// bodies, and a read that carried one would put unbounded text written by
	// whoever opened the pull request into the document every reader of the
	// view fetches.
	PullRequest(ctx context.Context, kind, repo string, number int) (PRDocument, error)
}

// ErrBitbucket is the project-wide read's refusal of a Bitbucket repository
// (reconciliation Q12, answered (A) on 2026-10-19; finding #30): its
// credential is one repository's access token and there is nothing to widen
// it to, so the coordinator reads GitHub only in v1.
//
// The refusal names the reason and never the repository: which repositories a
// project holds is a fact about the project, and a caller reports it as what
// it is.
var ErrBitbucket = errors.New("the project-wide view reads github only (docs/coordinator/reconciliation.md, Q12 A, finding #30): a bitbucket credential is one repository's access token and cannot be widened to a project")

// TokenMinter mints the coordinator's installation token for one repository:
// the coordinator's own read-only permission set, minted through
// github.MintInstallationToken (github.CoordinatorTokenPermissions, P2).
type TokenMinter func(ctx context.Context, repo string) (string, time.Time, error)

// ProviderOpener opens repo's git provider, with src installed as the source
// of every installation token it uses, and returns the credential values it
// holds — which the reader redacts out of whatever it reports. It is
// gitprov.Opener's shape, and for the same reason: the opener is the only one
// that knows which values are credentials.
//
// A provider that cannot be given a source is a refusal, never a fallback. A
// provider that minted on its own would ask GitHub for the run's set, which
// holds two writes, and this read must not be able to reach one (Q3's
// answer: the credential is a named read-only set, and no `write` anywhere).
type ProviderOpener func(ctx context.Context, repo string, src gitprov.TokenSource) (gitprov.Provider, []string, error)

// Project is the project-wide read: each repository's pull requests read
// through a provider opened on a read-only installation token of its own,
// minted once per repository and cached for the token's life (D193(A)).
//
// The project's repositories are named by the caller — the CLI's local config
// today, the aggregation job's own list when P5 builds it — because which
// repositories a project holds belongs to the config layer and not to the
// view. This reader serves one repository at a time and never decides which
// ones matter.
type Project struct {
	// Mint mints repo's read-only installation token. Required.
	Mint TokenMinter
	// Open opens repo's provider with the token source installed. Required.
	Open ProviderOpener
	// Redact removes the credential values an Opener returned from text
	// before that text is reported in an error (agent.Redact). Required, and
	// a missing one panics rather than being skipped: a provider's own error
	// text is another writer's, and a failure that carried a credential is
	// what a redactor exists to prevent (internal/followup's rule, for the
	// same reason).
	Redact func(text string, secrets []string) string
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu     sync.Mutex
	tokens map[string]token
}

// token is one repository's cached installation token and its expiry.
type token struct {
	value   string
	expires time.Time
}

// ProjectView is the project's view: every repository the caller read, each
// with its own records and its own collisions, in the order they were read.
//
// Nothing here compares across repositories. A collision is two pieces of
// work in flight touching one file or one service, and a service is a path's
// shape in the one tree the work is in (ServiceOf): two repositories' paths
// say nothing about each other, and a project-wide collision would be a
// coincidence of names rather than news.
type ProjectView struct {
	Repos []View `json:"repos"`
}

func (p *Project) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// PullRequests reads repo's pull requests numbered numbers, in the order
// asked for, through a provider opened on the coordinator's own credential.
//
// Four rules, because each one is a decision a reader would otherwise guess
// at:
//
//   - A kind this read does not serve is refused before anything is minted,
//     opened or read (ErrBitbucket). The refusal is about what a project may
//     hold and not about whether it has work in flight, so a repository with
//     nothing running is refused the same way — and a caller may therefore
//     ask about every repository it knows.
//   - No numbers is no call at all: a repository none of whose runs names a
//     pull request is one GitHub is not asked about.
//   - A read that fails fails the whole call. The pull requests a repository's
//     runs name are the preset of one view, and a view missing the ones the
//     host would not answer for is a partial view presented as a whole one.
//     Each answer is written to its own slot and the failures are read back in
//     number order, so the failure is the first number that failed whatever
//     finished first, and the slice is in the order asked for however the
//     goroutines interleave (P1's own rule, kept here: pullWorkers at a time).
//     The failure is a *PartialError, which still carries what was read and
//     which numbers were not, for the caller that records them one by one.
//   - Nothing this returns names a credential: the provider's own error text
//     goes through Redact with the values its Opener held and the token this
//     reader minted for the repository.
//
// What it reports also keeps the kind of the failure it rewrote (redactedError):
// the text is this reader's, but whether a failure is the configuration's or the
// host's is the caller's to decide, and a caller that could only see the text
// would have to match on it.
func (p *Project) PullRequests(ctx context.Context, kind, repo string, numbers []int) ([]PullRequest, error) {
	if err := supported(kind); err != nil {
		return nil, err
	}
	if len(numbers) == 0 {
		return nil, nil
	}
	mustRedact(p.Redact)
	prov, secrets, err := p.Open(ctx, repo, p.sourceFor(repo))
	if err != nil {
		return nil, redactedError{err: err, text: p.Redact(err.Error(), secrets)}
	}
	out := make([]PullRequest, len(numbers))
	errs := make([]error, len(numbers))
	sem := make(chan struct{}, min(pullWorkers, len(numbers)))
	var wg sync.WaitGroup
	for i, n := range numbers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			info, err := prov.PullRequest(ctx, n)
			if err != nil {
				errs[i] = err
				return
			}
			out[i] = PullRequest{
				Number: info.Number,
				URL:    info.URL,
				Title:  info.Title,
				Branch: info.SourceBranch,
				Draft:  info.Draft,
				State:  string(info.State),
			}
		}()
	}
	wg.Wait()
	var partial *PartialError
	for i, err := range errs {
		if err == nil {
			continue
		}
		if partial == nil {
			secrets = append(secrets, p.minted(repo)...)
			partial = &PartialError{err: redactedError{err: err,
				text: fmt.Sprintf("reading pull request %d of %s: %s", numbers[i], repo, p.Redact(err.Error(), secrets))}}
		}
		partial.Failed = append(partial.Failed, numbers[i])
	}
	if partial != nil {
		for i, err := range errs {
			if err == nil {
				partial.Read = append(partial.Read, out[i])
			}
		}
		return nil, partial
	}
	return out, nil
}

// PullRequest reads one pull request as the host has it, whole, through the
// same credential and the same rules as PullRequests: the coordinator's own
// read-only installation token for the repository, minted once and cached for
// its life, and a failure whose text goes through Redact with the values its
// Opener held and the token this reader minted.
//
// The refusal of a kind this read does not serve comes first, before anything
// is minted or opened (ErrBitbucket): a repository on a host the coordinator
// cannot read is refused the same way whether one pull request or a whole view
// was asked for, and the refusal is about what the project holds rather than
// about what is in it.
//
// A pull request the host does not have is the host's own failure, and it
// travels as one: what it carries is the provider's error, redacted, and the
// caller classifies it exactly as it classifies the same failure from
// PullRequests. Nothing here turns it into an empty document, which would say
// a pull request exists with no description.
func (p *Project) PullRequest(ctx context.Context, kind, repo string, number int) (PRDocument, error) {
	if err := supported(kind); err != nil {
		return PRDocument{}, err
	}
	mustRedact(p.Redact)
	prov, secrets, err := p.Open(ctx, repo, p.sourceFor(repo))
	if err != nil {
		return PRDocument{}, redactedError{err: err, text: p.Redact(err.Error(), secrets)}
	}
	info, err := prov.PullRequest(ctx, number)
	if err != nil {
		secrets = append(secrets, p.minted(repo)...)
		return PRDocument{}, redactedError{err: err,
			text: fmt.Sprintf("reading pull request %d of %s: %s", number, repo, p.Redact(err.Error(), secrets))}
	}
	return PRDocument{
		Number: info.Number,
		URL:    info.URL,
		Title:  info.Title,
		Body:   info.Body,
		State:  string(info.State),
		Draft:  info.Draft,
		Branch: info.SourceBranch,
		Base:   info.Base,
		Author: info.AuthorID,
	}, nil
}

// PartialError is PullRequests' failure once the provider was opened: the
// numbers that could not be read, and the pull requests that were. Its text is
// the first failure's, redacted, and Unwrap reaches that failure's kind.
//
// A caller that renders one view (status) reads it as the failure it is, which
// is the all-or-nothing rule above. The sweep reads it number by number
// instead: the numbers a repository's runs name are written by those runs,
// from inside their containers, so one number the host refuses is something a
// single run can put there, and a repository whose whole view failed on it
// would be a repository one run can keep stale forever.
type PartialError struct {
	// Read is the pull requests that were read, in the order asked for, and
	// Failed the numbers that were not, in the same order.
	Read   []PullRequest
	Failed []int
	err    error
}

func (e *PartialError) Error() string { return e.err.Error() }
func (e *PartialError) Unwrap() error { return e.err }

// minted is the installation token this reader holds for repo, if any. The
// opener's secrets do not include it — the source mints it on the provider's
// first call, after Open returned — and a host that echoes its credential back
// in a failure would otherwise put it in front of whoever reads the failure.
func (p *Project) minted(repo string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.tokens[repo]; ok {
		return []string{t.value}
	}
	return nil
}

// pullWorkers bounds the pull-request reads of one repository that are in
// flight at once. The reader this one replaced read them at this width — P1's
// own review asked for it, and P1's built note records it
// (docs/coordinator/implementation-plan.md) — and the reason is the same here.
// The numbers are a handful and each read is one round trip, so the bound is
// about what the host and the installation token will take rather than about
// this process: it is small because the same App credential mints the token
// behind every one of them.
//
// Both adapters are safe to call this way: the GitHub one serialises its token
// and identity lookups and the Bitbucket one its identity lookup, and both read
// through one http.Client.
const pullWorkers = 4

// sourceFor is the token source of one repository, which Open installs: every
// call through it is served from the cache while that repository's token is
// still valid for the validity asked for, and mints otherwise.
func (p *Project) sourceFor(repo string) gitprov.TokenSource {
	return repoSource{p: p, repo: repo}
}

// repoSource is one repository's token source: the house token-source shape
// (gitprov.TokenSource, the same contract tokenSource and ServiceTokens
// keep), so a provider is opened with its tokens coming from here without
// knowing where they come from.
type repoSource struct {
	p    *Project
	repo string
}

// Token returns an installation token for this source's repository, valid for
// at least minValid, minting one when the cached token would expire sooner —
// the same rule, and the same cache, as the adapter's own tokenSource.
//
// The key of the cache is the repository, and its lifetime is the token's:
// D193's answer is one token per repository, so two repositories hold two
// tokens and neither is reused for the other. The check-then-mint runs under
// the lock, as tokenSource's does, so one repository mints once however many
// callers reach it at once.
func (s repoSource) Token(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	p := s.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.tokens[s.repo]; ok && t.expires.Sub(p.now()) >= minValid {
		return t.value, t.expires, nil
	}
	value, expires, err := p.Mint(ctx, s.repo)
	if err != nil {
		return "", time.Time{}, err
	}
	if value == "" {
		// An empty token is no token: a provider handed one would fail every
		// call as an authentication error, and the cache must not hold it as
		// if it were one.
		return "", time.Time{}, errors.New("the coordinator's mint returned no installation token")
	}
	if p.tokens == nil {
		p.tokens = map[string]token{}
	}
	p.tokens[s.repo] = token{value: value, expires: expires}
	return value, expires, nil
}

// supported reports whether the project-wide read serves providers of kind.
func supported(kind string) error {
	switch kind {
	case gitprov.KindGitHub:
		return nil
	case gitprov.KindBitbucket:
		return ErrBitbucket
	}
	return fmt.Errorf("the project-wide view reads %s only: %q is not a git provider kind this repository knows (%s or %s)",
		gitprov.KindGitHub, kind, gitprov.KindGitHub, gitprov.KindBitbucket)
}

// redactedError is a failure whose text has had a credential removed and whose
// kind is still there: Unwrap is the original error, so a caller above finds
// what failed — a refusal of the credential the environment holds is the user's
// own and not the host's, and this reader is not where that is decided
// (internal/cli's redactedError, the same shape, for the same reason).
type redactedError struct {
	err  error
	text string
}

func (e redactedError) Error() string { return e.text }
func (e redactedError) Unwrap() error { return e.err }

// mustRedact panics when the redactor is missing: a forgotten one would ship
// the provider's own text — which is where a credential would arrive — to
// whoever reads the failure.
func mustRedact(redact func(string, []string) string) {
	if redact == nil {
		panic("coordinator: Project.Redact is required; build it with agent.Redact")
	}
}
