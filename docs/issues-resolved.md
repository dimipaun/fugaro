# `fugaro update`: which of your issues this release fixes

`fugaro update` is the whole upgrade, and when it installs a release it also
answers one question about the failures your machine filed: did this release
fix any of them? It asks GitHub what became of the project's filed issues,
prints the ones the release fixes, and writes the answer into the project's
shared store so every teammate sees it after their next read. The other half
of an issue's life — what an issue is, how it is filed and what happens to it
afterwards — is [issues.md](issues.md).

## What an update prints

A release that fixes two of the project's issues prints, after its own output
and nothing else:

```
This update fixes 2 of your project's issues: #0007 (the export command fails on a fresh checkout), #0012 (the retry keeps the old token).
#0007: hit by Ana and Jack
#0012: hit by Sam
#0007 is fixed. The command that failed was 'fugaro run "fix the export"'. You may want to try it again. It may change things.
```

Four things are in that: the one-line summary with the count, which is what a
person reads first; **who hit each issue** — `hit by Ana and Jack`, the names
of the people who reported it, so the admin running the update knows whom to
tell, since the admin is usually not one of them; the release's own record of
the failure, printed and never run; and, for a fix an earlier update never got
to report, the block below.

`Also fixed since the last pass:` opens that block. An issue whose milestone is
at or below the release the previous update spanned was fixed before this
window: nobody was told, usually because the machine that updated met GitHub
while it was unreachable or was rate limited. It is listed here rather than
lost, and the list is the same shape.

An update that fixes nothing of the project's prints nothing about issues at
all — not a heading, not a count, not a "none". A machine that is offline, a
project nobody has filed an issue with and a run that applied nothing all print
exactly the bytes they printed before this feature existed. There is nothing to
act on, so there is nothing to say. A coding agent's session is not one of these
cases: the findings are printed there as anywhere else, and it is the writing
the session declines (the last section below).

With `--json` the same list is an additive field on the result document,
`resolved_issues`, an array of `{number, summary, release, url, hit_by}`. It is
what the console, the mobile app and the history read, and like everything else
in that document it carries no issue text on stdout for a caller that did not
ask for it.

## How a release counts as fixing an issue

An issue is fixed by the release this update installed only when GitHub shows
both of these:

- it is **closed as completed**, and
- its **milestone** is a strict `X.Y.Z` — an optional leading `v` is accepted —
  **at or below the release that was installed**.

The milestone is what the pass reads, and the closing commit is not read at all
(D1756). The milestone names the release that *first* ships the fix, which is
what makes the answer useful to a reader: an issue fixed in 0.22.0 and
installed at 0.24.0 records `0.22.0`, so somebody still on 0.23.0 is told the
truth about their own machine rather than about the newest one.

Anything else is no verdict on a fix, and the issue stays `submitted`:

- **closed with no milestone**, or with one that is not a version (a title, a
  date, `next`): nothing is known about which release carries the fix, so
  nothing is claimed.
- **a milestone above the release that was installed**: the fix is real but not
  in this build. It is left exactly as it is and a later update picks it up.
- **still open**, and **a pull request** (the API answers about pull requests
  on the same route): not a fixed issue.

A closure that fixes nothing — **closed as not planned, or as duplicate** — is
not left as `submitted` either: the record moves to `dismissed` with `closed_as`
naming which of the two it was where the API says so, and an update prints
nothing about it, because a closure that fixes nothing is not something an
operator has to read.

A milestone **later** than the release that really ships the fix says "fixed"
late, which is safe — it names a release that does contain the fix — while one
**earlier** than the real release says "fixed" too early, which is not: a
reporter on a release without the fix is told their bug is gone. The milestone
discipline that keeps that from happening is a rule for whoever closes the
issue, and it is written where a release reads it:
[release.md](release.md).

## What is written, and where

Each fixed issue is marked `resolved` in the project's shared store, with
`resolved_in` set to its milestone. That record is what every other command
reads: `fugaro issue list`, `fugaro issue show` and `fugaro issue mine` print a
resolved issue with the release that fixed it and the link to the GitHub issue,
and a fresh report of the same failure uploaded afterwards is found by its
fingerprint and flagged as a regression against it — which is how a fix that
did not hold comes back to somebody's attention.

Two machines can resolve the same issue at once, an update and a daily report
among them. Nothing goes backwards over it: the transition out of `submitted`
is generation-matched and idempotent, so the one that loses re-reads the record,
sees `resolved`, and reports the issue as already resolved rather than writing
it twice.

## The installation record

`fugaro/installed.json` in the runs bucket, beside `fugaro/config.yaml`, holds
the two releases the last update spanned:

```json
{"format":1,"release":"0.24.0","previous_release":"0.23.0"}
```

