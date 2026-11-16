# Repository management: add, remove, ls and status

**Status: the spec as received. Nothing here is approved and nothing is built.** This is the owner's order of 2026-11-14 and the problem it answers, recorded in the owner's words where the owner gave words and in the brief's words where the brief gave them. The decisions this spec leaves open are in [reconciliation.md](reconciliation.md), numbered `D2400`–`D2417`, each with a recommendation; the work is in [implementation-plan.md](implementation-plan.md). **Where this file and the reconciliation disagree, the reconciliation wins** — this file is what was asked for, the reconciliation is what is true of the code.

---

## 0. The order

> **Owner, 2026-11-14:** *"we need full management commands for repos: add, remove, ls, status (sort of a doctor)."*

Four verbs, one subject: **a repository's enrolment in a project**. Today the group that answers to that subject is `fugaro project repos`, and it has four subcommands — `ls`, `add`, `rm` and `set` — of which `ls`, `add` and `rm` edit **only the published allow-list**.

## 1. The problem the order answers

Enrolling a repository by hand takes about **six steps, in an order that matters**, and the owner got the order wrong several times on the GitHub sandbox `dimipaun/fugarosandbox`:

1. the repository's own `fugaro.yaml` — its workflows, its `git.base_branch`, whether it names a `dockerfile:` or a catalog entry;
2. the `repos:` entry in **this machine's** project config — provider, `base_branch`, workflows, `github_app_id`, `claude_accounts`;
3. the image map entry, through `fugaro image map set <repo> <entry>`;
4. the published allow-list entry, through `fugaro project repos add` (or `fugaro project sync`);
5. `fugaro update`, which publishes the project layer the allow-list lives in;
6. `fugaro update --repo <checkout>`, which converges the repository's own root: its workflow job, its check job, its Scheduler job, and the job account's `run.invoker` grant on the credentials service.

Each step is a different command, a different writer and a different failure, and **nothing in the sequence tells the operator what is left to do**. A step done out of order does not fail where it was done — it fails later, somewhere else.

