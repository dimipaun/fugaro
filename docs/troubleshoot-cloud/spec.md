# The cloud tier of `fugaro troubleshoot`: the spec as received

Nov 15, 2026 · @Dimi

**Status: the owner's order of 2026-11-15, written down as received.** Nothing
here is built, no task is approved and no number is spent. The command today is
`internal/cli/troubleshoot.go` and the design behind it is
[../diagnose-agent/](../diagnose-agent/), which this spec does not reopen: the
evidence it collects, the redaction it applies, the read-only child it starts and
the offline path it prints are all taken as they are.

**Three of the four tiers are another task's.** A parallel run (`cli/troubleshoot-fix`)
fixes tier 1 (a model-free diagnosis), tier 2 (the local login, with a clear
refusal when it is missing) and tier 4 (evidence only, with the reason). This
folder designs the missing one, **tier 3, the cloud diagnosis job**, and builds
on that task rather than redoing it. Where this spec and
[reconciliation.md](reconciliation.md) disagree, the reconciliation wins: it is
the file that reads what the code actually does.

## Problem

`fugaro troubleshoot RUN` explains one Fugaro command that failed on this
machine. It collects four things — the command's redacted log, `fugaro doctor`'s
output, the effective configuration and the Fugaro source at the installed
version — and hands them to a model, which answers with the cause and the fix.

Today it can only do that on a credential this machine already holds: a
`CLAUDE_CODE_OAUTH_TOKEN` or an `ANTHROPIC_API_KEY` in the environment, or
otherwise the `claude` login already on this machine. On the owner's Mac there is
neither, and the command answers

```
Not logged in · Please run /login
```

and diagnoses nothing. The person is left with a failure, a log and no
explanation — and the machine they are standing in front of is exactly the
machine whose login they cannot fix in the middle of an incident.

The installation is not short of a credential. Every job it runs has one: the
project's own cascade of Claude accounts, or its API key, brokered by the
credentials service. What is missing is a path that lets a person spend that
credential on **one question about their own machine**.

## The four tiers

The order is the whole design. Each tier is tried in turn, and the first one that
can answer does.

1. **The rules — model-free.** The log or the run record already names the cause
   and no model is needed to read it. The first case is a job image older than
   the published policy: the run record carries the refusal and the image, and
   the answer is a sentence and a command. (`cli/troubleshoot-fix`)
2. **The local login.** The read-only local child, on this machine's own
   credential, exactly as it runs today — with a clear refusal, naming the fix,
   when there is no login. (`cli/troubleshoot-fix`)
3. **The cloud diagnosis job.** *This spec.* When there is no local login and a
   pre-flight says the launch path is healthy, the evidence is uploaded to the
   person's own project and one bounded job reads it and answers.
4. **Evidence only.** The evidence is printed where it is, with the reason it was
   not diagnosed. (`cli/troubleshoot-fix`)

### Why the cloud job cannot be first

Three reasons, and the first is the one that decides it.

- **The failures people troubleshoot are often failures of the cloud path
  itself.** A stale job image refused at launch, the credentials service's
  `state_unavailable`, a budget halt — each is a failure of the very path a cloud
  diagnosis job would be launched through. A job launched through a broken path
  hits the same gate, and the person gets a second failure instead of an
  explanation of the first. The local tiers exist because the cloud is often what
  is broken; putting the cloud first inverts the one property the command has.
- **The cold start.** A local tier answers in seconds; a job launch is a
  container start, a credential lease and a model call. A diagnosis a person
  runs while standing in front of a broken machine is not a thing to queue.
- **The cost.** A job spends the project's money. The local tier spends a
  credential the person already has. The tier that spends the project's money is
  the one asked for last, and only when the cheaper ones cannot answer.

The cloud tier is also the only tier that sends anything off the machine that the
local tier would not have sent, and the only one that can be refused by the
project's own state. Both are reasons to reach for it last, not reasons not to
have it.

## What this spec must settle

Seven questions, each of which the reconciliation carries as a numbered
`D`-question with a recommended answer. Nothing here is decided by this document.

1. **The order of the tiers**, and why the cloud job cannot be first.
2. **The pre-flight** that says the launch path is healthy: which cheap reads
   decide it, without launching anything, and what it prints when it declines.
3. **The diagnosis job itself**: the catalog recipe, the bundle it reads, the
   answer document it writes, its cap, its timeout, the shared job variant it
   runs on, the account and model it spends, and what the run record shows.
4. **What leaves the machine**: the redaction, the bundle's contents, where the
   bundle lives and for how long, and why that is not a new disclosure.
5. **Who may run it**: the role, the human-terminal rule for any credential, and
   the managed-project and coding-agent refusals.
6. **The spend and the failure modes**: the cap's value, what a job that does not
   answer within its timeout prints, and the no-retry rule.
7. **The live check** the owner will run, as one script: the cloud tier answers
   for a deliberately refused launch, and falls back to evidence only when the
   cloud tier is made unavailable.

## Acceptance

- On a machine with no `claude` login and no token in the environment, a failed
  `fugaro troubleshoot RUN` still ends with a cause and a fix, and it does so by
  asking the project to read the evidence rather than by asking the person to log
  in.
- The tier that answers is visible in what the command prints: a person can tell
  a rules answer, a local answer, a cloud answer and the evidence-only path
  apart, and a cloud answer says what it cost.
- The cloud tier is never reached when the launch path is broken. A stale job
  image, a credentials service that is off or unreachable, and a budget halt each
  end in the reason and the command that fixes it — not in a second failure.
- Nothing leaves the machine that the local tier would not have sent, and what
  does leave goes to the person's own project, under a prefix that expires.
- A cloud diagnosis never runs on the person's credential and never reads one of
  the installation's secrets into the CLI.
- A coding agent's session and a managed project get the same refusals from this
  tier that `fugaro run` gives them.

## What is not in this spec

- **Tiers 1, 2 and 4.** They belong to `cli/troubleshoot-fix`; this folder only
  says where the cloud tier sits among them.
- **`fugaro diagnose`.** It explains a *cloud* run (`<repo-slug>/<run-id>`) and
  is unchanged (D1600). This tier explains a command that failed on the machine,
  by asking the cloud a question about it.
- **The console, the mobile app, the website and the plugin.** A skill sentence
  naming the tier arrives only once the tier exists; no console or mobile screen
  reads it.
- **The evidence the command already collects.** Its four files, their redaction
  and the offline path are [../diagnose-agent/](../diagnose-agent/)'s, and this
  spec takes them as they are.
