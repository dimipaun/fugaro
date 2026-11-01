# Shared images keyed by the resolved dependency set

**Status: design only; C8a is built to it (2026-10-30) — the key, the superset lookup and the
lock, in `internal/imagecheck` — and C8b and C8c are not.** *The status as first written, kept:*
**Status: design only; nothing here is implemented.** This is the design the plan's task **C7**
(`implementation-plan.md`) asks for; **C8** builds it, and this file is meant to be concrete enough
that C8 needs no further design. Core MIT: nothing in this design is `ee/` behaviour.

The decisions are settled. **D617**: the published policy is the intent and the lock lives in the
runs bucket beside `builds/`. **D618 as answered** (the owner chose *against* the recommendation):
images are shared between repositories whose resolved dependency set is identical, scoped to exactly
that. **D619**: warn at launch on a digest mismatch; no pinning, since Cloud Run cannot vary the image
per execution (U4). **D620**: key files before the source layer. **D621**: trigger-based rebuilds; a
build is Cloud Build cost outside the run budget. **D622**: the resolver runs only on the operator's
machine. **D625**: propose, then confirm. Tension **T6** and open item **O6** are resolved
(2026-10-29): each repository's job is updated per build, the bucket lock is the one source, and a
divergence is a fork under user control, never automatic. The image-count cap (§8), the retention
(§9) and the C3/C4 question are ruled too (2026-10-29); the job's clone shape (§6) is still the
owner's. All are listed at the end of this file.

## 1. What is shared, and what is not

An image is shared between repositories whose **resolved dependency set** is identical. The set is
the thing that costs: a build installs it once, and every repository on that key runs an image that
already holds it. Nothing else is shared — not the source, not the workflow, not the repository's
secrets, not its job.

