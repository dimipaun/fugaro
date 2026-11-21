# Manage kinds: the descriptor the shell and the kinds share

**Status: written with task MG0 of the [implementation plan](implementation-plan.md), and the shape the shell and
the four kind tasks build against.** It describes code inside the console and nothing else: no CLI document, no wire
shape and no operation changes because of it. The spec's section 5 is the grouping it draws, as
[reconciliation.md](reconciliation.md)'s D2903, D2905 and D2909 amended it.

## The descriptor

`ManageKind` (`console/src/renderer/manage/types.ts`):

| Member | What it is |
| --- | --- |
| `id` | The kind's own name, unique in the console (`members`, `keys`, `budget`) |
| `group` | One of the four group names below, checked by `defineManageKind` |
| `title` | The heading of the kind's own section |
| `source` | The console operation whose answer the kind decodes (`members.list`, `budget.show`) |
| `columns` | The table's columns: a `key`, the `title` drawn above it, and the `TEXT_CLASSES` class every cell of it passes under |
| `decode(doc)` | The only place the kind's document is known: `unknown` in, the `ManageRow[]` the table draws out |
| `actions` | What the kind offers beside **every** row it decodes: `copy-command {argv, consequence}` or `open-view {view}` |

A `ManageRow` is a `key` (the row's identity among the rows of one read) and `cells`, one string per column, keyed by
the column's `key`. There is no separate status member: "status" in the spec's list view is a column like any other,
so a kind that has one names it in `columns`.

**A cell the document holds no value for is the empty string, and it is drawn as nothing** -- never as the console's
mark and never as a word. The mark (`n/a`, `availability.ts`) is the console's answer to a value it could not *read*,
and an empty cell is not that: the document answered, and its answer is that it holds no value there. A column's class
is the class of the value it holds, and the classes that read a value (`repoPattern`, `repository`) refuse an empty
string, so a shell that handed one to its class would draw a refusal where there is nothing to refuse. The CLI prints a
word of its own for the same state in its own table ("not admitted"); that word is the CLI's rendering of an empty
cell, not a value of the document, and it stays there. `repos.ts`'s two enrolment halves are the case: the allow-list
entry that admits a repository and the config key this machine holds it under, each an empty cell where the document
has none.

**A row may carry `actions` of its own** (`ManageRow.actions`, added by MO-C), and it is `optional` for the reason the
kind's own list is not: the command D2903 puts on the clipboard names the row it came from -- `fugaro project repos rm
github/acme/app` is one repository's -- and that value is a value of that row's document, which only the kind holds. A
command declared on the kind alone could only be one that names no repository, person or key at all, so a kind whose
rows each need their own declares them on the row, and a row that leaves the member out draws the kind's own
`actions` and nothing else. People & access's are MO-P's three -- a pair's `fugaro user revoke`, a person's
`fugaro user offboard` and an account's `fugaro accounts rm` -- and Code's are MO-C's repository removal.

**It is a descriptor, not a contract.** The console's contract is `console/docs/json-contract.md`, which is about
what the CLI sends and the console reads back; a descriptor is neither, and no reader outside this binary consumes
one. That is why a member added to it, a column moved, or a fifth kind is an ordinary change with no version: there
is no reader to break, and the shell never needed a kind's rows (which is also why MG0 has no edge to break with
the shell or the kinds it precedes).

**It is a prerequisite, and there is no format to migrate.** The field rule (`CLAUDE.md`) is about a shape a reader
outside this binary consumes: a member may be added inside a format's version, and removing or retyping one bumps
it. Nothing here is such a shape, so nothing here is versioned and no reader has to be taught it.

## The four groups, and their order

`MANAGE_GROUPS` is the spec's grouping, in the order the Project tab draws it. The order is part of the descriptor
so that a kind file cannot disagree with its neighbours about where its own group sits.

| Group | Kinds the plan gives it | Source operations |
| --- | --- | --- |
| People & access | members, the roles each holds, OAuth accounts (MO-P) | `members.list`, `accounts.inventory` |
| Code | repositories, image catalog entries, recipes (MO-C) | `repos.ls`, `repos.status`, `image.catalog.ls`, `recipes.ls` |
| Models & keys | models, API keys (MO-K) | `models.list`, `secrets.inventory` |
| Budget | caps and kill state (MO-B) | `budget.show` |

`defineManageKind` refuses a kind whose `group` is not one of the four: the union in the type is a compile-time
error, and the check is what catches a descriptor assembled where the compiler cannot see it. A refusal is a throw
at the point the kind is defined rather than a section missing from the Project tab.

## One kind, one file

A kind is defined under `console/src/renderer/manage/kinds/` and nowhere else -- never in the shell, never in
`types.ts` and never in `entry.ts`. That directory is the kinds' own; the plan's slices assign the files
(`people.ts`, `models.ts`, `keys.ts`, `budget.ts`, `repos.ts`, `images.ts`, `recipes.ts`), and where a slice gives
one file to several kinds of one subject, that file holds them: People & access's three rows are `people.ts`'s, and
Code's repositories are `repos.ts`'s two -- the spec's subject answers it with two reads (`repos.ls` for what each
repository is made of, `repos.status` for the doctor of one repository's enrolment), and a kind names one `source`.
`manage/kinds/index.ts` (task I-MG) lists the files as the kinds in `MANAGE_GROUPS`' order, which is the list the
Project tab mounts.

## What the shell renders

The shell (`manage/shell.ts`, task MO-SH) draws the groups, one section per kind, and **only what a kind decodes**:
it reads no document key by name, knows no column of its own and holds no sentence about a row. A cell is drawn
from `decode`'s answer and passes `sanitizeText` under the class its column declared; a string the kind did not
decode is a string the shell does not draw. Every kind gets its own read, its own busy mark and its own refusal, so
one kind's refusal leaves the others drawn (`.claude/rules/console.md`'s parallel-sections rule).

Reduced mode is the shell's question and not the descriptor's: where a kind's rows carry spend, caps or kill
figures, the drawing asks `hidesView` (`.claude/rules/console.md`, MO-B), because a reduced console withholds a
figure rather than misreading a shape it does not know.

## A Copy-command

`buildCopyCommand` (`console/src/renderer/manage/command.ts`) is the only way a kind puts a command on the
clipboard. It is handed an ordered list of parts, each either the console's own word or a value a document
supplied, and it answers the action or a refusal:

- a **value** must match `[A-Za-z0-9][A-Za-z0-9._/@:=+-]*`, with no `/`-separated `..` segment;
- a **word** the console wrote must match the same alphabet, opening with at most two dashes (`--project=acme`, so
  the console's own flags are words and a document's string never becomes one).

A part outside those alphabets **refuses the whole action** -- it is not quoted, escaped, dropped or repaired, the
same answer the WSL route gives a word with a shell metacharacter (`distroShellUnsafe`,
`.claude/rules/console.md`). The value is refused rather than quoted because quoting is a second implementation of
the shell's own grammar, and a Copy button has a cheaper answer: the values a kind copies are an address, a role, a
repository, an account and a key's name, and a value that is not a plain word is a value this console will not copy.
The row is drawn without its Copy, and the refusal's sentence never quotes the value it refused -- a value the
console will not put in a shell is one it will not put in a sentence either.

The action carries the argv, and the text on the clipboard is that argv joined with single spaces
(`copyCommandText`, the one place the join is spelled). That is
unambiguous exactly because no admitted word can hold a space, a tab or a newline, so no caller quotes anything.
The refusal is an ordinary outcome and never a repair: `;`, `$(`, a backtick, a newline, a space, a leading `-`,
`=` `@` or `~`, and a `..` segment are each a value the console will not copy.

**The action also carries a `consequence`, and it is the console's own sentence rather than a word of the command.**
What a destructive command takes away is what the operator has to read before they paste, and D2909 gives a removal no
confirmation window to say it in: the next step after the Copy is a terminal, so the sentence travels with the
command. `buildCopyCommand` takes it as an argument, so the composition is the console's own and the sentence is not held
to the alphabets above: a sentence is not a word, and the two never mix. That is not an exemption for a **value** inside
it: the one part of a consequence a document supplies -- the repository a removal is about, the allow-list entry that
still admits it -- is read through the `TEXT_CLASSES` class its column declares before it is interpolated (`repos.ts`'s
`repoTextOf`), and a value the class refuses draws no action at all rather than a sentence with the value left out. What
the sentence must state is the CLI's own account of the command (spec section 8), which is why MO-C's two are composed of
phrases copied verbatim out of `internal/cli/project_repos_rm.go` and `project_enroll_converge.go` and held back to those
files by `manage-code.test.ts`, and MO-P's three -- a revoke's, an offboarding's and an account removal's -- out of
`internal/cli/user_grant.go`, `internal/cli/offboard.go` and `internal/cli/accounts_rm.go`, held back by
`manage-people.test.ts`, and it is the empty string for a command with nothing to state. A refused action takes
its consequence with it: nothing is drawn for a command that was refused.

## What is not here

- **A write action.** D2903 admits a console write only where it is neither secret nor destructive and has a
  terminal-free path; the two the plan admits (`models add` as a widening, MW1, and `image add` with `--no-apply`,
  MW2) each arrive with the action of their own, in their own task. The other writes -- `models use`, `models rm`,
  `image rm`, `user grant|revoke`, `secrets set|rm`, `repos add|rm` -- stay Copy-commands (O5).
- **A secret.** No kind draws, holds or copies a key's value: the keys kind copies `secrets set` and `secrets rm`
  (D2905), and no Copy-command carries a value a person has to keep.
- **The CLI's own field metadata.** The spec's optional enhancement (commands describing their own fields) is
  D2908's business and reaches the console through the settings document alone; a Manage kind hand-builds its
  columns, which is what D2903's first pass needs (R10).
