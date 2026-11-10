# `fugaro issue`: what happened to my issues

`fugaro issue` is the family the issue commands live under. An issue is one
failure a machine hit: the machine writes it to a local file first, and then
uploads it to the project's shared store in the runs bucket, where it gets a
number and where the same failure reported by somebody else is attached to the
same record as an occurrence. This page documents `fugaro issue mine`, the view
over the issues this user reported.

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
