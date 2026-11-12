# Model A/B testing

A project can sample a share of its code runs and run each of them twice: the **champion**, the run the developer sees, and a **challenger** on another coder model — the same task, at the same commit, from the same recipe, rigor mode and tier. The challenger is a shadow: it pushes nothing, opens no pull request, comments on nothing and holds no git write credential, so a sampled task still leaves one branch and one pull request. Both arms upload the patch they produced.

`fugaro ab` reports what the pairs did. It switches no model on its own: it recommends, and the decision is the owner's.

A/B is **off by default** — a project that publishes no `ab:` block samples nothing, and so does a repository that turns it off.

## Turn it on

**Per installation.** The block is the project layer's, so it is written once for the project and published:

```yaml
ab:
  rate: 0.05                      # share of eligible runs sampled, up to 0.10 (0, the default, is off)
  challengers: [claude-sonnet-5]  # the models that may run as the challenger, in priority order
  max_pairs_per_day: 5            # pairs per repository per UTC day (5 when the key is unset)
  judge_model: claude-opus-5-5    # the model a pair's blind judge run uses (below)
  exchange_rate: {pro: 0.5}       # what a mode accepts as the challenger's extra median cost per pair
```

Edit `$XDG_CONFIG_HOME/fugaro/projects/<project>.layer.yaml`, else the same path under `~/.config`. `exchange_rate` is optional: a mode it leaves out takes the figure Fugaro ships — 0 for POC, 0.25 for Startup, 0.5 for Pro, 2 for Critical.

Publish the file:

```bash
fugaro config publish
```

A coding agent's session is refused that write; run it at your own terminal. `fugaro update` publishes the same file, so an installation already being updated needs no second command, and `fugaro config layer` prints what the project holds now. `fugaro doctor` reports a checkout whose copy of the layer has drifted from the published one.

**Per repository:** a repository's own `fugaro.yaml` may write `ab:` with `rate: 0` alone, which never samples that repository. Every other key of the block is the owner's, because a repository author who could name a challenger could send that repository's code to a provider the owner opened only to others.

## What gets sampled

A launch is paired only when all of this holds: it is a first run of a code task at POC, Startup, Pro or Critical; the project's `rate` draws its run ID (a hash of the ID, never a random number); the repository is under `max_pairs_per_day` for the UTC day; one of `challengers` differs from the champion's coder and passes every check the champion's own model passes — the run's allow-list, a route that claims it and names the repository in `allow_data_to`, a price, an API-key credential, and a provider gateway in `enforce` mode; the run has a dollar cap and the day holds five per-run caps of headroom; and both arms pass the launch's budget pre-check.

A Hotfix, a follow-up, a retry, a best-of-N attempt, a review, artifact or digest run, and a launch whose `--run-id` you chose (the hash would be steerable) are never sampled. Where anything above fails, the champion launches alone and the launcher prints one line saying why, as `--json` does too. Two facts refuse every challenger at once: a Bitbucket repository, and a GitHub repository whose App key the credentials service holds, because neither can mint the read-only token a challenger's clone needs.

Both arms share one batch label — `ab-pair-<the champion's run ID>`, or the label the launch passed with `--batch`, which wins over the derived one — so `fugaro runs ls --batch <label>` shows the pair either way. The challenger's outcome is `none` and it owns no pull request: the work to continue is the champion's.

## Read the result

`fugaro ab` scores the pairs from the run records alone — no model call, no judgement — so the same records always give the same table. One row per rigor mode × tier × model pair:

- **PAIRS** is how many valid pairs the row holds; a pair whose arms differ in start commit, mode, tier or recipe, or whose arm never started, is in no figure at all.
- **WINS** and **LOSSES** are the challenger's, and **TIES** decided nothing — no figure is drawn from one. **CHALLENGER WIN 95%** is the interval around its win share.
- **COST** is the median pair cost ratio, the challenger's model dollars over the champion's. **HALTS** is each arm's halt count: a halt is already a loss, and the count says how often a limit rather than the work decided a pair. **FOLLOW-UPS** counts the runs continuing the champion's pull request, which is context only.
- **ADVICE** is the rule at the mode's exchange rate: the challenger when it is clearly better (the lower bound of its win share above a half) and its median cost ratio is within the mode's rate, or when it is not worse (the upper bound of its loss share below 0.6) and cheaper; otherwise the champion stays. A row with fewer than 20 decisive pairs says `no conclusion: too few decisive pairs`, which is not the same answer as keeping the champion.

`--since` is how far back the read goes (30 days by default, `0` for no bound) and `--repo owner/name` narrows it to one repository; `--json` prints the rows and their recommendation objects as one document. No person, repository or task text is in either. `fugaro ab` is not in the MIT core build: a build without it answers that this build does not include it.

## The blind judge

`ab.judge_model` names the model a pair's judge run calls. The judge is the `ab-judge` recipe, and `fugaro run --recipe ab-judge` starts it — one judge run per repository at a time, so a launch is refused while one has not finished, and a project that publishes no `ab.judge_model` has nothing to judge with.

It lists the pairs whose arms are both complete and unjudged from the last 14 days, shows each pair's two patches as A and B with no run, branch, model or commit in them, and makes one tool-less model call per pass, twice with the order swapped. Only an arm both passes agreed on is decided; anything else is a tie. The verdicts are written under the judge run's own prefix.

Detail, and the decisions behind it: [project-layer.md](../project-layer.md) §5 and §7, [model-ab-testing/reconciliation.md](../model-ab-testing/reconciliation.md).
