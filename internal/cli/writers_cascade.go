package cli

import (
	"github.com/dimipaun/fugaro/internal/settings"
)

// writers_cascade.go registers the leaves `fugaro accounts cascade` writes
// (docs/contracts/config-ownership.md §2, D2609, D2613). The cascade is one
// block with six members and the command writes them one flag at a time, so
// each member is a pattern of its own with the flag that writes it — the
// provider leaves' arrangement (writers_providers.go), and for the same reason:
// a single pattern would have to state one argv for every member, and a
// boolean member's flag carries its value in the flag's own word.
//
// Every member is registered TWICE, under two spellings, and the two are the
// two documents the cascade is one block in (F23, D2607). `credential_cascade.*`
// is this machine's own config — the carrier N1 added and the key the command
// edits — and `credentials.*` is the published node's spelling of the same
// member, which is the row R1 registers and the spelling a person reads in
// `fugaro settings` and in a refusal. Which of the two a writer's Argv writes is
// the carrier: the store of both is local, and `Node` is what says the published
// node serves the key besides it (D2615), so the after-write hook runs the
// policy stage for either spelling — a cascade written into this file and never
// published is a cascade no run reads.
//
// The node's six are the rows the conformance test measures as gaps, and it is
// right to: the value lands in the file at `credential_cascade.<member>` and the
// key the row is spelled by is the node's path, so a rig holding only files
// cannot see the write at that spelling. The line a refusal prints is the fact
// those registrations exist for (item 5 of the owner's direction: a refusal
// names the working spelling), and `settings set credentials.backoff.cap` is
// refused with the very command line this file registers.
//
// The two members R4 retires, `fallback` and `wait_threshold`, are registered
// too, and answer with retiredRoutingWriter rather than with `accounts cascade
// set`: the flag that wrote one is refused (MG4 of
// docs/model-credentials-routing/), the chain of the routing table's entry is
// what says what a run does when its credential is exhausted, and a refusal
// that named the retired flag would print a line that refuses in turn.
//
// A template carries the two words ArgvFor fills: settings.ValuePlaceholder
// where the value goes, and nothing for a key segment — a cascade member's name
// is in the pattern and in the flag, never a word the command line takes.
//
// A boolean member's flag is spelled `--flag=<value>` and never `--flag
// <value>`: pflag reads the word after a boolean flag as a positional argument,
// so the two-word spelling is a line that fails with "accepts 0 arg(s), received
// 1" while `--flag=false` is the one that runs — and the line a refusal prints
// has to be one that works (modelsWriterLeaf's own note, D2617's hand-off).
var cascadeWriterLeaves = []modelsWriterLeaf{
	{leaf: "fallback", flag: "fallback"},
	{leaf: "wait_threshold", flag: "wait-threshold"},
	{leaf: "backoff.initial", flag: "backoff-initial"},
	{leaf: "backoff.cap", flag: "backoff-cap"},
	{leaf: "stop_after_exhaustion_per_window", flag: "stop-after-exhaustion-per-window", boolean: true},
	{leaf: "concurrency_per_token", flag: "concurrency-per-token"},
}

var cascadeWriterRows = func() []modelsWriterRow {
	rows := make([]modelsWriterRow, 0, 2*len(cascadeWriterLeaves))
	for _, leaf := range cascadeWriterLeaves {
		writer := settings.Writer{
			Command: "fugaro accounts cascade set",
			Argv: append([]string{"accounts", "cascade", "set"},
				leaf.valueWords(modelsValuePlaceholder)...),
			Store: settings.StoreLocal,
			Node:  true,
		}
		if accountsCascadeRetiredLeaf(leaf.leaf) {
			writer = retiredRoutingWriter()
		}
		rows = append(rows,
			modelsWriterRow{pattern: "credential_cascade." + leaf.leaf, writer: writer},
			modelsWriterRow{pattern: "credentials." + leaf.leaf, writer: writer})
	}
	return rows
}()

// The registration is the family's own and it is fatal: a pattern two families
// claim has to fail every command of this binary rather than let one of the two
// silently win (docs/contracts/config-ownership.md §2). The node's six patterns
// are the ones this file's own diff made live — `credentials.*` was no pattern
// of any registrant's before it, and the six rows were T1's golden until now.
func init() {
	for _, row := range cascadeWriterRows {
		if err := settings.RegisterWriter(row.pattern, row.writer); err != nil {
			panic(err)
		}
	}
}
