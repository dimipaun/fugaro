package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// update_bitbucket_tokens_test.go is T12 of docs/bitbucket-token-types/
// (implementation-plan.md, R5): `fugaro update` is what publishes the node's
// bitbucket_tokens key, and it is the whole of what an installation holding
// typed Bitbucket token records has to run. The key is new with a release that
// does NOT move the node's version, so an installation whose node is already at
// this version has no migration step that would carry it: the drift comparison
// between the local config and the node (policyConfigDrift) is what makes the
// publish happen, and a run whose records are already what the node carries
// publishes nothing.
//
// The identities the entries carry are the half the local config does not state
// (R12): they live in the creds state store, under each token's own document
// key, and the doors read them there. A project that names no typed token
// derives no key and reads no store at all, which is R3's half of the rule —
// the installation holding the legacy `bitbucket-token` alone converges exactly
// as it did before this key existed.

// typedTokenRecords is one typed Bitbucket token record in the local config
// (T2): the operator's own statement of what a token reaches, in the config's
// two-segment spelling.
const typedTokenRecords = "bitbucket_tokens:\n" +
	"  - {name: ci, type: repo, coverage: [workspace-a/repo-a]}\n"

// typedTokenKey is the document key the record is spoken of by, everywhere the
// node, the bundle and the audit lines name it (R5): the name the local config
// writes, spelled into the family's key.
var typedTokenKey = config.BitbucketTokenKey("ci")

// seedTokenIdentity plants the status `fugaro creds set`'s add-time check
// writes for a typed token (T6): the account UUID Bitbucket answered for it,
// under the token's own document key.
func seedTokenIdentity(t *testing.T, f *projectFixture, key, identity string) {
	t.Helper()
	f.db.Set(creds.PathTokenStatus(key), map[string]any{"identity": identity})
}

// nodeBytes is the published node exactly as the database holds it: the
// comparison an idempotence test needs, since two publishes of one document
// that differ in nothing must leave the same bytes.
func nodeBytes(t *testing.T, f *projectFixture) []byte {
	t.Helper()
	v := f.db.Value(projectpolicy.PathShared())
	if v == nil {
		t.Fatalf("no policy node at %s", projectpolicy.PathShared())
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestUpdatePublishesTheTokensKey is this task's red step: an installation
// whose node is already at this version and whose local config names a typed
// Bitbucket token publishes a node carrying bitbucket_tokens, with the coverage
// the config states and the identity the state store holds. Before this task
// the same run published the node and left the key out, so the operator's own
// records reached no run — and the key cannot arrive by migration, because this
// release moves no version.
func TestUpdatePublishesTheTokensKey(t *testing.T) {
	f := newProjectFixture(t, typedTokenRecords)
	f.db.Set(projectpolicy.PathShared(), currentPolicyNode())
	seedTokenIdentity(t, f, typedTokenKey, "uuid-ci")
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the policy stage refused a config naming a typed Bitbucket token: %v\n%s", err, out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Errorf("the node is version %d, want %d: this fixture's node is already at this version, so the key has to arrive by the publish and not by a migration", got, projectpolicy.Version)
	}
	bt := publishedPolicy(t, f).BitbucketTokens
	if bt == nil || len(bt.Entries) != 1 {
		t.Fatalf("the published node carries %+v, want the one record the local config names:\n%s", bt, out)
	}
	e := bt.Entries[0]
	if e.Name != typedTokenKey {
		t.Errorf("the entry's name is %q, want %q: a token is named by its document key and never by the bare suffix (R5)", e.Name, typedTokenKey)
	}
	if e.Type != config.BitbucketTokenTypeRepo {
		t.Errorf("the entry's type is %q, want %q", e.Type, config.BitbucketTokenTypeRepo)
	}
	if want := []string{"bitbucket/workspace-a/repo-a"}; !slices.Equal(e.Coverage, want) {
		t.Errorf("the entry's coverage is %v, want %v: the node spells a coverage entry provider-qualified, and the credentials service matches the node's spelling", e.Coverage, want)
	}
	if e.Identity != "uuid-ci" {
		t.Errorf("the entry's identity is %q, want the UUID the state store holds: the node is where the service learns whose token it is serving (R12)", e.Identity)
	}
}

// TestUpdateWithOnlyTheLegacyTokenPublishesNoKey: an installation holding the
// legacy `bitbucket-token` alone — every installation that never stored a typed
// token — converges with the node it would have had before this key existed
// (R3): no bitbucket_tokens member, and no record written anywhere.
//
// The state store is made unreadable on purpose, and that is the assertion
// rather than a detail of the fixture: a status node this release cannot decode
// (an unknown member, which a newer release's shape would leave) fails the read
// whole. This run converges all the same, which is what says the store was
// never read — the read is what R3's half of the rule forbids for a project
// that names no typed token, and a stage that made it would stop the update of
// every such installation over a subtree it has no record in.
func TestUpdateWithOnlyTheLegacyTokenPublishesNoKey(t *testing.T) {
	f := newProjectFixture(t, bitbucketTokenConfig)
	f.db.Set(projectpolicy.PathShared(), currentPolicyNode())
	f.db.Set(creds.PathTokenStatus(config.BitbucketSecret), map[string]any{"unmodelled": true})
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("an installation whose local config names only the legacy %s did not converge: %v\n%s", config.BitbucketSecret, err, out)
	}
	if bt := publishedPolicy(t, f).BitbucketTokens; bt != nil {
		t.Errorf("the node carries bitbucket_tokens = %+v, want no such member: a project that names no typed token derives no key at all (R3), and the node of every installation that never stored one stays what it was", bt)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Errorf("the node is version %d, want %d", got, projectpolicy.Version)
	}
}

