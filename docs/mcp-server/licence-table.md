# The hosted MCP server: the dependency licence table

**Status: design answered by the owner 2026-11-04; M1, M2, M3, M6 and M4's first half are built and
merged on `main`; M5 is built except the paired device M-a asks for, which is M5b and is a hard
prerequisite before any binary links the front door (the owner's ruling of 2026-11-07); M7 is built
except the reading of the installation's published state its lists come from, which no task of this
plan carries; M10 is built except the store's own bucket and its one grant, which are M4's store
half and not started; M8 is built except the wiring that would serve its four read tools, which is
the task M5b gates; M9 is not started; this folder approves no task.**

**This file is a table, and it is not a gate.** The owner ruled on 2026-11-07 that he would clear the
`zitadel/oidc` dependency set by reading one table, and this is it: he read it and cleared the set
the same day. **The clearance is his own, and the record of it is
[licence-gate.md](licence-gate.md)** — [reconciliation.md](reconciliation.md) §6 check 1's gate file.
This table is the evidence that record stands on, and **it claims no clearance of its own**: what a
table can do is set out its rows so that a person can read them the way the check asks, which is what
the rest of this file does.

## What a row is, and how the rows were read

**A row is a module this branch adds or bumps against `origin/main`.** The two trees were compared
by running `go list -m -json all` in this checkout and in a copy of `origin/main`'s `go.mod` and
`go.sum`, and diffing the module paths and versions. That gives **23 rows**: **11 modules new to the
build list**, **11 whose version moved**, and **one** (`github.com/kr/text`) whose version is the
one `main` already built with and whose `go.mod` line is new.

**The licence of each row was read from the module's own file in the module cache**, not from a
summary and not from the module's repository: `go mod download -json <module>@<version>` gives the
module's directory, and the `LICENSE`/`COPYING` file in it is what the two quoted lines below come
from, verbatim. Where a module carries a `NOTICE`, its copyright line is quoted beside the licence's
own. **An SPDX identifier is named only where the text is the standard text of that licence** — a
file whose body differs from the standard text in any way has no identifier beside it and says so.
**No row is UNKNOWN**: every module below was found in the cache and its licence file was read, and
none of the 23 is a licence this table could not identify.

**"In the fugaro binary?" is the reachability of the module's packages in this tree**, read three
ways: `go list -deps ./cmd/fugaro` (the binary, with and without `-tags ee`), `go list -deps ./...`
(the module's own packages, which `go build ./...` compiles), and `go list -deps -test ./...` (what
only tests reach). The three answers that appear below are:

- **yes** — a package of the module is in `./cmd/fugaro`'s dependency graph, so its code is linked
  into the binary a person runs;
- **no, compiled with `ee/coordinator/mcpauth`** — the module is imported by that package, which
  `go build ./...` compiles, and **no binary links it**: nothing outside `ee/` imports the front
  door, which is what `TestNoBinaryLinksTheFrontDoorYet` holds and what **M5b** is the prerequisite
  for relaxing ([implementation-plan.md](implementation-plan.md) §3);
- **no, and no Go file here imports it** — the module is in the build list because a dependency's
  own `go.mod` requires it, and the requirer is named in the row. Its code is not compiled, linked
  or tested by anything in this repository.

**`go.sum` gained 27 lines, and 13 of them are a module's source.** The 13 source hashes are the
rows a build here compiles or tests: **ten of the eleven bumps** — every one but
`golang.org/x/tools`, whose source nothing fetches because nothing compiles it — plus the three new
modules `zitadel/oidc`, `zitadel/schema` and `muhlemmer/gu`. **The other 14 are `/go.mod`-only
lines**, which are the hash of a file read to resolve the graph and not of a module whose source is
used: the ten bumps' new versions, the three new modules' own, and `github.com/creack/pty` v1.1.9 —
a version below the v1.1.24 the build list already carried, whose `go.mod` is read and whose source
is not. **A module can sit in the build list with no line in `go.sum` at all**, and eight of the new
rows above do: they are required by `zitadel/oidc`'s `go.mod` and not by this repository's, and a
pruned graph reads the `go.mod` of a module the main module's own `go.mod` names.

**`go.sum` also loses 22 lines, and no module leaves the build list for it.** They are the two
hashes each of the nine bumps whose old version `main`'s `go.sum` listed — `doublestar/v4` v4.10.2,
`go-jose/v4` v4.1.4, `go-logr/logr` v1.4.3, `testify` v1.11.1, the three `otel` modules v1.44.0,
`x/sync` v0.23.0 and `x/text` v0.42.0 (`yaml/v3`'s and `x/tools`' old versions were in the build
list and not in `go.sum`) — plus both hashes of `github.com/davecgh/go-spew` and
`github.com/pmezard/go-difflib`, two modules `main` already carried at the version below and no Go
file here imports: both are still in the build list, so nothing in this table is a module the branch
takes away.

**The numbers above are what `go mod tidy` leaves**, and that is the state `go.sum` ships in: the
file is tidy on this branch (`go mod tidy -diff` prints nothing), so no line in it is kept for a
module the graph no longer needs.

## The 23 rows

**`Not clearly permissive` is empty on every row**, and that is the answer the owner reads this
table for: every licence below is the standard permissive text of its family — MIT, Apache 2.0,
BSD 3-clause or the Unlicense — with no field-of-use restriction, no copyleft, no source-disclosure
obligation, no advertising clause and no non-commercial term. The two rows whose licence is not a
single identifier (`go.yaml.in/yaml/v3`, dual MIT/Apache) are permissive either way, and the one
whose file is a public-domain dedication rather than a grant (`muhlemmer/gu`, the Unlicense) is
permissive in effect; both are noted under the table rather than left for the reader to find.

| Change | Module | Version | Licence, as the module's own file reads it | In the fugaro binary? | Not clearly permissive |
| --- | --- | --- | --- | --- | --- |
| new | `github.com/go-chi/chi/v5` | v5.5.0 | `MIT License`; `Copyright (c) 2015-present Peter Kieltyka (https://github.com/pkieltyka), Google Inc.` — `LICENSE`, body is the standard MIT text (SPDX: MIT) | no, and no Go file here imports it (`zitadel/oidc`'s `go.mod` requires it) | — |
| new | `github.com/golang/mock` | v1.6.0 | `Apache License` / `Version 2.0, January 2004`; **the file carries no copyright holder line** — its appendix holds the placeholder `Copyright [yyyy] [name of copyright owner]`, and the module's own source headers carry `// Copyright 2010 Google Inc.`, in its own `controller.go` beside the `gomock` package — not a file of this tree, so it is named and not cited — `LICENSE`, standard Apache 2.0 text (SPDX: Apache-2.0) | no, and no Go file here imports it (`zitadel/oidc`'s `go.mod` requires it) | — |
| new | `github.com/google/go-github/v31` | v31.0.0 | `Copyright (c) 2013 The go-github AUTHORS. All rights reserved.`; the file carries **no title line** — `LICENSE`, standard BSD 3-clause text, third clause `Neither the name of Google Inc. nor the names of its` (SPDX: BSD-3-Clause) | no, and no Go file here imports it (`zitadel/oidc`'s `go.mod` requires it) | — |
| new | `github.com/google/go-querystring` | v1.2.0 | `Copyright (c) 2013 Google. All rights reserved.`; no title line — `LICENSE`, standard BSD 3-clause text, third clause `Neither the name of Google Inc. nor the names of its` (SPDX: BSD-3-Clause) | no, and no Go file here imports it (`zitadel/oidc`'s `go.mod` requires it) | — |
| new | `github.com/gorilla/securecookie` | v1.1.2 | `Copyright (c) 2023 The Gorilla Authors. All rights reserved.`; no title line — `LICENSE`, standard BSD 3-clause text, third clause `Neither the name of Google Inc. nor the names of its` (SPDX: BSD-3-Clause) | no, and no Go file here imports it (`zitadel/oidc`'s `go.mod` requires it) | — |
| new | `github.com/jeremija/gosubmit` | v0.2.8 | `Copyright 2022 Jerko Steiner`; the file carries **no title line** — `LICENSE`, body is the standard MIT text (SPDX: MIT) | no, and no Go file here imports it (`zitadel/oidc`'s `go.mod` requires it) | — |
| new | `github.com/muhlemmer/gu` | v0.3.1 | `This is free and unencumbered software released into the public domain.`; **no copyright holder line** — the text dedicates the authors' copyright interest to the public domain and names nobody — `LICENSE`, the standard Unlicense text, closing `For more information, please refer to <https://unlicense.org>` (SPDX: Unlicense) | **no, compiled with `ee/coordinator/mcpauth`** through `zitadel/oidc`, which imports it, and linked into no binary | — |
| new | `github.com/muhlemmer/httpforwarded` | v0.1.0 | `Copyright (c) 2009 The Go Authors. All rights reserved.` and `Copyright (c) 2016 Tim Heckman. All rights reserved.`; no title line — `LICENSE`, standard BSD 3-clause text, third clause `Neither the name of Google Inc. nor the names of its` (SPDX: BSD-3-Clause) | no, and no Go file here imports it (`zitadel/oidc`'s `go.mod` requires it) | — |
| new | `github.com/rs/cors` | v1.11.1 | `Copyright (c) 2014 Olivier Poitrey <rs@dailymotion.com>`; the file carries **no title line** — `LICENSE`, body is the standard MIT text (SPDX: MIT) | no, and no Go file here imports it (`zitadel/oidc`'s `go.mod` requires it) | — |
| new | `github.com/zitadel/oidc/v3` | v3.51.17 | `Apache License` / `Version 2.0, January 2004`; **`LICENSE` carries no copyright holder line** — the module's `NOTICE` reads `Copyright The zitadel/oidc Contributors` — `LICENSE`, standard Apache 2.0 text (SPDX: Apache-2.0) | **no, compiled with `ee/coordinator/mcpauth`**, which imports its `pkg/oidc` package, and linked into no binary | — |
| new | `github.com/zitadel/schema` | v1.3.2 | `Copyright (c) 2023 The Gorilla Authors. All rights reserved.`; no title line — `LICENSE`, standard BSD 3-clause text, third clause `Neither the name of Google Inc. nor the names of its` (SPDX: BSD-3-Clause) | **no, compiled with `ee/coordinator/mcpauth`** through `zitadel/oidc`, which imports it, and linked into no binary | — |
| bumped | `github.com/bmatcuk/doublestar/v4` | v4.10.2 → v4.10.4 | `The MIT License (MIT)`; `Copyright (c) 2014 Bob Matcuk` — `LICENSE`, body is the standard MIT text (SPDX: MIT) | **yes** | — |
| bumped | `github.com/go-jose/go-jose/v4` | v4.1.4 → v4.1.6 | `Apache License` / `Version 2.0, January 2004`; **the file carries no copyright holder line and the module ships no `NOTICE`** — `LICENSE`, standard Apache 2.0 text (SPDX: Apache-2.0) | **yes** | — |
| bumped | `github.com/go-logr/logr` | v1.4.3 → v1.4.4 | `Apache License` / `Version 2.0, January 2004`; **no copyright holder line in the file, and no `NOTICE`** — `LICENSE`, standard Apache 2.0 text (SPDX: Apache-2.0) | **yes** | — |
| bumped | `github.com/stretchr/testify` | v1.11.1 → v1.12.1 | `MIT License`; `Copyright (c) 2012-2020 Mat Ryer, Tyler Bunnell and contributors.` — `LICENSE`, standard MIT text (SPDX: MIT) | no, and no Go file here imports it: `zitadel/oidc`'s **own tests** do (`pkg/oidc.test` → `testify/assert`, `go mod why -m`) | — |
| bumped | `go.opentelemetry.io/otel` | v1.44.0 → v1.46.0 | `Apache License` / `Version 2.0, January 2004`; **no copyright holder line in the file, and no `NOTICE`** — `LICENSE`, standard Apache 2.0 text (SPDX: Apache-2.0) | **yes** | — |
| bumped | `go.opentelemetry.io/otel/metric` | v1.44.0 → v1.46.0 | `Apache License` / `Version 2.0, January 2004`; **no copyright holder line in the file, and no `NOTICE`** — `LICENSE`, standard Apache 2.0 text (SPDX: Apache-2.0) | **yes** | — |
| bumped | `go.opentelemetry.io/otel/trace` | v1.44.0 → v1.46.0 | `Apache License` / `Version 2.0, January 2004`; **no copyright holder line in the file, and no `NOTICE`** — `LICENSE`, standard Apache 2.0 text (SPDX: Apache-2.0) | **yes** | — |
| bumped | `go.yaml.in/yaml/v3` | v3.0.4 → v3.0.5 | `This project is covered by two different licenses: MIT and Apache.`, then `#### MIT License ####` for the files ported from libyaml with `Copyright (c) 2006-2010 Kirill Simonov` and `Copyright (c) 2006-2011 Kirill Simonov`, and `### Apache License ###` for the rest; `NOTICE` reads `Copyright 2011-2016 Canonical Ltd.` — the Apache half is the standard text (SPDX: MIT OR Apache-2.0) | no, and no Go file here imports it: `zitadel/oidc`'s **own tests** reach it (`testify/assert/yaml`, `go mod why -m`) | — |
| bumped | `golang.org/x/sync` | v0.23.0 → v0.24.0 | `Copyright 2009 The Go Authors.`; no title line — `LICENSE`, standard BSD 3-clause text, third clause `Neither the name of Google LLC nor the names of its`, and a `PATENTS` file granting a patent licence beside it (SPDX: BSD-3-Clause) | **yes** | — |
| bumped | `golang.org/x/text` | v0.42.0 → v0.43.0 | `Copyright 2009 The Go Authors.`; no title line — `LICENSE`, standard BSD 3-clause text, third clause `Neither the name of Google LLC nor the names of its`, and a `PATENTS` file beside it (SPDX: BSD-3-Clause) | **yes** | — |
| bumped | `golang.org/x/tools` | v0.49.0 → v0.51.0 | `Copyright 2009 The Go Authors.`; no title line — `LICENSE`, standard BSD 3-clause text, third clause `Neither the name of Google LLC nor the names of its`, and a `PATENTS` file beside it (SPDX: BSD-3-Clause) | no, and no Go file here imports it (`golang.org/x/text` v0.43.0's `go.mod` requires it) | — |
| `go.mod` line only | `github.com/kr/text` | v0.2.0 (unchanged: the version `main` already built with) | `Copyright 2012 Keith Rarick`; no title line — the file is named `License`, and its body is the standard MIT text (SPDX: MIT) | no, and no Go file here imports it (`gopkg.in/check.v1`, which `gopkg.in/yaml.v3`'s own tests import, requires it: `go mod why -m`) | — |

## What the rows do not settle, and what is beside them

- **Why the bumps are in this branch at all.** Every version that moved is required by
  `github.com/zitadel/oidc/v3` v3.51.17's own `go.mod`, or by a module it moved: `go mod graph`
  names `zitadel/oidc` as the requirer of `doublestar/v4` v4.10.4, `go-jose/v4` v4.1.6,
  `go-logr/logr` v1.4.4, `x/text` v0.43.0 and the three `otel` modules, and `x/text` v0.43.0 as the
  requirer of `x/sync` v0.24.0 and `x/tools` v0.51.0. **The bumps are not this work's choices**: a
  minimum-version selection raises the version of a module already in the build list when a new
  dependency's `go.mod` requires a higher one, and taking the library out of `go.mod` is the one
  change that puts every one of those versions back ([reconciliation.md](reconciliation.md) §6,
  check 1).
- **Which rows are new code in a binary, and which are not.** **Eight modules are linked into the
  fugaro binary** — the bumps to `doublestar/v4`, `go-jose/v4`, `go-logr/logr`, `x/text`, `x/sync`
  and the three `otel` modules — and **every one of them was already linked into it before this
  branch**, at the lower version: what the branch changes about them is a version, not a dependency.
  **Three modules are compiled and linked into nothing** (`zitadel/oidc`, which
  `ee/coordinator/mcpauth` imports, and `zitadel/schema` and `muhlemmer/gu`, which it reaches through
  that library), and **twelve are imported by no Go file in this repository at all**: eight new ones
  that `zitadel/oidc`'s `go.mod` requires, three bumps (`testify`, `go.yaml.in/yaml/v3`, `x/tools`)
  that the library's own tests reach, and the one module whose `go.mod` line is new without its
  version moving (`kr/text`, reached from `gopkg.in/yaml.v3`'s tests).
- **The one library call.** `ee/coordinator/mcpauth` calls **one function** from `zitadel/oidc`
  (`oidc.VerifyCodeChallenge`, `ee/coordinator/mcpauth/token.go:217`); everything else it takes from
  the library is a type, a constant or an error value. That is the surface the whole table stands
  behind, and it is what §6 check 1's second alternative — the library out of `go.mod` and M5 rebuilt
  without it — would replace.
- **The two rows that are not a single identifier.** `go.yaml.in/yaml/v3` is **dual-licensed MIT or
  Apache 2.0**, the choice being the licensee's, and both halves are permissive;
  `muhlemmer/gu` is the **Unlicense**, a public-domain dedication rather than a grant of rights,
  which some policies read differently from MIT even though it is permissive in effect. Both are
  named here so the reading is the owner's rather than this table's.
- **Apache-2.0 rows with no copyright line.** Six of the rows (`golang/mock`, `go-jose/v4`,
  `go-logr/logr` and the three `otel` modules) ship the standard Apache 2.0 text with the appendix's
  placeholder left in place and no `NOTICE`; `zitadel/oidc` ships a `NOTICE`. The absence of a
  copyright line is a property of those files and is quoted as such rather than filled in from
  anywhere else, and it does not change the licence: Apache 2.0 §4 requires a `NOTICE` only where
  the work ships one.
- **What this table does not cover.** It reads the modules' licence files and nothing else: it is
  not counsel's clearance, it does not read the copyright headers of individual source files, and it
  says nothing about the modules `main` already carried. **The gate it is evidence for is
  [reconciliation.md](reconciliation.md) §6, check 1, and the owner cleared it on 2026-11-07 by
  reading this table: the record is [licence-gate.md](licence-gate.md).**