`release` is the release that update applied and `previous_release` the one the
installation was on before it. The pair is the **window** the update's own
"Also fixed" block and the daily report's Resolved list are read over: it is
what makes "fixed since the last update" a question with an answer. The object
is written by the update door applying the installation, with a create-if-absent
followed by a generation-matched replace, and it only ever rises — a second
admin updating from an older CLI never lowers it, and a re-run of the same
release keeps the window as it is rather than moving it. A machine that may not
write it — a launcher running a config-only update — skips it silently and its
update is otherwise unchanged. An operator reads it with their own login:

```sh
gcloud storage cat gs://<runs bucket>/fugaro/installed.json
```

An absent record, or one this build cannot read, means **no window**: the update
prints the sweep — every issue the store already holds as `resolved` at or below
this build, under the `Also fixed since the last pass:` block — so the first
update after this feature lists the project's whole back catalogue of fixed
issues rather than only what its own pass found; the daily report, which has no
release to compare against, omits its Resolved list rather than guessing. An
installation that has never updated with a build that writes the object is in
exactly that state. A value that is wrong
and too high has no automatic recovery — an operator deletes the object
(`gcloud storage rm gs://<runs bucket>/fugaro/installed.json`) and the next
update writes a fresh one.

## The daily report

`fugaro runs digest` prints its Issues section in a person's own terminal, and
that section carries the same fixes from the other side: a **Resolved since the
last update:** list, one line per issue fixed in the record's window, with the
release and who hit it. When this machine runs a release older than the newest
fix the list holds, one line follows it:

```
Upgrade to 0.24.0 for these fixes: brew upgrade dimipaun/tap/fugaro, then fugaro update.
```

That is the nudge for the reporters the admin's own update cannot reach: nobody
is told anything by somebody else's machine. The section is printed where a
person reads it and nowhere else — not in a coding agent's session, not in the
scheduled job, not in the console — and that is also where a pass an earlier
update skipped is retried, because the report is what reaches a reporter
between two updates. The committed report file is untouched by any of it: the
Issues section is printed after the digest's own output and never written into
it.

## Offline, rate limited, and in a coding agent's session

The pass is best effort in every direction, because the thing it runs inside is
the whole upgrade. None of the following fails an update, prompts, or makes it
wait:

- **GitHub unreachable.** The pass ends at the first transport error and an
  update prints exactly what it printed before this feature existed. With
  GitHub back, the next `fugaro update` — or the next daily report — lists the
  issue, which is what the record's window is for.
- **Rate limited.** GitHub's unauthenticated limit is 60 requests an hour for
  one address, shared by everyone behind it. A pass stops on the first 403 or
  429 rather than spending the remainder on refusals, and the issues it did not
  reach are asked next time.
- **Bounded, always.** At most 20 issues a pass, least recently checked first —
  a cursor file in the user's own cache directory records when each number was
  last asked, so an issue that stays open for months cannot keep a newer one
  from ever being reached. Three seconds a request, 20 seconds for the pass in
  all, 256 KiB of a response, redirects refused.
- **The store unreadable or unwritable.** One line's worth of nothing: the
  update succeeds and the next one retries.
- **A coding agent's session.** The findings are printed and **writes nothing**:
  the resolve pass and the installation record are both cloud writes, and a
  session makes none of them, by design. The person's own `fugaro update` at
  their own terminal is what records and marks.

What is read from GitHub is a state and a release number and nothing else:
GitHub's own text — a title, a body, a comment — is never printed and never
stored, so what a person reads in an update is always this project's own words
about its own issue.

## The command each fixed issue names

The line naming the command that failed comes from **this machine's own issue
file** when it has one, matched by the issue number and the fingerprint of the
bug, and only otherwise from the shared record. A command from this machine is
the person's own, so it comes with the suggestion; one from the shared record is
printed as the command that failed and nothing more, labelled `(from the shared
record)`, because the store is writable by every launcher and a suggestion is
worth more than the text deserves. Only a `fugaro` command this build's own
tree answers to is printed at all, and it is re-redacted and printed as data
first.

Nothing ever runs it. An update runs no command a record holds, reads no stdin
and waits for nobody.

## What this does not do

- **An issue needs a milestone to be resolved.** An issue closed as completed
  without one is never reported fixed, however clearly the fix shipped.
- **A milestone earlier than the real release says "fixed" too early**, and the
  pass cannot tell: it reads the milestone and nothing else.
- **The issue's author can close their own issue as completed.** A close is not
  a maintainer's word that a fix shipped, and it is read as one.
- **Anonymous GitHub reads are rate limited** — 60 requests an hour from one
  address — so a busy office shares one budget, and a pass that meets the limit
  waits for the next update.
- **A GitHub reopen of a resolved issue is not noticed.** The record stays
  `resolved`; what surfaces a fix that did not hold is a fresh report of the
  same failure, which is flagged as a regression and linked to the original
  issue.
