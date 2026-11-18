package cli

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// proxySwitchFixture is the installation every row of this file runs on: the
// policy fixture's config (one GitHub repository and one Bitbucket one,
// acme/other), with the credentials service's block — which is what the flip
// needs before it does anything at all — and a Secret Manager fake behind the
// endpoints the bundle read goes through.
func proxySwitchFixture(t *testing.T) (*projectFixture, *gcpfake.Secrets) {
	t.Helper()
	sm := gcpfake.NewSecrets(t)
	f := newProjectFixture(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
	rewriteConfigLine(t, "no_auth: true", "no_auth: true, secret_manager: "+sm.URL+"/")
	return f, sm
}

// seedProxyBundle stores one version of the credentials service's bundle
// document, as `fugaro creds set` writes it, and lets the fake hand the value
// back: a nil doc is the container with no version at all, which is the state a
// fresh deploy leaves.
func seedProxyBundle(t *testing.T, sm *gcpfake.Secrets, doc []byte) {
	t.Helper()
	sm.Seed(credsvc.BundleSecretID(initProjectName), map[string]string{gcp.LabelManaged: gcp.ManagedValue}, doc)
	sm.AllowAccess()
}

// proxySwitchBundle is the document left by `fugaro creds set bitbucket-token`
// and `fugaro creds set claude-oauth-token`: the project's one workspace token
// on the legacy key the flip's own gate reads
// (bundleHoldsBitbucketToken), beside the model credential the installation
// was already serving.
func proxySwitchBundle(t *testing.T) []byte {
	t.Helper()
	return []byte(config.BitbucketSecret + ": " + credsValue(t, "bb") + "\n" +
		config.AnthropicSecret + ": " + credsValue(t, "model") + "\n")
}

// proxySwitchConfig is the fixture's local config as the run left it, parsed
// again from the bytes on disk: the half of the switch an operator reads and
// the next `fugaro secrets` command edits.
func proxySwitchConfig(t *testing.T, f *projectFixture) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Parse([]byte(readFileString(t, f.path)))
	if err != nil {
		t.Fatalf("the local config this run left does not read back: %v", err)
	}
	return lc
}

// proxyNodeOn is the published node as the database holds it, decoded: what
// every row below asserts about the node is its bitbucket_token block, which is
// derived from the local config and is the whole admission of a proxied run.
func proxyNodeOn(t *testing.T, f *projectFixture) *projectpolicy.Policy {
	t.Helper()
	b, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	p, found, err := projectpolicy.Decode(b)
	if err != nil || !found {
		t.Fatalf("the published node does not decode (found=%v): %v", found, err)
	}
	return p
}

// publishProxySwitchNode writes the node this release publishes for the
// fixture's config, so that a row's own run is what moves it: the state of an
// installation that is up to date on everything but the proxy, which is exactly
// the one a node already at this version has no migration step for (the drift
// row in policyConfigDrift is the whole of what carries the block there).
func publishProxySwitchNode(t *testing.T, f *projectFixture) {
	t.Helper()
	if out, errOut, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("the fixture's own publish failed: %v\n%s\n%s", err, out, errOut)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Fatalf("the fixture's published node is version %d, want %d: every row below is about what the run does to a node this binary's own publish wrote", got, projectpolicy.Version)
	}
}

