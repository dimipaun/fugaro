# A vision-capable model for a text-only coder: reconciliation against the code

Read against `origin/main` at `256106ecf1`, and against the 36 pull requests open on 2026-11-10 (the
two that carry decision numbers this folder's block must clear: #2258 and #2259). Line numbers are
that commit's. Each finding is **Exists**, **Missing** or **Conflicts**, with its consequence for the
spec or the plan.

## 1. What exists

**F1: Exists. The refusal, and where it fires.** `Server.check` (`internal/gateway/forward.go:465`)
runs `checkRequest` (`:520`), whose last rule is the image one (`:550`):

> `model %s is priced without an image rate (the price table's image_tokens, a per-image token
> ceiling): an image block can't be reserved without one. model_prices sets token rates only`

It reads `parsed.images`, the count of base64 `image` content blocks the request carries
(`blocks`, `:270`, the `case "image"` at `:282`), and `m`, the price row of the model the request
names (`modelFor`, `:489`). It is reached before the call is reserved and before anything is sent, so
the refusal costs nothing; a text-only call on the same model is never refused (`p.images > 0`).
*Consequence:* the spec's premise is exact — this is the sentence the run died on, and it is one rule
in one place.

**F2: Exists. The per-image ceiling is a reservation input and nothing else.** `pricing.Model.ImageTokens`
(`internal/pricing/pricing.go:56`, "per-image token ceiling (conservative); 0: images refused") enters
exactly one computation: the reservation's input bound,
`bound = satAdd(satAdd(body, RequestOverheadTokens), satMul(ImageCount, ImageTokens))`
(`internal/pricing/worst.go:45`). Settlement is by the response's reported usage
(`fromUsage`, `forward.go:1111`) and has no image dimension at all; the charge the ledger keeps
(`charge`, `internal/gateway/ledger.go:86`) carries the serving model and the route, never an image
count. *Consequence:* "priced with its own `ImageTokens`" is a reservation-and-refusal change plus a
price row, not a change to how a call is billed — and the plan says so, so nobody looks for an
image term in the invoice.

**F3: Exists. The embedded table's image rates are six Claude rows.** `internal/pricing/table.go:50-62`
gives `ImageTokens` to `claude-opus-5-5`, `claude-opus-5`, `claude-sonnet-5-5`, `claude-sonnet-5`,
`claude-haiku-4-5` (the standard ceiling) and `claude-fable-5-1`, from two constants marked
"unverified: no billed image measured yet" (`:22-23`, 6,000 and 2,000). Nothing else in the binary
sets the field. *Consequence:* the spec's parenthetical is exact.

**F4: Exists, and this is the sharpest one. No owner lever can give a provider model an image rate.**
`model_prices` decodes into `pricing.Overrides`, whose wire shape has no image member
(`wireRates`, `internal/pricing/overrides.go:31`), and `Table.With` builds the replacement row as
`Model{ID: key, Rates: r}` (`internal/pricing/pricing.go:362`) — rates and nothing else, which is
what the refusal's own sentence means by "model_prices sets token rates only". The built-in provider
price catalog is the same: it writes `Rates` (`internal/pricing/catalog_apply.go:196`, `:250`) and its
rule D8 refuses a model that prices an image
(`docs/design/provider-price-catalog.md:152`). The table's fallback row for a model no table knows
carries `Max()` too, which is `Rates` only (`pricing.go:294`). *Consequence:* the spec's "a
configured vision-capable model … priced with its own ImageTokens" needs a **new price input** for a
provider model, or the slot can only ever name one of the six embedded Claude IDs. The plan carries
it as its own task (V2) and as D2001, because the issue's own case — a cheap coder through a provider
— is exactly the case a Claude-only vision slot does not fix on the wallet side.

**F5: Exists. The model slots, and every place a fourth one has to be registered.** The three slots
are `config.ModelRoles` (`internal/config/config.go:685`, coder/reviewer/background), resolved by
`Agent.ModelFor` (`:737`, a role's slot else `agent.model`), listed by `Agent.PinnedModels`
(`:761`), named by `pinPaths` (`internal/config/pins.go:23`) and judged by `CheckPins` (`:79`),
`CheckAllowed` (`:206`) and `PinWarnings` (`:118`); allowed in the project layer and the repository
by `config.Scopes` (`internal/config/scope.go:60-61`), held to a model-ID shape by
`validateAgentModels` (`internal/config/validate.go:907`), decoded in the project layer
(`LayerAgent.Models`, `internal/config/layer.go:414`) and in `internal/localcfg`, described by
`schemas/fugaro.schema.json` and `schemas/project-layer.schema.json`, rewritten by the routing switch
(`pinSlots`, `internal/runner/routing.go:388`), and read per stage by `stageModel`
(`internal/runner/routing.go:512`) → `stageSpec` (`internal/runner/gateway.go:551`) → `PinVars`
(`internal/agent/env.go:438`) for the stage's environment. `docs/models-commands` (`fugaro models ls`
and `models use`, D791) reads the same keys, and `docs/model-crafts` MC1 (`internal/config/craft.go`,
merged) has a craft per role whose *model* half is the same kind of key. *Consequence:* "resolved like
the other model slots" is a known, enumerable list of call sites — the plan's V1 walks it — and
D2007 asks whether the vision slot is a full pin (priced, allow-listed, routing-rewritten) or a
lesser key.

**F6: Exists. A stage admits exactly two models, and the model a request names must be one of them.**
`gateway.Stage` carries `Model` and `Background` (`internal/gateway/gateway.go:143-148`), `pinOf`
turns them into the request pin's list (`forward.go:474`), and a request naming anything else is
refused "model X is not pinned for stage implement" (`forward.go:522`). The two names are the role
slot's (or a review level's, `stageModel`) and `agent.models.background`; the stage's environment
sets Claude Code's opus/sonnet/subagent roles to the first and its haiku role to the second
(`PinVars`, `internal/agent/env.go:438`). `sameModel` matches a routed model by exact ID only
(`forward.go:559`). *Consequence:* if an image call is to go out on a model that is not the stage's
own, the stage's admitted set has to grow — a gateway change, not a config one. The plan's
recommended mechanism (V7) avoids it by making the vision model *be* the stage's model.

