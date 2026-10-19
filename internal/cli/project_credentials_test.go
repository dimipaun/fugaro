package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

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
// nothing, and an account list that loses its last account leaves no child
// behind — a stale child is an admission the service still reads (C1.11), so a
// publish that left one would be a revoked token still being handed out.
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

// TestPolicyAdmissionsAreWrittenBeforeTheSharedNode: the two writes of a
// publish are not one transaction (the shared node is one compare-and-set, the
// admissions are a multi-path patch), so their order is the security decision.
// The children go first, in one patch, and only then the shared node.
//
// The dangerous direction is a child that admits an account the shared node no
// longer covers: a revocation that silently did not happen, on the node the
// service reads to decide whether a run may be handed somebody's subscription
// token. Writing the children first makes the failure mode of a half-applied
// publish the harmless one — the children are a copy of an admission the shared
// node implied, so a publish that dies after them leaves the installation as
// tight as the config asked for. The other order leaves the loose child behind.
//
// The test drives the failure: the shared node's compare-and-set is refused, and
// the admissions are still there. Mutation (verified, run and restored): swap
// the two writes in runProjectPolicyPublish (publish the shared node first) —
// this test fails, the admissions never written.
func TestPolicyAdmissionsAreWrittenBeforeTheSharedNode(t *testing.T) {
	f := newProjectFixture(t, accountAdmissionsYAML)
	f.db.Deny(projectpolicy.PathShared())
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("the publish succeeded although the shared node's write was refused")
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("the shared node exists although its write was refused")
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(f.db.Value(projectpolicy.PathRepo(slug)))
	if got, want := string(b), `{"credentials":{"accounts":["ops","spare"]}}`; got != want {
		t.Errorf("the admissions written before the shared node are %s, want %s: the children are written first, so a publish that cannot write the shared node still leaves the admissions the config asked for", got, want)
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
