// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// This file is P5 of docs/coordinator/implementation-plan.md: the
// aggregation job's own logic — the recompute, the stale marker and the write
// set. The job around it (a scheduled Cloud Run job in the installation root,
// on the pattern deploy/terraform/gcp/modules/installation/history.tf runs)
// only carries the environment and the entry point; everything that decides
// what is written lives here, where it can be tested without a cloud.
//
// What it does, once per repository: read the runs the bucket holds and the
// pull requests those runs name (P2's reader), join them (P1's Join), write
// the view into P3's node, and keep the intent records of that repository
// bounded. It is the part that makes P4's records readable rather than merely
// writable.
//
// Three properties are the part rather than niceties around it:
//
//   - The document is a FUNCTION of its inputs. Nothing in what it writes
//     comes from the clock, so a sweep over an unchanged GitHub and unchanged
//     runs writes the same bytes, and the database's own ETag — a hash of the
//     node's content — does not move. A view stamped with the time it was
//     computed would be a new generation every tick, and a reader caching on
//     the ETag (which is what the CAS of Q6(A) is for) would re-read a
//     document that had not changed. Sweeper.Now is used only for facts about
//     a sweep and not about a view: since when a repository has been stale,
//     how far back its runs are read (RunsWindow), and how old an intent
//     record may grow (DefaultIntentMaxAge).
//   - What it could not read is RECORDED, never guessed at. A repository
//     whose runs could not be read, or whose host could not be opened, keeps
//     its previous view and gets a marker beside it (Stale) rather than a view
//     computed from half its inputs: a view missing the pull requests the host
//     would not answer for reads as "nothing is in flight" about a repository
//     that has work in it, which is the one outcome this must not produce. One
//     pull request the host would not answer for is recorded on its own in
//     the view (Unread), with the state the last sweep read for it, and the
//     others are read: the numbers are written by the runs themselves, and a
//     repository one bad number could keep stale is a repository one run
//     could keep stale. Every reason is a classification of this package's
//     own and never the host's text — a failure's own message is another
//     writer's, and Q14(A) is why it is read as data and not stored as a
//     sentence.
//   - It is BOUNDED, per tick and per document. A tick reads the runs of the
//     last RunsWindow, reads at most DefaultReadBudget pull requests from the
//     host (a merged pull request is not read again: the state is final), and
//     starts where the last tick stopped (the cursor), so a project with more
//     work than one tick can read is read in turns rather than always from its
//     first repository. The view holds at most MaxViewRecords records and
//     MaxViewCollisions collisions, and the intent records are bounded by
//     count and by age.
//   - It takes nothing back and advises nobody. There is no comparison
//     against what is in flight, no advice and no model: the sweep writes the
//     view and the marker, and that is the whole of its output (spec §3,
//     Q10(C) refused).
//
// Its account may write the whole database (roles/firebasedatabase.admin, D194
// — IAM cannot express a path, so the rules cannot fence it and neither can a
// role). What keeps it to its own nodes is Store's own check, which every
// write here goes through: the sweep has no other way to reach the database,
// and everything it writes is built from coordpath.PathRoot's paths.

// DefaultIntents is how many intent records one repository keeps, oldest
// first out (Sweeper.Keep). A declaration is about work about to start, so
// the records that matter are the newest ones, and a repository that has run
// more than this many since the last sweep has a view to read rather than a
// log to keep. It is the shape D197 gives the override log — the last 200 per
// project — applied to a repository's own node.
const DefaultIntents = 200

// DefaultIntentMaxAge is how old an intent record may grow before the sweep
// removes it whatever the count (Sweeper.MaxAge). It is RunsWindow: a
// declaration older than every run the view is computed from describes work
// the view can no longer say anything about.
const DefaultIntentMaxAge = RunsWindow

// intentClockSkew is how far ahead of the sweep's clock a record's At may be
// and still be believed. At is the reporter's own clock, and a reporter that
// stamped a record in the future would otherwise make it the newest record
// for as long as it liked — never pruned by count and never by age. A record
// further ahead than this is ordered as the oldest there is instead.
const intentClockSkew = 5 * time.Minute

