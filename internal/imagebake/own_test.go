package imagebake

import (
	"strings"
	"testing"
)

// TestEntryOwnIsTheAuthorsBytes: the boundary between the entry's own
// definition and the stages this package appends, which is what a reader
// judging the entry's own text has to read (the catalog's refusal of a
// definition that asks for a secret of its own, B9, D2311) — and never the
// whole object, whose generated stages mount the git credential themselves.
func TestEntryOwnIsTheAuthorsBytes(t *testing.T) {
	const own = "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\nRUN --mount=type=secret,id=git-credentials git clone \"$REPO_URL\" /work/repo\n"
	rep := repoSpec("acme/app", "app-1234abcd")
	def, err := Append([]byte(own), Decide(Request{
		Entry: "app", Repo: rep, Repos: []Repo{rep}, Map: map[string]string{"github/acme/app": "app"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := string(EntryOwn(def))
	if got != own {
		t.Errorf("EntryOwn(composed) = %q, want the entry's own bytes %q", got, own)
	}
	// The mount the entry's author wrote is what a reader of the entry's own
	// text has to see, so this is the boundary and not a search for the
	// engine's stage: the composed object carries a mount either way.
	if !strings.Contains(got, "--mount=type=secret,id=git-credentials git clone") {
		t.Errorf("the entry's own mount is not in EntryOwn's answer: %q", got)
	}
	if strings.Contains(got, markerPrefix) {
		t.Errorf("EntryOwn(composed) carries the engine's own marker: %q", got)
	}

	// A definition nothing was composed into is returned whole, marker or no
	// marker: the copy of a plain entry is its author's bytes.
	for _, plain := range []string{own, "FROM scratch", "", "\n"} {
		if got := string(EntryOwn([]byte(plain))); got != plain {
			t.Errorf("EntryOwn(%q) = %q, want it whole", plain, got)
		}
	}

	// A definition that already bakes is composed again by appending, so it can
	// carry several stages and the entry's own bytes are still the prefix of
	// both: the first marker is the boundary, whichever stage is the last.
	twice, err := Append(def, Decide(Request{
		Entry: "app", Repo: rep, Repos: []Repo{rep}, Map: map[string]string{"github/acme/app": "app"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(EntryOwn(twice)); got != own {
		t.Errorf("EntryOwn(a definition composed twice) = %q, want the entry's own bytes %q", got, own)
	}

	// A line that looks like a marker but names a path this package does not
	// bake at is not a stage of its own (BakedSlug's rule, so that a definition
	// nobody composed here is not read as though it had been): the bytes are the
	// entry's own and none of them is dropped.
	outside := "# fugaro bake: /work/repo\n" + own
	if got := string(EntryOwn([]byte(outside))); got != outside {
		t.Errorf("EntryOwn = %q, want the whole definition: a marker outside %s is not a stage of this package's", got, Root)
	}

	// A definition that opens with the marker is not one this package composed,
	// however it continues: Append appends to bytes it was given, and the catalog
	// refuses an empty definition, so the author's own bytes are always under the
	// first stage. Read as the boundary it would answer with nothing, and a mount
	// the entry's author wrote under a copied marker line — the shape this test
	// below writes — would be the one thing the guard never saw.
	forged := markerPrefix + Root + "/app\nFROM scratch\nRUN --mount=type=secret,id=NPM_TOKEN true\n"
	if got := string(EntryOwn([]byte(forged))); got != forged {
		t.Errorf("EntryOwn(a definition that opens with a marker) = %q, want the whole definition", got)
	}
	// And a definition that is nothing but stages: not composed by Append, and
	// read whole for the same reason (a reader that asked for the entry's own
	// bytes here has none, and answering with the engine's own mount would be
	// worse than answering with them).
	if got := EntryOwn(def[len(own):]); string(got) != string(def[len(own):]) {
		t.Errorf("EntryOwn(stages alone) = %q, want the whole definition", got)
	}
}
