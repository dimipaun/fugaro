package cli

import (
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// accountAdmissionsYAML is an operator's config with two Claude accounts, both
// admitted to acme/app, and one profile that serves them over that repository
// only. acme/other is enrolled in the project's repos: map — it is in the
// allow-list — but no account admits it, which is the case the admissions must
// not invent a child for.
const accountAdmissionsYAML = "claude_accounts:\n" +
	"  ops: { allow_data_to: [acme/app] }\n" +
	"  spare: { allow_data_to: [acme/app] }\n" +
	"profiles:\n" +
	"  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, claude_accounts: [ops, spare], allow_repos: [\"github/acme/app\"] }\n"

// withoutTheAccountsYAML is the same project with the accounts and the profile
// gone: no account admits any repository, so that repository's child has no
// reason to exist any more and the publish that removes the last admission must
// remove it.
const withoutTheAccountsYAML = "profiles: {}\n"

// oneAccountYAML is accountAdmissionsYAML with spare gone: one account admits
// acme/app instead of two. It is what a revocation looks like on this project —
// the same child, narrowed — and it is the config the tightening test publishes
// when the shared node's write fails.
const oneAccountYAML = "claude_accounts:\n" +
	"  ops: { allow_data_to: [acme/app] }\n" +
	"profiles:\n" +
	"  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, claude_accounts: [ops], allow_repos: [\"github/acme/app\"] }\n"

// swappedAccountYAML is the same project with the accounts exchanged: ops is
// moved to acme/other (and named by no profile any more, so it is in no child's
// admission list) and spare serves acme/app. It is the config that makes the
// app's child change hands — one account admits the repository before it, a
// different one after — which is the case the narrowing phase used to report as
// a removal.
const swappedAccountYAML = "claude_accounts:\n" +
	"  ops: { allow_data_to: [acme/other] }\n" +
	"  spare: { allow_data_to: [acme/app] }\n" +
	"profiles:\n" +
	"  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, claude_accounts: [spare], allow_repos: [\"github/acme/app\"] }\n"

// TestPolicyAdmissionsPublishedNamesOnly: O4's publishing half, end to end.
// Each repository some account admits gets a node of its own under
// config/policy/repos/$slug — keyed by the slug the repository's own run token
// carries — and that node holds the NAMES of the accounts admitting it and
// nothing else. A repository no account admits gets no node at all.
//
// Names only is the security claim (O4): the node is readable by the run whose
// slug it is, so a token or a secret name written here is a value a
// prompt-injected agent can take through /proc. The document is asserted
// exactly, as bytes, because that is what "nothing else" means — a member added
// here by a later change is a value published to a run, and this test is where
// that change has to be argued for.
func TestPolicyAdmissionsPublishedNamesOnly(t *testing.T) {
	f := newProjectFixture(t, accountAdmissionsYAML)
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(f.db.Value(projectpolicy.PathRepo(slug)))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"credentials":{"accounts":["ops","spare"]}}`; got != want {
		t.Errorf("the node at %s is %s, want %s (the account NAMES only, sorted, and nothing else)", projectpolicy.PathRepo(slug), got, want)
	}
	rp, found, err := projectpolicy.DecodeRepo(b)
	if err != nil || !found {
		t.Fatalf("the published child does not decode (found=%v): %v", found, err)
	}
	if got := rp.AdmittedAccounts(); len(got) != 2 || got[0] != "ops" || got[1] != "spare" {
		t.Errorf("the published admissions = %v, want [ops spare]", got)
	}
	// The repository no account admits has no child: its absence is the
	// statement ("this repository may use no account's token"), and a child
	// with an empty list would be a spelling DecodeRepo refuses.
	other, err := task.Slug("bitbucket", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	if v := f.db.Value(projectpolicy.PathRepo(other)); v != nil {
		t.Errorf("a repository no account admits got a child: %v", v)
	}
	// The operator reads the admission: the line names the slug and the
	// accounts, so a publish that changed who may use a token is visible in it.
	for _, want := range []string{"admits " + slug, "ops", "spare"} {
		if !strings.Contains(out, want) {
			t.Errorf("the publish output does not contain %q:\n%s", want, out)
		}
	}
	// The child key is a slug: it is what the run's own token carries in its
	// `fs` claim (task.Slug), not a second spelling of the repository's name.
	if strings.Contains(slug, "/") {
		t.Errorf("the child key %q is not a slug", slug)
	}
}

// TestPolicyAdmissionsFollowTheConfig: a publish is the whole of what the
// admissions are, in both directions. Publishing the same config twice changes
// nothing, an account list that loses its last account leaves no child behind —
// a stale child is an admission the service still reads (C1.11), so a publish
// that left one would be a revoked token still being handed out — and a child
// that changes hands admits the account the config now names.
//
// The third case is the review finding on this change: a repository whose child
// admits ops and is then served by spare. The narrowing phase runs first and
// finds nothing to keep — it cannot leave ops admitted while the shared node no
// longer admits it, and a written empty list is a document DecodeRepo refuses —
// so the child goes there and the widening phase writes it again. That is not a
// removal, and the publish used to say it was: the operator read "removed (no
// account admits this repository)" and, on the very next line, "admits <slug>:
// spare". The first line was untrue.
func TestPolicyAdmissionsFollowTheConfig(t *testing.T) {
	f := newProjectFixture(t, accountAdmissionsYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(f.db.Value(projectpolicy.PathRepo(slug)))
	if got, want := string(b), `{"credentials":{"accounts":["ops","spare"]}}`; got != want {
		t.Fatalf("a second publish changed the admissions: %s, want %s", got, want)
	}
	// The config loses the accounts: the child goes.
	rewriteProjectExtra(t, f, accountAdmissionsYAML, withoutTheAccountsYAML)
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	if v := f.db.Value(projectpolicy.PathRepo(slug)); v != nil {
		t.Errorf("the child of a repository no account admits survived the publish: %v", v)
	}
	if !strings.Contains(out, "removed") {
		t.Errorf("the publish does not say the admission was removed:\n%s", out)
	}

	// And the swap: the child admits ops, and the config moves ops elsewhere and
	// admits spare, so the repository changes hands rather than losing its
	// admission.
	f = newProjectFixture(t, oneAccountYAML)
	if out, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	if got, _ := json.Marshal(f.db.Value(projectpolicy.PathRepo(slug))); string(got) != `{"credentials":{"accounts":["ops"]}}` {
		t.Fatalf("the fixture did not publish the admission this case is about: %s", got)
	}
	rewriteProjectExtra(t, f, oneAccountYAML, swappedAccountYAML)
	out, _, err = execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	if got, _ := json.Marshal(f.db.Value(projectpolicy.PathRepo(slug))); string(got) != `{"credentials":{"accounts":["spare"]}}` {
		t.Errorf("after the swap the child is %s, want the account the config now admits", got)
	}
	if strings.Contains(out, "removed") {
		t.Errorf("the publish says the repository's admission was removed and admits one on the next line:\n%s", out)
	}
	// What truly happens, said: the account that no longer admits the repository
	// is named, and the one that does is admitted.
	for _, want := range []string{"ops no longer admits this repository", "admits " + slug + ": spare"} {
		if !strings.Contains(out, want) {
			t.Errorf("the publish does not say %q:\n%s", want, out)
		}
	}
}

// TestPolicyAdmissionsRefuseToRemoveANodeThisFugaroCannotRead: removing is
// publishing too, and this Fugaro removes only what it understands. A child
// that does not decode as this shape is a node whose meaning is not known — it
// may be a later release's — so the publish refuses and names it instead of
// deleting it. The refusal is a refusal: no shared node is written either, so
// the operator meets it before anything changes.
func TestPolicyAdmissionsRefuseToRemoveANodeThisFugaroCannotRead(t *testing.T) {
	f := newProjectFixture(t, "")
	const ghost = "ghost-0123456789abcdef"
	f.db.Set(projectpolicy.PathRepos()+"/"+ghost, map[string]any{"credentials": map[string]any{"accounts": []any{}}})
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("a publish that had to remove a node it cannot read succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), ghost) {
		t.Errorf("the refusal does not name the node: %v", err)
	}
	if f.db.Value(projectpolicy.PathRepos()+"/"+ghost) == nil {
		t.Error("the node was removed anyway")
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("the shared node was written although the publish was refused")
	}
}

// TestPolicyPublishKeepsTheCredentialsBlock: D153 makes every member of
// `credentials` operator-written policy — the cascade's fallback, its
// threshold, its backoff and D142's two controls — so this release's publisher
// has no value to write there and keeps what the node holds (the same treatment
// clone_urls and an unaccounted-for routing block get). The node is the only
// place the block exists, and a publish that dropped it would put a project
// back on oauth-only, silently, with nobody having written that.
//
// The block reaches the node through one route, and what this test holds is
// that route plus the shape of what it carries:
//
//   - drop `pol.Credentials = old.Credentials` in buildProjectPolicy: the node
//     loses the block altogether (nothing in this release sets it in the built
//     policy), so does the "credentials kept:" line, and the project is back on
//     the defaults it never chose — silently onto oauth-only for a project that
//     had turned the cascade's fallback on.
//   - the members are asserted as bytes and as decoded values, which is what
//     makes a *written* false or 0 (D142's opt-outs, the two values a
//     value-typed re-encode would fold onto their defaults) part of what this
//     test holds: the publish must write the operator's block, not the
//     defaults, for every member.
//
// (The first mutation verified, run and restored.)
func TestPolicyPublishKeepsTheCredentialsBlock(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": projectpolicy.Version,
		"budget":  map[string]any{"mode": "enforce", "per_run_usd": 5},
		"credentials": map[string]any{
			"fallback":                         "auto",
			"wait_threshold":                   "90s",
			"backoff":                          map[string]any{"initial": "1m", "cap": "2h"},
			"stop_after_exhaustion_per_window": false,
			"concurrency_per_token":            3,
		},
	})
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	raw := f.db.Value(projectpolicy.PathShared())
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	// The block is on the node and it is the operator's block, member by member:
	// the durations as the strings they wrote rather than re-spelt, the controls
	// as the booleans and numbers they wrote. A count as well as the members, so
	// a member added to the block by this binary would show up here.
	var doc struct {
		Credentials map[string]json.RawMessage `json:"credentials"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Credentials) != 5 {
		t.Fatalf("the published credentials block is %s, want the operator's five members", b)
	}
	for member, want := range map[string]string{
		"fallback":                         `"auto"`,
		"wait_threshold":                   `"90s"`,
		"backoff":                          `{"cap":"2h","initial":"1m"}`,
		"stop_after_exhaustion_per_window": `false`,
		"concurrency_per_token":            `3`,
	} {
		if got := string(doc.Credentials[member]); got != want {
			t.Errorf("the published credentials.%s = %s, want %s (the operator's value, kept)", member, got, want)
		}
	}
	// And it still reads as the policy the operator wrote.
	p, found, err := projectpolicy.Decode(b)
	if err != nil || !found {
		t.Fatalf("the published node does not decode (found=%v): %v", found, err)
	}
	c := p.Credentials
	if c == nil {
		t.Fatal("the published node carries no credentials block: the publish dropped it")
	}
	if c.FallbackValue() != projectpolicy.FallbackAuto || c.WaitThresholdDuration() != 90*time.Second ||
		c.ConcurrencyCap() != 3 || c.StopsAfterExhaustion() {
		t.Errorf("the published block reads as %q, want the operator's auto/90s/3/stop", credentialsText(c))
	}
	// The operator is told it was kept: that line is the only place a publish
	// says what it did with the block.
	if !strings.Contains(out, "credentials kept: fallback auto") {
		t.Errorf("the publish does not say the credentials block was kept:\n%s", out)
	}
	// A null member is an absent one to Decode (the two are the same policy), so
	// a publish writes the node without the key rather than keeping a null —
	// the same treatment a null routing member gets, and it means a published
	// null is still publishable over.
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"credentials": nil,
	})
	out, _, err = execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("a published null credentials member was unpublishable: %v\n%s", err, out)
	}
	if node, ok := f.db.Value(projectpolicy.PathShared()).(map[string]any); ok && node["credentials"] != nil {
		t.Errorf("a null credentials member survived the publish: %v", node["credentials"])
	}
}