// MaxUndecodablePrunes is how many undecodable intent records the sweep
// removes from one repository in one tick. Such a record has no age and no
// place in the order, so neither bound reaches it; removing it is a write
// per record, and this keeps a node full of them from spending a whole tick's
// writes on one repository. The next tick takes the rest.
const MaxUndecodablePrunes = 20

// RunsWindow is how far back one tick reads a repository's runs. A view is
// computed from the runs of this window and the pull requests they name, so
// a pull request whose newest run is older than this leaves the view: it is
// history by then, and the bound is what keeps a tick's reads proportional to
// recent work rather than to everything a repository ever ran.
const RunsWindow = 30 * 24 * time.Hour

// DefaultReadBudget is how many pull requests one tick reads from the host,
// across every repository (Sweeper.Budget). The coordinator's token is minted
// from the GitHub App the runs themselves use, and the App's rate limit is
// one budget for both: at four ticks an hour this is 800 reads an hour at
// most, a sixth of the 5000 an installation token is allowed, so a sweep can
// never be the reason a run's own calls are refused.
const DefaultReadBudget = 200

// MaxViewRecords and MaxViewCollisions bound one view document. A document a
// reader fetches whole must not grow with what the runs write, and the runs
// write the numbers it is computed from. What the bound drops is said in the
// document (View.Omitted, View.OmittedCollisions).
const (
	MaxViewRecords    = 500
	MaxViewCollisions = 500
)

// finalWriteGrace is how long the writes that close a tick — a stale marker
// and the cursor — may take after the tick's own deadline has passed. Without
// it, the tick that ran out of time is the one tick that could not say so: its
// context is already done when the marker is written. It is well inside the
// job's own timeout (600s against the command's 8-minute bound).
const finalWriteGrace = 30 * time.Second

// Repo is one repository the sweep reconciles: the git provider kind it is
// on, the name the host spells it with, and the key its node sits under.
//
// The slug is the caller's and not derived here, because it is not a function
// of the name alone: task.Slug hashes the provider with it, so the caller
// that knows the provider knows the slug, and a second derivation here would
// be a second thing to keep in step with the tree the runs are stored in.
type Repo struct {
	// Provider is the git provider kind ("github", "bitbucket"), as
	// gitprov spells them.
	Provider string `json:"provider"`
	// Name is the repository as the host spells it, "owner/name".
	Name string `json:"repo"`
	// Slug is the key of its node (task.Slug).
	Slug string `json:"slug"`
}

// RunsReader reads one repository's runs minted at or after since, as the view
// holds them (Run). It is the runs bucket's shape behind one method, so the
// sweep reads a repository's runs the way the CLI's own commands do without
// this package opening a bucket.
type RunsReader interface {
	Runs(ctx context.Context, repo Repo, since time.Time) ([]Run, error)
}

// Stale is the marker the sweep writes when it could not reconcile a
// repository: since when, and why — the view beside it is the last one that
// could be computed, and it is left exactly as it was.
//
// The reason is one of the constants below and never the failing read's own
// message: that message is another writer's text (GitHub's, the bucket's), and
// what a reader of the coordinator's state needs from it is which read failed
// rather than what it said (Q14 A).
type Stale struct {
	// Since is when the repository first failed to reconcile, in UTC, and it
	// stays put while the failures continue: a marker that restamped itself
	// every tick would be a document that changes on every sweep that changed
	// nothing, which is what a reader caching on the ETag would then re-read.
	Since time.Time `json:"since"`
	// Reason is which read failed, as this package's own classification.
	Reason string `json:"reason"`
}

// The marker's reasons: one per way a repository could not be reconciled.
const (
	// StaleRuns is the runs bucket's: the view's other half.
	StaleRuns = "the repository's runs could not be read"
	// StalePullRequests is the host's, for the repository as a whole: an
	// outage, a credential the environment does not hold, or a host the
	// coordinator's token could not be opened against. One pull request the
	// host refused is not this (UnreadFailed).
	StalePullRequests = "the repository's pull requests could not be read"
	// StaleUnsupported is the configuration's: the project-wide read serves
	// github only (Q12 A, finding #30), so a repository on another provider
	// will not reconcile however often it is swept. It is recorded rather
	// than failing the sweep, because a project that holds one such
	// repository still has every other one's view to keep true.
	StaleUnsupported = "the project-wide read serves github only, so this repository was not read"
	// StaleDeadline is the tick's own: it ran out of time while this
	// repository was being read, and the next tick starts after it.
	StaleDeadline = "the tick ran out of time while this repository was read"
)

