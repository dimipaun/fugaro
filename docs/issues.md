# `fugaro issue`: what happened to my issues

`fugaro issue` is the family the issue commands live under. An issue is one
failure a machine hit: the machine writes it to a local file first, and then
uploads it to the project's shared store in the runs bucket, where it gets a
number and where the same failure reported by somebody else is attached to the
same record as an occurrence. An issue is named by its number — `#0007` — once
the store has given it one, and by its local ID `local-<run stem>` until then;
`show`, `dismiss` and `submit` take either spelling (`7` and `#0007` are the
same reference), and `list` prints the one a reader can copy.

| Command | What it is for |
| --- | --- |
| `fugaro issue list` | the project's issues, one row each: the whole team's, and this machine's not-yet-uploaded ones |
| `fugaro issue mine` | the issues this user reported, with where each stands and the release that fixed it |
| `fugaro issue show REF` | one issue, whole: its header and the diagnosis' own words |
| `fugaro issue submit [REF]` | exactly what would be filed on GitHub for one issue or for every one still waiting, and the filing itself with `--ask` |
| `fugaro issue dismiss REF` | mark one issue as not worth filing, here and in the project's store |
| `fugaro issue sync` | upload the issues this machine saved and has not uploaded yet |

`list` takes `--mine`, `--all` and `--json`; `mine` takes `--all` and `--json`;
`show` takes `--json`; `submit` takes `--ask`; `dismiss` and `sync` take none.
Nothing in the family runs a command for you: where a record names the command
that failed, it is printed and never run, because what it names came out of a
job's log.

## The listing: `fugaro issue list`

`list` prints the project's issues, one row an issue, with the class the
diagnosis gave it, where it stands and its one-line summary. The order is the
store's own: the issues with no number yet — this machine's, the ones a person
has to look at — first, then the numbered ones ascending. An issue belongs to
the project once it has a number, so the rows are the whole team's; where the
project's copy and this machine's file disagree, the project's is printed. A
record this machine's folder holds for another project is not a row: the folder
is one for the whole machine, and another project's number is not this
listing's to print.

Listing uploads this machine's own pending issues first, exactly as `mine`
does: the same failure reported twice is one issue, and the one still waiting
here has to reach the store before it can be shown beside the team's. A run
that cannot reach the store lists this machine's own records alone and says why
on stderr, which leaves the table itself clean for a reader and a script; a
machine with nothing saved and no store prints nothing at all, rather than a
header with no rows under it.

`--mine` lists the issues this user reported instead: the same rows `fugaro
issue mine` prints, from the same read, so the two commands answer with one
listing. `--all` goes with `--mine` and only with it, and shows the rows the
30-day rule hides; on its own it is refused, because this machine's own folder
keeps every record until it is deleted, and the rule is about the project's
issues and not about the files here.

`--json` prints the records themselves, in the shape
`schemas/issue-record.schema.json` describes — or, with `--mine`, the versioned
document `schemas/issue-mine.schema.json` describes.

## One issue: `fugaro issue show REF`

`show` prints one issue whole: a header naming it and where it stands — its
class, status, the run it came from, who reported it, when it was created, the
Fugaro version and OS that failed, the command that failed, and the GitHub link
once it has one — and then the diagnosis' own words: the summary, the
diagnosis, the steps and the log excerpts. A field with nothing in it is left
out. Every one of those values was written about log text this machine did not
write, so each is printed as data: nothing in it reaches the terminal as an
escape sequence, and the stored link is printed only in the shape
`https://github.com/dimipaun/fugaro/issues/<n>` — anything else a shared record
holds is dropped rather than followed.

An issue that has been uploaded is printed as the project's store has it: the
store is the truth once an issue has a number, so a teammate's dismissal, or a
fix the update pass recorded, shows here rather than the copy this machine
wrote when the failure happened. A number also names a teammate's issue, which
this machine has no file of: the rows `list` prints are the whole team's, and a
reference that is a number is read from the project's store. This machine's own
pending issues are uploaded first, as `list` does. A run that cannot reach the
store prints this machine's own copy and says why on stderr — or, for a number
it has no file of, refuses.

`--json` prints the record itself, in the shape
`schemas/issue-record.schema.json` describes.

## Filing one: `fugaro issue submit [REF]`

`submit` is the one command of the family that sends text to a public
repository, and it sends nothing until a person has read the exact bytes. It
builds the post once — the title, the body and the sha256 of those bytes — and
prints them. With no reference it visits every `pending` issue and skips the
ones already filed or set aside. Without `--ask` it files nothing and uploads
nothing: a plain run is a preview.

