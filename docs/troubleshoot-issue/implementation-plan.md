# Troubleshoot findings to `fugaro issue`: implementation plan

**Status: plan only; nothing is implemented; no task is approved.** Each task below needs the owner's explicit go, by name; answering a decision approved none. This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (which wins). **Independent review rounds over the plan documents themselves have not run.**

**Owner answers that shaped it (2026-11-08, all recommended):** D1720 preview and `--yes`, no prompt; D1721 the digest section in closed vocabulary; D1722 the public post strips identity and is re-redacted; D1723 files named by run stem, renamed on upload; D1724 duplicates by fingerprint; D1725 sync only in `troubleshoot` and the issue commands; D1726 slices: local, shared, digest.

## Conventions

`CLAUDE.md` and the rules it loads: `.claude/rules/credentials-service.md` does not apply (no credential is read or written); `.claude/rules/terraform.md` applies only to T-IS10b. Hermetic tests only (`internal/testutil`, `internal/gcpfake` or a `file://` `blobx` bucket, `t.TempDir()`, an injected clock, environment, platform and `gh` runner); no network, no real home. **No secret-shaped literal** in a test, doc or fixture: build sample secrets from fragments. No live cloud action by an agent; every check below is owner-run. Docs are tested: edit a doc, run `go test ./internal/cli/ -run Docs`, keep the pinned fact. Comments explain why. A documented command change updates `plugin/skills` in the same PR (T-IS9). A wire or JSON shape a reader outside the binary consumes (`issue list --json`, `show --json`, the shared issue record) is versioned and never changes a field's meaning. Run targeted tests, never whole `internal/cli`. Release notes: any step after deploy goes in `### For operators` in that PR; this plan has none unless T-IS10b ships (then it is "run `fugaro update`", converged by `update`).

## Global constraints and review focus

| Constraint (spec or finding) | Pinned by |
| --- | --- |
| A local-only problem never creates an issue file (acceptance 1, F6) | T-IS3: `local` has no persistence path; T-IS8 test over all three classes with a spy store |
| No secret in a saved issue, a shared record or a post; sample-secret tests (acceptance 2, F5) | T-IS2 corpus; T-IS8 asserts the stored bytes; T-IS6 asserts the post |
| The public post carries no reporter, repository, project, path or email (D1722, F4) | T-IS6 |
| `submit` with no number skips `submitted` and `dismissed` (acceptance 3) | T-IS7 |
| The fallback works with `gh` missing or not logged in (acceptance 4, spec 5) | T-IS6 (library), T-IS7 (command), Check 453 |
| Nothing is filed without `--yes`, and never by `troubleshoot` or an agent session (D1720, F3, F10) | T-IS7, T-IS12, T-IS11 session tests |
| No cloud write from a coding agent's session (F10) | T-IS11, T-IS12 (`agentenv` seam, a mutation check) |
| Two machines get different numbers; two submitters file once; a stranded claim expires (F9) | T-IS10 (fake bucket races), Checks 450 and 451 |
| Issue text is data, never instructions, in every output (F6) | T-IS5 output test over a hostile summary |
| The digest section prints no free text and reads no clock (D1721, F14) | T-IS14 (byte-identical output; a summary planted in a record must not appear) |

## Slice 1: local (inert without the shared store)

