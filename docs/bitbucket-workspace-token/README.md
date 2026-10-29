# Bitbucket workspace and project tokens

**Status: nothing is implemented. T1.1 and T1.2 are APPROVED for 0.12.1 (2026-10-27); no other task is approved.**

The owner ruled D460–D463 on 2026-10-27:

- **D460:** Bitbucket repositories also run in shared project jobs. This was **not the
  recommendation**.
- **D461:** a workspace or project access token, whichever is narrowest.
- **D462:** the reach risk is accepted, with the allow-list, minimum scopes and a plain statement.
- **D463:** 0.12.1 is quick, and anything big goes to 0.13.0.

On the same day the owner also answered:

- **D474:** yes, cloud runs refuse a non-default Bitbucket API URL, except an owner-allow-listed
  root.
- **D475:** yes, launchers are trusted as before.
- **D477:** bump the policy version, which was **not the recommendation**, so 0.13.0 is a lockstep
  upgrade.

In a later round the same day the owner answered the rest:

- **D464:** yes, in 0.12.1, because 50 lines is still a per-repository chore: `*` within one path segment of `allow_data_to`, such as
  `myworkspace/*`. A bare `*`, `*/*` and a wildcard workspace are refused, and there are at most two
  `*` and never a regular expression. This was **not the recommendation**. Run
  20261027-212846-dc73 builds it.
- **D465:** the derived `bitbucket_token` policy block, checked by the runner (the accounts precedent).
- **D466:** yes, `fugaro doctor` warns about an overriding own copy, which is otherwise silent.
- **D467:** documentation in 0.12.1, with no code.
- **D468:** no, there is no sandbox to run it in; the docs carry the "not yet live-checked" label.
- **D473:** yes, so existing installations are unchanged.
- **D476:** no, the trust rule is kept and the risk stated, accepted under D462. This was **not the recommendation**.
- **T2:** Cloud-compatible API roots only, because there is one adapter for one API; Data Center stays out.

No decision is pending. Each task still needs the owner's own explicit go.

**Amended 2026-10-27:** S2 is re-planned against the credentials-service proxy of
[../secrets-brokering/](../secrets-brokering/README.md) (its D493, D505); T1.1 and T1.2 stay
approved for 0.12.1, and T1.1 gains one pointer sentence (its D490). The release table below is
superseded for 0.13.0 by that plan's "The re-planned Bitbucket S2".

**Spec owner:** the Fugaro owner. **Date:** 2026-10-27. **Base:** `main` at `a5f521f1c`.

| Release | Tasks |
| --- | --- |
| **0.12.1** | Run **dc73** (the `allow_data_to` wildcard, D464) first. Then **T1.1** (docs and the `SECURITY.md` reach statement) and **T1.2** (CLI help text, comments and message wording), **both APPROVED 2026-10-27**, which launch after dc73 merges. Per-repository jobs are unchanged. Check 180 (T1.3, T1.4) stays owner-run and comes later: no Premium sandbox workspace is known |
| **0.13.0** | **T2.1–T2.11**: Bitbucket in shared project jobs, the node's derived Bitbucket block enforced by the runner and checked at publish and deploy, the doctor warning, the API-root pin (T2.8, on), the policy version 4 and its lockstep release notes (T2.11), the phase-3 docs and Check 181. None is approved. **T2.10 is off** (D476), and **T3.1 is superseded** by dc73 (D464) |

The headline finding: storing one workspace or project token for many repositories **already
works**. It runs through the `project_secrets.bitbucket-token` allow-list and
`fugaro secrets set bitbucket-token --project-wide`, and `fugaro update --repo` then asks for
nothing. The docs present that path as an exception, so the 0.12.1 slice is docs and text.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's request of 2026-10-27, verbatim |
| [reconciliation.md](reconciliation.md) | What exists (§2); Atlassian's facts (§3); findings F1–F18 (§4); decisions D460–D477, all answered 2026-10-27 (§5); the release split (§6); tensions T1–T2 and open items O1–O7 (§7); UNVERIFIED U1–U8 (§9). **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices S1–S2 (S3 superseded by run dc73); tasks T1.1–T1.4 and T2.1–T2.11 (T2.5 folded into T2.9, T2.10 off, T3.1 superseded), each with its files, tests, red step, command and reviewer tier; levels, critical path, shared-files table, and Checks 180–181. **Only T1.1 and T1.2 are approved** (0.12.1, after dc73 merges) |

## Typos fixed in the spec

None.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D460–D489** | **D460–D477** are written (all ruled 2026-10-27; D469–D472 are resolutions). **D478–D489** are reserved and unused. The block sits above the subscriptions work's D430–D459, on `main` and in every open PR |
| **Checks 180–189** | **180–181** are proposed (owner-run, NOT RUN). **182–189** are reserved and unused. The block sits above Checks 170–179 |
