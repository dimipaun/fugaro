package cli

import (
	"strings"
	"testing"
)

// TestDocsProjectImageCatalogDesign: docs/project-image-catalog/ must carry the
// owner's catalog spec verbatim, the reconciliation of that spec against the
// code, and the plan built on the two.
//
// The assertions are two lists per file, and the difference between them is the
// point.
//
// The first is made of sentences the documents state, each naming the control
// that carries a fact — the owner's own words in spec.md, each finding's
// Exists/Missing/Conflicts verdict, each decision's recommended answer, the
// tag-cover algorithm's three tie-breaks, the two exact refusal messages, the
// nightly trigger, the plan's red steps and its named tests. Emptying the
// control that carries one of them fails its own assertion, rather than passing
// on a heading that is still there.
//
// The second is structural anchors — headings, the file table's rows, the task
// labels and the pinned test names — and they are checked so each document keeps
// the shape its sibling folders' documents have. A mutation of a behaviour does
// not, and cannot be made to, fail on an anchor.
//
// The folder is a design: nothing in it is code, and this test pins the
// documents rather than any behaviour. It also pins the one relationship note
// the brief requires in docs/forge-authorship-images/shared-images.md, because
// this folder's README claims that note exists and a claim about another file is
// worth holding to the file.
func TestDocsProjectImageCatalogDesign(t *testing.T) {
	const dir = "docs/project-image-catalog"

	// The owner's spec, verbatim. The only permitted change is the substitution
	// of the two example project names, and the status line says so; the rest of
	// the assertions are the owner's own sentences, so an edit to the text fails
	// here rather than passing unnoticed.
	specPath := dir + "/spec.md"
	spec := readDoc(t, specPath)
	specSection := docSection(t, specPath, spec, "# Fugaro Project Image Catalog Spec (v1, lean)")
	projectImageCatalogMustContain(t, specSection, specPath, "fact", []string{
		// The one substitution, and the rule it follows.
		"names were replaced by the placeholders **Project A** and **Project B** before the text reached this",
		"no customer or organisation name appears in docs, PRs or",
		"no typo fixed, no wording edited",
		// The summary, which is the design's own starting point.
		"Job images move from repo-centric to project-centric.",
		"Repo-level images stay as an optional override.",
		// The non-goals, which the reconciliation leans on.
		"Automatic dependency detection or analysis of repos.",
		"Generating, composing or layering images automatically.",
		"Per-repo Terraform or a heavy per-repo init.",
		// The entry's fields and the three sources.
		"name: unique within the project, for example frontend, backend or universal.",
		"tags: a free-form list of what the image provides",
		"dockerfile: a path to a Dockerfile (in the project Git repo or a named repo), or",
		"inline: Dockerfile contents written straight into the catalog, or",
		"image: a reference to a prebuilt image (optional, if cheap to support).",
		"The project marks one entry as default.",
		// The repository's four options.
		"1. Nothing: it uses the project default.",
		"2. image: <name>: it uses that catalog image.",
		"3. needs: [tags]: Fugaro chooses an image that covers those tags.",
		"4. A repo-level override image, for the rare repo that doesn't fit the catalog.",
		// The selection rule, whose tie-breaks the reconciliation spells out.
		"Choose the catalog image with the fewest tags that still covers them all.",
		"If two images tie, use the default when it qualifies; otherwise pick by name.",
		"If no image fits, fail before the job starts. Say which tags are missing and suggest adding an image or a universal image.",
		"A user can pin an image for a job by hand; Fugaro warns if the pinned image doesn't cover the needed tags.",
		// Where a Dockerfile lives, and why.
		"Every Dockerfile is copied into the project's GCS bucket before it is built, and the build reads it from there.",
		"Fugaro fetches the file with the project's existing git credentials from the security service, at the repository's own provider (GitHub or Bitbucket); no new access is needed.",
		"The project repo is optional, so neither source depends on it.",
		// Build secrets, in the owner's words.
		"Build-time secrets, such as a private npm token, come from the project's security service.",
		"are never written into image layers",
		// The migration, which is opt-in.
		"Existing repo-level images are never converted automatically.",
		"`fugaro image promote <repo> [name]` opts in",
		// The runtime contract.
		"An image can be built from any base, as long as it meets the Fugaro runtime contract.",
		"Fugaro does not check the FROM line.",
		"After each build, Fugaro runs a quick check inside the image and fails the build if anything required is missing.",
		// Baked repos, storage and building.
		"Fugaro fetches the latest commits on top of the baked copy; ordinary commits never need a rebuild.",
		"Built images are stored in the project's own Google Artifact Registry",
		"A few recent versions are kept for rollback.",
		"A nightly job checks each image and rebuilds it only if its definition changed, its base image changed, or a baked repo's dependency files changed",
		// The CLI sketch.
		"fugaro image ls lists catalog entries, their tags and the default.",
		"fugaro image add <name> --tags ... --dockerfile <path> | --inline <file> adds an entry.",
		"fugaro image build [name] builds one image or all of them.",
		"fugaro image rm <name> removes an entry.",
		// The three open questions, which the reconciliation answers.
		"Where should catalog definitions live: Firebase project state, the project Git repo, or both?",
		"Should images be rebuilt on a schedule for base-image security updates?",
		"How should the docker tag map to execution backends (gen-2 Cloud Run or VM)?",
		// The owner's rulings, and the one that decides this work's order.
		"**CATALOG FIRST**.",
		"Dependency-set sharing stays documented as a later option and is **NOT cancelled**.",
		"C8a code or the old design; mark their relationship.",
		"a **DESIGN-ONLY** pull request now, aimed at **0.16.0**",
		"nothing is built until the owner approves the",
		"placeholders **Project A** and **Project B**, never those names",
		"never those names",
	})

	// The reconciliation: every finding's verdict, every decision's recommended
	// answer, and the two things the spec leaves unsaid — the tie-breaks and the
	// refusal's own words.
	recPath := dir + "/reconciliation.md"
	rec := readDoc(t, recPath)
	recSection := docSection(t, recPath, rec, "# Project image catalog: reconciliation against the code")
	projectImageCatalogMustContain(t, recSection, recPath, "fact", []string{
		// It governs where the owner's spec is silent.
		"Where this file disagrees with [spec.md](spec.md), this file wins.",
		// The block is taken above every number either the tree or an open pull
		// request holds.
		"this work holds **D1140–D1159**",
		"highest decision **written** on `main` is **D1139**",
		"**D1140** is free",
		// The findings, each with its verdict. The two the reconciliation turns
		// on are the deepest invariant and the one capability no Cloud Run job
		// can have.
		"**F1: Exists. Images are per `(repository, workflow)`, not per project.**",
		"**F2: Missing. `image:` is a build-customisation block with no name.**",
		"**F3: Conflicts. `dockerfile:` is one repository-relative path, and only the repository's own.**",
		"**F5: Exists, as `internal/imagecheck` (C8a).",
		"**F6: Conflicts. `checkout: shared` is refused outright today.**",
		"**F7: Exists. The job's image is fixed at deploy time, and Cloud Run cannot vary it per execution.**",
		"**F9: Conflicts. The credentials service is a closed-set model-credential bundle, not a build-secret\nbroker.**",
		"**F10: Exists, as `internal/image`. The runtime contract is the selftest, not a published list.**",
		"**F11: Exists. The published policy node has a closed known-key set and an operator-owned rule.**",
		"**F12: Conflicts. One run, one repository.**",
		"**F13: Missing. `fugaro image ls|add|rm|promote` do not exist.**",
		"**F14: Exists, as a naming collision. `catalogSince`/`catalogShipped` are the *price* catalog.**",
		"**F15: Exists, as a collision. `promote` is a Cloud Build step, not a CLI command.**",
		"**F16: Exists. The `docker` tag has no execution backend today on Cloud Run.**",
		// The seven answered decisions, each stated as answered and as the
		// recommendation it took, and the one the first round marked answered by
		// mistake, which is open.
		"**ANSWERED by the owner, 2026-11-02, as recommended: the PUBLISHED PROJECT POLICY NODE",
		"**ANSWERED by the owner, 2026-11-02, as recommended: a manual pin acts AT DEPLOY TIME and prints a WARNING",
		"the EXISTING WORKFLOW-SECRET PATH, and the credentials service is UNCHANGED",
		"the SHARED registry with the EXISTING retention",
		"an override and a catalog reference used together are REFUSED, and the error names BOTH keys",
		"the BUILD TASKS START IN 0.17.0",
		"ANSWERED by the owner, 2026-11-02: VM/Batch only, and there is ONE rule everywhere.",
		"**D1148** | Should images be rebuilt on a schedule for base-image security updates? (**open question 2**) | **ANSWERED by the owner, 2026-11-03: BUILD IT, IN 0.18.0.**",
		"**one project-level scheduled check** evaluates **every catalog entry**",
		"**each entry that fires gets one catalog build**: the **same build account**",
		"**No new account and no broader grant**",
		"**What is scheduled** (the owner's answer, 2026-11-03): the catalog's rebuilds are **one",
		"catalog build per entry that fires, as the **same build account**",
		// The catalog is what a baked image is built from, and it lifts no
		// refusal.
		"**The catalog is what a `checkout: baked` image is built FROM; it changes no checkout kind.**",
		// The tag-cover algorithm's four cases, its three tie-breaks and its
		// totality: a pin, the docker rule, the empty set's default, and the
		// order among covering candidates.
		"### 3.1 The algorithm",
		"**A pin is not the algorithm.**",
		"**The candidate set**",
		"**An empty needed set picks the default.**",
		"**Covering candidates**",
		"**fewest tags**",
		"**the default, when it qualifies**",
		"**the lexicographically first name**",
		"it makes the choice total",
		// The two exact refusals, which are the spec's "fail before the job
		// starts" in words a user reads.
		"### 3.3 The fail-before-start message",
		"no image in this project's catalog covers the tags this job needs:",
		"add an entry that covers the missing tags (fugaro image add <name> --tags postgres redis firebase-emulator), or add a \"universal\" entry that covers every tag this project uses",
		"this job's image is tagged docker, which needs a VM backend: Cloud Run gen-2 has no Docker daemon",
		// Promote is never automatic.
		"Nothing is converted unless the command is run",
		// The nightly triggers, including the one this work adds.
		"| `catalog-definition` (**new**) |",
		"| `max-age` (**exists**) |",
		"This is the **scheduled security rebuild** D1148 answers (2026-11-03)",
		// The skew is the verifier list's in shape.
		"the publisher writes NOTHING when the catalog is empty",
		// Core, not ee.
		"**Core (MIT), the whole catalog.**",
		// What a merger re-checks.
		"## 5. What a merger must re-check",
		"**The reserved name `environments`**",
	})

	// The plan: the seven tasks, their red steps, their named tests, and the
	// gates and slots that hold them.
	planPath := dir + "/implementation-plan.md"
	plan := readDoc(t, planPath)
	planSection := docSection(t, planPath, plan, "# Project image catalog: implementation plan")
	projectImageCatalogMustContain(t, planSection, planPath, "fact", []string{
		// Nothing is approved by being written down.
		"**Nothing here is approved to build.**",
		"nothing is built until the owner approves the",
		// A SEC task gets the independent review.
		"A task marked **SEC** gets the owner's independent review in addition to the usual one.",
		// The one blocking dependency, and the one this plan reads.
		"the catalog rides the mechanism `verifier_accounts` establishes",
		"**no task here builds",
		// The red step each task starts from.
		"**Red step:** each of these **fails today**, and for a stated reason:",
		"`TestCatalogEntryRejectsAnUnknownSourceKind` and `TestCatalogOverrideAndReferenceConflict` fail",
		"`TestWorkflowImageAndDockerfileStillConflict` is **not** a red step",
		"**Red step:** both of these **fail today**, and for a stated reason:",
		"`TestPolicyNodeOmitsAnEmptyCatalog` fails because the key does not exist",
		"`TestOlderCLIRefusesANodeCarryingImageCatalog` is **not** a red step and is not",
		"**Red step:** `TestCatalogDockerfileIsCopiedUnderBuildsCatalog` fails",
		"**Red step:** `TestCatalogBuildMountsItsSecretAsAFileNeverAnEnv` fails",
		"**Red step:** `TestChooseTakesTheFewestCoveringTags` fails",
		"**Red step:** `TestImageLsListsCatalogEntriesAndTheDefault` fails",
		"**Red step:** `TestContractCheckPassesOnANonFugaroBaseThatMeetsTheContract` fails",
		// The named tests the plan must make pass.
		"`TestChooseTiesToTheDefault`",
		"`TestChooseTiesByName`",
		"`TestNoCoveringEntryRefusesBeforeTheJobStarts`",
		"`TestDockerTagOnCloudRunIsRefusedBeforeTheJobStarts`",
		"`TestDockerTagOnTheVMBackendIsAllowed`",
		"`TestCatalogBuildMountsItsSecretAsAFileNeverAnEnv`",
		"`TestCatalogCopySkipsAnUnchangedDockerfile`",
		"`TestImagePromoteIsNeverAutomatic`",
		"`TestImageRmRefusesAnEntryAJobStillPointsAt`",
		"`TestImageCatalogGateIsSpelledApartFromThePriceCatalog`",
		"`TestContractCheckDoesNotInspectTheFromLine`",
		"`TestContractCheckStillFailsOnARootImage`",
		"`TestOlderCLIRefusesANodeCarryingImageCatalog`",
		"`TestLocalConfigCatalogKeepsThePublishedOneWhenSilent`",
		"`TestUpdatePublishesTheCatalogWithNoSecondCommand`",
		// The live check is owner-run and named rather than numbered.
		"**no agent performs any step of it**",
		"nine steps",
		// The release slot: the design in 0.16.0, the build in 0.17.0 (the owner's cut).
		"**The build is 0.17.0**",
		"it carries no code",
		// The one operator entry, and only for an installation that names one.
		"upgrade every launcher before publishing a catalog",
		"An installation that names none takes no step at",
	})

	// The amendment: a repository build context for a dockerfile entry. It is a
	// design, it decides nothing that is the owner's, and the assertions below
	// hold the controls it turns on — the amended sentence kept verbatim beside
	// its amendment, the record of what was built, the trigger the check reads
	// without cloning, the credential kept out of the image, the skew a new
	// member causes, and the tasks with their red steps and their order.
	ctxPath := dir + "/context-design.md"
	ctx := readDoc(t, ctxPath)
	ctxSection := docSection(t, ctxPath, ctx, "# A repository build context for catalog `dockerfile` entries")
	projectImageCatalogMustContain(t, ctxSection, ctxPath, "fact", []string{
		// A design, and not an approval.
		"**Status: design only. This is an amendment to [spec.md](spec.md), it is not approved, and no task in",
		"**Nothing here is approved to build.**",
		"marked **SEC** gets the owner's independent review in addition to the usual one.",
		// The amended spec sentence, kept verbatim, and the amendment beside it.
		"That is the issue's own requirement and it is what makes this amendment additive:",
		"Build context: a catalog entry can set it explicitly. By default, a repo-referenced Dockerfile uses",
		"**AMENDED AFTER REVIEW (this design):**",
		"**amended**: there is **no such default**.",
		// The numbers: no D taken, no finding added.
		"They are **not findings**",
		"and **this amendment takes no",
		// The four gaps, each read against the code.
		"**G1. The owner's spec asks for a context and the code has none.**",
		"**G2. The catalog build's context is the Dockerfile and nothing else.**",
		"**G3. \"One commit\" is not guaranteed today, and the commit is already load-bearing.**",
		"`--build-arg \"FUGARO_COMMIT=$$commit\"` and as",
		"`--label \"org.opencontainers.image.revision=$$commit\"`",
		"**G4. The removal works around G2 by hand.**",
		"*\"a catalog build never clones a repository into the build context\"*",
		// A1: the key, its own path rule, and no change for an entry without one.
		"### A1. `context:` on a `source: { kind: dockerfile }` entry",
		"gets `catalogContextMessage`, the same rules as the Dockerfile's path",
		"An entry with **no** `context` keeps today's meaning exactly",
		// A2: one commit.
		"### A2. The Dockerfile and its context come from **one commit**",
		"**One commit, named in",
		// A3: the clone outside the Docker build.
		"### A3. The context reaches the build by a clone **outside** the Docker build",
		"`git -c credential.helper='store --file=/creds/git-credentials'`",
		// A4: .dockerignore, and the checkout contract.
		"### A4. What the context holds, `.dockerignore`, and the checkout contract",
		"`.dockerignore` is read by Docker from the context root",
		"**does not call `finalize-checkout`**",
		// A5: the record and the trigger.
		"### A5. The record, and the rebuild triggers",
		"**`context_sha256`**, the identity of the context the image was built from.",
		"**The check never clones.**",
		"and never a checkout, never a blob tree, never a clone.",
		// A6: the bounds, and what a huge context costs.
		"### A6. Bounds, and what a huge context costs",
		"**The context has no bound in the code today and this amendment does not add one**",
		// A7: the node, the schema and the skew.
		"### A7. The node, the schema and the skew",
		"**`checkMemberNames` before the decoder runs**",
		"**Whether the policy `Version` moves is the owner's, and it is O1**",
		"the whole entry's leaf set is **ten**, and the registry's rows go **325 → 326**",
		// A8: what does not change.
		"### A8. What this amendment does not change",
		"**An entry without `context`.** Byte for byte today's build",
		// The D1208 pointer: no bake, and the original decision stands.
		"entry's own Dockerfile does the clone, the warm-up and a final `finalize-checkout`, and the engine",
		"is a reason against a bake, not",
		// The security analysis, as an attacker would see it.
		"### 4.1 Who can change the context's contents between the pin and the build",
		"### 4.2 One commit means one tree",
		"the Dockerfile cannot come from a newer commit than the context",
		"### 4.3 A compromised repository already controls its Dockerfile",
		"**A context adds no new principal and no new",
		"### 4.4 The credential stays out of the image",
		"mode **0600**, inside the `/creds` directory the `prep` step makes 0700 and owns as the image's user",
		"### 4.5 What the amendment does not widen",
		// The tasks, their red steps and their order.
		"**Red step.** `TestCatalogContextAcceptsTheRepositoryRoot` fails",
		"**Red step.** `TestCatalogCopyResolvesTheRefToOneCommit` fails",
		"**Red step.** `TestCatalogContextStepClonesAtThePinnedCommit` fails",
		"**Red step.** `TestCheckTriggersOnAContextChange` fails",
		"**Depends on.** C1 (the field) and C2 (the commit).",
		"**Critical path:** C1 or C2 → C3 → C4.",
		"**Shared files.** No level has two writers of one file.",
		// The live check is owner-run, and the release carries no second command.
		"**no agent performs any step of it**",
		"**no second command**, per the HARD RULE.",
		// The four open items, and not one of them decided.
		"### O1. Does the policy `Version` move?",
		"### O2. How does the nightly check learn that the context moved?",
		"### O3. Is the context bounded, and where?",
		"### O4. Which release carries this build, and in what order against the removal?",
		"**Recommendation: it does not.**",
		"**Recommendation: the precise read**",
		"**Recommendation: bounded where the CLI resolves it**",
		"**Alternative: the context rides `max-age`.**",
		"**Alternative: no bound.**",
	})
	projectImageCatalogMustContain(t, ctxSection, ctxPath, "structure", []string{
		"## Numbers this amendment holds",
		"## 1. The gaps (G1–G4)",
		"## 2. The amendment (A1–A8)",
		"## 3. What of the spec this amends, verbatim, with the original kept visible",
		"## 4. The security analysis, as an attacker would see it",
		"## 5. The build tasks (C1–C4)",
		"## 6. Parallel execution",
		"## 7. The live check",
		"## 8. Release actions",
		"## 9. Open items (O1–O4) — the owner's, and none is decided here",
		"## 10. What this amendment is not",
		"### C1. `context` in the source grammar, and its row",
		"### C2. One commit: the copy resolves the ref and reads at it",
		"### C3. The clone step, the `--file` build, and the record's field",
		"### C4. The trigger, and the check's read",
		"### 3.1 [spec.md](spec.md) §Where Dockerfiles live, last bullet — **amended after review**",
		"| L0 | C1, C2 |",
		"| L1 | C3 |",
		"| L2 | C4 |",
	})

	// The README: the status, the two releases, the relationship to the shared
	// images, the numbers and the file table.
	readmePath := dir + "/README.md"
	readme := readDoc(t, readmePath)
	projectImageCatalogMustContain(t, readme, readmePath, "fact", []string{
		"**Status: design only; nothing here is approved and no code follows from this folder.**",
		"**Release: the design is 0.16.0, the build is 0.17.0**",
		"then cut 0.16.0's scope and moved it to 0.17.0.",
		"**Answered by the owner, 2026-11-02** — seven decisions, each as the recommendation:",
		"**D1140** (catalog definitions live in the published policy node under the key `image_catalog`",
		"**The owner answered D1148 on 2026-11-03: build the",
		"**D1148** (scheduled security rebuilds, on\n**2026-11-03**: build them, in 0.18.0).",
		"**D1149** (VM/Batch only, and one rule everywhere: on Cloud Run a docker-tagged entry is",
		"decisions of D1140–D1159 are **open**",
		"**Answering a decision is not approving a task**",
		// The relationship to the shared-images design, which is the one note
		// this work is allowed to add to that folder.
		"**The owner's ruling of 2026-11-02 is CATALOG FIRST.**",
		"stays **documented as a later option and is NOT cancelled**",
		"the relationship is **marked** in\n[shared-images.md](../forge-authorship-images/shared-images.md)",
		// The folder exists because the documents have different roles.
		"**spec is kept verbatim** as its own file",
		"the two example project names become **Project A** and **Project B**",
		// The citation convention, and the numbers.
		"**How a `:line` in this folder reads.**",
		"| **D1140–D1159** | this work's block.",
		"**F1–F16** are this folder's own",
		// The amendment, and the one thing it takes from nothing: the folder's
		// D-block and its findings stay exactly what they are.
		"**The amendment in [context-design.md](context-design.md) is reconciled against",
		"It takes **no `D` number** and adds **no finding**",
		"**The amendment does not edit the three files above.**",
		"**The amendment takes no `D` number and adds no finding**",
		"**Nothing in it is approved to build**",
		"**It touches the removal's order and not its decisions.**",
		// What the folder is not.
		"**Not code.** It is a design",
		"**Not a multi-repo job.**",
		"**Not a lift of the `checkout: shared` refusal.**",
		"One run, one repository is the deepest invariant in the code today",
	})

	// The structure: the headings and labels a reader navigates by, and the
	// three files the README's table links.
	projectImageCatalogMustContain(t, readme, readmePath, "structure", []string{
		"| [spec.md](spec.md) |",
		"| [reconciliation.md](reconciliation.md) |",
		"| [implementation-plan.md](implementation-plan.md) |",
		"| [context-design.md](context-design.md) |",
		"## How this relates to `docs/forge-authorship-images/`",
		"## The amendment: a repository build context",
		"## Numbers this work holds",
		"## At merge, and before implementation",
		"## What this folder is not",
	})
	projectImageCatalogMustContain(t, planSection, planPath, "structure", []string{
		"## Dependencies",
		"## Where each decision lands",
		"## Where each finding lands",
		"## Gates (every task)",
		"## PIC1. The catalog type, its source grammar and the config surface",
		"## PIC2. The published node: `image_catalog`, the keep rule and the skew",
		"## PIC3. Fetching a Dockerfile and copying it into the bucket",
		"## PIC4. The build: Cloud Build reads the copy, the secrets mount, the smoke, the record",
		"## PIC5. The selection: the tag-cover algorithm, the backend rule and the refusal",
		"## PIC6. The CLI: `ls`, `add`, `rm`, `build`, `promote`, and the gate",
		"## PIC7. The runtime-contract check on any base",
		"## Shared files",
		"## Live checks",
		"## Release slot",
	})

	// The one relationship note this work adds to the shared-images design: the
	// catalog comes first, the dependency-set sharing stays a later option and
	// is not cancelled, and C8a is not touched. The folder's README claims this
	// note exists, so the claim is held to the file it names.
	sharedPath := "docs/forge-authorship-images/shared-images.md"
	shared := readDoc(t, sharedPath)
	projectImageCatalogMustContain(t, shared, sharedPath, "fact", []string{
		"**Relationship to the project image catalog (owner's ruling, 2026-11-02): CATALOG FIRST.**",
		"stays a **later option and is NOT cancelled**",
		"stays in the tree and is not touched by the catalog work, and C8b/C8c stay unscheduled",
		"[docs/project-image-catalog/](../project-image-catalog/)",
	})
}

// projectImageCatalogMustContain fails the test for each want absent from the
// document, so a mutation of any single control is one failing assertion rather
// than one indistinguishable failure. part names which list the string came
// from, so a failure says whether a fact or the document's structure went
// missing.
func projectImageCatalogMustContain(t *testing.T, got, path, part string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("the design's %s is missing %q (%s)", part, w, path)
		}
	}
}
