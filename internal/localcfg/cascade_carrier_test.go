package localcfg

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestCascadeCarrierValidatesAtLoad: the cascade block is carried under this
// file's own key (D2607, F23) and held at load to the rule the node's own
// decode applies to its member (projectpolicy.ValidCredentials), so a value the
// node would refuse is refused where the operator wrote it rather than at the
// publish that carries it there. The misspelt `fallback` is the case that
// matters: `oauth-only` and `auto` are two answers a reader acts on, and a typo
// between them leaves a file its author reads as `auto` and the cascade reading
// as oauth-only.
//
// What the load does NOT ask is D152's rule about `auto` and the budget
// together — the ceiling must be enforcing with a per-run ceiling above zero —
// because that is a property of the cascade and the budget block, and a carrier
// that holds one half of the pair does not answer for the other
// (projectpolicy's validCredentials asks it on the node, where both are in
// hand). A file may therefore state `fallback: auto` and be read; the published
// node is where the pair is judged.
func TestCascadeCarrierValidatesAtLoad(t *testing.T) {
	c, err := Parse([]byte(sample + `credential_cascade:
  fallback: auto
  wait_threshold: 90s
  backoff: {initial: 1m, cap: 2h}
  stop_after_exhaustion_per_window: false
  concurrency_per_token: 4
`))
	if err != nil {
		t.Fatalf("a block the rule accepts was refused: %v", err)
	}
	blk := c.CredentialCascade
	if blk == nil {
		t.Fatal("the file states a cascade and the config carries none")
	}
	// Read through the accessors a consumer reads, so this test cannot agree
	// with a decode that put the values somewhere no reader looks.
	if got := blk.FallbackValue(); got != projectpolicy.FallbackAuto {
		t.Errorf("fallback = %q, want auto", got)
	}
	if got, want := blk.WaitThresholdDuration(), 90*time.Second; got != want {
		t.Errorf("wait_threshold = %s, want %s", got, want)
	}
	if got, want := blk.BackoffInitialDuration(), time.Minute; got != want {
		t.Errorf("backoff.initial = %s, want %s", got, want)
	}
	if got, want := blk.BackoffCapDuration(), 2*time.Hour; got != want {
		t.Errorf("backoff.cap = %s, want %s", got, want)
	}
	// The three members that mean something different when absent are read as
	// the operator wrote them and not as their defaults: the record is what a
	// publisher (N4) tells "the owner wrote this" from "the owner wrote
	// nothing" by, and a decode that flattened the written false or the
	// written 4 into the absent key would make the file say the opposite.
	if blk.StopAfterExhaustionPerWindow == nil || *blk.StopAfterExhaustionPerWindow {
		t.Error("the written stop_after_exhaustion_per_window: false did not survive the decode, so a publish would carry the protective default the operator opted out of")
	}
	if got := blk.ConcurrencyCap(); got != 4 {
		t.Errorf("concurrency_per_token = %d, want the written 4", got)
	}
	// An empty block is a block: it states no member and every member's own
	// default stands. It is not the absent key, which states no block at all —
	// telling the two apart is what the publisher does (N4).
	if empty, err := Parse([]byte(sample + "credential_cascade: {}\n")); err != nil || empty.CredentialCascade == nil {
		t.Errorf("a written empty block = %+v, %v; want a block that states nothing", empty.CredentialCascade, err)
	}
	if silent, err := Parse([]byte(sample)); err != nil || silent.CredentialCascade != nil {
		t.Errorf("a file that says nothing about the cascade = %+v, %v; want no block", silent.CredentialCascade, err)
	}

	// Each refusal names the key the operator wrote, not the node's member:
	// `credentials.fallback` in a sentence about a file whose key is
	// `credential_cascade` would send them to a key that means something else
	// there (F23).
	for name, block := range map[string]string{
		"a misspelt fallback":                 "fallback: oauth_only",
		"a fallback in another case":          "fallback: Auto",
		"a threshold with no unit":            "wait_threshold: 15",
		"a negative threshold":                "wait_threshold: -5m",
		"a cap below the initial wait":        "backoff: {cap: 1m}",
		"a backoff member that is not a wait": "backoff: {initial: soon}",
		"a negative concurrency":              "concurrency_per_token: -1",
	} {
		_, err := Parse([]byte(sample + "credential_cascade: {" + block + "}\n"))
		if err == nil {
			t.Errorf("%s: the file loaded, and a cascade the node would refuse must not be readable here", name)
			continue
		}
		if !strings.Contains(err.Error(), "credential_cascade") {
			t.Errorf("%s: the refusal does not name the key the operator wrote: %v", name, err)
		}
	}
	// The node's model grades are carried here too, and held at load to the
	// rule the node's own decode applies (config.ValidateModelGrades): a grade
	// is a model and the credential or pool its calls go out on, so a grade
	// written with no model — the state the rung that needs it refuses and asks
	// about (D2039) — is refused where the operator wrote it.
	grades, err := Parse([]byte(sample + "model_grades:\n  strong: {model: anthropic/claude-sonnet-5.5, access: acme-main}\n"))
	if err != nil {
		t.Fatalf("a grade the rule accepts was refused: %v", err)
	}
	if g := grades.ModelGrades; g == nil || !g.Set(config.GradeStrong) || g.Of(config.GradeStrong).Model != "anthropic/claude-sonnet-5.5" {
		t.Errorf("model_grades = %+v, want the strong grade the file states", g)
	}
	for name, doc := range map[string]string{
		"a grade with no model":              "model_grades:\n  strong: {access: acme-main}\n",
		"a grade whose model is not an ID":   "model_grades:\n  strongest: {model: \"claude opus\"}\n",
		"a grade whose access is not a name": "model_grades:\n  strong: {model: anthropic/claude-sonnet-5.5, access: \"Not A Name\"}\n",
		"a grade this Fugaro does not have":  "model_grades:\n  stronger: {model: anthropic/claude-opus-5.5}\n",
	} {
		if _, err := Parse([]byte(sample + doc)); err == nil {
			t.Errorf("%s: the file loaded, and a grade the node would refuse must not be readable here", name)
		}
	}

	// A member this Fugaro does not model is refused by the strict decode,
	// which names the member and the block's type rather than the file's key;
	// the check here is that it is refused at all, because a misspelt member
	// silently dropped is a setting the operator believes is in force.
	for name, doc := range map[string]string{
		"a member this Fugaro does not model": "credential_cascade: {wait: 1m}\n",
		"a member of the node's block":        "credential_cascade: {budget: {mode: enforce}}\n",
	} {
		if _, err := Parse([]byte(sample + doc)); err == nil {
			t.Errorf("%s: the file loaded, and a key no reader looks at must not read as a setting", name)
		}
	}
}

