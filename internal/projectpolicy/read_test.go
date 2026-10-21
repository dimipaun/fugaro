package projectpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// readDB is a database acting as an operator: the reads Read makes are the
// operator's own, with no run token involved.
func readDB(t *testing.T) (*gcpfake.RTDB, *rtdb.Client) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "operator-id-token" }})
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

const readPolicy = `{"version": 3, "budget": {"mode": "observe", "per_run_usd": 5}, "allow_repos": ["github/acme/*"]}`

// The marker is what decides whether this installation can have a policy at
// all (task 1.3 step 1): absent, or below PolicyRulesVersion, this
// installation's rules predate the policy nodes, and a read that is refused
// there is not an error.
func TestReadWithoutTheMarkerIsLegacy(t *testing.T) {
	for name, seed := range map[string]func(*gcpfake.RTDB){
		"no marker and no node": func(*gcpfake.RTDB) {},
		"no marker and a node":  func(f *gcpfake.RTDB) { f.Set(PathShared(), json.RawMessage(readPolicy)) },
		"no marker and a refused read": func(f *gcpfake.RTDB) {
			f.Set(PathShared(), json.RawMessage(readPolicy))
			f.DenyRead(PathShared())
		},
		// Generation 0 is the only generation below PolicyRulesVersion that an
		// integer can name, and it is the one with no policy nodes: generation
		// 1, the policy generation, is TestReadKeepsThePolicyOfThePolicyGeneration's
		// case below, and reading it as legacy took a published policy out of
		// every run's reach (the review finding on P2.1).
		"a marker below the policy generation": func(f *gcpfake.RTDB) {
			f.Set(PathRulesMarker(), PolicyRulesVersion-1)
			f.Set(PathShared(), json.RawMessage(readPolicy))
			f.DenyRead(PathShared())
		},
	} {
		f, db := readDB(t)
		seed(f)
		p, found, err := Read(context.Background(), db)
		if p != nil || found || err != nil {
			t.Errorf("%s: Read = (%v, %v, %v), want (nil, false, nil): the owner's rules deploy has not happened yet, and nothing may halt on that", name, p, found, err)
		}
	}
}

// A marker at the policy generation — generation 1, while this binary deploys
// generation 2 — is a deployment that *does* carry the config/policy nodes, and
// the policy published under it is read. Generation 1 is a real deployment: its
// rules have the nodes, `fugaro project policy publish` wrote there and
// `fugaro update --firebase` of that release recorded it. Gating on RulesVersion
// answered "not found" for it, which every caller of Read takes as "this
// installation has no policy": the runner kept the ceiling its job environment
// was deployed with, the launcher skipped the allow-list and release-skew gates
// (internal/cli's launchPolicyGates) and doctor printed a published policy
// beside a legacy read — on an installation whose policy was published and
// governed nothing (the review finding on P2.1).
//
// Mutation (verified, run and restored): gate on `gen < RulesVersion` instead
// of `gen < PolicyRulesVersion` in Read — this test fails, Read answering
// (nil, false, nil) for the published node.
func TestReadKeepsThePolicyOfThePolicyGeneration(t *testing.T) {
	if PolicyRulesVersion >= RulesVersion {
		t.Fatalf("PolicyRulesVersion = %d and RulesVersion = %d: this test is about a generation below this binary's, and the two have met", PolicyRulesVersion, RulesVersion)
	}
	f, db := readDB(t)
	f.Set(PathRulesMarker(), PolicyRulesVersion)
	f.Set(PathShared(), json.RawMessage(readPolicy))
	p, found, err := Read(context.Background(), db)
	if err != nil || !found || p == nil {
		t.Fatalf("Read = (%v, %v, %v), want the policy: a marker at the policy generation carries the policy nodes", p, found, err)
	}
	if p.Budget.Mode != "observe" || p.Budget.PerRunUSD != 5 {
		t.Errorf("policy = %+v, want the published one", p)
	}
	// And a denial here is still a refusal rather than "no policy": the rules
	// were changed out from under a published policy.
	f.DenyRead(PathShared())
	if p, found, err := Read(context.Background(), db); err == nil || p != nil || found {
		t.Errorf("a denied read = (%v, %v, %v), want a refusal", p, found, err)
	}
}

