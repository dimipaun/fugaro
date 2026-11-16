package settings

import (
	"slices"
	"strings"
	"testing"
)

// These tests are C1's, the contract the writer registry and the after-write
// hook are built against (docs/config-ownership/implementation-plan.md §3,
// D2609, D2610, D2611), plus CR1's revision of it: the segment placeholders,
// the overlap refusal, and the copies in and out. They register under
// `writertest.` (and `wtesta.`-`wtests.`) so that no pattern a family registers
// can collide with one of theirs, and so that a pattern left in the table by a
// test matches no registry row. Each registration is dropped again by
// registerForTest, so the package's tests run twice (`-count=2`) without a
// second run meeting the first run's table as another family's registrations.

// sameWriter is the equality a Writer needs: it carries an Argv, so two
// writers with the same fields are not comparable with ==.
func sameWriter(a, b Writer) bool {
	return a.Command == b.Command && a.Store == b.Store && a.Label == b.Label &&
		a.Reason == b.Reason && slices.Equal(a.Argv, b.Argv)
}

// registerForTest registers w and drops the pattern again when the test ends.
// Without the cleanup a second run of this package's tests (go test -count=2)
// meets its own registrations as another family's, which is the duplicate
// RegisterWriter refuses.
func registerForTest(t *testing.T, pattern string, w Writer) {
	t.Helper()
	if err := RegisterWriter(pattern, w); err != nil {
		t.Fatalf("%s: %v", pattern, err)
	}
	t.Cleanup(func() { unregisterWriter(pattern) })
}

// TestWriterOfMatchesWildcardRows pins the lookup's wildcard rule to the one
// the registry's own rows are addressed by (settingsPatternMatch,
// internal/cli/settings_set.go): the same number of dot-separated segments, a
// `*` standing for exactly one non-empty segment, and the more specific row
// deciding when two patterns match one key. A row like
// `stages.*.claude_account` is reachable by the name a person writes only if
// the two rules agree.
func TestWriterOfMatchesWildcardRows(t *testing.T) {
	// Each behaviour is registered under its own namespace, so that no pattern
	// of one case decides the answer of another.
	exact := Writer{Command: "fugaro writertest exact", Argv: []string{"writertest", "exact", KeyPlaceholder}, Store: StoreRepo}
	wild := Writer{Command: "fugaro writertest wild", Argv: []string{"writertest", "wild", KeyPlaceholder}, Store: StoreLocal}
	specific := Writer{Command: "fugaro writertest specific", Argv: []string{"writertest", "specific", KeyPlaceholder}, Store: StoreNode}
	broad := Writer{Command: "fugaro writertest broad", Argv: []string{"writertest", "broad", KeyPlaceholder}, Store: StoreProject}

	for pattern, w := range map[string]Writer{
		"wtesta.exact.leaf":  exact,
		"wtesta.*.leaf":      broad,
		"wtestb.*.leaf":      wild,
		"wtestc.wide.*.leaf": specific,
		"wtestc.*.*.leaf":    broad,
	} {
		registerForTest(t, pattern, w)
	}

	for _, tc := range []struct {
		key   string
		want  Writer
		found bool
		why   string
	}{
		{
			key: "wtesta.exact.leaf", want: exact, found: true,
			why: "the row registered under the key itself and the pattern beside it both match, and the row written out in full decides",
		},
		{
			key: "wtesta.other.leaf", want: broad, found: true,
			why: "a key the broad row's * stands for is answered by it",
		},
		{
			key: "wtestb.name.leaf", want: wild, found: true,
			why: "a * stands for one segment, so the row answers the concrete name a person writes",
		},
		{
			key: "wtestb.leaf",
			why: "the key has one segment fewer than the pattern, and a * never spans a dot",
		},
		{
			key: "wtestb..leaf",
			why: "the segment the * would stand for is empty, and a * matches a name, which is never empty",
		},
		{
			key: "wtestc.wide.name.leaf", want: specific, found: true,
			why: "two patterns match this key and the more specific one (fewer varying segments) is the writer",
		},
		{
			key: "wtestc.other.name.leaf", want: broad, found: true,
			why: "the pattern with two varying segments is the only one that matches, so it is the writer however specific it is not",
		},
		{
			key: "wtestb.name.leaf.deeper",
			why: "the key has one segment more than the pattern",
		},
		{
			key: "wtestd.nothing.registered",
			why: "no row was registered for this key and no pattern matches it",
		},
	} {
		got, ok := WriterOf(tc.key)
		if ok != tc.found {
			t.Errorf("WriterOf(%s) found = %v, want %v: %s", tc.key, ok, tc.found, tc.why)
			continue
		}
		if !tc.found {
			continue
		}
		if !sameWriter(got, tc.want) {
			t.Errorf("WriterOf(%s) = %+v, want %+v: %s", tc.key, got, tc.want, tc.why)
		}
	}
}

