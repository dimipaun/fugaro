# `fugaro rigor`: reconciliation against the code

Read against `origin/main` at `709574da` (2026-10-31). Each finding is **Exists**, **Missing** or
**Conflicts**, with its consequence for the spec or the plan.

The decisions were renumbered to **D830–D839** in an earlier pass: the merged Coordinator v2 design
(`docs/console-coordinator-v2`) holds **D810–D829**, which is the block this folder had taken while
that design was an open pull request. Nothing else about the findings changed with the merge — and
nothing changed with `709574da` either, which is a coordinator read-batching change on keys this
folder never names.

- **No line numbers anywhere in this folder**, for the reason
  `docs/rigor-modes/reconciliation.md` gives: a line number is wrong the first time anyone edits the
  file above it, and a path and a symbol survive. Every claim below names the file and the symbol, so
  a reader checks it with `git grep` rather than trusting this file — and, like that folder, this one
  changed nothing in the repository: the files it says must change are the *plan's* files.

## 1. What exists

**F1: Exists. The vocabulary and the one resolver.** `internal/rigor/mode.go` holds the five modes
(`Modes`), the built-in default (`const Default = Pro`), `Known`, `Parse`, `Rank` and `Strictest`.
`internal/rigor/resolve.go` holds the layers a resolution may name (`Layers`), the configuration the
resolver reads (`Config`), `Resolve` and `Resolved`, and `internal/rigor/select.go` holds `Select` and
`ModesOf`. The package comment states the precedence once and says no other package may order two
modes. *Consequence:* `rigor show` calls `Resolve` and prints its answer; it states no rule of its
own, and `from` comes from the package's own set (spec §7).

