package cli

import "github.com/dimipaun/fugaro/internal/settings"

// This file is `rigor set`'s half of the config-ownership writer contract
// (docs/contracts/config-ownership.md §4): the rows this command writes,
// registered as the command line that writes them, so that a refusal for one of
// them names the command that works and T1's conformance test can run it
// (D2609, D2610).
//
// What is left here is the project layer's four leaves of one rule — the mode
// and the rule's own name, glob and level — because a member area and the
// per-repository map R15 (D3237) gave this command are written through the
// rows the layer's own registry entries own, and one pattern has one registrant
// (contract §4).
//
// The store each row names is where its own argv writes: this machine's project
// layer file, the one `fugaro update` seeds and publishes.
//
// Three of the four templates carry a word ArgvFor cannot fill from the key,
// and it is the same word in all three: the glob a rule is addressed by. The
// registry's varying segment there is the rule's NAME (the segment before the
// leaf), while the command takes the glob as `--area` and the name as `--name`,
// so the templates state both from the one word they have — `<1>`, the rule's
// name standing in for its glob as well. The line therefore runs and writes the
// key it is registered for; it is not the glob a person would type, and the two
// words it cannot know are the ones the callsite has and the registry does not
// (contract §2: the template is the shape of the command line, and the glob is
// the caller's own).
//
// The registration is a package init and its error is a programming error: a
// pattern another family already claimed, a store outside the vocabulary or a
// pattern that overlaps one of the same shape is a diff to settle rather than a
// registration to win, so this panics and every command of the binary fails
// loudly instead of one of the two silently winning (contract §2).
func init() {
	for _, w := range rigorSetWriters {
		if err := settings.RegisterWriter(w.pattern, w.writer); err != nil {
			panic(err)
		}
	}
}

// rigorSetWriters is the table: every key `rigor set` writes, one pattern per
// registry row. The project layer's four are spelled leaf by leaf rather than
// under one `layer.defaults.rigor.*` pattern, because a pattern's varying
// segment is what the template's `<1>` is filled from and the two spellings do
// not share one: `layer.defaults.rigor.default` has no segment at all and
// `layer.defaults.rigor.areas.*.level` has the rule's name.
var rigorSetWriters = []struct {
	pattern string
	writer  settings.Writer
}{
	// The project layer's default, the key `fugaro update` publishes for every
	// repository of the project that names no narrower mode.
	{
		pattern: "layer.defaults.rigor.default",
		writer: settings.Writer{
			Command: "fugaro rigor set",
			Argv:    []string{"rigor", "set", settings.ValuePlaceholder, "--layer", "project"},
			Store:   settings.StoreProject,
		},
	},
	// One rule's three leaves. `rigor set` writes the rule whole — its glob,
	// its name and its mode together — so all three rows carry the same line,
	// and the pattern's varying segment is the name the rule is written at.
	{
		pattern: "layer.defaults.rigor.areas.*.name",
		writer: settings.Writer{
			Command: "fugaro rigor set",
			Argv: []string{"rigor", "set", settings.ValuePlaceholder,
				"--area", "<1>", "--name", "<1>", "--layer", "project"},
			Store: settings.StoreProject,
		},
	},
	{
		pattern: "layer.defaults.rigor.areas.*.match",
		writer: settings.Writer{
			Command: "fugaro rigor set",
			Argv: []string{"rigor", "set", settings.ValuePlaceholder,
				"--area", "<1>", "--name", "<1>", "--layer", "project"},
			Store: settings.StoreProject,
		},
	},
	{
		pattern: "layer.defaults.rigor.areas.*.level",
		writer: settings.Writer{
			Command: "fugaro rigor set",
			Argv: []string{"rigor", "set", settings.ValuePlaceholder,
				"--area", "<1>", "--name", "<1>", "--layer", "project"},
			Store: settings.StoreProject,
		},
	},
}
