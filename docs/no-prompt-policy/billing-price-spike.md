# The build price: the Cloud Billing spike T8 was held for

**Status: spike. Docs only: no code, no grant written, no write to any cloud.** It resolves the
UNVERIFIED the plan left open — *"D1550's Billing grant"* (reconciliation §6) — and answers the six
questions the owner asked of it. Everything below is either a quotation from Google's own
documentation (by URL, in §7), a fact read out of this repository (by path and symbol), a fact read from
the API itself by the owner's probe and dated where it appears (§8), or marked **UNVERIFIED**.

**The two facts this spike could not read for want of a credential were verified by the owner's own
probe on 2026-11-08** (§1.4, §1.5, and the record in §8): their OAuth login, `X-Goog-User-Project` naming
their own installation project, **no billing role and no API key**, read-only — the two `curl` calls the
spike asked for, both answered. What that verifies is the **user path** — a person's own credentials,
naming the installation project as their quota project, need no IAM grant in `deploy/terraform` and no
`grantRules` row, the Cloud Billing API on that project being the only installation change.

**It does not verify the job path**, and the header says so rather than rounding up: the probe sent a
*user* token **with** the header, while §1.2's split rule has the catalog check job's client send **no**
quota project at all on its service account (§9 item 1). Whether that request is billed to the account's
own project, and whether a service account naming its own project needs `serviceusage.services.use`, is
what keeps the job's reach unchanged — so the `installation/iam.tf` entry stays on T8's file list and the
level-3 security review stays conditional on T8's live run of that path, not cancelled by this probe.

Two kinds of evidence weaker than the probe are marked where they appear: a **spot observation** from an
unauthenticated request to a public endpoint (§1.4), reproducible only in outline, which never carries a
conclusion on its own, and the probe's own **single read of the catalog** (§1.5), which is what one call
at one moment returned and not a guarantee about tomorrow's catalog. The spike itself made no live cloud
read and used no credential: its own network calls were to Google's public documentation and a handful
of unauthenticated requests to a public endpoint — no token, no key, no project.

**Decisions this answers:** D1550 (the rate is queried from Cloud Billing at run time), tension T5,
F19 (the lease path), F5 (the estimator), and T8's file list (`iam.tf` *"only if the spike finds a
grant is needed"* — **it finds none on the user path**, 2026-11-08; the job path is §9 item 1, which
T8's live run closes).

## 0. The answer, in one screen

| Question | Answer |
| --- | --- |
| Which API | `cloudbilling.googleapis.com`, the **Cloud Billing Catalog API**: `services.list` then `services.skus.list` (GA, `v1`), whose `Sku.pricingInfo` carries the public list-price timeline. The newer **Google Cloud Pricing API** (`v2beta/services`, `v2beta/skus`, `v1beta/skus/<id>/price`) is **Preview** and does the same for public data. |
| Which role | **None.** Google documents *"You don't need any specific IAM permissions to make this API call"* for the public services and SKUs. |
| Which grant to write in `iam.tf` | **None on the user path** — concluded, and **verified 2026-11-08** with the credential (§1.4, §3, §8). Not a new `grantRules` row either, and the only installation change is enabling the API (§3): the lookup runs only where the budget backend is on, which is where the person's `serviceusage.services.use` already comes from (§1.2). The **job** path is the split rule's other half and the probe did not read it (§9 item 1) — so T8 keeps `installation/iam.tf` on its file list, and the level-3 review, until its own live run of that path says no grant there either. |
| Which credential | The launching identity's **own OAuth credentials**; no API key anywhere in the design. **Verified 2026-11-08** by the owner's probe (§1.4): `GET /v1/services` answered **200** to `Authorization: Bearer $(gcloud auth print-access-token)` with `X-Goog-User-Project` naming their installation project, **no billing role and no key**. |
| Contract prices | **Not reachable from a project**, by design: they need `billing.billingAccountSkus.list` / `billing.billingAccountPrice.get` on the **Cloud Billing account** (`roles/billing.viewer`/`roles/billing.admin`, whose lowest grant level is the billing account). A project module cannot grant it. The cap needs an upper bound, and the **list price is one** — so this is a limit we do not need to cross. |
| The recommendation | The lookup, as D1550 says, with three amendments and a per-process cache (§6). The user path's fact gating it was verified 2026-11-08 (§1.4), and T8's live run closes the job path (§9 item 1); had the token route failed, the fallback was the pinned dated table, which is the smallest design that still caps builds in dollars. |

## 1. Which API gives a Cloud Build machine-minute price

### 1.1 Two APIs, one data source

| | Cloud Billing Catalog API (`v1`, GA) | Google Cloud Pricing API (`v1beta`/`v2beta`, **Preview**) |
| --- | --- | --- |
| Services | `GET /v1/services` | `GET /v2beta/services` |
| SKUs | `GET /v1/services/{serviceId}/skus` | `GET /v2beta/skus?filter=service="services/<id>"` |
| Price | inside `Sku.pricingInfo` (`effectiveTime`, `summary`, `pricingExpression`, `aggregationInfo`) | `GET /v1beta/skus/{skuId}/price`, and `…/prices` for all |
| Region | `Sku.serviceRegions` | `geoTaxonomy` (`REGIONAL` + `regionalMetadata.region`) |
| Who reads it | any caller with an **identity** — an API key, or a token; no IAM permission exists for the public data | the same |

The Preview page describes itself as *"a set of API resources that helps you get the prices and
other information for Google Cloud services"* and says of the public half: *"To get information
about public services, SKUs, or prices, you must create an API key."* It is under the Pre-GA terms
(*"Preview … subject to the 'Pre-GA Offerings Terms'"*), which is a reason to prefer the GA `v1`
catalog for a launch-path read.

Both give the **list price**. The billing-account half of the Preview API is the only thing that
returns `contractPrice`, and it is out of reach (§1.3).

`PricingExpression` is the shape to be careful with, because the *unit* is data, not a constant:
`usageUnit` (`"h"`, `"GiBy.mo"`, …), `usageUnitDescription`, `baseUnit`, `baseUnitConversionFactor`
and `tieredRates[]` (sorted by ascending `startUsageAmount`, each a `unitPrice`). Cloud Build's
machine-time SKUs came back with `usageUnit` `min` and its network SKUs with `GiBy`, in the owner's
probe of 2026-11-08 (§1.5); **the code must still read the unit and refuse anything it does not
recognise**, the same way it refuses an unknown machine type (§4).

### 1.2 Enabling it, and on which project

The public-price page says: *"Before you begin: Enable the Cloud Billing API, if it is not already
enabled."* Enabling needs `serviceusage.services.enable` (`roles/owner`, or
`roles/serviceusage.serviceUsageAdmin`). The API is enabled **on a project**: the API key's project
for the key route, or the caller's quota project for the OAuth route.

This matters here, in three ways:

- `deploy/terraform/gcp/modules/installation/apis.tf` enables `run`, `storage`, `secretmanager`,
  `artifactregistry`, `cloudbuild`, `cloudscheduler`, `logging`, `monitoring`, `iam`,
  `cloudresourcemanager`, `cloudasset` and (conditionally) `aiplatform`/`billingbudgets` —
  **`cloudbilling.googleapis.com` is not in that list**. Fugaro's own live checklist has the
  operator enable it by hand today, on the **ADC quota project** (`docs/gcp-live-checklist.md:1339`)
  — the arrangement the next bullet says a launch path may not rely on.