// TestPolicyPublishRefusesACredentialsBlockItCannotRead: the other side of the
// carry-over, and the reason it cannot be a tolerant read. `credentials` is
// strict inside (the package doc's "strict in the other direction"), so a block
// this Fugaro cannot decode — a member a later release adds, a misspelt one, a
// value out of range — makes the publish refuse, naming the member, before
// anything is written. The operator is sent to fix the node by hand.
//
// This is worth its own test because the alternative is the tempting one: a
// publish that read the block tolerantly and wrote back what it understood
// would drop a setting it does not model — on the block that decides whether a
// project spends money and whether its cascade is on. Here the same block fails
// loudly instead, and neither the shared node nor an admission is written.
func TestPolicyPublishRefusesACredentialsBlockItCannotRead(t *testing.T) {
	for _, tc := range []struct{ name, key, block string }{
		{"a member a later release might add", "spend_threshold", `{"spend_threshold": "1h"}`},
		{"a misspelt member", "wait_thresold", `{"wait_thresold": "1h"}`},
		{"a value out of range", "concurrency_per_token", `{"concurrency_per_token": -1}`},
		{"a value this Fugaro has no meaning for", "fallback", `{"fallback": "maybe"}`},
	} {
		f := newProjectFixture(t, accountAdmissionsYAML)
		f.db.Set(projectpolicy.PathShared(), map[string]any{
			"version":     projectpolicy.Version,
			"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
			"credentials": json.RawMessage(tc.block),
		})
		out, _, err := execute(t, "project", "policy", "publish")
		if err == nil {
			t.Fatalf("%s: the publish succeeded:\n%s", tc.name, out)
		}
		if !strings.Contains(err.Error(), tc.key) {
			t.Errorf("%s: the refusal is %q, want it to name %s", tc.name, err.Error(), tc.key)
		}
		// The refusal is a refusal: the admissions of this publish were not
		// written either, so a publish the operator has to fix leaves the
		// installation as it was.
		if v := f.db.Value(projectpolicy.PathRepos()); v != nil {
			t.Errorf("%s: the refused publish wrote admissions anyway: %v", tc.name, v)
		}
	}
}

