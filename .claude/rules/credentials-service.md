---
paths:
  - "internal/creds/**"
  - "internal/credsvc/**"
  - "SECURITY.md"
  - "website/content/security.md"
---

# Credentials service

- Everything here is security-sensitive. Get an independent review before merging, and never merge on an in-loop `ship` alone.
- Once the caller's identity is verified, every refusal writes exactly one audit line (`internal/credsvc/audit.go`). No path may write zero lines or two.
- No credential value, token or key may appear in an audit line, a log line or an error. A refusal names the field, never its content. Test fixtures carry no secret-shaped literals either.
- The service ships dormant. An installation opts in with `fugaro update --provision-credentials`, and that command and `fugaro creds set` are refused inside an agent or a run. Never run either one.
- `config.ServiceHeldSecretNames` stays empty. Flipping it (#907) takes the GitHub App key's mount and accessor off the job, and the owner gates it on live checks 94-96 and 99. A Terraform guard test (`modules/workflow`, `roots/repo/tests`) holds the module's mirror to it.
- The service account's only project-level grant is the custom database role `fugaroCredentialsDatabase`, with exactly `firebasedatabase.instances.get` and `.update`. Never use a predefined role and never add the instance lifecycle permissions.
- `website/content/security.md` is `SECURITY.md` byte for byte, below a generated header. Edit only `SECURITY.md`, then regenerate the page with `go run ./internal/clidoc` in the same PR. `internal/cli/docs_website_security_test.go` fails on any drift, and `website/test/security.mjs` holds the page's shape.
- Every reader of run dollars splits money from notional on `model_notional_usd`, never on `ModelBasis` (P12, D140). `internal/runstore/cost_readers_guard_test.go` enforces this.
- The owner runs every step in `docs/credentials-service/runbook.md`. If a task needs one, say which step is theirs and stop.
