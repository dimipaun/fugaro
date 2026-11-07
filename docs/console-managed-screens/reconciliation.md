# The console's managed-project screens, reconciled against the code

This file reads [spec.md](spec.md) — the owner's ask of 2026-11-07 — against the console and the CLI
as they are on this tree, and says for each of the five things he named where the fact lives, whether
a screen can read it today, and what a CLI change would have to add. Nothing here is a task approval:
the tasks are [implementation-plan.md](implementation-plan.md), and **not one of D1470–D1489 is
answered**.

**How this was read.** Every claim was checked by reading the named file and symbol, not a summary of
it. **Citations name a path and a symbol**, and a line number only where the line itself is the point
and was read directly; a line number is wrong the next time the file is touched, and this folder has
no pinning test to catch a drifted one (F25). Read against **`origin/main` at `56c91f66`**, 2026-11-07.
The **21 open pull requests** were read with `gh pr list` on the same day, and the one that matters
here — **#1816**, DON7 — is named where it is load-bearing (F22, D1486). Every pull-request head this
checkout could fetch was read too, for the two number blocks (§8).

**Verdicts used here.** *Exists* — the tree already has what the ask needs, and a screen can read it.
*Gap* — the fact exists and nothing prints it in a shape a screen can read. *Rule* — the tree states
how this must stay, and enforces it. *Wrong* — the ask, or a reading of it, states something the code
does not do.

## 1. Headline table: the five things the owner named, and what the tree has

| The ask | What the tree has | Class |
| --- | --- | --- |
| "the project's managed state (the marks: managed / revoked, who admitted what)" | The two marks are real and are read by `managed.ReadMarkFacts` (`internal/infra/managed/read.go`); "who admitted what" is the audit record family (`internal/managed/audit.go`). **One thing prints any of it, and it prints prose**: the `managed-audit` doctor check (F13). No `--json` document carries the marks as fields, and none carries a record. **"Revoked" is not a stored state at all** (F15) | **Gap** |
| "the review account keys (`default_claude_account`, `claude_account_stages`: names only, never a token)" | Both keys exist in the policy node, the local config and the settings registry (F16), and `fugaro settings --json` already prints them as rows with their values (F17) — which the console already spawns (`settings.read`). **Names, not tokens, is already true of the read**: an account is a name, and a `sensitive` row's value is omitted | **Exists** |
| "the policy notes (what the last update changed, runs in flight at the time)" | The node itself is printable (`fugaro project policy show --json`, F18), and the console has **no operation that reaches it** (F4). "What the last update changed" is **printed and not recorded** (F19); "runs in flight at the time" is computed during a write and **recorded nowhere** (F20) | **Gap** (two of the three) |
| "the retirements the last plain `fugaro update` made (derived resources only)" | The retirement set is real and "derived resources only" is enforced by `infra.RetireStops` (F21). `fugaro update --json` carries **the run's own** predicted set; **nothing persists the last run's** | **Gap** |
| "the credentials service state once DON7 lands" | `doctorCredentialsServiceDeployed` returns `nil` for an installation with no `credentials_service` block; **DON7** makes it one line (F22). It is **not this work's to build** | **Gap**, and it is DON7's |

