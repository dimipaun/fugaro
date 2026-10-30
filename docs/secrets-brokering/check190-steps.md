# Check 190 — the owner's steps (git fetch and push through a throwaway Cloud Run proxy)

**Status: written by task B0.4 of the secrets-brokering plan; run by the owner, task B0.5. NOT RUN.**
**No agent runs any step of it**, and no agent has: what it measures is a live project and a real
token.

The check is `scripts/live-checks/check190.sh`. The register's section is `docs/gcp-live-checklist.md`,
"Check 190", and that is where the block you paste back is recorded; the script's own header carries
what it settles (the reconciliation's F13, F14, U2, U7 and U8), what it creates, what it costs in wall
clock and what its Restore deletes. Read those before you start. This file is the three commands.

## What you need

- **Installation `fugaro` only.** The project (`fugaro-dev`) and the region (`us-east5`) are
  hard-coded in the script, and `fugaro doctor` must answer that this is the `fugaro-dev`
  installation: the script refuses to run against anything else. Never `belong`, never another
  project.
- A **sandbox** Bitbucket repository you may push a throwaway branch to, and its access token on your
  clipboard. The token is used for one run, is read by `gcloud` out of the file below rather than from
  a command line, is mounted into the throwaway service as a file rather than as an environment
  variable, and is printed nowhere — not in the block, not in a log, not in an error.
- `bash`, `mktemp`, `gcloud`, `git`, `curl`, `awk` and `fugaro` on PATH, `gcloud` authenticated as
  you. Nothing is installed and nothing is fetched.
- An account that can **create a bucket and an Artifact Registry repository and set IAM on both**
  (`storage.buckets.create`, `artifactregistry.repositories.create` and `setIamPolicy` on each): a
  project Owner can. The check makes its own staging bucket, `gs://fugaro-dev-chk190-staging`
  (uniform access, `us-east5`), and its own registry, `fugaro-chk190`, and grants the installation's
  build account (`fugaro-b-*`) `roles/storage.objectViewer` on that bucket and
  `roles/artifactregistry.writer` on that registry, nothing project-wide. The installation's own
  `gs://fugaro-dev_cloudbuild` is not touched. You do not need to read Cloud Logging: the check reads
  the build's status, not its log.
- **Around two hours at the terminal.** The cold-start wait is 35 minutes of it and the slow push is
  held open for an hour; one build and one idle Cloud Run instance are billed to the project while it
  runs.

## The steps

1. **Write the token file, mode `0600`, without putting the token on a command line:**

   ```sh
   umask 077 && pbpaste > ~/check190.token
   ```

   Where there is no `pbpaste`, `umask 077 && cat > ~/check190.token`, paste, then Ctrl-D. The script
   refuses a file that is not `0600` and refuses an empty one, so a step 1 that went wrong stops the
   dry run rather than the real one.

2. **The dry run first.** It creates nothing, builds nothing, deploys nothing and deletes nothing: it
   runs the guards and the baseline reads, then prints every command a real run would run and every
   command its Restore would run. Read that list before you go on.

   ```sh
   CHECK190_REPO=<workspace>/<sandbox repository> CHECK190_TOKEN_FILE=~/check190.token \
     bash scripts/live-checks/check190.sh --dry-run
   ```

3. **The run itself** — the same command without `--dry-run`:

   ```sh
   CHECK190_REPO=<workspace>/<sandbox repository> CHECK190_TOKEN_FILE=~/check190.token \
     bash scripts/live-checks/check190.sh
   ```

   Progress goes to stderr; one `CHECK 190 RESULT` block goes to stdout, at the end. **Paste that
   whole block back to the coordinator** and nothing else: it is counts, sizes and times, and it
   carries no token, no header and no body.

4. **Delete the token file:**

   ```sh
   rm ~/check190.token
   ```

## If it stops before the end

A stop prints the same block first, with an `aborted` fact naming the reason, and exits `1`. The stops
worth knowing by name:

- **`--no-allow-unauthenticated` missing from the deploy line** — the check refuses before anything is
  created, because a throwaway service that answers unauthenticated requests would put the sandbox
  repository's token behind a public URL. Nothing was created; the flag belongs in `DEPLOY_FLAGS`.
- **A public invoker on the service anyway** — the check reads
  `gcloud run services get-iam-policy` back and refuses before it sends a request if the policy names
  `allUsers` or `allAuthenticatedUsers`. A service, repository or secret of the check's own names that
  was **already there** is also a hard stop: this check never adopts, writes into or deletes what it
  did not create.
- **The service answers an unauthenticated request.** A project-level `allUsers` or
  `allAuthenticatedUsers` binding with `roles/run.invoker` does not show up in the service's own
  `get-iam-policy` — an unauthenticated `GET /chk190/health` is the only thing that sees it. A
  **401/403** is the pass; anything else **ends the run there**, before it clones or pushes anything,
  rather than spending the next two hours behind a public URL. The fix is a project-level binding
  someone has to remove, not a flag here.
- **A bucket named `fugaro-dev-chk190-staging` is already there** — a hard stop like the service,
  repository and secret above, before anything is created: the check never adopts one. If it holds
  nothing but an earlier attempt's staged source, delete it (`gcloud storage rm -r
  gs://fugaro-dev-chk190-staging`) and run again.
- **The build fails** (`build`) — the build runs as one of the installation's own build accounts
  (`fugaro-b-*`), from a build config that sets `options.logging: CLOUD_LOGGING_ONLY`, as Fugaro's
  own image builds do, after the `grants` row gave that account its two grants and a 60 s wait for
  them to take effect. The row carries Cloud Build's own failure detail; a grant that had not taken
  effect yet is the likeliest cause, and running again is the fix. Nothing was deployed; the Restore
  deletes what the run made.

## What is left behind

Nothing. The Restore runs from the exit trap too, so a Ctrl-C in the middle still deletes the service,
the image, the Artifact Registry repository, the Secret Manager container, the staging bucket with the
build's source in it (the two grants go with the registry and the bucket) and the throwaway branches
on the sandbox repository — and it asserts four counts read `0` before the
scratch directory goes. If a count is not `0`, the directory is **kept**, with a message saying where
it is and what to delete by hand.

Two of its facts are worth reading even on a clean run, because they are the ones that would otherwise
be believed too easily. `branch-deleted` lists the branches with a **freshly minted** identity token —
the one the slow push used has expired by the time the Restore runs — and it never reads a listing
that fails as "the branch is gone": a branch holding a 33 MiB blob left on the sandbox repository is
the outcome that would actually cost you. `bucket-deleted` **reads the staging bucket back**
rather than assuming the delete worked, and a bucket it cannot read is a failure, not a pass. If
either reads FAIL, the block says what to delete by hand.

**No agent runs any step of it.**
