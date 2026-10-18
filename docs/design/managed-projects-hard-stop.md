# The billing hard stop: a design of its own

**Status: design only. Nothing in this document is built, no code follows it until the owner answers the
decisions in §5 and approves an implementation task of its own, no step of it performs or prescribes a live
cloud action, and no secret is handled. It is SECURITY-SENSITIVE**: it introduces an always-on credential with
money-adjacent power (the reconciliation's finding 25) and it is the one feature in this line of work that can
stop a live project's spending, so it takes an **independent opus review** and decides nothing by itself.

**What this document is.** Task **6.0** of [the managed-projects plan](../plans/2026-10-16-managed-projects.md)
(`:831`) — the row whose **Files** line is *"a design of its own, if the owner wants one"*. The owner wants one:
on **2026-10-18** they approved **building the billing hard stop**, which is what that row was waiting for.
D63's answer of 2026-10-16 — **"no hard stop in the first managed release"** — is kept as written where it is
([managed-projects.md](managed-projects.md) §5, `:177`, and the plan's Task 6.0), because it is the record of
what was decided then and why: a hard stop needs **a Pub/Sub topic and a function holding billing-account
admin**, which is a **new always-on credential with money-adjacent power**, in a project whose premise is that
the agent there has broad rights. What the 2026-10-18 approval changes is the decision *after* that one: **the
hard stop is built — in its own release, under its own decisions, and only in the shape §5 recommends and the
owner answers.** Nothing else is reopened. The budget stays an alert; the apply gate's ruling of 2026-10-18
stands (`internal/managed/budget.go`: the recorded amount is the evidence that the owner chose a budget, and it
caps no bill); Fugaro's own kill switch stays what it is.

**Why the deliverable is a document and not code, stated once.** The plan fixes *that* the hard stop is wanted,
*what it costs in privilege* (finding 25) and *which two mechanisms the spec offered* — "disables billing or
scales services down" ([managed-projects-spec.md](managed-projects-spec.md) `:84`, `:91`). It fixes **no
mechanism**: not what stops the money, not who may undo it, not how a false positive is recovered, and not how
any of it is proven without a live cloud. Those four are the whole of §3 to §6 below, they are answered here as
decisions rather than as prose, and the implementation is written only after the owner answers them.

**Where it sits, and what binds it.** It is a **managed-projects** document, so the reconciliation's §8 rules and
the plan's Global Constraints carry over unchanged and are not restated here: **docs and code only, no live
action**; **never a secret**; **never `belong`** — no step of this design names it, and every check-shaped step
of it runs in the sandbox checkout with the register's guard block; **fail closed**; **no task's verification
needs a live cloud** (Global Constraint 12 — which is why §6 is a section rather than a promise); **nothing under
`.github/workflows/`**; and the plan's flag for security-sensitive work. It adds no task to the plan and reopens
no phase: the implementation it describes is Task 6.0's own, and it starts when the owner says so.

**Releases, stated rather than assumed.** D69's ruling of 2026-10-16 put managed projects in **0.8.5**, and the
owner's rulings of 2026-10-18 keep project-centric phases 4 and 6 out of **0.9.0**. Nothing here is scheduled
into a release: the plan's Task 6.0 is **unscheduled**, and this document does not reschedule it. What it does
say is what the mechanism needs that 0.8.5 does not have — a second GCP project in the recommended shape (§3.3),
one billing-side permission (§3.4), and a change to the creation fact's version (§5, D85) — and that the last of
those touches the read every managed apply already depends on.

## 1. What the plan and the reconciliation fix, and what they leave open

The four questions, each with the sentence in the tree that asks it and nothing that answers it:

1. **What exactly stops the money — billing, or the resources?** The spec's own open question
   (`managed-projects-spec.md:91`: *"Should the hard stop disable billing, or only scale services down?"*). D63's
   alternative column prices both and chooses neither: **"Disable billing on 100%"** costs *"a Pub/Sub topic plus
   a function holding billing-account admin, in a project the agent is meant to control; a false trigger stops
   everything in the project including the run that would fix it, and re-enabling billing is a manual owner
   action"*, and **"scale services down"** costs *"still needs the function and its credential, and a 'scale
   down' that misses one job is a hard stop that does not stop"*. Nothing in the plan or the design picks one.
2. **Who can undo it?** Nowhere stated. D63's cost column has the whole of it — *"re-enabling billing is a
   manual owner action"* — which names no identity, no command and no record.
3. **How is a false positive recovered?** Nowhere stated, and it is the failure mode D63's own cost column puts
   first: *"a false trigger stops everything in the project including the run that would fix it"*.
4. **How is it proven without a live cloud?** Nowhere stated, and Global Constraint 12 makes it a constraint
   rather than a preference: **no task's verification may need a real project or a billing account.** So the
   mechanism has to be designed with a seam a fake can sit behind, and the one live step has to be an owner-run
   check — which is why §6 names the number and reserves it.

**What the plan does fix, and this document does not re-derive.** The privilege cost is real and named
(finding 25: *"a new always-on credential with money-adjacent power, in a project whose whole premise is that
the agent there has broad rights"*); there is **no Budgets API client, no Pub/Sub subscription and no
enforcement path in the tree** (finding 24); the budget's alerts **only notify** and are delivered to the billing
account's own people, not to "the project owner" (finding 24 and D63's row); and the budget's `projects` filter is
already project-scoped, so the budget the spec wants already covers what the spec wants it to cover.

## 2. What the tree holds today — the four pieces the hard stop sits beside

- **The budget and its alerts.** `google_billing_budget` is created opt-in at creation, filtered to the project,
  with threshold fractions that default to `[0.5, 0.9, 1.0]`
  (`deploy/terraform/gcp/modules/installation/budget.tf:11-36`, the default at `variables.tf:182`, the same
  default passed through by the root at `roots/installation/variables.tf:95`). It carries **no
  `all_updates_rule`**, so it has no notification channel and no Pub/Sub topic of its own: its mails go to GCP's
  default recipients, the holders of Billing Account Administrator and Billing Account User on the billing
  account. `alert.tf`'s email channel belongs to the image check's two policies and is not attached to the
  budget.
- **The creation fact.** `fugaro/managed.json` at the runs bucket's root, written only by the owner-run creation
  and unreadable-by-write to any credential a run holds (`internal/infra/managed/marker.go:26-55`). Version 2
  records the budget amount and its currency, which is what makes the apply gate's question answerable at all
  (`internal/infra/managed/read.go:126`, `ReadBudget`).
- **The apply gate.** `internal/managed/budget.go`'s `ApplyGate` and `internal/managed/enable.go`'s `Enable`: the
  apply is refused unless an apply-time read answered with an amount and a currency the marker's own rule
  accepts, and the owner's ruling of 2026-10-18 removed the comparison with Fugaro's recorded model spend
  because the two figures are not the same quantity. **The recorded amount caps no bill**; it decides whether an
  apply happens at all.
- **Fugaro's own halt.** `fugaro budget kill` / `fugaro budget resume`
  (`internal/cli/budget.go:1137-1229`; `internal/budget/kill.go:141`) is the closest thing in the tree to a hard
  stop, and it is the **model this design copies for the parts of a stop that are not the stopping**: it records
  who, when and why; it is confirmed by typing the project's name; it halts runs in flight within seconds and
  refuses new ones; it is one command to undo; and a switch nobody can read counts as **on**
  (`internal/budget/kill.go:16-27`). What it acts on is **Fugaro's own model spend, not the project's GCP bill**
  (finding 26), so it is not this feature — but its shape is the shape §4 and §5 describe.

**What is missing, and it is more than a client.** There is no `all_updates_rule`, no topic, no subscription, no
enforcement, and no Budgets-API client. There is also **no way to unlink billing at all**: `LinkBilling`
(`internal/infra/projectcreate.go:504-537`) links and only links — its own comment says it "never relinks" and
its guard refuses to write when an account is already linked (`:521`) — so the mechanism this design recommends
is the **inverse of a call the tree already makes** (`UpdateBillingInfo` at `:525`), and the inverse does not
exist yet. The deployer account of D75 can neither link nor unlink: it holds no billing permission at all
(`deploy/terraform/gcp/modules/installation/deployer.tf`), and the same file states the neighbouring rule this
design has to respect — `serviceusage.services.enable` is granted and *"Not disable: a delete is the owner's to
approve, and the deployer could not apply it"* (`:112`).

## 3. The mechanism, in three parts: the trigger, the action, and where it runs

A hard stop is three decisions that fail independently, so they are priced separately: **what fires it**, **what
it does**, and **where the thing that does it lives**. A stop is only as strong as the weakest of the three —
and the third is the one that decides whether this is a control or a promise.

### 3.1 What fires it

| Trigger | What it is | What it costs |
|---|---|---|
| **The budget's own Pub/Sub notification** (recommended) | `all_updates_rule { pubsub_topic = … }` on the budget the creation already makes, delivering the 100% rule's notification to a topic the enforcement subscribes to | A topic and a subscription per managed project (or one topic the budgets of several projects publish to), plus the update of an existing budget by an owner-run apply. It is the **only channel that carries the figures** — the message's attributes are `billingAccountId`, `budgetId` and `schemaVersion`, and its data `costAmount`, `budgetAmount`, `currencyCode`, `costIntervalStart`, `alertThresholdExceeded` and `forecastThresholdExceeded` (Cloud Billing docs, "Programmatic budget notifications", read 2026-10-18) — so the predicate of §5 D83 can be exact. It is also **late and repetitive by design**: sent "multiple times per day", the first one "may take several hours", and delivery is at-least-once, so duplicates and out-of-order messages are normal input rather than errors. |
| **A Scheduler poll of the Cloud Billing Budgets API** | A scheduled job that reads the budget and decides | **Not an option**: the Budgets API v1 `Budget` resource has no field that reports spend — `amount` is the target and the threshold rules only decide when to alert (§8 item 1, settled by the API reference, read 2026-10-18). A poll would have to read the billing export instead, which is the next row. |
| **A Cloud Billing export into BigQuery** | The bill's rows, queried on a schedule | Exact figures and history, at the price of a dataset, a billing-account-scoped read and hours of delay — a figure the notification carries for free, bought again more slowly. |
| **The alert email and a person** | Today's behaviour (D63's answer) | Zero new privilege and zero new components — and a stop that fires when somebody reads their mail. It is the honest baseline every row above is measured against, and it is what is left standing if none of them is chosen. |

### 3.2 What it does

| Action | What it is | What it costs |
|---|---|---|
| **Unlink the billing account from the managed project** (recommended) | The `projects.updateBillingInfo` method with an empty `billingAccountName` — the inverse of `LinkBilling`'s write (`internal/infra/projectcreate.go:525`), and the call Google's own "disable billing with notifications" guide makes | One call, one permission (§3.4). It stops every billable service in the project at once instead of a list that can be incomplete, the project and its IDs survive, and **relinking is the undo** — but **not a guaranteed one**: Google's own pages say that while billing is disabled *"some of your Google Cloud resources might be removed and become non-recoverable"*, that there is *"no guarantee of service recovery"*, and that after a relink resources can take up to 24 hours to restart (Cloud Billing docs, "Enable, disable, or change billing for a project" and "Disable billing usage with notifications", read 2026-10-18). So the state bucket and the Firestore/RTDB data the reconciliation's §6 item 2 calls not disposable are **at risk, not preserved**, for as long as the stop holds (§8 item 5). It also stops the installation itself — including the run that would have fixed the cause, which is D63's own first objection — it is refused outright while the project holds an active or pending committed-use discount, and it is the row that needs a billing-adjacent credential. |
| **Arm Cloud Billing's native spend cap** | A budget with `spendCap` set (Budgets API v1, read 2026-10-18): one project, one service, and at 100% of the amount Google itself pauses **new usage** of that service until the cap is lifted or the next monthly period begins | **No credential of Fugaro's at all, no guardian, nothing to deploy**: Google enforces it, and lifting it needs `billing.budgets.configureSpendCap` on the billing account (or `billing.resourcebudgets.configureSpendCap` on the project), which no run identity holds. Its eligible services today are exactly the two that carry a run's own spend — **Cloud Run** (`run.googleapis.com`) and **Vertex AI** (`aiplatform.googleapis.com`) — plus Cloud Run functions and the Gemini API. What it does not stop: persistent resources (storage, a Compute/Batch VM, Cloud Build, Artifact Registry), in-flight requests, and anything the next period resets. It cannot be added to the existing alert-only budget (a new budget per service), it is described as a release for "a limited set of services" (launch stage UNVERIFIED, §8 item 8), and an enforced budget cannot be edited or deleted until it is lifted. D86 asks whether to arm it **as well as** D79's mechanism. |
| **Scale the workloads down** | Min instances to zero, schedules paused, jobs disabled | Project-scoped permissions only, so **no billing-account credential at all** — the smallest privilege footprint of any row here. And a "scale down" that misses one job is a hard stop that does not stop (D63's words): the completeness of the stop is the completeness of a hand-written list, and a claim on a bucket or an egress line is not on it. |
| **Disable the project's billable APIs** | `serviceusage.services.disable` for the services in `apis.tf` | Reversible and project-scoped, and it is against two rules already written down: `apis.tf` says *"Nothing here ever disables an API: other things in the project may depend on it"*, and `deployer.tf:112` says enabling is the deployer's and disabling is not. |
| **Delete the project** | The ultimate stop | It destroys what the reconciliation's §6 item 2 shows is **not** disposable: the state bucket that lives in the project, the Firestore and RTDB contents a from-zero apply re-creates the database for but not the data, and the database's location, which can never be changed. Refused as a mechanism. |
| **Suspend the identities** | Revoke the run accounts' and the deployer's IAM | Stops new work and leaves every running resource billing — a stop that does not stop, and one that can brick the project's own recovery path. |

### 3.3 Where it runs, and the thing that decided this design

**The finding.** A managed project's premise is that the agent there holds broad rights: it shares the
container, the user, `/proc` and the metadata server with the runner (`SECURITY.md:71-81`), so it holds
whatever the run's identity holds. **Anything the hard stop is made of that lives in the managed project — the
topic, the subscription, the function, its account, its role binding — is therefore something that agent can
delete, disable or rewire with credentials it already has**, before or instead of spending. A hard stop inside
the blast radius it exists to limit is not a control; it is a promise, and it is precisely the failure the
marker's own design avoids by keeping `fugaro/managed.json` at the runs bucket's **root**, where every grant is
conditioned to a prefix below it and no credential a run holds has a write path
(`internal/infra/managed/marker.go:26-39`).

**The recommendation.** The enforcement lives in a **project of its own — a guardian project** — which no
credential a managed run holds has any IAM on, and which holds the function, its identity, the topic and the
subscription, and the record of what it did. One guardian per installation serves every managed project of that
installation, the way one deployer account serves them all (C8's answer of 2026-10-16). The guardian is the
**owner's own project**, created and applied by the owner outside Fugaro's managed apply. It does not have to be
a new project: **any project the managed project's identities hold no IAM on serves** — but "no IAM" has to hold
for **every agent that can run there**, not only the managed project's. An installation's own project runs
agents too: its job accounts share their container with an agent exactly as a managed run's do, so the guardian
may sit in such a project only if **no Fugaro run identity of that project holds any permission on the
guardian's topic, subscription, service or account** (asserted from the roles, the way §6 asserts the
credential), and **never in a managed project**, whose deployer and agent hold broad rights by design. A
dedicated project that runs no Fugaro job is the shape with nothing to assert, and is the recommendation; an
existing unmanaged installation is the cheaper alternative under the condition above — except `belong`, which
Global Constraint 7 puts out of scope for every step of this work, so it can never be the guardian. It does have to be **billable in its own right** (a
Cloud Run service in it needs the project to have a billing link), and that is legitimate rather than
self-defeating: unlinking a *managed* project's billing leaves the guardian's own link alone, which is exactly
why the two must not be the same project.

**What it costs, and the alternatives.** A second project, a second thing to deploy and to patch, and — the part
to weigh — **the guardian is now itself something that can silently stop working**: a guardian that is broken,
uninstalled or unlinked is a hard stop that does not exist, and nothing in the managed project would notice
(§7 item 3). The alternatives are both worse for the reason above: **in the managed project** — no new project,
and the enforcement is inside the blast radius, disarmable by the agent it is meant to bound; **in the managed
project plus an organization policy** protecting the topic from deletion — it needs an organization, which a
personal installation need not have, and it protects one resource rather than the mechanism.

### 3.4 The credential, and how far it reaches

The enforcement's identity must be able to do exactly one thing to the billing side: **remove this project's
billing association.** Three facts decide how narrow that can be made, and all three are reads this document
owes before anything is written (§8 items 2 and 3):

1. **Whether the permission can be bound on the managed project alone.** Google's billing pages name the unlink
   permission as **either** `billing.resourceAssociations.delete` on the billing account (Billing Account
   Administrator) **or** `resourcemanager.projects.deleteBillingAssignment` on the **project** (Project Billing
   Manager, or Owner) — read 2026-10-18, so the project-scoped form exists on paper (§8 item 2 says what is
   still owed). Bound that way, the credential's reach is the managed project and finding 25's "money-adjacent
   power" is a permission over **one project's link**; bound on the billing account, its reach is **every
   project on that account**. Google's own sample function is the wide form — it grants Billing Account
   Administrator — and **an implementation must not copy it**. The narrow form has its own price, and it is the
   one to watch: **a project-scoped binding lives in the managed project's IAM policy**, which is inside the
   blast radius §3.3 keeps the enforcement out of. It is safe only while **no identity a managed run can reach
   holds `resourcemanager.projects.setIamPolicy`** on that project — true today because D75's deployer holds no
   `resourcemanager.projects.*` at all (`deploy/terraform/gcp/modules/installation/deployer.tf:127`) — so the
   implementation must pin that absence as a test, because the day it stops being true the agent can remove the
   stop's binding and the stop silently stops existing.
2. **Whether a stop-only credential is constructible**: whether a custom role can hold the delete permission
   without the create permission, or whether the only usable role grants both. **What the design insists on
   either way**: the stop's credential is **never the apply identity** (D75's deployer, which holds no billing
   permission and must never be given one), holds **nothing in the managed project beyond that one billing
   permission** (no storage, no run, no IAM — so a compromise of the enforcement is not a way into any project),
   and is **used only to take the link away**. Whether a custom role may carry
   `resourcemanager.projects.deleteBillingAssignment` without `createBillingAssignment` is the open half (§8
   item 3); a relink also needs `billing.resourceAssociations.create` on the billing account, which a
   project-scoped stop credential would not hold, so even the predefined Project Billing Manager role cannot
   relink on its own. If the platform will not let it be stop-only, that is stated in the deployment's own output and
   in SECURITY.md rather than glossed.
3. **What the fallback is if the reach cannot be bounded**: **one billing account per managed project** — an
   account whose only project is the managed one, so the credential's reach and the project's are the same thing
   again — at the cost of losing consolidated invoicing and of the owner creating a billing account per managed
   project. This is the owner's call, not a detail: it is priced here so the reach can be chosen rather than
   inherited.

### 3.5 Why an agent can neither fire the stop nor disarm it

A stop an agent can **fire** is a denial-of-service lever on the owner's project; a stop it can **disarm** is no
stop. Each path, with what closes it in the recommended shape:

| Path | Closed by |
|---|---|
| **Spend to 100%** | Nothing, and nothing should: that is the event the stop exists for. It is not a lever, because it costs the agent the budget it would be stopped for. |
| **Forge a notification on the topic** | The topic is in the guardian, and its only publisher is the identity Cloud Billing publishes as, granted Pub/Sub Publisher on that topic when the budget's notification rule is set (Google's page does not name that identity: UNVERIFIED, §8 item 4); no run identity holds `pubsub.topics.publish` there. Even a forged message must then pass D83's exact match — the `budgetId` attribute equal to the one the creation fact records — so a message for any other budget never stops. |
| **Call the enforcement endpoint directly** | A push subscription delivers to an HTTPS endpoint, so the service must **refuse every caller but the subscription's own push identity** (an authenticated push with an OIDC token checked by the service, no `allUsers` invoker, ingress limited). An endpoint anyone can POST to is a public "stop this project" button; the implementation's tests must assert the refusal of an unauthenticated and of a wrongly-signed caller. |
| **Delete or rewire the topic, subscription, service or account** | They live in the guardian (§3.3), on which no run identity holds IAM. |
| **Remove the stop's project-scoped binding** | No identity a run can reach holds `resourcemanager.projects.setIamPolicy` on the managed project (§3.4 item 1) — an invariant the implementation pins. |
| **Edit or delete the budget, or move the project to another billing account** | All three are billing-account operations (or need `billing.resourceAssociations.create` on another account plus `createBillingAssignment`); no run identity holds a billing permission, and the deployer of D75 holds none. |
| **Run the relink (the undo)** | Refused under the agent markers and typed-confirmed (§4, D84), and it needs `billing.resourceAssociations.create` on the billing account, which only the owner holds. |