// TestInstallationAtPreviousVersionConvergesBitbucketTokens: the installation
// the release behind left — a node this binary's reader refuses and the chain
// carries forward, and a local config that names a typed token — reaches both
// the new version and the new key with `fugaro update` alone. No step and no
// prompt: the migration is what the update always was, and the key rides in the
// node it writes.
func TestInstallationAtPreviousVersionConvergesBitbucketTokens(t *testing.T) {
	f := newProjectFixture(t, typedTokenRecords)
	f.db.Set(projectpolicy.PathShared(), staleNode(t))
	seedTokenIdentity(t, f, typedTokenKey, "uuid-ci")
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the installation one release behind did not converge: %v\n%s", err, out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Errorf("the node is version %d, want %d", got, projectpolicy.Version)
	}
	bt := publishedPolicy(t, f).BitbucketTokens
	if bt == nil || len(bt.Entries) != 1 {
		t.Fatalf("the converged node carries %+v, want the record the local config names:\n%s", bt, out)
	}
	if got := bt.Entries[0].Identity; got != "uuid-ci" {
		t.Errorf("the entry's identity is %q, want the UUID the state store holds: the migration and the publish read the same store (R12)", got)
	}
	if !strings.Contains(out, projectpolicy.BitbucketTokensKey) {
		t.Errorf("the diff does not name %s, the member this run added:\n%s", projectpolicy.BitbucketTokensKey, out)
	}
}

