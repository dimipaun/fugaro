# Fugaro — `fugaro setup` and `fugaro update`

Oct 20, 2026 · @Dimi

## The brief

This is too complicated: `fugaro init --name <project> --gcp-project <id> --create-project --link-billing <account> --firebase <id> --region <region>`.

Why specify both `--gcp-project <id>` and `--firebase <id>`? If missing, these should be asked interactively, with decent defaults (e.g. `--firebase` should default to `fugaro-<project>`, region to the one we use).

Also, since we are logged in in `gcloud`, can we get a list of possible billing accounts to choose from? I know firebase does it.

I don't like we use 'init' for both creation and update/refresh tbh.

## The answers, the same day (2026-10-20)

### 1. The commands

`fugaro setup` = the interactive first-time wizard (project, billing, Firebase, installation, then the first repository).

`fugaro update` = re-converge after a CLI upgrade (what a rerun of `init` and `init --repo` do today, non-interactive friendly).

`init` is removed. No backward compatibility is needed: only the owner's two installations exist, and they migrate by re-running.

### 2. One project, one id

ONE project id for Google Cloud and Firebase by default, default `fugaro-<project name>`. A separate Firebase project is possible only as an advanced flag (`--firebase-project`).
