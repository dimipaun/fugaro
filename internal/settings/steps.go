package settings

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// This file is what a settings row's own layers say about the follow-up steps
// a write of its key owes (composite AP5, D1561, D1562): the declaration a key
// has, read off the row the registry already states rather than kept in a
// second table of key spellings beside it. A second table is the option D1561
// rejects — a list that can disagree with the registry — and a key added to the
// registry declares its steps by being added.
//
// What a write owes is a fact about the key AND the layer the write landed in,
// which is why this file declares the layers' own stages and not a list per
// key: `agent.model` written into a checkout's `fugaro.yaml` is live once that
// change is merged and owes nothing here, the same key written at the project
// layer owes the layer stage, and the machine's own config owes the policy
// stage, because the node is derived from it (docs/contracts/config-ownership.md
// §3, D2615). A table from key to steps could not say that — one key has three
// answers — and it is the row's Layers, already the closed vocabulary
// `--layer` accepts, that says it.
//
// Two families declare more than their layers name and keep their own rows
// where the steps are run (internal/apply/steps.go, AP1): the image catalog's
// entries owe the image build, and the workspace's rows owe the workspace's two
// stages. Those rows are additions to what this file declares, and the runner
// takes both.
//
// Nothing here runs a step, reads a file or names a command: it is the
// declaration, and internal/apply and internal/cli are the two readers.

// layerSteps is the stage each layer of the registry's vocabulary names.
//
//   - `local` and `shared` are the machine's own config (internal/localcfg).
//     The published node is derived from it, so a value the node serves lives
//     there and a write to it is a publish: the policy stage.
//   - `policy` is the node itself, so its write is the policy stage too.
//   - `project` is the project layer file and `profile` its profiles block:
//     both are the layer stage's, which publishes the file (D830/D831).
//   - `repo`, `flag`, `default`, `caps` and `env` name no stage. A checkout's
//     `fugaro.yaml` is a file the next run reads and the change that makes it
//     live is the merge of the branch holding it (D1564); a flag, a built-in
//     default and an environment variable are not written by any command at
//     all; and the budget database's `caps` rows are live the moment the
//     command writes them (docs/contracts/config-ownership.md §3).
//
// Every layer of the vocabulary is here, including the ones naming no stage:
// KeySteps answers from this map, and a layer missing from it would be a row
// whose write silently owes nothing (TestEverySettingKeyDeclaresItsSteps).
var layerSteps = map[string]string{
	LayerLocal:   initflow.Policy,
	LayerShared:  initflow.Policy,
	LayerPolicy:  initflow.Policy,
	LayerProject: initflow.Layer,
	LayerProfile: initflow.Layer,
	LayerRepo:    "",
	LayerFlag:    "",
	LayerDefault: "",
	LayerCaps:    "",
	LayerEnv:     "",
}

// KeySteps is the follow-up steps key's own row declares, in no order of their
// own (the runner orders them), and false for a key no row answers for. It is
// what a write of the key owes for the key being what it is; the layer the
// write landed in adds the store's own stage on top
// (apply.WriteSteps), and the two together are what the write owes.
//
// A key varies (`workflows.*.commands.test` is written at
// `workflows.web.commands.test`), so the row is resolved the way the read
// resolves one: the row registered under the key itself, or the most specific
// pattern that matches it.
func KeySteps(key string) ([]string, bool) {
	row, ok := rowForKey(key)
	if !ok {
		return nil, false
	}
	var stages []string
	for _, layer := range row.Layers {
		if stage := layerSteps[layer]; stage != "" && !slices.Contains(stages, stage) {
			stages = append(stages, stage)
		}
	}
	return stages, true
}

// rowForKey is the registry row a write of key lands on: the row registered
// under the key itself, or the most specific pattern that matches it. The two
// patterns that could match one key are settled by the more specific of them —
// the row that varies in fewer segments, and the key's own text to break a tie
// the registry refuses to register — so the answer is a function of the
// registry and not of the order the table happens to be in.
//
// It is the resolution the after-write hook made for a key before this file
// existed (afterWriteStages, internal/cli), and the one the write path makes
// with the generic allowlist asked first (settingsWriteLookup): this package
// cannot import internal/cli, so the two spellings of one rule are held to each
// other where both are reachable
// (TestEverySettingKeyDeclaresItsSteps, internal/cli).
func rowForKey(key string) (Row, bool) {
	if row, ok := Lookup(key); ok {
		return row, true
	}
	best, found, varied := Row{}, false, 0
	for _, row := range Rows() {
		if !patternMatches(row.Key, key) {
			continue
		}
		if n := strings.Count(row.Key, "*"); !found || n < varied || (n == varied && row.Key < best.Key) {
			best, found, varied = row, true, n
		}
	}
	return best, found
}