// TestPolicyPublishRefusesToNarrowANodeItCannotRead: the tightening phase reads
// a child's accounts to compute what to take away, so a child it cannot decode
// is a child whose narrowing it cannot compute — and the tempting alternative,
// writing the config's names over it, is a publish that changed an admission it
// could not read (and, in the widening direction, one that would have written
// the loose value first). The publish refuses and names the node, and writes
// nothing at all: neither the child nor the shared node.
//
// Mutation (verified, run and restored): let the tightening phase write the
// desired names over a child that does not decode (skip the refusal) — this
// test fails, the publish succeeds and the node it could not read is gone.
func TestPolicyPublishRefusesToNarrowANodeItCannotRead(t *testing.T) {
	f := newProjectFixture(t, accountAdmissionsYAML)
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	// A child at the slug the config admits, holding a document DecodeRepo
	// refuses: an admission whose accounts are written as an empty list.
	ghost := map[string]any{"credentials": map[string]any{"accounts": []any{}}}
	f.db.Set(projectpolicy.PathRepo(slug), ghost)
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("a publish that had to narrow a node it cannot read succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), slug) {
		t.Errorf("the refusal does not name the node: %v", err)
	}
	b, _ := json.Marshal(f.db.Value(projectpolicy.PathRepo(slug)))
	if got, want := string(b), `{"credentials":{"accounts":[]}}`; got != want {
		t.Errorf("the node this Fugaro cannot read was written over: %s, want %s", got, want)
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("the shared node was written although the publish was refused")
	}
}

