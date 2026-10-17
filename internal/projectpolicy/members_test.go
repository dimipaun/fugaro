package projectpolicy

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

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
