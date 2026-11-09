# Secrets and credential brokering

**Status: S0 is complete — every task of it, B0.6 (Check 190's run record) included — and every inert
piece of S1 is on `main` beside the proxy chain's first two tasks — B1.5's service route and B1.6's
loopback forwarder, which landed after the census below was read; the chain's runner half (B1.7) and
the two live checks (B1.11), which are **written and not run**, are too, while the docs (B1.10) and
the flip (B1.13 part two) are not, and no task is approved** — [status-2026-11-07.md](status-2026-11-07.md) reads
every task of the plan, one row each, against the merged tree, and says which can launch now. The
owner answered D490–D506 on 2026-10-27: all as recommended except **D501** (the service keeps scaling
to zero: not the recommendation;
D502, staying opt-in, was relayed with it), and **D494**, which follows the recommendation and
**departs from the owner's spec** (no RTDB token store). **D497a is amended after review** (the
claim also names the run's pull requests) and waits for the owner's confirmation (O5). Open items
O1–O8 wait for the owner. Each task needs the
owner's own explicit go.

**Spec owner:** the Fugaro owner. **Date:** 2026-10-27. **Base:** `main` at `db4c5f390`, merged
forward before the PR.

| Release | What |
| --- | --- |
| **0.12.1** | Nothing of this work except one pointer sentence in docs/bitbucket-workspace-token's approved T1.1 |
| **0.13.0** | **S0:** the secret-reader audit as a tested doc, the CI guard, doctor's Secret Manager sweep and deployer read, Check 190 (owner-run) |
| **0.14.0** | **S1, data and inert pieces only:** policy version 4 with the `bitbucket_token` block and the `proxy` switch, the bundle's Bitbucket token, the git proxy's parser and rules, the flip list still empty, the API-root pin, `fugaro creds set bitbucket-token` and the operator draft (B1.13 part one); the Bitbucket proxy itself arrives in 0.15.0, so no proxied run |
| **0.15.0** | The Bitbucket proxy for per-repository jobs of opted-in installations (B1.5–B1.7, B1.9, B1.10, B1.11) and Check 191 (owner-run) on the release that carries the proxy — **0.21.0**, the first tag holding `internal/credsvc/bitbucketproxy.go`, so the steps name that release and not this one |
| **after 0.15.0** | The flip (B1.13 part two, the job gives up the token) and Check 192 (O7) |
| later | S2 model modes, S3 vault fetch, S4 builds, S5 shared jobs |

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, "Fugaro Secrets and Credential Brokering" (2026-10-27), verbatim. The customer it names is replaced by a placeholder |
| [reconciliation.md](reconciliation.md) | What exists (§2); who reads Secret Manager today (§3); findings F1–F28 (§4); decisions D490–D506 with the owner's answers and resolutions R1–R4 (§5); 0.12.1 meanwhile (§6); tensions T1–T6 as the rulings handle them and open items O1–O8 (§7); UNVERIFIED U1–U8 (§9). **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices S0–S5; tasks B0.1–B0.6, B1.0 (conditional) and B1.1–B1.16 with files, tests, red steps, commands and reviewer tiers; the re-planned Bitbucket S2; levels, critical path, shared files; Checks 190–192. **Nothing in it is approved** |
| [secret-readers.md](secret-readers.md) | The S0 audit (task B0.1, as a tested doc): one row per reader of a Secret Manager value today, each carrying the decision that keeps it reading and the slice that ends it. Its `E<number>` IDs are the key B0.2's guard lists in its own exceptions table, so a hit with no row and a row with no hit both fail the build |
| [status-2026-11-07.md](status-2026-11-07.md) | The census of 2026-11-07 (the owner's 0.23.0 scope names this work): every task of the plan, DONE, PARTIAL, NOT STARTED or OWNER-RUN, with the merged commit and the test that proves it, what blocks it, and the tasks that can launch now. **It approves no task** |
| [check190-steps.md](check190-steps.md) | The owner's own steps for Check 190 (written by task B0.4, run by the owner in task B0.5): the token file at mode `0600`, the `--dry-run`, the run, the block to paste back and the `rm`. The script is `scripts/live-checks/check190.sh` and the register's section is `docs/gcp-live-checklist.md`, "Check 190" |
| [check191-steps.md](check191-steps.md) | The owner's own steps for Checks 191 and 192 (written by task B1.11, run by the owner in task B1.12 and after the flip): the `--dry-run`, the run, the `--after-flip` run, the block to paste back and what each stop by name means. One script, `scripts/live-checks/check191.sh`, and the register's sections are `docs/gcp-live-checklist.md`, "Check 191" and "Check 192" |

## Typos fixed in the spec

None.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D490–D519** | D490–D506 written and answered 2026-10-27; D507–D519 reserved and unused. Above the Bitbucket workspace-token work's D460–D489, on `main` and in every open PR |
| **Checks 190–199** | 190–192 assigned by the plan (owner-run): 190 is written by B0.4 and **was run by the owner on 2026-10-30 (B0.5) — the >32 MiB push failed with a 413 — and its result is recorded in the register (B0.6)**; 191–192 are **written** by B1.11 (`scripts/live-checks/check191.sh`, one script, `--after-flip` for 192) and **not run** — 191 runs on the released 0.21.0 — the first tag that carries the proxy — and 192 after the flip; 193–199 reserved and unused. Above Checks 180–189 |

## Related

- [../credentials-service/](../credentials-service/README.md): the service this spec extends
  (D130–D154, Q6).
- [../bitbucket-workspace-token/](../bitbucket-workspace-token/README.md): D460–D477; its S2 is
  re-planned here (D493).
- [../bitbucket-credentials/](../bitbucket-credentials/README.md): D363, whose parked option (d),
  a push and pull-request proxy, is this spec's Bitbucket half.