// TestRegisterWriterRefusesADuplicatePattern is the pin on item 3 and on the
// plan's "no pattern has two registrants": the second registration of a
// pattern is refused, and the first writer still answers, so a family that
// registers a row another family owns fails loudly rather than silently
// replacing it.
func TestRegisterWriterRefusesADuplicatePattern(t *testing.T) {
	const pattern = "writertest.duplicate.leaf"
	first := Writer{Command: "fugaro writertest first", Argv: []string{"writertest", "first", KeyPlaceholder}, Store: StoreLocal}
	registerForTest(t, pattern, first)
	// The second writer states both shapes in full, so that what the call below
	// refuses is the duplicate and not a writer the vocabulary already rejects.
	err := RegisterWriter(pattern, Writer{Command: "fugaro writertest second", Argv: []string{"writertest", "second", KeyPlaceholder}, Store: StoreRepo})
	if err == nil {
		t.Fatalf("%s: the second registration was accepted: one pattern has one registrant (docs/contracts/config-ownership.md)", pattern)
	}
	if !strings.Contains(err.Error(), pattern) {
		t.Errorf("the refusal does not name %s: %v", pattern, err)
	}
	got, ok := WriterOf(pattern)
	if !ok || !sameWriter(got, first) {
		t.Errorf("WriterOf(%s) = %+v, %v, want the first registration %+v: a refused registration leaves the table as it was", pattern, got, ok, first)
	}
}