- **Which quota project the client names depends on the credential, and getting that wrong is the one
  way this design grows a grant.** The repo's own Cloud Billing client is the counter-example to
  avoid: `internal/billing/accounts.go:249-271` `newService` passes **no** `option.WithQuotaProject`
  and leans on whatever quota project the credentials themselves name — its own comment says so:
  *"including their own quota project, which the Cloud Billing API needs and which is never the project
  being set up"* — and `ErrNoQuotaProject` (`:168-174`) exists for the case where they name none, when
  Google bills the call to its own project. That is exactly the hand-enabled arrangement
  `docs/gcp-live-checklist.md:1339` records and `internal/infra/firebase.go:498-503` reports as an
  error, so **a launch path must not inherit that choice** — but the choice is not one setting for
  everyone. `option.WithQuotaProject(p)` makes the client send `X-Goog-User-Project: p` on **every**
  request, service-account credentials included: the header goes out whenever a quota project resolved
  non-empty, whatever the credential kind is
  (`google.golang.org/api@v0.299.0` `transport/http/dial.go:189,240-242`). So:
  - **User ADC — the person's CLI, and an agent on their credentials — passes
    `option.WithQuotaProject(<installation project>)`**, the pattern the Cloud Run/Logging backend
    already uses (`internal/backend/gcp/gcp.go:67-82`, `option.WithQuotaProject(o.GCPProject)`). A user
    credential has no project of its own and some APIs refuse it without a quota project (the repo says
    so at `:79-80`, and sets the option at `:81`), and the alternative fallback is the user's own ADC
    file, whose `quota_project_id`
    may name **any** project of theirs (`google.golang.org/api@v0.299.0` `internal/creds.go:285-303`:
    precedence is the client option, then `GOOGLE_CLOUD_QUOTA_PROJECT`, then the credentials file) — a
    project `fugaro update` does not own and where the operator would have to enable the API by hand.
    **This path needs no new grant *where the budget backend is on*** — the person holds
    `roles/serviceusage.serviceUsageConsumer` on the installation project there
    (`deploy/terraform/gcp/modules/firebase/iam.tf:109-115`, `usage_consumer` over
    `setunion(local.people, local.admins)`). **And where it is off, the lookup must not run at all.** The
    grant comes from the Firebase module, which only `--firebase-project` applies — *"It is the flag that
    asks for the backend — a run without it builds none"* (`internal/cli/init.go:422`), and the stage
    skips itself without it (`internal/cli/init_stages.go:947-951`, *"no --firebase-project given"*) —
    while builds happen on such an installation too. So on a backend-less project a lookup naming the
    installation project would be **refused** for want of `serviceusage.services.use`. The way out is not
    a grant: the lookup exists to feed a **dollar reservation**, and the reservation is the backend's
    ledger, so without the backend there is nothing to reserve and nothing to look up. That is the repo's
    own pattern for a ledger feature on a project with no backend — return before the call
    (`internal/cli/accounts.go:262-264`, whose remedy sentence is `fugaro update --firebase-project <p>`;
    `internal/cli/api.go:159` refuses the same way for the spend history). §3 records what the alternative
    would cost if the owner ever wants a price without the backend, and §6 makes the gate a T8 test.
  - **Service-account credentials — the catalog check job, and the history job — pass no quota project
    at all.** Then the client sends **no** `X-Goog-User-Project` header, and the request is billed to
    the account's own project: the installation project, where this section enables the API. Nothing
    supplies a quota project in that case unless something asks for one — with no client option the
    only fallbacks are `GOOGLE_CLOUD_QUOTA_PROJECT` and a `quota_project_id` in the credentials file,
    and a service-account key file carries `project_id`, not `quota_project_id` (the library's own
    fixtures give that field only to a user-account file). **`serviceusage.services.use` is therefore
    not in play for the job, and no `grantRules` row is needed** — which matters more than it looks:
    `toCatalogCheck` is a principal kind of its own, the one row that accepts it being
    `roles/storage.objectUser` on a bucket and only under one repository's records condition
    (`internal/infra/tf/cover.go:223`, the `catalogRecordsGrant` check at `:1982`), so a grant carrying
    this permission to that account is not an ordinary covered change at all; it reaches the owner as a
    question from `fugaro update`, which the HARD RULE says an upgrade must not need.
- On the installation project, the user path's enablement needs nothing new **there**, because the one
  permission set that lets a credential use a project as its quota project is already granted to the
  installation's **people and admins** by the module the backend turns on
  (`deploy/terraform/gcp/modules/firebase/iam.tf:109-115`; the `grantRules` row that covers the grant
  is `roles/serviceusage.serviceUsageConsumer: toPeople | toAccounts` at
  `internal/infra/tf/cover.go:189`). The only other holder anywhere is the **history account**
  (`:117-121` `history_usage`), whose Firestore client sends that header too (`:102-108`) — and that
  comment is the closest thing in the repo to the open question §3 argues about: it concludes that
  sending the header *"requires serviceusage.services.use on it"*, which is the reading under which
  the header is not free even for a caller naming its own project. **The spike does not lean on it
  either way**, and does not claim to know why that one account holds the role; §3 says what T8's live
  run settles. What is certain is the rule's asymmetry: the header goes out only when a client names a
  project, so a client that names none cannot need the permission — and on an installation whose backend
  is off, the client names none because the lookup does not run (the bullet above).

So the enablement, if the lookup ships, is one line in `apis.tf`'s `local.apis` — a
`google_project_service`, a type `allowedTypes` already covers (`internal/infra/tf/cover.go:22`), and
*no* `grantRules` change — **provided the client names a quota project only for user credentials**; the
same requirement is stated again in §2's table, §3's table and §6.

### 1.3 Contract and negotiated prices

The billing-account half needs, verbatim from the same page: `billing.billingAccountServices.list`,
`billing.billingAccountSkus.list`, `billing.billingAccountSkuGroups.list`,
`billing.billingAccountSkuGroupSkus.list` and `billing.billingAccountPrice.get` — *"If you're calling
the API using a service account, the service account must have the permissions"* — and the only
predefined roles holding them are **Billing Account Administrator** and **Billing Account Viewer**.
`https://cloud.google.com/billing/docs/how-to/billing-access` (and its IAM twin,
`https://cloud.google.com/iam/docs/roles-permissions/billing`) states their lowest grant level
explicitly: *"Lowest-level resources where you can grant this role: Billing Account."*

Three consequences, and they are the heart of this spike:

1. A project module — every module in `deploy/terraform/gcp/modules/**` — **cannot** grant them. A
   `google_project_iam_member` with `roles/billing.viewer` would be a no-op at best; the role's
   permissions over a contract live on the billing account.
2. Therefore **the lookup must be built on public list prices.** It does not need a grant at all.
3. That is the *correct* number anyway: the estimator is an **upper bound used to reserve** (D1543:
   *"build timeout × machine rate, an upper bound"*), the settle uses actual spend, so a list price
   at or above the contract price reserves conservatively. *Assumption, stated:* a negotiated
   contract price is not **above** the public list price. If an installation's contract ever priced
   a SKU above list, the reservation would be optimistic by the difference — and the per-run cap
   would still bound it. Fugaro cannot see that case without a billing-account role, so it cannot
   detect it; the assumption is written here so a reader can attack it.

### 1.4 Credential: the caller's own OAuth credentials — **verified 2026-11-08**

What is documented, and what was observed:

- Every public example on the pricing page sends `…?key=API_KEY`, and the page says *"you must
  create an API key"*.
