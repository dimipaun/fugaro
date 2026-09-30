# fugarosandbox

The sandbox repository for Fugaro's live tests: a tiny npm project with no
dependencies and two passing tests. Fugaro opens pull requests here while it
is being developed; nothing in it matters beyond that.

- `npm test` runs `node --test` and writes a JUnit report to `junit.xml`.
- `fugaro.yaml` sets no reviewers, so a test run never notifies a person.
