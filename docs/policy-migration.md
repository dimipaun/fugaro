# The published policy, across versions

The published policy node (`config/policy/shared` in the project's Realtime
Database) carries a `version`. That number is the shape its reader understands,
and it moves when a release changes what a reader must *enforce* — not when a
field is merely added. `internal/projectpolicy/version.go` holds the number, the
versions this Fugaro writes and the reader that reads them.

A node written in an older version is not a node this Fugaro can use: its
reader refuses it rather than acting on half of it. And until 0.15.0 the only
way forward was `fugaro project policy publish --delete` followed by a fresh
`fugaro project policy publish` — a deletion, and one that loses every field
the owner had edited into the node by hand (`allow_repos`, `clone_urls`,
`routing`, profiles, accounts, blocks no release models).

## What `fugaro update` does to an older node

`fugaro update` (and `fugaro upgrade`) migrates the node **in place**, never
deleting it:

1. it reads the node and the ETag it was read at;
2. it runs the migration chain (`projectpolicy.Migrate`), which moves the node
   forward one version at a time and reports, for each step, what it changes
   and what it keeps — every top-level key but the version is kept as it is,
   read off the node rather than listed by the step, so a block the owner wrote
   by hand comes back byte for byte;
3. a version this Fugaro does not understand is refused untouched: a node
   written by a *newer* Fugaro says so, and a node this Fugaro has no step for
   and no refusal for cannot happen (a release that raises the version ships
   its step — see `docs/release.md`'s checklist);
4. a step whose node needs a member the *node alone* cannot state says so in
   its report. That is the `bitbucket_token` block: it is derived at publish
   from `project_secrets.bitbucket-token.allow_data_to` in the project's local
   config, together with the `bitbucket_api_roots` allow-list. The stage
   derives both from the local config with the publisher's own functions and
   patches them into the node **before** migrating it, so the operator is not
   sent back to a republish they do not need. It does that whether or not the
   migration would have refused: a node whose profiles mount no Bitbucket
   credential migrates cleanly without the block, and the runs that read the
   migrated node afterwards would fail closed on a block a publish would have
   written. Only a member the node does not already carry is written, so a
   block the owner narrowed by hand is not the local config's to replace. A
   project whose local config states none derives nothing, and a node that then
   cannot be read fails closed;
5. it prints **one diff** — what each step changes, what is kept as it is, what
   was derived from the local config — and writes the node back with the ETag
   it read, after the project's name is typed at a real terminal (or `--yes`
   where a confirmation is enough for the operator; a run that is still in
   flight stops the write unless `--yes` is passed, exactly as `publish
   --delete` does with it).

## Who publishes the node

`fugaro update` is the one command an operator runs, and it is also what
*publishes*: the same stage that migrates an older node writes the node the
local config states when the published one does not carry it — the budget block,
the model-price overrides, the base branch, the routing switch and the review
floor. A config edit therefore reaches every run of the project through the one
command, instead of a second `fugaro project policy publish` after every edit.

It is one write. The migration and the publish share one ownership read (the
database this installation adopted), one ETag and one write path, and the
document a publish writes is built on top of the migrated node, so a run that
has both to do does both in a single ETag-guarded write. `fugaro project policy
publish` builds its document through the same function, so the two doors cannot
drift into two shapes of one node.

A publish never touches the allow-list (`allow_repos`): it is the node's own,
and re-seeding it is `--seed-repos` on the explicit door (P4 — removing an entry
is how a repository is un-enrolled). And a node that already carries what the
config states is not rewritten at all: the stage writes nothing, byte for byte.

## A machine whose file is older than the node (D854)

The published node can be moved by *another* machine — a second operator's
laptop, or the console. That machine's config file, meanwhile, states what it
stated when it was last written from the node. Publishing from it would replace
a newer node with an older file.

So the local config records the generation its policy keys were last written
from (`policy_generation`, the ETag the node was read at), and a
`fugaro update` that finds the published node ahead of it refuses the publish
and says so: it prints the three-way state — the generation this file was
written from, the one the node carries now, and the members the two differ on —
and offers to **re-seed** the file from the published node instead, under the
same typed confirmation (`--yes` where that is enough), with a backup of the
file it replaces. Nothing of the older file is published. An installation whose
config records no generation adopts the one it reads: refusing once would ask a
question with no information in it, since the machine has no baseline to compare
against.