// Unread is one pull request a view could not read on this pass: its number,
// why, and whether the state the view shows for it is the last one a sweep did
// read (Carried) rather than none at all.
//
// Failures and RetryAfter are the view's memory of a number the host keeps
// refusing. Such a number is never read, so nothing else would stop the next
// tick asking for it again — first, because a number no sweep has read sorts
// ahead of the rest — and a run that wrote a bogus one would spend its
// repository's share of the read budget on every tick. Failures counts the
// consecutive ticks the host refused it, up to MaxReadFailures; once it gets
// there RetryAfter is when it is asked for again, and until then it is carried
// as unread without a read.
type Unread struct {
	Number     int       `json:"number"`
	Reason     string    `json:"reason"`
	Carried    bool      `json:"carried,omitempty"`
	Failures   int       `json:"failures,omitempty"`
	RetryAfter time.Time `json:"retry_after,omitzero"`
}

// MaxReadFailures is how many consecutive ticks the host may refuse one pull
// request before the sweep stops asking for it every tick, and ReadRetryAfter
// how long it then waits before asking once more. Three refusals is past a
// transient error on any one tick; a day is how long a number that is really
// a pull request (one the token cannot read yet, say) waits to be seen.
const (
	MaxReadFailures = 3
	ReadRetryAfter  = 24 * time.Hour
)

// Unread's reasons.
const (
	// UnreadFailed is the host's refusal of one pull request: a number no
	// pull request has, one the token may not read, or a read that failed on
	// its own.
	UnreadFailed = "the host did not answer for this pull request"
	// UnreadDeferred is the tick's read budget, spent before this pull request
	// was reached: the newest ones are read first, so the deferred ones are
	// the oldest the repository's runs name.
	UnreadDeferred = "the tick's read budget was spent before this pull request was read"
)

// Why a tick stopped before reaching every repository (Result.Stopped).
const (
	StoppedDeadline = "the tick's deadline"
	StoppedBudget   = "the tick's read budget"
)

// RepoResult is what one repository's sweep did, and Result is the whole
// tick's. They are the job's log line and the manual invocation's output: a
// reader of a failed tick needs to know which repositories were reconciled
// and which were left stale, and a count of records is how a person sees that
// a view is not empty.
type RepoResult struct {
	Repo string `json:"repo"`
	Slug string `json:"slug"`
	// Records, InFlight and Collisions are the view that was written. They
	// are zero for a repository that was left stale, whose view is the
	// previous sweep's.
	Records    int `json:"records"`
	InFlight   int `json:"in_flight"`
	Collisions int `json:"collisions"`
	// Read is how many pull requests this repository's sweep asked the host
	// for, and Unread how many the view records it could not read
	// (View.Unread).
	Read   int `json:"read"`
	Unread int `json:"unread"`
	// Omitted is how many records the view's bound left out.
	Omitted int `json:"omitted,omitempty"`
	// Stale is whether this sweep could not reconcile the repository, and
	// Reason is why ("" when it did).
	Stale  bool   `json:"stale"`
	Reason string `json:"reason,omitempty"`
	// Pruned is how many intent records this sweep removed.
	Pruned int `json:"pruned"`
	// Undecodable is how many documents of this repository's node the sweep
	// could not decode: intent records (which it also removes, counted in
	// Pruned), the stale marker and the previous view. None of them stops
	// the tick.
	Undecodable int `json:"undecodable,omitempty"`
}

