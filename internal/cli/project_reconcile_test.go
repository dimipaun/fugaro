package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// project_reconcile_test.go is the reconcile N4 adds to buildProjectPolicy
// (docs/config-ownership/implementation-plan.md, section N4): the members the
// local config became the carrier of — the credentials cascade, model_grades,
// the crafts family (MC18) and the node_removals record — are merged into the
// node the file's own statements are about, member by member, and never
// rebuilt from the file alone (D2607, D2608, reconciliation.md F5, F6, F22,
// F24).
//
// Both doors into that one build are driven here, because the plan's red step
// is that they reconcile the same node: `fugaro project policy publish` (the
// explicit door) and the policy stage `fugaro update` runs. The node they are
// handed is the one another machine's publish — or a hand edit — leaves, which
// is the state the reconcile exists for.

// reconciledMemberKeys are the members this file's tests are about: MC18's
// craft family and the two blocks N4 writes beside it.
var reconciledMemberKeys = []string{"credentials", "model_grades", "crafts", "craft_credentials", "craft_pools"}

// reconcileBlocks is those members as another machine's publish leaves them in
// the node: a cascade with every member written, two grades, a credential, a
// pool of it with an owner, and a craft on the pool. The fallback is `auto` on
// purpose — the fixture's budget block is enforcing with a positive ceiling, so
// the node decodes — and it is the value a publish from a file that says
// nothing must not quietly put back to oauth-only (D152).
func reconcileBlocks() map[string]any {
	return map[string]any{
		"credentials": map[string]any{
			"fallback":                         projectpolicy.FallbackAuto,
			"wait_threshold":                   "45s",
			"backoff":                          map[string]any{"initial": "1s", "cap": "30s"},
			"stop_after_exhaustion_per_window": true,
			"concurrency_per_token":            4,
		},
		"model_grades": map[string]any{
			config.GradeStrong:    map[string]any{"model": "claude-sonnet-5", "access": "node-main"},
			config.GradeStrongest: map[string]any{"model": "claude-opus-5-5"},
		},
		"craft_credentials": []any{map[string]any{"name": "node-main", "type": config.CredentialTypeAnthropic}},
		"craft_pools": []any{map[string]any{
			"name": "node-pool", "members": []any{"node-main"}, "owner": "owner@example.com",
		}},
		"crafts": map[string]any{
			config.CraftCoder: map[string]any{"model": "claude-opus-5-5", "access": "node-pool"},
		},
	}
}

// reconciledNode is a fixture whose node carries those members: the node of a
// project another machine published, written by hand rather than by a publish,
// so this machine's file records no generation for it — a checkout that has
// never published. The budget block is one the local config tightens rather
// than raises (9 against the file's 5), so no test here has to answer for a
// ceiling.
func reconciledNode(t *testing.T, f *projectFixture) {
	t.Helper()
	node := staleBudgetNode()
	for k, v := range reconcileBlocks() {
		node[k] = v
	}
	f.db.Set(projectpolicy.PathShared(), node)
}

// reconciledMembersOf is those members of the node as the document they are,
// for a comparison that is about them and nothing else: every other member of
// the node is the publish's own business.
func reconciledMembersOf(t *testing.T, f *projectFixture) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(nodeJSON(t, f, projectpolicy.PathShared())), &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, k := range reconciledMemberKeys {
		if v, ok := doc[k]; ok {
			out[k] = v
		}
	}
	return out
}

func reconciledMembers(t *testing.T, f *projectFixture) string {
	t.Helper()
	return membersText(t, reconciledMembersOf(t, f))
}

// TestReconcileSilenceKeepsTheNode: a file that says nothing about the cascade,
// the grades, the credentials, the pools and the crafts publishes the node's
// own for every one of them (D2607's preserve half, F5, F6). Without it a
// publish from a checkout whose file predates these keys would delete what
// another machine wrote — every run of the project losing the cascade it falls
// over on, the credential it spends and the craft access it goes out on.
//
// The diff is the other half and the reason this is a test and not a comment:
// the blocks are unchanged, so no +/- line of the diff above mentions them, and
// the operator who never wrote the keys would read that silence as "they are
// gone". Each block says `kept`, and the line carries the node's own values.
func TestReconcileSilenceKeepsTheNode(t *testing.T) {
	f := newProjectFixture(t, "")
	reconciledNode(t, f)
	want := reconciledMembers(t, f)

	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("a publish from a config silent about every one of these blocks was refused: %v\n%s", err, out)
	}
	if got := reconciledMembers(t, f); got != want {
		t.Errorf("a publish from a silent config rewrote the node's blocks:\n got %s\nwant %s", got, want)
	}
	for _, line := range []string{
		"credentials kept: fallback auto, wait_threshold 45s, backoff.initial 1s, backoff.cap 30s, stop_after_exhaustion_per_window true, concurrency_per_token 4",
		"model_grades kept: strong claude-sonnet-5 on node-main, strongest claude-opus-5-5",
		"craft_credentials kept: node-main: anthropic",
		"craft_pools kept: node-pool: members node-main, strategy failover, owner owner@example.com",
		"crafts kept: coder: model claude-opus-5-5, access node-pool",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("the diff does not tell the operator the block was kept (%q):\n%s", line, out)
		}
	}
}

