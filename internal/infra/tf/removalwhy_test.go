package tf

import (
	"strings"
	"testing"
)

// TestRemovalRuleWhyRecognisesTheRemovalRulesSentences pins the predicate
// internal/managed decides a why's bound by against the sentences this package
// actually writes: every one of the four, built from the rule that writes it,
// is recognised, and the lines of the same two gates that are NOT a removal
// rule's sentence are not — because those carry the plan's own text and no
// identity, and the wider bound exists for the identity alone.
//
// A rule whose sentence stops being recognised is the dangerous direction: the
// why is then cut to safetext.FieldMax, in the middle of the identity its
// reader has to copy into `fugaro allow-delete`, which is an approval of
// nothing. A line that starts being recognised is the other: the plan's own
// text, echoed whole up to refusalWhyMax.
//
// Mutation (drop the marker phrase from one of the four sentences, or widen the
// predicate to every why of the two gates): the assertion for that sentence
// fails.
func TestRemovalRuleWhyRecognisesTheRemovalRulesSentences(t *testing.T) {
	listed := removal("google_storage_bucket", bucketBefore("fmgd-artifacts", "2026-01-02T03:04:05Z"))
	entries := []DeleteEntry{{Address: listed.Address, Before: "type=google_storage_bucket name=other"}}
	if ok, why := Approves(entries, listed); ok {
		t.Fatal("the fixture's entry approved the removal, so it has no sentence")
	} else if !RemovalRuleWhy(why) {
		t.Errorf("tf.Approves' sentence about an entry bound to another object is not recognised as a removal rule's, so internal/managed cuts it at FieldMax — in the middle of the identity it prints:\n%q", why)
	}

	// The same rule's other shape: a before view that identifies nothing.
	bare := removal("google_storage_bucket", map[string]any{"name": "fmgd-artifacts"})
	if ok, why := Approves([]DeleteEntry{{Address: bare.Address}}, bare); ok {
		t.Fatal("a before view that identifies nothing approved a removal")
	} else if !RemovalRuleWhy(why) {
		t.Errorf("tf.Approves' sentence about an unidentified removal is not recognised as a removal rule's:\n%q", why)
	}

	moved := removal("google_storage_bucket", bucketBefore("fmgd-artifacts", "2026-01-02T03:04:05Z"))
	moved.Change.Before = map[string]any{"name": "fmgd-artifacts", "id": "moved-1"}
	if why := movedRemovalReason(moved); !RemovalRuleWhy(why) {
		t.Errorf("movedRemovalReason's sentence is not recognised as a removal rule's:\n%q", why)
	}
	if why := notOnDeleteListReason(listed); !RemovalRuleWhy(why) {
		t.Errorf("the cover's address rule is not recognised as a removal rule's:\n%q", why)
	}

	// And the lines the same two gates write that are not those sentences: the
	// plan's own action_reason line, which is the one that was echoed whole,
	// and the three reasons the cover writes for a change it does not judge at
	// all.
	for _, why := range []string{
		"actions delete: action_reason noop, which the managed delete rule does not know",
		managedModeReason("data"),
		managedProviderReason("registry.terraform.io/hashicorp/random"),
		"no actions",
		"a resource " + strings.Repeat("x", 32) + " whose image the plan wrote",
	} {
		if RemovalRuleWhy(why) {
			t.Errorf("a why that is not a removal rule's sentence is recognised as one, so internal/managed carries it whole up to refusalWhyMax:\n%q", why)
		}
	}
}
