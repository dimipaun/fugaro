package infra

import "testing"

func TestNormalizeRulesKeepsLiterals(t *testing.T) {
	same := [][2]string{
		{"a  b // c\n d", "a b d"},
		{"a /* x */ b", "a b"},
		{`x == "a//b" // c`, `x ==   "a//b"`},
		{`x == 'a /* b */'`, "x == 'a /* b */'\r\n"},
	}
	for _, p := range same {
		if normalizeRules(p[0]) != normalizeRules(p[1]) {
			t.Errorf("%q != %q: %q vs %q", p[0], p[1], normalizeRules(p[0]), normalizeRules(p[1]))
		}
	}
	diff := [][2]string{
		{`x == "a//b"`, `x == "a"`},
		{`x == "a  b"`, `x == "a b"`},
		{`allow read: if true;`, `allow read: if false;`},
		{"ab", "a b"},
	}
	for _, p := range diff {
		if normalizeRules(p[0]) == normalizeRules(p[1]) {
			t.Errorf("%q == %q", p[0], p[1])
		}
	}
}
