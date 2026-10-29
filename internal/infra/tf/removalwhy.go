package tf

import "strings"

// removalWhyMarker is the phrase every removal rule's own sentence carries and
// no other why this tree writes does: the rules that judge a change against the
// owner's delete list all say which list they read ("the owner's delete list"
// in three of them, "the delete list" in movedRemovalReason). It is what
// RemovalRuleWhy recognises those sentences by.
//
// A phrase of the RULE's words rather than of a value, deliberately: the
// predicate below decides how much of a why an operator's verdict carries (see
// internal/managed's refusalWhy), and matching on text a plan wrote would let a
// plan choose its own bound. The four sentences this tree writes — Approves'
// two (identity.go), movedRemovalReason (guard.go) and notOnDeleteListReason
// (managed_cover.go) — are pinned to it by
// TestRemovalRuleWhyRecognisesTheRemovalRulesSentences, which builds each of
// them from the rule that writes it.
//
// The residual it leaves, so a later reader does not have to find it: a VALUE a
// plan wrote that happened to carry this phrase — the cover's action_reason
// line, the image strings beside it — would be read as a removal rule's
// sentence and carried whole up to refusalWhyMax. It is a bound and not an
// injection either way: whatever is carried is safetext.FieldUncut's rewriting
// of the text, controls and all, and the one thing a phrase can widen is how
// much of it one refusal line holds.
const removalWhyMarker = "delete list"

// RemovalRuleWhy reports whether why is one of the removal rules' own
// sentences: the sentences whose reader has to be able to copy the identity of
// the object a plan removes out of them whole (Approves, movedRemovalReason,
// notOnDeleteListReason).
//
// Those are the only whys whose bound is wider than one safetext.Field, and the
// difference is the reason: safetext.Field cuts at FieldMax, which falls in the
// middle of an identity these sentences print, and an identity printed to a
// prefix is an approval of nothing — the owner records what the refusal showed,
// the entry matches no object, and every later apply refuses the same removal
// with the same sentence. Every other why — another gate's, and a line of these
// two gates that is not one of these sentences, such as the plan's own
// action_reason line — is plan-authored text with no identity in it, and one
// Field is the bound it had before the removal rules were let past it.
//
// The question is asked of the SENTENCE and not of the gate that wrote it: both
// gates that judge a change against the owner's delete list write lines that are
// not these sentences as well (internal/managed/plan.go's refusalWhy, the review
// finding that split the two apart).
func RemovalRuleWhy(why string) bool {
	return strings.Contains(why, removalWhyMarker)
}

// notOnDeleteListReason is the managed cover's own removal rule: an address that
// a removal names and the owner's delete list does not hold. It prints the
// identity of the object the plan removes beside the address, because the person
// who can approve it is the owner and this is where they read which object the
// entry has to name (RemoveIdentityText, D72/D74) — the sentence is one of the
// four RemovalRuleWhy recognises.
func notOnDeleteListReason(rc ResourceChange) string {
	return "an address that is not on the owner's delete list (the plan removes " + RemoveIdentityText(rc) + ")"
}
