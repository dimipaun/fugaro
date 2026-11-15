package localcfg

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// bitbucketTokensFixture is a complete block: one record of each type, the
// three forms R1 pairs with them, so the tests below change one member at a
// time against a document that otherwise loads.
const bitbucketTokensFixture = `bitbucket_tokens:
  - name: ci
    type: repo
    coverage: [acme/api]
    expires: 2027-01-15
  - name: premium
    type: project
    coverage: [acme]
    project_uuid: "{b7f1c2d0}"
    project_key: API
  - name: wide
    type: workspace
    coverage: [acme/*]
`

// TestLocalConfigReadsBitbucketTokens: the block is the operator's own local
// config and the parse reads it whole — name, type, coverage, the project's
// UUID and key, and the expiry — which is what T3 derives the node's
// bitbucket_tokens key from and what T11 writes a record into.
func TestLocalConfigReadsBitbucketTokens(t *testing.T) {
	c, err := Parse([]byte(sample + bitbucketTokensFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.BitbucketTokens) != 3 {
		t.Fatalf("BitbucketTokens = %+v, want the three records", c.BitbucketTokens)
	}
	ci := c.BitbucketTokens[0]
	if ci.Name != "ci" || ci.Type != config.BitbucketTokenTypeRepo || len(ci.Coverage) != 1 || ci.Coverage[0] != "acme/api" || ci.Expires != "2027-01-15" {
		t.Errorf("the repository record read as %+v", ci)
	}
	premium := c.BitbucketTokens[1]
	if premium.ProjectUUID != "{b7f1c2d0}" || premium.ProjectKey != "API" {
		t.Errorf("the project record read as %+v, want its UUID and key", premium)
	}
	// A config with no block carries none: the key is absent, not empty, so a
	// node derived from it publishes no bitbucket_tokens key (T3).
	plain, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if plain.BitbucketTokens != nil {
		t.Errorf("a config with no bitbucket_tokens block carries %+v", plain.BitbucketTokens)
	}
}

// TestLocalConfigRejectsUnknownTokenMember: the block decodes strictly, like
// every other document this loader reads, and R12 is the reason it has to:
// identity and verified_at are written by `fugaro creds set` into the creds
// state store and are deliberately not members here, so a config that carries
// either is refused rather than read as a token whose identity no code would
// ever consult — the state a hand-edited config would otherwise leave.
func TestLocalConfigRejectsUnknownTokenMember(t *testing.T) {
	for _, member := range []string{"identity", "verified_at", "value", "secret_expires", "project"} {
		_, err := Parse([]byte(sample + "bitbucket_tokens:\n  - name: ci\n    type: repo\n    coverage: [acme/api]\n    " + member + ": something\n"))
		if err == nil {
			t.Fatalf("a bitbucket_tokens record carrying %s: was accepted", member)
		}
		if !strings.Contains(err.Error(), member) {
			t.Errorf("the refusal of %s: does not name the member: %v", member, err)
		}
	}
	// The control: the same record without the extra member loads, so the
	// refusals above are the member and not the fixture.
	if _, err := Parse([]byte(sample + "bitbucket_tokens:\n  - name: ci\n    type: repo\n    coverage: [acme/api]\n")); err != nil {
		t.Fatalf("a plain record was refused: %v", err)
	}
}

// TestLocalConfigRefusesABitbucketTokenTypeMismatch: R1's agreement is checked
// at load — a record typed `repo` whose coverage is `ws/*` is refused by the
// local config itself, before anything derives a node from it or writes a
// bundle entry for it — and the refusal names the record, so the operator
// knows which entry to edit.
func TestLocalConfigRefusesABitbucketTokenTypeMismatch(t *testing.T) {
	_, err := Parse([]byte(sample + "bitbucket_tokens:\n  - name: ci\n    type: repo\n    coverage: [myworkspace/*]\n"))
	if err == nil {
		t.Fatal("a record typed repo with workspace coverage was accepted")
	}
	if !strings.Contains(err.Error(), "bitbucket_tokens.ci") {
		t.Errorf("the refusal does not name the record: %v", err)
	}
}

// TestLocalConfigSharedDropsBitbucketTokens: the block is operator-only, so the
// converge never publishes it — the shared file is written into the runs
// bucket, which any launcher of the project can write, and a launcher who could
// put a record there would be stating which requests its own token carries (the
// coverage is what a request is matched against, R1, D2351). The drop is also
// what keeps an older CLI's strict ParseShared from meeting the key at all: a
// shared file carrying it is refused whole there, which would lock an
// un-upgraded teammate out of every run.
func TestLocalConfigSharedDropsBitbucketTokens(t *testing.T) {
	lc, err := Parse([]byte(sample + bitbucketTokensFixture))
	if err != nil {
		t.Fatal(err)
	}
	s := lc.Shared()
	if s.BitbucketTokens != nil {
		t.Errorf("Shared published %+v, want the block dropped", s.BitbucketTokens)
	}
	if len(lc.BitbucketTokens) != 3 {
		t.Errorf("Shared cleared the receiver's own records: %+v", lc.BitbucketTokens)
	}
	out, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "bitbucket_tokens") {
		t.Errorf("the published document carries the block:\n%s", out)
	}
}

// TestLocalConfigBitbucketTokenExpiryIsADate: `expires` is the one expiry
// source a typed token has (R6) — no secret_expires entry is read for it — so
// a date this file cannot use is refused here, where the operator wrote it,
// rather than becoming a doctor check that silently says nothing.
func TestLocalConfigBitbucketTokenExpiryIsADate(t *testing.T) {
	if _, err := Parse([]byte(sample + "bitbucket_tokens:\n  - name: ci\n    type: repo\n    coverage: [acme/api]\n    expires: soon\n")); err == nil {
		t.Fatal("an expiry that is not a date was accepted")
	} else if !strings.Contains(err.Error(), "bitbucket_tokens.ci.expires") {
		t.Errorf("the refusal does not name the record's expiry: %v", err)
	}
	if _, err := Parse([]byte(sample + "bitbucket_tokens:\n  - name: ci\n    type: repo\n    coverage: [acme/api]\n    expires: 2027-01-15\n")); err != nil {
		t.Errorf("a date was refused: %v", err)
	}
}
