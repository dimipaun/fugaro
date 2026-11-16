# Spec: `fugaro review --pr N` works out of the box (the review job, option C)

Owner: the project owner. Recorded 2026-11-14 from the owner's rulings of 2026-11-13; the owner wrote no single spec
document, so this page assembles the spec from the sources below, each fact tagged with where it comes from, and
changes no word of their substance. Where this page and `reconciliation.md` disagree, the reconciliation wins.

## Sources

| Tag | Source |
| --- | --- |
| **[R]** | the owner's ruling of 2026-11-13, "option C" (project memory `owner-ruling-2026-11-13-review-job-option-c`) |
| **[F]** | the owner's feedback of 2026-11-13, "review must work out of the box" (project memory `feedback-review-must-work-out-of-the-box`) |
| **[C1]** | the merged pull request #2523 (its description and commit message), piece 1 |
| **[B]** | the merged pull requests #2526 and #2527 (the Bitbucket review class), whose task briefs say they are the Bitbucket half of option C |
| **[P3]** | the brief of the live run `20261114-130709-a709` (piece 3) |

## The problem as received

[F] `fugaro review --pr N` refused on the owner's own repository with "review-only variant of a shared project job, and
this repository has none", while `fugaro run --recipe review-only --pr N` worked. The cause: the isolated review job
exists only as a `-review` sibling of a shared project job, and a project that publishes no `profiles:` block runs every
workflow on a per-repository job. The owner's words: "I don't want our users to have to jump through hoops to use
something that, by all intuition, should just work out of the box."

[F] Two spellings of the same review must not differ in whether they work. The fix belongs in `fugaro update` and the
CLI, never in a step the user takes (never "publish profiles to get a review"); `update` must stay safe with runs in
flight and converge from the previous version.

[C1] A review job mounts no secret and clones the pull request head itself; the credentials service pairs a review by
the run identity's repository slug and the policy's admissions, not by the job it ran on. So one review-only job per
(provider, account) can serve every repository.

## The ruling: option C

[R] Build option C: **one review-only job per (provider, account), deployed by `fugaro update`**, whether or not the
project declares profiles. It was chosen over:

- **A**, a `-review` sibling of every per-repository job (doubles the job count);
- **B**, a default profile (leaves baked, catalog and Bitbucket repositories per-repository and widens the shared App
  key).

[R] The pieces, with their review levels:

1. **Piece 1 (L3):** infra and Terraform. An implicit default review-only job per (provider, account) even with no
   profiles.
2. **Piece 2 (L3):** the credentials service and the policy admit it; the runner's `checkJobClass` agrees.
3. **Piece 3 (L2):** the launcher's `reviewJobFor` resolves it when there is no coding job; the refusal goes.
4. **Piece 4 (L2):** the migration (one `fugaro update` converges with a run in flight) and the documents.
5. **Then a traced Bitbucket piece.** [R] "Launch piece 1 now, then trace Bitbucket and the catalog-image mapping; I
   suspect we need workspace level tokens for Bitbucket."

[R] Listed as unverified by the ruling itself: the base-image review path for baked repositories, the default account
when the auth is an API key or Vertex, and a Terraform plan with no project jobs.

## Boundaries the briefs state

[C1][P3] Piece 1 is infra only. Piece 3 is GitHub only (Bitbucket stays refused with its own sentence) and keeps every
other refusal: no credentials service, an account the service does not admit, the pull request's own coding run still
going, a binary older than the installation. A review is never started on a coding job. No field changes meaning; no
secret-shaped literal; a finding that needs a product decision is reported, not fixed.
