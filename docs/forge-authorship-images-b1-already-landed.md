# B1 — one list of forge kinds — was already on `main`

**Nothing was implemented for this task: its deliverable was already on `main` when this run
started.** This note is the evidence, and — with the one State line it moves in
`docs/forge-authorship-images/implementation-plan.md` — the pull request's whole change. No Go file,
no test and no golden was touched.

## Where it landed

`9af1fd50`, *gitprov: one list of forge kinds (B1) (#2332)* — merged 2026-11-11, an ancestor of this
branch's base `2988c2d6`. The first line of its commit message is the plan's **Commit** line
(`gitprov: one list of forge kinds, and an inventory of every switch on them`) with the pull
request's number appended, and its diff is the task: the files the plan's **Files** line names, and
nothing else.

| the commit's file | what it did |
|---|---|
| `internal/gitprov/kinds.go` (new, 17 lines) | the two constants, and `Kinds` beside them |
| `internal/gitprov/kinds_test.go` (new; 464 lines at the merge, 466 today) | the two guards the plan names, plus the third a review round added |
| `internal/gitprov/gitprov.go` (−6) | the two kind constants left it |
| `internal/gitprov/providers/providers.go` (1 line) | the unknown-kind refusal's text is built from `Kinds` |

## Why it was picked up again

The plan's B1 **State** line is the census `reconciliation-image-setup.md` read against `origin/main`
at the owner's ask of 2026-11-10, where it is "**Kept** — there is no `internal/gitprov/kinds.go`…"
(line 96). It still read `Kept` when this run was dispatched, because the plan's State block has not
been edited since that census landed (`6a727d43`, #2258): **every task merged after the census still
reads `Kept` there**, and B1 is the one this run was handed.

`docs/status.md` is the living table and already counts B1 merged — its `forge-authorship-images` row
says 12/25. So the tree was of one mind and the plan's snapshot was not, and this run changes the one
line that made the task look open.

## The task, point by point

| B1's clause | state on `main` |
|---|---|
| `gitprov.Kinds` lists the forge kinds | `internal/gitprov/kinds.go:17` — `var Kinds = []string{KindGitHub, KindBitbucket}`, beside the two constants (lines 5–6) the same file declares |
| `providers.FromEnv` and its error text read it | `internal/gitprov/providers/providers.go:135` — `fmt.Errorf("unknown git provider %q (want %s)", kind, strings.Join(gitprov.Kinds, " or "))`, whose text is the sentence it has always printed |
| a switch on a kind names the constants | `internal/gitprov/gitprov.go` declares no kind any more (no `KindGitHub =` in it), and `KindForHost`/`KindForURL` return the constants |
| `TestKindsListedOnce` | `internal/gitprov/kinds_test.go:164` — every `Kind* = "…"` constant the package declares is in `Kinds`, exactly once, read out of the package's own source (`declaredKindConstants`) rather than written out again |
| `TestEveryKindSwitchIsInventoried` | `internal/gitprov/kinds_test.go:202` — the module's non-test Go files are parsed for a kind identifier or an exact-value string literal, every hit needs a row in `forgeKindGoSites` and every row has to still be a hit; the hand-kept `forgeKindNonGoSites` half holds the sites outside Go, where no scan can look |
| the existing `internal/gitprov/providers` tests, unchanged | `internal/gitprov/providers/providers_test.go` is the file #2332 left it, and green |
| no behaviour change | the commit's own message: the refusal reads `want github or bitbucket`, which is what it read before, and the literal provider lists (`config.Providers`, `runner.jobProviderKinds`) were deliberately left alone — they are not identifier switches |

The review rounds that followed the first commit are on `main` too: the scan reads a kind spelled as
a string as well as by identifier, a row pointing into a tree this checkout does not carry (CI's
core-without-ee job deletes `ee/`) is skipped while a deleted package is reported, and
`TestAbsentTreeSkipsOnlyAWholeMissingTree` (`internal/gitprov/kinds_test.go:271`) pins that rule
against a temp root.

## Evidence, run on this branch's base `2988c2d6`

- **The plan's command is green.** `GOFLAGS=-p=1 GOMAXPROCS=2 go test ./internal/gitprov/...
  -run 'Kind|FromEnv' -count=1` → `ok github.com/dimipaun/fugaro/internal/gitprov 0.546s`,
  `ok …/internal/gitprov/providers 0.125s`, and the four adapter packages `[no tests to run]`.
- **The red step is still a compile failure, shown without moving a file.** With `Kinds` deleted
  from a copy of `kinds.go` and that copy given to the tool as an overlay
  (`go test -overlay … ./internal/gitprov/ -run 'TestKindsListedOnce|TestEveryKindSwitchIsInventoried'
  -count=1`, the tree untouched):

      internal/gitprov/kinds_test.go:169:25: undefined: Kinds
      internal/gitprov/kinds_test.go:177:143: undefined: Kinds
      internal/gitprov/kinds_test.go:180:23: undefined: Kinds
      FAIL	github.com/dimipaun/fugaro/internal/gitprov [build failed]

- **The inventory guard is not vacuous.** A scratch `internal/gitprov/zz_mutation_tmp.go` holding
  `var _ = KindGitHub` fails the guard naming the file —

      internal/gitprov/zz_mutation_tmp.go names KindGitHub, and forgeKindGoSites has no row for it:
      add the row and say what the file does with the kinds — or that the kind it spells is not a
      provider kind — because a second forge has to visit it too

  — and the file removed, the same run is green again.
- **The repository's configured build and suite are green on this branch**: `fugaro verify build` and
  `fugaro verify test`, whose results the pull request carries.