- Every billing-account example sends `-H "Authorization: Bearer $(gcloud auth print-access-token)"`
  and is what the IAM permissions above are for.
- *Spot observation, not a quotation, and the wording is not stable.* A request to the public
  endpoint with no credential at all is refused — HTTP 403 on every attempt — but **the body differs
  between identical requests**, so no argument here may rest on the text. Re-run 2026-11-07 against
  `GET https://cloudbilling.googleapis.com/v1/services`, the same URL seconds apart answered both
  *"Method doesn't allow unregistered callers (callers without established identity). Please use API
  Key or other form of API consumer identity to call this API."* (plain, `status: PERMISSION_DENIED`)
  and, with a `google.rpc.ErrorInfo` detail, *"Permission denied on the service."* (`reason:
  ACCESS_DENIED`) — in both orders, and with and without `?pageSize=5`, which does not predict which
  comes back. What holds every time is the status, not the sentence: the endpoint wants an
  **identity**, and an API key is one form of it. With a well-formed but unissued key the answer is
  instead HTTP 400 with `reason: API_KEY_INVALID` (*"API key not valid…"*), which is the stable one and
  is what the pricing page's examples exist for. Neither says which IAM a *token* would need.
- No Cloud Billing IAM role or permission for the **public** catalog exists at all: neither
  `https://cloud.google.com/billing/docs/how-to/billing-access` nor
  `https://cloud.google.com/iam/docs/roles-permissions/billing` lists a `services.skus.*`
  permission, while both list the `billingAccount*` ones the private half needs.
- This repository's own plan already assumed the token route works:
  `docs/plans/2026-09-27-m4-gcp-backend.md:3838` — *"…or an `Authorization: Bearer $(gcloud
  auth print-access-token)` header, sent through `curl --config` so the token stays out of argv."*

**Verified 2026-11-08.** The owner ran exactly that call, on their own OAuth login, with **no billing
role and no API key**, naming their installation project (`fugaro-dev`) in `X-Goog-User-Project`:

```sh
curl -sS --config <(printf 'header = "Authorization: Bearer %s"\nheader = "X-Goog-User-Project: %s"\nurl = "https://cloudbilling.googleapis.com/v1/services?pageSize=5"\n' \
  "$(gcloud auth print-access-token)" "<the installation project>")
```

It answered **HTTP 200 with the public service list**, and so did the same call at `?pageSize=1000` and
`?pageSize=5000` — the last two returning the whole catalog, Cloud Build among it (§1.5). What that
settles, and what it does not:

- **The token route is real.** An OAuth caller with no billing role reads the public catalog, so the
  documentation's *"You don't need any specific IAM permissions to make this API call"* is not confined
  to the API-key examples, and **no API key exists anywhere in this design**. §3's key row and §6's
  amendment 3 are therefore settled rather than conditional: the token is the route, and the widening a
  key would have bought is not bought.
- **The header the user path will send is accepted.** The probe's `X-Goog-User-Project` naming the
  installation project is the header §1.2's split rule has the user path send, and the call behind it
  answered 200. It does not show *which* permission made it succeed — the caller's
  `serviceusage.services.use` on that project is the reading §1.2 gives, and the probe cannot separate
  "the header is free" from "the person holds the permission" (§9, item 1, for the service-account half
  of the split, which sends no header at all).

The second header is not decoration: without it a user token is billed to Google's own quota project,
which is the case `internal/billing/accounts.go:168-174` `ErrNoQuotaProject` describes and not the one
the lookup will be in (§1.2). With it, the caller names a project — which is why §1.2 splits the option
by credential kind rather than setting it once.

The unauthenticated 403 above decides neither way: Google's frontend refuses the caller before any IAM
is consulted, so only the same call **with** a token separates "public data, no permission" from "the
token needs a role" — which is what the probe did.

The recipe is this repo's own for a bearer token, and it is why the token stays out of argv:
`curl --config` reads the headers from a process substitution and `printf` is a shell builtin, so no
process's command line carries it (`docs/git-providers.md:82-85`; `docs/gcp-live-checklist.md:7415`
states the rule for its own token — "`ps` shows a process's argv to every user of the machine for as
long as the process lives" — and a token passed as `-H "Authorization: Bearer $(…)"` is readable by any
local user for its lifetime, and these are full-identity tokens).

### 1.5 The Cloud Build serviceId, its SKUs, and the shape T8's parser reads — **verified 2026-11-08**

**Verified 2026-11-08** by the owner's own probe, the same identity and header as §1.4, read-only. Two
facts, and then the shape:

- **The service.** Cloud Build's entry in the public service list is `serviceId` **`8B5D-EF7D-EB12`**,
  found by its `displayName` `"Cloud Build"`. `GET /v1/services` is one read and answers **200** at
  every page size asked — `5`, `1000` and `5000` alike, the whole public service list.
- **The SKUs, in one read.** `GET /v1/services/8B5D-EF7D-EB12/skus?pageSize=5000` answered **200 with
  370 SKUs in a single page and no `nextPageToken`**, so the whole Cloud Build catalog is one request
  today. A `nextPageToken` is still followed when one arrives — the catalog grows — but no page loop is
  needed to read it now, and 370 is a reading of one moment rather than a constant.

**The SKU shape, which is what T8's parser is written against.** Every SKU carried the fields §1.1
lists; the three the lookup uses are:

| Field | What the probe read there |
| --- | --- |
| `description` | prose naming the machine family, the resource, the unit and the region: `"E2 ram utilization per minute in us-west1"`, `"C3 CPU utilization per minute in asia-southeast1 (Private Pool)"` |
| `serviceRegions[]` | the regions the SKU is priced in — which is why a price is looked up per `build.region` and not once per machine type |
| `pricingInfo[0].pricingExpression.usageUnit` | `min` for machine time and `GiBy` for network — **the unit is data, so a SKU whose unit the parser does not know refuses** (§1.1, §4) |

Two properties of that `description` the mapping has to carry, both readable from the two examples:

- **It is per resource, not per machine type.** The descriptions the probe read price CPU utilization
  and RAM utilization separately, inside a named family and region, and name no Cloud Build machine type
  (`E2_HIGHCPU_8`). So the table T8 pins is machine type → the SKU(s) whose description matches it, and
  whether one machine type is one row or a CPU row and a RAM row summed is T8's to pin, with
  `TestBuildCostIsAnUpperBound` freezing the ids and the arithmetic (§6, amendment 2).
- **It can name a pool the build is not in.** `"(Private Pool)"` appears in a description, and Fugaro
  submits to the **default pool** (the bullet below), so a SKU whose description carries a pool marker
  is not where a build's price comes from.

**And the rule the shape is read under: unknown or unmapped refuses.** An unknown `serviceId`, an
unmatched `description`, a `usageUnit` the parser does not know or a region the table does not carry is
a refusal, never a default — the owner's *"a missing or unknown price refuses, never defaults"*, kept
here as the fact the parser is written against (§4's unknown-or-unmapped row; §5's hostile-config
defence).

What is known from the documentation and stays true:

- Cloud Build bills build-minutes: *"Cloud Build charges for build-minutes consumed. A build-minute
  is incurred every minute that a build is in process. Partial minutes are billed at the actual
  number of seconds consumed."* The rate is a function of **machine type and region**
  (`https://cloud.google.com/build/pricing`, machine-type tables per region).
- Each billing account has *"2,500 free build-minutes per month"* (e2-standard-2, default pool).
  The estimator must **ignore** it: Fugaro cannot know how much of the month's free tier is left,
  so ignoring it keeps the number an upper bound.