## 4. The recovery: what fires the stop, and how a false positive is undone

The two halves are one design, because a stop nobody can explain is the failure mode that gets the whole
mechanism removed (D62's row makes the same argument about a wrong cost block).

**What fires it (§5 D83, in outline).** Only an exact match: a notification whose `budgetId` attribute is **the
budget this project's creation fact records**, whose **actual** cost has reached the budget — `costAmount` at
or above `budgetAmount`, the comparison Google's own sample makes, with `alertThresholdExceeded` at or above
1.0 as a second witness (it is "the highest configured threshold exceeded", so a budget with a threshold above
1.0 reports that one, and an "exactly 1.0" test would never fire there) — **never** on
`forecastThresholdExceeded`, in the marker's currency, for a `costIntervalStart` the applier has not already
acted on, and not older than the period it names. 50% and 90% stay alerts. Because delivery is at-least-once,
a repeat of a period already acted on is the normal case and is a no-op, not an error. Everything else —
unparseable, unknown, mismatched, late, repeated — **never stops**, is recorded, and alerts. The direction is
deliberate and is the opposite of "stop on anything suspicious": a stop is expensive to undo and cheap to get
wrong, so it happens only on a message the applier can prove, and the cost of a missed stop is bounded by the
alert email and D63's human backstop, which remain the first line.

**How a false positive is undone (§5 D84, in outline).** By the owner, through one owner-run command that
relinks the project's billing account: **refused under each of the six agent markers**
(`internal/cli/initsecrets.go:132`, the refusal `fugaro allow-delete` already carries at
`internal/cli/allow_delete.go:114`), **confirmed by typing the exact project ID** (`initflow.Typed`), and
**recorded with who, when and why** — the shape `fugaro budget kill` / `resume` already has
(`internal/cli/budget.go:1188-1223`). The stop's own credential cannot undo it (§3.4): **the thing that stops
the money can only ever take the link away**, so no failure or compromise of the enforcement can re-enable
spending. Recovery is also **against the clock**: Google's pages say an unlinked project's resources *"might be removed
and become non-recoverable"* and publish no grace period on that page (§8 item 5 owes the deletion timeline it
links to), and that a relink can take up to 24 hours to bring resources back. A stop whose undo races data
loss is a different control from one whose undo does not, so the runbook the alert prints must say so, and the
false-positive review is urgent rather than routine. And nothing relinks automatically: spend cannot fall while the project is unlinked, so an auto-undo is a
loop, not a recovery.

