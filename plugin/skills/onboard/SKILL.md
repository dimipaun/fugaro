---
name: onboard
description: Onboard the current repository to Fugaro. Writes fugaro.yaml, including each workflow's image settings, from the repository's CI config and tool-version files; writes a repository Dockerfile only when image settings can't express what the build needs; and loops on fugaro validate --json and fugaro image build --local --json until both pass. Use when the user wants to set up Fugaro for a repository, or to fix a fugaro.yaml or a failing local image build.
---

# Onboard a repository to Fugaro

You are writing the repository's `fugaro.yaml`: how Fugaro builds and tests this repository inside a container, and what that container needs. Fugaro's engine never guesses these. You read the repository and decide.

You are done when every workflow passes two checks:
- `fugaro validate --json` prints `"valid": true`.
- `fugaro image build --local --json` prints a smoke result with `"passed": true`.

Ground rules:
- **Evidence.** Base every setting on evidence in the repository, and cite it in your summary with the file and line. Leave out settings you have no evidence for; Fugaro's defaults cover them.
- **Secrets.** Never write a secret value into any file, and never print one. Secrets are listed by name only.
- **No commits.** Don't commit or push. The user commits `fugaro.yaml`, and any `.fugaro/` files, through a normal pull request.
- **Existing files.** If `fugaro.yaml` already exists, you are fixing it. Keep the user's choices unless they are wrong, and say what you changed.
- **Scope.** Keep the file about this repository's build and test. Don't copy unrelated CI details such as deploy targets, release flags or mobile builds.

## 0. Preconditions

Run from the repository root (`git rev-parse --show-toplevel`). Check that `fugaro version` works and that Docker answers (`docker version`). If either fails, tell the user what to install and stop.

## 1. Start from the template

Read `fugaro config example`. It annotates every field. Write `fugaro.yaml` in the same shape, with only the fields you have evidence for.

## 2. Decide the workflows

A workflow is one buildable unit with its own image.

- **`web-node`:** `package.json` at the root with a lockfile (`yarn.lock`, `pnpm-lock.yaml`, `package-lock.json` or `npm-shrinkwrap.json`). Usually named `web`. A monorepo with one root lockfile (Yarn, pnpm or npm workspaces) is one workflow.
- **`server-jvm`:** `gradlew` or `settings.gradle(.kts)`. Usually named `server`. The `server-jvm` image is not published yet, so its image build fails with "has no published image yet". Record the workflow if the user wants it, tell them it can't run yet, and finish the web workflows.
- **Both:** two workflows.

Set the git settings:
- `git.provider` from `git remote get-url origin`: `github.com` means `github`, `bitbucket.org` means `bitbucket`.
- `git.base_branch` from the remote's default branch (`git symbolic-ref refs/remotes/origin/HEAD`), or the branch CI treats as main.

The image keeps the https form of the remote as its `origin`, even when your clone uses SSH, and runs add credentials to it. A host other than `github.com` or `bitbucket.org` needs `FUGARO_GIT_PROVIDER` at run time; tell the user.

## 3. Read the CI config for commands, reports and secrets

Look at `.github/workflows/*.yml`, `bitbucket-pipelines.yml`, `.gitlab-ci.yml`, `.circleci/config.yml` and `Jenkinsfile`, and at the `package.json` scripts or Makefile targets they call. For each workflow, set the following.

**`commands.build` and `commands.test`.** Use the exact invocations CI runs, through the repository's package manager (`yarn build`, `pnpm -r test`, `npm test`). Prefer the root script CI calls over rebuilding its parts. Skip lint-only, deploy, release, mobile (Android, iOS, Maestro, Detox) and emulator-based steps.

**Test environment.** The agent runs your commands with a filtered environment, so variables CI sets for its tests don't reach them. Put them inline in the command, for example `CI=true NODE_OPTIONS=--max-old-space-size=6144 yarn test`.

**`commands.reports`.** These are globs for the JUnit XML the tests write; without JUnit XML, Fugaro can't count tests. If the suite writes none, add a reporter in the command itself rather than editing the repository's config. For example, use `jest --ci --reporters=default --reporters=jest-junit` with `JEST_JUNIT_OUTPUT_DIR=reports`, or `vitest run --reporter=default --reporter=junit --outputFile=reports/junit.xml`. Tell the user if that needs a dev dependency the repository lacks.