// TestPolicyPublishWarnsBeforeTheMembersOlderReadersRefuse: the plan's C1.2
// skew section ends by handing this decision to the review of the task ("The
// review of this task decides whether `fugaro project policy publish` warns
// before publishing either"), and the review asked for the line in the diff.
// The two members are the ones an older Fugaro does not refuse *around* but
// refuses *whole*:
//
//   - `credentials`: an older CLI does not know the key, so it refuses every
//     launch and every publish of the project;
//   - `routing.claude.via: cascade`: the older shape check refuses the node for
//     a value it does not know, so an older CLI refuses launch and publish and
//     an older runner halts the run with that routing error.
//
// So a publish that puts either on the node is a publish that stops the
// installation until it is upgraded, and the diff — the one thing the operator
// reads — has to say so. The line lives in the diff, which this command prints
// after the write (the review finding that made it a claim about the node as it
// now is): the operator reads this warning about what they have just published,
// not about what they are about to.
//
// Mutation (verified, run and restored): drop the skewWarning loop in
// printPolicyDiff — the two cases fail; and print the lines unconditionally —
// the third case fails.
//
// The lines are worded about the *next launch*, not "before this publish"
// (security review m-5): they are printed after the write, so advice about the
// publish itself is advice about a moment that has already passed. The
// "before the next launch" assertion in each of the two cases pins the wording
// (mutation: put "before this publish" back — both fail).
func TestPolicyPublishWarnsBeforeTheMembersOlderReadersRefuse(t *testing.T) {
	// The operator's own config writes the cascade switch (D150, O3), so this
	// is the route by which a cascade reaches the node in this release.
	f := newProjectFixture(t, "routing:\n  claude:\n    via: cascade\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	for _, want := range []string{"warning", "routing.claude.via: cascade", "upgrade the CLI", "runner image", "before the next launch"} {
		if !strings.Contains(out, want) {
			t.Errorf("the publish of a cascade switch does not warn with %q:\n%s", want, out)
		}
	}
	// The credentials member reaches the node by the carry-over (nothing in
	// this release writes one), and it is the narrower warning: the CLI, not
	// the runner image.
	f = newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"credentials": map[string]any{"fallback": "oauth-only"},
	})
	out, _, err = execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	for _, want := range []string{"warning", "credentials member", "upgrade the CLI", "before the next launch"} {
		if !strings.Contains(out, want) {
			t.Errorf("the publish of a credentials member does not warn with %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "runner image") {
		t.Errorf("the credentials member's warning claims a runner requirement it does not have (the runner ignores the key):\n%s", out)
	}
	// Neither member: no warning at all. A warning printed beside every publish
	// is a warning an operator stops reading.
	f = newProjectFixture(t, "")
	out, _, err = execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	if strings.Contains(out, "warning") {
		t.Errorf("a publish of neither member warns anyway:\n%s", out)
	}
}

// TestPolicyAdmissionsAreTightenedBeforeTheSharedNode: the two writes of a
// publish are not one transaction (the shared node is one compare-and-set, the
// admissions are a multi-path patch), so their order is the security decision —
// and the decision is direction-aware. Taking an admission away is written
// *before* the shared node, so a publish that dies between the two has still
// revoked it: a child the config no longer admits, left admitting an account,
// is a revocation that silently did not happen, on the node the service reads
// to decide whether a run may be handed somebody's subscription token (C1.11).
//
// The test drives the failure: a project whose child admits ops and spare, a
// config that has dropped spare, and a shared node whose compare-and-set is
// refused. The child must already have lost spare, and the failure must say so
// — the operator's next publish will not (the child already reads as narrowed),
// so this message is the only place it is said.
//
// Mutations (verified, run and restored): swap the phases (widen before the
// shared node) — the child keeps spare and this test fails; drop the narrowed
// note from the failure message — the message assertion fails.
func TestPolicyAdmissionsAreTightenedBeforeTheSharedNode(t *testing.T) {
	f := newProjectFixture(t, accountAdmissionsYAML)
	if out, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("the first publish: %v\n%s", err, out)
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	// The config drops spare: only ops admits acme/app any more.
	rewriteProjectExtra(t, f, accountAdmissionsYAML, oneAccountYAML)
	f.db.Deny(projectpolicy.PathShared())
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("the publish succeeded although the shared node's write was refused:\n%s", out)
	}
	b, _ := json.Marshal(f.db.Value(projectpolicy.PathRepo(slug)))
	if got, want := string(b), `{"credentials":{"accounts":["ops"]}}`; got != want {
		t.Errorf("after a publish that could not write the shared node, %s is %s, want %s: the children are narrowed first, so the revocation of spare landed even though the config's own node did not", projectpolicy.PathRepo(slug), got, want)
	}
	// And the omission is real: the shared node still carries the accounts of
	// the publish that succeeded, spare among them.
	if node, ok := f.db.Value(projectpolicy.PathShared()).(map[string]any); !ok {
		t.Fatal("the shared node of the first publish is gone")
	} else if accounts, _ := node["accounts"].(map[string]any); accounts["spare"] == nil {
		t.Errorf("the shared node no longer admits spare, so this test does not drive the state it is about: %v", node["accounts"])
	}
	// The failure says what landed: the narrowed child, by name.
	for _, want := range []string{"narrowed", slug, "spare"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not name what it narrowed (%q missing): %v", want, err)
		}
	}
}

