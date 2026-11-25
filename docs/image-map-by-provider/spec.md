# Fugaro Image Map Keyed by Provider Spec (lift L11)

Owner: Dimi Paun
Status: approved by the owner, 2026-11-25

Code claims are checked against `origin/main` at `691ece010`. They cite a path and a symbol, never a line.

## Purpose

The project's repo-to-image map (`image_map`, D1906) is keyed by `owner/name` with no provider (`projectpolicy.ImageMap`, `projectpolicy.ValidImageMap`). One project can hold a GitHub `acme/app` and a Bitbucket `acme/app`: `task.Slug` hashes the provider in for exactly that reason. The map cannot tell them apart, so both run the same image, and neither can be mapped alone (audit RC51, report §5).

L11 keys the map by `provider/owner/name`, the form `projectpolicy.Canonical` already produces and the allow-list (`allow_repos`, `projectpolicy.MatchRepo`) already uses.

## Guiding principles

- **One spelling of a repository's identity.** The map uses the allow-list's form, written by one function (`projectpolicy.Canonical`). No new format.
- **`fugaro update` is the whole upgrade.** `update` rewrites the map in place: in the local config and on the published node. No operator step.
- **A field never changes meaning.** A key that meant `owner/name` does not start meaning `provider/owner/name` under the same name (Q2).
- **Safe with runs in flight.** No runner reads the map (verified: `internal/runner` has no reader). Its readers are the CLI at launch and in `update`, doctor, the image commands, and the credentials service's build-token handler.

## Scope

In scope:
- the map's key, in the local config (`localcfg.Config.ImageMap`) and on the policy node (`projectpolicy.Policy.ImageMap`);
- its validator and its escape (`escKey`/`unescKey` already handle a key with `/`);
- every reader;
- `fugaro image map set`;
- the migration in `update`;
- the JSON outputs that print the map: `image survey --json` (`imageSurveySetup.ImageMap`, `schemas/image-survey.schema.json`) and `project policy show --json` (`showPolicy.ImageMap`);
- the `images` and `setup` skills, and the website's CLI reference.

Out of scope:
- the catalog's own keys (entry names have no provider);
- `bake_repos`, `image_services` and `clone_urls`;
- the per-repository image rows (D1900 keeps them).

## The key

- **Form:** `<provider>/<owner>/<name>`, nested groups allowed, from `projectpolicy.Canonical(provider, repo)`. For example, `github/acme/app` and `bitbucket/acme/app` are two keys.
- **Validator:** `ValidImageMap` requires each key to be `projectpolicy.Canonical`'s output: a valid provider (`task.ValidProvider`), then a canonical repository path. A key in the old form is refused with the fix (Q3).
- **On the node:** stored under `escKey(key)`, as today. `UnmarshalJSON` keeps its rule that one repository may be mapped once.

## Readers

