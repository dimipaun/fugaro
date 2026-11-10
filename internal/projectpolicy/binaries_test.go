package projectpolicy

import (
	"slices"
	"strings"
	"testing"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// binariesNode is a policy node carrying a binaries block, written at the
// version this Fugaro reads. The version is interpolated from the constant and
// never typed: a fixture that pinned a number would fail for the bump rather
// than for the block, and these tests are about the block.
func binariesNode(block string) []byte {
	return []byte(`{"version": ` + itoa(Version) + `, "binaries": ` + block + `}`)
}

// TestBinariesAllowPatternsAreRefusedAtDecode is this task's red step and the
// rule spec.md §2.1 states: an allow entry is a doublestar path glob relative
// to the repository root, and one that is not is refused where it is written
// rather than read as a rule that admits something other than what the owner
// believed. The four checks are internal/config's rebuild.paths rule
// (validateRebuild) and the same four internal/rigor's Area.Validate applies,
// in the same order and with the same words — one pattern rule in this tree
// (F9), not a second spelling of it.
//
// **One entry the plan's red step names is not refused here, and that is not
// an omission.** `website/public/**.png` is a pattern doublestar's validator
// accepts: `**` is "any depth" only when it is a whole path segment, and
// inside one it matches like `*`, so that entry is a lock that admits
// `website/public/a.png` and not `website/public/deep/a.png`. Refusing it
// would be a fifth pattern check, which F8 counts and F9 forbids; the case is
// pinned as accepted, and as narrow, by
// TestBinariesAllowTakesAMisplacedDoubleStar. The plan's red step and spec.md
// §2.1 both give it as refused, and the PR reports the disagreement.
func TestBinariesAllowPatternsAreRefusedAtDecode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry string
		want  string
	}{
		{"an absolute path", "/etc/passwd", "relative to the repository root"},
		{"a .. component", "../elsewhere/**", "must not contain a .."},
		{"a .. that is the whole entry", "..", "must not contain a .."},
		{"a .. in the middle", "website/public/../../etc/**", "must not contain a .."},
		{"an empty entry", "", "must not be empty"},
		{"whitespace alone", "   ", "must not be empty"},
		{"a pattern the matcher cannot read", "brand/[.png", "not a valid glob pattern"},
		{"an unterminated character class", "brand/[", "not a valid glob pattern"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode(binariesNode(`{"allow": ["` + tc.entry + `"]}`))
			if err == nil {
				t.Fatalf("Decode accepted %q as an allow entry, so a project that wrote it would be bounded by a rule it did not state: p = %+v", tc.entry, p)
			}
			if p != nil {
				t.Errorf("Decode returned a policy beside the refusal: %+v", p)
			}
			if !found {
				t.Error("found must be true: the node exists and cannot be used (fail closed)")
			}
			if !strings.Contains(err.Error(), "binaries.allow[0]") {
				t.Errorf("%v does not name the entry: an operator with a hundred entries has to be told which one to fix", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%v does not say %q, the reason the entry is refused", err, tc.want)
			}
		})
	}
	// The refusal is the entry, not the block: the same block with a readable
	// pattern decodes, so a rule that refused every list would fail here.
	p, found, err := Decode(binariesNode(`{"allow": ["website/public/**", "brand/*.png"]}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode(a readable list) = (%v, %v, %v), want the node read", p, found, err)
	}
	if want := []string{"website/public/**", "brand/*.png"}; !slices.Equal(p.BinariesAllow(), want) {
		t.Errorf("BinariesAllow() = %v, want %v", p.BinariesAllow(), want)
	}
}

// TestBinariesAllowTakesAMisplacedDoubleStar pins what the pattern rule does
// *not* refuse, so a later change that adds a fifth check has to change this
// test on purpose rather than quietly widening or narrowing the rule.
//
// `**` means "any depth" only as a whole path segment; `**.png` is two `*`
// inside one segment and matches exactly one. The design keeps doublestar's
// own semantics rather than a second, stricter one (D982, F9), so the entry is
// accepted and the test states the lock the owner actually gets.
func TestBinariesAllowTakesAMisplacedDoubleStar(t *testing.T) {
	const entry = "website/public/**.png"
	if !doublestar.ValidatePattern(entry) {
		t.Fatalf("doublestar refuses %q, so the entry is not the case this test is about", entry)
	}
	p, found, err := Decode(binariesNode(`{"allow": ["` + entry + `"]}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the entry accepted as the matcher reads it", p, found, err)
	}
	if want := []string{entry}; !slices.Equal(p.BinariesAllow(), want) {
		t.Fatalf("BinariesAllow() = %v, want %v", p.BinariesAllow(), want)
	}
	if ok, err := doublestar.Match(entry, "website/public/a.png"); err != nil || !ok {
		t.Errorf("Match(%q, one level down) = (%v, %v), want a match: the entry is a one-segment lock", entry, ok, err)
	}
	if ok, err := doublestar.Match(entry, "website/public/deep/a.png"); err != nil || ok {
		t.Errorf("Match(%q, two levels down) = (%v, %v), want no match: `**` inside a segment is not any depth, and the owner who wrote this got a narrower lock than the words suggest", entry, ok, err)
	}
}

