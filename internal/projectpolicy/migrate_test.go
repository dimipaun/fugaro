package projectpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// migrateFixture is a policy node as it is stored, read off testdata. The
// fixtures are the nodes an installation holds, not documents this package
// writes: the migration's whole input is a node written by a release this one
// has to move forward.
func migrateFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestMigrateFromTheOldestNodePreservesHandEditedAllowList: the state this
// migration exists for. A version-3 node is one today's reader refuses, so an
// installation that published under 0.13.x can only reach this release's policy
// by deleting the node and publishing again — which loses every entry the owner
// edited by hand. The migrator walks the node forward instead, one step per
// version, and everything the node carries survives: the hand-edited allow_repos
// entry, clone_urls, routing, the profiles and the accounts block.
func TestMigrateFromTheOldestNodePreservesHandEditedAllowList(t *testing.T) {
	raw := migrateFixture(t, "policy-v3-hand-edited.json")

	// The premise, asserted here so the fixture cannot drift into a node this
	// test is not about: the current reader refuses it as a version skew.
	if _, _, err := Decode(raw); err == nil {
		t.Fatal("the version-3 fixture decodes with the current reader, so it is not a node this migration is for")
	} else {
		var ve *VersionError
		if !errors.As(err, &ve) || ve.Found != 3 {
			t.Fatalf("Decode(the version-3 fixture) = %v, want a version-3 skew refusal", err)
		}
	}

	migrated, changes, err := Migrate(raw)
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	p, found, err := Decode(migrated)
	if err != nil || !found || p == nil {
		t.Fatalf("the migrated node does not decode: Decode = (%v, %v, %v)", p, found, err)
	}
	if p.Version != Version {
		t.Errorf("the migrated node is version %d, want %d", p.Version, Version)
	}
	// github/owner/repo-b is the entry the owner wrote into the node by hand:
	// it is on no list the local config derives, so a --delete-and-publish
	// loses it and the migration must not.
	if want := []string{"github/owner/repo-a", "github/owner/repo-b"}; !slices.Equal(p.AllowRepos, want) {
		t.Errorf("allow_repos = %v, want %v", p.AllowRepos, want)
	}
	if got, want := p.CloneURLs["github/owner/repo-a"], "https://github.com/owner/repo-a.git"; got != want {
		t.Errorf("clone_urls[github/owner/repo-a] = %q, want %q", got, want)
	}
	if p.Routing == nil || p.Routing.Claude == nil || p.Routing.Claude.Via != "auto" || !p.Routing.Claude.Names("claude-sonnet-5-5") {
		t.Errorf("routing = %+v, want the claude switch the node carries", p.Routing)
	}
	pr, ok := p.Profiles[DefaultProfileName]
	if !ok {
		t.Fatalf("the migrated node defines no %s profile: %v", DefaultProfileName, p.ProfileNames())
	}
	if pr.Provider != "github" || !slices.Equal(pr.ClaudeAccounts, []string{"ops"}) {
		t.Errorf("profiles.%s = %+v, want the github profile serving ops", DefaultProfileName, pr)
	}
	if got := p.Accounts["ops"].AllowRepos; !slices.Equal(got, []string{"github/owner/repo-a"}) {
		t.Errorf("accounts.ops.allow_repos = %v, want the account the node carries", got)
	}

	// The report a caller renders as the diff: a step per version in between,
	// each naming what it kept. Nothing here asks the operator for anything
	// more — this node has no pool to mark (the craft blocks came later).
	if len(changes) != Version-3 {
		t.Fatalf("changes = %+v, want one step per version from 3 to %d", changes, Version)
	}
	if changes[0].From != 3 || changes[0].To != 4 {
		t.Errorf("changes[0] = %+v, want the step from version 3 to version 4", changes[0])
	}
	if changes[0].Changed == "" {
		t.Error("changes[0].Changed is empty: the diff must say what the step changed")
	}
	// Kept is read off the node, not off a list in the step: every top-level key
	// but version, including the block no release of this Fugaro models. That is
	// what makes the diff an operator reads a claim about *their* node.
	for _, kept := range []string{"allow_repos", "clone_urls", "routing", "profiles", "accounts", "future_release_block"} {
		if !slices.Contains(changes[0].Kept, kept) {
			t.Errorf("changes[0].Kept = %v, want it to name %s", changes[0].Kept, kept)
		}
	}
	if slices.Contains(changes[0].Kept, "version") {
		t.Errorf("changes[0].Kept = %v, want it to leave out the one field the step writes", changes[0].Kept)
	}
	if changes[0].Note != "" {
		t.Errorf("changes[0].Note = %q, want no further move for a node with no Bitbucket profile", changes[0].Note)
	}
}

