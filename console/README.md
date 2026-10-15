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

What is here today: this file, and the contract the console reads
(`docs/json-contract.md`, pinned by plan Task 0.1). The Node toolchain (plan
Task 1.0) comes next, and this file gains Electron's pinned version with it.