// TestLabelIsOneOfTheThree pins D2611's vocabulary — the three labels, and the
// empty one an ordinary row carries — and the refusals that make it real: a
// label outside it is not a writer, the `file` label (the one that stands for a
// key a person edits) carries its one-line reason or the row is not registered
// at all, and a writer states exactly one of the two shapes Writer documents —
// a row a command writes has a Command, an Argv and a store that is not `none`,
// and a labelled row has neither an Argv nor a store. T5 makes the label with
// its reason the pinned fact for a row no command writes, which is what L1
// registers.
func TestLabelIsOneOfTheThree(t *testing.T) {
	for got, want := range map[Label]string{
		LabelPlumbing:     "plumbing",
		LabelFakeEndpoint: "fake-endpoint",
		LabelFile:         "file",
	} {
		if string(got) != want {
			t.Errorf("the %s label reads %q: the vocabulary is the owner's D2611 answer and the conformance test, the read and the skill text all spell it so", want, string(got))
		}
	}

	refused := []struct {
		name    string
		pattern string
		writer  Writer
		why     string
	}{
		{
			name: "a label outside the three", pattern: "writertest.label.unknown",
			writer: Writer{Label: Label("manual"), Store: StoreNone},
			why:    "one label `manual` fails item 8's stated reason, which is why D2611 splits it into three",
		},
		{
			name: "a file row with no reason", pattern: "writertest.label.bare",
			writer: Writer{Label: LabelFile, Store: StoreNone},
			why:    "a `file` row states in one line why the key is not a settings row, or the label stands for nothing",
		},
		{
			name: "a store outside the six", pattern: "writertest.label.store",
			writer: Writer{Store: Store("cloud")},
			why:    "the store is where the value lands and what the conformance test asserts (D2610), so a store nobody defined is a write no reader can place",
		},
		{
			name: "a row a command writes with no command line", pattern: "writertest.shape.noarg",
			writer: Writer{Command: "fugaro settings set", Store: StoreRepo},
			why:    "the conformance test runs the Argv and a refusal prints it, so an unlabelled writer with none puts a bare `fugaro` in both places (T1, W3)",
		},
		{
			name: "a row a command writes that names no command", pattern: "writertest.shape.nocommand",
			writer: Writer{Argv: []string{"settings", "set", KeyPlaceholder, "<value>"}, Store: StoreRepo},
			why:    "the settings read and the refusal say which command owns the key, so a runnable writer that names none states half of itself",
		},
		{
			name: "a row a command writes that lands nowhere", pattern: "writertest.shape.nowhere",
			writer: Writer{Command: "fugaro settings set", Argv: []string{"settings", "set", KeyPlaceholder, "<value>"}, Store: StoreNone},
			why:    "a writer with an Argv lands in a file or a database, and a store of `none` is a write the conformance test can assert nothing about (D2610)",
		},
		{
			name: "a labelled row that also has a command line", pattern: "writertest.shape.labelarg",
			writer: Writer{Label: LabelPlumbing, Argv: []string{"setup"}, Store: StoreNone},
			why:    "the label stands in for a runnable writer, so a labelled row with an Argv is two answers to one question (T5)",
		},
		{
			name: "a labelled row that names a store", pattern: "writertest.shape.labelstore",
			writer: Writer{Label: LabelFakeEndpoint, Store: StoreLocal},
			why:    "a row no command writes lands nowhere: the store of a labelled row is `none`, not the file a person's own edit touches (T5)",
		},
	}
	for _, tc := range refused {
		if err := RegisterWriter(tc.pattern, tc.writer); err == nil {
			t.Errorf("%s: %+v was registered: %s", tc.name, tc.writer, tc.why)
		}
	}

	ok := []struct {
		writer  Writer
		pattern string
	}{
		{Writer{Command: "fugaro setup", Store: StoreNone, Label: LabelPlumbing}, "writertest.label.plumbing"},
		{Writer{Command: "fugaro setup", Store: StoreNone, Label: LabelFakeEndpoint}, "writertest.label.fake"},
		{Writer{Label: LabelFile, Store: StoreNone, Reason: "the file is the editor: no command writes it"}, "writertest.label.file"},
		{Writer{Command: "fugaro settings set", Argv: []string{"settings", "set", KeyPlaceholder, "<value>"}, Store: StoreRepo}, "writertest.label.runnable"},
	}
	for _, tc := range ok {
		if err := RegisterWriter(tc.pattern, tc.writer); err != nil {
			t.Errorf("%s: %v", tc.pattern, err)
		}
		t.Cleanup(func() { unregisterWriter(tc.pattern) })
	}
}

// TestArgvForFillsTheKeyAndItsSegments is CR1's half of the placeholder rule
// (§2): `<key>` is the whole key, `<n>` is the key's segment at the pattern's
// nth `*` counting left to right, and `<value>` is left for the caller to fill.
// The `pools.*.owner` case is the probe CR1 was raised for: a template writing
// the whole key where the command takes the pool's name answers with
// `accounts pool add pools.team.owner`, which is not a command anybody can run.
func TestArgvForFillsTheKeyAndItsSegments(t *testing.T) {
	// A1's row shape, spelled as the probe had it. The pattern is A1's to
	// register; cleanup takes it back out, so this test leaves no row behind.
	registerForTest(t, "pools.*.owner", Writer{
		Command: "fugaro accounts pool add",
		Argv:    []string{"accounts", "pool", "add", "<1>", "--owner", ValuePlaceholder},
		Store:   StoreLocal,
	})
	// Two stars that are not adjacent, so that `<1>` and `<2>` are the segments
	// of the first and the second star (the key's second and fourth) rather than
	// its first two.
	registerForTest(t, "wtestp.*.mid.*", Writer{
		Command: "fugaro writertest segments",
		Argv:    []string{"writertest", KeyPlaceholder, "<1>", "<2>", ValuePlaceholder},
		Store:   StoreLocal,
	})

	for _, tc := range []struct {
		key  string
		want []string
		why  string
	}{
		{
			key:  "pools.team.owner",
			want: []string{"accounts", "pool", "add", "team", "--owner", ValuePlaceholder},
			why:  "the probe CR1 was raised for: the `*` stands for the pool's name, and the value is the caller's (T1)",
		},
		{
			key:  "wtestp.first.mid.second",
			want: []string{"writertest", "wtestp.first.mid.second", "first", "second", ValuePlaceholder},
			why:  "`<key>` is the whole key, and `<1>` and `<2>` are the stars' segments left to right, not the key's leading ones",
		},
	} {
		w, ok := WriterOf(tc.key)
		if !ok {
			t.Fatalf("%s: no writer was registered for the key", tc.key)
		}
		if got := w.ArgvFor(tc.key); !slices.Equal(got, tc.want) {
			t.Errorf("ArgvFor(%s) = %q, want %q: %s", tc.key, got, tc.want, tc.why)
		}
	}
}

