# The roster contract (C1)

The Go half is [`internal/workspace/roster.go`](../../internal/workspace/roster.go) and the schemas are
[`schemas/workspace-roster.schema.json`](../../schemas/workspace-roster.schema.json) and
[`schemas/workspace-audit-event.schema.json`](../../schemas/workspace-audit-event.schema.json). This document is the
prose half, and it is the half the sides are held to: it says what each side may rely on, what none of them may, and
what changing the contract costs.

C1 is a contract and not an implementation ([implementation-plan.md](implementation-plan.md) §3). Nothing here computes
a drift, writes a member and reaches no cloud: it holds the roster's shape, the audit event's shape, the two functions
that read and write each, the two schemas, the `Store` and `AuditSink` seams, and the stubs
(`internal/workspace/roster_stub.go`) the sides compile against. I1 deletes exactly that file when it adapts the stores
WU1 merged (`RosterStore`, `AuditWriter`) onto the seams. It exists because the level-2 tasks are written against one
decision at once and cannot wait for each other: **WU24** writes the first roster, **WU7** and **WU8** are its writers,
**WU5**, **WU6**, **WU18**, **WU13**, **WU9** and **WU23** change it, **WU11**, **WU12** and **WU15** read it, and
**C2** carries the one shape of it that lives in a member's own database.

It rests on the owner's rulings of 2026-11-20: a machine member is recorded in the workspace node and nowhere else,
with the roles the workspace expects it to hold (D2806); the audit trail is create-only, and a change no Fugaro
command made is visible against Cloud Audit Logs (D2811); a person from outside the allowed domains is admitted with
explicit confirmation and marked (D2812); a member's copy of the member list is advisory and drift is the fence
(D2813); the roster is a live node written only under its ETag (D2817); the grants the workspace wrote are recorded
in the workspace node and never in a member's roster (D2818 as corrected); an admin entry is a claim about a live
binding and not a permission (D2823); and the mechanism -- the folder the workspace owns, or the service-account model
of D2803 -- is decided once, when the workspace is created (R6).

## Why the roster is a node of its own, and why the shape is one file

The roster is at `workspaceRosters/<name>` in the workspace's own database: a SIBLING of the published record at
`workspaces/<name>` and never inside it (R5). The place is not cosmetic. The publish stage of `fugaro update`
rewrites the record whole and refuses a node carrying a key it does not know, so a roster inside it would stop the
update of every CLI older than this plan — the failure the HARD RULE forbids. A node of its own carries its own ETag,
and nobody else's write touches it. Nothing about the mechanism needs a rules change (R7): the workspace rules deny
every client at the root, so the node is reached by IAM alone — the workspace's own service, its admins and the
workspace project's owners, editors and budget admins, which is who D2817 says may write it.

The shape is one file and one schema pair because it is a wire format over IAM: five accounts of the workspace's
project can rewrite the node, and every level-2 task reads or writes it. A key whose meaning drifted between two of
them would be a roster that says one thing to the writer and another to the reader, and the drift fence (D2813,
D2817) turns that into an incident across every member. So the document is closed at every level (an unknown field is
an error, as every config and wire format here does), the schema and the Go types are held to each other by the tests
in [`internal/workspace/roster_test.go`](../../internal/workspace/roster_test.go) — the enum lists, the address
grammar, the folder grammar and the version consts are compared key by key rather than trusted to agree — and
`schemas/schemas_test.go` pins the same enums to the same Go lists from the other side.

## The shapes

