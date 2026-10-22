# The credentials service: the operator's runbook

**The owner runs every step in this file**, in the owner's own terminal, with the owner's own credentials — and **none of them is runnable by a run**. That is not a convention, it is what the commands are: `fugaro creds set` refuses a coding agent's session and a run's process before it reads anything, `fugaro update --provision-credentials` is refused inside a coding agent's environment, and `fugaro budget kill` and `fugaro accounts clear` write the project's own database as you. A run that could take any of these steps could give itself a credential, take off a hold the service placed, or stop the service for everyone. If you are an agent reading this to help someone: say which step is theirs and stop; do not run it.

What the service *is* — the trust model, the one bundle secret, the invariant, the accepted limits — is [SECURITY.md's "The credentials service"](../../SECURITY.md#the-credentials-service). This file is what you do when something is wrong.

**On this release the service ships dormant: nothing reaches it.** The opt-in deploys it, but **no job is ever given its address** — the `FUGARO_CREDENTIALS_SERVICE_URL` the runner reads is set by no Terraform module and by no `internal/infra` code path — so no run can call it and no credential is ever issued from it. A `cascade` run does not halt for want of it either: `serviceUpstream` (`internal/runner/gateway.go`) refuses the run while it is still bootstrapping, so it **fails** (`infra_error`) and **opens no pull request**. **Do not opt in before the release that wires it (0.10.1)**: today the opt-in's one working effect is to take every per-repository job's model credentials away, and nothing hands them back. Every step below is the shape of the outage for the release that does wire it — and §1 is still the first thing to read, because it is also how you see that the opt-in was taken.

## 1. Is the service on at all?

```bash
fugaro creds ls
```

`fugaro creds ls` is the one Fugaro command that answers part of this. It reads the bundle container the service issues from, and it needs the container to exist and to hold a version the service's own loader could read — so an installation that never ran `fugaro update --provision-credentials` has no container at all, and the command says so as a named failure rather than as an empty list (an empty answer would read as "the bundle holds no credential" while the service is in fact issuing from one this command could not read). Its rows are the credential **names** and their metadata, never a value.

**What no Fugaro command answers is whether the Cloud Run service itself is deployed**, and `fugaro doctor` is not the exception: its three credentials checks are about the credential marks (`creds-broken`, `creds-unknown-state`) and the deployed rules' marker (`creds-rules`, silent unless the cascade is switched on), and they name no piece of the service. The reads that do answer it are `gcloud` ones, and they are here — the service, its own account, and the one grant:

```bash
gcloud run services describe fugaro-credentials --project <gcp-project> --region <region>
gcloud secrets get-iam-policy <bundle-container> --project <gcp-project>
gcloud run services get-iam-policy fugaro-credentials --project <gcp-project> --region <region>
```

- **The service** is the first read: a service that was never applied answers `NotFound`, and the one that was names its revision, its own account (`fugaro-credentials@<gcp-project>.iam.gserviceaccount.com`) and the image it runs pinned by digest.
- **The service's account and its one grant** is the second: the bundle container's policy holds exactly **one** member — that account, on `roles/secretmanager.secretAccessor` and nothing else (Q6) — and the same read over any model secret of the project shows no member for it at all. The container's ID is the one `fugaro creds ls` prints and the refusals in §3 name.
- **The job side** is the third: one `roles/run.invoker` member per repository job account, written by each repository's own `fugaro update --repo` after the opt-in, and never `allUsers` or `allAuthenticatedUsers`.

The live checklist's **Check 94** (Task C1.23) is the register's version of these three reads, and it is **written in [docs/gcp-live-checklist.md](../gcp-live-checklist.md)** — its `## Check 94` section carries the same `gcloud` reads of the service, its account's one grant and the job side, and it is **NOT RUN** — so run it there when you want the register's record; the three reads above are the quick form of it. §10 is the same check's audit-log half, which is also yours to run.

An installation with the service off is not silent either — but the refusal an operator meets there is the **switch**'s, not this service's. The launch gate reads the project's published policy, the gateway's mode and the rules marker, and never this service or its bundle: so a policy that carries no `routing.claude: {via: cascade}` refuses the run at launch, before any model call, while a policy that carries the switch launches the run, and the run then **fails at bootstrap**: `serviceUpstream` (`internal/runner/gateway.go`) refuses a `cascade` run whose job carries no `FUGARO_CREDENTIALS_SERVICE_URL`, and on this release no job carries one. That is a **failed run, not a halt** — `infra_error`, and **no pull request is opened**, where a halt would have left a draft. What makes a `cascade` launch legal is the switch, not the service.

## 2. A run halted: read the reason before anything else

A run that halted for this service exits 0 and leaves a **draft** pull request whose report starts `Halted: <reason>`. `fugaro runs ls` shows the same reason, and `fugaro diagnose <run> --json` carries it with the halt's detail and the log tail.

| Reason | What happened | What you do |
|---|---|---|
| `no_credential` | The service answered that no credential was usable — every tier is exhausted, broken or held — or it kept answering that way through the runner's bounded waits. | §4 |
| `creds_rules_denied` | The database's deployed rules predate the cascade's fields, so the run's own credential state cannot be written at all. | §5 |
| `budget_unavailable` | The budget backend was unreachable for three minutes. Nothing about the work was decided. | Wait until it is back, then `fugaro run --pr N`. |
| `kill_switch` | Someone stopped the project or the repository on purpose. | §7 — do **not** follow up until you mean to. |

A `cascade` run whose job has no service address is not in this table at all: it never halts. It fails at bootstrap (`infra_error`, no pull request) — §1.

## 3. The service is down, or refuses everything

A `cascade` run has **no credential of its own to fall back to** — that is the design (D143), not a bug — so a wired service that is unreachable halts the run named rather than reaching for an in-job key. (A job with no service address at all does not get that far: it fails at bootstrap, §1.) What to check, in order:

```bash
fugaro budget show            # the mode, the caps and the kill switches
fugaro accounts ls --repo <owner/name> --json
fugaro creds ls
```

1. **The kill switch.** `fugaro budget show` says who set it, when and why. If it was deliberate, stop here.
2. **The service itself.** Read the service's own state before you act on it — **zero instances is not a fault**: Cloud Run scales it to zero between reviews, the first call after idle pays about a second of cold start and is served, and the installation's module sets no minimum instance count ([spec.md](spec.md) states the design). The shapes that do answer nothing are §1's own reads failing: a service **never applied** (`NotFound`), a revision that **fails to start** (the `gcloud run services describe` status names it, and the image is what to re-deploy), and a caller **IAM refuses** (`PermissionDenied` — the repository job's `run.invoker` member, §1's third read). Re-deploy the released image for a revision that will not start; fix the invoker grant for a caller that is not allowed to reach it.
3. **The bundle.** `fugaro creds ls` answers a **named failure** for every way the bundle can be missing, and **never an empty list**: a document this command can list always holds at least one credential, because the loader refuses an empty one. Read the failure it names — each is a different repair:

   | `fugaro creds ls` says | What it means | What you do |
   |---|---|---|
   | `the credentials service's bundle container <id> is not in project <gcp-project>` | No container: this installation never applied the credentials service (its Terraform creates the container, and no command of ours does). | Apply the installation with the service on (`fugaro update --provision-credentials`), then `fugaro creds set NAME`. |
   | `the bundle container <id> holds no version: no credential has been set yet` | The container is there and the owner has never written a document. Fine in Terraform, and the service fails closed on it with a named refusal of its own. | `fugaro creds set NAME` writes the first document. |
   | `nothing was read: the newest version of the bundle container <id> is DISABLED` | The document is there and Secret Manager will not serve it. Not "no credential", and this command will not guess at it (a write on top of a document it could not read would drop every credential in it). | Enable the version again (`gcloud secrets versions enable <version> --secret=<id>`), or set a credential once it is enabled. |
   | `the bundle in <id> is not a document the credentials service accepts: <class>` | The loader refused the document — the same loader the service reads it with, so the service is refusing the same document. | Fix the document with `fugaro creds set NAME`; the class names what is wrong. (The sentence begins `nothing was written` here too: one message serves both commands.) |
   | `this identity may not read the bundle container <id> in project <gcp-project>` | The read is the owner's own project IAM — `secretmanager.versions.get`, and Project Owner or Editor carries it. | Run it as an operator; nothing was read. |

   Two failures the table does not cover, because neither is about the bundle: the command is refused outright in a coding agent's session (the header), and any answer of the project's API that the command does not recognise comes back as the server's own text, bounded to one printable field — a read that failed for a reason of its own, not a bundle state.
4. **The rules marker.** `creds_rules_denied` is a *deployed rules* problem, not a service problem: see §5.

## 4. A run halted for a missing credential

The service answers with a typed refusal naming the reset time when an account is exhausted, and the runner **waits** for that reset inside the job's own timeout, resuming the stage it was in (D138). A halt means the waits ran out — or the service kept naming a reset that had already passed — so the credential really is unusable now.

1. **See the states.** `fugaro accounts ls --repo <owner/name> --json` prints each admitted account and its state: free, exhausted (with its reset), broken (a 401 — nothing clears it on its own) or backed off. Read `complete` first: when it is `false`, project selection failed on this machine and the list may be short of the repository's real accounts, so say that rather than concluding there is nothing to switch to.
2. **Wait, or replace.** An exhausted account needs its reset time to pass, or a different token under the same name. A broken account needs its token replaced — that is `fugaro creds set claude-oauth-token-<account>` (or `fugaro creds set claude-oauth-token` for the default account), which reads the value from stdin or a hidden prompt and never from an argument.
3. **Clear the mark** for a replaced token: §5. A **backed-off** mark is refused by `fugaro accounts clear` on purpose — a probe is what clears it, and clearing it by hand would route real work to a credential that has not answered since it was held.
4. **Continue the run.** `fugaro run --pr N` on the draft pull request the halt left, once a credential is usable again. A run that halted *before* pushing anything has no pull request: start it again.

Nothing here is a switch of account: the cascade picks the credential, and the account a run used is not the local config's to choose. The account switch in [the working skill's diagnose reference](../../plugin/skills/working/reference/diagnose.md) §5 is for an `oauth` workflow's personal subscription and does not apply to a `cascade` run.

