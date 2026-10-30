package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// bitbucketTokenConfig is the operator's decision to hold one workspace or
// project access token for the whole project, and the one profile that mounts
// it. Publishing it is what BW-T2.3 adds to the publish path: the node's
// bitbucket_token block is DERIVED from project_secrets.bitbucket-token
// (projectpolicy.DeriveBitbucketToken, D473/D493) the way the accounts block
// is derived from claude_accounts, and the policy's own decoder refuses a
// profile that mounts the token without one.
const bitbucketTokenConfig = "project_secrets:\n" +
	"  bitbucket-token: { allow_data_to: [workspace-a/repo-a] }\n" +
	"profiles:\n" +
	"  standard: { provider: bitbucket, cpu: 1, memory: 2Gi, timeout_class: 6h, allow_repos: [bitbucket/workspace-a/repo-a], secrets: [bitbucket-token] }\n"

// bitbucketTokenWithoutAProfile is the same operator decision with no profile
// mounting the token. The block is derived anyway (T2.1 as amended by
// docs/secrets-brokering's D493): a proxied per-repository job holds no
// credential at all and reads this block as its whole admission, so it is not
// a statement about a mount.
const bitbucketTokenWithoutAProfile = "project_secrets:\n" +
	"  bitbucket-token: { allow_data_to: [workspace-a/repo-a, workspace-a/api-*] }\n"

// TestPolicyPublishWritesTheBitbucketTokenBlock: the published node carries the
// block the token's reach is read from, and it carries none at all when the
// config derives none — the two states that mean "this token reaches no
// repository" (D493).
func TestPolicyPublishWritesTheBitbucketTokenBlock(t *testing.T) {
	f := newProjectFixture(t, bitbucketTokenConfig)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("publishing a profile that mounts the project's Bitbucket token: %v", err)
	}
	bt := publishedPolicy(t, f).BitbucketToken
	if bt == nil {
		t.Fatal("the published node carries no bitbucket_token block, so the token's reach is stated nowhere in it")
	}
	if want := []string{"bitbucket/workspace-a/repo-a"}; !slices.Equal(bt.AllowRepos, want) {
		t.Errorf("bitbucket_token.allow_repos = %v, want %v", bt.AllowRepos, want)
	}
	if bt.Proxy {
		t.Error("bitbucket_token.proxy = true, and this config does not set the operator's switch")
	}

	// No profile mounts the token, and the block is derived all the same.
	f = newProjectFixture(t, bitbucketTokenWithoutAProfile)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	bt = publishedPolicy(t, f).BitbucketToken
	if bt == nil {
		t.Fatal("the published node carries no bitbucket_token block although project_secrets names repositories: a proxied run's admission is this block and no profile had to mount the token for it to exist (D493)")
	}
	if want := []string{"bitbucket/workspace-a/api-*", "bitbucket/workspace-a/repo-a"}; !slices.Equal(bt.AllowRepos, want) {
		t.Errorf("bitbucket_token.allow_repos = %v, want %v (provider-qualified, sorted)", bt.AllowRepos, want)
	}

	// Nothing in the config derives a block, so the node's document carries no
	// key at all: an empty list would read as a written block, which the node's
	// own decoder refuses.
	f = newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if bt := publishedPolicy(t, f).BitbucketToken; bt != nil {
		t.Errorf("bitbucket_token = %+v, want none: this config holds no project_secrets.bitbucket-token entry", bt)
	}
	if b := publishNodeBytes(t, f); strings.Contains(string(b), "bitbucket_token") {
		t.Errorf("the published node carries a bitbucket_token key:\n%s", b)
	}
}

// TestPolicyPublishWritesTheProxySwitch: proxy is the operator's
// per-installation switch (D493) and it lives in project_secrets, so the
// publish is the one place it reaches the node — and the diff says which way it
// is set, because it decides whether a job of this project holds the token at
// all.
func TestPolicyPublishWritesTheProxySwitch(t *testing.T) {
	f := newProjectFixture(t, "project_secrets:\n"+
		"  bitbucket-token: { allow_data_to: [workspace-a/repo-a], proxy: true }\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	bt := publishedPolicy(t, f).BitbucketToken
	if bt == nil {
		t.Fatal("the published node carries no bitbucket_token block")
	}
	if !bt.Proxy {
		t.Error("bitbucket_token.proxy = false, want the operator's switch: the node is where the runner and the credentials service read it")
	}
	if !strings.Contains(out, "bitbucket_token.proxy: on") {
		t.Errorf("the publish diff does not say the token is forwarded:\n%s", out)
	}
}

// TestPolicyPublishWritesTheBitbucketAPIRoots: the owner's allow-listed API
// roots (D474) are the operator's local key, published into the node the run
// reads them from — and a config that lists none publishes no key.
func TestPolicyPublishWritesTheBitbucketAPIRoots(t *testing.T) {
	f := newProjectFixture(t, "bitbucket_api_roots: [https://api.bitbucket.example/2.0]\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://api.bitbucket.example/2.0"}; !slices.Equal(publishedPolicy(t, f).BitbucketAPIRoots, want) {
		t.Errorf("bitbucket_api_roots = %v, want %v", publishedPolicy(t, f).BitbucketAPIRoots, want)
	}

	f = newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if got := publishedPolicy(t, f).BitbucketAPIRoots; len(got) != 0 {
		t.Errorf("bitbucket_api_roots = %v, want none", got)
	}
	if b := publishNodeBytes(t, f); strings.Contains(string(b), "bitbucket_api_roots") {
		t.Errorf("the published node carries a bitbucket_api_roots key:\n%s", b)
	}
}

// TestPolicyPublishRefusesABitbucketProfileAdmittingAnUnlistedRepository: a
// profile that mounts the token admits only what the block admits
// (projectpolicy.validBitbucketProfileRepos), and the publish is where the
// operator reads that — with the file to fix and the two steps to take.
func TestPolicyPublishRefusesABitbucketProfileAdmittingAnUnlistedRepository(t *testing.T) {
	f := newProjectFixture(t, "project_secrets:\n"+
		"  bitbucket-token: { allow_data_to: [workspace-a/repo-a] }\n"+
		"profiles:\n"+
		"  standard: { provider: bitbucket, cpu: 1, memory: 2Gi, timeout_class: 6h, allow_repos: [bitbucket/workspace-a/repo-b], secrets: [bitbucket-token] }\n")
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("publishing a profile that admits a repository the token's block does not was allowed")
	}
	for _, want := range []string{"bitbucket/workspace-a/repo-b", "project_secrets.bitbucket-token.allow_data_to", "local config", "publish again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a node was written anyway")
	}
}

