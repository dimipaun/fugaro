# The api-key review path — reconciliation

**Status: reconciliation only; no code is implemented; no task approved.** This document reads every
clause of the owner's ask ([spec.md](spec.md), 2026-11-07) against the tree, classifies what it finds,
and turns the parts that need a decision into questions **D1410–D1429** (§7). Nothing here is
approved and nothing follows it: a recommendation is not a go, and an answer is not an approval.

**Checked against:** `origin/main` at `aa26f322f`, on **2026-11-07**. `graphify` has no graph for
this repository (there is no `graphify-out/`), so every claim was located with `git grep` and then
read in place; every path and Go symbol cited was re-checked to exist on `aa26f322f` before it was
written. Line numbers are never cited in a citation; a few appear inside a quoted value only where
the quote is the point.

[spec.md](spec.md) is the owner's ask, verbatim. **Where this document disagrees with
[spec.md](spec.md), this document wins** — and there is one place it does (**F20**).

## 0. What the ask is, and what it really is in this repository

The ask names **one** thing — "the api-key review path" — and the tree has **two** independent
reviews, each with its own machinery, its own credential and its own record:

| | The in-job review | The cloud review run |
| --- | --- | --- |
| What it is | the `review` / `adversarial` stages of the same Cloud Run job that coded the change | a separate job, `fugaro review --pr N` (or `fugaro run --pr N --recipe review-only`), launched for a repository the project admits |
| Where it is | `internal/runner` (`resolveMixedStages`, `stageAgentEnv`) | `internal/credsvc` (the review class) + `internal/runner/gateway.go` (`reviewClassRun`) |
| What credential it uses today | a **Claude OAuth account** scoped to the stage, held in the runner's own environment | the credentials service's **review class**, from the installation's Claude OAuth account, held by the service and never by the job |
| Where its dollars are | **notional** (`runstore.StageTiming.ModelNotionalUSD`, `Credential: "oauth"`), uncapped by the gateway | **real**, charged by the gateway from the service's answer |
| Whether it can be launched today | **yes** — a stage of a run | **no** — built and not admitted: `reviewJobAdmitted` is `false` (**F10**, **U6**) |
| What proves it | **Check 290**, written and **NOT RUN** (**U5**) | the class's own tests |

So the ask is one name for two paths. §1 and §2 read each of them; §4 states the reading this design
takes and the decision that settles it (**D1410**).

## 1. The in-job half

**F1 — Exists. An in-job review served by an Anthropic API key works today, and needs no new
machinery at all.** A repository whose `fugaro.yaml` writes `agent.auth: api-key`, whose coder is a
model a provider route claims and whose `models.reviewer` is a Claude model gets exactly the shape
the ask describes: `internal/runner/gateway.go`'s `gatewayUpstream` takes the `"api-key"` arm and
builds `gateway.Upstream{Kind: "anthropic", APIKey: <the job's ANTHROPIC_API_KEY>}`, so the review
stage's calls go through the gateway to **Anthropic's own API** on the run's own key, beside the
coder's calls going through the same gateway to the provider. This is
`docs/design/mixed-credentials.md` **§3**, headed "Path A: Claude through an Anthropic API key",
and §1's own words for the path are *"Works today with the code as it is"*. The configuration is
documented for users in `docs/multi-model.md` §4, whose example writes `agent.auth: api-key` with
`reviewer:` a Claude ID and an `agent.levels` block.

**F2 — Exists. The dollars are real, capped and priced, and the ask's clause 1 is satisfied by the
existing machinery.** The gateway's `StageReport.Used` is the real figure
(`internal/gateway/gateway.go`), the run records it as `runstore.Cost.ModelUSD` and per stage as
`runstore.StageTiming.ModelUSD`, and the cap is enforced by `internal/gateway/ledger.go`: the
`"run_cap"` halt is the one the reservation path builds, `haltedMessage` is the getter the request
path asks, and a halted run is answered from it rather than forwarded. Nothing about the review stage is
special: it is a stage, and a stage's calls are the gateway's.

**F3 — Exists, and it is not what the ask asks for. Attribution is by model and by route, not by
key.** `runstore.Cost` carries `ModelBy` and `RouteBy`, and the gateway's call log records
`serving_model` and `priced_as`; none of them names a **credential**. A run that holds one Anthropic
key and one provider key has no field anywhere that says which of the two paid a given stage, beyond
the model the stage ran. The ask's clause 1 says "attributed by the gateway like every other call",
which the tree already does — but a reader who takes "attributed" to mean *attributed to a key* will
not find it, and this design does not propose to add it (**D1415** is about the narrower question of
whether the record names the credential a stage the run's own key served, and the answer may well be
that it should not — see **F18**).

**F4 — Exists. Both upstreams in one run is already exercised.** `internal/runner/routing_e2e_test.go`'s
`TestRoutedSwitchKeepsTheFirstLineLoop` runs a provider-coder api-key run whose senior review is on a
Claude ID no route claims, and asserts both directions: `g.compat.Count() == 3` for the coder's
stages and `g.fake.Count() == 1`, whose failure message is `Anthropic saw %d calls, want the senior
review alone: an unrouted pin must keep its Claude ID`.
`internal/runner/layer_claim_test.go`'s `TestLayerClaimLeavesADirectCoderStageAlone` is the mirror
image (`fake == 1 && compat == 1`). So `docs/design/mixed-credentials.md` §3's "Missing: … a gateway
test with both upstreams in one run" is **stale** (**F9**).