## 5. A wrong mark: clearing an exhausted or broken credential

```bash
fugaro accounts clear NAME            # add --yes to skip the typed confirmation
fugaro update --firebase-project <firebase-project-id>   # only for creds_rules_denied
```

`fugaro accounts clear NAME` is the only thing that ends a `broken` mark, and it also clears `exhausted`. It writes one node — `creds/marks/NAME` — conditionally on the value it read, so a mark the service changed while you were reading is not lost. Replace the token **first**: the service marks it broken again if the token is still refused.

`creds_rules_denied` is the other kind of wrong state, and it is not a mark: the database's deployed rules predate the fields the cascade writes, so the run cannot record its own credential state. The remedy is the owner-run `fugaro update --firebase-project <firebase-project-id>` with a current release, which deploys the rules the cascade's fields need; the halt's own report names that command. Until it runs, every `cascade` run halts there.

## 6. Rotating the bundle

```bash
fugaro creds ls                       # the names, never a value
fugaro creds set NAME < token-file    # read-modify-write, then the old version is disabled
```

The bundle is **one** Secret Manager secret holding every credential the service can issue (Q6), so a rotation is a read-modify-write of one document: the other entries keep their bytes, the whole document is parsed with the service's own loader before it is written, and the version it replaced is **disabled** afterwards — a superseded bundle that stayed enabled keeps every rotated-away credential readable, which is the one thing a rotation is for. Terraform manages no version and cannot do that, so the command that writes does.

