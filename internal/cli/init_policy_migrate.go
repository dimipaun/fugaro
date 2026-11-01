package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The published policy node an older Fugaro wrote, carried over to the version
// this one reads, IN PLACE: the policy stage of `fugaro update`'s converge and
// the policy step of `fugaro upgrade`. Both call the same two functions, so
// the diff one shows and the write the other makes cannot drift.
//
// It exists for the owner's rule of 2026-10-30 — `fugaro update` is the whole
// upgrade, and it works perfectly. Before this, a bump of projectpolicy.Version
// (0.14.0 raised it to 4) left every installation holding an older node with
// one way forward, 'fugaro project policy publish --delete' and a fresh
// publish: a deletion, and one that loses every field the owner had edited into
// the node by hand (allow_repos, clone_urls, routing, profiles, accounts,
// blocks no release models). The migration moves the node instead
// (projectpolicy.Migrate) and adds what the version this Fugaro writes needs
// and the node alone cannot state: the bitbucket_token block and
// bitbucket_api_roots, derived from the project's own local config with the
// publisher's own functions.
//
// The write is a writer's write, so it holds the writer's posture (P8): a node
// carrying keys this Fugaro does not know is refused rather than rewritten, and
// the refusal names the count and never a value. Migrate preserves those keys —
// its Change.Kept is read off the node for exactly that reason — so the refusal
// is this stage's decision and not the migration's: a node whose unknown blocks
// only a newer Fugaro can read is that Fugaro's to migrate.

// policyMigration is one read of the published node and the node it would be
// written as: the plan the operator is shown, and the write itself. Everything
// the write needs is read once, here, because the ETag the write is conditional
// on is the ETag of the node the operator read the diff against — a second read
// between the confirmation and the write would make the confirmed diff and the
// written document two different things.
type policyMigration struct {
	lc      *localcfg.Config
	db      *rtdb.Client
	etag    string // the published node's ETag, as read; the write is conditional on it
	from    int
	moved   []byte // the node as it will be written: Migrate's output, plus what was derived
	changes []projectpolicy.Change
	derived []string // the top-level members derived from the local config, sorted
}

// policyNodeUnreachable is whether this project has no published policy node to
// read at all. The node lives in the project's Realtime Database, so a config
// that names no budget backend has none — not a failure of this stage but the
// absence of the thing it migrates, which is why it is a skip and not an error
// (a config written before the budget backend, or one whose operator never
// configured it, is a state `fugaro update` must not stop on).
func policyNodeUnreachable(lc *localcfg.Config) bool {
	return lc.Budget == nil || lc.Budget.RTDBURL == ""
}

// policyMigrationNotOurs is the answer policyMigrationFor gives for a database
// the shared ownership predicate refuses (budgetDBUnadopted): there is nothing
// to migrate HERE, and the callers turn it into the skip their own vocabulary
// has — `fugaro update`'s stage into a skipped stage, `fugaro upgrade` into a
// skipped step — rather than into an error, because the rest of the run has
// nothing to do with that database, exactly as the rules stage's skip is not a
// refusal.
//
// It is a type and not a plain error so a caller cannot mistake it for a read
// that failed (the two are the only two things this function returns): a failed
// read is the run's error, and this is a state the operator resolves with the
// sentence the refusal carries — Check's own words, or the command the config's
// half names.
type policyMigrationNotOurs struct{ reason string }

func (e *policyMigrationNotOurs) Error() string { return e.reason }

