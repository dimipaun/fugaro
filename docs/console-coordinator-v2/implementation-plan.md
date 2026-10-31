# Implementation plan

**Status: design approved 2026-10-31, every decision answered as recommended; the CLI tasks C0-C4 queue for 0.15.0 and the console tasks K1-K5 for 0.117.0; the INPUT BOX of the terminal waits for the coordinator front door (P7/P8) and its own independent security review; the read-only transcript pane ships first (D827).** The owner's answers to
[reconciliation.md](reconciliation.md)'s decisions are turned into tasks here without a second design
pass. The CLI tasks would ship in **0.15.0** (the release after `v0.14.0`) and the console tasks in
**0.117.0** (after `console-v0.116.0`). Every task is droppable on its own: none of them is a
prerequisite for the others except where "Depends on" says so.

Three conventions this plan keeps:

- **A red step first.** Every task opens with a test that **fails** on `main` as it stands. A task
  whose red step cannot be written is a task that changes nothing, and is not in this plan.
- **At most 12 files a task.** A task that needs more is two tasks.
- **Security-sensitive is a label, not a mood.** A task that adds or moves an entry on
  `SPAWN_COMMANDS` (`console/src/main/spawn.ts`) or `CONSOLE_OPERATIONS` (`console/src/main/ipc.ts`)
  needs an independent review, its pinning test in `console/test/unit/`, and the §5.1 table of
  `docs/design/desktop-console.md` updated in the same change (`.claude/rules/console.md`).

**No release-note migration is needed for any of this.** Nothing here changes a policy version, a
rules version, a node shape or an image contract, so `fugaro update` has nothing to converge and
`docs/releases/` gains no `### For operators` step. The one field added to a wire shape (C1) is
**additive**, which the repo's own rule allows without a format version bump.

---

## 1. The CLI side (0.15.0)

### C0. Profile the reads this page turns on

**Files.** `docs/console-coordinator-v2/spec.md` (the `(unmeasured)` cells), and nothing else.

**What it does.** Runs each of the page's reads against a real installation and records what it
costs: the byte size and the wall-clock time of `fugaro runs ls --json --since 24h` at 24 h, 7 d and
`0`; of `fugaro coordinator status --from-store --all --json` at several repository counts; of
`fugaro plan status --json --all`; of `fugaro budget show --json`; and of the same `runs ls` argv
with `--limit 50`. It replaces every `(unmeasured)` in [spec.md](spec.md) with the reading, and
where a reading contradicts a figure already in that table, it corrects the table.

**Interfaces.** None. This task changes prose and no code.

**Red step.** `gofmt -l .` prints nothing and `git diff --stat` shows one file; the "red" half is
that [spec.md](spec.md) contains `(unmeasured)` on `main`, which this task removes. There is no
test: a measurement is not a behaviour.

**Command.** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1 -timeout 40m` (the docs tests
that read `docs/`), then the reads themselves against the owner's own installation.

**Depends on.** Nothing. It needs no owner answer, and its numbers are what O1
and O2 in [reconciliation.md](reconciliation.md) are waiting for.

**Review tier.** Ordinary.

---

### C1. The repository's provider on the stored view

**Answers D814.** The console cannot build a pull request URL without knowing which host the
repository is on, and no document it reads carries it (reconciliation F1).

**Files.**

1. `ee/coordinator/state.go` — `RepoView` gains `Provider string \`json:"provider,omitempty"\``.
2. `ee/coordinator/state.go` — `ReadRepoView` keeps taking the caller's spelling; the provider is set
   by the caller, so the signature stays `(ctx, slug, repo)` and the caller assigns the field.
3. `ee/coordinator/cli/coordinator_view.go` — `coordinatorStoredView` fills it from
   `env.RepoProvider(repo, fcli.CheckoutOf(ctx, repo)())`, the same call `coordinatorViewOf` already
   makes.
4. `ee/coordinator/cli/coordinator.go` — the human table gains no column; the field is for readers.
5. `console/src/shared/schema.ts` — `COORDINATOR_REPO_KEYS` gains `provider`; `CoordinatorRepoView`
   gains `readonly provider: string`.