// appendProxySwitchConfig adds a block to the fixture's local config, as the
// operator's own edit: the rows that start from a file the update finds
// something in use it.
func appendProxySwitchConfig(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// proxyEntryOn is the fixture's Bitbucket entry after a run, and whether it is
// there at all.
func proxyEntryOn(t *testing.T, f *projectFixture) (config.ProjectSecret, bool) {
	t.Helper()
	entry, ok := proxySwitchConfig(t, f).ProjectSecrets[config.BitbucketSecret]
	return entry, ok
}

// TestUpdateTurnsTheBitbucketProxyOn is S6's red step: the policy stage of one
// `fugaro update` on a project with a Bitbucket repository whose installation
// carries the credentials service turns the proxy on — proxy: true in the local
// config's bitbucket-token entry, with the project's Bitbucket repository in
// that entry's allow_data_to, and the node's bitbucket_token block published
// from the very same entry in the same write (D2306, D493).
//
// The second row is the runner pin the acceptance names (proxySince): a binary
// whose release is the one BEFORE the runner half turns nothing on, because the
// launch gate refuses every run of the repository such a flip produced, and the
// flip must not be the thing that makes a project unrunnable.
//
// The third row is the question the gate asks of the bundle and not the account
// bookkeeping beside it: the bundle holds the workspace token alone, with no
// model credential in it at all, and the flip is still the right write — it is
// readCredentialsBundle's reading that answers this, not the readiness question
// the credentials switch is judged by (bundleHoldsBitbucketToken).
//
// The last row is the acceptance's "a project already on it is unchanged": an
// entry that already forwards for every one of the project's Bitbucket
// repositories is one this update leaves exactly where it is, file and node
// alike — the entry is the operator's, written by `fugaro secrets set --proxy`
// or by an earlier update, and a step that rewrote it would be the update
// publishing a document nobody changed.
func TestUpdateTurnsTheBitbucketProxyOn(t *testing.T) {
	const forwarding = "project_secrets:\n  " + config.BitbucketSecret + ": { proxy: true, allow_data_to: [acme/other] }\n"
	for name, tc := range map[string]struct {
		release string
		extra   string
		doc     []byte
		turn    bool
	}{
		"a binary at the release that carries the proxy":     {release: proxySince, turn: true, doc: proxySwitchBundle(t)},
		"a binary at the release before it (the runner pin)": {release: shiftRelease(t, proxySince, -1), turn: false},
		"a bundle holding the workspace token alone": {
			release: proxySince, turn: true,
			doc: []byte(config.BitbucketSecret + ": " + credsValue(t, "bb") + "\n"),
		},
		"a project whose entry already forwards": {release: proxySince, extra: forwarding, turn: false},
	} {
		f, sm := proxySwitchFixture(t)
		if tc.extra != "" {
			appendProxySwitchConfig(t, f.path, tc.extra)
		}
		publishProxySwitchNode(t, f)
		seedProxyBundle(t, sm, tc.doc)
		before := readFileString(t, f.path)
		beforeNode, err := json.Marshal(proxyNodeOn(t, f).BitbucketToken)
		if err != nil {
			t.Fatal(err)
		}
		if tc.extra != "" && string(beforeNode) == "null" {
			t.Fatalf("%s: the fixture's node carries no bitbucket_token block although the config forwards, so this row says nothing about leaving a project on the proxy alone", name)
		}
		useVersion(t, tc.release)
		h := newMigrationRig(t, f, &initOptions{})

		out, err := h.apply(t, true)
		if err != nil {
			t.Fatalf("%s: the policy stage failed: %v\n%s", name, err, out)
		}
		entry, ok := proxyEntryOn(t, f)
		p := proxyNodeOn(t, f)
		if !tc.turn {
			// Nothing was turned on: the file is the one the run found, and
			// the node's block is the one it carried — none at all for a row
			// whose config states no entry, the operator's own for the row
			// that already forwards.
			if got := readFileString(t, f.path); got != before {
				t.Errorf("%s: the run rewrote the local config:\n%s", name, got)
			}
			after, err := json.Marshal(p.BitbucketToken)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(beforeNode) {
				t.Errorf("%s: the node's bitbucket_token moved from %s to %s:\n%s", name, beforeNode, after, out)
			}
			if tc.extra == "" && ok && entry.ProxyOn() {
				t.Errorf("%s: the config's %s entry forwards after the run:\n%s", name, config.BitbucketSecret, out)
			}
			if strings.Contains(out, "turned the credentials service's Bitbucket proxy on") {
				t.Errorf("%s: the run says it turned the proxy on:\n%s", name, out)
			}
			continue
		}
		if !ok || !entry.ProxyOn() {
			t.Fatalf("%s: the config's %s entry does not forward after the run (entry %+v, present %v): the flip is the write S6 is\n%s", name, config.BitbucketSecret, entry, ok, out)
		}
		if !slices.Contains(entry.AllowDataTo, "acme/other") {
			t.Errorf("%s: the config's %s entry reaches %v, want acme/other among them: the credential a proxied run fetches is the one for the repository's workspace (D493)\n%s", name, config.BitbucketSecret, entry.AllowDataTo, out)
		}
		if p.BitbucketToken == nil || !p.BitbucketToken.Proxy {
			t.Fatalf("%s: the node carries bitbucket_token = %+v, want a block with proxy: true: the block is derived from the entry the same run wrote, and it is the whole admission of a proxied run\n%s", name, p.BitbucketToken, out)
		}
		if want := "bitbucket/acme/other"; !slices.Contains(p.BitbucketToken.AllowRepos, want) {
			t.Errorf("%s: the node's bitbucket_token.allow_repos = %v, want %s among them: the block is DeriveBitbucketToken's answer for the entry this run wrote\n%s", name, p.BitbucketToken.AllowRepos, want, out)
		}
		if !strings.Contains(out, "turned the credentials service's Bitbucket proxy on") {
			t.Errorf("%s: the run does not say the proxy was turned on:\n%s", name, out)
		}
	}
}

// TestUpdateBitbucketProxyConvergesInOneUpdate: ONE `fugaro update` of a project
// at the previous version's node converges both halves — the node migrated to
// this version with the bitbucket_token block the config states, and the
// operator's file carrying the switch — and the run's own repository stage,
// which reads the pair and refuses a disagreement, is not stopped by the order
// the two are written in (the node first, the file after it: an entry that is
// absent is never a disagreement for resolveBitbucketProxied).
//
// A second update is the operator step the HARD RULE forbids, and a second
// `fugaro secrets set --proxy` is the second command it forbids with it.
func TestUpdateBitbucketProxyConvergesInOneUpdate(t *testing.T) {
	f, sm := proxySwitchFixture(t)
	publishProxySwitchNode(t, f)
	behind := f.db.Value(projectpolicy.PathShared())
	node, ok := behind.(map[string]any)
	if !ok {
		t.Fatalf("the published node is %T, not an object", behind)
	}
	node["version"] = projectpolicy.Version - 1
	f.db.Set(projectpolicy.PathShared(), node)
	seedProxyBundle(t, sm, proxySwitchBundle(t))
	useVersion(t, proxySince)
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("one update did not converge a project whose node is one version behind and whose config the flip is about: %v\n%s", err, out)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Errorf("the node is version %d after one run, want %d\n%s", got, projectpolicy.Version, out)
	}
	p := proxyNodeOn(t, f)
	if p.BitbucketToken == nil || !p.BitbucketToken.Proxy {
		t.Fatalf("the migrated node carries bitbucket_token = %+v, want the block the same run's config edit implies: a node that moves with no block beside a file that forwards is the pair resolveBitbucketProxied stops a run over\n%s", p.BitbucketToken, out)
	}
	entry, ok := proxyEntryOn(t, f)
	if !ok || !entry.ProxyOn() || !slices.Contains(entry.AllowDataTo, "acme/other") {
		t.Errorf("the config's %s entry is %+v (present %v) after one run, want it forwarding for acme/other: the switch is the file half of the same step\n%s", config.BitbucketSecret, entry, ok, out)
	}
}