// Result is one tick of the sweep, in the order the repositories were swept:
// from the cursor's repository on, wrapping round.
type Result struct {
	Repos []RepoResult `json:"repos"`
	// Stopped is why the tick stopped before reaching every repository, ""
	// when it reached them all.
	Stopped string `json:"stopped,omitempty"`
	// Next is the slug of the repository the next tick starts with.
	Next string `json:"next,omitempty"`
}

// StaleRepos is how many of the tick's repositories could not be reconciled.
func (r Result) StaleRepos() int {
	n := 0
	for _, repo := range r.Repos {
		if repo.Stale {
			n++
		}
	}
	return n
}

// PathSweepCursor is the sweep's own cursor: the slug of the repository the
// next tick starts with (SweepCursor). It is one small document whatever the
// project holds, and it lives in the coordinator's subtree like everything
// else the sweep writes.
const PathSweepCursor = coordpath.PathProject + "/sweep_cursor"

// SweepCursor is the document at PathSweepCursor.
type SweepCursor struct {
	Next string `json:"next"`
}

// Sweeper reconciles the coordinator's documents with what GitHub and the
// runs bucket say. Its whole dependency list is the two reads, the store it
// writes through and the clock: no credential of its own, no service, no
// model, and no advice.
type Sweeper struct {
	// Reader is P2's project-wide read: the pull requests a repository's runs
	// name, through the coordinator's own read-only installation token.
	// Required.
	Reader Reader
	// Runs reads one repository's runs. Required.
	Runs RunsReader
	// Store is the coordinator's own state, and the only way this writes.
	// Required.
	Store *Store
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Keep is how many intent records one repository keeps; 0 means
	// DefaultIntents. A negative value keeps every record.
	Keep int
	// MaxAge is how old an intent record may grow; 0 means
	// DefaultIntentMaxAge. A negative value bounds nothing by age.
	MaxAge time.Duration
	// Budget is how many pull requests one tick reads; 0 means
	// DefaultReadBudget.
	Budget int
}

// Sweep reconciles the repositories in repos, one after another, starting
// with the one the cursor names, and returns what it did.
//
// The repositories are independent: one that could not be reconciled is
// marked stale and the next one is swept anyway. The tick stops early on two
// things, and the cursor then names the repository the next tick starts
// with, so a project whose work is more than one tick can read is read in
// turns rather than always from the same first repository: its deadline (the
// repository being read is marked StaleDeadline, and the next tick starts
// after it), and its read budget (the next tick starts with the repository
// this one did not reach). A tick that reached every repository moves the
// cursor one on, so no repository is always the first.
//
// A failure of the store itself is the one thing that stops the tick with an
// error — it is the sweep's own failure rather than a repository's, it would
// fail for every repository after it, and the job's exit code is where an
// operator reads it.
func (s *Sweeper) Sweep(ctx context.Context, repos []Repo) (Result, error) {
	if s.Reader == nil || s.Runs == nil || s.Store == nil {
		panic("coordinator: Sweeper needs a Reader, a Runs source and a Store; build it with NewStore")
	}
	res := Result{Repos: make([]RepoResult, 0, len(repos))}
	if len(repos) == 0 {
		return res, nil
	}
	start, err := s.cursor(ctx, repos)
	if err != nil {
		return res, err
	}
	budget := s.Budget
	if budget <= 0 {
		budget = DefaultReadBudget
	}
	next := (start + 1) % len(repos)
	for k := range len(repos) {
		i := (start + k) % len(repos)
		if ctx.Err() != nil {
			res.Stopped, next = StoppedDeadline, i
			break
		}
		if budget == 0 {
			res.Stopped, next = StoppedBudget, i
			break
		}
		row, err := s.one(ctx, repos[i], &budget)
		res.Repos = append(res.Repos, row)
		if err != nil {
			return res, err
		}
		if row.Reason == StaleDeadline {
			res.Stopped, next = StoppedDeadline, (i+1)%len(repos)
			break
		}
	}
	res.Next = repos[next].Slug
	wctx, cancel := closing(ctx)
	defer cancel()
	return res, s.Store.Write(wctx, PathSweepCursor, SweepCursor{Next: res.Next})
}

