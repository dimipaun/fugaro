# Launching a run for a repository without its checkout: reconciliation

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task approved.**

**Where this file disagrees with [spec.md](spec.md), this file wins.** Every claim below was re-checked
against `origin/main` as merged into this branch at `1625859c` (2026-11-04), by reading the code
named, not by trusting a neighbouring document. **#1477 has merged**, so the refusal this design
answers is on `main`; the first re-check, of 2026-11-01 against `432623d9c`, read it as still open, and
F1 is the finding that changed. The owner's answers of 2026-11-04 are in §3.

## 1. Findings against the tree

**F1. The refusal this design answers is on `main`.** `launchProjectJobFor` (`internal/cli/project_job.go`)
refuses a launch whose working directory holds no checkout of the repository, once the project's
published policy carries at least one profile:

```go
co := checkoutConfig(ctx, spec.Repo)
if co == nil {
    p, found, err := e.policy.read(ctx, e.lc)
    if err != nil || !found || p == nil || len(p.Profiles) == 0 {
        return nil, nil
    }
    return nil, userErr("fugaro run --repo %s cannot tell which Cloud Run job it targets: the working directory is not %s's checkout, or its fugaro.yaml could not be read (see fugaro validate). ...")
}
```

So this design answers the tree and not a pull request, and every red step in the plan is red against
`main` as it stands. The earlier reading — that #1477 was open, that the plan's first task either
waited for it or carried it — is withdrawn.

