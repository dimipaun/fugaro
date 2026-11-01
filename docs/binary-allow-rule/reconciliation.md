# The binary allow rule and the no-verdict label: findings, decisions and open items

**Status: the decisions D981–D997 are written with a recommendation and none is answered; the owner
decides.** **D997 is left OPEN on purpose** and is the one decision here that changes a run's
behaviour rather than its shape: whether a binary the coder commits itself is refused at all. The
implementation plan is written to its recommended option, and its AR4 does not exist if the owner
answers otherwise. Where this file disagrees with [spec.md](spec.md), **this file wins**.

## 0. What was read, and how the numbers were taken

The findings below are against the code as it stands: `main` at `3d6ed04d` (2026-11-01) plus the
open PR **#1476** ("runner: an unparseable review is tolerated, backlog filing cannot break verify,
and no stray binary is committed"), which is where both deferrals come from and which is read here
from its diff and not from a merged tree. Items that need #1476's code to exist are marked **(needs
#1476)**; everything else is `main`.

The decision block is the **next free block above every `D<number>` on `main` and in every open pull
request**, read on 2026-11-01: `main` carries up to **D901**; the twenty-four open pull requests'
highest is **D980** (PR #1490). This work therefore takes **D981–D1010**: **D981–D997 written here**
(the last of them, D997, added in this branch's review follow-up and deliberately left open),
**D998–D1010 reserved and unused**. The block is thirty wide, as D520–D549 and D490–D519 are.

## 1. Findings

**F1 — the refuse-everything rule is real, and it is the rule this design must narrow, not the
check that must go.** #1476's `internal/runner/stray.go` calls a path a stray when the first 8000
bytes contain a NUL, and there is no input to that decision other than the bytes: the path, the
mode, the extension and the project all go unread. Its own comment argues the content test is
deliberate (a mode test refused a script a run had written and forgotten to commit), and that
argument stands. What is missing is not a better test but a **name the project can vouch for**.

**F2 — the population is large enough that this is not a corner.** This repository tracks **44**
images and fonts: 32 `.png`, 7 `.woff2`, 4 `.webp`, 1 `.ico`, spread over `brand/`,
`website/public/`, `website/public/images/`, `console/src/renderer/fonts/`, `console/build/`,
`mobile/assets/` and the console's e2e screenshot snapshots. The largest is
`brand/fugaro-logo.png` at 855 285 bytes. A run whose task is "update the site's hero image" is a
run this repository asks for, and today it ends a failed draft.

**F3 — the check reads one of the five ways a file reaches a branch.** #1476 guards finalize's
leftover commit (`gitops.CommitAllExcept`, `runner.go:3442`) only. **(needs #1476)** The other four
are the agent's own `git commit` in implement, `internal/runner/gate.go:357`'s `CommitAll` of a
`commands.fix` working-tree change, `internal/runner/digest.go:359`'s report step's commit, and —
the one a first reading misses — `internal/runner/shadow.go:324`'s commit inside `shadowFinalize`,
which is a **separate function** from `finalize` and so is not covered by anything placed in
`finalize`. (`runner.go:3836`'s backlog commit is not one of them: it commits the engine's own
`docs/backlog/` files.) A binary committed by any of the first four is on a branch and no check saw
it.

**F4 — `git add -N` defeats the check by construction.** `gitops.Untracked` is
`git ls-files --others --exclude-standard`, which lists a path git would *add*. A path staged with
intent-to-add is in the index, so it is no longer "other" — and the `git add -A` inside the leftover
commit then stages its **bytes**. **(needs #1476)** The check is blind to it, and the payload is on
the branch. This is the finding that makes the residual a hole rather than a tidiness gap.

**F5 — finalize's status switch has one `default`, and everything lands in it.**
`internal/runner/runner.go:3648–3654`: `ready` → succeeded, a halt → halted, a cancel → cancelled,
**everything else → `failed`**. An unreviewed run, a run with failing tests and a run whose review
left findings the mode blocks on are the same word on the wire.

**F6 — issue 2 is at least two states, not one.** `Decide` refuses readiness with "no review
verdict" in two places: `last == nil` (no round for it to read) and **(needs #1476)**
`last.Verdict == "none"` (a round that answered nothing after its retry). The first case predates
#1476 and is not a tolerance question — nothing was reviewed, so nothing can be tolerated; the
second is #1476's new refusal, and its own comment is the ruling this design adopts (*"a run nobody
read is not one this rule marks ready"*).

**F7 — which round `Decide` reads is settled elsewhere, and this design depends on it.**
`seniorReview` (`internal/runner/runner.go:2794`) skips a `runstore.TierFirst` round, so a run whose
highest level is the first-line review has `last == nil` and is a draft today. **That is already a
named defect with an answer**: [docs/review-levels-ab-jobmax](../review-levels-ab-jobmax/reconciliation.md)
D723 (answered 2026-10-30) rules that *"the highest level that ran decides readiness"*, and its task
**L4.2** is what makes `Decide` read that round. After L4.2, `last == nil` means what it says — no
review round ran at all — and the no-verdict state is one refusal with two reasons rather than three
with a level-resolution question hidden in the middle. This design's label is written for the state,
not for the round.

**F8 — the pattern language this rule needs already exists, twice, with one rule.** `rigor.Area.Match`
and `internal/config`'s `rebuild.paths` both match with `github.com/bmatcuk/doublestar/v4` and both
validate a pattern with the same four checks in the same order: not empty, not absolute, no `..`
component, `doublestar.ValidatePattern` (`internal/rigor/area.go:63`, `internal/config/validate.go:383`).
**The count is four for the pattern rule and five for `Area.Validate` as a whole**, which also
bounds a match's length (`MaxAreaPattern`, 200) — an area-specific check the binaries entries need
too, since an entry is copied into the run record and into the pull request's status line. Writing
"four" for the binaries rule, as this folder's first draft did in spec.md §2.1, undercounts by the
one check that bounds text on a wire. F9's rule in this tree — one matcher, one pattern rule — is
what forbids a fifth spelling here.

**F9 — the dependency is already direct.** `doublestar/v4` is in `go.mod` (`v4.10.2`) and imported
by `internal/rigor`, `internal/config` and others. The allow rule adds no module.

**F10 — `internal/policy.Merge` already knows the direction this key needs.**
`internal/policy/merge.go:37–43`: *"A later layer may only tighten: the smaller positive cap, the
stricter mode (enforce > observe > off), **the intersection of allow-lists**"*. `Layer.AllowedModels`
is the list precedent and `TightenInt` the cap precedent, so a `Layer.BinariesAllow` +
`Layer.BinariesMaxBytes` pair needs no new merge rule — and putting the key on the ceiling alone is
what a `ReviewFloor`-shaped member does already.

**F11 — the carrier for an owner-only key has a precedent, and it is `review_floor`.**
`localcfg.Config.ReviewFloor` travels into the node and never into the shared config,
`internal/cli/sharedcfg.go:198`'s `sharedForbidden` refuses the key at publish, and
`internal/cli/project_cmd.go:670`'s `buildProjectPolicy` copies it in. `internal/cli/docs_project_centric_test.go:2326`
already pins `project_cmd.go`'s `buildProjectPolicy` by name, so the "one build path" guard this
design leans on is a test that exists.

**F12 — the publish stage exists; the publish itself is another design's task.**
`internal/cli/init_stages.go` registers `initflow.Policy` (`policyMigrateStage`,
`internal/cli/init_policy_migrate.go:352`) today for the migration, and
[docs/settings-commands](../settings-commands/implementation-plan.md) **S3** is what makes that
stage publish the node from the local config with the ETag and generation guards. This design's
publish half is a member of that node build, not a stage of its own.

**F13 — the status vocabulary is one word in five places, and only four are this repository's.**
`internal/runstore/runstore.go:28–40` (the constants), `schemas/result.schema.json:16` (the enum a
strict reader validates), `internal/digest/digest.go:120` (`statuses`, the closed set a record's
words are checked against — a value outside it renders `other`), `internal/watch/finished.go:44`
(`isFailure`) and `internal/userstats/userstats.go:196` (the census switch's `default` → `Other`).
`internal/runview/runview.go:394` passes a terminal record's status through unchanged, so nothing is
derived wrongly there. The fifth reader is the console (`console/src/shared/schema.ts`,
`console/src/source/redact.ts`), and it is another work stream's.

**F14 — a run's diff does not currently say which paths were *added*.** `parseNumstat`
(`internal/runner/diffbase.go:532`) reads added/deleted counts and a `binary` flag per path from
`git diff --numstat -z --no-renames`, and it has no status field, so "an added binary" cannot be read
out of it. The reading §4.2 needs is a status row (`A`/`D`/`M` per path) cross-referenced with the
numstat binary flag, or `--diff-filter=A` beside it; the design names the shape and the plan's task
pins it. **`--raw`, not `--name-status`**, because the move rule §4.2 adds (an `A` path whose new
blob id is a `D` path's old blob id is a rename of a tracked file, not an addition) needs both blob
ids, and `--raw -z` carries them beside the same status letter.

**The cross-reference is by path, never by index, and that is a design requirement rather than an
implementation detail.** Two separate `-z` invocations produce two NUL-delimited streams whose
ordering is git's, not a promise: pairing row *i* of one with row *i* of the other would mispair
silently the first time the two orderings differ (a rename, a submodule, a path git quotes
differently). §4.2 therefore reads **one** stream for the status and the blob ids
(`--raw -z --no-renames`) and builds the binary set as a `map[string]bool` **keyed by the path** the
numstat stream reports,
then looks each `A` path up in that map. `--no-renames` is what makes the path a single field on the
numstat side too; a path absent from the map is *unreadable*, and the design refuses it rather than
treating it as text (F1's rule: the check fails closed). AR4's `TestTheTwoReadingsPairByPath` pins
this with a range whose two orderings differ, so an index-based pairing is red rather than latent.

## 2. The decisions

Each is written as a **recommendation** with the cost of the alternative, so that "as recommended"
needs no rewrite.

**D981 — the shape of the rule.** **Recommended:** two members on the policy node —
`binaries.allow`, a list of repository-relative path globs, and `binaries.max_bytes`, one
project-wide cap in bytes — with the block optional and absent meaning "no binary is admitted".
*Cost of the alternative:* a **single list of `{path, max_bytes}` entries** buys a per-path cap
nobody has asked for, at the price of a nested carrier, a nested validation, a registry row per
field and a merge that has to pair two lists by index; a **directory-prefix-only language** (no
globs) is simpler to explain but cannot say `brand/*.png`, so the owner of this repository would have
to admit `brand/` wholesale, which is a wider grant than the one they want.

**D982 — the pattern language is the tree's existing one.** **Recommended:** doublestar globs,
validated at publish time by the four checks `internal/config`'s `rebuild.paths` applies plus the
length bound `rigor.Area.Validate` adds (`MaxAreaPattern`, 200) — five, not four, per F8 — with no
new matcher and no new module. *Cost of the alternative:* a bespoke
"`**` means any depth, everything else is literal" language would be smaller to document and would
be a **second** glob semantics in a tree that has gone out of its way to have one; and a
loosely-validated pattern is the failure mode the emptiest-looking lock produces — a project that
wrote `website/public/**.png`, believed the directory was admitted, and is refused the file it
expected to pass.

**D983 — the shipped cap.** **Recommended:** `max_bytes` defaults to **2 MiB (2 097 152 bytes)** when
the block is present and the key is absent, and a project may only lower it. It is above the largest
asset this repository tracks (855 285 bytes) with room for a retina replacement, and it is a bound,
not a policy. *Cost of the alternative:* **no default** — requiring the key whenever `allow` is
non-empty makes the block two required keys and gives every project that never thought about size a
refusal to read before it can admit anything; a **much smaller default** (256 KiB) is a rule that
silently refuses the very images a site needs.

**D984 — extensions are not a key.** **Recommended:** an extension restriction is written inside a
pattern (`brand/*.png`), and no `extensions` member exists. *Cost of the alternative:* an
`extensions: ["png", "woff2"]` list is easier to write, and it admits those extensions at **every**
path in the repository — including the fixture and testdata directories a reviewer's eye is not on,
which is where a payload would go.

**D985 — no content-type check.** **Recommended:** the "is this a binary" decision stays #1476's NUL
test, and the rule's second input is the path and the size only. *Cost of the alternative:* a
`net/http.DetectContentType` (or magic-number) allow list is stdlib and cheap, and it would admit
**any** PNG at an admitted path — a finer-looking gate that is wider in practice, over the same
bytes nobody can read, at the price of a second read of every added file and a second vocabulary
(PNG/JPEG/WOFF2/…) to keep.

**D986 — who may write it.** **Recommended:** the key is the **policy node's only**. `fugaro.yaml`
has no `binaries` key and `internal/config` refuses one; `internal/cli/sharedcfg.go`'s
`sharedForbidden` gains `binaries` so the shared config refuses it; no project-layer or profile key
carries it in this release. *Cost of the alternative:* a **project-layer** list, merged by
intersection, would let a project narrow the owner's list — a real capability, with no case today,
and it costs a registry row, a `settings set` shape and one more document an operator must read
before they can say why a file was admitted or was not. A **repository** key is not on the table at
all: `fugaro.yaml` is in the checkout the run's own agent writes, so a key there is a self-grant
(F1's whole point).

**D987 — what silence means at publish.** **Recommended:** an absent `binaries` key in the local
project config leaves the published node's block alone (it is not a request to remove it), and taking
an admission away on purpose is a **written empty block** (`binaries: {allow: []}`), which is also
the strictest answer. This is `review_floor`'s written-empty-object rule, and it is the direction
that matters here: a publish that *widened* what a project commits because the operator's file
happened to omit a block is the surprise this folder exists to remove. *Cost of the alternative:*
"absent means remove" is one rule instead of two spellings to tell apart, and it silently widens the
project's admissions on a machine whose local file was written before the key existed — the exact
class of the HARD RULE in `CLAUDE.md` (a change `fugaro update` makes that nobody asked for).

**D988 — where a refusal happens, and what it does.** **Recommended:** both checks run in **finalize,
before the pull request is settled**: an untracked stray is held out of the leftover commit (as
#1476 does) and a *committed* one is named and fails the run, with the commit left on the branch.
The run ends `StatusFailed` / `OutcomeDraft`, exactly as #1476's refusal does today. *Cost of the
alternative:* **a pre-flight check in the implement stage** would refuse earlier and cheaper, and it
cannot be complete — `gate.go`'s autofix and `digest.go`'s step commit *after* implement, and the
review and fix rounds run after it too; a check there would need a second one later anyway, which is
two places for one rule.

**D989 — close the residual, with one reading, called from both finalize paths.** **Recommended:**
yes — the run's own diff (`diffBase()...HEAD`) is read once and an **added** path that is binary and
unadmitted is refused, so the agent's own commit, `git add -N`, `gate.go`'s autofix commit and
`digest.go`'s step all meet one check. The *reading* is one; the *call sites* are two, because
`shadowFinalize` (`shadow.go:321`) is a separate function and a check written only in `finalize`
would leave the challenger arm's own leftover commit unread. Modified paths are not refused. No
commit is rewritten. *Cost of the alternative:* **leaving it open** keeps the diff smaller and
leaves F4 as a documented bypass — a run can put a payload on the branch with one `git add -N` and
one commit, and "the reviewer will see it" is false for exactly the bytes the rule exists for.
**Checking at each committer** instead is a copy of one rule per call site, one read of the checkout
per copy, and a committer added later that nobody remembers to guard — which is the mistake that
already happened once: this design's first draft counted four committers and missed
`shadowFinalize`.

**D990 — the policy version.** **Recommended:** **bump `projectpolicy.Version` 4 → 5**, add
`VersionSince[5] = "0.15.0"`, and carry the convergence test the HARD RULE asks for. A version-4
image tolerates a new *top-level* key (it lands in `UnknownKeys`), so tolerating would not be
fail-open — but it would be **fail-silent**: the owner publishes a path list, the version-4 image
refuses the very file the list admits, and the run ends a draft with nothing saying why. That is the
"published change that silently does nothing" class `CLAUDE.md`'s HARD RULE forbids, and `fugaro
update` refreshes the image in the same command, so the lockstep costs the operator nothing.
*Cost of the alternative:* tolerating means no republish step and no skew window — and it means a
node whose member is half-enforced for as long as an image lags, which is the state this design
exists to remove.

**D991 — what an unadmitted binary does to the run's status.** **Recommended:** unchanged —
`StatusFailed` / `OutcomeDraft`. It is a policy decision the runner made correctly, which is what
`StatusFailed`'s own comment says it is for; the new status of §8 is for a run nothing failed in, and
a refused binary is not that. *Cost of the alternative:* a second new status ("refused") would
separate the two on a list, at the price of another value on a wire five clients read and of a word
that says less than the reason already does.

**D992 — what the agent is told.** **Recommended:** the implement prompt names the admitted paths and
the cap, and the refusal sentence names the rule and says a repository cannot widen it. *Cost of the
alternative:* saying nothing in the prompt keeps the prompt shorter and makes every run that needs to
add an asset fail once before it finds out — a full retry round spent on a fact the run could have
been given for one line.

**D993 — what the pull request says.** **Recommended:** one bounded line in the status section
(`binaries: 2 admitted (…, …)`) when a run committed a binary at an admitted path, and the refusal
in the run's reason and the PR's own status line when it did not. *Cost of the alternative:* saying
nothing keeps the body the same size and leaves a reader to discover the unreadable file by opening
the diff; a per-file section would grow the body with what the diff already shows.

**D994 — the label's name.** **Recommended:** **`unreviewed`**. It says the state a person acts on —
this pull request has no review behind it — and it is the same word for the two states of F6.
*Cost of the alternative:* **`no_verdict`** names the mechanism rather than the state, and reads
oddly on a run whose first-line review did run and had nothing to say; a **reused** word
(`succeeded`, or `failed` with a friendlier reason) is forbidden outright — a name never changes
meaning, and `failed` still has to mean a policy decision for the runs that keep it.

**D995 — how finalize learns which label to write.** **Recommended:** `Decide` returns a small typed
value (readiness, reason, and the class `ready`/`blocked`/`unreviewed`) and finalize's `switch`
gains a `case` on the class. *Cost of the alternative:* matching on the reason string is a two-line
change and makes a **sentence** an enum: the next task to reword "no review verdict" moves a status
by accident, and nothing fails.

**D996 — the census.** **Recommended:** `internal/userstats` leaves the new value in its `default`
bucket (`Other`), so this slice changes no aggregate shape. *Cost of the alternative:* a
`Unreviewed` counter is more truthful on the spend screen, and it is a wire change in
`internal/userstats`, the console and the phone for a figure the run records themselves already
answer.

**D997 — the binaries the coder commits itself: refuse them, or keep #1476's finalize-only check?
(OPEN: the owner's ruling comes before AR4 is written.)** #1476's check reads the **working tree** at
finalize — `git ls-files --others`, what `git add -A` would stage. A binary the coder put in a commit
of its own is *not* in that set, so it is not refused, and neither `git commit -am work` nor
`git add -N payload.bin` followed by any runner commit is seen by anything (F3, F4). **Closing it
means refusing a *new* binary anywhere in the run's own range `diffBase()...HEAD`, subject to this
design's allow rule** — a **behaviour change**, which is why it is the owner's to take and not a
detail the design settles (D989 chose *how* a closure is written; this is *whether* to close at all).

**Recommended: (A) refuse across the run's own range, subject to the allow rule.** finalize reads the
diff it is already handed for the review and fails an added binary the node does not admit, whoever
committed it — the coder, `gate.go`'s autofix, `digest.go`'s step, or the challenger arm's own
commit. The run ends `StatusFailed` / `OutcomeDraft`, the commits stay on the branch, the reason
names the paths; a person deletes the file. *Cost of A:*

- **False refusals of legitimate assets**, which is the very failure this folder exists to remove,
  and **one shape of them is new**: with `--no-renames` a *move* reads as a delete plus an add, so
  renaming a tracked `brand/logo.png` to `brand/logo-v2.png` would be an *added binary* at a path
  the project may not have admitted. **Spec §4.2 answers it in the reading itself** — an `A` path
  whose new blob id is a `D` path's old blob id is a move of a tracked file, not an addition — so
  the flag-free, cost-free mitigation is already in the design and the allow rule covers the rest by
  naming the project's asset directories. The residual false refusal is a move that *also* edits the
  bytes (two ids, so an addition by the rule): refused by name, with the rule in the reason, so the
  answer is a publish and not a bug report.
- **Run time**: one more `git diff` over a range the runner already walks for the review and the
  review scope, in the same order as that read. Not a new class of cost, and AR4's task adds no
  second pass over the checkout.
- **A "fix" that rewrote the coder's commits is refused**, and it is the tempting repair: the
  refusal is a verdict on the run. Rewriting would break the pull request's own record — the review
  read those commits, the review scope and the patch object name them, the stamp hook signed them —
  so the branch keeps them and a person deletes the file.

**Alternative (B): check only at finalize, exactly as #1476 ships.** The smallest change: no new
reading, no rename question, nothing added to `finalize`. *Cost of B:* **the bypass stands in
precisely the case the rule is for.** One `git commit -am work`, or one `git add -N` and a later
commit, puts a payload on the branch and **no check reads it**: the run is not failed, and because
the rule is not about readiness the run can still end `succeeded` / `ready` — so nothing in the run
tells a person to look. A person's only signal is the review input's marker line (`binary file %s
changed, %d bytes`), which a review round can pass over; the rule then refuses the sloppy stray and
admits the deliberate one, which is the wrong way round.

**Alternative (C): check at every commit the runner makes.** *Cost of C:* it is a copy of one rule
per call site with one read of the checkout each, and it **does not close the hole this decision is
about** — the coder's own `git commit` is not a runner call site at all, so the very commits in
question are still unread. The count is also the part that already went wrong: a first reading of F3
found four committers and missed `shadowFinalize`, and a rule with five homes has five places for the
next committer to be missed.

**Refused: a refusal in the repository's own git (`pre-receive`, a stamp hook).** It runs where the
bytes are, but it is a file in the checkout the coder writes and reads, and #1418's API-root refusal
is this repository's own record of what trusting the checkout's git configuration costs.

## 3. Open items

**The one decision left open on purpose is §2's D997** — whether a binary the coder commits itself
is refused at all. It sits in §2 rather than here because it takes the same shape as the rest (a
recommendation and the cost of each answer) and it has a recommendation; it is "open" in the sense
that no answer to it may be assumed by an implementation. The three items below are the ones this
design does not even propose an answer to.

**O1 — does anything still reach `last == nil` once L4.2 lands?** F7's answer (D723, L4.2) removes
the first-line-only case, so after it `last == nil` means a run with **no review round at all**.
Whether such a run exists by configuration (a recipe that names no review stage) and whether it too
should be `unreviewed` — or whether it is a refusal of a different kind — is a question for the
levels work, not for this label; this design labels the state it is given and does not move the
readiness rule. Recorded rather than settled.

**O2 — what the console and the phone draw for `unreviewed`.** The chip's colour and word are those
clients' (E2). Until they ship, the value reaches them as itself, because `status` is a contract key
in `console/src/source/redact.ts` and the person pass never rewrites it.

**O3 — whether the project layer should ever carry `binaries`.** D986 keeps the key on the ceiling
only. If a project with many repositories later wants one list plus a narrower one per job profile,
the merge direction is already settled (D986, F10) and the work is a registry row and a `settings`
shape — not a new rule.

## 4. What this design does not decide

- **Readiness**, the **retry** `seniorLoop` gives a silent reviewer, and whether a silent senior
  round should be retried more than once. §8 is about the label only.
- **The console's and the phone's screens** (`console/**`, `mobile/**`): external (E2).
- **Any repository-facing key** for the rule (D986), and any change to `fugaro.yaml`'s own schema
  beyond "the key does not exist there".
- **The other two deferrals of the 0.14.2 runner group** that are not these two: this folder answers
  the binary refusal and the no-verdict label, and nothing else in #1476.
