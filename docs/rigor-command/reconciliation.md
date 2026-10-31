# `fugaro rigor`: reconciliation against the code

Read against `origin/main` at `eef99df3` and the 8 pull requests open on 2026-10-31. Line numbers
are that commit's. Each finding is **Exists**, **Missing** or **Conflicts**, with its consequence
for the spec or the plan.

## 1. What exists

**F1: Exists. The vocabulary and the one resolver.** `internal/rigor/mode.go` holds the five modes
(`Modes`, `:36`), the built-in default (`const Default = Pro`, `:46`), `Known` (`:50`), `Parse`
(`:56`), `Rank` (`:75`) and `Strictest` (`:105`). `internal/rigor/resolve.go` holds the layers a
resolution may name (`:20`–`:43`, `Layers` at `:55`), the configuration the resolver reads
(`Config`, `:68`), `Resolve` (`:164`) and `Resolved` (`:135`), and `internal/rigor/select.go` holds
`Select` (`:28`) and `ModesOf` (`:66`). The package comment states the precedence once and says no
other package may order two modes. *Consequence:* `rigor show` calls `Resolve` and prints its
answer; it states no rule of its own, and `from` comes from the package's own set (spec §7).

**F2: Exists. The two layers' rigor shapes.** `config.LayerRigor` (`internal/config/layer.go:133`)
is `defaults.rigor` — `default` and `areas` — and `config.Rigor` (`internal/config/config.go:111`)
is `fugaro.yaml`'s `rigor:` block with the same two keys. Both are settable in both layers
(`internal/config/scope.go:129`, `:130`), which is `docs/project-layer.md` §5's two rows.
`ProjectLayer.SetsRigor()` (`:108`) answers whether the layer writes the key at all, the question a
pre-0.11.0 runner refuses on. *Consequence:* `set` writes one of two keys in one of two files, and
nothing new is modelled.

**F3: Missing. The project layer's local source file has no home.** `fugaro config publish FILE`
takes an arbitrary path (`internal/cli/config_publish.go:25`, `:39`) and nothing records which file
an installation publishes from. The local project config (`~/.config/fugaro/projects/<project>.yaml`)
does not carry the layer and has no key pointing at it; the layer cache
(`$XDG_CACHE_HOME/fugaro/project-layers/<project>.json`, `internal/localcfg/layercache.go:18`) is
written *after* a publish (`SaveLayerCache`, `:45`) and holds the **published** object's text, so
it is a cache of the cloud's copy and never the owner's source. *Consequence:* a command that edits
"the file the owner publishes from" cannot be written before that file has a name. D810 answers
where it lives; R0 builds it.

**F4: Missing. `fugaro update` does not publish the project layer.** The converge's stages end at
`Policy` (`internal/initflow/stage.go:97`), and that stage migrates the **policy node** in place
(`internal/cli/init_policy_migrate.go`), deriving members from the local config; `publishLayer`
(`internal/cli/config_publish.go:99`) is reached only from `fugaro config publish`
(`:70`), whose fan-out to each repository's `builds/<slug>/project-layer.yaml` is `fanOutLayer`
(`:357`). No stage reads, publishes or fans out a layer file. *Consequence:* without a new stage the
owner's next step after `rigor set` is `fugaro config publish FILE` — a second command, which the
HARD RULE forbids. D811 answers it; R1 builds it, and it is the plan's first external dependency.

