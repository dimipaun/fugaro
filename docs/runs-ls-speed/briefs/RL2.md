# Task: runstore: the pull-request index, one object per run

Build task RL2 of `docs/runs-ls-speed/implementation-plan.md`. Read its RL2 section, `docs/runs-ls-speed/reconciliation.md` (F3, F4, F7, F8, D2961 to D2963) and `docs/bucket-iam/spec.md` §2 first. This is a security-sensitive format: the job account that writes it is not trusted, so it is a hint and never a source of rows.

**Goal.** Define the pull-request index: one small object per run at `runs/<slug>/pr-index/<N>/<run-id>.json`, with fields `version`, `run_id`, `slug`, `pr`, `kind` (`opened` or `followed`) and `at`. Add `runstore.PRIndexEntry`, `PRIndexPath`, `PutPRIndex` (create-if-absent: a second write of the same run is a no-op, never an overwrite) and `ListPRIndex(ctx, slug, n)` (one prefix listing, strict decode, an undecodable or mismatching object is skipped with a warning and never fails the query). Add `schemas/pr-index.schema.json`, add-only fields. No Terraform or IAM change: the job account already writes `runs/<slug>/`.

**Files.** `internal/runstore/prindex.go` (new), `internal/runstore/prindex_test.go` (new), `schemas/pr-index.schema.json` (new), `schemas/schemas_test.go`, `internal/runstore/list.go` only if a reader needs the skip. Grep every reader that walks `runs/<slug>/` (including `internal/runcache`) and make each skip `pr-index/`; name them in the pull request.

**Tests, red first.** `TestPRIndexSchemaRoundTrip`, `TestEntryPathIsPerRun` (fails before the path function), `TestPutIsCreateIfAbsent`, `TestListPRIndexByNumber`, `TestBadEntrySkipped`, `TestReadersSkipPRIndex`. Run `go test ./internal/runstore/ ./schemas/ -run 'PRIndex|Entry|Readers|Schema' -count=1`, then build, gofmt, vet.

**Rules.** A field never changes meaning: strict decode, a `version`. No secret-shaped literals. Targeted tests. Do not touch `docs/status.md` or `.github/workflows/`. Commit message: `runstore: the pull-request index, one object per run (D2962)`.