// With the marker deployed the node's own state decides: absent is today's
// behaviour, present and readable is the policy.
func TestReadWithTheMarker(t *testing.T) {
	f, db := readDB(t)
	f.Set(PathRulesMarker(), RulesVersion)
	p, found, err := Read(context.Background(), db)
	if p != nil || found || err != nil {
		t.Fatalf("an absent node = (%v, %v, %v), want (nil, false, nil): installed, not adopted", p, found, err)
	}

	f.Set(PathShared(), json.RawMessage(readPolicy))
	if p, found, err = Read(context.Background(), db); err != nil || !found || p == nil {
		t.Fatalf("Read = (%v, %v, %v), want the policy", p, found, err)
	}
	if p.Budget.Mode != "observe" || p.Budget.PerRunUSD != 5 {
		t.Errorf("policy = %+v", p)
	}

	// A null body is an absent node, not a policy.
	f.Set(PathShared(), nil)
	if p, found, err = Read(context.Background(), db); p != nil || found || err != nil {
		t.Errorf("a null node = (%v, %v, %v), want (nil, false, nil)", p, found, err)
	}
}

// A marker ABOVE this generation's reads the node: the deployed rules are a
// newer release's, which carry the policy nodes, and whether this binary
// understands the policy it finds there is Decode's question (Version), not the
// marker's. The gate is asymmetric on purpose — below is "the policy nodes do
// not exist here yet", above is "they do" — and a read that treated the two
// alike would answer "no policy" for an installation that has one, which every
// caller takes as an empty ceiling.
//
// Mutation (verified, run and restored): make the gate `gen != RulesVersion`
// instead of `gen < RulesVersion` — the policy case fails, Read answering
// (nil, false, nil) for a published node.
func TestReadWithAMarkerAboveThisGeneration(t *testing.T) {
	f, db := readDB(t)
	f.Set(PathRulesMarker(), RulesVersion+1)
	f.Set(PathShared(), json.RawMessage(readPolicy))
	p, found, err := Read(context.Background(), db)
	if err != nil || !found || p == nil {
		t.Fatalf("Read = (%v, %v, %v), want the policy: newer rules still carry the node", p, found, err)
	}
	if p.Budget.Mode != "observe" || p.Budget.PerRunUSD != 5 {
		t.Errorf("policy = %+v, want the published one", p)
	}

	// The node still decides the other two states: absent is "installed, not
	// adopted", and one this binary cannot read is an error rather than a
	// policy-less answer.
	f.Set(PathShared(), nil)
	if p, found, err = Read(context.Background(), db); p != nil || found || err != nil {
		t.Errorf("an absent node under a newer marker = (%v, %v, %v), want (nil, false, nil)", p, found, err)
	}
	f.Set(PathShared(), json.RawMessage(`{"version": 99}`))
	if p, found, err = Read(context.Background(), db); err == nil || p != nil || !found {
		t.Errorf("an unreadable policy under a newer marker = (%v, %v, %v), want a refusal", p, found, err)
	}
}

// A denial with the marker deployed is the case that must not fall back to a
// wider ceiling: the rules were changed out from under a published policy.
func TestReadRefusesADeniedNode(t *testing.T) {
	f, db := readDB(t)
	f.Set(PathRulesMarker(), RulesVersion)
	f.Set(PathShared(), json.RawMessage(readPolicy))
	f.DenyRead(PathShared())
	p, found, err := Read(context.Background(), db)
	if err == nil || p != nil {
		t.Fatalf("Read = (%v, %v, %v), want a refusal", p, found, err)
	}
	if !errors.Is(err, rtdb.ErrPermission) || !strings.Contains(err.Error(), PathShared()) {
		t.Errorf("err = %v, want it to name the node and the denial", err)
	}
}

