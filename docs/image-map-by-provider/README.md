# Image map keyed by provider (lift L11)

**Status: plan only; nothing is implemented; no task approved.** Every decision is answered.

**Spec owner:** Dimi Paun. The owner approved the spec on 2026-11-25. It was reconciled and
planned the same day, against `origin/main` at `3dfc5cb8f`.

This is lift L11 of the [repo-coupling audit](../repo-coupling-audit/README.md) (report §7, RC51,
D3238). The project's repo-to-image map is keyed by `owner/name`, so a GitHub and a Bitbucket
repository of one name share one image. L11 keys the map by `provider/owner/name`, under a new
member, `repo_images`, and `fugaro update` moves the old map once.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec as approved. No typo was fixed |
| [reconciliation.md](reconciliation.md) | Assumptions A1–A4, findings F1–F35 (F12–F20 from review round 1, F21–F32 from round 2, F33–F35 from round 3), decisions D3370–D3377 with the owner's answers (D3372 and D3376 amended after review, by the owner), resolutions R1–R16, open items O1–O4, UNVERIFIED V1–V3. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Nineteen tasks in six levels (T0, K1; T0b, S1; T0c, U1a, R1, R2a, R2b, R2c; U1b, R2d, R3, S2; U1c, S3, D1a, D1b; X1), the shared files, the edges kept, and the owner's Check 940. Nothing in it is approved |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph, as `fugaro plan graph` reads it |

**The owner's answers, all as recommended, two amended after review:**
- **D3370:** the key is `provider/owner/name`, from `projectpolicy.Canonical`.
- **D3371:** the member is `repo_images` on the node, in the local config and in `--json`.
- **D3372:** an old-form key is refused. **Amended after review** and confirmed on 2026-11-25:
  `update` builds images before it rewrites the policy, so an old `image_map` member is read as
  today until `update` rewrites it. Only an old-form key inside `repo_images` is refused.
- **D3373:** a name at two providers migrates to both keys, with the same entry.
- **D3374:** it ships one release after L1.
- **D3375:** a name whose provider `update` cannot find migrates to every provider.
- **D3376:** `update` removes `image_map` in the write that adds `repo_images`. **Amended after
  review** by the owner on 2026-11-25: `image_map` stays on the node until a step after the
  credentials service's redeploy removes it, so the old service never refuses a build token.
- **D3377:** a catalog entry's `Source.Repo` is a follow-up.

**Numbers reserved:**
- Decisions **D3370–D3379**: D3370–D3377 used, D3378–D3379 unused.
- Checks **940–949**: Check 940 used, 941–949 unused.

Both blocks were checked free on `main` at `3dfc5cb8f` and in every open pull request's documents.

**What waits on something external:**
- the owner's go for each task;
- L1's release, before L11's (D3374);
- Check 940, which the owner runs after the release that carries U1b;
