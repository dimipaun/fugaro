package config

import (
	"testing"
)

// lifted_rows_test.go is I1's half of lift L8 on the config package: every
// reader of a repository's file drops every lifted key (this file pins that the
// two lists the drop reads stay apart), and the same walk over the whole CLI and
// the runner is consumers_test.go's TestEveryReaderDropsLiftedKeys and the
// runner package's own (docs/repo-override-rulings/implementation-plan.md, I1).

// TestLiftedAndRetiredListsAreApart is R1 read from the other side than
// TestLiftedKeysAreNotMCRsRetiredKeys: the lifted list and the retired one meet
// nowhere, and RetiredKeys is still model-credentials-routing's R3 list exactly
// — the rows its own release 2 refuses and this release still honours.
//
// A lifted key that crept onto RetiredKeys would be told to a person as a key
// that stopped working, cutting the bridge model-credentials-routing relies on
// (F1), and a retired key that crept onto the lifted list would be dropped from
// a credential a run still needs. The two lists are held apart by reading every
// key of one out of the other, in both directions, in both documents, and by
// reading the note machinery's own answer: a document that writes a lifted key
// answers no retired note for it, and one that writes a retired key does.
//
// R3's own spelling is not restated here: TestEveryRetiredKeyIsNamedWhereItIsWritten
// already holds RetiredKeys to it, so a row added to or dropped from that table
// fails there and this test reads the same list.
//
// Mutation (run, restored): mark a lifted row Retired, or add a lifted key to
// R3's list, and this test names it.
func TestLiftedAndRetiredListsAreApart(t *testing.T) {
	if len(LiftedKeys) == 0 || len(RetiredKeys) == 0 {
		t.Fatal("this test reads one empty list")
	}
	for _, k := range LiftedKeys {
		for _, doc := range []Scope{InRepo, InProject} {
			if rk, ok := retiredIn(RetiredKeys, k.Key, doc); ok {
				t.Errorf("%s is lifted and retired in a %s (%q): the two lists meet where a key cannot be both", k.Key, docKindName(doc), rk.Why)
			}
			// The document that writes it answers a lifted note and no retired
			// one: the note machinery reads the same two lists apart.
			if notes := RetiredKeyNotes(docWriting(t, k.Key, doc), doc); len(notes) != 0 {
				t.Errorf("a %s that writes the lifted key %s is told about it as retired: %+v", docKindName(doc), k.Key, notes)
			}
		}
		row, ok := scopeRow(k.Key)
		if !ok {
			t.Errorf("%s is lifted and is no Scopes row", k.Key)
			continue
		}
		if row.Retired != 0 {
			t.Errorf("%s is lifted and its Scopes row marks Retired %v: a lifted key is named by its own list, never by Retired (R1)", k.Key, row.Retired)
		}
	}
	for _, rk := range RetiredKeys {
		if _, ok := liftedRow(rk.Key); ok {
			t.Errorf("%s is retired in a %s and lifted: model-credentials-routing's rows stay honoured and out of this list", rk.Key, docKindName(rk.In))
		}
		// And a retired key is read as one: its own document is told so, with
		// the path that document writes (a layer's carries its defaults:).
		notes := RetiredKeyNotes(docWriting(t, rk.Key, rk.In), rk.In)
		want := rk.Key
		if rk.In == InProject {
			want = "defaults." + rk.Key
		}
		if len(notes) != 1 || notes[0].Path != want {
			t.Errorf("a %s that writes the retired key %s answers %+v, want its own note at %q", docKindName(rk.In), rk.Key, notes, want)
		}
	}
}
