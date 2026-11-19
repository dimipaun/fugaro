# Workspace credentials: a person's tokens and the shared keys, held once

**Status: every decision is answered (D2960–D2979, owner, 2026-11-19); no plan is written, no task
exists and none is approved.** An independent review of 2026-11-19 found that four of the first
eight answers could not be built as written; §5's ten decisions replace or complete them, and the
owner answered those the same day. **This work waits on the workspace's service being deployed
under its own plan (D2968), and D2975 is wider than this addendum (§5).** Written on 2026-11-18 at the owner's request; revised on
2026-11-19 against `origin/main` at `e2ec97c1a` and the review. It is an addendum to
[the workspaces design](../workspaces/README.md) and to
[workspace projects and users](../workspace-projects-users/README.md).

**Numbers reserved:** decisions **D2960–D2979** (all twenty used). **The first eight were asked and
answered as D2930–D2937**; that block was taken by `docs/review-against-task` while this draft was
open, so they are renumbered here one for one (D2930 is D2960, and so on to D2937 as D2967). Read on
2026-11-19: no file on `main` and no open pull request uses D2960 or higher.

## 1. The ask

The owner, 2026-11-18:

> Wouldn't be easier for ppl to store their oauth tokens at the workspace level so they can use them
> in every project? Ditto for the openrouter and anthropic key...

## 2. What exists

The first draft of this section read the design as if it were deployed. It is not.

- **The workspace's bundle can be written.** `fugaro creds set`, run with the workspace owner's
  config selected, writes the workspace's one bundle (`openBundleStore`, `internal/cli/creds.go`).
  Among other names it takes `claude-oauth-token`, `claude-oauth-token-<account>`,
  `anthropic-api-key` and `provider-key`.
- **Nothing serves that bundle to a member's run.** `credsvc.NewWorkspace`, `WorkspaceShared` and
  `NewWorkspaceMeter` have no caller outside tests, and `internal/credsvc/workspace_cap.go` says the
  meter is not wired to the call. The workspace root
  (`deploy/terraform/gcp/roots/workspace/main.tf`) creates a service account, a database role and
  one grant, and no service. The node that would hold the member table and the shared allow-lists
  has an encoder and a decoder and no writer (`internal/projectpolicy/workspace_credentials.go`).
- **Which credential a run picks when the member and the workspace both hold one is still open**
  (V5 of the workspaces design).
- **"A person's token" is two different things today.** `fugaro accounts add` declares a
  project-wide named Claude account, which has no owner. `fugaro secrets user set` stores a secret
  under the person's own prefix (`fugaro_u_<sub>_<name>`), which is owner-only and never lent
  (dynamic delivery, R7 and R11). Most stored Claude tokens are the first kind.
- **The person-prefix intake is the installation's own.** It admits that installation's launchers,
  and nothing in it names a workspace. The workspace's service admits no member's person
  (workspaces spec §4, F24, F31).
- **The service cannot tell who launched a run.** The run's identity carries no launcher. The
  launcher is the owner of a slip that the member's own service writes into the member's own
  database, which that member's admins can rewrite (D1943).
- **An OpenRouter key is mounted in the job that spends it**, one key per declared credential
  (D1634, D1670). The service's provider route can reach OpenRouter upstream, but selection refuses
  every provider-tier candidate until Check 50 (D150).
- **A project in no workspace** has only its own store.

## 3. The proposal

When a project is in a workspace, the workspace is where a credential is stored by default, once.
The project's own store stays for a project in no workspace.

1. A shared account or key is stored once in the workspace's bundle and served to the members its
   allow-list names.
2. A person's own Claude token is stored once at the workspace and serves that person's runs in the
   member projects.
3. An OpenRouter key becomes shareable through the workspace's service.

The two rules of the workspaces design hold as written. A member's own policy still narrows
everything the workspace offers. No run, CLI or person is ever handed a credential's value.

## 4. The eight answered decisions, and what the review found

The owner answered each on 2026-11-19, option (a) of every one, which was the recommendation.
"Stands" means the review found nothing against it.

