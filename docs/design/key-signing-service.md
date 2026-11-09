# The key signing service

**Status: design and threat model. No code.** `ee/keyservice/` does not exist, nothing mints a key
but a person, and no infrastructure for the service is written. Until the entity exists and the
service is built (T13, S8.1) keys are **minted by hand** (S2.10), and every sentence below about the
running service is a design rather than a description.

This is subscriptions **S3.1**, the first task of slice S3: the design the tasks after it are written
against — the issuing service (S3.2), the admin page and the manual queue (S3.4), the dedicated
project's infrastructure (S3.5), the register of the slice's checks (S3.6), the website's forms
(S3.3) — and the threat model the plan's review focus names ("The key service, attacked",
[implementation-plan.md](../subscriptions/implementation-plan.md) §"Global constraints and review
focus"). It also carries the runbook for the two OWNER-RUN tasks that end the slice (S3.7's deploy
and S3.8's key creation), which the plan's release actions leave here rather than in an operator's
release note.

Where a sentence below is settled it is because a ruling settles it and the ruling is cited beside
it; where nothing settles a question the sentence says so, and §12 carries it as an open decision
for the owner.

**Numbers.** The subscriptions work holds **D430–D459**
([README.md](../subscriptions/README.md) §"The numbers this work holds"), of which D430–D448 are
written; this design's eleven questions take **D449–D459**, the reserved half — free when this
document claimed it (no `D449`–`D459` appeared anywhere under `docs/` or in `internal/`), and
written by §12, which is why this commit moves the half from reserved to written in the work's own
two registries ([README.md](../subscriptions/README.md) §"The numbers this work holds",
[reconciliation.md](../subscriptions/reconciliation.md) §"Numbers this work holds"). Its checks are
**Checks 170–179**,
which the plan already names for this slice and S3.6 registers; this document names them (§10) and
does not register them, because a check number is claimed by its section in
[docs/gcp-live-checklist.md](../gcp-live-checklist.md).

**What this document is not.** It is not the key format (that is
[docs/licensing/licence-key.md](../licensing/licence-key.md), S2.1, and this design adds one rule to
it, §2), not a verifier (S2.3–S2.5), not the lapse state the console and the app enter after the
grace (S6.1), not the website's privacy notice (S7.3) and not the merchant's commercial terms: D442
names the merchant and its fee rates and the tax plan are private (D430), so no figure appears here.

## 1. What the service is (T13)