// An unreadable or unusable node is an error too: the caller must halt, never
// read it as an empty ceiling.
func TestReadRefusesAnUnusableNode(t *testing.T) {
	for name, body := range map[string]string{
		"not an object":                     `[1, 2, 3]`,
		"no version":                        `{"budget": {"mode": "observe"}}`,
		"a version this code does not know": `{"version": 99}`,
		"a misspelt budget key":             `{"version": 3, "budget": {"per_run_usdc": 5}}`,
		"a bad mode":                        `{"version": 3, "budget": {"mode": "enforc"}}`,
	} {
		f, db := readDB(t)
		f.Set(PathRulesMarker(), RulesVersion)
		f.Set(PathShared(), json.RawMessage(body))
		p, found, err := Read(context.Background(), db)
		if err == nil || p != nil || !found {
			t.Errorf("%s: Read = (%v, %v, %v), want a refusal", name, p, found, err)
		}
	}
}

// The marker is a node a person can edit by hand: a value that is not an
// integer is a user error naming it, never a silent "no policy" (and never a
// zero that reads as legacy).
func TestReadRefusesANonIntegerMarker(t *testing.T) {
	for _, v := range []any{"1", []any{1}, map[string]any{"gen": 1}, 1.5, true} {
		f, db := readDB(t)
		f.Set(PathRulesMarker(), v)
		f.Set(PathShared(), json.RawMessage(readPolicy))
		p, found, err := Read(context.Background(), db)
		if err == nil || p != nil || found {
			t.Fatalf("marker %#v: Read = (%v, %v, %v), want a refusal", v, p, found, err)
		}
		var me *MarkerError
		if !errors.As(err, &me) || !strings.Contains(err.Error(), PathRulesMarker()) {
			t.Errorf("marker %#v: err = %v, want a MarkerError naming the node (a user error, not an outage)", v, err)
		}
	}
}

// A negative marker is an integer, so the ordinary "< RulesVersion" test read
// it as the pre-policy generation (P3) — and a hand-edited -1 therefore made a
// project-centric installation silently legacy: every run of it would keep the
// ceiling its job environment was deployed with, with the policy it was meant
// to be bound by unread, and a launcher could publish one that never governs.
// No deploy writes a negative generation (RulesVersion only goes up),
// so the node is refused as the MarkerError it is, exactly as a value that is
// not an integer at all is: the caller must halt on a node a person edited,
// never guess what it meant.
//
// Hand mutation this fails on: dropping the `gen < 0` check (or widening it to
// `gen < -1`), which turns the negative marker back into (nil, false, nil).
func TestReadRefusesANegativeMarker(t *testing.T) {
	for _, gen := range []any{-1, -RulesVersion, -1000} {
		f, db := readDB(t)
		f.Set(PathRulesMarker(), gen)
		f.Set(PathShared(), json.RawMessage(readPolicy))
		p, found, err := Read(context.Background(), db)
		if err == nil || p != nil || found {
			t.Fatalf("marker %v: Read = (%v, %v, %v), want a refusal: a negative generation is not the legacy path", gen, p, found, err)
		}
		var me *MarkerError
		if !errors.As(err, &me) || !strings.Contains(err.Error(), PathRulesMarker()) {
			t.Errorf("marker %v: err = %v, want a MarkerError naming the node (a user error, not an outage)", gen, err)
		}
	}
	// A generation above this one is at least this one's: it reads as deployed
	// rather than as an error, so the check above refuses negatives alone.
	f, db := readDB(t)
	f.Set(PathRulesMarker(), RulesVersion+1)
	f.Set(PathShared(), json.RawMessage(readPolicy))
	if p, found, err := Read(context.Background(), db); err != nil || !found || p == nil {
		t.Errorf("marker above this generation: Read = (%v, %v, %v), want the policy", p, found, err)
	}
}

