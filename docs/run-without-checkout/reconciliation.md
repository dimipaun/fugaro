# Launching a run for a repository without its checkout: reconciliation

**Where this file disagrees with [spec.md](spec.md), this file wins.** Every claim below was checked
against `origin/main` at `432623d9c` and against the open pull requests of 2026-11-01, by reading
the code named, not by trusting a neighbouring document.

## 1. Findings against the tree

**F1. The refusal this design answers is not on `main`, and the brief's premise is one release
ahead of the tree.** `main`'s `launchProjectJobFor` (`internal/cli/project_job.go`) still says, with
its own comment, "No checkout in the working directory (fugaro run --repo ... from a machine that
does not hold the repository) means no answer to either, and a launch that cannot tell keeps the job
it has always had":

```go
co := checkoutConfig(ctx, spec.Repo)
if co == nil {
    return nil, nil
}
```

`432623d9c` is `release: v0.14.1`, and the refusal ships in **PR #1477**, which is open. So this
design answers a state the tree reaches only when #1477 lands, and the plan's tasks must be written
against #1477's code, not against `main`'s. Two consequences: the first task either waits for #1477
or carries it, and the red step of every task that changes this function is red against #1477's
tree, not against `main`.

**F2. `co == nil` conflates two causes, and #1477's message is false for one of them.**
`checkoutParse` (`internal/cli/cloud.go`) returns nil both when `checkoutRoot` names no checkout and
when the checkout exists but `readFugaroYAML` fails — the tree has no `fugaro.yaml` (an operator on a
branch whose tree predates the file, which is exactly when `checkoutFugaroYAMLAtRef` would answer
from the objects). #1477's refusal then tells that operator to "run it from `X`'s checkout", which is
where they are. Today's `return nil, nil` is wrong for the other cause (`docs/checkout.md`'s "Known
gaps": a clone workflow outside the checkout is judged baked), and neither behaviour distinguishes
them.