**The shape of the whole thing.** Of the five, **one is readable today with no CLI change at all**
(the review account keys, through a read the console already makes), **three need a field or a record
the CLI does not have** (the marks and the audit trail; the last update's diff and its in-flight
count; the last update's retirement set), and **one belongs to another work** (DON7). Every screen is
read-only, and the console's own contract makes that the cheap half: **a read is a new exact-argv row
and a new IPC operation, and a write is not on the table** (F5, F6).

## 2. The console on this tree

- **Five tabs, one of which is the project.** `VIEWS` (`console/src/renderer/entry.ts:2462`) is
  frozen and holds Live, Graph, Spend, Project and Coordinator. The Project tab carries
  `view: 'caps'`, which is what makes it hide its figures in a reduced console (F9).
- **The Project tab is nine panels from four reads.** `PROJECT_PANEL_KEYS`
  (`console/src/renderer/screens/projects.ts:166`) is the seven-panel model — `caps`, `kill`,
  `repositories`, `accounts`, `secrets`, `policy`, `enrollment` — and the models panel and the
  settings panel are the eighth and ninth, each a read of its own.
- **Every panel names its source, and where there is no source the panel says so.**
  `PROJECTS_SOURCE_LINES` (`projects.ts:107`) is that table: `budget show --json`,
  `accounts ls --json`, `secrets ls --json`, `models ls --json`, `settings --json`.
- **The policy panel is the one with no source.** `POLICY_SOURCE_LINE` (`projects.ts:120`) names the
  command the panel *would* read, and **no row of `CONSOLE_OPERATIONS` carries it**: the panel's
  `operation` is null and it draws the sentence saying it cannot read (F4).
- **The surface and the fence.** `CONSOLE_OPERATIONS` (`console/src/main/ipc.ts:529`) is the whole
  IPC surface; the spawn allow-list is **exact argv** through `argvFor`
  (`console/src/main/spawn.ts:1334`), pinned in rule 6's table of `docs/design/desktop-console.md`
  §5.1. **A new read is a new row on both and a Go change as well**: the table and `consoleReadPaths`
  (`internal/cli/project.go`) are two halves of one fence held equal by a Go test (F26), so a console
  read moves `docs/design/desktop-console.md`, `internal/cli/project.go`, the test that pins them and
  `spawn.ts` — and it is a **SECURITY-SENSITIVE** change, because a new row on that table always is.
- **Two writes, and this work adds none.** `run.launch` (rule 11) and `settings.edit` (rule 12);
  `console/docs/json-contract.md` §9 states the rule and both exceptions.
- **Untrusted text is drawn as text.** Every string from a document goes through `putText` with the
  class its wire key declares; `TEXT_CLASSES` (`console/src/shared/sanitize.ts:84`) is the closed
  list, and rule 7 forbids `innerHTML`, a `dangerouslySetInnerHTML` equivalent and an `href` built
  from it.
- **Sections, not a redraw.** The owner's rule of 2026-10-31 (`.claude/rules/console.md`): every tab
  is independent sections, each with its own read, its own busy state and its own refusal; independent
  reads start in parallel; a section paints the moment its data arrives; a sequence token covers each
  section; and jobs are never part of the first paint. The renderer half is
  `console/src/renderer/section.ts` (`sectionRead`, `renderBusy`, `renderStamp`, `resultCache`).
- **Reduced mode.** A view that shows spend, caps or the kill controls must ask `hidesView`
  (`console/src/shared/version.ts:689`), whose closed list is `REDUCED_VIEWS` (`version.ts:392`).
- **The contract document.** `console/docs/json-contract.md` §6 is "the other documents" — §6.19 is
  `fugaro settings --json` and §6.20 its dry run — and a new document is a new numbered section
  there. Both it and `docs/design/desktop-console.md` are pinned by Go docs tests
  (`internal/cli/docs_desktop_console_test.go`, `docs_console_boundary_test.go`), so a change to
  either runs `go test ./internal/cli/ -run Docs` in the same commit.
- **The Go boundary.** No Go file outside `consoleBoundaryAllowList`
  (`internal/cli/docs_console_boundary_test.go`) may import or name a `console/` path. A Go task in
  the plan therefore names no console file, and a console task names no Go file.

## 3. The CLI's reads on this tree

What each command the screens would read prints today, by symbol. Nothing below is a proposal; the
proposals are the decisions in §7.

**The managed marks and the audit trail.** `internal/infra/managed` holds both marks: the creation
fact `MarkerObject` (`marker.go`) in the runs bucket and the `fugaro_managed` project label
(`ProjectLabelKey`, `ProjectLabelValue`), and a project is managed only when the two agree —
`ReadMarkFacts` (`read.go`) answers `MarkFacts{Managed, Reason, Version, Generation}`. The audit
family is `internal/managed/audit.go`: `AuditRecord` carries `version`, `command`, `argv`, `project`,
`gcp_project`, `plan_digest`, `verdict` (`AuditAdmitted` / `AuditRefused`), `rules`, `marker_version`,
`marker_generation`, `delete_list_generation`, `agent_marker`, `identity` — "the account the write ran
as" — and `at`; the records are filed under `AuditPrefix` and read by `managed.ListAuditRecords`
(`audit_read.go`) and `ParseAuditRecord`. **What prints them:** the `managed-audit` doctor check
(`internal/cli/doctor_managed_audit.go`), which is one `doctorCheck` with an `id`, an `ok`, a
`problem` and a `fix` — **prose**, not fields — and `fugaro managed flag|unflag`
(`internal/cli/managed_flag.go`), which prints human text only. `fugaro managed` itself is `apply`,
`flag` and `unflag` (`internal/cli/managed_apply.go`).

**The doctor document.** `fugaro doctor --json` prints `doctorOutput` (`internal/cli/doctor.go`):
`ok`, `checks[]`, `project`, `fugaro_yaml`, `secrets`, `plugin`, `release`, `error`. Each
`doctorCheck` is `id`, `ok`, `problem`, `fix`, `severity` and `class`. The check IDs this work cares
about are `managed-audit` and the policy family — `policy-published`, `policy-newer-than-binary`,
`policy-older-than-binary`, `policy-unknown-keys`, `policy-vs-jobs`, `policy-rules-skew` — plus
`credentials-service-deployed` (`internal/cli/doctor_brokering.go`). `doctor --json` is already on
rule 6's table, with `--dir D` and never `--strict`.

**The review account keys.** They exist three times, with two spellings. The published policy node
carries `DefaultClaudeAccount` and `ClaudeAccountStages`
(`internal/projectpolicy/policy.go`); the local config carries `default_claude_account` and
`stages.<stage>.claude_account` (`internal/localcfg/localcfg.go`, `StageChoice.ClaudeAccount`); and
the settings registry carries the two rows `default_claude_account` and `stages.*.claude_account`
(`internal/settings/registry.go`), both local-layer, both in `settingsGenericKeys`
(`internal/cli/settings_set.go`) so that `settings set` writes them. **What prints them:**
`fugaro settings --json` prints `settingsDoc` (`internal/cli/settings_render.go`), whose
`settings[]` rows are `settingsJSON` — `key`, `scope`, `value`, `wins`, `default`, `state`,
`sensitive`, `writable`, `owner`, `target_layer`, `levels` — and a `sensitive` row's `value` is
omitted. `settings.read` is already on rule 6's table and already on the Project tab.

**The policy node.** `fugaro project show --json` prints `projectShowDoc`
(`internal/cli/project_cmd.go`): `project`, `gcp_project`, `mode`, `caps`, `policy`, `allow_repos`,
`enrolled`, `running_now`, `running_not_enrolled`, `enrolled_not_running`. Its `policy` embeds
`projectpolicy.Policy`, so the node's own keys — `default_claude_account`, `claude_account_stages`,
`allow_repos`, `base_branch`, `routing`, `model_prices`, `image_catalog` and the rest — are in the
document. `fugaro project policy show --json` prints `{project, policy}` alone. **Neither is on rule
6's table**, so the console reaches neither.

**What the last update changed.** `fugaro update` is `newUpdateCmdWith` → `newInitCmdWith(o,
updateDoor)` (`internal/cli/update.go`). Its policy stage prints the change:
`printPolicyPublishDiff`, `printFirstPublishDiff` and `printPolicyDrift`
(`internal/cli/init_policy_publish.go`). What it **records** locally is a generation, not a diff:
`policyGenerationState` and `policyGenerationRecord` (`init_policy_publish.go`). The runs in flight
are computed by `runsInFlight` (`internal/cli/project_cmd.go`) and `refuseRunsInFlight`
(`internal/cli/init_policy_migrate.go`), which a managed project answers with a note and proceeds;
**the count at the moment of the publish is written nowhere.**

**The retirements.** `infra.Retirement` (`internal/infra/retire.go`) is `address`, `kind`, `cause`,
`kept`, `logical`, `secret_id`, `job`, `region`, `protected`. "Derived resources only" is
`RetireStops` / `retireStop` (`retire.go`): a row stops the whole set if it carries `Kept` text, is
outside `module.repo`, or is not in `tf.RetirableTypes` — and `retireManagedStops`
(`internal/cli/init_retire.go`) spells that out for a managed project in words ("a managed project's
own update retires the derived resources it created and no others"). `printRetirements`
(`init_retire.go`) is the human summary, and `fugaro update --json` (the `--json` flag,
`internal/cli/init.go`) carries the run's own predicted set in `initResult.Retire`. **Nothing
persists it.**

**The credentials service.** `doctorCredentialsServiceDeployed`
(`internal/cli/doctor_brokering.go`) returns `nil` when the local config has no `credentials_service`
block. **DON7** (`docs/credentials-service/default-on.md`, open pull request **#1816**) removes that
early return and produces one line — an `ID`, a `Severity`, a `Problem`, a `Fix` — with five service
states and, per repository, the held-off rows. `fugaro creds ls --json` (`internal/cli/creds.go`)
prints names, presence and last-changed only, and no value.

**The coordinator's own in-flight fact, which is not the same thing.**
`coordinator status --from-store --all --json` is already on rule 6's table, and its `Record.InFlight`
(`ee/coordinator/view.go`) is the coordinator's notion of a run in flight now. It is **not** the
number of runs that were in flight when the last policy publish ran, and the screens must not draw
one as the other (D1483).

## 4. Findings

| # | Finding | Class | Where it lands in the tree |
| --- | --- | --- | --- |
| **F1** | The console draws five tabs, and none of them is a managed screen | **Exists** | `VIEWS` (`console/src/renderer/entry.ts:2462`): Live, Graph, Spend, Project, Coordinator |
| **F2** | The Project tab is the project's own screen: nine panels from four reads | **Exists** | `PROJECT_PANEL_KEYS` (`console/src/renderer/screens/projects.ts:166`) is the seven-panel model; the models panel and the settings panel are each a read of their own |
| **F3** | Its sources are five command lines, and every panel must trace to one | **Exists** | `PROJECTS_SOURCE_LINES` (`projects.ts:107`); the design's review criterion for the screen is that every field traces to a rule 6 row |
| **F4** | The policy panel has no operation, so the console cannot read the policy node at all | **Gap** | `POLICY_SOURCE_LINE` (`projects.ts:120`) names the command and no `CONSOLE_OPERATIONS` row carries it; the panel's `operation` is null and it says what it cannot read |
| **F5** | The console's surface is a closed list and its spawn allow-list is exact argv | **Rule** | `CONSOLE_OPERATIONS` (`console/src/main/ipc.ts:529`), `argvFor` (`console/src/main/spawn.ts:1334`), rule 6's table (`docs/design/desktop-console.md` §5.1) |
| **F6** | Exactly two operations write, and neither is this work's | **Rule** | `run.launch` (rule 11) and `settings.edit` (rule 12); `console/docs/json-contract.md` §9 states the rule and both exceptions |
| **F7** | Every string from a document is drawn as text, through a class the wire key declares | **Rule** | `TEXT_CLASSES` (`console/src/shared/sanitize.ts:84`), `putText`; rule 7 forbids `innerHTML` |
| **F8** | A section has its own read, its own busy state and its own refusal, and independent reads run in parallel | **Rule** | The owner's rule of 2026-10-31 (`.claude/rules/console.md`); `sectionRead`, `renderBusy`, `renderStamp` (`console/src/renderer/section.ts`) |
| **F9** | A view showing spend, caps or the kill controls must withhold in a reduced console | **Rule** | `hidesView` (`console/src/shared/version.ts:689`), `REDUCED_VIEWS` (`version.ts:392`) |
| **F10** | `doctor --json` prints a closed document of checks, and a check carries prose rather than fields | **Exists** | `doctorOutput` and `doctorCheck` (`internal/cli/doctor.go`) |
| **F11** | The managed state is two marks that must agree, read by one function | **Exists** | `MarkerObject`, `ProjectLabelKey`/`ProjectLabelValue` (`internal/infra/managed/marker.go`); `ReadMarkFacts` → `MarkFacts` (`internal/infra/managed/read.go`) |
| **F12** | "Who admitted what" is the audit record family, and it names the account the write ran as | **Exists** | `AuditRecord` and `AuditPrefix` (`internal/managed/audit.go`), `AuditAdmitted`/`AuditRefused`; `ListAuditRecords`, `ParseAuditRecord` (`internal/managed/audit_read.go`) |
| **F13** | One thing prints any of F11 or F12, and it prints prose — so no screen can read the marks or the records as fields | **Gap** | The `managed-audit` check (`internal/cli/doctor_managed_audit.go`) is one `doctorCheck` with a `problem` and a `fix`; `fugaro managed flag\|unflag` (`internal/cli/managed_flag.go`) prints human text only |
| **F14** | `fugaro managed` has three subcommands and none of them reads the state back | **Exists** | `newManagedCmd` adds `apply`, `flag` and `unflag` (`internal/cli/managed_apply.go`) |
| **F15** | "Revoked" is not a stored state; it is a run-time fact of a write gate | **Wrong** (as a screen's fact) | `managedGate.revoked` (`internal/cli/managed_gate.go`) is set by a second mark read taken immediately before a write, and nothing persists it |
| **F16** | The review account keys exist in three places, with two spellings | **Exists** | `internal/projectpolicy/policy.go` (`default_claude_account`, `claude_account_stages`), `internal/localcfg/localcfg.go` (`default_claude_account`, `stages.<stage>.claude_account`), `internal/settings/registry.go` (`default_claude_account`, `stages.*.claude_account`) |
| **F17** | The console can already read those keys, and a sensitive row's value is already withheld | **Exists** | `settingsDoc` and `settingsJSON` (`internal/cli/settings_render.go`); `settings.read` is on rule 6's table and on the Project tab |
| **F18** | The policy node is printable as JSON, by two commands, and the console spawns neither | **Gap** (console side) | `projectShowDoc` (`internal/cli/project_cmd.go`) and `project policy show --json`'s `{project, policy}`; neither is on rule 6's table |
| **F19** | "What the last update changed" is printed, never recorded | **Gap** | `printPolicyPublishDiff`, `printFirstPublishDiff`, `printPolicyDrift` (`internal/cli/init_policy_publish.go`); what is recorded is a generation — `policyGenerationState`, `policyGenerationRecord` |
| **F20** | "Runs in flight at the time" is computed during a write and recorded nowhere | **Gap** | `runsInFlight` (`internal/cli/project_cmd.go`), `refuseRunsInFlight` (`internal/cli/init_policy_migrate.go`); the coordinator's `Record.InFlight` (`ee/coordinator/view.go`) is a different fact |
| **F21** | The retirement set is real, "derived resources only" is enforced, and only the run's own set is printed | **Gap** | `infra.Retirement` and `RetireStops`/`retireStop` (`internal/infra/retire.go`); `retireManagedStops` and `printRetirements` (`internal/cli/init_retire.go`); `initResult.Retire` (`internal/cli/init.go`) is the run's own predicted set |
| **F22** | The credentials service's state is one doctor line, and it does not exist yet | **Gap** | `doctorCredentialsServiceDeployed` (`internal/cli/doctor_brokering.go`) returns `nil` with no block; **DON7** (`docs/credentials-service/default-on.md`) is open pull request **#1816** |
| **F23** | The console's contract document is numbered, and §9 is where the read-only rule lives | **Exists** | `console/docs/json-contract.md` §6.19 (`settings --json`), §6.20 (the dry run), §9 |
| **F24** | No Go file outside a three-entry allow-list may name a `console/` path | **Rule** | `consoleBoundaryAllowList` (`internal/cli/docs_console_boundary_test.go`) |
| **F25** | There is no registry test over `docs/`: a folder that wants its documents pinned writes its own `docs_*_test.go`, and one that does not ships none | **Rule** | `docs/in-job-review/README.md` states it; `docs/image-env/` is a folder with no pin, and this one follows it |
| **F26** | **A console read is a Go change as well as a console one**: §5.1 rule 6's table and `consoleReadPaths` are two halves of one fence, and a Go test holds them equal | **Rule** | `consoleReadPaths` (`internal/cli/project.go`) is keyed by cobra's own command path and marked in `NewRootCmd` (`internal/cli/root.go`); `TestDocsDesktopConsoleDesignAllowListIsReadOnlyInTheCode` and `TestDocsDesktopConsoleDesignAllowListIsExactArgv` (`internal/cli/docs_desktop_console_test.go`) pin the table's **22 argvs** as a closed list and its **21 commands** as a count, in both directions. `TestConsoleAllowListedReadsWriteNothing` (`internal/cli/checkouts_test.go`) is where a read is proved to remember no checkout |
| **F27** | **A new `--json` document is a Go struct and a contract section in one commit**: the contract's shape test reads the CLI's own types | **Rule** | `TestDocsDesktopConsoleContractNamesTheOtherShapesFields` (`internal/cli/docs_desktop_console_test.go`) reflects over a list of the CLI's document structs and requires every json field to be named in `console/docs/json-contract.md`; `TestDocsDesktopConsoleContractNamesOnlyRealFlags` and `…NamesTheFlagInventory` do the same for the flags a documented command carries |

## 5. The attacker probe

The questions a read-only screen still has to answer, each answered from the tree above. "The
operator" is whoever runs the console; "the project" is a managed project; a repository's text is
untrusted (`SECURITY.md`).

| Probe | Answer |
| --- | --- |
| **Can a screen write a managed mark?** | No. There is no operation for it and no spawn row that reaches one: the marks are written by `fugaro managed flag\|unflag` (the owner at a terminal) and by `fugaro setup`/`update --managed`, and **none of those argv is on rule 6's table** (F5, F14). The screens add reads, and rule 6's never-table already refuses `project policy publish` and every writer (F6) |
| **Can a button on these screens edit anything?** | Not if D1471 is answered as recommended: every control is a read, a refresh or an expand. The one row the ask names that `settings set` *could* write — `default_claude_account` — is drawn without a control (D1471) |
| **Does an account name reach the renderer as a secret?** | No. An account is a name by construction (`internal/config/claude_accounts.go`, and `accounts ls --json` "has no access call, so it can't print a value"), the settings read omits a `sensitive` row's value entirely (F17), and no screen asks for a secret. **A name is still untrusted text** — it comes from a config file — so it is drawn through `putText` with its own class like every other document string (F7) |
| **Does the policy node's text reach the renderer as markup?** | No. The node carries operator- and repository-authored strings — `allow_repos`, `clone_urls`, the stage names in `claude_account_stages`, the routing and catalog blocks — and every one of them is a string in a document, drawn through `putText` with the class its wire key declares, never through `innerHTML` and never into an `href` (F7, rule 7) |
| **Can a hostile CLI make the screen render something it did not expect?** | At most text and numbers. The decoders are closed and strict, a member the decoder does not know is ignored rather than drawn, and a document the screen cannot decode is a refusal naming its own command rather than a partial draw (F8) |
| **Does the audit record leak anything the operator did not mean to publish?** | It can: `AuditRecord.Argv` is the argv the write ran as (F12). That is why D1476 recommends a bounded subset — the time, the command, the verdict, the identity and the plan digest — and never `argv` |
| **Does a retirement leak a secret?** | No. `Retirement.SecretID` is a **name**, not a value, and the fields the screen would draw are addresses, kinds, causes and the `kept`/`logical`/`protected` flags (F21). An address is repository-authored text and is sanitized as such |
| **What does a child see?** | Exactly what every other console child sees: the fixed environment (`CHILD_ENV_KEYS`), no credential in an argv, no secret in a variable the app writes, and a working directory that is the app's own (rules 8 and 10). These rows add no environment and no argument beyond the project name |
| **Can the read be made to touch the host's credentials?** | It reads the installation's own configuration and the project's own records, exactly as the reads beside it do. `managed status --json` reads the runs bucket through the operator's own credentials, as `doctor --json` already does — which is why it is on rule 6's table beside `doctor --json` and not beside nothing |

## 6. What the screens may not do

Stated as a list because each one is a fence a later task could move without noticing:

1. **No write, and no button that edits.** No `managed apply`, `managed flag`, `managed unflag`,
   `settings set`, `settings unset`, `project policy publish`, `project repos add|rm`, `project sync`,
   `update` or `run`. The console's two writes stay the launch (rule 11) and the settings edit (rule
   12), and this work adds no third (F6, D1471).
2. **The managed marks are never written from the console.** They are the CLI's, at a terminal, and
   the screens show them (F5, F11).
3. **No figure the Spend screen owns.** These screens show no dollars and no caps: the caps are the
   Project tab's `caps` panel and the spend is the Spend screen's (F2, F9).
4. **No secret, in any form.** No screen asks the CLI for a value, and a row the read marks
   `sensitive` is dropped whole rather than drawn with a blank (F17, D1479).
5. **No derived fact drawn as a read one.** "Revoked" is not shown as a state (F15, D1475), and the
   coordinator's `in_flight` is never drawn as the runs in flight at the last publish (F20, D1483).
6. **No unbounded read.** The audit read is the newest N of a day, not a history the operator pages
   through (D1477), and jobs are never part of the first paint (F8).

## 7. Decisions D1470–D1489

**Not one of these is answered here.** The owner's-answer cell is empty in every row, and a task in
[implementation-plan.md](implementation-plan.md) may not start before its row is answered. Each row
states the question, what this reconciliation recommends and why, the alternative, and what the
alternative costs.

| # | Question | Recommendation (and why) | Alternative | Its cost | Owner's answer |
| --- | --- | --- | --- | --- | --- |
| **D1470** | Is there a managed screen at all, and is it a sixth tab or a group of sections inside the Project tab? | **Sections of the Project tab.** The tab is already the project's own screen (F2), the noun is the same one, and a sixth tab for one project's facts would be a second home for it. The sections are deferred like the models and settings panels, so the first paint does not grow | A sixth tab, "Managed" | A sixth entry in `VIEWS`, a new rail target, a new empty state, and a second screen that has to say which project it is about — for facts the Project tab already sits on top of | |
| **D1471** | Read-only: may any control on these screens write? | **No.** Every control is a read, a refresh or an expand, and the ask's own word is READ-ONLY. The console's two writes are rule 11's launch and rule 12's settings edit, and neither is extended here | Reuse the settings pencil for `default_claude_account` and the stage rows, so the account keys are editable from the same screen | A third write path with its own confirmation, its own dry run, its own allow-list row and its own security review — for one key family, on a work whose whole scope is reading. The pencil stays where it is, on the settings section | |
| **D1472** | Where does the managed state come from: a new structured field, or parsing the `managed-audit` check's prose? | **A structured field.** The check carries `problem` and `fix` sentences (F13); a screen that parsed them would break on the next rewording of either, silently, and would draw a wrong state rather than nothing | Parse the check's `problem`/`fix` text | A screen coupled to prose, with no test that can hold it: the CLI is free to reword, and the console cannot tell a reworded sentence from a changed state | |
| **D1473** | Which command carries it: `fugaro doctor --json`, or a new `fugaro managed status --json`? | **A new `fugaro managed status --json`.** `doctor`'s document is a list of checks whose shape is `id`/`ok`/`problem`/`fix` (F10) and has no room for records; a screen wants one closed document about one project, which is what `coordinator status --from-store --json` already is for the coordinator. It also keeps `doctor`'s document stable for every other consumer | Add `managed` and `managed_audit` members to `doctorOutput` | A new command surface with its own help, its own `--json` shape, its own docs and its own allow-list row — and `doctor`'s document stays as it is. The alternative's cost is a screen's facts living inside a diagnostic document whose shape is prose | |
| **D1474** | Does the read expose the marks' `Version`, `Reason` and `Generation`, or only the boolean? | **All four.** `MarkFacts` already carries them (F11), the generation is what makes "the same project, later" checkable, and the reason is the CLI's own sentence for a project that is *not* managed — which is the answer a screen owes an operator who expected one | The `managed` boolean alone | A screen that says "not managed" and cannot say why, when the CLI already computed the why: a label, not a reading | |
| **D1475** | May a screen say a project is "revoked"? | **No.** "Revoked" is a run-time fact of a write gate and is never persisted (F15); the screen shows the marks' own answer and, beside it, the last audit record's verdict — admitted or refused | Derive "revoked" from the marks' absence, or from a refused record | A state the CLI does not have, drawn as though it did: an operator who removed the label would read "revoked", which is a different claim from "not managed", and the console would be the only place it exists | |
| **D1476** | How much of an audit record reaches the screen? | **A bounded subset: the time, the command, the verdict, the identity and the plan digest, plus the day's count.** These are the fields the check already names, and they are the ones that answer "who admitted what" | Every field, `argv` included | `argv` is the argv the write ran as (F12): a path, a root name or a title the operator did not mean to publish, on a screen anyone at the machine can read. The bounded subset is also what makes the row stable across CLI versions | |
| **D1477** | Is the audit read bounded, and how? | **The newest N of the current UTC day, plus the previous day when the day is empty** — the shape the check already reads (`newestAuditRecord` and its yesterday fallback, F13). It needs no date control and no paging | A date the operator picks, or a paging control over the whole family | A control, a new argument on the argv row, a new bound on the read, and an empty state for a day with nothing in it — for a trail the CLI can already print | |
| **D1478** | Where do the review account keys come from: `settings --json`, or the policy node? | **`settings --json`**, which the console already spawns as `settings.read` and which already carries `default_claude_account` and `stages.*.claude_account` as rows with their values (F16, F17) | Add a `project policy show` row to the allow-list and read the node's `default_claude_account`/`claude_account_stages` | A second read of the same fact, and a second place it can be wrong. The node's spelling is the policy's; the read's spelling is the registry's — two names for one thing is exactly what a screen should not have to reconcile | |
| **D1479** | "Names only, never a token": how is that held rather than promised? | **By the read's own rule.** An account is a name by construction, and a row the read marks `sensitive` is dropped whole — no key, no value, no row — which is the rule the settings-edit design already fixed (F17, rule 12) | Draw whatever the row's `value` holds and rely on these keys never being secret-shaped | A rule that holds until someone adds a key to the same table: the decoder rule is what makes it hold after that, and the promise is what stops holding | |
| **D1480** | Does the console get an operation for the policy node at all? | **Yes.** The Project tab's policy panel has no source today (F4), and the ask's "policy notes" is the node — so this work adds the row and the operation, and the panel stops saying it cannot read | Leave the policy panel as it is and put the policy notes nowhere | The ask's third item is not built, and the Project tab keeps a panel whose only content is the sentence saying it has none | |
| **D1481** | `project show --json` or `project policy show --json`? | **`project policy show --json`.** It prints `{project, policy}` and nothing else (F18) — the smallest document the screen needs. `project show --json` also carries `caps`, which is the Spend screen's and the caps panel's subject, and a screen that read it would be a third home for a dollar figure | `project show --json` | The caps travel with the policy whether or not the screen draws them, and the screen has to drop them — a decoder that must refuse part of a document is a decoder that can get it wrong | |
| **D1482** | What is "the policy notes: what the last update changed"? | **The node as it is, plus doctor's policy checks** — `policy-published`, `policy-newer-than-binary`, `policy-older-than-binary`, `policy-unknown-keys`, `policy-vs-jobs`, `policy-rules-skew` — which are already structured and already say what is wrong with the published node (F10, F19). The change itself is not recorded anywhere, so the screen does not claim it | Record the last publish's diff and show that | A new record the CLI does not keep (F19), a new field on a wire shape the console, mobile and history read, and a screen that is empty until the first publish after the change ships — for a diff the operator can already get from `fugaro update`'s own output | |
| **D1483** | Is "runs in flight at the time" shown, and from where? | **Shown only as the coordinator's own `in_flight`, labelled as the coordinator's** (F20) — or not shown at all if the owner prefers; what the screen must not do is draw it as the count at the last publish, which is recorded nowhere | Record the in-flight count in the publish's own record | A new field on a wire shape three clients read, an addition to a write path, and a number that is a snapshot of a moment nobody can reproduce — for a fact the coordinator already reports live | |
| **D1484** | The retirements: where does the screen get them? | **Nowhere yet.** The screen shows nothing until the CLI records the last plain update's set; the prerequisite is CM1, and the honest empty state is the sentence that says so (F21) | Derive the set live by re-planning the installation | A read that runs Terraform's plan — a write-shaped act with a state backend and a lock, on a screen, for a list the run already computed once. Refused on the merits, not on cost | |
| **D1485** | What does a recorded retirement carry, and does it carry `argv` or a secret? | **The fields `infra.Retirement` already has** — `address`, `kind`, `cause`, `kept`, `logical`, `protected` (F21) — and the secret **name** where one is named. Never a value, and never the Terraform plan | The whole plan, or the addresses with their resource bodies | A plan is a document about the operator's whole project, not about the retirements, and it carries values the retirement set does not need. An address is repository-authored text and is sanitized as such | |
| **D1486** | The credentials-service screen: build it now, or after DON7? | **After DON7 (#1816), and it draws doctor's own line rather than re-deriving the state** (F22). A console that re-derived "is this repository switched on" would be the second answer to a question the CLI answers once, and the first time the two disagreed the console would be the one lying | Read the bundle and the service's configuration directly from the console | A second implementation of the switch's own predicate, a credential-shaped read inside the renderer's process, and a screen that disagrees with `doctor` — which is the one thing the CLI's own design refuses by name | |
| **D1487** | Does any of these views hide in a reduced console? | **No.** They carry no dollars, no caps and no kill control, so they are `null` in `VIEWS` the way Live, Graph and Coordinator are (F9) | Give the group `caps` | A screen that withholds itself in a reduced console although it has nothing to withhold — and a later spend figure added to it would have to revisit this row rather than being stopped by it | |
| **D1488** | How many reads, and do they run in parallel? | **One section per read** — the managed state, the audit trail, the account keys, the policy notes, the retirements and the credentials service — each with its own busy state and its own refusal, started in parallel and painted on arrival, which is the owner's rule of 2026-10-31 and what `section.ts` implements (F8). **And one module per section** (`console/src/renderer/screens/managed.ts` beside the tab's own `projects.ts`, which wires them), so that two tasks never write one file and the tab's own file has one writer per level | One combined read, or one spinner for the group | A screen that waits for its slowest child before drawing anything, and a refusal that cannot name its own command — the two things the rule exists to forbid | |
| **D1489** | The live check, and the release slot. | **Check 360, owner-run (§8), and console 0.121.0 (Raspberry)**, which is the slot the owner already named in the ask. 360 is a fresh block above every claim in the tree and in every pull-request head | Ship in 0.120.0, the release whose notes are already written | The screens would ship before the CLI fields they read (CM1, CM2), so every screen would draw its empty state and the release would be a promise rather than a feature | |

## 8. The number block, and how it was read

**Decisions.** This work holds **D1470–D1489**, and reserves **D1490–D1499** so no second work can take
a number adjacent to it. Read on 2026-11-07, in this checkout:

```sh
# the highest D number written or cited on main (origin/main at 56c91f66)
git grep -h -o -E 'D1[0-9]{3}' origin/main -- docs | sort -n | uniq | tail -1   # D1409
# the highest in any pull-request head this checkout fetched
git fetch origin '+refs/pull/*/head:refs/remotes/pr/*' --depth=1
git grep -h -o -E 'D1[0-9]{3}' $(git for-each-ref --format='%(refname)' refs/remotes/pr) -- docs \
  | sort -n | uniq | tail -1                                                    # D1429
# and nothing in the block this work takes
git grep -l -E 'D147[0-9]|D148[0-9]' origin/main --                            # nothing
git grep -l -E 'D147[0-9]|D148[0-9]' $(git for-each-ref --format='%(refname)' refs/remotes/pr)  # nothing
```

The block is taken **above D1429 rather than at D1430** so that two works in flight cannot claim
adjacent numbers and collide at their merge — the reason `docs/console-settings-edit` took D1100
rather than D1076, and `docs/catalog-build-context` D1390 rather than D1410. The local `main` ref in
this checkout was **stale** (`9f7cb4ca`) while `origin/main` was `56c91f66`; every reading above was
taken against `origin/main` and against the working tree, which are the same commit.

**Checks.** This work claims **Check 360** (implementation-plan §6), with **361–369** available.
Read on 2026-11-07:

```sh
# the highest Check section actually written on main
grep -o '^## Check [0-9]*' docs/gcp-live-checklist.md | grep -o '[0-9]*' | sort -n | tail -1  # 290
# the highest Check number cited anywhere on main
git grep -h -o -E 'Check [0-9]+' origin/main -- docs | grep -oE '[0-9]+' | sort -n | tail -1   # 314
# the highest claimed in a pull-request head
for r in $(git for-each-ref --format='%(refname)' refs/remotes/pr); do
  git grep -h -o -E 'Check [0-9]+' $r -- docs 2>/dev/null | grep -oE '[0-9]+' | sort -n | tail -1
done | sort -n | tail -1                                                                       # 330
```

330 is `docs/api-key-review`'s, in open pull request **#1819**, which takes it "far above the
register's highest written section" for the same reason this work takes 360: a fresh block cannot
collide with a claim nobody has made. The register's own rule — that a claim above the highest written
section is settled at its merge, and the later of two colliding claims renumbers itself — is unchanged
by this folder, and **the register's row and section are written by the task that implements the
check** (plan CM9).

## 9. UNVERIFIED

- **The console 0.121.0 release file does not exist yet.** `docs/releases/console/` holds up to
  `v0.120.0.md`; the plan names the file the release task writes (CM10), and its title ("Raspberry")
  is the owner's word from the ask.
- **DON7's exact check ID and severity are read from its design, not from the tree** (F22): the
  design names five service states and a `warning` severity, and the implementing task owns the
  strings. If DON7's line lands with a different ID, the screen follows the CLI rather than the
  design.
- **`fugaro managed status --json`'s document shape is this design's**, not a fact about the tree: no
  such command exists. Its members are named in the plan (CM2) as a proposal, and the decisions above
  are what settle them.
- **The 21 open pull requests were read with `gh pr list` on 2026-11-07**, and the number blocks were
  read against every `refs/pull/*/head` this checkout could fetch (1,796 refs, depth 1). A pull
  request opened after that day is not in either reading.
- **Nothing here was run.** No command in this file was executed against a real installation: the
  reads are read from the code, and the owner-run check (Check 360) is where the behaviour is proved.