**F2: Exists. The two layers' rigor shapes.** `config.LayerRigor` (`internal/config/layer.go`) is
`defaults.rigor` — `default` and `areas` — and `config.Rigor` (`internal/config/config.go`) is
`fugaro.yaml`'s `rigor:` block with the same two keys. Both are settable in both layers
(`internal/config/scope.go`'s two `rigor.*` rows), which is `docs/project-layer.md` §5's two rows.
`ProjectLayer.SetsRigor()` (`internal/config/layer.go`) answers whether the layer writes the key at
all, the question a pre-0.11.0 runner refuses on. *Consequence:* `set` writes one of two keys in one
of two files, and nothing new is modelled.

**F3: Missing. The project layer's local source file has no home.** `fugaro config publish FILE`
takes an arbitrary path (`internal/cli/config_publish.go`), and nothing records which file an
installation publishes from. The local project config (`~/.config/fugaro/projects/<project>.yaml`)
does not carry the layer and has no key pointing at it; the layer cache
(`$XDG_CACHE_HOME/fugaro/project-layers/<project>.json`, `internal/localcfg/layercache.go`) is written
*after* a publish (`localcfg.SaveLayerCache`) and holds the **published** object's text, so it is a
cache of the cloud's copy and never the owner's source. *Consequence:* a command that edits "the file
the owner publishes from" cannot be written before that file has a name. **D830 answers it by
convention, not by a new key** (SD4, answered): the file is
`~/.config/fugaro/projects/<project>-layer.yaml` — the project's own name beside its config — so
there is no flag and no config key to add, and nothing to record. R0 builds the path and the door;
R1 gives the file its first content on an installation that has none.

**F4: Missing. `fugaro update` does not publish the project layer.** The converge's stages end at
`Policy` (`internal/initflow/stage.go`), and that stage migrates the **policy node** in place
(`internal/cli/init_policy_migrate.go`), deriving members from the local config; `publishLayer`
(`internal/cli/config_publish.go`) is reached only from `fugaro config publish`, whose fan-out to
each repository's `builds/<slug>/project-layer.yaml` is `fanOutLayer` (same file). No stage reads,
publishes or fans out a layer file. *Consequence:* without a new stage the owner's next step after
`rigor set` is `fugaro config publish FILE` — a second command, which the HARD RULE forbids. **D831
answers it** (SD4, answered): a `Layer` stage after `Policy`, which **seeds** the local file from the
published layer when it is absent — so an installation that has never had one gets a file that is
exactly what is in effect — and then publishes the file when it is **`unpublished`**, the shared
state word R0's `config layer`, R3's `show` and doctor's check all print. R1 builds both halves.

**F5: Exists. The resolution the spec's `from` column mirrors.** The run record's `rigor.from` is
already `area`, `repo`, `project`, `default` or `request`, never `reached`
(`schemas/result.schema.json`'s `rigor` block, `internal/runstore`'s `KnownRigorFrom`, D24) and
`console/docs/json-contract.md` §5 (the `runs ls --json` block, and the degraded feed's body) states
the same five words. *Consequence:* `rigor show` prints the same vocabulary minus the two values no
launcher can produce (`request`, `reached`), so a person comparing the two surfaces reads one set of
words. And the answer itself comes from **`rigor.Resolve`** (`internal/rigor/resolve.go`) — the one
function that orders the modes and settles the precedence, called per repository, the way the
settings design's row calls it. Neither surface derives a mode a second way.

**F6: Exists. The wrappers the CLI resolves through.** `config.Config.RigorConfig()`
(`internal/config/config.go`) returns `internal/rigor.Config` from a resolved config — the project
layer's half (`config.Config.ProjectRigor`) and the repository's own block kept apart, because both
area lists are in effect at once (D20c). `fugaro run` already reads a checkout's config this way for
its `--mode` note (`internal/cli/run.go`'s `noteRigorRequest`). *Consequence:* `rigor show` builds
one `rigor.Config` per repository from this wrapper rather than reading the YAML itself.

**F7: Missing. There is no `fugaro rigor` command, and no surface resolves a mode for a repository
that is not running.** `fugaro runs ls`'s `MODE` column and `fugaro diagnose`'s `Rigor:` line read a
**record** of a past run; `fugaro config show` resolves the checkout's config with each value's
source but does not carry the rigor resolver's answer, and `doctor`'s layer checks report the layer,
not the mode. *Consequence:* `show` is new, and the four sections of spec §2 have no predecessor to
extend.

**F8: Exists. `--mode` and its vocabulary check.** `fugaro run --mode` is the run's own request (its
flag help and `internal/cli/run.go`'s `rigorFlags`), validated against the closed vocabulary before
any cloud work (`rigorFlags`), with `--path` declaring the paths area rules match. A request that
cannot take effect is reported once per launch by `noteRigorRequest` (same file), which is the
raise-only rule D6 stated to the launcher. *Consequence:* `set` reuses the same vocabulary check and
the same spelling of the list (`rigor.ModeList`), so a near miss is refused the same way in both
commands.

**F9: Missing. The mode floors are not built.** D8, D18 and D21 put the sensitive-path floors
outside every layer; `docs/rigor-modes/implementation-plan.md` T1.5 is the task that builds them
(`internal/rigor/floors.go`), and no such file exists: `internal/rigor` has no floor name, and
`Resolve`'s comment says the floors "are outside every layer and which this function does not carry"
(`internal/rigor/resolve.go`). *Consequence:* `show` must not display a floor set that does not
exist. Its floors section prints the review floor (F10) and one line saying the mode floors are not
built, and E2 is the dependency that adds them.

**F10: Exists. The review floor.** `localcfg.Config.ReviewFloor` (`internal/localcfg/localcfg.go`,
D722/G1) is per rigor mode, validated at load (`Config.ReviewFloorPolicy`, same file, over
`policy.ParseFloor`) and published into the policy node, where `fugaro project policy show` prints it
and a run fails closed on it. It never enters the shared config a launcher can write
(`internal/cli/sharedcfg.go`'s `sharedForbidden`). *Consequence:* it is the one floor `show` can
print today, from the published policy; the spec's §2.4 says so, and `set` states that it cannot
lower it.

**F11: Exists. The typed confirmation.** `confirmTyped` and `confirmTypedStrict`
(`internal/cli/budget.go`) ask for the project's name, and `--yes` does not stand in for the strict
form, which is what `fugaro project repos rm` uses and why (`confirmRemovalStrict`,
`internal/cli/project_cmd.go`). *Consequence:* the lowering confirmation of D832 is this helper, not
a new one.

**F12: Exists. The write helpers and a line-edit precedent.** `lineDiff`, `backupFile`
(`<file>.bak-<timestamp>`, mode 0600) and `writeFileAtomic` (`internal/cli/init.go`) are the
diff/backup/write triple `fugaro restore` (`internal/cli/restore.go`, which calls `backupFile`,
`writeFileAtomic` and `lineDiff`) and the `models` plan (its D790) already use.
`setGCPProjectLine` (`internal/cli/init_fugaroyaml.go`) is a precedent for a targeted edit of one
line of `fugaro.yaml` that keeps the rest. *Consequence:* the commands reuse all four; only the
document-tree edit itself is new, and it is **not rigor's to write**: it is the editor task's
`internal/yamledit` (the README's joint order, position 1), and R2 is the thin caller `EditRigor`
over it. That package **does not exist on `main` yet** — no `internal/yamledit` directory, and
neither does the path an earlier pass of this folder proposed for it, `internal/localcfg/edit.go` —
so this folder names the package and the task that lands it, never an existing file. Nothing here is
a fifth helper under a rigor name.

**F13: Conflicts. `hotfix` has no gate, and the gate D12 names does not exist.** D12 requires
`fugaro run --mode hotfix` to be "gated on the base branch's configuration" (`rigor.hotfix`), and
`internal/cli/followup.go` repeats that sentence when it refuses a follow-up's hotfix. No
`rigor.hotfix` key exists anywhere: `schemas/fugaro.schema.json`'s `rigor` block carries `default`
and `areas` only, `internal/config/scope.go` has two rigor rows and neither is it, and no code reads
one. So a first run's `--mode hotfix` is refused by nothing but the raise-only rule. *Consequence:*
this changes what D834 may *claim*, and §3 says how: refusing `set hotfix` as a **default** is right
on its own terms and does not close the gate, and this design does not pretend otherwise. The
missing opt-in is reported in §4 and is another work's, not a gate this design may invent.

**F14: Missing. Nothing refuses two area rules with one glob.** `rigor.Area.Validate`
(`internal/rigor/area.go`) checks one rule's name, glob and mode — `MaxAreaName`, `MaxAreaPattern`
and `MaxAreas` are its bounds (`internal/rigor/area.go`, `areaNameRE` for the name) — and
`internal/config`'s validation of the two blocks runs it per rule. Two rules with the same `match` in
one layer are accepted and resolve through `Strictest`, so the weaker one's intent is silently
discarded. *Consequence:* `set --area` refuses to create such a pair (D838), the loader's own silence
is reported in §4, and the plan's follow-up task **R8** closes it where the validation already runs —
so a pair written by hand cannot sit in a layer either.

**F15: Exists. The coding-agent marker.** `agentMarker` and `agentEnv` (`internal/cli/initsecrets.go`)
wrap `agentenv.Marker`, and `fugaro config publish` refuses on it before anything else
(`internal/cli/config_publish.go`). The wording that refusal uses — and the one the `models` commands
reuse (SD9, D789 there) — is not "refuse before saying anything": the command prints **the diff and
the command it would have run** and writes nothing, `--yes` included, so the person reading the
transcript sees exactly what was asked for and exactly what was not done. *Consequence:* `set`/`unset`
of the **project layer** write a file outside every repository and are refused in those words there
(spec §3 step 3), and §4's lowering confirmation inherits them, because a typed project name is a
prompt a session cannot answer (spec §4); the
`--repo` form writes a repository's own file and is allowed. D830's answer keeps that split, and
spec §3 states it where the steps are.

**F16: Exists. Each repository's file can be read at its ref with no network.** The remembered
checkouts (`internal/localcfg/checkouts.go`, `CheckoutsVersion`, `LoadCheckouts` and
`Checkouts.ProjectCheckouts`) record where this machine has each repository; `readFugaroYAMLAtRef`
(`internal/cli/run.go`) reads a `fugaro.yaml` **at a given ref** from such a checkout with no fetch,
and `agentAtRef` (same file) is a caller of it — the read `show`'s per-repository rows need, because
it is the **base branch** that must be read and not the working tree. The neighbouring
`checkoutConfig` (`internal/cli/cloud.go`) is **not** that read and does not stand in for it: it
parses the checkout the command is standing in, at whatever state that tree is in — the current
file, not an arbitrary ref — and `cachedProjectLayer` (same file) resolves the layer for a checkout.
*Consequence:* `show`'s per-repository rows are a local read at a ref, and a repository with no
checkout is shown as unknown (D839, spec §2.3) rather than read from the working tree, which would
answer for this machine instead of for the branch the run will use.

**F17: Exists. The console reads no rigor configuration.** Its sources are the CLI documents listed
in `docs/design/desktop-console.md` §2.1; rigor reaches it only through each run's `rigor` block in
`fugaro watch --json` (`console/docs/json-contract.md` §5). The console's project view is planned,
not built: it is plan **P6** in `docs/coordinator/implementation-plan.md` (§5, "The console's
project view"), which `docs/console-dependency-graph/implementation-plan.md` cites as
"Coordinator **P6**" — one plan, one folder, and this document names the folder that holds it.
*Consequence:* no console task in this plan (D836).

## 2. What the commands must not become

**F18: The gap this design exists to close is the owner's, and it is stated as a HARD RULE.**
`CLAUDE.md`: "one missed or broken update loses the user's trust … a release must not ship an
operator step that is another command … if a change needs one, `update` learns to do it in the same
release". A project default that reaches runs only after `fugaro config publish FILE` is exactly
such a step, and today it is the **only** way the project layer moves. *Consequence:* the plan's
ordering is not "commands first, publish later": **R1 (the update stage) is in the Needs column of
R4 and R5**, so a `set` task cannot be built without the stage that publishes what it writes — the
HARD RULE held mechanically, not by a paragraph — and R1 itself sits at position 4 of the README's
joint order, before the shared write half (settings S5) and the commands that call it.

## 3. Decisions D830–D839

The recommendation comes first; the alternative and **its cost** are the second column. **D830 and
D831 are answered** — the owner's SD4 ruling of 2026-10-31, and the spec is written to that answer;
**D832–D839 are open**, each with the recommendation the spec and the plan are written to.

D830's answer is one of the rulings this design **shares with the settings design**, and those are
stated once, in identical words, in the [README](README.md) ("the joint order, and the rulings the
rigor and settings designs share"): one editor package `internal/yamledit` owned by the editor task
and landing first, the state word **`unpublished`**, the wording `unknown (no checkout on this
machine)`, and the joint order the two designs land in. Where a decision below uses one of those
words it means that ruling and nothing local to this folder.

| D | Question | Recommendation | The alternative, and its cost | Answer |
| --- | --- | --- | --- | --- |
| **D830** | Where does `rigor set` write the project default, and where does the update read it from? | **The one conventional local layer file, `~/.config/fugaro/projects/<project>-layer.yaml`** — the project's own name beside its config — with **no `--layer` flag and no new config key**: the path is derived, so there is nothing to record and nothing to resolve. The file is created only when the project has **no** published layer; when it is absent while a layer **is** published, that is a refusal whose remedy is `fugaro update`, which seeds it (D831). | **(b) A `--layer FILE` flag:** every later `update` has to read the same flag from somewhere, so the flag implies either the config key below or a state where the file the owner edits is not the file `update` publishes. **(c) A `project_layer:` key in the local project config:** a second place a path lives, for a file whose name is already fixed by the project's own name — and one more key to migrate. **(d) A new top-level `rigor:` block in the local project config, merged into the layer by `update`:** two sources of truth for one key, `update` gains a merge whose result no file shows, and the layer the owner publishes stops being the layer they read. **(e) `set` writes the published object directly:** a second door to the cloud outside `update`'s confirmation, migration and version checks — the thing D780 already refused for `models` | **Answered 2026-10-31 (SD4): option (a), with the flags dropped** |
| **D831** | How does the change reach runs: through `update`, or through a publish step? | **A new `Layer` stage in `fugaro update`**, after `Policy`, with two halves: it **seeds** the local file from the published layer when the file is absent (so the first `update` after this ships leaves an installation with a file that is exactly the layer in effect), and when the file is **`unpublished`** — differs from the published object, by the same sha256 comparison `config layer`, `show` and doctor make — it shows the diff, publishes through `publishLayer` and fans out through `fanOutLayer`, behind update's one confirmation. A change to an executable key still needs `--executable-changes`. `fugaro config publish FILE` stays as the explicit door, its FILE argument now defaulting to the same conventional path. | **Keep `config publish FILE` as the only door:** the owner's edit stays a second command, which the HARD RULE forbids (F18) — and it is the one command the update rule is written about. **Publishing without seeding:** an installation that has never had a local file stays without one, so `rigor set`'s first act is the refusal of D830 and the owner's "one command" is two after all. Publishing *only* rigor and not the rest of the file: two writers for one object, and a state where the published layer is half the file | **Answered 2026-10-31 (SD4): the recommendation, seeding included** |
| **D832** | What confirmation does lowering a mode need? | **The typed project name** (`confirmTypedStrict`, F11) whenever the new mode ranks below what the layer resolves today; `--yes` does not stand in for it. Equal or higher is an ordinary y/N. The review floor, the sentence that this command cannot lower it and the command that can (`fugaro rigor floor`, SD10) print beside the diff. A coding agent's session cannot answer a typed name, so there the command prints the diff and the command it would have run and writes nothing (SD9, spec §4). | **`--yes` or a y/N for every direction:** a lowered project default is a whole project reviewing less, from a flag typed in advance, in a script, possibly by an agent — the direction that must never be one keystroke. **Refuse lowering entirely:** contradicts D21, which says a narrower layer may lower and a project that wants a floor declares one | **Open.** |
| **D833** | Does `--repo R` open a pull request or write the working tree? | **Write the working tree of the repository's checkout, after a diff and a confirmation; never commit or push.** The user's own branch and review carry it, exactly as `models use` (D781 there). | **Open a pull request from the CLI:** needs a write path to GitHub and Bitbucket on the developer's machine with the user's credentials, which the CLI does not have today, and it is one more place that pushes | **Open.** |
| **D834** | May `set` write `hotfix`? | **No, in any of the four forms** — not as a project default, not as an area rule, not in a repository's block, not as a `--json`-silent value. The refusal names `fugaro run --mode hotfix`, the operator's own door (D12). **This refusal is owed on its own terms and does not close D12's gate:** F13 shows the base-branch opt-in does not exist, so today a hand-written `rigor.default: hotfix` reaches a mode that tolerates almost everything with nothing gating it, and `--mode hotfix` on a first run is gated by nothing but the raise-only rule. Closing that is the plan's task **R7**, whose first step is a question to the owner; this design must not be read as evidence that the gate exists. | **Allow it as a default:** `hotfix` ranks below every on-axis mode (`Rank`), so a project defaulting to it would be a project whose runs tolerate nearly everything, reachable by anyone who can write the file — and D12's whole point is that the mode with the least review must not be the easy one. **Allow it with a typed confirmation:** still writes a default D12 says is an operator's own run, and the confirmation would be asked once for a state every later run inherits. **Wait for F13's gate before refusing:** leaves the one default that must never be written writable by a one-word command, to fix a gap in a different check | **Open.** |
| **D835** | How stable is `rigor show --json`? | **A versioned document (`"version": 1`) with a JSON Schema in `schemas/`; members are only added within a version, and a renamed or re-meant member is a new member.** `from` is bounded by `internal/rigor`'s own set. | **Unversioned:** the console and any script that reads it break on the first change with nothing to check against (the same alternative D784 refused for `models ls --json`) | **Open.** |
| **D836** | Does the console show the resolved project default? | **Not in this work.** When the console's project view ships (P6, F17) it reads `fugaro rigor show --json` read-only, or shows nothing; the console never reads `~/.config/fugaro` or the bucket, and the shape it reads is documented in `console/docs/json-contract.md` by the task that builds the screen. | **A console task now:** there is no project view to put it in (F17), so the task would either build a screen the redesign replaces or read a document through `fugaro doctor`'s shape, which is not that document | **Open.** |
| **D837** | What does `unset` write? | **It removes the key or the rule, so the layer below decides again** — and a no-op with a sentence, exit 0, when there is nothing to remove. | **Write the built-in default (`pro`) in place of the key:** freezes Fugaro's own default into the owner's file, indistinguishable from a deliberate pin, and it silently becomes wrong the day the built-in default changes. **Error when there is nothing to remove:** makes an idempotent script fail, for a state that is already what the caller asked for | **Open.** |
| **D838** | How is an area rule addressed, and what does `--area` require? | **By its glob.** `set --area GLOB` replaces that rule's mode; a glob the layer does not carry adds a rule and then **requires `--name`** (it is what the run record and the pull request print); `--name` on an existing rule renames it. A second rule with the same glob in one layer is refused (F14). | **Address by name:** names are not unique, the loader does not enforce uniqueness, and a rename would silently retarget the command. **Derive the name from the glob:** writes a name the owner never chose into three surfaces a person reads, and two globs can derive one name | **Open.** |
| **D839** | Where does `show` read each repository's own block? | **The remembered local checkouts, at the base branch, with no fetch** (F16); a repository with no checkout is shown as unknown (spec §2.3 pins the text and the JSON) and is never guessed. | **Read through the git provider's API:** a network call with a credential on the developer's machine, for a listing — and it would still be stale against the branch the run will read. **Resolve only the project layer for such a repository:** prints a mode that may be neither the right value nor the right reason, with nothing saying so | **Open.** |

## 4. Noticed while reconciling, and not this work's

Reported rather than fixed, because each needs its own decision and its own reviewer.

- **F13's missing gate — the plan's task R7.** D12's base-branch opt-in (`rigor.hotfix`) is named by
  the follow-up refusal (`internal/cli/followup.go`) and by no code path: no `rigor.hotfix` key in
  `schemas/fugaro.schema.json`, no row in `internal/config/scope.go`, nothing that reads one. What
  `hotfix` **does** today, once something resolves it, is in three places and is not nothing:
  `internal/rigor/label.go` gives it the security gate alone (every other band is tolerated),
  `internal/rigor/filing.go` files one backlog item for what it deferred, and its levels and rounds
  are **Pro's** — `internal/rigor/levels.go`'s row is Pro's set (**O10**, the other design's ruling:
  `docs/review-levels-ab-jobmax/reconciliation.md` §4, taken by the owner on 2026-10-30 — hotfix
  takes Pro's levels, L2 on with L1 as Pro has it, because the spec set defaults for POC, Startup,
  Pro and Critical only) and `internal/rigor/rounds.go` gives it 1 round, which `minRoundCeiling`
  clamps to today's default of 2 while **O3** is open (the rigor-modes design's own open item,
  `docs/rigor-modes/reconciliation.md` — whether a mode may reduce the round ceiling). So a
  hand-written `rigor.default: hotfix` in a layer or in a `fugaro.yaml` already reaches a mode that
  tolerates almost everything, with no gate at all, while `fugaro run --mode hotfix` cannot take
  effect (raise-only, D6). This design refuses to *write* hotfix as a default — a different check,
  owed on its own terms (D834) — and it does not close that gate: R7 does, and its first step is a
  question to the owner rather than code (see the plan).
- **F14's silent pair — the plan's task R8.** Two area rules with one glob in one layer are accepted
  by the loader and resolved by `Strictest`, so the weaker rule's intent is discarded with nothing
  said. A load-time refusal belongs with the validation that already runs `Area.Validate` per rule
  (`internal/config`), not with a command; R8 puts it there.
- **The review floor has no command — and it is SD10's.** `review_floor` is hand-edited in the local
  project config and published by `fugaro project policy publish` — the same shape of gap this design
  closes for the rigor default, on the key that is a floor rather than a default (F10). The owner
  ruled (SD10) that it gets its own subcommand of this family, **`fugaro rigor floor show|set|unset`**,
  which is why `show`'s floors section (spec §2.4) and `set`'s floor sentence name it, and why the
  command group R3 builds leaves room for the subgroup. The rigor command itself **gains no floor
  flag**: the floor is changed by the subcommand that is about floors and by nothing else.
- **`governance` has the same gap, one key over.** `docs/task-governance/implementation-plan.md`
  says the layer file published by `fugaro config publish FILE` is governance's only home. R1's
  layer stage removes the second command for *every* project-layer key at once; nothing else here
  touches governance.