**F5 — Gap. Nothing names the path, and nothing pins the review stage's accounting in it.** There is
no config key, no doc section and no test that calls an api-key run's review "the api-key review".
The behaviour of F1 is exercised only incidentally, inside fixtures built for other questions (the
routing switch's, the layer claim's), and **no test asserts that a review stage in an api-key run is
charged real dollars** — that its `ModelNotionalUSD` is zero, that its `StageTiming.Credential` is
empty, and that its charge counts against the cap. An owner reading `docs/multi-model.md` gets a
configuration example and no statement of which credential the review then runs on. This is the
first slice's whole content, and it is a test and a paragraph, not a mechanism.

**F6 — Gap. The run holds exactly one Anthropic key.** `config.AnthropicSecret` is one secret name
(`internal/config/config.go`), it is mounted as one environment variable
(`config.ReservedSecrets`), and `internal/runner/gateway.go`'s `gatewayUpstream` reads it into the
gateway's single `Upstream.APIKey` (`internal/gateway/gateway.go`'s `Options.Upstream`). A
`gateway.Route` carries a **provider's** key, not Anthropic's (`internal/gateway/route.go`'s
`Route.Credential`). So "the key can be a separate secret from the coder's provider key" (the ask's
clause 4) is already true — the provider's key is a different secret, mounted under a different
variable (`internal/config/providers.go`'s `ModelProvider.SecretEnv` method) — but a **second Anthropic
key**, one that pays for the review and nothing else, does not exist and cannot be selected per
stage.

**F7 — Rule. A published stage account takes the review off the gateway, and nothing prefers the
key.** `internal/runner/agentenv.go`'s `resolveMixedStages` runs only for an `api-key` run
(`r.cfg.Agent.Auth != "api-key"` returns nil), reads the project policy's `ClaudeAccountStages` for
`config.MixedAccountStages` (`internal/config/stage_accounts.go`: `review`, `adversarial`) and the
project's `DefaultClaudeAccount`, and puts the stage's account in `r.mixedStages`. From there
`stageDirect` is true, `stagePins` returns nil, `stageAgentEnv` swaps the environment through
`agent.OAuthStageEnv` and the stage goes **straight to Anthropic** — notional dollars, no gateway, no
cap. So an owner who publishes both an Anthropic key and a stage account for `review` gets the
**account**, and there is no spelling that says "use the key". `resolveMixedStages` will *refuse* a
stage account whose model a provider route claims (the "goes straight to Anthropic" refusal), but it
has nothing to say about a stage account that shadows a perfectly good key.

**F8 — Rule. Under `agent.auth: oauth` the gateway is refused, so an api-key review is
unreachable.** `internal/runner/routing.go`'s `GatewayOn` answers true for `"api-key"`, `"vertex"`
and `config.AuthCascade` and false for everything else, and `internal/agent/env.go`'s `BuildEnv`
refuses a gateway for an `oauth` run outright (an oauth run's own token never goes through it). A
run whose coder is on the subscription therefore cannot have a review on an API key: the gateway is
the only thing that holds a key and attaches it per call, and that run has no gateway. Whether that
stays is **D1414**.

**F9 — Wrong, and it matters for the plan's size. `docs/design/mixed-credentials.md` §3's "Missing"
list is partly stale.** It names four gaps: the user docs, the cost guidance, a gateway test with
both upstreams in one run, and one sandbox live check. The third is already there (**F4**); the
fourth is **Check 290**'s sibling and is this design's Check 330 (implementation-plan §5); the first
two are real, and this design's AK1–AK3 are what close them. A reader who takes §3's list at face
value will size the in-job half too large.

## 2. The cloud half

**F10 — Exists, merged, and it is the OAuth path — but it is built and not admitted, so no review
launch reaches it today.** `internal/cli/review.go`'s `reviewJobAdmitted` is a constant `false`, and
`checkReviewJobAdmitted` refuses every launch of `fugaro review --pr N` with "this installation's
credentials service does not admit the review job's own caller", naming the way out it can offer:
`fugaro run --pr N --recipe review-only`, the review in the run's own job. The refusal is not a
missing feature in the launcher — the credentials service refuses a project-scoped job's caller
whole (its Global Constraint 6), which is why `docs/cloud-senior-review`'s **CSR11** flips the
constant only after that is fixed — and `docs/releases/v0.20.2.md`'s known limits say the same
("the cloud review is built and not admitted", unchanged from `docs/releases/v0.20.0.md`). The one
door is `reviewSkipAdmissionCheckEnv`, the owner's own C3.9 live check's step 3, off by default and
refused in an agent's session. So **every statement below about the cloud path is about code that a
real installation cannot yet reach**, and the prerequisite for reaching it is `docs/cloud-senior-review`
§1's **E7** — the service enabled *and* the service admitting the review job's own account, which is
**C3.9**'s work, unscheduled and no `fugaro update` step — followed by the flip of the constant
(**CSR11**, after the owner's live checks). None of that is in this design's slices, and neither
**D1410** nor **D1422** is priced with it (**U6**).

With that said, the class itself is real and merged.
`internal/runner/gateway.go`'s `reviewClassRun` is the conjunction of the run's mode being `review`
(`r.runClass`) and the job it was admitted to being the review-only variant (`r.job.Class`); when it
is true, `internal/runner/gateway.go`'s `modelAuth` returns `config.AuthCascade` — so the class is
spelled as the cascade and every arm the path needs already exists — and `gatewayUpstream`'s cascade
arm builds the credentials-service upstream. `internal/credsvc/review.go` holds the class
(`ClassReview`), the variant's job and service-account names, and `ReviewJobAccount`; the pairing
resolves the run to one Claude account.

**F11 — Rule. The class refuses a repository whose own `agent.auth` is an API key, in a named
sentence.** `internal/runner/routing.go`'s `checkReviewClassModelPath` switches on
`r.cfg.Agent.Auth` and admits only `"oauth"` and `config.AuthCascade`; anything else — `api-key`,
`vertex` — is refused with a sentence that names the api-key review and the release it is in, and
`internal/runner/routing_internal_test.go`'s `TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn` holds
both halves. The refusal is fail-closed in both directions: the review variant deliberately mounts
no credential of the repository's (CSR7), and `internal/agent/env.go`'s cascade arm refuses a run
with no gateway rather than falling back to one.

**F12 — Exists, and this is the finding that halves the slice. The service already holds an
Anthropic API key and already knows how to send it to Anthropic.** `internal/credsvc/bundle.go`'s
`CredentialKey` maps `config.AnthropicSecret` to `(config.AnthropicSecret, KindModel, creds.TierB,
true)` — tier B — and `internal/credsvc/forward.go`'s `send` builds
`gateway.Upstream{Kind: upstreamKindAnthropic, APIKey: e.Value()}` and, for a non-tier-A entry,
attaches it with `gateway.UpstreamHeaders`. `upstreamKindAnthropic` is `"anthropic"` and its base URL
is `""` — Anthropic's real address. So for an **Anthropic** key there is **no base URL to carry, no
auth shape to model, and no upstream to build**: everything §6.7 lists as missing is missing for a
**provider** key, and only for one.

**F13 — Gap. The class's candidate list offers the pairing's tier-A account and nothing else.**
`internal/credsvc/cascade.go`'s `reviewCandidates` returns the entries whose tier is `creds.TierA`
**and** whose name equals the pairing's account, and reports every other credential as not admitted.
`candidates` calls it for `ClassReview` before anything else, so a review caller is never offered the
tier-B key that F12 says the service holds and can send.

**F14 — Gap. No pairing can name the project's key.** `ReviewJobAccount.Account` is documented in
`internal/credsvc/review.go` as "the Claude account's name as the policy's accounts block spells it",
and the axis it is derived from is `internal/config/claude_accounts.go`'s, whose names are the
`claude-oauth-token` family (`config.ClaudeAccountSecret`). A review job whose account were the
project's Anthropic key would need a second axis value that is not a Claude account at all, and
`config.ProjectJobAccounts` — the one function the deployer, the launcher and the runner share — is
where that value would have to come from.

**F15 — Gap, and it is the OpenRouter half. There is no provider key in the service's document and
no upstream that would carry one.** `CredentialKey`'s closed set has no entry for a provider key;
`internal/creds/select.go` defines `TierC` as "reserved and never selected" — the third-party route
this release does not build; a provider key lives in the local configuration's `providers` block
(`internal/config/providers.go`'s `ModelProvider`: a base URL, an auth shape of `bearer` or
`x-api-key`, a logical secret name and a model list) and is mounted into a **run's** job. So §6.7's
cost sentence is exactly right for a provider key: nowhere to hold it, no pairing to name it, no
candidate list that may offer it, no upstream to send it to.

**F16 — Exists. `TierC` is already the reserved third-party tier.** `internal/creds/select.go`:
"TierC is reserved and never selected. It is the third-party route (an OpenRouter key) this release
does not build". `internal/credsvc/cascade.go`'s `wireTier` is what spells it on the wire, and the
**refusal** is the selector's: `internal/creds/select.go`'s tier switch admits `TierA` and `TierB`
and returns an error for `TierC`, so a caller offering one is refused rather than quietly served by
something else. The OpenRouter half therefore has a tier constant to land on and no other part of
the apparatus.

**F17 — Gap, and it is narrower than it first looks: the job variant exists, the credential does
not.** The review variant is one job per `(profile, provider, account)` of the published policy
(`internal/credsvc/review.go`'s `ReviewJobName` and `ReviewJobAccountsFor`, over
`config.ProjectJobAccounts`), and **the account axis always carries the no-account entry**:
`config.ProjectJobAccounts` starts from `[]string{""}` and `config.ReviewVariantAccount` spells that
entry as `config.DefaultClaudeAccount` (`"default"`), so a profile that publishes **no** Claude
account still yields exactly one account on the axis. The deployer builds its review twin for it
(`internal/infra/projectjobs.go`'s `ProjectReviewJobs`, one `reviewVariant` per coding job) and the
service admits it (`internal/credsvc/review.go`'s `ReviewJobAccountsFor` maps `""` to `"default"`,
and `ReviewVariantAccount` is the same function the deployer's mapping is). So the variant is
deployed and admitted; what an installation with an Anthropic key and no subscription has is **no
credential the variant's model path can be served by** — the class's candidate list is tier-A Claude
accounts only (**F13**) and the pairing has no value that names a key (**F14**), and the launcher's
own account chain (`internal/cli/run.go`'s `claudeAccountFor`, `mixedRunAccount`) resolves to the
default account, which holds nothing. The ask's clause 2 ("a project with only an Anthropic API key …
still gets an Opus-class independent review") is therefore a **credential-shape** gap and not a
deployment-shape one. **U3** records that no live installation was inspected, so the deployed shape
is read from the code and not measured.

**F18 — Gap, and it is narrower than it looks. The record cannot name a credential the run's own
key served — and the field cannot be widened to let it.** `internal/runstore/runstore.go`'s
`StageTiming.Credential` is documented as *the credential that served this stage **when it was not
the run's own***, `CredentialOAuth` (`"oauth"`) is its only non-empty constant, and
`schemas/result.schema.json` fixes the enum at `["oauth"]` with the same sentence. An api-key review
stage — in either half — records `""`, the same value every ordinary stage records.

So recording `"api-key"` there is **not additive**: it puts a value in a field whose stated meaning
excludes exactly that case, which is the thing CLAUDE.md forbids — *a field whose meaning must change
gets a new name*. Two honest shapes exist (**D1415**): a **new field** on `StageTiming`, which is
additive and keeps `credential` at one meaning; or **nothing at all**, which is defensible because
the record already answers the question this design exists to ask. For the **review** stage the two
configurations are already distinguishable without any new field — the account path records
`credential: "oauth"` with `model_notional_usd == model_usd`, and the key path records neither — and
for any stage the model and the route (`route_by`, the gateway's `serving_model` and `priced_as`)
name what served it. What a new field would buy is the join, in one place, rather than a reader
combining three.

## 3. The ask's own clauses, one by one

**F19 — Rule. Clause 1 ("the review's dollars are real … not notional") is true exactly when no
stage account is published.** For the in-job half it is **already true** (**F1**, **F2**) and it
becomes **false** the moment a project publishes a stage account for `review` (**F7**) — which is
the configuration Check 290 is written for — and, since Check 290 is **not run** (**U5**), a
configuration nothing has yet measured on a real installation. So the clause is not a request to build
something; it is a request to *stop* the account path from being the only one, or to make the key
path selectable. For the cloud half the dollars are already real (**F10**) — when it runs, which
today it cannot: the class's calls are the service's, carried by the gateway, and the launch in front
of them is refused (**U6**).

**F20 — Wrong, and it is the one place this document corrects the ask. Clause 3's citation of D894
is not the subscription-terms question.** D894 is `docs/cloud-senior-review`'s, and it is the
**reviewer's Claude-session configuration posture** — answered by the owner on 2026-11-01 with option
(a): *the reviewer's session loads no configuration from the head it reviews, and is given the base
branch's `CLAUDE.md` and `.claude/rules` in its place*. It has nothing to do with subscription terms.
The subscription-accounts-terms question the clause means is `docs/accounts-add/spec.md` §11.4 (the
owner confirming that one project may spread across several subscription accounts, D875) and the
Terms work in `docs/subscriptions/`. The clause's substance stands — an API key raises no
subscription-terms question, and D1412/D1414 are where that is decided — but a reader who follows the
citation to D894 will find a different subject.

**F21 — Exists. Clause 4 ("the key can be a separate secret") is already true, and it is a
property, not a task.** The Anthropic key is `config.AnthropicSecret` under its own environment
variable; the coder's provider key is the route's own secret (the `ModelProvider.SecretEnv`
method), mounted separately and read by `internal/runner`'s provider-credential path.
`internal/infra/spec.go`'s `SecretMounts` mounts them independently, and only the Anthropic key may
be absent from an installation (`internal/infra/readiness.go`'s `OptionalSecret`, the #371 rule).
The one thing the clause could mean beyond this is a
**second Anthropic key for the review alone** (**F6**), which is **D1412**.

**F22 — Rule. Clause 2's "only OpenRouter" is not automatic.** A project with no Anthropic key and
only an OpenRouter route can get an Opus-class review only if a route entry **claims the Claude ID**,
and a route may claim a Claude model only in one shape: one explicit `anthropic/<id>`
(`internal/config/providers.go`'s `CheckClaudeClaim`, over `ClaudePatterns = ["claude-*",
"anthropic/*"]` and `CheckModelPattern`). A pattern, a `:variant`, a `[1m]` spelling or a bare
`claude-*` name is refused at the route. So "only OpenRouter" is a route the owner writes, not a
default.

**F23 — Rule. A review key pointed at Anthropic cannot be spelled as a route.** For the same reason
as **F22**, read the other way: `CheckClaudeClaim` refuses a route claiming a bare `claude-*` ID, so
an owner who wanted the review's own key expressed as a `gateway.Route` aimed at
`https://api.anthropic.com` cannot spell it that way for a canonical Claude ID. The
`anthropic/<id>` claim shape is available, and it routes the call **and prices it from the route's
row** — which is a different thing from the run's own Anthropic key, and not what clause 4 asks for.

**F24 — Gap. The credentials service's document has no room for a second key of any kind.**
`CredentialKey`'s closed set carries `config.AnthropicSecret` at tier B and the `claude-oauth-token`
family at tier A, and nothing else model-shaped. So a **review-scoped** Anthropic key — the shape
D1412 would ask for if the owner wants the review's dollars on a secret of their own — is a new
spelling in a closed set, a new bundle entry, a new mount and a new pairing axis, whether the key is
held by the service (the cloud half) or by the job (the in-job half, where it would be a second
`SecretMounts` entry).

## 4. The two readings of "the api-key review"

The ask can be read two ways, and they are not the same design.

**Reading 1 — the in-job stage.** The review the owner wants is the mixed run's stage-scoped
credential, with an API key in the account's place: an `api-key` run whose coder is on a provider
route and whose `review`/`adversarial` stages run on an Anthropic API key **through the gateway**,
so their dollars are real, capped and in `ModelUSD` exactly as the coder's are. Under this reading
the path **already exists** (**F1**, **F2**) and the design's content is: name it, pin it with tests,
give the owner a way to choose the key over a published account (**F7**), and decide whether the
review gets a secret of its own (**F6**).

**Reading 2 — the cloud review run.** The review the owner wants is the **independent** review —
`fugaro review --pr N`, the job that reads the change and signs a verdict — served by an API key
instead of the installation's Claude OAuth account. Under this reading the path does **not** exist:
the class refuses it (**F11**), the candidate list offers nothing else (**F13**), no pairing can name
a key (**F14**) and, for OpenRouter, the service has nothing to hold or send (**F15**). This is the
slice §6.7 cut from 0.20.0.

**This design does not pick one silently.** It reads the ask's own clauses as pointing at **both**:
clause 2's "a project with only an Anthropic API key" and clause 3's terms question are the **cloud**
reading (an installation with no subscription at all — **F17**), while clause 1's "not 'notional'"
and the citation of the mixed run's Check 290 are the **in-job** reading (**F19**). The plan is
therefore written in three slices that can be taken independently, and **D1410 is the gate on all of
them**: the in-job slice (AK1–AK3) needs no service change and can ship alone; the cloud
Anthropic-key slice (AK4–AK5) is the cheap half of §6.7; the OpenRouter slice (AK6–AK7) is §6.7 as
written.

## 5. The security analysis as an attacker would see it

**What changes about the credential.** Nothing about *what is sent*: a review reads a diff, and the
reviewer's posture — the head is diff content and never configuration (**D894(a)**), the repository
admission and the allow-list the service holds every call to — is unchanged by which key pays. What
changes is **whose key it is** and **where it lives**.

**The in-job half adds no residual that an api-key run does not already have.** The key is already in
the runner's own environment for any `api-key` run (`config.ReservedSecrets`), already registered
with the redactor, and already kept out of the agent's environment: `internal/agent/env.go`'s
`BuildEnv` gives the agent the gateway's address and its per-run token and registers the real key for
redaction only, and `gatewayUpstream` holds it in the runner process. A review stage on the same key
is a stage on the same key — the `/proc` residual `SECURITY.md` records for an `api-key` run is
neither widened nor narrowed by this design. The **stage-account** path is the one that adds a
residual (the account's OAuth token in the runner's environment for the whole run), and this design
does not touch it.

**The cloud half's threat is the one §6.7 already names: a review job that holds a key is a job that
can be turned into something else.** The class's own door is what stops that — `reviewClassRun`'s
conjunction of the run's mode and the job it was admitted to (`F10`), which is why the
`FUGARO_REVIEW_JOB` marker is a permission the caller states and never a credential — and this design
must not weaken it. The tier-B key is the **project's own paid key**, and a review call served by it
is a call the project already pays for, so no new party gains access to it. The one genuinely new
question is **scope**: today the tier-B key is offered to no review caller at all, and offering it
means a review of repository A spends the project's key — which is exactly what a cascade coding run
already does with the same key, so the change is a *widening of who may spend it*, not of *what it
reaches*.

**The OpenRouter half is where the attack surface actually grows.** A provider key is a third party's
credential with a base URL the service would have to carry; `internal/credsvc/forward.go`'s
destination rules and the gateway's route validation exist precisely so that a credential goes to
the host it belongs to. Carrying a provider's base URL into the service's document puts a
configuration fact into a credentials-only bundle — the design point §6.7 flags as why CSR17 is not
a small task — and it is the reason this design recommends splitting the slice rather than landing
both halves together (**D1416**).

**What this design does not do.** It does not put the key in the agent's environment, it does not
give a review stage a credential the run does not already hold, and it does not let a repository's
own file choose which credential serves its review: `checkReviewClassModelPath`'s refusal of an
api-key repository (**F11**) is a refusal the plan keeps, and AK5's change is to *admit the key as
the class's own credential*, never to let the repository supply one.

## 6. UNVERIFIED

| # | What | Why it is unverified |
| --- | --- | --- |
| U1 | Whether an Anthropic API key may be used for the review under Anthropic's own terms for a **third-party** review product | The repository records no such term anywhere. This is an owner/counsel question, not a code one, and it is the substance behind clause 3 (**F20**) |
| U2 | The real per-review cost of an Opus-class review on the Anthropic key | No price is quoted here, by the repository's rule; the owner's own figures live in the private business repository |
| U3 | Whether a live installation that publishes **no** Claude account has actually deployed the review variant's job | The tree says yes and this design reads it that way (**F17**: the account axis always carries the no-account entry, the deployer builds the twin for it and the service admits it), but that is read from `config.ProjectJobAccounts`, `internal/infra/projectjobs.go` and `internal/credsvc/review.go`; no live installation was inspected |
| U4 | Whether the credentials service's review class has been exercised live for a repository whose `agent.auth` is `api-key` | The refusal is pinned by a unit test; no live run of the refused shape was found in the register |
| **U5** | **Whether the mixed run works on a real installation at all** — the in-job account path this design reads as the state of the art | **The owner's ask says Check 290 proved it on 2026-11-06, and the register says otherwise.** `docs/gcp-live-checklist.md`'s summary row for 290 ends "**USER-RUN, NOT RUN**" and its own section is headed "NOT RUN"; `docs/design/mixed-credentials.md` §3, §9 and §11, `docs/multi-model.md` §3 and `docs/releases/v0.20.0.md` all say the check "has not been measured live". So the rule is **built** (tasks B1–B6, 0.19.0) and **unmeasured**: every statement in §1 about the account path's behaviour rests on the code and its tests, and none on a run. This is also why the design's own live check (**Check 330**, implementation-plan §5) carries the contrast case — the same task run with and without a stage account — rather than assuming Check 290's numbers exist |
| **U6** | **Whether a cloud review launch is reachable at all on this tree** | **It is not.** `internal/cli/review.go`'s `reviewJobAdmitted` is a constant `false` and `checkReviewJobAdmitted` refuses every `fugaro review --pr N` with a sentence naming `fugaro run --pr N --recipe review-only` as the way to review today; `docs/releases/v0.20.2.md`'s known limits carry it forward from 0.20.0 ("the cloud review is built and not admitted"). The gate is `docs/cloud-senior-review` §1's **E7**, whose second half is the service admitting a project-scoped job's caller (**C3.9**, unscheduled), and the constant is flipped by **CSR11** only after the owner's live checks. **No live installation was inspected** — the refusal was read from the code and from the release notes — and the design's cloud slices, **D1410** and **D1422** are priced without this prerequisite because it is not theirs (**F10**) |

## 7. Owner decisions D1410–D1429

Recommendation first in each, with the reason; then the alternative and its cost. **The owner's
answer cell is empty in every row: none of these is answered, and nothing in this folder is an
approval.** **D1410–D1429 are this work's block** (§8).

| # | The question | Recommended, and why | The alternative, and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1410** | Which review does "the api-key review path" name — the in-job stage, the cloud review run, or both? | **Both, in three slices** (§4): the in-job slice is a test and a paragraph, the cloud Anthropic-key slice is small (**F12**), and the OpenRouter slice is §6.7. Writing them as one task would price a test at a service change. **The cloud slices are priced on their own code and not on a launch anybody can make today** (**F10**, **U6**) | **The in-job half alone.** Cheapest, and it satisfies clauses 1 and 4 — but clause 2's "a project with only an Anthropic API key" is the cloud half (**F17**), so the ask would be only half answered. It also sidesteps E7 entirely, which is the only reading under which this design ships something an installation can actually run | |
| **D1411** | For the in-job half, is `docs/design/mixed-credentials.md` §3's Path A the answer, or does the review need machinery of its own? | **Path A is the answer.** It works today (**F1**), it satisfies clauses 1 and 4, and "fits the existing machinery rather than adding a parallel one" is the ask's own instruction | **A stage-scoped review credential**, mirroring `stage_accounts`. Its cost is a new config key, a new precedence rule against `claude_accounts`, and a second credential in the runner — a parallel path for a capability the gateway already has | |
| **D1412** | Does the review get an Anthropic key of its **own** — a second secret, separate from the run's? | **No, in the first slice.** The run already has one key and the provider's key is already a different secret (**F21**); a second Anthropic key buys separation the record cannot even express (**F18**) | **A second secret** (a name, a `SecretMounts` entry, a gateway upstream selected per stage). Its cost is that `gateway.Upstream` is one value for the whole run, so the gateway would need a per-stage key — machinery nothing else needs | |
| **D1413** | When a project publishes **both** an Anthropic key and a stage account for `review`, which wins? | **The key, when the owner asks for it explicitly** — but the spelling is the decision. Today the account wins silently (**F7**) and no key says otherwise | **Leave the account winning.** Zero cost, and it keeps one rule — but an owner who adds a key expecting the review to use it gets notional dollars with no warning, which is the shape of bug Check 290's own configuration would hide | |
| **D1414** | May an `oauth` run's review move onto an API key? | **No.** The gateway is what holds a key, `GatewayOn` refuses `oauth` (**F8**), and an oauth run holds no key to move onto — the request is incoherent without a second credential | **A second key for an oauth run's review.** Its cost is the machinery D1412 rejects, plus a run that holds two credentials of two kinds at once — the thing `docs/design/mixed-credentials.md` §11's "never a second provider's key" was written to prevent | |
| **D1415** | Does the record name the credential a stage the run's **own** key served? | **A new field, or nothing — never a new value on `credential`.** `credential`'s stated meaning is "the credential that served this stage when it was not the run's own" (**F18**), so `"api-key"` there is a meaning change under an existing name, which CLAUDE.md forbids; a new field is additive and keeps both meanings intact | **Nothing at all**, which is the cheaper and defensible answer: the review's two configurations are already distinguishable (`credential: "oauth"` with `model_notional_usd == model_usd` for the account, neither for the key) and the model and the route name what served any stage. Its cost is that a reader must combine three fields to answer one question. **A new value on `credential`** is the third shape and is not available: it changes a meaning on a wire shape the console, the mobile app and the history read | |
| **D1416** | Does the cloud half land both keys at once, or the Anthropic key first? | **The Anthropic key first.** It needs no base URL, no auth shape and no new upstream (**F12**); the provider key needs all three and a bundle that is not a credential (**F15**) | **Both at once**, as §6.7's CSR17 sketches. Its cost is one SEC task that carries the whole provider apparatus and the bundle-contract question together | |
| **D1417** | For the cloud half, does the pairing name the **project's own** `anthropic-api-key`, or a review-scoped secret? | **The project's own key**, which is what the service already holds at tier B (**F12**). A review is a call the project pays for, exactly as a cascade coding run is | **A review-scoped secret** in the bundle. Its cost is a second entry in a closed key set (**F24**) plus a mount, for a distinction the accounting does not make | |
| **D1418** | Does the bundle grow a section for a provider's base URL and auth shape, or does the published policy carry them? | **Defer until D1416 is answered** — this is §6.7's own "design point this task must settle first", and it is a change to a contract another reader consumes | Either way: a bundle section that is not a credential, or a policy node that carries routing facts. Both are contract changes and both are priced in §6.7 | |
| **D1419** | How does the service reach a provider's endpoint? | **Not at all in the Anthropic-key slice** (**F12**): `upstreamKindAnthropic` with a `""` base is already the right upstream. A provider endpoint is CSR18's work | **A provider upstream in the service now.** Its cost is a destination rule, a base URL from a non-credential source, and a second way for a credential to leave the service | |
| **D1420** | What happens to `TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn` when the path lands? | **Rewritten, keeping its name** — §6.7's own instruction: the refusal is 0.20.0's answer and the test gains the second direction | **Deleted.** Its cost is losing the one test that holds the fail-closed direction (a repository's own file must never choose the review's credential) | |
| **D1421** | Does the Anthropic-key half ship in 0.20.0, in the release after it, or not at all? | **After 0.20.0.** The owner cut the path from 0.20.0 on 2026-11-06, and nothing here found a reason to reopen a cut the owner made with the full picture in front of them | **Into 0.20.0.** Its cost is re-opening a release whose tasks and live checks are already sequenced, for a slice §6.7 deliberately put outside every level | |
| **D1422** | May `fugaro review --pr N` be launched for a project with no Claude account at all? | **Yes, once the key is a pairing axis** — this is clause 2 (**F17**), and it is the whole point of the Anthropic-key half. **The variant is already deployed and admitted for that project** (the axis always carries the no-account entry, **F17**); what is missing is the credential | **No.** Its cost is that clause 2 is not answered: an installation with only a key still cannot have an independent review. **Either way the question is downstream of E7** (**F10**): every review launch is refused until the service admits the review caller, and **this design's slices do not carry that work** — pricing it here without E7 would price a launch nobody can make | |
| **D1423** | Which model does an api-key review run? | **The same model the OAuth review runs** — the class's own model path is unchanged by which credential pays, and the ask is about the credential and not the model | **A separate model choice for the key path.** Its cost is a second model resolution beside `applyLevels` and the class's, for no gain the ask names | |
| **D1424** | What is the cost basis of an api-key review? | **`runstore.BasisAPIList`** — `internal/runstore/cost.go`'s `ModelBasis` already answers `api-key` this way, and the review's dollars are list-price dollars like any other api-key run's | **A basis of its own for reviews.** Its cost is a third basis in a contract the console, the history and the mobile app read | |
| **D1425** | Is an api-key review held to the service's `budget.allowed_models` gate? | **Yes, unchanged** — the class is already held to it (`internal/runner/routing.go`'s allow-list arm, the owner's ruling of 2026-10-22), and which key pays does not change what may be called | **Exempt the key path.** Its cost is a review that may call a model the project's own policy refuses, which is the one thing the allow-list exists to prevent | |
| **D1426** | Is the cloud api-key review a **service** path, or a job-held key? | **The service path**, which is D143's posture: the job holds no credential and the service attaches one per call | **A key mounted into the review job.** Its cost is the `/proc` residual `SECURITY.md` records for a job that holds a credential, in the one job that reads untrusted content | |
| **D1427** | Is a live check owed, and by whom? | **Yes — Check 330** (implementation-plan §5), owner-run, on a sandbox installation and repository | **None.** Its cost is a path whose only evidence is unit tests, which is exactly the gap §6.7's own tasks were left with | |
| **D1428** | Which page carries the user-facing statement of the path? | **`docs/multi-model.md`**, whose §3 already states the Anthropic-key rule in the owner's words and whose §4 already shows the configuration | **A new page.** Its cost is a second place to keep true, beside a page that is already the user-facing answer | |
| **D1429** | Does the plugin's `routing` skill change? | **No**, unless a command or flag changes. The skill is checked against the CLI by `plugin/`'s tests, and this design adds no command | **A skill section describing the review's credential.** Its cost is a skill that documents behaviour rather than commands, which is not what the skill tests check | |

## 8. The number block, and how it was read

**The decision block.** Read on **2026-11-07** against `origin/main` at `aa26f322f` and against every
open pull request's head:

- `git grep -n 'D141[0-9]\|D142[0-9]' origin/main -- docs/` returns **no path at all**.
- The same grep over the fetched head of every open pull request returns no path either.
- **Positive controls**, run so that "nothing" is not a broken command: `git grep -c D1409
  origin/main -- docs/` returns three files (`docs/catalog-build-context/README.md`,
  `implementation-plan.md`, `reconciliation.md`), and `git grep -n D1390 origin/main -- docs/`
  returns the same folder — so the command does find a decision number that is present.
- The highest decision written on `main` is therefore **D1409**, the tail of
  `docs/catalog-build-context`'s block (merged as #1805), and **D1410–D1429 are free**. They are all
  written above and **not one is answered**; none is reserved unused.

**The check block.** The register's highest **written** `## Check` section on `main` is **290**
(`docs/gcp-live-checklist.md`, the mixed-credentials run). **291 is claimed** by `docs/image-env`;
**292 is claimed by `docs/catalog-build-context`** (merged on `main` as #1805, at its own README's
numbers table and its implementation-plan's §6, both read for this folder's D1409 above);
**293–299 are claimed by nobody**; `docs/stateless-orchestrator` claims **300–309** and
`docs/cli-output-console-graph` claims **310–314**. This work takes **Check 330**, well above every
one of them, so no renumbering is owed whichever order the claims merge in. Like every claim above
the highest written section, it is **claimed and not reserved**: the register's rule is that a claim
is settled at its merge, and the register's own row and section are written by the task that
implements the check, under the number free at that merge (implementation-plan §5).

**This folder's own numbers.** Findings **F1–F24** and tasks **AK1–AK8** are this folder's, and take
no number any other work reads.

## 9. Open items

These are the owner's. The recommendation and the cost of each are in §7; this is the index, with
the reason each one exists.

1. **D1410 — blocking, and first.** The ask names one path and the tree has two reviews (§4).
   **No task in [implementation-plan.md](implementation-plan.md) may start before this is
   answered**, because the answer decides which slice the plan is about. The two halves also differ
   in when they can be used: the in-job half runs today, and the cloud half is refused at the launch
   until **E7** is met (**F10**, **U6**) — so an answer of "both" is three slices of code and a
   path an owner still cannot reach.
2. **D1421 — the release.** The owner cut the api-key review from 0.20.0 on 2026-11-06; this design
   reopens nothing, and the plan books no release until the owner says which one (**F11**, §4).
3. **D1412 and D1413 — the in-job fork, and the one that decides whether the first slice is a test
   or a config key.** The run has one Anthropic key (**F6**) and a published stage account silently
   wins (**F7**).
4. **D1414 — the `oauth` run.** A subscription-coder run can never have an api-key review today
   (**F8**), and the ask does not say whether it should.
5. **D1415 — the record.** `StageTiming.Credential` cannot name a credential the run's own key
   served without changing a meaning on a wire shape the console, the mobile app and the history
   read (**F18**), so the answer is a new field or nothing.
6. **D1416, D1417, D1418 and D1419 — the cloud fork, and the one that decides the slice's size.**
   The Anthropic-key half needs a pairing that may name the project's key (**F12**, **F13**); the
   OpenRouter half needs the provider-key apparatus §6.7 describes (**F15**).
7. **D1420 — the refusal.** `TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn` is the test that holds
   the fail-closed direction, and §6.7 says it is rewritten rather than deleted.
8. **D1422 and D1427 — clause 2 and the evidence.** Whether a project with no Claude account may
   launch a review at all (**F17** — and note the variant is already deployed for it: what it lacks
   is a credential, not a job), and whether the owner runs Check 330 to prove the path. Both are
   downstream of **E7** (**F10**, **U6**).
9. **U1 and U2 (§6) — the terms question and the cost.** Both are outside the tree: clause 3's
   substance (**F20**) is an owner/counsel question, and the figures are the owner's own.