`--ask` uploads this machine's own pending issues first, exactly as `issue sync`
does, shows the preview, and files exactly those bytes after a yes at your own
terminal. It is the only thing that files: a pipe and a coding agent's session
never do, because a confirmation nobody can answer is a refusal and never a
silent proceed. The filing is made under a claim on the project's record, so
two people filing one issue at the same moment file it once; a claim another
submitter holds is refused with who holds it and until when, and a record that
changed after the preview was shown is refused rather than filed unseen.

Filing needs GitHub's own `gh` with an account on github.com. Where it is
missing, or has no account there, the install command or `gh auth login` is
printed with a new-issue link holding the same title and body; nothing is filed
and the command exits non-zero. The post carries the bug and not whose machine
hit it: it is projected from the record and scrubbed of the reporter, the
project's and the repository's names, home-directory paths and emails, and the
text is re-redacted by your own binary at submit, because the store is a file
any teammate's machine can have written. A link has a length limit, so a body
too long for it is cut to a fixed budget and ends with a notice saying so,
naming the reference a person prints the whole issue with — `fugaro issue
show` — rather than a path on the machine that filed it.

## Setting one aside: `fugaro issue dismiss REF`

`dismiss` marks one issue as not worth filing. The status is written in the
issue's own file and nothing else changes: the record stays where it is, so a
listing still shows what was set aside and who set it aside, and nothing is
filed.

An issue is the project's, so one that has already been uploaded is set aside
in the project's store as well: a teammate's listing then shows it as
dismissed, and the decision does not have to be made again on every machine. A
store that cannot be reached is a line on stderr and the dismissal here stands
— the command is safe to run again once the store is back. A store that answers
and refuses — the issue is filed, or a teammate has already decided it — is
another matter, and refuses the command, because the decision is then not this
machine's to make.

A number this machine has no file of is a teammate's issue: it is read from the
project's store and set aside there, and a store that cannot be reached is then
the refusal rather than a line, there being nothing here to set aside instead.
An issue that has already been filed is refused outright: it is open on GitHub,
and setting it aside here would not close it.

## Getting them to the team: `fugaro issue sync`

`sync` uploads the issues this machine saved and has not uploaded yet. An issue
is saved on the machine that hit the failure and uploaded afterwards, so a run
that was offline, or that died before its upload, leaves it here; this is the
command that retries those. Each one takes the project's next number, is renamed
to it, and — when a teammate already reported the same failure — is attached to
their issue as one more occurrence instead of becoming a second one.

An issue that cannot be uploaded stays where it is and is named in one line, and
the next run retries it: nothing here deletes an issue or files one on GitHub.
`No pending issues.` is the whole answer when nothing was waiting.

## What happened to my issues: `fugaro issue mine`

`fugaro issue mine` lists the issues this machine's user reported to the
project: the ones they created and the ones their own report was merged into as
an occurrence of somebody else's record. `fugaro issue list --mine` prints the
same listing from the same read — it is one function, not two — and takes the
same two flags.

Like `list`, it syncs first: this machine's own pending issues are uploaded
before the project is read, and an upload that fails is one line, never an error
that stops the listing. In a coding agent's session nothing is uploaded and
nothing is read from the project at all: the rows are this machine's own files
and the last shared read it cached.

### The rows

Each row is the issue's reference, the status it displays and its one-line
summary, and under them what is known about it:

- **The reference** is the project's number, `#0007`, once the shared store has
  given the issue one, and this machine's own name for it — `local-<stem>` —
  before that.
- **The status** is one of `local` (written here and not uploaded yet),
  `pending`, `submitted`, `resolved`, `dismissed` or `regression`. Two of those
  are derived here rather than values of the record: `local` says the issue has
  no project number yet, and `regression` says the user's own flagged occurrence
  is on an issue the shared record calls resolved — a failure that came back
  after the release that fixed it. Another reporter's regression does not make
  this user's row one.
- **The times**: when the user hit it, when it was submitted, when it was
  resolved and when it was dismissed. A submitted, resolved or dismissed time
  that has not happened prints `-`, and the hit time is the user's own — the
  moment the issue was created here, or the time in the user's own earliest
  occurrence when a report of theirs was merged into somebody else's issue.
- **The link** to the GitHub issue, printed for a `submitted`, `resolved` or
  `regression` row. Only a link in the shape
  `https://github.com/dimipaun/fugaro/issues/<n>` is printed; anything else in a
  shared record is dropped rather than followed.