// TestMigrateEditsOnlyTheBytesItWrites: the steps edit the node in place,
// so the migration is the owner's document with one field written and not a
// re-encoding of it. Decoding into a map and marshalling it again would sort
// the keys, compact the whitespace, escape <, > and &, and fold a repeated key
// onto the last one — semantics survive that, but a caller that diffs the two
// nodes, or skips a write it believes changed nothing, is reading a document
// that is not the one it holds. The fixture carries a block this Fugaro does
// not model, with a value encoding/json would escape, so the property is
// pinned rather than argued.
func TestMigrateEditsOnlyTheBytesItWrites(t *testing.T) {
	raw := migrateFixture(t, "policy-v3-hand-edited.json")
	migrated, _, err := Migrate(raw)
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	want := bytes.Replace(raw, []byte(`"version": 3`), []byte(`"version": 7`), 1)
	if !bytes.Equal(migrated, want) {
		t.Errorf("the migrated node is not the node with its version rewritten:\n got %s\nwant %s", migrated, want)
	}
	// The unmodelled block comes back with its own bytes, including the & and
	// < a re-encoding would write as escapes: that is the difference between
	// editing the owner's document and writing a new one that means the same.
	if !bytes.Contains(migrated, []byte(`"escaped": "a & b <c>"`)) {
		t.Error("the migrated node does not hold the unmodelled block's value as the fixture wrote it, so the step re-encoded the document rather than editing it")
	}
	p, found, err := Decode(migrated)
	if err != nil || !found || p == nil {
		t.Fatalf("the migrated node does not decode: (%v, %v, %v)", p, found, err)
	}
	// The unmodelled block is carried, and the reader reports it as unmodelled
	// rather than reading it: a step that re-encoded a typed block would drop a
	// newer release's key with nothing failing, which is what this guards.
	if !slices.Contains(p.UnknownKeys, "future_release_block") {
		t.Errorf("the migrated node's unknown keys = %v, want the block the fixture carries", p.UnknownKeys)
	}
}