// TestAnOverLongBinariesPatternIsRefused is the fifth check of spec.md §2.1
// made a test rather than a sentence: the four rebuild.paths checks are
// shared, and the length bound is the one the spec's first draft left out of
// its count (F8). An entry reaches the run record and the pull request's
// status line, so an unbounded one is unbounded text on a wire.
func TestAnOverLongBinariesPatternIsRefused(t *testing.T) {
	// A pattern of exactly the bound is readable and admitted: the check is a
	// bound, not a rejection of long paths.
	atBound := strings.Repeat("a", rigor.MaxAreaPattern)
	if len(atBound) != rigor.MaxAreaPattern {
		t.Fatalf("the fixture is %d characters, want the bound %d", len(atBound), rigor.MaxAreaPattern)
	}
	if p, found, err := Decode(binariesNode(`{"allow": ["` + atBound + `"]}`)); err != nil || !found || p == nil {
		t.Fatalf("Decode(a pattern of exactly %d characters) = (%v, %v, %v), want it admitted", rigor.MaxAreaPattern, p, found, err)
	}
	over := atBound + "a"
	p, found, err := Decode(binariesNode(`{"allow": ["` + over + `"]}`))
	if err == nil {
		t.Fatalf("Decode accepted a %d-character entry: an entry is copied into the run record and the pull request's status line, so its length is bounded where it is written", len(over))
	}
	if p != nil || !found {
		t.Errorf("Decode = (%+v, %v), want no policy and found true", p, found)
	}
	if !strings.Contains(err.Error(), "binaries.allow[0]") || !strings.Contains(err.Error(), "at most") {
		t.Errorf("%v does not name the entry and the bound it broke", err)
	}
}

// TestBinariesAllowIsBoundedInCount is the other half of the bound: the list
// itself is bounded, the way rigor.MaxAreas bounds a layer's areas, because
// the matched subset of it is what the run record and the pull request carry.
func TestBinariesAllowIsBoundedInCount(t *testing.T) {
	entry := func(i int) string { return `"dir` + itoa(i) + `/*.png"` }
	list := func(n int) string {
		entries := make([]string, n)
		for i := range entries {
			entries[i] = entry(i)
		}
		return `{"allow": [` + strings.Join(entries, ",") + `]}`
	}
	if p, found, err := Decode(binariesNode(list(rigor.MaxAreas))); err != nil || !found || p == nil {
		t.Fatalf("Decode(a list of exactly %d entries) = (%v, %v, %v), want it admitted", rigor.MaxAreas, p, found, err)
	}
	p, found, err := Decode(binariesNode(list(rigor.MaxAreas + 1)))
	if err == nil {
		t.Fatalf("Decode accepted %d entries, want a refusal at %d", rigor.MaxAreas+1, rigor.MaxAreas)
	}
	if p != nil || !found {
		t.Errorf("Decode = (%+v, %v), want no policy and found true", p, found)
	}
	if !strings.Contains(err.Error(), "at most") {
		t.Errorf("%v does not state the bound the list broke", err)
	}
}

