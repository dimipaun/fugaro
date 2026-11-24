# Contract: the per-repository view (C1)

`internal/cli/update_repos.go` and `internal/initflow/repos.go`. Every stage of `fugaro update` that
does per-repository work reads the project's repositories this way and no other. The rules it
serves are R12, R13, R16, R19 and R21 of [reconciliation.md](reconciliation.md); the task is C1 of
[implementation-plan.md](implementation-plan.md).

## The list

`enrolledRepos(ctx, enrolledIn) ([]repoView, []string)` answers one view per enrolled repository,
in canonical owner/name order, and the admit patterns the reader could not expand into names (one
notice line each, R19). `(e *initEngine).repos(ctx)` is the same call with the run's memo and this
machine's config; it is what a stage uses.

What a stage may rely on:

- **The same list from every directory.** The list is the project's: this machine's config's
  repositories and the concrete names the published policy admits. No caller passes a working
  directory, and none is read. The working directory picks the project and orders the human output,
  and does nothing else (spec §Guiding principles).
- **Memoized per run.** The first read of a command answers every later one, so the repositories
  are read once, and every stage and step of the run sees the same views (`reposState`, on the
  engine). A caller with no engine -- `doctor`'s two repository checks -- calls the free function
  and reads fresh.
- **Canonical order, always.** `--json`, the event stream and every summary line are in canonical
  owner/name order, whatever order the reader listed them in. Only the human output moves the
  hint's repository first, and `hintFirst` copies to do it: the slice the document is built from is
  never reordered.
- **The project layer, once.** `enrolledIn.Layer` is the layer the run already read, which every
  `fugaro.yaml` is parsed over; `enrolledIn.LayerFile` is the local layer file `update --check`
  passes instead, so the check makes no bucket read (R13, R17).

## A repository that could not be read

`repoView.Unread` carries one line saying why -- a 404, no access, no `fugaro.yaml` on the default
branch, a file that does not validate, a provider that cannot be read. `repoView.Read()` is the
yes/no, and the reader answers one shape or the other whole: an unread view carries no branch, no
head commit, no `fugaro.yaml`, no parse and no extra files, so a stage never holds half a
repository and never has to ask which half it may use.

- A stage **reports it and never guesses**: its line for that repository is `unread`, naming the
  reason and the fix, and it does no per-repository work for it.
- One unread repository never fails the update: every other repository is converged.
- A project-wide decision that needs every repository's file is held while any is unread, never
  made on part of the list.

## Extra files

A view carries `Files` by repository-relative path -- `.claude/settings.json`, `docs/backlog/` --
only when the caller asked for those paths. A stage reads nothing it did not ask for, and the
reader takes the extra paths as an option, so a stage that needs a file says so in the input rather
than reading one behind the list's back.

## The line

A stage reports one `initflow.RepoLine` per repository it has something to say about:
`{repo, state, detail}`, with the state `done`, `needs you`, `unread` or `skipped`. Every line is
built through `newRepoLine`, which passes the repository and the detail through
`pluginwire.Printable` and `safetext.OneLine`: a branch name, a path or a provider's own sentence
must not rewrite a terminal or fill the document (F27, R12).

- A stage records its lines with `e.reportRepos(stage, lines)` and hands the same lines back from
  `RepoLines() []RepoLine`, which is how the loop reads them (the `RepoReporter` interface, U13).
- A step after the stage loop -- the fold, the notices -- has no `Stage` and reports under its own
  step name the same way.

## No working-tree read

No stage reads a checkout. Each repository is read at the default branch the code host names, and a
local edit or a local checkout changes nothing about what `update` does. `--check` is the same read
with no write.

## Until I1

`internal/cli/update_repos_stub.go` stands in for the real reader (R21): it answers the working
directory's checkout as a single view, read the way the stages read it today, or none outside a
checkout. It is deleted by I1, which points the same seam at the reader that reads every repository
through the credentials service's person read mint (U2).
