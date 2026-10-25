package rigor

import (
	"strings"
	"testing"
)

// TestAreaValidateAcceptsTheShapeD20Names pins the rule D20c describes: a
// name, a doublestar glob and a mode, all three there and all three usable.
func TestAreaValidateAcceptsTheShapeD20Names(t *testing.T) {
	for _, a := range []Area{
		{Name: "payments", Match: "internal/payments/**", Mode: Critical},
		{Name: "admin UI", Match: "web/admin/*.tsx", Mode: Startup},
		{Name: "poc-spike", Match: "**/*.spike.go", Mode: POC},
		{Name: "one", Match: "a", Mode: Pro},
		{Name: "ledger", Match: "internal/ledger/**", Mode: Hotfix},
		{Name: strings.Repeat("n", MaxAreaName), Match: strings.Repeat("a", MaxAreaPattern), Mode: Pro},
	} {
		if msg := a.Validate(); msg != "" {
			t.Errorf("%+v: Validate() = %q; want ok", a, msg)
		}
	}
}

// TestAreaValidateRefuses pins every load-time refusal, one per case, so a
// rule that the resolver could not use is a configuration error rather than
// a rule that silently never fires (D20c). Each case states the reason in
// its name; the message is checked only for naming the broken piece.
func TestAreaValidateRefuses(t *testing.T) {
	cases := []struct {
		name string
		area Area
		part string // a word the message must hold
	}{
		{"no name", Area{Match: "internal/**", Mode: Pro}, "name"},
		{"empty name", Area{Name: "", Match: "internal/**", Mode: Pro}, "name"},
		{"name with a newline", Area{Name: "pay\nments", Match: "a", Mode: Pro}, "name"},
		{"name with a control character", Area{Name: "pay\x1bments", Match: "a", Mode: Pro}, "name"},
		{"name with a bidi override", Area{Name: "pay‮ments", Match: "a", Mode: Pro}, "name"},
		{"name starting with a space", Area{Name: " payments", Match: "a", Mode: Pro}, "name"},
		{"name with a slash", Area{Name: "payments/ledger", Match: "a", Mode: Pro}, "name"},
		{"name too long", Area{Name: strings.Repeat("n", MaxAreaName+1), Match: "a", Mode: Pro}, "name"},
		{"no match", Area{Name: "payments", Mode: Pro}, "match"},
		{"match too long", Area{Name: "payments", Match: strings.Repeat("a", MaxAreaPattern+1), Mode: Pro}, "match"},
		{"absolute match", Area{Name: "payments", Match: "/internal/**", Mode: Pro}, "relative"},
		{"match with a ..", Area{Name: "payments", Match: "../elsewhere/**", Mode: Pro}, ".."},
		{"match with a .. in the middle", Area{Name: "payments", Match: "internal/../../etc/**", Mode: Pro}, ".."},
		{"match doublestar rejects", Area{Name: "payments", Match: "internal/[", Mode: Pro}, "glob"},
		{"match with an unclosed brace", Area{Name: "payments", Match: "internal/{a,b", Mode: Pro}, "glob"},
		{"no mode", Area{Name: "payments", Match: "internal/**"}, "mode"},
		{"mode with the wrong case", Area{Name: "payments", Match: "internal/**", Mode: "Critical"}, "mode"},
		{"mode outside the vocabulary", Area{Name: "payments", Match: "internal/**", Mode: "strict"}, "mode"},
	}
	for _, c := range cases {
		msg := c.area.Validate()
		if msg == "" {
			t.Errorf("%s: Validate() = ok; want refused", c.name)
			continue
		}
		if !strings.Contains(msg, c.part) {
			t.Errorf("%s: Validate() = %q; want a message naming %q", c.name, msg, c.part)
		}
	}
}

// TestAreaValidateRefusesAWhitespaceOnlyMatch: a match of spaces is the
// missing-match mistake written with a space bar, and doublestar accepts it —
// the pattern is a legal glob that matches nothing a repository holds. So the
// one rule that can refuse it is this one, and it refuses it as a missing
// match rather than as a bad glob: what the rule needs is a pattern, and a
// value with no pattern in it is the same mistake as an empty one (D20c).
func TestAreaValidateRefusesAWhitespaceOnlyMatch(t *testing.T) {
	for _, match := range []string{" ", "   ", "\t", " \t "} {
		msg := Area{Name: "payments", Match: match, Mode: Pro}.Validate()
		if !strings.HasPrefix(msg, "must have a match") {
			t.Errorf("Area{Match: %q}.Validate() = %q; want the missing-match refusal, not the glob one", match, msg)
		}
	}
}

// TestAreaValidateSaysWhyTheNameIsRequired pins the distinct refusal for a
// rule that wrote no name at all. It is not the shape message: a missing name
// is refused for the reason the name exists — it is what names the rule to
// the person reading the pull request it governed — and a rule that gets the
// generic shape message instead has lost that explanation.
func TestAreaValidateSaysWhyTheNameIsRequired(t *testing.T) {
	msg := Area{Match: "internal/**", Mode: Pro}.Validate()
	if !strings.HasPrefix(msg, "must have a name") {
		t.Errorf("Validate() = %q; want the missing-name refusal, not the shape one", msg)
	}
	if !strings.Contains(msg, "decided the mode") {
		t.Errorf("Validate() = %q; want it to say what the name is for", msg)
	}
}

// TestAreaValidateNamesTheVocabulary pins that the refusal for a bad mode is
// one a reader can act on: it lists every mode the resolver knows, so a
// misspelt one is answered with the set it should have been drawn from.
func TestAreaValidateNamesTheVocabulary(t *testing.T) {
	msg := Area{Name: "payments", Match: "internal/**", Mode: "Poc"}.Validate()
	for _, m := range Modes {
		if !strings.Contains(msg, string(m)) {
			t.Errorf("the mode refusal %q does not name %q", msg, m)
		}
	}
}
