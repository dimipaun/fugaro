# The binary allow rule and the no-verdict label: the design

**Status: design only; nothing is implemented; the owner decides.** This document is the design in
full. Where [reconciliation.md](reconciliation.md) disagrees with it, the reconciliation wins, and
the tasks in [implementation-plan.md](implementation-plan.md) are written to what is here.

## 1. What the code does today

**The stray refusal (#1476).** finalize's leftover commit used to be `gitops.CommitAll`, which is
`git add -A` and a commit: everything the run left behind went onto the branch. PR #1476 puts a
check in front of it:

- `gitops.Untracked` lists `git ls-files --others --exclude-standard -z` — the paths git would add,
  which is exactly the set `git add -A` would stage, an ignored file aside.
- `runner.strayArtifacts` reads each of those paths (up to 8000 bytes,
  `binaryScanBytes`) and calls it a stray when the bytes hold a NUL. A symlink is not one; a path
  that vanished between the listing and the read is not one; a path it cannot read **is** one.
- finalize calls `r.fail(strayRefusal(strays))` — the reason names every path with its reason — and
  then commits with `gitops.CommitAllExcept(ctx, leftover, strayPaths(strays))`, so the file stays
  untracked in the tree and the branch does not carry it. The run ends `failed`, `outcome: draft`.
- the challenger arm of a shadow pair (`shadow.go`) holds back the same paths so the two arms'
  records agree.

The rule's own comment states its reason: *"a file that is not text is a build's artifact, not the
run's work"*. It is right about a `go build` product and about a bundle's output. It is wrong about
a run whose task *is* to add an asset.

**The no-verdict refusal (#1476).** `runner.Decide` (`internal/runner/outcome.go:45`) answers
readiness from the last verified test on the final commit and the last **senior** review round
(`seniorReview` skips a first-line round). #1476 adds a case: a senior round whose verdict is `none`
— no parseable verdict after the one retry `seniorLoop` gives it — is not ready, because *"a run
nobody read is not one this rule marks ready"*. The pull request stays a draft and the reason says
which round it was.

finalize then maps that refusal to a status (`internal/runner/runner.go:3653`):

```go
	r.rec.Reason = reason
	switch {
	case ready:
		r.rec.Status, r.rec.Outcome = runstore.StatusSucceeded, runstore.OutcomeReady
	case halt != nil:
		r.rec.Status, r.rec.Outcome, r.rec.Halt = runstore.StatusHalted, runstore.OutcomeDraft, halt
	case r.isCancelled():
		r.rec.Status, r.rec.Outcome = runstore.StatusCancelled, runstore.OutcomeDraft
	default:
		r.rec.Status, r.rec.Outcome = runstore.StatusFailed, runstore.OutcomeDraft
	}
```

**Every** refusal that is not a halt and not a cancellation lands in `default`, and that is the
whole of item 2: `failed` is the bucket, and the reason is the only place the difference is kept.

## 2. The allow rule: its shape

The project states, once, which paths a run may add a binary at and how large one may be. Two
members on the policy node, beside `review_floor` (D722) and written the same way:

```json
{
  "version": 5,
  "binaries": {
    "allow": ["website/public/**", "console/src/renderer/fonts/**", "brand/*.png"],
    "max_bytes": 2097152
  }
}
```

**2.1 `binaries.allow` is a list of doublestar path globs**, relative to the repository root, and the
publication is valid when every entry passes the four checks this tree already applies to a path
glob — **not empty, not starting with `/`, no `..` component, `doublestar.ValidatePattern` accepts
it** — the same four in the same order that `rigor.Area.Validate` (`internal/rigor/area.go:63`) and
`internal/config`'s `rebuild.paths` (`internal/config/validate.go:383`) apply. `doublestar/v4` is
already a direct dependency (`go.mod`), so this adds no module: one matcher, one pattern rule, the
F9 rule the areas and the rebuild paths already follow. A pattern the matcher cannot read is refused
**at publish time**, which is the "no lock that is not there" rule: a project that wrote
`website/public/**.png` and believed it had allowed the directory would otherwise be refused a
file it had every reason to expect would pass.

**Absent `allow`, or an absent `binaries` block, admits nothing** — today's behaviour, byte for
byte. `"allow": []` is the same answer written out loud.

**2.2 `binaries.max_bytes` is one cap, project-wide.** An added file at an admitted path is admitted
only while its size is at most the cap; above it, it is refused by name with the cap in the reason.
One cap and not one per entry: a path already says *where*, the only thing a path cannot say is
*how big*, and a per-entry cap would double the node's shape (and its merge, and its registry row)
for a case nobody has. The shipped default is **2 MiB (2 097 152 bytes)**, which is above the
largest asset this repository tracks (`brand/fugaro-logo.png`, 855 285 bytes) with room for a
retina-sized replacement, and a project may only lower it (§3).

**A file that is not an added binary is not checked at all: not a text file, however large, and not
a binary the base commit already tracks.** A text file is reviewable and the diff carries it; a
tracked binary is the repository's own — a run that replaces an icon is doing the task it was given,
and the check has no business refusing it. Only an **added binary at a path the project has not
admitted** is refused.

**2.3 Extensions are not a key.** `"extensions": ["png", "woff2"]` would admit those extensions at
*every* path in the repository — including a fixture directory nobody reads, and including
`internal/runner/testdata/…png`, which is exactly where a payload would be hidden from a reviewer's
eye. An extension restriction is written *inside* a pattern instead (`brand/*.png`, `**/*.woff2`),
which says the same thing without giving up the path. One way to say it, not two.

**2.4 Content types are not checked.** No `net/http.DetectContentType`, no magic-number table, no
extension-to-MIME map. The rule's premise is that a NUL byte in the first 8000 bytes means "a person
cannot review this", and the check that decides *that* is already #1476's. A second, finer test —
"this is a PNG, and PNGs are fine" — would admit **any** PNG at an admitted path, would run a second
read of every added file, and would still not make the bytes reviewable: the file a reviewer must
judge is still a marker line with a size on it. The honest boundary is the path and the size, and §9
states what it leaves open.

## 3. Who may write it, and in which direction

**The key is the policy node's, and it exists in no repository file.** Concretely:

- **`fugaro.yaml` carries no `binaries` key at all**, and `internal/config` refuses one: it decodes
  strictly, so an unknown field is an error rather than a silently-dropped value, and
  `internal/config`'s schema section and `schemas/fugaro.schema.json` say the key does not exist.
  This is the load-bearing half. `fugaro.yaml` sits inside the checkout, on the run's own branch,
  and the run's own agent writes it — a repository-scoped allow rule would let a prompt-injected
  agent write `binaries: {allow: ["**"]}`, commit it, and admit its own payload with the
  repository's own consent-looking file. The rule's writer must not be a file the subject of the
  rule can write, and there is exactly one such file here.
- **The owner's local project config carries it, and `fugaro project policy publish` (and, once the
  settings work lands, `fugaro update`) builds it into the node.** The carrier is
  `localcfg.Config`, the way `base_branch` and `review_floor` are carried, and for the same reason
  their comments give: the value travels into the owner-only node and never into the shared config a
  launcher can write (`internal/cli/sharedcfg.go`'s `sharedForbidden` gains `binaries`, so
  `fugaro config publish` refuses it).
- **A publish that says nothing leaves the node's list alone.** An absent `binaries` key in the local
  file is not a request to remove the published one; taking a list away on purpose is a written
  empty block (`binaries: {allow: []}`), which is the strictest answer and not the same spelling as
  silence. `review_floor`'s written-empty-object rule is exactly this, and a publish that lowered a
  rule by accident is the class of surprise this whole folder exists to remove.
- **The direction is one way, and the mechanism for it already exists.** `internal/policy.Merge`
  already knows how to tighten a list — *"the intersection of allow-lists"* (`internal/policy/merge.go:37`)
  — and the binaries list merges the same way, as `Layer.BinariesAllow` beside `Layer.AllowedModels`:
  a layer below the ceiling may only remove entries, and a cap below it may only be smaller
  (`TightenInt`). No layer carries the key today (§D986), so the merge's direction is a property of
  the package rather than of a caller, which is the ReviewFloor precedent exactly.

## 4. Where finalize checks it

**Two checks, one rule, at one moment.** The run's outcome, its push and its pull request all happen
in `finalize`, and both checks run before `ensurePR` — so a run that fails either one ends a draft
that a person can look at, exactly as #1476's refusal does today.

**4.1 The untracked check (what #1476 already does, widened by the rule).** `strayArtifact` returns
nil for a path whose bytes are text, and now also for a path the policy admits and whose size is
within the cap. Everything else is unchanged: the file is held out of the leftover commit by
`CommitAllExcept`, the reason names it, and the run fails. This is the check that covers the run
that *adds an asset and does not commit it*, which is the ordinary case and the one item 1 is about.

**4.2 The committed check (the residual, closed).** A binary the agent committed itself — or one
`swept` into a commit by `gate.go`'s `CommitAll` for `commands.fix`
(`internal/runner/gate.go:357`) or by `digest.go`'s report step (`internal/runner/digest.go:359`) —
never passes through 4.1: `Untracked` lists only what git would *add*, and a committed path is not
that. So finalize also reads **the run's own diff** (`diffBase()...HEAD`, the range the review stage
is already handed) and refuses an **added** path that is binary and unadmitted:

- the reading is `git diff --name-status -z --no-renames --end-of-options <base>...HEAD` — added
  (`A`) paths only, with `--no-renames` so a move is an add and a delete rather than a status the
  parser has to know;
- a path whose blob is binary is `git diff --numstat -z`'s `-`/`-` row, which
  `parseNumstat` already computes (`internal/runner/diffbase.go:532`, `reviewChange.binary`);
- **modified** paths are not refused at all: `M` on a tracked binary is the repository's own file
  being replaced, which §2.1 leaves alone;
- the file is *already committed*, so the branch carries it and the run cannot take it back without
  rewriting the agent's history — which this design does not do (§D989). The run fails, the pull
  request stays a draft, and the reason names the path, its size and the rule, so the person
  deletes it.

**4.3 One function, two callers.** Both checks are the same predicate — *is this added path a binary
the project has not admitted?* — and the plan writes it once
(`internal/runner/stray.go`'s `strayArtifact` grows the policy, and a sibling in the same file reads
the diff). A second copy of "is it admitted" beside the merge's would be a second answer to one
question.

**4.4 The refusal is a run failure, not an infrastructure error.** `StatusFailed` /
`OutcomeDraft`, the same as today's stray refusal: this is the runner making a policy decision
correctly, which is what `StatusFailed`'s own comment says it is for.

## 5. What the agent sees

**In the prompt.** The implement stage's prompt gains one clause naming what the project admits:
the paths, the size cap, and the sentence that a build product left behind is refused and will end
the run as a draft. The list is not a secret — the policy node is readable by every run token, and
the agent could read it there — so stating it is not a disclosure; it is what makes a run that needs
to add an asset put it where the project admitted it, instead of failing once and stopping. The
review stage's `binaryRule` (`internal/runner/reviewinput.go:42`) is untouched: the reviewer is still
told to never read or attach a binary, and the marker line is still all it gets.

**In the refusal.** The reason names every refused path with why (`it is not text`, `it could not be
read`, `it is 3 145 728 bytes, over the project's 2 097 152-byte cap`), names the rule (`the
project's binaries.allow`), and states where the fix is for the two readers who have one: a person
admits a path in the owner's project config and publishes it (`fugaro project policy publish`
today; `fugaro update` once the settings work lands), and **a repository cannot** — the sentence
says so, so an agent that decides to "fix" the refusal by editing `fugaro.yaml` learns immediately
that the file it is about to write is not one the rule reads, and a run that wrote it anyway is
refused by `internal/config`'s strict decode.