**Ruled, 2026-10-27 (T13,
[reconciliation.md](../subscriptions/reconciliation.md) §"Resolutions"):** the key service is
**owner-operated in a dedicated Fugaro GCP project** — Cloud Run, a **non-exportable** KMS signing
key, and the internet-facing intake (the forms, the merchant's events and the admin page) **split
from a non-public minter that alone holds the signer role**. It is built **after the entity exists**
(the service issues keys as the Licensor, item 7 of T13), and hand minting is the interim.

| The service is | The service is not |
| --- | --- |
| The one place a key is minted, once it exists: free keys through its checks, paid keys from the merchant's events, Enterprise keys from its admin page (§4–§6) | In any path the product takes. Nothing in the CLI, the console or the app calls it: verification is offline (R2, [reconciliation.md](../subscriptions/reconciliation.md) §"Resolutions"), and the only routes in are a person's browser and the merchant's webhook |
| A component of the licensor, in a project the owner operates, with its own Terraform and its own deploy (§3.4, §11) | An installation's component. It never lives under `deploy/terraform`, and no `fugaro` command deploys, reaches or configures it |
| A service that mints offline-verifiable keys of one format (S2.1), so a key it issues and a key minted by hand are indistinguishable to every verifier | A revocation point, a phone-home, a seat counter or a telemetry sink. Nothing in it makes any product check anything at run time |

The last row is the whole of D440's amendment to R2: the spec's "no Fugaro-hosted dependency"
becomes "no hosted dependency **at runtime**", and this service is reached when a person asks for a
key, never by the product.

## 2. The key it issues (S2.1, restated with one addition)

The format, the claims, the states (valid / grace / expired), the expiry-only rule (O4), the tier
maxima and the two-key overlap are **`docs/licensing/licence-key.md`'s** and are not redesigned here.
What this design has to state is what the service puts in the two claims it alone computes.

| Claim | What the service sets it to |
| --- | --- |
| `tier`, `features`, `seats`, `organisation` | The check that passed (§4) or the purchase that arrived (§5). The plan's paid path states the seats the customer bought; a Free Plan key's tier and features are D449's |
| `issued` | **The service's own clock at the mint, never a caller's value** |
| `expires` | **The end of the term the key covers, never beyond `issued` + the tier's maximum (S2.1)** |

The two bolded entries are the one rule this design adds to S2.1's, and they close, for
service-minted keys, the hole that note lists under its tier maximum: "`issued` is the minter's to
choose, so a key minted with an `issued` far in the future passes the length check and then reads
`valid` … for as long as its minter wants". A caller of the service's minter supplies the tier, the
claims and the term; it cannot supply a date. The hole stays open for **hand minting**, which is the
owner's own tool on the owner's own machine (S2.10), and closing it there would take a rule S2.1
deliberately does not have — so it is stated as a limit rather than fixed (§9.2).

**A renewal is the case the `expires` rule is written for.** It is minted 30 days before the key it
succeeds expires (S2.1's overlap, §5.3) and covers the term that begins where that key ends, so its
`expires` is the end of the covered term — `issued` plus a term plus those 30 days, for a term of the
length its predecessor covered. A minter that dated `expires` as `issued` plus the bare term would
hand every renewal 30 days short of the term the customer paid for, and would take those days from a
customer who cancels at the end of it. That is why S2.1's tier maximum is the term **plus** the 30-day lead (its §"Limits, accepted"):
the ceiling is the longest key the design mints, not licence to over-issue.

Two more of S2.1's rules the minter obeys and nothing else in the slice states:

- **the payload is signed as it is spelled.** The signature covers the payload's raw bytes before any
  JSON is parsed, so the minter serialises the claim set once and signs exactly those bytes; a
  re-encode between signing and sending invalidates the key. The service never re-marshals a payload
  it did not mint;
- **the key id names the environment.** The production and staging keys are two ids, and a production
  verifier holds production ids only, so a staging key is *refused* by every production build rather
  than half-accepted. Test-mode merchant events mint staging keys and nothing else (§5.4), which is
  what Check 179 measures.

## 3. The shape: two surfaces, one signer

The split T13 rules is an **identity** boundary and not a code boundary: the intake is reachable from
the internet and cannot sign; the minter can sign and is not reachable from the internet. Everything
below assumes a reader who has the intake's code fully in hand.

### 3.1 The intake

One Cloud Run service, publicly invokable, that terminates TLS, serves the forms' endpoints, receives
the merchant's events, serves the admin page, and answers with a uniform page (§7.1). It holds:

- the verification-token store and the outbound mail path (§4.1);
- the free-mail list, the paid-domain lookup and the eligibility checks (§4);
- the merchant's webhook secret and the event store (§5.2);
- the admin session and its step-up (§6);
- the issuance record (§7.3).

It **cannot sign**: its service account holds no `cloudkms.signerVerifier` (nor any KMS role) on the
key, and it reaches the minter only by an authenticated call. Check 174's "the intake's … identity
[is] refused" is that refusal, measured once the service runs.

### 3.2 The minter

A second Cloud Run service with **no public invoker**: only the intake's service account holds
`roles/run.invoker` on it, and the call carries an ID token the platform verifies. It takes a claim
set, computes `issued` and `expires` itself (§2), refuses a claim beyond the tier's maximum, calls
the KMS signing API, and returns the three-part key. It holds nothing else: no mail path, no
merchant secret, no store of applicants' fields beyond the mint request it just served.

Whether that second service is a **service** or a **job** the intake triggers, and whether the mint
is synchronous (the form's answer) or drained from a queue (the admin's approval), is **D450** — the
split is ruled and its mechanism is not. The design's recommendation is a service, for the reason
Check 174 is written the way it is: two Cloud Run services give two identities the platform enforces,
which is the smallest thing an auditor can hold an IAM policy to.

### 3.3 The identities and the grants

| Identity | Holds | Holds nothing |
| --- | --- | --- |
| the intake's service account | `roles/run.invoker` on the minter; the mail path's credentials; read/write on the event and issuance stores; the admin session's own secret | any KMS role; any way to call the signer except through the minter |
| the minter's service account | `roles/cloudkms.signer` **on the one key ring**, `roles/run.invoker` on nothing, and read access to nothing an applicant typed | mail, the merchant's secret, the stores |
| a person's browser | nothing in the project | — |
| the owner, at the admin page | the admin session, after step-up (§6) | — |
| **any agent, any run, any installation** | **nothing** | everything: no agent mints with, holds or reaches a signing key, and no agent deploys or operates the service (R5 as amended by D440, [.claude/rules/ee.md](../../.claude/rules/ee.md)) |

The KMS key itself is asymmetric, non-exportable, and signed with **through its API** — never a
private key file, on any machine an agent reaches or the owner's (S2.10's own rule). S2.1 fixes the
algorithm as **Ed25519**, because the three verifiers verify that signature; whether the managed key
store offers it, and at which protection level, is **UNVERIFIED U9** and stays so until S3.5 confirms
it against the provider's documentation at build time. This design states the requirement rather
than the answer: a signing key whose private half cannot be exported, under a key id the verifiers
name, that produces a signature the S2.1 verifiers accept. A store that cannot do all three changes
the slice, not this document — and it is S3.5's red step to find out.

### 3.4 Where the code and the infrastructure live

**The code lives under `ee/keyservice/`.** The owner ruled on 2026-10-29 that the key-minting service
belongs under `ee/` "for example `ee/keysvc/`"
([ee-reorg/implementation-plan.md](../ee-reorg/implementation-plan.md) E4.1), and the plan names the
directory in three tasks' file lists and again in its shared-files table (S3.2, S3.4, S3.5). The two
names differ by
a syllable, and S3.1 is the task that decides ("with the location per S3.1"): this design takes
**`ee/keyservice/`**, because E4.1's `ee/keysvc/` is written as an example and the plan's task file
lists are written as paths, so the plan's spelling costs no later task an edit and reuses the name
`keyservice` that the plan and the status census already share.

**One Go module and one Go package.** The plan's own note says that if S3.1 puts S3.2 and S3.5 in one
Go package, S3.5 and S3.8 move one level later
([implementation-plan.md](../subscriptions/implementation-plan.md) §"Shared files"). They stay in one
package — the intake, the minter, the checks, the admin page and the queue are one program's parts,
they share the claim set and the record, and the split T13 rules is an identity boundary rather than
a compilation one (§3.3) — and the level move is worth it, because two Go packages would exist only
to satisfy a dependency graph. Every file there carries the two-line SPDX notice, and
`ee/keyservice/terraform/` is a sibling rather than a Go package.

**The infrastructure never lives in `deploy/terraform`.** That tree is embedded in the binary
(`deploy/terraform/terraform.go:15`, `//go:embed all:gcp`) and every resource type it declares is
covered by the one confirmation `fugaro init` gives for **the user's installation project**
(`internal/infra/tf/cover.go:18`, `TestAllowedTypesCoverTheModules`). A key ring, a signing key and
two services in a project the owner operates and an installation does not would either fail that
coverage or be planned and applied by an installation that does not own them. So the service's
Terraform lives at `ee/keyservice/terraform/` (S3.5), the owner applies it with their own credentials
(§11), and nothing about it is reachable from a `fugaro` command.

**The code is published, and that is a trade-off and not only a location** (T13 item 11): under `ee/`
the checks are visible in the public repository, so a reader can see exactly what the free-mail list
holds, how the open-source check reads a repository, and what the throttle is — which is why §4.6 and
§7 state what each check can and cannot prove rather than implying a strength. The alternative — the
private repository — would hide the checks and put the product's own licence code out of the tree
that builds the binary, which is a decision the owner has not taken.

## 4. The checks

Each check ends in one of three outcomes: **mint** (the key is issued now), **queue** (a person
decides, §4.5) or **refuse**. Which outcome each input gets is the plan's and S3.2's test names; what
this section adds is why, and what each check is worth.

### 4.1 Email verification and the free-mail list

The free seat is applied for with an email address, and **nothing is minted before that address is
verified**: the intake sends a single-use, expiring token to it and mints only when the token comes
back (`TestUnverifiedEmailMintsNothing`, `TestVerificationTokenSingleUse`). The address the token was
sent to is the one the key binds to, so a token cannot be transplanted to another address.

An address at a **free email provider** is not refused, and it is not minted either: it goes to the
queue. The Terms §6.3 read alone would refuse it ("addresses at free email providers are not
accepted"), but **T15 rules that an individual counts as an organisation of one** for the free seat
and the public-OSI rule, and an individual maintainer is exactly who holds a free-mail address — so
the refusal the Terms write for a *firm* would shut out the person T15 protects. The queue is where
that decision belongs, and it is also what the Terms already provide for: §6.3's "other requests"
are "from an individual without an email address on their organisation's domain". The list itself —
its source, how it is pinned, and who updates it — is **D453**.

### 4.2 One per organisation, by domain (T15, D432)

The free seat is one per legal person, and the service's proxy for the legal person is the **email
domain** (§6.3: "determined by email domain and, where it is known, by the legal person's identity").
A domain that already holds a free key gets no second one; it goes to the queue
(`TestSecondFreeKeyForADomainGoesToTheQueue`), where a person can see the second domain, the
subdomain, or the second legal person that the automatic check cannot.

That check is a proxy and it is a weak one: it is defeated by a second domain, by a subdomain, and by
anyone who can read mail at an address they do not administer. The design's answer is not a stronger
domain check but the pair of the queue and the record: the second application is not silently
refused, it is seen, and the record in §7.3 is what "where it is known, by the legal person's
identity" is kept for. UNVERIFIED U11 says the same thing about the whole check set from the other
side: whether an email domain, a free-mail list and the code host's API are *enough* to enforce it
has never been measured, and Checks 171 and 172 are where it will be.

### 4.3 A domain with a paid subscription

§10.2(a) is explicit that "a Customer that has a Paid Plan has no free seat", so a domain that the
service knows holds a paid subscription is **refused** rather than queued
(`TestDomainWithAPaidSubscriptionGetsNoFreeKey`). The knowledge is the service's own: its event store
(§5.2) holds every purchase, renewal and cancellation the merchant sent, keyed by the buying
organisation's domain, and that store is the only place the question is answered. A domain the
service has never seen is not evidence of anything, so this check catches a buyer asking for a free
seat and not a customer who bought under another domain — a limit, not a gap the design can close,
because the merchant's event carries the buyer's own details and the service has no other source.

### 4.4 The open-source check and proof of control

§10.2(b) and §6.3: the applicant gives **the URLs of its repositories** and an email address, the
service verifies that **every repository is public under a licence approved by the Open Source
Initiative**, and if so issues a key valid for one year. Two things follow, and they are the whole of
this check:

- **visibility and licence are read, per repository, from the code host's API** — public, and a
  licence the OSI approves. A private repository, a repository with no licence, and a licence that is
  not OSI-approved are each a refusal (`TestOpenSourceCheckRefusesAPrivateOrNonOSIRepository`), and
  the applicant's email must verify first, exactly as §4.1's does;
- **proof of control is required** (`TestOpenSourceCheckNeedsProofOfControl`), because the visible
  facts prove nothing about who is asking: F47 is precisely that "a repository URL and an email prove
  no control of the repository, so anyone can claim a well-known public project". What counts as
  proof is **D452** — the options are a token the applicant commits to a repository the check can
  read, an application installation that shows control of the organisation, or a signed commit — and
  the design does not pick one, because each has a different cost to an honest individual
  maintainer, who is T15's own case.

**"All repositories public" rests on the applicant's word** (T13 item 2): the API lists what the
applicant names, and it cannot see a private repository beside them. The check is therefore
best-effort and the Terms are the enforcement — §6.4 has the customer acknowledge that the Licensor
"relies on these Terms, and not on technical controls" — which is the same shape as S2.1's accepted
limits.

### 4.5 The manual queue

The queue is not a fallback for the checks' failures; it is the surface the Terms give the requests
the checks cannot decide: §6.3's "other requests" — an individual without a domain address, a
multi-seat trial, an academic or nonprofit application, and §10.2(c)'s academic institutions "on a
short application renewed every year" — plus the three cases above (a free-mail domain, a second key
for a domain, and anything a person wants to look at). A queued request notifies the owner
(`TestQueueNotifiesTheOwner`), the owner approves or declines, and **an approval mints once**
(`TestApprovalMintsOnce`) — the idempotency of §5.2 applied to a person's click.

### 4.6 What the checks cannot prove

Stated together, because a reader who trusts one of them should see the rest:

| The check | What it proves | What it cannot |
| --- | --- | --- |
| email verification | the applicant reads mail at that address, now | who they are, or that the domain is theirs |
| the free-mail list | the address is not at a provider on the list | that it is at the legal person's own domain; the list is only as fresh as D453's update path |
| one per domain | one application per domain string | a second domain, a subdomain, or the same person twice |
| the paid-domain lookup | the domain the merchant's events name has bought | a customer who bought under another domain |
| the open-source check | every repository named is public under an OSI licence, and D452's proof was given | a private repository beside them; whether the applicant is the project |

None of these is a licence boundary. S2.1's limits say the same of the key itself: the source is
visible, a clock set back extends `expires`, and nothing compares the organisation with the
installation. The Terms are the enforcement, and the service's job is to make the honest path easy
and the dishonest one visible in the record (§7.3) — not to be a door that cannot be walked around.

## 5. Paid keys from the merchant (D442)

### 5.1 The events

The merchant of record is **Paddle** for Team checkout (D442, [reconciliation.md](../subscriptions/reconciliation.md)
§"Decisions"), and the service's paid path is its events: a **purchase** mints a key stating the User
Seats purchased (§6.3, "following purchase"), a **renewal** mints the next key 30 days before the
current one expires (§5.3), a **cancellation** stops the reissue, and a seat change reissues
(`TestSeatChangeReissues`, `TestCancellationStopsReissue`). Enterprise is not this path: it is signed
and invoiced directly by the entity and its key is minted from the admin page (§5.5).

### 5.2 Signature, idempotency and order

Every event is **signature-verified before it is read** (`TestPurchaseEventNeedsAValidSignature`): the
raw body is verified against the merchant's signing secret, and a body whose signature does not
verify is dropped without a mint and without a store write. Every event is **idempotent by its own
id** (`TestPurchaseEventIsIdempotent`): the same event delivered twice mints once, because the record
of what was minted for which event is written before the answer is returned. A **replayed or
out-of-order** event is ignored (`TestReplayedOrOutOfOrderEventIsIgnored`): the store keeps the
subscription's own state and an event older than what it holds does not move it backwards, so a
cancellation followed by a replayed purchase does not resurrect a key.

The admin page's approval is the same idempotency in a person's hands (`TestApprovalMintsOnce`), and
the auto-renewal path is the same in a scheduler's: one mint per (subscription, term, seats).

### 5.3 Renewal, seat change, cancellation

The next key issues **30 days before expiry** (T26, [reconciliation.md](../subscriptions/reconciliation.md)
§"Rulings") and **two keys may be installed**; the verifier takes the latest valid one, so a renewal
landing early changes nothing for the customer and an issuing outage only bites once it lasts past 60
days (§8). The service's job in that window is small and precise: mint the renewal key with an
`issued` of the day it runs and an `expires` of the term's end (§2), never withdraw the current one
(the service has no revocation, by design — S2.1), and mint nothing at all for a subscription whose
state is cancelled.

A **seat change** reissues, because `seats` is a claim in the key (§2) and a customer who adds or
removes seats holds a key that states the number they bought — the console shows seats used against
it, advisory and never enforced (D439). A **cancellation** stops the reissue: the key the customer
holds keeps working until it expires, which is T25's accepted refund-and-cancellation leakage, and
the service's record says what happened.

### 5.4 Test mode and the staging pair

A merchant event in **test mode** mints a **staging key and nothing else**
(`TestTestModeEventsMintStagingKeysOnly`), against the staging key id of §2's second rule: every
production verifier holds production ids only, so a test-mode key cannot license a customer, and the
staging installation is the only place it is accepted. Where the staging key and its intake live —
one project and one key ring, or a second project — is **D459**; what is not open is that the two key
ids stay apart, because that is the whole of Check 179.

### 5.5 Enterprise keys

They are minted from the **admin page**, by the owner, from an Order Form: §6.3's "Paid
Subscriptions" path with the seats the form names, and the `organisation` claim an Order Form id
rather than a domain (S2.1's claim table). Nothing here is self-serve, and the per-tier limits and
the audit line of §6 apply to these mints exactly as they do to an approval.

## 6. The admin page and the manual queue

The page is a second authenticated surface that can mint **any** key for **any** tier (T13 item 8),
so it is the service's highest-value target and its defences are the design's, not an
implementation's afterthought. S3.4's test names are the list, and each row below says what the
defence is and what it is worth.

| Defence | What it is | Held by |
| --- | --- | --- |
| Authentication | the page is reachable only by the **owner's own account**, signed in through the project's own identity, and every other authenticated account is refused | `TestAdminRefusesAnyoneButTheOwner` |
| Step-up | a fresh second factor **per minting action**, not per session, so a stolen session alone cannot mint | **D455** (which factor) |
| CSRF | the page's own state is checked on every action, so a cross-site request from a page the owner visits is refused | `TestAdminRefusesACrossSiteRequest` |
| Escaping | every field an applicant typed is escaped **on render**, so queued text cannot become a stored script in a page that can mint | `TestQueueEscapesApplicantText` |
| Audit | every mint writes one line naming the tier, the seats, the plan and the actor, before the key is returned | `TestEveryMintIsAudited`; destination **D456** |
| Limits | a per-tier cap on mints in a window, so a compromised session cannot drain the key ring's authority | `TestPerTierMintLimit`; the caps **D454** |
| Notification | a queued request notifies the owner, so the queue is seen | `TestQueueNotifiesTheOwner` |
| Idempotency | an approval mints once | `TestApprovalMintsOnce` |

The person is the project's **owner account** and not a list, because there is exactly one licensor
and the page mints the Licensor's keys; whether a second person may ever hold it is part of **D455**.
The store the page reads holds applicants' free text, which is why escaping is a design rule here and
not a template's business (§9.1's row 8).

## 7. Abuse, privacy and the small print

### 7.1 Uniform answers

**Every answer reads the same**, whether the application is minted, queued or the domain already
holds a key (`TestAnswersDoNotRevealAnExistingKey`, T13 item 10): the page says the request was
received and what happens next, and never whether a key exists for a domain, which would turn the
form into a lookup for someone else's licence state. The queue's answers to the *owner* are not
uniform, and that is the point — the difference lives behind the authentication of §6.

### 7.2 Tokens and throttling

Three rules, and each answers a threat rather than a best practice:

- **single-use, expiring tokens** for verification links (and for anything else mailed), so a leaked
  link is not a key forever (§4.1, `TestVerificationTokenSingleUse`);
- **throttling on the intake** (`TestRequestsAreThrottled`): per-address and per-IP, so the service
  cannot be used to mail-bomb a third party's address or to enumerate (§7.1);
- **applicant fields are never interpolated into mail headers** (`TestApplicantFieldsCannotInjectMailHeaders`):
  the address the intake mails to is the one it verified, and any name or subject an applicant typed
  is data in the body, never a header value — which is the same rule as §6's escaping, on the other
  wire.

**All of it has to work inside the site's CSP.** The website serves one Content-Security-Policy
(`website/test/origins.mjs:712`): `script-src 'self'` plus named hosts, `connect-src 'self'` plus
named hosts, and **`form-action 'self'`** — widened, since S3.3, to the intake's one exact host,
which is D451's answer below. Two consequences the forms' task inherits:

- a **third-party challenge widget** (a captcha vendor) is not admissible without the owner widening
  `script-src`, `connect-src` and probably `frame-src` — an owner decision (`.claude/rules/website.md`),
  and one the site's privacy page's own promise ("nothing the Fugaro project operates") argues
  against. A **same-origin** challenge is admissible (`'self'` is a script the site serves itself),
  and so is the intake's own throttle;
- a form **posting to the intake's own origin** is blocked by `form-action 'self'` unless the owner
  widens that directive to the intake's exact host. The plan expected that widening (S3.3's Files
  name "the CSP tests the owner approves changing"), and the alternative — a same-origin path that
  forwards to the intake — is **D451**, which **the owner settled on 2026-11-07** as option (a).
  S3.3 carried it: `firebase.json`'s `form-action` carries the intake origin, `website/test/origins.mjs`
  admits a form action for this site or that one host and for no other, and the intake is admitted
  in no other directive. The visitor's personal data still reaches the intake, so §7.3's notice has
  to say so — that half is S7.3's and is not written.