6. `console/test/unit/schema.test.ts` — the decoder's row for the new key.
7. `ee/coordinator/cli/console_contract_test.go` — the Go side of the key-list pin, if it asserts the
   list in that direction.
8. `console/docs/json-contract.md` §6.13 — the new key's row in the `repos` table.

**Interfaces.** `coordinator.RepoView.Provider` is one of `config.Providers`' two words (`github`,
`bitbucket`) or **`""`** — and `""` is a real answer, not a gap: a machine whose config records no
provider for the repository and has no checkout of it has none to give
(`cloudEnv.repoProvider`, `internal/cli/project_job.go`). **The console draws no link for `""`** and
shows the number as text.

**Red step.** A test in `ee/coordinator/cli/coordinator_view_test.go`: a stored view read for a
repository whose config records `provider: github` answers `"provider": "github"` in the JSON. It
fails today — the key is absent. Its other half: a repository whose config records no provider and
whose checkout is unreadable answers `"provider": ""` **and the key is present**, because a reader
must be able to tell "no provider" from "an older CLI".

**Command.** `GOFLAGS=-p=2 go test ./ee/coordinator/... ./internal/cli -run 'TestCoordinator|TestDocs' -count=1 -timeout 40m`, then `gofmt -l .` and `go vet ./...`.

**Depends on.** Nothing. It is the first task on the critical path for K2.

**Review tier.** Ordinary — but it is a **wire-shape change to a document the console decodes
strictly**, so the Go key-list test and the console's `COORDINATOR_REPO_KEYS` move in the same
change or the console refuses the whole document.

---

### C2. `runs ls` learns to print a page

**Answers D818.** Today the command prints every row it read and has no cursor (reconciliation F5).

**Files.**

1. `internal/cli/runs.go` — `newLsCmd` gains `--limit N` (default `0`, meaning every row) and
   `--after <run-id>`; the two are refused together with `--watch`.
2. `internal/cli/runs.go` — `runLs` applies the limit after the sort and the cursor before it.
3. `internal/cli/runs_test.go` — the red step below.
4. `internal/cli/runs.go`'s command `Long` text — the two flags' sentences, which the CLI's own docs
   shape pins.
5. `console/docs/json-contract.md` §5 — the `--limit`/`--after` argv and the promise that `totals`
   is the window's.
6. `console/src/main/spawn.ts` — the `runs.list` row's two new declared arguments.
7. `console/src/main/ipc.ts` — the operation's schema for the same two.
8. `console/test/unit/ipc.test.ts`, `console/test/unit/spawn.test.ts` — the pinning tests.
9. `docs/design/desktop-console.md` §5.1 — the allow-list table's `runs ls` row.

**Interfaces.**

- The listing's order becomes **`created` descending, then `run` descending** — stated, because the
  cursor is defined by it.
- `--after <run-id>` returns the rows **strictly after** that run **in that order**. An id the
  window does not hold is a **refusal**, not an empty page: a cursor that silently returns nothing
  reads as "no more runs", which is the one wrong answer a page can give.
- **`totals` is the whole window's, never the page's.** A limit shortens what is printed and nothing
  else. This is the promise [spec.md](spec.md) §2.6 makes on the console's behalf, and it is C2's
  burden to keep.

**Red step.** `internal/cli/runs_test.go`, four cases, all failing today: `--limit 2` prints two rows
and a `totals` over all five; `--after <third row's id>` prints the next two; `--after <an id outside
the window>` exits 1 with a sentence naming the id; `--limit 0` prints every row, so the default is
unchanged.

**Command.** `GOFLAGS=-p=2 go test ./internal/cli -run 'TestRunsLs|TestDocs' -count=1 -timeout 40m`.

**Depends on.** Nothing. It is on the critical path for K4.

