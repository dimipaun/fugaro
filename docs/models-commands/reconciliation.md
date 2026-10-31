# `fugaro models`: reconciliation against the code

Read against `origin/main` at `2d849e802`. Line numbers are that commit's. Each finding is
**Exists**, **Missing** or **Conflicts**, with its consequence for the spec or the plan.

## 1. What exists

**F1: Exists. The route table and its rules.** `config.ModelProvider` (`internal/config/providers.go:41`)
carries `models`, `allow_data_to`, `route_fee_pct`, `require_zdr` and `price_policy`; `Matches` (`:183`),
`AllowsData` (`:200`) and `ProviderFor` (`:274`) answer "which route serves this ID and may it see this
repository". `ValidateModelProviders` (`:323`) refuses overlapping routes and Claude claims;
`CheckModelPattern` (`:863`) and `CheckClaudeClaim` (`:842`) are the per-entry rules, and the variant refusal
is in `CheckProviderPolicy` (`:987`, the message at `:1000`). *Consequence:* `models add` reuses these rules
on the edited config; it writes none of its own (spec §4 steps 1–2).

**F2: Exists. One price table builder.** `config.PriceTable` (`internal/config/pricetable.go:40`) builds the
embedded Claude rows, then the catalog rows for every model a catalog-eligible route claims, then the owner's
`model_prices` last. The CLI's one wrapper is `priceTableFor` / `priceTable` (`internal/cli/prices.go:23`,
`:35`), which `validate`, `diagnose` and `budget prices` share. *Consequence:* `models ls`, `search` and `add`
price through `priceTable`, so they cannot disagree with a run.

**F3: Exists. A price listing.** `fugaro budget prices [--json]` (`internal/cli/budget.go:1455`, run at `:1551`)
prints every priced row with its source, the catalog's tier, bound, endpoint counts, the tier's dearest prices
(`priceCatalog`, `TierInputMaxPerM`/`TierOutputMaxPerM`) and the catalog's age. *Consequence:* `models ls`'s
models section is a superset of this listing (it adds routes, allow-lists and usage). The plan shares the row
builder rather than writing a second one (Task M1), and `budget prices` stays as it is (D794).

**F4: Exists. The catalog is a curated, offline file.** `catalog.Load` (`internal/pricing/catalog/catalog.go:51`)
reads the embedded `catalog.json`; `File.CheckedAt` (`:230`) dates it and staleness is `catalog.StaleAfter`
(applied at `internal/pricing/catalog_apply.go:92`). The rows come from an allowlist in
`scripts/gen-price-catalog.sh`, grown by a reviewed diff. *Consequence:* `models search` searches a handful of
OpenRouter rows plus the embedded Claude table. That is small on purpose, and the output says what is not there
and how to price it (D788).

**F5: Exists. The pins and allow-list rules.** `config.CheckPins` (`internal/config/pins.go:34`) requires each
of coder, reviewer and background to be priced; `config.CheckAllowed` (`:154`) holds them to the policy's
`allowed_models` and the routing switch; `config.EffectiveBackground` (`internal/config/providers.go:1098`)
gives the background model when it is unset. `fugaro validate` runs them (`internal/cli/validate.go:265`,
`:385`). *Consequence:* `models use` checks its result with the same calls `validate` makes (spec §6).

