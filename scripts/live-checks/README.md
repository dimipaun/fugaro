# The live checks' scripts

One script per check, with its own hermetic test beside it, which drives it
against fake `gcloud`, `fugaro`, `git` and `gh` on `PATH`: no network, no home
directory, no cloud. Both naming forms are in use: the older `check<NNN>.sh`
with `check<NNN>_test.sh` (`check680.sh`), and `check-<NNN>-<name>.sh` with
`check-<NNN>-<name>_test.sh` (`check-890-data-layout.sh`), whose name says what
the check measures; a few carry the number alone (`check-840.sh`). The list in
`scripts/live_checks_test.go` names every script and its test, whichever form
it has. What each check
measures, and the command an owner runs it with, is in
[`docs/gcp-live-checklist.md`](../../docs/gcp-live-checklist.md) — a section per
check and a row in its register. Running a check for real is owner-run; nothing
here needs an account.

The repo-override-rulings plan's three checks are here too, all owner-run:
`check-910-lift-snapshot.sh` (Check 910, the lifted values of every enrolled
repository, read before release A's `fugaro update`), `check-911-lift-compare.sh`
(Check 911, the same read after it, compared with that snapshot) and
`check-912-lift-refusal.sh` (Check 912, release B refusing a lifted key on the
default branch, naming it on a follow-up ref and naming it, with its fix, in
`fugaro update --plan-only`'s plan). What each measures and the
command to run it with is in
[`docs/gcp-live-checklist.md`](../../docs/gcp-live-checklist.md), as the paragraph
above says of every script here.

`go test ./scripts` runs every `_test.sh` through `TestLiveCheckScripts`
(`scripts/live_checks_test.go`, which lists them). One script's own test can also
be run by hand:

```sh
bash scripts/live-checks/check680_test.sh          # all of one script's cases
CHECK_TEST_ONLY=test_the_whole_check_passes bash scripts/live-checks/check680_test.sh
CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check680_test.sh   # every 3rd case
```

## A full pass, locally, in shards

The suite is every script's every case, and one pass of it is long: tens of
minutes on a laptop. `LIVE_CHECK_SHARD=<index>/<of>` — the variable CI's
`scripts-live-*` runners set — keeps item *i* of the suite where
`i % of == index`, so a full pass is *of* shorter runs. Four of them:

```sh
for i in 0 1 2 3; do
  LIVE_CHECK_SHARD=$i/4 go test ./scripts -run TestLiveCheckScripts -count=1 -timeout 30m || break
done
```

`-timeout 30m` because Go's default 10-minute package timeout is shorter than a
pass; each shard of a four-way split is about a quarter of one, so a smaller
value works there. A value this variable cannot read — `LIVE_CHECK_SHARD=4/4`,
`LIVE_CHECK_SHARD=x`, a typo of any shape — fails the run rather than falling
back to the whole suite, because a silent fallback is a green run that tested
everything three times over and reports nothing. `TestLiveCheckCIShardCoversTheSuiteOnce`
pins that a split is a partition: every item on exactly one runner, in this
file's order.

Inside a pass the shards run eight at a time (`liveCheckWorkers`) under a
twelve-minute deadline each (`liveCheckTimeout`), and each script is itself split
three ways (`CHECK_TEST_SHARD=i/n`, read by the scripts' own `t()` helper), so a
script that hangs is reported by name rather than as a package-wide timeout.