// policyRetryThenBreak is policyConflictOnce one attempt further on: the other
// operator's publish lands on the shared node just before this publish's first
// write — which is the tightening patch, since that phase runs before the
// shared node's compare-and-set — so that compare-and-set is refused and the
// loop reads the node again. breakRetry runs once, inside the RoundTrip of the
// second attempt's first request, so what it breaks is the attempt the loop's
// own retry began, after the first attempt's narrowing has already landed.
//
// That is what makes the second attempt's failure the only place the narrowing
// is reported: the loop drops the lines the first attempt returned, and the
// next publish finds the child already narrowed and says nothing about it.
type policyRetryThenBreak struct {
	rt         http.RoundTripper
	db         *gcpfake.RTDB
	val        any    // what the other publish left on the shared node
	breakRetry func() // what breaks the second attempt, run once
	wrote      bool
	broke      bool
}

func (c *policyRetryThenBreak) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		if !c.wrote {
			c.wrote = true
			c.db.Set(projectpolicy.PathShared(), c.val)
		}
		return c.rt.RoundTrip(r)
	}
	if c.wrote && !c.broke {
		c.broke = true
		c.breakRetry()
	}
	return c.rt.RoundTrip(r)
}

// otherPolicyNode is what the other operator's publish left on the shared node:
// this fixture's node with a different per-run ceiling, so the compare-and-set
// this publish makes against the node it read is refused.
func otherPolicyNode(t *testing.T, f *projectFixture) map[string]any {
	t.Helper()
	node, ok := f.db.Value(projectpolicy.PathShared()).(map[string]any)
	if !ok {
		t.Fatalf("no policy node at %s to copy", projectpolicy.PathShared())
	}
	other := maps.Clone(node)
	other["budget"] = map[string]any{"mode": "observe", "per_run_usd": 9}
	return other
}

