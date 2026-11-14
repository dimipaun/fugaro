# The shared-job contract (C1)

The Go half is [`internal/sharedjob`](../../internal/sharedjob/sharedjob.go): `Pairing`, `RunIdentity`, `JobChoice` and
`BitbucketProfile`, and a stub for each shape. This document is the prose half, and it is the half the sides are held
to: it says what each side may rely on, what none of them may, and what changing the contract costs.

C1 is a contract and not an implementation (plan C1, slice `bb-shared-jobs`). It exists because four tasks are written
in parallel against the same two decisions and cannot wait for each other: S1 and S4 (which together carry
`docs/bitbucket-workspace-token`'s T2.2) are the deployer and the launcher, S2 (`docs/secrets-brokering`'s B5.3) is the
credentials service's pairing, and S3 is the runner. The shapes in `internal/sharedjob` are what they call; the stubs
(`internal/sharedjob/stub.go`, and the test beside it) return a shape and decide nothing, and **I1 deletes exactly those
two files** when it wires the sides.

The contract rests on the owner's answers of 2026-11-13 ([reconciliation.md](reconciliation.md)): the pairing is the
verified run identity paired against the profile's repositories, and a shared Bitbucket job holds no token at all
(D2302); a baked workflow runs on a shared job like any other, so the checkout mode no longer chooses the job model
(D2304); and a project that mixes providers gets one default profile per provider, `standard` for the first and
`standard-<provider>` for another (D2310, R8).

## The shapes

| Shape | Carries | Decided by |
| --- | --- | --- |
| `RunIdentity` | the verified service-account email (`Account`), the run id (`Run`) and the run's own repository (`Repo`, canonical) | the credentials service, off the caller's own token; no field of it is request text |
| `Pairing` | that identity, the profile the calling job serves (`Profile`), and the verdict: `Allowed`, or `Refusal` — the sentence refusing the call, naming the repository the call asked for | the credentials service (S2) |
| `JobChoice` | the canonical repository and the workflow a launch names, and the profile that serves them — or no profile | the launcher (S4) and the runner's candidate logic, which must answer alike |
| `BitbucketProfile` | the profile's `Name`, its `Provider`, the account axis of its jobs (`Accounts`), its own `AllowRepos`, the `Secrets` it mounts and its `CPU`, `Memory` and `TimeoutClass` | the published policy, projected by whoever reads it |

`Pairing`'s zero value is a refusal and `JobChoice`'s empty `Profile` is the per-repository job, so no shape here reads
as a decision nobody made. A `Pairing` that carries a refusal is a refusal whatever its `Allowed` says: the shapes fail
closed, and a sentence naming a repository a call may not reach is not also a permission. That is deliberate: the sides
are written in parallel, and a shape that is not yet an answer must not be mistakeable for one.

**A profile's jobs are one per (profile, provider, account)**, and `Accounts` is that axis: the no-account job, spelled
`""`, plus one entry per account the profile lists, with `"default"` spelled `""` — `config.ProjectJobAccounts`' answer
over the published profile, and the one function that derives it. Both sides name the jobs from it, as
`gcp.ProjectJobName(Name, Provider, account)`, so neither derives the axis for itself: a side that did would launch a
run at a job nobody deployed, or deploy a job nobody launches.

## What each side may rely on

**The deployer (S1).** A profile is served by a shared job when its `Provider` is the provider the job serves —
`bitbucket` for a Bitbucket job — and it mounts no `config.BitbucketSecret`. It may rely on the projection carrying
every field it needs to make that decision and to build the jobs it deploys: one job per entry in `Accounts`, with the
`CPU`, `Memory` and `TimeoutClass` the projection carries. And on a profile that mounts the Bitbucket token being
*refused* a shared job rather than deployed without the mount: the token's reach is a per-repository list a shared job
cannot honour, and the job that serves the profile holds no token at all (D2302). `AllowRepos` is a profile's own list
and only that: the repositories it names for a message or a listing, never the verdict.