**In the pull request.** The status section's policy line already reports the resolved policy; an
admitted binary adds nothing to it. A run that *used* the rule — committed a binary at an admitted
path — says so in one line, `binaries: 2 admitted (website/public/images/a.webp, …)`, so a reader of
the pull request knows an unreadable file is there without opening the diff. That line is bounded
(the first few paths, then a count), because the pull request body is a surface a run can grow.

## 6. How `fugaro update` publishes it

**Through the stage that already exists, with no separate command anywhere.**

`internal/cli/init_stages.go` already registers `initflow.Policy` (`policyMigrateStage`,
`internal/cli/init_policy_migrate.go:352`), and
[docs/settings-commands](../settings-commands/implementation-plan.md) S3 is what makes that stage
also **publish** the node from the local config through `buildProjectPolicy`
(`internal/cli/project_cmd.go:670`) under an ETag guard and a generation guard (D854). The binaries
block is a member `buildProjectPolicy` fills like `review_floor`, so:

- **`fugaro update` publishes it.** One command, no flag, no second step: the HARD RULE in
  `CLAUDE.md` holds, and there is nothing for an operator to remember.
- **The explicit door builds the same bytes.** `fugaro project policy publish` goes through the same
  `buildProjectPolicy`, so the stage and the door cannot drift; the settings plan's
  `TestUpdateStageAndTheExplicitDoorBuildTheSameNode` is the guard and this work does not add a
  second path.