// publishNarrowedOnARetry drives the publish this file's three retry tests are
// about and hands back its failure. The project's acme/app child admits ops and
// spare, the config then drops spare, and the shared node is taken by another
// publish just before this one's tightening patch reaches it: the first attempt
// narrows the child to ops and lands that write, then loses the
// compare-and-set; breakRetry then breaks the second attempt in whatever way
// the caller names.
//
// It asserts the two halves every one of those tests rests on — the narrowing
// landed, and the failure reports it — because the note is the only place it is
// reported (narrowedNote's own reason: the next publish finds the child already
// narrowed). The phrase asserted is the note's, not the word "narrowed": the
// refusal to run over an unreadable child carries that word on its own, and an
// assertion that a message merely says "narrowed" would pass with no note at
// all.
func publishNarrowedOnARetry(t *testing.T, breakRetry func(f *projectFixture)) (*projectFixture, error) {
	t.Helper()
	f := newProjectFixture(t, accountAdmissionsYAML)
	if out, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("the first publish: %v\n%s", err, out)
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	// The config drops spare: only ops admits acme/app any more, so the next
	// publish's first attempt takes spare away from the child and writes it.
	rewriteProjectExtra(t, f, accountAdmissionsYAML, oneAccountYAML)
	c := &policyRetryThenBreak{rt: http.DefaultTransport, db: f.db, val: otherPolicyNode(t, f)}
	c.breakRetry = func() { breakRetry(f) }
	budgetTransport = c
	t.Cleanup(func() {
		budgetTransport = nil
		f.db.DenyRead("")
	})
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("the publish succeeded although its second attempt was broken:\n%s", out)
	}
	if !c.broke {
		t.Fatal("the publish never retried: the first attempt has to narrow the child and then lose the compare-and-set")
	}
	b, _ := json.Marshal(f.db.Value(projectpolicy.PathRepo(slug)))
	if got, want := string(b), `{"credentials":{"accounts":["ops"]}}`; got != want {
		t.Errorf("after the failed publish %s is %s, want %s: the first attempt's narrowing landed and nothing undid it", projectpolicy.PathRepo(slug), got, want)
	}
	for _, want := range []string{"a narrowing is not undone by this failure", slug, "spare"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not name what already landed (%q missing): %v", want, err)
		}
	}
	return f, err
}

// TestPolicyPublishSaysWhatLandedWhenTheRetryCannotReadTheNode: the second
// attempt cannot read the shared node at all, so it writes nothing — and the
// narrowing the attempt before it landed is still there, which this message is
// the only place to say.
//
// Mutation (verified, run and restored): drop the note from that return
// (project_cmd.go, readPolicyNode's failure) — the message assertion fails.
func TestPolicyPublishSaysWhatLandedWhenTheRetryCannotReadTheNode(t *testing.T) {
	publishNarrowedOnARetry(t, func(f *projectFixture) {
		f.db.DenyRead(projectpolicy.PathShared())
	})
}

// TestPolicyPublishSaysWhatLandedWhenTheRetryCannotReadTheAdmissions: the
// second attempt reads the shared node and then cannot read the admissions
// children. Nothing of this attempt was written either, and the revocation the
// first attempt landed has to be named — otherwise the one write this command
// must never lose track of is reported by nothing at all.
//
// Mutation (verified, run and restored): drop the note from that return
// (project_cmd.go, the projectpolicy.PathRepos read) — the message assertion
// fails.
func TestPolicyPublishSaysWhatLandedWhenTheRetryCannotReadTheAdmissions(t *testing.T) {
	publishNarrowedOnARetry(t, func(f *projectFixture) {
		f.db.DenyRead(projectpolicy.PathRepos())
	})
}

// TestPolicyPublishSaysWhatLandedWhenTheRetryCannotNarrow: the second attempt
// gets as far as the tightening phase and refuses to run over a child it cannot
// read — a node somebody wrote by hand between the two attempts, at acme/other's
// slug, which is not the child this test's narrowing is about. The refusal names
// the node it will not touch, and it has to name the narrowing of the attempt
// before it as well.
//
// Mutation (verified, run and restored): drop the note from that return
// (project_cmd.go, tightenAdmissions' failure) — the message assertion fails.
func TestPolicyPublishSaysWhatLandedWhenTheRetryCannotNarrow(t *testing.T) {
	publishNarrowedOnARetry(t, func(f *projectFixture) {
		otherSlug, err := task.Slug("bitbucket", "acme/other")
		if err != nil {
			t.Fatal(err)
		}
		// DecodeRepo refuses a child whose credentials block admits nothing.
		f.db.Set(projectpolicy.PathRepo(otherSlug), map[string]any{"credentials": map[string]any{"accounts": []any{}}})
	})
}