## 5. The decisions

Each with this design's recommendation, **the cost of the alternative**, and an owner-answer column that is
**empty until the owner answers it**. These are **D79 to D86** — the next free numbers in the D-series, which
[managed-projects.md](managed-projects.md) §5 records (`:143`: "the next free number in the D-series is D79").
**This document does not edit that design's §5 and does not reserve a check row in the register**, for the rule
those files are already governed by: one writer per file, so this design writes itself and leaves `:143`'s
sentence, the plan's Task 6.0 row and the checklist to the tasks that own them.

| # | The question | This design's recommendation | The cost of the alternative | Owner's answer |
|---|---|---|---|---|
| **D79** | **What stops the money — the billing link, or the resources?** | **Unlink the managed project's billing account** (§3.2): one call, deterministic over everything that bills, leaving the data, the state bucket and the IDs in place, and undone by relinking. Scale-down and API-disabling are refused as the *mechanism* — the first is only as complete as a hand-written list (D63's own objection), the second is what `apis.tf` and `deployer.tf:112` already refuse in this tree — and deleting the project is refused because the reconciliation's §6 item 2 shows it is not disposable. **Priced honestly**: an unlinked project's data is at risk while the stop holds (Google: resources *"might be removed and become non-recoverable"*), so this is a stop that trades the bill for a recovery race (§4). | **Scale the workloads down** (§3.2): a project-scoped permission and no billing-adjacent credential at all, at the price of a stop that misses what nobody listed — a bucket, an egress line, a job under another name — which is the one outcome the hard stop exists to prevent. **Google's native spend cap** (§3.2) is the credential-free form of a scale-down for Cloud Run and Vertex AI only — D86 asks about it separately. | |
| **D80** | **What fires it?** | **The budget's own Pub/Sub notification** (§3.1), on the budget the managed creation already makes, at its 100% rule: it is the only channel that carries the figures D83's predicate needs, and it needs no polling and no billing export. | **A Scheduler poll of the Budgets API**: not an option — the API reports no spend (§8 item 1, settled). **The billing export**: exact and slow, buying again what the notification carries. **The alert email and a person**: no new privilege, and a stop that fires when somebody reads their mail. | |
| **D81** | **Where does the enforcement run, and what can disarm it?** | **In a guardian project of its own** (§3.3), which no credential a managed run holds has any IAM on — because an enforcement inside the managed project is disarmable by the agent it exists to bound, with credentials it already has (`SECURITY.md:71-81`), which would make the hard stop a promise rather than a control. One guardian per installation, created and applied by the owner, itself billable. | **In the managed project**: no second project and one less moving part — and the enforcement sits inside the blast radius it exists to limit. **Plus an organization policy** protecting the topic: needs an organization, and fences one resource rather than the mechanism. | |
| **D82** | **What does the credential hold, and how far does it reach?** | The narrowest thing the platform allows: **the managed project's own billing link**, if the permission can be bound on the project (§3.4 item 1); **stop-only**, if a custom role can hold the delete without the create (§3.4 item 2); **never the apply identity**, and **nothing in the managed project beyond that one permission** — whose binding is only as safe as the absence of `resourcemanager.projects.setIamPolicy` from every identity a run can reach (§3.4 item 1). Its reach is stated in the deployment's own output and in SECURITY.md, and if it cannot be bounded, the owner sees that and §3.4 item 3's fallback in front of them. | **Hand it to the operator's identity and stop by hand**: zero new always-on credential — the honest answer to a "no new credential" ruling — at the price of a stop that fires when a person is at a terminal. **One billing account per managed project**: bounds the reach by construction, at the cost of consolidated invoicing and one account per project. | |
| **D83** | **What exactly fires the stop?** | **An exact match, all of it required** (§4): the `budgetId` attribute is the budget the creation fact records; **actual** cost has reached the budget (`costAmount` ≥ `budgetAmount`, with `alertThresholdExceeded` ≥ 1.0), never a forecast; the currency is the marker's; the `costIntervalStart` has not been acted on before (delivery is at-least-once, so a repeat is a no-op); and the message is not older than the period it names. **Anything else never stops**, and is recorded and alerted. | **Stop on any 100% notification on the billing account**: fewer moving parts and no change to the creation fact — at the price of one project's overspend stopping **every** project on that account, because the message names the budget (`budgetId`) and carries no project field (§8 item 4, settled from the documented schema). | |
| **D84** | **Who can undo it, and what does recovery require?** | **The owner alone, through one owner-run, agent-refused command** that relinks the project's billing account, typed-confirmed with the exact project ID and recorded with who, when and why (§4) — `fugaro budget kill`/`resume`'s shape. **The stop's credential can never undo it**, and nothing relinks automatically. Recovery's **deadline** (if unlinked resources are at risk) is read before the implementation and printed in the runbook. | **Let the enforcement relink**: one component fewer, and the always-on credential then holds both directions of the money — the one thing §3.4 refuses. **Auto-relink when spend falls**: spend cannot fall while unlinked, so it is a loop. | |
| **D85** | **Is it opt-in, and what does the creation fact record?** | **Opt-in at creation and only there**, beside `--budget` and refused without `--create-project --managed`, with **no default** — D64's rule ("a silent default is a number the owner did not choose") applies with more force to a default that can stop a project. The creation fact gains **version 3**, recording that the hard stop is armed, **the budget's own identity** it answers and the guardian's name — written only by the owner-run creation, like today's fact. **And the coupling is stated rather than discovered**: `ReadBudget` (`internal/infra/managed/read.go:126`) is what opens the apply gate and refuses a version it does not know, so teaching the marker version 3 and teaching that read version 3 are **one change**. | **On by default for every managed project**: the control exists where it is wanted without a flag, at the price of an unattended stop nobody opted into, on a project whose premise is that an agent operates it. **A separate object instead of a version**: one more thing to find and to prove the bucket for, and a second authority beside the fact — the shape `marker.go:47-55` already refused once. | |
| **D86** | **Arm Google's native spend cap on Cloud Run and Vertex AI as well?** | **Yes, as the first layer, opt-in beside D85's flag**: one owner-run spend-cap budget per eligible service the project uses (`run.googleapis.com`, `aiplatform.googleapis.com`), at the same amount, created by the owner-run creation with the owner's billing permission. Google enforces it, so it adds **no credential, no guardian and nothing an agent can reach** — lifting it needs `billing.budgets.configureSpendCap` — and it pauses new usage of the two services that carry a run's own spend, which is the spend an agent drives. D79's unlink stays the backstop for what it does not cover. | **Without it**: the only stop is D79's, with its credential and its recovery race. **Spend caps alone, no unlink**: zero new privilege at all, at the price of leaving storage, Compute/Batch VMs, Cloud Build and every other service unbounded — the incomplete-list objection again, though Google maintains the list. Either way it is a release for "a limited set of services" whose launch stage, quota and Cloud Run-jobs coverage are UNVERIFIED (§8 item 8), and it lifts itself at each new monthly period. | |

