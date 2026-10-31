# `fugaro models`: the spec

The commands below are written to the recommended answer of each decision (D780–D795,
[reconciliation.md](reconciliation.md) §3; D780–D783 answered 2026-10-30, D784–D795 answered 2026-10-31 as recommended). A decision answered the other way changes the
paragraph that names it. Repository names are placeholders (`owner/repo-a`, `owner/repo-b`).

## 1. The rule this serves

`fugaro update` is the whole upgrade (`CLAUDE.md`, HARD RULE). So:

- The `models` commands **edit local files and read**. None of them writes the published policy
  node, a job, a secret or anything else in the cloud (D780).
- Every change they make reaches the cloud through the next `fugaro update`, which already re-converges
  the installation and, with the policy stage the update-converge work adds (0.14.1 / 0.15.0, part b
  of UC1), publishes the policy. Each write command ends by printing that one command and nothing
  else (no `project policy publish`, no `update --repo` per repository, D787).
- A change `fugaro update` cannot yet carry is a gap in `update`, not a step for the user (F9).

## 2. `fugaro models ls [--json]`

Offline. Reads the selected project config, the built-in price catalog, the embedded Claude price
table and, for repository usage, this machine's remembered checkouts (D793). One table per section:

1. **Routes.** Each `providers` entry: its name, `base_url` host, the `models` it claims (exact IDs and
   patterns as written), `route_fee_pct`, and `allow_data_to` (or "every repository" when absent or
   `["*"]`, the owner's 2026-10-13 ruling). The direct Anthropic route is listed as `direct` with the
   Claude IDs the embedded table prices, and `routing.claude` (when set) shows which Claude IDs it sends
   down a provider route.
2. **Models.** Every model a run can name today: each exact ID a route claims, each `model_prices` key,
   each `budget.allowed_models` entry and each model a repository pins (section 4). For each: the route
   that serves it (or "no route": refused at launch), the price row's source (`catalog`, `local`, `embedded`,
   or `none`), and for a catalog row its tier, the bound charged, the endpoint count and the **spread**
   (the tier's dearest input and output price over the bound charged, as `fugaro budget prices` and
   `fugaro validate` already print it). The catalog's `checked_at` and age head the section, with the
   30-day staleness warning the catalog already carries.
3. **Allow-lists.** For each model: which repositories may use it, the intersection of the route's
   `allow_data_to` and `budget.allowed_models` (when set). A model `budget.allowed_models` leaves out is
   shown as refused for every repository.
4. **Usage.** For each repository the project config records (`repos:`): its `agent.models.coder`,
   `agent.models.reviewer` and `agent.models.background` (the effective background when unset,
   `config.EffectiveBackground`), read from the repository's `fugaro.yaml` at its base branch in this
   machine's checkout, resolved over the project layer when the machine holds the layer's cached copy.
   A repository with no checkout here is listed as `unknown (no checkout on this machine)`, never
   guessed. A row whose pin no route serves, or no price prices, is flagged with the refusal a launch
   would give.

`--json` prints one document, `{"version": 1, "project": ..., "catalog": {...}, "routes": [...],
"models": [...], "repositories": [...]}`, whose members are only ever added to within a version
(D784). It has a JSON Schema in `schemas/` like the other wire formats.

## 3. `fugaro models search <text> [--json]`

Offline. Searches the built-in price catalog and the embedded Claude table by substring of the model
ID (case-insensitive), and prints for each hit: the ID, the source (`catalog` or `embedded`), the tiers
the catalog carries (`zdr`, `all`), each tier's price range and endpoint count, the context and output
limits, and whether a route of the selected project already claims it (and which). It never reaches
the network (D788). A model the catalog does not carry is not found, and the output says so with the
one way to use it anyway: `fugaro models add <id> --price-in X --price-out Y`.

## 4. `fugaro models add <id> [--route NAME] [--price-in X --price-out Y] [--yes]`

Edits the **selected local project config** and nothing else (D780). In order:

1. **Refuse what no route may serve.** A Claude ID (`claude-*`, `anthropic/*`) is refused with the
   pointer to `routing.claude` (a Claude model is served direct, or routed by the owner's
   `routing.claude` switch, never added as a provider model by this command; D785). A variant suffix
   (`:free`, `:nitro`, `:online`) or an alias is refused, as `fugaro validate` refuses it. With no
   `--route`, the route is the one whose `models` already claim the ID; if none does and exactly one
   route exists, that route; otherwise the command refuses and names the routes to choose from.
2. **Claim.** If the route's `models` already claim the ID (an exact entry or a pattern), the route is
   not changed. Otherwise the exact ID is appended to that route's `models` list, never a pattern
   (D785). The result must pass `config.ValidateModelProviders` (no two routes overlap, no Claude claim).
3. **Price.** A model the catalog prices needs no price: the command prints the row it will be
   charged at, the spread to the tier's dearest endpoint, and what `price_policy: highest` would do,
   stating that the switch is not yet accepted by `fugaro update` (F7, D795). A model the catalog does
   not price, or any model on a route that is not catalog-eligible, **requires** `--price-in` and
   `--price-out` (dollars per million tokens), written as a `model_prices` entry with the cache fields
   left to their defaults (D782). Prices given for a catalog model become a `model_prices` entry that
   replaces the catalog row, and the command says so.
4. **Allow.** When `budget.allowed_models` is set, the ID is appended to it (otherwise no run could
   use the model); when it is absent, it stays absent (D786).
