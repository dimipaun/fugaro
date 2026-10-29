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
- `bash`, `gcloud`, `git`, `curl` and `awk` on PATH, `gcloud` authenticated as you. Nothing is
  installed and nothing is fetched.
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

A stop prints the same block first, with an `aborted` fact naming the reason, and exits `1`. The two
stops worth knowing by name:

- **`--no-allow-unauthenticated` missing from the deploy line** — the check refuses before anything is
  created, because a throwaway service that answers unauthenticated requests would put the sandbox
  repository's token behind a public URL. Nothing was created; the flag belongs in `DEPLOY_FLAGS`.
- **A public invoker on the service anyway** — the check reads
  `gcloud run services get-iam-policy` back and refuses before it sends a request if the policy names
  `allUsers` or `allAuthenticatedUsers`. A service, repository or secret of the check's own names that
  was **already there** is also a hard stop: this check never adopts, writes into or deletes what it
  did not create.

## What is left behind

Nothing. The Restore runs from the exit trap too, so a Ctrl-C in the middle still deletes the service,
the image, the Artifact Registry repository, the Secret Manager container and the throwaway branch on
the sandbox repository — and it asserts three counts read `0` before the scratch directory goes. If a
count is not `0`, the directory is **kept**, with a message saying where it is and what to delete by
hand.

**No agent runs any step of it.**
