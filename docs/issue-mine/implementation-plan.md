# `fugaro issue mine`: implementation plan

**Status: plan only; nothing is implemented; no task is approved.** Each task below needs the owner's explicit go, by name; answering a decision approved none. This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (which wins).

**Owner answers that shaped it (2026-11-08):** D1780 the user is the verified credential address, cached for offline display reads (recommended); D1781 three additive date fields on the record (recommended); D1782 "covered" needs the CLI and the installation record at or above `resolved_in` (recommended); D1783 row order: regression, fixed-not-updated, local and pending, submitted, resolved and dismissed (recommended); D1784 **no GitHub access in `mine`: go by `resolved_in` alone (not the recommendation)**.

## Prerequisites outside this plan

`mine` is a view over two plans that are docs only when this is written. Nothing here can start before the task it names has merged.

| Needs | From | Used by |
| --- | --- | --- |
| the record, its codec and the local store (`internal/issues/issue.go`, `store.go`) | troubleshoot-issue T-IS1 | T-IM2, T-IM6 |
| the record schema (`schemas/issue-record.schema.json`) | T-IS3 | T-IM2 |
| `issue.go`, `issue_list.go` (the commands) | T-IS5 | T-IM6 |
| the shared store (`internal/issues/shared.go`: `Upload`, `ListShared`, occurrences, the shared dismissal) | T-IS10, T-IS11 | T-IM2, T-IM3 |
| `RecordSubmitted` | T-IS12 | T-IM2 |
| `SyncPending` | T-IS11 | T-IM6 |
| `resolved`, `resolved_in`, `SetResolved`, `SetClosed` | update-plan T-RU3 | T-IM2, T-IM3 |
| the installation record reader (`internal/installrec`) and `updatecheck.CanonicalRelease`/`AtOrBelow` | T-RU2, T-RU1 (the two helpers only; its GitHub reader is not used, D1784) | T-IM6 |
| the command-tree check for a printed command (`internal/diagnoseagent/fixcmd.go`, D1614) | diagnose-agent T-DG8 (T-IS19 needs it too) | T-IM6 |
| the issues page, the plugin paragraph, the docs test | T-IS9 | T-IM7 |

## Conventions