// cursor is the index in repos of the repository this tick starts with: the
// one the cursor document names, and the first when it names none of them —
// a repository that left the list, a cursor never written, or one that does
// not decode. None of those stops the tick.
func (s *Sweeper) cursor(ctx context.Context, repos []Repo) (int, error) {
	var raw json.RawMessage
	_, found, err := s.Store.Read(ctx, PathSweepCursor, &raw)
	if err != nil || !found {
		return 0, err
	}
	var c SweepCursor
	if json.Unmarshal(raw, &c) != nil {
		return 0, nil
	}
	if i := slices.IndexFunc(repos, func(r Repo) bool { return r.Slug == c.Next }); i >= 0 {
		return i, nil
	}
	return 0, nil
}

// closing is the context the writes that close a tick run under: ctx's values
// without its deadline, and finalWriteGrace of its own.
func closing(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), finalWriteGrace)
}

// one reconciles one repository: the recompute, the write, and the records it
// keeps. budget is the tick's remaining read budget, which this spends.
func (s *Sweeper) one(ctx context.Context, repo Repo, budget *int) (RepoResult, error) {
	row := RepoResult{Repo: repo.Name, Slug: repo.Slug}
	view, reason, err := s.recompute(ctx, repo, budget, &row)
	if err != nil {
		return s.storeFailed(ctx, repo, row, err)
	}
	if reason != "" {
		return s.stale(ctx, repo, row, reason)
	}
	if err := s.Store.Write(ctx, PathView(repo.Slug), view); err != nil {
		return s.storeFailed(ctx, repo, row, err)
	}
	if err := s.clearStale(ctx, repo); err != nil {
		return s.storeFailed(ctx, repo, row, err)
	}
	row.Records, row.InFlight, row.Collisions = len(view.Records), view.InFlight(), len(view.Collisions)
	row.Unread, row.Omitted = len(view.Unread), view.Omitted
	return s.pruned(ctx, repo, row)
}

// storeFailed is one repository's store failure: the tick's own error,
// unless it is the tick's deadline that failed it, which is a repository the
// tick ran out of time on and is marked so.
func (s *Sweeper) storeFailed(ctx context.Context, repo Repo, row RepoResult, err error) (RepoResult, error) {
	if ctx.Err() != nil {
		return s.stale(ctx, repo, row, StaleDeadline)
	}
	return row, err
}

// stale records that repo could not be reconciled, and prunes its records
// anyway while the tick has time. The marker is written under closing's
// context, so a tick whose deadline is what failed it still says so.
func (s *Sweeper) stale(ctx context.Context, repo Repo, row RepoResult, reason string) (RepoResult, error) {
	if ctx.Err() != nil {
		reason = StaleDeadline
	}
	row.Stale, row.Reason = true, reason
	wctx, cancel := closing(ctx)
	defer cancel()
	if err := s.markStale(wctx, repo, reason, &row); err != nil {
		return row, err
	}
	if ctx.Err() != nil {
		return row, nil
	}
	return s.pruned(ctx, repo, row)
}

func (s *Sweeper) pruned(ctx context.Context, repo Repo, row RepoResult) (RepoResult, error) {
	n, err := s.prune(ctx, repo, &row)
	row.Pruned = n
	if err != nil && ctx.Err() != nil {
		return row, nil
	}
	return row, err
}