| Shape | Carries | Decided by |
| --- | --- | --- |
| `Roster` | `Version`, `Mechanism`, `FolderID`, `People`, `Admins`, `Domains`, `Grants` | `DecodeRoster` and `EncodeRoster`; `Validate` holds a roster a caller built itself |
| `Person` | `Address`, `Kind` (`human` or `sa`), `External`, and `Roles` for a machine | the person's own entry; a machine's roles are D2806's expectation |
| `Kind` | `human` or `sa` — the closed vocabulary, `Kinds()` | this package |
| `Mechanism` | `folder` or `sa`, `Mechanisms()` | `workspace init` (WU24), and never changed afterwards |
| `Role` | `viewer`, `operator`, `admin` — the member roster's own set, `Roles()` | this package, held to `internal/access.Roles()` by a test |
| `Action` | the closed list of audited changes, `Actions()` | this package, one per command that writes the node |
| `AuditEvent` | `Version`, `ID`, `Time`, `Actor`, `Action`, and `Project`, `Subject`, `Role` when they apply | `DecodeAuditEvent` and `EncodeAuditEvent` |
| `MirrorEntry` | `Address`, `Kind`, `External` | the entry only: the document that holds it is C2's |
| `Revision` | `ETag`, `Found` | a `Store`'s read, and never a caller's own guess |
| `Store` | `Path`, `Read`, `Write` | I1 adapts `RosterStore` onto it |
| `AuditSink` | `Create` | I1 adapts `AuditWriter` onto it |
| `NewAuditEventID` | the id grammar, over an instant and a source of randomness | this package |

## The roster, key by key

**`version` is 1 and is written in every roster.** A reader refuses any other value with "a newer Fugaro wrote it"
rather than reading the half it recognizes: a field never changes meaning, so a shape that has to move gets version 2,
and a name that has to change gets a new name. An older CLI meeting a newer node refuses it — that is the answer the
HARD RULE wants — and the closed objects below are what makes that refusal certain rather than hopeful.

**`mechanism` and `folder_id` go together.** `folder` means the workspace owns a folder its members' projects are
moved into and an admin is a folder owner; `sa` means the service-account model of D2803, where the workspace reaches
a member through an account in that member's own project and an admin is whoever may mint a token for it (F20). The
folder id is written exactly when the mechanism is `folder`: an id under `sa` names a folder that is not that
workspace's, and a `folder` workspace without one names no folder at all. Both halves are refused by the reader AND
by the schema (`allOf`/`if`/`then`), because an editor of a hand-written node has to see the rule too. The id's
grammar is the one a recorded folder parent is held to (`folderParentRE`), cut to `folders/<number>`: this program
creates folders and never organizations.

**`people` is the workspace's list, sorted by address, each address once, and never empty.** An address is written in
its canonical spelling (`CanonicalAddress`): printable ASCII with no whitespace, one `@`, a non-empty local part and a
non-empty domain folded to lower case. The local part is left exactly as it was written — an address is
case-sensitive there, and `access.Member` folds it the same way — so the schema's pattern is precisely the set of
spellings the fold leaves alone, which is what `TestRosterSchemaAgreesWithTheReaderOnAddresses` holds it to over a
corpus.

**A person is a `human` or a machine (`sa`), and the external mark is written on every one of them, `false`
included.** The mark is D2812's statement about how somebody was admitted — from outside the allowed domains, which
the CLI asks about by naming `--external` — and not a default a reader may assume, so the key is required, the reader
refuses its absence, and it is never omitted. A machine is never marked external: it is admitted by the workspace's
own decision and comes from no domain. A reader that wants to know whether somebody is an outsider computes it from
`domains` and not from the mark, which an admin can rewrite; the drift check (WU12) is that reader.

**`roles` belongs to a machine and to nobody else (D2806).** For a machine they are the per-project roles the
workspace EXPECTS it to hold — the plan `status` compares against the live IAM — and not a grant: the workspace's own
IAM gives a machine what it holds, and Fugaro writes no grant for one. A human's entry that carries a role is refused
by the reader and by the schema: a person's roles are grants in the members' own rosters, and a second place they
could be read from is a second answer to "what may this person do".