- **For a resolved issue**, the release that fixed it and how this machine
  compares with that release: `fixed in 0.23.0` when the CLI and the
  installation are both at or above it; the version this machine runs followed
  by the one command that closes the gap, `fugaro update`, when they are not;
  `installation not checked` when the installation's own record could not be
  read, which leaves the CLI deciding alone; a record whose fixing release is
  not one this build can compare against; and `dev build: cannot compare` for a
  build that is not a release and so cannot be updated to one.
- **How many other people hit it**, as `also hit by 2 other(s)`. Other
  reporters are counted and never named: a count of zero prints nothing, and the
  user is never counted among them. The same person on two machines is one
  person.
- **For an issue this machine is already covered for**, the command this
  machine's own issue file recorded for the failure, prefixed by `retry:` and
  followed by "commands can change things; nothing is run for you". Nothing is
  ever run: the row is a listing.

### The order

Rows that need attention come first:

1. `regression` — a fix that did not hold.
2. resolved issues this machine is *behind* on: fixed, not updated.
3. `local` and `pending` together — the longest wait is the loudest.
4. `submitted`.
5. `resolved` (covered or unknown) and `dismissed`.

Within the first, second and fifth group the newest status change comes first;
within the third and fourth the oldest hit comes first.

### Finished rows age out after 30 days

A row whose resolve time or dismissal time is more than 30 days old is hidden,
and a final line says how many were hidden. `--all` shows them. A row in groups
1 to 4 is never hidden, and neither is a row that carries no such time: nothing
is hidden on a guess. The rule is applied when the listing is printed, against
the current clock, so a cached row cannot keep a stale one hidden.

### Offline

After a successful read of the project's store, `mine` writes a snapshot of
those rows for this installation — one file per project, in the user's cache
directory — and prints `offline: shared state as of <time>` from it when the
store cannot be reached. The snapshot holds display data only: no command, and
nobody else's identity. It stands in for the shared part of the listing; this
machine's own issue files are always read live, so an issue that changed here
since the snapshot was written is current.

With no snapshot cached, an offline run prints this machine's own local issues
only and one line saying so. Either way the coverage and the 30-day rule are
computed again for this run, so an offline listing cannot keep a nudge stale or
hide a row the clock has brought back.

### Who "me" is

The user is the address of the verified Google credential the command runs as —
the same string an upload records as the issue's reporter, and never the local
config's `user:` key or `git config user.email`, which a person or an agent can
set. After each successful lookup the address is cached in the user's cache
directory as the **identity cache**, which is what an offline run reads to know
who "me" is. The cache drives display reads only: no upload, claim or dismissal
ever reads it. An account switch is picked up on the next run that can reach the
credential, so until then an offline listing is the previous account's rows.

### No GitHub is read

`mine` reads no GitHub issue state and never asks the GitHub API about an
issue: what says an issue is fixed is the `resolved_in` release the shared
record carries, printed against this machine's own version. The listing is
therefore as current as the last read of the shared store — an issue closed on
GitHub stays `submitted` here until the resolve pass marks the record resolved
and this user's next `mine` reads that. The pass that runs today is the daily
report's: `fugaro runs digest`, in a person's own terminal, asks GitHub what
became of the project's filed issues and writes the ones its release fixes back
as `resolved`. (The root command's once-a-day release check is not issue state,
and it runs for this command as it does for every other.)

### Machine-readable output

`--json` prints one versioned document — `"format": 1`, and
`schemas/issue-mine.schema.json` describes it — with the source the rows came
from (`shared`, `snapshot` or `local`), the time that state was known, the
installation's release, the number of hidden rows and whether the store's own
listing was cut short. The rows carry the same display data as the printed ones,
counts included; no other reporter's name or digest is in the document.

## In a coding agent's session

An agent's session may read what is on this machine and may not write to the
cloud, so the family splits there. `list` and `show` print this machine's own
files and read nothing from the project; `mine` is the one of the three that
also prints the last shared read it cached, because the rows it shows are the
project's and an offline `mine` would otherwise have none. `sync` refuses,
because uploading is a write to the project's store. `dismiss` refuses even for
an issue that exists only on this machine: a steered diagnosis could otherwise
bury a real bug, so a person sets one aside. `submit` previews as usual, and
what it previews is the bytes this machine holds rather than the project's
record of a numbered issue; `--ask` refuses, because the upload and the claim
are writes, so a person's own run re-reads that record and shows what it will
file. Each refusal names the command a person runs at their own terminal
rather than saying that an agent may not.
