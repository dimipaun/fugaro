package projectpolicy

import (
	"strings"
	"testing"
)

// binariesNode wraps a binaries block in the node this binary reads. The node
// is written raw, the way this package writes every fixture, and the version is
// the guard such a literal owes: a bump has to come here deliberately rather
// than leave the test reading a node no reader of this release accepts.
func binariesNode(t *testing.T, block string) []byte {
	t.Helper()
	if Version != 4 {
		t.Fatalf("the policy Version is %d, and this test's node is written for 4: update both together, deliberately", Version)
	}
	return []byte(`{"version": 4, "binaries": ` + block + `}`)
}

// TestDecodeRefusesFoldedBinariesMembers: the block's member names are held to
// the spelling the carrier documents, the way the budget block's and the
// credentials block's are, and this is the half a strict decode does not give.
// encoding/json folds a member name by Unicode simple case folding before it
// matches it, so `{"allow": ["safe/**"], "Allow": ["**"]}` has no unknown
// member in it at all: it decodes, the last spelling wins, and a node that
// means to admit one directory admits every binary — the failure D982 refuses
// for the patterns themselves, reached by the spelling of the key. A member
// written twice is the same rule one spelling at a time, and a member outside
// the documented names is refused rather than folded onto one.
func TestDecodeRefusesFoldedBinariesMembers(t *testing.T) {
	for _, tc := range []struct{ name, block, want string }{
		{"a case-folded allow beside the real one", `{"allow": ["safe/**"], "Allow": ["**"]}`, "reads as one member"},
		{"allow written twice", `{"allow": ["safe/**"], "allow": ["**"]}`, `names "allow" twice`},
		{"max_bytes spelled in another case", `{"allow": ["safe/**"], "Max_Bytes": 1024}`, `spells max_bytes as "Max_Bytes"`},
		{"a member this Fugaro does not read", `{"allow": ["safe/**"], "cap": 1024}`, "unknown field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode(binariesNode(t, tc.block))
			if err == nil || p != nil || !found {
				t.Fatalf("Decode(%s) = (%v, %v, %v), want a refusal: the node is not the one this reader would act on", tc.block, p, found, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestDecodeTellsTheWrittenBinariesListFromTheAbsentOne: the two spellings a
// reader past the publish needs to tell apart, because the runner reads them
// differently — a written `allow: []` is the owner's list cleared (D987) and
// comes back as an empty non-nil slice, an absent one is nil, and a node with
// no block at all carries no carrier. What the three have in common is the
// answer the check gives them: no path is admitted.
func TestDecodeTellsTheWrittenBinariesListFromTheAbsentOne(t *testing.T) {
	p, found, err := Decode(binariesNode(t, `{"allow": [], "max_bytes": 1024}`))
	if err != nil || !found || p == nil || p.Binaries == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the block read", p, found, err)
	}
	if got := p.Binaries; got.Allow == nil || len(got.Allow) != 0 || got.MaxBytes != 1024 {
		t.Errorf("the written empty list came back as %#v, want an empty non-nil allow and the cap", got)
	}

	p, found, err = Decode(binariesNode(t, `{"max_bytes": 1024}`))
	if err != nil || !found || p == nil || p.Binaries == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the block read", p, found, err)
	}
	if got := p.Binaries; got.Allow != nil || got.MaxBytes != 1024 {
		t.Errorf("the absent list came back as %#v, want a nil one", got)
	}

	// A written null and an absent member are one absence — no carrier at all —
	// and a written empty block is a carrier nothing is set in: the same state
	// at the check, and the spelling D987 reads as the list cleared at publish.
	if q, _, err := Decode(binariesNode(t, `null`)); err != nil || q.Binaries != nil {
		t.Errorf("a null block = (%v, %v), want no carrier", q, err)
	}
	if q, _, err := Decode(binariesNode(t, `{}`)); err != nil || q.Binaries == nil || q.Binaries.Allow != nil || q.Binaries.MaxBytes != 0 {
		t.Errorf("a written empty block = (%v, %v), want the empty carrier", q, err)
	}
}
