package projectpolicy

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// decodeBitbucketNode decodes a policy node built from the parts under test:
// the bitbucket_token block and the profiles block, on top of what Decode
// requires anyway (a version, a budget and the project's own allow_repos). It
// is decodeProfiles' twin for the tests that need the block beside the
// profiles, so a fixture writes only the members its rule is about.
func decodeBitbucketNode(t *testing.T, parts ...string) (*Policy, error) {
	t.Helper()
	raw := `{"version":` + itoa(Version) + `,"budget":{"mode":"enforce"},"allow_repos":["github/acme/app","bitbucket/workspace-a/repo-a"]`
	for _, part := range parts {
		raw += "," + part
	}
	raw += "}"
	p, found, err := Decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	if !found {
		t.Fatal("Decode found no policy")
	}
	return p, nil
}

// bitbucketProfile is a Bitbucket shared job: the shape a profile that mounts
// the token has to be, as a config carrier.
func bitbucketProfile() config.JobProfile {
	return config.JobProfile{
		Provider:     "bitbucket",
		CPU:          2,
		Memory:       "4Gi",
		TimeoutClass: "6h",
		Secrets:      []string{config.BitbucketSecret},
	}
}

// TestDeriveBitbucketTokenQualifiesTheSlugs: the block is derived from the
// operator's owner/name list, whose entries may carry dc73's segment wildcards
// (D464). An exact slug goes through Canonical, which is what makes the
// derived entry match the canonical repository the runner holds; a pattern
// cannot (task.CanonicalRepo refuses "*"), so it is qualified directly — the
// provider prefix, the lower-cased literal owner and the name segment as
// written. The mutation is a derivation that writes the raw slug, or one that
// passes a pattern through Canonical and so refuses the operator's own entry.
func TestDeriveBitbucketTokenQualifiesTheSlugs(t *testing.T) {
	profiles := map[string]Profile{DefaultProfileName: {bitbucketProfile()}}
	secrets := map[string]config.ProjectSecret{
		config.BitbucketSecret: {AllowDataTo: []string{"Workspace-A/Repo-A", "workspace-a/api-*", "Workspace-A/API-*", "Workspace-A/Repo-A"}},
	}
	bn, err := DeriveBitbucketToken(profiles, secrets)
	if err != nil {
		t.Fatalf("DeriveBitbucketToken = %v", err)
	}
	if bn == nil {
		t.Fatal("DeriveBitbucketToken returned no block for an entry that names repositories")
	}
	// A pattern's name segment is lower-cased the way its owner's is (the
	// matcher compares segment against segment case-sensitively), so the two
	// spellings of the wildcard entry above derive one entry, not two.
	want := []string{"bitbucket/workspace-a/api-*", "bitbucket/workspace-a/repo-a"}
	if !slices.Equal(bn.AllowRepos, want) {
		t.Errorf("allow_repos = %v, want %v", bn.AllowRepos, want)
	}
	// The derived entries are the ones MatchRepo matches a canonical
	// repository with: the exact entry reaches the repository, the pattern
	// reaches its family, and neither reaches another workspace.
	if !AdmitRepo(bn.AllowRepos, "bitbucket/workspace-a/repo-a") {
		t.Error("the derived block does not admit the repository its own entry names")
	}
	if !AdmitRepo(bn.AllowRepos, "bitbucket/workspace-a/api-v2") {
		t.Error("the derived block does not admit a repository its wildcard entry names")
	}
	if AdmitRepo(bn.AllowRepos, "bitbucket/workspace-b/repo-a") {
		t.Error("the derived block admits another workspace")
	}
	if AdmitRepo(bn.AllowRepos, "github/workspace-a/repo-a") {
		t.Error("the derived block admits another provider's repository")
	}
	// A list the CLI's own validation refuses is refused here too, rather than
	// published (a wildcard owner is every workspace).
	if _, err := DeriveBitbucketToken(profiles, map[string]config.ProjectSecret{
		config.BitbucketSecret: {AllowDataTo: []string{"*/repo-a"}},
	}); err == nil {
		t.Error("a wildcard owner in the operator's entry produced a block")
	}
	// An entry the local config accepts and the node's own reader would refuse
	// is refused here, so that publishing cannot write a policy this binary
	// cannot read back: a pattern ending in ".git" (a canonical repository
	// never does), and a segment longer than the node reads.
	for _, slug := range []string{
		"workspace-a/api-*.git",
		"workspace-a/" + strings.Repeat("x", MaxPatternLen) + "*",
	} {
		_, err := DeriveBitbucketToken(profiles, map[string]config.ProjectSecret{
			config.BitbucketSecret: {AllowDataTo: []string{slug}},
		})
		if err == nil {
			t.Errorf("allow_data_to %q produced a block although the node refuses the entry it derives", slug)
			continue
		}
		if !strings.Contains(err.Error(), "project_secrets.bitbucket-token.allow_data_to") {
			t.Errorf("allow_data_to %q: the refusal does not name the operator's entry: %v", slug, err)
		}
	}
}

