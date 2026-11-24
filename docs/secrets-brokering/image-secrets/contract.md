# The workflow-secrets wire contract

What a run and the credentials service may each rely on when a workflow secret is fetched at
bootstrap (`/v1/workflow-secrets`). The Go half is `internal/credsvc/wire.go`, written by **C1** of
[implementation-plan.md](implementation-plan.md); the service side is **IS1** and the runner's fetch
is **IS2**. Where this document and that code disagree, the code is the contract and this file is the
bug — both are this repository's, and a change to either is one change.

**Status: the contract only.** Nothing serves it yet: version 2's route is IS1, the runner's version-2
call is IS2, and I1 wires the two. Version 1 is served today by the S3 route
([../implementation-plan.md](../implementation-plan.md), B3.2) and is unchanged by any of this.

## The two versions

One route, two versions, and **one shape per version**: a caller writes exactly one of these bodies,
and the service refuses a body that carries a field of the other version rather than reading it as
one of the two.

| Version | The call | The answer |
| --- | --- | --- |
| 1 | `{"env":"DEPLOY_TOKEN"}` | `{"env":"DEPLOY_TOKEN","value":"…"}` |
| 2 | `{"name":"deploy-token","version":2}` | `{"name":"deploy-token","value":"…"}` |

- **Version 1 is the absence of `version`**, not the value `1`: every caller of it was written before
  the field existed and writes none. A body that spells `version: 1` is refused as a shape no side of
  this contract produces, so there is one set of bytes per version and a test of either side pins
  those bytes.
