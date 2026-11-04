# Reconciliation: every claim of the design against the code, and the decisions this design needs

**Status: a design, and nothing is built.** The decisions are **D1260–D1277** (§4), each with a
recommendation and the cost of every alternative; **D1278–D1289 are reserved and unused**. **None of
them is answered** — this pull request asks, it does not decide (the issue's own ask is one paragraph,
and the owner has not ruled on any of the eighteen). **Where this file and [README.md](README.md) or
[spec.md](spec.md) disagree, this file wins.**

Reconciled against `origin/main` at **`b41709ea`**, on 2026-11-04, by reading the files rather than
the designs that describe them. Line numbers are that head.

**Re-read on 2026-11-04 after merging `b3ed1842`**, which moved two of the citations below
(`ProjectSecretCandidate` `spec.go:1344` → `:1356`, `projectSecretCandidates` `:1311` → `:1323`) and
nothing else this file reads; the line numbers here are the merged head's. The threat model (§3) was
written in the same round, and the config-management work's `fugaro secrets rm` — unscheduled when
this file was first written and now an open branch, #1691 — moved the plan's order (§2 of
[implementation-plan.md](implementation-plan.md)) and its shared-files table (§4 of it).

**The shape of the reading: the mechanism the issue asks for is already built, end to end, and the
only thing standing in its way is one branch of one predicate.** A project-wide secret today is a
container (`gcp.ProjectSecretID`), a per-repository eligibility answer
(`infra.ProjectSecretCandidate`), a live resolution that prefers the repository's own copy
(`resolveSecretID`), a per-repository grant and mount (`local.secrets` in the repository module), a
listing, a set of doctor checks, a store path with a hidden prompt, and a reach sentence the operator
reads before the value is stored. Nothing in that chain asks whether the name is a *reserved* name.
What asks is `projectWideAccepted`'s last branch — "a workflow's own secret has no project-wide
meaning" — and `ValidateProjectSecrets`'s key rule. So this design is a **gate design**: where the
per-name allow-list lives, what a name in it may be, and what must stay refused. The load-bearing
findings are F2 (the mechanism is name-agnostic), F7 (the grant shape exists), F9 (a shared job
cannot honour a per-repository list) and F16 (no reader class is added, so the brokering guard's
table does not move).

---

## 1. Findings: what exists

### F1. The refusal is the last branch of one function, and two functions word it

**Claim of the issue:** the "The problem" section — "`fugaro secrets set --help` says so: *A
workflow's own secret has no project-wide meaning and is stored in the repository's own container as
before.*"