### 7.3 The issuance record, retention and the privacy notice

**What is kept.** One record per issued key, in the service's store and in the owner's private
business repository (R5 as amended by D440, [reconciliation.md](../subscriptions/reconciliation.md)
§"Resolutions"): the key id, the tier, the seats, the plan it was issued under, the `issued` and
`expires` dates, the `organisation` claim, and what minted it — a check, a merchant event id, or an
approval. The record is the service's own memory: it is what §4.3's paid-domain lookup reads, what
§4.2's "where it is known" is kept for, and what §6's audit line adds to. It holds no key material,
and the private halves never leave the key ring (§3.3).

**What is personal data.** The applicant's email address, the organisation's domain, the repository
URLs, and the merchant's buyer data arriving in an event (T13 item 3). The design's rule is the
minimum and the stated period: the intake collects what a check needs and nothing else, the
verification token's address is dropped once the token is spent or expires, and the record's own
retention — for how long an address and a domain stay after the key expires, and what happens to a
deletion request — is **D457**, with counsel's own read (PIPEDA and GDPR) in S8.4's round.

**The privacy notice is S7.3's and this design sets its constraints.** `website/test/privacy.mjs`
rule 5 ("The project operates no host the software talks to") is what the service is an exception to:
the software still talks to nothing (R2), so the notice's sentence is restated for a service reached
when a *person* asks for a key, never by the product, and rule 5's scan is extended to `ee/` with
`ee/keyservice/` **excluded** — the service's own code names its host and the site's, which is what
the exclusion is for. The merchant of record appears as a recipient of buyers' data, and the whole of
it needs the owner's approval (all of it is S7.3's text, not this document's).

