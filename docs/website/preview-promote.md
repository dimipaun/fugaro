# The website's preview and promote workflows

**Status: copied in by the owner (the staged copies are removed), never run.** The two workflows live at
[`.github/workflows/website-preview.yml`](../../.github/workflows/website-preview.yml)
and [`.github/workflows/website-promote.yml`](../../.github/workflows/website-promote.yml);
they were staged under `docs/pending-workflows/` first because **no Fugaro run may create or edit a file under `.github/workflows/`**
(GitHub rejects the push; website plan Global Constraint 2, finding F4). The
owner copies them in — [the copy procedure](../pending-workflows/README.md) is
the one that directory already documents — and **everything in "One-time setup"
below is the owner's to run**. No agent runs any command in this document, and
none was run while it was written: this page is the instructions, not a record.

Owner: **@Dimi** (the `dimipaun` account, the repository's owner and the only
reviewer of the `website-live` environment). Date: **2026-10-18**. The shape is
the owner's ruling of 2026-10-18: **previews are manual and public, promoting is
manual and the owner's alone, and authentication to Google is Workload Identity
Federation, never a key file.**

## What the two workflows are

| | `website-preview.yml` | `website-promote.yml` |
|---|---|---|
| Trigger | `workflow_dispatch` only | `workflow_dispatch` only |
| Inputs | `ref` (default `main`), `channel` | `channel` (an existing preview channel) |
| What it does | a credential-free `build` job checks out the ref and runs the site's gate (`npm run check` in `website/`: the Astro build and every `website/test/*.mjs`); a separate `deploy` job takes the build as an artifact, vets its `firebase.json`, and deploys `website/dist` to a **preview channel** of the site's own Firebase project (`firebase hosting:channel:deploy "$CHANNEL" --project "$FIREBASE_PROJECT_ID" --expires 7d --json`) | copies an existing preview channel's current version onto the **live** channel (`firebase hosting:clone "$PROJECT:$CHANNEL" "$PROJECT:live"`); nothing is rebuilt |
| What it writes | a new version in one preview channel | the live channel |
| Output | the preview URL, the channel, the ref and the commit, in the run's **job summary** | the project, the promoted channel and who promoted it, in the job summary |
| Who may start it | anyone with write access to the repository, from `main` (the WIF provider refuses any other dispatch ref) | **the owner alone** — the `website-live` environment's required reviewer *and* a guard on `github.actor` |
| Credential | none in the file: Workload Identity Federation | none in the file: Workload Identity Federation |
| May leave the site down? | no: the live channel is never read or written | yes, by design: promoting is the one thing that moves the live site |

Both are `workflow_dispatch`-only on purpose. **Nothing deploys because a branch
moved**: a merge to `main` deploys nothing, and the live site changes only when
the owner promotes bytes they have already seen on a preview URL. This is what
D109 asked for — the site's deploy is decoupled from the release train, which is
also why neither file is referenced by `ci.yml`, `release.yml` or
`scripts/release-gate.sh`.

**A preview is public.** A Firebase Hosting preview channel is served on its own
URL and is reachable by anyone who has it; there is no visitor gate on it. So
nothing that must not be public belongs in a build that is deployed to a
preview, and the promote workflow is the only thing that makes a preview the
site.

## What both files hold, and what they refuse to hold

- **No credential, and no secret.** Both authenticate with the
  [`google-github-actions/auth`](https://github.com/google-github-actions/auth)
  action in **Workload Identity Federation** mode, pinned by commit SHA like
  every other action in this repository: the job's own OIDC token (minted by
  GitHub for that run) is exchanged for a short-lived access token for the
  deployer service account. **No service account key, no key file, no
  `FIREBASE_TOKEN`, and no `secrets.` reference of any kind** appears in either
  file: the three values they read are repository *variables*, which are
  identifiers rather than credentials, and nothing is written into a log.