| # | Was | Question and answer | After the review |
| --- | --- | --- | --- |
| **D2960** | D2930 | A person's Claude token is stored once, at the workspace's service | **Needs D2969, D2971 and D2973.** Nothing moves a stored token, so the per-project copies stay and offboarding still sweeps every member |
| **D2961** | D2931 | It serves every member the person holds a role in, read from the workspace's roster | **Cannot be built as written.** The workspace's roster holds members and admins, not per-project grants (D2817 as amended); those live in each member's own database. With D2962's owner-only rule it reduces to "wherever the person launched a run" |
| **D2962** | D2932 | Only runs its owner launched may draw on it; sharing is an opt-in per token | **The first half needs D2970.** The opt-in contradicts R7 and R11 of dynamic delivery: see D2976 |
| **D2963** | D2933 | A shared key may carry an explicit `*` for every member | **Needs D2977.** A `*` in a list of project names changes that field's meaning, and an older service would refuse the whole node |
| **D2964** | D2934 | An OpenRouter key moves to the workspace service's provider route | **Cannot be built as written.** It reverses D150, D1634 and D1670, which it did not name: see D2975 |
| **D2965** | D2935 | A personal token's use does not count against the workspace cap | **Stands, with D2974.** It must follow the credential actually served |
| **D2966** | D2936 | A project in no workspace is out of scope | **Stands** |
| **D2967** | D2937 | The work is a slice of workspace-projects-users, after WU0 | **Under-scoped.** The service this depends on is not deployed: see D2968 |

## 5. The ten decisions the review added

