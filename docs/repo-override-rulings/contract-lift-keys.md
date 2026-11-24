# The lifted keys contract (C1)

Task **C1** of [implementation-plan.md](implementation-plan.md), module `lift-keys`. It states **R1**
(amended after review) — a lifted key is a list of its own, never a `Retired` row — with the spec's
§3 and §5 rulings behind it: **D3237** (B3, B9, B11 and B16 move to the project), **D3291**
(`agent.levels.l1–l3.model` is a B3 key) and **D3299** (L13's `model_grades.<grade>.model` rides
inside L8; its `.access` rows stay model-credentials-routing's). **F1** and **F2** are the findings it
answers, **F25** is why `config.AgentOf` is named under the drop, and
[reconciliation.md](reconciliation.md) wins wherever it and [spec.md](spec.md) disagree.

The Go is `internal/config/lifted.go` (`LiftedKey`, `LiftedKeys`, `LiftedKeyNotes`); the note's shape
is `config.RetiredKeyNote` (`internal/config/retired.go`), reused rather than duplicated, because a
person reads both lists the same way. The pins are `internal/config/lifted_test.go`.

**S1** drops every lifted key in `config.Resolve` (and in `config.AgentOf`, F25), **S4** refuses a
write of one in a repository, **S2** prints the notes at `validate` and `doctor`, **S5** prints them
at a launch and gates a pre-L8 image on the default branch writing one, **DK1**'s grades ride the
same list, and **I1** walks every reader of report §5 over a file that writes every key. Release B
(**XB1**, **XB2**, **XB6**) refuses them on the default branch with these sentences. A side that
finds this contract wrong or incomplete raises a change request
([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than working around
it.

## (a) The list, and what is on it

`config.LiftedKeys` is every key D3237 lifts out of a repository's own `fugaro.yaml`, in path order,
each with the project home that replaces it and the sentence a person reads:

| Key (a repository's file) | Home after L8 | Item |
| --- | --- | --- |
| `agent.model` | `defaults.agent.model` | B3 |
| `agent.models.coder`, `.reviewer`, `.background`, `.vision`, `.diagnose` | `defaults.agent.models.<role>` | B3 |
| `agent.crafts.<craft>.model` (the five crafts) | `defaults.agent.crafts.<craft>.model` | B3 |
| `agent.levels.l1.model`, `.l2.model`, `.l3.model` | `defaults.agent.levels.<lN>.model` | B3, D3291 |
| `ab.rate` | `ab.repos.<owner/name>`, the layer's own top-level `ab:` block | B9 |
| `rigor.default` | `defaults.rigor.repos.<owner/name>` | B11 |
| `rigor.areas` | a member area of the project's, `defaults.rigor.areas` | B11 |
| `model_grades.strong.model`, `model_grades.strongest.model` | `defaults.model_grades.<grade>.model` | L13, D3299 |

Nineteen keys, and three things are deliberately not on the list:

- **A `.access` row.** `agent.crafts.<craft>.access` and `model_grades.<grade>.access` are
  model-credentials-routing's retired keys, still honoured until its release 2 (its X2); they are
  `ScopeRow.Retired` rows and `RetiredKeyNotes` names them. The two lists hold the two halves of a
  craft and of a grade apart — the model lifts, the credential or pool does not — and
  `TestLiftedKeysAreNotMCRsRetiredKeys` fails if either half crosses over.
- **`agent.levels.<lN>.effort`, `.none`, `.focus`** (and every other B4 key): B4 is tighten-only and
  migrates nothing, so those values stay in the repository and the merge judges them (RT1, RT2).
- **`.fugaro/recipes/`** (B16): it is a folder and no key, so PR3 (the runner stops reading it), PR5
  (the cleanup diff) and PR1 (the launcher's lookup) own it.

**`ab.rate` is the one path two documents share.** A repository's file writes it as its A/B opt-out;
a project layer's own top-level `ab:` block writes it as the project's sampling rate, which AB1 puts
`ab.repos` beside. A lifted key is one **a repository's file** must stop writing, and the list is
read for that document alone: what a layer may hold is `Scopes`' to say, as it is today. This is also
why the list carries no document dimension — every entry is lifted in the same document.

## (b) The note, and the sentence

`config.LiftedKeyNotes(data []byte) []config.RetiredKeyNote` is every lifted key `data` still writes,
in path order — a stable order for a diff and for a message — with the path as the document writes
it and the sentence a person reads. `data` is a repository's own `fugaro.yaml`; a project layer is
never handed here, since its `defaults.agent.model` and its `ab.repos` block are the keys' new homes
and not lifted keys at all.

It **decides nothing**, exactly as `RetiredKeyNotes` does not: bytes this cannot read as YAML answer
no note, and release A's lifted keys still decode and still validate, so a caller prints a note and
never a refusal (D3063, kept). Release B is the release that refuses, with the same `Why`.

`LiftedKey.Why` is **the one refusal text release B prints**, at `validate`, `update` and a
default-branch launch (XB1, XB2, XB6). Each sentence opens with the key's own path, so it stands
alone in a message, and it names the command that states the key in its new home:

```
agent.models.coder is the project's choice (D3237): remove it from fugaro.yaml; the project sets it
with `fugaro models use --layer project`.

ab.rate is the project's (D3237): remove the ab: block from fugaro.yaml; opt this repository out with
`ab.repos.<owner/name>: 0` in the project layer.

rigor.default is the project's (D3237): this repository's level is `rigor.repos.<owner/name>` in the
project layer; set it with `fugaro rigor set <mode> --repo <owner/name>`.
```

`<owner/name>`, `<mode>` and `<glob>` are the repository's own values, which a list read for a
document — not for a repository — cannot fill in; the sentence is a template a person completes with
the repository they are looking at. B3's fourteen keys share one sentence because they are one move
(`liftedModel`); the four others are their own. Nothing here asks a question, and nothing here is a
confirmation: release A drops and names, release B refuses, and both print the sentence
([docs/no-prompt-policy](../no-prompt-policy/)).

## (c) What each side may rely on

| What | Where it holds |
| --- | --- |
| **The list is the only answer to "is this key lifted."** One key, one row: a consumer looks a path up in `LiftedKeys` and never writes a second list, a `switch` of its own or a `strings.HasPrefix` over the blocks | S1's drop, S4's refusal, DK1's grades |
| **The sentence is the one refusal text.** A key's `Why` is what release B prints, unchanged, at every one of the three refusals | XB1, XB2, XB6, S2, S5 |
| **`config.Resolve` is the only place that drops.** Both arms of it, on every ref, before the merge; no other reader needs a rule, and no reader may honour a lifted key by accident | S1, I1 (the reader test) |
| **A lifted key is never `Retired`.** `RetiredKeys` is model-credentials-routing's R3 list exactly, and its rows stay honoured until its X2; a lifted key's `Scopes` row keeps its `In` bits and gains no `Retired` bit until release B takes `InRepo` off it (XB1) | `TestLiftedKeysAreNotMCRsRetiredKeys`, `TestEveryRetiredKeyIsNamedWhereItIsWritten` |
| **Every lifted row admits `InRepo`.** A key a repository's file may not write is not one this list can lift out of it, so a row that loses its `InRepo` bit is a lifted key whose note can never be printed | `TestLiftedKeysAreNotMCRsRetiredKeys` |
| **The list is exhaustive and in path order**, so `LiftedKeyNotes` answers exactly as many notes as there are keys a file writes, and a diff of the notes is stable | `TestLiftedKeyNotesNamesEveryWrittenKey` |
| **The four homes are the spec's, not this contract's to invent.** Which project key holds a moved value — and how a planner writes it — is MC1's, AB3's, RG2's and PR4's | spec §5, D3290, D3303 |

The list is deliberately **read, not derived**: it is not `Scopes` filtered (R1 makes it its own
list, and a derived one would move the moment a scope sentence changed) and not a `Retired` bit
(which would break MCR's pin). A key added to it is added here, with its home and its sentence, and
the test's own `wantLifted` spells the same list out so the addition cannot be half-made.

## (d) What is not decided here

- **What a moved value becomes in the project.** The majority rule for a silent project, the clash
  rule for a recipe name and the rename-on-collision of a rigor area are the planners' (D3290,
  D3301, R4); this contract states only where a key goes.
- **How the drop is made.** Which arm of `config.Resolve`, what it records in the `Resolution` and
  how the repository-own fields are computed after it are S1's (R2). This contract fixes the list S1
  reads, not the code that reads it.
- **The gates.** Which image-age constants a launch judges, and whether a pre-L8 runner is kept off a
  layer that writes `ab.repos`, are S5's and AB1's (R9, R16).
- **The writers' refusals.** Which command refuses which key, and what `models use` defaults to, are
  S4's (R15).
- **The tighten-only keys.** B4's table, its baseline and the recorded "ignored" lines are RT1's and
  RT2's, and none of those keys is on this list.
