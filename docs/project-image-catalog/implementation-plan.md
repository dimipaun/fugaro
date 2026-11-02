# Project image catalog: implementation plan

**Nothing here is approved to build.** The owner's ruling of 2026-11-02 is explicit: this pull request
is the design, the build tasks are planned in it, and **nothing is built until the owner approves the
plan** ([spec.md](spec.md) §Owner rulings). Seven decisions are answered — **D1140** (the catalog
travels in the published policy node under `image_catalog`), **D1146** (a pin acts at deploy time and
warns), **D1149** (VM/Batch only, and the one Cloud Run rule), **D1150** (the existing workflow-secret
path; the credentials service unchanged), **D1152** (the shared registry and its retention), **D1155**
(override plus catalog reference refused, naming both keys) and **D1158** (**the build tasks start in
0.16.0**). **D1148 stays open** with its recommendation (reuse the daily check's triggers), and so do
the rest of [reconciliation.md](reconciliation.md) §2: each task below says which decision it waits
on.

Each task touches at most 10 files, names its tests, starts with a **red step**, and ends at the gates
below. A task marked **SEC** gets the owner's independent review in addition to the usual one. The
tasks are **PIC1–PIC7**; the tasks are ordered, not numbered by dependency.

## Dependencies

Four things this plan does **not** own, and one it reads.

- **The published policy node's keep rule** (**D1075**, [docs/c39-review-identity/](../c39-review-identity/)):
  the catalog rides the mechanism `verifier_accounts` establishes — a new top-level key of
  `internal/projectpolicy.Policy`, copied from the local configuration by `fugaro update`, omitted when
  empty, operator-owned via `sharedForbidden`. If that key's publication path changes before PIC2
  starts, **D1140** and **D1156** are re-read.
- **The multi-repo job** (`docs/forge-authorship-images/`, slice D, gated on D613–D616): one run, one
  repository is the code's deepest invariant (**F12**). PIC5 writes the tag-cover rule for the union
  over a job's repositories so it is correct the day a multi-repo job exists, but **no task here builds
  one**, and the single-repo case is what ships.
- **The shared-images design** (D617–D622; C8a built, C8b/C8c not): **CATALOG FIRST** (D1141). No task
  here touches `internal/imagecheck`'s key, superset or lock, and none deletes C8a. PIC4 **reads**
  `imagecheck`'s check triggers and adds one; PIC6's `image ls` and the C8c design's `image ls` are the
  same command with a `--shared` section (**D1143**), and C8c is not a dependency of any task here.
- **The change-set / coordinator work** (`docs/coordinator/`): the "linked run" of
  [reconciliation.md](reconciliation.md) D1154 is that work's, and no task here adds one.
- **Read, not changed:** `internal/credsvc` (the credentials service's closed bundle stays closed —
  **D1150**, **F9**), `images/base/tools.tsv` (PIC7 reads the contract's `critical` rows), and
  everything under `ee/`, `console/`, `mobile/`, `website/`, `plugin/` except PIC6's skill check.

### Order

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | PIC1 | nothing (the catalog type and its grammar) |
| 1 | PIC2, PIC3 | PIC1 (the type each carries); PIC3 also PIC1's `source` grammar |
| 2 | PIC4 | PIC3 (the copied Dockerfile and its bucket key), PIC2 (the node the entry is read from) |
| 3 | PIC5 | PIC1, PIC2 (the entries to choose among) |
| 4 | PIC6 | PIC1, PIC2, PIC4 (the entry a build writes a record for), PIC5 (the selection the CLI reports) |
| 5 | PIC7 | PIC4 (the smoke PIC7 widens) |

## Where each decision lands

| Decision | Task | Decision | Task |
| --- | --- | --- | --- |
| D1140 where the definitions live (**answered**: the published node) | PIC2 (the key and the keep rule), PIC3 (the pointer's target in the bucket) | D1150 build secrets (**answered**, **SEC**) | PIC4 |
| D1141 the catalog's relationship to shared images | no task — the note in [shared-images.md](../forge-authorship-images/shared-images.md), in **this** pull request | D1151 the runtime contract list | PIC7 |
| D1142 `checkout: baked\|clone\|shared` | PIC1 | D1152 storage, names, retention (**answered**) | PIC4 |
| D1143 `image ls` and the C8c collision | PIC6 | D1153 baked repos and the fetch-forward | PIC4 (the build), PIC5 (the launch) |
| D1144 `image build` | PIC6 | D1154 multi-repo and linked runs | PIC5 |
| D1145 `image add`/`rm`/`promote`, and the build-step collision | PIC6 | D1155 repo override and `needs:` precedence (**answered**) | PIC1 (the config), PIC5 (the selection) |
| D1146 the tag-cover algorithm and the refusal (**answered**) | PIC5 | D1156 the new key's skew | PIC2 (the node), PIC6 (the gate) |
| D1147 promote semantics | PIC6 | D1157 core or `ee/` | every task is core; no task touches `ee/` |
| D1148 scheduled security rebuilds (**open**, recommendation only) | PIC4 | D1158 the build release (**answered**: 0.16.0) | §"Release slot" |
| D1149 the `docker` tag's backend (**answered**) | PIC5 | D1159 (reserved) | — |

## Where each finding lands

| Finding | Task | Finding | Task |
| --- | --- | --- | --- |
| F1 images are per (repository, workflow) | PIC1 (the type sits above it), PIC4 (the build) | F9 the credentials service is not a build-secret broker | PIC4 (**SEC**) |
| F2 `image:` has no name | PIC1 | F10 the runtime contract is the selftest | PIC7 |
| F3 `dockerfile:` is one repo-relative path | PIC1 (the grammar), PIC3 (the fetch) | F11 the policy node's closed key set | PIC2 |
| F4 the daily check already rebuilds | PIC4 | F12 one run, one repository | PIC5 |
| F5 `internal/imagecheck` is C8a | PIC2 (the key is not reused), PIC6 (the command split) | F13 `image ls`/`add`/`rm`/`promote` are absent | PIC6 |
| F6 `checkout: shared` is refused | PIC1 (**D1142**) | F14 `catalogSince` is the *price* catalog | PIC6 |
| F7 the job's image is fixed at deploy time | PIC5 | F15 `promote` is a Cloud Build step | PIC6 |
| F8 no project-level registry for catalog images | PIC4 | F16 `docker` has no Cloud Run backend | PIC5 |

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test ./internal/cli/
-run Docs -count=1 -timeout 40m` when a doc changed; `go test ./internal/eeboundary/... -count=1`; and,
for PIC4 only, `go test -tags terraform ./internal/infra/...`, `terraform fmt -check -recursive
deploy/terraform`, and the `terraform` job's `init -backend=false -lockfile=readonly` + `validate` +
`terraform test` in each root it touches, with `TF_DATA_DIR` outside the tree. Never the whole
`internal/cli` or `internal/runner` package. No task adds a dependency, so `go.mod` is unchanged by all
seven.

## PIC1. The catalog type, its source grammar and the config surface (D1142, D1155, F1, F2, F3, F6, F13)

The catalog as a **config type**, with no cloud side yet: the entry (`Name`, `Tags`, `Source`,
`Description`), the three `Source` kinds (`dockerfile` = repo + path + optional branch, `inline` =
Dockerfile bytes, `image` = a prebuilt reference), the project's `default` marker, and the repository's
three references (`image: <name>`, `needs: [tags]`, and the existing override). `checkout: baked` stays
the default and the only kind a catalog reference works with; `clone` refuses a catalog reference the
way it refuses `image:`/`dockerfile:` today; `shared` keeps its own refusal (**D1142**, **F6**). A
repository that sets both an override and a catalog reference is refused, and the message names **both
keys** — the override's (`image`/`dockerfile`) and the catalog reference's (`image_name`/`needs`) — the
shape `image`/`dockerfile` mutual exclusion already takes (**D1155**, answered 2026-11-02).

- **Files (8):** `internal/config/catalog.go` (new: `Catalog`, `Entry`, `Source`, the validation, the
  default-marker rule, the name grammar), `internal/config/config.go` (`Workflow.ImageName`/`Needs`),
  `internal/config/image.go` (`validateCatalogRef`, and the `clone`/`shared` refusals' new arm),
  `internal/config/defaults.go` (the default marker), `schemas/fugaro.schema.json` (the new keys),
  `internal/config/catalog_test.go` (new), `internal/config/image_test.go`, `internal/config/schema_test.go`.
- **Red step:** each of these **fails today**, and for a stated reason:
  `TestCatalogEntryRejectsAnUnknownSourceKind` and `TestCatalogOverrideAndReferenceConflict` fail
  because the catalog-reference keys do not exist, so a `fugaro.yaml` that carries both an override and
  a reference cannot even be parsed — strict decoding errors before the refusal can be asserted
  (`TestWorkflowImageAndDockerfileStillConflict` is **not** a red step: that rule exists today and the
  test already passes); `TestCatalogReferenceWithCloneCheckoutIsRefused` fails because `clone` accepts
  any workflow today (`internal/config/image.go:322` refuses only the build keys, and has no catalog
  arm).
- **Tests:** `TestCatalogEntryRejectsAnUnknownSourceKind`, `TestCatalogDefaultIsOneEntryOrNone`,
  `TestCatalogNameGrammarIsUniqueAndBounded`, `TestCatalogDockerfileSourceNeedsRepoAndPath`,
  `TestCatalogReferenceWithCloneCheckoutIsRefused`, `TestCatalogReferenceWithSharedCheckoutIsRefused`,
  `TestCatalogOverrideAndReferenceConflict`, `TestCatalogSchemaAcceptsTheNewKeys`.
- **Reviewer:** opus.

## PIC2. The published node: `image_catalog`, the keep rule and the skew (D1140, D1156, F5, F11). **SEC**

The catalog travels as a **new top-level key of the published project policy node**, copied from the
local configuration `fugaro update` already holds, exactly as `verifier_accounts` does (**D1140**): the
key is added to `knownTopLevelKeys`, the local configuration gains the member and the **keep rule**
(this configuration's catalog when it states one, the published one otherwise), the key is
operator-owned in `sharedForbidden`, and the publisher **omits it when the effective catalog is empty**,
so a project with no catalog has a node byte for byte what it had (**D1156**). The skew note is the
`verifier_accounts` one in shape: a launcher on an older CLI **refuses** a node carrying the key
(`refuseUnknownPolicyKeys`), a runner tolerates it and logs it, so an installation that names a catalog
upgrades every launcher before it launches again. The image-catalog gate is spelled
**`imageCatalogSince`/`imageCatalogShipped`**, apart from the *price* catalog's `catalogSince`/`catalogShipped`
(**F14**). The C8a key, superset and lock are **not** touched (**F5**, **D1141**).

- **Files (9):** `internal/projectpolicy/policy.go` (the key, the type, `knownTopLevelKeys`),
  `internal/projectpolicy/policy_test.go`, `internal/localcfg/localcfg.go` (the member and the keep rule),
  `internal/localcfg/localcfg_test.go`, `internal/cli/sharedcfg.go` (`sharedForbidden`),
  `internal/cli/sharedcfg_test.go`, `internal/cli/project_cmd.go` (the refusal's sentence names the key),
  `internal/cli/project_cmd_test.go`, `internal/cli/recipes_skew.go` (`imageCatalogSince`/`imageCatalogShipped`).
- **Red step:** both of these **fail today**, and for a stated reason:
  `TestPolicyNodeOmitsAnEmptyCatalog` fails because the key does not exist, so the publisher has
  nothing to omit; `TestLocalConfigCatalogIsDecodedAndKept` fails because today's local configuration
  has no `image_catalog` member, so a config that carries a catalog is either dropped or refused by
  strict decoding. `TestOlderCLIRefusesANodeCarryingImageCatalog` is **not** a red step and is not
  claimed as one: the CLI already refuses a node carrying **any** unknown key
  (`refuseUnknownPolicyKeys`, `internal/cli/project_cmd.go:2836`), so that test passes before the task
  starts — it is kept in the list because it is the pin on the skew staying honest after the key is
  added.
- **Tests:** `TestPolicyNodeOmitsAnEmptyCatalog`, `TestPolicyNodeCarriesTheCatalogWhenSet`,
  `TestLocalConfigCatalogKeepsThePublishedOneWhenSilent`, `TestSharedConfigRefusesImageCatalog`,
  `TestOlderCLIRefusesANodeCarryingImageCatalog`, `TestImageCatalogGateIsNotThePriceCatalogGate`,
  `TestUpdatePublishesTheCatalogWithNoSecondCommand`.
- **Reviewer:** opus **plus the independent review a SEC task gets** — the node is a published contract
  every launcher reads, and a wrong keep rule silently drops an operator's catalog, the failure the
  `verifier_accounts` keep rule exists to prevent.

## PIC3. Fetching a Dockerfile and copying it into the bucket (D1140, F3, F9). **SEC**

Every Dockerfile is copied into the project's bucket **before** it is built, and the build reads it from
there — one read path and a record of exactly what was built ([spec.md](spec.md) §Where Dockerfiles
live). A **repo reference** (`repo:path`, optional branch) is fetched with the project's **existing**
GitHub credentials through `internal/gitprov`; no new access is added (**F9**'s boundary: the
credentials service's bundle is not widened). An **inline** definition is written from project state. The
copy is keyed under `builds/catalog/<name>/Dockerfile`, beside the layer copy
(`config.LayerCopyKey`), and written with the same read-compare-write-if-changed rule
`internal/cli/layer_copy.go` uses, so a republish does not churn the object. **SEC** because it handles
a repository credential and writes to the project bucket: the credential is used through `gitprov` and
never logged, the fetch is bounded, and the path is validated against traversal.

- **Files (10):** `internal/gitprov/gitprov.go` (a `Contents`-shaped read on the interface),
  `internal/gitprov/github/contents.go` (new: the contents API, bounded, credential from the existing
  source), `internal/gitprov/fake/fake.go` (the fake's file tree),
  `internal/catalog/fetch.go` (new: the resolver — repo reference vs inline — and the key),
  `internal/catalog/fetch_test.go` (new), `internal/config/catalog.go` (the key helper beside the type),
  `internal/cli/catalog_copy.go` (new: `writeCatalogDockerfile`, the `layer_copy.go` rule),
  `internal/cli/catalog_copy_test.go` (new), `internal/config/layer.go` (the key's prefix rule, read),
  `internal/backend/gcp/build.go` (the Cloud Build step that reads the copied Dockerfile by its
  `builds/catalog/` key — the read path PIC4's build consumes).
- **Red step:** `TestCatalogDockerfileIsCopiedUnderBuildsCatalog` fails (no key, no writer), and
  `TestCatalogFetchRejectsAPathOutsideTheRepository` fails (nothing validates the path).
- **Tests:** `TestCatalogDockerfileIsCopiedUnderBuildsCatalog`, `TestCatalogCopySkipsAnUnchangedDockerfile`,
  `TestCatalogInlineDefinitionIsWrittenFromState`, `TestCatalogFetchRejectsAPathOutsideTheRepository`,
  `TestCatalogFetchUsesTheExistingCredentialAndLogsNone`, `TestCatalogFetchIsBounded`.
- **Reviewer:** opus **plus the independent review a SEC task gets** (a credential path and a bucket
  write).

## PIC4. The build: Cloud Build reads the copy, the secrets mount, the smoke, the record (D1148, D1150, D1152, D1153, F4, F8, F9, F10). **SEC**

A catalog build is a Cloud Build run that reads the copied Dockerfile from the bucket (**PIC3**),
builds it with the entry's context, mounts the entry's **build secrets** as BuildKit **file** secrets
(**D1150**, **SEC**), runs **today's** runtime-contract smoke as it stands
(`image.SpecForCloud`, `internal/image/selftest.go:80` — **PIC7**, which depends on this task, is what
widens it to any base; this task runs the smoke that exists), and writes a build record the check
reads. The image lands in the installation's **shared registry**, named from the entry, tagged by
content hash with a moving `:latest` (**D1152**), under the registry's existing cleanup posture. The
daily check (**F4**) gains one trigger, `catalog-definition`, and reuses `base`, `lockfiles` and
`max-age` per entry — the scheduled security rebuild **D1148 recommends (open)**. **SEC** because it
mounts secrets:
the value is a BuildKit **file** secret, never an `ENV`, a build argument, a layer or a log — the rule
`_SECRET_ENVS` and `render.go`'s `secretMounts` already carry, and `images/scan.sh --secrets` enforces.

- **Files (9):** `internal/backend/gcp/build.go` (the catalog build's steps and `_SECRET_ENVS`),
  `internal/backend/gcp/catalog.go` (new: the build spec, the registry name, the record key),
  `internal/backend/gcp/catalog_test.go` (new), `internal/image/render.go` (the file-secret mounts for a
  catalog build), `images/derived/cloudbuild.yaml` (the steps a catalog build reuses),
  `internal/imagecheck/check.go` (the `catalog-definition` trigger), `internal/imagecheck/check_test.go`,
  `deploy/terraform/gcp/modules/installation/registry.tf` (the cleanup posture, read; changed only if the
  posture needs a catalog rule), `internal/backend/gcp/build_test.go`.
- **Red step:** `TestCatalogBuildMountsItsSecretAsAFileNeverAnEnv` fails (no catalog build exists), and
  `TestCheckTriggersOnACatalogDefinitionChange` fails (the trigger does not exist).
- **Tests:** `TestCatalogBuildReadsTheCopiedDockerfile`, `TestCatalogBuildMountsItsSecretAsAFileNeverAnEnv`,
  `TestCatalogBuildWritesARecordTheCheckReads`, `TestCheckTriggersOnACatalogDefinitionChange`,
  `TestCheckRebuildsACatalogImageOnABaseChange`, `TestCatalogImageLandsInTheSharedRegistryWithAContentTag`,
  `TestCatalogBuildRefusesWhenTheCopyIsMissing`.
- **Reviewer:** opus **plus the independent review a SEC task gets** (a secret path).

## PIC5. The selection: the tag-cover algorithm, the backend rule and the refusal (D1146, D1149, D1154, D1155, F7, F12, F16)

The tag-cover algorithm of [reconciliation.md](reconciliation.md) §3, in one place, **total and
deterministic**, and its four cases are all this task's:

- **a pin is not the algorithm** — a hand-pinned image, or a repository that names an entry with
  `image: <name>` (**D1155**), is used as it is; its tags are checked only to **warn**, and **D1146**
  (answered 2026-11-02) puts the warning at **deploy time**, where the job spec is built, not at launch;
- **an empty needed set picks the DEFAULT** — a repository that names nothing asked for nothing, so the
  entry marked `default` wins and the tag counts are never compared (without this the entry with the
  **fewest tags** would win, which is not the spec's "nothing: it uses the project default");
- otherwise the needed set is the union over the job's repositories, a candidate covers iff it has
  **every** needed tag, and among covering candidates the order is the **fewest tags**, then the
  **default** when it qualifies, then the **lexicographically first name**;
- **the `docker` rule is one rule** (**D1149**, answered 2026-11-02): on Cloud Run the candidate set is
  the catalog **minus every entry tagged `docker`** unless `docker` is needed, and a needed set that
  carries `docker` on Cloud Run has no candidate and is §3.3's second refusal. A VM (Cloud Batch) job
  keeps the whole catalog.

No candidate (with a non-empty needed set and no pin) is a **refusal before the job starts**, with
§3.3's exact message: the choice is made where the job spec is built (**F7**: the job's image is fixed
at deploy time), so it is a deploy-time decision, not a launch-time one. The rule is written for the
union over a job's repositories so it is correct for a multi-repo job the day one exists; **today the
union has one member** (**D1154**, **F12**).

- **Files (8):** `internal/catalog/select.go` (new: `Needed`, `Covers`, `Choose`, the two refusals),
  `internal/catalog/select_test.go` (new), `internal/infra/spec.go` (the image chosen through `Choose`),
  `internal/infra/checkjob.go` (the same for the check job), `internal/infra/spec_test.go`,
  `internal/backend/batch/run.go` (the `docker` tag's backend rule, beside today's `docker: true` refusal at `:124`), `internal/cli/run.go` (the refusal's
  placement), `internal/cli/run_test.go`.
- **Red step:** `TestChooseTakesTheFewestCoveringTags` fails (no selection), and
  `TestNoCoveringEntryRefusesBeforeTheJobStarts` fails (nothing refuses).
- **Tests:** `TestChooseTakesTheFewestCoveringTags`, `TestChooseTiesToTheDefault`, `TestChooseTiesByName`,
  `TestChooseIsOrderIndependent`, `TestNoCoveringEntryRefusesBeforeTheJobStarts`,
  `TestPinnedImageThatDoesNotCoverIsWarnedAndUsed`, `TestDockerTagOnCloudRunIsRefusedBeforeTheJobStarts`,
  `TestDockerTagOnTheVMBackendIsAllowed`, `TestSingleRepoJobNeedsOnlyItsOwnTags`.
- **Reviewer:** opus.

## PIC6. The CLI: `ls`, `add`, `rm`, `build`, `promote`, and the gate (D1143, D1144, D1145, D1147, D1156, F13, F14, F15)

The spec's four subcommands, mapped onto the existing family. `fugaro image ls` lists catalog entries
by default and the shared lock's entries under `--shared` (**D1143**, the C8c collision named in its
help). `fugaro image build [name]` builds the named entry (or all) when a catalog is published, and
keeps today's per-workflow meaning otherwise (**D1144**). `fugaro image add`/`rm` are the spec's, and
`image rm` **refuses** an entry a repository or a job still points at. `fugaro image promote <repo>
[name]` reads the repository's existing definition, writes the entry, and repoints the repository in the
same commit — never automatic, never on `fugaro update` (**D1147**); its help names the **Cloud Build
step** it shares a word with (**F15**). The command family's skew gate is **`imageCatalogSince`**
(**D1156**), not the price catalog's `catalogSince` (**F14**).

- **Files (9):** `internal/cli/image.go` (`newImageCmd`'s four additions), `internal/cli/image_catalog.go`
  (new: `ls`, `add`, `rm`), `internal/cli/image_catalog_test.go` (new), `internal/cli/image_promote.go`
  (new), `internal/cli/image_promote_test.go` (new), `internal/cli/image_build_catalog.go` (new: the
  catalog arm of `image build`), `internal/cli/image_build_catalog_test.go` (new),
  `internal/cli/recipes_skew.go` (the gate), `plugin/skills/working/SKILL.md` (the command the skill
  names, checked by `plugin/`'s tests).
- **Red step:** `TestImageLsListsCatalogEntriesAndTheDefault` fails (no `ls`), and
  `TestImageRmRefusesAnEntryAJobStillPointsAt` fails (nothing refuses).
- **Tests:** `TestImageLsListsCatalogEntriesAndTheDefault`, `TestImageLsSharedListsTheLockEntries`,
  `TestImageAddWritesAnEntry`, `TestImageRmRefusesAnEntryAJobStillPointsAt`,
  `TestImageBuildWithACatalogBuildsTheEntry`, `TestImageBuildWithoutACatalogKeepsItsMeaning`,
  `TestImagePromoteCopiesTheDefinitionAndRepointsTheRepo`, `TestImagePromoteIsNeverAutomatic`,
  `TestImageCatalogGateIsSpelledApartFromThePriceCatalog`.
- **Reviewer:** opus.

## PIC7. The runtime-contract check on any base (D1151, F10). **SEC**

The spec's §Runtime contract: "an image can be built from any base, as long as it meets the Fugaro
runtime contract… Fugaro does not check the `FROM` line… after each build, Fugaro runs a quick check
inside the image and fails the build if anything required is missing." Today's smoke
(`image.SpecForCloud`) checks the structural facts and the `critical` tools of `images/base/tools.tsv`,
which assumes a Fugaro base. PIC7 **widens** the check so it runs on **any** base: `git`, a shell and
the agent binary are asserted even when the image does not `FROM` a Fugaro base, the check reads the
contract's `critical` rows rather than a second hand-written list, and the `FROM` line is **not**
inspected (the spec says so in as many words). **SEC** because the check is the hardening gate — it
asserts non-root, no passwordless sudo, `tini` as PID 1 and the setuid/capability scan — and a widened
check must not weaken any of them.

- **Files (7):** `internal/image/selftest.go` (the any-base checks and the contract list read),
  `internal/image/selftest_test.go`, `images/derived/Dockerfile.tmpl` (the smoke's invocation),
  `images/derived/smoke_test.go` or its sibling (the smoke's own test), `internal/imagecheck/record.go`
  (the contract's hash in the record, read), `internal/backend/gcp/catalog.go` (the smoke's call site),
  `internal/image/render.go` (read).
- **Red step:** `TestContractCheckPassesOnANonFugaroBaseThatMeetsTheContract` fails (the check assumes
  a Fugaro base today), and `TestContractCheckFailsWhenGitIsMissing` fails.
- **Tests:** `TestContractCheckPassesOnANonFugaroBaseThatMeetsTheContract`,
  `TestContractCheckFailsWhenGitIsMissing`, `TestContractCheckFailsWhenTheAgentBinaryIsMissing`,
  `TestContractCheckDoesNotInspectTheFromLine`, `TestContractCheckStillFailsOnARootImage`,
  `TestContractCheckStillFailsOnPasswordlessSudo`, `TestContractListIsReadNotDuplicated`.
- **Reviewer:** opus **plus the independent review a SEC task gets** (the hardening gate).

## Shared files

Files more than one task edits, and what each does to them. A task that finds itself in one of these
for a different reason has taken a wrong turn.

| File | Tasks | What each writes |
| --- | --- | --- |
| `internal/config/catalog.go` | PIC1, PIC3, PIC5 (read) | PIC1 creates the type, the grammar and the validation; PIC3 adds the bucket-key helper beside it; PIC5 reads the entries and never writes them |
| `internal/cli/recipes_skew.go` | PIC2, PIC6 | PIC2 adds `imageCatalogSince`/`imageCatalogShipped` and their comment; PIC6 consumes the gate. The price catalog's `catalogSince`/`catalogShipped` are untouched by both (**F14**) |
| `internal/backend/gcp/build.go` | PIC3, PIC4 | PIC3 adds the copy's read path; PIC4 the catalog build's steps and its `_SECRET_ENVS` row |
| `internal/image/render.go` | PIC4, PIC7 (read) | PIC4 adds the catalog build's file-secret mounts; PIC7 reads the mounts to prove the smoke runs after them |
| `internal/imagecheck/check.go` | PIC4 | one task: the `catalog-definition` trigger, beside the existing ones. No other task edits it |
| `internal/backend/gcp/catalog.go` | PIC4, PIC7 | PIC4 creates it — the catalog build spec, the registry name and the record key; PIC7 adds the widened smoke's call site in it |
| `internal/infra/spec.go`, `internal/infra/checkjob.go` | PIC5 | one task: the image chosen through `Choose` at both call sites |
| `internal/projectpolicy/policy.go` | PIC2 | one task: the key, its type and `knownTopLevelKeys`. No other task edits it |
| `internal/cli/image.go` | PIC6 | one task: the four subcommands registered in `newImageCmd` |

## Live checks

**One, and it is named and not numbered.** The register's highest **written** section on `main` is
**Check 190**, open pull requests claim above it, and the register's own rule is that a claim above the
highest written section is settled at its merge ([docs/gcp-live-checklist.md](../gcp-live-checklist.md)).
So the task that writes this check takes the number free at **its** merge, and this plan writes it under
a name. It is **owner-run**: **no agent performs any step of it**, and it is **NOT RUN** until PIC1–PIC7
ship and the owner approves the plan.

**The named check — a catalog image built and a job launched from it — nine steps:**

1. Publish a catalog with two entries, one narrow and one universal, and mark the narrow one default.
2. `fugaro image ls` shows both, their tags and the default.
3. `fugaro image build` builds both; the images appear in the installation's shared registry, tagged by
   content hash with `latest` beside them.
4. A repository with `needs: [<a tag only the universal entry has>]` launches and its job runs the
   **universal** image, not the default — the tag-cover choice, observed.
5. A repository with no catalog reference launches and its job runs the **default**.
6. A job whose needed tags no entry covers **is refused before it starts**, with §3.3's message naming
   the missing tags.
7. A job whose image carries the `docker` tag on Cloud Run **is refused before it starts**, with §3.3's
   second message naming the backend.
8. `fugaro image promote <repo>` copies the repository's definition into the catalog and repoints the
   repository; a second run of `fugaro update` changes nothing (the migration is opt-in, not automatic).
9. The next daily check rebuilds only the entry whose definition changed, and the record names the
   trigger.

Cost: one sandbox project's build minutes and one run's spend; nothing created, enabled or deleted
outside the sandbox.

## Release slot

- **This design** merges whenever the owner answers it: it carries no code, and the owner's ruling puts
  it in **0.16.0** ([spec.md](spec.md) §Owner rulings).
- **The build is 0.16.0 too — the owner moved it earlier** (**D1158**, **answered 2026-11-02**: the
  build tasks start in 0.16.0). The design and the build land in the same release, so PIC1–PIC7 are
  0.16.0 tasks and nothing waits for 0.17.0, which was this folder's first recommendation.
  0.16.0 is the release [docs/c39-review-identity/](../c39-review-identity/) and
  [docs/console-settings-edit/](../console-settings-edit/) already fill, so the three share a window and
  their order inside it is the owner's. The catalog's `image_catalog` key and the shared-image operator
  steps (C8b/C8c, unscheduled) are **not** in the same window unless the owner puts them there.
- **`fugaro update` carries it whole.** An installation converges with the one documented command: the
  image pin moves, the catalog arrives in the code, and the catalog's own definitions are published by
  the same policy stage that publishes `verifier_accounts` — **no second command**, per the HARD RULE.
  The release's `### For operators` carries **one** entry from this work, and only for an installation
  that names a catalog: **upgrade every launcher before publishing a catalog** (PIC2's skew: an older
  CLI refuses a policy node carrying `image_catalog`). An installation that names none takes no step at
  all — the key is omitted when the effective catalog is empty.
