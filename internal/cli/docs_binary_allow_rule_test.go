package cli

import (
	"strings"
	"testing"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The design folder for the binaries rule had no guard at all until the review
// of AR0: nothing in the tree read docs/binary-allow-rule/, so its first draft
// could keep naming an entry the shipped rule accepts — and the caller AR1 adds
// would have been written against a sixth pattern check, the second and
// stricter pattern language beside the one `rigor.areas` and `rebuild.paths`
// share that F8's count and F9 forbid (F15). The two tests below pin both
// halves of the correction, each against the shipped rule rather than against
// the page alone: a page reverted to the draft's claim fails, and so does a
// rule that grows the check the pages say it has not got.

// misplacedDoubleStar is the entry the design's first draft refused and the
// shipped rule accepts: `**` is "any depth" in doublestar only as a whole path
// segment, so inside one it matches like `*`.
const misplacedDoubleStar = "website/public/**.png"

// TestDocsBinaryAllowRuleStatesTheMisplacedDoubleStarAsAccepted: the entry is
// a readable pattern and a lock over exactly one level, the rule accepts it,
// and all three pages of the folder say so.
func TestDocsBinaryAllowRuleStatesTheMisplacedDoubleStarAsAccepted(t *testing.T) {
	if err := projectpolicy.ValidBinariesAllow([]string{misplacedDoubleStar}); err != nil {
		t.Fatalf("ValidBinariesAllow(%q) = %v: the pages pinned below state that the entry is a readable pattern, so a rule that refuses it is the sixth check F8's count and F9 forbid", misplacedDoubleStar, err)
	}
	if ok, err := doublestar.Match(misplacedDoubleStar, "website/public/a.png"); err != nil || !ok {
		t.Errorf("Match(%q, website/public/a.png) = (%v, %v), want a match: the entry is a lock over exactly one level", misplacedDoubleStar, ok, err)
	}
	if ok, err := doublestar.Match(misplacedDoubleStar, "website/public/deep/a.png"); err != nil || ok {
		t.Errorf("Match(%q, website/public/deep/a.png) = (%v, %v), want no match: `**` inside a path segment is not any depth, and the pages say so", misplacedDoubleStar, ok, err)
	}
	for _, tc := range []struct{ path, want string }{
		{
			"docs/binary-allow-rule/spec.md",
			"`website/public/**.png` is readable: `**` means \"any depth\" in doublestar only as a whole path segment",
		},
		{
			"docs/binary-allow-rule/spec.md",
			"refusing it would be a sixth check: a second, stricter pattern language beside the one `rigor.areas` and `rebuild.paths` already share, which F8's count and F9 forbid",
		},
		{
			"docs/binary-allow-rule/implementation-plan.md",
			"**`website/public/**.png` is not one of them, and this step's first draft had it first (F15):**",
		},
		{
			"docs/binary-allow-rule/implementation-plan.md",
			"**AR1 adds no check for it** — its caller is written against the rule that shipped",
		},
		{
			"docs/binary-allow-rule/reconciliation.md",
			"F15 — the design's first draft took a readable pattern for an unreadable one, and AR0's red step named it first.",
		},
		{
			"docs/binary-allow-rule/reconciliation.md",
			"shipped rule accepts it, AR0's `TestBinariesAllowTakesAMisplacedDoubleStar` pins the lock the owner actually gets",
		},
	} {
		if doc := flattenDoc(readDoc(t, tc.path)); !strings.Contains(doc, tc.want) {
			t.Errorf("%s does not say %q: the folder has to state what the shipped rule does with the entry, or the next task is written against the first draft's claim", tc.path, tc.want)
		}
	}
}

// TestDocsBinaryAllowRuleNamesTheEntriesTheRuleRefuses is the other half: the
// correction above moved one entry *out* of the refused set, and the set the
// red step does name has to stay the set the rule refuses — a page that dropped
// the case entirely rather than correcting it fails here, and so does a rule
// that stopped refusing one of them.
func TestDocsBinaryAllowRuleNamesTheEntriesTheRuleRefuses(t *testing.T) {
	for _, entry := range []string{"/etc/passwd", "../elsewhere/**", "", "brand/[.png"} {
		if err := projectpolicy.ValidBinariesAllow([]string{entry}); err == nil {
			t.Errorf("ValidBinariesAllow(%q) = nil, want a refusal: the red step pinned below names it as one the decoder refuses", entry)
		}
	}
	plan := flattenDoc(readDoc(t, "docs/binary-allow-rule/implementation-plan.md"))
	if want := "`/etc/passwd`, a `..` component, an empty entry and a pattern the matcher cannot read (`brand/[.png`) are all accepted by a decoder that has no such member, and the task exists to refuse them where they are written"; !strings.Contains(plan, want) {
		t.Errorf("AR0's red step no longer names the entries the decoder refuses (%q):\n%s", want, plan)
	}
}
