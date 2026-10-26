# Registry cleanup

Artifact Registry cleanup deletes old image versions from `fugaro-base` and from each repository's registry, so the storage they cost stops growing. Every installation starts with it in **dry run**: the registry logs what it would delete and deletes nothing.

## Audit the dry run first

A day or more after the first images exist, read the log the dry run wrote for `fugaro-base` and for each repository's registry — check 17 of [gcp-live-checklist.md](../gcp-live-checklist.md) is the live form of this. It must never list a version tagged `latest`, or `dev-` in `fugaro-base`. If one appears, stop: the keep rules are wrong, and it is a bug to report.

## Turn it on

**Per installation:**

```bash
fugaro update --registry-cleanup on          # only fugaro-base's dry-run flag changes in the plan
```

**Per repository**, in each checkout:

```bash
fugaro update --repo                         # no flag: the repository copies the installation's setting
```

The repository step copies the installation's `registry_cleanup_dry_run` output into that repository's registry, so it takes no flag of its own. Both registries and `fugaro-base` end with the dry run off. The setting is recorded in the local config, so a later `fugaro setup` or `fugaro update` keeps it.

## What it deletes, and what it never does

Keep rules always win over delete rules: a version tagged `latest` (and `dev-` in `fugaro-base`) and the newest three are never deleted. What the delete rules reach is untagged versions older than 14 days and, in a repository's registry, versions tagged `candidate-` older than 2 days. Cost: none; it lowers registry storage.

## Off again

**Per installation:** `fugaro update --registry-cleanup dry-run` puts the dry run back, and `fugaro update --registry-cleanup off` removes the cleanup policies altogether. `fugaro update --forget` turns both off as part of a rollback ([offboarding.md](offboarding.md)).
