package projectpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestTheSwitchIsOffWhenAbsent: R9's switch (WC6, D2972) is off wherever the
// member's published policy does not name it, and that is the whole of the
// default — a member that never set it serves no personal token, and no
// version of this reader has to be told anything else.
//
// The key is one top-level member of the node, so the three states a reader
// must tell apart are the node's own: absent (off), false (off, written), true
// (on). A pointer is what makes the first two distinguishable, and a value that
// is not a boolean at all is refused naming the key rather than read as off —
// a switch that silently answers "off" to a misspelling is a switch a member
// cannot turn on.
func TestTheSwitchIsOffWhenAbsent(t *testing.T) {
	bare := []byte(fmt.Sprintf(`{"version":%d,"budget":{"mode":"observe","per_run_usd":12},"allow_repos":["github/acme/app"]}`, Version))
	p, found, err := Decode(bare)
	if err != nil || !found || p == nil {
		t.Fatalf("Decode(a node without the switch) = (%v, %v, %v), want a policy", p, found, err)
	}
	if p.PersonalWorkspaceTokens != nil {
		t.Errorf("PersonalWorkspaceTokens = %v, want unset: the node does not name it", *p.PersonalWorkspaceTokens)
	}
	if p.PersonalWorkspaceTokensOn() {
		t.Error("PersonalWorkspaceTokensOn() = true for a node that does not name the switch, want false: absent reads as off (R9)")
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: this release's reader knows the key", p.UnknownKeys)
	}
	var unset *Policy
	if unset.PersonalWorkspaceTokensOn() {
		t.Error("PersonalWorkspaceTokensOn() = true for a nil policy, want false: a policy that is not there serves no personal token")
	}

	for _, tc := range []struct {
		name string
		body string
		on   bool
	}{
		{"written true", `"personal_workspace_tokens":true`, true},
		{"written false", `"personal_workspace_tokens":false`, false},
		{"written null", `"personal_workspace_tokens":null`, false},
	} {
		raw := []byte(fmt.Sprintf(`{"version":%d,"budget":{"mode":"observe"},%s}`, Version, tc.body))
		q, found, err := Decode(raw)
		if err != nil || !found || q == nil {
			t.Fatalf("%s: Decode = (%v, %v, %v), want a policy", tc.name, q, found, err)
		}
		if got := q.PersonalWorkspaceTokensOn(); got != tc.on {
			t.Errorf("%s: PersonalWorkspaceTokensOn() = %v, want %v", tc.name, got, tc.on)
		}
		if len(q.UnknownKeys) != 0 {
			t.Errorf("%s: UnknownKeys = %v, want none", tc.name, q.UnknownKeys)
		}
	}

	// A value that is not a boolean is the node's own refusal, and it names
	// the key: the member's admin has to be able to see which member is wrong.
	bad := []byte(fmt.Sprintf(`{"version":%d,"budget":{"mode":"observe"},"personal_workspace_tokens":"yes"}`, Version))
	if q, _, err := Decode(bad); err == nil || q != nil {
		t.Fatalf("Decode(personal_workspace_tokens = \"yes\") = (%v, %v), want a refusal", q, err)
	} else if !strings.Contains(err.Error(), "personal_workspace_tokens") {
		t.Errorf("the refusal reads %q, want it to name the member", err)
	}
}

// TestTheRealReaderAnswersFromTheMemberPolicy: the reader the service, the
// launcher and doctor share is the node's own reader (PersonSwitchOverDB), so
// what it answers for a member is what that member's published policy says and
// nothing else. It is the WC6 side of creds.PersonSwitchReader: the interface
// itself is pinned in the external conformance test beside this file, and what
// is pinned here is the answer over a real (fake) database read.
func TestTheRealReaderAnswersFromTheMemberPolicy(t *testing.T) {
	f, db := readDB(t)
	f.Set(PathRulesMarker(), PolicyRulesVersion)
	f.Set(PathShared(), json.RawMessage(fmt.Sprintf(`{"version":%d,"budget":{"mode":"observe"},"personal_workspace_tokens":true}`, Version)))
	r := PersonSwitchOverDB(db)
	on, err := r.PersonSwitchOn(context.Background(), "github/acme/app")
	if err != nil || !on {
		t.Fatalf("PersonSwitchOn = (%v, %v), want (true, nil): the member's policy names the switch with a true", on, err)
	}

	// The same member, with the key off: written false is off, and so is a
	// node that does not carry it at all.
	for name, body := range map[string]string{
		"written false": `"personal_workspace_tokens":false`,
		"absent":        `"budget":{"mode":"observe"}`,
	} {
		f.Set(PathShared(), json.RawMessage(fmt.Sprintf(`{"version":%d,%s}`, Version, body)))
		if on, err := r.PersonSwitchOn(context.Background(), "github/acme/app"); err != nil || on {
			t.Errorf("%s: PersonSwitchOn = (%v, %v), want (false, nil)", name, on, err)
		}
	}

	// A read that fails is an error and never a false (WC5, WC7): the caller
	// must not be handed "off" for a policy nobody could read.
	f.DenyRead(PathShared())
	if on, err := r.PersonSwitchOn(context.Background(), "github/acme/app"); err == nil || on {
		t.Errorf("a denied read = (%v, %v), want a refusal: a policy that could not be read is not an off switch", on, err)
	}
}