// TestCredentialsKeyStillMeansTheTypedList: this release adds the cascade to the
// local config and it must not take the `credentials` key to do it (F23). Here
// the key is the list of typed credentials (docs/model-crafts/spec.md §1) and
// stays exactly that: one key, one meaning, the cascade under a name of its own
// (`credential_cascade`). The node is the other way round — its `credentials`
// member IS the cascade — so the two documents spell the same two things with
// one key between them, and the refusals below are the half that keeps a reader
// from taking one for the other.
func TestCredentialsKeyStillMeansTheTypedList(t *testing.T) {
	c, err := Parse([]byte(sample + "credentials:\n  - {name: acme-main, type: openrouter}\n"))
	if err != nil {
		t.Fatalf("a typed credential was refused: %v", err)
	}
	if len(c.Credentials) != 1 || c.Credentials[0].Name != "acme-main" || c.Credentials[0].Type != config.CredentialTypeOpenRouter {
		t.Errorf("credentials = %+v, want the one typed credential the file lists", c.Credentials)
	}
	if c.CredentialCascade != nil {
		t.Errorf("credentials: produced a cascade (%+v): the two blocks must be two keys here, and the cascade's own name is what keeps them apart", c.CredentialCascade)
	}
	// And the other direction: the cascade's members are not credential fields,
	// whatever the node's key for the same members is.
	for name, doc := range map[string]string{
		"the cascade's members under credentials": "credentials:\n  fallback: auto\n",
		"a cascade member inside a credential":    "credentials:\n  - {name: acme-main, type: openrouter, fallback: auto}\n",
		"a typed credential under the cascade":    "credential_cascade:\n  - {name: acme-main, type: openrouter}\n",
	} {
		if _, err := Parse([]byte(sample + doc)); err == nil {
			t.Errorf("%s: the file loaded, and the two blocks must never be read as one (F23)", name)
		}
	}
}

