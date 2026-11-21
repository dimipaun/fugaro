# The key-release wire (C1)

Task **C1** of [implementation-plan.md](implementation-plan.md). What it states is **R1** (the shape
of K3: one Cloud KMS asymmetric key, the launcher holds only its public half, the credentials
service releases the seal key to the run's own VM), **R5** (the blob's variable and the plain
marker's one meaning), **R7** (the blob is treated as a secret in output), **R13** (the key fact is
`fugaro update`'s alone), **R15** and **R16** (publishing keeps the two facts; an opt-out is a
written value), **R17** (the service refuses a registration where Batch is off) and **D3108** (the
launcher registers the job it wrapped for), with **D3101** (the VM-attested release), **D3102** as
amended (the service's two reads), **D3109** (the launcher class alone may register), **D3110** and
**D3111** (the opt-in fact and its withdrawal) and **F3** (a Batch job prints its variables) behind
the rules; **U1**, **O1** and **Check 830** are the one fact it names and does not answer
([reconciliation.md](reconciliation.md)).

The Go is `internal/budget/token/wrap_contract.go` (the envelope), `internal/credsvc/sealkey_wire.go`
(the wire), `internal/localcfg/batch_sealkey.go` (the block's two identity facts) and the stub the
contract ships, `internal/credsvc/sealkey_stub.go`.

**BR1** (the launcher wraps and registers), **BR2** (the release route), **BR3** (the link from a VM
to its job), **BR4** (the runner's fetch), **BR7** (doctor and validate), **BR12** (the documents),
**BR14** (the reaper), **BR16** (the update writes the block), **BR17** (the registration's write
path), **BR19** (publishing), **BR21** (retiring the grants), **BR22** (the deploy input) and
**I1** (the wiring) are written against this document. A side that finds it wrong or incomplete
raises a change request ([docs/contract-first-decoupling](../contract-first-decoupling/spec.md))
rather than working around it; the one change already anticipated is
**R-C1-link-permission**, [below](#the-change-anticipated).

## The envelope

A sealed launch puts one blob in the job, as an ordinary variable — `FUGARO_BUDGET_TOKEN_WRAPPED`
(`token.BudgetTokenWrappedEnv`, R5). It is a blob and not a key: what a reader of the job obtains is
a value only the installation's KMS key opens, and the plain seal key is never a field of a Batch
job in any form (F3).

```
version byte || RSA-OAEP(SHA-256, empty label) ciphertext          (257 bytes)
```

and the plaintext, which never appears on the wire beside it, is

```
job digest (32) || wrap time (8, big-endian Unix seconds) || seal key (32)      (72 bytes)
```

- **The digest and the wrap time are inside the ciphertext** and never beside it (R1's first guard).
  They are what the release is held to — the asking VM must be a VM of the job the digest names, and
  that job must have been created within a short bound of the wrap time — so a field beside the blob
  would be a field a reader could edit to point one run's release at another job's VM. The
  encryption carries no authenticity and needs none (R1): the public key is public, anyone may wrap
  anything, and a forged blob holds a key that opens nothing.
- **The job digest is `token.JobDigest`**: SHA-256 of the job's FULL resource name
  (`projects/<p>/locations/<l>/jobs/<id>`), the spelling the platform gives a job and the one the
  link answers with. A job's name and not a run's: the name is what the launcher holds before the
  job exists, while the id Google assigns at creation is D3108's second call. Both sides compute it
  and neither is handed the other's answer.
- **The padding and the hash** are RSA-OAEP with SHA-256 for the message digest and MGF1 and an
  empty label — the one algorithm Cloud KMS names `RSA_DECRYPT_OAEP_2048_SHA256`, which is what
  BR5 creates the key with. The two names are one fact written twice, not a choice either side
  makes.
- **The size bound.** A 2048-bit key gives a 256-byte ciphertext and carries at most 190 bytes of
  OAEP-SHA-256 plaintext, so this envelope is 257 bytes and its 72-byte plaintext sits inside it
  with room to spare. `token.Wrap` refuses a key that is not 2048 bits and a seal key that is not
  `token.KeySize`; `token.ParseWrapped` refuses a blob that is not one 257-byte envelope of this
  version; `token.ParseWrappedPlaintext` refuses a plaintext of any other length rather than reading
  it for the fields it happens to have.
- **The wire spelling** is one unpadded base64url string, strictly decoded — 343 characters —
  the way the seal's own values are spelled. A padded spelling, the standard alphabet, a second
  segment and a first byte that is not this version are all `ErrWrapped`. There is one version byte
  and not two: it covers the plaintext's layout as well as the ciphertext's.

**What never travels.** Not the private half of the key: the launcher encrypts to the public key in
the published block (R13) and the private half is a Cloud KMS key no Fugaro process reads. Not the
plaintext, in any form: the service decrypts, reads the digest and the wrap time, and holds them; the
runner is given the seal key and nothing else. And R7's rule holds on this wire — the blob is treated
as a secret in output although it is not one — so no refusal, error or audit line repeats it, and no
line of this contract's code prints it.

## The route

Three paths, and they are three because this service's rule is one caller class per route
(`internal/credsvc/wire.go`): one job-class route and two launcher-class routes. A path that served
both classes would be a route whose rules depend on which kind of caller arrived.

| Path | Class | What it is |
| --- | --- | --- |
| `/v1/batch/seal-key` (`credsvc.SealKeyPath`) | the job class (`verifyJob`'s own table) | the release: a run's VM sends its identity token and the blob and receives the seal key |
| `/v1/batch/seal-key/register` (`credsvc.SealKeyRegisterPath`) | the launcher class alone (`VerifyLauncher`) | the first registration call, made BEFORE the job is created |
| `/v1/batch/seal-key/job` (`credsvc.SealKeyJobPath`) | the launcher class alone (`VerifyLauncher`) | the second registration call, made after the create returned |

The release route admits the per-repository and the shared project job classes and refuses every
other class (R1's third guard); the two registration routes admit the launcher class and refuse
every other, including the job and review classes (D3108, D3109).

**The release call.** `SealKeyRequest` carries `run` (`<slug>/<run id>`, the one spelling
`FUGARO_RUN` carries), `job` (the calling job's full resource name, the one its own environment
carries) and `wrapped` (the blob). Both claims are *claims*: `run` is what the caller says about
itself and is used for one thing, finding the run's budget record, and `job` decides nothing until
three facts agree with it —

- the digest inside the DECRYPTED blob is `token.JobDigest` of this `job`;
- the registration the launcher wrote for that digest names this `job`, and carries a job id;
- the link (below) answers that this VM is a VM of that job, and the id it reports is the
  registered one.

Nothing else is decided from the request. The caller does not name the KMS key, the installation's
project or the job id: the first two are the service's own configuration and the third is what the
registration and the link answer, and a request that could name any of them would be a caller
choosing what it is proved by. Two guards hold the token's own claims (R1's second guard): its
project must be the installation's, and its zone one of the installation's region.

**Where the two claims come from.** Both are the launcher's own strings, and the tree already has
each one's home, which is why this contract does not mint a third spelling:

| Claim | The variable | The name |
| --- | --- | --- |
| `run` | the job variable the launcher writes, `<slug>/<run id>` | `FUGARO_RUN`, the value `backend.Execution.Run` reads back and `gcp.CheckRunExecution` binds a name to a run by |
| `job` | the job variable carrying the Batch job's own resource name, which Batch injects for nothing and the launcher therefore supplies | `backend.BatchJobEnv` — `FUGARO_BATCH_JOB` |

The launcher-forgeable reading is F24's and is why R1's release rests on the link and the
registration rather than on the claims: a launcher can write any `FUGARO_RUN` into a job of their
own, and the account the service verified, the VM-to-job link and the registered job id are what
such a job cannot reproduce.

**The bound.** The job must have been created within a short bound after the wrap time read out of
the plaintext, tolerance included, so a blob is worthless long after it was wrapped. The bound is the
route's constant (BR2's), and the link is given the wrap time to apply it: a job created outside the
bound is one of the answers `JobLink.VMsJob` reports false for, not an error.

**The answer.** `SealKeyAnswer` carries `key` and nothing else: the seal key in `token.EncodeKey`'s
one spelling, which the runner parses with `token.ParseKey` and registers with its redactor before
anything can print it (BR4). Nothing about the job, the run or the registration comes back — a run
that has the key has what it asked for, and a second fact would be a second thing to believe.

**The refusal.** One typed refusal is this contract's, `credsvc.ErrSealKeyNotReleased`
(`ErrSealKeyNotReleased`), and it is one sentinel for every reason — an unregistered digest, a
registration with no id, a VM of another job, a finished job, a job created outside the bound, a
token whose project or zone is not the installation's, a blob of another format. The run's answer is
the same in each case: it stops and says so. Which reason it was is the audit line's, written once
per refusal after the caller is verified and never quoting the caller's own text (BR2), so a caller
probing the route learns nothing from the differences between refusals. The plain seal key and the
blob appear in no audit line, log line or error, and a refusal names the field and never its
content.

## The registration

A launcher that wraps a blob registers the job it is about to create (D3108). Two calls, both
launcher-class, both answering `RegisterAnswer` — which is empty on purpose, because a launcher that
knows its entry was written has what it needs and nothing about the entry is worth printing in a
launcher's log:

1. **`SealKeyRegisterPath`**, before the create: `RegisterRequest` — `run`, `digest` (the blob's job
   digest in `token.EncodeDigest`'s spelling) and `job` (the name the launcher is about to create).
   The service checks that `job` hashes to `digest` rather than recomputing one from the other, and
   writes the entry with the launcher's VERIFIED identity.
2. **`SealKeyJobPath`**, after the create returned: `RegisterJobRequest` — `run`, `digest` and
   `job_id`. Only the identity that wrote the entry may write its id, and the id is written once.

The release then requires the calling VM's job to carry that id, which is what closes the replay of
a queued job: a job deleted and re-created under the same name has another id and is refused.

**Where the entry lives.** On the run's own budget record, under
`runs/<slug>/<run>/sealkeys/<digest>` (`credsvc.BatchSealKeysNode` — the node name `sealkeys`).
The run's record and not a node of the service's own, because the run's agent can READ the entries —
the run may see which job is registered for its blob — while no token holder may write one: the
database rules give the node `.read` to the run's own identity and `.write` to nobody (BR17). The
fields are `credsvc.Registration`'s and their spellings are the wire's:

| Field | Key | What it is |
| --- | --- | --- |
| `Registration.Job` | `job` | the job's full resource name, as the launcher named it |
| `Registration.Launcher` | `launcher` | the verified launcher identity — the account, never the token it presented |
| `Registration.At` | `at` | when the entry was registered, Unix seconds: the launcher's own clock, recorded and never decided on |
| `Registration.JobID` | `job_id` | the job's unique id, written by the second call, empty until then |

An entry with no id releases nothing. One entry per digest, written once and never overwritten or
removed while the run's record lives; a `--retry` or the relaunch D3106 leads to mints a new key, so
it has a new digest and adds an entry beside the old one, and an entry another launcher adds for a
digest of their own blocks nothing and opens nothing. A launcher that dies between the two calls
leaves a run that stops at release, which is the safe side: a registered digest whose job was never
created releases nothing, because no VM exists to ask for it.

**What the service reads it through.** `credsvc.Registry`:

```go
Entry(ctx context.Context, run string, digest [token.WrappedDigestSize]byte) (Registration, bool, error)
```

Looked up by the run and the digest the service read out of the DECRYPTED blob, so a caller cannot
ask for an entry it did not already hold a blob for. "Nothing is registered" is `false` and not an
error; an unreadable record is the error arm, this package's standing rule. The read is all this
contract's release side has: the write path is BR17's, and the two are apart so that the release
cannot create the entry that admits it.

## The link

The one question the release route cannot answer itself is which job the calling VM belongs to.
`credsvc.JobLink` is the whole of "this VM is this job's":

```go
VMsJob(ctx context.Context, vm VMIdentity, job string, wrappedAt time.Time) (JobVM, bool, error)
```

`VMIdentity` is the calling VM as the route holds it after verification — the account, the token's
project and zone, and the token itself, which the link reads while the call is in flight and which is
never stored or repeated. `JobVM` is the answer: the job, its unique id and when it was created. The
link carries no decision of its own: the bound the creation time is held to is applied where the
wrap time is known, and the ID is compared with the registered one by the route.

`VMsJob` answers **false, and no error**, for a VM that is no Batch job's, for a job that has
finished, and for a job created outside the bound `wrappedAt` sets: those are answers about a VM, not
failures to read one, and the caller turns all of them into `ErrSealKeyNotReleased`. A read that
FAILED is the error arm — an unreadable state is not an answer, this service's standing rule, because
a run refused for the wrong reason is a run nobody can debug. A label the launcher set is not a link
(BR3's own red step).

**Which Google-made fact links a VM to its job, and which permission reads it, are named by Check
830 and not by this contract** (U1, R4, D3102 as amended, O1). This document states the question and
not the answer: `R-C1-link-permission` writes both into `internal/credsvc/sealkey_wire.go` and into
this section once the owner has run the check and answered O1. Until then nothing in this tree may
assume either, and the stub refuses every VM, so a link that does not exist cannot admit one by
accident. The second read is part of the same answer: the service already needs
`batch.jobs.get` conditioned on the Fugaro job-name prefix, and the link's read is the one O1
settles.

## The opener

`credsvc.KeyOpener` is the one place the private half is used, and the private half is never in the
process:

```go
Open(ctx context.Context, ciphertext []byte) ([]byte, error)
```

The implementation (BR2's `kmsopen.go`) sends the ciphertext from `token.ParseWrapped` to Cloud KMS
and hands back the plaintext, which is `token.ParseWrappedPlaintext`'s input and nothing else. A
Fugaro binary that leaks its whole memory therefore leaks no decryption key, and R9 is why the key's
version in the block is the version the blob was wrapped to rather than a name resolved at release
time. `Open`'s error is the service's own unreadable-state refusal and never a verdict about the
caller: a blob KMS refuses and a KMS call that failed are told apart by the implementation, and both
leave the caller with `ErrSealKeyNotReleased`.

## The block's two identity facts

`localcfg.Batch` carries two facts (`internal/localcfg/batch_sealkey.go`). Both are the
installation's and not a machine's preference, both are published with the block so a launcher who
is not an operator receives them (R13, R15), and neither is written by any settings command. The
two differ in whose hand writes them: the OPT-IN is an operator's own — the selection of the
backend writes `in` (R2) and the explicit opt-out writes `withdrawn` (R16) — while the KEY FACT is
`fugaro update`'s alone and by nothing else (R13).

**The opt-in fact** (`batch.opt_in`, `localcfg.Batch.OptIn`) has three states and not two (D3110 as
amended, D3111):

| Value | Meaning | Who writes it |
| --- | --- | --- |
| absent | nobody has opted this installation in: no compute root is applied, and a launch is refused | a block that never carried the fact |
| `in` (`localcfg.BatchOptInOn`) | `fugaro update` applies the compute root, creates the key if it does not exist and records the key fact (R2) | an operator's selection of the backend, published |
| `withdrawn` (`localcfg.BatchOptInWithdrawn`) | a WRITTEN withdrawal: the root is applied with the grants off, the key and its ring stay, and a registration is refused (R16, R17) | an operator's explicit opt-out command (BR20) |

A withdrawal is a value and not an absence, so an update can tell an installation that opted out
from one that never opted in. A fourth spelling is refused by `validateBatch` rather than read as
"absent", because a misspelling that meant a third state would be an installation whose root is
applied for a reason nobody wrote down. `Batch.OptedIn` and `Batch.OptInWithdrawn` answer the
question each caller asks, and `Publishing` keeps these two fields from the published copy whatever
the publishing machine's own block says (R15, BR19).

**The key fact** (`batch.seal_key`, `localcfg.BatchSealKey`) is one fact in two fields, written by
`fugaro update` from the compute root's own outputs (R13):

| Field | What it is |
| --- | --- |
| `version` (`BatchSealKey.Version`) | the Cloud KMS key version's full resource name (`projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>/cryptoKeyVersions/<v>`) |
| `public_key` (`BatchSealKey.PublicKey`) | that version's public key, PEM-encoded SPKI — public by construction, and what a launcher encrypts to |

The VERSION and not the key: a blob wrapped to one version is opened by that version, which is what
makes R9's "an update with a run in flight changes nothing it holds" a fact about the block rather
than a promise about the update. Neither field is a secret and a substituted public key would hand
its holder every seal key wrapped to it, which is why doctor fails a machine whose local fact
differs from the published one (BR7) and why nothing here accepts a fact a settings command wrote.
`localcfg.ParseSealKeyPublicKey` reads the public key — one `PUBLIC KEY` PEM block holding a PKIX
SubjectPublicKeyInfo, RSA, exactly 2048 bits — and refuses anything else with
`localcfg.ErrSealKeyPublicKey`, never echoing the value. A block whose `seal_key` is half written is
refused as one fact in two fields rather than read as a fact with a missing half; a block that names
no key at all is R3's launch refusal, whose fix is `fugaro update`.

**The settings registry lists them as keys no settings command writes** (R13): `batch.opt_in`,
`batch.seal_key.public_key` and `batch.seal_key.version` are rows owned by `update`, the generic
write path refuses them, and they are in the installation-facts table of
[docs/config-management/spec.md](../config-management/spec.md) §6 with no flag.

**The spelling check and not a proof.** These parses hold the two facts to their shapes; whether
this installation's project really holds that key version, and whether its public key is that
version's, are cloud facts read by doctor and by the service, never by a parse. No launch reads the
cloud to decide anything (R3): the backend wraps where the block names the key and refuses with
`fugaro update` where it does not.

## The deploy input

`credsvc.BatchEnabledEnv` is `FUGARO_BATCH_ON`, the credentials service's one deploy input (R17,
D3112). `fugaro update` sets it from the published opt-in fact — `1` where the fact says `in`,
anything else or nothing where it is absent or withdrawn — and the registration is refused where it
is off, so a launch registers first and no job and no VM exist yet. The release route does not read
it: a run in flight keeps what it holds (R9, R16), and a service whose deploy forgot the input is
one that refuses registrations rather than one that admits them. The name is spelled in
`.claude/rules/credentials-service.md` too (BR22).

## The compute root's new names

BR5 creates the key and gives the credentials service account what it needs; BR16 reads the key
fact back out and writes the block. The names they share are these, and both sides spell them
exactly (the module's and the root's, one for one, the way the compute tree already passes its
outputs through):

| Name | Kind | What it is |
| --- | --- | --- |
| `batch_seal_key_version` | output | the KMS key version's full resource name — the value `batch.seal_key.version` records |
| `batch_seal_key_public_key` | output | that version's public key, PEM — the value `batch.seal_key.public_key` records |
| `seal_key_grants` | input | a boolean, `true` by default: `false` plans the key, its ring and the custom role WITHOUT the three grants R16 removes — the service's binding to its role, its decrypt on the key, and what the root was fed for the shared project job accounts — which is what an opt-out applies |

Two more names the compute root gains with them: the key RING's and the key's own ids, which are the
module's own locals and not inputs, because nothing outside the module creates them (the shape the
reaper's invocation names already have, `modules/compute/outputs.tf`); and `credentials_account`, the
credentials service's own service account, which the decrypt on the key is bound to — an input,
because the compute root creates neither that account nor its name.

**The mirror the names have to reach.** `internal/infra.ComputeOutputs` reads the root's
`terraform output -json`, and `TestComputeOutputsCoverTheRoot` holds that struct and the root's
outputs to each other one for one, so the two outputs above need their fields in
`internal/infra/compute.go` in the same change (BR16's file) — a name added to the root alone is a
name the stage never sees. The `.claude/rules/terraform.md` rules apply to the whole change: the
cover tables are a tripwire and not a fence, so a new resource type or principal kind is added on
purpose with the reason in the pull request (D1545).

## The stub, and what I1 deletes

The contract arrives before its implementations, so `internal/credsvc/sealkey_wire.go` declares the
three sides as package variables — `SealKeyJobLink`, `SealKeyOpener`, `SealKeyRegistry` — and
`internal/credsvc/sealkey_stub.go` answers every call the way that can never release a key: no VM is
a VM of any job, no ciphertext opens (`ErrUnavailable`), no digest is registered. The stubs decide
nothing — they read no token, no KMS key, no record and no cloud — and each is installed by a
guarded `init` that stands only while nothing else has assigned one, because the files that fill
them sort before it in name order.

**I1 deletes `sealkey_stub.go` AND `sealkey_stub_test.go`**, and replaces them with the real sides.
Every test that is about the wire's rules rather than about the stub lives in
`sealkey_wire_test.go`, which I1 does not touch. This is the shape `provider_wire_stub.go` already
has.

## The change anticipated

**R-C1-link-permission** (the plan's §7 change, level 1, after BR0 and before BR3, BR5 and BR12):
when the owner has run Check 830 and answered O1, it writes two things — the Google-made fact that
links a VM to its job, as BR3 must implement it, and the permission that reads it, as the credentials
service account's custom role must carry it — into `internal/credsvc/sealkey_wire.go` (the
`JobLink` section) and into this document. No side may write a link or a grant before it: a guess at
the fact is a link a second launcher can forge, and a guess at the permission is a role that either
cannot read or reads too much.
