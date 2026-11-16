# T11 — independent review of the console and plugin changes (#2633)

Reviewed commit `fe0d59d58`: the console/ and plugin/ halves and, because a removal can weaken a refusal anywhere it touched, its `internal/` deletions. Read against plan T11, `spec.md` and `reconciliation.md` F1–F9 (lines 76–78 put the console's `--yes` in T11's scope).

**Verdict: ship on the reviewed scope** — no correctness or security finding in the console or the plugin. Its `internal/` half did leave `internal/cli` red (**F1**, the one real finding); F2 is wording. This review changed no code.

## CHECK 1 — the console

Two argv shapes change, both the settings write in `console/src/main/spawn.ts`:

| console action | before | what gates it now |
| --- | --- | --- |
| settings edit, `widening` | argv ended `--layer L --yes` | the console's own window (`settingsedit.ts:483`, opened at 397) and its main-process re-check `CONFIRM_TICK_NEEDED` (`main/confirm.ts:137`, enforced at 363 — untouched by this commit, so a widening still cannot be confirmed without the tick), with the CLI's coding-agent fence beside them (`internal/cli/settings_write.go:135`) |
| settings edit, `narrowing` | the same, with `--yes` | the same window; no tick, as before |

`fugaro run` (`main/launch.ts`) never prompted and never passed `--yes`; the roadmap move, the graph pane, the log tail and every other row are unchanged. The console answers no question the CLI no longer asks, and keeps the gate it always had: `SETTINGS_EDIT_PHASE_WORDS` (`spawn.ts:1546`) now spells `['--dry-run','--json']` for the dry run and `[]` for both write phases, which is the read-then-write flow `settingsedit.ts` already ran.

The write is not left ungated where the CLI does not refuse: the CLI asks nothing for an ordinary piped settings write (`announceSettingsWrite` → `askConfirm`, true with no `--ask`), as before this commit — `--yes` was never required — and it still refuses a coding agent's session (`settings_write.go:137`) and gates a managed project (`initflow.GateConfirms`, `settings_write.go:169`).

No argv can smuggle a flag: the row's words are literals, every argument is `kind`-checked, a `value` goes through `isSafeArgumentValue` (`spawn.ts:1609`), which refuses a word starting with `-`, the setting's value is emitted behind `--`, and `--layer`/`--project`/`--config` come from `argvFor`, not the renderer. `--yes` is off `SPAWN_FLAGS`, so `assertArgvFlags` would refuse it.

All 23 `SPAWN_FLAGS` exist on the CLI: every one but `--until` (`ee/report/cli/report.go:107`) and `--from-store` (`ee/coordinator/cli/coordinator.go:707`) appears in a build of HEAD's own strings, and those two are the ee-only registrations a core build cannot carry. Every `SPAWN_COMMANDS` path resolves, and no console path appends `--yes` or `--non-interactive`: the only `console/` hits are npm's own `npx --yes electron-builder` in `console/README.md`.

## CHECK 2 — the plugin

`go test ./plugin/` passes: the lint resolves every command and flag a skill spells against the cobra tree, and `plugin/skills_lint_test.go:538` refuses `-y`, `--yes` and `--non-interactive` outright. `go test -tags ee ./ee/plugincheck/` passes too, resolving the paid commands' flags in the tagged tree. A sweep of the command lines in `plugin/skills/**` against a build of HEAD found no missing command and no removed flag; the skills say a session with no terminal is refused and never that a flag confirms, matching plan T11.

## CHECK 3 — internal/ deletions