**`commands.rerun_failed`.** Set it only when you are sure how the test runner selects single tests by the IDs in its JUnit report. For Gradle: `command: ./gradlew test`, `each: "--tests {id}"`. Otherwise leave it out.

**`secrets`.** Declare the variables that hold credentials the build or test steps read, such as a private registry token (`NPM_TOKEN`, `NODE_AUTH_TOKEN`) referenced from `.npmrc` or `.yarnrc.yml`. Give each a lower-case logical `name` (`npm-token`) and its `env` variable. Deploy credentials are not needed.

**Docker.** If the tests need Docker (docker-compose, Testcontainers, `services:` containers, `docker run`), stop and tell the user: Fugaro's Cloud Run backend can't run Docker. Offer a test command that covers the suites that don't need it, or waiting for the Docker-capable backend.

## 4. Work out the image

The derived image is the base image plus three things: the repository's checkout at `base_branch`, its installed dependencies, and whatever `image:` adds.

Fugaro detects the package manager itself, from `packageManager` in `package.json` or else the lockfile, and installs dependencies with one of:
- `yarn install --immutable` for Yarn 2 and later
- `yarn install --frozen-lockfile` for Yarn 1
- `pnpm fetch` followed by an offline `pnpm install --frozen-lockfile`
- `npm ci`

You don't configure that. If `fugaro validate` says `packageManager` and the lockfile disagree, tell the user; don't delete either.

Map what you find to `image:`:

| Evidence in the repository | Setting |
|---|---|
| `.nvmrc`, `.node-version`, `nodejs` in `.tool-versions`, `node-version:` of `actions/setup-node`, a CI image such as `node:24.19.0`, or `engines.node` | `image.node`: the exact version when the repository pins one (`"24.19.0"`), otherwise the major (`"24"`). Quote it. Leave it out if nothing pins a version. |
| `apt-get install` lines in CI | `image.apt`: the package names, dropping those the base already has (`ca-certificates curl git procps sudo tini xz-utils zstd`). |
| `playwright install --with-deps`, or CI running in a `mcr.microsoft.com/playwright` image | `image.setup`: install the browsers the tests use through the repository's own Playwright, so the version matches the lockfile. Use `npx playwright install --with-deps chromium`, `pnpm exec playwright install --with-deps chromium`, or `yarn playwright install --with-deps chromium`; in a Yarn workspace, use `yarn workspace <workspace> playwright install --with-deps chromium`. |
| Other steps CI runs after installing dependencies and before building, such as code generation | `image.setup`, one step each, only if the build can't run without them. |

How `setup` steps run:
- They run after the dependency install, as the non-root `fugaro` user, in `/work/repo`.
- `sudo` works during these steps and only then. That is what `--with-deps` needs.
- Browsers and other downloads land in `fugaro`'s home, so they are there at run time.
- Each step is one line: no newlines, no `<<`, no trailing backslash. Chain commands with `&&`, or call a script in the repository.

### When to write a Dockerfile instead

Write one only when `image:` can't express the need: a toolchain that isn't a Node version or an apt package, a vendor installer, or build arguments the template doesn't have. Then:

1. Run `fugaro image render --workflow <name> > .fugaro/<name>.Dockerfile`.
2. In `fugaro.yaml`, remove the workflow's `image:` block and set `dockerfile: .fugaro/<name>.Dockerfile`. The two can't both be set.
3. Edit the Dockerfile, keeping the contract `fugaro validate` checks:
   - The final stage starts `FROM ${FUGARO_BASE}`.
   - The checkout is cloned into `/work/repo`.
   - `finalize-checkout` stays after every step that touches git config.
   - The image ends as `USER fugaro` in `WORKDIR /work/repo`.
   - There is no `ENTRYPOINT`, and no `VOLUME` under `/work`.

The build context holds only the repository bundle, so use files from `/work/repo` after the clone step rather than `COPY`. `ENV` lines don't reach the agent unless Fugaro allowlists them, so put variables the commands need into the commands.