// TestDeriveBitbucketTokenWithoutAProfile: T2.1 as amended (D493) — the block
// is derived from project_secrets.bitbucket-token.allow_data_to whether or not
// a profile mounts the token, because a proxied job does not hold the
// credential and this block is its admission. It is nil only when there is
// nothing to derive it from: no entry, or an empty list ("shared with
// nobody"). The mutation is the pre-amendment derivation, which returns nil
// unless some profile mounts the token.
func TestDeriveBitbucketTokenWithoutAProfile(t *testing.T) {
	secrets := map[string]config.ProjectSecret{
		config.BitbucketSecret: {AllowDataTo: []string{"workspace-a/repo-a"}, Proxy: true},
	}
	bn, err := DeriveBitbucketToken(map[string]Profile{}, secrets)
	if err != nil {
		t.Fatalf("DeriveBitbucketToken = %v", err)
	}
	if bn == nil {
		t.Fatal("DeriveBitbucketToken returned no block although project_secrets names a repository")
	}
	if !slices.Equal(bn.AllowRepos, []string{"bitbucket/workspace-a/repo-a"}) {
		t.Errorf("allow_repos = %v, want the qualified slug", bn.AllowRepos)
	}
	if !bn.Proxy {
		t.Error("the derived block does not carry the operator's proxy switch")
	}
	// No entry, and an entry with no repository: both are "this project's
	// token reaches nothing", and both publish no block.
	for _, tc := range []struct {
		name    string
		secrets map[string]config.ProjectSecret
	}{
		{"no entry", map[string]config.ProjectSecret{}},
		{"empty list", map[string]config.ProjectSecret{config.BitbucketSecret: {}}},
		{"another name", map[string]config.ProjectSecret{"anthropic-api-key": {AllowDataTo: []string{"workspace-a/repo-a"}}}},
	} {
		bn, err := DeriveBitbucketToken(map[string]Profile{}, tc.secrets)
		if err != nil {
			t.Errorf("%s: DeriveBitbucketToken = %v", tc.name, err)
		}
		if bn != nil {
			t.Errorf("%s: DeriveBitbucketToken = %+v, want none", tc.name, bn)
		}
	}
}

// TestBitbucketTokenBlockCarriesProxy: the block carries the operator's
// per-installation switch, and nothing else writes it: proxy lives on
// project_secrets.bitbucket-token (config.ProjectSecret), never on a profile,
// because it is one decision for the whole installation (G3, G9). The
// mutation is a derivation that drops the switch, which would leave the
// credentials service reading proxy false on a node the operator turned on.
func TestBitbucketTokenBlockCarriesProxy(t *testing.T) {
	profiles := map[string]Profile{DefaultProfileName: {bitbucketProfile()}}
	for _, proxy := range []bool{false, true} {
		bn, err := DeriveBitbucketToken(profiles, map[string]config.ProjectSecret{
			config.BitbucketSecret: {AllowDataTo: []string{"workspace-a/repo-a"}, Proxy: proxy},
		})
		if err != nil {
			t.Fatalf("proxy %v: DeriveBitbucketToken = %v", proxy, err)
		}
		if bn == nil {
			t.Fatalf("proxy %v: DeriveBitbucketToken returned no block", proxy)
		}
		if bn.Proxy != proxy {
			t.Errorf("proxy %v: the block carries %v", proxy, bn.Proxy)
		}
	}
	// The node reads the member back, and an absent one means off: an
	// installation that has not opted in is not proxied.
	p, err := decodeBitbucketNode(t, `"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/*"],"proxy":true}`)
	if err != nil {
		t.Fatalf("a block carrying proxy was refused: %v", err)
	}
	if p.BitbucketToken == nil || !p.BitbucketToken.Proxy {
		t.Fatalf("the decoded block = %+v, want proxy true", p.BitbucketToken)
	}
	p, err = decodeBitbucketNode(t, `"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/*"]}`)
	if err != nil {
		t.Fatalf("a block without proxy was refused: %v", err)
	}
	if p.BitbucketToken == nil || p.BitbucketToken.Proxy {
		t.Fatalf("the decoded block = %+v, want proxy false", p.BitbucketToken)
	}
	// A member this Fugaro does not know is refused, like every other block's.
	if _, err := decodeBitbucketNode(t, `"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/*"],"allow_repo":[]}`); err == nil {
		t.Error("a misspelt member inside the block was accepted")
	}
}