**`admins` is a set of PEOPLE of this roster, sorted, each once, and never empty.** An entry is a claim about a
binding that lives in IAM and not a permission (D2823): only an entry whose live binding the mechanism's own check
confirms is trusted, which is why `workspace project status` compares the two and reports an admin entry whose
binding was never given as drift. An admin who is not a person of this roster, and a machine listed as an admin, are
both refused by the reader — neither is expressible in JSON Schema, because the rule reaches across two lists of one
document. R8 is the other half and is not this document's: the admins also hold `roles/owner` on the workspace's own
project, and that binding is made by the mechanism, not recorded here.

**`domains` is the allowed list of D2812**, folded to lower case, sorted, each once, and absent for a workspace that
has listed none. The fold is enforced on both sides: the domain of an address is folded, and the entry is compared
against it as a string, so two spellings of one domain would let a person from outside the list be admitted as one
from inside it.

**`grants` is the mark D2818 keeps here and nowhere else.** Each entry is one grant the workspace wrote into a
member's own roster: that member's project, the person's address and the role. It is the workspace's record of what
it wrote and never the grant itself — the member's roster holds the real one — so `workspace project reconcile`
(WU13) and `status` (WU11) compare the two, and a grant a member's own admin removed by hand is a drift row rather
than a silent loss. A grant for somebody who is not a person of this roster, or for a machine, is refused by the
reader: taking a person off the roster takes their grants with them, in one write.

**What the node does not carry.** No per-project entry (R14): a workspace's own projects are records of their own, in
`workspaces/<name>` and in the member records, and a roster that copied one would be a second place the same fact
could disagree. No member's Firebase project, no job service account, no credential, no secret, and no role a person
holds in a member — those are the member's own roster (`internal/access`) and the member's own config. A machine's
member roster, grant and audit node do not exist at all (D2806).

## What the schema refuses, and what only the reader can

The split is the contract, so it is written down. JSON Schema has no ordering keyword, no uniqueness keyword and no
way to reach across two lists, and a schema file is fetched by its `$id` and read by an editor who has never seen
this repository — so what it can state, it states, and the rest is the reader's, restated in
`internal/workspace/roster.go` and pinned there.

| Rule | Schema | Reader |
| --- | --- | --- |
| Closed objects, required keys, `version` const, enums, the address/domain/project/folder grammars | yes | yes |
| `folder_id` exactly when the mechanism is `folder` | yes (`if`/`then`/`else`) | yes |
| A machine is never external; a human carries no roles | yes | yes |
| A field spelled in another case, or written twice | the case, by the closed objects | both — a validator reads one value per name, the last, so the repetition is gone before it is asked |
| `people` and `admins` are never empty | yes (`minItems`) | yes |
| Every list is SORTED and holds no duplicate | no — the reader alone | yes |
| An admin is a person of this roster, and a human | no — reaches across two lists | yes |
| A grant's user is a person of this roster, and a human | no — the same | yes |
| The external mark is present | yes (`required`) | yes (the wire type's pointer) |
| An empty optional list (`domains`, `grants`, `roles`) | admitted | admitted, as the empty list |

The last row is deliberate: the database prunes a null, an empty object and an empty list on the way in, so the
absent key and the empty list are one and the same thing and a schema that refused `[]` would refuse a document the
node really holds. A document the schema admits is a document this package reads back unchanged, and the tests hold
that in both directions.

## The audit event

`AuditEvent` is one entry of a trail that is create-only (D2811): one document per event, created and never updated,
with every change of the roster recorded — a person added or removed, an admin's entry, a domain edited, a grant
written or taken back, a project added, removed, linked or unlinked, an offboarding — with the actor and the instant.
The same document is what a MEMBER's own trail holds (C2), written into `config/admin/workspace-audit` of that
member's database, because a project admin holds no write on the workspace's project at all (F11, D2813); the shape
lives here, in the contract both writers share, and not at either of them.

**`id` is a Firestore document id and a database key at once**, so it carries nothing either store refuses: printable
ASCII without `.`, `#`, `$`, `/`, `[` or `]`, and never a leading `__`, which Firestore reserves. `NewAuditEventID`
mints one per event: the instant in UTC, a `-`, and eight random bytes, so a listing in key order is a listing in
time order to the second and two events made in one second are two documents rather than one create-only refusal.

**`time` is an RFC 3339 instant** and the type holds it parsed, so no reader parses it twice. `format` is an
annotation in JSON Schema and not something a validator enforces, so the reader is what holds a node to it; the zero
instant is refused on both sides. **`project`, `subject` and `role` are written exactly when they apply** and left
out otherwise: an event about a domain names no project, and an event about a project names no role.

**The trail is what makes a change Fugaro did not make visible.** Whoever holds the workspace project's IAM writes the
database with the rules bypassed, so an entry can be deleted or rewritten by an admin (threat-model.md §3, §7 item
5). The property the design rests on is that Fugaro never updates one, and the cross-check against Cloud Audit Logs —
a `SetIamPolicy` whose resulting binding is drift and which no trail of Fugaro's holds is a `status` row (D2811, R13,
WU12) — is the fence.