## 6. How it is proven without a live cloud

Global Constraint 12: **no task's verification may need a real project or a billing account.** The mechanism is
therefore designed as a seam first, and the one live step is the owner's.

**The seam, and why it is this shape.** Three pieces, each testable without a cloud, and none of them able to
reach the network on its own:

1. **A pure decision** — parse the notification into a value, then decide over that value against the rules of
   D83 and answer with a verdict and its reason. No client, no clock and no network: every precondition gets a
   table-driven test, and the tests that matter most are the **refusals** (wrong threshold, wrong currency,
   wrong budget, malformed, repeated period, stale message).
2. **An applier behind one interface** — the unlink, expressed as a single call the decision never makes
   directly, so every test of the decision runs against a fake that records what it was asked to do, and the
   tests can assert that **a refused notification calls nothing at all**. That assertion is the whole of
   "never stops on anything it cannot prove", and it cannot be written against a live client.
3. **A record, written through the same seam** — what was stopped, when, on which notification, and by which
   rule, so D84's undo has something to read and the false-positive review has something to explain.

**The fakes, and the one addition they owe.** `internal/gcpfake` already models the billing read and write this
feature sits on (`internal/gcpfake/billing.go:15-40`; `UpdateBillingInfo` at `:157-185`), including the states
that make the preconditions testable — suspended, hidden, refused and closed. **It cannot yet express an
unlink**: any accepted update sets `enabled = true` and stores the account (`:179`), so the fake has to be
taught that an empty account name **removes** the link before a single test of the applier can mean anything —
a fake that cannot model the feature's only write is a fake that would let a wrong implementation pass.