**The shared-job test is not `projectpolicy.SharedBitbucketProfile`.** That predicate is the *per-repository* Bitbucket
job's — the bitbucket provider **and** a mount of the Bitbucket token (D473, pinned by
`TestSharedBitbucketProfileNeedsTheTokenMount`) — so its rule is the opposite of the paragraph above, and it is true
for exactly the profiles a shared job must be refused for: a side that reached for the name would deploy a tokenless
shared job for a profile D2302 refuses one for, which is the most expensive way for this contract to be misread. C1
adds no behaviour and leaves the predicate and its pin where they are; what becomes of it is S1's, and S1's own path is
the plan's — its acceptance reads the profile fields above, and `TestBitbucketProfileMountsNoToken` is the new pin. A
side deciding at run time uses `BitbucketProfile`'s facts (`Provider`, `Secrets`) and never that predicate.

**The credentials service (S2).** It pairs a call from a shared job's verified identity to the repository in
`RunIdentity.Repo` — the run's own, which is a claim of the token the service verified and never one the request names
(G3). It may rely on `Pairing` being the whole of what the runner needs to show: the sentence refusing a call names the
repository the call asked for, so an operator reads which repository was refused and edits the profile's list or the
published `bitbucket_token.allow_repos`. The verdict is the service's, and it is `projectpolicy.Policy.Admits` (the
provider, the project's list and the profile's list) *and* the node's `bitbucket_token.allow_repos` — the contract
carries no repository list of its own and no second gate.

**The runner (S3).** It may rely on `RunIdentity.Repo` being the repository its run was launched for, and on a refusal
naming the repository it has to report. It shows that sentence when the proxy refuses a repository and halts with it,
rather than writing a second wording: one sentence means the refusal an operator reads is the refusal the service made.
The job it runs in holds no Bitbucket token, so no side of this contract carries one, and nothing here tells the runner
what to do with a repository the *policy* refuses: that is `Pairing` alone.

**The launcher (S4) and the runner's candidate logic.** They answer `JobChoice` over the same two facts and must agree
name for name, because the launcher computes the job's name from the choice and the runner matches `CLOUD_RUN_JOB`
against the same computation. The name each computes is `gcp.ProjectJobName(name, provider, account)` — the chosen
profile's own `Name` and `Provider` and an entry of its `Accounts` — so the account axis is read off the projection and
neither side keeps a list of accounts, or a rule for spelling one, of its own. A workflow that names no profile
resolves to the default profile of its repository's provider (D2310, R8); a choice of no profile means the launch keeps
its per-repository job, which is the only job there is on an installation that publishes no profiles, and the checkout
mode (`clone` or `baked`) no longer decides between them (D2304).

## What no side may rely on

- **A stub's answer.** The stubs admit nothing, refuse nothing, resolve no profile and apply no rule. Nothing they
  return is a decision any side may act on, and I1 removes them rather than promoting them.
- **`AllowRepos` as a gate.** It is a profile's own list. Whether a repository may run on a profile is
  `Policy.Admits`', which holds the project's enrollment, the profile's list and the provider together; reading the
  list alone would admit repositories the project denies.
- **The projection as the published document.** `BitbucketProfile` is what the sides read, not the policy. A side that
  needs a field it does not carry amends C1 (below) instead of reaching around it into `projectpolicy`.
- **A credential in any of these shapes.** There is no token, no secret value and no header text here, and there never
  will be: the job holds nothing, and a field carrying one would put a credential in the document every side reads.
- **A refusal as retryable.** A refusal means the repository is not admitted: the operator edits the profile's
  `allow_repos` or the token's own list — `project_secrets.bitbucket-token.allow_data_to` in the local config, which is
  what republishing derives the node's `bitbucket_token.allow_repos` from, or that published node itself — and publishes
  again. It is not a transient failure, and no side retries it.

## Changing the contract

The shapes are the contract, and a side that finds it needs more does not work around them: the plan anticipates one
change, CR1 — if S2's review finds the pairing needs the workflow name, CR1 amends `internal/sharedjob` and this
document, and the sides it reaches (S3 and S4) follow in the run's own follow-up. Amending C1 means editing both halves
in one sweep, and the same discipline holds for a field a side needs and this document does not name: the shape and the
"what each side may rely on" line change together, in the review that finds it. C1's first review is the worked example:
the job's own name showed the projection was short of the account axis and of the resources the deployer builds a job
from, and `Accounts`, `CPU`, `Memory` and `TimeoutClass` were added to both halves here rather than read around.
