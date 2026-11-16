package cli

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/settings"
)

// writers_table.go is T1's half of the writer registry
// (docs/contracts/config-ownership.md §4): the rows no family of
// docs/config-ownership/implementation-plan.md §3 owns. Every row the generic
// path writes registers one pattern here by walking the registry, so a row the
// allowlist gains or loses moves this table with it and nothing is restated;
// the commands no task of the plan replaces (budget, workspace, secrets and
// project) register below them, and the families that DO replace a command
// register their own rows in their own file (A1 pools, M1 `models use`, M2
// providers and prices, M3 rigor, A2 crafts and grades, K1 the cascade, K2
// binaries, S1 the secrets work's three patterns). A row none of them has
// registered yet is a gap the conformance test measures into
// testdata/writer_gaps.golden.
//
// Registration happens here rather than in a family file because these rows
// have no family: `settings set` is the generic path, and the four commands
// below are one command each with no task of this plan rewriting them.

func init() {
	for _, row := range settings.Rows() {
		writer, ok := writersTableGeneric(row)
		if !ok {
			continue
		}
		if err := settings.RegisterWriter(row.Key, writer); err != nil {
			panic(err)
		}
	}
	for key, argv := range writersBudgetRows {
		command := "fugaro " + argv[0] + " " + argv[1]
		if err := settings.RegisterWriter(key, settings.Writer{
			Command: command,
			Argv:    argv,
			Store:   settings.StoreBudget,
		}); err != nil {
			panic(err)
		}
	}
}

// writersBudgetRows is the budget's own writers (contract §4): the caps and the
// kill switch are written neither by `settings set` — they are not settings
// files but nodes of the project's Realtime Database — nor by any family of
// docs/config-ownership/implementation-plan.md §3, so they are T1's.
//
// A key here is a flag of one command rather than a key named on the command
// line: `caps.global.per_day_usd` is `budget set --global --daily`, and the
// registry's spelling of the row goes to the reader and the refusal rather than
// into the argv. `caps.mode` carries the caps beside the mode because the
// writer checks that a mode of enforce has caps to enforce, and an installation
// whose caps are unset is exactly the one this test probes; the two flags name
// values the row does not claim to write.
//
// `kill.global.on` is the one writer here that takes no value: the switch is
// turned on by `budget kill --all` itself, and no word of the command line
// carries the row's value.
var writersBudgetRows = map[string][]string{
	"caps.mode":                 {"budget", "set", "--global", "--daily", "5", "--per-run", "1", "--mode", writersValueWord},
	"caps.global.per_day_usd":   {"budget", "set", "--global", "--daily", writersValueWord},
	"caps.global.per_run_usd":   {"budget", "set", "--global", "--per-run", writersValueWord},
	"caps.defaults.per_day_usd": {"budget", "set", "--defaults", "--daily", writersValueWord},
	"caps.defaults.per_run_usd": {"budget", "set", "--defaults", "--per-run", writersValueWord},
	"caps.max_reserve_usd":      {"budget", "set", "--global", "--max-reserve", writersValueWord},
	"kill.global.on":            {"budget", "kill", "--all"},
}

// writersValueWord is the placeholder an Argv carries where the value goes: the
// word CR1 gives the contract as settings.ValuePlaceholder, spelled here until
// that revision lands (this package cannot reference a test's constant, and
// writer_conformance_test.go's writerValueWord mirrors this one and fails if
// the two drift). ArgvFor fills `<key>`; this word is left for the caller —
// the conformance test fills it, and a refusal prints the line that works once
// W3 reads it.
const writersValueWord = "<value>"

// writersTableGeneric is the writer of a registry row the generic path owns:
// `settings set` writes it, the read's own accept set is the test (the owner
// column names this path, the key is one spec §4.4 hands it, and a sensitive
// value never travels through this command).
//
// The layer is the row's narrowest — settingsDefaultLayer's own rule — and it
// is written into the argv rather than left to the command's default, so the
// line a refusal prints says where the value lands. A row whose layers are
// none of `--layer`'s three (a `profile` row, a cloud `caps` row) is not this
// path's to write at all: no layer word would make the command line work.
func writersTableGeneric(row settings.Row) (settings.Writer, bool) {
	if row.Owner != "settings set" || row.Sensitive || !settingsGenericKey(row.Key) {
		return settings.Writer{}, false
	}
	layer := settingsDefaultLayer(row)
	if !slices.Contains(settingsWriteLayers, layer) {
		return settings.Writer{}, false
	}
	value, ok := writersTableValue(row)
	if !ok {
		return settings.Writer{}, false
	}
	argv := append([]string{"settings", "set", settings.KeyPlaceholder}, append(value, "--layer", layer)...)
	if layer != settingsLayerLocal && writersExecutableKey(row.Key) {
		argv = append(argv, "--executable-changes")
	}
	return settings.Writer{
		Command: "fugaro settings set",
		Argv:    argv,
		Store:   writersTableStore(layer),
	}, true
}

// writersExecutableKey reports whether writing the key runs as shell and so
// needs the owner's --executable-changes: the keys config.ExecutableKeys names
// in a repository's file, and the same keys at the project layer's profile
// spelling, where one profile templates the commands and image steps of every
// repository that takes it. The gate the write path reads is the one this flag
// answers (workflowExecutableGate, layerExecutableGate), so the command line
// the table registers is the one that works rather than one the gate refuses.
func writersExecutableKey(key string) bool {
	if key == writersDefaultProfileKey {
		return true
	}
	for _, pattern := range config.ExecutableKeys {
		if pattern == key || settingsPatternMatch(strings.Replace(pattern, "workflows.*.", "layer.profiles.*.", 1), key) {
			return true
		}
	}
	return false
}

// writersDefaultProfileKey is the one key outside config.ExecutableKeys whose
// write the layer's own gate refuses without the acknowledgement: the edit
// re-points which profile's commands and image steps apply to every repository
// of the project, so layerExecutableGate counts the commands of the profile it
// leaves as changed.
const writersDefaultProfileKey = "layer.default_profile"

// writersTableValue is the words of the argv that carry the value: the
// positional value of a scalar row, `--add` for the rows whose value is a list
// of strings (D1162's flag, the one `settingsHintListKeys` names), and
// `--add-entry` for the rows whose value is a block (D1162) — a map's entry is
// one member the caller spells, and an object list's entry is its key field
// beside one member, which is what the entry write requires.
//
// A block the entry pair cannot address — an object list with no key field,
// which is `settingsEntryBlocks`' own note about the cache blocks — answers
// false: neither the scalar form nor the entry form writes it, so no command
// line would work and the row is a gap rather than a registered writer whose
// argv prints.
func writersTableValue(row settings.Row) ([]string, bool) {
	if _, block, ok := settingsEntryBlockPattern(row.Key); ok {
		switch {
		case block.kind == settingsEntryMap:
			return []string{"--add-entry", writersValueWord}, true
		case block.keyField != "":
			return []string{"--add-entry", writersValueWord, "--add-entry", writersValueWord}, true
		default:
			return nil, false
		}
	}
	if settingsHintListKeys[row.Key] {
		return []string{"--add", writersValueWord}, true
	}
	return []string{writersValueWord}, true
}

// writersTableStore is the store a `--layer` value writes in: the file the
// command's own argv names, which is what the conformance test asserts the key
// changed in (D2610).
func writersTableStore(layer string) settings.Store {
	switch layer {
	case settingsLayerRepo:
		return settings.StoreRepo
	case settingsLayerProject:
		return settings.StoreProject
	default:
		return settings.StoreLocal
	}
}