// TestBinariesAbsentAdmitsNothing: a project that publishes no binaries block
// admits no binary at all, which is the behaviour of every node written before
// this member existed — byte for byte, since the publisher writes no member
// for a project that sets none. There is no fallback and no default pattern.
func TestBinariesAbsentAdmitsNothing(t *testing.T) {
	p, found, err := Decode([]byte(`{"version": ` + itoa(Version) + `}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
	}
	if p.Binaries != nil {
		t.Errorf("Binaries = %+v, want nil: the member is absent and the publisher writes none", p.Binaries)
	}
	if got := p.BinariesAllow(); len(got) != 0 {
		t.Errorf("BinariesAllow() = %v, want nothing admitted", got)
	}
	// The cap of a project that admits nothing is still the shipped default:
	// the question "how large may an admitted binary be" is answered the same
	// way whether or not a list is written, and AR3 reads it as one value.
	if got := p.BinariesMaxBytes(); got != DefaultBinariesMaxBytes {
		t.Errorf("BinariesMaxBytes() = %d, want the default %d", got, DefaultBinariesMaxBytes)
	}
	// The zero policy and a nil one answer the same way, so a caller that
	// holds no node needs no branch of its own.
	if got := (*Policy)(nil).BinariesAllow(); len(got) != 0 {
		t.Errorf("a nil policy admits %v, want nothing", got)
	}
	if got := (*Policy)(nil).BinariesMaxBytes(); got != DefaultBinariesMaxBytes {
		t.Errorf("a nil policy's cap = %d, want the default %d", got, DefaultBinariesMaxBytes)
	}
}

// TestBinariesWrittenEmptyAdmitsNothing: the two spellings of an empty block
// admit nothing at admission time, which is the whole answer a run reads, and
// Decode keeps the block instead of folding it onto the absent member because
// what it reads is a *document* and a document can say `binaries: {allow: []}`
// — D987's spelling for taking an admission away on purpose, where silence in
// the local file keeps the node's list.
//
// A node is narrower than a document, and the difference is the database's
// rather than this decoder's: it stores no member for an empty object or an
// empty list, so `{}` and `{"allow": []}` are shapes a published node does not
// carry and no read of one returns (F16). What a node's member presence does
// say is that the node carries a block — a list, or a cap with no list — and
// that is the distinction the pointer keeps.
func TestBinariesWrittenEmptyAdmitsNothing(t *testing.T) {
	for _, block := range []string{`{}`, `{"allow": []}`, `{"allow": [], "max_bytes": 1024}`} {
		t.Run(block, func(t *testing.T) {
			p, found, err := Decode(binariesNode(block))
			if err != nil || !found || p == nil {
				t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
			}
			if got := p.BinariesAllow(); len(got) != 0 {
				t.Errorf("BinariesAllow() = %v, want nothing admitted", got)
			}
			if p.Binaries == nil {
				t.Error("Binaries = nil, and the document wrote a block: the publisher cannot tell a written empty block (clear the list) from a silent file (keep it) if this decoder folds the two together")
			}
		})
	}
}

// TestBinariesMaxBytesDefaultsAndLowerOnly is D983's rule: the cap is 2 MiB
// when the block is present and the key is absent, and a project may only
// lower it. A bound, not a policy.
//
// A written null, 0 or a negative value is refused rather than read as the
// default: those are the values encoding/json turns into a zero, and a cap
// that reads as "unset" is a cap nobody can see — the same reading
// route_fee_pct's own decode takes.
func TestBinariesMaxBytesDefaultsAndLowerOnly(t *testing.T) {
	if DefaultBinariesMaxBytes != 2*1024*1024 {
		t.Fatalf("DefaultBinariesMaxBytes = %d, want 2 MiB (2097152): D983 ships 2 MiB, above the largest asset this repository tracks", DefaultBinariesMaxBytes)
	}
	for _, tc := range []struct {
		name  string
		block string
		want  int64
	}{
		{"absent", `{"allow": ["brand/*.png"]}`, DefaultBinariesMaxBytes},
		{"the default written out", `{"allow": ["brand/*.png"], "max_bytes": 2097152}`, DefaultBinariesMaxBytes},
		{"lowered", `{"allow": ["brand/*.png"], "max_bytes": 1048576}`, 1048576},
		{"one byte", `{"allow": ["brand/*.png"], "max_bytes": 1}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode(binariesNode(tc.block))
			if err != nil || !found || p == nil {
				t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
			}
			if got := p.BinariesMaxBytes(); got != tc.want {
				t.Errorf("BinariesMaxBytes() = %d, want %d", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		block string
		want  string
	}{
		{"above the ceiling", `{"allow": ["brand/*.png"], "max_bytes": 2097153}`, "at most"},
		{"zero", `{"allow": ["brand/*.png"], "max_bytes": 0}`, "greater than 0"},
		{"negative", `{"allow": ["brand/*.png"], "max_bytes": -1}`, "greater than 0"},
		{"null", `{"allow": ["brand/*.png"], "max_bytes": null}`, "null"},
		{"a string", `{"allow": ["brand/*.png"], "max_bytes": "2097152"}`, "max_bytes"},
		{"a float", `{"allow": ["brand/*.png"], "max_bytes": 1.5}`, "max_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode(binariesNode(tc.block))
			if err == nil {
				t.Fatalf("Decode accepted %s as the cap: a project may only lower the 2 MiB bound, and a value that reads as unset is a cap nobody can see", tc.block)
			}
			if p != nil || !found {
				t.Errorf("Decode = (%+v, %v), want no policy and found true", p, found)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%v does not say %q", err, tc.want)
			}
		})
	}
}

// TestBinariesBlockRefusesAMemberItDoesNotRead: the block is decoded with the
// strictness every block of this node is — an unknown member, a member
// spelled in another case and a member written twice are refused rather than
// dropped, because a misspelt `allow` is a list that admits nothing and a
// misspelt `max_bytes` is a cap that reads as the default. Neither is visible
// in a decoded policy.
func TestBinariesBlockRefusesAMemberItDoesNotRead(t *testing.T) {
	for _, tc := range []struct{ name, block string }{
		{"an unknown member", `{"allow": [], "max_size": 1}`},
		{"a case-folded member", `{"allow": [], "Max_Bytes": 1}`},
		{"a member written twice", `{"allow": ["brand/*.png"], "allow": ["website/public/**"]}`},
		{"not an object", `[]`},
		{"a bare string", `"brand/*.png"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode(binariesNode(tc.block))
			if err == nil {
				t.Fatalf("Decode accepted %s, want a refusal: the block is read strictly like every other block of this node", tc.block)
			}
			if p != nil || !found {
				t.Errorf("Decode = (%+v, %v), want no policy and found true", p, found)
			}
			if !strings.Contains(err.Error(), "binaries") {
				t.Errorf("%v does not name the block", err)
			}
		})
	}
}