**F6: Exists. Where each key may be set.** `config.Scopes` (`internal/config/scope.go:59-61`) allows
`agent.models.coder`, `.reviewer` and `.background` in the project layer and the repository; `budget.allowed_models`
(`:76`) is repository-only in `fugaro.yaml` (a tightening of the owner's ceiling). Providers, prices and routing are
not `fugaro.yaml` keys at all: they are fields of the local config (`internal/localcfg/localcfg.go:106`, `:109`,
`:131`) that only the owner writes. *Consequence:* the owner-only boundary in spec §8 is already the code's;
`models use` writes only `agent.models.<role>`, and only in the repository (D792).

**F7: Conflicts. `price_policy` and `require_zdr` are not yet deployable.** `catalogShipped` is `false`
(`internal/cli/recipes_skew.go:150`), so `fugaro update --repo` refuses a route that sets either switch
(`refuseCatalogSwitches`, `:1510`; reached through `gateCatalogSwitches`, `internal/cli/init.go:3473`).
`docs/multi-model.md` §2 says to leave both out. *Consequence:* the brief's "explain the cost spread and
`price_policy`" is an explanation only: `models add` prints the spread and what `highest` would do, and writes
no switch until `catalogShipped` turns true (D795).

## 2. What is missing, and what conflicts

**F8: Missing (dependency). `fugaro update` does not publish the policy.** `buildProjectPolicy`
(`internal/cli/project_cmd.go:655`) copies `model_prices` (`:908`), `allowed_models` (`:753`), `routing` and
`providers` (`policyProviders`, `:849`) into the node, and it is reached only by `fugaro project policy publish`
(`:95`). The update-converge work adds the migration library (open PR #1408, UC1 part a) and, in part b, the
`fugaro update` stage that publishes and migrates the node. *Consequence:* the `models` commands must not ship
before that stage, or the owner's next step after `models add` is a second command, which is what the HARD RULE
forbids. The plan's first task depends on it (implementation-plan.md, "Order").

**F9: Conflicts. A route change still needs `fugaro update --repo` in every repository.** A run routes by the
`FUGARO_MODEL_PROVIDERS` copy in its own job's environment, cut to its `allow_data_to`, not by the node's
`providers` (`docs/multi-model.md` §2; `fugaro upgrade`'s drift note, `internal/cli/upgrade_cloud.go:428`, says so).
`fugaro upgrade` already detects that drift, per repository, and only prints the command. *Consequence:* `models
add` of a model no route claims yet changes a route, and that change needs one `update --repo` per allowed
repository today. D787 decides how `fugaro update` carries it instead.

**F10: Missing. No command writes the local project config except the converge doors.** `initRun.writeConfig`
(`internal/cli/init.go:2373`) and `restoreRun.writeConfig` (`internal/cli/restore.go:949`) show a line diff
(`lineDiff`, `internal/cli/init.go:2629`), back up the old file (`backupFile`, `:2566`, `<file>.bak-<timestamp>`,
mode 0600) and write atomically (`writeFileAtomic`, `:2602`). Both marshal the whole struct
(`localcfg.Config.Marshal`, `internal/localcfg/localcfg.go:1558`, `yaml.Marshal`). *Consequence:* the backup,
diff and atomic write are reused as they are. The marshalling is not, because it drops comments and order
(F11, D790).

**F11: Conflicts. A struct round trip loses a hand-edited config's comments.** `yaml.Marshal` of `localcfg.Config`
writes no comments, reorders keys to the struct's order, and turns an `allow_data_to:` key with every entry
commented out into an absent key. That last case is not cosmetic: the loader refuses a written-but-empty
`allow_data_to` (`ModelProvider.UnmarshalYAML`, `internal/config/providers.go:106`), and a re-marshal would change
the refusal into "every repository". *Consequence:* the `models` writes edit the YAML node tree (spec §9). A
precedent for a targeted edit exists: `setGCPProjectLine` (`internal/cli/init_fugaroyaml.go:61`) edits one line
of `fugaro.yaml` and keeps the rest.

**F12: Missing. No command lists routes, allow-lists and repository usage together.** `budget prices` lists
prices; `project policy show` (`newProjectPolicyShowCmd`, `internal/cli/project_cmd.go:2157`) lists the published node; `doctor`'s
`routing-resolution` check prints the routing. Nothing joins routes, prices, allow-lists and the pins of each
repository. *Consequence:* `models ls` is new. Adding a route (a new `providers` entry with its URL, auth and
secret name) is not asked for and stays a hand edit; `models add` extends an existing route only.

**F13: Exists, offline. Each repository's pins can be read without the network.** The remembered checkouts
(`internal/localcfg/checkouts.go`, `CheckoutsVersion` at `:57`) record where this machine has each repository;
`agentAtRef` (`internal/cli/run.go:1661`) already reads a `fugaro.yaml` at a ref from a local checkout with no
fetch; `layerRoutes` (`internal/cli/layer_resolve.go:357`) resolves the project layer's part. *Consequence:*
`models ls` and `models rm` read usage from local checkouts only, and say which repositories they could not see
(D783, D793).

**F14: Exists. The coding-agent marker.** `agentenv.Marker` (`internal/agentenv/agentenv.go:33`), wrapped as
`agentEnv` (`internal/cli/initsecrets.go:146`), is what `setup`, `update` and `image refresh` read to apply nothing
in an agent's session. *Consequence:* `models add` and `rm` use it the same way (D789).

**F15: Exists. The node's shape.** `projectpolicy.Policy` (`internal/projectpolicy/policy.go:129`) carries
`budget.allowed_models` (`Budget`, `:260`), `providers` (`Provider`, `:288`, the same carrier as the local config),
`model_prices` (`Prices`, `internal/projectpolicy/prices.go:34`) and `routing`; it lives at `config/policy/shared`
(`internal/projectpolicy/paths.go:20`, `PathShared` `:29`) at `Version` 4 (`internal/projectpolicy/version.go:79`).
*Consequence:* no new node member is needed. The `models` commands change only the local config, and the node
changes through the publish path that already exists.

**F16: Exists. The console and the mobile app read no model configuration.** The console's sources are the CLI
documents listed in `docs/design/desktop-console.md` §2.1; model names reach it only through each run's
entry in `fugaro watch --json`. *Consequence:* no console or mobile task (spec §10).

## 3. Decisions D780–D795

**D780–D783 answered 2026-10-30 (D783 against the recommendation); D784–D795 answered 2026-10-31, all as recommended.** The recommendation comes first; the plan is written to the answers.

| D | Question | Recommendation | The alternative, and its cost | Answer |
| --- | --- | --- | --- | --- |
| **D780** | Where does `models add` (and `rm`) write? | **The local project config only.** The node is published by `fugaro update`'s policy stage (F8). | Also publish the node: a second command that writes the policy outside `update`'s confirmation, its migration and its version checks, and a node that can disagree with the local config the next `update` publishes | **Answered 2026-10-30: local project config only; `fugaro update` publishes the policy** (the recommendation). Why: keeps `update` the only door to the cloud. |
| **D781** | Does `models use` open a pull request or write the working tree? | **Write the working tree of the repository's checkout, after a diff and a confirmation; never commit or push.** The user's own branch and review carry it. | Open a pull request from the CLI: needs a write path to GitHub and Bitbucket on the developer's machine, with the user's credentials, which the CLI does not have today; and it is one more place that pushes | **Answered 2026-10-30: edit the working-tree `fugaro.yaml` after a diff and a confirmation; never commit or push** (the recommendation). Why: the user's own branch and review carry the change. |
| **D782** | A model the catalog does not price: is a price required? | **Required: `--price-in` and `--price-out`, refused without.** | Optional, charged at the dearest tier: the catalog has no tier for such a model, so "dearest" means the table's maximum rates (`priced_as: max`, `docs/multi-model.md` §6), which over-charges a cheap model by around two orders of magnitude and makes the per-run cap bind at once | **Answered 2026-10-30: `--price-in` and `--price-out` required for an uncatalogued model** (the recommendation). Why: a dearest-tier fallback over-charges by about two orders of magnitude. |
| **D783** | Does `rm` refuse a model a repository still pins? | **Refuse, naming each repository and role it found; warn for each recorded repository with no checkout on this machine.** | Remove with a warning (the owner's choice, not the recommendation): the next run of that repository fails at launch with an `infra_error` (no provider claims the model), which is the failure the refusal would have prevented; the warning text says so | **Answered 2026-10-30: not the recommendation. `rm` is allowed, with a warning naming each pinning repository and role; no refusal.** Why: the owner prefers an unblocked edit, knowing the next run of that repository fails with a no-provider error. |
| **D784** | How stable is `models ls --json`? | **A versioned document (`"version": 1`) with a JSON Schema in `schemas/`; members are only added within a version, and a renamed or re-meant member is a new member (the no-semantic-field-changes rule).** | Unversioned: the console and agents that read it break on the first change, with nothing to check against | **Answered 2026-10-31: the recommendation.** |
| **D785** | What does `add` write into a route? | **Nothing when a route already claims the ID; otherwise the exact ID appended to one route's `models`, never a pattern. Claude IDs are refused (they go through `routing.claude`).** | Write a family pattern: claims models nobody priced or reviewed, and widens where a repository's code may go by more than the owner typed | **Answered 2026-10-31: the recommendation.** |
| **D786** | Does `add` touch `budget.allowed_models`? | **Append when the list is set; leave it absent when it is absent.** | Never touch it: an added model stays unusable wherever the list is set, and the owner learns that at the next launch | **Answered 2026-10-31: the recommendation.** |
| **D787** | How does a route change reach every job without `update --repo` per repository? | **`fugaro update` re-converges each recorded repository whose job's provider copy differs from what the local config would write now, behind its one confirmation (the drift `fugaro upgrade` already computes, F9).** Belongs to the update-converge work; the `models` commands wait for it. | (a) Runs read `providers` from the node: changes the source of the routing decision, a security-sensitive move with its own review; (b) document `update --repo` per repository: a second command, which the HARD RULE forbids | **Answered 2026-10-31: the recommendation.** |
| **D788** | Where does `search` look? | **Offline: the built-in catalog and the embedded Claude table.** | Query OpenRouter's public model list: a network call, rows nobody reviewed or priced through the allowlist, and an answer that differs from what a run will charge | **Answered 2026-10-31: the recommendation.** |
| **D789** | In a coding agent's session? | **`add` and `rm` print the diff and the command and write nothing, `--yes` included; `use` (the repository's own file) is allowed; `ls` and `search` always run.** | Allow the writes: an agent could widen where code goes or what it costs by editing the owner's file, with no human confirmation | **Answered 2026-10-31: the recommendation.** |
| **D790** | How is the file written? | **Edit the YAML node tree of the touched keys only; re-parse strictly and compare the decoded configs; reuse `backupFile`, `lineDiff` and `writeFileAtomic`.** | Marshal the struct: drops comments, reorders keys and turns a commented-out `allow_data_to` into "every repository" (F11), which fails the migration requirement | **Answered 2026-10-31: the recommendation.** |
| **D791** | Which roles does `use` take? | **`coder`, `reviewer`, `background` (the `agent.models` keys today); the review levels' keys are added by the task that adds them.** | Add level names now: names keys that do not exist yet, and guesses the outcome of the review-levels tasks | **Answered 2026-10-31: the recommendation.** |
| **D792** | Which file does `use` write? | **The repository's `fugaro.yaml` only.** | Also the project layer: that file is in the runs bucket, which every launcher can write, so a CLI write there is not an owner-only act; a separate decision if wanted | **Answered 2026-10-31: the recommendation.** |
| **D793** | Where does `ls` read each repository's pins? | **The remembered local checkouts, at the base branch, with no fetch; a repository with no checkout is shown as unknown.** | Read through the git provider's API: a network call with a credential on the developer's machine, for a listing | **Answered 2026-10-31: the recommendation.** |
| **D794** | Command name and relation to `budget prices` | **A new top-level `fugaro models`; `budget prices` stays, and both share one row builder.** | Fold into `budget` (`budget models`): routes and allow-lists are not budget, and the owner asked for "models" | **Answered 2026-10-31: the recommendation.** |
| **D795** | `price_policy` and `require_zdr` in `add` | **Explain only (the spread, the bound charged, what `highest` would charge) and write no switch while `catalogShipped` is false (F7); a `--price-policy` flag comes with the release that lifts the refusal.** | Write the switch now: `fugaro update --repo` refuses the route, so the owner's next step fails | **Answered 2026-10-31: the recommendation.** |