// TestArgvForLeavesAPlaceholderItDoesNotOwn is the other half of that rule: a
// placeholder is a whole word, `<value>` is never filled, and a `<n>` the
// registered pattern has no nth `*` for is left as written rather than dropped
// or refused — a key the pattern matches has one segment per pattern segment,
// so there is nothing to fill it with, and ArgvFor answers a []string with no
// error to refuse with. A word silently dropped would run a different command
// than the template states.
func TestArgvForLeavesAPlaceholderItDoesNotOwn(t *testing.T) {
	registerForTest(t, "wtestq.*.leaf", Writer{
		Command: "fugaro writertest untouched",
		Argv:    []string{"writertest", "--pool=<1>", "<1>", "<2>", ValuePlaceholder, KeyPlaceholder},
		Store:   StoreRepo,
	})
	w, ok := WriterOf("wtestq.name.leaf")
	if !ok {
		t.Fatalf("wtestq.name.leaf: no writer was registered for the key")
	}
	want := []string{"writertest", "--pool=<1>", "name", "<2>", ValuePlaceholder, "wtestq.name.leaf"}
	if got := w.ArgvFor("wtestq.name.leaf"); !slices.Equal(got, want) {
		t.Errorf("ArgvFor(wtestq.name.leaf) = %q, want %q: `<1>` is the segment the one `*` stands for, `--pool=<1>` is not a placeholder, `<2>` has no second `*` to name, and `<value>` is the caller's", got, want)
	}

	// A Writer built by hand rather than answered by WriterOf carries no
	// pattern, so it fills `<key>` and leaves every `<n>` (contract §2).
	hand := Writer{Command: "fugaro writertest hand", Argv: []string{"writertest", "<1>", KeyPlaceholder}, Store: StoreLocal}
	handWant := []string{"writertest", "<1>", "wtestq.name.leaf"}
	if got := hand.ArgvFor("wtestq.name.leaf"); !slices.Equal(got, handWant) {
		t.Errorf("ArgvFor on a Writer that was never registered = %q, want %q: the segment a `<n>` names is the pattern's, and this Writer has none", got, handWant)
	}
}

