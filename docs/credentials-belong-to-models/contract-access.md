# The access contract (C1)

Task **C1** of [implementation-plan.md](implementation-plan.md). It states **R1** (one question every
reader of the retired auth mode is moved onto), **R3** (the retired keys and the rule that reads
them), **R4** (a new record under a new name), **R5** (a new header beside the old), **R6** (the
registry entry's new member) and **R15** (an older reader reads what it did, the launcher's entry
included), with **D3060** (a repository has no auth mode), **D3061** as amended (an unset access is
the first credential that reaches, Vertex only where it is named), **D3062** (a file's `access` is
moved as written), **D3064** as amended (the cascade becomes a marked pool), **D3068** (a stage's
account is its craft's access), **D3069** (`--token` names a subscription credential), **D3070** (a
run shows what it goes out on) and **D3072** (release 2 rejects the keys) behind the rules; **F9**,
**F20**, **F35**, **F38**, **F43**, **F44**, **F47** and **F49** are the findings it answers, and
**F26**/**F33** are why two names are declared twice
([reconciliation.md](reconciliation.md)).

The Go is `internal/craft/access.go` (the question and the answer), `internal/craft/legacy.go` (the
migration's rule), `internal/craft/access_stub.go` and `internal/runner/access_stub.go` (the two
stubs), `internal/creds/repoaccess.go` (the record), `internal/credsvc/access_wire.go` (the wire) and
`internal/gateway/accesswire.go` (the gateway's mirror); the names every side shares are
`internal/config/access_names.go`.

**CM1** (the resolver, by reach) and **CM4** (the migration) work out the access; **CM2** (a
repository's crafts), **CM5** (the record), **CM6** (the service), **CM7** (the gateway), **CM8**
(the run's model path), **CM9** (the agent's environment), **CM10** (the record and the cost),
**CM11** (the account rules), **CM12** (the registry and the views), **CM13** (the cloud side),
**CM14** (the launch) and **CM15** (the gates and the bundle) are written against it; **I1** deletes
the two stubs and wires the real sides. A side that finds this contract wrong or incomplete raises a
change request ([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than
working around it.

## (a) The question, and the answer

One question replaces every reader of `agent.auth`: **for this stage — and for the task's grade where
it names one — what do the calls go out on?** It is `craft.Resolver`:

```go
type Resolver interface {
	AccessFor(stage, grade string) (Access, bool)
}
```

`Access` is a credential or a pool and nothing else, and it says the five things a caller needs:

| Member | What it is | Why the caller needs it |
| --- | --- | --- |
| `Kind` (`AccessCredential`, `AccessPool`) | which of the two it is | the record's refs, the pool path and the walk are chosen on it |
| `Name` | the name the config, the record and the wire all speak | it is what the record names (CM6) and what a line prints |
| `Members` (`config.PoolMember`: name, type, path) | the credential itself for a credential, the pool's members in the pool's own order for a pool | the type decides the path (`config.CredentialServingPath`), and the order is what failover walks |
| `Chosen` | whether the credential was chosen, or is the project's default account (F35) | it is one of the two conditions the account walk has (CM11) |
| `Cascade` | whether a pool carries the cascade's marker (D3064 as amended) | a subscription member of it is served with the spread switch off (CM36) |

A member of a type nothing serves carries an **empty** `Path` rather than being dropped: a caller
that has to refuse it has the name to refuse it with. `ok=false` is the contract's "nothing here
answers" — a stage this vocabulary has no craft for, or a file that writes none of the retired keys.
It is **never a default access**, and in particular never Vertex (F49).

The types are config's own four (A2) and no fifth vocabulary is invented: what a name *is* is
answered by which of a layer's two blocks defines it, exactly as `craft.Resolve` answers it today.

## (b) How a run learns its access

A new launcher resolves each craft's and each grade's access and puts the answer in the execution's
environment under **`config.EnvAccess`** (`FUGARO_ACCESS`, R15 (3)). The entry is one JSON document,
`craft.Resolved` (`craft.ResolvedVersion`, `craft.Encode` / `craft.ParseResolved`):

```json
{"version":1,"crafts":{"coder":{"kind":"pool","name":"cascade","members":[{"name":"alice","type":"claude-oauth","path":"credsvc"}],"chosen":true,"cascade":true}},"grades":{}}
```

Every value in it is a name or a type and never a credential's value. The document decodes strictly
and its version is a member: an entry written by another release is refused rather than half read,
because a document half understood is one that could send a stage's calls to something the launcher
never resolved. It is a plain `FUGARO_`-prefixed variable, like the fact in (h), and not a secret.

| A run's state | What it does |
| --- | --- |
| an entry is present and readable | it uses it: its craft's answer, and the grade's where its task names a grade and the document holds one — a grade the document does not hold falls back to the craft's, and a craft the document does not hold is a refusal, never a fallback |
| no entry (an older launcher, or a repository no update has migrated) | it applies the migration's rule of (c) to its own file, in memory, and runs as it did (R3) |
| no entry **and** no retired key in its file | it is refused with the fix — `craft.LegacyNoAccessRefusal`, which names `fugaro update` and the craft's own `access` — and is **never sent to Vertex** (F49) |

The two halves of the first row are deliberately not the same rule. A **craft** the record does not
carry is the migration's "nothing here answers" and a refusal; a **grade** the document does not carry
is not, because a document holds a grade only where the project wrote `model_grades.<grade>.access`
(F38) — the grade is a rung that may simply not be raised, and refusing on its absence would refuse
every graded task of a project that names no grade's access, while the craft's entry is the answer
every call has. The run's side reads it that way today (`internal/runner/access_stub.go`), and CM8's
accessor and CM14's launcher are written to the same reading.

The refusal is the sentence an operator acts on; it names Vertex only as the thing the run is not
sent to. `internal/runner/access_stub.go` is the run's half of that table until CM8 wires the real
accessor; a handed document this release cannot read is an **error** and not a fall back to the rule.

## (c) The migration's rule, as a function

`craft.LegacyAccess(in LegacyInput, stage, grade)` is a real function over the config's own types —
no file, no environment, no database — and it is the contract's because three sides must give one
answer about the retired keys: the migration, which moves them into the new vocabulary (CM4), the
launch, which decides a job, an account and whether the job hears about the credentials service from
the access (CM14), and the runner, which reads its file when nothing was handed to it (CM8).

It is asked in the order a reader of today's configuration gets, each step narrower than the next:

| The retired key | The access it means | Chosen? |
| --- | --- | --- |
| `model_grades.<grade>.access` (the task's grade, F38) | the access it writes, as written (D3062), for the **coder** and the **merge judge** alone | yes |
| `agent.crafts.<craft>.access` | the access it writes, as written (D3062) | yes |
| `agent.auth: oauth` + a stage's account | the account's credential (`claude-oauth`, served by the service) | yes |
| `agent.auth: oauth` + `agent.oauth_token` | the file's account, the same way | yes |
| `agent.auth: oauth` + the project's `default_claude_account` | the project's default account, the same way | yes |
| `agent.auth: oauth`, no account anywhere | the unsuffixed default account (`config.DefaultClaudeAccount`) | **no** (F35) |
| `agent.auth: api-key` + a **mix** stage's account | that account (the stage's own key, else the project's `default_claude_account` — the mix's account, D1848) | yes |
| `agent.auth: api-key` | the project's own Anthropic key, `config.CredentialAnthropicKey` | yes |
| `agent.auth: cascade` | the pool the migration wrote, `config.PoolCascade`, **marked** (D3064 as amended) | yes |
| `agent.auth: vertex` (the old default) | the built-in credential `config.CredentialVertex` | yes |
| none of them | **nothing** — `ok=false`, never Vertex (F49) | — |

An account *is* a credential of type `claude-oauth` in the new vocabulary (R7's first clause), so a
stage account and `agent.oauth_token` name one and the rule answers it as one; an account whose name
no layer declares is answered anyway, because the account keys name an ACCOUNT whose token exists
whether or not the credentials block declares it, and such a repository runs on that token today. A
name a retired `access` key wrote is different: a name no layer holds is a problem the config reports
at the key that wrote it, and the rule answers nothing rather than inventing a type.

Five more rules the table depends on:

- A stage account is a **stage's** key, and the caller hands the two account sources in as two fields:
  `LegacyInput.StageAccounts` holds `stages.<stage>.claude_account` alone, and
  `LegacyInput.DefaultAccount` holds `default_claude_account`. Folding the project default into
  `StageAccounts` would make it beat the file's `agent.oauth_token`, which the walk reads above it
  (§11.2's rungs 3 and 4). A stage the project wrote nothing for runs on what the rungs below it name,
  exactly as it does today — the rule does not move a stage's runs onto another credential. A craft
  whose stages name different accounts is a craft the migration cannot write one access for, and it
  reports it (CM4) rather than this rule choosing for it.
- The **mix** is the one place an api-key run goes out on a subscription: the review, adversarial and
  triage stages may run on the account the project names (`config.MixedAccountStages`, D1848, D1851),
  and every other stage keeps the project's key. The stage's own key is served as written; the mix's
  account beyond the config's own keys — the review model, the coder's provider route, the budget
  mode, the allow-list — is the launch's to judge (CM14), never this rule's.
- The **crafts block the caller hands is the run's own**: `LegacyInput.Accesses` is the run's
  `agent.crafts` with every layer's answer already resolved into it and with `applyGrade` already
  having written the task's grade where it belongs, and the rule reads inheritance **inside that one
  block** (`config.Crafts.Inherited`). That chain is the run's own — the first-line review and the
  merge judge follow the coder, the security review the senior review (`craft.Resolve`,
  `config.CraftParent`) — so a repository whose coder's craft names a pool serves its first-line
  review on that pool today and the rule answers the same. A block lifted from a single layer, or one
  `applyGrade` has not written, would answer a craft that follows another one an access the run never
  uses.
- A grade's access is read for the **coder's craft and the merge judge's**, and for no other craft
  outright: those are the two `applyGrade` writes it into today (`Crafts.Coder` and
  `Crafts.MergeJudge`, `internal/runner/routing.go`). A craft that follows the coder and sets no access
  of its own — the first-line review — is still served it, because the block the caller hands carries
  the written grade and inheritance inside that block reads it, exactly as the run's own resolution
  does. Reading the grade for every craft outright would take a craft that has an access of its own — a
  senior review on a pool of its own — onto the coder's credential, which is the move a migration may
  not make (R3).
- `Chosen` is F35's second condition and nothing else: it is false **only** for the unsuffixed default
  account reached without a choice. Vertex, the project's key, a **mix** account a project named and
  the cascade's pool have no walk to be walked on and are chosen.

## (d) The record: `creds/repoaccess/<slug>`

A new record under a new name, with its own version (`creds.RepoAccess`, `creds.RepoAccessVersion`,
`creds.PathRepoAccess`, R4). The repository auth record changed subject (F5), so by the rule that a
field never changes meaning this is a new record: `creds/repoauth/<slug>` goes on saying what it says
— an older image's calls are still narrowed by it — and this one is what a call that declares its
craft is served by (CM6). Through release 1 `fugaro update` writes both; release 2 stops writing the
old one and deletes it.

| Member | What it holds |
| --- | --- |
| `version` | the format's version (1). A reader that meets another one refuses the node rather than reading the members it recognises as the whole of it |
| `crafts` | per craft, the access the project wrote — or, where it wrote none, **one credential per model family** (F43, D3061 as amended): the first the project declares and admits for that repository that reaches a Claude model, and the first for each provider. A craft the record does not carry is one whose calls are refused (CM6) |
| `grades` | per grade, what a task that names it may be served by (F38). A grade is the project's alone: a repository's file may not write one |
| `tokens` | the subscription credentials a run of this repository may name with `--token` (D3069), `config.DefaultClaudeAccount` for the unsuffixed Claude subscription. Sorted, once each |

Each entry is a `creds.AccessRef` — a `name`, whether that name is a `pool`, and whether a pool
carries the `cascade` marker — and never a value (G18): a credential and a pool live in one
namespace, and the marker is what lets a subscription be served with the spread switch off (CM36).

The record names what a repository's runs **may** be served by and never what one run resolved to
(R4). A call declares its craft and is served what the record names for it, so **a run's whole reach
is the union over its crafts**, which is `RepoAccess.Reach()`; `AccessForCraft`, `AccessForGrade` and
`NamesToken` are the three reads a caller makes, and `ReadRepoAccess` / `PutRepoAccess` (and
`RTDBStore.RepoAccess`) the two I/O sides. The write is whole and not a merge — an access the project
dropped has to go — and is conditional on the value read, so a concurrent update leaves one answer
rather than a torn one.

The record lives under `creds/`, the subtree the database rules deny every run identity (O1, D136),
and two callers reach it: the operator's CLI, which writes it (CM5), and the service, which decides
from it (CM6).

## (e) The wire

A new runner declares three things, each in a header of its own, beside the auth header and never in
it (R5, F44):

| Header | The value, and its validation | What the service does with it |
| --- | --- | --- |
| `X-Fugaro-Access-Craft` | one of `config.CraftNames` (`credsvc.ValidAccessCraft`) | it is the key the record is read by (CM6) |
| `X-Fugaro-Access-Grade` | one of `config.GradeNames` (`credsvc.ValidAccessGrade`) | a task that names a grade is served the grade's access where the record holds one, and its craft's otherwise (the row above) |
| `X-Fugaro-Access-Chosen` | a credential's name as this wire spells one, or `config.DefaultClaudeAccount` (`credsvc.ValidAccessChosen`) | the one thing the record cannot answer from the craft alone: which of the names it allows is being spent, for `--token` and the walk |

**Why new names and not the old one.** A call that carries `X-Fugaro-Auth-Mode` is an older image's
and is narrowed by the OLD record, exactly as today; a call that carries one of these is a new
runner's and is narrowed by the access record. A new runner's statement written into the old header
would be read as an older image's and answered out of a record that says nothing about crafts. A
call with no access header at all is an older image's too, and where an access record exists it is
narrowed to nothing that record does not name (F30, F42 — CM6).

These are the caller's own words, as the auth header is: what they buy is nothing on their own — the
service resolves them against the record and serves no more than the record names — and what they
must be is **decidable**, which is what the validation in `internal/credsvc/access_wire.go` is for. A
craft this run does not have, a grade this project does not have or a name no credential could carry
is refused at the door rather than compared against a record and found unequal, which would send an
operator looking in the wrong document.

`X-Fugaro-Access-Chosen` is a name and never a value, and it is **not** `X-Fugaro-Credential`: that
one is the service's ANSWER, naming the credential that answered, and a field never changes meaning.

**The gateway's mirror.** `internal/gateway` cannot import `internal/credsvc`: the service imports
the gateway, so the dependency cannot run the other way (F26). The three names are therefore declared
twice — `internal/credsvc/access_wire.go`, which is the contract, and
`internal/gateway/accesswire.go`, which is a copy — and `TestTheGatewayAndTheServiceSpellTheHeaderAlike`
holds them to each other: the values are equal, and none of them is a name the wire already carries
(`TestTheAccessHeaderIsNotTheAuthHeader`). The gateway **sends** the declaration and never decides
from it: what a call may be served by is the record, read by the service.

## (f) The registry entry's new member

A new run's registry entry carries no `auth` and a new member, `config.RegistryAccessMember`
(`"access"`, R6): the credentials and pools the run's crafts resolve to, sorted and without repeats —
what `watch` and the console read where they read a repository's auth mode before (CM12). `credential`
keeps the meaning it has, the one credential being spent on right now, and `auth` is left out of a
new entry and never reused: a field never changes meaning. The database rules gain the member,
deployed by `update` as the rules are, and a runner that meets rules without it drops the member and
writes the rest, as it does for `recipe`.

## (g) A repository's crafts in the project's config

The installation's config gains a per-repository craft block, keyed **`config.RepoCraftsKey`**
(`"crafts"`, F9): the same block the project policy node publishes at the project level, holding one
craft's model and access each, and a repository's block may only select among credentials and pools
the project holds (CM2).

It is published in a **node of its own**, `config.RepoCraftsNodePrefix`
(`config/policy/repocrafts/`, one child per repository slug), **beside** the per-repository policy
node (`projectpolicy`'s `config/policy/repos/`) and not inside it (F47, R15 (2)). The node is its own
because the per-repository node is decoded strictly by every CLI of the release behind — a teammate's
older CLI would refuse every policy publish the moment it gained a member, and the node has no
version to refuse cleanly on (`tightenAdmissions` decodes every child). A node no older reader reads
can gain its members without gating anything. Its children carry `version`
(`config.RepoCraftsNodeVersion`, 1) beside their crafts for the same reason the access record does.
The full path is built by the package that owns the database key's escaping rule — `projectpolicy`'s
`seg`, which the publisher and every reader of the tree go through (CM2's `PathRepoCrafts`) — so
`internal/config` states the family and adds no third escaper. The rules that cover the policy tree
gain the new node, deployed by `update`.

## (h) The one recorded fact

`config.CredentialsServedEnv` (`FUGARO_CREDENTIALS_SERVED`, F20): whether the bundle holds every
credential a repository's crafts resolve to, recorded per repository. Its true value is
`config.ServedValue` and nothing else, and an absent entry is not true — the same road, and the same
reading, as `config.ProviderKeysServedEnv`.

| Where | What |
| --- | --- |
| recorded on | a repository's own entry in the installation's config, under `config.RepoEntryCredentialsServedKey` (`credentials_served`, `localcfg.Repo`'s new field, CM15) — a NEW key beside `CredentialsServiceReady` and not that field's second meaning: a repository can be applied with the switch on and still have a craft whose credential the bundle does not hold, which is a launch refused with the fix (F19) |
| planned from | `internal/infra`'s spec builder, into the job's entry (CM15) |
| read by | the run, out of the execution's environment |

It replaces the keys plan's second fact, "the stage account is served" (F20): under D3068 a stage's
account is the access of that stage's craft, so one fact per repository covers it. A fact set where
it should not be gains the setter nothing: the service derives what it serves from the record and the
verified repository, so the worst a forged fact reaches is that run's own call, and never another
repository's credential.

## The files, and why the names live where they do

The names are one list in `internal/config` (`access_names.go`) because it is the one package every
side may import: `internal/gateway` imports it and may not import `internal/credsvc`;
`internal/infra` plans a job with the fact and must not import `internal/craft`'s readers;
`internal/localcfg` and `internal/creds` import config and each other not at all. A name spelled in
two packages is a repository served what its record does not name, or refused for a call it never
made. That is also why the contract is past five files: the question and the answer are
`internal/craft`'s, the rule is `internal/craft/legacy.go`, the record is `internal/creds`'s, the
wire has a copy in `internal/gateway` (F26), and the stubs sit in the two packages that consume the
contract before their real sides land.

Two notes for whoever reads the plan against this branch:

- `internal/creds/repoaccess_test.go` is a file the plan's **Files** list omits but its **Command**
  requires (`go test ./internal/creds/ -run TheRecordNames`).
- `internal/craft/access_test.go` carries two pins beyond the plan's **Tests** list —
  `TestTheHandedEntryIsTheAnswerItEncodes` and `TestARunWithNoHandedEntryAppliesTheLegacyRule` —
  because (b)'s encoding and fallback are the contract's too. CM8's own
  `TestARunWithNoHandedAccessAppliesTheLegacyRule` is the runner's side of the same fact and is not
  a duplicate of it: the craft's pin holds the entry and the rule, the runner's holds the run.

## What is not decided here

- **Which access a craft resolves to when the project wrote none.** That is CM1's `craft.ByReach`,
  judged from the credential's type and the craft's model (D3061 as amended). The stub in
  `internal/craft/access_stub.go` answers the migration's rule alone and is fail-closed about
  everything the rule cannot decide — a file that writes none of the retired keys is answered
  nothing, and a caller must refuse rather than guess.
- **What a repository's crafts are, at the project level.** CM2 writes the block and the node; this
  contract states the key, the path's family and the version a child carries.
- **The record's writers and readers.** CM5 works out what the record says from the project's config
  and writes it; CM6 is the only reader that decides anything from it. The record's own shape is
  here, and the rule that fills its `crafts` for an unset access is F43's.
- **Whether a credential is served.** (h)'s fact is recorded by CM15 and read by the run; the service
  never trusts it.