- Private-pool (worker pool) pricing is a separate table; Fugaro sets no `Options.Pool` — the request
  carries a service account, a machine type, a logging mode, a timeout and a queue TTL and nothing
  else (`internal/backend/gcp/build.go:227-240`) — so it submits to the **default pool**, and the
  default-pool table is the right one.
- The two commands the owner ran on 2026-11-08 — and what T8's live check repeats — with the token in a
  `curl --config` process substitution for the reason given in §1.4:

```sh
# the Cloud Build serviceId
curl -sS --config <(printf 'header = "Authorization: Bearer %s"\nheader = "X-Goog-User-Project: %s"\nurl = "https://cloudbilling.googleapis.com/v1/services?pageSize=1000"\n' \
  "$(gcloud auth print-access-token)" "<the installation project>")
# its SKUs: ids, displayNames, serviceRegions, pricingInfo
curl -sS --config <(printf 'header = "Authorization: Bearer %s"\nheader = "X-Goog-User-Project: %s"\nurl = "https://cloudbilling.googleapis.com/v1/services/<serviceId>/skus?pageSize=5000"\n' \
  "$(gcloud auth print-access-token)" "<the installation project>")
```

## 2. Which identity launches builds, and what each would need

All three paths submit through the Go Cloud Build client with the caller's Application Default
Credentials (`internal/backend/gcp/build.go:405-417` `NewBuilder`, `:490` `Submit`, `:503` `create`),
and **the build always runs as the repository's own build account**, never the project default and
never a person: `internal/backend/gcp/build.go:227` sets
`b.ServiceAccount = projects/<p>/serviceAccounts/<s.ServiceAccount>`, and `:363` refuses any account
that is not `fugaro-b-<repo>@<project>.iam.gserviceaccount.com`.

| Path | Submit identity (the credential the API call carries) | Build runs as | Submitting today | What a billing read would add |
| --- | --- | --- | --- | --- |
| A person's CLI at a terminal | Their **ADC** — `authorized_user`, i.e. the person (`internal/cli/init_project.go:490` `credentialNote` names it; `GOOGLE_APPLICATION_CREDENTIALS` can override it with a service-account key). `gcloud auth application-default login` is the documented way to get it. | The repository's build account | `cloudbuild.builds.create` through the custom role `roles/fugaroBuildSubmitter` (`deploy/terraform/gcp/modules/installation/iam.tf:35`, granted to `var.operators` at `:77` `operator_build_submitter`) | Nothing to grant, **on an installation whose budget backend is on**: `cloudbilling.googleapis.com` enabled on the installation project, which the lookup's client names as its quota project (`internal/backend/gcp/gcp.go:67-82` is the pattern; `internal/billing/accounts.go:249-271` is the counter-example that must not be copied, §1.2), and `serviceusage.services.use` there — held because the Firebase module grants it to people and admins (`firebase/iam.tf:109-115`). That module is applied only with `--firebase-project` (`internal/cli/init.go:422`, `init_stages.go:947-951`), so on a **backend-less** installation the person holds neither the ledger nor the permission, and the lookup must not run there at all: there is no dollar cap to feed (§1.2, §3). |
| An agent's session, on the person's credentials | **The same identity.** Nothing in the credential path reads `internal/agentenv`'s markers: an agent runs the same binary with the same ADC, so it submits as the person and reads prices as the person. | The repository's build account | the same `roles/fugaroBuildSubmitter` | The same, and no more. A price is public, read-only data: an agent that can build can already spend the money, and reading the rate it is charged at is not a privilege change (F20's trust probes are unchanged). |
| The catalog check job (`fugaro-catalog-check`) | Its **own service account** (`installation/catalogcheck.tf:43`), on the job `fugaro image check --job --catalog` (`:99`, `:125`) | Each repository's build account, per repository | `projects/<p>/roles/fugaroBuildSubmitter`, granted by `installation/catalogcheck.tf:33-41` `catalog_check_build_submitter`, plus `roles/iam.serviceAccountUser` on each repo's build account (`modules/repo/catalogcheck.tf:24`) | The API enabled on the project, and — for the **key** route — the key delivered to the job. That is a new secret on the launch path and *a new `grantRules` row* (§3). For the **token** route, **nothing, and the lookup's client must pass no quota project here**: with service-account credentials that means no `X-Goog-User-Project` header is sent at all, so the account's own project — the installation project, where §1.2 enables the API — is the one billed, and `serviceusage.services.use` never enters the picture (§1.2). Note what **not** to cite here: `firebase/iam.tf:102-108` reads the other way — that client sends the same header and the module concludes it needs `serviceusage.services.use` — and its parenthetical about a service account's *own* project is not a claim this doc can rest on. What the doc rests on is narrower and verifiable: the job's client passes **no** quota project, so no header is sent, so the project-consumer check that permission guards is not reached (`§1.2`). And there is no second project to point at in the first place: `checkFirebaseProject` (`internal/cli/init.go:1427`) **refuses** a Firebase project other than the installation's own GCP project, so a differing pair is not the explanation for the history account's grant. And, like every lookup, the job reads a price only where the budget backend is on — the job itself is gated on `catalog_check`, which is a different switch (§1.2). |

Two notes a reader should not have to rediscover:

- The Go comment at `internal/infra/catalogcheck.go:53` and `internal/cli/image_check_catalog.go:50`
  says the job's account *"holds no project role at all"*. Read against the Terraform that is
  narrower than it sounds — the job **must** hold `roles/fugaroBuildSubmitter` to submit anything
  (`catalogcheck.tf:33-41`). The comment is about *database* roles in the sentence before it (the
  published catalog is an RTDB node); it reads as a stronger claim than the fact. Worth a wording fix
  in T10, not a code change here.
- The deployer service account (`fugaro-deployer`) is **not** on the build path: it is assumed only
  by a managed Terraform apply (`internal/managed/deployer.go`). Nothing in the lookup needs it, and
  nothing here widens it.

## 3. The smallest grant, if any, and where it would live

**The recommendation needs no IAM grant on the user path, and no new `grantRule`.** The job path's
half of the table is the `serviceUsageConsumer` row below, whose *No* rests on the split rule's
unprobed half — T8's live run is what confirms it (§9 item 1).

| Change | Where | Needed? |
| --- | --- | --- |
| Enable `cloudbilling.googleapis.com` | `deploy/terraform/gcp/modules/installation/apis.tf`, in `local.apis` — on the **installation project**, which the lookup's client must name as its quota project **for user credentials** (§1.2), and where the budget backend lives | **Yes**, if the lookup ships, and it is the **only** installation change. A `google_project_service`, already in `allowedTypes` (`internal/infra/tf/cover.go:22`), so no cover change. It is not enabled today. Without the quota project on the user path, this row is not enough and the enablement lands on the operator's own project instead — an operator step (HARD RULE). |
| Any `google_project_iam_member` granting a billing role | `installation/iam.tf` | **No.** A project grant of `roles/billing.viewer` would not carry the permissions anyway (§1.3). Do not write it. |
| `roles/serviceusage.serviceUsageConsumer` for the catalog check account | `installation/catalogcheck.tf`, and a new `toCatalogCheck` row in `cover.go`'s `grantRules` | **No, and it would not be an ordinary row** (see below). The job's lookup client passes no quota project, so the job never needs the permission; only the **user** path does, and the person already holds it. |
| `roles/serviceusage.serviceUsageConsumer` for the operators, so the user path also works **without** a budget backend | `deploy/terraform/gcp/modules/installation/iam.tf` (`var.operators`, as `operator_build_submitter` is granted at `:77-85`) | **No** — the lookup does not run without the backend (§1.2). This row is recorded because it is the one real alternative: it would be **covered by an existing `grantRules` row** (`toPeople`, `internal/infra/tf/cover.go:189`, so no new row and no secret), it grants a permission the same person already has on every installation whose backend is on, and it would let a backend-less installation read a price it has no ledger to spend against. If the owner wants that, it is one `google_project_iam_member` and T5's *"a new grant in a security-sensitive module"* becomes live. |
| A new `grantRules` row | `internal/infra/tf/cover.go:143` | **No** for the token route. |
| A new `grantRules` row for a **secret**, if an API key is chosen | `cover.go`'s `google_secret_manager_secret_iam_member` row | **No** — the token route is verified on the **user** path (§1.4, 2026-11-08), and that is the path a key would have existed for; on the job path the same route is T8's live run (§9 item 1), so no key exists today and this row is written only if that run fails. Had a key been needed it would have been a **widening**: that row accepts no `toCatalogCheck` member (`cover.go:233-250`), and the catalog check account is a principal kind of its own (`toCatalogCheck`, `cover.go:127`, matched by `isCatalogCheckMember` at `:753`) which exactly **one** row accepts — the bucket `roles/storage.objectUser` under one repository's records condition (`:223`, `catalogRecordsGrant` at `:1982`), and nothing else. So an API key handed to the job would have meant a new row admitting `toCatalogCheck`: the account's reach would have grown from one repository's records to a secret the lookup reads. |

**Why the `serviceUsageConsumer` row above is the wrong fix even if the job ever did need it.** A
grant naming `toCatalogCheck` is not covered by init's one confirmation the way `toAccounts` rows are:
the kind exists so that a grant to this account is accepted only where a row names that exact
(type, role, condition) on purpose (`internal/infra/tf/cover.go:106-117`), and only the bucket
`roles/storage.objectUser` row does — under one repository's records condition (`:223`,
`catalogRecordsGrant` at `:1982`) — so every **other** grant to it is **refused by the plan review and
asked of the owner** (`:1338` `checkGrant`, whose `toCatalogCheck` case is `:1381-1391`;
`TestCatalogCheckAccountIsItsOwnPrincipalKind`, `TestCatalogCheckRecordsGrant`).
It is the one row an upgrade cannot ship silently, so reaching for it to buy a number used only to
reserve is the opposite of the smallest change.

