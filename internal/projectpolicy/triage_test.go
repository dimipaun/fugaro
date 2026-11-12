package projectpolicy

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The published node's triage member (T-TR5, D1854): the owner's own switch
// for the run's triage stage, the one a repository may turn off for itself and
// may not turn back on (internal/policy's Merge, tighter-only).
//
// The member is additive: an installation already on this release's policy
// version publishes a node without it, an older reader lists it in UnknownKeys
// and reads the rest (P8), and no migration step is owed — which is what
// TestInstallationAtThePreviousPolicyConverges below is for.

// TestTriageMemberIsReadStrictly: the member is a bool or nothing at all. A
// node that writes another type is refused rather than read as unset, because
// the value decides whether a run spends a stage's model call: a `"false"` the
// reader dropped would be a run triaging when the owner said not to.
func TestTriageMemberIsReadStrictly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		want  *bool
		found bool
		why   string // a substring the refusal must carry
	}{
		{name: "off", body: `"triage":false`, want: boolp(false), found: true},
		{name: "on", body: `"triage":true`, want: boolp(true), found: true},
		{name: "null", body: `"triage":null`},
		{name: "absent", body: `"base_branch":"main"`},
		{name: "a string", body: `"triage":"false"`, why: "triage"},
		{name: "a number", body: `"triage":0`, why: "triage"},
		{name: "an object", body: `"triage":{"off":true}`, why: "triage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"version":6,` + tc.body + `}`)
			p, found, err := Decode(raw)
			if tc.why != "" {
				if err == nil {
					t.Fatalf("the node %s was read: %v", raw, p.Triage)
				}
				if !strings.Contains(err.Error(), tc.why) {
					t.Errorf("the refusal %q does not name %q", err, tc.why)
				}
				return
			}
			if err != nil || !found || p == nil {
				t.Fatalf("Decode(%s) = (%v, %v, %v), want a policy", raw, p, found, err)
			}
			switch {
			case tc.want == nil && p.Triage != nil:
				t.Errorf("Triage = %v, want unset: an absent member and a written null are one answer", *p.Triage)
			case tc.want != nil && (p.Triage == nil || *p.Triage != *tc.want):
				t.Errorf("Triage = %v, want %v", p.Triage, *tc.want)
			}
			if len(p.UnknownKeys) != 0 {
				t.Errorf("UnknownKeys = %v, want none: triage is a key this Fugaro reads", p.UnknownKeys)
			}
		})
	}
	// A node that misspells the member is the older reader's own case: the key
	// is unknown, listed and read past rather than refused, which is the
	// additive-member promise a node of a later release relies on (P8). What it
	// must not do is answer the switch.
	p, found, err := Decode([]byte(`{"version":6,"triage_off":true}`))
	if err != nil || !found {
		t.Fatalf("a node with a misspelt member was refused: (%v, %v, %v)", p, found, err)
	}
	if p.Triage != nil {
		t.Errorf("Triage = %v, want unset: a member nobody writes is not the switch", *p.Triage)
	}
	if want := []string{"triage_off"}; !slices.Equal(p.UnknownKeys, want) {
		t.Errorf("UnknownKeys = %v, want %v", p.UnknownKeys, want)
	}
}

// TestInstallationAtThePreviousPolicyConverges: the member is additive, so an
// installation whose published node predates it converges with no operator step
// at all — the node keeps its version, the migration walk has nothing to do,
// the node decodes, and every key it does carry is read exactly as before. That
// is the HARD RULE (F15) answered by construction rather than by a migration:
// nothing was published that an installation has to be moved to, and the runner
// reads the member as unset, which is the stage's own default (D1851).
//
// The other direction is the same decision: a node that *does* carry the member
// is read by a reader that predates it, listed in UnknownKeys and read past, so
// a run started by a release behind this one neither refuses the node nor
// misreads the switch as on.
func TestInstallationAtThePreviousPolicyConverges(t *testing.T) {
	// A node of the release behind: what an installation that has not
	// re-applied anything carries. Its version is this release's, because the
	// member moved no version — an installation converges by carrying on, not
	// by being walked forward.
	previous := []byte(fmt.Sprintf(`{"version":%d,"budget":{"mode":"observe","per_run_usd":12},"base_branch":"main","allow_repos":["github/acme/app"]}`, Version))
	migrated, changes, err := Migrate(previous)
	if err != nil {
		t.Fatalf("Migrate refused a node at this version: %v", err)
	}
	if len(changes) != 0 || string(migrated) != string(previous) {
		t.Errorf("Migrate wrote %s (%d change(s)): a node at this version is left exactly as it is", migrated, len(changes))
	}
	p, found, err := Decode(previous)
	if err != nil || !found || p == nil {
		t.Fatalf("Decode(the previous release's node) = (%v, %v, %v), want a policy", p, found, err)
	}
	if p.Triage != nil {
		t.Errorf("Triage = %v, want unset: the node predates the member", *p.Triage)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: the node carries only keys this Fugaro reads", p.UnknownKeys)
	}
	if p.Budget.Mode != "observe" || p.Budget.PerRunUSD != 12 || p.BaseBranch != "main" {
		t.Errorf("the node's own keys read as %+v: an installation at the previous policy converges to the same policy", p.Budget)
	}

	// The reader of the release behind, met with a node that carries the
	// member: the same decode, minus the key it does not know.
	carrying := []byte(fmt.Sprintf(`{"version":%d,"budget":{"mode":"observe"},"triage":false}`, Version))
	preKey := make(map[string]bool, len(knownTopLevelKeys))
	for k, v := range knownTopLevelKeys {
		preKey[k] = v
	}
	delete(preKey, "triage")
	q, found, err := decodeWithKeys(carrying, preKey)
	if err != nil || !found || q == nil {
		t.Fatalf("the reader that predates the member refused the node: (%v, %v, %v)", q, found, err)
	}
	if want := []string{"triage"}; !slices.Equal(q.UnknownKeys, want) {
		t.Errorf("UnknownKeys = %v, want %v: that is the node a CLI of the release behind meets, and the runner of it logs it rather than stopping", q.UnknownKeys, want)
	}
	if q.Budget.Mode != "observe" {
		t.Errorf("the older reader read budget.mode = %q, want observe: the rest of the node is read past the member", q.Budget.Mode)
	}
}

func boolp(v bool) *bool { return &v }
