package projectpolicy

import (
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
