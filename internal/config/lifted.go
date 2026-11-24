package config

import "gopkg.in/yaml.v3"

// The lifted keys (R1 of docs/repo-override-rulings/reconciliation.md): the
// keys of a repository's own fugaro.yaml that the owner's ruling D3237 moves
// into the project, which release A of lift L8 reads and no longer honours and
// release B refuses.
//
// What lifts, and where it goes, is the spec's §3 and §5: the model choice (B3,
// `agent.model`, `agent.models.*`, `agent.crafts.*.model` and, by D3291,
// `agent.levels.l1–l3.model`) goes to the project layer's `defaults.agent.*`,
// where a project sets every repository of it (`fugaro models use --layer
// project`); `ab.rate` (B9) goes to `ab.repos.<owner/name>` in the layer's own
// top-level `ab:` block, which keeps the repository's opt-out and moves the
// sampling rate itself; `rigor.default` and `rigor.areas` (B11) go to
// `defaults.rigor.repos.<owner/name>` and to a member area of the project's
// (`fugaro rigor set … --repo`); and L13's `model_grades.<grade>.model`, carried
// inside L8 by D3299, goes to the project layer's grades, whose `.access` rows
// stay model-credentials-routing's. `.fugaro/recipes/` (B16) moves too and is a
// folder and no key of this list.
//
// **A lifted key is not a ScopeRow.Retired row** (R1, amended after review).
// "Retired" here means named and still honoured (`RetiredKeyNotes`, D3063), and
// model-credentials-routing's own retired rows — `agent.auth`, a craft's
// `access`, a grade's `access`, `agent.oauth_token` — are still honoured on
// purpose until its release 2 (its X2). Marking the lifted rows Retired would
// break TestEveryRetiredKeyIsNamedWhereItIsWritten, which holds RetiredKeys to
// that plan's R3 list exactly, and would tell a person a key had moved before
// `update` has moved it. So the lifted keys are a list of their own and the two
// lists stay apart; a test holds them apart.
//
// What every side may rely on:
//
//   - LiftedKeys is the only answer to "is this key lifted". S1 drops every key
//     it names (config.Resolve, both arms, every ref), S4 refuses a write of one
//     in a repository, S2 and S5 print its note, and DK1's grades ride on the
//     same list.
//   - LiftedKey.Why is the one refusal text release B prints, at `validate`,
//     `update` and a default-branch launch, so a sentence is changed here and
//     nowhere else. Each begins with the key's own path, so it stands alone.
//   - config.Resolve is the only place that drops. No reader honours a lifted
//     key by accident, and no reader needs a rule of its own.

// LiftedKey is one lifted key where it was and where it lives now: the path a
// repository's file writes, the project home that replaces it, and the sentence
// a person reads.
type LiftedKey struct {
	// Key is the fugaro.yaml path in a repository's file, as config.Scopes
	// spells it.
	Key string
	// Home is where the project states the key now: a project layer path under
	// the layer file's own defaults: block ("defaults.agent.models.coder"), or,
	// for the two keys a repository may not state at all, the block of the
	// layer's own ("ab.repos.<owner/name>"). <owner/name> stands for the
	// repository the value is about, which this list, read for a document and
	// not for a repository, cannot name.
	Home string
	// Why is the sentence a person reads where a repository's file still writes
	// the key: what became of it and the command that states it in its new home.
	Why string
}