**F2. `co == nil` still conflates two causes, and the merged message gives one way out for both.**
`checkoutParse` (`internal/cli/cloud.go`) returns nil both when `checkoutRoot` names no checkout and
when the checkout exists but `readFugaroYAML` fails — the tree has no `fugaro.yaml` (an operator on a
branch whose tree predates the file, which is exactly the case the checkout's own objects can answer).
The merged message names both causes, which the single-cause text it replaced did not, and it is
therefore no longer *false* for either. What it still does is answer both with "run it from `%s`'s
checkout" — advice that is where the operator already is in the second cause, and a refusal of a
launch the local `git cat-file` could decide. F2 is a reason for D909, not a claim about the message.

**F3. `repoProvider` panics on a nil `co` when the local config has no entry.**
`internal/cli/project_job.go:243` is `if r, ok := e.localRepo(repo); ok && r.Provider != "" { return
r.Provider }; return co.Git.Provider`. Any code that runs *because* there is no checkout must not call
it in that form. In practice `repoSlug` (`internal/cli/cloud.go`) has already refused a launch whose
provider is unknown ("cannot tell the git provider of %s: set provider in its repos entry of the local
config, or run from its checkout"), so the provider is known — but the call must be reordered or
split, and that is a test in the plan.

**F4. The Bitbucket narrowing is provable from the code, not from a policy.**
`infra.SharedJobProvider` (`internal/infra/projectjobs.go:312`) is a list holding `github` and nothing
else, with a test that walks it against `config.Providers`; and `launchProjectJobFor`'s own gate is
`provider != pr.Provider || !infra.SharedJobProvider(pr.Provider)`. That gate sits *after* the checkout
lookup and after the `co == nil` refusal, so it is unreachable when there is no checkout: a Bitbucket
repository with no checkout is refused for a question that has exactly one answer.

**F5. A workflow's two keys come from different layers, and that is why no default can be guessed.**
`docs/project-layer.md`'s key table gives `workflows.*.checkout` the layers `profile, repo` — the
project layer may make a workflow a clone workflow — and `workflows.*.job_profile` `repo` alone: only
the repository's own file names the profile. Without that file the launcher can know a workflow clones
and still not know which shared job it runs on, and the default (`standard`) is wrong exactly where
this matters, since a project publishes profiles because its repositories name them.

**F6. The credential the owner's answer names exists; the reader and the launcher's identity do
not.** The **person-class read mint** shipped in 0.18.0 (GitHub: #1646, #1669; the Bitbucket twin
beside it, BB4):

- `internal/credsvc/githubread.go` — the route `/v1/github/read-token`, `VerifyPersonCaller` against
  the installation's own people list, the permission set `github.CoordinatorTokenPermissions`
  (`contents` and `pull requests`, read), the signing key from the service's bundle and nowhere else,
  the repository resolved through the installation's inventory and refused by name when it is not
  held, one audit line per mint (`PurposeGitHubRead`).
- `internal/gitprov/github/serviceread.go` — its client, `ServiceReadTokens`, a `gitprov.TokenSource`
  with `URL`, `Repository`, `AppID` and `IDToken`, and two refusals (`ErrServiceRead`,
  `ErrServiceReadIdentity`). The App ID travels in the body and is not a secret (`localcfg.Repo`'s
  `repos.<repo>.github_app_id`, the same number the platform passes to a job).

What does **not** exist: a file-at-a-ref reader — `gitprov.Provider` (`internal/gitprov/gitprov.go`)
has **no** file-read method, by design, since a Bitbucket credential cannot be widened and the
interface should not grow a capability one provider has — and the launcher's own person identity (F13).
The `contents:read`-only set the recommendation asked for is no longer needed: the mint is the
service's, it asks GitHub for its own read-only set, and its client has no field in which to ask for
another.

**F7. The recorded job needs no new storage.** `internal/runstore/launch.go` records `Job` in
`launch.json`, and `existingLaunch` (`internal/cli/run.go`) already backfills that record from
`result.json`'s execution when the CLI died before writing one — `ParseExecution(rec.Execution)` and
`l.Job = id.Job`. Source 2 of the spec is therefore a read of something already written, on a path
the command already walks.

**F8. Reuse of a recorded job cannot widen, and can only fail.** The runner re-decides both axes at
run start: `checkJobProfile` (`internal/runner/project.go:162`) halts a run whose job's profile is
not its workflow's, and `checkProjectJobAccount` (`internal/runner/projectjob.go:406`) does the same
for the account; `ProjectJobOfPolicy`/`matchJobName` refuse a name outside the project's policy and
the repository's own jobs. A per-repository job carries no profile and nothing is compared (R2-9),
which is also why a failed read falling back to one would go unnoticed by the runner — the argument
in D905.

**F9. Every console launch is from outside every checkout, so the refusal breaks the GUI on any
migrated installation.** The `run.launch` child's working directory is the app's own private, empty
directory (`console/src/main/launch.ts:828`; the directory is `privateWorkingDirectory`,
`console/src/main/spawn.ts:1615`), deliberately — "a launch spawned in a checkout could take its ref
from whatever the operator happens to have checked out, invisibly". So `checkoutRoot` is always
empty for the child, `co` is always nil, and on an installation whose policy defines a profile every
console launch of every repository is refused. `--ref` is on `NEVER_REACHABLE` in that schema, so a
console launch is always at the base branch and can never be influenced by a branch.

**F10. The credentials service can serve a launcher — as a person, which is the owner's answer to
D902.** The first reading of this finding said the service binds a mint to a **JobAccount** and a
launcher is therefore not an admitted caller. That is true of the run's service mint
(`POST /v1/github/installation-token`); it is not true of the read mints since 0.18.0, which admit a
**person** (`VerifyPersonCaller`, the installation's people list) and deliberately refuse the
coordinator job on the GitHub route (`internal/credsvc/githubread.go`). The caller class the owner's
answer needs therefore exists. What the service still will not do: mint for a repository the
installation has not onboarded (refused by name), mint for a caller outside the people list, and —
the limit that matters here — answer at all on an installation that has not opted into the service,
which is dormant until `fugaro update --provision-credentials` is run by the owner. Bitbucket's read
mint exists (BB4) and nothing here needs it: no shared job serves Bitbucket (F4), so source 3 is never
reached for one.

**F11. `docs/checkout.md`'s remedy for the gap is narrower than the gap, and `docs/project-layer.md`
still describes the old behaviour.** `docs/checkout.md`'s "Known gaps" now describes the refusal as
it is on `main` — that bullet is current — and names the remedy "falling back to the deployed job's
own `FUGARO_CHECKOUT` (or to its image being `base_images.base`)". That answer decides only the
**kind**: clone or baked. The job also needs the workflow's `job_profile`, which the deployed job's
own environment cannot name and only the repository's file carries (F5), so the remedy in that page
cannot close the gap it describes. `docs/project-layer.md` still says "A launch that cannot read the
repository's own workflows — `fugaro run --repo …` from a machine with no checkout of it — keeps the
per-repository job, as it does today", which the merged refusal made false. Neither file is edited by
this design: `docs/checkout.md` is B1's after the code lands, and `docs/project-layer.md` is another
document's to fix. Both are reported here and in the plan's D1 rather than changed now.

**F12. The doc tests that can bite these files.** No test enumerates `docs/`'s directories, so a new
folder is free; but three `WalkDir`-style tests read every page — the compute-backend test (refusing a
retired port literal anywhere under `docs/`), the mobile-telemetry test (a decision number's block must
be reserved by exactly one design and must be free on `main`), and `docs_live_init_census_test.go` (a
retired command spelling may appear only in dated records and a named list). This design's files use no
retired literal, no price, no secret-shaped value, no real name, and take the free block in §3. **And
no test named this folder on 2026-11-04**, so this round adds the one that holds the answered state:
`internal/cli/docs_run_without_checkout_test.go`, pinning the four status lines and D902's deviation.

**F13. The launcher's person identity is not in the core, and the core may not import the one that
exists.** The only code in the tree that mints an operator's own Google ID token is
`ee/coordinator/cli`'s `coordinatorPersonIdentity` — `gcloud auth print-identity-token`, with the
refusals around a missing gcloud, no active account and an unusable answer — and it exists there
because a person's own ADC cannot be turned into an ID token by the Google client libraries at all
(their id-token sources handle a service account, an impersonated account and the metadata server,
and refuse an authorized-user credential by name). `internal/eeboundary/boundary_test.go` refuses any
core import of `ee/`, so the launcher cannot reuse that function and needs its own: a core helper
(package `internal/cli`, or a core package `ee/` may import if the duplication is to be repaid) whose
argv is the plain mint for a person and the audience-scoped one for a service account, exactly as that
function decides it. This is new work in this round, and the plan's A3 carries it.

## 2. What the brief asks for that the tree cannot do

**"Let the runner pick the job after cloning."** It cannot, and the spec says so (§9): a Cloud Run
execution names its job before the container starts, and the tree has no mechanism that moves a run
to another job. The runner's only power is to halt, and `checkJobProfile` already uses it. A "runner
picks" design is therefore a run that claims, mints a budget token, bills an execution, clones and
then fails — strictly worse than a refusal at the launcher, which costs nothing and tells the
operator the same thing.

**"Which credential reads the repository's `fugaro.yaml`?"** The owner answered this one (D902): the
installation's credentials service, person class. It is not the design's recommendation — the
recommendation was the App's key read as `--check-github-app` reads it — and the answer's cost is
stated in §3: the read is available only where the installation has opted into the service, and it
gives the launcher a dependency on `gcloud` being installed and logged in (F13). What the answer buys
over the recommendation is that the launcher holds no App key at all: an installation-wide credential
that can mint for every repository the App serves never enters the process that decides the job.

**"Does the read lift the refusal for `--retry` and `--pr`?"** It does, from the run's own
record rather than from the forge (D907), which is also the only answer that keeps a continuation on
the job it continues.

## 3. Decisions

The block **D901–D919** is held by this work; **D901–D914** are used below and D915–D919 are held for
the decisions implementing this design will need (a task that needs one takes the next number in the
block). The block's census is in the README. Every decision was answered by the owner on
**2026-11-04**; the answer column carries the date, the owner's reason in one line, and the words
**not the recommendation** where the answer is not the one this file proposed.

| # | Question | Recommendation | The alternative, and what it costs | Answered 2026-11-04 |
| --- | --- | --- | --- | --- |
| **D901** | Is the read automatic, or behind an explicit act? | **Automatic, on the one path that would otherwise refuse**: no new flag, the console unchanged, and a launch that already works untouched | **A new `--config-from-forge` flag** (plus a sixth field in the console's launch contract): a console release must ship before the GUI can launch *anything* on a migrated installation, a new flag enters the CLI reference and three skills, and every operator re-types the command once. **A probe run** (launch a run that clones, reads the document, reports the job the real run should use): two executions and two claims per launch, a new job kind, and the same information is one API call away | **As recommended.** Automatic, on the one refusing path: a flag would need a console release before the GUI could launch anything, and the read happens only where the launch would otherwise stop |
| **D902** | Which credential reads it | **The installation's existing App**, read exactly as `--check-github-app` reads it (Secret Manager, the operator's own credentials, a one-line notice, in memory only), minting a `contents:read`-only token for `spec.Repo`; refused by `refuseKeyReadInAgent` in a coding agent's session | **The credentials service**: a new principal, a second admission rule beside its `JobAccount` binding, an IAM binding on a dormant service, and nothing for Bitbucket. **The operator's ambient forge credential** (`gh`, a credential helper): Fugaro reads a person's credential out of a tool that may not exist, `docs/git-providers.md`'s statement stops being true in a new way, and Bitbucket has no equivalent. **A new stored launch token**: a new secret to create, store and rotate, duplicating the App | **Not the recommendation. Read through the credentials service's person class.** The launcher reads the file as the operator themselves (`github.ServiceReadTokens` against `/v1/github/read-token`), so **the CLI holds no App key and reads no environment variable**; the owner's reason is that the class shipped in 0.18.0, so a launcher need not hold an installation-wide key at all. The costs, stated: the read exists only where the installation has opted into the service (dormant otherwise: the launch refuses, D905), the launcher needs `gcloud auth print-identity-token` and so a `gcloud` on PATH and logged in (F13), the App ID comes from `repos.<repo>.github_app_id`, and every launch's read is one audited mint. The agent-session refusal stands as recommended, now for this value rather than for a key (`refuseKeyReadInAgent`, the shape `internal/cli/doctor_brokering.go` already uses for the service's bundle) |
| **D903** | At which ref | **`spec.Ref`** — the ref the run checks out and the runner re-reads | **The default branch**: a run at another ref is placed by a document it does not run under, and since the runner halts rather than widening, the operator pays a claim and a budget token for a decision the launcher could have made — on a `--ref` launch, at the moment someone is watching a branch | **As recommended.** `spec.Ref`, so the launcher and the runner read the same document; another ref turns the decision into a paid halt |
| **D904** | Caching | **None**: at most one read per command, memoized in-command like `checkoutParseCache`, written nowhere | **A config-directory cache keyed by (repo, ref)**: a stale document places a run the ref no longer selects and the run halts after the claim; the invalidation story cannot be honest about a force-push | **As recommended.** No cache: a document the ref no longer carries must not be able to place a run |
| **D905** | What a failed read does | **Refuse**, naming the cause and the two ways out (the checkout, or the permission) | **Fall back to the per-repository job** (what `main` did before #1477; the run usually works, since `internal/infra/spec.go` gives a clone workflow a per-repository job too): the project's policy chose a placement and the run silently does not use it — the profile's image, cpu, memory, secrets and admission list do not apply — and the only trace is a job name in `launch.json` | **As recommended.** Refuse. A read that fails means the job cannot be decided, and a run silently placed on a job the policy did not choose is worse than a refusal; the message names the cause and the ways out |
| **D906** | The console's contract | **Unchanged**: five fields, no new control, the CLI's refusals rendered verbatim | **A sixth field and a launch-screen control**: a console release before the GUI works at all, and a control that would be permanently on (every console launch is outside a checkout), so it buys no consent while the contract's "the five fields, and nothing else" gains a version question | **As recommended.** Unchanged: every console launch is outside a checkout, so a control would be permanently on and a sixth field would need a console release first |
| **D907** | `--retry` and `--pr` | **Reuse the job the run being continued recorded** (`Launch.Job`, backfilled by `existingLaunch`), falling through to the other sources only when there is no launch record | **Always read the document**: a follow-up can land on a different job than the run it follows when the ref's document changed in between — the one change a continuation must not make — and it spends a mint on a job already recorded. **Always refuse** from outside the checkout: a run launched by the console could never be retried from the console | **As recommended.** The recorded job: a continuation must stay on the job it continues, and the runner's own checks still judge it |
| **D908** | A provider no shared job serves | **Proceed on the per-repository job with no read** (`infra.SharedJobProvider`): it is the same answer the checkout path gives for the same repository | **Refuse for every provider** (the merged refusal's shape): every Bitbucket console launch is refused for a question with one answer, and the operator's way out — a checkout — is not one the console can take | **As recommended.** Proceed on the per-repository job with no read and no credential: for a provider no shared job serves, it is the same answer the checkout path gives |
| **D909** | A checkout whose working tree has no `fugaro.yaml` | **Read the ref's document from the checkout's objects** (`checkoutFugaroYAMLAtRef`, promoted from the auth axis to the whole document) | **Keep the refusal, fix only the message**: an operator standing in the checkout is told to run it from the checkout, when the local `git cat-file` that answers is the reader the same function already uses two gates later | **As recommended.** Read the ref's objects: the local `git cat-file` answers where the working tree does not, and the operator standing in the checkout is not sent back to it |
| **D910** | Where the reader lives | **A CLI seam** (`readRepoConfigAtRef`) over a new `FileAt` in `internal/gitprov/github`, with the package's existing replaceable HTTP seams and `httpfixture` for replay | **`gitprov.Provider.FileAt`**: a method every provider must implement, the fake included, for a read only GitHub can serve — and a second place a caller could reach with the run's own credential instead of a narrow one | **As recommended**, with the launcher's person identity added beside it (F13): a core helper minting the operator's own ID token, because the core may not import `ee/`'s |
| **D911** | Which permissions the minted token asks for | **A new `contents:read` + `metadata:read` set** in `internal/gitprov/github`, beside the two that exist | **`RunTokenPermissions`**: the launcher's process would hold a write-capable token for every repository the App serves, to read one file, enlarging the blast radius of the one process this design newly gives a provider credential | **As recommended, and moot as a choice:** the mint is the service's (D902), it asks GitHub for its own read-only set (`CoordinatorTokenPermissions`), and the client has no field in which to ask for another. The banned set is still banned: never `RunTokenPermissions` |
| **D912** | The message an operator sees | **Two distinct refusals**: "outside `X`'s checkout" (no checkout anywhere) and "the ref's `fugaro.yaml` could not be read" (with the cause and both ways out); the merged text stays only for the coding-agent case, where it is true | **One message**: the operator in a checkout is told to run from the checkout, and the operator without the permission is told something that does not fix it | **As recommended.** Two messages for two causes, because they have two remedies; a third line names the installation's own credentials-service opt-in where that is why no read could be made |
| **D913** | The documents this changes | **`docs/checkout.md`** loses the "Known gaps" bullet; **`docs/git-providers.md`** states the one exception in its own words; `docs/project-layer.md` is not touched; no `plugin/skills` change, since no flag is added (D901) | **No documentation change**: two pages keep describing a launcher that cannot read the file, and the next reader believes them | **As recommended**, with the exception now the service's read rather than a key read: `docs/git-providers.md` says the CLI holds no provider credential of its own, and a launch outside a checkout on an installation with a credentials service holds one read-only, repository-scoped token the service minted for the operator, for one file |
| **D914** | The review this work needs | **Security-sensitive, like the credentials service**: the tasks that add the read (A1–A3 of the plan) merge only on an independent review, and the flag-refusal path is a named test, not a comment | **Ordinary review**: the first path that reads a repository's configuration outside a checkout, with a provider credential in the process, ships on one reviewer's judgement | **As recommended, and wider:** under `.claude/rules/credentials-service.md` the path is the service's, so the review covers the person class, the audited mint, the absence of a key in the launcher and the agent-session refusal |

## 4. Open items

These are questions for the owner that are not design decisions, and the plan is writable without
their answers.

- **O1. A repository whose local config carries no `github_app_id`.** The App ID is per repository
  (`localcfg.Config`'s `repos.<r>.github_app_id`), it is what the person's read mint is signed as
  (`internal/gitprov/github/serviceread.go`), and a launch for such a repository from outside its
  checkout cannot make the read and refuses (D905) — correct, but the reason is invisible until the
  operator reads the message. Should `fugaro doctor` report a repository the installation serves
  whose local entry has no App ID, in the shape it already uses for an unset field? Nothing in this
  design depends on the answer.
- **O2. A retry whose recorded job the policy no longer defines.** D907 reuses the recorded job
  unconditionally; if the profile was removed from the published policy since, the run starts and
  the runner's placement check halts it. Refusing at the launcher would cost a policy read in a path
  that has none today. Worth a line in `docs/checkout.md` either way; not a decision this design
  needs.
- **O3. The console's repository picker.** A `checkouts[]` identity is a path the launch deliberately
  discards (`console/src/main/launch.ts`). Whether the picker should also offer repositories the
  installation serves but the app has no checkout of is a console design question, not this one's:
  the CLI's answer is the same either way.
- **O4. An installation that has not opted into the credentials service.** D902's answer makes the
  read available only where the service is provisioned, so the console's launch stays refused there
  and the message's only way out is the checkout the console does not have. Whether the refusal
  should name the installation's own `fugaro update --provision-credentials` — a command the rules
  refuse inside an agent or a run — or say only that the installation has no credentials service, is
  a wording question for whoever writes A2's messages; the design asks for the second, and nothing
  else depends on the answer.
