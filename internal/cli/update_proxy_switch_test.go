package cli

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
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
			if tc.extra == "" && ok && entry.ProxyStated() {
				t.Errorf("%s: the config's %s entry forwards after the run:\n%s", name, config.BitbucketSecret, out)
			}
			if strings.Contains(out, "turned the credentials service's Bitbucket proxy on") {
				t.Errorf("%s: the run says it turned the proxy on:\n%s", name, out)
			}
			continue
		}
		if !ok || !entry.Proxying() {
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
	if !ok || !entry.Proxying() || !slices.Contains(entry.AllowDataTo, "acme/other") {
		t.Errorf("the config's %s entry is %+v (present %v) after one run, want it forwarding for acme/other: the switch is the file half of the same step\n%s", config.BitbucketSecret, entry, ok, out)
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

// TestUpdateRefusesASecondBitbucketWorkspace is D2314: one Bitbucket access
// token reaches the repositories of one workspace and the proxy forwards that
// one token, so a project whose Bitbucket repositories span two workspaces is
// one the credentials service cannot serve. The refusal names the second
// workspace and a repository of each, and it is raised where the flip would
// turn the switch on — so the file and the node are left exactly as they were,
// and nothing was written for an operator to undo.
func TestUpdateRefusesASecondBitbucketWorkspace(t *testing.T) {
	f, sm := proxySwitchFixture(t)
	publishProxySwitchNode(t, f)
	seedProxyBundle(t, sm, proxySwitchBundle(t))
	rewriteConfigLine(t, "  acme/other: { provider: bitbucket, workflows: [web] }",
		"  acme/other: { provider: bitbucket, workflows: [web] }\n  other-ws/repo: { provider: bitbucket, workflows: [web] }")
	before := readFileString(t, f.path)
	useVersion(t, proxySince)
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("the update turned a proxy on for a project whose Bitbucket repositories are in two workspaces, and the credentials service forwards one token per project:\n%s", out)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d: which project a repository belongs to is the operator's own decision\n%s", got, ExitUserError, err)
	}
	for _, want := range []string{"acme", "other-ws", "acme/other", "other-ws/repo", "D2314", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q, so it does not name what the operator has to move:\n%v", want, err)
		}
	}
	if _, ok := proxyEntryOn(t, f); ok {
		t.Errorf("the refused run wrote the %s entry anyway", config.BitbucketSecret)
	}
	if got := readFileString(t, f.path); got != before {
		t.Errorf("the refused run rewrote the local config:\n%s", got)
	}
	if got := nodeVersionOn(t, f.db); got != projectpolicy.Version {
		t.Errorf("the node moved to version %d on a refusal", got)
	}
	if p := proxyNodeOn(t, f); p.BitbucketToken != nil {
		t.Errorf("the refused run published bitbucket_token = %+v", p.BitbucketToken)
	}
}

