# The backlog's format

This folder is the repository's one backlog (rigor modes, the owner's ruling of
2026-10-20: [reconciliation](../rigor-modes/reconciliation.md) D26–D32). It
holds one Markdown file per item, named `<id>-<slug>.md`, beside this document
and the generated [index](README.md).

## The id, and the file name

`<id>` is `YYYYMMDD-NN` — the day the item was filed and a two-digit sequence
that day, allocated by scanning the folder — and `<slug>` is lower-kebab words
from the title.

A date plus a sequence reaches the ruling's point with no allocator: two pull
requests filing an item at once write two differently named files, and the
merge is a union of disjoint files rather than a conflict (D26). The front
matter's `id:` repeats the id, so the name and the field are checked against
each other and an id resolves to a path without a lookup.

The one collision the shape leaves — two branches filing the day's Nth item —
is a **rename of one file**, on the branch that would merge second, and the
folder's pin refuses a duplicate id so it surfaces there rather than on `main`.

## The front matter

Eight fields, each required, between an opening and a closing `---` fence. A
ninth field is a field nobody decided, and is refused.

| Field | What it says |
| --- | --- |
| `id` | the file name's first two segments (D26) |
| `title` | the filer's words, one line |
| `mode` | the **resolved** mode the run ran under; `none` for an item filed by hand |
| `area` | the matched areas' names, **strictest first**, comma-separated; `project` or `repo` for an item about the whole layer |
| `status` | `open`, `closed` or `dropped` — three words, and no fourth |
| `filed-by` | the run's launcher, or the filer's login for a hand-filed item |
| `filed-in` | what filed it: `run:<run id>`, `pr:<number>`, or `hand` for an item a person filed |
| `date` | the record's finish time in UTC, as `YYYY-MM-DD` — **never** the clock of whoever regenerates the index |

The mode and the area are recorded as the **resolved** values rather than as
the configuration's own words, so an item is readable without the config that
produced it — the same reason the run record carries the resolved mode (D27).

`filed-in`'s third value is the one word D27 does not name: it names the two
shapes a **run** produces (`run:<run id>` and the `pr:<number>` of the pull
request the run opened), and an item a person files has neither behind it.
`hand` is that state said out loud rather than a run id invented for it.

## The body

Four headings. The **first two are required**, so an item cannot be a title
with no text; a fifth heading is a shape nobody decided, and is refused.

```markdown
## What was left open

## Why

## What done looks like

## Links
```

## Closing an item

The pull request that fixes an item flips `status` to `closed` **in the same
commit**, names the item id in its body, and regenerates the index in that same
commit (D31). The regenerate-and-compare pin is what makes that a rule the tree
holds rather than a sentence here: an item flipped without the index being
regenerated is red.

- A closed item's file is **never deleted**. Its id stays resolvable forever,
  because older pull requests cite it, and it carries a `## Closed by` line
  naming the pull request that closed it.
- An item that is dropped rather than fixed sets `status: dropped` and records
  its reason in the body.
- A run may not close an item it did not fix. That is a review rule rather than
  a rule a test can hold, stated here so a reviewer can ask.

## The index

[README.md](README.md) is generated from the item files by
`fugaro backlog index`. Do not edit it: regenerate it. Two branches that
regenerated it at once are reconciled by re-running the command, never by
merging the file (D28).
