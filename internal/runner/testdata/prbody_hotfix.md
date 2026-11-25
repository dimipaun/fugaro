Adds feature.txt, and a flag to turn it off.

## Deferred
- the retry loop backs off blindly; a jittered backoff is a follow-up
- the error message names the field but not the fix

## Open risks
- the parser keeps the last of two duplicate keys

## Verification
- ran the fixture suite: sh test.sh, exit 0
- ran the flag both ways by hand

## Revert plan
- revert the commit, or set FEATURE_OFF=1 to restore the old path

## Morning note
- what broke: the nightly import stopped on a duplicate key
- what was done: the parser keeps the last key instead of stopping
- what needs a proper fix: the import should report the duplicate
- how to verify: the nightly run finished clean

## Open questions
- should the flag default to off?

[//]: # (fugaro:status begin)
### Fugaro status
**Ready for review** · rigor: hotfix (repo) · tests passed on `<sha>` · review: ship in round 1 (recorded: model claude-opus-5, at <sha>) · review levels: level 2: ship in round 1 on `<sha>` · model cost $0.00 · finished <time>
**Hand-back** (`pr.md`)

**Revert plan** (`## Revert plan`)

- revert the commit, or set FEATURE\_OFF=1 to restore the old path

**Morning note** (`## Morning note`)

- what broke: the nightly import stopped on a duplicate key
- what was done: the parser keeps the last key instead of stopping
- what needs a proper fix: the import should report the duplicate
- how to verify: the nightly run finished clean
Run `abcd` (a stale time here means the run died: `fugaro diagnose 20260926-221530-abcd`)
[//]: # (fugaro:status end)