A member a single file edit cannot re-seed (a value the node leaves out, which
the file would have to drop) is refused by name rather than skipped: a re-seed
that recorded the generation while leaving a member behind is exactly the
overwrite this rule exists to prevent.

A project policy is read under a deployed security rules generation, so the
migration's write is gated on it (`requirePolicyRules`): the same run deploys
the generation this Fugaro carries **before** the write — `fugaro update`'s
rules stage, and `fugaro upgrade`'s rules step, both of them one deploy of one
document — so an installation whose deployed rules predate the policy model
reaches the node with the one command, instead of meeting the gate and being
sent to a second one. A node already at this version is left alone whatever the
deployed rules are.

`fugaro upgrade --check` does not read the node at all: the node lives in the
project's Realtime Database and reading it needs credentials, and `--check`'s
own promise is that it makes no cloud call. It says the policy step cannot tell
instead, and the upgrade without `--check` is what migrates it.

The write is a writer's write, so it holds the same posture a publish does: a
node carrying top-level keys this Fugaro does not model is refused, untouched,
with the count of those keys (never their values) and the remedy — upgrade
Fugaro, then run `fugaro update` again, so the newer binary that knows them
does the migration.

Two refusals stay put on purpose. A coding agent's session never writes the
policy (every cloud write of this CLI is the operator's, in their own terminal):
`fugaro update` there exits 1 with the checkout left for the user, and
`fugaro upgrade`'s rules and policy steps are skipped, each naming the command
to run in their own terminal — an expected exit, not a failure. The same holds
for every command that writes the node by hand: `fugaro project policy publish`
(including `--delete`, which takes the policy away), and `fugaro project repos
add`, `repos rm` and `sync`, which write its allow-list — a coding agent's
session is refused before the read, so nothing is written and, for `sync`, no
GitHub call is even made. And a
concurrent writer — a publish landing between the read and the write — makes
the write fail rather than overwrite: the operator confirmed a diff of a node
that is no longer there.

## When `--delete` is still the answer

- The node is one only a *newer* Fugaro can read: upgrade Fugaro first, then
  `fugaro update`; a `--delete` here would drop the fields that binary does
  know about, and the ones it does not.
- The node's version is **below the chain's first step**: 1 or 2, which 0.8.0
  and 0.8.5 wrote (`projectpolicy.VersionSince`), while the chain starts at 3,
  the version 0.9.0 introduced. No step reaches them — the chain holds the move
  0.14.0's bump needed, and a node older than the model the migrators read is
  not one they can carry over. Both doors refuse it with the sentence a publish
  uses for the same state, whose remedy is this row: `fugaro project policy
  publish --delete`, then a publish from this release.
- The node is malformed in a way no migration can fix (it is not a JSON object,
  its version is not an integer, no release ever wrote its version, a top-level
  key is written twice, or the migrated document is one the current reader
  still refuses): `fugaro project policy show` prints what it can, and
  `fugaro project policy publish --delete` removes the node and the per-repo
  children under it, after the typed confirmation.
- The project wants the policy gone: `--delete` is the removal, and it is
  unchanged by this work.

Migrating is never a substitute for reading the diff: the node is what every
run of the project is admitted by, and the confirmation is where the operator
sees what their policy becomes.

## For operators (draft for the 0.15.0 release notes)

```markdown
### For operators

Run `fugaro update`. It brings the installation, the repository and — new in
this release — the published policy node up to this version: a node an earlier
release wrote is migrated in place, after a diff of what changes and what is
kept, and it asks for the project's name (or `--yes`) before writing. It also
now publishes the policy node from the local config — a config edit reaches
every run of the project through this one command, with no separate publish —
and `fugaro project policy publish`, which writes the same node by hand, is
refused in a coding agent's session. Nothing is deleted and nothing has to be
published again. Expect the confirmation when the policy's version moved or when
a member of the local config is not what the node carries; a project whose node
is already current is told so and asked nothing.
```