**F5: Exists. The resolution the spec's `from` column mirrors.** The run record's `rigor.from` is
already `area`, `repo`, `project`, `default` or `request`, never `reached`
(`schemas/result.schema.json:373`, `internal/runstore`'s `KnownRigorFrom`, D24)
and `console/docs/json-contract.md` §6.1 states the same five words. *Consequence:* `rigor show`
prints the same vocabulary minus the two values no launcher can produce (`request`, `reached`), so
a person comparing the two surfaces reads one set of words.

**F6: Exists. The wrappers the CLI resolves through.** `config.Config.RigorConfig()`
(`internal/config/config.go:131`) returns `internal/rigor.Config` from a resolved config — the
project layer's half (`ProjectRigor`, `:146`) and the repository's own block kept apart, because
both area lists are in effect at once (D20c). `fugaro run` already reads a checkout's config this
way for its `--mode` note (`internal/cli/run.go:1174`). *Consequence:* `rigor show` builds one
`rigor.Config` per repository from this wrapper rather than reading the YAML itself.

**F7: Missing. There is no `fugaro rigor` command, and no surface resolves a mode for a repository
that is not running.** `fugaro runs ls`'s `MODE` column and `fugaro diagnose`'s `Rigor:` line read a
**record** of a past run; `fugaro config show` resolves the checkout's config with each value's
source but does not carry the rigor resolver's answer, and `doctor`'s layer checks report the layer,
not the mode. *Consequence:* `show` is new, and the four sections of spec §2 have no predecessor to
extend.

**F8: Exists. `--mode` and its vocabulary check.** `fugaro run --mode` is the run's own request
(`internal/cli/run.go:281`), validated against the closed vocabulary before any cloud work
(`rigorFlags`, `:1155`), with `--path` declaring the paths area rules match. *Consequence:* `set`
reuses the same vocabulary check and the same spelling of the list (`rigor.ModeList`), so a near
miss is refused the same way in both commands.

**F9: Missing. The mode floors are not built.** D8, D18 and D21 put the sensitive-path floors
outside every layer; `docs/rigor-modes/implementation-plan.md` T1.5 (`:529`) is the task that
builds them (`internal/rigor/floors.go`), and no such file exists: `internal/rigor` has no floor
name, and `Resolve`'s comment says the floors "are outside every layer and which this function
does not carry" (`internal/rigor/resolve.go:156-158`). *Consequence:* `show` must not display a
floor set that does not exist. Its floors section prints the review floor (F10) and one line saying
the mode floors are not built, and E2 is the dependency that adds them.

**F10: Exists. The review floor.** `localcfg.Config.ReviewFloor` (`internal/localcfg/localcfg.go:278`,
D722/G1) is per rigor mode, validated at load (`ReviewFloorPolicy`, `:1205`) and published into the
policy node, where `fugaro project policy show` prints it and a run fails closed on it. It never
enters the shared config a launcher can write (`internal/cli/sharedcfg.go`'s `sharedForbidden`).
*Consequence:* it is the one floor `show` can print today, from the published policy; the spec's
§2.4 says so, and `set` states that it cannot lower it.

**F11: Exists. The typed confirmation.** `confirmTyped` (`internal/cli/budget.go:184`) and
`confirmTypedStrict` (`:210`) ask for the project's name, and `--yes` does not stand in for the
strict form, which is what `fugaro project repos rm` uses and why (`confirmRemovalStrict`,
`internal/cli/project_cmd.go:2159`). *Consequence:* the lowering confirmation of D812 is this
helper, not a new one.

**F12: Exists. The write helpers and a line-edit precedent.** `lineDiff`
(`internal/cli/init.go:2641`), `backupFile` (`:2578`, `<file>.bak-<timestamp>`, mode 0600) and
`writeFileAtomic` (`:2614`) are the diff/backup/write triple `fugaro restore` and the `models` plan
already use (`restore.go:963`, `:970`). `setGCPProjectLine` (`internal/cli/init_fugaroyaml.go:61`) is a precedent for
a targeted edit of one line of `fugaro.yaml` that keeps the rest. *Consequence:* the commands reuse
all four; only the project layer's document-tree edit is new (R2).

**F13: Conflicts. `hotfix` has no gate, and the gate D12 names does not exist.** D12 requires
`fugaro run --mode hotfix` to be "gated on the base branch's configuration" (`rigor.hotfix`), and
`internal/cli/followup.go:720` repeats that sentence when it refuses a follow-up's hotfix. No
`rigor.hotfix` key exists anywhere: `schemas/fugaro.schema.json`'s `rigor` block (`:198`) carries
`default` and `areas` only, `internal/config/scope.go` has two rigor rows and neither is it, and no
code reads one. So a first run's `--mode hotfix` is refused by nothing but the raise-only rule.
*Consequence:* refusing `set hotfix` as a **default** (D814) is what this work owns and it is not
enough by itself; the missing opt-in is reported in §4 and is another work's, not a gate this
design may invent.

**F14: Missing. Nothing refuses two area rules with one glob.** `rigor.Area.Validate`
(`internal/rigor/area.go:63`) checks one rule's name, glob and mode — `MaxAreaName` (40),
`MaxAreaPattern` (200) and `MaxAreas` (100) are its bounds (`:37`, `:39`, `:43`) — and
`internal/config`'s validation of the two blocks runs it per rule. Two rules with the same `match`
in one layer are accepted and resolve through `Strictest`, so the weaker one's intent is silently
discarded. *Consequence:* `set --area` refuses to create such a pair (D818) and the loader's own
silence is reported in §4.

**F15: Exists. The coding-agent marker.** `agentMarker` (`internal/cli/initsecrets.go:141`) and
`agentEnv` (`:146`) wrap `agentenv.Marker`, and `fugaro config publish` refuses on it before
anything else (`internal/cli/config_publish.go:36-38`). *Consequence:* `set`/`unset` of the
**project layer** write a file outside every repository and are refused there exactly as
`models add` is (D789 there); the `--repo` form writes a repository's own file and is allowed.
D810's answer keeps that split, and the spec states it.

**F16: Exists. Each repository's file can be read at its ref with no network.** The remembered
checkouts (`internal/localcfg/checkouts.go`, `CheckoutsVersion` at `:57`, `LoadCheckouts` at
`:268`, `ProjectCheckouts` at `:388`) record where this machine has each repository, and
`agentAtRef` (`internal/cli/run.go:1661`) already reads a `fugaro.yaml` at a ref from a local
checkout with no fetch; `checkoutConfig` (`internal/cli/cloud.go:586`) reads the current one, and
`cachedProjectLayer` (`:720`) resolves the layer for a checkout. *Consequence:* `show`'s per-
repository rows are a local read, and a repository with no checkout is shown as unknown (D819).

**F17: Exists. The console reads no rigor configuration.** Its sources are the CLI documents listed
in `docs/design/desktop-console.md` §2.1; rigor reaches it only through each run's `rigor` block in
`fugaro watch --json` (`console/docs/json-contract.md` §6.1). The console's project view is planned,
not built (`docs/console-dependency-graph/reconciliation.md:192`, plan P6). *Consequence:* no
console task in this plan (D816).

## 2. What the commands must not become

**F18: The gap this design exists to close is the owner's, and it is stated as a HARD RULE.**
`CLAUDE.md`: "one missed or broken update loses the user's trust … a release must not ship an
operator step that is another command … if a change needs one, `update` learns to do it in the same
release". A project default that reaches runs only after `fugaro config publish FILE` is exactly
such a step, and today it is the **only** way the project layer moves. *Consequence:* the plan's
ordering is not "commands first, publish later": R1 (the update stage) is a prerequisite of the
release that carries R3–R5, in the same sense the `models` plan's E1 is for it.

## 3. Decisions D810–D819

The recommendation comes first; the alternative and **its cost** are the second column. **No
decision here is answered yet.**

| D | Question | Recommendation | The alternative, and its cost | Answer |
| --- | --- | --- | --- | --- |
| **D810** | Where does `rigor set` write the project default, and where does the update read it from? | **The project layer's local source file**, resolved as `--layer FILE` → the path the local project config records (a new optional key) → `~/.config/fugaro/projects/<project>-layer.yaml`. The file is created only when the project has **no** published layer; otherwise an absent file is a refusal naming both remedies. `set` records a path it was given so the next `update` uses the same one. | **(b) A new top-level `rigor:` block in the local project config, merged into the layer by `update`:** two sources of truth for one key (the layer file stays hand-edited for every other key), `update` gains a merge whose result no file shows, and the layer the owner publishes stops being the layer they read. **(c) `set` writes the published object directly:** a second door to the cloud outside `update`'s confirmation, migration and version checks — the thing D780 already refused for `models` | — |
| **D811** | How does the change reach runs: through `update`, or through a publish step? | **A new `Layer` stage in `fugaro update`**, after `Policy`: when the local layer file differs from the published object it shows the diff, publishes through `publishLayer` and fans out through `fanOutLayer`, behind update's one confirmation; it skips with a sentence when no local file exists, and a change to an executable key still needs `--executable-changes`. `fugaro config publish FILE` stays, and gains the resolved path as its default argument. | **Keep `config publish FILE` as the only door:** the owner's edit stays a second command, which the HARD RULE forbids (F18) — and it is the one command the update rule is written about. Publishing *only* rigor and not the rest of the file: two writers for one object, and a state where the published layer is half the file | — |
| **D812** | What confirmation does lowering a mode need? | **The typed project name** (`confirmTypedStrict`, F11) whenever the new mode ranks below what the layer resolves today; `--yes` does not stand in for it. Equal or higher is an ordinary y/N. The review floor and the sentence that this command cannot lower it print beside the diff. | **`--yes` or a y/N for every direction:** a lowered project default is a whole project reviewing less, from a flag typed in advance, in a script, possibly by an agent — the direction that must never be one keystroke. **Refuse lowering entirely:** contradicts D21, which says a narrower layer may lower and a project that wants a floor declares one | — |
| **D813** | Does `--repo R` open a pull request or write the working tree? | **Write the working tree of the repository's checkout, after a diff and a confirmation; never commit or push.** The user's own branch and review carry it, exactly as `models use` (D781 there). | **Open a pull request from the CLI:** needs a write path to GitHub and Bitbucket on the developer's machine with the user's credentials, which the CLI does not have today, and it is one more place that pushes | — |
| **D814** | May `set` write `hotfix`? | **No, in any of the four forms** — not as a project default, not as an area rule, not in a repository's block, not as a `--json`-silent value. The refusal names `fugaro run --mode hotfix`, the operator's own door (D12). | **Allow it as a default:** `hotfix` ranks below every on-axis mode (`Rank`, `:75`), so a project defaulting to it would be a project whose runs tolerate nearly everything, reachable by anyone who can write the file — and D12's whole point is that the mode with the least review must not be the easy one. **Allow it with a typed confirmation:** still writes a default D12 says is an operator's own run, and the confirmation would be asked once for a state every later run inherits | — |
| **D815** | How stable is `rigor show --json`? | **A versioned document (`"version": 1`) with a JSON Schema in `schemas/`; members are only added within a version, and a renamed or re-meant member is a new member.** `from` is bounded by `internal/rigor`'s own set. | **Unversioned:** the console and any script that reads it break on the first change with nothing to check against (the same alternative D784 refused for `models ls --json`) | — |
| **D816** | Does the console show the resolved project default? | **Not in this work.** When the console's project view ships (P6, F17) it reads `fugaro rigor show --json` read-only, or shows nothing; the console never reads `~/.config/fugaro` or the bucket, and the shape it reads is documented in `console/docs/json-contract.md` by the task that builds the screen. | **A console task now:** there is no project view to put it in (F17), so the task would either build a screen the redesign replaces or read a document through `fugaro doctor`'s shape, which is not that document | — |
| **D817** | What does `unset` write? | **It removes the key or the rule, so the layer below decides again** — and a no-op with a sentence, exit 0, when there is nothing to remove. | **Write the built-in default (`pro`) in place of the key:** freezes Fugaro's own default into the owner's file, indistinguishable from a deliberate pin, and it silently becomes wrong the day the built-in default changes. **Error when there is nothing to remove:** makes an idempotent script fail, for a state that is already what the caller asked for | — |
| **D818** | How is an area rule addressed, and what does `--area` require? | **By its glob.** `set --area GLOB` replaces that rule's mode; a glob the layer does not carry adds a rule and then **requires `--name`** (it is what the run record and the pull request print); `--name` on an existing rule renames it. A second rule with the same glob in one layer is refused (F14). | **Address by name:** names are not unique, the loader does not enforce uniqueness, and a rename would silently retarget the command. **Derive the name from the glob:** writes a name the owner never chose into three surfaces a person reads, and two globs can derive one name | — |
| **D819** | Where does `show` read each repository's own block? | **The remembered local checkouts, at the base branch, with no fetch** (F16); a repository with no checkout is shown as `unknown (no checkout on this machine)` and is never guessed. | **Read through the git provider's API:** a network call with a credential on the developer's machine, for a listing — and it would still be stale against the branch the run will read. **Resolve only the project layer for such a repository:** prints a mode that may be neither the right value nor the right reason, with nothing saying so | — |

## 4. Noticed while reconciling, and not this work's

Reported rather than fixed, because each needs its own decision and its own reviewer.

- **F13's missing gate.** D12's base-branch opt-in (`rigor.hotfix`) is named by the follow-up
  refusal (`internal/cli/followup.go:720`) and by no code path. `--mode hotfix` on a first run is
  gated by nothing but the raise-only rule. This design refuses to *write* hotfix as a default; it
  does not close that gate, because the gate is a key, a schema change and a run-start check, and
  `internal/runner` is another work's file this release.
- **F14's silent pair.** Two area rules with one glob in one layer are accepted by the loader and
  resolved by `Strictest`. A load-time refusal belongs with the validation that already runs
  `Area.Validate` per rule (`internal/config`), not with a command.
- **The review floor has no command.** `review_floor` is hand-edited in the local project config
  and published by `fugaro project policy publish` — the same shape of gap this design closes for
  the rigor default, on the key that is a floor rather than a default (F10). It is worth one command
  of its own; it is not this one, and this one must not grow a flag that writes a floor.
- **`governance` has the same gap, one key over.** `docs/task-governance/implementation-plan.md:569`
  says the layer file published by `fugaro config publish FILE` is governance's only home. R1's
  layer stage removes the second command for *every* project-layer key at once; nothing else here
  touches governance.