And the header is not new to this account: `internal/cli/image_check_catalog.go:540` already builds the
job's build-submit client with `gcp.Options{GCPProject: e.gcpProject}`, which is
`option.WithQuotaProject(<installation project>)` (`internal/backend/gcp/gcp.go:78-82`) — sent from the
catalog check service account. `serviceUsageConsumer` is granted in exactly two places in
`deploy/terraform/gcp`, and neither is that account (`firebase/iam.tf:109-115` for people and admins,
`:117-121` for the history account). So one of two things is true, and T8's live run is what tells them
apart: either a header naming the caller's **own** project costs no permission — in which case the
lookup could pass one too and the split in §1.2 is simply the tidier rule — or the catalog check job's
build-submit path has needed that permission since it shipped, in which case the lookup must not add a
second caller with the same need. The split rule costs nothing under either reading, which is why §1.2
recommends it rather than betting.

So T8's `deploy/terraform/gcp/modules/installation/iam.tf` edit — *"only if the spike finds a grant
is needed"* (implementation plan, T8) — **is not needed**, and tension T5's *"a new grant in a
security-sensitive module"* **does not arise**.

One more cover fact the owner should have beside this, because it is the hostile-config surface
(§5): the plan review validates the *build service account* and the *base images* of a check spec
(`internal/infra/tf/cover.go:2483` `checkCatalogSpec`, `:2523` `buildAccount`) and **not the machine
type**; and the local config's own validation of it is a charset rule, not an allowlist
(`internal/localcfg/localcfg.go:1338-1339` `machineTypeRE = ^[A-Za-z0-9_-]{1,40}$`, checked at `:1829`).
A hostile local config can already choose the machine type. §5 says why that is survivable, and what
the estimator must do so it stays survivable.

## 4. Failure modes