// recompute reads one repository's two inputs and joins them, and answers
// with the reason it could not when the repository as a whole could not be
// read. err is the store's.
//
// A read that fails is not an error of the sweep: it is a fact about the
// repository, and the caller records it (Stale) — or, for one pull request,
// the view does (Unread). What this must not do is join half the inputs,
// which is why the pull requests are not asked for at all when the runs could
// not be read.
func (s *Sweeper) recompute(ctx context.Context, repo Repo, budget *int, row *RepoResult) (View, string, error) {
	runs, err := s.Runs.Runs(ctx, repo, s.now().Add(-RunsWindow))
	if err != nil {
		return View{}, StaleRuns, nil
	}
	now := s.now()
	known, before, err := s.previous(ctx, repo, now, row)
	if err != nil {
		return View{}, "", err
	}
	ask, carried, backedOff := []int{}, []PullRequest{}, []int{}
	for _, n := range Numbers(runs) {
		if pr, ok := known[n]; ok && pr.State == StateMerged {
			// Merged is final on the host, so the state the last sweep read
			// is the state, and re-reading it every tick would spend the
			// shared rate limit on an answer that cannot change.
			carried = append(carried, pr)
			continue
		}
		if u, ok := before[n]; ok && now.Before(u.RetryAfter) {
			backedOff = append(backedOff, n)
			continue
		}
		ask = append(ask, n)
	}
	// The newest first, and the ones no sweep has read before ahead of the
	// rest: what a budget cut defers is then the oldest work the runs name,
	// whose last-read state the view still shows.
	slices.SortFunc(ask, func(a, b int) int {
		_, ka := known[a]
		_, kb := known[b]
		if ka != kb {
			if !ka {
				return -1
			}
			return 1
		}
		return cmp.Compare(b, a)
	})
	var deferred []int
	if len(ask) > *budget {
		ask, deferred = ask[:*budget], ask[*budget:]
	}
	*budget -= len(ask)
	row.Read = len(ask)
	slices.Sort(ask)

	prs, err := s.Reader.PullRequests(ctx, repo.Provider, repo.Name, ask)
	var partial *PartialError
	var failed []int
	switch {
	case ctx.Err() != nil:
		return View{}, StaleDeadline, nil
	case errors.Is(err, ErrBitbucket):
		return View{}, StaleUnsupported, nil
	case errors.As(err, &partial):
		prs, failed = partial.Read, partial.Failed
	case err != nil:
		return View{}, StalePullRequests, nil
	}
	prs = append(prs, carried...)
	var unread []Unread
	for _, set := range []struct {
		numbers []int
		reason  string
	}{{failed, UnreadFailed}, {backedOff, UnreadFailed}, {deferred, UnreadDeferred}} {
		for _, n := range set.numbers {
			u := Unread{Number: n, Reason: set.reason}
			if pr, ok := known[n]; ok {
				prs, u.Carried = append(prs, pr), true
			}
			// A number that was not asked for keeps the count it had; one the
			// host refused again adds to it, and backs off once it reaches the
			// bound.
			u.Failures, u.RetryAfter = before[n].Failures, before[n].RetryAfter
			if slices.Contains(failed, n) {
				u.Failures = min(u.Failures+1, MaxReadFailures)
				if u.Failures >= MaxReadFailures {
					u.RetryAfter = now.Add(ReadRetryAfter)
				}
			}
			unread = append(unread, u)
		}
	}
	view := bound(Join(repo.Name, prs, runs), MaxViewRecords, MaxViewCollisions)
	view.Unread = keptUnread(view, unread)
	return view, "", nil
}

// previous is the host state the last written view holds for each pull
// request it read — the answer a pull request that is not read again carries —
// and the unread entries it recorded, which hold each refused number's failure
// count and retry-after. A view that does not decode is a view with no
// answers, counted and stepped over — the next write replaces it.
func (s *Sweeper) previous(ctx context.Context, repo Repo, now time.Time, row *RepoResult) (map[int]PullRequest, map[int]Unread, error) {
	var raw json.RawMessage
	_, found, err := s.Store.Read(ctx, PathView(repo.Slug), &raw)
	if err != nil || !found {
		return nil, nil, err
	}
	var v View
	if json.Unmarshal(raw, &v) != nil {
		row.Undecodable++
		return nil, nil, nil
	}
	out := map[int]PullRequest{}
	for _, r := range v.Records {
		if r.PR != nil && r.PR.State != "" {
			out[r.PR.Number] = *r.PR
		}
	}
	unread := map[int]Unread{}
	for _, u := range v.Unread {
		// The count is the view's own, but the view is a document in a
		// database other writers could reach: a count outside the bound is
		// held to it, and a retry further off than one back-off is due now
		// rather than a number never asked for again.
		u.Failures = max(0, min(u.Failures, MaxReadFailures))
		if u.RetryAfter.After(now.Add(ReadRetryAfter)) {
			u.RetryAfter = time.Time{}
		}
		unread[u.Number] = u
	}
	return out, unread, nil
}