// TestReconcileWrittenValueWinsFieldByField: a member the file writes is the
// member the node is written with, and the members it leaves out are the
// node's — one block at a time and one half of a value at a time (D2607). A
// file that writes `wait_threshold` has said nothing about the fallback, and a
// publish that read that silence as "the default" would put a project back on
// oauth-only for a block nobody meant to change; a grade written with a model
// and no access keeps the access the node serves it on.
func TestReconcileWrittenValueWinsFieldByField(t *testing.T) {
	f := newProjectFixture(t, "")
	reconciledNode(t, f)
	rewriteProjectConfig(t, f, "credential_cascade: { wait_threshold: 90s }\n"+
		"model_grades:\n  strong: { model: claude-opus-5-5 }\n"+
		"credentials:\n  - { name: node-main, type: anthropic }\n"+
		"pools:\n  - { name: node-pool, members: [node-main] }\n"+
		"crafts:\n  merge-judge: { model: claude-opus-5-5, access: node-pool }\n")

	if out, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("the publish was refused: %v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if p.Credentials == nil {
		t.Fatal("the publish dropped the cascade the node carried")
	}
	if got := p.Credentials.WaitThreshold; got != "90s" {
		t.Errorf("credentials.wait_threshold = %q, want the file's 90s", got)
	}
	if got := p.Credentials.Fallback; got != projectpolicy.FallbackAuto {
		t.Errorf("credentials.fallback = %q, want the node's %s: the file says nothing about it", got, projectpolicy.FallbackAuto)
	}
	if p.Credentials.Backoff == nil || p.Credentials.Backoff.Initial != "1s" || p.Credentials.Backoff.Cap != "30s" {
		t.Errorf("credentials.backoff = %+v, want the node's 1s/30s", p.Credentials.Backoff)
	}
	if p.Credentials.StopAfterExhaustionPerWindow == nil || !*p.Credentials.StopAfterExhaustionPerWindow {
		t.Errorf("credentials.stop_after_exhaustion_per_window = %v, want the node's true", p.Credentials.StopAfterExhaustionPerWindow)
	}
	if p.Credentials.ConcurrencyPerToken == nil || *p.Credentials.ConcurrencyPerToken != 4 {
		t.Errorf("credentials.concurrency_per_token = %v, want the node's 4", p.Credentials.ConcurrencyPerToken)
	}

	if p.ModelGrades == nil {
		t.Fatal("the publish dropped the grades the node carried")
	}
	if got := p.ModelGrades.Of(config.GradeStrong); got.Model != "claude-opus-5-5" || got.Access != "node-main" {
		t.Errorf("model_grades.strong = %+v, want the file's model and the node's access", got)
	}
	if got := p.ModelGrades.Of(config.GradeStrongest); got.Model != "claude-opus-5-5" {
		t.Errorf("model_grades.strongest = %+v, want the node's own: the file writes no grade of that name", got)
	}

	pools := map[string]config.Pool{}
	for _, q := range p.CraftPools {
		pools[q.Name] = q
	}
	if got, ok := pools["node-pool"]; !ok || got.Owner != "owner@example.com" || len(got.Members) != 1 || got.Members[0] != "node-main" {
		t.Errorf("craft_pools[node-pool] = %+v (present %v), want the file's members and the node's owner", got, ok)
	}
	if len(p.CraftCredentials) != 1 || p.CraftCredentials[0].Name != "node-main" {
		t.Errorf("craft_credentials = %+v, want the one credential both documents name", p.CraftCredentials)
	}
	if p.Crafts == nil {
		t.Fatal("the publish dropped the crafts the node carried")
	}
	if got := p.Crafts.MergeJudge; got.Model != "claude-opus-5-5" || got.Access != "node-pool" {
		t.Errorf("crafts.merge-judge = %+v, want the craft the file writes", got)
	}
	if got := p.Crafts.Coder; got.Model != "claude-opus-5-5" || got.Access != "node-pool" {
		t.Errorf("crafts.coder = %+v, want the node's own: the file writes no craft of that name", got)
	}
}

