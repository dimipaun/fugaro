# 0.14.0 operator steps (draft)

**Status: the draft as task B1.13 part one wrote it, and 0.14.0 has shipped.** It is kept as written
because it is what that release's `### For operators` section carried; the sentence below that the
proxy "arrives in 0.15.0" did not happen — 0.15.0, and every release through 0.20.3, shipped without
the proxy chain, which no release carries today (its first two tasks, B1.5 and B1.6, are merged since
the census below was read, and the runner's half, B1.7, is not).
[status-2026-11-07.md](status-2026-11-07.md) is the census
of 2026-11-07: it says what is on `main`, what remains and which tasks can launch now.

**Amended 2026-11-10 (D1984): the draft is still kept exactly as written, and one step it was
missing is carried in a dated amendment below the list** — a non-default `FUGARO_BITBUCKET_API_URL`
that its installation has not listed in `bitbucket_api_roots`. The list itself is not touched.

The ordered steps an installation must run after deploying 0.14.0. They are drafted here (plan task
B1.13 part one) and copied into the release notes' `### For operators` section by the 0.14.0 release
PR (`docs/releases/v0.14.0.md`), which is what an operator reads.

This draft carries the **policy v4 lockstep** (decision D477, Bitbucket plan task T2.1 as amended by
[implementation-plan.md](implementation-plan.md) D493) — the one step that is owed by every
installation, whether or not it uses Bitbucket.

Bitbucket brokering (the proxy) arrives in 0.15.0: the owner moved that chain there on 2026-10-30. In
0.14.0 a Bitbucket run's job still holds its own token and reads it as it always has, so
`proxy: true` in the published block changes nothing such a run does.

### For operators

1. **Before upgrading, keep the enrolled list**: run `fugaro project policy show` and keep what it
   prints, because the republish below rebuilds `allow_repos`.
2. **Upgrade the CLI, then refresh the images.** The policy version moves to 4, and the two
   versions refuse each other: a 0.14.0 CLI refuses a launch against a published version-3 node,
   and a 0.13.0-or-older CLI refuses a launch against a version-4 node. A job image built from
   0.13.0 or older is not caught at launch: the run starts and its runner halts at bootstrap with
   "refresh the runner image". Refresh each repository's image: `fugaro image refresh` in the
   checkout of each onboarded repository (or `fugaro update --base base`). `fugaro image build`
   alone is not enough: it builds on the base image the local config already pins, whose runner is
   still the older release's.
3. **Republish the project policy, per installation**, in your own terminal outside any repository
   checkout. The order is the lockstep, because `fugaro project policy publish` refuses to
   overwrite a node written in another version:
   1. `fugaro project policy publish --delete` — removes the version-3 node.
   2. `fugaro project policy publish` — writes the version-4 node.
   3. Read the diff the publish prints and repair the list against what step 1 kept, with
      `fugaro project repos add <owner/name>` and `fugaro project repos rm <owner/name>`: the
      delete removes the whole node, so entries added or removed by hand do not survive it.
4. **If a Bitbucket profile mounts the token**, check the republish succeeded: the node now
   requires a profile that mounts `bitbucket-token` to write its own exact `allow_repos`, a subset
   of the block's, and to not be a GitHub profile. A node that breaks either rule is refused where
   it is published, and the fix is the local config and a publish, not the node by hand.

### Amended 2026-11-10: one step the list above was missing (D1984)

**The four steps above shipped as they stand; this fifth one did not, and the runners built from
0.14.0 on make it owed.** It is plan task T2.11's step 5, and decision D1984 of
[../bitbucket-workspace-token/reconciliation-secrets-brokering.md](../bitbucket-workspace-token/reconciliation-secrets-brokering.md)
records why it is written here: the pin is live in every runner built from 0.14.0 on, so an
installation with a non-default root that refreshed its images has runs refused today, and the note
that would have told it why never shipped.

5. **If a job's environment sets `FUGARO_BITBUCKET_API_URL`** to a root other than
   `https://api.bitbucket.org/2.0`, list that root in `bitbucket_api_roots` and republish **before**
   you upgrade the runners, or every cloud run of a Bitbucket repository is refused. The list is the
   operator's own local config, published through the policy by `fugaro project policy publish`
   (plan task T2.3), and the refusal is `internal/runner/runner.go`'s `checkBitbucketAPIRoot`,
   called from `openProvider` for every Bitbucket provider outside a local invocation. A job that
   sets no such variable, or sets the default, owes nothing: the default root needs no entry, and an
   installation with no Bitbucket repositories owes nothing either.

**This is an amendment, not a rewrite.** The draft above stays exactly as B1.13 part one wrote it
and as 0.14.0 shipped it; a dated note below the list is how this repository corrects a shipped
release record.