## The seams, and what I1 does

`Store` reads a workspace's roster node and writes it back under the revision it was read at. `AuditSink` creates an
event. Both are interfaces only so that the level-2 tasks are testable without a database (the plan's hermetic-tests
rule): every one of them injects its own double in its own test and never the contract's stub, which is why I1 can
delete `roster_stub.go` without rewriting a test.

**A revision comes from a read and from nowhere else.** An ETag a caller made up is a precondition on a document
nobody saw, which is exactly the overwrite the ETag prevents, and `internal/rtdb` refuses an empty one rather than
reading it as "write unconditionally". An absent node reads as `Found` false beside the store's null ETag, and a
write under that revision CREATES the node — which is the first write of a workspace, and the reason no `fugaro
update` stage seeds a roster (R7). Two first writers cannot both create one.

**A write that did not land because the node moved under the writer is `rtdb.ErrPrecondition`, and that is the answer
and not a failure:** the caller reads the node again and judges its change against what is really there, so a change
it never saw survives and its own is the one that is remade. `errors.Is` is how a caller tells that from a failure —
denied, unreachable, a body that is not a document — and a failure is reported, never retried into.

**The stubs decide nothing.** `StubStore` reads no node (`Found` false, no bytes, which is what a workspace whose
roster has never been written reads as) and `StubStore.Write` and `StubAuditSink.Create` answer an error: `nil` is the
one answer a stub must not give, because a caller that read it would believe a node was written or an audit entry
created that is not there.

## The mirror entry

`MirrorEntry` is one person as a MEMBER's own mirror holds them: the member list written into that member's database
at `config/admin/workspace-members`, so a member's own command can tell its own people what the workspace says about
them without holding a grant on the workspace's project at all (D2813). The entry carries the address, the kind and
the external mark, and nothing else — no roles, no grants, no project of the workspace's. The document that holds the
entries is C2's (`internal/workspace/memberops.go`, `contract-member.md`), it is decoded strictly by the same
`DisallowUnknownFields` that refuses an unknown key inside an entry, and this type is the entry.

**The mirror is advisory and nothing acts on it.** It is writable by that member's own admins, so it is what the drift
comparison reads to say that a member's copy has been rewritten; the workspace's own roster stays the record, and a
decision made from a mirror alone would be a decision an admin of the member could make.

## What each side may rely on