**The failure that made the order visible.** A repository's `github-app-key` secret **container** is created by the repository's own root with **no version in it** (the container is Terraform's, the version is written by a person through `fugaro secrets set` or by the credentials service). Terraform's readiness gate then plans **no job at all** for a workflow that mounts it — "secret %s has no version" — so the enrolment looks complete and the repository has **no job**. Nothing said so. The owner learned it when a **launch was refused**: *"launching: job %s does not exist; create it with `fugaro update`"*. The one reading that would have said it — `doctor`'s `project-secret-stored` check, and the GitHub App check that says the private key is not stored yet — is not where an operator enrolling a repository looks, and it is not per-repository.

So the order is one symptom and the empty container is another, and both have the same cause: **the information needed to enrol a repository is spread over six commands and four stores, and no verb of the group puts it together.**

## 2. What the four verbs must do

### 2.1 `add` — the whole converge behind one confirmation

`fugaro project repos add <provider/owner/name | owner/name>` must do all six steps of §1, in the right order, behind **one** confirmation, and print the full list of what it will do before it does any of it.

- It **reads** what it can rather than asking: the provider and the canonical name from the argument and the repository's origin; the workflows and the base branch from the repository's own `fugaro.yaml`; the GitHub App id and the Claude accounts from the machine's project config; the image from the workflow's own catalog reference, then the project's image map, then the catalog's default; the allow-list pattern from the repository's canonical name.
- It **refuses**, naming the one command that fixes it, for everything it cannot decide: no checkout for the repository, a `fugaro.yaml` that names no workflow, a workflow naming a catalog entry the catalog does not hold, a project config that names no GitHub App id where one is needed, and a coding agent's session.
- It is **idempotent**: run again over a completed enrolment, it does nothing and says so, rather than reporting a change it did not make.
- It is **resumable**: run over an enrolment a failure or a Ctrl-C left half-done, it converges the missing steps and leaves the done ones alone.
- What the one confirmation covers, and what it does not, must be exactly true of the command: **the first image build is not part of it** — it is billable per build-minute and it is `fugaro image build <entry>`'s — and the command says so in the list it prints.

### 2.2 `rm` — the whole removal, behind one confirmation

`fugaro project repos rm <repo>` must undo the enrolment, and it must be **exactly as wide as `add`, minus what must never be removed**.

It removes: the published allow-list entry; the repository's `repos:` entry in this machine's project config; its image map entry; the published per-repository policy node; and the repository's cloud resources — its workflow job, its check job, its Scheduler job and its job account's grants — through the same retire path `fugaro update` uses.

It **deliberately leaves**, and says so before the confirmation and after the write: the provider's App installation; the images built for it; its secrets and their versions; its run history; its open pull requests; its remote branch. A removal that silently deleted a run's history would be a different promise than the one the order makes.

**An in-flight run is never disturbed.** `rm` reads whether a run of that repository is going; if one is, the confirmation is the strict one — no flag stands in for it — and the removal never rewrites the run's records, its ledger or its counters.

A **`--purge`** that also deletes the history, the images or the secrets is **out of scope** unless the reconciliation decides otherwise.

### 2.3 `ls` — one row per repository

`fugaro project repos ls` must answer "what is enrolled here, and what is each repository made of" in one row per repository: its canonical name, its provider, its base branch, its workflows, its image, its Claude accounts, and both halves of its enrolment state — **published** (in the allow-list) and **local** (in this machine's config).

It must carry a `--json` document with a **versioned** shape, because the desktop console and the history read the group's answers and a shape that changes under them without a version is a figure drawn wrong.

### 2.4 `status [REPO]` — a doctor for repositories

`fugaro project repos status [REPO]` must be **a doctor for the enrolment**: short and actionable, under the one-screen rule, with a row per fact that decides whether a run can start.

Rows it must carry, one per repository or one for the named repository:

| Row | The fact |
| --- | --- |
| App installation reachable | the provider's App is installed on the repository (or the Bitbucket workspace token admits it) |
| Credential version | the `github-app-key` secret — or the Bitbucket token — holds an **enabled version**, which is the fact the empty container lost |
| Account tokens | each `claude_accounts` entry's stored token exists and has an enabled version |
| Image map | the repository has an entry in the effective map, and whether the entry's image is built and current |
| `allow_data_to` | the accounts and project secrets whose `allow_data_to` names the repository |
| `credentials_service_ready` | the repository's record says the credentials service serves it |
| Job and grant | the repository's job is deployed and its job account holds `run.invoker` on the credentials service |
| Project layer copy | this machine's project config holds the repository's `repos:` entry |
| Last run | the last run of the repository and its outcome |

Constraints on it, all of them load-bearing:

- **Every failing row names the single command that fixes it.** A row that says what is wrong and not what to type is the state the empty container already put the owner in.
- **Exit status is nonzero when something blocks a launch**, and zero when nothing does — the same contract `fugaro doctor` has.
- **It is read-only.** It makes no cloud write on any path, `--json` included.
- It is **short**: the rows that pass collapse, the rows that fail carry the sentence and the fix.

### 2.5 Where they live, and who may call them

- The verbs extend the existing `fugaro project repos` group rather than a new one.
- The **gate** each verb asks is the roles-and-scopes gate the group already asks: the writes ask **admin at the project**; the reads ask what a read asks.
- Anything that puts a **secret** in front of a person — the token for a Bitbucket repository, the App private key — follows the human-terminal rules the repository already has: read at a hidden prompt or from stdin, never from an argument, never printed, refused in a coding agent's session.
- The **desktop console** may call the **read** verbs only. It never calls `add` or `rm`.
- The **plugin skill** and the **docs** that name these commands change in the same pull requests as the CLI does.

### 2.6 How `fugaro update` relates

The HARD RULE stands: **`fugaro update` is the whole upgrade** and it works perfectly, with no operator step that is another command. So:

- `add` must never leave an installation in a state that `fugaro update` would then change — the converge it writes is the converge update re-derives, not a second shape update would correct.
- `update` keeps working unchanged on an installation that has never run `add`, and on one that has.
- The one **owner-run live check** the order needs is a single script, in the shape of Check 313: **enrol the GitHub sandbox end to end and remove it again**, on a throwaway checkout of `dimipaun/fugarosandbox`, asserting every reading the six steps produce and the readings `status` claims.

## 3. What this work must not do

- **It must not re-add a flag that a landed change removed.** The no-prompt policy's T11 removed `--yes` and `--non-interactive` together; the group's `add` and `rm` no longer take them, and a plan that puts `--yes` back on `add` contradicts a decided, merged ruling.
- **It must not make `add` or `rm` reachable from a coding agent's session**, and it must not lift any refusal this repository has landed.
- **It must not delete a secret, a secret version, an image, a run's history or a person's pull request.**
- **It must not write the published policy from anywhere but the one ETag-guarded writer.** The generation guard stays; the policy generation is recorded, not invented.
- **It must not disturb a run in flight** — no step of `add` or `rm` may rewrite what a run holds.
- **It must not touch `belong`.** Every live step is owner-run, on the sandbox installation.
- **It must not weaken `doctor`.** `status` reuses what `doctor` computes; it does not fork it, and it adds no second reader of the same fact.
- **It must not leave `docs/status.md` without this spec's row.** The row is written in this design's own sweep, and **no task of the plan edits the file**: whoever merges the spec moves the row as tasks land.

## 4. What is asked of the owner

Six questions, each answered as a numbered decision with a recommendation in [reconciliation.md](reconciliation.md):

1. **`add`** — is the converge behind one confirmation the right shape, and what does the confirmation cover?
2. **`rm`** — what does it remove, what does it deliberately leave, and is `--purge` in or out?
3. **`ls`** — what is one row, and what is the versioned `--json` shape?
4. **`status`** — which rows, and what makes its exit status nonzero?
5. **Where they live** — the group, the console's read-only reach, the gate, and the human-terminal rules for a secret.
6. **`update`** — how the converge and the whole upgrade are kept from contradicting each other, and the one owner-run live check.