## 8. Uptime and the overlap (T26)

**The promise is issuance only**: validation is offline (R2), so a stopped service cannot stop a
single installation's work — an installed key keeps working until the grace ends. What an outage
costs is **new** keys: a renewal, a seat change, or a first application. The key format is what
absorbs it — the next key issues 30 days before expiry and two keys may be installed, so an outage
**bites only past 60 days** (T26) — and that is a property of S2.1's design, not of a queue the
service keeps.

The **uptime target** the plan asks for here is the owner's number and it is not written in this
document: what a target states is a commercial commitment, and the Terms carry none for the Free
Plans (§10.5: "no warranty, indemnity, service-level commitment or support"). So the design states
the **behaviour** it can be held to — the 60-day absorption, the renewal window, and what a form
submission gets while the intake is down (an honest failure page, and whether the request is queued
for retry) — and leaves the figure and the paid-customer promise to **D458**.

Two consequences of a stopped service worth naming: **the record is the only memory** (§7.3), so the
service's store must survive a redeploy; and **the verification token's expiry** must be long enough
that a person who gets the mail during a short outage can still finish, which is D457's period's
sibling.

## 9. The threat model

### 9.1 The twelve T13 names, and what stops each

T13's own list (item numbers below are its, [reconciliation.md](../subscriptions/reconciliation.md)
§"Rulings") is the threat model's spine; the columns are this design's.

