# `fugaro models`: managing a project's models with one command

**Status: design approved; the tasks queue behind CLI 0.14.1 (0.15.0).** D780–D783 were answered on 2026-10-30 (D783 against the recommendation); D784–D795 were answered on 2026-10-31, all as recommended. The plan is written to the answers.

**Asked by:** the owner, 2026-10-30: "we should have fugaro commands to manage the models".
**Reconciled against** `origin/main` at `2d849e802` and the 15 pull requests open on 2026-10-30.

## What it is

Switching a repository's reviewer to another model today is four hand steps: edit the local project
config (`~/.config/fugaro/projects/<project>.yaml`: a route's `models:` list, and a
`model_prices:` entry when the built-in price catalog carries no row), run `fugaro validate`, run
`fugaro update --repo` in every repository the route may send code to, and edit the repository's
`fugaro.yaml`. That is a hand-edited config plus a second command, which the HARD RULE in
`CLAUDE.md` forbids: `fugaro update` is the whole upgrade.

This work adds one command family, `fugaro models` (`ls`, `search`, `add`, `rm`, `use`), that makes
the owner's edit one command, checked before it is written, and leaves `fugaro update` as the only
command that carries the change to the cloud.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The five commands, the policy path, the owner-only boundaries, the console and mobile read side, and the migration of a hand-edited config |
| [reconciliation.md](reconciliation.md) | Each spec claim against the code on `main`: what exists, what is missing, what conflicts (findings F1–F16), and the decisions D780–D795. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks M0–M6 (at most 12 files each) and the external dependencies E1, E2, with tests, red steps, gates, SEC flags and levels. **Nothing in it is approved** |

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D780–D799** | this work's block: **D780–D795** written, answered; **D796–D799** reserved and unused. Taken above every number on `main` (the highest is D759, the top of review-levels-ab-jobmax's reserved D720–D759) and above every open pull request's docs: the console auto-update plan (#1377) writes D760–D772, and #715 names D800, so D773–D779 are left as a gap rather than packed against #1377's block |
| **Checks** | none: every behaviour this work adds is a local file edit or an offline read, and the plan tests each one hermetically. No live check is proposed, so no check number is taken |
