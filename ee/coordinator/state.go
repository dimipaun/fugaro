// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/coordpath"
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

// The subtree's own paths live in internal/coordpath, and this package holds no
// second copy of them: the project rules grant a read on PathRoot and the
// doctor reads the marker those rules were generated with, both in the core, so
// the prefix the rules authorise and the prefix this library writes have to be
// one definition rather than two that drift.

// PathIntents is one repository's intent records — P4's report path writes
// there and nothing else does — and PathIntent one of them, keyed by
// IntentID. The node is named and not derived: a reader of the tree has to be
// able to tell the declarations from whatever the sweep (P5) keeps beside
// them under the same repository.
func PathIntents(slug string) string { return coordpath.PathRepo(slug) + "/intents" }

// PathIntent is one intent record.
func PathIntent(slug, id string) string { return PathIntents(slug) + "/" + id }

// PathView is one repository's view — the document the sweep (P5) replaces in
// place, holding the join of the runs behind the repository and the pull
// requests those runs name (View).
//
// It is named rather than derived, like PathIntents beside it and for the
// same reason: the ID's rule (coordpath.DocumentID) is that the same thing described
// twice is one node, and what this node describes is the repository's own
// view, of which there is one at a time and no history. A derived ID would
// have to be a function of the view's content, which is the one thing a
// reader cannot know before reading it.
func PathView(slug string) string { return coordpath.PathRepo(slug) + "/view" }

// PathStale is the marker the sweep writes when it could not reconcile a
// repository (Stale): it says since when and why, and the view at PathView is
// left exactly as the last sweep that could read everything wrote it.
func PathStale(slug string) string { return coordpath.PathRepo(slug) + "/stale" }

// ErrOutsideSubtree is Store's refusal of a path that is not under the
// coordinator's subtree (coordpath.PathRoot). The writer holds
// roles/firebasedatabase.admin and bypasses the rules, so this check, and not
// the rules or IAM, is what keeps it to its own subtree (D194).
var ErrOutsideSubtree = errors.New("coordinator: the path is outside the coordinator's subtree")

// inSubtree refuses a path outside coordpath.PathRoot. It trims the one leading or
// trailing slash internal/rtdb tolerates, and rtdb itself refuses the empty
// and dotted segments that could otherwise climb out.
func inSubtree(path string) error {
	p := strings.Trim(path, "/")
	if p == coordpath.PathRoot || strings.HasPrefix(p, coordpath.PathRoot+"/") {
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

// RepoView is one repository's stored view as the database holds it: the
// document the sweep replaces in place (PathView), whether there was one at
// all, and the marker beside it that says the repository could not be
// reconciled (PathStale).
//
// Stored is what a reader cannot tell from the view alone. An absent node and
// a repository the sweep reconciled to nothing both read as a view with no
// records, and a reader that showed the second when it had the first would
// claim the coordinator had read a repository it never did.
//
// The marker is carried as the sweep wrote it and no age is computed from it:
// the view beside it is a function of its inputs and carries no time
// (sweep.go), so a read with a clock of its own would be comparing two things
// that are not on the same scale.
type RepoView struct {
	// Repo is the repository the caller asked about, "owner/name", as the
	// caller spelled it. The node's key is a slug — a hash of the provider
	// and the name — so an absent node carries no name to read back.
	Repo string `json:"repo"`
	// Provider is the git provider kind the caller resolved for this
	// repository, one of config.Providers' two words, or "" when the caller
	// had none to give. It is the caller's answer and not the node's: the
	// stored view names no host, and a reader that built a pull request's URL
	// out of the URL the sweep stored would be reading a field a job wrote as
	// if it were a fact about the repository.
	//
	// The key is written even when it is empty, so "" is a repository with no
	// provider rather than a document older than this field.
	Provider string `json:"provider"`
	// Stored is whether the database held a view document.
	Stored bool `json:"stored"`
	// View is the stored view, and the zero view when Stored is false.
	View View `json:"view"`
	// Stale is the marker the sweep left, exactly as stored, or nil when the
	// repository has none.
	Stale *Stale `json:"stale,omitempty"`
}

// StoredProjectView is every repository's stored view, in the order the
// caller read them. Like ProjectView it compares nothing across repositories.
type StoredProjectView struct {
	Repos []RepoView `json:"repos"`
}

// ReadRepoView reads repo's view and the marker beside it from slug's node,
// typed rather than raw: this is the read the console and a person's own CLI
// make (D364), and neither of them holds the credential a sweep uses.
//
// A node that does not decode is refused rather than shown as a view with
// nothing in it, and an unknown field is a refusal too: this is the strict
// decoding the tree's other wire formats keep, and a document this reader
// cannot read is one it must not pass off as empty.
func (s *Store) ReadRepoView(ctx context.Context, slug, repo string) (RepoView, error) {
	out := RepoView{Repo: repo}
	var raw json.RawMessage
	if _, found, err := s.Read(ctx, PathView(slug), &raw); err != nil {
		return RepoView{}, err
	} else if found {
		if err := strictDecode(raw, &out.View); err != nil {
			return RepoView{}, fmt.Errorf("coordinator: reading /%s: %w", PathView(slug), err)
		}
		out.Stored = true
	}
	var mark json.RawMessage
	if _, found, err := s.Read(ctx, PathStale(slug), &mark); err != nil {
		return RepoView{}, err
	} else if found {
		var stale Stale
		if err := strictDecode(mark, &stale); err != nil {
			return RepoView{}, fmt.Errorf("coordinator: reading /%s: %w", PathStale(slug), err)
		}
		out.Stale = &stale
	}
	return out, nil
}

// strictDecode decodes one stored document, refusing a field this build does
// not know rather than dropping it.
func strictDecode(raw json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
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