| # | The attempt | What stops it | What it costs, or what is left |
| --- | --- | --- | --- |
| 1 | Reach the signing key | A non-exportable key in a managed store, one identity that may sign, no agent identity with any access, and a key id for rotation (§3.3, §2, §3.4; **Check 174**) | No revocation (S2.1): a leaked **signing** key's horizon is short key lives and rotation, not a recall. And `issued` is the minter's to choose, so a future-dated key outlives its tier's maximum (§2, §9.2) |
| 2 | Cheap abuse of the free checks | The free-mail list, one per domain, proof of control, the queue, and the record (§4; **Checks 171–172**) | Second domains, subdomains and a private repository beside the named ones are outside every check (§4.6, U11) |
| 3 | Harvest personal data through a bare mailbox | The minimum collected, a stated retention, and the notice (§7.3, S7.3; D457) | The intake must be able to mail, so one address per verified application is kept until D457's period ends |
| 4 | Use an outage to drop paying customers to read-only | The 30-day lead and the two-key overlap: an outage bites only past 60 days (§8, T26; **Check 175**) | The interruption is absorbed, not removed: a 60-day outage reaches customers |
| 5 | Break into the project the service runs in | A **dedicated** project rather than an installation's, two identities, and least privilege (§3.3) | One operator, one boundary: an owner's account is the whole of the administrative surface (§6, D455) |
| 6 | Impersonate a customer at the free seat | Email verification, the free-mail list, one per domain, the paid-domain lookup, the queue (§4) | The key binds to an organisation and nothing compares it with the installation (S2.1): a key can be shared |
| 7 | Mint keys as the Licensor before there is a seller | The service waits for the entity (T13); until then the owner mints by hand (§11) | Hand-minted keys carry the `issued` hole (§2) |
| 8 | Get at the admin page | Owner-only authentication, per-action step-up, CSRF defence, escaping, an audit line, per-tier limits (§6; **Check 173**) | The page is a second surface that can mint anything: D454's caps and D455's factor are the owner's answers, and until they exist the page is not built |
| 9 | Forge or replay a purchase | Signature verification before the body is read, idempotency by event id, ordering (§5.2; **Check 179**) | A merchant secret leaked to the intake mints Team keys until it is rotated — a rotation path owned by the deploy (§11) |
| 10 | Learn whether a domain holds a key | Uniform answers (§7.1), throttling (§7.2) | Timing still leaks in principle; the design's answer is the same sentence and the same work per request |
| 11 | Read the checks and walk around them | Nothing — the code is published under `ee/` (§3.4) | Deliberate: the checks are visible and the Terms are the enforcement (§4.6) |
| 12 | Use the hosted service as a runtime dependency | The product never calls it (R2, §1); the verifier makes no network call | The privacy notice is the only place the exception is stated to a reader (S7.3) |