**F7: Exists. The failure the issue reports.** A call the gateway refused is a stage violation; the
runner fails the stage with it and resumes nothing (`internal/runner/runner.go:4647-4650`:
`r.fail("stage " + name + ": " + violations[0])`). The coder's stages are `plan`, `implement`, `fix`
and `review_first` (`config.StageRole`, `internal/config/config.go:718`), so an image read by the
coder ends the run at `implement`. *Consequence:* the spec's "not mid-stage" is the right demand: the
run spends a stage and the money it already spent before it dies.

**F8: Exists. The "never read image files" rule is the *review* stage's, not the coder's.**
`deniedBinaryReads` (`internal/agent/settings.go:52`) denies `Read(**/*.png)` and nine more kinds, and
it is added to the managed settings only when the stage is a review stage
(`DenyRules(denyWeb, review)`, `:93`, called from `internal/runner/agentenv.go:369` with
`config.IsReviewStage(stage)`). The sentence in the brief belongs to the review input
(`binaryRule`, `internal/runner/reviewinput.go:42`) and to `PickPrompt`; the coder's own
`SystemPrompt` (`internal/runner/prompts.go:40`) says nothing about images. The comment beside the
deny list cites the same failure as the issue, one stage earlier in the run
(`settings.go:41-48`, run `20261021-002046-31cc`). *Consequence:* the spec's last line — "until then:
briefs for text-only coders say never to read image files" — describes something that is **not in
the tree today**: a text-only coder is told nothing, and one `Read` of a golden PNG is enough. The
plan keeps it as an interim task (V0), because it is the cheapest thing that stops the bleeding and
it is needed anyway for the images the bootstrap check cannot see (D2014).

