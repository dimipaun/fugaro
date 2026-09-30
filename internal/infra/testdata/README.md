# internal/infra/testdata

`m4-jobspec-sandbox.json` and `m4-jobspec-sandbox.fields.tsv` are the job
spec the M4 binary printed (`fugaro gcp job-spec`) for the live-test sandbox
fixture (`deploy/sandbox/`, as `acme/sandbox` on Bitbucket, with the local
config in `spec_test.go`'s `m4LocalConfig`). They were captured with the M4
code (`main` at b857957, before the M5 merge ed87a87), and
`TestSpecMatchesM4JobSpec` compares `internal/infra` against them, so the
names never drift from what M4 created.

The command that printed them was removed after 0828eaa, so they can't be
regenerated from the current tree: to reproduce them, check out 0828eaa and
run its `internal/infra/testdata/capture-m4-jobspec.sh`. Never edit them.