**Answered by the owner on 2026-11-19: option (a) of every one, which was the recommendation,
except D2975.** Three of them withdraw part of an earlier answer: D2975 (D2964's route), D2976
(D2962's opt-in) and D2977 (D2963's `*`).

**D2975 is the owner's own answer and not either option:** "All keys should be served through the
credsrv, we want to move away completely from mounting them, too much terraform." It is a ruling
about every project, not only a workspace's members: no model key is mounted in a job, and the
credentials service serves all of them. It reverses D1634 and D1670. It is wider than this addendum
and needs its own spec. **D150's gate stays** (owner, 2026-11-19, as recommended): the service refuses a Claude model through
a provider key until Check 50 has run and passed, so moving provider keys off job mounts is planned
now and switches on only after that check.

The first option of each is the recommendation.

| # | Question | Options | Why the first |
| --- | --- | --- | --- |
| **D2968** | The workspace's service is not deployed, its node has no writer and its cap meter is not wired. Who builds that, and when? | **(a) Its own plan, first**; this addendum waits on it. (b) This work builds it | It is the unfinished half of the workspaces design, larger than this addendum, and every item here needs it |
| **D2969** | Which "token" moves? | **(a) The project-wide named accounts and keys first**, as shared entries the workspace owner writes; a person's own prefix later, after the threat model. (b) Both at once. (c) Only the person's prefix | (a) meets most of the ask with the design already settled and no new trust. The person's prefix is where every security finding sits |
| **D2970** | Where does the workspace's service get a launcher it can trust? | **(a) Its own grant route and launcher class**, admitted from the workspace's roster. (b) The member's slip | A member's admins can forge the slip (D1943). With (b) they could spend any workspace person's subscription in their project with no act by that person |
| **D2971** | May a member's person store a token at the workspace? Today the workspace's service admits none (workspaces spec §4, F11) | **(a) Yes, for the people on the workspace's roster**, granted by the workspace owner's `fugaro update`; a person added since the last update waits for the next. (b) No | Without it D2960 has no way in. It amends a settled rule, so it goes through the threat model |
| **D2972** | How does a member's policy admit a personal token? Today it admits a workspace credential by name | **(a) One switch in the member's policy**, off by default. (b) The member names each person's token | (b) makes every member's admin list every person, which defeats the ask. Off by default keeps "a workspace never widens a member" |
| **D2973** | When the member and the workspace both hold a credential of one name, which is served? (V5) | **(a) The member's own**, and `fugaro update` reports a per-project copy that shadows a workspace one. (b) The workspace's | The member's own store is the tighter one. The cost: a token rotated at the workspace is still shadowed by an old per-project copy until that copy is removed |
| **D2974** | A personal name that is missing falls through to the bundle's entry of the same name today. At the workspace? | **(a) Never**: a missing personal token is a refusal. (b) Fall through, as an installation does | At the workspace the fall-through would serve the shared, paid entry without asking its allow-list, and count it as unbilled |
| **D2975** | OpenRouter, given D150, D1634 and D1670 | **(a) Leave it mounted per project for now**; revisit when Check 50 opens the provider tier. (b) Reverse the three decisions for workspace members now | The key is deliberately on the runner path, answered on 2026-11-17. Moving it also needs the per-repository flip discipline of secrets brokering |
| **D2976** | Lending a personal token | **(a) Never**; what is shared is a shared entry (D2969). (b) An opt-in per token, amending R7 and R11 | Keeps one meaning for "personal". Consequence either way: a run launched by a service account (the coordinator, the orchestrator) never holds a personal token |
| **D2977** | "Every member" for a shared entry | **(a) A new key `all_members`**, with a version bump of the node and a guard for an older service, and only once the cap meter is wired. (b) `*` in the list | The allow-list is the workspace owner's fence over its own money. Without the meter nothing bounds a member that joins later |

## 5a. The uniform rule, and what the plan is held to

The owner, 2026-11-19, after the ten answers above: the rule should apply uniformly to every key and
token, the Bitbucket token included. Two decisions record it, both answered that day as recommended.

| # | Question | Answer | Why |
| --- | --- | --- | --- |
| **D2978** | Is there one rule for every credential kind? | **Yes.** Any credential (a Claude token, an Anthropic or OpenRouter key, a Bitbucket or GitHub credential) may be stored at the project or at the workspace. The project's copy is looked up first and the workspace's is the fallback, and on the fallback the workspace's allow-list still decides. A personal token still never falls back to a shared entry (D2974) | One rule and no special cases; it is D2973 stated for every kind |
| **D2979** | Which service carries the traffic of a credential the workspace holds? | **The one that holds it.** The workspace's service proxies the calls, git included, and never hands the value to a member's service. A project that wants its git traffic local stores its own token | A member's admins control their own service, so a value handed to it is a value they can take. The load is measured and scaled instead |

Three constraints on the plan follow, the first two the owner's own concerns of the same day:

- **One code path.** The workspace's service and a project's are one binary and one implementation
  (`internal/credsvc`); the workspace's differs only in its per-member checks, and a project's
  service is the one-member case. The plan adds no second implementation of a route.
- **A live load check before anything depends on it.** The workspace's service carries every
  member's model calls on shared credentials, and the git traffic of any Bitbucket token it holds.
  A numbered check measures it at a realistic count of concurrent runs, and the plan sets the
  instance and concurrency limits explicitly; today they are the platform's defaults.
- **One service down stops every member's shared calls.** The plan states what a member's run does
  then, and keeps the service warm as Check 511 does for a project's.
- **Every project keeps its own service.** The owner considered, on 2026-11-19, not provisioning a
  project's service when the project holds no credential of its own, and kept the ruling of
  2026-11-05: `fugaro update` deploys it for every installation. It also serves review signing,
  build credentials, workflow secrets and the Bitbucket proxy, and it costs nothing idle. The plan
  ensures that a project with no credential of its own never wakes it.

## 6. What it costs

- **Every workspace admin can read every person's token.** R8 of workspace-projects-users gives a
  workspace admin owner on the workspace's project, so each one can read every personal token from
  Secret Manager in one place. D2825 says a workspace admin's terminal must not read personal
  tokens; the two meet here.
- **The intake's reach becomes workspace-wide.** Whoever holds its account can replace or delete
  any person's token for every member at once.
- **Offboarding does not get simpler at first.** Per-project copies remain, so the sweep over every
  member stays. Offboarding does not end an open slip (up to 24 hours) or stop a run in flight.
- **One subscription, more runs.** A token that serves several projects reaches its rate window
  sooner. No workspace-wide pool exists to fail over within: pools are each member's own policy.
- **A workspace that is not linked** (D2819) has no roster and gets none of the personal half.

## 7. Settled by the review

- **U1.** The workspace's deployment has no person-prefix intake. Settled: no.
- **U2.** The provider route cannot serve OpenRouter as it stands: the upstream half works, and
  selection refuses the tier until Check 50. Settled: not yet.
- **U3.** The workspace's service has no launcher it can trust. Settled: no.
- **U4.** The owner's shared list is not written by any command. Settled: there is no writer.
