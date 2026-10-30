# 0.14.0 operator steps (draft)

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