**What is asserted where, with fakes.** The Terraform side is proven the way the tree already proves its roles
and modules: `terraform fmt`, `validate`, the module's own `.tftest.hcl` in the shape of
`deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`, and the golden plans regenerated with
`scripts/gen-golden-plans.sh` — and **the guardian's Terraform is a root of the owner's, not of a managed
apply**, so the hard stop widens nothing a managed plan may carry and the gate's allowlists are untouched. The
credential's narrowness is asserted from the role definition itself, one permission per line with its
justification, the way `deployer.tf`'s two custom roles are written today. The gate coupling of D85 is asserted
where `ReadBudget` already is: a version-3 fact read by a build that knows version 3, and a version it does not
know still refused.

**The one live thing: Check 71, owner-run, sandbox only, never `belong`.** What a fake cannot answer is what
this design calls the mechanism: that a real budget's 100% notification arrives with the fields D83's predicate
needs, that the stop actually removes the project's billing, and that relinking restores it. So the
implementation's live half is one owner-run check — the 100% notification stopping the **sandbox** project, the
record saying so, `fugaro managed relink` restoring it, and a 50% notification **not** stopping anything —
written as a row and a section in [docs/gcp-live-checklist.md](../gcp-live-checklist.md), with the register's
guard block re-run at every step, in a sandbox project only. **This document writes neither the row nor the
section** (the implementation task does, so one writer owns each). **Check 71 does not exist yet and is NOT RUN**: nothing about this
mechanism has been exercised against a project, and this document writes no row, no section and no result. The number is **71**, and the reading is the register's own: 58 to 68 are written or reserved by the console, budget-token-channel, project-centric, console-v2, voice and managed-projects lines, 69 (as 69a-69c) is the website work's, **70 (as 70a-70c) is the website redesign's** (`docs/website/redesign/implementation-plan.md` on `main`, merged in #560 — an earlier revision of this document took 70 before reading `main`), and the mobile work — which holds 80-89 carved out of the website's range — records **70-79 as "unused and deliberately not taken"** (`docs/mobile/README.md:38`), a sentence the redesign has already read as leaving them takeable. That leaves 71 the lowest number no line of work holds on `main`, on `integration-0.9.0` or in an open branch (read 2026-10-18). **If the owner wants 71-79 left unspent, this document changes nothing it has written**: the implementation task takes the number free at its own merge, which is the register's own rule for two claims in flight (`docs/gcp-live-checklist.md:183-185`).

