package bbtokens

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// selector is the real Selector (T4, D2355): the node's records, the project
// lookup (T5) and the status store (T6), walked narrowest first.
//
// It holds no bundle, no policy and no token: the two documents a selection is
// decided from arrive with the request's own Snapshot, so one request cannot be
// admitted against one read of them and served under another.
type selector struct {
	records  []Record
	resolver ProjectResolver
	status   StatusStore
}

// NewSelector answers the Selector the service serves each request with.
//
// records are the node's TYPED records — the entries of its bitbucket_tokens
// key (T3), each of which carries the same coverage spelling a Record does —
// and they are the caller's read of the node rather than a second read here:
// whoever took the request's Snapshot read the node once, and building the
// selector from that one read is what keeps the selection, Decide and the
// forward from disagreeing about which records exist.
//
// The legacy bitbucket-token key is deliberately not among them. It is the
// implicit record of R3 — untyped, and covered by the operator's allow_data_to
// list as the node carries it — so Select reads it off the snapshot's own node
// and an installation that never stored a typed token is served exactly as it
// was before this work.
func NewSelector(records []Record, resolver ProjectResolver, status StatusStore) Selector {
	return selector{records: records, resolver: resolver, status: status}
}

// Select answers the narrowest usable token covering canonical, and the refusal
// that says why when there is none (D2355, R12).
//
// The order is the type's, narrowest first — a repository token, then a project
// token whose lookup answered the UUID it names, then a workspace token — and
// the legacy key ranks after every typed record, however exactly its own list
// names the repository (O6). Two tokens of one rank are decided by their
// document keys, so the same request served twice walks the same candidate
// first. A token the store marks throttled or broken is skipped and the
// narrowest USABLE token serves, which can be a wider one: that is the stated
// price of the swap (R12).
//
// Two orderings of the walk are part of the answer rather than of its cost. A
// record's status is read before its project lookup, so a token the store holds
// is skipped without asking the resolver about it. And the resolver is asked
// only when no usable repository token already serves, so a narrower token
// being chosen (R2) means no wider one is asked about at all: the lookup costs
// an upstream call with the candidate's own credential, and a credential that
// is not serving this request must not be spent on it.
//
// write is not consulted. Which token may write is D2353's: a write scope is
// proven on first use and refused by the proxy's own reaction to Bitbucket's
// 403, never decided here from a record or a status that states nothing about
// it.
//
// Every skip is silent and none is a refusal of its own: a token has to be in
// both documents to serve anything, and a record a reader refuses, a bundle key
// with no record, a record with no bundle key, a project token whose lookup did
// not confirm it and a status that cannot be read are all "not a candidate"
// rather than a class (R14, fail closed).
func (s selector) Select(ctx context.Context, snap Snapshot, canonical string, write bool) (Choice, error) {
	if !holdsAnyToken(snap.Bundle) {
		return Choice{}, &Refusal{Class: ClassNoToken}
	}

	var (
		repos     []candidate
		projects  []Record
		wider     []candidate
		throttled bool
		broken    bool
	)
	now := time.Now()
	for _, rec := range s.candidates(snap) {
		if err := rec.Validate(); err != nil {
			continue
		}
		if !covers(rec, canonical) {
			continue
		}
		if _, held := snap.Bundle.Value(rec.Name); !held {
			continue
		}
		// The status is read BEFORE any lookup: a token the store holds is not a
		// candidate whatever the lookup would have answered, and a lookup made
		// on its behalf is an upstream call with a credential that cannot serve
		// it — which is exactly the 429 or 401 an exhausted token is in, so the
		// class it earns would be lost to the lookup's own failure.
		st, err := s.status.Read(ctx, rec.Name)
		if err != nil {
			continue
		}
		switch {
		case st.Available() || lapsed(st, now):
		case st.State == StateBroken:
			broken = true
			continue
		default:
			throttled = true
			continue
		}
		switch rec.Type {
		case TypeRepo:
			repos = append(repos, candidate{rec: rec, rank: rankOf(rec)})
		case TypeProject:
			projects = append(projects, rec)
		default:
			wider = append(wider, candidate{rec: rec, rank: rankOf(rec)})
		}
	}

	usable := make([]candidate, 0, len(repos)+len(projects)+len(wider))
	usable = append(usable, repos...)
	usable = append(usable, wider...)
	// The lookup is made only when it can change the answer: a usable repository
	// token is narrower than every project token and already serves this
	// request, so R2's "a narrower token is chosen before a project token is
	// considered" is precisely that no project token is asked about. A usable
	// WORKSPACE token does not suppress it, because a project token is the
	// narrower of the two and its lookup is what says whether it reaches.
	if len(repos) == 0 {
		for _, rec := range projects {
			if s.projectCovers(ctx, canonical, rec) {
				usable = append(usable, candidate{rec: rec, rank: rankOf(rec)})
			}
		}
	}

	if len(usable) == 0 {
		// A throttle is reported ahead of a broken token when both are in the
		// way: a hold lapses on its own, so the caller's retry may succeed, and
		// the call after it reports the token an operator has to clear.
		switch {
		case throttled:
			return Choice{}, &Refusal{Class: ClassThrottled, Repo: canonical}
		case broken:
			return Choice{}, &Refusal{Class: ClassBroken, Repo: canonical}
		default:
			return Choice{}, &Refusal{Class: ClassNotCovered, Repo: canonical}
		}
	}

	slices.SortStableFunc(usable, func(a, b candidate) int {
		if n := cmp.Compare(a.rank, b.rank); n != 0 {
			return n
		}
		return strings.Compare(string(a.rec.Name), string(b.rec.Name))
	})
	return Choice{Name: usable[0].rec.Name}, nil
}

