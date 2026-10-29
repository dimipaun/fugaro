package watch

import (
	"context"
	"errors"
)

// MergeNow asks gh, now, whether one pull request URL is merged.
//
// PRState answers that question for a screen: Filter returns the rows to draw
// and starts the lookups off the render loop, and the answer lands in the cache
// for the next frame. `fugaro plan status` cannot work that way — its document
// is finished before it is printed, and a task whose pull request is merged is
// done (GV1.6) — so it needs the same question asked synchronously. This is that
// method, and it lives here rather than in the CLI on purpose: the URL is
// matched against this package's own anchored ghPRURL before anything is
// spawned, the argv is built from the parsed owner, repository and number with
// the host pinned, and the process group, the capped pipes and the fixed
// environment are the ones ask already uses. A second spawn path in a caller
// would be a second answer to "which URLs may be handed to a subprocess".
//
// It answers (false, false) for everything it cannot tell: no gh, a URL that is
// not a GitHub pull request URL, a lookup that failed or timed out, and a
// PRState that is nil or disabled. That is the same posture as the screen's
// lookups — nothing here fails because gh did not answer — and the caller reads
// it as "merge unknown", never as "not merged".
//
// The cache is read, never written. A merged or closed answer already in hand is
// that answer, and a fresh open one is open; a lookup this call makes is
// returned to the caller and not remembered, because its caller asks each URL
// once and the cache's own bookkeeping (the failure note, the backoff) is
// Filter's. The one thing written is noGH, which says gh will not appear while
// this process lives: a caller asking about several URLs then spawns LookPath
// once rather than once per URL.
func (p *PRState) MergeNow(ctx context.Context, url string) (merged, known bool) {
	if p == nil || p.disabled {
		return false, false
	}
	ref, ok := parseGHPRURL(url)
	if !ok {
		return false, false
	}

	p.mu.Lock()
	if a, ok := p.seen[ref.url]; ok && (a.state.gone() || p.now().Sub(a.at) < p.ttl(a)) {
		p.mu.Unlock()
		return a.state == prMerged, a.state != prUnknown
	}
	// A URL another lookup holds, and a gh already known to be missing, are both
	// "no answer" rather than a second question.
	if p.noGH || p.busy[ref.url] {
		p.mu.Unlock()
		return false, false
	}
	p.busy[ref.url] = true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.busy, ref.url)
		p.mu.Unlock()
	}()

	// The caller's context is bounded here too: a plan status read that is
	// cancelled (a Ctrl-C, a console that has gone away) must not wait out one
	// lookup per pull request.
	cctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	state, err := p.askFn(cctx, ref)
	if err != nil {
		if errors.Is(err, errGHNotFound) {
			p.mu.Lock()
			p.noGH = true
			p.mu.Unlock()
		}
		return false, false
	}
	return state == prMerged, state != prUnknown
}

// PRRepo is the owner/name a GitHub pull request URL names, read with the same
// anchored pattern MergeNow matches before it spawns anything; ok is false for
// every URL MergeNow would not ask about. A caller that must not let a job point
// at somebody else's pull request compares this with its own repository before
// it asks.
func PRRepo(url string) (repo string, ok bool) {
	ref, ok := parseGHPRURL(url)
	if !ok {
		return "", false
	}
	return ref.owner + "/" + ref.repo, true
}
