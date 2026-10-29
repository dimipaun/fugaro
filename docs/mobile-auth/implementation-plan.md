# The mobile app's sign-in, pairing and allowlist — the implementation plan

**Status: plan only; no code is implemented; approval gate open.** This plan follows from
[reconciliation.md](reconciliation.md), which wins wherever it disagrees with [spec.md](spec.md).
**Amended 2026-10-22:** the owner accepted the parts **as recommended**, with **P0 and P1 approved**
and **P3–P5 ordered after P6.6 and P6.8**; the gate stays open for **every other part** — see
[README.md](README.md), which records the ruling and the provenance of the relay.
Every path and symbol below was re-read against `main` at `d9dda8f3` (`cli: fugaro --version and
fugaro -v print the version, like fugaro version (#794)`) on **2026-10-21**, and is cited by **path
and symbol, never by line number**. A path marked **new** does not exist yet.
**Owner: Dimi Paun. Date: 2026-10-21.**

**Every part is independently mergeable.** A part lands, it is verified, and the tree works with it
and without the parts after it. **Target release: the first release after the mobile Phase 6 tasks
this work extends — D236's recommendation — and not 0.11.0**, which the central orchestrator
(`docs/central-orchestrator/implementation-plan.md`) and the rigor modes work (open PR #773) hold.
No part assumes a release that has not shipped.

**The plan is written to D221–D236, and the owner has answered them — all sixteen as recommended,
on 2026-10-22.** Each recommendation is the one the parts mean, and every part names the decision
it would be rewritten for. **The plan is also written to a second thing it cannot supply
itself**: the mobile work's Phase 6 tasks —
`docs/mobile/implementation-plan.md`'s **P6.6** (`internal/cli/pair.go`,
`internal/budget/token/device.go`) and **P6.8** (`mobile/src/pairing/`) — are **approved 2026-10-18
and unbuilt**, and this work extends both. **Nothing here can be merged before they exist**, which
is §2's first dependency and §11's first clause.

---

## 1. What this plan is written to, and what changed since the reconciliation

The reconciliation read the tree at `d9dda8f3`. Four things it found shape the order rather than
decorating it:

1. **The reversal is one program.** `internal/cli/init_firebase.go`'s `refusePublicSignUp` is what
   stands between the spec's step 4 and every project in existence, and the shape of the change is
   a **narrower** refusal rather than a flag — the reconciliation's §3.1 lists the fields that would
   have to be admitted (`defaultSupportedIdpConfigs`/`oauthIdpConfigs` for `google.com` and
   `microsoft.com`, `signIn.email.enabled` for the link) and the fields that must stay refused.
   That is why P6 exists as a part: the owner should be able to read what the alternative costs
   without building any of it.
2. **The tree's answer is already built and already argued.** `internal/budget/rules/rules.go`'s
   `PathDevices` comment is the whole of D166 — *"It is deliberately not a claim"*, *"deleting this
   node revokes at once"* — and the owner's answer of 2026-10-20 already defined the entry the
   spec's allowlist would replace. So this work is **two fields and a resolver** on a record that
   exists (P1, P2, P3), not a new authorisation model.