// The keys this Fugaro does not know are tolerated and exposed (Decode's own
// tolerance must not be silent): the runner logs them, the CLI refuses them.
func TestDecodeExposesUnknownTopLevelKeys(t *testing.T) {
	p, found, err := Decode([]byte(`{"version": 3, "budget": {"mode": "observe"}, "future_key": 1, "another": {"a": 1}}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want a policy", p, found, err)
	}
	if got := strings.Join(p.UnknownKeys, ","); got != "another,future_key" {
		t.Errorf("UnknownKeys = %q, want another,future_key (sorted)", got)
	}
	if p, found, err := Decode(json.RawMessage(readPolicy)); err != nil || !found {
		t.Errorf("the fixture policy = (%v, %v, %v)", p, found, err)
	} else if len(p.UnknownKeys) != 0 {
		t.Errorf("a policy with only known keys has UnknownKeys = %v", p.UnknownKeys)
	}
}

// A routing block whose member this Fugaro cannot name is refused, not read as
// an empty switch. `routing: {claud: ...}` is a misspelt claude and
// `routing: {gemini: ...}` belongs to something this release does not route:
// either would decode to a nil switch and leave every Claude call on the direct
// route while the node says otherwise, silently, on the document a run is
// routed by.
func TestDecodeRefusesARoutingBlockThatNamesNoClaude(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"a misspelt claude", `{"version": 3, "routing": {"claud": {"via": "direct"}}}`, `"claud"`},
		{"a member this release does not route", `{"version": 3, "routing": {"gemini": {"via": "vertex"}}}`, `"gemini"`},
		{"claude written with no switch", `{"version": 3, "routing": {"claude": null}}`, "routing.claude"},
		{"a member beside a null claude", `{"version": 3, "routing": {"claude": null, "gemini": {"via": "vertex"}}}`, "routing.claude"},
	} {
		raw := []byte(tc.raw)
		p, found, err := Decode(raw)
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode = (%v, %v, %v), want a refusal", tc.name, p, found, err)
			continue
		}
		for _, want := range []string{tc.want, "claude"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: err = %v, want it to name %s", tc.name, err, want)
			}
		}
	}

	// The other side: a block this Fugaro *can* act on is read, whatever else it
	// names — the switch is the member named claude, and a sibling a newer
	// release added is not this Fugaro's to refuse.
	raw := []byte(`{"version": 3, "providers": {"openrouter": {"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["anthropic/claude-sonnet-5.5"], "allow_data_to": ["*"]}}, "routing": {"claude": {"via": "auto", "models": ["claude-sonnet-5-5"], "future_route": "x"}, "gemini": {"via": "vertex"}}}`)
	p, found, err := Decode(raw)
	if err != nil || !found || p == nil || p.Routing == nil || p.Routing.Claude == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the switch read", p, found, err)
	}
	if !p.Routing.Claude.Names("claude-sonnet-5-5") {
		t.Errorf("the switch = %+v, want the model it names", p.Routing.Claude)
	}

	// An empty block names nothing, and a nil switch is what it says: the same
	// state as an absent one (D1b), not the refusal above.
	p, found, err = Decode([]byte(`{"version": 3, "routing": {}}`))
	if err != nil || !found || p == nil || p.Routing != nil && p.Routing.Claude != nil {
		t.Fatalf("an empty routing block = (%v, %v, %v), want it read as no switch", p, found, err)
	}
}

// Member names inside `routing` that encoding/json reads as another name are
// refused, at both levels of the block. `{"claude": {"via": "direct", "VIA":
// "openrouter"}}` is not a switch with a misspelt key in it: the decoder matches
// a member name case-insensitively and keeps the last of two that fold to one
// name, so the route this Fugaro took would depend on the order the keys were
// written in — the same for `"via"` twice. And `{"CLAUDE": {...}}` alone is read
// as the switch by that same case-insensitive match, which is why the names are
// checked before the block is judged to name no claude at all.
func TestDecodeRefusesDuplicateOrCaseFoldedRoutingMembers(t *testing.T) {
	for _, tc := range []struct{ name, raw, want, why string }{
		{"a case-folded via beside the real one", `{"version": 3, "routing": {"claude": {"via": "direct", "VIA": "openrouter"}}}`, "routing.claude", "reads as one member"},
		{"via written twice", `{"version": 3, "routing": {"claude": {"via": "openrouter", "via": "direct"}}}`, "routing.claude", "keeps the last one it reads"},
		{"models spelled in another case", `{"version": 3, "routing": {"claude": {"via": "direct", "MODELS": ["claude-sonnet-5-5"]}}}`, "routing.claude", "spells models as \"MODELS\""},
		{"claude spelled in another case", `{"version": 3, "routing": {"CLAUDE": {"via": "direct"}}}`, "routing", "spells claude as \"CLAUDE\""},
		{"a case-folded claude beside the real one", `{"version": 3, "routing": {"claude": {"via": "direct"}, "Claude": {"via": "openrouter"}}}`, "routing", "reads as one member"},
	} {
		p, found, err := Decode([]byte(tc.raw))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode = (%v, %v, %v), want a refusal", tc.name, p, found, err)
			continue
		}
		// The refusal names the block it is in and says what encoding/json would
		// have done with the name — which is the reason the spelling matters here
		// and does not for a member this Fugaro does not read.
		for _, want := range []string{tc.want, tc.why} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: err = %v, want it to contain %q", tc.name, err, want)
			}
		}
	}

	// The other side: a member this Fugaro does not model, spelled once, is not a
	// name read two ways — it stays tolerated, exactly as it is in the tests
	// above.
	if _, found, err := Decode([]byte(`{"version": 3, "routing": {"claude": {"via": "direct", "future_route": "x"}, "gemini": {"via": "vertex"}}}`)); err != nil || !found {
		t.Errorf("an unmodelled sibling = (found=%v, err=%v), want it read", found, err)
	}
}

// routing.claude.via is read in phase 1b, and must not accept garbage now.
func TestDecodeRefusesAnUnknownVia(t *testing.T) {
	for _, via := range []string{"", "Direct", "openrouter-api", "none"} {
		raw := []byte(`{"version": 3, "routing": {"claude": {"via": "` + via + `", "models": ["claude-sonnet-5"]}}}`)
		p, found, err := Decode(raw)
		if err == nil || p != nil || !found {
			t.Errorf("via %q: Decode = (%v, %v, %v), want a refusal", via, p, found, err)
		} else if !strings.Contains(err.Error(), "routing.claude.via") {
			t.Errorf("via %q: err = %v, want it to name the key", via, err)
		}
	}
	// The provider entry the routed via values resolve through: without it the
	// switch names a route nothing in the node claims, which is refused on
	// purpose (validRouting). A via this Fugaro cannot act on is still the
	// refusal this test is about, so the block is carried in both loops where
	// the via itself is the fault.
	for _, via := range []string{RouteDirect, RouteOpenRouter, RouteAuto} {
		raw := []byte(`{"version": 3, "providers": {"openrouter": {"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer", "secret": "openrouter-key", "models": ["anthropic/claude-sonnet-5.5"], "allow_data_to": ["*"]}}, "routing": {"claude": {"via": "` + via + `", "models": ["claude-sonnet-5-5"]}}}`)
		if p, found, err := Decode(raw); err != nil || !found || p == nil || p.Routing.Claude.Via != via {
			t.Errorf("via %q: Decode = (%v, %v, %v), want it accepted", via, p, found, err)
		}
	}
}
