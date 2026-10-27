# Bitbucket workspace and project tokens

**Status: plan only; nothing is implemented; no task approved.**

The owner ruled D460–D463 on 2026-10-27:

- **D460:** Bitbucket repositories also run in shared project jobs. This was **not the
  recommendation**.
- **D461:** a workspace or project access token, whichever is narrowest.
- **D462:** the reach risk is accepted, with the allow-list and a plain statement.
- **D463:** 0.12.1 is quick, and anything big goes to 0.13.0.

D464–D468 wait for the owner. Each task needs the owner's own explicit go.

**Spec owner:** the Fugaro owner. **Date:** 2026-10-27. **Base:** `main` at `a5f521f1c`.

| Release | Tasks |
| --- | --- |
| **0.12.1** | **T1.1** (docs and the `SECURITY.md` reach statement) and **T1.2** (CLI help and refusal text). Per-repository jobs are unchanged, and there is no behaviour change. With D468 = "before", also **T1.3** and **T1.4** (Check 180, owner-run) |
| **0.13.0** | **T2.1–T2.7**: Bitbucket in shared project jobs, the allow-list check at deploy and at publish, the doctor warning, runner tests, the phase-3 docs and Check 181. **T3.1** (a workspace pattern) only if D464 says so |

The headline finding: storing one workspace or project token for many repositories **already
works**. It runs through the `project_secrets.bitbucket-token` allow-list and
`fugaro secrets set bitbucket-token --project-wide`, and `fugaro update --repo` then asks for
nothing. The docs present that path as an exception, so the 0.12.1 slice is docs and text.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's request of 2026-10-27, verbatim |
| [reconciliation.md](reconciliation.md) | What exists (§2); Atlassian's facts (§3); findings F1–F16 (§4); decisions D460–D473 (§5); the release split (§6); tension T1 and open items O1–O3 (§7); UNVERIFIED U1–U7 (§9). **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices S1–S3; tasks T1.1–T1.4, T2.1–T2.7 and T3.1, each with its files, tests, red step, command and reviewer tier; levels, critical path, shared-files table, and Checks 180–181. **Nothing in it is approved** |

## Typos fixed in the spec

None.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D460–D489** | **D460–D473** are written (D460–D463 ruled; D464–D468 pending; D469–D473 resolutions). **D474–D489** are reserved and unused. The block sits above the subscriptions work's D430–D459, on `main` and in every open PR |
| **Checks 180–189** | **180–181** are proposed (owner-run, NOT RUN). **182–189** are reserved and unused. The block sits above Checks 170–179 |