- **The runner and the node move together.** The image refresh is part of the same `fugaro update`
  (behind its own confirmation), so a node that carries `binaries` and an image that reads it arrive
  in one command.
- **The policy version moves 4 → 5** (§D990), `VersionSince` gains its row (`5: "0.15.0"`), and
  `TestVersionSinceCoversEveryVersion` fails until it does. The convergence test the HARD RULE asks
  for — an installation at the previous version ends `fugaro update` with a version-5 node published
  — is `TestInstallationAtThePreviousVersionConvergesBinaries`, written with the `…Binaries` suffix
  so it cannot collide with the sibling designs' convergence tests in package `cli`.
- **The migration is the node's own.** An installation whose published node is version 4 and carries
  no `binaries` member is not broken by the bump in either direction: a version-5 reader tolerates a
  node with no `binaries` (the block is optional, absent meaning "admits nothing"), and the version-4
  node is republished by the same update that refreshes the image. The only operator step is
  `fugaro update`.

**What a version-4 reader makes of a version-5 node, stated plainly.** It refuses the node
(`RunnerSkew`) — which is the fail-closed direction and the reason the bump is taken rather than
tolerated. See §D990 for why, and for the cost of the alternative.

## 7. The residual: does the agent's own commit get checked?

**Yes — §4.2 is that closure, and this section is why it is worth the work.** Before #1476 the
question could not be asked, because exactly one thing committed: finalize. Today there are four
places a file can reach the branch (finalize's leftover commit, `gate.go`'s autofix commit of a
`commands.fix` working-tree change, `digest.go`'s report step, and the agent's own `git commit` in
the implement stage), and #1476 guards only the first. That gap is not exotic:

- an agent that runs `git commit -am work` at the end of implement puts everything it wrote,
  compiler output included, on the branch, and no check ever saw it;
- `git add -N payload.bin` puts a path in the index without its content, so `ls-files --others`
  stops listing it — and the `git add -A` inside `CommitAll`/`CommitAllExcept` then stages its bytes
  into the leftover commit. #1476's check is blind to it by construction, because the path is no
  longer "untracked".

**Closing it at one point (finalize, over the run's diff) and not at four call sites is the whole
argument**: each of the four paths ends with the same bytes on the same branch, the range
`diffBase()...HEAD` is already computed for the review stage and the review scope, and a check at
each committer would be four places to keep in step and four places for a future committer to be
missed. The cost is named in §D989: a file the agent committed cannot be taken back without
rewriting history, so the run fails and a person deletes it, where an untracked stray is silently
held out.

## 8. The status label for a run no review answered

**The label is `unreviewed`** — `runstore.StatusUnreviewed`, the seventh value of the status
vocabulary, beside `running`, `succeeded`, `failed`, `infra_error`, `cancelled` and `halted`:

```go
	// StatusUnreviewed is a run that finished its loop, pushed and opened
	// (or updated) its pull request, and that no review verdict exists for:
	// a senior review round that answered nothing after its retry, or a run
	// whose review never produced a senior round at all. Nothing failed —
	// its tests passed — and nothing read it either, which is the state the
	// label names.
	StatusUnreviewed Status = "unreviewed"
```

**8.1 A new name, never a re-aimed old one.** `failed` keeps its meaning exactly as its comment
states it — a policy decision the runner made correctly — and narrows only in the sense that this
one class of run stops being one of them. The rule that a name never changes meaning is what forbids
the cheap alternative (`succeeded` for a draft, or `failed` with a friendlier reason), and it is also
what makes this a *wire* change with a task list rather than a one-line edit.

**8.2 Which runs carry it.** Every readiness refusal whose reason is "no review verdict": a round
that answered nothing after its retry, and a run with no review round for `Decide` to read at all.
The second is reachable today — a mode whose level 2 is off runs a first-line review only
(`internal/rigor/levels.go`, `POC` is the shipped row) and `seniorReview` skips a first-line round,
so the run ends `failed` for a review it was configured not to have — and that half is being fixed
where it belongs, by [docs/review-levels-ab-jobmax](../review-levels-ab-jobmax/reconciliation.md)
D723's *"the highest level that ran decides readiness"* and its task L4.2. This design does not
touch readiness: it labels the state, and after L4.2 the label covers one refusal with two reasons
rather than three with a level-resolution question inside.

**8.3 How the call site learns it.** `Decide` stops answering in a bool: it returns the readiness,
the reason **and the class** (`ready`, `blocked`, `unreviewed`) as a small typed value, and
finalize's switch gains `case dec.Kind == KindUnreviewed: r.rec.Status, r.rec.Outcome =
runstore.StatusUnreviewed, runstore.OutcomeDraft`. The alternative — finalize matching on the reason
string — is refused in §D995: a sentence is not an enum, and the next task to reword it would move a
status by accident.

**8.4 The wire, and who reads it.** The label is a value of a field four clients read, so the tasks
name each one:

| Reader | What it does with the new value |
| --- | --- |
| `schemas/result.schema.json` (`status` enum, line 16) | gains `unreviewed` — the run record's own schema, and the one a strict reader validates against |
| `internal/digest` (`statuses`, `internal/digest/digest.go:120`) | gains it, or a record this Fugaro wrote renders as `other` |
| `internal/watch` (`isFailure`, `internal/watch/finished.go:44`) | **not** a failure: nothing failed. A watched run that was never reviewed is a draft to look at, not an outage, and the row carries the status word itself |
| `internal/userstats` | the switch's `default` already counts it as `Other`; §D996 recommends leaving it there and naming the counter a follow-up |
| `internal/runview` | passes a status through unchanged when the record is terminal (`runview.go:394`), so nothing is derived wrongly; the console and the phone are the readers that need a chip, and they are **external** (E2) — `console/**` and `mobile/**` are other work streams' files |

**8.5 What the pull request says.** The status section's line for the round is already #1476's
(`review round N returned no parseable verdict after a retry`), and the run's reason is unchanged.
The label changes what the *record* and the *lists* say, and it is deliberately the last thing
finalize decides, so a run that later fails for another reason is still `failed`.

## 9. Threat model

**Assets.** (a) The branch and the pull request a person reviews and merges. (b) The reviewer's
attention: a binary in a diff is a file nobody can read, so "what may be a binary" is a claim about
what review can even mean. (c) The owner's authority over what their project commits.

**Adversary.** The run's own coding agent, under prompt injection from the repository's content, the
task, or anything else it reads. It can write any file in the checkout, write `fugaro.yaml`,
`CLAUDE.md`, `.gitignore` and `.git/info/exclude`, run `git` (including `add -N`, `commit`,
`update-index`), and edit the working tree between any two of the runner's own reads.

**Not the adversary.** The launcher (it can already publish whatever policy it likes by other
means), the repository's other collaborators, and the network. Nor is the *owner*: the rule is the
owner's own word, and a rule that its writer can widen is not a rule.

**Properties this design holds.**

| # | Property | What holds it |
| --- | --- | --- |
| P1 | No added path becomes a binary on the branch unless the project's node admits it | §4.1 for an untracked path, §4.2 for a committed one |
| P2 | Nothing the run can write can widen the rule | the key is not in `fugaro.yaml` (strict decode refuses it), not in the shared config (`sharedForbidden` refuses a publish), and not in the task, the project layer or the checkout |
| P3 | A refusal names every path and the rule, so a person can act | the reason and the pull request's line |
| P4 | A run whose review answered nothing is not reported as a failure | §8 |
| P5 | The rule does not widen what a run may *ship*, only what it may *commit* | readiness is untouched: tests and review still decide |

**Attacks, and what each one meets.**

| Attack | Answer |
| --- | --- |
| The agent writes `binaries: {allow: ["**"]}` into `fugaro.yaml` and commits it | `internal/config` decodes strictly: the field does not exist, so the run stops at load, and the file never reaches a run's policy in the first place (P2, §3) |
| The agent self-commits a payload during implement | §4.2 refuses the run at finalize; the commit stays on the branch and a person deletes it |
| The agent stages the payload with `git add -N` so `ls-files --others` no longer lists it | the same: the bytes land in a commit, and §4.2 reads the commit's path out of the diff |
| The agent writes `.gitignore` or `.git/info/exclude` for the path | the file is never committed, so it never reaches the branch. It is not a bypass, and it is not an escape either: the file stays in the tree where the person reading the run finds it |
| The payload is committed under an admitted path | **admitted, by design.** The rule is a path-and-size gate; the project said that path may hold a binary. §2.4 says why no finer gate is offered |
| The payload is a "text" file with no NUL in its first 8000 bytes (a script, a base64 blob) | **not a binary by the rule's premise, and reviewable** — the diff carries it and the reviewer reads it. This is #1476's boundary, not a new one |
| The agent makes the admitted file enormous | `max_bytes` refuses it by name (§2.2) |
| The agent writes a text file of any size at any path | out of scope: the rule is about binaries, and a text file is read by the review like any other diff line |

**Residual risk, stated rather than papered over.** (1) An admitted path is trusted: a project that
admits `website/public/**` has said a binary may sit anywhere under it, and the reviewer's only
signal is the marker line with its size. (2) A committed payload is refused *after* it is on the
branch — §4.2 fails the run and does not rewrite history, so containment is "a draft nobody merged"
rather than "the bytes never left the checkout". (3) The check reads the checkout's own git, which
the agent writes; `stray.go` already refuses an unreadable file rather than trusting it, and §4.2's
reading goes through the same `sessionGit` seam the review range does, but a checkout whose `.git`
the agent owns is a checkout with a smaller trusted core than the repository's own history would
have. That limit is #1476's and is named here rather than claimed away.

## 10. Out of scope

- **Per-repository and per-profile allow lists.** §D986 keeps the key on the ceiling only.
- **Per-entry size caps**, **content sniffing**, **extension lists** (§2.2–§2.4).
- **A console or phone chip for `unreviewed`** — the label is this repository's; the two clients are
  external (E2), and until they ship, an unknown status reaches them as itself (`status` is a
  contract key in `console/src/source/redact.ts`, so the person pass never rewrites it).
- **Any change to readiness, to the retry `seniorLoop` gives a silent reviewer, or to whether a
  first-line-only mode should be ready at all.**
