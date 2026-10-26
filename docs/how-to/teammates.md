# Teammates: no setup

A launcher or an operator in a checkout of an onboarded repository can run `fugaro run`, `fugaro runs ls`, `fugaro diagnose`, `fugaro logs` and `fugaro watch` on a fresh machine with no local config and no `fugaro setup`. Design: [design/shared-config.md](../design/shared-config.md).

## The shared file (**per installation**)

An operator publishes the project's config to the runs bucket: `fugaro setup`, `fugaro update` and `fugaro update --repo` each do it after they write the local config, and `fugaro update --publish-config` does it and stops.

It is `fugaro/config.yaml` in `gs://fugaro-runs-<gcp-project>`. It holds the project config except what is owner-only, personal or derived, and it never holds a secret, the `terraform:` section, `user:`, `endpoints:`, `providers:`, `project_secrets:` or a repository's `base_branch`. Launchers read the runs bucket, and write only `runs/` and `tokens/`; only operators write the shared file. A publish merges into what is published, so a machine that lacks another's repositories does not erase them — to remove an entry, edit the object by hand.

## The `gcp_project:` line (**per repository**)

The checkout's `fugaro.yaml` carries `gcp_project: <id>` next to `project:`. It is the trust anchor: the GCP project is never guessed. Without it a command refuses and says which line to add, and `fugaro update --anchor`, run in the checkout, writes it.

## A local config on another machine (**per installation**)

An owner or operator who wants the whole local config on a second machine — for `fugaro update`, `fugaro budget` and the other owner commands — runs it there, in their own terminal:

```bash
fugaro update --gcp-project <gcp-project> --region <region>
```

On a machine with no config for the installation it takes the project's name from the installation, writes the local config from its outputs (the launchers, operators and budget backend included) and applies nothing; it then names the roles you may lack and the one command an owner runs to grant them. `fugaro setup` is never that command: it only starts fresh, and it refuses a GCP project that already holds an installation.

## The cache

The first command caches the validated shared config under `$XDG_CACHE_HOME/fugaro/shared-config/<project>.json`, else under `~/.cache`, and uses it as is for 24 hours; after that the bucket is read again. If the bucket cannot be reached at all, a cache up to 7 days old is used with a warning that says how old it is, and after that the command fails. A refused access never falls back to the cache.

## When it refuses

Every refusal names the field, and none is repaired. `fugaro doctor` prints a `shared-config` line when the config came from the shared file, or a `shared-config-differs` line when your local config and the published one differ, naming the fields.
