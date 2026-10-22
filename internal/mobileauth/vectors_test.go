package mobileauth

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

// written is the time the vectors' entries were written, so that a case's
// expectation does not depend on when the test runs: this package reads no
// clock, and neither does its testdata.
func written() time.Time { return time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC) }

// vectors is testdata/entries.json: the normalisation's cases, the resolution
// rule's cases and the node-key encoding's cases, as data. The file carries the
// accept cases and the refusals beside each other, because a refusal is the
// half of this package that admits nobody and it is worth reading in one place.
//
// It is data rather than a table in this file for the reason the tree's other
// vector files are: a case is a statement about the record, and a reader who
// wants to know what a role resolves to should read the cases rather than the
// code that runs them. The named tests in mobileauth_test.go are the ones the
// red step's mutations are proved against; the file here is the breadth.
type vectors struct {
	Normalize       []normalizeCase `json:"normalize"`
	NormalizeDomain []normalizeCase `json:"normalize_domain"`
	Resolve         []resolveCase   `json:"resolve"`
	Keys            []keyCase       `json:"keys"`
}

type normalizeCase struct {
	Name      string `json:"name"`
	In        string `json:"in"`
	Want      string `json:"want"`
	WantError bool   `json:"want_error"`
}

type resolveCase struct {
	Name      string  `json:"name"`
	Exact     []Entry `json:"exact"`
	Domains   []Entry `json:"domains"`
	Address   string  `json:"address"`
	Want      Role    `json:"want"`
	WantError string  `json:"want_error"`
}

type keyCase struct {
	Address string `json:"address"`
	Key     string `json:"key"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	data, err := os.ReadFile("testdata/entries.json")
	if err != nil {
		t.Fatalf("reading the vectors: %v", err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decoding the vectors: %v", err)
	}
	if len(v.Normalize) == 0 || len(v.NormalizeDomain) == 0 || len(v.Resolve) == 0 || len(v.Keys) == 0 {
		t.Fatal("the vectors file has an empty section: every section is required, because a section that silently empties is a check that stopped checking")
	}
	return v
}

// TestVectorsNormalize runs testdata/entries.json's normalisation cases: the
// one form two writers agree on, and every form this package refuses rather
// than folding into a guess.
func TestVectorsNormalize(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.Normalize {
		got, err := NormalizeEmail(c.In)
		if c.WantError {
			if err == nil {
				t.Errorf("%s: NormalizeEmail(%q) = %q, want a refusal", c.Name, c.In, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: NormalizeEmail(%q) = %v, want %q", c.Name, c.In, err, c.Want)
			continue
		}
		if got != c.Want {
			t.Errorf("%s: NormalizeEmail(%q) = %q, want %q", c.Name, c.In, got, c.Want)
		}
	}
}

// TestVectorsNormalizeDomain runs the domain cases: a domain entry is a whole
// domain, folded to lower case, and every wildcard, single label, trailing dot
// and numeric last label is refused.
func TestVectorsNormalizeDomain(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.NormalizeDomain {
		got, err := NormalizeDomain(c.In)
		if c.WantError {
			if err == nil {
				t.Errorf("%s: NormalizeDomain(%q) = %q, want a refusal", c.Name, c.In, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: NormalizeDomain(%q) = %v, want %q", c.Name, c.In, err, c.Want)
			continue
		}
		if got != c.Want {
			t.Errorf("%s: NormalizeDomain(%q) = %q, want %q", c.Name, c.In, got, c.Want)
		}
	}
}

// TestVectorsResolve runs testdata/entries.json's resolution cases, including
// every refusal: ErrNotAllowed for an address no entry admits, ErrAmbiguous for
// two entries of one kind that disagree.
func TestVectorsResolve(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.Resolve {
		role, err := Resolve(c.Exact, c.Domains, c.Address)
		switch c.WantError {
		case "":
			if err != nil {
				t.Errorf("%s: Resolve(%q) = %v, want %q", c.Name, c.Address, err, c.Want)
				continue
			}
			if role != c.Want {
				t.Errorf("%s: Resolve(%q) = %q, want %q", c.Name, c.Address, role, c.Want)
			}
		case "not_allowed":
			if !errors.Is(err, ErrNotAllowed) {
				t.Errorf("%s: Resolve(%q) = %q, %v; want ErrNotAllowed", c.Name, c.Address, role, err)
			}
		case "ambiguous":
			if !errors.Is(err, ErrAmbiguous) {
				t.Errorf("%s: Resolve(%q) = %q, %v; want ErrAmbiguous", c.Name, c.Address, role, err)
			}
		default:
			t.Errorf("%s: the vectors file names an error %q, which this test does not know", c.Name, c.WantError)
		}
		if err != nil && role != RoleNone {
			t.Errorf("%s: Resolve(%q) = %q beside %v, want the zero Role whenever it refuses", c.Name, c.Address, role, err)
		}
	}
}

// TestVectorsNodeKeys runs the key cases: the encoding is injective and the
// decode is its inverse, so a key read back from the node is the address the
// entry names.
func TestVectorsNodeKeys(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.Keys {
		got, err := EncodeNodeKey(c.Address)
		if err != nil {
			t.Errorf("%s: EncodeNodeKey = %v", c.Address, err)
			continue
		}
		if got != c.Key {
			t.Errorf("EncodeNodeKey(%q) = %q, want %q", c.Address, got, c.Key)
			continue
		}
		back, err := DecodeNodeKey(c.Key)
		if err != nil {
			t.Errorf("DecodeNodeKey(%q) = %v", c.Key, err)
			continue
		}
		if back != c.Address {
			t.Errorf("DecodeNodeKey(%q) = %q, want %q", c.Key, back, c.Address)
		}
	}
}
