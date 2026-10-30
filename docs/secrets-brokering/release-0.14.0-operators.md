# 0.14.0 operator steps (draft)

The ordered steps an installation must run after deploying 0.14.0. They are drafted here (plan task
B1.13 part one) and copied into the release notes' `### For operators` section by the 0.14.0 release
PR (`docs/releases/v0.14.0.md`), which is what an operator reads.

This draft carries the **policy v4 lockstep** (decision D477, Bitbucket plan task T2.1 as amended by
[implementation-plan.md](implementation-plan.md) D493) — the one step that is owed by every
installation, whether or not it uses Bitbucket. The proxy steps (storing a token with `creds set`,
`proxy: true`, `fugaro update --repo` per proxied repository) are B1.13's and are not in this draft.

### For operators

1. **Upgrade the CLI, then refresh the image.** The policy version moves to 4, and a version-3
   node is refused by a version-4 reader in both directions: a 0.14.0 CLI refuses a published
   version-3 node, and the 0.13.0-or-older CLI and runner image refuse a version-4 node. Until both
   are on 0.14.0, a launch is refused with the skew remedy rather than run.
2. **Republish the project policy, per installation**, in your own terminal outside any repository
   checkout. The order is the lockstep, because `fugaro project policy publish` refuses to
   overwrite a node written in another version:
   1. `fugaro project policy publish --delete` — removes the version-3 node.
   2. `fugaro project policy publish` — writes the version-4 node.
   3. Re-add every repository you had added by hand with `fugaro project repos add`: the delete
      removes the whole node, so those entries go with it.
3. **If a Bitbucket profile mounts the token**, check the republish succeeded: the node now
   requires a profile that mounts `bitbucket-token` to write its own exact `allow_repos`, a subset
   of the block's, and to not be a GitHub profile. A node that breaks either rule is refused where
   it is published, and the fix is the local config and a publish, not the node by hand.