### 9.2 The limits this design does not close

Stated rather than defended, the way S2.1's own are.

- **No revocation.** The service can stop minting and cannot recall: a key it issued works until it
  expires. A cancellation, a refund (T25) and a leak all end at `expires`.
- **The tier maximum bounds one key's length, not a signing key's horizon.** The service picks
  `issued` itself (§2), so a *service*-minted key cannot be future-dated; hand minting still can, and
  either way a leaked signing key mints a year-and-30-day key per call.
- **The record is not a control.** It makes abuse visible after the fact — the second domain, the
  buyer who asked for a free seat — and it cannot prevent it.
- **The checks are visible and best-effort** (§4.6, §9.1 rows 2 and 11).
- **The staging key is refused in production only by the key id** (§2): a verifier whose keyring
  wrongly carried a staging id would accept a test key, which is why S2.1's
  `TestTestKeyIDIsNotInTheProductionKeyring` is a rule and not a nicety.

## 10. Checks 170–179

The plan's register for this slice, with what each measures against this design. All are
**OWNER-RUN** and none has run; S3.6 writes their sections and this document does not.

| Check | What it holds this design to |
| --- | --- |
| **170** | A key from the service, or hand-minted with S2.10, verifies in the `ee` CLI, the console and the app **with the network off** — R2's "no hosted dependency at runtime", measured (§1, §2) |
| **171** | A free-mail domain, a second free key for one organisation, and a domain with a paid subscription get no free key — §4.1–§4.3 |
| **172** | The open-source check needs proof of control; it refuses a private or non-OSI repository and accepts an all-public OSI one — §4.4 (and D452 is what "proof" means) |
| **173** | The admin page refuses an unauthenticated visitor and any account but the owner's; a queued request notifies the owner; approval mints one key — §6 |
| **174** | The signing key cannot be exported; only the minter's identity signs; the intake's and an agent's identities are refused — §3.3, the slice's one security boundary |
| **175** | With the service stopped, installed keys keep working, and the next key, issued 30 days before expiry, overlaps the current one — §8 |
| **179** | A purchase in the merchant's test mode mints a staging-key Team key that production verifiers refuse; a renewal reissues it; a cancellation stops the reissue — §5 |