// TestTheReaderCarriesAnErrorAndAnAbsentProject: the two answers that are not
// the node's own value. A read that reports the project as not installed
// answers off — there is no member policy, so there is no switch — and a read
// that fails passes the failure through untouched, because a caller that folds
// it into false would serve the shared entries as if the member had said so.
func TestTheReaderCarriesAnErrorAndAnAbsentProject(t *testing.T) {
	boom := errors.New("rtdb: refused")
	r := PersonSwitchReader{Read: func(context.Context, string) (*Policy, bool, error) {
		return nil, false, boom
	}}
	if on, err := r.PersonSwitchOn(context.Background(), "github/acme/app"); !errors.Is(err, boom) || on {
		t.Errorf("a failing read = (%v, %v), want (false, boom)", on, err)
	}

	r = PersonSwitchReader{Read: func(context.Context, string) (*Policy, bool, error) {
		return nil, false, nil
	}}
	if on, err := r.PersonSwitchOn(context.Background(), "github/acme/app"); err != nil || on {
		t.Errorf("a member with no policy = (%v, %v), want (false, nil)", on, err)
	}
}

// TestAnOlderReaderDecodesPastTheSwitch: the policy's version does not move for
// the switch (R9), so a reader of the release behind meets a node that carries
// it and reads every other member of it exactly as before — the key is listed
// among the unknown top-level keys the decode tolerates, and the runner of that
// release warns once and keeps enforcing what it does understand.
//
// The decode is not the whole of the skew, and this test must not be read as
// one: the readers that DECIDE by the node refuse one carrying a key they do not
// know — the CLI's launch gate (internal/cli/launch_budget.go, projectPolicyRead)
// and the publish and migration stages among them — so the moment the member's
// `fugaro update` publishes the switch, an older CLI in that project loses
// launches on it until it is upgraded, the skew `bitbucket_tokens` had
// (docs/releases/v0.25.0.md, and the note WC6 leaves in the next release's
// `### Known limits`). What this test pins is the half the decode owns: no
// member is refused for the key, no other member is misread, and no version
// moves — which is what makes the upgrade of a CLI sufficient, and no
// republish, migration or fix by hand necessary.
func TestAnOlderReaderDecodesPastTheSwitch(t *testing.T) {
	carrying := []byte(fmt.Sprintf(`{"version":%d,"budget":{"mode":"observe","per_run_usd":12},"base_branch":"main","allow_repos":["github/acme/app"],"personal_workspace_tokens":true}`, Version))
	preKey := make(map[string]bool, len(knownTopLevelKeys))
	for k, v := range knownTopLevelKeys {
		preKey[k] = v
	}
	delete(preKey, "personal_workspace_tokens")

	q, found, err := decodeWithKeys(carrying, preKey)
	if err != nil || !found || q == nil {
		t.Fatalf("the reader that predates the switch refused the node: (%v, %v, %v)", q, found, err)
	}
	if want := []string{"personal_workspace_tokens"}; !slices.Equal(q.UnknownKeys, want) {
		t.Errorf("UnknownKeys = %v, want %v: the older reader lists the member and reads on", q.UnknownKeys, want)
	}
	if q.Budget.Mode != "observe" || q.Budget.PerRunUSD != 12 || q.BaseBranch != "main" {
		t.Errorf("the older reader read budget = %+v, base_branch = %q: the rest of the node is read past the member", q.Budget, q.BaseBranch)
	}
	if !slices.Equal(q.AllowRepos, []string{"github/acme/app"}) {
		t.Errorf("AllowRepos = %v, want the published list", q.AllowRepos)
	}

	// And the same node read by this release: the key is no longer unknown,
	// the switch is on, and the version is the one the node states.
	p, _, err := Decode(carrying)
	if err != nil || p == nil {
		t.Fatalf("Decode(the node carrying the switch) = (%v, %v)", p, err)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none", p.UnknownKeys)
	}
	if !p.PersonalWorkspaceTokensOn() {
		t.Error("PersonalWorkspaceTokensOn() = false, want true")
	}
	if p.Version != Version {
		t.Errorf("Version = %d, want %d: the switch moves no version", p.Version, Version)
	}
	// The migration walk has nothing to do with it either: a node at this
	// version that carries the switch is left exactly as it is.
	migrated, changes, err := Migrate(carrying)
	if err != nil {
		t.Fatalf("Migrate refused a node carrying the switch: %v", err)
	}
	if len(changes) != 0 || string(migrated) != string(carrying) {
		t.Errorf("Migrate wrote %s (%d change(s)): the switch is additive", migrated, len(changes))
	}
}
