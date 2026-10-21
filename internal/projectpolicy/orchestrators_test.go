package projectpolicy

import (
	"fmt"
	"strings"
	"testing"
)

// orchestratorsNode is a policy node carrying the orchestrators block under
// test, with the minimum the decoder otherwise needs. An empty block is left
// out of the document, which is the way a node says it carries none.
func orchestratorsNode(block string) string {
	body := `"version":` + itoa(Version) + `,"budget":{"mode":"enforce"},"allow_repos":["github/acme/app"]`
	if block != "" {
		body += `,"orchestrators":` + block
	}
	return "{" + body + "}"
}

// TestOrchestratorsBlockStrictDecode: an orchestrator entry carries the two
// values D12's `add` has to be able to name — the identities admitted and the
// cap — so a key inside it that this Fugaro does not read is not a dropped
// field but an enforced one gone quiet. A misspelt `cap_usd` decodes to the
// zero cap and reads as "no ceiling", which is the one direction a cap must
// never move; a misspelt `allow` decodes to the nil slice and reads as "nobody
// is admitted", which is indistinguishable from an entry whose identities were
// dropped.
//
// The cases are the ones the accounts and profiles blocks are held against: an
// unknown key (DisallowUnknownFields, with the member-spelling check refusing
// it a second time), a member spelled in another case (encoding/json folds
// case, so "Cap_USD" answers to cap_usd while every check that reads the raw
// document looks up the documented spelling), and a member or a block key
// written twice (the decoder keeps the last one it reads).
//
// Two mutations prove the two mechanisms, and they are measured rather than
// assumed. Taking spelledMember out of the member loop (`known := member`) lets
// the case-folded entry through. Taking the strict decode out alone lets
// nothing through — checkOrchestratorMemberSpelling refuses the unknown name
// too, which is the second door accounts.go has for the same reason — and
// taking that loop and the strict decode out *together* lets both the
// case-folded and the unknown-key entries through, which is the mutation this
// pair of doors exists to refuse.
func TestOrchestratorsBlockStrictDecode(t *testing.T) {
	const good = `{"shepherd":{"cap_usd":25,"allow":["alice-uid"]}}`
	for name, tc := range map[string]struct{ block, want string }{
		"an unknown key inside an entry": {
			`{"shepherd":{"cap_usd":25,"cap":25,"allow":["alice-uid"]}}`,
			"orchestrators.shepherd",
		},
		"a case-folded member": {
			`{"shepherd":{"Cap_USD":25,"allow":["alice-uid"]}}`,
			"write cap_usd",
		},
		"a member written twice": {
			`{"shepherd":{"cap_usd":25,"cap_usd":5,"allow":["alice-uid"]}}`,
			"names \"cap_usd\" twice",
		},
		"an orchestrator written twice": {
			`{"shepherd":{"cap_usd":25,"allow":["alice-uid"]},"shepherd":{"cap_usd":5,"allow":["bob-uid"]}}`,
			"names \"shepherd\" twice",
		},
		"a block that is not an object":  {`5`, "orchestrators"},
		"an entry that is not an object": {`{"shepherd":["alice-uid"]}`, "orchestrators.shepherd"},
		"a null entry":                   {`{"shepherd":null}`, "orchestrators.shepherd"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Decode([]byte(orchestratorsNode(tc.block))); err == nil {
				t.Fatalf("Decode accepted %s", tc.block)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
		})
	}

	// And the documented spelling decodes, whole, with the block read as a
	// known key rather than filed under UnknownKeys: a reader that dropped it
	// there would lose the cap an orchestrator's jobs are bounded by while the
	// identities beside it still read as admitted.
	p, err := decodeNode(t, orchestratorsNode(good))
	if err != nil {
		t.Fatal(err)
	}
	o, ok := p.OrchestratorFor("shepherd")
	if !ok || o.CapUSD != 25 || len(o.Allow) != 1 || o.Allow[0] != "alice-uid" {
		t.Fatalf("orchestrators.shepherd = %+v (present %v)", o, ok)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("the orchestrators block was tolerated as unknown: %v", p.UnknownKeys)
	}
	if names := p.OrchestratorNames(); len(names) != 1 || names[0] != "shepherd" {
		t.Errorf("OrchestratorNames = %v", names)
	}
	if !p.AdmittedTo("shepherd", "alice-uid") || p.AdmittedTo("shepherd", "bob-uid") || p.AdmittedTo("nobody", "alice-uid") {
		t.Errorf("AdmittedTo reads %v wrong: it must be exactly the entry's list, and an orchestrator the block does not name admits nobody", o.Allow)
	}
	// An empty block defines nothing and is legal: it is a node that runs no
	// central orchestrator, not one whose block went missing.
	p, err = decodeNode(t, orchestratorsNode(`{}`))
	if err != nil || len(p.Orchestrators) != 0 {
		t.Fatalf("an empty orchestrators block: %+v, %v", p, err)
	}
	// An unknown top-level key beside it keeps the P8 tolerance.
	p, err = decodeNode(t, orchestratorsNode(good)[:len(orchestratorsNode(good))-1]+`,"future_key":1}`)
	if err != nil || len(p.UnknownKeys) != 1 || p.UnknownKeys[0] != "future_key" {
		t.Fatalf("a node with an unknown top-level key: %+v, %v", p, err)
	}
}