// TestUpdateDefersTheBitbucketProxyOnAReSeed is the fix for the failure the
// switch met on a file behind the published node: this machine's config records
// a generation the node has moved past, so the stage plans D854's re-seed —
// which writes the LOCAL FILE and never the node — and the flip's own edit is
// what makes the file state a switch the node does not carry. The re-seed has no
// member for the block (it splices the hands a hand edit can reach), so the run
// used to stop with "put the published values of bitbucket-token there by hand
// and run fugaro update again": the second command the HARD RULE forbids, and
// one that repeated forever, because the run that refused to write the switch
// never converged the file to the node's generation either.
//
// The switch is deferred instead: the plan is re-read from the config this run
// holds, which is the state the re-seed converges to, so this update records the
// generation and finishes with nothing written to the node, and the NEXT update
// — the file and the node one generation again — turns the proxy on.
func TestUpdateDefersTheBitbucketProxyOnAReSeed(t *testing.T) {
	f, sm := proxySwitchFixture(t)
	publishProxySwitchNode(t, f)
	stale := recordedGeneration(t, f)
	if stale == "" {
		t.Fatal("the fixture's publish recorded no generation, so there is nothing for this row's file to be behind")
	}
	setConfig(t, f, func(s string) string {
		return strings.Replace(s, "policy_generation: "+stale, "policy_generation: etag-from-an-earlier-publish", 1)
	})
	seedProxyBundle(t, sm, proxySwitchBundle(t))
	useVersion(t, proxySince)
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the update did not converge a file behind the published node: %v\n%s", err, out)
	}
	if strings.Contains(out, "by hand") {
		t.Errorf("the update left the switch for a hand edit:\n%s", out)
	}
	if _, ok := proxyEntryOn(t, f); ok {
		t.Errorf("the deferred run wrote the config's %s entry anyway:\n%s", config.BitbucketSecret, out)
	}
	if p := proxyNodeOn(t, f); p.BitbucketToken != nil {
		t.Errorf("the deferred run published bitbucket_token = %+v, want none: the re-seed writes no node\n%s", p.BitbucketToken, out)
	}
	if got := recordedGeneration(t, f); got != nodeETag(t, f) {
		t.Errorf("policy_generation = %q after the run, want the node's own generation (%q): a file that stays behind the node defers the switch on every update\n%s", got, nodeETag(t, f), out)
	}
	if p := proxyNodeOn(t, f); p.Version != projectpolicy.Version {
		t.Errorf("the node is version %d after the run, want %d", p.Version, projectpolicy.Version)
	}

	// The file and the node are one generation again, so the next update is the
	// one that turns the switch on.
	h = newMigrationRig(t, f, &initOptions{})
	out, err = h.apply(t, true)
	if err != nil {
		t.Fatalf("the update after the re-seed failed: %v\n%s", err, out)
	}
	entry, ok := proxyEntryOn(t, f)
	if !ok || !entry.ProxyOn() || !slices.Contains(entry.AllowDataTo, "acme/other") {
		t.Fatalf("the config's %s entry is %+v (present %v) after the second update, want it forwarding for acme/other\n%s", config.BitbucketSecret, entry, ok, out)
	}
	if p := proxyNodeOn(t, f); p.BitbucketToken == nil || !p.BitbucketToken.Proxy {
		t.Errorf("the node carries bitbucket_token = %+v after the second update, want the block the entry implies\n%s", p.BitbucketToken, out)
	}
}