// TestReconcileExplicitUnsetRemoves: a name in the file's `node_removals`
// record is the one statement that takes a member away (D2607's removal half,
// F22). Without it a revoked grant stays served: a credential the file no
// longer defines would keep reaching runs through the node, and a pool a person
// was removed from would keep serving them.
//
// The record is spent by the publish that carries it out — the node no longer
// holds the name, so the next reconcile has nothing to remove — and the file
// says so, because a record that stayed behind would fire on every run.
func TestReconcileExplicitUnsetRemoves(t *testing.T) {
	f := newProjectFixture(t, "")
	reconciledNode(t, f)
	rewriteProjectConfig(t, f, "node_removals:\n  pools: [node-pool]\n  craft_credentials: [node-main]\n  crafts: [coder]\n")

	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("the publish of an explicit unset was refused: %v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if len(p.CraftPools) != 0 {
		t.Errorf("craft_pools = %+v, want the removed pool gone", p.CraftPools)
	}
	if len(p.CraftCredentials) != 0 {
		t.Errorf("craft_credentials = %+v, want the removed credential gone", p.CraftCredentials)
	}
	if p.Crafts != nil && *p.Crafts != (config.Crafts{}) {
		t.Errorf("crafts = %+v, want the removed craft gone", p.Crafts)
	}
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if !removedNames(lc).IsZero() {
		t.Errorf("the file still records removals the published node no longer holds: %+v", lc.NodeRemovals)
	}
	for _, removed := range []string{
		"node_removals pools node-pool cleared",
		"node_removals craft_credentials node-main cleared",
		"node_removals crafts coder cleared",
	} {
		if !strings.Contains(out, removed) {
			t.Errorf("the publish does not say %q was carried out:\n%s", removed, out)
		}
	}
}

// TestReconcileFirstRunNeverDeletes: the first reconcile of a node this
// machine's file did not write is additive (D2607). The file states one cascade
// member and nothing else, and every other member the node carries — the whole
// craft family, the grades, the rest of the cascade — is left exactly as it
// was: a checkout that adopts a project another machine published must not
// empty it on its first `fugaro update`.
func TestReconcileFirstRunNeverDeletes(t *testing.T) {
	f := newProjectFixture(t, "")
	reconciledNode(t, f)
	before := reconciledMembersOf(t, f)
	rewriteProjectConfig(t, f, "credential_cascade: { wait_threshold: 90s }\n")

	if out, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("the publish was refused: %v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if p.Credentials == nil || p.Credentials.WaitThreshold != "90s" {
		t.Fatalf("credentials = %+v, want the threshold the file writes", p.Credentials)
	}
	if p.Credentials.Fallback != projectpolicy.FallbackAuto {
		t.Errorf("credentials.fallback = %q, want the node's %s", p.Credentials.Fallback, projectpolicy.FallbackAuto)
	}
	after := reconciledMembersOf(t, f)
	delete(before, "credentials")
	delete(after, "credentials")
	if membersText(t, before) != membersText(t, after) {
		t.Errorf("a publish from a file that states one cascade member changed the node's other blocks:\n got %s\nwant %s",
			membersText(t, after), membersText(t, before))
	}
}

// TestReconcileRefusesInAnAgentSession: nothing publishes a grant from a coding
// agent's session, whatever the gate said (D2617). The gate is admitted here —
// `admitted=true`, the reading of a managed project whose gate judged the
// document and admitted it — and the refusal is still the session's own, asked
// by the reconcile beside the gate rather than by it: a credential or a pool a
// session queued in the local config is published by the owner's own next
// `fugaro update`, never by the session that wrote it.
//
// The node is asserted unmoved, which is what makes this a refusal and not a
// sentence: the write is what the session must not reach.
func TestReconcileRefusesInAnAgentSession(t *testing.T) {
	f := newProjectFixture(t, "")
	reconciledNode(t, f)
	before := reconciledMembers(t, f)
	rewriteProjectConfig(t, f, "pools:\n  - { name: acme-pool, members: [vertex], owner: owner@example.com }\n")
	h := newMigrationRig(t, f, &initOptions{}).managedGate()
	t.Setenv("CLAUDECODE", "1")
	m, _, err := h.s.plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	err = policyMigrationConfirm(context.Background(), h.cmd, m, false, h.out, true, true, false, h.s.e.r.conditions(), inflightQuiet)
	var refused *initflow.AgentError
	if !errors.As(err, &refused) {
		t.Fatalf("a coding agent's session was not refused the grant it queued (err=%v):\n%s", err, h.out)
	}
	if refused.Marker != "CLAUDECODE" {
		t.Errorf("the refusal names the marker %q, want CLAUDECODE", refused.Marker)
	}
	if got := reconciledMembers(t, f); got != before {
		t.Errorf("the refused session moved the node:\n got %s\nwant %s", got, before)
	}
}