3. **The rules generation stays where it is, and that is a finding rather than an omission.**
   `RulesVersion` is 3 (`internal/projectpolicy/version.go`), the generation the device node and
   the pairing nodes arrived with (#601). The device node's document entry is
   `config/admin/devices/$uid` with `.read: false, .write: false` and **nothing else** — no
   `validate`, no `$other` — and `userClaim` turns on the node's **existence**. So two fields
   written beside it change nothing a client meets, and the fields take **no generation**
   (reconciliation §F29, whose precedent is `version.go`'s own note that the credentials children
   "deliberately did not make a generation 4"). P2 is therefore not a rules-generation part: it is
   the part that **proves** the document does not move, and that says what would move it.
4. **The Phase 6 tasks this extends are unbuilt.** `internal/cli/pair.go` does not exist,
   `fugaro pair` is not registered in `internal/cli/root.go`, and `mobile/src/pairing/` is not in
   the tree. Every part here that touches the CLI (P3, P5) or the app (P4) is written as an
   **extension** of a task that has not landed, and §2 makes that the first edge in the graph.

**One thing this plan does not re-open.** The reconciliation's §3.2 sets out what the spec's path
does to the attack surface, and §4's D221 recommends against it. **This plan is written to the
recommendations**, so no part builds a provider session, an email link, a per-project Firebase app
instance on the phone, or `signInWithCredential` anywhere. P6 is the part that **writes the
alternative down** so that the recommendation is a decision the owner can check rather than a
sentence they have to trust.

## 2. The order, and why it is this order

Five dependencies force the order, and nothing else in it is arbitrary:

1. **Nothing can be built on documents nobody has pinned.** Every plan folder in this tree guards
   its own documents with a test — `docs/central-orchestrator/implementation-plan.md` names the two
   models to follow, *"`internal/cli/docs_coordinator_test.go` and
   `internal/cli/docs_windows_cli_test.go`, both on `main`"* — and this folder has none
   (reconciliation §F27). That is **P0**, and it is the only part that depends on nothing.
2. **Nothing can be written to the node until the entry's shape is decided.** The resolver (P1) is
   a pure function: no I/O, no credential, no database. It decides who is an admin, so it is the
   part that has to be right before anything calls it.
3. **Nothing can be written to the node until it is proved to hold it safely.** P2 is the part that
   settles what the deployed document does with a node carrying `email` and a `role` — that a client
   still reads nothing and writes nothing, that `USER` still turns on existence, and that an entry
   written before the fields existed is still admitted — and it is the part that would notice if any
   of that were untrue.
4. **The writer comes after the shape and that proof.** P3 is `fugaro pair` extended, and it
   depends on P6.6 existing as well as on P1 and P2. It is the first part that touches a credential,
   an IAM role and a typed confirmation, and it is the first part a person can feel.
5. **The declaration comes last among the mechanisms.** P5 publishes a declared list into the node,
   which is only meaningful once the node holds entries and a command writes them — and it must
   copy an existing rule faithfully (`publishAllowRepos`: *"a later publish never re-adds a name an
   operator removed"*), which is easier to check against P3's writes than against a description of
   them.

**P6 is outside the order on purpose**: it is documentation about the path this plan refuses, and it
can be written at any time. It is placed last because it cites §3 of the reconciliation, and §3 is
where a reader is sent from the decisions.

## 3. What is deliberately not in this plan

Named, so that a reader does not look for it and a later writer does not re-add it by accident:

- **Any provider session, anywhere.** No `signInWithCredential`, no enabled provider in any
  project, no change to `refusePublicSignUp` in v1 (D221). The change is written down in P6 and
  nowhere executed.
- **The email-link flow** (D228): no action URL, no App Links, no `assetlinks.json`, no
  `apple-app-site-association`, no committed reverse-DNS identity in `mobile/`.
- **The app's per-project Firebase instance** (D234): no Firebase SDK in `mobile/package.json`, no
  third secure-store item, no per-project session or listener.
- **The console's allowlist screen** (D235): the spec's *"later from the console"* needs the
  console's project-wide view, which is unbuilt (`docs/design/desktop-console-v2.md`).
- **Custom tokens minted by the credentials service** (D235, and reconciliation §0.2): the spec's
  own deferral, dependent on `docs/credentials-service/`'s first slice existing.
- **The domain rule in the rules** (D223): the domain is resolved at administration, and no part
  writes a `matches()` predicate over `auth.token.email`.
- **A rules generation** (reconciliation §F29): the fields travel on a node the deployed document
  already denies to every client and already tests only for existence, so the template, the golden
  and `RulesVersion` are untouched — and a part that finds it needs one stops and asks, because a
  generation is a deploy the owner has to run on every installation.
- **`belong`, EdgeWeb and EdgeServer** (reconciliation §F28): no part of this work reads, writes or
  names any installation, and no fixture is taken from one.
- **Anything under `.github/workflows/`**: no part needs one; if one ever does, it goes to
  `docs/pending-workflows/` for the owner to copy.

## 4. What the owner must answer before any part starts

The plan is written to the recommendation in each decision; a part whose decision is answered the
other way is rewritten before it starts, not after. **Sixteen decisions, D221–D236**, all in the
reconciliation's §4 — and two of them carry most of the weight: **D221** decides whether any of the
provider half exists, and **D231** decides where the allowlist's record lives.

| Decision | What it blocks | The recommendation the plan is written to |
| --- | --- | --- |
| **D221** | P6, and every part if answered the other way | **no** provider session reaches a project's database; sign-in stays custom-tokens-only, and a provider sign-in (if any) is checked once, at pairing |
| **D222** | P1, P2, P3, P5 | the allowlist is the **device node**, with the administered email and the role as fields on it; no email-keyed map |
| **D223** | P1, P3 | the domain rule is enforced **at administration**, when the entry is written |
| **D224** | P1 | the exact-over-domain resolution runs **once, in the CLI, at the write**; the node stores the resolved role |
| **D225** | P2, P3 | the role is a **field on the node**, written day one, read by the CLI and the console, **never by the rules** in v1 |
| **D226** | P3 | the verified email is checked **at pairing**, by the CLI, in the shape `internal/cli/init_members.go`'s `userEmail` already uses |
| **D227** | P3, P6 | Apple is identity-only; a private-relay address is never an allowlist key and the relay domain is never a domain entry |
| **D228** | P6 | the email link is **refused in v1, by name** |
| **D229** | P3, P5 | the command is **`fugaro pair`, extended** — not a new `fugaro allow` family |
| **D230** | P3, P5 | the first pairing writes the first admin, and `fugaro init` records the seed address it will match |
| **D231** | P5 | the **node is the source of truth**; the declared list in the project's `fugaro.yaml` is a seed and a record, published to the node, and a publish never re-adds what an operator removed |
| **D232** | P4 | **two secure-store items stay two**; no provider credential is stored |
| **D233** | P4 | the QR payload is unchanged: the Firebase configuration and the one-shot pairing token, **never an identity** |
| **D234** | P4 | v1 has **no** per-project Firebase app instance; the account model stays D114's |
| **D235** | the plan's whole shape | v1 is the allowlist's two fields and the CLI that writes them; the provider half, the console screen and the minted custom tokens are later |
| **D236** | the release, and the branch | the **first release after the mobile Phase 6 tasks**, and not 0.11.0 |

Two things the table cannot express, said here so they are not lost:

- **D221 and D231 are decisions about a compromise, not about convenience.** D221 decides whether a
  public API key in a QR is a session or nothing; D231 decides whether a removal is one write or a
  synchronisation. Each recommendation is the one that keeps the property the tree already argues
  for — default-deny, and revocation that does not wait for a token to expire — and each
  alternative is a real posture rather than a worse default.
- **Nothing in this plan says the parts may start the day the gate passes.** P0 and P1 can: neither
  touches the CLI, the rules, the app or a credential. **P3, P4 and P5 wait on Phase 6**, and §7
  says which parts an independent reviewer must take.

## 5. The parts

Every part has the same shape: **Scope**, **Files** (the paths and symbols it touches), **Red step**
(the failing test written first, and the mutation that proves it), **Verification**, **Depends on**,
**Reviewer tier**, **Security-sensitive**, **Commit**.

Reviewer tiers are this plan's vocabulary, and it is the coordinator plan's: **standard** (one
reviewer, the usual path), **careful** (a reviewer who reads the record, the rules or the plan), and
**SECURITY-SENSITIVE** (an **independent review by a reviewer who did not write it, on a different
model** — this plan asks for Opus — with its own paragraph in the pull request body saying what was
reviewed and what was not).

**P0 and P1 need no new decision, and the owner's approval of 2026-10-22 opens both.** P2 needs
D222/D225. P3 needs the Phase 6 pair to exist. P4 needs Phase 6's P6.8. P5 needs P3. P6 needs only
D221's answer. **The decisions are answered and the parts are not**: P0 and P1 are the two the
owner approved, and every other part is still reviewed on its own, on a branch of its own.

---

### P0 — Pin this folder, its findings, its decisions and its parts

**Scope.** This folder has no guard (reconciliation §F27), and the neighbouring folders show why it
needs one: `internal/cli/docs_mobile_mailbox_plan_test.go`'s header records a pin that discovered a
plan citing a file three times and listing it nowhere, and the coordinator's own guard exists
because *"the spec stops being verbatim"* is a thing a later edit can do silently. The part adds
`internal/cli/docs_mobile_auth_test.go`, holding: **the spec is the owner's text** — its title, its
six section headings, and the absence of any decision number, any finding number and any of this
folder's own vocabulary in it; **the four documents exist** and the index's status line says *"plan
only; no code is implemented"*; **the decisions are D221–D236**, contiguous, each written as a
`### D<n> — …` heading with its **Recommendation** first and its **Alternative**/cost after it
(D235, which scopes v1 rather than choosing between two things, states none — the guard names it as
the one exception);
**the parts are P0–P6**, contiguous and in order; and **the paths the reconciliation and the plan
cite resolve on disk**, under the rule below. **What the guard does not hold is stated rather than
implied**: a symbol named beside a citation without the possessive form is outside it, exactly as the
coordinator plan records of its own pair of checks; and the guard reads **this folder only** — it
makes no claim about `docs/mobile/`, `docs/central-orchestrator/` or the register, which their own
pins hold.
**One test is deliberately unusual**: a scan that no line of this folder carries a Firebase API key
shape (`AIza…`), a PEM header, or a reverse-DNS identity literal — because this folder is *about*
Firebase configuration and the next person to edit it is the one most likely to paste a real one in.

**The citation rule, written out, because it cannot be "every path resolves".** Not everything that
looks like a path is one: these documents cite **Realtime Database node paths** and an **IAM role**,
which are not files at all, and a **dependency directory** the repository never carries. So the check
has a **filter** and an **excuse list**, and both are part of the part:

- **The filter: a citation is a path only if it is a backticked single token, contains `/`, and its
  first segment is a directory of *this* repository** — and that list is **read from the top level of
  the tree at run time** rather than written into the guard (`docs/`, `internal/`, `mobile/`,
  `shared/`, `schemas/`, `deploy/` and their siblings, `.github/` among them), so a directory added
  later is covered without an edit and a directory renamed breaks the check loudly. That is what
  tells a file from the two things these documents cite constantly and no guard can open:
  **Realtime Database node paths** (`config/admin`, `config/admin/devices`, `config/policy/shared`)
  and **IAM roles** (`roles/firebasedatabase.admin`) — neither of which starts with a repository
  directory. Anything with a space, a `|`, a `—` or a glob in it is prose, not a citation.
- **The excuse list, by name, each entry with its reason, and each entry required to be *stale-able*.**
  **Three** paths this work will create (`internal/cli/docs_mobile_auth_test.go` — this part;
  `internal/mobileauth/` — P1; `docs/mobile-auth/provider-sign-in.md` — P6) and four that another
  approved, unbuilt task owns (`internal/cli/pair.go` and `internal/budget/token/device.go` —
  mobile Task **P6.6**; `mobile/src/pairing/` and `mobile/src/overview/` — Task **P6.8**) are
  excused **only while they do not exist**. The guard **fails on an excused path that resolves**,
  which is the property that makes the list self-cleaning: the pull request that lands P1 or Phase 6
  deletes its entry in the same change, and a reader can see from the list alone what is still
  missing. **`mobile/node_modules` is excused differently and permanently** — it is a dependency
  directory the repository never carries, the app's manifest is the citation, and the guard says so
  in its own line rather than folding it into the list above.
- **The reconciliation marks no path `new`, and the plan marks only its own** — which is why the
  excuse list comes from **both** documents rather than from a marker: the plan marks
  `internal/cli/docs_mobile_auth_test.go`, `internal/mobileauth/` and
  `docs/mobile-auth/provider-sign-in.md` **new**, and says of P6.6's two files that they are Phase 6's
  and of `mobile/src/pairing/` that it is P6.8's; the reconciliation's §F23 names all four of the
  borrowed ones at once — *"Task P6.6 … is `internal/cli/pair.go` + `internal/budget/token/device.go`;
  Task P6.8 is `mobile/src/pairing/` and `mobile/src/overview/`"*. **The guard holds the list rather
  than re-deriving it**, so an implementer who finds a fifth must add it visibly instead of loosening
  the rule.

**Files.** New `internal/cli/docs_mobile_auth_test.go`; `docs/mobile-auth/`'s four documents.

**Red step.** Seven mutations, each a different check. **Rename a path** one of the citations names —
`internal/budget/rules/rules.go` moved to a name that does not exist in a scratch tree — and the
citation check must fail, naming the path. **Add a line number** to a citation (`rules.go:433`) and
the same check must fail. **Change one byte of `docs/mobile-auth/spec.md`** — insert a decision
number, or reword a heading — and the verbatim check must fail. **Delete a part heading** and the
parts check must fail. **Renumber one decision** (D236 to D237) and the decisions check must fail,
naming the number and the position. **Paste an `AIza…`-shaped string** into the reconciliation and
the secret-shape scan must fail, naming the file. **Make an excuse stale** — create the file
`internal/mobileauth/` in a scratch tree and leave its entry on the excuse list — and the check must
fail, naming the entry and the pull request that owes its deletion. Restore each before the next.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/cli/ -run
TestDocsMobileAuth`; and the docs pins of the folders this work reads — `go test ./internal/cli/
-run 'TestDocs|TestMailboxPlan'` — unchanged, run to prove this part touches nothing outside its own
folder and its own guard.

**Depends on.** Nothing. It is the only part that depends on nothing at all.

**Reviewer tier.** Standard. **Security-sensitive.** No.

**Commit.** `cli: pin the mobile auth folder, its decisions and its parts`

---

### P1 — The entry as data, and the resolution rule

**Scope.** D222, D223 and D224, as a pure function: a new package **`internal/mobileauth/`** holding
the **entry** (an address, a role, the time it was written and who wrote it), the **role** as a
closed set in the order the spec names (`viewer`, `operator`, `admin`) with the ordering the
resolution needs, the **address normalisation** two writers must agree on (the domain lower-cased,
the local part kept as written, no display-name form accepted, one address never equal to two), and
the **resolver**: given the exact entries, the domain entries and an address, the resolved role, with
an exact entry overriding its domain's. **It reads and writes nothing**: no database, no credential,
no file, no clock it does not take as an argument. The package is where the rules' own argument
lives in code — a resolver that a rule cannot perform (F13) is a resolver that has to be right in
one place and tested there.

**Files.** New `internal/mobileauth/` (`Entry`, `Role`, `Resolve`, `NormalizeEmail`, the node-key
encoding) and its test plus testdata; no other file.

**Red step.** Five mutations. **A domain entry alone admits its member** — remove the domain branch
from the resolver and the test that pairs a person through a domain must fail. **An exact email does
not override its domain's role** — reorder the two branches and the test that gives one address a
different role from its domain must fail. **A role outside the closed set is accepted** — widen the
set and the test that an unknown role is refused must fail. **Two spellings of one address resolve
differently** — drop the normalisation and the test that case-folded domains compare equal must
fail. **A key cannot be read back as the address it was made from** — drop the encoding's inverse
and the test that an entry's node key round-trips must fail.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/mobileauth/...`. No
emulator, no fake and no network: this part has nothing to fake.

**Depends on.** **Nothing but its own answers** — D222, D223, D224. It reads no database and holds
no credential, so it is mergeable the day they are answered.

**Reviewer tier.** **SECURITY-SENSITIVE.** **Independent opus review.** **What the review must
cover:** the override order and why it is that order; the normalisation's failure modes (what it
refuses, and what it collapses that should not be collapsed); the role set's closure and what an
unknown or absent role resolves to — and the fact that the resolver is the **only** place a role is
decided, so a mistake here is a person's authority and not a display bug.

**Commit.** `mobileauth: the allowlist entry, its roles and the resolution rule`

---

### P2 — The node's fields, and the rules document that does not move

**Scope.** D225, and **reconciliation §F29**: the device node at `config/admin/devices/$uid`
(`internal/budget/rules/rules.go`'s `PathDevices`) carries the **administered email** and the
**role** as children, and **the deployed document is not touched**. That is not a shortcut, it is
the reading: the template declares the node as `{ ".read": false, ".write": false }` with no
`validate` and no `$other` beneath it, `userClaim` (the `USER` macro) turns on the node's
**existence** — `root.child('config/admin/devices').child(auth.uid).exists()` — and nothing in the
document names a child of it. A generation is what the tree's own precedent says is **not** owed
here: `internal/projectpolicy/version.go`'s generation-3 note records that the credentials children
"deliberately did not make a generation 4", being optional children of a node that behaves
identically without them, where a bump "would make `projectpolicy.Read` answer 'not found' and drop
every published policy … on any installation whose owner has not redeployed the rules yet". **So
the part is a proof and a fence, and it is a part rather than a paragraph because three of its
claims are the sort that quietly stop being true:** that a client still cannot **read** the node,
still cannot **write** it, and that a node **without** the new fields still admits its holder. It
also states, in `PathDevices`' comment and in the plan's own §3, **what would move the document** —
the first rule that *reads* one of these fields (D225's later half) or a `validate` over the node's
shape — so that a later writer adds the generation deliberately instead of discovering it.

**Files.** `internal/budget/rules/rules.go` (the `PathDevices` comment: the fields, and why the
document does not move); `internal/budget/rules/emulator_pairing_test.go` (the cases below);
`internal/mobileauth/` (the field names P1 defines, read rather than repeated). **Not**
`internal/budget/rules/rules.json.tmpl`, **not** `internal/budget/rules/testdata/rules.golden.json`,
**not** `internal/projectpolicy/version.go`: a part whose claim is "the document does not move" is
only as good as the fact that its diff does not touch one — and if a reviewer finds that a `.validate`
or a `$other` on the node would be *needed* for the fields to be safe, that is a finding against
D222/D225 and the part is replanned, not silently extended.

**Red step.** Four mutations, three of them in the emulator and one in the document. **A client may
read the node** — change the template's `$uid` entry to `.read: "USER"` and the emulator test that a
paired phone cannot read its own entry must fail. **A client may write the node** — the same with `.write`, in the same
scratch copy of the template, and the test that a phone cannot promote itself must fail. **The fields become load-bearing by accident** — make `USER` also require
`child('role').exists()` and the test that a node written before the fields existed still reaches the
mailbox must fail. **The document moved** — add a `.validate` under the node and the package's own
reading of the golden must fail, which is the mutation that proves this part's central claim rather
than a behaviour: `internal/budget/rules/testdata/rules.golden.json` is unchanged by this part, and
the test that says so must be able to fail.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/budget/rules/...`,
with the emulator cases run under the package's own build tag (`firebase`, as
`internal/budget/rules/emulator_pairing_test.go` and its siblings already do); the golden **unchanged
and read**; `go test ./internal/projectpolicy/...`; and the docs pins of `docs/mobile/` unchanged,
because the check register's rows quote generation 3 and this part leaves it where it is.

**Depends on.** **D222, D225**, and P1's field names. It does not depend on P1's code: the names can
be read from P1's entry type, and this plan assumes the recommendation's names.

**Reviewer tier.** **SECURITY-SENSITIVE.** **Independent opus review.** **What the review must
cover:** that the node's read and write predicates are unchanged in the deployed document and that
nothing in the change adds reach; that `USER` still turns on existence alone and that an entry
written before the fields existed is still admitted; that the golden and `RulesVersion` are
**untouched** and that this is correct — the reviewer should read `internal/projectpolicy/version.go`
and the template and say so in their own words; and what a principal holding
`roles/firebasedatabase.admin` can do to an entry (the reconciliation records that this is not a
boundary against such a principal, and the review must confirm the part does not claim otherwise).

**Commit.** `rules: the device node's email and role, and the document that does not move`

---

### P3 — `fugaro pair` writes the entry

**Scope.** D226, D229 and D230, in the CLI: the existing pairing command — Phase 6's
`internal/cli/pair.go`, whose `pair` is add, `pair ls` the listing and `pair revoke` the deletion —
gains the **administered email** and the **role**. The email is **checked at pairing**: the CLI
refuses an unverified address in the shape `internal/cli/init_members.go`'s `userEmail` already
uses (called by `authenticatedUser`), naming the address as it does and never echoing anything
else; the role is **resolved** by P1 before the write (D224); the node is written **only after** the
typed code matches, as the existing pairing already requires; and `pair ls` prints each entry's
address and role and **never a token, a key or a uid's secret material**. Under D230 the **first**
pairing writes the first entry with `role: admin`, because the person running it holds
`roles/firebasedatabase.admin` — and the seed address `fugaro init` records is what admits them
before a second admin exists. `fugaro pair revoke` is unchanged: deleting the node is the
revocation, and it stays one write.

**Files.** `internal/cli/pair.go` (**new**, Phase 6's file) and `internal/cli/root.go` (the one
registration line Phase 6 already owns); `internal/budget/token/device.go` (**new**, Phase 6's);
`internal/cli/init_firebase.go` (the seed address, if D230 is answered as recommended); and their
tests.

**Red step.** Five mutations, against the fakes the Phase 6 task already uses. **An unverified
email is written** — remove the refusal and the test that pairing stops on an unverified address
must fail. **A wrong typed code writes an entry** — move the write before the code check and the
test that nothing is written must fail. **The node carries the unresolved role** — pass the address
through without P1's resolver and the test where a domain entry and an exact entry differ must fail.
**`pair ls` prints a secret** — add the token to the listing and the test that the output carries no
token-shaped string must fail. **An admin can be revoked down to viewer by a non-admin** — assert
the IAM refusal the command already needs, with a message naming the missing role.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/cli/ -run 'Pair'`
and the token package's own tests, with `internal/gcpfake/identitytoolkit.go` as the fake; the
emulator cases P2 added, run against the entries this command writes. **Not verified here:** any
live project — the pairing code path is Phase 6's own owner-run check, and §9 proposes the two this
work adds by name.

**Depends on.** **Phase 6's P6.6 must exist** (`internal/cli/pair.go`, `internal/budget/token/device.go`,
the `root.go` line) — this part edits a file that is not in the tree; **P1** (the resolver);
**P2** (the fields); **D226, D229, D230.**

**Reviewer tier.** **SECURITY-SENSITIVE.** **Independent opus review.** **What the review must
cover:** that the typed-code gate still precedes every write; what the unverified-email refusal
checks and against which response; that the resolved role is written and nothing resolves at read
time; that `pair ls` and every error message print no secret; that `revoke` is still one deletion;
and which IAM role each half requires.

**Commit.** `cli: fugaro pair writes the administered email and the resolved role`

---

### P4 — The app holds no provider session, and no third item

**Scope.** D232, D233 and D234 in `mobile/`: the boundary, not a feature. Three things are
asserted and one is added. **Asserted:** `mobile/package.json` gains **no** Firebase, Google, Apple
or Microsoft auth dependency; `mobile/src/accounts/secret.js`'s `SECRET_ITEMS` is still exactly
`['idToken','macKey']` and `mobile/src/boundary.test.js`'s counted list of files that may write a
credential is unchanged; and no reverse-DNS identity appears in a committed file, which
`mobile/src/app-config.test.js` already holds (D125) and which this part extends to the new files.
**Added:** the first screen's **"ask your admin"** state — the screen the spec's step 5 asks for —
reached from the pairing flow's refusal path (Phase 6's P6.8 seam in
`mobile/src/app/first-screen.js`), shown **beside** the demo entry and never in front of it, so the
sentence `docs/mobile/play-review-access.md` promises Play's reviewers (*"needs no account, sign-in
or network"*) stays true and `mobile/src/demo/safety.test.js`'s requirement on it is untouched. The
QR payload validator that Task P6.8 writes is **extended by a test, not by a field**: a payload
carrying an email, an identity or a credential is refused.

**Files.** `mobile/src/app/first-screen.js`; `mobile/src/pairing/` (**Phase 6's**, P6.8);
`mobile/src/boundary.test.js`; `mobile/src/app-config.test.js`; `mobile/package.json` (read, never
changed); `mobile/src/demo/safety.test.js` (read, never changed).

**Red step.** Four mutations, in the app's runner. **A provider SDK is added** — put any auth
package into `mobile/package.json`'s dependencies and the boundary test must fail, naming it.
**A third secure-store item appears** — add a name to `SECRET_ITEMS` and the test that the item set
is closed must fail. **The first screen gates the demo** — move the sign-in or the pairing control
above the demo entry and the test that the demo is reachable with no account must fail. **A payload
carrying an identity is accepted** — remove the validator's refusal and the test that a payload
with an email is refused must fail.

**Verification.** The app's own runner — **`npm test` in `mobile/`**, which
`mobile/package.json`'s `test` script defines as `node --test "src/**/*.test.js"`. (The plan this
work extends runs that command for its **`shared/`** tasks and calls the app's a *"trivial test"*
beside the boundary test at P3.1; the command itself is the manifest's, not a citation from that
document.) **This container cannot run it** — no Node is installed (reconciliation §5's U7) — so
this part's verification is the app's CI job and the owner's reading, and this plan says so rather
than implying a green run. The Go half of this work touches no `mobile/` file.

**Depends on.** **Phase 6's P6.8 must exist** (`mobile/src/pairing/`, and the seam in
`mobile/src/app/first-screen.js`); **D232, D233, D234.**

**Reviewer tier.** **SECURITY-SENSITIVE.** **Independent opus review.** **What the review must
cover:** that the boundary tests can fail (each mutation above, shown red); that nothing in the new
screen or the validator touches the network, the secure store or a credential; that the demo's
promise is unchanged and still tested; and that the validator's refusal is what the QR carries
rather than what the app would like it to carry.

**Commit.** `mobile: no provider session, no third item, and the ask-your-admin state`

---

### P5 — The declared allowlist, and the publish that seeds the node

**Scope.** D231: the **declared** list is written where the tree already keeps declared state — the
checkout's `fugaro.yaml`, read by `internal/localcfg` and validated by `schemas/fugaro.schema.json`
— and a **publish** writes it into `config/admin/devices`, in the shape
`internal/cli/project_cmd.go`'s `newProjectPolicyPublishCmd` already uses: an **ETag-guarded write**,
gated on the deployed rules marker by `requirePolicyRules`, seeded from the declared list, and
**never re-adding an entry an operator removed** — which is `publishAllowRepos`'s own rule
(*"a later publish never re-adds a name an operator removed"*) applied to people. The publish is
**additive**: it seeds entries that are not there, and it never deletes one, because deletion is
`fugaro pair revoke` and one write of one node is what makes revocation immediate. A person in the
file who is not on the node is **not admitted** — the node is what the rules read (D231), and the
part's own test proves it rather than asserting it. The domain entries live beside the exact ones in
the same declared block, so that a reader sees one list; the node still carries the **resolved**
role for each person, per D224.

**Files.** `internal/cli/project_cmd.go` (the publish's allowlist half); `internal/localcfg/` (the
declared block and its validation); `schemas/fugaro.schema.json`; `internal/mobileauth/` (the entry
type the declaration is parsed into); `docs/gcp-live-checklist.md` **not touched** — the check that
exercises this is proposed by name in §9, and the phase that ships a check writes it.

**Red step.** Four mutations. **A removed entry comes back** — make the publish seed unconditionally
and the test that a revoked person is not re-added must fail. **A published entry admits without the
node** — make the resolver read the declared list rather than the node and the test that a
declared-but-unwritten person is refused must fail. **A publish without the rules marker writes
anyway** — drop the `requirePolicyRules` gate and the test that a marker-less publish refuses must
fail. **A declared domain entry grants a role to an address the file never names** — make the
declaration authoritative for roles and the test that the node's resolved role wins must fail.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/cli/ -run
'ProjectPolicy|Pair'` and `go test ./internal/localcfg/...`; the schema's own test if the package
has one; and the docs pins of `docs/mobile/` and the register unchanged.

**Depends on.** **P1** (the entry and the resolver), **P3** (a writer against which "never re-adds"
is meaningful), **D231**.

**Reviewer tier.** **SECURITY-SENSITIVE.** **Independent opus review.** **What the review must
cover:** that the publish can only **add** entries and that removal stays a single `revoke`; that
the ETag write is guarded and what a lost race does; that the declaration cannot admit anyone the
node does not carry; that the schema refuses a malformed entry rather than defaulting a role; and
that nothing in the file's parsing takes a value from a place a run can write.

**Commit.** `cli: publish the declared allowlist, and never re-add what was revoked`

---

### P6 — The provider path, written down and refused

**Scope.** D221's alternative, discharged as documentation and **not built**: a new
`docs/mobile-auth/provider-sign-in.md` that writes out — with every path and symbol re-verified —
what enabling a provider session in every project would change. It carries the reconciliation's §3
in full: the **configuration delta per project** (the enabled `defaultSupportedIdpConfigs` and
`oauthIdpConfigs`; the per-platform OAuth client registration; the authorised domains; the email
link's action URL and the App Links/Universal Links artifacts keyed by an identity
`mobile/app.json` deliberately does not carry), the **narrower refusal** `refusePublicSignUp` would
have to become (the exact fields admitted and the exact fields still refused), the **attack
surface** (a QR that is a door, the API key that is public by design, the rules as the only fence,
and the per-family denial an emulator must prove), the **per-provider verified-email question**
(Google, Entra ID, Apple's relay), the **loss of immediate revocation** unless the rules keep
reading a node, and the **checks** that would have to pass before a line of it is written. Its
purpose is the one `docs/pending-workflows/` has: the next person who wants this should read a
document, not re-derive a decision.

**Files.** New `docs/mobile-auth/provider-sign-in.md`; and the index and the decisions it is
referenced from (this plan's §3 and the reconciliation's D221) — no code, no rules, no Terraform,
no `.github/workflows/` file.

**Red step.** A documentation part's red step is its citations: **move a cited symbol** (rename
`refusePublicSignUp` in a scratch tree) and the folder's guard from P0 must fail, naming it; **name
a file the tree does not have** and the same check must fail; and **write a decision as if it were
answered** ("the owner has decided") and the guard's verbatim/decision checks must fail, because
this document describes an alternative and must not read as a ruling.

**Verification.** `go test ./internal/cli/ -run TestDocsMobileAuth` (P0's guard, extended to read
this document); a reading of the document against the reconciliation's §3 for every item it must
carry; and no other package, because nothing else is touched.

**Depends on.** **D221**, and nothing else — it can be written before any of P1–P5 and is placed
last only because it cites them.

**Reviewer tier.** **Careful.** **Security-sensitive.** No — it is documentation, and it changes no
behaviour. **But the reviewer reads it as a threat model**: the review must confirm that the
document does not understate the attack surface, that it names what would have to be *proved*
rather than assumed (the default-deny, the per-provider claims, the dynamic-pattern question in
§5's U1), and that it is unmistakably a description of a refused path rather than a plan.

**Commit.** `docs(mobile-auth): the provider path, written out and refused`

---

## 6. The critical path, and the effort

```
P0 ─────────────────────────────────────────────┐
P1 ──┬────────────────────────────── P5
     │                                │
P2 ──┴─► P3 ◄── Phase 6's P6.6        │
              │                       │
P4 ◄── Phase 6's P6.8 (independent of P3)
P6 (independent of all of them; needs only D221)
```

- **The critical path is P1 → P2 → P3 → P5.** P1 is the entry and the rule; P2 is the proof that
  keeping it on the node costs the deployed document nothing; P3 is who writes it; P5 is how it is
  declared. Everything a person can feel on the CLI is on this path.
- **P0 and P1 can start the day the owner says so**, and P6 can too if D221 is answered; P0 and P1
  are deliberately the parts with no database, no credential and no app in them.
- **Effort, in parts:** 7 parts; **5 SECURITY-SENSITIVE** (P1, P2, P3, P4 and P5 — §7 lists each,
  and P4 is one of them because it is the fence around the app's credential boundary: it is the part
  that would notice a provider SDK, a third secure-store item or an identity in a QR payload, and it
  is the part whose own test runner **cannot be run here**, so a reviewer's reading is a larger share
  of its evidence than of any other part's); **3 that wait on Phase 6** (P3 on P6.6, P4 on P6.8, P5 on
  P3 and so on P6.6); **P0 needs no decision at all**, and **P1 needs its decisions answered but
  nothing built**.
- **The first cut a user can feel is P1 + P2 + P3**: an allowlist entry with an email and a role,
  written by a pairing that refuses an unverified address. Nothing in it can admit a person the
  admin did not name, and nothing in it spends a cent — which is the point.

## 7. Security-sensitive parts, and what an independent review must cover

| Part | Why | The independent review must confirm |
| --- | --- | --- |
| **P1** | the resolution rule **is** the authorisation decision | the override order; the normalisation's failure modes; the closed role set and what an absent role resolves to; that no second place resolves a role |
| **P2** | it is the proof that the fields change nothing for a client, on the node that decides who is in | that the deployed document's read and write predicates are unchanged; that `USER` still turns on the node's existence and that an entry written before the fields existed is still admitted; that the golden and `RulesVersion` are untouched, read against `internal/projectpolicy/version.go` and the template rather than taken on the part's word |
| **P3** | it writes the record and it checks a person's email | the typed-code gate precedes every write; what the unverified-email refusal reads; that the resolved role is written; that no output carries a secret; that `revoke` is one deletion; the IAM role each half needs |
| **P4** | it is the fence around the credential boundary on the device, and **its tests could not be run in this container** (`mobile/package.json`'s `"test"` is the app's only runner, and no Node is installed) | that each boundary test can fail (the four mutations of the red step shown red); that nothing in the new screen or the QR validator touches the network, the secure store or a credential; that the demo promise is unchanged and still tested; and that no provider dependency, secure-store item or identity literal enters `mobile/` through this part |
| **P5** | it can grant, and it writes from a file | that it can only add; that removal stays one `revoke`; the ETag guard and a lost race; that the declaration never admits anyone the node does not carry; that a malformed entry is refused rather than defaulted |

An independent review means **a reviewer who did not write the change, on a different model (this
plan asks for Opus), with the diff in front of them and the part's own claim about what it does not
guarantee**. Each of those pull requests carries a paragraph saying what was reviewed and what was
not.

## 8. What each part's tests must do (the rules this plan holds itself to)

1. **Test-first, and mutation-proven.** Every red step above names the bug it is written for; the
   part plants it, sees the test fail, and removes it. A test that cannot be made to fail is not a
   test.
2. **`GOFLAGS=-p=2 go vet ./...`, `gofmt -l .`, and the tests of every package touched**, before the
   part is finished. A rules **change** would add the emulator run under its own tag and the
   **goldens regenerated and read**; **no part here changes the rules document** (P2's whole claim
   is that it does not move), so the golden is read and left alone. A Terraform change would add
   `terraform fmt -check -recursive` and `terraform validate` — **no part here touches Terraform**.
3. **No secret-shaped literal, ever** — in a test, a fixture, a golden or a document. This folder is
   about Firebase configuration, so P0's scan is the mechanical half of this rule and the review is
   the other half.
4. **The rules are generated, not hand-edited**, and this work generates none: if a later part finds
   it needs a `.validate`, a `$other` or a role predicate in `internal/budget/rules/rules.json.tmpl`,
   that is a **generation** with `internal/projectpolicy/version.go`'s `RulesVersion` bumped and the
   goldens regenerated — a decision of its own (reconciliation §F29), not an extension of P2.
5. **Nothing under `.github/workflows/`** — every part's file list is closed, and none of them lists
   a workflow.
6. **Targeted tests only, and docs pins stay green**: every part that touches this folder runs
   `go test ./internal/cli/ -run TestDocsMobileAuth`, and every part that touches `docs/mobile/`'s
   neighbours runs their pins (`go test ./internal/cli/ -run 'TestDocs|TestMailboxPlan'`), because a
   citation that stops resolving is a claim nobody can check.
7. **The app's parts are verified where the app is.** No Node is installed in this container
   (reconciliation §5's U7), so P4's verification is the app's own CI job; a part that cannot run its
   own tests here says so in its pull request body rather than reporting a run it did not make.

## 9. The owner-run live checks, proposed by name and not numbered

The register's own rule is that **the phase that ships the check writes it**, so this plan proposes
its checks **by name** and **takes no number**. The reading, taken against `docs/gcp-live-checklist.md`
and the documents that name numbers in it:

- **The highest written `## Check N` section is 111**, the setup wizard's — and the register's own
  numbering paragraph records what it read at `1d325928`: written sections to **89**, with **90–99
  the credentials service's** and **100–109 the mobile functional spec's**. Of those, **100–104**
  are proposed by that plan's **P6.11** (approved 2026-10-18) and **105–109 reserved and unused**.
  The mobile block is **ten numbers, 80–89**: **81, 82 and 89 are written** and **80 and 83–88** are
  still unwritten and belong to the mobile plan's **Task P4.2** — which is why three of the block's
  ten are in the register, and why `docs/mobile/implementation-plan.md` says *"all ten are run by
  the owner"* of a set only three of which exist as text. **110 is claimed, twice, and written by
  nobody**: `docs/central-orchestrator/README.md` records the two claims — `docs/mobile/design/voice-evaluation-switch.md`
  (P5.3) and the always-on orchestrator design in **PR #632** (§7) — and the register's paragraph
  records the state rather than the claimants (*"110 is claimed by an open pull request"*). **A
  reader must not treat 110 as free**: it is two unmerged claims, and the register's rule is that
  whichever of two open claims merges second renumbers itself.
- **Above 111, every number is named or proposed by a document that has written no row.**
  `docs/mobile/voice-session/README.md` takes **113–118** and attributes **112** to another work —
  `docs/windows-cli/` (*"merged to `main` as `58bd6b05`, PR #757"*, for its own owner-run Windows
  check) — while that work's own reconciliation (F18, D219) records the
  reading behind it: *"the register's next free number was **112**; it is claimed, so this work takes
  **113–118**"*. `docs/console-windows/implementation-plan.md` proposes **119** for the owner's
  manual checklist and claims nothing (its D12(a)).
- **A claim is settled at its merge**, and above 111 not one number is a **written row**: 110 is
  claimed twice and written by nobody, 112 is claimed by a work merged to `main` that this branch
  does not carry, 113–118 are taken by documents that have written no row, and 119 is proposed. **So
  the next free number depends on how they settle, and this plan takes none** — the register's rule
  (the phase that ships a check writes it) applied honestly: this work ships no check in its first
  parts, and the part that does takes whatever number is free at its own merge.

| Proposed check, by name | Part | Kind | What it proves |
| --- | --- | --- | --- |
| **the pairing that refuses an unverified address** | P3 | owner | the refusal fires on a real account whose address is not verified, and nothing is written |
| **the paired phone cannot read its own entry** | P2, P3 | owner | the node is `.read: false` on the deployed rules, and `pair ls` is the only way to see an entry |
| **a revoked pairing stops on the next request** | P3 | owner | D166's property on a real project: `pair revoke`, then a read the phone could make a moment before |
| **an exact entry overrides its domain's role** | P1, P5 | **runner** | the resolver's rule, on the real declaration the owner wrote |
| **a publish never re-adds a revoked entry** | P5 | owner | the rule `publishAllowRepos` already holds, applied to people |
| **a declared entry that is not on the node admits nobody** | P5 | owner | the node is the authority, not the file |
| **the QR carries no identity** | P4 | owner | a scan of a real QR, read as text: the Firebase configuration and the pairing token, nothing else |
| **the first screen with no account** | P4 | owner | Play's promise, on a device: the demo is reachable with no sign-in, and "ask your admin" stands beside it |
| **`fugaro init` still refuses a provider** | P6 | owner | `refusePublicSignUp` fires on a project with a provider enabled, on the real API |
| **the app's boundary on a real build** | P4 | **runner** | the app's CI job, not a live check |

Each check above is **owner-run, on a real project, by the owner** unless it is marked **runner**;
there is no live action of any kind in this work, and no step of any check is run by an agent.

## 10. The numbers this plan would hold

- **Decision numbers: D221–D236**, and they are new. They are taken from **above every written
  number**, the way `docs/mobile/voice-session/README.md` takes its own: the highest **written**
  decision number under `docs/` at `d9dda8f3` is **D220**, the coordinator holds **D193–D197**, and
  **D198–D200** are free but **D200** is recorded by the voice-session work as possibly held by the
  owner's notes. Each decision is written in [reconciliation.md](reconciliation.md) §4,
  **recommendation first**, with the alternative and its cost.
- **Check numbers: none claimed**, for the reason in §9. The part that ships a check takes the
  number free at its own merge, which is the register's rule and not this plan's.
- **No Go code exists in this work yet, and no part of P1–P6 has been written.** The only files this
  work has added are the four documents in this folder.

## 11. The owner approval gate

**No part of this plan starts before the owner passes this gate.** Passing it means, at minimum:

1. **Confirming that the mobile Phase 6 tasks this work extends are the ones that land** — P6.6 and
   P6.8 are approved 2026-10-18 and unbuilt, and **P3, P4 and P5 cannot merge before them**. If
   Phase 6 is reshaped, this plan's parts are reshaped with it.
2. **Answering D221**, because it decides whether the provider half exists at all. The plan is
   written to "no provider session reaches the database", and P6 is the only part that changes if
   the answer is the other way — but **P4, P6 and D232–D234 all move with it**, because the app's
   boundary, its dependency list and its secure-store item set are the same question.
3. **Answering D222, D223, D224 and D225 together**, because they are one record: what the entry
   is, where a domain rule is applied, where the exact-over-domain rule is resolved, and what reads
   a role. Answering one and not the others leaves P1 and P2 without a shape.
4. **Ruling on D231**, which is the one decision that allocates a fact between two stores — the
   node and the project's Git repo. Shipping either silently decides the other, and the two differ
   in exactly the property the reconciliation spends its longest finding on: whether a removal is
   one write.
5. **Naming the release and the branch**: this plan recommends **the first release after the mobile
   Phase 6 tasks — not 0.11.0** (D236), and says that no part assumes a release that has not
   shipped.

**Passed 2026-10-22.** The owner answered **D221–D236, each as recommended** — so §4's clauses 2,
3 and 4 are answered, clause 5's release is the one D236 recommends, and clause 1's confirmation
stands on the Phase 6 tasks being the ones that land — and approved **P0 and P1**, the two parts
the owner approved. **What the gate did not open is as much a part of it**: **P2** waits on
nothing but its own approval, **P3, P4 and P5 wait on P6.6 and P6.8**, and **P6** waits on nothing
but D221 — and a part that lands is still reviewed on its own, with its own reviewer tier.

**What this gate is not.** It is not an approval of any part: approving the plan answers §4's
decisions, and each part is still reviewed on its own — five of them by an independent reviewer on
a different model. It is not a promise about ordering beyond §2's five dependencies. And it is not a
substitute for the register's own rules about numbers: a decision number, once allocated, is never
reused or renumbered, and a check number is taken by the phase that writes it.
