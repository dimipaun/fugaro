# The spec: a workflow secret stored once for the project, behind an operator-local allow-list

**Status: the issue text, verbatim, with this line added; it states no decision of its own.**
Issue **#1678**, "secrets: let a workflow secret be stored once for the project, behind an
allow-list", opened by the repository owner. It is reproduced below **byte for byte** — headings,
the checklist box and the em dashes included — and nothing in this file is the design. The design is
[README.md](README.md), [reconciliation.md](reconciliation.md) and
[implementation-plan.md](implementation-plan.md); where one of them and this file disagree about what
the issue *says*, this file wins, because this file is a copy of the issue and they are readings of
it.

---

# Issue #1678: secrets: let a workflow secret be stored once for the project, behind an allow-list

### The problem

A workflow's own secret (`workflows.<w>.secrets` in `fugaro.yaml`) is always stored per repository. `fugaro secrets set --help` says so: "A workflow's own secret has no project-wide meaning and is stored in the repository's own container as before." Only the reserved names (the API key, the GitHub App key, a provider's key, an allow-listed reserved secret, a named Claude account's token) can be stored once for the project.

That doesn't fit a project of many repositories that all need the same credential. Example: a project of about 65 Maven services that all resolve dependencies from one private Nexus through a read-only account. Each repository's workflow declares `nexus-username` and `nexus-password`, and the operator has to store both values once per repository: about 130 hidden prompts, and 130 copies to rotate when the password changes.

### What you would like

A workflow secret that can be stored once for the project and mounted by every repository that declares it, gated the way the project-wide Bitbucket token already is: an operator-local allow-list (like `project_secrets.<name>.allow_data_to`, wildcards within a segment) naming the repositories the value may reach, so a project copy never widens to a repository the operator did not list. A repository's own copy would still win over the project's, as it does for the reserved names.

### Help

- [ ] I could help build, review or test this
