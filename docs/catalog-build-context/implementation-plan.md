# A repository build context for catalog `dockerfile` entries: implementation plan

**Status: plan only; nothing is implemented; no task approved.** Five tasks, **CB1–CB5**, each with
its files (at most six, and CB1 is exactly at the cap), its interfaces, its named tests, its **red
step**, its **mutation**, its **reviewer tier**, what it depends on, its command and its commit.
Written against the tree of **2026-11-06** (`495104e5`, the tip of `main` when this folder was
written); every path and symbol below was resolved against that tree while writing it.

**No task may start.** `docs/project-image-catalog/context-design.md` — merged on `main`, in the
folder whose spec this feature belongs to — already designs exactly this feature, unbuilt and
unapproved ([reconciliation.md](reconciliation.md) **F29**). **D1408** asks whether this folder folds
into it, supersedes it or stands beside it, and until the owner answers, **CB1 does not start**, because
the answer decides which folder's tasks are real. Every other gate is §7.

**Written to:** [spec.md](spec.md) (issue **#1738** verbatim) and [reconciliation.md](reconciliation.md).
**Where they disagree, the reconciliation wins**; where a task below and the reconciliation disagree,
**the reconciliation's findings (F1–F33) and decisions (D1390–D1409) win**, and the task is wrong.

**Rules every task obeys** (this repository's own, not this plan's invention):

- a **red step** first: the named test fails for the reason the task exists, and the failure is quoted
  in the pull request;
- **targeted tests only** (`GOFLAGS=-p=2`, `-count=1`, `-timeout 40m` where a package is slow), never a
  whole package that takes half an hour — `internal/cli` and `internal/runner` are the two that bite;
- **hermetic tests only** (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`): no live call, no
  registry, no real job, no network, no home directory;
- **no secret-shaped literal** anywhere, in a test or a doc;
- **a field never changes meaning**: no task reuses or retypes an existing wire field, config key or
  environment name; the record's new fields are new names (F25, D1400);
- **`fugaro update` stays the whole upgrade**: nothing here adds an operator step, and no release note
  may say "now also run …" (the repository's hard rule);
- **comments explain why, never what**; no TODO, no section header, no restatement of a name;
- **every path and symbol a task names must exist on `main`** before that task starts; the two
  absences this folder found are recorded in reconciliation §6 **U5**;
- **`.github/workflows/` is never touched** by any task (F27).

---

## 1. The order, and what forces it

| Level | Task | What forces it |
| --- | --- | --- |
| 1 | **CB1** — `context` in the source grammar, and the shapes every later task reads | **D1408's answer, first of all.** Nothing else. It writes no behaviour: a grammar that accepts a key nothing reads is what makes CB2 and CB3 writable at the same level |
| 2 | **CB2** — the copy reads one commit, and stages what the check will compare | CB1 alone. It is the producer half of the interface CB1 fixed: it writes the pinned commit and the context hash into `builds/catalog/<entry>/`, and CB3 consumes them |
| 2 | **CB3** — the checkout step, and the context the build runs on (SEC) | CB1 alone, and **not CB2**: the two share an interface, not a file. It is the only task that changes what a Cloud Build executes: **SEC — opus, with the independent security review mandatory** |
| 3 | **CB4** — the context change is a trigger | CB2 (the staged hash it reads) and CB3 (the record field it compares). It makes the promise the issue's second bullet asks for — "the rebuild triggers stay exact" |
| 4 | **CB5** — the skew gate, the folded-in design note, and Check 292 | CB4 for the half that documents behaviour (**a doc that says a context change rebuilds an entry is false until CB4 has landed**), and CB1 alone for the gate half. CB5 splits in exactly that way and says so in its commit |

**CB2 and CB3 are the same level and share no file**, which is why they are two tasks and not one:
CB2 is `internal/catalog` and `internal/cli`'s copy, CB3 is `internal/backend/gcp` and the Cloud Build
template. Either can land alone and neither release is worse for it — an entry whose context is staged
but never checked out is built exactly as today (CB2 alone), and a checkout with nothing to pin is
refused by CB1's validation before it is reached (CB3 alone).

**The critical path is CB1 → CB2 → CB4 → CB5**, with **CB3** as a second level-2 branch that CB4 also
waits on. **CB3 is the expected one to slip**: it is the only SEC task, the only one that touches the
Cloud Build template, and the only one whose change is invisible to the check until CB4 lands (F33).
Nothing else in this plan moves if it slips — CB1, CB2 and CB5's gate half ship, and the release is
never worse than the tree it replaces.

## 2. Shared files

One writer per file per level. `internal/imagecheck/check.go` is the only file two tasks touch, and
they are at **different levels** (CB3 at 2, CB4 at 3) writing **disjoint regions** — CB3 the record
struct's two new fields, CB4 the comparison and the new reason constant — which is why CB4 depends on
CB3 rather than running beside it.

| File | Tasks (level) | Note |
| --- | --- | --- |
| `internal/config/catalog.go` | CB1 (1) | the `Source.Context` field, `validateCatalogSource`'s rule for it, and the two new bucket-key helpers beside `CatalogDockerfileKey`. One writer |
| `internal/config/catalog_test.go` | CB1 (1) | the validation tests |
| `internal/settings/registry.go` | CB1 (1) | the `loc("image_catalog.entries.*.source.context", …)` row beside the existing `image_catalog.entries.*.source.*` rows |
| `schemas/settings.schema.json` | CB1 (1) | the member, beside `image_catalog.entries.*.source.path` |
| `internal/projectpolicy/policy.go` | CB1 (1) | `catalogSourceMembers` gains `"context"` — the node's own allowlist, which a strict decode alone would not cover (F10) |
| `internal/projectpolicy/policy_test.go` | CB1 (1) | the node-decode test |
| `internal/catalog/fetch.go` | CB2 (2) | the read at a resolved commit, and the context tree's own read |
| `internal/catalog/fetch_test.go` | CB2 (2) | the fetch tests |
| `internal/cli/catalog_copy.go` | CB2 (2) | `writeCatalogDockerfile` and the new staging writes |
| `internal/cli/catalog_copy_test.go` | CB2 (2) | the copy tests |
| `internal/cli/image_build_catalog.go` | CB2 (2) | the commit `gitRead(… "rev-parse", "HEAD")` resolves today; the entry's own commit replaces it |
| `internal/cli/image_build_catalog_test.go` | CB2 (2) | the submission tests |
| `internal/backend/gcp/catalog.go` | CB3 (2) | `catalogBuildSteps` gains the checkout step; `CatalogBuildSpec` gains the two fields; `catalogRecordStep` writes them |
| `internal/backend/gcp/catalog_test.go` | CB3 (2) | the step-order tests |
| `internal/backend/gcp/build.go` | CB3 (2) | the step's own builder, beside `CatalogDockerfileStep`, and `TemplateSalt`'s doc if the salt moves (D1402) |
| `internal/backend/gcp/build_test.go` | CB3 (2) | the step-builder tests |
| `images/derived/cloudbuild.yaml` | CB3 (2) | the new step, by id, in the file `catalogBuildSteps` picks steps out of |
| `internal/imagecheck/check.go` | CB3 (2) **and** CB4 (3) | **two writers at two levels, disjoint regions**: CB3 the two fields on `CatalogRecordState`, CB4 the comparison in `DecideCatalog` and the reason constant. CB4 depends on CB3 for this file and says so |
| `internal/imagecheck/check_test.go` | CB4 (3) | the decision tests |
| `internal/cli/image_check_catalog.go` | CB4 (3) | `evaluate` reads the staged hash and puts it in `CatalogInputs` |
| `internal/cli/image_check_catalog_test.go` | CB4 (3) | the job's read tests |
| `internal/infra/readiness.go` | CB5 (4) | the skew gate, the shape `imageCatalogShipped` already has (F22) |
| `internal/infra/readiness_catalog_test.go` | CB5 (4) | the gate's test |
| `docs/project-image-catalog/context-design.md` | CB5 (4) | **the fold-in D1408 asks for**, in that folder's own series — or, if the owner answers "stand beside", a one-line pointer in each direction and nothing else |
| `docs/gcp-live-checklist.md` | CB5 (4) | **only** Check 292's own summary row and section, written by the task that implements it, under the register's own rule |

**Not touched by this plan, and named so it is not discovered later:** `deploy/terraform/**` (the
recommended answers to D1398 and D1401 add no IAM grant and no secret: the check job keeps the
credential-less shape of `internal/infra/tf/cover.go`'s `CatalogCheckAccountID` and the
`objectViewer`-only grant of `internal/backend/gcp/names.go`'s `CatalogReadCondition`. **If the owner
takes D1401's alternative — a git credential for the check job — that answer is false and CB4 grows a
Terraform task, promoted to SEC**), `internal/cli/sharedcfg.go` (`image_catalog` is already in
`sharedForbidden`), `internal/backend/gcp/names.go` (no new name, no new condition), `internal/infra/spec.go`
and `internal/infra/catalogcheck.go` (the job spec already carries the whole `config.Catalog`), `plugin/**`
(no command and no flag changes, so the plugin's CLI-vs-skill tests have nothing to move),
`console/**`, `mobile/**`, `shared/**`, `website/**`, `ee/**`, `SECURITY.md` (unless D1401's
alternative is taken, in which case §4.6's analysis is what that file's pull request adds), and
`.github/workflows/**`.

## 3. Tasks

### CB1. `context` in the source grammar, and the shapes every later task reads

**Decisions:** D1390, D1391, D1392, D1393, D1400, D1405. **Findings:** F14, F15, F22, F25, F26.
**Depends on:** **D1408's answer**, and nothing else. **Reviewer tier:** standard.

- **Files:** `internal/config/catalog.go`, `internal/config/catalog_test.go`,
  `internal/settings/registry.go`, `schemas/settings.schema.json`, `internal/projectpolicy/policy.go`,
  `internal/projectpolicy/policy_test.go`. **6 files — at the cap.** The record's two new fields are
  **not** here: they are CB3's, declared by the task that writes them, so this task stays at the cap
  and CB4 depends on CB3 for them.
- **Interfaces:** `config.Source` gains `Context string \`yaml:"context,omitempty" json:"context,omitempty"\``,
  meaning **the context root relative to the repository `Repo` names**, `""` when the entry has none.
  `validateCatalogSource`'s `SourceDockerfile` branch gains the rule, through a new
  `catalogContextMessage` **beside** `catalogPathMessage` rather than reusing it (F15, D1392): a
  context may be `.` (the repository root), it may not carry a control character, it may not be
  absolute, it may not hold a `..` segment, and it may be at most `maxCatalogPathLen` — the same
  rules, minus the one that refuses the root, and the two messages say why they differ. `context` is a
  `SourceDockerfile` field, so `addCatalogWrongKind`'s table gains a row for it and a source of
  another kind carrying it is refused with the existing "belongs to the %s source; this source is %s"
  message. Two new helpers beside `CatalogDockerfileKey` — `CatalogContextCommitKey(name)` and
  `CatalogContextHashKey(name)` — return `builds/catalog/<name>/source-commit` and
  `builds/catalog/<name>/context-sha256`, checked against `catalogNameRE` the way
  `CatalogDockerfileKey` is, because this is again the one place a name becomes a key.
  `internal/projectpolicy/policy.go`'s `catalogSourceMembers` gains `"context"`.
- **Tests:** `TestCatalogContextIsADirectoryPath` (`.` and `ci/` are accepted; a `..` segment, an
  absolute path, a control character and an over-long value are each refused with the context's own
  message, not the Dockerfile path's), `TestCatalogContextBelongsToADockerfileSource` (an `inline` or
  `image` source carrying `context` is refused by `addCatalogWrongKind`'s message),
  `TestCatalogContextKeyRefusesANameOutsideTheGrammar` (`CatalogContextCommitKey` and
  `CatalogContextHashKey` refuse a name `CatalogDockerfileKey` refuses), and
  `TestPolicyAcceptsAContextInTheNode` / `TestPolicyRefusesAnUnknownSourceMember` (the node's
  `catalogSourceMembers` allowlist: `context` decodes, a spelling it does not list is refused).
  **Red step:** `TestCatalogContextIsADirectoryPath` fails on the first assertion — `Source` has no
  `Context` field, so `internal/localcfg`'s strict `Parse` (`dec.KnownFields(true)`) refuses the whole
  file with an unknown-field error before validation is reached (F14).
- **Mutation to hold it:** reuse `catalogPathMessage` for `context` → `TestCatalogContextIsADirectoryPath`
  fails on `.`, which is the issue's own example value and the one the two keys must disagree about
  (F15). Second mutation: drop the `catalogNameRE` check from the two new key helpers → the
  name-outside-the-grammar test fails, and that is the mutation that would let a name write outside
  `builds/catalog/<entry>/`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/config/ -run 'Catalog' -count=1` then
  `go test ./internal/projectpolicy/ -run 'Catalog|Policy' -count=1` then
  `go test ./internal/settings/ -count=1` then `go build ./... && gofmt -l . && go vet ./...`.
- **Commit:** `config: a dockerfile entry names a build context`.

### CB2. The copy reads one commit, and stages what the check will compare

**Decisions:** D1394, D1395, D1398, D1399, D1401 (the recommended half), D1405. **Findings:** F2, F16,
F19, F20, F24, F31. **Depends on:** CB1. **Reviewer tier:** standard.

- **Files:** `internal/catalog/fetch.go`, `internal/catalog/fetch_test.go`,
  `internal/cli/catalog_copy.go`, `internal/cli/catalog_copy_test.go`,
  `internal/cli/image_build_catalog.go`, `internal/cli/image_build_catalog_test.go`. **6 files.**
- **Interfaces:** `internal/catalog`'s `Fetch` gains a sibling that resolves the entry's `Branch` to a
  **commit** once and reads both the Dockerfile and the context tree at it — the shape the issue's
  "exact commit the Dockerfile was read at" needs and the tree has no equivalent of today (F2, F16).
  `writeCatalogDockerfile` keeps its read-compare-write shape and gains the two staged objects under
  CB1's keys: the resolved commit (`CatalogContextCommitKey`) and the sha256 of the context's
  contents (`CatalogContextHashKey`). The context hash is a **stable digest over the tree** — paths
  and blobs, in a fixed order — so it is comparable across machines and across a `git` version, and it
  is what CB4 compares. `runImageBuildCatalog` stops recording `gitRead(… "rev-parse", "HEAD")` as
  the entry's commit for a context entry and records the staged one; **an entry with no context keeps
  today's value and today's path, byte for byte** (F23). A bound (D1398's answer) is enforced here,
  at copy time, so the refusal names the entry before a build is paid for; `.dockerignore` is honoured
  by being left alone (D1399's recommended answer) and is **not** written, generated or required.
- **Tests:** `TestCatalogCopyReadsTheDockerfileAndTheContextAtOneCommit` (a fake opener that answers
  two different trees for two different refs, and a copy that must read both at the resolved commit,
  not at the branch), `TestCatalogCopyStagesTheContextHash` (the hash object's bytes are the digest of
  the tree, and an unchanged tree writes nothing the second time — the read-compare-write property the
  Dockerfile copy already has), `TestCatalogCopyBoundsTheContext` (a context over the bound is refused
  with a message naming the entry, and nothing is written), and
  `TestCatalogCopyWithoutAContextIsUnchanged` (an entry with no `context` writes exactly the objects
  it writes today, and its recorded commit is still the checkout's HEAD). The last one is the
  regression the issue's own last paragraph promises.
- **Red step:** `TestCatalogCopyReadsTheDockerfileAndTheContextAtOneCommit` fails on the fake opener:
  the context is read at the branch, so the two trees differ from the pinned commit's — the failure is
  `internal/catalog/fetch.go`'s `fetchDockerfile` calling `r.Contents(ctx, s.Path, s.Branch)`, a ref
  read that never answers a commit (F2).
- **Mutation to hold it:** stage the branch instead of the resolved commit → the first test fails on
  the two reads disagreeing. Second mutation: hash the context with a walk that does not sort its
  paths → `TestCatalogCopyStagesTheContextHash` fails on the second copy of an unchanged tree writing
  a different object, which is the mutation that would rebuild every context entry every night.
- **Command:** `GOFLAGS=-p=2 go test ./internal/catalog/ -count=1` then
  `go test ./internal/cli/ -run 'CatalogCopy|ImageBuildCatalog|Catalog' -count=1`.
- **Commit:** `catalog copy: one commit for the Dockerfile and its context`.

### CB3. The checkout step, and the context the build runs on (SEC)

**Decisions:** D1396, D1397, D1400, D1404, D1406, D1407, and D1402's salt half. **Findings:** F1, F3,
F4, F7, F9, F12, F21, F24, F25, F33. **Depends on:** CB1. **Reviewer tier: SEC — opus, with the
independent security review mandatory.**

- **Files:** `internal/backend/gcp/catalog.go`, `internal/backend/gcp/catalog_test.go`,
  `internal/backend/gcp/build.go`, `internal/backend/gcp/build_test.go`,
  `images/derived/cloudbuild.yaml`, `internal/imagecheck/check.go`. **6 files, plus
  `images/cloudbuild_test.go` if the new step changes a property that file pins** — the pull request
  says which, and a step added between two existing ones should not.
- **Interfaces:** `catalogBuildSteps` gains one step **by id**, between `dockerfile` and
  `catalog-pin`, that clones the entry's repository at the staged commit into `/workspace/context`
  and removes the Dockerfile it just copied there, so the context is the repository's tree and the
  Dockerfile the build uses is the one CB2 read (`--file /workspace/context/<path>`, D1404). The
  clone is the shape `images/derived/Dockerfile.tmpl` already proves works: a shallow clone, then
  `git cat-file -e "$commit^{commit}" || git fetch --quiet origin "$commit"`, then
  `git reset --hard "$commit"` (F12) — a shallow `--depth 1 --branch` clone does not contain an
  arbitrary commit, so the fetch-by-sha fallback is load-bearing. It is **outside** the Docker build,
  because Docker resolves `COPY` before any `RUN` and a checkout inside the build is not a context at
  all (F21, D1396). When `context` names a subdirectory, that directory **is** the context root
  (D1406). `CatalogBuildSpec` gains the two fields the step needs (the entry's `Context` and the
  pinned commit); `check` refuses a spec that has one without the other. `CatalogRecordState` gains
  `ContextCommit` and `ContextSHA256` — **two new names, neither a reuse of `SourceCommit`** (F25,
  D1400) — written by `catalogRecordStep`. `docker build` **keeps** its
  `--secret "id=git-credentials,src=/creds/git-credentials"` (D1397): D1208's migrated entries clone
  inside their own Dockerfile and need it, and stopping it for context entries would make the two
  entry shapes behave differently for no gain. `TemplateSalt` covers the changed
  `images/derived/cloudbuild.yaml`, so the catalog check should salt on it in the same change (F33,
  D1402's second half) — **if the owner takes that half**; the alternative is stated in the task's
  commit message rather than left silent.
- **Tests:** `TestCatalogBuildHasACheckoutStepForAContextEntry` (the step order is
  `dockerfile`, `catalog-context`, `catalog-pin`, … and the step's script names the staged commit, not
  the branch), `TestCatalogBuildHasNoCheckoutStepWithoutAContext` (an entry with no context builds the
  step list it builds today, in the same order — the regression CB1's grammar promises),
  `TestCatalogContextRootIsTheNamedSubdirectory` (`context: ci/` makes the build's context `/workspace/context/ci`,
  or copies that directory into `/workspace/context`, whichever D1406's answer fixes, and the
  Dockerfile's `COPY` resolves from there), and `TestCatalogRecordCarriesTheContextCommitAndHash`
  (the record has both new fields, and neither is spelled `source_commit`). **The reviewer is asked
  for one thing beyond the tests:** whether the step can read or write anything the build account
  could not already read or write before it existed (reconciliation §4.2, F24).
- **Red step:** `TestCatalogBuildHasACheckoutStepForAContextEntry` fails on the step list: there is no
  step between `dockerfile` and `catalog-pin`, and `/workspace/context` holds one file
  (`CatalogDockerfileStep`'s "a catalog build reads one object instead of a checkout", F4).
- **Mutation to hold it:** run the clone **inside** the Docker build instead of before it → the
  first test fails on the step order, and the mutation is exactly the one that would leave `COPY`
  broken while looking like the feature (F21). Second mutation: reuse `SourceCommit` for the context's
  commit → `TestCatalogRecordCarriesTheContextCommitAndHash` fails on the field's name, which is the
  wire-format rule (F25). Third mutation: drop the `git fetch` fallback and keep only the shallow
  clone → the step fails on a commit that is not the branch head, which is the case the issue's
  "exact commit" asks for.
- **Command:** `GOFLAGS=-p=2 go test ./internal/backend/gcp/ -run 'Catalog' -count=1` then
  `go test ./images/ -run 'CloudBuild' -count=1` then `go build ./...`. That last one is not
  optional: `images/cloudbuild_test.go` has no build tag (the `docker`-tagged files beside it are the
  ones that build images), and it already pins the yaml's properties a new step can break —
  `TestCloudBuildCredentialOnlyInVolume`, `TestCloudBuildNoSubstitutionsInScripts`,
  `TestPromoteAfterSmoke` and `TestSmokeIsNotAStep`. A new step that reads `$$COMMIT` where the file
  requires `$$` escaping, or that carries a secret outside the volume, fails there and not in the
  build.
- **Commit:** `catalog build: check out the entry's context before docker build`.

### CB4. The context change is a trigger

**Decisions:** D1401, D1402, D1406 (the half the trigger needs). **Findings:** F17, F18, F33.
**Depends on:** CB2 and CB3. **Reviewer tier:** standard.

- **Files:** `internal/imagecheck/check.go`, `internal/imagecheck/check_test.go`,
  `internal/cli/image_check_catalog.go`, `internal/cli/image_check_catalog_test.go`. **4 files.**
- **Interfaces:** `CatalogInputs` gains `ContextSHA256` and `CatalogInputsFingerprint` gains the same
  field, so the back-off's `Equal` covers it and a context that moved and moved back does not rebuild
  twice. `DecideCatalog` gains one comparison beside the definition's, with its own reason constant
  (`ReasonCatalogContext`, F17) — a **new reason**, because `ReasonCatalogDefinition`'s own doc says
  it means the copied definition's bytes changed, and a night when both moved must record both
  (D1402). `internal/cli/image_check_catalog.go`'s `evaluate` reads CB1's `CatalogContextHashKey`
  object from the bucket and puts it in `CatalogInputs` — **a bucket read, not a git read**, which is
  the whole point of the recommendation: the job keeps the credential-less shape the comment on its
  `Lockfiles: false` states (F17, §4.6). `rebuild.paths` gets no catalog counterpart (F18, D1401):
  the trigger is the whole context tree, and a narrower list is not in this plan.
- **Tests:** `TestDecideCatalogFiresOnAContextChange` (the staged hash differs from the record's →
  `catalog-context` fires, beside `catalog-definition` when the Dockerfile moved too, in that order),
  `TestDecideCatalogDoesNotFireOnAnUnchangedContext` (the same hash → no reason, and the back-off's
  `Equal` sees the same inputs), and `TestCatalogCheckReadsTheStagedContextHash` (the job's `evaluate`
  reads the hash object and never calls the git provider — the test fails if a `gitprov` opener is
  touched at all, which is the assertion that keeps the check credential-less).
- **Red step:** `TestDecideCatalogFiresOnAContextChange` fails on the reasons: the decision is `Skip`,
  because `DecideCatalog` compares `in.DefinitionSHA256 != rec.DefinitionSHA256` and nothing else
  (F17).
- **Mutation to hold it:** fold the context into `ReasonCatalogDefinition` → the first test fails on
  the reason's name, and the mutation is the one D1402 refuses. Second mutation: read the context
  through a git opener instead of the staged object → the third test fails, and that is the mutation
  that would give the check job a credential it was designed not to have (§4.6, F13).
- **Command:** `GOFLAGS=-p=2 go test ./internal/imagecheck/ -run 'Catalog' -count=1` then
  `go test ./internal/cli/ -run 'ImageCheckCatalog|CatalogCheck' -count=1`.
- **Commit:** `imagecheck: a context change is a catalog trigger`.

### CB5. The skew gate, the folded-in note, and Check 292

**Decisions:** D1403, D1408, D1409. **Findings:** F22, F29, F30, F32. **Depends on:** CB4 for the half
that describes behaviour, CB1 alone for the gate half. **Reviewer tier:** standard.

- **Files:** `internal/infra/readiness.go`, `internal/infra/readiness_catalog_test.go`,
  `docs/project-image-catalog/context-design.md`, `docs/gcp-live-checklist.md`. **4 files.**
- **Interfaces:** a `var imageContextShipped = false` beside `imageCatalogShipped`, flipped by the
  release that first writes a node carrying `context` (F22, D1403), and the `Readiness` branch that
  reads it: a workflow whose entry carries a context is refused on an installation whose base image
  predates that release, with the sentence naming the version, the way `imageCatalogShipped`'s refusal
  does. The second half is the fold-in **D1408** asks for: if the owner answers "fold into
  `docs/project-image-catalog/context-design.md`", this task carries this folder's findings into that
  amendment's own series (G/A/O/C) and leaves this folder as the reading that fed it; if the owner
  answers "stand beside", the same task writes the one-line pointer in each direction and nothing
  else. **It is the same task either way**, because both answers are a documentation change in the
  same release and neither is a code path.
- **Tests:** `TestReadinessRefusesAContextEntryBeforeTheGateIsFlipped` (the gate off → the workflow is
  refused with the version sentence; the gate on → it is not), and
  `TestReadinessDoesNotRefuseAnEntryWithNoContext` (the same installation, an entry with no context,
  deployed — the gate must not touch entries the feature does not apply to). Then the docs tests:
  `go test ./internal/cli/ -run Docs -count=1`, which is the only way to know whether any pinned
  sentence, anchor or count moved.
- **Red step:** `TestReadinessRefusesAContextEntryBeforeTheGateIsFlipped` fails on the gate: there is
  no such variable, so a node carrying `context` is deployed to a runner that reads it as an unknown
  key rather than refused (F22).
- **Mutation to hold it:** gate on the *catalog* rather than on the context key → the second test
  fails, and that is the mutation that would refuse every catalog project on an old installation,
  which is the opposite of what a skew gate is for.
- **Command:** `GOFLAGS=-p=2 go test ./internal/infra/ -run 'Catalog|Readiness' -count=1` then
  `go test ./internal/cli/ -run Docs -count=1`.
- **Commit:** `infra: a context entry is refused by a runner that predates it, and the note`.

---

## 4. What is deliberately not in this plan

- **A narrower path list for the trigger** (the issue's "or a narrower list of paths, like
  `rebuild.paths`"). D1401's recommended answer makes the whole context tree the trigger; a
  `context.paths` key is a second feature with its own grammar, its own validation and its own
  interaction with `.dockerignore`, and the issue offers it as an alternative to the first, not
  beside it (F18).
- **A context in another repository.** D1393 recommends against it, and the issue's own wording —
  "the repository the Dockerfile is in" — does not ask for it.
- **A `RUN --mount=type=secret` clone inside the entry's Dockerfile, done by the engine.** That is
  D1208's answer to a different question (the migrated entry's own cache warm-up) and it is the
  entry's Dockerfile that does it, by hand. CB3 does not generate, rewrite or validate it.
- **Any change to `fugaro image promote`.** `internal/cli/image_promote.go` writes a catalog entry
  from a repository's image definition; a context is not something it can infer, so it writes no
  `context` key and an entry it writes builds exactly as today.
- **A new command or flag.** The feature is a config key, so `plugin/skills` has nothing to move and
  the plugin's CLI-vs-skill tests are untouched.
- **`deploy/terraform/**`, unless D1401's alternative is taken.** §2 says why.
- **`SECURITY.md`.** §4's analysis is a trust statement this folder writes down; if D1401's
  alternative is taken, that file's sentence is CB4's, and CB4 is promoted to SEC.
- **Anything under `.github/workflows/`.** F27.

## 5. Release slot, and what the release owes

**No slot is claimed here.** **D1409** is the owner's: which release carries this, and in what order
against `docs/repo-image-removal` — whose **D1208** (owner ruling 2026-11-04) refused an engine-side
mechanism keyed by repository, on the ground that it "re-creates the per-repository build input inside
the catalog, which is the thing the ruling removes" (F30). The plan **assumes the removal lands
first**, so this work is a later release than the one that makes the catalog the only image path, and
the design can then be read against the entries D1208 actually produced. If the owner answers the
other way, CB1 and CB5's gate half are unaffected and CB2–CB4's ordering against R0–R2 becomes a
release-planning problem rather than a code one.

**The `### For operators` sentence the release owes, quoted here so it is not invented on release
day.** `fugaro update` is the whole upgrade — the repository's hard rule — so this section may not
name a second command, a deletion or a manual repair:

> **### For operators**
>
> Run `fugaro update`. It rebuilds the installation's base image and the catalog images whose entry
> changed shape, and it asks before it starts those builds. An entry that names no `context:` is built
> exactly as before and its image is not rebuilt. A runner older than this release refuses a project
> whose catalog carries a `context:` with a message naming the version it needs, rather than building
> an entry whose context it would silently ignore.

**Check 292 is not a gate on the release and does not move the slot.** It is the evidence the owner
runs on a real installation, and it is the last thing this work owes, not the first.

## 6. Check 292 — the live check (owner-run, NOT RUN)

**One entry with a context, one build, one nightly decision, and four facts read back.** It is
owner-run by the register's own rule, it needs a real installation and a real repository, and it is
**NOT RUN** — nothing in this folder has been executed.

**Number:** **Check 292** — the first number free above the register's highest **written** section,
**290** (`docs/gcp-live-checklist.md`), read on 2026-11-06 over `main` and every open pull request.
**291 is claimed** by `docs/image-env`, which states the register's rule: a claim above the highest
written section is settled at its merge, and whichever of two claims merges second renumbers itself.
**292–299 are claimed by nobody**; `docs/stateless-orchestrator` claims 300–309 and `pr/1780`'s
`docs/cli-output-console-graph` claims 310–314, both above this block.
[reconciliation.md](reconciliation.md) §8 is the reading, with its commands.

**What it does.** On an installation with a project image catalog, add an entry whose source is a
`dockerfile` with `context: .` at a repository whose Dockerfile has a `COPY` of a file in that
repository — the issue's own example is exactly this. Then:

1. `fugaro update` copies the definition and stages the pinned commit and the context hash, and asks
   nothing it did not ask before.
2. `fugaro image build` builds the entry, and the built image **contains the copied file** — the
   fact the whole issue is about, read back with `docker run … cat /opt/team/scripts/<file>` or the
   image's own `docker inspect` history.
3. The entry's build record names **one** commit, and the image's
   `org.opencontainers.image.revision` label is that same commit — the "one record of what was
   built" the issue's first bullet asks for.
4. A commit that changes a file **under the context directory** and nothing else, pushed to the
   branch, makes the nightly catalog check decide `Rebuild` with the `catalog-context` reason — and a
   commit outside the context directory, with the entry's Dockerfile unchanged, does not.

**What a failure means.** (2) failing means CB3 is wrong and the feature does not exist. (3) failing
means CB2 pinned the wrong commit. (4) failing means CB4 is wrong, and the entry will be rebuilt
nightly or never — either way the issue's second bullet is unmet. The register's own summary row and
section are written by the task that implements the check, under the number free **at that merge**.

## 7. Gates, and what a task may not start without

| Task | May not start until |
| --- | --- |
| **CB1** | **D1408** (fold into `docs/project-image-catalog/context-design.md`, supersede it, or stand beside it) — the blocking one; then D1391 (where the key lives), D1392 (`.` allowed), D1393 (same repository only), D1405 (a repository the project has a credential for) |
| **CB2** | CB1 landed, and D1394/D1395 (which commit, and whether the Dockerfile is read at it), D1398 (the bound), D1399 (`.dockerignore`) |
| **CB3** | CB1 landed, and D1396 (outside the Docker build), D1397 (the credential stays), D1400 (the record's two new names), D1404 (the build reads the copied Dockerfile), D1406 (what a subdirectory context means), D1407 (smoke unchanged) |
| **CB4** | CB2 and CB3 landed, and D1401 (how the check learns the context moved — **if the answer is a credential for the check job, this task grows a Terraform task and is promoted to SEC**), D1402 (the reason's name, and the template salt) |
| **CB5** | CB4 landed for the note half, CB1 for the gate half, and D1403 (the skew gate), D1409 (the release and its order against the removal) |

**If CB3 slips**, CB1, CB2, CB4's decision half and CB5's gate half are all still worth landing, and
none of them is worse than the tree it replaces: a grammar that accepts a key nothing reads, a copy
that pins a commit nothing checks out, a trigger for a hash that never changes, and a gate that
refuses a key no node carries. **The owner's call is whether that partial state ships** — this plan
does not assume it does, and every task above is written so it can land alone.