// TestRegisterWriterRefusesAnOverlappingPattern is CR1's new refusal (§2): two
// patterns with the same number of varying segments that can match one key
// cannot both stand, because which of the two answered would then be the
// table's text order and not the specificity the contract promises. Patterns of
// different varying counts stay allowed — they meet only where one is strictly
// the more specific, and the more specific wins.
func TestRegisterWriterRefusesAnOverlappingPattern(t *testing.T) {
	first := Writer{Command: "fugaro writertest first", Argv: []string{"writertest", "first", KeyPlaceholder}, Store: StoreLocal}
	registerForTest(t, "wtestr.*.b", first)

	// `wtestr.x.*` and `wtestr.*.b` both match wtestr.x.b, each varying one
	// segment: the second registration is refused, and it names both patterns.
	err := RegisterWriter("wtestr.x.*", Writer{Command: "fugaro writertest second", Argv: []string{"writertest", "second", KeyPlaceholder}, Store: StoreRepo})
	if err == nil {
		t.Fatalf("wtestr.x.* was registered over wtestr.*.b: two patterns that both answer wtestr.x.b leave the answer to the table's order (docs/contracts/config-ownership.md)")
	}
	for _, want := range []string{"wtestr.*.b", "wtestr.x.*"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if got, ok := WriterOf("wtestr.x.b"); !ok || !sameWriter(got, first) {
		t.Errorf("WriterOf(wtestr.x.b) = %+v, %v, want the first registration %+v: a refused registration leaves the table as it was", got, ok, first)
	}

	// The same two patterns with different varying counts are the case the
	// contract keeps: the row written out in full is the more specific one.
	specific := Writer{Command: "fugaro writertest specific", Argv: []string{"writertest", "specific", KeyPlaceholder}, Store: StoreNode}
	registerForTest(t, "wtestr.x.b", specific)
	if got, ok := WriterOf("wtestr.x.b"); !ok || !sameWriter(got, specific) {
		t.Errorf("WriterOf(wtestr.x.b) = %+v, %v, want %+v: a pattern with no varying segment may be registered beside a broader one and answers first", got, ok, specific)
	}
	if got, ok := WriterOf("wtestr.other.b"); !ok || !sameWriter(got, first) {
		t.Errorf("WriterOf(wtestr.other.b) = %+v, %v, want %+v: the broader pattern still answers the keys the exact row does not", got, ok, first)
	}
}

// TestWriterOfClonesTheArgv is CR1's copy rule (§2): RegisterWriter stores a
// clone of the Argv it is handed and WriterOf answers a clone of the stored
// one, so neither a family's own template slice nor a caller's copy of the
// answer is a way to edit the words another reader gets.
func TestWriterOfClonesTheArgv(t *testing.T) {
	const key = "wtests.clone.leaf"
	template := []string{"writertest", "clone", KeyPlaceholder}
	registerForTest(t, "wtests.*.leaf", Writer{Command: "fugaro writertest clone", Argv: template, Store: StoreProject})
	want := []string{"writertest", "clone", KeyPlaceholder}

	template[0] = "edited after registration"
	w, ok := WriterOf(key)
	if !ok {
		t.Fatalf("%s: no writer was registered for the key", key)
	}
	if !slices.Equal(w.Argv, want) {
		t.Errorf("WriterOf(%s).Argv = %q, want %q: the registration stores a copy, so editing the slice a family registered is not editing the table", key, w.Argv, want)
	}

	w.Argv[0] = "edited after lookup"
	again, ok := WriterOf(key)
	if !ok {
		t.Fatalf("%s: no writer was registered for the key", key)
	}
	if !slices.Equal(again.Argv, want) {
		t.Errorf("WriterOf(%s).Argv = %q, want %q after editing the first answer: the lookup answers a copy too", key, again.Argv, want)
	}

	filled := again.ArgvFor(key)
	filled[0] = "edited after ArgvFor"
	last, ok := WriterOf(key)
	if !ok {
		t.Fatalf("%s: no writer was registered for the key", key)
	}
	if !slices.Equal(last.Argv, want) {
		t.Errorf("WriterOf(%s).Argv = %q, want %q after editing what ArgvFor returned", key, last.Argv, want)
	}
}

// TestWriterStoreIsOneOfTheSix is the store vocabulary's own pin: the
// constants are the six the acceptance names, and a store whose value is none
// of them is refused (the third case of the refusals above). D2615 makes the
// store the input of the stage choice the after-write hook makes, so a store
// nobody defined would be a write no stage is ever run for.
func TestWriterStoreIsOneOfTheSix(t *testing.T) {
	for got, want := range map[Store]string{
		StoreLocal:   "local",
		StoreProject: "project",
		StoreRepo:    "repo",
		StoreNode:    "node",
		StoreBudget:  "budget",
		StoreNone:    "none",
	} {
		if string(got) != want {
			t.Errorf("the %s store reads %q", want, string(got))
		}
	}
}