Checks 176–178 are other slices' (the lapse state and the coordinator's job) and are named here only
so the block's boundary is not mistaken for this design's.

## 11. The runbook (S3.7, S3.8 and hand minting — all OWNER-RUN)

The plan's release actions put this here: the service's deploy (S3.7) and the key creation (S3.8)
"belong to the licensor, not to an operator", so they have **no `### For operators` entry** and no
release note carries them. **No agent performs any step, and no agent holds or reaches a signing
key** (R5 as amended by D440).

**What waits for the entity:** the deploy and the checks that run against it (steps 3 and 4), which
is the step S8.1 gates — the service issues keys as the Licensor, so the entity exists and the Terms
name it as Licensor before it is deployed (D441) — and S3.5's Terraform, which the deploy applies.
**What does not:** the key ring's own steps, and what follows from them (1, 2 and 5). S3.8's note in
the plan says so in as many words — "This step does not need the entity: the key belongs to the
project the owner operates" — and that is why S2.10 mints by hand from S3.8 until the service exists.

1. **Create the project and the keys (S3.8).** Create the dedicated project, apply the key-ring part
   of `ee/keyservice/terraform`, create the **production** and **staging** signing keys in it, and
   **publish both public halves**. Hand minting can start here, and S2.9's production public half
   comes from this step.
2. **Mint by hand while the service does not exist (S2.10).** The owner mints one key at a time
   through the KMS signing API with their own credentials — never a local private key file — against
   S2.1's claim set, so the interim's keys and the service's are the same format. This is what
   Check 170's "hand-minted" half measures.
3. **Apply the rest of S3.5 and deploy (S3.7).** Apply the two services and their identities, deploy
   S3.2 and S3.4, and configure the merchant's webhook: the endpoint's URL and the signing secret
   (§5.2). Rotating that secret and the key ids is this step's, done from the owner's own credentials.
4. **Run the slice's checks.** Checks 170–175 and 179, recorded in
   [docs/gcp-live-checklist.md](../gcp-live-checklist.md) by S3.6's sections.
5. **The verifiers catch up.** Publishing the production public half into the CLI, the console and
   the app is S2.9, which depends on S3.8 rather than on the service being live — so a key minted by
   hand licenses an installation before the service exists.

**Key rotation, when it comes.** A new key id is added to the key ring and the public half is
published in a release of all three verifiers, because S2.1's rotation rule is a keyring entry and
not a server-side switch: the old id stays until the last key it signed has passed its grace
(`expires + 30 days`), and the new keys are minted against the new id. There is no revocation in that
path — an old key works until it expires — which is S2.1's accepted limit and not this step's to fix.

## 12. Decisions for the owner

### Settled

Restated with its source, so that nothing below is read as open that is not.

- **T13 (2026-10-27)** — owner-operated, a dedicated Fugaro GCP project, Cloud Run, a non-exportable
  KMS signing key, intake split from signing, built after the entity exists; hand minting until then.
  ([reconciliation.md](../subscriptions/reconciliation.md) §"Rulings")
