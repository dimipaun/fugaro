---
paths:
  - "deploy/terraform/**"
  - "internal/infra/**"
---

# Terraform and the cloud side

- Run `terraform fmt -check -recursive deploy/terraform` before pushing. A missed `fmt` turned main's `terraform` job red after #951.
- In each root under `deploy/terraform/gcp/roots` (`installation`, `firebase`, `repo`, `compute`), run `init -backend=false -lockfile=readonly`, `validate` and `terraform test`, as the `terraform` job in `.github/workflows/ci.yml` does.
- Set `TF_DATA_DIR` outside the tree. `deploy/terraform` is embedded in the Go binary, so a root's `.terraform/` would end up inside it.
- A change to a module under `deploy/terraform/gcp/modules` changes every root that uses it. `installation` uses both `installation` and `project`. Test every affected root, not just the one you meant to change.
- When a module's plan changes, regenerate the golden plans in `internal/infra/tf/testdata/golden` with `scripts/gen-golden-plans.sh`. Never edit them by hand.
- Also run `go test -tags terraform ./internal/infra/...` (it tests the wrapper against a real binary) and `tflint`. `sh deploy/terraform/scan.sh` (Trivy) has to stay clean of HIGH and CRITICAL findings, unless `.trivyignore` justifies one.
- IAM: list the exact permissions and use the narrowest custom role. Use a predefined role only when it is exactly the surface you need (`SECURITY.md` explains why the credentials service's database role is a custom role).
- `internal/infra/tf/cover.go` decides what one `fugaro update` confirmation covers. A new resource type, or a role granted to a new kind of principal, is not covered until you add it to `allowedTypes` or `grantRules` on purpose. Never widen those tables to make a plan pass.
- Never run `terraform apply`, `fugaro setup`, `fugaro update` or any other live cloud action from an agent run. The owner runs live checks from `docs/gcp-live-checklist.md`.
- IAM, the cover tables and the credentials service's grants are security-sensitive. Get an independent review before merging.
- Steps an operator must take after a deploy go under `### For operators` in the release notes, in the same PR.