Each site that builds a `catalog.ImageRequest` passes the provider. `ImageRequest` gains `Provider`, and `mappedEntry` looks up `projectpolicy.Canonical(req.Provider, req.Canonical)`. The sites are:
- `run.go` (launch);
- `infra/spec.go`, `repoCtx.imageRequest` (`update`'s job images);
- `init_policy_migrate.go`;
- `image_build_catalog.go`;
- `image_catalog.go` (two sites);
- `repos_view.go` (two sites);
- `doctor_layer.go`.

The provider comes from the repository's local-config entry (`localcfg` `repos.<name>.provider`, the source `canonicalRepoArg` already uses) or the job's own input (`infra` spec `Provider`). A site that cannot name a provider is a defect, not a fallback to the old lookup. The reconciliation lists each site's source.

**`credsvc.buildEntryRepos`:** the request already carries `Provider`. The entry's set becomes the keys whose provider is the request's provider. Its existing "the other provider holds it" refusal then reads straight off the key.

## `fugaro image map set <repo> <entry>`

`<repo>` is read the way `canonicalRepoArg` reads an allow-list entry:
- `owner/name` takes its provider from the local config, and is refused when the config does not list it;
- `provider/owner/name` is taken as written;
- a path resolves through its checkout, as today.

The line it prints names the key as written: `github/acme/app runs the catalog entry go from now on …`.

## Migration, inside `update`

`update` rewrites the map once, in the stage that publishes the policy (`init_policy_migrate.go`), and in the local config it publishes from. Each old key `owner/name` becomes:
- **at exactly one provider among the enrolled repositories** (the local config's list plus published admits, as L5's D3350 counts them): the key under that provider;
- **at two providers:** one key per provider, with the same entry. Both keep the image they run today;
- **at none:** dropped, with one line naming it (`image map: acme/old dropped: no enrolled repository has that name`). Today it reaches no job either.

The migration is idempotent: a map already in the new form is left byte for byte. It is one brew-style line in the policy stage, under the run's existing confirmation, and asks nothing. Order: the build-token handler is the credentials service, which `update` redeploys. The policy stage writes the new map before any image build of the same `update`, and the stage order test pins it.

## Old CLIs

Unknown top-level members of the node are tolerated (`knownTopLevelKeys`, P8). So an older reader of a node that carries the new member (Q2) sees no map: it reports none, and its build token is refused by name. It never sees a wrong mapping. L1's stamp fences every CLI from 0.29.0 on. CLIs before L1 keep the gap the owner accepted for L8 (L1's Q2).

## Acceptance criteria

1. A project with a GitHub and a Bitbucket `acme/app` maps each to a different entry, and each job runs its own entry (launch, `update`'s job images, doctor and `repos view` agree).
2. `update` on a node and a local config holding `acme/app: go` writes the new key under the enrolled provider, under both providers when both are enrolled, and drops an unenrolled name with its line. A second `update` changes nothing.
3. `image map set acme/app go` writes `github/acme/app`. An unlisted `owner/name` is refused, naming `provider/owner/name`.
4. The build-token handler refuses a repository mapped only at the other provider, by name.
5. An old-form key, after migration, is refused by `ValidImageMap` with the fix. A key with an invalid provider is refused.
6. `image survey --json` and `project policy show --json` print the new member (Q2). Their schemas and `plugin/` tests pass.
7. `TestUpdateLeavesARunInFlightRunning` passes, and an installation at the previous release converges.
8. All tests are hermetic.

## Non-goals

- No provider in catalog entry names.
- No change to `task.Slug`.
- No change to the allow-list's form.

## Open questions for the owner

- **Q1. Key form: `provider/owner/name` (flat, the allow-list's form) or a nested map per provider?** Recommend flat. It is `projectpolicy.Canonical`'s output and already the allow-list's. One spelling means one parser and one escape. A nested map would be a second shape for the same identity.
- **Q2. The member's name: keep `image_map` with new keys, or a new name (`repo_images`) everywhere (node, local config, `--json`)?** Recommend a new name. Under "a field never changes meaning", a key set that means something else gets a new name. An older reader then sees no map and refuses loudly, instead of mismatching. The local config's key renames with it, so no spelling means two things. The cost: one rename across the schemas, skills and the website reference.
- **Q3. After migration, is an old-form key refused, or still read as "every provider"?** Recommend refused, with the fix (`image map set provider/owner/name`). `update` has already rewritten every map it can reach, so an old key is a hand edit or an un-updated machine. Reading it as every provider would keep RC51's collision alive.
- **Q4. A name at two providers: migrate to both keys, or refuse and ask the owner to choose?** Recommend both. That is the image each runs today, so nothing changes under the user, and `image map set` splits them later. Refusing would make `update` stop on a choice, which "Fugaro asks for nothing" forbids.
- **Q5. Ship in the release after L1, as L8 does?** Recommend yes. The stamp then fences every 0.29.0+ CLI from reading the new member as no map.
