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

// policyMigrationFor reads the published node and works out what it would be
// written as, or nil when there is nothing to migrate: no node is published, or
// it is already the version this Fugaro writes.
func policyMigrationFor(ctx context.Context, lc *localcfg.Config) (*policyMigration, error) {
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, err
	}
	etag, found, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil || !found {
		return nil, err
	}
	moved, changes, err := projectpolicy.Migrate(raw)
	if err != nil {
		if moved, changes, err = migrateWithDerivedMembers(raw, lc, err); err != nil {
			return nil, err
		}
	}
	if len(changes) == 0 {
		return nil, nil
	}
	if err := refuseMigrationUnknownKeys(moved); err != nil {
		return nil, err
	}
	return &policyMigration{lc: lc, db: db, etag: etag, from: changes[0].From,
		moved: moved, changes: changes, derived: derivedMembers(moved, raw)}, nil
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
)

// migrateWithDerivedMembers retries the migration with the members Migrate
// cannot derive from the node — the Bitbucket block and the API-root
// allow-list — patched in from the project's local config, using the
// publisher's own functions (DeriveBitbucketToken, and the config's
// bitbucket_api_roots, which the publisher copies).
//
// It runs only when Migrate failed with a note, which is Migrate's own way of
// saying the local config is what is missing: a version-3 node whose profile
// mounts the project's Bitbucket credential is refused there because the block
// that credential is bounded by (bitbucket_token.allow_repos) is derived at
// publish, and a step whose only input is the node cannot state it. Everything
// else — a malformed node, a node ahead of this Fugaro — is that failure, as it
// came.
//
// The members are patched into the node and the migration is retried rather
// than written after it, so the retried Migrate judges the document the node
// actually becomes (its own strict read of the result is what accepts it), and
// so the diff the operator reads is a diff of bytes that were migrated.
func migrateWithDerivedMembers(raw []byte, lc *localcfg.Config, first error) ([]byte, []projectpolicy.Change, error) {
	var me *projectpolicy.MalformedError
	if !errors.As(first, &me) || me.Note == "" {
		return nil, nil, first
	}
	profiles := policyProfiles(lc)
	token, err := projectpolicy.DeriveBitbucketToken(profiles, lc.ProjectSecrets)
	if err != nil {
		return nil, nil, userErr("%v", err)
	}
	node := raw
	if token != nil {
		if node, err = patchPolicyJSON(node, bitbucketTokenKey, token); err != nil {
			return nil, nil, userErr("deriving the policy's %s block from %s: %v", bitbucketTokenKey, lc.Name, err)
		}
	}
	if len(lc.BitbucketAPIRoots) > 0 {
		if node, err = patchPolicyJSON(node, bitbucketAPIRootsKey, lc.BitbucketAPIRoots); err != nil {
			return nil, nil, userErr("carrying %s's %s allow-list into the policy: %v", lc.Name, bitbucketAPIRootsKey, err)
		}
	}
	if len(derivedMembers(node, raw)) == 0 {
		return nil, nil, first
	}
	return projectpolicy.Migrate(node)
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
	return userErr("%s carries %d key(s) this Fugaro does not know (%s), and migrating it would write a document whose meaning this binary cannot read: nothing was written; upgrade Fugaro (fugaro upgrade), then run %s again",
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
	e       *initEngine
	planned bool
	m       *policyMigration
	err     error
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
func (s *policyMigrateStage) plan(ctx context.Context) (*policyMigration, error) {
	if !s.planned {
		s.planned = true
		s.m, s.err = policyMigrationFor(ctx, s.e.lc)
	}
	return s.m, s.err
}

// Check reports what the published node is. A refusal here is the run's, not a
// step left for the user: a malformed node, or one written by a newer Fugaro,
// is not something the operator of this binary can finish by hand, and the
// message says which of the two it is.
func (s *policyMigrateStage) Check(ctx context.Context) (initflow.Status, error) {
	m, err := s.plan(ctx)
	if err != nil {
		return initflow.Status{}, err
	}
	if m == nil {
		return initflow.Status{State: initflow.Done, Detail: fmt.Sprintf("the published policy is version %d", projectpolicy.Version)}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the published policy is version %d and this Fugaro writes version %d", m.from, projectpolicy.Version)}, nil
}

func (s *policyMigrateStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	m, err := s.plan(ctx)
	if err != nil {
		return initflow.Plan{}, err
	}
	if m == nil {
		return initflow.Plan{NothingToDo: true, Detail: fmt.Sprintf("the published policy is version %d", projectpolicy.Version)}, nil
	}
	if env.Interactive || env.Yes {
		m.print(s.e.r.cmd.OutOrStdout())
	}
	return initflow.Plan{Detail: fmt.Sprintf("migrate the published policy from version %d to %d in place, after your confirmation", m.from, projectpolicy.Version)}, nil
}

// Apply is the write. The refusals are the four the delete path has, in the
// same order and the same words where they are the same refusal: a coding
// agent's session, a run in flight (which --yes does not confirm here either),
// no terminal (--yes is the confirmation then), and the typed project name.
func (s *policyMigrateStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if marker := agentMarker(os.Getenv); marker != "" {
		return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
	}
	m, err := s.plan(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if m == nil {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	cmd := s.e.r.cmd
	inflight, err := runsInFlight(ctx, m.db, m.lc)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if inflight > 0 && !env.Yes {
		return initflow.Outcome{}, userErr("%d run(s) are in flight: a run that is mid-flight read the policy this migration would rewrite, so it is refused without --yes; wait for them (fugaro runs ls), or pass --yes once you have read what it does", inflight)
	}
	m.print(cmd.OutOrStdout())
	if err := confirmRemoval(cmd, m.lc, env.Yes, policyMigrationWhat+": every run of the project reads it, and the fields you edited into it by hand are carried over rather than published again"); err != nil {
		return initflow.Outcome{}, err
	}
	if err := m.write(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("migrated the published policy from version %d to %d in place", m.from, projectpolicy.Version)}, nil
}

// Verify reads the node back: a write that claims a version it did not land is
// a policy this Fugaro would keep rewriting on every run.
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
	return nil
}
