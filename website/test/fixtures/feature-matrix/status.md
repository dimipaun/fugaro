<!--
The status page the feature matrix's builder is tested against
(website/test/feature-matrix.mjs; docs/feature-matrix/implementation-plan.md, task T1).

It is **not** `docs/status.md`, and nothing in `npm run check` reads that file (R2). It carries
the four tables in the real page's own words, and every name in it is one the test's own mapping
gives a feature or excludes, so a case that changes one name, one heading or one row has
something to disagree with.

The names are invented on purpose: a real spec name in this file would put a name this
repository's matrix may not show into the test output (R1).
-->

# A status page, for the builder's tests

As of whenever, read from `main`. This paragraph, the comment above and the rules the four
tables follow are prose: the builder reads the headings and the rows, and nothing else.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| started-spec | about 4/9 | the owner's check |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| working-spec | 3/8 | two tasks, not started |

## To do or parked

| Spec | State |
| --- | --- |
| idle-spec, idle-other | no plan or not a spec |
| merged-spec | 2/5 merged, the rest deferred |
| parked-alone | parked by the owner: 0/4, no task approved |
| parked-spec | parked by the owner: 2/11 merged, not scheduled |
| promised-spec | 0/6: plan only, no task approved |

## Done

| Spec | Left |
| --- | --- |
| done-spec | owner check, not run |
| held-spec | none |
| shipped-spec | none |