// policyMigrationFor reads the published node and works out what it would be
// written as. published is whether a node is there at all — a project with a
// budget backend and no published policy is a state of its own, and one the
// delete and show paths already word ("runs fall back to the ceiling their
// job's environment carries"), not the same thing as a node already at this
// version. The migration is nil when there is nothing to migrate.
//
// It is a writer, so it holds a writer's gate (budgetDBUnadopted), and it holds
// it HERE, before the node is read: this is the second stage that writes into
// the budget database, and the rules stage being skipped over a database the
// installation never adopted does not stop this one from migrating the node in
// it — the read, the ETag and the write all land in that same database, and
// `fugaro upgrade` reaches this function with no rules stage in front of it at
// all.
//
// That gate is the rules stage's own, the same function and not a second one
// worded alike (the second review's finding: the two drifted, and a database the
// rules stage skipped came out writable for this stage). It is asked of every
// shape — no Firebase project recorded, the installation's own project with a
// URL pointing at the database beside it, a project other than the
// installation's own — so the two stages answer the same question the same way
// by construction, and the ordinary installation (budget.firebase_project equal
// to gcp_project, its own database and its own mark) asks a read it already had
// to make anyway.
func policyMigrationFor(ctx context.Context, lc *localcfg.Config) (m *policyMigration, published bool, err error) {
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, false, err
	}
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "this stage migrates no policy there", selfCommand()+" update"); why != "" {
		return nil, false, &policyMigrationNotOurs{reason: why}
	}
	etag, found, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	node, derr := withDerivedMembers(raw, lc)
	moved, changes, err := projectpolicy.Migrate(node)
	switch {
	case err == nil && len(changes) == 0:
		// A node already at this version: nothing is written, so nothing is
		// derived either, and the derivation's refusal is not this stage's to
		// make here. It is a config a publish would refuse (an allow_data_to
		// entry the policy's own reader rejects), and the operator meets it on
		// the publish that derives the block — not as a failure of the policy
		// stage of every fugaro update of a node nobody is migrating.
		return nil, true, nil
	case derr != nil:
		// The derivation's own error is the refusal whether or not Migrate
		// had one: a member the local config cannot state is a config a
		// publish refuses too, and carrying on writes a node without the
		// block a publish would have put there — the state a proxied run's
		// whole admission is missing from. It also says more than Migrate's
		// note can about the node it did refuse: the note says only that the
		// block belongs to a publish, the derivation names the entry to fix.
		return nil, true, derr
	case err != nil:
		return nil, true, policyMigrationRefusal(err)
	}
	// The write is the publisher's write, so it holds the publisher's gate
	// (P3, requirePolicyRules): a policy node is read by the runs a deployed
	// rules generation admits, and one written under rules that predate the
	// policy model is a policy nothing reads. It is checked only when a
	// migration is pending — a node already at this version is left alone
	// whatever the deployed rules are — and it is checked here rather than at
	// the write because the operator reads the diff before it: a refusal after
	// the typed confirmation would be a document they confirmed and did not
	// get.
	if err := requirePolicyRules(ctx, db, lc); err != nil {
		return nil, true, err
	}
	if err := refuseMigrationUnknownKeys(moved); err != nil {
		return nil, true, err
	}
	return &policyMigration{lc: lc, db: db, etag: etag, from: changes[0].From,
		moved: moved, changes: changes, derived: derivedMembers(moved, raw)}, true, nil
}

// policyMigrationRefusal is Migrate's own refusal with the remedy that fits
// this side of the skew: a *VersionError carries the shared sentence alone
// (Migrate's contract: the caller prints SkewSentence), so a node ahead of this
// Fugaro is told to upgrade the binary and one behind it — a version below the
// chain's first step, 1 or 2, which 0.8.0 and 0.8.5 wrote and no step reaches
// (projectpolicy.VersionSince) — is told the move that works. Anything else, a
// malformed node among them, is returned as it came: it is not a skew and has
// no version to word.
func policyMigrationRefusal(err error) error {
	var ve *projectpolicy.VersionError
	if errors.As(err, &ve) {
		return userErr("%s", projectpolicy.SkewSentence(ve, projectpolicy.ReaderCLI))
	}
	return err
}

// policyMigrationConfirm is the tail both doors run, to the line and in the
// order: the in-flight refusal, the diff, and the write behind the
// confirmation the delete path takes. It is one function because the decision
// is one decision — the node every run of the project is admitted by,
// rewritten with what the operator has just read — and all three of its parts
// are that decision: a sentence worded twice is a sentence that can drift, and
// an order kept twice is one that can differ, which is what it was before this
// (the step printed the diff and then refused, showing a document the operator
// could not act on without a second run of the same command).
//
// What each door does around it is its own: the agent-session refusal and the
// absent node's detail, and how a failure is worded where the door reports one.
func policyMigrationConfirm(ctx context.Context, cmd *cobra.Command, m *policyMigration, yes bool, w io.Writer) error {
	inflight, err := runsInFlight(ctx, m.db, m.lc)
	if err != nil {
		return err
	}
	if inflight > 0 && !yes {
		return userErr("%d run(s) are in flight: a run that is mid-flight read the policy this migration would rewrite, so it is refused without --yes; wait for them (fugaro runs ls), or pass --yes once you have read what it does", inflight)
	}
	m.print(w)
	if err := confirmRemoval(cmd, m.lc, yes, policyMigrationWhat+": every run of the project reads it, and the fields you edited into it by hand are carried over rather than published again"); err != nil {
		return err
	}
	return m.write(ctx)
}