// TestUpdateBitbucketProxySwitchDefersOnAReSeed is the review's first finding
// (S6's fix, 2026-11-18): the switch is a member `fugaro update` writes, and it
// must never be the reason the one command stops working.
//
// The machine's file records a policy_generation the published node has moved
// past — another machine's publish, or the console — so the policy stage is
// D854's re-seed: the file is written FROM the node rather than the node from
// the file. The switch's own bitbucket_token difference is a member that splice
// table has no case for, so a plan built from the switch's config landed on the
// fallback and the update stopped with "put the published values of
// bitbucket_token there by hand" — and went on stopping, on every later run,
// until the operator edited the file by hand.
//
// The switch is held back for that one run instead: the generation is recorded,
// the update converges, and the NEXT update turns the switch on. The mutation is
// a plan that keeps the switch on the re-seed path, which is the failure this
// test is the red step of.
func TestUpdateBitbucketProxySwitchDefersOnAReSeed(t *testing.T) {
	f, sm := proxySwitchFixture(t)
	publishProxySwitchNode(t, f)
	gen := recordedGeneration(t, f)
	if gen == "" {
		t.Fatal("the fixture's publish recorded no generation, so this run is not the re-seed this test is about")
	}
	setConfig(t, f, func(s string) string {
		return strings.Replace(s, "policy_generation: "+gen, "policy_generation: etag-from-an-earlier-publish", 1)
	})
	seedProxyBundle(t, sm, proxySwitchBundle(t))
	useVersion(t, proxySince)
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the Bitbucket proxy switch failed the re-seed of a machine whose file is one generation behind: %v\n%s", err, out)
	}
	if entry, ok := proxyEntryOn(t, f); ok {
		t.Errorf("the run turned the switch on beside a re-seed it holds back for: the config's %s entry is %+v\n%s", config.BitbucketSecret, entry, out)
	}
	if p := proxyNodeOn(t, f); p.BitbucketToken != nil {
		t.Errorf("the node carries bitbucket_token = %+v, want none: the block is derived from an entry nothing wrote\n%s", p.BitbucketToken, out)
	}
	// The generation IS recorded, which is the whole of what that run owed: the
	// next update reads a file written from the node it published and turns the
	// switch on then.
	if gen := recordedGeneration(t, f); gen != nodeETag(t, f) {
		t.Errorf("policy_generation = %q after the run, want the ETag the node is at (%q): the re-seed records the generation and the next update turns the switch on", gen, nodeETag(t, f))
	}
	for _, want := range []string{"leaves the Bitbucket proxy switch off", "moved past the generation", "the next update turns the switch on"} {
		if !strings.Contains(out, want) {
			t.Errorf("the deferral does not say %q, so the operator does not know the switch is one update away:\n%s", want, out)
		}
	}
	if strings.Contains(out, "turned the credentials service's Bitbucket proxy on") {
		t.Errorf("the run says it turned the proxy on:\n%s", out)
	}
}

// TestUpdateLeavesTheNodeBlockWhenTheConfigStatesNoEntry is the review's second
// finding (S6's fix, 2026-11-18), and it is the one that could take a project's
// proxy off: the drift comparison runs for every door, and an absent
// project_secrets entry derives nil exactly as an entry whose list is empty
// does.
//
// This machine's config states no such entry at all — a second operator's file,
// a config whose secrets live elsewhere, one whose entry was removed — while the
// published node carries the block the project's proxy is admitted by. Before
// the fix the run reported that difference and published the node WITHOUT
// bitbucket_token, turning the proxy off for the whole project and stranding the
// proxied runs in flight; an absent entry is no statement about the switch, so
// the run now leaves the node where it is.
//
// The switch is off on this machine by construction (no credentials_service
// block: the flip needs a service to forward from), so the door's note may not
// claim a launch is proxied either — which is the second half of the finding.
func TestUpdateLeavesTheNodeBlockWhenTheConfigStatesNoEntry(t *testing.T) {
	const entry = "project_secrets:\n  " + config.BitbucketSecret + ": { proxy: true, allow_data_to: [acme/other] }\n"
	f := newProjectFixture(t, entry)
	publishProxySwitchNode(t, f)
	if bt := proxyNodeOn(t, f).BitbucketToken; bt == nil || !bt.Proxy {
		t.Fatalf("the fixture's node carries bitbucket_token = %+v, want the block the entry above states: this test is about leaving one alone", bt)
	}
	// The entry goes: the node keeps the block, and this file says nothing about
	// the switch.
	setConfig(t, f, func(s string) string { return strings.Replace(s, entry, "", 1) })
	before := readFileString(t, f.path)
	h := newMigrationRig(t, f, &initOptions{})

	f.db.Set(budget.PathMode, budget.ModeEnforce)
	f.db.Set("agents", map[string]any{"proj-1234": map[string]any{"run-1": map[string]any{"repo": "github/acme/app"}}})
	if err := h.s.e.announceRunsInFlight(context.Background()); err != nil {
		t.Fatal(err)
	}
	note := h.out.String()
	if !strings.Contains(note, "wait for none") && !strings.Contains(note, "waits for none") {
		t.Fatalf("the run printed no note about the runs in flight, so the assertion below is about an empty string:\n%s", note)
	}
	if strings.Contains(note, "bitbucket proxy switch") {
		t.Errorf("the note names the Bitbucket proxy switch over an update that turns nothing on: this machine holds no credentials_service block, so no launch of the project is proxied by it\n%s", note)
	}

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the update failed over a config that states no project_secrets entry: %v\n%s", err, out)
	}
	bt := proxyNodeOn(t, f).BitbucketToken
	if bt == nil || !bt.Proxy {
		t.Fatalf("the node's bitbucket_token is %+v after the run, want the block the project's proxy is admitted by left in place: publishing the node without it turns the proxy off for every repository of the project\n%s", bt, out)
	}
	if want := "bitbucket/acme/other"; !slices.Contains(bt.AllowRepos, want) {
		t.Errorf("the node's bitbucket_token.allow_repos = %v, want %s among them: the block is the project's whole admission and this run may not narrow it\n%s", bt.AllowRepos, want, out)
	}
	if got := readFileString(t, f.path); got != before {
		t.Errorf("the run rewrote a local config it has nothing to say about:\n%s", got)
	}
	if strings.Contains(out, "turned the credentials service's Bitbucket proxy on") {
		t.Errorf("the run says it turned the proxy on:\n%s", out)
	}
}