// TestUpdateLeavesAStatedOffSwitchOff is the owner's ruling of 2026-11-18: the
// switch is tri-state, and only an ABSENT field is one this update may answer
// (config.ProjectSecret.Proxy). An entry that writes proxy: false is the
// operator's own no — the state a hand edit leaves, since `fugaro secrets set
// --proxy=false` takes the key away rather than writing a second spelling of off
// — and a step of the update that turned it on would be the update overruling
// the one person who answered.
//
// The file is left byte for byte as it was, and the node stays as the entry
// states it: the node's block is derived from the same entry, so an entry
// stating off derives a block stating off and there is nothing for this run to
// publish.
func TestUpdateLeavesAStatedOffSwitchOff(t *testing.T) {
	f, sm := proxySwitchFixture(t)
	appendProxySwitchConfig(t, f.path, "project_secrets:\n  "+config.BitbucketSecret+": { proxy: false, allow_data_to: [acme/other] }\n")
	publishProxySwitchNode(t, f)
	seedProxyBundle(t, sm, proxySwitchBundle(t))
	before := readFileString(t, f.path)
	useVersion(t, proxySince)
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the update failed on a config that states the switch off: %v\n%s", err, out)
	}
	entry, ok := proxyEntryOn(t, f)
	if !ok || entry.ProxyOn() || !entry.ProxySet() {
		t.Errorf("the config's %s entry reads on=%v stated=%v after the run, want the stated off it found: only an absent field is one this update answers (the owner's ruling of 2026-11-18)\n%s",
			config.BitbucketSecret, entry.ProxyOn(), entry.ProxySet(), out)
	}
	if got := readFileString(t, f.path); got != before {
		t.Errorf("the update rewrote the local config of a project whose entry states the switch off:\n%s", got)
	}
	if strings.Contains(out, "turned the credentials service's Bitbucket proxy on") {
		t.Errorf("the update says it turned the proxy on:\n%s", out)
	}
}