// TestMigrateRoundTripsThroughTheStrictReader: what the migrator writes
// is a node the current reader reads, and it is the same policy the node was —
// every field but the version is preserved in meaning, which is what comparing
// the two decoded documents says. The migrated node is also a fixed point:
// migrating it again is the no-op.
func TestMigrateRoundTripsThroughTheStrictReader(t *testing.T) {
	raw := migrateFixture(t, "policy-v3-hand-edited.json")
	migrated, _, err := Migrate(raw)
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	got, found, err := Decode(migrated)
	if err != nil || !found {
		t.Fatalf("the migrated node does not decode: (%v, %v)", found, err)
	}
	// The fixture with its version rewritten by hand is what "nothing but the
	// version changed" means, and the strict reader is the judge of it.
	hand := bytes.Replace(raw, []byte(`"version": 3`), []byte(`"version": 7`), 1)
	want, found, err := Decode(hand)
	if err != nil || !found {
		t.Fatalf("the hand-rewritten fixture does not decode: (%v, %v)", found, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the migrated node decodes to %+v, want the same policy as the hand-rewritten node %+v", got, want)
	}

	again, changes, err := Migrate(migrated)
	if err != nil {
		t.Fatalf("Migrate(the migrated node) = %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("Migrate(the migrated node) reported %+v, want no changes", changes)
	}
	if !bytes.Equal(again, migrated) {
		t.Errorf("Migrate(the migrated node) = %s, want it back byte for byte", again)
	}
}

// TestMigrateRefusesANewerNode: a node ahead of this Fugaro is not one a
// migration can move, and the refusal is the one every too-new reader already
// words — a *VersionError, so the caller prints the sentence that fits its side
// (SkewSentence) instead of a message of its own.
func TestMigrateRefusesANewerNode(t *testing.T) {
	for _, v := range []int{Version + 1, 99} {
		raw := []byte(`{"version":` + itoa(v) + `,"budget":{"mode":"enforce"}}`)
		migrated, changes, err := Migrate(raw)
		var ve *VersionError
		if !errors.As(err, &ve) {
			t.Fatalf("Migrate(version %d) = %v, want a *VersionError", v, err)
		}
		if ve.Found != v || ve.Understood != Version {
			t.Errorf("the refusal carries (%d, %d), want (%d, %d)", ve.Found, ve.Understood, v, Version)
		}
		if !bytes.Equal(migrated, raw) {
			t.Errorf("Migrate(version %d) returned %s, want the input untouched", v, migrated)
		}
		if len(changes) != 0 {
			t.Errorf("Migrate(version %d) reported %+v, want no changes", v, changes)
		}
		for _, reader := range []Reader{ReaderCLI, ReaderRunner} {
			// The too-new remedy differs per reader: the CLI's binary is the
			// package manager's to replace (`fugaro update` only moves a
			// checkout onto it), the job's image is refreshed.
			remedy := "brew upgrade dimipaun/tap/fugaro"
			if reader == ReaderRunner {
				remedy = "refresh the runner image (fugaro update)"
			}
			if msg := SkewSentence(ve, reader); !strings.Contains(msg, remedy) {
				t.Errorf("SkewSentence(%d over %d, %v) = %q, want the too-new remedy", v, Version, reader, msg)
			}
		}
	}
}

// TestMigrateRefusesAMalformedNodeUntouched: a node the migration cannot read
// whole is refused, and the input comes back untouched so a caller that
// ignores the error cannot write a half-migrated document over it. The classes
// are the ones a hand-written or corrupt node falls into: not JSON at all, not
// an object, no version, a version no release ever wrote, a version that is
// not an integer, a top-level key written twice, and a node whose migrated
// form the strict reader still refuses.
//
// The last of those is refused after the step has run, so it is the one case
// that reports the step it read: what the migration would have done is part of
// why it stopped.
func TestMigrateRefusesAMalformedNodeUntouched(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		changes int
	}{
		{"not JSON", `{"version": 3,`, 0},
		{"not an object", `[3]`, 0},
		{"no version", `{"budget": {"mode": "enforce"}}`, 0},
		{"version 0", `{"version": 0, "budget": {"mode": "enforce"}}`, 0},
		{"a negative version", `{"version": -1}`, 0},
		{"a version that is not an integer", `{"version": "3"}`, 0},
		{"a version written twice", `{"version": 3, "version": 3}`, 0},
		{"a top-level key written twice", `{"version": 3, "allow_repos": [], "allow_repos": ["github/owner/repo-a"]}`, 0},
		{"a migrated node the reader refuses", `{"version": 3, "allow_repos": ["nope"]}`, Version - 3},
	} {
		raw := []byte(tc.raw)
		migrated, changes, err := Migrate(raw)
		var me *MalformedError
		if !errors.As(err, &me) {
			t.Errorf("%s: Migrate = %v, want a *MalformedError", tc.name, err)
			continue
		}
		if !strings.Contains(err.Error(), "cannot be migrated") {
			t.Errorf("%s: %v does not name the class of the refusal", tc.name, err)
		}
		if !bytes.Equal(migrated, raw) {
			t.Errorf("%s: Migrate returned %s, want the input untouched", tc.name, migrated)
		}
		if len(changes) != tc.changes {
			t.Errorf("%s: Migrate reported %+v, want %d change(s)", tc.name, changes, tc.changes)
		}
	}
}

// TestMigrateCurrentIsANoOp: a node already at Version has nothing to migrate,
// and Migrate says so by handing it back byte for byte with no changes — it
// does not re-judge a node it has nothing to do to, which is Decode's answer
// and not its own.
func TestMigrateCurrentIsANoOp(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"version":7,"budget":{"mode":"enforce"},"allow_repos":["github/owner/repo-a"]}`),
		[]byte(`{"version":7,"allow_repos":["nope"]}`),
	} {
		migrated, changes, err := Migrate(raw)
		if err != nil {
			t.Fatalf("Migrate(%s) = %v, want a no-op", raw, err)
		}
		if len(changes) != 0 {
			t.Errorf("Migrate(%s) reported %+v, want no changes", raw, changes)
		}
		if !bytes.Equal(migrated, raw) {
			t.Errorf("Migrate(%s) = %s, want the node back byte for byte", raw, migrated)
		}
	}
}

// TestMigrateReportsTheBitbucketRepublish: a version-3 node carrying a profile
// that mounts the project's Bitbucket credential cannot be migrated by a pure
// library. The bitbucket_token block version 4 reads is derived at publish from
// the project's local config, so no step may invent it from the node, and the
// node without it is one the current reader refuses. The migration stops
// there, and both the change list and the refusal itself say the one move that
// finishes the job: republish, which derives the block. The refusal has to say
// it and not only sit beside it — the caller prints the error, so a refusal
// that names the reader's field and no move sends the operator to the wrong
// file.
func TestMigrateReportsTheBitbucketRepublish(t *testing.T) {
	raw := migrateFixture(t, "policy-v3-bitbucket-profile.json")
	migrated, changes, err := Migrate(raw)
	if err == nil {
		t.Fatal("Migrate returned a node for a policy whose profile mounts bitbucket-token: the block is derived at publish and cannot come from the node")
	}
	var me *MalformedError
	if !errors.As(err, &me) {
		t.Fatalf("Migrate = %v, want a *MalformedError", err)
	}
	if !bytes.Equal(migrated, raw) {
		t.Errorf("Migrate returned %s, want the input untouched", migrated)
	}
	if len(changes) != Version-3 {
		t.Fatalf("changes = %+v, want the steps that were read, the first with the move that finishes it", changes)
	}
	if changes[0].From != 3 || changes[0].To != 4 {
		t.Fatalf("changes[0] = %+v, want the step from version 3 to version 4, which is the one that reports the move", changes[0])
	}
	if !strings.Contains(changes[0].Note, "republish to derive the Bitbucket block") {
		t.Errorf("changes[0].Note = %q, want it to say republish to derive the Bitbucket block", changes[0].Note)
	}
	if !strings.Contains(changes[0].Note, config.BitbucketSecret) {
		t.Errorf("changes[0].Note = %q, want it to name %s", changes[0].Note, config.BitbucketSecret)
	}
	if !strings.Contains(err.Error(), "bitbucket_token") {
		t.Errorf("%v does not name the block the node is missing", err)
	}
	if !strings.Contains(err.Error(), changes[0].Note) {
		t.Errorf("%v does not carry the move that finishes the job, so a caller printing only the error tells the operator what the reader refused and not what to do about it", err)
	}
}

// TestEveryOlderVersionHasAMigratorOrARefusal is the version-bump guard beside
// TestVersionSinceCoversEveryVersion: every policy version below Version is
// either moved forward by a step or named as too old, and the tables and the
// behaviour agree. A bump of Version that adds neither fails here, because the
// alternative is a release that ships an installation nothing can move.
//
// The too-old refusal is a *VersionError like any other skew, and the remedy
// the operator reads comes from SkewSentence and not from a sentence of this
// package's own: version.go words a version skew in one place so the two sides
// cannot drift, and a second wording here would be the drift.
func TestEveryOlderVersionHasAMigratorOrARefusal(t *testing.T) {
	for v := 1; v < Version; v++ {
		_, migrates := migrators[v]
		refused := tooOld[v]
		if migrates == refused {
			t.Errorf("policy version %d: a migrator is %v and a too-old refusal is %v — every version below %d needs exactly one of the two", v, migrates, refused, Version)
		}
		// Migratable is the same answer as the tables, in the question form the
		// remedies outside this package turn on: `fugaro doctor` names `fugaro
		// update` for a node the chain carries and the publisher's
		// delete-then-publish for one below its first step, and a predicate that
		// disagreed with the walk would send an operator to a command that stops.
		if got := Migratable(v); got != migrates {
			t.Errorf("Migratable(%d) = %v, and migrators[%d] exists = %v: the walk and the answer doctor branches on must be one answer", v, got, v, migrates)
		}
	}
	if !Migratable(Version) {
		t.Errorf("Migratable(%d) = false: a node already at this version is one nothing has to be migrated from", Version)
	}
	if Migratable(Version + 1) {
		t.Errorf("Migratable(%d) = true: a node ahead of this Fugaro is refused before the walk, and its move is a newer binary", Version+1)
	}
	for v := 1; v < Version; v++ {
		raw := []byte(`{"version":` + itoa(v) + `,"budget":{"mode":"enforce"},"allow_repos":["github/owner/repo-a"]}`)
		migrated, _, err := Migrate(raw)
		if err == nil {
			p, found, derr := Decode(migrated)
			if derr != nil || !found || p.Version != Version {
				t.Errorf("policy version %d: Migrate returned a node the reader does not read as version %d: (%v, %v)", v, Version, found, derr)
			}
			continue
		}
		var ve *VersionError
		if !errors.As(err, &ve) {
			t.Errorf("policy version %d: Migrate = %v, want a migrator or a *VersionError", v, err)
			continue
		}
		if ve.Found != v || ve.Understood != Version {
			t.Errorf("policy version %d: the refusal carries (%d, %d), want (%d, %d)", v, ve.Found, ve.Understood, v, Version)
		}
		if !bytes.Equal(migrated, raw) {
			t.Errorf("policy version %d: a refused node must come back untouched", v)
		}
		if msg := SkewSentence(ve, ReaderCLI); !strings.Contains(msg, "--delete") {
			t.Errorf("policy version %d: SkewSentence = %q, want the older-node remedy the operator is told to run", v, msg)
		}
	}
}

