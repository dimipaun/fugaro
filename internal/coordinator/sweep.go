package coordinator

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

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
//     document that had not changed. Sweeper.Now is used for the ONE thing
//     that is a fact about a sweep and not about a view: since when a
//     repository has been stale.
//   - What it could not read is RECORDED, never guessed at. A failed read
//     leaves the previous view where it is and writes a marker beside it
//     (Stale) rather than writing a view computed from half its inputs: a
//     view missing the pull requests the host would not answer for reads as
//     "nothing is in flight" about a repository that has work in it, which is
//     the one outcome this must not produce. The marker carries a
//     classification of this package's own and never the host's text — a
//     failure's own message is another writer's, and Q14(A) is why it is read
//     as data and not stored as a sentence.
//   - It takes nothing back and advises nobody. There is no comparison
//     against what is in flight, no advice and no model: the sweep writes the
//     view and the marker, and that is the whole of its output (spec §3,
//     Q10(C) refused).
//
// Its account may write the whole database (roles/firebasedatabase.admin, D194
// — IAM cannot express a path, so the rules cannot fence it and neither can a
// role). What keeps it to its own nodes is Store's own check, which every
// write here goes through: the sweep has no other way to reach the database,
// and everything it writes is built from PathRoot's paths.

// DefaultIntents is how many intent records one repository keeps, oldest
// first out (Sweeper.Keep). A declaration is about work about to start, so
// the records that matter are the newest ones, and a repository that has run
// more than this many since the last sweep has a view to read rather than a
// log to keep. It is the shape D197 gives the override log — the last 200 per
// project — applied to a repository's own node.
const DefaultIntents = 200

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

// RunsReader reads one repository's runs, as the view holds them (Run). It is
// the runs bucket's shape behind one method, so the sweep reads a repository's
// runs the way the CLI's own commands do without this package opening a
// bucket.
type RunsReader interface {
	Runs(ctx context.Context, repo Repo) ([]Run, error)
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
	// StalePullRequests is the host's: an outage, a rate limit, a credential
	// the environment does not hold, or a pull request the host refused.
	StalePullRequests = "the repository's pull requests could not be read"
	// StaleUnsupported is the configuration's: the project-wide read serves
	// github only (Q12 A, finding #30), so a repository on another provider
	// will not reconcile however often it is swept. It is recorded rather
	// than failing the sweep, because a project that holds one such
	// repository still has every other one's view to keep true.
	StaleUnsupported = "the project-wide read serves github only, so this repository was not read"
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
	// Stale is whether this sweep could not reconcile the repository, and
	// Reason is why ("" when it did).
	Stale  bool   `json:"stale"`
	Reason string `json:"reason,omitempty"`
	// Pruned is how many intent records this sweep removed.
	Pruned int `json:"pruned"`
}

// Result is one tick of the sweep, in the order the repositories were given.
type Result struct {
	Repos []RepoResult `json:"repos"`
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
	// Now is the clock the stale marker is stamped with; nil means time.Now.
	Now func() time.Time
	// Keep is how many intent records one repository keeps; 0 means
	// DefaultIntents. A negative value keeps every record.
	Keep int
}

// Sweep reconciles every repository in repos, one after another, and returns
// what it did.
//
// The order is the caller's and the repositories are independent: one that
// could not be reconciled is marked stale and the next one is swept anyway. A
// failure of the store itself is the one thing that stops the tick — it is
// the sweep's own failure rather than a repository's, it would fail for every
// repository after it, and the job's exit code is where an operator reads it.
func (s *Sweeper) Sweep(ctx context.Context, repos []Repo) (Result, error) {
	if s.Reader == nil || s.Runs == nil || s.Store == nil {
		panic("coordinator: Sweeper needs a Reader, a Runs source and a Store; build it with NewStore")
	}
	res := Result{Repos: make([]RepoResult, 0, len(repos))}
	for _, repo := range repos {
		row, err := s.one(ctx, repo)
		if err != nil {
			return res, err
		}
		res.Repos = append(res.Repos, row)
	}
	return res, nil
}

