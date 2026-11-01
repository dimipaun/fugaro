# `fugaro update`: the spec

The behaviour below is written to the recommended answer of each decision (D940–D953,
[reconciliation.md](reconciliation.md) §3). A decision answered the other way changes the paragraph that
names it. Repository names are placeholders (`owner/repo-a`, `person-1`); no teammate's machine, path or
secret is named anywhere.

## 1. The rule this serves

`fugaro update` is the whole upgrade (`CLAUDE.md`, HARD RULE). A user who has run `brew upgrade` and then
`fugaro update` is done: no second command, no per-checkout step, no "install the plugin yourself". The
plugin's home is the **machine**, not a repository, because the tool that has to be brought up to date is
the user's own Claude Code — and one command that converges both is the only shape the rule allows.

## 2. One command: what `fugaro update` does

`fugaro upgrade` is removed (D941). Its five steps go to the places `fugaro update` already has for
them, and one of them moves stage as well, for a reason that is a defect in the naive version:

| `fugaro upgrade`'s step | Where it lives after this work |
| --- | --- |
| `pin` | **dropped as a step.** The merge into `.claude/settings.json` becomes opt-in, behind `fugaro update-skills --pin` (§4, D949), and `update` runs it only for a repository that already carries a pin — as the migration of §8, not as a step |
| `plugin` | **`fugaro update`'s `plugin` stage** (`internal/cli/init_plugin_stage.go`), retargeted at user scope (§3), and now `LocalUnderAgent` |
| `cloud` | `fugaro update`'s **checkout** stage, where it already is (`internal/cli/init_checkout_upgrade.go:39`, `checkoutSteps`) |
| `rules` | `fugaro update`'s rules stage, where it already is (`init_rules_stage.go`) |
| `policy` | `fugaro update`'s policy stage, where it already is (`init_policy_migrate.go`) |

**Why the plugin moves stage and does not stay in the checkout stage (F13, and it decides §2).** The
checkout stage is declared *after* the repository stage (`internal/initflow/stage.go:159`, `Checkout`
needs `Repository`), so its steps are unreachable while the repository stage is waiting for the user. The
plugin step is in the checkout stage today only because it was `upgrade`'s step and the two front doors
were made to share one implementation. Moving the plugin to user scope does not change that ordering: a
session whose repository stage returns needs-you — a repository not onboarded, a missing GitHub App, a
run in flight — would reach the plugin never. The **plugin stage does not have that problem**: it runs
before `Repository` (`internal/initflow/init_stages.go`), which is exactly why the pin's merge was put
there in the first place. So the plugin stage keeps its position and changes its subject.

**Why the plugin stage becomes `LocalUnderAgent` (D941).** The stage is *not* local under an agent marker
today, and that is deliberate: it writes a file that lands in a pull request, which an agent may not
approve. At user scope every write it makes is on this machine's own Claude Code — the same class of
write the checkout stage is already `LocalUnderAgent` for (`init_checkout_upgrade.go:53`, and
`internal/initflow/stage.go:337` for the interface). So a coding agent's session moves the plugin, and no
longer stops at `left for you` for it.

The **checkout stage** keeps the `cloud` step and nothing else. Its `pin` and `plugin` entries
(`init_checkout_upgrade.go:39`) are removed, its `planFor` loses its `pinSkip` case, and
`fugaroCheckout` (`internal/cli/upgrade.go:355`) loses its second disjunct: "the checkout's
`.claude/settings.json` wires the Fugaro plugin" is no longer a way for a checkout to be a Fugaro
checkout, because a repository that never opted in has no such file and is still one.

## 3. The plugin at user scope (D940)

