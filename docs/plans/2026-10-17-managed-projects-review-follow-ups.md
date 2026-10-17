# Managed projects: review follow-ups for Tasks 0.x and 1.x

Findings that the cloud run's second review of PR #488 (Task 5.1, the MISSING
column on `secrets ls`) raised against code that PR does not touch. Each
belongs to the task that wrote the code and gets its own fix, with tests,
outside #488. The plan is [2026-10-16-managed-projects.md](2026-10-16-managed-projects.md).

| # | Severity | Where | Finding | Task |
|---|----------|-------|---------|------|
| 1 | low | `internal/infra/managed/deletelist.go` (`parseDeleteList`, `ReadDeleteListForWrite`) | The "object here is not a list I wrote" state is not pinned: dropping `if len(d.Addresses) == 0 { return nil }` turns `{"addresses":[]}` into `Unreadable=false`, and no test catches it (the test compares with `len`/`slices.Equal`). `allow_delete.go`'s Unreadable branch has no CLI test. Fix is tests: assert nil and Unreadable for the empty-addresses body, and seed a foreign object for the confirmation. | 1.4 |
| 2 | low | `internal/cli/init.go` (`markCreated`) | The runs bucket is read from `r.res.Outputs`, set only in `writeConfig`, so a run whose Firebase root apply fails after `installRoot` created the bucket writes no marker and warns "the installation did not finish", which is untrue there. Fail-closed; the defect is the lost fact and the wrong reason. | 0.1 |
| 3 | low | `internal/cli/allow_delete.go` (`allowDeleteWriteErr`) | Every `ErrRefused` is blamed on the bucket, but `WriteDeleteList` also raises it for an invalid list, including one over the 64 KiB cap (about 1700 addresses), sending the owner to change project config instead of approving fewer addresses. | 1.4 |
| 4 | low | `internal/managed/plan.go` (`refusals`) | Splits each refusal on the first `" ("`, so a `for_each` key holding `" ("` yields a garbage address; and `rest` is returned only when nothing was shaped, so a mixed gate message drops its non-address line. Latent until Task 3.1 reads these addresses. | 1.0 |
| 5 | low | `internal/infra/managed/deletelist.go` (`proveRunsBucket`) | Proves the project number and the `fugaro=managed` label but not which bucket: other labelled buckets of the project would pass. Not reachable today (callers pin the name), but the name check should live in the proof. | 1.4 |

Done separately: finding 7 of the same review (the marker tests in
`internal/cli/init_managed_test.go` asserted absence in buckets that did not
exist) is fixed on branch `managed/marker-test-assertions`, with this note.
