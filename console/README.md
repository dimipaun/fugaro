# console/

**Nothing under `console/` is importable by, or imports, the Go module.**

This directory holds the Fugaro desktop console — the Electron application the
owner's spec asks for in its section 6, designed in
[docs/design/desktop-console.md](../docs/design/desktop-console.md) (which placed
it here under `console/`, D3) and built by
[docs/plans/2026-10-15-desktop-console.md](../docs/plans/2026-10-15-desktop-console.md).

It is not a Go package and it never becomes one. `go build ./...`,
`go vet ./...` and `gofmt -l .` take no notice of it, `go.mod` and `go.sum` gain
nothing from it, and no Go file in this module builds against a path under
`console/` (design §8.1 item 4). Three Go tests hold that, in
`internal/cli/docs_console_boundary_test.go`:
`TestConsoleIsOutsideTheGoModule`, `TestGoModHasNoConsoleDependency` and
`TestNoGoFileEmbedsFromConsole`. A `.go` file or a `go.mod` in this directory, an
import of a `console/` path, a `go:embed` over one, or a require line added to
the root module without the baseline being edited fails one of them by name.

One Go file may *mention* a `console/` path without importing anything from it,
because the console's JSON contract is a document a test reads as bytes:
`internal/cli/docs_desktop_console_test.go`, which reads
`console/docs/json-contract.md`. It is the single entry on that boundary test's
allow-list; a second entry is a deliberate edit to the list, with the case for it
written down, rather than a quiet new reference somewhere else.

That mention check is a heuristic, and it is honest about both halves. Its floor
is the plan's literal rule: a Go file whose text contains the string `console/`
is a mention, wherever that string appears — so a string like
`"github.com/x/console/y"` counts too, and the answer is an allow-list entry or a
reworded string, decided on purpose. Above the floor, the file is parsed and read
as paths, which catches the spellings no single literal holds: a `console` *path
segment* next to `docs`, or written as a directory, including inside a
`filepath.Join("..", "..", "console", "docs", "x")` argument list. What it does
not do: follow a path assembled from variables, or prove that a `console` segment
is *this* repository's console. Both limits are on the safe side — a missed
spelling in a live file would be caught by the import and `go:embed` checks if it
did anything, and an over-reported one costs an entry in the list, said out loud.

The scan reads every `.go` file of the module except the directories the go
command ignores (`.git/`, `node_modules/`, and any name beginning with `.` or
`_`, so a nested checkout under `.worktrees/` cannot fail it), and it reads
`testdata/` deliberately.

What is here today: this file, and the contract the console reads
(`docs/json-contract.md`, pinned by plan Task 0.1). The Node toolchain (plan
Task 1.0) comes next, and this file gains Electron's pinned version with it.
