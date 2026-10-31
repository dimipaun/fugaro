# `fugaro settings`: reconciliation against the code

Read against `origin/main` at `29004ec9` and the 18 pull requests open on 2026-10-31. Each finding is
**Exists**, **Missing** or **Conflicts**, with its consequence for the spec or the plan. Every claim
names the file and the symbol it was read from; the audit's own `file:line` citations
([audit.md](audit.md), taken at `1c9be2a2c`) were re-checked against that commit and are cited here
by symbol rather than by line, so a reader checks them with `git grep` instead of trusting a number
that the next edit above them invalidates.

## 1. What exists

**F1: Exists. The one resolver for a checkout's `fugaro.yaml`.** `config.Resolve` and
`Resolution.SourceOf` (`internal/config/resolve.go`) answer "which layer set this key", and
`showValues` with `newConfigShowCmd` (`internal/cli/config_show.go`) already print
`PATH VALUE SOURCE` per key. *Consequence:* `settings --repo owner/repo-a` reuses this resolution; it
writes no second resolver and invents no precedence (spec §2.2).

**F2: Exists. The scope table, and its two pins.** `config.Scopes` and `ScopeRow`
(`internal/config/scope.go`) hold every `fugaro.yaml` key and the layers it may be set in, pinned
against `docs/project-layer.md` by `TestScopeTableMatchesDocs` (`internal/config/docs_scope_test.go`)
and against the design doc by `TestScopesMatchDesignDoc` (`internal/config/scope_test.go`).
*Consequence:* the registry of spec §3 **reads** this table for its `fugaro.yaml` rows and a test
asserts the two agree; neither the table nor its pins move.

**F3: Exists. A strict loader for each of the three documents.** `localcfg.Parse` for the local
project config, `config.ParseProjectLayer` for the project layer, and the repository's own strict
parse for `fugaro.yaml` — unknown fields are errors in all three. *Consequence:* `settings set`
checks its result with these and writes no validation of its own (spec §4.2 step 3).

**F4: Exists. The write triple, the typed confirmation, and a line-edit precedent.** `lineDiff`,
`backupFile` (`<file>.bak-<UTC timestamp>`, mode 0600) and `writeFileAtomic` (`internal/cli/init.go`);
`confirmTyped` and `confirmTypedStrict` (`internal/cli/budget.go`), the latter what
`confirmRemovalStrict` (`internal/cli/project_cmd.go`) uses; `setGCPProjectLine`
(`internal/cli/init_fugaroyaml.go`) as the targeted one-line edit of `fugaro.yaml`. *Consequence:*
every write in spec §4 and §5 reuses the write triple and both confirmations. The one-line edit is
the precedent, not the route: `fugaro.yaml` is edited through the same shared editor as the other two
documents (D847), which supersedes `setGCPProjectLine` without changing what it does today.

**F5: Exists. The coding-agent marker, and two doors that honour it.** `agentMarker` and `agentEnv`
(`internal/cli/initsecrets.go`) wrap `agentenv.Marker`; `fugaro config publish`
(`internal/cli/config_publish.go`) and `fugaro recipes publish` (`internal/cli/recipes.go`) refuse on
it before anything else. *Consequence:* SD9's behaviour is the code's already; the plan extends the
same call to the doors that lack it (F16).

**F6: Exists. Every reader the read command folds together.** `config show`, `config layer`,
`config list`, `validate`, `budget show`, `project show`, `project policy show`, `budget prices`,
`accounts ls`, `secrets ls`, `user ls`, `pair ls`, `backends ls` and `doctor` each read one layer or
one store (audit §4B(A)). *Consequence:* spec §2.7's per-layer cost table names the reader to reuse
for each layer; `settings` adds no second reader for anything.

**F7: Exists. The precedence rules the `WINS` column names.** `policy.Merge`
(`internal/policy/merge.go`) is the tighten-only merge of the ceiling with the layers below it;
`runCeiling` (`internal/cli/validate.go`) builds the ceiling a run starts under and carries
`Effective.Ignored`; `internal/rigor`'s resolver owns the mode order and the area union; the
Realtime Database caps order `caps.defaults` under `caps.repo` (`internal/budget/model.go`).
*Consequence:* spec §2.2 states the code's rules once; it decides nothing new.