**`workspace init` (WU24).** It creates the roster: the mechanism, the folder the workspace owns when the mechanism
is the folder (WU2's authority creates it), the caller as the first person and the first admin, and one `init` audit
event. It may rely on `EncodeRoster` refusing a roster its own reader would refuse, and on the create path of
`Store.Write`: a `Revision` from a read that found nothing, written under the store's null ETag.

**The writers (WU5, WU6, WU7, WU8, WU9, WU13, WU18, WU23).** They read the node, change the roster in Go, and write it
back under the revision they read. They may rely on: `DecodeRoster` refusing anything they could not have written; the
lists being sets, so a change is a sort and a de-duplication rather than an append; `CanonicalAddress` for anything an
operator types; `Update`-style read-modify-write being safe under `rtdb.ErrPrecondition`; and `EncodeRoster` refusing
a machine's grant, a human's role, or a grant for somebody the roster does not hold. A change to the roster and the
audit event it records are two writes and not one transaction: the trail is a separate create-only collection, and an
entry is written after the change it records, never instead of it.

**The readers (WU11, WU12, WU15).** They may rely on a decoded roster being a complete, consistent statement: every
person in its canonical spelling, every list sorted, every admin a person, every grant's user a person of the roster.
WU12's drift comparison reads `Domains` for outsiderness and never the external mark, compares `Grants` against the
members' own rosters, and treats a live binding with no `Admins` entry as drift in the other direction. Everything
either of them PRINTS is escaped: a member-controlled string reaching a terminal is the same hazard the record
migration already pins (`TestUpdateEscapesWhatTheRecordMigrationEchoes`).

**C2 (the member side).** It holds the mirror's document and the member-side audit node. It may rely on
`MirrorEntry`'s three keys and on `AuditEvent`'s shape for the entries and events it writes; it must not add a field
to a member's roster (D2818 as corrected) and must not read this node as its own record. The workspace node's audit
collection is `internal/workspace.AuditCollection` in the workspace's project; a member's is its own node in its own
database, and neither writer reads the other's.

**I1.** It adapts `RosterStore` (`Read`/`Write`, already conditional) and `AuditWriter` (`Create`, already
create-only) onto the two interfaces, adds their registration and deletes `roster_stub.go` and
`memberops_stub.go`. Nothing in this contract changes when it does: the seams were written to be wrapped, and the
stub's removal is not a rewrite of any test.

## What no side may rely on

- **A stub's answer.** `StubStore` and `StubAuditSink` decide nothing, and I1 deletes the file rather than promoting
  it.
- **An ETag a store did not read.** There is no unconditional write in this contract, and a caller that invents a
  revision is the overwrite the node exists to prevent.
- **The mark as a grant.** `Grants` records what the workspace wrote; it grants nothing. Admission is the member's
  own policy (`internal/access`), and D2818's mark is what `reconcile` compares, not a permission.
- **The external mark as a verdict.** It is how somebody was admitted; outsiderness is computed from `Domains`.
- **The mirror as the record.** It is advisory, member-writable, and read only to report drift.
- **A role on a human, a grant for a machine, or a folder id under `sa`.** All three are refused by the reader, and a
  side that needs one of them needs a contract change and not a workaround.
- **A hand-edited node as a document this side must keep working.** The node is written by several parties, so
  everything read is validated before it is acted on, and an unknown version, kind, mechanism, role, action or key is
  a refusal naming the fix.
- **A secret in the node.** There is none here and there never will be: the roster is a document its admins write and
  a drift comparison reads.

## Changing the contract

The roster and the event are wire formats, so a field never changes meaning: adding one is what version 1 already
allows — an older reader refuses it by the closed object — and removing one, retyping one, or changing what one means
is `RosterVersion` 2 or `AuditEventVersion` 2, written here, in the schema and in the Go type in one sweep. A stale
node of the older version is refused with "a newer Fugaro wrote it" and is not migrated by this package: `fugaro
update` is the whole upgrade (the HARD RULE), so a version bump ships its migration inside `update` and a test that
an installation at the previous version converges.

Amending C1 means editing `internal/workspace/roster.go`, `rosterstore.go`, the two schemas, this document and
`internal/workspace/roster_test.go` together, in the review that finds the shape short — the plan anticipates exactly
one such change, **R-roster-sa**, if a machine member turns out to need an owner or an expiry on `Person`; C2's files
and `memberops.go` are not C1's and are never changed in the same sweep. No side works around a shape it finds short:
the shape and the "what each side may rely on" line change together (the plan's *Ask first* rule).