// TestShapeFreeFloorIsTheVersionAfterTheRepublishBump: ShapeFreeFloor is how
// far back a reader may carry a node when it serves that node and cannot write
// it, and it is deliberately one version above the chain's first step. The step
// from the floor down is shape-only, and the node it produces is one a version-4
// reader enforces the Bitbucket token block by — a block only a republish can
// put there, which TestMigrateReportsTheBitbucketRepublish says in the step's
// own words. Carrying it in memory would hand a service a policy the operator
// never published, so the floor is the version that block arrived in
// (VersionSince[4]), and a change that makes a step need something the node does
// not hold moves the floor with it.
func TestShapeFreeFloorIsTheVersionAfterTheRepublishBump(t *testing.T) {
	if ShapeFreeFloor != 4 {
		t.Fatalf("ShapeFreeFloor = %d: version 4 is the version the Bitbucket token block arrived in (VersionSince[4]), and the step below it is the one whose node needs a republish", ShapeFreeFloor)
	}
	if !Migratable(ShapeFreeFloor - 1) {
		t.Errorf("Migratable(%d) = false: the chain does carry the version below the floor — a floor that coincided with the chain's first step would be that step's rule and not this one, which is the service's", ShapeFreeFloor-1)
	}
	// The floor is the reader's rule and not the chain's, and the two readers on
	// either side of it are the two functions: the CLI's carries the version
	// below the floor forward because its own next stage rewrites the node, and
	// the service's refuses it because nothing it can do puts the block there.
	below := []byte(`{"version":3,"budget":{"mode":"enforce"},"allow_repos":["github/owner/repo-a"]}`)
	if carried, found, err := DecodeCarriedForward(below); err != nil || !found || carried.Version != Version {
		t.Errorf("DecodeCarriedForward(a version-%d node) = (%v, %v, %v), want it carried to version %d: the writer's own reader answers a question about a node its next stage migrates", ShapeFreeFloor-1, carried, found, err, Version)
	}
	var ve *VersionError
	if p, found, err := DecodeCarriedForwardShapeFree(below); !errors.As(err, &ve) || !found {
		t.Errorf("DecodeCarriedForwardShapeFree(a version-%d node) = (%v, %v, %v), want the refusal Decode gives for a node that is there", ShapeFreeFloor-1, p, found, err)
	}
	// The version at the floor is carried like any other, so the refusal is the
	// floor and not the service refusing everything it did not publish itself.
	at := []byte(`{"version":4,"budget":{"mode":"enforce"},"allow_repos":["github/owner/repo-a"]}`)
	if p, found, err := DecodeCarriedForwardShapeFree(at); err != nil || !found || p.Version != Version {
		t.Errorf("DecodeCarriedForwardShapeFree(a version-%d node) = (%v, %v, %v), want it carried to version %d", ShapeFreeFloor, p, found, err, Version)
	}
}

