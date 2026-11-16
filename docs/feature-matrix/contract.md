# The feature matrix contract

The shapes the feature matrix is built from, fixed by task C1 of
[implementation-plan.md](implementation-plan.md) and enforced by
`website/src/lib/feature-matrix-model.mjs`. Four tasks are built against these shapes — T1 builds
the snapshot, T2 writes the mapping, T3 renders `FEATURES.md` and T4a renders `/features/` — and
none of them exists yet, so the shapes are frozen here rather than by whatever the first caller
found convenient. Both validators refuse a shape they were not taught, naming the
field: a matrix built from a file that grew a status or a plan the page has no mark for is a page
that ships a row it cannot explain.

## The closed sets

| Set | Values | Where it comes from |
| --- | --- | --- |
| `STATUSES` | `available`, `coming-soon`, `planned` | D2443. status.md's own states (Nearly there, Work in progress, To do or parked, Done) are never exposed: they are progress through a plan, and the matrix is a list of capabilities |
| `PLANS` | `community`, `team`, `enterprise` | D2440. The order is the page's column order and `FEATURES.md`'s table order |

A feature names the **lowest** plan that includes it, and every higher plan is checked as well: a
feature at `team` is in Team and Enterprise and not in Community. The snapshot carries the three
booleans per row, and `validateSnapshot` refuses a row with a gap in them — three empty cells is
not a row a page can draw.

Both shapes carry `version: 1`. Adding a field does not move it; removing or retyping one does, and
a field whose meaning has to change gets a new name instead (CLAUDE.md, "A field never changes
meaning").

## The mapping

Hand-kept (D2441), one file: `docs/feature-matrix/mapping.yaml`, written by T2. C1's code takes the
parsed object, so the YAML reader stays the site's one (`parseYAML`, `src/content.ts` — a second
reader would be a second dialect).

```yaml
version: 1
features:
  - id: bitbucket-support           # the row's public id, a slug of user-language words (R8)
    name: Bitbucket support         # the words a reader reads
    explanation: One or two sentences, in the reader's language, on what it lets them do.
    plan: community                 # the lowest plan that includes it (D2440)
    specs: [bitbucket-credentials]  # every status.md row that contributes to this feature
    hold:                           # optional; a hold only ever holds a feature back to coming-soon (D2444)
      status: coming-soon
      reason: Built, and not shipped on any stream yet.
excluded:
  - spec: in-job-review             # a status.md row that is not a feature
    reason: Internal to the loop; there is nothing here for a reader to buy.
retired: [old-feature]              # ids the matrix has dropped on purpose (R7)
```

| Field | Required | What it is |
| --- | --- | --- |
| `version` | yes | `1` |
| `features` | yes | the rows, in the order the page draws them |
| `features[].id` | yes | the row's public id: lowercase words joined by hyphens, never containing a spec name (R8) |
| `features[].name` | yes | the feature's name in the reader's language |
| `features[].explanation` | yes | one or two sentences on what it lets the reader do |
| `features[].plan` | yes | the lowest plan that includes it, one of `PLANS` |
| `features[].specs` | yes | the status.md names that contribute to this feature; at least one, or the row has no status to derive |
| `features[].hold` | no | `{status, reason}`: a built feature that has not shipped. `status` is `coming-soon` and nothing else (D2444) |
| `excluded` | no | `{spec, reason}` entries: the status.md names that are not features, each with the reason (F6). Absent reads as none |
| `retired` | no | ids the matrix has dropped on purpose, so the regression check does not refuse them (R7). Absent reads as none |

`excluded` and `retired` are optional because their absence is the safe reading in both directions:
nothing excluded means T1 reports every unmapped name rather than passing one through, and nothing
retired means no id is exempt from the regression check.

## The snapshot

What T1 builds, what T3 renders and what the page reads. One JSON object:

```json
{
  "version": 1,
  "as_of": "v0.25.0",
  "rows": [
    {
      "id": "bitbucket-support",
      "name": "Bitbucket support",
      "explanation": "Runs your tasks on Bitbucket Cloud and opens the pull request there.",
      "status": "available",
      "plans": { "community": true, "team": true, "enterprise": true }
    }
  ]
}
```

| Field | Required | What it is |
| --- | --- | --- |
| `version` | yes | `1` |
| `as_of` | yes | the release the matrix was read against, written `vX.Y.Z` |
| `rows` | yes | one entry per feature, in the mapping's order |
| `rows[].id`, `.name`, `.explanation` | yes | carried through from the mapping |
| `rows[].status` | yes | one of `STATUSES` |
| `rows[].plans` | yes | `true`/`false` for each of `PLANS`; a row's true plans are its lowest plan and every plan above it |