**F9: Exists. What the record already says about which model served a call.** A stage's timing carries
the model it was *pinned* to (`StageTiming.Model`, `internal/runstore/runstore.go:1125`, whose own
comment says "what the config asked for, not what the upstream served"), and the run's cost carries
`cost.model_by` and `cost.route_by` keyed by the **serving** model and route
(`internal/runstore/cost.go:48-52`), fed from `charge.model` through the stage report's `ByModel`
(`internal/gateway/gateway.go:182`, `settle` at `internal/gateway/ledger.go:210`) into
`run.modelBy` (`internal/runner/gateway.go:568`). `fugaro diagnose` carries both in its diagnosis
(`internal/cli/diagnose.go:101-103`, filled at `:606`) and reads the model names back for its pin
warnings (`:644`); `fugaro report` folds the same split into its budget totals
(`internal/cli/reportcore.go:161`); the console draws `model_by` as the run's per-model spend table
(`console/src/renderer/screens/run.ts:862`, `:1680`). The gateway's log line carries the serving
model, the route and `priced_as` per call (`internal/gateway/log.go:38-57`), and the record does not. *Consequence:*
"the record … must name which model served the image" is satisfiable **without a new field** when the
serving model is the stage's model, and a `model_by` entry appears for it by itself (D2010).

**F10: Exists. The bootstrap point an early check belongs at, and what a warning can reach.** The
bootstrap runs at `internal/runner/runner.go:2155`; by `:2352` the task is in hand (`r.spec.Task`,
read at `:2156`) and the checkout is open (`r.repo`, set at `:2352`), and `checkBudget`
(`internal/runner/gateway.go:787`, called at `runner.go:2644`) is where the model and price rules
already run: `CheckAllowed` (`:813`), `CheckPins` (`:820`), the pin warnings (`:824`). Its failures
are `config_invalid` refusals. Bootstrap warnings go to the job log only (`r.d.Log.Warn`) — the
`runstore.Record` has no warning list, and nothing but the task text reaches the pull request body.
*Consequence:* a *refusal* is cheap and lands where the owner looks; a *warning* is a log line. D2006
weighs them.

**F11: Exists. The runner already knows a file is an image — on the review path only.** `git diff
--numstat` plus git's own binary test classifies a changed file (`reviewChanges`,
`internal/runner/reviewinput.go:187`), binaries are named with their size and never attached
(`binaryMarker`, `:93`), and the review job lists the tree with `ls-tree` (`internal/runner/reviewmode.go:341`).
Nothing of this runs for an implement run, and nothing anywhere looks for an *image extension* as
such. *Consequence:* V5's scan is new code, small, and should reuse the extension list
`deniedBinaryReads` already holds rather than start a second list (the task says so; search before
writing a helper).

**F12: Exists. The doctor's model and price checks.** `doctorPriceCatalog`
(`internal/cli/doctor.go:629`, registered at `:397`) reports the catalog's age, and
`doctorModelKeyInAgent` (`internal/cli/doctor_brokering.go:955`, registered at `doctor.go:580`)
warns when a model a repository names is a key an agent session holds. Both are checks of the same
shape, and both already have the resolved project config and the repository's `fugaro.yaml` in
hand. *Consequence:* the doctor line the spec asks for is a third check of the same
shape, and the plan gives it a task (V6).

**F13: Exists. A call's route is the model's own.** `routeFor` matches a route by model ID or prefix
(`internal/gateway/route.go:180`), `withRoute` swaps the upstream when one claims the model (`:217`),
and the runner builds the route list from the models its stages can run
(`gatewayRoutes`, `internal/runner/providers.go:113-143`). *Consequence:* a vision model needs the
route table to claim **it**, exactly as any other model does; and the route list is built from the
stages' models, so a vision model a stage never runs gets no route and no credential (D2008).

## 2. What is missing, and what conflicts