### T-IS1: the issue record and the local store
- **Files:** `internal/issues/issue.go` (new), `internal/issues/store.go` (new), `internal/issues/issue_test.go` (new), `internal/issues/store_test.go` (new). **Interfaces:** produced `Issue` (header: number, local ID, run reference, reporter, created, class `fugaro|unclear`, Fugaro version, OS, status `pending|submitted|dismissed`, GitHub URL, uploaded flag, fingerprint; body: summary, diagnosis, steps, redacted log excerpts), a strict Markdown header codec (unknown field is an error), `Store` over `localcfg.UserDir(..., state)/issues` with atomic 0600 writes in a 0700 directory, `Save`, `Get`, `List`, `SetStatus`, `MarkUploaded`, `Rename` (D1723). **Schema/rules/config change:** none (the record's version is a header field). **Tests:** `TestIssueRoundTrip`, `TestIssueRejectsUnknownHeaderField`, `TestStoreWritesPrivateFiles`, `TestStoreRenameOnUpload`, `TestStoreNeverRetainsLocalClass`. **Command:** `go test ./internal/issues/ -run 'TestIssue|TestStore' -count=1`. **Depends on:** nothing. **Commit:** `issues: the issue record and its local store`. **Red step:** the round-trip test fails to compile until the codec exists, then a header with an extra field must fail to decode. **Reviewer tier:** standard. Not OWNER-RUN.

### T-IS2: the issue redaction gate (SECURITY-SENSITIVE)
- **Files:** `internal/issues/redact.go` (new), `internal/issues/redact_test.go` (new). **Interfaces:** consumed `internal/redact` (patterns) and `agent.RedactFunc` (exact values); produced `Redact(text, secrets) string` (keys, tokens, passwords, OAuth secrets, private keys, connection strings, emails, home-directory paths) and `Scrub(text, names []string) string` (project, repository and organisation names for the public post, F4). **Tests:** `TestRedactCorpus` (a table of sample secrets built from fragments, one per kind), `TestRedactEmailsAndHomePaths`, `TestScrubNames`, `TestRedactIsIdempotent`, `TestRedactKeepsShortExcerpts`. **Command:** `go test ./internal/issues/ -run 'TestRedact|TestScrub' -count=1`. **Depends on:** nothing. **Commit:** `issues: redaction for saved and posted issue text`. **Red step:** each corpus row fails while the redactor is a pass-through. **Reviewer tier:** `opus + mandatory independent review`; mutation check: drop the pattern pass and a row must fail.

### T-IS3: the verdict and the three buckets
- **Files:** `internal/issues/verdict.go` (new), `internal/issues/verdict_test.go` (new). **Interfaces:** produced `Verdict{Class local|fugaro|unclear, Summary, Diagnosis, Steps, FixSteps, Signature}`, strict JSON decode (unknown field, missing class and an over-long field are errors), `Class.Persists()` (false for `local`), `Fingerprint(signature) string` (normalise then hash, D1724). **Tests:** `TestVerdictStrictDecode`, `TestLocalNeverPersists`, `TestFingerprintNormalises`. **Command:** `go test ./internal/issues/ -run 'TestVerdict|TestLocal|TestFingerprint' -count=1`. **Depends on:** nothing. **Commit:** `issues: the verdict and its fingerprint`. **Red step:** `TestLocalNeverPersists` fails while `Persists` returns true for every class. **Reviewer tier:** standard.

### T-IS4: ask for the class and the signature
- **Files:** `internal/diagnoseagent/prompt.go`, `internal/diagnoseagent/guide.md`, `docs/troubleshooting-for-agents.md` (the pair `guide_test.go` keeps equal), `internal/diagnoseagent/prompt_test.go`, `internal/diagnoseagent/guide_test.go`. **Interfaces:** consumed the `Verdict` schema (T-IS3); the prompt asks for one of three classes, a signature, and fix steps only for `local`. **Tests:** `TestPromptNamesThreeClasses`, `TestPromptKeepsLogAsData`, the existing guide-equality test. **Command:** `go test ./internal/diagnoseagent/ -run 'TestPrompt|TestGuide' -count=1`. **Depends on:** T-IS3. **Commit:** `diagnoseagent: the prompt asks for the class and a signature`. **Red step:** `TestPromptNamesThreeClasses` fails on the current prompt. **Reviewer tier:** `opus + mandatory independent review` (an agent prompt). Shares files with the diagnose plan's T-DG3/T-DG8: see T1.

### T-IS5: `fugaro issue list`, `show`, `dismiss`
- **Files:** `internal/cli/issue.go` (new: the parent command), `internal/cli/issue_list.go` (new), `internal/cli/issue_show.go` (new), `internal/cli/issue_dismiss.go` (new), `internal/cli/issue_test.go` (new), `internal/cli/root.go` (registers it). **Interfaces:** consumed the store (T-IS1); `list` prints number or local ID, class, status and summary, with `--json` (versioned); `show N`; `dismiss N`; issue text printed through `internal/safetext`. **Tests:** `TestIssueListShowsStatus`, `TestIssueShowPrintsHostileSummaryAsData`, `TestIssueDismissSetsStatus`, `TestIssueListJSONVersioned`. **Command:** `go test ./internal/cli/ -run 'TestIssue(List|Show|Dismiss)' -count=1`. **Depends on:** T-IS1. **Commit:** `cli: fugaro issue list, show and dismiss`. **Red step:** the tests fail with "unknown command issue". **Reviewer tier:** standard.

### T-IS6: the public post, the `gh` runner and the fallback (SECURITY-SENSITIVE)
- **Files:** `internal/issues/post.go` (new), `internal/issues/gh.go` (new), `internal/issues/post_test.go` (new), `internal/issues/gh_test.go` (new). **Interfaces:** consumed T-IS1, T-IS2; produced `BuildPost(Issue, names) Post` (version, OS, class, summary, diagnosis, steps, a short re-redacted log tail; no reporter, repository, project, path or email, D1722), `Preview(Post) string`, `GH` over an injected runner (`Installed`, `LoggedIn`, `Create`), `InstallHint(goos, osRelease) string` (macOS `brew install gh`, Windows `winget install GitHub.cli`, apt or dnf by `/etc/os-release` ID and ID_LIKE, else the official install page), `NewIssueURL(Post)` cut to the byte budget with "full text: `<local path>`" (F16). The target repository is `updatecheck.Repo`, passed with `-R`. **Tests:** `TestPostStripsIdentity`, `TestPostReRedacts`, `TestInstallHintByPlatform`, `TestNewIssueURLCutsLongBody`, `TestGHNotInstalledAndNotLoggedIn`. **Command:** `go test ./internal/issues/ -run 'TestPost|TestInstallHint|TestNewIssueURL|TestGH' -count=1`. **Depends on:** T-IS1, T-IS2. **Commit:** `issues: the public post, the gh runner and the fallback link`. **Red step:** `TestPostStripsIdentity` fails while the builder copies the header. **Reviewer tier:** `opus + mandatory independent review`; mutation check: add the reporter to the post and the test must fail.

### T-IS7: `fugaro issue submit`
- **Files:** `internal/cli/issue_submit.go` (new), `internal/cli/issue_submit_test.go` (new), `internal/cli/issue.go` (registers it). **Interfaces:** consumed T-IS5, T-IS6; `submit N` prints the preview and files nothing; `submit N --yes` runs `gh issue create`, records the URL and sets `submitted`; `submit` with no number lists the pending issues and, with `--yes`, files each, skipping `submitted` and `dismissed`; without `gh` or a login it prints the install or `gh auth login` line and the new-issue link, files nothing, exits non-zero; reads no stdin. **Tests:** `TestSubmitPreviewFilesNothing`, `TestSubmitYesFilesAndRecords`, `TestSubmitAllSkipsSubmittedAndDismissed`, `TestSubmitWithoutGhPrintsFallback`, `TestSubmitNotLoggedInPrintsLogin`, `TestSubmitReadsNoStdin`. **Command:** `go test ./internal/cli/ -run 'TestSubmit' -count=1`. **Depends on:** T-IS5, T-IS6. **Commit:** `cli: fugaro issue submit with a preview and the gh fallback`. **Red step:** `TestSubmitAllSkipsSubmittedAndDismissed` fails with a runner spy that records a call for every issue. **Reviewer tier:** `opus + mandatory independent review` (it sends text to a public repository).

### T-IS8: the recording seam
- **Files:** `internal/issues/record.go` (new), `internal/issues/record_test.go` (new). **Interfaces:** consumed T-IS1, T-IS2, T-IS3; produced `Record(v Verdict, run RunMeta, secrets) (Saved, error)`: `local` prints nothing and writes nothing and returns the fix steps; `fugaro` and `unclear` redact, save and return the line `Saved issue #0007. Run 'fugaro issue submit 7' to file it on GitHub.` (the local ID before an upload, the number after). T-DG8's output calls it once per verdict (an amendment to that task, T1). **Tests:** `TestRecordLocalWritesNothing`, `TestRecordFugaroAndUnclearSaveRedacted`, `TestRecordLineNamesTheSubmitCommand`, `TestRecordNeverSubmits`. **Command:** `go test ./internal/issues/ -run 'TestRecord' -count=1`. **Depends on:** T-IS1, T-IS2, T-IS3. **Commit:** `issues: record a verdict as a saved issue`. **Red step:** `TestRecordLocalWritesNothing` fails against a store spy while the function saves every class. **Reviewer tier:** standard.

### T-IS13: the `doctor` line and the `setup` hint
- **Files:** `internal/cli/doctor_issues.go` (new), `internal/cli/doctor_issues_test.go` (new), `internal/cli/doctor.go` (calls it), `internal/cli/setup.go` (one printed line recommending `gh` and `gh auth login`, never required). **Interfaces:** consumed the store (T-IS1); one line only when issues are unsynced or pending (the "doctor is one screen" rule). **Tests:** `TestDoctorIssuesLineOnlyWhenUnsynced`, `TestSetupPrintsGhHintOnce`. **Command:** `go test ./internal/cli/ -run 'TestDoctorIssues|TestSetupPrintsGhHint' -count=1`. **Depends on:** T-IS1. **Commit:** `cli: a doctor line for unsynced issues and a gh hint in setup`. **Red step:** both fail until the line is printed. **Reviewer tier:** standard.

## Slice 2: the shared store

### T-IS10: the shared store (SECURITY-SENSITIVE)
- **Files:** `internal/issues/shared.go` (new), `internal/issues/shared_test.go` (new). **Interfaces:** consumed `internal/blobx`, T-IS1; keys under `fugaro/issues/`: the record, a counter taken with a `ReplaceIf` loop, the fingerprint index created with `Create`, occurrence objects, and the claim object created with `Create` carrying holder and time with a 15-minute lease after which it can be taken (F9); produced `Upload`, `ListShared`, `Claim`, `Release`, `RecordSubmitted`, `AttachOccurrence`. **Tests (fake bucket):** `TestConcurrentUploadsGetDistinctNumbers`, `TestFingerprintMatchAttachesOccurrence`, `TestClaimIsExclusive`, `TestClaimExpiresAfterLease`, `TestSubmittedIsFinal`, `TestUploadReadBackIsRedactedAtSubmit`. **Command:** `go test ./internal/issues/ -run 'TestConcurrent|TestFingerprintMatch|TestClaim|TestSubmittedIsFinal' -count=1`. **Depends on:** T-IS1. **Commit:** `issues: the shared store, numbering and the claim`. **Red step:** the distinct-numbers test fails with a counter that reads then writes without the generation match. **Reviewer tier:** `opus + mandatory independent review`; mutation check: remove the generation precondition and `TestConcurrentUploadsGetDistinctNumbers` must fail.

### T-IS10b: CONDITIONAL, close the job's write to `fugaro/issues/` (only if Check 452 finds it writable)
- **Files:** to be named by the task once the check reports (`deploy/terraform/gcp/modules/installation/*`, `internal/infra/**`, a converge test); `.claude/rules/terraform.md` applies. **What:** an IAM condition so a job's service account cannot write the prefix, shipped by `fugaro update` (the hard rule), with a test that an installation at the previous version converges. **Depends on:** T-IS10 and Check 452. **Reviewer tier:** `opus + mandatory independent review`. **Not drafted until the owner rules on O1.**

### T-IS11: `issue sync`, the list of the whole team, and the end-of-troubleshoot sync
- **Files:** `internal/cli/issue_sync.go` (new), `internal/cli/issue_sync_test.go` (new), `internal/cli/issue.go` (registers it), `internal/cli/issue_list.go` (merges the shared issues), `internal/issues/sync.go` (new), `internal/issues/sync_test.go` (new). **Interfaces:** consumed T-IS5, T-IS10, `internal/agentenv`; `SyncPending` uploads each unsynced issue (number, rename, duplicates attach as an occurrence), marks it uploaded, and is called by T-DG8's output after `Record` and by `list`, `show`, `submit`; a failed upload leaves the issue local and prints one line; in a coding agent's session upload refuses with the command a person runs, and retries nothing (F10). **Tests:** `TestSyncUploadsPendingAndRenames`, `TestSyncOfflineKeepsLocal`, `TestSyncRefusesInAgentSession`, `TestListShowsWholeTeamsIssues`, `TestSyncRunsOnlyInIssueCommands`. **Command:** `go test ./internal/issues/ ./internal/cli/ -run 'TestSync|TestListShowsWholeTeams' -count=1`. **Depends on:** T-IS7 (the same `issue.go` registration), T-IS10. **Commit:** `cli: issue sync and the whole team's list`. **Red step:** `TestSyncRefusesInAgentSession` fails while the seam is ignored; mutation check on the `agentenv` guard. **Reviewer tier:** `opus + mandatory independent review` (a cloud write path).

### T-IS12: `submit` claims first
- **Files:** `internal/cli/issue_submit.go`, `internal/cli/issue_submit_test.go`. **Interfaces:** consumed T-IS7, T-IS10; `submit --yes` claims the shared issue before `gh`, refuses with the claimer and time when another holds a live claim, records the URL and `submitted` in the shared store, releases the claim when `gh` fails, and refuses in an agent session. **Tests:** `TestSubmitRefusesWhenClaimedByAnother`, `TestSubmitReleasesClaimOnGhFailure`, `TestSubmitRecordsURLInSharedStore`, `TestSubmitRefusesInAgentSession`. **Command:** `go test ./internal/cli/ -run 'TestSubmit(Refuses|Releases|Records)' -count=1`. **Depends on:** T-IS7, T-IS10. **Commit:** `cli: submit claims the issue in the shared store first`. **Red step:** the first test fails while two fake users both reach `gh`. **Reviewer tier:** `opus + mandatory independent review`.

## Slice 3: the daily report

### T-IS14: the digest's Issues section
- **Files:** `internal/digest/issues.go` (new), `internal/digest/issues_test.go` (new), `internal/digest/testdata/` (a golden, new). **Interfaces:** produced `Issues` input in the closed vocabulary (number, class, status, created, occurrences) and `Thresholds`; sections **New** (last 24 hours before the period's end date), **Pending** (age against that date, oldest first, each with `fugaro issue submit N`), **Recurring** (sorted by occurrences, merged ones counted), a firmer wording at the thresholds, and the single line "No pending issues." when none. No summary text, no clock (D1721, G1, G3). **Tests:** `TestIssuesSectionNoSummaryText`, `TestIssuesSectionByteIdentical`, `TestIssuesSectionNudgeFirmer`, `TestIssuesSectionEmptyIsOneLine`, `TestIssuesSectionFitsCap`. **Command:** `go test ./internal/digest/ -run 'TestIssuesSection' -count=1`. **Depends on:** nothing. **Commit:** `digest: an Issues section in the closed vocabulary`. **Red step:** the no-summary test fails when a planted summary reaches the output. **Reviewer tier:** standard.

### T-IS15: the nudge thresholds as a setting
- **Files:** `internal/settings/registry.go`, `internal/settings/registry_test.go`, `schemas/` (the file the registry names for a user-level key; the task locates it), `internal/localcfg/localcfg.go` and its test if the key lives in the user config. **Interfaces:** produced `issues.nudge_after_days` and `issues.nudge_recurrence` with documented defaults, strict decoding. **Tests:** `TestIssuesNudgeKeysDecodeStrictly`, `TestSettingsRegistryHasIssuesKeys`. **Command:** `go test ./internal/settings/ ./internal/localcfg/ -run 'TestIssuesNudge|TestSettingsRegistryHasIssuesKeys' -count=1`. **Depends on:** nothing. **Commit:** `settings: thresholds for the issue nudge`. **Red step:** the registry test fails on a missing key. **Reviewer tier:** standard.

### T-IS16: feed the digest from the shared issues
- **Files:** `internal/cli/runs_digest.go`, `internal/cli/runs_digest_test.go`. **Interfaces:** consumed T-IS10 (`ListShared`), T-IS14, T-IS15; reads the shared issues when the digest already opens the project's bucket; a read failure omits the section and says so on stderr (the report is never blocked). **Tests:** `TestRunsDigestIncludesIssuesSection`, `TestRunsDigestOmitsSectionOnReadFailure`. **Command:** `go test ./internal/cli/ -run 'TestRunsDigest(Includes|Omits)' -count=1`. **Depends on:** T-IS10, T-IS14, T-IS15. **Commit:** `cli: the digest reads the shared issues`. **Red step:** the first test fails without the feed. **Reviewer tier:** standard.

### T-IS9: docs, plugin and release notes
- **Files:** `docs/issues.md` (new), `plugin/skills/working/SKILL.md` (one paragraph; lint), `docs/releases/` (the next release's notes: the new command, `### For operators`: "run `fugaro update`" only if T-IS10b ships), `internal/cli/docs_issue_test.go` (new). **Tests:** `TestDocsIssueNamesEveryCommand` and the plugin command-and-flag check. **Command:** `go test ./internal/cli/ -run 'TestDocsIssue' -count=1` and `go test ./plugin/... -count=1`. **Depends on:** T-IS7, T-IS11, T-IS12, T-IS16. **Commit:** `docs: fugaro issue`. **Red step:** the docs test fails on a missing page. **Reviewer tier:** standard (the plugin skill is a prompt-class path: `opus` if the router says so by path). Shares `plugin/skills/working/SKILL.md` with T-DG8 (T1).

## Parallel execution

**Edges (from "Depends on"):** T-IS4 ← T-IS3; T-IS5 ← T-IS1; T-IS6 ← T-IS1, T-IS2; T-IS7 ← T-IS5, T-IS6; T-IS8 ← T-IS1, T-IS2, T-IS3; T-IS10 ← T-IS1; T-IS10b ← T-IS10 + Check 452; T-IS11 ← T-IS7, T-IS10; T-IS12 ← T-IS7, T-IS10; T-IS13 ← T-IS1; T-IS16 ← T-IS10, T-IS14, T-IS15; T-IS9 ← T-IS7, T-IS11, T-IS12, T-IS16.

| Level | Tasks (run concurrently) |
| --- | --- |
| 0 | T-IS1, T-IS2, T-IS3, T-IS14, T-IS15 |
| 1 | T-IS4, T-IS5, T-IS6, T-IS8, T-IS10, T-IS13 |
| 2 | T-IS7, T-IS16, (T-IS10b if ruled) |
| 3 | T-IS11, T-IS12 |
| 4 | T-IS9 |

**Critical path:** T-IS1 → T-IS5 → T-IS7 → T-IS11 → T-IS9 (five levels). T-IS7 follows T-IS5 and T-IS6 because it needs the commands and the post builder; T-IS11 follows T-IS7 only because both register on `issue.go`, and T-IS12 follows T-IS7 because it edits `issue_submit.go`.

**Shared-files table (no level holds two writers of a file):**

| File | Writers (level) |
| --- | --- |
| `internal/cli/issue.go` | T-IS5 (1), T-IS7 (2), T-IS11 (3) |
| `internal/cli/issue_list.go` | T-IS5 (1), T-IS11 (3) |
| `internal/cli/issue_submit.go`, `issue_submit_test.go` | T-IS7 (2), T-IS12 (3) |
| `internal/cli/root.go` | T-IS5 (1) |
| `internal/cli/doctor.go`, `internal/cli/setup.go` | T-IS13 (1) |
| `internal/cli/runs_digest.go` | T-IS16 (2) |
| `internal/diagnoseagent/prompt.go`, `guide.md`, `docs/troubleshooting-for-agents.md` | T-IS4 (1) |
| `plugin/skills/working/SKILL.md` | T-IS9 (4) |
| every `internal/issues/*.go` file | one task each (the lists above) except none shared |

**Outside this plan (T1):** T-DG8 of `docs/diagnose-agent/` also edits `internal/cli/root.go`, `docs/troubleshooting-for-agents.md`, `plugin/skills/working/SKILL.md`; T-IS4 and T-IS9 and T-IS5 must not run in the same level as T-DG8, and whichever lands second rebases. T-DG8's output also gains the call to `Record` and `SyncPending` (T-IS8, T-IS11); that edit belongs to T-DG8's PR, or to a small follow-up if T-DG8 lands first.

## Checks (reserved Checks 450–459; all OWNER-RUN)

- **Check 450, numbering across machines.** Two machines on one project each run `fugaro issue sync` with a pending issue at the same moment; the owner types `fugaro issue list` on both and confirms two distinct numbers and one shared list. Measures F9's counter on the real bucket (also U2).
- **Check 451, one filing.** Two shells run `fugaro issue submit N --yes` on the same pending issue within seconds; exactly one GitHub issue exists afterwards (the owner closes it) and the other shell prints the claimer. Measures the claim.
- **Check 452, the job's write access.** The owner (not an agent) attempts a write to `fugaro/issues/probe` as a job's service account and records whether it is refused; the result decides T-IS10b (U1, O1).
- **Check 453, the fallback.** On a machine with no `gh`, and on one with `gh` not logged in, `fugaro issue submit N --yes` prints the install command or `gh auth login` and a new-issue link whose body fits; the owner opens the link and sees the title and body filled in (U3, U4).

## Considered and declined

- **Syncing on every command (spec 2).** Declined, D1725.
- **Summaries in the daily report.** Declined, D1721; `issue list` carries them.
- **A model that matches duplicates.** Declined, D1724.
- **A `fugaro issue add`.** Not in the spec; open item O2.