// TestPolicyAdmissionsAreNotWidenedBeforeTheSharedNode is the other direction,
// and the review finding that made the order direction-aware: a publish that
// *adds* an admission must not leave the child ahead of the shared node, or a
// failure between the two writes leaves a repository admitted to an account the
// node does not cover. Nothing is written before the node's own write, so the
// installation after this failure is exactly the one before it.
//
// Mutation (verified, run and restored): write every child first (the order
// this test was written against) — the child appears although the shared node
// was refused, and this test fails.
func TestPolicyAdmissionsAreNotWidenedBeforeTheSharedNode(t *testing.T) {
	f := newProjectFixture(t, accountAdmissionsYAML)
	f.db.Deny(projectpolicy.PathShared())
	_, _, perr := execute(t, "project", "policy", "publish")
	if perr == nil {
		t.Fatal("the publish succeeded although the shared node's write was refused")
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("the shared node exists although its write was refused")
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if v := f.db.Value(projectpolicy.PathRepo(slug)); v != nil {
		t.Errorf("a publish that could not write the shared node admitted an account anyway: %s holds %v. An addition has to follow the node it copies, because a child ahead of its source admits an account the shared node does not cover", projectpolicy.PathRepo(slug), v)
	}
	// And the failure does not claim more than it did: nothing was narrowed
	// either, so it says the shared node is as it was and no more.
	if strings.Contains(perr.Error(), "narrowed") {
		t.Errorf("the failure claims a narrowing that did not happen: %v", perr)
	}
}

// TestPolicyPublishSaysWhatLandedWhenTheAdmissionsCannotBeWritten: the mirror
// of the tightening case — the shared node's write succeeds, so the publish
// cannot be undone, and the widening patch is then refused. The command has to
// fail (the config is not what is published), and it has to say which half it
// got: the node was set, the admissions were not, and the repositories that
// lost an account are still admitted to the others.
//
// Mutation (verified, run and restored): swallow the widening error — the
// publish reports success with admissions that are missing, and this test
// fails.
func TestPolicyPublishSaysWhatLandedWhenTheAdmissionsCannotBeWritten(t *testing.T) {
	f := newProjectFixture(t, accountAdmissionsYAML)
	f.db.Deny(projectpolicy.PathRepos())
	_, _, perr := execute(t, "project", "policy", "publish")
	if perr == nil {
		t.Fatal("the publish succeeded although its admissions could not be written")
	}
	if f.db.Value(projectpolicy.PathShared()) == nil {
		t.Error("the shared node was not written, so this test does not drive the state it is about")
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if v := f.db.Value(projectpolicy.PathRepo(slug)); v != nil {
		t.Errorf("the admissions were written although their patch was refused: %v", v)
	}
	if msg := perr.Error(); !strings.Contains(msg, "was set") || !strings.Contains(msg, projectpolicy.PathRepos()) {
		t.Errorf("the failure does not say that the shared node was set and the admissions were not: %v", perr)
	}
}

// TestPolicyShowPrintsTheCredentialsBlock: the block has no writer in this
// release (D153 — the values are the operator's, per project), so `policy show`
// is the only place an operator reads them back; a block the node carries and
// the command does not print is a setting nobody can audit, on the one node
// that decides whether the project spends money and whose subscription quota it
// spends. An absent member prints as no line (the node does not write it), and
// a node with no block prints no line at all — printing one would read as a
// policy that has a block.
//
// Mutation (verified, run and restored): drop the `if p.Credentials != nil`
// print in printPolicy — this test fails, and the third case (a node with no
// block) is what the guard is for.
func TestPolicyShowPrintsTheCredentialsBlock(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": projectpolicy.Version,
		"budget":  map[string]any{"mode": "enforce", "per_run_usd": 5},
		"credentials": map[string]any{
			"fallback":              "auto",
			"wait_threshold":        "90s",
			"concurrency_per_token": 3,
		},
	})
	out, _, err := execute(t, "project", "policy", "show")
	if err != nil {
		t.Fatalf("policy show: %v\n%s", err, out)
	}
	for _, want := range []string{"credentials:", "fallback auto", "wait_threshold 90s", "concurrency_per_token 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("policy show does not print %q:\n%s", want, out)
		}
	}
	// A member the node does not write is not printed as one: the line is the
	// node's members, not the defaults a reader would resolve.
	if strings.Contains(out, "backoff.initial") {
		t.Errorf("policy show invents a member the node does not carry:\n%s", out)
	}
	// And a node with no credentials block prints no credentials line.
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": projectpolicy.Version,
		"budget":  map[string]any{"mode": "enforce", "per_run_usd": 5},
	})
	out, _, err = execute(t, "project", "policy", "show")
	if err != nil {
		t.Fatalf("policy show: %v\n%s", err, out)
	}
	if strings.Contains(out, "credentials:") {
		t.Errorf("policy show prints a credentials block the node does not carry:\n%s", out)
	}
}