// TestOrchestratorEntryValuesAreChecked: every value in the entry is one the
// command that writes it also refuses, and the refusals are here because the
// node is the document a service reads rather than the CLI's private copy — a
// policy published by an older release, or written by hand, is read by this
// one.
//
// Each case is a value that survives the strict decode and means something
// other than what it looks like: a cap of zero or a negative one (a job nobody
// bounded), a cap below the smallest unit the spend ledger holds (rounds to
// nothing), no identities at all (an entry about nothing, which is why `rm`
// removes the entry instead), a uid the database cannot key (an identity this
// project could publish and never administer), and a list that is not sorted
// (two publishes of the same set would be two different documents).
func TestOrchestratorEntryValuesAreChecked(t *testing.T) {
	for name, tc := range map[string]struct{ block, want string }{
		"no cap": {
			`{"shepherd":{"allow":["alice-uid"]}}`,
			"must name the ceiling",
		},
		"a zero cap": {
			`{"shepherd":{"cap_usd":0,"allow":["alice-uid"]}}`,
			"must name the ceiling",
		},
		"a negative cap": {
			`{"shepherd":{"cap_usd":-1,"allow":["alice-uid"]}}`,
			"must name the ceiling",
		},
		"a cap that rounds to nothing": {
			`{"shepherd":{"cap_usd":0.0000001,"allow":["alice-uid"]}}`,
			"rounds to nothing",
		},
		"a cap above the largest amount": {
			`{"shepherd":{"cap_usd":100001,"allow":["alice-uid"]}}`,
			"not a number from 0",
		},
		"no identities": {
			`{"shepherd":{"cap_usd":25}}`,
			"must name at least one identity",
		},
		"an empty identity list": {
			`{"shepherd":{"cap_usd":25,"allow":[]}}`,
			"must name at least one identity",
		},
		"an empty uid": {
			`{"shepherd":{"cap_usd":25,"allow":[""]}}`,
			"must not be empty",
		},
		"a uid with a dot in it": {
			`{"shepherd":{"cap_usd":25,"allow":["alice.uid"]}}`,
			`carries ".", which the database refuses in a key`,
		},
		"a uid with a slash in it": {
			`{"shepherd":{"cap_usd":25,"allow":["alice/uid"]}}`,
			"the database refuses in a key",
		},
		"a uid written twice": {
			`{"shepherd":{"cap_usd":25,"allow":["alice-uid","alice-uid"]}}`,
			"is listed twice",
		},
		"an unsorted identity list": {
			`{"shepherd":{"cap_usd":25,"allow":["bob-uid","alice-uid"]}}`,
			"must be sorted",
		},
		"a name that is not a slug": {
			`{"Shepherd One":{"cap_usd":25,"allow":["alice-uid"]}}`,
			"central orchestrator name",
		},
		"an empty name": {
			`{"":{"cap_usd":25,"allow":["alice-uid"]}}`,
			"must not be empty",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Decode([]byte(orchestratorsNode(tc.block))); err == nil {
				t.Fatalf("Decode accepted %s", tc.block)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
		})
	}

	// The bounds themselves, and they are checked at the boundary and one past
	// it: a node is not made to cost an unbounded read.
	uids := make([]string, 0, MaxOrchestratorAllow)
	for i := range MaxOrchestratorAllow {
		uids = append(uids, fmt.Sprintf("u%03d", i))
	}
	p, err := decodeNode(t, orchestratorsNode(`{"shepherd":{"cap_usd":25,"allow":["`+strings.Join(uids, `","`)+`"]}}`))
	if err != nil || len(p.Orchestrators["shepherd"].Allow) != MaxOrchestratorAllow {
		t.Fatalf("%d identities: %v", MaxOrchestratorAllow, err)
	}
	if _, err := decodeNode(t, orchestratorsNode(`{"shepherd":{"cap_usd":25,"allow":["`+strings.Join(append(uids, "u999"), `","`)+`"]}}`)); err == nil {
		t.Errorf("Decode accepted %d identities, past MaxOrchestratorAllow (%d)", MaxOrchestratorAllow+1, MaxOrchestratorAllow)
	}
	if err := ValidOrchestratorUID(strings.Repeat("u", MaxUIDLength)); err != nil {
		t.Errorf("a %d-character uid: %v, want it accepted", MaxUIDLength, err)
	}
	if err := ValidOrchestratorUID(strings.Repeat("u", MaxUIDLength+1)); err == nil {
		t.Errorf("ValidOrchestratorUID accepted %d characters, past MaxUIDLength (%d)", MaxUIDLength+1, MaxUIDLength)
	}
	if err := ValidOrchestratorName(strings.Repeat("a", 40)); err != nil {
		t.Errorf("a 40-character name: %v, want it accepted", err)
	}
	if err := ValidOrchestratorName(strings.Repeat("a", 41)); err == nil {
		t.Errorf("ValidOrchestratorName accepted 41 characters")
	}
}