`CLAUDE.md` and the rules it loads: `.claude/rules/credentials-service.md` does not apply (the identity is read through `access.Caller`, no credential is stored or written); `.claude/rules/terraform.md` does not apply (no Terraform change; the launcher grant is troubleshoot-issue T-IS17's). Hermetic tests only (`internal/testutil`, `gcpfake.NewGCS` for the shared store, `t.TempDir()`, an injected clock, environment, agent-marker seam, version and `Caller`); no network, no real home, no GitHub. **No secret-shaped literal** in a test, doc or fixture: sample addresses are `user@example.test`. No live cloud action by an agent; every check below is owner-run. Docs are tested: edit a doc, run `go test ./internal/cli/ -run Docs`, keep the pinned fact. Comments explain why. A documented command change updates `plugin/skills` in the same PR (T-IM7). The one JSON shape a reader outside the binary consumes (`mine --json`) is versioned and its field names are new. Run targeted tests, never whole `internal/cli`. **Release notes:** nothing runs after deploy; the operator line stays "run `fugaro update`" (F16).

## Global constraints and review focus

| Constraint (spec, finding or decision) | Pinned by |
| --- | --- |
| A user sees the issues they created and the duplicates of theirs merged into another's (acceptance 1, F7) | T-IM3 (`TestMineFindsOwnAndMergedIssues`), T-IM6 |
| A resolved issue shows the fixing version and, when behind, "run `fugaro update`" (acceptance 2, D1782) | T-IM1, T-IM6 |
| Works offline: local rows and the last known state (acceptance 3, F9) | T-IM5, T-IM6 (`TestMineOfflineUsesSnapshot`), Check 471 |
| `mine` makes no request to GitHub (D1784, F6) | T-IM6 (`TestMineMakesNoGitHubRequest`: an `http.RoundTripper` that fails the test on any call) |
| Display status is derived once: `local`, `regression` are not record values (F2) | T-IM1 |
| Covered = CLI and installation both at or above `resolved_in`; a `dev` CLI is unknown (D1782, F5) | T-IM1 |
| The identity cache never drives a write (F3) | T-IM4 (mutation check), T-IM6 |
| The retry suggestion is only this machine's own command, only for a covered row, only a `fugaro` command in the tree, never from the shared record (F8) | T-IM6 (`TestMineNeverPrintsSharedCommand`) |
| Other reporters are counted, never named, and the user is not counted (F15) | T-IM3, T-IM6 (`TestMineJSONHasCountsNotNames`) |
| Shared text is data: `safetext`, strict URL shape, strict `resolved_in`, validated numbers and digests, `ReadMax` (F11) | T-IM3, T-IM6 (`TestMinePrintsHostileSharedRecordAsData`) |
| A coding agent's session reads local files and the snapshot only; no shared read, no upload (F10) | T-IM6 (mutation check on the guard) |
| Ordering and the 30-day hiding (F13) | T-IM1 |
| The regression row is the user's own flagged occurrence, not another reporter's (F14) | T-IM3, T-IM1 |
| `list --mine` and `mine` are one function; `--all` is refused without `--mine` (F12) | T-IM6 |
| The listing is one paginated names-only call, capped, never an error when truncated (F7) | T-IM3 |

## Slice 1: the parts that stand alone

### T-IM1: the row model
- **Files:** `internal/issues/mine.go` (new), `internal/issues/mine_test.go` (new). **Interfaces:** consumed nothing outside the standard library and `updatecheck`'s release helpers; produced the pure function `BuildRows(in MineInput) (rows []Row, hidden int)` where `MineInput` is its own struct (the user's local issues, the shared views of the user's issues, the CLI version, the installation release when known, the clock, `All bool`) and `Row` is `{Number, LocalID, Summary, Display, HitAt, SubmittedAt, ResolvedAt, DismissedAt, URL, ResolvedIn, Coverage, OthersHit, AsOf}`. `Display` is `local|pending|submitted|resolved|dismissed|regression` (F2), `Coverage` is `covered|behind|unknown|none` (F5), and the function orders and hides (F13). It reads no file, clock or environment. **Schema/rules/config change:** none. **Tests:** `TestMineDisplayStatusDerivation` (a table over local, uploaded, dismissed-locally, regression-flagged, resolved), `TestMineCoverageCLIAndInstallation`, `TestMineCoverageInstallationUnknownFallsBackToCLI`, `TestMineCoverageDevBuildIsUnknown`, `TestMineOrderGroups` (the five groups of D1783, and the within-group order), `TestMineHidesOldResolvedAndDismissedOnly`, `TestMineNeverHidesBehindOrRegressionOrUndated`, `TestMineAllShowsEverything`, `TestMineHiddenCount`. **Command:** `go test ./internal/issues/ -run 'TestMine(Display|Coverage|Order|Hides|Never|All|Hidden)' -count=1`. **Depends on:** nothing. **Commit:** `issues: the rows of issue mine`. **Red step:** the order test fails to compile until `BuildRows` exists, then fails while rows sort by number. **Reviewer tier:** standard. Not OWNER-RUN.

### T-IM2: the record's three dates
- **Files:** `internal/issues/issue.go`, `internal/issues/issue_test.go`, `internal/issues/shared.go`, `internal/issues/shared_test.go`, `schemas/issue-record.schema.json` (each written by T-IS1, T-IS10, T-IS3 and, for the resolver's setters, T-RU3, which must have merged). **Interfaces:** the record and the local header gain `submitted_at`, `resolved_at` and `dismissed_at` (strict RFC 3339 UTC, empty until the transition); `RecordSubmitted`, `SetResolved`, `SetClosed` and the shared dismissal stamp the matching field from an injected clock in the same generation-matched write that makes the transition; a record that lacks a field decodes with it empty; the fields never change an existing field's meaning (D1781). **Schema/rules/config change:** the record schema gains the three optional fields. **Tests:** `TestSubmittedAtStampedWithSubmission`, `TestResolvedAtStampedByResolver`, `TestDismissedAtStampedByDismissAndByClosedAs`, `TestRecordWithoutDatesDecodes`, `TestDatesRejectNonUTCOrMalformed`, `TestSchemaMatchesRecordWithDates`. **Command:** `go test ./internal/issues/ -run 'TestSubmittedAt|TestResolvedAt|TestDismissedAt|TestRecordWithoutDates|TestDatesReject|TestSchemaMatchesRecordWithDates' -count=1`. **Depends on:** nothing in this plan (the prerequisites above). **Commit:** `issues: the record carries when it was submitted, resolved and dismissed`. **Red step:** the stamp tests fail while the transitions write no time. **Reviewer tier:** standard (the fields are display data; the writers' generation matching is unchanged and re-tested by the prerequisites' own tests). **Release note:** none beyond the train rule (O2).

### T-IM4: the identity (SECURITY-SENSITIVE)
- **Files:** `internal/issues/identity.go` (new), `internal/issues/identity_test.go` (new). **Interfaces:** consumed `access.Caller` through an injected seam, `localcfg.UserDir(getenv, goos, cache)`; produced `Resolve(ctx, deps) (Identity, error)` with `Identity{Address, Digest, Source}` and `Source` one of `verified`, `cached`, `none`. A verified lookup (bounded by its own short timeout, a constant under the 20 seconds `access` allows) returns the address, lower-cased, and rewrites the cache (user cache dir, mode 0600 in a 0700 directory, a `format` field, strict decode, atomic); a failed lookup returns the cached address with `Source: cached`; neither returns `none`. `Digest` is the truncated SHA-256 of the lower-cased address (F7), the one function the store's key layout and `mine` share. **No upload, claim or dismissal reads the cache:** `VerifiedOnly(ctx, deps)` is the entry those paths use and refuses a cached answer. **Tests:** `TestResolveVerifiedWritesCache`, `TestResolveOfflineUsesCache`, `TestResolveNoCacheNoneWhenOffline`, `TestResolveRefusesServiceAccountAndWritesNoCache`, `TestVerifiedOnlyIgnoresCache`, `TestCacheIsPrivateAndStrict`, `TestDigestIsStableAndCaseInsensitive`. **Command:** `go test ./internal/issues/ -run 'TestResolve|TestVerifiedOnly|TestCacheIs|TestDigest' -count=1`. **Depends on:** nothing. **Commit:** `issues: the caller's identity, cached for display reads`. **Red step:** `TestVerifiedOnlyIgnoresCache` fails while the one function returns a cached address everywhere; **mutation check:** make `VerifiedOnly` fall back to the cache and the test must fail. **Reviewer tier:** `opus + mandatory independent review`. Not OWNER-RUN.

### T-IM5: the offline snapshot
- **Files:** `internal/issues/snapshot.go` (new), `internal/issues/snapshot_test.go` (new). **Interfaces:** consumed `localcfg.UserDir(..., cache)`, T-IM1's `Row` (through its own plain struct so the file imports nothing from T-IM1); produced `WriteSnapshot(dir, rows, now)` and `ReadSnapshot(dir) (Snapshot, error)` for the last successful `mine` (user cache directory, mode 0600, `format: 1`, strictly decoded, atomic write; a decode failure or an unknown format is "no snapshot", never an error). It stores the display data only: **no command** and no other reporter's identity. **Tests:** `TestSnapshotRoundTrip`, `TestSnapshotRejectsUnknownFieldAndFormat`, `TestSnapshotHoldsNoCommandAndNoReporter`, `TestSnapshotIsPrivate`, `TestSnapshotWriteIsAtomic`, `TestSnapshotCorruptIsNoSnapshot`. **Command:** `go test ./internal/issues/ -run 'TestSnapshot' -count=1`. **Depends on:** nothing. **Commit:** `issues: a local snapshot of the last issue mine`. **Red step:** the round-trip test fails to compile until the codec exists; the no-command test fails while the row struct is copied whole. **Reviewer tier:** standard.

## Slice 2: the shared reader

### T-IM3: the occurrence names and the reader (SECURITY-SENSITIVE)
- **Files:** `internal/issues/shared.go`, `internal/issues/shared_test.go` (amended: the occurrence key layout, written by T-IS10 and edited by T-IM2 one level earlier), `internal/issues/mineread.go` (new), `internal/issues/mineread_test.go` (new). **Interfaces:** consumed `internal/blobx` (the embedded bucket's names-only `List`, `ReadMax`), T-IM4's `Digest`, T-IM2's fields; produced the key layout `issues/occurrences/<NNNN>/<reporter-digest>/<time>` for **every** hit, the issue's first report included (F7), and `ListMine(ctx, bucket, id, now) (Shared, error)` that makes one paginated names-only listing of `issues/occurrences/` (capped at a constant 5000 names; a longer listing sets `Truncated`), validates each name by shape (number digits, digest hex, time strictly parsed; a malformed name is skipped), and from names alone returns, for each issue the user reported, the user's earliest hit, the count of distinct other digests and the user's own occurrence keys; then reads the user's issue records (`ReadMax`, bounded) and, for `resolved` ones only, the user's own occurrence objects to learn the regression flag. Nothing else is read. **Tests (`gcpfake.NewGCS`):** `TestMineFindsOwnAndMergedIssues`, `TestMineCountsOthersByDigestNotNames`, `TestMineSamePersonTwoMachinesCountsOnce`, `TestMineUserNotCountedAsOther`, `TestMineListingIsOnePaginatedNamesOnlyCall` (a counting wrapper: no object read for membership), `TestMineListingCapSetsTruncated`, `TestMineSkipsMalformedNames` (a `../` number, a short digest, a bad time), `TestMineReadsOnlyOwnRecords`, `TestMineRegressionIsTheUsersOwnFlaggedOccurrence`, `TestMineOtherReportersRegressionDoesNotMakeMineOne`, `TestMineBoundedRecordRead`. **Command:** `go test ./internal/issues/ -run 'TestMine(Finds|Counts|Same|User|Listing|Skips|Reads|Regression|Other|Bounded)' -count=1`. **Depends on:** T-IM2 (it edits `shared.go` after it), T-IM4 (the digest). **Commit:** `issues: occurrence names carry the reporter digest; list a user's issues from names`. **Red step:** `TestMineListingIsOnePaginatedNamesOnlyCall` fails against a reader that fetches every occurrence; **mutation check:** drop the shape validation and `TestMineSkipsMalformedNames` must fail. **Reviewer tier:** `opus + mandatory independent review` (it reads a store any launcher can write and fixes a key layout). Not OWNER-RUN.

## Slice 3: the command

### T-IM6: `fugaro issue mine` and `list --mine`
- **Files:** `internal/cli/issue_mine.go` (new), `internal/cli/issue_mine_test.go` (new), `internal/cli/issue_list.go` (the `--mine` and `--all` flags; written by T-IS5 and T-IS11), `internal/cli/issue.go` (registers `mine`; written by T-IS5, T-IS7, T-IS11), `schemas/issue-mine.schema.json` (new). **Interfaces:** consumed T-IM1, T-IM3, T-IM4, T-IM5, the local store (T-IS1), `SyncPending` (T-IS11), `installrec.Read` (T-RU2), `updatecheck.CanonicalRelease` and `AtOrBelow` (T-RU1), the `agentMarker` guard, the command-tree check, `internal/safetext`, `cli.Version`. The flow: resolve the identity (T-IM4); in a coding agent's session read local files and the snapshot only; elsewhere call `SyncPending` as `list` does (one line on failure), read the installation record, call `ListMine`, build rows, write the snapshot; offline (the shared read fails) merge the snapshot with local files and print "offline: shared state as of <time>"; with no snapshot print local rows only and say so. Printing: the number (`#0007` or the local ID), the summary, the display status, the three dates (`-` when absent), the GitHub link (only in the shape `https://github.com/dimipaun/fugaro/issues/<n>`) for `submitted`, `resolved` and `regression`, `resolved_in` and the coverage for resolved rows ("fixed in 0.22.1; you are on 0.22.0: run `fugaro update`"; "installation not checked" when the record is unread; "dev build: cannot compare" for a dev CLI), "also hit by N others" (counts only), and for a **covered** row only the command from **this machine's own issue file**, through `safetext`, only if it is a `fugaro` command in the CLI's tree, with "commands can change things; nothing is run for you"; a hidden count line when rows are hidden. `list --mine` runs this same function; `--all` is refused on `list` without `--mine`. `--json` prints `{"format":1,"source":"shared|snapshot|local","as_of":…,"installed":…,"hidden":N,"truncated":bool,"issues":[…]}`, counts only. The command makes no request to GitHub (D1784) and writes only the snapshot and the identity cache. **Schema/rules/config change:** `schemas/issue-mine.schema.json`. **Tests:** `TestMineListsOwnIssuesFirstGroupFirst`, `TestMineResolvedBehindShowsUpdateNudge`, `TestMineResolvedCoveredShowsLocalCommandOnly`, `TestMineNeverPrintsSharedCommand`, `TestMineRetryOnlyForFugaroCommandInTree`, `TestMineOfflineUsesSnapshot`, `TestMineOfflineWithoutSnapshotShowsLocalOnly`, `TestMineAgentSessionReadsLocalOnly` (the mutation check on the guard), `TestMineSyncsLikeList`, `TestMineMakesNoGitHubRequest`, `TestMinePrintsHostileSharedRecordAsData`, `TestMineRejectsForeignURLShape`, `TestMineJSONVersionedAndMatchesSchema`, `TestMineJSONHasCountsNotNames`, `TestListMineAliasIsSameOutput`, `TestListAllWithoutMineIsRefused`, `TestMineDoesNotSetNotifiedFlag`. **Command:** `go test ./internal/cli/ -run 'TestMine|TestListMine|TestListAllWithoutMine' -count=1`. **Depends on:** T-IM1, T-IM3, T-IM4, T-IM5. **Commit:** `cli: fugaro issue mine`. **Red step:** the tests fail with "unknown command mine"; the GitHub test fails if any reader of `internal/issueresolve` is wired in. **Reviewer tier:** `opus + mandatory independent review` (it prints another party's text and a command suggestion). Not OWNER-RUN.

### T-IM7: docs, plugin and release notes
- **Files:** `docs/issues.md` (a section; the page is T-IS9's), `plugin/skills/working/SKILL.md` (one sentence naming `fugaro issue mine`; lint), `docs/releases/` (the next release's notes: the command, "run `fugaro update`" as the operator line, and the known limit of O1), `internal/cli/docs_issue_test.go` (extended; T-IS9's file). **Tests:** `TestDocsIssueNamesMine` (the page names `mine`, `list --mine`, `--all`, `--json`, the 30-day rule, the offline snapshot and the identity cache, and says no GitHub is read) and the plugin command-and-flag check. **Command:** `go test ./internal/cli/ -run 'TestDocsIssue' -count=1` and `go test ./plugin/... -count=1`. **Depends on:** T-IM6. **Commit:** `docs: fugaro issue mine`. **Red step:** the docs test fails on the missing section. **Reviewer tier:** the plugin skill is a prompt-class path, so `opus` by the router's path rule; otherwise standard. Not OWNER-RUN.

## Parallel execution

**Edges (from "Depends on"):** T-IM3 ← T-IM2, T-IM4; T-IM6 ← T-IM1, T-IM3, T-IM4, T-IM5; T-IM7 ← T-IM6. T-IM1, T-IM2, T-IM4 and T-IM5 depend on nothing in this plan.

| Level | Tasks (run concurrently) |
| --- | --- |
| 0 | T-IM1, T-IM2, T-IM4, T-IM5 |
| 1 | T-IM3 |
| 2 | T-IM6 |
| 3 | T-IM7 |

**Critical path:** T-IM2 → T-IM3 → T-IM6 → T-IM7 (four levels). T-IM3 follows T-IM2 because both edit `internal/issues/shared.go` and its test, and follows T-IM4 for the shared digest; T-IM6 needs every piece; T-IM7 needs the command to exist.

**Shared-files table (no level holds two writers of a file):**

| File | Writers (level) |
| --- | --- |
| `internal/issues/issue.go`, `issue_test.go` | T-IM2 (0) |
| `internal/issues/shared.go`, `shared_test.go` | T-IM2 (0), T-IM3 (1) |
| `schemas/issue-record.schema.json` | T-IM2 (0) |
| `internal/issues/mine.go`, `mine_test.go` | T-IM1 (0) |
| `internal/issues/identity.go`, `identity_test.go` | T-IM4 (0) |
| `internal/issues/snapshot.go`, `snapshot_test.go` | T-IM5 (0) |
| `internal/issues/mineread.go`, `mineread_test.go` | T-IM3 (1) |
| `internal/cli/issue_mine.go`, `issue_mine_test.go`, `schemas/issue-mine.schema.json` | T-IM6 (2) |
| `internal/cli/issue.go`, `internal/cli/issue_list.go` | T-IM6 (2) |
| `docs/issues.md`, `plugin/skills/working/SKILL.md`, `internal/cli/docs_issue_test.go`, the release notes | T-IM7 (3) |

**Outside this plan:** T-IM2 edits files that T-IS1, T-IS3, T-IS10 and T-RU3 write, and T-IM6 edits `issue.go` and `issue_list.go`, which T-IS5, T-IS7, T-IS11 and update-plan T-RU8 write; T-IM2 and T-IM6 must not share a level with any of those, and the later to land rebases. T-IM7 shares `plugin/skills/working/SKILL.md` and `docs/issues.md` with T-IS9, update-plan T-RU7 and diagnose-agent T-DG8.

## Checks (reserved Checks 470–479; all OWNER-RUN)

- **Check 470, the shared lookup across machines and people.** On a project with the issue store: the owner (a non-operator launcher login) saves an issue on machine 1 (`fugaro issue sync`), then on machine 2 with the same Google login types `fugaro issue mine` and confirms the issue is listed with its number; a second person types `fugaro issue mine` after hitting the same failure and uploading, and both see "also hit by 1 other" and the same number. Measures F7 and U1 (whether a launcher can list `issues/occurrences/`).
- **Check 471, offline.** After one online `fugaro issue mine`, the owner turns the network off and types `fugaro issue mine` again: rows appear with "offline: shared state as of <time>", in a few seconds. Then with a cleared cache directory, it prints local rows only and says so. Measures F9 and U3 (how long the credential lookup takes with no network).
- **Check 472, the listing at size.** On the project with the most issues available, the owner types `time fugaro issue mine --json` and records the issue count and seconds, and whether `truncated` is true. Measures U2 and whether the 5000-name cap is right.

## Considered and declined

- **Refreshing GitHub from `mine`** (the spec's own lines). Declined by D1784: the owner goes by `resolved_in` alone.
- **A per-reporter index object in the store.** One names-only listing already answers membership, hit time and count (F7); an index would be one more writer to keep consistent.
- **Printing other reporters' names.** Declined (F15): counts only; the update plan prints names to the admin for a reason `mine` does not share.
- **Suggesting the shared record's command.** Declined (F8): only this machine's own command, as update-plan O4 ruled.
- **A `mine`-written `resolved`.** Declined (D1784): only `fugaro update` writes it.