**F14: Missing. There is no vision slot, anywhere.** `ModelRoles` has three keys
(`config.go:686-691`); `grep -ri vision` finds no Go identifier in the tree; no config key, no schema
member, no craft, no price input. *Consequence:* everything the spec asks for is new; the plan's V1
is the whole of the slot.

**F15: Missing. The refusal's sentence names a fix the owner does not have.** It says "model_prices
sets token rates only" (`forward.go:551`), which is true and is exactly the dead end: the owner's
only price lever cannot add an image rate (F4). *Consequence:* even if the vision slot never ships,
the sentence should name what the owner can do — and once V1 and V2 exist, it should name
`agent.models.vision` and `model_prices`'s new image member. V4 is that change, and it has a wrinkle:
the same function is the credentials service's check (`RequestRefusal`, `internal/gateway/export.go:113`,
whose caller has no stage, no repository config and no vision slot —
`internal/credsvc/request.go:129`). A sentence that tells a credentials-service caller to set
`agent.models.vision` would be wrong there. D2009 decides how the hint reaches the two callers: the
pin a caller already passes (`RequestPin`, `export.go:60`) is the seam that exists.

**F16: Conflicts. "An image input is routed to a configured vision-capable model" against "it never
rewrites a body."** The gateway's package comment states the contract for the whole budget design:
"It never rewrites a body, never buffers a stream, and never logs a header or a body"
(`internal/gateway/gateway.go:7`), and the same rule is restated where the request's own keys are
held to the Messages API's (`forward.go:44-51`). The `model` field is in that body. So a per-request
substitution — the literal reading of "routed to" — means the gateway rewriting the request's model
to the vision model, re-checking it against the vision model's own bounds and price, and answering
the agent with a model it did not name; and because Claude Code keeps what it read in the session's
context, the image is in *every later request of that stage* too, so the substitution does not end
with the image: the session migrates to the vision model until the stage ends. *Consequence:* the
literal mechanism buys nothing the narrower one does not, and costs the invariant plus the pinned
model's meaning. D2002 recommends the narrower one (the stage runs the vision model) and states the
literal one's cost; the plan is written for the recommendation, and names the two tasks that change
if the owner answers the other way.

**F17: Missing. Nothing reads the task or the repository for image files.** The task text is in hand
at bootstrap (`r.spec.Task`, F10) and the checkout too (`r.repo`), but no code looks at either for a
file the agent is likely to read as an image; the only whole-tree listing in the tree is the review
job's (`reviewmode.go:341`). *Consequence:* V5 writes the scan; D2005 decides what "the task or repo
names image files" is read as, because the second half is the broad one — most repositories track a
PNG, and a check that refuses on that would refuse most runs.

**F18: Conflicts. The interim rule the spec assumes is not there.** See F8. The plan does not treat
the spec's last line as a statement of fact; it treats it as work (V0).

**F19: Exists, and it is what makes this work a release gate rather than a merge. Every key it adds is
read by a strict decoder, so an older job image does not ignore the new member — it refuses the
document.** A repository's `fugaro.yaml` is decoded strictly (the schemas' own descriptions say it: "the
key is refused by an older binary whatever it says, its decoder being strict"), the policy node decodes
each price entry with `DisallowUnknownFields` (`internal/projectpolicy/prices.go:118`, after
`checkPriceMemberNames`), and the runner's own `pricing.ParseOverrides` refuses unknown fields
(`internal/pricing/overrides.go:63`). So the two members V1 and V2 add — `agent.models.vision` and
`model_prices.*.image_tokens` — each break a run whose job image predates them, whether the value
arrives in the config, the node or `FUGARO_MODEL_PRICES`. The established answer is the `...Since`
discipline, already in the tree for every key of this shape: one constant per key family — 34 of them
in `internal/cli/recipes_skew.go`, among which `catalogSince:173`, `reviewLevelsSince:407`,
`noneModelSince:434` and `proxySince:130` — then a bullet in `docs/release.md`'s Before-you-tag list,
the release named in the schema descriptions, and a launch check that refuses an older image naming the key and the image
to move (`checkImageSince`, `internal/cli/recipes_skew.go:1018`). *Consequence:* V3 is the task that
carries the constant, the launch refusal and the release bullet for both members; without it, the
release that ships V1 or V2 fails a run that bootstrapped before it, which is the one thing the update
rule forbids (D2015, and `docs/design/update-in-flight.md`). It is not a policy-version bump: nothing
removed or re-meant moves a version, and a version the older binary does not know refuses the whole
node rather than the one member.