// TestSharedBitbucketProfileNeedsTheTokenMount: D473 — a profile is a
// Bitbucket shared job when it serves bitbucket AND mounts the token. Both
// halves are load-bearing: the provider alone is any Bitbucket job, and the
// mount alone is the shape validProfiles refuses on a github profile.
func TestSharedBitbucketProfileNeedsTheTokenMount(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    config.JobProfile
		want bool
	}{
		{"bitbucket with the token", bitbucketProfile(), true},
		{"bitbucket without it", config.JobProfile{Provider: "bitbucket"}, false},
		{"github with it", config.JobProfile{Provider: "github", Secrets: []string{config.BitbucketSecret}}, false},
		{"github without it", config.JobProfile{Provider: "github"}, false},
	} {
		if got := SharedBitbucketProfile(tc.p); got != tc.want {
			t.Errorf("%s: SharedBitbucketProfile = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestDecodeRefusesABroadBitbucketTokenPattern: the block is one credential's
// reach, so it keeps D464's narrower rules whoever writes the node: no bare
// "*", a literal owner segment, at most two wildcards. MatchRepo would accept
// "*" and "bitbucket/*/*" in an allow_repos list — a project's enrollment may
// be that wide — and this block must not, or a single stored token would be
// forwarded for every workspace. The mutation is a validation that holds the
// block to ValidPattern alone.
func TestDecodeRefusesABroadBitbucketTokenPattern(t *testing.T) {
	for _, pattern := range []string{
		"*",
		"bitbucket/*/*",
		"bitbucket/*/repo-a",
		"bitbucket/workspace-a/a*b*c*",
		// A first segment that is not the provider this block belongs to is
		// dead rather than fail-open (MatchRepo compares segment against
		// segment, and every entry it is matched against is a bitbucket
		// canonical), and a "*" there reads as "any provider's repositories":
		// both are refused where the block is judged.
		"*/workspace-a/*",
		"github/workspace-a/repo-a",
	} {
		doc := `"bitbucket_token":{"allow_repos":["` + pattern + `"]}`
		if _, err := decodeBitbucketNode(t, doc); err == nil {
			t.Errorf("bitbucket_token.allow_repos accepted %q", pattern)
		} else if !strings.Contains(err.Error(), "bitbucket_token") {
			t.Errorf("%q: the refusal does not name the block: %v", pattern, err)
		}
	}
	// A written block that names no repository is refused too: an empty list
	// reads as "the token reaches nothing", and the way to say that is to
	// remove the block.
	for _, doc := range []string{
		`"bitbucket_token":{"allow_repos":[]}`,
		`"bitbucket_token":{"allow_repos":null}`,
		`"bitbucket_token":{}`,
	} {
		if _, err := decodeBitbucketNode(t, doc); err == nil {
			t.Errorf("%s was accepted", doc)
		}
	}
	// The shapes the derivation writes are the ones the node accepts.
	for _, pattern := range []string{"bitbucket/workspace-a/repo-a", "bitbucket/workspace-a/*", "bitbucket/workspace-a/api-*"} {
		p, err := decodeBitbucketNode(t, `"bitbucket_token":{"allow_repos":["`+pattern+`"]}`)
		if err != nil {
			t.Errorf("bitbucket_token.allow_repos refused %q: %v", pattern, err)
			continue
		}
		if !slices.Equal(p.BitbucketToken.AllowRepos, []string{pattern}) {
			t.Errorf("allow_repos = %v, want %q", p.BitbucketToken.AllowRepos, pattern)
		}
	}
	// The refusal never echoes a value, because there is none in the block to
	// echo: it names the block and the entry's shape only.
	if _, err := decodeBitbucketNode(t, `"bitbucket_token":{"allow_repos":["bitbucket/*/*"]}`); err != nil {
		if strings.Contains(err.Error(), config.BitbucketSecret+"=") {
			t.Errorf("the refusal looks like it echoes a value: %v", err)
		}
	}
}

// TestPolicyCarriesBitbucketAPIRoots: D474 — the owner allow-lists the API
// roots T2.8 accepts, and each one is an https origin with no query, no
// fragment and no userinfo. The mutation is a check that only reads the
// scheme: an http root sends the token in clear, and userinfo is a credential
// written into a node every run token can read.
func TestPolicyCarriesBitbucketAPIRoots(t *testing.T) {
	for _, root := range []string{
		"http://api.bitbucket.org",
		"https://api.bitbucket.org/?a=b",
		"https://api.bitbucket.org/#frag",
		"https://person-1:not-a-real-value@api.bitbucket.org",
		"",
		"api.bitbucket.org",
	} {
		if _, err := decodeBitbucketNode(t, `"bitbucket_api_roots":["`+root+`"]`); err == nil {
			t.Errorf("bitbucket_api_roots accepted %q", root)
		} else if !strings.Contains(err.Error(), "bitbucket_api_roots") {
			t.Errorf("%q: the refusal does not name the block: %v", root, err)
		}
	}
	for _, root := range []string{
		"https://api.bitbucket.org",
		"https://api.bitbucket.org/",
		"https://bitbucket.example.com/api",
	} {
		p, err := decodeBitbucketNode(t, `"bitbucket_api_roots":["`+root+`"]`)
		if err != nil {
			t.Errorf("bitbucket_api_roots refused %q: %v", root, err)
			continue
		}
		if !slices.Equal(p.BitbucketAPIRoots, []string{root}) {
			t.Errorf("bitbucket_api_roots = %v, want %q", p.BitbucketAPIRoots, root)
		}
	}
	// The block is matched exactly after trimming one trailing "/", so the two
	// spellings of the same root are one entry a reader normalizes; nothing
	// else about the string is rewritten here.
	p, err := decodeBitbucketNode(t, `"bitbucket_api_roots":["https://api.bitbucket.org/"]`)
	if err != nil {
		t.Fatalf("a root with a trailing slash was refused: %v", err)
	}
	if got := strings.TrimSuffix(p.BitbucketAPIRoots[0], "/"); got != "https://api.bitbucket.org" {
		t.Errorf("trimmed root = %q", got)
	}
	// No block at all is an empty list, never an error.
	p, err = decodeBitbucketNode(t, `"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`)
	if err != nil {
		t.Fatalf("a node without bitbucket_api_roots was refused: %v", err)
	}
	if len(p.BitbucketAPIRoots) != 0 {
		t.Errorf("bitbucket_api_roots = %v, want none", p.BitbucketAPIRoots)
	}
}

// typedTokenRecords are the three records a project holding one token of each
// type states, in the local config's own spelling: a repository token whose
// repository is written in another case, a workspace token, and a project
// token whose coverage is its workspace slug alone beside the project's UUID.
func typedTokenRecords() []config.BitbucketToken {
	return []config.BitbucketToken{
		{Name: "repo-a", Type: config.BitbucketTokenTypeRepo, Coverage: []string{"Workspace-A/Repo-A"}},
		{Name: "ws-a", Type: config.BitbucketTokenTypeWorkspace, Coverage: []string{"Workspace-A/*"}},
		{Name: "proj-a", Type: config.BitbucketTokenTypeProject, Coverage: []string{"Workspace-A"}, ProjectUUID: "proj-uuid", ProjectKey: "PROJ"},
	}
}

// TestDeriveBitbucketTokensKey is R5's derivation: one entry per typed record,
// carrying the record's own type and its coverage in the node's
// provider-qualified spelling, plus the identity and the project UUID the node
// needs to serve it — and nothing a caller could take a token with.
//
// The entry is named by the record's DOCUMENT KEY (config.BitbucketTokenKey)
// and never by the bare suffix, so no name can collide with a model account's
// or with the legacy key. The coverage is translated and not copied, because
// the service reads only the node: the operator's two-segment slug and its
// workspace-only project spelling are this package's, and the node — the
// spelling MatchRepo compares — is the derivation's (R5). The identity is the
// state store's, keyed by the same document key.
//
// The mutation is a derivation that publishes the suffix as a name, that
// copies the local config's spelling into the node, or that drops the UUID a
// project token is matched by.
func TestDeriveBitbucketTokensKey(t *testing.T) {
	identities := map[string]string{config.BitbucketTokenKey("ws-a"): "uuid-ws"}
	key, err := DeriveBitbucketTokens(typedTokenRecords(), identities)
	if err != nil {
		t.Fatalf("DeriveBitbucketTokens = %v", err)
	}
	if key == nil {
		t.Fatal("a project naming three typed tokens published no key")
	}
	want := []BitbucketTokenNode{
		{Name: "bitbucket-token--repo-a", Type: "repo", Coverage: []string{"bitbucket/workspace-a/repo-a"}},
		{Name: "bitbucket-token--ws-a", Type: "workspace", Coverage: []string{"bitbucket/workspace-a/*"}, Identity: "uuid-ws"},
		{Name: "bitbucket-token--proj-a", Type: "project", Coverage: []string{"bitbucket/workspace-a/*"}, ProjectUUID: "proj-uuid"},
	}
	if len(key.Entries) != len(want) {
		t.Fatalf("the key holds %d entries, want %d: %+v", len(key.Entries), len(want), key.Entries)
	}
	for i, w := range want {
		if !reflect.DeepEqual(key.Entries[i], w) {
			t.Errorf("entries[%d] = %+v, want %+v", i, key.Entries[i], w)
		}
	}
	// The identity is the store's and only the store's: a token the state store
	// recorded nothing for carries none, which is a token whose account is
	// unknown rather than an account named by an empty string.
	if key.Entries[0].Identity != "" || key.Entries[2].Identity != "" {
		t.Errorf("identities = %q and %q, want none: only the token the store named has one", key.Entries[0].Identity, key.Entries[2].Identity)
	}
	// The coverage the node carries is the one a canonical repository is
	// matched against, so the record read off the node reaches exactly the
	// repository its operator named, and no other.
	if !AdmitRepo(key.Entries[0].Coverage, "bitbucket/workspace-a/repo-a") {
		t.Error("the derived entry does not admit the repository its own record names")
	}
	if AdmitRepo(key.Entries[0].Coverage, "bitbucket/workspace-a/repo-b") {
		t.Error("the derived entry admits a repository its record does not name")
	}

	// The key is the node's: a policy carrying it marshals the member, and this
	// reader reads it back whole — the round trip a publish takes, since the
	// node the publisher writes is the one the next run decodes.
	raw, err := json.Marshal(&Policy{Version: Version, Budget: Budget{Mode: "enforce"}, BitbucketTokens: key})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"`+BitbucketTokensKey+`"`) {
		t.Fatalf("a policy carrying the key marshals no member:\n%s", raw)
	}
	back, found, err := Decode(raw)
	if err != nil || !found {
		t.Fatalf("the node the derivation published does not read back: (%v, %v, %v)", back, found, err)
	}
	if len(back.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: the key is this Fugaro's own", back.UnknownKeys)
	}
	if back.BitbucketTokens == nil || !reflect.DeepEqual(back.BitbucketTokens.Entries, key.Entries) {
		t.Errorf("the key read back as %+v, want %+v", back.BitbucketTokens, key.Entries)
	}

	// A project that names no typed token publishes no key at all, so the node
	// of such a project — every installation that never stored one — is what it
	// was before this key existed.
	for _, records := range [][]config.BitbucketToken{nil, {}} {
		none, err := DeriveBitbucketTokens(records, nil)
		if err != nil {
			t.Fatalf("DeriveBitbucketTokens(%v) = %v", records, err)
		}
		if none != nil {
			t.Errorf("a project naming no typed token derived %+v, want no key", none)
		}
		raw, err := json.Marshal(&Policy{Version: Version, Budget: Budget{Mode: "enforce"}, BitbucketTokens: none})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), BitbucketTokensKey) {
			t.Errorf("a project naming no typed token publishes the member:\n%s", raw)
		}
	}

	// A record whose coverage the node cannot carry is refused here, where the
	// sentence names the operator's own entry, rather than published as a node
	// this binary — and every run of the project — then refuses: the config's
	// alphabet admits a ".git" suffix and a segment longer than the node reads,
	// and the node's rule does not.
	_, err = DeriveBitbucketTokens([]config.BitbucketToken{
		{Name: "repo-a", Type: config.BitbucketTokenTypeRepo, Coverage: []string{"Workspace-A/Repo-A.git"}},
	}, nil)
	if err == nil {
		t.Fatal("a coverage the node refuses derived a key")
	}
	if !strings.Contains(err.Error(), "bitbucket_tokens.repo-a") {
		t.Errorf("the refusal does not name the operator's record: %v", err)
	}
	// A record whose NAME the key family cannot spell is refused here too, for
	// the reason the coverage guard above exists: the rule the config judges a
	// name by is the account-name regex alone, whose repetitions bound
	// character pairs and so admit a name past the family's length bound, while
	// config.BitbucketTokenKey demands both — and answers "" for the name it
	// refuses. An entry named "" is one this package's own reader refuses, so
	// the node would be refused by every reader and every run of the project,
	// and the identity would be looked up under the empty key.
	long := strings.Repeat("a-", 20) + "b"
	if ps := config.ValidateBitbucketTokens([]config.BitbucketToken{
		{Name: long, Type: config.BitbucketTokenTypeRepo, Coverage: []string{"Workspace-A/Repo-A"}},
	}); len(ps) > 0 {
		t.Fatalf("the fixture name %q is refused by the config's own rule (%s), so this case would not reach the derivation's guard", long, ps[0].String())
	}
	if _, err := DeriveBitbucketTokens([]config.BitbucketToken{
		{Name: long, Type: config.BitbucketTokenTypeRepo, Coverage: []string{"Workspace-A/Repo-A"}},
	}, nil); err == nil {
		t.Errorf("a record named %q, which the key family cannot spell, derived a key", long)
	} else if !strings.Contains(err.Error(), "bitbucket_tokens.") || !strings.Contains(err.Error(), long) {
		t.Errorf("the refusal does not name the operator's record: %v", err)
	}
	// A record the config's own rules refuse is refused before anything is
	// derived from it: the halves of a record state one fact (R1), and a node
	// derived from halves that disagree would be a token served as the wrong
	// type.
	if _, err := DeriveBitbucketTokens([]config.BitbucketToken{
		{Name: "ws-a", Type: config.BitbucketTokenTypeRepo, Coverage: []string{"Workspace-A/*"}},
	}, nil); err == nil {
		t.Error("a record whose type and coverage disagree derived a key")
	}
}

// TestDecodeRefusesAMalformedTokensEntry is the reader's per-entry guard, one
// refusal at a time: validBitbucketTokenNode, which holds an entry to the
// node's own spelling of a token — the family's document key, one of the three
// types, a coverage that names something, and patterns the node's own rule
// admits. The mutation is that guard answering nil, which leaves the node
// accepting the legacy key as a typed token's name, a bare suffix, a type
// outside the three, an entry that reaches nothing, and every pattern
// ValidBitbucketTokenCoverage refuses — the hole C1 closed on the record side,
// reopened on the node's.
//
// Each refusal names the entry it is about — bitbucket_tokens.entries[i], and
// the index of the entry rather than the first one — because that is what an
// operator edits: an entry is derived from a record of their own file.
func TestDecodeRefusesAMalformedTokensEntry(t *testing.T) {
	good := `{"name":"bitbucket-token--repo-a","type":"repo","coverage":["bitbucket/workspace-a/repo-a"]}`
	// The shapes the derivation writes are the shapes the node accepts: the
	// guard refuses a broken entry, not the key.
	p, err := decodeBitbucketNode(t, `"`+BitbucketTokensKey+`":{"entries":[`+good+`]}`)
	if err != nil {
		t.Fatalf("a node carrying one derived entry was refused: %v", err)
	}
	if p.BitbucketTokens == nil || len(p.BitbucketTokens.Entries) != 1 {
		t.Fatalf("the key read back as %+v, want the one entry", p.BitbucketTokens)
	}
	for _, tc := range []struct {
		why   string
		entry string
	}{
		{"the legacy key as a name", `{"name":"bitbucket-token","type":"repo","coverage":["bitbucket/workspace-a/repo-a"]}`},
		{"the bare suffix as a name", `{"name":"repo-a","type":"repo","coverage":["bitbucket/workspace-a/repo-a"]}`},
		{"no name", `{"type":"repo","coverage":["bitbucket/workspace-a/repo-a"]}`},
		{"a name past the family's length", `{"name":"bitbucket-token--` + strings.Repeat("a-", 20) + `b","type":"repo","coverage":["bitbucket/workspace-a/repo-a"]}`},
		{"no type", `{"name":"bitbucket-token--repo-a","coverage":["bitbucket/workspace-a/repo-a"]}`},
		{"a type outside the three", `{"name":"bitbucket-token--repo-a","type":"owner","coverage":["bitbucket/workspace-a/repo-a"]}`},
		{"no coverage", `{"name":"bitbucket-token--repo-a","type":"repo","coverage":[]}`},
		{"every repository of every provider", `{"name":"bitbucket-token--ws-a","type":"workspace","coverage":["*"]}`},
		{"a wildcard owner", `{"name":"bitbucket-token--ws-a","type":"workspace","coverage":["bitbucket/*/*"]}`},
		{"another provider", `{"name":"bitbucket-token--repo-a","type":"repo","coverage":["github/workspace-a/repo-a"]}`},
		{"three stars", `{"name":"bitbucket-token--repo-a","type":"repo","coverage":["bitbucket/workspace-a/a*b*c*"]}`},
	} {
		// The broken entry is the second one, so a refusal that names the
		// first — or that names the key and no entry — fails here.
		doc := `"` + BitbucketTokensKey + `":{"entries":[` + good + `,` + tc.entry + `]}`
		_, err := decodeBitbucketNode(t, doc)
		if err == nil {
			t.Errorf("%s was accepted: %s", tc.why, tc.entry)
			continue
		}
		if want := BitbucketTokensKey + ".entries[1]"; !strings.Contains(err.Error(), want) {
			t.Errorf("%s: the refusal does not name %s: %v", tc.why, want, err)
		}
	}
}

// TestNodeWithoutTokensKeyReadsAsBefore: the key is additive, and a node that
// carries none reads exactly as it did. An absent member, a written null and a
// written empty block are one node — the publisher writes nothing for a project
// that names no typed token — and a policy carrying no key marshals none, which
// is what keeps such a project's bytes what they were (D1156's rule for the
// key beside this one). Version does not move (R5): the key is a top-level
// member, no value a reader enforces changes with it, and an entry is read by
// the service, which re-reads the node per request.
func TestNodeWithoutTokensKeyReadsAsBefore(t *testing.T) {
	for _, raw := range []string{
		`{"version": ` + itoa(Version) + `}`,
		`{"version": ` + itoa(Version) + `, "bitbucket_tokens": null}`,
		`{"version": ` + itoa(Version) + `, "bitbucket_tokens": {"entries": []}}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err != nil || !found {
			t.Fatalf("Decode(%s) = (%v, %v, %v), want the node read", raw, p, found, err)
		}
		if p.BitbucketTokens != nil {
			t.Errorf("Decode(%s).BitbucketTokens = %+v, want none: an absent, null and empty key are one node", raw, p.BitbucketTokens)
		}
		if len(p.UnknownKeys) != 0 {
			t.Errorf("Decode(%s).UnknownKeys = %v, want none", raw, p.UnknownKeys)
		}
		if p.Version != Version {
			t.Errorf("Decode(%s).Version = %d, want %d", raw, p.Version, Version)
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), BitbucketTokensKey) {
			t.Errorf("a policy carrying no key marshals one:\n%s", b)
		}
	}
	if Version != 7 {
		t.Errorf("Version = %d, want 7: this key moves no version (R5)", Version)
	}
	// The block beside the key is untouched by it: bitbucket_token reads the
	// same members, with the same meanings, in a node that carries the key and
	// in one that does not.
	p, err := decodeBitbucketNode(t, `"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/repo-a"],"proxy":true}`)
	if err != nil {
		t.Fatalf("a node without the key was refused: %v", err)
	}
	if !slices.Equal(p.BitbucketToken.AllowRepos, []string{"bitbucket/workspace-a/repo-a"}) || !p.BitbucketToken.Proxy {
		t.Errorf("bitbucket_token = %+v, want the block it was", p.BitbucketToken)
	}
	if p.BitbucketTokens != nil {
		t.Errorf("BitbucketTokens = %+v, want none", p.BitbucketTokens)
	}
}

// TestNodeTokensKeyCarriesNoValue: R5's "never a value, no secret name and no
// secret version". The node is readable by every run token of the job, so a
// hostile agent that reads this key learns which repositories a token reaches
// and nothing it could present as the credential (F21).
//
// The members are read off the two types rather than listed, so a field added
// to either is judged by this test without a second list: what an entry holds
// is the shape's own fields, and what the derivation is given is the record's —
// neither carries a value, and neither can gain one without failing here.
func TestNodeTokensKeyCarriesNoValue(t *testing.T) {
	key, err := DeriveBitbucketTokens(typedTokenRecords(), map[string]string{config.BitbucketTokenKey("ws-a"): "uuid-ws"})
	if err != nil {
		t.Fatalf("DeriveBitbucketTokens = %v", err)
	}
	raw, err := json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	var written struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	if len(written.Entries) != len(key.Entries) {
		t.Fatalf("the key marshals %d entries, want %d", len(written.Entries), len(key.Entries))
	}
	want := carrierMembers(reflect.TypeOf(BitbucketTokenNode{}))
	slices.Sort(want)
	// Every member an entry writes is one of the shape's — and, across the
	// three entries, every member of the shape is written, so a member added to
	// it is judged here rather than passing unnoticed.
	union := map[string]bool{}
	for i, entry := range written.Entries {
		for name := range entry {
			if !slices.Contains(want, name) {
				t.Errorf("entries[%d] carries %q, which is not a member of the entry's shape", i, name)
			}
			union[name] = true
		}
	}
	var got []string
	for name := range union {
		got = append(got, name)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("the key's entries carry %v, want the entry's own members %v", got, want)
	}
	for _, name := range want {
		if strings.Contains(name, "value") || strings.Contains(name, "secret") || strings.Contains(name, "version") {
			t.Errorf("the node's entry carries the member %q: the node is readable by every run token of the job", name)
		}
	}
	// The record the derivation is given is the operator's own statement, and
	// it carries no value either: the value is the bundle's, found by the
	// document key this key carries (T1).
	for _, name := range carrierMembers(reflect.TypeOf(config.BitbucketToken{})) {
		if strings.Contains(name, "value") || strings.Contains(name, "secret") {
			t.Errorf("the record carries the member %q: a local config file cannot state a token's value", name)
		}
	}
}

// TestOldRunnerReaderToleratesTheTokensKey is the runner's half of the skew R5
// states, the pair the image_catalog key set has: a runner of the release
// behind reads a node carrying this key, lists it as a key it does not know and
// enforces every value it does understand, which is P8 — an unknown TOP-LEVEL
// key is a newer Fugaro's, never a node to halt on. The older reader is not
// reached by spelling a key this package does not model, because
// knownTopLevelKeys holds this one: the same node is decoded twice, once under
// this key set and once under a copy of the list as it stood before the key.
//
// The key is at the top level for exactly this: every unknown member INSIDE a
// block this package decodes is refused, so a token's facts carried in the
// bitbucket_token block would make a runner of the release behind refuse the
// whole node.
func TestOldRunnerReaderToleratesTheTokensKey(t *testing.T) {
	node := `{"version": ` + itoa(Version) + `,` +
		` "bitbucket_token": {"allow_repos": ["bitbucket/workspace-a/repo-a"]},` +
		` "bitbucket_tokens": {"entries": [{"name": "bitbucket-token--ws-a", "type": "workspace",` +
		` "coverage": ["bitbucket/workspace-a/*"], "identity": "uuid-ws"}]}}`
	p, found, err := Decode([]byte(node))
	if err != nil || !found {
		t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: %s is a key this Fugaro reads", p.UnknownKeys, BitbucketTokensKey)
	}
	if p.BitbucketTokens == nil || len(p.BitbucketTokens.Entries) != 1 {
		t.Fatalf("BitbucketTokens = %+v, want the node's one entry", p.BitbucketTokens)
	}
	if got := p.BitbucketTokens.Entries[0]; got.Name != "bitbucket-token--ws-a" || got.Identity != "uuid-ws" {
		t.Errorf("the entry read as %+v, want the name and the identity it carries", got)
	}

	preKey := make(map[string]bool, len(knownTopLevelKeys))
	for k, v := range knownTopLevelKeys {
		preKey[k] = v
	}
	delete(preKey, BitbucketTokensKey)
	q, found, err := decodeWithKeys([]byte(node), preKey)
	if err != nil || !found {
		t.Fatalf("the reader that predates the key refused the node: (%v, %v, %v)", q, found, err)
	}
	if want := []string{BitbucketTokensKey}; !slices.Equal(q.UnknownKeys, want) {
		t.Errorf("the reader that predates the key lists UnknownKeys = %v, want %v: that is the node a CLI of that age refuses and a runner of it logs", q.UnknownKeys, want)
	}
	// What the older reader knows of the key is that it is there and that it
	// reads nothing of it: the key set is the whole of the difference this
	// fixture can state (that reader's struct predates the field, which no
	// fixture here can be), and it is the difference the skew is about — the
	// node is read, the key is listed, and nothing halts.
	//
	// Everything it does understand is read whole, which is what "tolerated"
	// means for a runner: the token block it enforces, and the rest of the node
	// beside it. A run that bootstrapped under the release behind therefore
	// keeps the credential its own job was deployed with — the fallback R5
	// states, and never a node it halts on.
	if q.BitbucketToken == nil || !slices.Equal(q.BitbucketToken.AllowRepos, []string{"bitbucket/workspace-a/repo-a"}) {
		t.Errorf("the reader that predates the key read bitbucket_token as %+v, want the block it enforces", q.BitbucketToken)
	}
	if q.Version != Version {
		t.Errorf("the reader that predates the key read Version = %d, want %d", q.Version, Version)
	}
}