// TestSecretsDenyKeepsTheSwitchAnswerItFound: the second half of the same
// ruling, on the command that reformats an entry. A flow list is the shape
// docs/git-providers.md teaches the Bitbucket allow-list in, and the rewrite
// that makes it editable writes the entry again — so a stated off is written
// back as stated off rather than dropped, or the entry the update finds
// afterwards would be one NOBODY answered and the next `fugaro update` would
// turn the proxy on.
func TestSecretsDenyKeepsTheSwitchAnswerItFound(t *testing.T) {
	secretsRmFugaro(t)
	secretsFixture(t)
	projectSecretFixture(t, "project_secrets:\n  "+config.BitbucketSecret+": { proxy: false, allow_data_to: [\"acme/app\"] }\n")
	path := os.Getenv("FUGARO_CONFIG")
	noTerminal(t)

	out, errOut, err := execute(t, "secrets", "deny", config.BitbucketSecret, "acme/app")
	if err != nil {
		t.Fatalf("secrets deny %s acme/app: %v\nstdout %s\nstderr %s", config.BitbucketSecret, err, out, errOut)
	}
	entry := secretsPolicyEntry(t, path, config.BitbucketSecret)
	if !entry.ProxySet() || entry.ProxyOn() {
		t.Errorf("the entry reads on=%v stated=%v after the reformatting edit, want the stated off written back: an entry left stating nothing about the switch is one the next update turns the proxy on for",
			entry.ProxyOn(), entry.ProxySet())
	}
	if len(entry.AllowDataTo) != 0 {
		t.Errorf("allow_data_to = %v, want the removal the run asked for", entry.AllowDataTo)
	}
	if got := readFileString(t, path); !strings.Contains(got, "proxy: false") {
		t.Errorf("the rewritten entry does not carry the stated off:\n%s", got)
	}
}

// TestUpdateLeavesABlockThisMachineNeverStated is the second fix: the node
// carries a bitbucket_token block and this machine's config states no entry for
// it at all. The published node is the ONE source of proxiedness and an absent
// entry is not a statement about it (resolveBitbucketProxied), so the update
// leaves the block exactly where it is — while an entry that IS stated still
// reports its own answer, an empty allow_data_to included, which is the removal
// an operator has to see.
//
// The state is the ordinary one of a second machine: another machine (or the
// console) published the block, this machine's local config never named the
// entry, and its bundle holds no Bitbucket token, so the flip is held back. A
// row derived from the absent entry used to report the block as drift and
// publish the node without it, which takes every proxied run of the repository
// apart with no one having asked.
func TestUpdateLeavesABlockThisMachineNeverStated(t *testing.T) {
	const stated = "project_secrets:\n  " + config.BitbucketSecret + ": { proxy: true, allow_data_to: [acme/other] }\n"
	f, sm := proxySwitchFixture(t)
	appendProxySwitchConfig(t, f.path, stated)
	publishProxySwitchNode(t, f)
	before := proxyNodeOn(t, f)
	if before.BitbucketToken == nil {
		t.Fatal("the fixture's own publish carries no bitbucket_token block, so this row is not about a node that states one")
	}
	// The entry goes: what is left is a machine that never stated it, beside the
	// node the machine that did published.
	rewriteConfigLine(t, stated, "")
	seedProxyBundle(t, sm, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
	useVersion(t, proxySince)
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the update failed on a machine that states no entry: %v\n%s", err, out)
	}
	after := proxyNodeOn(t, f)
	if after.BitbucketToken == nil || !after.BitbucketToken.Proxy || !slices.Contains(after.BitbucketToken.AllowRepos, "bitbucket/acme/other") {
		t.Errorf("the node carries bitbucket_token = %+v after the run, want the block it carried (%+v) left exactly where it is: this machine stated nothing about the switch, and a row read off the absent entry removes a block no one here asked to remove\n%s",
			after.BitbucketToken, before.BitbucketToken, out)
	}
	if _, ok := proxyEntryOn(t, f); ok {
		t.Errorf("the run wrote the config's %s entry, which this machine never stated:\n%s", config.BitbucketSecret, out)
	}
}

