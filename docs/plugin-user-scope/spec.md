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
write the checkout stage is already `LocalUnderAgent` for (`init_checkout_upgrade.go:63`, and
`internal/initflow/stage.go:349` for the interface). So a coding agent's session moves the plugin, and no
longer stops at `left for you` for it. **That marker is about a session, not about a person:** it says
what the loop may apply under an agent, and it does nothing for a teammate at a terminal, whose run the
ordinary path drives (§11).

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
  same run with fewer steps, and `fugaro update-skills` is the command for the plugin alone. **That
  argument is about a coding agent's session and it does not carry to the teammate who is not the owner**:
  a marker is set by the session, not by a person's roles, so the teammate's run is driven by the
  ordinary path, where a stage that has something to do and no terminal to ask at stops the loop — which
  is §11's gap, D954 and U8. `--local`'s removal is therefore gated on U8, not on §2 alone. The **cost**
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
- `not installed` becomes a **warning** (today it is `info`, `doctor_render.go:203`, which is
  `plugin-install`'s row in `doctorMeasureIDs`): with the plugin at user scope, "no plugin on this
  machine" is a state `fugaro update` repairs, and a measurement nothing acts on is the silent pass the
  classes exist to prevent.
- `plugin-install` **moves into `doctorUpdatePrefixes`** (`doctor_render.go:117`) and its default-view
  line becomes ``run `fugaro update` ``, replacing ``run `fugaro upgrade --local` `` in
  `doctor_render.go:768` (`doctorUpdateFixes`, `:644`) and `doctor_render_test.go:729`. The comment above
  `doctorUpdatePrefixes` already says this line was waiting for the change (`doctor_render.go:88-91`):
  "the converge's plugin stage rewrites the checkout's pin, not the installation: `fugaro upgrade
  --local` ... is what installs it." After §2 the plugin stage *is* the installation's, so the class's own
  fix is the true one — and the plan-reader's "not here yet" note goes with the move.
- `pluginwire.Fix` **already points at `fugaro update` everywhere a fix names a command.** The tree
  moved under this design: a commit on this branch's own line of work (#1495, "every message that sent
  the owner to `fugaro upgrade --local` says `fugaro update`") re-pointed `Outdated`, `NotWired`,
  `Unpinned`, `InstalledDiffers` and `NotInstalled` at `fugaro update` (`internal/pluginwire/status.go:49`,
  `:55`, `:57`; `Foreign` names no command) and the same sentences in `internal/cli/init_plugin_stage.go`
  (`pluginFirstRun`, `:32`, and `pluginRefresh`, `:41`, both "Run fugaro update …"). What is **left** is
  `Newer`'s line
  (`status.go:51`, "fugaro upgrade never moves a pin down"), which goes with the command (U4), and the
  two `doctor_render.go` lines above, which U3 owns. The census and every line number in these documents
  were re-read on this branch's merged tree, not trusted from the day they were written
  ([reconciliation.md](reconciliation.md) §7).

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
  skills' own text is what a user reads when the plugin is not there. **For the teammate who is not the
  owner, the second command is not yet the one that does it** — §11 is where that is answered, and U8 is
  what closes it.
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

## 11. A teammate who is not the owner (F23–F25, D954, D955)

**The verification question, written down so it can be checked rather than assumed: a teammate who is not
the owner runs `fugaro update` in a checkout — what happens?** Every claim below was read in the tree on
this branch after `origin/main` was merged into it (`28425eba`), not assumed from the rulings.

**The answer in one sentence: what stops a non-owner is the converge's ordering, not a permission.** No
write `fugaro update` makes for the plugin needs an owner's credential — the install runs the `claude` on
the teammate's own `PATH` (`internal/cli/upgrade_plugin.go:26`, `claudeplugin.Decide`; F5: the argv is
already `--scope user`) and needs no project at all. What ends the run is the loop's own rule: it applies
each stage in order and **"stops at the first stage that needs the user or fails: nothing after it
runs"** (`internal/initflow/loop.go:329`, `converge`), marking everything after it `blocked: waits for
<stage>` (`:339`). Four of the stages in front of the plugin are cloud writes (`internal/cli/init_stages.go:169`,
`:182`), so a teammate's run ends before the plugin as soon as the first of those has something to do.

| update's stage, in order (`init_stages.go:169`, `:182`, `:188-195`) | what it does for a teammate who is not the owner | the credential it needs there |
| --- | --- | --- |
| preflight | the environment (`gcloud`, `terraform`, ADC), and **nothing runs at all** if it is not ready (`init_stages.go:296`) — a teammate who ran `gcloud auth application-default login` passes, which is all `README.md` asks of a launcher | none |
| installation | writes **this machine's** local config from the installation's outputs and applies nothing (`newMachineConfig`, `init_new_machine.go:42`), then prints the roles and the one-line command an owner runs to grant them (`roleNotes`, `:117`) | read access to the state bucket's outputs, no apply |
| firebase | skipped unless `--firebase-project` | — |
| images | **the first stage that can end the run**: it is not exempt from `newMachineGuard` (`init_new_machine.go:94`), and the release the teammate just installed is exactly what gives it something to do | Artifact Registry write, and a billable Cloud Build |
| secrets | the same guard (`initsecrets.go:620`) | Secret Manager |
| **plugin** | **exempt from the guard by name** (`init_new_machine.go:95` exempts `Installation` and `Plugin`; its comment says why — "the plugin wiring writes a local file only … the run still wires the plugin, with its own confirmation") and it needs no cloud — **but only if the run reaches it** | none |
| repository | the guard refuses it; it is a cloud write (the onboarding creates Terraform resources) | the operator role |
| rules | a cloud write | deploying the security rules |
| policy | a cloud write | the project's Realtime Database |
| checkout | **shaped for a teammate**, and this part already works: its `cloud` step skips with its own sentence ("no local project config selects this checkout …: the cloud step is for the operator who set the project up", `upgrade_cloud.go:80`), and what is left is the machine's own writes | none |

**Where that lands in the teammate's ordinary case, and it is the release's own day.** A machine that has
never run `setup` has no local config (`README.md`'s own words: a launcher needs the CLI and ADC, "no
`fugaro setup`"), so the installation stage takes the new-machine path, the guard arms, and the next cloud
stage that has something to do refuses it. On the day of a release there always is one: the images stage
has this release's base image to pin or copy wherever the checkout names a base kind in its `fugaro.yaml`
(the ordinary checkout) or the run passes `--firebase-project`. The run writes the config, prints the role
notes and stops: **the plugin the teammate ran `fugaro update` for is `blocked`, and so is the checkout
stage.** The guard's exemption is the code's own statement that the plugin's writes are the machine's; the
images stage simply comes first, and an exemption by stage name cannot see that.

**So `fugaro update` does not today do what `fugaro upgrade --local` did for a non-owner, and the gap is
worth naming precisely.** `--local` was defined as "pin and plugin only … the cloud, rules and policy
steps are skipped before they read anything, never attempted … the exit code is 0 when the local steps
succeeded" (`upgrade.go:141-144`): no cloud stage could stop it, because it had none. Plain `fugaro
update` reaches the local half only where every stage in front of it is **skipped or done**. Two changes
make the one command true for this reader, and both are U8:

1. **D954 — a stage that is only this machine's is not blocked by a cloud stage that stopped.** The
   property is already in the tree three times by hand: `LocalUnderAgent` ("every write of the stage's own
   is on this machine", `internal/initflow/stage.go:349`), `newMachineGuard`'s name list
   (`init_new_machine.go:95`) and `NeedsBilling`'s case list (`stage.go`'s `case Preflight, Project,
   Plugin, Checkout`, under a comment that calls the plugin "the plugin wiring, and the checkout's own
   steps, whose pin and plugin write the checkout's settings and the user's own Claude Code"). `initflow`
   gains the marker itself, the plugin stage implements it, `converge` applies such a stage even after
   `stopped != ""` and reports it in its own words (`machine-local: ran although images waits for you`),
   and `newMachineGuard` reads the marker instead of the stage's name. **The alternative is to move the
   plugin stage to just after `installation`** (`init_stages.go:169`) — a smaller diff, and it is not the
   recommendation: a plugin stage that returns needs-you would then end the run *before* the images,
   secrets and repository work the owner's own run does today, and the stage order is what the plan view
   and `docs/design/upgrade.md`'s step list show every reader. **The cost of the recommendation**: the
   loop's "nothing after it runs" gains one exception, and the marker's rule has to stay true by hand —
   a marked stage reads no cloud and writes nothing outside the machine.
2. **D955 — the user-scope install stops asking about the repository.** The plugin stage consults the
   checkout's repository gate (`init_plugin_stage.go:109`, `authState`), and where the checkout's
   repository is not in the machine's config, `update`'s `enrolling()` is false (`internal/cli/init.go:242`)
   — so it is needs-you with the line naming `fugaro setup` (`:112-117`, `enrolLeft`). That gate belongs
   to the **pin**: it is what authorises writing `.claude/settings.json` in somebody's checkout. A
   machine's plugin install concerns no repository, and a new machine's config lists no repositories at
   all (`newMachineConfig` copies the installation's outputs, which carry launchers, operators, cleanup,
   log isolation and the budget backend), so the gate would send every teammate to a second command for a
   write that is not the repository's. At user scope the **install half drops the gate**; the gate stays
   for the checkout write U5 adds (the pin's removal), which is also gated by `namesAnotherProject`. The
   same reasoning removes the stage's need for a checkout: outside one it prints the settings snippet and
   skips (`:81-88`), and that snippet is the **pin's**, which is opt-in (D949) — a machine install
   happens anywhere. **The alternative**: keep the gate as it is, and every teammate is sent to `fugaro
   setup` before their own machine's plugin is installed.

**A pin the plugin stage cannot move must not end the run, and today it does.** §8 says "a pin that is
not removable is not a failure … each is left exactly as it is, with the existing refusal text, and the
run continues to the next stage" — a `pluginwire.ForeignError`, a `ForkError` and an invalid file are
needs-you in the code (`init_plugin_stage.go:128-136`), and needs-you is the loop's stop. U8 makes those
three the shape the loop already has for "done with what this session may do, with something only the
user can do": apply, report the refusal as `Outcome.Left` (which continues **and** sets the exit code,
`loop.go:439`) and leave the file alone. This is F25, and it is the one part of the gap that is a
contradiction inside this design rather than a consequence of the move.

**The exit code changes for this reader, and that is the honest part.** `fugaro upgrade --local` exited
**0** when the local steps succeeded (`upgrade.go:144`); a teammate's `fugaro update` ends **1**, because
the run really does have something left for a person — the owner's cloud stages, in `left_for_you`
(`initflow.Result.ExitCode`). A script that read `--local`'s 0 reads `--json`'s `left_for_you` instead.
The HARD RULE is not what this costs: the run that must exit 0 after doing everything is the **owner's**,
and it does.

**The open item: what a teammate's ADC may do is IAM, and this design cannot see it.** Whether that
teammate may read the state bucket's outputs, plan the installation, or write Artifact Registry is
decided by bindings in the project, not by anything the binary reads — so **which** cloud stage ends a
given teammate's run is not answerable from the code, and this document does not pretend otherwise. It
does not have to be: U8 makes the local half independent of where the cloud stops, rather than making the
cloud stages succeed. What is deliberately not promised is the other half — that a teammate's own `fugaro
update` afterwards converges the installation. That is the owner's run, and §10 says so.

**The two documents that send a teammate to a command are not this pull request's, and the order
matters.** `README.md` (~line 46, "run `fugaro upgrade --local` in the checkout … then restart the
session") and `website/data/install.yaml` (~line 119, the plugin path's third command) both still name
`--local`; both paths belong to other runs (U7 names them, and this design directory may not edit them).
**The ordering rule U8 puts on them:** neither may be rewritten to `fugaro update` before U8 lands, and
U4 may not delete `--local` (or `fugaro upgrade`) before U8 lands either — a release that deleted the old
spelling while the new one cannot yet reach the plugin would leave a teammate with a sentence and no
command. That is the one place in this plan where the order is not a preference but a
correctness condition, and [implementation-plan.md](implementation-plan.md)'s table carries it.

## 12. What this work does not do

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
