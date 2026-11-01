# `fugaro accounts add`: one command for a named Claude token

**Status: design approved; decisions answered.** The owner answered every question in
[reconciliation.md](reconciliation.md) §4 on 2026-10-31: **D860, D861, D864, D865, D866, D867, D869
and D870 as recommended; D862 changed to the design's alternative (a)** — `--claude` gives the login
the owner's **own terminal** and then asks for **one paste** at the hidden prompt, so there is no pty
child, no line-copier, no token-run guard and no new dependency, and **Windows works**; **D868
changed** — `--yes` **covers everything**, a widening of `allow_data_to` included (still refused in a
coding agent's session, never worked around), and the confirmation `--yes` stands for is written in
[spec.md](spec.md) §2.6; and **D863, D871 and D872 are moot**, because D862's answer removes the pty
and the capture they were about. The spec and the plan are written to the answers; the tasks may
start. On 2026-11-01 the owner answered the three the brief had not asked — **D873,
D874 and D875**, which account a run uses when the project holds several — and they are written in
[spec.md](spec.md) §11 and in §4's last rows. Answering them exposed a fourth question the owner has not
ruled on — **D876**, that the run record names one account for the whole run and so cannot say which
account served which stage — and it is written as **open** in [spec.md](spec.md) §11.5.

**Asked by:** the owner, 2026-10-31: the named-Claude-token flow on
[docs/project-claude-accounts.md](../project-claude-accounts.md) is **seven steps** — two hand edits of
`~/.config/fugaro/projects/<project>.yaml` and five commands — and the owner wants **one command** that
takes a name and the token and does all the work. Better still, a flag that runs Anthropic's own login,
takes the token from it and stores it, so the owner types nothing.

**Reconciled against** `origin/main` at `0a1f5694`, re-checked against `802ae65eb` (after #1464, #1468,
#1471, #1463 and #1467 merged), and against the pull requests open on 2026-10-31.

## What it is

One command, `fugaro accounts add <name>`, that does everything the page's seven steps do — takes the
token, stores it project-wide where only a shared job can mount it, declares the account and its
repositories in the owner's local config, puts it on a profile, and converges the published policy and
the deployed job set so the next `fugaro run --token <name>` works:

```console
$ fugaro accounts add team-a
Claude account team-a: paste the token (input hidden), then press Enter: 
account   team-a            claude-oauth-token-team-a (project-wide, version 1)
repos     owner/repo-a      claude_accounts.team-a.allow_data_to
profile   standard          profiles.standard.claude_accounts += team-a
publish   policy node       accounts.team-a.allow_repos = [github/owner/repo-a]
deploy    jobs              + standard/github/team-a (and its service account)
Nothing else to run: fugaro run --token team-a is ready.
```

and a login first, when the owner prefers it to hunting for a token:

```console
$ fugaro accounts add team-a --claude
```

`--claude` is **two acts**, not a capture: Fugaro runs Anthropic's own `claude setup-token` login in
the owner's **own terminal** — no pty child, nothing between the owner and the login — and when the
login has printed the token, the command asks for **one paste** at the hidden prompt it always uses
(spec §3). Fugaro never reads the token out of the login's output.

`fugaro accounts rm <name>` is its inverse. Re-running `add` for a name replaces that account's token,
which is the rotation path.

The **HARD RULE** of [CLAUDE.md](../../CLAUDE.md) is what makes this one command rather than a command
plus a follow-up: the publish and the deploy are `fugaro update`'s own stages, invoked by this command
through the same code, so the account is usable the moment the command returns and no second command or
hand edit stands between the owner and a run.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The command, its flags, the order of its work, the `--claude` login path, the inverse, what each existing command becomes, the threat model, and how a run chooses among several accounts (§11, D873–D876 — §11.5 is **D876, open**) |
| [reconciliation.md](reconciliation.md) | Every spec claim against the code on `main` and against the open pull requests: what exists, what is missing, what conflicts (findings F1–F19), the
dependencies E1–E5, and the owner decisions D860–D876 (D876 open). **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks A0–A7 (at most 12 files each), the shared-files table and the level order. **Nothing in it is approved** |

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D860–D879** | this work's block: **D860–D875** written (D871 and D872 are the guard's, added in the fix round, and **moot** with D862's answer: there is no capture for them to govern; **D873–D875 are the run's own account choice**, answered by the owner on 2026-11-01 and written in [spec.md](spec.md) §11), **D876** the open question their answer exposed (spec §11.5: the run record's one `oauth_account` cannot name a stage's account), **D877–D879** reserved and unused. Taken above every number on `main`, whose highest is now **D859** — the `settings` design merged as **#1464** with **D840–D859**, so the earlier reading of the tree (`D829`, the tail of `docs/console-coordinator-v2`'s D810–D829) is one merge behind. Below this block: the `rigor` design (#1463, merged) holds **D830–D839**, the console auto-update plan (#1458, merged; it replaced the closed #1377) holds **D760–D772**, and the models block (**D780–D799**) sits below all of them. Above it: **D880–D899** belong to the cloud-senior-review design, **merged** as **#1472** — **D880–D894** written, **D895–D899** reserved. This work's ceiling is unchanged: it takes nothing above **D879**, and **D873–D875 were re-checked on 2026-11-01** against `origin/main` and against the pull requests open that day — no decision on `main` and none in an open pull request used D873–D879, so the block took its next three numbers and nothing had to be renumbered — and **D876 was re-checked the same day, when the fix round raised it**, on the same reading: still free, so it is taken rather than reserved. The token `D800` in `docs/` is the false positive `docs/models-commands/README.md` records (`U+D800` in #715's diff), not a decision |
| **Checks** | none taken here. The `--claude` login needs a live check, and `docs/gcp-live-checklist.md` is another work stream's file (`docs/accounts-add` may not edit it), so the check is written in [spec.md](spec.md) §9 as **LC1–LC4** and the task that implements the flag moves it into the checklist under the number free at that merge. Its second half is [Check 35](../gcp-live-checklist.md) (**U2**), written and **never run** |

## What this design does not do

- It adds no code. Every command below is a proposal.
- It changes no credential semantics: the account is still a name, a project-wide secret and a job per
  (profile, provider, account) ([docs/project-claude-accounts.md](../project-claude-accounts.md) §1).
- It adds no way to reach an account the owner has not admitted: the choice keys of spec §11 name an
  account, every name is still held to `claude_accounts` before any launch, and `auto` — which chooses
  for the owner, and only among those — ships off (D874, D875).
- It invents no way to mint a token. `claude setup-token` stays Anthropic's, and Fugaro never prints a
  token on any path of its own. With `--claude` it is Anthropic's login that prints the token, on the
  owner's own terminal, and the owner pastes it once at the hidden prompt.
- It does not touch the cost cap: the gateway's budget is unchanged, and none of this spends anything.
