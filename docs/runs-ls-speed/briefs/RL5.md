# Task: docs: fast runs ls, its residual and the skills' sentences

Build task RL5 of `docs/runs-ls-speed/implementation-plan.md`. It needs RL1, RL3 and RL2B merged. Read its RL5 section and `docs/runs-ls-speed/reconciliation.md` (D2964, D2965). Two of its files are plugin skills, which are a security path: expect an independent review.

**Goal.** Describe what was built, in the places that already describe `runs ls`.
- `docs/bucket-iam/spec.md` §10: the residual of D2964 in two sentences (an entry erased by a hostile run on a shared job hides a run older than 24 hours from `--pr N`; it never invents a row, because each listed run's own records are read).
- `plugin/skills/working/SKILL.md` and `plugin/skills/landing/SKILL.md`: the sentences that name `fugaro runs ls` say that `--state running` and `--pr N` are the fast queries and what the one stderr fallback line means. Keep every command and flag a skill names real (the plugin tests check them).
- The release notes file of the release that carries RL3 (`docs/releases/`, see `docs/releases/README.md`): a Highlights line, and `### For operators` stays "run `fugaro update`" plus one line that the update writes the pull-request index for the last 90 days of runs, creates only what is missing and waits for no run.
- `website/content/cli-reference.md`: only if help text moved, regenerated with `go run ./ee/clidoc`.

**Tests.** `go test ./internal/cli/ -run Docs -count=1 -timeout 30m`, `go test ./plugin/... -count=1`, `go test ./ee/clidoc/ -run TestCliReferenceIsCurrent -count=1`, and `npm run check` in `website/` only if website content changed. Keep every pinned fact and change the wording in both places.

**Rules.** No secret-shaped literals. Do not touch `docs/status.md` or `.github/workflows/`. Commit message: `docs: fast runs ls, its residual and the skills' sentences`.