- **Least privilege, declared.** `permissions:` is written out at the workflow
  level and on every job; the only scopes used are `contents: read` (the
  preview's checkout) and `id-token: write` (the federation exchange), and
  `id-token: write` is held only by the job that authenticates — never at the
  workflow level and never by the preview's `build` job, which runs the
  requested ref's code. Neither file asks for `workflows: write`, and neither
  ever holds `contents: write`. No `${{ }}` expression appears inside any
  `run:` script: inputs reach the shell as environment variables only.
- **No `pull_request_target`, and no pull request at all.** The trigger block
  contains exactly one key. A pull request from a fork runs nothing here — it
  cannot, because there is no event for it — so it can reach no credential and
  cannot cause a deploy.
- **No project id committed.** The Firebase project is named on the firebase
  CLI's command line from `vars.FIREBASE_PROJECT_ID`, exactly as
  `scripts/deploy-website.sh` names it from its `--project` argument. There is
  no `.firebaserc` anywhere in the tree, and the deployer's project is the
  site's own (`fugaro-prod`, D91) — **never an installation's**, and never a
  project a Fugaro run can reach. This workflow runs no Fugaro command and is
  invoked by no Fugaro command; no credential a Fugaro run holds is used by
  either job.
- **The channel name is validated the way the script validates it.** Both
  workflows check `$CHANNEL` against `^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$` — the
  same pattern `scripts/deploy-website.sh`'s `--channel` check uses,
  character for character — and both refuse the name `live` as an input
  (`website-preview` never writes live; `website-promote` only ever writes it).
  `internal/cli/docs_website_deploy_test.go` reads the pattern out of both
  files and fails if the workflow and the script ever drift apart.
- **The promote job's two locks.** `environment: website-live` makes GitHub
  hold the job until its only required reviewer approves it, and
  `if: github.actor == 'dimipaun'` means a dispatch by anybody else is skipped
  before a runner or an approval request exists. A second step re-checks the
  actor inside the job, so an edit that later loses the `if:` still stops with
  a message rather than asking the reviewer to approve a promotion they did not
  start.

## One-time setup — the owner's, and only the owner's

Everything below is run **by the owner, in the owner's own terminal**, with
`gcloud` authenticated as an account that can administer the site's project
(`fugaro-prod`, D91 — the project created for the website only). Nothing here
creates a credential: a Workload Identity Federation pool holds no secret, the
deployer service account holds no key, and the only thing GitHub stores is
three variable *values*, all of which are identifiers.

**Step 0 — the shell's variables, and the project number.** The WIF principal
below is built from the project **number**, not the id:

```sh
PROJECT=fugaro-prod                 # the website's own Firebase project (D91)
REPO=dimipaun/fugaro                # this repository, owner included
REPO_ID=$(gh api "repos/$REPO" --jq .id)   # its immutable numeric id
PROJECT_NUMBER=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')
SA=website-deployer@$PROJECT.iam.gserviceaccount.com
```

**Step 1 — the deployer service account.** No key, ever: nothing below creates
one, and the workflow has no field that could take one.

```sh
gcloud iam service-accounts create website-deployer \
  --project="$PROJECT" \
  --display-name="website deployer (GitHub Actions)"
```

**Step 2 — its Firebase Hosting role, on that project and nowhere else.**
Firebase Hosting has exactly two predefined roles, `roles/firebasehosting.admin`
(*"full control of Firebase Hosting resources"*) and
`roles/firebasehosting.viewer`; a deploy writes Hosting sites, so the deployer
takes the admin role on the site's project.

```sh
gcloud projects add-iam-policy-binding "$PROJECT" \
  --member="serviceAccount:$SA" \
  --role="roles/firebasehosting.admin" \
  --condition=None
```

Two more roles are **not** granted here; add one only if a real run reports it,
and read the run's own error before doing so:

- `roles/serviceusage.serviceUsageConsumer` on `$PROJECT` — needed only if a
  call fails for *using* the project as its quota project (a
  `serviceusage.services.use` denial).
- `roles/firebase.viewer` on `$PROJECT` — needed only if firebase-tools cannot
  *read* the project itself (`firebase.projects.get`).

**Step 3 — the Workload Identity Federation pool and provider.** The provider
is conditioned on this repository (by name **and** by its immutable numeric
id, so a deleted-and-recreated or transferred `dimipaun/fugaro` is not the same
repository), on the event being a `workflow_dispatch`, **and on the workflow
having been dispatched from `main`** (`assertion.ref`, the ref the workflow
*file* came from — not the preview's `ref` input, which only chooses what is
built). So no other repository, no push-triggered workflow and **no branch's
own copy of a workflow file** can ever exchange a token through it: only the
two files as `main` has them, which is what branch protection on `main`
reviews.

```sh
gcloud iam workload-identity-pools create github \
  --project="$PROJECT" \
  --location=global \
  --display-name="GitHub Actions"

gcloud iam workload-identity-pools providers create-oidc github \
  --project="$PROJECT" \
  --location=global \
  --workload-identity-pool=github \
  --display-name="GitHub Actions (OIDC)" \
  --issuer-uri="https://token.actions.githubusercontent.com" \
  --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.ref=assertion.ref,attribute.environment=assertion.environment,attribute.event_name=assertion.event_name,attribute.repository_id=assertion.repository_id" \
  --attribute-condition="assertion.repository == 'dimipaun/fugaro' && assertion.repository_id == '$REPO_ID' && assertion.ref == 'refs/heads/main' && assertion.event_name == 'workflow_dispatch'"
```

**Step 4 — let that repository's workflows impersonate the deployer.** The
member is the pool's `attribute.repository` principal set for this repository,
so any *other* repository's OIDC token is refused before the service account is
consulted, whatever it claims:

```sh
gcloud iam service-accounts add-iam-policy-binding "$SA" \
  --project="$PROJECT" \
  --role="roles/iam.workloadIdentityUser" \
  --member="principalSet://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/github/attribute.repository/$REPO"
```

**Step 5 — the three repository variables.** Variables, not secrets: these
values are identifiers, they are readable by anyone who can read the repository,
and neither workflow can hold a secret at all.

```sh
gh variable set FIREBASE_PROJECT_ID --repo "$REPO" --body "$PROJECT"
gh variable set GCP_WORKLOAD_IDENTITY_PROVIDER --repo "$REPO" \
  --body "projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/github/providers/github"
gh variable set GCP_DEPLOYER_SERVICE_ACCOUNT --repo "$REPO" --body "$SA"
```

**Step 6 — the `website-live` environment, with the owner as its only required
reviewer.** Either in the repository's **Settings → Environments** (create
`website-live`, add yourself under **Required reviewers**, and under
**Deployment branches and tags** choose **Selected branches** with `main` and
`main` only), or with the API:

```sh
gh api --method PUT "repos/$REPO/environments/website-live" --input - <<JSON
{
  "reviewers": [{"type": "User", "id": $(gh api users/dimipaun --jq .id)}],
  "deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}
}
JSON

gh api --method POST "repos/$REPO/environments/website-live/deployment-branch-policies" \
  -f name=main
```

**Leave "Prevent self-review" off.** The owner is both the actor and the only
reviewer, and GitHub would otherwise refuse the owner's own approval of the
owner's own run — the environment would hold every promotion forever.

**Step 7 — copy the two files in.** With the procedure
[`docs/pending-workflows/README.md`](../pending-workflows/README.md) already
documents, in one commit, with the `git rm` of both staged copies (it is
load-bearing: while a staged copy exists, `docs_website_deploy_test.go` reads
*it* rather than the file GitHub runs).

## The first run, and what to expect

1. **A preview.** `gh workflow run website-preview --ref main -f ref=main -f
   channel=owner-test` (or **Actions → website-preview → Run workflow**). Expect
   a green run whose **summary** carries a table with the channel, a
   `https://…web.app`-shaped URL, the ref and the commit. Open the URL: the site
   should be the build of that commit. Nothing about the live site changed.
2. **A promote.** `gh workflow run website-promote --ref main -f
   channel=owner-test`. Expect the job to appear as **Waiting** until you
   approve it under **Actions → the run → Review deployments**, and, after the
   approval, a summary naming the channel it promoted. `https://fugaro.dev`
   should then serve the version that channel held.
3. **The owner-run live checks.** Checks 69a and 69b of
   [`docs/gcp-live-checklist.md`](../gcp-live-checklist.md) are what prove the
   deployed result: the canonical origin, the deployed HTML being the build's
   artifact, and that the page requests nothing from a third-party origin before
   consent.

If a run stops at its first step, the message names the missing variable and
this page; that is the setup above being incomplete, not a bug in the workflow.

## What this authorises, and the accepted limits