## 3. Unverified

- **U1.** Whether Claude Code sends an explicit model ID written in a `.claude/agents/*.md` file as
  written, or maps it through `ANTHROPIC_DEFAULT_*`/`CLAUDE_CODE_SUBAGENT_MODEL`
  (`internal/agent/env.go:438`, whose comment names the opus, sonnet and subagent roles the pin
  fills and says nothing about a `model:` field in an agent file). This matters to D2002's option
  (c) alone; it is a measurement against the pinned Claude Code,
  of the kind `docs/multi-model.md` §7 records, and it is not run here.
- **U2.** The issue's own run, `20261107-104613-b2af`: its record is in the owner's cloud and was not
  read from here. The code path it names is verified (F1, F7); the run itself is the owner's report.
- **U3.** What per-image ceiling an OpenRouter vision model should be given. The embedded table's two
  ceilings are explicitly unverified (`table.go:22-23`), and the vision guide's caps (4,784 and 1,568
  visual tokens) are Anthropic's. Whoever writes the row picks the number; the plan says it is a
  reservation ceiling and therefore the conservative direction (Overrun is what a too-low ceiling
  costs, and it is charged).

## 4. Decisions D2000–D2016

**All seventeen rows are answered (owner 2026-11-10): every one as recommended.** Each row carries the
recommendation, the alternative's cost and the answer. The plan is written to the recommendations; a
decision answered the other way would change the tasks that cite it, and each row says which. No task
is approved by these answers.