- **`env` keeps its meaning and version 2 does not read it** (CLAUDE.md, "a field never changes
  meaning"): it is the environment variable a value is exposed as. A version-1 call names the variable
  and a container is found for it in the per-repository record `creds/workflowsecrets/<slug>`; a
  version-2 call names the **logical secret** (`config.Secret.Name`, spelled `deploy-token`) and the
  service serves the project's own `fugaro_p_<name>` container.
- **`name` is held to the shape a `secrets:` entry is held to** (`config.SecretNameRE`: lower-case
  letters, digits and dashes), because a version-2 call names the entry a repository's `fugaro.yaml`
  declares. `env`, in version 1, is held to the shape a job could mount a variable under
  (`config.EnvNameRE`).
- **Everything a call carries is a selector, never a grant.** The repository is the verified job
  account's, the workflow is that same account's, and the run is the verified run identity's. No
  version of this contract has a field for any of them, or for a container: a call can only name a
  variable or a logical name, and what that is allowed to reach is decided by the service from records
  the caller cannot write.
- **Neither version carries a request identifier, a tier or a slip.** The slip, when one is presented,
  travels on the contract's own header (`X-Fugaro-Slip`), unchanged by this work.

## What each side may rely on

- **The answer is exactly one secret and nothing else.** Not a workflow's whole set, not another
  variable's or another name's value. Both sides read the echoed key — `env` for version 1, `name` for
  version 2 — and a caller that reads an answer under a key it did not ask for must refuse it rather
  than mount it: an answer is the only place a value ever appears, and a mismatched key is how a value
  would reach the wrong variable.
- **The answer is written no-store, and no header of it carries anything of the value.** A caching
  intermediary between the service and the run would otherwise hand the next caller a credential the
  service never served it.
- **No refusal, log line or audit line carries a value or echoes what the call sent.** A refusal names
  the field that was wrong and never its content: the caller's bytes are text from another process,
  and reading them back into a sentence is how an operator's log becomes a place a name leaks from.
- **One verified refusal is one audit line**, written before the value is handed over: an act the
  service cannot record is not made.
- **A refusal is typed, and one code of the six does not end that secret's fetch.** The caller relays
  the contract's own code and prints its own sentence for it; it never displays the service's message,
  which is written for the operator's log. The exception is `workflow_secret_not_allowed` — the answer
  that the name is not in the record — which a runner meets by asking version 1 once for that secret's
  own variable before it gives up, so a repository already served by version 1 keeps working while its
  name is being copied into the record (IS2). Every other refusal is final, and the fallback itself
  goes when version 1 does.
- **Version 1's behaviour is untouched.** A caller of it is served exactly what it was served before
  this contract gained a version, and a mounted value still wins over any fetch (the flip is a no-op
  for a job that still mounts its secrets).

## The refusal codes version 2 answers

The codes below are the whole of what version 2 may be refused with. They exist so a caller can tell
the cases apart and print the fix that actually works; a runner that has to guess between them would
send an operator to the wrong document. A code that is not in this table is not one this contract
writes, and a reader must refuse a refusal body it cannot read rather than act on it.

| Code | Status | What it means | The fix the caller prints |
| --- | --- | --- | --- |
| `unverified_run_identity` | 403 | The caller is not a per-repository coding job: a review job, a shared project job, a build identity, a coordinator or a person names no one repository's workflow. | Nothing to run: the class of caller is what refused, and this route serves a repository's coding job only. |
| `workflow_secret_not_allowed` | 403 | The name is not one this repository may fetch: the project record's allow-list for it does not admit the calling repository. | `fugaro secrets allow NAME owner/repo` |
| `workflow_secret_unmarked` | 503 | The name's container is not one this service serves a workflow secret from: a `fugaro_p_` container with no workflow-secret marker, or one whose shape is not the intake's. | `fugaro update --repo` (which copies a project copy into the grammar, or says why it will not), or `fugaro secrets shared set NAME`. |
| `workflow_secrets_off` | 503 | Version 2 is not served while a repository's own copies are still in force. | Nothing to run yet: the flip that retires the per-repository record is a later release, and until it lands this refusal is the answer. |
| `state_unavailable` | 503 | The service could not read the state it must read — the record, the container, or its own reader. | Retry; if it persists, the installation's database or Secret Manager access is the incident. |
| `request_refused` | 400 | The body is not one this contract reads: an unknown field, both versions' fields at once, a name of neither shape, or a version this contract does not carry. | Fix the caller: this is the request, not the state. |

`workflow_secret_not_allowed` and `workflow_secret_unmarked` are the two that matter most, because
they are the two an operator meets while a name is being set up, and they have different fixes: one
is the owner's allow-list, the other is the container's own shape. A refusal that could not tell them
apart would be the reason to reopen this contract (the plan's R-request).

## The file field

`config.Secret.File` (YAML `file: true`, D3327) is the repository's declaration that the value should
reach the run as a **path** rather than as the variable's own content — a tool that insists on reading
a credential from a file. What the contract fixes is the declaration's shape and what the runner owes
it:

- **A file secret still names its `env`**, which is the variable that receives the path. `fugaro.yaml`
  refuses a file entry with no variable, because the value would otherwise be written where nothing
  points at it. The key is a boolean; a string is a typo, refused by the decoder and by
  `schemas/fugaro.schema.json`.
- **The runner's obligations are IS2's** and are stated here so a reader of this contract knows what
  the declaration buys: the value is written to a file under a tmpfs directory with mode `0400` and
  the variable is set to that path; the value is registered with the run's redactor *before* it is
  written anywhere; and a file is then treated exactly as the environment secret it stands in for —
  present for a coding run's stages, never written in an independent review run, removed at run end
  (F19, G3). A `0400` file does not hide the value from the agent's own stages, which share the run's
  user; nothing here claims otherwise.
- **The declaration changes no access.** A file secret is fetched exactly like any other (version 2 by
  name), and a name that is not allowed, or whose container is not marked, is refused exactly as above.

## The stub

`internal/credsvc/workflowsecrets_stub.go` is `WorkflowSecretsStub`, an HTTP handler that answers both
versions from two in-memory maps: `Values` for version 2 (a logical name to a value) and `Envs` for
version 1 (a variable to a value). It exists so IS2 can drive the runner's fetch against a real handler
and the contract's own bytes, before any route serves it.

It decides nothing, and deliberately: it reads no identity, no record, no label and no cloud, and no
server wires it in. A name its map does not hold is refused `workflow_secret_not_allowed`, which is
what lets a test pin the runner's fallback to version 1 for a variable the version-1 map does hold.
**I1 deletes the file**, and the real route's policy tests (IS1) are what hold the refusals this stub
merely spells.