// TestUpdateAsksForTheWorkspaceTokenOnce is R7 on the door that cannot take the
// value: the proxy forwards the workspace token the bundle holds, so a project
// whose bundle holds none is one the flip must not turn on (a proxied run has no
// fallback at all, D143) — and the update says so once, with the command that
// stores it, and converges everything else.
//
// The two rows are the two no-token bundles the read can answer with: a
// container holding no version at all, which is the state a fresh deploy leaves
// behind, and a document holding the project's model credential and no Bitbucket
// token. Neither takes a value from anyone: the bundle is the owner's own, and
// `fugaro creds set` is refused outright in a coding agent's session and in a
// run's own process (C1.25, T2) — the line is the no-prompt policy's answer in
// place of a question (D1540/F14).
func TestUpdateAsksForTheWorkspaceTokenOnce(t *testing.T) {
	for name, doc := range map[string][]byte{
		"a bundle with no version at all":     nil,
		"a bundle holding no Bitbucket token": []byte(config.AnthropicSecret + ": " + credsValue(t, "model") + "\n"),
	} {
		f, sm := proxySwitchFixture(t)
		publishProxySwitchNode(t, f)
		seedProxyBundle(t, sm, doc)
		before := readFileString(t, f.path)
		useVersion(t, proxySince)
		h := newMigrationRig(t, f, &initOptions{})

		out, err := h.apply(t, true)
		if err != nil {
			t.Fatalf("%s: the update was refused rather than told what to store: %v\n%s", name, err, out)
		}
		if _, ok := proxyEntryOn(t, f); ok {
			t.Errorf("%s: the config's %s entry was written:\n%s", name, config.BitbucketSecret, out)
		}
		if p := proxyNodeOn(t, f); p.BitbucketToken != nil {
			t.Errorf("%s: the node carries bitbucket_token = %+v, want none: the block is derived from an entry nothing wrote\n%s", name, p.BitbucketToken, out)
		}
		if got := readFileString(t, f.path); got != before {
			t.Errorf("%s: the run rewrote the local config:\n%s", name, got)
		}
		if n := strings.Count(out, "creds set "+config.BitbucketTokenKey("acme")); n != 1 {
			t.Errorf("%s: the ask appears %d time(s) in the run's own output, want once:\n%s", name, n, out)
		}
		for _, want := range []string{"leaves the Bitbucket proxy switch off", "Nothing was written"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: the line does not say %q, so it is not the gate's own sentence:\n%s", name, want, out)
			}
		}
	}
}

// TestUpdateHoldsTheSwitchBackOverASecondWorkspace is D2314 as the owner
// amended it on 2026-11-18: one Bitbucket access token reaches the repositories
// of one workspace and the proxy forwards that one token, so a project whose
// Bitbucket repositories span two workspaces is one the credentials service
// cannot serve — and the update SAYS so and holds the switch back instead of
// failing the policy stage over it. The whole upgrade is this one command, and a
// project whose repositories span two workspaces is one this update converges
// every other step of whether or not the operator ever moves one: the file and
// the node are left exactly as they were, the switch is not written, and the
// line names the second workspace and a repository of each, because the fix is
// the operator's own.
func TestUpdateHoldsTheSwitchBackOverASecondWorkspace(t *testing.T) {
	f, sm := proxySwitchFixture(t)
	publishProxySwitchNode(t, f)
	seedProxyBundle(t, sm, proxySwitchBundle(t))
	rewriteConfigLine(t, "  acme/other: { provider: bitbucket, workflows: [web] }",
		"  acme/other: { provider: bitbucket, workflows: [web] }\n  other-ws/repo: { provider: bitbucket, workflows: [web] }")
	before := readFileString(t, f.path)
	useVersion(t, proxySince)
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the update failed over a project whose Bitbucket repositories are in two workspaces, want the switch held back and every other step done: %v\n%s", err, out)
	}
	for _, want := range []string{"leaves the Bitbucket proxy switch off", "acme", "other-ws", "acme/other", "other-ws/repo", "D2314", "nothing was written", "finished every other step"} {
		if !strings.Contains(out, want) {
			t.Errorf("the line does not carry %q, so it does not name what the operator has to move:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "leaves the Bitbucket proxy switch off"); n != 1 {
		t.Errorf("the switch's own line appears %d time(s), want once:\n%s", n, out)
	}
	if _, ok := proxyEntryOn(t, f); ok {
		t.Errorf("the run wrote the %s entry anyway:\n%s", config.BitbucketSecret, out)
	}
	if got := readFileString(t, f.path); got != before {
		t.Errorf("the run rewrote the local config:\n%s", got)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Errorf("the node moved to version %d over a switch this run held back", got)
	}
	if p := proxyNodeOn(t, f); p.BitbucketToken != nil {
		t.Errorf("the held-back run published bitbucket_token = %+v", p.BitbucketToken)
	}
}