**Two things are true at once today, and the design moves one of them.** The plugin is *installed in
Claude Code* at **user scope** already — `claudeplugin.go:43`, `installUser = claude plugin install
fugaro@fugaro --scope user`, and `addPrefix` adds the marketplace at the same scope — so `fugaro update`'s
plugin step is not changing the scope of the `claude` call; there is nothing to change. What is at
**project scope** is the *enablement*: the committed `.claude/settings.json` that `pluginwire` merges
declares the `fugaro` marketplace and enables `fugaro@fugaro` for everyone who opens that checkout. So
what this decision moves is not an install but the **reach of the plugin**: from "whoever opens this
repository" to "whoever has this machine". A user who has run `fugaro update` once has the skills in every
repository on that machine; a repository that commits the pin still gives them to a teammate who has run
nothing.

`fugaro update-skills` and `fugaro update`'s plugin stage install and update the plugin in Claude Code at
**user scope**, through the `claude` CLI on `PATH`. That is what `internal/claudeplugin` already does
(`plan.go:80`, `Decide`), and the change is what `Decide` *stops* doing:

- **It installs at user scope when no install applies to the checkout — and it no longer treats a
  project or local install as something to maintain.** Today `Decide` updates every applying install at
  its own scope (`plan.go`'s `stale` loop over `scopes`, `claudeplugin.go:46`), so `fugaro upgrade`
  maintains checkout-scoped installs too. Those installs exist only where a person made them by hand, and
  they are the pin's problem, not the machine's: the loop becomes `user` only.
- **Reading keeps its meaning.** `Applying` (`plan.go:41`) and `Effective` (`plan.go:62`) still answer
  "which install applies to this checkout", because D942's doctor check asks that question about the
  machine.
- **The fallback stays.** Without `claude` on `PATH` the step still prints the slash commands to type
  (`noClaudeHint`, `upgrade_plugin.go:20`), and the `claudeplugin.Allowed` allowlist, the empty working
  directory for a marketplace add, the scrubbed child environment and the timeouts are all unchanged. **No
  `claude` argv changes**: the install was already `--scope user`, and what changes is `Decide`'s *filter*
  — which installs it looks at to decide whether the machine is behind.
- **A restart is still required, and still said.** Running Claude Code sessions, this one included, keep
  the old skills until they are restarted; the step's sentence is unchanged.

**Cloud runs are unaffected, from the code.** The runner's agent never loads a plugin: the launch
contract forbids passing any plugin, marketplace, trust or settings-source flag
(`internal/agent/agent_test.go:892`, `TestClaudeArgsNeverSideloadPlugins`), the runner's environment is
scrubbed of plugin and trust variables, and review mode strips `.claude` and `.claude-plugin` from every
reviewed checkout before the agent starts (`internal/runner/reviewmode.go:253`, `stripClaudeConfig`).
Nothing a user's machine installs at user scope is reachable from a job, and nothing a job does depends
on the pin. (A run *does* see the checkout's `.claude/settings.json` on a normal implement stage, but it
passes the flags that would make it load one never.)

## 4. The opt-in pin (D949)

The repository pin in `.claude/settings.json` becomes **opt-in and never the default**. It exists for one
job: a team that wants the Fugaro skills **required** in a repository, delivered by the commit rather than
by each teammate's machine.

- **The door is `fugaro update-skills --pin`.** `update-skills` keeps its command name, its `--check`,
  `--dir`, `--json` and `--allow-fork` flags, and gains `--pin`, which is today's behaviour (the merge,
  the diff, the notices, the fork refusal, the never-commit rule, the `pluginwire` safety properties).
  Without `--pin` it is the user-scope install of §3, and `--check` reports both.
- **`--pin` is not a scope.** It says *also wire the repository*, and it does not change where the plugin
  is installed: the machine's user-scope install is what makes the skills work, and the pin is what makes
  them required. A user who runs only `--pin` on a machine with no user-scope install has a repository
  that names a plugin Claude Code will install on trust, which is exactly today's behaviour and is kept.