- **T15 (2026-10-27)** — an individual is an organisation of one for the free seat and the public-OSI
  rule. (Same section; §4.1's queue is where it lands.)
- **T26 (2026-10-27)** — the next key issues 30 days before expiry, two keys may be installed, and an
  outage must exceed 60 days to bite. (Same section; §8.)
- **O4 (2026-10-27)** — the key carries expiry only: no covered-versions date and no release-date
  stamping. (§2, S2.1.)
- **The key binding (F35, F47)** — one key per organisation with a seat count, not per user. (§4.2,
  §7.3.)
- **D432, D439, D440, D442** — one free seat per organisation (an individual being one); seats are
  compared and never enforced; the evaluation key is three months and the open-source key a year;
  Paddle is the merchant of record for Team and Enterprise is invoiced directly.
- **D451 (2026-11-07)** — the site's forms reach the intake the owner's way: option (a), the policy's
  `form-action 'self'` widened to the intake's one exact host, and no page loads from it. The
  approval is recorded in
  [reconciliation.md](../subscriptions/reconciliation.md)'s §"Rulings" (S3.3 is the task that
  carries it). (§7.2; the notice it obliges §7.3 to say is S7.3's, still unwritten.)
- **R2 as amended by D440** — no phone-home at runtime; the service is reached when a person asks for
  a key, never by the product. (§1.)
- **R5 as amended by D440** — the record lives in the service's store and the private business
  repository; no agent mints, holds or reaches a key, deploys or operates the service. (§7.3, §11.)
- **D430, T22** — no figure from the private business repository enters any file, which is why §8
  states a behaviour and not an uptime number.
- **The plan's slice** — the code under `ee/keyservice/`, the infrastructure never in
  `deploy/terraform`, Checks 170–179 owner-run and registered by S3.6, and S3.7/S3.8's runbook here.
  (§3.4, §10, §11.)

### Open

| # | Question | Options | Recommendation | Cost of each | Status |
| --- | --- | --- | --- | --- | --- |
| D449 | Which `tier`, `features` and `seats` does a Free Plan key carry, and does the key record the plan it was issued under? | (a) all three free keys carry the Team plan's features, with the seats the plan allows and the plan's own name in the record only; (b) a `community` tier for the evaluation seat and the Team features for the open-source and academic keys; (c) a fourth tier for free keys | (a) | (a) §10.2(b) and (c) grant "the Team plan's features" and §10.3 makes the free seat a single-seat trial, so the key states what the customer gets, and the plan's name stays out of a format S2.1 fixed; (b) makes the console show a different tier for two keys that turn on the same features; (c) a new tier in three verifiers and a format change, decided in a design that does not own the format | open |
| D450 | What shape is the minter? | (a) a second Cloud Run **service** with no public invoker, called by the intake with an ID token; (b) a Cloud Run **job** the intake triggers, drained from a queue; (c) one service, two credentials inside it | (a) | (a) two identities the platform enforces and the smallest thing an IAM audit can hold — but a second deployed service to keep alive; (b) a queue drain is right for the admin's approvals and wrong for a form's answer, which then has to poll; (c) one process's identity is one, so the signer role would sit on the internet-facing service, which is what T13 splits | open |
| D452 | What counts as **proof of control** of a repository for the open-source check? | (a) a token the applicant commits to a repository the check can read; (b) an application installation that shows control of the organisation; (c) a commit signed by a key the applicant publishes | (a) | (a) one file in one of the repositories already named, readable through the same API, and it works for an individual with no organisation; (b) proves the organisation and asks an individual maintainer to install an app on their own account; (c) proves a key, not the project, and asks the applicant to hold one | open |
| D453 | Where does the free-mail list come from and who keeps it? | (a) a pinned list of domains committed under `ee/keyservice/`, updated by a task of its own; (b) a public list fetched at build time; (c) a hand-kept list in the service's configuration | (a) | (a) the check is hermetic, testable and reviewable in the public repository, and it is stale until someone updates it — which is why the update is a task rather than a habit; (b) a network fetch at build and a licence question about the list's own terms; (c) a change to the running service that nothing in the tree records | open |
| D454 | What are the per-tier mint limits, and what does a limit do? | (a) a cap per tier per window that **queues** the excess; (b) a hard refusal; (c) no cap until abuse is seen | (a) | (a) an honest burst (a batch of renewals) is seen and answered and a drain is stopped; (b) one mistyped renewal becomes a refusal for a paying customer; (c) the caps are what Check 173 measures, and "no cap" is not a limit to measure | open |
| D455 | Who may reach the admin page, and what is the step-up? | (a) the owner's own account only, with a hardware key or TOTP **per minting action**; (b) a second owner account as well, with the same factor; (c) a password and a session cookie | (a) | (a) one account and one factor, and a stolen session alone mints nothing; (b) a second person who can mint every tier is a second licensor in fact, which the Terms' Licensor wording has not been written for; (c) a session that mints a year-and-30-day key per click | open |
| D456 | Where does the audit log go, and who reads it? | (a) the project's own log sink, read by the owner, with the mint line repeated in the issuance record (§7.3); (b) the record only; (c) the record and an off-project sink | (a) | (a) one place to look and no new dependency, and the log's retention is the project's; (b) a log that a redeploy or a store bug can take with it, in the surface Check 173's audit is about; (c) an off-project copy is D457's personal data in a second place before D457 is answered | open |
| D457 | What does the issuance record hold, and for how long? | (a) the fields §7.3 lists, with the applicant's address and domain dropped once the key has expired, and the mints themselves kept; (b) everything kept indefinitely; (c) the minimum: the key id and the tier only | (a) | (a) the record answers §4.3's lookup and §4.2's question while it is needed and stops being a store of personal data after; (b) a perpetual store of scraped addresses, which no notice in this tree yet covers; (c) a paid-domain lookup that cannot answer, so §4.3 stops working | open |
| D458 | What uptime target, and what does a form submission get while the intake is down? | (a) no external target for the Free Plans (the Terms carry none, §10.5), the 60-day absorption stated, and an honest failure page; (b) a stated target for paying customers only; (c) a target for everyone | (a), or (b) with (a) | (a) matches the Terms as counsel wrote them and promises nothing Check 175 does not already measure; (b) a commitment that the merchant's and the Terms' own documents then have to carry; (c) a promise about a service whose interruption is already absorbed for 60 days | open |
| D459 | Where does the staging pair live? | (a) the same project and key ring, with the staging key id and a staging intake that is not publicly invokable; (b) a second project; (c) no staging service, a staging key minted by hand only | (a) | (a) one project to operate and the two ids Check 179 needs, with the staging intake's exposure as the thing to watch; (b) a second project's IAM, quotas and cost for a test-only path; (c) Check 179's "a purchase in the merchant's test mode" needs an event path, which (c) does not have | open |

## 13. Out of scope

- **The key format, the claims, the states and the tier maxima** — S2.1's
  ([docs/licensing/licence-key.md](../licensing/licence-key.md)). This design adds one rule to it
  (§2) and changes nothing else.
- **The three verifiers and the production public key** — S2.3–S2.5, S2.9, and their own plans.
- **The lapse state and what the console and the app keep after the grace** — S6.1, T12.
- **The website's privacy notice, the pricing page and the community page** — S7.1–S7.3, and their
  text is counsel's and the owner's, not this design's.
- **The merchant's commercial terms** — fees, tax and refunds are D442's and the private business
  repository's (D430).
- **The entity, the Terms' Licensor wording and the copyright** — S8.1, S8.3, S8.4.
- **The coordinator's key-gated job and Check 178** — S1.4, S1.9, S1.10, deferred with enforcement
  (D524).
- **Seat counting and the seat estimate** — S4.1, S4.2, D433, D439.
