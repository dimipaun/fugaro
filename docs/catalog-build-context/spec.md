# The spec: a repository build context for catalog `dockerfile` entries

**Status: the issue text, verbatim, with this line added; it states no decision of its own.**
Issue **#1738**, "catalog: a repository build context for dockerfile entries", opened by **the
issue's reporter** — a Fugaro operator running a project image catalog on their own installation,
the role the issue's own "The problem" section describes ("a project image catalog entry built from a
repository's Dockerfile", "the Dockerfiles teams already have", "our first run") — and **still open**.
No `@handle` and no name is recorded here, by this repository's rule that no real name or handle of a
person appears in `docs/`. It is reproduced below **byte for byte** — headings, the fenced Dockerfile
block and the checklist box included — and nothing in this file is the design. The design is
[README.md](README.md), [reconciliation.md](reconciliation.md) and
[implementation-plan.md](implementation-plan.md); where one of them and this file disagree about what
the issue *says*, this file wins, because this file is a copy of the issue and they are readings of
it.

---

### The problem

A project image catalog entry built from a repository's Dockerfile (`source: { kind: dockerfile, repo, path, branch }`) is built with a context that holds only the Dockerfile: the catalog build copies that one file into the project's bucket and runs `docker build … /workspace/context` on a folder containing nothing else (`internal/backend/gcp/catalog.go`: the catalog build "never clones a repository into the build context"). So every `COPY` or `ADD` of a file from the repository fails.

That rules out the Dockerfiles teams already have. A typical CI image is built from the repository root as context, for example `docker build -f ci/Dockerfile-java .` in a Bitbucket Pipelines or GitHub Actions step, and copies the repository's own files into the image:

```dockerfile
COPY ci/maven/settings.xml /home/fugaro/.m2/settings.xml
COPY ci/scripts /opt/team/scripts
COPY cli /opt/team/scripts
```

To use such a Dockerfile as a catalog entry today, the operator either keeps building it outside Fugaro and publishes a prebuilt (`kind: image`) entry, which gives up Fugaro's build, smoke and nightly rebuild, or rewrites every `COPY` as a `RUN --mount=type=secret,id=git-credentials …` step that clones the repository a second time inside the build. The spec's reason for the Dockerfile-only copy (one read path, a record of exactly what was built, no full checkout for one file) is sound, but it makes the most common Dockerfile shape unbuildable.

### What you would like

An optional build context for a `dockerfile` entry: the repository the Dockerfile is in, or a directory of it, checked out at the exact commit the Dockerfile was read at, as the build context.

```yaml
image_catalog:
  entries:
    - name: team-image
      source:
        kind: dockerfile
        repo: owner/tools
        path: ci/Dockerfile-java
        branch: main
        context: .          # new: the repository root at the pinned commit; or a subdirectory such as ci/
```

What it would keep from the current design:

- **One record of what was built**: the Dockerfile's sha256 plus the commit SHA the context was checked out at, both in the build record and the image's revision label, the way per-repository derived builds already pin `FUGARO_COMMIT`.
- **The rebuild triggers stay exact**: the nightly catalog check would treat a change under the context directory on the branch (or a narrower list of paths, like `rebuild.paths`) as a definition change, beside the Dockerfile and the base.
- **The credential stays out of the image**: the checkout uses the repository's provider credential outside the Docker build, as the per-repository build already does, so no step in the Dockerfile ever sees it.
- **The existing machinery**: per-repository derived builds already clone the repository with its credential, so this reuses that path rather than adding a new one.

An entry without `context:` keeps today's Dockerfile-only build.

### Help

- [ ] I could help build, review or test this
