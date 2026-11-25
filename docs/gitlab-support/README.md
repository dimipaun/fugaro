# GitLab support

**Parked by the owner by name on 2026-11-25.** GitLab is kicked down the road, not built until a
named need exists — a GitLab customer or a GitLab repository — and then only after this folder's own
reconciliation of tokens and `Draft:` semantics (D610).

**Nothing here is written or approved.** There is no reconciliation, no plan and no approved task:
this folder holds only the GitLab material that already existed in
[docs/forge-authorship-images](../forge-authorship-images/), moved here on 2026-11-25 when the task
**B3** left that plan. **The first output when a need is named is the tokens and `Draft:`
reconciliation**, before any adapter task is written. No decision is taken and no design invented
here.

## The task, as it stood (B3)

B3 was a task of
[docs/forge-authorship-images/implementation-plan.md](../forge-authorship-images/implementation-plan.md)
until 2026-11-25. Its text, as that plan had it:

> **B3. GitLab.** GATED: D610. **State:** Kept — **parked** (the owner's ruling of 2026-11-21 keeps
> it so), gated, and unwritten: no file under `docs/` reconciles GitLab.
>
> A docs-only reconciliation first: tokens (project or group access tokens and what they cost),
> `Draft:` semantics, the image build's credential step, the credentials service's mint, the secrets
> and Terraform, using B1's inventory table as the list of places. The adapter tasks are written from
> it. **Not sized here** beyond: L, security-sensitive throughout.

**Its first output is that reconciliation, and it is unwritten.** It is the whole of what a named need
buys first: the tokens, the `Draft:` semantics, and every place B1's inventory table lists (the image
build's credential step, the credentials service's mint, the secrets, Terraform, the schemas and the
plugin skills).

## The decisions it rests on

Both are answered (2026-10-29) and stay in
[docs/forge-authorship-images/reconciliation.md](../forge-authorship-images/reconciliation.md) §4.

**D610 — GitLab (F11).** *Recommended:* not in 0.14; first the forge-kind cleanup (B1), GitLab after a
named need, with its own reconciliation of tokens and draft semantics. *Alternative:* build GitLab
now — an L-sized, security-sensitive change across config, infra, builds, the credentials service and
the skills. *Owner, 2026-10-29:* no GitLab in 0.14; it needs a named need and its own reconciliation
first.

**D611 — the forge interface (F9), the part a third forge meets.** *Recommended:* keep
`gitprov.Provider`; add only a CI-status read when change sets need it; minting stays in `TokenSource`
and the service. *Owner, 2026-10-29:* keep `gitprov.Provider`. A GitLab adapter implements that
interface; it does not replace it.

## What the repository already says about GitLab

`git grep -n -i gitlab -- docs internal` (2026-11-25) finds no GitLab support and no GitLab code:

- **GitLab is an unknown forge.** `internal/gitprov/kinds.go` declares `KindGitHub` and
  `KindBitbucket` only; `internal/gitprov/providers` and `fugaro.yaml` validation refuse `gitlab`
  (`unknown git provider`; `must be one of github, bitbucket`). B1's inventory tables in
  `internal/gitprov/kinds_test.go` are the list of places a third forge touches.
- **Finding F11** ([reconciliation.md](../forge-authorship-images/reconciliation.md)) records the gap
  and the places: config validation, `providers.FromEnv`, the image build's credential step
  (`images/derived/cloudbuild.yaml`), the secret names and Terraform, the credentials service's mint,
  the schemas and the plugin skills — plus GitLab's own semantics (merge requests, the `Draft:` title
  prefix, project and group access tokens). **U3** leaves GitLab.com's and self-managed signing of API
  and web commits unverified.
- **The compatibility matrix** ([docs/compatibility-matrix](../compatibility-matrix/)) lists GitLab
  Cloud and GitLab self-managed as **Roadmap** (D1529, F21: the spec's "Coming soon" for GitLab Cloud
  was the gap); nothing schedules either.
- **forge-labels** ([docs/forge-labels](../forge-labels/)): its spec names GitLab labels, the code has
  no GitLab provider, and D2431 rules GitHub only until one exists.
- **Four places know the name and nothing else:** `internal/cli/project_profile.go` names `gitlab`
  (with `fake`) in `project profile add --provider`'s help, the one user-facing text that calls GitLab
  a provider (published in `website/content/cli-reference.md`) — and the value is refused at publish,
  where `internal/projectpolicy/profiles.go` holds a profile's provider to `config.Providers`
  (`github, bitbucket`); `internal/cli/run.go` accepts
  `<id>-<login>@users.noreply.gitlab.com` as a forge noreply commit address (D649);
  `internal/imagesurvey/signals.go` reads `.gitlab-ci.yml` as a repository's CI signal; and
  `internal/imagecheck/reduce.go` lists `gitlab:` as a source that is not an ecosystem registry.
- **Test fixtures** use `gitlab` as a value a command must refuse (`internal/cli/exec_test.go`,
  `internal/config/config_test.go`, `internal/projectpolicy/cloneurls_test.go` and others).

No code moves here. The summary is a reading of the tree, not a plan.
