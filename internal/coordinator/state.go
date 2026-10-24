package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The coordinator's subtree, and the one place it is written.
//
// D195 (answered 2026-10-21) puts the coordinator's state in a **new top-level
// `coordinator/` subtree**, project-wide facts under `coordinator/project/`
// and per-repository facts under `coordinator/repos/<slug>/`, and every
// document keyed by a deterministic ID. It is deliberately not under `config/`:
// that is the subtree the rules' USER and RUN macros are built around, so a
// coordinator node there would sit inside the tree the policies are authorised
// on, and every later widening of `config/` would reach the coordinator's state
// as well. As a separate prefix the read grant of Q15(A) is one location rather
// than a list, which is what the template's `coordinator` node grants.
//
// Q6 (answered 2026-10-19) is the other half: the coordinator's "never
// contradicts itself" is the house idiom and not a new lock — a deterministic
// document ID plus ETag/`If-Match` CAS (reconciliation findings #16 and #17,
// which is `internal/rtdb`'s GetETag and PutIfMatch, as `internal/budget`'s
// SetKill already uses them). The writer identity is the aggregation job's own
// service account, which holds `roles/firebasedatabase.admin` and therefore
// bypasses these rules entirely: the rules keep every *client* out of the
// subtree, and what keeps the job to its own nodes is its code, not IAM
// (D194, answered 2026-10-21, and the comment beside the grant in
// deploy/terraform/gcp/modules/firebase/iam.tf).

// The subtree's own paths. PathRoot is the prefix the rules grant a read on,
// and nothing the coordinator writes may sit outside it.
const (
	// PathRoot is the coordinator's top-level subtree.
	PathRoot = "coordinator"
	// PathProject is where the project-wide facts live.
	PathProject = PathRoot + "/project"
	// PathRepos is the parent of one child per repository, keyed by the
	// repository's slug as budget.Key escapes it.
	PathRepos = PathRoot + "/repos"
)

// PathRepo is one repository's node: the parent of every document about it.
func PathRepo(slug string) string { return PathRepos + "/" + budget.Key(slug) }

// PathProjectDocument is one project-wide document, and PathRepoDocument one
// repository's. id comes from DocumentID.
func PathProjectDocument(id string) string { return PathProject + "/" + id }

// PathRepoDocument is one repository's document, under that repository.
func PathRepoDocument(slug, id string) string { return PathRepo(slug) + "/" + id }

// DocumentID is the database key of one coordinator document, derived from the
// parts that describe the thing it is about — the caller and the declaration
// for an intent report, the repository and the sweep's own inputs for a view —
// and from nothing else. No clock, no counter and no random source goes in: the
// same thing described twice is the same key, which is how "the same event
// delivered twice is harmless" is satisfied without an Idempotency-Key header
// (finding #17) and why the same report twice writes one document rather than
// two.
//
// The parts are length-prefixed before hashing, so ("a", "bc") and ("ab", "c")
// are different documents. The answer is lower-case hex: a Realtime Database
// key may not hold a dot, a dollar, a hash, a bracket or a slash, and hex makes
// each of those unrepresentable rather than escaped — the same reason
// budget.Key exists, for keys that have to stay readable.
func DocumentID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p))))
		h.Write([]byte{':'})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ErrOutsideSubtree is Store's refusal of a path that is not under PathRoot.
// The writer holds roles/firebasedatabase.admin and bypasses the rules, so
// this check, and not the rules or IAM, is what keeps it to its own subtree
// (D194).
var ErrOutsideSubtree = errors.New("coordinator: the path is outside the coordinator's subtree")

// inSubtree refuses a path outside PathRoot. It trims the one leading or
// trailing slash internal/rtdb tolerates, and rtdb itself refuses the empty
// and dotted segments that could otherwise climb out.
func inSubtree(path string) error {
	p := strings.Trim(path, "/")
	if p == PathRoot || strings.HasPrefix(p, PathRoot+"/") {
		return nil
	}
	return fmt.Errorf("%w: /%s", ErrOutsideSubtree, p)
}

// writeAttempts bounds Store.Write's read-then-CAS loop. It is the count
// internal/budget's SetKill uses (finding #17), and it is deliberately small:
// every attempt lost is another writer holding the same document, and a
// coordinator that keeps losing is one whose caller should be told rather than
// one that should keep trying.
const writeAttempts = 5

// Store reads and writes the coordinator's documents through
// `internal/rtdb`. It is the only way this package touches the database, and
// it holds no state of its own: the concurrency control is the ETag the
// database hands back, never a lock in this process (Q6 A).
type Store struct {
	c        *rtdb.Client
	attempts int
}

// NewStore returns a store over an already-opened client. The caller owns the
// client's credential: this package never mints one and never reads one.
func NewStore(c *rtdb.Client) *Store { return &Store{c: c, attempts: writeAttempts} }

// Read returns the document at path into out (nil to read nothing) and the
// ETag to write it back with, and found is false for an absent node. An absent
// node leaves out untouched: "no document" is an answer, and a zero value would
// make it indistinguishable from a document of zeroes.
func (s *Store) Read(ctx context.Context, path string, out any) (etag string, found bool, err error) {
	if err := inSubtree(path); err != nil {
		return "", false, err
	}
	return s.c.GetETag(ctx, path, out)
}

// Put writes v (nil deletes) at path only while the node still carries etag,
// and otherwise refuses with rtdb.ErrPrecondition. ETags work on one location,
// so this is the single-document write; a caller that must change several
// documents uses one Put per document and decides what a partial failure means.
func (s *Store) Put(ctx context.Context, path, etag string, v any) error {
	if err := inSubtree(path); err != nil {
		return err
	}
	return s.c.PutIfMatch(ctx, path, etag, v)
}

// Write reads path and puts v under the ETag it read, retrying while another
// writer wins the race, and refusing with the last error once the attempts are
// spent. Only a lost race (rtdb.ErrPrecondition) is retried: a refusal or an
// outage is returned at once, since another attempt would meet the same
// answer. Every write of the coordinator's state goes through it, so no
// caller can write a document without having read its ETag first.
func (s *Store) Write(ctx context.Context, path string, v any) error {
	if err := inSubtree(path); err != nil {
		return err
	}
	var err error
	for attempt := 0; attempt < s.attempts; attempt++ {
		var etag string
		if etag, _, err = s.Read(ctx, path, nil); err != nil {
			return err
		}
		err = s.Put(ctx, path, etag, v)
		if err == nil {
			return nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return err
		}
	}
	return fmt.Errorf("coordinator: writing /%s: %d attempts lost the ETag race: %w", path, s.attempts, err)
}