## The block, and the `as_of` line

`FEATURES.md` carries a fenced JSON block holding the whole snapshot, under its rendered table
(R5). `snapshotBlock(snapshot)` writes it and `readSnapshotBlock(markdown)` reads it back; the
block is written in a fixed key order, so the file's diff is the snapshot's change and nothing
else. The page's loader reads that block, so the page and the markdown cannot disagree, and the
page never reads status.md at build time (D2446, D2447).

The block is the **only** fenced block the file carries, and its fence is `json`.
`readSnapshotBlock` refuses a document with no such block, one with more than one, an unclosed one
and one whose JSON is not a snapshot of the shape above.

The file also carries the stamp as a line of its own:

```text
as_of: v0.25.0
```

That line is what T5's `scripts/release.sh` greps for — the script has `git`, `grep` and no `node`
(F10), so the line is a fact of the file and not something the script derives. D2449 adds that the
file must also carry a commit since the previous release tag: the stamp alone does not prove a
refresh, because notes are written ahead of their release. The block carries the same version under
`"as_of"`, and it carries no date, which is why `leakProblems` exempts nothing.

## The fixture and the stub

`website/test/fixtures/feature-matrix/snapshot.json` is a snapshot of the shape above, and
`FIXTURE_SPECS` (`website/src/lib/feature-matrix-model.mjs`) is its own spec list. T3's and T4a's
tests render it — the markdown and the page — and hold `leakProblems` over what they rendered
against `FIXTURE_SPECS`. Its five rows cover all three statuses and all three plans, so a renderer's
three columns are exercised, and its words carry no spec name, no status.md section name, no task
id and no date.

The fixture belongs to the contract and not to the stub: I1 deletes
`website/src/lib/feature-matrix-stub.mjs` and its own test when the real builder lands, and the
tests that render the fixture stay. They read it through the model and never import the stub, which
is what keeps the plan's "tests never import the stub" true once the stub is gone.

The stub's `buildSnapshot({statusMarkdown, mapping, previous, asOf})` answers with the fixture and
reads none of its arguments. Its signature is the real one, which is what makes I1's change a
deletion rather than a rewrite:

| Argument | What T1's builder reads it as |
| --- | --- |
| `statusMarkdown` | `docs/status.md`'s text |
| `mapping` | the parsed mapping above |
| `previous` | the snapshot out of the `FEATURES.md` on `main`, or null when there is none (R7) |
| `asOf` | the release being stamped, `vX.Y.Z` |

## The leak check

`leakProblems(text, specNames)` lists what neither output may carry (R1, R8): a spec name out of
`specNames`, a status.md section name, a task id, or a date. An empty list is the pass, and T3's
and T4a's tests are the two callers.

| What it finds | How it is matched |
| --- | --- |
| A spec name | Only a **hyphenated** name, and only as a whole token, so `bitbucket-credentials` is a leak in `bitbucket-credentials` and not in `bitbucket-credentials-extra`. The single-word spec names in status.md (`mobile`, `website`, `portal`, `plans`, ...) are ordinary words and are not matched |
| A status.md section name | `Nearly there`, `Work in progress`, `Parked`. These are the three F13 criterion 3 names; `Done` is a section heading too, and is deliberately not on the list, because "done" is ordinary English and a check that refused it would refuse honest copy |
| A task id | This repository's form: one to three capitals, a number, an optional dotted part and an optional trailing letter (`C1`, `T4a`, `W1.3`, `D2440`) |
| A date | `2026-11-15`, and a spelled month with a year (`November 2026`). The stamp is a version, so nothing is exempted |

The text is read as given. A caller checking built HTML passes the page's own text, because markup
is not copy: an inline `<path d="M34 178">` is a coordinate, not a task id.

## What the validators refuse

Every refusal names the field it was reading, and `validateMapping` and `validateSnapshot` return
the value they checked, so a caller writes `const snapshot = validateSnapshot(JSON.parse(source))`
and reads nothing else.

- An unknown key, anywhere in either shape, naming the key and the path it sits at.
- A missing required field, naming it.
- A status outside `STATUSES`, and a plan outside `PLANS`.
- A hold whose status is not `coming-soon`.
- An `id` that is not a slug of lowercase words joined by hyphens.
- An `as_of` that is not `vX.Y.Z`.
- A `plans` map with a gap in it, or with no plan set at all.
- A value of the wrong type: an object where a list belongs, an empty string where a name belongs,
  a string where one of the three booleans belongs.
