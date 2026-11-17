package runstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/task"
)

// ChangeSetMemberFile is the member's own record, under the run's own
// runs/<slug>/<run-id>/ prefix.
const ChangeSetMemberFile = "changeset.json"

// VerdictShip is the verdict a member records when nothing must change before
// its pull request is merged: the word the run's own reviews carry in
// result.json (ReviewSummary.Verdict), and the one a reader of a whole set
// looks for — a member that recorded anything else, or nothing, has not
// shipped, and the set it belongs to waits for it (D614, D3).
const VerdictShip = "ship"

// ChangeSetMember is what one member run records about the change set it was
// launched in (task.Spec.ChangeSet): the set it belongs to, the run it is, its
// own pull request, the commit its verdict is about and the verdict it ended
// on. The record is written by whichever run last answered for the member's
// pull request — the member run or a follow-up on it — which is why the commit
// travels with the verdict rather than being read from the member run's own
// result.json (D2).
//
// It sits under the member's own prefix because that is the only one a run
// reaches (SECURITY.md): a run reads and writes its own record, and the step
// that sees a whole set reads every member's (D3). It is not a trust boundary
// and no authorization reads it: the run acts as the job, so a record is the
// run's word about itself, and the runs of one repository share their prefixes,
// so a later run of the same repository can rewrite an earlier one's (T5).
//
// SetID, Repo and RunID repeat what the member's task says (D642) so that the
// step reading a whole set can check a member against the list every other
// member was launched with, rather than trusting the prefix it found it under.
//
// A field added later is fine; changing the meaning of one of these, or
// dropping one, bumps Version, which is what a reader checks before it reads
// the rest.
type ChangeSetMember struct {
	Version int    `json:"version"`
	SetID   string `json:"set_id"`
	Repo    string `json:"repo"`
	RunID   string `json:"run_id"`
	// PRURL is the member's own pull request, which is what lets a whole set
	// be reached later (D3). Empty until the run has opened one.
	PRURL string `json:"pr_url,omitempty"`
	// Verdict is the run's own final verdict word, VerdictShip when nothing
	// must change before this is merged (the word the run's reviews carry in
	// result.json). Empty while the run has not ended, and on a run that ended
	// without one: the member writes it once its ending is known, and a run
	// that ended without shipping writes the empty word over whatever an
	// earlier attempt at the same pull request recorded (D2).
	Verdict string `json:"verdict,omitempty"`
	// HeadSHA is the commit the verdict is about: the head the run that wrote
	// this record had pushed when it wrote it, written with the verdict and
	// never on its own. It is here and not read from the member run's own
	// result.json because the run that writes this record is not always the run
	// the set was launched from: a follow-up to a member's pull request pushes
	// to the member's branch, keeps its own commit under its own run id, and
	// answers on the MEMBER's record (D2), so a step settling a whole set (D3)
	// takes the commit from here.
	HeadSHA string `json:"head_sha,omitempty"`
	// Verified is whether a clean-tree test passed on HeadSHA, as the run that
	// wrote the record read its own verify table (the rule the runner's own
	// pushedVerified keeps). A verdict whose commit nothing verified is one no
	// settle acts on, and that table lives under the run that pushed, where a
	// reader of the member's prefix cannot see it.
	Verified bool `json:"verified,omitempty"`
	// At is when the record was last written, UTC.
	At time.Time `json:"at"`
}

// Validate checks the record's own shape. A member that cannot say which set
// and which run it is would be read by a settle step as a member of whatever
// prefix it was found under, which is not something the record may leave open.
func (m *ChangeSetMember) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if m.Version != 1 {
		bad("change set member: version must be 1")
	}
	if !task.ValidRunID(m.SetID) {
		bad("change set member: set_id %q must look like 20260926-221530-a1b2", m.SetID)
	}
	if !task.ValidRunID(m.RunID) {
		bad("change set member: run_id %q must look like 20260926-221530-a1b2", m.RunID)
	}
	if c, err := task.CanonicalRepo(m.Repo); err != nil {
		bad("change set member: repo %q must look like owner/name", m.Repo)
	} else if c != m.Repo {
		// The member list a member is checked against holds canonical forms
		// (task.Validate), so a record naming the same repository in another
		// spelling would not match the very list it repeats.
		bad("change set member: repo %q is not canonical (%s)", m.Repo, c)
	}
	if m.PRURL != "" && !strings.HasPrefix(m.PRURL, "http") {
		bad("change set member: pr_url %q must be a URL", m.PRURL)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrCorrupt, errors.Join(errs...))
	}
	return nil
}

// WriteChangeSetMember stores the run's changeset.json.
func (s *Store) WriteChangeSetMember(ctx context.Context, m *ChangeSetMember) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return s.PutFile(ctx, ChangeSetMemberFile, data, "application/json")
}

// ReadChangeSetMember loads and checks the run's changeset.json; ErrNotFound
// means the run recorded none — a run in no change set, or one that ended
// before it had a pull request to record.
func (s *Store) ReadChangeSetMember(ctx context.Context) (*ChangeSetMember, error) {
	data, err := s.readRecordObject(ctx, ChangeSetMemberFile)
	if err != nil {
		return nil, err
	}
	var m ChangeSetMember
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decoding %s: %w: %w", ChangeSetMemberFile, ErrCorrupt, err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", ChangeSetMemberFile, err)
	}
	return &m, nil
}