**F8: Exists. The runtime controls, and their deliberate immediacy.** `fugaro budget set|kill|resume`
(`internal/cli/budget.go`) and `fugaro accounts clear` (`internal/cli/accounts.go`) write the
Realtime Database at once, outside `update`, on purpose — a kill switch delayed is useless.
*Consequence:* SD7 keeps them; `settings` reads their effect as `caps.*` and `kill.*` rows and writes
none of it.

**F9: Exists. The node's shape, version and defaults.** `projectpolicy.Policy`
(`internal/projectpolicy/policy.go`) already carries `Credentials` (the cascade's fallback, wait
threshold, backoff, stop-after-exhaustion and per-token concurrency) with its defaults
(`DefaultWaitThreshold`, `DefaultBackoffInitial`, `DefaultBackoffCap`, `DefaultConcurrencyPerToken`),
`clone_urls`, `providers`, `model_prices`, `routing`, `budget`, `profiles`, `accounts`,
`bitbucket_token`, `bitbucket_api_roots`, `review_floor` and `allow_repos`, at
`config/policy/shared` and `config/policy/repos/<slug>` (`internal/projectpolicy/paths.go`).
*Consequence:* SD5's import has a typed target and adds no node member; the two new local blocks are
decoded into the shapes the node already has.

**F10: Exists. The console reads CLI documents and nothing else.** Its sources are the documents of
`console/docs/json-contract.md` (the "other documents" chapter carries §6.1–§6.14), its spawn
contract is `console/src/main/ipc.ts` (`ALLOWED_OPERATION_FLAGS`), and it never reads
`~/.config/fugaro` or the runs bucket itself. *Consequence:* spec §10 names the section a Settings
view would need (§6.15) and no console task is in this plan.

## 2. What is missing, and what conflicts

**F11: Missing. No command lists the settings.** The nearest is `config show`, which covers one
checkout's resolved `fugaro.yaml` and nothing of the local config, the node, the Realtime Database
caps, the shared config, the environment variables or another repository (audit §1, §4B(A)).
*Consequence:* `settings` is new (spec §2), and it is the first slice's first task.