**F3. `repoProvider` panics on a nil `co` when the local config has no entry.**
`internal/cli/project_job.go:224` is `if r, ok := e.localRepo(repo); ok && r.Provider != "" { return
r.Provider }; return co.Git.Provider`. Any code that runs *because* there is no checkout must not
call it in that form. In practice `repoSlug` (`internal/cli/cloud.go`) has already refused a launch
whose provider is unknown ("cannot tell the git provider of %s: set provider in its repos entry of
the local config, or run from its checkout"), so the provider is known — but the call must be
reordered or split, and that is a test in the plan.

**F4. The Bitbucket narrowing is provable from the code, not from a policy.**
`infra.SharedJobProvider` (`internal/infra/projectjobs.go:312`) is a list holding `github` and
nothing else, with a test that walks it against `config.Providers`; and `launchProjectJobFor`'s own
gate is `provider != pr.Provider || !infra.SharedJobProvider(pr.Provider)` — a repository on another
provider "stays on its per-repository job, decided here before any claim is taken or budget token
minted". That gate sits *after* the checkout lookup, so it is unreachable when `co == nil`: a
Bitbucket repository with no checkout is refused by #1477 for a question that has exactly one
answer.

**F5. A workflow's two keys come from different layers, and that is why no default can be
guessed.** `docs/project-layer.md`'s key table gives `workflows.*.checkout` the layers `profile,
repo` — the project layer may make a workflow a clone workflow — and `workflows.*.job_profile`
`repo` alone: only the repository's own file names the profile. Without that file the launcher can
know a workflow clones and still not know which shared job it runs on, and the default (`standard`)
is wrong exactly where this matters, since a project publishes profiles because its repositories
name them.

**F6. The machinery for the read exists; the pieces that are missing are named and small.** The CLI
already reads the App's key (Secret Manager, with the operator's own credentials, after a one-line
notice, refused under an agent marker: `internal/cli/githubapp.go`), already parses it
(`github.ParsePrivateKey`), already mints installation tokens (`MintInstallationToken`,
`github.AppRepositories` for the metadata-only read), and already has replaceable HTTP seams
(`githubAPIURL`, `githubAPIClient`) with a replay helper (`internal/gitprov/httpfixture`). What does
not exist: a `contents:read`-only permission set (`RunTokenPermissions` and
`CoordinatorTokenPermissions` are the two that exist, and the first is write-capable), and a
file-at-a-ref reader — `gitprov.Provider` (`internal/gitprov/gitprov.go`) has **no** file-read
method, by design, since a Bitbucket credential cannot be widened and the interface should not grow
a capability one provider has.

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

**F9. Every console launch is from outside every checkout, so #1477 breaks the GUI on any migrated
installation.** The `run.launch` child's working directory is the app's own private, empty directory
(`console/src/main/launch.ts:828`; the directory is `privateWorkingDirectory`,
`console/src/main/spawn.ts:1615`), deliberately — "a launch spawned in a checkout could take its ref
from whatever the operator happens to have checked out, invisibly". So `checkoutRoot` is always
empty for the child, `co` is always nil, and on an installation whose policy defines a profile every
console launch of every repository is refused. `--ref` is on `NEVER_REACHABLE` in that schema, so a
console launch is always at the base branch and can never be influenced by a branch.

**F10. The two credentials the brief asks about are unusable, for reasons already in the tree.**
`internal/credsvc`'s mint binds a minted token to a **JobAccount** — a job identity and its
repository — so a launcher asking as itself is not an admitted caller, and serving one would be a
second admission rule in a service that ships dormant. `ee/coordinator/project.go` refuses Bitbucket
by name (`ErrBitbucket`: a Bitbucket credential is one repository's access token and cannot be
widened to a project), which is the same fact `SharedJobProvider` encodes.

**F11. `docs/checkout.md` and `docs/project-layer.md` both describe `main`'s behaviour and are made
stale by #1477.** `docs/checkout.md`'s "Known gaps" bullet names the per-repository judgement as an
accepted gap with the remedy "run `fugaro run` in the repository's checkout";
`docs/project-layer.md:25` says "A launch that cannot read the repository's own workflows — `fugaro
run --repo …` from a machine with no checkout of it — keeps the per-repository job, as it does
today". Neither is true once #1477 lands. **`docs/project-layer.md` is owned by open PR #1478**, and
neither file is touched by this design.

**F12. The doc tests that can bite these files.** No test enumerates `docs/`'s directories, so a new
folder is free; but three `WalkDir("../../docs")` tests read every page — the compute-backend test
(refusing a retired port literal anywhere under `docs/`), the mobile-telemetry test (a decision
number's block must be reserved by exactly one design and must be free on `main`), and
`docs_live_init_census_test.go` (a retired command spelling may appear only in dated records and a
named list). This design's files use no retired literal, no price, no secret-shaped value, no real
name, and take the free block in §3.

## 2. What the brief asks for that the tree cannot do

**"Let the runner pick the job after cloning."** It cannot, and the spec says so (§9): a Cloud Run
execution names its job before the container starts, and the tree has no mechanism that moves a run
to another job. The runner's only power is to halt, and `checkJobProfile` already uses it. A "runner
picks" design is therefore a run that claims, mints a budget token, bills an execution, clones and
then fails — strictly worse than a refusal at the launcher, which costs nothing and tells the
operator the same thing.

**"Which credential reads the repository's `fugaro.yaml`?"** None that the tree has today beyond the
App: the CLI holds no provider credential (`docs/git-providers.md`), the credentials service cannot
serve a launcher (F10), and the two existing readers of the App's key are behind explicit flags
refused in a coding agent's session. The design reuses that path (D902) rather than inventing one.

**"Does the read lift #1477's refusal for `--retry` and `--pr`?"** It does, from the run's own
record rather than from the forge (D907), which is also the only answer that keeps a continuation on
the job it continues.

## 3. Decisions

The block **D901–D919** is held by this work; **D901–D914** are used below and D915–D919 are held for
the decisions implementing this design will need (a task that needs one takes the next number in the
block). The block's census is in the README.

Every recommendation is written first. The alternative's cost is the reason not to take it, and
where an alternative is free it should win.

| # | Question | Recommendation | The alternative, and what it costs |
| --- | --- | --- | --- |
| **D901** | Is the read automatic, or behind an explicit act? | **Automatic, on the one path that would otherwise refuse**: no new flag, the console unchanged, and a launch that already works untouched | **A new `--config-from-forge` flag** (plus a sixth field in the console's launch contract): a console release must ship before the GUI can launch *anything* on a migrated installation, a new flag enters the CLI reference and three skills, and every operator re-types the command once. **A probe run** (launch a run that clones, reads the document, reports the job the real run should use): two executions and two claims per launch, a new job kind, and the same information is one API call away |
| **D902** | Which credential reads it | **The installation's existing App**, read exactly as `--check-github-app` reads it (Secret Manager, the operator's own credentials, a one-line notice, in memory only), minting a `contents:read`-only token for `spec.Repo`; refused by `refuseKeyReadInAgent` in a coding agent's session | **The credentials service**: a new principal, a second admission rule beside its `JobAccount` binding, an IAM binding on a dormant service, and nothing for Bitbucket. **The operator's ambient forge credential** (`gh`, a credential helper): Fugaro reads a person's credential out of a tool that may not exist, `docs/git-providers.md`'s statement stops being true in a new way, and Bitbucket has no equivalent. **A new stored launch token**: a new secret to create, store and rotate, duplicating the App |
| **D903** | At which ref | **`spec.Ref`** — the ref the run checks out and the runner re-reads | **The default branch**: a run at another ref is placed by a document it does not run under, and since the runner halts rather than widening, the operator pays a claim and a budget token for a decision the launcher could have made — on a `--ref` launch, at the moment someone is watching a branch |
| **D904** | Caching | **None**: at most one read per command, memoized in-command like `checkoutParseCache`, written nowhere | **A config-directory cache keyed by (repo, ref)**: a stale document places a run the ref no longer selects and the run halts after the claim; the invalidation story cannot be honest about a force-push |
| **D905** | What a failed read does | **Refuse**, naming the cause and the two ways out (the checkout, or the permission) | **Fall back to the per-repository job** (what `main` does today; the run usually works, since `internal/infra/spec.go` gives a clone workflow a per-repository job too): the project's policy chose a placement and the run silently does not use it — the profile's image, cpu, memory, secrets and admission list do not apply — and the only trace is a job name in `launch.json`. This is the fallback #1477 exists to remove |
| **D906** | The console's contract | **Unchanged**: five fields, no new control, the CLI's refusals rendered verbatim | **A sixth field and a launch-screen control**: a console release before the GUI works at all, and a control that would be permanently on (every console launch is outside a checkout), so it buys no consent while the contract's "the five fields, and nothing else" gains a version question |
| **D907** | `--retry` and `--pr` | **Reuse the job the run being continued recorded** (`Launch.Job`, backfilled by `existingLaunch`), falling through to the other sources only when there is no launch record | **Always read the document**: a follow-up can land on a different job than the run it follows when the ref's document changed in between — the one change a continuation must not make — and it spends a secret read on a job already recorded. **Always refuse** from outside the checkout: a run launched by the console could never be retried from the console |
| **D908** | A provider no shared job serves | **Proceed on the per-repository job with no read** (`infra.SharedJobProvider`): it is the same answer the checkout path gives for the same repository | **Refuse for every provider** (#1477's shape): every Bitbucket console launch is refused for a question with one answer, and the operator's way out — a checkout — is not one the console can take |
| **D909** | A checkout whose working tree has no `fugaro.yaml` | **Read the ref's document from the checkout's objects** (`checkoutFugaroYAMLAtRef`, promoted from the auth axis to the whole document) | **Keep the refusal, fix only the message**: an operator standing in the checkout is told to run it from the checkout, when the local `git cat-file` that answers is the reader the same function already uses two gates later |
| **D910** | Where the reader lives | **A CLI seam** (`readRepoConfigAtRef`) over a new `FileAt` in `internal/gitprov/github`, with the package's existing replaceable HTTP seams and `httpfixture` for replay | **`gitprov.Provider.FileAt`**: a method every provider must implement, the fake included, for a read only GitHub can serve — and a second place a caller could reach with the run's own credential instead of a narrow one |
| **D911** | Which permissions the minted token asks for | **A new `contents:read` + `metadata:read` set** in `internal/gitprov/github`, beside the two that exist | **`RunTokenPermissions`**: the launcher's process would hold a write-capable token for every repository the App serves, to read one file, enlarging the blast radius of the one process this design newly gives a provider credential |
| **D912** | The message an operator sees | **Two distinct refusals**: "outside `X`'s checkout" (no checkout anywhere) and "the ref's `fugaro.yaml` could not be read" (with the cause and both ways out); #1477's single text stays only for the coding-agent case, where it is true | **One message**: the operator in a checkout is told to run from the checkout, and the operator without the permission is told something that does not fix it |
| **D913** | The documents this changes | **`docs/checkout.md`** loses the "Known gaps" bullet; **`docs/git-providers.md`** states the one exception in its own words ("the CLI holds no provider credential *of its own*: it holds the App's key for one read and one pre-check, in memory, refused under an agent's marker"); `docs/project-layer.md` is #1478's to fix; no `plugin/skills` change, since no flag is added (D901) | **No documentation change**: two pages keep describing a launcher that cannot read the file and a CLI that holds no provider credential at all, and the next reader believes them |
| **D914** | The review this work needs | **Security-sensitive, like the credentials service**: the tasks that add the read (A1–A3 of the plan) merge only on an independent review, and the flag-refusal path is a named test, not a comment | **Ordinary review**: the first path that reads the App's key without a flag ships on one reviewer's judgement |

## 4. Open items

These are questions for the owner that are not design decisions, and the plan is writable without
their answers.

- **O1. A repository whose local config carries no `github_app_id`.** The App ID is per repository
  (`localcfg.Config`'s `repos.<r>.github_app_id`) and `--check-github-app` needs it. A launch for
  such a repository, from outside its checkout, cannot make the read and refuses (D905) — correct,
  but the reason is invisible until the operator reads the message. Should `fugaro doctor` report a
  repository the installation serves whose local entry has no App ID, in the shape it already uses
  for an unset field? Nothing in this design depends on the answer.
- **O2. A retry whose recorded job the policy no longer defines.** D907 reuses the recorded job
  unconditionally; if the profile was removed from the published policy since, the run starts and
  the runner's placement check halts it. Refusing at the launcher would cost a policy read in a path
  that has none today. Worth a line in `docs/checkout.md` either way; not a decision this design
  needs.
- **O3. The console's repository picker.** A `checkouts[]` identity is a path the launch deliberately
  discards (`console/src/main/launch.ts`). Whether the picker should also offer repositories the
  installation serves but the app has no checkout of is a console design question, not this one's:
  the CLI's answer is the same either way.