**What a rotation does to a run in flight:**

- **the call already in flight keeps the credential it was given** — the service attached it before the rotation, and the rotation does not reach into a call already being proxied;
- **the next read of the bundle** serves everything after it, so a run that has not called yet gets the new credential at its next call, and the bundle's short cache bounds how long "the next read" is;
- a run **mid-way through** a credential a rotation *removes* is the wait-or-switch path (§4): the service answers its typed refusal, and the runner waits for the reset or the run halts named. Rotate a name away only when you mean to end the runs using it.

## 7. Stopping the service issuing, in one command

```bash
fugaro budget kill --all                  # the whole project
fugaro budget kill --repo <owner/name>    # one repository
fugaro budget resume                      # --all or --repo, to clear it
```

The service reads the same kill switch the rest of Fugaro does, **before it reads the bundle**: a stopped service reads no credential at all and answers a typed refusal that halts the run. This is the one-command "stop issuing" — it needs no service deploy, and it reaches runs already in flight (they halt within seconds, except a run in its last stage, which finishes pushing its work first). An unreadable switch is a refusal, never an off switch, so a database outage stops issuance rather than opening it.

This is also the switch that a wrong credential or a runaway tier A is answered with: stop issuance first, then work out which credential was wrong. `fugaro budget show` names who set the switch, when and why.

