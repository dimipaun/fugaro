# The claim's attestation and the registration (C1)

Task **C1** of [implementation-plan.md](implementation-plan.md), re-cut by LM19 of
[launcher-does-not-mint](../launcher-does-not-mint/README.md). What it states is **R1** (the shape
of K4: the ticket asks for a run's identity while NAMING the Batch job the launcher is about to
create, the run claims with its VM's identity token, and the service serves the identity only to a
VM of that job), **R18** (the attestation is the run's own, and a record that names a job is
refused without one), **R19** (the issuance record's two writes: the job with the ticket, the job's
id by the registration) and **R20** (a roster service account's run attests exactly as a person's
does), with **D3108** (the launcher registers the job it wrapped for, and a re-created job is
refused by its id), **D3114** (a record that names a job and carries no attestation is refused),
**D3123** (the caller classes) and **D3102** as amended (the service's reads) behind the rules.
**U1**, **O1** and **Check 830** are the one fact it names and does not answer: which Google-made
fact links a VM to its job, and the read that answers it, are the owner's measurement
([reconciliation.md](reconciliation.md)).

The Go is `internal/identitywire/batchclaim.go` (the attestation's shape, the registration route
and its bodies, the job's name and the `VMIdentity` interface), `internal/identitywire/wire.go`
(the two fields the two bodies gain, the two the record gains, and the route in the table
`RouteFor` reads), `internal/credsvc/identity_config.go` (the link's field on the service's
identity configuration, and the rule D3114 states) and the one line of
`internal/credsvc/identity_ticket.go` that writes the ask's job onto the issuance record — R19's
first write, which is the ticket route's because the ticket route is what writes that record.

**This contract does not touch the merged key-release wire.** `internal/credsvc/sealkey_wire.go`
and [contract-key-release.md](contract-key-release.md) are [BR23](implementation-plan.md)'s to
retire, and nothing here reads them. Two of the shapes that wire carries are declared here as well —
the job's name (`JobName`, the same rule as the release wire's `validBatchJobNameRE`) and the run
reference's splitting (`checkRunRef`, which the release wire declares too) — so that the launcher,
the record and the registration are held to a rule of this contract's own rather than to one the
file BR23 retires carries. The two copies the release wire holds are BR23's to remove with that
file, and until then the rule is spelled in both.

**BR1** (the launcher), **BR2** (the claim's attestation and the route that serves it), **BR3**
(the link from a VM to its job), **BR4** (the runner's claim, merged), **BR12** (the documents),
**BR13** (Check 831), **BR17** (the registration's route and its mount), **BR22** (the deploy
input) and **I1** (the wiring) are written against this document. A side that finds it wrong or
incomplete raises a change request
([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than working
around it.

## The two fields the wire gains

The claim's wire is the merged C1 of the launcher plan (`internal/identitywire/wire.go`, its
`contract.md`), and this plan adds exactly four fields to it: two on the bodies, two on the record.

| Body | Field | Written by | Empty means |
| --- | --- | --- | --- |
| `TicketRequest` | `job` (`JobName`) | the launcher, in its ask | a Cloud Run launch: the ask is the release behind's, byte for byte |
| `ClaimRequest` | `attestation` (`Attestation`) | the run, in its claim | a Cloud Run launch: the claim is the release behind's, byte for byte |
| `Record` | `job` (`JobName`) | the ticket route, with the ticket (R19), and again by a re-ask before the claim (R6) | a Cloud Run run's record, or one whose launcher last asked without a job |
| `Record` | `job_id` (`string`) | the registration, once (R19) | a record that names no job, or one whose job is not registered yet |

- **Both body fields are `omitempty`**, and that is a fact about a bridge and not a tidiness: a
  Cloud Run launch of this release sends the bodies of the release behind, and a runner of this
  release that sends an attestation to a service of the release behind is refused **by name** — the
  strict decode (`DecodeStrict`) names the field it does not know, which is the second trigger the
  CLI and the runner read (F37, R7).
- **The job's name is the platform's own**: `projects/<project>/locations/<location>/jobs/<job>`,
  `[a-z0-9-]` in every part (`JobName.Valid`). The ticket ask is held to that shape, and an ask that
  names another is refused before the launcher has created anything to name — a name of another
  shape can never be served, because the registration writes an id against it and the claim is
  served only to a VM of it (D3114). Whether the project holds such a job is a read and never a
  parse.
- **The attestation is the VM's identity token, whole, in the metadata server's full format**
  (U2). The full format is the point of the read: its claims carry the instance's id, name, project
  and zone, which is the fact the link is made of. It is a credential of the VM and is treated as a
  secret everywhere (R7): it reaches no log line, no error, no audit line and no record — `Record`
  keeps no copy of it, exactly as it keeps no copy of the ticket or the nonce.
- **The record's `job` is written with the ticket, and it is not immutable while the ticket is
  unclaimed**: the record's own caller may ask again before the claim, and that ask replaces the
  ticket **and the job together**, in the sweep's one write — R6's
  `TestTicketReplacedBeforeClaim` pins the ticket and the same write carries the job, so the field
  always holds the **last** ask's word. It is the launcher's own word about the launch it is
  making, and a second job under the same name is a different launch with a different run id; a
  launcher that asked for a job and then re-asked without one has made its **own** run a Cloud Run
  claim, and only that caller could have — the ticket is re-issued to no other identity (R5), and
  the field is the ticket route's write alone and never the claim's. **What fixes it is the claim**:
  a claimed ticket is never re-issued (R4), so no ask is served after the claim and the field the
  claim route reads is the one the last unclaimed ask wrote. **The record's `job_id` is written by
  the registration** and only once, and only before the claim (BR17), so the id cannot move it
  either; a record that names a job and carries no id yet is a launcher that died between its two
  calls, and the run stops at its claim, which is the safe side (R1).
- **What does NOT change.** No field of the claim names a uid, a claim set or the signer; no field
  the service signs is read from a caller. The record's `job` and `job_id` are read back by this
  service and by the record's own rules, and no other reader outside the binary consumes them —
  they are added to a stored value and neither retyped nor renamed (a field never changes meaning).

## The rule the record's job turns on (D3114)

A record that names a job is a **Batch launch**, and a Batch launch is served only to a VM of that
job — which the account check alone cannot decide, because every job one launcher creates runs as
the same job service account (F4). The field turns on what the record holds **at the claim**, and
what it holds then is the last ask of the caller on the record (R6, above): the claim itself can
neither set it nor clear it. The service's rule follows, and it is asked **before anything of the
installation is read** (`credsvc.CheckClaimAttestation`):

- a record that names a job, claimed with **no** attestation, is refused (`ErrClaimAttestation`);
- a record that names a job, claimed with something that is **not shaped like** an identity token
  (three dot-separated base64url segments, `Attestation.WellFormed`) is refused the same way. It is
  a spelling check and not a proof: which VM the token names and which job that VM belongs to are
  the link's;
- a record that names **no** job is a Cloud Run run's: it passes, and the account check is the
  whole of what serves it. The rule is one-directional on purpose — D3114's danger is the
  OMISSION, a claim that leaves the field out and falls back to the account check on a record that
  names a job, and an attestation with no job to bind it to decides nothing either way.

The refusal is the claim route's to serve, with **one audit line** written after the caller is
verified, naming the field and never its content, and the run halts with the claim's own reason
(BR2). The rule function is in this contract so that the route cannot sign without it.

## The registration: `POST /v1/identity/job`

After the create returns, the launcher tells the service the job's Google-assigned id (R19). The
route is `identitywire.JobPath`, its body is `JobRequest` and its answer is `JobResponse` (empty:
a launcher that is told the entry was written has what it needs).

- **Who may call it**: the classes the ticket ask admits — a person, a roster service account and
  the resident orchestrator. The registration is the ticket ask's second half, and a roster service
  account is a launcher (D3123, R20). The route's classes are one half of the fence; the other half
  is R19's, and it is the record's: **only the identity that asked for the ticket may write its
  record's id**, which is a fact of the record's caller and not of the class table.
- **The body**: `slug` and `run` (the record's two names, in `TicketRequest`'s spelling and not as
  one `<slug>/<run id>` ref), `job` (the full resource name, as the ask named it and as the
  launcher created it) and `job_id` (the platform's id, opaque to this service).
- **The refusals** (`identity_refused`): a `slug`/`run` pair the record's own spelling could not
  carry (`JobRequest.Record`, which is `checkRunRef` and refuses rather than escapes); a `job` that
  is not the job the record names; an id written by another identity, or written twice; an
  unregistered run (`ErrNoRecord`); and — where the installation has not opted in — the refusal
  R17 states, below. Each is one audit line, as everywhere in this service.
- **The mount is not this contract's.** C1 puts the route in the **table** (`identitywire.routes`),
  so `RouteFor` names it and no reader of the wire respells the path. Admitting the path at the
  service's door — `internal/credsvc/server.go`'s `servedRoute`, and the answer a caller gets there
  before the link is wired — is **BR17**'s, and `internal/credsvc/identity_stub.go` changes not one
  line here: until BR17 mounts it, a caller that reaches `/v1/identity/job` gets the unknown-path
  answer at the door and no refusal of this contract's.

## The question the service cannot answer itself: `VMIdentity`

`identitywire.VMIdentity` is the link from a claiming VM to a Batch job (BR3), and it is the whole
of "this VM is this job's":

```go
VMsJob(ctx context.Context, attestation Attestation, job JobName, jobID string, recordedAt time.Time) (bool, error)
```

- It answers **whether the VM that presented the attestation is a VM of `job`**, that the job it
  read carries `jobID`, and that the job is live (exists, has not finished) and was created within
  the bound of `recordedAt`. A VM of another job, a finished job, a job created outside the bound
  and a job whose id is not the registered one are **false, with no error**: those are answers
  about a caller, and the route answers each its own refusal. **A read that failed is the error
  arm**, and an unreadable state is not an answer — a run refused for the wrong reason is a run
  nobody can debug.
- **What the link's implementation reads is named by Check 830 and not guessed here** (U1, O1,
  D3102 as amended). The permission that read needs, and the shape of its answer, are the owner's
  measurement; nothing in this tree may assume either.
- **The bound is stated, once, in this contract**: `ClaimBound` (15 minutes) is the longest a
  launcher may take between the ticket ask and the create, and `ClaimClockTolerance` (2 minutes) is
  the slack either side of it — the record's time is this service's clock and the creation time is
  the platform's, and two clocks that agree to the second is a promise neither makes. The window is
  `[recordedAt − tolerance, recordedAt + bound + tolerance]`, and it is narrower than the ticket's
  own life, which is what keeps the ticket from being the bound.
- **The field that holds it is `IdentityConfig.VMIdentity`**, and a **nil** link is an installation
  whose link is not wired: a claim on a record that names a job is then answered the contract's
  `not_served`, because a VM nothing proved is no VM of any job and a claim the service cannot
  judge must not be served on the account check alone (D3114). Nothing falls back.
- It is declared in `internal/identitywire` and not beside the field, because `internal/credsvc`
  already declares a `VMIdentity` of its own — the release wire's struct of the caller's own facts
  — and one name in one package is one thing. The service's field is typed by it.

## The classes, and what each caller is

**The claim's classes do not change** (D3123, R20). The claim is the run's own job service account's
call and nobody else's, whatever launched the run: a run launched by a person, by a roster service
account or by the resident orchestrator claims identically, and the attestation it carries is its
own VM's. The launcher's class is on the issuance record (`Record.Class`) and is never read at the
claim. R20 is the reason it can be: the classes differ in what they may be ISSUED, and not in what
the run they launched must prove.

## The opt-in fact and the deploy input (D3110, D3111, D3112, R17)

A registration is refused where this installation has not opted in, so that no job and no VM exist
yet: the refusal is the launcher's, before anything is created, and it names opting in and
`fugaro update` as the fix. Two spellings are already in the tree and this contract adds neither:
the deploy input is `FUGARO_BATCH_ON` (`credsvc.BatchEnabledEnv`, on for `"1"` and off for anything
else or nothing, so a service whose deploy forgot the input refuses registrations rather than
admitting them), and the fact behind it is the published `batch:` block's opt-in, which has **three
states** and not two — absent (nobody opted in; what a block written on a machine that never chose
Batch says), `in`, and `withdrawn` (a written value, so that an update can tell an installation
that opted out from one that never opted in). They are declared where they already are
(`internal/localcfg/batch_sealkey.go`, `internal/credsvc/sealkey_wire.go`, both of them the release
wire's and both of them BR23's to retire), and the route that reads them is BR17's, with BR22
setting the input at deploy.

## What each side may rely on

- **The launcher** (BR1, BR17): its ticket ask may name the job it is about to create, in the
  platform's spelling; the identity the service issues is then for a run on that job and for no
  other, and its claim will need the attestation. It registers the id after the create, before the
  run claims, and it may rely on the route refusing an id it did not ask for the ticket for. A
  re-ask before the claim is its own last word — the job goes with the ticket it replaces — so a
  launcher that means its run to be attested names the job in the ask the record is left holding.
- **The run** (BR4): on Batch it claims with its VM's identity token in the full format; on Cloud
  Run it claims with the ticket and the nonce alone, exactly as before. It never names a job at the
  claim, and it never learns anything about the link's answer beyond served or refused.
- **The service** (LM1's ticket route): it records the ask's job with the ticket and refuses a job
  of another shape. It reads the record's job — as the record holds it when the claim arrives, which
  is the last unclaimed ask's — to decide whether a claim needs an attestation, and it asks the link
  before signing. It stores no attestation and no ticket.
- **The link** (BR3): it is asked one question about one VM and one job, it answers false for every
  VM that is not that job's, and it answers an error only when the read failed. It is told the
  bound and the tolerance; it is told nothing about which Google-made fact it must read, because
  Check 830 names that.