// TestUpdateKeepsTheHoldAWrittenProxyFalseIs: the switch is tri-state (S6's fix,
// 2026-11-18, owner ruling): an entry that says nothing about proxy and one that
// writes `proxy: false` are two different states, and only the absent one is a
// state `fugaro update` turns on.
//
// No command of this program writes a false — `fugaro secrets set
// bitbucket-token --proxy=false` removes the key — so a written false is a hand
// edit and the hold it spells, and the whole update converges around it. The
// mutation is a flip that reads the field as a bool, which is the state this
// test is the red step of: it turned the switch on and widened the entry's
// allow_data_to under an operator who had said no.
func TestUpdateKeepsTheHoldAWrittenProxyFalseIs(t *testing.T) {
	const hold = "project_secrets:\n  " + config.BitbucketSecret + ": { proxy: false }\n"
	f, sm := proxySwitchFixture(t)
	appendProxySwitchConfig(t, f.path, hold)
	publishProxySwitchNode(t, f)
	seedProxyBundle(t, sm, proxySwitchBundle(t))
	before := readFileString(t, f.path)
	useVersion(t, proxySince)
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the update failed over an entry that holds the switch off: %v\n%s", err, out)
	}
	entry, ok := proxyEntryOn(t, f)
	if !ok || !entry.ProxyStated() || entry.Proxying() {
		t.Errorf("the config's %s entry is %+v (present %v), want the written hold left exactly as it is: `proxy: false` is the operator's own word about the switch\n%s", config.BitbucketSecret, entry, ok, out)
	}
	if slices.Contains(entry.AllowDataTo, "acme/other") {
		t.Errorf("the run widened an entry the operator holds the switch off on: allow_data_to = %v, want the list as it was\n%s", entry.AllowDataTo, out)
	}
	if got := readFileString(t, f.path); got != before {
		t.Errorf("the run rewrote the local config of a held-off switch:\n%s", got)
	}
	if strings.Contains(out, "turned the credentials service's Bitbucket proxy on") {
		t.Errorf("the run says it turned the proxy on over a written hold:\n%s", out)
	}
	if p := proxyNodeOn(t, f); p.BitbucketToken != nil {
		t.Errorf("the node carries bitbucket_token = %+v, want none: the derivation of a held-off entry with no allow_data_to is no block\n%s", p.BitbucketToken, out)
	}
}