// LiftedKeys is every lifted key, in path order — the order LiftedKeyNotes
// answers in, which is a stable order for a diff and for a message.
var LiftedKeys = []LiftedKey{
	{
		Key:  "ab.rate",
		Home: "ab.repos.<owner/name>",
		Why:  "ab.rate is the project's (D3237): remove the ab: block from fugaro.yaml; opt this repository out with `ab.repos.<owner/name>: 0` in the project layer.",
	},
	liftedModel("agent.crafts.coder.model"),
	liftedModel("agent.crafts.merge-judge.model"),
	liftedModel("agent.crafts.review.model"),
	liftedModel("agent.crafts.security-review.model"),
	liftedModel("agent.crafts.senior-review.model"),
	liftedModel("agent.levels.l1.model"),
	liftedModel("agent.levels.l2.model"),
	liftedModel("agent.levels.l3.model"),
	liftedModel("agent.model"),
	liftedModel("agent.models.background"),
	liftedModel("agent.models.coder"),
	liftedModel("agent.models.diagnose"),
	liftedModel("agent.models.reviewer"),
	liftedModel("agent.models.vision"),
	{
		Key:  "model_grades.strong.model",
		Home: "defaults.model_grades.strong.model",
		Why:  "model_grades.strong.model is the project's (D3237): remove it from fugaro.yaml; the project layer states each grade's model in `defaults.model_grades.<grade>.model`.",
	},
	{
		Key:  "model_grades.strongest.model",
		Home: "defaults.model_grades.strongest.model",
		Why:  "model_grades.strongest.model is the project's (D3237): remove it from fugaro.yaml; the project layer states each grade's model in `defaults.model_grades.<grade>.model`.",
	},
	{
		Key:  "rigor.areas",
		Home: "defaults.rigor.areas",
		Why:  "rigor.areas is the project's (D3237): remove it from fugaro.yaml; the project states this repository's areas as a member area of its own — `fugaro rigor set <mode> --area <glob> --name <name> --repo <owner/name>`.",
	},
	{
		Key:  "rigor.default",
		Home: "defaults.rigor.repos.<owner/name>",
		Why:  "rigor.default is the project's (D3237): this repository's level is `rigor.repos.<owner/name>` in the project layer; set it with `fugaro rigor set <mode> --repo <owner/name>`.",
	},
}

// liftedModel is one of B3's keys. The project layer states the same path under
// its own defaults:, and the fix is one sentence for all of them because they
// are one move: the fourteen keys are the model a role, a craft or a review
// level runs as, and `fugaro models use --layer project` writes every one of
// them.
func liftedModel(key string) LiftedKey {
	return LiftedKey{
		Key:  key,
		Home: "defaults." + key,
		Why:  key + " is the project's choice (D3237): remove it from fugaro.yaml; the project sets it with `fugaro models use --layer project`.",
	}
}

// LiftedKeyNotes is every lifted key data still writes, in path order: the path
// as the document writes it and the sentence a person reads. data is a
// repository's own fugaro.yaml — the one document these keys lift out of — and
// the paths are its own spelling, so a project layer's `defaults.agent.model`
// (the key's new home) and its `ab.repos` block answer no note: they are where
// the keys live now.
//
// A layer is never handed here. `ab.rate` is the one path two documents share —
// a repository's opt-out and the layer's own top-level `ab:` block's sampling
// rate, which AB1 puts `ab.repos` beside — and a lifted key is one a
// repository's file must stop writing; what a layer may hold is the scope
// table's to say, as it is today.
//
// It decides nothing, exactly as RetiredKeyNotes does not: bytes this cannot
// read as YAML answer no note, and release A's keys still decode and still
// validate, so a caller prints a note and never a refusal — S2 prints it at
// `validate` and `doctor`, S5 at a launch. Release B is the release that
// refuses, with the same sentence.
func LiftedKeyNotes(data []byte) []RetiredKeyNote {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil
	}
	tree, _ := docTree(&node)
	var notes []RetiredKeyNote
	keyPaths(tree, "", func(path string) {
		k, ok := liftedRow(path)
		if !ok {
			return
		}
		notes = append(notes, RetiredKeyNote{Path: path, Why: k.Why})
	})
	return notes
}

// liftedRow is the lifted key whose path is exactly path. Every entry is a leaf
// of the document with nothing of fugaro.yaml's under it, so a path below one
// (a shape the strict decode refuses) is not the lifted key's.
func liftedRow(path string) (LiftedKey, bool) {
	for _, k := range LiftedKeys {
		if k.Key == path {
			return k, true
		}
	}
	return LiftedKey{}, false
}