## 5. Resources

The defaults are 4 CPUs and 8Gi for `web-node`, and 8 CPUs and 32Gi for `server-jvm`. Cloud Run allows at most 16Gi with 4 CPUs, and 32Gi with 8.

Match what the repo's CI gives its build and test steps today. Read the CI machine size: Bitbucket `size: 1x/2x/4x/8x` is 4/8/16/32 GB, and GitHub-hosted `ubuntu-latest` has 16 GB for public repositories and 8 GB for private ones. Use that memory, rounded up to a Cloud Run size, plus about 1Gi for the agent itself, which runs in the same container.
- If CI raises the Node heap with `--max-old-space-size=<MB>` and the test runner uses parallel workers, check workers × heap + 2Gi against that figure.
- When the estimate exceeds the CI memory, first look for how CI copes (`--maxWorkers=1`, a nightly-only suite) and mirror it. Raise `resources` only if CI really has more memory. Say which you chose and why.

## 6. Validate until clean

Run `fugaro validate --json`. It prints `{"valid": …, "problems": [{"path": …, "message": …}]}`. Fix each problem at its `path` and run it again until `valid` is true. Don't silence a problem by deleting something the build needs.

## 7. Build the image until the smoke test passes

Run `fugaro image build --local --json`, adding `--workflow <name>` when there are several workflows.

Docker's build log goes to stderr. The JSON on stdout has `image`, `dockerfile`, `commit`, `origin` and `error`, plus `smoke.checks[]`, each with a `name`, `ok` and `detail`.

Before reading failures, know three things:
- A development build of `fugaro` has no published base image, and says so. Run `fugaro image build --local --json --base <image>` with an image the user built (`images/build-base.sh web-node` in the Fugaro repository).
- The image bakes the committed `HEAD`. Your uncommitted `fugaro.yaml` is read from the working tree, but uncommitted changes to `package.json` or lockfiles are not in the image.
- The first build on Apple Silicon runs under emulation, because Cloud Run is `linux/amd64`, and can take a long time. Tell the user rather than giving up.

When it fails, read the `error` and the end of the build log, then fix `fugaro.yaml`:

| Failure | Likely fix |
|---|---|
| apt: "Unable to locate package" | Fix the name in `image.apt`. The base is Ubuntu 24.04. |
| The dependency install fails with 401 or 403 | A registry token is missing. Declare it in `secrets` and ask the user to export the variable before rerunning. `fugaro` passes declared secrets to the build without storing them. |
| `--immutable` or `--frozen-lockfile` refuses a lockfile change | The lockfile is out of date in the repository. Tell the user. |
| A `setup` step fails | Fix the step. To debug, run it by hand in the image: `docker run --rm -it <image> bash`. |
| The origin is missing or not https | The clone needs an `origin` remote on the provider host. Tell the user. |
| Smoke `node` fails | `image.node` doesn't match what was installed. |
| Smoke `verify-build` fails | `commands.build` is wrong for the image, or needs a tool, variable or secret. |
| Smoke `origin`, `git-credentials` or `home-credentials` fails | Something wrote credentials into the image, or changed the origin. Remove that step. |
| Smoke `user`, `init` or `no-sudo` fails | The repository Dockerfile changed `USER`, `ENTRYPOINT` or the sudo rules. Restore the contract. |
| Smoke `checkout` fails | `/work/repo` is not at the commit that was built. Remove the `setup` step or Dockerfile line that checks out, resets or pulls. |
| Smoke `claude`, `no-setuid` or `sudoers` fails | The repository Dockerfile removed Claude Code, added setuid binaries or left sudo rules behind. Restore the contract. |

If the same failure survives three different fixes, stop and ask the user.

## 8. Hand off

Tell the user, briefly:
- the workflows, their commands and report globs, each with the file it came from
- the image settings with the evidence for each, and anything you couldn't express
- the secrets to create, by logical name, and where each is used. `fugaro init --repo` binds them to Secret Manager later.
- that they should commit `fugaro.yaml`, and `.fugaro/*.Dockerfile` if you wrote one, in a pull request