- **The marketplace ref follows the default branch** (`claude plugin marketplace add` takes no ref,
  `docs/design/upgrade.md`, Non-goals), so the installed plugin's version is whatever
  `dimipaun/fugaro`'s default branch carries, which the release PR bumps to the release before the tag.
  It can therefore be **newer** than the `fugaro` on `PATH`; the step's existing note (and doctor's
  `newer` state) is what says so, and it stays a note, not a failure.
- **Nothing moves down and nothing is rewritten** by the opt-in path: the fork refusal, the
  never-valid-JSON-rewrite rule, the "pin never moves down" rule and the two-space re-indent are
  `pluginwire`'s and are unchanged.

## 5. The flags of `update`, and what is left of `upgrade` (D948, D950)

- **`--check` survives, on `fugaro update`.** It is added there: read-only, no `claude` call, no cloud
  call, exit 1 when anything the local files can see is stale. `--plan-only` is not that mode — a stage's
  plan may read the cloud (the images stage reads the bucket) — and the skills need a mode that promises
  none of it.
- **`--local` does not survive, because the marker absorbs it.** On `upgrade` it meant "the pin and the
  plugin only — the local half": `upgrade` under an agent marker leaves every step to the user, so the
  local half needed a flag to be runnable at all. After §2 the plugin stage is `LocalUnderAgent` and the
  checkout stage is too, so **plain `fugaro update`, under an agent marker, does the local half and parks
  the cloud half** — which is what `--local` said, without a flag. `fugaro update --local` would be the
  same run with fewer steps, and `fugaro update-skills` is the command for the plugin alone. The **cost**
  of dropping it: a note or a habit that says `fugaro update --local` becomes a cobra "unknown flag" and
  has to say `fugaro update`, and the plan's census lists the places.
- **The cloud flags stay where they are.** `--repo`, `--workflow`, `--image-source`, `--expect-digest`,
  `--config`, `--project`, `--gcp-project`, `--region` were already `update`'s (the checkout stage's
  `refreshOptions` come from `update`'s own options, `init_checkout_upgrade.go`, `upgradeCtx`), and
  `upgrade`'s copies of them go with the command.
