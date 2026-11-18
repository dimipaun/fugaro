package cli

import "github.com/dimipaun/fugaro/internal/settings"

// This file is `rigor security`'s half of the config-ownership writer contract
// (docs/contracts/config-ownership.md §4): the five rows this command writes,
// registered as the command line that writes them, so that a refusal for one of
// them names the command that works and T1's conformance test can run it
// (D2609, D2610). The contract's own table gives this family "repo
// `security.patterns`, `layer.security.*`" — the list by its bare key where a
// repository's `fugaro.yaml` spells it, the same list by its three row fields
// where the project layer file spells it, and the layer's own
// `always_level_3` beside them.
//
// The store each row names is where its own argv writes: the bare list is a
// checkout's `fugaro.yaml` (M4, D1564 — written in the working tree and
// published by nothing but the owner's own commit) and the four `layer.` rows
// are this machine's project layer file, the one `fugaro update` seeds and
// publishes. `security.patterns` is the one registry row whose key is stated in
// both layers, and the writer is registered under the layer its argv names —
// the repository's, because the file the bare spelling is written in is that
// one; the project layer's spelling of the same rows is the `layer.` pattern
// below, and `fugaro rigor security add --layer project` is the line that writes
// it.
//
// Every template carries a word ArgvFor cannot fill, and it is this family's own
// in all of them: the row's CATEGORY. The registry states what varies in a row's
// key — the row's name, and for the bare list nothing at all — and it states no
// category anywhere, while the command takes one per row and refuses a category
// outside the seven the classifier labels matches with. A template that guessed
// one would write a row the caller did not ask for, so they all state
// `securityCategoryPlaceholder`, which ArgvFor leaves as written (contract §2:
// the template is the shape of the command line, and the words it cannot place
// are the callsite's). The glob is the second such word for the three rows whose
// varying segment is the row's NAME — the union keeps a row by its name, and the
// command takes the glob as `--match`, so the line spells the name as both, as
// `rigor set`'s three area rows do (writers_rigor.go). The line therefore runs
// and is the shape of the write; the category it cannot know is why these four
// rows are still in testdata/writer_gaps.golden, which is what that file records
// until I1.
//
// The registration is a package init and its error is a programming error: a
// pattern another family already claimed, a store outside the vocabulary or a
// pattern that overlaps one of the same shape is a diff to settle rather than a
// registration to win, so this panics and every command of the binary fails
// loudly instead of one of the two silently winning (contract §2).
func init() {
	for _, w := range securityWriters {
		if err := settings.RegisterWriter(w.pattern, w.writer); err != nil {
			panic(err)
		}
	}
}

// securityWriters is the table: every key `rigor security` writes, one pattern
// per registry row.
var securityWriters = []struct {
	pattern string
	writer  settings.Writer
}{
	// The repository's own list, the whole key: the command writes one entry of
	// it, and the entry is addressed by its name. The two words such a line needs
	// are the entry's name and its glob, and the key is the list rather than
	// either of them, so both are stated as the whole key — the one row this
	// template can spell is a row whose name and glob are the list's own path.
	{
		pattern: "security.patterns",
		writer: settings.Writer{
			Command: "fugaro rigor security",
			Argv: []string{"rigor", "security", "add",
				"--name", settings.KeyPlaceholder, "--match", settings.KeyPlaceholder,
				"--category", securityCategoryPlaceholder, "--layer", "repo"},
			Store: settings.StoreRepo,
		},
	},
	// The project layer's switch. Its argv carries no value word at all: the
	// command turns the key on by being asked for it and states the value itself,
	// as `fugaro budget kill` does for the kill switch, and the registered line is
	// the whole line that writes it (T1 runs such a row once).
	{
		pattern: "layer.security.always_level_3",
		writer: settings.Writer{
			Command: "fugaro rigor security",
			Argv:    []string{"rigor", "security", "add", "--always-level-3", "--layer", "project"},
			Store:   settings.StoreProject,
		},
	},
	// One row's three leaves. `rigor security add` writes the row whole — its
	// name, its glob and its category together — so all three rows carry the same
	// line, and the pattern's varying segment is the name the row is addressed
	// and kept by.
	{
		pattern: "layer.security.patterns.*.name",
		writer: settings.Writer{
			Command: "fugaro rigor security",
			Argv: []string{"rigor", "security", "add",
				"--name", "<1>", "--match", "<1>",
				"--category", securityCategoryPlaceholder, "--layer", "project"},
			Store: settings.StoreProject,
		},
	},
	{
		pattern: "layer.security.patterns.*.match",
		writer: settings.Writer{
			Command: "fugaro rigor security",
			Argv: []string{"rigor", "security", "add",
				"--name", "<1>", "--match", "<1>",
				"--category", securityCategoryPlaceholder, "--layer", "project"},
			Store: settings.StoreProject,
		},
	},
	{
		pattern: "layer.security.patterns.*.category",
		writer: settings.Writer{
			Command: "fugaro rigor security",
			Argv: []string{"rigor", "security", "add",
				"--name", "<1>", "--match", "<1>",
				"--category", securityCategoryPlaceholder, "--layer", "project"},
			Store: settings.StoreProject,
		},
	},
}