// TestMigratedPoolStaysLegacyOpen: the pools a version-4 node carries are the
// pools the installation serves from, and D1942 keeps them serving exactly whom
// they served before the upgrade. A pool that names no owner is one written
// before owners existed, so the step marks it legacy-open rather than inventing
// an owner it cannot know — and the marker is a NAME in the node and not the
// absence of an owner (F37): a pool written now must name one where it is
// published, so "no owner" and "open to everyone" must not be the same piece of
// text. The pool that does name an owner keeps it, and its roster, untouched.
func TestMigratedPoolStaysLegacyOpen(t *testing.T) {
	raw := migrateFixture(t, "policy-v4-craft-pools.json")
	migrated, _, err := Migrate(raw)
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	p, found, err := Decode(migrated)
	if err != nil || !found || p == nil {
		t.Fatalf("the migrated node does not decode: (%v, %v, %v)", p, found, err)
	}
	pools := map[string]config.Pool{}
	for _, pool := range p.CraftPools {
		pools[pool.Name] = pool
	}
	open, ok := pools["claude"]
	if !ok {
		t.Fatalf("the migrated node lost the claude pool: %+v", p.CraftPools)
	}
	if !open.LegacyOpen {
		t.Errorf("pools.claude = %+v, want the legacy-open marker the migration writes", open)
	}
	if open.Owner != "" {
		t.Errorf("pools.claude.owner = %q, want none: no step may invent an owner from a node that names none", open.Owner)
	}
	if !open.Serves("stranger@example.com") {
		t.Error("the migrated legacy-open pool serves nobody, so an installation's shared token stopped being served by the upgrade")
	}
	// Publishable as it stands: the marker is what the published node's rule
	// reads, so `fugaro update`'s republish of the migrated node goes through
	// with the pool still in it.
	if ps := config.ValidatePublishedPools(p.CraftPools, p.CraftCredentials); len(ps) != 0 {
		t.Errorf("the migrated node's pools are refused where they are published: %+v", ps)
	}

	owned, ok := pools["bmb"]
	if !ok {
		t.Fatalf("the migrated node lost the bmb pool: %+v", p.CraftPools)
	}
	if owned.LegacyOpen || owned.Owner != "owner@example.com" || !slices.Equal(owned.AllowedUsers, []string{"teammate@example.com"}) {
		t.Errorf("pools.bmb = %+v, want the owner and the roster the node wrote, and no marker", owned)
	}
	if !owned.Serves("teammate@example.com") || owned.Serves("stranger@example.com") {
		t.Errorf("pools.bmb serves the wrong people: %+v", owned)
	}

	// The marker is written, not inferred: a reader of the node — the console,
	// a hand edit, the next migration — has to be able to tell a pool that
	// predates owners from one whose owner the operator has yet to name.
	if !bytes.Contains(migrated, []byte(`"legacy_open": true`)) {
		t.Errorf("the migrated node does not spell the marker:\n%s", migrated)
	}
}