// candidate is one record with the rank its type holds, so that the walk and
// the ordering cannot disagree about which token is the narrower one.
type candidate struct {
	rec  Record
	rank int
}

// candidates is the node's records as one list: the typed records the caller
// read off the bitbucket_tokens key, and the implicit legacy record R3 makes of
// the bitbucket_token block — untyped, and covered by that block's allow_repos
// list as written, which is the operator's allow_data_to list as the node
// carries it.
//
// A node with no block at all still yields the legacy record, with no coverage:
// the key is implicit, and an installation that never named a repository has to
// keep working with no step (F14), so it reads as a token that reaches nothing
// rather than as a missing one.
func (s selector) candidates(snap Snapshot) []Record {
	out := make([]Record, 0, len(s.records)+1)
	out = append(out, s.records...)

	legacy := Record{Name: LegacyName, Type: TypeUntyped}
	if snap.Policy != nil && snap.Policy.BitbucketToken != nil {
		legacy.Coverage = snap.Policy.BitbucketToken.AllowRepos
	}
	return append(out, legacy)
}

// projectCovers asks whether the record's own project token reaches canonical,
// which is the whole of project matching: the lookup is made with the candidate
// token itself and never with a wider or a narrower one (R2), and it is made
// only for a project record whose workspace coverage already admitted the
// repository, and whose status is usable, and only when no usable repository
// token already serves — so a token in another workspace is never asked about,
// a token the store holds is never asked about, and a token in this one is
// asked nothing a repository token would have answered first.
//
// A lookup that fails is not an answer, so the token is not a candidate: the
// contract forbids a bare error here, and a token whose reach could not be
// confirmed must not serve.
func (s selector) projectCovers(ctx context.Context, canonical string, rec Record) bool {
	uuid, err := s.resolver.ProjectOf(ctx, canonical, rec.Name)
	if err != nil {
		return false
	}
	return rec.MatchesProject(uuid)
}

// covers reports whether any of the record's coverage patterns admits the
// canonical repository. The patterns are in the node's provider-qualified
// spelling, which is the spelling MatchRepo compares against.
func covers(rec Record, canonical string) bool {
	for _, pattern := range rec.Coverage {
		if projectpolicy.MatchRepo(pattern, canonical) {
			return true
		}
	}
	return false
}

// rankOf is the narrowest-first order of the three types, with the untyped
// legacy key after all of them (O6). The rank is the type's and never the
// coverage's: a legacy list that names one repository exactly is still a wider
// credential than a repository token, which is the departure O6 states.
func rankOf(rec Record) int {
	switch rec.Type {
	case TypeRepo:
		return 0
	case TypeProject:
		return 1
	case TypeWorkspace:
		return 2
	default:
		return 3
	}
}

// lapsed reports a hold whose end has passed. Until is when an exhaustion or a
// backoff lapses, so a token Bitbucket throttled a minute ago serves again
// without an operator; the zero Until is "no end", which is what a broken
// token's hold carries and why it is never read as lapsed.
func lapsed(st Status, now time.Time) bool {
	if st.State != StateExhausted && st.State != StateBackedOff {
		return false
	}
	return !st.Until.IsZero() && !now.Before(st.Until)
}

// holdsAnyToken reports whether the bundle holds a Bitbucket token at all. It
// is the difference between an installation that stored none — which is
// ClassNoToken, a run holding no credential of its own — and a repository no
// stored token reaches, which is ClassNotCovered and names the repository.
func holdsAnyToken(b BundleTokens) bool {
	return b != nil && len(b.Names()) > 0
}
