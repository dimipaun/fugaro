# The workspace credentials node, version 2 (C3)

Task **C3** of [implementation-plan.md](implementation-plan.md). The rules it states are **R6**, **R7** and
**R19** of [reconciliation.md](reconciliation.md), the answer to **U2** is its own, and the decisions behind them
are D2992 (a job account may be a job of several repositories), D2977 (`all_members`, and when the owner may
write it), D2978 (one rule for every credential kind), F15 (the pool was model-only) and F42 (a shared job's
`allow_repos` is a list of *patterns*). The shapes are `internal/projectpolicy`: `WorkspaceCredentials` with
`WorkspaceCredentialMember`, `WorkspaceShared` and `WorkspaceJobAccount`, the constant
`WorkspaceCredentialsVersion`, and `Validate` (the reader's rules); the version's own reader is
`internal/projectpolicy/workspace_credentials_v2.go`. The schema is
[`schemas/workspace-credentials.schema.json`](../../schemas/workspace-credentials.schema.json), and this document
is what it means.

**WS2, WS5, WS14, WS15, WS16 and WS17 read this node**; **WS3 and WS4 write it**. The types are the whole
contract and there is no stub: a column a reader needs and this document does not name is a column it cannot
rely on. A side that finds the contract wrong or incomplete raises a change request
([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than working around it.

## The shape

The node is the JSON object at `workspace_credentials/<name>` in the workspace's own database
(`projectpolicy.PathWorkspaceCredentials`). Its top level is **read tolerantly** — an unknown key is collected in
`WorkspaceCredentials.UnknownKeys` and nothing else happens, so a node a newer Fugaro wrote does not brick an
older reader (P8) — while every object below it is **closed**: a key inside a member's row, a pool entry or a
job account's entry is a field this Fugaro does not write and the decode is refused, naming the key.

| Key | Who writes it | What it means |
| --- | --- | --- |
| `version` | the writer | the node's format version; this document describes **2** |
| `name` | the writer | the workspace's name, a project name (F21), and the last segment of the path |
| `members` | the writer | the per-member table, in project-name order, each member once |
| `members[].project` | the writer | the member's project name — the namespace the node and the workspace's record both speak |
| `members[].firebase_project` | the writer | the member's Firebase project id: the `aud` of a run identity minted there, and the fact the member's database is derived from |
| `members[].database_region` | the writer | the region the member's database is in, or empty for the project's default region |
| `members[].fp` | the writer | the member's project name as the run identity's `fp` claim carries it; `Validate` refuses a row whose `project` and `fp` disagree |
| `members[].job_accounts` | the writer | the member's job service accounts, by address, in the row's own order — **the list version 1 already had, with exactly that meaning** |
| `members[].job_account_repos` | the writer | version 2's column: one entry per account of `job_accounts`, in that list's own order, each naming `account` and the concrete `repos` it is a job of |
| `members[].exchange_url` | the writer | where the workspace's signer mints a member-bound assertion for (spec.md §3, D1293) |
| `shared` | the writer | the pool, in name order, each name once (D1298) |
| `shared[].name` | the writer | the credential's name — the name the member's bundle and its policy's `accounts` block speak |
| `shared[].kind` | the writer | version 2's column: `model` (`SharedKindModel`), or a kind a repository's `git.provider` may be (`config.Providers`) for a forge credential |
| `shared[].all_members` | the writer | version 2's column: written `true` for an entry offered to every member of the workspace (D2977) |
| `shared[].projects` | the writer | the member projects the entry is offered to, sorted and each once — version 1's column, and exclusive with `all_members` |
| `shared[].covers` | the writer | version 2's column: the concrete repositories a **forge** entry reaches, sorted and each once, each with the entry's own `kind` as its provider segment |

`job_account_repos` and `covers` are **lists of concrete repositories**, in the one spelling this tree writes a
repository in: `projectpolicy.Canonical`'s, `<provider>/<owner>/<name>`, ASCII lower-cased, no trailing `.git`.
**A pattern is refused where it is written** (`github/acme/*`, `*`), for F42's reason: the workspace's check
compares the concrete slug a run identity carries, and a pattern is a declaration about repositories that do not
exist yet. What a pattern is for — the member's own `allow_repos`, which is a list of patterns — is asked
separately, on the intersection (R5, D1298).

## The rules (R6, R7, U2)

1. **One account, one list of repositories.** `job_accounts` and `job_account_repos` are ONE declaration written
   in one order: the entry at position *i* names the account `job_accounts[i]` names, and `Validate` refuses a
   row whose halves disagree — a shorter or longer list, or an entry naming another account. A row read in halves
   would be a caller whose reachable repositories depend on which half a reader believed.
2. **A shared job's account carries several repositories.** One for a repository's own job, several for a shared
   project job, which is the shape the shared-jobs-default work makes usual (O3, O4, D2992, F37). The seam a call
   is admitted at holds the run's verified repository to the list of the account that presented it, so the list is
   what makes "the job may be on this repository" answerable at all.
3. **A row that declares none declares none.** An empty `job_account_repos` — an empty list, or the key absent,
   which this reader reads the same — is a STATE, not an error: it is what a version-1 row decodes to (the version
   never said), and what a member whose owner has added the account but has not declared its repositories yet
   carries. The scope refuses such an account's runs with the refusal that names the workspace owner's
   `fugaro update` (R19), and **never** reads the empty list as "every repository". A row that declares SOME of its
   accounts' repositories is neither state and is refused by `Validate`: a declaration read in halves is worse
   than one that was never written.
4. **A pool entry says which kind of credential it is.** The kind tells the service which route may serve the
   entry (D2978, D2984): a model call is never served a Bitbucket token, and vice versa. A kind that is neither
   `model` nor a kind a repository's `git.provider` may be is refused; the node spells no list of kinds of its
   own, because `internal/config`'s `Providers` is where they exist and a second list here could disagree with it.
5. **`all_members` and `projects` are exclusive, and one of them is written** (R7). An entry offered to the
   members it names carries `projects` and no `all_members` at all; an entry offered to every member carries
   `all_members: true` and no `projects`. The writer omits either when it is empty rather than writing an empty
   list, so a reader cannot find a `false` beside a list and has to read the list. A node that carries both is
   refused rather than read as one of the two: a reader that picked would be deciding who may be served a
   credential, and that is the workspace owner's decision. An entry that carries neither is refused too — a
   written shared credential that names no member is a half-written declaration, and the two readings of it
   ("everybody" and "nobody") are the one mistake that would either widen the pool or hide it.
6. **A forge entry names the repositories it covers, and a model entry names none** (U2). The coverage is the
   workspace owner's own list — the repositories their credential is stored for — and never the member's; each
   entry is a concrete repository whose provider segment IS the entry's `kind`, so the kind and the coverage
   cannot disagree. `Validate` refuses a model entry that covers repositories (the repositories a model call may
   be made for are the member's own policy's business, and an entry that named them would be a second, weaker
   answer to that question) and a forge entry that covers none (a half-written declaration: the service cannot
   say which runs may be served it).
7. **The node carries no credential's value.** Not a token, not a key, not a proxy URL: the entry names a
   credential, and the value lives in the workspace's own secret store (rule 2 of the
   [workspace credentials spec](../workspace-credentials/spec.md) §4). The two columns version 2 adds are
   *declarations about a credential*, never the credential.
8. **Version 1 still decodes.** The reader reads the version first and hands the document to that version's own
   row reader, so a version-2 column under version 1 is refused rather than dropped. What version 1 does not say
   is not invented: an account of a version-1 row carries an empty repository list (rule 3), and a version-1 pool
   entry is a model credential, which is the only kind the pool could have held (F15). A version no release wrote
   — including a node with no `version` key at all — is refused with `*WorkspaceVersionError` rather than read as
   the nearest one.

## What this contract cannot promise

### The node cannot say whether a member's own policy admits the entry

`shared[].projects` is the workspace owner's fence over their own money, and the member's own policy is a second,
independent fence: a call routed to the workspace passes the entry's allow-list, the member's policy AND the
repository's admission (R5, D1298). Nothing in this node says what a member's policy holds — the workspace
cannot read a member's database for it (F38) — so an entry offered to a project may still be refused for that
project's runs, and a reader must not treat "offered here" as "served here".

### A coverage can be wider than the member's profile at that moment

F42's residual: the workspace owner's coverage is their own list, and it is not narrowed by what a member's
shared job profile says today. The check the service makes is the intersection, so the excess is refused at the
call rather than hidden — but it is not refused here, and a node that named only what every member's live
profile holds is not something this shape can express.

### The node cannot say when `all_members` may be written

D2977 writes `all_members` only once the cap meter is wired, and only for a workspace with a cap set. That is a
rule about the workspace's own record and its publish path, and the node carries **no cap**, so its reader cannot
ask it: an entry with `all_members: true` on a workspace with no cap is a node this reader accepts and the
writer must not produce.

## What each side may rely on

- **A reader (WS2, WS5, WS14, WS15, WS16, WS17)** may rely on: `DecodeWorkspaceCredentials` refusing a node it
  cannot read rather than returning a half-read one; `Validate` having asked every rule above; an account's
  repositories being concrete, canonical, sorted, deduplicated, and each account holding its own list; a pool
  entry's kind being a kind it can route by, with a forge entry's coverage non-empty and of that kind; and a
  version-1 node being readable, with the empty repository list of rule 3 — which it must refuse the run for,
  naming the workspace owner's `fugaro update` (R19), rather than treat as an unconstrained account.
- **A writer (WS3, WS4)** may rely on: a node built by `WorkspaceCredentialsNode` being one the reader reads
  (the encoder holds the value to `Validate` first); the writer's own refusals arriving before anything is
  published; and the version constant being the one place the format's version is named. It may not rely on the
  reader inventing a column it did not write: a row whose repositories it has not declared reads as rule 3's
  state, and a member's runs are refused until it declares them.
- **Both sides** may rely on the additive rule: a field in this node is added, never retyped ([CLAUDE.md](../../CLAUDE.md),
  "a field never changes meaning"). That is why the repositories of a job account are a new key
  (`job_account_repos`) holding one entry per account, and not a reworked `job_accounts`: the meaning of
  `job_accounts` — the list of the member's job service account addresses — is unchanged, and a reader of either
  version reads it the same way.

## What this contract does not decide

- **The writer, its command and the migration** (WS3, WS4): what a workspace owner types, which local fields
  feed the table, how a version-1 node is carried forward in place, and the order that publishes version 2 only
  after the service that reads it is deployed. This document describes the format the writer must produce.
- **Whether a shared entry names a project that is not a member of the workspace.** The node's own reader does
  not ask it (F13); the writer that knows the member list is where that refusal belongs.
- **How the scope turns a rule into a typed reason** (R16's and R19's halt reasons, the route's refusals): this
  contract says which facts the node holds, not how a refusal is worded at the seam.
- **The workspace's record** (`schemas/workspace.schema.json`, `projectpolicy.PathWorkspace`): its own document,
  its own version, and a different reader. A bump of one says nothing about the other.

## Review

Reviewer tier **opus + independent review** before a side is written against it
([implementation-plan.md](implementation-plan.md), Conventions): this is a contract over a wire format that
carries which repositories a workspace owner's credential may be spent from, and the change it fences —
repositories per job account, and a coverage per forge entry — is what holds a run to the repository it is on.