| Cause | Signal | Behaviour | Precedent in the repo |
| --- | --- | --- | --- |
| API disabled on the project | 403 with `google.rpc.ErrorInfo`, `reason: SERVICE_DISABLED`, `metadata.service = cloudbilling.googleapis.com` | **Refuse**, and name the API and the project with the `gcloud services enable … --project <p>` line | `internal/infra/apis.go:82-99` `serviceDisabled`; `internal/infra/firebase.go:517-536` `disabledConsumer` (already used for this very API) |
| No quota project on the ADC | 403 naming a Google-owned quota project, `reason: SERVICE_DISABLED` | **Refuse.** Reachable on the **user** path only, and only if the client did not name one: the person's ADC carries no quota project, Google bills the call to a project of its own, and the error therefore names that project rather than the installation's. This is exactly the failure §1.2's option exists to prevent, and the shape `ErrNoQuotaProject` reports. With service-account credentials no header is sent at all, so the mode cannot arise | `internal/billing/accounts.go:168-174` `ErrNoQuotaProject`; `internal/infra/firebase.go:499-503` |
| Permission denied | 403 without `SERVICE_DISABLED` | **Refuse** | `internal/billing/accounts.go:150-159` `ErrNoPermission` |
| The named quota project is one the caller may not use | 403 `PERMISSION_DENIED` naming the project and `serviceusage.services.use` (Google's system-parameters check; not `SERVICE_DISABLED`) | **Refuse**, and say which project was named. On the user path this is the backend-less shape §1.2 describes: the lookup does not run there, so it is a bug in the gate, not a condition to repair with a grant | the same `ErrNoPermission` shape; §3's alternative row is the only way to make it a real state |
| Quota / rate limit | 429 `RESOURCE_EXHAUSTED` | Retry with a small bound, then **refuse**. A build is one lookup per command (§6), so this should be rare | the same shape `internal/billing` uses for its paged read |
| Offline, DNS, TLS | transport error | **Refuse.** Note it is *not* a new offline scenario: submitting the build itself needs the network. What is new is that a transient failure of a **second** API now blocks a paid action that would otherwise have proceeded — which is the argument for the cache in §6 | — |
| Unknown SKU, unmapped machine type, unexpected `usageUnit` | the catalog answers, but nothing matches | **Refuse** (the owner's rule: *"a missing or unknown price refuses, never defaults"*). This is also the hostile-config defence | — |
| Price changes between the lookup and the build | no signal | Accept. The looked-up price is the **reservation**; the settle uses actual spend (`internal/budget/lease.go`). Record the SKU id, the price and the read time *in the lease node* so a later price change cannot rewrite what was reserved | `internal/budget/model.go:30-72` names the node paths |

**A cached price, with an age limit.** Two forms, and they are not equivalent:

1. **Per-process (recommended).** One lookup per command invocation, held in memory, reused for
   every build that invocation submits — a `fugaro image refresh` builds several images, the catalog
   check job builds many. No persistence, so no age limit is needed: the value cannot outlive the
   command that read it. It costs nothing, adds no node, no writer and no rules change.
2. **Persisted, with an age limit (not recommended as part of the smallest design).** A small node
   (the lease's own key space, `buildleases/<slug>/<id>` — T8's, never `runs/`) keyed by SKU id,
   holding `{micros, currency, priceReadAt, source}`; served while `now - priceReadAt ≤ 7d`; on
   expiry the lookup must succeed or the build refuses. It is **not** a default — it is a recorded
   observation with an expiry, which is what makes it compatible with *"a missing or unknown price
   refuses"*. It buys availability when the Billing API is briefly unreachable, and it costs a
   writer on a new rules path (a generation bump and an `update` migration, HARD RULE) — a poor
   trade for a number that is only ever used to reserve. If the owner wants it anyway, the age limit
   belongs next to the lease's other constants, and `doctor` should report the cached price's age
   (not its absence).

## 5. The SKU-to-machine-type mapping, and what a hostile local config changes

**The chain, as the code has it today:**

```
local config: build.machine_type   (default E2_HIGHCPU_8; internal/localcfg/localcfg.go:1598)
              build.region, else region                     (:1148-1150, :2361-2365 BuildRegion)
        │
        ├─ internal/cli/image.go:310 → cloudBuildSpec(…, lc.Build.MachineType, …) → :456, the spec at :460
        ├─ internal/cli/image_build_catalog.go:361
        ├─ internal/cli/init.go:4709 (converge), image_refresh_steps.go:416 (refresh)
        ├─ internal/infra/spec.go:1729   (FUGARO_CHECK_SPEC, the repo daily check job)
        └─ internal/infra/catalogcheck.go:218 (FUGARO_CATALOG_CHECK_SPEC)
        │
        ▼
internal/backend/gcp/build.go:231  b.Options.MachineType = s.MachineType
internal/backend/gcp/build.go:239  b.Timeout = "3600s"        ← fixed, not configurable
internal/backend/gcp/build.go:240  b.QueueTtl = "3600s"       ← how long it may wait for a worker
                                                              before that timeout starts; billed for
                                                              neither, and the lease's deadline adds it
        │
        ▼
Cloud Build machine  ──── the rate ────▶  a Cloud Billing SKU
```

The last arrow is the one the owner's probe read (§1.5, 2026-11-08), and it lands in the SKU's
`description` prose rather than on an id: the family, the resource, the unit and the region are all in
that sentence, and the id beside it is the row T8's table pins. Three things make the hop harder than
it looks:

- **Two spellings.** Cloud Build's enum is `E2_HIGHCPU_8` (`.../build/docs/build-config-file-schema`:
  `"machineType": "E2_HIGHCPU_8"`, and *"The default machine type is e2-standard-2"*), while the
  pricing page's table is `e2-highcpu-8`. The mapping between them is by hand.
- **Aliases.** *"Cloud Build continues to offer n1-highcpu-8 and n1-highcpu-32 machine types. They
  are offered at the same price as e2-highcpu-8 and e2-highcpu-32."* Two names, one price — which in
  §1.5's description-keyed table means one row serving both enums, frozen by the test that pins the
  table.
- **Region.** The SKU is regional (`Sku.serviceRegions` in `v1`; `geoTaxonomy.type = REGIONAL` in the
  Preview API), and the region comes from `build.region`/`region` in the local config.

*Indicative numbers, for the shape of the estimate* (`https://cloud.google.com/build/pricing`,
default pool, read 2026-11-07): `e2-highcpu-8` **$0.0156/min**; with the fixed 3600 s timeout the
upper bound is **$0.936** per build. `e2-standard-2` is $0.006/min → $0.36. The free tier
(2,500 min/account/month) is deliberately **not** subtracted (§1.5).

**What a hostile local config can change, and what it cannot:**

- It **can** set `build.machine_type` (any 1–40 char token: `^[A-Za-z0-9_-]{1,40}$`) and
  `build.region`, and those reach `b.Options.MachineType` and the build location on every path above.
  It can also reach the two check jobs' specs when it is the machine that runs `fugaro update`.
- It **cannot** make the estimate disagree with the build, provided one rule holds: **the rate is
  looked up for the same machine type and region that go into the request**, and an unknown
  type/region **refuses**. Then the worst a hostile config does is buy a more expensive machine
  *and reserve the matching, larger amount* — which is what a cap is for.
- It **must never** be able to supply the rate. No config key, local or published, may carry a price
  (implementation plan T8: *"a machine type absent from the build config [refuses]; local config can
  supply neither"*). A rate read from config is a
  rate an agent sets, and the cap stops meaning anything.
- An unknown machine type at submit time is rejected by Cloud Build itself — the blast radius is a
  failed build, not a wrong estimate *provided the estimator refuses first*. (That Cloud Build
  rejects it is **UNVERIFIED** here; it is the safe assumption.)
- If a hostile config picks a region where the requested type is unavailable and Cloud Build falls
  back to `e2-standard-2`, the actual cost is **lower** than the estimate. The reservation stays an
  upper bound. Worth a line in the test.

## 6. Recommendation

**Take D1550 as written — the price is read from Cloud Billing at run time, and a missing or unknown
price refuses — with three amendments.**

1. **Read the GA `v1` Cloud Billing Catalog API**, not the Preview pricing endpoints: `services.list`
   once (pin the Cloud Build `serviceId`, `8B5D-EF7D-EB12`, read 2026-11-08 in §1.5, or cache it from
   one listing) and `services.skus.list` filtered to it, taking `Sku.pricingInfo` and
   `Sku.serviceRegions`. A Preview (Pre-GA) API on the launch path of every paid build is a business
   risk the GA catalog does not carry.
2. **Pin the mapping, not the price.** A small reviewed table in `internal/budget/buildcost.go` maps
   Cloud Build's machine-type enum (`E2_HIGHCPU_8`, `e2-medium`, the `n1-highcpu-*` aliases) to the
   **SKU id and its `description` predicate** per region family — the prose shape §1.5 verified, so a row
   carries both the id it resolves to and what the description must say, and the test can freeze what the
   runtime predicate matches; the lookup supplies only the *number*.
   `TestBuildCostIsAnUpperBound` freezes the SKU ids and the arithmetic. This is what turns
   a silent mapping bug into a failing test — and a silent mapping bug is the one way this design
   under-estimates.
3. **No grant, and no key.** The installation change is `cloudbilling.googleapis.com` in `apis.tf`, and
   the client names a quota project **only for user credentials**: `option.WithQuotaProject(<installation
   project>)` when the ADC is a person's, and none at all when it is a service account's (`§1.2`) — the
   job's credential then sends no `X-Goog-User-Project` and needs no `serviceusage.services.use`. The
   client is **not** `internal/billing`'s, which leaves the quota project to the credentials (§1.2), and
   the lazy fallback is **not** allowed either: `GOOGLE_CLOUD_QUOTA_PROJECT` in the environment can
   supply one for a service account, so the lookup must not let it (`internal/creds.go:285-303`), and
   the catalog check job's environment must not carry the variable (`catalog_check.env` is written by
   `internal/infra`). **And the lookup runs only where the budget backend is on** (§1.2): its user-path
   grant is the Firebase module's (`firebase/iam.tf:109-115`), which only `--firebase-project` applies,
   while the reservation it feeds is that backend's ledger — so a backend-less installation gets no
   lookup rather than a grant. T8 pins all four with tests beside the lookup: the option slice carries
   `option.WithQuotaProject(<installation project>)` for user ADC and **not** for service-account
   credentials; the env var changes neither answer; and the lookup is never constructed without a
   backend (the repo's own early return, `internal/cli/accounts.go:262-264`). No `iam.tf` edit, no
   `grantRules` row, no secret, no API key — on the **user** path, which the 2026-11-08 probe (§1.4) read
   green, so there this is the design as built rather than the design if the route works. The job path's
   half is T8's live run (§9 item 1), and it is what decides the `iam.tf` entry T8 keeps on its list.

**The exact grant, stated once:** *none on the user path*. Federated out: nothing in
`installation/iam.tf`, nothing in `cover.go`'s `grantRules`, one line in `apis.tf` — plus, on the user
path, a quota project the installation already lets that person use (`§1.2`), which is why the lookup
may not run where that grant is absent (a backend-less installation). That pair, and nothing else, is
what keeps the enablement inside `fugaro update`. **The job path stays open**: the probes sent a *user*
token (§9 item 1), so whether the catalog check job's service-account request — which sends no
`X-Goog-User-Project` at all — needs `serviceusage.services.use` on its own project is T8's live run to
settle, and until it does `installation/iam.tf` stays on T8's file list, with the level-3 review of any
entry it needs.

**The cache rule, stated once:** one lookup per command, held in memory, reused for every build that
command submits; never persisted, so it never ages and needs no limit. If the owner later wants
availability across commands, the persisted form — the second one in §4 — is the one to build, with a 7-day limit
and a `doctor` line for its age.

**The one gate before T8 starts — run and passed 2026-11-08, on the user path.** The §1.4 probe and the
§1.5 probe ran on the owner's own OAuth login, with no billing role and no API key, read-only, and both
came back 200: the public service list, and Cloud Build's SKUs. So the design above stands as the
smallest one that meets the requirement **on the path those probes read**, and the `apis.tf` line is
that path's whole installation change, with no `grantRules` row behind it: a public list price needs no
IAM grant, the one requirement being the Cloud Billing API enabled on the installation project, which is
the project the budget backend lives on and the quota project the user path names (§1.2). **The job
path is not closed by those probes** (§9 item 1): its client sends no quota project at all, so T8's live
run of the catalog check job's own request is what decides whether `installation/iam.tf` needs an entry
for it — **T8 keeps that file on its list, and any level-3 review of an entry, until that run says
otherwise**. Nor is a billing-account role any
help: contract prices are out of a project module's reach (§1.3), which is why the estimator reads the
list price. **The other branch did not happen** — had the token route returned 403,
the lookup would have needed an API key, and the smallest safe design would then be *no longer the
lookup*: a key is a bearer secret that would have to reach the catalog check job through a new
`grantRules` row (§3), which is a wider change than the whole rest of T8, in exchange for a number used
only to reserve.

**The fallback, had either probe failed — and the smallest alternative that would still have met the
requirement:** a **pinned, dated price table in the repository**, keyed by Cloud Build machine type,
holding the *list* price per minute, with the date it was read — the pattern this repo already uses
twice (`internal/backend/gcp/prices.go:9-27`, Cloud Run jobs, *"checked 2026-09-27"*;
`internal/backend/batch/prices.go:326-329` `priceReadAt = "2026-10-15"`, whose own comment says
**UNVERIFIED**). It is kept on the record as the alternative the probes ruled out, not as a plan. The
rules would have been:

- the table is the **allowlist**: a machine type absent from it **refuses** a build (that is T8's
  *"a missing machine type refuses"*, unchanged);
- the estimate is `3600 s × the table's rate`, an upper bound, and the table holds **list** prices,
  so a contract discount only makes it more conservative;
- the local config still supplies only the machine type, never the rate;
- **`doctor` warns when the table's read date is older than 180 days** — a warning, not a refusal: an
  installation that cannot reach the network (or has an old binary) must still build, and the HARD
  RULE forbids making the owner run anything but `fugaro update`. `fugaro update` refreshing the
  table is what makes the warning disappear.
- what it costs: `fugaro update` must carry a re-read price whenever a price moves (the release
  checklist gains one line), and the table can silently under-reserve if a price were to *rise*
  between releases.

**And one thing T8 must not do either way:** the estimate is client-side only. A direct
`gcloud builds submit` bypasses it (F19, T2) — the lookup does not change that, and no grant here
should be read as making it true.

## 7. Sources

Google's documentation (fetched 2026-11-07; "Last updated" as Google prints it):

| URL | Used for | Last updated |
| --- | --- | --- |
| `https://cloud.google.com/billing/docs/how-to/get-pricing-information-api` | The whole of §1: what the Pricing API is, public vs contract data, "you must create an API key", "You don't need any specific IAM permissions", the exact billing-account permissions, "Enable the Cloud Billing API" | 2026-11-04 |
| `https://cloud.google.com/billing/v1/getting-started` | The Catalog API's scope: *"Programmatic access to the entire public Google Cloud catalog … Billable SKUs, Public pricing, Relevant metadata"* | 2026-11-04 |
| `https://cloud.google.com/billing/docs/reference/rest` | The `v1` resource list (`services`, `services.skus`) and "Lists all publicly available SKUs for a given cloud service" | 2025-09-04 |
| `https://cloud.google.com/billing/docs/reference/rest/v1/services.skus/list` | `Sku`, `PricingInfo`, `PricingExpression`, `TierRate`, `serviceRegions`, and the OAuth scopes (`cloud-platform`, `cloud-billing`, `cloud-billing.readonly`) | — |
| `https://cloud.google.com/billing/docs/how-to/billing-access` | Role scopes: *"Lowest-level resources where you can grant this role: Billing Account"* for Billing Account Viewer | 2026-11-04 |
| `https://cloud.google.com/iam/docs/roles-permissions/billing` | The permission list: the `billingAccount*` permissions exist, no public-catalog permission does | 2026-11-04 |
| `https://cloud.google.com/build/pricing` | Build-minutes, the free tier, per-machine-type per-minute prices, the `n1-highcpu-*` aliases, private-pool pricing | — |
| `https://cloud.google.com/build/docs/build-config-file-schema` | *"The default machine type is e2-standard-2"*, `machineType: 'E2_HIGHCPU_8'` | — |
| `https://cloud.google.com/build/docs/iam-roles-permissions` | `cloudbuild.builds.create` is what submits a build; the caution about running as the Cloud Build legacy service account | 2026-10-26 |
| `https://cloud.google.com/build/docs/cloud-build-service-account` | The default *build* identity is a service account — which is why `build.go:227` always names one | — |

The repository, by path and symbol:

| Path | Used for |
| --- | --- |
| `docs/no-prompt-policy/reconciliation.md` §3 D1543/D1548/D1550, §5 T5, F5, F18, F19, F20, §6 | The decisions and the UNVERIFIED this spike answers |
| `docs/no-prompt-policy/implementation-plan.md` T8S, T8 | The task that commissioned this spike, the T8 file list, and the condition T8S answers: *"the grant in `iam.tf` is UNVERIFIED until then"* — answered **no grant on the user path** on 2026-11-08 (§8), the job path being §9 item 1 and T8's live run |
| `internal/backend/gcp/build.go:227,231,239,240,370,405-417,490,503` | The build request: service account, machine type, the fixed 3600 s timeout, the queue TTL the lease's deadline adds, the submit path |
| `internal/backend/gcp/gcp.go:67-82` | ADC as the identity, and the quota-project pattern for the **user** path: `option.WithQuotaProject(o.GCPProject)` when a project is known — that backend's own builder, not a shared one, and the option it sets unconditionally (§1.2 splits it by credential) |
| `google.golang.org/api@v0.299.0` `transport/http/dial.go:189,240-242`; `internal/creds.go:29,285-303` | The header rule the split rests on: `X-Goog-User-Project` is set whenever a quota project resolved non-empty, whatever the credential kind; with no client option the only fallbacks are `GOOGLE_CLOUD_QUOTA_PROJECT` and a `quota_project_id` in the credentials file |
| `internal/cli/image_check_catalog.go:50,540` | The catalog check job's client, built with `gcp.Options{GCPProject: …}` — the same account, already sending the same header |
| `deploy/terraform/gcp/modules/firebase/iam.tf:102-121` | `usage_consumer` (people and admins) and `history_usage` (the history account): who holds `serviceUsageConsumer`, and why the Firestore client needs it — an account naming a project that is **not** its own |
| `internal/cli/init.go:422`; `internal/cli/init_stages.go:947-951`; `internal/cli/accounts.go:262-264`; `internal/cli/api.go:159` | `--firebase-project` is what turns the backend on (*"a run without it builds none"*) and its stage skips without it; and the repo's own shape for a ledger feature on a project with no backend — return before the call, with the `fugaro update --firebase-project` remedy |
| `docs/git-providers.md:82-85`; `docs/gcp-live-checklist.md:7415` | The repo's rule for a bearer token: `curl --config` from a process substitution, because `ps` shows argv to every local user for the process's lifetime |
| `internal/cli/init_project.go:437,490` | `adcInfo`/`credentialNote`: `authorized_user` is the person |
| `internal/billing/accounts.go:41-68,150-174,249-271` | The repo's existing Cloud Billing client: ADC only, `billingAccounts.list`, the permission/quota-project/API-disabled errors — and `newService`, which names **no** quota project, so the lookup must not copy it (§1.2) |
| `internal/infra/apis.go:20-31,82-99`; `internal/infra/firebase.go:498-536` | Disabled-API detection, and that `cloudbilling.googleapis.com` is not enabled by the installation |
| `deploy/terraform/gcp/modules/installation/apis.tf:4-25` | The enabled-API list |
| `deploy/terraform/gcp/modules/installation/iam.tf:35-42,70-83` | `fugaroBuildSubmitter`, and the operator grant |
| `deploy/terraform/gcp/modules/installation/catalogcheck.tf:24-46,99,125` | The catalog check job and its account's grants |
| `deploy/terraform/gcp/modules/repo/*.tf`, `modules/repo/catalogcheck.tf:24` | The repository build account and the per-repo actAs grant |
| `internal/infra/tf/cover.go:22,127,143,189,223,290,753,1982,2483,2523` | `allowedTypes`; the principal kinds (`toCatalogCheck` at `:127`, the comment that makes it one at `:106-117`, `isCatalogCheckMember` at `:753`); `grantRules` (`:143`) with the one row that accepts that kind — `roles/storage.objectUser` at `:223`, checked by `catalogRecordsGrant` at `:1982` — and the `serviceUsageConsumer` row at `:189`; `allowedRoles` (`:290`); `checkCatalogSpec` (`:2483`) and `buildAccount` (`:2523`) |
| `internal/localcfg/localcfg.go:1148-1150,1337-1339,1598,1629-1630,1826,1829,2361-2365` | `build.machine_type`/`build.region`, the charset rules, the default, the region fallback |
| `internal/backend/gcp/prices.go:9-27`; `internal/backend/batch/prices.go:47-79,326-329` | The repo's pinned-price-table precedent, dates included |
| `docs/plans/2026-09-27-m4-gcp-backend.md:3838` | The repo's own earlier recipe for a catalog read with a bearer token |
| `docs/gcp-live-checklist.md:1339,2668` | The operator's today's-by-hand `cloudbilling.googleapis.com` enablement on the ADC quota project |

Not a source but the evidence the two verified sections rest on: **the owner's probe of 2026-11-08**
(§8) — the API read directly, with the owner's own credential, and its answers recorded there. It read
the **user** path only; §9 item 1 is the job path, which T8's live run closes.

## 8. The owner's probe, 2026-11-08

The credential this spike lacked, used once, read-only: the owner ran §1.4's recipe and §1.5's two
commands at their own terminal on their own OAuth login — `gcloud auth print-access-token`,
`X-Goog-User-Project` naming their installation project (`fugaro-dev`), **no billing role and no API
key**. Nothing was written: no IAM binding changed, no key created, no price recorded outside this
document. What it read, and what each reading settles:

| Question (§) | Read | Answer |
| --- | --- | --- |
| Does an OAuth caller with no billing role read the public catalog? (§1.4) | `GET /v1/services` | **200** with the public service list, at `pageSize=5`, `1000` and `5000` alike. **Verified.** |
| Is Cloud Build in it, and are its SKUs readable? (§1.5) | `GET /v1/services`, then `GET /v1/services/8B5D-EF7D-EB12/skus?pageSize=5000` | **200**; `serviceId` **`8B5D-EF7D-EB12`**, found by `displayName` `"Cloud Build"`; **370 SKUs in one page and no `nextPageToken`**. **Verified.** |
| What does a SKU carry, and in what unit? (§1.5) | the same 370 | `description`, `serviceRegions`, `pricingInfo[0].pricingExpression.usageUnit` — **`min`** for machine time, **`GiBy`** for network. **Verified**, and the shape T8's parser is written against. |
| Beyond enabling the API, is anything needed in `deploy/terraform`? (§3, §6) | — | **No grant on the user path**: no `iam.tf` entry, no `grantRules` row, no secret, no key. Every reading above carried a *user* token; the job path's no-quota-project request is the first bullet below, and T8's live run settles it. |

**What it does not settle, so that the table is not over-read:**

- The **service-account** half of §1.2's split rule: the probe sent a *user* token **with** the header.
  Whether a job request with **no** quota project is billed to the account's own project, and whether a
  service account naming its own project needs `serviceusage.services.use`, is what §9's first item
  still asks and what T8's live run confirms.
- The live T8 path itself: the option slice, the catalog check job's environment, the lease node, the
  rules generation. The probe read a catalog; it exercised none of that code.

## 9. UNVERIFIED, collected

1. **That a service-account request with no quota project is billed to the account's own project, and
   that naming its own project needs no `serviceusage.services.use`** (§1.2). The split rule rests on
   the first half; the library source settles what header is sent, but not which project Google then
   treats as the consumer. The second half is the one §3's argument reaches for evidence on: the
   catalog check job's existing client already sends that header from that account, and no job account
   holds the permission — either that is fine or that path has always needed it, and the history
   account holding `serviceUsageConsumer` beside its Firestore client's header (`firebase/iam.tf:102-121`)
   points at the second. T8's live run should confirm both, since the split is what keeps the job's
   reach unchanged — and it is the run that decides whether `installation/iam.tf` needs an entry for the
   job at all: until it does, that file stays on T8's list and any entry still takes the level-3 review
   before it is written.
2. That Cloud Build rejects an unknown `machineType` at submit time rather than falling back to the
   default (§5). Assumed in the safe direction. **Not probed**: the probe read prices and launched
   nothing.
3. That a negotiated contract price is never **above** the public list price (§1.3) — assumed, and
   stated so a reviewer can attack it.
4. Whether `fugaro update`'s own apply would be refused by `cover.go` if a future task ever adds the
   `google_project_service` row for this API. Not checked: the type is allowed and the change is
   one entry, but the golden plans were not re-rendered in a docs-only change.
5. Whether the console or the mobile app read the marker or the budget record in a way the build
   lease's settled price would have to appear in. Out of this spike's scope; T8 owns it.
