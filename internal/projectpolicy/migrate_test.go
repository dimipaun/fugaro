package projectpolicy

import (
	"bytes"
	"errors"
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

// TestMigrateV3ToV4PreservesHandEditedAllowList: the state this migration
// exists for. A version-3 node is one today's reader refuses, so an
// installation that published under 0.13.x can only reach 0.14.0's policy by
// deleting the node and publishing again — which loses every entry the owner
// edited by hand. The migrator moves the node forward instead, and everything
// the node carries survives: the hand-edited allow_repos entry, clone_urls,
// routing, the profiles and the accounts block.
func TestMigrateV3ToV4PreservesHandEditedAllowList(t *testing.T) {
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

	// The report a caller renders as the diff: one step, 3 to 4, naming what it
	// kept. Nothing here asks the operator for anything more.
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want the one step this node needed", changes)
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

// TestMigrateV3ToV4EditsOnlyTheVersionBytes: the step edits the node in place,
// so the migration is the owner's document with one field written and not a
// re-encoding of it. Decoding into a map and marshalling it again would sort
// the keys, compact the whitespace, escape <, > and &, and fold a repeated key
// onto the last one — semantics survive that, but a caller that diffs the two
// nodes, or skips a write it believes changed nothing, is reading a document
// that is not the one it holds. The fixture carries a block this Fugaro does
// not model, with a value encoding/json would escape, so the property is
// pinned rather than argued.
func TestMigrateV3ToV4EditsOnlyTheVersionBytes(t *testing.T) {
	raw := migrateFixture(t, "policy-v3-hand-edited.json")
	migrated, _, err := Migrate(raw)
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	want := bytes.Replace(raw, []byte(`"version": 3`), []byte(`"version": 4`), 1)
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

// TestMigrateV3ToV4RoundTripsThroughTheStrictReader: what the migrator writes
// is a node the current reader reads, and it is the same policy the node was —
// every field but the version is preserved in meaning, which is what comparing
// the two decoded documents says. The migrated node is also a fixed point:
// migrating it again is the no-op.
func TestMigrateV3ToV4RoundTripsThroughTheStrictReader(t *testing.T) {
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
	hand := bytes.Replace(raw, []byte(`"version": 3`), []byte(`"version": 4`), 1)
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
			if msg := SkewSentence(ve, reader); !strings.Contains(msg, "fugaro upgrade") {
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
		{"a migrated node the reader refuses", `{"version": 3, "allow_repos": ["nope"]}`, 1},
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
		[]byte(`{"version":4,"budget":{"mode":"enforce"},"allow_repos":["github/owner/repo-a"]}`),
		[]byte(`{"version":4,"allow_repos":["nope"]}`),
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
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want the step that was read, with the move that finishes it", changes)
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