// TestMigrationFromPreviousVersionConverges: an installation at the version
// before this one converges to this one under `fugaro update` and nothing else
// (the HARD RULE, G7). Its published node is a version-4 node — that release's
// document with the older number in it — and the chain carries it forward to a
// node the current reader reads, keeps the pools it serves and is a fixed point
// afterwards, so a second update changes nothing.
func TestMigrationFromPreviousVersionConverges(t *testing.T) {
	raw := migrateFixture(t, "policy-v4-craft-pools.json")

	// The premise, asserted here so the fixture cannot drift into a node this
	// test is not about: the previous version's node is one this reader refuses
	// as a skew.
	if _, _, err := Decode(raw); err == nil {
		t.Fatal("the version-4 fixture decodes with the current reader, so it is not a node this migration is for")
	} else {
		var ve *VersionError
		if !errors.As(err, &ve) || ve.Found != 4 {
			t.Fatalf("Decode(the version-4 fixture) = %v, want a version-4 skew refusal", err)
		}
	}

	migrated, changes, err := Migrate(raw)
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	p, found, err := Decode(migrated)
	if err != nil || !found || p == nil {
		t.Fatalf("the migrated node does not decode: (%v, %v, %v)", p, found, err)
	}
	if p.Version != Version {
		t.Errorf("the migrated node is version %d, want %d", p.Version, Version)
	}
	if len(p.CraftPools) != 2 || len(p.CraftCredentials) != 1 || p.Crafts == nil || p.Crafts.Coder.Access != "claude" {
		t.Errorf("the migrated node's craft blocks = %+v / %+v / %+v, want the pools, the credentials and the access the node carried", p.CraftPools, p.CraftCredentials, p.Crafts)
	}
	if !slices.Equal(p.AllowRepos, []string{"github/owner/repo-a"}) || p.Budget.PerRunUSD != 5 {
		t.Errorf("the migrated node's policy = %+v, want the policy the node carried", p)
	}
	// One step per version in between, each naming what it did: this is the
	// diff `fugaro update` prints.
	if len(changes) != Version-4 || changes[0].From != 4 || changes[0].To != 5 {
		t.Fatalf("changes = %+v, want the steps from version 4 to %d", changes, Version)
	}

	// A fixed point: the next update has nothing to do to this node, which is
	// what "an installation at the previous version converges" has to mean.
	again, nochanges, err := Migrate(migrated)
	if err != nil {
		t.Fatalf("Migrate(the migrated node) = %v", err)
	}
	if len(nochanges) != 0 || !bytes.Equal(again, migrated) {
		t.Errorf("the second migration reported %+v over %d bytes, want the node back byte for byte", nochanges, len(migrated))
	}
}