- **The credential is held by `main`'s copy of the two files and nothing
  else.** The provider's condition requires `assertion.ref ==
  'refs/heads/main'`, so a workflow dispatched from any other branch — including
  a branch's own copy of `website-promote.yml` with its `if:` and its
  `environment:` removed — is refused a token by Google, before the service
  account is consulted. What a write-access holder can still do with `main`'s
  files:
  - **A preview of any ref.** `gh workflow run website-preview --ref main -f
    ref=<any branch> -f channel=…` builds and deploys that branch to a
    **public** preview URL. The owner accepted this on 2026-10-18 (previews are
    manual and public). `channel=live` is refused by `main`'s file.
  - **No promotion.** `main`'s `website-promote.yml` is skipped for any actor
    but the owner, and the `website-live` environment holds it for the owner's
    approval.
  The boundary therefore rests on **branch protection on `main`**: a change
  that weakens either file has to be reviewed and merged there first. The
  deployer still holds `roles/firebasehosting.admin`, which can write live, so
  a weakened file on `main` *is* a path to live — keep `main` protected, and
  keep both files' changes under the owner's review.
- **The build never holds the credential.** The preview's `build` job runs the
  requested ref's code and the site's whole npm tree, so it has no `id-token`
  scope: neither the ref nor a compromised dependency can mint the OIDC token
  the deployer trusts. The `deploy` job checks nothing out and runs no site
  script; it takes the build as an artifact and refuses a `firebase.json` with a
  `predeploy`/`postdeploy` hook or a `public` dir other than `website/dist`
  (either would let the ref run a command with the credential or publish the
  credential file). The residual is firebase-tools itself: it is installed by
  version (`firebase-tools@15.19.0`), but its transitive dependencies are not
  locked, and it runs in the job that holds `id-token: write`.
- **A tighter split is available if wanted.** Because the promote job carries
  an environment, GitHub's OIDC token includes an `environment` claim, so a
  second provider conditioned on `assertion.environment == 'website-live'` —
  plus a second repository variable for the promote job to read — would let
  only the environment-gated job impersonate a deployer that can write live.
  Firebase Hosting has no predefined role that deploys previews but not live,
  so this would need a custom role or an IAM condition; it is not done here:
  one provider, one service account, one project, both workflows.
- **A preview channel expires.** `--expires 7d` on each deploy, so a preview
  that is not deployed again disappears; the live channel is unaffected. A
  promotion copies the channel's *current* version, so promoting an expired (or
  redeployed-elsewhere) channel is the owner's call at approval time — the run
  refuses a channel it cannot find rather than promoting something else.
- **The live site's custom domain is not touched by either workflow.**
  Registering `fugaro.dev`, attaching it to the Firebase project and writing the
  DNS records are the owner's steps (D92), and `firebase.json` deliberately
  carries no redirect rule that could loop.

## Not verified

**Neither workflow has ever run.** Written and staged on 2026-10-18 by a Fugaro
run with no cloud access, no deploy and no Google API call of any kind — no step
of the setup above was executed, no service account, pool, provider, variable or
environment was created, and nothing was sent anywhere. What *is* verified is
the two files' YAML shape, by
[`internal/cli/docs_website_deploy_test.go`](../../internal/cli/docs_website_deploy_test.go)
(`workflow_dispatch`-only, no `pull_request_target`, every action pinned by SHA,
the environment and the actor guard on the promote job, the channel pattern
matching the script's, the preview URL in the job summary, and no key-file
credential anywhere) and, where available, by `actionlint`.

**One thing short of a run was checked, and it is the CLI's own contract:**
read out of the source of the pinned firebase-tools (**v15.19.0**, the version
both workflows install and `internal/budget/rules/emulator` pins),
`hosting:channel:deploy --json` prints `{"status": "success", "result": …}`
with `result` keyed by the site id and carrying `url`, which is the shape the
preview summary reads, and `hosting:clone` refuses a source channel the project
has not got — and one carrying no version — with its own message, which is what
the promote job relies on instead of listing the project's channels first. The
two global options both use (`-P, --project`, `-j, --json`) are that version's
too.

Specifically unexercised, and each is named so the first real run settles it:

- that the JSON above comes back from a **real** deploy (a different shape
  fails the run loudly — it prints the CLI's own JSON — and the fix is this one
  step), and that the promoted channel's `url` is the one the preview served;
- that firebase-tools authenticates from the credential file
  `google-github-actions/auth` writes (a Workload Identity Federation
  `external_account`, not a key);
- that `roles/firebasehosting.admin` alone is enough for a deploy, and whether
  either of the two add-on roles in step 2 is needed (that step's symptoms are
  the ones to match against the run's error);
- that `firebase hosting:clone "$PROJECT:$CHANNEL" "$PROJECT:live"` names the
  project's default Hosting site correctly as a site id;
- that the preview's artifact hand-off (`actions/upload-artifact` of
  `firebase.json` + `website/dist`, downloaded into `site/`) lays the two out
  as the deploy step expects, and that the provider's `assertion.ref` and
  `assertion.repository_id` conditions admit `main`'s dispatches (a refusal
  here is an STS `unauthorized_client` error in the auth step).

## See also

- [`docs/pending-workflows/README.md`](../pending-workflows/README.md) — why these files are staged, and the copy-in steps
- [`scripts/deploy-website.sh`](../../scripts/deploy-website.sh) — the same build, gate and deploy for a human at a terminal, and the channel pattern both workflows use
- [`firebase.json`](../../firebase.json) — the hosting configuration the deploy reads (one site, no project id, no redirects)
- [`website/data/site.yaml`](../../website/data/site.yaml) — the canonical URL the live site is served on
- [`docs/website/implementation-plan.md`](implementation-plan.md) — Task W2.1, whose credential rule (F14) and D91/D109 constraints this pair implements
- [`docs/gcp-live-checklist.md`](../gcp-live-checklist.md) — checks 69a and 69b, the owner-run live checks