// bound holds a view to maxRecords records and maxCollisions collisions,
// and says how many of each it left out.
//
// Records in flight are kept before finished ones, and within each the ones
// whose newest run is newest: what is dropped is the oldest history, and a
// collision is about work in flight, so it is computed again over the
// records kept. Collisions past the bound are dropped from the end of the
// view's own order.
func bound(v View, maxRecords, maxCollisions int) View {
	if len(v.Records) > maxRecords {
		recs := slices.Clone(v.Records)
		newest := func(r Record) string {
			if len(r.Runs) > 0 {
				return r.Runs[0].ID
			}
			return ""
		}
		slices.SortStableFunc(recs, func(a, b Record) int {
			if a.InFlight != b.InFlight {
				if a.InFlight {
					return -1
				}
				return 1
			}
			if c := cmp.Compare(newest(b), newest(a)); c != 0 {
				return c
			}
			an, bn := 0, 0
			if a.PR != nil {
				an = a.PR.Number
			}
			if b.PR != nil {
				bn = b.PR.Number
			}
			return cmp.Compare(bn, an)
		})
		v.Omitted = len(recs) - maxRecords
		recs = recs[:maxRecords]
		sortRecords(recs)
		v.Records = recs
		v.Collisions = collisions(recs)
	}
	if len(v.Collisions) > maxCollisions {
		v.OmittedCollisions = len(v.Collisions) - maxCollisions
		v.Collisions = v.Collisions[:maxCollisions]
	}
	return v
}

// keptUnread is the unread pull requests the bounded view still holds a
// record for, sorted by number: one the bound dropped is said by Omitted, and
// listing it here too would grow the document past the bound it just applied.
func keptUnread(v View, unread []Unread) []Unread {
	held := map[int]bool{}
	for _, r := range v.Records {
		if r.PR != nil {
			held[r.PR.Number] = true
		}
	}
	out := slices.DeleteFunc(unread, func(u Unread) bool { return !held[u.Number] })
	slices.SortFunc(out, func(a, b Unread) int { return cmp.Compare(a.Number, b.Number) })
	if len(out) == 0 {
		return nil
	}
	return out
}

// markStale writes the repository's stale marker, keeping the moment the
// repository first failed to reconcile: the view beside it is the last one
// that could be computed, and how long it has been standing is what a reader
// of it needs. A marker already there is therefore rewritten with its own
// Since and the reason of this failure, which changes the node only when the
// reason itself changed. A marker that does not decode is replaced, and
// counted.
func (s *Sweeper) markStale(ctx context.Context, repo Repo, reason string, row *RepoResult) error {
	path := PathStale(repo.Slug)
	var raw json.RawMessage
	_, found, err := s.Store.Read(ctx, path, &raw)
	if err != nil {
		return err
	}
	mark := Stale{Since: s.now().UTC(), Reason: reason}
	if found {
		var prev Stale
		switch {
		case json.Unmarshal(raw, &prev) != nil:
			row.Undecodable++
		case !prev.Since.IsZero():
			mark.Since = prev.Since
		}
	}
	return s.Store.Write(ctx, path, mark)
}

// clearStale removes the marker of a repository that reconciled, and writes
// nothing when there is none: a sweep that read everything must not move the
// generation of a node it has nothing to say about.
func (s *Sweeper) clearStale(ctx context.Context, repo Repo) error {
	path := PathStale(repo.Slug)
	etag, found, err := s.Store.Read(ctx, path, nil)
	if err != nil || !found {
		return err
	}
	if err := s.Store.Put(ctx, path, etag, nil); err != nil && !errors.Is(err, rtdb.ErrPrecondition) {
		return err
	}
	return nil
}

