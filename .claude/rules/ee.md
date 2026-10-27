---
paths:
  - "ee/**"
---

# `ee/` — net-new organisation features, under their own licence

- `ee/` is under **`ee/LICENSE`, a draft counsel has not read** (D311, 2026-10-26): the file's first line says so and only the owner removes it. The folder holds no code yet and has never been released. `ee/README.md` says so and `TestDocsEELicensed` holds it. Nothing moves out of the core into `ee/` (D302), and an outside contribution here accepts `ee/LICENSE` and needs the signed CLA (`CONTRIBUTING.md`). Never edit the licence text itself: it is the owner's and counsel's.
- **Nothing outside `ee/` imports anything under `ee/`.** The core builds and tests with this directory deleted, and that is what keeps `LICENSE`, the plugin manifest and the Homebrew cask true. `internal/eeboundary/boundary_test.go` is the only guard — `TestCoreNeverImportsEE`, `TestCoreNeverEmbedsEE`, `TestNoModuleRouteIntoEE` — and it refuses a `//go:build ee` core file, an embed, a symlink, a `//go:generate` line, a cgo include, a `replace` directive and a nested `go.mod` just as hard as a plain import. Never edit a test to admit a route into `ee/`: that is the owner's decision. A contributor who deletes the test is caught by review, which is why this rule names the tests.
- **One exception, `cmd/fugaro/ee_on.go` exactly** (D303 as amended, R6): `//go:build ee` and nothing else, package `main`, one blank import of the registration package `github.com/dimipaun/fugaro/ee/register`. No second assembly file, no `ee/register` imported under a name, no other directory's file of that name, and no core package that imports `ee/` at all.
- **Every file under `ee/` starts with the two-line notice** (R3, `TestEEFilesCarryTheNotice`): `SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise`, then `See ee/LICENSE for the terms.`, in the comment syntax of its type. Only `ee/README.md` and `ee/LICENSE` are exempt; a `#!` script carries it on lines 2 and 3; a type with no comment syntax needs a sibling `<name>.license` file; a symlink is refused. `ee/LICENSE` must exist as soon as one source file does (`TestEESourceNeedsTheLicence`).
- **No key material ever enters a file here** (R8): not the licence-key private key, not a fixture shaped like one. Minting is the owner's, off every machine an agent reaches, and the licence-key check stays inside `ee/` where nothing in the core can read it.
- The release sets the `ee` build tag only in the release that relabels every licence label together (`scripts/notices_test.go`'s `TestEETagNeedsTheLabels`), never on its own.
- After a change here run `go test ./internal/eeboundary/ -count=1` and, when a doc moved with it, `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
