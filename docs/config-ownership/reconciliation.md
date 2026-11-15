# Config ownership: reconciliation (DRAFT, phase 1)

`spec.md` as amended by the code and by the owner's answers. Where this file disagrees with `spec.md`,
this file wins. Checked against `origin/main` at `eddaa041b` on 2026-11-15 (the audit in `audit.md`
was taken at `3e1b02570`; every claim cited below was re-read at `eddaa041b`).

**Status: draft. No plan is written. Every decision in section 4 has an empty answer cell.**
Decisions take **D2600-D2639** (highest in use on `main` and in open pull requests' documents: D2551;
D2600-D2639 are reserved, used or not). Findings are numbered `F1`... within this document, as every
earlier reconciliation does. Checks: none reserved yet.

**Folder.** `docs/config-management/` already exists on `main`: it is the 2026-11-03 package ("no
configuration file edited by hand", G0-G13, D1160-D1183). This spec is its successor in subject but not
a revision of it, so it takes `docs/config-ownership/`. Section 2 says what of the older package it
must respect.

## 1. The owner's nine direction items: answered 2026-11-15

Recorded as answered by the owner on 2026-11-15 (his words are in `spec.md`); not re-asked.
Feasibility against the code is in section 3 (F-numbers), contradictions in section 5.

| # | Direction | Answer |
| --- | --- | --- |
| 1 | Semantic command for a field with invariants, secrets, publish, list entries or side effect; concepts: pools and crafts; credentials and the cascade (`credentials.*` incl. `fallback`, `binaries.*`, node `model_grades`); rigor and security paths; models and routes | Answered 2026-11-15 |
| 2 | Generic `settings` for scalar leaves; `settings list`/`get` show every field wherever it lives | Answered 2026-11-15 |
| 3 | One writer per field; semantic commands are front doors onto the one write path | Answered 2026-11-15 |
| 4 | Every registry row names its owner and a test pins that the owner writes the key | Answered 2026-11-15 |
| 5 | `settings set` on a semantic-owned field refuses and names the owner | Answered 2026-11-15 |
| 6 | A refusal names the working spelling | Answered 2026-11-15 |
| 7 | Uniform `--layer repo\|project\|local` on every writer; merge rule per field; ceilings default to the project layer | Answered 2026-11-15 |
| 8 | No field is manual-only without a stated reason; plumbing and fake endpoints get an explicit label | Answered 2026-11-15 |
| 9 | Local to node: each semantic command writes the node itself, and `project policy publish` and `fugaro update` also reconcile it | Answered 2026-11-15 |

Also ruled 2026-11-15 (spec): `governance.stage_timeout` is for real use; the project layer is the 99%
path and must reach runs; a repository's value may only tighten. The first plan task is the
registry-versus-writer conformance test.

## 2. Assumptions

- **A1.** "The three layers" are the repo `fugaro.yaml`, the project layer (the layer file and the
  published policy node, with the budget database as a third store of the project) and the local config.
- **A2.** "One write path" means the code path `settingsResolveTarget` / `settingsLayerGate` and the
  document writers under `settings_set.go`, which `rigor set`, `rigor floor` and `accounts pool` already
  call in part (F4); it does not mean one binary entry point.
- **A3.** `--layer` uniformity applies where a key has more than one real layer; a key with one real layer
  (a pool lives only in the local config and the node) refuses a wrong layer with a sentence naming the
  right one rather than growing a second layer (D2605).
- **A4.** "Reconcile the node" in item 9 is additive on first run: it never deletes a node member that no
  local key states (D2607).
- **A5.** The nine items bind; where the code contradicts one, the contradiction is listed in section 5
  and is a question to the owner, not a silent change of the item.
- **A6.** This work is built with `fugaro update` as the whole upgrade (CLAUDE.md hard rule): any change
  to what `update` publishes ships its previous-version convergence test.

## 3. Findings

### 3.1 What the repository already has, by the spec's concepts

| Spec concept | What exists on `eddaa041b` |
| --- | --- |
| The registry | `internal/settings/registry.go`: `Row{Key, Layers, Merge, Default, Sensitive, Owner, Doc, TypedWhy}`; `fugaro.yaml` rows are derived from `config.Scopes` (`yamlRow`, `scopeLayers`), layer rows by `layerRow` (key prefixed `layer.`), local rows by `loc`, `locTighten`, `locLocal` |
| Owner column | `Row.Owner` is a token; `settingsFamilyCommands` in `internal/cli/settings_set.go` maps a token to a command family (`fugaro models`, `fugaro rigor set`, `fugaro rigor floor`, `fugaro accounts`, `fugaro secrets`, `fugaro project`, `fugaro workspace`, `fugaro image map`, `fugaro project clone-url`) |
| The pin on owners | `TestRegistryOwnerNamesACommand` (`internal/settings/registry_test.go`) pins the owner to a **vocabulary** of family names; it does not run the command (F2) |
| Refusal | `settingsRefuseRow`: sensitive, family, `*` key, then `settingsGenericKey` allowlist; the family sentence is one generic string |
| Shared write path | `settingsResolveTarget`, `settingsLayerGate` called from `settings_set.go`, `rigor.go`, `rigor_floor.go` |
| Entry writes | `--add-entry` / `--remove-entry` on `settings set` (config-management G1, merged) |
| Layer flags | `--layer` over `settingsWriteLayers` (local, project, repo) and `--repo`; `settingsAimFlags` adds `--project`/`--config` (the Fugaro project, not a layer) |
| Publish | `buildProjectPolicy` in `internal/cli/project_cmd.go`; `fugaro update` stages `policy` (`init_policy_publish.go`) and `layer` (`init_layer_stage.go`) |
| Pending steps / chains | composite-commands AP1 (`internal/apply`) and AP2 (stage subset, #2757) merged; `fugaro apply` (AP3) not built (section 6) |

### 3.2 Findings

Class: Wrong, Exists, Gap, Rule, Open.

- **F1 (Gap, confirmed).** `governance.stage_timeout.<stage>` cannot be written at the project layer by
  its natural name. The registry holds `governance.stage_timeout` as a block row (`Merge: Tighten`, layers
  repo) and concrete rows only as `layer.governance.stage_timeout.implement` (and `review`...) via
  `layerRow`; a concrete unprefixed key finds neither (`settingsWriteRowHinted` answers "no setting has
  that name"). `stage_budget`, `stage_tokens`, `tiers` are blocks with no entry row. Consequence: the
  owner's 99% case fails today; the four repo-level block rows are listed writable but cannot be
  (D2604).
- **F2 (Gap, confirmed).** Item 4's pin does not exist. `TestRegistryOwnerNamesACommand` checks that
  `Owner` is in a list of 13 family names; nothing checks that the family writes the key. The audit counts
  114 rows whose owner does not write the key; I confirmed the mechanism (owner is a token, the family map
  names a command family, not a flag) and spot-checked `agent.levels.*.model`, `pools.*.owner`,
  `layer.jobs.reports.paused` (owner `update`, no writer) and `layer.ab.rate` (owner `models`); I did not
  re-run the dump (U1). Consequence: the first task builds a conformance test over the table (D2610).
- **F3 (Wrong in the code, confirmed).** `settings_set.go` hands `credentials.` and `clone_urls.` to the
  generic path (the allowlist near line 272 and its test `TestSettingsGenericKeyIsSpec44sAllowlist` listing
  `credentials.fallback`), but the registry has no `credentials.fallback` row, so the write answers "no
  setting has that name"; and the owner has now ruled `credentials.*` semantic. Consequence: the allowlist
  entry, its test and the §4.4 wording flip; the registry gains rows whose owner is the new command.
- **F4 (Exists).** The "one write path" is half built: `rigor set`, `rigor floor` call
  `settingsResolveTarget`/`settingsLayerGate`. `accounts pool add|rm` (`accounts_pool.go`) calls
  `settingsResolveTarget` and `settingsLayerGate` with a fixed project layer and then parses the
  local config itself; `models use` writes a repository working tree with its own code. Consequence:
  item 3 is a refactor of those two, not a new layer; cost is bounded.
- **F5 (Wrong in the doc, confirmed).** `accounts pool` text says pools are created "here or in the
  published node" and that `fugaro update` leaves node pools alone ("MC4's job"). `buildProjectPolicy`
  still does `pol.CraftCredentials = old.CraftCredentials`, `pol.CraftPools = old.CraftPools`,
  `pol.Crafts = old.Crafts` (also `old.Credentials`, `old.Binaries`, `old.ModelGrades`), so a pool added
  by `accounts pool add` never reaches the credentials service or `internal/runner/poolchoice.go`, which
  read the node. Gap 2 stands. The comment names "MC4" as the publish; **model-crafts MC4 as merged
  (#2252) is the migration inside `update`**, so the label in the code is stale and no plan task of that
  name is left to wait for.
- **F6 (Exists, partly).** `binaries` is no longer manual: binary-allow-rule AR1 (open, #2782, not merged)
  adds `localcfg.BinariesAllow/BinariesMaxBytes`, fills the member in `buildProjectPolicy` and bumps the
  policy node to version 8 (silence keeps the node's list; a written empty block clears it). So the
  audit's gap 3 for `binaries.*` is being closed by another plan; the writer (settings rows over those
  local keys) is not stated in AR1's file list as I read it (U3). `credentials.*` (fallback,
  wait_threshold, backoff, stop_after_exhaustion_per_window, concurrency_per_token) and node `model_grades`
  have **no local carrier** and no writer (D2607).
- **F7 (Rule).** A pool is a spending and data-egress grant (`accounts_pool.go` header: "only the owner
  creates one", F12/D1628). Publishing local pools to the node at `update` (item 9) makes `update` a
  grant-widening writer. The existing refusals (agent session refusal, managed gate, loosening judgement)
  stay with each stage; a reconcile must not publish from an agent session (D2607).
- **F8 (Gap, confirmed).** `pools.*.owner|allowed_users|legacy_open` have no writer; `docs/project-layer.md`
  says publish refuses an ownerless pool. After F5 is closed a pool added with no owner can never publish:
  the writer must take the owner (D2608).
- **F9 (Rule).** `settings set --layer project` for the 17 `agent.*`/`git.*` rows lands in the wrong place:
  `settingsResolveTarget` for the project layer takes `strings.Split(strings.TrimPrefix(row.Key, "layer."), ".")`
  as the key path, and these rows have no `layer.` prefix, so the key lands at the layer file's top level
  and fails at re-read. `docs/project-layer.md` section 5 documents the broken spelling (D2606).
- **F10 (Gap).** `fugaro rigor set` writes only the layer file and has no `--repo`/`--layer` flag (flags:
  `--area`, `--name`, `--ask`, the aim flags); `settings` refuses repo `rigor.*` naming it (gap 5).
- **F11 (Exists).** The registry owner column was already made the read's own table by G12
  (config-management D1178/D1179, merged), including a `config` and `config init` token for keys no
  command edits. Item 8's stated-reason label extends that vocabulary, it does not start one.
- **F12 (Exists / doc).** `layer.governance.*` is enforced only by a build with the `ee` tag
  (`internal/policy/hook.go`: untagged builds validate and hold no run to governance). The audit left
  open whether the released runner image carries it: `images/base/Dockerfile` builds `go build -tags ee`
  (the credentials image does not, ee-reorg D524). Consequence: governance reaches runs in the base image;
  `settings` Doc for governance rows should say "enforced by the ee build".
- **F13 (Gap, confirmed).** Unregistered fields: repo `ab.rate` (the layer row `layer.ab.rate` exists, owner
  `models`), `workflows.*.needs`, `layer.security.*`, `layer.jobs.*.paused` (rows exist, owner `update`,
  no writer), repo `planning.decoupling` (the layer row `layer.defaults.planning.decoupling` is writable).
- **F14 (Rule, composite-commands).** See section 6 for the whole cross-reference: the chain, `--no-apply`,
  the fence on cap raises, and the file overlaps.
- **F15 (Rule, old package).** config-management D1168 ("a dedicated command exists only where its rules
  differ from the generic writer's") is the rule that classifies a field; the owner's nine items give the
  list. D1161/D1162/D1163 (concrete key addresses a wildcard row, `--add-entry`, object-list entries
  addressed by key field) are the mechanism the semantic commands should call.
- **F16 (Gap).** `plugin/skills/working/SKILL.md` omits the `layer.` spelling and the failing
  `--layer project`; the plugin tests check every command and flag a skill names against the CLI, so any
  new command or flag spelling edits the skill in the same pull request. `docs/settings-commands/audit.md`
  is stale (the audit says so; not re-read in full, U4).
- **F17 (Gap).** Ceiling keys (`budget.*`, `governance.*`, `agent.max_*`) default to the **repo** file,
  which can only tighten; the owner-level value needs `--layer local` or `--layer project`. Item 7 changes
  the default; every documented example and the console's `settings.edit` row relies on the old default
  (D2605).
- **F18 (Open, not determined by the audit).** `settings set layer.defaults.agent --add-entry model=X`
  (does it write model keys), per-leaf writers for `batch.*`, `build.*`, `compute_prices.*`, and whether
  `setup` ever writes `endpoints.*` are still unknown; U2.

### 3.3 Completeness check

- **Success criteria:** the spec's measurable ones are the conformance test (every row's owner really
  writes its key) and the ten gaps closed. "Uniform" and "no stale copy survives" need an observable: a
  converge test that a local value and the node agree after each semantic command, `publish` and `update`
  (D2610, D2607).
- **Commands:** from CLAUDE.md: `go test ./internal/settings/ ./internal/cli/ -run '...' -count=1`, docs
  tests `-run Docs`, plugin tests in `plugin/`. **Where things live:** `internal/settings`,
  `internal/cli/settings_*.go`, `accounts_pool.go`, `models_use.go`, `rigor*.go`, `project_cmd.go`,
  `internal/projectpolicy`, `internal/localcfg`. **Style:** `rigor_floor.go` (a semantic command over the
  shared path). **Boundaries:** never edit `.github/workflows/`; no new prompt (`--ask` only, no-prompt
  policy); no secret-shaped literals; new wire fields additive (a field never changes meaning).

## 4. Decisions (all open; answer cells empty)

Each carries my recommendation, the alternative and its cost.

| # | Question | Recommendation | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D2601** | Is this one capability or a map? | **A map of seven modules** (section 7): conformance first, one write path, node publish, then three writer families, then labels and docs. The writer families touch different files and can run in parallel | One capability, two slices: fewer interfaces to settle, but every task reasons over 478 rows and the files collide on `settings_set.go` | |
| **D2602** | Item 9 against composite-commands: do the semantic commands write the node directly, or through the chain (`policy` stage, pending file, `fugaro apply`)? | **Through the same stage function `update` runs, called in-process as `accounts add` does (`accountsAddStages`), on failure keep the local change and say what is not yet live (composite D1563).** Adopt `--no-apply` and pending when `apply` (AP3) lands | Wait for AP3: composite is parked on no-prompt policy, so this work would stall behind it. Or write the node by a bespoke call: a second path, which item 3 forbids | |
| **D2603** | Where is the scalar/semantic line for the borderline fields (the audit lists about 60 manual-only keys outside your four concepts: `orchestrator.*`, `issues.nudge_*`, `triage`, `verifier_accounts`, `mobile_allow`, `secret_rotation_days`, `bitbucket_api_roots`, `bitbucket_tokens.*`, `ab.*`, `layer.jobs.*.paused`) | **Scalars and flags go to `settings` (new rows, no new command); `bitbucket_tokens.*` and `secret_*` stay with `secrets`, which gains the missing writes; `mobile_allow` and `orchestrator.decision_model_per_class` go to `settings --add-entry`** | A command per concept (`orchestrator set`, `mobile allow`): more surface, more skill text and docs tests | |
| **D2604** | How does `governance.stage_timeout.implement` resolve (F1)? | **Concrete stage rows exist at both layers; the unprefixed key defaults to `--layer project` (the owner-level path), a repo write needs `--layer repo` and may only tighten; the four broken block rows are replaced by per-stage rows and `tiers.*` entry rows.** In an agent session a project-layer raise is fenced (composite D1576) | Keep the `layer.` spelling only and make the "no setting has that name" error name it (item 6): one line of code, but the 99% path stays the long spelling | |
| **D2605** | Default write target (item 7, F17) | **By merge rule: a Tighten (ceiling) key writes the project layer when one exists (else local); a Replace key keeps the repo default; `--layer` always overrides; a layer the key does not have refuses naming the layers it has.** | Require `--layer` whenever a key has more than one layer: explicit, but breaks every skill and the console row that omit it. Cost of recommendation: a behaviour change for scripts that relied on the repo default of a ceiling key | |
| **D2606** | The `--layer project` spelling for the 17 `agent.*`/`git.*` rows (F9) | **Accept `agent.x --layer project` and map it to `layer.defaults.agent.x` in the one resolver; keep the long spelling working; fix `docs/project-layer.md`** | Remove `project` from those rows' Layers and fix the doc: smaller, but the project default for `agent.*` is then only writable by the long spelling, against item 7 | |
| **D2607** | The source of truth for node-only blocks (`credentials.*` cascade, node `model_grades`, and crafts/pools/credentials lists) so that "no stale copy survives" (item 9) | **Add a local carrier for each (as AR1 does for `binaries`), so the local config states what the node should hold; publish and `update` copy local to node: silence keeps the node's value, a written value wins, an explicit unset removes; the first reconcile is additive and never deletes a node member the local config does not state; nothing publishes from an agent session** | Writer edits the node only, local has no copy: no new keys or schema, but `update` cannot reconcile what it does not hold, so item 9's second half is empty for these blocks. A mirror (local authoritative) risks wiping hand-made node entries on the first `update` | |
| **D2608** | Pool owner, `allowed_users`, `legacy_open` writers (F8) | **`accounts pool add` takes `--owner`, `--allow-user` (repeatable) and `--legacy-open`; an ownerless pool saves locally but refuses to publish with a sentence naming `--owner`** | Default the owner to the signed-in owner: fewer flags, but it grants a spend right by default | |
| **D2609** | How a refusal names the working spelling (item 6) and where the owner lives (item 4) | **Replace the `Owner` token by a typed writer on the row: the command and the argv template (`fugaro accounts pool add NAME ...`), so the refusal prints the line and the conformance test runs it.** Family map stays for the human sentence | Keep tokens and add a golden table `key -> argv` pinned by a test: cheaper, drifts from the registry | |
| **D2610** | Shape of the first-task conformance test (D2609 decides the source) | **Executing: for every row, a hermetic fake runs its writer's argv and asserts the key changes in the right store; rows whose store is the cloud (budget database, node-only) are asserted through the fakes in `internal/gcpfake`; a row that names no writer must carry a stated reason label (item 8)** | Structural only (command and flag exist): fast, but passes the 114 misattributions that exist today for the wrong reason | |
| **D2611** | Labels for the no-writer keys (item 8) | **Three labels in the owner vocabulary G12 started: `plumbing` (setup/update write it), `fake-endpoint` (test-only; setup never writes `endpoints.*`, to be verified in F18), `file` (a key in a named file nobody edits; each needs a one-line reason or becomes a settings row)** | One label `manual`: fails item 8's "stated reason" | |
| **D2612** | Sequencing against open pull requests that touch the same files | **Start with the conformance test and the pure registry/spelling tasks (no overlap); hold node-publish and the `binaries.*` rows until #2782 (AR1) merges; hold writer-family tasks that touch `settings_set.go` or `accounts_rm.go` until composite AP5/AP6 are decided (section 6)** | Build all now and rebase: faster, and each of #2782, #2753, #2732 touches `project_cmd.go` or `localcfg.go` | |

## 5. Open tensions with the nine direction items

None of these re-asks an answered item; each is a place two answers, or an answer and the code, pull
apart. All are listed for the owner.

- **T1 (item 9 against composite-commands D1561-D1563, D1566).** Item 9 says each semantic command
  writes the node itself. Composite-commands says a management command saves, runs its chain of named
  steps, and on a failed step keeps the change and marks the step pending. If the semantic command writes
  the node in-process it must follow D1563 (saved, not live yet, name the command) or it contradicts a
  binding answer. D2602 recommends that reading; the owner confirms it.
- **T2 (item 5 against the code's own rule for `credentials.*`).** The code treats `credentials.*` as a
  generic-path key (F3); item 1 and item 5 make it semantic. Not a contradiction of the item, a reversal
  of existing code and its test; listed because an installation's documented `settings set credentials...`
  habit will start refusing.
- **T3 (item 7 "uniform `--layer`" against single-layer keys).** Pools and node blocks have no repo layer;
  `rigor set` has none today. Uniform means the flag everywhere with refusal naming the real layers (A3);
  if the owner meant every writer accepts all three, the repo layer for pools would need a meaning that
  the project layer's "repo may only select among crafts" rule (`merge.go` tightenCraft) does not give it.
- **T4 (item 7's default against composite D1564).** Composite says a repo-layer write edits the file and
  is live only when merged, commits and opens nothing. D2605 sends ceilings to the project layer by
  default, which is a cloud write for a key that used to be a file edit: the first `settings set` of a
  ceiling with no `--layer` now publishes (through the chain). No contradiction of a stated item, but a
  behaviour change the owner should see.
- **T5 (item 4 against the 128 manual-only rows).** Item 4 pins that an owner really writes the key; item
  8 allows labelled manual-only rows. A row labelled `file` has an owner that cannot be run; D2610 makes
  the label the pinned fact for those rows. Confirm.

## 6. Composite-commands: what it decides, what overlaps, what this work must respect

Source: `docs/composite-commands/` on `origin/main` (README, spec, reconciliation, plan). **Status:** row
in `docs/status.md`: "composite-commands | 1/10 | depends on no-prompt-policy" (parked); README: "parked,
1/10 merged". On `main`: AP1 (#1924, the pending store and change-to-stage table, `internal/apply`) and AP2
(#2757, `stagesNamed` generalising `accountsAddStages`) are both merged (the status row lags by one).
AP3 to AP10 are not built. Decisions D1560-D1579 (D1578-D1579 reserved), checks 390-399 reserved. All
D1560-D1577 are answered by the owner on 2026-11-07 (recommended, or confirmed as written) and are
**binding here**.

**What it already decides that overlaps this spec**

| Topic | Composite decision | Effect on this spec |
| --- | --- | --- |
| Management commands run their chain | D1561: named steps built over `initflow` stage names; D1566: in-set commands are `settings set/unset`, `image add/rm`, `accounts add/rm`, `project repos add/rm/sync`, `models add/rm`, `workspace` writes, `project policy publish`; out: `budget set`, `secrets/creds set`, `recipes publish`, `allow-delete`. **`accounts pool` and `models use` are in neither list** | Item 9's "writes the node itself" is this chain. The semantic commands in this spec (`accounts pool`, `models use`, a credentials/cascade writer, crafts) must be added to the registry/step table (AP1's `steps.go`), and D1566's list is amended to name them (D2602) |
| Failure and pending | D1563: keep the change, mark pending, exit non-zero, name `fugaro apply`; D1562 local pending file; D1570/D1575 file shape and trust | T1. New writers inherit it |
| The skip flag | D1565: `--no-apply`, one helper `addNoApplyFlag` | Every new or changed writer here carries it; no second spelling |
| One write path | AP2 `stagesNamed`; D1567 `update` runs every stage, `apply` only the pending | Reconcile-on-update (item 9) is the `policy` stage `update` already runs; the new node members are written in `buildProjectPolicy`, which both doors share |
| Repo-layer write | D1564: writes the file, says it is live after merge, commits and opens nothing | Any repo-layer writer here (`rigor`, crafts selection) behaves so; T4 |
| Agent sessions | D1571, D1573, D1576: steps run under the no-prompt plan's rules; every cap raise the chain publishes is fenced in an agent session, `budget.*`, `workspace cap set --monthly`, `governance.*`, `agent.max_*`, including with `--no-apply` | `governance.stage_timeout`/`stage_budget`/`stage_tokens` raised at the project layer (D2604) is a cap raise: must be fenced. Pool or credential grants published by the reconcile are not in D1576's list: the fence is extended by this plan (D2607), flagged as an amendment to ask about |
| Refusal wording | F11/AP7: an audit of "now run X" texts, an allow-list golden; `plugin/skills` edits | This spec's refusal wording (item 6, "names the working spelling") is the same kind of text. Its golden allow-list (`testdata/next_step_allow.golden`, AP9) and lint apply to every new sentence; the refusal this spec adds names a working `settings`/semantic command, never `update`/`apply` as a manual step (F11 class (b) stays) |
| Console | D1569/D1572: `settings.edit` row gains `--no-apply`; one new `fugaro apply --status --json` read | A changed default layer (D2605) changes the `settings.edit` argv; same files (AP8) |

**Where they contradict or leave a hole**

1. Composite names `accounts pool`/`models use`/`rigor set`/`rigor floor` nowhere; D1566's set must
   be amended, not re-decided (T1).
2. Composite's out-list says `secrets/creds set` write the cloud with no follow-up; this spec's `secrets`
   gains missing writes (D2603) and so stays out of the chain, as D1566 says.
3. Composite D1577 (update never builds a catalog image) is unaffected.

**Same files and commands (the plan must order them, no level may hold two writers)**

| File | Composite task | This work |
| --- | --- | --- |
| `internal/cli/settings_set.go`, `internal/settings/steps.go` | AP5 | write path, default layer, spelling, refusal text, allowlist flip (F3) |
| `internal/cli/accounts_rm.go`, `project_enroll.go`, `project_cmd.go`, `models_add.go`, `models_rm.go`, `workspace.go` | AP6 | `project_cmd.go` (node publish), `accounts_rm.go` (pool removal), `models_*` (route writers) |
| `internal/cli/project.go`, `docs_desktop_console_test.go`, `console/src/main/spawn.ts`, `ipc.ts`, `settingsedit.ts`, `confirm.ts` | AP8 | only if the settings default layer or a new writer reaches the console |
| the F11 file set, `plugin/skills/*`, `CLAUDE.md` | AP7 | the refusal wording and the skill edits (F16) |
| `internal/apply/steps.go` (AP1, merged) | table rows per change | a row per new writer |

Open pull requests that touch the shared files now: #2782 (binary-allow-rule AR1:
`project_cmd.go`, `localcfg.go`, `policy.go`, `registry.go`), #2753 (shared-jobs-default S5:
`project_cmd.go`), #2732 (`localcfg.go`), #2498 (escalation E6: `policy.go`, `registry.go`), #2576 and
#2538 (rigor). Older package (config-management 2026-11-03) is also binding: D1161-D1163, D1168, D1178/D1179
(merged G0-G2, G6, G7, G9, G10, G12, G13 as far as I could see from the log; its README header says 8/14 as of
2026-11-10, current count unchecked, U5), and settings-commands S11 (the end-to-end converge test) is not built:
it is the nearest existing test to D2610 and should be reused, not duplicated.

## 7. Proposed capability map (to approve under D2601)

Arrows point one way. Module ids are final once approved.

| Module | Responsibility | Depends on |
| --- | --- | --- |
| `cm-conformance` | The executing conformance test, the typed writer on each registry row, the three labels (items 4, 8; D2609-D2611); first task | none |
| `cm-write-path` | One resolver for layers: default by merge rule, `agent.*` project spelling, `stage_*` rows, refusal wording and the credentials allowlist flip (items 3, 5-7; F1, F3, F9, F17) | `cm-conformance` |
| `cm-node-publish` | The local carriers and the reconcile for crafts, craft_credentials, craft_pools, credentials cascade, model_grades, and the update convergence test (item 9; F5-F7) | `cm-conformance`; held for #2782 |
| `cm-accounts` | Pool owner/allowed_users/legacy_open, crafts and grades access, `claude_accounts` add (F8) | `cm-write-path`, `cm-node-publish` |
| `cm-cascade` | A writer for `credentials.*`, `binaries.*` (over AR1's carrier), node `model_grades` | `cm-node-publish` |
| `cm-models` | Providers, prices, levels, `agent.model/recipe`, `agent.crafts.*`, `layer.claude_models`, `ab.*`, rigor repo path (F10, F13) | `cm-write-path` |
| `cm-docs` | The five doc contradictions, the skill, release notes, unregistered fields' rows | all above |

## UNVERIFIED

- **U1.** The audit's row counts (478 rows; 190, 128, 109, 114 by class). I confirmed the mechanisms and
  spot-checked rows; I did not re-run the registry dump at `eddaa041b`. The conformance test settles it.
- **U2.** Whether `--add-entry model=X` writes the model keys, per-leaf writers for `batch.*`, `build.*`,
  `compute_prices.*`, and whether `setup` writes `endpoints.*`. A hermetic probe in the first task settles
  them; anyone with the tree can run it.
- **U3.** Whether AR1 (#2782) adds `binaries.*` settings rows; the open pull request's diff decides.
- **U4.** `docs/settings-commands/audit.md` stale claims were taken from the audit, not re-read in full.
- **U5.** Current merged count of config-management G0-G13 and settings-commands S10.
- **U6.** Whether the released runner image is built from `images/base/Dockerfile`'s `-tags ee` line and the
  derived images inherit that binary; the Dockerfile says so, a release check would prove it.
- **U7.** Whether a node-version bump is needed for new node members (shapes unchanged, so probably not;
  `projectpolicy.Version` is 7 and AR1 takes 8).
