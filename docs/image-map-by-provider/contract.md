# The repository-to-image map's shape (T0)

Task **T0** of [implementation-plan.md](implementation-plan.md). What it states is **D3370** (the key
is `provider/owner/name`) and **D3371** (the member is `repo_images` everywhere), with **D3372** and
**R2** behind the read of the old member, **R10** behind the provider's shape, and **R11** behind
what a message calls the map ([reconciliation.md](reconciliation.md)). The Go is
`internal/projectpolicy/repoimages.go` and `internal/catalog/select.go`.

This is the shape every other task compiles against. **T0b**, **S1**, **R1**, **R2a**, **R2b**,
**R2c**, **R3**, **S2**, **S3** and **X1** are written against it; a side that finds this document
wrong or incomplete raises a change request
([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than working around
it.

**What T0 does not state**, and which task does: `repoImageKey` and the migration (**U1a**), the
local config's field and its effective reader (**T0b**), the shared config's refusal (**T0c**), the
policy stage that publishes the member (**U1b**), and the step that retires `image_map` (**U1c**).

## The member

| What | Where | Rule |
| --- | --- | --- |
| the map on the node | `Policy.RepoImages`, json `repo_images` | `projectpolicy.RepoImages`, a `map[string]string`: the key is `projectpolicy.Canonical(provider, repo)` ("github/acme/app"), the value a catalog entry's name (`config.ValidCatalogName`) |
| the map's keys on the wire | under `escKey(key)` | RTDB refuses a key carrying "/", and a canonical name carries two; `MarshalJSON`/`UnmarshalJSON` are the escape, exactly as `clone_urls` and `model_prices` hold theirs, so no writer has to remember it |
| the old member | `Policy.ImageMap`, json `image_map` | read as it is read today until `update` rewrites it, and removed by the step after the credentials service (D3372, D3376 as amended) |

`repo_images` is a member of its own and not `image_map` with new keys: a key that meant `owner/name`
must not start meaning `provider/owner/name` under the same name, because an older reader would read
a provider as an owner and map the wrong repository. It lists `repo_images` in `UnknownKeys` and
reads no map instead (R4). The node's version does not move (R3).

## `ValidRepoImages`: the write is held to `config.Providers`, the read only to the shape

`ValidRepoImages(m map[string]string) error` holds each key to two halves:

- **the provider**: `task.ValidProvider`'s **shape**, and deliberately **not** `config.Providers`.
  This is R10 and it is a wire-format rule, not tidiness: `credsvc` and every job image re-read the
  node while runs are in flight, so a release that adds a provider (GitLab, B3) must not make an
  older reader refuse the node. A key at a provider this release does not serve is **kept**, and the
  readers that enumerate keys skip it because it serves nothing (R9);
- **the path**: `task.CanonicalRepo`'s own answer, unchanged — the spelling every other key of this
  tree uses.

So an old nested key such as `acme/team/app` decodes, reads as the provider `acme`, and serves
nothing. An old-form key such as `acme/app` is refused, because its path is not canonical, and the
refusal names the fix (`fugaro image map set <provider>/<owner>/<name> <entry>`). **Refusing the old
form is the writers' job** (U1a, S2, S3 refuse it against `config.Providers`); a reader refuses only
what no lookup could reach.

`RepoImages.UnmarshalJSON` refuses one repository named twice — under one key written twice or under
the escaped and the literal spelling — as `CloneURLs` does, because the image a repository's jobs run
would otherwise depend on the order the block's keys are in.

## The node's effective map

`func (p *Policy) EffectiveRepoImages() (images map[string]string, fromRepoImages bool)` is R2 written
once. `repo_images` wins over `image_map`, **per member and never per key**: a node carrying the new
member answers it whatever the old one holds, and a node carrying only the old one answers that, read
as it is read today. `fromRepoImages` says which half the answer is — true for provider-qualified
keys, false for bare names. A nil policy and a node with neither member answer `(nil, false)`.

The rule is one function because three callers must not disagree about which member a node serves:
the credentials service (**S1**, which reads no local config, F25), `localcfg`'s effective reader
(**T0b**, which builds on this), and the launch and update paths.

## The job's lookup

`catalog.ImageRequest` gains three fields beside today's `ImageMap`:

| Field | What it is |
| --- | --- |
| `Provider` | the provider kind of `Canonical`'s repository ("github", "bitbucket"), from the repository's local-config entry or the job's own input (F31) |
| `RepoImages` | the map keyed by `provider/owner/name` |
| `LegacyMap` | the map keyed by bare repository name |

A caller fills **exactly one** of `RepoImages` and `LegacyMap`, from `EffectiveRepoImages`.
`mappedEntry` reads `RepoImages[Canonical(Provider, Canonical)]` when `RepoImages` is set, and the
legacy map by bare name otherwise. The two keyspaces are told apart by which field is set and never
by the shape of a key: a lookup that guessed would map a repository nobody mapped.

`ImageMap` stays and is read **only** where neither new field is set, so today's callers keep working
until X1 has moved them all; X1 then removes the field.

`UnknownMappedEntryError`'s sentence names the map as **"the repository-to-image map
(`repo_images`)"**, whatever member the project still holds — that is what R11 pins, so no message
says "repo images" (which means D1900's per-repository image rows) and none says `image_map`.