5. **Check, show, back up, write.** The new file must parse with `localcfg.Parse` (strict) and pass
   the same provider, price and routing checks `fugaro validate` runs. The command prints the line diff,
   asks for confirmation at a terminal (`--yes` confirms outside a terminal), backs up the old file to
   `<file>.bak-<UTC timestamp>` (mode 0600) and writes the new one atomically (temporary file, rename,
   mode 0600). Only the keys it changes are rewritten: comments, ordering and every other key are kept
   byte for byte (D790, §9).
6. **Hand off.** It prints `next: fugaro update` and, when a repository pins nothing that uses the
   model, `fugaro models use <repo> <role> <id>` as the way to use it.

## 5. `fugaro models rm <id> [--yes]`

The reverse of `add`, through the same check, diff, backup and atomic write: it drops the ID's exact
entry from its route's `models` (a pattern that claims it is left, and the command says the ID stays
claimed by it), its `model_prices` entry and its `budget.allowed_models` entry. It **does not refuse**
when a repository pins the model (D783, the owner's answer, not the recommendation): it warns, naming each
repository and role this machine can see that pins it and each repository it cannot see (no checkout), and
the warning says the next run of that repository fails at launch with a no-provider error until the pin is
changed (`fugaro models use`) or the model is added back. `budget.allowed_models` is never left as an empty list,
which the loader refuses: removing its last entry is refused with the reason.

## 6. `fugaro models use <repo> <coder|reviewer|background> <id> [--yes]`

Prepares the change to the repository's own `fugaro.yaml` (`agent.models.<role>`), never silently
(D781):

- It resolves the repository's checkout (the current directory when it is that repository's checkout,
  else the remembered checkout), and refuses when there is none.
- It checks the result as a launch would: a route claims the ID and its `allow_data_to` admits the
  repository, the model is priced, `budget.allowed_models` admits it, `agent.auth` is `api-key` for a
  provider model, `budget.mode` is `enforce`, and, once the review-levels work ships it, the D721
  guardrail holds. A failure is
  the refusal and nothing is written; the message names the `models add` that would fix a missing
  claim or price.
- It edits **only** that one line of the working tree's `fugaro.yaml` (inserting `agent.models` when
  absent), keeping comments and every other byte, prints the diff, and asks for confirmation (`--yes`
  outside a terminal). It never commits, pushes or opens a pull request: the change reaches runs when
  the user's own branch is merged to the base branch, which is what every run reads.
- The roles are the three keys `agent.models` has today. The review levels' own per-level models
  (D720–D737) are added to `use` by the task that adds their keys (D791).
- It writes the repository's file only, never the project layer, whose `agent.models` keys a launcher
  can write too (D792).

## 7. Policy: one path, through `fugaro update`

`model_prices`, `budget.allowed_models` and `routing` reach a run through the published policy node
`config/policy/shared`, and `providers` through the copy in each repository job's environment
(`FUGARO_MODEL_PROVIDERS`), cut to that repository's `allow_data_to`. The `models` commands write neither.
`fugaro update` carries both:

- **The node:** the policy stage the update-converge work adds publishes the local config's
  `model_prices`, `allowed_models` and `routing` (with the migration of an older node in place), behind
  `update`'s own confirmation. Until that stage ships, the `models` commands are not released (F8).
- **The job copies:** a change to a route (`models`, `allow_data_to`) must reach every job it affects
  without `fugaro update --repo` in each checkout. `fugaro update` gains the step that re-converges each
  recorded repository's job whose provider copy differs from what the local config would write now,
  behind the same confirmation (D787). `fugaro upgrade`'s drift note already computes the difference.

## 8. Owner-only boundaries

- **Providers are the owner's,** set in the local project config, never in `fugaro.yaml`: a
  repository can name a model the owner opened to it and nothing more (no route, URL, price or key).
  `models use` writes only `agent.models.<role>`.
- **No config write in a coding agent's session when the config is outside the repository.** `models
  add` and `models rm` write `~/.config/fugaro/projects/<project>.yaml`, outside every repository: under a
  coding agent's environment variable (`CLAUDECODE` and the like, `agentenv.Marker`) they print the diff
  and the command for the user to run, and write nothing, `--yes` included (D789). `models use` writes the
  repository's own `fugaro.yaml` in the working tree, which an agent may edit anyway; it is allowed there.
  `ls` and `search` read only and run anywhere.
- **No secret** is read, printed or stored by these commands. A new route's key is `fugaro secrets
  set <route>-api-key`; adding a route is out of scope (`add` only extends an existing one, F12).

## 9. Migration of a hand-edited config

A project config edited by hand before these commands exist must keep working and survive a `models`
write unchanged except for the keys the command touches:

- The write edits the YAML document tree, not a re-marshalled struct: comments (including the commented
  `allow_data_to` entries the owner's ruling treats specially), key order, quoting style and unknown-to-
  this-command keys are kept (D790). `localcfg.Config.Marshal` drops all of those, so it is not used.
- The edited document is re-parsed with the strict loader and must decode to the old config plus exactly
  the intended change (a test compares the two decoded configs field by field).
- A `models` write and `fugaro update`'s own config rewrite agree: `update` already rewrites the file with
  a diff and a `.bak`; the `models` write uses the same backup naming and file mode.
- Nothing is renamed or moved: an off-convention `secret:` name, a pattern entry, an existing
  `model_prices` override and a `routing` block are left as written.

## 10. The console and the mobile app

Neither reads model configuration today. The console's model information comes from `fugaro watch
--json` (each run's coder and reviewer models and spend, `docs/design/desktop-console.md` §2.1) and
`fugaro diagnose`; it never reads `~/.config/fugaro` itself. A console models screen, when wanted, reads
`fugaro models ls --json` the way the console reads every other CLI document, and writes nothing. The
mobile app reads nothing of this. No console or mobile task is in this plan.