// TestPolicyPublishAcceptsAnAllowListedBitbucketProfile: the other side of the
// same rule. One allow_data_to entry may serve a whole workspace (D464), so a
// profile admitting repositories the entry's pattern matches publishes, and the
// block keeps the pattern rather than the expansion.
func TestPolicyPublishAcceptsAnAllowListedBitbucketProfile(t *testing.T) {
	f := newProjectFixture(t, "project_secrets:\n"+
		"  bitbucket-token: { allow_data_to: [\"workspace-a/*\"] }\n"+
		"profiles:\n"+
		"  standard: { provider: bitbucket, cpu: 1, memory: 2Gi, timeout_class: 6h, allow_repos: [bitbucket/workspace-a/repo-a, bitbucket/workspace-a/repo-b], secrets: [bitbucket-token] }\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("publishing a profile inside the token's block: %v", err)
	}
	bt := publishedPolicy(t, f).BitbucketToken
	if bt == nil || !slices.Equal(bt.AllowRepos, []string{"bitbucket/workspace-a/*"}) {
		t.Fatalf("bitbucket_token = %+v, want the workspace pattern the operator allow-listed", bt)
	}
}

// TestPolicyPublishNeverDropsTheBitbucketBlockSilently: the block and the API
// roots are derived from THIS machine's config, so a node another operator's
// config published carries values this publish cannot re-derive. They are
// replaced rather than kept — the config is the one anchor — but never in
// silence: the publish already prints what it changes, so a removal is a "-"
// line and a different list a "~" line, the way allow_repos is printed.
func TestPolicyPublishNeverDropsTheBitbucketBlockSilently(t *testing.T) {
	otherOperatorNode := func() *projectpolicy.Policy {
		return &projectpolicy.Policy{
			Version:           projectpolicy.Version,
			Budget:            projectpolicy.Budget{Mode: "enforce", PerRunUSD: 5},
			AllowRepos:        []string{"bitbucket/acme/other", "github/acme/app"},
			BitbucketToken:    &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/workspace-a/repo-a"}},
			BitbucketAPIRoots: []string{"https://api.bitbucket.example/2.0"},
		}
	}

	// Nothing in this machine's config derives either, so both go.
	f := newProjectFixture(t, "")
	put(t, f, otherOperatorNode())
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"- bitbucket_token.allow_repos: bitbucket/workspace-a/repo-a",
		"- bitbucket_api_roots: https://api.bitbucket.example/2.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the publish dropped a value without a line (%q):\n%s", want, out)
		}
	}
	p := publishedPolicy(t, f)
	if p.BitbucketToken != nil || len(p.BitbucketAPIRoots) != 0 {
		t.Errorf("bitbucket_token = %+v, bitbucket_api_roots = %v, want none: this config derives none", p.BitbucketToken, p.BitbucketAPIRoots)
	}

	// And a config that derives a different list says so, rather than letting
	// the reader believe the node still carries what it carried.
	f = newProjectFixture(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [workspace-a/repo-b] }\n")
	put(t, f, otherOperatorNode())
	out, _, err = execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	const want = "~ bitbucket_token.allow_repos: bitbucket/workspace-a/repo-a → bitbucket/workspace-a/repo-b"
	if !strings.Contains(out, want) {
		t.Errorf("the publish replaced the block without a line:\nwant %q in\n%s", want, out)
	}
	if got := publishedPolicy(t, f).BitbucketToken; got == nil || !slices.Equal(got.AllowRepos, []string{"bitbucket/workspace-a/repo-b"}) {
		t.Errorf("bitbucket_token = %+v, want this config's own list", got)
	}
}

// TestPolicyPublishRefusesABitbucketAPIRootTheNodeWouldRefuse: the local key is
// copied into the node and held to the node's own rule there — one rule, in
// projectpolicy, applied by the decode the publish runs before it writes
// anything (D474). So a root the node refuses is a publish that changed
// nothing, and the operator reads which entry it was.
func TestPolicyPublishRefusesABitbucketAPIRootTheNodeWouldRefuse(t *testing.T) {
	f := newProjectFixture(t, "bitbucket_api_roots: [http://api.bitbucket.example/2.0]\n")
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("publishing an http API root was allowed: the runner sends the Bitbucket token to this root")
	}
	for _, want := range []string{"bitbucket_api_roots[0]", "http://api.bitbucket.example/2.0", "https"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a node was written anyway")
	}
}

// publishNodeBytes is the published node as the document it is, for the
// assertions that are about a key's presence rather than a decoded value.
func publishNodeBytes(t *testing.T, f *projectFixture) []byte {
	t.Helper()
	b, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
