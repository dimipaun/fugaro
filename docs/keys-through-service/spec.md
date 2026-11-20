# Every model key through the credentials service

Owner: Dimi Paun. Date: 2026-11-19. This brief was written by the coordinator from the owner's
ruling D2975 in [workspace credentials](../workspace-credentials/spec.md); it is the spec this
folder reconciles against the code.

## The owner's words

> All keys should be served through the credsrv, we want to move away completely from mounting
> them, too much terraform.

## What to build

- Every model credential a run uses is served by the credentials service: the service holds the
  key and makes the upstream call. This applies to every project, in a workspace or not.
- No model key is mounted into a job's environment by Terraform, and the Terraform that mounts one
  goes away.
- It reverses D1634 and D1670, which keep OpenRouter and Vertex keys on the runner path, mounted in
  the job that spends them.

## What stays

- D150's gate: the service refuses a Claude model through a third-party provider key until Check
  50 has run and passed.
- `fugaro update` is the whole upgrade, and it is safe with runs in flight.
- No command waits for input.

## Out of scope

- The workspace's service ([workspace service](../workspace-service/README.md)) and per-person
  tokens.
- Credentials that are not model keys: git tokens, the GitHub App key, workflow secrets and signing
  keys, which [secrets brokering](../secrets-brokering/README.md) owns.
