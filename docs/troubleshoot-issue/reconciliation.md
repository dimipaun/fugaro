# Troubleshoot findings to `fugaro issue`: reconciliation

**Status: reconciliation of [spec.md](spec.md) against `origin/main` (commit `d3352fad2`, read 2026-11-08). Where the two disagree this file wins.** The spec is the owner's (Dimi Paun). Nothing here is implemented and no task is approved. Every cited path and symbol was grepped on `origin/main`; what was not checked is in section 4.

**Numbers.** This work reserves **D1720–D1749** and **Checks 450–459** (verified above every `D` and `Check` number on `origin/main` and in the open pull requests' docs on 2026-11-08: the highest seen were D1710 and Check 441). The sibling this spec builds on is `docs/diagnose-agent/` (D1600–D1619, Checks 410–419).

## What the spec's concepts already have

| Spec concept | What the repo has |
| --- | --- |
| `fugaro troubleshoot` | **Not built.** Named by `docs/diagnose-agent/` (D1604; `fugaro diagnose RUN` keeps explaining a cloud run, D1600). Written so far: the credential resolver (`internal/diagnoseagent/creds.go`), the pattern redactor (`internal/redact`), the embedded guide and prompt (`internal/diagnoseagent/guide.go`, `prompt.go`). The command is that plan's T-DG8 |
| Redaction before anything is written | `internal/redact` (patterns) and `internal/agent/redact.go` (exact values, `RedactFunc`); the diagnose plan's T-DG2/T-DG6 apply both to every evidence file |
| Filing on GitHub with `gh issue create` | `runs digest --submit-draft` prints `gh issue create --web -R <updatecheck.Repo>` and sends nothing (`internal/cli/runs_digest_submit.go`, D327); `updatecheck.Repo` is `dimipaun/fugaro`, **a public repository** |
| Project-level shared storage next to recipes | The runs bucket, key `fugaro/recipes/NAME.yaml` (`internal/cli/recipes.go`); `fugaro/project.json` (`deploy/terraform/gcp/modules/installation/bucket.tf`). Atomic writes: `internal/blobx` (`Create` is create-if-absent, `ReplaceIf` and `DeleteIf` are generation-matched) |
| A local folder | `localcfg.UserDir(getenv, goos, kind)`, kinds `config`, `cache`, `state` (`internal/localcfg/platformdirs.go`) |
| "The daily report on costs and job activity" | `fugaro runs digest` (`internal/digest`, `internal/cli/runs_digest.go`): MIT, committed Markdown, closed vocabulary, deterministic, at most 8 KiB, with an extra-section hook (`internal/digest/section.go`). `fugaro report` is a different, paid command (`ee/report/cli/report.go`) |
| A name that is "already taken": reports | The committed digest reports under `runs digest --dir`, and `fugaro report`. The folder is `issues` |
| Coding-agent session detection | `internal/agentenv` (`Marker`, `Present`, `Session`) |

## 1. Findings

**F1. Wrong (the command does not exist).** The spec starts from `fugaro troubleshoot`. It is the agent command of `docs/diagnose-agent/`, plan only apart from three parts; its verdict has two outcomes (your configuration, or a Fugaro bug with a report "ready to file"). *Consequence:* the issue store and `fugaro issue` are built on their own, fed by a verdict type this plan defines (three classes plus a signature, D1726's slice order), and one hook in T-DG8's output calls the recorder (T-IS8). No second troubleshoot is built.

**F2. Rule (the confirmation prompts).** The spec's `submit` "asks for confirmation" and the no-number `submit` "asks before filing each one". `docs/no-prompt-policy/` (and the owner's rule that terminal runs ask nothing) says no command waits for input; `internal/agentenv` marks a coding agent's session, which confirms nothing. *Consequence:* D1720: preview, and `--yes` files.

**F3. Rule (D327: a learning is never filed by the command).** `runs digest --submit-draft` deliberately sends nothing; its comment says "a learning is never filed by the command, by a run or by an agent". The spec's `submit` runs `gh issue create` itself. *Consequence:* this is a different artifact (a bug report the user asks to file, typed by the user with `--yes`), so D327 stands for learnings and is cited in the README as untouched; `troubleshoot` itself still never submits, and a coding agent's session never files (F13).

**F4. Gap (the issue leaves a private project for a public repository).** The spec's header carries reporter, task ID and OS; the body carries log excerpts. The target is public. The digest's own rule (G1) is that a report that can leave the repository carries no repository name and no run ID. *Consequence:* D1722: the shared store keeps the whole issue; the GitHub post is built from a stricter projection and re-redacted at submit, and the preview shows exactly that text.

**F5. Gap (redaction is best-effort and the second reader is a different person).** `internal/agent/redact.go` states its output is "best-effort redacted, not guaranteed clean"; pattern redaction is new (`internal/redact`). In this spec a teammate's saved text is later posted by someone else. *Consequence:* redact on write (as the spec says), and again **at submit**, by the submitter's own binary, over the text it is about to post; plus emails, home-directory paths and the project and repository names (scrubbed always, not "if possible"). Tests with sample secrets built from fragments (rule: no secret-shaped literal).

**F6. Gap (a verdict from untrusted text).** The classification comes from an agent that read log text the user does not control (the diagnose plan's F5). A hostile log can try to steer the verdict to `fugaro` (noise) or to `local` (hide a real bug). *Consequence:* the agent is read-only (D1602 there) and the CLI, not the agent, writes the file from a strictly decoded verdict; `local` is structurally unsaveable (a type with no persistence path, T-IS3); a steered `fugaro` costs one pending entry the user can dismiss, and nothing is ever filed without `submit --yes`.

**F7. Gap ("task ID" and the file name).** A troubleshoot runs over a local command log whose ID is the log stem (`<command>-<14-digit UTC>-<pid>`, D1609 of the diagnose plan) or a cloud ref `<repo-slug>/<run-id>`; neither is a "task ID", and the project number exists only after upload. *Consequence:* D1723.

**F8. Gap (the duplicate test is undefined).** "If one matches" has no meaning without a key. A model comparing text would put teammates' issue text into a prompt. *Consequence:* D1724: a fingerprint.

**F9. Gap (numbering and the claim need atomic writes, and a claim can be stranded).** `blobx.Create` is create-if-absent and `ReplaceIf` is generation-matched, so a counter and a claim are expressible. A claim whose holder crashes before `gh` finishes would block the issue forever. *Consequence:* the claim carries a lease (15 minutes, a constant) after which anyone may take it; the number is taken with a counter `ReplaceIf` loop and a lost race retries; a submitted record is final (T-IS10). GitHub-side duplicate search before creating is not attempted (cost; the claim covers two teammates).

**F10. Rule (a coding agent's session must not write to the cloud).** `CLAUDE.md`: the update skill "never works around the refusal of cloud writes in a coding agent's session", and the no-prompt plan's managed gate refuses writes from `agentenv`. Upload, claim and counter writes are cloud writes. *Consequence:* in a session `agentenv.Present` reports, the issue is saved locally, upload and `submit --yes` refuse with a line naming the command a person runs, and nothing retries. Pinned by T-IS11 and T-IS12.

**F11. Gap (who can write the shared prefix).** Team members can write the runs bucket (they publish recipes there). Whether a job's service account can write `fugaro/issues/` is **not verified**. A hostile agent run that forges an issue would reach a teammate's `submit`. *Consequence:* the post is rebuilt and re-redacted at submit (F5), `submit` shows the preview first, and Check 452 measures the job's write access; if it can write, T-IS10b closes it in Terraform behind `fugaro update` (the hard rule: no operator step).

**F12. Gap (sync "on the next command").** A cloud call on every command, offline and in an agent session. *Consequence:* D1725.

**F13. Gap (state is split between a local file and the shared store).** The spec's statuses live in both. *Consequence:* the shared store is the truth once uploaded (`pending`, `submitted`, `dismissed` plus the URL); the local copy refreshes on `issue list|show|sync`; a dismissal is shared too (it is a team's issue, spec 4b), and `list` shows the whole team's.

**F14. Gap (the daily report is the digest, and the digest forbids what the section wants).** The digest renders no free text of a record (G1) and reads no clock (G3). The spec's section wants a one-line summary, "how long waiting" and reporter-agnostic recurrence. *Consequence:* D1721: counts, numbers, classification, ages computed from stored times against the period's end date (the digest's own date, not a clock), recurrence, and the submit command; summaries only in `issue list`. The thresholds the spec wants in config are a settings key (T-IS15).

**F15. Exists (the install hints).** No `brew install gh` or `winget` string exists in `internal/`. It is built here, with the platform and `/etc/os-release` read through an injected seam, and a link to `https://cli.github.com` for an unknown distro.

**F16. Gap (the new-issue URL has a length limit).** The fallback link carries title and body in the query; browsers and GitHub refuse long URLs. *Consequence:* the body is cut to a fixed byte budget (a constant, 6 KiB after encoding), ends with "full text: `<local file path>`", and the preview says it was cut.

**F17. Rule (every command family touches docs, plugin and console).** `plugin/` tests check every command and flag a skill names against the CLI; many `internal/cli/docs_*_test.go` pin docs; the console reads `--json` output. *Consequence:* T-IS9 and a `--json` for `list` and `show` with the format's version (a field never changes meaning).

**F18. Gap (setup "recommends" `gh`).** `internal/cli/setup.go` is the admin's command and the no-prompt rule forbids a question. *Consequence:* one printed line at the end of `setup`, and a `doctor` line (T-IS13); never required.

## 2. Decisions

All answered by the owner on 2026-11-08 through `AskUserQuestion`; every answer is the recommended option.

| # | Question | Recommendation and reason | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1720** | `submit` confirmation against no-prompt (F2) | `submit N` prints the exact post and files nothing; `--yes` files. No-number `submit` lists the pending issues and, with `--yes`, files each; it skips `submitted` and `dismissed`. No stdin read | Prompt on a terminal: breaks no-prompt, hangs in an agent or CI | **Preview + `--yes`, 2026-11-08 (recommended): keeps the no-prompt rule.** |
| **D1721** | Where the Issues section goes (F14) | The digest, closed vocabulary: counts by class, issue numbers, ages, recurrence, the submit command, "No pending issues." when none. No summary text | Digest with summaries (breaks G1); paid report only (not the daily committed one) | **Digest, closed vocabulary, 2026-11-08 (recommended): summaries stay in `issue list`/`show`.** |
| **D1722** | What leaves the machine on GitHub (F4, F5) | The shared store keeps reporter and run reference; the post carries version, OS, class, summary, diagnosis, steps and a short re-redacted log tail, with no reporter, repository, project or path | Post everything: identity and paths become public | **Strip identity, re-redact, 2026-11-08 (recommended): the repository is public.** |
| **D1723** | File naming (F7) | `local-<run-stem>.md` until uploaded; then `<NNNN>-<run-stem>.md`, the local ID kept in the header | Never rename: files don't sort by number | **Run stem, renamed on upload, 2026-11-08 (recommended).** |
| **D1724** | Duplicate matching (F8) | The agent returns a normalized signature (failing command and error class); the CLI hashes it; an exact match attaches an occurrence (reporter, time, redacted excerpt). Similar issues with other signatures stay separate and the human merges at submit | A model compares summaries: spend, nondeterminism, teammates' text becomes prompt input | **Fingerprint, 2026-11-08 (recommended).** |
| **D1725** | When sync runs (F12) | At the end of `troubleshoot` and in `issue list`, `show`, `submit`, `sync`; one `doctor` line for the unsynced count; never on other commands | Every command: a cloud call everywhere | **Issue commands + troubleshoot, 2026-11-08 (recommended).** |
| **D1726** | Slicing | Slice 1 local only (classify, redact, files, list/show/dismiss, `submit` with the `gh` fallback); slice 2 the shared store; slice 3 the digest section | One slice: a large first review, nothing useful until the cloud half is right | **Local first, 2026-11-08 (recommended).** |

Spec owner recorded: Dimi Paun (asked, answered 2026-11-08).

Resolutions that were not questions (one defensible answer): the lease and URL budget constants (F9, F16); the verdict's `local` class is unsaveable by type (F6); agent sessions never upload or file (F10); the store folder is `issues` under `UserDir(..., state)` and the shared prefix is `fugaro/issues/` (F5, F11).

## 3. Open tensions and open items for the owner

- **O1. Check 452 may force a Terraform change.** If a job's service account can write `fugaro/issues/`, T-IS10b (an IAM condition shipped by `fugaro update`, with a converge test) becomes necessary; the owner runs the check and rules.
- **T1. This plan edits files the diagnose plan's T-DG8 also edits** (`internal/cli/root.go`, `docs/troubleshooting-for-agents.md`, `internal/diagnoseagent/guide.md`). Not a contradiction of an answer; the two must not run in one level (shared-files table) and the second to land rebases.
- **T2. D1721 against the spec's "one-line summary" in the report.** Followed as the owner answered (summaries stay in `list`); the spec text is amended.
- **O2. The `fugaro issue add` question.** No command creates an issue by hand; the spec has none. Not added (no new functionality); say if wanted.

## 4. UNVERIFIED

- **U1.** Whether a job's service account can write `fugaro/issues/` in the runs bucket (Terraform's bucket IAM was not read; Check 452).
- **U2.** That a team member's gcloud login can write and generation-match the runs bucket under `fugaro/` on every installation (recipes publish proves writes; the counter's `ReplaceIf` was not exercised by a person).
- **U3.** The exact URL length GitHub accepts for the new-issue link; the budget in F16 is conservative, not measured.
- **U4.** The `/etc/os-release` ID values the Linux hint branches on (apt for debian-like, dnf for fedora-like).
- **U5.** The settings key's validation shape for the thresholds (`internal/settings/registry.go` read, its tests not).