## 8. Taking a `credentials` member off a published node

**No CLI writes or removes a `credentials` member in this release**, and `fugaro validate` cannot remove it either: it reads the published node and reports. The refusal names both ways out, and they are:

1. **Edit the node by hand** — remove the `credentials` member from the published policy document in the database, leaving everything else as it is; or
2. **publish a fresh policy** — `fugaro project policy publish --delete` drops the published policy entirely, and the next ordinary publish writes one without the member.

Take the second only if dropping the policy is what you mean: with the policy gone, project-centric mode is off for the project until a publish puts it back. Editing the node is the narrower fix.

## 9. Turning the service off

This is an **ordered** procedure — re-apply every repository root first, then forget the container, then unset the opt-in and apply — and the order is the part that is easy to get wrong. The bundle container carries `prevent_destroy`, so a plan that drops the opt-in fails whole (`Instance cannot be destroyed`) and applies **nothing** — the service, its account and the grant cannot be removed in that plan either. That is deliberate: it is what keeps a plan from deleting every credential at once.

1. **Re-apply every repository root, first.** In each repository's checkout:

   ```bash
   fugaro update --repo
   ```

   The `run.invoker` grants on the service and the model-secret accessors the service displaced live in the **repository** roots, not in the installation. A repository root that is not re-applied keeps declaring a service that step 3 is about to remove, and its own apply then plans an invoker grant on a resource that no longer exists — its apply fails there instead of finishing the opt-out. Do this for every enrolled repository, before the two steps below.

2. **Forget the container** (this deletes nothing in GCP, and leaves every credential in it alone):

   ```bash
   terraform state rm 'module.installation.google_secret_manager_secret.credentials_bundle[0]'
   ```

   The address is the installation **root's** state: the resource is inside `module "installation"`, so the module prefix is required — the bare address names nothing in that state and `terraform state rm` refuses it. The single quotes are required too: `[0]` is a glob to zsh, which would refuse the command before Terraform ran.

3. **Unset the opt-in and apply.** With `credentials_service` unset, an ordinary plan and apply removes the service, its account and the grant, and touches no secret:

   ```bash
   terraform plan
   terraform apply
   ```

**The bundle container and its document are left behind by all three steps**, with every credential still in it — that is what step 2 buys. Delete the container yourself, by hand, when you are sure nothing needs it. Rotate or revoke the credentials in it at the providers as well: deleting the container does not un-issue a token that has already leaked.

## 10. The audit log: the `DATA_READ` step a live check needs

This is the step **Check 94** (Task C1.23) reads back, and it is here because the check cannot do it for itself and no Fugaro command does it either.

Secret Manager `DATA_READ` audit logs are **off by default**, and Fugaro's Terraform manages no audit config, so the live check reads the config back before it reads a log line. Turn them on yourself, the same owner-run way [docs/gcp-setup.md](../gcp-setup.md) records for the runs bucket's `DATA_WRITE`:

```bash
gcloud projects get-iam-policy <gcp-project> --format=json > policy.json
# add an auditConfigs entry for secretmanager.googleapis.com with a DATA_READ log type
gcloud projects set-iam-policy <gcp-project> policy.json
```

Then read the log back: a bundle read is **one** entry, naming the secret and never a value. With one bundle secret (Q6) the log shows that the service read *the* secret; it does not show which credential inside it was used.