**What the code does.** `internal/cli/secrets.go:759`'s `projectWideAccepted` is the whole
eligibility rule for the store path, read in four ordered branches: (1) a reserved name, through
`lc.ProjectSecrets[name]` or D2's sweep (`config.ProjectScopeByDefault`); (2) a model provider's own
key, accepted unconditionally; (3) a named Claude account's derived secret, through the account's own
`allow_data_to`; (4) **anything else — a workflow's own secret — `false`**, with `:780`'s
`projectWideRefusal` wording it: *"%s has no project-wide meaning: only a reserved secret (%s), a
model provider's key, or a named Claude account's token is stored project-wide"*. The help text the
issue quotes is `:308`, and the store-time note at `:859` repeats the rule to the person storing a
value (*"A workflow's own secret (workflows.<n>.secrets) cannot be shared this way and stays a
repository copy"*).

**Consequence.** The issue's ask is one clause added to branch 4 and one to branch 1's key rule —
but *where* the clause reads its answer from is the whole design, and §4 is about that and nothing
else. A design that only edited `projectWideAccepted` would ship a store path with no plan-time
predicate behind it: F3 is the function that decides the mount, and it must give the same answer.

### F2. Everything downstream of the name is already name-agnostic

**Claim of the design:** that the container, the labels, the Terraform wiring and the mount need no
new mechanism.

**What the code does.**

- `internal/infra/tfvars.go:852`'s `projectSecretContainers` builds the installation root's
  `project_secrets` map from `lc.ProjectSecrets` (every entry with a non-empty `allow_data_to`),
  `lc.Providers` and `lc.ClaudeAccounts` — and it never asks whether a name is reserved.
- `deploy/terraform/gcp/modules/repo/variables.tf:111` declares `project_secrets` as
  `optional(map(string), {})`, a plain logical-name → secret-ID map, and its own validation
  (`:186`–`:190`) only requires that a name a workflow mounts be in `repo.secrets` **or**
  `repo.project_secrets`.
- `deploy/terraform/gcp/modules/repo/workflows.tf:9` merges the two maps into `local.secrets`, and
  `module "workflow"` (`:17`) takes it as `secrets` — which is the map both `secret_key_ref` and the
  job account's `google_secret_manager_secret_iam_member` are built from.

**Consequence.** The feature's whole cloud-side cost is zero new resource types and zero new grant
shapes, exactly as `docs/design/project-secrets.md` §4 argued when it added the project container.
The design's job is to say so plainly rather than propose a mechanism the tree already has.

### F3. Eligibility is one exported function, and six call sites ask it

**Claim of the design:** that one predicate decides both "may be stored" and "may be mounted", so the
two cannot drift.

**What the code does.** `internal/infra/spec.go:1356`'s `ProjectSecretCandidate(lc, logical, repo)`
returns the project secret ID when the repository may mount a project copy of `logical`, and it reads
`config.AllowedForProjectSecret` (the `project_secrets` entry), `config.ProjectScopeByDefault` (D2's
sweep), `ClaudeAccountBySecret` and `ProviderBySecret` in that order. Its callers are
`spec.go:1323`'s `projectSecretCandidates` (the per-repository candidate map),
`internal/cli/secrets.go:1103`'s `repoMayUseProject` (the store-time note and the listing),
`internal/cli/initsecrets.go:424`'s `projectScope` and `:447`'s `find` (init's stage: where to store,
and what not to prompt for), `internal/cli/secrets.go:1267` and `:1341` (the listing paths) and
`internal/cli/doctor.go`'s scope checks.

**Consequence.** A new source of eligibility added *inside* `ProjectSecretCandidate` reaches all six
at once, which is the property this design must not break. A design that instead taught
`projectWideAccepted` about workflow secrets on its own would ship a store path and a plan that
disagree — the failure `initsecrets.go:414`'s own comment names (*"the stage can never store a value
somewhere the check above would not look for it"*).

### F4. The one place a workflow secret *is* name-shaped is the config validator

**Claim of the design:** that `project_secrets`'s key rule is the thing that must not be weakened
blindly.

**What the code does.** `internal/config/project_secrets.go:168`'s `ValidateProjectSecrets` gives
the block exactly one key rule (`P1`): *"The only key rule is membership in ReservedSecrets (P1)"* —
anything else is refused with *"is not a secret Fugaro stores project-wide: only \<the four\>"*. The
caller is `internal/localcfg/localcfg.go:1605`. The rest of the function is per-entry
(`invalidAllowDataToEntry`, `:227`) and the `proxy` refusal (D493).

**Consequence.** A block that accepts a workflow secret's arbitrary name loses the check that
catches a typo — for *every* name in it, the reserved ones included. D1260 and D1261 exist because
of this finding, and they are the only two decisions here whose alternatives differ in what the
validator can still catch.

### F5. Precedence is already "the repository's own copy wins", in one helper

**Claim of the issue:** "A repository's own copy would still win over the project's, as it does for
the reserved names."

**What the code does.** `internal/infra/readiness.go:207`'s `resolveProject` asks
`:628`'s `resolveSecretID(hasVersion, repoID, projectID)`, which returns the repository's ID unless
the repository's container has no enabled version *and* the project's does. `Readiness` then writes
`out.ProjectSecrets[logical] = cand` only in that case, and `workflows.tf:9`'s `merge()` prefers the
second map — so "the repo override is gone" is the only state in which the project copy mounts, and
the comment says so.

**Consequence.** D1264 needs no code: the property the issue asks for is the property the tree
already holds, for every name, and a design that changed it for workflow secrets would make one
class of name behave differently from another with no reader able to tell which.

### F6. The container's key set must never shrink, and the discovery union is what holds it

**Claim of the design:** that a hand-written entry that names nothing is a stray container rather
than a broken installation.

**What the code does.** `internal/infra/discover.go:486`'s `discovery.projectSecrets` unions every
correctly-labelled live container into the map the root plans, so "a name the current config no
longer makes eligible is never dropped from `for_each` — `prevent_destroy` turns that into a hard
`terraform plan` failure". `:528`'s `liveProjectSecrets` is the `List`; the per-name `Get` is what an
S2 pre-apply store needs, so a container `fugaro secrets set --project-wide` created before any plan
is adopted rather than conflicted with.

**Consequence.** An entry the operator typed wrong creates a container that is never mounted and
never destroyed. That is the cost D1260's recommendation is weighed against, and §6 states it rather
than hiding it.

### F7. The per-repository grant already exists and is name-agnostic

**Claim of the design:** that no new IAM resource type is needed.

**What the code does.** `workflows.tf`'s `module "workflow"` is placed with `secrets = local.secrets`
(`:17`'s map, `:27`), which is the map the workflow module's own `google_secret_manager_secret_iam_member`
and `secret_key_ref` read (`docs/design/project-secrets.md` §4: *"No new Terraform resource type for
IAM, in either module"*). A project container lives in the installation root, in a different state,
and the grant is written from the repository's own root against the ID string.

**Consequence.** Nothing in `deploy/terraform/` changes for this design. A task that finds itself
editing it has misread the design (see [implementation-plan.md](implementation-plan.md) §1).

### F8. The installation root cannot read a repository's `fugaro.yaml`

**Claim of the design:** that an entry cannot be validated against a workflow's declaration.

**What the code does.** `internal/localcfg/localcfg.go:1007`'s `Repo` holds `Provider`,
`BaseBranch`, `Workflows []string` (names), `GitHubAppID` and `Vertex` — no path, no checkout, no
parsed `fugaro.yaml`. `localcfg`'s project config is the operator's own file; a repository's secrets
block lives in the repository.

**Consequence.** No validator, no doctor check and no plan can answer "does some repository declare
this name" for the installation. D1261 cannot borrow a declaration check from `config.Validate`, and
D1271's answer (no new `validate` output) follows from the same fact.

### F9. A shared project job cannot honour a per-repository allow-list, and the code already refuses one that cannot

**Claim of the design:** that a profile may not mount a project workflow secret.

**What the code does.** `internal/cli/doctor.go:1611`'s own comment: a shared job *"mounts the
container its secret_env names, which is always the project-wide one (infra.ProjectSecretCandidate,
gcp.ProjectSecretID), so a repository copy does not reach it"*. And
`internal/projectpolicy/profiles.go:322` refuses a profile naming a Claude OAuth token with the
sentence that is this design's precedent: *"its allow_data_to is a per-repository list a shared job
cannot honour — a profile is shared by every repository admitted to it"*.

**Consequence.** D1266 has a shape to copy rather than a rule to invent. The accounts case (`accounts.go`
deriving `allow_repos` into the policy so a profile's list can only narrow it) is the *other*
possible answer, and D1266 costs it.

### F10. The credentials service's bundle is a closed set of kinds

**Claim of the design:** that a project workflow secret does not go into the bundle.

**What the code does.** `internal/credsvc/bundle.go:82` names the four kinds that are not model
credentials (the App key, the upstream Bitbucket credential, the Bitbucket read consumer, the
per-account review-signing keys), and `:392`'s `Bundle` is *"the service's whole credential set"*
with one writer (`fugaro creds set NAME`, C1.25) and one reader. `docs/secrets-brokering/`'s S3 is
the slice that would move workflow `secrets:` to a vault fetch (B3.1: *"the runner gets the
workflow's `secrets:` from the service at bootstrap; the job loses its accessors"*), and it is
**unscheduled**.

**Consequence.** D1267 is about not opening a second storage path for one credential before S3 is
scheduled, and about the sentence B3.1's task must carry when it is.

### F11. The secret-readers audit already lists the reader, so the brokering guard does not move

**Claim of the design:** that no new reader class appears.

**What the code does.** `docs/secrets-brokering/secret-readers.md` row **E1** covers the
per-repository job accounts reading *"the git credential …, the model credentials …, and the
workflow's own `secrets:`"*, through Cloud Run `secret_key_ref` mounts resolved by
`internal/infra/spec.go`'s `SecretMounts`; row **E5** covers a Batch VM reading the same names. The
reader *class* is unchanged by this design — what changes is which container the ID resolves to, and
`SecretMounts` already emits a logical name and a variable, never an ID.

**Consequence.** B0.2's guard, whose exceptions table keys on these IDs, needs **no new row** and no
edit. That is a finding the design states rather than a task it owes — and it is the reason §6 can
say the brokering work is untouched.

### F12. The allow-list matcher already does "wildcards within a segment", and one matcher serves both lists

**Claim of the issue:** "wildcards within a segment".

**What the code does.** `internal/config/project_secrets.go:66`'s `matchRepoPattern` splits both
sides on `/`, refuses a different segment count, refuses a wildcard in the owner segment, and calls
`config.MatchSegment` per segment — which `internal/projectpolicy/match.go:183` says the published
policy's `allow_repos` matcher shares, *"one matcher, one behaviour"*. `:227`'s `invalidAllowDataToEntry`
bounds an entry at two segments and two stars and refuses the bare `*`.

**Consequence.** D1276 is a "leave it alone" decision with a reason: a second matcher would be the
drift the shared one exists to prevent, and the narrowness `invalidAllowDataToEntry` carries
(literal owner) is the *right* narrowness for a credential one owner holds.

### F13. The allow-list is already a settings key, and `settings set` is not built

**Claim of the design:** that a future `settings set` becomes a second writer of the same decision.

**What the code does.** `internal/settings/registry.go:644` registers
`project_secrets.*.allow_data_to` (default `nobody`) and `:645` `project_secrets.*.proxy`, and
`schemas/settings.schema.json:962` publishes both. `docs/settings-commands/` designs the command;
`fugaro settings set` does not exist on `main`.

**Consequence.** The new block's `allow_data_to` owes a registry row and a schema key in the same
task that adds it, or the two blocks drift the day `settings set` lands. `implementation-plan.md`'s
W0 carries it.

### F14. Four documents, one help text and one skill state the opposite of this design

**Claim of the design:** that the docs are part of the change, not a follow-up.

**What the code does.** `SECURITY.md:28` (*"A name that has no project-wide meaning (a workflow's own
`secrets:` entry, or the default Claude account's `claude-oauth-token`) keeps the repository scope it
had"*); `SECURITY.md:38` (*"`workflows.<n>.secrets` cannot be shared this way at all: a workflow's
own secret stays a repository copy"*); `docs/project-layer.md:244` (*"`workflows.*.secrets` stays a
repository's own application secret, and a name with a project-wide meaning cannot be shared through
a workflow's block"*); `docs/design/project-secrets.md` §3 step 4 (*"Else: refused — never a
workflow's own secret, which has no project-wide meaning"*); `internal/cli/secrets.go:308` (the help
text the issue quotes) and `:859` (the store-time note); and
`plugin/skills/setup/SKILL.md:130` (*"Setup's prompts (step 9) have no `--repo` to pass: they store a
name project-wide, except a per-repository one"*).

**Consequence.** Every one of them is a sentence a person uses to decide whether storing a shared
value is safe, so D1272 and D1273 are load-bearing: the feature may not ship with them unchanged,
and each carries a test (the docs tests, and `plugin/`'s own gate on the skill's commands).

### F15. `fugaro validate` in a repository's checkout does read the operator's local config

**Claim of the design:** that a repository's own `validate` could, but should not, mention the
project copy.

**What the code does.** `internal/cli/validate.go:355`'s `providerProblems` returns early unless
`lc.Name == cfg.Project` — so a checkout whose `project:` names the operator's own project *does*
read the local config, and `CheckWorkflowProviderSecrets` already judges a workflow's secret names
against it.

**Consequence.** D1271 asks whether a project workflow secret joins that output, and answers no: the
list is operator-local and per-machine, so the same checkout would print a different note on a
colleague's machine — and a note whose presence depends on whose laptop ran it is worse than silence.

### F16. The store path creates the container, and the plan adopts it

**Claim of the design:** that the operator may store a value before the installation root has ever
planned the container.

**What the code does.** `fugaro secrets set`'s own help (*"creating the secret if it doesn't
exist"*), and `secretsSetProjectWide`'s label map at `internal/cli/secrets.go:845`
(`fugaro=managed`, `fugaro_project`, `fugaro_secret`) — byte-identical to the labels
`discovery.projectSecrets` demands before it adopts a container (F6). D1263 depends on this and adds
nothing.

### F17. The listing and the ALLOWED column read three sources, and the new block is a fourth

**Claim of the design:** that the listing needs an edit and no new column.

**What the code does.** `internal/cli/secrets.go:1145`'s `projectSecretAllowed` answers the ALLOWED
column from `lc.ProjectSecrets`, then D2's sweep, then `lc.ClaudeAccounts`, then `lc.Providers`; the
project-wide listing itself is a label `List` (`:1267`, `:1341`) and so already shows any container
this design's block creates. `secretEntry` (`:1082`) carries `Scope` and `Allowed`; the `--json`
shape is unchanged by adding a source, because both fields already exist.

**Consequence.** No wire-format change and no version bump: the new block is a fourth answer to two
questions the document already asks. That is deliberate and it is why D1270 is cheap.

### F18. The reserved names cannot collide, because the repository's own declaration cannot use them

**Claim of the design:** that a project workflow secret can never be a reserved name.

**What the code does.** `config.ReservedSecrets` (`internal/config/config.go:863`) is the four
platform-mounted names; `config.Validate` refuses a workflow declaring one, and the provider
convention names and the two harness OAuth namespaces are refused the same way
(`internal/config/providers.go` `CheckWorkflowProviderSecrets`, `docs/multi-model.md:43`). The
`creds-bundle` name is refused at `secrets set` (`credentialsBundleRefusal`,
`internal/cli/secrets.go:417`).

**Consequence.** D1261's list of refused names is not a new policy: it is the union of refusals the
tree already makes, restated in one place so the new block cannot become a way *around* them.

### F19. The wildcard's reach sentence already exists, and this design gives it a heavier subject

**Claim of the issue:** "a project copy never widens to a repository the operator did not list".

**What the code does.** `SECURITY.md:32`: *"A wildcard means every current AND future repository it
matches. A repository onboarded into that workspace later mounts this value on its next `fugaro
setup`, with no further edit to the list, and nobody is asked again — that is the point of writing
one entry instead of fifty, and it is the risk."*

**Consequence.** For a reserved name the subject is a model credential or a git token; for a
workflow secret it is whatever the repository's build and tests read — the 65 services' Nexus
account in the issue. D1273 rules that the sentence is written for the new subject rather than
inherited, and that the store-time note names the reach (F3's `repoMayUseProject`).

### F20. The number block, read by grep rather than inherited

**Claim of the design:** D1260–D1289 is free.

**What the code does.** See §5. On `main`, the highest written decision is **D1189**; **#1676**'s
branch writes **D1190–D1209** and names **D1219** as its block's top; the stateless-orchestrator
work (#1682) writes no decision yet and is to take **D1220–D1259**.

**Consequence.** This work takes **D1260–D1289**, writes **D1260–D1277** and reserves
**D1278–D1289**. A later task re-runs §5's commands rather than trusting this paragraph.

---

## 2. What this design leaves alone

- **`fugaro secrets set`'s value handling.** stdin-only, the hidden prompt, the length floor and
  ceiling, the trailing-newline rule, the refusal to take a value from an argument or a flag: none of
  it is touched, and no task in the plan may touch it. The issue is about *where* a value is stored
  and *who* may mount it.
- **The reserved names and their rules.** D2's sweep, `PerRepositorySecret`, the provider
  convention, the harness OAuth namespaces and the `creds-bundle` refusal stay exactly as they are;
  D1261 restates them, it does not amend them.
- **The precedence.** `resolveSecretID` is cited (F5) and never edited.
- **`docs/design/project-secrets.md`.** It is the parent design and its D-numbers are not this
  work's; the one line that changes is §3's step 4, and D1272 makes it a documentation edit with a
  pointer to this folder, not a rewrite.
- **`deploy/terraform/`.** No task edits a `.tf` file (F2, F7). If one finds itself needing to, it
  **stops and reports**.
- **The brokering guard and `secret-readers.md`.** F11: no new reader, no new row.
- **The bundle and the credentials service.** D1267. No task opens `internal/credsvc/`.
- **`fugaro managed apply`, the managed gate, the delete list and the run record.** Another design's
  (D1011–D1040). This design names none of them and adds no command to any gate.
- **The live checks.** No task adds, runs or edits one; `docs/gcp-live-checklist.md` is untouched.
  Every test in the plan is hermetic (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`).

---

## 3. The threat model

The three questions a security reviewer asks of this design before any decision is answered — what a
rotation reaches, what `doctor` says about a shadowed name, and who can read the value — each read
against the tree rather than against the shape of the code around it.

### ROTATION. One version add reaches every job the entry lists

**Claim.** A rotation is `secrets set`'s existing add-a-version path, and it reaches every repository
the entry names with nothing else to run: no apply, no re-grant, no prompt.

**What the code does.** The mount is by container, not by version:
`deploy/terraform/gcp/modules/workflow/job.tf:67` writes `version = "latest"` into the job's
`secret_key_ref`, so a job resolves the newest enabled version when its container starts and the
**next run** of every job that mounts the container reads the new value. The container's ID is
`gcp.ProjectSecretID(lc.Name, logical)` — the same ID whatever the name is (F2) — so a rotated
workflow secret travels the path the reserved names already travel. The grant is on the container
(`secret_accessor` on the secret, F7), not on a version, so it survives the rotation untouched.

**What a rotation does not reach, which is D1264's fact seen from the other side.** A repository that
holds its own copy with an enabled version resolves *its own* copy (`resolveSecretID`, F5), so
rotating the project's value leaves that repository on its own value — silently and by design.
`doctor`'s `secret-scope-shadowed` is where that is readable from a checkout, and the store-time note
and `secrets rm`'s own note say it at the moment a value is stored or withdrawn. So the issue's 130
rotation prompts collapse to **one** `secrets set` for the repositories the entry lists, and to
**nothing** for a repository that keeps its own copy: that one rotates its own, which is the cost of
keeping it.

### The doctor check. The two existing scope checks already iterate the candidate map

**Claim.** D1270 says **no new doctor check**. This is why, rather than an omission.

**What the code does.** `doctorRepoSecretsOf` walks every logical name a checkout's workflows declare
and asks `infra.ProjectSecretCandidate(lc, logical, repo)` for each (`internal/cli/doctor.go:1000`),
filling `facts.candidates` — the map `doctorSecretScopeChecks` iterates to build `secret-scope` and
`secret-scope-shadowed`. The predicate is the one function this design changes (F3), so the moment it
answers for a listed repository, both checks cover the name with **no doctor code change at all**:
`secret-scope` prints which copy a name resolves to (project scope, repository copy, both, or
neither), and `secret-scope-shadowed` names the repository copy that wins over the project's, with the
`secrets set --repo` command that fixes it. A new check could only restate either.

### Who can read the value

**Claim.** The repositories that can read a project-scoped workflow secret are exactly the ones the
entry's `allow_data_to` names, and the one place that set is computed is the **plan-time** candidate
map.

**What the code does — three steps, because the claim is only true as the three together.**

- **The grant is the job account's, per mounted secret.** `deploy/terraform/gcp/modules/workflow/sa.tf:196`
  writes `roles/secretmanager.secretAccessor` for `google_service_account.job` on
  `var.secrets[each.value]` — one member per secret the job mounts, at the secret level, never at the
  project's. The map it indexes is `local.secrets`
  (`deploy/terraform/gcp/modules/repo/workflows.tf:9`): the repository's own containers **overlaid
  with `var.repo.project_secrets`**, which is the plan's resolution and nothing else.
- **Only a repository the entry lists ever gets a project candidate.**
  `internal/infra/spec.go:1356`'s `ProjectSecretCandidate(lc, logical, repo)` is the single function
  that answers, and it is where an entry's list is read (F3). A name it refuses produces no entry in
  `repo.project_secrets`, so there is no ID for the grant to name.
- **Enforcement is that map, and it is not an independent Terraform check.** Nothing under
  `deploy/terraform/` reads `allow_data_to`: the module grants what the plan hands it. The boundary is
  therefore a **plan-time** one — a candidate the function returns becomes a grant, and a name it
  refuses never becomes one — and a reader who assumed a second, Terraform-side check exists would be
  assuming an enforcement point this design does not have. That is why W2, the task that changes the
  predicate, carries a **SEC** flag and is the task that changes who can read the value.

**What this does not claim.** A value mounted by a repository the entry lists is readable by
everything that repository's job is: its runs' agents, and its launchers. The allow-list decides which
repositories mount a value, never what a run does with it once mounted — the sentence `SECURITY.md`
already carries for the Bitbucket token, and §6 states for this subject rather than softening it.

---

## 4. Decisions

Each decision carries the question, the **recommendation**, and the cost of every alternative.
**None is answered**: this pull request asks.

### D1260. Where the allow-list lives

**Question.** Does a workflow secret's per-name allow-list live in the existing operator-local
`project_secrets` block, or in a second block of the same shape?

**Recommendation.** A **new operator-local block, `workflow_secrets:`**, whose entries are the same
`config.ProjectSecret` shape (`allow_data_to`, plus the reserved `proxy` switch refused by
`ValidateProjectSecrets`'s own rule). `project_secrets` keeps its rule that every key is a reserved
name; `ValidateProjectSecrets` (F4) is not weakened for any name, reserved or not; and the new block
gets the key rule the reserved block cannot give a workflow secret — every reservation the tree
already makes (F18), restated in one place. The two blocks are distinguished structurally, so a
reader never has to ask which kind of name a given entry holds.

**Cost of the alternatives.**

- **Relax `project_secrets` to accept a workflow secret's name.** Smallest diff (one function's key
  rule, one predicate's branch, no new field anywhere) and one block for the operator to learn. It
  costs the typo check *for every entry in the block*: today `ValidateProjectSecrets` refuses a name
  that is not one of the four reserved ones, which is the only thing that catches a misspelling
  before it becomes a container (F6). It also widens the meaning of a block whose whole documented
  identity is "the reserved names, per name" — `docs/project-layer.md:244` and `SECURITY.md:28` both
  read it that way. Rejected on the typo check, not on taste.
- **A per-entry `kind: workflow` field in `project_secrets`.** One block, and the entry itself says
  which kind of name it holds. It costs a second key rule anyway (the reserved names must refuse
  `kind: workflow`, and a workflow name must demand it), so the block ends up with two rules and one
  shape rather than one rule each; and a hand-written entry claiming a reserved name is a workflow
  secret has to be refused by hand, which is a state the two-block design cannot reach.
- **Publish the allow-list in the project policy instead of keeping it operator-local.** The reach
  would be readable by `fugaro doctor` everywhere and by the console. It costs the property the
  issue asks for in one word — *operator-local* — and it publishes, per arbitrary secret name, a
  list of repository names into a document every run token can read. Rejected.

### D1261. Which names the new block accepts

**Question.** What may a key of `workflow_secrets` be, given that the installation root cannot see
any repository's `fugaro.yaml` (F8) and so cannot check a declaration?

**Recommendation.** A name that passes `config.CheckSecretName`/`config.SecretNameRE`, and is refused
if **any** existing reservation claims it: a key of `config.ReservedSecrets`, a configured provider's
conventional name or its own `secret:`, either harness OAuth namespace
(`claude-oauth-token*`, `codex-oauth-token*`), or `config.CredentialsBundleSecret`. The refusals
reuse the sentences the tree already has (`providerConventionRefusal`, `codexOAuthRefusal`,
`credentialsBundleRefusal`) rather than wording new ones. **No declaration check**: a name no
repository declares is accepted and inert (§6).

**Cost of the alternatives.**

- **Demand a declaration, i.e. refuse a name no repository's workflow declares.** It would catch the
  typo F4's removal gives up. It is impossible where the validation happens (F8) and would be
  machine-dependent everywhere else — the same objection D1271 makes to a `validate` note, with the
  added cost of a check that passes on the operator's laptop and fails in CI.
- **Accept any name at all, including the reserved ones.** Shortest rule, no list to maintain. It
  costs the property F18 establishes: a `workflow_secrets.bitbucket-token` entry would be a second,
  unaudited way to move a git credential to project scope, and `workflow_secrets.creds-bundle` would
  mount the service's whole document into a job.
- **Accept a name only when it is *declared* in the same block, e.g. an entry naming the repository
  paths that declare it.** Explicit, but it duplicates a fact the repository already holds and can
  drift from it: the entry would say a repository declares the name, and the repository's file would
  say otherwise, with nothing to reconcile the two.

### D1262. Eligibility at plan time: does a workflow secret get a default?

**Question.** Does a workflow secret in the new block follow D2's sweep (`ProjectScopeByDefault`, a
name with no entry is project-wide for every repository), or the reserved-and-listed rule ("absent or
empty means nobody")?

**Recommendation.** **Absent or empty means nobody.** No sweep, for any workflow secret, ever. The
operator's entry is the whole gate, and a repository that no entry names mounts nothing.

**Cost of the alternatives.**

- **Sweep workflow secrets in.** A repository's declared workflow secret would resolve to a project
  copy with no entry at all, which is one fewer thing for the operator to write. It costs the
  issue's own guarantee — "a project copy never widens to a repository the operator did not list" —
  and it inverts the gate: the presence of a *value* would mount it everywhere, so an operator who
  stored a value for one repository would find it on sixty-five.
- **Sweep them in only when the name is declared by more than one repository.** Sounds cautious,
  and it is the worst of both: the installation cannot count declarations (F8), and the rule would
  change silently as repositories are onboarded — a mount appearing on the sixty-fifth repository
  because the first sixty-four declare the name is exactly the "widening nobody listed" the issue
  refuses.

### D1263. Where the value is stored

**Question.** A new container shape, or today's project container?

**Recommendation.** **Today's project container, unchanged**: `gcp.ProjectSecretID(lc.Name, name)`,
the three labels (`fugaro=managed`, `fugaro_project`, `fugaro_secret`, F16), `prevent_destroy`,
`projectSecretContainers` (`:852`) deriving it from the entry, and `discovery.projectSecrets`'
union (F6) adopting one the store path created first.

**Cost of the alternatives.**

- **A separate container namespace for workflow secrets** (a label or an ID prefix of its own). It
  would make the two kinds visible in a bucket listing. It costs a second adoption path in
  `discovery`, a second label set in the store path and in `doctor`, and a second `ProjectSecretID`
  domain — for a distinction no reader acts on, since eligibility and mounting are per name either
  way.
- **A per-repository container the plan shares by name.** It would keep the container next to the
  repository that first stored it. It costs the whole point: the value would be stored once per
  repository again, and the deletion of that repository would take the shared copy with it.

### D1264. Precedence

**Question.** Does the repository's own copy still win?

**Recommendation.** **Yes, unchanged** (`resolveSecretID`, F5). The store-time note and the `secrets
ls` scope column keep saying so, and the new block adds no exception.

**Cost of the alternatives.**

- **The project copy wins.** Then a rotation of the shared value would silently override every
  repository that had stored its own — including one that stored its own *because* it needs a
  different value — and `fugaro secrets set --repo` would become a command that appears to work and
  changes nothing. Rejected: a store that does not take effect is the failure mode the whole
  "repo override" rule exists to prevent.
- **Refuse a repository-copy store for a name the project also holds.** It would remove the
  shadowing footgun the note warns about. It costs a repository the ability to carry its own value
  where it deliberately differs (the plausible case: one service of the sixty-five has its own Nexus
  account), and it turns a note into a refusal — a tightening this design has no evidence for.

### D1265. The store path

**Question.** Which spellings of `fugaro secrets set` store a workflow secret project-wide, and what
does each require?

**Recommendation.** Three spellings, one decision function (F3):

- `fugaro secrets set NAME --project-wide` — accepted for a workflow secret when the new block has an
  entry for `NAME` with a non-empty `allow_data_to`, and refused with the entry's own field named
  otherwise (present-and-empty means nobody, exactly as for a reserved name). **No checkout
  required**, because the entry is the gate and a value stored before any repository declares the
  name is inert (§6).
- `fugaro secrets set NAME` (bare) — stores project-wide when the entry lists *this repository*
  (today's `projectScopeName` rule, through `ProjectSecretCandidate`), and this repository's own copy
  otherwise, with the note at `:859` naming which it was.
- `fugaro secrets set NAME --repo owner/name` — the repository's own copy, as today, with today's
  rule that the checkout's workflow must declare the name (`declares`, `:461`).

**Cost of the alternatives.**

- **Require a checkout for `--project-wide` too, and the declaration in it.** It would catch a name
  no workflow declares at store time. It costs the bootstrapping order — an operator who wants to
  store the Nexus value once and *then* onboard sixty-five repositories would have to onboard one
  first — and it does not catch the case it looks like it catches, because a name declared by one
  repository and needed by another passes.
- **Make the bare command from any checkout store project-wide whenever an entry exists, regardless
  of whether the entry lists this repository.** One fewer rule. It costs the property
  `projectScopeName`'s own comment records: a person in a checkout the entry excludes would write the
  shared copy that other repositories mount, from a repository that may not mount it.
- **A fourth spelling, e.g. `--workflow-secret NAME`.** It would name the kind explicitly. It costs
  a flag the CLI has to keep for ever for a decision the entry already makes, and it makes the bare
  command's answer ambiguous in a checkout where the entry lists the repository.

### D1266. Shared project jobs

**Question.** May a shared project job (`profiles.<p>.secrets`) mount a project workflow secret?

**Recommendation.** **No, and by refusal**: `projectpolicy`'s profile validation refuses a
`profiles.<p>.secrets` entry naming a project workflow secret, in the shape
`profiles.go:322` already refuses a Claude OAuth token with (F9) — *a profile is shared by every
repository admitted to it, so a per-repository allow-list is not one a shared job can honour*. The
refusal is written where the rule lives, so `publish`, the deployer and both readers of the node
hold one profile to one rule rather than a copy each.

**Cost of the alternatives.**

- **Derive the allow-list into the policy and require the profile's `allow_repos` to narrow it**, the
  way `accounts.<name>.allow_repos` is derived from `claude_accounts.<name>.allow_data_to`. It is
  the one alternative that lets the shared job mount the value, and it is a real precedent. It costs
  a new policy node keyed by **arbitrary, unbounded secret names** (every account name is bounded by
  `ClaudeAccountNameRE`; a workflow secret's is not), a policy version bump, a new derivation and its
  migration, and it publishes an operator-local list into a document every run token reads — for a
  feature (`profiles`) that is itself not yet deployed. Rejected on scope, and worth revisiting when
  shared jobs land.
- **Leave it alone: the profile is the boundary.** No code at all. It costs the guarantee the issue
  asks for — an entry naming three repositories, and a profile whose `allow_repos` names thirty,
  would mount the value on all thirty and nothing would say so.

### D1267. The credentials service

**Question.** Does a project workflow secret become a bundle entry, or wait for S3's vault fetch?

**Recommendation.** **Neither now.** The bundle is untouched: it is a closed set of kinds (F10), one
document, one writer (`fugaro creds set NAME`) and one reader, and a workflow secret is not one of
them. The design records the **handoff**: when B3.1 (S3) is scheduled, the vault fetch reads the
container the mount reads — `gcp.ProjectSecretID` of the logical name — so a repository that has
switched to the fetch mounts and fetches the same value, and this design's allow-list keeps deciding
the reach. Rows E1 and E5 of `secret-readers.md` then change their **exit slice** wording, not their
reach, and that is B3.1's edit.

**Cost of the alternatives.**

- **Add a fifth non-model bundle kind now.** The service would hold the credential and attach it,
  which is where the brokering plan is going anyway. It costs a kind, a route, an admission rule and
  an audit line before S3 is scheduled, and it means one credential with two storage paths for as
  long as S3 has not landed — the state the design exists to avoid.
- **Refuse a project workflow secret on an installation that has opted into the service.** It would
  keep the two from coexisting wrongly. It costs the feature for exactly the installations that care
  most about credential reach, for a conflict that arrives only at S3.

### D1268. The published policy

**Question.** Does anything about the new block reach the project policy?

**Recommendation.** **No.** The block is operator-local, like `project_secrets` (whose only published
derivations are the Bitbucket token's `allow_repos` and the accounts' `allow_repos`). The mount is
decided at plan time on the operator's own machine, the shared job case is refused (F9, D1266), and
nothing a run reads needs the list.

**Cost of the alternatives.**

- **Publish the reach** so `doctor` and the console can show it from any machine. It costs a policy
  version bump, a reader, and the property that the list is the operator's own — a published list of
  repository names per secret name is exactly what `SECURITY.md` is careful not to hand a run token.
- **Publish nothing but a boolean per name** ("this name is project-scoped"). It is a smaller
  disclosure. It costs a node whose only reader would be a doctor check, and it would be *wrong* for
  a repository not on the entry's list — which is the fact a run would want and the one the document
  cannot state per repository.

### D1269. The setup flow's secrets stage

**Question.** Does the stage stop prompting for a workflow secret the project copy covers?

**Recommendation.** **Yes, with no new logic**: `find()` (`initsecrets.go:447`) already asks
`ProjectSecretCandidate` and the project listing before it adds a name to `wanted`, and
`projectScope` (`:424`) already answers where the stage stores. The task is the **tests** that hold
it, not a change. The 130 prompts the issue counts are what this removes, and it removes them on the
stage the operator actually runs.

**Cost of the alternatives.**

- **Keep prompting, and let the operator answer.** It preserves the stage's "you are asked once per
  repository" feel. It costs the issue's whole motivation, and it leaves a repository copy behind
  that wins over the project copy for ever (F5) — the silent precedence flip the stage's own comment
  says prompting would cause.
- **Have the stage *offer* the project copy with a note.** It informs the operator. It costs a prompt
  inside a stage that runs unattended in a run (`initflow`'s `NeedsYou` path) and a sentence whose
  subject would be a list the stage cannot read for the other repositories.

### D1270. The listing, the doctor surface and the settings registry

**Question.** What does the operator see, and what does `settings set` owe?

**Recommendation.** `secrets ls --project-wide` and doctor's scope checks read
`ProjectSecretCandidate` and `projectSecretAllowed` (`:1145`), which gain the new block as a fourth
source — no new column, no `--json` field, no format version (F17). One settings registry row and one
schema key land in the same task that adds the block (F13). **No new doctor check**: a check that the
entry names a repository that declares the name would need every repository's `fugaro.yaml` (F8), and
one that reads only the checkout in hand would print a clean bill of health for the other sixty-four.
The stray-container case (§6) is stated, not checked.

**Cost of the alternatives.**

- **A new doctor check over the checkout in hand** ("this checkout declares a name the operator stores
  project-wide"). It is honest and cheap, and it is also the F15 objection: a check whose answer
  depends on which checkout doctor was run in, printed as if it were a fact about the project.
- **A check that lists every `workflow_secrets` entry and the repositories whose checkouts the machine
  has.** Broader, and it costs a check that is silent about the entries it cannot see — silence that
  reads as "fine".
- **Leave the settings registry alone** until `settings set` lands. It costs the day that command
  ships writing a `project_secrets.*.allow_data_to` row and no other, so the new block's list would be
  settable only by hand while the reserved one is settable by command — a difference nobody chose.

### D1271. `fugaro validate` in the repository's own checkout

**Question.** Does a repository's `validate` say anything about a project copy of one of its
workflow secrets?

**Recommendation.** **No new output.** `providerProblems` (`validate.go:355`) may read the
operator's local config, but the answer is per-machine (F15): the same checkout would carry a note on
the operator's laptop and none in CI or on a colleague's, which teaches a reader to distrust the
notes that do appear.

**Cost of the alternatives.**

- **A note naming the scope each of the repository's secrets resolves to.** It is the same
  information `doctor`'s `secret-scope` line gives, in the command a repository's own CI runs. It
  costs a machine-dependent note in a command whose output people diff, and it duplicates doctor.
- **A warning when a required name resolves to nothing.** That check exists already, in `secrets
  ls`'s MISSING column (F17), and it needs no local config to be useful.

### D1272. The documents that change in the same pull request

**Question.** Which sentences ship with the feature?

**Recommendation.** All of F14's, plus one addition: `SECURITY.md`'s project-scope section gains the
reach sentence for a workflow secret (D1273). Concretely — `SECURITY.md:28`, `SECURITY.md:38`,
`docs/project-layer.md:244`, `docs/design/project-secrets.md` §3 step 4 (a pointer to this folder,
not a rewrite), `internal/cli/secrets.go`'s `set` help (`:308`) and store-time note (`:859`),
`plugin/skills/setup/SKILL.md:130`, and this folder's own status lines. `internal/cli/docs_*_test.go`
pins each.

**Cost of the alternatives.**

- **Ship the feature and fix the docs later.** It costs the sentence a person uses to decide whether
  storing one value for sixty-five repositories is safe — the one thing `SECURITY.md`'s project-scope
  section exists to give them before they type the command. Rejected.
- **Rewrite `docs/design/project-secrets.md` in place.** It is the parent design of this one and its
  own record. It costs a reader's ability to see what changed and when, which is what the folder per
  design exists for.

### D1273. The reach sentence, for the new subject

**Question.** What does the operator read before the value is stored?

**Recommendation.** The store-time note (`:859`, whose shape F19 records) and `SECURITY.md` say it in
the shape the Bitbucket token's warning already has (`SECURITY.md:67`): the entry's list decides which
repositories mount the value; a wildcard entry means every repository it matches **now and later**, so a repository
onboarded afterwards mounts it with nothing else to do; **the value is whatever the repository's
build and tests read**, and a repository that stores its own copy keeps it. The note names the reach
through `repoMayUseProject`, never a fixed sentence, so an entry that lists three repositories says
three.

**Cost of the alternatives.**

- **One fixed sentence ("every enrolled repository").** Shorter, and it is what the sentence says
  today for a swept-in reserved name. It costs accuracy for exactly the entries this design is about:
  an entry that lists `myworkspace/*` and an entry that lists one repository would read the same.
- **Say nothing at store time and document it once in `SECURITY.md`.** It costs the reader who never
  opens `SECURITY.md` — and the store path is where the decision is made.

### D1274. The container's lifetime, and the entry that names nothing

**Question.** What happens to a container whose entry is later removed or was a typo?

**Recommendation.** Today's behaviour, stated rather than changed: the discovery union (F6) keeps it
planned, `prevent_destroy` keeps it, and it mounts nowhere. The design **says so** in the help and in
`SECURITY.md`'s new sentence — an entry creates a container whether or not a repository declares the
name — and leaves removal to the operator, who may delete the container by hand once no entry names
it.

**Cost of the alternatives.**

- **Create the container lazily, only when some repository's plan resolves a candidate.** It would
  make a typo cost nothing. It costs the store-before-plan order (F16) and the installation root's
  inability to see any repository's `fugaro.yaml` (F8): there is no later moment at which the root
  learns the name.
- **Make the entry refuse to exist unless a repository declares the name.** The D1261 alternative
  again, with the same impossibility.
- **Turn `prevent_destroy` off for the new block's containers.** It would make a mistake removable.
  It costs the property N3 exists for, on a container holding a credential: a config edit would
  destroy a value other repositories still mount.

### D1275. The `proxy` switch on the new block

**Question.** May an entry of `workflow_secrets` set `proxy: true`?

**Recommendation.** **Refused**, by `ValidateWorkflowSecrets`, with `ValidateProjectSecrets`'s own
sentence shape (F4's validator, D493: *proxy is the Bitbucket token's switch and nothing else's*). The refusal is
belt and braces — D1261 already refuses `bitbucket-token` as a key — and it is what keeps the
forwarding feature from acquiring a second spelling.

**Cost of the alternative.** **Allow it and ignore it.** No rule to write. It costs the failure
`ValidateProjectSecrets` names for the same field: *"a setting nothing reads, which is the failure
mode this block's checks exist to prevent — an operator who wrote it believes their installation is
proxied."*

### D1276. The `allow_data_to` entry rules

**Question.** Does the new block use the same entry rules and the same matcher?

**Recommendation.** **Yes, unchanged**: `invalidAllowDataToEntry` (`:227`) and `matchRepoPattern`
(`:66`), through `config.AllowedForProjectSecret`'s own shape. Owner segment literal, `*` inside a
segment, two segments, at most two stars, the bare `*` refused, case-folded.

**Cost of the alternatives.**

- **Reuse the published policy's `allow_repos` rules instead** (`projectpolicy.ValidPattern`: a bare
  `*`, `*/acme/*`, a length bound). It would be one rule for both lists. It costs the distinction
  `invalidAllowDataToEntry`'s own comment draws: this list is the repositories **one credential**
  reaches, and a wildcard owner is every workspace.
- **Allow a bare `*`** so an operator can share one value with the whole installation. It costs the
  issue's guarantee in one character, and it is the widening `project_secrets` refuses today.

### D1277. The release slot

**Question.** Which release carries the build?

**Recommendation.** **CLI 0.19.0**, and stated plainly: **the build is security-sensitive** — it
changes which repositories may mount a credential an operator stored once, and D1260 and D1261 are
decisions a reviewer has to weigh rather than a mechanic's choices. `docs/release-streams.yaml` has
the CLI at `current: 0.17.0`, `next: Ocelot` (0.18.0), so 0.19.0 is free and its name is unspent.

**Cost of the alternatives.**

- **0.18.0**, the managed-agent-converge theme's release. It is already carrying the M0–M9 build and
  `docs/config-management/`'s G0–G13. It costs a security-sensitive change riding in a release whose
  theme is a different one, with the review attention that implies.
- **Unscheduled.** Nothing to cost; recorded so a reader does not read the recommendation as a
  commitment.

---

## 5. The number block

**D1260–D1289 is taken above every `D<number>` on `main` and in every open work stream**, read on
2026-11-04 by grepping the trees rather than inheriting a neighbour's block.

- **On `main`**, at `b41709ea`, the highest *written* decision is **D1189**:
  `grep -rho "D1[0-9][0-9][0-9]" docs/ internal/ --include='*.md' --include='*.go' | sort -u | tail -1`
  prints `D1189`.
- **In the open work streams**: `gh pr list --state open` at the same moment lists thirteen pull
  requests. **#1676** (`fugaro/20261104-010247-fac5`) writes **D1190–D1209** and names **D1219** as
  its block's top — read from its own branch with
  `git grep -ho "D1[12][0-9][0-9]" origin/pr1676 -- docs/ | sort -u`, which prints D1190…D1209 and
  D1219 and nothing between D1210 and D1218. **#1682** (`docs/stateless-orchestrator`) writes no
  decision at all today (its tree holds one spec file), and the stateless-orchestrator work is to
  take **D1220–D1259**. **#1683** (`fugaro/20261104-025456-71d0`) and **#1672**
  (`fugaro/20261104-003102-845c`) write nothing above D1189 — both are run work, and #1672's only
  document is a backlog item.
- **The highest number claimed anywhere is therefore `D1259`**, or `D1219` if the
  stateless-orchestrator block is read as unclaimed, and the next free block above both is
  **D1260–D1289**, the thirty-wide width `docs/ee-reorg` took for D520–D549 and
  `docs/secrets-brokering` for D490–D519 before it.
- **This work takes D1260–D1289: D1260–D1277 are written here and D1278–D1289 are reserved and
  unused.** A later task re-checks this section's facts rather than its conclusion; the four
  commands above are the re-check.

---

## 6. The residual this design refuses to hide

- **An entry the operator typed wrong creates a container that nothing mounts** (§D1274, F6). The
  design cannot catch it before the container exists, because no validation can tie an entry to a
  repository's declaration (F8). It is stated in the help, and the container is inert: `prevent_destroy`
  and the discovery union mean it is never destroyed either, so the fix is a hand deletion once no
  entry names it.
- **The value reaches whoever mounts it, and a placeholder is a plateau.** A project workflow secret
  is mounted as an ordinary job environment variable, so every run's agent can read it out of the
  runner's own environment (`SECURITY.md`, "What one run's agent can reach"). That is already true of
  a repository copy; what changes is how many repositories one value reaches. The store-time sentence
  (D1273) is the only thing that tells the operator, and it cannot tell them what the value *is*.
- **A shared project job and this feature are mutually exclusive until shared jobs decide otherwise**
  (D1266). An installation that has both will find the profile's refusal first.
- **The credentials service's vault fetch (S3/B3.1) will move the read, not the reach** (D1267). Until
  it is scheduled, a project workflow secret is mounted and never brokered — and a repository copy
  wins over the project copy, so an installation that has opted into the service still carries the
  repository's own copy in its job.
- **None of this is a boundary against a hostile agent.** The allow-list decides which jobs mount a
  value, not what a run does with it once mounted; a run of an allow-listed repository can read it
  from its own environment and send it anywhere, exactly as it can today with a repository copy. The
  gate is a decision about reach, and `SECURITY.md` says as much or this design is wrong about its
  own subject.
- **Nothing here is verified against a live project.** No task in
  [implementation-plan.md](implementation-plan.md) runs a cloud call, and the 65-repository case the
  issue names is a scale claim this design takes from the issue rather than reproduces.
