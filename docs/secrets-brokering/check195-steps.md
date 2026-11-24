# Check 195 — the owner's steps (a Cloud Build step calls an IAM-only Cloud Run service)

**Status: written by task B4.1 of the secrets-brokering plan; run by the owner, task B4.2. NOT RUN.**
No agent runs any step of it, and no agent has: what it measures is a live project and a real ID token.
A **FAIL stops S4** and comes back to the owner as a decision — the reading is what B4.3's `VerifyBuild`
and B4.4's Terraform grant are built on, so a service that does not admit a build account's own token
is a design answer, not a bug to work around.

The check is `scripts/live-checks/check195.sh`. The register's section is `docs/gcp-live-checklist.md`,
"Check 195", and that is where the block you paste back is recorded; the script's own header carries
what it settles (the reconciliation's **U4** and **U10**), what it creates, what it costs and what its
Restore deletes. Read those before you start. This file is the two commands.

## What you need

- **Installation `fugaro` only.** The project (`fugaro-dev`) and the region (`us-east5`) are
  hard-coded in the script, and `fugaro doctor --project fugaro` must answer that this is the
  `fugaro-dev` installation: the script refuses to run against anything else. Never `belong`, never
  another project, and no knob can point it anywhere else — the two names it creates are constants in
  the file.
- `bash`, `mktemp`, `gcloud`, `curl` and `fugaro` on PATH, `gcloud` authenticated as you. Nothing is
  installed and nothing is fetched.
- An account that can **create a Cloud Run service, create a service account, set IAM on the service
  and add one project-level binding**, submit a build, and read the build's own lines out of Cloud
  Logging: a project Owner can.
- **Nothing else. No token, no secret, no repository, no file to prepare.** The only credential in the
  whole path is the ID token the build's own step mints for itself, out of the build's own metadata
  server, and uses where it mints it. This check mints no credential of its own and asks you for
  none — which is exactly what it is there to settle.
- **Minutes at the terminal**: one build (a couple of minutes), one deploy and one short call. The
  service scales to zero and the run deletes it.

## The steps

1. **The dry run first.** It prints every command a real run would run and every command its Restore
   would run, and it **calls nothing at all** — no `gcloud`, no `fugaro`, no `curl`. Read the plan
   before you go on; it is also the mode to try if you are not sure your shell is the right one.

   ```sh
   bash scripts/live-checks/check195.sh --dry-run
   ```

2. **The run itself** — the same command without `--dry-run`:

   ```sh
   bash scripts/live-checks/check195.sh
   ```

   Progress goes to stderr; one `CHECK 195 RESULT` block goes to stdout, at the end. **Paste that
   whole block back to the coordinator** and nothing else: it carries the status the service answered,
   the token's `email` claim (an address, which is a name and not a credential), the names of what was
   created and deleted, and no token.

## If it stops before the end

A stop prints the same block first, with an `aborted` fact naming the reason, and exits `1`. The stops
worth knowing by name:

- **`--no-allow-unauthenticated` missing from the deploy line** — refused before anything is created:
  a throwaway service that answered unauthenticated calls would make a 200 in the build's step prove
  nothing about the grant. Nothing was created; the flag belongs in `DEPLOY_FLAGS`.
- **A public member in the service's own IAM policy** (`iam-policy`) — `gcloud run services
  get-iam-policy` is read **before** anything is called, and `allUsers` or `allAuthenticatedUsers` in
  it stops the run. Nothing was called; the service is deleted as usual.
- **The service answers an unauthenticated call** (`unauthenticated-request`) — a project-level
  `allUsers` or `allAuthenticatedUsers` binding with `roles/run.invoker` does **not** show up in the
  service's own `get-iam-policy`, so one unauthenticated `GET` is the only thing that sees it. A
  **401/403** is the pass; anything else **ends the run there**, before the throwaway account is
  created and before any token is sent. The fix is a project-level binding someone has to remove, not
  a flag here.
- **A service or an account of the check's own names already there** (`fugaro-chk195`,
  `fugaro-chk195-caller`) — a hard stop before anything is created: the check never adopts, grants on
  or deletes what it did not create. Look at it, delete it yourself if you know it is a leftover of an
  earlier attempt, and run again.
- **A list that cannot be read** — the two existence reads above are filtered **lists**, and a list
  that failed (an expired session, a transient API error, a permission your account does not hold) is
  a stop of its own, never the answer "it is not there": read that way, the check would go on over a
  service or an account it did not create and then **delete** it. The stop names the exact list to read
  by hand.
- **The project's own IAM policy already names the account** — the third leftover, and the only one the
  two lists cannot see: a binding on the project at the check's account address whose account is gone
  (a `deleted:serviceAccount:…` member, usually a leftover of an earlier attempt whose removal failed).
  It stops the run before anything is created — this check adds a binding at that same address and then
  reads the policy back to prove it removed it again, and once its account is deleted that leftover can
  never be removed — and the stop prints the exact `remove-iam-policy-binding` to run by hand. A policy
  read that **failed** stops the run too, for the same reason a failed list does.
- **The grant does not land** (`invoker-grant`) — the service's own policy is read back after the
  binding and must name the throwaway account and nobody else. A binding that did not land is a stop
  rather than a 403 the block would report as the account having been refused, which is a different
  fact.
- **The build fails** (`build`) — the row carries Cloud Build's own failure detail, the step's own
  exit code out of the build's record, and the build's own last lines, redacted. The likeliest causes,
  in order: your account lacks `iam.serviceAccounts.actAs` on the throwaway account (so `gcloud builds
  submit` was refused before the build existed), the Cloud Build API is not enabled in `fugaro-dev`, or
  this gcloud has no `--no-source` flag. The account and the service are deleted either way.
- **The call is refused** (`call`, the reading the check exists for) — the step printed the status it
  got and the email claim it was refused with. A **403** is Cloud Run refusing that caller on that
  service: the grant does not admit a build account's own token, which is the answer S4 rests on, so
  report it rather than working around it. A **401** is the front end refusing the token itself.
  Read the step's own lines with `gcloud builds log <build id> --project=fugaro-dev`.
- **The logWriter grant does not land** (`logwriter-wait`) — the row says what the project's own
  policy read back, and how long it was waited for. The wait is **bounded** (120 s, plus a short fixed
  settle) because IAM's propagation is not something the check can wait out indefinitely; the build
  runs either way, and a FAIL here means the build may write **no log at all**. Check the binding by
  hand with `gcloud projects get-iam-policy fugaro-dev --format=json`.
- **The build's own line cannot be read** (`call`) — the block does **not** give up here: the step ends
  with one exit code per outcome, Cloud Build writes that code into the build's own record
  (`gcloud builds describe <build id> --project=fugaro-dev` → `steps[0].exitCode`, which is not Cloud
  Logging at all), and the row names the outcome from it — 0 (the service answered 200 *and* the
  token's email claim is the throwaway account), 11 (401), 12 (403), 13 (any other status or none at
  all), 14 (the metadata server minted no token), 15 (the claim is another identity). Code `0` is the
  one code that record does **not** carry: `exitCode` is a proto3 `int64` with no presence, so a zero
  is omitted and a step that exited 0 and a step that never ran leave the same empty column — the
  check reads that `0` from the build's own `SUCCESS` instead (this build's only step is this one),
  and the row says which of the two readings it is. What the line
  adds is the email claim as **text**: read it by hand with
  `gcloud builds log <build id> --project=fugaro-dev`, where `<build id>` is the row's own id. Your
  account needs `roles/logging.viewer` on the project, and the line may simply not have arrived yet —
  in the owner's run of 2026-11-24 it never did.
- **A step exit code the check's own step never writes** (`call`) — a **failure**, and the row says
  the cause is not named rather than guessing at one: the step has one `exit` per outcome and nothing
  else in it exits, so a 1 is a step that died before it decided anything (a container that never
  started, a shell that could not run the script). Read the build with
  `gcloud builds describe <build id> --project=fugaro-dev` and its lines with
  `gcloud builds log <build id> --project=fugaro-dev`.
- **A delete that failed** (`service-deleted`, `account-deleted`, `binding-removed`, `restore`) —
  every one of the three is **read back**, and a FAILed row names what is left, what the last refusal
  said and the exact command to remove it. The project-level binding is removed **before** the account
  on purpose: a binding that outlives its member reads back as `deleted:serviceAccount:…` and can
  never be removed again. Those two writes are ordered and IAM's consistency can still put them out of
  order, so the account deletion re-reads the policy and is **retried** (up to six times). The owner's
  run of 2026-11-24 came back with the account still there, and **what its refusal said was not
  captured**: either cause fits, a delete refused as *still referenced by a policy binding* in the
  moment after the binding went, or a delete that landed whose account the listing went on naming. The
  re-read before each attempt and the read-back after it answer both. A build that FAILED changes
  nothing about any of it: the account goes either way.

## What is left behind

Nothing but the **build's own record**, in the project's Cloud Build history: it is the project's log
of what ran rather than a resource this check created, and nothing here deletes it — `gcloud builds
list --project=fugaro-dev` shows it. Everything the check makes is deleted by its Restore, which runs
from the exit trap too, so a Ctrl-C in the middle still deletes the service (and the invoker binding
with it), the project-level `roles/logging.logWriter` binding and the throwaway account — and each of
the three is read back. The scratch directory goes at the end, and its row says whether it did.

The `roles/logging.logWriter` binding is the one grant this check makes beyond the reading itself, and
it is there because a build that names its own service account must say where its logs go
(`options.logging: CLOUD_LOGGING_ONLY`, as Fugaro's own image builds do) and writes them with **that**
account's credentials — without it the build still runs, but the line carrying the token's email claim
is never written anywhere the check can read it.

**No agent runs any step of it.**