`CLAUDE.md`, `SECURITY.md` and `website/content/security.md` were not reviewed (T13's).

Every deleted `refuse`/`userErr` branch either lost a confirmation (fine) or kept its guard with only the "or pass `--yes`" escape dropped (fine): the coding-agent fences (`settings_write.go:137`, `image_refresh.go:469`), the in-flight policy guards (`init_policy_migrate.go`, `init_policy_publish.go`), the coordinator opt-in and the retirement. The branches that went with no subject left are `askConflict`, `AskAndYes`, the `--dry-run` + `--yes` pair and the "confirmed by --yes" lines. None removed a guard. `refreshOptions.yes` and `refreshPlan.yes` stay as fields no flag sets (T12 deletes them), so they are always false — stricter, never looser. The one CLI behaviour change is intentional and taught by the skills: `fugaro config init` now writes the repository's `fugaro.yaml` instead of printing it behind `--yes`.

## Findings

- **F1 — correctness.** The commit left `internal/cli` red: `fugaro verify test` on this branch fails 9 tests there (this branch changes one markdown file, so they are main's). Run side by side, the nine pass at `fe0d59d58^` and fail at `fe0d59d58` with the Claude session's markers unset, so this is the commit and not this container's environment. Two causes, both its own sweep: (a) argv still carries the removed flag — `internal/cli/update_catalog_stale_test.go:106,131,172,199` run `update --yes` and `update --yes --json`, refused on the flag (dropping `--yes` and adding `fakeTerminal(t)` fixes the three non-`--json` ones, tried in a throwaway worktree); (b) applying runs modelled under `--json`, which T7/T11 no longer confirm, so they stop at a stage — `TestUpdateIsTotalOverItsOwnStageSet`, `TestUpdateWithACapAndNoMemberRunsOn`, `TestUpdateSkipsAWorkspaceDatabaseThisInstallationDoesNotOwn`, `TestUpdateJSONDoesNotReachTheCatalogRebuild` and `TestUpdateJSONUnchanged` need the shape the commit's own message gives the files it did sweep ("read the run's own account rather than a --json document"). `TestARefusalAfterAFailureStillSaysHowToUnblockIt` wants no terminal and `applyingRig` now fakes one (`noTerminal`). `TestUpdateJSONUnchanged`'s premise — a run with nothing to confirm — does not hold: the rig's plugin stage always has the plugin to install, and the refusal names stage `plugin`. Fix: one follow-up task for the update tests; not done here, because (b) rewrites what these tests pin and that is the owner's call, and a `--yes`-only fix leaves the package red.
  CI's `cli-update` shard runs exactly these tests, so it is red there too unless CI's environment differs in a way I could not reproduce.
- **F2 — wording (T12).** Stale `--yes` names in comments and one test name: `internal/cli/settings_set_test.go:155` (`TestSettingsSetRefusesInAnAgentSessionEvenWithYes`), `internal/cli/image_refresh.go:170`, `internal/cli/accounts_pool.go:63`; and three green test files still pass the flag (`settings_managed_test.go:773`, `settings_dryrun_test.go:671`, `secrets_rm_test.go:301`), so a grep sweep is worth as much as a hand list. No behaviour either way; no built string carries the flag.

## Commands run

```
git show fe0d59d58 -- console/ plugin/     # and the same for internal/, into files
go build -o /tmp/t11/fugaro-main ./cmd/fugaro    # 0 strings hits for the two flags
go test ./plugin/ -count=1 ; go test -tags ee ./ee/plugincheck/ -count=1
go test ./internal/cli/ -run Docs -count=1 ; go test ./internal/cli/ -run Console -count=1
go test -tags ee ./ee/coordinator/cli/ -run ConsoleContract -count=1
go test ./internal/cli/ -run 'TestSettingsSetBacksUpAndWritesAtomically|TestSettingsSetRefusesInAnAgentSessionEvenWithYes|TestSettingsSetRepoLayerIsAllowedInAnAgentSession|TestSettingsSetWritesNothingInTheCloud' -count=1 -v
fugaro verify build ; fugaro verify test -v       # 9 failures in internal/cli, all F1's
# F1's before/after, in detached worktrees, markers unset:
git worktree add --detach /tmp/t11/at-fe0 fe0d59d58 ; git worktree add --detach /tmp/t11/before-fe0 fe0d59d58^
env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u AI_AGENT -u CURSOR_AGENT -u CLAUDE_CODE_SSE_PORT go test ./internal/cli/ -run '<the nine>' -count=1
```
