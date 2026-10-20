# Terraform test fixtures

`bitbucket-oauth.tfvars.json` and `github-vertex.tfvars.json` are the golden
tfvars `fugaro update` writes, regenerated with
`go test ./internal/infra -run TestGoldenTfvars -update` (TestGoldenTfvars
compares them byte for byte).

`project-secrets.tfvars.json` is **hand-maintained**: it is a copy of
`bitbucket-oauth.tfvars.json` plus one `repo.project_secrets` entry, which no
`-update` run can produce — `RepoVars` serializes only what `Repo()` computes,
and `ProjectSecrets` is filled in by `Readiness` (design
`docs/design/project-secrets.md` §5), which `TestGoldenTfvars` never calls. It
exists so `repo.tftest.hcl`'s `build_and_check_use_the_resolved_secret` run can
plan a repository whose `bitbucket-token` resolves to a project-wide container.

`TestProjectSecretsFixtureIsTheGoldenPlusOneKey` in `internal/infra` pins it:
the file must stay exactly `bitbucket-oauth.tfvars.json` with that one key
added, so re-generating the golden for an unrelated reason cannot let this
fixture drift silently.