| D | Question | Recommendation | The alternative, and its cost | Answer |
| --- | --- | --- | --- | --- |
| **D2000** | What makes a model vision-capable? | **Its price row's `ImageTokens`, and nothing else.** A model with an image rate can take an image; one without cannot. The refusal (`forward.go:550`) and the reservation (`worst.go:45`) already ask exactly this question, so the slot needs no second fact about a model. | A `vision: true` flag separate from pricing: two sources of truth that can disagree, and the gateway would still refuse the call the flag invited, because the reservation has no ceiling to count | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2001** | How does a provider vision model get an image rate? | **A new `image_tokens` member on `model_prices`** (the local config, the published node's price block, and `FUGARO_MODEL_PRICES`), with its schema and validation, so an owner can price any model they route — which is the issue's own case (a cheap coder through OpenRouter). | (a) The vision slot may only name one of the six embedded Claude IDs (F3): zero new wire format, but the run buys Claude prices for one screenshot and a provider-only installation has nothing to point at; (b) the catalog prices images: the generator's D8 refuses such a model today (`docs/design/provider-price-catalog.md:152`), and a generated image rate nobody reviewed is a price the owner cannot correct per repository | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2002** | What does "an image input is routed to it" mean? | **The stage that can see images runs the vision model.** At bootstrap the coder's stages are pinned to the vision model instead of the text-only coder (V7): the agent reads images natively, the gateway admits exactly the model the stage runs (no new pin surface, F6), nothing is rewritten, and the record's `model_by` and the stage's `model` name it by construction (F9). | **The gateway substitutes the model on a request that carries an image block** (the literal reading): it rewrites a body the contract says it never rewrites (F16), re-checks and re-prices the call as a different model than the pin, answers the agent with a model it did not name — and because the image stays in Claude Code's context, every later request of that stage carries it too, so the session ends up on the vision model anyway, without a stage boundary and with a `StageTiming.Model` that no longer describes what ran | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2003** | Where does the slot live, and what is it called? | **`agent.models.vision`**, the fourth key of `ModelRoles` (F5), allowed in the project layer and the repository like the other three, and read by `fugaro models ls`/`use` as a fourth role name. | A craft (`agent.crafts.vision.model` + `.access`, `docs/model-crafts` MC1 is merged): gives the vision calls a credential of their own, but the crafts are a closed set of four that the spec of that work fixes, the vision model has no *stage* of its own, and a fifth craft would need that folder's D1620 vocabulary re-decided here. If the owner wants a vision *credential*, D2008 is the row that adds it, when that work's plan is open | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2004** | Which stages run the vision model? | **Every stage whose model has no image rate**, which for today's configuration is the coder's (`plan`, `implement`, `fix`, `review_first`) and never the reviewer's — a mixed run keeps its Claude reviewer and its prices untouched (F5, F7, and `config.StageRole`). | Only `implement` and `fix`: leaves `plan` able to die the same death on the same fixture work, and makes the run's model depend on which stage is running in a way the record (`StageTiming.Model`) makes visible but the owner cannot predict | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2005** | What is "the task or the repo names image files" read as? | **The task text names an image file (an extension from `deniedBinaryReads`' own list, `settings.go:52`) → refuse; the repository tracks an image file at the base branch (a bounded `ls-tree`, reusing that list) → warn.** The first is the issue's own case and costs nothing when it is wrong; the second cannot refuse, because most repositories track a logo or a favicon | Task text only: narrower and never wrong, but the run that reads a PNG nobody named still dies mid-stage — which is the failure the check exists to prevent. Repository only: refuses most runs | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2006** | Warning, or refusal? | **Refuse, naming the fix** (`agent.models.vision`, and how to price the model for it, D2001), at bootstrap, before any stage spends. The repository-only case warns in the job log, which is where the bootstrap's model warnings already go (F10). | Warn and let the stage fail: the run pays for a stage's work it cannot finish and the owner reads the gateway's sentence at `implement` — the issue's own complaint. The refusal's cost is a repository whose task text merely *mentions* a PNG (a task that documents an icon, say) needing one config line | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2007** | Is the vision slot a full pin? | **Yes: priced (`CheckPins`), on `budget.allowed_models` when there is a list (`CheckAllowed`), rewritten by the routing switch (`pinSlots`), warned about for a placeholder or catalog price (`PinWarnings`), and refused when it is not a model ID** — every rule the other three slots get, from the same functions (F5). | A lesser key judged only when it is used: a run whose vision model no route claims, or which the policy does not allow, fails inside a stage instead of at bootstrap, which is the class of failure this work exists to remove | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2008** | Which account and route serve the vision call? | **Its own model ID decides both**: the route table claims it like any other model (`routeFor`, F13), and its credential is the coder craft's access. `agent.crafts.vision.access` is added only if the owner wants a different credential for images. | A dedicated credential per vision model from day one: a key the owner must add and rotate for a few calls per run, and a second path for the same provider; and on the credentials-service path it would be a new tier decision in that service | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2009** | What stays a refusal? | **The image rule stays a refusal in every case where nothing can serve it**: an image on a model with no image rate and no vision model configured (the sentence now naming the fix, V4); a vision model that is unpriced, unrouted, or outside `allowed_models` (the pin rules, D2007); a non-base64 image source (unchanged, `forward.go:287`); and the credentials service's own path, which has no stage, no repository configuration and no vision slot — the same check, the same refusal, and the pin it already passes is how the caller-specific hint reaches it (F15). | Turning the image rule into a pass-through when no ceiling exists: the reservation would under-count the call, which is the one direction the budget may not go | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2010** | How is "which model served the image" recorded? | **Nothing new is stored.** The stage's `model` and `cost.model_by` already name the model that served its calls (F9), and the mechanism the recommendation picks makes them the vision model by construction. `fugaro diagnose` and the console already print both, and the gateway's log line already carries the per-call serving model. | A new per-call or per-run field naming the image call: a record-format change for a fact the serving model already carries, and a field whose meaning would have to be defined for a stage that ran two models (the background model already does) | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2011** | What does the doctor line say? | **A new check (`vision-coder`, beside `doctorModelKeyInAgent`, F12): the coder model's image rate, whether a vision model is set, and — when the repository tracks image files — which of the two is missing.** It is informational when the coder has an image rate and a warning when the repository has images and neither does. | Report it only in the run: the owner learns at launch, which is later than a `fugaro doctor` they already run | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2012** | Does the vision model get its own output limit and effort? | **No new keys.** The stage that runs it uses the coder's `max_output_tokens.coder` and its effort, like any other stage model (F5): the vision calls are the coder's work, on another model. | `max_output_tokens.vision`: a key whose value nobody can choose before seeing a vision model's answers, added for symmetry | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2013** | What does `fugaro models` do with it? | **`models ls` shows the vision slot beside the other three, and `models use vision <id>` writes `agent.models.vision` in the repository's `fugaro.yaml`** — D791's role list gains the fourth name; the slot's own checks are `models use`'s existing ones (priced, a route opens it to this repository, no whitespace). | Leave the commands alone: the owner edits `fugaro.yaml` by hand for a key the command family exists to make one command | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2014** | What ships while this plan is unapproved, and what does the brief say? | **V0 ships on its own**: a text-only coder is *refused the read* (the review path's deny list applied to a stage whose model has no image rate, `DenyRules`, F8) and its brief says why — one line, naming the file kinds and the fix. It is the mitigation the issue's last line asks for, and it stays valuable after the rest lands: it is what makes an image the bootstrap check did not foresee a refusal the agent can work around instead of a dead stage. | Wait for the whole plan: the failure keeps killing runs in the meantime. Or the brief sentence alone, without the deny rule: a prompt is not a permission (the comment at `settings.go:41-48` says exactly this, about the same failure) | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2015** | What does `fugaro update` do, and what does the release owe the images? | **Nothing beyond what it already does — `fugaro update` is the whole upgrade — plus the `...Since` guard the new members force (F19).** The key is additive (a config that never writes it loads and behaves exactly as today), the price member is additive on the same three carriers, and the policy node keeps its version (F5: nothing is removed or re-meant); the one new obligation is V3: `visionSince`, the launch refusal that names the image to move, and the `docs/release.md` bullet — because a strict decoder refuses a new member rather than ignoring it, and `update` rebuilds the images that read those documents. A release carrying V1–V3 has no `### For operators` step beyond `fugaro update`. | A policy-version bump with a migration: a version move for keys nobody removed, which the update-in-flight rule then has to carry for no reader's benefit, and which refuses the whole node where the guard refuses one launch | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |
| **D2016** | Does the vision model apply when the coder already has an image rate? | **No.** A model with an image rate reads the image itself (F1 keeps accepting it); the vision slot is consulted only when the stage's model has none, so a Claude coder's run is byte-for-byte what it is today. | Always route an image to the vision model: spends a second model's call for every run whose model was already fine, and moves the reviewer's images off the reviewer — which is a review-quality change nobody asked for | Recommended -- owner 2026-11-10 (AskUserQuestion: all recommended) |

## 5. What the plan does with these

- **Answered as recommended:** V0 (D2014), V1 (D2003, D2007), V2 (D2001), V3 (D2015, F19), V4 (D2009),
  V5 (D2005, D2006), V6 (D2010, D2011, D2012, D2013), V7 (D2000, D2002, D2004, D2016), V8.
- **Answered the other way:** D2002's alternative replaces V7 with a gateway rewrite task and adds a
  record task for D2010; D2001's (a) halves V2 and moves the vision slot's row into the embedded
  table; D2006's alternative turns V5's refusal into a warning; D2003's craft answer moves V1's key
  into `docs/model-crafts`' MC1 shape and adds that folder's MC9 to the release's prerequisites.
- **Still open for the owner:** U1 (the subagent alternative's premise) and U3 (the ceiling of
  whatever row the owner prices).