// TestUpdateTokensStepIsIdempotent: a second `fugaro update` over the
// installation the first one converged changes none of the node's bytes and
// publishes nothing. The comparison is the drift row's own — the node's entries
// against the derivation — so a row that compared the file's spelling of a
// coverage entry against the node's would publish on every update, forever,
// rewriting a document no run reads differently.
func TestUpdateTokensStepIsIdempotent(t *testing.T) {
	f := newProjectFixture(t, typedTokenRecords)
	f.db.Set(projectpolicy.PathShared(), currentPolicyNode())
	seedTokenIdentity(t, f, typedTokenKey, "uuid-ci")

	first := newMigrationRig(t, f, &initOptions{})
	if out, err := first.apply(t, true); err != nil {
		t.Fatalf("the first update did not converge: %v\n%s", err, out)
	}
	converged := nodeBytes(t, f)
	if publishedPolicy(t, f).BitbucketTokens == nil {
		t.Fatal("the first update published no bitbucket_tokens key, so this test would pin nothing")
	}

	second := newMigrationRig(t, f, &initOptions{})
	out, err := second.apply(t, true)
	if err != nil {
		t.Fatalf("the second update refused the installation the first one converged: %v\n%s", err, out)
	}
	if got := nodeBytes(t, f); string(got) != string(converged) {
		t.Errorf("the second update rewrote the node:\n got %s\nwant %s", got, converged)
	}
	// A node that already carries what the local config states leaves the stage
	// with nothing to write and nothing to show: the second run's transcript is
	// empty where the first one printed the publish it made.
	if strings.TrimSpace(out) != "" {
		t.Errorf("the second update printed a diff over a node that already carries what the local config states:\n%s", out)
	}
}

// TestUpdateInFlightLineNamesTheBitbucketTokensPublish: the step this task adds
// to the update's one line about the runs in flight. The policy stage writes
// the node's bitbucket_tokens key, and a PROXIED call re-reads that node on
// every request it serves (internal/credsvc/bitbucketproxy.go), where the
// runner's own read is memoized at bootstrap — so the first call after the
// publish may be served by a different token than the call before it, and the
// line has to say so. Nothing here asks a run to finish first: the clause names
// the step, and the update writes it while the run goes on.
//
// The two negative cases are the halves of the clause's gate: a project whose
// config names no typed token publishes no such key, and a project whose node
// does not turn the credentials service's Bitbucket route on has no per-call
// reader of it.
func TestUpdateInFlightLineNamesTheBitbucketTokensPublish(t *testing.T) {
	proxiedNode := func() map[string]any {
		n := currentPolicyNode()
		n["bitbucket_token"] = map[string]any{
			"allow_repos": []string{"bitbucket/workspace-a/repo-a"},
			"proxy":       true,
		}
		return n
	}
	line := func(t *testing.T, node map[string]any, records bool) string {
		t.Helper()
		extra := ""
		if records {
			extra = typedTokenRecords
		}
		f := newProjectFixture(t, extra)
		f.db.Set(projectpolicy.PathShared(), node)
		h := newMigrationRig(t, f, &initOptions{}).inFlight()
		h.s.e.announceRunsInFlight(context.Background())
		return strings.TrimSpace(h.out.String())
	}
	t.Run("a proxied call re-reads the node the key was just published in", func(t *testing.T) {
		got := line(t, proxiedNode(), true)
		for _, want := range []string{"1 run in flight", "the Bitbucket tokens this update publishes", "re-reads on every request"} {
			if !strings.Contains(got, want) {
				t.Errorf("the line does not say %q over a run whose proxied calls re-read the node this update publishes:\n%s", want, got)
			}
		}
		if strings.Contains(got, "no step reaches them") {
			t.Errorf("the line claims no step can reach a run in flight, over the node this update rewrites:\n%s", got)
		}
	})
	t.Run("no record, no key", func(t *testing.T) {
		got := line(t, proxiedNode(), false)
		if strings.Contains(got, "Bitbucket tokens") {
			t.Errorf("the line names the Bitbucket tokens over a project whose config names none, so this update publishes no such key:\n%s", got)
		}
	})
	t.Run("a node that does not proxy", func(t *testing.T) {
		node := currentPolicyNode()
		node["bitbucket_token"] = map[string]any{"allow_repos": []string{"bitbucket/workspace-a/repo-a"}}
		got := line(t, node, true)
		if strings.Contains(got, "Bitbucket tokens") {
			t.Errorf("the line names the Bitbucket tokens over a project whose Bitbucket calls never reach the credentials service, so no call re-reads the node:\n%s", got)
		}
	})
}