// derivedMembers is the top-level members the node gained on the way from raw
// to moved: what the publisher's own derivations added. It is read off the two
// documents rather than tracked as the derivation runs, because Migrate itself
// adds members too (the version) and the diff has to say which is which.
func derivedMembers(moved, raw []byte) []string {
	var out []string
	for _, key := range []string{bitbucketTokenKey, bitbucketAPIRootsKey} {
		before, was, err := jsonMember(raw, key)
		if err == nil && was && !projectpolicy.IsNull(before) {
			continue
		}
		now, is, err := jsonMember(moved, key)
		if err == nil && is && !projectpolicy.IsNull(now) {
			out = append(out, key)
		}
	}
	return out
}

const (
	bitbucketTokenKey    = "bitbucket_token"
	bitbucketAPIRootsKey = "bitbucket_api_roots"
	policyMigrationWhat  = "this rewrites the published policy node in place"
	policyNoDBReason     = "the project's config names no Firebase budget backend, so there is no published policy node to read"
)

// withDerivedMembers patches into the node the members a publish would have
// derived from the project's local config and the node cannot state — the
// bitbucket_token block and the bitbucket_api_roots allow-list — using the
// publisher's own functions (DeriveBitbucketToken, and the config's
// bitbucket_api_roots, which the publisher copies). The returned node is the
// input byte for byte when there was nothing to add.
//
// It runs whether or not the migration would have refused, and before it: a
// version-3 node whose profile mounts the project's Bitbucket credential is
// refused by Migrate (the block that credential is bounded by is derived at
// publish, and a step whose only input is the node cannot state it), but a node
// with no profile mounting the token migrates cleanly without the block — and
// the migrated node is then read by every run of the project, where a block a
// publish would have written is missing and the project's Bitbucket runs fail
// closed. Both are the same omission and get the same answer.
//
// A member the node already carries is never rewritten: the block is the
// node's, and the owner may have narrowed its allow-list by hand. The error is
// the derivation's own (the config's allow_data_to cannot become a policy
// entry), and it is the caller's to place: only a write that would carry the
// block — a pending migration — is stopped by it, because a node that is not
// migrated derives nothing and so needs nothing.
func withDerivedMembers(raw []byte, lc *localcfg.Config) ([]byte, error) {
	carries := func(key string) bool {
		before, was, err := jsonMember(raw, key)
		return err == nil && was && !projectpolicy.IsNull(before)
	}
	node := raw
	if !carries(bitbucketTokenKey) {
		token, err := projectpolicy.DeriveBitbucketToken(policyProfiles(lc), lc.ProjectSecrets)
		if err != nil {
			return raw, userErr("%v", err)
		}
		if token != nil {
			if node, err = patchPolicyJSON(node, bitbucketTokenKey, token); err != nil {
				return raw, userErr("deriving the policy's %s block from %s: %v", bitbucketTokenKey, lc.Name, err)
			}
		}
	}
	if !carries(bitbucketAPIRootsKey) && len(lc.BitbucketAPIRoots) > 0 {
		var err error
		if node, err = patchPolicyJSON(node, bitbucketAPIRootsKey, lc.BitbucketAPIRoots); err != nil {
			return raw, userErr("carrying %s's %s allow-list into the policy: %v", lc.Name, bitbucketAPIRootsKey, err)
		}
	}
	return node, nil
}

// refuseMigrationUnknownKeys is P8 at this stage's write: a node carrying
// top-level keys this Fugaro does not model is refused, and the node is left
// exactly as it was.
//
// The count is the whole of what the refusal says about the keys — not their
// values, which a node may hold anything under — and the remedy is the one that
// works: a newer Fugaro knows what they mean, so it, and then `fugaro update`,
// is the move. It is deliberately not 'fix the node by hand': the operator of a
// node written by a release above this binary has nothing to fix.
func refuseMigrationUnknownKeys(moved []byte) error {
	p, found, err := projectpolicy.Decode(moved)
	if err != nil || !found || len(p.UnknownKeys) == 0 {
		return nil
	}
	return userErr("%s carries %d key(s) this Fugaro does not know (%s), and migrating it would write a document whose meaning this binary cannot read: nothing was written; upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run %s again",
		projectpolicy.PathShared(), len(p.UnknownKeys), unknownPolicyKeysText(p.UnknownKeys), selfCommand()+" update")
}