**F12: Missing. There is no registry of keys and defaults.** The defaults are scattered over four
places nothing ties together (audit §4C): the literals inside `applyDefaults`
(`internal/config/defaults.go`), the named constants (`DefaultReviewRounds`, `DefaultAgentAuth`,
`DefaultJobProfile`, `ABDefaultMaxPairsPerDay`, `rigor.Default`, `budget.DefaultGrace`, the
`projectpolicy.Default*` set, governance's `WithDefaults`), the local loader's own literals
(`max_parallel`, `build.machine_type`), and `internal/config/example.yaml`'s comments. The schemas
carry **no** JSON-Schema `default` keyword: the only `"default"` strings in `schemas/*.json` are the
property name `rigor.default` (`fugaro.schema.json`, `project-layer.schema.json`) and a
`not: {const: "default"}`. *Consequence:* SD16 builds the registry (spec §3); the DEFAULT column and
`--changed` have no other source, and a fourth copy of a default is what the audit's recommendation
exists to prevent.

**F13: Missing. `fugaro update` does not publish the policy node.** `buildProjectPolicy`
(`internal/cli/project_cmd.go`) is reached only by `newProjectPolicyPublishCmd`; the converge's
`initflow.Policy` stage (`internal/initflow/stage.go`) is the in-place **migration**
(`internal/cli/init_policy_migrate.go`), and it neither publishes nor says the local config is
unpublished. *Consequence:* SD3, and the first slice's third task (spec §6.1). **This is the same
stage the `models` design's E1 names** (its F8): one stage, built once, by whichever work lands it
first (F20).

**F14: Missing. `fugaro update` does not publish the project layer.** `publishLayer` and
`fanOutLayer` (`internal/cli/config_publish.go`) are reached only from `newConfigPublishCmd`, and
the project layer has no local source file at all — the only local copy is a cache of the published
object (`internal/localcfg/layercache.go`). *Consequence:* SD4, and the layer stage is **PR #1463's
R1** (`docs/rigor-command/implementation-plan.md`), which also creates the local file (its R0). This
design does not build a second one (F20).

**F15: Conflicts. `credentials.*` and `clone_urls` exist only on the node.** `localcfg.Config`
(`internal/localcfg/localcfg.go`) has no such block; `buildProjectPolicy` carries both over from the
published node and refuses to write a node it cannot read back, and its own refusal text says "no
CLI writes or removes in this release". The only writer is the Firebase console. This is the audit's
highest-risk gap (G3): the block is untyped and unvalidated until the next read, and
`credentials.fallback: auto` decides whether a run spends the paid credential tiers.
*Consequence:* SD5 — two new local blocks and an import on the first `update` (spec §6.4).

**F16: Conflicts. Two cloud-writing doors are not refused in a coding agent's session.**
`fugaro project policy publish` (`internal/cli/project_cmd.go`) and `fugaro project repos add`
(`internal/cli/project_enroll.go`) carry no `agentMarker` call, while `config publish`,
`recipes publish`, `user add|rm`, `managed flag` and `allow-delete` all do; only
`project repos add --github-app-key` is refused. *Consequence:* SD8 adds the refusal to both, for
parity (spec §6.1). The deliberate exceptions — `budget set|kill` and `accounts clear` — stay.

**F17: Conflicts. `config show` reports a clamped value as if it were in force.** A repository's
`budget.per_run_usd: 50` under a ceiling of 10 is printed with source `repo`; only `validate` says
the ceiling clamps it (`internal/cli/validate.go`'s clamp warning). *Consequence:* D849 — the
`VALUE` column is the effective value and `--why` carries the raw one. `config show` itself keeps
its behaviour: its `PATH VALUE SOURCE` shape is read by its own consumers, and changing what
`SOURCE` means for a clamped key is a field-meaning question that is not this work's (§5).

**F18: Missing. `project repos ls` has no `--json`.** `newProjectReposLsCmd`
(`internal/cli/project_enroll.go`) prints the allow-list and the seen repositories as text, while
every other read in this area has a versioned document. *Consequence:* reported in §5, not fixed
here: it is the allow-list's read, and `settings` shows `allow_repos` from the node, whose reader
already exists.

**F19: Missing. There is no settings JSON Schema.** `schemas/` holds eight documents
(`fugaro`, `project-layer`, `task`, `result`, `recipe`, `image-lock`, `creds-bundle`,
`pair-event`), none of them a settings read, and `schemas/schemas_test.go` loads and exercises each.
*Consequence:* D848 fixes `"version": 1` and the add-only rule; the plan's S1 writes
`schemas/settings.schema.json` and the test row for it.

**F20: Coordination, not a gap. The two `fugaro update` stages this design needs are two other
works' stages.** The policy stage is the `models` plan's E1 (`docs/models-commands/reconciliation.md`
F8, `implementation-plan.md` "Order") and belongs to the update-converge work; the layer stage is
PR #1463's R1 and belongs to `docs/rigor-command`. *Consequence:* spec §7 and the plan's "Order"
make both **dependencies, not tasks**: whichever work lands each first, the other deletes its copy,
and the release that carries `settings set` carries both. The read command is useful before either
(F11: it makes G1, G2 and G5 visible today), which is why it is the first slice.

## 3. The owner's answers to SD1–SD18

**All eighteen were answered by the owner on 2026-10-31.** SD1–SD5 carry named answers; SD6–SD18
were answered as recommended. The recommendation and the cost of the alternative are the audit's
(§6); the answers below are the owner's, and the spec is written to them.

| # | Question | Recommendation (audit §6) | The alternative, and its cost | Answer |
| --- | --- | --- | --- | --- |
| **SD1** | The name of the unified command | **`fugaro settings`** (read, plus `get`/`set`/`unset`); `config show` stays as its repository view | `config show --all`: overloads a group that already means three things, and it needs a checkout today | **Answered: the command is `fugaro settings`** — read, plus `get`, `set` and `unset` |
| **SD2** | Generic `set`, or one command per block | **Hybrid**: a generic `get`/`set`/`unset` driven by the key registry, plus dedicated families where a setting needs more than a value | Generic only: typing a provider map or a routing block on a command line is error-prone, and the cross-key checks are lost | **Answered: hybrid** — the generic path from the registry, dedicated families for the structured blocks |
| **SD3** | Does `fugaro update` publish the policy node from the local config every run? | **Yes**, behind its one confirmation, with a diff; `project policy publish` keeps `--delete` and `--seed-repos` | Keep the separate publish: violates the HARD RULE (G1) — an edit plus `update` changes nothing and warns about nothing | **Answered: yes, and `update` publishes the policy node** |
| **SD4** | Does `fugaro update` publish the project layer, with a seeded local file? | **Yes** (the `rigor` plan's D830/D831), plus seeding the local file on the first update | `config publish FILE` only: G2 stays, and the layer's every key keeps a second command | **Answered: yes, and `update` publishes the project layer too**, seeding a local layer file on the first update |
| **SD5** | `credentials.*` and `clone_urls`: where is the source of truth? | **Move them to local-config blocks that `update` publishes**; the first update imports the node's values | Commands that write the node directly: a second door to the cloud outside `update` (the model D780 refused) | **Answered: local-config blocks, published by `fugaro update`, with the first update importing the node's values** |
| **SD6** | The policy `budget.mode` and the Realtime Database `config/mode` | **Keep both, name them distinctly** (`budget.mode`, `caps.mode`), and have `update --help` say which it writes | Merge into one: changes what a field means, which is forbidden | **Answered 2026-10-31: the recommendation** |
| **SD7** | Do `budget set\|kill` stay immediate writes outside `update`? | **Yes**: runtime controls, not upgrade state | Route them through `update`: a kill switch delayed is useless | **Answered: the recommendation** |
| **SD8** | An agent refusal for `project policy publish` and `project repos add` | **Add it**, for parity with `config publish`, `recipes publish` and `user add` | Leave as is: an agent can widen the allow-list or publish a policy | **Answered: the recommendation** |
| **SD9** | Agent-session behaviour for a local or layer write | **Print the diff and the command, write nothing, `--yes` included** (the `models` D789); repository writes allowed | Allow with `--yes`: contradicts D789 | **Answered: the recommendation** |
| **SD10** | Where the review floor command lives | **`fugaro rigor floor show\|set\|unset`**, with lowering behind the typed project name | `project floor`: splits rigor across two nouns | **Answered: the recommendation** |
| **SD11** | Provider drift re-converge in `update` | Already answered (D787); listed so the dependency is visible | – | **Answered (D787, 2026-10-31): `update` re-converges each recorded repository whose job's provider copy drifted** |
| **SD12** | Environment variables as a settings layer, without a `set` | **Show only** | Write to a shell profile: not Fugaro's | **Answered: the recommendation** |
| **SD13** | Do project-layer writes stay launcher-reachable? | **Out of scope here; record it** | – | **Answered: recorded, and out of this work's scope** |
| **SD14** | The two things called "profiles" | **Distinct settings keys now** (`layer.profiles`, `job_profiles`); a YAML rename is a separate decision | Rename the YAML key now: churn across policy, node and docs | **Answered: the recommendation** |
| **SD15** | Negation flags for opt-ins (`--no-coordinator`) | **Add them as needed**, so turning an opt-in off is a command | Hand edit (today) | **Answered: the recommendation** |
| **SD16** | One machine-readable defaults and keys registry | **Build it**; generate or pin the schema `default`s, the scope table doc and `applyDefaults` from it | Keep scattered: the DEFAULT column and `--changed` would need a fourth copy | **Answered: the recommendation** |
| **SD17** | The `project_secrets` per-machine copy | **Publish it in the node and read it from there in `update --repo`** | Keep the manual copy: a documented hand step on every operator machine | **Answered: the recommendation** |
| **SD18** | Who may `settings set --layer local` | **Anyone may edit their own file; the boundary stays IAM at publish** | Enforce roles locally: not enforceable on a file the user owns | **Answered: the recommendation** |

## 4. This design's decisions D840–D854

Everything the audit left open that is **not** one of its SDs. The recommendation comes first; the
alternative and its cost are the second column. **All are recommended; none is answered yet, and
D854 is the one the owner is being asked about now.**

| D | Question | Recommendation | The alternative, and its cost |
| --- | --- | --- | --- |
| **D840** | Where does the key and defaults registry live? | **A new `internal/settings` package**, whose `fugaro.yaml` rows are read from `config.Scopes` and pinned against it by a test. `internal/config` learns nothing of `localcfg` or `projectpolicy` | **Extend `config.Scopes` with default, merge and store columns:** puts local-config and node knowledge into the package the runner and the task format import, and every consumer of a `fugaro.yaml` key would carry it |
| **D841** | Are the schema `default` keywords and the docs table generated from the registry, or pinned to it? | **Pinned by a test**, the shape `TestScopeTableMatchesDocs` already gives the scope table | **Generated** (`go generate`): a step a release must remember to run, and a generated file whose diff is noise nobody reads |
| **D842** | Where is the local source of truth for `credentials.*` and `clone_urls`? | **Two new blocks in the local project config**, decoded strictly like every other block, beside `budget`, `providers` and `routing` | **A separate `<project>-policy.yaml`:** a second file for the owner to know about, while the local config is already the home of every policy-section key (audit §1.3) |
| **D843** | What does `update` do when neither the local file nor the node carries the two blocks? | **Nothing: no block is created and no default is seeded** | **Seed the defaults into the local file:** writes values nobody chose, and makes a file look authoritative where the node's own defaults already decide |
| **D844** | What does the import confirm, and what does a refusal do? | **It prints the values, confirms under update's own confirmation, and writes with the diff/backup/atomic triple; a refusal leaves the file and the node untouched and says so** | **Write the import without asking:** a file the owner never wrote appears in their config home, carrying the block that decides whether paid tiers are spent |
| **D845** | The `--layer` vocabulary, and how `--layer repo` finds its file | **`local`, `project`, `repo`; `default` is never a target; `--layer repo` without `--repo` uses the current directory's checkout and refuses when the directory is not one** | **Name the layers after their files** (`localcfg`, `layer`, `fugaro.yaml`): names that change when a file moves, and that say nothing about precedence |
| **D846** | Which writes ask for the typed project name? | **Loosening a ceiling key (`budget.*`, `governance.*`), removing a `review_floor` entry, turning a `followup.*` key on, and writing any key the registry marks as a floor; `--yes` never covers the typed form, and in a coding agent's session a typed write is refused even at `--layer repo`** (spec §4.2) | **The typed name for every write:** ceremony on the common, harmless direction, which teaches people to type their project name without reading it |
| **D847** | One comment-preserving editor or one per document? | **One shared package, `internal/yamledit`,** holding generic locate-and-splice operations — set a scalar, remove a key, add or remove a list item, upsert or remove a sequence item by key — used for the local config, the local layer file **and** `fugaro.yaml`; **one owning task (the editor task) lands it first** in the joint order, and a cross-document test asserts all three documents preserve comments, key order and untouched bytes the same way | **One editor per document** (the `models` plan's M0, the `rigor` plan's R2, and the line-edit helper beside `setGCPProjectLine`): three implementations of a property each plan tests on its own, and three that will drift apart |
| **D848** | The `--json` version, and where the console contract section goes | **`schemas/settings.schema.json`, `"version": 1`, members added only within a version; the console section is `console/docs/json-contract.md` §6.15, written by the task that builds the screen** | **Fix the contract here:** a design that writes the console's own document drifts from the screen it describes, and the console boundary keeps `console/docs/` a file the console's work owns |
| **D849** | Is `VALUE` the effective value or the winning layer's raw one? | **The effective value a run gets; `--why` carries the raw value and the clamp** | **The winning layer's raw value:** the state `config show` is in today (F17), where a clamped 50 reads as if it were in force |
| **D850** | What may a read touch? | **Reads run anywhere, an agent session included; no Secret Manager access call, no Terraform state; a cloud layer that cannot be read is `unread: <reason>`, never a default** | **Refuse reads in an agent session:** a read-only command answering a question an agent may legitimately ask, and the refusal pushes it to a hand read of the same files with no diff and no redaction |
| **D851** | Is the `STATE` vocabulary closed? | **Closed** (spec §2.5); a new state is a new member of the set and an addition to `--json` | **Free text:** a consumer cannot branch on the column, which is the only reason the column exists |
| **D852** | `settings get` on a key set nowhere, and on an unknown key | **Set nowhere prints the default row and exits 0; unknown exits non-zero with the nearest registry names** | **Non-zero for set nowhere:** an idempotent script fails on a state that is already the answer — the argument `rigor unset` makes for its no-op |
| **D853** | Which keys does the generic path own? | **Every scalar and string-list key; a map key, and any key whose `Owner` is a family, is refused with that family's command** | **Let the generic path write maps:** typing a provider map or an area rule on a command line loses the cross-key checks (route overlap, `Area.Validate`, the cascade's silence rules) the families exist for |
| **D854** | A stale local copy overwriting a newer publish: what does `update` do when the object has moved since this machine's file was written? | **Record the generation the local file was seeded from or last published at. When the published generation has moved since, `update` refuses to publish, shows the three-way state (this machine's file, the generation it was written from, the object published now), and offers to re-seed the local file from the published one under confirmation, with a backup** | **Publish on difference anyway** (what §6 does until the owner answers): on a second machine, one operator's newer layer or policy node is silently replaced by this machine's older file. Nothing reports it — every later `update` on the losing machine rewrites the same stale content — so the change is noticed only when a run behaves in a way nobody chose, and the newer publish is unrecoverable unless someone kept a copy |

**D854 is open, and the owner is being asked about it.** **D855–D859 are reserved and unused.**

## 5. Noticed while reconciling, and not this work's

- **Two works claimed one decision block, and the second has moved off it.** `docs/console-coordinator-v2`
  on `main` holds **D810–D829**, and PR #1463 (`docs/rigor-command`) had claimed **D810–D829** for
  itself (`docs/rigor-command/README.md`'s number table, and D810–D819 written in its reconciliation).
  Two documents cannot own one block. PR #1463 has since renumbered its own block to **D830–D839**,
  so the collision is resolved: this design takes the next free number above every block written on
  `main` and in an open pull request (**D840**), and the two places it cites the rigor design — the
  local layer file that `rigor set` writes (**D830**) and the `Layer` stage in `fugaro update` that
  publishes it with seeding (**D831**) — cite the renumbered decisions. The block this design held
  before the renumbering (D830–D849) is released, so the two blocks cannot be confused; nothing
  outside this folder ever cited it.
- **`internal/cli/run.go`'s cascade refusal names the wrong file.** Its message says to "add the
  allow-list to the project layer and publish it (`fugaro project policy publish`)", while
  `budget.allowed_models` is a key of the **local project config** and the project layer has no such
  key. The sentence names the wrong file to a person who is already blocked.
- **`fugaro project repos ls` has no `--json`** (F18), while every other read in this area has one.
- **`applyDefaults` hard-codes `"main"`** (`internal/config/defaults.go`) beside
  `config.DefaultBaseBranch` (`internal/config/basebranch.go`): two copies of one default, which the
  registry of spec §3 collapses.
- **`credentials_service.url` and the node's `credentials.*` are two different things with one
  word.** The first is the installation's own credentials service, in the local config; the second is
  the cascade's spending policy, on the node. The registry must keep them apart by key
  (`credentials_service.url` versus `credentials.fallback`), or the read will be read as the write.
- **The clamp is invisible in `config show`** (F17). Closed for `settings` by D849; `config show`
  keeps its own behaviour, because changing what its `SOURCE` column means for a clamped key is a
  decision for its consumers, not a fix this design may slip in.
- **`fugaro config layer` requires a git checkout**, so an owner outside a repository cannot read
  the layer through that command (audit §7). This design's read does not inherit that: the layer has
  a file of its own, `~/.config/fugaro/projects/<project>-layer.yaml` (`localcfg.LayerFile`), and a
  published object, so `fugaro settings --layer project` answers outside a checkout. Whether the
  `rigor` design's R0 lifts the requirement for `config layer` itself is its decision; nothing here
  claims it for that command.