## 7. What this design refuses to accept, and the residual risk

1. **D63's privilege objection is answered, not dismissed.** The enforcement does need a credential with a
   money-adjacent power, and no wording here removes that. What the design does is make it as narrow as the
   platform allows — one project, one permission, one direction, in a project the run cannot reach — and say
   plainly what is left: **a credential that can unlink billing can also be used to unlink billing.** If the
   permission cannot be bound on the managed project (§3.4 item 1), its reach is the billing account and every
   project on it inherits this risk, and the owner should decide that with §3.4 item 3's fallback in front of
   them.
2. **The stop stops the project, not the bill.** A budget notification fires when spend has crossed a fraction;
   the money is already spent, and the notification is not instantaneous — Google says the first notification
   "may take several hours", that there is "a delay between incurring costs and receiving budget notifications",
   and that actual costs can take more than 24 hours to appear. So the hard stop caps the spend **after** that
   lag, and its overshoot is hours of the project's burn rate — which is exactly why D63 keeps the alert as the first line and why this
   document treats the hard stop as the second one rather than as a budget.
3. **The guardian is a dependency, and a silent one.** A guardian that is broken, uninstalled, unlinked or
   quietly not firing is a hard stop that does not exist, and nothing in the managed project would notice.
   Whatever shape D81 takes, the design requires the guardian to be observed as itself (its own alert on its own
   failure), because a control whose failure is invisible is not a control.