// write puts the migrated node back with the ETag it was read at. A concurrent
// writer — another publish, a hand edit through the console — makes the
// precondition fail, and that is a refusal, not a retry: the operator confirmed
// a diff of the node this write replaces, and a write that suppressed a change
// nobody read would be exactly the unasked rewrite `--delete` exists to avoid.
func (m *policyMigration) write(ctx context.Context) error {
	if err := m.db.PutIfMatch(ctx, projectpolicy.PathShared(), m.etag, json.RawMessage(m.moved)); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return userErr("the policy node at %s changed while this migration was being confirmed (another publish landed meanwhile), so nothing was written: read what the node now holds with 'fugaro project policy show', then run %s again",
				projectpolicy.PathShared(), selfCommand()+" update")
		}
		return dbErr(m.lc, err, true)
	}
	return nil
}

// print is the one confirmation diff: what each step changes, what stays as it
// was, what had to come from the local config, and — when the node mounts the
// project's Bitbucket credential and no block could be derived — the republish
// that is still the operator's move (Migrate's own note, passed through rather
// than worded a second time here).
func (m *policyMigration) print(w io.Writer) {
	fmt.Fprintf(w, "%s: published (version %d) and rewritten in place as version %d; nothing is removed\n",
		projectpolicy.PathShared(), m.from, projectpolicy.Version)
	for _, ch := range m.changes {
		fmt.Fprintf(w, "  %d to %d: %s\n", ch.From, ch.To, ch.Changed)
		kept := make([]string, 0, len(ch.Kept))
		for _, name := range ch.Kept {
			if !slices.Contains(m.derived, name) {
				kept = append(kept, name)
			}
		}
		if len(kept) > 0 {
			fmt.Fprintf(w, "    kept as it is: %s\n", strings.Join(kept, ", "))
		}
		if ch.Note != "" && !slices.Contains(m.derived, bitbucketTokenKey) {
			fmt.Fprintf(w, "    %s\n", ch.Note)
		}
	}
	if len(m.derived) > 0 {
		fmt.Fprintf(w, "  derived from the project's local config, as a publish derives them: %s\n", strings.Join(m.derived, ", "))
	}
}

// policyMigrateStage is the stage itself. It is SelfConfirming, like the
// engines: it shows its own diff and takes its own confirmation, and refuses a
// coding agent's session and a run with no terminal and no --yes.
type policyMigrateStage struct {
	e         *initEngine
	planned   bool
	m         *policyMigration
	published bool
	err       error
}

// policyNoneDetail is what both doors say for a project that has a budget
// backend and no node in it. It is the state the delete and show paths already
// word, and it is not the same thing as a node already at this version: an
// operator told "the published policy is version 4" would go looking for a
// policy that is not there.
func policyNoneDetail() string {
	return fmt.Sprintf("no policy is published at %s: runs fall back to the ceiling their job's environment carries", projectpolicy.PathShared())
}

func newPolicyMigrateStage(e *initEngine) *policyMigrateStage { return &policyMigrateStage{e: e} }

func (policyMigrateStage) Name() string    { return initflow.Policy }
func (policyMigrateStage) SelfConfirming() {}

func (policyMigrateStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Policy, Kind: initflow.LeftCommand, Text: selfCommand() + " update"}
}

// plan reads the node once per run: the stage's check, its plan and its apply
// all ask the same question, and the answer is what the confirmation is made
// against.
func (s *policyMigrateStage) plan(ctx context.Context) (*policyMigration, bool, error) {
	if !s.planned {
		s.planned = true
		s.m, s.published, s.err = policyMigrationFor(ctx, s.e.lc)
	}
	return s.m, s.published, s.err
}

// forget drops the read the check, the plan and the apply share for the whole
// run, so the next of them reads the node and the rules it is gated on again.
// It exists because a run changes under this stage's feet: the rules stage
// (init_rules_stage.go) runs before it and deploys the generation this stage's
// write is gated on (requirePolicyRules), while the read-only preview pass —
// every stage's check, before the first apply — is where this stage's refusal
// is made. Without this the refusal cached there outlives the deploy that
// answered it, and the run refuses its own work with a sentence about rules
// that are no longer deployed.
func (s *policyMigrateStage) forget() {
	s.planned, s.m, s.published, s.err = false, nil, false, nil
}

