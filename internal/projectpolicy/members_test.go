package projectpolicy

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestMemberListsReadOnePersonEntries: the two lists a member's own admission
// is read from hold entries of every kind — a `domain:` an operator wrote by
// hand, the `group:` and `serviceAccount:` an apply would accept, the
// `allUsers` that means everybody — and only `user:<address>` names one person.
// The others are INERT and not a refusal: the person beside such an entry is
// still admitted, which is the whole reason the reader does not refuse a list it
// does not fully understand. An entry under `user:` that is not one address —
// whitespace inside it, a wildcard, no `@`, nothing at all — is inert the same
// way, and the two lists are one answer rather than two.
func TestMemberListsReadOnePersonEntries(t *testing.T) {
	lists := MemberListsOf(
		[]string{
			"user:person-1@example.com",
			"domain:example.com",
			"Domain:example.com",
			"group:eng@example.com",
			"serviceAccount:bot@proj-1234.iam.gserviceaccount.com",
			"allUsers",
			"allAuthenticatedUsers",
		},
		[]string{
			"serviceAccount:other@proj-1234.iam.gserviceaccount.com",
			"User:person-3@example.com",
			" user:person-4@example.com",
			"user:person-4@example.com ",
			"user:*@example.com",
			"user:person-5@example.com*",
			"user:",
			"user:person-6",
			"user:Person Seven <person-7@example.com>",
			"user:person-2@example.com",
		},
	)
	want := []string{"person-1@example.com", "person-2@example.com"}
	if got := lists.Addresses(); !slices.Equal(got, want) {
		t.Errorf("the two lists name %v, and the only entries that are one person are %v: an entry of another kind names no one, and an entry that is not one address admits nobody", got, want)
	}
}

// TestMemberListsHandsBackTheSpellingTheOperatorWrote: the addresses come back
// as the list wrote them, sorted and without an exact repeat, and the answer is
// a copy — a caller cannot write through it to the lists it was read from. The
// folding is the comparison's own (ee/coordinator/oauth's admission, with
// mobileauth's one rule), so it is deliberately not done here: the reader that
// decided which entries name a person would otherwise be deciding what an
// address is, which is the rule its reader owns.
func TestMemberListsHandsBackTheSpellingTheOperatorWrote(t *testing.T) {
	lists := MemberListsOf(
		[]string{"user:person-1@EXAMPLE.com", "user:person-1@EXAMPLE.com"},
		[]string{"user:person-1@example.com"},
	)
	want := []string{"person-1@EXAMPLE.com", "person-1@example.com"}
	if got := lists.Addresses(); !slices.Equal(got, want) {
		t.Fatalf("the lists name %v, and the two spellings are two entries: %v", got, want)
	}
	got := lists.Addresses()
	got[0] = "stranger@example.com"
	if after := lists.Addresses(); !slices.Equal(after, want) {
		t.Errorf("writing through the answer changed the lists: %v", after)
	}

	// A member with no lists at all names nobody, which is the empty answer and
	// not an error: it is a member nothing admits, and the admission that reads
	// it refuses every person rather than failing open.
	if got := MemberListsOf(nil, nil).Addresses(); len(got) != 0 {
		t.Errorf("a member with no lists names %v", got)
	}
}

// TestPolicyMemberListsCoverTheirStructs: the hand-written member-name lists
// the node's readers hand checkMemberNames — budgetMembers, priceMembers,
// priceTierMembers, routingMembers and routingClaudeMembers — are the only
// names that check can speak about. It refuses an exact repeat, two names that
// fold to one, and a modelled name spelled in a folding of itself; a name the
// list does not carry is, to it, a key nothing looks at, while encoding/json
// still folds that name onto the field it answers to. So a field added to one
// of these structs without a line in its list reopens exactly the hole this
// package documents having shipped once: a rate written INPUT_PER_M beside
// input_per_m, priced by whichever spelling the document wrote last, and no
// check made. The reflection-derived providerMembers cannot have that problem;
// these five can, and until this test nothing pinned them (the review finding
// on this change).
//
// The comparison is set-for-set over the structs' own json tags — the spelling
// the node writes, and the spelling checkMemberNames matches against — so a
// list that is missing a field and a list that carries a name no field answers
// to both fail, each naming the difference in both directions.
func TestPolicyMemberListsCoverTheirStructs(t *testing.T) {
	for _, tc := range []struct {
		list []string
		typ  reflect.Type
		what string
	}{
		{budgetMembers, reflect.TypeOf(Budget{}), "budgetMembers covers Budget"},
		{priceMembers, reflect.TypeOf(Price{}), "priceMembers covers Price"},
		{priceTierMembers, reflect.TypeOf(PriceTier{}), "priceTierMembers covers PriceTier"},
		{routingMembers, reflect.TypeOf(config.Routing{}), "routingMembers covers config.Routing"},
		{routingClaudeMembers, reflect.TypeOf(config.RoutingClaude{}), "routingClaudeMembers covers config.RoutingClaude"},
	} {
		var fields []string
		for i := range tc.typ.NumField() {
			f := tc.typ.Field(i)
			if !f.IsExported() {
				continue
			}
			tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if tag == "" {
				tag = f.Name
			}
			fields = append(fields, tag)
		}
		want := slices.Clone(tc.list)
		slices.Sort(fields)
		slices.Sort(want)
		if slices.Equal(fields, want) {
			continue
		}
		var missing, extra []string
		for _, f := range fields {
			if !slices.Contains(want, f) {
				missing = append(missing, f)
			}
		}
		for _, w := range want {
			if !slices.Contains(fields, w) {
				extra = append(extra, w)
			}
		}
		t.Errorf("%s: the struct's json members are %v and the list is %v — a field the list does not name is a spelling checkMemberNames cannot refuse (missing from the list: %v; named by the list and not by the struct: %v)", tc.what, fields, want, missing, extra)
	}
	// The two aliases the routing lists describe are internal/config's own
	// objects, not respellings, so a field config adds arrives here too: this
	// is what makes the pin above worth having on the switch rather than only
	// on the policy's own blocks.
	if reflect.TypeOf(Routing{}) != reflect.TypeOf(config.Routing{}) {
		t.Errorf("projectpolicy.Routing is no longer config.Routing: the routing member list would then describe one struct while the node decodes another")
	}
}