4. **The stop's credential is not the apply's credential, and must never become it.** D75's deployer holds no
   billing permission and its role list says why it holds what it holds; this design adds nothing to it. An
   implementation that hands the stop to the deployer, or that lets an agent run the undo, is not a variant of
   this design — it is a different one, and a worse one.
5. **`belong` is untouched.** No step here names it; the guardian's project, its billing account and the
   one-account-per-project question are decisions the owner makes about their own accounts, at their own
   terminal, and never something a task of this plan does to an installation.
6. **The residual risk D55 accepted is unchanged by this feature.** In a managed project the agent's session
   applies with no prompt and shares the container and the metadata server with the runner
   (`SECURITY.md:71-81`). A hard stop outside the project bounds what that agent can spend; it bounds nothing
   else, and it does not make the apply path safer.

## 8. UNVERIFIED, to settle before the implementation starts

Each of these is a read or a decision, and none of them needs a live cloud except where it says so.

| # | What is unverified | How it gets settled |
|---|---|---|
| 1 | Whether the **Cloud Billing Budgets API reports a budget's spend** | **SETTLED 2026-10-18 (documentation read):** it does not. The v1 `Budget` resource's fields are `name`, `displayName`, `budgetFilter`, `amount`, `thresholdRules`, `notificationsRule`, `etag`, `ownershipScope` and `spendCap`, none of them a spend figure. The Scheduler poll of §3.1 is not an option. |
| 2 | Whether the unlink can be authorised by a role bound **on the managed project** — the fact that decides the credential's reach (§3.4 item 1) | **SETTLED on paper 2026-10-18:** the billing docs name `resourcemanager.projects.deleteBillingAssignment` on the project (Project Billing Manager, or Owner) as an alternative to `billing.resourceAssociations.delete` on the account. **Still UNVERIFIED:** the same page also lists a billing-account viewer role for disabling (likely for the console's account lookup, not the API call) — settled by Check 71 calling the unlink with the project-scoped credential alone. |
| 3 | Whether a **stop-only** credential is constructible — a custom role holding `resourcemanager.projects.deleteBillingAssignment` without `createBillingAssignment` | **UNVERIFIED.** Settled by a read of the permission's custom-role support level (the IAM permissions reference, read-only), then the role-definition assertion §6 describes. A project-scoped stop cannot relink anyway (§3.4 item 2), so this narrows the stop rather than deciding it. |
| 4 | **What the notification actually carries** | **SETTLED from the documented schema 2026-10-18:** attributes `billingAccountId`, `budgetId`, `schemaVersion`; data `budgetDisplayName`, `costAmount`, `costIntervalStart`, `budgetAmount`, `budgetAmountType`, `alertThresholdExceeded`, `forecastThresholdExceeded`, `currencyCode`; no project field; at-least-once, possibly out of order. **Still UNVERIFIED:** the identity Cloud Billing publishes as (the page does not name it) and one captured real message — the first owner run of Check 71. |
| 5 | Whether an **unlinked project's data is at risk**, after how long, and what relinking restores — **D84's clock** | **Partly SETTLED 2026-10-18:** at risk — "some of your Google Cloud resources might be removed and become non-recoverable"; relinking can take up to 24 hours to restart resources; an active or pending CUD refuses the unlink. **UNVERIFIED:** the deletion timeline the billing page links to, per service the installation uses (Cloud Storage, Firestore, RTDB, Secret Manager) — a documentation read before the implementation; the runbook carries its answer. |
| 6 | The reconciliation's own **U2** (`managed-projects.md:236`): a topic in another project, without a topic per project | **SETTLED on paper 2026-10-18:** the notification rule takes a full `projects/{projectId}/topics/{topic}` path, Google recommends a separate billing-administration project for these topics, and the caller needs `pubsub.topics.setIamPolicy` on the topic. Several budgets may name one topic; D83's `budgetId` match is what tells them apart. |
| 7 | Whether an existing budget can be **updated** to carry `all_updates_rule` by an owner-run apply, on an installation whose budget was created before it | **UNVERIFIED.** A `terraform plan` in the sandbox by the owner, when the implementation is written — the only step here that needs a project, and it is the owner's, read by them. |
| 8 | The **native spend cap** of §3.2 and D86: its launch stage, whether `run.googleapis.com` covers Cloud Run **jobs** (the shape a Fugaro run is) as well as services, any per-account limit on spend-cap budgets, and whether the Terraform provider's `google_billing_budget` exposes `spend_cap` | **UNVERIFIED.** The API reference (read 2026-10-18) documents the field and its states; the how-to page lists the four eligible services and says "In this release"; the release notes describe "a limited set of services". A documentation and provider-schema read before D86 is answered; the jobs question is settled live only, in Check 71. |

