package projectpolicy

import (
	"encoding/json"
	"strings"
	"testing"
)

// The published subscription switch (docs/model-crafts/reconciliation.md F22,
// D1622, D1636, plan MC5): whether a run may spread a project across two or
// more Claude OAuth subscriptions at all. It is the one model-crafts member
// that is a statement about the others rather than a definition among them, so
// what these tests pin is the pair of facts a run reads it through — an absent
// member is off, and a member the owner wrote is the owner's own answer,
// false included.

// TestPolicyWithNoSubscriptionSpreadWritesNoMember: the member is a pointer
// and the assertion is the marshal, because the publisher marshals this struct
// straight to the node — the rule Crafts and Orchestrator are held to, and the
// one that keeps every installation's node byte for byte what it was before the
// switch existed.
func TestPolicyWithNoSubscriptionSpreadWritesNoMember(t *testing.T) {
	raw, err := json.Marshal(&Policy{Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "subscription_spread") {
		t.Errorf("a policy whose owner wrote no switch writes a member: %s", raw)
	}
}

// TestSubscriptionSpreadRoundTrips: what the writer writes is what the reader
// reads, and the three spellings are three facts. A written true is the owner
// turning the spread on, a written false is his own "no" and is kept as the
// value it is (which is what the pointer is for: a value field could not tell
// it from an absent member), and an absent or null member is the state every
// installation is in and the one the re-encode writes nothing for.
func TestSubscriptionSpreadRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want *bool
	}{
		{"written true", `{"version":6,"subscription_spread":true}`, boolp(true)},
		{"written false", `{"version":6,"subscription_spread":false}`, boolp(false)},
		{"absent", `{"version":6}`, nil},
		{"null", `{"version":6,"subscription_spread":null}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode([]byte(tc.raw))
			if err != nil || !found || p == nil {
				t.Fatalf("Decode(%s) = (%v, %v, %v), want a policy", tc.raw, p, found, err)
			}
			switch {
			case tc.want == nil && p.SubscriptionSpread != nil:
				t.Fatalf("SubscriptionSpread = %v, want unset", *p.SubscriptionSpread)
			case tc.want != nil && p.SubscriptionSpread == nil:
				t.Fatalf("SubscriptionSpread = unset, want %v", *tc.want)
			case tc.want != nil && *p.SubscriptionSpread != *tc.want:
				t.Fatalf("SubscriptionSpread = %v, want %v", *p.SubscriptionSpread, *tc.want)
			}
			if len(p.UnknownKeys) != 0 {
				t.Errorf("UnknownKeys = %v, want none: the switch is a key this Fugaro reads", p.UnknownKeys)
			}
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil && strings.Contains(string(b), "subscription_spread") {
				t.Errorf("a policy decoded from %s re-marshals with the member: %s", tc.raw, b)
			}
			if tc.want != nil {
				// The member this package wrote is the member it reads: the
				// round trip is taken of the member alone, a policy written by
				// hand having no budget block to decode.
				var doc map[string]json.RawMessage
				if err := json.Unmarshal(b, &doc); err != nil {
					t.Fatal(err)
				}
				back, _, err := Decode([]byte(`{"version":6,"subscription_spread":` + string(doc["subscription_spread"]) + `}`))
				if err != nil || back == nil || back.SubscriptionSpread == nil {
					t.Fatalf("the member this package wrote does not decode = (%v, %v)", back, err)
				}
				if *back.SubscriptionSpread != *tc.want {
					t.Errorf("the round trip = %v, want %v", *back.SubscriptionSpread, *tc.want)
				}
			}
		})
	}
}

// TestSubscriptionSpreadDecodesStrictly: the member is a boolean and nothing
// else. A value that is neither true nor false is a person's mistake in a node
// the owner writes by hand, and reading it as one of the two would be the
// switch answering a question nobody asked — the direction D1622's rule fails
// in being "off", which is what a node that is refused never gets to say.
func TestSubscriptionSpreadDecodesStrictly(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"a string", `{"version":6,"subscription_spread":"true"}`, "cannot unmarshal string"},
		{"a number", `{"version":6,"subscription_spread":1}`, "cannot unmarshal number"},
		{"an object", `{"version":6,"subscription_spread":{}}`, "cannot unmarshal object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Decode([]byte(tc.raw))
			if err == nil {
				t.Fatalf("Decode(%s) read a switch out of %s", tc.raw, tc.name)
			}
			if !strings.Contains(err.Error(), "subscription_spread") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name the member and the shape: %v", err)
			}
		})
	}
}
