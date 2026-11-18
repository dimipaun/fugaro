# Workspace projects and users

**Status: plan only; nothing is implemented; no task approved.** See [docs/status.md](../status.md)
for the current table. Spec owner: Dimi Paun (the owner). Date: 2026-11-18. Checked against
`origin/main` at `76ce3d6b9`.

The owner's addendum to the workspaces spec: commands to manage the projects in a workspace, and a
model for its users and their roles, applied to GCP IAM. It extends
[the workspaces design](../workspaces/README.md) and [roles and scopes](../roles-scopes/README.md),
and it **supersedes the workspaces design's rule that a workspace never widens a member** for people
and roles (D2800), which is why its first task is a threat model under an independent security
review.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The addendum as received, byte for byte (no typos fixed) |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A7, findings F1–F22, decisions D2800–D2819 with the owner's answers, open items, UNVERIFIED U1–U6. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks WU0–WU21 (WU14 dropped, WU19 unused), contracts C1 and C2, integration I1; levels, critical path, shared files, the edges broken and kept; Checks 720–721 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D2800–D2829** (D2800–D2819 used, D2816 unused), checks
**720–729** (720 and 721 used). Read on 2026-11-18: the highest decision on `main` and in the open
pull requests was D2769, the highest check 709.

**Owner answers of 2026-11-18:** every decision as recommended. D2813 and D2817 were amended the same day
because Realtime Database IAM cannot scope a grant to one path (reconciliation F11, O3); review round 1
amended D2801, D2802, D2811 and D2813 and added D2818 and D2819 (O5), each answered as recommended.

**What a reader should not take from here:** no task is approved, and each needs the owner's go by
name; WU0's independent security review gates every other task; WU15 waits on periodic-reviews R9
(#3140); an existing workspace gets people management only when each member owner runs
`fugaro workspace project link` (D2819).