**Review tier.** **SECURITY-SENSITIVE** — two new argv words on a row the console may spawn. The
pull request body must say which words were added, that `--after`'s value is the run id the CLI
itself printed and is validated by the row's own alphabet (`ARGUMENT_RE`,
`console/src/main/spawn.ts`), and that no other `runs ls` flag became reachable.

---

### C3. `coordinator show`: one pull request, as the host has it

**Answers D816.** The stored view carries no body (reconciliation F3), and the console may not hold
a credential, so the read has to be a CLI one.

**Files.**

1. `ee/coordinator/cli/coordinator.go` — `NewCoordinatorCmd` gains `show`; a new
   `newCoordinatorShowCmd` with `--repo`, `--pr` and `--json`.
2. `ee/coordinator/cli/coordinator_view.go` — the read, reusing `coordinatorReader` (the coordinator's
   own read-only installation token) rather than inventing a second credential path.
3. `ee/coordinator/cli/coordinator_show.go` (new) — the document: `coordinator.PRDocument`, a new small
   type in `ee/coordinator/view.go`.
4. `ee/coordinator/view.go` — the document type, so a second transport cannot invent a shape.
5. `ee/coordinator/cli/coordinator_test.go` — the red step.
6. `ee/coordinator/cli/testdata/` — the fixture, in the shape the existing coordinator fixtures use.
7. `console/docs/json-contract.md` — a new §6.15 for the document.
8. `console/src/main/spawn.ts` — the new `coordinator.show` row.
9. `console/src/main/ipc.ts` — the new operation.
10. `console/src/shared/schema.ts` — the document's decoder.
11. `console/test/unit/ipc.test.ts` — the pinning test.
12. `docs/design/desktop-console.md` §5.1 — the allow-list table's new row.

**Interfaces.** The argv the console spawns is exactly
`fugaro coordinator show --repo <owner/name> --pr <n> --json`. The document is `{ number, url, title,
body, state, draft, branch, base, author, updated }` — every field the host answered, `body` included
as **untrusted text**, and no field invented. A pull request the host does not have is a refusal with
the host's own status, not an empty document.

**Red step.** A fixture-driven test in `ee/coordinator/cli/coordinator_test.go`: `coordinator show
--repo owner/repo-a --pr 1458 --json` prints the fixture's body, and the same command under
`FUGARO_CREDENTIALS_SERVICE_URL` **refuses with a sentence naming the credentials service** — the
second half is the one that matters, because it is the property reconciliation F9 says the read
inherits and the one a later change could silently lose.

**Command.** `GOFLAGS=-p=2 go test ./ee/coordinator/... -count=1 -timeout 40m`, plus the docs run
above.

