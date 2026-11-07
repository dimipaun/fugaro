# The ask: each project says what counts as security-sensitive for it

**The owner, 2026-11-07.** He is the spec's owner: the block below is his, verbatim, and it is the
only text in this folder he wrote. An answer he gives to a decision is recorded in
[reconciliation.md](reconciliation.md) §8, in that decision's own row; this file stays his words.

**Task SC0**, queued 2026-11-07 (it is the design half of a security-classification family whose
implementation half is not in this repository — [reconciliation.md](reconciliation.md) F15).

The ask, verbatim:

> Owner 2026-11-07: 'we have to find a way to allow each project using Fugaro to determine what
> counts as security sensitive for their own situation. It should have a project level component, and
> an optional repo-level one.' This matters because the rule 'security-sensitive work gets the
> independent review in the run (level 3, --review-levels)' is being baked into the plugin skills for
> all users (queued task OD2), and today the definition lives only in this repository's coordinator
> habits and in internal/orchestrator/sensitivity (the path-based classifier, O1, #1822), the
> managed-gate and review recipes. DOCS ONLY, no code, folder docs/security-classification/ with
> README.md (status line: 'plan only; nothing implemented; no task approved'), spec.md (this brief
> verbatim), reconciliation.md (findings against the code, decisions D1500-D1519 each with your
> recommendation, the alternative and its cost, an empty owner's-answer cell, open tensions,
> UNVERIFIED), implementation-plan.md (slices and tasks parallel by construction: Files, Interfaces,
> Tests, Command, Depends on, Red step, reviewer tier, owner-run flagged; levels, critical path,
> shared-files table proving no level has two writers of a file). Before numbering grep docs on main
> and every open PR's docs for the highest D and Check number and take a fresh block (D1500-D1519
> should be free: D1490-D1499 are reserved by docs/console-managed-screens; checks: pick a fresh block
> above every claim, and say so in the README). Verify every claim against origin/main with git grep,
> never from memory; cite paths and symbols, not line numbers. Questions the design MUST settle: (1)
> the shape: a project-layer key (internal/config layer, projectpolicy, schemas/project-layer.schema.json)
> such as security_paths (globs) plus categories (credentials, guards, targets, infrastructure,
> prompts, CI) and a repo-level key in fugaro.yaml that may ONLY ADD (tightening-only merge like
> internal/policy: a repo can never remove a project pattern or the engine's built-in floor list); the
> engine's built-in default list (what internal/orchestrator/sensitivity and the review routing already
> hold) as the floor; (2) WHO consumes the answer and WHEN: the runner (after implement, compute the
> diff's changed paths against the patterns and ESCALATE that run to review level 3 automatically when
> any match, so no one has to guess at launch time: spell out which levels, the rigor mode stays
> unchanged, the cost effect and the in-flight run record field), the launcher (a task that names
> paths, e.g. a plan task's Files), the orchestrator's classifier O1 (one source of truth, the same
> patterns), the plugin working skill (a CLI command such as fugaro security paths --check <path...> /
> --json that an agent calls instead of guessing; list its output contract), doctor (one line saying
> the project's list and the repo additions in effect), the console (read-only screen, defer); (3) how
> an owner edits it: fugaro config / settings set allow-list (D1027 floors only raised),
> managed-project no-prompt rule (a person at a terminal on a managed project is never asked to type a
> name), publish through the project layer and the policy node migration inside fugaro update (HARD
> RULE in CLAUDE.md: update is the whole upgrade, with a test an installation at the previous version
> converges); (4) what a repo-level key does when the project has none and vice versa, empty list
> semantics (empty means the floor, never 'nothing is sensitive': a project can never go below the
> floor), pattern syntax (the same glob dialect the repo already uses; document it, refuse patterns
> that match everything or nothing suspicious? decide), and the safety of the pattern list itself (it
> is a security control: who may write it, audited like the other managed writes, never read from the
> PR's own branch: read from the run's base like the review brief); (5) the interaction with the
> needs_owner work (OD1) and the skills (OD2): a classification that cannot be decided by paths (a
> task that touches no listed path but is security-sensitive by nature) needs a way for the task or the
> owner to force level 3 (--security flag / task field), and a way for the project to say 'always
> level 3'; (6) the smallest useful first slice and what ships dormant; (7) migration, schemas and the
> closed decoders that must learn the new field, golden files, the website cli reference, docs pins,
> release notes entry for operators. Do not decide anything silently: every owner decision is a D row
> with a recommendation.

**What came with the ask, and what it settles.** It is a **scope and a list of questions, not a
design**: it names one rule to make configurable ("security-sensitive work gets the independent review
in the run"), the two layers the answer must live in (a project component, an optional repo one), and
seven question groups the design must answer. It settles none of them itself — it says so in its own
words ("Do not decide anything silently") — and it names three things as *given* that this folder had
to check against the tree rather than take on trust: that the project layer is spelled
`internal/projectpolicy`, that task OD2 is queued, and that O1/#1822 is where the definition lives
today. Two of those three are wrong about this tree; [reconciliation.md](reconciliation.md) records
which, as F4 and F15, before a single decision is taken on top of them.

**The run's own delivery line.** The brief's closing line — the PR title, the PR body's contents and
the `go test ./internal/cli -run Docs` command — is the *run's* instruction and not part of the owner's
ask about the product. It is recorded here so this file is the brief verbatim, and it governs nothing
in the design.
