# The console dependency graph — the documents

**Status: plan only; nothing is implemented; no task approved.**

**Spec owner: Dimi Paun. Date: 2026-10-22.** Checked against `origin/main` at `cc50b5b23`.

The owner's spec for a dependency-graph view in the Fugaro console — release boxes per stream,
animated node states, a critical path, drag between releases — with its "Independent Release
Streams" addendum, reconciled against the repository and planned.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, **verbatim**, committed alone in its own first commit. |
| [reconciliation.md](reconciliation.md) | Findings **F1–F38** (Wrong / Exists / Gap / Rule / Open), decisions **GD1–GD8** with the owner's answers of 2026-10-22, the tensions (a)–(f) with the owner's rulings of 2026-10-22, the smallest first slice, security probes, UNVERIFIED **U1–U7** (U1–U3 resolved in review round 1). **Where it disagrees with `spec.md`, it wins.** |
| [implementation-plan.md](implementation-plan.md) | Tasks **GV0.1–GV4.1** in five slices, parallel levels L0–L5, the critical path, the shared-files table; what it supersedes in console v2 and amends in the coordinator plan. Nothing in it is approved. |

## Typos fixed in `spec.md`

None. The file is byte-identical to the text received.

## Numbers this work holds

- Decisions **GD1–GD8** (this folder's own prefix, GD8). Console v2's D21–D38 are not reused.
- Findings **F1–F38**; tasks **GV0.1–GV4.1**.
- Check **123** (the highest in use on `main` and in open pull requests was 122).

## Answers that were not the recommendation

**GD1** (wait for the coordinator's project record, 0.11.0), **GD2** (the console writes the
roadmap; reverses console v2 D30 and amends D22) and **GD3** (elkjs; reverses the v1 console design's D9).
GD7's file path was amended after reading the code (`docs/release-streams.yaml`; F29) and accepted
by the owner.

## The owner's rulings on the tensions (2026-10-22)

All six as recommended (reconciliation §4): (a) read-only graph until 0.11.0, drag with the
coordinator's record; (b) the amendments to D9, D22, D29, D30 and console-styling §3.9 confirmed;
(c) elkjs confirmed under EPL-2.0, pinned to 0.11.0, licence text shipped, file unmodified; (d)
per-plan task graphs now, release assignment later; (e) `docs/release-streams.yaml` accepted; (f)
proposed for coordinator P4 only.

## Waiting on the owner

No decision or tension is open. Each task still needs the owner's explicit go; GV3.1, GV3.2 and
GV4.1 also wait on the coordinator's record (0.11.0).