- **Nothing of `fugaro upgrade` is left, not even a tombstone (D950).** The retired `init` command has one
  (`internal/cli/retired.go`, W1.2) because it was two commands and a user typing it cannot tell them
  apart; `upgrade` is one command's worth of steps folding into another, and the user typing `fugaro
  upgrade` cannot be told apart from the user typing `fugaro update` — they want the same thing. A
  tombstone that prints a sentence and exits 1 turns a converging run into a non-zero exit for exactly the
  user the HARD RULE is about, and it would need its own census exemption class. `fugaro upgrade` becomes
  an unknown command, with cobra's own "unknown command" and the suggestions it already prints for a near
  miss. The **alternative** — a tombstone — costs one hidden command, one message constant, one test and
  one more entry in the census in `docs_live_init_census_test.go`'s shape, for a sentence the user did not
  ask for; the counter-argument is that a script or a note that says `fugaro upgrade` fails with cobra's
  message rather than with "this is now `fugaro update`", and the plan's doc task covers the living
  documents that say it.

## 6. Doctor (D942)

The plugin checks stop being about a checkout's pin and start being about **the plugin on this machine**.

**`plugin-pin` keeps its ID and every field's meaning.** It still reports the wiring in the checkout's
`.claude/settings.json` against this binary (`internal/pluginwire/status.go:110`, `Status`), and the
states keep their meanings. What changes is its **severity**: `not wired` is the normal state of a
repository that did not opt in, so it becomes `info` rather than `warning`, and it stops being the state
that turns a repository's CI red. Outside a checkout the check stays a warning today
(`internal/cli/doctor.go:324`) — that is the state `--strict` turns into a failure, and the design keeps
it, because a caller that asked doctor inside no checkout asked a question about a checkout.

**`plugin-install` is re-aimed at the user-scope install.** It compares the **user-scope** plugin's
version with **this CLI's** version, wherever the checkout's opt-in pin does not name a ref; where a pin
is present it still compares against the pin, because the repository asked for that ref. Its ID, its
`ok`, its `severity`, its `problem`, its `fix` and its JSON member keep their meanings — the state it
reports is still "the installed plugin is not the version Fugaro wants" — and what *changes* is which
version Fugaro wants and which install answers.

- `installed differs` is a **warning**, as today.
- `not installed` becomes a **warning** (today it is `info`, `doctor_render.go:174`'s
  `doctorMeasureIDs`): with the plugin at user scope, "no plugin on this machine" is a state `fugaro
  update` repairs, and a measurement nothing acts on is the silent pass the classes exist to prevent.
- `plugin-install` **moves into `doctorUpdatePrefixes`** (`doctor_render.go:100`) and its default-view
  line becomes ``run `fugaro update` ``, replacing ``run `fugaro upgrade --local` `` in
  `doctor_render.go:583`, `doctor_render.go:690` and `doctor_render_test.go:722`. The comment above
  `doctorUpdatePrefixes` already says this line was waiting for the change: "the converge's plugin stage
  rewrites the checkout's pin, not the installation: `fugaro upgrade --local` ... is what installs it."
  After §2 the plugin stage *is* the installation's, so the class's own fix is the true one.
- `pluginwire.Fix` for `Outdated`, `NotWired`, `Unpinned` and `InstalledDiffers`
  (`internal/pluginwire/status.go`) stops naming `fugaro upgrade --local` and names `fugaro update`.

**The one-screen doctor.** In the default view the two IDs read: `plugin-pin` — a repository that opts in
to the pin, and whether it matches this binary; `plugin-install` — the plugin on this machine, and whether
it is this binary's release. Neither changes class to `measure`; `plugin-install` changes from measure to
update, which is the class the owner's own list in `CLAUDE.md` puts it in ("the plugin pin: what `doctor`
already reports").

## 7. The session-start nudge (D945, D946, D947, D951, D952)

**The check (D945): `fugaro doctor --offline --json`.** The skills run it at session start. `--offline`
is one new flag on `doctor`: the full run with every check that answers from a file on this machine kept,
and every check that needs a credential, Terraform or the network dropped and said to be dropped. The
checks it keeps are the ones the owner's own rule names — the **update class** doctor already computes
(`doctorUpdatePrefixes`), which is `plugin-pin`, `plugin-install`, `base-image`, `image-config-<workflow>`,
`policy-rules-skew`, `policy-vs-jobs` and `policy-older-than-binary`, each of which is skipped rather
than guessed where its answer is not local.

*Why the nudge is `doctor`, and not `update --check`, in one sentence:* **the `working` skill runs in any
repository, enrolled or not.** A repository with no `fugaro.yaml` — whose owner has Fugaro installed and is
reading a run's logs, or following up a pull request — has no Fugaro checkout for `update`'s stages to
check, and `update --check` there answers "not a Fugaro checkout"; `doctor` answers `plugin-install`, which
on that machine is the whole question. The fact the nudge exists for is the machine's, so the door is the
machine's door.

*Why not `fugaro update --check --offline --json` (the alternative):* it would be a second JSON document
with a second version, a second set of check IDs and a second place to keep the "I cannot tell" sentences
in step with `doctor`'s — for facts `doctor` already reports in a document the console already reads
(`console/docs/json-contract.md` §6.4). It also puts a check flag on the one write command, where the
read-only door is `doctor`; a script that learned to poll `update --check` is a script one keystroke from
`update`. *Why not reuse `--plugin`:* `--plugin` is the plugin's own view and is what a repository's CI
runs with `--strict` (`internal/cli/doctor.go`'s own help); the nudge needs the whole update class, and
widening `--plugin` would change a documented flag's meaning for every CI that runs it.

**The contract (D946).** `--offline` adds **no new document and no new version**: `fugaro doctor --json`
is already in `console/docs/json-contract.md` §6.4, and the console reads it. It adds **one optional
field** to `doctorCheck`:

```
skipped  bool   // --offline did not run this check; its problem says why
```

Absent (and `false`) on every ordinary run, so every existing reader is unaffected; a check that is
`skipped` is also `ok: false` — never `ok: true`, which would be the silent pass `unread`
(`doctor.go:64`) exists to prevent — and carries a `problem` naming what it could not read. The field is
additive within the document's existing version, which is the rule for a member a reader outside this
binary consumes (`CLAUDE.md`, "A field never changes meaning"). `internal/cli/doctor_render.go` prints a
skipped check only under `--all`, in its own words, and counts it in no view's total.

**What the check can and cannot say (D951).**

| Question | Answerable offline? |
| --- | --- |
| Is the plugin on this machine this binary's release? | yes — `plugin-install`, from Claude Code's `installed_plugins.json` (`internal/pluginwire/status.go:221`, `installState`) |
| Does the checkout open the plugin the repository pins? | yes — `plugin-pin`, from `.claude/settings.json` |
| Is the repository's image on this release's base? | yes — `base-image`, from the local project config (`doctorProject.BaseImages`) |
| Does a workflow's job carry what the local config would write now? | yes where the local config is readable — `image-config-<workflow>` |
| Is the **published policy node** this binary's version? | **no.** It lives in the project's Realtime Database; reading it needs credentials. `fugaro update --check` already says so in its own words (`upgrade.go:501`, `checkPolicyCaveat`) |
| Is the **deployed security rules generation** this binary's? | **no**, for the same reason (`upgrade.go:568`, `checkRulesCaveat`) |
| Is a **newer release** available? | **no, and it is a network call — opt-in only (D952)** |

**The network stays opt-in (D952).** A newer release is the one fact that needs the network
(`internal/cli/doctor.go:172`, `cliVersionCheck` and `updatecheck.Refresh`). The skills never run it: the
session-start check is `--offline`, and the skills do not ask the user's permission to reach the network
on their behalf at the start of a session. The opt-in is doctor's own `cli-version` check in the full
run, which the owner runs when they want it, and `fugaro update --check` names `brew upgrade` in its
offline verdicts where a newer CLI would be the only fix.

**The exact skill sentence (D947).** `working`'s opening and `setup`'s first step say, once, in the
skill's own voice:

> At the start of a session, run `fugaro doctor --offline --json` once. If any check comes back with
> `skipped: false` and `ok: false`, tell the user in one sentence what it is — in plain words, not the
> check's own text — and offer the one command that fixes it: `fugaro update`, in their own terminal
> window, not through the agent. A coding agent's session cannot apply cloud changes, by design: never
> look for a flag or another command that would let this session apply those writes, and never unset the
> marker that says this session is one. If everything `--offline` could check is ok, say nothing and
> carry on: a session that opens with a status report nobody asked for is the failure this sentence
> exists to avoid.

**Half of that is not new wording, and that is the point.** The last two sentences are
`plugin/skills/upgrade/SKILL.md:27`'s, already pinned verbatim by `plugin/upgrade_skill_test.go:18,20`
("A coding agent's session cannot apply cloud changes, by design", "never look for a flag or another
command that would let this session apply those writes"). D953 moves the sentence into the skill that
becomes `update`, and D947 puts the same words at the top of `working` and `setup`; the pinned strings
travel with it rather than being rewritten, and the `plugin/` test that pins them keeps gating the release
without a new string to justify. The one clause this design adds is the marker half — "never unset the
marker that says this session is one" — because §8's migration is the first thing an agent session has a
reason to want around.

`setup` adds its own half, because it is the skill that runs before a repository is set up: if
`plugin-install` is not ok, the user has no Fugaro skills and the wizard's own step comes first.

## 8. Migration: a repository that carries an old pin (D943)

A repository wired by an earlier Fugaro carries a `fugaro@fugaro` pin in a committed
`.claude/settings.json`. That file must not break, and the user must not be handed a step.

- **`fugaro update` offers its removal.** The plugin stage prints the two entries it would take out (the
  `fugaro` marketplace and `enabledPlugins["fugaro@fugaro"]`) as a diff, says in one sentence what the
  repository loses (a teammate on this machine needs no commit to get the skills, and the skills are
  already installed at user scope), and removes them.
- **It never commits.** The change is the working tree's, like every other edit this CLI makes to a
  checkout's files; the user reviews it with `git diff` and commits it or does not.
- **`--yes` confirms it; a terminal asks.** It is a local file write on this machine, the same class the
  pin's own write was confirmed by. Refusing to remove without a typed confirmation would be the second
  command the HARD RULE forbids, in the shape of "run `fugaro update`, then answer a question you did not
  expect".
- **In a coding agent's session the diff is printed and nothing is written**, `--yes` included. The
  removal is a change to a file the repository commits, and an agent does not decide that. The stage says
  the exact line to run in the user's own terminal.
- **`fugaro` never unsets the marker to get around this**, and no flag, environment variable or
  "non-interactive" mode exists that would. The refusal is the one `fugaro update` already makes for the
  writes that reach outside the machine (its own help text, `--yes`), and it stays a refusal.
- **A pin that is not removable is not a failure.** A file that is not valid JSON, a fork's marketplace, a
  pin whose ref is not a release tag: each is left exactly as it is, with the existing refusal text, and
  the run continues to the next stage. `update` is converging a machine, not validating a repository.
- **Nothing is removed twice.** The offer fires while the entries are there and stops when they are gone,
  so a second `fugaro update` prints nothing about it.

## 9. The setup skill, and the tests that gate the skills (D944)

- **The setup wizard stops writing the pin.** `fugaro setup`'s plugin stage (the same
  `internal/cli/init_plugin_stage.go`) installs the plugin at user scope and writes no
  `.claude/settings.json`; `--pin` is the flag that keeps today's behaviour for a repository that wants
  it.
- **The `setup` skill loses step 9's sentence** about putting "the `.claude/settings.json` change the
  installation step already made (the plugin pin)" into the pull request
  (`plugin/skills/setup/SKILL.md:154`), because there is no such change by default. Its step 1 keeps
  `fugaro doctor --json` and gains the §7 sentence.
- **The `plugin/` tests that check skills against the CLI still gate releases**, and they are the reason
  §9 exists as a section rather than as a sentence in a doc task:
  - `plugin/plugin_test.go:134` pins the skill names `{"setup", "working", "routing", "parallelism",
    "upgrade"}`; `upgrade` is replaced by **`update`** — the skill is retitled, not dropped, because its
    job (check, explain, hand over the one command) is the job that stays.
  - `plugin/skills_lint_test.go:583`'s `userRunsAllowed` names `fugaro upgrade`; it becomes `fugaro
    update` (already in the list) and `fugaro upgrade` is removed.
  - `plugin/skills_lint_test.go:746`'s `upgradeYesOnly` allows `--yes` on `fugaro upgrade` in a
    `user-runs` block and on nothing else; it is re-pointed at `fugaro update`, which is now the command
    that carries the cloud half.
  - `plugin/upgrade_skill_test.go` becomes `plugin/update_skill_test.go` and is rewritten against the
    `update` skill's sentences; its `` ```bash user-runs\nfugaro update\n``` `` assertion survives
    verbatim, and so do the two sentences D947 moves into `working` and `setup`.
  - **The skill directory is renamed with the command (D953):** `plugin/skills/upgrade/` becomes
    `plugin/skills/update/`, its `SKILL.md`'s description and body are rewritten for the two-command
    world, and `reference/states.md` — which `internal/cli/upgrade_skill_doc_test.go:13` reads at that
    path and checks against `stepStates` and `upgradeSteps` — moves with it and loses the `pin:` and
    `plugin:` step rows that no longer exist. The **directory name is the skill's `name:`**, which is
    what `plugin/plugin_test.go` reads, so the two cannot drift apart.
  - `plugin/readme_test.go:45` lists `".claude/settings.json"` among the phrases the plugin README must
    carry; the README paragraph is rewritten to say the plugin is installed at user scope, and the list
    follows.
  - `internal/cli/upgrade_skill_doc_test.go` and `internal/cli/docs_upgrade_test.go` pin the old skill and
    the old command in `README.md`, `docs/release.md`, `.claude/skills/new-release/SKILL.md` and
    `docs/design/v1.md`; each is rewritten for the one command.
- **No skill gains `allowed-tools`.** The lint allows only `name` and `description` in frontmatter, and
  the refusal to pre-approve tools is unchanged.

## 10. Trade-offs, stated plainly (D940's cost, and the ones the move creates)

- **Every teammate installs on their own machine: `brew upgrade dimipaun/tap/fugaro`, then `fugaro
  update`.** That is a real cost the pin used to pay for them, and the design does not hide it: the
  session-start nudge (§7) is what makes it a two-command onboarding instead of a silent absence, and the
  skills' own text is what a user reads when the plugin is not there.
- **Version skew between teammates is possible and is not detected by the repository.** Two teammates can
  run two plugin versions, and one can run a plugin newer than their CLI (the marketplace follows the
  default branch, §4). Nothing in a repository can tell; `doctor` reports it per machine, and the
  session-start nudge is where a teammate sees it.
- **A repository that wants the skills required keeps the opt-in pin, and with it the trust surface.** A
  committed `.claude/settings.json` naming a marketplace is a file that makes every teammate's agent load
  third-party skill text once they trust the folder — which is why `fugaro update-skills` prints its
  notices before blessing it and why `pluginwire` refuses to rewrite a file it cannot parse. The opt-in
  makes that a decision a repository takes rather than a default it inherits.
- **Cloud-run agents are unaffected, and that is checked, not assumed** (F10, §3): the launch contract
  forbids plugin and settings-source flags, the runner's environment is scrubbed, and review mode strips
  `.claude`. A cloud run's behaviour does not change with the pin.
- **A user who never installs the plugin at user scope gets no skills in an interactive session**, where
  today a committed pin gives them to anyone who trusts the folder. The nudge is the answer, and it is a
  sentence in a skill — not a boundary. An agent that ignores its skill's first instruction is not
  stopped by anything here, which is the same honesty `docs/design/upgrade.md` states about its own
  three-command rule.
- **`--local`'s removal takes away a mode some notes use.** A document that says `fugaro upgrade --local`
  becomes `fugaro update` (or `fugaro doctor --offline` where it was asking a question), and the census in
  [reconciliation.md](reconciliation.md) §4 is the list of places that has to happen.

## 11. What this work does not do

- **It writes no code.** Four documents, one commit.
- **It does not change `fugaro update`'s cloud behaviour**, its confirmations, its typed project names,
  its `--yes` coverage or the checkout stage's parked-cloud machinery for an agent session.
- **It does not move the *skills' content* into the image or a package manager.** The plugin reaches people
  through Claude Code's marketplace, which is what the `claude` CLI speaks; a second distribution channel
  is a separate decision and is not taken here.
- **It does not add a `fugaro` command.** No `fugaro plugin` family, no `fugaro nudge`: the check is
  `doctor`'s, and the write is `update`'s.
- **It does not touch `internal/runner/runner.go`, `internal/cli/root.go`, `website/**`, `plugin/**` or any
  other file another 0.15.0 run owns.** The tasks in [implementation-plan.md](implementation-plan.md) name
  them; this pull request does not edit them.