// one reconciles one repository: the recompute, the write, and the records it
// keeps.
func (s *Sweeper) one(ctx context.Context, repo Repo) (RepoResult, error) {
	row := RepoResult{Repo: repo.Name, Slug: repo.Slug}
	view, reason := s.recompute(ctx, repo)
	switch {
	case reason != "":
		row.Stale, row.Reason = true, reason
		if err := s.markStale(ctx, repo, reason); err != nil {
			return row, err
		}
	default:
		if err := s.Store.Write(ctx, PathView(repo.Slug), view); err != nil {
			return row, err
		}
		if err := s.clearStale(ctx, repo); err != nil {
			return row, err
		}
		row.Records, row.InFlight, row.Collisions = len(view.Records), view.InFlight(), len(view.Collisions)
	}
	pruned, err := s.prune(ctx, repo)
	if err != nil {
		return row, err
	}
	row.Pruned = pruned
	return row, nil
}

// recompute reads one repository's two inputs and joins them, and answers
// with the reason it could not when either read failed.
//
// A read that fails is not an error of the sweep: it is a fact about the
// repository, and the caller records it (Stale). What this must not do is
// join half the inputs, which is why the pull requests are not asked for at
// all when the runs could not be read.
func (s *Sweeper) recompute(ctx context.Context, repo Repo) (View, string) {
	runs, err := s.Runs.Runs(ctx, repo)
	if err != nil {
		return View{}, StaleRuns
	}
	prs, err := s.Reader.PullRequests(ctx, repo.Provider, repo.Name, Numbers(runs))
	switch {
	case errors.Is(err, ErrBitbucket):
		return View{}, StaleUnsupported
	case err != nil:
		return View{}, StalePullRequests
	}
	return Join(repo.Name, prs, runs), ""
}

// markStale writes the repository's stale marker, keeping the moment the
// repository first failed to reconcile: the view beside it is the last one
// that could be computed, and how long it has been standing is what a reader
// of it needs. A marker already there is therefore rewritten with its own
// Since and the reason of this failure, which changes the node only when the
// reason itself changed.
func (s *Sweeper) markStale(ctx context.Context, repo Repo, reason string) error {
	path := PathStale(repo.Slug)
	var prev Stale
	_, found, err := s.Store.Read(ctx, path, &prev)
	if err != nil {
		return err
	}
	mark := Stale{Since: s.now().UTC(), Reason: reason}
	if found && !prev.Since.IsZero() {
		mark.Since = prev.Since
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

// prune keeps the newest Keep intent records of one repository and removes
// the rest. It returns how many it removed.
//
// The bound is applied here and not by the report path, which writes and
// takes nothing back (P4): a writer that pruned would be a writer that reads
// what is in flight, which is the view's job and a different answer.
//
// Two things make this safe against a reporter writing at the same time. The
// records are ordered from one read of the node, so a record written after it
// is not a candidate at all — it is newer than every one that was read, and
// the next sweep sees it. And every removal goes through the store's own
// compare-and-set on the record's OWN ETag, so a record that changed between
// the read and the removal is left alone rather than deleted: it is no longer
// the record this sweep decided to drop.
func (s *Sweeper) prune(ctx context.Context, repo Repo) (int, error) {
	keep := s.Keep
	switch {
	case keep == 0:
		keep = DefaultIntents
	case keep < 0:
		// Every record, which is what the field's own doc says a negative
		// value means. It is read here rather than left to the slice below,
		// where a negative keep would be an index past the end.
		return 0, nil
	}
	parent := PathIntents(repo.Slug)
	var records map[string]Intent
	_, found, err := s.Store.Read(ctx, parent, &records)
	if err != nil {
		return 0, err
	}
	if !found || len(records) <= keep {
		return 0, nil
	}
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	// Oldest first, by the record's own clock and then by its ID: two
	// declarations stamped in the same instant are ordered by the key they
	// already have, so which one is dropped does not depend on the order the
	// database listed them in.
	slices.SortFunc(ids, func(a, b string) int {
		if c := records[a].At.Compare(records[b].At); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	pruned := 0
	for _, id := range ids[:len(ids)-keep] {
		path := PathIntent(repo.Slug, id)
		etag, found, err := s.Store.Read(ctx, path, nil)
		if err != nil {
			return pruned, err
		}
		if !found {
			continue
		}
		switch err := s.Store.Put(ctx, path, etag, nil); {
		case err == nil:
			pruned++
		case errors.Is(err, rtdb.ErrPrecondition):
			// A reporter rewrote it while this sweep was reading: it is not
			// the record this sweep decided to drop, and the next sweep
			// decides about the new one.
		default:
			return pruned, err
		}
	}
	return pruned, nil
}

func (s *Sweeper) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}