// TestNodeRemovalsRecordNamesNotValues: the explicit-unset record (D2607, F22)
// holds NAMES, one per block, because silence in this file keeps the node's
// value — so a removal that reached the node only by a name's disappearance
// would never reach it, and a revoked credential would go on being served. The
// entries are held to the shape a name of their own block has, and every
// refusal here refuses to repeat the entry: the record is the one list in this
// file where a value may be pasted where a name belongs, so an entry that fails
// the name rule is exactly the text a sentence must not print.
func TestNodeRemovalsRecordNamesNotValues(t *testing.T) {
	c, err := Parse([]byte(sample + `node_removals:
  pools: [acme-pool, acme-backup]
  crafts: [senior-review]
  craft_credentials: [acme-main]
`))
	if err != nil {
		t.Fatalf("a record of names was refused: %v", err)
	}
	r := c.NodeRemovals
	if r == nil {
		t.Fatal("the file states a record and the config carries none")
	}
	if len(r.Pools) != 2 || r.Pools[0] != "acme-pool" || len(r.Crafts) != 1 || r.Crafts[0] != config.CraftSeniorReview || len(r.CraftCredentials) != 1 || r.CraftCredentials[0] != "acme-main" {
		t.Errorf("node_removals = %+v, want the three names the file lists", r)
	}
	if r.IsZero() {
		t.Error("a record naming three removals reads as no removals, so a reconcile would clear a state there is")
	}
	if empty, err := Parse([]byte(sample + "node_removals: {}\n")); err != nil || empty.NodeRemovals == nil || !empty.NodeRemovals.IsZero() {
		t.Errorf("a written empty block = %+v, %v; want a record naming nothing", empty.NodeRemovals, err)
	}

	for name, doc := range map[string]string{
		"a pool where a name belongs":        "node_removals:\n  pools: [{name: acme-pool, members: [acme-main]}]\n",
		"a value where a name belongs":       "node_removals:\n  pools: ['acme-main: {type: openrouter}']\n",
		"a craft that is not one of four":    "node_removals:\n  crafts: [senior]\n",
		"an entry with no name in it":        "node_removals:\n  craft_credentials: ['']\n",
		"a name recorded twice":              "node_removals:\n  pools: [acme-pool, acme-pool]\n",
		"a block the node does not have":     "node_removals:\n  credentials: [acme-main]\n",
		"a block named after the node's own": "node_removals:\n  craft_pools: [acme-pool]\n",
	} {
		_, err := Parse([]byte(sample + doc))
		if err == nil {
			t.Errorf("%s: the file loaded, and a removal is a name of a block this node has", name)
			continue
		}
		if strings.Contains(err.Error(), "acme-main: {type: openrouter}") {
			t.Errorf("%s: the refusal repeats the entry, which is what the record cannot do: a value pasted where a name belongs would reach a terminal, a log and an issue report through it (the batch.secrets rule)", name)
		}
	}
	// The node's `credentials` member is the cascade, has no names of its own
	// and is never listed here (F23): the cascade is removed by publishing a
	// cascade without it, which is what makes `node_removals` a record of the
	// three blocks that ARE lists of names and of nothing else.
	if _, err := Parse([]byte(sample + "node_removals:\n  credentials: [acme-main]\n")); err == nil {
		t.Error("the record lists the node's credentials block, which is the cascade and holds no names")
	}
}

// TestCascadeCarrierNeverReachesTheSharedCopy pins the shared-config half from
// this package's side: the three are the owner's own, and Config.Shared drops
// all of them whatever the local file says. MergeShared is checked beside it
// because it is the other path into a published copy — it takes the published
// object's fields for what the local one lacks, so a field missed here would
// come back from the bucket into the file this machine next publishes.
func TestCascadeCarrierNeverReachesTheSharedCopy(t *testing.T) {
	c, err := Parse([]byte(sample + `credential_cascade: {fallback: auto, wait_threshold: 90s}
model_grades:
  strong: {model: anthropic/claude-sonnet-5.5, access: acme-main}
node_removals: {pools: [acme-pool]}
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.CredentialCascade == nil || c.ModelGrades == nil || c.NodeRemovals == nil {
		t.Fatalf("the fixture states none of the three: %+v", c)
	}
	for name, got := range map[string]*Config{"shared": c.Shared(), "merged": MergeShared(nil, c)} {
		if got.CredentialCascade != nil || got.ModelGrades != nil || got.NodeRemovals != nil {
			t.Errorf("%s: cascade=%+v grades=%+v removals=%+v; the three are the owner's own and reach a run through the policy node",
				name, got.CredentialCascade, got.ModelGrades, got.NodeRemovals)
			continue
		}
		out, err := got.Marshal()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, key := range []string{"credential_cascade", "model_grades", "node_removals"} {
			if strings.Contains(string(out), key) {
				t.Errorf("%s: the published copy carries %s:\n%s", name, key, out)
			}
		}
	}
	if c.CredentialCascade == nil || c.ModelGrades == nil || c.NodeRemovals == nil {
		t.Error("Shared/MergeShared cleared the local carriers, so the file the operator wrote is not what the commands read back")
	}
}