## 9. What follows this document

**Nothing follows it until the owner answers D79 to D86.** Answering a decision is not approving a task (the
reconciliation's §8 rule 5), and this document's own approval gate is the same one: a task exists only after
the answers are in, and it starts on an explicit go of its own. The shape it should take is fixed by what is
above and by nothing else:

- **The decisions first**, then an implementation task whose Files are, in outline: the decision and its tests
  (a new package beside `internal/managed/`), the applier behind its interface and the fake that owes an unlink
  (`internal/gcpfake/billing.go`), the creation fact's version 3 and `ReadBudget` taught it in the **same**
  change (D85), the owner-run relink command beside `internal/cli/allow_delete.go` and registered in
  `internal/cli/root.go`, the skill line `plugin/skills/setup/SKILL.md` (Global Constraint 13), and — for the
  guardian — a Terraform root or module of the owner's, **outside** the managed project's roots, with its role
  written one permission per line with a justification.
- **The check's row and section** are that task's, not this document's, and the number is §6's.
- **The five things an implementation must not do**: put the enforcement inside the managed project (D81);
  hand the credential to the apply identity (D82, §7 item 4), or copy Google's sample and grant it Billing
  Account Administrator (§3.4 item 1); let an agent — or the enforcement itself — relink (D84); accept a call
  on the enforcement endpoint from anything but the subscription's own push identity (§3.5); or give any
  identity a managed run can reach `resourcemanager.projects.setIamPolicy` on the managed project, which would
  let it remove the stop's binding (§3.4 item 1).

**Nothing in this document is built, no row or section of the live checklist is written by it, and the
`.github/workflows` tree, every secret, and the `belong` installation are untouched by it.**