// TestUpdatePublishesAnOwnedLocalPoolAskingNothing: a grant is what the owner's
// own update publishes, and it asks nothing — no typed name, no question, not
// even a terminal (D1542, D1552, F24, and the HARD RULE's own reading: `fugaro
// update` is the whole upgrade and a prompt in front of it would be the one the
// policy forbids). The pool is in the file and the node lacks it, so the
// publish adds it; the gate sees the widening (loosening()) and the door waives
// the typed name for it because a grant is the whole of what this publish
// loosens.
//
// stdin is closed and --yes is not passed, so a confirmation of any kind here
// would be a refusal: this is the case that used to end in one.
func TestUpdatePublishesAnOwnedLocalPoolAskingNothing(t *testing.T) {
	noTerminal(t)
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	rewriteProjectConfig(t, f, "pools:\n  - { name: acme-pool, members: [vertex], owner: owner@example.com }\n")
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, false)
	if err != nil {
		t.Fatalf("the update refused an owned local pool: %v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if len(p.CraftPools) != 1 || p.CraftPools[0].Name != "acme-pool" || p.CraftPools[0].Owner != "owner@example.com" {
		t.Errorf("craft_pools = %+v, want the pool the file states with its owner", p.CraftPools)
	}
	if strings.Contains(out, "CONFIRM") {
		t.Errorf("the update asked for a typed name for a grant:\n%s", out)
	}
}

// TestLocalLegacyOpenIsNeverPublished: `legacy_open` is the marker D1942's
// migration writes into the node for a pool that predates owners, and it is
// never the config's (D2608). A pool in the file that carries it is therefore
// not published with the marker — the marker would make the pool served to
// everyone, which is the one thing a file must not be able to say — and the
// line says what happened to the key rather than leaving the operator to
// wonder why the pool they wrote is not in the node.
func TestLocalLegacyOpenIsNeverPublished(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	rewriteProjectConfig(t, f, "pools:\n  - { name: acme-loose, members: [vertex], legacy_open: true }\n")
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("a config holding a pool marked legacy_open failed the update: %v\n%s", err, out)
	}
	if got := publishedPolicy(t, f).CraftPools; len(got) != 0 {
		t.Errorf("craft_pools = %+v, want the marker never published from a file", got)
	}
	if !strings.Contains(out, "legacy_open is the marker the migration writes") {
		t.Errorf("the update does not say what happened to the key:\n%s", out)
	}
}

// TestOwnerlessLocalPoolIsSkippedNotRefused: D2608's answer is one line at both
// doors — an ownerless local pool is saved in the file, is not published, and
// is named, and neither door fails on it. The pool is what `fugaro accounts
// pool add` writes today, so refusing it would refuse a command of this very
// release, and the file is not a thing `fugaro update` may rewrite.
//
// The update door's half is TestUpdateNamesThePoolTheNodeCannotCarry's; this is
// the explicit door's, which is the one a person reaches for when the line
// tells them the pool is not published.
func TestOwnerlessLocalPoolIsSkippedNotRefused(t *testing.T) {
	f := newProjectFixture(t, "")
	reconciledNode(t, f)
	setConfig(t, f, func(s string) string {
		return s + "pools:\n  - { name: acme-loose, members: [vertex] }\n"
	})

	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("the explicit publish failed on an ownerless local pool: %v\n%s", err, out)
	}
	if !strings.Contains(out, "craft_pools acme-loose not published: owner is required") {
		t.Errorf("the publish does not name the pool the node cannot carry:\n%s", out)
	}
	for _, q := range publishedPolicy(t, f).CraftPools {
		if q.Name == "acme-loose" {
			t.Errorf("the ownerless pool was published: %+v", q)
		}
	}
}

