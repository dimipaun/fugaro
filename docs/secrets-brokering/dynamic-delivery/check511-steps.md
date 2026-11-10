# Check 511 — the owner's steps (a request every N seconds keeps one instance up, U4)

**Status: written by task DS0.2 of the dynamic-delivery plan; run by the owner, task DS0.3. It has not
been run: **NOT RUN.** No agent runs any step of it**, and no agent may: what it measures is a live
Cloud Run service's own instance behaviour, which nothing in this tree can answer.

The check is `scripts/live-checks/check511.sh` (its sibling test is
`scripts/live-checks/check511_test.sh`, which drives it against fake tools only and never a real
project). The register's section is `docs/gcp-live-checklist.md`, "Check 511", and that is where the
block you paste back is recorded. What the check settles is in the plan
([secrets-brokering/dynamic-delivery/implementation-plan.md](implementation-plan.md), its DS0.2 and
DS0.3) and in the check's own header — which also carries what it makes, what it costs and what its
restore deletes. Read the header before you start; this file is the commands.

## What the check answers

**U4:** does a request every N seconds keep **one** service instance up between runs? D1934 answers
U4's cold start with a keep-warm heartbeat from the runner, and its interval is the one the runner's
budget session already keeps: 15 seconds (`internal/budget/session.go`'s `HeartbeatEvery`). DS1.5 is
written against that answer, so a heartbeat that does **not** keep an instance up returns to the owner
before that task.

The premise is D501 and it is a row of its own: the credentials service (`fugaro-credentials`) has
`min_instance_count = 0` in the project, so an instance that survives the window survives because
requests keep arriving and not because the configuration holds it. If the platform reports a minimum
above zero, the check says so and reads its own warmth as the **configuration's**, not the beat's.

## What you need

- **Installation `fugaro` only.** The project (`fugaro-dev`) and the region (`us-east5`) are hard-coded
  in the script, and `fugaro doctor --project fugaro --all` must answer that this is the `fugaro-dev`
  installation, or the script stops before it makes a call. Never `belong`, and an account naming
  `belong` is refused by name.
- `bash`, `mktemp`, `python3`, `curl`, `gcloud` and `fugaro` on PATH. `gcloud` must be authenticated as
  an account that holds `roles/run.invoker` on the service (the installation's own operators do) and
  may read the project's logs (`roles/logging.viewer`) — the instance ids row is read from Cloud
  Logging, and `CHECK511_INSTANCE_IDS=0` turns that row off for a reader who cannot read them.
- **Five minutes of wall clock** by default (`CHECK511_HOLD_SECONDS`, 300) and **nothing billable**: no
  job, no build, no container, no binding and no account. The requests go to a route the service does
  not serve, so its container answers 404 itself — no credential is read, no model is called and no
  budget is spent. **Nothing is created in the cloud**, which is why the check's `restore` row says
  exactly that.

## The steps

1. **The dry run first.** It sends nothing to the service and writes nothing: it runs the guards — and
   `fugaro doctor --project fugaro --all` is a network **read** of the installation (its config, policy
   and billing, and the update check), so a dry run offline stops at the guard rather than printing a
   plan — then prints the calls and rows a real run would make.

   ```sh
   bash scripts/live-checks/check511.sh --dry-run
   ```

2. **The check itself**, the same command without `--dry-run`. Leave the machine alone for the length
   of the window — a person using the installation sends other requests to the same service, and they
   are traffic the beat did not make:

   ```sh
   bash scripts/live-checks/check511.sh
   ```

   Progress goes to stderr, one line per beat; one `CHECK 511 RESULT` block goes to stdout at the end.
   **Paste that whole block back** and nothing else: it is verdicts and short words, and it carries no
   token, no credential value and no log body.

## What the block's rows are

`service` and `scale-to-zero` are the premise and are read from the platform's own service document.
`container-answers` is the opening request: the unserved route is answered by **the container itself**
(404 with its own route list), which is how the check tells an instance serving from the front end
refusing — an answer that is not that one stops the run rather than being read as an instance.
`idle-cost` is that opening request's latency, a reading and not a criterion: on an installation nobody
is using it is usually the one that pays a cold start. `heartbeat-answered` and `heartbeat-warm` are
the window itself: every request answered by the container, and none of them paying a cold start.
`instance-ids` is the same window read from Cloud Logging — **one** instance id across the run's own
entries is U4's reading rather than only its timing. `idle-gap` is the contrast, and it is not asked
for by default (`CHECK511_IDLE_GAP_SECONDS=0`).

**Exit codes.** `0`: every criterion that was measured held. `1`: a measured criterion did not hold —
a cold start inside the window, a request the container did not answer, or two instances named.
`2`: the check could not measure — a tool is missing, the service or its logs could not be read, the
token was refused, or a row has no reading (the log had not caught up, the entries carry no instance
id). A key the run never reached reads `FAIL — not reached`.

## What is left behind

Nothing. The check creates no cloud resource, and the `restore` row says so rather than leaving you to
assume it. The only trace is the platform's own request log entries for the requests the check made,
which are not the check's to delete — the row names them so that a reader who looks finds them.

## After it has run

Record the block in `docs/gcp-live-checklist.md`'s "Check 511" section and change the row's
`OWNER-RUN, NOT RUN.` to what the run found. **A failed 511 stops DS1.5 and returns to the owner as a
decision:** the heartbeat's interval is what that task would otherwise assume.