// Check reports what the published node is. A refusal here is the run's, not a
// step left for the user: a malformed node, or one written by a newer Fugaro,
// is not something the operator of this binary can finish by hand, and the
// message says which of the two it is.
func (s *policyMigrateStage) Check(ctx context.Context) (initflow.Status, error) {
	if policyNodeUnreachable(s.e.lc) {
		return initflow.Status{State: initflow.Skipped, Detail: policyNoDBReason}, nil
	}
	m, published, err := s.plan(ctx)
	var notOurs *policyMigrationNotOurs
	if errors.As(err, &notOurs) {
		return initflow.Status{State: initflow.Skipped, Detail: notOurs.reason}, nil
	}
	if err != nil {
		return initflow.Status{}, err
	}
	switch {
	case !published:
		return initflow.Status{State: initflow.Done, Detail: policyNoneDetail()}, nil
	case m == nil:
		return initflow.Status{State: initflow.Done, Detail: fmt.Sprintf("the published policy is version %d", projectpolicy.Version)}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the published policy is version %d and this Fugaro writes version %d", m.from, projectpolicy.Version)}, nil
}

func (s *policyMigrateStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	if policyNodeUnreachable(s.e.lc) {
		return initflow.Plan{NothingToDo: true, Detail: policyNoDBReason}, nil
	}
	m, published, err := s.plan(ctx)
	var notOurs *policyMigrationNotOurs
	if errors.As(err, &notOurs) {
		return initflow.Plan{NothingToDo: true, Detail: notOurs.reason}, nil
	}
	if err != nil {
		return initflow.Plan{}, err
	}
	switch {
	case !published:
		return initflow.Plan{NothingToDo: true, Detail: policyNoneDetail()}, nil
	case m == nil:
		return initflow.Plan{NothingToDo: true, Detail: fmt.Sprintf("the published policy is version %d", projectpolicy.Version)}, nil
	}
	if env.Interactive || env.Yes {
		m.print(s.e.r.cmd.OutOrStdout())
	}
	return initflow.Plan{Detail: fmt.Sprintf("migrate the published policy from version %d to %d in place, after your confirmation", m.from, projectpolicy.Version)}, nil
}

// Apply is the write. The refusals are the ones the delete path has, in the
// same order and the same words where they are the same refusal: a coding
// agent's session, a run in flight without --yes, no terminal (--yes is the
// confirmation then), and the typed project name. --yes is the way past the
// in-flight refusal once the operator has read what the migration does, which
// is what that refusal itself tells them — the same reading
// `fugaro project policy publish --delete` has (internal/cli/project_cmd.go).
func (s *policyMigrateStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if marker := agentMarker(os.Getenv); marker != "" {
		return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
	}
	if policyNodeUnreachable(s.e.lc) {
		return initflow.Outcome{Detail: policyNoDBReason}, nil
	}
	m, _, err := s.plan(ctx)
	var notOurs *policyMigrationNotOurs
	if errors.As(err, &notOurs) {
		return initflow.Outcome{Detail: notOurs.reason}, nil
	}
	if err != nil {
		return initflow.Outcome{}, err
	}
	if m == nil {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	cmd := s.e.r.cmd
	if err := policyMigrationConfirm(ctx, cmd, m, env.Yes, cmd.OutOrStdout()); err != nil {
		return initflow.Outcome{}, err
	}
	return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("migrated the published policy from version %d to %d in place", m.from, projectpolicy.Version)}, nil
}

// Verify reads the node back: a write that claims a version it did not land is
// a policy this Fugaro would keep rewriting on every run.
//
// The version is not the whole of the answer. Migrate short-circuits on a node
// already at this version (from == Version), so it never reaches the reader
// gate it ends its walk with — and that gate judged the document the walk was
// about to write, not the bytes the database now holds. Decode is that gate,
// called here on what was read back: a node at this version that the current
// reader refuses is a write that landed a policy no run of the project can
// admit itself by.
func (s *policyMigrateStage) Verify(ctx context.Context) error {
	if s.m == nil {
		return nil
	}
	_, found, raw, err := readPolicyNode(ctx, s.m.db, s.m.lc)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("the published policy is gone")
	}
	_, changes, err := projectpolicy.Migrate(raw)
	if err != nil {
		return err
	}
	if len(changes) > 0 {
		return fmt.Errorf("the published policy is still version %d", changes[0].From)
	}
	if _, _, err := projectpolicy.Decode(raw); err != nil {
		return fmt.Errorf("the published policy was written but cannot be read back: %w", err)
	}
	return nil
}