Only a **baked** workflow — one whose job runs a per-repository derived image — takes part. A
`clone` workflow's job runs the installation's base image directly and builds nothing
(`internal/infra/repo.go`'s `NeedsBuild`), so it is outside the key universe: it has no key, no
lock entry and no shared image, and its job is not moved by anything in this design.

The shared image holds the **dependency layer only**. Today's derived image bakes one repository's
source at its commit (`images/derived/Dockerfile.tmpl`, `internal/image/render.go`), and the
registry-readers note the plan's task **C6** would add describes that; C6 is not written, so no
SECURITY.md paragraph exists to amend yet. The shared image holds no repository's source at all, so
one repository's registry readers never read another's through it.

Where the source then comes from is a constraint, not a detail, and the plan's C8 file list does not
carry it. A run gets its source from a clone **only when its job is clone-shaped**: `internal/infra/spec.go`
writes `FUGARO_CHECKOUT=clone` and `FUGARO_REMOTE` on a `checkout: clone` workflow's job and on no
other, the runner clones at launch only when `r.cloneJob()` is true (`internal/runner/provision.go`;
`internal/runner/runner.go`'s `openCheckout` clones only on that answer and otherwise opens the
checkout the image is expected to hold), and `PlanWorkflowJob` refuses a job without the marker for
exactly this reason — moving its image "would leave a job expecting a baked checkout"
(`internal/infra/checkjob.go`). A **baked** workflow's job — the kind §10 migrates — carries neither
marker, so a baked job moved onto a source-less image finds no checkout and no remote and fails at
bootstrap. §6 is about the fix: C8 must make the job clone-shaped as well as point it at the shared
image, and `FUGARO_COMMIT` is not an answer — it is a Cloud Build build argument
(`images/derived/Dockerfile.tmpl`) that no launch reads.

## 2. The normalised key

### 2.1 The four inputs

The key is computed from exactly four things, and nothing else:

| Input | Where it comes from |
| --- | --- |
| Resolved package names, versions and integrities | the workflow's key files — the blobs `imagecheck.KeyFiles` already returns (the workflow's `cache:` key globs, or, with no `cache:`, `config.DefaultCache`'s per-base default), reduced per ecosystem as §2.3 |
| Toolchain | the root `mise.toml` / `.tool-versions` tool pins (`config.MiseConfigFiles`) and `image.tools`' mise tool → version pairs |
| Base image | the base the build ran FROM: the record's `BaseRef` and `BaseDigest` |
| (the key's own version) | the canonical form's version line, §2.4 |

The key is normalised: **only resolved package names, versions and their integrities, toolchain
and base image count**. The integrity is the one each key file states for that package's version
(D618, answered 2026-10-31), and it is part of the package input and not a fifth one: a version is
what a lockfile resolved to, and the integrity or checksum beside it is what that version *was* —
the artifact the image is built from — so two repositories that resolved one version to two
artifacts are two dependency sets and two keys. §2.3 and §2.4 state the rule and its consequences: a
package from a source that is not the ecosystem's registry leaves the repository unkeyed (§2.5), and
a package a file states **no** integrity for is keyed only when the file also says it came from the
ecosystem's **default registry** — the rule §2.3 states, with each ecosystem's default registry
named there — so a name and a version alone never stand in for an artifact.

One case sits outside those four and is not a fifth input: a key file whose format §2.3 does not
name cannot be reduced, and the repository is then **unkeyed** (§2.5) rather than hashed — a format
Fugaro cannot read must never be silently dropped, because two repositories whose tool files differ
would otherwise share an image.

### 2.2 Why `image.setup` is not an input

The plan's C7 sentence lists `image.setup` among the key's inputs; O6's control (1), the later and
owner-accepted ruling, fixes the inputs to resolved names, versions and their integrities, toolchain
and base image, and
this design follows O6 — stated here rather than silently, because the two texts differ. The
consequence is named where it is paid: the baked `image.setup` is the one of whichever repository
first built that key, so a repository whose `image.setup` differs while producing the same set
inherits that step (§4). A change to `image.setup` still forces a **rebuild** — it is part of the
existing `ImageConfigHash` (`internal/imagecheck/record.go`), which is the rebuild trigger and not
the sharing key — but it does not by itself make a new key, and so does not fork.

### 2.3 Reducing each ecosystem's lockfile to names, versions and integrities

The workflow's key files are the evidence set; each is reduced by its own format. **Every fact
carries the integrity or checksum the file states for that version's artifact** (D618): npm's
`integrity`, go.sum's `h1:` hash, Cargo's `checksum`, composer's `dist.shasum` or, for a package it
took from a VCS, its `source.reference`, Bundler's `CHECKSUMS`, Poetry's and uv's artifact hashes,
pipenv's hashes, and the `--hash` lines a pip pin carries.

**A package a file states no integrity for is keyed only when the file also says it came from the
ecosystem's default registry** (D618, as the reviewer's probes of 2026-10-31 refined it): name and
version then say which artifact it is, because that registry serves one artifact per version of a
package. A source the file states that is *not* the default registry — a private index, a mirror, a
second registry — is fine beside an integrity, since the integrity is of the artifact whatever host
served it; what it cannot be is the only thing an entry has. A source is read as a URL and not as
text: the host is the one the URL's authority names, so a source that states the default registry's
name in userinfo (`https://registry.npmjs.org:x@evil.example/x.tgz`, whose host is `evil.example`)
is not the default registry's, and a source stating userinfo at all is never read as its. Where the
format's own convention says a source belongs and the file states none — a Bundler `GEM` block with
no `remote:` line, a composer entry with neither a `dist.shasum` nor a `source.reference` — the
entry is not the default registry's either, and it leaves the repository unkeyed:

| Ecosystem | The default registry, and what the format writes for it |
| --- | --- |
| npm | `registry.npmjs.org`, and Yarn's own alias of it, `registry.yarnpkg.com`: the host of the entry's `resolved` URL |
| pnpm | the same two hosts, and a key that is a bare `name@version` is the default registry's by the format's convention, which writes no source line for one. That convention is the one place the key rests on a file it does not read: the registry an install fetches a bare entry from is configured in `.npmrc`, which states no version and contributes no fact, so such a lock keys the same whether that file names the default registry or another one — the limit the shared-image boundary section of [SECURITY.md](../../SECURITY.md) states |
| Go modules | `proxy.golang.org`, and the module path's own origin: go.sum states an `h1:` hash for every module it records |
| crates.io | the git index and the sparse index (`index.crates.io`), which are what a `source` line names |
| PyPI | `pypi.org` and `files.pythonhosted.org`: a uv or Poetry `registry` source, and Poetry's *absent* `[package.source]`, which is PyPI by the format's convention. A Poetry `legacy` source is a registry whatever host it names, so it is keyed only beside the artifact hashes it states |
| RubyGems | `rubygems.org`: the `remote:` of the `GEM` block the entry is written in. A `PATH` gem is the repository's own tree and is keyed by the version the lock states for it |
| Packagist | `repo.packagist.org`, which composer.lock does **not** name per entry: a package's `dist.url` is the vendor's own archive host (a packagist package's is GitHub's), so an entry stating neither a shasum nor a reference is unkeyed rather than read as the default registry's |

The four remaining shapes below are the exceptions the design names; every other key file is unkeyed
(§2.5):

- A package a file resolves from **a source that is not the ecosystem's registry** leaves the
  repository **unkeyed**, never keyed by the versions beside it: the artifact is one the registry
  never served, so the image could not be built from it. The shapes are per ecosystem — an npm alias
  (`node_modules/<alias>` whose entry names another package) or a `resolved` that is a git, file,
  link or bare path; a pnpm key that holds `@npm:`, `@git+`, `@file:`, `@link:`, `@workspace:`,
  `@patch:` or `@registry:` or a resolution that names a git, repo, directory, file, link or tarball;
  a Yarn `resolved` or `resolution` that names a git, file, link, exec or portal source; a requirements
  line that is a URL, a `git+…` requirement or a direct reference (`name @ https://…`); a Poetry
  source of type `git`, `file`, `directory` or `url` (a `legacy` private index is a registry); a uv
  `git`, `path`, `directory`, `file` or `url` source or an `editable`/`virtual` source that is not
  `.`; a Pipfile entry's `git`, `path` or `file`; a Cargo `source` that is neither the registry, the
  sparse index nor absent; a Bundler `GIT` block; a composer `dev-` branch or `path` repository.
  **Three of those are read one step earlier than the source line, because the source line does not
  name a source**: a Yarn alias's header (`lodash@npm:evil-pkg@^1.0.0`) and a Yarn git descriptor's
  are the header, and the resolved line beside an alias is the *aliased* package's; a Yarn 1 git
  dependency's resolved line is a codeload archive, a plain `https` URL at a host no host check can
  tell from another registry's tarball, while its header states the git specifier or the `owner/repo`
  shorthand the dependency was declared with; an npm lockfile v1 (and a `packages` entry with no
  `resolved`) states the source in its `version` field, so a `version` holding a scheme — a git URL,
  `github:owner/repo#ref`, `file:…` — is a source and not a version. Each is refused rather than
  keyed.
- The repository's **own tree is not such a source**: an npm workspace member's entry, a crate the
  Cargo lock writes with no source of its own, a uv or Poetry project's own entry, a Berry
  `workspace:` entry and a Bundler `PATH` gem are part of the set the lock resolves, and they are
  keyed by the version the lock states for them: the image holds no artifact for one of those, so the
  name and the version are all there is to say about it.
- A **version that is not a version** — a composer `dev-main`, a branch a lock wrote under a moving
  reference — is one the registry served but not one that identifies an artifact, and it is the same
  answer for the same reason.
- Strictness around the fact's three fields is part of the reduction: the fields are split from the
  right, so a **name may hold an `@`** (a scoped npm name) and a **version may not**, no field holds
  whitespace, and a NUL in a name or any other control character in any of the three is refused
  (`UnkeyedUnreadable`) rather than keyed by. §2.4 says what that buys: the fields are encoded apart,
  so no fact can hold one of the fields of another.

Each file's own reduction:

| File | Reduction |
| --- | --- |
| `package-lock.json` | every entry of `packages` with a `version` (the root `""` entry is skipped: it is the repository, not a dependency), as its `name` (the path after the last `node_modules/`, or the entry's own name for a workspace member's entry, which is written under the member's path) and `version`, transitives included |
| `npm-shrinkwrap.json` | `package-lock.json`'s reduction: one format under the two names npm writes it under, and a repository's lockfile is whichever of them npm wrote |
| `pnpm-lock.yaml`, `pnpm-lock.yml` | every `packages` / `snapshots` key, split at the last `@` into name and version, with the leading `/` a lockfileVersion 6.0 lock (pnpm 8) writes and a peer suffix in parentheses neither part of the name nor of the version: the two sections name the same packages and are keyed by the package a key names, so a v9 snapshot key written with its peers (`x@1.0.0(react@18.0.0)`) and the `packages` entry for the same x are one package and one fact, and neither the peer suffix nor the section a key is written in adds a fact of its own. A `resolution:` written as a block — the field on its own line with `integrity:` or `tarball:` under it, which older locks write — is read like the one-line form, and a `resolution:` with an empty body is refused rather than keyed with no source and no integrity beside it; a `resolution` field's value is read with the commas inside its quotes left alone, so an integrity or a URL holding one is the value the lock states. A key no package name and version can be read from is refused rather than keyed from the text of its key: a lockfileVersion 5.x lock (pnpm 5, 6 and 7) writes `/name/version`, hanging the peer suffixes on the version's own text, so reading one would key a fact for a package like `react-dom/18.0.0_react`, a name no registry holds, and every 5.x lock is unkeyed (§2.5). A lock older than both sections, which states its dependencies against bare versions under a top-level `dependencies:`, `devDependencies:` or `optionalDependencies:` and holds no `packages` or `snapshots` map at all, is refused rather than read as one that holds nothing (§2.5) |
| `yarn.lock` | every entry header line `"name@range":` — **every** range it lists, not only the first, since a header whose second descriptor is a git or file specifier states a source no registry served — with its `version` field, which Yarn 1 writes after a space and Berry after a colon; the `__metadata` block is the lock's own header and is ignored. A Berry `patch:` descriptor — `typescript@patch:typescript@npm%3A5.4.5#optional!builtin<compat/typescript>`, in the header and in the resolution line beside it — is read by its inner `npm%3A` range, the package's own registry version, with the `%3A` colon restored: the entry is the registry's, and the patch's own name, the `builtin<...>` it writes (the `optional!` Yarn may write before it says the patch is applied only where it applies and is not part of the name), joins the entry's checksum as the fact's third field, sorted and deduplicated. Two locks differing only in which built-in patch they applied are then two facts and two keys, since the patch is what the install applies to that artifact and the checksum beside it says nothing about it. A patch whose bytes the repository holds — `#./patches/typescript.patch`, `#~/.yarn/patches/typescript.patch`, or a bare hash — names no built-in patch, and those bytes are not in the fact, so the repository stays **unkeyed** (§2.5) |
| `go.sum`, `go.work.sum` | every `module version` line, with a `/go.mod`-suffixed line merged into the module it names rather than a second fact, so each module@version appears once; the integrity is the module's own hash, from the line that is not the `/go.mod` one where the file states both. Where the file states **two** `h1:` lines for one module at one version — the go command appends a line for every hash it verifies — both are joined into one integrity, **sorted and deduplicated**: keeping the last line alone would make the fact, and so the key, depend on the order the file happened to write them in, and the same two lines in the other order would be another key |
| `requirements.txt`, `requirements*.txt` | every `name==version` pin, with the name as PEP 503 normalises it and any extras dropped from the name, and with the `--hash` values it carries as its integrity; its environment marker and a full-line comment are the pin's own and are not facts. pip-tools and `uv pip compile --universal` write the marker and the continuation together (`name==1.0 ; python_version < "3.11" \`), so the backslash ends the marker and the hashes after it belong to that pin. A line that is not a pin — `-r`, a URL, an editable install, an option, an unpinned name — makes the repository **unkeyed**, because the file then does not state what pip would install |
| `poetry.lock`, `uv.lock`, `Pipfile.lock` | each `[[package]]` / `package` entry's `name` and `version`, with the artifact hashes the entry states — Poetry's `files` list or `[metadata.files]` table, uv's `sdist` and `wheels`, Pipfile's `hashes` — as its integrity. Pipfile.lock's `default` and `develop` sections are both read, and the `==` its versions are written with is pip's operator, not part of the version. An entry that states no hashes is keyed only when the lock says it came from PyPI's own index: its own `index` field names a source in `_meta.sources` and is resolved through it, an entry that names none came from the first source the lock lists — that is the order pipenv tries them in — and a source the lock does not list is not PyPI's |
| `Cargo.lock` | each `[[package]]` entry's `name` and `version` with its `checksum`, the workspace's own crate included: it is one of the lock's packages, and it states no checksum of its own |
| `Gemfile.lock`, `composer.lock` | each `specs` / `packages` entry's `name` and `version`, with the gem's `CHECKSUMS` line as its integrity and, for composer, `dist.shasum` or the `source.reference` the package was installed at; Bundler's `GEM` and `PATH` blocks and composer's `packages` and `packages-dev` are both read, and Dev-only dependencies are not exempt (O6). Bundler's `GIT` block, a composer `dev-` branch and a composer `path` repository are non-registry sources and leave the repository unkeyed (§2.5) |

The key-file layer carries more than lockfiles (D620: the install above the source layer reads them),
and the rest of it states no resolved package set of its own. Each contributes no `pkg:` fact, and
none of them makes a repository unkeyed:

| File | Reduction |
| --- | --- |
| mise's configuration — `mise.toml`, `.mise.toml`, `mise/config.toml`, `.mise/config.toml`, `.config/mise.toml`, `.config/mise/config.toml`, `.tool-versions`, `mise.lock`, the `conf.d` fragments under `mise/`, `.mise/` and `.config/mise/` | the **toolchain**, §2.1's second input rather than a package set: each tool's pin as `tool@version`. These are key files because the install reads them, and §2.1's tool half is what the key takes from them, so no `pkg:` fact comes from one |
| `package.json`, each workspace member's `package.json`, and the manifest beside a lockfile the table reads — `go.mod`, `Cargo.toml`, `pyproject.toml`, `Gemfile`, `composer.json`, `Pipfile` | nothing: they state dependency ranges, the workspace's members and the direct dependencies, and the lockfile beside them is what resolves those |
| `pnpm-workspace.yaml` | nothing: it names the workspace's members, not a resolved set |
| `.npmrc`, `.yarnrc`, `.yarnrc.yml`, `.pnpmfile.cjs`, `.yarn/releases/…`, `.yarn/plugins/…`, `.yarn/patches/…`, and the `.patch` or `.diff` path a pnpm `patchedDependencies` entry names | nothing: they configure the install and patch its inputs, so a change to one rebuilds the image (the key files and `ImageConfigHash`), and none of them names a version |
| a path `image.key_files` names | by its own name when the table above knows the format — an operator may key a lockfile the built-ins do not, or a manifest beside one, which states ranges and contributes nothing — and **unkeyed** when neither table names it (§2.5): the default for a key file is unkeyed, never "no dependencies", because a file Fugaro does not read is not a file that resolved to nothing |

**The no-facts table is a statement about the key, not about what those files do.** Most of them
either run during the install the image bakes or point that install somewhere else: `.npmrc` and
`.yarnrc.yml` name registries, `.pnpmfile.cjs` and a plugin under `.yarn/plugins/` are code the
package manager loads, a patch under `.yarn/patches/` or the path a pnpm `patchedDependencies` entry
names rewrites a package's source before it is installed, a `package.json` `scripts` entry —
`preinstall`, `postinstall`, and whatever a dependency's own scripts do — executes as the install
runs, a `.yarn/cache` holds artifacts an install may reuse instead of fetching, and a `go.mod`
`replace` directive sends a module to another tree than the one its version names. None of that is a
fact of the key, and the integrity rules above do **not** stand between repositories of one
installation: a repository that can change one of those files changes what the next build of that key
installs for **every** repository already on it. That is the same class as the `image.setup` of the
repository that first built the key, which the rest inherit (§2.2): image sharing is a decision about
repositories an installation already trusts, and the key is not a boundary between them.

Anything else — any key file named in `key_files` that neither table names — makes the repository
**unkeyed** (§2.5) and is never read as "no dependencies". The default answer for an unknown file is
the refusal, because a file read as the empty set would key the repository exactly as every other
repository with a file Fugaro does not understand is keyed. It is one rule with many shapes:
`gradle.lockfile`, `conda-lock.yml`, `environment.yml`, Swift's `Package.resolved`, `bun.lockb`,
`Podfile.lock` and `packages.lock.json` are locks of ecosystems the table does not name;
`constraints.txt`, `dev-requirements.txt` and `requirements/dev.txt` state resolved sets under names
the requirements row does not match (a constraint file pins what is installed, and reading it as
"no facts" is the same mistake); and a tool file is recognised by the convention of its name rather
than by a list that closes: `.nvmrc`, `.sdkmanrc`, `global.json`, `rust-toolchain.toml` and the
`-version` files (`.python-version`, `.terraform-version`, …) are all named after the tool they pin,
and one Fugaro read as "no fact" would key the repository while its image was installed for another
repository's toolchain. The no-facts table above is the whole of the exemption, and it is written by
path: a `.patch` file and a `.yarn/**` path are there because the layer the install reads them from
is D620's, not because their names look like locks. Dev-only dependencies are not exempt from the
other direction: they make a key like any other, because the owner did not choose to exempt them
(O6).

A key file that cannot be read in full makes the repository unkeyed for the same reason: one larger
than 8 MiB, or holding more than 200 000 entries, is refused rather than truncated, because a set
read in part is a different set and it would take another repository's key. The same holds for a
file whose named section — a pnpm `packages`/`snapshots`, a Bundler `specs:` list, a lockfile entry —
holds a line at an indent the reducer cannot place and yields no entry, and for a line written where
such a section writes its keys that no package name and version can be read from. A pnpm
lockfileVersion 5.x lock (pnpm 5, 6 and 7) writes `/name/version` there, hanging the peer suffixes
on the version's own text, and a reader that took what follows the last `@` for a version would read
a package name no registry holds and key a fact for it out of a lock it read only in part. A file
that states its whole resolved set in a shape the reducer does not read at all is refused for the
same reason: a pnpm lock older than the `packages` map writes each dependency against a bare version
under a top-level `dependencies:`, `devDependencies:` or `optionalDependencies:` section of its own
and has no `packages` or `snapshots` map at all, so the empty set such a file would otherwise return
is not "no dependencies" but "read nothing", and every such repository would take one key. A section
with no line in it at all does state no dependencies, and is read as that.
`internal/imagecheck/reduce.go` is this table: `Reduce(name, bytes)` returns the facts; nothing,
which is the exemption above and the only way a file contributes nothing; or the reason the file
leaves the repository unkeyed.

**The bound is on bytes already in memory, so the caller bounds its own read**: C8b reads a key file
without loading an unbounded blob into the job first, and the refusal inside `Reduce` is the second
half of that, not the first. A file too large to read is refused by the caller's bound before this
table is reached.

### 2.4 Canonical form and the hash

The canonical form is text, one line per fact, with a version line first:

```
key v2
base:"<base_ref>@<base_digest>"
pkg:"\"<name>\" \"<version>\" \"<integrity>\""
tool:"<tool>@<version>"
```

- Every line after the version line is a prefix and then its whole fact written as **one quoted Go
  string literal** (`strconv.Quote`), so the `pkg:` line's own inner quotes come out escaped in the
  block above. The block is the bytes and not a sketch of them: unquoting a line's text after its
  prefix gives back the three literals §3.1 stores. `base:` and `tool:` write one field each, so
  their quoting is the plain one.
- A `pkg:` fact is the name, the version and the integrity or checksum the file states for that
  version's artifact (D618). **Each of the three is its own quoted literal, one space between
  them**, and the third is written even when the file states no integrity for that package, so a
  fact is always three literals: `"left-pad" "1.3.0" "sha512-aaa"`, and `"left-pad" "1.3.0" ""` for
  a package the file states no integrity for.
- **The three fields are encoded apart because a fact must be injective.** A name, a version and an
  integrity are all text a key file supplied, and any of them may hold the characters a joined
  spelling would use as separators — an `@` in an npm lockfile's own path-derived name, an
  integrity that is itself text. One string `name@version@integrity` makes two different triples one
  fact, and a key built over it would hand one dependency set another's image: npm's
  `"node_modules/s@1": {"version": "h", "resolved": "…"}` and `"node_modules/s": {"version": "1",
  "integrity": "h"}` are the same string `s@1@h`. Quoting each field fixes that, and the encoding
  is pinned by a test with those two shapes in it. Because of it **no fact can hold one of the
  fields of another**, which the spelling without per-field quoting could not say.
- A field never holds a space: a name, a version or an integrity with whitespace in it is a shape no
  ecosystem writes, and the file is refused (`UnkeyedUnreadable`). So the space between the three
  literals is a separator a reader can split on, and a fact read back gives back its three fields.
- The fact's fields are used as the file states them, after the ecosystem's own canonicalisation
  where it defines one (PyPI's PEP 503 normalisation, which folds case, `-`, `_` and `.`; the Go
  module path as written, which is case-sensitive). A control character in a name, a version or an
  integrity, a NUL in a name, or an `@` in a version is not a shape any ecosystem writes: the file
  is refused (`UnkeyedUnreadable`) rather than keyed by it.
- Each literal is a **quoted Go string literal** (`strconv.Quote`), which is the block's inner quotes,
  the ones a field wears; the fact is quoted a second time onto its line. A name, a version or an
  integrity is text a key file supplied, and a fact holding a newline would otherwise end its line
  and have the rest of it read as further facts: a package named `left-pad\npkg:"react"` would hash as
  a further fact and take its key — and so be served its image. Quoting escapes that newline, and
  every other control character, quote and backslash, so one fact is one line whatever it holds.
- Every line is **deduplicated** and the lines after the version line are sorted bytewise, so the
  order of files, of entries in a file and of JSON or YAML keys cannot matter.
- The bytes end with a single newline. The hash is the lower-case hex **SHA-256** of those bytes,
  computed over UTF-8 with no BOM.
- `key v2` is the reduction's version: a change to §2.3's rules, or to this encoding, starts a new
  key universe instead of silently reusing images built under the old rules. The version is the
  canonical form's first line, so the two universes cannot collide.

### 2.5 What the key ignores, and where it is computed

The key ignores: file order, comments, formatting (whitespace, indentation, key order), the
repository name, the workflow name, and build and test commands — the workflow's own `commands:` and
`image.build`, which are not dependency facts.

The key is computed **only on the operator's machine** (D622), by `fugaro image refresh` and
`fugaro project sync`, from a checkout at the repository's base branch head — the same tree
`imagecheck.KeyFiles` reads there. Never in Cloud Build and never in a job: a job runs with
installation credentials, and C2's resolver command is arbitrary code. The refresh passes the key
it computed to the build as an input, and the build's record step writes the value it was given
and never recomputes one of its own (D622): the key has to be the same fact on every machine, and
the build's own clone is not the tree the operator's checkout is.

The **local resolver** (the withdrawn plan task C2, D622) is the key's population input, as read
here: a read-only command declared in the project layer which prints the repositories to consider,
one `owner/name` to a line; it runs only on the operator's machine, its output is intersected with
`allow_repos`, and every dropped entry is warned (F26, T3: the resolver never widens access, and it
never writes the policy). It does **not** supply versions: versions come only from the repository's
own committed key files, since a resolver that asserted versions would be a second source of truth
(D617) and would have to run repository code. A repository whose key files do not reduce is
**unkeyed**: it keeps today's per-(repository, workflow) image and its own build record, `image ls`
does not list it (§7), and the refresh treats it as it does today.

### 2.6 The key's image name

A shared image needs a home no single repository owns. Today `RegistryRepoID(slug)` is one Artifact
Registry repository per repository, and `ImageName(registry, slug, workflow)` puts a repository's
derived image in it; a sibling repository's build account cannot push there and a sibling's job
account cannot pull from there. So C8 adds **one shared registry repository per installation**
(`internal/backend/gcp/names.go`, derived like the others: a hash of a fixed purpose word, so it can
never equal a repository's own) and names the image by its key alone:
`<registry>/<shared repo>/img-<first 12 hex of the key>`. The name carries no repository and no
workflow, which is the point: the same name is the same dependency set. The install's second
repository on a key adopts the first's image rather than building a second copy (§4).

## 3. The lock

### 3.1 Path and shape

The lock is one JSON object **under `builds/` in the runs bucket**, at `builds/image-lock.json`.
It is one object because it describes the installation, not a repository; the name cannot collide
with a build record's key, which is always `builds/<slug>/<workflow>/image.json` with the slug as a
directory.

```json
{
  "version": 1,
  "images": {
    "9f2c…": {
      "key": "9f2c…",
      "image": "europe-west1-docker.pkg.dev/project/fugaro-shared-4a1b2c3d4e5f/img-9f2c1a2b3c4d",
      "digest": "sha256:…",
      "base_ref": "ghcr.io/<owner>/fugaro-base:0.15.0",
      "base_digest": "sha256:…",
      "packages": ["\"left-pad\" \"1.3.0\" \"sha512-aaa\"", "\"react\" \"18.3.1\" \"sha512-bbb\""],
      "tools": ["node@22.11.0"],
      "built_at": "2026-10-29T09:12:04Z",
      "build_id": "…",
      "repositories": ["owner/repo-a", "owner/repo-b"],
      "jobs": {"owner/repo-a": "fugaro-repo-a-web", "owner/repo-b": "fugaro-repo-b-web"},
      "last_used_at": "2026-10-29T09:40:11Z"
    }
  }
}
```

`packages` and `tools` are the resolved set the image was built from, as §2.4's lines without their
`pkg:` and `tool:` prefixes — a package entry is the fact's three quoted literals, quotes included,
because that is the text the key hashed and the text §4 compares, and a decoded pair would put back
the joining the encoding exists to avoid. §4 decides a reuse from them, because a hash cannot be compared for
containment: an entry holding only its key could never be a superset of anything. §5 calls a
repository diverged when the entry it is on does not hold its set. A refresh stores the set it just
resolved, beside the key it hashed from it.

`repositories` and `jobs` are the same set seen twice: which repositories are on this image, and each
one's job name as it is at update time (the operator's machine reads the job, so it knows). They are
the answer to "is a job pointing at this image?" (§9) and to `image ls` (§7). `last_used_at` is the
last time a refresh placed a repository on the image (§9's age is measured from it), which keeps
§3.3's one-writer rule whole: a launch reads the lock (D619's warning) and writes nothing.

A job — one repository and one workflow — is on **at most one entry** of the whole lock. A job runs
one image, and a launch that asked which one it is would otherwise take whichever entry it met
first; so `ParseLock` refuses a lock that names one job on two entries, naming the job, the
repository and both keys, and the schema says the same in `jobs`. It is a rule about two entries at
once, which no schema validating one entry can state. **What it reads is the pair the entry states —
the repository its `jobs` map is keyed by, and the job name beside it —** since two repositories may
carry the same job name, and reading the name alone would refuse a lock that places one repository's
job on one image while another repository's job of that name is on a second. What the pair cannot
catch is one job **renamed** between two entries: a name is all the lock states of a job, so only the
writer that knows the rename can, and §3.3's refresh is that writer — C8b1b's job, and the reason the
rule is not claimed to be more than it is. The rule is what a fork keeps: when a choice
moves a repository's job to a new image (§5, §6), the entry it left no longer names that job, so the
job is on the new entry alone.

### 3.2 Strict decoding, and the schema

The lock decodes **strictly**: unknown fields and an unknown `version` are errors, which is the
opposite of `ParseRecord`'s deliberate tolerance. The reason is that this object is written by every
operator's machine and read by every fugaro version in use, so a tolerant reader is a destructive
one: an older fugaro that ignored a newer field and rewrote the object would drop it for everyone. A
strict reader stops and names the version instead. `schemas/image-lock.schema.json`, which C8 adds,
is the same shape for editors and for the schema checks the repository's other wire formats have.

### 3.3 Who writes it, and when

Only the operator's machine writes the lock: `fugaro image refresh` after a successful build and the
promotion of the image, and `fugaro image prune` when it removes an entry. Cloud Build never touches
it — the build's record step writes only `builds/<slug>/<workflow>/image.json` — and neither the
daily check job nor `fugaro image ls` writes anything.

The lock is written **after** a successful build, and an image already in the lock is
never rebuilt or overwritten in place. A key's entry, once present, keeps its digest; a change to a repository's
dependency set is a change of key, and a new key is a new entry (§5), which is what keeps T6(c)'s
blast radius local: the sibling repositories on the old key are untouched.

### 3.4 Concurrency: two refreshes on one key

Two operators' machines can refresh the same key at the same time. Two mechanisms, in this order:

1. A **claim** per key, `builds/image-lock.claims/<key>`, taken with `blobx.Create` (create-if-absent,
   atomic on GCS) before a build starts and released with a generation-matched delete. A second
   refresh that finds the claim adopts the image the first is building (waiting for its lock entry)
   instead of paying for a second build. A claim whose object is older than the build timeout is
   stale and may be replaced, so a crashed refresh does not block the key forever.
2. The lock write itself is **generation-matched where the object exists**: `blobx.ReplaceIf(ctx, key,
   data, gen, prev)` with the generation the refresh read. `ReplaceIf` cannot create the object — on
   GCS a generation of 0 is "no generation to match", and off GCS it answers `ErrConflict` for a
   missing one — so the **first** write is a create: `blobx.Create(ctx, key, data, contentType)`,
   which is create-if-absent and answers `blobx.ErrExists` when another refresh made it first. The two
   answers are told apart on purpose: `ErrExists` means the writer arrived at an empty lock, so
   nothing was lost — it re-reads and carries on with the object the other writer made; `ErrConflict`
   means the object it read has changed, so it re-reads, re-plans once, and a second conflict exits
   non-zero naming the two writers, leaving the operator to rerun.

   The per-key claim above only serialises refreshes of the *same* key; the create-or-replace pair is
   what makes two *different* keys' refreshes safe, since both write the one lock object: whichever
   creates it wins, and the other one — reading before or after — either creates and loses with
   `ErrExists` or replaces against the generation it read. Either way no entry is lost, and an entry
   is only ever added by its own key's refresh.

## 4. Superset lookup, and what it costs

A superset reuses an existing image **only when it holds the whole set at the same versions and the
same integrities**. Before building a key, the refresh looks for an existing lock entry that holds
the repository's entire dependency set with identical `pkg:` facts — the same names at the same
versions, resolved to the same artifacts, compared as the three quoted literals §2.4 writes, since
two different triples are two different strings there — and may hold more. The toolchain and the base image are
not superset-able: the base must be the identical ref and digest, and the `tool:` lines identical,
because an extra tool or a different base changes
what the image is, while an extra package only widens what is installed. When several entries
qualify, the one with the fewest extra `pkg:` lines wins; ties go to the most recent `built_at`, then
to the lower key hex, so the choice is deterministic and two machines pick the same image.

The cost is stated plainly because the owner accepted it (O6): the repository's image then contains
packages it does not depend on. That is a **wider supply-chain surface** — more code in the image,
more known vulnerabilities to track, more to update, and every reader of that image's registry reads
all of it. It also means the image's dependency layer was installed by another repository's
`image.setup` command (§2.2). The alternative — building one image per key — keeps each image to its
own set and pays a build per key; the design follows the owner's answer and reuses.

## 5. Divergence: propose, then confirm

A repository's key **diverges** when the refresh computes a set that the image the repository is on
does not hold. A divergence is **proposed and confirmed, never forked silently**: T6(c) was resolved
against the automatic fork the reconciliation had recommended.

The proposal names the facts and offers exactly three choices, spelled `fork`,
`adopt <repository>` and `pin`:

| Choice | What it does |
| --- | --- |
| `fork` | builds a new image for the new key; only this repository's job moves to it |
| `adopt <repository>` | puts this repository on the image `<repository>` is on, when that image is a superset of this repository's set at the same versions and integrities (§4); the choice names the repository so the operator sees whose image it is and what it carries |
| `pin` | stays pinned: keeps the image the repository is on, digest unchanged, and warns; the job is not touched and nothing is built |

The proposal prints the diff summary the decision needs — packages added, removed and their version
changes by count — and, for `adopt`, the candidates with their extra-package counts. Nothing is
built before an answer, and every choice updates **only this repository's job** (§6).

Interactive, it reads one of the three. `fugaro image refresh`
refuses to build when there is no terminal, or with `--json`: it prints the proposal (as JSON under
`--json`) and exits non-zero, unless `--on-divergence` names the choice —
`--on-divergence=fork`, `--on-divergence=adopt=<repository>`, `--on-divergence=pin`. The flag is the
only way to answer without a terminal, and like today's `--yes` (`internal/cli/image_refresh.go`) it
is **refused in a coding agent's session**. Exit codes: an unanswered proposal exits 1
(`ExitUserError`), a flag that names a repository the lock does not hold, or one whose image is not
a superset, exits 1 with that reason; a build or a job update that failed remotely exits 2
(`ExitRemoteError`).

`fugaro project sync` proposes too — the same facts, the same three choices — and **never builds**:
it prints the proposal and, interactively, the exact `fugaro image refresh --on-divergence=…` line to
run. Sync writes nothing about images; its own confirmation shape is D625's.

## 6. Each repository's own job: the image **and** the clone shape

Cloud Run cannot vary the image per execution (U4), so the digest lives in the job. After a build, an
adopt or a pin, the operator's machine updates **only the repository whose refresh this is**: read
the job, refuse anything that is not the owner's job by its labels, keep the shape, write, never
return an execution token (`internal/infra/checkjob.go`'s `WorkflowJobUpdate`). A sibling
repository's job is not read and not written, which is what makes the divergence local.

The update is **not the image alone**. §1's constraint says why: a source-less shared image is only
runnable by a job that clones at launch, and today only a `checkout: clone` workflow's job carries
the two variables that say so (`FUGARO_CHECKOUT=clone`, `FUGARO_REMOTE=<the repository's
credential-free clone URL>`, both written by `internal/infra/spec.go`). So C8's job update writes
three things — the image (the lock's digest for the repository's key), `FUGARO_CHECKOUT` and
`FUGARO_REMOTE` — and the workflow's shape joins the key universe only once both are on the job. Two
routes get there, and they differ in who owns the job's shape:

- **Route A — a third checkout kind in `fugaro.yaml`** (for example `checkout: shared`: "run a shared
  dependency-layer image and clone the source at launch"). `internal/config/image.go`'s
  `validateCheckout` refuses `image.tools` and `image.setup` on `checkout: clone` precisely because a
  clone workflow has no build — so a repository on a shared image cannot be declared `clone` today,
  and a kind of its own is what lets it declare the dependency-layer keys it needs while still
  cloning. `internal/infra/spec.go` then writes the two markers from the workflow (the same code
  that writes every other job env), `NeedsBuild` (`internal/infra/repo.go`) treats it as needing a
  shared build, `PlanWorkflowJob` accepts the shape, and the runner needs no change beyond the
  `FUGARO_CHECKOUT` value it already switches on. Files: `internal/config/{config.go,image.go,
  defaults.go}`, `schemas/fugaro.schema.json`, `internal/infra/{spec.go,repo.go,checkjob.go}`,
  `internal/runner/provision.go`, and the plugin's `fugaro.yaml` documentation.
- **Route B — the job's markers only, no new kind.** The workflow stays `checkout: baked` and the
  update writes the two markers onto the job. This is smaller, and its cost is exactly the drift §1
  found: the job's env then disagrees with `fugaro.yaml`, and the next `fugaro update` re-derives the
  env from the spec (`spec.go`), so the spec — not only the refresh — has to know the repository is
  on a shared key, which means a local-config value or a lock read at update time, and two places
  deciding what a job's shape is. Files: as C8b's list plus `internal/infra/spec.go`,
  `internal/runner/provision.go` (or `runner.go`, if the remote is instead derived in the runner, as
  `internal/runner/projectjob.go`'s `cloneURL` already derives a project job's).

**Route A is the design's recommendation** — one writer for the job's env, so the update and
`fugaro update` cannot drift — but the choice is the owner's: it is a user-visible `fugaro.yaml` key
against a smaller diff, and either route needs the same three things on the job. The C8 split's "prerequisites
the plan's C8 file list omits" carries the files, and the open list carries the question.

## 7. `fugaro image ls`

`fugaro image ls` reads the lock and prints one row per image; it makes no registry call:

| Column | Value |
| --- | --- |
| `IMAGE` | the image name, untagged (§2.6) |
| `HASH` | the first 12 hex of the key |
| `REPOSITORIES` | the repositories on it, comma-separated, from `repositories` |
| `LAST USE` | the date of `last_used_at`, `YYYY-MM-DD` in UTC |

A final line carries the count and the cap: `12 images (cap 20)`, with the warning's wording (§8)
when the count is over. Under `--json` the shape is
`{"version":1,"count":12,"cap":20,"over_cap":false,"images":[{"key":"…full hex…","image":"…","digest":"sha256:…","repositories":["owner/repo-a"],"jobs":{"owner/repo-a":"…"},"built_at":"…","last_used_at":"…"}]}`
— the full key, the digest and the timestamps the table rounds away. A repository that is unkeyed
(§2.5) is not in the lock and so not listed; its own image is where it is today, in its build record
and `fugaro image status`.

## 8. The image-count cap

`fugaro doctor` and `fugaro image ls` warn when the lock's image count passes a cap. The cap exists
for the question the owner asked when accepting D618 — how to stop trivial differences exploding the
number of images — and the answer's other two parts are the normalised key (§2) and the superset
(§4); this is the alarm, not the mechanism. It is a **warning only**: nothing is refused and nothing
is deleted for being over it.

The cap is **20** images per installation (ruled 2026-10-29). A fixed default is preferred over a
multiple of the enrolled-repository count because the count is not what grows — a repository's own
set does, across its workflows and its dependency churn — so a cap that scaled with the repositories
would never fire on the case it exists for. The warning names the count, the cap, and the two
remedies (`fugaro image ls`, `fugaro image prune`). The ruling keeps the paragraph above whole: 20 is
a warning and never a refusal, so no refresh, no build and no command is stopped by the count, and
nothing is deleted for being over it.

## 9. Clean-up: `fugaro image prune`

An entry is **eligible** when both hold:

1. **no job points at it** — its `jobs` is empty, which is what a `fork` leaves behind for the old
   key (a `pin` keeps the repository's job on it, so a pinned image is never eligible), and what a
   repository removed from the installation (`fugaro project sync` never un-enrols; D617/C7: a
   removed repository is dropped from the lock only) leaves behind too; and
2. its `last_used_at` is older than `images.retention_days` — no refresh has placed a repository on
   it for that long, and no run can be on it, since a run's image is fixed at launch and a run does
   not outlive its task timeout.

The clean-up never removes an image a job still points at — never one a job still points at,
whatever its age. `fugaro image prune` removes every eligible entry in one pass — the eligibility is
a rule, so no per-image judgement is asked of the operator — prints each one as it does, asks once
before the first delete (today's confirmation shape; `--yes` answers it unattended, never in a
coding agent's session), and has `--dry-run`, which prints the list and deletes nothing. The registry image is deleted first and the lock entry second, generation-matched by
the same rule as §3.4's write (`blobx.DeleteIf` for a delete), so a crash between the two leaves an
entry whose digest the registry no longer has, which
`doctor` reports and the next `prune` finishes.

**`images.retention_days`, default 30, is confirmed (ruled 2026-10-29).** The key belongs to the
**project layer** — the shared, published part of the operator's local config, beside `base_images`
(`internal/localcfg`) — and not to a repository's own `fugaro.yaml`: an entry is shared by several
repositories and its deletion removes a registry image some other repository may still point at, so
it is shared policy, and only the project layer sets shared policy. A machine-local override is
deliberately not read: it would let one operator's machine prune an installation-wide image on a
retention the other operators never agreed to. The value is validated where the layer is parsed: a
positive integer count of days, and nothing else.

Pruning also covers the per-repository images the migration leaves behind (§10): an image in a
repository's own registry repository whose build record shows no job on it and whose last build is
older than `images.retention_days` is removed the same way.

## 10. Migration from per-(repository, workflow) images

Nothing is rewritten at once; a repository moves when it is next refreshed.

1. Until a refresh, a repository is exactly as it is today: its own registry repository
   (`RegistryRepoID(slug)`), its own image (`ImageName`), its job on that image's digest, and its
   record at `builds/<slug>/<workflow>/image.json`.
2. The next `fugaro image refresh` computes the key (§2). With no entry for it and no superset, it
   builds once into the shared registry repository (§2.6) and moves that repository's job (§6). With
   an entry, or a superset, it adopts and builds nothing. The build is handed the key the refresh
   computed and its record step writes that value as it is (D622): the build never recomputes a key
   of its own, so the record's `key` is the operator's machine's fact whatever the build cloned. The repository stops building its own
   image; the old one stays where it is, pointed at by nothing, and §9's second clause removes it
   after `images.retention_days`.
3. The second repository on a key finds the first's entry and adopts it, so a key built by two
   repositories is one build, two jobs, one digest.
4. An unkeyed repository (§2.5) never moves.

`fugaro update --repo` keeps creating the per-repository registry repository, since a repository may
be unkeyed and an installation may be mid-migration; deleting it is an infra change and is not part
of this design. The release that ships C8 needs a `### For operators` entry naming the shared
registry repository and the two grants below, in the same PR (`docs/releases/README.md`).

## 11. The build's credential

**Unchanged.** The build still runs as the repository's build account
(`internal/backend/gcp`'s `BuildServiceAccountID`) and still clones with the repository's existing
credential — the GitHub App's read-only installation token, or the `bitbucket-token` secret — from
the repository's own base branch. Sharing introduces no new credential, mounts no new secret, and
stores nothing new: the shared image is built by a repository's own build, from that repository's own
checkout, and the record step writes the same record it writes today (with the key added, C8a).

Two IAM facts follow from §2.6 and are not credentials: the build account needs write on the shared
registry repository, and every enrolled repository's job account needs read on it. Both are
Terraform changes in `deploy/terraform/gcp` that the plan's C8 file list does not name, and they are
the one piece of C8 not covered by its own file list.

## 12. What is baked

The dependency layer, and only it: the toolchain the key names, the packages the key names, and the
base the key names — everything the warm-up and `mise install` produce. No repository's source, no
workflow's `commands:`, no repository secret. The source comes from the clone at launch (§6's clone
shape), and which commit that is comes from the run itself — the job's base branch and the task —
not from anything baked here: `FUGARO_COMMIT` is a build argument of the image build and no launch
reads it. This is what C3's layer order makes possible (the C8 split, below): the key-file layer is
the shared image's whole content, and the source layer that follows it is the per-run clone, not a
build step.

## C8 split and its dependencies

Three tasks, in the order below; each is independently testable and each has its own red step. The
test names are the plan's C8 list, distributed to the task that makes them pass.

### C8a. The key, the superset and the lock (`imagecheck`)

- **Files (9):** `internal/imagecheck/key.go` (new), `internal/imagecheck/key_test.go` (new),
  `internal/imagecheck/superset.go` (new), `internal/imagecheck/superset_test.go` (new),
  `internal/imagecheck/lock.go` (new), `internal/imagecheck/lock_test.go` (new),
  `internal/imagecheck/record.go` (`Record` gains `Key`), `internal/imagecheck/record_test.go`,
  `schemas/image-lock.schema.json` (new).
- **Behaviour:** `imagecheck.Key(resolved) string` over §2.4's canonical form;
  `imagecheck.Lock` with strict decoding (§3.2) and the generation-matched write (§3.4);
  `imagecheck.Superset(lock, resolved)` per §4. Pure: no cloud, no registry, no bucket —
  `t.TempDir()` and `mem://` at most.
- **Tests:** `TestKeyIdenticalForIdenticalDependencySets`, `TestKeyChangesWithAnyLockfile`,
  `TestKeyIgnoresOrderCommentsFormattingRepoNameAndCommands`, `TestKeyChangesWithVersionToolchainOrBase`,
  `TestSupersetReusesAnImageWithExtras`, `TestSupersetRequiresSameVersions`, `TestLockDecodesStrictly`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/imagecheck/ -run 'Key|Superset|Lock' -count=1`.
- **Red step:** `TestKeyIdenticalForIdenticalDependencySets` fails: there is no `imagecheck.Key`.
- **Depends on:** nothing beyond today's `imagecheck.KeyFiles`. **This is the piece to land first.**
- **Commit:** `imagecheck: the normalised dependency-set key, the superset lookup and the lock`
- **Reviewer:** standard.

### C8b. Refresh, proposal and the per-repository job update

- **Files (12):** `internal/cli/image_refresh.go`, `internal/cli/image_proposal.go` (new),
  `internal/cli/image_refresh_shared_test.go` (new), `internal/cli/image_proposal_test.go` (new),
  `internal/infra/checkjob.go` (§6: the image, the clone marker and the remote),
  `internal/infra/checkjob_test.go`, `internal/infra/spec.go` (route A's two markers),
  `internal/backend/gcp/names.go` (the shared registry repository's name),
  `internal/backend/gcp/names_test.go`, `internal/image/render.go` (the shared render input: the
  dependency layer alone), `internal/image/render_test.go`, `internal/runner/provision.go` (the
  checkout kind the runner clones on, route A).
- **Behaviour:** §2.5's key computation on the operator's machine, §4's superset lookup, §5's
  proposal and `--on-divergence`, §6's single-repository job update (the image, the clone marker and
  the remote), §3.3's write order and §3.4's create-or-replace rule.
- **Tests:** `TestRefreshBuildsOnceForSharedKey`, `TestRefreshForksOnChange`,
  `TestRefreshNeverOverwritesALockedDigest`, `TestRefreshUpdatesOnlyTheChangedRepositoryJob`,
  `TestDivergenceProposesAndWaits`, `TestDivergenceNonInteractiveRefusesWithoutFlag`,
  `TestDivergenceFlagNamesTheChoice`, `TestStayPinnedWarns`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ ./internal/infra/ ./internal/image/ ./internal/backend/gcp/ -run 'Refresh|Divergence|ProposesAndWaits|StayPinned|WorkflowJob|ImageRender' -count=1`.
- **Red step:** `TestRefreshBuildsOnceForSharedKey` fails: two repositories with one set build twice.
- **Depends on:** C8a and **C3** (see below).
- **OWNER-RUN:** a sandbox refresh across two repositories with one set and one with another: one
  build, two jobs on one digest, a proposal (not a build) on a dependency change, and each of fork,
  adopt and stay-pinned.
- **Reviewer:** opus + mandatory independent review (the registry layout and job definitions of every
  installation change).
- **Commit:** `image: repositories with the same resolved dependency set share one image`

### C8c. `image ls`, prune, the cap warning, the reference and the skill

- **Files (8):** `internal/cli/image_ls.go` (new), `internal/cli/image_ls_test.go` (new),
  `internal/cli/image_prune.go` (new), `internal/cli/image_prune_test.go` (new),
  `internal/cli/doctor.go`, `internal/cli/doctor_test.go`, `website/content/cli-reference.md`
  (regenerated by `go run ./internal/clidoc`), `plugin/skills/working/SKILL.md` (the `image`
  commands the skill names — the edit waits until the 0.13.0 freeze on `plugin/**` lifts).
- **Behaviour:** §7's table and `--json`, §8's warning in `doctor` and `image ls`, §9's prune with
  `--dry-run` and its two conditions, §10's migration clause for a repository's own old image.
- **Tests:** `TestImageLsListsHashesAndRepos`, `TestDoctorWarnsOverImageCap`,
  `TestPruneRemovesImagesUnusedForRetentionDays`, `TestPruneKeepsAnImageAJobPointsAt`, plus the CLI
  reference's own test after the regeneration.
- **Command:** `go run ./internal/clidoc`; then
  `GOFLAGS=-p=2 go test ./internal/cli/ ./internal/clidoc/ -run 'ImageLs|ImagePrune|DoctorWarnsOverImageCap|CliReference' -count=1`.
- **Red step:** `TestPruneKeepsAnImageAJobPointsAt` fails: there is no `fugaro image prune`.
- **Depends on:** C8a, C8b.
- **Commit:** `cli: fugaro image ls and image prune, and the image-count cap in doctor`
- **Reviewer:** standard.

### Prerequisites the plan's C8 file list omits

Found while writing this design; each one blocks C8b as the plan describes it. The owner may want
C8b's file list amended — and this design's §6 route chosen — before that task starts.

- **The clone shape (§6).** A job on a source-less shared image must carry `FUGARO_CHECKOUT` and
  `FUGARO_REMOTE`, which no baked workflow's job has today. Route A adds a `checkout:` kind:
  `internal/config/{config.go,image.go,defaults.go}`, `schemas/fugaro.schema.json`,
  `internal/infra/{spec.go,repo.go}`, the plugin's `fugaro.yaml` documentation; route B adds
  `internal/infra/spec.go` and `internal/runner/provision.go` (or `runner.go`) and a local-config way
  for the spec to know the repository is on a shared key. Without one of them the job update cannot
  work at all.
- **One shared registry repository per installation, and two grants (§2.6, §11).** Terraform in
  `deploy/terraform/gcp`: write for each repository's build account, read for each repository's job
  account. Today's per-repository registry repository is not reachable from a sibling's build or job.
- **SECURITY.md and its website copy.** The shared image bakes no source, and the note C6 would add
  describes the per-repository image that does, so the paragraph needs a sentence for the shared
  image: `SECURITY.md` and `website/content/security.md` (regenerated). Neither is in the plan's C8
  file list, and `SECURITY.md` carries the independent review `.claude/rules/credentials-service.md`
  requires.
- **A `### For operators` entry** in the release notes of the release that ships C8 (§10), naming the
  new registry repository and the grants.

### What C8 needs from C3 and C4

- **C3 (lockfiles before source in the derived image): C8 cannot ship without it.** The shared image
  *is* the dependency layer, and a shared image must hold no repository's source (§1, §12) — which is
  only expressible because C3 puts key files before the source layer, so the layer a sibling's job
  runs is the dependency layer and the source layer behind it is the per-run clone. Without
  C3 the first repository's source would sit in the layer every sibling's job runs, and one
  repository's registry readers would read another's source: the security property of D618's scope.
  The **smallest piece C8 needs** is C3's render ordering alone —
  `internal/image/render.go`'s key-files-before-source layout (and the template it renders) with
  `TestRenderPutsKeyFilesBeforeSource` green — not C3's whole task; C8's own `render.go` edit rebases
  on that ordering.
- **C4 (the image digest checked at launch): C8 can ship without it.** C4 adds a *warning*; C8's
  mechanism is the job update (§6). Shipping C8 first loses only the mismatch warning, and C4 then
  lands with the plan's `TestRunDigestComparesAgainstTheLockForItsKey` — the digest compared against the lock for the repository's key, rather than against the build record — which is one change to
  the comparison's source, not a rework of the warning. If C4 ships first as written, C8 owns that
  one change; the lock is C8a's, so either order works.

**Options for the owner** (this is the C3/C4 question the open list asks, stated as options rather
than decided in this section; the owner's ruling of 2026-10-29 is at the end of this file):

1. **C8a now, C8b/C8c after C3.** C8a is pure and needs nothing shipped; it can land beside C3's work.
   C8b waits for C3's ordering. C4's warning lands whenever it is convenient and takes its
   lock-aware form.
2. **C3 → C4 → C8a/b/c**, the plan's level order, one slice C at a time.
3. **C3 → C8a/b/c with C4 folded into C8b**, so the lock-aware launch warning is written by the task
   that owns the lock.

## Owner rulings

Ruled 2026-10-29, after this design's review:

- **The image-count cap value** (§8): **20** images per installation, warned by `doctor` and
  `fugaro image ls`, never enforced. It is a warning and never a refusal: nothing is stopped and
  nothing is deleted for being over it.
- **`images.retention_days`** (§9): **30** days, confirmed. The key is owned by the project layer,
  applied by `fugaro image prune`, never to an image a job still points at.
- **The C3/C4 question** (stated as options in the C8 split): both are in 0.14.0, so option 1 is the
  ruling — C8a lands beside C3, which touches none of C8a's files, and C8b waits for C3's
  key-files-before-source ordering, without which a shared image would hold the first repository's
  source. C4 is merged (#1315); as the C8 split says, the lock-aware form of its comparison
  (`TestRunDigestComparesAgainstTheLockForItsKey`) is one change the lock's owner makes, not a
  rework of the warning.

Still open:

- **How the job becomes clone-shaped (§6)**: route A, a third `checkout:` kind in `fugaro.yaml` (the
  design's recommendation: one writer for the job's env), or route B, the markers written onto the job
  alone (a smaller diff, and two places deciding what a job's shape is). C8b cannot work without one
  of them, and the choice changes its file list.