// TestPolicyPublishAndUpdateReconcileTheSameNode is the plan's red step: the
// two doors build one node from one config, and the members N4 added are the
// half MC18's TestUpdateStageAndTheExplicitDoorBuildTheSameNode cannot reach —
// a written cascade, a written grade, a rewritten pool and an explicit unset.
// A publish that reconciled these members one way and the update another would
// leave a project whose node depends on which command the operator ran, which
// is the state this release exists to remove (D2607).
func TestPolicyPublishAndUpdateReconcileTheSameNode(t *testing.T) {
	extra := "credential_cascade: { wait_threshold: 90s }\n" +
		"model_grades:\n  strong: { model: claude-opus-5-5 }\n" +
		"credentials:\n  - { name: acme-main, type: openrouter }\n" +
		"pools:\n  - { name: node-pool, members: [acme-main], owner: owner@example.com }\n" +
		"crafts:\n  coder: { model: claude-opus-5-5, access: node-pool }\n" +
		"node_removals:\n  craft_credentials: [node-main]\n"

	stage := newProjectFixture(t, "")
	reconciledNode(t, stage)
	setConfig(t, stage, func(s string) string { return s + extra })
	h := newMigrationRig(t, stage, &initOptions{})
	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("the update stage refused to reconcile the node: %v\n%s", err, out)
	}

	door := newProjectFixture(t, "")
	reconciledNode(t, door)
	setConfig(t, door, func(s string) string { return s + extra })
	if out, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("the explicit door refused to reconcile the node: %v\n%s", err, out)
	}

	byStage := nodeJSON(t, stage, projectpolicy.PathShared())
	byDoor := nodeJSON(t, door, projectpolicy.PathShared())
	if byStage != byDoor {
		t.Errorf("the two doors reconciled one config into two nodes:\nupdate   %s\npublish  %s", byStage, byDoor)
	}
	p := publishedPolicy(t, stage)
	if p.Credentials == nil || p.Credentials.WaitThreshold != "90s" || p.Credentials.Fallback != projectpolicy.FallbackAuto {
		t.Errorf("credentials = %+v, want the file's threshold and the node's fallback", p.Credentials)
	}
	if got := p.ModelGrades.Of(config.GradeStrong); got.Model != "claude-opus-5-5" || got.Access != "node-main" {
		t.Errorf("model_grades.strong = %+v, want the file's model and the node's access", got)
	}
	if len(p.CraftCredentials) != 1 || p.CraftCredentials[0].Name != "acme-main" {
		t.Errorf("craft_credentials = %+v, want the file's credential alone: node-main is the explicit unset", p.CraftCredentials)
	}
}

// TestBuildProjectPolicyRefusesAMergedCascadeTheCascadeCannotUse is the merge's
// own rule: the file and the node are each valid alone — the file at load
// (localcfg.validate) and the node at decode (publishedPolicyOf) — and the
// block the merge hands back is held to the block's own rules, because the
// backoff pair's rule is about the EFFECTIVE pair, an absent member being its
// default (ValidCredentials, D153). The node's `backoff.initial: 2h` beside the
// file's `backoff.cap: 1h` is a schedule that never doubles, and the publish
// must refuse it where the file's key can be named rather than let the node's
// decode refuse it on the next `fugaro update`.
func TestBuildProjectPolicyRefusesAMergedCascadeTheCascadeCannotUse(t *testing.T) {
	lc := &localcfg.Config{
		Name:              "acme",
		Budget:            &localcfg.Budget{Mode: "observe"},
		CredentialCascade: &projectpolicy.Credentials{Backoff: &projectpolicy.Backoff{Cap: "1h"}},
	}
	node := &projectpolicy.Policy{
		Budget:      projectpolicy.Budget{Mode: "observe"},
		Credentials: &projectpolicy.Credentials{Backoff: &projectpolicy.Backoff{Initial: "2h"}},
	}
	if err := projectpolicy.ValidCredentials(lc.CredentialCascade, "credential_cascade"); err != nil {
		t.Fatalf("the fixture's file block is not valid alone, so it is no fixture for the merge: %v", err)
	}
	if err := projectpolicy.ValidCredentials(node.Credentials, "credentials"); err != nil {
		t.Fatalf("the fixture's node block is not valid alone, so it is no fixture for the merge: %v", err)
	}
	pol, _, err := buildProjectPolicy(lc, []string{"github/acme/app"}, node, false, nil)
	if err == nil {
		t.Fatalf("the merged block was built into a policy: %+v", pol.Credentials)
	}
	if !strings.Contains(err.Error(), "credential_cascade.backoff.cap") {
		t.Errorf("the refusal does not name the local key the operator wrote: %v", err)
	}
}