// rewriteProjectExtra replaces the extra YAML a fixture was built with, so a
// test can publish twice from one project with a different config — the
// publisher reads the file on every run, and a second publish is a second
// command against the same installation.
func rewriteProjectExtra(t *testing.T, f *projectFixture, old, new string) {
	t.Helper()
	b, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)
	if !strings.Contains(cfg, old) {
		t.Fatalf("the fixture's config does not hold %q: %s", old, cfg)
	}
	if err := os.WriteFile(f.path, []byte(strings.Replace(cfg, old, new, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAdmissionsAreReadBackBeforeTheyAreWritten: a child node is put through the
// reader the launcher uses (projectpolicy.DecodeRepo) before this publish
// patches it — checkPolicyBytes, for the children (security review m-6). The
// bytes that are written are the bytes the reader was asked about, so the
// document written and the document read back cannot be two spellings of one
// admission, and a child this Fugaro would refuse is a refusal with nothing
// written rather than a node the *next* publish stops on: DecodeRepo is strict,
// and tightenAdmissions refuses to write over a child it cannot read.
//
// No config reaches the refusal today — the account names are
// config.ValidClaudeAccountRef's own (DeriveAccounts checks each one), they are
// the accounts block's map keys, and repoAdmissions sorts them and never repeats
// one — which is why this test calls the guard directly instead of through a
// fixture: it is the second door on a rule the first door already holds, and the
// door has to work. Its two ways in are a rule the writer and the reader could
// come to disagree about, and a future caller of the two phases.
//
// Mutation (verified, run and restored): return the marshalled document without
// decoding it (the state this review found) — every case below fails.
func TestAdmissionsAreReadBackBeforeTheyAreWritten(t *testing.T) {
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}

	// A child of valid names: the bytes, and the reader's answer to them.
	raw, err := admissionBytes(slug, []string{"ops", "spare"})
	if err != nil {
		t.Fatalf("a child naming two valid accounts was refused: %v", err)
	}
	decoded, found, err := projectpolicy.DecodeRepo(raw)
	if err != nil || !found || decoded == nil {
		t.Fatalf("the bytes this publish would write are not a node this Fugaro reads back (%v): %s", err, raw)
	}
	if got := decoded.AdmittedAccounts(); !slices.Equal(got, []string{"ops", "spare"}) {
		t.Errorf("the child reads back as %v, want the accounts it was built from", got)
	}

	// A name the reader refuses (an account name is lower-case): refused here,
	// where the slug says which repository it was for.
	_, err = admissionBytes(slug, []string{"Ops"})
	if err == nil {
		t.Fatal("a child naming an account the reader refuses was allowed to be written")
	}
	for _, want := range []string{slug, "not a node this Fugaro can read back", "fugaro validate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}

	// An empty admission is the state DecodeRepo refuses so that a hand-written
	// node cannot take tier A away from a repository by accident: the writer
	// does not produce one either.
	if _, err := admissionBytes(slug, nil); err == nil {
		t.Error("a child admitting no account was allowed to be written")
	}

	// The phase's patch is checked whole, before any of it is written: one
	// unreadable child beside a good one is no patch at all.
	other, err := task.Slug("github", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admissionPatch(map[string][]string{slug: {"ops"}, other: {"Ops"}}); err == nil {
		t.Error("a patch carrying one unreadable child was allowed")
	}
	// And the bytes a patch carries are the bytes admissionBytes checked, so
	// what is written and what was read back cannot drift apart.
	one, err := admissionBytes(slug, []string{"ops"})
	if err != nil {
		t.Fatal(err)
	}
	patch, err := admissionPatch(map[string][]string{slug: {"ops"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(patch[slug]) != string(one) {
		t.Errorf("the patched child is %s, want the bytes that were read back (%s)", patch[slug], one)
	}
}

// TestPolicyPublishSaysHowACredentialsBlockItCarriesOverIsRemoved: the one
// refused state this release has no CLI way out of, and the sentence has to say
// so (security review m-1). The node carries credentials.fallback: auto, which
// D152 allows only while the policy's budget enforces a per-run ceiling; the
// operator's config changes the budget to observe, and the publish — which
// carries the node's block over, nothing here writing one — is refused. The
// refusal sends the operator to 'fugaro validate' for every other cause, and
// that cannot remove a member the config does not hold: the ways out are the
// node itself and a fresh policy, and the refusal names both.
func TestPolicyPublishSaysHowACredentialsBlockItCarriesOverIsRemoved(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"credentials": map[string]any{"fallback": "auto"},
	})
	before := nodeJSON(t, f, projectpolicy.PathShared())

	rewriteProjectExtra(t, f, "mode: enforce", "mode: observe")
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("the publish wrote a policy whose fallback: auto has no enforcing ceiling:\n%s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
	for _, want := range []string{"credentials", "fallback", "fugaro validate", "by hand", "--delete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so the operator has no way out of it: %v", want, err)
		}
	}
	if now := nodeJSON(t, f, projectpolicy.PathShared()); now != before {
		t.Errorf("the refused publish changed the node:\n%s", now)
	}
}
