# The spec: an image's `ENV` variables never reach a run

**Status: the issue text, verbatim, with this line added; it states no decision of its own.**
Issue **#1755**, "runs: an image's ENV variables never reach a run (allowlist); pass them through the
secrets denylist, or add a plain env map", opened by **@paulkore** on 2026-11-05, labelled
`enhancement`, and **still open**. It is reproduced below **byte for byte** — headings, the fenced
Dockerfile block, the transcript excerpt and the checklist box included — and nothing in this file is
the design. The design is [README.md](README.md), [reconciliation.md](reconciliation.md) and
[implementation-plan.md](implementation-plan.md); where one of them and this file disagree about what
the issue *says*, this file wins, because this file is a copy of the issue and they are readings of
it.

---

### The problem

The agent's environment, and so every command a run executes (`fugaro verify build`/`test`, the agent's own Bash calls), is built from an allowlist (`internal/agent/env.go`'s `baseEnv`: `PATH`, `HOME`, `JAVA_HOME`, locale, temp and cache variables, proxies and CA bundles, and four base-image variables), plus the auth variables and the workflow's declared secrets. Every other variable the job's image sets with `ENV` is silently dropped.

For a project catalog image that is surprising, and it broke our first run. The image is operator-owned (the catalog lives in the published policy, D1140) and is exactly where a team configures its toolchain. Ours sets, among others:

```dockerfile
ENV NEXUS_BASEURL="https://<private maven repository>"
ENV CHROME_BIN=/usr/bin/google-chrome-stable
ENV DOCKER_REGISTRY_BASE="<registry>"
```

and its `~/.m2/settings.xml` (copied from the team's CI settings) names the mirror as `${env.NEXUS_BASEURL}/repository/maven-public/`. In a run that variable is empty, so verify failed before reaching the repository:

```
Non-resolvable parent POM … Could not transfer artifact … from/to <mirror-id> (${env.NEXUS_BASEURL}/repository/maven-public/):
Cannot access ${env.NEXUS_BASEURL}/repository/maven-public/ with type default using the available connector factories
```

Nothing warned that the `ENV` lines are no-ops for runs; it took a failed run and a transcript search to find. The only hint in the docs is a clause in `docs/base-image.md` ("The agent's own environment is an allowlist"). Our build CLI also reads `NEXUS_BASEURL` (`require_var`) in its artifact-lookup helpers, so its validation of some repositories fails the same way. The workarounds are to hard-code values into files, or to store a public URL as a workflow "secret", one copy per repository.

### What you would like

Any of these, in order of preference; (3) helps on its own:

1. **Pass an image's `ENV` variables to the agent's environment, minus a denylist.** Fugaro already has the right denylist: the names a workflow secret's `env` may not take (`FUGARO_`, `ANTHROPIC_`, `CLAUDE_CODE_`, `GIT_`, `LD_`, `DYLD_`, `PYTHON`, `BASH`, `LC_`, `DOCKER_`, `BUILDKIT_`, `CLOUDSDK_`, `CLOUD_RUN_`, `VERTEX_REGION`, …, as in `schemas/fugaro.schema.json`). At least for catalog images, whose author is the operator; per-repository images could follow, since SECURITY.md already assumes the repository is trusted. Variables Fugaro sets itself (routing, auth, `FUGARO_*`) are applied last, as today, so nothing an image sets can override them.
2. **A plain, non-secret `env:` map on a workflow in `fugaro.yaml`** (and on a project-layer profile), checked against the same denylist, for values like a repository URL that are configuration, not secrets.
3. **Say so.** `fugaro image build`, `fugaro doctor` or `fugaro update --repo` list the variables a job's image declares that will not reach runs, and `docs/base-image.md` states the rule plainly next to "Bring your own".

### Help

- [ ] I could help build, review or test this