// prune removes the intent records of one repository that are older than
// MaxAge, and then the oldest past Keep. It returns how many it removed.
//
// The bound is applied here and not by the report path, which writes and
// takes nothing back (P4): a writer that pruned would be a writer that reads
// what is in flight, which is the view's job and a different answer.
//
// A record's age is its own At, which is the reporter's clock: one stamped
// further in the future than intentClockSkew is ordered as the oldest record
// there is, so a reporter cannot keep a record past both bounds by dating it
// ahead. A record that does not decode is counted and removed, at most
// MaxUndecodablePrunes a tick, never a stop of the tick: the node is written by
// every reporter, and one bad record is one reporter's.
//
// Two things make this safe against a reporter writing at the same time. The
// records are ordered from one read of the node, so a record written after it
// is not a candidate at all — it is newer than every one that was read, and
// the next sweep sees it. And every removal goes through the store's own
// compare-and-set on the record's OWN ETag, so a record that changed between
// the read and the removal is left alone rather than deleted: it is no longer
// the record this sweep decided to drop.
func (s *Sweeper) prune(ctx context.Context, repo Repo, row *RepoResult) (int, error) {
	keep, maxAge := s.Keep, s.MaxAge
	if keep == 0 {
		keep = DefaultIntents
	}
	if maxAge == 0 {
		maxAge = DefaultIntentMaxAge
	}
	if keep < 0 && maxAge < 0 {
		return 0, nil
	}
	parent := PathIntents(repo.Slug)
	var raw map[string]json.RawMessage
	_, found, err := s.Store.Read(ctx, parent, &raw)
	if err != nil || !found {
		return 0, err
	}
	now := s.now()
	type record struct {
		id string
		at time.Time
	}
	records := make([]record, 0, len(raw))
	var undecodable []string
	for id, doc := range raw {
		var in Intent
		if json.Unmarshal(doc, &in) != nil {
			row.Undecodable++
			undecodable = append(undecodable, id)
			continue
		}
		at := in.At
		if at.After(now.Add(intentClockSkew)) {
			at = time.Time{}
		}
		records = append(records, record{id: id, at: at})
	}
	// Oldest first, by the record's own clock and then by its ID: two
	// declarations stamped in the same instant are ordered by the key they
	// already have, so which one is dropped does not depend on the order the
	// database listed them in.
	slices.SortFunc(records, func(a, b record) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})
	drop := 0
	if maxAge > 0 {
		cutoff := now.Add(-maxAge)
		for drop < len(records) && records[drop].at.Before(cutoff) {
			drop++
		}
	}
	if keep >= 0 && len(records)-keep > drop {
		drop = len(records) - keep
	}
	pruned := 0
	for _, r := range records[:drop] {
		ok, err := s.dropIntent(ctx, PathIntent(repo.Slug, r.id), false)
		if err != nil {
			return pruned, err
		}
		if ok {
			pruned++
		}
	}
	// In ID order, so the records a bounded tick leaves for the next one do
	// not depend on the order the database listed them in.
	slices.Sort(undecodable)
	if len(undecodable) > MaxUndecodablePrunes {
		undecodable = undecodable[:MaxUndecodablePrunes]
	}
	for _, id := range undecodable {
		ok, err := s.dropIntent(ctx, PathIntent(repo.Slug, id), true)
		if err != nil {
			return pruned, err
		}
		if ok {
			pruned++
		}
	}
	return pruned, nil
}

// dropIntent removes one intent record through the store's compare-and-set
// on the record's own ETag, and reports whether it did. With undecodable set
// it removes the record only while it still does not decode: a reporter that
// rewrote it since the node was read wrote a declaration, which is not the
// record this sweep decided to drop.
func (s *Sweeper) dropIntent(ctx context.Context, path string, undecodable bool) (bool, error) {
	var doc json.RawMessage
	etag, found, err := s.Store.Read(ctx, path, &doc)
	if err != nil || !found {
		return false, err
	}
	if undecodable && json.Unmarshal(doc, new(Intent)) == nil {
		return false, nil
	}
	switch err := s.Store.Put(ctx, path, etag, nil); {
	case err == nil:
		return true, nil
	case errors.Is(err, rtdb.ErrPrecondition):
		// A reporter rewrote it while this sweep was reading: it is not
		// the record this sweep decided to drop, and the next sweep
		// decides about the new one.
		return false, nil
	default:
		return false, err
	}
}

func (s *Sweeper) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}