// TestMigrateWritesTheMarkerOnlyWhereItIsMissing: the step's edit is confined
// to the pools that need it. An entry that names an owner is the operator's own
// answer and is left as it was written, and one that already carries the marker
// — a node a person edited by hand, since no version-4 release writes the
// member — is not written a second time: the step reports the pools it marked,
// and a node whose every pool is one of those two comes back with its version
// field alone rewritten and craft_pools reported as kept.
func TestMigrateWritesTheMarkerOnlyWhereItIsMissing(t *testing.T) {
	raw := []byte(`{"version":4,"craft_pools":[` +
		`{"name":"owned","members":["vertex"],"owner":"owner@example.com"},` +
		`{"name":"marked","members":["vertex"],"legacy_open":true}]}`)
	migrated, changes, err := Migrate(raw)
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	if len(changes) != 3 || changes[0].Changed != "the version field: 4 to 5" || changes[1].Changed != "the version field: 5 to 6" || changes[2].Changed != "the version field: 6 to 7" {
		t.Fatalf("changes = %+v, want the step to report the version field alone: no pool of this node needed a marker", changes)
	}
	if !slices.Contains(changes[0].Kept, "craft_pools") {
		t.Errorf("changes[0].Kept = %v, want craft_pools: the step wrote nothing inside it", changes[0].Kept)
	}
	p, found, err := Decode(migrated)
	if err != nil || !found {
		t.Fatalf("the migrated node does not decode: (%v, %v)", found, err)
	}
	for _, pool := range p.CraftPools {
		switch pool.Name {
		case "owned":
			if pool.LegacyOpen {
				t.Errorf("pools.owned = %+v, want the owner the node named and no marker", pool)
			}
		case "marked":
			if !pool.LegacyOpen {
				t.Errorf("pools.marked = %+v, want the marker the node already carried", pool)
			}
		default:
			t.Errorf("the migrated node gained a pool: %+v", p.CraftPools)
		}
	}
}

// TestShapeFreeStepsOnlyMoveTheNumber is what lets Decode admit a node one
// version behind (ReadsVersion): the step is called shape-free because it
// splices the version member and nothing else, and a step that also rewrote,
// added or dropped a member would make every node of its version a node the
// reader above reads differently from the one its own release wrote. The node
// below carries a member of every kind the walk has to leave alone — a block
// with a list, a block with an object, an entry the owner edited by hand — and
// the step's whole output must be that node with the next number in it.
//
// A step whose node needs a move cannot be shape-free either: its Change
// carries a Note, which is what the operator is told to finish the job, and a
// tolerance that admitted such a version would read the node without ever
// printing it (version 3's Bitbucket republish is that case).
func TestShapeFreeStepsOnlyMoveTheNumber(t *testing.T) {
	for from := range shapeFreeSteps {
		step, ok := migrators[from]
		if !ok {
			t.Fatalf("shapeFreeSteps names version %d and the chain has no step for it", from)
		}
		node := fmt.Sprintf(`{"version": %d, "allow_repos": ["github/acme/app"], "budget": {"mode": "enforce"},
			"profiles": {"standard": {"secrets": ["bitbucket-token"], "allow_repos": ["github/acme/app"]}},
			"routing": {"claude": {"via": "direct"}}, "clone_urls": {"app": "https://example.invalid/app.git"}}`, from)
		out, ch, err := step(json.RawMessage(node))
		if err != nil {
			t.Fatalf("the step from %d: %v", from, err)
		}
		want := strings.Replace(node, fmt.Sprintf(`"version": %d`, from), fmt.Sprintf(`"version": %d`, from+1), 1)
		if string(out) != want {
			t.Errorf("the step from %d rewrote more than the version member:\n got %s\nwant %s", from, out, want)
		}
		if ch.Note != "" {
			t.Errorf("the step from %d carries the note %q: a node whose migration needs a move after it is not one a reader can read without printing it", from, ch.Note)
		}
	}
}
