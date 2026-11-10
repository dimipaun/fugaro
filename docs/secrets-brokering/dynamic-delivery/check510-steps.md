# Check 510 — the owner's steps (the name-prefix IAM condition, U3 and F30)

**Status: written by task DS0.2 of the dynamic-delivery plan; run by the owner, task DS0.3. It has not
been run: **NOT RUN.** No agent runs any step of it**, and no agent may: what it measures is a live
project's IAM and Secret Manager, and the answers are the ones DS2.2's Terraform is written against.

The check is `scripts/live-checks/check510.sh` (its sibling test is
`scripts/live-checks/check510_test.sh`, which drives it against fake tools only and never a real
project). The register's section is `docs/gcp-live-checklist.md`, "Check 510", and that is where the
block you paste back is recorded. What the check settles is in the plan
([secrets-brokering/dynamic-delivery/implementation-plan.md](implementation-plan.md), its DS0.2 and
DS0.3) and in the check's own header — which also carries what it creates, what it costs and what its
restore deletes. Read the header before you start; this file is the commands.

## What the check answers

- **U3:** does an IAM condition on `resource.name` apply to `secretmanager.versions.add` and
  `secretmanager.secrets.delete` at all? If it does not, the prefix-conditioned grants the intake and
  the credentials service carry are not the fence R3 and D1931 say they are.
- Does **one** condition naming both prefixes (`fugaro_u_` and `fugaro_p_`) admit each of them and
  refuse a name outside them? The refusal is measured against F6's collision class — a repository's own
  mounted secret, `fugaro-<slug>-<logical>-<hash>` — and not against a name the check invented.
- **F30:** `secretmanager.secrets.create` and `secretmanager.secrets.list` are checked on the
  **project**, so a conditioned grant of either admits nothing, not even the prefix it names. The
  intake therefore holds both unconditioned, and an unconditioned create of a name **outside** the
  prefixes then succeeds: that is part of the reading, not a surprise.
- The **maximum secret ID length** (R1), read by bisection rather than assumed.

## What you need

- **Installation `fugaro` only.** The project (`fugaro-dev`) and the region (`us-east5`) are hard-coded
  in the script, and `fugaro doctor --project fugaro --all` must answer that this is the `fugaro-dev`
  installation, or the script stops before it makes a call. Never `belong`, and an account naming
  `belong` is refused by name.
- `bash`, `mktemp`, `curl`, `gcloud` and `fugaro` on PATH. `gcloud` must be authenticated as an account
  that is an **Owner** of the sandbox project: the check creates one throwaway service account, grants
  it project IAM, and creates and deletes the probe containers its rows are read on.
- **Nothing billable and no model call.** The probe payload is filler; the cost is wall clock and a few
  API calls. The IAM waits are the slow part and the default budget is 90 seconds
  (`CHECK510_IAM_WAIT_SECONDS`).

## The steps

1. **The dry run first.** It writes nothing and calls nothing in the project: it runs the guards — and
   `fugaro doctor --project fugaro --all` is a network **read** of the installation (its config, policy
   and billing, and the update check), so a dry run offline stops at the guard rather than printing a
   plan — then prints every call a real run would make and every row the block would carry.

   ```sh
   bash scripts/live-checks/check510.sh --dry-run
   ```

2. **The check itself**, the same command without `--dry-run`:

   ```sh
   bash scripts/live-checks/check510.sh
   ```

   Progress goes to stderr; one `CHECK 510 RESULT` block goes to stdout at the end. **Paste that whole
   block back** and nothing else: it is verdicts and short words, and it carries no credential value,
   no access token and no payload.

## What the block's rows are

`test-account` and `bindings` are the check's own setup (the throwaway account `chk510-<stamp>` and the
two conditioned grants on the project), read back before anything is measured: if the binding is not
visible, the rows that depend on it read `FAIL` and the check ends `2` rather than reporting a refusal
it never earned. Then the two prefix admits (`condition-version-add-allow`,
`condition-prefix-allow`), the two refusals against the `fugaro-<slug>-…` shape
(`condition-version-add-deny`, `condition-delete-deny`), the delete admit
(`condition-delete-allow`), the two create rows (`create-conditioned` — F30's reading — and
`create-unconditioned`), `list-condition-admits-nothing`, `secret-id-length`, and the `restore` and
`scratch-removed` rows. A key the run never reached reads `FAIL — not reached`.

**Exit codes.** `0`: every criterion that was measured held. `1`: a measured criterion did not hold.
`2`: the check could not measure — a tool is missing, a probe of its own could not be set up, or the
fresh binding never became visible in the time allowed.

## What is left behind

Nothing, and the `restore` row is that reading: every binding the check wrote, the service account it
made and every container it created — the five probes plus the containers the length bisection makes,
about a dozen — are deleted **and read back**, by the owner's own credentials and never by the
conditioned account. It runs from the exit trap, so a Ctrl-C in the middle still deletes them. A
`restore: FAIL` names what to delete by hand.

## After it has run

Record the block in `docs/gcp-live-checklist.md`'s "Check 510" section and change the row's
`OWNER-RUN, NOT RUN.` to what the run found. **A failed 510 stops DS2.2 and returns to the owner as a
decision** — the condition shape is that task's whole content.