**Depends on.** Nothing, but it is the task that most wants C0's numbers first: [reconciliation.md](reconciliation.md)
O2 (the body's size) cannot be answered without a real read.

**Review tier.** **SECURITY-SENSITIVE** — a new argv **and** a read that mints a host credential.
The pull request body must state which credential path it reuses and that the token's permission set
is the coordinator's own read-only one (`github.CoordinatorTokenPermissions`, no `write` anywhere),
that no credential reaches the console, and that the read refuses under the credentials service.

---

### C4. `coordinator intents`: what the fleet has declared

**Answers D827.** The read-only transcript pane's source; the console cannot read the intent records
today (reconciliation §5.8).

**Files.**

1. `ee/coordinator/cli/coordinator.go` — `NewCoordinatorCmd` gains `intents`.
2. `ee/coordinator/state.go` — a reader over `coordpath.PathRepo(slug) + "/intents"`, beside
   `ReadRepoView` and through the same `Store`.
3. `ee/coordinator/cli/coordinator_intents.go` (new) — the argv, the flags (`--all`, `--repo`,
   `--since`, `--json`) and the document.
4. `ee/coordinator/cli/coordinator_test.go` — the red step.
5. `ee/coordinator/cli/testdata/` — the fixture.
6. `console/docs/json-contract.md` — the document's section.
7. `console/src/main/spawn.ts` — the `coordinator.intents` row.
8. `console/src/main/ipc.ts` — the operation.
9. `console/src/shared/schema.ts` — the decoder.
10. `console/test/unit/ipc.test.ts` — the pinning test.
11. `docs/design/desktop-console.md` §5.1 — the allow-list table's new row.

**Interfaces.** `fugaro coordinator intents --all --json` prints, per repository,
`{repo, provider, intents: [{id, declared_by, at, branch, release, files, services}]}` — the fields
`coordinator.Intent` already carries and no others, with `declared_by` drawn as **a declaration and
never an identity** (the sentence every coordinator surface already carries).

**Red step.** A test that the command prints a fixture's two intents for one repository and none for
a repository the store holds none for — the second half because "no records" is a fact and "the read
failed" is not.

**Command.** as C3.

**Depends on.** **D827's answer.** Nothing else. It is droppable without touching K1–K4.

**Review tier.** **SECURITY-SENSITIVE** — a new argv. The body must state that the read is the
database read the stored view already makes (`roles/firebasedatabase.viewer`), that it opens no host,
and that the text in an intent is another writer's and is carried as data.

---

## 2. The console side (0.117.0)

### K1. The action queue and the fleet totals

**Answers D810, D812, D813, D819.**

**Files.**

1. `console/src/renderer/screens/coordinator.ts` — `buildCoordinatorModel` gains the queue and the
   totals, computed off the document it already decodes.
2. `console/src/renderer/screens/coordinator.ts` — the queue's words, as exported constants beside
   `COORDINATOR_STORED_WORD` and its neighbours.
3. `console/src/renderer/entry.ts` — `showCoordinator` starts the queue's four reads together and
   draws each section as it answers (the progressive-rendering rule).
4. `console/src/renderer/entry.ts` — the `budget show` read the totals need, which the rail already
   makes.
5. `console/test/unit/coordinator-screen.test.ts` — the red step.
6. `console/test/unit/entry-wiring.test.ts` — the four reads are started in parallel and none waits.
7. `console/test/e2e/coordinator.spec.ts` — the queue on the recorded fixture.
8. `console/test/e2e/__screenshots__/` — the golden, re-recorded **only** with the owner present
   (`.claude/rules/console.md`).

**Interfaces.** Nothing new crosses the IPC boundary: the queue is computed from documents already
on screen plus the two reads the console already makes. That is the whole point of D810 and D819, and
it is why K1 needs **no** allow-list change.

**Red step.** A unit test that a document holding one open non-draft pull request, one halted run row
and one `ready` plan task draws **three** queue items, each with exactly one button, and that a
document holding an open **draft** pull request draws **none** — the second half because a draft is
not waiting for a review, and that is the difference between a queue and a list of everything open.

**Command.** `npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000`.

**Depends on.** C0 for the section's "as of" stamp only (the timing prose).

**Review tier.** Ordinary (no allow-list row moves). It is **the** task that must not invent a
figure: every number it draws either comes off a document or is absent.

---

### K2. Clickable pull request numbers, built and validated

**Answers D814, D815.**

**Files.**

1. `console/src/main/window.ts` — a new pure `pullRequestUrl(provider, repo, number)`, beside
   `validateExternalUrl` and using its allow-list.
2. `console/src/main/window.ts` — the strict patterns: `owner/name` against a reviewer-checked
   pattern, the number a positive integer, the provider one of the two kinds.
3. `console/src/main/ipc.ts` — an operation that takes `(provider, repo, number)` and **builds the
   URL in the main process**, then hands it to `openExternalValidated`.
4. `console/src/preload/` — the one preload method.
5. `console/src/renderer/screens/coordinator.ts` — the number as a button, in the queue and in the
   job list.
6. `console/test/unit/window.test.ts` — the builder's cases.
7. `console/test/unit/ipc.test.ts` — the operation's schema.
8. `console/test/unit/coordinator-screen.test.ts` — the screen's half.

**Interfaces.** `pullRequestUrl(provider, repo, number): string | null`. `null` for an unknown
provider, a malformed `owner/name`, a non-positive number — and **the renderer draws text, not a
button, for `null`**. The URL never comes from `pr.url` in any document (rule 2, reconciliation F2).
The host allow-list is not extended: both hosts are already on `ALLOWED_EXTERNAL_HOSTS`
(`console/src/main/window.ts`).

**Red step.** `window.test.ts`: `pullRequestUrl('github', 'owner/repo-a', 1458)` is
`https://github.com/owner/repo-a/pull/1458`, the Bitbucket case is
`https://bitbucket.org/owner/repo-a/pull-requests/1458`, and six hostile inputs — `provider:
'gitlab'`, `repo: 'owner/../evil'`, `repo: 'owner/repo-a/pull/1'`, `number: 0`, `number: -1`,
`number: 1.5` — each answer `null`. It fails today: the function does not exist.

**Command.** as K1, plus `npm run test:e2e` for the golden.

**Depends on.** **C1** (the provider field). Without C1 there is no provider to pass, and the
alternative — reading `pr.url` — is refused by D815.

**Review tier.** **SECURITY-SENSITIVE.** It adds an operation that reaches `shell.openExternal`, and
the whole of its safety is the first gate (built, not read). The body must state that no document
field reaches the builder as a URL, that the allow-list is unchanged, and that `null` draws text.

---

### K3. The inline description, expandable, loaded on demand

**Answers D816, D817.**

**Files.**

1. `console/src/renderer/screens/coordinator.ts` — the expanded pull-request block.
2. `console/src/renderer/screens/coordinator.ts` — the plain-text renderer: `sanitizeText` with the
   `sentence` class, 20 lines, a Show all control, no element built from anything inside.
3. `console/src/renderer/entry.ts` — the on-demand read, cached with the pane's "as of HH:MM".
4. `console/src/shared/schema.ts` — the `coordinator show` document's decoder and its
   `WIRE_TEXT_CLASSES` rows.
5. `console/src/renderer/entry.ts` — the refusal path for the credentials-service sentence.
6. `console/test/unit/coordinator-screen.test.ts` — the red step.
7. `console/test/e2e/coordinator.spec.ts` — the expanded row on the fixture.
8. `console/test/e2e/__screenshots__/` — the golden, with the owner present.

**Interfaces.** The read is made **once per pull request** and only when the operator expands: an
expand/collapse cycle does not re-read, and a second expand draws the cache until Re-check.

**Red step.** A unit test that a description carrying `<script>alert(1)</script>` and
`[click](https://evil.test)` is drawn as **text** — the DOM holds one text node and no element
carrying either — and that a description of 60 lines is clipped to 20 with the Show all control
present. It fails today: there is no description block.

**Command.** as K1.

**Depends on.** **C3** (the read) and **K2** (the same row).

**Review tier.** **SECURITY-SENSITIVE** — it draws text the host's untrusted author wrote. The body
must state which class it goes through, that no markdown is rendered, that no link is built from it,
and which credentials-service sentence is drawn when the read refuses.

---

### K4. The job list: on demand, expandable, pageable

**Answers D818.**

**Files.**

1. `console/src/renderer/screens/coordinator.ts` — the jobs section, its Load jobs control and its
   "as of" stamp.
2. `console/src/renderer/screens/coordinator.ts` — the row and its expanded body, off `runview.Row`'s
   own fields.
3. `console/src/renderer/entry.ts` — the paged reader and the Older 50 control.
4. `console/src/shared/schema.ts` — no change: `runs.list`'s decoder already exists.
5. `console/test/unit/coordinator-screen.test.ts` — the red step.
6. `console/test/unit/entry-wiring.test.ts` — **no** job read happens before Load jobs.
7. `console/test/e2e/coordinator.spec.ts` — the section on the fixture.
8. `console/test/e2e/__screenshots__/` — the golden, with the owner present.

**Interfaces.** The section holds at most what the operator paged to, plus the cursor of the last
row; the "41 runs, 38 succeeded" header is the **window's** figures, taken from the first page's
`totals` and never recomputed from the rows on screen.

**Red step.** Two tests. A unit test that the section's header shows the window's totals while the
list shows only the page — the two are different numbers in the fixture, and a screen that summed
the rows would show the page's. And a wiring test that **no** `runs ls` spawn happens until Load jobs
is pressed: that is the jobs rule, and it is the one a later change would break by "warming" the
section.

**Command.** as K1.

**Depends on.** **C2**. Until `--limit` exists there is no page to ask for.

**Review tier.** Ordinary — the argv it spawns is C2's, already reviewed there.

---

### K5. The read-only transcript pane

**Answers D826, D827.**

**Files.**

1. `console/src/renderer/screens/coordinator.ts` — the pane: the declared intents, the pane's own
   fixed questions answered from the stored view, and the disabled-input sentence.
2. `console/src/renderer/entry.ts` — the intents read and its "as of" stamp.
3. `console/src/shared/schema.ts` — the intents document's decoder.
4. `console/test/unit/coordinator-screen.test.ts` — the red step.
5. `console/test/e2e/coordinator.spec.ts` — the pane on the fixture.
6. `console/test/e2e/__screenshots__/` — the golden, with the owner present.

**Interfaces.** The pane has **no input control of any kind**. The sentence under it names what is
missing and where the decision is (the terminal's, D820). Every line drawn is text through
`sanitizeText`, and the pane builds no link.

**Red step.** A unit test that the pane draws a fixture's intent lines and that the built subtree
holds **no** `input`, `textarea` or contenteditable element — the second half being the whole
security claim of the read-only state.

**Command.** as K1.

**Depends on.** **C4** and **D827**.

**Review tier.** Ordinary (no allow-list row beyond C4's).

---

## 3. The terminal: approved D820–D828, the input box held

**No task for the input box of the terminal is written here, and none may be started before the front door and the independent security review.** [reconciliation.md](reconciliation.md)
§5 is the design; the tasks that build it are written **now that the owner has answered D820–D828 (2026-10-31)**, in one
of two shapes:

- If **D820 is answered "the coordinator's front door"**, the tasks depend on `docs/coordinator/implementation-plan.md`
  **P7** and **P8** existing — the service, its IAM and its tools — and the first of them is a
  **security review**, not a build task. The order that follows is fixed: the service and its tool
  set, then the CLI's `coordinator ask`, then the console's row and pane, each with its own
  independent review.
- If **D820 is answered "not now"**, nothing is written, and **K5** is the whole of the answer to
  the owner's sentence about a terminal — which is why D827 exists as a separate decision.

## 4. The critical path, and the shared files

```
C1 ──► K2 ──► K3          C2 ──► K4          C4 ──► K5
C0 ──────────► (fills the spec's numbers)     K1 ──► (needs only C0's stamps)
```

**The files more than one task touches**, and what each keeps separate:

| File | Tasks | The rule |
| --- | --- | --- |
| `console/src/main/spawn.ts` | C2, C3, C4 | one row per operation, `assertSpawnSurface` fails a table where a row has no operation |
| `console/src/main/ipc.ts` | C2, C3, C4, K2 | the same pairing from the other side, plus the never-reachable table's rows |
| `console/src/shared/schema.ts` | C1, C3, C4, K3 | strict decoding: a key one side gains without the other is a refusing console, not an unread field |
| `console/src/renderer/screens/coordinator.ts` | K1, K2, K3, K4, K5 | four of the five append a section; K1 owns the model, the rest own their section |
| `console/src/renderer/entry.ts` | K1, K3, K4, K5 | one `showCoordinator`, and the parallel start lives in it |
| `docs/design/desktop-console.md` §5.1 | C2, C3, C4 | the allow-list table, which the Go docs tests pin |
| `console/docs/json-contract.md` | C1, C2, C3, C4 | one section per document |

**Order:** C0, C1, C2, C3, C4 (CLI, 0.15.0), then K1, K2, K4, K3, K5
(console, 0.117.0). C0 first because its numbers are what O1 and O2 are waiting for, and because a
figure in [spec.md](spec.md) that turns out wrong changes what the console may draw.
